//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"

	"gmcs/internal/world"
)

// slotCount 解析 Set Player Inventory 包中的堆叠数量（字段：包 ID、槽位、数量）。
func slotCount(t *testing.T, packet []byte) int32 {
	t.Helper()
	return packetField(t, packet, 3)
}

// packetField 依次解码 packet 的前 n 个 VarInt 字段并返回第 n 个（1 起）。
func packetField(t *testing.T, packet []byte, n int) int32 {
	t.Helper()
	value := int32(0)
	for i := 1; i <= n; i++ {
		v, used, err := protocol.DecodeVarInt(packet)
		if err != nil {
			t.Fatalf("decode varint #%d: %v", i, err)
		}
		value = v
		packet = packet[used:]
	}
	return value
}

// TestPlayerDropItem 验证 Q / Ctrl+Q 丢弃：物品栏减少、掉落物实体生成
// （Add Entity + Item 元数据），第二次丢弃合并进已有堆叠。
func TestPlayerDropItem(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:stone*3"}
	instance, conn := joinServer(t, cfg, "Dropper")
	player := findSession(t, instance, "Dropper")

	// Q（状态 4 = DROP_ITEM）：丢出 1 个。先收到槽位更新，再收到实体广播。
	sendPlayerAction(t, conn, 4, 0, 0, 0, 0)
	invPacket := expectPlayPacket(t, conn, protocol.PlayPacketIDSetPlayerInventory)
	if count := slotCount(t, invPacket); count != 2 {
		t.Fatalf("inventory count after drop = %d, want 2", count)
	}
	expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)
	expectPlayPacket(t, conn, protocol.PlayPacketIDEntityMetadata)
	if stack := player.inventory.Get(item.SlotHotbarStart); stack.Count != 2 {
		t.Fatalf("inventory = %+v, want stone x2", stack)
	}

	instance.entityMu.Lock()
	items := make([]*itemEntity, 0, len(instance.items))
	for _, e := range instance.items {
		items = append(items, e)
	}
	instance.entityMu.Unlock()
	if len(items) != 1 || items[0].Stack.Count != 1 {
		t.Fatalf("dropped items = %+v, want one stone", items)
	}

	// Ctrl+Q（状态 3 = DROP_ALL_ITEMS）：丢出剩余整组。
	sendPlayerAction(t, conn, 3, 0, 0, 0, 0)
	invPacket = expectPlayPacket(t, conn, protocol.PlayPacketIDSetPlayerInventory)
	if count := slotCount(t, invPacket); count != 0 {
		t.Fatalf("inventory after drop stack = %d, want empty", count)
	}
	// 合并为周期检查（原版：跨方块边界移动时每 2 tick、静止时每 40 tick）。
	// 两个堆叠抛出后一起飞行/下落，合并帧很快到来；最多推进 39 tick，
	// 避免拾取延迟（40）到期把物品捡回背包。
	for i := 0; i < itemMergeIntervalStationary-1; i++ {
		instance.entityMu.Lock()
		merged := len(instance.items) == 1
		instance.entityMu.Unlock()
		if merged {
			break
		}
		instance.tick()
	}
	if stack := player.inventory.Get(item.SlotHotbarStart); !stack.IsEmpty() {
		t.Fatalf("inventory after drop stack = %+v, want empty", stack)
	}
	instance.entityMu.Lock()
	var total int32
	countEntities := len(instance.items)
	for _, e := range instance.items {
		total += e.Stack.Count
	}
	instance.entityMu.Unlock()
	if countEntities != 1 || total != 3 {
		t.Fatalf("after merge: %d entities, total %d, want 1 entity with 3", countEntities, total)
	}
}

