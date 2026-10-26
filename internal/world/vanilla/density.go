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
	noises *NoiseRegistry
}

// NewEvaluator 为给定噪声注册表创建求值器。
func NewEvaluator(noises *NoiseRegistry) *Evaluator {
	return &Evaluator{noises: noises}
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
	case "flat_cache", "cache_2d", "cache_once", "cache_all_in_cell":
		// 缓存只影响重复求值的开销与「同一格内只算一次」的语义，
		// 单点求值时等价于直接求值其子节点。
		return e.child(node, 0, ctx)
	case "spline":
		if node.Spline == nil {
			panic("vanilla: spline 节点缺少控制点表")
		}
		// 原版 Spline.compute 先把坐标密度函数求值成 double、再窄化成 float 参与
		// CubicSpline 求值，最后 f2d 转回 double。
		return float64(e.evaluateSpline(node.Spline, ctx))
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
