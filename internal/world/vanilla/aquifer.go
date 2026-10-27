//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import "math"

// 本文件是原版含水层（net.minecraft.world.level.levelgen.NoiseBasedAquifer，
// 1.21.11 混淆名 euc$c）的**网格几何**部分：格尺寸、索引换算与压力插值。
// 这些数值与公式逐条取自 euc$c 的构造函数与静态辅助方法：
//
//	a(int)        = i >> 4                （方块坐标 → 水平格坐标）
//	b(int)        = Math.floorDiv(i, 12)  （方块高度 → 竖向格坐标）
//	b(int, int)   = (i << 4) + j          （水平格坐标 → 方块坐标）
//	c(int, int)   = i * 12 + j            （竖向格坐标 → 方块高度）
//	a(int, int)   = 1 - (toY - fromY) / 25.0   （压力/相似度）
//	a(x, y, z)    = ((y-K)*sizeZ + (z-L))*sizeX + (x-J)
//
// 求解流程（computeFluid / computeSurfaceLevel / ComputeSubstance）见文件后半部分，
// 逐条对照 euc$c 字节码实现。

// aquiferGrid 是含水层的格网格（原版 NoiseBasedAquifer 的 J/K/L/M/N 字段）。
type aquiferGrid struct {
	originX int // J
	originY int // K
	originZ int // L
	sizeX   int // M
	sizeY   int // 竖向格数
	sizeZ   int // N
}

// newAquiferGrid 按原版构造函数计算格网格。
// minBlockX/maxBlockX/minBlockZ/maxBlockZ 是区块的方块坐标范围（含端点），
// minY/height 是维度高度范围。
func newAquiferGrid(minBlockX, maxBlockX, minBlockZ, maxBlockZ, minY, height int) aquiferGrid {
	grid := aquiferGrid{}
	grid.originX = blockToAquiferGridXZ(minBlockX - 5)
	endX := blockToAquiferGridXZ(maxBlockX-5) + 1
	grid.sizeX = endX - grid.originX + 1

	grid.originY = blockToAquiferGridY(minY+1) - 1
	endY := blockToAquiferGridY(minY+height+1) + 1
	grid.sizeY = endY - grid.originY + 1

	grid.originZ = blockToAquiferGridXZ(minBlockZ - 5)
	endZ := blockToAquiferGridXZ(maxBlockZ-5) + 1
	grid.sizeZ = endZ - grid.originZ + 1
	return grid
}

// size 返回格总数（原版数组长度 M * 竖向格数 * N）。
func (g aquiferGrid) size() int {
	return g.sizeX * g.sizeY * g.sizeZ
}

// index 返回 (x, y, z) 所在的格下标（原版私有方法 a(int,int,int)）。
// 坐标必须落在网格内，否则调用方应先做范围检查。
func (g aquiferGrid) index(x, y, z int) int {
	return ((y-g.originY)*g.sizeZ+(z-g.originZ))*g.sizeX + (x - g.originX)
}

// contains 报告格坐标是否落在网格内。
func (g aquiferGrid) contains(x, y, z int) bool {
	return x >= g.originX && x < g.originX+g.sizeX &&
		y >= g.originY && y < g.originY+g.sizeY &&
		z >= g.originZ && z < g.originZ+g.sizeZ
}

// aquiferPressure 是原版含水层的压力（相似度）函数：
// 两个竖向格相距越近，值越接近 1；相距 25 格以上时为负。
func aquiferPressure(fromY, toY int) float64 {
	return 1 - float64(toY-fromY)/25
}

// blockToAquiferGridXZ 把方块坐标折算到水平格坐标（原版 a/c(int)：右移 4 位）。
func blockToAquiferGridXZ(block int) int {
	return block >> 4
}

// blockToAquiferGridY 把方块高度折算到竖向格坐标（原版 b(int)：floorDiv(i, 12)）。
func blockToAquiferGridY(block int) int {
	return floorDiv(block, 12)
}

// aquiferGridToBlockXZ 由水平格坐标与格内偏移得到方块坐标（原版 b/d(int,int)）。
func aquiferGridToBlockXZ(cell, offset int) int {
	return (cell << 4) + offset
}

// aquiferGridToBlockY 由竖向格坐标与格内偏移得到方块高度（原版 c(int,int)）。
func aquiferGridToBlockY(cell, offset int) int {
	return cell*12 + offset
}

// fluidKind 是含水层关心的方块类别（原版用 BlockState 比较 Blocks.WATER/LAVA/AIR）。
type fluidKind uint8

