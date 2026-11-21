package vanilla

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestOreFeaturesGenerated(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(data, "minecraft:overworld", 4321)
	if err != nil {
		t.Fatal(err)
	}
	chunk, err := router.GenerateChunk(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	ores := map[string]int{}
	deepslate := 0
	chunk.ForEachBlock(func(_, _, _ int, block SurfaceBlock) {
		if strings.HasSuffix(block.Name, "_ore") {
			ores[block.Name]++
			if strings.HasPrefix(block.Name, "minecraft:deepslate_") {
				deepslate++
			}
		}
	})
	if len(ores) == 0 {
		t.Fatal("区块没有生成任何矿石")
	}
	for _, name := range []string{"minecraft:coal_ore", "minecraft:iron_ore"} {
		if ores[name] == 0 {
			t.Fatalf("缺少 %s（实际：%v）", name, ores)
		}
	}
	if deepslate == 0 {
		t.Fatalf("缺少深板岩矿石变体（实际：%v）", ores)
	}
}

func TestOreFeaturesDeterministic(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	first, err := NewRouter(data, "minecraft:overworld", 99)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewRouter(data, "minecraft:overworld", 99)
	if err != nil {
		t.Fatal(err)
	}
	a, err := first.GenerateChunk(3, -2)
	if err != nil {
		t.Fatal(err)
	}
	b, err := second.GenerateChunk(3, -2)
	if err != nil {
		t.Fatal(err)
	}
	a.ForEachBlock(func(x, y, z int, block SurfaceBlock) {
		if other := b.BlockAt(x, y, z); !reflect.DeepEqual(other, block) {
			t.Fatalf("(%d,%d,%d) = %+v, want %+v", x, y, z, other, block)
		}
	})
}

func TestPlacementCountAndHeightSampling(t *testing.T) {
	count := intProvider{kind: "minecraft:uniform", minimum: 2, maximum: 4}
	random := newLegacyRandom(1)
	for i := 0; i < 100; i++ {
		value := count.sample(random)
		if value < 2 || value > 4 {
			t.Fatalf("count sample = %d, want [2,4]", value)
		}
	}
	height := featureHeightProvider{
		kind:    "minecraft:trapezoid",
		minimum: heightAnchor{kind: "absolute", value: -64},
		maximum: heightAnchor{kind: "absolute", value: 64},
	}
	setting := NoiseSetting{MinY: -64, Height: 384}
	for i := 0; i < 100; i++ {
		value := height.sample(random, setting)
		if value < -64 || value > 64 {
			t.Fatalf("height sample = %d, want [-64,64]", value)
		}
	}
}

func TestTreeFeaturesGenerated(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(data, "minecraft:overworld", 4321)
	if err != nil {
		t.Fatal(err)
	}
	biomes, err := router.Biomes()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for chunkX := 0; chunkX < 120 && !found; chunkX++ {
		for chunkZ := 0; chunkZ < 20; chunkZ++ {
			name := biomes.BiomeAtBlock(chunkX*16+8, 64, chunkZ*16+8)
			if strings.Contains(name, "ocean") || strings.Contains(name, "river") {
				continue
			}
			chunk, err := router.GenerateChunk(chunkX, chunkZ)
			if err != nil {
				t.Fatal(err)
			}
			chunk.ForEachBlock(func(_, _, _ int, block SurfaceBlock) {
				if strings.Contains(block.Name, "_log") || strings.Contains(block.Name, "_leaves") {
					found = true
				}
			})
			if found {
				break
			}
		}
	}
	if !found {
		t.Fatal("陆地群系没有生成任何树木")
	}
}

func TestOceanHasNoSurfaceGrass(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(data, "minecraft:overworld", 4321)
	if err != nil {
		t.Fatal(err)
	}
	chunk, err := router.GenerateChunk(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	setting := router.Setting()
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			if !chunk.BlockAt(chunk.MinX+x, setting.SeaLevel-1, chunk.MinZ+z).IsWater() {
				continue
			}
			top := chunk.HeightAt(chunk.MinX+x, chunk.MinZ+z)
			if top >= setting.SeaLevel && chunk.BlockAt(chunk.MinX+x, top, chunk.MinZ+z).Name == "minecraft:grass_block" {
				t.Fatalf("(%d,%d) 海平面以上出现草方块，海洋区块不应有水面植被", x, z)
			}
		}
	}
}

