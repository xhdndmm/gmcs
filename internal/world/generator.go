package world

// FlatGroundLevel 是超平坦地形草方块层的 Y 坐标（世界最低处往上第 3 格）。
const FlatGroundLevel = WorldMinY + 3

// FlatSpawnY 是玩家在超平坦地形上的脚部 Y 坐标（草方块上方一格）。
const FlatSpawnY = FlatGroundLevel + 1

// SeaLevel 是主世界水面的 Y 坐标（原版海平面为 63，最高水方块位于 62）。
const SeaLevel = 62

// Generator 生成新区块的地形。
type Generator interface {
	// GenerateChunk 生成 (x, z) 处的初始区块。
	GenerateChunk(x, z int) *Chunk
	// SurfaceY 返回 (x, z) 处最高固体（非空气、非水）方块的 Y 坐标。
	// 必须是纯函数：不生成区块、不依赖区块内容，保证出生点与地形一致。
	SurfaceY(x, z int) int
	// Dimension 返回生成器对应的维度（决定区块网络编码的高度范围）。
	Dimension() Dimension
}

// FlatGenerator 生成与原版“超平坦”预设一致的地形：
// 一层基岩、两层泥土、一层草方块，其余为空气。
type FlatGenerator struct{}

// GenerateChunk 实现 Generator。
// 返回的区块尚未发布（World.Chunk 在锁内生成后才加入缓存），
// 因此生成过程使用无锁写入。
func (FlatGenerator) GenerateChunk(x, z int) *Chunk {
	chunk := NewChunkDim(x, z, DimensionOverworld)
	for blockX := 0; blockX < SectionSize; blockX++ {
		for blockZ := 0; blockZ < SectionSize; blockZ++ {
			chunk.setBlockStateDirect(blockX, WorldMinY, blockZ, BedrockBlock)
			chunk.setBlockStateDirect(blockX, WorldMinY+1, blockZ, DirtBlock)
			chunk.setBlockStateDirect(blockX, WorldMinY+2, blockZ, DirtBlock)
			chunk.setBlockStateDirect(blockX, FlatGroundLevel, blockZ, GrassBlock)
		}
	}
	return chunk
}

// SurfaceY 实现 Generator。
func (FlatGenerator) SurfaceY(int, int) int {
	return FlatGroundLevel
}

// Dimension 实现 Generator。
func (FlatGenerator) Dimension() Dimension {
	return DimensionOverworld
}
