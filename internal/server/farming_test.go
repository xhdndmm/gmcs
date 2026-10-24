//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 农业系统测试：
//   - 锄头耕地（草方块/泥土/土径 → 耕地，砂土/缠根泥土 → 泥土 + 根须掉落）；
//   - 作物种植（耕地/灵魂沙、光照要求、物品消耗）；
//   - 作物随机刻生长、下界疣 1/10 规则；
//   - 耕地湿度（近水回升、无水衰减、无作物时退化为泥土）；
//   - 骨粉催熟（年龄区间、消耗、满龄不消耗、世界事件 1505）。

// farmTestSpot 在出生点附近找一列陆地，清空上方并用泥土铺面，
// 返回地面方块坐标（作物将种在 groundY+1）。
func farmTestSpot(t *testing.T, instance *Server) (x, groundY, z int) {
	t.Helper()
	bx, _, bz := instance.spawnPositionFor(world.DimensionOverworld)
	baseX, baseZ := int(math.Floor(bx)), int(math.Floor(bz))
	w := instance.testWorld()
	dirt, ok := registry.BlockStateIDs["minecraft:dirt"]
	if !ok {
		t.Fatal("缺少泥土方块状态")
	}
	for _, off := range spawnNearOffsets {
		x, z := baseX+off[0], baseZ+off[1]
		groundYf, ok := w.GroundY(x, z)
		if !ok {
			continue
		}
		gy := int(math.Floor(groundYf)) - 1
		if gy <= world.SeaLevel {
			continue
		}
		// 清空上方 20 格（覆盖树冠），保证“露天”光照近似成立。
		for dy := 1; dy <= 20; dy++ {
			w.SetBlock(x, gy+dy, z, world.AirBlock)
		}
		w.SetBlock(x, gy, z, dirt)
		return x, gy, z
	}
	t.Fatal("出生点附近找不到可耕作的陆地")
	return 0, 0, 0
}

// farmlandAt 返回指定位置的耕地含水量（非耕地返回 -1）。
func farmlandAt(instance *Server, x, y, z int) int {
	moisture, ok := registry.FarmlandMoisture(instance.testWorld().BlockAt(x, y, z))
	if !ok {
		return -1
	}
	return moisture
}

// setFarmland 把位置设置为指定含水量的耕地。
func setFarmland(t *testing.T, instance *Server, x, y, z, moisture int) uint16 {
	t.Helper()
	state, ok := registry.FarmlandStateWithMoisture(moisture)
	if !ok {
		t.Fatalf("缺少耕地 moisture=%d 状态", moisture)
	}
	if !instance.testWorld().SetBlock(x, y, z, state) {
		t.Fatalf("SetBlock(farmland) 失败 (%d,%d,%d)", x, y, z)
	}
	return state
}

// TestHoeTillsSoil 验证锄头把泥土变为耕地并消耗 1 点耐久（e2e）。
func TestHoeTillsSoil(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Farmer")
	defer conn.Close()

	x, groundY, z := farmTestSpot(t, instance)
	player := activeSession(t, instance, "Farmer")
	player.inventory.Set(item.SlotHotbarStart, testStack(t, "minecraft:wooden_hoe", 1))

	sendUseItemOn(t, conn, x, groundY, z, 1)
	waitFor(t, func() bool { return farmlandAt(instance, x, groundY, z) == 0 }, "锄头应把泥土变为耕地")
	waitFor(t, func() bool {
		return player.inventory.Get(item.SlotHotbarStart).Damage == 1
	}, "耕地应消耗 1 点耐久")

	// 耕地应登记到随机刻表。
	instance.farmMu.Lock()
	_, registered := instance.farmlands[containerKey{
		dim: world.DimensionOverworld, x: x, y: groundY, z: z,
	}]
	instance.farmMu.Unlock()
	if !registered {
		t.Fatal("耕地未登记到随机刻表")
	}
}

