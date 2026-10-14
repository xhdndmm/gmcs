//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package protocol

import (
	"fmt"
)

// 1.21.11（协议 774）配方相关数据包。
//
// 1.21.2 起配方系统重做：服务端先通过 Update Recipes（0x83）声明全部配方
// （名称 + 产物物品；切石机配方单独列出），再按玩家通过 Recipe Book Add
// （0x48）下发已解锁的配方显示条目；客户端点击配方时发送 Place Recipe
// （0x26）请求服务端把原料放入合成格（可选“尽可能多次合成”）。
const (
	// PlayPacketIDUpdateRecipes 是 Update Recipes（配方声明，0x83）。
	PlayPacketIDUpdateRecipes = 0x83
	// PlayPacketIDRecipeBookAdd 是 Recipe Book Add（0x48）。
	PlayPacketIDRecipeBookAdd = 0x48
	// PlayPacketIDRecipeBookRemove 是 Recipe Book Remove（0x49）。
	PlayPacketIDRecipeBookRemove = 0x49
	// PlayPacketIDRecipeBookSettings 是 Recipe Book Settings（0x4A）。
	PlayPacketIDRecipeBookSettings = 0x4A

	// PlayServerboundPacketIDContainerButtonClick 是 Container Button Click（0x10）。
	PlayServerboundPacketIDContainerButtonClick = 0x10
	// PlayServerboundPacketIDPlaceRecipe 是 Place Recipe（0x26，配方书点击）。
	PlayServerboundPacketIDPlaceRecipe = 0x26
	// PlayServerboundPacketIDRecipeBookChangeSettings 是配方书开关/过滤设置（0x2D）。
	PlayServerboundPacketIDRecipeBookChangeSettings = 0x2D
	// PlayServerboundPacketIDRecipeBookSeenRecipe 是配方书查看记录（0x2E）。
	PlayServerboundPacketIDRecipeBookSeenRecipe = 0x2E
)

// SlotDisplay 的类型 ID（Update Recipes / Recipe Book Add 的显示条目）。
// 见 1.21.11 协议 SlotDisplay 枚举。
const (
	SlotDisplayEmpty         = 0
	SlotDisplayAnyFuel       = 1
	SlotDisplayItem          = 2
	SlotDisplayItemStack     = 3
	SlotDisplayTag           = 4
	SlotDisplaySmithingTrim  = 5
	SlotDisplayWithRemainder = 6
	SlotDisplayComposite     = 7
)

// RecipeDisplay 的类型 ID（1.21.11 协议 RecipeDisplay 枚举）。
const (
	RecipeDisplayCraftingShapeless = 0
	RecipeDisplayCraftingShaped    = 1
	RecipeDisplayFurnace           = 2
	RecipeDisplayStonecutter       = 3
	RecipeDisplaySmithing          = 4
)

// RecipeBookCategory 是配方书分类（1.21.11 协议枚举）。
const (
	RecipeCategoryCraftingBuildingBlocks = 0
	RecipeCategoryCraftingRedstone       = 1
	RecipeCategoryCraftingEquipment      = 2
	RecipeCategoryCraftingMisc           = 3
	RecipeCategoryFurnaceFood            = 4
	RecipeCategoryFurnaceBlocks          = 5
	RecipeCategoryFurnaceMisc            = 6
	RecipeCategoryBlastFurnaceBlocks     = 7
	RecipeCategoryBlastFurnaceMisc       = 8
	RecipeCategorySmokerFood             = 9
	RecipeCategoryStonecutter            = 10
	RecipeCategorySmithing               = 11
	RecipeCategoryCampfire               = 12
)

// appendSlotDisplayItem 追加一个物品（item）SlotDisplay。
func appendSlotDisplayItem(dst []byte, itemID int32) []byte {
	dst = AppendVarInt(dst, SlotDisplayItem)
	return AppendVarInt(dst, itemID)
}

