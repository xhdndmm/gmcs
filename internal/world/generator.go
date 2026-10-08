package world

import (
	"math"

	"gmcs/internal/registry"
)

// FlatGroundLevel 是超平坦地形草方块层的 Y 坐标（世界最低处往上第 3 格）。
const FlatGroundLevel = WorldMinY + 3

// FlatSpawnY 是玩家在超平坦地形上的脚部 Y 坐标（草方块上方一格）。
const FlatSpawnY = FlatGroundLevel + 1

// SeaLevel 是最高水方块的 Y 坐标。gmcs 的简单地形不追求与原版海平面
// 完全一致，但 62 与主世界观感接近（水面位于 Y=62）。
const SeaLevel = 62

// 地形参数：大陆基准高度与起伏幅度（方块）。
const (
	// oceanFloorDepth 是深海底基准高度，landBaseHeight 是平原基准高度。
	oceanFloorDepth = 42
	landBaseHeight  = 68
	// hillAmplitude 是丘陵起伏幅度、mountainAmplitude 是山地最大隆起幅度。
	hillAmplitude     = 14
	mountainAmplitude = 46
	// detailAmplitude 是细节起伏幅度（小尺度）。
	detailAmplitude = 5
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
// 返回的区块尚未发布（World.Chunk 在锁内生成后才加入缓存），
// 因此生成过程使用无锁写入。
func (FlatGenerator) GenerateChunk(x, z int) *Chunk {
	chunk := NewChunk(x, z)
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

// SeededGenerator 按种子生成地形：大陆度/山脊/丘陵/细节值噪声的高度图、
// 按温度与湿度划分的生物群系（雪原/雪原针叶林/针叶林/平原/森林/沼泽/沙漠/
// 稀树草原/丛林/海洋/深海/沙滩）、群系化的地表（沙/草/雪层）与植被
// （橡树/云杉/金合欢/仙人掌/枯灌木/草丛）。
//
// 该地形与原版世界生成在观感上相近，但不是原版算法：不含洞穴、矿物、
// 河流与结构。所有内容都是种子与坐标的确定纯函数。
type SeededGenerator struct {
	Seed int64
}

// 噪声与装饰的盐（任意互不相同的奇数即可，需在 int64 范围内）。
const (
	noiseSaltCont   = 0x1E3779B97F4A7C15 // 大陆度（极低频）
	noiseSaltHills  = 0x42B2AE3D27D4EB4F // 丘陵
	noiseSaltDetail = 0x165667B19E3779F9 // 细节
	noiseSaltRidge  = 0x27220A95B76E3C2D // 山脊
	noiseSaltTemp   = 0x632BE59BD9B4E019 // 温度
	noiseSaltHumid  = 0x53F9EE8D2B2A6F31 // 湿度

	treeSalt   = 0x5DEECE66D
	trunkSalt  = 0x2545F4914F6CDD1D
	decorSalt  = 0x5A09E667F3BCC909
	cactusSalt = 0x5C2B2AE3D27D4EB4
	spruceSalt = 0x165667B19E3779FA
	acaciaSalt = 0x27220A95B76E3C2E
)

// 群系 ID（同步注册表 minecraft:worldgen/biome 的条目 ID）。
// 世界数据中的群系必须使用同步注册表 ID（与区块数据一致）。
var (
	biomeDeepOcean   = mustBiome("minecraft:deep_ocean")
	biomeOcean       = mustBiome("minecraft:ocean")
	biomeBeach       = mustBiome("minecraft:beach")
	biomeForest      = mustBiome("minecraft:forest")
	biomeTaiga       = mustBiome("minecraft:taiga")
	biomeSnowyPlains = mustBiome("minecraft:snowy_plains")
	biomeSnowyTaiga  = mustBiome("minecraft:snowy_taiga")
	biomeDesert      = mustBiome("minecraft:desert")
	biomeSavanna     = mustBiome("minecraft:savanna")
	biomeSwamp       = mustBiome("minecraft:swamp")
	biomeJungle      = mustBiome("minecraft:jungle")
)

// mustBiome 查询同步注册表中的群系 ID；缺少必需群系属于部署错误，立即失败。
func mustBiome(name string) uint16 {
	id, ok := registry.SyncEntryID("minecraft:worldgen/biome", name)
	if !ok {
		panic("world: 同步注册表缺少群系 " + name)
	}
	return uint16(id)
}

// GenerateChunk 实现 Generator。
// 返回的区块尚未发布（World.Chunk 在锁内生成后才加入缓存），
// 因此生成过程使用无锁写入。
func (g SeededGenerator) GenerateChunk(chunkX, chunkZ int) *Chunk {
	chunk := NewChunk(chunkX, chunkZ)
	baseX := chunkX * SectionSize
	baseZ := chunkZ * SectionSize

	// 高度与群系先按列计算（地表与装饰逻辑复用）。
	var heights [SectionSize * SectionSize]int
	var biomes [SectionSize * SectionSize]uint16
	for localX := 0; localX < SectionSize; localX++ {
		for localZ := 0; localZ < SectionSize; localZ++ {
			worldX, worldZ := baseX+localX, baseZ+localZ
			height := terrainHeight(g.Seed, worldX, worldZ)
			heights[localX*SectionSize+localZ] = height
			biomes[localX*SectionSize+localZ] = g.biomeAt(worldX, worldZ, height)
		}
	}

	for localX := 0; localX < SectionSize; localX++ {
		for localZ := 0; localZ < SectionSize; localZ++ {
			height := heights[localX*SectionSize+localZ]
			biome := biomes[localX*SectionSize+localZ]

			chunk.setBlockStateDirect(localX, WorldMinY, localZ, BedrockBlock)
			// 石头填充到最上三层之下。
			for y := WorldMinY + 1; y <= height-4; y++ {
				chunk.setBlockStateDirect(localX, y, localZ, StoneBlock)
			}
			firstLayer := max(height-3, WorldMinY+1)
			switch {
			case biome == biomeDesert, height <= SeaLevel+1:
				// 沙漠与水下/水边：表层为沙。
				for y := firstLayer; y <= height; y++ {
					chunk.setBlockStateDirect(localX, y, localZ, SandBlock)
				}
			default:
				for y := firstLayer; y < height; y++ {
					chunk.setBlockStateDirect(localX, y, localZ, DirtBlock)
				}
				chunk.setBlockStateDirect(localX, height, localZ, GrassBlock)
			}
			// 水：从地面往上填满到海平面。
			for y := height + 1; y <= SeaLevel; y++ {
				chunk.setBlockStateDirect(localX, y, localZ, WaterBlock)
			}
		}
	}
	// 4×4 列分辨率的群系网格（与网络区块数据一致）。
	var grid [16]uint16
	for localX := 0; localX < SectionSize; localX++ {
		for localZ := 0; localZ < SectionSize; localZ++ {
			grid[biomeCellIndex(localX>>2, localZ>>2)] = biomes[localX*SectionSize+localZ]
		}
	}
	chunk.SetBiomeGrid(grid)
	g.decorate(chunk, heights[:], biomes[:])
	return chunk
}

// SurfaceY 实现 Generator。
func (g SeededGenerator) SurfaceY(x, z int) int {
	return terrainHeight(g.Seed, x, z)
}

// biomeAt 返回 (x, z) 处的群系 ID。高度参与温度修正：越高越冷，
// 山顶自然过渡到雪原。海面附近为海洋/深海/沙滩。
func (g SeededGenerator) biomeAt(x, z, height int) uint16 {
	switch {
	case height < SeaLevel-7:
		return biomeDeepOcean
	case height < SeaLevel-1:
		return biomeOcean
	case height <= SeaLevel+1:
		return biomeBeach
	}
	temp := valueNoise(g.Seed^noiseSaltTemp, x, z, 256)
	hum := valueNoise(g.Seed^noiseSaltHumid, x, z, 192)
	// 海拔修正：72 之上每格降温 0.006（约 110 高度进入雪线）。
	temp -= math.Max(0, float64(height-72)) * 0.006
	switch {
	case temp < 0.30:
		if hum < 0.62 {
			return biomeSnowyPlains
		}
		return biomeSnowyTaiga
	case temp < 0.62:
		switch {
		case hum < 0.42:
			return BiomePlains
		case hum < 0.72:
			return biomeForest
		default:
			return biomeSwamp
		}
	default:
		switch {
		case hum < 0.42:
			return biomeDesert
		case hum < 0.72:
			return biomeSavanna
		default:
			return biomeJungle
		}
	}
}

// decorate 按群系放置植被：树木（橡树/云杉/金合欢/高橡树）、仙人掌、
// 枯灌木、草丛与雪层。树冠半径最大 2 格，因此只在距区块边缘至少 2 格的
// 列上生长，避免需要修改相邻区块。只操作刚生成的未发布区块（无锁读写）。
func (g SeededGenerator) decorate(chunk *Chunk, heights []int, biomes []uint16) {
	baseX := chunk.X * SectionSize
	baseZ := chunk.Z * SectionSize
	for localX := 0; localX < SectionSize; localX++ {
		for localZ := 0; localZ < SectionSize; localZ++ {
			idx := localX*SectionSize + localZ
			height, biome := heights[idx], biomes[idx]
			worldX, worldZ := baseX+localX, baseZ+localZ
			land := height > SeaLevel+1

			// 树木/仙人掌：距边缘 2 格，避免树冠跨区块。
			if land && localX >= 2 && localX <= SectionSize-3 && localZ >= 2 && localZ <= SectionSize-3 {
				switch biome {
				case biomeForest:
					if hashUnit(g.Seed^treeSalt, worldX, worldZ) < 0.04 {
						g.placeOak(chunk, localX, localZ, height, worldX, worldZ, 4, 6)
					}
				case biomeSwamp:
					if hashUnit(g.Seed^treeSalt, worldX, worldZ) < 0.02 {
						g.placeOak(chunk, localX, localZ, height, worldX, worldZ, 4, 5)
					}
				case biomeJungle:
					if hashUnit(g.Seed^treeSalt, worldX, worldZ) < 0.05 {
						g.placeOak(chunk, localX, localZ, height, worldX, worldZ, 7, 9)
					}
				case biomeTaiga, biomeSnowyTaiga:
					if hashUnit(g.Seed^spruceSalt, worldX, worldZ) < 0.04 {
						g.placeSpruce(chunk, localX, localZ, height, worldX, worldZ)
					}
				case biomeSavanna:
					if hashUnit(g.Seed^acaciaSalt, worldX, worldZ) < 0.012 {
						g.placeAcacia(chunk, localX, localZ, height, worldX, worldZ)
					}
				case BiomePlains, biomeSnowyPlains:
					if hashUnit(g.Seed^treeSalt, worldX, worldZ) < 0.002 {
						g.placeOak(chunk, localX, localZ, height, worldX, worldZ, 4, 6)
					}
				case biomeDesert:
					if hashUnit(g.Seed^cactusSalt, worldX, worldZ) < 0.012 {
						g.placeCactus(chunk, localX, localZ, height, worldX, worldZ)
					} else if hashUnit(g.Seed^decorSalt, worldX, worldZ) < 0.02 {
						chunk.setBlockStateDirect(localX, height+1, localZ, DeadBushBlock)
					}
				}
			}

			// 草丛/雪层：整列（不需要留白；已被树占据的格子跳过）。
			if !land || chunk.getBlockStateLocked(localX, height+1, localZ) != AirBlock {
				continue
			}
			switch biome {
			case biomeSnowyPlains, biomeSnowyTaiga:
				chunk.setBlockStateDirect(localX, height+1, localZ, SnowLayerBlock)
			case BiomePlains, biomeForest, biomeSwamp, biomeTaiga, biomeJungle, biomeSavanna:
				if chunk.getBlockStateLocked(localX, height, localZ) == GrassBlock &&
					hashUnit(g.Seed^decorSalt, worldX, worldZ) < 0.1 {
					chunk.setBlockStateDirect(localX, height+1, localZ, ShortGrassBlock)
				}
			}
		}
	}
}

// surfaceIsGrass 报告 (localX, localZ) 的地表是否为草方块（树木生长条件）。
func surfaceIsGrass(chunk *Chunk, localX, height, localZ int) bool {
	return chunk.getBlockStateLocked(localX, height, localZ) == GrassBlock
}

// placeOak 在草面上放置橡树/高橡树（trunkMin–trunkMax 高的树干 + 阔树冠）。
func (g SeededGenerator) placeOak(chunk *Chunk, localX, localZ, height, worldX, worldZ, trunkMin, trunkMax int) {
	if !surfaceIsGrass(chunk, localX, height, localZ) {
		return
	}
	trunk := trunkMin + int(hashUnit(g.Seed^trunkSalt, worldX, worldZ)*float64(trunkMax-trunkMin+1))
	if trunk > trunkMax {
		trunk = trunkMax
	}
	top := height + trunk
	for y := height + 1; y <= top; y++ {
		chunk.setBlockStateDirect(localX, y, localZ, OakLogBlock)
	}
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
				g.putLeaf(chunk, localX+dx, top+dy, localZ+dz, OakLeavesBlock)
			}
		}
	}
}

