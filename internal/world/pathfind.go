//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

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
//
// 实现说明：每次搜索使用复用的工作区（sync.Pool），其中包含开放寻址
// 哈希表与显式二叉堆。表按 (x, z) 索引——网格对同一列总是返回同一个
// 脚部 Y，因此每列（包括不可行走的列）在一次搜索中最多查询一次网格；
// 与最初 map[PathNode]*pathInfo + container/heap 的实现相比，既消除了
// 每节点的堆分配与接口装箱，也避免了相邻扩展时的重复列查询。

import (
	"math"
	"sync"
)

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

// pathSlot 是哈希表中的一个格子状态。
type pathSlot struct {
	key    uint64 // 打包的列坐标（高 32 位 X、低 32 位 Z）
	feetY  int32  // 列的脚部 Y（walked 为 false 时无意义）
	parent int32  // 父槽位下标；起点为 -1
	g      float64
	gen    uint32 // 所属搜索的世代号（不等于当前世代即为空槽）
	walked bool   // 列可行走
	closed bool   // 已扩展
}

// pathHeapItem 是显式最小堆元素（按 f 排序）。
type pathHeapItem struct {
	slot int32
	f    float64
}

// pathSearch 是复用的 A* 工作区。
type pathSearch struct {
	slots      []pathSlot
	heap       []pathHeapItem
	generation uint32
}

var pathSearchPool = sync.Pool{New: func() any { return &pathSearch{} }}

// pathKey 把列坐标 (x, z) 打包为哈希键。
func pathKey(x, z int) uint64 {
	return uint64(uint32(int32(x)))<<32 | uint64(uint32(int32(z)))
}

// pathUnpackX 从键还原 X，pathUnpackZ 还原 Z。
func pathUnpackX(key uint64) int { return int(int32(key >> 32)) }
func pathUnpackZ(key uint64) int { return int(int32(key & 0xFFFFFFFF)) }

// pathHash 是键的混合哈希（splitmix64 终轧，低冲突）。
func pathHash(key uint64) uint64 {
	h := key * 0x9E3779B97F4A7C15
	h ^= h >> 29
	h *= 0xBF58476D1CE4E5B9
	h ^= h >> 32
	return h
}

// reset 为一次新搜索准备容量充足的工作区。不清零旧数据，用世代号区分
// 新旧内容；容量按最大插入量推导：每次扩展最多写入 4 个新槽位、扩展
// 次数不超过 maxNodes，因此插入量 ≤ 4·maxNodes+1，取容量 ≥ 6·maxNodes+16
// 的上幂可保证负载始终不超过 75%（探测一定能遇到空槽）。
func (ps *pathSearch) reset(maxNodes int) {
	need := 16
	for need < 6*maxNodes+16 {
		need <<= 1
	}
	if cap(ps.slots) < need {
		ps.slots = make([]pathSlot, need)
	} else {
		ps.slots = ps.slots[:need]
	}
	ps.heap = ps.heap[:0]
	ps.generation++
	if ps.generation == 0 {
		// 世代号回绕：清空一次以区分残留内容（约 40 亿次搜索后才发生）。
		clear(ps.slots)
		ps.generation = 1
	}
}

// find 返回键对应的槽位下标；键不在表中时返回 -1。
func (ps *pathSearch) find(key uint64) int32 {
	mask := uint64(len(ps.slots) - 1)
	for i := pathHash(key) & mask; ; i = (i + 1) & mask {
		slot := &ps.slots[i]
		if slot.gen != ps.generation {
			return -1
		}
		if slot.key == key {
			return int32(i)
		}
	}
}

// insert 写入新槽位并返回其下标；调用方必须保证键不在表中。
func (ps *pathSearch) insert(key uint64, feetY int32, walked bool, g float64, parent int32) int32 {
	mask := uint64(len(ps.slots) - 1)
	for i := pathHash(key) & mask; ; i = (i + 1) & mask {
		slot := &ps.slots[i]
		if slot.gen != ps.generation {
			slot.gen = ps.generation
			slot.key = key
			slot.feetY = feetY
			slot.walked = walked
			slot.g = g
			slot.parent = parent
			slot.closed = false
			return int32(i)
		}
	}
}

