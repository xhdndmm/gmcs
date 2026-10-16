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

// 本文件覆盖 Shift 快速移动（quickMoveStack）与原版 1.21.11 的对齐验证：
// 每个窗口的分支顺序、移动失败不回退、moveItemStackTo 的方向（reverse）语义。
// 方向语义依据：反编译 AbstractContainerMenu#moveItemStackTo 与官方服务端实测
// （合成结果槽 move(9,45,true) 落位快捷栏末尾、合成格 move(9,45,false) 落位主背包左上）。

// TestFurnaceQuickMoveInsertsInputAndFuel 验证熔炉窗口的 Shift 快捷移动对齐
// 原版 AbstractFurnaceMenu#quickMoveStack：
//   - 可烹饪物品只尝试原料槽（可烹饪且是燃料也优先原料槽）；
//   - 燃料物品只尝试燃料槽；
//   - 原料槽被占导致移动失败时不会回退到燃料槽或背包（物品留在原槽）；
//   - 其余物品在主背包 ↔ 快捷栏之间移动。
func TestFurnaceQuickMoveInsertsInputAndFuel(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.GameMode = "creative"
	instance, conn := joinServer(t, cfg, "FurnaceMove")

	logID, err := registry.ItemID("minecraft:oak_log")
	if err != nil {
		t.Fatal(err)
	}
	coalID, err := registry.ItemID("minecraft:coal")
	if err != nil {
		t.Fatal(err)
	}
	oreID, err := registry.ItemID("minecraft:iron_ore")
	if err != nil {
		t.Fatal(err)
	}
	dirtID, err := registry.ItemID("minecraft:dirt")
	if err != nil {
		t.Fatal(err)
	}

	x, y, z := placeFurnace(t, instance, "minecraft:furnace")
	sendSetCreativeSlot(t, conn, 0, logID, 1)  // 快捷栏 0 → 窗口槽 30
	sendSetCreativeSlot(t, conn, 1, coalID, 2) // 快捷栏 1 → 窗口槽 31
	sendSetCreativeSlot(t, conn, 2, dirtID, 5) // 快捷栏 2 → 窗口槽 32
	sendSetCreativeSlot(t, conn, 3, oreID, 3)  // 快捷栏 3 → 窗口槽 33

	sendUseItemOn(t, conn, x, y, z, 1)
	windowID, _ := openScreen(t, conn)
	expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)

	sendContainerClick(t, conn, windowID, 0, 30, 0, protocol.ClickModeQuickMove) // 原木（可烹饪+燃料）→ 原料槽
	sendContainerClick(t, conn, windowID, 0, 31, 0, protocol.ClickModeQuickMove) // 煤炭 → 燃料槽
	sendContainerClick(t, conn, windowID, 0, 32, 0, protocol.ClickModeQuickMove) // 泥土 → 主背包 ↔ 快捷栏
	sendContainerClick(t, conn, windowID, 0, 33, 0, protocol.ClickModeQuickMove) // 铁矿石：原料槽已占 → 留在原槽

	player := activeSession(t, instance, "FurnaceMove")
	processBarrier(t, conn)
	state := instance.registerFurnace(world.DimensionOverworld, x, y, z)
	if state == nil {
		t.Fatal("熔炉状态注册失败")
	}
	state.mu.Lock()
	input, fuel := state.slots[0], state.slots[1]
	state.mu.Unlock()
	if input.ItemID != logID || input.Count != 1 {
		t.Fatalf("原料槽 = %+v, want 1 原木", input)
	}
	if fuel.ItemID != coalID || fuel.Count != 2 {
		t.Fatalf("燃料槽 = %+v, want 2 煤炭", fuel)
	}
	if got := player.inventory.Get(0); !got.IsEmpty() {
		t.Fatalf("快捷栏 0 应为空，得到 %+v", got)
	}
	if got := player.inventory.Get(1); !got.IsEmpty() {
		t.Fatalf("快捷栏 1 应为空，得到 %+v", got)
	}
	// 泥土：快捷栏 2 → 主背包第一个空位（9）。
	if got := player.inventory.Get(9); got.ItemID != dirtID || got.Count != 5 {
		t.Fatalf("主背包 9 = %+v, want 5 泥土", got)
	}
	// 铁矿石：移动失败（原料槽被原木占用），不回退，留在原槽。
	if got := player.inventory.Get(3); got.ItemID != oreID || got.Count != 3 {
		t.Fatalf("快捷栏 3 = %+v, want 3 铁矿石（应留在原槽）", got)
	}
}

