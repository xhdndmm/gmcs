package server

import (
	"context"
	"log/slog"
	"math"
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
// 区块按由近到远的顺序发送（环序），然后卸载超出 视距+2 的区块。
func (s *session) syncChunks(centerX, centerZ int) error {
	radius := s.viewDistance()
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
				chunk, err := s.server.world.Chunk(pos.X, pos.Z)
				if err != nil {
					return err
				}
				// 复用编码缓冲：写出是同步的，下一区块可安全覆盖。
				s.chunkSendBuf = world.AppendChunkDataPacket(s.chunkSendBuf[:0], chunk)
				if err := s.writePacket(s.chunkSendBuf); err != nil {
					return err
				}
				s.sentChunks[pos] = struct{}{}
			}
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

// 区块内存卸载：长时间跑图会让世界内存缓存不断增长，服务器周期性地把
// 距所有玩家都超过 视距+chunkUnloadMargin 的区块移出内存缓存；
// 其中未保存的区块会先写入磁盘，玩家再次接近时重新读取或重新生成。
const (
	// chunkUnloadInterval 是卸载扫描的周期。
	chunkUnloadInterval = 5 * time.Second
	// chunkUnloadMargin 是卸载半径相对视距的余量（区块）。
	// 大于会话侧的卸载余量（视距+2），避免刚发出的区块立刻被移出缓存。
	chunkUnloadMargin = 4
)

// chunkUnloadLoop 周期性卸载远离玩家的区块，直到 ctx 取消。
func (s *Server) chunkUnloadLoop(ctx context.Context) {
	ticker := time.NewTicker(chunkUnloadInterval)
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

// unloadFarChunks 卸载距所有已加入玩家都超出 视距+chunkUnloadMargin 的区块。
// 服务器上没有玩家时不卸载（避免反复生成/读取刚探索过的区域）。
func (s *Server) unloadFarChunks() error {
	players := s.playerSnapshot()
	centers := make([]world.ChunkPos, 0, len(players))
	for _, player := range players {
		if !player.isJoined() {
			continue
		}
		x, _, z, _, _ := player.playerPosition()
		centers = append(centers, world.ChunkPos{
			X: int(math.Floor(x)) >> 4,
			Z: int(math.Floor(z)) >> 4,
		})
	}
	if len(centers) == 0 {
		return nil
	}
	unloaded, err := s.world.UnloadFar(centers, s.config.ViewDistance+chunkUnloadMargin)
	if err != nil {
		return err
	}
	if unloaded > 0 {
		slog.Debug("chunks unloaded", "count", unloaded)
	}
	return nil
}
