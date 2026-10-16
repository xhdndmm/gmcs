//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package registry

import (
	"sort"
	"sync"
)

// 配方匹配：在给定合成格（物品 ID 网格）中查找配方，以及按容器类型查询烹饪配方。
//
// 合成格表示：grid 为 width×height 行优先的物品 ID 数组，0 表示空格。
// 有序配方支持水平镜像匹配（与原版 ShapedRecipe 一致）；小图案可在大网格中
// 任意偏移放置，网格中图案外的格子必须为空。

// PropertySet 是一个配方属性集（原版 RecipePropertySet：
// 注册键 + 物品集合）。客户端用它构造锻造台槽位过滤（smithing_*）
// 与配方书筛选（*_input）。
type PropertySet struct {
	// Name 是注册键（如 "minecraft:smithing_template"）。
	Name string
	// Items 是物品注册表 ID（升序去重；集合语义，顺序不敏感）。
	Items []int32
}

// MatchCrafting 在合成格中查找配方并返回产物。
// grid 为 width×height 行优先物品 ID（0 = 空格）；找不到配方时 ok 为 false。
func MatchCrafting(grid []int32, width, height int32) (result int32, count int32, ok bool) {
	if int32(len(grid)) != width*height {
		return 0, 0, false
	}
	if r, ok := matchShaped(grid, width, height); ok {
		return r.Result, r.ResultCount, true
	}
	if r, ok := matchShapeless(grid, width, height); ok {
		return r.Result, r.ResultCount, true
	}
	return 0, 0, false
}

// matchShaped 尝试全部有序配方（含镜像与偏移放置）。
func matchShaped(grid []int32, width, height int32) (ShapedRecipe, bool) {
	for _, recipe := range ShapedRecipes {
		if recipe.Width > width || recipe.Height > height {
			continue
		}
		if matchShapedAt(recipe, grid, width, height, false) {
			return recipe, true
		}
		if matchShapedAt(recipe, grid, width, height, true) {
			return recipe, true
		}
	}
	return ShapedRecipe{}, false
}

// matchShapedAt 在给定偏移下匹配一次；mirror 表示水平镜像图案。
func matchShapedAt(recipe ShapedRecipe, grid []int32, width, height int32, mirror bool) bool {
	for oy := int32(0); oy+recipe.Height <= height; oy++ {
		for ox := int32(0); ox+recipe.Width <= width; ox++ {
			if shapedFitsAt(recipe, grid, width, height, ox, oy, mirror) {
				return true
			}
		}
	}
	return false
}

// shapedFitsAt 检查图案（可镜像）能否放置在 (ox, oy) 且其余格为空。
func shapedFitsAt(recipe ShapedRecipe, grid []int32, width, height, ox, oy int32, mirror bool) bool {
	for y := int32(0); y < recipe.Height; y++ {
		for x := int32(0); x < recipe.Width; x++ {
			// 镜像时图案列取反（原版只水平镜像）。
			px := x
			if mirror {
				px = recipe.Width - 1 - x
			}
			cell := recipe.Cells[y*recipe.Width+px]
			gridItem := grid[(oy+y)*width+(ox+x)]
			if cell < 0 {
				if gridItem != 0 {
					return false
				}
				continue
			}
			if !ingredientAccepts(recipe.Ingredients[cell], gridItem) {
				return false
			}
		}
	}
	// 图案外的格子必须为空（原版语义）。
	for y := int32(0); y < height; y++ {
		for x := int32(0); x < width; x++ {
			if x >= ox && x < ox+recipe.Width && y >= oy && y < oy+recipe.Height {
				continue
			}
			if grid[y*width+x] != 0 {
				return false
			}
		}
	}
	return true
}

// matchShapeless 尝试全部无序配方（多重集匹配，忽略位置与顺序）。
func matchShapeless(grid []int32, width, height int32) (ShapelessRecipe, bool) {
	// 统计网格中的非空物品。
	var items []int32
	for _, id := range grid {
		if id != 0 {
			items = append(items, id)
		}
	}
	if len(items) == 0 {
		return ShapelessRecipe{}, false
	}
	for _, recipe := range ShapelessRecipes {
		if len(recipe.Ingredients) != len(items) {
			continue
		}
		if shapelessMatches(recipe, items) {
			return recipe, true
		}
	}
	return ShapelessRecipe{}, false
}