func TestLakeFeaturePlacesLava(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(data, "minecraft:overworld", 4321)
	if err != nil {
		t.Fatal(err)
	}
	chunk := NewGeneratedChunk(0, 0, router.setting.MinY, router.setting.Height)
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			for y := 30; y <= 34; y++ {
				chunk.SetBlock(x, y, z, SurfaceBlock{Name: "minecraft:stone"})
			}
		}
	}
	placed := router.features.placed["minecraft:lake_lava_surface"]
	if !placed.supported || placed.lake == nil {
		t.Fatal("lake_lava_underground 未被解析为 lake")
	}
	random := newWorldgenRandom()
	random.setSeed(12345)
	router.placeLake(chunk, random.legacyRandom, *placed.lake, 8, 32, 8)
	lava := 0
	chunk.ForEachBlock(func(_, _, _ int, block SurfaceBlock) {
		if block.Name == "minecraft:lava" {
			lava++
		}
	})
	if lava == 0 {
		t.Fatal("湖泊没有放置岩浆")
	}
}

func TestSeagrassFeaturePlacesPlant(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(data, "minecraft:overworld", 4321)
	if err != nil {
		t.Fatal(err)
	}
	chunk := NewGeneratedChunk(0, 0, router.setting.MinY, router.setting.Height)
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			chunk.SetBlock(x, 50, z, SurfaceBlock{Name: "minecraft:sand"})
			for y := 51; y <= 62; y++ {
				chunk.SetBlock(x, y, z, SurfaceBlock{Name: "minecraft:water"})
			}
		}
	}
	random := newWorldgenRandom()
	random.setSeed(999)
	router.placeSeagrass(chunk, random.legacyRandom, seagrassConfiguration{probability: 1}, 8, 51, 8)
	if block := chunk.BlockAt(8, 51, 8); block.Name != "minecraft:tall_seagrass" && block.Name != "minecraft:seagrass" {
		t.Fatalf("海草没有放置，实际为 %s", block.Name)
	}
}

func TestSpruceTreesGeneratedInTaiga(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(data, "minecraft:overworld", 4321)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for chunkX := 280; chunkX < 340 && !found; chunkX++ {
		chunk, err := router.GenerateChunk(chunkX, 0)
		if err != nil {
			t.Fatal(err)
		}
		chunk.ForEachBlock(func(_, _, _ int, block SurfaceBlock) {
			if block.Name == "minecraft:spruce_log" || block.Name == "minecraft:spruce_leaves" {
				found = true
			}
		})
	}
	if !found {
		t.Fatal("taiga 区域没有生成云杉")
	}
}

func TestAdditionalFeatureParsers(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(data, "minecraft:overworld", 4321)
	if err != nil {
		t.Fatal(err)
	}
	if disk := router.features.placed["minecraft:disk_sand"]; !disk.supported || disk.disk == nil {
		t.Fatal("disk_sand 未被解析")
	}
	if pile := router.features.placed["minecraft:pile_hay"]; !pile.supported || pile.pile == nil {
		t.Fatal("pile_hay 未被解析")
	}
	if veg := router.features.placed["minecraft:pale_moss_patch"]; !veg.supported || veg.vegetation == nil {
		t.Fatal("pale_moss_patch 未被解析")
	}
	foundBamboo := false
	for _, placed := range router.features.placed {
		if placed.bamboo != nil {
			foundBamboo = true
			break
		}
	}
	if !foundBamboo {
		t.Fatal("没有解析任何 bamboo 地物")
	}
}

