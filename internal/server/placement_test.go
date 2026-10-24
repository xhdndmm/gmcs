//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 方块放置形态测试：台阶/半砖/柱状/朝向方块/活板门/梯子/火把的朝向与失败条件，
// 以及双半砖合并与活板门开合。

// useAt 构造一个指定点击面与光标高度的 Use Item On。
func useAt(x, y, z int, direction int32, cursorY float32) protocol.UseItemOn {
	return protocol.UseItemOn{
		Hand: 0, X: x, Y: y, Z: z, Direction: direction,
		CursorX: 0.5, CursorY: cursorY, CursorZ: 0.5,
	}
}

// TestOrientPlacementStairs 验证台阶朝向（facing=玩家水平朝向）与 half 判定。
func TestOrientPlacementStairs(t *testing.T) {
	name := "minecraft:oak_stairs"
	state := registry.BlockStateIDs[name]
	table := registry.PlacementStairs[name]
	for _, tc := range []struct {
		yaw    float32
		facing int
	}{
		{0, 1},   // 朝南
		{90, 2},  // 朝西
		{180, 0}, // 朝北
		{270, 3}, // 朝东
	} {
		got, gotName, ok := orientPlacement(name, state, tc.yaw, 0, useAt(0, 0, 0, 1, 0.5))
		if !ok || gotName != name {
			t.Fatalf("yaw=%v: ok=%v name=%q", tc.yaw, ok, gotName)
		}
		if want := table[0][tc.facing]; got != want {
			t.Fatalf("yaw=%v: 台阶状态 = %d, want %d（half=bottom facing=%d）", tc.yaw, got, want, tc.facing)
		}
	}
	// 点击侧面且光标高度 > 0.5 → half=top。
	got, _, _ := orientPlacement(name, state, 0, 0, useAt(0, 0, 0, 2, 0.8))
	if want := table[1][1]; got != want {
		t.Fatalf("侧面高光标应放置上半台阶：state=%d want %d", got, want)
	}
	// 点击底面 → half=top。
	got, _, _ = orientPlacement(name, state, 0, 0, useAt(0, 0, 0, 0, 0.2))
	if want := table[1][1]; got != want {
		t.Fatalf("底面点击应放置上半台阶：state=%d want %d", got, want)
	}
}

// TestOrientPlacementSlab 验证半砖 type 判定。
func TestOrientPlacementSlab(t *testing.T) {
	name := "minecraft:oak_slab"
	state := registry.BlockStateIDs[name]
	table := registry.PlacementSlabs[name]
	got, _, ok := orientPlacement(name, state, 0, 0, useAt(0, 0, 0, 1, 0.9))
	if !ok || got != table[0] {
		t.Fatalf("顶面点击应为下半砖：%d want %d", got, table[0])
	}
	got, _, _ = orientPlacement(name, state, 0, 0, useAt(0, 0, 0, 3, 0.8))
	if got != table[1] {
		t.Fatalf("侧面高光标应为上半砖：%d want %d", got, table[1])
	}
}

// TestOrientPlacementAxis 验证柱状方块的轴由点击面决定。
func TestOrientPlacementAxis(t *testing.T) {
	name := "minecraft:oak_log"
	state := registry.BlockStateIDs[name]
	table := registry.PlacementAxis[name]
	for _, tc := range []struct {
		direction int32
		axis      int
	}{
		{1, 1}, // 顶面 → y
		{0, 1}, // 底面 → y
		{2, 2}, // 北面 → z
		{3, 2}, // 南面 → z
		{4, 0}, // 西面 → x
		{5, 0}, // 东面 → x
	} {
		got, _, ok := orientPlacement(name, state, 0, 0, useAt(0, 0, 0, tc.direction, 0.5))
		if !ok || got != table[tc.axis] {
			t.Fatalf("direction=%d: axis 状态 = %d, want %d", tc.direction, got, table[tc.axis])
		}
	}
}