// appendSlotDisplayItemStack 追加一个物品堆栈（item_stack）SlotDisplay。
// 堆栈按 Slot 结构编码（count、itemId、组件数 0、移除组件数 0）。
func appendSlotDisplayItemStack(dst []byte, itemID, count int32) []byte {
	dst = AppendVarInt(dst, SlotDisplayItemStack)
	dst = AppendVarInt(dst, count)
	dst = AppendVarInt(dst, itemID)
	dst = AppendVarInt(dst, 0)
	return AppendVarInt(dst, 0)
}

// appendSlotDisplayIngredient 追加一个原料 SlotDisplay：
// 单个物品用 item 显示，多个物品用 composite（逐项 item 显示）。
// 空集合按 empty 编码（不会出现在合法配方中，防御性处理）。
func appendSlotDisplayIngredient(dst []byte, items []int32) []byte {
	switch len(items) {
	case 0:
		return AppendVarInt(dst, SlotDisplayEmpty)
	case 1:
		return appendSlotDisplayItem(dst, items[0])
	default:
		dst = AppendVarInt(dst, SlotDisplayComposite)
		dst = AppendVarInt(dst, int32(len(items)))
		for _, id := range items {
			dst = appendSlotDisplayItem(dst, id)
		}
		return dst
	}
}

// RecipeListEntry 是 Update Recipes 中的一条配方（名称 + 产物物品列表）。
type RecipeListEntry struct {
	Name  string
	Items []int32
}

// StonecutterRecipeEntry 是 Update Recipes 的切石机配方项：
// 输入为显式物品集合（HolderSet），输出为物品堆栈显示。
type StonecutterRecipeEntry struct {
	Input       []int32
	ResultItem  int32
	ResultCount int32
}

// EncodeUpdateRecipes 编码 Update Recipes（0x83）。
//
// recipes 为全部非切石机配方（合成/烹饪/锻造），仅用于客户端配方索引；
// stonecutter 为切石机配方（输入集合 + 结果显示），客户端按输入物品过滤后
// 的顺序与数组一致（Container Button Click 的按钮编号即过滤后下标）。
func EncodeUpdateRecipes(recipes []RecipeListEntry, stonecutter []StonecutterRecipeEntry) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDUpdateRecipes))
	packet = AppendVarInt(packet, int32(len(recipes)))
	for _, recipe := range recipes {
		packet = appendString(packet, recipe.Name)
		packet = AppendVarInt(packet, int32(len(recipe.Items)))
		for _, item := range recipe.Items {
			packet = AppendVarInt(packet, item)
		}
	}
	packet = AppendVarInt(packet, int32(len(stonecutter)))
	for _, entry := range stonecutter {
		// ID Set（HolderSet）：显式列表为 VarInt(数量+1) + 每项注册表 ID。
		// 0 保留给标签（Tag）形式，物品 ID 本身不带偏移。
		packet = AppendVarInt(packet, int32(len(entry.Input))+1)
		for _, id := range entry.Input {
			packet = AppendVarInt(packet, id)
		}
		packet = appendSlotDisplayItemStack(packet, entry.ResultItem, entry.ResultCount)
	}
	return packet
}

// RecipeBookAddItem 是 Recipe Book Add 的一条条目。
type RecipeBookAddItem struct {
	// DisplayID 是显示编号（客户端在 Place Recipe 中回传该编号）。
	DisplayID int32
	// Category 是配方书分类（RecipeCategory* 常量）。
	Category int32
	// Flags：bit0 = notification，bit1 = highlight（本实现统一为 0）。
	Flags uint8
	// Display 是配方显示内容。
	Display RecipeDisplay
}