const (
	fluidAir fluidKind = iota
	fluidWater
	fluidLava
)

// fluidStatus 等价原版 Aquifer.FluidStatus（euc$b）：
// 记录液面高度与流体类型，at(y) 在液面以下返回流体、否则返回空气。
type fluidStatus struct {
	level int
	kind  fluidKind
}

// at 返回高度 y 处的流体类别（原版 FluidStatus.at：y < fluidLevel ? fluidType : AIR）。
func (s fluidStatus) at(y int) fluidKind {
	if y < s.level {
		return s.kind
	}
	return fluidAir
}

// aquiferBlockPosKey 等价原版 BlockPos.asLong：
// x 低 26 位 << 38 | z 低 26 位 << 12 | y 低 12 位，用于含水层位置缓存。
func aquiferBlockPosKey(x, y, z int) int64 {
	const mask26 = int64(0x3FFFFFF)
	return (int64(x)&mask26)<<38 | (int64(z)&mask26)<<12 | (int64(y) & 0xFFF)
}

// aquiferBlockPosX/Y/Z 是 BlockPos.asLong 的逆运算（含符号扩展）。
func aquiferBlockPosX(key int64) int { return int(key << 0 >> 38) }
func aquiferBlockPosY(key int64) int { return int(key << 52 >> 52) }
func aquiferBlockPosZ(key int64) int { return int(key << 26 >> 38) }

// aquiferCalculatePressure 等价原版 NoiseBasedAquifer.calculatePressure：
// 两个相邻流体格在高度 y 处的水/岩浆交界返回 2，液面相同返回 0，
// 其余按液面差与竖直距离给出 [-2,2] 内的压力，再叠加 barrier 噪声。
// barrier 只在需要时求值并缓存到 *cached（原版用 MutableDouble 承载 NaN 哨兵）。
func aquiferCalculatePressure(y int, first, second fluidStatus, cached *float64, barrier func() float64) float64 {
	kindA := first.at(y)
	kindB := second.at(y)
	if (kindA == fluidLava && kindB == fluidWater) || (kindA == fluidWater && kindB == fluidLava) {
		return 2
	}
	levelDiff := first.level - second.level
	if levelDiff < 0 {
		levelDiff = -levelDiff
	}
	if levelDiff == 0 {
		return 0
	}
	mid := 0.5 * float64(first.level+second.level)
	distance := float64(y) + 0.5 - mid
	half := float64(levelDiff) / 2
	remaining := half - math.Abs(distance)
	var slope float64
	if distance > 0 {
		value := 0 + remaining
		if value > 0 {
			slope = value / 1.5
		} else {
			slope = value / 2.5
		}
	} else {
		value := 3 + remaining
		if value > 0 {
			slope = value / 3
		} else {
			slope = value / 10
		}
	}
	var noiseValue float64
	if slope < -2 || slope > 2 {
		noiseValue = 0
	} else if math.IsNaN(*cached) {
		*cached = barrier()
		noiseValue = *cached
	} else {
		noiseValue = *cached
	}
	return 2 * (noiseValue + slope)
}

// ---- 全局流体选择器（原版 NoiseBasedChunkGenerator.createFluidPicker） ----

// aquiferLavaLevel 是原版全局含水层里岩浆液面的固定高度。
const aquiferLavaLevel = -54

// aquiferWayBelowMinY 等价原版 DimensionType.WAY_BELOW_MIN_Y。
// 原版构造函数里 MIN_Y = -2032（可用高度的低 12 位），乘以 16 得到该常量。
const aquiferWayBelowMinY = -32512

// fluidKindOfBlock 把方块注册名折算成流体类别。含水层只关心水/岩浆/其它。
func fluidKindOfBlock(name string) fluidKind {
	switch qualify(name) {
	case "minecraft:water":
		return fluidWater
	case "minecraft:lava":
		return fluidLava
	default:
		return fluidAir
	}
}

// aquiferFluidPicker 等价原版 Aquifer.FluidPicker（euc$a）在正式服务器上的实现：
//
//	lava  = FluidStatus(-54, LAVA)
//	water = FluidStatus(sea_level, default_fluid)
//	return y < min(-54, sea_level) ? lava : water
//
// 原版在 IDE 调试模式下会额外返回一个位于 MIN_Y*2 的空气层，正式服务器不会
// 走到该分支，这里不实现。
type aquiferFluidPicker struct {
	threshold int
	water     fluidStatus
	lava      fluidStatus
}

