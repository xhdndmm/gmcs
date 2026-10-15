//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 发射器/投掷器测试：红石触发、随机槽位消耗、弹出与容器插入、
// 箭矢/TNT/生成蛋/桶行为。

// placeDispenserForTest 放置一个发射器/投掷器并注册其方块实体。
func placeDispenserForTest(t *testing.T, instance *Server, x, y, z int, name, facing string) uint16 {
	t.Helper()
	state := placeState(t, instance, x, y, z, name,
		map[string]string{"facing": facing, "triggered": "false"})
	instance.refreshDispenser(instance.testWorld(), [3]int{x, y, z}, state)
	return state
}

// setDispenserSlot 写入发射器方块实体的指定槽位。
func setDispenserSlot(t *testing.T, instance *Server, x, y, z int, typeName string, slot int, stack item.Stack) {
	t.Helper()
	typeID, ok := registry.StaticEntryID("minecraft:block_entity_type", typeName)
	if !ok {
		t.Fatalf("block entity type missing: %s", typeName)
	}
	entity, err := instance.testWorld().BlockEntityAt(x, y, z)
	if err != nil {
		entity = world.BlockEntity{}
	}
	entity.TypeID = typeID
	for len(entity.Items) < 9 {
		entity.Items = append(entity.Items, world.ContainerItem{})
	}
	entity.Items[slot] = containerItemFromStack(stack)
	if err := instance.testWorld().SetBlockEntity(x, y, z, entity); err != nil {
		t.Fatalf("SetBlockEntity: %v", err)
	}
}

// dispenserSlot 读取发射器方块实体的指定槽位。
func dispenserSlot(t *testing.T, instance *Server, x, y, z int, slot int) item.Stack {
	t.Helper()
	entity, err := instance.testWorld().BlockEntityAt(x, y, z)
	if err != nil || slot >= len(entity.Items) {
		return item.Empty()
	}
	return stackFromContainer(entity.Items[slot])
}

// triggerDispenser 用红石块给发射器供电并推进 4 tick 计划刻触发一次发射。
func triggerDispenser(t *testing.T, instance *Server, x, y, z int) {
	t.Helper()
	poweredBy := [3]int{x - 1, y, z}
	instance.testWorld().SetBlock(poweredBy[0], poweredBy[1], poweredBy[2], world.StoneBlock)
	redstoneState, ok := registry.BlockStateIDs["minecraft:redstone_block"]
	if !ok {
		t.Fatal("redstone block state missing")
	}
	instance.testWorld().SetBlock(poweredBy[0], poweredBy[1], poweredBy[2], redstoneState)
	instance.broadcastBlockUpdate(world.DimensionOverworld, poweredBy[0], poweredBy[1], poweredBy[2], int32(redstoneState))
	instance.updateRedstoneAround(world.DimensionOverworld, poweredBy[0], poweredBy[1], poweredBy[2])
	for i := 0; i < dispenserTriggerDelay; i++ {
		instance.tickRedstone()
	}
	// 清理电源（触发下降沿 → triggered=false），不影响已执行的行为。
	instance.testWorld().SetBlock(poweredBy[0], poweredBy[1], poweredBy[2], world.AirBlock)
	instance.updateRedstoneAround(world.DimensionOverworld, poweredBy[0], poweredBy[1], poweredBy[2])
}

