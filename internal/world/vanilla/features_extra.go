//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// extraFeature 保存尚未被 features.go 主 switch 特化的 configured_feature。
// 配置在放置时解析；这些地物出现频率低于矿石/树，避免为每个 placed_feature
// 常驻保存大量可选结构。
type extraFeature struct {
	kind   string
	config json.RawMessage
}

func (f *extraFeature) hasRuntime() bool { return f != nil }

func parseExtraFeature(kind string, config json.RawMessage) (*extraFeature, bool) {
	switch kind {
	case "minecraft:simple_random_selector",
		"minecraft:random_boolean_selector",
		"minecraft:vines",
		"minecraft:glowstone_blob",
		"minecraft:netherrack_replace_blobs",
		"minecraft:basalt_columns",
		"minecraft:basalt_pillar",
		"minecraft:block_column",
		"minecraft:huge_fungus",
		"minecraft:nether_forest_vegetation",
		"minecraft:sea_pickle",
		"minecraft:underwater_magma",
		"minecraft:freeze_top_layer",
		"minecraft:forest_rock",
		"minecraft:blue_ice",
		"minecraft:ice_spike",
		"minecraft:desert_well",
		"minecraft:delta_feature",
		"minecraft:twisting_vines",
		"minecraft:weeping_vines",
		"minecraft:chorus_plant",
		"minecraft:end_island",
		"minecraft:end_platform",
		"minecraft:end_spike",
		"minecraft:end_gateway",
		"minecraft:iceberg",
		"minecraft:monster_room",
		"minecraft:large_dripstone",
		"minecraft:dripstone_cluster",
		"minecraft:pointed_dripstone",
		"minecraft:sculk_patch",
		"minecraft:root_system",
		"minecraft:void_start_platform":
		return &extraFeature{kind: kind, config: config}, true
	default:
		return nil, false
	}
}

func (r *Router) placeExtra(chunk *GeneratedChunk, random *legacyRandom, feature extraFeature, x, y, z int) {
	switch feature.kind {
	case "minecraft:simple_random_selector":
		r.placeSimpleRandomSelector(chunk, random, feature.config, x, y, z)
	case "minecraft:random_boolean_selector":
		r.placeRandomBooleanSelector(chunk, random, feature.config, x, y, z)
	case "minecraft:vines":
		r.placeVines(chunk, random, x, y, z)
	case "minecraft:glowstone_blob":
		r.placeGlowstoneBlob(chunk, random, x, y, z)
	case "minecraft:netherrack_replace_blobs":
		r.placeNetherrackBlobs(chunk, random, feature.config, x, y, z)
	case "minecraft:basalt_columns":
		r.placeBasaltColumns(chunk, random, feature.config, x, y, z)
	case "minecraft:basalt_pillar":
		r.placeBasaltPillar(chunk, random, x, y, z)
	case "minecraft:block_column":
		r.placeBlockColumn(chunk, random, feature.config, x, y, z)
	case "minecraft:huge_fungus":
		r.placeHugeFungus(chunk, random, feature.config, x, y, z)
	case "minecraft:nether_forest_vegetation":
		r.placeNetherForestVegetation(chunk, random, feature.config, x, y, z)
	case "minecraft:sea_pickle":
		r.placeSeaPickle(chunk, random, feature.config, x, y, z)
	case "minecraft:underwater_magma":
		r.placeUnderwaterMagma(chunk, random, feature.config, x, y, z)
	case "minecraft:freeze_top_layer":
		r.placeFreezeTopLayer(chunk, random, x, y, z)
	case "minecraft:forest_rock":
		r.placeForestRock(chunk, random, feature.config, x, y, z)
	case "minecraft:blue_ice":
		r.placeBlueIce(chunk, random, x, y, z)
	case "minecraft:ice_spike":
		r.placeIceSpike(chunk, random, x, y, z)
	case "minecraft:desert_well":
		r.placeDesertWell(chunk, x, y, z)
	case "minecraft:delta_feature":
		r.placeDelta(chunk, random, feature.config, x, y, z)
	case "minecraft:twisting_vines":
		r.placeTwistingVines(chunk, random, feature.config, x, y, z)
	case "minecraft:weeping_vines":
		r.placeWeepingVines(chunk, random, x, y, z)
	case "minecraft:chorus_plant":
		r.placeChorusPlant(chunk, random, x, y, z)
	case "minecraft:end_island":
		r.placeEndIsland(chunk, random, x, y, z)
	case "minecraft:end_platform":
		r.placeEndPlatform(chunk, x, y, z)
	case "minecraft:end_spike":
		r.placeEndSpike(chunk, feature.config, x, y, z)
	case "minecraft:end_gateway":
		r.placeEndGateway(chunk, random, feature.config, x, y, z)
	case "minecraft:iceberg":
		r.placeIceberg(chunk, random, feature.config, x, y, z)
	case "minecraft:monster_room":
		r.placeMonsterRoom(chunk, random, x, y, z)
	case "minecraft:large_dripstone":
		r.placeLargeDripstone(chunk, random, feature.config, x, y, z)
	case "minecraft:dripstone_cluster":
		r.placeDripstoneCluster(chunk, random, feature.config, x, y, z)
	case "minecraft:pointed_dripstone":
		r.placePointedDripstone(chunk, random, feature.config, x, y, z)
	case "minecraft:sculk_patch":
		r.placeSculkPatch(chunk, random, feature.config, x, y, z)
	case "minecraft:root_system":
		r.placeRootSystem(chunk, random, feature.config, x, y, z)
	case "minecraft:void_start_platform":
		r.placeVoidStartPlatform(chunk, x, y, z)
	}
}

func featureState(raw json.RawMessage) (SurfaceBlock, error) {
	if len(raw) == 0 {
		return SurfaceBlock{}, fmt.Errorf("缺少方块状态")
	}
	return parseFeatureSurfaceBlock(raw)
}

