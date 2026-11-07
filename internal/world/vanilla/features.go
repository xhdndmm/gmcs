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
	configured    map[string]json.RawMessage
	biomeFeatures map[string][][]string
}

type placedFeature struct {
	feature    string
	kind       string
	ore        *oreConfiguration
	tree       *treeConfiguration
	selector   *selectorConfiguration
	patch      *patchConfiguration
	simple     *simpleBlockConfiguration
	placements []placementModifier
	supported  bool
}

type selectorConfiguration struct {
	entries    []selectorEntry
	defaultRef *featureRef
}

type selectorEntry struct {
	chance float32
	ref    *featureRef
}

type featureRef struct {
	name       string
	inline     json.RawMessage
	placements []placementModifier
}

type patchConfiguration struct {
	tries    int
	xzSpread int
	yzSpread int
	feature  *featureRef
}

type simpleBlockConfiguration struct {
	provider stateProvider
}

type stateProvider struct {
	entries []weightedState
}

type weightedState struct {
	state  SurfaceBlock
	weight int
}

func (p stateProvider) sample(random *legacyRandom) SurfaceBlock {
	if len(p.entries) == 0 {
		return SurfaceBlock{Name: "minecraft:air"}
	}
	total := 0
	for _, entry := range p.entries {
		total += entry.weight
	}
	value := random.nextIntBounded(total)
	for _, entry := range p.entries {
		if value < entry.weight {
			return entry.state
		}
		value -= entry.weight
	}
	return p.entries[len(p.entries)-1].state
}

type placementModifier struct {
	kind          string
	count         intProvider
	chance        int
	height        featureHeightProvider
	heightmap     string
	predicate     *blockPredicate
	maxWaterDepth int
	supported     bool
}

type blockPredicate struct {
	kind  string
	state string
	tag   string
	block string
}

type treeConfiguration struct {
	trunk         SurfaceBlock
	leaves        SurfaceBlock
	baseHeight    int
	heightRandA   int
	heightRandB   int
	foliageType   string
	foliageRadius intProvider
	foliageOffset intProvider
	foliageHeight int
}

type intProvider struct {
	kind     string
	constant int
	minimum  int
	maximum  int
	entries  []weightedInt
}

type weightedInt struct {
	value  int
	weight int
}

