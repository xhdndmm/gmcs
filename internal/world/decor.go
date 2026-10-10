package world

import "gmcs/internal/registry"

// 植被与地表装饰：按群系放置树木（橡树/白桦/云杉/金合欢/黑橡/丛林树）、
// 仙人掌、枯灌木、草丛、花与双格花、雪层、甘蔗。
//
// 与地形生成共用同一份确定性随机源（种子 + 坐标哈希），保证相同种子与
// 坐标生成完全相同的区块。装饰只操作刚生成的未发布区块（无锁读写），
// 且只在距区块边缘留白的位置生长树冠不会跨区块的树木。

// decorate 按群系放置植被（输入为生成阶段算好的列数据）。
func (g SeededGenerator) decorate(chunk *Chunk, columns *[SectionSize * SectionSize]columnData) {
	baseX, baseZ := chunk.X*SectionSize, chunk.Z*SectionSize
	for localX := 0; localX < SectionSize; localX++ {
		for localZ := 0; localZ < SectionSize; localZ++ {
			column := columns[localX*SectionSize+localZ]
			height, biome := column.height, column.biome
			worldX, worldZ := baseX+localX, baseZ+localZ
			land := height > SeaLevel+1

			// 树木/仙人掌：距边缘至少 2 格（黑橡树冠更大，另行判断）。
			inner := localX >= 2 && localX <= SectionSize-3 && localZ >= 2 && localZ <= SectionSize-3
			if land && inner {
				g.plantTree(chunk, localX, localZ, height, biome, worldX, worldZ)
			}

			// 雪层/地表植物（被树干占据的列跳过）。
			if !land || chunk.getBlockStateLocked(localX, height+1, localZ) != AirBlock {
				continue
			}
			g.plantGroundCover(chunk, localX, localZ, height, biome, worldX, worldZ)
		}
	}
}

// plantTree 在指定列尝试生长一棵树（密度与树种由群系决定）。
func (g SeededGenerator) plantTree(chunk *Chunk, localX, localZ, height int, biome uint16, worldX, worldZ int) {
	roll := func(salt int64) float64 { return hashUnit(g.Seed^salt, worldX, worldZ) }
	switch biome {
	case biomeForest:
		switch r := roll(treeSalt); {
		case r < 0.045:
			g.placeOak(chunk, localX, localZ, height, worldX, worldZ, 4, 6)
		case r < 0.065:
			g.placeBirch(chunk, localX, localZ, height, worldX, worldZ)
		}
	case biomeFlowerForest:
		if roll(treeSalt) < 0.025 {
			g.placeOak(chunk, localX, localZ, height, worldX, worldZ, 4, 6)
		}
	case biomeBirchForest:
		if roll(birchSalt) < 0.06 {
			g.placeBirch(chunk, localX, localZ, height, worldX, worldZ)
		}
	case biomeDarkForest:
		if roll(darkSalt) < 0.075 {
			g.placeDarkOak(chunk, localX, localZ, height, worldX, worldZ)
		}
	case biomeTaiga:
		if roll(spruceSalt) < 0.05 {
			g.placeSpruce(chunk, localX, localZ, height, worldX, worldZ)
		}
	case biomeSnowyTaiga:
		if roll(spruceSalt) < 0.04 {
			g.placeSpruce(chunk, localX, localZ, height, worldX, worldZ)
		}
	case biomeSavanna:
		if roll(acaciaSalt) < 0.012 {
			g.placeAcacia(chunk, localX, localZ, height, worldX, worldZ)
		}
	case biomeJungle:
		switch r := roll(jungleSalt); {
		case r < 0.06:
			g.placeJungle(chunk, localX, localZ, height, worldX, worldZ)
		case r < 0.09:
			g.placeOak(chunk, localX, localZ, height, worldX, worldZ, 5, 7)
		}
	case BiomePlains:
		if roll(treeSalt) < 0.0015 {
			g.placeOak(chunk, localX, localZ, height, worldX, worldZ, 4, 6)
		}
	case biomeSunflowerPlains:
		if roll(treeSalt) < 0.001 {
			g.placeOak(chunk, localX, localZ, height, worldX, worldZ, 4, 6)
		}
	case biomeSwamp:
		if roll(treeSalt) < 0.02 {
			g.placeOak(chunk, localX, localZ, height, worldX, worldZ, 4, 5)
		}
	case biomeMeadow:
		if roll(treeSalt) < 0.002 {
			g.placeOak(chunk, localX, localZ, height, worldX, worldZ, 4, 6)
		}
	case biomeGrove:
		if roll(spruceSalt) < 0.03 {
			g.placeSpruce(chunk, localX, localZ, height, worldX, worldZ)
		}
	case biomeDesert:
		if roll(cactusSalt) < 0.012 {
			g.placeCactus(chunk, localX, localZ, height, worldX, worldZ)
		} else if roll(decorSalt) < 0.02 {
			chunk.setBlockStateDirect(localX, height+1, localZ, DeadBushBlock)
		}
	case biomeBadlands:
		if roll(decorSalt) < 0.03 {
			chunk.setBlockStateDirect(localX, height+1, localZ, DeadBushBlock)
		} else if roll(cactusSalt) < 0.006 {
			g.placeCactus(chunk, localX, localZ, height, worldX, worldZ)
		}
	}
}