func featureReplaceable(block SurfaceBlock) bool {
	return block.IsAir() || block.HasFluid() || block.Name == "minecraft:short_grass" || block.Name == "minecraft:tall_grass" ||
		block.Name == "minecraft:fern" || block.Name == "minecraft:large_fern"
}

func (r *Router) placeSimpleRandomSelector(chunk *GeneratedChunk, random *legacyRandom, raw json.RawMessage, x, y, z int) {
	var encoded struct {
		Features []json.RawMessage `json:"features"`
	}
	if json.Unmarshal(raw, &encoded) != nil || len(encoded.Features) == 0 {
		return
	}
	ref, err := parseFeatureRef(encoded.Features[random.nextIntBounded(len(encoded.Features))])
	if err != nil {
		return
	}
	r.applyFeatureRef(chunk, random, ref, x, y, z)
}

func (r *Router) placeRandomBooleanSelector(chunk *GeneratedChunk, random *legacyRandom, raw json.RawMessage, x, y, z int) {
	var encoded struct {
		True  json.RawMessage `json:"feature_true"`
		False json.RawMessage `json:"feature_false"`
	}
	if json.Unmarshal(raw, &encoded) != nil {
		return
	}
	chosen := encoded.False
	if random.nextIntBounded(2) == 0 {
		chosen = encoded.True
	}
	ref, err := parseFeatureRef(chosen)
	if err != nil {
		return
	}
	r.applyFeatureRef(chunk, random, ref, x, y, z)
}

// placeVines 对应 VinesFeature：在空气方块上按方向随机选择一个可附着面。
func (r *Router) placeVines(chunk *GeneratedChunk, random *legacyRandom, x, y, z int) {
	if !chunk.InRange(x, y, z) || !chunk.BlockAt(x, y, z).IsAir() {
		return
	}
	directions := [][3]int{{0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0}}
	shuffleDirections(random, directions)
	for _, dir := range directions {
		support := chunk.BlockAt(x+dir[0], y+dir[1], z+dir[2])
		if !vineSupport(support, dir) {
			continue
		}
		properties := map[string]string{}
		switch {
		case dir[1] == 1:
			properties["up"] = "true"
		case dir[1] == -1:
			properties["down"] = "true"
		case dir[0] == 1:
			properties["east"] = "true"
		case dir[0] == -1:
			properties["west"] = "true"
		case dir[2] == 1:
			properties["south"] = "true"
		default:
			properties["north"] = "true"
		}
		chunk.SetBlock(x, y, z, SurfaceBlock{Name: "minecraft:vine", Properties: properties})
		return
	}
}

func vineSupport(block SurfaceBlock, dir [3]int) bool {
	if block.IsAir() || block.HasFluid() {
		return false
	}
	return block.Name != "minecraft:vine"
}

// placeGlowstoneBlob 对应 GlowstoneFeature：在顶部基岩/下界岩下方生成悬挂团。
func (r *Router) placeGlowstoneBlob(chunk *GeneratedChunk, random *legacyRandom, x, y, z int) {
	if !chunk.InRange(x, y, z) || !chunk.BlockAt(x, y, z).IsAir() {
		return
	}
	below := chunk.BlockAt(x, y-1, z)
	if below.Name != "minecraft:netherrack" && below.Name != "minecraft:basalt" && below.Name != "minecraft:blackstone" {
		return
	}
	chunk.SetBlock(x, y, z, SurfaceBlock{Name: "minecraft:glowstone"})
	for attempt := 0; attempt < 1500; attempt++ {
		px := x + random.nextIntBounded(17) - random.nextIntBounded(17)
		py := y - random.nextIntBounded(13)
		pz := z + random.nextIntBounded(17) - random.nextIntBounded(17)
		if !chunk.InRange(px, py, pz) || !chunk.BlockAt(px, py, pz).IsAir() {
			continue
		}
		neighbors := 0
		for _, d := range [][3]int{{1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0}, {0, 0, 1}, {0, 0, -1}} {
			if chunk.BlockAt(px+d[0], py+d[1], pz+d[2]).Name == "minecraft:glowstone" {
				neighbors++
			}
		}
		if neighbors == 1 {
			chunk.SetBlock(px, py, pz, SurfaceBlock{Name: "minecraft:glowstone"})
		}
	}
}

func (r *Router) placeNetherrackBlobs(chunk *GeneratedChunk, random *legacyRandom, raw json.RawMessage, x, y, z int) {
	var encoded struct {
		Radius json.RawMessage `json:"radius"`
		State  json.RawMessage `json:"state"`
		Target json.RawMessage `json:"target"`
	}
	if json.Unmarshal(raw, &encoded) != nil {
		return
	}
	radiusProvider, err := parseIntProvider(encoded.Radius)
	if err != nil {
		return
	}
	state, err := featureState(encoded.State)
	if err != nil {
		return
	}
	target, err := featureState(encoded.Target)
	if err != nil {
		return
	}
	radius := radiusProvider.sample(random)
	if radius <= 0 {
		return
	}
	for dx := -radius; dx <= radius; dx++ {
		for dy := -radius; dy <= radius; dy++ {
			for dz := -radius; dz <= radius; dz++ {
				if dx*dx+dy*dy+dz*dz > radius*radius || random.nextIntBounded(3) == 0 {
					continue
				}
				px, py, pz := x+dx, y+dy, z+dz
				if !chunk.InRange(px, py, pz) || chunk.BlockAt(px, py, pz).Name != target.Name {
					continue
				}
				chunk.SetBlock(px, py, pz, state)
			}
		}
	}
}

