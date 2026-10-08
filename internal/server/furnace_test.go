package server

import (
	"testing"

	"gmcs/internal/item"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
)

// placeFurnace 在玩家附近放置熔炉方块。
func placeFurnace(t *testing.T, instance *Server, blockName string) (int, int, int) {
	t.Helper()
	state, ok := registry.BlockStateIDs[blockName]
	if !ok {
		t.Fatalf("缺少方块状态 %s", blockName)
	}
	x, y, z := buildSpotNearSpawn(t, instance)
	if !instance.world.SetBlock(x, y, z, state) {
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
	state := instance.registerFurnace(x, y, z)
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
	entity, err := instance.world.BlockEntityAt(x, y, z)
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
	state := instance.registerFurnace(x, y, z)
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
	state := instance.registerFurnace(x, y, z)
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
	state := instance.registerFurnace(x, y, z)
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