func (p intProvider) sample(random *legacyRandom) int {
	switch p.kind {
	case "constant":
		return p.constant
	case "minecraft:uniform":
		return p.minimum + random.nextIntBounded(p.maximum-p.minimum+1)
	case "minecraft:weighted_list":
		total := 0
		for _, entry := range p.entries {
			total += entry.weight
		}
		if total <= 0 {
			return 0
		}
		value := random.nextIntBounded(total)
		for _, entry := range p.entries {
			if value < entry.weight {
				return entry.value
			}
			value -= entry.weight
		}
		return 0
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
		configured:    data.ConfiguredFeatures,
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
		placed := placedFeature{feature: qualify(encoded.Feature), supported: true}
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
		switch placed.kind {
		case "minecraft:ore", "minecraft:scattered_ore":
			ore, err := parseOreConfiguration(configured.Config)
			if err != nil {
				return nil, fmt.Errorf("解析 %s 的矿石配置失败：%w", placed.feature, err)
			}
			placed.ore = ore
		case "minecraft:tree":
			tree, err := parseTreeConfiguration(configured.Config)
			if err != nil {
				return nil, fmt.Errorf("解析 %s 的树木配置失败：%w", placed.feature, err)
			}
			placed.tree = tree
		case "minecraft:random_selector":
			selector, err := parseSelectorConfiguration(configured.Config)
			if err != nil {
				return nil, fmt.Errorf("解析 %s 的 selector 配置失败：%w", placed.feature, err)
			}
			placed.selector = selector
		case "minecraft:random_patch", "minecraft:flower":
			patch, err := parsePatchConfiguration(configured.Config)
			if err != nil {
				return nil, fmt.Errorf("解析 %s 的 patch 配置失败：%w", placed.feature, err)
			}
			placed.patch = patch
		case "minecraft:simple_block":
			simple, err := parseSimpleBlockConfiguration(configured.Config)
			if err != nil {
				return nil, fmt.Errorf("解析 %s 的简单方块配置失败：%w", placed.feature, err)
			}
			placed.simple = simple
		}
		for index, rawModifier := range encoded.Placement {
			modifier, err := parsePlacementModifier(rawModifier)
			if err != nil {
				return nil, fmt.Errorf("解析 %s 的第 %d 个 placement 失败：%w", name, index, err)
			}
			if !modifier.supported {
				placed.supported = false
			}
			placed.placements = append(placed.placements, modifier)
		}
		if placed.ore == nil && placed.tree == nil && placed.selector == nil && placed.patch == nil && placed.simple == nil {
			placed.supported = false
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
		Type          string          `json:"type"`
		Count         json.RawMessage `json:"count"`
		Chance        int             `json:"chance"`
		Height        json.RawMessage `json:"height"`
		Heightmap     string          `json:"heightmap"`
		Predicate     json.RawMessage `json:"predicate"`
		MaxWaterDepth int             `json:"max_water_depth"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return placementModifier{}, err
	}
	modifier := placementModifier{kind: qualify(encoded.Type), supported: true}
	switch modifier.kind {
	case "minecraft:count":
		count, err := parseIntProvider(encoded.Count)
		if err != nil {
			modifier.supported = false
			return modifier, nil
		}
		modifier.count = count
	case "minecraft:rarity_filter":
		modifier.chance = encoded.Chance
	case "minecraft:height_range":
		height, err := parseFeatureHeightProvider(encoded.Height)
		if err != nil {
			modifier.supported = false
			return modifier, nil
		}
		modifier.height = height
	case "minecraft:heightmap":
		modifier.heightmap = encoded.Heightmap
	case "minecraft:surface_water_depth_filter":
		modifier.maxWaterDepth = encoded.MaxWaterDepth
	case "minecraft:block_predicate_filter":
		predicate, err := parseBlockPredicate(encoded.Predicate)
		if err != nil {
			modifier.supported = false
			return modifier, nil
		}
		modifier.predicate = predicate
	case "minecraft:in_square", "minecraft:biome":
		// 无参数。
	default:
		// 未实现 placement 的地物不执行，但保留数据以保持 feature index。
		modifier.supported = false
	}
	return modifier, nil
}

func parseBlockPredicate(raw json.RawMessage) (*blockPredicate, error) {
	var encoded struct {
		Type  string          `json:"type"`
		State json.RawMessage `json:"state"`
		Tag   string          `json:"tag"`
		Block string          `json:"blocks"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, err
	}
	predicate := &blockPredicate{kind: qualify(encoded.Type), tag: encoded.Tag, block: encoded.Block}
	if len(encoded.State) != 0 {
		state, err := parseFeatureSurfaceBlock(encoded.State)
		if err != nil {
			return nil, err
		}
		predicate.state = state.Name
	}
	return predicate, nil
}

func parseSelectorConfiguration(raw json.RawMessage) (*selectorConfiguration, error) {
	var encoded struct {
		Default  json.RawMessage `json:"default"`
		Features []struct {
			Chance  float32         `json:"chance"`
			Feature json.RawMessage `json:"feature"`
		} `json:"features"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, err
	}
	configuration := &selectorConfiguration{}
	for _, entry := range encoded.Features {
		ref, err := parseFeatureRef(entry.Feature)
		if err != nil {
			return nil, err
		}
		configuration.entries = append(configuration.entries, selectorEntry{chance: entry.Chance, ref: ref})
	}
	if len(encoded.Default) != 0 {
		ref, err := parseFeatureRef(encoded.Default)
		if err != nil {
			return nil, err
		}
		configuration.defaultRef = ref
	}
	return configuration, nil
}

func parseFeatureRef(raw json.RawMessage) (*featureRef, error) {
	var name string
	if err := json.Unmarshal(raw, &name); err == nil {
		return &featureRef{name: qualify(name)}, nil
	}
	var encoded struct {
		Feature   json.RawMessage   `json:"feature"`
		Placement []json.RawMessage `json:"placement"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, err
	}
	ref := &featureRef{}
	var inlineName string
	if err := json.Unmarshal(encoded.Feature, &inlineName); err == nil {
		ref.name = qualify(inlineName)
	} else {
		ref.inline = encoded.Feature
	}
	for _, rawModifier := range encoded.Placement {
		modifier, err := parsePlacementModifier(rawModifier)
		if err != nil {
			return nil, err
		}
		ref.placements = append(ref.placements, modifier)
	}
	return ref, nil
}

func parsePatchConfiguration(raw json.RawMessage) (*patchConfiguration, error) {
	var encoded struct {
		Tries    int             `json:"tries"`
		XZSpread int             `json:"xz_spread"`
		YZSpread int             `json:"yz_spread"`
		Feature  json.RawMessage `json:"feature"`
	}

	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, err
	}
	ref, err := parseFeatureRef(encoded.Feature)
	if err != nil {
		return nil, err
	}
	return &patchConfiguration{tries: encoded.Tries, xzSpread: encoded.XZSpread, yzSpread: encoded.YZSpread, feature: ref}, nil
}

