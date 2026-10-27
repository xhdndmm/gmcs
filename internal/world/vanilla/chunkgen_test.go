//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"encoding/json"
	"strings"
	"testing"
)

// mustSurfaceSystem 构造 overworld 的地表系统。
func mustSurfaceSystem(t *testing.T, router *Router, chunkX, chunkZ int) *SurfaceSystem {
	t.Helper()
	system, err := NewSurfaceSystem(router.NewChunkSampler(chunkX, chunkZ))
	if err != nil {
		t.Fatalf("构造地表系统失败：%v", err)
	}
	if system == nil {
		t.Fatalf("overworld 应当有 surface_rule")
	}
	return system
}

// TestOverworldSurfaceRuleParses 校验 overworld 的 surface_rule 能被完整解析，
// 且规则树里出现的类型全部被实现（未实现的类型会让解析直接失败）。
func TestOverworldSurfaceRuleParses(t *testing.T) {
	router := mustRouter(t, 1)
	mustSurfaceSystem(t, router, 0, 0)

	data, err := Load()
	if err != nil {
		t.Fatalf("加载数据失败：%v", err)
	}
	setting, err := data.NoiseSetting("minecraft:overworld")
	if err != nil {
		t.Fatalf("读取噪声设置失败：%v", err)
	}
	var raw struct {
		SurfaceRule json.RawMessage `json:"surface_rule"`
	}
	if err := json.Unmarshal(setting.Raw, &raw); err != nil {
		t.Fatalf("解析噪声设置失败：%v", err)
	}

	found := map[string]int{}
	var walk func(node json.RawMessage)
	walk = func(node json.RawMessage) {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(node, &object); err != nil {
			return
		}
		if kind, _, problems := surfaceNodeHeader("surface_rule", node); len(problems) == 0 {
			found[kind]++
		}
		for _, value := range object {
			var nested json.RawMessage
			if err := json.Unmarshal(value, &nested); err == nil && len(nested) > 0 && nested[0] == '{' {
				walk(nested)
				continue
			}
			var list []json.RawMessage
			if err := json.Unmarshal(value, &list); err == nil {
				for _, entry := range list {
					if len(entry) > 0 && entry[0] == '{' {
						walk(entry)
					}
				}
			}
		}
	}
	walk(raw.SurfaceRule)

	expected := []string{
		"above_preliminary_surface", "bandlands", "biome", "block", "condition",
		"hole", "noise_threshold", "not", "sequence", "steep", "stone_depth",
		"temperature", "vertical_gradient", "water", "y_above",
	}
	for _, kind := range expected {
		if found[kind] == 0 {
			t.Errorf("规则树里没有出现 %s（数据或遍历有问题）", kind)
		}
		if _, ok := surfaceRuleSchemas[kind]; ok {
			continue
		}
		if _, ok := surfaceConditionSchemas[kind]; ok {
			continue
		}
		t.Errorf("类型 %s 既不是已实现的规则也不是已实现的条件", kind)
	}
}

// TestGenerateChunkTerrainLayers 校验陆地区块的垂直分层：
// 顶部草方块、其下泥土、再下石头/深板岩，底部基岩。
func TestGenerateChunkTerrainLayers(t *testing.T) {
	router := mustRouter(t, 4321)
	chunk, err := router.GenerateChunk(62, 62)
	if err != nil {
		t.Fatalf("生成失败：%v", err)
	}

	grass := 0
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			top := chunk.HeightAt(chunk.MinX+x, chunk.MinZ+z)
			topBlock := chunk.BlockAt(chunk.MinX+x, top, chunk.MinZ+z)
			if topBlock.Name == "minecraft:grass_block" {
				grass++
				if below := chunk.BlockAt(chunk.MinX+x, top-1, chunk.MinZ+z); below.Name != "minecraft:dirt" {
					t.Fatalf("(%d,%d) 草方块下面是 %s，want dirt", x, z, below)
				}
			}
			if bottom := chunk.BlockAt(chunk.MinX+x, chunk.MinY, chunk.MinZ+z); bottom.Name != "minecraft:bedrock" {
				t.Fatalf("(%d,%d) 最底部是 %s，want bedrock", x, z, bottom)
			}
		}
	}
	if grass < 16 {
		t.Fatalf("草方块只有 %d 个，区块 (62,62) 应当是陆地", grass)
	}
}

