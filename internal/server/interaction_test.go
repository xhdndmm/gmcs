package server

import (
	"math"
	"net"
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// sendPlayerAction 发送 Player Action（block_dig）包。
func sendPlayerAction(t *testing.T, conn net.Conn, status int32, x, y, z int, face byte) {
	t.Helper()
	packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDPlayerAction)
	packet = protocol.AppendVarInt(packet, status)
	packet = protocol.AppendInt64(packet, protocol.PackPosition(x, y, z))
	packet = append(packet, face)
	packet = protocol.AppendVarInt(packet, 0) // sequence
	if err := protocol.WritePacketWithCompression(conn, packet, compressionThreshold); err != nil {
		t.Fatal(err)
	}
}

// sendUseItemOn 发送 Use Item On（block_place）包（主手、光标居中）。
func sendUseItemOn(t *testing.T, conn net.Conn, x, y, z int, direction int32) {
	t.Helper()
	packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDUseItemOn)
	packet = protocol.AppendVarInt(packet, 0) // hand
	packet = protocol.AppendInt64(packet, protocol.PackPosition(x, y, z))
	packet = protocol.AppendVarInt(packet, direction)
	packet = protocol.AppendFloat32(packet, 0.5)
	packet = protocol.AppendFloat32(packet, 0.5)
	packet = protocol.AppendFloat32(packet, 0.5)
	packet = protocol.AppendBool(packet, false) // insideBlock
	packet = protocol.AppendBool(packet, false) // worldBorderHit
	packet = protocol.AppendVarInt(packet, 0)   // sequence
	if err := protocol.WritePacketWithCompression(conn, packet, compressionThreshold); err != nil {
		t.Fatal(err)
	}
}

// sendSetCarriedItem 发送快捷栏切换包。
func sendSetCarriedItem(t *testing.T, conn net.Conn, slot int32) {
	t.Helper()
	packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDSetCarriedItem)
	packet = protocol.AppendVarInt(packet, slot)
	if err := protocol.WritePacketWithCompression(conn, packet, compressionThreshold); err != nil {
		t.Fatal(err)
	}
}

// sendSetCreativeSlot 发送创造模式物品栏设置包（无数据组件）。
func sendSetCreativeSlot(t *testing.T, conn net.Conn, slot int16, itemID int32, count int32) {
	t.Helper()
	packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDSetCreativeSlot)
	packet = append(packet, byte(slot>>8), byte(slot))
	packet = protocol.AppendVarInt(packet, count)
	if count > 0 {
		packet = protocol.AppendVarInt(packet, itemID)
		packet = protocol.AppendVarInt(packet, 0) // added components
		packet = protocol.AppendVarInt(packet, 0) // removed components
	}
	if err := protocol.WritePacketWithCompression(conn, packet, compressionThreshold); err != nil {
		t.Fatal(err)
	}
}

// expectBlockUpdate 读取一个方块更新包并校验位置与状态。
func expectBlockUpdate(t *testing.T, conn net.Conn, x, y, z int, state int32) {
	t.Helper()
	payload := expectPlayPacket(t, conn, protocol.PlayPacketIDBlockUpdate)
	_, offset, err := protocol.DecodeVarInt(payload)
	if err != nil {
		t.Fatal(err)
	}
	position, offset, err := protocol.DecodeInt64(payload, offset)
	if err != nil {
		t.Fatal(err)
	}
	px, py, pz := protocol.UnpackPosition(position)
	if px != x || py != y || pz != z {
		t.Fatalf("方块更新位置 = (%d,%d,%d)，want (%d,%d,%d)", px, py, pz, x, y, z)
	}
	got, _, err := protocol.DecodeVarInt(payload[offset:])
	if err != nil || got != state {
		t.Fatalf("方块更新状态 = %d (err=%v)，want %d", got, err, state)
	}
}

// findDigTarget 在出生点附近寻找一个可破坏的方块列（返回顶面方块坐标）。
func findDigTarget(t *testing.T, instance *Server) (int, int, int) {
	t.Helper()
	return digTargetNearSpawn(t, instance)
}

// TestPlacementPushesPlayerUp 验证在玩家脚下放置方块时玩家被推到方块顶面
// （原版 pushEntitiesUp 行为），不会嵌入方块而被反穿墙校验反复回拉。
func TestPlacementPushesPlayerUp(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:stone*64"}
	instance, conn := joinServer(t, cfg, "Stacker")
	player := findSession(t, instance, "Stacker")
	x, y, z, _, _ := player.playerPosition()
	baseX, baseY, baseZ := int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))

	// 往玩家脚下的格子放石头（点击下方方块的顶面）。
	sendUseItemOn(t, conn, baseX, baseY-1, baseZ, 1)
	expectPlayPacket(t, conn, protocol.PlayPacketIDBlockUpdate)
	if got := instance.testWorld().BlockAt(baseX, baseY, baseZ); got != world.StoneBlock {
		t.Fatalf("placed block = %d, want stone", got)
	}
	// 玩家应被推到石头顶面（y = baseY+1），水平位置不变。
	// 位置包在方块更新之后发送，等待它到达再断言服务器侧位置（避免与推起
	// 执行时序竞争）。
	expectPlayPacket(t, conn, protocol.PlayPacketIDSynchronizePlayerPos)
	_, gotY, _, _, _ := player.playerPosition()
	if math.Abs(gotY-float64(baseY+1)) > 1e-9 {
		t.Fatalf("player y = %v, want %v", gotY, baseY+1)
	}
	// 推起后的移动不应被回拉（不会卡在方块里）。/list 往返作为处理屏障。
	sendPlayerPosition(t, conn, x+1, float64(baseY+1), z, true)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")
	gotX, _, gotZ, _, _ := player.playerPosition()
	if math.Abs(gotX-(x+1)) > 1e-9 || math.Abs(gotZ-z) > 1e-9 {
		t.Fatalf("move rejected after push-up: (%v,%v), want (%v,%v)", gotX, gotZ, x+1, z)
	}
}

