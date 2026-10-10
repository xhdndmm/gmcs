package server

import (
	"math"
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
	"gmcs/internal/world"
)

// clearCorridor 在玩家附近整平一条通道（floorY 铺石头，上方 3 格空气），
// 保证生物 AI 的视线/行走判定在测试中确定。
func clearCorridor(instance *Server, baseX, floorY, baseZ, halfWidth, length int) {
	for dx := -halfWidth; dx <= halfWidth; dx++ {
		for dz := -1; dz <= length; dz++ {
			instance.testWorld().SetBlock(baseX+dx, floorY, baseZ+dz, world.StoneBlock)
			for dy := 1; dy <= 4; dy++ {
				instance.testWorld().SetBlock(baseX+dx, floorY+dy, baseZ+dz, world.AirBlock)
			}
		}
	}
}

// TestSkeletonFiresArrowAtPlayer 验证骷髅在射程内站定射击：生成箭矢实体
// （Add Entity + 射击音效），箭矢飞行命中玩家并结算伤害。
func TestSkeletonFiresArrowAtPlayer(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Arrowed")
	player := findSession(t, instance, "Arrowed")
	if !instance.arrowsEnabled {
		t.Fatal("arrows disabled: registry data missing")
	}

	spawnX, spawnY, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	baseX, baseZ := int(math.Floor(spawnX)), int(math.Floor(spawnZ))
	floorY := int(math.Floor(spawnY)) - 1
	clearCorridor(instance, baseX, floorY, baseZ, 1, 8)

	skeleton := instance.addMob(world.DimensionOverworld, mobSkeleton, spawnX, spawnY, spawnZ+6)
	expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)

	// 就绪后第一帧立即射击。
	instance.entityMu.Lock()
	skeleton.ShootCooldown = 0
	instance.entityMu.Unlock()
	instance.tick()

	// 箭矢生成（Add Entity）与射击音效（Entity Sound Effect）。
	expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)
	expectPlayPacket(t, conn, protocol.PlayPacketIDEntitySoundEffect)
	instance.entityMu.Lock()
	arrowCount := len(instance.arrows)
	instance.entityMu.Unlock()
	if arrowCount != 1 {
		t.Fatalf("arrows after first shot = %d, want 1", arrowCount)
	}

	// 箭矢飞行（约 4 tick 命中）：命中后播放受伤动画 + 伤害事件。
	damaged := false
	for i := 0; i < 40; i++ {
		instance.tick()
		if health, _, _ := player.healthStatus(); health < maxPlayerHealth {
			damaged = true
			break
		}
	}
	if !damaged {
		t.Fatal("arrow never hit the player in range")
	}
	expectPlayPacket(t, conn, protocol.PlayPacketIDHurtAnimation)
	expectPlayPacket(t, conn, protocol.PlayPacketIDDamageEvent)
	health, _, _ := player.healthStatus()
	if want := float32(maxPlayerHealth - skeletonArrowDamage); math.Abs(float64(health-want)) > 0.01 {
		t.Fatalf("player health after arrow = %v, want %v", health, want)
	}
	// 命中的箭矢从飞行列表移除。
	instance.entityMu.Lock()
	remainingArrows := len(instance.arrows)
	instance.entityMu.Unlock()
	if remainingArrows != 0 {
		t.Fatalf("arrows after hit = %d, want 0", remainingArrows)
	}
}

