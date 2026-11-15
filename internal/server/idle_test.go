//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"testing"
	"time"

	"gmcs/internal/config"
	"gmcs/internal/world"
)

// TestIdleReleaseStages 验证无玩家时按阶段释放：运行时缓存、实体、空闲维度
// 分别由三个时间阈值控制，而不是在最后一名玩家退出时一次性清空。
func TestIdleReleaseStages(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "IdleStages")
	player := findSession(t, instance, "IdleStages")
	gameWorld := instance.testWorld()
	nether := instance.worldFor(world.DimensionNether)
	if nether == nil {
		t.Fatal("下界世界不可用")
	}
	if _, err := nether.Chunk(0, 0); err != nil {
		t.Fatal(err)
	}
	mob := instance.addMob(world.DimensionOverworld, mobZombie, 0.5, float64(world.FlatGroundLevel+1), 0.5)
	if mob == nil {
		t.Fatal("生成测试生物失败")
	}
	instance.hopperMu.Lock()
	instance.hoppers[containerKey{dim: world.DimensionOverworld, x: 0, y: 64, z: 0}] = &hopperState{}
	instance.hopperMu.Unlock()

	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	waitPlayerOffline(t, instance, player.uuid)

	// 阶段 1：清运行时缓存，保留实体。
	instance.idleMu.Lock()
	instance.idleStage1Delay = time.Nanosecond
	instance.idleStage2Delay = time.Hour
	instance.idleStage3Delay = time.Hour
	instance.idleMu.Unlock()
	if err := instance.unloadFarChunks(); err != nil {
		t.Fatal(err)
	}
	instance.hopperMu.Lock()
	hoppers := len(instance.hoppers)
	instance.hopperMu.Unlock()
	if hoppers != 0 {
		t.Fatalf("阶段 1 后漏斗缓存仍有 %d 条", hoppers)
	}
	instance.entityMu.Lock()
	mobs := len(instance.mobs)
	instance.entityMu.Unlock()
	if mobs == 0 {
		t.Fatal("阶段 1 不应清空实体")
	}
	if gameWorld.ChunkCount() != 0 {
		t.Fatalf("空闲区块未释放：%d", gameWorld.ChunkCount())
	}

	// 阶段 2：持久化并清空实体。
	instance.idleMu.Lock()
	instance.idleStage2Delay = time.Nanosecond
	instance.idleMu.Unlock()
	if err := instance.unloadFarChunks(); err != nil {
		t.Fatal(err)
	}
	instance.entityMu.Lock()
	mobs = len(instance.mobs)
	instance.entityMu.Unlock()
	if mobs != 0 {
		t.Fatalf("阶段 2 后实体仍有 %d 个", mobs)
	}

	// 阶段 3：关闭非主世界维度。
	instance.idleMu.Lock()
	instance.idleStage3Delay = time.Nanosecond
	instance.idleMu.Unlock()
	if err := instance.unloadFarChunks(); err != nil {
		t.Fatal(err)
	}
	instance.worldMu.RLock()
	_, stillOpen := instance.worlds[world.DimensionNether]
	instance.worldMu.RUnlock()
	if stillOpen {
		t.Fatal("阶段 3 后下界世界仍被保留")
	}
}
