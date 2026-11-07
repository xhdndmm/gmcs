//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package world

import (
	"log/slog"
	"sync"

	"gmcs/internal/registry"
	"gmcs/internal/world/vanilla"
)

// VanillaGenerator 用 internal/world/vanilla 的原版算法生成主世界区块。
//
// 并发模型：Router 本身持有可变求值缓存，不能并发使用；这里用 sync.Pool
// 为每个 worker 提供独立的 Router 会话（共享只读的密度函数图与噪声实例）。
// 因此 GenerateChunk 可以被异步预生成 worker 并发调用。
type VanillaGenerator struct {
	dimension Dimension
	base      *vanilla.Router
	pool      sync.Pool
	heights   sync.Map // ChunkPos -> *[256]int（SurfaceY 按区块缓存）
	warned    sync.Map // 未收录方块名（只记录一次，避免刷日志）
}

// NewVanillaGenerator 创建原版算法生成器。数据缺失或解析失败时返回错误；
// 主世界没有其他地形生成器作为回退，调用方应中止启动。
func NewVanillaGenerator(seed int64) (*VanillaGenerator, error) {
	return NewVanillaDimensionGenerator(DimensionOverworld, "minecraft:overworld", seed)
}

// NewVanillaDimensionGenerator 为指定维度创建原版算法生成器。
// settingName 是该维度的 noise_settings 注册名（overworld/nether/end）。
func NewVanillaDimensionGenerator(dimension Dimension, settingName string, seed int64) (*VanillaGenerator, error) {
	data, err := vanilla.Load()
	if err != nil {
		return nil, err
	}
	base, err := vanilla.NewRouter(data, settingName, seed)
	if err != nil {
		return nil, err
	}
	generator := &VanillaGenerator{dimension: dimension, base: base}
	generator.pool.New = func() any { return base.NewSession() }
	return generator, nil
}

// GenerateChunk 实现 Generator。
func (g *VanillaGenerator) GenerateChunk(x, z int) *Chunk {
	session := g.pool.Get().(*vanilla.Router)
	defer g.pool.Put(session)
	generated, err := session.GenerateChunk(x, z)
	if err != nil {
		// 运行期生成失败属于数据/实现错误。这里不伪装成成功区块：
		// 记录错误并生成一个平坦的应急区块，避免单个区块拖垮整个服务。
		// 正常数据不应走到这里；测试会覆盖初始化与生成路径。
		slog.Error("原版区块生成失败，返回平坦应急区块", "chunk_x", x, "chunk_z", z, "err", err)
		return FlatGenerator{}.GenerateChunk(x, z)
	}
	chunk := NewChunkDim(x, z, g.dimension)
	minX := x * SectionSize
	minZ := z * SectionSize
	generated.ForEachBlock(func(blockX, blockY, blockZ int, block vanilla.SurfaceBlock) {
		state, ok := surfaceBlockState(block)
		if !ok {
			if _, logged := g.warned.LoadOrStore(block.Name, struct{}{}); !logged {
				slog.Warn("原版地形包含未收录的方块，已按空气处理", "block", block.Name)
			}
			state = 0
		}
		chunk.setBlockStateDirect(blockX&15, blockY, blockZ&15, state)
	})
	chunk.SetBiomeGrid(g.biomeGrid(session, minX, minZ))
	return chunk
}

// biomeGrid 生成 gmcs 区块使用的 4×4 群系网格。
// 原版 Biomes 是 4×4×4；gmcs 的区块存储只保留地表格网，这里按 y=64 采样
// 地表格网。
func (g *VanillaGenerator) biomeGrid(session *vanilla.Router, minX, minZ int) [16]uint16 {
	var grid [16]uint16
	source, err := session.Biomes()
	if err != nil {
		for i := range grid {
			grid[i] = BiomePlains
		}
		slog.Error("原版群系源初始化失败，区块使用平原群系", "err", err)
		return grid
	}
	source.ResetCache()
	for gridZ := 0; gridZ < 4; gridZ++ {
		for gridX := 0; gridX < 4; gridX++ {
			name := source.BiomeAtBlock(minX+gridX*4+2, 64, minZ+gridZ*4+2)
			if id, ok := registry.SyncEntryID("minecraft:worldgen/biome", name); ok {
				grid[gridZ*4+gridX] = uint16(id)
			} else {
				grid[gridZ*4+gridX] = BiomePlains
			}
		}
	}
	return grid
}

// surfaceBlockState 把原版生成结果的注册名/属性映射为 gmcs 的方块状态 ID。
func surfaceBlockState(block vanilla.SurfaceBlock) (uint16, bool) {
	if len(block.Properties) != 0 {
		if id, ok := registry.BlockStateWithProps(block.Name, block.Properties); ok {
			return id, true
		}
	}
	if id, err := registry.BlockStateID(block.Name); err == nil {
		return id, true
	}
	return 0, false
}

// SurfaceY 实现 Generator。原版生成器没有独立的纯函数高度图，
// 这里按区块生成一次并缓存每列最高固体方块，供出生点搜索使用。
func (g *VanillaGenerator) SurfaceY(x, z int) int {
	pos := ChunkPos{X: x >> 4, Z: z >> 4}
	value, ok := g.heights.Load(pos)
	if !ok {
		heights := new([256]int)
		for i := range heights {
			heights[i] = g.dimension.MinY() - 1
		}
		chunk := g.GenerateChunk(pos.X, pos.Z)
		for localZ := 0; localZ < SectionSize; localZ++ {
			for localX := 0; localX < SectionSize; localX++ {
				if column := chunk.Column(localX, localZ); column.HasSolid {
					heights[localZ*SectionSize+localX] = column.SolidY
				}
			}
		}
		value, _ = g.heights.LoadOrStore(pos, heights)
	}
	heights := value.(*[256]int)
	return heights[(z&15)*SectionSize+(x&15)]
}

// Dimension 实现 Generator。
func (g *VanillaGenerator) Dimension() Dimension {
	return g.dimension
}
