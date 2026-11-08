//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"

	"gmcs/internal/registry"
)

// featureRegistry 是 placed_feature / configured_feature 的只读解析结果。
// 当前运行时只执行 minecraft:ore / minecraft:scattered_ore；其余地物在解析时
// 保留类型但不会被 GenerateChunk 执行（见 docs/TODO.md 的阶段 4c）。
type featureRegistry struct {
	placed        map[string]placedFeature
	configured    map[string]json.RawMessage
	biomeFeatures map[string][][]string
	// featureSteps 是原版 FeatureSorter 对全部群系求出的全局 step 顺序。
	// 每个 step 内的下标就是 setFeatureSeed 使用的 feature index。
	featureSteps []featureOrderStep
}

type featureOrderStep struct {
	names []string
	index map[string]int
}

type placedFeature struct {
	feature    string
	kind       string
	ore        *oreConfiguration
	tree       *treeConfiguration
	selector   *selectorConfiguration
	patch      *patchConfiguration
	simple     *simpleBlockConfiguration
	spring     *springConfiguration
	seagrass   *seagrassConfiguration
	lake       *lakeConfiguration
	vegetation *vegetationPatchConfiguration
	pile       *pileConfiguration
	disk       *diskConfiguration
	bamboo     *bambooConfiguration
	kelp       *kelpConfiguration
	multiface  *multifaceConfiguration
	fossil     *fossilConfiguration
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

type fossilConfiguration struct {
	fossilStructures  []string
	overlayStructures []string
	maxEmptyCorners   int
}

type multifaceConfiguration struct {
	block             string
	canBePlacedOn     map[string]struct{}
	ceiling           bool
	floor             bool
	wall              bool
	chanceOfSpreading float32
	searchRange       int
}

type kelpConfiguration struct{}

type bambooConfiguration struct {
	probability float32
}

type diskConfiguration struct {
	fallback   stateProvider
	rules      []diskRule
	target     *blockPredicate
	radius     intProvider
	halfHeight int
}

type diskRule struct {
	predicate *blockPredicate
	offsetX   int
	offsetY   int
	offsetZ   int
	state     stateProvider
}

type pileConfiguration struct {
	provider stateProvider
}

type vegetationPatchConfiguration struct {
	replaceable       string
	ground            stateProvider
	vegetation        *featureRef
	surface           string
	depth             intProvider
	extraBottomChance float32
	verticalRange     int
	vegetationChance  float32
	radius            intProvider
	extraEdgeChance   float32
}

type lakeConfiguration struct {
	fluid   stateProvider
	barrier stateProvider
}

type seagrassConfiguration struct {
	probability float32
}

type springConfiguration struct {
	state         SurfaceBlock
	requiresBelow bool
	rockCount     int
	holeCount     int
	validBlocks   map[string]struct{}
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
	// environment_scan
	direction string
	maxSteps  int
	allowed   *blockPredicate
	// surface_relative_threshold_filter
	thresholdMin *int
	thresholdMax *int
	// random_offset
	xzSpread intProvider
	ySpread  intProvider
	// noise_based_count / noise_threshold_count
	noiseToCountRatio int
	noiseFactor       float64
	noiseOffset       float64
	noiseLevel        float64
	aboveNoise        int
	belowNoise        int
	supported         bool
}

type blockPredicate struct {
	kind     string
	state    string
	tag      string
	block    string
	blocks   []string
	offset   [3]int
	children []*blockPredicate
	child    *blockPredicate
}

type treeConfiguration struct {
	trunk               SurfaceBlock
	leaves              SurfaceBlock
	baseHeight          int
	heightRandA         int
	heightRandB         int
	foliageType         string
	foliageRadius       intProvider
	foliageOffset       intProvider
	foliageHeight       int
	foliageHeightRandom intProvider
	foliageTrunkHeight  intProvider
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
		case "minecraft:spring_feature":
			spring, err := parseSpringConfiguration(configured.Config)
			if err != nil {
				return nil, fmt.Errorf("解析 %s 的 spring 配置失败：%w", placed.feature, err)
			}
			placed.spring = spring
		case "minecraft:seagrass":
			seagrass, err := parseSeagrassConfiguration(configured.Config)
			if err != nil {
				return nil, fmt.Errorf("解析 %s 的海草地物配置失败：%w", placed.feature, err)
			}
			placed.seagrass = seagrass
		case "minecraft:lake":
			lake, err := parseLakeConfiguration(configured.Config)
			if err != nil {
				return nil, fmt.Errorf("解析 %s 的湖泊配置失败：%w", placed.feature, err)
			}
			placed.lake = lake
		case "minecraft:vegetation_patch", "minecraft:waterlogged_vegetation_patch":
			vegetation, err := parseVegetationPatchConfiguration(configured.Config)
			if err != nil {
				return nil, fmt.Errorf("解析 %s 的植被 patch 配置失败：%w", placed.feature, err)
			}
			placed.vegetation = vegetation
		case "minecraft:block_pile":
			pile, err := parsePileConfiguration(configured.Config)
			if err != nil {
				return nil, fmt.Errorf("解析 %s 的方块堆配置失败：%w", placed.feature, err)
			}
			placed.pile = pile
		case "minecraft:disk":
			disk, err := parseDiskConfiguration(configured.Config)
			if err != nil {
				return nil, fmt.Errorf("解析 %s 的圆盘配置失败：%w", placed.feature, err)
			}
			placed.disk = disk
		case "minecraft:bamboo":
			bamboo, err := parseBambooConfiguration(configured.Config)
			if err != nil {
				return nil, fmt.Errorf("解析 %s 的竹子配置失败：%w", placed.feature, err)
			}
			placed.bamboo = bamboo
		case "minecraft:kelp":
			placed.kelp = &kelpConfiguration{}
		case "minecraft:multiface_growth":
			multiface, err := parseMultifaceConfiguration(configured.Config)
			if err != nil {
				return nil, fmt.Errorf("解析 %s 的多面生长配置失败：%w", placed.feature, err)
			}
			placed.multiface = multiface
		case "minecraft:fossil":
			fossil, err := parseFossilConfiguration(configured.Config)
			if err != nil {
				return nil, fmt.Errorf("解析 %s 的化石配置失败：%w", placed.feature, err)
			}
			placed.fossil = fossil
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
		if placed.ore == nil && placed.tree == nil && placed.selector == nil && placed.patch == nil && placed.simple == nil && placed.spring == nil && placed.seagrass == nil && placed.lake == nil && placed.vegetation == nil && placed.pile == nil && placed.disk == nil && placed.bamboo == nil && placed.kelp == nil && placed.multiface == nil && placed.fossil == nil {
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
	registry.buildFeatureOrder()
	return registry, nil
}

// buildFeatureOrder 复刻原版 FeatureSorter.buildFeaturesPerStep：
// 1. 按群系注册顺序扫描全部群系，给每个 (step, feature) 记录首次出现顺序；
// 2. 同一群系内相邻地物连边；
// 3. 对 DAG 做 DFS 后序，反转得到拓扑序；
// 4. 按 step 分组，组内顺序即 setFeatureSeed 的 index。
func (r *featureRegistry) buildFeatureOrder() {
	biomes := make([]string, 0, len(r.biomeFeatures))
	for name := range r.biomeFeatures {
		biomes = append(biomes, name)
	}
	sort.Strings(biomes)

	type node struct {
		step    int
		feature string
	}
	featureIDs := make(map[string]int)
	nextID := 0
	nodes := make(map[node]int)
	edges := make(map[node]map[node]struct{})
	for _, biome := range biomes {
		steps := r.biomeFeatures[biome]
		flattened := make([]node, 0)
		for step, names := range steps {
			for _, name := range names {
				name = qualify(name)
				if _, ok := featureIDs[name]; !ok {
					featureIDs[name] = nextID
					nextID++
				}
				n := node{step: step, feature: name}
				nodes[n] = featureIDs[name]
				flattened = append(flattened, n)
			}
		}
		for i := 0; i+1 < len(flattened); i++ {
			if edges[flattened[i]] == nil {
				edges[flattened[i]] = make(map[node]struct{})
			}
			edges[flattened[i]][flattened[i+1]] = struct{}{}
		}
	}
	ordered := make([]node, 0, len(nodes))
	for n := range nodes {
		ordered = append(ordered, n)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].step != ordered[j].step {
			return ordered[i].step < ordered[j].step
		}
		return nodes[ordered[i]] < nodes[ordered[j]]
	})
	visited := make(map[node]bool)
	inProgress := make(map[node]bool)
	result := make([]node, 0, len(nodes))
	var visit func(node) bool
	visit = func(n node) bool {
		if visited[n] {
			return true
		}
		if inProgress[n] {
			return false
		}
		inProgress[n] = true
		children := make([]node, 0, len(edges[n]))
		for child := range edges[n] {
			children = append(children, child)
		}
		sort.Slice(children, func(i, j int) bool {
			if children[i].step != children[j].step {
				return children[i].step < children[j].step
			}
			return nodes[children[i]] < nodes[children[j]]
		})
		for _, child := range children {
			if !visit(child) {
				return false
			}
		}
		delete(inProgress, n)
		visited[n] = true
		result = append(result, n)
		return true
	}
	ok := true
	for _, n := range ordered {
		if !visit(n) {
			ok = false
			break
		}
	}
	if !ok {
		// 数据异常时退回各 step 的首次出现顺序，保证服务器仍能启动。
		r.featureSteps = nil
		return
	}
	maxStep := 0
	for _, n := range result {
		if n.step+1 > maxStep {
			maxStep = n.step + 1
		}
	}
	steps := make([]featureOrderStep, maxStep)
	for i := range steps {
		steps[i].index = make(map[string]int)
	}
	for i := len(result) - 1; i >= 0; i-- {
		n := result[i]
		steps[n.step].index[n.feature] = len(steps[n.step].names)
		steps[n.step].names = append(steps[n.step].names, n.feature)
	}
	r.featureSteps = steps
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
		Direction     string          `json:"direction_of_search"`
		MaxSteps      int             `json:"max_steps"`
		Target        json.RawMessage `json:"target_condition"`
		Allowed       json.RawMessage `json:"allowed_search_condition"`
		MinInclusive  *int            `json:"min_inclusive"`
		MaxInclusive  *int            `json:"max_inclusive"`
		XZSpread      json.RawMessage `json:"xz_spread"`
		YSpread       json.RawMessage `json:"y_spread"`
		NoiseToCount  int             `json:"noise_to_count_ratio"`
		NoiseFactor   float64         `json:"noise_factor"`
		NoiseOffset   float64         `json:"noise_offset"`
		NoiseLevel    float64         `json:"noise_level"`
		AboveNoise    int             `json:"above_noise"`
		BelowNoise    int             `json:"below_noise"`
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
	case "minecraft:environment_scan":
		modifier.direction = encoded.Direction
		modifier.maxSteps = encoded.MaxSteps
		if len(encoded.Target) != 0 {
			target, err := parseBlockPredicate(encoded.Target)
			if err != nil {
				modifier.supported = false
				return modifier, nil
			}
			modifier.predicate = target
		}
		if len(encoded.Allowed) != 0 {
			allowed, err := parseBlockPredicate(encoded.Allowed)
			if err != nil {
				modifier.supported = false
				return modifier, nil
			}
			modifier.allowed = allowed
		}
	case "minecraft:surface_relative_threshold_filter":
		modifier.heightmap = encoded.Heightmap
		modifier.thresholdMin = encoded.MinInclusive
		modifier.thresholdMax = encoded.MaxInclusive
	case "minecraft:noise_based_count":
		modifier.noiseToCountRatio = encoded.NoiseToCount
		modifier.noiseFactor = encoded.NoiseFactor
		modifier.noiseOffset = encoded.NoiseOffset
	case "minecraft:noise_threshold_count":
		modifier.noiseLevel = encoded.NoiseLevel
		modifier.aboveNoise = encoded.AboveNoise
		modifier.belowNoise = encoded.BelowNoise
	case "minecraft:random_offset":
		xz, err := parseIntProvider(encoded.XZSpread)
		if err != nil {
			modifier.supported = false
			return modifier, nil
		}
		y, err := parseIntProvider(encoded.YSpread)
		if err != nil {
			modifier.supported = false
			return modifier, nil
		}
		modifier.xzSpread = xz
		modifier.ySpread = y
	default:
		// 未实现 placement 的地物不执行，但保留数据以保持 feature index。
		modifier.supported = false
	}
	return modifier, nil
}