func (r *Router) placeBasaltColumns(chunk *GeneratedChunk, random *legacyRandom, raw json.RawMessage, x, y, z int) {
	var encoded struct {
		Reach  json.RawMessage `json:"reach"`
		Height json.RawMessage `json:"height"`
	}
	if json.Unmarshal(raw, &encoded) != nil {
		return
	}
	reach, err := parseIntProvider(encoded.Reach)
	if err != nil {
		return
	}
	height, err := parseIntProvider(encoded.Height)
	if err != nil {
		return
	}
	columnHeight := height.sample(random)
	if columnHeight <= 0 {
		return
	}
	radius := reach.sample(random)
	if radius < 0 {
		radius = 0
	}
	// 原版围绕源点做若干列；这里生成一个近似圆柱，并保留顶部高度随机。
	for dx := -radius; dx <= radius; dx++ {
		for dz := -radius; dz <= radius; dz++ {
			if dx*dx+dz*dz > radius*radius {
				continue
			}
			px, pz := x+dx, z+dz
			top := y
			for top+1 < chunk.MinY+chunk.Height && chunk.BlockAt(px, top+1, pz).Name == "minecraft:basalt" {
				top++
			}
			for h := 0; h < columnHeight; h++ {
				if !chunk.InRange(px, top+h, pz) {
					break
				}
				current := chunk.BlockAt(px, top+h, pz)
				if !current.IsAir() && current.Name != "minecraft:basalt" && current.Name != "minecraft:lava" {
					break
				}
				chunk.SetBlock(px, top+h, pz, SurfaceBlock{Name: "minecraft:basalt", Properties: map[string]string{"axis": "y"}})
			}
		}
	}
}

func (r *Router) placeBasaltPillar(chunk *GeneratedChunk, random *legacyRandom, x, y, z int) {
	if !chunk.InRange(x, y, z) || !chunk.BlockAt(x, y, z).IsAir() {
		return
	}
	top := y
	for top < chunk.MinY+chunk.Height && chunk.BlockAt(x, top, z).IsAir() {
		top++
	}
	if top >= chunk.MinY+chunk.Height {
		return
	}
	for cursor := y; cursor < top; cursor++ {
		chunk.SetBlock(x, cursor, z, SurfaceBlock{Name: "minecraft:basalt", Properties: map[string]string{"axis": "y"}})
	}
	for dx := -3; dx <= 3; dx++ {
		for dz := -3; dz <= 3; dz++ {
			if dx*dx+dz*dz > 9 || random.nextIntBounded(10) >= 10-dx*dx-dz*dz {
				continue
			}
			px, pz := x+dx, z+dz
			py := top - 1
			for py >= y && chunk.BlockAt(px, py, pz).IsAir() {
				py--
			}
			for h := 0; h < 3 && chunk.InRange(px, py+h, pz); h++ {
				chunk.SetBlock(px, py+h, pz, SurfaceBlock{Name: "minecraft:basalt", Properties: map[string]string{"axis": "y"}})
			}
		}
	}
}

func (r *Router) placeBlockColumn(chunk *GeneratedChunk, random *legacyRandom, raw json.RawMessage, x, y, z int) {
	var encoded struct {
		Allowed   json.RawMessage `json:"allowed_placement"`
		Direction string          `json:"direction"`
		Layers    []struct {
			Height   json.RawMessage `json:"height"`
			Provider json.RawMessage `json:"provider"`
		} `json:"layers"`
	}
	if json.Unmarshal(raw, &encoded) != nil || len(encoded.Layers) == 0 {
		return
	}
	allowed, err := parseBlockPredicate(encoded.Allowed)
	if err != nil {
		return
	}
	direction := directionVector(encoded.Direction)
	if direction == [3]int{} {
		direction = [3]int{0, -1, 0}
	}
	total := 0
	heights := make([]int, len(encoded.Layers))
	states := make([]stateProvider, len(encoded.Layers))
	for i, layer := range encoded.Layers {
		heightProvider, err := parseIntProvider(layer.Height)
		if err != nil {
			return
		}
		heights[i] = heightProvider.sample(random)
		total += heights[i]
		provider, err := parseStateProvider(layer.Provider)
		if err != nil {
			return
		}
		states[i] = provider
	}
	if total <= 0 || !chunk.InRange(x, y, z) {
		return
	}
	cursor := featurePosition{x: x, y: y, z: z}
	limit := total
	for step := 0; step < total; step++ {
		next := featurePosition{x: cursor.x + direction[0], y: cursor.y + direction[1], z: cursor.z + direction[2]}
		if !chunk.InRange(next.x, next.y, next.z) || !r.blockPredicateMatches(chunk, allowed, next.x, next.y, next.z) {
			limit = step
			break
		}
		cursor = next
	}
	placeX, placeY, placeZ := x, y, z
	for i, count := range heights {
		remaining := count
		if limit < remaining {
			remaining = limit
		}
		for j := 0; j < remaining; j++ {
			if !chunk.InRange(placeX, placeY, placeZ) {
				return
			}
			chunk.SetBlock(placeX, placeY, placeZ, states[i].sample(random))
			placeX += direction[0]
			placeY += direction[1]
			placeZ += direction[2]
		}
		limit -= remaining
		if limit <= 0 {
			break
		}
	}
}

func (r *Router) placeHugeFungus(chunk *GeneratedChunk, random *legacyRandom, raw json.RawMessage, x, y, z int) {
	var encoded struct {
		Stem      json.RawMessage `json:"stem_state"`
		Hat       json.RawMessage `json:"hat_state"`
		Decor     json.RawMessage `json:"decor_state"`
		Planted   bool            `json:"planted"`
		ValidBase json.RawMessage `json:"valid_base_block"`
	}
	if json.Unmarshal(raw, &encoded) != nil {
		return
	}
	stem, err := featureState(encoded.Stem)
	if err != nil {
		return
	}
	hat, err := featureState(encoded.Hat)
	if err != nil {
		return
	}
	decor, err := featureState(encoded.Decor)
	if err != nil {
		return
	}
	base := chunk.BlockAt(x, y-1, z)
	if encoded.ValidBase != nil {
		valid, err := featureState(encoded.ValidBase)
		if err == nil && base.Name != valid.Name {
			return
		}
	}
	height := 4 + random.nextIntBounded(10)
	if random.nextIntBounded(12) == 0 {
		height *= 2
	}
	if y+height+1 >= chunk.MinY+chunk.Height {
		return
	}
	thick := !encoded.Planted && random.nextFloat() < 0.06
	for h := 0; h < height; h++ {
		for dx := 0; dx <= 0; dx++ {
			for dz := 0; dz <= 0; dz++ {
				chunk.SetBlock(x+dx, y+h, z+dz, stem)
			}
		}
	}
	// 树冠：顶部若干层，厚度与半径随高度增加。
	top := y + height
	radius := 1
	for layer := 0; layer < 3; layer++ {
		r := radius + layer/2
		if thick && layer == 0 {
			r++
		}
		for dx := -r; dx <= r; dx++ {
			for dz := -r; dz <= r; dz++ {
				if dx*dx+dz*dz > r*r+1 {
					continue
				}
				if random.nextFloat() < 0.9 {
					chunk.SetBlock(x+dx, top+layer, z+dz, hat)
				}
			}
		}
	}
	if random.nextFloat() < 0.3 {
		chunk.SetBlock(x, top, z, decor)
	}
}

