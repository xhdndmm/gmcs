package vanilla

import "testing"

func TestVillageJigsawStart(t *testing.T) {
	runtime, err := loadStructureRuntime()
	if err != nil {
		t.Fatal(err)
	}
	setIndex := -1
	for i, set := range runtime.sets {
		if set.Name == "minecraft:villages" {
			setIndex = i
			break
		}
	}
	if setIndex < 0 {
		t.Fatal("缺少 villages 结构集")
	}
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
	start := runtime.cachedStart(4321, setIndex, 52, 153, biomes)
	if start == nil {
		t.Fatal("村庄起点未生成")
	}
	if len(start.pieces) < 2 {
		t.Fatalf("村庄 piece 数 = %d", len(start.pieces))
	}
	chunk, err := router.GenerateChunk(52, 153)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	chunk.ForEachBlock(func(_, _, _ int, block SurfaceBlock) {
		switch block.Name {
		case "minecraft:oak_planks", "minecraft:cobblestone", "minecraft:oak_log":
			found = true
		}
	})
	if !found {
		t.Fatal("村庄区块没有结构方块")
	}
}

func TestRandomSpreadOriginDeterministic(t *testing.T) {
	runtime, err := loadStructureRuntime()
	if err != nil {
		t.Fatal(err)
	}
	for _, set := range runtime.sets {
		x1, z1 := randomSpreadOrigin(4321, set, 3, -2)
		x2, z2 := randomSpreadOrigin(4321, set, 3, -2)
		if x1 != x2 || z1 != z2 {
			t.Fatalf("%s 的起点不确定", set.Name)
		}
	}
}

func TestProcessorListApplied(t *testing.T) {
	runtime, err := loadStructureRuntime()
	if err != nil {
		t.Fatal(err)
	}
	if len(runtime.data.ProcessorLists) == 0 {
		t.Fatal("没有 processor_list 数据")
	}
	template, err := runtime.data.ParsedTemplate("minecraft:ancient_city/city_center/city_center_1")
	if err != nil {
		t.Fatalf("模板加载失败: %v", err)
	}
	random := newLegacyRandom(4321)
	processed := runtime.applyProcessorList(template, "minecraft:ancient_city_generic_degradation", random)
	if processed == nil || len(processed.Blocks) == 0 {
		t.Fatal("processor_list 应用后模板为空")
	}
}
