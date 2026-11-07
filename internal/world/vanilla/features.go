//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"encoding/json"
	"fmt"
	"math"

	"gmcs/internal/registry"
)

// featureRegistry 是 placed_feature / configured_feature 的只读解析结果。
// 当前运行时只执行 minecraft:ore / minecraft:scattered_ore；其余地物在解析时
// 保留类型但不会被 GenerateChunk 执行（见 docs/TODO.md 的阶段 4c）。
type featureRegistry struct {
	placed        map[string]placedFeature
	biomeFeatures map[string][][]string
}

type placedFeature struct {
	feature    string
	kind       string
	ore        *oreConfiguration
	placements []placementModifier
}

type placementModifier struct {
	kind   string
	count  intProvider
	chance int
	height featureHeightProvider
}

type intProvider struct {
	kind     string
	constant int
	minimum  int
	maximum  int
}

func (p intProvider) sample(random *legacyRandom) int {
	switch p.kind {
	case "constant":
		return p.constant
	case "minecraft:uniform":
		return p.minimum + random.nextIntBounded(p.maximum-p.minimum+1)
	default:
		return p.constant
	}
}

type featureHeightProvider struct {
	kind    string
	minimum heightAnchor
	maximum heightAnchor
	plateau int
}

func (p featureHeightProvider) sample(random *legacyRandom, setting NoiseSetting) int {
	minimum := p.minimum.resolve(setting)
	maximum := p.maximum.resolve(setting)
	if maximum < minimum {
		return minimum
	}
	switch p.kind {
	case "minecraft:constant":
		return minimum
	case "minecraft:trapezoid":
		span := maximum - minimum
		if p.plateau >= span {
			return minimum + random.nextIntBounded(span+1)
		}
		left := (span - p.plateau) / 2
		right := span - left
		return minimum + random.nextIntBounded(right+1) + random.nextIntBounded(left+1)
	default: // uniform
		return minimum + random.nextIntBounded(maximum-minimum+1)
	}
}

type oreConfiguration struct {
	size    int
	discard float32
	targets []oreTarget
}

type oreTarget struct {
	state SurfaceBlock
	tag   string
	block string
}

// parseFeatureRegistry 解析地物定义。未知/暂不支持的类型不会报错，
// 只是不会被执行；数据缺失则会返回错误。
func parseFeatureRegistry(data *Data) (*featureRegistry, error) {
	registry := &featureRegistry{
		placed:        make(map[string]placedFeature, len(data.PlacedFeatures)),
		biomeFeatures: make(map[string][][]string, len(data.BiomeFeatures)),
	}
	for name, raw := range data.PlacedFeatures {
		var encoded struct {
			Feature   string            `json:"feature"`
			Placement []json.RawMessage `json:"placement"`
		}
		if err := json.Unmarshal(raw, &encoded); err != nil {
			return nil, fmt.Errorf("解析 placed_feature %s 失败：%w", name, err)
		}
		placed := placedFeature{feature: qualify(encoded.Feature)}
		configuredRaw, ok := data.ConfiguredFeatures[placed.feature]
		if !ok {
			return nil, fmt.Errorf("placed_feature %s 引用了不存在的 configured_feature %s", name, placed.feature)
		}
		var configured struct {
			Type   string          `json:"type"`
			Config json.RawMessage `json:"config"`
		}
		if err := json.Unmarshal(configuredRaw, &configured); err != nil {
			return nil, fmt.Errorf("解析 configured_feature %s 失败：%w", placed.feature, err)
		}
		placed.kind = qualify(configured.Type)
		if placed.kind == "minecraft:ore" || placed.kind == "minecraft:scattered_ore" {
			ore, err := parseOreConfiguration(configured.Config)
			if err != nil {
				return nil, fmt.Errorf("解析 %s 的矿石配置失败：%w", placed.feature, err)
			}
			placed.ore = ore
			for index, rawModifier := range encoded.Placement {
				modifier, err := parsePlacementModifier(rawModifier)
				if err != nil {
					return nil, fmt.Errorf("解析 %s 的第 %d 个 placement 失败：%w", name, index, err)
				}
				placed.placements = append(placed.placements, modifier)
			}
		}
		registry.placed[qualify(name)] = placed
	}
	for biome, raw := range data.BiomeFeatures {
		var steps [][]string
		if err := json.Unmarshal(raw, &steps); err != nil {
			return nil, fmt.Errorf("解析群系 %s 的地物列表失败：%w", biome, err)
		}
		registry.biomeFeatures[qualify(biome)] = steps
	}
	return registry, nil
}