// TestFurnaceResultQuickMoveFillsHotbarLast 验证熔炉结果槽的 Shift 取走
// （原版 move(3, 39, true)）反向填充：优先快捷栏末尾的空槽。
func TestFurnaceResultQuickMoveFillsHotbarLast(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "FurnaceResult")

	ingotID, err := registry.ItemID("minecraft:iron_ingot")
	if err != nil {
		t.Fatal(err)
	}
	x, y, z := placeFurnace(t, instance, "minecraft:furnace")
	sendUseItemOn(t, conn, x, y, z, 1)
	windowID, _ := openScreen(t, conn)
	expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)

	player := activeSession(t, instance, "FurnaceResult")
	processBarrier(t, conn)
	state := instance.registerFurnace(world.DimensionOverworld, x, y, z)
	if state == nil {
		t.Fatal("熔炉状态注册失败")
	}
	state.mu.Lock()
	state.slots[2] = itemStackFor(ingotID, 1)
	state.mu.Unlock()

	sendContainerClick(t, conn, windowID, 0, 2, 0, protocol.ClickModeQuickMove)
	processBarrier(t, conn)
	// 熔炉窗口玩家区：3–29 主背包、30–38 快捷栏；反向填充 → 窗口槽 38 = 快捷栏 8。
	if got := player.inventory.Get(8); got.ItemID != ingotID || got.Count != 1 {
		t.Fatalf("快捷栏 8 = %+v, want 1 铁锭（反向填充优先快捷栏末尾）", got)
	}
	if got := player.inventory.Get(9); !got.IsEmpty() {
		t.Fatalf("主背包 9 应为空，得到 %+v", got)
	}
	state.mu.Lock()
	result := state.slots[2]
	state.mu.Unlock()
	if !result.IsEmpty() {
		t.Fatalf("结果槽应被清空，得到 %+v", result)
	}
}

// TestBrewingQuickMoveInserts 验证酿造台窗口的 Shift 快捷移动：
// 玻璃瓶 → 瓶子槽（每个瓶位上限 1，会分散到多个瓶位）；地狱疣 → 材料槽；
// 烈焰粉 → 燃料槽；其余物品主背包 ↔ 快捷栏。
func TestBrewingQuickMoveInserts(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.GameMode = "creative"
	instance, conn := joinServer(t, cfg, "BrewMove")

	bottleID, err := registry.ItemID("minecraft:glass_bottle")
	if err != nil {
		t.Fatal(err)
	}
	wartID, err := registry.ItemID("minecraft:nether_wart")
	if err != nil {
		t.Fatal(err)
	}
	powderID, err := registry.ItemID("minecraft:blaze_powder")
	if err != nil {
		t.Fatal(err)
	}
	dirtID, err := registry.ItemID("minecraft:dirt")
	if err != nil {
		t.Fatal(err)
	}

	x, y, z, brewState := placeBrewingStand(t, instance)
	if brewState == nil {
		t.Fatal("酿造台状态注册失败")
	}
	sendSetCreativeSlot(t, conn, 0, bottleID, 5) // 快捷栏 0 → 窗口槽 32
	sendSetCreativeSlot(t, conn, 1, wartID, 2)   // 快捷栏 1 → 窗口槽 33
	sendSetCreativeSlot(t, conn, 2, powderID, 2) // 快捷栏 2 → 窗口槽 34
	sendSetCreativeSlot(t, conn, 3, dirtID, 1)   // 快捷栏 3 → 窗口槽 35

	sendUseItemOn(t, conn, x, y, z, 1)
	windowID, _ := openScreen(t, conn)
	expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)

	sendContainerClick(t, conn, windowID, 0, 32, 0, protocol.ClickModeQuickMove)
	sendContainerClick(t, conn, windowID, 0, 33, 0, protocol.ClickModeQuickMove)
	sendContainerClick(t, conn, windowID, 0, 34, 0, protocol.ClickModeQuickMove)
	sendContainerClick(t, conn, windowID, 0, 35, 0, protocol.ClickModeQuickMove)

	player := activeSession(t, instance, "BrewMove")
	processBarrier(t, conn)
	state := brewState
	state.mu.Lock()
	slots := append([]item.Stack(nil), state.slots[:]...)
	state.mu.Unlock()
	for i := 0; i <= 2; i++ {
		if slots[i].ItemID != bottleID || slots[i].Count != 1 {
			t.Fatalf("瓶子槽 %d = %+v, want 1 玻璃瓶（每瓶位上限 1）", i, slots[i])
		}
	}
	if slots[3].ItemID != wartID || slots[3].Count != 2 {
		t.Fatalf("材料槽 = %+v, want 2 地狱疣", slots[3])
	}
	if slots[4].ItemID != powderID || slots[4].Count != 2 {
		t.Fatalf("燃料槽 = %+v, want 2 烈焰粉", slots[4])
	}
	// 5 个玻璃瓶只能进 3 个瓶位，剩余 2 个留在快捷栏 0。
	if got := player.inventory.Get(0); got.ItemID != bottleID || got.Count != 2 {
		t.Fatalf("快捷栏 0 = %+v, want 2 玻璃瓶（瓶位已满的剩余）", got)
	}
	// 泥土：快捷栏 3 → 主背包第一个空位（9）。
	if got := player.inventory.Get(9); got.ItemID != dirtID || got.Count != 1 {
		t.Fatalf("主背包 9 = %+v, want 1 泥土", got)
	}
}