// RecipeDisplay 是配方显示内容（仅覆盖本服务器发送的类型）。
type RecipeDisplay struct {
	// Type 是 RecipeDisplay* 常量。
	Type int32
	// Width/Height 仅 crafting_shaped 使用。
	Width  int32
	Height int32
	// Ingredients 是原料物品集合列表（有序；shapeless/烹饪/切石机为 1 项）。
	Ingredients [][]int32
	// ResultItem/ResultCount 是产物。
	ResultItem  int32
	ResultCount int32
	// CraftingStation 为 0 时不显示工作台图标，否则为工作台物品 ID。
	CraftingStation int32
	// Duration/Experience 仅 furnace 使用。
	Duration   int32
	Experience float32
	// Template/Base/Addition 仅 smithing 使用（Addition 为空表示无添加物槽）。
	Template []int32
	Base     []int32
	Addition []int32
}

// appendRecipeDisplay 追加 RecipeDisplay 类型的编码。
func appendRecipeDisplay(dst []byte, display RecipeDisplay) []byte {
	dst = AppendVarInt(dst, display.Type)
	switch display.Type {
	case RecipeDisplayCraftingShapeless:
		dst = AppendVarInt(dst, int32(len(display.Ingredients)))
		for _, ingredient := range display.Ingredients {
			dst = appendSlotDisplayIngredient(dst, ingredient)
		}
		dst = appendSlotDisplayItemStack(dst, display.ResultItem, display.ResultCount)
		dst = appendSlotDisplayIngredient(dst, stationItems(display.CraftingStation))
	case RecipeDisplayCraftingShaped:
		dst = AppendVarInt(dst, display.Width)
		dst = AppendVarInt(dst, display.Height)
		dst = AppendVarInt(dst, int32(len(display.Ingredients)))
		for _, ingredient := range display.Ingredients {
			dst = appendSlotDisplayIngredient(dst, ingredient)
		}
		dst = appendSlotDisplayItemStack(dst, display.ResultItem, display.ResultCount)
		dst = appendSlotDisplayIngredient(dst, stationItems(display.CraftingStation))
	case RecipeDisplayFurnace:
		dst = appendSlotDisplayIngredient(dst, display.Ingredients[0])
		dst = AppendVarInt(dst, SlotDisplayAnyFuel)
		dst = appendSlotDisplayItemStack(dst, display.ResultItem, display.ResultCount)
		dst = appendSlotDisplayIngredient(dst, stationItems(display.CraftingStation))
		dst = AppendVarInt(dst, display.Duration)
		dst = AppendFloat32(dst, display.Experience)
	case RecipeDisplayStonecutter:
		dst = appendSlotDisplayIngredient(dst, display.Ingredients[0])
		dst = appendSlotDisplayItemStack(dst, display.ResultItem, display.ResultCount)
		dst = appendSlotDisplayIngredient(dst, stationItems(display.CraftingStation))
	case RecipeDisplaySmithing:
		if len(display.Template) == 1 {
			dst = appendSlotDisplayItem(dst, display.Template[0])
		} else {
			dst = appendSlotDisplayIngredient(dst, display.Template)
		}
		dst = appendSlotDisplayIngredient(dst, display.Base)
		if len(display.Addition) == 0 {
			dst = AppendVarInt(dst, SlotDisplayEmpty)
		} else {
			dst = appendSlotDisplayIngredient(dst, display.Addition)
		}
		dst = appendSlotDisplayItemStack(dst, display.ResultItem, display.ResultCount)
		dst = appendSlotDisplayIngredient(dst, stationItems(display.CraftingStation))
	}
	return dst
}

// stationItems 把工作台物品 ID 转为 SlotDisplay 的原料列表形式（0 = 空）。
func stationItems(itemID int32) []int32 {
	if itemID == 0 {
		return nil
	}
	return []int32{itemID}
}

// EncodeRecipeBookAdd 编码 Recipe Book Add（0x48）。
func EncodeRecipeBookAdd(entries []RecipeBookAddItem, replace bool) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDRecipeBookAdd))
	packet = AppendVarInt(packet, int32(len(entries)))
	for _, entry := range entries {
		packet = AppendVarInt(packet, entry.DisplayID)
		packet = appendRecipeDisplay(packet, entry.Display)
		packet = AppendVarInt(packet, 0) // group：无分组
		packet = AppendVarInt(packet, entry.Category)
		packet = AppendBool(packet, false) // craftingRequirements：不发送
		packet = append(packet, entry.Flags)
	}
	return AppendBool(packet, replace)
}

