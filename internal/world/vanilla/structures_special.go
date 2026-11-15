//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"math"
	"sort"
)

// structureFrequencyAllows 复刻 StructurePlacement 的频率过滤。
// 目前覆盖 random_spread 的 default 与 legacy_type_3；后者是矿洞使用的算法。
func structureFrequencyAllows(seed int64, set structureSetDefinition, chunkX, chunkZ int) bool {
	if set.Frequency >= 1 {
		return true
	}
	switch set.FrequencyMethod {
	case "legacy_type_3":
		random := newLegacyRandom(0)
		random.nextLong()
		random.nextLong()
		mixed := int64(chunkX)*random.nextLong() ^ int64(chunkZ)*random.nextLong() ^ seed
		random.setSeed(mixed)
		return random.nextDouble() < set.Frequency
	default:
		random := newLegacyRandom(seed + int64(set.Salt) + int64(chunkX)*341873128712 + int64(chunkZ)*132897987541)
		return random.nextDouble() < set.Frequency
	}
}

func (r *structureRuntime) ringOrigins(seed int64, setIndex int, set structureSetDefinition) [][2]int {
	r.ringMu.Lock()
	defer r.ringMu.Unlock()
	if r.rings == nil {
		r.rings = make(map[int][][2]int)
	}
	if origins, ok := r.rings[setIndex]; ok {
		return origins
	}
	count := set.Count
	if count <= 0 {
		count = 128
	}
	distance := set.Distance
	if distance <= 0 {
		distance = 32
	}
	spread := set.Spread
	if spread <= 0 {
		spread = 3
	}
	random := newLegacyRandom(seed)
	angle := random.nextDouble() * math.Pi * 2
	ring := 0
	inRing := 0
	perRing := spread
	origins := make([][2]int, 0, count)
	for i := 0; i < count; i++ {
		radius := float64(4*distance+distance*ring*6) + (random.nextDouble()-0.5)*float64(distance)*2.5
		x := int(math.Round(math.Cos(angle) * radius))
		z := int(math.Round(math.Sin(angle) * radius))
		origins = append(origins, [2]int{floorDiv(x, 16), floorDiv(z, 16)})
		angle += math.Pi * 2 / float64(perRing)
		inRing++
		if inRing == perRing {
			inRing = 0
			ring++
			perRing += 2 * perRing / (ring + 1)
			if perRing < 1 {
				perRing = 1
			}
			if perRing > count-i-1 && count-i-1 > 0 {
				perRing = count - i - 1
			}
			angle += random.nextDouble() * math.Pi * 2
		}
	}
	r.rings[setIndex] = origins
	return origins
}

func (r *Router) applyConcentricStructures(chunk *GeneratedChunk, setIndex int, set structureSetDefinition, biomes *BiomeSource) {
	runtime, err := loadStructureRuntime()
	if err != nil {
		return
	}
	// 要塞 pieces 可能跨越 origin 周围数个区块；使用 24 区块半径覆盖。
	const radius = 24
	for _, origin := range runtime.ringOrigins(r.seed, setIndex, set) {
		if origin[0] < chunk.ChunkX-radius || origin[0] > chunk.ChunkX+radius ||
			origin[1] < chunk.ChunkZ-radius || origin[1] > chunk.ChunkZ+radius {
			continue
		}
		start := runtime.cachedStart(r.seed, setIndex, origin[0], origin[1], biomes, r)
		if start == nil {
			continue
		}
		for _, piece := range start.pieces {
			if piece.template == nil {
				placeStructureBlocks(chunk, piece)
				continue
			}
			random := newLegacyRandom(r.seed + int64(piece.origin[0])*31 + int64(piece.origin[1])*17 + int64(piece.origin[2])*13)
			placeTemplate(chunk, piece.template, piece.origin[0], piece.origin[1], piece.origin[2], piece.rotation, piece.mirror, random)
		}
	}
}

type specialBlockBuilder struct {
	blocks map[[3]int]structureBlockPlacement
	min    [3]int
	max    [3]int
}

func newSpecialBlockBuilder() *specialBlockBuilder {
	return &specialBlockBuilder{blocks: make(map[[3]int]structureBlockPlacement)}
}

func (b *specialBlockBuilder) set(x, y, z int, state SurfaceBlock) {
	if b.blocks == nil {
		b.blocks = make(map[[3]int]structureBlockPlacement)
	}
	pos := [3]int{x, y, z}
	if len(b.blocks) == 0 {
		b.min, b.max = pos, pos
	} else {
		for i := 0; i < 3; i++ {
			if pos[i] < b.min[i] {
				b.min[i] = pos[i]
			}
			if pos[i] > b.max[i] {
				b.max[i] = pos[i]
			}
		}
	}
	b.blocks[pos] = structureBlockPlacement{pos: pos, state: state}
}

