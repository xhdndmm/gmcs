//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"math"
	"testing"
)

// mustRouter 构造 overworld 的路由表。
func mustRouter(t *testing.T, seed int64) *Router {
	t.Helper()
	data, err := Load()
	if err != nil {
		t.Fatalf("加载数据失败：%v", err)
	}
	router, err := NewRouter(data, "minecraft:overworld", seed)
	if err != nil {
		t.Fatalf("构造路由表失败：%v", err)
	}
	return router
}

// TestRouterFieldsEvaluable 校验路由表每一项都能求值且结果有限。
func TestRouterFieldsEvaluable(t *testing.T) {
	router := mustRouter(t, 0)
	points := []Context{
		{X: 0, Y: 0, Z: 0},
		{X: 8, Y: -64, Z: -8},
		{X: -500, Y: 200, Z: 900},
	}
	for _, key := range router.keys {
		for _, ctx := range points {
			value := router.Field(key, ctx)
			if math.IsNaN(value) || math.IsInf(value, 0) {
				t.Fatalf("路由项 %s 在 %v 处取值为 %v", key, ctx, value)
			}
		}
	}
}

// TestRouterSeedAffectsFinalDensity 校验不同种子得到不同的最终密度（世界种子确实
// 参与了噪声派生）。
func TestRouterSeedAffectsFinalDensity(t *testing.T) {
	first := mustRouter(t, 0)
	second := mustRouter(t, 1)
	different := 0
	for x := -32; x <= 32; x += 16 {
		for z := -32; z <= 32; z += 16 {
			for y := -64; y <= 320; y += 64 {
				a := first.FinalDensity(Context{X: x, Y: y, Z: z})
				b := second.FinalDensity(Context{X: x, Y: y, Z: z})
				if a != b {
					different++
				}
			}
		}
	}
	if different == 0 {
		t.Fatal("不同种子的 final_density 完全相同，种子没有参与派生")
	}
}

// TestRouterSolidMatchesDensitySign 校验 Solid 就是 final_density > 0。
func TestRouterSolidMatchesDensitySign(t *testing.T) {
	router := mustRouter(t, 42)
	for y := -64; y <= 320; y += 8 {
		for _, xz := range [][2]int{{0, 0}, {7, -13}, {100, 200}} {
			ctx := Context{X: xz[0], Y: y, Z: xz[1]}
			want := router.FinalDensity(ctx) > 0
			if got := router.Solid(ctx.X, ctx.Y, ctx.Z); got != want {
				t.Fatalf("(%d,%d,%d) Solid=%v, 但 final_density=%v", ctx.X, ctx.Y, ctx.Z, got, router.FinalDensity(ctx))
			}
		}
	}
}

// TestRouterHasSurface 校验典型地形里地表附近确实存在"上实下空"的过渡，
// 这能证明 spline/offset/factor/jaggedness 这条链路在工作（而不是恒定的密度）。
func TestRouterHasSurface(t *testing.T) {
	router := mustRouter(t, 12345)
	for _, column := range [][2]int{{0, 0}, {16, 32}, {-256, 512}, {1024, -1024}} {
		solidCount := 0
		for y := -64; y < 320; y++ {
			if router.Solid(column[0], y, column[1]) {
				solidCount++
			}
		}
		if solidCount == 0 {
			t.Fatalf("列 %v 完全没有实心方块", column)
		}
		if solidCount == 384 {
			t.Fatalf("列 %v 整列都是实心方块", column)
		}
	}
}

// TestInterpolationCacheReducesWork 校验角点缓存生效：同一单元格内的多个方块
// 只会产生有限个 lattice 角点（每个 interpolated 节点最多 8 个）。
func TestInterpolationCacheReducesWork(t *testing.T) {
	router := mustRouter(t, 7)
	evaluator := router.Evaluator()
	interpolated := make(map[*Node]bool)
	var walk func(*Node)
	walk = func(node *Node) {
		if node == nil {
			return
		}
		if node.Type == "interpolated" {
			interpolated[node] = true
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	for _, key := range router.keys {
		walk(router.nodes[key])
	}
	if len(interpolated) == 0 {
		t.Fatal("路由表里应当存在 interpolated 节点")
	}

	// 覆盖一个 4×8×4 单元格内的全部方块。
	for x := 0; x < 4; x++ {
		for y := 0; y < 8; y++ {
			for z := 0; z < 4; z++ {
				router.Solid(x, y, z)
			}
		}
	}
	if limit := len(interpolated) * 8; len(evaluator.corners) > limit {
		t.Fatalf("角点缓存条目 = %d，超过单个单元格的理论上限 %d（%d 个 interpolated 节点 × 8 角点）",
			len(evaluator.corners), limit, len(interpolated))
	}
}

// TestResetInterpolationCacheKeepsResults 校验清空缓存不改变结果。
func TestResetInterpolationCacheKeepsResults(t *testing.T) {
	router := mustRouter(t, 9)
	ctxs := []Context{{X: 1, Y: -60, Z: 2}, {X: 17, Y: 70, Z: -33}}
	before := make([]float64, len(ctxs))
	for i, ctx := range ctxs {
		before[i] = router.FinalDensity(ctx)
	}
	router.Evaluator().ResetInterpolationCache()
	for i, ctx := range ctxs {
		if got := router.FinalDensity(ctx); got != before[i] {
			t.Fatalf("%v 处清缓存后取值变了：%v → %v", ctx, before[i], got)
		}
	}
}

// BenchmarkRouterFillChunk 衡量填满一个区块（16×384×16 = 98304 格）的实心判定
// 开销，含每区块清空角点缓存的成本。这是区块生成的粒度参考。
func BenchmarkRouterFillChunk(b *testing.B) {
	data, err := Load()
	if err != nil {
		b.Fatalf("加载数据失败：%v", err)
	}
	router, err := NewRouter(data, "minecraft:overworld", 0)
	if err != nil {
		b.Fatalf("构造路由表失败：%v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		router.Evaluator().ResetInterpolationCache()
		solid := 0
		for x := 0; x < 16; x++ {
			for z := 0; z < 16; z++ {
				for y := -64; y < 320; y++ {
					if router.Solid(x, y, z) {
						solid++
					}
				}
			}
		}
		_ = solid
	}
}

// BenchmarkRouterSolidColumn 衡量单列（384 格、冷缓存）的开销。
func BenchmarkRouterSolidColumn(b *testing.B) {
	data, err := Load()
	if err != nil {
		b.Fatalf("加载数据失败：%v", err)
	}
	router, err := NewRouter(data, "minecraft:overworld", 0)
	if err != nil {
		b.Fatalf("构造路由表失败：%v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		router.Evaluator().ResetInterpolationCache()
		solid := 0
		for y := -64; y < 320; y++ {
			if router.Solid(3, y, 5) {
				solid++
			}
		}
		_ = solid
	}
}
