package world

import "testing"

// TestUnloadFarUnloadsDistantChunks 验证远离中心点的区块被移出内存缓存，
// 缓存数量保持有界，且被卸载的区块可从磁盘恢复出与生成器一致的内容。
func TestUnloadFarUnloadsDistantChunks(t *testing.T) {
	dir := t.TempDir()
	instance, err := Open(dir, SeededGenerator{Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()

	const size = 7
	for x := 0; x < size; x++ {
		for z := 0; z < size; z++ {
			if _, err := instance.Chunk(x, z); err != nil {
				t.Fatal(err)
			}
		}
	}
	if got := instance.ChunkCount(); got != size*size {
		t.Fatalf("ChunkCount() = %d, want %d", got, size*size)
	}

	unloaded, err := instance.UnloadFar([]ChunkPos{{X: 3, Z: 3}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if want := size*size - 9; unloaded != want {
		t.Fatalf("UnloadFar() = %d, want %d", unloaded, want)
	}
	if got := instance.ChunkCount(); got != 9 {
		t.Fatalf("卸载后 ChunkCount() = %d, want 9", got)
	}

	// 重新访问被卸载的区块：内容应与生成器一致（从磁盘恢复）。
	reloaded, err := instance.Chunk(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	generated := SeededGenerator{Seed: 1}.GenerateChunk(0, 0)
	for x := 0; x < SectionSize; x += 5 {
		top, y, ok := generated.TopBlock(x, x)
		if !ok {
			continue
		}
		if got := reloaded.GetBlockState(x, y, x); got != top {
			t.Fatalf("区块 (0,0) 恢复后方块 (%d,%d,%d) = %d, want %d", x, y, x, got, top)
		}
	}
}

// TestUnloadFarSavesDirtyChunks 验证带修改的区块在卸载前写盘，不会丢数据。
func TestUnloadFarSavesDirtyChunks(t *testing.T) {
	dir := t.TempDir()
	instance, err := Open(dir, SeededGenerator{Seed: 7})
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()

	chunk, err := instance.Chunk(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	const y = 64
	chunk.SetBlockState(0, y, 0, WaterBlock)
	instance.MarkDirty(chunk)

	unloaded, err := instance.UnloadFar([]ChunkPos{{X: 50, Z: 50}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if unloaded != 1 {
		t.Fatalf("UnloadFar() = %d, want 1", unloaded)
	}
	if got := instance.ChunkCount(); got != 0 {
		t.Fatalf("ChunkCount() = %d, want 0", got)
	}

	// 绕开内存缓存与生成器，直接从磁盘读回并校验修改仍在。
	saved, err := LoadChunk(dir, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if saved == nil {
		t.Fatal("卸载时未把脏区块写入磁盘")
	}
	if got := saved.GetBlockState(0, y, 0); got != WaterBlock {
		t.Fatalf("磁盘上的方块 = %d, want %d", got, WaterBlock)
	}
}

// TestUnloadFarWithoutCentersIsNoop 验证没有中心点（没有玩家）时不卸载任何区块。
func TestUnloadFarWithoutCentersIsNoop(t *testing.T) {
	dir := t.TempDir()
	instance, err := Open(dir, SeededGenerator{Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()

	if _, err := instance.Chunk(0, 0); err != nil {
		t.Fatal(err)
	}
	if unloaded, err := instance.UnloadFar(nil, 4); err != nil || unloaded != 0 {
		t.Fatalf("UnloadFar(nil) = (%d, %v), want (0, nil)", unloaded, err)
	}
	if got := instance.ChunkCount(); got != 1 {
		t.Fatalf("ChunkCount() = %d, want 1", got)
	}
}
