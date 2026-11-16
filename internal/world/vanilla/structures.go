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
	Type                  string
	Biomes                string
	StartPool             string
	StartJigsawName       string
	ProjectStart          string
	TerrainAdaptation     string
	MineshaftType         string
	Size                  int
	MaxDistanceFromCenter int
	StartHeight           structureStartHeight
}

type structureStartHeight struct {
	Absolute    *int
	AboveBottom *int
}

type structureSetDefinition struct {
	Name            string
	Spacing         int
	Separation      int
	Salt            int
	Frequency       float64
	FrequencyMethod string
	PlacementType   string
	Count           int
	Distance        int
	Spread          int
	PreferredBiomes string
	Structures      []weightedStructure
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
	// cache 只缓存最近展开的 structure start。结构 start 可能包含矿洞/要塞的
	// 数千个方块，若按探索范围无限保留会持续占用内存；磁盘持久化仍负责
	// 跨重启一致性，缓存只是热路径加速。
	cacheMu    sync.Mutex
	cache      map[structureStartKey]*structureStart
	cacheOrder []structureStartKey
	ringMu     sync.Mutex
	rings      map[int][][2]int
	storeMu    sync.Mutex
	store      *structureStartStore
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
	name              string
	template          *structureTemplate
	templateName      string
	processors        string
	origin            [3]int
	rotation          templateRotation
	mirror            bool
	connectors        []pieceConnector
	terrainAdaptation string
	// 专用生成器（矿洞/要塞）直接提供绝对坐标方块，template 为 nil。
	size   [3]int
	blocks []structureBlockPlacement
}

type structureBlockPlacement struct {
	pos    [3]int
	state  SurfaceBlock
	entity *GeneratedBlockEntity
}

