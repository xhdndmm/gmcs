package world

import (
	"math"

	"gmcs/internal/registry"
)

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
	// riverWidth 是河流噪声带宽度（|2n-1| 小于该值处开始下切）。
	riverWidth = 0.045
	// riverBottomDepth 是河床相对海平面的深度。
	riverBottomDepth = 4
	// deepslateTop 是深板岩层的顶部 Y（该高度以下用深板岩）。
	deepslateTop = 0
)

// 噪声与装饰的盐（任意互不相同的奇数即可，需在 int64 范围内）。
const (
	noiseSaltCont    = 0x1E3779B97F4A7C15 // 大陆度（极低频）
	noiseSaltHills   = 0x42B2AE3D27D4EB4F // 丘陵
	noiseSaltDetail  = 0x165667B19E3779F9 // 细节
	noiseSaltRidge   = 0x27220A95B76E3C2D // 山脊
	noiseSaltTemp    = 0x632BE59BD9B4E019 // 温度
	noiseSaltHumid   = 0x53F9EE8D2B2A6F31 // 湿度
	noiseSaltRiver   = 0x0FEDCBA987654321 // 河流
	noiseSaltVariant = 0x7C15E5D26B3A9011 // 群系变体（向日葵平原/繁花森林/恶地）

	treeSalt   = 0x5DEECE66D
	trunkSalt  = 0x2545F4914F6CDD1D
	decorSalt  = 0x5A09E667F3BCC909
	cactusSalt = 0x5C2B2AE3D27D4EB4
	spruceSalt = 0x165667B19E3779FA
	acaciaSalt = 0x27220A95B76E3C2E
	birchSalt  = 0x3C6EF372FE94F82B
	darkSalt   = 0x13198A2E03707344
	jungleSalt = 0x6A09E667F3BCC908
	flowerSalt = 0x1F83D9ABFB41BD6B
	snowSalt   = 0x5BE0CD19137E2179
)

// 群系 ID（同步注册表 minecraft:worldgen/biome 的条目 ID）。
// 世界数据中的群系必须使用同步注册表 ID（与区块数据一致）。
var (
	biomeDeepOcean       = mustBiome("minecraft:deep_ocean")
	biomeOcean           = mustBiome("minecraft:ocean")
	biomeBeach           = mustBiome("minecraft:beach")
	biomeSnowyBeach      = mustBiome("minecraft:snowy_beach")
	biomeStonyShore      = mustBiome("minecraft:stony_shore")
	biomeRiver           = mustBiome("minecraft:river")
	biomeForest          = mustBiome("minecraft:forest")
	biomeFlowerForest    = mustBiome("minecraft:flower_forest")
	biomeBirchForest     = mustBiome("minecraft:birch_forest")
	biomeDarkForest      = mustBiome("minecraft:dark_forest")
	biomeTaiga           = mustBiome("minecraft:taiga")
	biomeSnowyPlains     = mustBiome("minecraft:snowy_plains")
	biomeSnowyTaiga      = mustBiome("minecraft:snowy_taiga")
	biomeDesert          = mustBiome("minecraft:desert")
	biomeBadlands        = mustBiome("minecraft:badlands")
	biomeSavanna         = mustBiome("minecraft:savanna")
	biomeSwamp           = mustBiome("minecraft:swamp")
	biomeJungle          = mustBiome("minecraft:jungle")
	biomeSunflowerPlains = mustBiome("minecraft:sunflower_plains")
	biomeMeadow          = mustBiome("minecraft:meadow")
	biomeGrove           = mustBiome("minecraft:grove")
	biomeSnowySlopes     = mustBiome("minecraft:snowy_slopes")
	biomeFrozenPeaks     = mustBiome("minecraft:frozen_peaks")
	biomeStonyPeaks      = mustBiome("minecraft:stony_peaks")
)

// mustBiome 查询同步注册表中的群系 ID；缺少必需群系属于部署错误，立即失败。
func mustBiome(name string) uint16 {
	id, ok := registry.SyncEntryID("minecraft:worldgen/biome", name)
	if !ok {
		panic("world: 同步注册表缺少群系 " + name)
	}
	return uint16(id)
}

