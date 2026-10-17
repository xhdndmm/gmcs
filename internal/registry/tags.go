//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package registry

import "sync"

// 静态注册表标签的查询辅助（伤害类型、实体类型、物品）。
//
// 这些标签的值是客户端内置注册表的 protocol_id（见 data_generated.go 的
// allTags），与 StaticEntryID 使用同一套 ID。标签集合在首次使用时建立，
// 之后为 O(1) 查询；标签规模很小（伤害类型 50 项、实体类型 150 项左右）。

// damageTypeTagSets 是“伤害类型标签名 → 伤害类型 ID 集合”。
var damageTypeTagSets = sync.OnceValue(func() map[string]map[int32]struct{} {
	return buildTagSets("minecraft:damage_type")
})

// entityTypeTagSets 是“实体类型标签名 → 实体类型 ID 集合”。
var entityTypeTagSets = sync.OnceValue(func() map[string]map[int32]struct{} {
	return buildTagSets("minecraft:entity_type")
})

// itemTagSets 是“物品标签名 → 物品 ID 集合”。
var itemTagSets = sync.OnceValue(func() map[string]map[int32]struct{} {
	return buildTagSets("minecraft:item")
})

func buildTagSets(registryName string) map[string]map[int32]struct{} {
	tags := allTags[registryName]
	sets := make(map[string]map[int32]struct{}, len(tags))
	for _, tag := range tags {
		ids := make(map[int32]struct{}, len(tag.entries))
		for _, id := range tag.entries {
			ids[id] = struct{}{}
		}
		sets[tag.name] = ids
	}
	return sets
}

// DamageTypeInTag 报告伤害类型是否属于指定标签
// （如 minecraft:bypasses_armor、minecraft:is_fall、minecraft:is_explosion）。
func DamageTypeInTag(tagName string, damageTypeID int32) bool {
	_, ok := damageTypeTagSets()[tagName][damageTypeID]
	return ok
}

// EntityTypeInTag 报告实体类型是否属于指定标签
// （如 minecraft:sensitive_to_smite、minecraft:sensitive_to_bane_of_arthropods）。
func EntityTypeInTag(tagName string, entityTypeID int32) bool {
	_, ok := entityTypeTagSets()[tagName][entityTypeID]
	return ok
}

// ItemInTag 报告物品是否属于指定物品标签（如 minecraft:enchantable/armor）。
func ItemInTag(tagName string, itemID int32) bool {
	_, ok := itemTagSets()[tagName][itemID]
	return ok
}