// TestOrientPlacementFacing 验证朝向方块（水平/六向）的 facing。
func TestOrientPlacementFacing(t *testing.T) {
	// 箱子：水平朝向的反方向（yaw=0 朝南 → facing=north）。
	chest, ok := registry.PlacementFacing["minecraft:chest"]
	if !ok {
		t.Fatal("缺少箱子放置状态")
	}
	got, _, ok := orientPlacement("minecraft:chest", registry.BlockStateIDs["minecraft:chest"], 0, 0, useAt(0, 0, 0, 1, 0.5))
	if !ok || got != chest[directionIndex("north")] {
		t.Fatalf("箱子 facing 应为 north：state=%d want %d", got, chest[directionIndex("north")])
	}
	// 木桶：六向 —— 抬头（pitch=-90）放置时 facing=down。
	barrel, ok := registry.PlacementFacing["minecraft:barrel"]
	if !ok {
		t.Fatal("缺少木桶放置状态")
	}
	got, _, ok = orientPlacement("minecraft:barrel", registry.BlockStateIDs["minecraft:barrel"], 0, -90, useAt(0, 0, 0, 1, 0.5))
	if !ok || got != barrel[directionIndex("down")] {
		t.Fatalf("抬头放置木桶 facing 应为 down：state=%d want %d", got, barrel[directionIndex("down")])
	}
	// 潜影盒：低头（pitch=90）放置时 facing=up。
	shulker, ok := registry.PlacementFacing["minecraft:white_shulker_box"]
	if !ok {
		t.Fatal("缺少潜影盒放置状态")
	}
	got, _, ok = orientPlacement("minecraft:white_shulker_box", registry.BlockStateIDs["minecraft:white_shulker_box"], 0, 90, useAt(0, 0, 0, 1, 0.5))
	if !ok || got != shulker[directionIndex("up")] {
		t.Fatalf("低头放置潜影盒 facing 应为 up：state=%d want %d", got, shulker[directionIndex("up")])
	}
}

// TestOrientPlacementTrapdoor 验证活板门 facing/half。
func TestOrientPlacementTrapdoor(t *testing.T) {
	name := "minecraft:oak_trapdoor"
	state := registry.BlockStateIDs[name]
	table := registry.PlacementTrapdoors[name]
	// yaw=0 朝南 → facing=north；点击顶面 → half=bottom。
	got, _, ok := orientPlacement(name, state, 0, 0, useAt(0, 0, 0, 1, 0.9))
	if !ok || got != table[0][directionIndex("north")-2] {
		t.Fatalf("活板门应为 bottom/north：state=%d want %d", got, table[0][0])
	}
	// 点击侧面 → half=top。
	got, _, _ = orientPlacement(name, state, 0, 0, useAt(0, 0, 0, 2, 0.2))
	if got != table[1][0] {
		t.Fatalf("侧面点击活板门应为 top：state=%d want %d", got, table[1][0])
	}
}

// TestOrientPlacementLadder 验证梯子只能贴在水平面。
func TestOrientPlacementLadder(t *testing.T) {
	name := "minecraft:ladder"
	state := registry.BlockStateIDs[name]
	table := registry.PlacementLadders[name]
	// 点击北面 → 梯子 facing=south。
	got, _, ok := orientPlacement(name, state, 0, 0, useAt(0, 0, 0, 2, 0.5))
	if !ok || got != table[horizontalFacingIndex("south")] {
		t.Fatalf("梯子应贴北面朝南：state=%d want %d", got, table[horizontalFacingIndex("south")])
	}
	// 点击顶面 → 失败。
	if _, _, ok := orientPlacement(name, state, 0, 0, useAt(0, 0, 0, 1, 0.5)); ok {
		t.Fatal("梯子不应能贴在顶面上")
	}
	// 点击底面 → 失败。
	if _, _, ok := orientPlacement(name, state, 0, 0, useAt(0, 0, 0, 0, 0.5)); ok {
		t.Fatal("梯子不应能贴在底面上")
	}
}