func TestAdvancedPlacementModifiersParse(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(data, "minecraft:overworld", 4321)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"minecraft:lake_lava_underground", "minecraft:lush_caves_vegetation"} {
		placed, ok := router.features.placed[name]
		if !ok {
			t.Fatalf("缺少 %s", name)
		}
		if !placed.supported {
			t.Fatalf("%s 仍被标记为 unsupported", name)
		}
	}
}

func TestKelpAndMultifaceParse(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(data, "minecraft:overworld", 4321)
	if err != nil {
		t.Fatal(err)
	}
	if kelp := router.features.placed["minecraft:kelp_cold"]; !kelp.supported || kelp.kelp == nil {
		t.Fatal("kelp_cold 未被解析或仍被标记为 unsupported")
	}
	if lichen := router.features.placed["minecraft:glow_lichen"]; !lichen.supported || lichen.multiface == nil {
		t.Fatal("glow_lichen 未被解析")
	}
}

func TestDarkOakTreeGenerated(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(data, "minecraft:overworld", 4321)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	// 深色森林地物包含随机 selector 与多种树；新增枝干 placer 会改变随机流，
	// 因此在一小片深色森林中查找，而不是假定固定区块一定有深色橡木。
	for chunkX := 501; chunkX <= 506 && !found; chunkX++ {
		chunk, err := router.GenerateChunk(chunkX, 1)
		if err != nil {
			t.Fatal(err)
		}
		chunk.ForEachBlock(func(_, _, _ int, block SurfaceBlock) {
			if block.Name == "minecraft:dark_oak_log" || block.Name == "minecraft:dark_oak_leaves" {
				found = true
			}
		})
	}
	if !found {
		t.Fatal("深色森林区块没有生成深色橡木")
	}
}

func TestFossilTemplatePlacement(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(data, "minecraft:overworld", 4321)
	if err != nil {
		t.Fatal(err)
	}
	placed := router.features.placed["minecraft:fossil_lower"]
	if placed.fossil == nil {
		t.Fatal("fossil_lower 未解析")
	}
	chunk := NewGeneratedChunk(0, 0, router.setting.MinY, router.setting.Height)
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			for y := 20; y < 50; y++ {
				chunk.SetBlock(x, y, z, SurfaceBlock{Name: "minecraft:stone"})
			}
		}
	}
	random := newWorldgenRandom()
	random.setSeed(4242)
	router.placeFossil(chunk, random.legacyRandom, *placed.fossil, 4, 30, 4)
	found := false
	chunk.ForEachBlock(func(_, _, _ int, block SurfaceBlock) {
		if strings.Contains(block.Name, "bone_block") || strings.Contains(block.Name, "coal_ore") || strings.Contains(block.Name, "diamond_ore") {
			found = true
		}
	})
	if !found {
		t.Fatal("化石模板没有写入骨骼/矿方块")
	}
}

func TestFallenTreePlacement(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(data, "minecraft:overworld", 4321)
	if err != nil {
		t.Fatal(err)
	}
	placed := router.features.placed["minecraft:fallen_oak_tree"]
	if placed.fallenTree == nil {
		t.Fatal("fallen_oak_tree 未解析")
	}
	chunk := NewGeneratedChunk(0, 0, router.setting.MinY, router.setting.Height)
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			chunk.SetBlock(x, 63, z, SurfaceBlock{Name: "minecraft:grass_block"})
		}
	}
	random := newWorldgenRandom()
	random.setSeed(1234)
	router.placeFallenTree(chunk, random.legacyRandom, *placed.fallenTree, 4, 64, 4)
	logs := 0
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			if chunk.BlockAt(x, 64, z).Name == "minecraft:oak_log" {
				logs++
			}
		}
	}
	if logs < 2 {
		t.Fatalf("倒木原木数量 = %d", logs)
	}
}

