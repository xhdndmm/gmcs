//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

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
	//
	// 预取范围比实际发送范围多一圈（视距+1）：玩家跨区块时，新环带的
	// 区块（下一次发送的对象）已在后台加载/生成完毕，发送路径无需等待
	// 磁盘/生成器（额外的一圈都仍在卸载半径（视距+4）内，不会被重复
	// 卸载/加载）。
	prefetchRadius := radius + 1
	prefetch := make([]world.ChunkPos, 0, (2*prefetchRadius+1)*(2*prefetchRadius+1))
	for ring := 0; ring <= prefetchRadius; ring++ {
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
// 并发的同一区块未命中请求经 inflight 合并（singleflight）：同时进入
// 世界的多名玩家不会对同一区块重复编码与压缩。
type chunkPacketCache struct {
	mu      sync.Mutex
	entries map[chunkPacketKey]*list.Element
	order   *list.List // 前端最新
	// hits/misses 为观测计数：hits = 复用缓存次数，misses = 实际编码次数。
	hits   atomic.Uint64
	misses atomic.Uint64
	// inflight 是进行中的编码（值为完成信号）；等待者醒来后重查缓存。
	inflight map[chunkPacketKey]chan struct{}
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

// chunkPacketCacheMax 是缓存的区块包上限。压缩帧约 0.5–2 KiB/项，4096 项
// 上限约 4–8 MiB，且能覆盖多名玩家的完整视距（加入风暴后近 100% 命中）；
// 小于单个玩家的区块数会随移动反复失效重编码（实测显著劣化）。
const chunkPacketCacheMax = 4096

// chunkPacket 返回区块数据包：命中缓存时复用同一份只读字节，
// 否则编码一次并缓存。同一区块的并发未命中经 singleflight 合并：
// 等待者不重复编码，领取同一份共享帧。
func (s *Server) chunkPacket(dim world.Dimension, pos world.ChunkPos, chunk *world.Chunk) []byte {
	key := chunkPacketKey{dim: dim, pos: pos}
	for {
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
		// 同一区块的编码正在进行：等待结果后重查缓存。
		if wait, inflight := s.chunkPackets.inflight[key]; inflight {
			s.chunkPackets.mu.Unlock()
			<-wait
			continue
		}
		if s.chunkPackets.inflight == nil {
			s.chunkPackets.inflight = make(map[chunkPacketKey]chan struct{})
		}
		done := make(chan struct{})
		s.chunkPackets.inflight[key] = done
		s.chunkPackets.mu.Unlock()
		s.chunkPackets.misses.Add(1)

		buffer := chunkEncodePool.Get().(*[]byte)
		packet := world.AppendChunkDataPacket((*buffer)[:0], chunk)
		frame, err := protocol.CompressPacketFrame(packet, compressionThreshold)
		chunkEncodePool.Put(buffer)

		s.chunkPackets.mu.Lock()
		if err != nil {
			delete(s.chunkPackets.inflight, key)
			close(done)
			s.chunkPackets.mu.Unlock()
			// 构造失败只影响该次发送（帧长度受限，实际不可能发生）；
			// 等待者会被唤醒并重试。
			slog.Error("构造区块帧失败", "error", err)
			return nil
		}
		entry := &chunkPacketEntry{key: key, chunk: chunk, rev: rev, frame: frame}
		s.chunkPackets.entries[key] = s.chunkPackets.order.PushFront(entry)
		if s.chunkPackets.order.Len() > chunkPacketCacheMax {
			oldest := s.chunkPackets.order.Back()
			s.chunkPackets.order.Remove(oldest)
			delete(s.chunkPackets.entries, oldest.Value.(*chunkPacketEntry).key)
		}
		delete(s.chunkPackets.inflight, key)
		close(done)
		s.chunkPackets.mu.Unlock()
		return frame
	}
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
	// defaultChunkUnloadGrace 是区块卸载的默认宽限期：区块连续远离所有
	// 玩家超过该时长才真正卸载。玩家在视距边缘来回移动（幅度小于阈值、
	// 时间小于宽限）时不会触发反复的卸载→重加载→写盘。
	defaultChunkUnloadGrace = 15 * time.Second
	// defaultDimensionIdleGrace 是无玩家维度释放区块前的默认空闲宽限。
	defaultDimensionIdleGrace = 30 * time.Second
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
		case <-s.unloadNow:
			// 最后一名玩家退出：立即释放区块资源，不必等下一个扫描周期。
			if err := s.unloadFarChunks(); err != nil {
				slog.Error("区块卸载失败", "error", err)
			}
		}
	}
}

