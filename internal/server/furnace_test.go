//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"testing"

	"gmcs/internal/item"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"

	"gmcs/internal/world"
)

// placeFurnace 在玩家附近放置熔炉方块。
func placeFurnace(t *testing.T, instance *Server, blockName string) (int, int, int) {
	t.Helper()
	state, ok := registry.BlockStateIDs[blockName]
	if !ok {
		t.Fatalf("缺少方块状态 %s", blockName)
	}
	x, y, z := buildSpotNearSpawn(t, instance)
	if !instance.testWorld().SetBlock(x, y, z, state) {
		t.Fatalf("无法放置 %s", blockName)
	}
	return x, y, z
}

// TestFurnaceSmeltsIronOre 验证熔炉熔炼主流程：
// 原料 + 燃料 → 燃烧 → 烹饪 200 tick → 产物。
func TestFurnaceSmeltsIronOre(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Smelter")

	ironOre, err := registry.ItemID("minecraft:iron_ore")
	if err != nil {
		t.Fatal(err)
	}
	ironIngot, err := registry.ItemID("minecraft:iron_ingot")
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
	state.mu.Lock()
	state.slots[0] = itemStackFor(ironOre, 1)
	state.slots[1] = itemStackFor(coal, 1)
	state.mu.Unlock()

	// 逐步 tick：点燃 + 烹饪 200 tick。
	for i := 0; i < 210; i++ {
		if instance.tickFurnace(state) {
			continue
		}
	}
	state.mu.Lock()
	result := state.slots[0]
	output := state.slots[2]
	burn := state.burnRemaining
	state.mu.Unlock()
	if !result.IsEmpty() {
		t.Fatalf("原料应被消耗，剩余 %+v", result)
	}
	if output.ItemID != ironIngot || output.Count != 1 {
		t.Fatalf("产物应为 1 铁锭，得到 %+v", output)
	}
	if burn <= 0 {
		t.Fatalf("燃料应仍在燃烧，剩余 %d", burn)
	}

	// 状态写回方块实体并可恢复（模拟重启）。
	instance.flushFurnace(state)
	entity, err := instance.testWorld().BlockEntityAt(x, y, z)
	if err != nil {
		t.Fatal(err)
	}
	if entity.BurnRemaining != burn || entity.Items[2].ItemID != ironIngot {
		t.Fatalf("熔炉状态未持久化：%+v", entity)
	}
	_ = conn
}

// TestFurnaceFuelConsumption 验证燃料消耗与燃烧时长（煤炭 1600 tick）。
func TestFurnaceFuelConsumption(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Fueler")

	ironOre, _ := registry.ItemID("minecraft:iron_ore")
	coal, _ := registry.ItemID("minecraft:coal")

	x, y, z := placeFurnace(t, instance, "minecraft:furnace")
	state := instance.registerFurnace(world.DimensionOverworld, x, y, z)
	state.mu.Lock()
	state.slots[0] = itemStackFor(ironOre, 1)
	state.slots[1] = itemStackFor(coal, 2)
	state.mu.Unlock()

	// 第一 tick 点燃。
	instance.tickFurnace(state)
	state.mu.Lock()
	burnTotal := state.burnTotal
	fuelCount := state.slots[1].Count
	state.mu.Unlock()
	if burnTotal != 1600 {
		t.Fatalf("煤炭燃烧时长 = %d, want 1600", burnTotal)
	}
	if fuelCount != 1 {
		t.Fatalf("燃料应消耗 1 个，剩余 %d", fuelCount)
	}
}

// TestBlastFurnaceHalvesBurnTime 验证高炉燃料燃烧速度是熔炉的 2 倍。
func TestBlastFurnaceHalvesBurnTime(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Blaster")

	ironOre, _ := registry.ItemID("minecraft:iron_ore")
	coal, _ := registry.ItemID("minecraft:coal")

	x, y, z := placeFurnace(t, instance, "minecraft:blast_furnace")
	state := instance.registerFurnace(world.DimensionOverworld, x, y, z)
	state.mu.Lock()
	state.slots[0] = itemStackFor(ironOre, 1)
	state.slots[1] = itemStackFor(coal, 1)
	state.mu.Unlock()

	instance.tickFurnace(state)
	state.mu.Lock()
	burnTotal := state.burnTotal
	cookTotal := state.cookTotal
	state.mu.Unlock()
	if burnTotal != 800 {
		t.Fatalf("高炉煤炭燃烧时长 = %d, want 800", burnTotal)
	}
	// 高炉配方烹饪时间 100 tick。
	if cookTotal != 100 {
		t.Fatalf("高炉烹饪时长 = %d, want 100", cookTotal)
	}
}

