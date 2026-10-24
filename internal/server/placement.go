//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"strings"

	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 方块放置形态：台阶、半砖、柱状方块（原木/玄武岩/锁链等）、朝向方块
// （箱子/熔炉/木桶/潜影盒等）、活板门、梯子与火把。
//
// 对照 1.21.11 服务端字节码：
//   - StairBlock.getStateForPlacement：facing = 玩家水平朝向，
//     half 由点击面/点击高度决定（state 表由生成器输出，shape=straight）；
//   - SlabBlock.getStateForPlacement：type 同上判定；点击单半砖时变为 double
//     （见 tryMergeSlab）；
//   - RotatedPillarBlock.getStateForPlacement：axis = 点击面的轴；
//   - HorizontalDirectionalBlock：facing = 水平朝向的反方向；
//   - 六向方块（木桶/潜影盒）：facing = 最近视线方向的反方向；
//   - TrapDoorBlock.getStateForPlacement：facing = 水平朝向的反方向，
//     half = 点击面为上时 bottom，否则 top；右键开合见 toggleTrapdoor；
//   - LadderBlock.getStateForPlacement：只能贴在水平面上，
//     facing = 点击面的反方向；
//   - WallOrFloorItem（火把）：点击顶面放地面火把、点击侧面放墙式火把
//     （facing = 点击面的反方向）、点击底面不能放置。
//
// 已知近似（见 docs/TODO.md）：台阶不做邻居形状更新（shape 恒为 straight）；
// 含水（waterlogged）一律为 false；活板门放置时不检查红石供电
// （原版供电时直接以打开状态放置）。

// torchWallVariants 是地面火把 → 墙式火把的映射。
var torchWallVariants = map[string]string{
	"minecraft:torch":          "minecraft:wall_torch",
	"minecraft:soul_torch":     "minecraft:soul_wall_torch",
	"minecraft:redstone_torch": "minecraft:redstone_wall_torch",
}

// faceNames 是协议方向索引对应的属性名（0=down … 5=east）。
var faceNames = [6]string{"down", "up", "north", "south", "west", "east"}

// placementHalfIndex 返回台阶/半砖的 half 索引（0=bottom、1=top），
// 与原版 `direction != DOWN && (direction == UP || clickY - posY <= 0.5)` 一致。
func placementHalfIndex(use protocol.UseItemOn) int {
	switch use.Direction {
	case 1: // 点击顶面
		return 0
	case 0: // 点击底面
		return 1
	}
	if use.CursorY > 0.5 {
		return 1
	}
	return 0
}

// horizontalFacingIndex 返回水平朝向在放置表里的索引（0=north…3=east）。
func horizontalFacingIndex(dir string) int {
	switch dir {
	case "north":
		return 0
	case "south":
		return 1
	case "west":
		return 2
	case "east":
		return 3
	}
	return 0
}

// directionIndex 返回六向朝向在放置表里的索引（0=down…5=east）。
func directionIndex(dir string) int {
	for i, name := range faceNames {
		if name == dir {
			return i
		}
	}
	return 0
}

// axisIndexFromFace 返回点击面对应的轴索引（0=x、1=y、2=z）。
func axisIndexFromFace(direction int32) int {
	switch direction {
	case 0, 1:
		return 1 // y
	case 2, 3:
		return 2 // z
	default:
		return 0 // x
	}
}

// nearestLookingDirection 返回玩家视线最近的方向（原版
// getNearestLookingDirection：俯仰角超过 ±45° 时取上下）。
func nearestLookingDirection(yaw, pitch float32) string {
	look := yawToFacing(yaw)
	switch {
	case pitch < -45:
		look = "up"
	case pitch > 45:
		look = "down"
	}
	return look
}

