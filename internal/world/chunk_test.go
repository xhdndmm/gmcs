package world

import "testing"

func TestChunkSetAndGetBlock(t *testing.T) {
	chunk := NewChunk(0, 0)
	chunk.SetBlock(0, 0, 0, StoneBlock)
	if got := chunk.BlockAt(0, 0, 0); got != StoneBlock {
		t.Fatalf("expected block id %d, got %d", StoneBlock, got)
	}
	if got := chunk.BlockAt(16, 0, 0); got != AirBlock {
		t.Fatalf("expected out-of-bounds block to be air, got %d", got)
	}
}

func TestEncodeChunkDataPacket(t *testing.T) {
	chunk := NewChunk(1, 2)
	chunk.SetBlock(1, 64, 3, GrassBlock)
	encoded := EncodeChunkDataPacket(chunk)
	if len(encoded) == 0 {
		t.Fatal("chunk data packet should not be empty")
	}
}
