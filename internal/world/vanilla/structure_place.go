//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"fmt"
	"math"
	"strings"
)

// templateRotation 是结构模板的 0/90/180/270 度旋转。
type templateRotation int

const (
	rotateNone templateRotation = iota
	rotateClockwise90
	rotate180
	rotateCounterClockwise90
)

// transformTemplatePos 把模板局部坐标变换到放置后的局部坐标。
func transformTemplatePos(pos [3]int, size [3]int, rotation templateRotation, mirror bool) [3]int {
	x, y, z := pos[0], pos[1], pos[2]
	if mirror {
		x = size[0] - 1 - x
	}
	switch rotation {
	case rotateClockwise90:
		return [3]int{size[2] - 1 - z, y, x}
	case rotate180:
		return [3]int{size[0] - 1 - x, y, size[2] - 1 - z}
	case rotateCounterClockwise90:
		return [3]int{z, y, size[0] - 1 - x}
	default:
		return [3]int{x, y, z}
	}
}

// transformedSize 返回旋转后的模板尺寸。
func transformedSize(size [3]int, rotation templateRotation) [3]int {
	if rotation == rotateClockwise90 || rotation == rotateCounterClockwise90 {
		return [3]int{size[2], size[1], size[0]}
	}
	return size
}

// placeTemplate 把一个结构模板按旋转/镜像写入区块；越界部分裁掉。
func placeTemplate(chunk *GeneratedChunk, template *structureTemplate, originX, originY, originZ int, rotation templateRotation, mirror bool, random *legacyRandom) {
	for _, block := range template.Blocks {
		if block.State < 0 || block.State >= len(template.Palette) {
			continue
		}
		state := template.Palette[block.State]
		if state.Name == "minecraft:jigsaw" {
			// jigsaw 方块在最终结构中变成 final_state；空/缺省时保留空气。
			if final, ok := nbtStringValue(block.NBT["final_state"]); ok {
				if parsed, err := parseSimpleBlockState(final); err == nil {
					state = parsed
				}
			} else {
				continue
			}
		}
		pos := transformTemplatePos(block.Pos, template.Size, rotation, mirror)
		x, y, z := originX+pos[0], originY+pos[1], originZ+pos[2]
		if state.Name == "minecraft:structure_block" {
			placeStructureMarker(chunk, block.NBT, x, y, z, rotation, mirror, random)
			continue
		}
		if state.Name == "minecraft:structure_void" {
			continue
		}
		chunk.SetBlock(x, y, z, state)
		if entity, ok := generatedBlockEntityFromNBT(state.Name, block.NBT); ok {
			entity.Type = blockEntityTypeName(entity.Type)
			if random != nil {
				initLootBlockEntity(&entity, random)
			}
			chunk.SetBlockEntity(x, y, z, entity)
		}
	}
	// 模板自带的实体（村民/动物等）也转入生成阶段实体列表。服务器目前只
	// 接管其中的展示框/潜影贝；其他类型保留给后续实体系统扩展。
	for _, rawEntity := range template.Entities {
		pos, ok := templateEntityPos(rawEntity["pos"])
		if !ok {
			continue
		}
		transformed := transformTemplatePos([3]int{int(math.Floor(pos[0])), int(math.Floor(pos[1])), int(math.Floor(pos[2]))}, template.Size, rotation, mirror)
		entityType, _ := nbtStringValue(rawEntity["id"])
		if entityType == "" {
			continue
		}
		entity := GeneratedEntity{
			Type: qualify(entityType),
			Pos:  [3]float64{float64(originX+transformed[0]) + pos[0] - math.Floor(pos[0]), float64(originY+transformed[1]) + pos[1] - math.Floor(pos[1]), float64(originZ+transformed[2]) + pos[2] - math.Floor(pos[2])},
		}
		if item, ok := nbtCompoundValue(rawEntity["Item"]); ok {
			entity.Item, _ = nbtStringValue(item["id"])
			entity.Item = qualify(entity.Item)
		}
		chunk.AddEntity(entity)
	}
}

func templateEntityPos(value any) ([3]float64, bool) {
	list, ok := value.([]any)
	if !ok || len(list) < 3 {
		return [3]float64{}, false
	}
	var result [3]float64
	for i := 0; i < 3; i++ {
		switch number := list[i].(type) {
		case float64:
			result[i] = number
		case float32:
			result[i] = float64(number)
		case int32:
			result[i] = float64(number)
		case int64:
			result[i] = float64(number)
		default:
			return [3]float64{}, false
		}
	}
	return result, true
}

func parseSimpleBlockState(value string) (SurfaceBlock, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return SurfaceBlock{}, fmt.Errorf("空方块状态")
	}
	properties := map[string]string{}
	if index := strings.IndexByte(value, '['); index >= 0 && strings.HasSuffix(value, "]") {
		propertiesText := value[index+1 : len(value)-1]
		value = value[:index]
		for _, entry := range strings.Split(propertiesText, ",") {
			keyValue := strings.SplitN(entry, "=", 2)
			if len(keyValue) == 2 {
				properties[strings.TrimSpace(keyValue[0])] = strings.TrimSpace(keyValue[1])
			}
		}
	}
	return SurfaceBlock{Name: qualify(value), Properties: properties}, nil
}

// templateCornerAirCount 统计模板四角在目标位置是否为空气，用于化石等结构
// 的“空角”约束。
func templateCornerAirCount(chunk *GeneratedChunk, template *structureTemplate, originX, originY, originZ int, rotation templateRotation, mirror bool) int {
	size := transformedSize(template.Size, rotation)
	corners := [8][3]int{
		{0, 0, 0}, {size[0] - 1, 0, 0}, {0, 0, size[2] - 1}, {size[0] - 1, 0, size[2] - 1},
		{0, size[1] - 1, 0}, {size[0] - 1, size[1] - 1, 0}, {0, size[1] - 1, size[2] - 1}, {size[0] - 1, size[1] - 1, size[2] - 1},
	}
	empty := 0
	for _, corner := range corners {
		if chunk.BlockAt(originX+corner[0], originY+corner[1], originZ+corner[2]).IsAir() {
			empty++
		}
	}
	return empty
}

// placeStructureMarker 处理末地城等模板中的结构方块标记。原版模板用
// mode=DATA / metadata=Sentry|Chest|Elytra 表示要生成的实体或箱子。
func placeStructureMarker(chunk *GeneratedChunk, data nbtCompound, x, y, z int, rotation templateRotation, mirror bool, random *legacyRandom) {
	metadata, _ := nbtStringValue(data["metadata"])
	switch metadata {
	case "Chest":
		chunk.SetBlock(x, y, z, SurfaceBlock{Name: "minecraft:chest", Properties: map[string]string{"facing": "north", "waterlogged": "false"}})
		chunk.SetBlockEntity(x, y, z, GeneratedBlockEntity{
			Type: "minecraft:chest", LootTable: "minecraft:chests/end_city_treasure", Data: nbtCompound{"id": "minecraft:chest"},
		})
	case "Sentry":
		chunk.AddEntity(GeneratedEntity{Type: "minecraft:shulker", Pos: [3]float64{float64(x) + 0.5, float64(y), float64(z) + 0.5}})
	case "Elytra":
		facing := rotateDirection("north", rotation, mirror)
		chunk.AddEntity(GeneratedEntity{
			Type: "minecraft:item_frame",
			Pos:  [3]float64{float64(x) + 0.5, float64(y), float64(z) + 0.5},
			Item: "minecraft:elytra", Facing: facing,
		})
	}
	_ = random
}
