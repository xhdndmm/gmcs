//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
	"gmcs/internal/world"
)

// flattenTestArea 在 (x, z) 周围铺一层石头地面并清空上方空气，
// 供需要确定性地形的用例使用（出生点附近可能有山坡/树冠）。
func flattenTestArea(instance *Server, x, y, z, radius int) {
	w := instance.testWorld()
	for dx := -radius; dx <= radius; dx++ {
		for dz := -radius; dz <= radius; dz++ {
			w.SetBlock(x+dx, y-1, z+dz, world.StoneBlock)
			for dy := 0; dy <= 12; dy++ {
				w.SetBlock(x+dx, y+dy, z+dz, world.AirBlock)
			}
		}
	}
}

// TestCactusDamagesPlayerAndMob 验证接触仙人掌会持续扣血（原版
// CactusBlock.entityInside：每次 1 点，频率由 0.5 秒无敌帧限制）。
func TestCactusDamagesPlayerAndMob(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Prickly")
	instance.worldAge.Store(worldNightStart) // 避免日光燃烧混淆伤害来源
	player := findSession(t, instance, "Prickly")
	w := instance.testWorld()

	spawnX, spawnY, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	baseX, baseY, baseZ := int(math.Floor(spawnX)), int(math.Floor(spawnY)), int(math.Floor(spawnZ))
	flattenTestArea(instance, baseX, baseY, baseZ, 4)

	// 在玩家东侧放一柱仙人掌（需要沙/红沙支撑，这里直接用石头支撑，
	// 方块内效果与支撑无关）。
	if !w.SetBlock(baseX+1, baseY, baseZ, world.CactusBlock) ||
		!w.SetBlock(baseX+1, baseY+1, baseZ, world.CactusBlock) {
		t.Fatal("无法放置仙人掌")
	}
	// 玩家紧贴仙人掌（碰撞盒 15/16 宽，玩家半宽 0.3 → 中心距 0.7625 起重叠）。
	player.setPlayerPosition(float64(baseX)+0.77, float64(baseY), float64(baseZ)+0.5, 0, 0)

	healthBefore, _, _ := player.healthStatus()
	instance.tick()
	healthAfter, _, _ := player.healthStatus()
	if healthAfter >= healthBefore {
		t.Fatalf("接触仙人掌后生命值未下降：%v -> %v", healthBefore, healthAfter)
	}

	// 生物同理。
	m := instance.addMob(world.DimensionOverworld, mobZombie, float64(baseX)+0.77, float64(baseY), float64(baseZ)+0.5)
	if m == nil {
		t.Fatal("生成僵尸失败")
	}
	instance.entityMu.Lock()
	mHealth := m.Health
	instance.entityMu.Unlock()
	instance.tick()
	instance.entityMu.Lock()
	health := m.Health
	instance.entityMu.Unlock()
	if health >= mHealth {
		t.Fatalf("生物接触仙人掌后生命值未下降：%v -> %v", mHealth, health)
	}
}

// TestPassiveMobPanicsWhenHurt 验证被动生物受击后逃跑（原版 PanicGoal）：
// 受击设置逃跑计时，并在之后的 tick 里远离攻击者。
func TestPassiveMobPanicsWhenHurt(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Panicky")
	instance.worldAge.Store(worldNightStart)
	player := findSession(t, instance, "Panicky")

	px, py, pz, _, _ := player.playerPosition()
	baseX, baseY, baseZ := int(math.Floor(px)), int(math.Floor(py)), int(math.Floor(pz))
	flattenTestArea(instance, baseX, baseY, baseZ, 6)

	cow := instance.addMob(world.DimensionOverworld, mobCow,
		float64(baseX)+0.5, float64(baseY), float64(baseZ)+2.5)
	if cow == nil {
		t.Fatal("生成牛失败")
	}
	instance.handleAttack(player, cow.ID)
	instance.entityMu.Lock()
	panicTicks := cow.PanicTicks
	startZ := cow.Z
	instance.entityMu.Unlock()
	if panicTicks <= 0 {
		t.Fatal("受击后未设置逃跑计时（PanicTicks）")
	}

	for i := 0; i < 20; i++ {
		instance.tick()
	}
	instance.entityMu.Lock()
	movedZ := cow.Z
	panicAfter := cow.PanicTicks
	instance.entityMu.Unlock()
	if movedZ <= startZ+0.5 {
		t.Fatalf("牛没有远离攻击者：z %v -> %v", startZ, movedZ)
	}
	if panicAfter >= panicTicks {
		t.Fatalf("逃跑计时未递减：%d -> %d", panicTicks, panicAfter)
	}
}