// TestHoeTillingRequiresAirAbove 验证上方有方块时不能耕作。
func TestHoeTillingRequiresAirAbove(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "FarmerBlocked")
	defer conn.Close()

	x, groundY, z := farmTestSpot(t, instance)
	stone, ok := registry.BlockStateIDs["minecraft:stone"]
	if !ok {
		t.Fatal("缺少石头方块状态")
	}
	instance.testWorld().SetBlock(x, groundY+1, z, stone)

	player := activeSession(t, instance, "FarmerBlocked")
	player.inventory.Set(item.SlotHotbarStart, testStack(t, "minecraft:wooden_hoe", 1))

	sendUseItemOn(t, conn, x, groundY, z, 1)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")

	if moisture := farmlandAt(instance, x, groundY, z); moisture != -1 {
		t.Fatalf("上方有方块时不应耕作：moisture=%d", moisture)
	}
	if damage := player.inventory.Get(item.SlotHotbarStart).Damage; damage != 0 {
		t.Fatalf("失败的耕作不应消耗耐久：damage=%d", damage)
	}
}

// TestHoeTillsCoarseAndRootedDirt 验证砂土与缠根泥土的转换
// （砂土 → 泥土；缠根泥土 → 泥土并掉落垂滴根须）。
func TestHoeTillsCoarseAndRootedDirt(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "FarmerDirt")
	defer conn.Close()

	x, groundY, z := farmTestSpot(t, instance)
	player := activeSession(t, instance, "FarmerDirt")
	player.inventory.Set(item.SlotHotbarStart, testStack(t, "minecraft:wooden_hoe", 1))

	coarse, ok := registry.BlockStateIDs["minecraft:coarse_dirt"]
	if !ok {
		t.Fatal("缺少砂土方块状态")
	}
	instance.testWorld().SetBlock(x, groundY, z, coarse)
	sendUseItemOn(t, conn, x, groundY, z, 1)
	waitFor(t, func() bool {
		name, ok := registry.BlockNameByState(instance.testWorld().BlockAt(x, groundY, z))
		return ok && name == "minecraft:dirt"
	}, "砂土应被耕成泥土")

	rooted, ok := registry.BlockStateIDs["minecraft:rooted_dirt"]
	if !ok {
		t.Fatal("缺少缠根泥土方块状态")
	}
	instance.testWorld().SetBlock(x, groundY, z, rooted)
	sendUseItemOn(t, conn, x, groundY, z, 1)
	waitFor(t, func() bool {
		name, ok := registry.BlockNameByState(instance.testWorld().BlockAt(x, groundY, z))
		return ok && name == "minecraft:dirt"
	}, "缠根泥土应被耕成泥土")
	rootsID, err := registry.ItemID("minecraft:hanging_roots")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		instance.entityMu.Lock()
		defer instance.entityMu.Unlock()
		for _, entity := range instance.items {
			if entity.Stack.ItemID == rootsID {
				return true
			}
		}
		return false
	}, "缠根泥土耕作应掉落垂滴根须")
}

// TestPlantCropOnFarmland 验证在耕地上种植小麦（消耗种子并登记随机刻）。
func TestPlantCropOnFarmland(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Planter")
	defer conn.Close()

	x, groundY, z := farmTestSpot(t, instance)
	w := instance.testWorld()
	setFarmland(t, instance, x, groundY, z, 0)
	player := activeSession(t, instance, "Planter")
	player.inventory.Set(item.SlotHotbarStart, testStack(t, "minecraft:wheat_seeds", 5))

	wheat, ok := registry.BlockStateIDs["minecraft:wheat"]
	if !ok {
		t.Fatal("缺少小麦方块状态")
	}
	sendUseItemOn(t, conn, x, groundY, z, 1) // 点击耕地顶面 → 种在 groundY+1
	waitFor(t, func() bool { return w.BlockAt(x, groundY+1, z) == wheat }, "耕地顶面应长出 age=0 小麦")
	waitFor(t, func() bool {
		return player.inventory.Get(item.SlotHotbarStart).Count == 4
	}, "种植应消耗 1 个种子")

	instance.farmMu.Lock()
	_, registered := instance.crops[containerKey{
		dim: world.DimensionOverworld, x: x, y: groundY + 1, z: z,
	}]
	instance.farmMu.Unlock()
	if !registered {
		t.Fatal("新种作物未登记到随机刻表")
	}
}

// TestPlantCropRequiresFarmland 验证种子不能直接种在泥土上。
func TestPlantCropRequiresFarmland(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "PlanterDirt")
	defer conn.Close()

	x, groundY, z := farmTestSpot(t, instance)
	player := activeSession(t, instance, "PlanterDirt")
	player.inventory.Set(item.SlotHotbarStart, testStack(t, "minecraft:wheat_seeds", 5))

	sendUseItemOn(t, conn, x, groundY, z, 1)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")

	if state := instance.testWorld().BlockAt(x, groundY+1, z); state != world.AirBlock {
		t.Fatalf("泥土上不应种植作物：state=%d", state)
	}
	if count := player.inventory.Get(item.SlotHotbarStart).Count; count != 5 {
		t.Fatalf("失败的种植不应消耗种子：count=%d", count)
	}
}

