package world

// 矿物生成：按深度带与数量确定性生成矿脉（普通与深板岩变体）。
// 矿脉只在石头/深板岩中替换方块（不填充洞穴），且限制在本区块内
// （跨区块的矿脉会在边界截断，观感影响极小）。

// oreSpec 描述一种矿物。
type oreSpec struct {
	// stone/deepslate 是普通与深板岩变体的方块状态。
	stone     uint16
	deepslate uint16
	// minY/maxY 是生成深度范围（含端点）。
	minY, maxY int
	// veins 是每个区块的矿脉数基数（再叠加 0–2 的确定性浮动）。
	veins int
	// veinSize 是矿脉的最大方块数。
	veinSize int
	// mountainOnly 为 true 时只在山地群系生成（绿宝石）。
	mountainOnly bool
}

// oreSalt 是矿物生成的随机盐。
const oreSalt = 0x0DE5BEEF00DCAFE1

var overworldOres = []oreSpec{
	{stone: CoalOreBlock, deepslate: DeepslateCoalOreBlock, minY: 0, maxY: 128, veins: 12, veinSize: 12},
	{stone: CopperOreBlock, deepslate: DeepslateCopperOre, minY: -16, maxY: 112, veins: 5, veinSize: 10},
	{stone: IronOreBlock, deepslate: DeepslateIronOreBlock, minY: -64, maxY: 64, veins: 8, veinSize: 8},
	{stone: GoldOreBlock, deepslate: DeepslateGoldOreBlock, minY: -64, maxY: 32, veins: 2, veinSize: 8},
	{stone: RedstoneOreBlock, deepslate: DeepslateRedstoneOre, minY: -64, maxY: 16, veins: 4, veinSize: 8},
	{stone: DiamondOreBlock, deepslate: DeepslateDiamondOre, minY: -64, maxY: 16, veins: 1, veinSize: 7},
	{stone: LapisOreBlock, deepslate: DeepslateLapisOre, minY: -64, maxY: 30, veins: 1, veinSize: 6},
	{stone: EmeraldOreBlock, deepslate: DeepslateEmeraldOre, minY: 0, maxY: 110, veins: 1, veinSize: 2, mountainOnly: true},
}

// placeOres 在区块内生成全部矿脉。
func placeOres(chunk *Chunk, seed int64, columns *[SectionSize * SectionSize]columnData) {
	for index := range overworldOres {
		spec := overworldOres[index]
		extra := int(hashInt(seed^oreSalt, chunk.X*13+index*7, chunk.Z*7+index*13) % 3)
		count := spec.veins + extra
		for vein := 0; vein < count; vein++ {
			sequence := newHashSeq(
				seed^oreSalt^int64(uint64(index)<<17),
				chunk.X*613+vein*29+index,
				chunk.Z*719+vein*37-index,
			)
			x := int(sequence.next() % SectionSize)
			z := int(sequence.next() % SectionSize)
			column := columns[x*SectionSize+z]
			if spec.mountainOnly && !isMountainBiome(column.biome) {
				continue
			}
			// 保留地表 5 格，避免矿脉直接暴露在地表。
			high := min(spec.maxY, column.height-5)
			if high <= spec.minY {
				continue
			}
			y := spec.minY + int(sequence.next()%uint64(high-spec.minY+1))
			size := 1 + int(sequence.next()%uint64(spec.veinSize+1))
			placeOreVein(chunk, sequence, x, y, z, size, spec)
		}
	}
}

// placeOreVein 用随机游走放置一条矿脉。
func placeOreVein(chunk *Chunk, sequence *hashSeq, x, y, z, size int, spec oreSpec) {
	for i := 0; i < size; i++ {
		if x >= 0 && x < SectionSize && z >= 0 && z < SectionSize {
			if y >= spec.minY && y <= spec.maxY {
				state := chunk.getBlockStateLocked(x, y, z)
				if state == StoneBlock || state == DeepslateBlock {
					target := spec.stone
					if y < deepslateTop {
						target = spec.deepslate
					}
					chunk.setBlockStateDirect(x, y, z, target)
				}
			}
		}
		// 随机走一步（保持在矿物深度范围内）。
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
		if y < spec.minY {
			y = spec.minY
		}
		if y > spec.maxY {
			y = spec.maxY
		}
	}
}

// isMountainBiome 报告群系是否属于山地（绿宝石生成范围）。
func isMountainBiome(biome uint16) bool {
	switch biome {
	case biomeStonyPeaks, biomeMeadow, biomeSnowySlopes, biomeFrozenPeaks, biomeGrove:
		return true
	}
	return false
}