// shapelessMatches 检查物品多重集能否与原料一一配对（回溯匹配）。
func shapelessMatches(recipe ShapelessRecipe, items []int32) bool {
	used := make([]bool, len(items))
	var assign func(slot int) bool
	assign = func(slot int) bool {
		if slot == len(recipe.Ingredients) {
			return true
		}
		for i, id := range items {
			if used[i] || !ingredientAccepts(recipe.Ingredients[slot], id) {
				continue
			}
			used[i] = true
			if assign(slot + 1) {
				return true
			}
			used[i] = false
		}
		return false
	}
	return assign(0)
}

// ingredientAccepts 报告物品 ID 是否属于原料集合。
func ingredientAccepts(ingredient Ingredient, itemID int32) bool {
	if itemID == 0 {
		return false
	}
	for _, id := range ingredient {
		if id == itemID {
			return true
		}
	}
	return false
}

// ContainsItem 报告升序物品 ID 列表是否包含指定物品（二分查找；
// 用于属性集与配置驱动集合的成员检查）。
func ContainsItem(items []int32, id int32) bool {
	i := sort.Search(len(items), func(i int) bool { return items[i] >= id })
	return i < len(items) && items[i] == id
}

// CookingRecipeFor 查询指定容器类型下可烹饪该物品的配方。
func CookingRecipeFor(kind CookingKind, itemID int32) (CookingRecipe, bool) {
	if itemID == 0 {
		return CookingRecipe{}, false
	}
	for _, recipe := range CookingRecipes {
		if recipe.Kind != kind {
			continue
		}
		if ingredientAccepts(recipe.Ingredient, itemID) {
			return recipe, true
		}
	}
	return CookingRecipe{}, false
}

// stonecuttingIndex 是输入物品 ID → 切石机配方下标（生成顺序，稳定）。
var stonecuttingIndex = func() map[int32][]int32 {
	index := make(map[int32][]int32)
	for i, recipe := range StonecuttingRecipes {
		for _, id := range recipe.Ingredient {
			index[id] = append(index[id], int32(i))
		}
	}
	return index
}()

// StonecuttingRecipesFor 返回可用于该输入物品的切石机配方下标
// （顺序与生成表一致；客户端按钮索引使用同一顺序）。
// 无可用配方时返回 nil。
func StonecuttingRecipesFor(input int32) []int32 {
	return stonecuttingIndex[input]
}

// StonecuttingRecipeAt 按下标返回切石机配方。
func StonecuttingRecipeAt(index int32) (StonecuttingRecipe, bool) {
	if index < 0 || int(index) >= len(StonecuttingRecipes) {
		return StonecuttingRecipe{}, false
	}
	return StonecuttingRecipes[index], true
}

// MatchSmithing 在模板/基底/添加物三个槽位中查找锻造升级配方
// （任一槽位为空时返回 false）。
func MatchSmithing(template, base, addition int32) (SmithingTransformRecipe, bool) {
	if template == 0 || base == 0 || addition == 0 {
		return SmithingTransformRecipe{}, false
	}
	for _, recipe := range SmithingTransformRecipes {
		if ingredientAccepts(recipe.Template, template) &&
			ingredientAccepts(recipe.Base, base) &&
			ingredientAccepts(recipe.Addition, addition) {
			return recipe, true
		}
	}
	return SmithingTransformRecipe{}, false
}

// MatchSmithingTrim 在模板/基底/添加物三个槽位中查找锻造纹饰配方
// （任一槽位为空时返回 false）。
func MatchSmithingTrim(template, base, addition int32) (SmithingTrimRecipe, bool) {
	if template == 0 || base == 0 || addition == 0 {
		return SmithingTrimRecipe{}, false
	}
	for _, recipe := range SmithingTrimRecipes {
		if ingredientAccepts(recipe.Template, template) &&
			ingredientAccepts(recipe.Base, base) &&
			ingredientAccepts(recipe.Addition, addition) {
			return recipe, true
		}
	}
	return SmithingTrimRecipe{}, false
}

// trimMaterialByItem 是物品 ID → 纹饰材质名的索引
// （minecraft:provides_trim_material 组件，见 TrimMaterialItems）。
var trimMaterialByItem = sync.OnceValue(func() map[int32]string {
	index := make(map[int32]string, len(TrimMaterialItems))
	for name, material := range TrimMaterialItems {
		id, err := ItemID(name)
		if err != nil {
			continue
		}
		index[id] = material
	}
	return index
})

// TrimMaterialForItem 返回物品的纹饰材质名（如 minecraft:gold）。
// 物品不携带 minecraft:provides_trim_material 组件时 ok 为 false
// （原版 TrimMaterials.createFromIngredient）。
func TrimMaterialForItem(itemID int32) (string, bool) {
	material, ok := trimMaterialByItem()[itemID]
	return material, ok
}

