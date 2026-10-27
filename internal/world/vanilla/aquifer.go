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
// 求解流程（computeSubstance / getFluidStatus）尚未实现，见 docs/TODO.md。

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