// orientPlacement 按原版放置规则返回放置状态与放置出的方块名。
// ok=false 表示该点击方式下不能放置（原版 getStateForPlacement 返回 null）。
func orientPlacement(name string, state uint16, yaw, pitch float32, use protocol.UseItemOn) (uint16, string, bool) {
	// 火把：按点击面选择地面/墙式变体。
	if wallName, ok := torchWallVariants[name]; ok {
		switch {
		case use.Direction == 0: // 底面
			return 0, "", false
		case use.Direction == 1: // 顶面：地面火把（默认状态）
			return state, name, true
		}
		facing := oppositeDir(faceNames[use.Direction])
		table, ok := registry.PlacementWallTorches[wallName]
		if !ok {
			return 0, "", false
		}
		return table[horizontalFacingIndex(facing)], wallName, true
	}
	// 梯子：只能贴在水平点击面上。
	if name == "minecraft:ladder" {
		if use.Direction < 2 {
			return 0, "", false
		}
		table, ok := registry.PlacementLadders[name]
		if !ok {
			return 0, "", false
		}
		facing := oppositeDir(faceNames[use.Direction])
		return table[horizontalFacingIndex(facing)], name, true
	}
	if table, ok := registry.PlacementFaceAttached[name]; ok {
		// 拉杆/按钮：按点击面决定附着面（原版 getNearestLookingDirection 的
		// 常见情形等价于“附着在被点击的表面上”）。
		face := 1 // wall
		facing := oppositeDir(faceNames[use.Direction])
		switch use.Direction {
		case 1: // 点击顶面：底部支撑（floor），朝向玩家水平朝向
			face = 0
			facing = yawToFacing(yaw)
		case 0: // 点击底面：顶部支撑（ceiling）
			face = 2
			facing = yawToFacing(yaw)
		}
		return table[face][horizontalFacingIndex(facing)], name, true
	}
	if table, ok := registry.PlacementStairs[name]; ok {
		facing := horizontalFacingIndex(yawToFacing(yaw))
		return table[placementHalfIndex(use)][facing], name, true
	}
	if table, ok := registry.PlacementSlabs[name]; ok {
		return table[placementHalfIndex(use)], name, true
	}
	if table, ok := registry.PlacementTrapdoors[name]; ok {
		half := 1
		if use.Direction == 1 {
			half = 0
		}
		facing := horizontalFacingIndex(oppositeDir(yawToFacing(yaw)))
		return table[half][facing], name, true
	}
	if table, ok := registry.PlacementFacing[name]; ok {
		index := directionIndex(oppositeDir(nearestLookingDirection(yaw, pitch)))
		if table[index] == 0 {
			// 水平四向方块不能使用上下朝向：按水平朝向处理。
			index = directionIndex(oppositeDir(yawToFacing(yaw)))
		}
		return table[index], name, true
	}
	if table, ok := registry.PlacementAxis[name]; ok {
		return table[axisIndexFromFace(use.Direction)], name, true
	}
	return state, name, true
}

// tryMergeSlab 处理“点击单半砖时叠成双半砖”（原版 SlabBlock.canBeReplaced：
// 单半砖可被替换，放置位置即被点击的半砖）。返回 true 表示已处理。
func (s *Server) tryMergeSlab(player *session, w *world.World, dim world.Dimension, use protocol.UseItemOn, current uint16, itemName string) bool {
	table, ok := registry.PlacementSlabs[itemName]
	if !ok {
		return false
	}
	currentName, ok := s.blockNameOf(current)
	if !ok || currentName != itemName {
		return false
	}
	if current != table[0] && current != table[1] {
		return false // 已是双半砖或不是本方块的半砖
	}
	if !w.SetBlock(use.X, use.Y, use.Z, table[2]) {
		return false
	}
	s.broadcastBlockUpdate(dim, use.X, use.Y, use.Z, int32(table[2]))
	s.updateRedstoneAround(dim, use.X, use.Y, use.Z)
	s.updateFluidsAround(dim, use.X, use.Y, use.Z)
	s.consumeHeldItem(player)
	return true
}

