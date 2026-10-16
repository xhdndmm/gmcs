//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package registry

import "testing"

// itemID 测试辅助：按命名空间名取物品 ID（未知名直接失败）。
func itemID(t *testing.T, name string) int32 {
	t.Helper()
	id, err := ItemID(name)
	if err != nil {
		t.Fatalf("未知物品 %s：%v", name, err)
	}
	return id
}

func TestMatchCraftingShaped(t *testing.T) {
	stick := itemID(t, "minecraft:stick")
	plank := itemID(t, "minecraft:oak_planks")
	craftingTable := itemID(t, "minecraft:crafting_table")

	// 2×2 橡木木板 → 工作台。
	grid := []int32{
		plank, plank,
		plank, plank,
	}
	result, count, ok := MatchCrafting(grid, 2, 2)
	if !ok || result != craftingTable || count != 1 {
		t.Fatalf("工作台配方匹配失败：result=%d count=%d ok=%v", result, count, ok)
	}

	// 3×3 中竖排 2 木板 → 木棍（4 个）。
	grid3 := []int32{
		plank, 0, 0,
		plank, 0, 0,
		0, 0, 0,
	}
	result, count, ok = MatchCrafting(grid3, 3, 3)
	if !ok || result != stick || count != 4 {
		t.Fatalf("木棍配方匹配失败：result=%d count=%d ok=%v", result, count, ok)
	}
}

func TestMatchCraftingOffsetAndMirror(t *testing.T) {
	// 木镐图案 ["XXX", " # ", " # "] 放在 3×3 网格中。
	pickaxe := itemID(t, "minecraft:wooden_pickaxe")
	plank := itemID(t, "minecraft:oak_planks")
	stick := itemID(t, "minecraft:stick")
	grid := []int32{
		plank, plank, plank,
		0, stick, 0,
		0, stick, 0,
	}
	result, count, ok := MatchCrafting(grid, 3, 3)
	if !ok || result != pickaxe || count != 1 {
		t.Fatalf("木镐匹配失败：result=%d count=%d ok=%v", result, count, ok)
	}

	// 小图案在大网格中偏移放置：竖排 2 木板放在第 3 列。
	gridOffset := []int32{
		0, 0, plank,
		0, 0, plank,
		0, 0, 0,
	}
	result, count, ok = MatchCrafting(gridOffset, 3, 3)
	if !ok || result != stick || count != 4 {
		t.Fatalf("偏移木棍匹配失败：result=%d count=%d ok=%v", result, count, ok)
	}
}

func TestMatchCraftingRejectsExtraItems(t *testing.T) {
	plank := itemID(t, "minecraft:oak_planks")
	stick := itemID(t, "minecraft:stick")
	// 木棍配方只允许 2 格；多放物品不匹配任何配方。
	grid := []int32{
		plank, stick,
		plank, stick,
	}
	if _, _, ok := MatchCrafting(grid, 2, 2); ok {
		t.Fatal("多余物品不应匹配任何配方")
	}
}

func TestMatchCraftingShapeless(t *testing.T) {
	// 无序配方：圆石 + 闪长岩 → 安山岩 ×2（顺序无关）。
	cobble := itemID(t, "minecraft:cobblestone")
	diorite := itemID(t, "minecraft:diorite")
	andesite := itemID(t, "minecraft:andesite")
	grid := []int32{
		0, 0, diorite,
		0, 0, 0,
		cobble, 0, 0,
	}
	result, count, ok := MatchCrafting(grid, 3, 3)
	if !ok || result != andesite || count != 2 {
		t.Fatalf("安山岩配方匹配失败：result=%d count=%d ok=%v", result, count, ok)
	}
}

func TestMatchCraftingTagIngredient(t *testing.T) {
	// 标签原料：任意木板 2 竖排 → 木棍（木板来自 #minecraft:planks 标签）。
	birch := itemID(t, "minecraft:birch_planks")
	stick := itemID(t, "minecraft:stick")
	grid := []int32{
		birch,
		birch,
	}
	result, count, ok := MatchCrafting(grid, 1, 2)
	if !ok || result != stick || count != 4 {
		t.Fatalf("白桦木棍配方匹配失败：result=%d count=%d ok=%v", result, count, ok)
	}
}

