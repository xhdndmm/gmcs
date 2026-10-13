package server

import (
	"math"
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 漏斗传输测试：推出/吸入、掉落物收集、红石锁定、熔炉面规则、
// 漏斗链错峰、内容持久化与破坏掉落。

// newHopperTestServer 构造一个无玩家的轻量服务器（区块随 SetBlock 加载）。
func newHopperTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = instance.worldFor(world.DimensionOverworld).Close() })
	return instance
}

// hopperTestPad 在出生点附近清出一块场地，返回地面坐标（y 为地面顶面）。
func hopperTestPad(t *testing.T, instance *Server, x, z int, height int) int {
	t.Helper()
	gameWorld := instance.testWorld()
	groundYf, ok := gameWorld.GroundY(x, z)
	if !ok {
		t.Fatalf("no ground at (%d,%d)", x, z)
	}
	groundY := int(math.Floor(groundYf))
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			gameWorld.SetBlock(x+dx, groundY, z+dz, world.StoneBlock)
			for dy := 1; dy <= height; dy++ {
				gameWorld.SetBlock(x+dx, groundY+dy, z+dz, world.AirBlock)
			}
		}
	}
	return groundY
}

// placeBlockAt 放置方块默认状态。
func placeBlockAt(t *testing.T, instance *Server, x, y, z int, name string) uint16 {
	t.Helper()
	state, ok := registry.BlockStateIDs[name]
	if !ok {
		t.Fatalf("block not found: %s", name)
	}
	if !instance.testWorld().SetBlock(x, y, z, state) {
		t.Fatalf("SetBlock failed at (%d,%d,%d)", x, y, z)
	}
	return state
}

// hopperAt 放置并注册一个指定朝向的漏斗。
func hopperAt(t *testing.T, instance *Server, x, y, z int, facing string) *hopperState {
	t.Helper()
	state, ok := registry.BlockStateWithProps(hopperBlockName, map[string]string{"facing": facing, "enabled": "true"})
	if !ok {
		t.Fatalf("hopper state not found for facing=%s", facing)
	}
	if !instance.testWorld().SetBlock(x, y, z, state) {
		t.Fatalf("SetBlock(hopper) failed at (%d,%d,%d)", x, y, z)
	}
	state2 := instance.registerHopper(world.DimensionOverworld, x, y, z)
	if state2 == nil {
		t.Fatal("registerHopper returned nil")
	}
	return state2
}

// setHopperSlot 直接写入漏斗槽位（测试用；模拟玩家或上游输入）。
func setHopperSlot(state *hopperState, slot int, stack item.Stack) {
	state.mu.Lock()
	state.slots[slot] = stack
	state.mu.Unlock()
}

// hopperSlotAt 读取漏斗槽位副本。
func hopperSlotAt(state *hopperState, slot int) item.Stack {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.slots[slot]
}

// chestItemsAt 读取箱子类容器的方块实体内容。
func chestItemsAt(t *testing.T, instance *Server, x, y, z int) []item.Stack {
	t.Helper()
	entity, err := instance.testWorld().BlockEntityAt(x, y, z)
	if err != nil {
		t.Fatal(err)
	}
	slots := make([]item.Stack, len(entity.Items))
	for i, slot := range entity.Items {
		slots[i] = item.Stack{ItemID: slot.ItemID, Count: slot.Count}
	}
	return slots
}

// setChestItems 写入箱子类容器的方块实体内容。
func setChestItems(t *testing.T, instance *Server, x, y, z int, stacks []item.Stack) {
	t.Helper()
	typeID, ok := registry.StaticEntryID("minecraft:block_entity_type", "minecraft:chest")
	if !ok {
		t.Fatal("chest block entity type missing")
	}
	items := make([]world.ContainerItem, len(stacks))
	for i, stack := range stacks {
		items[i] = world.ContainerItem{ItemID: stack.ItemID, Count: stack.Count}
	}
	if err := instance.testWorld().SetBlockEntity(x, y, z, world.BlockEntity{TypeID: typeID, Items: items}); err != nil {
		t.Fatal(err)
	}
}