// TestPlantCropRequiresSkyLight 验证上方有方块（光照近似为 0）时不能种植。
func TestPlantCropRequiresSkyLight(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "PlanterDark")
	defer conn.Close()

	x, groundY, z := farmTestSpot(t, instance)
	w := instance.testWorld()
	setFarmland(t, instance, x, groundY, z, 0)
	stone, ok := registry.BlockStateIDs["minecraft:stone"]
	if !ok {
		t.Fatal("缺少石头方块状态")
	}
	// 作物位置的整列上方被方块遮挡 → 天光近似为 0。
	w.SetBlock(x, groundY+2, z, stone)

	player := activeSession(t, instance, "PlanterDark")
	player.inventory.Set(item.SlotHotbarStart, testStack(t, "minecraft:wheat_seeds", 5))

	sendUseItemOn(t, conn, x, groundY, z, 1)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")

	if state := w.BlockAt(x, groundY+1, z); state != world.AirBlock {
		t.Fatalf("无天光时不应种植作物：state=%d", state)
	}
	if count := player.inventory.Get(item.SlotHotbarStart).Count; count != 5 {
		t.Fatalf("失败的种植不应消耗种子：count=%d", count)
	}
}

// TestBoneMealGrowsCrop 验证骨粉催熟：年龄 +2..5、消耗 1 个、广播 1505；
// 满龄作物不接受骨粉（不消耗）。
func TestBoneMealGrowsCrop(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Bonemeal")
	defer conn.Close()

	x, groundY, z := farmTestSpot(t, instance)
	w := instance.testWorld()
	setFarmland(t, instance, x, groundY, z, 7)
	wheat, ok := registry.BlockStateIDs["minecraft:wheat"]
	if !ok {
		t.Fatal("缺少小麦方块状态")
	}
	w.SetBlock(x, groundY+1, z, wheat)

	player := activeSession(t, instance, "Bonemeal")
	player.inventory.Set(item.SlotHotbarStart, testStack(t, "minecraft:bone_meal", 5))

	sendUseItemOn(t, conn, x, groundY+1, z, 1)
	waitFor(t, func() bool {
		age, ok := registry.BlockStateAge(w.BlockAt(x, groundY+1, z))
		return ok && age >= 2 && age <= 5
	}, "骨粉应把小麦催熟 2–5 级")
	waitFor(t, func() bool {
		return player.inventory.Get(item.SlotHotbarStart).Count == 4
	}, "骨粉应消耗 1 个")

	// 满龄作物：isValidBonemealTarget = false，不消耗骨粉。
	maxAgeState, ok := registry.BlockStateWithAge(wheat, 7)
	if !ok {
		t.Fatal("缺少 age=7 小麦状态")
	}
	w.SetBlock(x, groundY+1, z, maxAgeState)
	sendUseItemOn(t, conn, x, groundY+1, z, 1)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")
	if count := player.inventory.Get(item.SlotHotbarStart).Count; count != 4 {
		t.Fatalf("满龄作物不应消耗骨粉：count=%d", count)
	}
	if got := w.BlockAt(x, groundY+1, z); got != maxAgeState {
		t.Fatalf("满龄作物状态不应改变：state=%d want %d", got, maxAgeState)
	}
}

// TestNetherWartPlantAndGrowth 验证下界疣只能种在灵魂沙上，且按 1/10 规则生长。
func TestNetherWartPlantAndGrowth(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Wart")
	defer conn.Close()

	x, groundY, z := farmTestSpot(t, instance)
	w := instance.testWorld()
	soulSand, ok := registry.BlockStateIDs["minecraft:soul_sand"]
	if !ok {
		t.Fatal("缺少灵魂沙方块状态")
	}
	w.SetBlock(x, groundY, z, soulSand)
	player := activeSession(t, instance, "Wart")
	player.inventory.Set(item.SlotHotbarStart, testStack(t, "minecraft:nether_wart", 3))

	wart, ok := registry.BlockStateIDs["minecraft:nether_wart"]
	if !ok {
		t.Fatal("缺少下界疣方块状态")
	}
	sendUseItemOn(t, conn, x, groundY, z, 1)
	waitFor(t, func() bool { return w.BlockAt(x, groundY+1, z) == wart }, "灵魂沙顶面应长出下界疣")

	for i := 0; i < 500000; i++ {
		instance.tickCrops()
		if age, ok := registry.BlockStateAge(w.BlockAt(x, groundY+1, z)); ok && age > 0 {
			return
		}
	}
	t.Fatal("下界疣在 500000 tick 内未生长")
}

