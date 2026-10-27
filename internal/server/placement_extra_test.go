//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 床/告示牌放置与台阶邻居形状更新的测试。

// TestOrientPlacementStandingSign 验证立式告示牌的 rotation 由玩家 yaw 决定。
func TestOrientPlacementStandingSign(t *testing.T) {
	name := "minecraft:oak_sign"
	state := registry.BlockStateIDs[name]
	table := registry.PlacementStandingSigns[name]
	for _, tc := range []struct {
		yaw      float32
		rotation int
	}{
		{0, 8},    // 朝南 → (180+0)*16/360+0.5 = 8.5 → 8
		{-180, 0}, // 朝北 → 0（16 & 15）
		{90, 12},  // (270)*16/360+0.5 = 12.5 → 12
		{-90, 4},  // (90)*16/360+0.5 = 4.5 → 4
	} {
		got, gotName, ok := orientPlacement(name, state, tc.yaw, 0, useAt(0, 0, 0, 1, 0.5))
		if !ok || gotName != name {
			t.Fatalf("yaw=%v: ok=%v name=%q", tc.yaw, ok, gotName)
		}
		if want := table[tc.rotation]; got != want {
			t.Fatalf("yaw=%v: 告示牌状态 = %d, want %d（rotation=%d）", tc.yaw, got, want, tc.rotation)
		}
	}
}

// TestOrientPlacementWallSign 验证点击侧面时改用墙式告示牌并按点击面取 facing。
func TestOrientPlacementWallSign(t *testing.T) {
	name := "minecraft:oak_sign"
	state := registry.BlockStateIDs[name]
	wallName := "minecraft:oak_wall_sign"
	wallTable := registry.PlacementWallSigns[wallName]
	// 点击北面（direction=2）→ facing=north。
	got, gotName, ok := orientPlacement(name, state, 0, 0, useAt(0, 0, 0, 2, 0.5))
	if !ok || gotName != wallName {
		t.Fatalf("侧面点击应放置墙式告示牌：ok=%v name=%q", ok, gotName)
	}
	if want := wallTable[horizontalFacingIndex("north")]; got != want {
		t.Fatalf("墙式告示牌状态 = %d, want %d（facing=north）", got, want)
	}
	// 悬挂告示牌同样有墙式变体。
	hanging := "minecraft:oak_hanging_sign"
	got, gotName, ok = orientPlacement(hanging, registry.BlockStateIDs[hanging], 0, 0, useAt(0, 0, 0, 5, 0.5))
	if !ok || gotName != "minecraft:oak_wall_hanging_sign" {
		t.Fatalf("悬挂告示牌侧面点击：ok=%v name=%q", ok, gotName)
	}
	if want := registry.PlacementWallSigns["minecraft:oak_wall_hanging_sign"][horizontalFacingIndex("east")]; got != want {
		t.Fatalf("墙式悬挂告示牌状态 = %d, want %d", got, want)
	}
}

// TestPlaceBedTwoHalves 验证床占两格：脚格在放置位置、头格在 facing 方向。
func TestPlaceBedTwoHalves(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "PlacerBed")
	defer conn.Close()

	x, groundY, z := farmTestSpot(t, instance)
	w := instance.testWorld()
	player := activeSession(t, instance, "PlacerBed")
	player.inventory.Set(item.SlotHotbarStart, testStack(t, "minecraft:white_bed", 1))
	_, _, _, yaw, _ := player.playerPosition()

	sendUseItemOn(t, conn, x, groundY, z, 1)

	table := registry.PlacementBeds["minecraft:white_bed"]
	facing := horizontalFacingIndex(yawToFacing(yaw))
	offset := stairsFacingOffsets[facing]
	wantFoot := table[facing][bedPartFoot]
	wantHead := table[facing][bedPartHead]
	waitFor(t, func() bool { return w.BlockAt(x, groundY+1, z) == wantFoot }, "床的脚格应在放置位置")
	waitFor(t, func() bool {
		return w.BlockAt(x+offset[0], groundY+1, z+offset[2]) == wantHead
	}, "床的头格应在 facing 方向")
	waitFor(t, func() bool {
		return player.inventory.Get(item.SlotHotbarStart).Count == 0
	}, "放置床应消耗 1 个物品")
}

// TestRemoveBedPair 验证破坏床的一格会移除另一格。
func TestRemoveBedPair(t *testing.T) {
	instance := newHopperTestServer(t)
	w := instance.testWorld()
	x, groundY, z := farmTestSpot(t, instance)
	table := registry.PlacementBeds["minecraft:white_bed"]
	foot := table[0][bedPartFoot] // facing=north
	head := table[0][bedPartHead]
	// facing=north → 头格在 z-1。
	w.SetBlock(x, groundY+1, z, foot)
	w.SetBlock(x, groundY+1, z-1, head)
	w.SetBlock(x, groundY+1, z-1, head)

	instance.removeBedPair(w, world.DimensionOverworld, x, groundY+1, z, foot)
	if got := w.BlockAt(x, groundY+1, z-1); got != world.AirBlock {
		t.Fatalf("破坏脚格后头格 = %d, want 空气", got)
	}
}

// TestStairsShapeRefresh 验证台阶在邻居变化后重算 shape（inner/outer）。
func TestStairsShapeRefresh(t *testing.T) {
	instance := newHopperTestServer(t)
	w := instance.testWorld()
	x, groundY, z := farmTestSpot(t, instance)
	y := groundY + 1
	table := registry.PlacementStairsShape["minecraft:oak_stairs"]
	const (
		half       = 0
		south      = 1
		west       = 2
		innerRight = stairsInnerRight
		outerRight = stairsOuterRight
	)

	// 前方（南侧）放一个朝西的台阶 → outer_right。
	w.SetBlock(x, y, z, table[half][south][stairsStraight])
	w.SetBlock(x, y, z+1, table[half][west][stairsStraight])
	instance.refreshStairsShape(world.DimensionOverworld, w, x, y, z)
	if got := w.BlockAt(x, y, z); got != table[half][south][outerRight] {
		t.Fatalf("前方垂直台阶应得到 outer_right：state=%d want %d",
			got, table[half][south][outerRight])
	}
	// 移除前方台阶、改在后方（北侧）放朝西台阶 → inner_right。
	w.SetBlock(x, y, z+1, world.AirBlock)
	w.SetBlock(x, y, z-1, table[half][west][stairsStraight])
	instance.refreshStairsShape(world.DimensionOverworld, w, x, y, z)
	if got := w.BlockAt(x, y, z); got != table[half][south][innerRight] {
		t.Fatalf("后方垂直台阶应得到 inner_right：state=%d want %d",
			got, table[half][south][innerRight])
	}
	// 移除后方台阶 → 回到 straight。
	w.SetBlock(x, y, z-1, world.AirBlock)
	instance.refreshStairsShape(world.DimensionOverworld, w, x, y, z)
	if got := w.BlockAt(x, y, z); got != table[half][south][stairsStraight] {
		t.Fatalf("邻居移除后应回到 straight：state=%d want %d",
			got, table[half][south][stairsStraight])
	}
}