func (r *Router) placeNetherForestVegetation(chunk *GeneratedChunk, random *legacyRandom, raw json.RawMessage, x, y, z int) {
	var encoded struct {
		SpreadWidth  int             `json:"spread_width"`
		SpreadHeight int             `json:"spread_height"`
		Provider     json.RawMessage `json:"state_provider"`
	}
	if json.Unmarshal(raw, &encoded) != nil {
		return
	}
	provider, err := parseStateProvider(encoded.Provider)
	if err != nil {
		return
	}
	for attempt := 0; attempt < encoded.SpreadWidth*encoded.SpreadHeight; attempt++ {
		px := x + random.nextIntBounded(encoded.SpreadWidth*2+1) - encoded.SpreadWidth
		py := y + random.nextIntBounded(encoded.SpreadHeight*2+1) - encoded.SpreadHeight
		pz := z + random.nextIntBounded(encoded.SpreadWidth*2+1) - encoded.SpreadWidth
		if !chunk.InRange(px, py, pz) || !chunk.BlockAt(px, py, pz).IsAir() {
			continue
		}
		below := chunk.BlockAt(px, py-1, pz)
		if below.Name != "minecraft:crimson_nylium" && below.Name != "minecraft:warped_nylium" {
			continue
		}
		chunk.SetBlock(px, py, pz, provider.sample(random))
	}
}

func (r *Router) placeSeaPickle(chunk *GeneratedChunk, random *legacyRandom, raw json.RawMessage, x, y, z int) {
	var encoded struct {
		Count int `json:"count"`
	}
	if json.Unmarshal(raw, &encoded) != nil {
		return
	}
	for i := 0; i < encoded.Count; i++ {
		px := x + random.nextIntBounded(9) - random.nextIntBounded(9)
		pz := z + random.nextIntBounded(9) - random.nextIntBounded(9)
		py := -1
		for cursor := y + 1; cursor >= chunk.MinY; cursor-- {
			block := chunk.BlockAt(px, cursor, pz)
			if block.Name == "minecraft:water" {
				py = cursor
				break
			}
			if !block.IsAir() {
				break
			}
		}
		if py < 0 || !strings.HasPrefix(chunk.BlockAt(px, py-1, pz).Name, "minecraft:") {
			continue
		}
		if !chunk.BlockAt(px, py, pz).IsWater() {
			continue
		}
		chunk.SetBlock(px, py, pz, SurfaceBlock{Name: "minecraft:sea_pickle", Properties: map[string]string{"pickles": fmt.Sprintf("%d", random.nextIntBounded(4)+1), "waterlogged": "true"}})
	}
}

func (r *Router) placeUnderwaterMagma(chunk *GeneratedChunk, random *legacyRandom, raw json.RawMessage, x, y, z int) {
	var encoded struct {
		SearchRange     int     `json:"floor_search_range"`
		Probability     float32 `json:"placement_probability_per_valid_position"`
		PlacementRadius int     `json:"placement_radius_around_floor"`
	}
	if json.Unmarshal(raw, &encoded) != nil {
		return
	}
	centerY := y
	for dy := 0; dy <= encoded.SearchRange; dy++ {
		if chunk.BlockAt(x, y-dy, z).IsAir() {
			centerY = y - dy
			break
		}
	}
	radius := encoded.PlacementRadius
	if radius < 0 {
		radius = 0
	}
	for dx := -radius; dx <= radius; dx++ {
		for dz := -radius; dz <= radius; dz++ {
			if dx*dx+dz*dz > radius*radius || random.nextFloat() >= encoded.Probability {
				continue
			}
			px, pz := x+dx, z+dz
			py := centerY
			for py >= chunk.MinY && !chunk.BlockAt(px, py, pz).IsAir() {
				py--
			}
			for py >= chunk.MinY && chunk.BlockAt(px, py, pz).IsAir() {
				py--
			}
			if py < chunk.MinY || !chunk.BlockAt(px, py, pz).IsWater() {
				continue
			}
			chunk.SetBlock(px, py+1, pz, SurfaceBlock{Name: "minecraft:magma_block"})
		}
	}
}

func (r *Router) placeFreezeTopLayer(chunk *GeneratedChunk, random *legacyRandom, x, y, z int) {
	// freeze_top_layer 只影响区块最上层水/冰；遍历当前区块顶面。
	for px := chunk.MinX; px < chunk.MinX+16; px++ {
		for pz := chunk.MinZ; pz < chunk.MinZ+16; pz++ {
			for py := chunk.MinY + chunk.Height - 1; py >= chunk.MinY; py-- {
				block := chunk.BlockAt(px, py, pz)
				if block.IsAir() {
					continue
				}
				if block.Name == "minecraft:water" && chunk.BlockAt(px, py+1, pz).IsAir() {
					chunk.SetBlock(px, py, pz, SurfaceBlock{Name: "minecraft:ice"})
				}
				break
			}
		}
	}
}

