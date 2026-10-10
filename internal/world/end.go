package world

import (
	"math"

	"gmcs/internal/registry"
)

// 末地生成器：主岛（原点周围）+ 外部岛屿（192 格网格）+ 黑曜石柱。
//
// 高度模型：dimension_type：min_y=0、height=256；主岛表面约 y 62–70，
// 岛屿下方逐渐收窄，四周是虚空（y 0..45 附近无方块）。末地有天空光，
// 网络编码按 16 个 section 发送。

// 末地参数。
const (
	endMainRadius   = 46
	endMainTop      = 64
	endMainBottom   = 52
	endOuterCell    = 192
	endOuterMinDist = 320
	endSaltMain     = 0x1122334455667788
	endSaltEdge     = 0x2233445566778899
	endSaltOuter    = 0x33445566778899AA
	endSaltPillar   = 0x445566778899AABB
	endSaltHeight   = 0x5566778899AABBCC
	// endPillarCount 是主岛黑曜石柱数量（原版为 10 根）。
	endPillarCount = 10
)

// EndGenerator 生成末地地形。
type EndGenerator struct {
	Seed int64
}

// Dimension 实现 Generator。
func (EndGenerator) Dimension() Dimension {
	return DimensionEnd
}

// SurfaceY 实现 Generator：主岛范围返回岛面高度，其余返回外部岛屿高度或 0。
func (g EndGenerator) SurfaceY(x, z int) int {
	distance := math.Hypot(float64(x), float64(z))
	if distance < endMainRadius {
		return g.mainIslandTop(x, z)
	}
	if top, ok := g.outerIslandTop(x, z); ok {
		return top
	}
	return 0
}

// mainIslandTop 返回主岛在 (x, z) 处的顶面高度。
func (g EndGenerator) mainIslandTop(x, z int) int {
	distance := math.Hypot(float64(x), float64(z))
	falloff := 1 - smoothstep(clamp01((distance-endMainRadius*0.55)/(endMainRadius*0.45)))
	noise := valueNoise(g.Seed^endSaltHeight, x, z, 24)
	return endMainTop - 6 + int(falloff*6) + int((noise-0.5)*4)
}

// outerIslandTop 返回外部岛屿在 (x, z) 处的顶面高度；不属于任何岛屿时返回 false。
func (g EndGenerator) outerIslandTop(x, z int) (int, bool) {
	cellX := floorDiv(x, endOuterCell)
	cellZ := floorDiv(z, endOuterCell)
	h := hashInt(g.Seed^endSaltOuter, cellX, cellZ)
	if h%100 >= 62 {
		return 0, false
	}
	// 岛屿中心与半径由网格单元哈希决定。
	centerX := cellX*endOuterCell + int(h%97) + 48
	centerZ := cellZ*endOuterCell + int((h>>8)%97) + 48
	if math.Hypot(float64(x), float64(z)) < endOuterMinDist {
		return 0, false
	}
	radius := 12 + float64((h>>16)%20)
	distance := math.Hypot(float64(x-centerX), float64(z-centerZ))
	if distance > radius {
		return 0, false
	}
	falloff := 1 - smoothstep(clamp01(distance/radius))
	noise := valueNoise(g.Seed^endSaltOuter^0x77, x, z, 24)
	top := 50 + int(falloff*16) + int((noise-0.5)*6)
	return top, true
}

// GenerateChunk 实现 Generator。
func (g EndGenerator) GenerateChunk(chunkX, chunkZ int) *Chunk {
	chunk := NewChunkDim(chunkX, chunkZ, DimensionEnd)
	baseX, baseZ := chunkX*SectionSize, chunkZ*SectionSize

	// 预分配岛屿所在 section（y 30..100 → 全局 section 5..10）。
	for index := 5; index <= 10; index++ {
		chunk.PrepareGenerationSection(index)
	}
	for localX := 0; localX < SectionSize; localX++ {
		for localZ := 0; localZ < SectionSize; localZ++ {
			worldX, worldZ := baseX+localX, baseZ+localZ
			distance := math.Hypot(float64(worldX), float64(worldZ))
			if distance < endMainRadius {
				// 主岛：顶面 + 底面（随机厚度，边缘收窄）。
				top := g.mainIslandTop(worldX, worldZ)
				falloff := 1 - smoothstep(clamp01((distance-endMainRadius*0.55)/(endMainRadius*0.45)))
				thickness := 3 + int(falloff*6) + int(hashUnit(g.Seed^endSaltEdge, worldX, worldZ)*3)
				for y := max(endMainBottom-14, top-thickness); y <= top; y++ {
					chunk.setBlockStateDirect(localX, y, localZ, EndStoneBlock)
				}
				continue
			}
			if top, ok := g.outerIslandTop(worldX, worldZ); ok {
				thickness := 4 + int(hashUnit(g.Seed^endSaltEdge^0x55, worldX, worldZ)*4)
				for y := top - thickness; y <= top; y++ {
					chunk.setBlockStateDirect(localX, y, localZ, EndStoneBlock)
				}
			}
		}
	}

	// 黑曜石柱：主岛上的固定位置（10 根，按确定性高度）。
	if chunkX >= -3 && chunkX <= 3 && chunkZ >= -3 && chunkZ <= 3 {
		g.placePillars(chunk, baseX, baseZ)
	}
	// 出口传送门：主岛中心（0,0）所在区块生成（返回主世界的通道）。
	if chunkX == 0 && chunkZ == 0 {
		g.placeExitPortal(chunk, baseX, baseZ)
	}
	return chunk
}