// newAquiferFluidPicker 按原版 createFluidPicker 构造全局流体选择器。
func newAquiferFluidPicker(seaLevel int, defaultFluid fluidKind) aquiferFluidPicker {
	threshold := aquiferLavaLevel
	if seaLevel < threshold {
		threshold = seaLevel
	}
	return aquiferFluidPicker{
		threshold: threshold,
		water:     fluidStatus{level: seaLevel, kind: defaultFluid},
		lava:      fluidStatus{level: aquiferLavaLevel, kind: fluidLava},
	}
}

// computeFluid 返回该坐标的全局流体状态（与原版一致：只依赖 y）。
func (p aquiferFluidPicker) computeFluid(x, y, z int) fluidStatus {
	if y < p.threshold {
		return p.lava
	}
	return p.water
}

// aquiferSamplingOffsets 等价原版 SURFACE_SAMPLING_OFFSETS_IN_CHUNKS（13 项）。
// 每项是区块坐标偏移，使用时乘以 16 得到方块偏移。
var aquiferSamplingOffsets = [13][2]int{
	{0, 0},
	{-2, -1}, {-1, -1}, {0, -1}, {1, -1},
	{-3, 0}, {-2, 0}, {-1, 0}, {1, 0},
	{-2, 1}, {-1, 1}, {0, 1}, {1, 1},
}

// aquiferPressureThreshold 等价原版静态常量 m = aquiferPressure(Mth.square(10),
// Mth.square(12))，即 1 - 44/25 = -0.76。用于判断两个流体格是否"足够接近"。
var aquiferPressureThreshold = aquiferPressure(100, 144)

// preliminaryColumn 是 preliminary_surface_level 的缓存键（按 4 格对齐的 x/z）。
type preliminaryColumn struct {
	x int
	z int
}

// Aquifer 是原版 NoiseBasedAquifer（euc$c）的实现：把"密度为正"以外的空间
// 划分成空气 / 水 / 岩浆，是逐方块地形判定的最后一步。
//
// 使用方式（与原版 NoiseChunk 一致）：
//
//	sampler := router.NewChunkSampler(chunkX, chunkZ)
//	aquifer := sampler.Aquifer()
//	kind, defaultBlock := aquifer.ComputeSubstance(x, y, z, sampler.Density(x, y, z))
//
// 密度为正时 defaultBlock 为 true（调用方放置 noise_settings.default_block），
// 否则 kind 为 air / water / lava。
//
// 不可并发使用（内部含格缓存）。
type Aquifer struct {
	setting   NoiseSetting
	evaluator *Evaluator
	grid      aquiferGrid
	picker    aquiferFluidPicker
	random    positionalRandomFactory
	maxGridY  int

	barrier     *Node
	floodedness *Node
	spread      *Node
	lava        *Node
	erosion     *Node
	depth       *Node
	preliminary *Node

	statuses    []fluidStatus
	statusKnown []bool
	locations   []int64

	preliminaryCache map[preliminaryColumn]int

	shouldScheduleFluidUpdate bool
}

// newAquifer 按原版 NoiseBasedAquifer 构造函数建立某个区块的含水层。
func newAquifer(s *ChunkSampler) *Aquifer {
	setting := s.setting
	grid := newAquiferGrid(s.originX, s.originX+15, s.originZ, s.originZ+15, setting.MinY, setting.Height)
	aquifer := &Aquifer{
		setting:          setting,
		evaluator:        s.evaluator,
		grid:             grid,
		picker:           newAquiferFluidPicker(setting.SeaLevel, fluidKindOfBlock(setting.DefaultFluid)),
		random:           s.evaluator.noises.FromHashOf("aquifer").ForkPositional(),
		preliminaryCache: make(map[preliminaryColumn]int),
	}
	for _, field := range []struct {
		name   string
		target **Node
	}{
		{"barrier", &aquifer.barrier},
		{"fluid_level_floodedness", &aquifer.floodedness},
		{"fluid_level_spread", &aquifer.spread},
		{"lava", &aquifer.lava},
		{"erosion", &aquifer.erosion},
		{"depth", &aquifer.depth},
		{"preliminary_surface_level", &aquifer.preliminary},
	} {
		node, ok := s.router.nodes[field.name]
		if !ok {
			panic("vanilla: 噪声设置 " + setting.Name + " 缺少噪声路由项 " + field.name)
		}
		*field.target = node
	}

	size := grid.size()
	aquifer.statuses = make([]fluidStatus, size)
	aquifer.statusKnown = make([]bool, size)
	aquifer.locations = make([]int64, size)
	for i := range aquifer.locations {
		aquifer.locations[i] = math.MaxInt64
	}

	// 原版：preliminarySurfaceLevel 取格网格四角所在的最大地表高度，再加 8，
	// 换算回格坐标得到 maxGridY。高于 maxGridY 的方块直接使用全局流体选择器。
	preliminary := aquifer.preliminarySurfaceLevelRange(
		aquiferGridToBlockXZ(grid.originX, 0), aquiferGridToBlockXZ(grid.originZ, 0),
		aquiferGridToBlockXZ(grid.originX+grid.sizeX-1, 9), aquiferGridToBlockXZ(grid.originZ+grid.sizeZ-1, 9),
	) + 8
	gridY := floorDiv(preliminary+12, 12) - 1
	aquifer.maxGridY = aquiferGridToBlockY(gridY, 11) - 1
	return aquifer
}