// TestCraftResultQuickMoveFillsHotbarLast 验证玩家物品栏窗口（窗口 0）的合成
// 结果槽 Shift 取走：原版 move(9, 45, true) 反向填充 → 先占快捷栏末尾（槽 44）。
func TestCraftResultQuickMoveFillsHotbarLast(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.GameMode = "creative"
	instance, conn := joinServer(t, cfg, "CraftReverse")

	logID, err := registry.ItemID("minecraft:oak_log")
	if err != nil {
		t.Fatal(err)
	}
	planksID, err := registry.ItemID("minecraft:oak_planks")
	if err != nil {
		t.Fatal(err)
	}

	sendSetCreativeSlot(t, conn, 0, logID, 1)
	// 拾起原木放入合成格左上（窗口槽 1）。
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 36, 0, protocol.ClickModePickup)
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 1, 0, protocol.ClickModePickup)
	// Shift 点击结果槽（窗口槽 0）。
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 0, 1, protocol.ClickModeQuickMove)

	player := activeSession(t, instance, "CraftReverse")
	processBarrier(t, conn)
	// 窗口 0 的槽 44 = 快捷栏 8。
	if got := player.inventory.Get(8); got.ItemID != planksID || got.Count != 4 {
		t.Fatalf("快捷栏 8 = %+v, want 4 木板（反向填充优先快捷栏末尾）", got)
	}
	if got := player.inventory.Get(9); !got.IsEmpty() {
		t.Fatalf("主背包 9 应为空，得到 %+v", got)
	}
	if got := player.inventory.Get(0); !got.IsEmpty() {
		t.Fatalf("原木应被消耗，快捷栏 0 = %+v", got)
	}
}

