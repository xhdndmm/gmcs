package world

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestChunkPayloadRoundTrip(t *testing.T) {
	chunk := FlatGenerator{}.GenerateChunk(-5, 7)
	chunk.SetBlockState(1, WorldMinY+100, 2, StoneBlock)
	chunk.SetSectionBiome(3, 42)

	payload := encodeChunkPayload(chunk)
	decoded, err := decodeChunkPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.X != -5 || decoded.Z != 7 {
		t.Fatalf("decoded position %d,%d", decoded.X, decoded.Z)
	}
	if got := decoded.GetBlockState(0, WorldMinY, 0); got != BedrockBlock {
		t.Fatalf("bedrock lost: %d", got)
	}
	if got := decoded.GetBlockState(15, FlatGroundLevel, 15); got != GrassBlock {
		t.Fatalf("grass lost: %d", got)
	}
	if got := decoded.GetBlockState(1, WorldMinY+100, 2); got != StoneBlock {
		t.Fatalf("stone lost: %d", got)
	}
	if got := decoded.GetBlockState(1, WorldMinY+101, 2); got != AirBlock {
		t.Fatalf("unexpected block: %d", got)
	}
	if got := decoded.SectionBiome(3); got != 42 {
		t.Fatalf("biome lost: %d", got)
	}
	if got := decoded.SectionBiome(2); got != BiomePlains {
		t.Fatalf("unexpected biome: %d", got)
	}
}

func TestDecodeChunkPayloadRejectsCorrupt(t *testing.T) {
	valid := encodeChunkPayload(NewChunk(0, 0))

	badVersion := append([]byte{}, valid...)
	badVersion[5] = 99

	cases := map[string][]byte{
		"empty":       nil,
		"bad magic":   append([]byte("XXXX"), valid[4:]...),
		"truncated":   valid[:len(valid)-1],
		"trailing":    append(append([]byte{}, valid...), 0x00),
		"bad version": badVersion,
	}
	for name, payload := range cases {
		if _, err := decodeChunkPayload(payload); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestSaveAndLoadChunk(t *testing.T) {
	dir := t.TempDir()
	chunk := FlatGenerator{}.GenerateChunk(3, -9)
	payloads := map[ChunkPos][]byte{{X: 3, Z: -9}: encodeChunkPayload(chunk)}
	if err := SavePayloads(dir, payloads); err != nil {
		t.Fatal(err)
	}
	// 3>>5=0、-9>>5=-1。
	if _, err := os.Stat(filepath.Join(dir, "r.0.-1.mca")); err != nil {
		t.Fatalf("region file missing: %v", err)
	}

	loaded, err := LoadChunk(dir, 3, -9)
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil {
		t.Fatal("expected chunk on disk")
	}
	if got := loaded.GetBlockState(0, FlatGroundLevel, 0); got != GrassBlock {
		t.Fatalf("grass lost: %d", got)
	}

	missing, err := LoadChunk(dir, 4, -9)
	if err != nil || missing != nil {
		t.Fatalf("expected missing chunk, got %v err=%v", missing, err)
	}
}

func TestRegionFileKeepsNeighborChunks(t *testing.T) {
	dir := t.TempDir()
	first := FlatGenerator{}.GenerateChunk(0, 0)
	if err := SavePayloads(dir, map[ChunkPos][]byte{{X: 0, Z: 0}: encodeChunkPayload(first)}); err != nil {
		t.Fatal(err)
	}
	second := NewChunk(1, 1)
	second.SetBlockState(2, WorldMinY+200, 3, StoneBlock)
	if err := SavePayloads(dir, map[ChunkPos][]byte{{X: 1, Z: 1}: encodeChunkPayload(second)}); err != nil {
		t.Fatal(err)
	}

	chunk, err := LoadChunk(dir, 0, 0)
	if err != nil || chunk == nil {
		t.Fatalf("first chunk lost: %v", err)
	}
	if got := chunk.GetBlockState(7, FlatGroundLevel, 7); got != GrassBlock {
		t.Fatalf("first chunk content lost: %d", got)
	}
	chunk2, err := LoadChunk(dir, 1, 1)
	if err != nil || chunk2 == nil {
		t.Fatalf("second chunk lost: %v", err)
	}
	if got := chunk2.GetBlockState(2, WorldMinY+200, 3); got != StoneBlock {
		t.Fatalf("second chunk content lost: %d", got)
	}
}

func TestWorldLifecycle(t *testing.T) {
	dir := t.TempDir()
	w, err := Open(dir, FlatGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	chunk, err := w.Chunk(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := chunk.GetBlockState(0, WorldMinY, 0); got != BedrockBlock {
		t.Fatalf("generated chunk missing terrain: %d", got)
	}
	if got := w.DirtyCount(); got != 1 {
		t.Fatalf("dirty = %d, want 1", got)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := w.DirtyCount(); got != 0 {
		t.Fatalf("dirty after flush = %d, want 0", got)
	}
	// 已保存的区块再次访问不应重新标脏。
	if _, err := w.Chunk(0, 0); err != nil {
		t.Fatal(err)
	}
	if got := w.DirtyCount(); got != 0 {
		t.Fatalf("dirty after reload = %d, want 0", got)
	}

	// 修改并关闭。
	chunk.SetBlockState(5, WorldMinY+50, 5, StoneBlock)
	w.MarkDirty(chunk)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Chunk(1, 1); err == nil {
		t.Fatal("expected error on closed world")
	}

	// 重新打开：应从磁盘加载修改后的区块。
	w2, err := Open(dir, FlatGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w2.Close() }()
	reloaded, err := w2.Chunk(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.GetBlockState(5, WorldMinY+50, 5); got != StoneBlock {
		t.Fatalf("saved modification lost: %d", got)
	}
}

func TestWorldConcurrentChunkAccess(t *testing.T) {
	dir := t.TempDir()
	w, err := Open(dir, FlatGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for j := 0; j < 16; j++ {
				if _, err := w.Chunk(seed%4, j%4); err != nil {
					t.Errorf("chunk: %v", err)
					return
				}
			}
			if err := w.Flush(); err != nil {
				t.Errorf("flush: %v", err)
			}
		}(i)
	}
	wg.Wait()
}

func TestZlibDecompressLimit(t *testing.T) {
	big := bytes.Repeat([]byte{0xAB}, 4096)
	compressed, err := zlibCompress(big)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zlibDecompress(compressed, 1024); err == nil {
		t.Fatal("expected limit error")
	}
	decoded, err := zlibDecompress(compressed, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, big) {
		t.Fatal("round trip mismatch")
	}
}
