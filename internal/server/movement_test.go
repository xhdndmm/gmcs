package server

import (
	"math"
	"net"
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
	"gmcs/internal/world"
)

// expectResyncPosition 读取一个 Synchronize Player Position 包并返回其中的坐标
// （服务器用于拒绝非法移动的回拉包）。
func expectResyncPosition(t *testing.T, conn net.Conn) (float64, float64, float64) {
	t.Helper()
	payload := expectPlayPacket(t, conn, protocol.PlayPacketIDSynchronizePlayerPos)
	_, offset, err := protocol.DecodeVarInt(payload) // 包 ID
	if err != nil {
		t.Fatal(err)
	}
	_, size, err := protocol.DecodeVarInt(payload[offset:]) // 传送 ID（相对偏移需累加）
	if err != nil {
		t.Fatal(err)
	}
	offset += size
	x, offset, err := protocol.DecodeFloat64(payload, offset)
	if err != nil {
		t.Fatal(err)
	}
	y, offset, err := protocol.DecodeFloat64(payload, offset)
	if err != nil {
		t.Fatal(err)
	}
	z, _, err := protocol.DecodeFloat64(payload, offset)
	if err != nil {
		t.Fatal(err)
	}
	return x, y, z
}

// TestMoveDistanceValidation 验证服务器拒绝单包位移超过 100 格的位置包：
// 玩家坐标保持在上一次合法位置，并收到回拉包；正常幅度移动照常生效。
func TestMoveDistanceValidation(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Runner")
	player := findSession(t, instance, "Runner")
	x, y, z, _, _ := player.playerPosition()

	// 正常移动（1 格）被接受：随后发送的超距移动会被回拉到这个位置。
	if err := protocol.WritePacketWithCompression(conn,
		encodePlayerPosition(x+1, y, z, true), compressionThreshold); err != nil {
		t.Fatal(err)
	}
	far := x + maxMoveDistance + 50
	if err := protocol.WritePacketWithCompression(conn,
		encodePlayerPosition(far, y, z, true), compressionThreshold); err != nil {
		t.Fatal(err)
	}
	rx, ry, rz := expectResyncPosition(t, conn)
	if rx != x+1 || ry != y || rz != z {
		t.Fatalf("回拉位置 = (%.3f, %.3f, %.3f)，want (%.3f, %.3f, %.3f)", rx, ry, rz, x+1, y, z)
	}
	px, py, pz, _, _ := player.playerPosition()
	if math.Abs(px-(x+1)) > 1e-9 || math.Abs(py-y) > 1e-9 || math.Abs(pz-z) > 1e-9 {
		t.Fatalf("服务器坐标 = (%.3f, %.3f, %.3f)，want (%.3f, %.3f, %.3f)", px, py, pz, x+1, y, z)
	}

	// 垂直方向的超距位移同样被拒绝。
	if err := protocol.WritePacketWithCompression(conn,
		encodePlayerPosition(x+1, y+maxMoveDistance+50, z, true), compressionThreshold); err != nil {
		t.Fatal(err)
	}
	expectResyncPosition(t, conn)
	if _, py, _, _, _ = player.playerPosition(); math.Abs(py-y) > 1e-9 {
		t.Fatalf("垂直超距位移后 y = %.3f，want %.3f", py, y)
	}
}

// encodePlayerPosition 编码客户端 Player Position 包。
func encodePlayerPosition(x, y, z float64, onGround bool) []byte {
	packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDPlayerPosition)
	packet = protocol.AppendFloat64(packet, x)
	packet = protocol.AppendFloat64(packet, y)
	packet = protocol.AppendFloat64(packet, z)
	var flags byte
	if onGround {
		flags = 0x01
	}
	return append(packet, flags)
}

