//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"fmt"
	"math"
)

// Context 是密度函数求值上下文（原版 DensityFunction.FunctionContext 只需要方块坐标）。
type Context struct {
	X, Y, Z int
}

// Evaluator 在给定种子下对密度函数图求值。
//
// 已实现的节点语义全部按 1.21.11 服务端字节码核对：
//   - euk$d BlendAlpha  → 1.0
//   - euk$f BlendOffset → 0.0
//   - euk$p Noise       → noise(x*xz_scale, y*y_scale, z*xz_scale)
//   - euk$w ShiftedNoise→ noise(x*xz+shiftX, y*y+shiftY, z*xz+shiftZ)
//   - euk$v ShiftNoise  → noise(x*0.25, y*0.25, z*0.25) * 4.0
//     （Shift/ShiftA/ShiftB 只是坐标取法不同）
//   - euk$aa WeirdScaledSampler → scale * |noise(x/scale, y/scale, z/scale)|，
//     scale 由 type_1/type_2 的稀有度函数从 input 取值映射得到
//   - euk$ab YClampedGradient → Mth.clampedMap(y, from_y, to_y, from_value, to_value)
//   - euk$l Mapped      → abs/square/cube/half_negative/quarter_negative/invert/squeeze
//   - euk$g Clamp       → Mth.clamp(v, min, max)
//   - euk$r RangeChoice → min_inclusive <= v < max_exclusive 时取 when_in_range
//   - euk$a Ap2         → add/mul/min/max
//
// 尚未实现（会 panic 而不是静默返回 0）：spline（CubicSpline，float 精度）、
// interpolated（区块级 4×8×4 网格插值）、find_top_surface、old_blended_noise、
// end_islands。这些是阶段 3 的后续部分。
type Evaluator struct {
	noises       *NoiseRegistry
	blended      map[*Node]*BlendedNoise
	corners      map[cornerKey]float64
	cells        map[cellKey][2][2][2]float64
	grids        map[*Node]*denseGrid
	finalDensity *Node
	columns      map[columnKey]float64
	cellWidth    int
	cellHeight   int
}

// columnKey 是 flat_cache / cache_2d 的按列缓存键：节点 + (x, z)。
type columnKey struct {
	node *Node
	x    int
	z    int
}

// cornerKey 是 interpolated 角点缓存的键：节点 + 格点坐标。
// 相邻单元格共享角点，所以每个区块实际只算一次 lattice。
type cornerKey struct {
	node *Node
	x    int
	y    int
	z    int
}

// cellKey 是整格 8 角点缓存的键：节点 + 单元格原点。
type cellKey struct {
	node *Node
	x    int
	y    int
	z    int
}

// NewEvaluator 为给定噪声注册表创建求值器，内插值网格默认按 overworld 的
// 4×8 单元格（noise.size_horizontal=1、size_vertical=2）。
func NewEvaluator(noises *NoiseRegistry) *Evaluator {
	return NewEvaluatorWithCells(noises, 4, 8)
}

// NewEvaluatorWithCells 指定 interpolated 的单元格尺寸
// （原版 NoiseChunk：4*size_horizontal 与 4*size_vertical）。
func NewEvaluatorWithCells(noises *NoiseRegistry, cellWidth, cellHeight int) *Evaluator {
	if cellWidth <= 0 || cellHeight <= 0 {
		panic("vanilla: 内插值单元格尺寸必须为正")
	}
	return &Evaluator{
		noises:     noises,
		blended:    make(map[*Node]*BlendedNoise),
		corners:    make(map[cornerKey]float64),
		cells:      make(map[cellKey][2][2][2]float64),
		columns:    make(map[columnKey]float64),
		grids:      make(map[*Node]*denseGrid),
		cellWidth:  cellWidth,
		cellHeight: cellHeight,
	}
}

// ResetInterpolationCache 清空 interpolated 的角点与单元格缓存。区块生成时应当
// 每区块调用一次，否则缓存会随生成范围无限增长（值本身可重算，清空不影响结果）。
func (e *Evaluator) ResetInterpolationCache() {
	e.corners = make(map[cornerKey]float64)
	e.cells = make(map[cellKey][2][2][2]float64)
	e.columns = make(map[columnKey]float64)
}