// TestItemPickup 验证拾取：合并进同类堆叠、发送槽位/拾取动画/音效与移除。
func TestItemPickup(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Picker")
	player := findSession(t, instance, "Picker")
	x, y, z, _, _ := player.playerPosition()

	stone, err := item.FromName("minecraft:stone", 2)
	if err != nil {
		t.Fatal(err)
	}
	if instance.spawnItem(world.DimensionOverworld, stone, x, y, z, 0, 0, 0, 0) == nil {
		t.Fatal("spawnItem returned nil")
	}
	expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)
	expectPlayPacket(t, conn, protocol.PlayPacketIDEntityMetadata)

	// 一帧后拾取：背包中的 1 个石头与掉落的 2 个合并为 3。
	instance.tick()
	invPacket := expectPlayPacket(t, conn, protocol.PlayPacketIDSetPlayerInventory)
	if count := slotCount(t, invPacket); count != 3 {
		t.Fatalf("inventory count after pickup = %d, want 3", count)
	}
	collect := expectPlayPacket(t, conn, protocol.PlayPacketIDCollect)
	if picked := packetField(t, collect, 4); picked != 2 { // 包 ID、实体 ID、收集者 ID、数量
		t.Fatalf("pickup count = %d, want 2", picked)
	}
	expectPlayPacket(t, conn, protocol.PlayPacketIDSoundEffect)
	expectPlayPacket(t, conn, protocol.PlayPacketIDRemoveEntities)

	if stack := player.inventory.Get(item.SlotHotbarStart); stack.Count != 3 {
		t.Fatalf("inventory = %+v, want stone x3", stack)
	}
	instance.entityMu.Lock()
	remaining := len(instance.items)
	instance.entityMu.Unlock()
	if remaining != 0 {
		t.Fatalf("items remaining = %d, want 0", remaining)
	}
}

// TestItemMergeOverflow 验证周期合并：40 + 30 合并为 64 + 6（两个实体）。
func TestItemMergeOverflow(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Merger")
	player := findSession(t, instance, "Merger")
	x, y, z, _, _ := player.playerPosition()

	first, err := item.FromName("minecraft:stone", 40)
	if err != nil {
		t.Fatal(err)
	}
	second, err := item.FromName("minecraft:stone", 30)
	if err != nil {
		t.Fatal(err)
	}
	instance.spawnItem(world.DimensionOverworld, first, x, y, z, 0, 0, 0, itemPickupDelayPlayer)
	expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)
	expectPlayPacket(t, conn, protocol.PlayPacketIDEntityMetadata)
	instance.spawnItem(world.DimensionOverworld, second, x, y, z, 0, 0, 0, itemPickupDelayPlayer)
	expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)
	expectPlayPacket(t, conn, protocol.PlayPacketIDEntityMetadata)

	// 周期合并：两个静止堆叠在 age % 40 == 0 的帧合并（原版静止间隔）。
	// 把拾取延迟调高避免 40 tick 后拾取改变断言（合并本身不看拾取延迟）。
	instance.entityMu.Lock()
	for _, e := range instance.items {
		e.PickupDelayTicks = 100 * itemPickupDelayPlayer
	}
	instance.entityMu.Unlock()
	for i := 0; i < itemMergeIntervalStationary; i++ {
		instance.tick()
	}

	instance.entityMu.Lock()
	counts := make([]int32, 0, len(instance.items))
	var total int32
	for _, e := range instance.items {
		counts = append(counts, e.Stack.Count)
		total += e.Stack.Count
	}
	instance.entityMu.Unlock()
	if len(counts) != 2 || total != 70 {
		t.Fatalf("entities = %v (total %d), want [64 6] (total 70)", counts, total)
	}
}

// TestItemDespawnAndVoid 验证过期消失与掉入虚空的移除。
func TestItemDespawnAndVoid(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Waster")
	player := findSession(t, instance, "Waster")
	x, y, z, _, _ := player.playerPosition()

	stack, err := item.FromName("minecraft:stone", 1)
	if err != nil {
		t.Fatal(err)
	}
	// 即将过期：下一帧移除。
	e := instance.spawnItem(world.DimensionOverworld, stack, x, y+3, z, 0, 0, 0, 0)
	expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)
	expectPlayPacket(t, conn, protocol.PlayPacketIDEntityMetadata)
	instance.entityMu.Lock()
	e.AgeTicks = itemDespawnTicks - 1
	instance.entityMu.Unlock()
	instance.tick()
	expectPlayPacket(t, conn, protocol.PlayPacketIDRemoveEntities)

	// 掉入虚空：位置低于世界底部，直接移除。
	if instance.spawnItem(world.DimensionOverworld, stack, x, float64(-96), z, 0, -1, 0, 0) == nil {
		t.Fatal("spawnItem returned nil for void item")
	}
	expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)
	expectPlayPacket(t, conn, protocol.PlayPacketIDEntityMetadata)
	instance.tick()
	expectPlayPacket(t, conn, protocol.PlayPacketIDRemoveEntities)

	instance.entityMu.Lock()
	remaining := len(instance.items)
	instance.entityMu.Unlock()
	if remaining != 0 {
		t.Fatalf("items remaining = %d, want 0", remaining)
	}
}

