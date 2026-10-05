package server

import (
	"math"
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// itemState 返回掉落物的位置与速度快照（测试用）。
func itemState(t *testing.T, instance *Server, id int32) (x, y, z float64, exists bool) {
	t.Helper()
	instance.entityMu.Lock()
	defer instance.entityMu.Unlock()
	e, ok := instance.items[id]
	if !ok {
		return 0, 0, 0, false
	}
	return e.X, e.Y, e.Z, true
}

// TestItemPerAxisCollision 验证逐轴 AABB 碰撞：
// 水平抛出的掉落物撞墙停下（X 不再前进），且不会穿过墙。
func TestItemPerAxisCollision(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Thrower")
	player := findSession(t, instance, "Thrower")
	x, y, z, _, _ := player.playerPosition()
	baseX, baseY, baseZ := int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))

	// 在玩家东侧 3 格处建一堵两格高的墙。
	wallX := baseX + 3
	for _, dy := range []int{0, 1} {
		if !instance.world.SetBlock(wallX, baseY+dy, baseZ, world.StoneBlock) {
			t.Fatal("建墙失败")
		}
	}

	stone, err := item.FromName("minecraft:stone", 1)
	if err != nil {
		t.Fatal(err)
	}
	// 从玩家位置以 0.4 格/tick 向东抛出。
	e := instance.spawnItem(stone, x, float64(baseY)+1, z, 0.4, 0.1, 0, itemPickupDelayPlayer)
	if e == nil {
		t.Fatal("生成掉落物失败")
	}
	expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)
	expectPlayPacket(t, conn, protocol.PlayPacketIDEntityMetadata)

	// 推进足够多帧让它撞上墙。
	for i := 0; i < 30; i++ {
		instance.tick()
	}
	ex, _, _, exists := itemState(t, instance, e.ID)
	if !exists {
		t.Fatal("掉落物意外消失")
	}
	if ex >= float64(wallX) {
		t.Fatalf("掉落物穿过了墙：x = %.3f, 墙在 %d", ex, wallX)
	}
	if ex < float64(baseX)+0.5 {
		t.Fatalf("掉落物没有向墙移动：x = %.3f", ex)
	}
}

// TestItemFallsAndRests 验证重力与地面碰撞：掉落物从空中落下后停在地面高度，
// 不会沉入地面之下的方块。
func TestItemFallsAndRests(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Dropper")
	player := findSession(t, instance, "Dropper")
	x, _, z, _, _ := player.playerPosition()
	column, ok := instance.world.ColumnAt(int(math.Floor(x)), int(math.Floor(z)))
	if !ok || !column.HasSolid {
		t.Fatal("出生点没有地面")
	}
	groundY := float64(column.SolidY + 1)

	stone, err := item.FromName("minecraft:stone", 1)
	if err != nil {
		t.Fatal(err)
	}
	e := instance.spawnItem(stone, x, groundY+5, z, 0, 0, 0, itemPickupDelayPlayer*100)
	expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)
	expectPlayPacket(t, conn, protocol.PlayPacketIDEntityMetadata)

	for i := 0; i < 60; i++ {
		instance.tick()
	}
	_, ey, ez, exists := itemState(t, instance, e.ID)
	if !exists {
		t.Fatal("掉落物意外消失")
	}
	if math.Abs(ey-groundY) > 0.06 {
		t.Fatalf("落地高度 y = %.3f, want ≈ %.3f", ey, groundY)
	}
	if math.Abs(ez-z) > 0.01 {
		t.Fatalf("掉落物不应漂移：z = %.3f, want %.3f", ez, z)
	}
}

// TestItemDestroyedByHazards 验证岩浆/火/仙人掌销毁掉落物。
func TestItemDestroyedByHazards(t *testing.T) {
	for _, hazard := range []string{"minecraft:lava", "minecraft:fire", "minecraft:cactus"} {
		t.Run(hazard, func(t *testing.T) {
			cfg := config.Default()
			cfg.WorldDir = t.TempDir()
			cfg.SpawnMonsters = false
			instance, conn := joinServer(t, cfg, "Burner")
			player := findSession(t, instance, "Burner")
			x, y, z, _, _ := player.playerPosition()
			baseX, baseY, baseZ := int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))

			state, ok := registry.BlockStateIDs[hazard]
			if !ok {
				t.Fatalf("缺少方块 %s", hazard)
			}
			// 在玩家上方 2 格放置危险方块，并把掉落物生成在其中。
			hazardY := baseY + 2
			if !instance.world.SetBlock(baseX, hazardY, baseZ, state) {
				t.Fatal("放置危险方块失败")
			}

			stone, err := item.FromName("minecraft:stone", 1)
			if err != nil {
				t.Fatal(err)
			}
			e := instance.spawnItem(stone, x, float64(hazardY)+0.1, z, 0, 0, 0, itemPickupDelayPlayer)
			expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)
			expectPlayPacket(t, conn, protocol.PlayPacketIDEntityMetadata)

			instance.tick()
			if _, _, _, exists := itemState(t, instance, e.ID); exists {
				t.Fatalf("%s 中的掉落物应被销毁", hazard)
			}
			expectPlayPacket(t, conn, protocol.PlayPacketIDRemoveEntities)
		})
	}
}

// TestItemFloatsOnWater 验证掉落物在水中缓慢上浮而不是下沉。
func TestItemFloatsOnWater(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Floater")
	player := findSession(t, instance, "Floater")
	x, y, z, _, _ := player.playerPosition()
	baseX, baseY, baseZ := int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))

	// 在玩家上方 3 格放一格水，掉落物从水底开始。
	waterY := baseY + 3
	if !instance.world.SetBlock(baseX, waterY, baseZ, world.WaterBlock) {
		t.Fatal("放置水失败")
	}
	stone, err := item.FromName("minecraft:stone", 1)
	if err != nil {
		t.Fatal(err)
	}
	e := instance.spawnItem(stone, x, float64(waterY), z, 0, 0, 0, itemPickupDelayPlayer)
	expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)
	expectPlayPacket(t, conn, protocol.PlayPacketIDEntityMetadata)

	_, startY, _, _ := itemState(t, instance, e.ID)
	for i := 0; i < 20; i++ {
		instance.tick()
	}
	_, endY, _, exists := itemState(t, instance, e.ID)
	if !exists {
		t.Fatal("掉落物意外消失")
	}
	if endY <= startY {
		t.Fatalf("掉落物应在水中上浮：start=%.3f end=%.3f", startY, endY)
	}
}
