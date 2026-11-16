//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import "math"

// SimplexNoise 复刻 1.21.11 的 SimplexNoise（2D/3D）。主要用于
// Biome 的温度修正，当前只暴露 getValue(x,z)。
type SimplexNoise struct {
	xo, yo, zo float64
	p          [512]int
}

var simplexGradient = [16][3]int{
	{1, 1, 0}, {-1, 1, 0}, {1, -1, 0}, {-1, -1, 0},
	{1, 0, 1}, {-1, 0, 1}, {1, 0, -1}, {-1, 0, -1},
	{0, 1, 1}, {0, -1, 1}, {0, 1, -1}, {0, -1, -1},
	{1, 1, 0}, {0, -1, 1}, {-1, 1, 0}, {0, -1, -1},
}

const (
	simplexSQRT3 = 1.7320508075688772
	simplexF2    = 0.5 * (simplexSQRT3 - 1.0)
	simplexG2    = (3.0 - simplexSQRT3) / 6.0
)

// newSimplexNoise 对应 SimplexNoise(RandomSource)：3 个 double + 置换表。
func newBiomeSimplexNoise(random *legacyRandom) *SimplexNoise {
	noise := &SimplexNoise{
		xo: random.nextDouble() * 256,
		yo: random.nextDouble() * 256,
		zo: random.nextDouble() * 256,
	}
	for i := 0; i < 256; i++ {
		noise.p[i] = i
	}
	for i := 0; i < 256; i++ {
		other := random.nextIntBounded(256 - i)
		noise.p[i], noise.p[other+i] = noise.p[other+i], noise.p[i]
	}
	return noise
}

func (s *SimplexNoise) perm(index int) int { return s.p[index&0xFF] }

func biomeSimplexDot(gradient [3]int, x, y, z float64) float64 {
	return float64(gradient[0])*x + float64(gradient[1])*y + float64(gradient[2])*z
}

func biomeSimplexCorner(permutation int, x, y, z, factor float64) float64 {
	value := factor - x*x - y*y - z*z
	if value < 0 {
		return 0
	}
	value *= value
	return value * value * biomeSimplexDot(simplexGradient[permutation], x, y, z)
}

// Value2D 对应 SimplexNoise.getValue(double,double)。
func (s *SimplexNoise) Value2D(x, z float64) float64 {
	sum := (x + z) * simplexF2
	i := int(math.Floor(x + sum))
	j := int(math.Floor(z + sum))
	origin := float64(i+j) * simplexG2
	x0 := x - (float64(i) - origin)
	z0 := z - (float64(j) - origin)
	var i1, j1 int
	if x0 > z0 {
		i1, j1 = 1, 0
	} else {
		i1, j1 = 0, 1
	}
	x1 := x0 - float64(i1) + simplexG2
	z1 := z0 - float64(j1) + simplexG2
	x2 := x0 - 1 + 2*simplexG2
	z2 := z0 - 1 + 2*simplexG2
	ii, jj := i&0xFF, j&0xFF
	p0 := s.perm(ii+s.perm(jj)) % 12
	p1 := s.perm(ii+i1+s.perm(jj+j1)) % 12
	p2 := s.perm(ii+1+s.perm(jj+1)) % 12
	return 70 * (biomeSimplexCorner(p0, x0, z0, 0, 0.5) +
		biomeSimplexCorner(p1, x1, z1, 0, 0.5) +
		biomeSimplexCorner(p2, x2, z2, 0, 0.5))
}

