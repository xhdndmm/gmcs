//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"log/slog"
	"time"

	"gmcs/internal/world"
)

// 空闲资源逐级释放。
//
// 最后一名玩家退出后，服务器先按原有逻辑立即卸载区块（保证内存快速回落），
// 随后按时间分阶段释放运行时缓存、持久化实体和未使用的维度：
//
//	阶段 1（默认 30s）：漏斗/熔炉/酿造/农业/红石/流体/容器等运行时索引，
//	                     区块帧缓存和实体跟踪表；
//	阶段 2（默认 2m）：  保存生物/掉落物/箭矢/经验球后清空实体表；
//	阶段 3（默认 5m）：  关闭并移除下界/末地等非主世界维度，释放世界对象、
//	                     生成器池、区域文件句柄与区块 worker；
//	阶段 4（默认 15m）： 关闭包括主世界在内的全部维度；玩家再次加入时按需重建。
//
// 有玩家重新加入时 idle 计时归零；下一阶段不会继续执行。阶段时间可在测试中
// 直接覆盖，生产默认值在 newServer 中设置。

// resetIdleRelease 在玩家加入时调用，取消当前的空闲释放进度。
func (s *Server) resetIdleRelease() {
	s.idleMu.Lock()
	s.idleSince = time.Time{}
	s.idleStage = 0
	s.idleMu.Unlock()
}

// advanceIdleRelease 推进空闲释放阶段。调用方应在“当前没有已加入玩家”时
// 调用；now 可注入测试时间。
func (s *Server) advanceIdleRelease(now time.Time) {
	s.idleReleaseMu.Lock()
	defer s.idleReleaseMu.Unlock()
	s.idleMu.Lock()
	if s.idleSince.IsZero() {
		s.idleSince = now
	}
	elapsed := now.Sub(s.idleSince)
	desired := s.idleStage
	if s.idleStage1Delay > 0 && elapsed >= s.idleStage1Delay && desired < 1 {
		desired = 1
	}
	if s.idleStage2Delay > 0 && elapsed >= s.idleStage2Delay && desired < 2 {
		desired = 2
	}
	if s.idleStage3Delay > 0 && elapsed >= s.idleStage3Delay && desired < 3 {
		desired = 3
	}
	if s.idleStage4Delay > 0 && elapsed >= s.idleStage4Delay && desired < 4 {
		desired = 4
	}
	if desired <= s.idleStage {
		s.idleMu.Unlock()
		return
	}
	from := s.idleStage
	s.idleStage = desired
	s.idleMu.Unlock()

	for stage := from + 1; stage <= desired; stage++ {
		if s.hasJoinedIdlePlayers() {
			// 玩家在阶段之间加入：取消后续释放，避免清空活跃交互状态。
			s.resetIdleRelease()
			return
		}
		switch stage {
		case 1:
			s.releaseIdleRuntimeCaches()
			slog.Info("空闲服务器释放运行时缓存", "stage", 1)
		case 2:
			if err := s.releaseIdleEntities(); err != nil {
				slog.Error("空闲服务器释放实体失败", "stage", 2, "error", err)
			} else {
				slog.Info("空闲服务器释放实体缓存", "stage", 2)
			}
		case 3:
			s.releaseIdleWorldDimensions(false)
			slog.Info("空闲服务器关闭空闲维度", "stage", 3)
		case 4:
			s.releaseIdleWorldDimensions(true)
			slog.Info("空闲服务器关闭全部维度", "stage", 4)
		}
	}
}

// hasJoinedIdlePlayers 报告当前是否已有完成加入流程的玩家。
func (s *Server) hasJoinedIdlePlayers() bool {
	for _, player := range s.playerSnapshot() {
		if player.isJoined() {
			return true
		}
	}
	return false
}

