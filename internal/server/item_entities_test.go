package server

import (
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"
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
	// 合并为周期检查（每 4 tick）：推进到合并帧后应只剩一个实体。
	for i := 0; i < itemMergeIntervalTicks; i++ {
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
	if instance.spawnItem(stone, x, y, z, 0, 0, 0, 0) == nil {
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
	instance.spawnItem(first, x, y, z, 0, 0, 0, itemPickupDelayPlayer)
	expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)
	expectPlayPacket(t, conn, protocol.PlayPacketIDEntityMetadata)
	instance.spawnItem(second, x, y, z, 0, 0, 0, itemPickupDelayPlayer)
	expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)
	expectPlayPacket(t, conn, protocol.PlayPacketIDEntityMetadata)

	// 周期合并（每 4 tick）：合并为 64 + 6。
	for i := 0; i < itemMergeIntervalTicks; i++ {
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
	e := instance.spawnItem(stack, x, y+3, z, 0, 0, 0, 0)
	expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)
	expectPlayPacket(t, conn, protocol.PlayPacketIDEntityMetadata)
	instance.entityMu.Lock()
	e.AgeTicks = itemDespawnTicks - 1
	instance.entityMu.Unlock()
	instance.tick()
	expectPlayPacket(t, conn, protocol.PlayPacketIDRemoveEntities)

	// 掉入虚空：位置低于世界底部，直接移除。
	if instance.spawnItem(stack, x, float64(-96), z, 0, -1, 0, 0) == nil {
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
