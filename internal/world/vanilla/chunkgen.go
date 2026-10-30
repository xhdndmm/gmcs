//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"fmt"
	"math"
)

// GeneratedChunk 是按原版算法生成出来的区块：每个方块保存注册名与方块状态属性。
//
// 它只是"生成结果"，不是服务器的区块存储格式；接线层负责把它转换成
// internal/world 需要的方块 id。
type GeneratedChunk struct {
	ChunkX int
	ChunkZ int
	MinX   int
	MinZ   int
	MinY   int
	Height int

	blocks  []SurfaceBlock
	heights [256]int
}

// NewGeneratedChunk 创建一个全空气的区块。
func NewGeneratedChunk(chunkX, chunkZ, minY, height int) *GeneratedChunk {
	const chunkArea = 16 * 16
	if height <= 0 || height > math.MaxInt/chunkArea {
		panic(fmt.Sprintf("invalid chunk height: %d", height))
	}
	blockCount := height * chunkArea
	chunk := &GeneratedChunk{
		ChunkX: chunkX,
		ChunkZ: chunkZ,
		MinX:   chunkX * 16,
		MinZ:   chunkZ * 16,
		MinY:   minY,
		Height: height,
		blocks: make([]SurfaceBlock, blockCount),
	}
	for i := range chunk.blocks {
		chunk.blocks[i] = SurfaceBlock{Name: "minecraft:air"}
	}
	for i := range chunk.heights {
		chunk.heights[i] = minY - 1
	}
	return chunk
}

// index 返回方块在内部数组中的下标（y 层优先，其次 z，最后 x）。
func (c *GeneratedChunk) index(x, y, z int) int {
	return ((y - c.MinY) * 16 * 16) + ((z - c.MinZ) * 16) + (x - c.MinX)
}

// InRange 报告坐标是否落在该区块内。
func (c *GeneratedChunk) InRange(x, y, z int) bool {
	return x >= c.MinX && x < c.MinX+16 &&
		z >= c.MinZ && z < c.MinZ+16 &&
		y >= c.MinY && y < c.MinY+c.Height
}

// BlockAt 返回方块；越界返回空气（与原版 getBlockState 对区块外的处理一致）。
func (c *GeneratedChunk) BlockAt(x, y, z int) SurfaceBlock {
	if !c.InRange(x, y, z) {
		return SurfaceBlock{Name: "minecraft:air"}
	}
	return c.blocks[c.index(x, y, z)]
}

// SetBlock 写入方块并同步高度图（WORLD_SURFACE_WG：最高非空气方块）。
func (c *GeneratedChunk) SetBlock(x, y, z int, block SurfaceBlock) {
	if !c.InRange(x, y, z) {
		return
	}
	c.blocks[c.index(x, y, z)] = block
	slot := (z-c.MinZ)*16 + (x - c.MinX)
	if !block.IsAir() && y > c.heights[slot] {
		c.heights[slot] = y
	}
}

// HeightAt 返回该列最高非空气方块的 y（全空气时为 MinY-1）。
func (c *GeneratedChunk) HeightAt(x, z int) int {
	if x < c.MinX || x >= c.MinX+16 || z < c.MinZ || z >= c.MinZ+16 {
		return c.MinY - 1
	}
	return c.heights[(z-c.MinZ)*16+(x-c.MinX)]
}

// RecalculateHeights 按当前方块重新计算高度图。
func (c *GeneratedChunk) RecalculateHeights() {
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			top := c.MinY - 1
			for y := c.MinY + c.Height - 1; y >= c.MinY; y-- {
				if !c.blocks[c.index(c.MinX+x, y, c.MinZ+z)].IsAir() {
					top = y
					break
				}
			}
			c.heights[z*16+x] = top
		}
	}
}

// GenerateChunk 用原版算法生成一个区块：
// 密度 → 含水层（石头/水/岩浆/空气）→ 表面规则（草/沙/深板岩/冰/基岩等）。
func (r *Router) GenerateChunk(chunkX, chunkZ int) (*GeneratedChunk, error) {
	setting := r.setting
	chunk := NewGeneratedChunk(chunkX, chunkZ, setting.MinY, setting.Height)
	sampler := r.NewChunkSampler(chunkX, chunkZ)
	aquifer := sampler.Aquifer()

	stone := SurfaceBlock{Name: qualify(setting.DefaultBlock)}
	water := SurfaceBlock{Name: "minecraft:water", Properties: map[string]string{"level": "0"}}
	lava := SurfaceBlock{Name: "minecraft:lava"}
	air := SurfaceBlock{Name: "minecraft:air"}

	for x := chunk.MinX; x < chunk.MinX+16; x++ {
		for z := chunk.MinZ; z < chunk.MinZ+16; z++ {
			for y := setting.MinY; y < setting.MinY+setting.Height; y++ {
				kind, defaultBlock := aquifer.ComputeSubstance(x, y, z, sampler.Density(x, y, z))
				switch {
				case defaultBlock:
					chunk.SetBlock(x, y, z, stone)
				case kind == fluidWater:
					chunk.SetBlock(x, y, z, water)
				case kind == fluidLava:
					chunk.SetBlock(x, y, z, lava)
				default:
					chunk.SetBlock(x, y, z, air)
				}
			}
		}
	}
	chunk.RecalculateHeights()

	biomes, err := r.Biomes()
	if err != nil {
		return nil, err
	}
	if err := r.carveChunk(chunk, sampler, biomes); err != nil {
		return nil, err
	}
	chunk.RecalculateHeights()
	system, err := NewSurfaceSystem(sampler)
	if err != nil {
		return nil, err
	}
	if system != nil {
		system.BuildSurface(chunk, biomes)
		chunk.RecalculateHeights()
	}
	return chunk, nil
}

// isSolidBlock 等价原版 SurfaceSystem 的"实心"判定：
// 非空气且不含流体。
func isSolidBlock(block SurfaceBlock) bool {
	return !block.IsAir() && !block.HasFluid()
}

// floorDouble 是 Mth.floor(double)。
func floorDouble(value float64) int {
	return int(math.Floor(value))
}
