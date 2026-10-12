package server

import (
	"math"
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/world"
)

// TestZombiePathsAroundWall 验证生物 A* 寻路：生物与玩家之间有一堵
// 带缺口的墙时，直线移动被挡住，生物会绕到缺口再接近玩家。
func TestZombiePathsAroundWall(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Walled")
	player := findSession(t, instance, "Walled")

	px, py, pz, _, _ := player.playerPosition()
	baseX, baseZ := int(math.Floor(px)), int(math.Floor(pz))
	floorY := int(math.Floor(py)) - 1

	// 平整玩家周围 17×17 的地面（上方 4 格空气）。
	for dx := -8; dx <= 8; dx++ {
		for dz := -8; dz <= 8; dz++ {
			instance.testWorld().SetBlock(baseX+dx, floorY, baseZ+dz, world.StoneBlock)
			for dy := 1; dy <= 4; dy++ {
				instance.testWorld().SetBlock(baseX+dx, floorY+dy, baseZ+dz, world.AirBlock)
			}
		}
	}
	// 在玩家以东 4 格筑一道 3 格高的墙（z 从 -8 到 +6，只在南端留缺口）。
	for dz := -8; dz <= 6; dz++ {
		for dy := 1; dy <= 3; dy++ {
			instance.testWorld().SetBlock(baseX+4, floorY+dy, baseZ+dz, world.StoneBlock)
		}
	}

	mob := instance.addMob(world.DimensionOverworld, mobZombie, px+8, py, pz)
	if mob == nil {
		t.Fatal("生成僵尸失败")
	}

	reached := false
	for i := 0; i < 1500; i++ {
		instance.tick()
		instance.entityMu.Lock()
		distance := math.Hypot(mob.X-px, mob.Z-pz)
		instance.entityMu.Unlock()
		if distance <= mobAttackRange+0.6 {
			reached = true
			break
		}
	}
	if !reached {
		instance.entityMu.Lock()
		x, y, z := mob.X, mob.Y, mob.Z
		instance.entityMu.Unlock()
		t.Fatalf("僵尸未能绕墙到达玩家：位置 (%.1f, %.1f, %.1f)，玩家 (%.1f, %.1f, %.1f)",
			x, y, z, px, py, pz)
	}
}

// TestMobFallsOffLedge 验证生物可以走下最多 3 格的落差接近玩家
// （旧实现只允许 1 格高度差，会站在平台上不动）。
func TestMobFallsOffLedge(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Ledge")
	player := findSession(t, instance, "Ledge")

	px, py, pz, _, _ := player.playerPosition()
	baseX, baseZ := int(math.Floor(px)), int(math.Floor(pz))
	floorY := int(math.Floor(py)) - 1

	// 平整玩家周围 13×13 的地面。
	for dx := -6; dx <= 6; dx++ {
		for dz := -6; dz <= 6; dz++ {
			instance.testWorld().SetBlock(baseX+dx, floorY, baseZ+dz, world.StoneBlock)
			for dy := 1; dy <= 6; dy++ {
				instance.testWorld().SetBlock(baseX+dx, floorY+dy, baseZ+dz, world.AirBlock)
			}
		}
	}
	// 在以东 4–6 格处搭一个 3 格高的平台，僵尸站在平台上。
	for dx := 4; dx <= 6; dx++ {
		for dz := -1; dz <= 1; dz++ {
			instance.testWorld().SetBlock(baseX+dx, floorY+3, baseZ+dz, world.StoneBlock)
		}
	}
	mob := instance.addMob(world.DimensionOverworld, mobZombie, px+5, py+3, pz)
	if mob == nil {
		t.Fatal("生成僵尸失败")
	}

	reached := false
	for i := 0; i < 900; i++ {
		instance.tick()
		instance.entityMu.Lock()
		distance := math.Hypot(mob.X-px, mob.Z-pz)
		y := mob.Y
		instance.entityMu.Unlock()
		if distance <= mobAttackRange+0.6 && y < py+1 {
			reached = true
			break
		}
	}
	if !reached {
		instance.entityMu.Lock()
		x, y, z := mob.X, mob.Y, mob.Z
		instance.entityMu.Unlock()
		t.Fatalf("僵尸未能走下平台接近玩家：位置 (%.1f, %.1f, %.1f)，玩家 (%.1f, %.1f, %.1f)",
			x, y, z, px, py, pz)
	}
}
