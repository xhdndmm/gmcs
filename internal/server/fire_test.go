//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"strconv"
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// fireTestSetup 建好石质平台并返回服务器实例、世界、维度和平台基准坐标。
func fireTestSetup(t *testing.T, name string) (*Server, *world.World, world.Dimension, int, int, int) {
	t.Helper()
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, name)
	w := instance.testWorld()
	baseX, floorY, baseZ := fluidBase(t, instance)
	return instance, w, world.DimensionOverworld, baseX, floorY, baseZ
}

// firePropsAt 返回位置上方块状态的属性（要求是普通火）。
func firePropsAt(t *testing.T, w *world.World, x, y, z int) map[string]string {
	t.Helper()
	name, props, ok := registry.StateProps(w.BlockAt(x, y, z))
	if !ok || name != fireBlockName {
		t.Fatalf("位置 (%d,%d,%d) 不是普通火（name=%q ok=%v）", x, y, z, name, ok)
	}
	return props
}

// TestFirePlacementRequiresAirAndSoulBase 验证放置规则（原版
// FlintAndSteelItem.useOn → BaseFireBlock.canBePlacedAt/getState）：
// 目标必须是空气；下方为灵魂沙时放置的是灵魂火。
func TestFirePlacementRequiresAirAndSoulBase(t *testing.T) {
	instance, w, dim, baseX, floorY, baseZ := fireTestSetup(t, "FirePlacer")

	planks, err := registry.BlockStateID("minecraft:oak_planks")
	if err != nil {
		t.Fatal(err)
	}
	// 目标非空气 → 失败（原版 canBePlacedAt：目标不是空气直接失败）。
	w.SetBlock(baseX+2, floorY, baseZ, planks)
	if instance.placeFireAt(w, dim, baseX+2, floorY, baseZ) {
		t.Fatal("在木板上放置火 = true, want false")
	}
	if state := w.BlockAt(baseX+2, floorY, baseZ); state != planks {
		t.Fatalf("木板被替换 = %d", state)
	}

	// 下方是灵魂沙 → 灵魂火（没有 age 属性）。
	soulSand, err := registry.BlockStateID("minecraft:soul_sand")
	if err != nil {
		t.Fatal(err)
	}
	w.SetBlock(baseX+3, floorY-1, baseZ, soulSand)
	if !instance.placeFireAt(w, dim, baseX+3, floorY, baseZ) {
		t.Fatal("在灵魂沙上放置火 = false, want true")
	}
	if name, props, ok := registry.StateProps(w.BlockAt(baseX+3, floorY, baseZ)); !ok || name != soulFireBlockName {
		t.Fatalf("灵魂沙上的火 = %q（%v）, want soul_fire", name, ok)
	} else if _, hasAge := props["age"]; hasAge {
		t.Fatalf("灵魂火不应有 age 属性：%v", props)
	}

	// 石质地面上的空气位置 → 普通火。
	if !instance.placeFireAt(w, dim, baseX+4, floorY, baseZ) {
		t.Fatal("在石头地面上放置火 = false, want true")
	}
	if name, _, ok := registry.StateProps(w.BlockAt(baseX+4, floorY, baseZ)); !ok || name != fireBlockName {
		t.Fatalf("石头上的火 = %q（%v）, want fire", name, ok)
	}
}

// TestSoulFireHasNoTick 验证灵魂火没有 tick（原版 SoulFireBlock 未覆写 tick）：
// 反复推进也不会改变方块状态。
func TestSoulFireHasNoTick(t *testing.T) {
	instance, w, dim, baseX, floorY, baseZ := fireTestSetup(t, "SoulTicker")

	soulSoil, err := registry.BlockStateID("minecraft:soul_soil")
	if err != nil {
		t.Fatal(err)
	}
	w.SetBlock(baseX+1, floorY-1, baseZ, soulSoil)
	if !instance.placeFireAt(w, dim, baseX+1, floorY, baseZ) {
		t.Fatal("在灵魂土上放置火 = false, want true")
	}
	before := w.BlockAt(baseX+1, floorY, baseZ)
	for i := 0; i < 50; i++ {
		instance.tickFire(w, dim, [3]int{baseX + 1, floorY, baseZ}, before)
	}
	if after := w.BlockAt(baseX+1, floorY, baseZ); after != before {
		t.Fatalf("灵魂火被推进改变：%d → %d", before, after)
	}
	// 失去灵魂沙/灵魂土支撑后不再能存活（原版 SoulFireBlock.canSurvive）。
	w.SetBlock(baseX+1, floorY-1, baseZ, world.StoneBlock)
	if instance.fireCanSurvive(w, soulFireBlockName, [3]int{baseX + 1, floorY, baseZ}) {
		t.Error("失去灵魂土后 fireCanSurvive = true, want false")
	}
}

