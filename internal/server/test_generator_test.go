//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import "gmcs/internal/world"

// testGenerator 是服务器测试用的固定陆地地形：地面 y=64，海平面以下为石头。
// 生产代码不使用它；测试需要一个高于海平面、可预测的出生点平台。
type testGenerator struct{}

func (testGenerator) Dimension() world.Dimension { return world.DimensionOverworld }

func (testGenerator) SurfaceY(int, int) int { return 64 }

func (testGenerator) GenerateChunk(x, z int) *world.Chunk {
	chunk := world.NewChunkDim(x, z, world.DimensionOverworld)
	for localX := 0; localX < 16; localX++ {
		for localZ := 0; localZ < 16; localZ++ {
			chunk.SetBlockState(localX, world.WorldMinY, localZ, world.BedrockBlock)
			for y := world.WorldMinY + 1; y < 64; y++ {
				chunk.SetBlockState(localX, y, localZ, world.StoneBlock)
			}
			chunk.SetBlockState(localX, 64, localZ, world.GrassBlock)
		}
	}
	return chunk
}
