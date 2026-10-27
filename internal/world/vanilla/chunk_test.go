//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import "testing"

// TestChunkSamplerMatchesPointwise 是区块级填充的核心正确性检查：
// 预计算 lattice 后的三线性插值必须与逐点求值**逐位相同**。
func TestChunkSamplerMatchesPointwise(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatalf("加载数据失败：%v", err)
	}
	const seed = 2024
	pointwise, err := NewRouter(data, "minecraft:overworld", seed)
	if err != nil {
		t.Fatalf("构造路由表失败：%v", err)
	}
	sampled, err := NewRouter(data, "minecraft:overworld", seed)
	if err != nil {
		t.Fatalf("构造路由表失败：%v", err)
	}
	const chunkX, chunkZ = 3, -2
	sampler := sampled.NewChunkSampler(chunkX, chunkZ)
	if sampler.InterpolatedNodes() == 0 {
		t.Fatal("区块内应当至少有一个 interpolated 节点")
	}

	baseX := chunkX * 16
	baseZ := chunkZ * 16
	for x := 0; x < 16; x += 3 {
		for z := 0; z < 16; z += 5 {
			for y := -64; y < 320; y += 7 {
				ctx := Context{X: baseX + x, Y: y, Z: baseZ + z}
				want := pointwise.Field("final_density", ctx)
				got := sampler.Density(ctx.X, ctx.Y, ctx.Z)
				if got != want {
					t.Fatalf("(%d,%d,%d) 区块采样 = %.17g, 逐点 = %.17g（差 %g）",
						ctx.X, ctx.Y, ctx.Z, got, want, got-want)
				}
			}
		}
	}
}

// TestChunkSamplerAcrossCells 覆盖单元格边界：x/z 在 4 的倍数、y 在 8 的倍数处
// 也不能出现跳变或回退到逐点路径的差异。
func TestChunkSamplerAcrossCells(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatalf("加载数据失败：%v", err)
	}
	pointwise, err := NewRouter(data, "minecraft:overworld", 11)
	if err != nil {
		t.Fatalf("构造路由表失败：%v", err)
	}
	sampled, err := NewRouter(data, "minecraft:overworld", 11)
	if err != nil {
		t.Fatalf("构造路由表失败：%v", err)
	}
	const chunkX, chunkZ = -5, 7
	sampler := sampled.NewChunkSampler(chunkX, chunkZ)
	baseX := chunkX * 16
	baseZ := chunkZ * 16
	for _, x := range []int{0, 3, 4, 7, 8, 11, 12, 15} {
		for _, z := range []int{0, 4, 8, 12, 15} {
			for _, y := range []int{-64, -56, -1, 0, 7, 8, 63, 64, 319} {
				ctx := Context{X: baseX + x, Y: y, Z: baseZ + z}
				if got, want := sampler.Density(ctx.X, ctx.Y, ctx.Z), pointwise.Field("final_density", ctx); got != want {
					t.Fatalf("(%d,%d,%d) 区块采样 = %.17g, 逐点 = %.17g", ctx.X, ctx.Y, ctx.Z, got, want)
				}
			}
		}
	}
}

// BenchmarkChunkSamplerFillChunk 衡量"预计算 lattice + 填满一个区块的密度"的
// 总开销（16×384×16 = 98304 格）。
func BenchmarkChunkSamplerFillChunk(b *testing.B) {
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
		chunkX := i & 1023
		chunkZ := i >> 10
		sampler := router.NewChunkSampler(chunkX, chunkZ)
		baseX := chunkX * 16
		baseZ := chunkZ * 16
		solid := 0
		for x := 0; x < 16; x++ {
			for z := 0; z < 16; z++ {
				for y := -64; y < 320; y++ {
					if sampler.Density(baseX+x, y, baseZ+z) > 0 {
						solid++
					}
				}
			}
		}
		_ = solid
	}
}
