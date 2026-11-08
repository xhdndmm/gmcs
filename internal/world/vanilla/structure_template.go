//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"encoding/json"
	"fmt"
	"strings"
)

// structureTemplate 是原版 structure/*.nbt 的内存表示。
type structureTemplate struct {
	Size     [3]int
	Palette  []SurfaceBlock
	Blocks   []templateBlock
	Entities []nbtCompound
}

type templateBlock struct {
	Pos   [3]int
	State int
	NBT   nbtCompound
}

// jigsawBlock 是模板中 jigsaw 方块实体携带的连接信息。
type jigsawBlock struct {
	Pos               [3]int
	Name              string
	Target            string
	Pool              string
	FinalState        string
	Joint             string
	Direction         string
	SelectionPriority int
	PlacementPriority int
}

// templatePool 是原版 template_pool JSON 的解析结果。
type templatePool struct {
	Fallback string
	Elements []templatePoolElement
}

type templatePoolElement struct {
	Weight  int
	Element templatePoolElementDef
}

type templatePoolElementDef struct {
	Type       string
	Location   string
	Projection string
	Processors string
	Elements   []templatePoolElement
}

// parseStructureTemplate 解析 gzip/裸 NBT 结构模板。
func parseStructureTemplate(raw []byte) (*structureTemplate, error) {
	root, err := decodeNBT(raw)
	if err != nil {
		return nil, err
	}
	template := &structureTemplate{}
	if size, ok := nbtInts(root["size"]); ok && len(size) == 3 {
		template.Size = [3]int{size[0], size[1], size[2]}
	} else {
		return nil, fmt.Errorf("结构模板缺少 size")
	}
	if palette, ok := root["palette"].([]any); ok {
		for index, entry := range palette {
			compound, ok := nbtCompoundValue(entry)
			if !ok {
				return nil, fmt.Errorf("palette[%d] 不是 compound", index)
			}
			name, _ := nbtStringValue(compound["Name"])
			properties := map[string]string{}
			if props, ok := nbtCompoundValue(compound["Properties"]); ok {
				for key, value := range props {
					if text, ok := nbtStringValue(value); ok {
						properties[key] = text
					}
				}
			}
			template.Palette = append(template.Palette, SurfaceBlock{Name: qualify(name), Properties: properties})
		}
	}
	if blocks, ok := root["blocks"].([]any); ok {
		for index, entry := range blocks {
			compound, ok := nbtCompoundValue(entry)
			if !ok {
				return nil, fmt.Errorf("blocks[%d] 不是 compound", index)
			}
			pos, ok := nbtInts(compound["pos"])
			if !ok || len(pos) != 3 {
				return nil, fmt.Errorf("blocks[%d] 的 pos 非法", index)
			}
			state, _ := compound["state"].(int32)
			block := templateBlock{Pos: [3]int{pos[0], pos[1], pos[2]}, State: int(state)}
			if data, ok := nbtCompoundValue(compound["nbt"]); ok {
				block.NBT = data
			}
			template.Blocks = append(template.Blocks, block)
		}
	}
	if entities, ok := root["entities"].([]any); ok {
		for _, entry := range entities {
			if compound, ok := nbtCompoundValue(entry); ok {
				template.Entities = append(template.Entities, compound)
			}
		}
	}
	if len(template.Palette) == 0 && len(template.Blocks) > 0 {
		return nil, fmt.Errorf("结构模板有方块但没有 palette")
	}
	return template, nil
}

// jigsawBlocks 返回模板中的 jigsaw 连接块。
func (t *structureTemplate) jigsawBlocks() []jigsawBlock {
	result := make([]jigsawBlock, 0)
	for _, block := range t.Blocks {
		if block.NBT == nil {
			continue
		}
		id, _ := nbtStringValue(block.NBT["id"])
		if qualify(id) != "minecraft:jigsaw" {
			continue
		}
		jigsaw := jigsawBlock{Pos: block.Pos}
		if block.State >= 0 && block.State < len(t.Palette) {
			orientation := t.Palette[block.State].Properties["orientation"]
			if fields := strings.Split(orientation, "_"); len(fields) > 0 {
				jigsaw.Direction = fields[0]
			}
		}
		jigsaw.Name, _ = nbtStringValue(block.NBT["name"])
		jigsaw.Target, _ = nbtStringValue(block.NBT["target"])
		jigsaw.Pool, _ = nbtStringValue(block.NBT["pool"])
		jigsaw.FinalState, _ = nbtStringValue(block.NBT["final_state"])
		jigsaw.Joint, _ = nbtStringValue(block.NBT["joint"])
		jigsaw.SelectionPriority = nbtIntValue(block.NBT["selection_priority"])
		jigsaw.PlacementPriority = nbtIntValue(block.NBT["placement_priority"])
		result = append(result, jigsaw)
	}
	return result
}

func nbtIntValue(value any) int {
	switch number := value.(type) {
	case int8:
		return int(number)
	case int16:
		return int(number)
	case int32:
		return int(number)
	case int64:
		return int(number)
	}
	return 0
}

// parseTemplatePool 解析 template_pool JSON。
func parseTemplatePool(raw []byte) (*templatePool, error) {
	var encoded struct {
		Fallback string `json:"fallback"`
		Elements []struct {
			Weight  int             `json:"weight"`
			Element json.RawMessage `json:"element"`
		} `json:"elements"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, err
	}
	pool := &templatePool{Fallback: qualify(encoded.Fallback)}
	for index, entry := range encoded.Elements {
		element, err := parsePoolElement(entry.Element)
		if err != nil {
			return nil, fmt.Errorf("elements[%d]：%w", index, err)
		}
		pool.Elements = append(pool.Elements, templatePoolElement{Weight: entry.Weight, Element: element})
	}
	return pool, nil
}

func parsePoolElement(raw json.RawMessage) (templatePoolElementDef, error) {
	var encoded struct {
		Type        string          `json:"type"`
		ElementType string          `json:"element_type"`
		Location    string          `json:"location"`
		Projection  string          `json:"projection"`
		Processors  json.RawMessage `json:"processors"`
		Elements    []struct {
			Weight  int             `json:"weight"`
			Element json.RawMessage `json:"element"`
		} `json:"elements"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return templatePoolElementDef{}, err
	}
	elementType := encoded.Type
	if elementType == "" {
		elementType = encoded.ElementType
	}
	def := templatePoolElementDef{
		Type:       qualify(elementType),
		Location:   qualify(encoded.Location),
		Projection: encoded.Projection,
	}
	if len(encoded.Processors) != 0 {
		var processor string
		if err := json.Unmarshal(encoded.Processors, &processor); err == nil {
			def.Processors = qualify(processor)
		}
	}
	for _, entry := range encoded.Elements {
		element, err := parsePoolElement(entry.Element)
		if err != nil {
			return templatePoolElementDef{}, err
		}
		def.Elements = append(def.Elements, templatePoolElement{Weight: entry.Weight, Element: element})
	}
	return def, nil
}
