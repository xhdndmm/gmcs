//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// placeBrewingStand 在玩家附近放置酿造台并注册运行时状态。
func placeBrewingStand(t *testing.T, instance *Server) (int, int, int, *brewingState) {
	t.Helper()
	x, y, z := placeFurnace(t, instance, "minecraft:brewing_stand")
	state := instance.registerBrewing(world.DimensionOverworld, x, y, z)
	if state == nil {
		t.Fatal("酿造台状态注册失败")
	}
	return x, y, z, state
}

// mustItemID 是测试辅助：按名称解析物品 ID。
func mustItemID(t *testing.T, name string) int32 {
	t.Helper()
	id, err := registry.ItemID(name)
	if err != nil {
		t.Fatalf("缺少物品 %s: %v", name, err)
	}
	return id
}

// TestBrewingStandBrewsWaterToAwkward 验证酿造主流程：
// 水瓶 + 下界疣 + 烈焰粉 → 400 tick → 粗制的药水（燃料消耗 1 次、材料消耗 1 个）。
func TestBrewingStandBrewsWaterToAwkward(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Brewer")

	potion := mustItemID(t, "minecraft:potion")
	wart := mustItemID(t, "minecraft:nether_wart")
	blaze := mustItemID(t, "minecraft:blaze_powder")

	_, _, _, state := placeBrewingStand(t, instance)
	state.mu.Lock()
	state.slots[0] = item.Stack{ItemID: potion, Count: 1} // 水瓶（无组件 = 默认水）
	state.slots[3] = item.Stack{ItemID: wart, Count: 1}
	state.slots[4] = item.Stack{ItemID: blaze, Count: 1}
	state.mu.Unlock()

	// 第 1 tick：补充燃料（20 次）并立即开始酿造（消耗 1 次）。
	if !instance.tickBrewing(state) {
		t.Fatal("酿造台应报告变更")
	}
	state.mu.Lock()
	brewTime := state.brewTime
	fuel := state.fuel
	fuelSlot := state.slots[4]
	ingredient := state.slots[3]
	state.mu.Unlock()
	if brewTime != brewingTimeTicks {
		t.Fatalf("酿造时长 = %d, want %d", brewTime, brewingTimeTicks)
	}
	if fuel != brewingFuelUses-1 {
		t.Fatalf("燃料 = %d, want %d", fuel, brewingFuelUses-1)
	}
	if !fuelSlot.IsEmpty() {
		t.Fatalf("烈焰粉应被消耗，剩余 %+v", fuelSlot)
	}
	if ingredient.ItemID != wart {
		t.Fatalf("材料槽 = %+v, want 下界疣", ingredient)
	}

	// 推进 400 tick 完成酿造。
	for i := 0; i < brewingTimeTicks; i++ {
		instance.tickBrewing(state)
	}
	state.mu.Lock()
	bottle := state.slots[0]
	ingredient = state.slots[3]
	brewTime = state.brewTime
	state.mu.Unlock()
	if brewTime != 0 {
		t.Fatalf("进度应归零，brewTime=%d", brewTime)
	}
	if !ingredient.IsEmpty() {
		t.Fatalf("材料应被消耗，剩余 %+v", ingredient)
	}
	if bottle.ItemID != potion || bottle.PotionID != registry.PotionAwkward+1 {
		t.Fatalf("产物 = %+v, want 粗制的药水（PotionID=%d）", bottle, registry.PotionAwkward+1)
	}
}

