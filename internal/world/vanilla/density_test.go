//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"encoding/json"
	"math"
	"strconv"
	"testing"
)

// fmtFloat 把浮点数写成可嵌入 JSON 的字面量。
func fmtFloat(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}

// squeezeValue 按实现相同的运算顺序求值，避免常量折叠与运行时浮点结果差 1 ULP。
func squeezeValue(clamped float64) float64 {
	return clamped/2 - clamped*clamped*clamped/24
}

// mustParseNode 解析一段密度函数 JSON（失败即终止测试）。
func mustParseNode(t *testing.T, data *Data, text string) *Node {
	t.Helper()
	node, problems := parseNode(data, "test", json.RawMessage(text))
	if len(problems) > 0 {
		t.Fatalf("解析失败：%s", problems[0].String())
	}
	if node == nil {
		t.Fatal("解析结果为 nil")
	}
	return node
}

// mustEvaluator 用固定种子构造求值器。
func mustEvaluator(t *testing.T) *Evaluator {
	t.Helper()
	data, err := Load()
	if err != nil {
		t.Fatalf("加载数据失败：%v", err)
	}
	return NewEvaluator(NewNoiseRegistry(data, 0))
}

// TestEvaluateArithmetic 校验 add/mul/min/max/abs/square/cube/negate 类节点。
func TestEvaluateArithmetic(t *testing.T) {
	evaluator := mustEvaluator(t)
	data, err := Load()
	if err != nil {
		t.Fatalf("加载数据失败：%v", err)
	}
	ctx := Context{X: -3, Y: 7, Z: 11}
	cases := []struct {
		text string
		want float64
	}{
		{`{"type":"minecraft:constant","argument":2.5}`, 2.5},
		{`{"type":"minecraft:add","argument1":1.5,"argument2":2.5}`, 4},
		{`{"type":"minecraft:mul","argument1":1.5,"argument2":2.5}`, 3.75},
		{`{"type":"minecraft:min","argument1":1.5,"argument2":2.5}`, 1.5},
		{`{"type":"minecraft:max","argument1":1.5,"argument2":2.5}`, 2.5},
		{`{"type":"minecraft:abs","argument":-4.5}`, 4.5},
		{`{"type":"minecraft:square","argument":-3}`, 9},
		{`{"type":"minecraft:cube","argument":-2}`, -8},
		{`{"type":"minecraft:half_negative","argument":-3}`, -1.5},
		{`{"type":"minecraft:half_negative","argument":3}`, 3},
		{`{"type":"minecraft:quarter_negative","argument":-3}`, -0.75},
		{`{"type":"minecraft:quarter_negative","argument":3}`, 3},
		{`{"type":"minecraft:invert","argument":4}`, 0.25},
		{`{"type":"minecraft:squeeze","argument":1}`, squeezeValue(1)},
		{`{"type":"minecraft:squeeze","argument":9}`, squeezeValue(1)},
		{`{"type":"minecraft:clamp","input":15,"min":-1,"max":1}`, 1},
		{`{"type":"minecraft:clamp","input":-15,"min":-1,"max":1}`, -1},
		{`{"type":"minecraft:clamp","input":0.25,"min":-1,"max":1}`, 0.25},
	}
	for _, testCase := range cases {
		node := mustParseNode(t, data, testCase.text)
		if got := evaluator.Evaluate(node, ctx); got != testCase.want {
			t.Fatalf("%s = %v, want %v", testCase.text, got, testCase.want)
		}
	}
}