// toggleTrapdoor 处理右键开合活板门（原版 TrapDoorBlock.useWithoutItem：
// 切换 open，保留 facing/half/powered）。返回 true 表示已处理。
func (s *Server) toggleTrapdoor(player *session, w *world.World, use protocol.UseItemOn, current uint16) bool {
	name, ok := s.blockNameOf(current)
	if !ok || !strings.HasSuffix(name, "_trapdoor") {
		return false
	}
	closed, ok := registry.PlacementTrapdoors[name]
	if !ok {
		return false
	}
	open, ok := registry.PlacementTrapdoorsOpen[name]
	if !ok {
		return false
	}
	var next uint16
	found, opening := false, false
	for half := 0; half < 2 && !found; half++ {
		for facing := 0; facing < 4; facing++ {
			switch current {
			case closed[half][facing]:
				next, found, opening = open[half][facing], true, true
			case open[half][facing]:
				next, found, opening = closed[half][facing], true, false
			}
			if found {
				break
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
	// 开合音效（原版 TrapDoorBlock.useWithoutItem 的 playSound）。
	s.playTrapdoorSound(player.dimensionID(), [3]int{use.X, use.Y, use.Z}, name, opening)
	return true
}

// —— 门（占上下两格） ——
//
// 对照 1.21.11 DoorBlock.getStateForPlacement：facing = 玩家水平朝向，
// hinge 由 DoorBlock.getHinge 决定（邻居坚固面/相邻门 + 点击位置回退），
// open/powered = 位置（含上方一格）是否有红石信号。

// doorLowerAt 报告该位置是否为门的下半格（原版 DoorBlock.getHinge 中的
// `state.getBlock() instanceof DoorBlock && HALF == LOWER`）。
func (s *Server) doorLowerAt(w *world.World, pos [3]int) bool {
	state := w.BlockAt(pos[0], pos[1], pos[2])
	name, ok := s.blockNameOf(state)
	if !ok {
		return false
	}
	half, ok := doorHalfOf(name, state)
	return ok && half == 0
}

// doorHingeIndex 返回门的铰链索引（0=left、1=right），移植原版
// DoorBlock.getHinge(BlockPlaceContext)：
//
//  1. 以点击面为基准，逆时针侧（含上方一格）每个“面坚固”邻居计 −1，
//     顺时针侧（含上方一格）计 +1，得到 i；
//  2. 逆时针侧是门的下半格且顺时针侧不是，或 i > 0 → right；
//  3. 顺时针侧是门的下半格且逆时针侧不是，或 i < 0 → left；
//  4. 否则按点击位置相对门坐标的水平偏移回退（点击顶面/底面时恒为 left）。
func (s *Server) doorHingeIndex(w *world.World, use protocol.UseItemOn, x, y, z int) int {
	face := faceNames[use.Direction]
	ccw := counterClockwiseDir(face)
	cw := clockwiseDir(face)
	base := [3]int{x, y, z}
	above := [3]int{x, y + 1, z}
	ccwPos := offsetDir(base, ccw)
	ccwAbove := offsetDir(above, ccw)
	cwPos := offsetDir(base, cw)
	cwAbove := offsetDir(above, cw)

	i := 0
	if s.faceSturdy(w.BlockAt(ccwPos[0], ccwPos[1], ccwPos[2]), face) {
		i--
	}
	if s.faceSturdy(w.BlockAt(ccwAbove[0], ccwAbove[1], ccwAbove[2]), face) {
		i--
	}
	if s.faceSturdy(w.BlockAt(cwPos[0], cwPos[1], cwPos[2]), face) {
		i++
	}
	if s.faceSturdy(w.BlockAt(cwAbove[0], cwAbove[1], cwAbove[2]), face) {
		i++
	}
	ccwDoorLower := s.doorLowerAt(w, ccwPos)
	cwDoorLower := s.doorLowerAt(w, cwPos)
	if (ccwDoorLower && !cwDoorLower) || i > 0 {
		return 1 // right
	}
	if (cwDoorLower && !ccwDoorLower) || i < 0 {
		return 0 // left
	}
	return doorHingeFallback(use, face)
}

// doorHingeFallback 是原版 getHinge 尾部的点击位置回退：按点击面与点击位置
// 相对门坐标的水平偏移判定，点击顶面/底面（stepX=stepZ=0）时恒为 left。
func doorHingeFallback(use protocol.UseItemOn, face string) int {
	stepX, stepZ := 0, 0
	switch face {
	case "east":
		stepX = 1
	case "west":
		stepX = -1
	case "south":
		stepZ = 1
	case "north":
		stepZ = -1
	}
	dx := float64(use.CursorX)
	dz := float64(use.CursorZ)
	if (stepX < 0 && dz < 0.5) || (stepX > 0 && dz > 0.5) ||
		(stepZ < 0 && dx > 0.5) || (stepZ > 0 && dx < 0.5) {
		return 1 // right
	}
	return 0 // left
}

// doorHalfOf 返回门状态所处的半格（0=lower、1=upper）。
func doorHalfOf(name string, state uint16) (int, bool) {
	for _, tables := range []map[string][2][4][2]uint16{
		registry.PlacementDoors, registry.PlacementDoorsOpen,
	} {
		table, ok := tables[name]
		if !ok {
			continue
		}
		for half := 0; half < 2; half++ {
			for facing := 0; facing < 4; facing++ {
				for hinge := 0; hinge < 2; hinge++ {
					if table[half][facing][hinge] == state {
						return half, true
					}
				}
			}
		}
	}
	return 0, false
}

// isIronDoorName 报告方块名是否为铁门（只能用红石开合）。
func isIronDoorName(name string) bool {
	return strings.Contains(name, "iron_door")
}

// placeDoor 放置门（上下两格）；返回 false 表示空间不足或无法放置。
func (s *Server) placeDoor(player *session, w *world.World, dim world.Dimension, use protocol.UseItemOn, name string) bool {
	table, ok := registry.PlacementDoors[name]
	if !ok {
		return false
	}
	offset := blockFaceOffsets[use.Direction]
	x, y, z := use.X+offset[0], use.Y+offset[1], use.Z+offset[2]
	if _, ok := world.SectionIndex(y); !ok {
		return false
	}
	if _, ok := world.SectionIndex(y + 1); !ok {
		return false
	}
	if w.BlockAt(x, y, z) != world.AirBlock || w.BlockAt(x, y+1, z) != world.AirBlock {
		return false
	}
	_, _, _, yaw, _ := player.playerPosition()
	facing := yawToFacing(yaw)
	facingIndex := horizontalFacingIndex(facing)
	hinge := s.doorHingeIndex(w, use, x, y, z)
	lower := table[0][facingIndex][hinge]
	upper := table[1][facingIndex][hinge]
	// 原版 getStateForPlacement：位置本身或上方一格有邻居信号时，
	// 门以 open=true、powered=true 放置（铁门因此能直接被红石打开）。
	if s.bestNeighborSignal(w, [3]int{x, y, z}) > 0 ||
		s.bestNeighborSignal(w, [3]int{x, y + 1, z}) > 0 {
		if powered, ok := registry.PlacementDoorsPowered[name]; ok {
			lower = powered[0][facingIndex][hinge]
			upper = powered[1][facingIndex][hinge]
		}
	}
	if !w.SetBlock(x, y, z, lower) {
		return false
	}
	if !w.SetBlock(x, y+1, z, upper) {
		w.SetBlock(x, y, z, world.AirBlock)
		return false
	}
	s.broadcastBlockUpdate(dim, x, y, z, int32(lower))
	s.broadcastBlockUpdate(dim, x, y+1, z, int32(upper))
	s.updateRedstoneAround(dim, x, y, z)
	s.updateRedstoneAround(dim, x, y+1, z)
	s.updateFluidsAround(dim, x, y, z)
	s.updateFluidsAround(dim, x, y+1, z)
	s.consumeHeldItem(player)
	return true
}

// toggleDoor 处理右键开合门（原版 DoorBlock.useWithoutItem：切换 open，
// 通过邻居更新把同样的 open 值同步到另一半；铁门只能由红石打开）。
func (s *Server) toggleDoor(player *session, w *world.World, use protocol.UseItemOn, current uint16) bool {
	name, ok := s.blockNameOf(current)
	if !ok || isIronDoorName(name) {
		return false
	}
	closed, ok := registry.PlacementDoors[name]
	if !ok {
		return false
	}
	open, ok := registry.PlacementDoorsOpen[name]
	if !ok {
		return false
	}
	half, ok := doorHalfOf(name, current)
	if !ok {
		return false
	}
	next, sibling := uint16(0), uint16(0)
	found, opening := false, false
	for facing := 0; facing < 4 && !found; facing++ {
		for hinge := 0; hinge < 2; hinge++ {
			switch current {
			case closed[half][facing][hinge]:
				next = open[half][facing][hinge]
				sibling = open[1-half][facing][hinge]
				found, opening = true, true
			case open[half][facing][hinge]:
				next = closed[half][facing][hinge]
				sibling = closed[1-half][facing][hinge]
				found, opening = true, false
			}
			if found {
				break
			}
		}
	}
	if !found {
		return false
	}
	dim := player.dimensionID()
	if w.SetBlock(use.X, use.Y, use.Z, next) {
		s.broadcastBlockUpdate(dim, use.X, use.Y, use.Z, int32(next))
	}
	// 另一半（半格索引相反的 y 方向）。
	siblingY := use.Y + 1
	if half == 1 {
		siblingY = use.Y - 1
	}
	if siblingName, ok := s.blockNameOf(w.BlockAt(use.X, siblingY, use.Z)); ok && siblingName == name {
		if w.SetBlock(use.X, siblingY, use.Z, sibling) {
			s.broadcastBlockUpdate(dim, use.X, siblingY, use.Z, int32(sibling))
		}
	}
	// 开合音效以门的下半格为中心（原版 DoorBlock.setOpen 用当前格，
	// 客户端表现为同一位置；gmcs 统一取下半格便于两半一致）。
	baseY := use.Y
	if half == 1 {
		baseY = use.Y - 1
	}
	s.playDoorSound(dim, [3]int{use.X, baseY, use.Z}, name, opening)
	return true
}

// removeDoorPair 在门的一格被移除后移除另一格（不掉落，掉落由被破坏格的
// 战利品表结算：只有下半格掉落门）。
func (s *Server) removeDoorPair(w *world.World, dim world.Dimension, x, y, z int, state uint16) {
	name, ok := s.blockNameOf(state)
	if !ok {
		return
	}
	half, ok := doorHalfOf(name, state)
	if !ok {
		return
	}
	siblingY := y + 1
	if half == 1 {
		siblingY = y - 1
	}
	other := w.BlockAt(x, siblingY, z)
	if otherName, ok := s.blockNameOf(other); !ok || otherName != name {
		return
	}
	if otherHalf, ok := doorHalfOf(name, other); !ok || otherHalf == half {
		return
	}
	if w.SetBlock(x, siblingY, z, world.AirBlock) {
		s.broadcastBlockUpdate(dim, x, siblingY, z, int32(world.AirBlock))
	}
}