// TestDeathDropsInventory 验证死亡掉落：物品栏清空、掉落物生成。
func TestDeathDropsInventory(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:stone*5"}
	instance, _ := joinServer(t, cfg, "Victim2")
	player := findSession(t, instance, "Victim2")

	if !instance.damagePlayer(player, maxPlayerHealth, "Zombie", -1, instance.mobAttackDamageTypeID, nil) {
		t.Fatal("lethal damage should apply")
	}
	if !player.isDead() {
		t.Fatal("player should be dead")
	}
	if stack := player.inventory.Get(item.SlotHotbarStart); !stack.IsEmpty() {
		t.Fatalf("inventory after death = %+v, want empty", stack)
	}
	instance.entityMu.Lock()
	var total int32
	for _, e := range instance.items {
		total += e.Stack.Count
	}
	instance.entityMu.Unlock()
	if total != 5 {
		t.Fatalf("dropped total = %d, want 5", total)
	}
}

// TestItemMergeAcrossBuckets 验证合并空间索引的相邻桶扫描：两个掉落物
// 位于桶格边界两侧（间距 0.2 格）时必须合并（回归：分桶不能漏检邻桶候选）。
func TestItemMergeAcrossBuckets(t *testing.T) {
	instance := newHopperTestServer(t)
	groundY := hopperTestPad(t, instance, 101, 100, 3)

	// 桶格 2 格：101.9 → 格 50、102.1 → 格 51，正好跨边界。
	if entityBucketCoord(101.9) == entityBucketCoord(102.1) {
		t.Fatal("测试前提不成立：两坐标应在不同桶格")
	}
	stack, err := item.FromName("minecraft:stone", 10)
	if err != nil {
		t.Fatal(err)
	}
	y := float64(groundY) + 1.5
	if instance.spawnItem(world.DimensionOverworld, stack, 101.9, y, 100.5, 0, 0, 0, itemPickupDelayMining) == nil {
		t.Fatal("spawnItem returned nil")
	}
	if instance.spawnItem(world.DimensionOverworld, stack, 102.1, y, 100.5, 0, 0, 0, itemPickupDelayMining) == nil {
		t.Fatal("spawnItem returned nil")
	}

	// 合并为周期检查（原版：移动中每 2 tick、静止每 40 tick）：两个堆叠
	// 下落时跨越方块边界，合并帧很快出现；上限一个静止周期。
	for i := 0; i < itemMergeIntervalStationary; i++ {
		instance.entityMu.Lock()
		merged := len(instance.items) == 1
		instance.entityMu.Unlock()
		if merged {
			break
		}
		instance.tick()
	}

	instance.entityMu.Lock()
	count := len(instance.items)
	var total int32
	for _, e := range instance.items {
		total += e.Stack.Count
	}
	instance.entityMu.Unlock()
	if count != 1 || total != 20 {
		t.Fatalf("跨桶格合并失败：entities = %d, total = %d, want 1/20", count, total)
	}
}

// TestItemMergeStableWithFullStack 验证容量上限下的合并稳定性：10 个 7 的
// 堆叠（总 70）在任何处理顺序下都合并为 64 + 6，且后续合并帧不再变化
// （回归：空间索引快照的残留条目不能让计数漂移）。
func TestItemMergeStableWithFullStack(t *testing.T) {
	instance := newHopperTestServer(t)
	groundY := hopperTestPad(t, instance, 100, 100, 3)

	stack, err := item.FromName("minecraft:stone", 7)
	if err != nil {
		t.Fatal(err)
	}
	y := float64(groundY) + 1.5
	for i := 0; i < 10; i++ {
		if instance.spawnItem(world.DimensionOverworld, stack, 100.5, y, 100.5, 0, 0, 0, itemPickupDelayMining) == nil {
			t.Fatal("spawnItem returned nil")
		}
	}

	for i := 0; i < itemMergeIntervalStationary; i++ {
		instance.entityMu.Lock()
		settled := len(instance.items) == 2
		instance.entityMu.Unlock()
		if settled {
			break
		}
		instance.tick()
	}
	assertItemTotals := func(round string) {
		t.Helper()
		instance.entityMu.Lock()
		count := len(instance.items)
		var total int32
		for _, e := range instance.items {
			total += e.Stack.Count
		}
		instance.entityMu.Unlock()
		if count != 2 || total != 70 {
			t.Fatalf("%s：entities = %d, total = %d, want 2/70", round, count, total)
		}
	}
	assertItemTotals("首轮合并后")

	// 再推进一个完整静止周期（40 tick）：满堆叠不再吸收、残留堆叠无可合并对象，
	// 计数必须不变。
	for i := 0; i < itemMergeIntervalStationary; i++ {
		instance.tick()
	}
	assertItemTotals("次轮合并后")
}
