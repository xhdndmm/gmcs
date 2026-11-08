//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"

	"gmcs/internal/registry"
)

type structureDefinition struct {
	Type              string
	Biomes            string
	StartPool         string
	StartJigsawName   string
	ProjectStart      string
	TerrainAdaptation string
	Size              int
	StartHeight       structureStartHeight
}

type structureStartHeight struct {
	Absolute    *int
	AboveBottom *int
}

type structureSetDefinition struct {
	Name       string
	Spacing    int
	Separation int
	Salt       int
	Frequency  float64
	Structures []weightedStructure
}

type weightedStructure struct {
	Name   string
	Weight int
}

type structureRuntime struct {
	data   *structureData
	defs   map[string]structureDefinition
	sets   []structureSetDefinition
	pools  map[string]*templatePool
	poolMu sync.Mutex
	cache  sync.Map // structureStartKey -> *structureStart
}

type structureStartKey struct {
	seed    int64
	set     int
	originX int
	originZ int
}

type structureStart struct {
	pieces []structurePiece
}

type structurePiece struct {
	name       string
	template   *structureTemplate
	origin     [3]int
	rotation   templateRotation
	mirror     bool
	connectors []pieceConnector
}

type pieceConnector struct {
	pos       [3]int
	direction string
	name      string
	target    string
	pool      string
}

var (
	structureRuntimeOnce sync.Once
	structureRuntimeAll  *structureRuntime
	structureRuntimeErr  error
)

func loadStructureRuntime() (*structureRuntime, error) {
	structureRuntimeOnce.Do(func() {
		data, err := LoadStructureData()
		if err != nil {
			structureRuntimeErr = err
			return
		}
		runtime := &structureRuntime{
			data:  data,
			defs:  make(map[string]structureDefinition),
			pools: make(map[string]*templatePool),
		}
		for name, raw := range data.Structures {
			var encoded struct {
				Type              string          `json:"type"`
				Biomes            json.RawMessage `json:"biomes"`
				StartPool         string          `json:"start_pool"`
				StartJigsawName   string          `json:"start_jigsaw_name"`
				ProjectStart      string          `json:"project_start_to_heightmap"`
				TerrainAdaptation string          `json:"terrain_adaptation"`
				Size              int             `json:"size"`
				StartHeight       struct {
					Absolute    *int `json:"absolute"`
					AboveBottom *int `json:"above_bottom"`
				} `json:"start_height"`
			}
			if err := json.Unmarshal(raw, &encoded); err != nil {
				structureRuntimeErr = err
				return
			}
			def := structureDefinition{
				Type:              qualify(encoded.Type),
				StartPool:         qualify(encoded.StartPool),
				StartJigsawName:   encoded.StartJigsawName,
				ProjectStart:      encoded.ProjectStart,
				TerrainAdaptation: encoded.TerrainAdaptation,
				Size:              encoded.Size,
			}
			def.StartHeight = structureStartHeight{Absolute: encoded.StartHeight.Absolute, AboveBottom: encoded.StartHeight.AboveBottom}
			var biomeName string
			if json.Unmarshal(encoded.Biomes, &biomeName) == nil {
				def.Biomes = qualify(biomeName)
			}
			runtime.defs[name] = def
		}
		setNames := make([]string, 0, len(data.StructureSets))
		for name := range data.StructureSets {
			setNames = append(setNames, name)
		}
		sort.Strings(setNames)
		for _, setName := range setNames {
			raw := data.StructureSets[setName]
			var encoded struct {
				Placement struct {
					Type       string  `json:"type"`
					Spacing    int     `json:"spacing"`
					Separation int     `json:"separation"`
					Salt       int     `json:"salt"`
					Frequency  float64 `json:"frequency"`
				} `json:"placement"`
				Structures []struct {
					Structure string `json:"structure"`
					Weight    int    `json:"weight"`
				} `json:"structures"`
			}
			if err := json.Unmarshal(raw, &encoded); err != nil {
				structureRuntimeErr = err
				return
			}
			if encoded.Placement.Type != "minecraft:random_spread" {
				continue
			}
			set := structureSetDefinition{
				Name:       setName,
				Spacing:    encoded.Placement.Spacing,
				Separation: encoded.Placement.Separation,
				Salt:       encoded.Placement.Salt,
				Frequency:  encoded.Placement.Frequency,
			}
			if set.Frequency <= 0 {
				set.Frequency = 1
			}
			for _, entry := range encoded.Structures {
				set.Structures = append(set.Structures, weightedStructure{Name: qualify(entry.Structure), Weight: entry.Weight})
			}
			runtime.sets = append(runtime.sets, set)
		}
		structureRuntimeAll = runtime
	})
	return structureRuntimeAll, structureRuntimeErr
}