func parseBlockPredicate(raw json.RawMessage) (*blockPredicate, error) {
	var encoded struct {
		Type       string            `json:"type"`
		State      json.RawMessage   `json:"state"`
		Tag        string            `json:"tag"`
		Blocks     json.RawMessage   `json:"blocks"`
		Offset     []int             `json:"offset"`
		Predicates []json.RawMessage `json:"predicates"`
		Predicate  json.RawMessage   `json:"predicate"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, err
	}
	predicate := &blockPredicate{kind: qualify(encoded.Type), tag: encoded.Tag}
	if len(encoded.Offset) == 3 {
		predicate.offset = [3]int{encoded.Offset[0], encoded.Offset[1], encoded.Offset[2]}
	}
	for _, child := range encoded.Predicates {
		parsed, err := parseBlockPredicate(child)
		if err != nil {
			return nil, err
		}
		predicate.children = append(predicate.children, parsed)
	}
	if len(encoded.Predicate) != 0 {
		child, err := parseBlockPredicate(encoded.Predicate)
		if err != nil {
			return nil, err
		}
		predicate.child = child
	}
	var single string
	if err := json.Unmarshal(encoded.Blocks, &single); err == nil {
		predicate.block = single
	} else {
		var many []string
		if err := json.Unmarshal(encoded.Blocks, &many); err == nil {
			predicate.blocks = many
		}
	}
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
	case "minecraft:simple_state_provider", "minecraft:rotated_block_provider":
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

func parseFossilConfiguration(raw json.RawMessage) (*fossilConfiguration, error) {
	var encoded struct {
		FossilStructures  []string `json:"fossil_structures"`
		OverlayStructures []string `json:"overlay_structures"`
		MaxEmptyCorners   int      `json:"max_empty_corners_allowed"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, err
	}
	if len(encoded.FossilStructures) == 0 {
		return nil, fmt.Errorf("化石配置缺少 fossil_structures")
	}
	return &fossilConfiguration{
		fossilStructures:  encoded.FossilStructures,
		overlayStructures: encoded.OverlayStructures,
		maxEmptyCorners:   encoded.MaxEmptyCorners,
	}, nil
}

func parseMultifaceConfiguration(raw json.RawMessage) (*multifaceConfiguration, error) {
	var encoded struct {
		Block             string   `json:"block"`
		CanBePlacedOn     []string `json:"can_be_placed_on"`
		Ceiling           bool     `json:"can_place_on_ceiling"`
		Floor             bool     `json:"can_place_on_floor"`
		Wall              bool     `json:"can_place_on_wall"`
		ChanceOfSpreading float32  `json:"chance_of_spreading"`
		SearchRange       int      `json:"search_range"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, err
	}
	allowed := make(map[string]struct{}, len(encoded.CanBePlacedOn))
	for _, name := range encoded.CanBePlacedOn {
		allowed[qualify(name)] = struct{}{}
	}
	return &multifaceConfiguration{
		block:             qualify(encoded.Block),
		canBePlacedOn:     allowed,
		ceiling:           encoded.Ceiling,
		floor:             encoded.Floor,
		wall:              encoded.Wall,
		chanceOfSpreading: encoded.ChanceOfSpreading,
		searchRange:       encoded.SearchRange,
	}, nil
}

func parseBambooConfiguration(raw json.RawMessage) (*bambooConfiguration, error) {
	var encoded struct {
		Probability float32 `json:"probability"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, err
	}
	return &bambooConfiguration{probability: encoded.Probability}, nil
}