// TestDispenserEjectsItemWithRedstonePulse 验证发射器上升沿触发、4 tick 后
// 弹出物品，且下降沿清除 triggered。
func TestDispenserEjectsItemWithRedstonePulse(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "DispensePulse")

	baseX, floorY, baseZ := fluidBase(t, instance)
	stack, err := item.FromName("minecraft:stone", 3)
	if err != nil {
		t.Fatal(err)
	}
	state := placeDispenserForTest(t, instance, baseX, floorY, baseZ, dispenserBlockName, "east")
	setDispenserSlot(t, instance, baseX, floorY, baseZ, dispenserBlockName, 0, stack)

	// 触发前无计划刻、未触发。
	if name, props := blockProps(t, instance, baseX, floorY, baseZ); name != dispenserBlockName || props["triggered"] != "false" {
		t.Fatalf("initial dispenser = %s %v, want triggered=false", name, props)
	}
	_ = state

	triggerDispenser(t, instance, baseX, floorY, baseZ)

	// 物品从槽位消耗 1 个。
	remaining := dispenserSlot(t, instance, baseX, floorY, baseZ, 0)
	if remaining.Count != 2 {
		t.Fatalf("dispenser slot after dispensing = %d, want 2", remaining.Count)
	}
	// 前排位置出现掉落物。
	instance.entityMu.Lock()
	itemCount := len(instance.items)
	var front *itemEntity
	for _, e := range instance.items {
		front = e
	}
	instance.entityMu.Unlock()
	if itemCount != 1 {
		t.Fatalf("items spawned = %d, want 1", itemCount)
	}
	if front == nil || front.X < float64(baseX)+1 {
		t.Fatalf("ejected item at %+v, want at x >= %d", front, baseX+2)
	}
	// 上升沿后 triggered 应为 true（供电期间），下降沿后清除。
	if props := instance.testWorld().BlockAt(baseX, floorY, baseZ); props != 0 {
		if _, p, ok := registry.StateProps(props); ok && p["triggered"] != "false" {
			t.Fatalf("triggered after power removal = %q, want false", p["triggered"])
		}
	}
}

// TestDispenserTriggersOncePerRisingEdge 验证供电保持期间只发射一次。
func TestDispenserTriggersOncePerRisingEdge(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "DispenseOnce")

	baseX, floorY, baseZ := fluidBase(t, instance)
	stack, _ := item.FromName("minecraft:stone", 5)
	placeDispenserForTest(t, instance, baseX, floorY, baseZ, dispenserBlockName, "up")
	setDispenserSlot(t, instance, baseX, floorY, baseZ, dispenserBlockName, 0, stack)

	// 上升沿触发后保持供电：再推进多个 4 tick 也不会重复发射。
	poweredBy := [3]int{baseX - 1, floorY, baseZ}
	redstoneState, _ := registry.BlockStateIDs["minecraft:redstone_block"]
	instance.testWorld().SetBlock(poweredBy[0], poweredBy[1], poweredBy[2], redstoneState)
	instance.updateRedstoneAround(world.DimensionOverworld, poweredBy[0], poweredBy[1], poweredBy[2])
	runTicks(instance, dispenserTriggerDelay*4)
	if remaining := dispenserSlot(t, instance, baseX, floorY, baseZ, 0); remaining.Count != 4 {
		t.Fatalf("slot after long power = %d, want 4 (one dispense)", remaining.Count)
	}
}

// TestDropperInsertsIntoContainer 验证投掷器把物品插入前方容器。
func TestDropperInsertsIntoContainer(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "DropperInsert")

	baseX, floorY, baseZ := fluidBase(t, instance)
	stack, _ := item.FromName("minecraft:stone", 2)
	placeDispenserForTest(t, instance, baseX, floorY, baseZ, dropperBlockName, "east")
	setDispenserSlot(t, instance, baseX, floorY, baseZ, dropperBlockName, 0, stack)

	// 前方放置箱子。
	chestState, _ := registry.BlockStateIDs["minecraft:chest"]
	instance.testWorld().SetBlock(baseX+1, floorY, baseZ, chestState)
	instance.updateRedstoneAround(world.DimensionOverworld, baseX+1, floorY, baseZ)

	triggerDispenser(t, instance, baseX, floorY, baseZ)

	entity, err := instance.testWorld().BlockEntityAt(baseX+1, floorY, baseZ)
	if err != nil || len(entity.Items) == 0 || entity.Items[0].ItemID == 0 {
		t.Fatalf("chest content after dropper = %+v err=%v, want one stone", entity.Items, err)
	}
	if entity.Items[0].Count != 1 {
		t.Fatalf("chest slot count = %d, want 1", entity.Items[0].Count)
	}
	if remaining := dispenserSlot(t, instance, baseX, floorY, baseZ, 0); remaining.Count != 1 {
		t.Fatalf("dropper slot after insert = %d, want 1", remaining.Count)
	}
}

