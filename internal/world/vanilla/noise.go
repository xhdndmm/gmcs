//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"math"
	"strconv"
)

// ImprovedNoise 是原版 perlin 噪声（net.minecraft.world.level.levelgen.synth.ImprovedNoise）。
// 按 1.21.11 服务端字节码对齐：
//   - xo/yo/zo = nextDouble()*256；
//   - p[256] 先填 0..255，再对 i∈[0,256) 取 j=nextInt(256-i) 交换 p[i]/p[i+j]；
//   - noise(x,y,z) 等价 noise(x,y,z,0,0)（y 夹取参数为 0 时无 y 抹平）；
//   - 梯度为 SimplexNoise.GRADIENT 的 16 项整型表，点积无归一化。
type ImprovedNoise struct {
	xo, yo, zo float64
	p          [256]int
}

// NewImprovedNoise 用给定随机源构造噪声（等价原版 new ImprovedNoise(RandomSource)）。
func NewImprovedNoise(random *Xoroshiro) *ImprovedNoise {
	noise := &ImprovedNoise{
		xo: random.NextDouble() * 256,
		yo: random.NextDouble() * 256,
		zo: random.NextDouble() * 256,
	}
	for i := 0; i < 256; i++ {
		noise.p[i] = i
	}
	for i := 0; i < 256; i++ {
		j := random.NextInt(256 - i)
		noise.p[i], noise.p[i+j] = noise.p[i+j], noise.p[i]
	}
	return noise
}

// Noise 返回 (x, y, z) 处的 perlin 噪声（等价原版 ImprovedNoise.noise(x,y,z)）。
func (n *ImprovedNoise) Noise(x, y, z float64) float64 {
	d := x + n.xo
	e := y + n.yo
	f := z + n.zo
	i := int(math.Floor(d))
	j := int(math.Floor(e))
	k := int(math.Floor(f))
	return n.sampleAndLerp(i, j, k, d-float64(i), e-float64(j), f-float64(k), blendXYZ)
}

// perm 返回置换表第 index & 255 项（0–255）。
func (n *ImprovedNoise) perm(index int) int {
	return n.p[index&255]
}

// blendOrder 决定三轴插值的嵌套顺序；由对拍测试确定原版使用哪一种。
type blendOrder uint8

const (
	blendXYZ blendOrder = iota // 先 x，再 y，最后 z
	blendXZY
	blendYXZ
	blendYZX
	blendZXY
	blendZYX
)

// sampleAndLerp 在立方体 8 个角上取梯度点积并按 order 做三轴插值。
func (n *ImprovedNoise) sampleAndLerp(i, j, k int, d, e, f float64, order blendOrder) float64 {
	var cube [2][2][2]float64
	for ix := 0; ix < 2; ix++ {
		for iy := 0; iy < 2; iy++ {
			for iz := 0; iz < 2; iz++ {
				index := n.perm(n.perm(i+ix) + j + iy)
				index = n.perm(index + k + iz)
				cube[ix][iy][iz] = gradDot(index, d-float64(ix), e-float64(iy), f-float64(iz))
			}
		}
	}
	var order3 [3]int
	switch order {
	case blendXYZ:
		order3 = [3]int{0, 1, 2}
	case blendXZY:
		order3 = [3]int{0, 2, 1}
	case blendYXZ:
		order3 = [3]int{1, 0, 2}
	case blendYZX:
		order3 = [3]int{1, 2, 0}
	case blendZXY:
		order3 = [3]int{2, 0, 1}
	default:
		order3 = [3]int{2, 1, 0}
	}
	deltas := [3]float64{fade(d), fade(e), fade(f)}
	// at 按（内层, 中层, 外层）三轴的位取角点值。
	at := func(inner, middle, outer int) float64 {
		var bits [3]int
		bits[order3[0]] = inner
		bits[order3[1]] = middle
		bits[order3[2]] = outer
		return cube[bits[0]][bits[1]][bits[2]]
	}
	innerDelta := deltas[order3[0]]
	middleDelta := deltas[order3[1]]
	outerDelta := deltas[order3[2]]
	plane := func(outer, middle int) float64 {
		return lerp(innerDelta, at(0, middle, outer), at(1, middle, outer))
	}
	return lerp(outerDelta,
		lerp(middleDelta, plane(0, 0), plane(0, 1)),
		lerp(middleDelta, plane(1, 0), plane(1, 1)))
}