func (b *specialBlockBuilder) fill(x0, y0, z0, x1, y1, z1 int, state SurfaceBlock) {
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	if y0 > y1 {
		y0, y1 = y1, y0
	}
	if z0 > z1 {
		z0, z1 = z1, z0
	}
	for y := y0; y <= y1; y++ {
		for z := z0; z <= z1; z++ {
			for x := x0; x <= x1; x++ {
				b.set(x, y, z, state)
			}
		}
	}
}

func (b *specialBlockBuilder) piece(name string, origin [3]int) structurePiece {
	blocks := make([]structureBlockPlacement, 0, len(b.blocks))
	keys := make([][3]int, 0, len(b.blocks))
	for pos := range b.blocks {
		keys = append(keys, pos)
	}
	sort.Slice(keys, func(i, j int) bool {
		for axis := 0; axis < 3; axis++ {
			if keys[i][axis] != keys[j][axis] {
				return keys[i][axis] < keys[j][axis]
			}
		}
		return false
	})
	for _, pos := range keys {
		blocks = append(blocks, b.blocks[pos])
	}
	size := [3]int{0, 0, 0}
	for axis := 0; axis < 3; axis++ {
		size[axis] = b.max[axis] - b.min[axis] + 1
	}
	return structurePiece{name: name, origin: origin, size: size, blocks: blocks}
}

var airState = SurfaceBlock{Name: "minecraft:air"}
var caveAirState = SurfaceBlock{Name: "minecraft:cave_air"}

func planksState() SurfaceBlock { return SurfaceBlock{Name: "minecraft:oak_planks"} }
func fenceState() SurfaceBlock  { return SurfaceBlock{Name: "minecraft:oak_fence"} }
func railState() SurfaceBlock   { return SurfaceBlock{Name: "minecraft:rail"} }

// buildMineshaft 实现矿洞的主通道/分支/房间/楼梯骨架。它沿用原版
// MineshaftPieces 的尺寸和方块语义，但不依赖模板；这是专用生成器路径。
func (r *structureRuntime) buildMineshaft(seed int64, originX, originZ int, def structureDefinition, router *Router) *structureStart {
	random := newLegacyRandom(seed + int64(originX)*17 + int64(originZ)*31)
	startX, startZ := originX*16+8, originZ*16+8
	startY := 50
	mesa := def.MineshaftType == "mesa"
	planks := SurfaceBlock{Name: "minecraft:oak_planks"}
	if mesa {
		planks = SurfaceBlock{Name: "minecraft:dark_oak_planks"}
	}
	_ = router
	start := &structureStart{}
	builder := newSpecialBlockBuilder()
	type segment struct {
		x, y, z int
		dir     int
		length  int
	}
	directions := [4][2]int{{1, 0}, {0, 1}, {-1, 0}, {0, -1}}
	visited := make(map[[3]int]bool)
	queue := []segment{{x: startX, y: startY, z: startZ, dir: random.nextIntBounded(4), length: 10 + random.nextIntBounded(8)}}
	segments := 0
	for len(queue) > 0 && segments < 80 {
		current := queue[0]
		queue = queue[1:]
		segments++
		dx, dz := directions[current.dir][0], directions[current.dir][1]
		x, y, z := current.x, current.y, current.z
		for step := 0; step < current.length; step++ {
			for side := -1; side <= 1; side++ {
				bx, bz := x+dz*side, z-dx*side
				for by := y; by < y+3; by++ {
					builder.set(bx, by, bz, airState)
				}
				builder.set(bx, y-1, bz, planks)
			}
			builder.set(x, y, z, railState())
			if step%5 == 0 {
				for side := -1; side <= 1; side++ {
					bx, bz := x+dz*side, z-dx*side
					builder.set(bx, y+3, bz, planks)
					if side != 0 {
						builder.set(bx, y, bz, fenceState())
						builder.set(bx, y+1, bz, fenceState())
					}
				}
			}
			key := [3]int{x, y, z}
			if visited[key] {
				break
			}
			visited[key] = true
			x += dx
			z += dz
			if random.nextIntBounded(12) == 0 {
				queue = append(queue, segment{x: x, y: y, z: z, dir: (current.dir + 1 + random.nextIntBounded(2)) % 4, length: 5 + random.nextIntBounded(8)})
			}
			if random.nextIntBounded(18) == 0 {
				queue = append(queue, segment{x: x, y: y - 4, z: z, dir: current.dir, length: 5 + random.nextIntBounded(6)})
			}
		}
		// 末端房间；原版在部分交叉点生成 7×7 房间。
		if random.nextIntBounded(3) == 0 {
			for ry := -1; ry <= 3; ry++ {
				for rz := -3; rz <= 3; rz++ {
					for rx := -3; rx <= 3; rx++ {
						state := airState
						if ry == -1 || ry == 3 {
							state = planks
						}
						builder.set(x+rx, y+ry, z+rz, state)
					}
				}
			}
		}
		if len(queue) < 5 && random.nextBoolean() {
			queue = append(queue, segment{x: x, y: y, z: z, dir: random.nextIntBounded(4), length: 5 + random.nextIntBounded(10)})
		}
	}
	start.pieces = append(start.pieces, builder.piece("minecraft:mineshaft", [3]int{startX, startY, startZ}))
	return start
}