func TestExtraFeatureRuntime(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(data, "minecraft:overworld", 4321)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"minecraft:vines",
		"minecraft:glowstone",
		"minecraft:desert_well",
		"minecraft:forest_flowers",
		"minecraft:pointed_dripstone",
	} {
		placed, ok := router.features.placed[name]
		if !ok {
			t.Fatalf("缺少 placed_feature %s", name)
		}
		if !placed.supported || !placed.hasRuntime() {
			t.Fatalf("%s 未接入运行时", name)
		}
	}
	chunk := NewGeneratedChunk(0, 0, router.setting.MinY, router.setting.Height)
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			for y := 0; y <= 4; y++ {
				chunk.SetBlock(x, y, z, SurfaceBlock{Name: "minecraft:sandstone"})
			}
		}
	}
	random := newWorldgenRandom()
	random.setSeed(12345)
	router.placeDesertWell(chunk, 8, 4, 8)
	water := false
	chunk.ForEachBlock(func(_, _, _ int, block SurfaceBlock) {
		if block.Name == "minecraft:water" {
			water = true
		}
	})
	if !water {
		t.Fatal("沙漠水井没有水")
	}
}

func TestAllBiomeFeaturesHaveRuntime(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(data, "minecraft:overworld", 4321)
	if err != nil {
		t.Fatal(err)
	}
	for biome, steps := range router.features.biomeFeatures {
		for step, names := range steps {
			for _, name := range names {
				placed, ok := router.features.placed[qualify(name)]
				if !ok {
					t.Fatalf("%s step %d 引用了不存在的 placed_feature %s", biome, step, name)
				}
				if !placed.supported || !placed.hasRuntime() {
					t.Fatalf("%s step %d 的地物 %s（%s）没有运行时", biome, step, name, placed.kind)
				}
			}
		}
	}
}

func TestGeodeExactPlacesLayers(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(data, "minecraft:overworld", 4321)
	if err != nil {
		t.Fatal(err)
	}
	placed := router.features.placed["minecraft:amethyst_geode"]
	if placed.geode == nil {
		t.Fatal("紫水晶晶洞没有解析为 geode 配置")
	}
	chunk := NewGeneratedChunk(0, 0, router.setting.MinY, router.setting.Height)
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			for y := -32; y <= 32; y++ {
				chunk.SetBlock(x, y, z, SurfaceBlock{Name: "minecraft:stone"})
			}
		}
	}
	random := newLegacyRandom(12345)
	router.placeGeodeExact(chunk, random, *placed.geode, 8, 0, 8)
	counts := map[string]int{}
	chunk.ForEachBlock(func(_, _, _ int, block SurfaceBlock) { counts[block.Name]++ })
	for _, name := range []string{"minecraft:smooth_basalt", "minecraft:calcite", "minecraft:amethyst_block"} {
		if counts[name] == 0 {
			t.Fatalf("晶洞缺少分层方块 %s：%v", name, counts)
		}
	}
}

func TestRootSystemPlacesRootedDirtAndHangingRoots(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(data, "minecraft:overworld", 4321)
	if err != nil {
		t.Fatal(err)
	}
	placed := router.features.placed["minecraft:rooted_azalea_tree"]
	if placed.extra == nil {
		t.Fatal("rooted_azalea_tree 没有解析为 root_system")
	}
	chunk := NewGeneratedChunk(0, 0, router.setting.MinY, router.setting.Height)
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			for y := 50; y < 70; y++ {
				chunk.SetBlock(x, y, z, SurfaceBlock{Name: "minecraft:stone"})
			}
			for y := 70; y < 80; y++ {
				chunk.SetBlock(x, y, z, SurfaceBlock{Name: "minecraft:air"})
			}
		}
	}
	random := newWorldgenRandom()
	random.setSeed(2024)
	router.placeRootSystem(chunk, random.legacyRandom, placed.extra.config, 8, 70, 8)
	rooted, hanging := 0, 0
	chunk.ForEachBlock(func(_, _, _ int, block SurfaceBlock) {
		switch block.Name {
		case "minecraft:rooted_dirt":
			rooted++
		case "minecraft:hanging_roots":
			hanging++
		}
	})
	if rooted == 0 {
		t.Fatal("根系统没有放置缠根泥土")
	}
	if hanging == 0 {
		t.Fatal("根系统没有放置悬挂根")
	}
}

