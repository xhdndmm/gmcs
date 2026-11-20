//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/world"
)

func TestStructureSpawnerSpawnsNearPlayer(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	instance, _ := joinServer(t, cfg, "Spawner")
	if !instance.mobsEnabled {
		t.Skip("生物系统未启用")
	}
	player := findSession(t, instance, "Spawner")
	x, y, z := instance.spawnPositionFor(world.DimensionOverworld)
	entity := world.BlockEntity{TypeID: 1, SpawnerEntity: "minecraft:zombie", SpawnerDelay: 0}
	if err := instance.testWorld().SetBlockEntity(int(x), int(y), int(z), entity); err != nil {
		t.Fatal(err)
	}
	// 刷怪笼同样受光照限制：默认白天表面光照为 15，先把时间设为夜晚，
	// 让测试关注玩家半径与延迟路径。
	instance.worldAge.Store(worldNightStart)
	instance.entityMu.Lock()
	before := len(instance.mobs)
	instance.entityMu.Unlock()
	instance.tickSpawners([]*session{player})
	instance.entityMu.Lock()
	got := len(instance.mobs)
	instance.entityMu.Unlock()
	if got <= before {
		t.Fatalf("刷怪笼没有生成生物：before=%d after=%d", before, got)
	}
}
