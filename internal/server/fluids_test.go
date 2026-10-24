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

// 流体系统测试：扩散/下落/无限水/岩浆交互/桶/下界蒸发/火焰生命周期。
//
// 测试通过 instance.tick() 手动驱动计划刻（joinServer 设置了 tickInterval=0）。

// fluidBase 返回一块干净的封闭测试平台：出生点上方清除区域并铺石头地板，
// 四周筑墙（避免“找坡”搜索在平台外找到落差，使平地扩散测试可预期）。
func fluidBase(t *testing.T, instance *Server) (int, int, int) {
	t.Helper()
	baseX, baseY, baseZ := redstoneBase(instance)
	w := instance.testWorld()
	floorY := baseY + 4
	const west, east, north, south = -6, 16, -6, 6
	for dx := west; dx <= east; dx++ {
		for dz := north; dz <= south; dz++ {
			for dy := 0; dy <= 4; dy++ {
				w.SetBlock(baseX+dx, floorY+dy, baseZ+dz, world.AirBlock)
			}
			w.SetBlock(baseX+dx, floorY-1, baseZ+dz, world.StoneBlock)
			w.SetBlock(baseX+dx, floorY-2, baseZ+dz, world.StoneBlock)
		}
	}
	// 围墙（顶到 floorY+3，阻断找坡搜索越过平台边缘）。
	for dx := west; dx <= east; dx++ {
		for _, dz := range []int{north, south} {
			for dy := 0; dy <= 3; dy++ {
				w.SetBlock(baseX+dx, floorY+dy, baseZ+dz, world.StoneBlock)
			}
		}
	}
	for dz := north; dz <= south; dz++ {
		for _, dx := range []int{west, east} {
			for dy := 0; dy <= 3; dy++ {
				w.SetBlock(baseX+dx, floorY+dy, baseZ+dz, world.StoneBlock)
			}
		}
	}
	return baseX, floorY, baseZ
}

// placeFluidSourceForTest 在测试中放置一个流体源并调度其计划刻
// （等价于玩家放置：onPlace → shouldSpreadLiquid + scheduleTick）。
func placeFluidSourceForTest(t *testing.T, instance *Server, x, y, z int, name string) {
	t.Helper()
	state, ok := fluidSourceState(name)
	if !ok {
		t.Fatalf("fluid source state missing: %s", name)
	}
	if !instance.testWorld().SetBlock(x, y, z, state) {
		t.Fatalf("SetBlock fluid failed at (%d,%d,%d)", x, y, z)
	}
	dim := world.DimensionOverworld
	instance.updateFluidsAround(dim, x, y, z)
	instance.scheduleFluidTick(dim, [3]int{x, y, z}, fluidTickDelay(name, dim))
}

// fluidProps 返回位置处流体的方块名与 level 属性（非流体返回 false）。
func fluidProps(t *testing.T, instance *Server, x, y, z int) (string, string, bool) {
	t.Helper()
	name, props, ok := registry.StateProps(instance.testWorld().BlockAt(x, y, z))
	if !ok {
		return "", "", false
	}
	return name, props["level"], true
}

// runTicks 手动推进 n 个服务器 tick（含流体计划刻）。
func runTicks(instance *Server, n int) {
	for i := 0; i < n; i++ {
		instance.tick()
	}
}

// TestWaterSpreadsLevelsAndStops 验证水在平面上按等级扩散并在 7 格外停止。
func TestWaterSpreadsLevelsAndStops(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "FluidSpread")

	baseX, floorY, baseZ := fluidBase(t, instance)
	placeFluidSourceForTest(t, instance, baseX, floorY, baseZ, waterBlockName)
	runTicks(instance, 5*9+20)

	// 源保持 level 0。
	if _, level, ok := fluidProps(t, instance, baseX, floorY, baseZ); !ok || level != "0" {
		t.Fatalf("source level = %q ok=%v, want 0", level, ok)
	}
	// 距离 1..7 依次是 level 1..7。
	for distance := 1; distance <= 7; distance++ {
		name, level, ok := fluidProps(t, instance, baseX+distance, floorY, baseZ)
		if !ok || name != waterBlockName {
			t.Fatalf("distance %d: name=%q ok=%v, want water", distance, name, ok)
		}
		want := string(rune('0' + distance))
		if level != want {
			t.Fatalf("distance %d: level=%q, want %q", distance, level, want)
		}
	}
	// 距离 8 不应有水。
	if name, _, ok := fluidProps(t, instance, baseX+8, floorY, baseZ); ok && name == waterBlockName {
		t.Fatalf("distance 8 has water (level %v), want none", name)
	}
}

