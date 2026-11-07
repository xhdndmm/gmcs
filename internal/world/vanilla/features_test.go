package vanilla

import (
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
			top := chunk.HeightAt(chunk.MinX+x, chunk.MinZ+z)
			if top >= setting.SeaLevel {
				t.Fatalf("(%d,%d) 顶部 %d 高于海平面，海洋区块不应有水面植被", x, z, top)
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
