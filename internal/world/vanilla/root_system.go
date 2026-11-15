//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"encoding/json"
	"fmt"

	"gmcs/internal/registry"
)

type rootSystemConfiguration struct {
	rootRadius       int
	rootAttempts     int
	rootColumnHeight int
	rootState        SurfaceBlock
	rootReplaceable  string
	hangingRadius    int
	hangingAttempts  int
	hangingSpan      int
	hangingState     SurfaceBlock
	allowedTree      json.RawMessage
	requiredSpace    int
	allowedWater     int
}

func parseRootSystemConfiguration(raw json.RawMessage) (*rootSystemConfiguration, error) {
	var encoded struct {
		RootRadius       int             `json:"root_radius"`
		RootAttempts     int             `json:"root_placement_attempts"`
		RootColumnHeight int             `json:"root_column_max_height"`
		RootState        json.RawMessage `json:"root_state_provider"`
		RootReplaceable  string          `json:"root_replaceable"`
		HangingRadius    int             `json:"hanging_root_radius"`
		HangingAttempts  int             `json:"hanging_root_placement_attempts"`
		HangingSpan      int             `json:"hanging_roots_vertical_span"`
		HangingState     json.RawMessage `json:"hanging_root_state_provider"`
		AllowedTree      json.RawMessage `json:"allowed_tree_position"`
		RequiredSpace    int             `json:"required_vertical_space_for_tree"`
		AllowedWater     int             `json:"allowed_vertical_water_for_tree"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, err
	}
	rootState, err := parseRootStateProvider(encoded.RootState)
	if err != nil {
		return nil, err
	}
	hanging, err := parseRootStateProvider(encoded.HangingState)
	if err != nil {
		return nil, err
	}
	if encoded.RootRadius <= 0 {
		encoded.RootRadius = 3
	}
	if encoded.RootAttempts <= 0 {
		encoded.RootAttempts = 20
	}
	if encoded.RootColumnHeight <= 0 {
		encoded.RootColumnHeight = 100
	}
	if encoded.HangingRadius <= 0 {
		encoded.HangingRadius = 3
	}
	if encoded.HangingAttempts <= 0 {
		encoded.HangingAttempts = 20
	}
	if encoded.HangingSpan <= 0 {
		encoded.HangingSpan = 2
	}
	return &rootSystemConfiguration{
		rootRadius:       encoded.RootRadius,
		rootAttempts:     encoded.RootAttempts,
		rootColumnHeight: encoded.RootColumnHeight,
		rootState:        rootState,
		rootReplaceable:  encoded.RootReplaceable,
		hangingRadius:    encoded.HangingRadius,
		hangingAttempts:  encoded.HangingAttempts,
		hangingSpan:      encoded.HangingSpan,
		hangingState:     hanging,
		allowedTree:      encoded.AllowedTree,
		requiredSpace:    encoded.RequiredSpace,
		allowedWater:     encoded.AllowedWater,
	}, nil
}

func parseRootStateProvider(raw json.RawMessage) (SurfaceBlock, error) {
	var encoded struct {
		State json.RawMessage `json:"state"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return SurfaceBlock{}, err
	}
	if len(encoded.State) == 0 {
		return SurfaceBlock{}, fmt.Errorf("根状态 provider 缺少 state")
	}
	return parseFeatureSurfaceBlock(encoded.State)
}

func (r *Router) placeRootSystem(chunk *GeneratedChunk, random *legacyRandom, raw json.RawMessage, x, y, z int) {
	configuration, err := parseRootSystemConfiguration(raw)
	if err != nil {
		return
	}
	replaceable := func(block SurfaceBlock) bool {
		if configuration.rootReplaceable == "" {
			return block.IsAir()
		}
		state, err := registry.BlockStateID(block.Name)
		return err == nil && registry.BlockTagContains(trimTag(configuration.rootReplaceable), state)
	}
	// 根系在树周围铺一圈可替换方块。
	for i := 0; i < configuration.rootAttempts; i++ {
		dx := random.nextIntBounded(configuration.rootRadius*2+1) - configuration.rootRadius
		dz := random.nextIntBounded(configuration.rootRadius*2+1) - configuration.rootRadius
		if dx*dx+dz*dz > configuration.rootRadius*configuration.rootRadius {
			continue
		}
		px, pz := x+dx, z+dz
		if !chunk.InRange(px, y, pz) {
			continue
		}
		startY := y
		if chunk.BlockAt(px, startY, pz).IsAir() {
			startY--
		}
		if chunk.InRange(px, startY, pz) && replaceable(chunk.BlockAt(px, startY, pz)) {
			chunk.SetBlock(px, startY, pz, configuration.rootState)
		}
		// 根系向下的柱状根须：只在可替换方块中延伸。
		height := random.nextIntBounded(configuration.rootColumnHeight/8+1) + 1
		for step := 1; step <= height; step++ {
			py := startY - step
			if !chunk.InRange(px, py, pz) || !replaceable(chunk.BlockAt(px, py, pz)) {
				break
			}
			chunk.SetBlock(px, py, pz, configuration.rootState)
		}
	}
	// 悬挂根：在半径内随机寻找空气柱，向下生成 hanging_roots。
	for i := 0; i < configuration.hangingAttempts; i++ {
		dx := random.nextIntBounded(configuration.hangingRadius*2+1) - configuration.hangingRadius
		dz := random.nextIntBounded(configuration.hangingRadius*2+1) - configuration.hangingRadius
		px, pz := x+dx, z+dz
		if !chunk.InRange(px, y, pz) {
			continue
		}
		top := y
		for py := y; py > y-configuration.hangingSpan && py > chunk.MinY; py-- {
			if chunk.BlockAt(px, py, pz).IsAir() {
				top = py
				break
			}
		}
		for py := top; py > top-configuration.hangingSpan && py > chunk.MinY; py-- {
			if !chunk.BlockAt(px, py, pz).IsAir() {
				break
			}
			chunk.SetBlock(px, py, pz, configuration.hangingState)
		}
	}
}

func trimTag(tag string) string {
	if len(tag) > 0 && tag[0] == '#' {
		return tag[1:]
	}
	return tag
}