func parseSimpleBlockConfiguration(raw json.RawMessage) (*simpleBlockConfiguration, error) {
	var encoded struct {
		ToPlace json.RawMessage `json:"to_place"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, err
	}
	provider, err := parseStateProvider(encoded.ToPlace)
	if err != nil {
		return nil, err
	}
	return &simpleBlockConfiguration{provider: provider}, nil
}

func parseStateProvider(raw json.RawMessage) (stateProvider, error) {
	var encoded struct {
		Type    string          `json:"type"`
		State   json.RawMessage `json:"state"`
		Entries []struct {
			Data   json.RawMessage `json:"data"`
			Weight int             `json:"weight"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return stateProvider{}, err
	}
	var provider stateProvider
	switch qualify(encoded.Type) {
	case "minecraft:simple_state_provider":
		state, err := parseFeatureSurfaceBlock(encoded.State)
		if err != nil {
			return stateProvider{}, err
		}
		provider.entries = []weightedState{{state: state, weight: 1}}
	case "minecraft:weighted_state_provider":
		for _, entry := range encoded.Entries {
			state, err := parseFeatureSurfaceBlock(entry.Data)
			if err != nil {
				return stateProvider{}, err
			}
			provider.entries = append(provider.entries, weightedState{state: state, weight: entry.Weight})
		}
	default:
		return stateProvider{}, fmt.Errorf("不支持的 state provider %q", encoded.Type)
	}
	return provider, nil
}