// fade 是 perlin 的五次平滑曲线（原版 Mth.k）。
func fade(value float64) float64 {
	return value * value * value * (value*(value*6-15) + 10)
}

// lerp 是线性插值（原版 Mth.d：from + delta*(to-from)）。
func lerp(delta, first, second float64) float64 {
	return first + delta*(second-first)
}

// gradDot 计算第 index & 15 个梯度与 (x, y, z) 的点积（原版 SimplexNoise.dot，无归一化）。
func gradDot(index int, x, y, z float64) float64 {
	gradient := perlinGradients[index&15]
	return float64(gradient[0])*x + float64(gradient[1])*y + float64(gradient[2])*z
}

// perlinGradients 是原版 SimplexNoise.GRADIENT 的 16 项表
// （末 4 项非前 4 项的顺序重复，按 1.21.11 静态初始化读出）。
var perlinGradients = [16][3]int{
	{1, 1, 0}, {-1, 1, 0}, {1, -1, 0}, {-1, -1, 0},
	{1, 0, 1}, {-1, 0, 1}, {1, 0, -1}, {-1, 0, -1},
	{0, 1, 1}, {0, -1, 1}, {0, 1, -1}, {0, -1, -1},
	{1, 1, 0}, {0, -1, 1}, {-1, 1, 0}, {0, -1, -1},
}

// noiseOctaveDraws 是构造一个 ImprovedNoise 在随机源上的推进量：
// 3 次 nextDouble 折合 6 次 nextInt()，加 256 次洗牌，共 262。
// 原版 PerlinNoise 对零振幅八度用 RandomSource.consumeCount(262) 跳过。
const noiseOctaveDraws = 262

// wrap 等价原版 PerlinNoise.wrap：把坐标折回 ±33554432 的区间，
// 避免浮点精度在地图边缘之外退化。原版用 (long)Math.floor(...)，Java 的
// double→long 转换会饱和，这里显式复刻饱和行为。
func wrap(value float64) float64 {
	scaled := value/33554432 + 0.5
	var floored int64
	switch {
	case scaled >= 9223372036854775807.0:
		floored = math.MaxInt64
	case scaled <= -9223372036854775808.0:
		floored = math.MinInt64
	default:
		floored = int64(math.Floor(scaled))
	}
	return value - float64(floored)*33554432
}

// skipRandomDraws 等价原版 RandomSource.consumeCount：连续取 count 次 nextInt()。
// 对 Xoroshiro 而言每次 nextInt() 恰好消耗一个 nextLong()。
func skipRandomDraws(random *Xoroshiro, count int) {
	for i := 0; i < count; i++ {
		random.NextLong()
	}
}

// PerlinNoise 等价原版 net.minecraft.world.level.levelgen.synth.PerlinNoise 的
// 非 legacy 路径：按八度把多个 ImprovedNoise 用振幅加权叠加。
type PerlinNoise struct {
	amplitudes            []float64
	levels                []*ImprovedNoise
	lowestFreqInputFactor float64
	lowestFreqValueFactor float64
	maxValue              float64
}