// TestBrewingUpgradeChain 验证连续酿造（粗制 → 夜视 → 延长夜视）与进度持久化。
func TestBrewingUpgradeChain(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Chainer")

	potion := mustItemID(t, "minecraft:potion")
	carrot := mustItemID(t, "minecraft:golden_carrot")
	redstone := mustItemID(t, "minecraft:redstone")
	blaze := mustItemID(t, "minecraft:blaze_powder")

	_, _, _, state := placeBrewingStand(t, instance)
	brew := func(ingredientID int32) {
		state.mu.Lock()
		state.slots[3] = item.Stack{ItemID: ingredientID, Count: 1}
		if state.fuel <= 0 {
			state.slots[4] = item.Stack{ItemID: blaze, Count: 1}
		}
		state.mu.Unlock()
		for i := 0; i < brewingTimeTicks+1; i++ {
			instance.tickBrewing(state)
		}
	}

	state.mu.Lock()
	state.slots[0] = item.Stack{ItemID: potion, Count: 1, PotionID: registry.PotionAwkward + 1}
	state.mu.Unlock()

	brew(carrot)
	state.mu.Lock()
	got := state.slots[0]
	state.mu.Unlock()
	if got.ItemID != potion || got.PotionID != registry.PotionNightVision+1 {
		t.Fatalf("粗制 + 金胡萝卜 = %+v, want 夜视", got)
	}

	brew(redstone)
	state.mu.Lock()
	got = state.slots[0]
	state.mu.Unlock()
	if got.ItemID != potion || got.PotionID != registry.PotionLongNightVision+1 {
		t.Fatalf("夜视 + 红石 = %+v, want 延长夜视", got)
	}
}

// TestBrewingContainerRecipeSplash 验证容器配方：
// 粗制药水 + 火药 → 喷溅粗制药水（药水种类保留、物品变为喷溅药水）。
func TestBrewingContainerRecipeSplash(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Splasher")

	potion := mustItemID(t, "minecraft:potion")
	splash := mustItemID(t, "minecraft:splash_potion")
	gunpowder := mustItemID(t, "minecraft:gunpowder")
	blaze := mustItemID(t, "minecraft:blaze_powder")

	_, _, _, state := placeBrewingStand(t, instance)
	state.mu.Lock()
	state.slots[0] = item.Stack{ItemID: potion, Count: 1, PotionID: registry.PotionAwkward + 1}
	state.slots[3] = item.Stack{ItemID: gunpowder, Count: 1}
	state.slots[4] = item.Stack{ItemID: blaze, Count: 1}
	state.mu.Unlock()

	for i := 0; i < brewingTimeTicks+1; i++ {
		instance.tickBrewing(state)
	}
	state.mu.Lock()
	bottle := state.slots[0]
	state.mu.Unlock()
	if bottle.ItemID != splash || bottle.PotionID != registry.PotionAwkward+1 {
		t.Fatalf("产物 = %+v, want 喷溅粗制药水（PotionID=%d）", bottle, registry.PotionAwkward+1)
	}
}

// TestBrewingCancelOnIngredientRemoved 验证酿造中途材料被拿走：
// 进度归零、不产出、燃料不退还（原版行为）。
func TestBrewingCancelOnIngredientRemoved(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Canceller")

	potion := mustItemID(t, "minecraft:potion")
	wart := mustItemID(t, "minecraft:nether_wart")
	blaze := mustItemID(t, "minecraft:blaze_powder")

	_, _, _, state := placeBrewingStand(t, instance)
	state.mu.Lock()
	state.slots[0] = item.Stack{ItemID: potion, Count: 1}
	state.slots[3] = item.Stack{ItemID: wart, Count: 1}
	state.slots[4] = item.Stack{ItemID: blaze, Count: 1}
	state.mu.Unlock()

	instance.tickBrewing(state) // 开始酿造
	for i := 0; i < 100; i++ {
		instance.tickBrewing(state)
	}
	// 拿走材料：下一 tick 取消。
	state.mu.Lock()
	state.slots[3] = item.Empty()
	state.mu.Unlock()
	instance.tickBrewing(state)

	state.mu.Lock()
	brewTime := state.brewTime
	fuel := state.fuel
	bottle := state.slots[0]
	state.mu.Unlock()
	if brewTime != 0 {
		t.Fatalf("取消后进度应为 0，得到 %d", brewTime)
	}
	if fuel != brewingFuelUses-1 {
		t.Fatalf("取消不退还燃料，fuel=%d want %d", fuel, brewingFuelUses-1)
	}
	if bottle.PotionID != 0 || bottle.ItemID != potion {
		t.Fatalf("取消后水瓶不应变化：%+v", bottle)
	}
}