func parseTreeConfiguration(raw json.RawMessage) (*treeConfiguration, error) {
	var encoded struct {
		TrunkProvider struct {
			State json.RawMessage `json:"state"`
		} `json:"trunk_provider"`
		FoliageProvider struct {
			State json.RawMessage `json:"state"`
		} `json:"foliage_provider"`
		TrunkPlacer struct {
			Type        string `json:"type"`
			BaseHeight  int    `json:"base_height"`
			HeightRandA int    `json:"height_rand_a"`
			HeightRandB int    `json:"height_rand_b"`
		} `json:"trunk_placer"`
		FoliagePlacer struct {
			Type   string          `json:"type"`
			Radius json.RawMessage `json:"radius"`
			Height json.RawMessage `json:"height"`
		} `json:"foliage_placer"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, nil
	}
	if qualify(encoded.TrunkPlacer.Type) != "minecraft:straight_trunk_placer" ||
		qualify(encoded.FoliagePlacer.Type) != "minecraft:blob_foliage_placer" {
		return nil, nil
	}
	trunk, err := parseFeatureSurfaceBlock(encoded.TrunkProvider.State)
	if err != nil {
		return nil, err
	}
	leaves, err := parseFeatureSurfaceBlock(encoded.FoliageProvider.State)
	if err != nil {
		return nil, err
	}
	radius, err := parseIntProvider(encoded.FoliagePlacer.Radius)
	if err != nil {
		return nil, nil
	}
	var foliageHeight int
	if len(encoded.FoliagePlacer.Height) != 0 {
		if err := json.Unmarshal(encoded.FoliagePlacer.Height, &foliageHeight); err != nil {
			return nil, nil
		}
	}
	return &treeConfiguration{
		trunk:         trunk,
		leaves:        leaves,
		baseHeight:    encoded.TrunkPlacer.BaseHeight,
		heightRandA:   encoded.TrunkPlacer.HeightRandA,
		heightRandB:   encoded.TrunkPlacer.HeightRandB,
		foliageType:   qualify(encoded.FoliagePlacer.Type),
		foliageRadius: radius,
		foliageHeight: foliageHeight,
	}, nil
}

func parseIntProvider(raw json.RawMessage) (intProvider, error) {
	var constant int
	if err := json.Unmarshal(raw, &constant); err == nil {
		return intProvider{kind: "constant", constant: constant}, nil
	}
	var encoded struct {
		Type         string `json:"type"`
		Minimum      int    `json:"min_inclusive"`
		Maximum      int    `json:"max_inclusive"`
		Distribution []struct {
			Data   int `json:"data"`
			Weight int `json:"weight"`
		} `json:"distribution"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return intProvider{}, err
	}
	switch qualify(encoded.Type) {
	case "minecraft:uniform":
		return intProvider{kind: "minecraft:uniform", minimum: encoded.Minimum, maximum: encoded.Maximum}, nil
	case "minecraft:weighted_list":
		provider := intProvider{kind: "minecraft:weighted_list"}
		for _, entry := range encoded.Distribution {
			provider.entries = append(provider.entries, weightedInt{value: entry.Data, weight: entry.Weight})
		}
		return provider, nil
	default:
		return intProvider{}, fmt.Errorf("不支持的 int provider %q", encoded.Type)
	}
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

// applyFeatures 执行区块的 generation steps。当前已实现矿石与部分树木；
// 未实现的地物/placement 会被标记为 unsupported 并跳过。
func (r *Router) applyFeatures(chunk *GeneratedChunk, biomes *BiomeSource) {
	if r.features == nil {
		return
	}
	// 收集该区块所有地表群系的地物列表，并保留首次出现顺序。
	// 原版 FeatureSorter 还会考虑全部 4×4×4 群系并做拓扑排序；这里先实现
	// 地表群系并集，树木/矿石以外的地物在后续阶段继续对齐。
	maxSteps := 0
	byStep := make([][]string, 0, 11)
	seenByStep := make([]map[string]struct{}, 0, 11)
	for gridZ := 0; gridZ < 4; gridZ++ {
		for gridX := 0; gridX < 4; gridX++ {
			biome := biomes.BiomeAtBlock(chunk.MinX+gridX*4+2, 64, chunk.MinZ+gridZ*4+2)
			steps := r.features.biomeFeatures[biome]
			if len(steps) > maxSteps {
				maxSteps = len(steps)
			}
			for len(byStep) < len(steps) {
				byStep = append(byStep, nil)
				seenByStep = append(seenByStep, make(map[string]struct{}))
			}
			for step, names := range steps {
				for _, name := range names {
					name = qualify(name)
					if _, ok := seenByStep[step][name]; ok {
						continue
					}
					seenByStep[step][name] = struct{}{}
					byStep[step] = append(byStep[step], name)
				}
			}
		}
	}
	if maxSteps == 0 {
		return
	}
	random := newWorldgenRandom()
	decorationSeed := random.setDecorationSeed(r.seed, chunk.ChunkX, chunk.ChunkZ)
	for step := 0; step < maxSteps; step++ {
		for index, name := range byStep[step] {
			placed, ok := r.features.placed[name]
			if !ok || !placed.supported || (placed.ore == nil && placed.tree == nil && placed.selector == nil && placed.patch == nil && placed.simple == nil) {
				continue
			}
			random.setFeatureSeed(decorationSeed, index, step)
			r.applyPlacedFeature(chunk, random.legacyRandom, placed)
		}
	}
}

type featurePosition struct {
	x, y, z int
}

func (r *Router) applyPlacedFeature(chunk *GeneratedChunk, random *legacyRandom, placed placedFeature) {
	positions := []featurePosition{{x: chunk.MinX, y: 0, z: chunk.MinZ}}
	for _, modifier := range placed.placements {
		if !modifier.supported {
			return
		}
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
		case "minecraft:heightmap":
			for i := range positions {
				positions[i].y = chunkHeightmap(chunk, positions[i].x, positions[i].z, modifier.heightmap) + 1
			}
		case "minecraft:surface_water_depth_filter":
			filtered := positions[:0]
			for _, position := range positions {
				depth := 0
				for y := position.y; y < chunk.MinY+chunk.Height; y++ {
					block := chunk.BlockAt(position.x, y, position.z)
					if block.Name != "minecraft:water" {
						break
					}
					depth++
				}
				if depth <= modifier.maxWaterDepth {
					filtered = append(filtered, position)
				}
			}
			positions = filtered
		case "minecraft:rarity_filter":
			filtered := positions[:0]
			for _, position := range positions {
				if modifier.chance > 0 && random.nextIntBounded(modifier.chance) == 0 {
					filtered = append(filtered, position)
				}
			}
			positions = filtered
		case "minecraft:block_predicate_filter":
			filtered := positions[:0]
			for _, position := range positions {
				if r.blockPredicateMatches(chunk, modifier.predicate, position.x, position.y, position.z) {
					filtered = append(filtered, position)
				}
			}
			positions = filtered
		case "minecraft:biome":
			// 群系列表已经限定，保留位置。
		default:
			return
		}
	}
	for _, position := range positions {
		if position.y < chunk.MinY || position.y >= chunk.MinY+chunk.Height {
			continue
		}
		switch {
		case placed.ore != nil:
			r.placeOre(chunk, random, *placed.ore, position.x, position.y, position.z)
		case placed.tree != nil:
			r.placeTree(chunk, random, *placed.tree, position.x, position.y, position.z)
		case placed.selector != nil:
			r.placeSelector(chunk, random, *placed.selector, position.x, position.y, position.z)
		case placed.patch != nil:
			r.placePatch(chunk, random, *placed.patch, position.x, position.y, position.z)
		case placed.simple != nil:
			r.placeSimple(chunk, random, *placed.simple, position.x, position.y, position.z)
		}
	}
}

