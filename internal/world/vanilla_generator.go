//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package world

import (
	"log/slog"
	"strings"
	"sync"

	"gmcs/internal/registry"
	"gmcs/internal/world/vanilla"
)

// GeneratorMode 是主世界地形生成器的选择（配置项 world_generator）。
type GeneratorMode string

const (
	// GeneratorLegacy 是 gmcs 自研生成器（当前默认）。
	GeneratorLegacy GeneratorMode = "legacy"
	// GeneratorVanilla 是按原版算法生成地形的实验性生成器：
	// 密度函数 + 含水层 + 表面规则 + cave/canyon 雕刻器。
	// 地物与结构尚未实现，因此不会生成矿物/树木/建筑。
	GeneratorVanilla GeneratorMode = "vanilla"
)

// ParseGeneratorMode 解析配置值；空值回退 legacy。
func ParseGeneratorMode(value string) (GeneratorMode, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "legacy", "gmcs":
		return GeneratorLegacy, true
	case "vanilla", "official", "vanilla_like":
		return GeneratorVanilla, true
	default:
		return GeneratorLegacy, false
	}
}

// NewGeneratorWithMode 按维度与模式创建生成器。
// vanilla 模式目前只支持主世界；下界/末地仍使用原有生成器。
func NewGeneratorWithMode(dimension Dimension, seed int64, mode GeneratorMode) Generator {
	if dimension == DimensionOverworld && mode == GeneratorVanilla {
		generator, err := NewVanillaGenerator(seed)
		if err == nil {
			return generator
		}
		slog.Error("原版地形生成器初始化失败，回退到 legacy 生成器", "err", err)
	}
	return NewGenerator(dimension, seed)
}

// VanillaGenerator 用 internal/world/vanilla 的原版算法生成主世界区块。
//
// 并发模型：Router 本身持有可变求值缓存，不能并发使用；这里用 sync.Pool
// 为每个 worker 提供独立的 Router 会话（共享只读的密度函数图与噪声实例）。
// 因此 GenerateChunk 可以像 legacy 生成器一样被异步预生成 worker 并发调用。
type VanillaGenerator struct {
	seed     int64
	base     *vanilla.Router
	fallback Generator
	pool     sync.Pool
	heights  sync.Map // ChunkPos -> *[256]int（SurfaceY 按区块缓存）
	warned   sync.Map // 未收录方块名（只记录一次，避免刷日志）
}

// NewVanillaGenerator 创建原版算法生成器。数据缺失或解析失败时返回错误，
// 调用方应回退到 legacy 生成器。
func NewVanillaGenerator(seed int64) (*VanillaGenerator, error) {
	data, err := vanilla.Load()
	if err != nil {
		return nil, err
	}
	base, err := vanilla.NewRouter(data, "minecraft:overworld", seed)
	if err != nil {
		return nil, err
	}
	generator := &VanillaGenerator{seed: seed, base: base, fallback: SeededGenerator{Seed: seed}}
	generator.pool.New = func() any { return base.NewSession() }
	return generator, nil
}

// GenerateChunk 实现 Generator。
func (g *VanillaGenerator) GenerateChunk(x, z int) *Chunk {
	session := g.pool.Get().(*vanilla.Router)
	defer g.pool.Put(session)
	generated, err := session.GenerateChunk(x, z)
	if err != nil {
		// 运行期生成失败属于数据/实现错误；不让整个区块加载失败，
		// 回退到 legacy 生成器并记录错误（便于定位）。
		slog.Error("原版区块生成失败，回退到 legacy", "chunk_x", x, "chunk_z", z, "err", err)
		return g.fallback.GenerateChunk(x, z)
	}
	chunk := NewChunkDim(x, z, DimensionOverworld)
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
// 原版 Biomes 是 4×4×4；gmcs 的区块存储只保留地表格网，这里按地表高度附近
// 采样（y=64），与旧生成器“地表群系”的语义一致。
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
			heights[i] = WorldMinY - 1
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
	return DimensionOverworld
}
