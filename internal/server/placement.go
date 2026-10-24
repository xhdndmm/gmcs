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
	found := false
	for half := 0; half < 2 && !found; half++ {
		for facing := 0; facing < 4; facing++ {
			switch current {
			case closed[half][facing]:
				next, found = open[half][facing], true
			case open[half][facing]:
				next, found = closed[half][facing], true
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
	// 原版播放 block.wooden_trapdoor.toggle / 铁质音效；gmcs 未接入逐方块音效，
	// 客户端自身预测会播放本地音效。
	return true
}
