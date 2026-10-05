package world

import "math"

// FlatGroundLevel 是超平坦地形草方块层的 Y 坐标（世界最低处往上第 3 格）。
const FlatGroundLevel = WorldMinY + 3

// FlatSpawnY 是玩家在超平坦地形上的脚部 Y 坐标（草方块上方一格）。
const FlatSpawnY = FlatGroundLevel + 1

// SeaLevel 是最高水方块的 Y 坐标。gmcs 的简单地形不追求与原版海平面
// 完全一致，但 62 与主世界观感接近（水面位于 Y=62）。
const SeaLevel = 62

// 地形参数：基础高度与起伏幅度（方块）。高度范围为 [57, 70]。
const (
	terrainBaseHeight = 64
	terrainAmplitude  = 14
)

// Generator 生成新区块的地形。
type Generator interface {
	// GenerateChunk 生成 (x, z) 处的初始区块。
	GenerateChunk(x, z int) *Chunk
	// SurfaceY 返回 (x, z) 处最高固体（非空气、非水）方块的 Y 坐标。
	// 必须是纯函数：不生成区块、不依赖区块内容，保证出生点与地形一致。
	SurfaceY(x, z int) int
}

// FlatGenerator 生成与原版“超平坦”预设一致的地形：
// 一层基岩、两层泥土、一层草方块，其余为空气。
type FlatGenerator struct{}

// GenerateChunk 实现 Generator。
func (FlatGenerator) GenerateChunk(x, z int) *Chunk {
	chunk := NewChunk(x, z)
	for blockX := 0; blockX < SectionSize; blockX++ {
		for blockZ := 0; blockZ < SectionSize; blockZ++ {
			chunk.SetBlockState(blockX, WorldMinY, blockZ, BedrockBlock)
			chunk.SetBlockState(blockX, WorldMinY+1, blockZ, DirtBlock)
			chunk.SetBlockState(blockX, WorldMinY+2, blockZ, DirtBlock)
			chunk.SetBlockState(blockX, FlatGroundLevel, blockZ, GrassBlock)
		}
	}
	return chunk
}

// SurfaceY 实现 Generator。
func (FlatGenerator) SurfaceY(int, int) int {
	return FlatGroundLevel
}

// SeededGenerator 按种子生成简单地形：多层值噪声的高度图、石头/泥土/草方块
// 层次、低洼处的水、水面附近的沙滩以及稀疏的橡树。
//
// 该地形与原版世界生成在观感上相近，但不是原版算法：不含洞穴、矿物、
// 生物群系差异与结构，海平面也仅近似。所有内容都是种子与坐标的确定纯函数。
type SeededGenerator struct {
	Seed int64
}

// 树木相关的噪声盐与生长概率。
const (
	treeSalt  = 0x5DEECE66D
	trunkSalt = 0x2545F4914F6CDD1D
	// treeChance 是每列长出橡树的概率（仅草地）。
	treeChance = 0.008
	// 三层地形噪声的盐（任意互不相同的奇数即可，需在 int64 范围内）。
	noiseSaltCoarse = 0x1E3779B97F4A7C15
	noiseSaltMedium = 0x42B2AE3D27D4EB4F
	noiseSaltFine   = 0x165667B19E3779F9
)

// GenerateChunk 实现 Generator。
func (g SeededGenerator) GenerateChunk(chunkX, chunkZ int) *Chunk {
	chunk := NewChunk(chunkX, chunkZ)
	baseX := chunkX * SectionSize
	baseZ := chunkZ * SectionSize
	for localX := 0; localX < SectionSize; localX++ {
		for localZ := 0; localZ < SectionSize; localZ++ {
			worldX := baseX + localX
			worldZ := baseZ + localZ
			height := terrainHeight(g.Seed, worldX, worldZ)

			chunk.SetBlockState(localX, WorldMinY, localZ, BedrockBlock)
			// 石头填充到最上三层之下。
			for y := WorldMinY + 1; y <= height-4; y++ {
				chunk.SetBlockState(localX, y, localZ, StoneBlock)
			}
			firstLayer := max(height-3, WorldMinY+1)
			if height <= SeaLevel+1 {
				// 水下与水边的沙滩。
				for y := firstLayer; y <= height; y++ {
					chunk.SetBlockState(localX, y, localZ, SandBlock)
				}
			} else {
				for y := firstLayer; y < height; y++ {
					chunk.SetBlockState(localX, y, localZ, DirtBlock)
				}
				chunk.SetBlockState(localX, height, localZ, GrassBlock)
			}
			// 水：从地面往上填满到海平面。
			for y := height + 1; y <= SeaLevel; y++ {
				chunk.SetBlockState(localX, y, localZ, WaterBlock)
			}
		}
	}
	g.decorateTrees(chunk)
	return chunk
}

