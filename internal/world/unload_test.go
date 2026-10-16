//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package world

import (
	"testing"
	"time"
)

// TestUnloadFarGraceKeepsRecentlyFarChunks 验证卸载宽限期：区块首次被判定
// 远离所有玩家后先保留（只记录计时起点），连续远离超过宽限后才真正卸载。
func TestUnloadFarGraceKeepsRecentlyFarChunks(t *testing.T) {
	instance, err := Open(t.TempDir(), SeededGenerator{Seed: 3})
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()

	for x := 0; x < 4; x++ {
		for z := 0; z < 4; z++ {
			if _, err := instance.Chunk(x, z); err != nil {
				t.Fatal(err)
			}
		}
	}
	centers := []ChunkPos{{X: 0, Z: 0}}
	// 宽限取值远大于两次“立即扫描”之间的耗时（微秒级），避免慢机器上
	// 调度延迟造成误判。
	const grace = 500 * time.Millisecond

	// 第一次扫描：远处区块首次判远，进入宽限期（保留）。
	unloaded, err := instance.UnloadFar(centers, 0, grace)
	if err != nil {
		t.Fatal(err)
	}
	if len(unloaded) != 0 {
		t.Fatalf("宽限期内卸载了 %d 个区块，want 0", len(unloaded))
	}
	// 宽限期内再次扫描：仍保留。
	unloaded, err = instance.UnloadFar(centers, 0, grace)
	if err != nil {
		t.Fatal(err)
	}
	if len(unloaded) != 0 {
		t.Fatalf("宽限期内第二次扫描卸载了 %d 个区块，want 0", len(unloaded))
	}

	// 再次扫描前让近处区块重新变近：宽限计时被清零（远处区块不受影响——
	// 用 (0,0) 作中心连续扫描验证语义稳定）。
	if _, err := instance.Chunk(0, 0); err != nil {
		t.Fatal(err)
	}

	// 等待超过宽限后再扫描：远处 15 个区块被卸载，(0,0) 保留。
	time.Sleep(grace + 100*time.Millisecond)
	unloaded, err = instance.UnloadFar(centers, 0, grace)
	if err != nil {
		t.Fatal(err)
	}
	if want := 15; len(unloaded) != want {
		t.Fatalf("超出宽限后卸载 %d 个区块，want %d", len(unloaded), want)
	}
	if got := instance.ChunkCount(); got != 1 {
		t.Fatalf("卸载后 ChunkCount() = %d, want 1", got)
	}
}