// applyStructures 在区块生成末尾放置结构。当前支持 jigsaw 结构；
// 要塞/矿洞/末地城的专用生成器尚未接入。
func (r *Router) applyStructures(chunk *GeneratedChunk, biomes *BiomeSource) {
	runtime, err := loadStructureRuntime()
	if err != nil {
		return
	}
	for setIndex, set := range runtime.sets {
		if set.Spacing <= 0 || len(set.Structures) == 0 {
			continue
		}
		radius := 8
		startCellX := floorDiv(chunk.ChunkX-radius, set.Spacing)
		endCellX := floorDiv(chunk.ChunkX+radius, set.Spacing)
		startCellZ := floorDiv(chunk.ChunkZ-radius, set.Spacing)
		endCellZ := floorDiv(chunk.ChunkZ+radius, set.Spacing)
		for cellX := startCellX; cellX <= endCellX; cellX++ {
			for cellZ := startCellZ; cellZ <= endCellZ; cellZ++ {
				originX, originZ := randomSpreadOrigin(r.seed, set, cellX, cellZ)
				if originX < chunk.ChunkX-radius || originX > chunk.ChunkX+radius ||
					originZ < chunk.ChunkZ-radius || originZ > chunk.ChunkZ+radius {
					continue
				}
				start := runtime.cachedStart(r.seed, setIndex, originX, originZ, biomes)
				if start == nil {
					continue
				}
				for _, piece := range start.pieces {
					placeTemplate(chunk, piece.template, piece.origin[0], piece.origin[1], piece.origin[2], piece.rotation, piece.mirror)
				}
			}
		}
	}
}

func randomSpreadOrigin(seed int64, set structureSetDefinition, cellX, cellZ int) (int, int) {
	rangeSize := set.Spacing - set.Separation
	if rangeSize <= 0 {
		rangeSize = 1
	}
	random := newLegacyRandom(seed + int64(set.Salt) + int64(cellX)*341873128712 + int64(cellZ)*132897987541)
	x := cellX*set.Spacing + random.nextIntBounded(rangeSize)
	z := cellZ*set.Spacing + random.nextIntBounded(rangeSize)
	return x, z
}

func (r *structureRuntime) cachedStart(seed int64, setIndex, originX, originZ int, biomes *BiomeSource) *structureStart {
	key := structureStartKey{seed: seed, set: setIndex, originX: originX, originZ: originZ}
	if cached, ok := r.cache.Load(key); ok {
		return cached.(*structureStart)
	}
	// 先占位，避免并发生成时重复大量工作；简单起见直接构建并覆盖。
	start := r.buildStart(seed, setIndex, originX, originZ, biomes)
	if start == nil {
		return nil
	}
	r.cache.Store(key, start)
	return start
}