// TestHopperPushesIntoContainerBelow 验证漏斗向下方容器推送：
// 每次搬运 1 个物品、冷却 8 tick（原版 MOVE_ITEM_SPEED）。
func TestHopperPushesIntoContainerBelow(t *testing.T) {
	instance := newHopperTestServer(t)
	spawnX, _, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	x, z := int(math.Floor(spawnX))+8, int(math.Floor(spawnZ))+8
	groundY := hopperTestPad(t, instance, x, z, 4)

	placeBlockAt(t, instance, x, groundY-1, z, "minecraft:chest")
	hopper := hopperAt(t, instance, x, groundY, z, "down")
	stone, err := item.FromName("minecraft:stone", 3)
	if err != nil {
		t.Fatal(err)
	}
	setHopperSlot(hopper, 0, stone)

	// 第一 tick：搬运 1 个。
	instance.tick()
	chest := chestItemsAt(t, instance, x, groundY-1, z)
	if len(chest) == 0 || chest[0].Count != 1 {
		t.Fatalf("第一 tick 后箱子内容 = %v, want 1 个石头", chest)
	}
	if got := hopperSlotAt(hopper, 0).Count; got != 2 {
		t.Fatalf("第一 tick 后漏斗剩余 = %d, want 2", got)
	}
	// 冷却期内不再搬运（7 tick 后仍是 1 个）。
	for i := 0; i < hopperMoveItemSpeed-1; i++ {
		instance.tick()
	}
	if got := hopperSlotAt(hopper, 0).Count; got != 2 {
		t.Fatalf("冷却期间漏斗剩余 = %d, want 2", got)
	}
	// 第 8 tick：再搬运 1 个。
	instance.tick()
	chest = chestItemsAt(t, instance, x, groundY-1, z)
	if chest[0].Count != 2 {
		t.Fatalf("第 8 tick 后箱子内容 = %v, want 2 个石头", chest)
	}
}

// TestHopperPullsFromContainerAbove 验证漏斗从上方容器抽取（左到右顺序）。
func TestHopperPullsFromContainerAbove(t *testing.T) {
	instance := newHopperTestServer(t)
	spawnX, _, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	x, z := int(math.Floor(spawnX))+8, int(math.Floor(spawnZ))+8
	groundY := hopperTestPad(t, instance, x, z, 4)

	hopper := hopperAt(t, instance, x, groundY, z, "down")
	placeBlockAt(t, instance, x, groundY+1, z, "minecraft:chest")
	dirt, err := item.FromName("minecraft:dirt", 5)
	if err != nil {
		t.Fatal(err)
	}
	setChestItems(t, instance, x, groundY+1, z, []item.Stack{dirt})

	instance.tick()
	if slot := hopperSlotAt(hopper, 0); slot.ItemID != dirt.ItemID || slot.Count != 1 {
		t.Fatalf("抽取后漏斗槽位 = %+v, want 1 个泥土", slot)
	}
	chest := chestItemsAt(t, instance, x, groundY+1, z)
	if chest[0].Count != 4 {
		t.Fatalf("抽取后箱子剩余 = %d, want 4", chest[0].Count)
	}
	// 冷却结束后继续抽取。
	for i := 0; i < hopperMoveItemSpeed; i++ {
		instance.tick()
	}
	if got := hopperSlotAt(hopper, 0).Count; got != 2 {
		t.Fatalf("冷却后漏斗槽位 = %d, want 2", got)
	}
}

// TestHopperCollectsDroppedItems 验证漏斗收集吸入盒内的掉落物。
func TestHopperCollectsDroppedItems(t *testing.T) {
	instance := newHopperTestServer(t)
	spawnX, _, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	x, z := int(math.Floor(spawnX))+8, int(math.Floor(spawnZ))+8
	groundY := hopperTestPad(t, instance, x, z, 4)

	hopper := hopperAt(t, instance, x, groundY, z, "down")
	// 空漏斗下方没有容器：物品留在漏斗里。
	grass, err := item.FromName("minecraft:grass_block", 2)
	if err != nil {
		t.Fatal(err)
	}
	instance.spawnItem(world.DimensionOverworld, grass, float64(x)+0.5, float64(groundY)+1.2, float64(z)+0.5, 0, 0, 0, itemPickupDelayMining)

	waitFor(t, func() bool {
		instance.tick()
		return hopperSlotAt(hopper, 0).Count == 2
	}, "漏斗应收集掉落物")
}