func chunkHeightmap(chunk *GeneratedChunk, x, z int, kind string) int {
	if x < chunk.MinX || x >= chunk.MinX+16 || z < chunk.MinZ || z >= chunk.MinZ+16 {
		return chunk.MinY - 1
	}
	for y := chunk.MinY + chunk.Height - 1; y >= chunk.MinY; y-- {
		block := chunk.BlockAt(x, y, z)
		// 地物放置使用的高度图取“可站立表面”：流体不算表面，避免在水面
		// 上生成草/花。OCEAN_FLOOR 与原版一样取最高固体。
		switch kind {
		case "OCEAN_FLOOR", "OCEAN_FLOOR_WG":
			if !block.IsAir() && !block.HasFluid() {
				return y
			}
		case "MOTION_BLOCKING", "MOTION_BLOCKING_NO_LEAVES":
			if !block.IsAir() && !block.HasFluid() {
				return y
			}
		default: // WORLD_SURFACE, WORLD_SURFACE_WG
			if !block.IsAir() && !block.HasFluid() {
				return y
			}
		}
	}
	return chunk.MinY - 1
}

func (r *Router) blockPredicateMatches(chunk *GeneratedChunk, predicate *blockPredicate, x, y, z int) bool {
	if predicate == nil {
		return true
	}
	block := chunk.BlockAt(x, y, z)
	switch predicate.kind {
	case "minecraft:would_survive":
		if !block.IsAir() {
			return false
		}
		below := chunk.BlockAt(x, y-1, z)
		return !below.IsAir() && !below.HasFluid()
	case "minecraft:matching_blocks":
		return predicate.block == "" || qualify(predicate.block) == block.Name
	case "minecraft:matching_block_tag":
		if state, err := registry.BlockStateID(block.Name); err == nil {
			return registry.BlockTagContains(predicate.tag, state)
		}
		return false
	case "minecraft:matching_fluids":
		return block.Name == qualify(predicate.block) || (predicate.block == "" && block.HasFluid())
	case "minecraft:replaceable":
		return block.IsAir()
	case "minecraft:solid":
		return !block.IsAir() && !block.HasFluid()
	default:
		return false
	}
}