// placeSpruce 在草面上放置云杉（高树干 + 圆锥树冠）。
func (g SeededGenerator) placeSpruce(chunk *Chunk, localX, localZ, height, worldX, worldZ int) {
	if !surfaceIsGrass(chunk, localX, height, localZ) {
		return
	}
	trunk := 5 + int(hashUnit(g.Seed^trunkSalt, worldX, worldZ)*4) // 5-8
	top := height + trunk
	for y := height + 1; y <= top; y++ {
		chunk.setBlockStateDirect(localX, y, localZ, SpruceLogBlock)
	}
	for dy := -3; dy <= 1; dy++ {
		radius := 0
		switch {
		case dy <= -2:
			radius = 2
		case dy <= 0:
			radius = 1
		}
		for dx := -radius; dx <= radius; dx++ {
			for dz := -radius; dz <= radius; dz++ {
				if dx == 0 && dz == 0 && dy <= 0 {
					continue
				}
				if radius == 2 && abs(dx) == 2 && abs(dz) == 2 {
					continue // 裁角
				}
				g.putLeaf(chunk, localX+dx, top+dy, localZ+dz, SpruceLeavesBlock)
			}
		}
	}
}

// placeAcacia 在草面上放置金合欢（高树干 + 平顶树冠）。
func (g SeededGenerator) placeAcacia(chunk *Chunk, localX, localZ, height, worldX, worldZ int) {
	if !surfaceIsGrass(chunk, localX, height, localZ) {
		return
	}
	trunk := 4 + int(hashUnit(g.Seed^trunkSalt, worldX, worldZ)*3) // 4-6
	top := height + trunk
	for y := height + 1; y <= top; y++ {
		chunk.setBlockStateDirect(localX, y, localZ, AcaciaLogBlock)
	}
	for dy := 0; dy <= 1; dy++ {
		radius := 2
		if dy == 1 {
			radius = 1
		}
		for dx := -radius; dx <= radius; dx++ {
			for dz := -radius; dz <= radius; dz++ {
				if dx == 0 && dz == 0 && dy == 0 {
					continue
				}
				if radius == 2 && abs(dx) == 2 && abs(dz) == 2 {
					continue // 裁角
				}
				g.putLeaf(chunk, localX+dx, top+dy, localZ+dz, AcaciaLeavesBlock)
			}
		}
	}
}

