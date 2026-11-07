//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package world

import "math"

func valueNoise(seed int64, x, z, period int) float64 {
	fx := float64(x) / float64(period)
	fz := float64(z) / float64(period)
	x0 := int(math.Floor(fx))
	z0 := int(math.Floor(fz))
	fracX := smoothstep(fx - float64(x0))
	fracZ := smoothstep(fz - float64(z0))
	v00 := hashUnit(seed, x0, z0)
	v10 := hashUnit(seed, x0+1, z0)
	v01 := hashUnit(seed, x0, z0+1)
	v11 := hashUnit(seed, x0+1, z0+1)
	top := v00 + (v10-v00)*fracX
	bottom := v01 + (v11-v01)*fracX
	return top + (bottom-top)*fracZ
}

// valueNoise3D 是周期为 period 的三维值噪声（三线性插值 + smoothstep）。
// 洞穴采样用粗网格调用它，不用于逐方块热路径。
func valueNoise3D(seed int64, x, y, z, period int) float64 {
	fx := float64(x) / float64(period)
	fy := float64(y) / float64(period)
	fz := float64(z) / float64(period)
	x0, y0, z0 := int(math.Floor(fx)), int(math.Floor(fy)), int(math.Floor(fz))
	tx := smoothstep(fx - float64(x0))
	ty := smoothstep(fy - float64(y0))
	tz := smoothstep(fz - float64(z0))

	v000 := hashUnit3(seed, x0, y0, z0)
	v100 := hashUnit3(seed, x0+1, y0, z0)
	v010 := hashUnit3(seed, x0, y0+1, z0)
	v110 := hashUnit3(seed, x0+1, y0+1, z0)
	v001 := hashUnit3(seed, x0, y0, z0+1)
	v101 := hashUnit3(seed, x0+1, y0, z0+1)
	v011 := hashUnit3(seed, x0, y0+1, z0+1)
	v111 := hashUnit3(seed, x0+1, y0+1, z0+1)

	bottom := lerp(lerp(v000, v100, tx), lerp(v010, v110, tx), ty)
	top := lerp(lerp(v001, v101, tx), lerp(v011, v111, tx), ty)
	return lerp(bottom, top, tz)
}

func smoothstep(t float64) float64 {
	return t * t * (3 - 2*t)
}

func lerp(a, b, t float64) float64 {
	return a + (b-a)*t
}

// clamp01 把值限制到 [0, 1]。
func clamp01(t float64) float64 {
	if t < 0 {
		return 0
	}
	if t > 1 {
		return 1
	}
	return t
}

// hashUnit 把 (seed, x, z) 混合为 [0, 1) 的确定性伪随机数（splitmix64 变体）。
func hashUnit(seed int64, x, z int) float64 {
	h := uint64(seed)*0x9E3779B97F4A7C15 +
		uint64(int64(x))*0xBF58476D1CE4E5B9 +
		uint64(int64(z))*0x94D049BB133111EB
	h ^= h >> 29
	h *= 0xBF58476D1CE4E5B9
	h ^= h >> 32
	return float64(h>>11) / float64(uint64(1)<<53)
}

// hashUnit3 把 (seed, x, y, z) 混合为 [0, 1) 的确定性伪随机数。
func hashUnit3(seed int64, x, y, z int) float64 {
	h := uint64(seed)*0x9E3779B97F4A7C15 +
		uint64(int64(x))*0xBF58476D1CE4E5B9 +
		uint64(int64(y))*0x94D049BB133111EB +
		uint64(int64(z))*0xD6E8FEB86659FD93
	h ^= h >> 29
	h *= 0xBF58476D1CE4E5B9
	h ^= h >> 32
	return float64(h>>11) / float64(uint64(1)<<53)
}

// hashInt 把 (seed, a, b) 混合为 64 位确定性伪随机整数。
func hashInt(seed int64, a, b int) uint64 {
	h := uint64(seed)*0x9E3779B97F4A7C15 +
		uint64(int64(a))*0xBF58476D1CE4E5B9 +
		uint64(int64(b))*0x94D049BB133111EB
	h ^= h >> 30
	h *= 0xBF58476D1CE4E5B9
	h ^= h >> 27
	h *= 0x94D049BB133111EB
	return h ^ (h >> 31)
}

// hashSeq 是基于 splitmix64 的确定性随机数序列（洞穴/矿物生成用）。
type hashSeq struct {
	state uint64
}

// newHashSeq 用 (seed, a, b) 构造随机序列。
func newHashSeq(seed int64, a, b int) *hashSeq {
	state := hashInt(seed, a, b)
	if state == 0 {
		state = 0x9E3779B97F4A7C15
	}
	return &hashSeq{state: state}
}

// next 返回下一个伪随机数。
func (s *hashSeq) next() uint64 {
	s.state += 0x9E3779B97F4A7C15
	z := s.state
	z ^= z >> 30
	z *= 0xBF58476D1CE4E5B9
	z ^= z >> 27
	z *= 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// nextInt 返回 [0, n) 的伪随机整数（n <= 0 时返回 0）。
func (s *hashSeq) nextInt(n int) int {
	if n <= 0 {
		return 0
	}
	return int(s.next() % uint64(n))
}

// nextFloat 返回 [0, 1) 的伪随机浮点数。
func (s *hashSeq) nextFloat() float64 {
	return float64(s.next()>>11) / float64(uint64(1)<<53)
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
