//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import "fmt"

// NoiseRegistry 按种子构造与原版一致的 NormalNoise 实例。
//
// 原版 RandomState 的构造顺序（按 1.21.11 字节码）：
//
//	positional = settings.randomAlgorithm().create(seed).forkPositional()
//
// 之后每个噪声在首次使用时用 `positional.fromHashOf(<资源名>)` 派生独立随机流，
// 再 `NormalNoise.createLegacy` 构造。由于每个噪声的随机源只取决于资源名与
// 工厂种子，**构造顺序不影响结果**，所以这里可以安全地惰性构造并缓存。
type NoiseRegistry struct {
	data    *Data
	seed    int64
	factory positionalRandomFactory
	cache   map[string]*NormalNoise
}

// NewNoiseRegistry 为给定数据包与种子创建噪声注册表。
func NewNoiseRegistry(data *Data, seed int64) *NoiseRegistry {
	return &NoiseRegistry{
		data:    data,
		seed:    seed,
		factory: NewXoroshiro(seed).ForkPositional(),
		cache:   make(map[string]*NormalNoise),
	}
}

// Seed 返回注册表对应的世界种子（用于诊断）。
func (r *NoiseRegistry) Seed() int64 {
	return r.seed
}

// Noise 返回指定噪声参数对应的 NormalNoise。
// name 可以带或不带 minecraft: 前缀；未定义的噪声参数会 panic（数据包与实现
// 不匹配是编程错误，不应静默产出 0 噪声）。
func (r *NoiseRegistry) Noise(name string) *NormalNoise {
	key := qualify(name)
	if noise, ok := r.cache[key]; ok {
		return noise
	}
	params, ok := r.data.Noises[key]
	if !ok {
		panic(fmt.Sprintf("vanilla: 缺少噪声参数 %s", key))
	}
	noise := NewNormalNoiseLegacy(r.factory.FromHashOf(key), params.FirstOctave, params.Amplitudes)
	r.cache[key] = noise
	return noise
}