// NewPerlinNoise 等价原版 PerlinNoise.create(RandomSource, int, DoubleList)。
//
// 随机源推进顺序按 1.21.11 字节码逐条复刻：
//  1. 先无条件构造一个 ImprovedNoise（作为最低频八度复用）；
//  2. 仅当最低频八度下标落在振幅表内且振幅非零时使用它，否则这份随机数被丢弃；
//  3. 对更低频（下标递减）的每个八度：振幅非零则新建 ImprovedNoise，
//     振幅为零（或越界）则 consumeCount(262) 跳过；
//  4. 校验实际构造的层数等于非零振幅个数；
//  5. 最低频八度不得高于 -(size-1)（原版“暂时禁用正八度”）。
func NewPerlinNoise(random *Xoroshiro, firstOctave int, amplitudes []float64) *PerlinNoise {
	size := len(amplitudes)
	if size == 0 {
		panic("vanilla: PerlinNoise 至少需要一个八度振幅")
	}
	perlin := &PerlinNoise{
		amplitudes: append([]float64(nil), amplitudes...),
		levels:     make([]*ImprovedNoise, size),
	}
	minOctave := -firstOctave
	shared := NewImprovedNoise(random)
	if minOctave >= 0 && minOctave < size && perlin.amplitudes[minOctave] != 0 {
		perlin.levels[minOctave] = shared
	}
	for i := minOctave - 1; i >= 0; i-- {
		if i < size && perlin.amplitudes[i] != 0 {
			perlin.levels[i] = NewImprovedNoise(random)
			continue
		}
		skipRandomDraws(random, noiseOctaveDraws)
	}
	created, nonZero := 0, 0
	for _, level := range perlin.levels {
		if level != nil {
			created++
		}
	}
	for _, amplitude := range perlin.amplitudes {
		if amplitude != 0 {
			nonZero++
		}
	}
	if created != nonZero {
		panic("vanilla: PerlinNoise 的八度层数与非零振幅个数不一致")
	}
	if minOctave < size-1 {
		panic("vanilla: PerlinNoise 暂不支持正八度")
	}
	perlin.initFactors(firstOctave)
	return perlin
}

// NewPerlinNoiseLegacy 等价原版 PerlinNoise.createLegacy(RandomSource, int, DoubleList)
// （混淆名 b）。这是世界生成实际使用的路径：每个非零振幅八度的 ImprovedNoise
// 都从 forkPositional() 派生的独立随机流取数，按 "octave_<n>" 的 MD5 命名，
// 因此不依赖创建顺序，也不存在“跳过一个 ImprovedNoise”的随机数消耗。
func NewPerlinNoiseLegacy(random *Xoroshiro, firstOctave int, amplitudes []float64) *PerlinNoise {
	size := len(amplitudes)
	if size == 0 {
		panic("vanilla: PerlinNoise 至少需要一个八度振幅")
	}
	perlin := &PerlinNoise{
		amplitudes: append([]float64(nil), amplitudes...),
		levels:     make([]*ImprovedNoise, size),
	}
	positional := random.ForkPositional()
	for i := 0; i < size; i++ {
		if perlin.amplitudes[i] == 0 {
			continue
		}
		octave := firstOctave + i
		perlin.levels[i] = NewImprovedNoise(positional.FromHashOf("octave_" + strconv.Itoa(octave)))
	}
	perlin.initFactors(firstOctave)
	return perlin
}

// initFactors 计算原版构造函数尾部的两个八度因子与理论上界：
//
//	lowestFreqInputFactor = 2^firstOctave
//	lowestFreqValueFactor = 2^(n-1) / (2^n - 1)
//	maxValue              = valueFactor(2)
func (p *PerlinNoise) initFactors(firstOctave int) {
	size := len(p.amplitudes)
	p.lowestFreqInputFactor = math.Pow(2, float64(firstOctave))
	p.lowestFreqValueFactor = math.Pow(2, float64(size-1)) / (math.Pow(2, float64(size)) - 1)
	p.maxValue = p.valueFactor(2)
}

// MaxValue 返回该噪声在 |input|≤2 时可能达到的最大绝对值（原版 maxValue()），
// 用于 NormalNoise 归一化。
func (p *PerlinNoise) MaxValue() float64 {
	return p.maxValue
}

// Value 返回 (x, y, z) 处的叠加噪声（原版 PerlinNoise.getValue(x,y,z)）。
func (p *PerlinNoise) Value(x, y, z float64) float64 {
	result := 0.0
	inputFactor := p.lowestFreqInputFactor
	valueFactor := p.lowestFreqValueFactor
	for i, level := range p.levels {
		if level != nil {
			result += p.amplitudes[i] * level.Noise(
				wrap(x*inputFactor),
				wrap(y*inputFactor),
				wrap(z*inputFactor),
			) * valueFactor
		}
		inputFactor *= 2
		valueFactor /= 2
	}
	return result
}

