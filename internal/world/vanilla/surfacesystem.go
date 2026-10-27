//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"encoding/json"
	"math"
)

// 本文件实现原版 SurfaceSystem（1.21.11 混淆名 evi）的区块地表构建流程。
//
// 上下文（evh$g）的字段与原版一一对应：
//
//	r/s      → blockX/blockZ
//	t        → minSurfaceLevel（SurfaceSystem.getMinSurfaceLevel）
//	B        → blockY
//	C        → stoneDepthBelow（从上往下遇到的流体顶部 y+1，没有则为 MIN_VALUE）
//	D        → surfaceDepth（y - minY + 1）
//	E        → stoneDepthAbove（从顶往下的连续实心方块数）
//	x/c()    → preliminarySurfaceLevel（相邻四个区块初步地表高度的双线性插值 - 8）
//
// 原版把部分条件按"上下文更新计数"缓存（evh$i 的 a/y 字段），这里不做缓存：
// 这些条件都是纯函数，跳过的只是性能优化，结果逐位相同。

// surfaceSystemNoises 是原版 SurfaceSystem 构造函数里取用的噪声键。
var surfaceSystemNoiseNames = struct {
	surface, surfaceSecondary, clayBandsOffset          string
	badlandsPillar, badlandsPillarRoof, badlandsSurface string
	icebergPillar, icebergPillarRoof, icebergSurface    string
}{
	surface:            "minecraft:surface",
	surfaceSecondary:   "minecraft:surface_secondary",
	clayBandsOffset:    "minecraft:clay_bands_offset",
	badlandsPillar:     "minecraft:badlands_pillar",
	badlandsPillarRoof: "minecraft:badlands_pillar_roof",
	badlandsSurface:    "minecraft:badlands_surface",
	icebergPillar:      "minecraft:iceberg_pillar",
	icebergPillarRoof:  "minecraft:iceberg_pillar_roof",
	icebergSurface:     "minecraft:iceberg_surface",
}

// SurfaceSystem 等价原版 SurfaceSystem：把密度/含水层的结果按 surface_rule 换成
// 最终方块（草、沙、深板岩、冰等）。
type SurfaceSystem struct {
	setting      NoiseSetting
	defaultBlock string
	seaLevel     int
	positional   positionalRandomFactory
	preliminary  func(x, z int) int

	noiseSurface            *NormalNoise
	noiseSurfaceSecondary   *NormalNoise
	noiseClayBandsOffset    *NormalNoise
	noiseBadlandsPillar     *NormalNoise
	noiseBadlandsPillarRoof *NormalNoise
	noiseBadlandsSurface    *NormalNoise
	noiseIcebergPillar      *NormalNoise
	noiseIcebergPillarRoof  *NormalNoise
	noiseIcebergSurface     *NormalNoise

	clayBands []SurfaceBlock
	rule      surfaceRule

	gradientFactories map[string]positionalRandomFactory
}

// NewSurfaceSystem 为给定区块采样器建立地表系统（与含水层共享求值器状态）。
func NewSurfaceSystem(s *ChunkSampler) (*SurfaceSystem, error) {
	setting := s.setting
	var raw struct {
		SurfaceRule json.RawMessage `json:"surface_rule"`
	}
	if err := json.Unmarshal(setting.Raw, &raw); err != nil {
		return nil, err
	}
	registry := s.evaluator.noises
	system := &SurfaceSystem{
		setting:                 setting,
		defaultBlock:            setting.DefaultBlock,
		seaLevel:                setting.SeaLevel,
		positional:              registry.factory,
		preliminary:             s.Aquifer().preliminarySurfaceLevel,
		noiseSurface:            registry.Noise(surfaceSystemNoiseNames.surface),
		noiseSurfaceSecondary:   registry.Noise(surfaceSystemNoiseNames.surfaceSecondary),
		noiseClayBandsOffset:    registry.Noise(surfaceSystemNoiseNames.clayBandsOffset),
		noiseBadlandsPillar:     registry.Noise(surfaceSystemNoiseNames.badlandsPillar),
		noiseBadlandsPillarRoof: registry.Noise(surfaceSystemNoiseNames.badlandsPillarRoof),
		noiseBadlandsSurface:    registry.Noise(surfaceSystemNoiseNames.badlandsSurface),
		noiseIcebergPillar:      registry.Noise(surfaceSystemNoiseNames.icebergPillar),
		noiseIcebergPillarRoof:  registry.Noise(surfaceSystemNoiseNames.icebergPillarRoof),
		noiseIcebergSurface:     registry.Noise(surfaceSystemNoiseNames.icebergSurface),
		gradientFactories:       make(map[string]positionalRandomFactory),
	}
	clayRandom := registry.FromHashOf("clay_bands")
	system.clayBands = buildClayBands(clayRandom)

	if len(raw.SurfaceRule) == 0 {
		return nil, nil
	}
	rule, problems := parseSurfaceRule(registry, setting.Name+"/surface_rule", raw.SurfaceRule)
	if len(problems) > 0 {
		return nil, surfaceError(problems[0])
	}
	system.rule = rule
	return system, nil
}