// plantGroundCover 放置雪层、草丛、花与双格花、甘蔗（仅当顶部为空）。
func (g SeededGenerator) plantGroundCover(chunk *Chunk, localX, localZ, height int, biome uint16, worldX, worldZ int) {
	roll := func(salt int64) float64 { return hashUnit(g.Seed^salt, worldX, worldZ) }
	surface := chunk.getBlockStateLocked(localX, height, localZ)
	set := func(state uint16) { chunk.setBlockStateDirect(localX, height+1, localZ, state) }

	// 雪层：雪原/雪山群系在草或雪块上覆盖薄雪。
	switch biome {
	case biomeSnowyPlains, biomeSnowyTaiga:
		if surface == GrassBlock || surface == SnowBlock {
			set(SnowLayerBlock)
			return
		}
	case biomeSnowySlopes, biomeFrozenPeaks, biomeGrove:
		if surface == SnowBlock || surface == StoneBlock || surface == GrassBlock {
			set(SnowLayerBlock)
			return
		}
	case biomeSnowyBeach:
		if surface == SandBlock {
			set(SnowLayerBlock)
			return
		}
	case biomeStonyPeaks:
		if roll(decorSalt) < 0.03 {
			set(GravelBlock)
		}
		return
	case biomeStonyShore, biomeDeepOcean, biomeOcean, biomeBeach, biomeRiver:
		return
	}

	if surface != GrassBlock {
		return
	}
	switch biome {
	case biomeFlowerForest:
		switch r := roll(flowerSalt); {
		case r < 0.22:
			set(g.flowerState(localX, localZ, worldX, worldZ))
		case r < 0.5:
			set(ShortGrassBlock)
		}
	case biomeMeadow, biomeSunflowerPlains:
		switch r := roll(flowerSalt); {
		case r < 0.06:
			g.placeDoublePlant(chunk, localX, localZ, height, "minecraft:sunflower")
		case r < 0.25:
			set(g.flowerState(localX, localZ, worldX, worldZ))
		case r < 0.6:
			set(ShortGrassBlock)
		}
	case BiomePlains, biomeForest, biomeBirchForest, biomeDarkForest, biomeTaiga, biomeJungle, biomeSavanna:
		switch {
		case roll(flowerSalt) < 0.02:
			set(g.flowerState(localX, localZ, worldX, worldZ))
		case roll(decorSalt) < 0.12:
			set(ShortGrassBlock)
		}
	case biomeSwamp:
		switch {
		case roll(flowerSalt) < 0.01:
			g.placeDoublePlant(chunk, localX, localZ, height, "minecraft:lilac")
		case roll(decorSalt) < 0.15:
			set(ShortGrassBlock)
		}
	}

	// 甘蔗：邻水草/沙上的 1–3 格高（仅当放置位置与邻列同高可见）。
	if biome == biomeSwamp || biome == biomeJungle || biome == biomeRiver {
		g.trySugarCane(chunk, localX, localZ, height, worldX, worldZ)
	}
}

// flowerState 从三种单格花中确定性地选一种。
func (g SeededGenerator) flowerState(localX, localZ, worldX, worldZ int) uint16 {
	switch hashInt(g.Seed^flowerSalt, worldX, worldZ) % 3 {
	case 0:
		return DandelionBlock
	case 1:
		return PoppyBlock
	default:
		return CornflowerBlock
	}
}

// placeDoublePlant 放置双格植物（lower + upper 两个状态）。
func (g SeededGenerator) placeDoublePlant(chunk *Chunk, localX, localZ, height int, name string) {
	lower, ok := registry.BlockStateWithProps(name, map[string]string{"half": "lower"})
	if !ok {
		return
	}
	upper, ok := registry.BlockStateWithProps(name, map[string]string{"half": "upper"})
	if !ok {
		return
	}
	if chunk.getBlockStateLocked(localX, height+2, localZ) != AirBlock {
		return
	}
	chunk.setBlockStateDirect(localX, height+1, localZ, lower)
	chunk.setBlockStateDirect(localX, height+2, localZ, upper)
}