// TestOrientPlacementTorch 验证火把的地面/墙式变体选择。
func TestOrientPlacementTorch(t *testing.T) {
	torch := registry.BlockStateIDs["minecraft:torch"]
	// 点击东面 → 墙式火把 facing=west。
	got, name, ok := orientPlacement("minecraft:torch", torch, 0, 0, useAt(0, 0, 0, 5, 0.5))
	if !ok || name != "minecraft:wall_torch" {
		t.Fatalf("侧面点击应放墙式火把：name=%q ok=%v", name, ok)
	}
	if want := registry.PlacementWallTorches["minecraft:wall_torch"][horizontalFacingIndex("west")]; got != want {
		t.Fatalf("墙式火把 facing 应为 west：%d want %d", got, want)
	}
	// 点击顶面 → 地面火把。
	got, name, ok = orientPlacement("minecraft:torch", torch, 0, 0, useAt(0, 0, 0, 1, 0.5))
	if !ok || name != "minecraft:torch" || got != torch {
		t.Fatalf("顶面点击应放地面火把：state=%d name=%q ok=%v", got, name, ok)
	}
	// 点击底面 → 失败。
	if _, _, ok := orientPlacement("minecraft:torch", torch, 0, 0, useAt(0, 0, 0, 0, 0.5)); ok {
		t.Fatal("底面点击不应放置火把")
	}
}

// TestPlaceChestFacing 验证 e2e 放置箱子的朝向与玩家朝向相反。
func TestPlaceChestFacing(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "PlacerChest")
	defer conn.Close()

	x, groundY, z := farmTestSpot(t, instance)
	player := activeSession(t, instance, "PlacerChest")
	player.inventory.Set(item.SlotHotbarStart, testStack(t, "minecraft:chest", 1))
	_, _, _, yaw, _ := player.playerPosition()

	sendUseItemOn(t, conn, x, groundY, z, 1)
	table := registry.PlacementFacing["minecraft:chest"]
	want := table[directionIndex(oppositeDir(yawToFacing(yaw)))]
	waitFor(t, func() bool {
		return instance.testWorld().BlockAt(x, groundY+1, z) == want
	}, "箱子应按玩家朝向的反方向放置")
	if count := player.inventory.Get(item.SlotHotbarStart).Count; count != 0 {
		t.Fatalf("放置应消耗 1 个箱子：count=%d", count)
	}
}

// TestPlaceLogAxis 验证 e2e 木头按点击面决定轴向。
func TestPlaceLogAxis(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "PlacerLog")
	defer conn.Close()

	x, groundY, z := farmTestSpot(t, instance)
	w := instance.testWorld()
	w.SetBlock(x+1, groundY, z, world.AirBlock)
	w.SetBlock(x+1, groundY+1, z, world.AirBlock)
	player := activeSession(t, instance, "PlacerLog")
	player.inventory.Set(item.SlotHotbarStart, testStack(t, "minecraft:oak_log", 1))

	// 点击泥土方块东面 → 木头 axis=x。
	sendUseItemOn(t, conn, x, groundY, z, 5)
	want := registry.PlacementAxis["minecraft:oak_log"][0]
	waitFor(t, func() bool { return w.BlockAt(x+1, groundY, z) == want }, "木头应按点击面设置 axis=x")
}

// TestPlaceStairsOrientation 验证 e2e 台阶朝向与 half。
func TestPlaceStairsOrientation(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "PlacerStairs")
	defer conn.Close()

	x, groundY, z := farmTestSpot(t, instance)
	player := activeSession(t, instance, "PlacerStairs")
	player.inventory.Set(item.SlotHotbarStart, testStack(t, "minecraft:oak_stairs", 1))
	_, _, _, yaw, _ := player.playerPosition()

	sendUseItemOn(t, conn, x, groundY, z, 1)
	table := registry.PlacementStairs["minecraft:oak_stairs"]
	want := table[0][horizontalFacingIndex(yawToFacing(yaw))]
	waitFor(t, func() bool {
		return instance.testWorld().BlockAt(x, groundY+1, z) == want
	}, "台阶应按玩家朝向放置在下半格")
}

