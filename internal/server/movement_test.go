package server

import (
	"math"
	"net"
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
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
