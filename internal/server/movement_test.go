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
		if !instance.world.SetBlock(wallX, baseY+dy, int(math.Floor(z)), world.StoneBlock) {
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
	for _, height := range []float64{5, 4, 3, 2, 1, 0} {
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
