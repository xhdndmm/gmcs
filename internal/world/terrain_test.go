package world

import (
	"testing"
)

// TestSeededGeneratorCaves 验证陆地列存在洞穴，且洞穴不破坏地表密封与基岩层。
// 注意种子 7 的原点附近全是海洋，因此先在更大范围内寻找陆地区块。
func TestSeededGeneratorCaves(t *testing.T) {
	generator := SeededGenerator{Seed: 7}
	undergroundAir := 0
	landChunks := 0
	for cx := -8; cx <= 8; cx++ {
		for cz := -8; cz <= 8; cz++ {
			// 先统计该区块的陆地列数（高于海平面）。
			landColumns := 0
			for x := 0; x < SectionSize; x++ {
				for z := 0; z < SectionSize; z++ {
					if generator.SurfaceY(cx*SectionSize+x, cz*SectionSize+z) > SeaLevel+1 {
						landColumns++
					}
				}
			}
			if landColumns < 100 {
				continue
			}
			landChunks++
			chunk := generator.GenerateChunk(cx, cz)
			for x := 0; x < SectionSize; x++ {
				for z := 0; z < SectionSize; z++ {
					height := generator.SurfaceY(cx*SectionSize+x, cz*SectionSize+z)
					if height <= SeaLevel+1 {
						continue // 水下列不参与统计与密封断言
					}
					// 地表密封：最高一格地表必须存在（洞穴最多刻到 height-2）。
					if state := chunk.GetBlockState(x, height, z); state == AirBlock {
						t.Fatalf("surface seal broken at (%d,%d,%d)", cx*SectionSize+x, height, cz*SectionSize+z)
					}
					// 基岩层不被雕刻。
					if state := chunk.GetBlockState(x, WorldMinY, z); state != BedrockBlock {
						t.Fatalf("bedrock removed at (%d,%d,%d)", x, WorldMinY, z)
					}
					// 统计地下空腔（洞穴）。
					for y := WorldMinY + 2; y < height-1; y++ {
						if chunk.GetBlockState(x, y, z) == AirBlock {
							undergroundAir++
						}
					}
				}
			}
		}
	}
	if landChunks == 0 {
		t.Fatal("no land chunks found in scan range")
	}
	if undergroundAir < 100 {
		t.Fatalf("underground air blocks = %d across %d land chunks, expected caves (>=100)", undergroundAir, landChunks)
	}
}

// TestSeededGeneratorOres 验证矿物生成：至少出现煤/铁等矿脉，且深板岩
// 只出现在 y < 0。
func TestSeededGeneratorOres(t *testing.T) {
	generator := SeededGenerator{Seed: 11}
	counts := map[uint16]int{}
	for cx := 0; cx < 4; cx++ {
		for cz := 0; cz < 4; cz++ {
			chunk := generator.GenerateChunk(cx, cz)
			for x := 0; x < SectionSize; x++ {
				for z := 0; z < SectionSize; z++ {
					for y := WorldMinY; y < 64; y++ {
						switch state := chunk.GetBlockState(x, y, z); state {
						case CoalOreBlock, DeepslateCoalOreBlock, IronOreBlock, DeepslateIronOreBlock,
							CopperOreBlock, DeepslateCopperOre, GoldOreBlock, DeepslateGoldOreBlock,
							RedstoneOreBlock, DeepslateRedstoneOre, DiamondOreBlock, DeepslateDiamondOre,
							LapisOreBlock, DeepslateLapisOre, EmeraldOreBlock, DeepslateEmeraldOre:
							counts[state]++
						case DeepslateBlock:
							if y >= deepslateTop {
								t.Fatalf("deepslate above deepslateTop at y=%d", y)
							}
						case StoneBlock:
							if y < deepslateTop {
								t.Fatalf("stone below deepslateTop at y=%d", y)
							}
						}
					}
				}
			}
		}
	}
	if counts[CoalOreBlock]+counts[DeepslateCoalOreBlock] == 0 {
		t.Fatal("no coal ore found across 16 chunks")
	}
	if counts[IronOreBlock]+counts[DeepslateIronOreBlock] == 0 {
		t.Fatal("no iron ore found across 16 chunks")
	}
}