// RecipeBookSetting 是一类配方书的显示设置（打开 / 过滤）。
type RecipeBookSetting struct {
	Open      bool
	Filtering bool
}

// EncodeRecipeBookSettings 编码 Recipe Book Settings（0x4A）。
func EncodeRecipeBookSettings(crafting, furnace, blast, smoker RecipeBookSetting) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDRecipeBookSettings))
	appendSetting := func(dst []byte, setting RecipeBookSetting) []byte {
		dst = AppendBool(dst, setting.Open)
		return AppendBool(dst, setting.Filtering)
	}
	packet = appendSetting(packet, crafting)
	packet = appendSetting(packet, furnace)
	packet = appendSetting(packet, blast)
	packet = appendSetting(packet, smoker)
	return packet
}

// EncodeRecipeBookRemove 编码 Recipe Book Remove（0x49）。
func EncodeRecipeBookRemove(displayIDs []int32) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDRecipeBookRemove))
	packet = AppendVarInt(packet, int32(len(displayIDs)))
	for _, id := range displayIDs {
		packet = AppendVarInt(packet, id)
	}
	return packet
}

// ParseContainerButtonClick 解析 Container Button Click（0x10）：
// 窗口 ID + 按钮编号（切石机为选中配方下标）。
func ParseContainerButtonClick(packet []byte) (windowID int32, buttonID int32, err error) {
	_, offset, err := DecodeVarInt(packet)
	if err != nil {
		return 0, 0, err
	}
	windowID, size, err := DecodeVarInt(packet[offset:])
	if err != nil {
		return 0, 0, err
	}
	offset += size
	buttonID, _, err = DecodeVarInt(packet[offset:])
	if err != nil {
		return 0, 0, err
	}
	return windowID, buttonID, nil
}

// ParsePlaceRecipe 解析 Place Recipe（0x26）：窗口 ID、配方显示编号、是否尽可能多次。
func ParsePlaceRecipe(packet []byte) (windowID int32, displayID int32, makeAll bool, err error) {
	_, offset, err := DecodeVarInt(packet)
	if err != nil {
		return 0, 0, false, err
	}
	windowID, size, err := DecodeVarInt(packet[offset:])
	if err != nil {
		return 0, 0, false, err
	}
	offset += size
	displayID, size, err = DecodeVarInt(packet[offset:])
	if err != nil {
		return 0, 0, false, err
	}
	offset += size
	if offset >= len(packet) {
		return 0, 0, false, fmt.Errorf("配方请求缺少 makeAll 字段")
	}
	value := packet[offset]
	if value != 0 && value != 1 {
		return 0, 0, false, fmt.Errorf("makeAll 字段非法：%d", value)
	}
	return windowID, displayID, value == 1, nil
}

// ParseRecipeBookChangeSettings 解析配方书开关/过滤设置（0x2D）。
func ParseRecipeBookChangeSettings(packet []byte) (bookID int32, open, filtering bool, err error) {
	_, offset, err := DecodeVarInt(packet)
	if err != nil {
		return 0, false, false, err
	}
	bookID, size, err := DecodeVarInt(packet[offset:])
	if err != nil {
		return 0, false, false, err
	}
	offset += size
	if offset+2 > len(packet) {
		return 0, false, false, fmt.Errorf("配方书设置字段缺失")
	}
	return bookID, packet[offset] != 0, packet[offset+1] != 0, nil
}

// ParseRecipeBookSeenRecipe 解析配方书查看记录（0x2E）：配方显示编号。
func ParseRecipeBookSeenRecipe(packet []byte) (displayID int32, err error) {
	_, offset, err := DecodeVarInt(packet)
	if err != nil {
		return 0, err
	}
	displayID, _, err = DecodeVarInt(packet[offset:])
	return displayID, err
}
