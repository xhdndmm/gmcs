package world

import "math"

// 下界生成器：岩层地形 + 岩浆海 + 洞穴 + 灵魂沙/萤石/石英/远古残骸。
//
// 高度模型：全局内存模型为 -64..320，但下界只使用 y 0..255
// （dimension_type：min_y=0、height=256）。基岩地板 y=0、基岩天花板 y=127，
// 岩浆海液面 y=lavaLevel(31)，地形基准高度由噪声在 28..50 之间起伏。
// 网络编码只发送维度范围内的 16 个 section（见 chunk.go）。

// 下界参数。
const (
	netherLavaLevel   = 31
	netherRoofY       = 127
	netherFloorMin    = 26
	netherFloorRange  = 26
	netherSaltFloor   = 0x1A2B3C4D5E6F7081
	netherSaltCarve   = 0x2B3C4D5E6F708192
	netherSaltGlow    = 0x3C4D5E6F708192A3
	netherSaltSoul    = 0x4D5E6F708192A3B4
	netherSaltQuartz  = 0x5E6F708192A3B4C5
	netherSaltDebris  = 0x6F708192A3B4C5D6
	netherSaltBasalt  = 0x708192A3B4C5D6E7
	netherCarvePeriod = 34
)

// NetherGenerator 生成下界地形。
type NetherGenerator struct {
	Seed int64
}

// Dimension 实现 Generator。
func (NetherGenerator) Dimension() Dimension {
	return DimensionNether
}

// netherFloorY 返回 (x, z) 列的地形基准高度（28..50）。
func (g NetherGenerator) netherFloorY(x, z int) int {
	noise := valueNoise(g.Seed^netherSaltFloor, x, z, 56)
	other := valueNoise(g.Seed^netherSaltFloor^0x1111, x, z, 21)
	return netherFloorMin + int(noise*float64(netherFloorRange)) + int((other-0.5)*4)
}

// SurfaceY 实现 Generator：返回列的地形表面（不超过天花板下 8 格）。
func (g NetherGenerator) SurfaceY(x, z int) int {
	return min(g.netherFloorY(x, z), netherRoofY-8)
}

// GenerateChunk 实现 Generator。
func (g NetherGenerator) GenerateChunk(chunkX, chunkZ int) *Chunk {
	chunk := NewChunkDim(chunkX, chunkZ, DimensionNether)
	baseX, baseZ := chunkX*SectionSize, chunkZ*SectionSize

	// 1. 基础岩层：基岩地板 + 净岩 / 岩浆海 + 天花板。
	// 下界全局 section 4（y=0）到 11（y 112–127）。
	for index := 4; index <= 11; index++ {
		chunk.PrepareGenerationSection(index)
	}
	for localX := 0; localX < SectionSize; localX++ {
		for localZ := 0; localZ < SectionSize; localZ++ {
			worldX, worldZ := baseX+localX, baseZ+localZ
			floorY := g.netherFloorY(worldX, worldZ)
			chunk.setBlockStateDirect(localX, 0, localZ, BedrockBlock)
			// 岩浆海：低于 floorY 的列在地面以下填岩浆。
			for y := 1; y <= netherRoofY-1; y++ {
				switch {
				case y <= floorY:
					chunk.setBlockStateDirect(localX, y, localZ, NetherrackBlock)
				case y <= netherLavaLevel:
					chunk.setBlockStateDirect(localX, y, localZ, LavaBlock)
				}
			}
			chunk.setBlockStateDirect(localX, netherRoofY, localZ, BedrockBlock)
		}
	}

	// 2. 洞穴雕刻（含天花板附近的开洞）。
	g.carveNetherCaves(chunk, baseX, baseZ)

	// 3. 矿脉：下界石英与远古残骸。
	g.placeNetherOres(chunk, chunkX, chunkZ)

	// 4. 地表装饰：灵魂沙、岩浆块、玄武岩斑块、萤石。
	g.decorateNether(chunk, baseX, baseZ)

	return chunk
}

