//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

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