// TestBrewingOpenWindow 验证打开酿造台窗口（菜单类型、41 槽与初始属性）。
func TestBrewingOpenWindow(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.GameMode = "creative"
	instance, conn := joinServer(t, cfg, "Watcher")

	x, y, z, state := placeBrewingStand(t, instance)
	state.mu.Lock()
	state.brewTime = 321
	state.fuel = 12
	state.mu.Unlock()

	sendUseItemOn(t, conn, x, y, z, 1)
	windowID, menuType := openScreen(t, conn)
	wantMenu, ok := registry.StaticEntryID("minecraft:menu", "minecraft:brewing_stand")
	if !ok {
		t.Fatal("缺少 brewing_stand 菜单类型")
	}
	if menuType != wantMenu {
		t.Fatalf("菜单类型 = %d, want %d", menuType, wantMenu)
	}
	content := expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)
	gotWindow, slots := containerContent(t, content)
	if gotWindow != windowID {
		t.Fatalf("窗口编号 = %d, want %d", gotWindow, windowID)
	}
	if len(slots) != brewingSlots+36 {
		t.Fatalf("窗口槽位数 = %d, want %d", len(slots), brewingSlots+36)
	}
	// 初始窗口属性：0 = 酿造进度、1 = 燃料。
	for wantIndex, wantValue := range []int16{321, 12} {
		payload := expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerProperty)
		_, offset, err := protocol.DecodeVarInt(payload)
		if err != nil {
			t.Fatal(err)
		}
		gotWindow, size, err := protocol.DecodeVarInt(payload[offset:])
		if err != nil {
			t.Fatal(err)
		}
		offset += size
		gotIndex, offset, err := protocol.DecodeInt16(payload, offset)
		if err != nil {
			t.Fatal(err)
		}
		gotValue, _, err := protocol.DecodeInt16(payload, offset)
		if err != nil {
			t.Fatal(err)
		}
		if gotWindow != windowID || gotIndex != int16(wantIndex) || gotValue != wantValue {
			t.Fatalf("属性包 = (窗口 %d, 索引 %d, 值 %d), want (窗口 %d, 索引 %d, 值 %d)",
				gotWindow, gotIndex, gotValue, windowID, wantIndex, wantValue)
		}
	}
}

// TestBrewingCanPlaceRules 验证窗口槽位放置规则（对照原版各槽 mayPlace）。
func TestBrewingCanPlaceRules(t *testing.T) {
	potion := mustItemID(t, "minecraft:potion")
	splash := mustItemID(t, "minecraft:splash_potion")
	glass := mustItemID(t, "minecraft:glass_bottle")
	wart := mustItemID(t, "minecraft:nether_wart")
	blaze := mustItemID(t, "minecraft:blaze_powder")
	diamond := mustItemID(t, "minecraft:diamond")

	state := &brewingState{}
	backing := brewingSlotBacking{state: state}

	// 空瓶子槽：接受药水类与玻璃瓶，拒绝其他物品。
	if !backing.CanPlace(0, item.Stack{ItemID: potion, Count: 1}) {
		t.Fatal("空瓶子槽应接受水瓶")
	}
	if !backing.CanPlace(2, item.Stack{ItemID: splash, Count: 1}) {
		t.Fatal("空瓶子槽应接受喷溅药水")
	}
	if !backing.CanPlace(1, item.Stack{ItemID: glass, Count: 1}) {
		t.Fatal("空瓶子槽应接受玻璃瓶")
	}
	if backing.CanPlace(0, item.Stack{ItemID: diamond, Count: 1}) {
		t.Fatal("瓶子槽不应接受钻石")
	}
	// 已有瓶子时拒绝再放入。
	state.slots[0] = item.Stack{ItemID: potion, Count: 1}
	if backing.CanPlace(0, item.Stack{ItemID: potion, Count: 1}) {
		t.Fatal("非空瓶子槽不应再接受物品")
	}
	// 材料槽：只接受酿造材料。
	if !backing.CanPlace(3, item.Stack{ItemID: wart, Count: 1}) {
		t.Fatal("材料槽应接受下界疣")
	}
	if backing.CanPlace(3, item.Stack{ItemID: diamond, Count: 1}) {
		t.Fatal("材料槽不应接受钻石")
	}
	// 燃料槽：只接受烈焰粉。
	if !backing.CanPlace(4, item.Stack{ItemID: blaze, Count: 1}) {
		t.Fatal("燃料槽应接受烈焰粉")
	}
	if backing.CanPlace(4, item.Stack{ItemID: diamond, Count: 1}) {
		t.Fatal("燃料槽不应接受钻石")
	}
}