// TestSeededGeneratorBiomeSet 验证大范围地形出现预期的主要群系类别。
func TestSeededGeneratorBiomeSet(t *testing.T) {
	generator := SeededGenerator{Seed: 3}
	seen := map[uint16]bool{}
	for x := -3000; x <= 3000; x += 64 {
		for z := -3000; z <= 3000; z += 64 {
			height := generator.SurfaceY(x, z)
			seen[generator.biomeAt(x, z, height)] = true
		}
	}
	// 至少出现以下类别中的 10 种（雪原/针叶/平原/森林/沙漠/稀树/丛林/海洋/
	// 山地/恶地等）。
	categories := []uint16{
		biomeDeepOcean, biomeOcean, biomeBeach, biomeStonyShore, biomeRiver,
		biomeForest, biomeBirchForest, biomeDarkForest, biomeTaiga,
		biomeSnowyPlains, biomeSnowyTaiga, biomeDesert, biomeBadlands,
		biomeSavanna, biomeSwamp, biomeJungle, biomeMeadow, biomeStonyPeaks,
	}
	found := 0
	for _, biome := range categories {
		if seen[biome] {
			found++
		}
	}
	if found < 10 {
		t.Fatalf("biome categories found = %d (of %d), expected variety", found, len(categories))
	}
}

// TestSeededGeneratorRiver 验证河流噪声确实把地形压低到海平面附近。
func TestSeededGeneratorRiver(t *testing.T) {
	// 在较大范围内寻找河心（riverFactor 接近 1）的列。
	generator := SeededGenerator{Seed: 5}
	bestFactor := 0.0
	bestX, bestZ := 0, 0
	for x := -2000; x <= 2000; x += 8 {
		for z := -2000; z <= 2000; z += 8 {
			if factor := generator.riverFactor(x, z); factor > bestFactor {
				bestFactor = factor
				bestX, bestZ = x, z
			}
		}
	}
	if bestFactor < 0.9 {
		t.Fatalf("best river factor = %v, expected a river channel in range", bestFactor)
	}
	height := generator.SurfaceY(bestX, bestZ)
	if height > SeaLevel+1 {
		t.Fatalf("river center height = %d, expected the channel to be near/below sea level (%d)", height, SeaLevel)
	}
}

// TestNetherGenerator 验证下界地形结构：基岩地板/天花板、净岩、岩浆海。
func TestNetherGenerator(t *testing.T) {
	generator := NetherGenerator{Seed: 42}
	chunk := generator.GenerateChunk(0, 0)
	lava, netherrack := 0, 0
	for x := 0; x < SectionSize; x++ {
		for z := 0; z < SectionSize; z++ {
			if state := chunk.GetBlockState(x, 0, z); state != BedrockBlock {
				t.Fatalf("nether floor at (%d,0,%d) = %d, want bedrock", x, z, state)
			}
			if state := chunk.GetBlockState(x, netherRoofY, z); state != BedrockBlock {
				t.Fatalf("nether roof at (%d,%d,%d) = %d, want bedrock", x, netherRoofY, z, state)
			}
			for y := 1; y <= netherLavaLevel; y++ {
				switch chunk.GetBlockState(x, y, z) {
				case LavaBlock:
					lava++
				case NetherrackBlock:
					netherrack++
				}
			}
		}
	}
	if netherrack == 0 {
		t.Fatal("no netherrack below lava level")
	}
	if lava == 0 {
		t.Fatal("no lava sea found")
	}
}

// TestEndGenerator 验证末地主岛与虚空结构。
func TestEndGenerator(t *testing.T) {
	generator := EndGenerator{Seed: 42}
	chunk := generator.GenerateChunk(0, 0)
	endStone := 0
	for x := 0; x < SectionSize; x++ {
		for z := 0; z < SectionSize; z++ {
			for y := 40; y <= 100; y++ {
				if chunk.GetBlockState(x, y, z) == EndStoneBlock {
					endStone++
				}
			}
		}
	}
	if endStone == 0 {
		t.Fatal("end main island missing in chunk (0,0)")
	}
	// 虚空：在远处寻找一个完全空的区块（外部岛屿按 192 格网格稀疏分布，
	// 网格单元间必然存在虚空）。
	foundVoid := false
	for cx := 40; cx < 80 && !foundVoid; cx++ {
		for cz := 40; cz < 80 && !foundVoid; cz++ {
			remote := generator.GenerateChunk(cx, cz)
			empty := true
			for x := 0; x < SectionSize && empty; x++ {
				for z := 0; z < SectionSize && empty; z++ {
					for y := 0; y < 128; y++ {
						if remote.GetBlockState(x, y, z) != AirBlock {
							empty = false
							break
						}
					}
				}
			}
			foundVoid = empty
		}
	}
	if !foundVoid {
		t.Fatal("expected void chunks between outer end islands")
	}
}