// TestWaterFallsDownwards 验证水源向下形成下落水柱并在地面展开。
func TestWaterFallsDownwards(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "FluidFall")

	baseX, floorY, baseZ := fluidBase(t, instance)
	// 高处水源（floorY+4 在清理区内）：floorY+3/+2/+1 应为下落（level 8），
	// floorY 面应向外展开 level 1。
	placeFluidSourceForTest(t, instance, baseX, floorY+4, baseZ, waterBlockName)
	runTicks(instance, 5*12+30)

	for dy := 1; dy <= 3; dy++ {
		name, level, ok := fluidProps(t, instance, baseX, floorY+dy, baseZ)
		if !ok || name != waterBlockName || level != "8" {
			t.Fatalf("falling block y+%d: name=%q level=%q ok=%v, want water level 8", dy, name, level, ok)
		}
	}
	// 地面侧向展开：相邻格 level 1。
	name, level, ok := fluidProps(t, instance, baseX+1, floorY, baseZ)
	if !ok || name != waterBlockName || level != "1" {
		t.Fatalf("side spread: name=%q level=%q ok=%v, want water level 1", name, level, ok)
	}
}

// TestInfiniteWaterSource 验证两个相邻水源 + 下方固体生成新的水源。
func TestInfiniteWaterSource(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "FluidInfinite")

	baseX, floorY, baseZ := fluidBase(t, instance)
	placeFluidSourceForTest(t, instance, baseX, floorY, baseZ, waterBlockName)
	placeFluidSourceForTest(t, instance, baseX+2, floorY, baseZ, waterBlockName)
	runTicks(instance, 5*12+30)

	name, level, ok := fluidProps(t, instance, baseX+1, floorY, baseZ)
	if !ok || name != waterBlockName || level != "0" {
		t.Fatalf("middle block: name=%q level=%q ok=%v, want source (level 0)", name, level, ok)
	}
}

// TestLavaWaterInteractions 验证岩浆与水接触的凝固规则：
// 岩浆源 + 水 → 黑曜石；流动岩浆 + 水 → 圆石。
func TestLavaWaterInteractions(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "FluidLava")

	baseX, floorY, baseZ := fluidBase(t, instance)
	// 岩浆源 + 相邻水源 → 黑曜石。
	placeFluidSourceForTest(t, instance, baseX, floorY, baseZ, lavaBlockName)
	placeFluidSourceForTest(t, instance, baseX+1, floorY, baseZ, waterBlockName)
	runTicks(instance, 5*3)
	if name, ok := instance.blockNameOf(instance.testWorld().BlockAt(baseX, floorY, baseZ)); !ok || name != "minecraft:obsidian" {
		t.Fatalf("lava source + water = %q, want obsidian", name)
	}

	// 流动岩浆（level 1 = amount 7）+ 水 → 圆石。
	flowing, ok := fluidFlowingState(lavaBlockName, 7)
	if !ok {
		t.Fatal("flowing lava state missing")
	}
	instance.testWorld().SetBlock(baseX+4, floorY, baseZ, flowing)
	placeFluidSourceForTest(t, instance, baseX+5, floorY, baseZ, waterBlockName)
	runTicks(instance, 5*3)
	if name, ok := instance.blockNameOf(instance.testWorld().BlockAt(baseX+4, floorY, baseZ)); !ok || name != "minecraft:cobblestone" {
		t.Fatalf("flowing lava + water = %q, want cobblestone", name)
	}
}

