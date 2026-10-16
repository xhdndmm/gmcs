//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package registry

import "sync"

// BlockTagContains 报告方块状态 state 所属的方块是否属于方块标签 tagName
// （如 "minecraft:infiniburn_overworld"、"minecraft:soul_fire_base_blocks"）。
//
// 生成数据（data_generated.go 的 allTags）中的方块标签值是方块注册表 ID；
// blockStateNameRanges 按方块注册顺序排列，其下标即方块注册表 ID，
// 因此可把标签值换算成方块名后按名字匹配。未收录的标签名返回 false。
func BlockTagContains(tagName string, state uint16) bool {
	name, ok := BlockNameByState(state)
	if !ok {
		return false
	}
	_, ok = blockTagNameSets()[tagName][name]
	return ok
}

// blockTagNameSets 是“方块标签名 → 方块名集合”的查询索引（首次使用时构建）。
var blockTagNameSets = sync.OnceValue(func() map[string]map[string]struct{} {
	blockTags := allTags["minecraft:block"]
	sets := make(map[string]map[string]struct{}, len(blockTags))
	for _, tag := range blockTags {
		names := make(map[string]struct{}, len(tag.entries))
		for _, id := range tag.entries {
			if id < 0 || int(id) >= len(blockStateNameRanges) {
				continue
			}
			names[blockStateNameRanges[id].Name] = struct{}{}
		}
		sets[tag.name] = names
	}
	return sets
})