// push 把槽位按 f 值送入最小堆。
func (ps *pathSearch) push(slot int32, f float64) {
	ps.heap = append(ps.heap, pathHeapItem{slot: slot, f: f})
	i := len(ps.heap) - 1
	for i > 0 {
		parent := (i - 1) / 2
		if ps.heap[parent].f <= ps.heap[i].f {
			break
		}
		ps.heap[parent], ps.heap[i] = ps.heap[i], ps.heap[parent]
		i = parent
	}
}

// pop 弹出并返回 f 值最小的元素；调用方必须保证堆非空。
func (ps *pathSearch) pop() pathHeapItem {
	top := ps.heap[0]
	last := len(ps.heap) - 1
	ps.heap[0] = ps.heap[last]
	ps.heap = ps.heap[:last]
	i := 0
	for {
		left := 2*i + 1
		if left >= len(ps.heap) {
			break
		}
		smallest := left
		if right := left + 1; right < len(ps.heap) && ps.heap[right].f < ps.heap[left].f {
			smallest = right
		}
		if ps.heap[i].f <= ps.heap[smallest].f {
			break
		}
		ps.heap[i], ps.heap[smallest] = ps.heap[smallest], ps.heap[i]
		i = smallest
	}
	return top
}

// reconstruct 从终点沿父链回溯，返回不含起点的正向路径（新分配，可安全
// 逃逸到调用方；工作区随后归还池中）。
func (ps *pathSearch) reconstruct(end, start int32) []PathNode {
	count := 0
	for slot := end; slot != start; slot = ps.slots[slot].parent {
		count++
	}
	path := make([]PathNode, count)
	slot := end
	for i := count - 1; i >= 0; i-- {
		state := &ps.slots[slot]
		path[i] = PathNode{X: pathUnpackX(state.key), Y: int(state.feetY), Z: pathUnpackZ(state.key)}
		slot = state.parent
	}
	return path
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

	search := pathSearchPool.Get().(*pathSearch)
	defer pathSearchPool.Put(search)
	search.reset(limits.MaxNodes)

	start := search.insert(pathKey(from.X, from.Z), int32(from.Y), true, 0, -1)
	search.push(start, pathHeuristic(from, goal))

	visited := 0
	neighbors := [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}

	for len(search.heap) > 0 {
		item := search.pop()
		state := &search.slots[item.slot]
		if state.closed {
			continue // 懒删除：槽位可能以更优代价被重新入堆
		}
		current := PathNode{X: pathUnpackX(state.key), Y: int(state.feetY), Z: pathUnpackZ(state.key)}
		if pathArrived(current, goal) {
			return search.reconstruct(item.slot, start)
		}
		state.closed = true
		visited++
		if visited > limits.MaxNodes {
			return nil
		}
		for _, d := range neighbors {
			nx, nz := current.X+d[0], current.Z+d[1]
			key := pathKey(nx, nz)
			next := search.find(key)
			if next < 0 {
				// 首次访问该列：查询网格并缓存（不可行走的列同样缓存，
				// 避免相邻扩展时反复查询同一列）。
				feet, ok := grid(nx, nz)
				if !ok {
					search.insert(key, 0, false, 0, -1)
					continue
				}
				next = search.insert(key, int32(feet), true, math.Inf(1), -1)
			} else if !search.slots[next].walked {
				continue
			}
			nextState := &search.slots[next]
			dy := int(nextState.feetY) - current.Y
			if dy > limits.MaxStepUp || dy < -limits.MaxFall {
				continue
			}
			g := state.g + pathStepCost(dy)
			if nextState.closed || nextState.g <= g {
				continue
			}
			nextState.g = g
			nextState.parent = item.slot
			search.push(next, g+pathHeuristic(PathNode{X: nx, Y: int(nextState.feetY), Z: nz}, goal))
		}
	}
	return nil
}
