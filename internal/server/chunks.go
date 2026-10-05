package server

import (
	"log/slog"
	"math"

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
				if err := s.writePacket(world.EncodeChunkDataPacket(chunk)); err != nil {
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