// TestSpectatorMovesThroughBlocks 验证旁观者可以穿过方块（原版
// Player.isSpectator 时 noPhysics，服务端不做穿墙校验）。
func TestSpectatorMovesThroughBlocks(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Ghost")
	player := findSession(t, instance, "Ghost")

	x, y, z, _, _ := player.playerPosition()
	baseX, baseY, baseZ := int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))
	flattenTestArea(instance, baseX, baseY, baseZ, 4)
	// 目标位置整体埋进石头（脚部与头部两格）。
	for dy := 0; dy <= 1; dy++ {
		instance.testWorld().SetBlock(baseX+1, baseY+dy, baseZ, world.StoneBlock)
	}

	// 直接设置为旁观者（/gamemode 需要 op 权限，与本用例关注的移动校验无关）。
	player.setGameMode(uint8(config.GameModeSpectator))
	if got := player.gameModeID(); got != uint8(config.GameModeSpectator) {
		t.Fatalf("游戏模式 = %d, want 旁观者", got)
	}

	targetX, targetZ := float64(baseX+1)+0.5, float64(baseZ)+0.5
	sendPlayerPosition(t, conn, targetX, float64(baseY), targetZ, false)
	processBarrier(t, conn)
	gotX, _, gotZ, _, _ := player.playerPosition()
	if math.Abs(gotX-targetX) > 1e-6 || math.Abs(gotZ-targetZ) > 1e-6 {
		t.Fatalf("旁观者移动被拒绝：(%v,%v) want (%v,%v)", gotX, gotZ, targetX, targetZ)
	}
}

// TestStuckPlayerIsPushedOut 验证玩家被方块夹住（活塞/关闭的门/恢复存档等）
// 时会被推出到空位，而不是永久拒绝所有移动。
func TestStuckPlayerIsPushedOut(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Trapped")
	player := findSession(t, instance, "Trapped")

	x, y, z, _, _ := player.playerPosition()
	baseX, baseY, baseZ := int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))
	flattenTestArea(instance, baseX, baseY, baseZ, 4)
	// 把玩家嵌进石头里（模拟方块被推入玩家位置）。
	instance.testWorld().SetBlock(baseX, baseY, baseZ, world.StoneBlock)
	instance.testWorld().SetBlock(baseX, baseY+1, baseZ, world.StoneBlock)
	if positionClear(instance.testWorld(), x, y, z) {
		t.Fatal("测试前提失效：玩家位置应当是嵌在方块里")
	}

	// 任意移动包：应触发“已嵌入 → 推出到空位”，而不是无限回拉。
	sendPlayerPosition(t, conn, x+0.4, y, z, false)
	processBarrier(t, conn)
	px, py, pz, _, _ := player.playerPosition()
	if !positionClear(instance.testWorld(), px, py, pz) {
		t.Fatalf("玩家仍嵌在方块中：(%.2f,%.2f,%.2f)", px, py, pz)
	}
	// 推出后应能继续正常移动。
	sendPlayerPosition(t, conn, px+0.5, py, pz, true)
	processBarrier(t, conn)
	gotX, _, gotZ, _, _ := player.playerPosition()
	if math.Abs(gotX-(px+0.5)) > 1e-6 || math.Abs(gotZ-pz) > 1e-6 {
		t.Fatalf("推出后移动仍被拒绝：(%.2f,%.2f) want (%.2f,%.2f)", gotX, gotZ, px+0.5, pz)
	}
}