// TestBrewingBreakDropsContents 验证破坏酿造台掉落内容（药水种类保留）。
func TestBrewingBreakDropsContents(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.GameMode = "creative"
	instance, conn := joinServer(t, cfg, "Basher")

	potion := mustItemID(t, "minecraft:potion")

	x, y, z, state := placeBrewingStand(t, instance)
	state.mu.Lock()
	state.slots[0] = item.Stack{ItemID: potion, Count: 1, PotionID: registry.PotionNightVision + 1}
	state.mu.Unlock()

	sendPlayerAction(t, conn, 0, x, y, z, 1)
	expectBlockUpdate(t, conn, x, y, z, int32(world.AirBlock))

	instance.tick() // 推进掉落物一帧
	found := false
	instance.entityMu.Lock()
	for _, e := range instance.items {
		if e.Stack.ItemID == potion && e.Stack.PotionID == registry.PotionNightVision+1 {
			found = true
		}
	}
	instance.entityMu.Unlock()
	if !found {
		t.Fatal("破坏酿造台后应掉落夜视药水（含药水 ID）")
	}
	if count := countBlockEntities(t, instance, x, y, z); count != 0 {
		t.Fatalf("破坏后不应残留方块实体数据（count=%d）", count)
	}
}

// TestBrewingPersistence 验证酿造进度与槽位随方块实体写回并可恢复。
func TestBrewingPersistence(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Saver")

	potion := mustItemID(t, "minecraft:potion")

	x, y, z, state := placeBrewingStand(t, instance)
	state.mu.Lock()
	state.slots[0] = item.Stack{ItemID: potion, Count: 1, PotionID: registry.PotionHealing + 1}
	state.slots[3] = item.Stack{ItemID: mustItemID(t, "minecraft:nether_wart"), Count: 2}
	state.fuel = 9
	state.brewTime = 222
	state.mu.Unlock()

	instance.flushBrewing(state)
	entity, err := instance.testWorld().BlockEntityAt(x, y, z)
	if err != nil {
		t.Fatal(err)
	}
	if entity.BrewTime != 222 || entity.BrewFuel != 9 {
		t.Fatalf("进度未持久化：%+v", entity)
	}
	if len(entity.Items) != brewingSlots || entity.Items[0].PotionID != registry.PotionHealing+1 {
		t.Fatalf("槽位未持久化：%+v", entity.Items)
	}

	// 模拟重新加载：清除运行时状态后从方块实体恢复。
	key := containerKey{dim: world.DimensionOverworld, x: x, y: y, z: z}
	instance.brewingMu.Lock()
	delete(instance.brewings, key)
	instance.brewingMu.Unlock()
	revived := instance.registerBrewing(world.DimensionOverworld, x, y, z)
	if revived == nil {
		t.Fatal("重新注册失败")
	}
	revived.mu.Lock()
	brewTime := revived.brewTime
	fuel := revived.fuel
	bottle := revived.slots[0]
	ingredient := revived.slots[3]
	revived.mu.Unlock()
	if brewTime != 222 || fuel != 9 {
		t.Fatalf("恢复的进度 = (%d, %d), want (222, 9)", brewTime, fuel)
	}
	if bottle.PotionID != registry.PotionHealing+1 {
		t.Fatalf("恢复的药水 = %+v", bottle)
	}
	if ingredient.ItemID == 0 || ingredient.Count != 2 {
		t.Fatalf("恢复的材料 = %+v", ingredient)
	}
}