// TestMergeSlabIntoDouble 验证点击单半砖叠成双半砖。
func TestMergeSlabIntoDouble(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "PlacerSlab")
	defer conn.Close()

	x, groundY, z := farmTestSpot(t, instance)
	w := instance.testWorld()
	table := registry.PlacementSlabs["minecraft:oak_slab"]
	w.SetBlock(x, groundY, z, table[0]) // 下半砖
	player := activeSession(t, instance, "PlacerSlab")
	player.inventory.Set(item.SlotHotbarStart, testStack(t, "minecraft:oak_slab", 1))

	sendUseItemOn(t, conn, x, groundY, z, 1)
	waitFor(t, func() bool { return w.BlockAt(x, groundY, z) == table[2] }, "点击单半砖应叠成双半砖")
	waitFor(t, func() bool {
		return player.inventory.Get(item.SlotHotbarStart).Count == 0
	}, "合并应消耗 1 个半砖")
}

// TestToggleTrapdoor 验证右键开合活板门。
func TestToggleTrapdoor(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "TrapdoorUser")
	defer conn.Close()

	x, groundY, z := farmTestSpot(t, instance)
	w := instance.testWorld()
	closed := registry.PlacementTrapdoors["minecraft:oak_trapdoor"][0][0]
	open := registry.PlacementTrapdoorsOpen["minecraft:oak_trapdoor"][0][0]
	w.SetBlock(x, groundY, z, closed)

	// 空手右键 → 打开。
	sendUseItemOn(t, conn, x, groundY, z, 1)
	waitFor(t, func() bool { return w.BlockAt(x, groundY, z) == open }, "右键应打开活板门")
	// 再右键 → 关闭。
	sendUseItemOn(t, conn, x, groundY, z, 1)
	waitFor(t, func() bool { return w.BlockAt(x, groundY, z) == closed }, "再次右键应关闭活板门")
}

// TestPlaceTorchOnWall 验证点击侧面放置墙式火把。
func TestPlaceTorchOnWall(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "PlacerTorch")
	defer conn.Close()

	x, groundY, z := farmTestSpot(t, instance)
	w := instance.testWorld()
	w.SetBlock(x, groundY, z-1, world.AirBlock)
	player := activeSession(t, instance, "PlacerTorch")
	player.inventory.Set(item.SlotHotbarStart, testStack(t, "minecraft:torch", 1))

	sendUseItemOn(t, conn, x, groundY, z, 2) // 点击北面
	want := registry.PlacementWallTorches["minecraft:wall_torch"][horizontalFacingIndex("south")]
	waitFor(t, func() bool { return w.BlockAt(x, groundY, z-1) == want }, "侧面点击应放置墙式火把")
}

// TestOrientPlacementFaceAttached 验证拉杆与按钮的附着面与朝向。
func TestOrientPlacementFaceAttached(t *testing.T) {
	lever := "minecraft:lever"
	table, ok := registry.PlacementFaceAttached[lever]
	if !ok {
		t.Fatal("缺少拉杆放置状态")
	}
	state := registry.BlockStateIDs[lever]
	// 点击顶面：floor + 玩家朝向（yaw=90 → west）。
	got, _, ok := orientPlacement(lever, state, 90, 0, useAt(0, 0, 0, 1, 0.5))
	if !ok || got != table[0][horizontalFacingIndex("west")] {
		t.Fatalf("拉杆 floor/west = %d, want %d", got, table[0][horizontalFacingIndex("west")])
	}
	// 点击东面：wall + facing=west。
	got, _, _ = orientPlacement(lever, state, 0, 0, useAt(0, 0, 0, 5, 0.5))
	if got != table[1][horizontalFacingIndex("west")] {
		t.Fatalf("拉杆 wall/west = %d, want %d", got, table[1][horizontalFacingIndex("west")])
	}
	// 点击底面：ceiling + 玩家朝向。
	got, _, _ = orientPlacement(lever, state, 90, 0, useAt(0, 0, 0, 0, 0.5))
	if got != table[2][horizontalFacingIndex("west")] {
		t.Fatalf("拉杆 ceiling/west = %d, want %d", got, table[2][horizontalFacingIndex("west")])
	}
	// 按钮使用同一套附着规则。
	button := "minecraft:oak_button"
	buttonTable, ok := registry.PlacementFaceAttached[button]
	if !ok {
		t.Fatal("缺少橡木按钮放置状态")
	}
	got, _, ok = orientPlacement(button, registry.BlockStateIDs[button], 0, 0, useAt(0, 0, 0, 3, 0.5))
	if !ok || got != buttonTable[1][horizontalFacingIndex("north")] {
		t.Fatalf("按钮 wall/north = %d, want %d", got, buttonTable[1][horizontalFacingIndex("north")])
	}
}

