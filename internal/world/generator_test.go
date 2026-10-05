package world

import "testing"

// TestSeededGeneratorDeterministic 验证相同种子、相同坐标生成完全相同的区块。
func TestSeededGeneratorDeterministic(t *testing.T) {
	first := SeededGenerator{Seed: 42}
	second := SeededGenerator{Seed: 42}
	for _, pos := range []ChunkPos{{0, 0}, {1, -1}, {-3, 5}} {
		a := first.GenerateChunk(pos.X, pos.Z)
		b := second.GenerateChunk(pos.X, pos.Z)
		for x := 0; x < SectionSize; x++ {
			for z := 0; z < SectionSize; z++ {
				for y := WorldMinY; y < WorldMinY+WorldHeight; y++ {
					if a.GetBlockState(x, y, z) != b.GetBlockState(x, y, z) {
						t.Fatalf("chunk %v differs at (%d,%d,%d)", pos, x, y, z)
					}
				}
			}
		}
	}
}

// TestSeededGeneratorDiffersBySeed 验证不同种子会产生不同地形。
func TestSeededGeneratorDiffersBySeed(t *testing.T) {
	a := SeededGenerator{Seed: 1}.GenerateChunk(0, 0)
	b := SeededGenerator{Seed: 2}.GenerateChunk(0, 0)
	for x := 0; x < SectionSize; x++ {
		for z := 0; z < SectionSize; z++ {
			topA, okA := a.TopSolidY(x, z)
			topB, okB := b.TopSolidY(x, z)
			if okA != okB || topA != topB {
				return
			}
		}
	}
	t.Fatal("different seeds produced identical terrain")
}

// TestSeededGeneratorStructure 验证基岩层、高度范围与水不超过海平面。
func TestSeededGeneratorStructure(t *testing.T) {
	generator := SeededGenerator{Seed: 7}
	for _, pos := range []ChunkPos{{0, 0}, {2, 3}, {-1, -2}} {
		chunk := generator.GenerateChunk(pos.X, pos.Z)
		for x := 0; x < SectionSize; x++ {
			for z := 0; z < SectionSize; z++ {
				if state := chunk.GetBlockState(x, WorldMinY, z); state != BedrockBlock {
					t.Fatalf("bottom block of chunk %v at (%d,%d) is %d, want bedrock", pos, x, z, state)
				}
				height := generator.SurfaceY(pos.X*SectionSize+x, pos.Z*SectionSize+z)
				if height <= WorldMinY || height > SeaLevel+8 {
					t.Fatalf("terrain height %d out of expected range", height)
				}
				if state := chunk.GetBlockState(x, SeaLevel+1, z); state == WaterBlock {
					t.Fatalf("water above sea level in chunk %v at (%d,%d)", pos, x, z)
				}
				if height < SeaLevel {
					// 低洼处：地面之上应是水。
					if state := chunk.GetBlockState(x, height+1, z); state != WaterBlock {
						t.Fatalf("expected water above ground at (%d,%d) in chunk %v, got %d",
							pos.X*SectionSize+x, pos.Z*SectionSize+z, pos, state)
					}
				} else if height > SeaLevel+1 {
					// 高地：地表应是草方块。
					if state := chunk.GetBlockState(x, height, z); state != GrassBlock {
						t.Fatalf("expected grass at (%d,%d,%d) in chunk %v, got %d",
							pos.X*SectionSize+x, height, pos.Z*SectionSize+z, pos, state)
					}
				}
			}
		}
	}
}

// TestSeededGeneratorTrees 验证橡树存在、树干立在草地上且遵守区块边缘留白。
func TestSeededGeneratorTrees(t *testing.T) {
	generator := SeededGenerator{Seed: 7}
	logs := 0
	for cx := 0; cx < 4; cx++ {
		for cz := 0; cz < 4; cz++ {
			chunk := generator.GenerateChunk(cx, cz)
			for x := 0; x < SectionSize; x++ {
				for z := 0; z < SectionSize; z++ {
					for y := WorldMinY; y < WorldMinY+WorldHeight; y++ {
						if chunk.GetBlockState(x, y, z) != OakLogBlock {
							continue
						}
						logs++
						// 树只在距边缘 2 格以内的列生长。
						if x < 2 || x > SectionSize-3 || z < 2 || z > SectionSize-3 {
							t.Fatalf("tree trunk at (%d,%d) in chunk (%d,%d) too close to the border", x, z, cx, cz)
						}
						if below := chunk.GetBlockState(x, y-1, z); below != OakLogBlock && below != GrassBlock {
							t.Fatalf("log at (%d,%d,%d) in chunk (%d,%d) stands on block %d",
								x, y, z, cx, cz, below)
						}
					}
				}
			}
		}
	}
	if logs == 0 {
		t.Fatal("expected at least one oak tree across 16 chunks")
	}
}

// TestFloorDivision 验证负坐标的向下取整除法与取模。
func TestFloorDivision(t *testing.T) {
	cases := []struct {
		a, b int
		div  int
		mod  int
	}{
		{15, 16, 0, 15},
		{16, 16, 1, 0},
		{-1, 16, -1, 15},
		{-16, 16, -1, 0},
		{-17, 16, -2, 15},
	}
	for _, test := range cases {
		if got := floorDiv(test.a, test.b); got != test.div {
			t.Fatalf("floorDiv(%d, %d) = %d, want %d", test.a, test.b, got, test.div)
		}
		if got := floorMod(test.a, test.b); got != test.mod {
			t.Fatalf("floorMod(%d, %d) = %d, want %d", test.a, test.b, got, test.mod)
		}
	}
}

// TestWorldGroundY 验证世界在超平坦生成器下的地面高度查询。
func TestWorldGroundY(t *testing.T) {
	instance, err := Open(t.TempDir(), FlatGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()

	for _, pos := range []struct{ x, z int }{{0, 0}, {15, 15}, {-1, -17}, {-16, 32}} {
		y, ok := instance.GroundY(pos.x, pos.z)
		if !ok || y != FlatSpawnY {
			t.Fatalf("GroundY(%d, %d) = %v, %v; want %d, true", pos.x, pos.z, y, ok, FlatSpawnY)
		}
	}
	if got := instance.SurfaceY(5, -7); got != FlatGroundLevel {
		t.Fatalf("SurfaceY = %d, want %d", got, FlatGroundLevel)
	}
}
