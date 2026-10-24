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
//     open/powered = 放置位置是否有邻居信号，in_wall = 沿 facing 轴的前后
//     邻居是否为 `minecraft:walls` 标签方块（邻居变化时由 refreshOpenable
//     按 updateShape 语义重新计算）；
//   - DoorBlock/TrapDoorBlock/FenceGateBlock.neighborChanged：powered 变化时
//     把 powered 与 open 一起设置；门的两半由 updateShape 同步。
//
// 已知近似（见 docs/TODO.md）：门铰链未采用邻居双门/固体判定（仅按点击
// 位置回退）；开合音效按方块材质分组广播，不使用逐方块 BlockSetType 的完整数据。

// fenceGateFacingByIndex 返回栅栏门 facing 索引对应的方向名（与放置表一致：
// 0=north、1=south、2=west、3=east）。
func fenceGateFacingByIndex(index int) string {
	switch index {
	case 1:
		return "south"
	case 2:
		return "west"
	case 3:
		return "east"
	}
	return "north"
}

// fenceGateInWallIndex 返回栅栏门的 in_wall 索引（0/1）：沿 facing 轴的前后两个
// 邻居任一属于 `minecraft:walls` 标签即为 1。原版 FenceGateBlock 的
// getStateForPlacement 与 updateShape 都按 facing 轴判定。
// ok=false 表示两个邻居所在区块未全部加载，无法判定（调用方应保留现有值）。
func fenceGateInWallIndex(w *world.World, x, y, z, facingIndex int) (index int, ok bool) {
	pos := [3]int{x, y, z}
	dir := fenceGateFacingByIndex(facingIndex)
	front := offsetDir(pos, dir)
	back := offsetDir(pos, oppositeDir(dir))
	if !w.ChunkLoaded(front[0], front[2]) || !w.ChunkLoaded(back[0], back[2]) {
		return 0, false
	}
	if isWallState(w.BlockAt(front[0], front[1], front[2])) ||
		isWallState(w.BlockAt(back[0], back[1], back[2])) {
		return 1, true
	}
	return 0, true
}

// isWallState 报告方块状态是否属于 minecraft:walls 标签（原版 BlockTags.WALLS）。
func isWallState(state uint16) bool {
	return registry.BlockTagContains("minecraft:walls", state)
}

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
	// 原版 FenceGateBlock.getStateForPlacement：in_wall 由沿 facing 轴的两侧
	// 邻居是否为 `minecraft:walls` 标签方块决定。
	inWall, _ := fenceGateInWallIndex(w, x, y, z, facingIndex)
	state := closed[inWall][facingIndex]
	if s.bestNeighborSignal(w, [3]int{x, y, z}) > 0 {
		if powered, ok := registry.PlacementFenceGatesPowered[name]; ok {
			state = powered[inWall][facingIndex]
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
	opening := false
	// in_wall 在开合时保持不变（由邻块更新另行维护，与原版 useWithoutItem 一致）。
	for inWall := 0; inWall < 2 && !found; inWall++ {
		for facing := 0; facing < 4 && !found; facing++ {
			switch current {
			case closed[inWall][facing]:
				next, found, opening = open[inWall][facing], true, true
			case open[inWall][facing], powered[inWall][facing]:
				next, found, opening = closed[inWall][facing], true, false
			}
		}
	}
	if !found {
		return false
	}
	if !w.SetBlock(use.X, use.Y, use.Z, next) {
		return false
	}
	s.broadcastBlockUpdate(player.dimensionID(), use.X, use.Y, use.Z, int32(next))
	s.playFenceGateSound(player.dimensionID(), [3]int{use.X, use.Y, use.Z}, opening)
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

// refreshFenceGate 更新栅栏门：既按邻居信号维护 open/powered，也按沿 facing 轴
// 的墙体邻居维护 in_wall（原版 FenceGateBlock.neighborChanged + updateShape）。
func (s *Server) refreshFenceGate(w *world.World, dim world.Dimension, pos [3]int,
	state uint16, signal bool, closed [2][4]uint16) {
	name, _ := s.blockNameOf(state)
	open, ok := registry.PlacementFenceGatesOpen[name]
	if !ok {
		return
	}
	powered, ok := registry.PlacementFenceGatesPowered[name]
	if !ok {
		return
	}
	tables := [3][2][4]uint16{closed, open, powered}
	for inWall := 0; inWall < 2; inWall++ {
		for facing := 0; facing < 4; facing++ {
			variant := variantOf(state, closed[inWall][facing], open[inWall][facing], powered[inWall][facing])
			if variant < 0 {
				continue
			}
			// 目标变体：通电 → 通电打开；断电且当前是通电打开 → 关闭；
			// 其余情况（含手动打开且未通电）保持不变。
			target := variant
			switch {
			case signal && variant != 2:
				target = 2
			case !signal && variant == 2:
				target = 0
			}
			wantInWall := inWall
			if index, known := fenceGateInWallIndex(w, pos[0], pos[1], pos[2], facing); known {
				wantInWall = index
			}
			next := tables[target][wantInWall][facing]
			if next == state {
				return
			}
			if !w.SetBlock(pos[0], pos[1], pos[2], next) {
				return
			}
			s.broadcastBlockUpdate(dim, pos[0], pos[1], pos[2], int32(next))
			if target != variant {
				// 只有开合变化才播音效（in_wall 外观变化是静默的）。
				s.playFenceGateSound(dim, pos, target != 0)
			}
			return
		}
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
				s.playTrapdoorSound(dim, pos, name, signal)
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
			s.playDoorSound(dim, [3]int{pos[0], baseY, pos[2]}, name, signal)
			return
		}
	}
}
