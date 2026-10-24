//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/world"
)

// TestSegmentIntersectsBox 验证箭矢扫掠命中判定（高速箭矢不再穿过目标）。
func TestSegmentIntersectsBox(t *testing.T) {
	box := world.Box{MinX: -0.3, MinY: 0, MinZ: 1.7, MaxX: 0.3, MaxY: 1.95, MaxZ: 2.3}
	cases := []struct {
		name       string
		x0, y0, z0 float64
		x1, y1, z1 float64
		want       bool
	}{
		{"穿过目标", 0, 1, 0, 0, 1, 3, true},
		{"起点在盒内", 0, 1, 2, 0, 1, 3, true},
		{"终点在盒内", 0, 1, 0, 0, 1, 2.2, true},
		{"完全越过", 0, 1, 3, 0, 1, 6, false},
		{"横向偏离", 1.5, 1, 0, 1.5, 1, 3, false},
		{"高度偏离", 0, 3, 0, 0, 3, 3, false},
	}
	for _, tc := range cases {
		if got := segmentIntersectsBox(tc.x0, tc.y0, tc.z0, tc.x1, tc.y1, tc.z1, box); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestArrowHitsMobAtHighSpeed 验证 3 格/tick 的玩家箭矢不会穿过 0.6 格宽的
// 生物（旧实现只检查终点会漏判）。
func TestArrowHitsMobAtHighSpeed(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Tunnel")
	player := findSession(t, instance, "Tunnel")

	spawnX, spawnY, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	baseX, baseZ := int(spawnX), int(spawnZ)
	clearCorridor(instance, baseX, int(spawnY)-1, baseZ, 1, 6)

	// 目标在 +2 格；一 tick 后箭矢终点约 +3 格（已越过生物），扫掠判定应命中。
	zombie := instance.addMob(world.DimensionOverworld, mobZombie, spawnX, spawnY, spawnZ+2)
	if arrow := instance.spawnArrow(world.DimensionOverworld, spawnX, spawnY+playerEyeHeight, spawnZ,
		0, 0, 3, 6, player.entityID, 0, false); arrow == nil {
		t.Fatal("spawnArrow returned nil")
	}
	instance.tick()

	instance.entityMu.Lock()
	health := zombie.Health
	remaining := len(instance.arrows)
	instance.entityMu.Unlock()
	if health >= mobKinds[mobZombie].health {
		t.Fatalf("高速箭矢穿过了生物：health=%v", health)
	}
	if remaining != 0 {
		t.Fatalf("命中后仍存在 %d 支箭", remaining)
	}
}