type pieceConnector struct {
	pos               [3]int
	direction         string
	name              string
	target            string
	pool              string
	selectionPriority int
	placementPriority int
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
			cache: make(map[structureStartKey]*structureStart),
		}
		for name, raw := range data.Structures {
			var encoded struct {
				Type              string          `json:"type"`
				Biomes            json.RawMessage `json:"biomes"`
				StartPool         string          `json:"start_pool"`
				StartJigsawName   string          `json:"start_jigsaw_name"`
				ProjectStart      string          `json:"project_start_to_heightmap"`
				TerrainAdaptation string          `json:"terrain_adaptation"`
				MineshaftType     string          `json:"mineshaft_type"`
				Size              int             `json:"size"`
				MaxDistance       int             `json:"max_distance_from_center"`
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
				Type:                  qualify(encoded.Type),
				StartPool:             qualify(encoded.StartPool),
				StartJigsawName:       encoded.StartJigsawName,
				ProjectStart:          encoded.ProjectStart,
				TerrainAdaptation:     encoded.TerrainAdaptation,
				MineshaftType:         encoded.MineshaftType,
				Size:                  encoded.Size,
				MaxDistanceFromCenter: encoded.MaxDistance,
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
					Type            string          `json:"type"`
					Spacing         int             `json:"spacing"`
					Separation      int             `json:"separation"`
					Salt            int             `json:"salt"`
					Frequency       float64         `json:"frequency"`
					FrequencyMethod string          `json:"frequency_reduction_method"`
					Count           int             `json:"count"`
					Distance        int             `json:"distance"`
					Spread          int             `json:"spread"`
					PreferredBiomes json.RawMessage `json:"preferred_biomes"`
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
			if encoded.Placement.Type != "minecraft:random_spread" && encoded.Placement.Type != "minecraft:concentric_rings" {
				continue
			}
			set := structureSetDefinition{
				Name:            setName,
				Spacing:         encoded.Placement.Spacing,
				Separation:      encoded.Placement.Separation,
				Salt:            encoded.Placement.Salt,
				Frequency:       encoded.Placement.Frequency,
				FrequencyMethod: encoded.Placement.FrequencyMethod,
				PlacementType:   encoded.Placement.Type,
				Count:           encoded.Placement.Count,
				Distance:        encoded.Placement.Distance,
				Spread:          encoded.Placement.Spread,
				PreferredBiomes: parseBiomeList(encoded.Placement.PreferredBiomes),
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
	if r.structureCacheDir != "" {
		runtime.SetStructureCacheDir(r.structureCacheDir)
	}
	for setIndex, set := range runtime.sets {
		if len(set.Structures) == 0 {
			continue
		}
		if set.PlacementType == "minecraft:concentric_rings" {
			r.applyConcentricStructures(chunk, setIndex, set, biomes)
			continue
		}
		if set.Spacing <= 0 {
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
				if !structureFrequencyAllows(r.seed, set, originX, originZ) {
					continue
				}
				if originX < chunk.ChunkX-radius || originX > chunk.ChunkX+radius ||
					originZ < chunk.ChunkZ-radius || originZ > chunk.ChunkZ+radius {
					continue
				}
				start := runtime.cachedStart(r.seed, setIndex, originX, originZ, biomes, r)
				if start == nil {
					continue
				}
				for _, piece := range start.pieces {
					if piece.template == nil {
						placeStructureBlocks(chunk, piece)
						continue
					}
					adaptStructureTerrain(chunk, piece)
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

const structureStartCacheLimit = 256

func (r *structureRuntime) cacheGet(key structureStartKey) (*structureStart, bool) {
	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()
	start, ok := r.cache[key]
	return start, ok
}

func (r *structureRuntime) cachePut(key structureStartKey, start *structureStart) {
	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()
	if _, exists := r.cache[key]; !exists {
		r.cacheOrder = append(r.cacheOrder, key)
	}
	r.cache[key] = start
	for len(r.cacheOrder) > structureStartCacheLimit {
		oldest := r.cacheOrder[0]
		r.cacheOrder = r.cacheOrder[1:]
		delete(r.cache, oldest)
	}
}

func (r *structureRuntime) cachedStart(seed int64, setIndex, originX, originZ int, biomes *BiomeSource, router *Router) *structureStart {
	key := structureStartKey{seed: seed, set: setIndex, originX: originX, originZ: originZ}
	if cached, ok := r.cacheGet(key); ok {
		return cached
	}
	// 先尝试磁盘 start；没有时再构建。原版 start 会在区块状态中持久化，
	// 这里用独立目录保存展开后的 pieces，保证重启前后一致。
	if start, ok := r.startStore().load(key, r); ok {
		r.cachePut(key, start)
		return start
	}
	start := r.buildStart(seed, setIndex, originX, originZ, biomes, router)
	if start == nil {
		return nil
	}
	r.cachePut(key, start)
	r.startStore().save(key, start)
	return start
}

func (r *structureRuntime) startStore() *structureStartStore {
	r.storeMu.Lock()
	defer r.storeMu.Unlock()
	if r.store == nil {
		r.store = &structureStartStore{}
	}
	return r.store
}

// SetStructureCacheDir 配置结构 start 的磁盘缓存目录；空字符串关闭持久化。
func (r *structureRuntime) SetStructureCacheDir(dir string) {
	store := r.startStore()
	store.mu.Lock()
	store.dir = dir
	store.mu.Unlock()
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
	if def.Type == "minecraft:mineshaft" {
		return r.buildMineshaft(seed, originX, originZ, def, router)
	}
	if def.Type == "minecraft:stronghold" {
		return r.buildStronghold(seed, originX, originZ, router)
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
	piece.terrainAdaptation = def.TerrainAdaptation
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
				targetPiece.terrainAdaptation = def.TerrainAdaptation
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
	piece := structurePiece{name: name, template: template, templateName: name, processors: processors, origin: [3]int{originX, originY, originZ}, rotation: rotation, mirror: mirror, size: transformedSize(template.Size, rotation)}
	jigsaws := template.jigsawBlocks()
	sort.SliceStable(jigsaws, func(i, j int) bool {
		return jigsaws[i].SelectionPriority > jigsaws[j].SelectionPriority
	})
	for _, jigsaw := range jigsaws {
		pos := transformTemplatePos(jigsaw.Pos, template.Size, rotation, mirror)
		piece.connectors = append(piece.connectors, pieceConnector{
			pos:               [3]int{originX + pos[0], originY + pos[1], originZ + pos[2]},
			direction:         rotateDirection(jigsaw.Direction, rotation, mirror),
			name:              jigsaw.Name,
			target:            jigsaw.Target,
			pool:              jigsaw.Pool,
			selectionPriority: jigsaw.SelectionPriority,
			placementPriority: jigsaw.PlacementPriority,
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
		type candidate struct {
			jigsaw   jigsawBlock
			priority int
		}
		candidates := make([]candidate, 0, len(targetJigsaws))
		for _, targetJigsaw := range targetJigsaws {
			if connector.target != "" && targetJigsaw.Name != connector.target {
				continue
			}
			// 目标连接块必须朝向源连接块（原版 JigsawPlacement 检查
			// JigsawBlock 的 front 方向）；否则会在接口处生成背靠背的墙。
			if targetDir := rotateDirection(targetJigsaw.Direction, rotation, mirror); oppositeDirection(targetDir) != connector.direction {
				continue
			}
			candidates = append(candidates, candidate{jigsaw: targetJigsaw, priority: targetJigsaw.PlacementPriority})
		}
		sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].priority > candidates[j].priority })
		for _, item := range candidates {
			targetPos := transformTemplatePos(item.jigsaw.Pos, target.Size, rotation, mirror)
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
	if def.MaxDistanceFromCenter > 0 {
		limit = def.MaxDistanceFromCenter
	} else if def.Size > 0 {
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
	if base == nil {
		return nil
	}
	var top *structurePiece
	switch random.nextIntBounded(3) {
	case 0:
		top = add("minecraft:end_city/base_roof", base, [3]int{-1, 4, -1}, rotation)
	case 1:
		second := add("minecraft:end_city/second_floor_2", base, [3]int{-1, 0, -1}, rotation)
		top = add("minecraft:end_city/second_roof", second, [3]int{-1, 8, -1}, rotation)
	default:
		second := add("minecraft:end_city/second_floor_2", base, [3]int{-1, 0, -1}, rotation)
		third := add("minecraft:end_city/third_floor_2", second, [3]int{-1, 4, -1}, rotation)
		top = add("minecraft:end_city/third_roof", third, [3]int{-1, 8, -1}, rotation)
	}
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
			chunk, _, err = router.generateBaseChunk(key.x, key.z)
			if err != nil {
				return 64
			}
			router.structureTerrain[key] = chunk
			router.structureTerrainOrder = append(router.structureTerrainOrder, key)
			const terrainCacheLimit = 16
			for len(router.structureTerrainOrder) > terrainCacheLimit {
				oldest := router.structureTerrainOrder[0]
				router.structureTerrainOrder = router.structureTerrainOrder[1:]
				delete(router.structureTerrain, oldest)
			}
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

func oppositeDirection(direction string) string {
	switch direction {
	case "north":
		return "south"
	case "south":
		return "north"
	case "east":
		return "west"
	case "west":
		return "east"
	case "up":
		return "down"
	case "down":
		return "up"
	}
	return direction
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