// valueFactor 等价原版私有方法 valueFactor(double)：按八度衰减后的振幅加权和。
func (p *PerlinNoise) valueFactor(value float64) float64 {
	result := 0.0
	factor := p.lowestFreqValueFactor
	for i, level := range p.levels {
		if level != nil {
			result += p.amplitudes[i] * value * factor
		}
		factor /= 2
	}
	return result
}

// normalNoiseInputFactor 是原版 NormalNoise.INPUT_FACTOR：第二份 PerlinNoise
// 采样时对坐标做的固定缩放，用于去相关。
const normalNoiseInputFactor = 1.0181268882175227

// NormalNoise 等价原版 net.minecraft.world.level.levelgen.synth.NormalNoise：
// 两份同参数、但随机相位不同的 PerlinNoise 相加后按 valueFactor 归一化，
// 使输出落在约 [-1, 1]。
type NormalNoise struct {
	first       *PerlinNoise
	second      *PerlinNoise
	firstOctave int
	amplitudes  []float64
	valueFactor float64
	maxValue    float64
}

// NewNormalNoise 等价原版 NormalNoise.create(RandomSource, NoiseParameters)。
//
// 注意：原版世界生成实际用的是 NewNormalNoiseLegacy（Noises.createNormalNoise
// 调的是 createLegacy）；这个非 legacy 版本仅用于对照测试。
func NewNormalNoise(random *Xoroshiro, firstOctave int, amplitudes []float64) *NormalNoise {
	first := NewPerlinNoise(random, firstOctave, amplitudes)
	second := NewPerlinNoise(random, firstOctave, amplitudes)
	return newNormalNoise(first, second, firstOctave, amplitudes)
}

// NewNormalNoiseLegacy 等价原版 NormalNoise.createLegacy(RandomSource, NoiseParameters)
// （混淆名 b），即原版世界生成（Noises.createNormalNoise）真正使用的路径。
func NewNormalNoiseLegacy(random *Xoroshiro, firstOctave int, amplitudes []float64) *NormalNoise {
	first := NewPerlinNoiseLegacy(random, firstOctave, amplitudes)
	second := NewPerlinNoiseLegacy(random, firstOctave, amplitudes)
	return newNormalNoise(first, second, firstOctave, amplitudes)
}

// newNormalNoise 完成两份 PerlinNoise 的归一化（原版构造函数的尾部）。
func newNormalNoise(first, second *PerlinNoise, firstOctave int, amplitudes []float64) *NormalNoise {
	minIndex, maxIndex := math.MaxInt32, math.MinInt32
	for i, amplitude := range amplitudes {
		if amplitude != 0 {
			minIndex = min(minIndex, i)
			maxIndex = max(maxIndex, i)
		}
	}
	valueFactor := 0.16666666666666666 / expectedDeviation(maxIndex-minIndex)
	return &NormalNoise{
		first:       first,
		second:      second,
		firstOctave: firstOctave,
		amplitudes:  append([]float64(nil), amplitudes...),
		valueFactor: valueFactor,
		maxValue:    (first.MaxValue() + second.MaxValue()) * valueFactor,
	}
}

// MaxValue 返回归一化后的理论上界（原版 maxValue()）。
func (n *NormalNoise) MaxValue() float64 {
	return n.maxValue
}

// Value 返回 (x, y, z) 处的噪声值（原版 NormalNoise.getValue(x,y,z)）。
func (n *NormalNoise) Value(x, y, z float64) float64 {
	scaledX := x * normalNoiseInputFactor
	scaledY := y * normalNoiseInputFactor
	scaledZ := z * normalNoiseInputFactor
	return (n.first.Value(x, y, z) + n.second.Value(scaledX, scaledY, scaledZ)) * n.valueFactor
}

// expectedDeviation 等价原版 NormalNoise.expectedDeviation(int)：
// 振幅跨度越小，叠加后的方差越接近单八度，需要更小的归一化分母。
func expectedDeviation(octaveSpan int) float64 {
	return 0.1 * (1 + 1/float64(octaveSpan+1))
}