// placePillars 在主岛放置黑曜石柱（仅当柱子落在本区块内时写入）。
func (g EndGenerator) placePillars(chunk *Chunk, baseX, baseZ int) {
	for i := 0; i < endPillarCount; i++ {
		angle := float64(i) * (2 * math.Pi / endPillarCount)
		radius := 18 + float64(hashInt(g.Seed^endSaltPillar, i, 0)%12)
		pilarX := int(math.Round(math.Cos(angle) * radius))
		pilarZ := int(math.Round(math.Sin(angle) * radius))
		height := 8 + int(hashInt(g.Seed^endSaltPillar, i, 1)%12)
		localX := pilarX - baseX
		localZ := pilarZ - baseZ
		if localX < 0 || localX >= SectionSize || localZ < 0 || localZ >= SectionSize {
			continue
		}
		top := g.mainIslandTop(pilarX, pilarZ)
		for y := top + 1; y <= top+height; y++ {
			chunk.setBlockStateDirect(localX, y, localZ, ObsidianBlock)
		}
		// 柱顶放置火把（原版为铁栏杆 + 末地水晶座；此处简化为柱顶照明）。
		if top+height+1 < 250 {
			chunk.setBlockStateDirect(localX, top+height+1, localZ, TorchBlock)
		}
	}
}

// endExitPortalOffsets 是出口传送门框相对中心的 12 个位置（框架环半径 2，
// 不含四角；与服务器端末地传送门激活判定一致）。
var endExitPortalOffsets = [12][2]int{
	{-2, -1}, {-2, 0}, {-2, 1},
	{2, -1}, {2, 0}, {2, 1},
	{-1, -2}, {0, -2}, {1, -2},
	{-1, 2}, {0, 2}, {1, 2},
}

// placeExitPortal 在主岛中心生成出口传送门（12 个填有眼睛的末地传送门框 +
// 中心 3×3 末地传送门），作为末地返回主世界的通道。
//
// 与原版差异：原版出口传送门在击败末影龙后生成（4 格基岩 + 传送门，处于
// (0,75,0)）；gmcs 未实现末影龙，因此在主岛生成时直接构建简化结构。
func (g EndGenerator) placeExitPortal(chunk *Chunk, baseX, baseZ int) {
	frame, frameOK := registry.BlockStateWithProps("minecraft:end_portal_frame",
		map[string]string{"eye": "true", "facing": "north"})
	endPortal, portalOK := registry.BlockStateIDs["minecraft:end_portal"]
	if !frameOK || !portalOK {
		return
	}
	// 取中心附近最高列，避免结构埋在岛面下。
	maxTop := g.mainIslandTop(0, 0)
	for dx := -3; dx <= 3; dx++ {
		for dz := -3; dz <= 3; dz++ {
			if top := g.mainIslandTop(dx, dz); top > maxTop {
				maxTop = top
			}
		}
	}
	portalY := maxTop + 1
	set := func(worldX, y, worldZ int, state uint16) {
		localX, localZ := worldX-baseX, worldZ-baseZ
		if localX < 0 || localX >= SectionSize || localZ < 0 || localZ >= SectionSize {
			return
		}
		chunk.setBlockStateDirect(localX, y, localZ, state)
	}
	for _, offset := range endExitPortalOffsets {
		x, z := offset[0], offset[1]
		// 框架下方垫一层末地石（悬空位置更自然，避开水面上方）。
		set(x, portalY-1, z, EndStoneBlock)
		set(x, portalY, z, frame)
	}
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			set(dx, portalY, dz, endPortal)
		}
	}
}