// TestChestQuickMoveFillsHotbarLast 验证箱子 → 玩家（原版 ChestMenu
// move(n, n+36, true)）反向填充：优先快捷栏末尾（窗口槽 62 = 快捷栏 8）。
func TestChestQuickMoveFillsHotbarLast(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "ChestReverse")

	appleID, err := registry.ItemID("minecraft:apple")
	if err != nil {
		t.Fatal(err)
	}
	cx, cy, cz := chestPositions(t, instance)
	placeChest(t, instance, cx, cy, cz)
	// 预置内容：容器槽 0 放 10 个苹果（直接写方块实体，打开时读取）。
	if err := instance.testWorld().SetBlockEntity(cx, cy, cz, world.BlockEntity{
		TypeID: 1, // minecraft:chest 的方块实体类型（由 resolveContainerDefs 校验）
		Items:  []world.ContainerItem{{ItemID: appleID, Count: 10}},
	}); err != nil {
		t.Fatal(err)
	}
	sendUseItemOn(t, conn, cx, cy, cz, 1)
	windowID, _ := openScreen(t, conn)
	expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)

	player := activeSession(t, instance, "ChestReverse")
	sendContainerClick(t, conn, windowID, 0, 0, 0, protocol.ClickModeQuickMove)
	processBarrier(t, conn)
	// 箱子窗口：0–26 容器、27–53 主背包、54–62 快捷栏。
	if got := player.inventory.Get(8); got.ItemID != appleID || got.Count != 10 {
		t.Fatalf("快捷栏 8 = %+v, want 10 苹果（反向填充优先快捷栏末尾）", got)
	}
	if got := player.inventory.Get(9); !got.IsEmpty() {
		t.Fatalf("主背包 9 应为空，得到 %+v", got)
	}
	entity, err := instance.testWorld().BlockEntityAt(cx, cy, cz)
	if err != nil {
		t.Fatal(err)
	}
	if len(entity.Items) != 0 && !entity.Items[0].IsEmpty() {
		t.Fatalf("容器槽 0 应被清空，得到 %+v", entity.Items)
	}
}

// TestEnderChestQuickMoveInsertsIntoEnderChest 验证末影箱窗口的玩家槽位
// Shift 点击会存入末影箱（原版末影箱以 ChestMenu 打开），而不是在背包内换位。
func TestEnderChestQuickMoveInsertsIntoEnderChest(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.GameMode = "creative"
	instance, conn := joinServer(t, cfg, "EnderMove")

	appleID, err := registry.ItemID("minecraft:apple")
	if err != nil {
		t.Fatal(err)
	}
	x, y, z := placeEnderChest(t, instance)
	sendSetCreativeSlot(t, conn, 0, appleID, 3)
	sendUseItemOn(t, conn, x, y, z, 1)
	windowID, _ := openScreen(t, conn)
	expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)

	// 末影箱窗口：0–26 箱槽、27–53 主背包、54–62 快捷栏；快捷栏 0 = 窗口 54。
	sendContainerClick(t, conn, windowID, 0, 54, 0, protocol.ClickModeQuickMove)
	player := activeSession(t, instance, "EnderMove")
	processBarrier(t, conn)
	if got := player.enderChest.Get(0); got.ItemID != appleID || got.Count != 3 {
		t.Fatalf("末影箱槽 0 = %+v, want 3 苹果", got)
	}
	if got := player.inventory.Get(0); !got.IsEmpty() {
		t.Fatalf("快捷栏 0 应为空，得到 %+v", got)
	}
}

// TestHopperQuickMoveBothDirections 验证漏斗窗口：
// 玩家物品 → 漏斗（正向 move(0,5,false)）；漏斗物品 → 玩家（反向 move(5,41,true)，
// 优先快捷栏末尾，窗口槽 40 = 快捷栏 8）。
func TestHopperQuickMoveBothDirections(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.GameMode = "creative"
	instance, conn := joinServer(t, cfg, "HopperMove")

	appleID, err := registry.ItemID("minecraft:apple")
	if err != nil {
		t.Fatal(err)
	}
	hx, hy, hz := buildSpotNearSpawn(t, instance)
	hopper := hopperAt(t, instance, hx, hy, hz, "down")
	sendSetCreativeSlot(t, conn, 0, appleID, 4)

	sendUseItemOn(t, conn, hx, hy, hz, 1)
	windowID, _ := openScreen(t, conn)
	expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)

	// 漏斗窗口：0–4 漏斗、5–31 主背包、32–40 快捷栏；快捷栏 0 = 窗口 32。
	sendContainerClick(t, conn, windowID, 0, 32, 0, protocol.ClickModeQuickMove)
	player := activeSession(t, instance, "HopperMove")
	processBarrier(t, conn)
	hopper.mu.Lock()
	slot0 := hopper.slots[0]
	hopper.mu.Unlock()
	if slot0.ItemID != appleID || slot0.Count != 4 {
		t.Fatalf("漏斗槽 0 = %+v, want 4 苹果", slot0)
	}
	if got := player.inventory.Get(0); !got.IsEmpty() {
		t.Fatalf("快捷栏 0 应为空，得到 %+v", got)
	}

	// 漏斗 → 玩家：反向填充到快捷栏末尾（窗口槽 40 → 快捷栏 8）。
	sendContainerClick(t, conn, windowID, 0, 0, 0, protocol.ClickModeQuickMove)
	processBarrier(t, conn)
	if got := player.inventory.Get(8); got.ItemID != appleID || got.Count != 4 {
		t.Fatalf("快捷栏 8 = %+v, want 4 苹果（反向填充优先快捷栏末尾）", got)
	}
	hopper.mu.Lock()
	slot0 = hopper.slots[0]
	hopper.mu.Unlock()
	if !slot0.IsEmpty() {
		t.Fatalf("漏斗槽 0 应被清空，得到 %+v", slot0)
	}
}

