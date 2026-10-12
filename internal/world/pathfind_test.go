package world

import "testing"

// testPathGrid 从 (x,z) → 脚部 Y 的高度表构造行走网格；缺失的列不可行走。
type testPathGrid map[[2]int]int

func (g testPathGrid) grid() PathGrid {
	return func(x, z int) (int, bool) {
		feet, ok := g[[2]int{x, z}]
		return feet, ok
	}
}

// assertPathValid 校验路径的连续性与合法性。
func assertPathValid(t *testing.T, grid PathGrid, from, goal PathNode, path []PathNode, limits PathLimits) {
	t.Helper()
	current := from
	if feet, ok := grid(from.X, from.Z); ok {
		current.Y = feet
	}
	for i, node := range path {
		if _, ok := grid(node.X, node.Z); !ok {
			t.Fatalf("路径节点 %d %v 不可行走", i, node)
		}
		if d := manhattanDistance(current.X, current.Z, node.X, node.Z); d != 1 {
			t.Fatalf("路径节点 %d %v 与前一节点 %v 不相邻（距离 %d）", i, node, current, d)
		}
		dy := node.Y - current.Y
		if dy > limits.MaxStepUp || dy < -limits.MaxFall {
			t.Fatalf("路径节点 %d 落差 %d 超限", i, dy)
		}
		current = node
	}
	if d := manhattanDistance(current.X, current.Z, goal.X, goal.Z); d > 1 {
		t.Fatalf("路径终点 %v 与目标 %v 不相邻（距离 %d）", current, goal, d)
	}
}

func TestFindPathStraightLine(t *testing.T) {
	heights := testPathGrid{}
	for x := 0; x <= 6; x++ {
		heights[[2]int{x, 0}] = 64
	}
	grid := heights.grid()
	from := PathNode{X: 0, Y: 64, Z: 0}
	goal := PathNode{X: 5, Y: 64, Z: 0}
	path := FindPath(grid, from, goal, DefaultPathLimits())
	if path == nil {
		t.Fatal("直线路径不应为 nil")
	}
	if len(path) != 4 { // 0→1→2→3→4，与目标 5 相邻即停
		t.Fatalf("路径长度 = %d, want 4（%v）", len(path), path)
	}
	if last := path[len(path)-1]; last.X != 4 || last.Z != 0 {
		t.Fatalf("路径终点 = %v, want (4,*,0)", last)
	}
	assertPathValid(t, grid, from, goal, path, DefaultPathLimits())
}

func TestFindPathAroundWall(t *testing.T) {
	// 7×5 平地，x=3 处 z=1..3 是墙（不可行走），只能从 z=0 或 z=4 绕行。
	heights := testPathGrid{}
	for x := 0; x <= 6; x++ {
		for z := 0; z <= 4; z++ {
			if x == 3 && z >= 1 && z <= 3 {
				continue
			}
			heights[[2]int{x, z}] = 64
		}
	}
	grid := heights.grid()
	from := PathNode{X: 0, Y: 64, Z: 2}
	goal := PathNode{X: 6, Y: 64, Z: 2}
	path := FindPath(grid, from, goal, DefaultPathLimits())
	if path == nil {
		t.Fatal("绕墙路径不应为 nil")
	}
	if len(path) < 8 {
		t.Fatalf("绕墙路径过短：%v", path)
	}
	assertPathValid(t, grid, from, goal, path, DefaultPathLimits())
	// 必须经过 z=0 或 z=4 的绕行点。
	detoured := false
	for _, node := range path {
		if node.Z == 0 || node.Z == 4 {
			detoured = true
		}
	}
	if !detoured {
		t.Fatalf("路径未绕行：%v", path)
	}
}

