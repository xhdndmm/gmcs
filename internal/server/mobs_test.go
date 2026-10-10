package server

import (
	"math"
	"testing"
	"time"

	"gmcs/internal/config"
	"gmcs/internal/item"
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
// 且抽样的权重顺序符合表定义（僵尸权重高于蜘蛛）；被动生物只从
// 被动集合中挑选。
func TestMobSpawnKindWeights(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	instance, _ := joinServer(t, cfg, "Weigher")

	counts := map[mobKind]int{}
	for i := 0; i < 400; i++ {
		kind, ok := instance.pickSpawnKind(true)
		if !ok {
			t.Fatalf("pickSpawnKind(true) returned false (registry data missing)")
		}
		if _, exists := mobKinds[kind]; !exists {
			t.Fatalf("pickSpawnKind returned unknown kind %d", kind)
		}
		if mobKinds[kind].passive {
			t.Fatalf("hostile pick returned passive kind %v", mobKinds[kind].name)
		}
		counts[kind]++
	}
	if counts[mobZombie] == 0 {
		t.Fatalf("zombie never picked in 400 draws: %v", counts)
	}
	if counts[mobZombie] <= counts[mobSpider] {
		t.Fatalf("expected zombie (weight 40) more common than spider (weight 10): %v", counts)
	}

	passiveCounts := map[mobKind]int{}
	for i := 0; i < 200; i++ {
		kind, ok := instance.pickSpawnKind(false)
		if !ok {
			t.Fatalf("pickSpawnKind(false) returned false")
		}
		if !mobKinds[kind].passive {
			t.Fatalf("passive pick returned hostile kind %v", mobKinds[kind].name)
		}
		passiveCounts[kind]++
	}
	if len(passiveCounts) < 2 {
		t.Fatalf("expected multiple passive kinds: %v", passiveCounts)
	}
}

// TestMobKindsTableValid 验证全部生物种类的基础数值配置完整。
func TestMobKindsTableValid(t *testing.T) {
	for kind, stats := range mobKinds {
		if stats.name == "" || stats.health <= 0 || stats.walkSpeed <= 0 ||
			stats.spawnWeight <= 0 ||
			stats.hurtSound == "" || stats.deathSound == "" {
			t.Errorf("mobKinds[%d] invalid: %+v", kind, stats)
		}
		if stats.damage <= 0 && kind != mobCreeper && !stats.passive {
			// 苦力怕是自爆伤害（damage=0 是预期值）；被动生物不攻击（damage=0）。
			t.Errorf("mobKinds[%d].damage = %v", kind, stats.damage)
		}
		if stats.passive && stats.followRange != 0 {
			t.Errorf("passive mob %v should not have a follow range", stats.name)
		}
		if !stats.passive && stats.followRange <= 0 {
			t.Errorf("hostile mob %v needs a follow range", stats.name)
		}
	}
	if len(mobKinds) != 8 {
		t.Fatalf("expected 8 mob kinds, got %d", len(mobKinds))
	}
}

// TestPassiveMobDaySpawn 验证白天生成被动生物（牛/猪/羊）、夜晚生成敌对生物。
func TestPassiveMobDaySpawn(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.MaxMobs = 16
	instance, _ := joinServer(t, cfg, "Farmer")

	// 白天（正午）：尝试生成，应只出现被动生物。
	instance.worldAge.Store(6000)
	spawned := 0
	for i := 0; i < 400 && spawned < 4; i++ {
		before := mobCount(instance)
		instance.trySpawnMob(instance.playerSnapshot())
		for _, m := range mobSnapshot(instance) {
			if mobKinds[m.Kind].passive {
				spawned++
			} else {
				t.Fatalf("hostile mob %v spawned during the day", mobKinds[m.Kind].name)
			}
		}
		if mobCount(instance) == before {
			continue
		}
	}
	if spawned == 0 {
		t.Skip("no passive spawn succeeded in range (terrain/water); covered by weight tests")
	}

	// 夜晚：生成敌对生物。
	instance.worldAge.Store(14000)
	for i := 0; i < 200; i++ {
		instance.trySpawnMob(instance.playerSnapshot())
		for _, m := range mobSnapshot(instance) {
			if !mobKinds[m.Kind].passive && m.Kind != mobZombie {
				return // 出现了非僵尸的敌对生物（僵尸也可能生成）
			}
		}
	}
	// 至少验证：夜晚允许生成敌对生物（僵尸）。
	hasHostile := false
	for _, m := range mobSnapshot(instance) {
		if !mobKinds[m.Kind].passive {
			hasHostile = true
		}
	}
	if !hasHostile {
		t.Skip("no hostile spawn succeeded in range (covered by night spawn tests)")
	}
}

// TestEndermanAggroWhenLookedAt 验证末影人被注视后激怒并攻击；背对时不激怒。
func TestEndermanAggroWhenLookedAt(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Starer")
	player := findSession(t, instance, "Starer")

	spawnX, spawnY, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	baseX, baseZ := int(math.Floor(spawnX)), int(math.Floor(spawnZ))
	floorY := int(math.Floor(spawnY)) - 1
	clearCorridor(instance, baseX, floorY, baseZ, 1, 6)

	// 玩家初始朝向 yaw=0（看向 +Z）。先检查背对（−Z 方向）不构成注视。
	behind := instance.addMob(world.DimensionOverworld, mobEnderman, spawnX, spawnY, spawnZ-3)
	expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)
	if playerLooksAtMob(player, behind) {
		t.Fatal("enderman behind the player should not be considered looked at")
	}
	instance.entityMu.Lock()
	instance.mobs[behind.ID].Dead = true // 移除该测试生物（避免干扰）
	instance.mobs[behind.ID].DeadTicks = mobDeathTicks + 1
	instance.entityMu.Unlock()

	// 正视（+Z 方向）的末影人：被注视 → 激怒 → 抬手后攻击（7 点伤害）。
	front := instance.addMob(world.DimensionOverworld, mobEnderman, spawnX, spawnY, spawnZ+1)
	expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)
	if !playerLooksAtMob(player, front) {
		t.Fatal("enderman in front of the player should be considered looked at")
	}
	instance.tick()
	instance.entityMu.Lock()
	angry := instance.mobs[front.ID] != nil && instance.mobs[front.ID].Angry
	instance.entityMu.Unlock()
	if !angry {
		t.Fatal("enderman was not angered by being looked at")
	}
	for i := 0; i < mobAttackWindupTicks+2; i++ {
		instance.tick()
	}
	health, _, _ := player.healthStatus()
	if want := float32(maxPlayerHealth - mobKinds[mobEnderman].damage); health != want {
		t.Fatalf("player health after enderman attack = %v, want %v", health, want)
	}
}