// TestHopperCollectsIntoLeftmostSlot 验证收集/搬运按槽位顺序选择第一个可用槽
// （原版 addItem：不优先合并，而是最左的“空槽或同类未满”槽位）。
func TestHopperCollectsIntoLeftmostSlot(t *testing.T) {
	instance := newHopperTestServer(t)
	spawnX, _, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	x, z := int(math.Floor(spawnX))+8, int(math.Floor(spawnZ))+8
	groundY := hopperTestPad(t, instance, x, z, 4)

	hopper := hopperAt(t, instance, x, groundY, z, "down")
	stone, err := item.FromName("minecraft:stone", 3)
	if err != nil {
		t.Fatal(err)
	}
	// 槽 0 空、槽 1 已有同类物品（未满）：掉落物应进入槽 0（最左可用）。
	setHopperSlot(hopper, 1, stone)
	drop, err := item.FromName("minecraft:stone", 2)
	if err != nil {
		t.Fatal(err)
	}
	instance.spawnItem(world.DimensionOverworld, drop, float64(x)+0.5, float64(groundY)+1.2, float64(z)+0.5, 0, 0, 0, itemPickupDelayMining)

	waitFor(t, func() bool {
		instance.tick()
		return hopperSlotAt(hopper, 0).Count == 2
	}, "掉落物应进入最左可用槽（槽 0）")
	if got := hopperSlotAt(hopper, 1); got.Count != 3 {
		t.Fatalf("槽 1 = %d, want 3（未被合并）", got.Count)
	}
}

// TestHopperSuctionBlockedByFullBlock 验证吸入盒被完整方块挡住时的判定
// （原版 does_not_block_hoppers 标签：蜂巢/蜂箱为例外）。
func TestHopperSuctionBlockedByFullBlock(t *testing.T) {
	instance := newHopperTestServer(t)
	spawnX, _, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	x, z := int(math.Floor(spawnX))+8, int(math.Floor(spawnZ))+8
	groundY := hopperTestPad(t, instance, x, z, 4)
	hopperAt(t, instance, x, groundY, z, "down")

	w := instance.testWorld()
	above := [3]int{x, groundY + 1, z}
	if instance.hopperSuctionBlocked(w, above) {
		t.Fatal("空气不应该阻挡吸入")
	}
	placeBlockAt(t, instance, x, groundY+1, z, "minecraft:stone")
	if !instance.hopperSuctionBlocked(w, above) {
		t.Fatal("完整方块应阻挡吸入")
	}
	// 原版判定是 isCollisionShapeFullBlock：非完整形状（如半砖）不阻挡吸入。
	slab, ok := registry.BlockStateIDs["minecraft:oak_slab"]
	if !ok {
		t.Fatal("缺少 oak_slab 方块状态")
	}
	if !w.SetBlock(x, groundY+1, z, slab) {
		t.Fatal("SetBlock(slab) failed")
	}
	if instance.hopperSuctionBlocked(w, above) {
		t.Fatal("半砖（非完整碰撞形状）不应阻挡吸入")
	}
	placeBlockAt(t, instance, x, groundY+1, z, "minecraft:bee_nest")
	if instance.hopperSuctionBlocked(w, above) {
		t.Fatal("蜂巢不应阻挡吸入（does_not_block_hoppers 标签）")
	}
	placeBlockAt(t, instance, x, groundY+1, z, "minecraft:beehive")
	if instance.hopperSuctionBlocked(w, above) {
		t.Fatal("蜂箱不应阻挡吸入（does_not_block_hoppers 标签）")
	}
}