// TestCreativeBlockBreakAndPlace 验证创意模式的破坏与放置：
// 世界体素被修改、附近玩家收到 Block Update、破坏与放置的距离校验生效。
func TestCreativeBlockBreakAndPlace(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.GameMode = "creative"
	instance, conn := joinServer(t, cfg, "Builder")

	x, y, z := findDigTarget(t, instance)
	original := instance.testWorld().BlockAt(x, y, z)

	// 破坏：创意模式在 START_DESTROY_BLOCK 立即生效。
	sendPlayerAction(t, conn, 0, x, y, z, 1)
	expectBlockUpdate(t, conn, x, y, z, int32(world.AirBlock))
	if got := instance.testWorld().BlockAt(x, y, z); got != world.AirBlock {
		t.Fatalf("破坏后方块 = %d, want 空气", got)
	}

	// 放置：点击下方方块的顶面，把石头放回刚挖掉的位置（快捷栏 0 槽的初始石头）。
	sendUseItemOn(t, conn, x, y-1, z, 1)
	expectBlockUpdate(t, conn, x, y, z, int32(world.StoneBlock))
	if got := instance.testWorld().BlockAt(x, y, z); got != world.StoneBlock {
		t.Fatalf("放置后方块 = %d, want 石头（原方块 %d）", got, original)
	}

	// 距离校验：远处的破坏请求被忽略。
	fx, fy, fz := x+100, y, z+100
	before := instance.testWorld().BlockAt(fx, fy, fz)
	sendPlayerAction(t, conn, 0, fx, fy, fz, 1)
	if got := instance.testWorld().BlockAt(fx, fy, fz); got != before {
		t.Fatal("超出距离的破坏不应生效")
	}
}

// TestCreativeInventoryAndPlace 验证创造模式物品栏设置（Set Creative Mode Slot）
// 后可以放置对应方块。
func TestCreativeInventoryAndPlace(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.GameMode = "creative"
	cfg.StartingItems = nil // 清空初始物品，只用创造物品栏提供的物品
	instance, conn := joinServer(t, cfg, "Builder")

	// 把泥土放进快捷栏 2 槽并切换到该槽位。
	dirtID, err := registry.ItemID("minecraft:dirt")
	if err != nil {
		t.Fatal(err)
	}
	sendSetCreativeSlot(t, conn, 2, dirtID, 64)
	sendSetCarriedItem(t, conn, 2)

	x, y, z := findDigTarget(t, instance)
	sendPlayerAction(t, conn, 0, x, y, z, 1) // 先清出位置
	expectBlockUpdate(t, conn, x, y, z, int32(world.AirBlock))

	sendUseItemOn(t, conn, x, y-1, z, 1)
	expectBlockUpdate(t, conn, x, y, z, int32(world.DirtBlock))
	if got := instance.testWorld().BlockAt(x, y, z); got != world.DirtBlock {
		t.Fatalf("放置后方块 = %d, want 泥土", got)
	}
}

// TestSurvivalBlockPlaceConsumesItem 验证生存模式放置会消耗物品
// （初始石头 ×1，第二次放置因物品耗尽被忽略）。
func TestSurvivalBlockPlaceConsumesItem(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:stone*1"}
	instance, conn := joinServer(t, cfg, "Miner")

	x, y, z := findDigTarget(t, instance)
	// 生存模式：客户端完成挖掘后提交 STOP_DESTROY_BLOCK（状态 2）。
	sendPlayerAction(t, conn, 2, x, y, z, 1)
	expectBlockUpdate(t, conn, x, y, z, int32(world.AirBlock))

	sendUseItemOn(t, conn, x, y-1, z, 1)
	expectBlockUpdate(t, conn, x, y, z, int32(world.StoneBlock))
	// 槽位同步（数量减为 0 → 空槽位）。
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetPlayerInventory)

	// 第二个目标列：物品已耗尽，放置不应生效。
	x2, y2, z2 := 0, 0, 0
	for _, cx := range []int{x + 1, x + 2, x + 3} {
		if state, cy, ok := instance.testWorld().TopBlock(cx, z); ok && state != world.WaterBlock {
			x2, y2, z2 = cx, cy, z
			break
		}
	}
	if x2 == 0 && y2 == 0 && z2 == 0 {
		t.Fatal("找不到第二个测试目标")
	}
	sendPlayerAction(t, conn, 2, x2, y2, z2, 1)
	expectBlockUpdate(t, conn, x2, y2, z2, int32(world.AirBlock))
	sendUseItemOn(t, conn, x2, y2-1, z2, 1)
	if got := instance.testWorld().BlockAt(x2, y2, z2); got != world.AirBlock {
		t.Fatalf("物品耗尽后不应能放置方块（方块 = %d）", got)
	}
}
