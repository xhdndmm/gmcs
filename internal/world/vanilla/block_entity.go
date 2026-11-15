//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"encoding/json"
	"strings"
)

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
	// 刷怪笼数据（结构模板中的 SpawnData/Delay/MinSpawnDelay/MaxSpawnDelay）。
	SpawnerEntity   string
	SpawnerDelay    int
	SpawnerMinDelay int
	SpawnerMaxDelay int
	// 告示牌文本（front_text/back_text 的 messages，已去掉 JSON 引号）。
	FrontText [4]string
	BackText  [4]string
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
	if name == "minecraft:spawner" {
		if spawnData, ok := nbtCompoundValue(data["SpawnData"]); ok {
			if spawnEntity, ok := nbtCompoundValue(spawnData["entity"]); ok {
				entity.SpawnerEntity, _ = nbtStringValue(spawnEntity["id"])
				entity.SpawnerEntity = qualify(entity.SpawnerEntity)
			}
		}
		entity.SpawnerDelay = nbtIntValue(data["Delay"])
		entity.SpawnerMinDelay = nbtIntValue(data["MinSpawnDelay"])
		entity.SpawnerMaxDelay = nbtIntValue(data["MaxSpawnDelay"])
	}
	if name == "minecraft:sign" || name == "minecraft:hanging_sign" {
		entity.FrontText = signMessages(data, "front_text")
		entity.BackText = signMessages(data, "back_text")
	}
	entity.Data = data
	return entity, true
}

// signMessages 读取告示牌一侧的 4 行文本。模板 NBT 中 messages 是 JSON
// 文本组件字符串；这里保存其文本展示值，无法解析时保留原字符串。
func signMessages(data nbtCompound, side string) [4]string {
	var result [4]string
	text, ok := nbtCompoundValue(data[side])
	if !ok {
		return result
	}
	messages, ok := text["messages"].([]any)
	if !ok {
		return result
	}
	for index := 0; index < len(messages) && index < 4; index++ {
		raw, ok := nbtStringValue(messages[index])
		if !ok {
			continue
		}
		result[index] = decodeTextComponent(raw)
	}
	return result
}

// SetBlockEntity 设置生成区块中的方块实体；越界坐标忽略。
func (c *GeneratedChunk) SetBlockEntity(x, y, z int, entity GeneratedBlockEntity) {
	if c.captureEntities != nil && (!c.captureOutside || !c.InRange(x, y, z)) {
		c.captureEntities[[3]int{x, y, z}] = entity
	}
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

// decodeTextComponent 把告示牌 JSON 文本组件压缩为可展示文本；支持常见的
// {"text":"..."} 与纯字符串。原版完整组件保留在模板 Data 中。
func decodeTextComponent(raw string) string {
	var text string
	if json.Unmarshal([]byte(raw), &text) == nil {
		return text
	}
	var component struct {
		Text string `json:"text"`
	}
	if json.Unmarshal([]byte(raw), &component) == nil {
		return component.Text
	}
	return raw
}