// NoiseRegistry 返回求值器使用的噪声注册表。
func (e *Evaluator) NoiseRegistry() *NoiseRegistry {
	return e.noises
}

// Evaluate 求值一个密度函数节点。遇到未实现的节点类型会 panic：
// 静默返回 0 会产出错误地形，宁可尽早暴露。
func (e *Evaluator) Evaluate(node *Node, ctx Context) float64 {
	if node == nil {
		panic("vanilla: 对 nil 密度函数节点求值")
	}
	switch node.Type {
	case "constant":
		return node.Value
	case "coordinate":
		switch node.RefName {
		case "minecraft:x":
			return float64(ctx.X)
		case "minecraft:y":
			return float64(ctx.Y)
		case "minecraft:z":
			return float64(ctx.Z)
		}
		panic("vanilla: 未知的内置坐标密度函数 " + node.RefName)
	case "add":
		return e.child(node, 0, ctx) + e.child(node, 1, ctx)
	case "mul":
		return e.child(node, 0, ctx) * e.child(node, 1, ctx)
	case "min":
		return math.Min(e.child(node, 0, ctx), e.child(node, 1, ctx))
	case "max":
		return math.Max(e.child(node, 0, ctx), e.child(node, 1, ctx))
	case "abs", "square", "cube", "half_negative", "quarter_negative", "invert", "squeeze":
		return mappedValue(node.Type, e.child(node, 0, ctx))
	case "clamp":
		return clampValue(e.child(node, 0, ctx), node.Min, node.Max)
	case "y_clamped_gradient":
		return clampedMap(float64(ctx.Y), node.FromY, node.ToY, node.FromValue, node.ToValue)
	case "noise":
		noise := e.noises.Noise(node.Noise)
		x, y, z := float64(ctx.X), float64(ctx.Y), float64(ctx.Z)
		return noise.Value(x*node.XZScale, y*node.YScale, z*node.XZScale)
	case "shifted_noise":
		noise := e.noises.Noise(node.Noise)
		x := float64(ctx.X)*node.XZScale + e.child(node, 0, ctx)
		y := float64(ctx.Y)*node.YScale + e.child(node, 1, ctx)
		z := float64(ctx.Z)*node.XZScale + e.child(node, 2, ctx)
		return noise.Value(x, y, z)
	case "shift", "shift_a", "shift_b":
		noise := e.noises.Noise(node.Noise)
		x, y, z := float64(ctx.X), float64(ctx.Y), float64(ctx.Z)
		switch node.Type {
		case "shift_a":
			y = 0
		case "shift_b":
			x, y, z = z, x, 0
		}
		return noise.Value(x*0.25, y*0.25, z*0.25) * 4
	case "weird_scaled_sampler":
		input := e.child(node, 0, ctx)
		scale := rarityScale(node.RarityMapper, input)
		noise := e.noises.Noise(node.Noise)
		return scale * math.Abs(noise.Value(
			float64(ctx.X)/scale,
			float64(ctx.Y)/scale,
			float64(ctx.Z)/scale,
		))
	case "range_choice":
		value := e.child(node, 0, ctx)
		if value >= node.Min && value < node.Max {
			return e.child(node, 1, ctx)
		}
		return e.child(node, 2, ctx)
	case "blend_density":
		// 没有邻区块混合时原版 Blender.blendDensity 是恒等变换。
		return e.child(node, 0, ctx)
	case "blend_alpha":
		return 1
	case "blend_offset":
		return 0
	case "flat_cache", "cache_2d":
		// 原版 euu$g(FlatCache) 用 (x, z) 索引的数组、euu$d(Cache2D) 缓存
		// 最后一次 (x, z) 的结果，两者都是“每个 (x, z) 只算一次、与 y 无关”。
		return e.columnValue(node, ctx)
	case "cache_once", "cache_all_in_cell":
		// 原版这两种缓存的粒度绑在 NoiseChunk 的插值格上（euu.F 计数器 /
		// 格内数组）。当前按逐点直通处理：语义上等价于“每个方块算一次”，
		// 但比原版多算了同格内其余方块——已在 docs/TODO.md 记为待优化项。
		return e.child(node, 0, ctx)
	case "spline":
		if node.Spline == nil {
			panic("vanilla: spline 节点缺少控制点表")
		}
		// 原版 Spline.compute 先把坐标密度函数求值成 double、再窄化成 float 参与
		// CubicSpline 求值，最后 f2d 转回 double。
		return float64(e.evaluateSpline(node.Spline, ctx))
	case "old_blended_noise":
		return e.blendedNoise(node).Value(ctx.X, ctx.Y, ctx.Z)
	case "interpolated":
		return e.evaluateInterpolated(node, ctx)
	case "find_top_surface":
		return e.evaluateFindTopSurface(node, ctx)
	}
	panic(fmt.Sprintf("vanilla: 密度函数节点类型 %q 尚未实现求值", node.Type))
}