func parseDiskConfiguration(raw json.RawMessage) (*diskConfiguration, error) {
	var encoded struct {
		StateProvider json.RawMessage `json:"state_provider"`
		Target        json.RawMessage `json:"target"`
		Radius        json.RawMessage `json:"radius"`
		HalfHeight    int             `json:"half_height"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, err
	}
	fallback, rules, err := parseRuleBasedStateProvider(encoded.StateProvider)
	if err != nil {
		return nil, err
	}
	target, err := parseBlockPredicate(encoded.Target)
	if err != nil {
		return nil, err
	}
	radius, err := parseIntProvider(encoded.Radius)
	if err != nil {
		return nil, err
	}
	return &diskConfiguration{fallback: fallback, rules: rules, target: target, radius: radius, halfHeight: encoded.HalfHeight}, nil
}

func parseRuleBasedStateProvider(raw json.RawMessage) (stateProvider, []diskRule, error) {
	var encoded struct {
		Fallback json.RawMessage `json:"fallback"`
		Rules    []struct {
			IfTrue json.RawMessage `json:"if_true"`
			Then   json.RawMessage `json:"then"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return stateProvider{}, nil, err
	}
	fallback, err := parseStateProvider(encoded.Fallback)
	if err != nil {
		return stateProvider{}, nil, err
	}
	rules := make([]diskRule, 0, len(encoded.Rules))
	for _, entry := range encoded.Rules {
		predicate, err := parseBlockPredicate(entry.IfTrue)
		if err != nil {
			return stateProvider{}, nil, err
		}
		state, err := parseStateProvider(entry.Then)
		if err != nil {
			return stateProvider{}, nil, err
		}
		rules = append(rules, diskRule{predicate: predicate, state: state})
	}
	return fallback, rules, nil
}

func parsePileConfiguration(raw json.RawMessage) (*pileConfiguration, error) {
	var encoded struct {
		StateProvider json.RawMessage `json:"state_provider"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, err
	}
	provider, err := parseStateProvider(encoded.StateProvider)
	if err != nil {
		return nil, err
	}
	return &pileConfiguration{provider: provider}, nil
}

func parseVegetationPatchConfiguration(raw json.RawMessage) (*vegetationPatchConfiguration, error) {
	var encoded struct {
		Replaceable       string          `json:"replaceable"`
		GroundState       json.RawMessage `json:"ground_state"`
		VegetationFeature json.RawMessage `json:"vegetation_feature"`
		Surface           string          `json:"surface"`
		Depth             json.RawMessage `json:"depth"`
		ExtraBottom       float32         `json:"extra_bottom_block_chance"`
		VerticalRange     int             `json:"vertical_range"`
		VegetationChance  float32         `json:"vegetation_chance"`
		Radius            json.RawMessage `json:"xz_radius"`
		ExtraEdge         float32         `json:"extra_edge_column_chance"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, err
	}
	ground, err := parseStateProvider(encoded.GroundState)
	if err != nil {
		return nil, err
	}
	vegetation, err := parseFeatureRef(encoded.VegetationFeature)
	if err != nil {
		return nil, err
	}
	depth, err := parseIntProvider(encoded.Depth)
	if err != nil {
		return nil, err
	}
	radius, err := parseIntProvider(encoded.Radius)
	if err != nil {
		return nil, err
	}
	return &vegetationPatchConfiguration{
		replaceable:       qualify(encoded.Replaceable),
		ground:            ground,
		vegetation:        vegetation,
		surface:           encoded.Surface,
		depth:             depth,
		extraBottomChance: encoded.ExtraBottom,
		verticalRange:     encoded.VerticalRange,
		vegetationChance:  encoded.VegetationChance,
		radius:            radius,
		extraEdgeChance:   encoded.ExtraEdge,
	}, nil
}

func parseLakeConfiguration(raw json.RawMessage) (*lakeConfiguration, error) {
	var encoded struct {
		Fluid   json.RawMessage `json:"fluid"`
		Barrier json.RawMessage `json:"barrier"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, err
	}
	fluid, err := parseStateProvider(encoded.Fluid)
	if err != nil {
		return nil, err
	}
	barrier, err := parseStateProvider(encoded.Barrier)
	if err != nil {
		return nil, err
	}
	return &lakeConfiguration{fluid: fluid, barrier: barrier}, nil
}

func parseSeagrassConfiguration(raw json.RawMessage) (*seagrassConfiguration, error) {
	var encoded struct {
		Probability float32 `json:"probability"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, err
	}
	return &seagrassConfiguration{probability: encoded.Probability}, nil
}

func parseSpringConfiguration(raw json.RawMessage) (*springConfiguration, error) {
	var encoded struct {
		State              json.RawMessage `json:"state"`
		RequiresBlockBelow bool            `json:"requires_block_below"`
		RockCount          int             `json:"rock_count"`
		HoleCount          int             `json:"hole_count"`
		ValidBlocks        json.RawMessage `json:"valid_blocks"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, err
	}
	state, err := parseFeatureSurfaceBlock(encoded.State)
	if err != nil {
		return nil, err
	}
	valid := map[string]struct{}{}
	var names []string
	if err := json.Unmarshal(encoded.ValidBlocks, &names); err == nil {
		for _, name := range names {
			valid[qualify(name)] = struct{}{}
		}
	} else {
		var tag string
		if err := json.Unmarshal(encoded.ValidBlocks, &tag); err != nil {
			return nil, err
		}
		valid["#"+qualify(tag)] = struct{}{}
	}
	return &springConfiguration{state: state, requiresBelow: encoded.RequiresBlockBelow, rockCount: encoded.RockCount, holeCount: encoded.HoleCount, validBlocks: valid}, nil
}

func parseTreeConfiguration(raw json.RawMessage) (*treeConfiguration, error) {
	var encoded struct {
		TrunkProvider struct {
			State   json.RawMessage `json:"state"`
			Entries []struct {
				Data json.RawMessage `json:"data"`
			} `json:"entries"`
		} `json:"trunk_provider"`
		FoliageProvider struct {
			State   json.RawMessage `json:"state"`
			Entries []struct {
				Data json.RawMessage `json:"data"`
			} `json:"entries"`
		} `json:"foliage_provider"`
		TrunkPlacer struct {
			Type        string `json:"type"`
			BaseHeight  int    `json:"base_height"`
			HeightRandA int    `json:"height_rand_a"`
			HeightRandB int    `json:"height_rand_b"`
		} `json:"trunk_placer"`
		FoliagePlacer struct {
			Type        string          `json:"type"`
			Radius      json.RawMessage `json:"radius"`
			Height      json.RawMessage `json:"height"`
			Offset      json.RawMessage `json:"offset"`
			TrunkHeight json.RawMessage `json:"trunk_height"`
		} `json:"foliage_placer"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, nil
	}
	foliageType := qualify(encoded.FoliagePlacer.Type)
	trunkType := qualify(encoded.TrunkPlacer.Type)
	knownTrunk := trunkType == "minecraft:straight_trunk_placer" ||
		trunkType == "minecraft:dark_oak_trunk_placer" ||
		trunkType == "minecraft:forking_trunk_placer" ||
		trunkType == "minecraft:fancy_trunk_placer" ||
		trunkType == "minecraft:giant_trunk_placer" ||
		trunkType == "minecraft:mega_jungle_trunk_placer" ||
		trunkType == "minecraft:bending_trunk_placer" ||
		trunkType == "minecraft:upwards_branching_trunk_placer" ||
		trunkType == "minecraft:cherry_trunk_placer"
	if !knownTrunk || !strings.HasSuffix(foliageType, "_foliage_placer") {
		return nil, nil
	}
	trunk, err := parseProviderFirstState(encoded.TrunkProvider.State, encoded.TrunkProvider.Entries)
	if err != nil {
		return nil, err
	}
	leaves, err := parseProviderFirstState(encoded.FoliageProvider.State, encoded.FoliageProvider.Entries)
	if err != nil {
		return nil, err
	}
	radius, err := parseIntProvider(encoded.FoliagePlacer.Radius)
	if err != nil {
		return nil, nil
	}
	var foliageHeight int
	var foliageHeightRandom intProvider
	if len(encoded.FoliagePlacer.Height) != 0 {
		if err := json.Unmarshal(encoded.FoliagePlacer.Height, &foliageHeight); err != nil {
			provider, providerErr := parseIntProvider(encoded.FoliagePlacer.Height)
			if providerErr != nil {
				return nil, nil
			}
			foliageHeightRandom = provider
		}
	}
	offset, err := parseOptionalIntProvider(encoded.FoliagePlacer.Offset, 0)
	if err != nil {
		return nil, nil
	}
	trunkHeight, err := parseOptionalIntProvider(encoded.FoliagePlacer.TrunkHeight, 0)
	if err != nil {
		return nil, nil
	}
	return &treeConfiguration{
		trunk:               trunk,
		leaves:              leaves,
		baseHeight:          encoded.TrunkPlacer.BaseHeight,
		heightRandA:         encoded.TrunkPlacer.HeightRandA,
		heightRandB:         encoded.TrunkPlacer.HeightRandB,
		foliageType:         foliageType,
		foliageRadius:       radius,
		foliageOffset:       offset,
		foliageHeight:       foliageHeight,
		foliageHeightRandom: foliageHeightRandom,
		foliageTrunkHeight:  trunkHeight,
	}, nil
}

