package server

import (
	"container/list"
	"context"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"gmcs/internal/protocol"
	"gmcs/internal/world"
)

// 区块流式加载：玩家视距内的区块持续保持已发送状态；
// 跨区块移动时增量发送新区块，并卸载超出 视距+2 的旧区块。
//
// 状态（sentChunks/centerChunk）由会话 goroutine 串行访问：
// 进入世界、移动处理、重生流程都在同一 goroutine 中执行。

// viewDistance 返回该会话使用的区块视距（正方形半径）。
func (s *session) viewDistance() int {
	return s.server.config.ViewDistance
}

// syncChunks 确保以 (centerX, centerZ) 为中心、半径为视距的区块均已发送，
// 区块按由近到远的顺序发送（环序）；新发送的区块用 Chunk Batch Start/Finished
// 标注为一批（客户端据此估算加载速度并回报期望速率，见
// ServerboundChunkBatchReceived）。最后卸载超出 视距+2 的区块。
//
// 注意：与速度节流不同，当前实现仍在一批内立即发送全部区块（不按客户端
// 回报的 chunksPerTick 暂停），重连/重生后的大量区块会一次性写出。
func (s *session) syncChunks(centerX, centerZ int) error {
	radius := s.viewDistance()
	dim := s.dimensionID()
	gameWorld := s.server.worldFor(dim)

	// 先把视距内尚未发送的区块按由近到远提交给异步预生成 worker 池：
	// 多个区块并行生成，随后逐环发送时依次等待各自结果（首个环通常
	// 已经生成完毕，显著缩短进入世界的等待时间）。
	prefetch := make([]world.ChunkPos, 0, (2*radius+1)*(2*radius+1))
	for ring := 0; ring <= radius; ring++ {
		for dx := -ring; dx <= ring; dx++ {
			for dz := -ring; dz <= ring; dz++ {
				if max(absInt(dx), absInt(dz)) != ring {
					continue
				}
				pos := world.ChunkPos{X: centerX + dx, Z: centerZ + dz}
				if _, ok := s.sentChunks[pos]; ok {
					continue
				}
				prefetch = append(prefetch, pos)
			}
		}
	}
	if len(prefetch) > 0 {
		gameWorld.Prefetch(prefetch)
	}

	batch := 0
	for ring := 0; ring <= radius; ring++ {
		for dx := -ring; dx <= ring; dx++ {
			for dz := -ring; dz <= ring; dz++ {
				if max(absInt(dx), absInt(dz)) != ring {
					continue
				}
				pos := world.ChunkPos{X: centerX + dx, Z: centerZ + dz}
				if _, ok := s.sentChunks[pos]; ok {
					continue
				}
				chunk, err := gameWorld.Chunk(pos.X, pos.Z)
				if err != nil {
					return err
				}
				if batch == 0 {
					if err := s.writePacket(protocol.EncodeChunkBatchStart()); err != nil {
						return err
					}
				}
				// 共享压缩帧：同一批/跟随的多名玩家复用同一份只读字节，
				// 免去逐连接重复编码与压缩。
				frame := s.server.chunkPacket(dim, pos, chunk)
				if err := s.writeFrame(frame); err != nil {
					return err
				}
				s.sentChunks[pos] = struct{}{}
				batch++
			}
		}
	}
	if batch > 0 {
		if err := s.writePacket(protocol.EncodeChunkBatchFinished(int32(batch))); err != nil {
			return err
		}
	}
	limit := radius + 2
	for pos := range s.sentChunks {
		if absInt(pos.X-centerX) > limit || absInt(pos.Z-centerZ) > limit {
			if err := s.writePacket(protocol.EncodeForgetLevelChunk(int32(pos.X), int32(pos.Z))); err != nil {
				return err
			}
			delete(s.sentChunks, pos)
		}
	}
	s.centerChunk = world.ChunkPos{X: centerX, Z: centerZ}
	return nil
}

