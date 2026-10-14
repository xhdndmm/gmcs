//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"log/slog"

	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
)

// 配方书（1.21.2+ 配方系统）：
//
//   - 进入世界时发送 Update Recipes（0x83）：声明全部非切石机配方
//     （名称 + 产物物品）与切石机配方列表（输入集合 + 结果显示）；
//   - 随后发送 Recipe Book Add（0x48）：本实现视玩家已解锁全部配方，
//     因此一次下发全部显示条目（displayId 为服务端构建表中该配方的下标）；
//   - 处理客户端的 Place Recipe（0x26）：把配方的原料从背包放入当前打开
//     的合成格（窗口 0 的 2×2 或工作台 3×3），makeAll 时尽可能多次合成；
//   - 配方书开关设置与“查看配方”记录仅解析保存，不影响服务端逻辑。
//
// 显示数据（物品、分类、工作台图标）在服务器启动时构建一次，跨玩家共享。

// bookRecipeKind 标识配方书条目对应的配方来源表。
type bookRecipeKind uint8

const (
	bookShaped bookRecipeKind = iota
	bookShapeless
	bookSmelting
	bookBlasting
	bookSmoking
	bookCampfire
	bookSmithing
)

// bookRecipe 是配方书条目对生成表的下标引用。
type bookRecipe struct {
	kind  bookRecipeKind
	index int32
}

// recipeBook 是服务端持有的配方书数据（内容与玩家无关，只读共享）。
type recipeBook struct {
	// updateRecipes / addPacket 是预编码的共享数据包（只读，可跨玩家复用）。
	updateRecipes []byte
	addPacket     []byte
	// entries[displayId] = 配方引用（仅合成配方参与放置流程）。
	entries []bookRecipe
}

