package registry

import "testing"

// 形状断言辅助。
func shapeOf(t *testing.T, name string) []ShapeBox {
	t.Helper()
	id, ok := BlockStateIDs[name]
	if !ok {
		t.Fatalf("缺少方块 %s 的默认状态 ID", name)
	}
	return BlockStateShape(id)
}

func boxEqual(a, b ShapeBox) bool {
	return a == b
}

// TestShapeFullBlocks 验证完整方块的碰撞形状。
func TestShapeFullBlocks(t *testing.T) {
	for _, name := range []string{
		"minecraft:stone", "minecraft:grass_block", "minecraft:oak_leaves",
		"minecraft:glass", "minecraft:slime_block",
	} {
		boxes := shapeOf(t, name)
		if len(boxes) != 1 || !boxEqual(boxes[0], ShapeBox{0, 0, 0, 16, 16, 16}) {
			t.Fatalf("%s 形状 = %v, want 整块", name, boxes)
		}
	}
}

// TestShapeEmpty 验证无碰撞方块（空气/水/火把等装饰）。
func TestShapeEmpty(t *testing.T) {
	for _, name := range []string{
		"minecraft:air", "minecraft:water", "minecraft:lava", "minecraft:torch",
		"minecraft:short_grass", "minecraft:oak_sapling", "minecraft:rail",
		"minecraft:oak_sign", "minecraft:wall_torch",
	} {
		if boxes := shapeOf(t, name); len(boxes) != 0 {
			t.Fatalf("%s 形状 = %v, want 无碰撞", name, boxes)
		}
	}
}

// TestShapeSlab 验证半砖三种形态（状态 ID 来自 reports/blocks.json）。
func TestShapeSlab(t *testing.T) {
	// oak_slab：bottom=13131（默认）、top=13129、double=13133。
	cases := []struct {
		state uint16
		want  ShapeBox
	}{
		{13131, ShapeBox{0, 0, 0, 16, 8, 16}},
		{13129, ShapeBox{0, 8, 0, 16, 16, 16}},
		{13133, ShapeBox{0, 0, 0, 16, 16, 16}},
	}
	for _, c := range cases {
		boxes := BlockStateShape(c.state)
		if len(boxes) != 1 || !boxEqual(boxes[0], c.want) {
			t.Fatalf("状态 %d 形状 = %v, want %v", c.state, boxes, c.want)
		}
	}
}

// TestShapeStairs 验证直段楼梯（默认状态：朝北上行、下半）。
func TestShapeStairs(t *testing.T) {
	boxes := shapeOf(t, "minecraft:oak_stairs")
	want := []ShapeBox{
		{0, 0, 0, 16, 8, 16},
		{0, 8, 0, 16, 16, 8},
	}
	if len(boxes) != len(want) {
		t.Fatalf("楼梯形状数 = %d, want %d：%v", len(boxes), len(want), boxes)
	}
	for i := range want {
		if !boxEqual(boxes[i], want[i]) {
			t.Fatalf("楼梯形状[%d] = %v, want %v", i, boxes[i], want[i])
		}
	}
}

// TestShapeFence 验证栅栏：默认无连接只有柱；全连接含四条臂。
// 碰撞柱 4×24×4、臂 4 厚 24 高（原版字节码常量）。
func TestShapeFence(t *testing.T) {
	boxes := shapeOf(t, "minecraft:oak_fence")
	if len(boxes) != 1 || !boxEqual(boxes[0], ShapeBox{6, 0, 6, 10, 24, 10}) {
		t.Fatalf("默认栅栏形状 = %v, want 仅柱", boxes)
	}
	// oak_fence 全连接状态 6764（east/north/south/west 全 true）。
	boxes = BlockStateShape(6764)
	if len(boxes) != 5 {
		t.Fatalf("全连接栅栏形状数 = %d, want 5：%v", len(boxes), boxes)
	}
}

// TestShapeSnowCactusFarm 验证雪层/仙人掌/耕地高度。
func TestShapeSnowCactusFarm(t *testing.T) {
	if boxes := shapeOf(t, "minecraft:snow"); len(boxes) != 1 ||
		!boxEqual(boxes[0], ShapeBox{0, 0, 0, 16, 2, 16}) {
		t.Fatalf("雪层形状 = %v, want 2/16 高", boxes)
	}
	if boxes := shapeOf(t, "minecraft:cactus"); len(boxes) != 1 ||
		!boxEqual(boxes[0], ShapeBox{1, 0, 1, 15, 15, 15}) {
		t.Fatalf("仙人掌形状 = %v", boxes)
	}
	if boxes := shapeOf(t, "minecraft:farmland"); len(boxes) != 1 ||
		!boxEqual(boxes[0], ShapeBox{0, 0, 0, 16, 15, 16}) {
		t.Fatalf("耕地形状 = %v, want 15/16 高", boxes)
	}
}

// TestShapeBedAndPlate 验证床（床垫 3–9）与压力板。
func TestShapeBedAndPlate(t *testing.T) {
	boxes := shapeOf(t, "minecraft:red_bed")
	found := false
	for _, b := range boxes {
		if boxEqual(b, ShapeBox{0, 3, 0, 16, 9, 16}) {
			found = true
		}
	}
	if !found {
		t.Fatalf("床形状 %v 缺少床垫盒", boxes)
	}
	if boxes := shapeOf(t, "minecraft:stone_pressure_plate"); len(boxes) != 1 ||
		!boxEqual(boxes[0], ShapeBox{1, 0, 1, 15, 1, 15}) {
		t.Fatalf("压力板形状 = %v", boxes)
	}
}

// TestStateShapeBounds 验证所有方块状态都可查询且不越界。
func TestStateShapeBounds(t *testing.T) {
	for id := range blockStateShapes {
		boxes := BlockStateShape(uint16(id))
		for _, b := range boxes {
			if b.X1 > b.X2 || b.Y1 > b.Y2 || b.Z1 > b.Z2 {
				t.Fatalf("状态 %d 的盒子坐标无序：%+v", id, b)
			}
			if b.X2 > 16 || b.Z2 > 16 || b.Y2 > 24 {
				t.Fatalf("状态 %d 的盒子坐标越界：%+v", id, b)
			}
		}
	}
}