func TestFindPathStepUpAndDown(t *testing.T) {
	// x=0..2 高 64，x=3 高 65，x=4 高 66：允许逐级上跳。
	heights := testPathGrid{
		{0, 0}: 64, {1, 0}: 64, {2, 0}: 64, {3, 0}: 65, {4, 0}: 66,
	}
	grid := heights.grid()
	path := FindPath(grid, PathNode{X: 0, Y: 64, Z: 0}, PathNode{X: 4, Y: 66, Z: 0}, DefaultPathLimits())
	if path == nil {
		t.Fatal("逐级上跳路径不应为 nil")
	}
	assertPathValid(t, grid, PathNode{X: 0, Y: 64, Z: 0}, PathNode{X: 4, Y: 66, Z: 0}, path, DefaultPathLimits())

	// 2 格高的墙无法直接上跳，且无绕行。
	blocked := testPathGrid{{0, 0}: 64, {1, 0}: 66}
	path = FindPath(blocked.grid(), PathNode{X: 0, Y: 64, Z: 0}, PathNode{X: 1, Y: 66, Z: 0}, DefaultPathLimits())
	if path != nil {
		t.Fatalf("2 格高差不应有路径：%v", path)
	}
}

func TestFindPathFallLimit(t *testing.T) {
	// 下落 4 格超过 MaxFall=3。
	heights := testPathGrid{{0, 0}: 64, {1, 0}: 60}
	path := FindPath(heights.grid(), PathNode{X: 0, Y: 64, Z: 0}, PathNode{X: 1, Y: 60, Z: 0}, DefaultPathLimits())
	if path != nil {
		t.Fatalf("超过下落上限不应有路径：%v", path)
	}
	// 下落 3 格允许。
	heights[[2]int{1, 0}] = 61
	path = FindPath(heights.grid(), PathNode{X: 0, Y: 64, Z: 0}, PathNode{X: 1, Y: 61, Z: 0}, DefaultPathLimits())
	if path == nil {
		t.Fatal("下落 3 格应有路径")
	}
}

func TestFindPathUnreachable(t *testing.T) {
	// 孤立列。
	heights := testPathGrid{{0, 0}: 64, {5, 5}: 64}
	path := FindPath(heights.grid(), PathNode{X: 0, Y: 64, Z: 0}, PathNode{X: 5, Y: 64, Z: 5}, DefaultPathLimits())
	if path != nil {
		t.Fatalf("不可达目标不应有路径：%v", path)
	}
}

func TestFindPathAlreadyAdjacent(t *testing.T) {
	heights := testPathGrid{{0, 0}: 64, {1, 0}: 64}
	path := FindPath(heights.grid(), PathNode{X: 0, Y: 64, Z: 0}, PathNode{X: 1, Y: 64, Z: 0}, DefaultPathLimits())
	if path == nil {
		t.Fatal("相邻目标应返回非 nil 空路径")
	}
	if len(path) != 0 {
		t.Fatalf("相邻目标路径应为空：%v", path)
	}
	// 相邻但高差 2 格：不算到达。
	heights[[2]int{1, 0}] = 66
	path = FindPath(heights.grid(), PathNode{X: 0, Y: 64, Z: 0}, PathNode{X: 1, Y: 66, Z: 0}, DefaultPathLimits())
	if path != nil {
		t.Fatalf("高差 2 格不应到达：%v", path)
	}
}

func TestFindPathRangeAndNodeLimits(t *testing.T) {
	heights := testPathGrid{}
	for x := 0; x <= 200; x++ {
		heights[[2]int{x, 0}] = 64
	}
	grid := heights.grid()
	from := PathNode{X: 0, Y: 64, Z: 0}
	goal := PathNode{X: 150, Y: 64, Z: 0}
	if path := FindPath(grid, from, goal, DefaultPathLimits()); path != nil {
		t.Fatalf("超出 MaxRange 不应有路径：len=%d", len(path))
	}
	limits := DefaultPathLimits()
	limits.MaxNodes = 20
	goal = PathNode{X: 100, Y: 64, Z: 0}
	if path := FindPath(grid, from, goal, limits); path != nil {
		t.Fatalf("超出 MaxNodes 不应有路径：len=%d", len(path))
	}
}

func TestSurfacePathGrid(t *testing.T) {
	w := newTestWorld(t)
	grid := SurfacePathGrid(w)
	feet, ok := grid(0, 0)
	if !ok {
		t.Fatal("平地列应可行走")
	}
	if feet <= WorldMinY {
		t.Fatalf("脚部 Y = %d 不合理", feet)
	}
	// 把 (4, 4) 的地表替换为水：该列不再可行走。
	if !w.SetBlock(4, feet-1, 4, WaterBlock) {
		t.Fatal("设置水方块失败")
	}
	if _, ok := grid(4, 4); ok {
		t.Fatal("水面列不应可行走")
	}
}
