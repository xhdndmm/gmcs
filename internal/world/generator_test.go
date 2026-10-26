//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package world

import (
	"math"
	"testing"
)

// TestSeededGeneratorTerrainVariety 验证大范围高度有明显起伏（海洋—丘陵—山地）。
func TestSeededGeneratorTerrainVariety(t *testing.T) {
	generator := SeededGenerator{Seed: 11}
	minH, maxH := math.MaxInt, math.MinInt
	for x := -1200; x <= 1200; x += 16 {
		for z := -1200; z <= 1200; z += 16 {
			h := generator.SurfaceY(x, z)
			if h < minH {
				minH = h
			}
			if h > maxH {
				maxH = h
			}
		}
	}
	if maxH-minH < 30 {
		t.Fatalf("terrain height range too small: [%d, %d]", minH, maxH)
	}
}

// TestSeededGeneratorBiomeVariety 验证大范围出现多个生物群系。
func TestSeededGeneratorBiomeVariety(t *testing.T) {
	generator := SeededGenerator{Seed: 11}
	seen := map[uint16]bool{}
	for x := -1200; x <= 1200; x += 32 {
		for z := -1200; z <= 1200; z += 32 {
			h := generator.SurfaceY(x, z)
			seen[generator.biomeAt(x, z, h)] = true
		}
	}
	if len(seen) < 4 {
		t.Fatalf("expected biome variety over 2400×2400 blocks, got %d kinds", len(seen))
	}
}

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

// TestSeededGeneratorStructure 验证基岩层、高度范围、水不超过海平面与地表完整
// （洞穴不破坏最高一格地表）。
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
				if height <= WorldMinY || height > landBaseHeight+mountainAmplitude+10 {
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
					// 陆地：最高一格地表必须存在（洞穴保持顶部密封），
					// 材质依群系而定（草/沙/红沙/石头/雪等）。
					switch state := chunk.GetBlockState(x, height, z); state {
					case AirBlock, WaterBlock, LavaBlock:
						t.Fatalf("land surface missing at (%d,%d,%d) in chunk %v, got %d",
							pos.X*SectionSize+x, height, pos.Z*SectionSize+z, pos, state)
					}
				}
			}
		}
	}
}

// TestSeededGeneratorTrees 验证树木存在且树干立在可生长方块上。
// 树干可以落在区块边缘（树冠由相邻区块补齐，见 decorate 的跨区块树冠说明）。
// 种子 42 的原点区域为陆地且树木茂密（见 docs/TODO.md：地形为确定性纯函数）。
func TestSeededGeneratorTrees(t *testing.T) {
	generator := SeededGenerator{Seed: 42}
	logs := 0
	for cx := -3; cx < 3; cx++ {
		for cz := -3; cz < 3; cz++ {
			chunk := generator.GenerateChunk(cx, cz)
			for x := 0; x < SectionSize; x++ {
				for z := 0; z < SectionSize; z++ {
					for y := WorldMinY; y < WorldMinY+WorldHeight; y++ {
						state := chunk.GetBlockState(x, y, z)
						if !isLogBlock(state) {
							continue
						}
						logs++
						below := chunk.GetBlockState(x, y-1, z)
						if below != state && !isTreeGround(below) {
							t.Fatalf("log at (%d,%d,%d) in chunk (%d,%d) stands on block %d",
								x, y, z, cx, cz, below)
						}
					}
				}
			}
		}
	}
	if logs == 0 {
		t.Fatal("expected at least one tree across 36 chunks")
	}
}

// isLogBlock 报告方块是否为树木原木。
func isLogBlock(state uint16) bool {
	return state == OakLogBlock || state == SpruceLogBlock || state == AcaciaLogBlock ||
		state == BirchLogBlock || state == DarkOakLogBlock || state == JungleLogBlock
}

