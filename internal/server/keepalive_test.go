//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
)

// TestUnsolicitedKeepAliveDisconnects 验证未请求的 Keep Alive（没有待回应的
// 挑战 ID）会按原版断开连接，而不是静默忽略。
func TestUnsolicitedKeepAliveDisconnects(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	_, conn := joinServer(t, cfg, "Unsolicited")

	packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDKeepAlive)
	packet = protocol.AppendInt64(packet, 12345)
	if err := protocol.WritePacketWithCompression(conn, packet, compressionThreshold); err != nil {
		t.Fatal(err)
	}
	if got := expectKickComponent(t, conn); got != "disconnect.timeout" {
		t.Fatalf("断开消息 = %q, want disconnect.timeout", got)
	}
}