// surfaceError 把解析问题转成 error。
func surfaceError(problem Problem) error {
	return &surfaceProblemError{problem: problem}
}

// surfaceProblemError 包装地表规则解析错误。
type surfaceProblemError struct{ problem Problem }

func (e *surfaceProblemError) Error() string { return e.problem.String() }

// getMinSurfaceLevel 等价原版 SurfaceSystem.getMinSurfaceLevel。
func (s *SurfaceSystem) getMinSurfaceLevel(x, z int) int {
	value := s.noiseSurface.Value(float64(x), 0, float64(z))*2.75 + 3.0
	value += s.positional.At(x, 0, z).NextDouble() * 0.25
	return int(value)
}

// surfaceDepth 等价原版 SurfaceSystem.getSurfaceDepth。
func (s *SurfaceSystem) surfaceDepth(x, z int) float64 {
	return s.noiseSurfaceSecondary.Value(float64(x), 0, float64(z))
}

// band 等价原版 SurfaceSystem.getBand(x, y, z)。
func (s *SurfaceSystem) band(x, y, z int) SurfaceBlock {
	offset := int(math.Round(s.noiseClayBandsOffset.Value(float64(x), 0, float64(z)) * 4))
	n := len(s.clayBands)
	index := (y + offset + n) % n
	if index < 0 {
		index += n
	}
	return s.clayBands[index]
}

// gradientRandom 等价原版 RandomState.getOrCreateRandomFactory(name).at(x, y, z)。
func (s *SurfaceSystem) gradientRandom(name string, x, y, z int) *Xoroshiro {
	factory, ok := s.gradientFactories[name]
	if !ok {
		factory = s.positional.FromHashOf(name).ForkPositional()
		s.gradientFactories[name] = factory
	}
	return factory.At(x, y, z)
}

// buildClayBands 等价原版 SurfaceSystem.<clinit> 之后的粘土带初始化。
// 颜色顺序按全局静态块字段（WHITE/ORANGE/TERRACOTTA/YELLOW/BROWN/RED/LIGHT_GRAY）。
func buildClayBands(random *Xoroshiro) []SurfaceBlock {
	const (
		whiteTerracotta     = "minecraft:white_terracotta"
		orangeTerracotta    = "minecraft:orange_terracotta"
		terracotta          = "minecraft:terracotta"
		yellowTerracotta    = "minecraft:yellow_terracotta"
		brownTerracotta     = "minecraft:brown_terracotta"
		redTerracotta       = "minecraft:red_terracotta"
		lightGrayTerracotta = "minecraft:light_gray_terracotta"
	)
	bands := make([]SurfaceBlock, 192)
	for i := range bands {
		bands[i] = SurfaceBlock{Name: terracotta}
	}
	for index := 0; index < len(bands); {
		index += random.NextInt(5) + 1
		if index < len(bands) {
			bands[index] = SurfaceBlock{Name: orangeTerracotta}
		}
		index++
	}
	fillClayBands(random, bands, 1, yellowTerracotta)
	fillClayBands(random, bands, 2, brownTerracotta)
	fillClayBands(random, bands, 1, redTerracotta)
	spread := random.NextIntBetween(9, 15)
	for index, cursor := 0, 0; index < spread && cursor < len(bands); index++ {
		bands[cursor] = SurfaceBlock{Name: whiteTerracotta}
		if cursor > 0 && random.NextBoolean() {
			bands[cursor-1] = SurfaceBlock{Name: lightGrayTerracotta}
		}
		if cursor+1 < len(bands) && random.NextBoolean() {
			bands[cursor+1] = SurfaceBlock{Name: lightGrayTerracotta}
		}
		cursor += random.NextInt(16) + 4
	}
	return bands
}

// fillClayBands 等价原版 SurfaceSystem.a(RandomSource, BlockState[], int, BlockState)。
func fillClayBands(random *Xoroshiro, bands []SurfaceBlock, thickness int, name string) {
	layers := random.NextIntBetween(6, 15)
	for i := 0; i < layers; i++ {
		length := thickness + random.NextInt(3)
		start := random.NextInt(len(bands))
		for offset := 0; start+offset < len(bands) && offset < length; offset++ {
			bands[start+offset] = SurfaceBlock{Name: name}
		}
	}
}

// ---- 上下文 ----

// surfaceContext 等价原版 SurfaceRules.Context。
type surfaceContext struct {
	system  *SurfaceSystem
	chunk   *GeneratedChunk
	biomes  *BiomeSource
	rule    surfaceRule
	pending int64

	blockX, blockZ          int
	minSurfaceLevel         int
	preliminarySurfaceLevel int

	blockY          int
	surfaceDepth    int
	stoneDepthAbove int
	stoneDepthBelow int

	biomeName string
	climate   biomeClimate
	hasBiome  bool
}