// TestWaterDestroysPlants 验证流动水冲毁可替换的小方块（草）。
func TestWaterDestroysPlants(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "FluidPlants")

	baseX, floorY, baseZ := fluidBase(t, instance)
	grass, ok := registry.BlockStateIDs["minecraft:short_grass"]
	if !ok {
		t.Fatal("short_grass missing")
	}
	instance.testWorld().SetBlock(baseX+1, floorY, baseZ, grass)
	placeFluidSourceForTest(t, instance, baseX, floorY, baseZ, waterBlockName)
	runTicks(instance, 5*3)
	if name, ok := instance.blockNameOf(instance.testWorld().BlockAt(baseX+1, floorY, baseZ)); !ok || name != waterBlockName {
		t.Fatalf("grass replacement = %q, want water", name)
	}
}

// TestWaterEvaporatesInNether 验证水在下界蒸发（放置后被移除）。
func TestWaterEvaporatesInNether(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "FluidNether")

	nether := instance.worldFor(world.DimensionNether)
	if nether == nil {
		t.Fatal("nether world unavailable")
	}
	// 在末地传送门出口平台高度附近的空气中放置水。
	x, y, z := 8, 70, 8
	for dy := -1; dy <= 2; dy++ {
		nether.SetBlock(x, y+dy, z, world.AirBlock)
	}
	nether.SetBlock(x, y-1, z, world.StoneBlock)
	source, _ := fluidSourceState(waterBlockName)
	nether.SetBlock(x, y, z, source)
	dim := world.DimensionNether
	instance.scheduleFluidTick(dim, [3]int{x, y, z}, fluidTickDelay(waterBlockName, dim))
	runTicks(instance, fluidTickDelay(waterBlockName, dim)+2)
	if state := nether.BlockAt(x, y, z); state != world.AirBlock {
		t.Fatalf("nether water state = %d, want air (evaporated)", state)
	}
}

// TestBucketPlaceAndPickup 验证玩家水桶放置水源与空桶收集源流体。
func TestBucketPlaceAndPickup(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "BucketUser")
	player := findSession(t, instance, "BucketUser")

	baseX, floorY, baseZ := fluidBase(t, instance)
	w := instance.testWorld()

	waterBucketID, err := registry.ItemID(waterBucketItemName)
	if err != nil {
		t.Fatalf("water bucket item missing: %v", err)
	}
	bucketID, err := registry.ItemID(bucketItemName)
	if err != nil {
		t.Fatalf("bucket item missing: %v", err)
	}
	// 放在快捷栏并选中。
	player.inventory.Set(0, item.Stack{ItemID: waterBucketID, Count: 1})
	player.setSelectedHotbarSlot(0)

	// 直接调用桶逻辑（等价于 Use Item On 包中点击下方方块顶面）。
	use := protocol.UseItemOn{X: baseX, Y: floorY - 1, Z: baseZ, Direction: 1}
	if !instance.tryUseBucket(player, w, use) {
		t.Fatal("tryUseBucket(place) = false, want true")
	}
	name, level, ok := fluidProps(t, instance, baseX, floorY, baseZ)
	if !ok || name != waterBlockName || level != "0" {
		t.Fatalf("placed fluid: name=%q level=%q ok=%v, want water source", name, level, ok)
	}
	if got := player.inventory.Get(0); got.ItemID != bucketID || got.Count != 1 {
		t.Fatalf("held item after placing = %+v, want empty bucket", got)
	}

	// 空桶收集水源。
	player.setSelectedHotbarSlot(0)
	use = protocol.UseItemOn{X: baseX, Y: floorY, Z: baseZ, Direction: 0}
	if !instance.tryUseBucket(player, w, use) {
		t.Fatal("tryUseBucket(pickup) = false, want true")
	}
	if state := w.BlockAt(baseX, floorY, baseZ); state != world.AirBlock {
		t.Fatalf("source after pickup = %d, want air", state)
	}
	if got := player.inventory.Get(0); got.ItemID != waterBucketID || got.Count != 1 {
		t.Fatalf("held item after pickup = %+v, want water bucket", got)
	}
}