// TestStonecutterQuickMoveInsertsInput 验证切石机窗口：是切石配方原料的物品
// 只尝试输入槽（原版 StonecutterMenu 属性集判断）；非原料物品主背包 ↔ 快捷栏。
func TestStonecutterQuickMoveInsertsInput(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.GameMode = "creative"
	instance, conn := joinServer(t, cfg, "CutterMove")

	stoneID, err := registry.ItemID("minecraft:stone")
	if err != nil {
		t.Fatal(err)
	}
	dirtID, err := registry.ItemID("minecraft:dirt")
	if err != nil {
		t.Fatal(err)
	}
	x, y, z := placeMenuBlock(t, instance, "minecraft:stonecutter")
	sendSetCreativeSlot(t, conn, 0, stoneID, 2)
	sendSetCreativeSlot(t, conn, 1, dirtID, 1)
	sendUseItemOn(t, conn, x, y, z, 1)
	windowID, _ := openScreen(t, conn)
	expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)

	// 切石机窗口：0 输入、1 结果、2–28 主背包、29–37 快捷栏；快捷栏 0 = 29。
	sendContainerClick(t, conn, windowID, 0, 29, 0, protocol.ClickModeQuickMove)
	sendContainerClick(t, conn, windowID, 0, 30, 0, protocol.ClickModeQuickMove)

	player := activeSession(t, instance, "CutterMove")
	processBarrier(t, conn)
	state := player.getStonecutter()
	if state == nil {
		t.Fatal("切石机窗口状态缺失")
	}
	if input := state.inputStack(); input.ItemID != stoneID || input.Count != 2 {
		t.Fatalf("输入槽 = %+v, want 2 石头", input)
	}
	// 泥土不是切石原料：快捷栏 1 → 主背包第一个空位（窗口 2 = 物品栏 9）。
	if got := player.inventory.Get(9); got.ItemID != dirtID || got.Count != 1 {
		t.Fatalf("主背包 9 = %+v, want 1 泥土", got)
	}
}