func TestCaveAndVoidAirAreAir(t *testing.T) {
	for _, name := range []string{"minecraft:air", "minecraft:cave_air", "minecraft:void_air"} {
		if !(SurfaceBlock{Name: name}).IsAir() {
			t.Errorf("%s.IsAir() = false, want true", name)
		}
	}
	for _, name := range []string{"minecraft:stone", "minecraft:water"} {
		if (SurfaceBlock{Name: name}).IsAir() {
			t.Errorf("%s.IsAir() = true, want false", name)
		}
	}
}

func TestCanyonVerticalRadiusProgress(t *testing.T) {
	definition := carverDefinition{shape: canyonShape{verticalRadiusDefaultFactor: 1}}
	start := canyonVerticalScale(definition, newLegacyRandom(1), 100, 0)
	middle := canyonVerticalScale(definition, newLegacyRandom(1), 100, 50)
	end := canyonVerticalScale(definition, newLegacyRandom(1), 100, 100)
	if start != 0 || middle < 0.75 || middle >= 1 || end < 0.75 || end >= 1 {
		t.Fatalf("canyon vertical scales = start:%v middle:%v end:%v", start, middle, end)
	}
}

// TestGenerateChunkOcean 校验海洋区块：海平面处是水、没有草方块、海底不是空气。
func TestGenerateChunkOcean(t *testing.T) {
	router := mustRouter(t, 4321)
	chunk, err := router.GenerateChunk(0, 0)
	if err != nil {
		t.Fatalf("生成失败：%v", err)
	}
	setting := router.Setting()
	water := 0
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			top := chunk.HeightAt(chunk.MinX+x, chunk.MinZ+z)
			if top >= setting.SeaLevel {
				t.Fatalf("(%d,%d) 顶部 %d 高于海平面，区块 (0,0) 应当是海洋", x, z, top)
			}
			floor := chunk.BlockAt(chunk.MinX+x, top, chunk.MinZ+z)
			if floor.IsAir() {
				t.Fatalf("(%d,%d) 海底是空气", x, z)
			}
			if floor.Name == "minecraft:grass_block" {
				t.Fatalf("(%d,%d) 水下出现草方块", x, z)
			}
			if chunk.BlockAt(chunk.MinX+x, setting.SeaLevel-1, chunk.MinZ+z).IsWater() {
				water++
			}
		}
	}
	if water == 0 {
		t.Fatalf("海洋区块在海平面以下没有水")
	}
}

func TestGenerateChunkCarvesPositiveDensityBlocks(t *testing.T) {
	router := mustRouter(t, 4321)
	chunk, err := router.GenerateChunk(0, 0)
	if err != nil {
		t.Fatalf("生成失败：%v", err)
	}
	sampler := router.NewChunkSampler(0, 0)
	carvedAir := 0
	for x := chunk.MinX; x < chunk.MinX+16; x++ {
		for z := chunk.MinZ; z < chunk.MinZ+16; z++ {
			for y := chunk.MinY; y < chunk.MinY+chunk.Height; y++ {
				if sampler.Density(x, y, z) > 0 && chunk.BlockAt(x, y, z).IsAir() {
					carvedAir++
				}
			}
		}
	}
	if carvedAir == 0 {
		t.Fatal("没有正密度方块被雕刻为空气，overworld cave carver 可能未执行")
	}
}

// TestGenerateChunkDeterministic 校验同种子同区块两次生成完全一致。
func TestGenerateChunkDeterministic(t *testing.T) {
	router := mustRouter(t, 98765)
	first, err := router.GenerateChunk(-3, 7)
	if err != nil {
		t.Fatalf("生成失败：%v", err)
	}
	second, err := router.GenerateChunk(-3, 7)
	if err != nil {
		t.Fatalf("生成失败：%v", err)
	}
	for x := first.MinX; x < first.MinX+16; x++ {
		for z := first.MinZ; z < first.MinZ+16; z++ {
			for y := first.MinY; y < first.MinY+first.Height; y++ {
				a := first.BlockAt(x, y, z)
				b := second.BlockAt(x, y, z)
				if a.Name != b.Name || len(a.Properties) != len(b.Properties) {
					t.Fatalf("(%d,%d,%d) 两次生成不同：%v vs %v", x, y, z, a, b)
				}
				for key, value := range a.Properties {
					if b.Properties[key] != value {
						t.Fatalf("(%d,%d,%d) 两次生成属性不同：%v vs %v", x, y, z, a, b)
					}
				}
			}
		}
	}
}