// TestPlaceLeverOnGround 验证 e2e 放置拉杆（地面支撑）并能右键通电。
func TestPlaceLeverOnGround(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "LeverUser")
	defer conn.Close()

	x, groundY, z := farmTestSpot(t, instance)
	w := instance.testWorld()
	stone, ok := registry.BlockStateIDs["minecraft:stone"]
	if !ok {
		t.Fatal("缺少石头方块状态")
	}
	w.SetBlock(x, groundY, z, stone)
	player := activeSession(t, instance, "LeverUser")
	player.inventory.Set(item.SlotHotbarStart, testStack(t, "minecraft:lever", 1))
	_, _, _, yaw, _ := player.playerPosition()

	sendUseItemOn(t, conn, x, groundY, z, 1)
	table := registry.PlacementFaceAttached["minecraft:lever"]
	want := table[0][horizontalFacingIndex(yawToFacing(yaw))]
	waitFor(t, func() bool { return w.BlockAt(x, groundY+1, z) == want }, "拉杆应附着在地面并按玩家朝向放置")

	// 右键通电：powered=true。
	sendUseItemOn(t, conn, x, groundY+1, z, 1)
	waitFor(t, func() bool {
		name, props, ok := registry.StateProps(w.BlockAt(x, groundY+1, z))
		return ok && name == "minecraft:lever" && props["powered"] == "true"
	}, "右键拉杆应通电")
}

// TestDoorHingeIndex 验证门铰链按点击位置判定的回退分支。
func TestDoorHingeIndex(t *testing.T) {
	// 面朝南（stepZ=+1）：点击位置 dx<0.5 → right，dx>=0.5 → left。
	if got := doorHingeIndex(useAt(0, 0, 0, 1, 0.5), "south"); got != 0 {
		t.Fatalf("south 中心点击铰链 = %d, want left(0)", got)
	}
	left := protocol.UseItemOn{X: 0, CursorX: 0.2, CursorY: 0.5, CursorZ: 0.5, Direction: 1}
	if got := doorHingeIndex(left, "south"); got != 1 {
		t.Fatalf("south 偏西点击铰链 = %d, want right(1)", got)
	}
	// 面朝东（stepX=+1）：dz>0.5 → right。
	right := protocol.UseItemOn{X: 0, CursorX: 0.5, CursorY: 0.5, CursorZ: 0.8, Direction: 1}
	if got := doorHingeIndex(right, "east"); got != 1 {
		t.Fatalf("east 偏南点击铰链 = %d, want right(1)", got)
	}
}

// TestPlaceDoorBothHalves 验证放置门时同时创建上下两格并消耗 1 个物品。
func TestPlaceDoorBothHalves(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "DoorPlacer")
	defer conn.Close()

	x, groundY, z := farmTestSpot(t, instance)
	w := instance.testWorld()
	player := activeSession(t, instance, "DoorPlacer")
	player.inventory.Set(item.SlotHotbarStart, testStack(t, "minecraft:oak_door", 1))
	_, _, _, yaw, _ := player.playerPosition()

	sendUseItemOn(t, conn, x, groundY, z, 1)
	table := registry.PlacementDoors["minecraft:oak_door"]
	facing := yawToFacing(yaw)
	hinge := doorHingeIndex(useAt(0, 0, 0, 1, 0.5), facing)
	lower := table[0][horizontalFacingIndex(facing)][hinge]
	upper := table[1][horizontalFacingIndex(facing)][hinge]
	waitFor(t, func() bool { return w.BlockAt(x, groundY+1, z) == lower }, "门下半格未放置")
	waitFor(t, func() bool { return w.BlockAt(x, groundY+2, z) == upper }, "门上半格未放置")
	if count := player.inventory.Get(item.SlotHotbarStart).Count; count != 0 {
		t.Fatalf("放置门应消耗 1 个物品：count=%d", count)
	}
}