// TestAnvilQuickMoveNoFallback 验证铁砧窗口（原版 ItemCombinerMenu 的 b() 恒真）：
// 玩家物品只尝试输入槽；两个输入槽都被占用时移动失败且不回退（物品留在原槽）。
func TestAnvilQuickMoveNoFallback(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.GameMode = "creative"
	instance, conn := joinServer(t, cfg, "AnvilMove")

	diamondID, err := registry.ItemID("minecraft:diamond")
	if err != nil {
		t.Fatal(err)
	}
	emeraldID, err := registry.ItemID("minecraft:emerald")
	if err != nil {
		t.Fatal(err)
	}
	ingotID, err := registry.ItemID("minecraft:iron_ingot")
	if err != nil {
		t.Fatal(err)
	}
	x, y, z := placeMenuBlock(t, instance, "minecraft:anvil")
	sendSetCreativeSlot(t, conn, 0, diamondID, 1)
	sendSetCreativeSlot(t, conn, 1, emeraldID, 1)
	sendSetCreativeSlot(t, conn, 2, ingotID, 2)
	sendUseItemOn(t, conn, x, y, z, 1)
	windowID, _ := openScreen(t, conn)
	expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)

	// 铁砧窗口：0/1 输入、2 结果、3–29 主背包、30–38 快捷栏；快捷栏 0 = 30。
	sendContainerClick(t, conn, windowID, 0, 30, 0, protocol.ClickModeQuickMove) // 钻石 → 输入 0
	sendContainerClick(t, conn, windowID, 0, 31, 0, protocol.ClickModeQuickMove) // 绿宝石 → 输入 1
	sendContainerClick(t, conn, windowID, 0, 32, 0, protocol.ClickModeQuickMove) // 铁锭 → 失败，留在原槽

	player := activeSession(t, instance, "AnvilMove")
	processBarrier(t, conn)
	state := player.getAnvil()
	if state == nil {
		t.Fatal("铁砧窗口状态缺失")
	}
	if in0 := state.inputStack(0); in0.ItemID != diamondID {
		t.Fatalf("输入槽 0 = %+v, want 钻石", in0)
	}
	if in1 := state.inputStack(1); in1.ItemID != emeraldID {
		t.Fatalf("输入槽 1 = %+v, want 绿宝石", in1)
	}
	if got := player.inventory.Get(2); got.ItemID != ingotID || got.Count != 2 {
		t.Fatalf("快捷栏 2 = %+v, want 2 铁锭（应留在原槽，不回退背包）", got)
	}
}

// TestSmithingQuickMoveInserts 验证锻造台窗口（原版 SmithingMenu 的 b(stack)
// 由三个输入槽的物品集合决定）：模板/基底/添加物分别进对应槽；均不匹配时
// 主背包 ↔ 快捷栏。
func TestSmithingQuickMoveInserts(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.GameMode = "creative"
	instance, conn := joinServer(t, cfg, "SmithMove")

	templateID, err := registry.ItemID("minecraft:netherite_upgrade_smithing_template")
	if err != nil {
		t.Fatal(err)
	}
	swordID, err := registry.ItemID("minecraft:diamond_sword")
	if err != nil {
		t.Fatal(err)
	}
	ingotID, err := registry.ItemID("minecraft:netherite_ingot")
	if err != nil {
		t.Fatal(err)
	}
	dirtID, err := registry.ItemID("minecraft:dirt")
	if err != nil {
		t.Fatal(err)
	}
	x, y, z := placeMenuBlock(t, instance, "minecraft:smithing_table")
	sendSetCreativeSlot(t, conn, 0, templateID, 1)
	sendSetCreativeSlot(t, conn, 1, swordID, 1)
	sendSetCreativeSlot(t, conn, 2, ingotID, 1)
	sendSetCreativeSlot(t, conn, 3, dirtID, 1)
	sendUseItemOn(t, conn, x, y, z, 1)
	windowID, _ := openScreen(t, conn)
	expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)

	// 锻造台窗口：0–2 输入、3 结果、4–30 主背包、31–39 快捷栏；快捷栏 0 = 31。
	sendContainerClick(t, conn, windowID, 0, 31, 0, protocol.ClickModeQuickMove) // 模板
	sendContainerClick(t, conn, windowID, 0, 32, 0, protocol.ClickModeQuickMove) // 基底
	sendContainerClick(t, conn, windowID, 0, 33, 0, protocol.ClickModeQuickMove) // 添加物
	sendContainerClick(t, conn, windowID, 0, 34, 0, protocol.ClickModeQuickMove) // 泥土 → 主背包

	player := activeSession(t, instance, "SmithMove")
	processBarrier(t, conn)
	state := player.getSmithing()
	if state == nil {
		t.Fatal("锻造台窗口状态缺失")
	}
	if got := state.inputStack(0); got.ItemID != templateID {
		t.Fatalf("模板槽 = %+v, want 模板", got)
	}
	if got := state.inputStack(1); got.ItemID != swordID {
		t.Fatalf("基底槽 = %+v, want 钻石剑", got)
	}
	if got := state.inputStack(2); got.ItemID != ingotID {
		t.Fatalf("添加物槽 = %+v, want 下界合金锭", got)
	}
	// 泥土不匹配任何输入集合 → 快捷栏 → 主背包第一个空位（窗口 4 = 物品栏 9）。
	if got := player.inventory.Get(9); got.ItemID != dirtID || got.Count != 1 {
		t.Fatalf("主背包 9 = %+v, want 1 泥土", got)
	}
}