func parseOreConfiguration(raw json.RawMessage) (*oreConfiguration, error) {
	var encoded struct {
		Size    int     `json:"size"`
		Discard float32 `json:"discard_chance_on_air_exposure"`
		Targets []struct {
			State  json.RawMessage `json:"state"`
			Target struct {
				PredicateType string `json:"predicate_type"`
				Tag           string `json:"tag"`
				Block         string `json:"block"`
			} `json:"target"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, err
	}
	configuration := &oreConfiguration{size: encoded.Size, discard: encoded.Discard}
	for _, target := range encoded.Targets {
		state, err := parseFeatureSurfaceBlock(target.State)
		if err != nil {
			return nil, err
		}
		entry := oreTarget{state: state}
		switch target.Target.PredicateType {
		case "minecraft:tag_match":
			entry.tag = target.Target.Tag
		case "minecraft:block_match":
			entry.block = target.Target.Block
		default:
			return nil, fmt.Errorf("不支持的 ore target 谓词 %q", target.Target.PredicateType)
		}
		configuration.targets = append(configuration.targets, entry)
	}
	if configuration.size <= 0 || len(configuration.targets) == 0 {
		return nil, fmt.Errorf("矿石配置缺少 size 或 targets")
	}
	return configuration, nil
}

func parseFeatureSurfaceBlock(raw json.RawMessage) (SurfaceBlock, error) {
	var block SurfaceBlock
	if err := json.Unmarshal(raw, &block); err != nil {
		return SurfaceBlock{}, err
	}
	block.Name = qualify(block.Name)
	return block, nil
}

func parsePlacementModifier(raw json.RawMessage) (placementModifier, error) {
	var encoded struct {
		Type   string          `json:"type"`
		Count  json.RawMessage `json:"count"`
		Chance int             `json:"chance"`
		Height json.RawMessage `json:"height"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return placementModifier{}, err
	}
	modifier := placementModifier{kind: qualify(encoded.Type)}
	switch modifier.kind {
	case "minecraft:count":
		count, err := parseIntProvider(encoded.Count)
		if err != nil {
			return placementModifier{}, err
		}
		modifier.count = count
	case "minecraft:rarity_filter":
		modifier.chance = encoded.Chance
	case "minecraft:height_range":
		height, err := parseFeatureHeightProvider(encoded.Height)
		if err != nil {
			return placementModifier{}, err
		}
		modifier.height = height
	case "minecraft:in_square", "minecraft:biome":
		// 无参数。
	default:
		return placementModifier{}, fmt.Errorf("不支持的 placement modifier %q", modifier.kind)
	}
	return modifier, nil
}

func parseIntProvider(raw json.RawMessage) (intProvider, error) {
	var constant int
	if err := json.Unmarshal(raw, &constant); err == nil {
		return intProvider{kind: "constant", constant: constant}, nil
	}
	var encoded struct {
		Type    string `json:"type"`
		Minimum int    `json:"min_inclusive"`
		Maximum int    `json:"max_inclusive"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return intProvider{}, err
	}
	if qualify(encoded.Type) != "minecraft:uniform" {
		return intProvider{}, fmt.Errorf("不支持的 int provider %q", encoded.Type)
	}
	return intProvider{kind: "minecraft:uniform", minimum: encoded.Minimum, maximum: encoded.Maximum}, nil
}

func parseFeatureHeightProvider(raw json.RawMessage) (featureHeightProvider, error) {
	var encoded struct {
		Type    string          `json:"type"`
		Minimum json.RawMessage `json:"min_inclusive"`
		Maximum json.RawMessage `json:"max_inclusive"`
		Plateau int             `json:"plateau"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return featureHeightProvider{}, err
	}
	minimum, err := parseHeightAnchor(encoded.Minimum)
	if err != nil {
		return featureHeightProvider{}, err
	}
	maximum, err := parseHeightAnchor(encoded.Maximum)
	if err != nil {
		return featureHeightProvider{}, err
	}
	return featureHeightProvider{
		kind:    qualify(encoded.Type),
		minimum: minimum,
		maximum: maximum,
		plateau: encoded.Plateau,
	}, nil
}

// applyOreFeatures 执行区块内 generation step 6 的矿石地物。
//
// 这里只执行 ore/scattered_ore；其他地物暂不执行，因此不会把未实现的地物
// 伪装成已生成。矿石写入限制在当前区块内，跨越区块边界的部分会被裁掉。
func (r *Router) applyOreFeatures(chunk *GeneratedChunk, biomes *BiomeSource) {
	if r.features == nil {
		return
	}
	var names []string
	seen := make(map[string]struct{})
	for gridZ := 0; gridZ < 4; gridZ++ {
		for gridX := 0; gridX < 4; gridX++ {
			biome := biomes.BiomeAtBlock(chunk.MinX+gridX*4+2, 64, chunk.MinZ+gridZ*4+2)
			steps := r.features.biomeFeatures[biome]
			if len(steps) <= 6 {
				continue
			}
			for _, name := range steps[6] {
				name = qualify(name)
				if _, ok := seen[name]; ok {
					continue
				}
				seen[name] = struct{}{}
				names = append(names, name)
			}
		}
	}
	if len(names) == 0 {
		return
	}
	random := newWorldgenRandom()
	decorationSeed := random.setDecorationSeed(r.seed, chunk.ChunkX, chunk.ChunkZ)
	for index, name := range names {
		placed, ok := r.features.placed[name]
		if !ok || placed.ore == nil {
			continue
		}
		random.setFeatureSeed(decorationSeed, index, 6)
		r.applyPlacedOre(chunk, random.legacyRandom, placed)
	}
}

type featurePosition struct {
	x, y, z int
}

func (r *Router) applyPlacedOre(chunk *GeneratedChunk, random *legacyRandom, placed placedFeature) {
	positions := []featurePosition{{x: chunk.MinX, y: 0, z: chunk.MinZ}}
	for _, modifier := range placed.placements {
		switch modifier.kind {
		case "minecraft:count":
			count := modifier.count.sample(random)
			if count <= 0 {
				return
			}
			next := make([]featurePosition, 0, len(positions)*count)
			for _, position := range positions {
				for range count {
					next = append(next, position)
				}
			}
			positions = next
		case "minecraft:in_square":
			for i := range positions {
				positions[i].x += random.nextIntBounded(16)
				positions[i].z += random.nextIntBounded(16)
			}
		case "minecraft:height_range":
			for i := range positions {
				positions[i].y = modifier.height.sample(random, r.setting)
			}
		case "minecraft:rarity_filter":
			filtered := positions[:0]
			for _, position := range positions {
				if modifier.chance > 0 && random.nextIntBounded(modifier.chance) == 0 {
					filtered = append(filtered, position)
				}
			}
			positions = filtered
		case "minecraft:biome":
			// 原版 biome filter 只做群系校验；矿石 placed_feature 的群系列表
			// 已由 biomeFeatures 限定，这里保留位置即可。
		default:
			return
		}
	}
	for _, position := range positions {
		if position.y < chunk.MinY || position.y >= chunk.MinY+chunk.Height {
			continue
		}
		r.placeOre(chunk, random, *placed.ore, position.x, position.y, position.z)
	}
}

// placeOre 复刻 1.21.11 OreFeature.place / placeOre（见 CFR 反编译 eys）。
func (r *Router) placeOre(chunk *GeneratedChunk, random *legacyRandom, configuration oreConfiguration, originX, originY, originZ int) {
	angle := random.nextFloat() * float32(math.Pi)
	spread := float32(configuration.size) / 8
	radius := int(math.Ceil(float64((float32(configuration.size)/16*2 + 1) / 2)))
	x1 := float64(originX) + math.Sin(float64(angle))*float64(spread)
	x2 := float64(originX) - math.Sin(float64(angle))*float64(spread)
	z1 := float64(originZ) + math.Cos(float64(angle))*float64(spread)
	z2 := float64(originZ) - math.Cos(float64(angle))*float64(spread)
	y1 := float64(originY + random.nextIntBounded(3) - 2)
	y2 := float64(originY + random.nextIntBounded(3) - 2)
	minX := originX - int(math.Floor(float64(spread))) - radius
	minY := originY - 2 - radius
	minZ := originZ - int(math.Floor(float64(spread))) - radius
	sizeX := 2 * (int(math.Floor(float64(spread))) + radius)
	sizeY := 2 * (2 + radius)
	// OCEAN_FLOOR 高度检查：原版只在源点高度低于/等于包围盒底部时跳过。
	if minY > chunk.HeightAt(originX, originZ) {
		return
	}
	targetCount := configuration.size
	centers := make([]float64, targetCount*4)
	for i := 0; i < targetCount; i++ {
		t := float32(i) / float32(targetCount)
		centerX := lerpFloat64(float64(t), x1, x2)
		centerY := lerpFloat64(float64(t), y1, y2)
		centerZ := lerpFloat64(float64(t), z1, z2)
		ellipsoidRadius := random.nextDouble() * float64(targetCount) / 16
		centerRadius := ((math.Sin(float64(float32(math.Pi)*t))+1)*ellipsoidRadius + 1) / 2
		centers[i*4] = centerX
		centers[i*4+1] = centerY
		centers[i*4+2] = centerZ
		centers[i*4+3] = centerRadius
	}
	// 官方先剔除被更大的椭球完全覆盖的椭球。
	for i := 0; i < targetCount-1; i++ {
		if centers[i*4+3] <= 0 {
			continue
		}
		for j := i + 1; j < targetCount; j++ {
			if centers[j*4+3] <= 0 {
				continue
			}
			dx := centers[i*4] - centers[j*4]
			dy := centers[i*4+1] - centers[j*4+1]
			dz := centers[i*4+2] - centers[j*4+2]
			dr := centers[i*4+3] - centers[j*4+3]
			if dr*dr <= dx*dx+dy*dy+dz*dz {
				continue
			}
			if dr > 0 {
				centers[j*4+3] = -1
			} else {
				centers[i*4+3] = -1
			}
		}
	}
	visited := make([]bool, sizeX*sizeY*sizeX)
	for i := 0; i < targetCount; i++ {
		ellipsoidRadius := centers[i*4+3]
		if ellipsoidRadius < 0 {
			continue
		}
		centerX, centerY, centerZ := centers[i*4], centers[i*4+1], centers[i*4+2]
		startX := maxInt(int(math.Floor(centerX-ellipsoidRadius)), minX)
		startY := maxInt(int(math.Floor(centerY-ellipsoidRadius)), minY)
		startZ := maxInt(int(math.Floor(centerZ-ellipsoidRadius)), minZ)
		endX := maxInt(int(math.Floor(centerX+ellipsoidRadius)), startX)
		endY := maxInt(int(math.Floor(centerY+ellipsoidRadius)), startY)
		endZ := maxInt(int(math.Floor(centerZ+ellipsoidRadius)), startZ)
		for x := startX; x <= endX; x++ {
			nx := (float64(x) + 0.5 - centerX) / ellipsoidRadius
			if nx*nx >= 1 {
				continue
			}
			for y := startY; y <= endY; y++ {
				ny := (float64(y) + 0.5 - centerY) / ellipsoidRadius
				if nx*nx+ny*ny >= 1 {
					continue
				}
				for z := startZ; z <= endZ; z++ {
					nz := (float64(z) + 0.5 - centerZ) / ellipsoidRadius
					if nx*nx+ny*ny+nz*nz >= 1 {
						continue
					}
					if !chunk.InRange(x, y, z) {
						continue
					}
					index := x - minX + (y-minY)*sizeX + (z-minZ)*sizeX*sizeY
					if index < 0 || index >= len(visited) || visited[index] {
						continue
					}
					visited[index] = true
					current := chunk.BlockAt(x, y, z)
					for _, target := range configuration.targets {
						if !r.oreTargetMatches(target, current) {
							continue
						}
						if !oreDiscard(random, configuration.discard) && hasAirExposure(chunk, x, y, z) {
							break
						}
						chunk.SetBlock(x, y, z, target.state)
						break
					}
				}
			}
		}
	}
}

func (r *Router) oreTargetMatches(target oreTarget, block SurfaceBlock) bool {
	if target.tag != "" {
		if state, err := registry.BlockStateID(block.Name); err == nil {
			return registry.BlockTagContains(target.tag, state)
		}
		return false
	}
	return target.block == "" || qualify(target.block) == block.Name
}

// oreDiscard 对应 OreFeature.canPlaceOre 中的 discard_chance_on_air_exposure：
// chance<=0 时跳过空气暴露检查，>=1 时必须检查；中间值按随机数决定。
func oreDiscard(random *legacyRandom, chance float32) bool {
	if chance <= 0 {
		return true
	}
	if chance >= 1 {
		return false
	}
	return random.nextFloat() >= chance
}

func hasAirExposure(chunk *GeneratedChunk, x, y, z int) bool {
	neighbors := [...][3]int{{x + 1, y, z}, {x - 1, y, z}, {x, y + 1, z}, {x, y - 1, z}, {x, y, z + 1}, {x, y, z - 1}}
	for _, neighbor := range neighbors {
		if !chunk.InRange(neighbor[0], neighbor[1], neighbor[2]) {
			continue
		}
		if chunk.BlockAt(neighbor[0], neighbor[1], neighbor[2]).IsAir() {
			return true
		}
	}
	return false
}

func lerpFloat64(t, a, b float64) float64 {
	return a + t*(b-a)
}