// TestSunlightBurnsUndead 验证僵尸/骷髅在白天阳光下持续着火（原版
// Mob.isSunBurnTick + igniteForSeconds(8)），夜晚不点燃。
func TestSunlightBurnsUndead(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Sunny")

	spawnX, spawnY, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	baseX, baseY, baseZ := int(math.Floor(spawnX)), int(math.Floor(spawnY)), int(math.Floor(spawnZ))
	flattenTestArea(instance, baseX, baseY, baseZ, 4)

	instance.worldAge.Store(6000) // 正午
	zombie := instance.addMob(world.DimensionOverworld, mobZombie,
		float64(baseX)+0.5, float64(baseY), float64(baseZ)+0.5)
	if zombie == nil {
		t.Fatal("生成僵尸失败")
	}
	instance.tick()
	instance.entityMu.Lock()
	dayFire := zombie.FireTicks
	instance.entityMu.Unlock()
	if dayFire <= 0 {
		t.Fatal("白天阳光下的僵尸没有被点燃")
	}

	// 夜晚：新的僵尸不应被阳光点燃。
	instance.worldAge.Store(worldNightStart)
	night := instance.addMob(world.DimensionOverworld, mobSkeleton,
		float64(baseX)+2.5, float64(baseY), float64(baseZ)+0.5)
	if night == nil {
		t.Fatal("生成骷髅失败")
	}
	instance.tick()
	instance.entityMu.Lock()
	nightFire := night.FireTicks
	instance.entityMu.Unlock()
	if nightFire != 0 {
		t.Fatalf("夜晚的骷髅不应被阳光点燃（FireTicks=%d）", nightFire)
	}
}

// TestAquaticMobsSpawnAndSwim 验证海洋生物的存在与游动：种类选择返回水生生物，
// 且在水中游动时始终停留在水体内。
func TestAquaticMobsSpawnAndSwim(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Angler")

	kind, ok := instance.pickAquaticKind()
	if !ok {
		t.Fatal("没有可生成的海洋生物（注册表条目缺失）")
	}
	if !mobKinds[kind].aquatic {
		t.Fatalf("pickAquaticKind 返回了非水生生物 %v", mobKinds[kind].name)
	}

	// 造一个 5×5×3 的水池。
	w := instance.testWorld()
	spawnX, spawnY, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	baseX, baseY, baseZ := int(math.Floor(spawnX)), int(math.Floor(spawnY)), int(math.Floor(spawnZ))
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			w.SetBlock(baseX+dx, baseY-3, baseZ+dz, world.StoneBlock)
			for dy := -2; dy <= -1; dy++ {
				w.SetBlock(baseX+dx, baseY+dy, baseZ+dz, world.WaterBlock)
			}
		}
	}
	fish := instance.addMob(world.DimensionOverworld, kind, float64(baseX)+0.5, float64(baseY)-2, float64(baseZ)+0.5)
	if fish == nil {
		t.Fatal("生成水生生物失败")
	}

	moved := false
	instance.entityMu.Lock()
	for i := 0; i < 200; i++ {
		before := [3]float64{fish.X, fish.Y, fish.Z}
		instance.swimMob(w, fish, mobKinds[kind].walkSpeed)
		if fish.X != before[0] || fish.Y != before[1] || fish.Z != before[2] {
			moved = true
		}
		if !instance.waterBodyAt(w, fish.X, fish.Y, fish.Z) {
			instance.entityMu.Unlock()
			t.Fatalf("水生生物离开了水体：(%.2f,%.2f,%.2f)", fish.X, fish.Y, fish.Z)
		}
	}
	instance.entityMu.Unlock()
	if !moved {
		t.Fatal("水生生物没有游动")
	}
}

// oceanAround 报告 (x, z) 周围 36 格内是否全是水（用于寻找海洋测试点）。
func oceanAround(w *world.World, x, z int) bool {
	for dx := -36; dx <= 36; dx += 12 {
		for dz := -36; dz <= 36; dz += 12 {
			column, ok := w.ColumnAt(x+dx, z+dz)
			if !ok || !isWaterState(column.TopState) {
				return false
			}
		}
	}
	return true
}

