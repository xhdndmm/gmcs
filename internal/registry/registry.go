//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

// Package registry 提供 1.21.11 注册表数据与标签。
//
// 数据由 tools/genregistries 从官方客户端的内置数据包与官方服务端
// 数据生成器报告（reports/registries.json）生成，见 data_generated.go。
// 发送 Registry Data 时省略条目 NBT，由 Known Packs 协商（minecraft:core）
// 让客户端从本地数据包读取定义；标签无法通过 Known Packs 获取，
// 必须由服务器通过 Update Tags 完整发送（见 AllTags）。
package registry

import (
	"sort"
	"sync"
)

// Registry 是一个同步注册表：条目顺序即客户端分配的数字 ID 顺序。
type Registry struct {
	Name    string
	Entries []string
}

// Tag 是一个注册表标签，Entries 为注册表条目 ID（已展开嵌套引用）。
type Tag struct {
	Name    string
	Entries []int32
}

// RegistryTags 是一个注册表的全部标签。
type RegistryTags struct {
	Name string
	Tags []Tag
}

var synchronized = sync.OnceValue(func() []Registry {
	names := make([]string, 0, len(syncEntries))
	for name := range syncEntries {
		names = append(names, name)
	}
	sort.Strings(names)
	registries := make([]Registry, 0, len(names))
	for _, name := range names {
		registries = append(registries, Registry{Name: name, Entries: syncEntries[name]})
	}
	return registries
})

// Synchronized 返回全部同步注册表（按注册表名排序）。
// 调用者不得修改返回的切片内容。
func Synchronized() []Registry {
	return synchronized()
}

var itemNamesByID = sync.OnceValue(func() map[int32]string {
	names := make(map[int32]string, len(ItemIDs))
	for name, id := range ItemIDs {
		names[id] = name
	}
	return names
})

// ItemName 返回物品注册表 ID 对应的命名空间 ID（ItemID 的反向映射）。
// 用于需要与 Minecraft 版本无关标识的场景（如玩家数据持久化）。
func ItemName(id int32) (string, bool) {
	name, ok := itemNamesByID()[id]
	return name, ok
}

// Food 返回物品的食物数值（营养与饱和度）；不是食物时返回 false。
func Food(itemID int32) (FoodValue, bool) {
	name, ok := ItemName(itemID)
	if !ok {
		return FoodValue{}, false
	}
	value, ok := FoodValues[name]
	return value, ok
}

// PlayerEquippableSlot 返回物品在玩家装备槽中的部位名（head/chest/legs/feet/offhand）；
// 物品不可由玩家装备时返回 ""。对应原版 LivingEntity.getEquipmentSlotForItem
// （minecraft:equippable 组件的 slot，且玩家 canUseSlot 支持该槽位）。
// 用于玩家物品栏窗口的盔甲槽 Slot.mayPlace、Shift 点击自动装备与副手槽判定。
func PlayerEquippableSlot(itemID int32) string {
	name, ok := ItemName(itemID)
	if !ok {
		return ""
	}
	return PlayerEquippableSlots[name]
}

// ItemTool 返回工具物品的挖掘数据（原版 ItemStack 的 minecraft:tool 组件）；
// 非工具物品返回 false（此时挖掘速度按 1.0 处理、且不算“正确工具”）。
func ItemTool(itemID int32) (ToolComponent, bool) {
	name, ok := ItemName(itemID)
	if !ok {
		return ToolComponent{}, false
	}
	component, ok := ToolComponents[name]
	return component, ok
}

// MiningSpeed 返回该工具对方块状态的挖掘速度倍率（原版 Tool.getMiningSpeed：
// 取首个匹配规则，未匹配时用 default_mining_speed）。
func (t ToolComponent) MiningSpeed(state uint16) float32 {
	for _, rule := range t.Rules {
		if rule.Matches(state) {
			return rule.Speed
		}
	}
	return t.DefaultSpeed
}

// CorrectForDrops 报告该工具对指定方块状态是否算“正确工具”
// （原版 Tool.isCorrectForDrops：取首个匹配规则，未匹配为 false）。
func (t ToolComponent) CorrectForDrops(state uint16) bool {
	for _, rule := range t.Rules {
		if rule.Matches(state) {
			return rule.Correct
		}
	}
	return false
}

// Matches 报告工具规则是否匹配方块状态（标签或方块名列表）。
func (r ToolRule) Matches(state uint16) bool {
	if r.BlocksTag != "" {
		return BlockTagContains(r.BlocksTag, state)
	}
	if len(r.Blocks) == 0 {
		return false
	}
	name, ok := BlockNameByState(state)
	if !ok {
		return false
	}
	for _, candidate := range r.Blocks {
		if candidate == name {
			return true
		}
	}
	return false
}

