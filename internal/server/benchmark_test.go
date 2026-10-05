package server

import (
	"math"
	"testing"

	"gmcs/internal/config"
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
	defer instance.world.Close()

	spawnX, spawnY, spawnZ := instance.spawnPosition()
	_ = spawnY
	for i := 0; i < 32; i++ {
		x := int(math.Floor(spawnX)) + i%8
		z := int(math.Floor(spawnZ)) + i/8
		groundY, ok := instance.world.GroundY(x, z)
		if !ok {
			b.Fatal("no ground at spawn area")
		}
		instance.addMob(float64(x)+0.5, groundY, float64(z)+0.5)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
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