// TestDropperEjectsWithoutContainer 验证前方无容器时投掷器弹出物品。
func TestDropperEjectsWithoutContainer(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "DropperEject")

	baseX, floorY, baseZ := fluidBase(t, instance)
	stack, _ := item.FromName("minecraft:stone", 1)
	placeDispenserForTest(t, instance, baseX, floorY, baseZ, dropperBlockName, "east")
	setDispenserSlot(t, instance, baseX, floorY, baseZ, dropperBlockName, 0, stack)
	triggerDispenser(t, instance, baseX, floorY, baseZ)

	instance.entityMu.Lock()
	count := len(instance.items)
	instance.entityMu.Unlock()
	if count != 1 {
		t.Fatalf("ejected items = %d, want 1", count)
	}
	if remaining := dispenserSlot(t, instance, baseX, floorY, baseZ, 0); !remaining.IsEmpty() {
		t.Fatalf("dropper slot after eject = %+v, want empty", remaining)
	}
}

// TestDispenserShootsArrow 验证发射器按朝向射出箭矢。
func TestDispenserShootsArrow(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "DispenseArrow")
	if !instance.arrowsEnabled {
		t.Skip("arrows disabled in this build")
	}

	baseX, floorY, baseZ := fluidBase(t, instance)
	stack, _ := item.FromName("minecraft:arrow", 4)
	placeDispenserForTest(t, instance, baseX, floorY, baseZ, dispenserBlockName, "east")
	setDispenserSlot(t, instance, baseX, floorY, baseZ, dispenserBlockName, 0, stack)
	triggerDispenser(t, instance, baseX, floorY, baseZ)

	instance.entityMu.Lock()
	count := len(instance.arrows)
	var velocityX float64
	for _, a := range instance.arrows {
		velocityX = a.VelX
	}
	instance.entityMu.Unlock()
	if count != 1 {
		t.Fatalf("arrows after dispense = %d, want 1", count)
	}
	if velocityX < 0.9 || velocityX > 1.3 {
		t.Fatalf("arrow velocity x = %v, want ~1.1 (east)", velocityX)
	}
	if remaining := dispenserSlot(t, instance, baseX, floorY, baseZ, 0); remaining.Count != 3 {
		t.Fatalf("arrow slot count = %d, want 3", remaining.Count)
	}
}

// TestDispenserPlacesAndPicksBucketFluid 验证发射器的倒水与装桶行为。
func TestDispenserPlacesAndPicksBucketFluid(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "DispenseBucket")

	baseX, floorY, baseZ := fluidBase(t, instance)
	waterBucket, err := item.FromName(waterBucketItemName, 1)
	if err != nil {
		t.Fatal(err)
	}
	placeDispenserForTest(t, instance, baseX, floorY, baseZ, dispenserBlockName, "east")
	setDispenserSlot(t, instance, baseX, floorY, baseZ, dispenserBlockName, 0, waterBucket)
	triggerDispenser(t, instance, baseX, floorY, baseZ)

	name, level, ok := fluidProps(t, instance, baseX+1, floorY, baseZ)
	if !ok || name != waterBlockName || level != "0" {
		t.Fatalf("dispensed fluid: name=%q level=%q ok=%v, want water source", name, level, ok)
	}
	bucketID, _ := registry.ItemID(bucketItemName)
	if remaining := dispenserSlot(t, instance, baseX, floorY, baseZ, 0); remaining.ItemID != bucketID || remaining.Count != 1 {
		t.Fatalf("slot after emptying = %+v, want empty bucket", remaining)
	}

	// 第二个发射器用空桶收集前方水源。
	x2 := baseX + 5
	emptyBucket, _ := item.FromName(bucketItemName, 1)
	placeDispenserForTest(t, instance, x2, floorY, baseZ, dispenserBlockName, "east")
	setDispenserSlot(t, instance, x2, floorY, baseZ, dispenserBlockName, 0, emptyBucket)
	placeFluidSourceForTest(t, instance, x2+1, floorY, baseZ, waterBlockName)
	triggerDispenser(t, instance, x2, floorY, baseZ)

	if state := instance.testWorld().BlockAt(x2+1, floorY, baseZ); state != world.AirBlock {
		t.Fatalf("source after pickup = %d, want air", state)
	}
	waterBucketID, _ := registry.ItemID(waterBucketItemName)
	if remaining := dispenserSlot(t, instance, x2, floorY, baseZ, 0); remaining.ItemID != waterBucketID || remaining.Count != 1 {
		t.Fatalf("slot after filling = %+v, want water bucket", remaining)
	}
}

