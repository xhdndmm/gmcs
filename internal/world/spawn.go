package world

import "math"

// 出生点搜索：所有维度通用的确定性螺旋搜索。
//
// 第一阶段用生成器的 SurfaceY（纯函数，无需生成区块）粗扫候选列；
// 第二阶段对候选列生成区块并验证（地面存在、上方两格空气、无危险方块）。
// 出生点必须位于世界边界内。

// spawnHint 报告生成器给出的候选列是否值得生成区块验证。
func spawnHint(generator Generator, dimension Dimension, x, z int) bool {
	height := generator.SurfaceY(x, z)
	switch dimension {
	case DimensionNether:
		return height >= 5 && height <= 120
	case DimensionEnd:
		return height > 20
	default:
		return height > SeaLevel+1
	}
}

// FindSpawn 在原点附近搜索安全的出生点（脚部世界坐标）。
// 返回 false 表示在搜索范围内没有找到（调用方回退到原点）。
func (w *World) FindSpawn(halfSize float64) (x, y, z float64, ok bool) {
	inside := func(blockX, blockZ int) bool {
		if halfSize <= 0 {
			return true
		}
		return math.Abs(float64(blockX)+0.5) <= halfSize && math.Abs(float64(blockZ)+0.5) <= halfSize
	}

	// 第一阶段：16 格步长的螺旋粗扫（大陆度噪声周期 256 格，足以覆盖
	// 多个大陆/海洋单元；最远 2048 格）。
	const (
		step        = 16
		maxDistance = 2048
	)
	consider := func(blockX, blockZ int) (float64, float64, float64, bool) {
		if !inside(blockX, blockZ) {
			return 0, 0, 0, false
		}
		if !spawnHint(w.generator, w.dimension, blockX, blockZ) {
			return 0, 0, 0, false
		}
		chunk, err := w.Chunk(floorDiv(blockX, SectionSize), floorDiv(blockZ, SectionSize))
		if err != nil {
			return 0, 0, 0, false
		}
		groundY, found := spawnGroundY(chunk, floorMod(blockX, SectionSize), floorMod(blockZ, SectionSize), w.dimension)
		if !found {
			return 0, 0, 0, false
		}
		return float64(blockX) + 0.5, float64(groundY + 1), float64(blockZ) + 0.5, true
	}

	if x, y, z, found := consider(0, 0); found {
		return x, y, z, true
	}
	for radius := step; radius <= maxDistance; radius += step {
		for dx := -radius; dx <= radius; dx += step {
			for _, dz := range [...]int{-radius, radius} {
				if x, y, z, found := consider(dx, dz); found {
					return x, y, z, true
				}
			}
		}
		for dz := -radius + step; dz <= radius-step; dz += step {
			for _, dx := range [...]int{-radius, radius} {
				if x, y, z, found := consider(dx, dz); found {
					return x, y, z, true
				}
			}
		}
	}
	return 0, 0, 0, false
}

// spawnGroundY 返回区块内 (x, z) 列可站立的脚部 Y（地面顶面 + 1）。
// 判定：自上而下找到第一个非空气方块；它必须是安全地面（非水/岩浆/
// 树叶/原木/仙人掌），且上方至少两格空气或非固体装饰。
func spawnGroundY(chunk *Chunk, x, z int, dimension Dimension) (int, bool) {
	top := WorldMinY + WorldHeight - 1
	switch dimension {
	case DimensionNether:
		top = 120
	case DimensionEnd:
		top = 100
	}
	groundY := -1
	var groundState uint16
	for y := top; y > dimension.MinY()+1; y-- {
		state := chunk.GetBlockState(x, y, z)
		if state == AirBlock {
			continue
		}
		if state == WaterBlock {
			return 0, false // 水面/湖底：不适合作为出生点
		}
		groundY, groundState = y, state
		break
	}
	if groundY < 0 {
		return 0, false
	}
	switch groundState {
	case LavaBlock, MagmaBlock, CactusBlock, OakLeavesBlock, SpruceLeavesBlock,
		AcaciaLeavesBlock, BirchLeavesBlock, DarkOakLeavesBlock, JungleLeavesBlock,
		OakLogBlock, SpruceLogBlock, AcaciaLogBlock, BirchLogBlock, DarkOakLogBlock, JungleLogBlock:
		return 0, false
	}
	if dimension == DimensionOverworld && groundY <= SeaLevel {
		return 0, false
	}
	if dimension == DimensionNether && (groundY < 5 || groundY > 120) {
		return 0, false
	}
	// 上方两格必须是可站立空间（空气；允许花/草等非碰撞装饰）。
	if state := chunk.GetBlockState(x, groundY+1, z); state != AirBlock && state != ShortGrassBlock && state != DandelionBlock && state != PoppyBlock && state != CornflowerBlock {
		return 0, false
	}
	if state := chunk.GetBlockState(x, groundY+2, z); state != AirBlock && !IsDecorationState(state) {
		return 0, false
	}
	return groundY, true
}

// IsDecorationState 报告状态是否是非固体装饰（花/草等），用于出生点空间判定。
func IsDecorationState(state uint16) bool {
	switch state {
	case ShortGrassBlock, DandelionBlock, PoppyBlock, CornflowerBlock, DeadBushBlock, SugarCaneBlock:
		return true
	}
	return false
}
