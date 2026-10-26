//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"math"
	"testing"
)

// TestNoiseRegistryBuildsEveryNoise 确认全部噪声参数都能按种子构造，
// 且取值有限、落在 NormalNoise 的理论上界内。
func TestNoiseRegistryBuildsEveryNoise(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatalf("加载数据失败：%v", err)
	}
	registry := NewNoiseRegistry(data, 12345)
	if len(data.Noises) == 0 {
		t.Fatal("数据包中没有噪声参数")
	}
	for name := range data.Noises {
		noise := registry.Noise(name)
		if noise == nil {
			t.Fatalf("%s 构造失败", name)
		}
		bound := noise.MaxValue()
		for _, point := range oracleSamplePoints {
			value := noise.Value(point[0], point[1], point[2])
			if math.IsNaN(value) || math.IsInf(value, 0) {
				t.Fatalf("%s 在点 %v 的取值非有限：%v", name, point, value)
			}
			if math.Abs(value) > bound {
				t.Fatalf("%s 在点 %v 的取值 %v 超过上界 %v", name, point, value, bound)
			}
		}
	}
}

// TestNoiseRegistryIsOrderIndependent 校验噪声实例与构造顺序无关
// （原版每个噪声的随机流只由资源名与工厂种子决定）。
func TestNoiseRegistryIsOrderIndependent(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatalf("加载数据失败：%v", err)
	}
	first := NewNoiseRegistry(data, 777)
	forward := first.Noise("minecraft:continentalness").Value(1.5, 64, -3.25)

	second := NewNoiseRegistry(data, 777)
	// 先构造别的噪声，再构造 continentalness。
	for _, name := range []string{"minecraft:cave_cheese", "minecraft:erosion", "minecraft:temperature"} {
		if _, ok := data.Noises[name]; ok {
			second.Noise(name)
		}
	}
	backward := second.Noise("minecraft:continentalness").Value(1.5, 64, -3.25)
	if forward != backward {
		t.Fatalf("构造顺序影响了噪声取值：%v vs %v", forward, backward)
	}
}

// BenchmarkNoiseRegistryLookup 衡量缓存命中的开销（世界生成每区块会反复取噪声）。
func BenchmarkNoiseRegistryLookup(b *testing.B) {
	data, err := Load()
	if err != nil {
		b.Fatalf("加载数据失败：%v", err)
	}
	registry := NewNoiseRegistry(data, 0)
	registry.Noise("minecraft:continentalness")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = registry.Noise("minecraft:continentalness")
	}
}
