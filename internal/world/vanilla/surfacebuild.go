//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import "math"

// BuildSurface 等价原版 SurfaceSystem.buildSurface：逐列从地表往下扫描，
// 对 default_block 应用规则树，并在侵蚀恶地/冰冻海洋上做整列扩展。
func (s *SurfaceSystem) BuildSurface(chunk *GeneratedChunk, biomes *BiomeSource) {
	if s.rule == nil {
		return
	}
	ctx := &surfaceContext{system: s, chunk: chunk, biomes: biomes}
	for localX := 0; localX < 16; localX++ {
		for localZ := 0; localZ < 16; localZ++ {
			x := chunk.MinX + localX
			z := chunk.MinZ + localZ
			topY := chunk.HeightAt(x, z) + 1
			// 原版按 settings.useLegacyRandomSource() 在"y=0"与"表层高度"之间选群系
			// 采样高度（overworld 的 legacy_random_source 为 false，取表层高度）。
			biomeName := biomes.BiomeAtBlock(x, topY, z)
			if biomeName == "minecraft:eroded_badlands" {
				s.erodedBadlandsExtension(chunk, x, z, topY)
			}

			ctx.updateXZ(x, z)
			stoneDepthAbove := 0
			stoneDepthBelow := noStoneDepth
			columnTop := math.MaxInt32
			for y := topY; y >= chunk.MinY; y-- {
				state := chunk.BlockAt(x, y, z)
				if state.IsAir() {
					stoneDepthAbove = 0
					stoneDepthBelow = noStoneDepth
					continue
				}
				if state.HasFluid() {
					if stoneDepthBelow == noStoneDepth {
						stoneDepthBelow = y + 1
					}
					continue
				}
				if columnTop >= y {
					columnTop = aquiferWayBelowMinY
					for probe := y - 1; probe >= chunk.MinY-1; probe-- {
						if !isSolidBlock(chunk.BlockAt(x, probe, z)) {
							columnTop = probe + 1
							break
						}
					}
				}
				stoneDepthAbove++
				ctx.updateY(stoneDepthAbove, y-columnTop+1, stoneDepthBelow, x, y, z)
				if state.Name != s.defaultBlock {
					continue
				}
				if result, ok := s.rule.apply(ctx, x, y, z); ok {
					chunk.SetBlock(x, y, z, result)
				}
			}

			if biomeName == "minecraft:frozen_ocean" || biomeName == "minecraft:deep_frozen_ocean" {
				s.icebergExtension(ctx.preliminarySurfaceLevel, chunk, biomeName, x, z, topY)
			}
		}
	}
}

// erodedBadlandsExtension 等价原版 SurfaceSystem 的侵蚀恶地柱状扩展：
// 用 badlands_* 噪声把空腔填成默认方块（形成孤立的红色砂岩柱）。
func (s *SurfaceSystem) erodedBadlandsExtension(chunk *GeneratedChunk, x, z, topY int) {
	strength := math.Min(
		math.Abs(s.noiseBadlandsSurface.Value(float64(x), 0, float64(z))*8.25),
		s.noiseBadlandsPillar.Value(float64(x)*0.2, 0, float64(z)*0.2)*15,
	)
	if strength <= 0 {
		return
	}
	roof := math.Abs(s.noiseBadlandsPillarRoof.Value(float64(x)*0.75, 0, float64(z)*0.75) * 1.5)
	height := 64 + math.Min(strength*strength*2.5, math.Ceil(roof*50)+24)
	top := floorDouble(height)
	if topY > top {
		return
	}
	defaultBlock := SurfaceBlock{Name: s.defaultBlock}
	for y := top; y >= chunk.MinY; y-- {
		state := chunk.BlockAt(x, y, z)
		if state.Name == s.defaultBlock {
			break
		}
		if state.IsWater() {
			return
		}
	}
	for y := top; y >= chunk.MinY; y-- {
		if !chunk.BlockAt(x, y, z).IsAir() {
			break
		}
		chunk.SetBlock(x, y, z, defaultBlock)
	}
}

// icebergExtension 等价原版 SurfaceSystem 的冰冻海洋冰山扩展：
// 按 iceberg_* 噪声把水面附近的空气/水换成浮冰与雪块。
func (s *SurfaceSystem) icebergExtension(minSurfaceLevel int, chunk *GeneratedChunk, biomeName string, x, z, topY int) {
	strength := math.Min(
		math.Abs(s.noiseIcebergSurface.Value(float64(x), 0, float64(z))*8.25),
		s.noiseIcebergPillar.Value(float64(x)*1.28, 0, float64(z)*1.28)*15,
	)
	if strength <= 1.8 {
		return
	}
	roof := math.Abs(s.noiseIcebergPillarRoof.Value(float64(x)*1.17, 0, float64(z)*1.17) * 1.5)
	height := math.Min(strength*strength*1.2, math.Ceil(roof*40)+14)

	// 原版 Biome.shouldMeltFrozenOceanIcebergSlightly：冰冻海洋会让冰山略低。
	if s.biomeMeltsIcebergSlightly(biomeName) {
		height -= 2
	}
	var surfaceLevel, waterTop float64
	if height > 2 {
		waterTop = float64(s.seaLevel) - height - 7
		height += float64(s.seaLevel)
		surfaceLevel = height
	} else {
		height = 0
		waterTop = 0
		surfaceLevel = 0
	}

	random := s.positional.At(x, 0, z)
	snowLayers := 2 + random.NextInt(4)
	snowLimit := s.seaLevel + 18 + random.NextInt(10)
	placed := 0
	start := topY
	if int(surfaceLevel)+1 > start {
		start = int(surfaceLevel) + 1
	}
	for y := start; y >= minSurfaceLevel; y-- {
		state := chunk.BlockAt(x, y, z)
		fill := false
		if state.IsAir() && y < int(surfaceLevel) && random.NextDouble() > 0.01 {
			fill = true
		} else if state.IsWater() && y > int(waterTop) && y < s.seaLevel && waterTop != 0 && random.NextDouble() > 0.15 {
			fill = true
		}
		if !fill {
			continue
		}
		if placed <= snowLayers && y > snowLimit {
			chunk.SetBlock(x, y, z, SurfaceBlock{Name: "minecraft:snow_block"})
			placed++
		} else {
			chunk.SetBlock(x, y, z, SurfaceBlock{Name: "minecraft:packed_ice"})
		}
	}
}

// biomeMeltsIcebergSlightly 等价原版 Biome.shouldMeltFrozenOceanIcebergSlightly。
// 只有 FROZEN_OCEAN / DEEP_FROZEN_OCEAN 返回 true（其余群系的温度修正不影响冰山）。
func (s *SurfaceSystem) biomeMeltsIcebergSlightly(name string) bool {
	return name == "minecraft:frozen_ocean" || name == "minecraft:deep_frozen_ocean"
}
