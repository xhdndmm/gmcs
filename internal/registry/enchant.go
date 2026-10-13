package registry

// 附魔系统的运行时查询辅助；数据来自 enchants_generated.go（由 genregistries 生成）。
//
// 附魔注册表 ID 等于数据包文件路径的字典序下标（与客户端内置注册表一致）。

// sortedContains 在升序 int32 列表中二分查找。
func sortedContains(list []int32, value int32) bool {
	low, high := 0, len(list)-1
	for low <= high {
		mid := int(uint(low+high) >> 1)
		if list[mid] == value {
			return true
		}
		if list[mid] < value {
			low = mid + 1
		} else {
			high = mid - 1
		}
	}
	return false
}

// EnchantmentName 返回附魔的注册名。
func EnchantmentName(id int32) (string, bool) {
	if id < 0 || int(id) >= len(EnchantmentNames) {
		return "", false
	}
	return EnchantmentNames[id], true
}

// EnchantmentSupported 报告附魔是否可作用于物品（supported_items 已展开为物品 ID）。
func EnchantmentSupported(enchantID, itemID int32) bool {
	if enchantID < 0 || int(enchantID) >= len(EnchantmentSupportedItems) {
		return false
	}
	return sortedContains(EnchantmentSupportedItems[enchantID], itemID)
}

// EnchantmentTableApplicable 报告附魔台是否可向物品提供该附魔
// （原版 Enchantment 字节码：supported_items 含物品，且 primary_items
// 存在时必须同时命中 primary_items；普通书由调用方另行放行）。
// 铁砧/命令等路径只用 EnchantmentSupported（不带 primary 限制）。
func EnchantmentTableApplicable(enchantID, itemID int32) bool {
	if !EnchantmentSupported(enchantID, itemID) {
		return false
	}
	if enchantID < 0 || int(enchantID) >= len(EnchantmentPrimaryItems) {
		return true
	}
	primary := EnchantmentPrimaryItems[enchantID]
	if len(primary) == 0 {
		return true
	}
	return sortedContains(primary, itemID)
}

// EnchantmentCompatible 报告两个附魔是否可以共存（原版 Enchantment.areCompatible：
// 相同附魔不兼容；任一方的 exclusive_set 包含对方则不兼容）。
func EnchantmentCompatible(a, b int32) bool {
	if a == b {
		return false
	}
	if a >= 0 && int(a) < len(EnchantmentExclusive) && sortedContains(EnchantmentExclusive[a], b) {
		return false
	}
	if b >= 0 && int(b) < len(EnchantmentExclusive) && sortedContains(EnchantmentExclusive[b], a) {
		return false
	}
	return true
}

// InEnchantingTable 报告附魔是否出现在附魔台的可选集合（in_enchanting_table
// 标签；列表保持标签定义顺序，不适用于二分查找，规模很小直接线性扫描）。
func InEnchantingTable(enchantID int32) bool {
	for _, id := range EnchantmentsInEnchantingTable {
		if id == enchantID {
			return true
		}
	}
	return false
}

// EnchantmentMaxLevel 返回附魔的最大等级。
func EnchantmentMaxLevel(id int32) (int32, bool) {
	if id < 0 || int(id) >= len(EnchantmentMaxLevels) {
		return 0, false
	}
	return EnchantmentMaxLevels[id], true
}

// EnchantmentWeight 返回附魔的随机权重。
func EnchantmentWeight(id int32) (int32, bool) {
	if id < 0 || int(id) >= len(EnchantmentWeights) {
		return 0, false
	}
	return EnchantmentWeights[id], true
}

// EnchantmentAnvilCost 返回附魔的铁砧惩罚系数。
func EnchantmentAnvilCost(id int32) (int32, bool) {
	if id < 0 || int(id) >= len(EnchantmentAnvilCosts) {
		return 0, false
	}
	return EnchantmentAnvilCosts[id], true
}

// EnchantmentCostRange 返回附魔在指定等级（1 起）的成本区间
// （min_cost / max_cost 的 linear 展开）。
func EnchantmentCostRange(id, level int32) (minCost, maxCost int32, ok bool) {
	if id < 0 || int(id) >= len(EnchantmentMinCosts) {
		return 0, 0, false
	}
	if level < 1 || int(level) > len(EnchantmentMinCosts[id]) {
		return 0, 0, false
	}
	return EnchantmentMinCosts[id][level-1], EnchantmentMaxCosts[id][level-1], true
}

// EnchantableValue 返回物品的附魔能力值（minecraft:enchantable 组件）；
// 未列出的物品不可在附魔台附魔。
func EnchantableValue(itemID int32) (int32, bool) {
	name, ok := ItemName(itemID)
	if !ok {
		return 0, false
	}
	value, ok := EnchantableValues[name]
	return value, ok
}

// EnchantmentPowerProvider 报告方块是否提供附魔能量（书架）。
func EnchantmentPowerProvider(blockID int32) bool {
	return sortedContains(EnchantmentPowerProviders, blockID)
}

// EnchantmentPowerTransmitter 报告方块是否传播附魔能量（#replaceable 等）。
func EnchantmentPowerTransmitter(blockID int32) bool {
	return sortedContains(EnchantmentPowerTransmitters, blockID)
}