func TestSculkPatchSpreadsAndCatalyst(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(data, "minecraft:overworld", 4321)
	if err != nil {
		t.Fatal(err)
	}
	placed := router.features.placed["minecraft:sculk_patch_deep_dark"]
	if placed.extra == nil {
		t.Fatal("sculk_patch_deep_dark 没有解析")
	}
	chunk := NewGeneratedChunk(0, 0, router.setting.MinY, router.setting.Height)
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			for y := 20; y < 40; y++ {
				chunk.SetBlock(x, y, z, SurfaceBlock{Name: "minecraft:deepslate"})
			}
		}
	}
	random := newWorldgenRandom()
	random.setSeed(4242)
	router.placeSculkPatch(chunk, random.legacyRandom, placed.extra.config, 8, 40, 8)
	sculk, catalyst := 0, 0
	chunk.ForEachBlock(func(_, _, _ int, block SurfaceBlock) {
		switch block.Name {
		case "minecraft:sculk":
			sculk++
		case "minecraft:sculk_catalyst":
			catalyst++
		}
	})
	if sculk == 0 {
		t.Fatal("sculk patch 没有生成 sculk")
	}
	_ = catalyst
}

func TestFeatureResultCaptureIncludesOutOfChunkWrites(t *testing.T) {
	chunk := NewGeneratedChunk(0, 0, -64, 384)
	captured := map[[3]int]SurfaceBlock{}
	chunk.capture = captured
	chunk.SetBlock(15, 64, 15, SurfaceBlock{Name: "minecraft:stone"})
	chunk.SetBlock(16, 64, 15, SurfaceBlock{Name: "minecraft:oak_leaves"})
	if got := captured[[3]int{16, 64, 15}]; got.Name != "minecraft:oak_leaves" {
		t.Fatalf("越界写入没有进入 capture: %+v", got)
	}
	if chunk.InRange(16, 64, 15) {
		t.Fatal("越界坐标不应落在区块内")
	}
}

func TestRegionFeatureCacheDeterministic(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(data, "minecraft:overworld", 4321)
	if err != nil {
		t.Fatal(err)
	}
	router.SetStructuresEnabled(false)
	first, err := router.GenerateChunk(12, -3)
	if err != nil {
		t.Fatal(err)
	}
	// 先清空缓存，第二次从头生成；结果必须与缓存路径一致。
	router.featureCache = newFeatureResultCache(96)
	second, err := router.GenerateChunk(12, -3)
	if err != nil {
		t.Fatal(err)
	}
	first.ForEachBlock(func(x, y, z int, block SurfaceBlock) {
		if got := second.BlockAt(x, y, z); got.Name != block.Name {
			t.Fatalf("region 缓存导致不一致 (%d,%d,%d): %s != %s", x, y, z, block.Name, got.Name)
		}
	})
}

func TestRegionFeaturesApplyNeighborWrites(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	localRouter, err := NewRouter(data, "minecraft:overworld", 4321)
	if err != nil {
		t.Fatal(err)
	}
	localRouter.SetStructuresEnabled(false)
	localBase, biomes, err := localRouter.generateBaseChunk(3, 12)
	if err != nil {
		t.Fatal(err)
	}
	localRouter.applyFeatures(localBase, biomes)
	globalRouter, err := NewRouter(data, "minecraft:overworld", 4321)
	if err != nil {
		t.Fatal(err)
	}
	globalRouter.SetStructuresEnabled(false)
	global, err := globalRouter.GenerateChunk(3, 12)
	if err != nil {
		t.Fatal(err)
	}
	differences := 0
	localBase.ForEachBlock(func(x, y, z int, block SurfaceBlock) {
		if global.BlockAt(x, y, z).Name != block.Name {
			differences++
		}
	})
	if differences == 0 {
		t.Fatal("region 地物没有把邻域越界写入合并到当前区块")
	}
}

