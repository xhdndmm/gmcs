//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package world

import "gmcs/internal/registry"

// 植被与地表装饰：按群系放置树木（橡树/白桦/云杉/金合欢/黑橡/丛林树）、
// 仙人掌、枯灌木、草丛、花与双格花、雪层、甘蔗。
//
// 与地形生成共用同一份确定性随机源（种子 + 坐标哈希），保证相同种子与
// 坐标生成完全相同的区块。装饰只操作刚生成的未发布区块（无锁读写）。
//
// 跨区块树冠：树冠（以及黑橡树的 2×2 树干）可能伸进相邻区块。每个区块除了
// 处理“树干落在本区块”的树木，还会扫描区块外 treeMargin 格内的树干候选
// 位置（这些位置的概率/高度/群系都由纯函数决定，与生成顺序无关），只把落在
// 本区块内的树叶写入。这样相邻区块各自补齐自己那一半树冠，不会出现沿每个
// 区块边界 4 格宽的“无树走廊”，也不会出现被切掉一半的树冠。

// treeMargin 是树冠可能越出根区块的最大水平距离。
const treeMargin = 3

// decorate 按群系放置植被（输入为生成阶段算好的列数据）。
func (g SeededGenerator) decorate(chunk *Chunk, columns *[SectionSize * SectionSize]columnData) {
	baseX, baseZ := chunk.X*SectionSize, chunk.Z*SectionSize
	// 1) 本区块内的树干与地面植被（地面植被只放在本区块的列上）。
	for localX := 0; localX < SectionSize; localX++ {
		for localZ := 0; localZ < SectionSize; localZ++ {
			column := columns[localX*SectionSize+localZ]
			worldX, worldZ := baseX+localX, baseZ+localZ
			land := column.height > SeaLevel+1
			if land {
				g.plantTree(chunk, worldX, worldZ, column.height, column.biome, true)
			}
			// 雪层/地表植物（被树干占据的列跳过）。
			if !land || chunk.getBlockStateLocked(localX, column.height+1, localZ) != AirBlock {
				continue
			}
			g.plantGroundCover(chunk, localX, localZ, column.height, column.biome, worldX, worldZ)
		}
	}
	// 2) 邻区块树干伸入本区块的树叶（树干由根区块写入：placeTrunk=false）。
	for localX := -treeMargin; localX < SectionSize+treeMargin; localX++ {
		for localZ := -treeMargin; localZ < SectionSize+treeMargin; localZ++ {
			if localX >= 0 && localX < SectionSize && localZ >= 0 && localZ < SectionSize {
				continue // 本区块的树干已在第 1 步处理
			}
			worldX, worldZ := baseX+localX, baseZ+localZ
			height := terrainHeight(g.Seed, worldX, worldZ)
			if height <= SeaLevel+1 {
				continue
			}
			g.plantTree(chunk, worldX, worldZ, height, g.biomeAt(worldX, worldZ, height), false)
		}
	}
}