// TestFireExtinguishesWithoutFuelAfterAgeThree 验证无燃料、下方顶面完整时：
// age ≤ 3 保留，age 超过 3 后熄灭（原版 FireBlock.tick 的无燃料分支）。
func TestFireExtinguishesWithoutFuelAfterAgeThree(t *testing.T) {
	instance, w, dim, baseX, floorY, baseZ := fireTestSetup(t, "BurnoutTicker")

	pos := [3]int{baseX, floorY, baseZ}
	if !instance.placeFireAt(w, dim, pos[0], pos[1], pos[2]) {
		t.Fatal("placeFireAt = false, want true")
	}
	removed := false
	for i := 0; i < 20 && !removed; i++ {
		props := firePropsAt(t, w, pos[0], pos[1], pos[2])
		age, err := strconv.Atoi(props["age"])
		if err != nil {
			t.Fatalf("age 属性无效: %v", props)
		}
		instance.fireTickBody(w, dim, pos, props)
		if w.BlockAt(pos[0], pos[1], pos[2]) == world.AirBlock {
			removed = true
			if age <= 3 {
				t.Fatalf("火焰在 age=%d（≤3）时被移除", age)
			}
			break
		}
		if age > 3 {
			t.Fatalf("火焰在 age=%d 时仍保留（无燃料应熄灭）", age)
		}
	}
	if !removed {
		t.Fatal("无燃料火焰在 20 次推进内未熄灭")
	}
}

// TestFireNearFuelAndIgniteOdds 验证可燃判定（原版 isNearFuel / getIgniteOdds /
// getBurnOdds）：以机械提取的可燃数值表为准。
func TestFireNearFuelAndIgniteOdds(t *testing.T) {
	instance, w, _, baseX, floorY, baseZ := fireTestSetup(t, "FuelProbe")

	planks, err := registry.BlockStateID("minecraft:oak_planks")
	if err != nil {
		t.Fatal(err)
	}
	leaves, err := registry.BlockStateID("minecraft:oak_leaves")
	if err != nil {
		t.Fatal(err)
	}
	firePos := [3]int{baseX, floorY, baseZ}
	if instance.fireNearFuel(w, firePos) {
		t.Fatal("只有石质地面时 fireNearFuel = true, want false")
	}
	if odds := instance.fireIgniteOdds(w, baseX, floorY, baseZ); odds != 0 {
		t.Fatalf("石头旁空气引燃值 = %d, want 0", odds)
	}

	w.SetBlock(baseX+1, floorY, baseZ, planks)
	if !instance.fireNearFuel(w, firePos) {
		t.Fatal("相邻木板时 fireNearFuel = false, want true")
	}
	if odds := instance.fireIgniteOdds(w, baseX, floorY, baseZ); odds != 5 {
		t.Fatalf("木板旁空气引燃值 = %d, want 5（木板鼓励值）", odds)
	}
	// 目标不是空气 → 引燃值 0（原版 isEmptyBlock 前置判定）。
	if odds := instance.fireIgniteOdds(w, baseX+1, floorY, baseZ); odds != 0 {
		t.Fatalf("木板自身引燃值 = %d, want 0", odds)
	}
	w.SetBlock(baseX+1, floorY, baseZ, leaves)
	if odds := instance.fireIgniteOdds(w, baseX, floorY, baseZ); odds != 30 {
		t.Fatalf("树叶旁空气引燃值 = %d, want 30（树叶鼓励值）", odds)
	}
	// 非可燃方块（石头）不算燃料。
	w.SetBlock(baseX+1, floorY, baseZ, world.StoneBlock)
	if instance.fireNearFuel(w, firePos) {
		t.Fatal("相邻石头时 fireNearFuel = true, want false")
	}
}

