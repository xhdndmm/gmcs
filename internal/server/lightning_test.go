//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"io"
	"math"
	"net"
	"testing"
	"time"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
	"gmcs/internal/world"
)

// TestLightningStrikeEffects 验证落雷的完整效果（原版 LightningBolt）：
// 生成闪电实体、点燃雷击点、对范围内的生物与玩家结算 5 点伤害并点燃、
// 2 tick 后移除闪电实体。
func TestLightningStrikeEffects(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Stormy")
	instance.worldAge.Store(worldNightStart) // 排除日光燃烧干扰
	player := findSession(t, instance, "Stormy")
	if !instance.lightningEnabled {
		t.Fatal("闪电系统未启用")
	}

	w := instance.testWorld()
	spawnX, spawnY, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	baseX, baseY, baseZ := int(math.Floor(spawnX)), int(math.Floor(spawnY)), int(math.Floor(spawnZ))
	flattenTestArea(instance, baseX, baseY, baseZ, 6)

	// 强制雷暴 + 降雨：isRainingAt 依赖下雨标志，落雷判定依赖 isThundering。
	instance.setWeatherParameters(0, 6000, true, true)

	strikeX, strikeZ := baseX+3, baseZ+3
	surface, ok := w.GroundY(strikeX, strikeZ)
	if !ok {
		t.Fatal("无法取得地面高度")
	}
	strikeY := int(math.Floor(surface))

	// 生物与玩家都站在雷击点旁（水平 1 格内）。
	mob := instance.addMob(world.DimensionOverworld, mobZombie, float64(strikeX)+1.6, float64(strikeY), float64(strikeZ)+0.5)
	if mob == nil {
		t.Fatal("无法生成测试生物")
	}
	player.setPlayerPosition(float64(strikeX)+0.5, float64(strikeY), float64(strikeZ)+0.5, 0, 0)
	healthBefore, _, _ := player.healthStatus()

	instance.strikeLightning(strikeX, strikeZ)

	// 1) 客户端收到闪电实体（可能先收到生物生成的 Add Entity，按类型筛选）。
	payload := expectAddEntityType(t, conn, instance.lightningTypeID)
	_, offset, err := protocol.DecodeVarInt(payload)
	if err != nil {
		t.Fatal(err)
	}
	entityID, n, err := protocol.DecodeVarInt(payload[offset:])
	if err != nil {
		t.Fatal(err)
	}
	offset += n
	if entityID <= 0 {
		t.Fatalf("闪电实体 ID = %d", entityID)
	}

	// 2) 雷击点点燃。
	if got := w.BlockAt(strikeX, strikeY, strikeZ); got == world.AirBlock {
		t.Fatal("雷击点未点火")
	}

	// 3) 附近生物受伤并被点燃。
	instance.entityMu.Lock()
	mobHealth, mobFire := mob.Health, mob.FireTicks
	instance.entityMu.Unlock()
	if mobHealth >= 20 {
		t.Fatalf("生物未受伤：health = %v", mobHealth)
	}
	if mobFire <= 0 {
		t.Fatal("生物未被点燃")
	}

	// 4) 玩家受伤并着火。
	healthAfter, _, _ := player.healthStatus()
	if healthAfter >= healthBefore {
		t.Fatalf("玩家未受伤：%v -> %v", healthBefore, healthAfter)
	}
	if player.fireTicks.Load() <= 0 {
		t.Fatal("玩家未被点燃")
	}

	// 5) 2 tick 后闪电实体被移除。
	for i := 0; i < lightningLifetime; i++ {
		instance.tick()
	}
	instance.entityMu.Lock()
	remaining := len(instance.lightningBolts)
	instance.entityMu.Unlock()
	if remaining != 0 {
		t.Fatalf("闪电实体未移除：剩余 %d", remaining)
	}
	if remove := expectPlayPacket(t, conn, protocol.PlayPacketIDRemoveEntities); len(remove) == 0 {
		t.Fatal("未收到 Remove Entities")
	}
}

// TestLightningRequiresRain 验证晴天不落雷（原版 isRainingAt 判定）：
// 雷击仍然会生成实体（视觉），但只有下雨时才点火与结算伤害。
// 这里直接验证“未下雨时 strikeLightning 不产生任何效果”。
func TestLightningRequiresRain(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Dry")
	instance.worldAge.Store(worldNightStart)
	player := findSession(t, instance, "Dry")

	w := instance.testWorld()
	spawnX, spawnY, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	baseX, baseY, baseZ := int(math.Floor(spawnX)), int(math.Floor(spawnY)), int(math.Floor(spawnZ))
	flattenTestArea(instance, baseX, baseY, baseZ, 6)

	// 雷暴但不下雨（原版：没有降雨时 isRainingAt 为 false）。
	instance.setWeatherParameters(0, 6000, false, true)

	strikeX, strikeZ := baseX+2, baseZ-2
	surface, ok := w.GroundY(strikeX, strikeZ)
	if !ok {
		t.Fatal("无法取得地面高度")
	}
	strikeY := int(math.Floor(surface))
	healthBefore, _, _ := player.healthStatus()
	player.setPlayerPosition(float64(strikeX)+0.5, float64(strikeY), float64(strikeZ)+0.5, 0, 0)

	instance.strikeLightning(strikeX, strikeZ)

	if got := w.BlockAt(strikeX, strikeY, strikeZ); got != world.AirBlock {
		t.Fatal("未下雨时不应点火")
	}
	instance.entityMu.Lock()
	bolts := len(instance.lightningBolts)
	instance.entityMu.Unlock()
	if bolts != 0 {
		t.Fatalf("未下雨时不应生成闪电实体：%d", bolts)
	}
	healthAfter, _, _ := player.healthStatus()
	if healthAfter != healthBefore {
		t.Fatalf("未下雨时玩家不应受伤：%v -> %v", healthBefore, healthAfter)
	}
	if player.fireTicks.Load() != 0 {
		t.Fatal("未下雨时玩家不应被点燃")
	}
}

