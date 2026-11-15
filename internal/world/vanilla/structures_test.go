package vanilla

import (
	"testing"

	"gmcs/internal/registry"
)

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
	start := runtime.cachedStart(4321, setIndex, 52, 153, biomes, router)
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

func TestEndCityTemplatesExist(t *testing.T) {
	data, err := LoadStructureData()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"minecraft:end_city/base_floor",
		"minecraft:end_city/second_floor_1",
		"minecraft:end_city/third_floor_1",
		"minecraft:end_city/third_roof",
	} {
		template, err := data.ParsedTemplate(name)
		if err != nil || template == nil || len(template.Blocks) == 0 {
			t.Fatalf("末地城模板 %s 不可用：%v", name, err)
		}
	}
}

func TestEndCityBaseAssembly(t *testing.T) {
	runtime, err := loadStructureRuntime()
	if err != nil {
		t.Fatal(err)
	}
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(data, "minecraft:end", 4321)
	if err != nil {
		t.Fatal(err)
	}
	start := runtime.buildEndCity(4321, 40, 72, router)
	if start == nil || len(start.pieces) < 4 {
		t.Fatalf("末地城基础拼装失败：%+v", start)
	}
	foundBase, foundRoof := false, false
	for _, piece := range start.pieces {
		switch piece.name {
		case "minecraft:end_city/base_floor":
			foundBase = true
		case "minecraft:end_city/third_roof":
			foundRoof = true
		}
	}
	if !foundBase || !foundRoof {
		t.Fatalf("末地城缺少基础/顶部模板：%+v", start.pieces)
	}
}

func TestTemplateBlockEntityItems(t *testing.T) {
	template := &structureTemplate{
		Size: [3]int{1, 1, 1},
		Palette: []SurfaceBlock{
			{Name: "minecraft:chest", Properties: map[string]string{"facing": "north"}},
		},
		Blocks: []templateBlock{{
			Pos: [3]int{0, 0, 0}, State: 0,
			NBT: nbtCompound{
				"id": "minecraft:chest",
				"Items": []any{
					nbtCompound{"id": "minecraft:golden_apple", "count": int8(1), "Slot": int8(13)},
					nbtCompound{"id": "minecraft:bread", "Count": int8(3)},
				},
			},
		}},
	}
	chunk := NewGeneratedChunk(0, 0, -64, 384)
	placeTemplate(chunk, template, 3, 64, 4, rotateNone, false, newLegacyRandom(1))
	entity, ok := chunk.BlockEntityAt(3, 64, 4)
	if !ok {
		t.Fatal("模板箱子没有生成方块实体")
	}
	if entity.Type != "minecraft:chest" || len(entity.Items) != 2 {
		t.Fatalf("方块实体数据不符：%+v", entity)
	}
	if entity.Items[0].Name != "minecraft:golden_apple" || entity.Items[0].Count != 1 {
		t.Fatalf("第一个物品不符：%+v", entity.Items[0])
	}
	if entity.Items[1].Name != "minecraft:bread" || entity.Items[1].Count != 3 {
		t.Fatalf("第二个物品不符：%+v", entity.Items[1])
	}
}

func TestGenerateBlockEntityFromLootTableNBT(t *testing.T) {
	entity, ok := generatedBlockEntityFromNBT("minecraft:chest", nbtCompound{
		"id":        "minecraft:chest",
		"LootTable": "minecraft:chests/end_city_treasure",
	})
	if !ok || entity.LootTable != "minecraft:chests/end_city_treasure" {
		t.Fatalf("战利品表 NBT 未保留：%+v, ok=%v", entity, ok)
	}
}

func TestLootTableEvaluatesEndCityTreasure(t *testing.T) {
	data, err := LoadStructureData()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := data.LootTables["minecraft:chests/end_city_treasure"]; !ok {
		t.Fatal("缺少 end_city_treasure 战利品表")
	}
	items := data.LootItems("minecraft:chests/end_city_treasure", newLegacyRandom(4321))
	if len(items) == 0 {
		t.Fatal("战利品表没有生成物品")
	}
	for _, item := range items {
		if item.Name == "" || item.Count <= 0 {
			t.Fatalf("非法战利品条目：%+v", item)
		}
		if _, err := registry.ItemID(item.Name); err != nil {
			t.Fatalf("战利品物品未收录：%s", item.Name)
		}
	}
}