// TestCropRandomTickGrowth 验证露天耕地上小麦会在随机刻中生长。
func TestCropRandomTickGrowth(t *testing.T) {
	instance := newHopperTestServer(t)
	x, z := 40, 40
	groundY := hopperTestPad(t, instance, x, z, 20)
	w := instance.testWorld()
	setFarmland(t, instance, x, groundY, z, 7)
	wheat, ok := registry.BlockStateIDs["minecraft:wheat"]
	if !ok {
		t.Fatal("缺少小麦方块状态")
	}
	w.SetBlock(x, groundY+1, z, wheat)
	if light := cropLightLevel(w, x, groundY+1, z); light < cropPlaceLight {
		t.Fatalf("测试场地不满足露天条件：light=%d", light)
	}
	instance.registerCrop(world.DimensionOverworld, x, groundY+1, z)

	// 单株作物、湿润耕地：每 tick 选中概率 3/4096，生长概率约 1/7，
	// 期望约 9500 tick 生长一次；50 万 tick 内必然生长。
	for i := 0; i < 500000; i++ {
		instance.tickCrops()
		if age, ok := registry.BlockStateAge(w.BlockAt(x, groundY+1, z)); ok && age > 0 {
			return
		}
	}
	t.Fatal("露天湿润耕地上的小麦在 500000 tick 内未生长")
}

// TestCropGrowthSpeed 验证生长速度公式（3×3 耕地含水量与行列/对角减半）。
func TestCropGrowthSpeed(t *testing.T) {
	instance := newHopperTestServer(t)
	x, z := 40, 40
	groundY := hopperTestPad(t, instance, x, z, 4)
	w := instance.testWorld()
	wheat, ok := registry.BlockStateIDs["minecraft:wheat"]
	if !ok {
		t.Fatal("缺少小麦方块状态")
	}
	// 仅中心耕地、干燥：1 + 1 = 2。
	setFarmland(t, instance, x, groundY, z, 0)
	w.SetBlock(x, groundY+1, z, wheat)
	if got := instance.cropGrowthSpeed(w, x, groundY+1, z, wheat); got != 2 {
		t.Fatalf("干燥孤立作物 speed = %v, want 2", got)
	}
	// 湿润：1 + 3 = 4。
	setFarmland(t, instance, x, groundY, z, 7)
	if got := instance.cropGrowthSpeed(w, x, groundY+1, z, wheat); got != 4 {
		t.Fatalf("湿润孤立作物 speed = %v, want 4", got)
	}
	// 东西相邻同种作物：westEast 为真但 northSouth 为假，且无对角作物 → 不减半。
	w.SetBlock(x+1, groundY+1, z, wheat)
	w.SetBlock(x+1, groundY, z, 0) // 相邻列保持石质（非耕地）
	if got := instance.cropGrowthSpeed(w, x, groundY+1, z, wheat); got != 4 {
		t.Fatalf("单行作物 speed = %v, want 4", got)
	}
	// 再加南北相邻 → 行列齐全，减半为 2。
	w.SetBlock(x, groundY+1, z+1, wheat)
	if got := instance.cropGrowthSpeed(w, x, groundY+1, z, wheat); got != 2 {
		t.Fatalf("行列作物 speed = %v, want 2", got)
	}
}