// TestUnloadFarGraceResetsWhenNear 验证区块重新变近后宽限计时清零：
// 计时清零后再次远离需要重新计时，不会被旧计时立即卸载。
// 时序设计不依赖“sleep 不能太久”：宽限内的清零与重新计时通过
// “首次判远 → 变近 → 立即再判远”的顺序验证，中间的等待只用于超过宽限。
func TestUnloadFarGraceResetsWhenNear(t *testing.T) {
	instance, err := Open(t.TempDir(), SeededGenerator{Seed: 9})
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()

	for x := 0; x < 2; x++ {
		for z := 0; z < 2; z++ {
			if _, err := instance.Chunk(x, z); err != nil {
				t.Fatal(err)
			}
		}
	}
	const grace = 120 * time.Millisecond

	// 步骤 A：中心 (0,0)。(1,1) 首次判远，开始计时 T_A。
	if _, err := instance.UnloadFar([]ChunkPos{{X: 0, Z: 0}}, 0, grace); err != nil {
		t.Fatal(err)
	}
	// 等待超过宽限：若计时未被清零，后续步骤中 (1,1) 会被判超时。
	time.Sleep(grace + 30*time.Millisecond)

	// 步骤 B：中心移到 (1,1)。它重新变近（计时清零）；持续远离的
	// (0,1)/(1,0) 在本次扫描因旧计时超限被卸载。
	if _, err := instance.UnloadFar([]ChunkPos{{X: 1, Z: 1}}, 0, grace); err != nil {
		t.Fatal(err)
	}

	// 步骤 C：中心立刻回到 (0,0)。刚刚（步骤 B）变近的 (1,1) 计时已清零，
	// 本次只是重新开始计时，必须保留——若仍沿用 T_A 旧计时，这里会被卸载。
	unloaded, err := instance.UnloadFar([]ChunkPos{{X: 0, Z: 0}}, 0, grace)
	if err != nil {
		t.Fatal(err)
	}
	for _, pos := range unloaded {
		if pos == (ChunkPos{X: 1, Z: 1}) {
			t.Fatal("重新变近的区块不应被旧计时卸载（宽限计时未清零）")
		}
	}
	if _, ok := instance.chunks[ChunkPos{X: 1, Z: 1}]; !ok {
		t.Fatal("(1,1) 应保留在内存中")
	}
	if _, ok := instance.chunks[ChunkPos{X: 0, Z: 0}]; !ok {
		t.Fatal("(0,0) 应保留在内存中")
	}
}

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

	unloaded, err := instance.UnloadFar([]ChunkPos{{X: 3, Z: 3}}, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := size*size - 9; len(unloaded) != want {
		t.Fatalf("UnloadFar() 卸载 %d 个，want %d", len(unloaded), want)
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

	unloaded, err := instance.UnloadFar([]ChunkPos{{X: 50, Z: 50}}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(unloaded) != 1 || unloaded[0] != (ChunkPos{X: 0, Z: 0}) {
		t.Fatalf("UnloadFar() = %v, want [（0,0）]", unloaded)
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
	if unloaded, err := instance.UnloadFar(nil, 4, 0); err != nil || len(unloaded) != 0 {
		t.Fatalf("UnloadFar(nil) = (%v, %v), want (nil, nil)", unloaded, err)
	}
	if got := instance.ChunkCount(); got != 1 {
		t.Fatalf("ChunkCount() = %d, want 1", got)
	}
}

// TestCloseSavesDirtyChunks 验证关闭世界会把已生成（待保存）的区块写入磁盘。
func TestCloseSavesDirtyChunks(t *testing.T) {
	dir := t.TempDir()
	instance, err := Open(dir, SeededGenerator{Seed: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := instance.Chunk(2, 2); err != nil {
		t.Fatal(err)
	}
	if err := instance.Close(); err != nil {
		t.Fatal(err)
	}
	saved, err := LoadChunk(dir, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if saved == nil {
		t.Fatal("Close 未保存已生成区块")
	}
}

// TestChunkConcurrentEditAndRead 验证运行时方块修改与区块读取/编码并发安全
// （在 -race 下运行才有意义）。
func TestChunkConcurrentEditAndRead(t *testing.T) {
	dir := t.TempDir()
	instance, err := Open(dir, SeededGenerator{Seed: 21})
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	chunk, err := instance.Chunk(0, 0)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 300; i++ {
			instance.SetBlock(i%16, 64, i%16, StoneBlock)
			instance.SetBlock(i%16, 64, i%16, AirBlock)
			_ = instance.BlockAt(i%16, 64, i%16)
		}
	}()
	for i := 0; i < 300; i++ {
		_ = EncodeChunkDataPacket(chunk)
		_ = encodeChunkPayload(chunk)
	}
	<-done
}

// TestUnloadAllReleasesEveryChunk 验证 UnloadAll 卸载全部区块并保存脏区块
// （服务器没有任何玩家时用其释放区块内存）。
func TestUnloadAllReleasesEveryChunk(t *testing.T) {
	dir := t.TempDir()
	instance, err := Open(dir, SeededGenerator{Seed: 3})
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()

	for i := 0; i < 3; i++ {
		if _, err := instance.Chunk(i, i); err != nil {
			t.Fatal(err)
		}
	}
	chunk, err := instance.Chunk(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	chunk.SetBlockState(0, 64, 0, StoneBlock)
	instance.MarkDirty(chunk)

	unloaded, err := instance.UnloadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(unloaded) != 3 {
		t.Fatalf("UnloadAll() 卸载 %d 个，want 3", len(unloaded))
	}
	if got := instance.ChunkCount(); got != 0 {
		t.Fatalf("ChunkCount() = %d, want 0", got)
	}

	// 脏区块在卸载前写盘，可重新读回。
	saved, err := LoadChunk(dir, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if saved == nil {
		t.Fatal("UnloadAll 未把脏区块写入磁盘")
	}
	if got := saved.GetBlockState(0, 64, 0); got != StoneBlock {
		t.Fatalf("磁盘上的方块 = %d, want %d", got, StoneBlock)
	}
}

// TestReleaseRegionHandles 验证空闲释放会清空区域文件句柄缓存。
func TestReleaseRegionHandles(t *testing.T) {
	dir := t.TempDir()
	instance, err := Open(dir, SeededGenerator{Seed: 5})
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()

	chunk, err := instance.Chunk(2, 2)
	if err != nil {
		t.Fatal(err)
	}
	chunk.SetBlockState(0, 64, 0, StoneBlock)
	instance.MarkDirty(chunk)
	if _, err := instance.UnloadAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadChunk(dir, 2, 2); err != nil {
		t.Fatal(err)
	}
	regionCache.mu.Lock()
	before := regionCache.order.Len()
	regionCache.mu.Unlock()
	if before == 0 {
		t.Fatal("读取区块后应有缓存的区域文件句柄")
	}
	ReleaseRegionHandles()
	regionCache.mu.Lock()
	after := regionCache.order.Len()
	regionCache.mu.Unlock()
	if after != 0 {
		t.Fatalf("ReleaseRegionHandles 后仍有 %d 个句柄缓存", after)
	}
}