// TestSpawnMobInOceanSpawnsAquatic 验证海洋里的自然刷怪生成水生生物
// （旧实现没有任何海洋生物，水面列被直接拒绝）。
func TestSpawnMobInOceanSpawnsAquatic(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = true
	cfg.MaxMobs = 200
	instance, _ := joinServer(t, cfg, "Sailor")
	instance.worldAge.Store(6000) // 白天也应生成海洋生物
	player := findSession(t, instance, "Sailor")
	w := instance.testWorld()

	px, _, pz, _, _ := player.playerPosition()
	var oceanX, oceanZ int
	found := false
	for radius := 32; radius <= 1024 && !found; radius += 32 {
		for _, dir := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}, {1, 1}, {-1, -1}, {1, -1}, {-1, 1}} {
			x, z := int(px)+dir[0]*radius, int(pz)+dir[1]*radius
			if oceanAround(w, x, z) {
				oceanX, oceanZ, found = x, z, true
				break
			}
		}
	}
	if !found {
		t.Skip("该种子附近没有足够大的海洋")
	}
	player.setPlayerPosition(float64(oceanX)+0.5, float64(w.SurfaceY(oceanX, oceanZ)+1), float64(oceanZ)+0.5, 0, 0)

	players := instance.playerSnapshot()
	for i := 0; i < 100; i++ {
		instance.trySpawnMob(players)
	}
	aquatic := 0
	instance.entityMu.Lock()
	for _, m := range instance.mobs {
		if mobKinds[m.Kind].aquatic {
			aquatic++
		}
	}
	instance.entityMu.Unlock()
	if aquatic == 0 {
		t.Fatal("海洋中未生成任何水生生物")
	}
}

// TestLevelStatePersistence 验证世界时间与天气写入 level.json 并在重启后恢复
// （旧行为：worldAge 只在内存里，重启后回到 0 = 早上）。
func TestLevelStatePersistence(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.WorldDir = dir

	saved := &Server{config: cfg}
	saved.worldAge.Store(43210)
	saved.weatherMu.Lock()
	saved.weather.raining = true
	saved.weather.thundering = true
	saved.weather.rainTicks = 1234
	saved.weather.thunderTicks = 5678
	saved.weatherMu.Unlock()
	if err := saved.saveLevelState(); err != nil {
		t.Fatalf("saveLevelState: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, levelFileName)); err != nil {
		t.Fatalf("level.json 未写入：%v", err)
	}

	loaded := &Server{config: cfg}
	if err := loaded.loadLevelState(); err != nil {
		t.Fatalf("loadLevelState: %v", err)
	}
	if got := loaded.worldAge.Load(); got != 43210 {
		t.Fatalf("恢复后的世界时间 = %d, want 43210", got)
	}
	state := loaded.weatherSnapshot()
	if !state.raining || !state.thundering || state.rainTicks != 1234 || state.thunderTicks != 5678 {
		t.Fatalf("恢复后的天气 = %+v", state)
	}
	if !loaded.rainingFlag.Load() {
		t.Fatal("恢复后 rainingFlag 未同步")
	}

	// 文件缺失时保持默认（不报错）。
	missing := &Server{config: config.Default()}
	missing.config.WorldDir = t.TempDir()
	if err := missing.loadLevelState(); err != nil {
		t.Fatalf("缺少 level.json 时应忽略：%v", err)
	}
	if missing.worldAge.Load() != 0 {
		t.Fatalf("缺少 level.json 时世界时间应为 0，got %d", missing.worldAge.Load())
	}
}

// TestGhostRecipePacketEncoding 验证 Place Ghost Recipe 的字段编码
// （1.21.11：VarInt 窗口 ID + VarInt 配方显示编号）。
func TestGhostRecipePacketEncoding(t *testing.T) {
	packet := protocol.EncodePlaceGhostRecipe(protocol.PlayerInventoryWindowID, 42)
	id, offset, err := protocol.DecodeVarInt(packet)
	if err != nil || id != protocol.PlayPacketIDPlaceGhostRecipe {
		t.Fatalf("包 ID = %d (%v), want %d", id, err, protocol.PlayPacketIDPlaceGhostRecipe)
	}
	window, size, err := protocol.DecodeVarInt(packet[offset:])
	if err != nil || window != protocol.PlayerInventoryWindowID {
		t.Fatalf("窗口 ID = %d (%v)", window, err)
	}
	offset += size
	display, _, err := protocol.DecodeVarInt(packet[offset:])
	if err != nil || display != 42 {
		t.Fatalf("配方显示编号 = %d (%v)", display, err)
	}
}