// SeededGenerator 按种子生成主世界地形：
//
//   - 大陆度/山脊/丘陵/细节四层值噪声构成高度图（深海—海洋—沙滩—平原—
//     丘陵—山地—雪峰），河流噪声在陆地上切出通往海平面的河道；
//   - 温度/湿度噪声 + 海拔降温划分 24 种群系（含雪原、针叶林、白桦林、
//     黑森林、沼泽、丛林、稀树草原、沙漠、恶地、山地各群系等）；
//   - 地下包含奶酪洞穴（3D 噪声）与蠕虫隧道（随机游走），深处洞穴填充岩浆；
//   - 8 种矿物（煤矿—绿宝石，含深板岩变体）按深度与数量确定性生成；
//   - 群系化地表（草/沙/红沙/陶瓦/雪/石头）与植被（各类树木、仙人掌、
//     枯灌木、草丛、花、睡莲、甘蔗等）。
//
// 该地形与原版世界生成在观感上相近，但不是原版算法：不含原版噪声域扭曲、
// 含水层、结构与地物（村庄/要塞/矿洞等）。所有内容都是种子与坐标的
// 确定纯函数：相同种子与坐标生成完全相同的区块。
type SeededGenerator struct {
	Seed int64
}

// Dimension 实现 Generator。
func (SeededGenerator) Dimension() Dimension {
	return DimensionOverworld
}

// columnData 是一列的地形信息（高度与群系）。
type columnData struct {
	height int
	biome  uint16
}

// GenerateChunk 实现 Generator。
// 返回的区块尚未发布（World.Chunk 在锁内生成后才加入缓存），
// 因此生成过程使用无锁写入：地形填充、洞穴雕刻与装饰都是单线程操作。
func (g SeededGenerator) GenerateChunk(chunkX, chunkZ int) *Chunk {
	chunk := NewChunkDim(chunkX, chunkZ, DimensionOverworld)
	baseX := chunkX * SectionSize
	baseZ := chunkZ * SectionSize

	// 高度与群系先按列计算（地表、洞穴与装饰逻辑复用）。
	var columns [SectionSize * SectionSize]columnData
	maxHeight := WorldMinY
	for localX := 0; localX < SectionSize; localX++ {
		for localZ := 0; localZ < SectionSize; localZ++ {
			worldX, worldZ := baseX+localX, baseZ+localZ
			height := terrainHeight(g.Seed, worldX, worldZ)
			columns[localX*SectionSize+localZ] = columnData{
				height: height,
				biome:  g.biomeAt(worldX, worldZ, height),
			}
			if height > maxHeight {
				maxHeight = height
			}
		}
	}

	// 基础地形：基岩层 + 石头/深板岩 + 表层 + 水。
	// 先预分配将要写入的 section（减少位宽重打包）。
	if maxSection, ok := SectionIndex(maxHeight); ok {
		for index := 0; index <= maxSection; index++ {
			chunk.PrepareGenerationSection(index)
		}
	}
	for localX := 0; localX < SectionSize; localX++ {
		for localZ := 0; localZ < SectionSize; localZ++ {
			column := columns[localX*SectionSize+localZ]
			height, biome := column.height, column.biome

			chunk.setBlockStateDirect(localX, WorldMinY, localZ, BedrockBlock)
			for y := WorldMinY + 1; y <= height-4; y++ {
				if y < deepslateTop {
					chunk.setBlockStateDirect(localX, y, localZ, DeepslateBlock)
				} else {
					chunk.setBlockStateDirect(localX, y, localZ, StoneBlock)
				}
			}
			g.fillSurface(chunk, localX, localZ, height, biome)
			// 水：从地面往上填满到海平面。
			for y := height + 1; y <= SeaLevel; y++ {
				chunk.setBlockStateDirect(localX, y, localZ, WaterBlock)
			}
		}
	}

	// 洞穴雕刻（在基础地形之后、矿物之前：矿物不会悬空出现在洞穴里，
	// 但洞穴会穿过矿脉边缘，观感更接近原版）。
	g.carveCaves(chunk, &columns, maxHeight)

	// 矿物。
	placeOres(chunk, g.Seed, &columns)

	// 4×4 列分辨率的群系网格（与网络区块数据一致）。
	var grid [16]uint16
	for localX := 0; localX < SectionSize; localX++ {
		for localZ := 0; localZ < SectionSize; localZ++ {
			grid[biomeCellIndex(localX>>2, localZ>>2)] = columns[localX*SectionSize+localZ].biome
		}
	}
	chunk.SetBiomeGrid(grid)
	g.decorate(chunk, &columns)
	return chunk
}