// TestGrindstoneQuickMoveConditional 验证砂轮窗口（原版 GrindstoneMenu）：
// 输入区未满时只尝试输入槽（物品不被接受则失败，不回退）；两个输入槽
// 都非空时主背包 ↔ 快捷栏。
func TestGrindstoneQuickMoveConditional(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.GameMode = "creative"
	instance, conn := joinServer(t, cfg, "GrindMove")

	pickID, err := registry.ItemID("minecraft:diamond_pickaxe")
	if err != nil {
		t.Fatal(err)
	}
	dirtID, err := registry.ItemID("minecraft:dirt")
	if err != nil {
		t.Fatal(err)
	}
	x, y, z := placeMenuBlock(t, instance, "minecraft:grindstone")
	sendSetCreativeSlot(t, conn, 0, pickID, 1)
	sendSetCreativeSlot(t, conn, 1, dirtID, 1)
	sendUseItemOn(t, conn, x, y, z, 1)
	windowID, _ := openScreen(t, conn)
	expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)

	// 砂轮窗口：0/1 输入、2 结果、3–29 主背包、30–38 快捷栏；快捷栏 0 = 30。
	sendContainerClick(t, conn, windowID, 0, 30, 0, protocol.ClickModeQuickMove) // 镐 → 输入 0
	sendContainerClick(t, conn, windowID, 0, 31, 0, protocol.ClickModeQuickMove) // 泥土：输入未满 → 只试输入，失败留在原槽

	player := activeSession(t, instance, "GrindMove")
	processBarrier(t, conn)
	state := player.getGrindstone()
	if state == nil {
		t.Fatal("砂轮窗口状态缺失")
	}
	if got := state.inputStack(0); got.ItemID != pickID {
		t.Fatalf("输入槽 0 = %+v, want 钻石镐", got)
	}
	if got := player.inventory.Get(1); got.ItemID != dirtID || got.Count != 1 {
		t.Fatalf("快捷栏 1 = %+v, want 1 泥土（应留在原槽）", got)
	}

	// 两个输入槽都非空 → 主背包 ↔ 快捷栏（泥土从快捷栏进主背包）。
	state.setInputs(itemStackFor(pickID, 1), itemStackFor(pickID, 1))
	sendContainerClick(t, conn, windowID, 0, 31, 0, protocol.ClickModeQuickMove)
	processBarrier(t, conn)
	if got := player.inventory.Get(9); got.ItemID != dirtID || got.Count != 1 {
		t.Fatalf("主背包 9 = %+v, want 1 泥土（两个输入槽都非空时走背包换位）", got)
	}
	if got := player.inventory.Get(1); !got.IsEmpty() {
		t.Fatalf("快捷栏 1 应为空，得到 %+v", got)
	}
}