// TestMoveSpeedLimit 验证逐 tick 速度上限：单包位移在 100 格硬上限内、
// 但超过 10 格/tick 的移动会被拒绝并回拉；正常速度的移动不受影响。
func TestMoveSpeedLimit(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Sprinter")
	player := findSession(t, instance, "Sprinter")
	x, y, z, _, _ := player.playerPosition()

	// 20 格/包（< 100 硬上限，> 10 格/tick）：拒绝。
	if err := protocol.WritePacketWithCompression(conn,
		encodePlayerPosition(x+20, y, z, true), compressionThreshold); err != nil {
		t.Fatal(err)
	}
	rx, _, _ := expectResyncPosition(t, conn)
	if math.Abs(rx-x) > 1e-9 {
		t.Fatalf("超速移动后回拉位置 x = %.3f, want %.3f", rx, x)
	}

	// 8 格/包（< 10 格/tick）：接受（贴合地面的合法位置）。
	sendGroundMove(t, conn, instance, x+8, z)
	waitFor(t, func() bool {
		px, _, _, _, _ := player.playerPosition()
		return math.Abs(px-(x+8)) < 1e-9
	}, "正常速度移动应被接受")
}

// TestMoveIntoBlockRejected 验证穿墙检测：终点嵌在固体方块中的移动被拒绝。
func TestMoveIntoBlockRejected(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.GameMode = "creative"
	instance, conn := joinServer(t, cfg, "Clipper")
	player := findSession(t, instance, "Clipper")
	x, y, z, _, _ := player.playerPosition()

	// 在玩家东侧堆两格方块（与身体同高），形成一个“墙”。
	wallX := int(math.Floor(x)) + 1
	baseY := int(math.Floor(y))
	for _, dy := range []int{0, 1} {
		if !instance.testWorld().SetBlock(wallX, baseY+dy, int(math.Floor(z)), world.StoneBlock) {
			t.Fatal("无法设置测试方块")
		}
	}

	// 发送一个“嵌进墙里”的位置：终点位于墙方块内部。
	if err := protocol.WritePacketWithCompression(conn,
		encodePlayerPosition(float64(wallX)+0.5, y, z, true), compressionThreshold); err != nil {
		t.Fatal(err)
	}
	rx, _, _ := expectResyncPosition(t, conn)
	if math.Abs(rx-x) > 1e-9 {
		t.Fatalf("穿墙移动后回拉位置 x = %.3f, want %.3f", rx, x)
	}
}

// TestFallDamageFromServerPhysics 验证摔落伤害改由服务器端地面检测驱动：
// 即使客户端始终上报“未着地”，服务器也会在落到地面时结算伤害；
// 落入水中则不结算（水域中断下落）。
func TestFallDamageFromServerPhysics(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Diver")
	player := findSession(t, instance, "Diver")
	x, y, z, _, _ := player.playerPosition()

	// 从 5 格高处“下落”，客户端始终上报 onGround=false。
	// 起点高度直接写入服务器状态（正常客户端无法一包上升 5 格，
	// 会被悬空上升限制拒绝）。/list 往返作为屏障，避免与读循环并发。
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")
	player.setPlayerPosition(x, y+5, z, 0, 0)
	player.updateFallState(player.playerWorld(), x, y+5, z)
	for _, height := range []float64{4, 3, 2, 1, 0} {
		if err := protocol.WritePacketWithCompression(conn,
			encodePlayerPosition(x, y+height, z, false), compressionThreshold); err != nil {
			t.Fatal(err)
		}
	}
	// 落地（高度 0）时服务器应结算摔落伤害：ceil(5-3) = 2 点。
	healthPacket := expectPlayPacket(t, conn, protocol.PlayPacketIDUpdateHealth)
	_, offset, err := protocol.DecodeVarInt(healthPacket)
	if err != nil {
		t.Fatal(err)
	}
	health, _, err := protocol.DecodeFloat32(healthPacket, offset)
	if err != nil || health != maxPlayerHealth-2 {
		t.Fatalf("摔落伤害后生命 = %v (err=%v), want %v", health, err, maxPlayerHealth-2)
	}
}