// buildRecipeBook 构建配方书共享数据；注册表数据缺失时保持禁用。
func (s *Server) buildRecipeBook() {
	itemID := func(name string) int32 {
		id, _ := registry.ItemID(name)
		return id
	}
	craftingTable := itemID("minecraft:crafting_table")
	furnaceItem := itemID("minecraft:furnace")
	blastFurnaceItem := itemID("minecraft:blast_furnace")
	smokerItem := itemID("minecraft:smoker")
	smithingTable := itemID("minecraft:smithing_table")

	var (
		list    []protocol.RecipeListEntry
		addList []protocol.RecipeBookAddItem
		entries []bookRecipe
	)
	appendAdd := func(display protocol.RecipeDisplay, category int32, ref bookRecipe) {
		addList = append(addList, protocol.RecipeBookAddItem{
			DisplayID: int32(len(entries)),
			Category:  category,
			Display:   display,
		})
		entries = append(entries, ref)
	}
	copyIngredients := func(ingredients []registry.Ingredient) [][]int32 {
		out := make([][]int32, len(ingredients))
		for i, ingredient := range ingredients {
			out[i] = append([]int32(nil), ingredient...)
		}
		return out
	}
	// shapedDisplayIngredients 把有序配方的紧凑原料表展开为 Width×Height 的
	// 行优先网格。客户端的 ShapedCraftingRecipeDisplay 构造函数强制要求
	// ingredients 数量等于 width*height（空格以 empty 显示占位），否则
	// Recipe Book Add 整个数据包在客户端解码失败并断开连接。
	shapedDisplayIngredients := func(recipe registry.ShapedRecipe) [][]int32 {
		out := make([][]int32, len(recipe.Cells)) // 生成数据保证 len(Cells) == Width*Height
		for cell, ref := range recipe.Cells {
			if ref >= 0 {
				out[cell] = append([]int32(nil), recipe.Ingredients[ref]...)
			}
		}
		return out
	}

	for i, recipe := range registry.ShapedRecipes {
		list = append(list, protocol.RecipeListEntry{Name: recipe.Name, Items: []int32{recipe.Result}})
		appendAdd(protocol.RecipeDisplay{
			Type:            protocol.RecipeDisplayCraftingShaped,
			Width:           recipe.Width,
			Height:          recipe.Height,
			Ingredients:     shapedDisplayIngredients(recipe),
			ResultItem:      recipe.Result,
			ResultCount:     recipe.ResultCount,
			CraftingStation: craftingTable,
		}, protocol.RecipeCategoryCraftingMisc, bookRecipe{kind: bookShaped, index: int32(i)})
	}
	for i, recipe := range registry.ShapelessRecipes {
		list = append(list, protocol.RecipeListEntry{Name: recipe.Name, Items: []int32{recipe.Result}})
		appendAdd(protocol.RecipeDisplay{
			Type:            protocol.RecipeDisplayCraftingShapeless,
			Ingredients:     copyIngredients(recipe.Ingredients),
			ResultItem:      recipe.Result,
			ResultCount:     recipe.ResultCount,
			CraftingStation: craftingTable,
		}, protocol.RecipeCategoryCraftingMisc, bookRecipe{kind: bookShapeless, index: int32(i)})
	}
	cookingKinds := map[registry.CookingKind]struct {
		kind      bookRecipeKind
		category  int32
		stationID int32
	}{
		registry.CookingSmelting: {bookSmelting, protocol.RecipeCategoryFurnaceMisc, furnaceItem},
		registry.CookingBlasting: {bookBlasting, protocol.RecipeCategoryBlastFurnaceMisc, blastFurnaceItem},
		registry.CookingSmoking:  {bookSmoking, protocol.RecipeCategorySmokerFood, smokerItem},
		registry.CookingCampfire: {bookCampfire, protocol.RecipeCategoryCampfire, 0},
	}
	for i, recipe := range registry.CookingRecipes {
		meta, ok := cookingKinds[recipe.Kind]
		if !ok {
			continue
		}
		list = append(list, protocol.RecipeListEntry{Name: recipe.Name, Items: []int32{recipe.Result}})
		appendAdd(protocol.RecipeDisplay{
			Type:            protocol.RecipeDisplayFurnace,
			Ingredients:     [][]int32{append([]int32(nil), recipe.Ingredient...)},
			ResultItem:      recipe.Result,
			ResultCount:     recipe.ResultCount,
			CraftingStation: meta.stationID,
			Duration:        recipe.CookTime,
			Experience:      float32(recipe.Experience),
		}, meta.category, bookRecipe{kind: meta.kind, index: int32(i)})
	}
	for i, recipe := range registry.SmithingTransformRecipes {
		list = append(list, protocol.RecipeListEntry{Name: recipe.Name, Items: []int32{recipe.Result}})
		appendAdd(protocol.RecipeDisplay{
			Type:            protocol.RecipeDisplaySmithing,
			Template:        append([]int32(nil), recipe.Template...),
			Base:            append([]int32(nil), recipe.Base...),
			Addition:        append([]int32(nil), recipe.Addition...),
			ResultItem:      recipe.Result,
			ResultCount:     recipe.ResultCount,
			CraftingStation: smithingTable,
		}, protocol.RecipeCategorySmithing, bookRecipe{kind: bookSmithing, index: int32(i)})
	}

	stonecutter := make([]protocol.StonecutterRecipeEntry, 0, len(registry.StonecuttingRecipes))
	for _, recipe := range registry.StonecuttingRecipes {
		stonecutter = append(stonecutter, protocol.StonecutterRecipeEntry{
			Input:       append([]int32(nil), recipe.Ingredient...),
			ResultItem:  recipe.Result,
			ResultCount: recipe.ResultCount,
		})
	}

	s.recipeBook = &recipeBook{
		updateRecipes: protocol.EncodeUpdateRecipes(list, stonecutter),
		addPacket:     protocol.EncodeRecipeBookAdd(addList, true),
		entries:       entries,
	}
	slog.Info("配方书已构建",
		"recipes", len(list), "stonecutter", len(stonecutter), "bookEntries", len(addList))
}