// fillSurface 填充地表层（高度 height 以上由调用方填水）。
// 不同群系的地表材质不同：草/沙/红沙/陶瓦/雪/石头/砾石等。
func (g SeededGenerator) fillSurface(chunk *Chunk, localX, localZ, height int, biome uint16) {
	set := func(y int, state uint16) { chunk.setBlockStateDirect(localX, y, localZ, state) }
	firstLayer := max(height-3, WorldMinY+1)

	switch {
	case biome == biomeDeepOcean || biome == biomeOcean:
		// 海底：砾石 + 黏土斑块 + 沙（靠近岸边）。
		worldX, worldZ := chunk.X*SectionSize+localX, chunk.Z*SectionSize+localZ
		for y := firstLayer; y <= height; y++ {
			state := GravelBlock
			switch {
			case y == height && hashUnit(g.Seed^decorSalt, worldX, worldZ) < 0.3:
				state = ClayBlock
			case height >= SeaLevel-4:
				state = SandBlock
			}
			set(y, state)
		}
	case biome == biomeBeach || biome == biomeSnowyBeach || biome == biomeRiver:
		for y := firstLayer; y <= height; y++ {
			set(y, SandBlock)
		}
	case biome == biomeStonyShore:
		for y := firstLayer; y <= height; y++ {
			state := StoneBlock
			if hashUnit(g.Seed^0x44, localX, localZ) < 0.35 {
				state = GravelBlock
			}
			set(y, state)
		}
	case biome == biomeDesert:
		for y := firstLayer; y <= height; y++ {
			set(y, SandBlock)
		}
	case biome == biomeBadlands:
		// 红沙表层 + 陶瓦条带（4–8 层一循环）。
		set(height, RedSandBlock)
		for y := max(height-1, WorldMinY+1); y >= firstLayer; y-- {
			if (height-y)%5 == 0 {
				set(y, TerracottaBlock)
			} else {
				set(y, RedSandBlock)
			}
		}
	case biome == biomeStonyPeaks || biome == biomeFrozenPeaks:
		for y := firstLayer; y <= height; y++ {
			set(y, StoneBlock)
		}
	case biome == biomeSnowySlopes:
		for y := firstLayer; y < height; y++ {
			set(y, StoneBlock)
		}
		set(height, SnowBlock)
	case biome == biomeGrove:
		for y := firstLayer; y < height; y++ {
			set(y, StoneBlock)
		}
		set(height, SnowBlock)
	case biome == biomeMeadow && height > 96:
		for y := firstLayer; y < height; y++ {
			set(y, StoneBlock)
		}
		set(height, GrassBlock)
	case biome == biomeSnowyPlains || biome == biomeSnowyTaiga:
		for y := firstLayer; y < height; y++ {
			set(y, DirtBlock)
		}
		set(height, GrassBlock)
	default:
		for y := firstLayer; y < height; y++ {
			set(y, DirtBlock)
		}
		set(height, GrassBlock)
	}
}

// SurfaceY 实现 Generator。
func (g SeededGenerator) SurfaceY(x, z int) int {
	return terrainHeight(g.Seed, x, z)
}

// riverFactor 返回 (x, z) 处的河流强度 [0, 1]：越大表示越接近河道中心。
// 只在陆地上生效（由调用方判断高度）。
func (g SeededGenerator) riverFactor(x, z int) float64 {
	river := valueNoise(g.Seed^noiseSaltRiver, x, z, 232)
	distance := math.Abs(2*river - 1)
	if distance >= riverWidth {
		return 0
	}
	return smoothstep(1 - distance/riverWidth)
}