func TestCookingRecipeFor(t *testing.T) {
	ironOre := itemID(t, "minecraft:iron_ore")
	rawBeef := itemID(t, "minecraft:beef")
	ironIngot := itemID(t, "minecraft:iron_ingot")
	steak := itemID(t, "minecraft:cooked_beef")
	stick := itemID(t, "minecraft:stick")

	// 熔炉：矿石与食物都可烹饪。
	recipe, ok := CookingRecipeFor(CookingSmelting, ironOre)
	if !ok || recipe.Result != ironIngot || recipe.CookTime != 200 {
		t.Fatalf("铁矿熔炼配方错误：%+v ok=%v", recipe, ok)
	}
	recipe, ok = CookingRecipeFor(CookingSmelting, rawBeef)
	if !ok || recipe.Result != steak {
		t.Fatalf("牛肉熔炼配方错误：%+v ok=%v", recipe, ok)
	}

	// 高炉只收矿物/装备，不收食物。
	if _, ok := CookingRecipeFor(CookingBlasting, rawBeef); ok {
		t.Fatal("高炉不应有牛肉配方")
	}
	recipe, ok = CookingRecipeFor(CookingBlasting, ironOre)
	if !ok || recipe.CookTime != 100 {
		t.Fatalf("高炉铁矿配方错误：%+v ok=%v", recipe, ok)
	}

	// 烟熏炉只收食物。
	if _, ok := CookingRecipeFor(CookingSmoking, ironOre); ok {
		t.Fatal("烟熏炉不应有铁矿配方")
	}
	if _, ok := CookingRecipeFor(CookingSmoking, rawBeef); !ok {
		t.Fatal("烟熏炉应有牛肉配方")
	}

	// 不可烹饪物品查询为空。
	if _, ok := CookingRecipeFor(CookingSmelting, stick); ok {
		t.Fatal("木棍不应有烹饪配方")
	}
}

func TestRecipeTablesSane(t *testing.T) {
	if len(ShapedRecipes) < 700 {
		t.Fatalf("有序配方数 %d 偏少", len(ShapedRecipes))
	}
	if len(ShapelessRecipes) < 300 {
		t.Fatalf("无序配方数 %d 偏少", len(ShapelessRecipes))
	}
	if len(CookingRecipes) < 110 {
		t.Fatalf("烹饪配方数 %d 偏少", len(CookingRecipes))
	}
	for _, recipe := range ShapedRecipes {
		if int32(len(recipe.Cells)) != recipe.Width*recipe.Height {
			t.Fatalf("配方 %s 网格尺寸与 Cells 不符", recipe.Name)
		}
		for _, cell := range recipe.Cells {
			if cell >= int32(len(recipe.Ingredients)) {
				t.Fatalf("配方 %s 的 Cells 越界：%d", recipe.Name, cell)
			}
		}
		if recipe.Result <= 0 || recipe.ResultCount <= 0 {
			t.Fatalf("配方 %s 结果非法", recipe.Name)
		}
	}
	for _, recipe := range CookingRecipes {
		if recipe.CookTime <= 0 {
			t.Fatalf("烹饪配方 %s 时间非法", recipe.Name)
		}
	}
}