// TestHopperLockedByRedstone 验证红石锁定：enabled=false 时不推不吸，解锁后恢复。
func TestHopperLockedByRedstone(t *testing.T) {
	instance := newHopperTestServer(t)
	spawnX, _, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	x, z := int(math.Floor(spawnX))+8, int(math.Floor(spawnZ))+8
	groundY := hopperTestPad(t, instance, x, z, 4)

	hopper := hopperAt(t, instance, x, groundY, z, "down")
	placeBlockAt(t, instance, x, groundY-1, z, "minecraft:chest")
	stone, err := item.FromName("minecraft:stone", 1)
	if err != nil {
		t.Fatal(err)
	}
	setHopperSlot(hopper, 0, stone)

	// 相邻红石块：漏斗被锁定。
	placeBlockAt(t, instance, x+1, groundY, z, "minecraft:redstone_block")
	instance.updateRedstoneAround(world.DimensionOverworld, x+1, groundY, z)
	if name, props := blockProps(t, instance, x, groundY, z); name != hopperBlockName || props["enabled"] != "false" {
		t.Fatalf("通电后漏斗 = %s %v, want enabled=false", name, props)
	}
	for i := 0; i < 20; i++ {
		instance.tick()
	}
	if got := hopperSlotAt(hopper, 0).Count; got != 1 {
		t.Fatalf("锁定期间漏斗 = %d, want 1（不搬运）", got)
	}
	if chest := chestItemsAt(t, instance, x, groundY-1, z); len(chest) != 0 && !chest[0].IsEmpty() {
		t.Fatalf("锁定期间箱子不应收到物品：%v", chest)
	}

	// 移除红石块：解锁后恢复搬运。
	if !instance.testWorld().SetBlock(x+1, groundY, z, world.AirBlock) {
		t.Fatal("无法移除红石块")
	}
	instance.updateRedstoneAround(world.DimensionOverworld, x+1, groundY, z)
	if _, props := blockProps(t, instance, x, groundY, z); props["enabled"] != "true" {
		t.Fatalf("断电后漏斗 = %v, want enabled=true", props)
	}
	instance.tick()
	if chest := chestItemsAt(t, instance, x, groundY-1, z); len(chest) == 0 || chest[0].Count != 1 {
		t.Fatalf("解锁后箱子内容 = %v, want 1 个石头", chest)
	}
}

// TestHopperIntoFurnaceFaces 验证熔炉面规则：上方只能进原料槽、
// 侧面只能进燃料槽（非燃料物品不被接受）。
func TestHopperIntoFurnaceFaces(t *testing.T) {
	instance := newHopperTestServer(t)
	spawnX, _, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	x, z := int(math.Floor(spawnX))+8, int(math.Floor(spawnZ))+8
	groundY := hopperTestPad(t, instance, x, z, 5)

	// 上方漏斗 → 熔炉原料槽（原版 SLOTS_FOR_UP = [0]）。
	placeBlockAt(t, instance, x, groundY-1, z, "minecraft:furnace")
	above := hopperAt(t, instance, x, groundY, z, "down")
	beef, err := item.FromName("minecraft:beef", 2)
	if err != nil {
		t.Fatal(err)
	}
	setHopperSlot(above, 0, beef)
	instance.tick()
	furnace := instance.registerFurnace(world.DimensionOverworld, x, groundY-1, z)
	furnace.mu.Lock()
	gotInput, gotFuel := furnace.slots[0], furnace.slots[1]
	furnace.mu.Unlock()
	if gotInput.ItemID != beef.ItemID || gotInput.Count != 1 {
		t.Fatalf("熔炉原料槽 = %+v, want 1 个生牛肉", gotInput)
	}
	if !gotFuel.IsEmpty() {
		t.Fatalf("熔炉燃料槽 = %+v, want 空", gotFuel)
	}

	// 侧面漏斗 → 熔炉燃料槽（原版 SLOTS_FOR_SIDES = [1]）。
	// 注意：同一 tick 内熔炉点火会消耗燃料，因此断言“燃料槽有煤或已在燃烧”。
	side := hopperAt(t, instance, x+1, groundY-1, z, "west")
	coal, err := item.FromName("minecraft:coal", 1)
	if err != nil {
		t.Fatal(err)
	}
	setHopperSlot(side, 0, coal)
	instance.tick()
	furnace.mu.Lock()
	gotFuel = furnace.slots[1]
	burning := furnace.burnRemaining > 0
	furnace.mu.Unlock()
	if gotFuel.ItemID != coal.ItemID && !burning {
		t.Fatalf("侧面煤炭既未进燃料槽也未燃烧：槽位 = %+v", gotFuel)
	}
	if gotFuel := hopperSlotAt(side, 0); !gotFuel.IsEmpty() {
		t.Fatalf("侧面漏斗应已交出煤炭：%+v", gotFuel)
	}

	// 侧面非燃料（石头）不被接受。
	stone, err := item.FromName("minecraft:stone", 1)
	if err != nil {
		t.Fatal(err)
	}
	setHopperSlot(side, 0, stone)
	for i := 0; i < hopperMoveItemSpeed+1; i++ {
		instance.tick()
	}
	if got := hopperSlotAt(side, 0); got.ItemID != stone.ItemID {
		t.Fatalf("非燃料物品不应进入熔炉：漏斗槽位 = %+v", got)
	}
}