// TestSmokerRejectsOre 验证烟熏炉不接受矿物。
func TestSmokerRejectsOre(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Cooker")

	ironOre, _ := registry.ItemID("minecraft:iron_ore")
	coal, _ := registry.ItemID("minecraft:coal")

	x, y, z := placeFurnace(t, instance, "minecraft:smoker")
	state := instance.registerFurnace(world.DimensionOverworld, x, y, z)
	state.mu.Lock()
	state.slots[0] = itemStackFor(ironOre, 1)
	state.slots[1] = itemStackFor(coal, 1)
	state.mu.Unlock()

	for i := 0; i < 50; i++ {
		instance.tickFurnace(state)
	}
	state.mu.Lock()
	burn := state.burnRemaining
	progress := state.cookProgress
	input := state.slots[0]
	state.mu.Unlock()
	// 无可烹饪配方时不点燃燃料、不推进进度。
	if burn != 0 || progress != 0 {
		t.Fatalf("烟熏炉不应熔炼矿物：burn=%d progress=%d", burn, progress)
	}
	if input.Count != 1 {
		t.Fatalf("原料不应被消耗：%+v", input)
	}
}

// TestFurnaceOpenWindow 验证打开熔炉窗口（菜单类型与 39 槽）。
func TestFurnaceOpenWindow(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.GameMode = "creative"
	instance, conn := joinServer(t, cfg, "Watcher")

	x, y, z := placeFurnace(t, instance, "minecraft:furnace")
	sendUseItemOn(t, conn, x, y, z, 1)
	windowID, menuType := openScreen(t, conn)
	wantMenu, ok := registry.StaticEntryID("minecraft:menu", "minecraft:furnace")
	if !ok {
		t.Fatal("缺少 furnace 菜单类型")
	}
	if menuType != wantMenu {
		t.Fatalf("菜单类型 = %d, want %d", menuType, wantMenu)
	}
	content := expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)
	gotWindow, slots := containerContent(t, content)
	if gotWindow != windowID {
		t.Fatalf("窗口编号 = %d, want %d", gotWindow, windowID)
	}
	if len(slots) != 39 {
		t.Fatalf("窗口槽位数 = %d, want 39", len(slots))
	}
}

// itemStackFor 测试辅助：按物品 ID 构造堆栈。
func itemStackFor(itemID, count int32) item.Stack {
	return item.Stack{ItemID: itemID, Count: count}
}

// TestFurnaceFuelSlotRestriction 验证燃料槽的物品限制
// （原版 AbstractFurnaceBlockEntity#canPlaceItem）：只接受燃料；
// 空桶仅在没有空桶时作为湿海绵烘干的特例被接受。
func TestFurnaceFuelSlotRestriction(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "FuelSlot")

	coal, err := registry.ItemID("minecraft:coal")
	if err != nil {
		t.Fatal(err)
	}
	ironOre, err := registry.ItemID("minecraft:iron_ore")
	if err != nil {
		t.Fatal(err)
	}
	bucket, err := registry.ItemID("minecraft:bucket")
	if err != nil {
		t.Fatal(err)
	}
	waterBucket, err := registry.ItemID("minecraft:water_bucket")
	if err != nil {
		t.Fatal(err)
	}

	x, y, z := placeFurnace(t, instance, "minecraft:furnace")
	state := instance.registerFurnace(world.DimensionOverworld, x, y, z)
	if state == nil {
		t.Fatal("熔炉状态注册失败")
	}
	backing := furnaceSlotBacking{state: state}

	if !backing.CanPlace(1, itemStackFor(coal, 1)) {
		t.Fatal("煤应可放入燃料槽")
	}
	if backing.CanPlace(1, itemStackFor(ironOre, 1)) {
		t.Fatal("铁矿不是燃料，不应放入燃料槽")
	}
	if !backing.CanPlace(0, itemStackFor(ironOre, 1)) {
		t.Fatal("输入槽应接受任意物品（由配方决定）")
	}
	// 空桶：燃料槽为空时可放入（湿海绵烘干特例）。
	if !backing.CanPlace(1, itemStackFor(bucket, 1)) {
		t.Fatal("燃料槽为空时应接受空桶")
	}
	state.mu.Lock()
	state.slots[1] = itemStackFor(bucket, 1)
	state.mu.Unlock()
	if backing.CanPlace(1, itemStackFor(bucket, 1)) {
		t.Fatal("燃料槽已有空桶时不应再接受空桶")
	}
	if backing.CanPlace(1, itemStackFor(waterBucket, 1)) {
		t.Fatal("水桶不是燃料，不应放入燃料槽")
	}
	_ = waterBucket
}