// plantTree 在指定列尝试生长一棵树（密度与树种由群系决定）。
// 坐标为世界坐标；placeTrunk 为 false 表示该树干属于相邻区块，
// 只把落在本区块内的树叶补上。
func (g SeededGenerator) plantTree(chunk *Chunk, worldX, worldZ, height int, biome uint16, placeTrunk bool) {
	roll := func(salt int64) float64 { return hashUnit(g.Seed^salt, worldX, worldZ) }
	switch biome {
	case biomeForest:
		switch r := roll(treeSalt); {
		case r < 0.045:
			g.placeOak(chunk, worldX, worldZ, height, 4, 6, placeTrunk)
		case r < 0.065:
			g.placeBirch(chunk, worldX, worldZ, height, placeTrunk)
		}
	case biomeFlowerForest:
		if roll(treeSalt) < 0.025 {
			g.placeOak(chunk, worldX, worldZ, height, 4, 6, placeTrunk)
		}
	case biomeBirchForest:
		if roll(birchSalt) < 0.06 {
			g.placeBirch(chunk, worldX, worldZ, height, placeTrunk)
		}
	case biomeDarkForest:
		if roll(darkSalt) < 0.075 {
			g.placeDarkOak(chunk, worldX, worldZ, height, placeTrunk)
		}
	case biomeTaiga:
		if roll(spruceSalt) < 0.05 {
			g.placeSpruce(chunk, worldX, worldZ, height, placeTrunk)
		}
	case biomeSnowyTaiga:
		if roll(spruceSalt) < 0.04 {
			g.placeSpruce(chunk, worldX, worldZ, height, placeTrunk)
		}
	case biomeSavanna:
		if roll(acaciaSalt) < 0.012 {
			g.placeAcacia(chunk, worldX, worldZ, height, placeTrunk)
		}
	case biomeJungle:
		switch r := roll(jungleSalt); {
		case r < 0.06:
			g.placeJungle(chunk, worldX, worldZ, height, placeTrunk)
		case r < 0.09:
			g.placeOak(chunk, worldX, worldZ, height, 5, 7, placeTrunk)
		}
	case BiomePlains:
		if roll(treeSalt) < 0.0015 {
			g.placeOak(chunk, worldX, worldZ, height, 4, 6, placeTrunk)
		}
	case biomeSunflowerPlains:
		if roll(treeSalt) < 0.001 {
			g.placeOak(chunk, worldX, worldZ, height, 4, 6, placeTrunk)
		}
	case biomeSwamp:
		if roll(treeSalt) < 0.02 {
			g.placeOak(chunk, worldX, worldZ, height, 4, 5, placeTrunk)
		}
	case biomeMeadow:
		if roll(treeSalt) < 0.002 {
			g.placeOak(chunk, worldX, worldZ, height, 4, 6, placeTrunk)
		}
	case biomeGrove:
		if roll(spruceSalt) < 0.03 {
			g.placeSpruce(chunk, worldX, worldZ, height, placeTrunk)
		}
	case biomeDesert:
		if roll(cactusSalt) < 0.012 {
			g.placeCactus(chunk, worldX, worldZ, height, placeTrunk)
		} else if placeTrunk && roll(decorSalt) < 0.02 {
			chunk.setBlockStateDirect(chunkLocal(worldX), height+1, chunkLocal(worldZ), DeadBushBlock)
		}
	case biomeBadlands:
		if placeTrunk && roll(decorSalt) < 0.03 {
			chunk.setBlockStateDirect(chunkLocal(worldX), height+1, chunkLocal(worldZ), DeadBushBlock)
		} else if roll(cactusSalt) < 0.006 {
			g.placeCactus(chunk, worldX, worldZ, height, placeTrunk)
		}
	}
}