// SurfaceY 实现 Generator。
func (g SeededGenerator) SurfaceY(x, z int) int {
	return terrainHeight(g.Seed, x, z)
}

// decorateTrees 在草地上生成稀疏的橡树。树冠半径最大 2 格，因此只在距
// 区块边缘至少 2 格的列上生长，避免需要修改相邻区块。
func (g SeededGenerator) decorateTrees(chunk *Chunk) {
	baseX := chunk.X * SectionSize
	baseZ := chunk.Z * SectionSize
	for localX := 2; localX <= SectionSize-3; localX++ {
		for localZ := 2; localZ <= SectionSize-3; localZ++ {
			worldX := baseX + localX
			worldZ := baseZ + localZ
			if hashUnit(g.Seed^treeSalt, worldX, worldZ) >= treeChance {
				continue
			}
			height := terrainHeight(g.Seed, worldX, worldZ)
			if height <= SeaLevel+1 {
				continue
			}
			trunkHeight := 4 + int(hashUnit(g.Seed^trunkSalt, worldX, worldZ)*3) // 4-6
			for y := height + 1; y <= height+trunkHeight; y++ {
				chunk.SetBlockState(localX, y, localZ, OakLogBlock)
			}
			top := height + trunkHeight
			for dy := -2; dy <= 1; dy++ {
				radius := 2
				if dy >= 0 {
					radius = 1
				}
				for dx := -radius; dx <= radius; dx++ {
					for dz := -radius; dz <= radius; dz++ {
						if dx == 0 && dz == 0 && dy <= 0 {
							continue // 树干位置
						}
						// 裁掉四角与顶/底层的角块，使树冠更接近原版。
						if abs(dx) == radius && abs(dz) == radius && (dy <= -2 || dy >= 1) {
							continue
						}
						if chunk.GetBlockState(localX+dx, top+dy, localZ+dz) != AirBlock {
							continue
						}
						chunk.SetBlockState(localX+dx, top+dy, localZ+dz, OakLeavesBlock)
					}
				}
			}
		}
	}
}

// terrainHeight 返回 (x, z) 处地面（最高固体方块）的 Y 坐标。
// 三个不同尺度的值噪声叠加，产生 [57, 70] 的高度。
func terrainHeight(seed int64, x, z int) int {
	noise := 0.55*valueNoise(seed^noiseSaltCoarse, x, z, 48) +
		0.30*valueNoise(seed^noiseSaltMedium, x, z, 16) +
		0.15*valueNoise(seed^noiseSaltFine, x, z, 7)
	height := float64(terrainBaseHeight) - terrainAmplitude/2 + noise*terrainAmplitude
	return int(math.Floor(height))
}

// valueNoise 是格点周期为 period 的值噪声（双线性插值 + smoothstep）。
func valueNoise(seed int64, x, z, period int) float64 {
	fx := float64(x) / float64(period)
	fz := float64(z) / float64(period)
	x0 := int(math.Floor(fx))
	z0 := int(math.Floor(fz))
	fracX := smoothstep(fx - float64(x0))
	fracZ := smoothstep(fz - float64(z0))
	v00 := hashUnit(seed, x0, z0)
	v10 := hashUnit(seed, x0+1, z0)
	v01 := hashUnit(seed, x0, z0+1)
	v11 := hashUnit(seed, x0+1, z0+1)
	top := v00 + (v10-v00)*fracX
	bottom := v01 + (v11-v01)*fracX
	return top + (bottom-top)*fracZ
}

func smoothstep(t float64) float64 {
	return t * t * (3 - 2*t)
}

// hashUnit 把 (seed, x, z) 混合为 [0, 1) 的确定性伪随机数（splitmix64 变体）。
func hashUnit(seed int64, x, z int) float64 {
	h := uint64(seed)*0x9E3779B97F4A7C15 +
		uint64(int64(x))*0xBF58476D1CE4E5B9 +
		uint64(int64(z))*0x94D049BB133111EB
	h ^= h >> 29
	h *= 0xBF58476D1CE4E5B9
	h ^= h >> 32
	return float64(h>>11) / float64(uint64(1)<<53)
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