// evaluateSpline 求值原版 CubicSpline.Multipoint（float 精度）。
//
// 公式（按 bfe$e 字节码逐条复刻）：
//
//	if f < locations[0]  → linearExtend(f, ..., 0)
//	if f >= locations[n-1] → linearExtend(f, ..., n-1)
//	否则取区间 i，t = (f-loc[i])/(loc[i+1]-loc[i])，
//	  a = d[i]*(loc[i+1]-loc[i]) - (y[i+1]-y[i])
//	  b = -d[i+1]*(loc[i+1]-loc[i]) + (y[i+1]-y[i])
//	  result = lerp(t, y[i], y[i+1]) + t*(1-t)*lerp(t, a, b)
func (e *Evaluator) evaluateSpline(spline *Spline, ctx Context) float32 {
	points := spline.Points
	if len(points) == 0 {
		panic("vanilla: spline 没有控制点")
	}
	coordinate := float32(e.Evaluate(spline.Coordinate, ctx))
	last := len(points) - 1
	index := len(points) - 1
	for i := range points {
		if coordinate < float32(points[i].Location) {
			index = i - 1
			break
		}
	}
	switch {
	case index < 0:
		return linearExtendSpline(coordinate, points[0], e.splinePointValue(points[0], ctx))
	case index == last:
		return linearExtendSpline(coordinate, points[last], e.splinePointValue(points[last], ctx))
	}
	first, second := points[index], points[index+1]
	start := float32(first.Location)
	end := float32(second.Location)
	t := (coordinate - start) / (end - start)
	y0 := e.splinePointValue(first, ctx)
	y1 := e.splinePointValue(second, ctx)
	slope0 := float32(first.Derivative)
	slope1 := float32(second.Derivative)
	a := slope0*(end-start) - (y1 - y0)
	b := -slope1*(end-start) + (y1 - y0)
	return lerpFloat(t, y0, y1) + t*(1-t)*lerpFloat(t, a, b)
}

// splinePointValue 取控制点的值：嵌套子样条按同一上下文递归求值。
func (e *Evaluator) splinePointValue(point SplinePoint, ctx Context) float32 {
	if point.Sub != nil {
		return e.evaluateSpline(point.Sub, ctx)
	}
	return float32(point.Value)
}

// linearExtendSpline 是 CubicSpline 在区间外的线性外推：导数为 0 时取端点值。
func linearExtendSpline(coordinate float32, point SplinePoint, value float32) float32 {
	derivative := float32(point.Derivative)
	if derivative == 0 {
		return value
	}
	return value + derivative*(coordinate-float32(point.Location))
}

// lerpFloat 是 Mth.lerp 的单精度版本：start + delta*(end-start)。
func lerpFloat(delta, start, end float32) float32 {
	return start + delta*(end-start)
}

