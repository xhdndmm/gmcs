//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"math"
	"testing"
)

// TestOldBlendedNoiseParameters 校验 overworld/base_3d_noise 的五个参数
// 被正确解析到节点字段上（xz_scale/y_scale 走 XZScale/YScale，
// 其余三项走 Legacy[2..4]）。
func TestOldBlendedNoiseParameters(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatalf("加载数据失败：%v", err)
	}
	raw, ok := data.Function("minecraft:overworld/base_3d_noise")
	if !ok {
		t.Fatal("缺少 minecraft:overworld/base_3d_noise")
	}
	node, problems := parseNode(data, "test", raw)
	if len(problems) > 0 {
		t.Fatalf("解析失败：%s", problems[0].String())
	}
	if node.Type != "old_blended_noise" {
		t.Fatalf("节点类型 = %q, want old_blended_noise", node.Type)
	}
	if node.XZScale != 0.25 || node.YScale != 0.125 {
		t.Fatalf("xz_scale/y_scale = %v/%v, want 0.25/0.125", node.XZScale, node.YScale)
	}
	want := [3]float64{80, 160, 8}
	for i, value := range want {
		if node.Legacy[2+i] != value {
			t.Fatalf("Legacy[%d] = %v, want %v", 2+i, node.Legacy[2+i], value)
		}
	}
}

// TestNoiseSmearedWithZeroScale 校验 yScale 为 0 时抹平版退化为普通采样。
func TestNoiseSmearedWithZeroScale(t *testing.T) {
	noise := NewImprovedNoise(NewXoroshiro(3))
	points := [][3]float64{
		{0, 0, 0}, {12.5, -64, 3.25}, {-0.75, 320, 1e3},
	}
	for _, point := range points {
		want := noise.Noise(point[0], point[1], point[2])
		if got := noise.NoiseSmeared(point[0], point[1], point[2], 0, 0); got != want {
			t.Fatalf("%v 处抹平采样 = %.17g, want %.17g", point, got, want)
		}
	}
}

// TestBlendedNoiseDeterministic 校验同种子同参数构造出的实例逐点一致，
// 且不同种子会得到不同结果（随机源来自 fromHashOf("minecraft:terrain")）。
func TestBlendedNoiseDeterministic(t *testing.T) {
	build := func(seed int64) *BlendedNoise {
		data, err := Load()
		if err != nil {
			t.Fatalf("加载数据失败：%v", err)
		}
		registry := NewNoiseRegistry(data, seed)
		return NewBlendedNoise(registry.FromHashOf("minecraft:terrain"), 0.25, 0.125, 80, 160, 8)
	}
	first := build(0)
	second := build(0)
	other := build(1)

	different := 0
	for _, point := range [][3]int{{0, 0, 0}, {16, 64, 32}, {-123, -64, 456}, {1600, 320, -1600}} {
		a := first.Value(point[0], point[1], point[2])
		if b := second.Value(point[0], point[1], point[2]); a != b {
			t.Fatalf("%v 处两次构造结果不同：%v vs %v", point, a, b)
		}
		if math.IsNaN(a) || math.IsInf(a, 0) {
			t.Fatalf("%v 处取值非有限：%v", point, a)
		}
		if math.Abs(a) > first.MaxValue() {
			t.Fatalf("%v 处取值 %v 超过上界 %v", point, a, first.MaxValue())
		}
		if c := other.Value(point[0], point[1], point[2]); c != a {
			different++
		}
	}
	if different == 0 {
		t.Fatal("不同种子得到了完全相同的噪声，随机源派生可能没生效")
	}
}

// BenchmarkBlendedNoiseValue 衡量旧版混合噪声的单点采样开销（24 个八度）。
func BenchmarkBlendedNoiseValue(b *testing.B) {
	data, err := Load()
	if err != nil {
		b.Fatalf("加载数据失败：%v", err)
	}
	registry := NewNoiseRegistry(data, 0)
	noise := NewBlendedNoise(registry.FromHashOf("minecraft:terrain"), 0.25, 0.125, 80, 160, 8)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = noise.Value(i&1023, 64, i>>10)
	}
}
