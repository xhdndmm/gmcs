//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"
	"strings"

	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 本文件补充三类方块放置/更新语义：
//
//   - 台阶的邻居形状更新（原版 StairsBlock.getStairsShape）：
//     straight / inner_left / inner_right / outer_left / outer_right；
//   - 床（原版 BedBlock）：占两格，脚格为放置位置、头格在 facing 方向；
//   - 告示牌（原版 StandingSignBlock / WallSignBlock）：立式按玩家朝向取
//     rotation（0–15），贴墙时改为墙式变体并按点击面取 facing。

const (
	// 台阶 shape 索引，与原版 BlockState.Shape 枚举顺序一致。
	stairsStraight   = 0
	stairsInnerLeft  = 1
	stairsInnerRight = 2
	stairsOuterLeft  = 3
	stairsOuterRight = 4

	// 床的 part 索引。
	bedPartFoot = 0
	bedPartHead = 1
)

// stairsFacingOffsets 是水平 facing 索引对应的位移（0=north、1=south、2=west、3=east）。
var stairsFacingOffsets = [4][3]int{{0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0}}

// stairsCounterClockwise 等价 Direction.getCounterClockWise 的水平四向映射：
// north→west、west→south、south→east、east→north。
var stairsCounterClockwise = [4]int{2, 3, 1, 0}

// oppositeFacingIndex 返回水平朝向的反方向（north↔south、west↔east）。
func oppositeFacingIndex(facing int) int {
	return facing ^ 1
}

// facingAxisOf 返回水平朝向所在的轴：0=X（west/east）、1=Z（north/south）。
func facingAxisOf(facing int) int {
	if facing < 2 {
		return 1
	}
	return 0
}

// stairsStateParts 反查台阶状态对应的 (half, facing, shape)。
func stairsStateParts(name string, state uint16) (half, facing, shape int, ok bool) {
	table, exists := registry.PlacementStairsShape[name]
	if !exists {
		return 0, 0, 0, false
	}
	for h := 0; h < 2; h++ {
		for f := 0; f < 4; f++ {
			for s := 0; s < 5; s++ {
				if table[h][f][s] == state {
					return h, f, s, true
				}
			}
		}
	}
	return 0, 0, 0, false
}

// stairsPartsAt 返回该位置台阶的 (half, facing)，非台阶返回 ok=false。
func (s *Server) stairsPartsAt(w *world.World, x, y, z int) (half, facing int, ok bool) {
	state := w.BlockAt(x, y, z)
	name, hasName := s.blockNameOf(state)
	if !hasName {
		return 0, 0, false
	}
	half, facing, _, ok = stairsStateParts(name, state)
	return half, facing, ok
}

// canTakeStairsShape 等价原版 StairsBlock.canTakeShape：
// 目标方向上的方块不是台阶，或台阶的 facing/half 与本方块不同。
func (s *Server) canTakeStairsShape(w *world.World, x, y, z, half, facing, dir int) bool {
	nx := x + stairsFacingOffsets[dir][0]
	nz := z + stairsFacingOffsets[dir][2]
	otherHalf, otherFacing, ok := s.stairsPartsAt(w, nx, y, nz)
	return !ok || otherFacing != facing || otherHalf != half
}

// stairsShapeFor 等价原版 StairsBlock.getStairsShape。
func (s *Server) stairsShapeFor(w *world.World, x, y, z, half, facing int) int {
	// 前方（facing 方向）的台阶决定 outer_left / outer_right。
	ax := x + stairsFacingOffsets[facing][0]
	az := z + stairsFacingOffsets[facing][2]
	if otherHalf, otherFacing, ok := s.stairsPartsAt(w, ax, y, az); ok && otherHalf == half {
		if facingAxisOf(otherFacing) != facingAxisOf(facing) &&
			s.canTakeStairsShape(w, x, y, z, half, facing, oppositeFacingIndex(otherFacing)) {
			if otherFacing == stairsCounterClockwise[facing] {
				return stairsOuterLeft
			}
			return stairsOuterRight
		}
	}
	// 后方（facing 反方向）的台阶决定 inner_left / inner_right。
	back := oppositeFacingIndex(facing)
	bx := x + stairsFacingOffsets[back][0]
	bz := z + stairsFacingOffsets[back][2]
	if otherHalf, otherFacing, ok := s.stairsPartsAt(w, bx, y, bz); ok && otherHalf == half {
		if facingAxisOf(otherFacing) != facingAxisOf(facing) &&
			s.canTakeStairsShape(w, x, y, z, half, facing, otherFacing) {
			if otherFacing == stairsCounterClockwise[facing] {
				return stairsInnerLeft
			}
			return stairsInnerRight
		}
	}
	return stairsStraight
}

// refreshStairsShape 重算某格台阶的 shape 并广播结果（非台阶方块直接返回）。
func (s *Server) refreshStairsShape(dim world.Dimension, w *world.World, x, y, z int) {
	state := w.BlockAt(x, y, z)
	name, ok := s.blockNameOf(state)
	if !ok {
		return
	}
	table, ok := registry.PlacementStairsShape[name]
	if !ok {
		return
	}
	half, facing, shape, ok := stairsStateParts(name, state)
	if !ok {
		return
	}
	next := s.stairsShapeFor(w, x, y, z, half, facing)
	if next == shape {
		return
	}
	newState := table[half][facing][next]
	if w.SetBlock(x, y, z, newState) {
		s.broadcastBlockUpdate(dim, x, y, z, int32(newState))
	}
}