func (r *Router) placePatch(chunk *GeneratedChunk, random *legacyRandom, patch patchConfiguration, x, y, z int) {
	for attempt := 0; attempt < patch.tries; attempt++ {
		offsetX := x + random.nextIntBounded(patch.xzSpread*2+1) - patch.xzSpread
		offsetY := y + random.nextIntBounded(patch.yzSpread*2+1) - patch.yzSpread
		offsetZ := z + random.nextIntBounded(patch.xzSpread*2+1) - patch.xzSpread
		r.applyFeatureRef(chunk, random, patch.feature, offsetX, offsetY, offsetZ)
	}
}

func (r *Router) placeSimple(chunk *GeneratedChunk, random *legacyRandom, simple simpleBlockConfiguration, x, y, z int) {
	if !chunk.InRange(x, y, z) || !chunk.BlockAt(x, y, z).IsAir() {
		return
	}
	chunk.SetBlock(x, y, z, simple.provider.sample(random))
}

func (r *Router) placeSelector(chunk *GeneratedChunk, random *legacyRandom, selector selectorConfiguration, x, y, z int) {
	var chosen *featureRef
	for _, entry := range selector.entries {
		if random.nextFloat() < entry.chance {
			chosen = entry.ref
			break
		}
	}
	if chosen == nil {
		chosen = selector.defaultRef
	}
	if chosen == nil {
		return
	}
	r.applyFeatureRef(chunk, random, chosen, x, y, z)
}