// Value3D 对应 SimplexNoise.getValue(double,double,double)。
func (s *SimplexNoise) Value3D(x, y, z float64) float64 {
	sum := (x + y + z) * (1.0 / 3.0)
	i := int(math.Floor(x + sum))
	j := int(math.Floor(y + sum))
	k := int(math.Floor(z + sum))
	origin := float64(i+j+k) * (1.0 / 6.0)
	x0 := x - (float64(i) - origin)
	y0 := y - (float64(j) - origin)
	z0 := z - (float64(k) - origin)
	var i1, j1, k1, i2, j2, k2 int
	if x0 >= y0 {
		if y0 >= z0 {
			i1, j1, k1, i2, j2, k2 = 1, 0, 0, 1, 1, 0
		} else if x0 >= z0 {
			i1, j1, k1, i2, j2, k2 = 1, 0, 0, 1, 0, 1
		} else {
			i1, j1, k1, i2, j2, k2 = 0, 0, 1, 1, 0, 1
		}
	} else if y0 < z0 {
		i1, j1, k1, i2, j2, k2 = 0, 0, 1, 0, 1, 1
	} else if x0 < z0 {
		i1, j1, k1, i2, j2, k2 = 0, 1, 0, 0, 1, 1
	} else {
		i1, j1, k1, i2, j2, k2 = 0, 1, 0, 1, 1, 0
	}
	x1 := x0 - float64(i1) + 1.0/6.0
	y1 := y0 - float64(j1) + 1.0/6.0
	z1 := z0 - float64(k1) + 1.0/6.0
	x2 := x0 - float64(i2) + 1.0/3.0
	y2 := y0 - float64(j2) + 1.0/3.0
	z2 := z0 - float64(k2) + 1.0/3.0
	x3 := x0 - 1.0 + 0.5
	y3 := y0 - 1.0 + 0.5
	z3 := z0 - 1.0 + 0.5
	ii, jj, kk := i&0xFF, j&0xFF, k&0xFF
	p0 := s.perm(ii+s.perm(jj+s.perm(kk))) % 12
	p1 := s.perm(ii+i1+s.perm(jj+j1+s.perm(kk+k1))) % 12
	p2 := s.perm(ii+i2+s.perm(jj+j2+s.perm(kk+k2))) % 12
	p3 := s.perm(ii+1+s.perm(jj+1+s.perm(kk+1))) % 12
	return 32 * (biomeSimplexCorner(p0, x0, y0, z0, 0.6) +
		biomeSimplexCorner(p1, x1, y1, z1, 0.6) +
		biomeSimplexCorner(p2, x2, y2, z2, 0.6) +
		biomeSimplexCorner(p3, x3, y3, z3, 0.6))
}

// PerlinSimplexNoise 复刻 1.21.11 PerlinSimplexNoise。
type PerlinSimplexNoise struct {
	levels             []*SimplexNoise
	highestInputFactor float64
	highestValueFactor float64
}

func newPerlinSimplexNoise(random *legacyRandom, octaves []int) *PerlinSimplexNoise {
	if len(octaves) == 0 {
		panic("vanilla: PerlinSimplexNoise 需要至少一个八度")
	}
	minOctave, maxOctave := octaves[0], octaves[0]
	present := make(map[int]bool, len(octaves))
	for _, octave := range octaves {
		present[octave] = true
		if octave < minOctave {
			minOctave = octave
		}
		if octave > maxOctave {
			maxOctave = octave
		}
	}
	count := maxOctave - minOctave + 1
	base := newBiomeSimplexNoise(random)
	noise := &PerlinSimplexNoise{levels: make([]*SimplexNoise, count)}
	if maxOctave >= 0 && maxOctave < count && present[0] {
		noise.levels[maxOctave] = base
	}
	for index := maxOctave + 1; index < count; index++ {
		if index >= 0 && present[maxOctave-index] {
			noise.levels[index] = newBiomeSimplexNoise(random)
		} else {
			random.consumeCount(262)
		}
	}
	if maxOctave > 0 {
		seed := int64(base.Value3D(base.xo, base.yo, base.zo) * 9.223372036854776e18)
		second := newLegacyRandom(seed)
		for index := maxOctave - 1; index >= 0; index-- {
			if index < count && present[maxOctave-index] {
				noise.levels[index] = newBiomeSimplexNoise(second)
			} else {
				second.consumeCount(262)
			}
		}
	}
	noise.highestInputFactor = math.Pow(2, float64(maxOctave))
	noise.highestValueFactor = 1 / (math.Pow(2, float64(count)) - 1)
	return noise
}

// Value 对应 PerlinSimplexNoise.getValue(x,z,useOrigin)。
func (n *PerlinSimplexNoise) Value(x, z float64, useOrigin bool) float64 {
	var result float64
	inputFactor := n.highestInputFactor
	valueFactor := n.highestValueFactor
	for _, level := range n.levels {
		if level != nil {
			ox, oz := 0.0, 0.0
			if useOrigin {
				ox, oz = level.xo, level.yo
			}
			result += level.Value2D(x*inputFactor+ox, z*inputFactor+oz) * valueFactor
		}
		inputFactor /= 2
		valueFactor *= 2
	}
	return result
}

// 温度修正器使用固定种子的世界生成噪声（Biome 静态字段）。
var (
	biomeTemperatureNoise       = newPerlinSimplexNoise(newLegacyRandom(1234), []int{0})
	biomeFrozenTemperatureNoise = newPerlinSimplexNoise(newLegacyRandom(3456), []int{-2, -1, 0})
	biomeInfoNoise              = newPerlinSimplexNoise(newLegacyRandom(2345), []int{0})
)