// TestHopperSuckAndPushSameTick 验证原版“推出与吸入互不排斥”：
// 同一 tick 内既向朝向容器推出 1 个，又（漏斗未满时）从上方容器吸入 1 个。
func TestHopperSuckAndPushSameTick(t *testing.T) {
	instance := newHopperTestServer(t)
	spawnX, _, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	x, z := int(math.Floor(spawnX))+8, int(math.Floor(spawnZ))+8
	groundY := hopperTestPad(t, instance, x, z, 4)

	placeBlockAt(t, instance, x, groundY-1, z, "minecraft:chest")
	hopper := hopperAt(t, instance, x, groundY, z, "down")
	placeBlockAt(t, instance, x, groundY+1, z, "minecraft:chest")
	stone, err := item.FromName("minecraft:stone", 1)
	if err != nil {
		t.Fatal(err)
	}
	dirt, err := item.FromName("minecraft:dirt", 1)
	if err != nil {
		t.Fatal(err)
	}
	setHopperSlot(hopper, 0, stone)
	setChestItems(t, instance, x, groundY+1, z, []item.Stack{dirt})

	instance.tick()
	chest := chestItemsAt(t, instance, x, groundY-1, z)
	if len(chest) == 0 || chest[0].ItemID != stone.ItemID || chest[0].Count != 1 {
		t.Fatalf("下方箱子 = %v, want 1 个石头（推出）", chest)
	}
	slot := hopperSlotAt(hopper, 0)
	if slot.ItemID != dirt.ItemID || slot.Count != 1 {
		t.Fatalf("漏斗槽位 = %+v, want 1 个泥土（同一 tick 吸入）", slot)
	}
	above := chestItemsAt(t, instance, x, groundY+1, z)
	if len(above) != 0 && !above[0].IsEmpty() {
		t.Fatalf("上方箱子应已交出泥土：%v", above)
	}
}

// TestHopperChainedCooldownRequiresEmptyTarget 验证错峰冷却的触发条件：
// 只有“目标漏斗在插入前完全为空”时才设置目标冷却（原版 isEmpty 判定），
// 因此非空目标漏斗可以在同一 tick 继续搬运自己的物品。
func TestHopperChainedCooldownRequiresEmptyTarget(t *testing.T) {
	instance := newHopperTestServer(t)
	spawnX, _, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	x, z := int(math.Floor(spawnX))+8, int(math.Floor(spawnZ))+8
	groundY := hopperTestPad(t, instance, x, z, 5)

	placeBlockAt(t, instance, x, groundY-2, z, "minecraft:chest")
	lower := hopperAt(t, instance, x, groundY-1, z, "down")
	upper := hopperAt(t, instance, x, groundY, z, "down")
	sand, err := item.FromName("minecraft:sand", 1)
	if err != nil {
		t.Fatal(err)
	}
	gravel, err := item.FromName("minecraft:gravel", 1)
	if err != nil {
		t.Fatal(err)
	}
	// 下方漏斗非空（槽 0 有砂砾），上方漏斗推入沙子。
	setHopperSlot(lower, 0, gravel)
	setHopperSlot(upper, 0, sand)

	instance.tick()
	// 无论两个漏斗的 tick 顺序如何：下漏斗不受错峰冷却影响，
	// 本 tick 把最左槽（砂砾）推入箱子（沙子则留在下漏斗内，位置取决顺序）。
	chest := chestItemsAt(t, instance, x, groundY-2, z)
	if len(chest) == 0 || chest[0].ItemID != gravel.ItemID || chest[0].Count != 1 {
		t.Fatalf("箱子 = %v, want 1 个砂砾（目标非空时不设错峰冷却）", chest)
	}
	lower.mu.Lock()
	sandCount := int32(0)
	for _, stack := range lower.slots {
		if stack.ItemID == sand.ItemID {
			sandCount += stack.Count
		}
	}
	lower.mu.Unlock()
	if sandCount != 1 {
		t.Fatalf("下漏斗内沙子数 = %d, want 1", sandCount)
	}
	if got := hopperSlotAt(upper, 0); !got.IsEmpty() {
		t.Fatalf("上漏斗应已交出沙子：%+v", got)
	}
}