// updateStairsAround 在 (x, y, z) 变化后重算该格与其水平相邻（含对角）的台阶形状。
// 原版由 Block.updateShape 逐邻居触发，这里按同一层 3×3 邻域统一刷新。
func (s *Server) updateStairsAround(dim world.Dimension, w *world.World, x, y, z int) {
	if w == nil {
		return
	}
	for dz := -1; dz <= 1; dz++ {
		for dx := -1; dx <= 1; dx++ {
			s.refreshStairsShape(dim, w, x+dx, y, z+dz)
		}
	}
}

// placeBed 放置床：脚格放在 (x, y, z)、头格放在 facing 方向（原版
// BedBlock.getStateForPlacement + BedBlock.canSurvive 的两格校验）。
// 返回 true 表示已放置（调用方负责消耗物品）。
func (s *Server) placeBed(player *session, w *world.World, dim world.Dimension, x, y, z int, name string, yaw float32) bool {
	table, ok := registry.PlacementBeds[name]
	if !ok {
		return false
	}
	if _, ok := world.SectionIndex(y); !ok {
		return false
	}
	if _, ok := world.SectionIndex(y + 1); !ok {
		return false
	}
	facing := horizontalFacingIndex(yawToFacing(yaw))
	offset := stairsFacingOffsets[facing]
	headX, headZ := x+offset[0], z+offset[2]
	footState := table[facing][bedPartFoot]
	headState := table[facing][bedPartHead]
	if !replaceableForPlacement(w.BlockAt(x, y, z)) || !replaceableForPlacement(w.BlockAt(headX, y, headZ)) {
		return false
	}
	if !supportedBelow(w, x, y, z) || !supportedBelow(w, headX, y, headZ) {
		return false
	}
	if !w.SetBlock(x, y, z, footState) {
		return false
	}
	if !w.SetBlock(headX, y, headZ, headState) {
		w.SetBlock(x, y, z, world.AirBlock)
		return false
	}
	s.broadcastBlockUpdate(dim, x, y, z, int32(footState))
	s.broadcastBlockUpdate(dim, headX, y, headZ, int32(headState))
	s.updateRedstoneAround(dim, x, y, z)
	s.updateFluidsAround(dim, x, y, z)
	s.consumeHeldItem(player)
	return true
}

// removeBedPair 在破坏床的一格时同步移除另一格（原版 BedBlock.updateShape 的
// 掉落/移除语义；掉落由被破坏格的战利品表结算）。
func (s *Server) removeBedPair(w *world.World, dim world.Dimension, x, y, z int, state uint16) {
	name, ok := s.blockNameOf(state)
	if !ok {
		return
	}
	table, ok := registry.PlacementBeds[name]
	if !ok {
		return
	}
	facing, part, ok := bedPartsOf(table, state)
	if !ok {
		return
	}
	offset := stairsFacingOffsets[facing]
	siblingX, siblingZ := x+offset[0], z+offset[2]
	if part == bedPartHead {
		siblingX, siblingZ = x-offset[0], z-offset[2]
	}
	other := w.BlockAt(siblingX, y, siblingZ)
	otherName, ok := s.blockNameOf(other)
	if !ok || otherName != name {
		return
	}
	otherFacing, otherPart, ok := bedPartsOf(table, other)
	if !ok || otherPart == part || otherFacing != facing {
		return
	}
	if w.SetBlock(siblingX, y, siblingZ, world.AirBlock) {
		s.broadcastBlockUpdate(dim, siblingX, y, siblingZ, int32(world.AirBlock))
	}
}

// bedPartsOf 反查床状态对应的 (facing, part)。
func bedPartsOf(table [4][2]uint16, state uint16) (facing, part int, ok bool) {
	for f := 0; f < 4; f++ {
		for p := 0; p < 2; p++ {
			if table[f][p] == state {
				return f, p, true
			}
		}
	}
	return 0, 0, false
}

// signPlacement 返回告示牌的放置结果：贴墙时改用墙式变体（facing = 点击面），
// 否则用立式变体（rotation 由玩家 yaw 决定）。
func signPlacement(name string, yaw float32, use protocol.UseItemOn) (uint16, string, bool) {
	table, ok := registry.PlacementStandingSigns[name]
	if !ok {
		return 0, name, false
	}
	if use.Direction >= 2 {
		wallName, ok := wallSignVariant(name)
		if !ok {
			return 0, name, false
		}
		wallTable, ok := registry.PlacementWallSigns[wallName]
		if !ok {
			return 0, name, false
		}
		return wallTable[horizontalFacingIndex(faceNames[use.Direction])], wallName, true
	}
	// 原版 StandingSignBlock.getStateForPlacement：
	// rotation = floor((180 + yaw) * 16 / 360 + 0.5) & 15。
	rotation := int(math.Floor(float64((180.0+yaw)*16.0/360.0+0.5))) & 15
	return table[rotation], name, true
}

// wallSignVariant 返回立式告示牌对应的墙式变体名。
func wallSignVariant(name string) (string, bool) {
	switch {
	case strings.HasSuffix(name, "_hanging_sign"):
		return strings.TrimSuffix(name, "_hanging_sign") + "_wall_hanging_sign", true
	case strings.HasSuffix(name, "_sign"):
		return strings.TrimSuffix(name, "_sign") + "_wall_sign", true
	}
	return "", false
}

// replaceableForPlacement 报告该状态是否可被放置覆盖（空气/水/岩浆）。
func replaceableForPlacement(state uint16) bool {
	return state == world.AirBlock || registry.IsWaterState(state) || registry.IsLavaState(state)
}

// supportedBelow 报告 (x, y, z) 下方是否有可支撑的方块（用于床的 canSurvive 近似）。
func supportedBelow(w *world.World, x, y, z int) bool {
	below := w.BlockAt(x, y-1, z)
	if below == world.AirBlock || registry.IsWaterState(below) || registry.IsLavaState(below) {
		return false
	}
	return len(registry.BlockStateShape(below)) > 0
}