// updateXZ 等价原版 Context.updateXZ。
func (c *surfaceContext) updateXZ(x, z int) {
	c.blockX = x
	c.blockZ = z
	c.minSurfaceLevel = c.system.getMinSurfaceLevel(x, z)
	c.preliminarySurfaceLevel = c.computePreliminarySurfaceLevel()
	c.hasBiome = false
}

// updateY 等价原版 Context.updateY(stoneDepthAbove, surfaceDepth, stoneDepthBelow, x, y, z)。
func (c *surfaceContext) updateY(stoneDepthAbove, surfaceDepth, stoneDepthBelow, x, y, z int) {
	c.stoneDepthAbove = stoneDepthAbove
	c.surfaceDepth = surfaceDepth
	c.stoneDepthBelow = stoneDepthBelow
	c.blockX = x
	c.blockY = y
	c.blockZ = z
	c.hasBiome = false
}

// computePreliminarySurfaceLevel 等价原版 Context.c()：
// 相邻四个区块初步地表高度的双线性插值，再加上本列 getMinSurfaceLevel 并减去 8。
func (c *surfaceContext) computePreliminarySurfaceLevel() int {
	chunkX := c.blockX >> 4
	chunkZ := c.blockZ >> 4
	p00 := c.system.preliminary(chunkX<<4, chunkZ<<4)
	p10 := c.system.preliminary((chunkX+1)<<4, chunkZ<<4)
	p01 := c.system.preliminary(chunkX<<4, (chunkZ+1)<<4)
	p11 := c.system.preliminary((chunkX+1)<<4, (chunkZ+1)<<4)
	deltaX := float64(c.blockX&15) / 16.0
	deltaZ := float64(c.blockZ&15) / 16.0
	level := int(math.Floor(lerp2(deltaX, deltaZ, float64(p00), float64(p10), float64(p01), float64(p11))))
	const howFarBelowPreliminarySurfaceLevelToBuildSurface = 8
	return level + c.minSurfaceLevel - howFarBelowPreliminarySurfaceLevelToBuildSurface
}

// biome 返回当前方块所在群系（原版 Context 在每方块上用 (x, y, z) 查群系）。
func (c *surfaceContext) biome() string {
	if !c.hasBiome {
		c.biomeName = c.biomes.BiomeAtBlock(c.blockX, c.blockY, c.blockZ)
		c.climate = c.biomes.Climate(c.biomeName)
		c.hasBiome = true
	}
	return c.biomeName
}

// steep 等价原版 Context.steep：相邻列高度差达到 4 格。
func (c *surfaceContext) steep() bool {
	localX := c.blockX & 15
	localZ := c.blockZ & 15
	zMinus := maxInt(localZ-1, 0)
	zPlus := minInt(localZ+1, 15)
	zMinusHeight := c.chunk.HeightAt(c.chunk.MinX+localX, c.chunk.MinZ+zMinus)
	zPlusHeight := c.chunk.HeightAt(c.chunk.MinX+localX, c.chunk.MinZ+zPlus)
	if zPlusHeight >= zMinusHeight+4 {
		return true
	}
	xMinus := maxInt(localX-1, 0)
	xPlus := minInt(localX+1, 15)
	xMinusHeight := c.chunk.HeightAt(c.chunk.MinX+xMinus, c.chunk.MinZ+localZ)
	xPlusHeight := c.chunk.HeightAt(c.chunk.MinX+xPlus, c.chunk.MinZ+localZ)
	return xMinusHeight >= xPlusHeight+4
}

// coldEnoughToSnow 等价原版 Biome.coldEnoughToSnow：温度是否低于 0.15。
//
// **已知限制**（见 docs/TODO.md）：原版这里有两处 PerlinSimplexNoise：
//   - FROZEN 温度修正器：温度 <= 0 时用 FROZEN_TEMPERATURE_NOISE 代替基础温度；
//   - 高度修正：方块高于海平面 + 17 时用 TEMPERATURE_NOISE 降温。
//
// 本实现尚未移植 PerlinSimplexNoise，因此这两处退化为"直接用群系基础温度"。
// overworld 地表规则里 temperature 只用在冰冻海洋分支（方块高度不超过
// 海平面 + 17，高度修正分支不触发），但 FROZEN 噪声项缺失会让冰冻海洋
// 一律判定为"冷"，结冰范围比原版偏大。
func (c *surfaceContext) coldEnoughToSnow() bool {
	return c.climate.temperature < 0.15
}

// lerp2 等价原版 Mth.lerp2。
func lerp2(deltaX, deltaZ, v00, v10, v01, v11 float64) float64 {
	return lerpUnclamped(deltaZ, lerpUnclamped(deltaX, v00, v10), lerpUnclamped(deltaX, v01, v11))
}

// maxInt / minInt 是简单的整数最值。
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