// TestCreeperExplodesNearPlayer 验证苦力怕引信结束后的爆炸：销毁方块、
// 移除苦力怕、广播爆炸包并结算玩家伤害。
func TestCreeperExplodesNearPlayer(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Boomed")
	player := findSession(t, instance, "Boomed")

	spawnX, spawnY, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	baseX, baseZ := int(math.Floor(spawnX)), int(math.Floor(spawnZ))
	floorY := int(math.Floor(spawnY)) - 1
	// 清理爆心周围 7×7 区域，保证爆炸影响范围可预测。
	for dx := -3; dx <= 3; dx++ {
		for dz := -3; dz <= 3; dz++ {
			instance.testWorld().SetBlock(baseX+dx, floorY, baseZ+dz, world.StoneBlock)
			for dy := 1; dy <= 4; dy++ {
				instance.testWorld().SetBlock(baseX+dx, floorY+dy, baseZ+dz, world.AirBlock)
			}
		}
	}

	creeper := instance.addMob(world.DimensionOverworld, mobCreeper, spawnX, spawnY, spawnZ+2)
	expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)

	// 引信只剩 1 tick：下一帧爆炸。
	instance.entityMu.Lock()
	creeper.Fuse = creeperFuseTicks - 1
	instance.entityMu.Unlock()
	instance.tick()

	// 苦力怕实体已移除。
	instance.entityMu.Lock()
	mobCount := len(instance.mobs)
	instance.entityMu.Unlock()
	if mobCount != 0 {
		t.Fatalf("mobs after explosion = %d, want 0 (creeper removed)", mobCount)
	}
	// 爆心下方的石头被炸毁。
	if state := instance.testWorld().BlockAt(baseX, floorY, baseZ+2); state != world.AirBlock {
		t.Fatalf("block below the explosion = %d, want air", state)
	}
	// 客户端收到爆炸包。
	expectPlayPacket(t, conn, protocol.PlayPacketIDExplosion)
	// 玩家受伤：爆心 2 格外、伤害半径 6 格 → 24*(1-2/6)=16 点。
	health, _, _ := player.healthStatus()
	if want := float32(maxPlayerHealth - 16); math.Abs(float64(health-want)) > 0.5 {
		t.Fatalf("player health after explosion = %v, want ~%v", health, want)
	}
}

// TestSpiderMeleeAttack 验证蜘蛛近战：抬手后命中玩家并造成 2 点伤害。
func TestSpiderMeleeAttack(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Spidered")
	player := findSession(t, instance, "Spidered")

	spawnX, spawnY, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	baseX, baseZ := int(math.Floor(spawnX)), int(math.Floor(spawnZ))
	floorY := int(math.Floor(spawnY)) - 1
	clearCorridor(instance, baseX, floorY, baseZ, 1, 4)

	instance.addMob(world.DimensionOverworld, mobSpider, spawnX, spawnY, spawnZ+1)
	expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)

	// 抬手期间不造成伤害。
	instance.tick()
	if health, _, _ := player.healthStatus(); health != maxPlayerHealth {
		t.Fatalf("player damaged during windup: %v", health)
	}
	for i := 0; i < mobAttackWindupTicks+2; i++ {
		instance.tick()
	}
	expectPlayPacket(t, conn, protocol.PlayPacketIDHurtAnimation)
	expectPlayPacket(t, conn, protocol.PlayPacketIDDamageEvent)
	health, _, _ := player.healthStatus()
	if want := float32(maxPlayerHealth - mobKinds[mobSpider].damage); health != want {
		t.Fatalf("player health after spider hit = %v, want %v", health, want)
	}
}

// TestMobSpawnKindWeights 验证按权重挑选生成种类：全部返回值合法，
// 且抽样的权重顺序符合表定义（僵尸权重最高）。
func TestMobSpawnKindWeights(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	instance, _ := joinServer(t, cfg, "Weigher")

	counts := map[mobKind]int{}
	for i := 0; i < 400; i++ {
		kind, ok := instance.pickSpawnKind()
		if !ok {
			t.Fatalf("pickSpawnKind returned false (registry data missing)")
		}
		if _, exists := mobKinds[kind]; !exists {
			t.Fatalf("pickSpawnKind returned unknown kind %d", kind)
		}
		counts[kind]++
	}
	if counts[mobZombie] == 0 {
		t.Fatalf("zombie never picked in 400 draws: %v", counts)
	}
	if counts[mobZombie] <= counts[mobSpider] {
		t.Fatalf("expected zombie (weight 40) more common than spider (weight 10): %v", counts)
	}
}

// TestMobKindsTableValid 验证全部生物种类的基础数值配置完整。
func TestMobKindsTableValid(t *testing.T) {
	for kind, stats := range mobKinds {
		if stats.name == "" || stats.health <= 0 || stats.walkSpeed <= 0 ||
			stats.followRange <= 0 || stats.spawnWeight <= 0 ||
			stats.hurtSound == "" || stats.deathSound == "" {
			t.Errorf("mobKinds[%d] invalid: %+v", kind, stats)
		}
		if stats.damage <= 0 && kind != mobCreeper {
			// 苦力怕是自爆伤害（damage=0 是预期值）。
			t.Errorf("mobKinds[%d].damage = %v", kind, stats.damage)
		}
	}
	if len(mobKinds) != 4 {
		t.Fatalf("expected 4 mob kinds, got %d", len(mobKinds))
	}
}