// snowLine 返回该温度下的雪线高度：越热雪线越高。
func snowLine(temp float64) float64 {
	return 84 + temp*52
}

// biomeAt 返回 (x, z) 处的群系 ID，height 为该列地形高度。
// 选择顺序：海洋 → 河流 → 海滩/石岸 → 雪线以上山地 → 高山 →
// 温度/湿度矩阵（含变体群系）。
func (g SeededGenerator) biomeAt(x, z, height int) uint16 {
	if height < SeaLevel-7 {
		return biomeDeepOcean
	}
	if height < SeaLevel-1 {
		return biomeOcean
	}

	temp := valueNoise(g.Seed^noiseSaltTemp, x, z, 256)
	hum := valueNoise(g.Seed^noiseSaltHumid, x, z, 192)
	// 海拔修正：越高越冷（约 150 高度处降温 0.3）。
	temp -= math.Max(0, float64(height-80)) * 0.004

	// 河流：河道下切到海平面之下，河面附近判定为河流群系。
	if height <= 98 {
		if factor := g.riverFactor(x, z); factor > 0.35 && height <= SeaLevel+1 {
			return biomeRiver
		}
	}

	if height <= SeaLevel+1 {
		switch {
		case temp < 0.30:
			return biomeSnowyBeach
		case hum < 0.30:
			return biomeStonyShore
		default:
			return biomeBeach
		}
	}

	// 高山群系：按海拔与温度过渡。
	line := snowLine(temp)
	switch {
	case float64(height) > line+34:
		return biomeFrozenPeaks
	case float64(height) > line+12:
		return biomeSnowySlopes
	case float64(height) > line+4:
		return biomeGrove
	case float64(height) > line:
		if temp < 0.5 {
			return biomeGrove
		}
		return biomeMeadow
	case height > 104 && temp < 0.6:
		return biomeStonyPeaks
	case height > 92 && temp >= 0.35:
		return biomeMeadow
	}

	// 温度/湿度矩阵。
	switch {
	case temp < 0.30: // 严寒
		if hum < 0.55 {
			return biomeSnowyPlains
		}
		return biomeSnowyTaiga
	case temp < 0.55: // 寒冷
		switch {
		case hum < 0.35:
			return biomeTaiga
		case hum < 0.62:
			return biomeBirchForest
		default:
			return biomeDarkForest
		}
	case temp < 0.78: // 温带
		variant := hashUnit(g.Seed^noiseSaltVariant, x, z)
		switch {
		case hum < 0.28:
			if variant < 0.05 {
				return biomeSunflowerPlains
			}
			return BiomePlains
		case hum < 0.52:
			if variant < 0.06 {
				return biomeFlowerForest
			}
			return biomeForest
		case hum < 0.70:
			return biomeBirchForest
		case hum < 0.86:
			return biomeDarkForest
		default:
			return biomeSwamp
		}
	default: // 炎热
		variant := hashUnit(g.Seed^noiseSaltVariant, x, z)
		switch {
		case hum < 0.30:
			if variant < 0.10 {
				return biomeBadlands
			}
			return biomeDesert
		case hum < 0.58:
			return biomeSavanna
		default:
			return biomeJungle
		}
	}
}

// terrainHeight 返回 (x, z) 处地面（最高固体方块）的 Y 坐标。
// 大陆度噪声决定基准（深海底 → 平原），高大陆度叠加山脊噪声形成山地，
// 丘陵与细节噪声提供中小尺度起伏；河流噪声在陆地上切出河道。
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

	// 河流：把陆地压低到海平面之下（高处山地不受影响）。
	if height > float64(SeaLevel-4) && height <= 98 {
		river := valueNoise(seed^noiseSaltRiver, x, z, 232)
		distance := math.Abs(2*river - 1)
		if distance < riverWidth {
			factor := smoothstep(1 - distance/riverWidth)
			target := float64(SeaLevel - riverBottomDepth)
			height -= (height - target) * factor
		}
	}
	return int(math.Floor(height))
}

