//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 栅栏门 in_wall 属性测试：
// 原版 FenceGateBlock 的 IN_WALL 由沿 facing 轴的前后两个邻居是否为
// `minecraft:walls` 标签方块决定，放置时计算（getStateForPlacement），
// 邻块变化时重新计算（updateShape），且不影响开合状态。

// gateFacingFor 返回与玩家当前朝向一致的栅栏门 facing 索引。
func gateFacingFor(t *testing.T, instance *Server, name string) int {
	t.Helper()
	player := activeSession(t, instance, name)
	_, _, _, yaw, _ := player.playerPosition()
	return horizontalFacingIndex(yawToFacing(yaw))
}

// TestFenceGateInWallOnPlacement 验证放置时按 facing 轴两侧的墙计算 in_wall。
func TestFenceGateInWallOnPlacement(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "GateWall")
	defer conn.Close()

	x, groundY, z := farmTestSpot(t, instance)
	w := instance.testWorld()
	player := activeSession(t, instance, "GateWall")
	player.inventory.Set(item.SlotHotbarStart, testStack(t, "minecraft:oak_fence_gate", 4))
	facing := gateFacingFor(t, instance, "GateWall")
	dir := fenceGateFacingByIndex(facing)
	front := offsetDir([3]int{x, groundY + 1, z}, dir)

	wall, ok := registry.BlockStateIDs["minecraft:cobblestone_wall"]
	if !ok {
		t.Fatal("缺少圆石墙方块状态")
	}
	// 沿 facing 轴的前方放一堵墙：放置栅栏门时应带 in_wall=true。
	if !w.SetBlock(front[0], front[1], front[2], wall) {
		t.Fatal("无法放置墙体")
	}
	sendUseItemOn(t, conn, x, groundY, z, 1)
	want := registry.PlacementFenceGates["minecraft:oak_fence_gate"][1][facing]
	waitFor(t, func() bool { return w.BlockAt(x, groundY+1, z) == want },
		"facing 轴前方有墙时栅栏门应以 in_wall=true 放置")
}

// TestFenceGateInWallUpdatesOnNeighborChange 验证邻块变化后 in_wall 重新计算，
// 且不改变已有的开合状态。
func TestFenceGateInWallUpdatesOnNeighborChange(t *testing.T) {
	instance := newHopperTestServer(t)
	x, z := 30, 30
	groundY := hopperTestPad(t, instance, x, z, 3)
	w := instance.testWorld()
	wall, ok := registry.BlockStateIDs["minecraft:cobblestone_wall"]
	if !ok {
		t.Fatal("缺少圆石墙方块状态")
	}
	closed := registry.PlacementFenceGates["minecraft:oak_fence_gate"]
	open := registry.PlacementFenceGatesOpen["minecraft:oak_fence_gate"]
	pos := [3]int{x, groundY + 1, z}
	const facing = 0 // north：facing 轴为 z，前后分别是 (z-1) 与 (z+1)

	w.SetBlock(pos[0], pos[1], pos[2], closed[0][facing])
	// 无墙时：状态保持不变（不应被误改）。
	instance.refreshOpenable(w, world.DimensionOverworld, pos, closed[0][facing])
	if got := w.BlockAt(pos[0], pos[1], pos[2]); got != closed[0][facing] {
		t.Fatalf("无墙时栅栏门状态被改动：%d want %d", got, closed[0][facing])
	}

	// 后方放墙 → in_wall 应变 true，仍是关闭状态。
	w.SetBlock(pos[0], pos[1], pos[2]-1, wall)
	instance.refreshOpenable(w, world.DimensionOverworld, pos, closed[0][facing])
	if got := w.BlockAt(pos[0], pos[1], pos[2]); got != closed[1][facing] {
		t.Fatalf("邻块加墙后未更新 in_wall：%d want %d", got, closed[1][facing])
	}

	// 打开的栅栏门在加墙后仍保持打开（in_wall=true 的打开状态）。
	w.SetBlock(pos[0], pos[1], pos[2], open[0][facing])
	instance.refreshOpenable(w, world.DimensionOverworld, pos, open[0][facing])
	if got := w.BlockAt(pos[0], pos[1], pos[2]); got != open[1][facing] {
		t.Fatalf("打开状态的栅栏门未更新 in_wall：%d want %d", got, open[1][facing])
	}

	// 移除墙 → in_wall 恢复 false。
	w.SetBlock(pos[0], pos[1], pos[2]-1, world.AirBlock)
	instance.refreshOpenable(w, world.DimensionOverworld, pos, open[1][facing])
	if got := w.BlockAt(pos[0], pos[1], pos[2]); got != open[0][facing] {
		t.Fatalf("移除墙后 in_wall 未恢复：%d want %d", got, open[0][facing])
	}
}

// TestFenceGateInWallIgnoresPerpendicularWalls 验证垂直于 facing 轴的墙不影响
// in_wall（原版只检查 facing 轴上的两个邻居）。
func TestFenceGateInWallIgnoresPerpendicularWalls(t *testing.T) {
	instance := newHopperTestServer(t)
	x, z := 34, 34
	groundY := hopperTestPad(t, instance, x, z, 3)
	w := instance.testWorld()
	wall, ok := registry.BlockStateIDs["minecraft:cobblestone_wall"]
	if !ok {
		t.Fatal("缺少圆石墙方块状态")
	}
	closed := registry.PlacementFenceGates["minecraft:oak_fence_gate"]
	pos := [3]int{x, groundY + 1, z}
	w.SetBlock(pos[0], pos[1], pos[2], closed[0][0])
	// facing=north 时 facing 轴为 z；x-1 / x+1 是垂直方向，不应触发 in_wall。
	w.SetBlock(pos[0]-1, pos[1], pos[2], wall)
	w.SetBlock(pos[0]+1, pos[1], pos[2], wall)
	instance.refreshOpenable(w, world.DimensionOverworld, pos, closed[0][0])
	if got := w.BlockAt(pos[0], pos[1], pos[2]); got != closed[0][0] {
		t.Fatalf("垂直方向的墙不应改变 in_wall：%d want %d", got, closed[0][0])
	}
}