// TestHopperPullsFurnaceOutput 验证漏斗从下方抽取熔炉产物（原版 SLOTS_FOR_DOWN = [2,1]）。
func TestHopperPullsFurnaceOutput(t *testing.T) {
	instance := newHopperTestServer(t)
	spawnX, _, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	x, z := int(math.Floor(spawnX))+8, int(math.Floor(spawnZ))+8
	groundY := hopperTestPad(t, instance, x, z, 5)

	placeBlockAt(t, instance, x, groundY, z, "minecraft:furnace")
	below := hopperAt(t, instance, x, groundY-1, z, "down")
	furnace := instance.registerFurnace(world.DimensionOverworld, x, groundY, z)
	iron, err := item.FromName("minecraft:iron_ingot", 2)
	if err != nil {
		t.Fatal(err)
	}
	furnace.mu.Lock()
	furnace.slots[2] = iron
	furnace.mu.Unlock()

	instance.tick()
	if slot := hopperSlotAt(below, 0); slot.ItemID != iron.ItemID || slot.Count != 1 {
		t.Fatalf("抽取熔炉产物失败：%+v", slot)
	}
}

// TestHopperChainLockstep 验证漏斗链的错峰冷却：同一游戏刻内物品不会
// 连续穿过两个漏斗（下漏斗要么已搬运过、要么刚被设置为冷却）。
func TestHopperChainLockstep(t *testing.T) {
	instance := newHopperTestServer(t)
	spawnX, _, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	x, z := int(math.Floor(spawnX))+8, int(math.Floor(spawnZ))+8
	groundY := hopperTestPad(t, instance, x, z, 5)

	placeBlockAt(t, instance, x, groundY-2, z, "minecraft:chest")
	lower := hopperAt(t, instance, x, groundY-1, z, "down")
	upper := hopperAt(t, instance, x, groundY, z, "down")
	sand, err := item.FromName("minecraft:sand", 3)
	if err != nil {
		t.Fatal(err)
	}
	setHopperSlot(upper, 0, sand)

	// 第一 tick：上漏斗推出 1 个（或下漏斗从上方吸入 1 个），
	// 但下漏斗本 tick 不可能把物品再推给箱子。
	instance.tick()
	if chest := chestItemsAt(t, instance, x, groundY-2, z); len(chest) > 0 && !chest[0].IsEmpty() {
		t.Fatalf("第一 tick 物品不应到达箱子：%v", chest)
	}
	if got := hopperSlotAt(lower, 0).Count; got == 0 {
		t.Fatal("第一 tick 后下漏斗应有物品")
	}
	// 8 tick 之后，下漏斗的搬运到达箱子。
	for i := 0; i < hopperMoveItemSpeed; i++ {
		instance.tick()
	}
	chest := chestItemsAt(t, instance, x, groundY-2, z)
	if len(chest) == 0 || chest[0].Count < 1 {
		t.Fatalf("8 tick 后箱子内容 = %v, want 至少 1 个沙子", chest)
	}
	// 最终全部到达（不卡住、不复制）。
	for i := 0; i < 200; i++ {
		instance.tick()
	}
	chest = chestItemsAt(t, instance, x, groundY-2, z)
	if chest[0].Count != 3 {
		t.Fatalf("200 tick 后箱子 = %v, want 3 个沙子", chest)
	}
}

// TestHopperPersistAndBreak 验证漏斗内容随方块实体持久化、破坏时掉落。
func TestHopperPersistAndBreak(t *testing.T) {
	instance := newHopperTestServer(t)
	spawnX, _, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	x, z := int(math.Floor(spawnX))+8, int(math.Floor(spawnZ))+8
	groundY := hopperTestPad(t, instance, x, z, 4)

	hopper := hopperAt(t, instance, x, groundY, z, "down")
	stone, err := item.FromName("minecraft:stone", 7)
	if err != nil {
		t.Fatal(err)
	}
	setHopperSlot(hopper, 0, stone)
	instance.flushHopper(hopper)

	// 模拟重载：清空运行时状态后重新注册，内容应从方块实体恢复。
	instance.hopperMu.Lock()
	delete(instance.hoppers, containerKey{dim: world.DimensionOverworld, x: x, y: groundY, z: z})
	instance.hopperMu.Unlock()
	reloaded := instance.registerHopper(world.DimensionOverworld, x, groundY, z)
	if got := hopperSlotAt(reloaded, 0); got.Count != 7 {
		t.Fatalf("重载后漏斗内容 = %+v, want 7 个石头", got)
	}

	// 破坏漏斗：内容掉落到世界。
	def := instance.containerDefs[hopperBlockName]
	if def == nil {
		t.Fatal("hopper container def missing")
	}
	instance.destroyHopper(world.DimensionOverworld, nil, x, groundY, z, def)
	instance.entityMu.Lock()
	dropped := int32(0)
	for _, entity := range instance.items {
		dropped += entity.Stack.Count
	}
	instance.entityMu.Unlock()
	if dropped != 7 {
		t.Fatalf("破坏后掉落 = %d, want 7", dropped)
	}
}