// TestSmithingTrimMatching 验证纹饰配方的匹配与添加物 → 材质解析。
func TestSmithingTrimMatching(t *testing.T) {
	netheriteChest := itemID(t, "minecraft:netherite_chestplate")
	boltTemplate := itemID(t, "minecraft:bolt_armor_trim_smithing_template")
	goldIngot := itemID(t, "minecraft:gold_ingot")

	recipe, ok := MatchSmithingTrim(boltTemplate, netheriteChest, goldIngot)
	if !ok || recipe.Pattern != "minecraft:bolt" {
		t.Fatalf("纹饰配方匹配失败：ok=%v pattern=%q", ok, recipe.Pattern)
	}
	if material, ok := TrimMaterialForItem(goldIngot); !ok || material != "minecraft:gold" {
		t.Fatalf("金锭的纹饰材质 = %q ok=%v, want minecraft:gold", material, ok)
	}
	if _, ok := TrimMaterialForItem(netheriteChest); ok {
		t.Fatal("下界合金胸甲不应提供纹饰材质")
	}
	// 添加物不在 trim_materials 标签内：不匹配。
	if _, ok := MatchSmithingTrim(boltTemplate, netheriteChest, itemID(t, "minecraft:iron_ore")); ok {
		t.Fatal("铁矿不应匹配纹饰配方添加物")
	}
	// 升级配方（下界合金升级）不应被当作纹饰配方。
	if _, ok := MatchSmithingTrim(
		itemID(t, "minecraft:netherite_upgrade_smithing_template"),
		itemID(t, "minecraft:diamond_sword"),
		itemID(t, "minecraft:netherite_ingot")); ok {
		t.Fatal("下界合金升级组合不应匹配纹饰配方")
	}
	// 全部 18 条纹饰配方都有非空图案与原料。
	if len(SmithingTrimRecipes) != 18 {
		t.Fatalf("纹饰配方数 = %d, want 18", len(SmithingTrimRecipes))
	}
	for _, r := range SmithingTrimRecipes {
		if r.Pattern == "" || len(r.Template) == 0 || len(r.Base) == 0 || len(r.Addition) == 0 {
			t.Fatalf("纹饰配方 %s 字段不完整", r.Name)
		}
	}
}

// TestPropertySetsMatchVanilla 验证属性集与官方 1.21.11 服务端 Update Recipes
// 实测一致（集合数量与规模逐一对齐）。
func TestPropertySetsMatchVanilla(t *testing.T) {
	sets := PropertySets()
	want := []struct {
		name  string
		count int
	}{
		{"minecraft:smithing_base", 37},
		{"minecraft:smithing_template", 19},
		{"minecraft:smithing_addition", 11},
		{"minecraft:furnace_input", 156},
		{"minecraft:blast_furnace_input", 62},
		{"minecraft:smoker_input", 9},
		{"minecraft:campfire_input", 9},
	}
	if len(sets) != len(want) {
		t.Fatalf("属性集数量 = %d, want %d", len(sets), len(want))
	}
	for i, w := range want {
		set := sets[i]
		if set.Name != w.name {
			t.Fatalf("属性集[%d] = %s, want %s", i, set.Name, w.name)
		}
		if len(set.Items) != w.count {
			t.Fatalf("属性集 %s 物品数 = %d, want %d（与原版抓包不一致）", set.Name, len(set.Items), w.count)
		}
		for j := 1; j < len(set.Items); j++ {
			if set.Items[j-1] >= set.Items[j] {
				t.Fatalf("属性集 %s 未严格升序去重：位置 %d", set.Name, j)
			}
		}
	}
	// 关键成员抽查。
	templates := sets[1].Items
	if !ContainsItem(templates, itemID(t, "minecraft:bolt_armor_trim_smithing_template")) ||
		!ContainsItem(templates, itemID(t, "minecraft:netherite_upgrade_smithing_template")) {
		t.Fatal("smithing_template 应包含纹饰模板与下界合金升级模板")
	}
	addition := sets[2].Items
	if !ContainsItem(addition, itemID(t, "minecraft:gold_ingot")) ||
		!ContainsItem(addition, itemID(t, "minecraft:netherite_ingot")) {
		t.Fatal("smithing_addition 应包含金锭与下界合金锭")
	}
	base := sets[0].Items
	if !ContainsItem(base, itemID(t, "minecraft:diamond_sword")) ||
		!ContainsItem(base, itemID(t, "minecraft:netherite_chestplate")) {
		t.Fatal("smithing_base 应包含钻石剑与下界合金胸甲")
	}
}