func parseProviderFirstState(state json.RawMessage, entries []struct {
	Data json.RawMessage `json:"data"`
}) (SurfaceBlock, error) {
	if len(state) != 0 {
		return parseFeatureSurfaceBlock(state)
	}
	if len(entries) == 0 {
		return SurfaceBlock{}, fmt.Errorf("state provider 没有 state/entries")
	}
	return parseFeatureSurfaceBlock(entries[0].Data)
}

func parseOptionalIntProvider(raw json.RawMessage, fallback int) (intProvider, error) {
	if len(raw) == 0 {
		return intProvider{kind: "constant", constant: fallback}, nil
	}
	return parseIntProvider(raw)
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
	if len(r.features.featureSteps) == 0 {
		return
	}
	// 原版 applyBiomeDecoration 收集中心区块周围 3×3 区块内全部
	// 4×4×4 section 群系，再用全局 FeatureSorter 顺序筛选本 step 的
	// feature index，并按升序执行。
	present := r.collectFeatureBiomes(chunk, biomes)
	random := newWorldgenRandom()
	decorationSeed := random.setDecorationSeed(r.seed, chunk.ChunkX, chunk.ChunkZ)
	for step, order := range r.features.featureSteps {
		indices := make([]int, 0, len(order.names))
		for biome := range present {
			steps := r.features.biomeFeatures[biome]
			if step >= len(steps) {
				continue
			}
			for _, name := range steps[step] {
				if index, ok := order.index[qualify(name)]; ok {
					indices = append(indices, index)
				}
			}
		}
		sort.Ints(indices)
		last := -1
		for _, index := range indices {
			if index == last {
				continue
			}
			last = index
			name := order.names[index]
			placed, ok := r.features.placed[name]
			if !ok || !placed.supported || (placed.ore == nil && placed.tree == nil && placed.selector == nil && placed.patch == nil && placed.simple == nil && placed.spring == nil && placed.seagrass == nil && placed.lake == nil && placed.vegetation == nil && placed.pile == nil && placed.disk == nil && placed.bamboo == nil && placed.kelp == nil && placed.multiface == nil && placed.fossil == nil) {
				continue
			}
			random.setFeatureSeed(decorationSeed, index, step)
			r.applyPlacedFeature(chunk, random.legacyRandom, placed)
		}
	}
}

// collectFeatureBiomes 收集中心区块周围 3×3 区块内所有 section 的群系。
// 与原版 ChunkAccess.getSections → BiomeManager 的 4×4×4 采样一致。
func (r *Router) collectFeatureBiomes(chunk *GeneratedChunk, biomes *BiomeSource) map[string]struct{} {
	present := make(map[string]struct{}, 32)
	for chunkX := chunk.ChunkX - 1; chunkX <= chunk.ChunkX+1; chunkX++ {
		for chunkZ := chunk.ChunkZ - 1; chunkZ <= chunk.ChunkZ+1; chunkZ++ {
			for biome := range r.chunkBiomeSet(chunkX, chunkZ, chunk.MinY, chunk.Height, biomes) {
				present[biome] = struct{}{}
			}
		}
	}
	return present
}

// chunkBiomeSet 缓存单个区块的 4×4×4 群系集合。3×3 邻域复用后，
// 每个区块只需做一次完整 section 采样，避免重复 9 倍开销。
func (r *Router) chunkBiomeSet(chunkX, chunkZ, minY, height int, biomes *BiomeSource) map[string]struct{} {
	key := chunkCoord{x: chunkX, z: chunkZ}
	if cached, ok := r.biomeSetCache[key]; ok {
		return cached
	}
	if len(r.biomeSetCache) > 1024 {
		r.biomeSetCache = make(map[chunkCoord]map[string]struct{}, 1024)
	}
	set := make(map[string]struct{}, 8)
	// 地物索引只需要“本区块可能出现的群系集合”。当前执行的地物以地表/浅层
	// 为主，按 y=64 的 4×4 地表群系采样，避免每区块对全部 section 做数千次
	// 气候噪声求值（实测这是区块生成的主要开销）。
	quartY := 64 >> 2
	for quartX := 0; quartX < 4; quartX++ {
		for quartZ := 0; quartZ < 4; quartZ++ {
			set[biomes.BiomeAt(chunkX*4+quartX, quartY, chunkZ*4+quartZ)] = struct{}{}
		}
	}
	r.biomeSetCache[key] = set
	return set
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
		case "minecraft:noise_based_count":
			next := make([]featurePosition, 0, len(positions))
			for _, position := range positions {
				count := noiseBasedCount(position.x, position.y, position.z, modifier)
				for n := 0; n < count; n++ {
					next = append(next, position)
				}
			}
			positions = next
		case "minecraft:noise_threshold_count":
			next := make([]featurePosition, 0, len(positions))
			for _, position := range positions {
				count := noiseThresholdCount(position.x, position.z, modifier)
				for n := 0; n < count; n++ {
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
		case "minecraft:environment_scan":
			next := make([]featurePosition, 0, len(positions))
			for _, position := range positions {
				if modifier.allowed != nil && !r.blockPredicateMatches(chunk, modifier.allowed, position.x, position.y, position.z) {
					continue
				}
				dx, dy, dz := directionDelta(modifier.direction)
				found := false
				for step := 0; step < modifier.maxSteps; step++ {
					position.x += dx
					position.y += dy
					position.z += dz
					if !chunk.InRange(position.x, position.y, position.z) {
						break
					}
					if modifier.predicate == nil || r.blockPredicateMatches(chunk, modifier.predicate, position.x, position.y, position.z) {
						found = true
						break
					}
				}
				if found {
					next = append(next, position)
				}
			}
			positions = next
		case "minecraft:surface_relative_threshold_filter":
			filtered := positions[:0]
			for _, position := range positions {
				height := chunkHeightmap(chunk, position.x, position.z, modifier.heightmap)
				value := position.y - height
				if modifier.thresholdMin != nil && value < *modifier.thresholdMin {
					continue
				}
				if modifier.thresholdMax != nil && value > *modifier.thresholdMax {
					continue
				}
				filtered = append(filtered, position)
			}
			positions = filtered
		case "minecraft:random_offset":
			for i := range positions {
				xOffset := modifier.xzSpread.sample(random)
				zOffset := modifier.xzSpread.sample(random)
				positions[i].x += xOffset
				positions[i].z += zOffset
				positions[i].y += modifier.ySpread.sample(random)
			}
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
		case placed.spring != nil:
			r.placeSpring(chunk, *placed.spring, position.x, position.y, position.z)
		case placed.seagrass != nil:
			r.placeSeagrass(chunk, random, *placed.seagrass, position.x, position.y, position.z)
		case placed.lake != nil:
			r.placeLake(chunk, random, *placed.lake, position.x, position.y, position.z)
		case placed.vegetation != nil:
			r.placeVegetationPatch(chunk, random, *placed.vegetation, position.x, position.y, position.z)
		case placed.pile != nil:
			r.placeBlockPile(chunk, random, *placed.pile, position.x, position.y, position.z)
		case placed.disk != nil:
			r.placeDisk(chunk, random, *placed.disk, position.x, position.y, position.z)
		case placed.bamboo != nil:
			r.placeBamboo(chunk, random, *placed.bamboo, position.x, position.y, position.z)
		case placed.kelp != nil:
			r.placeKelp(chunk, random, position.x, position.y, position.z)
		case placed.multiface != nil:
			r.placeMultiface(chunk, random, *placed.multiface, position.x, position.y, position.z)
		case placed.fossil != nil:
			r.placeFossil(chunk, random, *placed.fossil, position.x, position.y, position.z)
		}
	}
}