func (r *Router) placeForestRock(chunk *GeneratedChunk, random *legacyRandom, raw json.RawMessage, x, y, z int) {
	var encoded struct {
		State json.RawMessage `json:"state"`
	}
	if json.Unmarshal(raw, &encoded) != nil {
		return
	}
	state, err := featureState(encoded.State)
	if err != nil {
		return
	}
	radiusX := random.nextIntBounded(3) + 3
	radiusY := random.nextIntBounded(2) + 2
	radiusZ := random.nextIntBounded(3) + 3
	for dx := -radiusX; dx <= radiusX; dx++ {
		for dy := -radiusY; dy <= radiusY; dy++ {
			for dz := -radiusZ; dz <= radiusZ; dz++ {
				nx := float64(dx) / float64(radiusX)
				ny := float64(dy) / float64(radiusY)
				nz := float64(dz) / float64(radiusZ)
				if nx*nx+ny*ny+nz*nz > 1.0 {
					continue
				}
				px, py, pz := x+dx, y+dy, z+dz
				if chunk.InRange(px, py, pz) && chunk.BlockAt(px, py, pz).IsAir() {
					chunk.SetBlock(px, py, pz, state)
				}
			}
		}
	}
}

func (r *Router) placeBlueIce(chunk *GeneratedChunk, random *legacyRandom, x, y, z int) {
	if !chunk.InRange(x, y, z) {
		return
	}
	height := random.nextIntBounded(4) + 1
	for h := 0; h < height && chunk.InRange(x, y+h, z); h++ {
		chunk.SetBlock(x, y+h, z, SurfaceBlock{Name: "minecraft:blue_ice"})
	}
}

func (r *Router) placeIceSpike(chunk *GeneratedChunk, random *legacyRandom, x, y, z int) {
	height := random.nextIntBounded(4) + 4
	for h := 0; h < height; h++ {
		radius := 1
		if h > height/2 {
			radius = 0
		}
		for dx := -radius; dx <= radius; dx++ {
			for dz := -radius; dz <= radius; dz++ {
				if !chunk.InRange(x+dx, y+h, z+dz) {
					continue
				}
				chunk.SetBlock(x+dx, y+h, z+dz, SurfaceBlock{Name: "minecraft:packed_ice"})
			}
		}
	}
}

func (r *Router) placeDesertWell(chunk *GeneratedChunk, x, y, z int) {
	y++
	for dy := 0; dy < 4; dy++ {
		for dx := -2; dx <= 2; dx++ {
			for dz := -2; dz <= 2; dz++ {
				if (dx == -2 || dx == 2 || dz == -2 || dz == 2) && dy == 0 {
					continue
				}
				if dx*dx+dz*dz > 6 && dy == 0 {
					continue
				}
				if chunk.InRange(x+dx, y+dy, z+dz) {
					chunk.SetBlock(x+dx, y+dy, z+dz, SurfaceBlock{Name: "minecraft:sandstone"})
				}
			}
		}
	}
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			if dx == 0 && dz == 0 {
				continue
			}
			if chunk.InRange(x+dx, y+1, z+dz) {
				chunk.SetBlock(x+dx, y+1, z+dz, SurfaceBlock{Name: "minecraft:sandstone"})
			}
		}
	}
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			if chunk.InRange(x+dx, y+1, z+dz) {
				chunk.SetBlock(x+dx, y+1, z+dz, SurfaceBlock{Name: "minecraft:water"})
			}
		}
	}
	if chunk.InRange(x, y+4, z) {
		chunk.SetBlock(x, y+4, z, SurfaceBlock{Name: "minecraft:sandstone"})
	}
	for _, d := range [][3]int{{-1, 3, 0}, {1, 3, 0}, {0, 3, -1}, {0, 3, 1}} {
		if chunk.InRange(x+d[0], y+d[1], z+d[2]) {
			chunk.SetBlock(x+d[0], y+d[1], z+d[2], SurfaceBlock{Name: "minecraft:sandstone"})
		}
	}
}

func (r *Router) placeDelta(chunk *GeneratedChunk, random *legacyRandom, raw json.RawMessage, x, y, z int) {
	var encoded struct {
		Contents json.RawMessage `json:"contents"`
		Rim      json.RawMessage `json:"rim"`
		Size     json.RawMessage `json:"size"`
		RimSize  json.RawMessage `json:"rim_size"`
	}
	if json.Unmarshal(raw, &encoded) != nil {
		return
	}
	contents, err := featureState(encoded.Contents)
	if err != nil {
		return
	}
	rim, err := featureState(encoded.Rim)
	if err != nil {
		return
	}
	sizeProvider, err := parseIntProvider(encoded.Size)
	if err != nil {
		return
	}
	rimProvider, err := parseIntProvider(encoded.RimSize)
	if err != nil {
		return
	}
	size := sizeProvider.sample(random)
	rimSize := rimProvider.sample(random)
	if size <= 0 {
		return
	}
	for dx := -size; dx <= size; dx++ {
		for dz := -size; dz <= size; dz++ {
			distance := math.Sqrt(float64(dx*dx + dz*dz))
			if distance > float64(size) {
				continue
			}
			px, pz := x+dx, z+dz
			py := y
			for py > chunk.MinY && (chunk.BlockAt(px, py-1, pz).IsAir() || chunk.BlockAt(px, py-1, pz).HasFluid()) {
				py--
			}
			if distance > float64(size-rimSize) {
				for py < chunk.MinY+chunk.Height && chunk.BlockAt(px, py, pz).IsAir() {
					py++
				}
				chunk.SetBlock(px, py, pz, rim)
			} else {
				for py > chunk.MinY && chunk.BlockAt(px, py-1, pz).IsAir() {
					py--
				}
				if py < chunk.MinY+chunk.Height {
					chunk.SetBlock(px, py, pz, contents)
				}
			}
		}
	}
}