// TestBiomeSourceMatchesTerrain 校验群系源与地形一致：
// 陆地区块的地表群系不是海洋，海洋区块的地表群系是海洋类群系。
func TestBiomeSourceMatchesTerrain(t *testing.T) {
	router := mustRouter(t, 4321)
	biomes, err := NewBiomeSource(router)
	if err != nil {
		t.Fatalf("构造群系源失败：%v", err)
	}
	land := biomes.BiomeAtBlock(62*16+8, 0, 62*16+8)
	ocean := biomes.BiomeAtBlock(8, 0, 8)
	t.Logf("land biome (y=0)=%s ocean biome (y=0)=%s", land, ocean)

	// 地表规则按"表层高度"取群系，因此这里也用生成后的地表高度采样。
	landChunk, err := router.GenerateChunk(62, 62)
	if err != nil {
		t.Fatalf("生成失败：%v", err)
	}
	oceanChunk, err := router.GenerateChunk(0, 0)
	if err != nil {
		t.Fatalf("生成失败：%v", err)
	}
	land = biomes.BiomeAtBlock(62*16+8, landChunk.HeightAt(62*16+8, 62*16+8)+1, 62*16+8)
	ocean = biomes.BiomeAtBlock(8, oceanChunk.HeightAt(8, 8)+1, 8)
	t.Logf("land biome (surface)=%s ocean biome (surface)=%s", land, ocean)

	if !strings.Contains(land, "plains") && !strings.Contains(land, "forest") &&
		!strings.Contains(land, "hills") && !strings.Contains(land, "meadow") &&
		!strings.Contains(land, "taiga") && !strings.Contains(land, "grove") &&
		!strings.Contains(land, "peaks") && !strings.Contains(land, "savanna") &&
		!strings.Contains(land, "jungle") && !strings.Contains(land, "swamp") &&
		!strings.Contains(land, "badlands") && !strings.Contains(land, "beach") &&
		!strings.Contains(land, "birch") && !strings.Contains(land, "cherry") &&
		!strings.Contains(land, "windswept") && !strings.Contains(land, "mushroom") {
		t.Fatalf("陆地区块的群系是 %s（不像陆地群系）", land)
	}
	if !strings.Contains(ocean, "ocean") && !strings.Contains(ocean, "river") &&
		!strings.Contains(ocean, "beach") && !strings.Contains(ocean, "swamp") {
		t.Fatalf("海洋区块的群系是 %s（不像水域群系）", ocean)
	}
	if again := biomes.BiomeAtBlock(62*16+8, 0, 62*16+8); again != land {
		t.Fatalf("群系采样不稳定：%s vs %s", land, again)
	}
}

// TestBiomeSourceCoversAllBiomes 校验参数表里的群系都能被采到名字（数据完整）。
func TestBiomeSourceCoversAllBiomes(t *testing.T) {
	router := mustRouter(t, 4321)
	biomes, err := NewBiomeSource(router)
	if err != nil {
		t.Fatalf("构造群系源失败：%v", err)
	}
	seen := map[string]bool{}
	for x := -4096; x <= 4096; x += 128 {
		for z := -4096; z <= 4096; z += 128 {
			seen[biomes.BiomeAtBlock(x, 0, z)] = true
		}
	}
	if len(seen) < 10 {
		t.Fatalf("采样到的群系只有 %d 种，参数表可能没生效", len(seen))
	}
	for name := range seen {
		if _, ok := biomes.climates[name]; !ok {
			t.Fatalf("群系 %s 没有气候数据", name)
		}
	}
}

// BenchmarkGenerateChunk 度量完整区块生成（密度 + 含水层 + 地表规则）的耗时。
func BenchmarkGenerateChunk(b *testing.B) {
	data, err := Load()
	if err != nil {
		b.Fatalf("加载数据失败：%v", err)
	}
	router, err := NewRouter(data, "minecraft:overworld", 1234)
	if err != nil {
		b.Fatalf("构造路由表失败：%v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := router.GenerateChunk(62, 62); err != nil {
			b.Fatalf("生成失败：%v", err)
		}
	}
}
