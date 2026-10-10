package server

import (
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/protocol"

	"gmcs/internal/world"
)

// TestChunkStreamingOnMovement 验证玩家跨越区块边界后服务器增量发送新区块
// （Set Center Chunk → Chunk Data）并卸载超出视距的旧区块（Forget Level Chunk）。
func TestChunkStreamingOnMovement(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Walker")
	_, _, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)

	// 移动到区块 (10, 0)：距原中心 10 > 视距 2+2，区块 (0,0) 等应被卸载。
	// 逐 tick 速度上限为 10 格/包，这里按 8 格一步分步移动。
	walkTo(t, conn, instance, 10*16+0.5, spawnZ)

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

// TestChunkUnloadFarFromPlayers 验证远离所有玩家的区块会从世界内存缓存中卸载
// （长时间跑图后内存占用保持有界），玩家附近的区块保留，
// 且被卸载的区块可以正常重新加载。
func TestChunkUnloadFarFromPlayers(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Explorer")

	nearCount := instance.testWorld().ChunkCount()
	if nearCount == 0 {
		t.Fatal("玩家附近应已加载区块")
	}

	// 模拟跑图：加载一组远离出生点的区块（如玩家绕了一圈后离开）。
	for i := 0; i < 500; i++ {
		if _, err := instance.testWorld().Chunk(100+i%50, 100+i/50); err != nil {
			t.Fatal(err)
		}
	}
	if got := instance.testWorld().ChunkCount(); got != nearCount+500 {
		t.Fatalf("加载跑图区块后 ChunkCount() = %d, want %d", got, nearCount+500)
	}

	if err := instance.unloadFarChunks(); err != nil {
		t.Fatal(err)
	}
	if got := instance.testWorld().ChunkCount(); got != nearCount {
		t.Fatalf("卸载后 ChunkCount() = %d, want %d（跑图区块应被移出内存）", got, nearCount)
	}

	// 被卸载的区块在再次访问时应能从磁盘恢复。
	chunk, err := instance.testWorld().Chunk(100, 100)
	if err != nil {
		t.Fatal(err)
	}
	if chunk == nil {
		t.Fatal("重新加载被卸载的区块返回 nil")
	}
}
