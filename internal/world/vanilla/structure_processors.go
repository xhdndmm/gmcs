//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"encoding/json"
	"strings"

	"gmcs/internal/registry"
)

type processorList struct {
	Processors []processorDefinition
}

type processorDefinition struct {
	Type           string
	Integrity      float32
	Rottable       string
	RottableBlocks []string
	Rules          []processorRule
}

type processorRule struct {
	InputType   string
	Block       string
	Tag         string
	Properties  map[string]string
	Probability float32
	Output      *SurfaceBlock
	OutputAir   bool
}

// applyProcessorList 对模板做一份副本并应用 processor_list。
// 当前实现 block_rot 与 rule/random_block_match；其余处理器保留原方块。
func (r *structureRuntime) applyProcessorList(template *structureTemplate, name string, random *legacyRandom) *structureTemplate {
	if name == "" {
		return template
	}
	raw, ok := r.data.ProcessorList(name)
	if !ok {
		return template
	}
	var encoded struct {
		Processors []struct {
			Type      string          `json:"processor_type"`
			Integrity float32         `json:"integrity"`
			Rottable  json.RawMessage `json:"rottable_blocks"`
			Value     json.RawMessage `json:"value"`
			Blocks    json.RawMessage `json:"blocks"`
			Rules     []struct {
				InputPredicate struct {
					PredicateType string            `json:"predicate_type"`
					Block         string            `json:"block"`
					Tag           string            `json:"tag"`
					Properties    map[string]string `json:"properties"`
					Probability   float32           `json:"probability"`
				} `json:"input_predicate"`
				OutputState json.RawMessage `json:"output_state"`
			} `json:"rules"`
		} `json:"processors"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return template
	}
	clone := &structureTemplate{Size: template.Size, Palette: template.Palette, Blocks: append([]templateBlock(nil), template.Blocks...)}
	for _, processor := range encoded.Processors {
		switch qualify(processor.Type) {
		case "minecraft:block_rot":
			rottable := map[string]struct{}{}
			var single string
			if json.Unmarshal(processor.Rottable, &single) == nil {
				rottable[qualify(single)] = struct{}{}
			} else {
				var many []string
				if json.Unmarshal(processor.Rottable, &many) == nil {
					for _, block := range many {
						rottable[qualify(block)] = struct{}{}
					}
				}
			}
			for i := range clone.Blocks {
				if clone.Blocks[i].State < 0 || clone.Blocks[i].State >= len(clone.Palette) {
					continue
				}
				block := clone.Palette[clone.Blocks[i].State]
				if !processorMatches(rottable, block.Name) {
					continue
				}
				if random.nextFloat() > processor.Integrity {
					clone.Blocks[i].State = -1
				}
			}
		case "minecraft:protected_blocks":
			protected := parseBlockList(processor.Value)
			for i := range clone.Blocks {
				if clone.Blocks[i].State < 0 || clone.Blocks[i].State >= len(clone.Palette) {
					continue
				}
				if processorMatches(protected, clone.Palette[clone.Blocks[i].State].Name) {
					clone.Blocks[i].State = -1
				}
			}
		case "minecraft:block_ignore":
			ignored := parseBlockList(processor.Blocks)
			for i := range clone.Blocks {
				if clone.Blocks[i].State < 0 || clone.Blocks[i].State >= len(clone.Palette) {
					continue
				}
				if processorMatches(ignored, clone.Palette[clone.Blocks[i].State].Name) {
					clone.Blocks[i].State = -1
				}
			}
		case "minecraft:rule":
			for _, rule := range processor.Rules {
				if rule.OutputState == nil {
					continue
				}
				output, err := parseFeatureSurfaceBlock(rule.OutputState)
				if err != nil {
					continue
				}
				inputType := qualify(rule.InputPredicate.PredicateType)
				if inputType == "" {
					inputType = "minecraft:block_match"
				}
				inputBlock := qualify(rule.InputPredicate.Block)
				inputTag := strings.TrimPrefix(rule.InputPredicate.Tag, "#")
				inputProperties := rule.InputPredicate.Properties
				probability := rule.InputPredicate.Probability
				stateIndex := -1
				for i, block := range clone.Palette {
					if block.Name == output.Name {
						stateIndex = i
						break
					}
				}
				if stateIndex < 0 {
					clone.Palette = append(clone.Palette, output)
					stateIndex = len(clone.Palette) - 1
				}
				for i := range clone.Blocks {
					if clone.Blocks[i].State < 0 || clone.Blocks[i].State >= len(clone.Palette) {
						continue
					}
					block := clone.Palette[clone.Blocks[i].State]
					if !processorInputMatches(inputType, inputBlock, inputTag, inputProperties, block) {
						continue
					}
					if probability > 0 && random.nextFloat() >= probability {
						continue
					}
					clone.Blocks[i].State = stateIndex
				}
			}
		}
	}
	// State -1 表示被 block_rot 移除，改写为空气调色板项。
	airIndex := -1
	for i, block := range clone.Palette {
		if block.Name == "minecraft:air" {
			airIndex = i
			break
		}
	}
	if airIndex < 0 {
		clone.Palette = append(clone.Palette, SurfaceBlock{Name: "minecraft:air"})
		airIndex = len(clone.Palette) - 1
	}
	for i := range clone.Blocks {
		if clone.Blocks[i].State < 0 {
			clone.Blocks[i].State = airIndex
			clone.Blocks[i].NBT = nil
		}
	}
	return clone
}

func processorInputMatches(inputType, blockName, tag string, properties map[string]string, block SurfaceBlock) bool {
	switch inputType {
	case "minecraft:always_true":
		return true
	case "minecraft:tag_match":
		if state, err := registry.BlockStateID(block.Name); err == nil {
			return registry.BlockTagContains(tag, state)
		}
		return false
	case "minecraft:blockstate_match":
		if block.Name != blockName {
			return false
		}
		for key, value := range properties {
			if block.Properties[key] != value {
				return false
			}
		}
		return true
	default:
		return block.Name == blockName
	}
}

func processorMatches(blocks map[string]struct{}, name string) bool {
	if _, ok := blocks[name]; ok {
		return true
	}
	for key := range blocks {
		if strings.HasPrefix(key, "#") {
			if state, err := registry.BlockStateID(name); err == nil && registry.BlockTagContains(strings.TrimPrefix(key, "#"), state) {
				return true
			}
		}
	}
	return false
}

// parseBlockList 解析 processor 的字符串/字符串数组字段。
func parseBlockList(raw json.RawMessage) map[string]struct{} {
	result := map[string]struct{}{}
	if len(raw) == 0 {
		return result
	}
	var single string
	if json.Unmarshal(raw, &single) == nil {
		result[qualify(single)] = struct{}{}
		return result
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		for _, value := range many {
			result[qualify(value)] = struct{}{}
		}
	}
	return result
}