// chunkLocal 把世界坐标换算为区块内的 0–15 下标。
func chunkLocal(world int) int {
	local := world % SectionSize
	if local < 0 {
		local += SectionSize
	}
	return local
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
			set(g.flowerState(worldX, worldZ))
		case r < 0.5:
			set(ShortGrassBlock)
		}
	case biomeMeadow, biomeSunflowerPlains:
		switch r := roll(flowerSalt); {
		case r < 0.06:
			g.placeDoublePlant(chunk, localX, localZ, height, "minecraft:sunflower")
		case r < 0.25:
			set(g.flowerState(worldX, worldZ))
		case r < 0.6:
			set(ShortGrassBlock)
		}
	case BiomePlains, biomeForest, biomeBirchForest, biomeDarkForest, biomeTaiga, biomeJungle, biomeSavanna:
		switch {
		case roll(flowerSalt) < 0.02:
			set(g.flowerState(worldX, worldZ))
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
func (g SeededGenerator) flowerState(worldX, worldZ int) uint16 {
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

// treeSurface 报告树根位置的地表是否允许该树种生长。placeTrunk 为 false
// （邻区块的树干）时无法读取未生成区块的方块，按群系地表直接接受：
// 上述树种只会被分配到草/沙/雪覆盖的群系。
func (g SeededGenerator) treeSurface(chunk *Chunk, worldX, height, worldZ int, placeTrunk bool, allowed ...uint16) bool {
	if !placeTrunk {
		return true
	}
	state := chunk.getBlockStateLocked(chunkLocal(worldX), height, chunkLocal(worldZ))
	for _, option := range allowed {
		if state == option {
			return true
		}
	}
	return false
}

// putTrunk 写入一格树干（世界坐标；place=false 时跳过，用于邻区块树冠）。
func putTrunk(chunk *Chunk, worldX, y, worldZ int, state uint16, place bool) {
	if !place {
		return
	}
	localX := worldX - chunk.X*SectionSize
	localZ := worldZ - chunk.Z*SectionSize
	if localX < 0 || localX >= SectionSize || localZ < 0 || localZ >= SectionSize {
		return
	}
	if y < WorldMinY || y >= WorldMinY+WorldHeight {
		return
	}
	chunk.setBlockStateDirect(localX, y, localZ, state)
}

// placeOak 在草面上放置橡树（trunkMin–trunkMax 高的树干 + 阔树冠）。
func (g SeededGenerator) placeOak(chunk *Chunk, worldX, worldZ, height, trunkMin, trunkMax int, placeTrunk bool) {
	if !g.treeSurface(chunk, worldX, height, worldZ, placeTrunk, GrassBlock, DirtBlock) {
		return
	}
	trunk := trunkMin + int(hashInt(g.Seed^trunkSalt, worldX, worldZ)%uint64(trunkMax-trunkMin+1))
	g.placeSimpleTree(chunk, worldX, worldZ, height, trunk, OakLogBlock, OakLeavesBlock, 2, placeTrunk)
}

// placeBirch 在草面上放置白桦（细树干 + 较小树冠）。
func (g SeededGenerator) placeBirch(chunk *Chunk, worldX, worldZ, height int, placeTrunk bool) {
	if !g.treeSurface(chunk, worldX, height, worldZ, placeTrunk, GrassBlock, DirtBlock) {
		return
	}
	trunk := 5 + int(hashInt(g.Seed^trunkSalt^0x21, worldX, worldZ)%3) // 5-7
	g.placeSimpleTree(chunk, worldX, worldZ, height, trunk, BirchLogBlock, BirchLeavesBlock, 2, placeTrunk)
}

// placeSpruce 在草/雪面上放置云杉（高树干 + 圆锥树冠）。
func (g SeededGenerator) placeSpruce(chunk *Chunk, worldX, worldZ, height int, placeTrunk bool) {
	if !g.treeSurface(chunk, worldX, height, worldZ, placeTrunk, GrassBlock, DirtBlock, SnowBlock) {
		return
	}
	trunk := 6 + int(hashInt(g.Seed^trunkSalt, worldX, worldZ)%4) // 6-9
	top := height + trunk
	for y := height + 1; y <= top; y++ {
		putTrunk(chunk, worldX, y, worldZ, SpruceLogBlock, placeTrunk)
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
				g.putLeaf(chunk, worldX+dx, top+dy, worldZ+dz, SpruceLeavesBlock)
			}
		}
	}
}

// placeDarkOak 在草面上放置黑橡树（2×2 树干 + 宽树冠）。
// 树冠半径 3，因此要求 2×2 树干在根区块内距边缘至少 3 格。
func (g SeededGenerator) placeDarkOak(chunk *Chunk, worldX, worldZ, height int, placeTrunk bool) {
	if localX := chunkLocal(worldX); localX < 3 || localX > SectionSize-4 {
		return
	}
	if localZ := chunkLocal(worldZ); localZ < 3 || localZ > SectionSize-4 {
		return
	}
	for dx := 0; dx <= 1; dx++ {
		for dz := 0; dz <= 1; dz++ {
			if !g.treeSurface(chunk, worldX+dx, height, worldZ+dz, placeTrunk, GrassBlock, DirtBlock) {
				return
			}
		}
	}
	trunk := 6 + int(hashInt(g.Seed^trunkSalt^0x42, worldX, worldZ)%3) // 6-8
	top := height + trunk
	for dy := 1; dy <= trunk; dy++ {
		for dx := 0; dx <= 1; dx++ {
			for dz := 0; dz <= 1; dz++ {
				putTrunk(chunk, worldX+dx, height+dy, worldZ+dz, DarkOakLogBlock, placeTrunk)
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
				g.putLeaf(chunk, worldX+dx, top+dy, worldZ+dz, DarkOakLeavesBlock)
			}
		}
	}
}

