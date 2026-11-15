//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package main

import (
	"encoding/json"
	"math"
	"strings"

	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// vanillaDataVersion 是 1.21.11（world version 4671）的 DataVersion。
const vanillaDataVersion = 4671

// convertVanillaChunk 把 Anvil chunk NBT 转为 gmcs 内存区块。
func convertVanillaChunk(root compound, dim world.Dimension) (*world.Chunk, error) {
	x, _ := number(root["xPos"])
	z, _ := number(root["zPos"])
	chunk := world.NewChunkDim(int(x), int(z), dim)
	sections, _ := root["sections"].([]any)
	var biomeGrid [16]uint16
	for i := range biomeGrid {
		biomeGrid[i] = plainsBiomeID()
	}
	for _, raw := range sections {
		section, ok := raw.(compound)
		if !ok {
			continue
		}
		sectionY, _ := number(section["Y"])
		blockStates, _ := section["block_states"].(compound)
		if blockStates != nil {
			palette, _ := blockStates["palette"].([]any)
			data, _ := blockStates["data"].([]int64)
			states := decodeVanillaPalette(palette, data, 4096)
			for index, state := range states {
				localX := index & 15
				localZ := (index >> 4) & 15
				localY := (index >> 8) & 15
				y := int(sectionY)*16 + localY
				if y < world.WorldMinY || y >= world.WorldMinY+world.WorldHeight {
					continue
				}
				chunk.SetBlockState(localX, y, localZ, state)
			}
		}
		biomes, _ := section["biomes"].(compound)
		if biomes != nil && sectionY == 0 {
			palette, _ := biomes["palette"].([]any)
			data, _ := biomes["data"].([]int64)
			values := decodeBiomePalette(palette, data, 64)
			for cellZ := 0; cellZ < 4; cellZ++ {
				for cellX := 0; cellX < 4; cellX++ {
					index := (0*4+cellZ)*4 + cellX
					if index < len(values) && values[index] != 0 {
						biomeGrid[cellZ*4+cellX] = values[index]
					}
				}
			}
		}
	}
	chunk.SetBiomeGrid(biomeGrid)
	if entities, ok := root["block_entities"].([]any); ok {
		for _, raw := range entities {
			entity, ok := raw.(compound)
			if !ok {
				continue
			}
			blockEntity, bx, by, bz, ok := parseVanillaBlockEntity(entity)
			if !ok {
				continue
			}
			chunk.SetBlockEntity(int(bx)-chunk.X*16, int(by), int(bz)-chunk.Z*16, blockEntity)
		}
	}
	return chunk, nil
}

func plainsBiomeID() uint16 {
	if id, ok := registry.SyncEntryID("minecraft:worldgen/biome", "minecraft:plains"); ok {
		return uint16(id)
	}
	return world.BiomePlains
}

func decodeVanillaPalette(palette []any, data []int64, count int) []uint16 {
	if len(palette) == 0 {
		return make([]uint16, count)
	}
	values := make([]uint16, count)
	if len(palette) == 1 || len(data) == 0 {
		state := paletteStateID(palette[0])
		for i := range values {
			values[i] = state
		}
		return values
	}
	bits := int(math.Ceil(math.Log2(float64(len(palette)))))
	if bits < 4 {
		bits = 4
	}
	perLong := 64 / bits
	mask := uint64(1<<bits) - 1
	for i := 0; i < count; i++ {
		value := (uint64(data[i/perLong]) >> uint((i%perLong)*bits)) & mask
		if int(value) < len(palette) {
			values[i] = paletteStateID(palette[value])
		}
	}
	return values
}

func paletteStateID(value any) uint16 {
	entry, ok := value.(compound)
	if !ok {
		if name, ok := value.(string); ok {
			if id, err := registry.BlockStateID(name); err == nil {
				return id
			}
		}
		return 0
	}
	name, _ := entry["Name"].(string)
	properties := map[string]string{}
	if props, ok := entry["Properties"].(compound); ok {
		for key, value := range props {
			if text, ok := value.(string); ok {
				properties[key] = text
			}
		}
	}
	if len(properties) > 0 {
		if id, ok := registry.BlockStateWithProps(name, properties); ok {
			return id
		}
	}
	if id, err := registry.BlockStateID(name); err == nil {
		return id
	}
	return 0
}

func decodeBiomePalette(palette []any, data []int64, count int) []uint16 {
	values := make([]uint16, count)
	if len(palette) == 0 {
		fallback := plainsBiomeID()
		for i := range values {
			values[i] = fallback
		}
		return values
	}
	ids := make([]uint16, len(palette))
	for i, entry := range palette {
		name, _ := entry.(string)
		if id, ok := registry.SyncEntryID("minecraft:worldgen/biome", name); ok {
			ids[i] = uint16(id)
		} else {
			ids[i] = plainsBiomeID()
		}
	}
	if len(palette) == 1 || len(data) == 0 {
		for i := range values {
			values[i] = ids[0]
		}
		return values
	}
	bits := int(math.Ceil(math.Log2(float64(len(palette)))))
	if bits < 1 {
		bits = 1
	}
	perLong := 64 / bits
	mask := uint64(1<<bits) - 1
	for i := 0; i < count; i++ {
		value := (uint64(data[i/perLong]) >> uint((i%perLong)*bits)) & mask
		if int(value) < len(ids) {
			values[i] = ids[value]
		}
	}
	return values
}

func biomeName(id uint16) string {
	names := registry.SyncEntryNames("minecraft:worldgen/biome")
	if int(id) < len(names) {
		return names[id]
	}
	return "minecraft:plains"
}

func number(value any) (int32, bool) {
	switch v := value.(type) {
	case int8:
		return int32(v), true
	case int16:
		return int32(v), true
	case int32:
		return v, true
	case int64:
		return int32(v), true
	case int:
		return int32(v), true
	}
	return 0, false
}

func parseVanillaBlockEntity(value compound) (world.BlockEntity, int32, int32, int32, bool) {
	id, _ := value["id"].(string)
	typeID, ok := registry.StaticEntryID("minecraft:block_entity_type", id)
	if !ok {
		return world.BlockEntity{}, 0, 0, 0, false
	}
	x, _ := number(value["x"])
	y, _ := number(value["y"])
	z, _ := number(value["z"])
	entity := world.BlockEntity{TypeID: typeID}
	if table, ok := value["LootTable"].(string); ok {
		entity.LootTable = table
	}
	if items, ok := value["Items"].([]any); ok {
		maxSlot := -1
		type parsedItem struct {
			slot int
			item world.ContainerItem
		}
		parsed := make([]parsedItem, 0, len(items))
		for _, raw := range items {
			item, ok := raw.(compound)
			if !ok {
				continue
			}
			itemName, _ := item["id"].(string)
			if itemName == "" {
				continue
			}
			itemID, err := registry.ItemID(itemName)
			if err != nil {
				continue
			}
			count, _ := number(item["count"])
			if count == 0 {
				count, _ = number(item["Count"])
			}
			slot, _ := number(item["Slot"])
			if int(slot) > maxSlot {
				maxSlot = int(slot)
			}
			parsed = append(parsed, parsedItem{slot: int(slot), item: world.ContainerItem{ItemID: itemID, Count: count}})
		}
		if maxSlot >= 0 {
			entity.Items = make([]world.ContainerItem, maxSlot+1)
			for _, entry := range parsed {
				if entry.slot >= 0 && entry.slot < len(entity.Items) {
					entity.Items[entry.slot] = entry.item
				}
			}
		}
	}
	if spawner, ok := value["SpawnData"].(compound); ok {
		if e, ok := spawner["entity"].(compound); ok {
			entity.SpawnerEntity, _ = e["id"].(string)
		}
	}
	if delay, ok := number(value["Delay"]); ok {
		entity.SpawnerDelay = delay
	}
	if delay, ok := number(value["MinSpawnDelay"]); ok {
		entity.SpawnerMinDelay = delay
	}
	if delay, ok := number(value["MaxSpawnDelay"]); ok {
		entity.SpawnerMaxDelay = delay
	}
	entity.FrontText = vanillaSignText(value, "front_text")
	entity.BackText = vanillaSignText(value, "back_text")
	if exact, ok := value["exact"].(int8); ok {
		entity.GatewayExact = exact != 0
	}
	if exit, ok := value["exit"].([]int32); ok && len(exit) == 3 {
		entity.GatewayExit = [3]int{int(exit[0]), int(exit[1]), int(exit[2])}
	}
	return entity, x, y, z, true
}

func vanillaSignText(value compound, side string) [4]string {
	var result [4]string
	text, _ := value[side].(compound)
	messages, _ := text["messages"].([]any)
	for i := 0; i < len(messages) && i < 4; i++ {
		raw, _ := messages[i].(string)
		result[i] = decodeTextComponent(raw)
	}
	return result
}

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

// buildVanillaChunk 把 gmcs 内存区块编码为现代 Anvil chunk NBT。
func buildVanillaChunk(chunk *world.Chunk, dim world.Dimension) (compound, error) {
	sections := make([]any, 0)
	minSection := dim.MinY() / 16
	maxSection := (dim.MinY() + dim.Height()) / 16
	for sectionY := minSection; sectionY < maxSection; sectionY++ {
		states := make([]uint16, 4096)
		hasBlock := false
		for localY := 0; localY < 16; localY++ {
			y := sectionY*16 + localY
			for localZ := 0; localZ < 16; localZ++ {
				for localX := 0; localX < 16; localX++ {
					state := chunk.GetBlockState(localX, y, localZ)
					idx := (localY << 8) | (localZ << 4) | localX
					states[idx] = state
					if state != 0 {
						hasBlock = true
					}
				}
			}
		}
		if !hasBlock {
			continue
		}
		palette, data := encodePalette(states, 4)
		blockStates := compound{"palette": palette}
		if len(data) > 0 {
			blockStates["data"] = data
		}
		biomePalette, biomeData := encodeBiomes(chunk)
		biomes := compound{"palette": biomePalette}
		if len(biomeData) > 0 {
			biomes["data"] = biomeData
		}
		sections = append(sections, compound{
			"Y":            int8(sectionY),
			"block_states": blockStates,
			"biomes":       biomes,
		})
	}
	blockEntities := make([]any, 0)
	chunk.ForEachBlockEntity(func(x, y, z int, entity world.BlockEntity) {
		if converted, ok := buildVanillaBlockEntity(entity, chunk.X*16+x, y, chunk.Z*16+z); ok {
			blockEntities = append(blockEntities, converted)
		}
	})
	heightmap := encodeHeightmap(chunk, dim)
	heightmaps := compound{
		"MOTION_BLOCKING": heightmap,
		"WORLD_SURFACE":   heightmap,
	}
	root := compound{
		"DataVersion":    int32(vanillaDataVersion),
		"xPos":           int32(chunk.X),
		"zPos":           int32(chunk.Z),
		"yPos":           int32(minSection),
		"Status":         "minecraft:full",
		"LastUpdate":     int64(0),
		"InhabitedTime":  int64(0),
		"sections":       sections,
		"block_entities": blockEntities,
		"entities":       []any{},
		"Heightmaps":     heightmaps,
		"isLightOn":      int8(1),
		"structures":     compound{"starts": compound{}},
	}
	return root, nil
}

func encodePalette(states []uint16, minBits int) ([]any, []int64) {
	palette := make([]uint16, 0)
	index := make(map[uint16]int)
	for _, state := range states {
		if _, ok := index[state]; !ok {
			index[state] = len(palette)
			palette = append(palette, state)
		}
	}
	paletteNBT := make([]any, 0, len(palette))
	for _, state := range palette {
		paletteNBT = append(paletteNBT, blockStatePalette(state))
	}
	if len(palette) <= 1 {
		return paletteNBT, nil
	}
	bits := int(math.Ceil(math.Log2(float64(len(palette)))))
	if bits < minBits {
		bits = minBits
	}
	data := packValues(states, bits, index)
	return paletteNBT, data
}

func blockStatePalette(state uint16) compound {
	name, props, ok := registry.StateProps(state)
	if !ok {
		name, _ = registry.BlockNameByState(state)
	}
	if name == "" {
		name = "minecraft:air"
	}
	result := compound{"Name": name}
	if len(props) > 0 {
		properties := compound{}
		for key, value := range props {
			properties[key] = value
		}
		result["Properties"] = properties
	}
	return result
}

func encodeBiomes(chunk *world.Chunk) ([]any, []int64) {
	grid := chunk.BiomeGrid()
	palette := make([]uint16, 0)
	index := make(map[uint16]int)
	values := make([]uint16, 64)
	for i := 0; i < 64; i++ {
		biome := grid[i%16]
		values[i] = biome
		if _, ok := index[biome]; !ok {
			index[biome] = len(palette)
			palette = append(palette, biome)
		}
	}
	paletteNBT := make([]any, 0, len(palette))
	for _, biome := range palette {
		paletteNBT = append(paletteNBT, biomeName(biome))
	}
	if len(palette) <= 1 {
		return paletteNBT, nil
	}
	bits := int(math.Ceil(math.Log2(float64(len(palette)))))
	if bits < 1 {
		bits = 1
	}
	return paletteNBT, packValues(values, bits, index)
}

func packValues(values []uint16, bits int, index map[uint16]int) []int64 {
	perLong := 64 / bits
	mask := uint64(1<<bits) - 1
	data := make([]int64, (len(values)+perLong-1)/perLong)
	for i, state := range values {
		value := uint64(index[state]) & mask
		data[i/perLong] |= int64(value << uint((i%perLong)*bits))
	}
	return data
}

func encodeHeightmap(chunk *world.Chunk, dim world.Dimension) []int64 {
	values := make([]uint16, 256)
	for z := 0; z < 16; z++ {
		for x := 0; x < 16; x++ {
			column := chunk.Column(x, z)
			height := 0
			if column.HasTop {
				height = column.TopY - dim.MinY() + 1
			}
			if height < 0 {
				height = 0
			}
			if height > 511 {
				height = 511
			}
			values[z*16+x] = uint16(height)
		}
	}
	data := make([]int64, 37)
	const perLong = 7
	for i, value := range values {
		data[i/perLong] |= int64(uint64(value) << uint((i%perLong)*9))
	}
	return data
}

func buildVanillaBlockEntity(entity world.BlockEntity, x, y, z int) (compound, bool) {
	name, ok := registry.StaticEntryName("minecraft:block_entity_type", entity.TypeID)
	if !ok {
		return nil, false
	}
	result := compound{
		"id": name,
		"x":  int32(x), "y": int32(y), "z": int32(z),
	}
	if entity.LootTable != "" {
		result["LootTable"] = entity.LootTable
	}
	if len(entity.Items) > 0 {
		items := make([]any, 0, len(entity.Items))
		for slot, item := range entity.Items {
			if item.IsEmpty() {
				continue
			}
			name, ok := registry.ItemName(item.ItemID)
			if !ok {
				continue
			}
			items = append(items, compound{
				"Slot": int8(slot), "id": name, "count": int32(item.Count),
			})
		}
		result["Items"] = items
	}
	if entity.SpawnerEntity != "" {
		result["SpawnData"] = compound{"entity": compound{"id": entity.SpawnerEntity}}
		result["Delay"] = entity.SpawnerDelay
		result["MinSpawnDelay"] = entity.SpawnerMinDelay
		result["MaxSpawnDelay"] = entity.SpawnerMaxDelay
	}
	if entity.FrontText != [4]string{} {
		result["front_text"] = signTextNBT(entity.FrontText)
	}
	if entity.BackText != [4]string{} {
		result["back_text"] = signTextNBT(entity.BackText)
	}
	if entity.GatewayExact || entity.GatewayExit != [3]int{} {
		result["exact"] = int8(boolByte(entity.GatewayExact))
		result["exit"] = []int32{int32(entity.GatewayExit[0]), int32(entity.GatewayExit[1]), int32(entity.GatewayExit[2])}
	}
	return result, true
}

func signTextNBT(lines [4]string) compound {
	messages := make([]any, 4)
	for i, line := range lines {
		encoded, _ := json.Marshal(struct {
			Text string `json:"text"`
		}{Text: line})
		messages[i] = string(encoded)
	}
	return compound{"messages": messages}
}

func boolByte(value bool) byte {
	if value {
		return 1
	}
	return 0
}

func stripNamespace(value string) string {
	return strings.TrimPrefix(value, "minecraft:")
}
