//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

// adaptStructureTerrain 是 Beardifier 的生成后近似：在结构模板放置前，
// 按模板每列的最低方块，把低于该高度的空气/水补成石头支撑。原版 Beardifier
// 在密度阶段计算更平滑的 beard 偏移；这里保留确定性和不破坏模板语义。
func adaptStructureTerrain(chunk *GeneratedChunk, piece structurePiece) {
	if piece.template == nil {
		return
	}
	adaptation := piece.terrainAdaptation
	if adaptation != "minecraft:beard_thin" && adaptation != "minecraft:beard_box" && adaptation != "minecraft:bury" {
		return
	}
	floors := make(map[[2]int]int)
	tops := make(map[[2]int]int)
	for _, block := range piece.template.Blocks {
		if block.State < 0 || block.State >= len(piece.template.Palette) {
			continue
		}
		if piece.template.Palette[block.State].IsAir() {
			continue
		}
		pos := transformTemplatePos(block.Pos, piece.template.Size, piece.rotation, piece.mirror)
		x, y, z := piece.origin[0]+pos[0], piece.origin[1]+pos[1], piece.origin[2]+pos[2]
		key := [2]int{x, z}
		if floor, ok := floors[key]; !ok || y < floor {
			floors[key] = y
		}
		if top, ok := tops[key]; !ok || y > top {
			tops[key] = y
		}
	}
	stone := SurfaceBlock{Name: "minecraft:stone"}
	for key, floor := range floors {
		ground := chunk.HeightAt(key[0], key[1])
		switch adaptation {
		case "minecraft:beard_thin", "minecraft:beard_box":
			if ground >= floor-1 {
				continue
			}
			for y := ground + 1; y < floor; y++ {
				if !chunk.InRange(key[0], y, key[1]) {
					continue
				}
				current := chunk.BlockAt(key[0], y, key[1])
				if current.IsAir() || current.Name == "minecraft:water" {
					chunk.SetBlock(key[0], y, key[1], stone)
				}
			}
		case "minecraft:bury":
			top := tops[key]
			if ground <= top {
				continue
			}
			for y := top + 1; y <= ground; y++ {
				if chunk.InRange(key[0], y, key[1]) && chunk.BlockAt(key[0], y, key[1]).IsAir() {
					chunk.SetBlock(key[0], y, key[1], stone)
				}
			}
		}
	}
}
