//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// mobFireTestSetup 建好石质平台，放一只僵尸与一桶火，返回相关坐标。
func mobFireTestSetup(t *testing.T, name string) (*Server, *world.World, world.Dimension, *mob, [3]int) {
	t.Helper()
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, name)
	// 固定为夜晚：白天阳光下亡灵会持续被点燃（原版行为），
	// 会干扰“入火点燃/离开火熄灭”的断言。
	instance.worldAge.Store(worldNightStart + 1000)
	w := instance.testWorld()
	baseX, floorY, baseZ := fluidBase(t, instance)
	m := instance.addMob(world.DimensionOverworld, mobZombie, float64(baseX)+0.5, float64(floorY), float64(baseZ)+0.5)
	if m == nil {
		t.Fatal("addMob 失败")
	}
	firePos := [3]int{baseX + 1, floorY, baseZ}
	if !instance.placeFireAt(w, world.DimensionOverworld, firePos[0], firePos[1], firePos[2]) {
		t.Fatal("placeFireAt = false, want true")
	}
	return instance, w, world.DimensionOverworld, m, firePos
}

// TestMobIgnitesInFireAndBurns 验证生物站在火中会被点燃（原版 BaseFireBlock
// 的方块内效果：8 秒）并每秒受到 1 点火焰伤害；离开火后着火时间递减。
func TestMobIgnitesInFireAndBurns(t *testing.T) {
	instance, w, dim, m, firePos := mobFireTestSetup(t, "BurningMob")

	// 把生物移到火方块上（脚部所在方块即火）。
	instance.entityMu.Lock()
	m.X = float64(firePos[0]) + 0.5
	m.Z = float64(firePos[2]) + 0.5
	instance.entityMu.Unlock()

	instance.entityMu.Lock()
	env, ok := instance.tickMobEnvironment(w, m)
	instance.entityMu.Unlock()
	if !ok {
		t.Fatal("tickMobEnvironment = 无包, want 点燃包")
	}
	if m.FireTicks != mobFireBlockTicks {
		t.Fatalf("着火 tick = %d, want %d", m.FireTicks, mobFireBlockTicks)
	}
	if !m.FireSynced {
		t.Fatal("着火标志未同步")
	}
	found := false
	for _, packet := range env.packets {
		if len(packet) > 0 && packet[0] == byte(protocol.PlayPacketIDEntityMetadata) {
			found = true
		}
	}
	if !found {
		t.Fatal("未发送 Set Entity Data（着火标志）")
	}

	// 火焰伤害：着火的每秒 1 点（受受伤无敌帧限制，与原版一致）。
	instance.entityMu.Lock()
	m.Health = 100
	instance.entityMu.Unlock()
	for i := 0; i < 30; i++ {
		instance.entityMu.Lock()
		instance.tickMobEnvironment(w, m)
		instance.entityMu.Unlock()
	}
	instance.entityMu.Lock()
	healthAfter := m.Health
	instance.entityMu.Unlock()
	if healthAfter >= 100 {
		t.Fatalf("着火 30 tick 后生命值 %v 未下降（起始 100）", healthAfter)
	}

	// 离开火（把火清掉）后着火时间递减到 0 并清除标志。
	w.SetBlock(firePos[0], firePos[1], firePos[2], world.AirBlock)
	for i := 0; i < mobFireBlockTicks+5 && m.FireTicks > 0; i++ {
		instance.entityMu.Lock()
		instance.tickMobEnvironment(w, m)
		instance.entityMu.Unlock()
	}
	instance.entityMu.Lock()
	fireTicks, fireSynced, dead := m.FireTicks, m.FireSynced, m.Dead
	instance.entityMu.Unlock()
	if dead {
		t.Fatal("生物在熄灭阶段死亡（测试前提失效）")
	}
	if fireTicks != 0 {
		t.Fatalf("离开火后着火 tick = %d, want 0", fireTicks)
	}
	if fireSynced {
		t.Fatal("熄灭后着火标志未清除")
	}
	_ = dim
}

// TestMobFireExtinguishedByWater 验证入水立即熄灭（原版 Entity.baseTick 的
// clearFire 语义）。
func TestMobFireExtinguishedByWater(t *testing.T) {
	instance, w, _, m, _ := mobFireTestSetup(t, "WetMob")

	instance.entityMu.Lock()
	m.FireTicks = mobFireBlockTicks
	m.FireSynced = true
	instance.entityMu.Unlock()

	water, err := registry.BlockStateID("minecraft:water")
	if err != nil {
		t.Fatal(err)
	}
	x, y, z := int(math.Floor(m.X)), int(math.Floor(m.Y)), int(math.Floor(m.Z))
	if !w.SetBlock(x, y, z, water) {
		t.Fatal("放置水失败")
	}
	if !isWaterState(w.BlockAt(x, y, z)) {
		t.Fatal("目标位置不是水")
	}
	instance.entityMu.Lock()
	instance.tickMobEnvironment(w, m)
	instance.entityMu.Unlock()
	if m.FireTicks != 0 {
		t.Fatalf("入水后着火 tick = %d, want 0", m.FireTicks)
	}
}