// TestEvaluateBuiltinCoordinates 校验内置的 x/y/z 坐标密度函数。
func TestEvaluateBuiltinCoordinates(t *testing.T) {
	evaluator := mustEvaluator(t)
	data, err := Load()
	if err != nil {
		t.Fatalf("加载数据失败：%v", err)
	}
	ctx := Context{X: -3, Y: 7, Z: 11}
	for name, want := range map[string]float64{"minecraft:x": -3, "minecraft:y": 7, "minecraft:z": 11} {
		node := mustParseNode(t, data, `"`+name+`"`)
		if got := evaluator.Evaluate(node, ctx); got != want {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
	}
}

// TestYClampedGradient 校验 y 夹取梯度（Mth.clampedMap）。
func TestYClampedGradient(t *testing.T) {
	evaluator := mustEvaluator(t)
	data, err := Load()
	if err != nil {
		t.Fatalf("加载数据失败：%v", err)
	}
	node := mustParseNode(t, data,
		`{"type":"minecraft:y_clamped_gradient","from_y":-64,"to_y":-40,"from_value":0,"to_value":1}`)
	// -64 → 0，-40 → 1，中间线性，范围外夹取。
	cases := map[int]float64{-100: 0, -64: 0, -52: 0.5, -40: 1, 100: 1}
	for y, want := range cases {
		if got := evaluator.Evaluate(node, Context{Y: y}); got != want {
			t.Fatalf("y=%d 时取值 = %v, want %v", y, got, want)
		}
	}
}

// TestShiftNoiseNodes 校验 shift / shift_a / shift_b 的坐标取法与 0.25×4 缩放。
func TestShiftNoiseNodes(t *testing.T) {
	evaluator := mustEvaluator(t)
	data, err := Load()
	if err != nil {
		t.Fatalf("加载数据失败：%v", err)
	}
	noise := evaluator.NoiseRegistry().Noise("minecraft:offset")
	ctx := Context{X: 40, Y: 64, Z: -12}
	cases := []struct {
		text string
		want float64
	}{
		{`{"type":"minecraft:shift","argument":"minecraft:offset"}`,
			noise.Value(10, 16, -3) * 4},
		{`{"type":"minecraft:shift_a","argument":"minecraft:offset"}`,
			noise.Value(10, 0, -3) * 4},
		{`{"type":"minecraft:shift_b","argument":"minecraft:offset"}`,
			noise.Value(-3, 10, 0) * 4},
	}
	for _, testCase := range cases {
		node := mustParseNode(t, data, testCase.text)
		if got := evaluator.Evaluate(node, ctx); got != testCase.want {
			t.Fatalf("%s = %v, want %v", testCase.text, got, testCase.want)
		}
	}
}

// TestNoiseAndShiftedNoise 校验 noise 的缩放与 shifted_noise 的位移相加。
func TestNoiseAndShiftedNoise(t *testing.T) {
	evaluator := mustEvaluator(t)
	data, err := Load()
	if err != nil {
		t.Fatalf("加载数据失败：%v", err)
	}
	noise := evaluator.NoiseRegistry().Noise("minecraft:continentalness")
	ctx := Context{X: 100, Y: 64, Z: -200}

	plain := mustParseNode(t, data,
		`{"type":"minecraft:noise","noise":"minecraft:continentalness","xz_scale":0.25,"y_scale":0.125}`)
	if got, want := evaluator.Evaluate(plain, ctx), noise.Value(25, 8, -50); got != want {
		t.Fatalf("noise = %v, want %v", got, want)
	}

	shifted := mustParseNode(t, data,
		`{"type":"minecraft:shifted_noise","noise":"minecraft:continentalness","xz_scale":0.25,"y_scale":0.125,"shift_x":1.5,"shift_y":-2,"shift_z":3.5}`)
	if got, want := evaluator.Evaluate(shifted, ctx), noise.Value(26.5, 6, -46.5); got != want {
		t.Fatalf("shifted_noise = %v, want %v", got, want)
	}
}

// TestWeirdScaledSampler 校验两种稀有度映射的缩放函数。
func TestWeirdScaledSampler(t *testing.T) {
	evaluator := mustEvaluator(t)
	data, err := Load()
	if err != nil {
		t.Fatalf("加载数据失败：%v", err)
	}
	noise := evaluator.NoiseRegistry().Noise("minecraft:spaghetti_2d")
	ctx := Context{X: 30, Y: 20, Z: -40}

	// type_1：0.5 落在 else 分支 → 2.0；-0.4 落在 [−0.5,0) → 1.0。
	scales := []struct {
		input float64
		want  float64
	}{
		{-0.9, 0.75}, {-0.6, 0.75}, {-0.5, 1}, {-0.1, 1}, {0, 1.5}, {0.4, 1.5}, {0.5, 2},
	}
	for _, testCase := range scales {
		text := `{"type":"minecraft:weird_scaled_sampler","rarity_value_mapper":"type_1","noise":"minecraft:spaghetti_2d","input":` +
			fmtFloat(testCase.input) + `}`
		node := mustParseNode(t, data, text)
		want := testCase.want * math.Abs(noise.Value(30/testCase.want, 20/testCase.want, -40/testCase.want))
		if got := evaluator.Evaluate(node, ctx); got != want {
			t.Fatalf("type_1 input=%v 取值 = %v, want %v", testCase.input, got, want)
		}
	}

	// type_2 的边界：-0.75 → 0.75，-0.5 → 1，0.5 → 2，0.75 → 3。
	scales2 := []struct {
		input float64
		want  float64
	}{
		{-0.8, 0.5}, {-0.75, 0.75}, {-0.5, 1}, {0.4, 1}, {0.5, 2}, {0.7, 2}, {0.75, 3},
	}
	for _, testCase := range scales2 {
		text := `{"type":"minecraft:weird_scaled_sampler","rarity_value_mapper":"type_2","noise":"minecraft:spaghetti_2d","input":` +
			fmtFloat(testCase.input) + `}`
		node := mustParseNode(t, data, text)
		want := testCase.want * math.Abs(noise.Value(30/testCase.want, 20/testCase.want, -40/testCase.want))
		if got := evaluator.Evaluate(node, ctx); got != want {
			t.Fatalf("type_2 input=%v 取值 = %v, want %v", testCase.input, got, want)
		}
	}
}

// TestRangeChoiceUpperBoundExclusive 校验 range_choice 的上界是开区间。
func TestRangeChoiceUpperBoundExclusive(t *testing.T) {
	evaluator := mustEvaluator(t)
	data, err := Load()
	if err != nil {
		t.Fatalf("加载数据失败：%v", err)
	}
	build := func(input float64) *Node {
		return mustParseNode(t, data,
			`{"type":"minecraft:range_choice","input":`+fmtFloat(input)+
				`,"min_inclusive":-1,"max_exclusive":1,"when_in_range":10,"when_out_of_range":20}`)
	}
	for _, testCase := range []struct {
		input float64
		want  float64
	}{{-1, 10}, {-0.999, 10}, {0, 10}, {0.999, 10}, {1, 20}, {2, 20}, {-2, 20}} {
		if got := evaluator.Evaluate(build(testCase.input), Context{}); got != testCase.want {
			t.Fatalf("input=%v 取值 = %v, want %v", testCase.input, got, testCase.want)
		}
	}
}

// TestUnsupportedNodePanics 校验未实现节点显式 panic（不静默返回 0）。
func TestUnsupportedNodePanics(t *testing.T) {
	evaluator := mustEvaluator(t)
	data, err := Load()
	if err != nil {
		t.Fatalf("加载数据失败：%v", err)
	}
	node := mustParseNode(t, data, `{"type":"minecraft:end_islands"}`)
	defer func() {
		if recover() == nil {
			t.Fatal("未实现的节点类型应当 panic")
		}
	}()
	evaluator.Evaluate(node, Context{})
}

// TestSplineSinglePoint 校验单点样条：导数与 0 时处处取该点值，
// 导数非零时按原版 linearExtend 做线性外推。
func TestSplineSinglePoint(t *testing.T) {
	evaluator := mustEvaluator(t)
	data, err := Load()
	if err != nil {
		t.Fatalf("加载数据失败：%v", err)
	}
	constant := mustParseNode(t, data,
		`{"type":"minecraft:spline","spline":{"coordinate":"minecraft:y","points":[{"location":0,"value":0.25,"derivative":0}]}}`)
	for _, y := range []int{-1000, -1, 0, 1, 1000} {
		if got := evaluator.Evaluate(constant, Context{Y: y}); got != 0.25 {
			t.Fatalf("y=%d 时取值 = %v, want 0.25", y, got)
		}
	}
	linear := mustParseNode(t, data,
		`{"type":"minecraft:spline","spline":{"coordinate":"minecraft:y","points":[{"location":0,"value":0.25,"derivative":5}]}}`)
	if got := evaluator.Evaluate(linear, Context{Y: 2}); got != 10.25 {
		t.Fatalf("单点外推取值 = %v, want 10.25", got)
	}
}

// TestSplineInterpolation 校验区间内的三次插值与区间外的线性外推。
func TestSplineInterpolation(t *testing.T) {
	evaluator := mustEvaluator(t)
	data, err := Load()
	if err != nil {
		t.Fatalf("加载数据失败：%v", err)
	}
	// 两个控制点，导数都为 0：中间点应恰为两端平均值，整体保持单调。
	node := mustParseNode(t, data,
		`{"type":"minecraft:spline","spline":{"coordinate":"minecraft:y","points":[`+
			`{"location":0,"value":0,"derivative":0},`+
			`{"location":10,"value":1,"derivative":0}]}}`)
	cases := map[int]float64{-10: 0, 0: 0, 5: 0.5, 10: 1, 20: 1}
	for y, want := range cases {
		if got := evaluator.Evaluate(node, Context{Y: y}); got != want {
			t.Fatalf("y=%d 时取值 = %v, want %v", y, got, want)
		}
	}

	// 端点导数非零时区间外做线性外推：value + derivative*(y - location)。
	extrapolate := mustParseNode(t, data,
		`{"type":"minecraft:spline","spline":{"coordinate":"minecraft:y","points":[`+
			`{"location":0,"value":1,"derivative":2},`+
			`{"location":10,"value":3,"derivative":-0.5}]}}`)
	if got := evaluator.Evaluate(extrapolate, Context{Y: -3}); got != -5 {
		t.Fatalf("下界外推 = %v, want -5", got)
	}
	if got := evaluator.Evaluate(extrapolate, Context{Y: 20}); got != -2 {
		t.Fatalf("上界外推 = %v, want -2", got)
	}
}

// TestSplineNested 校验嵌套子样条按同一上下文递归求值。
func TestSplineNested(t *testing.T) {
	evaluator := mustEvaluator(t)
	data, err := Load()
	if err != nil {
		t.Fatalf("加载数据失败：%v", err)
	}
	node := mustParseNode(t, data,
		`{"type":"minecraft:spline","spline":{"coordinate":"minecraft:y","points":[`+
			`{"location":0,"value":{"coordinate":"minecraft:y","points":[`+
			`{"location":0,"value":10,"derivative":0}]},"derivative":0},`+
			`{"location":10,"value":100,"derivative":0}]}}`)
	if got, want := evaluator.Evaluate(node, Context{Y: 5}), 55.0; got != want {
		t.Fatalf("嵌套样条 y=5 取值 = %v, want %v", got, want)
	}
}

// TestOverworldRouterUnsupportedTypes 记录 overworld 路由树里仍未被
// 求值器覆盖的节点类型，作为阶段 3 剩余工作的清单。
func TestOverworldRouterUnsupportedTypes(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatalf("加载数据失败：%v", err)
	}
	setting, err := data.NoiseSetting("minecraft:overworld")
	if err != nil {
		t.Fatalf("读取噪声设置失败：%v", err)
	}
	seen := make(map[string]bool)
	var walk func(*Node)
	walk = func(node *Node) {
		if node == nil {
			return
		}
		seen[node.Type] = true
		for _, child := range node.Children {
			walk(child)
		}
		if node.Spline != nil {
			walk(node.Spline.Coordinate)
			var walkSpline func(*Spline)
			walkSpline = func(spline *Spline) {
				for _, point := range spline.Points {
					if point.Sub != nil {
						walkSpline(point.Sub)
					}
				}
			}
			walkSpline(node.Spline)
		}
	}
	for _, raw := range setting.Router {
		node, problems := parseNode(data, "minecraft:overworld", raw)
		if len(problems) > 0 {
			t.Fatalf("解析路由失败：%s", problems[0].String())
		}
		walk(node)
	}
	if seen["end_islands"] {
		t.Fatal("overworld 路由树不应出现 end_islands")
	}
	// 下列类型都已实现求值，它们都应当能在 overworld 路由树中找到。
	for _, implemented := range []string{"spline", "old_blended_noise", "interpolated", "find_top_surface"} {
		if !seen[implemented] {
			t.Fatalf("overworld 路由树应当出现 %q（数据或解析可能出错）", implemented)
		}
	}
	for _, implemented := range []string{"shift_a", "noise", "weird_scaled_sampler", "y_clamped_gradient", "add"} {
		if !seen[implemented] {
			t.Fatalf("路由树中应当出现节点类型 %q（数据或解析可能出错）", implemented)
		}
	}
}