func (r *structureRuntime) buildStart(seed int64, setIndex, originX, originZ int, biomes *BiomeSource) *structureStart {
	set := r.sets[setIndex]
	random := newLegacyRandom(seed + int64(set.Salt) + int64(originX)*17 + int64(originZ)*31)
	choice := chooseWeightedStructure(set.Structures, random)
	if choice == "" {
		return nil
	}
	def, ok := r.defs[choice]
	if !ok || def.Type != "minecraft:jigsaw" || def.StartPool == "" {
		return nil
	}
	if !r.biomeAllowed(def, biomes, originX*16+8, originZ*16+8) {
		return nil
	}
	pool := def.StartPool
	element, ok := r.choosePoolElement(pool, random)
	if !ok {
		return nil
	}
	template, err := r.data.ParsedTemplate(element.Location)
	if err != nil {
		return nil
	}
	rotation := templateRotation(random.nextIntBounded(4))
	mirror := random.nextIntBounded(2) == 0
	startX := originX*16 + 8
	startZ := originZ*16 + 8
	startY := r.startHeight(def, startX, startZ)
	if startY < -64 {
		startY = -64
	}
	piece := makePiece(r, element.Location, template, startX, startY, startZ, rotation, mirror, element.Processors, random)
	start := &structureStart{pieces: []structurePiece{piece}}
	center := [3]int{startX, startY, startZ}
	queue := []structurePiece{piece}
	depth := 0
	for len(queue) > 0 && depth < def.Size {
		depth++
		next := make([]structurePiece, 0)
		for _, current := range queue {
			for _, connector := range current.connectors {
				if connector.pool == "" || connector.pool == "minecraft:empty" {
					continue
				}
				targetElement, ok := r.choosePoolElement(connector.pool, random)
				if !ok {
					continue
				}
				targetTemplate, err := r.data.ParsedTemplate(targetElement.Location)
				if err != nil {
					continue
				}
				targetPiece, ok := alignPiece(r, current, connector, targetTemplate, targetElement.Location, targetElement.Processors, start, center, def, random)
				if !ok {
					continue
				}
				start.pieces = append(start.pieces, targetPiece)
				next = append(next, targetPiece)
			}
		}
		queue = next
	}
	return start
}

func makePiece(runtime *structureRuntime, name string, template *structureTemplate, originX, originY, originZ int, rotation templateRotation, mirror bool, processors string, random *legacyRandom) structurePiece {
	template = runtime.applyProcessorList(template, processors, random)
	piece := structurePiece{name: name, template: template, origin: [3]int{originX, originY, originZ}, rotation: rotation, mirror: mirror}
	for _, jigsaw := range template.jigsawBlocks() {
		pos := transformTemplatePos(jigsaw.Pos, template.Size, rotation, mirror)
		piece.connectors = append(piece.connectors, pieceConnector{
			pos:       [3]int{originX + pos[0], originY + pos[1], originZ + pos[2]},
			direction: rotateDirection(jigsaw.Direction, rotation, mirror),
			name:      jigsaw.Name,
			target:    jigsaw.Target,
			pool:      jigsaw.Pool,
		})
	}
	return piece
}

func alignPiece(runtime *structureRuntime, source structurePiece, connector pieceConnector, target *structureTemplate, name, processors string, start *structureStart, center [3]int, def structureDefinition, random *legacyRandom) (structurePiece, bool) {
	targetJigsaws := target.jigsawBlocks()
	if len(targetJigsaws) == 0 {
		return structurePiece{}, false
	}
	sourceDir := directionVector(connector.direction)
	for attempt := 0; attempt < 8; attempt++ {
		rotation := templateRotation(random.nextIntBounded(4))
		mirror := random.nextIntBounded(2) == 0
		for _, targetJigsaw := range targetJigsaws {
			if connector.target != "" && targetJigsaw.Name != connector.target {
				continue
			}
			targetPos := transformTemplatePos(targetJigsaw.Pos, target.Size, rotation, mirror)
			origin := [3]int{
				connector.pos[0] + sourceDir[0] - targetPos[0],
				connector.pos[1] + sourceDir[1] - targetPos[1],
				connector.pos[2] + sourceDir[2] - targetPos[2],
			}
			piece := makePiece(runtime, name, target, origin[0], origin[1], origin[2], rotation, mirror, processors, random)
			size := transformedSize(target.Size, rotation)
			if !withinCenter(origin, size, center, def) {
				continue
			}
			if overlapsAny(piece, start.pieces) {
				continue
			}
			return piece, true
		}
	}
	return structurePiece{}, false
}

func withinCenter(origin [3]int, size [3]int, center [3]int, def structureDefinition) bool {
	dx := origin[0] + size[0]/2 - center[0]
	dz := origin[2] + size[2]/2 - center[2]
	limit := 80
	if def.Size > 0 {
		limit = def.Size * 16
	}
	return dx*dx+dz*dz <= limit*limit
}

