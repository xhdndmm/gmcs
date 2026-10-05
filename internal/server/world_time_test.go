package server

import (
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"
)

// TestWorldTimeBroadcast 验证世界时间推进与 Update Time 同步包。
func TestWorldTimeBroadcast(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Watcher")

	// 推进 20 tick（一个同步周期）：应收到 Update Time。
	for i := 0; i < worldTimeBroadcastInterval; i++ {
		instance.tick()
	}
	payload := expectPlayPacket(t, conn, protocol.PlayPacketIDUpdateTime)
	_, offset, err := protocol.DecodeVarInt(payload) // 跳过包 ID
	if err != nil {
		t.Fatal(err)
	}
	age, offset, err := protocol.DecodeInt64(payload, offset)
	if err != nil {
		t.Fatal(err)
	}
	dayTime, _, err := protocol.DecodeInt64(payload, offset)
	if err != nil {
		t.Fatal(err)
	}
	if age != worldTimeBroadcastInterval {
		t.Fatalf("world age = %d, want %d", age, worldTimeBroadcastInterval)
	}
	if dayTime != age%worldDayLength {
		t.Fatalf("day time = %d, want %d", dayTime, age%worldDayLength)
	}
	if instance.isNight() {
		t.Fatal("world should not start at night")
	}
}

// TestMonstersOnlySpawnAtNight 验证敌对生物只在夜晚生成。
func TestMonstersOnlySpawnAtNight(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = true
	cfg.MaxMobs = 4
	instance, _ := joinServer(t, cfg, "DayWalker")
	players := instance.playerSnapshot()

	// 白天：不生成。
	for i := 0; i < 50; i++ {
		instance.trySpawnMob(players)
	}
	instance.entityMu.Lock()
	count := len(instance.mobs)
	instance.entityMu.Unlock()
	if count != 0 {
		t.Fatalf("mobs spawned during the day: %d", count)
	}

	// 夜晚：可以生成。
	instance.worldAge.Store(worldNightStart)
	spawned := false
	for i := 0; i < 50 && !spawned; i++ {
		instance.trySpawnMob(players)
		instance.entityMu.Lock()
		spawned = len(instance.mobs) > 0
		instance.entityMu.Unlock()
	}
	if !spawned {
		t.Fatal("no mob spawned at night")
	}
}

// TestMobKillRewardsExperience 验证击杀生物获得经验（Set Experience 同步）
// 并可能掉落战利品。
func TestMobKillRewardsExperience(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Hunter")
	player := findSession(t, instance, "Hunter")
	x, y, z, _, _ := player.playerPosition()

	// 在玩家面前生成一只僵尸，并把生命降到一次攻击即可击杀。
	mob := instance.addMob(x+1, y, z)
	expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)
	instance.entityMu.Lock()
	mob.Health = playerAttackDamage
	instance.entityMu.Unlock()

	sendAttack(t, conn, mob.ID)
	// 读循环异步处理攻击：轮询等待死亡。
	waitFor(t, func() bool {
		instance.entityMu.Lock()
		defer instance.entityMu.Unlock()
		return mob.Dead
	}, "mob should be dead")

	// 经验条：总经验 = 僵尸经验值。
	payload := expectPlayPacket(t, conn, protocol.PlayPacketIDSetExperience)
	_, offset, err := protocol.DecodeVarInt(payload) // 跳过包 ID
	if err != nil {
		t.Fatal(err)
	}
	if _, offset, err = protocol.DecodeFloat32(payload, offset); err != nil { // 经验条
		t.Fatal(err)
	}
	level, size, err := protocol.DecodeVarInt(payload[offset:])
	if err != nil {
		t.Fatal(err)
	}
	offset += size
	total, _, err := protocol.DecodeVarInt(payload[offset:])
	if err != nil {
		t.Fatal(err)
	}
	if total != zombieExperience || level != 0 {
		t.Fatalf("experience total/level = %d/%d, want %d/0", total, level, zombieExperience)
	}

	// 掉落物：腐肉数量 0–2（外加可能的稀有掉落）。
	instance.entityMu.Lock()
	drops := len(instance.items)
	instance.entityMu.Unlock()
	if drops > 3 {
		t.Fatalf("unexpected drop count: %d", drops)
	}
}

// TestPerItemStackLimit 验证按物品数据的堆叠上限：
// 雪球上限 16（64 个会占用 4 个槽位），工具上限 1。
func TestPerItemStackLimit(t *testing.T) {
	snowball, err := item.FromName("minecraft:snowball", 64)
	if err != nil {
		t.Fatal(err)
	}
	if snowball.MaxStack() != 16 {
		t.Fatalf("snowball max stack = %d, want 16", snowball.MaxStack())
	}
	var inventory item.Inventory
	remaining, changed := inventory.Add(snowball)
	if remaining != 0 {
		t.Fatalf("remaining = %d, want 0", remaining)
	}
	if len(changed) != 4 {
		t.Fatalf("changed slots = %v, want 4 slots", changed)
	}
	for _, slot := range changed {
		if stack := inventory.Get(slot); stack.Count != 16 {
			t.Fatalf("slot %d count = %d, want 16", slot, stack.Count)
		}
	}

	// 工具（不可堆叠）：每个槽位最多 1 个。
	sword, err := item.FromName("minecraft:diamond_sword", 3)
	if err != nil {
		t.Fatal(err)
	}
	if sword.MaxStack() != 1 {
		t.Fatalf("diamond sword max stack = %d, want 1", sword.MaxStack())
	}
	remaining, changed = inventory.Add(sword)
	if remaining != 0 || len(changed) != 3 {
		t.Fatalf("sword add: remaining=%d changed=%v, want 0 and 3 slots", remaining, changed)
	}
	for _, slot := range changed {
		if stack := inventory.Get(slot); stack.Count != 1 {
			t.Fatalf("sword slot %d count = %d, want 1", slot, stack.Count)
		}
	}
}
