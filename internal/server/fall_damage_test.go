package server

import (
	"net"
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
)

// sendPlayerPosition 发送 Player Position 包（含着地标志）。
func sendPlayerPosition(t *testing.T, conn net.Conn, x, y, z float64, onGround bool) {
	t.Helper()
	packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDPlayerPosition)
	packet = protocol.AppendFloat64(packet, x)
	packet = protocol.AppendFloat64(packet, y)
	packet = protocol.AppendFloat64(packet, z)
	var flags byte
	if onGround {
		flags = 0x01
	}
	packet = append(packet, flags)
	if err := protocol.WritePacketWithCompression(conn, packet, compressionThreshold); err != nil {
		t.Fatal(err)
	}
}

// TestFallDamage 验证下落超过 3 格后落地受到摔落伤害（每多 1 格 1 点），
// 短距离下落不受伤害。
func TestFallDamage(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Faller")
	player := findSession(t, instance, "Faller")
	spawnX, spawnY, spawnZ := instance.spawnPosition()

	// 站立在出生点（着地）。
	sendPlayerPosition(t, conn, spawnX, spawnY, spawnZ, true)

	// 从 6 格高处下落：伤害 = ceil(6-3) = 3。
	sendPlayerPosition(t, conn, spawnX, spawnY+6, spawnZ, false)
	sendPlayerPosition(t, conn, spawnX, spawnY, spawnZ, true)
	expectPlayPacket(t, conn, protocol.PlayPacketIDDamageEvent)
	healthPacket := expectPlayPacket(t, conn, protocol.PlayPacketIDUpdateHealth)
	_, offset, err := protocol.DecodeVarInt(healthPacket)
	if err != nil {
		t.Fatal(err)
	}
	health, _, err := protocol.DecodeFloat32(healthPacket, offset)
	if err != nil || health != maxPlayerHealth-3 {
		t.Fatalf("health after fall = %v (err=%v), want %v", health, err, maxPlayerHealth-3)
	}
	expectPlayPacket(t, conn, protocol.PlayPacketIDSoundEffect)

	// 从 2 格高处下落：无伤害（用 /list 响应确保服务器已处理完移动包）。
	sendPlayerPosition(t, conn, spawnX, spawnY+2, spawnZ, false)
	sendPlayerPosition(t, conn, spawnX, spawnY, spawnZ, true)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")
	if remaining, _, _ := player.healthStatus(); remaining != maxPlayerHealth-3 {
		t.Fatalf("health after short fall = %v, want %v", remaining, maxPlayerHealth-3)
	}
}