// MaxStackSize 返回物品的堆叠上限（未收录的物品按 64 处理）。
func MaxStackSize(itemID int32) int32 {
	name, ok := ItemName(itemID)
	if !ok {
		return 64
	}
	if size, ok := MaxStackSizes[name]; ok && size > 0 {
		return size
	}
	return 64
}

// MaxDamage 返回物品的耐久上限；不可损坏物品返回 false。
func MaxDamage(itemID int32) (int32, bool) {
	name, ok := ItemName(itemID)
	if !ok {
		return 0, false
	}
	value, ok := MaxDamages[name]
	return value, ok
}

// ArmorStats 返回物品提供的护甲值与韧性（官方 attribute_modifiers 的
// minecraft:armor / minecraft:armor_toughness）；不提供护甲值时 ok 为 false。
func ArmorStats(itemID int32) (ArmorStat, bool) {
	name, ok := ItemName(itemID)
	if !ok {
		return ArmorStat{}, false
	}
	value, ok := ArmorStatsByItem[name]
	return value, ok
}

// IsRepairMaterial 报告 material 是否为 item 在铁砧中接受的修复材料
// （minecraft:repairable 组件；其中的标签引用已在生成时展开）。
func IsRepairMaterial(itemID, materialID int32) bool {
	name, ok := ItemName(itemID)
	if !ok {
		return false
	}
	for _, id := range RepairItems[name] {
		if id == materialID {
			return true
		}
	}
	return false
}

// ItemDisplayName 返回物品在服务端语言文件中的悬停名
// （原版 ItemStack.getHoverName().getString()；缺翻译时为翻译键）。
func ItemDisplayName(itemID int32) (string, bool) {
	name, ok := ItemName(itemID)
	if !ok {
		return "", false
	}
	display, ok := ItemDisplayNames[name]
	return display, ok
}

// HasStoredEnchantments 报告物品是否携带 stored_enchantments 组件
// （1.21.11 为附魔书；铁砧的 rightIsBook 判定）。
func HasStoredEnchantments(itemID int32) bool {
	name, ok := ItemName(itemID)
	if !ok {
		return false
	}
	return StoredEnchantmentItems[name]
}

var cachedTags = sync.OnceValue(func() []RegistryTags {
	names := make([]string, 0, len(allTags))
	for name := range allTags {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]RegistryTags, 0, len(names))
	for _, name := range names {
		items := make([]Tag, 0, len(allTags[name]))
		for _, tag := range allTags[name] {
			items = append(items, Tag{Name: tag.name, Entries: tag.entries})
		}
		result = append(result, RegistryTags{Name: name, Tags: items})
	}
	return result
})

// staticIndex 是 StaticEntryIDs 的查询副本（注册表名 → 条目 → ID）。
var staticIndex = sync.OnceValue(func() map[string]map[string]int32 {
	return StaticEntryIDs
})

// StaticEntryID 查询静态注册表（条目 ID 由客户端内置注册表决定，如实体类型、
// 音效事件）中条目的 protocol_id。
func StaticEntryID(registryName, entry string) (int32, bool) {
	id, ok := staticIndex()[registryName][entry]
	return id, ok
}

// syncIndex 是同步注册表的反向索引（注册表名 → 条目 → ID）。
var syncIndex = sync.OnceValue(func() map[string]map[string]int32 {
	index := make(map[string]map[string]int32, len(syncEntries))
	for name, entries := range syncEntries {
		byName := make(map[string]int32, len(entries))
		for id, entry := range entries {
			byName[entry] = int32(id)
		}
		index[name] = byName
	}
	return index
})

// SyncEntryID 查询同步注册表条目的 ID（即服务器通过 Registry Data 发送的顺序）。
func SyncEntryID(registryName, entry string) (int32, bool) {
	id, ok := syncIndex()[registryName][entry]
	return id, ok
}

// AllTags 返回需要由服务器在配置阶段通过 Update Tags 完整发送的全部注册表标签：
// 同步注册表（ID 为服务器发送顺序）与静态注册表（ID 为客户端内置的全局注册表 ID）。
// 标签无法通过 Known Packs 获取，缺失会导致客户端解析本地数据失败。
// 调用者不得修改返回的切片内容。
func AllTags() []RegistryTags {
	return cachedTags()
}
