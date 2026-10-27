//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import "testing"

// TestAquiferGridForChunk 校验含水层格网格尺寸与索引布局。
// 对 0..15 的区块、minY=-64、height=384，原版得到 3×35×3 = 315 个格。
func TestAquiferGridForChunk(t *testing.T) {
	grid := newAquiferGrid(0, 15, 0, 15, -64, 384)
	if grid.originX != -1 || grid.originZ != -1 {
		t.Fatalf("水平格原点 = (%d, %d), want (-1, -1)", grid.originX, grid.originZ)
	}
	if grid.originY != -7 {
		t.Fatalf("竖向格原点 = %d, want -7", grid.originY)
	}
	if grid.sizeX != 3 || grid.sizeZ != 3 || grid.sizeY != 35 {
		t.Fatalf("格尺寸 = %d×%d×%d, want 3×35×3", grid.sizeX, grid.sizeY, grid.sizeZ)
	}
	if got, want := grid.size(), 315; got != want {
		t.Fatalf("格总数 = %d, want %d", got, want)
	}
}

// TestAquiferGridIndex 校验索引公式与包围盒判断。
func TestAquiferGridIndex(t *testing.T) {
	grid := newAquiferGrid(0, 15, 0, 15, -64, 384)
	// ((y-K)*sizeZ + (z-L))*sizeX + (x-J)
	const x, y, z = -1, -7, -1
	if got, want := grid.index(x, y, z), 0; got != want {
		t.Fatalf("原点的索引 = %d, want %d", got, want)
	}
	if got, want := grid.index(x+1, y, z), 1; got != want {
		t.Fatalf("x+1 的索引 = %d, want %d", got, want)
	}
	if got, want := grid.index(x, y, z+1), 3; got != want {
		t.Fatalf("z+1 的索引 = %d, want %d", got, want)
	}
	if got, want := grid.index(x, y+1, z), 9; got != want {
		t.Fatalf("y+1 的索引 = %d, want %d", got, want)
	}

	all := make(map[int]bool, grid.size())
	for gx := 0; gx < grid.sizeX; gx++ {
		for gy := 0; gy < grid.sizeY; gy++ {
			for gz := 0; gz < grid.sizeZ; gz++ {
				x := grid.originX + gx
				y := grid.originY + gy
				z := grid.originZ + gz
				index := grid.index(x, y, z)
				if index < 0 || index >= grid.size() {
					t.Fatalf("(%d,%d,%d) 的索引 %d 越界", x, y, z, index)
				}
				if all[index] {
					t.Fatalf("索引 %d 重复", index)
				}
				all[index] = true
			}
		}
	}
	if len(all) != grid.size() {
		t.Fatalf("索引覆盖 %d 个格，want %d", len(all), grid.size())
	}

	if !grid.contains(0, 0, 0) {
		t.Fatal("(0,0,0) 应当落在网格内")
	}
	if grid.contains(grid.originX+grid.sizeX, 0, 0) {
		t.Fatal("上界之外不应当落在网格内")
	}
}

// TestAquiferPressure 校验压力函数：同格为 1，相距 25 格为 0。
func TestAquiferPressure(t *testing.T) {
	cases := []struct {
		from, to int
		want     float64
	}{
		{0, 0, 1},
		{0, 1, 1 - 1.0/25.0},
		{0, 25, 0},
		{0, 50, -1},
		{10, 10, 1},
	}
	for _, testCase := range cases {
		if got := aquiferPressure(testCase.from, testCase.to); got != testCase.want {
			t.Fatalf("pressure(%d, %d) = %v, want %v", testCase.from, testCase.to, got, testCase.want)
		}
	}
}

// TestAquiferGridRoundTrip 校验方块坐标与格坐标之间的换算。
func TestAquiferGridRoundTrip(t *testing.T) {
	for _, block := range []int{-64, -49, -48, -1, 0, 11, 12, 15, 16, 100} {
		for offset := 0; offset < 12; offset++ {
			cell := blockToAquiferGridY(block*12 + offset)
			if cell != block {
				t.Fatalf("blockToAquiferGridY(%d) = %d, want %d", block*12+offset, cell, block)
			}
			if got := aquiferGridToBlockY(cell, offset); got != block*12+offset {
				t.Fatalf("aquiferGridToBlockY(%d, %d) = %d, want %d", cell, offset, got, block*12+offset)
			}
		}
		for offset := 0; offset < 16; offset++ {
			if got := aquiferGridToBlockXZ(blockToAquiferGridXZ(block*16+offset), offset); got != block*16+offset {
				t.Fatalf("水平格往返失败：block=%d offset=%d got=%d", block, offset, got)
			}
		}
	}
}