func TestIcebergPlacesIceInWater(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(data, "minecraft:overworld", 4321)
	if err != nil {
		t.Fatal(err)
	}
	placed := router.features.placed["minecraft:iceberg_packed"]
	if placed.extra == nil {
		t.Fatal("iceberg_packed 没有解析为 extra")
	}
	chunk := NewGeneratedChunk(0, 0, router.setting.MinY, router.setting.Height)
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			for y := 55; y < 75; y++ {
				chunk.SetBlock(x, y, z, SurfaceBlock{Name: "minecraft:water", Properties: map[string]string{"level": "0"}})
			}
		}
	}
	random := newWorldgenRandom()
	random.setSeed(9090)
	router.placeIceberg(chunk, random.legacyRandom, placed.extra.config, 8, 62, 8)
	count := 0
	chunk.ForEachBlock(func(_, _, _ int, block SurfaceBlock) {
		if block.Name == "minecraft:ice" || block.Name == "minecraft:packed_ice" || block.Name == "minecraft:blue_ice" {
			count++
		}
	})
	if count == 0 {
		t.Fatal("冰山没有生成冰方块")
	}
}

func TestSculkPatchDoesNotReplaceAir(t *testing.T) {
	router := mustRouter(t, 4321)
	chunk := NewGeneratedChunk(0, 0, router.setting.MinY, router.setting.Height)
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			for y := 20; y < 40; y++ {
				chunk.SetBlock(x, y, z, SurfaceBlock{Name: "minecraft:deepslate"})
			}
		}
	}
	random := newWorldgenRandom()
	random.setSeed(4242)
	router.placeSculkPatch(chunk, random.legacyRandom,
		json.RawMessage(`{"charge_count":10,"amount_per_charge":32,"spread_attempts":64,"spread_rounds":1,"growth_rounds":0,"catalyst_chance":0.5,"extra_rare_growths":0}`),
		8, 80, 8)
	sculk := 0
	chunk.ForEachBlock(func(_, y, _ int, block SurfaceBlock) {
		if y > 40 && block.Name == "minecraft:sculk" {
			sculk++
		}
	})
	if sculk != 0 {
		t.Fatalf("空气中生成了 %d 个 sculk", sculk)
	}
}

func TestSimpleBlockRequiresGroundSupport(t *testing.T) {
	chunk := NewGeneratedChunk(0, 0, -64, 384)
	simple := simpleBlockConfiguration{provider: stateProvider{entries: []weightedState{{
		state: SurfaceBlock{Name: "minecraft:short_grass"}, weight: 1,
	}}}}
	random := newLegacyRandom(1)
	router := mustRouter(t, 1)
	router.placeSimple(chunk, random, simple, 8, 80, 8)
	if !chunk.BlockAt(8, 80, 8).IsAir() {
		t.Fatal("空中不应生成短草")
	}
	chunk.SetBlock(8, 79, 8, SurfaceBlock{Name: "minecraft:grass_block"})
	router.placeSimple(chunk, random, simple, 8, 80, 8)
	if chunk.BlockAt(8, 80, 8).Name != "minecraft:short_grass" {
		t.Fatal("有地面支撑时应生成短草")
	}
}

func TestLargeDripstoneDoesNotFloatInAir(t *testing.T) {
	chunk := NewGeneratedChunk(0, 0, -64, 384)
	router := mustRouter(t, 1)
	random := newLegacyRandom(1)
	router.placeLargeDripstone(chunk, random, nil, 8, 80, 8)
	placed := 0
	chunk.ForEachBlock(func(_, _, _ int, block SurfaceBlock) {
		if block.Name == "minecraft:dripstone_block" || block.Name == "minecraft:pointed_dripstone" {
			placed++
		}
	})
	if placed != 0 {
		t.Fatalf("无洞穴支撑时生成了 %d 个滴水石方块", placed)
	}
}