func (r *Router) applyFeatureRef(chunk *GeneratedChunk, random *legacyRandom, ref *featureRef, x, y, z int) {
	raw := ref.inline
	if len(raw) == 0 {
		var ok bool
		raw, ok = r.features.configured[ref.name]
		if !ok {
			return
		}
	}
	var configured struct {
		Type   string          `json:"type"`
		Config json.RawMessage `json:"config"`
	}
	if err := json.Unmarshal(raw, &configured); err != nil {
		return
	}
	placed := placedFeature{feature: ref.name, kind: qualify(configured.Type), placements: ref.placements, supported: true}
	switch placed.kind {
	case "minecraft:ore", "minecraft:scattered_ore":
		ore, err := parseOreConfiguration(configured.Config)
		if err != nil {
			return
		}
		placed.ore = ore
	case "minecraft:tree":
		tree, err := parseTreeConfiguration(configured.Config)
		if err != nil || tree == nil {
			return
		}
		placed.tree = tree
	case "minecraft:random_selector":
		selector, err := parseSelectorConfiguration(configured.Config)
		if err != nil || selector == nil {
			return
		}
		placed.selector = selector
	case "minecraft:random_patch", "minecraft:flower":
		patch, err := parsePatchConfiguration(configured.Config)
		if err != nil || patch == nil {
			return
		}
		placed.patch = patch
	case "minecraft:simple_block":
		simple, err := parseSimpleBlockConfiguration(configured.Config)
		if err != nil || simple == nil {
			return
		}
		placed.simple = simple
	default:
		return
	}
	if len(ref.placements) == 0 {
		switch {
		case placed.ore != nil:
			r.placeOre(chunk, random, *placed.ore, x, y, z)
		case placed.tree != nil:
			r.placeTree(chunk, random, *placed.tree, x, y, z)
		case placed.selector != nil:
			r.placeSelector(chunk, random, *placed.selector, x, y, z)
		case placed.patch != nil:
			r.placePatch(chunk, random, *placed.patch, x, y, z)
		case placed.simple != nil:
			r.placeSimple(chunk, random, *placed.simple, x, y, z)
		}
		return
	}
	// 内层 placed feature 通常只有 block predicate；从头应用 placement 会把
	// 随机原点再次当作区块原点，因此这里只应用确定性的 predicate/heightmap。
	positions := []featurePosition{{x: x, y: y, z: z}}
	for _, modifier := range ref.placements {
		if !modifier.supported {
			return
		}
		switch modifier.kind {
		case "minecraft:block_predicate_filter":
			filtered := positions[:0]
			for _, position := range positions {
				if r.blockPredicateMatches(chunk, modifier.predicate, position.x, position.y, position.z) {
					filtered = append(filtered, position)
				}
			}
			positions = filtered
		case "minecraft:heightmap":
			for i := range positions {
				positions[i].y = chunkHeightmap(chunk, positions[i].x, positions[i].z, modifier.heightmap) + 1
			}
		case "minecraft:surface_water_depth_filter":
			// 在内层 rare 情况下直接保留。
		case "minecraft:biome":
		default:
			return
		}
	}
	for _, position := range positions {
		switch {
		case placed.ore != nil:
			r.placeOre(chunk, random, *placed.ore, position.x, position.y, position.z)
		case placed.tree != nil:
			r.placeTree(chunk, random, *placed.tree, position.x, position.y, position.z)
		case placed.selector != nil:
			r.placeSelector(chunk, random, *placed.selector, position.x, position.y, position.z)
		case placed.patch != nil:
			r.placePatch(chunk, random, *placed.patch, position.x, position.y, position.z)
		case placed.simple != nil:
			r.placeSimple(chunk, random, *placed.simple, position.x, position.y, position.z)
		}
	}
}

// placeTree 实现 straight_trunk_placer + blob_foliage_placer 的近似结构。
// 目前不处理 cross-chunk 写入：跨出当前区块的树干/树叶会被裁掉。
func (r *Router) placeTree(chunk *GeneratedChunk, random *legacyRandom, configuration treeConfiguration, x, y, z int) {
	height := configuration.baseHeight + random.nextIntBounded(configuration.heightRandA+1) + random.nextIntBounded(configuration.heightRandB+1)
	if height <= 0 || y+height+configuration.foliageHeight >= chunk.MinY+chunk.Height {
		return
	}
	for offset := 0; offset < height; offset++ {
		block := chunk.BlockAt(x, y+offset, z)
		if !block.IsAir() && block.Name != configuration.leaves.Name {
			return
		}
	}
	for offset := 0; offset < height; offset++ {
		chunk.SetBlock(x, y+offset, z, configuration.trunk)
	}
	radius := configuration.foliageRadius.sample(random)
	top := y + height - 1
	for layer := 0; layer < configuration.foliageHeight; layer++ {
		layerRadius := radius - 1 - layer/2
		if layerRadius < 0 {
			layerRadius = 0
		}
		layerY := top - layer
		for dx := -layerRadius; dx <= layerRadius; dx++ {
			for dz := -layerRadius; dz <= layerRadius; dz++ {
				if dx*dx+dz*dz > layerRadius*layerRadius+1 {
					continue
				}
				if layerRadius > 0 && absFeatureInt(dx) == layerRadius && absFeatureInt(dz) == layerRadius && random.nextIntBounded(2) == 0 {
					continue
				}
				target := chunk.BlockAt(x+dx, layerY, z+dz)
				if !target.IsAir() && target.Name != configuration.leaves.Name {
					continue
				}
				chunk.SetBlock(x+dx, layerY, z+dz, configuration.leaves)
			}
		}
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

func absFeatureInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