var featureNoiseOnce = sync.OnceValue(func() *NormalNoise {
	return NewNormalNoise(NewXoroshiro(0), -4, []float64{1})
})

func noiseBasedCount(x, y, z int, modifier placementModifier) int {
	if modifier.noiseFactor == 0 {
		return 0
	}
	value := featureNoiseOnce().Value(float64(x)/modifier.noiseFactor, float64(y)/modifier.noiseFactor, float64(z)/modifier.noiseFactor)
	count := int(math.Round((value + modifier.noiseOffset) * float64(modifier.noiseToCountRatio)))
	if count < 0 {
		return 0
	}
	return count
}

func noiseThresholdCount(x, z int, modifier placementModifier) int {
	// NoiseThresholdCountPlacement 的噪声因子固定为 80。
	value := featureNoiseOnce().Value(float64(x)/80, 0, float64(z)/80)
	if value < modifier.noiseLevel {
		return modifier.belowNoise
	}
	return modifier.aboveNoise
}

func directionDelta(direction string) (int, int, int) {
	switch direction {
	case "up":
		return 0, 1, 0
	case "north":
		return 0, 0, -1
	case "south":
		return 0, 0, 1
	case "west":
		return -1, 0, 0
	case "east":
		return 1, 0, 0
	default:
		return 0, -1, 0
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
	block := chunk.BlockAt(x+predicate.offset[0], y+predicate.offset[1], z+predicate.offset[2])
	switch predicate.kind {
	case "minecraft:all_of":
		for _, child := range predicate.children {
			if !r.blockPredicateMatches(chunk, child, x, y, z) {
				return false
			}
		}
		return true
	case "minecraft:any_of":
		for _, child := range predicate.children {
			if r.blockPredicateMatches(chunk, child, x, y, z) {
				return true
			}
		}
		return false
	case "minecraft:not":
		return predicate.child == nil || !r.blockPredicateMatches(chunk, predicate.child, x, y, z)
	case "minecraft:inside_world_bounds":
		return y >= chunk.MinY && y < chunk.MinY+chunk.Height && x >= chunk.MinX-15 && x < chunk.MinX+31 && z >= chunk.MinZ-15 && z < chunk.MinZ+31
	case "minecraft:would_survive":
		if !block.IsAir() {
			return false
		}
		below := chunk.BlockAt(x, y-1, z)
		return !below.IsAir() && !below.HasFluid()
	case "minecraft:matching_blocks":
		if predicate.block != "" && qualify(predicate.block) == block.Name {
			return true
		}
		for _, name := range predicate.blocks {
			if qualify(name) == block.Name {
				return true
			}
		}
		return false
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

func (r *Router) placeFossil(chunk *GeneratedChunk, random *legacyRandom, configuration fossilConfiguration, x, y, z int) {
	data, err := LoadStructureData()
	if err != nil {
		return
	}
	index := random.nextIntBounded(len(configuration.fossilStructures))
	name := configuration.fossilStructures[index]
	template, err := data.ParsedTemplate(name)
	if err != nil || template == nil {
		return
	}
	rotation := templateRotation(random.nextIntBounded(4))
	if empty := templateCornerAirCount(chunk, template, x, y, z, rotation, false); empty > configuration.maxEmptyCorners {
		return
	}
	placeTemplate(chunk, template, x, y, z, rotation, false)
	if len(configuration.overlayStructures) == 0 {
		return
	}
	overlayName := configuration.overlayStructures[index%len(configuration.overlayStructures)]
	overlay, err := data.ParsedTemplate(overlayName)
	if err != nil || overlay == nil {
		return
	}
	placeTemplate(chunk, overlay, x, y, z, rotation, false)
}

func (r *Router) placeMultiface(chunk *GeneratedChunk, random *legacyRandom, configuration multifaceConfiguration, x, y, z int) {
	if !chunk.InRange(x, y, z) || !chunk.BlockAt(x, y, z).IsAir() {
		return
	}
	type face struct {
		name       string
		dx, dy, dz int
	}
	faces := []face{{"north", 0, 0, -1}, {"south", 0, 0, 1}, {"east", 1, 0, 0}, {"west", -1, 0, 0}}
	if configuration.ceiling {
		faces = append(faces, face{"up", 0, 1, 0})
	}
	if configuration.floor {
		faces = append(faces, face{"down", 0, -1, 0})
	}
	for i := len(faces) - 1; i > 0; i-- {
		j := random.nextIntBounded(i + 1)
		faces[i], faces[j] = faces[j], faces[i]
	}
	for _, candidate := range faces {
		if candidate.dy == 0 && !configuration.wall {
			continue
		}
		neighbor := chunk.BlockAt(x+candidate.dx, y+candidate.dy, z+candidate.dz)
		if _, ok := configuration.canBePlacedOn[neighbor.Name]; !ok {
			continue
		}
		properties := map[string]string{candidate.name: "true"}
		chunk.SetBlock(x, y, z, SurfaceBlock{Name: configuration.block, Properties: properties})
		return
	}
}

func (r *Router) placeKelp(chunk *GeneratedChunk, random *legacyRandom, x, y, z int) {
	if !chunk.InRange(x, y, z) || chunk.BlockAt(x, y, z).Name != "minecraft:water" {
		return
	}
	below := chunk.BlockAt(x, y-1, z)
	if below.IsAir() || below.HasFluid() {
		return
	}
	height := random.nextIntBounded(10) + 1
	for offset := 0; offset < height; offset++ {
		posY := y + offset
		if !chunk.InRange(x, posY, z) || chunk.BlockAt(x, posY, z).Name != "minecraft:water" {
			break
		}
		if offset == height-1 {
			chunk.SetBlock(x, posY, z, SurfaceBlock{Name: "minecraft:kelp", Properties: map[string]string{"age": fmt.Sprintf("%d", random.nextIntBounded(26))}})
		} else {
			chunk.SetBlock(x, posY, z, SurfaceBlock{Name: "minecraft:kelp_plant"})
		}
	}
}

func (r *Router) placeBamboo(chunk *GeneratedChunk, random *legacyRandom, configuration bambooConfiguration, x, y, z int) {
	if !chunk.InRange(x, y, z) || !chunk.BlockAt(x, y, z).IsAir() {
		return
	}
	below := chunk.BlockAt(x, y-1, z)
	if below.Name != "minecraft:grass_block" && below.Name != "minecraft:dirt" && below.Name != "minecraft:podzol" {
		return
	}
	height := random.nextIntBounded(12) + 5
	if random.nextFloat() < configuration.probability {
		radius := random.nextIntBounded(4) + 1
		for dx := -radius; dx <= radius; dx++ {
			for dz := -radius; dz <= radius; dz++ {
				if dx*dx+dz*dz > radius*radius {
					continue
				}
				posX, posZ := x+dx, z+dz
				if !chunk.InRange(posX, y-1, posZ) {
					continue
				}
				if block := chunk.BlockAt(posX, y-1, posZ); block.Name == "minecraft:grass_block" || block.Name == "minecraft:dirt" {
					chunk.SetBlock(posX, y-1, posZ, SurfaceBlock{Name: "minecraft:podzol"})
				}
			}
		}
	}
	for offset := 0; offset < height && chunk.InRange(x, y+offset, z); offset++ {
		if !chunk.BlockAt(x, y+offset, z).IsAir() {
			break
		}
		chunk.SetBlock(x, y+offset, z, SurfaceBlock{Name: "minecraft:bamboo"})
	}
}

func (r *Router) placeDisk(chunk *GeneratedChunk, random *legacyRandom, configuration diskConfiguration, x, y, z int) {
	radius := configuration.radius.sample(random)
	top := y + configuration.halfHeight
	bottom := y - configuration.halfHeight - 1
	for dx := -radius; dx <= radius; dx++ {
		for dz := -radius; dz <= radius; dz++ {
			if dx*dx+dz*dz > radius*radius {
				continue
			}
			placed := false
			for cursorY := top; cursorY > bottom; cursorY-- {
				posX, posZ := x+dx, z+dz
				if !chunk.InRange(posX, cursorY, posZ) {
					continue
				}
				if !r.blockPredicateMatches(chunk, configuration.target, posX, cursorY, posZ) {
					placed = false
					continue
				}
				state := configuration.fallback.sample(random)
				for _, rule := range configuration.rules {
					if r.blockPredicateMatches(chunk, rule.predicate, posX+rule.offsetX, cursorY+rule.offsetY, posZ+rule.offsetZ) {
						state = rule.state.sample(random)
						break
					}
				}
				chunk.SetBlock(posX, cursorY, posZ, state)
				if !placed {
					above := chunk.BlockAt(posX, cursorY+1, posZ)
					if above.IsAir() {
						chunk.SetBlock(posX, cursorY+1, posZ, state)
					}
				}
				placed = true
			}
		}
	}
}

func (r *Router) placeBlockPile(chunk *GeneratedChunk, random *legacyRandom, configuration pileConfiguration, x, y, z int) {
	radiusX := random.nextIntBounded(2) + 2
	radiusZ := random.nextIntBounded(2) + 2
	for dx := -radiusX; dx <= radiusX; dx++ {
		for dz := -radiusZ; dz <= radiusZ; dz++ {
			for dy := 0; dy <= 1; dy++ {
				if float32(dx*dx+dz*dz) > random.nextFloat()*10-random.nextFloat()*6 && random.nextFloat() >= 0.031 {
					continue
				}
				posX, posY, posZ := x+dx, y+dy, z+dz
				if !chunk.InRange(posX, posY, posZ) || !chunk.BlockAt(posX, posY, posZ).IsAir() {
					continue
				}
				below := chunk.BlockAt(posX, posY-1, posZ)
				if below.IsAir() || below.HasFluid() {
					continue
				}
				chunk.SetBlock(posX, posY, posZ, configuration.provider.sample(random))
			}
		}
	}
}

func (r *Router) placeVegetationPatch(chunk *GeneratedChunk, random *legacyRandom, configuration vegetationPatchConfiguration, x, y, z int) {
	radiusX := configuration.radius.sample(random) + 1
	radiusZ := configuration.radius.sample(random) + 1
	direction := 1
	if configuration.surface == "ceiling" {
		direction = -1
	}
	columns := make([]featurePosition, 0)
	for dx := -radiusX; dx <= radiusX; dx++ {
		edgeX := dx == -radiusX || dx == radiusX
		for dz := -radiusZ; dz <= radiusZ; dz++ {
			edgeZ := dz == -radiusZ || dz == radiusZ
			if edgeX && edgeZ {
				continue
			}
			if (edgeX || edgeZ) && random.nextFloat() > configuration.extraEdgeChance {
				continue
			}
			pos := featurePosition{x: x + dx, y: y, z: z + dz}
			for steps := 0; steps < configuration.verticalRange && chunk.InRange(pos.x, pos.y, pos.z) && chunk.BlockAt(pos.x, pos.y, pos.z).IsAir(); steps++ {
				pos.y += direction
			}
			for steps := 0; steps < configuration.verticalRange && chunk.InRange(pos.x, pos.y, pos.z) && !chunk.BlockAt(pos.x, pos.y, pos.z).IsAir(); steps++ {
				pos.y -= direction
			}
			if !chunk.InRange(pos.x, pos.y, pos.z) || !chunk.InRange(pos.x, pos.y+1, pos.z) {
				continue
			}
			surface := chunk.BlockAt(pos.x, pos.y, pos.z)
			if !r.vegetationReplaceable(configuration.replaceable, surface) {
				continue
			}
			depth := configuration.depth.sample(random)
			if configuration.extraBottomChance > 0 && random.nextFloat() < configuration.extraBottomChance {
				depth++
			}
			for placed := 0; placed < depth; placed++ {
				targetY := pos.y - direction*placed
				if !chunk.InRange(pos.x, targetY, pos.z) || !r.vegetationReplaceable(configuration.replaceable, chunk.BlockAt(pos.x, targetY, pos.z)) {
					break
				}
				chunk.SetBlock(pos.x, targetY, pos.z, configuration.ground.sample(random))
			}
			if configuration.vegetationChance > 0 && random.nextFloat() < configuration.vegetationChance {
				columns = append(columns, featurePosition{x: pos.x, y: pos.y + 1, z: pos.z})
			}
		}
	}
	for _, column := range columns {
		r.applyFeatureRef(chunk, random, configuration.vegetation, column.x, column.y, column.z)
	}
}

func (r *Router) vegetationReplaceable(tag string, block SurfaceBlock) bool {
	if tag == "" {
		return !block.IsAir() && !block.HasFluid()
	}
	state, err := registry.BlockStateID(block.Name)
	return err == nil && registry.BlockTagContains(strings.TrimPrefix(tag, "#"), state)
}

func (r *Router) placeLake(chunk *GeneratedChunk, random *legacyRandom, configuration lakeConfiguration, x, y, z int) {
	if y <= chunk.MinY+4 || !chunk.InRange(x, y+4, z) {
		return
	}
	y += 4
	var shape [2048]bool
	count := random.nextIntBounded(4) + 4
	for i := 0; i < count; i++ {
		d1 := random.nextDouble()*6 + 3
		d2 := random.nextDouble()*4 + 2
		d3 := random.nextDouble()*6 + 3
		x0 := random.nextDouble()*(16-d1-2) + 1 + d1/2
		y0 := random.nextDouble()*(8-d2-4) + 2 + d2/2
		z0 := random.nextDouble()*(16-d3-2) + 1 + d3/2
		for ix := 1; ix < 15; ix++ {
			for iz := 1; iz < 15; iz++ {
				for iy := 1; iy < 7; iy++ {
					dx := (float64(ix) - x0) / (d1 / 2)
					dy := (float64(iy) - y0) / (d2 / 2)
					dz := (float64(iz) - z0) / (d3 / 2)
					if dx*dx+dy*dy+dz*dz < 1 {
						shape[(ix*16+iz)*8+iy] = true
					}
				}
			}
		}
	}
	fluid := configuration.fluid.sample(random)
	barrier := configuration.barrier.sample(random)
	for ix := 0; ix < 16; ix++ {
		for iz := 0; iz < 16; iz++ {
			for iy := 0; iy < 8; iy++ {
				index := (ix*16 + iz) * 8
				if shape[index+iy] {
					continue
				}
				neighbor := (ix < 15 && shape[index+16*8+iy]) || (ix > 0 && shape[index-16*8+iy]) ||
					(iz < 15 && shape[index+8+iy]) || (iz > 0 && shape[index-8+iy]) ||
					(iy < 7 && shape[index+iy+1]) || (iy > 0 && shape[index+iy-1])
				if !neighbor {
					continue
				}
				block := chunk.BlockAt(x+ix, y+iy, z+iz)
				if iy >= 4 && block.HasFluid() {
					return
				}
				if iy < 4 || block.IsAir() || sameFeatureState(block, fluid) {
					continue
				}
				return
			}
		}
	}
	for ix := 0; ix < 16; ix++ {
		for iz := 0; iz < 16; iz++ {
			for iy := 0; iy < 8; iy++ {
				if !shape[(ix*16+iz)*8+iy] {
					continue
				}
				if iy >= 4 {
					chunk.SetBlock(x+ix, y+iy, z+iz, barrier)
				} else {
					chunk.SetBlock(x+ix, y+iy, z+iz, fluid)
				}
			}
		}
	}
}

func sameFeatureState(a, b SurfaceBlock) bool {
	if a.Name != b.Name || len(a.Properties) != len(b.Properties) {
		return false
	}
	for key, value := range a.Properties {
		if b.Properties[key] != value {
			return false
		}
	}
	return true
}

func (r *Router) placeSeagrass(chunk *GeneratedChunk, random *legacyRandom, configuration seagrassConfiguration, x, y, z int) {
	if !chunk.InRange(x, y, z) || chunk.BlockAt(x, y, z).Name != "minecraft:water" {
		return
	}
	below := chunk.BlockAt(x, y-1, z)
	if below.IsAir() || below.HasFluid() {
		return
	}
	tall := random.nextFloat() < configuration.probability
	if tall && chunk.InRange(x, y+1, z) && chunk.BlockAt(x, y+1, z).Name == "minecraft:water" {
		chunk.SetBlock(x, y, z, SurfaceBlock{Name: "minecraft:tall_seagrass", Properties: map[string]string{"half": "lower"}})
		chunk.SetBlock(x, y+1, z, SurfaceBlock{Name: "minecraft:tall_seagrass", Properties: map[string]string{"half": "upper"}})
		return
	}
	chunk.SetBlock(x, y, z, SurfaceBlock{Name: "minecraft:seagrass"})
}

func (r *Router) placeSpring(chunk *GeneratedChunk, spring springConfiguration, x, y, z int) {
	if !chunk.InRange(x, y, z) {
		return
	}
	below := chunk.BlockAt(x, y-1, z)
	if !spring.matches(below) || (spring.requiresBelow && !spring.matches(below)) {
		return
	}
	current := chunk.BlockAt(x, y, z)
	valid, holes := 0, 0
	neighbors := [...][3]int{{x + 1, y, z}, {x - 1, y, z}, {x, y, z + 1}, {x, y, z - 1}, {x, y + 1, z}}
	for _, neighbor := range neighbors {
		block := chunk.BlockAt(neighbor[0], neighbor[1], neighbor[2])
		if spring.matches(block) {
			valid++
		}
		if block.IsAir() {
			holes++
		}
	}
	if valid != spring.rockCount || holes != spring.holeCount {
		return
	}
	if !current.IsAir() && !spring.matches(current) {
		return
	}
	chunk.SetBlock(x, y, z, spring.state)
}

func (s springConfiguration) matches(block SurfaceBlock) bool {
	if _, ok := s.validBlocks[block.Name]; ok {
		return true
	}
	for key := range s.validBlocks {
		if strings.HasPrefix(key, "#") {
			if state, err := registry.BlockStateID(block.Name); err == nil && registry.BlockTagContains(strings.TrimPrefix(key, "#"), state) {
				return true
			}
		}
	}
	return false
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

func (r *Router) placePlacedAt(chunk *GeneratedChunk, random *legacyRandom, placed placedFeature, position featurePosition) {
	if position.y < chunk.MinY || position.y >= chunk.MinY+chunk.Height {
		return
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
	case placed.spring != nil:
		r.placeSpring(chunk, *placed.spring, position.x, position.y, position.z)
	case placed.seagrass != nil:
		r.placeSeagrass(chunk, random, *placed.seagrass, position.x, position.y, position.z)
	case placed.lake != nil:
		r.placeLake(chunk, random, *placed.lake, position.x, position.y, position.z)
	}
}

func (r *Router) parseConfiguredFeature(placed placedFeature, config json.RawMessage) placedFeature {
	switch placed.kind {
	case "minecraft:ore", "minecraft:scattered_ore":
		ore, err := parseOreConfiguration(config)
		if err != nil {
			placed.supported = false
			return placed
		}
		placed.ore = ore
	case "minecraft:tree":
		tree, err := parseTreeConfiguration(config)
		if err != nil || tree == nil {
			placed.supported = false
			return placed
		}
		placed.tree = tree
	case "minecraft:random_selector":
		selector, err := parseSelectorConfiguration(config)
		if err != nil || selector == nil {
			placed.supported = false
			return placed
		}
		placed.selector = selector
	case "minecraft:random_patch", "minecraft:flower":
		patch, err := parsePatchConfiguration(config)
		if err != nil || patch == nil {
			placed.supported = false
			return placed
		}
		placed.patch = patch
	case "minecraft:simple_block":
		simple, err := parseSimpleBlockConfiguration(config)
		if err != nil || simple == nil {
			placed.supported = false
			return placed
		}
		placed.simple = simple
	case "minecraft:spring_feature":
		spring, err := parseSpringConfiguration(config)
		if err != nil || spring == nil {
			placed.supported = false
			return placed
		}
		placed.spring = spring
	case "minecraft:seagrass":
		seagrass, err := parseSeagrassConfiguration(config)
		if err != nil || seagrass == nil {
			placed.supported = false
			return placed
		}
		placed.seagrass = seagrass
	case "minecraft:lake":
		lake, err := parseLakeConfiguration(config)
		if err != nil || lake == nil {
			placed.supported = false
			return placed
		}
		placed.lake = lake
	case "minecraft:vegetation_patch", "minecraft:waterlogged_vegetation_patch":
		vegetation, err := parseVegetationPatchConfiguration(config)
		if err != nil || vegetation == nil {
			placed.supported = false
			return placed
		}
		placed.vegetation = vegetation
	case "minecraft:block_pile":
		pile, err := parsePileConfiguration(config)
		if err != nil || pile == nil {
			placed.supported = false
			return placed
		}
		placed.pile = pile
	case "minecraft:disk":
		disk, err := parseDiskConfiguration(config)
		if err != nil || disk == nil {
			placed.supported = false
			return placed
		}
		placed.disk = disk
	case "minecraft:bamboo":
		bamboo, err := parseBambooConfiguration(config)
		if err != nil || bamboo == nil {
			placed.supported = false
			return placed
		}
		placed.bamboo = bamboo
	default:
		placed.supported = false
		return placed
	}
	return placed
}

func (r *Router) applyFeatureRef(chunk *GeneratedChunk, random *legacyRandom, ref *featureRef, x, y, z int) {
	if len(ref.inline) == 0 {
		if nested, ok := r.features.placed[ref.name]; ok {
			positions := []featurePosition{{x: x, y: y, z: z}}
			modifiers := append(append([]placementModifier{}, nested.placements...), ref.placements...)
			for _, modifier := range modifiers {
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
				case "minecraft:surface_water_depth_filter", "minecraft:biome":
				default:
					return
				}
			}
			for _, position := range positions {
				r.placePlacedAt(chunk, random, nested, position)
			}
			return
		}
	}
	raw := ref.inline
	var placed placedFeature
	if len(raw) == 0 {
		if cached, ok := r.configuredCache[ref.name]; ok {
			placed = cached
			placed.placements = ref.placements
		} else {
			var ok bool
			raw, ok = r.features.configured[ref.name]
			if !ok {
				return
			}
		}
	}
	if len(raw) != 0 {
		var configured struct {
			Type   string          `json:"type"`
			Config json.RawMessage `json:"config"`
		}
		if err := json.Unmarshal(raw, &configured); err != nil {
			return
		}
		placed = placedFeature{feature: ref.name, kind: qualify(configured.Type), placements: ref.placements, supported: true}
		parsed := r.parseConfiguredFeature(placed, configured.Config)
		if !parsed.supported {
			return
		}
		placed = parsed
		if len(ref.inline) == 0 {
			cached := placed
			cached.placements = nil
			r.configuredCache[ref.name] = cached
		}
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
		case placed.spring != nil:
			r.placeSpring(chunk, *placed.spring, x, y, z)
		case placed.seagrass != nil:
			r.placeSeagrass(chunk, random, *placed.seagrass, x, y, z)
		case placed.lake != nil:
			r.placeLake(chunk, random, *placed.lake, x, y, z)
		case placed.vegetation != nil:
			r.placeVegetationPatch(chunk, random, *placed.vegetation, x, y, z)
		case placed.pile != nil:
			r.placeBlockPile(chunk, random, *placed.pile, x, y, z)
		case placed.disk != nil:
			r.placeDisk(chunk, random, *placed.disk, x, y, z)
		case placed.bamboo != nil:
			r.placeBamboo(chunk, random, *placed.bamboo, x, y, z)
		case placed.kelp != nil:
			r.placeKelp(chunk, random, x, y, z)
		case placed.multiface != nil:
			r.placeMultiface(chunk, random, *placed.multiface, x, y, z)
		case placed.fossil != nil:
			r.placeFossil(chunk, random, *placed.fossil, x, y, z)
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
		case "minecraft:environment_scan":
			next := make([]featurePosition, 0, len(positions))
			for _, position := range positions {
				if modifier.allowed != nil && !r.blockPredicateMatches(chunk, modifier.allowed, position.x, position.y, position.z) {
					continue
				}
				dx, dy, dz := directionDelta(modifier.direction)
				found := false
				for step := 0; step < modifier.maxSteps; step++ {
					position.x += dx
					position.y += dy
					position.z += dz
					if !chunk.InRange(position.x, position.y, position.z) {
						break
					}
					if modifier.predicate == nil || r.blockPredicateMatches(chunk, modifier.predicate, position.x, position.y, position.z) {
						found = true
						break
					}
				}
				if found {
					next = append(next, position)
				}
			}
			positions = next
		case "minecraft:surface_relative_threshold_filter":
			filtered := positions[:0]
			for _, position := range positions {
				height := chunkHeightmap(chunk, position.x, position.z, modifier.heightmap)
				value := position.y - height
				if modifier.thresholdMin != nil && value < *modifier.thresholdMin {
					continue
				}
				if modifier.thresholdMax != nil && value > *modifier.thresholdMax {
					continue
				}
				filtered = append(filtered, position)
			}
			positions = filtered
		case "minecraft:random_offset":
			for i := range positions {
				xOffset := modifier.xzSpread.sample(random)
				zOffset := modifier.xzSpread.sample(random)
				positions[i].x += xOffset
				positions[i].z += zOffset
				positions[i].y += modifier.ySpread.sample(random)
			}
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
		case placed.spring != nil:
			r.placeSpring(chunk, *placed.spring, position.x, position.y, position.z)
		case placed.seagrass != nil:
			r.placeSeagrass(chunk, random, *placed.seagrass, position.x, position.y, position.z)
		case placed.lake != nil:
			r.placeLake(chunk, random, *placed.lake, position.x, position.y, position.z)
		case placed.vegetation != nil:
			r.placeVegetationPatch(chunk, random, *placed.vegetation, position.x, position.y, position.z)
		case placed.pile != nil:
			r.placeBlockPile(chunk, random, *placed.pile, position.x, position.y, position.z)
		case placed.disk != nil:
			r.placeDisk(chunk, random, *placed.disk, position.x, position.y, position.z)
		case placed.bamboo != nil:
			r.placeBamboo(chunk, random, *placed.bamboo, position.x, position.y, position.z)
		case placed.kelp != nil:
			r.placeKelp(chunk, random, position.x, position.y, position.z)
		case placed.multiface != nil:
			r.placeMultiface(chunk, random, *placed.multiface, position.x, position.y, position.z)
		case placed.fossil != nil:
			r.placeFossil(chunk, random, *placed.fossil, position.x, position.y, position.z)
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
	switch configuration.foliageType {
	case "minecraft:spruce_foliage_placer":
		r.placeSpruceFoliage(chunk, random, configuration, x, y, z, height)
	case "minecraft:pine_foliage_placer":
		r.placePineFoliage(chunk, random, configuration, x, y, z, height)
	default:
		r.placeBlobFoliage(chunk, random, configuration, x, y, z, height)
	}
}

func (r *Router) placeBlobFoliage(chunk *GeneratedChunk, random *legacyRandom, configuration treeConfiguration, x, y, z, height int) {
	radius := configuration.foliageRadius.sample(random)
	if radius <= 0 {
		radius = 2
	}
	foliageHeight := configuration.foliageHeight
	if foliageHeight <= 0 {
		foliageHeight = 3
	}
	top := y + height - 1
	for layer := 0; layer < foliageHeight; layer++ {
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

func (r *Router) placeSpruceFoliage(chunk *GeneratedChunk, random *legacyRandom, configuration treeConfiguration, x, y, z, height int) {
	top := y + height - 1
	start := height - configuration.foliageTrunkHeight.sample(random)
	if start < 4 {
		start = 4
	}
	baseRadius := configuration.foliageRadius.sample(random)
	radius := random.nextIntBounded(2)
	nextRadius := 1
	limit := 0
	for layerY := top; layerY >= top-start; layerY-- {
		r.placeFoliageLayer(chunk, random, configuration, x, layerY, z, radius)
		if radius >= nextRadius {
			radius = limit
			limit = 1
			if nextRadius+1 < baseRadius+1 {
				nextRadius++
			}
		} else {
			radius++
		}
	}
}

func (r *Router) placePineFoliage(chunk *GeneratedChunk, random *legacyRandom, configuration treeConfiguration, x, y, z, height int) {
	top := y + height - 1
	foliageHeight := configuration.foliageHeightRandom.sample(random)
	if foliageHeight <= 0 {
		foliageHeight = configuration.foliageHeight
	}
	if foliageHeight <= 0 {
		foliageHeight = 3
	}
	baseRadius := configuration.foliageRadius.sample(random)
	radius := 0
	for layerY := top; layerY >= top-foliageHeight; layerY-- {
		r.placeFoliageLayer(chunk, random, configuration, x, layerY, z, radius)
		if radius >= 1 && layerY == top-foliageHeight+1 {
			radius--
		} else if radius < baseRadius+1 {
			radius++
		}
	}
}

func (r *Router) placeFoliageLayer(chunk *GeneratedChunk, random *legacyRandom, configuration treeConfiguration, x, layerY, z, radius int) {
	for dx := -radius; dx <= radius; dx++ {
		for dz := -radius; dz <= radius; dz++ {
			if radius > 0 && absFeatureInt(dx) == radius && absFeatureInt(dz) == radius && random.nextIntBounded(2) == 0 {
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
