//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package world

import "testing"

// testMatchBlock 是测试用的“地形中不会出现”的方块状态（TNT），
// 用于验证按方块状态扫描只命中显式写入的位置。
var testMatchBlock = mustBlockState("minecraft:tnt")

// TestChunkLoadTracking 验证区块加载跟踪：开启后新加载/生成的区块被记录，
// 取走后清空；未开启跟踪时不记录；已缓存的区块不重复记录。
func TestChunkLoadTracking(t *testing.T) {
	dir := t.TempDir()
	instance, err := Open(dir, SeededGenerator{Seed: 7})
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()

	// 未开启跟踪：不记录任何区块。
	if _, err := instance.Chunk(0, 0); err != nil {
		t.Fatal(err)
	}
	if got := instance.TakeLoadedChunks(); got != nil {
		t.Fatalf("未开启跟踪时 TakeLoadedChunks() = %v, want nil", got)
	}

	instance.EnableChunkLoadTracking()
	for x := 0; x < 3; x++ {
		for z := 0; z < 2; z++ {
			if _, err := instance.Chunk(x, z); err != nil {
				t.Fatal(err)
			}
		}
	}
	// 已缓存的区块不会重复记录（Chunk 命中缓存直接返回）。
	if _, err := instance.Chunk(1, 1); err != nil {
		t.Fatal(err)
	}
	loaded := instance.TakeLoadedChunks()
	if len(loaded) != 5 {
		t.Fatalf("TakeLoadedChunks() 返回 %d 个区块，want 5（(0,0) 在开启跟踪前已缓存）", len(loaded))
	}
	if got := instance.TakeLoadedChunks(); got != nil {
		t.Fatalf("重复取走时应为空，得到 %v", got)
	}
	seen := make(map[ChunkPos]bool, len(loaded))
	for _, pos := range loaded {
		seen[pos] = true
	}
	for x := 0; x < 3; x++ {
		for z := 0; z < 2; z++ {
			pos := ChunkPos{X: x, Z: z}
			if (x == 0 && z == 0) == seen[pos] {
				t.Fatalf("区块 (%d,%d) 的记录状态不符合预期（seen=%v）", x, z, seen[pos])
			}
		}
	}
}

// TestChunkBlockStatePositions 验证单区块方块状态扫描：只返回该区块内匹配的
// 坐标；区块不在内存缓存中时返回 nil（不触发加载）。
func TestChunkBlockStatePositions(t *testing.T) {
	dir := t.TempDir()
	instance, err := Open(dir, SeededGenerator{Seed: 11})
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()

	if _, err := instance.Chunk(0, 0); err != nil {
		t.Fatal(err)
	}
	if !instance.SetBlock(3, 0, 5, testMatchBlock) {
		t.Fatal("SetBlock(匹配方块) 失败")
	}
	// 另一个区块内的同种方块不应出现在本区块的结果里。
	if !instance.SetBlock(SectionSize+1, 0, 5, testMatchBlock) {
		t.Fatal("SetBlock(匹配方块, 相邻区块) 失败")
	}

	match := func(state uint16) bool { return state == testMatchBlock }
	found := instance.ChunkBlockStatePositions(0, 0, match)
	if len(found) != 1 || found[0] != [3]int{3, 0, 5} {
		t.Fatalf("ChunkBlockStatePositions(0,0) = %v, want [[3 0 5]]", found)
	}
	found = instance.ChunkBlockStatePositions(1, 0, match)
	if len(found) != 1 || found[0] != [3]int{SectionSize + 1, 0, 5} {
		t.Fatalf("ChunkBlockStatePositions(1,0) = %v, want [[%d 0 5]]", found, SectionSize+1)
	}
	// 未加载的区块：返回 nil，且不把它加载进缓存。
	before := instance.ChunkCount()
	if got := instance.ChunkBlockStatePositions(50, 50, match); got != nil {
		t.Fatalf("未加载区块应返回 nil，得到 %v", got)
	}
	if after := instance.ChunkCount(); after != before {
		t.Fatalf("扫描不应加载区块：ChunkCount %d -> %d", before, after)
	}
}