func (r *Router) placeTwistingVines(chunk *GeneratedChunk, random *legacyRandom, raw json.RawMessage, x, y, z int) {
	var encoded struct {
		SpreadWidth  int `json:"spread_width"`
		SpreadHeight int `json:"spread_height"`
		MaxHeight    int `json:"max_height"`
	}
	if json.Unmarshal(raw, &encoded) != nil {
		return
	}
	for dx := -encoded.SpreadWidth; dx <= encoded.SpreadWidth; dx++ {
		for dz := -encoded.SpreadWidth; dz <= encoded.SpreadWidth; dz++ {
			for dy := -encoded.SpreadHeight; dy <= encoded.SpreadHeight; dy++ {
				px, py, pz := x+dx, y+dy, z+dz
				if !chunk.InRange(px, py, pz) || !chunk.BlockAt(px, py, pz).IsAir() {
					continue
				}
				if !chunk.BlockAt(px, py-1, pz).IsAir() && chunk.BlockAt(px, py-1, pz).Name != "minecraft:nether_wart_block" {
					continue
				}
				if random.nextIntBounded(6) != 0 {
					continue
				}
				height := random.nextIntBounded(encoded.MaxHeight) + 1
				r.placeVineColumn(chunk, random, px, py, pz, height, "minecraft:twisting_vines_plant", "minecraft:twisting_vines", 1)
			}
		}
	}
}

func (r *Router) placeWeepingVines(chunk *GeneratedChunk, random *legacyRandom, x, y, z int) {
	height := random.nextIntBounded(8) + 1
	for h := 0; h < height; h++ {
		if !chunk.InRange(x, y-h, z) || !chunk.BlockAt(x, y-h, z).IsAir() {
			break
		}
		name := "minecraft:weeping_vines_plant"
		if h == height-1 {
			name = "minecraft:weeping_vines"
		}
		chunk.SetBlock(x, y-h, z, SurfaceBlock{Name: name})
	}
}

func (r *Router) placeVineColumn(chunk *GeneratedChunk, random *legacyRandom, x, y, z, height int, plant, tip string, direction int) {
	for h := 0; h < height; h++ {
		py := y + h*direction
		if !chunk.InRange(x, py, z) || !chunk.BlockAt(x, py, z).IsAir() {
			break
		}
		name := plant
		if h == height-1 {
			name = tip
		}
		chunk.SetBlock(x, py, z, SurfaceBlock{Name: name})
	}
}

func (r *Router) placeChorusPlant(chunk *GeneratedChunk, random *legacyRandom, x, y, z int) {
	if !strings.HasPrefix(chunk.BlockAt(x, y-1, z).Name, "minecraft:") {
		return
	}
	height := random.nextIntBounded(8) + 1
	for h := 0; h < height; h++ {
		if !chunk.InRange(x, y+h, z) {
			break
		}
		chunk.SetBlock(x, y+h, z, SurfaceBlock{Name: "minecraft:chorus_plant"})
	}
	top := y + height
	radius := random.nextIntBounded(3)
	for dx := -radius; dx <= radius; dx++ {
		for dz := -radius; dz <= radius; dz++ {
			if dx*dx+dz*dz > radius*radius {
				continue
			}
			if chunk.InRange(x+dx, top, z+dz) && chunk.BlockAt(x+dx, top, z+dz).IsAir() {
				chunk.SetBlock(x+dx, top, z+dz, SurfaceBlock{Name: "minecraft:chorus_plant"})
			}
		}
	}
	if chunk.InRange(x, top+1, z) && chunk.BlockAt(x, top+1, z).IsAir() {
		chunk.SetBlock(x, top+1, z, SurfaceBlock{Name: "minecraft:chorus_flower"})
	}
}

func (r *Router) placeEndIsland(chunk *GeneratedChunk, random *legacyRandom, x, y, z int) {
	if y < 1 {
		return
	}
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			if dx*dx+dz*dz > 4 {
				continue
			}
			if chunk.InRange(x+dx, y, z+dz) {
				chunk.SetBlock(x+dx, y, z+dz, SurfaceBlock{Name: "minecraft:end_stone"})
			}
		}
	}
}

func (r *Router) placeEndPlatform(chunk *GeneratedChunk, x, y, z int) {
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			if chunk.InRange(x+dx, y, z+dz) {
				chunk.SetBlock(x+dx, y, z+dz, SurfaceBlock{Name: "minecraft:obsidian"})
				if chunk.InRange(x+dx, y+1, z+dz) {
					chunk.SetBlock(x+dx, y+1, z+dz, SurfaceBlock{Name: "minecraft:air"})
				}
			}
		}
	}
}

func (r *Router) placeEndSpike(chunk *GeneratedChunk, raw json.RawMessage, x, y, z int) {
	// 末地黑曜石柱由 EndSpikeFeature 在出生点固定位置生成；这里使用保底形状，
	// 精确高度/笼子需要结构 start 数据。
	height := 10
	for h := 0; h < height; h++ {
		if chunk.InRange(x, y+h, z) {
			chunk.SetBlock(x, y+h, z, SurfaceBlock{Name: "minecraft:obsidian"})
		}
	}
	_ = raw
}

func (r *Router) placeEndGateway(chunk *GeneratedChunk, random *legacyRandom, raw json.RawMessage, x, y, z int) {
	if !chunk.InRange(x, y, z) {
		return
	}
	chunk.SetBlock(x, y, z, SurfaceBlock{Name: "minecraft:end_gateway"})
	entity := GeneratedBlockEntity{Type: "minecraft:end_gateway"}
	var encoded struct {
		Exact bool  `json:"exact"`
		Exit  []int `json:"exit"`
	}
	if json.Unmarshal(raw, &encoded) == nil {
		entity.Data = nbtCompound{"exact": encoded.Exact}
		if len(encoded.Exit) == 3 {
			entity.Data["exit"] = []any{int32(encoded.Exit[0]), int32(encoded.Exit[1]), int32(encoded.Exit[2])}
		}
	}
	chunk.SetBlockEntity(x, y, z, entity)
	_ = random
}