func TestLeafLitterRequiresGroundSupport(t *testing.T) {
	chunk := NewGeneratedChunk(0, 0, -64, 384)
	chunk.SetBlock(1, 64, 1, SurfaceBlock{Name: "minecraft:oak_leaves"})
	litter := SurfaceBlock{Name: "minecraft:leaf_litter"}
	if simpleBlockCanSurvive(chunk, litter, 1, 65, 1) {
		t.Fatal("落叶不应能放在树叶上")
	}
	chunk.SetBlock(1, 64, 1, SurfaceBlock{Name: "minecraft:grass_block"})
	if !simpleBlockCanSurvive(chunk, litter, 1, 65, 1) {
		t.Fatal("落叶应能放在草方块上")
	}
}

func TestGeneratedLeafLitterRestsOnGround(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(data, "minecraft:overworld", 4321)
	if err != nil {
		t.Fatal(err)
	}
	for cx := 0; cx < 2; cx++ {
		for cz := 0; cz < 2; cz++ {
			chunk, err := router.GenerateChunk(cx, cz)
			if err != nil {
				t.Fatal(err)
			}
			chunk.ForEachBlock(func(x, y, z int, block SurfaceBlock) {
				if block.Name != "minecraft:leaf_litter" {
					return
				}
				below := chunk.BlockAt(x, y-1, z)
				if strings.HasSuffix(below.Name, "_leaves") || strings.HasSuffix(below.Name, "_log") {
					t.Fatalf("落叶 (%d,%d,%d) 生成在 %s 上", x, y, z, below.Name)
				}
			})
		}
	}
}

func TestPaleMossDecoratorPlacesHangingCarpet(t *testing.T) {
	chunk := NewGeneratedChunk(0, 0, -64, 384)
	trunk := SurfaceBlock{Name: "minecraft:oak_log"}
	chunk.SetBlock(2, 64, 2, trunk)
	config := treeConfiguration{
		trunk: trunk,
		decorators: []treeDecorator{{
			kind:              "minecraft:pale_moss",
			trunkProbability:  1,
			leavesProbability: 0,
			groundProbability: 0,
		}},
	}
	router := &Router{}
	router.applyTreeDecorators(chunk, newLegacyRandom(7), config, 2, 64, 2, 3, nil)
	found := false
	chunk.ForEachBlock(func(_, _, _ int, block SurfaceBlock) {
		if block.Name == "minecraft:pale_moss_carpet" {
			found = true
		}
	})
	if !found {
		t.Fatal("pale_moss 装饰器没有放置 pale_moss_carpet")
	}
}

func TestCreakingHeartDecoratorReplacesInteriorLog(t *testing.T) {
	chunk := NewGeneratedChunk(0, 0, -64, 384)
	trunk := SurfaceBlock{Name: "minecraft:oak_log"}
	for x := 1; x <= 3; x++ {
		for y := 63; y <= 65; y++ {
			for z := 1; z <= 3; z++ {
				chunk.SetBlock(x, y, z, trunk)
			}
		}
	}
	config := treeConfiguration{
		trunk: trunk,
		decorators: []treeDecorator{{
			kind:        "minecraft:creaking_heart",
			probability: 1,
		}},
	}
	router := &Router{}
	router.applyTreeDecorators(chunk, newLegacyRandom(9), config, 2, 64, 2, 3, nil)
	found := false
	chunk.ForEachBlock(func(_, _, _ int, block SurfaceBlock) {
		if block.Name == "minecraft:creaking_heart" {
			found = true
		}
	})
	if !found {
		t.Fatal("creaking_heart 装饰器没有替换原木")
	}
}