// TestDispenserSpawnsMobFromEgg 验证生成蛋发射生成生物。
func TestDispenserSpawnsMobFromEgg(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "DispenseEgg")

	baseX, floorY, baseZ := fluidBase(t, instance)
	egg, err := item.FromName("minecraft:zombie_spawn_egg", 2)
	if err != nil {
		t.Fatal(err)
	}
	placeDispenserForTest(t, instance, baseX, floorY, baseZ, dispenserBlockName, "east")
	setDispenserSlot(t, instance, baseX, floorY, baseZ, dispenserBlockName, 0, egg)
	triggerDispenser(t, instance, baseX, floorY, baseZ)

	instance.entityMu.Lock()
	count := len(instance.mobs)
	var kind mobKind
	for _, m := range instance.mobs {
		kind = m.Kind
	}
	instance.entityMu.Unlock()
	if count != 1 || kind != mobZombie {
		t.Fatalf("mobs after spawn egg = %d kind=%v, want one zombie", count, kind)
	}
	if remaining := dispenserSlot(t, instance, baseX, floorY, baseZ, 0); remaining.Count != 1 {
		t.Fatalf("egg slot count = %d, want 1", remaining.Count)
	}
}

// TestDispenserPrimesTnt 验证打火石发射器点燃前方 TNT 方块。
func TestDispenserPrimesTnt(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "DispenseTnt")
	if !instance.tntsEnabled {
		t.Skip("tnt disabled in this build")
	}

	baseX, floorY, baseZ := fluidBase(t, instance)
	flint, err := item.FromName("minecraft:flint_and_steel", 1)
	if err != nil {
		t.Fatal(err)
	}
	tntState, ok := registry.BlockStateIDs["minecraft:tnt"]
	if !ok {
		t.Fatal("tnt block missing")
	}
	instance.testWorld().SetBlock(baseX+1, floorY, baseZ, tntState)
	placeDispenserForTest(t, instance, baseX, floorY, baseZ, dispenserBlockName, "east")
	setDispenserSlot(t, instance, baseX, floorY, baseZ, dispenserBlockName, 0, flint)
	triggerDispenser(t, instance, baseX, floorY, baseZ)

	if state := instance.testWorld().BlockAt(baseX+1, floorY, baseZ); state != world.AirBlock {
		t.Fatalf("tnt block after prime = %d, want air", state)
	}
	instance.entityMu.Lock()
	count := len(instance.tnts)
	fuse := 0
	for _, e := range instance.tnts {
		fuse = e.Fuse
	}
	instance.entityMu.Unlock()
	if count != 1 {
		t.Fatalf("primed tnt = %d, want 1", count)
	}
	if fuse <= 0 || fuse > tntFuseTicks {
		t.Fatalf("fuse = %d, want (0, %d]", fuse, tntFuseTicks)
	}
}

// TestPrimedTntExplodesAndDestroysBlocks 验证 TNT 引信耗尽后爆炸并摧毁方块。
func TestPrimedTntExplodesAndDestroysBlocks(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "TntBoom")
	if !instance.tntsEnabled {
		t.Skip("tnt disabled in this build")
	}

	baseX, floorY, baseZ := fluidBase(t, instance)
	tnt, err := item.FromName("minecraft:tnt", 1)
	if err != nil {
		t.Fatal(err)
	}
	placeDispenserForTest(t, instance, baseX, floorY, baseZ, dispenserBlockName, "east")
	setDispenserSlot(t, instance, baseX, floorY, baseZ, dispenserBlockName, 0, tnt)
	triggerDispenser(t, instance, baseX, floorY, baseZ)

	// 目标位置放一个方块用于验证炸毁。
	instance.testWorld().SetBlock(baseX+2, floorY, baseZ, world.StoneBlock)
	runTicks(instance, tntFuseTicks+10)

	instance.entityMu.Lock()
	remaining := len(instance.tnts)
	instance.entityMu.Unlock()
	if remaining != 0 {
		t.Fatalf("primed tnt after fuse = %d, want 0", remaining)
	}
	if state := instance.testWorld().BlockAt(baseX+2, floorY, baseZ); state != world.AirBlock {
		t.Fatalf("block near explosion = %d, want destroyed (air)", state)
	}
}
