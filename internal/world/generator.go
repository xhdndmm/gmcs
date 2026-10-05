package world

// FlatGroundLevel 是超平坦地形草方块层的 Y 坐标（世界最低处往上第 3 格）。
const FlatGroundLevel = WorldMinY + 3

// FlatSpawnY 是玩家在超平坦地形上的脚部 Y 坐标（草方块上方一格）。
const FlatSpawnY = FlatGroundLevel + 1

// Generator 生成新区块的地形。
type Generator interface {
	// GenerateChunk 生成 (x, z) 处的初始区块。
	GenerateChunk(x, z int) *Chunk
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
