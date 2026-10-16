//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/world"
)

// TestMobLooksAtNearbyPlayer 验证未移动的生物会转向 8 格内的玩家
// （原版 Monster 的 LookAtPlayerGoal：2% 触发概率、40–79 tick 注视、
// 10°/tick 转向），且远处的玩家不会被注视。
func TestMobLooksAtNearbyPlayer(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Looked")
	baseX, floorY, baseZ := fluidBase(t, instance)

	mob := instance.addMob(world.DimensionOverworld, mobZombie, float64(baseX)+0.5, float64(floorY), float64(baseZ)+0.5)
	if mob == nil {
		t.Fatal("addMob 失败")
	}
	player := findSession(t, instance, "Looked")
	// 玩家在生物正东 3 格：期望朝向为 -90°（atan2(-dx, dz)）。
	player.setPlayerPosition(float64(baseX)+3+0.5, float64(floorY), float64(baseZ)+0.5, 0, 0)

	wantYaw := float32(-90)
	best := float32(180)
	for i := 0; i < 2000; i++ {
		instance.tick()
		instance.entityMu.Lock()
		yaw := mob.Yaw
		instance.entityMu.Unlock()
		if delta := float32(math.Abs(float64(wrapDegrees(yaw - wantYaw)))); delta < best {
			best = delta
		}
		if best <= 1 {
			break
		}
	}
	if best > 1 {
		t.Fatalf("生物朝向与期望方向的最小夹角 = %v°, want ≤1°（未注视玩家）", best)
	}

	// 远离玩家（20 格）的生物不应被注视。
	far := instance.addMob(world.DimensionOverworld, mobZombie, float64(baseX)+20.5, float64(floorY), float64(baseZ)+0.5)
	if far == nil {
		t.Fatal("addMob（远处）失败")
	}
	instance.entityMu.Lock()
	far.Yaw = 0
	instance.entityMu.Unlock()
	for i := 0; i < 500; i++ {
		instance.tick()
	}
	instance.entityMu.Lock()
	farYaw := far.Yaw
	farLook := far.LookTarget
	instance.entityMu.Unlock()
	if farLook != nil {
		t.Fatal("20 格外的玩家被选为注视目标")
	}
	if math.Abs(float64(wrapDegrees(farYaw))) > 0.01 && math.Abs(math.Abs(float64(wrapDegrees(farYaw)))-180) > 0.01 {
		// 远处生物仍会游荡（朝向会变），但不应朝向玩家（-90°）。
		if math.Abs(float64(wrapDegrees(farYaw-(-90)))) <= 5 {
			t.Fatalf("远处生物朝向 = %v°, 疑似注视了范围外的玩家", farYaw)
		}
	}
}