// TestSneakEdgeProtection 验证潜行边缘保护：潜行时不会走出支撑面边缘
// （完全无支撑时保持原位；部分无支撑时沿边缘滑动）。
func TestSneakEdgeProtection(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Sneaker")
	player := findSession(t, instance, "Sneaker")
	spawnX, spawnY, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	baseX, baseY, baseZ := int(math.Floor(spawnX)), int(math.Floor(spawnY)), int(math.Floor(spawnZ))

	// 铺平 3×3 石平台（Y=baseY），玩家站在平台中央顶面（baseY+1）。
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			instance.testWorld().SetBlock(baseX+dx, baseY, baseZ+dz, world.StoneBlock)
			for dy := 1; dy <= 2; dy++ {
				instance.testWorld().SetBlock(baseX+dx, baseY+dy, baseZ+dz, world.AirBlock)
			}
		}
	}
	// /list 往返作为屏障：排空读循环后再直接写会话状态。
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")
	player.setPlayerPosition(spawnX, float64(baseY+1), spawnZ, 0, 0)
	player.resetFallState()
	sendPlayerInput(t, conn, protocol.PlayerInputShift)
	// 向平台外直线移动：完全无支撑，应保持原位。
	sendPlayerPosition(t, conn, spawnX+2, float64(baseY+1), spawnZ, true)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")
	gotX, gotY, gotZ, _, _ := player.playerPosition()
	if math.Abs(gotX-spawnX) > 1e-9 || math.Abs(gotZ-spawnZ) > 1e-9 || math.Abs(gotY-float64(baseY+1)) > 1e-9 {
		t.Fatalf("潜行走出平台：(%v,%v,%v), want (%v,%v,%v)", gotX, gotY, gotZ, spawnX, baseY+1, spawnZ)
	}

	// 对角移动：只保留有支撑的分量（沿边缘滑动）。
	sendPlayerPosition(t, conn, spawnX+2, float64(baseY+1), spawnZ+0.5, true)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")
	gotX, _, gotZ, _, _ = player.playerPosition()
	if math.Abs(gotX-spawnX) > 1e-9 {
		t.Fatalf("潜行滑出平台（x）：%v, want %v", gotX, spawnX)
	}
	if math.Abs(gotZ-(spawnZ+0.5)) > 1e-9 {
		t.Fatalf("潜行沿边缘滑动被拒：%v, want %v", gotZ, spawnZ+0.5)
	}
}

// TestJumpRiseLimit 验证悬空上升限制（防飞行）：一次腾空累计上升超过
// maxJumpRise 后被拒绝并回拉；正常跳跃弧不受影响。
func TestJumpRiseLimit(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Flyer")
	player := findSession(t, instance, "Flyer")
	x, y, z, _, _ := player.playerPosition()
	baseX, baseY, baseZ := int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))
	// 清空起跳通道上方的装饰/树木，保证上升路径无碰撞。
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			for dy := 1; dy <= 3; dy++ {
				instance.testWorld().SetBlock(baseX+dx, baseY+dy, baseZ+dz, world.AirBlock)
			}
		}
	}

	// 原版跳跃弧（初速度 0.42、重力 0.08、阻力 0.98）的累计上升：
	// 0.42 → 0.7532 → 1.0013 → 1.1661 → 1.2492 → 1.2522（弧顶）。
	// 全程都必须被接受：旧上限 1.2 会拒绝弧顶附近的位置包并回拉。
	apex := 0.0
	for _, dy := range []float64{0.42, 0.7532, 1.001336, 1.166109, 1.249187, 1.252223} {
		sendPlayerPosition(t, conn, x, y+dy, z, false)
		apex = dy
	}
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")
	if _, gotY, _, _, _ := player.playerPosition(); math.Abs(gotY-(y+apex)) > 1e-9 {
		t.Fatalf("原版跳跃弧被拒绝：%v, want %v", gotY, y+apex)
	}

	// 超过预算的持续上升（悬停作弊）：被拒绝并回拉。
	sendPlayerPosition(t, conn, x, y+apex+0.3, z, false)
	expectResyncPosition(t, conn)
	_, gotY, _, _, _ := player.playerPosition()
	if gotY > y+maxJumpRise+1e-9 {
		t.Fatalf("飞行上升未被限制：%v", gotY)
	}
}