func overlapsAny(piece structurePiece, pieces []structurePiece) bool {
	size := transformedSize(piece.template.Size, piece.rotation)
	for _, other := range pieces {
		otherSize := transformedSize(other.template.Size, other.rotation)
		if piece.origin[0] < other.origin[0]+otherSize[0] && piece.origin[0]+size[0] > other.origin[0] &&
			piece.origin[1] < other.origin[1]+otherSize[1] && piece.origin[1]+size[1] > other.origin[1] &&
			piece.origin[2] < other.origin[2]+otherSize[2] && piece.origin[2]+size[2] > other.origin[2] {
			return true
		}
	}
	return false
}

func (r *structureRuntime) startHeight(def structureDefinition, x, z int) int {
	if def.ProjectStart != "" {
		// 当前区块生成路径没有邻区块高度图；以海平面附近作为近似起点。
		return 64
	}
	if def.StartHeight.Absolute != nil {
		return *def.StartHeight.Absolute
	}
	if def.StartHeight.AboveBottom != nil {
		return *def.StartHeight.AboveBottom
	}
	return 64
}

func (r *structureRuntime) biomeAllowed(def structureDefinition, biomes *BiomeSource, x, z int) bool {
	if def.Biomes == "" {
		return true
	}
	name := biomes.BiomeAtBlock(x, 64, z)
	if strings.HasPrefix(def.Biomes, "#") {
		id, ok := registry.SyncEntryID("minecraft:worldgen/biome", name)
		if !ok {
			return false
		}
		return registry.WorldgenBiomeInTag(strings.TrimPrefix(def.Biomes, "#"), id)
	}
	return def.Biomes == name
}

func chooseWeightedStructure(entries []weightedStructure, random *legacyRandom) string {
	total := 0
	for _, entry := range entries {
		total += entry.Weight
	}
	if total <= 0 {
		return ""
	}
	value := random.nextIntBounded(total)
	for _, entry := range entries {
		if value < entry.Weight {
			return entry.Name
		}
		value -= entry.Weight
	}
	return ""
}

func (r *structureRuntime) choosePoolElement(name string, random *legacyRandom) (templatePoolElementDef, bool) {
	pool, err := r.pool(name)
	if err != nil || pool == nil || len(pool.Elements) == 0 {
		return templatePoolElementDef{}, false
	}
	total := 0
	for _, element := range pool.Elements {
		total += element.Weight
	}
	if total <= 0 {
		return templatePoolElementDef{}, false
	}
	value := random.nextIntBounded(total)
	for _, element := range pool.Elements {
		if value < element.Weight {
			return element.Element, true
		}
		value -= element.Weight
	}
	return templatePoolElementDef{}, false
}

func (r *structureRuntime) pool(name string) (*templatePool, error) {
	name = qualify(name)
	r.poolMu.Lock()
	if pool, ok := r.pools[name]; ok {
		r.poolMu.Unlock()
		return pool, nil
	}
	r.poolMu.Unlock()
	raw, ok := r.data.TemplatePool(name)
	if !ok {
		return nil, nil
	}
	pool, err := parseTemplatePool(raw)
	if err != nil {
		return nil, err
	}
	r.poolMu.Lock()
	if existing, ok := r.pools[name]; ok {
		r.poolMu.Unlock()
		return existing, nil
	}
	r.pools[name] = pool
	r.poolMu.Unlock()
	return pool, nil
}

func rotateDirection(direction string, rotation templateRotation, mirror bool) string {
	index := map[string]int{"north": 0, "east": 1, "south": 2, "west": 3}[direction]
	if mirror {
		index = (4 - index) % 4
	}
	index = (index + int(rotation)) % 4
	return []string{"north", "east", "south", "west"}[(index+4)%4]
}

func directionVector(direction string) [3]int {
	switch direction {
	case "north":
		return [3]int{0, 0, -1}
	case "south":
		return [3]int{0, 0, 1}
	case "east":
		return [3]int{1, 0, 0}
	case "west":
		return [3]int{-1, 0, 0}
	case "up":
		return [3]int{0, 1, 0}
	case "down":
		return [3]int{0, -1, 0}
	default:
		return [3]int{0, 0, 0}
	}
}

// 保持 registry 导入用于后续群系标签过滤；当前 biomeAllowed 仍为占位。
var _ = registry.WorldgenBiomeInTag
var _ = sort.Ints
var _ = strings.TrimPrefix