// placeJungle 在草面上放置丛林树（高树干 + 两层树冠）。
func (g SeededGenerator) placeJungle(chunk *Chunk, worldX, worldZ, height int, placeTrunk bool) {
	if !g.treeSurface(chunk, worldX, height, worldZ, placeTrunk, GrassBlock, DirtBlock) {
		return
	}
	trunk := 8 + int(hashInt(g.Seed^trunkSalt^0x63, worldX, worldZ)%5) // 8-12
	top := height + trunk
	for y := height + 1; y <= top; y++ {
		putTrunk(chunk, worldX, y, worldZ, JungleLogBlock, placeTrunk)
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
				g.putLeaf(chunk, worldX+dx, top+dy, worldZ+dz, JungleLeavesBlock)
			}
		}
	}
}

// placeAcacia 在草面上放置金合欢（高树干 + 平顶树冠）。
func (g SeededGenerator) placeAcacia(chunk *Chunk, worldX, worldZ, height int, placeTrunk bool) {
	if !g.treeSurface(chunk, worldX, height, worldZ, placeTrunk, GrassBlock, DirtBlock) {
		return
	}
	trunk := 4 + int(hashInt(g.Seed^trunkSalt, worldX, worldZ)%3) // 4-6
	top := height + trunk
	for y := height + 1; y <= top; y++ {
		putTrunk(chunk, worldX, y, worldZ, AcaciaLogBlock, placeTrunk)
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
				g.putLeaf(chunk, worldX+dx, top+dy, worldZ+dz, AcaciaLeavesBlock)
			}
		}
	}
}

// placeSimpleTree 放置一棵“竖直树干 + 球形树冠”的普通树（橡树/白桦共用）。
func (g SeededGenerator) placeSimpleTree(chunk *Chunk, worldX, worldZ, height, trunk int, log, leaves uint16, canopyRadius int, placeTrunk bool) {
	top := height + trunk
	for y := height + 1; y <= top; y++ {
		putTrunk(chunk, worldX, y, worldZ, log, placeTrunk)
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
				g.putLeaf(chunk, worldX+dx, top+dy, worldZ+dz, leaves)
			}
		}
	}
}

// placeCactus 在沙/红沙面上放置 1–3 格高的仙人掌。
func (g SeededGenerator) placeCactus(chunk *Chunk, worldX, worldZ, height int, placeTrunk bool) {
	if !placeTrunk {
		return // 仙人掌没有跨区块的树冠
	}
	if !g.treeSurface(chunk, worldX, height, worldZ, true, SandBlock, RedSandBlock) {
		return
	}
	h := 1 + int(hashInt(g.Seed^trunkSalt, worldX, worldZ)%3) // 1-3
	for y := height + 1; y <= height+h; y++ {
		putTrunk(chunk, worldX, y, worldZ, CactusBlock, true)
	}
}

// putLeaf 在空气位置放置树叶（越界或已有方块时跳过）。
func (g SeededGenerator) putLeaf(chunk *Chunk, worldX, y, worldZ int, state uint16) {
	localX := worldX - chunk.X*SectionSize
	localZ := worldZ - chunk.Z*SectionSize
	if localX < 0 || localX >= SectionSize || localZ < 0 || localZ >= SectionSize {
		return // 不在本区块内（由相邻区块负责这一部分）
	}
	if y < WorldMinY+1 || y >= WorldMinY+WorldHeight {
		return
	}
	if chunk.getBlockStateLocked(localX, y, localZ) != AirBlock {
		return
	}
	chunk.setBlockStateDirect(localX, y, localZ, state)
}
