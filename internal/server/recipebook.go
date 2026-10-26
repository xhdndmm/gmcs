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
//   - 进入世界时发送 Update Recipes（0x83）：下发原版配方属性集
//     （smithing_base/template/addition 与四类 *_input 的物品集合）与切石机配方
//     （输入集合 + 结果显示；客户端的锻造台槽位校验依赖属性集，
//     缺失会让客户端拒绝把物品放入槽位）；
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

	propertySets := make([]protocol.PropertySetEntry, 0, 7)
	for _, set := range registry.PropertySets() {
		propertySets = append(propertySets, protocol.PropertySetEntry{Name: set.Name, Items: set.Items})
	}

	s.recipeBook = &recipeBook{
		updateRecipes: protocol.EncodeUpdateRecipes(propertySets, stonecutter),
		addPacket:     protocol.EncodeRecipeBookAdd(addList, true),
		entries:       entries,
	}
	slog.Info("配方书已构建",
		"propertySets", len(propertySets), "stonecutter", len(stonecutter), "bookEntries", len(addList))
}

// handlePlaceRecipe 处理 Place Recipe（配方书点击）。
//
// 对齐原版 1.21.2+ 语义（1.21.11 ServerPlaceRecipe / AbstractCraftingMenu 字节码）：
//   - 现有合成格物品必须先能全部退回背包（原版 testClearGrid），否则不执行任何操作；
//   - 原料不足（无法合成一次）时清空合成格并发送 Place Ghost Recipe，
//     客户端会把配方以“幽灵”形式显示在合成格中；
//   - 原料足够时清空合成格，再把原料放进合成格：
//     useMaxItems（Shift）→ 每格放入尽可能多的原料（最多可合成次数）；
//     否则若合成格原本已摆成该配方 → 每格再补 1 个；否则每格放 1 个。
//
// 与原版一致，本函数不自动合成：原料留在合成格，玩家自行取走产物
// （旧实现“Shift 立即全部合成并把产物塞进背包”是 1.21.2 之前的旧行为）。
// 仅支持合成配方（有序/无序）；烹饪/锻造/切石条目不属于合成格放置流程。
// 由会话读循环调用。
func (s *Server) handlePlaceRecipe(player *session, windowID, displayID int32, useMaxItems bool) {
	book := s.recipeBook
	if book == nil || displayID < 0 || int(displayID) >= len(book.entries) {
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
	gridWidth := int(backing.state.width)
	gridCount := backing.gridCount()

	// 配方原料：targets[合成格下标] = 候选物品集合（空 = 该格留空）。
	targets := make([][]int32, gridCount)
	switch ref.kind {
	case bookShaped:
		recipe := registry.ShapedRecipes[ref.index]
		if int(recipe.Width) > gridWidth || int(recipe.Height) > gridWidth {
			return // 配方放不进当前合成格
		}
		for y := 0; y < int(recipe.Height); y++ {
			for x := 0; x < int(recipe.Width); x++ {
				cell := recipe.Cells[y*int(recipe.Width)+x]
				if cell < 0 {
					continue
				}
				targets[y*gridWidth+x] = recipe.Ingredients[cell]
			}
		}
	case bookShapeless:
		recipe := registry.ShapelessRecipes[ref.index]
		if len(recipe.Ingredients) > gridCount {
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

	// 合成格当前是否已摆成该配方，以及占据格的最小数量
	// （原版 CraftingMenuAccess.matches 与 calculateAmountToCraft）。
	gridMatches := true
	gridMinCount := 0
	for slot := 0; slot < gridCount; slot++ {
		stack := working[1+slot]
		candidates := targets[slot]
		if len(candidates) == 0 {
			if !stack.IsEmpty() {
				gridMatches = false
			}
			continue
		}
		if stack.IsEmpty() || !itemIDIn(stack.ItemID, candidates) {
			gridMatches = false
			continue
		}
		if gridMinCount == 0 || int(stack.Count) < gridMinCount {
			gridMinCount = int(stack.Count)
		}
	}
	if !gridMatches {
		gridMinCount = 0
	}

	// 最多可合成次数（原版 StackedItemContents.getMaxCraftable 的等价贪心：
	// 每次合成按格子顺序取一种可用原料，取不满即停止）。
	maxCrafts := recipeMaxCrafts(working, invStart, invEnd, gridCount, targets)
	if maxCrafts <= 0 {
		// 原料不足：清空合成格并显示幽灵配方（原版 clearGrid + Place Ghost Recipe）。
		if !clearCraftGrid(working, gridCount, invStart, invEnd) {
			return // 合成格物品退不回背包：保持原样（原版 testClearGrid 失败）
		}
		s.writeBackCraft(player, backing, working, gridCount)
		_ = player.writePacket(protocol.EncodePlaceGhostRecipe(windowID, displayID))
		return
	}

	// 现有合成格物品退回背包；放不下则中止（快照丢弃，不产生任何变更）。
	if !clearCraftGrid(working, gridCount, invStart, invEnd) {
		return
	}

	// 每格放置数量（原版 calculateAmountToCraft）。
	amount := 1
	if useMaxItems {
		amount = maxCrafts
	} else if gridMinCount > 0 {
		amount = gridMinCount + 1
	}
	for slot, candidates := range targets {
		if len(candidates) == 0 {
			continue
		}
		placeUpTo(working, invStart, invEnd, candidates, 1+slot, amount)
	}

	s.writeBackCraft(player, backing, working, gridCount)
}

// itemIDIn 报告物品 ID 是否在候选集合中。
func itemIDIn(id int32, candidates []int32) bool {
	for _, candidate := range candidates {
		if candidate == id {
			return true
		}
	}
	return false
}

// clearCraftGrid 把合成格内容退回背包（区间 [invStart, invEnd)）。
// 任一格放不下时返回 false，调用方须放弃本次操作。
func clearCraftGrid(working []item.Stack, gridCount, invStart, invEnd int) bool {
	for slot := 1; slot <= gridCount; slot++ {
		stack := working[slot]
		if stack.IsEmpty() {
			continue
		}
		// 合成格物品退回背包时不受槽位限制（原版 placeItemBackInInventory）。
		remaining := mergeIntoArea(craftSlotBacking{}, working, stack, invStart, invEnd)
		if !remaining.IsEmpty() {
			return false
		}
		working[slot] = item.Empty()
	}
	return true
}

// recipeMaxCrafts 返回用背包（[invStart, invEnd)）与合成格现有物品最多能
// 完成多少次该配方。targets 为空（无原料格）时返回 0。
func recipeMaxCrafts(working []item.Stack, invStart, invEnd, gridCount int, targets [][]int32) int {
	counts := make(map[int32]int, 16)
	add := func(stack item.Stack) {
		if !stack.IsEmpty() {
			counts[stack.ItemID] += int(stack.Count)
		}
	}
	for _, stack := range working[invStart:invEnd] {
		add(stack)
	}
	for slot := 1; slot <= gridCount; slot++ {
		add(working[slot])
	}
	crafts := 0
	for {
		cells := 0
		for _, candidates := range targets {
			if len(candidates) == 0 {
				continue
			}
			picked := false
			for _, id := range candidates {
				if counts[id] > 0 {
					counts[id]--
					picked = true
					break
				}
			}
			if !picked {
				return crafts
			}
			cells++
		}
		if cells == 0 {
			return 0
		}
		crafts++
		if crafts > 4096 {
			return crafts
		}
	}
}

// placeUpTo 从背包区间取出至多 max 个符合候选集合的物品放入 gridSlot
// （原版 ServerPlaceRecipe.moveItemToGrid：按来源槽位逐个搬运）。
func placeUpTo(working []item.Stack, start, end int, candidates []int32, gridSlot, max int) {
	placed := 0
	for slot := start; slot < end && placed < max; slot++ {
		stack := working[slot]
		if stack.IsEmpty() || !itemIDIn(stack.ItemID, candidates) {
			continue
		}
		take := min(int(stack.Count), max-placed)
		current := working[gridSlot]
		if current.IsEmpty() {
			current = stack
			current.Count = int32(take)
		} else {
			current.Count += int32(take)
		}
		working[gridSlot] = current
		stack.Count -= int32(take)
		if stack.Count <= 0 {
			stack = item.Empty()
		}
		working[slot] = stack
		placed += take
	}
}

// writeBackCraft 把窗口快照写回合成格状态与玩家物品栏，并重新发送窗口内容。
func (s *Server) writeBackCraft(player *session, backing craftSlotBacking, working []item.Stack, gridCount int) {
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