// ShouldScheduleFluidUpdate 等价原版 Aquifer.shouldScheduleFluidUpdate()：
// 最近一次 ComputeSubstance 是否判定该方块需要重新调度流体更新。
func (a *Aquifer) ShouldScheduleFluidUpdate() bool {
	return a.shouldScheduleFluidUpdate
}

// quartFrom 等价原版 QuartPos.fromSection(QuartPos.toQuart(v))，即向下对齐到 4。
func quartFrom(value int) int {
	return (value >> 2) << 2
}

// preliminarySurfaceLevel 等价原版 NoiseChunk.preliminarySurfaceLevel(x, z)：
// 对 4 格对齐的列求 preliminary_surface_level 密度函数（一个 find_top_surface
// 节点，返回值本身就是方块高度）并取整，结果按列缓存。
func (a *Aquifer) preliminarySurfaceLevel(x, z int) int {
	column := preliminaryColumn{x: quartFrom(x), z: quartFrom(z)}
	if value, ok := a.preliminaryCache[column]; ok {
		return value
	}
	value := int(math.Floor(a.evaluator.Evaluate(a.preliminary, Context{X: column.x, Y: 0, Z: column.z})))
	a.preliminaryCache[column] = value
	return value
}

// preliminarySurfaceLevelRange 等价原版 NoiseChunk.preliminarySurfaceLevel
// 的四参数重载：在 [x1, x2] × [z1, z2] 上以 4 为步长取样并取最大值。
func (a *Aquifer) preliminarySurfaceLevelRange(x1, z1, x2, z2 int) int {
	best := math.MinInt32
	for z := z1; z <= z2; z += 4 {
		for x := x1; x <= x2; x += 4 {
			if level := a.preliminarySurfaceLevel(x, z); level > best {
				best = level
			}
		}
	}
	return best
}

// statusAt 等价原版 getAquiferStatus(int)：按格下标取流体状态，带缓存。
func (a *Aquifer) statusAt(index int) fluidStatus {
	if a.statusKnown[index] {
		return a.statuses[index]
	}
	loc := a.locations[index]
	status := a.computeFluid(aquiferBlockPosX(loc), aquiferBlockPosY(loc), aquiferBlockPosZ(loc))
	a.statuses[index] = status
	a.statusKnown[index] = true
	return status
}

// computeFluid 等价原版 computeFluid(int, int, int)：为一个含水层格求出
// 液面高度与流体类别。它会采样周围 13 个区块的地表高度来校正液面。
func (a *Aquifer) computeFluid(x, y, z int) fluidStatus {
	global := a.picker.computeFluid(x, y, z)
	bestSurface := math.MaxInt32
	top := y + 12
	bottom := y - 12
	foundSurface := false
	for _, offset := range aquiferSamplingOffsets {
		sampleX := x + (offset[0] << 4)
		sampleZ := z + (offset[1] << 4)
		surface := a.preliminarySurfaceLevel(sampleX, sampleZ) + 8
		center := offset[0] == 0 && offset[1] == 0
		if center && bottom > surface {
			return global
		}
		aboveTop := top > surface
		if aboveTop || center {
			status := a.picker.computeFluid(sampleX, surface, sampleZ)
			if status.at(surface) != fluidAir {
				if center {
					foundSurface = true
				}
				if aboveTop {
					return status
				}
			}
			if surface < bestSurface {
				bestSurface = surface
			}
		}
	}
	level := a.computeSurfaceLevel(x, y, z, global, bestSurface, foundSurface)
	return fluidStatus{level: level, kind: a.computeFluidType(x, y, z, global, level)}
}