// TestCraftingTableQuickMoveInsertsGridThenFallsBack 验证工作台窗口：
// 玩家槽位先尝试合成格（原版 move(1, 10, false)），合成格放不下时才退回
// 主背包 ↔ 快捷栏。
func TestCraftingTableQuickMoveInsertsGridThenFallsBack(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.GameMode = "creative"
	instance, conn := joinServer(t, cfg, "TableMove")

	planksID, err := registry.ItemID("minecraft:oak_planks")
	if err != nil {
		t.Fatal(err)
	}
	cobbleID, err := registry.ItemID("minecraft:cobblestone")
	if err != nil {
		t.Fatal(err)
	}
	x, y, z := placeMenuBlock(t, instance, "minecraft:crafting_table")
	sendSetCreativeSlot(t, conn, 0, planksID, 2)
	sendSetCreativeSlot(t, conn, 1, cobbleID, 1)
	sendSetCreativeSlot(t, conn, 2, planksID, 3)
	sendUseItemOn(t, conn, x, y, z, 1)
	windowID, _ := openScreen(t, conn)
	expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)

	// 工作台窗口：0 结果、1–9 合成格、10–36 主背包、37–45 快捷栏；快捷栏 i = 37+i。
	sendContainerClick(t, conn, windowID, 0, 37, 0, protocol.ClickModeQuickMove) // 木板 → 合成格 1
	sendContainerClick(t, conn, windowID, 0, 38, 0, protocol.ClickModeQuickMove) // 圆石 → 合成格 2

	player := activeSession(t, instance, "TableMove")
	processBarrier(t, conn)
	craft := player.getOpenCraft()
	if craft == nil {
		t.Fatal("工作台窗口状态缺失")
	}
	grid := craft.gridSnapshot()
	if len(grid) != 9 {
		t.Fatalf("合成格数量 = %d, want 9", len(grid))
	}
	if grid[0].ItemID != planksID || grid[0].Count != 2 {
		t.Fatalf("合成格 1 = %+v, want 2 木板", grid[0])
	}
	if grid[1].ItemID != cobbleID || grid[1].Count != 1 {
		t.Fatalf("合成格 2 = %+v, want 1 圆石", grid[1])
	}

	// 合成格被其他物品占满 → 快捷栏 2（木板）退回主背包（窗口 10 = 物品栏 9）。
	craft.mu.Lock()
	for i := range craft.grid {
		craft.grid[i] = itemStackFor(cobbleID, 1)
	}
	craft.mu.Unlock()
	sendContainerClick(t, conn, windowID, 0, 39, 0, protocol.ClickModeQuickMove)
	processBarrier(t, conn)
	if got := player.inventory.Get(9); got.ItemID != planksID || got.Count != 3 {
		t.Fatalf("主背包 9 = %+v, want 3 木板（合成格放不下时退回背包）", got)
	}
}

// TestEnchantingQuickMoveSingleItem 验证附魔台窗口：普通物品 Shift 点击只把
// 1 个物品放入输入槽（原版 split(1)），青金石进青金石槽。
func TestEnchantingQuickMoveSingleItem(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.GameMode = "creative"
	cfg.StartingItems = nil
	instance, conn := joinServer(t, cfg, "EnchantMove")

	swordID, err := registry.ItemID("minecraft:diamond_sword")
	if err != nil {
		t.Fatal(err)
	}
	lapisID, err := registry.ItemID("minecraft:lapis_lazuli")
	if err != nil {
		t.Fatal(err)
	}
	x, y, z := placeMenuBlock(t, instance, "minecraft:enchanting_table")
	sendSetCreativeSlot(t, conn, 0, swordID, 1)
	sendSetCreativeSlot(t, conn, 1, lapisID, 5)
	sendUseItemOn(t, conn, x, y, z, 1)
	windowID, _ := openScreen(t, conn)
	expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)

	// 附魔台窗口：0 输入、1 青金石、2–28 主背包、29–37 快捷栏；快捷栏 0 = 29。
	sendContainerClick(t, conn, windowID, 0, 29, 0, protocol.ClickModeQuickMove)
	player := activeSession(t, instance, "EnchantMove")
	processBarrier(t, conn)
	if got := player.inventory.Get(0); !got.IsEmpty() {
		t.Fatalf("快捷栏 0 应为空（剑已放入输入槽），得到 %+v", got)
	}
	state := player.getEnchanting()
	if state == nil {
		t.Fatal("附魔台窗口状态缺失")
	}
	if got := state.inputStack(enchantingInputSlot); got.ItemID != swordID || got.Count != 1 {
		t.Fatalf("输入槽 = %+v, want 1 剑", got)
	}

	// 青金石 → 青金石槽（整栈）。
	sendContainerClick(t, conn, windowID, 0, 30, 0, protocol.ClickModeQuickMove)
	processBarrier(t, conn)
	if got := state.inputStack(enchantingLapisSlot); got.ItemID != lapisID || got.Count != 5 {
		t.Fatalf("青金石槽 = %+v, want 5 青金石", got)
	}
	if got := player.inventory.Get(1); !got.IsEmpty() {
		t.Fatalf("快捷栏 1 应为空，得到 %+v", got)
	}
}