// TestBrewingBottleStateUpdate 验证 has_bottle 方块状态随瓶子槽变化更新。
func TestBrewingBottleStateUpdate(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "States")

	potion := mustItemID(t, "minecraft:potion")
	x, y, z, state := placeBrewingStand(t, instance)

	// 初始：无瓶子 → 默认状态（全部 false）。
	initial := instance.testWorld().BlockAt(x, y, z)
	name, props, ok := registry.StateProps(initial)
	if !ok || name != "minecraft:brewing_stand" {
		t.Fatalf("初始状态异常：%d %v", initial, props)
	}
	if props["has_bottle_0"] != "false" {
		t.Fatalf("初始 has_bottle_0 = %s, want false", props["has_bottle_0"])
	}

	// 放置瓶子并 tick：has_bottle_0 应变为 true。
	state.mu.Lock()
	state.slots[0] = item.Stack{ItemID: potion, Count: 1}
	state.mu.Unlock()
	instance.tickBrewingStands()

	current := instance.testWorld().BlockAt(x, y, z)
	_, props, ok = registry.StateProps(current)
	if !ok || props["has_bottle_0"] != "true" || props["has_bottle_1"] != "false" {
		t.Fatalf("瓶子状态未更新：%v", props)
	}
}

// TestBrewingWindowClickPlacesItems 通过真实协议点击验证窗口交互：
// 从快捷栏取水瓶放入瓶子槽、取烈焰粉放入燃料槽，并写回方块实体。
func TestBrewingWindowClickPlacesItems(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:potion", "minecraft:blaze_powder"}
	instance, conn := joinServer(t, cfg, "Clicker")

	potion := mustItemID(t, "minecraft:potion")
	blaze := mustItemID(t, "minecraft:blaze_powder")

	x, y, z, state := placeBrewingStand(t, instance)
	sendUseItemOn(t, conn, x, y, z, 1)
	windowID, _ := openScreen(t, conn)
	expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)
	// 打开时下发的两个初始属性（酿造进度与燃料）。
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerProperty)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerProperty)

	// 快捷栏第 0 格（菜单槽 32）取水瓶到光标。
	sendContainerClick(t, conn, windowID, 0, brewingSlots+27, 0, protocol.ClickModePickup)
	if _, _, slot, _ := setContainerSlotPacket(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)); slot != brewingSlots+27 {
		t.Fatalf("取水瓶的槽位 = %d, want %d", slot, brewingSlots+27)
	}
	if stack := cursorItemPacket(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)); stack.ID != potion {
		t.Fatalf("光标应为水瓶，得到 %+v", stack)
	}
	// 放入瓶子槽 0。
	sendContainerClick(t, conn, windowID, 0, 0, 0, protocol.ClickModePickup)
	if _, _, slot, stack := setContainerSlotPacket(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)); slot != 0 || stack.ID != potion {
		t.Fatalf("瓶子槽应收到水瓶，得到 slot=%d stack=%+v", slot, stack)
	}
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)

	// 快捷栏第 1 格（菜单槽 33）取烈焰粉到光标。
	sendContainerClick(t, conn, windowID, 0, brewingSlots+28, 0, protocol.ClickModePickup)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)
	if stack := cursorItemPacket(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)); stack.ID != blaze {
		t.Fatalf("光标应为烈焰粉，得到 %+v", stack)
	}
	// 放入燃料槽 4。
	sendContainerClick(t, conn, windowID, 0, 4, 0, protocol.ClickModePickup)
	if _, _, slot, stack := setContainerSlotPacket(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)); slot != 4 || stack.ID != blaze {
		t.Fatalf("燃料槽应收到烈焰粉，得到 slot=%d stack=%+v", slot, stack)
	}
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)

	// 服务端状态与方块实体应同步（点击后立即 flush）。
	waitFor(t, func() bool {
		entity, err := instance.testWorld().BlockEntityAt(x, y, z)
		if err != nil || len(entity.Items) < brewingSlots {
			return false
		}
		return entity.Items[0].ItemID == potion && entity.Items[4].ItemID == blaze
	}, "窗口点击内容应写回方块实体")
	state.mu.Lock()
	bottle := state.slots[0]
	fuel := state.slots[4]
	state.mu.Unlock()
	if bottle.ItemID != potion || fuel.ItemID != blaze {
		t.Fatalf("运行时槽位 = (瓶子 %+v, 燃料 %+v)", bottle, fuel)
	}
}