// TestFireBurnsAdjacentFuel 验证火焰会烧掉相邻可燃方块（原版 checkBurnOut）：
// 以橡木木板平台包围火焰，反复推进后木板上必然出现火或被烧毁（变成空气）。
func TestFireBurnsAdjacentFuel(t *testing.T) {
	instance, w, dim, baseX, floorY, baseZ := fireTestSetup(t, "Burner")

	planks, err := registry.BlockStateID("minecraft:oak_planks")
	if err != nil {
		t.Fatal(err)
	}
	// 3×3 木板平台，火焰位于中心（下方是木板 → age 15 后不会因“下方不可燃”熄灭）。
	watched := make([][3]int, 0, 9)
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			w.SetBlock(baseX+dx, floorY-1, baseZ+dz, planks)
			watched = append(watched, [3]int{baseX + dx, floorY - 1, baseZ + dz})
		}
	}
	firePos := [3]int{baseX, floorY, baseZ}
	if !instance.placeFireAt(w, dim, firePos[0], firePos[1], firePos[2]) {
		t.Fatal("placeFireAt = false, want true")
	}

	burned := false
	for i := 0; i < 2000 && !burned; i++ {
		state := w.BlockAt(firePos[0], firePos[1], firePos[2])
		name, props, ok := registry.StateProps(state)
		if !ok {
			break
		}
		switch name {
		case fireBlockName:
			instance.fireTickBody(w, dim, firePos, props)
		case soulFireBlockName:
			instance.tickFire(w, dim, firePos, state)
		default:
			// 火焰位置被烧掉后重新点燃，继续推进（模拟持续燃烧场景）。
			instance.tickFire(w, dim, firePos, state)
			state, ok := fireStateForAge(fireBlockName, 0)
			if !ok {
				t.Fatal("缺少火焰状态")
			}
			w.SetBlock(firePos[0], firePos[1], firePos[2], state)
		}
		for _, cell := range watched {
			got := w.BlockAt(cell[0], cell[1], cell[2])
			if got == planks {
				continue
			}
			if got == world.AirBlock {
				burned = true
				break
			}
			if name, _, ok := registry.StateProps(got); ok && name == fireBlockName {
				burned = true
				break
			}
			t.Fatalf("木板被替换成意外方块 %d", got)
		}
	}
	if !burned {
		t.Fatal("2000 次推进后相邻木板仍未被点燃/烧毁")
	}
}

// TestFireDoesNotSpreadWithoutFuel 验证周围全部不可燃时火焰不会点燃空气
// （原版 getIgniteOdds 为 0 → 蔓延循环跳过）。
func TestFireDoesNotSpreadWithoutFuel(t *testing.T) {
	instance, w, dim, baseX, floorY, baseZ := fireTestSetup(t, "DryFire")

	firePos := [3]int{baseX, floorY, baseZ}
	if !instance.placeFireAt(w, dim, firePos[0], firePos[1], firePos[2]) {
		t.Fatal("placeFireAt = false, want true")
	}
	inside := func(x, y, z int) bool {
		dx, dy, dz := x-firePos[0], y-firePos[1], z-firePos[2]
		return dx >= -1 && dx <= 1 && dz >= -1 && dz <= 1 && dy >= -1 && dy <= 4
	}
	for i := 0; i < 40; i++ {
		state := w.BlockAt(firePos[0], firePos[1], firePos[2])
		name, props, ok := registry.StateProps(state)
		if !ok || name != fireBlockName {
			break // 无燃料的火焰已熄灭。
		}
		instance.fireTickBody(w, dim, firePos, props)
		for dx := -1; dx <= 1; dx++ {
			for dz := -1; dz <= 1; dz++ {
				for dy := -1; dy <= 4; dy++ {
					x, y, z := firePos[0]+dx, firePos[1]+dy, firePos[2]+dz
					if x == firePos[0] && y == firePos[1] && z == firePos[2] {
						continue
					}
					if !inside(x, y, z) {
						continue
					}
					if name, _, ok := registry.StateProps(w.BlockAt(x, y, z)); ok && (name == fireBlockName || name == soulFireBlockName) {
						t.Fatalf("无燃料处出现火焰：(%d,%d,%d)", x, y, z)
					}
				}
			}
		}
	}
}

