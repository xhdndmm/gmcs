package world

import "testing"

// TestChunkStorageFootprint 断言生成区块的 section 存储足够紧凑。
// 这是确定性计算（不依赖 GC / MemStats），可稳定防止内存回归：
// 旧的“每方块 2 字节数组”实现对每个已分配 section 恒定占用 8 KiB，
// 种子地形单区块约 72 KiB；紧凑存储应远低于此。
//
// 上限说明：加入矿物（含深板岩变体）与更多群系后，部分 section 的调色板
// 超过 16 项而升到 5 位索引（2560 字节/区段）；实测平均约 16.2 KiB/区块，
// 仍远低于旧实现的 72 KiB。
func TestChunkStorageFootprint(t *testing.T) {
	generator := SeededGenerator{Seed: 42}
	const chunks = 64
	total := 0
	for x := 0; x < 8; x++ {
		for z := 0; z < 8; z++ {
			total += retainedSectionBytes(generator.GenerateChunk(x, z))
		}
	}
	average := total / chunks
	t.Logf("种子地形平均每区块 section 存储 = %d 字节", average)
	if average > 18*1024 {
		t.Fatalf("平均每区块 section 存储 %d 字节，超过 18 KiB 上限（内存回归）", average)
	}

	flat := retainedSectionBytes(FlatGenerator{}.GenerateChunk(0, 0))
	t.Logf("超平坦单区块 section 存储 = %d 字节", flat)
	if flat > 4*1024 {
		t.Fatalf("超平坦区块 section 存储 %d 字节，超过 4 KiB 上限（内存回归）", flat)
	}
}

// retainedSectionBytes 计算区块 section 存储实际持有的字节数
// （不含 chunk/section 结构与列高度缓存）。
func retainedSectionBytes(chunk *Chunk) int {
	chunk.mu.RLock()
	defer chunk.mu.RUnlock()
	total := 0
	for _, s := range chunk.sections {
		if s == nil {
			continue
		}
		total += len(s.storage.data) * 8
		total += cap(s.storage.palette) * 2
	}
	return total
}
