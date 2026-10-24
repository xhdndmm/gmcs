//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 可开合方块：门、活板门、栅栏门的放置与红石联动。
//
// 对照 1.21.11 服务端字节码：
//   - FenceGateBlock.getStateForPlacement：facing = 玩家水平朝向，
//     open/powered = 放置位置是否有邻居信号；in_wall 由相邻墙判定（未实现，
//     恒为 false）；
//   - DoorBlock/TrapDoorBlock/FenceGateBlock.neighborChanged：powered 变化时
//     把 powered 与 open 一起设置；门的两半由 updateShape 同步。
//
// 已知近似（见 docs/TODO.md）：栅栏门 in_wall 恒为 false；开合不广播音效
// （客户端自身预测播放）。

// isFenceGateState 报告方块状态是否为栅栏门。
func isFenceGateState(state uint16) bool {
	name, ok := registry.BlockNameByState(state)
	if !ok {
		return false
	}
	_, ok = registry.PlacementFenceGates[name]
	return ok
}

// isDoorState 报告方块状态是否为门。
func isDoorState(state uint16) bool {
	name, ok := registry.BlockNameByState(state)
	if !ok {
		return false
	}
	_, ok = registry.PlacementDoors[name]
	return ok
}

// isTrapdoorState 报告方块状态是否为活板门。
func isTrapdoorState(state uint16) bool {
	name, ok := registry.BlockNameByState(state)
	if !ok {
		return false
	}
	_, ok = registry.PlacementTrapdoors[name]
	return ok
}

// placeFenceGate 放置栅栏门（单格）。
func (s *Server) placeFenceGate(player *session, w *world.World, dim world.Dimension,
	x, y, z int, name string, yaw float32) bool {
	closed, ok := registry.PlacementFenceGates[name]
	if !ok {
		return false
	}
	facingIndex := horizontalFacingIndex(yawToFacing(yaw))
	state := closed[facingIndex]
	if s.bestNeighborSignal(w, [3]int{x, y, z}) > 0 {
		if powered, ok := registry.PlacementFenceGatesPowered[name]; ok {
			state = powered[facingIndex]
		}
	}
	if !w.SetBlock(x, y, z, state) {
		return false
	}
	s.broadcastBlockUpdate(dim, x, y, z, int32(state))
	s.updateRedstoneAround(dim, x, y, z)
	s.updateFluidsAround(dim, x, y, z)
	s.consumeHeldItem(player)
	return true
}

// toggleFenceGate 处理右键开合栅栏门。
func (s *Server) toggleFenceGate(player *session, w *world.World, use protocol.UseItemOn, current uint16) bool {
	name, ok := s.blockNameOf(current)
	if !ok {
		return false
	}
	closed, ok := registry.PlacementFenceGates[name]
	if !ok {
		return false
	}
	open, ok := registry.PlacementFenceGatesOpen[name]
	if !ok {
		return false
	}
	powered, ok := registry.PlacementFenceGatesPowered[name]
	if !ok {
		return false
	}
	next, found := uint16(0), false
	for facing := 0; facing < 4 && !found; facing++ {
		switch current {
		case closed[facing]:
			next, found = open[facing], true
		case open[facing], powered[facing]:
			next, found = closed[facing], true
		}
	}
	if !found {
		return false
	}
	if !w.SetBlock(use.X, use.Y, use.Z, next) {
		return false
	}
	s.broadcastBlockUpdate(player.dimensionID(), use.X, use.Y, use.Z, int32(next))
	return true
}

// refreshOpenable 按邻居信号更新门/活板门/栅栏门的 open/powered，
// 对应原版 xxxBlock.neighborChanged（powered 变化时同步 open）。
func (s *Server) refreshOpenable(w *world.World, dim world.Dimension, pos [3]int, state uint16) {
	name, ok := s.blockNameOf(state)
	if !ok {
		return
	}
	signal := s.bestNeighborSignal(w, pos) > 0
	if table, ok := registry.PlacementFenceGates[name]; ok {
		s.refreshFenceGate(w, dim, pos, state, signal, table)
		return
	}
	if table, ok := registry.PlacementTrapdoors[name]; ok {
		s.refreshTrapdoor(w, dim, pos, state, signal, table)
		return
	}
	if table, ok := registry.PlacementDoors[name]; ok {
		s.refreshDoor(w, dim, pos, state, signal, table)
	}
}