// trySugarCane 在邻水的草/沙上放置 1–3 格甘蔗。
func (g SeededGenerator) trySugarCane(chunk *Chunk, localX, localZ, height, worldX, worldZ int) {
	if hashUnit(g.Seed^decorSalt, worldX, worldZ) > 0.04 {
		return
	}
	surface := chunk.getBlockStateLocked(localX, height, localZ)
	if surface != GrassBlock && surface != SandBlock {
		return
	}
	// 只需水平邻列（区块内）存在与底面同高的水。
	hasWater := false
	for _, offset := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
		nx, nz := localX+offset[0], localZ+offset[1]
		if nx < 0 || nx >= SectionSize || nz < 0 || nz >= SectionSize {
			continue
		}
		if chunk.getBlockStateLocked(nx, height+1, nz) == WaterBlock {
			hasWater = true
			break
		}
	}
	if !hasWater {
		return
	}
	count := 1 + int(hashInt(g.Seed^trunkSalt, worldX, worldZ)%3)
	for i := 0; i < count; i++ {
		chunk.setBlockStateDirect(localX, height+1+i, localZ, SugarCaneBlock)
	}
}

// surfaceIs 报告 (localX, localZ) 的地表是否为给定方块之一。
func surfaceIs(chunk *Chunk, localX, height, localZ int, states ...uint16) bool {
	state := chunk.getBlockStateLocked(localX, height, localZ)
	for _, allowed := range states {
		if state == allowed {
			return true
		}
	}
	return false
}

// placeOak 在草面上放置橡树（trunkMin–trunkMax 高的树干 + 阔树冠）。
func (g SeededGenerator) placeOak(chunk *Chunk, localX, localZ, height, worldX, worldZ, trunkMin, trunkMax int) {
	if !surfaceIs(chunk, localX, height, localZ, GrassBlock, DirtBlock) {
		return
	}
	trunk := trunkMin + int(hashInt(g.Seed^trunkSalt, worldX, worldZ)%uint64(trunkMax-trunkMin+1))
	g.placeSimpleTree(chunk, localX, localZ, height, int(trunk), OakLogBlock, OakLeavesBlock, 2)
}

// placeBirch 在草面上放置白桦（细树干 + 较小树冠）。
func (g SeededGenerator) placeBirch(chunk *Chunk, localX, localZ, height, worldX, worldZ int) {
	if !surfaceIs(chunk, localX, height, localZ, GrassBlock, DirtBlock) {
		return
	}
	trunk := 5 + hashInt(g.Seed^trunkSalt^0x21, worldX, worldZ)%3 // 5-7
	g.placeSimpleTree(chunk, localX, localZ, height, int(trunk), BirchLogBlock, BirchLeavesBlock, 2)
}