// TestFireSpreadAllowedPlayerRadius 验证玩家半径门控（原版
// ServerLevel.canSpreadFireAround + ChunkMap.anyPlayerCloseEnoughTo，默认 128 格，
// 旁观者不计入）。区块加载门控由 tickFire 单独处理（本测试只验证半径判定）。
func TestFireSpreadAllowedPlayerRadius(t *testing.T) {
	instance, _, _, baseX, floorY, baseZ := fireTestSetup(t, "RadiusProbe")
	player := findSession(t, instance, "RadiusProbe")

	x, y, z, _, _ := player.playerPosition()
	_ = baseX
	_ = floorY
	_ = baseZ
	near := [3]int{int(x) + 100, int(y), int(z)}
	far := [3]int{int(x) + 200, int(y), int(z)}
	if !instance.fireSpreadAllowed(world.DimensionOverworld, near) {
		t.Error("半径 100 格内 fireSpreadAllowed = false, want true")
	}
	if instance.fireSpreadAllowed(world.DimensionOverworld, far) {
		t.Error("半径 200 格外 fireSpreadAllowed = true, want false")
	}
	if instance.fireSpreadAllowed(world.DimensionNether, near) {
		t.Error("其它维度 fireSpreadAllowed = true, want false")
	}

	player.setGameMode(uint8(config.GameModeSpectator))
	if instance.fireSpreadAllowed(world.DimensionOverworld, near) {
		t.Error("旁观者不应计入玩家半径")
	}
}

// TestFireInfiniburnTagPerDimension 验证维度对应的永燃标签（原版
// dimensionType.infiniburn()）：下界岩在主世界与下界都永燃，石头不永燃。
func TestFireInfiniburnTagPerDimension(t *testing.T) {
	netherrack, err := registry.BlockStateID("minecraft:netherrack")
	if err != nil {
		t.Fatal(err)
	}
	if !registry.BlockTagContains(fireInfiniburnTag(world.DimensionOverworld), netherrack) {
		t.Error("主世界永燃标签应包含下界岩")
	}
	if !registry.BlockTagContains(fireInfiniburnTag(world.DimensionNether), netherrack) {
		t.Error("下界永燃标签应包含下界岩")
	}
	if registry.BlockTagContains(fireInfiniburnTag(world.DimensionEnd), world.StoneBlock) {
		t.Error("末地永燃标签不应包含石头")
	}
}

// TestFireIgnitesTnt 验证火焰燃烧判定会点燃 TNT（原版 FireBlock.checkBurnOut
// → TntBlock.primeTnt）：命中后 TNT 方块位置不再是 TNT，并在服务端生成一只
// 已点燃的 TNT 实体。
func TestFireIgnitesTnt(t *testing.T) {
	instance, w, dim, baseX, floorY, baseZ := fireTestSetup(t, "TntBurner")

	tnt, err := registry.BlockStateID("minecraft:tnt")
	if err != nil {
		t.Fatal(err)
	}
	pos := [3]int{baseX, floorY, baseZ}
	tntPos := [3]int{baseX + 1, floorY, baseZ}
	w.SetBlock(tntPos[0], tntPos[1], tntPos[2], tnt)
	if !instance.placeFireAt(w, dim, pos[0], pos[1], pos[2]) {
		t.Fatal("placeFireAt = false, want true")
	}

	ignited := false
	for i := 0; i < 500 && !ignited; i++ {
		state := w.BlockAt(pos[0], pos[1], pos[2])
		name, props, ok := registry.StateProps(state)
		if !ok || name != fireBlockName {
			// 火焰自行熄灭后重新点燃继续推进。
			if state, ok := fireStateForAge(fireBlockName, 0); ok {
				w.SetBlock(pos[0], pos[1], pos[2], state)
			}
			continue
		}
		instance.fireTickBody(w, dim, pos, props)
		ignited = w.BlockAt(tntPos[0], tntPos[1], tntPos[2]) != tnt
	}
	if !ignited {
		t.Fatal("500 次推进后相邻 TNT 未被点燃")
	}
	instance.entityMu.Lock()
	primed := len(instance.tnts)
	instance.entityMu.Unlock()
	if primed == 0 {
		t.Fatal("TNT 方块被替换但未生成已点燃的 TNT 实体")
	}
}

