//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"encoding/json"
	"math"
	"strings"

	"gmcs/internal/registry"
)

type geodeConfiguration struct {
	filling          stateProvider
	inner            stateProvider
	alternate        stateProvider
	middle           stateProvider
	outer            stateProvider
	placements       []SurfaceBlock
	cannotReplace    string
	invalid          string
	layers           [4]float64 // filling, inner, middle, outer
	crackChance      float64
	crackSize        float64
	crackOffset      int
	outerDistance    intProvider
	distribution     intProvider
	pointOffset      intProvider
	minOffset        int
	maxOffset        int
	noiseScale       float64
	invalidLimit     int
	alternateChance  float64
	placementChance  float64
	requireAlternate bool
}

func parseGeodeConfiguration(raw json.RawMessage) (*geodeConfiguration, error) {
	var encoded struct {
		Blocks struct {
			Filling         json.RawMessage   `json:"filling_provider"`
			Inner           json.RawMessage   `json:"inner_layer_provider"`
			Alternate       json.RawMessage   `json:"alternate_inner_layer_provider"`
			Middle          json.RawMessage   `json:"middle_layer_provider"`
			Outer           json.RawMessage   `json:"outer_layer_provider"`
			InnerPlacements []json.RawMessage `json:"inner_placements"`
			CannotReplace   string            `json:"cannot_replace"`
			Invalid         string            `json:"invalid_blocks"`
		} `json:"blocks"`
		Crack struct {
			Chance float64 `json:"generate_crack_chance"`
			Size   float64 `json:"base_crack_size"`
			Offset int     `json:"crack_point_offset"`
		} `json:"crack"`
		Layers struct {
			Filling float64 `json:"filling"`
			Inner   float64 `json:"inner_layer"`
			Middle  float64 `json:"middle_layer"`
			Outer   float64 `json:"outer_layer"`
		} `json:"layers"`
		OuterDistance    json.RawMessage `json:"outer_wall_distance"`
		Distribution     json.RawMessage `json:"distribution_points"`
		PointOffset      json.RawMessage `json:"point_offset"`
		MinOffset        int             `json:"min_gen_offset"`
		MaxOffset        int             `json:"max_gen_offset"`
		NoiseScale       float64         `json:"noise_multiplier"`
		InvalidLimit     int             `json:"invalid_blocks_threshold"`
		AlternateChance  float64         `json:"use_alternate_layer0_chance"`
		PlacementChance  float64         `json:"use_potential_placements_chance"`
		RequireAlternate bool            `json:"placements_require_layer0_alternate"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, err
	}
	cfg := &geodeConfiguration{
		cannotReplace:    encoded.Blocks.CannotReplace,
		invalid:          encoded.Blocks.Invalid,
		layers:           [4]float64{encoded.Layers.Filling, encoded.Layers.Inner, encoded.Layers.Middle, encoded.Layers.Outer},
		crackChance:      encoded.Crack.Chance,
		crackSize:        encoded.Crack.Size,
		crackOffset:      encoded.Crack.Offset,
		minOffset:        encoded.MinOffset,
		maxOffset:        encoded.MaxOffset,
		noiseScale:       encoded.NoiseScale,
		invalidLimit:     encoded.InvalidLimit,
		alternateChance:  encoded.AlternateChance,
		placementChance:  encoded.PlacementChance,
		requireAlternate: encoded.RequireAlternate,
	}
	var err error
	if cfg.filling, err = parseStateProvider(encoded.Blocks.Filling); err != nil {
		return nil, err
	}
	if cfg.inner, err = parseStateProvider(encoded.Blocks.Inner); err != nil {
		return nil, err
	}
	if cfg.alternate, err = parseStateProvider(encoded.Blocks.Alternate); err != nil {
		return nil, err
	}
	if cfg.middle, err = parseStateProvider(encoded.Blocks.Middle); err != nil {
		return nil, err
	}
	if cfg.outer, err = parseStateProvider(encoded.Blocks.Outer); err != nil {
		return nil, err
	}
	for _, rawPlacement := range encoded.Blocks.InnerPlacements {
		block, err := parseFeatureSurfaceBlock(rawPlacement)
		if err != nil {
			return nil, err
		}
		cfg.placements = append(cfg.placements, block)
	}
	if cfg.outerDistance, err = parseIntProvider(encoded.OuterDistance); err != nil {
		return nil, err
	}
	if cfg.distribution, err = parseIntProvider(encoded.Distribution); err != nil {
		return nil, err
	}
	if cfg.pointOffset, err = parseIntProvider(encoded.PointOffset); err != nil {
		return nil, err
	}
	return cfg, nil
}

func geodeTagMatches(tag string, block SurfaceBlock) bool {
	tag = strings.TrimPrefix(tag, "#")
	if tag == "" {
		return false
	}
	state, err := registry.BlockStateID(block.Name)
	return err == nil && registry.BlockTagContains(tag, state)
}

type geodePoint struct {
	x, y, z int
	offset  int
}

func (r *Router) placeGeodeExact(chunk *GeneratedChunk, random *legacyRandom, cfg geodeConfiguration, x, y, z int) {
	// 原版 GeodeFeature 使用独立的 LegacyRandomSource 构造 -4..0 的 NormalNoise。
	noiseRandom := newLegacyRandom(r.seed)
	noise := NewNormalNoiseLegacySource(noiseRandom, -4, []float64{1})
	size := cfg.outerDistance.sample(random)
	if size <= 0 {
		return
	}
	points := make([]geodePoint, 0, size)
	invalid := 0
	for i := 0; i < size; i++ {
		px := x + cfg.outerDistance.sample(random)
		py := y + cfg.outerDistance.sample(random)
		pz := z + cfg.outerDistance.sample(random)
		block := chunk.BlockAt(px, py, pz)
		if block.IsAir() || geodeTagMatches(cfg.invalid, block) {
			invalid++
			if invalid > cfg.invalidLimit {
				return
			}
		}
		points = append(points, geodePoint{x: px, y: py, z: pz, offset: cfg.pointOffset.sample(random)})
	}
	crackPoints := make([]geodePoint, 0)
	crack := random.nextDouble() < cfg.crackChance
	if crack {
		side := random.nextIntBounded(4)
		span := size*2 + 1
		for _, dy := range []int{7, 5, 1} {
			switch side {
			case 0:
				crackPoints = append(crackPoints, geodePoint{x: x + span, y: y + dy, z: z})
			case 1:
				crackPoints = append(crackPoints, geodePoint{x: x, y: y + dy, z: z + span})
			case 2:
				crackPoints = append(crackPoints, geodePoint{x: x + span, y: y + dy, z: z + span})
			default:
				crackPoints = append(crackPoints, geodePoint{x: x, y: y + dy, z: z})
			}
		}
	}
	// 原版 threshold 基于 outer_wall_distance 的最大值，而不是本次采样值。
	outerMax := cfg.outerDistance.maximum
	if outerMax <= 0 {
		outerMax = size
	}
	scaled := float64(size) / float64(outerMax)
	fillingThreshold := 1 / math.Sqrt(cfg.layers[0])
	innerThreshold := 1 / math.Sqrt(cfg.layers[1]+scaled)
	middleThreshold := 1 / math.Sqrt(cfg.layers[2]+scaled)
	outerThreshold := 1 / math.Sqrt(cfg.layers[3]+scaled)
	crackThreshold := 1 / math.Sqrt(cfg.crackSize+random.nextDouble()/2+boolFloat(size > 3, scaled))
	placementCandidates := make([]geodePoint, 0)
	for px := x + cfg.minOffset; px <= x+cfg.maxOffset; px++ {
		for py := y + cfg.minOffset; py <= y+cfg.maxOffset; py++ {
			for pz := z + cfg.minOffset; pz <= z+cfg.maxOffset; pz++ {
				if !chunk.InRange(px, py, pz) {
					continue
				}
				noiseValue := noise.Value(float64(px), float64(py), float64(pz)) * cfg.noiseScale
				distance, crackDistance := 0.0, 0.0
				for _, point := range points {
					distance += 1/math.Sqrt(sq(px-point.x)+sq(py-point.y)+sq(pz-point.z)+float64(point.offset)) + noiseValue
				}
				for _, point := range crackPoints {
					crackDistance += 1/math.Sqrt(sq(px-point.x)+sq(py-point.y)+sq(pz-point.z)+float64(cfg.crackOffset)) + noiseValue
				}
				if distance < outerThreshold {
					continue
				}
				block := chunk.BlockAt(px, py, pz)
				if geodeTagMatches(cfg.cannotReplace, block) {
					continue
				}
				if crack && crackDistance >= crackThreshold && distance < fillingThreshold {
					chunk.SetBlock(px, py, pz, cfg.filling.sample(random))
					continue
				}
				switch {
				case distance >= fillingThreshold:
					chunk.SetBlock(px, py, pz, cfg.filling.sample(random))
				case distance >= innerThreshold:
					alternate := random.nextDouble() < cfg.alternateChance
					state := cfg.inner
					if alternate {
						state = cfg.alternate
					}
					chunk.SetBlock(px, py, pz, state.sample(random))
					if (!cfg.requireAlternate || alternate) && random.nextDouble() < cfg.placementChance {
						placementCandidates = append(placementCandidates, geodePoint{x: px, y: py, z: pz})
					}
				case distance >= middleThreshold:
					chunk.SetBlock(px, py, pz, cfg.middle.sample(random))
				default:
					chunk.SetBlock(px, py, pz, cfg.outer.sample(random))
				}
			}
		}
	}
	for _, point := range placementCandidates {
		if len(cfg.placements) == 0 {
			break
		}
		state := cfg.placements[random.nextIntBounded(len(cfg.placements))]
		for _, direction := range [][3]int{{0, 1, 0}, {0, -1, 0}, {0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0}} {
			px, py, pz := point.x+direction[0], point.y+direction[1], point.z+direction[2]
			if chunk.InRange(px, py, pz) && !chunk.BlockAt(px, py, pz).IsAir() {
				state.Properties = copyProperties(state.Properties)
				if state.Properties == nil {
					state.Properties = map[string]string{}
				}
				switch {
				case direction[1] == 1:
					state.Properties["facing"] = "up"
				case direction[1] == -1:
					state.Properties["facing"] = "down"
				case direction[2] == -1:
					state.Properties["facing"] = "north"
				case direction[2] == 1:
					state.Properties["facing"] = "south"
				case direction[0] == -1:
					state.Properties["facing"] = "west"
				default:
					state.Properties["facing"] = "east"
				}
				chunk.SetBlock(point.x, point.y, point.z, state)
				break
			}
		}
	}
}

func sq(v int) float64 { return float64(v) * float64(v) }
func boolFloat(ok bool, value float64) float64 {
	if ok {
		return value
	}
	return 0
}
func copyProperties(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	output := make(map[string]string, len(input))
	for k, v := range input {
		output[k] = v
	}
	return output
}
