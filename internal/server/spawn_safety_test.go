//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// TestUnstickPlayerPosition 验证“玩家被方块埋住”的兜底修正：
// 与方块碰撞形状重叠的位置会被推到最近的可站立空位，否则客户端发出的
// 移动都会因穿墙校验被服务器拒绝（表现为出生后无法移动）。
func TestUnstickPlayerPosition(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Stuck")

	w := instance.testWorld()
	stone, ok := registry.BlockStateIDs["minecraft:stone"]
	if !ok {
		t.Fatal("缺少石头方块状态")
	}
	x, y, z := buildSpotNearSpawn(t, instance)
	// 把该列填成实心石头柱（含玩家身体高度）。
	for dy := 0; dy < 3; dy++ {
		if !w.SetBlock(x, y+dy, z, stone) {
			t.Fatal("无法放置石头")
		}
	}
	px, pz := float64(x)+0.5, float64(z)+0.5
	if positionClear(w, px, float64(y), pz) {
		t.Fatal("埋在石头内的位置不应被判为可站立")
	}
	nx, ny, nz, moved := unstickPlayerPosition(w, px, float64(y), pz)
	if !moved {
		t.Fatal("被方块埋住的位置应被修正")
	}
	if !positionClear(w, nx, ny, nz) {
		t.Fatalf("修正后的位置仍与方块重叠：%v %v %v", nx, ny, nz)
	}
}

// TestJoinPositionIsMovable 验证新玩家进入世界后的位置既不嵌在方块里，
// 也能被服务器的移动校验接受（回归：出生在栅栏/凹坑等位置时完全无法移动）。
func TestJoinPositionIsMovable(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Spawner")

	player := activeSession(t, instance, "Spawner")
	w := instance.testWorld()
	x, y, z, _, _ := player.playerPosition()
	if !positionClear(w, x, y, z) {
		t.Fatalf("进入世界的位置与方块碰撞形状重叠：%v %v %v", x, y, z)
	}

	// 一次水平移动必须被接受（服务器接受后会更新会话位置）。
	sendPlayerPosition(t, conn, x+0.4, y, z, true)
	waitFor(t, func() bool {
		px, py, pz, _, _ := player.playerPosition()
		return math.Abs(px-(x+0.4)) < 0.05 && math.Abs(py-y) < 0.05 && math.Abs(pz-z) < 0.05
	}, "进入世界后的第一次水平移动应被服务器接受")
}

// TestSpawnColumnAvoidsCollisionShapes 验证出生点搜索会跳过虽然“地面可见”
// 但玩家碰撞盒会与碰撞形状相交的列（栅栏、墙、箱子等）。
func TestSpawnColumnAvoidsCollisionShapes(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Columner")

	w := instance.testWorld()
	fence, ok := registry.BlockStateIDs["minecraft:oak_fence"]
	if !ok {
		t.Fatal("缺少栅栏方块状态")
	}
	x, y, z := buildSpotNearSpawn(t, instance)
	// 栅栏上方 1 格处“地面顶面”看似可站立，但玩家碰撞盒会嵌进栅栏碰撞形状。
	if !w.SetBlock(x, y, z, fence) {
		t.Fatal("无法放置栅栏")
	}
	box := world.Box{
		MinX: float64(x) + 0.2, MinY: float64(y + 1), MinZ: float64(z) + 0.2,
		MaxX: float64(x) + 0.8, MaxY: float64(y+1) + 1.8, MaxZ: float64(z) + 0.8,
	}
	if !w.Collides(box) {
		t.Fatal("站在栅栏顶面的玩家碰撞盒应与栅栏碰撞形状相交（出生点必须跳过该列）")
	}
}
