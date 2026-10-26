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

// oracleNormalNoiseParams 是对拍数据里出现的 NormalNoise 参数
// （与 tools/vgoracle/Oracle.java 中的 cases 表一致）。
var oracleNormalNoiseParams = map[string]struct {
	firstOctave int
	amplitudes  []float64
}{
	"continentalness": {-9, []float64{1, 1, 2, 2, 2, 1, 1, 1, 1}},
	"cave_cheese":     {-8, []float64{0.5, 1, 2, 1, 2, 1, 0, 0, 0}},
}

// TestNormalNoiseMatchesVanilla 逐值比对 PerlinNoise 八度叠加与 NormalNoise
// 双 Perlin 归一化，覆盖零振幅八度的随机数跳过与两份噪声的相位偏移。
func TestNormalNoiseMatchesVanilla(t *testing.T) {
	oracle := loadOracle(t)
	if len(oracle.NormalNoise) == 0 {
		t.Fatal("对照数据缺少 normalNoise")
	}
	for _, group := range oracle.NormalNoise {
		params, ok := oracleNormalNoiseParams[group.Noise]
		if !ok {
			t.Fatalf("对拍数据中出现未登记的噪声 %q", group.Noise)
		}
		noise := NewNormalNoise(NewXoroshiro(group.Seed), params.firstOctave, params.amplitudes)
		if maxValue := noise.MaxValue(); maxValue <= 0 || math.IsInf(maxValue, 0) || math.IsNaN(maxValue) {
			t.Fatalf("%s seed=%d 的 maxValue = %v，应为有限正数", group.Noise, group.Seed, maxValue)
		}
		if len(group.Values) != len(oracleSamplePoints) {
			t.Fatalf("%s seed=%d 的采样点数 = %d, want %d", group.Noise, group.Seed, len(group.Values), len(oracleSamplePoints))
		}
		for i, want := range group.Values {
			point := oracleSamplePoints[i]
			got := noise.Value(point[0], point[1], point[2])
			if diff := got - want; diff > 1e-12 || diff < -1e-12 {
				t.Fatalf("%s seed=%d 点 %v 的取值 = %.17g, want %.17g（差 %g）",
					group.Noise, group.Seed, point, got, want, diff)
			}
		}
	}
}

// TestNormalNoiseLegacyMatchesVanilla 校验世界生成实际使用的 legacy 噪声
// （forkPositional + fromHashOf("octave_n") 派生每个八度随机源）。
func TestNormalNoiseLegacyMatchesVanilla(t *testing.T) {
	oracle := loadOracle(t)
	if len(oracle.NormalNoiseLegacy) == 0 {
		t.Fatal("对照数据缺少 normalNoiseLegacy")
	}
	for _, group := range oracle.NormalNoiseLegacy {
		params, ok := oracleNormalNoiseParams[group.Noise]
		if !ok {
			t.Fatalf("对拍数据中出现未登记的噪声 %q", group.Noise)
		}
		noise := NewNormalNoiseLegacy(NewXoroshiro(group.Seed), params.firstOctave, params.amplitudes)
		if len(group.Values) != len(oracleSamplePoints) {
			t.Fatalf("%s seed=%d 的采样点数 = %d, want %d", group.Noise, group.Seed, len(group.Values), len(oracleSamplePoints))
		}
		for i, want := range group.Values {
			point := oracleSamplePoints[i]
			got := noise.Value(point[0], point[1], point[2])
			if diff := got - want; diff > 1e-12 || diff < -1e-12 {
				t.Fatalf("%s seed=%d 点 %v 的取值 = %.17g, want %.17g（差 %g）",
					group.Noise, group.Seed, point, got, want, diff)
			}
		}
	}
}

// TestNormalNoiseLegacyStateMatchesVanilla 逐八度校验派生链：
// forkPositional() 的两个 nextLong 与 MD5("octave_<n>") 的异或结果必须与原版一致。
func TestNormalNoiseLegacyStateMatchesVanilla(t *testing.T) {
	oracle := loadOracle(t)
	if len(oracle.NormalNoiseLegacyState) == 0 {
		t.Fatal("对照数据缺少 normalNoiseLegacyState")
	}
	for _, group := range oracle.NormalNoiseLegacyState {
		params, ok := oracleNormalNoiseParams[group.Noise]
		if !ok {
			t.Fatalf("对拍数据中出现未登记的噪声 %q", group.Noise)
		}
		perlin := NewPerlinNoiseLegacy(NewXoroshiro(group.Seed), params.firstOctave, params.amplitudes)
		expected := make(map[int]struct{ xo, yo, zo float64 }, len(group.Octaves))
		for _, octave := range group.Octaves {
			expected[octave.Octave] = struct{ xo, yo, zo float64 }{octave.XO, octave.YO, octave.ZO}
		}
		if len(expected) != len(group.Octaves) {
			t.Fatalf("%s 的八度列表有重复项", group.Noise)
		}
		found := 0
		for i, level := range perlin.levels {
			if level == nil {
				continue
			}
			octave := params.firstOctave + i
			want, ok := expected[octave]
			if !ok {
				t.Fatalf("%s 多出了原版不存在的八度 %d", group.Noise, octave)
			}
			found++
			if level.xo != want.xo || level.yo != want.yo || level.zo != want.zo {
				t.Fatalf("%s 八度 %d 的偏移 = (%v,%v,%v), want (%v,%v,%v)",
					group.Noise, octave, level.xo, level.yo, level.zo, want.xo, want.yo, want.zo)
			}
		}
		if found != len(expected) {
			t.Fatalf("%s 只生成了 %d 个八度，原版有 %d 个", group.Noise, found, len(expected))
		}
	}
}

