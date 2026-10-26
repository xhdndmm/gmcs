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

// TestFurnaceLitPropertyFollowsBurning 验证熔炉方块 lit 属性跟随燃烧状态
// （原版 AbstractFurnaceBlockEntity#setLit）：点燃后 lit=true、燃料耗尽后
// lit=false，并同步写回世界方块状态。
func TestFurnaceLitPropertyFollowsBurning(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "LitFurnace")

	ironOre, err := registry.ItemID("minecraft:iron_ore")
	if err != nil {
		t.Fatal(err)
	}
	coal, err := registry.ItemID("minecraft:coal")
	if err != nil {
		t.Fatal(err)
	}

	x, y, z := placeFurnace(t, instance, "minecraft:furnace")
	state := instance.registerFurnace(world.DimensionOverworld, x, y, z)
	if state == nil {
		t.Fatal("熔炉状态注册失败")
	}
	if furnaceLitOf(instance.testWorld().BlockAt(x, y, z)) {
		t.Fatal("新放置的熔炉 lit 应为 false")
	}

	// 原料 + 燃料 → tick 一次即点燃。
	state.mu.Lock()
	state.slots[0] = itemStackFor(ironOre, 1)
	state.slots[1] = itemStackFor(coal, 1)
	state.mu.Unlock()
	if !instance.tickFurnace(state) {
		t.Fatal("点燃的那一 tick 应报告变更")
	}
	instance.applyFurnaceLit(state)
	if !furnaceLitOf(instance.testWorld().BlockAt(x, y, z)) {
		t.Fatal("燃烧中熔炉 lit 应为 true")
	}

	// 燃料耗尽（直接置 burnRemaining=1 再 tick 一 次）后 lit 回到 false。
	state.mu.Lock()
	state.burnRemaining = 1
	state.slots[0] = item.Empty()
	state.slots[1] = item.Empty()
	state.mu.Unlock()
	instance.tickFurnace(state)
	instance.applyFurnaceLit(state)
	if furnaceLitOf(instance.testWorld().BlockAt(x, y, z)) {
		t.Fatal("熄灭后熔炉 lit 应为 false")
	}
}

// TestContainerClosesWhenOutOfReach 验证玩家离开交互距离后窗口自动关闭
// （原版 ServerPlayer#tick 中的 containerMenu.stillValid 校验）：
// 下一个数据包到达时服务器下发 Close Container 并清理窗口状态。
func TestContainerClosesWhenOutOfReach(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Wanderer")

	cx, cy, cz := chestPositions(t, instance)
	placeChest(t, instance, cx, cy, cz)
	sendUseItemOn(t, conn, cx, cy, cz, 1)
	windowID, _ := openScreen(t, conn)
	_ = expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)

	player := activeSession(t, instance, "Wanderer")
	if player.openContainer == nil {
		t.Fatal("打开箱子后应记录打开的容器")
	}

	// 直接把玩家移到交互距离之外（跳过移动校验，模拟被活塞/传送移动）。
	px, py, pz, yaw, pitch := player.playerPosition()
	player.setPlayerPosition(px+12, py, pz+12, yaw, pitch)

	// 任意数据包都会触发窗口有效性校验。
	sendPlayerInput(t, conn, 0)
	payload := expectPlayPacket(t, conn, protocol.PlayPacketIDCloseContainer)
	_, offset, err := protocol.DecodeVarInt(payload)
	if err != nil {
		t.Fatal(err)
	}
	gotWindow, _, err := protocol.DecodeVarInt(payload[offset:])
	if err != nil {
		t.Fatal(err)
	}
	if gotWindow != windowID {
		t.Fatalf("Close Container 窗口编号 = %d, want %d", gotWindow, windowID)
	}
	if player.openContainer != nil {
		t.Fatal("窗口关闭后 openContainer 应被清理")
	}
}