// TrimMaterialID 返回纹饰材质的同步注册表条目 ID（客户端按 Registry Data
// 的发送顺序编号；trim 组件的 Holder 编码在其上加 1）。
func TrimMaterialID(name string) (int32, bool) {
	return SyncEntryID("minecraft:trim_material", name)
}

// TrimPatternID 返回纹饰图案的同步注册表条目 ID（同上，Holder 编码加 1）。
func TrimPatternID(name string) (int32, bool) {
	return SyncEntryID("minecraft:trim_pattern", name)
}

// unionItems 归并若干物品 ID 集合为升序去重列表。
func unionItems(groups ...Ingredient) []int32 {
	seen := make(map[int32]bool)
	var ids []int32
	for _, group := range groups {
		for _, id := range group {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

var (
	smithingTemplateItems = sync.OnceValue(func() []int32 {
		groups := make([]Ingredient, 0, len(SmithingTransformRecipes)+len(SmithingTrimRecipes))
		for _, recipe := range SmithingTransformRecipes {
			groups = append(groups, recipe.Template)
		}
		for _, recipe := range SmithingTrimRecipes {
			groups = append(groups, recipe.Template)
		}
		return unionItems(groups...)
	})
	smithingBaseItems = sync.OnceValue(func() []int32 {
		groups := make([]Ingredient, 0, len(SmithingTransformRecipes)+len(SmithingTrimRecipes))
		for _, recipe := range SmithingTransformRecipes {
			groups = append(groups, recipe.Base)
		}
		for _, recipe := range SmithingTrimRecipes {
			groups = append(groups, recipe.Base)
		}
		return unionItems(groups...)
	})
	smithingAdditionItems = sync.OnceValue(func() []int32 {
		groups := make([]Ingredient, 0, len(SmithingTransformRecipes)+len(SmithingTrimRecipes))
		for _, recipe := range SmithingTransformRecipes {
			groups = append(groups, recipe.Addition)
		}
		for _, recipe := range SmithingTrimRecipes {
			groups = append(groups, recipe.Addition)
		}
		return unionItems(groups...)
	})
)

// SmithingTemplateItems 返回锻造台模板槽的合法物品集合
// （原版属性集 minecraft:smithing_template：全部锻造配方的模板并集）。
func SmithingTemplateItems() []int32 { return smithingTemplateItems() }

// SmithingBaseItems 返回锻造台基底槽的合法物品集合
// （原版属性集 minecraft:smithing_base）。
func SmithingBaseItems() []int32 { return smithingBaseItems() }

// SmithingAdditionItems 返回锻造台添加物槽的合法物品集合
// （原版属性集 minecraft:smithing_addition）。
func SmithingAdditionItems() []int32 { return smithingAdditionItems() }

// CookingInputItems 返回指定烹饪容器可接受的输入物品集合
// （原版属性集 minecraft:furnace_input / blast_furnace_input /
// smoker_input / campfire_input）。
func CookingInputItems(kind CookingKind) []int32 {
	groups := make([]Ingredient, 0, len(CookingRecipes))
	for _, recipe := range CookingRecipes {
		if recipe.Kind == kind {
			groups = append(groups, recipe.Ingredient)
		}
	}
	return unionItems(groups...)
}

var propertySets = sync.OnceValue(func() []PropertySet {
	return []PropertySet{
		{Name: "minecraft:smithing_base", Items: SmithingBaseItems()},
		{Name: "minecraft:smithing_template", Items: SmithingTemplateItems()},
		{Name: "minecraft:smithing_addition", Items: SmithingAdditionItems()},
		{Name: "minecraft:furnace_input", Items: CookingInputItems(CookingSmelting)},
		{Name: "minecraft:blast_furnace_input", Items: CookingInputItems(CookingBlasting)},
		{Name: "minecraft:smoker_input", Items: CookingInputItems(CookingSmoking)},
		{Name: "minecraft:campfire_input", Items: CookingInputItems(CookingCampfire)},
	}
})

// PropertySets 返回原版 Update Recipes 包中的全部配方属性集：
// smithing_base/template/addition 用于客户端锻造台槽位的放入校验
// （缺失会让客户端拒绝把物品放入槽位），*_input 用于配方书筛选。
// 返回的集合为升序去重物品 ID（集合语义，顺序不敏感）。
// 调用者不得修改返回的切片内容。
func PropertySets() []PropertySet { return propertySets() }