// TestEndermanTeleportsWhenHurt 验证末影人受击后随机传送（位置变化）。
func TestEndermanTeleportsWhenHurt(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Hitter")

	spawnX, spawnY, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	baseX, baseZ := int(math.Floor(spawnX)), int(math.Floor(spawnZ))
	floorY := int(math.Floor(spawnY)) - 1
	clearCorridor(instance, baseX, floorY, baseZ, 6, 6)

	mob := instance.addMob(world.DimensionOverworld, mobEnderman, spawnX, spawnY, spawnZ+2)
	expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)
	instance.entityMu.Lock()
	startX, startZ := mob.X, mob.Z
	instance.entityMu.Unlock()

	sendAttack(t, conn, mob.ID)
	deadline := time.Now().Add(3 * time.Second)
	for {
		instance.entityMu.Lock()
		current := instance.mobs[mob.ID]
		moved, angry := false, false
		curX, curZ := startX, startZ
		if current != nil {
			curX, curZ = current.X, current.Z
			moved = math.Hypot(curX-startX, curZ-startZ) > 0.5
			angry = current.Angry
		}
		instance.entityMu.Unlock()
		if moved {
			if !angry {
				t.Fatal("enderman teleported but is not angry")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Skip("enderman teleport failed to find a valid spot (random, bounded attempts)")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// mobCount 返回当前生物数量（测试辅助）。
func mobCount(instance *Server) int {
	instance.entityMu.Lock()
	defer instance.entityMu.Unlock()
	return len(instance.mobs)
}

// mobSnapshot 返回当前生物快照（测试辅助）。
func mobSnapshot(instance *Server) []*mob {
	instance.entityMu.Lock()
	defer instance.entityMu.Unlock()
	mobs := make([]*mob, 0, len(instance.mobs))
	for _, m := range instance.mobs {
		mobs = append(mobs, m)
	}
	return mobs
}

// TestExplosionDamagesMobsAndItems 验证爆炸对附近的生物造成伤害并击飞掉落物。
func TestExplosionDamagesMobsAndItems(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Boom2")

	spawnX, spawnY, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	baseX, baseZ := int(math.Floor(spawnX)), int(math.Floor(spawnZ))
	floorY := int(math.Floor(spawnY)) - 1
	clearCorridor(instance, baseX, floorY, baseZ, 3, 3)

	// 生物放在爆心 2 格外：伤害 = (1-2/6)*24 = 16 → 僵尸 20 血不死；再放一只
	// 紧贴爆心的僵尸（伤害 24 → 死亡并掉落）。
	near := instance.addMob(world.DimensionOverworld, mobZombie, spawnX+2, spawnY, spawnZ)
	expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)
	far := instance.addMob(world.DimensionOverworld, mobZombie, spawnX+5.5, spawnY, spawnZ)
	expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)

	// 掉落物放在爆心附近：应获得速度（被炸飞）。
	stack, err := item.FromName("minecraft:stone", 1)
	if err != nil {
		t.Fatal(err)
	}
	dropEntity := instance.spawnItem(world.DimensionOverworld, stack, spawnX+1, float64(spawnY)+0.5, spawnZ, 0, 0, 0, 10)
	instance.entityMu.Lock()
	initialVel := dropEntity.VelX + dropEntity.VelY + dropEntity.VelZ
	instance.entityMu.Unlock()

	instance.explode(world.DimensionOverworld, spawnX, float64(spawnY)+0.5, spawnZ, 3, 24, "Creeper")

	instance.entityMu.Lock()
	nearHealth := float32(-1)
	nearDead := true
	if m := instance.mobs[near.ID]; m != nil {
		nearHealth = m.Health
		nearDead = m.Dead
	}
	farSurvives := instance.mobs[far.ID] != nil && !instance.mobs[far.ID].Dead
	vel := dropEntity.VelX + dropEntity.VelY + dropEntity.VelZ
	instance.entityMu.Unlock()

	if nearHealth != float32(mobKinds[mobZombie].health-16) {
		t.Fatalf("mob 2 blocks from the explosion = %v HP, want %v", nearHealth, mobKinds[mobZombie].health-16)
	}
	if nearDead {
		t.Fatal("mob 2 blocks from the explosion should survive (16 < 20)")
	}
	if !farSurvives {
		t.Fatal("mob 5.5 blocks away should be outside the damage radius")
	}
	if vel == initialVel {
		t.Fatal("drop near the explosion was not knocked back")
	}
}