// releaseIdleRuntimeCaches 清除只对在线交互有意义的运行时索引。区块本身已经
// 在 unloadAllChunks 中释放；这里避免这些索引继续持有已卸载区块的坐标/状态。
func (s *Server) releaseIdleRuntimeCaches() {
	s.hopperMu.Lock()
	clear(s.hoppers)
	s.hopperMu.Unlock()

	s.farmMu.Lock()
	clear(s.crops)
	clear(s.farmlands)
	s.farmCropScratch = nil
	s.farmFarmlandScratch = nil
	s.farmMu.Unlock()

	s.furnaceMu.Lock()
	clear(s.furnaces)
	s.furnaceMu.Unlock()

	s.brewingMu.Lock()
	clear(s.brewings)
	s.brewingMu.Unlock()

	s.composterMu.Lock()
	clear(s.composterDue)
	s.composterScanCounter = 0
	s.composterMu.Unlock()

	s.redstoneMu.Lock()
	clear(s.redstoneTicks)
	clear(s.torchToggles)
	s.redstoneMu.Unlock()

	s.fluidMu.Lock()
	clear(s.fluidTicks)
	s.fluidMu.Unlock()

	s.containerMu.Lock()
	clear(s.containers)
	s.containerMu.Unlock()

	s.entityMu.Lock()
	clear(s.entityTrack)
	s.entityMu.Unlock()

	// 区块帧缓存持有 *world.Chunk；空闲时清空 entries/order 让 GC 回收。
	// inflight 可能仍有一个正在编码的调用，保留其完成信号并由原调用清理。
	s.chunkPackets.mu.Lock()
	s.chunkPackets.order = nil
	s.chunkPackets.entries = nil
	s.chunkPackets.mu.Unlock()
}

// releaseIdleEntities 先保存可持久化实体，再清空内存实体表。保存失败时
// 保留实体，避免空闲释放造成数据丢失。
func (s *Server) releaseIdleEntities() error {
	if s.hasJoinedIdlePlayers() {
		return nil
	}
	if s.mobsEnabled || s.itemsEnabled {
		if err := s.saveEntities(); err != nil {
			return err
		}
	}
	if s.hasJoinedIdlePlayers() {
		// 保存期间有玩家加入：保留实体，下个空闲周期重新判断。
		return nil
	}
	s.entityMu.Lock()
	clear(s.mobs)
	clear(s.items)
	clear(s.arrows)
	clear(s.tnts)
	clear(s.xpOrbs)
	clear(s.lightningBolts)
	// 展示框/末影龙不是 entities.json 的持久化对象；保留它们，避免重新进入
	// 末地时丢失结构生成的展示框或 Boss 状态。
	s.entityMu.Unlock()

	// 实体文件已落盘；清空“已加载”标记，玩家重新进入维度时会重新读取。
	s.entityLoadMu.Lock()
	s.entitiesLoaded = make(map[world.Dimension]bool)
	s.entityLoadMu.Unlock()
	return nil
}

// releaseIdleWorldDimensions 关闭并移除没有玩家的维度。includeOverworld 为 false
// 时保留主世界；为 true 时连主世界也关闭（阶段 4）。维度会在下次访问时
// 通过 worldFor 重新创建并加载实体。
func (s *Server) releaseIdleWorldDimensions(includeOverworld bool) {
	if s.hasJoinedIdlePlayers() {
		return
	}
	type target struct {
		dim   world.Dimension
		world *world.World
	}
	s.worldMu.Lock()
	targets := make([]target, 0, len(s.worlds))
	for dim, gameWorld := range s.worlds {
		if dim == world.DimensionOverworld && !includeOverworld {
			continue
		}
		targets = append(targets, target{dim: dim, world: gameWorld})
	}
	s.worldMu.Unlock()

	for _, item := range targets {
		if err := item.world.Close(); err != nil {
			slog.Error("关闭空闲维度失败", "dimension", item.dim, "error", err)
			continue
		}
		s.worldMu.Lock()
		if s.worlds[item.dim] == item.world {
			delete(s.worlds, item.dim)
		}
		s.entityLoadMu.Lock()
		delete(s.entitiesLoaded, item.dim)
		s.entityLoadMu.Unlock()
		s.publishWorldsCacheLocked()
		s.worldMu.Unlock()
	}
	world.ReleaseRegionHandles()
}
