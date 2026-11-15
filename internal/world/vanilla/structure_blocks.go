//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

// placeStructureBlocks 写入专用生成器产生的绝对坐标方块。
func placeStructureBlocks(chunk *GeneratedChunk, piece structurePiece) {
	for _, block := range piece.blocks {
		chunk.SetBlock(block.pos[0], block.pos[1], block.pos[2], block.state)
		if block.entity != nil {
			chunk.SetBlockEntity(block.pos[0], block.pos[1], block.pos[2], *block.entity)
		}
	}
}
