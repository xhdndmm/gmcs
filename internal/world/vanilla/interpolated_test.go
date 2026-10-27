//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"math"
	"sort"
	"testing"
)

// TestOverworldCellSize 校验噪声设置里的 size_horizontal/size_vertical 被换算成
// 原版 NoiseChunk 的单元格尺寸（4×size）。
func TestOverworldCellSize(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatalf("加载数据失败：%v", err)
	}
	setting, err := data.NoiseSetting("minecraft:overworld")
	if err != nil {
		t.Fatalf("读取噪声设置失败：%v", err)
	}
	if setting.CellWidth != 4 || setting.CellHeight != 8 {
		t.Fatalf("单元格 = %d×%d, want 4×8", setting.CellWidth, setting.CellHeight)
	}
}

// TestInterpolatedReproducesLinearFunctions 校验 interpolated 的三线性插值：
// 对 x/y/z 这种线性函数应当逐点还原原值。
func TestInterpolatedReproducesLinearFunctions(t *testing.T) {
	evaluator := mustEvaluator(t)
	data, err := Load()
	if err != nil {
		t.Fatalf("加载数据失败：%v", err)
	}
	for _, coordinate := range []string{"minecraft:x", "minecraft:y", "minecraft:z"} {
		node := mustParseNode(t, data, `{"type":"minecraft:interpolated","argument":"`+coordinate+`"}`)
		for _, ctx := range []Context{
			{X: 0, Y: -64, Z: 0},
			{X: 5, Y: -61, Z: -7},
			{X: 16, Y: 0, Z: 31},
			{X: -3, Y: 63, Z: -129},
		} {
			want := float64(ctx.X)
			switch coordinate {
			case "minecraft:y":
				want = float64(ctx.Y)
			case "minecraft:z":
				want = float64(ctx.Z)
			}
			if got := evaluator.Evaluate(node, ctx); got != want {
				t.Fatalf("%s 在 %v 处插值 = %v, want %v", coordinate, ctx, got, want)
			}
		}
	}
}

// TestInterpolatedConstant 校验常量子节点的插值结果仍是该常量。
func TestInterpolatedConstant(t *testing.T) {
	evaluator := mustEvaluator(t)
	data, err := Load()
	if err != nil {
		t.Fatalf("加载数据失败：%v", err)
	}
	node := mustParseNode(t, data, `{"type":"minecraft:interpolated","argument":0.375}`)
	for _, ctx := range []Context{{}, {X: 7, Y: -33, Z: 91}, {X: -100, Y: 320, Z: -4}} {
		if got := evaluator.Evaluate(node, ctx); got != 0.375 {
			t.Fatalf("%v 处插值 = %v, want 0.375", ctx, got)
		}
	}
}

// TestFindTopSurface 校验顶面搜索：从 upper_bound 对齐到 cell_height 的高度
// 向下按 cell_height 步进，返回第一个密度 > 0 的高度。
func TestFindTopSurface(t *testing.T) {
	evaluator := mustEvaluator(t)
	data, err := Load()
	if err != nil {
		t.Fatalf("加载数据失败：%v", err)
	}
	// 密度在 y < -52 时为正、y >= -48 时为负。
	node := mustParseNode(t, data,
		`{"type":"minecraft:find_top_surface","density":`+
			`{"type":"minecraft:y_clamped_gradient","from_y":-64,"from_value":1,"to_y":-40,"to_value":-1},`+
			`"upper_bound":100,"lower_bound":-64,"cell_height":8}`)
	if got, want := evaluator.Evaluate(node, Context{X: 3, Z: 5}), -56.0; got != want {
		t.Fatalf("顶面高度 = %v, want %v", got, want)
	}

	// 密度恒为正时，直接从对齐后的 upper_bound 返回（floor(100/8)*8 = 96）。
	allSolid := mustParseNode(t, data,
		`{"type":"minecraft:find_top_surface","density":1,"upper_bound":100,"lower_bound":-64,"cell_height":8}`)
	if got, want := evaluator.Evaluate(allSolid, Context{}), 96.0; got != want {
		t.Fatalf("全实心顶面 = %v, want %v", got, want)
	}

	// 密度恒为非正时，一直找不到，返回 lower_bound。
	empty := mustParseNode(t, data,
		`{"type":"minecraft:find_top_surface","density":0,"upper_bound":100,"lower_bound":-64,"cell_height":8}`)
	if got, want := evaluator.Evaluate(empty, Context{}), -64.0; got != want {
		t.Fatalf("全空顶面 = %v, want %v", got, want)
	}

	// upper_bound 低于 lower_bound 时直接返回 lower_bound。
	below := mustParseNode(t, data,
		`{"type":"minecraft:find_top_surface","density":1,"upper_bound":-100,"lower_bound":-64,"cell_height":8}`)
	if got, want := evaluator.Evaluate(below, Context{}), -64.0; got != want {
		t.Fatalf("上界低于下界时 = %v, want %v", got, want)
	}
}

// TestOverworldRouterEvaluatesEndToEnd 对 overworld 路由表的每一项在若干坐标上
// 求值：确认整棵密度函数图（含 spline/interpolated/old_blended_noise/
// find_top_surface）都能求值，且结果有限。
func TestOverworldRouterEvaluatesEndToEnd(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatalf("加载数据失败：%v", err)
	}
	setting, err := data.NoiseSetting("minecraft:overworld")
	if err != nil {
		t.Fatalf("读取噪声设置失败：%v", err)
	}
	registry := NewNoiseRegistry(data, 12345)
	evaluator := NewEvaluatorWithCells(registry, setting.CellWidth, setting.CellHeight)

	keys := make([]string, 0, len(setting.Router))
	for key := range setting.Router {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	points := []Context{
		{X: 0, Y: 0, Z: 0},
		{X: 16, Y: -64, Z: 32},
		{X: -123, Y: 64, Z: -456},
		{X: 1000, Y: 320, Z: -2000},
		{X: -1, Y: -1, Z: -1},
	}
	for _, key := range keys {
		node, problems := parseNode(data, "minecraft:overworld/"+key, setting.Router[key])
		if len(problems) > 0 {
			t.Fatalf("%s 解析失败：%s", key, problems[0].String())
		}
		for _, ctx := range points {
			value := evaluator.Evaluate(node, ctx)
			if math.IsNaN(value) || math.IsInf(value, 0) {
				t.Fatalf("%s 在 %v 处取值为 %v", key, ctx, value)
			}
		}
	}
}

// TestFloorDiv 校验向下取整的整数除法（负坐标也要与 Mth.floorDiv 一致）。
func TestFloorDiv(t *testing.T) {
	cases := []struct{ value, divisor, want int }{
		{0, 4, 0}, {3, 4, 0}, {4, 4, 1}, {-1, 4, -1}, {-4, 4, -1}, {-5, 4, -2},
		{-64, 8, -8}, {-63, 8, -8}, {-57, 8, -8}, {-56, 8, -7},
	}
	for _, testCase := range cases {
		if got := floorDiv(testCase.value, testCase.divisor); got != testCase.want {
			t.Fatalf("floorDiv(%d, %d) = %d, want %d", testCase.value, testCase.divisor, got, testCase.want)
		}
	}
}