// buildStronghold 生成要塞的走廊/房间/传送门房间骨架。原版 StrongholdPieces
// 的房间模板是一个独立的生成器；这里使用同一套方块语义和尺寸构造专用路径。
func (r *structureRuntime) buildStronghold(seed int64, originX, originZ int, router *Router) *structureStart {
	random := newLegacyRandom(seed + int64(originX)*17 + int64(originZ)*31)
	startX, startZ := originX*16+8, originZ*16+8
	startY := r.startHeight(router, structureDefinition{Type: "minecraft:stronghold"}, startX, startZ)
	if startY < 8 {
		startY = 32
	}
	if startY > 200 {
		startY = 64
	}
	// 要塞通常埋在地下；bury 适配把中心放到地表以下。
	startY -= 20
	if startY < 8 {
		startY = 8
	}
	stone := SurfaceBlock{Name: "minecraft:stone_bricks"}
	mossy := SurfaceBlock{Name: "minecraft:mossy_stone_bricks"}
	cracked := SurfaceBlock{Name: "minecraft:cracked_stone_bricks"}
	root := newSpecialBlockBuilder()
	// 中央大厅。
	root.fill(startX-6, startY-1, startZ-6, startX+6, startY+6, startZ+6, stone)
	root.fill(startX-5, startY, startZ-5, startX+5, startY+5, startZ+5, airState)
	root.fill(startX-5, startY-1, startZ-5, startX+5, startY-1, startZ+5, stone)
	for i := 0; i < 4; i++ {
		dir := [2]int{0, 1}
		switch i {
		case 1:
			dir = [2]int{1, 0}
		case 2:
			dir = [2]int{0, -1}
		case 3:
			dir = [2]int{-1, 0}
		}
		x, z := startX+dir[0]*7, startZ+dir[1]*7
		length := 8 + random.nextIntBounded(8)
		for step := 0; step < length; step++ {
			for side := -1; side <= 1; side++ {
				bx, bz := x+dir[1]*side, z+dir[0]*side
				root.fill(bx, startY-1, bz, bx, startY+3, bz, stone)
				root.fill(bx, startY, bz, bx, startY+2, bz, airState)
			}
			if step%4 == 0 {
				root.set(x, startY+3, z, mossy)
			}
			x += dir[0]
			z += dir[1]
		}
		// 支路房间。
		root.fill(x-3, startY-1, z-3, x+3, startY+4, z+3, stone)
		root.fill(x-2, startY, z-2, x+2, startY+3, z+2, airState)
		root.set(x, startY+1, z, cracked)
	}
	// 传送门房间：中央 3×3 岩浆池和末地传送门框架。
	px, pz := startX, startZ
	root.fill(px-4, startY-1, pz-4, px+4, startY+4, pz+4, stone)
	root.fill(px-3, startY, pz-3, px+3, startY+3, pz+3, airState)
	for z := -2; z <= 2; z++ {
		for x := -2; x <= 2; x++ {
			if x == -2 || x == 2 || z == -2 || z == 2 {
				root.set(px+x, startY, pz+z, SurfaceBlock{Name: "minecraft:end_portal_frame", Properties: map[string]string{"facing": "north", "eye": "false"}})
			}
		}
	}
	root.fill(px-1, startY-1, pz-1, px+1, startY-1, pz+1, SurfaceBlock{Name: "minecraft:lava"})
	start := &structureStart{}
	start.pieces = append(start.pieces, root.piece("minecraft:stronghold", [3]int{startX, startY, startZ}))
	return start
}