// noise01 是 valueNoise 的便捷封装（固定周期 64 的单通道噪声）。
func (g SeededGenerator) noise01(salt int64, x, z int) float64 {
	return valueNoise(g.Seed^salt, x, z, 64)
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

// valueNoise3D 是周期为 period 的三维值噪声（三线性插值 + smoothstep）。
// 洞穴采样用粗网格调用它，不用于逐方块热路径。
func valueNoise3D(seed int64, x, y, z, period int) float64 {
	fx := float64(x) / float64(period)
	fy := float64(y) / float64(period)
	fz := float64(z) / float64(period)
	x0, y0, z0 := int(math.Floor(fx)), int(math.Floor(fy)), int(math.Floor(fz))
	tx := smoothstep(fx - float64(x0))
	ty := smoothstep(fy - float64(y0))
	tz := smoothstep(fz - float64(z0))

	v000 := hashUnit3(seed, x0, y0, z0)
	v100 := hashUnit3(seed, x0+1, y0, z0)
	v010 := hashUnit3(seed, x0, y0+1, z0)
	v110 := hashUnit3(seed, x0+1, y0+1, z0)
	v001 := hashUnit3(seed, x0, y0, z0+1)
	v101 := hashUnit3(seed, x0+1, y0, z0+1)
	v011 := hashUnit3(seed, x0, y0+1, z0+1)
	v111 := hashUnit3(seed, x0+1, y0+1, z0+1)

	bottom := lerp(lerp(v000, v100, tx), lerp(v010, v110, tx), ty)
	top := lerp(lerp(v001, v101, tx), lerp(v011, v111, tx), ty)
	return lerp(bottom, top, tz)
}

func smoothstep(t float64) float64 {
	return t * t * (3 - 2*t)
}

func lerp(a, b, t float64) float64 {
	return a + (b-a)*t
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

// hashUnit3 把 (seed, x, y, z) 混合为 [0, 1) 的确定性伪随机数。
func hashUnit3(seed int64, x, y, z int) float64 {
	h := uint64(seed)*0x9E3779B97F4A7C15 +
		uint64(int64(x))*0xBF58476D1CE4E5B9 +
		uint64(int64(y))*0x94D049BB133111EB +
		uint64(int64(z))*0xD6E8FEB86659FD93
	h ^= h >> 29
	h *= 0xBF58476D1CE4E5B9
	h ^= h >> 32
	return float64(h>>11) / float64(uint64(1)<<53)
}

// hashInt 把 (seed, a, b) 混合为 64 位确定性伪随机整数。
func hashInt(seed int64, a, b int) uint64 {
	h := uint64(seed)*0x9E3779B97F4A7C15 +
		uint64(int64(a))*0xBF58476D1CE4E5B9 +
		uint64(int64(b))*0x94D049BB133111EB
	h ^= h >> 30
	h *= 0xBF58476D1CE4E5B9
	h ^= h >> 27
	h *= 0x94D049BB133111EB
	return h ^ (h >> 31)
}

// hashSeq 是基于 splitmix64 的确定性随机数序列（洞穴/矿物生成用）。
type hashSeq struct {
	state uint64
}

// newHashSeq 用 (seed, a, b) 构造随机序列。
func newHashSeq(seed int64, a, b int) *hashSeq {
	state := hashInt(seed, a, b)
	if state == 0 {
		state = 0x9E3779B97F4A7C15
	}
	return &hashSeq{state: state}
}

// next 返回下一个伪随机数。
func (s *hashSeq) next() uint64 {
	s.state += 0x9E3779B97F4A7C15
	z := s.state
	z ^= z >> 30
	z *= 0xBF58476D1CE4E5B9
	z ^= z >> 27
	z *= 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// nextInt 返回 [0, n) 的伪随机整数（n <= 0 时返回 0）。
func (s *hashSeq) nextInt(n int) int {
	if n <= 0 {
		return 0
	}
	return int(s.next() % uint64(n))
}

// nextFloat 返回 [0, 1) 的伪随机浮点数。
func (s *hashSeq) nextFloat() float64 {
	return float64(s.next()>>11) / float64(uint64(1)<<53)
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