func (r *Router) placeIceberg(chunk *GeneratedChunk, random *legacyRandom, raw json.RawMessage, x, y, z int) {
	var encoded struct {
		State json.RawMessage `json:"state"`
	}
	if json.Unmarshal(raw, &encoded) != nil {
		return
	}
	state, err := featureState(encoded.State)
	if err != nil {
		return
	}
	// IcebergFeature.place 的主要流程：上半冰体、下半冰锥和随机尖顶。
	snow := random.nextDouble() > 0.7
	angle := random.nextDouble() * 2 * math.Pi
	radiusLimit := 11 - random.nextIntBounded(5)
	p8 := 3 + random.nextIntBounded(3)
	big := random.nextDouble() > 0.7
	height := 11
	if big {
		height = random.nextIntBounded(6) + 6
	} else {
		height = random.nextIntBounded(15) + 3
		if random.nextDouble() > 0.9 {
			height += random.nextIntBounded(19) + 7
		}
	}
	lowerHeight := minInt(height+random.nextIntBounded(11), 18)
	shape := minInt(height+random.nextIntBounded(7)-random.nextIntBounded(5), 11)
	radius := 11
	if big {
		radius = radiusLimit
	}
	for dx := -radius; dx < radius; dx++ {
		for dz := -radius; dz < radius; dz++ {
			for dy := 0; dy < height; dy++ {
				var limit int
				if big {
					limit = icebergBigLimit(dy, height, shape)
				} else {
					limit = icebergShapeLimit(random, dy, height, shape)
				}
				if !big && dx >= limit {
					continue
				}
				icebergPlaceCell(chunk, random, state, x, y, z, dx, dy, dz, limit, radius, big, snow, angle)
			}
		}
	}
	for dx := -radius; dx < radius; dx++ {
		for dz := -radius; dz < radius; dz++ {
			for dy := -1; dy > -lowerHeight; dy-- {
				limit := radius
				if big {
					v := float64(radius) * (1 - float64(dy*dy)/float64(maxInt(lowerHeight, 1)*8))
					limit = int(math.Floor(v))
				}
				layer := icebergLowerLimit(random, -dy, lowerHeight, shape)
				if dx >= layer {
					continue
				}
				icebergPlaceCell(chunk, random, state, x, y, z, dx, dy, dz, limit, radius, big, snow, angle)
			}
		}
	}
	// 原版还会在部分冰体上生成尖顶；保留一个确定性的小尖顶。
	if random.nextDouble() > 0.7 {
		spike := random.nextIntBounded(6) + 2
		for dy := 1; dy <= spike; dy++ {
			if chunk.InRange(x, y+height+dy, z) {
				chunk.SetBlock(x, y+height+dy, z, state)
			}
		}
	}
	_ = p8
}

func icebergShapeLimit(random *legacyRandom, layer, height, shape int) int {
	factor := float32(3.5) - random.nextFloat()
	value := (1 - float32(layer*layer)/(float32(height)*factor)) * float32(shape)
	if height > 15+random.nextIntBounded(5) {
		adjusted := layer
		if layer < 3+random.nextIntBounded(6) {
			adjusted = layer / 2
		}
		value = (1 - float32(adjusted)/(float32(height)*factor*0.4)) * float32(shape)
	}
	return int(math.Ceil(float64(value / 2)))
}

func icebergBigLimit(layer, height, shape int) int {
	value := (1 - float32(layer*layer)/float32(maxInt(height, 1))) * float32(shape)
	return int(math.Ceil(float64(value / 2)))
}

func icebergLowerLimit(random *legacyRandom, layer, height, shape int) int {
	factor := float32(1) + random.nextFloat()/2
	value := (1 - float32(layer)/(float32(maxInt(height, 1))*factor)) * float32(shape)
	return int(math.Ceil(float64(value / 2)))
}

func icebergPlaceCell(chunk *GeneratedChunk, random *legacyRandom, state SurfaceBlock, centerX, centerY, centerZ, dx, dy, dz, limit, radius int, big, snow bool, angle float64) {
	x, y, z := centerX+dx, centerY+dy, centerZ+dz
	if !chunk.InRange(x, y, z) {
		return
	}
	var distance float64
	if big {
		if limit <= 0 {
			return
		}
		sin, cos := math.Sin(angle), math.Cos(angle)
		fx, fz := float64(dx), float64(dz)
		a := (fx*cos - fz*sin) / float64(maxInt(radius, 1))
		b := (fx*sin + fz*cos) / float64(limit)
		distance = a*a + b*b - 1
	} else {
		// IcebergFeature.a(x,z,center,radius,random)
		density := 10 * clampFloat(float64(0.2+random.nextFloat()*0.6), 0.2, 0.8) / float64(maxInt(radius, 1))
		distance = density + float64(dx*dx+dz*dz) - float64(radius*radius)
	}
	if distance >= 0 {
		return
	}
	if big && distance > -0.5 && random.nextDouble() > 0.9 {
		return
	}
	current := chunk.BlockAt(x, y, z)
	if !(current.IsAir() || current.HasFluid() || current.Name == "minecraft:ice" || current.Name == "minecraft:snow_block" || current.Name == "minecraft:packed_ice") {
		return
	}
	chunk.SetBlock(x, y, z, state)
	if snow && y > centerY && random.nextDouble() > 0.8 {
		chunk.SetBlock(x, y+1, z, SurfaceBlock{Name: "minecraft:snow_block"})
	}
}