// updateChunks 在玩家跨越区块边界时增量同步区块；写失败时关闭连接。
func (s *session) updateChunks(x, z float64) {
	centerX := int(math.Floor(x)) >> 4
	centerZ := int(math.Floor(z)) >> 4
	if centerX == s.centerChunk.X && centerZ == s.centerChunk.Z {
		return
	}
	if err := s.writePacket(protocol.EncodeSetCenterChunk(int32(centerX), int32(centerZ))); err != nil {
		_ = s.conn.Close()
		return
	}
	if err := s.syncChunks(centerX, centerZ); err != nil {
		slog.Debug("chunk streaming failed", "name", s.name, "error", err)
		_ = s.conn.Close()
	}
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

type chunkPacketKey struct {
	dim world.Dimension
	pos world.ChunkPos
}

// chunkPacketCache 是跨玩家共享的区块数据包缓存：多名玩家看到同一区块时
// 只编码一次，后续直接复用同一份只读字节（并发读安全）。按 LRU 有界；
// 条目同时记录来源 *Chunk 与修改计数（revision），区块卸载重载或内容
// 修改后自动失效。键包含维度（不同维度可能有相同区块坐标）。
type chunkPacketCache struct {
	mu      sync.Mutex
	entries map[chunkPacketKey]*list.Element
	order   *list.List // 前端最新
	hits    atomic.Uint64
	misses  atomic.Uint64
}

// chunkEncodePool 复用区块编码缓冲（64 KiB/次），避免每次缓存未命中
// 都分配大缓冲（加入风暴时的分配峰值）。
var chunkEncodePool = sync.Pool{
	New: func() any {
		buffer := make([]byte, 0, 64*1024)
		return &buffer
	},
}

type chunkPacketEntry struct {
	key   chunkPacketKey
	chunk *world.Chunk
	rev   uint64
	// frame 是压缩后的完整帧（只读共享，跨连接直接写出）。
	frame []byte
}

// chunkPacketCacheMax 是缓存的区块包上限。压缩帧约 0.5–2 KiB/项，2048 项
// 上限约 2–4 MiB，且能覆盖多名玩家的完整视距（加入风暴后近 100% 命中）；
// 小于单个玩家的区块数会随移动反复失效重编码（实测显著劣化）。
const chunkPacketCacheMax = 2048

// chunkPacket 返回区块数据包：命中缓存时复用同一份只读字节，
// 否则编码一次并缓存。
func (s *Server) chunkPacket(dim world.Dimension, pos world.ChunkPos, chunk *world.Chunk) []byte {
	key := chunkPacketKey{dim: dim, pos: pos}
	rev := chunk.Revision()
	s.chunkPackets.mu.Lock()
	if s.chunkPackets.order == nil {
		s.chunkPackets.entries = make(map[chunkPacketKey]*list.Element)
		s.chunkPackets.order = list.New()
	}
	if elem, ok := s.chunkPackets.entries[key]; ok {
		entry := elem.Value.(*chunkPacketEntry)
		if entry.chunk == chunk && entry.rev == rev {
			s.chunkPackets.order.MoveToFront(elem)
			s.chunkPackets.mu.Unlock()
			s.chunkPackets.hits.Add(1)
			return entry.frame
		}
		s.chunkPackets.order.Remove(elem)
		delete(s.chunkPackets.entries, key)
	}
	s.chunkPackets.mu.Unlock()
	s.chunkPackets.misses.Add(1)

	buffer := chunkEncodePool.Get().(*[]byte)
	packet := world.AppendChunkDataPacket((*buffer)[:0], chunk)
	frame, err := protocol.CompressPacketFrame(packet, compressionThreshold)
	chunkEncodePool.Put(buffer)
	if err != nil {
		// 构造失败只影响该次发送（帧长度受限，实际不可能发生）。
		slog.Error("构造区块帧失败", "error", err)
		return nil
	}
	entry := &chunkPacketEntry{key: key, chunk: chunk, rev: rev, frame: frame}
	s.chunkPackets.mu.Lock()
	s.chunkPackets.entries[key] = s.chunkPackets.order.PushFront(entry)
	if s.chunkPackets.order.Len() > chunkPacketCacheMax {
		oldest := s.chunkPackets.order.Back()
		s.chunkPackets.order.Remove(oldest)
		delete(s.chunkPackets.entries, oldest.Value.(*chunkPacketEntry).key)
	}
	s.chunkPackets.mu.Unlock()
	return frame
}

// 区块内存卸载：长时间跑图会让世界内存缓存不断增长，服务器周期性地把
// 距所有玩家都超过 视距+chunkUnloadMargin 的区块移出内存缓存；
// 其中未保存的区块会先写入磁盘，玩家再次接近时重新读取或重新生成。
const (
	// defaultChunkUnloadInterval 是卸载扫描的默认周期（Server.chunkUnloadInterval
	// 可覆盖；测试将其调大以避免周期卸载干扰断言）。
	defaultChunkUnloadInterval = 5 * time.Second
	// chunkUnloadMargin 是卸载半径相对视距的余量（区块）。
	// 大于会话侧的卸载余量（视距+2），避免刚发出的区块立刻被移出缓存。
	chunkUnloadMargin = 4
)

// chunkUnloadLoop 周期性卸载远离玩家的区块，直到 ctx 取消。
// 间隔来自 Server.chunkUnloadInterval；<= 0 时禁用。
func (s *Server) chunkUnloadLoop(ctx context.Context) {
	interval := s.chunkUnloadInterval
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.unloadFarChunks(); err != nil {
				slog.Error("区块卸载失败", "error", err)
			}
		}
	}
}

// unloadFarChunks 卸载距所有已加入玩家都超出 视距+chunkUnloadMargin 的区块
// （按维度分别计算中心点）。服务器上没有玩家时不卸载（避免反复生成/读取
// 刚探索过的区域）。
func (s *Server) unloadFarChunks() error {
	players := s.playerSnapshot()
	centers := make(map[world.Dimension][]world.ChunkPos)
	for _, player := range players {
		if !player.isJoined() {
			continue
		}
		x, _, z, _, _ := player.playerPosition()
		dim := player.dimensionID()
		centers[dim] = append(centers[dim], world.ChunkPos{
			X: int(math.Floor(x)) >> 4,
			Z: int(math.Floor(z)) >> 4,
		})
	}
	if len(centers) == 0 {
		return nil
	}
	var firstErr error
	for dim, dimCenters := range centers {
		gameWorld := s.worldFor(dim)
		unloaded, err := gameWorld.UnloadFar(dimCenters, s.config.ViewDistance+chunkUnloadMargin)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if unloaded > 0 {
			slog.Debug("chunks unloaded", "dimension", dim, "count", unloaded)
		}
	}
	return firstErr
}
