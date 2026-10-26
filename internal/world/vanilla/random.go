//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

// 随机源：NextLong 与原版 XoroshiroRandomSource 逐位一致（用 tools/vgoracle 从官方
// jar 取到的 nextLong 序列校验，见 testdata/noise_oracle.json 与 random_test.go）。
// NextDouble/NextInt/NextFloat 是标准派生实现，尚未逐值对拍。
//
// 原版 RandomSource.create(long) 构造 XoroshiroRandomSource，内部状态由
// RandomSupport.mixStafford13 生成（SplitMix64 的 mix13 变体）。
// nextLong 使用 xoroshiro128++（rotate-left 17 后相加）。

// mixStafford13 是原版 RandomSupport.mixStafford13（SplitMix64 的 finalizer）。
func mixStafford13(value uint64) uint64 {
	value = (value ^ (value >> 30)) * 0xBF58476D1CE4E5B9
	value = (value ^ (value >> 27)) * 0x94D049BB133111EB
	return value ^ (value >> 31)
}

// seed128 把 64 位种子扩展为 Xoroshiro 的 128 位初始状态。
//
// 与原版 RandomSupport.upgradeSeedTo128bit 一致（按 1.21.11 服务端字节码核对）：
//
//	low0  = seed ^ 0x6A09E667F3BCC909
//	high0 = low0 + 0x9E3779B97F4A7C15
//	lo    = mixStafford13(low0)
//	hi    = mixStafford13(high0)
func seed128(seed int64) (uint64, uint64) {
	low := uint64(seed) ^ 0x6A09E667F3BCC909
	high := low + 0x9E3779B97F4A7C15
	return mixStafford13(low), mixStafford13(high)
}

// Xoroshiro 是原版 Xoroshiro128++ 随机源（XoroshiroRandomSource 的核心）。
// 零值不可用，必须通过 NewXoroshiro 构造。
type Xoroshiro struct {
	lo, hi uint64
}

// NewXoroshiro 用与 RandomSource.create(long) 相同的方式构造随机源。
func NewXoroshiro(seed int64) *Xoroshiro {
	lo, hi := seed128(seed)
	return &Xoroshiro{lo: lo, hi: hi}
}

// NextLong 返回下一个 64 位随机数（xoroshiro128++）。
func (r *Xoroshiro) NextLong() int64 {
	lo, hi := r.lo, r.hi
	result := rotateLeft64(lo+hi, 17) + lo
	hi ^= lo
	r.lo = rotateLeft64(lo, 49) ^ hi ^ (hi << 21)
	r.hi = rotateLeft64(hi, 28)
	return int64(result)
}

// NextDouble 返回 [0, 1) 的双精度随机数（取高 53 位，标准做法；未逐值对拍）。
func (r *Xoroshiro) NextDouble() float64 {
	return float64(uint64(r.NextLong())>>11) * 0x1p-53
}

// NextInt 返回 [0, bound) 的随机整数，与原版 XoroshiroRandomSource.nextInt(int)
// 一致：取 nextLong 的高 32 位做 32×32→64 乘法，用高 32 位作为结果，低位不足时
// 按无符号余数阈值重试（Lemire 有界随机，等价原版的 Integer.remainderUnsigned）。
func (r *Xoroshiro) NextInt(bound int) int {
	if bound <= 0 {
		panic("vanilla: NextInt 的 bound 必须为正")
	}
	limit := uint64(bound)
	for {
		value := uint64(r.NextLong()) & 0xFFFFFFFF
		product := value * limit
		low := product & 0xFFFFFFFF
		if low < limit {
			threshold := (0xFFFFFFFF - limit + 1) % limit
			if low < threshold {
				continue
			}
		}
		return int(product >> 32)
	}
}

// rotateLeft64 是 Long.rotateLeft。
func rotateLeft64(value uint64, bits int) uint64 {
	return value<<bits | value>>(64-bits)
}

// NextFloat 返回 [0, 1) 的单精度随机数。
func (r *Xoroshiro) NextFloat() float32 {
	return float32(r.NextDouble())
}