// TestFirePlacedAgesAndBurnsOut 验证火焰放置后随 age 增长最终熄灭
// （周围无可燃方块时）。
func TestFirePlacedAgesAndBurnsOut(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "FireUser")

	baseX, floorY, baseZ := fluidBase(t, instance)
	w := instance.testWorld()
	dim := world.DimensionOverworld
	if !instance.placeFireAt(w, dim, baseX, floorY, baseZ) {
		t.Fatal("placeFireAt = false, want true")
	}
	if name, _, ok := registry.StateProps(w.BlockAt(baseX, floorY, baseZ)); !ok || name != fireBlockName {
		t.Fatalf("fire block = %q, want fire", name)
	}
	// age 0→15 需要 15 次火焰推进（每次延迟 30–39 tick），随后一次熄灭。
	runTicks(instance, (fireMaxAge+2)*(fireTickDelay(instance)+1))
	if state := w.BlockAt(baseX, floorY, baseZ); state != world.AirBlock {
		t.Fatalf("fire after aging = %d, want air (extinguished)", state)
	}
}

// TestBlockBreakUnderWaterReflows 验证破坏水下/水边方块后水流重新扩散。
func TestBlockBreakUnderWaterReflows(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "FluidBreak")
	player := findSession(t, instance, "FluidBreak")

	baseX, floorY, baseZ := fluidBase(t, instance)
	w := instance.testWorld()
	// 在平台一侧砌一堵墙并放水源，破坏墙后水应流出。
	wallX := baseX + 1
	w.SetBlock(wallX, floorY, baseZ, world.StoneBlock)
	w.SetBlock(wallX, floorY+1, baseZ, world.StoneBlock)
	placeFluidSourceForTest(t, instance, baseX, floorY, baseZ, waterBlockName)
	runTicks(instance, 5*3)

	// 读循环屏障（/list 回复）后才能安全地直接写会话位置字段。
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "FluidBreak")
	player.setPlayerPosition(float64(baseX)+0.5, float64(floorY), float64(baseZ)+1.5, 0, 0)
	if !player.withinBlockReach(wallX, floorY, baseZ) {
		t.Fatalf("wall out of reach: player at (%v,%v,%v)", float64(baseX)+0.5, float64(floorY), float64(baseZ)+1.5)
	}
	instance.handlePlayerAction(player, protocol.PlayerAction{
		Status: 0, X: wallX, Y: floorY, Z: baseZ,
	})
	// 推进挖掘进度（空手挖石头需要 105 tick，200 tick 留出余量）后提交 STOP。
	// 测试无移动包，显式置位着地状态（悬空挖掘速度 ÷5）。
	player.setPlayerOnGround(true)
	runTicks(instance, 200)
	instance.handlePlayerAction(player, protocol.PlayerAction{
		Status: 2, X: wallX, Y: floorY, Z: baseZ,
	})
	if state := w.BlockAt(wallX, floorY, baseZ); state != world.AirBlock {
		t.Fatalf("wall after break = %d, want air", state)
	}
	runTicks(instance, 5*4)
	if name, _, ok := fluidProps(t, instance, wallX, floorY, baseZ); !ok || name != waterBlockName {
		t.Fatalf("water did not reflow into broken wall position: name=%q ok=%v", name, ok)
	}
}

// TestItemHazardUsesAnyFluidLevel 验证掉落物在流动岩浆（非源）中也被销毁。
func TestItemHazardUsesAnyFluidLevel(t *testing.T) {
	s := &Server{}
	if !s.itemHazardState(mustFluidState(t, lavaBlockName, 3)) {
		t.Fatal("itemHazardState(flowing lava) = false, want true")
	}
	if s.itemHazardState(world.AirBlock) {
		t.Fatal("itemHazardState(air) = true, want false")
	}
}

// mustFluidState 返回指定流体按 amount 的状态（测试辅助）。
func mustFluidState(t *testing.T, name string, amount int) uint16 {
	t.Helper()
	state, ok := fluidFlowingState(name, amount)
	if !ok {
		t.Fatalf("fluid flowing state missing: %s amount=%d", name, amount)
	}
	return state
}