// TestDimensionRanges 验证各维度的 section 范围与高度参数。
func TestDimensionRanges(t *testing.T) {
	cases := []struct {
		dim          Dimension
		minY         int
		height       int
		low, high    int
		sectionCount int
	}{
		{DimensionOverworld, -64, 384, 0, 24, 24},
		{DimensionNether, 0, 256, 4, 20, 16},
		{DimensionEnd, 0, 256, 4, 20, 16},
	}
	for _, test := range cases {
		if got := test.dim.MinY(); got != test.minY {
			t.Fatalf("%v MinY = %d, want %d", test.dim, got, test.minY)
		}
		if got := test.dim.Height(); got != test.height {
			t.Fatalf("%v Height = %d, want %d", test.dim, got, test.height)
		}
		low, high := test.dim.SectionRange()
		if low != test.low || high != test.high {
			t.Fatalf("%v SectionRange = [%d,%d), want [%d,%d)", test.dim, low, high, test.low, test.high)
		}
		if got := test.dim.SectionCount(); got != test.sectionCount {
			t.Fatalf("%v SectionCount = %d, want %d", test.dim, got, test.sectionCount)
		}
	}
	if _, ok := ParseDimension("minecraft:the_nether"); !ok {
		t.Fatal("ParseDimension(the_nether) failed")
	}
	if _, ok := ParseDimension("nether"); !ok {
		t.Fatal("ParseDimension(nether) failed")
	}
	if _, ok := ParseDimension("nowhere"); ok {
		t.Fatal("ParseDimension(nowhere) should fail")
	}
}

// TestPrefetchMatchesSync 验证异步预生成的区块与同步生成完全一致。
func TestPrefetchMatchesSync(t *testing.T) {
	instance, err := Open(t.TempDir(), SeededGenerator{Seed: 99})
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()

	const count = 36
	positions := make([]ChunkPos, 0, count)
	for i := 0; i < count; i++ {
		positions = append(positions, ChunkPos{X: i % 6, Z: i / 6})
	}
	instance.Prefetch(positions)
	for _, pos := range positions {
		chunk, err := instance.Chunk(pos.X, pos.Z)
		if err != nil {
			t.Fatalf("Chunk(%d,%d): %v", pos.X, pos.Z, err)
		}
		// 与同步生成对照（同一生成器、同一坐标）。
		expected := SeededGenerator{Seed: 99}.GenerateChunk(pos.X, pos.Z)
		for x := 0; x < SectionSize; x++ {
			for z := 0; z < SectionSize; z++ {
				for y := WorldMinY; y < WorldMinY+WorldHeight; y++ {
					if chunk.GetBlockState(x, y, z) != expected.GetBlockState(x, y, z) {
						t.Fatalf("prefetched chunk (%d,%d) differs at (%d,%d,%d)", pos.X, pos.Z, x, y, z)
					}
				}
			}
		}
	}
}

// TestPrefetchAfterClose 验证关闭时等待中的预生成请求会被解决而不是死锁。
func TestPrefetchAfterClose(t *testing.T) {
	instance, err := Open(t.TempDir(), SeededGenerator{Seed: 5})
	if err != nil {
		t.Fatal(err)
	}
	positions := make([]ChunkPos, 0, 64)
	for i := 0; i < 64; i++ {
		positions = append(positions, ChunkPos{X: i, Z: i})
	}
	instance.Prefetch(positions)
	if err := instance.Close(); err != nil {
		t.Fatal(err)
	}
	// 关闭后的调用必须返回错误而不是阻塞。
	if _, err := instance.Chunk(0, 0); err == nil {
		t.Fatal("Chunk after Close should fail")
	}
	// 重新打开同一目录应能正常读取（区块已保存或可再生成）。
	reopened, err := Open(instance.Dir(), SeededGenerator{Seed: 5})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := reopened.Chunk(0, 0); err != nil {
		t.Fatalf("reopened Chunk: %v", err)
	}
}

// BenchmarkGenerateChunkNether 衡量下界区块生成成本。
func BenchmarkGenerateChunkNether(b *testing.B) {
	generator := NetherGenerator{Seed: 42}
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		chunk := generator.GenerateChunk(i%64, i/64)
		benchSinkChunk = chunk
	}
}

// BenchmarkGenerateChunkEnd 衡量末地区块生成成本。
func BenchmarkGenerateChunkEnd(b *testing.B) {
	generator := EndGenerator{Seed: 42}
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		chunk := generator.GenerateChunk(i%64, i/64)
		benchSinkChunk = chunk
	}
}

// benchSinkChunk 防止编译器把区块生成当作死代码消除。
var benchSinkChunk *Chunk