// TestHopperSlotBackingSharesState 验证漏斗窗口与传输 Tick 共用同一份槽位：
// 窗口写入立即对搬运可见并写回方块实体，搬运写入也立即可被窗口读到。
func TestHopperSlotBackingSharesState(t *testing.T) {
	instance := newHopperTestServer(t)
	spawnX, _, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	x, z := int(math.Floor(spawnX))+8, int(math.Floor(spawnZ))+8
	groundY := hopperTestPad(t, instance, x, z, 4)

	hopper := hopperAt(t, instance, x, groundY, z, "down")
	player := &session{server: instance, name: "Tester"}
	backing := hopperSlotBacking{server: instance, player: player, state: hopper}
	if got := backing.SlotCount(); got != hopperSlots+36 {
		t.Fatalf("SlotCount() = %d, want %d", got, hopperSlots+36)
	}

	stone, err := item.FromName("minecraft:stone", 5)
	if err != nil {
		t.Fatal(err)
	}
	backing.Set(0, stone)
	if got := hopperSlotAt(hopper, 0); !got.Equal(stone) {
		t.Fatalf("窗口写入后搬运状态 = %+v, want %+v", got, stone)
	}
	entity, err := instance.testWorld().BlockEntityAt(x, groundY, z)
	if err != nil {
		t.Fatal(err)
	}
	if len(entity.Items) == 0 || entity.Items[0].ItemID != stone.ItemID || entity.Items[0].Count != stone.Count {
		t.Fatalf("窗口写入应写回方块实体：%+v", entity.Items)
	}

	dirt, err := item.FromName("minecraft:dirt", 2)
	if err != nil {
		t.Fatal(err)
	}
	setHopperSlot(hopper, 1, dirt)
	if got := backing.Get(1); !got.Equal(dirt) {
		t.Fatalf("窗口读取搬运状态 = %+v, want %+v", got, dirt)
	}
}

// TestHopperSlotBackingPlayerSlots 验证漏斗窗口后端报告的槽位数与玩家后方槽位映射。
func TestHopperSlotBackingPlayerSlots(t *testing.T) {
	player := &session{name: "Tester"}
	backing := hopperSlotBacking{player: player, state: &hopperState{}}
	tool, err := item.FromName("minecraft:stone_pickaxe", 1)
	if err != nil {
		t.Fatal(err)
	}
	// 窗口槽位 5 起为玩家主背包（内部槽位 9 起）。
	backing.Set(hopperSlots, tool)
	if got := player.inventory.Get(item.SlotMainStart); !got.Equal(tool) {
		t.Fatalf("主背包槽位 = %+v, want %+v", got, tool)
	}
	if got := backing.Get(hopperSlots); !got.Equal(tool) {
		t.Fatalf("窗口读取主背包 = %+v, want %+v", got, tool)
	}
}

// TestHopperPlacementFacing 验证放置朝向：点击面反方向，竖直退化为向下。
func TestHopperPlacementFacing(t *testing.T) {
	cases := []struct {
		clicked int
		want    string
	}{
		{0, "down"},  // 点击底面（-Y）→ UP 退化为 DOWN
		{1, "down"},  // 点击顶面（+Y）→ DOWN
		{2, "south"}, // 点击北面 → 朝南
		{3, "north"}, // 点击南面 → 朝北
		{4, "east"},  // 点击西面 → 朝东
		{5, "west"},  // 点击东面 → 朝西
	}
	for _, tc := range cases {
		state, ok := orientHopperPlacement(tc.clicked)
		if !ok {
			t.Fatalf("orientHopperPlacement(%d) failed", tc.clicked)
		}
		name, props, ok := registry.StateProps(state)
		if !ok || name != hopperBlockName {
			t.Fatalf("orientation(%d) = %s", tc.clicked, name)
		}
		if props["facing"] != tc.want {
			t.Fatalf("orientHopperPlacement(%d) facing = %s, want %s", tc.clicked, props["facing"], tc.want)
		}
	}
}

