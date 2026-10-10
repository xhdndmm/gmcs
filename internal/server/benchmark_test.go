package server

import (
	"math"
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/world"
)

// BenchmarkServerTick 衡量实体 Tick 的成本（32 只生物的移动、避障与广播判定）。
func BenchmarkServerTick(b *testing.B) {
	cfg := config.Default()
	cfg.WorldDir = b.TempDir()
	cfg.SpawnMonsters = false
	instance, err := New(cfg)
	if err != nil {
		b.Fatal(err)
	}
	defer instance.worldFor(world.DimensionOverworld).Close()

	spawnX, spawnY, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	_ = spawnY
	gameWorld := instance.worldFor(world.DimensionOverworld)
	for i := 0; i < 32; i++ {
		x := int(math.Floor(spawnX)) + i%8
		z := int(math.Floor(spawnZ)) + i/8
		groundY, ok := gameWorld.GroundY(x, z)
		if !ok {
			b.Fatal("no ground at spawn area")
		}
		instance.addMob(world.DimensionOverworld, mobZombie, float64(x)+0.5, groundY, float64(z)+0.5)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		instance.tick()
	}
	b.StopTimer()

	instance.entityMu.Lock()
	remaining := len(instance.mobs)
	instance.entityMu.Unlock()
	if remaining != 32 {
		b.Fatalf("剩余生物 %d，期望 32（基准可能未在测量真实路径）", remaining)
	}
}

// BenchmarkItemTick 衡量掉落物 Tick 的成本（128 个掉落物的物理、合并
// 扫描与拾取判定；场景中无玩家，走不拾取分支）。
func BenchmarkItemTick(b *testing.B) {
	cfg := config.Default()
	cfg.WorldDir = b.TempDir()
	cfg.SpawnMonsters = false
	instance, err := New(cfg)
	if err != nil {
		b.Fatal(err)
	}
	defer instance.worldFor(world.DimensionOverworld).Close()

	stack, err := item.FromName("minecraft:stone", 1)
	if err != nil {
		b.Fatal(err)
	}
	spawnX, _, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	gameWorld := instance.worldFor(world.DimensionOverworld)
	for i := 0; i < 128; i++ {
		x := float64(int(math.Floor(spawnX))+i%8*2) + 0.5
		z := float64(int(math.Floor(spawnZ))+i/8) + 0.5
		groundY, ok := gameWorld.GroundY(int(x), int(z))
		if !ok {
			b.Fatal("no ground at spawn area")
		}
		e := instance.spawnItem(world.DimensionOverworld, stack, x, groundY+1, z, 0, 0, 0, itemPickupDelayMining)
		if e == nil {
			b.Fatal("spawnItem returned nil")
		}
		// 基准期间不允许到期消失（迭代次数可能远超 6000 tick）。
		e.AgeTicks = -1 << 30
	}

	instance.entityMu.Lock()
	spawned := len(instance.items)
	instance.entityMu.Unlock()
	if spawned != 128 {
		b.Fatalf("掉落物 %d，期望 128", spawned)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		instance.tick()
	}
	b.StopTimer()

	instance.entityMu.Lock()
	remaining := len(instance.items)
	instance.entityMu.Unlock()
	if remaining != 128 {
		b.Fatalf("剩余掉落物 %d，期望 128（基准可能未在测量真实路径）", remaining)
	}
}