func (r *Router) placeMonsterRoom(chunk *GeneratedChunk, random *legacyRandom, x, y, z int) {
	widthX := random.nextIntBounded(2) + 2
	widthZ := random.nextIntBounded(2) + 2
	minX, maxX := -widthX-1, widthX+1
	minZ, maxZ := -widthZ-1, widthZ+1
	openings := 0
	for dx := minX; dx <= maxX; dx++ {
		for dy := -1; dy <= 4; dy++ {
			for dz := minZ; dz <= maxZ; dz++ {
				px, py, pz := x+dx, y+dy, z+dz
				solid := isSolidBlock(chunk.BlockAt(px, py, pz))
				if (dy == -1 || dy == 4) && !solid {
					return
				}
				if (dx == minX || dx == maxX || dz == minZ || dz == maxZ) && dy == 0 &&
					chunk.BlockAt(px, py, pz).IsAir() && chunk.BlockAt(px, py+1, pz).IsAir() {
					openings++
				}
			}
		}
	}
	if openings < 1 || openings > 5 {
		return
	}
	for dx := minX; dx <= maxX; dx++ {
		for dy := 3; dy >= -1; dy-- {
			for dz := minZ; dz <= maxZ; dz++ {
				px, py, pz := x+dx, y+dy, z+dz
				current := chunk.BlockAt(px, py, pz)
				boundary := dx == minX || dy == -1 || dz == minZ || dx == maxX || dy == 4 || dz == maxZ
				if boundary {
					if py >= chunk.MinY && !isSolidBlock(chunk.BlockAt(px, py-1, pz)) {
						chunk.SetBlock(px, py, pz, SurfaceBlock{Name: "minecraft:cobblestone"})
						continue
					}
					if current.IsAir() || current.Name == "minecraft:chest" {
						continue
					}
					if dy == -1 && random.nextIntBounded(4) != 0 {
						chunk.SetBlock(px, py, pz, SurfaceBlock{Name: "minecraft:mossy_cobblestone"})
					} else {
						chunk.SetBlock(px, py, pz, SurfaceBlock{Name: "minecraft:cobblestone"})
					}
					continue
				}
				if current.Name == "minecraft:chest" || current.Name == "minecraft:spawner" {
					continue
				}
				chunk.SetBlock(px, py, pz, SurfaceBlock{Name: "minecraft:cave_air"})
			}
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		for slot := 0; slot < 3; slot++ {
			px := x + random.nextIntBounded(widthX*2+1) - widthX
			pz := z + random.nextIntBounded(widthZ*2+1) - widthZ
			py := y
			if !chunk.BlockAt(px, py, pz).IsAir() {
				continue
			}
			solid := 0
			for _, d := range [][3]int{{1, 0, 0}, {-1, 0, 0}, {0, 0, 1}, {0, 0, -1}} {
				if isSolidBlock(chunk.BlockAt(px+d[0], py+d[1], pz+d[2])) {
					solid++
				}
			}
			if solid == 1 {
				chunk.SetBlock(px, py, pz, SurfaceBlock{Name: "minecraft:chest"})
				break
			}
		}
	}
	chunk.SetBlock(x, y, z, SurfaceBlock{Name: "minecraft:spawner"})
}

func (r *Router) placeLargeDripstone(chunk *GeneratedChunk, random *legacyRandom, raw json.RawMessage, x, y, z int) {
	// 大滴水石锥：当前生成一个中心石柱和上下锥体；完整 wind/bluntness 未移植。
	height := random.nextIntBounded(10) + 4
	for h := 0; h < height; h++ {
		if chunk.InRange(x, y+h, z) {
			chunk.SetBlock(x, y+h, z, SurfaceBlock{Name: "minecraft:dripstone_block"})
		}
	}
}

func (r *Router) placeDripstoneCluster(chunk *GeneratedChunk, random *legacyRandom, raw json.RawMessage, x, y, z int) {
	radius := random.nextIntBounded(4) + 2
	for dx := -radius; dx <= radius; dx++ {
		for dz := -radius; dz <= radius; dz++ {
			if dx*dx+dz*dz > radius*radius {
				continue
			}
			height := random.nextIntBounded(4) + 1
			for h := 0; h < height; h++ {
				if chunk.InRange(x+dx, y+h, z+dz) {
					chunk.SetBlock(x+dx, y+h, z+dz, SurfaceBlock{Name: "minecraft:dripstone_block"})
				}
			}
		}
	}
}

func (r *Router) placePointedDripstone(chunk *GeneratedChunk, random *legacyRandom, raw json.RawMessage, x, y, z int) {
	// 单点滴水石：上方为石/滴水石时向下，否则向上。
	direction := -1
	if !chunk.BlockAt(x, y+1, z).IsAir() && chunk.BlockAt(x, y+1, z).Name == "minecraft:dripstone_block" {
		direction = 1
	}
	height := random.nextIntBounded(3) + 1
	for h := 0; h < height; h++ {
		py := y + h*direction
		if !chunk.InRange(x, py, z) || !chunk.BlockAt(x, py, z).IsAir() {
			break
		}
		properties := map[string]string{"thickness": "tip", "vertical_direction": "down", "waterlogged": "false"}
		if direction > 0 {
			properties["vertical_direction"] = "up"
		}
		if h < height-1 {
			properties["thickness"] = "base"
		}
		chunk.SetBlock(x, py, z, SurfaceBlock{Name: "minecraft:pointed_dripstone", Properties: properties})
	}
}

func (r *Router) placeVoidStartPlatform(chunk *GeneratedChunk, x, y, z int) {
	centerX := chunk.ChunkX*16 + 8
	centerZ := chunk.ChunkZ*16 + 8
	centerY := y + 3
	for px := chunk.MinX; px < chunk.MinX+16; px++ {
		for pz := chunk.MinZ; pz < chunk.MinZ+16; pz++ {
			if maxInt(absFeatureInt(centerX-px), absFeatureInt(centerZ-pz)) > 16 {
				continue
			}
			state := SurfaceBlock{Name: "minecraft:cobblestone"}
			if px == centerX && pz == centerZ && centerY >= chunk.MinY && centerY < chunk.MinY+chunk.Height {
				state.Name = "minecraft:stone"
			}
			if chunk.InRange(px, centerY, pz) {
				chunk.SetBlock(px, centerY, pz, state)
			}
		}
	}
	_ = x
	_ = z
}

func (r *Router) placeSimpleBlockColumn(chunk *GeneratedChunk, random *legacyRandom, raw json.RawMessage, x, y, z int) {
	r.placeBlockColumn(chunk, random, raw, x, y, z)
}

func shuffleDirections(random *legacyRandom, directions [][3]int) {
	for i := len(directions) - 1; i > 0; i-- {
		j := random.nextIntBounded(i + 1)
		directions[i], directions[j] = directions[j], directions[i]
	}
}
