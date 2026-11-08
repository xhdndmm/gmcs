//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

// templateRotation 是结构模板的 0/90/180/270 度旋转。
type templateRotation int

const (
	rotateNone templateRotation = iota
	rotateClockwise90
	rotate180
	rotateCounterClockwise90
)

// transformTemplatePos 把模板局部坐标变换到放置后的局部坐标。
func transformTemplatePos(pos [3]int, size [3]int, rotation templateRotation, mirror bool) [3]int {
	x, y, z := pos[0], pos[1], pos[2]
	if mirror {
		x = size[0] - 1 - x
	}
	switch rotation {
	case rotateClockwise90:
		return [3]int{size[2] - 1 - z, y, x}
	case rotate180:
		return [3]int{size[0] - 1 - x, y, size[2] - 1 - z}
	case rotateCounterClockwise90:
		return [3]int{z, y, size[0] - 1 - x}
	default:
		return [3]int{x, y, z}
	}
}

// transformedSize 返回旋转后的模板尺寸。
func transformedSize(size [3]int, rotation templateRotation) [3]int {
	if rotation == rotateClockwise90 || rotation == rotateCounterClockwise90 {
		return [3]int{size[2], size[1], size[0]}
	}
	return size
}

// placeTemplate 把一个结构模板按旋转/镜像写入区块；越界部分裁掉。
func placeTemplate(chunk *GeneratedChunk, template *structureTemplate, originX, originY, originZ int, rotation templateRotation, mirror bool) {
	for _, block := range template.Blocks {
		if block.State < 0 || block.State >= len(template.Palette) {
			continue
		}
		state := template.Palette[block.State]
		if state.Name == "minecraft:structure_void" || state.Name == "minecraft:jigsaw" || state.Name == "minecraft:structure_block" {
			continue
		}
		pos := transformTemplatePos(block.Pos, template.Size, rotation, mirror)
		chunk.SetBlock(originX+pos[0], originY+pos[1], originZ+pos[2], state)
	}
}

// templateCornerAirCount 统计模板四角在目标位置是否为空气，用于化石等结构
// 的“空角”约束。
func templateCornerAirCount(chunk *GeneratedChunk, template *structureTemplate, originX, originY, originZ int, rotation templateRotation, mirror bool) int {
	size := transformedSize(template.Size, rotation)
	corners := [8][3]int{
		{0, 0, 0}, {size[0] - 1, 0, 0}, {0, 0, size[2] - 1}, {size[0] - 1, 0, size[2] - 1},
		{0, size[1] - 1, 0}, {size[0] - 1, size[1] - 1, 0}, {0, size[1] - 1, size[2] - 1}, {size[0] - 1, size[1] - 1, size[2] - 1},
	}
	empty := 0
	for _, corner := range corners {
		if chunk.BlockAt(originX+corner[0], originY+corner[1], originZ+corner[2]).IsAir() {
			empty++
		}
	}
	return empty
}