// TestDoorRequiresTwoBlocks 验证上方被占用时不放置门。
func TestDoorRequiresTwoBlocks(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "DoorBlocked")
	defer conn.Close()

	x, groundY, z := farmTestSpot(t, instance)
	w := instance.testWorld()
	stone, ok := registry.BlockStateIDs["minecraft:stone"]
	if !ok {
		t.Fatal("缺少石头方块状态")
	}
	w.SetBlock(x, groundY+2, z, stone)
	player := activeSession(t, instance, "DoorBlocked")
	player.inventory.Set(item.SlotHotbarStart, testStack(t, "minecraft:oak_door", 1))

	sendUseItemOn(t, conn, x, groundY, z, 1)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")
	if state := w.BlockAt(x, groundY+1, z); state != world.AirBlock {
		t.Fatalf("上方被占用时不应放置门：state=%d", state)
	}
	if count := player.inventory.Get(item.SlotHotbarStart).Count; count != 1 {
		t.Fatalf("失败的放置不应消耗物品：count=%d", count)
	}
}

// TestDoorToggleBothHalves 验证右键门会同时切换上下两格的 open 状态。
func TestDoorToggleBothHalves(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "DoorUser")
	defer conn.Close()

	x, groundY, z := farmTestSpot(t, instance)
	w := instance.testWorld()
	player := activeSession(t, instance, "DoorUser")
	player.inventory.Set(item.SlotHotbarStart, testStack(t, "minecraft:oak_door", 1))
	_, _, _, yaw, _ := player.playerPosition()
	facing := yawToFacing(yaw)
	hinge := doorHingeIndex(useAt(0, 0, 0, 1, 0.5), facing)
	facingIndex := horizontalFacingIndex(facing)
	closed := registry.PlacementDoors["minecraft:oak_door"]
	open := registry.PlacementDoorsOpen["minecraft:oak_door"]
	w.SetBlock(x, groundY+1, z, closed[0][facingIndex][hinge])
	w.SetBlock(x, groundY+2, z, closed[1][facingIndex][hinge])

	// 右键下半格 → 两格都打开。
	sendUseItemOn(t, conn, x, groundY+1, z, 1)
	waitFor(t, func() bool { return w.BlockAt(x, groundY+1, z) == open[0][facingIndex][hinge] },
		"门下半格未打开")
	waitFor(t, func() bool { return w.BlockAt(x, groundY+2, z) == open[1][facingIndex][hinge] },
		"门上半格未同步打开")
	// 再右键 → 两格都关闭。
	sendUseItemOn(t, conn, x, groundY+1, z, 1)
	waitFor(t, func() bool { return w.BlockAt(x, groundY+1, z) == closed[0][facingIndex][hinge] },
		"门下半格未关闭")
	waitFor(t, func() bool { return w.BlockAt(x, groundY+2, z) == closed[1][facingIndex][hinge] },
		"门上半格未同步关闭")
}

