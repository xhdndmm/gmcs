package server

import (
	"math"
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
	"gmcs/internal/world"
)

// 经验球测试：拆分表、拾取发放经验、追踪玩家、死亡掉落。

// TestXPOrbSplitValue 验证经验球拆分表（与原版 getExperienceValue 一致）。
func TestXPOrbSplitValue(t *testing.T) {
	cases := []struct{ amount, want int32 }{
		{1, 1}, {2, 1}, {3, 3}, {5, 3}, {7, 7}, {17, 17}, {36, 17},
		{37, 37}, {149, 149}, {300, 149}, {307, 307}, {2477, 2477}, {100000, 2477},
	}
	for _, tc := range cases {
		if got := xpOrbSplitValue(tc.amount); got != tc.want {
			t.Fatalf("xpOrbSplitValue(%d) = %d, want %d", tc.amount, got, tc.want)
		}
	}
}

// TestXPOrbsPickupAddsExperience 验证经验球拾取：玩家附近生成的经验球在
// 下一 tick 被拾取，并发送 Pickup Item（动画）与 Set Experience 包。
func TestXPOrbsPickupAddsExperience(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "OrbPicker")
	player := findSession(t, instance, "OrbPicker")
	processBarrier(t, conn)

	x, y, z, _, _ := player.playerPosition()
	instance.spawnXPOrbs(player.dimensionID(), x, y+0.5, z, 12)
	instance.entityMu.Lock()
	orbs := len(instance.xpOrbs)
	instance.entityMu.Unlock()
	if orbs == 0 {
		t.Fatal("spawnXPOrbs 未生成经验球")
	}

	instance.tick()

	// 拾取动画（Pickup Item）：先于经验同步发送。
	payload := expectPlayPacket(t, conn, protocol.PlayPacketIDCollect)
	if _, _, err := protocol.DecodeVarInt(payload); err != nil {
		t.Fatal(err)
	}
	// 经验同步：总经验 = 12。
	payload = expectPlayPacket(t, conn, protocol.PlayPacketIDSetExperience)
	_, offset, err := protocol.DecodeVarInt(payload) // 包 ID
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
	if total != 12 || level != 1 {
		t.Fatalf("经验 total/level = %d/%d, want 12/1", total, level)
	}
	// 结算后经验球被移除。
	instance.entityMu.Lock()
	remaining := 0
	for _, orb := range instance.xpOrbs {
		remaining += int(orb.Count)
	}
	instance.entityMu.Unlock()
	if remaining != 0 {
		t.Fatalf("经验球拾取后残留 %d 个", remaining)
	}
}

// TestXPOrbFollowsPlayer 验证经验球追踪：8 格内生成的经验球会飞向玩家并被拾取。
func TestXPOrbFollowsPlayer(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "OrbMagnet")
	player := findSession(t, instance, "OrbMagnet")
	processBarrier(t, conn)

	x, y, z, _, _ := player.playerPosition()
	// 距离玩家 5 格（拾取盒之外，8 格追踪范围内）。
	startX, startZ := x+5, z
	instance.spawnXPOrbs(player.dimensionID(), startX, y+1, startZ, 3)

	instance.entityMu.Lock()
	var orb *xpOrb
	for _, candidate := range instance.xpOrbs {
		orb = candidate
	}
	instance.entityMu.Unlock()
	if orb == nil {
		t.Fatal("经验球未生成")
	}
	dx := orb.X - x
	dz := orb.Z - z
	startDistance := math.Hypot(dx, dz)

	// 追踪若干 tick：经验球应向玩家靠近。
	for i := 0; i < 20; i++ {
		instance.tick()
		instance.entityMu.Lock()
		moved := false
		if current, ok := instance.xpOrbs[orb.ID]; ok {
			moved = math.Hypot(current.X-x, current.Z-z) < startDistance
		}
		instance.entityMu.Unlock()
		if !moved {
			break // 已被拾取（移除）
		}
	}

	// 最终应被拾取并发放经验。
	waitFor(t, func() bool {
		instance.tick()
		instance.entityMu.Lock()
		defer instance.entityMu.Unlock()
		return len(instance.xpOrbs) == 0
	}, "经验球应被玩家拾取")
	if _, _, total := player.experienceStatus(); total != 3 {
		t.Fatalf("追踪拾取后总经验 = %d, want 3", total)
	}
}

// TestPlayerDeathDropsExperienceOrbs 验证玩家死亡掉落经验球：
// 掉落量 = min(等级 × 7, 100)。
func TestPlayerDeathDropsExperienceOrbs(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "OrbVictim")
	player := findSession(t, instance, "OrbVictim")

	// 120 点总经验 → 8 级 → 掉落 min(8×7, 100) = 56。
	player.addExperience(120)
	if _, level, _ := player.experienceStatus(); level != 8 {
		t.Fatalf("120 经验对应等级 = %d, want 8", level)
	}
	if !instance.damagePlayer(player, maxPlayerHealth, "Zombie", -1, instance.mobAttackDamageTypeID, nil) {
		t.Fatal("致命伤害未生效")
	}
	instance.entityMu.Lock()
	total := int32(0)
	for _, orb := range instance.xpOrbs {
		total += orb.Value * orb.Count
	}
	instance.entityMu.Unlock()
	if total != 56 {
		t.Fatalf("死亡掉落经验 = %d, want 56", total)
	}
}

// TestXPOrbMerge 验证同值经验球合并（同组、0.5 格内）：
// 合并后计数相加、实体只剩一个。
func TestXPOrbMerge(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "OrbMerge")
	player := findSession(t, instance, "OrbMerge")
	x, y, z, _, _ := player.playerPosition()

	// 在玩家拾取范围之外生成两个同值经验球（同一位置 → 同组概率以外，
	// 直接构造实体以固定 ID 分组）。
	instance.entityMu.Lock()
	first := &xpOrb{ID: 41, Dim: world.DimensionOverworld, Value: 7, Count: 1, X: x + 20, Y: y + 1, Z: z}
	second := &xpOrb{ID: 81, Dim: world.DimensionOverworld, Value: 7, Count: 1, X: x + 20.1, Y: y + 1, Z: z}
	instance.xpOrbs[first.ID] = first
	instance.xpOrbs[second.ID] = second
	instance.entityMu.Unlock()

	// 合并扫描发生在年龄 % 20 == 1（首 tick 即满足）。
	instance.tick()
	instance.entityMu.Lock()
	remaining := len(instance.xpOrbs)
	mergedCount := int32(0)
	for _, orb := range instance.xpOrbs {
		mergedCount = orb.Count
	}
	instance.entityMu.Unlock()
	if remaining != 1 {
		t.Fatalf("同值经验球未合并：remaining = %d", remaining)
	}
	if mergedCount != 2 {
		t.Fatalf("合并后计数 = %d, want 2", mergedCount)
	}
}