// TestNormalNoisePerlinValueConsistency 校验 PerlinNoise.Value 与手工按八度
// 叠加的结果一致，避免八度下标或振幅对应关系写错。
func TestNormalNoisePerlinValueConsistency(t *testing.T) {
	amplitudes := []float64{0.5, 1, 2, 1, 2, 1, 0, 0, 0}
	perlin := NewPerlinNoise(NewXoroshiro(7), -8, amplitudes)
	inputFactor := perlin.lowestFreqInputFactor
	valueFactor := perlin.lowestFreqValueFactor
	want := 0.0
	for i, level := range perlin.levels {
		if level != nil {
			want += amplitudes[i] * level.Noise(
				wrap(3.5*inputFactor),
				wrap(64*inputFactor),
				wrap(-12.25*inputFactor),
			) * valueFactor
		}
		inputFactor *= 2
		valueFactor /= 2
	}
	if got := perlin.Value(3.5, 64, -12.25); got != want {
		t.Fatalf("PerlinNoise.Value = %.17g, 手工叠加 = %.17g", got, want)
	}
}

// TestNormalNoiseDetailMatchesVanilla 比对 NormalNoise 的内部组成：
// valueFactor、maxValue，以及两份 PerlinNoise 各自的取值。
// 这能区分“归一化因子错”和“随机源推进错”两类问题。
func TestNormalNoiseDetailMatchesVanilla(t *testing.T) {
	oracle := loadOracle(t)
	if len(oracle.NormalNoiseDetail) == 0 {
		t.Fatal("对照数据缺少 normalNoiseDetail")
	}
	const tolerance = 1e-12
	for _, group := range oracle.NormalNoiseDetail {
		params, ok := oracleNormalNoiseParams[group.Noise]
		if !ok {
			t.Fatalf("对拍数据中出现未登记的噪声 %q", group.Noise)
		}
		noise := NewNormalNoise(NewXoroshiro(group.Seed), params.firstOctave, params.amplitudes)
		if diff := noise.valueFactor - group.ValueFactor; diff > tolerance || diff < -tolerance {
			t.Fatalf("%s seed=%d 的 valueFactor = %.17g, want %.17g", group.Noise, group.Seed, noise.valueFactor, group.ValueFactor)
		}
		if diff := noise.MaxValue() - group.MaxValue; diff > tolerance || diff < -tolerance {
			t.Fatalf("%s seed=%d 的 maxValue = %.17g, want %.17g", group.Noise, group.Seed, noise.MaxValue(), group.MaxValue)
		}
		for i := range group.First {
			point := oracleSamplePoints[i]
			if got, want := noise.first.Value(point[0], point[1], point[2]), group.First[i]; math.Abs(got-want) > tolerance {
				t.Fatalf("%s seed=%d 第一份 Perlin 在点 %v = %.17g, want %.17g", group.Noise, group.Seed, point, got, want)
			}
			if got, want := noise.second.Value(point[0], point[1], point[2]), group.Second[i]; math.Abs(got-want) > tolerance {
				t.Fatalf("%s seed=%d 第二份 Perlin 在点 %v = %.17g, want %.17g", group.Noise, group.Seed, point, got, want)
			}
		}
	}
}

// BenchmarkNormalNoiseValue 衡量单次噪声采样开销（世界生成每区块要采集成千上万次）。
func BenchmarkNormalNoiseValue(b *testing.B) {
	noise := NewNormalNoiseLegacy(NewXoroshiro(0), -9, []float64{1, 1, 2, 2, 2, 1, 1, 1, 1})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = noise.Value(float64(i&1023), 64, float64(i>>10))
	}
}

// BenchmarkImprovedNoise 衡量单个 perlin 八度的采样开销。
func BenchmarkImprovedNoise(b *testing.B) {
	noise := NewImprovedNoise(NewXoroshiro(0))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = noise.Noise(float64(i&1023)*0.01, 0.5, float64(i>>10)*0.01)
	}
}
