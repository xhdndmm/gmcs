package server

import (
	"fmt"
	"math"
	"testing"
	"time"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// buildPortalFrame 在 (x, y, z) 处搭建 2×3 的黑曜石框（门面在 z 平面、
// 宽度沿 x），并把内部清成空气。
func buildPortalFrame(t *testing.T, instance *Server, x, y, z int) {
	t.Helper()
	w := instance.testWorld()
	set := func(bx, by, bz int, state uint16) {
		if !w.SetBlock(bx, by, bz, state) {
			t.Fatalf("SetBlock(%d,%d,%d) failed", bx, by, bz)
		}
	}
	for yy := y - 1; yy <= y+3; yy++ {
		set(x-1, yy, z, world.ObsidianBlock)
		set(x+2, yy, z, world.ObsidianBlock)
	}
	for dx := 0; dx <= 1; dx++ {
		set(x+dx, y-1, z, world.ObsidianBlock)
		set(x+dx, y+3, z, world.ObsidianBlock)
		for yy := y; yy <= y+2; yy++ {
			set(x+dx, yy, z, world.AirBlock)
		}
	}
}

// clearTestArea 把出生点附近的地面整平（floor 铺石头，上方 5 格空气）。
func clearTestArea(instance *Server, baseX, floorY, baseZ, radius int) {
	for dx := -radius; dx <= radius; dx++ {
		for dz := -radius; dz <= radius; dz++ {
			instance.testWorld().SetBlock(baseX+dx, floorY, baseZ+dz, world.StoneBlock)
			for dy := 1; dy <= 5; dy++ {
				instance.testWorld().SetBlock(baseX+dx, floorY+dy, baseZ+dz, world.AirBlock)
			}
		}
	}
}

// TestNetherPortalLighting 验证打火石点燃下界传送门：内部 2×3 变为传送门
// 方块（axis=x），且重复点燃不报错。
func TestNetherPortalLighting(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Lighter")
	player := findSession(t, instance, "Lighter")

	spawnX, spawnY, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	baseX, baseZ := int(math.Floor(spawnX)), int(math.Floor(spawnZ))
	y := int(math.Floor(spawnY))
	clearTestArea(instance, baseX, y-1, baseZ, 4)
	buildPortalFrame(t, instance, baseX, y, baseZ)

	if !instance.tryUseFlintAndSteel(player, instance.testWorld(), baseX, y-1, baseZ, world.ObsidianBlock) {
		t.Fatal("flint and steel on a valid frame should light the portal")
	}
	state, ok := portalBlockState("x")
	if !ok {
		t.Fatal("nether_portal axis=x state missing from registry")
	}
	for dx := 0; dx <= 1; dx++ {
		for dy := 0; dy <= 2; dy++ {
			if got := instance.testWorld().BlockAt(baseX+dx, y+dy, baseZ); got != state {
				t.Fatalf("portal block (%d,%d) = %d, want %d", dx, dy, got, state)
			}
		}
	}
	// 已点亮的框架再次点燃：仍然返回 true（幂等）。
	if !instance.tryUseFlintAndSteel(player, instance.testWorld(), baseX, y-1, baseZ, world.ObsidianBlock) {
		t.Fatal("re-lighting an existing portal should succeed")
	}
	// 非框架位置点燃：不点亮。
	if instance.tryUseFlintAndSteel(player, instance.testWorld(), baseX+5, y, baseZ+5, world.ObsidianBlock) {
		t.Fatal("flint and steel on a lone obsidian block should not light a portal")
	}
}

// TestNetherPortalTravel 验证玩家走入点亮的传送门后被传送到下界（坐标 1:8
// 缩放），并设置冷却防止立即回传。
func TestNetherPortalTravel(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.Ops = []string{"Walker"}
	instance, conn := joinServer(t, cfg, "Walker")
	player := findSession(t, instance, "Walker")

	spawnX, spawnY, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	baseX, baseZ := int(math.Floor(spawnX)), int(math.Floor(spawnZ))
	y := int(math.Floor(spawnY))
	clearTestArea(instance, baseX, y-1, baseZ, 4)
	buildPortalFrame(t, instance, baseX, y, baseZ)
	if !instance.tryUseFlintAndSteel(player, instance.testWorld(), baseX, y-1, baseZ, world.ObsidianBlock) {
		t.Fatal("lighting failed")
	}

	// 用 /tp 走到传送门中心（读循环串行处理，避免直接改会话状态）。
	sendChatCommand(t, conn, fmt.Sprintf("/tp %d %d %d", baseX, y, baseZ))
	expectPlayPacket(t, conn, protocol.PlayPacketIDSynchronizePlayerPos)
	// 发送一个合法的微小移动包触发传送门检测。
	sendPlayerPosition(t, conn, float64(baseX)+0.6, float64(y), float64(baseZ)+0.6, true)
	expectPlayPacket(t, conn, protocol.PlayPacketIDRespawn)

	deadline := time.Now().Add(5 * time.Second)
	for player.dimensionID() != world.DimensionNether || player.portalCooldown.Load() <= 0 {
		if time.Now().After(deadline) {
			t.Fatalf("player did not switch to the nether with a cooldown (dim=%v cooldown=%d)",
				player.dimensionID(), player.portalCooldown.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}
	// 下界坐标 ≈ 主世界坐标 / 8（允许搜索/建门半径带来的偏差）。
	px, py, pz, _, _ := player.playerPosition()
	if math.Abs(px*netherScale-float64(baseX)) > 200 || math.Abs(pz*netherScale-float64(baseZ)) > 200 {
		t.Fatalf("nether position (%v,%v) does not correspond to overworld (%d,%d)", px, pz, baseX, baseZ)
	}
	// 下界一侧的传送门存在（找到已有或新建）。
	netherX, netherY, netherZ := int(math.Floor(px)), int(math.Floor(py)), int(math.Floor(pz))
	if _, _, _, ok := instance.findExistingNetherPortal(instance.worldFor(world.DimensionNether), netherX, netherY, netherZ); !ok {
		t.Fatal("no portal found near the arrival position in the nether")
	}
}

// TestClearPortalsAfterFrameBreak 验证破坏框架后相连的传送门方块被清除。
func TestClearPortalsAfterFrameBreak(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Breaker")
	player := findSession(t, instance, "Breaker")

	spawnX, spawnY, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	baseX, baseZ := int(math.Floor(spawnX)), int(math.Floor(spawnZ))
	y := int(math.Floor(spawnY))
	clearTestArea(instance, baseX, y-1, baseZ, 4)
	buildPortalFrame(t, instance, baseX, y, baseZ)
	if !instance.tryUseFlintAndSteel(player, instance.testWorld(), baseX, y-1, baseZ, world.ObsidianBlock) {
		t.Fatal("lighting failed")
	}
	// 移除一根侧柱并触发清除。
	if !instance.testWorld().SetBlock(baseX-1, y, baseZ, world.AirBlock) {
		t.Fatal("removing the frame column failed")
	}
	instance.clearPortalsNear(world.DimensionOverworld, baseX-1, y, baseZ)
	for dx := 0; dx <= 1; dx++ {
		for dy := 0; dy <= 2; dy++ {
			if got := instance.testWorld().BlockAt(baseX+dx, y+dy, baseZ); got != world.AirBlock {
				t.Fatalf("portal block (%d,%d) = %d, want air after frame break", dx, dy, got)
			}
		}
	}
}

// TestEndPortalActivationAndTravel 验证末地传送门激活（12 眼齐备 → 中心
// 3×3 末地传送门）与传送（进入末地平台 (100,50,0)）。
func TestEndPortalActivationAndTravel(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.Ops = []string{"Ender"}
	instance, conn := joinServer(t, cfg, "Ender")
	player := findSession(t, instance, "Ender")

	spawnX, spawnY, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	baseX, baseZ := int(math.Floor(spawnX)), int(math.Floor(spawnZ))
	y := int(math.Floor(spawnY))
	clearTestArea(instance, baseX, y-1, baseZ, 5)

	frameEye, ok := registry.BlockStateWithProps("minecraft:end_portal_frame",
		map[string]string{"eye": "true", "facing": "north"})
	if !ok {
		t.Fatal("end_portal_frame eye=true state missing")
	}
	for _, offset := range endPortalFrameOffsets {
		if !instance.testWorld().SetBlock(baseX+offset[0], y, baseZ+offset[1], frameEye) {
			t.Fatalf("placing frame at %v failed", offset)
		}
	}
	// 用末影之眼交互触发激活检查（框架已全部填充）。
	if !instance.tryPlaceEnderEye(nil, instance.testWorld(), baseX+2, y, baseZ, frameEye) {
		t.Fatal("tryPlaceEnderEye should handle end portal frame")
	}
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			if state := instance.testWorld().BlockAt(baseX+dx, y, baseZ+dz); !isEndPortalState(state) {
				t.Fatalf("end portal block (%d,%d) = %d, want end portal", dx, dz, state)
			}
		}
	}

	// 走入末地传送门 → 传送到末地平台。
	sendChatCommand(t, conn, fmt.Sprintf("/tp %d %d %d", baseX, y, baseZ))
	expectPlayPacket(t, conn, protocol.PlayPacketIDSynchronizePlayerPos)
	sendPlayerPosition(t, conn, float64(baseX)+0.6, float64(y), float64(baseZ)+0.6, true)
	expectPlayPacket(t, conn, protocol.PlayPacketIDRespawn)

	deadline := time.Now().Add(5 * time.Second)
	for player.dimensionID() != world.DimensionEnd || player.portalCooldown.Load() <= 0 {
		if time.Now().After(deadline) {
			t.Fatalf("player did not switch to the end (dim=%v)", player.dimensionID())
		}
		time.Sleep(5 * time.Millisecond)
	}
	px, py, pz, _, _ := player.playerPosition()
	if math.Abs(px-100.5) > 1 || math.Abs(py-endPlatformY) > 1 || math.Abs(pz-0.5) > 1 {
		t.Fatalf("end arrival position = (%v,%v,%v), want (100.5,%d,0.5)", px, py, pz, endPlatformY)
	}
	end := instance.worldFor(world.DimensionEnd)
	if end.BlockAt(100, endPlatformY-1, 0) != world.ObsidianBlock {
		t.Fatal("end arrival platform was not built")
	}
}
