//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"crypto/md5"
	"encoding/binary"
)

// 随机源：NextLong 与原版 XoroshiroRandomSource 逐位一致（用 tools/vgoracle 从官方
// jar 取到的 nextLong 序列校验，见 testdata/noise_oracle.json 与 random_test.go）。
// NextDouble / NextInt 由 ImprovedNoise 的 xo/yo/zo 与 256 项置换表、以及
// NormalNoise 的逐值对拍间接校验（same file，见 noise_test.go）。
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

const (
	legacyRandomMask       = (uint64(1) << 48) - 1
	legacyRandomMultiplier = uint64(0x5DEECE66D)
	legacyRandomIncrement  = uint64(0xB)
)

// legacyRandom 是原版 LegacyRandomSource。地物和雕刻器的 WorldgenRandom
// 仍使用这条 48 位 LCG 随机流，而密度噪声使用 Xoroshiro。
type legacyRandom struct {
	seed uint64
}

func newLegacyRandom(seed int64) *legacyRandom {
	random := &legacyRandom{}
	random.setSeed(seed)
	return random
}

func (r *legacyRandom) setSeed(seed int64) {
	r.seed = (uint64(seed) ^ legacyRandomMultiplier) & legacyRandomMask
}

func (r *legacyRandom) next(bits uint) uint32 {
	r.seed = (r.seed*legacyRandomMultiplier + legacyRandomIncrement) & legacyRandomMask
	return uint32(r.seed >> (48 - bits))
}

func (r *legacyRandom) nextInt() int32 {
	return int32(r.next(32))
}

func (r *legacyRandom) nextIntBounded(bound int) int {
	if bound <= 0 {
		panic("vanilla: nextIntBounded 的 bound 必须为正")
	}
	if bound&(bound-1) == 0 {
		return int((int64(bound) * int64(r.next(31))) >> 31)
	}
	for {
		bits := int64(r.next(31))
		value := bits % int64(bound)
		if bits-value+int64(bound-1) <= int64(1<<31-1) {
			return int(value)
		}
	}
}

func (r *legacyRandom) nextLong() int64 {
	return int64(r.nextInt())<<32 + int64(r.nextInt())
}

func (r *legacyRandom) nextFloat() float32 {
	return float32(r.next(24)) / (1 << 24)
}

func (r *legacyRandom) nextDouble() float64 {
	value := uint64(r.next(26))<<27 | uint64(r.next(27))
	return float64(value) / (1 << 53)
}

// worldgenRandom wraps LegacyRandomSource and implements the feature/carver seed
// derivation used by WorldgenRandom.setLargeFeatureSeed.
type worldgenRandom struct {
	*legacyRandom
}

func newWorldgenRandom() *worldgenRandom {
	return &worldgenRandom{legacyRandom: newLegacyRandom(0)}
}

func (r *worldgenRandom) setLargeFeatureSeed(seed int64, chunkX, chunkZ int) int64 {
	r.setSeed(seed)
	xMultiplier := r.nextLong()
	zMultiplier := r.nextLong()
	largeFeatureSeed := int64(chunkX)*xMultiplier ^ int64(chunkZ)*zMultiplier ^ seed
	r.setSeed(largeFeatureSeed)
	return largeFeatureSeed
}

// NewXoroshiro 用与 RandomSource.create(long) 相同的方式构造随机源。
func NewXoroshiro(seed int64) *Xoroshiro {
	lo, hi := seed128(seed)
	return &Xoroshiro{lo: lo, hi: hi}
}

// newXoroshiroState 直接指定 128 位状态（原版 XoroshiroRandomSource(long,long) /
// XoroshiroRandomSource(Seed128bit) 构造器不做额外混合）。
func newXoroshiroState(lo, hi uint64) *Xoroshiro {
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

// NextDouble 返回 [0, 1) 的双精度随机数：取 nextLong 高 53 位再乘 2^-53
// （与原版 XoroshiroRandomSource.nextDouble 一致，已由 ImprovedNoise 的
// xo/yo/zo 逐值对拍验证）。
func (r *Xoroshiro) NextDouble() float64 {
	return float64(uint64(r.NextLong())>>11) * 0x1p-53
}

// NextInt 返回 [0, bound) 的随机整数，与原版 XoroshiroRandomSource.nextInt(int)
// 一致：取 nextLong 的**低 32 位**做 32×32→64 乘法，用乘积的高 32 位作为结果，
// 低位不足时按无符号余数阈值重试（Lemire 有界随机，等价原版的
// Integer.remainderUnsigned）。
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

// NextBoolean 等价原版 RandomSource.nextBoolean()：取 nextLong 的最低位。
func (r *Xoroshiro) NextBoolean() bool {
	return r.NextLong()&1 != 0
}

// NextIntBetween 等价原版 RandomSource.nextInt(int origin, int bound)：
// 半开区间 [origin, bound)，实现为 origin + nextInt(bound - origin)。
func (r *Xoroshiro) NextIntBetween(origin, bound int) int {
	return origin + r.NextInt(bound-origin)
}

// positionalRandomFactory 等价原版
// XoroshiroRandomSource.XoroshiroPositionalRandomFactory。
//
// 世界生成里的每个噪声都不直接从主随机源取数，而是用
//
//	random.forkPositional()                      // 连取两个 nextLong
//	.fromHashOf("minecraft:continentalness")     // 名字的 MD5 前 16 字节再与之异或
//
// 派生出一条独立的随机流，因此噪声实例的创建顺序不影响结果。
type positionalRandomFactory struct {
	lo, hi uint64
}

// ForkPositional 等价原版 RandomSource.forkPositional()：
// 连续取两个 nextLong 作为工厂的 128 位种子。
func (r *Xoroshiro) ForkPositional() positionalRandomFactory {
	return positionalRandomFactory{lo: uint64(r.NextLong()), hi: uint64(r.NextLong())}
}

// FromHashOf 等价原版 PositionalRandomFactory.fromHashOf(String)：
// seed = MD5(name)[0..8) 与 MD5(name)[8..16) （大端）分别与工厂种子异或。
func (f positionalRandomFactory) FromHashOf(name string) *Xoroshiro {
	lo, hi := seedFromHashOf(name)
	return newXoroshiroState(lo^f.lo, hi^f.hi)
}

// seedFromHashOf 等价原版 RandomSupport.seedFromHashOf(String)：
// Hashing.md5() 的摘要前 16 字节按大端拆成两个 int64（Guava Longs.fromBytes）。
func seedFromHashOf(name string) (uint64, uint64) {
	sum := md5.Sum([]byte(name))
	return binary.BigEndian.Uint64(sum[0:8]), binary.BigEndian.Uint64(sum[8:16])
}

// At 等价原版 PositionalRandomFactory.at(int, int, int)：
// 用 Mth.getSeed(x, y, z) 与工厂低位种子异或，高位种子保持不变。
func (f positionalRandomFactory) At(x, y, z int) *Xoroshiro {
	return newXoroshiroState(uint64(mthGetSeed(x, y, z))^f.lo, f.hi)
}

// mthGetSeed 等价原版 Mth.getSeed(int, int, int)：
//
//	l = (x * 3129871) ^ (z * 116129781) ^ y
//	l = l * l * 42317861 + l * 11
//	return l >> 16
//
// 注意乘法在 int 上完成后才扩展为 long（与原版字节码一致）。
func mthGetSeed(x, y, z int) int64 {
	value := int64(int32(x)*3129871) ^ (int64(z) * 116129781) ^ int64(y)
	value = value*value*42317861 + value*11
	return value >> 16
}