// evaluateInterpolated 求值原版 NoiseChunk.NoiseInterpolator：
// 把子节点在 cellWidth×cellHeight×cellWidth 的单元格角点上采样，
// 再用方块在单元格内的相对偏移做三线性插值（线性，不是 smoothstep）。
//
// 性能：角点与整格 8 角点都做了缓存，所以同一格内的 128 个方块只会查一次表，
// 相邻格共享的 lattice 角点也只算一次。区块生成时每区块调用
// ResetInterpolationCache 释放缓存。
func (e *Evaluator) evaluateInterpolated(node *Node, ctx Context) float64 {
	if grid, ok := e.grids[node]; ok {
		if value, ok := grid.sample(ctx); ok {
			return value
		}
	}
	cellX := floorDiv(ctx.X, e.cellWidth) * e.cellWidth
	cellY := floorDiv(ctx.Y, e.cellHeight) * e.cellHeight
	cellZ := floorDiv(ctx.Z, e.cellWidth) * e.cellWidth
	deltaX := float64(ctx.X-cellX) / float64(e.cellWidth)
	deltaY := float64(ctx.Y-cellY) / float64(e.cellHeight)
	deltaZ := float64(ctx.Z-cellZ) / float64(e.cellWidth)

	corners := e.cellCorners(node, cellX, cellY, cellZ)
	// 与原版 11 参数 Mth.lerp2 相同的嵌套顺序：x 最内，其次 y，最后 z。
	plane := func(iz int) float64 {
		return lerp(deltaY,
			lerp(deltaX, corners[0][0][iz], corners[1][0][iz]),
			lerp(deltaX, corners[0][1][iz], corners[1][1][iz]))
	}
	return lerp(deltaZ, plane(0), plane(1))
}

// columnValue 取 flat_cache / cache_2d 子节点在某个 (x, z) 的值并缓存
// （同一列的 384 个方块只算一次）。
func (e *Evaluator) columnValue(node *Node, ctx Context) float64 {
	key := columnKey{node: node, x: ctx.X, z: ctx.Z}
	if value, ok := e.columns[key]; ok {
		return value
	}
	value := e.child(node, 0, ctx)
	e.columns[key] = value
	return value
}

// cellCorners 返回某个单元格 8 个角点的值（按单元格缓存）。
func (e *Evaluator) cellCorners(node *Node, cellX, cellY, cellZ int) [2][2][2]float64 {
	key := cellKey{node: node, x: cellX, y: cellY, z: cellZ}
	if corners, ok := e.cells[key]; ok {
		return corners
	}
	var corners [2][2][2]float64
	for ix := 0; ix < 2; ix++ {
		for iy := 0; iy < 2; iy++ {
			for iz := 0; iz < 2; iz++ {
				corners[ix][iy][iz] = e.cornerValue(node, cellX+ix*e.cellWidth, cellY+iy*e.cellHeight, cellZ+iz*e.cellWidth)
			}
		}
	}
	e.cells[key] = corners
	return corners
}

// cornerValue 取 interpolated 子节点在某个格点上的值，并按（节点, 坐标）缓存。
// 相邻单元格共享角点，缓存后每区块每个节点的 lattice 只算一次。
func (e *Evaluator) cornerValue(node *Node, x, y, z int) float64 {
	key := cornerKey{node: node, x: x, y: y, z: z}
	if value, ok := e.corners[key]; ok {
		return value
	}
	value := e.child(node, 0, Context{X: x, Y: y, Z: z})
	e.corners[key] = value
	return value
}

// evaluateFindTopSurface 求值原版 DensityFunctions.FindTopSurface：
// 从 upperBound 按 cell_height 对齐后的高度开始，按 cell_height 步长向下找
// 第一个密度 > 0 的高度；找到不到则返回 lower_bound。
func (e *Evaluator) evaluateFindTopSurface(node *Node, ctx Context) float64 {
	cellHeight := node.CellHeight
	if cellHeight <= 0 {
		panic("vanilla: find_top_surface 的 cell_height 必须为正")
	}
	upper := e.child(node, 1, ctx)
	top := int(math.Floor(upper/float64(cellHeight))) * cellHeight
	if top <= node.LowerBound {
		return float64(node.LowerBound)
	}
	for y := top; y >= node.LowerBound; y -= cellHeight {
		if e.child(node, 0, Context{X: ctx.X, Y: y, Z: ctx.Z}) > 0 {
			return float64(y)
		}
	}
	return float64(node.LowerBound)
}

// floorDiv 是向下取整的整数除法（原版 Mth.floorDiv）。
func floorDiv(value, divisor int) int {
	quotient := value / divisor
	if value%divisor != 0 && (value < 0) != (divisor < 0) {
		quotient--
	}
	return quotient
}

