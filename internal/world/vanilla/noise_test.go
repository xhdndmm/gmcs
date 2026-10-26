//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"math"
	"testing"
)

// oracleSamplePoints 是 tools/vgoracle 导出真值时使用的采样坐标
// （顺序与 testdata/noise_oracle.json 中的 values 一一对应）。
var oracleSamplePoints = [][3]float64{
	{0, 0, 0},
	{0.5, 0, 0},
	{12.34, 56.78, -90.12},
	{1000.5, 64, -2000.25},
	{-0.5, 0.25, 0.75},
}

// TestImprovedNoiseStateMatchesVanilla 校验 xo/yo/zo 与 256 项置换表
// 与原版逐项一致（覆盖 NextDouble 与 Fisher–Yates 洗牌）。
func TestImprovedNoiseStateMatchesVanilla(t *testing.T) {
	oracle := loadOracle(t)
	if len(oracle.ImprovedNoiseState) == 0 {
		t.Fatal("对照数据缺少 improvedNoiseState")
	}
	for _, group := range oracle.ImprovedNoiseState {
		noise := NewImprovedNoise(NewXoroshiro(group.Seed))
		if noise.xo != group.XO || noise.yo != group.YO || noise.zo != group.ZO {
			t.Fatalf("seed=%d 偏移 = (%v,%v,%v), want (%v,%v,%v)",
				group.Seed, noise.xo, noise.yo, noise.zo, group.XO, group.YO, group.ZO)
		}
		if len(group.P) != 256 {
			t.Fatalf("seed=%d 置换表长度 = %d", group.Seed, len(group.P))
		}
		for i, want := range group.P {
			if noise.p[i] != want {
				t.Fatalf("seed=%d 置换表第 %d 项 = %d, want %d", group.Seed, i, noise.p[i], want)
			}
		}
	}
}

// TestImprovedNoiseMatchesVanilla 逐值比对 8 角插值的轴顺序，确定原版使用哪一种
// 并把结果固定下来（当前实现使用 blendXYZ）。
func TestImprovedNoiseMatchesVanilla(t *testing.T) {
	oracle := loadOracle(t)
	matches := make([]blendOrder, 0, len(oracle.ImprovedNoise))
	orders := []blendOrder{blendXYZ, blendXZY, blendYXZ, blendYZX, blendZXY, blendZYX}
	for _, order := range orders {
		ok := true
		for _, group := range oracle.ImprovedNoise {
			noise := NewImprovedNoise(NewXoroshiro(group.Seed))
			for i, want := range group.Values {
				point := oracleSamplePoints[i]
				d := point[0] + noise.xo
				e := point[1] + noise.yo
				f := point[2] + noise.zo
				i0 := int(math.Floor(d))
				j0 := int(math.Floor(e))
				k0 := int(math.Floor(f))
				got := noise.sampleAndLerp(i0, j0, k0, d-float64(i0), e-float64(j0), f-float64(k0), order)
				if diff := got - want; diff > 1e-12 || diff < -1e-12 {
					ok = false
					break
				}
			}
			if !ok {
				break
			}
		}
		if ok {
			matches = append(matches, order)
		}
	}
	if len(matches) == 0 {
		noise := NewImprovedNoise(NewXoroshiro(0))
		t.Fatalf("没有任何轴顺序能与官方取值一致；seed=0 (0,0,0) 实测 = %.17g, want %.17g",
			noise.Noise(0, 0, 0), oracle.ImprovedNoise[0].Values[0])
	}
	if matches[0] != blendXYZ {
		t.Fatalf("原版轴顺序 = %v，实现使用的 blendXYZ 需要改为它", matches[0])
	}
}