// TestDoorBreakDropsOnce 验证破坏门的一半会移除另一半，并且只掉落 1 个门。
func TestDoorBreakDropsOnce(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "DoorBreaker")
	defer conn.Close()

	x, groundY, z := farmTestSpot(t, instance)
	w := instance.testWorld()
	player := activeSession(t, instance, "DoorBreaker")
	player.inventory.Set(item.SlotHotbarStart, testStack(t, "minecraft:oak_door", 1))
	_, _, _, yaw, _ := player.playerPosition()
	facing := yawToFacing(yaw)
	hinge := doorHingeIndex(useAt(0, 0, 0, 1, 0.5), facing)
	facingIndex := horizontalFacingIndex(facing)
	closed := registry.PlacementDoors["minecraft:oak_door"]
	w.SetBlock(x, groundY+1, z, closed[0][facingIndex][hinge])
	w.SetBlock(x, groundY+2, z, closed[1][facingIndex][hinge])
	player.inventory.Set(item.SlotHotbarStart, item.Empty())

	breakBlockSurvival(t, conn, instance, player, x, groundY+1, z)
	waitFor(t, func() bool { return w.BlockAt(x, groundY+1, z) == world.AirBlock }, "门下半格未被破坏")
	waitFor(t, func() bool { return w.BlockAt(x, groundY+2, z) == world.AirBlock }, "门上半格未随下半格移除")
	doorID, err := registry.ItemID("minecraft:oak_door")
	if err != nil {
		t.Fatal(err)
	}
	countDrops := func() int {
		instance.entityMu.Lock()
		defer instance.entityMu.Unlock()
		total := 0
		for _, entity := range instance.items {
			if entity.Stack.ItemID == doorID {
				total += int(entity.Stack.Count)
			}
		}
		return total
	}
	waitFor(t, func() bool { return countDrops() == 1 }, "破坏下半格应只掉落 1 个门")
}

// TestIronDoorNotToggledByHand 验证铁门不能手动开合。
func TestIronDoorNotToggledByHand(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "IronDoor")
	defer conn.Close()

	x, groundY, z := farmTestSpot(t, instance)
	w := instance.testWorld()
	table := registry.PlacementDoors["minecraft:iron_door"]
	w.SetBlock(x, groundY+1, z, table[0][0][0])
	w.SetBlock(x, groundY+2, z, table[1][0][0])

	sendUseItemOn(t, conn, x, groundY+1, z, 1)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")
	if state := w.BlockAt(x, groundY+1, z); state != table[0][0][0] {
		t.Fatalf("铁门不应被右键打开：state=%d", state)
	}
}

// TestPlaceFenceGateFacing 验证栅栏门按玩家水平朝向放置（未通电为关闭）。
func TestPlaceFenceGateFacing(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "GatePlacer")
	defer conn.Close()

	x, groundY, z := farmTestSpot(t, instance)
	player := activeSession(t, instance, "GatePlacer")
	player.inventory.Set(item.SlotHotbarStart, testStack(t, "minecraft:oak_fence_gate", 1))
	_, _, _, yaw, _ := player.playerPosition()

	sendUseItemOn(t, conn, x, groundY, z, 1)
	want := registry.PlacementFenceGates["minecraft:oak_fence_gate"][0][horizontalFacingIndex(yawToFacing(yaw))]
	waitFor(t, func() bool {
		return instance.testWorld().BlockAt(x, groundY+1, z) == want
	}, "栅栏门应按玩家朝向放置")
}

// TestToggleFenceGate 验证右键开合栅栏门。
func TestToggleFenceGate(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "GateUser")
	defer conn.Close()

	x, groundY, z := farmTestSpot(t, instance)
	w := instance.testWorld()
	closed := registry.PlacementFenceGates["minecraft:oak_fence_gate"][0][0]
	open := registry.PlacementFenceGatesOpen["minecraft:oak_fence_gate"][0][0]
	w.SetBlock(x, groundY, z, closed)

	sendUseItemOn(t, conn, x, groundY, z, 1)
	waitFor(t, func() bool { return w.BlockAt(x, groundY, z) == open }, "右键应打开栅栏门")
	sendUseItemOn(t, conn, x, groundY, z, 1)
	waitFor(t, func() bool { return w.BlockAt(x, groundY, z) == closed }, "再次右键应关闭栅栏门")
}

