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
	Block       string
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
			Rules     []struct {
				InputPredicate struct {
					Block       string  `json:"block"`
					Probability float32 `json:"probability"`
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
		case "minecraft:rule":
			for _, rule := range processor.Rules {
				if rule.OutputState == nil {
					continue
				}
				output, err := parseFeatureSurfaceBlock(rule.OutputState)
				if err != nil {
					continue
				}
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
					if clone.Palette[clone.Blocks[i].State].Name == qualify(rule.InputPredicate.Block) &&
						(rule.InputPredicate.Probability <= 0 || random.nextFloat() < rule.InputPredicate.Probability) {
						clone.Blocks[i].State = stateIndex
					}
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