// carveNetherCaves 用 3D 噪声雕刻洞穴；低于岩浆液面的空腔填岩浆。
func (g NetherGenerator) carveNetherCaves(chunk *Chunk, baseX, baseZ int) {
	const gridStep = 4
	const gridStepY = 8
	xzCount := SectionSize/gridStep + 3
	yBase, yTop := 4, netherRoofY-4
	yCount := (yTop-yBase)/gridStepY + 2
	samples := make([]float32, xzCount*xzCount*yCount)
	index := 0
	for xi := 0; xi < xzCount; xi++ {
		worldX := baseX + xi*gridStep - gridStep
		for zi := 0; zi < xzCount; zi++ {
			worldZ := baseZ + zi*gridStep - gridStep
			for yi := 0; yi < yCount; yi++ {
				worldY := yBase + yi*gridStepY
				samples[index] = float32(valueNoise3D(g.Seed^netherSaltCarve, worldX, worldY, worldZ, netherCarvePeriod))
				index++
			}
		}
	}
	sample := func(x, y, z int) float64 {
		fx := float64(x+gridStep) / gridStep
		fy := float64(y-yBase) / gridStepY
		fz := float64(z+gridStep) / gridStep
		xi, yi, zi := int(fx), int(fy), int(fz)
		xi = max(0, min(xi, xzCount-2))
		yi = max(0, min(yi, yCount-2))
		zi = max(0, min(zi, xzCount-2))
		tx, ty, tz := clamp01(fx-float64(xi)), clamp01(fy-float64(yi)), clamp01(fz-float64(zi))
		valueAt := func(xi, yi, zi int) float64 {
			return float64(samples[(xi*xzCount+zi)*yCount+yi])
		}
		v000, v100 := valueAt(xi, yi, zi), valueAt(xi+1, yi, zi)
		v010, v110 := valueAt(xi, yi+1, zi), valueAt(xi+1, yi+1, zi)
		v001, v101 := valueAt(xi, yi, zi+1), valueAt(xi+1, yi, zi+1)
		v011, v111 := valueAt(xi, yi+1, zi+1), valueAt(xi+1, yi+1, zi+1)
		bottom := lerp(lerp(v000, v100, tx), lerp(v010, v110, tx), ty)
		top := lerp(lerp(v001, v101, tx), lerp(v011, v111, tx), ty)
		return lerp(bottom, top, tz)
	}

	for localX := 0; localX < SectionSize; localX++ {
		for localZ := 0; localZ < SectionSize; localZ++ {
			for y := 5; y < netherRoofY-2; y++ {
				if sample(localX, y, localZ) > 0.60 {
					if y <= netherLavaLevel {
						chunk.setBlockStateDirect(localX, y, localZ, LavaBlock)
					} else {
						chunk.setBlockStateDirect(localX, y, localZ, AirBlock)
					}
				}
			}
		}
	}
}

// placeNetherOres 生成下界石英与远古残骸矿脉。
func (g NetherGenerator) placeNetherOres(chunk *Chunk, chunkX, chunkZ int) {
	place := func(salt int64, veins, minY, maxY, maxSize int, target func(y int) uint16) {
		count := veins + int(hashInt(g.Seed^salt, chunkX, chunkZ)%3)
		for vein := 0; vein < count; vein++ {
			sequence := newHashSeq(g.Seed^salt, chunkX*97+vein, chunkZ*89-vein)
			x := int(sequence.next() % SectionSize)
			z := int(sequence.next() % SectionSize)
			y := minY + int(sequence.next()%uint64(maxY-minY+1))
			size := 1 + int(sequence.next()%uint64(maxSize+1))
			for i := 0; i < size; i++ {
				if x >= 0 && x < SectionSize && z >= 0 && z < SectionSize && y >= minY && y <= maxY {
					if chunk.getBlockStateLocked(x, y, z) == NetherrackBlock {
						chunk.setBlockStateDirect(x, y, z, target(y))
					}
				}
				switch sequence.nextInt(6) {
				case 0:
					x++
				case 1:
					x--
				case 2:
					y++
				case 3:
					y--
				case 4:
					z++
				default:
					z--
				}
			}
		}
	}
	place(netherSaltQuartz, 3, 1, netherRoofY-2, 8, func(int) uint16 { return NetherQuartzOreBlock })
	place(netherSaltDebris, 1, 8, 22, 3, func(int) uint16 { return AncientDebrisBlock })
}

// decorateNether 放置灵魂沙、岩浆块、玄武岩斑块与萤石。
func (g NetherGenerator) decorateNether(chunk *Chunk, baseX, baseZ int) {
	for localX := 0; localX < SectionSize; localX++ {
		for localZ := 0; localZ < SectionSize; localZ++ {
			worldX, worldZ := baseX+localX, baseZ+localZ
			wasAir := false
			for y := 5; y < netherRoofY; y++ {
				state := chunk.getBlockStateLocked(localX, y, localZ)
				switch {
				case state == AirBlock:
					wasAir = true
					continue
				case state == NetherrackBlock:
					if wasAir {
						// 空腔上方的净岩（天花板）：小概率生成萤石。
						if y > 48 && hashUnit(g.Seed^netherSaltGlow, worldX*3, worldZ*3+y) < 0.02 {
							chunk.setBlockStateDirect(localX, y, localZ, GlowstoneBlock)
						}
					} else if y <= netherLavaLevel+2 {
						// 岩浆海附近：灵魂沙与岩浆块。
						roll := hashUnit(g.Seed^netherSaltSoul, worldX, worldZ*5+y)
						if roll < 0.10 {
							chunk.setBlockStateDirect(localX, y, localZ, SoulSandBlock)
						} else if roll < 0.16 {
							chunk.setBlockStateDirect(localX, y, localZ, MagmaBlock)
						}
					} else {
						// 玄武岩斑块：以低频噪声划分的小区域。
						if valueNoise(g.Seed^netherSaltBasalt, worldX, worldZ, 48) > 0.72 &&
							hashUnit(g.Seed^netherSaltBasalt^0x99, worldX, worldZ) < 0.5 {
							chunk.setBlockStateDirect(localX, y, localZ, BasaltBlock)
						}
					}
					wasAir = false
				default:
					wasAir = false
				}
			}
		}
	}
}

// 确保 math 包被使用（噪声函数的数学分支）。
var _ = math.Abs
