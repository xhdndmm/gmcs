package server

import (
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
)

// TestChunkBatchOnMove 验证跨区块移动时新发送的区块被 Chunk Batch
// Start/Finished 包裹，且批大小等于新发送的区块数。
func TestChunkBatchOnMove(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "BatchMover")
	player := findSession(t, instance, "BatchMover")
	x, y, z, _, _ := player.playerPosition()

	// 向东移动 16 格跨一个区块：视距 2 的网格新增一列 5 个区块。
	sendPlayerPosition(t, conn, x+16, y, z, true)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetCenterChunk)
	expectPlayPacket(t, conn, protocol.PlayPacketIDChunkBatchStart)
	for i := 0; i < 5; i++ {
		expectPlayPacket(t, conn, protocol.PlayPacketIDChunkData)
	}
	finished := expectPlayPacket(t, conn, protocol.PlayPacketIDChunkBatchFinished)
	_, offset, err := protocol.DecodeVarInt(finished)
	if err != nil {
		t.Fatal(err)
	}
	size, _, err := protocol.DecodeVarInt(finished[offset:])
	if err != nil || size != 5 {
		t.Fatalf("batch size = %d (err=%v), want 5", size, err)
	}
}