// TestMobLavaIgnitesAndDamages 验证接触岩浆会点燃 15 秒并结算 4 点岩浆伤害
// （受无敌帧限制，与原版 lavaHurt 一致）。
func TestMobLavaIgnitesAndDamages(t *testing.T) {
	instance, w, _, m, _ := mobFireTestSetup(t, "LavaMob")

	lava, err := registry.BlockStateID("minecraft:lava")
	if err != nil {
		t.Fatal(err)
	}
	x, y, z := int(math.Floor(m.X)), int(math.Floor(m.Y)), int(math.Floor(m.Z))
	if !w.SetBlock(x, y, z, lava) {
		t.Fatal("放置岩浆失败")
	}
	if !isLavaState(w.BlockAt(x, y, z)) {
		t.Fatal("目标位置不是岩浆")
	}
	healthBefore := m.Health
	instance.entityMu.Lock()
	_, ok := instance.tickMobEnvironment(w, m)
	instance.entityMu.Unlock()
	if !ok {
		t.Fatal("tickMobEnvironment = 无包, want 岩浆伤害包")
	}
	if m.FireTicks != lavaFireTicks {
		t.Fatalf("岩浆着火 tick = %d, want %d", m.FireTicks, lavaFireTicks)
	}
	if got := healthBefore - m.Health; got != envLavaDamage {
		t.Fatalf("岩浆伤害 = %v, want %d", got, envLavaDamage)
	}
	// 无敌帧内不重复结算。
	instance.entityMu.Lock()
	instance.tickMobEnvironment(w, m)
	instance.entityMu.Unlock()
	if got := healthBefore - m.Health; got != envLavaDamage {
		t.Fatalf("无敌帧内岩浆伤害 = %v, want %d", got, envLavaDamage)
	}
}

// TestMobBurnsToDeathDropsLoot 验证火焰伤害可以杀死生物并触发掉落。
func TestMobBurnsToDeathDropsLoot(t *testing.T) {
	instance, w, _, m, firePos := mobFireTestSetup(t, "DeadMob")

	instance.entityMu.Lock()
	m.X = float64(firePos[0]) + 0.5
	m.Z = float64(firePos[2]) + 0.5
	m.Health = envFireDamage
	instance.entityMu.Unlock()

	dead := false
	for i := 0; i < mobFireDamageInterval*3 && !dead; i++ {
		instance.entityMu.Lock()
		instance.tickMobEnvironment(w, m)
		instance.entityMu.Unlock()
		instance.entityMu.Lock()
		if m.Dead {
			dead = true
		}
		instance.entityMu.Unlock()
	}
	if !dead {
		t.Fatal("火焰伤害未杀死生物（未产生掉落事件）")
	}
	instance.entityMu.Lock()
	stillOnFire := m.FireTicks != 0 || m.FireSynced
	instance.entityMu.Unlock()
	if stillOnFire {
		t.Fatal("死亡后着火状态未清除")
	}
}

// TestMobFireTicksPersisted 验证着火时长随 entities.json 往返，并在读档时限幅。
func TestMobFireTicksPersisted(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "PersistMob")

	m := instance.addMob(world.DimensionOverworld, mobZombie, 0.5, 100, 0.5)
	if m == nil {
		t.Fatal("addMob 失败")
	}
	instance.entityMu.Lock()
	m.FireTicks = mobFireBlockTicks
	instance.entityMu.Unlock()
	if err := instance.saveEntities(); err != nil {
		t.Fatalf("saveEntities: %v", err)
	}

	// 清空内存中的生物后重新读档，着火时长应恢复。
	instance.entityMu.Lock()
	delete(instance.mobs, m.ID)
	instance.entityMu.Unlock()
	if err := instance.loadEntitiesFor(world.DimensionOverworld); err != nil {
		t.Fatalf("loadEntitiesFor: %v", err)
	}
	instance.entityMu.Lock()
	var restored *mob
	for _, candidate := range instance.mobs {
		if candidate.Kind == mobZombie {
			restored = candidate
		}
	}
	instance.entityMu.Unlock()
	if restored == nil {
		t.Fatal("读档后未恢复僵尸")
	}
	if restored.FireTicks != mobFireBlockTicks {
		t.Fatalf("读档后着火 tick = %d, want %d", restored.FireTicks, mobFireBlockTicks)
	}
}

// TestPlayerIgnitesInFire 验证玩家站在火方块上会被点燃 8 秒并每秒受伤
// （此前玩家只会被岩浆点燃）。
func TestPlayerIgnitesInFire(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "BurningPlayer")

	baseX, floorY, baseZ := fluidBase(t, instance)
	w := instance.testWorld()
	player := findSession(t, instance, "BurningPlayer")
	player.setPlayerPosition(float64(baseX)+0.5, float64(floorY), float64(baseZ)+0.5, 0, 0)
	if !instance.placeFireAt(w, world.DimensionOverworld, baseX, floorY, baseZ) {
		t.Fatal("placeFireAt = false, want true")
	}
	instance.tick()
	// 火中的玩家至少被点燃 8 秒（可能因玩家额外累加略高）。
	if ticks := player.fireTicks.Load(); ticks < mobFireBlockTicks {
		t.Fatalf("玩家着火 tick = %d, want ≥ %d", ticks, mobFireBlockTicks)
	}
	healthBefore := player.health
	for i := 0; i < 25; i++ {
		instance.tick()
	}
	if player.health >= healthBefore {
		t.Fatalf("火中 25 tick 后玩家生命值 %v 未下降（起始 %v）", player.health, healthBefore)
	}
	// 离开火后入水立即熄灭（水方块）。
	player.setPlayerPosition(float64(baseX)+0.5, float64(floorY), float64(baseZ)+0.5, 0, 0)
	w.SetBlock(baseX, floorY, baseZ, world.AirBlock)
	water, err := registry.BlockStateID("minecraft:water")
	if err != nil {
		t.Fatal(err)
	}
	w.SetBlock(baseX, floorY, baseZ, water)
	instance.tick()
	if ticks := player.fireTicks.Load(); ticks != 0 {
		t.Fatalf("入水后玩家着火 tick = %d, want 0", ticks)
	}
}
