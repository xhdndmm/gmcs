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
			var biomeNames []string
			if json.Unmarshal(encoded.Biomes, &biomeNames) == nil {
				// 原版结构 biomes 可以是单个标签或列表；列表时要求命中任意一个。
				parts := make([]string, 0, len(biomeNames))
				for _, name := range biomeNames {
					parts = append(parts, qualify(name))
				}
				def.Biomes = strings.Join(parts, "|")
			} else {
				var biomeName string
				if json.Unmarshal(encoded.Biomes, &biomeName) == nil {
					def.Biomes = qualify(biomeName)
				}
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
	if !r.structuresEnabled {
		return
	}
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
				start := runtime.cachedStart(r.seed, setIndex, originX, originZ, biomes, r)
				if start == nil {
					continue
				}
				for _, piece := range start.pieces {
					random := newLegacyRandom(r.seed + int64(piece.origin[0])*31 + int64(piece.origin[1])*17 + int64(piece.origin[2])*13)
					placeTemplate(chunk, piece.template, piece.origin[0], piece.origin[1], piece.origin[2], piece.rotation, piece.mirror, random)
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

func (r *structureRuntime) cachedStart(seed int64, setIndex, originX, originZ int, biomes *BiomeSource, router *Router) *structureStart {
	key := structureStartKey{seed: seed, set: setIndex, originX: originX, originZ: originZ}
	if cached, ok := r.cache.Load(key); ok {
		return cached.(*structureStart)
	}
	// 先占位，避免并发生成时重复大量工作；简单起见直接构建并覆盖。
	start := r.buildStart(seed, setIndex, originX, originZ, biomes, router)
	if start == nil {
		return nil
	}
	r.cache.Store(key, start)
	return start
}

func (r *structureRuntime) buildStart(seed int64, setIndex, originX, originZ int, biomes *BiomeSource, router *Router) *structureStart {
	set := r.sets[setIndex]
	random := newLegacyRandom(seed + int64(set.Salt) + int64(originX)*17 + int64(originZ)*31)
	choice := chooseWeightedStructure(set.Structures, random)
	if choice == "" {
		return nil
	}
	def, ok := r.defs[choice]
	if !ok || !r.biomeAllowed(def, biomes, originX*16+8, originZ*16+8) {
		return nil
	}
	if def.Type == "minecraft:end_city" {
		return r.buildEndCity(seed, originX, originZ, router)
	}
	if def.Type != "minecraft:jigsaw" || def.StartPool == "" {
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
	startY := r.startHeight(router, def, startX, startZ)
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

func (r *structureRuntime) buildEndCity(seed int64, originX, originZ int, router *Router) *structureStart {
	// 复刻 EndCityPieces 的基础生成器：基础房间 -> 二/三层 -> 顶，
	// 然后递归生成塔楼/桥梁/胖塔；船只在桥生成器内按原版概率出现。
	random := newLegacyRandom(seed + int64(originX)*17 + int64(originZ)*31)
	rotation := templateRotation(random.nextIntBounded(4))
	startX, startZ := originX*16+8, originZ*16+8
	startY := r.startHeight(router, structureDefinition{Type: "minecraft:end_city", ProjectStart: "WORLD_SURFACE_WG"}, startX, startZ)
	if startY < 60 {
		return nil
	}
	start := &structureStart{}
	add := func(name string, parent *structurePiece, offset [3]int, rot templateRotation) *structurePiece {
		template, err := r.data.ParsedTemplate(name)
		if err != nil || template == nil {
			return nil
		}
		origin := [3]int{startX, startY, startZ}
		if parent != nil {
			dx, dz := rotateHorizontalOffset(offset[0], offset[2], rot)
			origin = [3]int{parent.origin[0] + dx, parent.origin[1] + offset[1], parent.origin[2] + dz}
		}
		piece := makePiece(r, name, template, origin[0], origin[1], origin[2], rot, false, "", random)
		start.pieces = append(start.pieces, piece)
		return &start.pieces[len(start.pieces)-1]
	}
	base := add("minecraft:end_city/base_floor", nil, [3]int{}, rotation)
	second := add("minecraft:end_city/second_floor_1", base, [3]int{-1, 0, -1}, rotation)
	third := add("minecraft:end_city/third_floor_1", second, [3]int{-1, 4, -1}, rotation)
	top := add("minecraft:end_city/third_roof", third, [3]int{-1, 8, -1}, rotation)
	if top == nil {
		return nil
	}
	// TOWER_GENERATOR；原版 tower generator 本身会继续递归。
	r.endCityTower(start, add, top, rotation, 1, random)
	return start
}

func (r *structureRuntime) endCityTower(start *structureStart, add func(string, *structurePiece, [3]int, templateRotation) *structurePiece, parent *structurePiece, rotation templateRotation, depth int, random *legacyRandom) {
	if parent == nil || depth > 8 {
		return
	}
	base := add("minecraft:end_city/tower_base", parent, [3]int{3 + random.nextIntBounded(2), -3, 3 + random.nextIntBounded(2)}, rotation)
	if base == nil {
		return
	}
	piece := add("minecraft:end_city/tower_piece", base, [3]int{0, 7, 0}, rotation)
	if piece == nil {
		return
	}
	branch := (*structurePiece)(nil)
	if random.nextIntBounded(3) == 0 {
		branch = piece
	}
	pieces := 1 + random.nextIntBounded(3)
	for i := 0; i < pieces; i++ {
		piece = add("minecraft:end_city/tower_piece", piece, [3]int{0, 4, 0}, rotation)
		if piece == nil {
			return
		}
		if i < pieces-1 && random.nextBoolean() {
			branch = piece
		}
	}
	if branch != nil {
		for _, direction := range []struct {
			offset [3]int
			rot    templateRotation
		}{
			{[3]int{1, -1, 0}, rotateNone},
			{[3]int{6, -1, 1}, rotateClockwise90},
			{[3]int{0, -1, 5}, rotate180},
			{[3]int{5, -1, 6}, rotateCounterClockwise90},
		} {
			if !random.nextBoolean() {
				continue
			}
			end := add("minecraft:end_city/bridge_end", branch, direction.offset, rotation)
			if end == nil {
				continue
			}
			r.endCityBridge(start, add, end, rotation, depth+1, random)
		}
		piece = add("minecraft:end_city/tower_top", piece, [3]int{-1, 4, -1}, rotation)
	} else if depth == 7 {
		piece = add("minecraft:end_city/tower_top", piece, [3]int{-1, 4, -1}, rotation)
	} else {
		r.endCityFatTower(start, add, piece, rotation, depth+1, random)
	}
	_ = piece
}

func (r *structureRuntime) endCityBridge(start *structureStart, add func(string, *structurePiece, [3]int, templateRotation) *structurePiece, parent *structurePiece, rotation templateRotation, depth int, random *legacyRandom) {
	if parent == nil || depth > 8 {
		return
	}
	length := random.nextIntBounded(4) + 1
	piece := add("minecraft:end_city/bridge_piece", parent, [3]int{0, 0, -4}, rotation)
	if piece == nil {
		return
	}
	verticalOffset := 0
	for i := 0; i < length; i++ {
		if random.nextBoolean() {
			piece = add("minecraft:end_city/bridge_piece", piece, [3]int{0, verticalOffset, -4}, rotation)
			verticalOffset = 0
			continue
		}
		if random.nextBoolean() {
			piece = add("minecraft:end_city/bridge_steep_stairs", piece, [3]int{0, verticalOffset, -4}, rotation)
		} else {
			piece = add("minecraft:end_city/bridge_gentle_stairs", piece, [3]int{0, verticalOffset, -8}, rotation)
		}
		verticalOffset = 4
		if piece == nil {
			return
		}
	}
	if piece == nil {
		return
	}
	if random.nextIntBounded(maximum(1, 10-depth)) != 0 {
		r.endCityTower(start, add, piece, rotation, depth+1, random)
	} else {
		add("minecraft:end_city/ship", piece, [3]int{-8 + random.nextIntBounded(8), verticalOffset, -70 + random.nextIntBounded(10)}, rotation)
	}
	add("minecraft:end_city/bridge_end", piece, [3]int{4, verticalOffset, 0}, rotation)
}

func (r *structureRuntime) endCityFatTower(start *structureStart, add func(string, *structurePiece, [3]int, templateRotation) *structurePiece, parent *structurePiece, rotation templateRotation, depth int, random *legacyRandom) {
	if parent == nil || depth > 8 {
		return
	}
	piece := add("minecraft:end_city/fat_tower_base", parent, [3]int{-3, 4, -3}, rotation)
	piece = add("minecraft:end_city/fat_tower_middle", piece, [3]int{0, 4, 0}, rotation)
	for i := 0; i < 2 && random.nextIntBounded(3) != 0; i++ {
		piece = add("minecraft:end_city/fat_tower_middle", piece, [3]int{0, 8, 0}, rotation)
		if piece == nil {
			return
		}
		for _, direction := range []struct {
			offset [3]int
			rot    templateRotation
		}{
			{[3]int{4, -1, 0}, rotateNone},
			{[3]int{12, -1, 4}, rotateClockwise90},
			{[3]int{0, -1, 8}, rotate180},
			{[3]int{8, -1, 12}, rotateCounterClockwise90},
		} {
			if !random.nextBoolean() {
				continue
			}
			end := add("minecraft:end_city/bridge_end", piece, direction.offset, rotation)
			if end != nil {
				r.endCityBridge(start, add, end, rotation, depth+1, random)
			}
		}
	}
	add("minecraft:end_city/fat_tower_top", piece, [3]int{-2, 8, -2}, rotation)
}

func maximum(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func rotateHorizontalOffset(x, z int, rotation templateRotation) (int, int) {
	switch rotation {
	case rotateClockwise90:
		return -z, x
	case rotate180:
		return -x, -z
	case rotateCounterClockwise90:
		return z, -x
	default:
		return x, z
	}
}

func (r *structureRuntime) startHeight(router *Router, def structureDefinition, x, z int) int {
	if def.ProjectStart != "" || def.Type == "minecraft:end_city" {
		key := chunkCoord{x: x >> 4, z: z >> 4}
		chunk, ok := router.structureTerrain[key]
		if !ok {
			var err error
			chunk, err = router.generateChunkInternal(key.x, key.z, false)
			if err != nil {
				return 64
			}
			router.structureTerrain[key] = chunk
		}
		heightmap := def.ProjectStart
		if heightmap == "" {
			heightmap = "WORLD_SURFACE_WG"
		}
		return chunkHeightmap(chunk, x, z, heightmap) + 1
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
	for _, entry := range strings.Split(def.Biomes, "|") {
		if strings.HasPrefix(entry, "#") {
			id, ok := registry.SyncEntryID("minecraft:worldgen/biome", name)
			if ok && registry.WorldgenBiomeInTag(strings.TrimPrefix(entry, "#"), id) {
				return true
			}
			continue
		}
		if entry == name {
			return true
		}
	}
	return false
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
