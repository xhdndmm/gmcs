//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// structureStartStore 把已经生成的结构 start 持久化到磁盘，避免服务器重启后
// 对同一结构重新展开出不同 pieces。原版把 StructureStart 存在区块 NBT 里；
// gmcs 的区块负载尚未迁移到 Anvil，因此先用独立目录保存，并保证同一
// (seed, set, origin) 的展开结果可复用。
//
// 这不是 Anvil 兼容格式，也不替代结构引用/区块状态；它只解决 gmcs 自身
// 重启后结构展开确定性的问题。
type structureStartStore struct {
	mu  sync.Mutex
	dir string
}

// diskStructureStart 是 structureStart 的可持久化表示。
type diskStructureStart struct {
	Pieces []diskStructurePiece `json:"pieces"`
}

type diskStructurePiece struct {
	Name              string               `json:"name"`
	Template          string               `json:"template,omitempty"`
	Origin            [3]int               `json:"origin"`
	Rotation          int                  `json:"rotation"`
	Mirror            bool                 `json:"mirror,omitempty"`
	Materialized      bool                 `json:"materialized,omitempty"`
	TerrainAdaptation string               `json:"terrain_adaptation,omitempty"`
	Size              [3]int               `json:"size,omitempty"`
	Blocks            []diskStructureBlock `json:"blocks,omitempty"`
}

type diskStructureBlock struct {
	Pos    [3]int                `json:"pos"`
	State  SurfaceBlock          `json:"state"`
	Entity *GeneratedBlockEntity `json:"entity,omitempty"`
}

func (s *structureStartStore) path(key structureStartKey) string {
	if s == nil || s.dir == "" {
		return ""
	}
	return filepath.Join(s.dir,
		fmt.Sprintf("%d_%d_%d_%d.json", key.seed, key.set, key.originX, key.originZ))
}

func (s *structureStartStore) load(key structureStartKey, runtime *structureRuntime) (*structureStart, bool) {
	if s == nil || s.dir == "" {
		return nil, false
	}
	s.mu.Lock()
	path := s.path(key)
	s.mu.Unlock()
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var disk diskStructureStart
	if err := json.Unmarshal(raw, &disk); err != nil {
		return nil, false
	}
	start := &structureStart{pieces: make([]structurePiece, 0, len(disk.Pieces))}
	for _, item := range disk.Pieces {
		piece := structurePiece{
			name:              item.Name,
			origin:            item.Origin,
			rotation:          templateRotation(item.Rotation),
			mirror:            item.Mirror,
			terrainAdaptation: item.TerrainAdaptation,
			size:              item.Size,
		}
		if item.Template != "" && !item.Materialized {
			template, err := runtime.data.ParsedTemplate(item.Template)
			if err != nil {
				return nil, false
			}
			piece.template = template
			piece.templateName = item.Template
		} else {
			piece.blocks = make([]structureBlockPlacement, 0, len(item.Blocks))
			for _, block := range item.Blocks {
				piece.blocks = append(piece.blocks, structureBlockPlacement{
					pos: block.Pos, state: block.State, entity: block.Entity,
				})
			}
			piece.size = item.Size
		}
		start.pieces = append(start.pieces, piece)
	}
	return start, true
}

func (s *structureStartStore) save(key structureStartKey, start *structureStart) {
	if s == nil || s.dir == "" || start == nil {
		return
	}
	disk := diskStructureStart{Pieces: make([]diskStructurePiece, 0, len(start.pieces))}
	for _, piece := range start.pieces {
		item := diskStructurePiece{
			Name:              piece.name,
			Origin:            piece.origin,
			Rotation:          int(piece.rotation),
			Mirror:            piece.mirror,
			TerrainAdaptation: piece.terrainAdaptation,
			Size:              piece.size,
			Template:          piece.templateName,
		}
		blocks := piece.blocks
		if piece.template != nil && piece.processors != "" {
			// processor_list 结果依赖随机流；直接保存处理后的方块，避免重启后
			// 用不同随机状态重新处理模板导致结构与首次生成不同。
			item.Materialized = true
			blocks = materializeTemplatePiece(piece)
		}
		for _, block := range blocks {
			var entity *GeneratedBlockEntity
			if block.entity != nil {
				copy := *block.entity
				entity = &copy
			}
			item.Blocks = append(item.Blocks, diskStructureBlock{Pos: block.pos, State: block.state, Entity: entity})
		}
		disk.Pieces = append(disk.Pieces, item)
	}
	raw, err := json.Marshal(disk)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return
	}
	path := s.path(key)
	temp := path + ".tmp"
	if err := os.WriteFile(temp, raw, 0o644); err != nil {
		return
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
	}
}

// materializeTemplatePiece 把已经应用 processor 的模板展开成绝对坐标方块，
// 用于持久化依赖随机流的 processor 结果。
func materializeTemplatePiece(piece structurePiece) []structureBlockPlacement {
	if piece.template == nil {
		return piece.blocks
	}
	result := make([]structureBlockPlacement, 0, len(piece.template.Blocks))
	random := newLegacyRandom(int64(piece.origin[0])*31 + int64(piece.origin[1])*17 + int64(piece.origin[2])*13)
	for _, block := range piece.template.Blocks {
		if block.State < 0 || block.State >= len(piece.template.Palette) {
			continue
		}
		state := piece.template.Palette[block.State]
		if state.Name == "minecraft:jigsaw" {
			if final, ok := nbtStringValue(block.NBT["final_state"]); ok {
				if parsed, err := parseSimpleBlockState(final); err == nil {
					state = parsed
				} else {
					continue
				}
			} else {
				continue
			}
		}
		if state.Name == "minecraft:structure_void" || state.Name == "minecraft:structure_block" {
			continue
		}
		pos := transformTemplatePos(block.Pos, piece.template.Size, piece.rotation, piece.mirror)
		placement := structureBlockPlacement{
			pos:   [3]int{piece.origin[0] + pos[0], piece.origin[1] + pos[1], piece.origin[2] + pos[2]},
			state: state,
		}
		if entity, ok := generatedBlockEntityFromNBT(state.Name, block.NBT); ok {
			entity.Type = blockEntityTypeName(entity.Type)
			initLootBlockEntity(&entity, random)
			placement.entity = &entity
		}
		result = append(result, placement)
	}
	return result
}