// placeCactus 在沙面上放置 1–3 格高的仙人掌。
func (g SeededGenerator) placeCactus(chunk *Chunk, localX, localZ, height, worldX, worldZ int) {
	if chunk.getBlockStateLocked(localX, height, localZ) != SandBlock {
		return
	}
	h := 1 + int(hashUnit(g.Seed^trunkSalt, worldX, worldZ)*3) // 1-3
	for y := height + 1; y <= height+h; y++ {
		chunk.setBlockStateDirect(localX, y, localZ, CactusBlock)
	}
}

// putLeaf 在空气位置放置树叶（越界或已有方块时跳过）。
func (g SeededGenerator) putLeaf(chunk *Chunk, x, y, z int, state uint16) {
	if x < 0 || x >= SectionSize || z < 0 || z >= SectionSize {
		return
	}
	if chunk.getBlockStateLocked(x, y, z) != AirBlock {
		return
	}
	chunk.setBlockStateDirect(x, y, z, state)
}

// terrainHeight 返回 (x, z) 处地面（最高固体方块）的 Y 坐标。
// 大陆度噪声决定基准（深海底 → 平原），高大陆度叠加山脊噪声形成山地，
// 丘陵与细节噪声提供中小尺度起伏；低频的温度/湿度噪声另行决定群系。
func terrainHeight(seed int64, x, z int) int {
	cont := valueNoise(seed^noiseSaltCont, x, z, 256)
	hills := valueNoise(seed^noiseSaltHills, x, z, 64)
	detail := valueNoise(seed^noiseSaltDetail, x, z, 12)
	ridge := valueNoise(seed^noiseSaltRidge, x, z, 96)

	// 大陆度 → 基准高度：cont ≲ 0.30 为海床，cont ≳ 0.52 为平原基准。
	land := smoothstep(clamp01((cont - 0.30) / 0.22))
	base := float64(oceanFloorDepth) + land*float64(landBaseHeight-oceanFloorDepth)
	// 山地：只在高大陆度出现；山脊噪声取绝对值折线，形成尖峰。
	mtnMask := smoothstep(clamp01((cont - 0.55) / 0.25))
	ridgeV := 1 - math.Abs(2*ridge-1)
	height := base +
		(hills-0.5)*hillAmplitude*land +
		(detail-0.5)*detailAmplitude +
		mtnMask*ridgeV*ridgeV*mountainAmplitude
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

// clamp01 把值限制到 [0, 1]。
func clamp01(t float64) float64 {
	if t < 0 {
		return 0
	}
	if t > 1 {
		return 1
	}
	return t
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