// variantOf 返回状态在关闭/打开/通电三种变体中的下标；-1 表示未识别。
func variantOf(state uint16, closed, open, powered uint16) int {
	switch state {
	case closed:
		return 0
	case open:
		return 1
	case powered:
		return 2
	}
	return -1
}

// refreshFenceGate 更新栅栏门。
func (s *Server) refreshFenceGate(w *world.World, dim world.Dimension, pos [3]int,
	state uint16, signal bool, closed [4]uint16) {
	name, _ := s.blockNameOf(state)
	open, ok := registry.PlacementFenceGatesOpen[name]
	if !ok {
		return
	}
	powered, ok := registry.PlacementFenceGatesPowered[name]
	if !ok {
		return
	}
	for facing := 0; facing < 4; facing++ {
		variant := variantOf(state, closed[facing], open[facing], powered[facing])
		if variant < 0 {
			continue
		}
		// 变体与信号一致时无需改动（手动打开且未通电也保持不变）。
		if (variant == 2) == signal {
			return
		}
		next := closed[facing]
		if signal {
			next = powered[facing]
		}
		if w.SetBlock(pos[0], pos[1], pos[2], next) {
			s.broadcastBlockUpdate(dim, pos[0], pos[1], pos[2], int32(next))
		}
		return
	}
}

// refreshTrapdoor 更新活板门。
func (s *Server) refreshTrapdoor(w *world.World, dim world.Dimension, pos [3]int,
	state uint16, signal bool, closed [2][4]uint16) {
	name, _ := s.blockNameOf(state)
	open, ok := registry.PlacementTrapdoorsOpen[name]
	if !ok {
		return
	}
	powered, ok := registry.PlacementTrapdoorsPowered[name]
	if !ok {
		return
	}
	for half := 0; half < 2; half++ {
		for facing := 0; facing < 4; facing++ {
			variant := variantOf(state, closed[half][facing], open[half][facing], powered[half][facing])
			if variant < 0 {
				continue
			}
			if (variant == 2) == signal {
				return
			}
			next := closed[half][facing]
			if signal {
				next = powered[half][facing]
			}
			if w.SetBlock(pos[0], pos[1], pos[2], next) {
				s.broadcastBlockUpdate(dim, pos[0], pos[1], pos[2], int32(next))
			}
			return
		}
	}
}

// refreshDoor 更新门的上下两格。
func (s *Server) refreshDoor(w *world.World, dim world.Dimension, pos [3]int,
	state uint16, signal bool, closed [2][4][2]uint16) {
	name, _ := s.blockNameOf(state)
	open, ok := registry.PlacementDoorsOpen[name]
	if !ok {
		return
	}
	powered, ok := registry.PlacementDoorsPowered[name]
	if !ok {
		return
	}
	half, ok := doorHalfOf(name, state)
	if !ok {
		return
	}
	for facing := 0; facing < 4; facing++ {
		for hinge := 0; hinge < 2; hinge++ {
			variant := variantOf(state, closed[half][facing][hinge],
				open[half][facing][hinge], powered[half][facing][hinge])
			if variant < 0 {
				continue
			}
			if (variant == 2) == signal {
				return
			}
			table := closed
			if signal {
				table = powered
			}
			baseY := pos[1]
			if half == 1 {
				baseY = pos[1] - 1
			}
			lower, upper := table[0][facing][hinge], table[1][facing][hinge]
			if w.SetBlock(pos[0], baseY, pos[2], lower) {
				s.broadcastBlockUpdate(dim, pos[0], baseY, pos[2], int32(lower))
			}
			if w.SetBlock(pos[0], baseY+1, pos[2], upper) {
				s.broadcastBlockUpdate(dim, pos[0], baseY+1, pos[2], int32(upper))
			}
			return
		}
	}
}