// TestFurnaceWetSpongeConversion 验证湿海绵烘干特例：产物为海绵，
// 燃料槽中的空桶变成水桶（原版 AbstractFurnaceBlockEntity#burn）。
func TestFurnaceWetSpongeConversion(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Sponge")

	sponge, err := registry.ItemID("minecraft:sponge")
	if err != nil {
		t.Fatal(err)
	}
	wetSponge, err := registry.ItemID("minecraft:wet_sponge")
	if err != nil {
		t.Fatal(err)
	}
	bucket, err := registry.ItemID("minecraft:bucket")
	if err != nil {
		t.Fatal(err)
	}
	waterBucket, err := registry.ItemID("minecraft:water_bucket")
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
	state.mu.Lock()
	state.slots[0] = itemStackFor(wetSponge, 1)
	state.slots[1] = itemStackFor(coal, 1)
	state.mu.Unlock()
	for i := 0; i < 210; i++ {
		instance.tickFurnace(state)
	}
	state.mu.Lock()
	output := state.slots[2]
	fuel := state.slots[1]
	state.mu.Unlock()
	if output.ItemID != sponge || output.Count != 1 {
		t.Fatalf("产物应为 1 海绵，得到 %+v", output)
	}
	// 燃料槽最初是煤：燃烧后为空，不会变成水桶（没有空桶）。
	if !fuel.IsEmpty() && fuel.ItemID == waterBucket {
		t.Fatalf("无空桶时不应生成水桶，燃料槽为 %+v", fuel)
	}

	// 第二次：燃料槽放空桶（同时仍需要燃料 -> 使用岩浆桶保证有燃料）。
	lavaBucket, err := registry.ItemID("minecraft:lava_bucket")
	if err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	state.slots[0] = itemStackFor(wetSponge, 1)
	state.slots[1] = itemStackFor(bucket, 1)
	state.slots[2] = item.Empty()
	state.burnRemaining = 0
	state.cookProgress = 0
	state.mu.Unlock()
	// 空桶不是燃料：熔炉不会被点燃，产物不会出现。
	for i := 0; i < 10; i++ {
		instance.tickFurnace(state)
	}
	state.mu.Lock()
	stillEmpty := state.slots[2].IsEmpty()
	state.mu.Unlock()
	if !stillEmpty {
		t.Fatal("空桶不是燃料，不应产出海绵")
	}

	// 岩浆桶作为燃料 + 空桶在燃料槽：岩浆桶会被消耗成空桶再变水桶？
	// 原版顺序：燃料消耗后若留下空桶，空桶再被湿海绵特判替换为水桶。
	state.mu.Lock()
	state.slots[0] = itemStackFor(wetSponge, 1)
	state.slots[1] = itemStackFor(lavaBucket, 1)
	state.burnRemaining = 0
	state.cookProgress = 0
	state.mu.Unlock()
	for i := 0; i < 210; i++ {
		instance.tickFurnace(state)
	}
	state.mu.Lock()
	output = state.slots[2]
	fuel = state.slots[1]
	state.mu.Unlock()
	if output.ItemID != sponge {
		t.Fatalf("产物应为海绵，得到 %+v", output)
	}
	if fuel.ItemID != waterBucket || fuel.Count != 1 {
		t.Fatalf("岩浆桶燃烧后的空桶应被替换为水桶，得到 %+v", fuel)
	}
}