// TestFarmlandMoistureNearWater 验证近水耕地随机刻回升到 7，且在无水
// 无作物时逐级衰减并最终退化为泥土。
func TestFarmlandMoistureNearWater(t *testing.T) {
	instance := newHopperTestServer(t)
	x, z := 40, 40
	groundY := hopperTestPad(t, instance, x, z, 4)
	w := instance.testWorld()
	// 铺一块足够大的石头地面并清空上方，确保 4 格半径内没有自然水体。
	stone, ok := registry.BlockStateIDs["minecraft:stone"]
	if !ok {
		t.Fatal("缺少石头方块状态")
	}
	for dx := -6; dx <= 6; dx++ {
		for dz := -6; dz <= 6; dz++ {
			w.SetBlock(x+dx, groundY, z+dz, stone)
			w.SetBlock(x+dx, groundY+1, z+dz, world.AirBlock)
		}
	}
	setFarmland(t, instance, x, groundY, z, 0)
	instance.registerFarmland(world.DimensionOverworld, x, groundY, z)
	water, ok := registry.BlockStateIDs["minecraft:water"]
	if !ok {
		t.Fatal("缺少水方块状态")
	}
	w.SetBlock(x+1, groundY, z, water)

	waitMoisture := func(want int, limit int, message string) {
		t.Helper()
		for i := 0; i < limit; i++ {
			instance.tickCrops()
			if farmlandAt(instance, x, groundY, z) == want {
				return
			}
		}
		t.Fatalf("%s（当前 moisture=%d）", message, farmlandAt(instance, x, groundY, z))
	}
	// 近水：随机刻直接把湿度设为 7（期望约 1365 tick）。
	waitMoisture(7, 200000, "近水耕地未升至湿度 7")

	// 移除水：湿度逐级衰减，最终退化为泥土。
	w.SetBlock(x+1, groundY, z, world.AirBlock)
	limit := 400000
	for i := 0; i < limit; i++ {
		instance.tickCrops()
		if name, ok := registry.BlockNameByState(w.BlockAt(x, groundY, z)); ok && name == "minecraft:dirt" {
			return
		}
	}
	t.Fatalf("无水无作物的耕地未退化为泥土（moisture=%d）", farmlandAt(instance, x, groundY, z))
}

// TestFarmlandWithCropStaysFarmland 验证上方有作物时干燥耕地不会退化为泥土。
func TestFarmlandWithCropStaysFarmland(t *testing.T) {
	instance := newHopperTestServer(t)
	x, z := 40, 40
	groundY := hopperTestPad(t, instance, x, z, 20)
	w := instance.testWorld()
	setFarmland(t, instance, x, groundY, z, 0)
	instance.registerFarmland(world.DimensionOverworld, x, groundY, z)
	wheat, ok := registry.BlockStateIDs["minecraft:wheat"]
	if !ok {
		t.Fatal("缺少小麦方块状态")
	}
	w.SetBlock(x, groundY+1, z, wheat)

	for i := 0; i < 100000; i++ {
		instance.tickCrops()
		if name, ok := registry.BlockNameByState(w.BlockAt(x, groundY, z)); ok && name != "minecraft:farmland" {
			t.Fatalf("上方有作物时耕地不应退化：%s", name)
		}
	}
}

// TestTrampleFarmland 验证玩家落到耕地上的踩踏退化（原版 FarmBlock.fallOn）。
func TestTrampleFarmland(t *testing.T) {
	instance := newHopperTestServer(t)
	x, z := 40, 40
	groundY := hopperTestPad(t, instance, x, z, 4)
	w := instance.testWorld()
	state := setFarmland(t, instance, x, groundY, z, 7)
	instance.registerFarmland(world.DimensionOverworld, x, groundY, z)
	top := float64(groundY) + 0.9375
	px, pz := float64(x)+0.5, float64(z)+0.5

	// 下落 0.4 格：不满足 > 0.5，不退化。
	instance.tryTrampleFarmland(world.DimensionOverworld, w, state, top, px, pz, 0.4)
	if name, _ := registry.BlockNameByState(w.BlockAt(x, groundY, z)); name != "minecraft:farmland" {
		t.Fatalf("小幅下落不应踩踏农田：%s", name)
	}
	// 下落 3 格：nextFloat() < 2.5 恒成立，必然退化。
	instance.tryTrampleFarmland(world.DimensionOverworld, w, state, top, px, pz, 3)
	if name, _ := registry.BlockNameByState(w.BlockAt(x, groundY, z)); name != "minecraft:dirt" {
		t.Fatalf("踩踏后应变为泥土：%s", name)
	}
	instance.farmMu.Lock()
	_, registered := instance.farmlands[containerKey{dim: world.DimensionOverworld, x: x, y: groundY, z: z}]
	instance.farmMu.Unlock()
	if registered {
		t.Fatal("踩踏后退化的耕地应注销随机刻登记")
	}
}
