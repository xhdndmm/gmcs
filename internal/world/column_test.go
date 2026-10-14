//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package world

import "testing"

// TestChunkCopyColumn 验证按列批量读取：与逐格 GetBlockState 结果一致、
// 越界位置返回空气、长度不足时截断。
func TestChunkCopyColumn(t *testing.T) {
	chunk := NewChunk(0, 0)
	chunk.SetBlockState(2, WorldMinY+10, 3, StoneBlock)
	chunk.SetBlockState(2, WorldMinY+11, 3, DirtBlock)
	chunk.SetBlockState(2, WorldMinY+13, 3, GrassBlock)

	var buf [8]uint16
	n := chunk.CopyColumn(2, WorldMinY+10, WorldMinY+13, 3, buf[:])
	if n != 4 {
		t.Fatalf("CopyColumn 写入数量 = %d, want 4", n)
	}
	want := [4]uint16{StoneBlock, DirtBlock, AirBlock, GrassBlock}
	for i, expected := range want {
		if buf[i] != expected {
			t.Fatalf("列第 %d 项 = %d, want %d", i, buf[i], expected)
		}
		if direct := chunk.GetBlockState(2, WorldMinY+10+i, 3); direct != expected {
			t.Fatalf("与 GetBlockState 不一致：%d vs %d", direct, expected)
		}
	}

	// 长度不足时截断。
	if n := chunk.CopyColumn(2, WorldMinY+10, WorldMinY+13, 3, buf[:2]); n != 2 {
		t.Fatalf("短缓冲写入数量 = %d, want 2", n)
	}
	// 区间为空/越界。
	if n := chunk.CopyColumn(2, WorldMinY+13, WorldMinY+10, 3, buf[:]); n != 0 {
		t.Fatalf("空区间写入数量 = %d, want 0", n)
	}
	if n := chunk.CopyColumn(2, WorldMinY-2, WorldMinY-1, 3, buf[:]); n != 2 || buf[0] != AirBlock || buf[1] != AirBlock {
		t.Fatalf("世界底部以下 = %d 项 %v, want 2 项空气", n, buf[:2])
	}
}

// TestWorldLoadedColumn 验证世界层按列读取：未加载区块返回 0（不触发生成），
// 已加载区块返回指定区间内的状态。
func TestWorldLoadedColumn(t *testing.T) {
	dir := t.TempDir()
	w, err := Open(dir, FlatGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	var buf [8]uint16
	// 未加载区块：不触发加载/生成。
	if n := w.LoadedColumn(1000, WorldMinY, WorldMinY+3, 1000, buf[:]); n != 0 {
		t.Fatalf("未加载区块写入数量 = %d, want 0", n)
	}
	if got := w.DirtyCount(); got != 0 {
		t.Fatalf("LoadedColumn 触发了区块加载/生成：dirty = %d", got)
	}

	// 加载区块后：列读取与 BlockAt 一致。
	if _, err := w.Chunk(0, 0); err != nil {
		t.Fatal(err)
	}
	w.SetBlock(3, WorldMinY+40, 4, StoneBlock)
	n := w.LoadedColumn(3, WorldMinY+39, WorldMinY+42, 4, buf[:])
	if n != 4 {
		t.Fatalf("已加载区块写入数量 = %d, want 4", n)
	}
	for i := 0; i < n; i++ {
		y := WorldMinY + 39 + i
		if buf[i] != w.BlockAt(3, y, 4) {
			t.Fatalf("y=%d 列读取 %d != BlockAt %d", y, buf[i], w.BlockAt(3, y, 4))
		}
	}
	if buf[1] != StoneBlock {
		t.Fatalf("列内方块 = %d, want stone %d", buf[1], StoneBlock)
	}
}