// TestFireSpreadThroughScheduler 验证经计划刻队列的真实路径：玩家附近的
// 木质平台上点燃火焰后，服务端 Tick 会自主推进并烧掉木板（覆盖区块加载与
// 玩家半径门控、重新排程与 fireTickBody 的连接）。
func TestFireSpreadThroughScheduler(t *testing.T) {
	instance, w, dim, baseX, floorY, baseZ := fireTestSetup(t, "SchedulerBurn")

	planks, err := registry.BlockStateID("minecraft:oak_planks")
	if err != nil {
		t.Fatal(err)
	}
	watched := make([][3]int, 0, 5)
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			w.SetBlock(baseX+dx, floorY-1, baseZ+dz, planks)
			watched = append(watched, [3]int{baseX + dx, floorY - 1, baseZ + dz})
		}
	}
	if !instance.placeFireAt(w, dim, baseX, floorY, baseZ) {
		t.Fatal("placeFireAt = false, want true")
	}

	burned := false
	for tick := 0; tick < 600 && !burned; tick++ {
		instance.tick()
		for _, cell := range watched {
			if w.BlockAt(cell[0], cell[1], cell[2]) != planks {
				burned = true
				break
			}
		}
	}
	if !burned {
		t.Fatal("600 tick 内木板未被点燃/烧毁（计划刻路径未生效）")
	}
}

// TestFireSpreadsIntoAirNearHighEncouragementFuel 验证蔓延循环会把火
// 扩散到高鼓励值燃料旁的空气（原版 getIgniteOdds + 难度/age 公式）。
// 布局：火 A；干草块在 A+(1,0,0)（相邻燃料，保证火焰不自行熄灭）；
// 观察的空气中 A+(1,0,-1)（其六邻域含干草块，鼓励值 60）。
func TestFireSpreadsIntoAirNearHighEncouragementFuel(t *testing.T) {
	instance, w, dim, baseX, floorY, baseZ := fireTestSetup(t, "SpreadWatcher")

	hay, err := registry.BlockStateID("minecraft:hay_block")
	if err != nil {
		t.Fatal(err)
	}
	firePos := [3]int{baseX, floorY, baseZ}
	hayPos := [3]int{baseX + 1, floorY, baseZ}
	targetPos := [3]int{baseX + 1, floorY, baseZ - 1}
	w.SetBlock(hayPos[0], hayPos[1], hayPos[2], hay)
	w.SetBlock(targetPos[0], targetPos[1], targetPos[2], world.AirBlock)
	if !instance.placeFireAt(w, dim, firePos[0], firePos[1], firePos[2]) {
		t.Fatal("placeFireAt = false, want true")
	}

	spread := false
	for i := 0; i < 20000 && !spread; i++ {
		if w.BlockAt(hayPos[0], hayPos[1], hayPos[2]) != hay {
			w.SetBlock(hayPos[0], hayPos[1], hayPos[2], hay)
		}
		fire, ok := fireStateForAge(fireBlockName, 0)
		if !ok {
			t.Fatal("缺少火焰状态")
		}
		w.SetBlock(firePos[0], firePos[1], firePos[2], fire)
		instance.fireTickBody(w, dim, firePos, map[string]string{"age": "0"})
		if name, _, ok := registry.StateProps(w.BlockAt(targetPos[0], targetPos[1], targetPos[2])); ok && name == fireBlockName {
			spread = true
		}
	}
	if !spread {
		t.Fatal("20000 次推进后空气未被点燃（蔓延到空气的路径未生效）")
	}
}