// leafTestState 返回指定树叶方块的默认（自然生成、persistent=false）状态。
func leafTestState(t *testing.T, name string) uint16 {
	t.Helper()
	state, ok := registry.BlockStateIDs[name]
	if !ok {
		t.Fatalf("缺少方块状态 %s", name)
	}
	if !isNaturalLeafState(state) {
		t.Fatalf("%s 的默认状态应是非 persistent 的自然树叶", name)
	}
	return state
}

// logTestState 返回指定原木方块的默认状态。
func logTestState(t *testing.T, name string) uint16 {
	t.Helper()
	state, ok := registry.BlockStateIDs[name]
	if !ok {
		t.Fatalf("缺少方块状态 %s", name)
	}
	if !isLogState(state) {
		t.Fatalf("%s 应属于 minecraft:logs 标签", name)
	}
	return state
}

// TestLeavesDecayAfterLogRemoved 验证砍掉原木后，6 格内已无原木的自然树叶
// 立即衰减消失（原版 LeavesBlock 的 distance 衰减）。
func TestLeavesDecayAfterLogRemoved(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Lumberjack")

	w := instance.testWorld()
	logState := logTestState(t, "minecraft:oak_log")
	leafState := leafTestState(t, "minecraft:oak_leaves")
	x, y, z := buildSpotNearSpawn(t, instance)
	// 清空判定范围内的天然树木：衰减判定看 6 格内是否存在任何原木，
	// 出生点附近恰好有天然树时会让本用例的“已无原木”前提不成立。
	for dx := -leafDecayRadius; dx <= leafDecayRadius; dx++ {
		for dy := -leafDecayRadius; dy <= leafDecayRadius; dy++ {
			for dz := -leafDecayRadius; dz <= leafDecayRadius; dz++ {
				state := w.BlockAt(x+dx, y+dy, z+dz)
				if isLogState(state) || isLeafState(state) {
					w.SetBlock(x+dx, y+dy, z+dz, world.AirBlock)
				}
			}
		}
	}

	// 树干 + 树干上方的树叶（距离 ≤ 6，仍有原木，不应衰减）。
	if !w.SetBlock(x, y, z, logState) || !w.SetBlock(x, y+1, z, logState) {
		t.Fatal("无法放置原木")
	}
	if !w.SetBlock(x, y+2, z, leafState) {
		t.Fatal("无法放置树叶")
	}
	instance.removeBlockAndBroadcast(world.DimensionOverworld, w, x, y+1, z, logState)
	if w.BlockAt(x, y+2, z) != leafState {
		t.Fatal("原木仍在 6 格内时树叶不应衰减")
	}

	// 砍掉最后一根原木：树叶失去依附，应立刻消失。
	instance.removeBlockAndBroadcast(world.DimensionOverworld, w, x, y, z, logState)
	if got := w.BlockAt(x, y+2, z); got == leafState {
		t.Fatal("原木全部移除后自然树叶应衰减消失")
	}
}

// TestPlacedLeavesDoNotDecay 验证玩家放置的树叶（persistent=true）永不衰减
// （原版 LeavesBlock#getStateForPlacement 设置 persistent）。
func TestPlacedLeavesDoNotDecay(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Gardener")

	w := instance.testWorld()
	logState := logTestState(t, "minecraft:oak_log")
	persistent, ok := leafPersistentState("minecraft:oak_leaves")
	if !ok {
		t.Fatal("缺少橡树叶的玩家放置状态")
	}
	if isNaturalLeafState(persistent) {
		t.Fatal("persistent=true 的树叶不应被判定为自然树叶")
	}
	x, y, z := buildSpotNearSpawn(t, instance)

	if !w.SetBlock(x, y, z, logState) {
		t.Fatal("无法放置原木")
	}
	if !w.SetBlock(x+1, y, z, persistent) {
		t.Fatal("无法放置玩家放置的树叶")
	}
	instance.removeBlockAndBroadcast(world.DimensionOverworld, w, x, y, z, logState)
	if w.BlockAt(x+1, y, z) != persistent {
		t.Fatal("玩家放置的树叶不应衰减")
	}
}