// TestBrewingHopperRules 验证漏斗对酿造台的面规则与插入/抽取过滤
// （对照原版 getSlotsForFace / canPlaceItem / canTakeItemThroughFace）。
func TestBrewingHopperRules(t *testing.T) {
	potion := mustItemID(t, "minecraft:potion")
	glass := mustItemID(t, "minecraft:glass_bottle")
	wart := mustItemID(t, "minecraft:nether_wart")
	blaze := mustItemID(t, "minecraft:blaze_powder")

	target := &transferTarget{kind: transferBrewing, items: make([]item.Stack, brewingSlots)}
	checkSlots := func(dir string, want []int) {
		t.Helper()
		got := target.slotsForFace(dir)
		if len(got) != len(want) {
			t.Fatalf("slotsForFace(%s) = %v, want %v", dir, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("slotsForFace(%s) = %v, want %v", dir, got, want)
			}
		}
	}
	checkSlots("up", []int{3})
	checkSlots("down", []int{0, 1, 2, 3})
	checkSlots("north", []int{0, 1, 2, 4})

	if !target.canPlace(3, item.Stack{ItemID: wart, Count: 1}) {
		t.Fatal("漏斗应能向材料槽插入下界疣")
	}
	if target.canPlace(3, item.Stack{ItemID: potion, Count: 1}) {
		t.Fatal("漏斗不应向材料槽插入药水")
	}
	if !target.canPlace(4, item.Stack{ItemID: blaze, Count: 1}) {
		t.Fatal("漏斗应能向燃料槽插入烈焰粉")
	}
	if target.canPlace(4, item.Stack{ItemID: wart, Count: 1}) {
		t.Fatal("漏斗不应向燃料槽插入下界疣")
	}
	if !target.canPlace(1, item.Stack{ItemID: potion, Count: 1}) {
		t.Fatal("漏斗应能向空瓶子槽插入药水")
	}
	target.items[1] = item.Stack{ItemID: potion, Count: 1}
	if target.canPlace(1, item.Stack{ItemID: glass, Count: 1}) {
		t.Fatal("漏斗不应向非空瓶子槽插入物品")
	}
	if target.canTake(3, item.Stack{ItemID: wart, Count: 1}) {
		t.Fatal("材料槽不应对漏斗抽出材料")
	}
	if !target.canTake(3, item.Stack{ItemID: glass, Count: 1}) {
		t.Fatal("材料槽应允许漏斗抽出玻璃瓶")
	}
	if !target.canTake(0, item.Stack{ItemID: potion, Count: 1}) {
		t.Fatal("瓶子槽应允许漏斗抽出药水")
	}
}

