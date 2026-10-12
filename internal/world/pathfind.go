package world

// 地表行走模型的 A* 寻路（供生物 AI 使用）。
//
// 网格模型：每个 (x, z) 列有一个“脚部 Y”（站立时脚底所在的坐标），
// 由 PathGrid 提供。相邻列之间允许最多 MaxStepUp 格上跳与 MaxFall 格下落，
// 其余情况视为不可通行。搜索为 4 邻接（无对角移动），代价含上下坡惩罚，
// 启发式为曼哈顿距离（可采纳，保证最短路）。
//
// 限制：只考虑地表列高度（不含头顶净空、水中游泳与飞行），
// 因此适用于地表行走的生物；搜索规模由 MaxNodes/MaxRange 约束，
// 超限返回 nil（调用方回退到直线移动）。

import "container/heap"

// PathNode 是寻路中的一个格子位置（Y 为脚部坐标）。
type PathNode struct{ X, Y, Z int }

// PathGrid 报告列 (x, z) 是否可行走；可行走时返回脚部 Y。
type PathGrid func(x, z int) (feetY int, ok bool)

// SurfacePathGrid 返回基于世界高度列缓存的行走网格：
// 列顶为固体（非空气、非水）且不是水/岩浆时可行走，脚部 = 顶部固体 + 1。
func SurfacePathGrid(w *World) PathGrid {
	return func(x, z int) (int, bool) {
		column, ok := w.ColumnAt(x, z)
		if !ok || !column.HasTop || !column.HasSolid {
			return 0, false
		}
		switch column.TopState {
		case WaterBlock, LavaBlock:
			return 0, false
		}
		return column.SolidY + 1, true
	}
}

// PathLimits 限制一次寻路的规模与允许的落差。
type PathLimits struct {
	// MaxNodes 是最大扩展节点数（超出返回 nil）。
	MaxNodes int
	// MaxRange 是起终点最大曼哈顿距离（超出直接返回 nil）。
	MaxRange int
	// MaxStepUp 是相邻列允许的最大上跳格数。
	MaxStepUp int
	// MaxFall 是相邻列允许的最大下落格数。
	MaxFall int
}

// DefaultPathLimits 返回常用限制（512 节点、48 格、上跳 1、下落 3）。
func DefaultPathLimits() PathLimits {
	return PathLimits{MaxNodes: 512, MaxRange: 48, MaxStepUp: 1, MaxFall: 3}
}

// pathInfo 是 A* 的节点状态。
type pathInfo struct {
	g      float64
	parent PathNode
	closed bool
}

// pathQueueItem 是优先级队列元素。
type pathQueueItem struct {
	node PathNode
	f    float64
}

// pathQueue 是按 f 值的最小堆。
type pathQueue []pathQueueItem

func (q pathQueue) Len() int           { return len(q) }
func (q pathQueue) Less(i, j int) bool { return q[i].f < q[j].f }
func (q pathQueue) Swap(i, j int)      { q[i], q[j] = q[j], q[i] }
func (q *pathQueue) Push(x any)        { *q = append(*q, x.(pathQueueItem)) }
func (q *pathQueue) Pop() any {
	old := *q
	n := len(old)
	item := old[n-1]
	*q = old[:n-1]
	return item
}

// manhattanDistance 返回水平曼哈顿距离。
func manhattanDistance(ax, az, bx, bz int) int {
	dx, dz := ax-bx, az-bz
	if dx < 0 {
		dx = -dx
	}
	if dz < 0 {
		dz = -dz
	}
	return dx + dz
}

// pathHeuristic 是到目标的曼哈顿距离启发式（目标允许“相邻即到达”，
// 因此减去 1 仍然可采纳）。
func pathHeuristic(n, goal PathNode) float64 {
	d := manhattanDistance(n.X, n.Z, goal.X, goal.Z)
	if d <= 1 {
		return 0
	}
	return float64(d - 1)
}

// pathStepCost 是相邻列移动的代价：上坡 +0.4/格、下坡 +0.1/格。
func pathStepCost(dy int) float64 {
	cost := 1.0
	if dy > 0 {
		cost += 0.4 * float64(dy)
	} else if dy < 0 {
		cost += 0.1 * float64(-dy)
	}
	return cost
}

// pathArrived 报告节点 n 是否视为到达目标：水平相邻（曼哈顿 ≤ 1）
// 且高度差不超过 1 格（生物可触及的范围）。
func pathArrived(n, goal PathNode) bool {
	if manhattanDistance(n.X, n.Z, goal.X, goal.Z) > 1 {
		return false
	}
	dy := n.Y - goal.Y
	return dy >= -1 && dy <= 1
}

// FindPath 在行走网格上做 A* 搜索。
//
// from 是起点（其 Y 会被网格值覆盖）；goal 是目标格。当搜索到达目标格
// 或与目标水平相邻（曼哈顿距离 ≤ 1、高度差 ≤ 1）时结束——后者让生物可以
// 停在不可站立的玩家旁边。
//
// 返回不含起点的节点序列（最后一项为终点/相邻点）；起点已到达时返回
// 非 nil 空序列；无路径或超限返回 nil。
func FindPath(grid PathGrid, from, goal PathNode, limits PathLimits) []PathNode {
	if limits.MaxNodes <= 0 {
		limits.MaxNodes = 512
	}
	if limits.MaxRange <= 0 {
		limits.MaxRange = 48
	}
	if limits.MaxStepUp <= 0 {
		limits.MaxStepUp = 1
	}
	if limits.MaxFall <= 0 {
		limits.MaxFall = 3
	}
	if feet, ok := grid(from.X, from.Z); ok {
		from.Y = feet
	}
	if feet, ok := grid(goal.X, goal.Z); ok {
		goal.Y = feet
	}
	if manhattanDistance(from.X, from.Z, goal.X, goal.Z) > limits.MaxRange {
		return nil
	}
	if pathArrived(from, goal) {
		return []PathNode{}
	}

	info := map[PathNode]*pathInfo{from: {g: 0}}
	queue := &pathQueue{{node: from, f: pathHeuristic(from, goal)}}
	heap.Init(queue)
	visited := 0
	neighbors := [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}

	for queue.Len() > 0 {
		item := heap.Pop(queue).(pathQueueItem)
		current := item.node
		state := info[current]
		if state == nil || state.closed {
			continue
		}
		if pathArrived(current, goal) {
			return reconstructPath(info, current, from)
		}
		state.closed = true
		visited++
		if visited > limits.MaxNodes {
			return nil
		}
		for _, d := range neighbors {
			nx, nz := current.X+d[0], current.Z+d[1]
			feet, ok := grid(nx, nz)
			if !ok {
				continue
			}
			dy := feet - current.Y
			if dy > limits.MaxStepUp || dy < -limits.MaxFall {
				continue
			}
			next := PathNode{X: nx, Y: feet, Z: nz}
			g := state.g + pathStepCost(dy)
			if old, ok := info[next]; ok && (old.closed || old.g <= g) {
				continue
			}
			info[next] = &pathInfo{g: g, parent: current}
			heap.Push(queue, pathQueueItem{node: next, f: g + pathHeuristic(next, goal)})
		}
	}
	return nil
}

// reconstructPath 从终点回溯到起点，返回不含起点的正向路径。
func reconstructPath(info map[PathNode]*pathInfo, end, from PathNode) []PathNode {
	var reversed []PathNode
	for current := end; current != from; {
		reversed = append(reversed, current)
		state := info[current]
		if state == nil {
			return nil
		}
		current = state.parent
	}
	// 反转。
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	return reversed
}