// expectAddEntityType 读取数据包直到收到 entity type 为 wantType 的
// Add Entity（同一帧可能先收到测试生物自己的生成包）。
func expectAddEntityType(t *testing.T, conn net.Conn, wantType int32) []byte {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		packet, err := protocol.ReadPacketWithCompression(conn, compressionThreshold)
		if err != nil {
			t.Fatalf("read packet: %v", err)
		}
		id, offset, err := protocol.DecodeVarInt(packet)
		if err != nil || id != protocol.PlayPacketIDAddEntity {
			continue
		}
		_, n, err := protocol.DecodeVarInt(packet[offset:])
		if err != nil {
			continue
		}
		offset += n + 16 // 实体 ID + UUID
		typeID, _, err := protocol.DecodeVarInt(packet[offset:])
		if err != nil {
			continue
		}
		if typeID == wantType {
			return packet
		}
	}
	t.Fatalf("did not receive add entity with type %d", wantType)
	return nil
}

// benchLightningServer 构造一台带 players 名已加入玩家的服务器（基准用）：
// 写入丢弃，位置固定在出生点附近的地表。
func benchLightningServer(b *testing.B, players int, thundering bool) (*Server, []*session) {
	b.Helper()
	cfg := config.Default()
	cfg.WorldDir = b.TempDir()
	cfg.SpawnMonsters = false
	instance, err := New(cfg)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = instance.worldFor(world.DimensionOverworld).Close() })
	instance.worldAge.Store(worldNightStart)
	if thundering {
		instance.setWeatherParameters(0, 600000, true, true)
	} else {
		instance.setWeatherParameters(6000, 0, false, false)
	}
	spawnX, spawnY, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	sessions := make([]*session, 0, players)
	for i := 0; i < players; i++ {
		sess := &session{
			server:   instance,
			entityID: instance.entityIDs.Add(1),
			health:   maxPlayerHealth,
			food:     maxPlayerFood,
			writer:   io.Discard,
		}
		sess.pose.Store(&playerPose{
			dim: world.DimensionOverworld,
			x:   spawnX + float64(i)*8,
			y:   spawnY,
			z:   spawnZ,
		})
		sess.markJoined()
		sessions = append(sessions, sess)
	}
	return instance, sessions
}

// BenchmarkLightningTickClear 衡量非雷暴时的闪电 Tick 成本
// （只有一个原子读取与雷暴判定，必须接近免费）。
func BenchmarkLightningTickClear(b *testing.B) {
	instance, players := benchLightningServer(b, 4, false)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		instance.tickLightning(players)
	}
}

// BenchmarkLightningTickThunder 衡量雷暴时的闪电 Tick 成本
// （4 名玩家各掷一次 1/100000 的骰子；落雷本身是稀有事件，不计入本基准）。
func BenchmarkLightningTickThunder(b *testing.B) {
	instance, players := benchLightningServer(b, 4, true)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		instance.tickLightning(players)
		// 丢弃可能生成的闪电，避免统计被落雷结算（稀有）污染。
		instance.entityMu.Lock()
		clear(instance.lightningBolts)
		instance.entityMu.Unlock()
	}
}

// TestLightningChancePerTick 验证雷暴期间的落雷频率与原版同量级
// （每 tick 命中概率 = 视距内区块数 / 100000，这里用统计方式验证
// 1 万 tick 内至少落雷一次且不会过于频繁）。
func TestLightningChancePerTick(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Frequency")
	instance.worldAge.Store(worldNightStart)
	player := findSession(t, instance, "Frequency")
	player.setPlayerPosition(0.5, 100, 0.5, 0, 0)

	instance.setWeatherParameters(0, 600000, true, true)
	strikes := 0
	for i := 0; i < 10000; i++ {
		before := instance.entityIDs.Load()
		instance.tickLightning(instance.playerSnapshot())
		if instance.entityIDs.Load() != before {
			strikes++
		}
		// 清掉上一步产生的闪电，避免累积影响后续统计。
		instance.entityMu.Lock()
		instance.lightningBolts = make(map[int32]*lightningBolt)
		instance.entityMu.Unlock()
	}
	if strikes == 0 {
		t.Fatal("雷暴期间 1 万 tick 内没有落雷")
	}
	if strikes > 200 {
		t.Fatalf("落雷过于频繁：1 万 tick 内 %d 次", strikes)
	}
}