// TestRedstoneOpensTrapdoor 验证红石信号开合活板门。
func TestRedstoneOpensTrapdoor(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "TrapdoorRedstone")
	defer instance.worldFor(world.DimensionOverworld).Close()

	w := instance.testWorld()
	baseX, baseY, baseZ := redstoneBase(instance)
	y := baseY + 1
	for dx := 0; dx <= 1; dx++ {
		for dy := 0; dy <= 1; dy++ {
			w.SetBlock(baseX+dx, y+dy, baseZ, world.AirBlock)
		}
	}
	closed := registry.PlacementTrapdoors["minecraft:oak_trapdoor"][0][0]
	w.SetBlock(baseX+1, y, baseZ, closed)
	lever := placeState(t, instance, baseX, y, baseZ, leverName,
		map[string]string{"face": "floor", "facing": "north", "powered": "false"})

	if !instance.handleRedstoneUse(w, baseX, y, baseZ, lever) {
		t.Fatal("handleRedstoneUse(lever) = false, want true")
	}
	powered := registry.PlacementTrapdoorsPowered["minecraft:oak_trapdoor"][0][0]
	if got := w.BlockAt(baseX+1, y, baseZ); got != powered {
		t.Fatalf("通电后活板门应为打开状态：%d want %d", got, powered)
	}
	current := w.BlockAt(baseX, y, baseZ)
	if !instance.handleRedstoneUse(w, baseX, y, baseZ, current) {
		t.Fatal("handleRedstoneUse(lever off) = false, want true")
	}
	if got := w.BlockAt(baseX+1, y, baseZ); got != closed {
		t.Fatalf("断电后活板门应关闭：%d want %d", got, closed)
	}
}

// TestRedstoneOpensDoor 验证红石信号同时开合门的上下两格。
func TestRedstoneOpensDoor(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "DoorRedstone")
	defer instance.worldFor(world.DimensionOverworld).Close()

	w := instance.testWorld()
	baseX, baseY, baseZ := redstoneBase(instance)
	y := baseY + 1
	for dx := 0; dx <= 1; dx++ {
		for dy := 0; dy <= 2; dy++ {
			w.SetBlock(baseX+dx, y+dy, baseZ, world.AirBlock)
		}
	}
	closed := registry.PlacementDoors["minecraft:oak_door"]
	w.SetBlock(baseX+1, y, baseZ, closed[0][0][0])
	w.SetBlock(baseX+1, y+1, baseZ, closed[1][0][0])
	lever := placeState(t, instance, baseX, y, baseZ, leverName,
		map[string]string{"face": "floor", "facing": "north", "powered": "false"})

	if !instance.handleRedstoneUse(w, baseX, y, baseZ, lever) {
		t.Fatal("handleRedstoneUse(lever) = false, want true")
	}
	powered := registry.PlacementDoorsPowered["minecraft:oak_door"]
	if got := w.BlockAt(baseX+1, y, baseZ); got != powered[0][0][0] {
		t.Fatalf("通电后门下半格应为打开状态：%d want %d", got, powered[0][0][0])
	}
	if got := w.BlockAt(baseX+1, y+1, baseZ); got != powered[1][0][0] {
		t.Fatalf("通电后门上半格应同步打开：%d want %d", got, powered[1][0][0])
	}
}

// TestRedstoneOpensFenceGate 验证红石信号开合栅栏门。
func TestRedstoneOpensFenceGate(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "GateRedstone")
	defer instance.worldFor(world.DimensionOverworld).Close()

	w := instance.testWorld()
	baseX, baseY, baseZ := redstoneBase(instance)
	y := baseY + 1
	for dx := 0; dx <= 1; dx++ {
		for dy := 0; dy <= 1; dy++ {
			w.SetBlock(baseX+dx, y+dy, baseZ, world.AirBlock)
		}
	}
	closed := registry.PlacementFenceGates["minecraft:oak_fence_gate"][0][0]
	w.SetBlock(baseX+1, y, baseZ, closed)
	lever := placeState(t, instance, baseX, y, baseZ, leverName,
		map[string]string{"face": "floor", "facing": "north", "powered": "false"})

	if !instance.handleRedstoneUse(w, baseX, y, baseZ, lever) {
		t.Fatal("handleRedstoneUse(lever) = false, want true")
	}
	powered := registry.PlacementFenceGatesPowered["minecraft:oak_fence_gate"][0][0]
	if got := w.BlockAt(baseX+1, y, baseZ); got != powered {
		t.Fatalf("通电后栅栏门应为打开状态：%d want %d", got, powered)
	}
}