// blendedNoise 按节点缓存 BlendedNoise 实例（参数固定，随机源由种子派生）。
func (e *Evaluator) blendedNoise(node *Node) *BlendedNoise {
	if noise, ok := e.blended[node]; ok {
		return noise
	}
	// 解析时 xz_scale / y_scale 落在 XZScale / YScale，其余三项落在 Legacy[2..4]。
	noise := NewBlendedNoise(
		e.noises.FromHashOf("minecraft:terrain"),
		node.XZScale,
		node.YScale,
		node.Legacy[2],
		node.Legacy[3],
		node.Legacy[4],
	)
	e.blended[node] = noise
	return noise
}

// child 返回第 index 个子节点并求值。
func (e *Evaluator) child(node *Node, index int, ctx Context) float64 {
	if index >= len(node.Children) {
		panic(fmt.Sprintf("vanilla: 节点 %q 缺少第 %d 个子节点", node.Type, index))
	}
	return e.Evaluate(node.Children[index], ctx)
}

// mappedValue 实现原版 DensityFunctions.Mapped（Mapped.Type 的 ordinal 顺序为
// ABS/SQUARE/CUBE/HALF_NEGATIVE/QUARTER_NEGATIVE/INVERT/SQUEEZE）。
func mappedValue(kind string, value float64) float64 {
	switch kind {
	case "abs":
		return math.Abs(value)
	case "square":
		return value * value
	case "cube":
		return value * value * value
	case "half_negative":
		if value > 0 {
			return value
		}
		return value * 0.5
	case "quarter_negative":
		if value > 0 {
			return value
		}
		return value * 0.25
	case "invert":
		return 1 / value
	case "squeeze":
		clamped := clampValue(value, -1, 1)
		return clamped/2 - clamped*clamped*clamped/24
	}
	panic("vanilla: 未知的 Mapped 类型 " + kind)
}

// rarityScale 实现原版 WeirdScaledSampler.RarityValueMapper 的缩放函数
// （按 eux$a 的 a/b 两个静态方法字节码复刻）。
func rarityScale(mapper string, value float64) float64 {
	switch mapper {
	case "type_1":
		switch {
		case value < -0.5:
			return 0.75
		case value < 0:
			return 1
		case value < 0.5:
			return 1.5
		default:
			return 2
		}
	case "type_2":
		switch {
		case value < -0.75:
			return 0.5
		case value < -0.5:
			return 0.75
		case value < 0.5:
			return 1
		case value < 0.75:
			return 2
		default:
			return 3
		}
	}
	panic("vanilla: 未知的 rarity_value_mapper " + mapper)
}

// clampValue 是 Mth.clamp。
func clampValue(value, minValue, maxValue float64) float64 {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

// clampedMap 是 Mth.clampedMap = clampedLerp(inverseLerp(...), ...)。
func clampedMap(value, fromMin, fromMax, toMin, toMax float64) float64 {
	return clampedLerp(inverseLerp(value, fromMin, fromMax), toMin, toMax)
}

// inverseLerp 是 Mth.inverseLerp：(value - from) / (to - from)。
func inverseLerp(value, from, to float64) float64 {
	return (value - from) / (to - from)
}

// clampedLerp 是 Mth.clampedLerp：delta 夹到 [0,1] 后做线性插值。
func clampedLerp(delta, from, to float64) float64 {
	if delta < 0 {
		return from
	}
	if delta > 1 {
		return to
	}
	return from + delta*(to-from)
}

// lerpUnclamped 是 Mth.lerp：不做夹取的线性插值。
// 原版 NoiseBasedAquifer 用的是 Mth.b(DDDDD)，即
// lerp(inverseLerp(value, from, to), newFrom, newTo)，delta 可能超出 [0,1]。
func lerpUnclamped(delta, from, to float64) float64 {
	return from + delta*(to-from)
}

// roundToward 是 Mth.roundToward(double, int)：向下取整到 interval 的倍数。
func roundToward(value float64, interval int) int {
	return int(math.Floor(value/float64(interval))) * interval
}
