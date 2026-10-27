//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

// BlendedNoise 等价原版 net.minecraft.world.level.levelgen.synth.BlendedNoise
// （JSON 里的 old_blended_noise，overworld 的 base_3d_noise 在用）。
//
// 构造（1.21.11 字节码）：
//
//	minLimit = PerlinNoise.createLegacy(random, rangeClosed(-15, 0))  // 16 个振幅 1
//	maxLimit = PerlinNoise.createLegacy(random, rangeClosed(-15, 0))
//	main     = PerlinNoise.createLegacy(random, rangeClosed(-7, 0))   // 8 个振幅 1
//
// 注意：`fkf.a(RandomSource, IntStream)` 传给 PerlinNoise 构造器的 legacy 标志是
// **false**，因此这里走的是"顺序消耗同一个随机源"的非 legacy 路径
// （NewPerlinNoise），而不是按 "octave_n" 派生独立随机流的 legacy 路径。
// 随机源由 RandomState 给出：positional.fromHashOf("minecraft:terrain")。
type BlendedNoise struct {
	minLimit             *PerlinNoise
	maxLimit             *PerlinNoise
	main                 *PerlinNoise
	xzScale              float64 // p
	yScale               float64 // q
	xzScaleFactor        float64 // l
	yScaleFactor         float64 // m
	smearScaleMultiplier float64 // n
	xzMultiplier         float64 // j = 684.412 * p
	yMultiplier          float64 // k = 684.412 * q
	minLimitHeightValue  float64 // o = minLimit.valueFactor(k + 2.0)，仅用于 min/maxValue
}

// NewBlendedNoise 等价原版 BlendedNoise(RandomSource, xzScale, yScale,
// xzFactor, yFactor, smearScaleMultiplier)。
func NewBlendedNoise(random *Xoroshiro, xzScale, yScale, xzFactor, yFactor, smearScaleMultiplier float64) *BlendedNoise {
	minLimit := NewPerlinNoise(random, -15, octaveOnes(16))
	maxLimit := NewPerlinNoise(random, -15, octaveOnes(16))
	main := NewPerlinNoise(random, -7, octaveOnes(8))
	noise := &BlendedNoise{
		minLimit:             minLimit,
		maxLimit:             maxLimit,
		main:                 main,
		xzScale:              xzScale,
		yScale:               yScale,
		xzScaleFactor:        xzFactor,
		yScaleFactor:         yFactor,
		smearScaleMultiplier: smearScaleMultiplier,
	}
	noise.xzMultiplier = 684.412 * noise.xzScale
	noise.yMultiplier = 684.412 * noise.yScale
	noise.minLimitHeightValue = minLimit.valueFactor(noise.yMultiplier + 2)
	return noise
}

// octaveOnes 生成 n 个 1.0 振幅（原版 IntStream.rangeClosed(...).mapToDouble(i -> 1.0)）。
func octaveOnes(n int) []float64 {
	amplitudes := make([]float64, n)
	for i := range amplitudes {
		amplitudes[i] = 1
	}
	return amplitudes
}

// MinLimitHeightValue 返回原版字段 o（minLimit 在 y=k 处的 valueFactor）。
func (b *BlendedNoise) MinLimitHeightValue() float64 {
	return b.minLimitHeightValue
}

// MinValue 返回原版 minValue()（就是 -maxValue()）。
func (b *BlendedNoise) MinValue() float64 {
	return -b.MaxValue()
}

// MaxValue 返回原版 maxValue()：就是构造时算出的字段 o。
func (b *BlendedNoise) MaxValue() float64 {
	return b.minLimitHeightValue
}

// Value 等价原版 BlendedNoise.compute(ctx)：
// 先用 main（8 个八度）算出 0..1 的混合系数，再用 minLimit/maxLimit（各 16 个八度）
// 在两者之间做夹取插值，最后除以 128。
func (b *BlendedNoise) Value(x, y, z int) float64 {
	scaledX := float64(x) * b.xzMultiplier
	scaledY := float64(y) * b.yMultiplier
	scaledZ := float64(z) * b.xzMultiplier
	mainX := scaledX / b.xzScaleFactor
	mainY := scaledY / b.yScaleFactor
	mainZ := scaledZ / b.xzScaleFactor
	smear := b.yMultiplier * b.smearScaleMultiplier
	smearStep := smear / b.yScaleFactor

	mainValue := 0.0
	octaveFactor := 1.0
	for octave := 0; octave < 8; octave++ {
		if noise := b.main.OctaveNoise(octave); noise != nil {
			mainValue += noise.NoiseSmeared(
				wrap(mainX*octaveFactor),
				wrap(mainY*octaveFactor),
				wrap(mainZ*octaveFactor),
				smearStep*octaveFactor,
				mainY*octaveFactor,
			) / octaveFactor
		}
		octaveFactor /= 2
	}
	mix := (mainValue/10 + 1) / 2
	atUpperLimit := mix >= 1
	atLowerLimit := mix <= 0

	minValue := 0.0
	maxValue := 0.0
	octaveFactor = 1
	for octave := 0; octave < 16; octave++ {
		sampleX := wrap(scaledX * octaveFactor)
		sampleY := wrap(scaledY * octaveFactor)
		sampleZ := wrap(scaledZ * octaveFactor)
		sampleSmear := smear * octaveFactor
		sampleYMax := scaledY * octaveFactor
		if !atUpperLimit {
			if noise := b.minLimit.OctaveNoise(octave); noise != nil {
				minValue += noise.NoiseSmeared(sampleX, sampleY, sampleZ, sampleSmear, sampleYMax) / octaveFactor
			}
		}
		if !atLowerLimit {
			if noise := b.maxLimit.OctaveNoise(octave); noise != nil {
				maxValue += noise.NoiseSmeared(sampleX, sampleY, sampleZ, sampleSmear, sampleYMax) / octaveFactor
			}
		}
		octaveFactor /= 2
	}
	return clampedLerp(mix, minValue/512, maxValue/512) / 128
}