// TestHopperRegistersInNewlyLoadedChunk 验证“区块加载后注册”路径：
// 直接从世界（磁盘/生成器）加载进来的漏斗不需要交互即可参与传输。
func TestHopperRegistersInNewlyLoadedChunk(t *testing.T) {
	instance := newHopperTestServer(t)
	spawnX, _, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	x, z := int(math.Floor(spawnX))+8, int(math.Floor(spawnZ))+8
	hopperTestPad(t, instance, x, z, 4)
	// 消费出生点附近区块的加载记录，保证下面的新漏斗位于“新加载”的区块里。
	instance.tick()

	// 远处（另一个区块）直接写入漏斗与下方箱子：不经过放置路径注册。
	farX, farZ := x+40, z+40
	farGround := hopperTestPad(t, instance, farX, farZ, 4)
	placeBlockAt(t, instance, farX, farGround-1, farZ, "minecraft:chest")
	state, ok := registry.BlockStateWithProps(hopperBlockName, map[string]string{"facing": "down", "enabled": "true"})
	if !ok {
		t.Fatal("hopper state not found")
	}
	if !instance.testWorld().SetBlock(farX, farGround, farZ, state) {
		t.Fatal("SetBlock(hopper) failed")
	}
	stone, err := item.FromName("minecraft:stone", 2)
	if err != nil {
		t.Fatal(err)
	}
	entity := world.BlockEntity{
		TypeID: instance.hopperTypeID,
		Items: []world.ContainerItem{
			{ItemID: stone.ItemID, Count: stone.Count},
		},
	}
	if err := instance.testWorld().SetBlockEntity(farX, farGround, farZ, entity); err != nil {
		t.Fatal(err)
	}

	key := containerKey{dim: world.DimensionOverworld, x: farX, y: farGround, z: farZ}
	instance.hopperMu.Lock()
	_, registered := instance.hoppers[key]
	instance.hopperMu.Unlock()
	if registered {
		t.Fatal("测试前提不成立：漏斗不应已被注册")
	}

	// 一次 Tick：新加载区块被消费并注册，本 tick 即开始搬运。
	instance.tick()
	instance.hopperMu.Lock()
	state2, ok := instance.hoppers[key]
	instance.hopperMu.Unlock()
	if !ok {
		t.Fatal("新加载区块中的漏斗应被自动注册")
	}
	if got := hopperSlotAt(state2, 0).Count; got != 1 {
		t.Fatalf("第一 tick 后漏斗 = %d, want 1", got)
	}
	if chest := chestItemsAt(t, instance, farX, farGround-1, farZ); len(chest) == 0 || chest[0].Count != 1 {
		t.Fatalf("第一 tick 后箱子 = %v, want 1 个石头", chest)
	}
}

// TestHopperDroppedOnChunkUnload 验证区块卸载时丢弃漏斗注册：
// 内容已随卸载写盘，重新注册后从方块实体恢复为一致内容（不复制、不丢失）。
func TestHopperDroppedOnChunkUnload(t *testing.T) {
	instance := newHopperTestServer(t)
	spawnX, _, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	x, z := int(math.Floor(spawnX))+8, int(math.Floor(spawnZ))+8
	groundY := hopperTestPad(t, instance, x, z, 4)

	hopper := hopperAt(t, instance, x, groundY, z, "down")
	stone, err := item.FromName("minecraft:stone", 3)
	if err != nil {
		t.Fatal(err)
	}
	setHopperSlot(hopper, 0, stone)
	instance.flushHopper(hopper)

	instance.dropHoppersInChunks(world.DimensionOverworld, []world.ChunkPos{{X: x >> 4, Z: z >> 4}})
	key := containerKey{dim: world.DimensionOverworld, x: x, y: groundY, z: z}
	instance.hopperMu.Lock()
	_, registered := instance.hoppers[key]
	instance.hopperMu.Unlock()
	if registered {
		t.Fatal("卸载区块后漏斗注册应被丢弃")
	}

	// 重新加载：内容来自方块实体（与丢弃前一致）。
	reloaded := instance.registerHopper(world.DimensionOverworld, x, groundY, z)
	if got := hopperSlotAt(reloaded, 0); got.Count != 3 || got.ItemID != stone.ItemID {
		t.Fatalf("重新注册后漏斗内容 = %+v, want 3 个石头", got)
	}
}
