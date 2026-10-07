package registry

// 配方匹配：在给定合成格（物品 ID 网格）中查找配方，以及按容器类型查询烹饪配方。
//
// 合成格表示：grid 为 width×height 行优先的物品 ID 数组，0 表示空格。
// 有序配方支持水平镜像匹配（与原版 ShapedRecipe 一致）；小图案可在大网格中
// 任意偏移放置，网格中图案外的格子必须为空。

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
