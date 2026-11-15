//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import "strings"

// GeneratedItem 是生成阶段方块实体容器中的一个物品。
// 只保留注册名和数量；组件数据由接线层按需转换。
type GeneratedItem struct {
	Name  string
	Count int
}

// GeneratedBlockEntity 是结构模板中携带的方块实体初始数据。
// Data 保留原始 NBT，便于以后接入刷怪笼、告示牌等类型。
type GeneratedBlockEntity struct {
	Type      string
	Items     []GeneratedItem
	LootTable string
	Data      nbtCompound
}

// blockEntityBlock 报告方块名是否为需要生成初始数据的方块实体。
func blockEntityBlock(name string) bool {
	switch name {
	case "minecraft:chest", "minecraft:trapped_chest", "minecraft:barrel",
		"minecraft:dispenser", "minecraft:dropper", "minecraft:hopper",
		"minecraft:furnace", "minecraft:blast_furnace", "minecraft:smoker",
		"minecraft:brewing_stand", "minecraft:spawner", "minecraft:sign",
		"minecraft:hanging_sign", "minecraft:lectern", "minecraft:beehive",
		"minecraft:bee_nest", "minecraft:jukebox", "minecraft:decorated_pot",
		"minecraft:crafter", "minecraft:chiseled_bookshelf", "minecraft:end_gateway":
		return true
	}
	return false
}

// generatedBlockEntityFromNBT 从结构模板方块 NBT 中解析方块实体初始数据。
func generatedBlockEntityFromNBT(name string, data nbtCompound) (GeneratedBlockEntity, bool) {
	if data == nil || !blockEntityBlock(name) {
		return GeneratedBlockEntity{}, false
	}
	entity := GeneratedBlockEntity{Type: name, LootTable: ""}
	if table, ok := nbtStringValue(data["LootTable"]); ok && table != "" {
		entity.LootTable = qualify(table)
	}
	if rawItems, ok := data["Items"].([]any); ok {
		for _, raw := range rawItems {
			item, ok := nbtCompoundValue(raw)
			if !ok {
				continue
			}
			itemName, _ := nbtStringValue(item["id"])
			if itemName == "" {
				continue
			}
			count := 1
			if value, ok := item["count"].(int8); ok {
				count = int(value)
			} else if value, ok := item["Count"].(int8); ok {
				count = int(value)
			} else if value, ok := item["count"].(int32); ok {
				count = int(value)
			} else if value, ok := item["Count"].(int32); ok {
				count = int(value)
			}
			if count <= 0 {
				continue
			}
			entity.Items = append(entity.Items, GeneratedItem{Name: qualify(itemName), Count: count})
		}
	}
	entity.Data = data
	return entity, true
}

// SetBlockEntity 设置生成区块中的方块实体；越界坐标忽略。
func (c *GeneratedChunk) SetBlockEntity(x, y, z int, entity GeneratedBlockEntity) {
	if !c.InRange(x, y, z) {
		return
	}
	if c.blockEntities == nil {
		c.blockEntities = make(map[[3]int]GeneratedBlockEntity)
	}
	c.blockEntities[[3]int{x, y, z}] = entity
}

// BlockEntityAt 返回生成区块中坐标处的方块实体。
func (c *GeneratedChunk) BlockEntityAt(x, y, z int) (GeneratedBlockEntity, bool) {
	entity, ok := c.blockEntities[[3]int{x, y, z}]
	return entity, ok
}

// ForEachBlockEntity 遍历生成区块中的方块实体；按写入 map 的顺序，调用方
// 不应依赖顺序。
func (c *GeneratedChunk) ForEachBlockEntity(fn func(x, y, z int, entity GeneratedBlockEntity)) {
	for pos, entity := range c.blockEntities {
		fn(pos[0], pos[1], pos[2], entity)
	}
}

// blockEntityTypeName 规范化模板中的方块实体 id，避免早期数据缺少命名空间。
func blockEntityTypeName(name string) string {
	if strings.Contains(name, ":") {
		return name
	}
	return "minecraft:" + name
}