// handlePlaceRecipe 处理 Place Recipe（配方书点击）：把原料放入当前合成格。
// 仅支持合成配方（有序/无序）；烹饪/锻造条目不属于合成格放置流程。
// 由会话读循环调用。
func (s *Server) handlePlaceRecipe(player *session, windowID, displayID int32, makeAll bool) {
	book := s.recipeBook
	if book == nil {
		return
	}
	if displayID < 0 || int(displayID) >= len(book.entries) {
		return
	}
	var backing craftSlotBacking
	switch {
	case windowID == protocol.PlayerInventoryWindowID && player.getOpenCraft() == nil:
		backing = craftSlotBacking{player: player, state: player.invCraftState(), windowZero: true}
	case player.getOpenCraft() != nil && windowID == player.windowID:
		backing = craftSlotBacking{player: player, state: player.getOpenCraft()}
	default:
		return
	}
	ref := book.entries[displayID]
	gridWidth := int32(backing.state.width)
	gridCount := backing.gridCount()

	// 计算每个格子的候选物品集合（空 = 不放置）。
	targets := make([][]int32, gridCount)
	switch ref.kind {
	case bookShaped:
		recipe := registry.ShapedRecipes[ref.index]
		if recipe.Width > gridWidth || recipe.Height > gridWidth {
			return // 配方放不进当前合成格
		}
		for y := int32(0); y < recipe.Height; y++ {
			for x := int32(0); x < recipe.Width; x++ {
				cell := recipe.Cells[y*recipe.Width+x]
				if cell < 0 {
					continue
				}
				targets[y*gridWidth+x] = recipe.Ingredients[cell]
			}
		}
	case bookShapeless:
		recipe := registry.ShapelessRecipes[ref.index]
		if int32(len(recipe.Ingredients)) > gridWidth*gridWidth {
			return
		}
		for i, ingredient := range recipe.Ingredients {
			targets[i] = ingredient
		}
	default:
		return // 烹饪/锻造配方不参与放置
	}

	// 全部在窗口快照上操作：先记下当前内容与背包区间。
	working := make([]item.Stack, backing.SlotCount())
	for i := range working {
		working[i] = backing.Get(i)
	}
	ranges := backing.invRanges()
	invStart, invEnd := ranges[0][0], ranges[len(ranges)-1][1]

	// 现有合成格物品退回背包；放不下则中止（快照丢弃，不产生任何变更）。
	for slot := 1; slot <= gridCount; slot++ {
		stack := working[slot]
		if stack.IsEmpty() {
			continue
		}
		stack = mergeIntoArea(backing, working, stack, invStart, invEnd)
		if !stack.IsEmpty() {
			return
		}
		working[slot] = item.Empty()
	}

	// 放置一轮原料；缺少原料时保留已有部分（与原版幽灵配方一致）。
	placeOnce := func() (complete bool) {
		complete = true
		for slot, candidates := range targets {
			if len(candidates) == 0 || !working[1+slot].IsEmpty() {
				continue
			}
			if !placeFromWorking(working, invStart, invEnd, candidates, 1+slot) {
				complete = false
			}
		}
		return complete
	}
	placeOnce()

	// makeAll：循环取走产物（直接放入背包）并重新放置原料，直到原料不足或背包已满。
	if makeAll {
		for iter := 0; iter < 64; iter++ {
			result := backing.resultOf(working)
			if result.IsEmpty() {
				break
			}
			stack := result
			for _, target := range ranges {
				stack = mergeIntoArea(backing, working, stack, target[0], target[1])
				if stack.IsEmpty() {
					break
				}
			}
			if !stack.IsEmpty() {
				break // 背包已满
			}
			backing.consumeGrid(working)
			if !placeOnce() {
				break
			}
		}
	}

	// 写回：合成格状态与玩家物品栏槽位。
	backing.state.mu.Lock()
	for i := 0; i < gridCount; i++ {
		backing.state.grid[i] = working[1+i]
	}
	backing.state.mu.Unlock()
	for i := 1 + gridCount; i < len(working); i++ {
		backing.Set(i, working[i])
	}

	// 重新发送完整窗口内容（合成格、结果槽与玩家物品栏）。
	if backing.windowZero {
		_ = player.writePacket(s.inventoryContentPacketFor(player, backing))
	} else {
		s.sendCraftContent(player, backing)
	}
}

// placeFromWorking 从窗口快照的背包槽位区间取出 1 个符合候选集合的物品，
// 放入 gridSlot；成功返回 true。
func placeFromWorking(working []item.Stack, start, end int, candidates []int32, gridSlot int) bool {
	for slot := start; slot < end; slot++ {
		stack := working[slot]
		if stack.IsEmpty() {
			continue
		}
		matched := false
		for _, id := range candidates {
			if stack.ItemID == id {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		taken := stack
		taken.Count = 1
		stack.Count--
		if stack.Count <= 0 {
			stack = item.Empty()
		}
		working[slot] = stack
		working[gridSlot] = taken
		return true
	}
	return false
}

// inventoryContentPacketFor 与 inventoryContentPacket 相同，但使用给定的
// 合成格 backing（配方书放置后刷新窗口 0 的内容）。
func (s *Server) inventoryContentPacketFor(player *session, backing craftSlotBacking) []byte {
	slots := make([][]byte, 0, backing.SlotCount())
	for i := 0; i < backing.SlotCount(); i++ {
		slots = append(slots, backing.Get(i).AppendSlot(nil))
	}
	return protocol.EncodeContainerSetContent(
		protocol.PlayerInventoryWindowID, player.windowState, slots, player.getCursor().AppendSlot(nil))
}