func TestTemplateLootTableBlockEntity(t *testing.T) {
	template := &structureTemplate{
		Size:    [3]int{1, 1, 1},
		Palette: []SurfaceBlock{{Name: "minecraft:chest"}},
		Blocks: []templateBlock{{
			Pos: [3]int{0, 0, 0}, State: 0,
			NBT: nbtCompound{"id": "minecraft:chest", "LootTable": "minecraft:chests/abandoned_mineshaft"},
		}},
	}
	chunk := NewGeneratedChunk(0, 0, -64, 384)
	placeTemplate(chunk, template, 3, 64, 4, rotateNone, false, newLegacyRandom(99))
	entity, ok := chunk.BlockEntityAt(3, 64, 4)
	if !ok {
		t.Fatal("战利品箱没有生成方块实体")
	}
	if entity.Type != "minecraft:chest" || len(entity.Items) == 0 || entity.LootTable == "" {
		t.Fatalf("战利品箱数据不符：%+v", entity)
	}
}

func TestMineshaftAndStrongholdBuilders(t *testing.T) {
	runtime, err := loadStructureRuntime()
	if err != nil {
		t.Fatal(err)
	}
	mineshaft, ok := runtime.defs["minecraft:mineshaft"]
	if !ok {
		t.Fatal("缺少矿洞结构定义")
	}
	start := runtime.buildMineshaft(4321, 0, 0, mineshaft, nil)
	if start == nil || len(start.pieces) == 0 || len(start.pieces[0].blocks) == 0 {
		t.Fatal("矿洞生成器没有产生方块")
	}
	if _, ok := runtime.defs["minecraft:stronghold"]; !ok {
		t.Fatal("缺少要塞结构定义")
	}
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(data, "minecraft:overworld", 4321)
	if err != nil {
		t.Fatal(err)
	}
	start = runtime.buildStronghold(4321, 0, 0, router)
	if start == nil || len(start.pieces) == 0 || len(start.pieces[0].blocks) == 0 {
		t.Fatal("要塞生成器没有产生方块")
	}
	hasPortal := false
	for _, block := range start.pieces[0].blocks {
		if block.state.Name == "minecraft:end_portal_frame" {
			hasPortal = true
			break
		}
	}
	if !hasPortal {
		t.Fatal("要塞没有末地传送门框架")
	}
}

func TestConcentricRingOrigins(t *testing.T) {
	runtime, err := loadStructureRuntime()
	if err != nil {
		t.Fatal(err)
	}
	index := -1
	for i, set := range runtime.sets {
		if set.Name == "minecraft:strongholds" {
			index = i
			break
		}
	}
	if index < 0 {
		t.Fatal("缺少 strongholds 结构集")
	}
	origins := runtime.ringOrigins(4321, index, runtime.sets[index])
	if len(origins) != runtime.sets[index].Count {
		t.Fatalf("要塞环数量 = %d, want %d", len(origins), runtime.sets[index].Count)
	}
	if origins[0] == origins[1] {
		t.Fatal("要塞环原点重复")
	}
}

func TestMineshaftFrequencyFilter(t *testing.T) {
	runtime, err := loadStructureRuntime()
	if err != nil {
		t.Fatal(err)
	}
	var set structureSetDefinition
	found := false
	for _, candidate := range runtime.sets {
		if candidate.Name == "minecraft:mineshafts" {
			set, found = candidate, true
			break
		}
	}
	if !found {
		t.Fatal("缺少 mineshafts 结构集")
	}
	allowed := 0
	const samples = 20000
	for i := 0; i < samples; i++ {
		if structureFrequencyAllows(4321, set, i, i*3) {
			allowed++
		}
	}
	if allowed == 0 || allowed > samples/20 {
		t.Fatalf("矿洞频率过滤结果异常：%d/%d", allowed, samples)
	}
}