// TestHopperIntoBrewingStand 端到端验证漏斗与酿造台交互：
// 上方漏斗插入材料、侧面漏斗插入烈焰粉、下方漏斗抽出瓶子槽（药水 ID 保留，
// 材料槽的下界疣不可抽出）。
func TestHopperIntoBrewingStand(t *testing.T) {
	instance := newHopperTestServer(t)
	spawnX, _, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	x, z := int(math.Floor(spawnX))+8, int(math.Floor(spawnZ))+8
	groundY := hopperTestPad(t, instance, x, z, 5)

	// 上方漏斗（朝下）→ 酿造台材料槽（原版 SLOTS_FOR_UP = [3]）。
	placeBlockAt(t, instance, x, groundY-1, z, "minecraft:brewing_stand")
	above := hopperAt(t, instance, x, groundY, z, "down")
	wart, err := item.FromName("minecraft:nether_wart", 2)
	if err != nil {
		t.Fatal(err)
	}
	setHopperSlot(above, 0, wart)
	instance.tick()
	brewing := instance.registerBrewing(world.DimensionOverworld, x, groundY-1, z)
	if brewing == nil {
		t.Fatal("酿造台状态注册失败")
	}
	brewing.mu.Lock()
	material := brewing.slots[3]
	brewing.mu.Unlock()
	if material.ItemID != wart.ItemID || material.Count != 1 {
		t.Fatalf("酿造台材料槽 = %+v, want 1 个下界疣", material)
	}

	// 侧面漏斗（朝西）→ 酿造台燃料槽（原版 SLOTS_FOR_SIDES = [0,1,2,4]，
	// 烈焰粉只能进槽 4）。注意：同一 tick 内酿造台会把烈焰粉消耗为 20 次
	// 使用（fuel = 20、槽位清空），因此断言“已补充燃料或槽内仍有烈焰粉”。
	side := hopperAt(t, instance, x+1, groundY-1, z, "west")
	blaze, err := item.FromName("minecraft:blaze_powder", 1)
	if err != nil {
		t.Fatal(err)
	}
	setHopperSlot(side, 0, blaze)
	instance.tick()
	brewing.mu.Lock()
	fuel := brewing.slots[4]
	fuelCount := brewing.fuel
	brewing.mu.Unlock()
	if fuel.ItemID != blaze.ItemID && fuelCount != brewingFuelUses {
		t.Fatalf("侧面烈焰粉既未进入燃料槽也未补充燃料：槽位 = %+v, fuel = %d", fuel, fuelCount)
	}

	// 下方漏斗从酿造台抽取：先放夜视药水到瓶子槽，应被抽走且保留药水 ID。
	below := hopperAt(t, instance, x, groundY-2, z, "down")
	potion := mustItemID(t, "minecraft:potion")
	brewing.mu.Lock()
	brewing.slots[0] = item.Stack{ItemID: potion, Count: 1, PotionID: registry.PotionNightVision + 1}
	// 无燃料酿造（材料是下界疣、瓶子是夜视药水，无匹配配方），不会干扰抽取。
	brewing.mu.Unlock()
	for i := 0; i < hopperMoveItemSpeed+1; i++ {
		instance.tick()
	}
	got := hopperSlotAt(below, 0)
	if got.ItemID != potion || got.PotionID != registry.PotionNightVision+1 {
		t.Fatalf("下方漏斗应抽到夜视药水（含药水 ID），得到 %+v", got)
	}
	// 材料槽的下界疣不可被漏斗抽出（canTakeItemThroughFace 只允许玻璃瓶）。
	found := false
	for i := 0; i < hopperSlots; i++ {
		if hopperSlotAt(below, i).ItemID == wart.ItemID {
			found = true
		}
	}
	if found {
		t.Fatal("材料槽的下界疣不应被漏斗抽出")
	}
}
