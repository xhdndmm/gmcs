package server

import (
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
)

// TestChunkStreamingOnMovement 验证玩家跨越区块边界后服务器增量发送新区块
// （Set Center Chunk → Chunk Data）并卸载超出视距的旧区块（Forget Level Chunk）。
func TestChunkStreamingOnMovement(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Walker")
	_, spawnY, spawnZ := instance.spawnPosition()

	// 移动到区块 (10, 0)：距原中心 10 > 视距 2+2，区块 (0,0) 等应被卸载。
	packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDPlayerPosition)
	packet = protocol.AppendFloat64(packet, 10*16+0.5)
	packet = protocol.AppendFloat64(packet, spawnY)
	packet = protocol.AppendFloat64(packet, spawnZ)
	packet = protocol.AppendBool(packet, true)
	if err := protocol.WritePacketWithCompression(conn, packet, compressionThreshold); err != nil {
		t.Fatal(err)
	}

	expectPlayPacket(t, conn, protocol.PlayPacketIDSetCenterChunk)
	expectPlayPacket(t, conn, protocol.PlayPacketIDChunkData)
	unload := expectPlayPacket(t, conn, protocol.PlayPacketIDForgetLevelChunk)
	// Forget Level Chunk 的字段顺序为 Z、X。
	_, offset, err := protocol.DecodeVarInt(unload)
	if err != nil {
		t.Fatal(err)
	}
	z, offset, err := protocol.DecodeInt32(unload, offset)
	if err != nil {
		t.Fatal(err)
	}
	x, _, err := protocol.DecodeInt32(unload, offset)
	if err != nil {
		t.Fatal(err)
	}
	if absInt(int(x-10)) <= 4 && absInt(int(z)) <= 4 {
		t.Fatalf("unloaded chunk (%d,%d) is still within the view limit", x, z)
	}
}