// placeSpruce 在草/雪面上放置云杉（高树干 + 圆锥树冠）。
func (g SeededGenerator) placeSpruce(chunk *Chunk, localX, localZ, height, worldX, worldZ int) {
	if !surfaceIs(chunk, localX, height, localZ, GrassBlock, DirtBlock, SnowBlock) {
		return
	}
	trunk := 6 + hashInt(g.Seed^trunkSalt, worldX, worldZ)%4 // 6-9
	top := height + int(trunk)
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

// placeDarkOak 在草面上放置黑橡树（2×2 树干 + 宽树冠）。
// 树冠半径 3，因此要求距区块边缘至少 3 格且 2×2 树干均在同一高度。
func (g SeededGenerator) placeDarkOak(chunk *Chunk, localX, localZ, height, worldX, worldZ int) {
	if localX < 3 || localX > SectionSize-4 || localZ < 3 || localZ > SectionSize-4 {
		return
	}
	for dx := 0; dx <= 1; dx++ {
		for dz := 0; dz <= 1; dz++ {
			if !surfaceIs(chunk, localX+dx, height, localZ+dz, GrassBlock, DirtBlock) {
				return
			}
		}
	}
	trunk := 6 + hashInt(g.Seed^trunkSalt^0x42, worldX, worldZ)%3 // 6-8
	top := height + int(trunk)
	for dy := 1; dy <= int(trunk); dy++ {
		for dx := 0; dx <= 1; dx++ {
			for dz := 0; dz <= 1; dz++ {
				chunk.setBlockStateDirect(localX+dx, height+dy, localZ+dz, DarkOakLogBlock)
			}
		}
	}
	for dy := -2; dy <= 1; dy++ {
		radius := 3
		if dy == 1 {
			radius = 2
		}
		for dx := -radius; dx <= radius+1; dx++ {
			for dz := -radius; dz <= radius+1; dz++ {
				if dx >= 0 && dx <= 1 && dz >= 0 && dz <= 1 && dy <= 0 {
					continue // 树干位置
				}
				if abs(dx-1) == radius && abs(dz-1) == radius {
					continue // 裁角
				}
				g.putLeaf(chunk, localX+dx, top+dy, localZ+dz, DarkOakLeavesBlock)
			}
		}
	}
}

// placeJungle 在草面上放置丛林树（高树干 + 两层树冠）。
func (g SeededGenerator) placeJungle(chunk *Chunk, localX, localZ, height, worldX, worldZ int) {
	if !surfaceIs(chunk, localX, height, localZ, GrassBlock, DirtBlock) {
		return
	}
	trunk := 8 + hashInt(g.Seed^trunkSalt^0x63, worldX, worldZ)%5 // 8-12
	top := height + int(trunk)
	for y := height + 1; y <= top; y++ {
		chunk.setBlockStateDirect(localX, y, localZ, JungleLogBlock)
	}
	for dy := -1; dy <= 1; dy++ {
		radius := 2
		if dy < 0 {
			radius = 1
		}
		for dx := -radius; dx <= radius; dx++ {
			for dz := -radius; dz <= radius; dz++ {
				if dx == 0 && dz == 0 && dy <= 0 {
					continue
				}
				g.putLeaf(chunk, localX+dx, top+dy, localZ+dz, JungleLeavesBlock)
			}
		}
	}
}

// placeAcacia 在草面上放置金合欢（高树干 + 平顶树冠）。
func (g SeededGenerator) placeAcacia(chunk *Chunk, localX, localZ, height, worldX, worldZ int) {
	if !surfaceIs(chunk, localX, height, localZ, GrassBlock, DirtBlock) {
		return
	}
	trunk := 4 + hashInt(g.Seed^trunkSalt, worldX, worldZ)%3 // 4-6
	top := height + int(trunk)
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

// placeSimpleTree 放置一棵“竖直树干 + 球形树冠”的普通树（橡树/白桦共用）。
func (g SeededGenerator) placeSimpleTree(chunk *Chunk, localX, localZ, height, trunk int, log, leaves uint16, canopyRadius int) {
	top := height + trunk
	for y := height + 1; y <= top; y++ {
		chunk.setBlockStateDirect(localX, y, localZ, log)
	}
	for dy := -2 - (canopyRadius - 2); dy <= 1; dy++ {
		radius := canopyRadius
		if dy >= 0 {
			radius = canopyRadius - 1
		}
		for dx := -radius; dx <= radius; dx++ {
			for dz := -radius; dz <= radius; dz++ {
				if dx == 0 && dz == 0 && dy <= 0 {
					continue // 树干位置
				}
				if abs(dx) == radius && abs(dz) == radius && (dy <= -2 || dy >= 1) {
					continue // 裁角，使树冠更接近原版
				}
				g.putLeaf(chunk, localX+dx, top+dy, localZ+dz, leaves)
			}
		}
	}
}

// placeCactus 在沙/红沙面上放置 1–3 格高的仙人掌。
func (g SeededGenerator) placeCactus(chunk *Chunk, localX, localZ, height, worldX, worldZ int) {
	if !surfaceIs(chunk, localX, height, localZ, SandBlock, RedSandBlock) {
		return
	}
	h := 1 + hashInt(g.Seed^trunkSalt, worldX, worldZ)%3 // 1-3
	for y := height + 1; y <= height+int(h); y++ {
		chunk.setBlockStateDirect(localX, y, localZ, CactusBlock)
	}
}

// putLeaf 在空气位置放置树叶（越界或已有方块时跳过）。
func (g SeededGenerator) putLeaf(chunk *Chunk, x, y, z int, state uint16) {
	if x < 0 || x >= SectionSize || z < 0 || z >= SectionSize {
		return
	}
	if y < WorldMinY+1 || y >= WorldMinY+WorldHeight {
		return
	}
	if chunk.getBlockStateLocked(x, y, z) != AirBlock {
		return
	}
	chunk.setBlockStateDirect(x, y, z, state)
}