// computeSurfaceLevel 等价原版 computeSurfaceLevel：结合深暗之域判定与
// fluid_level_floodedness 噪声决定液面高度。
func (a *Aquifer) computeSurfaceLevel(x, y, z int, global fluidStatus, bestSurface int, foundSurface bool) int {
	ctx := Context{X: x, Y: y, Z: z}
	var first, second float64
	if a.isDeepDarkRegion(ctx) {
		first = -1
		second = -1
	} else {
		depth := float64(bestSurface + 8 - y)
		var flooding float64
		if foundSurface {
			flooding = clampedMap(depth, 0, 64, 1, 0)
		}
		noise := clampValue(a.evaluator.Evaluate(a.floodedness, ctx), -1, 1)
		// 原版：Mth.b(f, 1.0, 0.0, -0.3, 0.8) = lerp(inverseLerp(f, 1, 0), -0.3, 0.8)
		inverse := inverseLerp(flooding, 1, 0)
		high := lerpUnclamped(inverse, -0.3, 0.8)
		low := lerpUnclamped(inverse, -0.8, 0.4)
		first = noise - low
		second = noise - high
	}
	if second > 0 {
		return global.level
	}
	if first > 0 {
		return a.computeRandomizedFluidSurfaceLevel(x, y, z, bestSurface)
	}
	return aquiferWayBelowMinY
}

// isDeepDarkRegion 等价原版 OverworldBiomeBuilder.isDeepDarkRegion。
func (a *Aquifer) isDeepDarkRegion(ctx Context) bool {
	return a.evaluator.Evaluate(a.erosion, ctx) < -0.22499999403953552 &&
		a.evaluator.Evaluate(a.depth, ctx) > 0.8999999761581421
}

// computeRandomizedFluidSurfaceLevel 等价原版同名方法：用
// fluid_level_spread 噪声在 40 格高度带内给出确定性的液面扰动。
func (a *Aquifer) computeRandomizedFluidSurfaceLevel(x, y, z, bestSurface int) int {
	cellX := floorDiv(x, 16)
	cellY := floorDiv(y, 40)
	cellZ := floorDiv(z, 16)
	base := cellY*40 + 20
	value := a.evaluator.Evaluate(a.spread, Context{X: cellX, Y: cellY, Z: cellZ}) * 10
	candidate := base + roundToward(value, 3)
	if candidate < bestSurface {
		return candidate
	}
	return bestSurface
}

// computeFluidType 等价原版 computeFluidType：液面足够低时用 lava 噪声决定
// 该格是岩浆还是水。
func (a *Aquifer) computeFluidType(x, y, z int, global fluidStatus, level int) fluidKind {
	kind := global.kind
	if level <= -10 && level != aquiferWayBelowMinY && global.kind != fluidLava {
		ctx := Context{X: floorDiv(x, 64), Y: floorDiv(y, 40), Z: floorDiv(z, 64)}
		if math.Abs(a.evaluator.Evaluate(a.lava, ctx)) > 0.3 {
			kind = fluidLava
		}
	}
	return kind
}