// unloadFarChunks 卸载距所有已加入玩家都超出 视距+chunkUnloadMargin 的区块
// （按维度分别计算中心点；连续超出 chunkUnloadGrace 宽限期才真正卸载，
// 防止玩家在卸载边界来回移动时反复加载/卸载/写盘）。
// 服务器没有任何已加入玩家时，卸载全部维度的全部区块并驱逐区块帧缓存
// （释放内存；下次进入时按需从磁盘加载或重新生成）。
// 已打开但当前无玩家的维度在空闲超过 dimensionIdleGrace 后同样释放全部
// 区块（玩家切维度/传送后旧维度不会长期占用内存）。
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
		// 服务器没有已加入玩家：释放全部区块（含区块帧缓存），
		// 避免空闲服务器长期占用跑图积攒的区块内存。
		return s.unloadAllChunks()
	}
	var firstErr error
	for dim, dimCenters := range centers {
		gameWorld := s.worldFor(dim)
		unloaded, err := gameWorld.UnloadFar(dimCenters, s.config.ViewDistance+chunkUnloadMargin, s.chunkUnloadGrace)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		s.releaseChunkCaches(dim, unloaded)
		if len(unloaded) > 0 {
			slog.Debug("chunks unloaded", "dimension", dim, "count", len(unloaded))
		}
	}
	s.releaseIdleDimensions(centers)
	return firstErr
}

// releaseChunkCaches 在区块卸载后丢弃相关运行时缓存：
// 区块帧缓存持有 *world.Chunk 指针，不驱逐会阻止已卸载区块被 GC
// （内存随探索持续增长）；漏斗注册同理需要丢弃（内容已随卸载写盘，
// 重新加载时重新读取）。
func (s *Server) releaseChunkCaches(dim world.Dimension, unloaded []world.ChunkPos) {
	if len(unloaded) == 0 {
		return
	}
	s.dropHoppersInChunks(dim, unloaded)
	s.dropFarmBlocksInChunks(dim, unloaded)
	for _, pos := range unloaded {
		s.evictChunkPacket(dim, pos)
	}
}

// releaseIdleDimensions 释放"当前没有任何已加入玩家的已打开维度"的
// 全部区块：维度首次变为空闲时只记录时间，空闲超过 dimensionIdleGrace
// 后才真正释放（玩家快速切回维度时无需重建）；宽限 <= 0 时立即释放。
// active 是有玩家维度的中心点集合。
func (s *Server) releaseIdleDimensions(active map[world.Dimension][]world.ChunkPos) {
	s.worldMu.RLock()
	worlds := make(map[world.Dimension]*world.World, len(s.worlds))
	for dim, gameWorld := range s.worlds {
		worlds[dim] = gameWorld
	}
	s.worldMu.RUnlock()

	now := time.Now()
	type releaseTarget struct {
		dim       world.Dimension
		gameWorld *world.World
	}
	var releases []releaseTarget
	s.dimIdleMu.Lock()
	if s.dimIdleSince == nil {
		s.dimIdleSince = make(map[world.Dimension]time.Time, len(worlds))
	}
	for dim, gameWorld := range worlds {
		if _, inUse := active[dim]; inUse {
			delete(s.dimIdleSince, dim)
			continue
		}
		if s.dimensionIdleGrace <= 0 {
			releases = append(releases, releaseTarget{dim: dim, gameWorld: gameWorld})
			delete(s.dimIdleSince, dim)
			continue
		}
		since, tracked := s.dimIdleSince[dim]
		if !tracked {
			// 首次观察到空闲：只记录时间，给玩家快速切回留出宽限。
			s.dimIdleSince[dim] = now
			continue
		}
		if now.Sub(since) < s.dimensionIdleGrace {
			continue
		}
		releases = append(releases, releaseTarget{dim: dim, gameWorld: gameWorld})
		delete(s.dimIdleSince, dim)
	}
	s.dimIdleMu.Unlock()

	for _, target := range releases {
		unloaded, err := target.gameWorld.UnloadAll()
		if err != nil {
			slog.Error("空闲维度区块释放失败", "dimension", target.dim, "error", err)
			continue
		}
		s.releaseChunkCaches(target.dim, unloaded)
		if len(unloaded) > 0 {
			slog.Debug("空闲维度区块已释放", "dimension", target.dim, "count", len(unloaded))
		}
	}
}

// evictChunkPacket 从区块帧缓存移除指定位置（区块卸载时调用）。
func (s *Server) evictChunkPacket(dim world.Dimension, pos world.ChunkPos) {
	key := chunkPacketKey{dim: dim, pos: pos}
	s.chunkPackets.mu.Lock()
	if elem, ok := s.chunkPackets.entries[key]; ok {
		s.chunkPackets.order.Remove(elem)
		delete(s.chunkPackets.entries, key)
	}
	s.chunkPackets.mu.Unlock()
}

// unloadAllChunks 卸载所有已打开维度的全部区块并驱逐区块帧缓存；
// 服务器没有玩家时由卸载循环调用（释放内存资源）。
func (s *Server) unloadAllChunks() error {
	s.worldMu.Lock()
	worlds := make(map[world.Dimension]*world.World, len(s.worlds))
	for dim, gameWorld := range s.worlds {
		worlds[dim] = gameWorld
	}
	s.worldMu.Unlock()

	var firstErr error
	for dim, gameWorld := range worlds {
		unloaded, err := gameWorld.UnloadAll()
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		s.releaseChunkCaches(dim, unloaded)
		if len(unloaded) > 0 {
			slog.Info("空闲服务器释放区块", "dimension", dim, "count", len(unloaded))
		}
	}
	// 空闲服务器不再持有区域文件句柄（fd 交给 GC 回收）。
	world.ReleaseRegionHandles()
	return firstErr
}
