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

// ForEachBlock 按 y→z→x 顺序遍历区块内全部方块。
func (c *GeneratedChunk) ForEachBlock(fn func(x, y, z int, block SurfaceBlock)) {
	for y := c.MinY; y < c.MinY+c.Height; y++ {
		for z := c.MinZ; z < c.MinZ+16; z++ {
			for x := c.MinX; x < c.MinX+16; x++ {
				fn(x, y, z, c.blocks[c.index(x, y, z)])
			}
		}
	}
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
	return r.generateChunkInternal(chunkX, chunkZ, true)
}

func (r *Router) generateChunkInternal(chunkX, chunkZ int, withStructures bool) (*GeneratedChunk, error) {
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
				density := sampler.Density(x, y, z)
				kind, defaultBlock := aquifer.ComputeSubstance(x, y, z, density)
				switch {
				case defaultBlock:
					// 原版 NoiseChunk 的 materialRule 顺序是
					// aquifer -> ore vein；矿脉只替换默认石头。
					placed := false
					if setting.OreVeinsEnabled {
						if vein, ok := r.oreVeinAt(sampler, x, y, z); ok {
							chunk.SetBlock(x, y, z, vein)
							placed = true
						}
					}
					if !placed {
						chunk.SetBlock(x, y, z, stone)
					}
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
	// 原版 ChunkStatus 顺序为 NOISE → SURFACE → CARVERS：
	// 表面规则先于雕刻器执行，雕刻出的洞穴/峡谷不会被重新铺上草/沙/水。
	system, err := NewSurfaceSystem(sampler)
	if err != nil {
		return nil, err
	}
	if system != nil {
		system.BuildSurface(chunk, biomes)
		chunk.RecalculateHeights()
	}
	if err := r.carveChunk(chunk, sampler, biomes); err != nil {
		return nil, err
	}
	if setting.Name == "minecraft:nether" {
		// 原版下界基岩顶位于噪声设置高度范围的最高一层（y=127）。
		// 表面规则在部分列会留下净岩，这里按维度边界补齐；不改变 y<127 的洞穴。
		roof := setting.MinY + setting.Height - 1
		bedrock := SurfaceBlock{Name: "minecraft:bedrock"}
		for x := chunk.MinX; x < chunk.MinX+16; x++ {
			for z := chunk.MinZ; z < chunk.MinZ+16; z++ {
				chunk.SetBlock(x, roof, z, bedrock)
			}
		}
	}
	// 原版顺序：CARVERS → FEATURES → STRUCTURES。
	r.applyFeatures(chunk, biomes)
	if withStructures {
		r.applyStructures(chunk, biomes)
	}
	chunk.RecalculateHeights()
	return chunk, nil
}

// oreVeinAt 复刻 1.21.11 OreVeinifier。
// 铜脉 y∈[0,50]、铁脉 y∈[-60,-8]；toggle>0 选铜，否则选铁。
func (r *Router) oreVeinAt(sampler *ChunkSampler, x, y, z int) (SurfaceBlock, bool) {
	toggle := sampler.Field("vein_toggle", x, y, z)
	veininess := math.Abs(toggle)
	var minimum, maximum int
	var ore, raw, shell string
	if toggle > 0 {
		minimum, maximum = 0, 50
		ore, raw, shell = "minecraft:copper_ore", "minecraft:raw_copper_block", "minecraft:granite"
	} else {
		minimum, maximum = -60, -8
		ore, raw, shell = "minecraft:iron_ore", "minecraft:raw_iron_block", "minecraft:tuff"
	}
	if y < minimum || y > maximum {
		return SurfaceBlock{}, false
	}
	distanceToEdge := min(maximum-y, y-minimum)
	edgeFactor := clampedMap(float64(distanceToEdge), 0, 20, -0.2, 0)
	if veininess+edgeFactor < 0.4 {
		return SurfaceBlock{}, false
	}
	positional := r.registry.factory.At(x, y, z)
	if positional.NextFloat() > 0.7 {
		return SurfaceBlock{}, false
	}
	if sampler.Field("vein_ridged", x, y, z) >= 0 {
		return SurfaceBlock{}, false
	}
	threshold := clampedMap(veininess, 0.4, 0.6, 0.1, 0.3)
	if float64(positional.NextFloat()) < threshold && sampler.Field("vein_gap", x, y, z) > -0.3 {
		if positional.NextFloat() < 0.02 {
			return SurfaceBlock{Name: raw}, true
		}
		return SurfaceBlock{Name: ore}, true
	}
	return SurfaceBlock{Name: shell}, true
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