// ComputeSubstance 等价原版 NoiseBasedAquifer.computeSubstance：
//
//   - density > 0：返回 defaultBlock=true，调用方放置 noise_settings.default_block；
//   - 高度高于 maxGridY：直接使用全局流体选择器；
//   - 否则在 4 个最近的含水层格之间按距离加权求压力，决定最终是
//     空气、水还是岩浆（与原版一样，被压实的负密度区域也可能是空气）。
//
// y 必须与 density 对应的方块高度一致。
func (a *Aquifer) ComputeSubstance(x, y, z int, density float64) (kind fluidKind, defaultBlock bool) {
	if density > 0 {
		a.shouldScheduleFluidUpdate = false
		return fluidAir, true
	}
	global := a.picker.computeFluid(x, y, z)
	if y > a.maxGridY {
		a.shouldScheduleFluidUpdate = false
		return global.at(y), false
	}
	if global.at(y) == fluidLava {
		a.shouldScheduleFluidUpdate = false
		return fluidLava, false
	}

	gridX := blockToAquiferGridXZ(x - 5)
	gridY := blockToAquiferGridY(y + 1)
	gridZ := blockToAquiferGridXZ(z - 5)

	var bestDist [4]int
	var bestIndex [4]int
	for i := range bestDist {
		bestDist[i] = math.MaxInt32
	}
	for offsetX := 0; offsetX <= 1; offsetX++ {
		for offsetY := -1; offsetY <= 1; offsetY++ {
			for offsetZ := 0; offsetZ <= 1; offsetZ++ {
				cellX := gridX + offsetX
				cellY := gridY + offsetY
				cellZ := gridZ + offsetZ
				index := a.grid.index(cellX, cellY, cellZ)
				loc := a.locations[index]
				if loc == math.MaxInt64 {
					rng := a.random.At(cellX, cellY, cellZ)
					loc = aquiferBlockPosKey(
						aquiferGridToBlockXZ(cellX, rng.NextInt(10)),
						aquiferGridToBlockY(cellY, rng.NextInt(9)),
						aquiferGridToBlockXZ(cellZ, rng.NextInt(10)),
					)
					a.locations[index] = loc
				}
				dx := aquiferBlockPosX(loc) - x
				dy := aquiferBlockPosY(loc) - y
				dz := aquiferBlockPosZ(loc) - z
				dist := dx*dx + dy*dy + dz*dz
				switch {
				case bestDist[0] >= dist:
					bestIndex[3], bestIndex[2], bestIndex[1] = bestIndex[2], bestIndex[1], bestIndex[0]
					bestDist[3], bestDist[2], bestDist[1] = bestDist[2], bestDist[1], bestDist[0]
					bestIndex[0], bestDist[0] = index, dist
				case bestDist[1] >= dist:
					bestIndex[3], bestIndex[2] = bestIndex[2], bestIndex[1]
					bestDist[3], bestDist[2] = bestDist[2], bestDist[1]
					bestIndex[1], bestDist[1] = index, dist
				case bestDist[2] >= dist:
					bestIndex[3] = bestIndex[2]
					bestDist[3] = bestDist[2]
					bestIndex[2], bestDist[2] = index, dist
				case bestDist[3] >= dist:
					bestIndex[3], bestDist[3] = index, dist
				}
			}
		}
	}

	status1 := a.statusAt(bestIndex[0])
	pressure12 := aquiferPressure(bestDist[0], bestDist[1])
	state := status1.at(y)
	if pressure12 <= 0 {
		if pressure12 >= aquiferPressureThreshold {
			status2 := a.statusAt(bestIndex[1])
			a.shouldScheduleFluidUpdate = status1 != status2
		} else {
			a.shouldScheduleFluidUpdate = false
		}
		return state, false
	}

	// 水面上方紧邻岩浆时，需要调度流体更新（原版在水与岩浆交界的处理）。
	if state == fluidWater && a.picker.computeFluid(x, y-1, z).at(y-1) == fluidLava {
		a.shouldScheduleFluidUpdate = true
		return state, false
	}

	cached := math.NaN()
	barrier := func() float64 {
		return a.evaluator.Evaluate(a.barrier, Context{X: x, Y: y, Z: z})
	}
	status2 := a.statusAt(bestIndex[1])
	p12 := pressure12 * aquiferCalculatePressure(y, status1, status2, &cached, barrier)
	if density+p12 > 0 {
		a.shouldScheduleFluidUpdate = false
		return fluidAir, true
	}

	status3 := a.statusAt(bestIndex[2])
	pressure13 := aquiferPressure(bestDist[0], bestDist[2])
	if pressure13 > 0 {
		p13 := pressure12 * pressure13 * aquiferCalculatePressure(y, status1, status3, &cached, barrier)
		if density+p13 > 0 {
			a.shouldScheduleFluidUpdate = false
			return fluidAir, true
		}
	}

	pressure23 := aquiferPressure(bestDist[1], bestDist[2])
	if pressure23 > 0 {
		p23 := pressure12 * pressure23 * aquiferCalculatePressure(y, status2, status3, &cached, barrier)
		if density+p23 > 0 {
			a.shouldScheduleFluidUpdate = false
			return fluidAir, true
		}
	}

	status4 := a.statusAt(bestIndex[3])
	neighbourDiffers := status1 != status2
	secondDiffers := pressure23 >= aquiferPressureThreshold && status2 != status3
	thirdDiffers := pressure13 >= aquiferPressureThreshold && status1 != status3
	if neighbourDiffers || secondDiffers || thirdDiffers {
		a.shouldScheduleFluidUpdate = true
	} else {
		a.shouldScheduleFluidUpdate = pressure13 >= aquiferPressureThreshold &&
			aquiferPressure(bestDist[0], bestDist[3]) >= aquiferPressureThreshold &&
			status1 != status4
	}
	return state, false
}