// isTreeGround 报告方块能否支撑树干（草/土/雪块）。
func isTreeGround(state uint16) bool {
	return state == GrassBlock || state == DirtBlock || state == SnowBlock
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

// TestTreeCanopyCrossesChunkBorder 验证跨区块树冠：树干位于区块边缘列时，
// 相邻区块会补上该树的树叶（旧实现要求树干距边界至少 2 格，导致森林沿每个
// 区块边界出现 4 格宽的无树走廊，树冠也会被切掉一半）。
func TestTreeCanopyCrossesChunkBorder(t *testing.T) {
	generator := SeededGenerator{Seed: 42}
	isLeaf := func(state uint16) bool {
		return state == OakLeavesBlock || state == BirchLeavesBlock || state == SpruceLeavesBlock ||
			state == JungleLeavesBlock || state == AcaciaLeavesBlock || state == DarkOakLeavesBlock
	}
	found := false
	for cz := -4; cz <= 4 && !found; cz++ {
		for cx := -4; cx <= 4 && !found; cx++ {
			chunk := generator.GenerateChunk(cx, cz)
			next := generator.GenerateChunk(cx+1, cz)
			for localZ := 0; localZ < SectionSize && !found; localZ++ {
				for y := WorldMinY; y < 140 && !found; y++ {
					if !isLogBlock(chunk.GetBlockState(SectionSize-1, y, localZ)) {
						continue
					}
					// 树干在最东列：树冠（半径 2–3）必须出现在东侧区块的前两列。
					for dy := -3; dy <= 2 && !found; dy++ {
						for dz := -2; dz <= 2 && !found; dz++ {
							z := localZ + dz
							if z < 0 || z >= SectionSize {
								continue
							}
							for dx := 0; dx <= 1; dx++ {
								if isLeaf(next.GetBlockState(dx, y+dy, z)) {
									found = true
									break
								}
							}
						}
					}
				}
			}
		}
	}
	if !found {
		t.Fatal("未找到跨区块的树冠（相邻区块缺少边缘列树的树叶）")
	}
}

// TestUndergroundIsMostlySolid 验证洞穴密度接近原版观感。
// 旧实现（噪声阈值 0.66 + 密集蠕虫隧道）地下空腔约占 20–25%，
// 表现为“挖开三格就掉进大洞”的镂空世界。
func TestUndergroundIsMostlySolid(t *testing.T) {
	for _, seed := range []int64{42, 12345} {
		generator := SeededGenerator{Seed: seed}
		air, total := 0, 0
		for cx := 0; cx < 6; cx++ {
			for cz := 0; cz < 6; cz++ {
				chunk := generator.GenerateChunk(cx, cz)
				for x := 0; x < SectionSize; x++ {
					for z := 0; z < SectionSize; z++ {
						height := generator.SurfaceY(cx*SectionSize+x, cz*SectionSize+z)
						if height <= SeaLevel+1 {
							continue
						}
						for y := WorldMinY + 1; y < height-1; y++ {
							total++
							if chunk.GetBlockState(x, y, z) == AirBlock {
								air++
							}
						}
					}
				}
			}
		}
		if total == 0 {
			t.Fatalf("seed %d：没有陆地样本", seed)
		}
		if ratio := float64(air) / float64(total); ratio > 0.15 {
			t.Fatalf("seed %d：地下空腔占比 %.1f%%，洞穴过密（应 < 15%%）", seed, ratio*100)
		}
	}
}

// TestRiverBanksAreSmooth 验证河流两侧没有 1 格宽的垂直峡谷。
// 旧实现对很窄的噪声带直接做 smoothstep 下切，会在等值线处留下 4 格宽、
// 十余格深的笔直峭壁。
func TestRiverBanksAreSmooth(t *testing.T) {
	generator := SeededGenerator{Seed: 42}
	samples := 0
	for z := -256; z <= 256; z += 8 {
		for x := -320; x < 320; x++ {
			factor := generator.riverFactor(x, z)
			if factor < 0.2 {
				continue
			}
			samples++
			delta := abs(generator.SurfaceY(x+1, z) - generator.SurfaceY(x, z))
			if delta > 6 {
				t.Fatalf("河流谷地出现 %d 格落差（x=%d,z=%d, river=%.2f）", delta, x, z, factor)
			}
		}
	}
	if samples == 0 {
		t.Fatal("采样范围内没有河流")
	}
}
