//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"math"
	"sync"
)

// 末地岛屿密度函数（DensityFunctions.EndIslandDensityFunction）的移植。
// 原版在代码里用固定种子 0 的 LegacyRandomSource，再 setSeed(17292)
// 构造 SimplexNoise；因此结果与世界种子无关。

var endIslandNoise struct {
	once    sync.Once
	simplex *simplexNoise
}

func endIslandsValue(x, z int) float64 {
	endIslandNoise.once.Do(func() {
		endIslandNoise.simplex = newSimplexNoise(0, 17292)
	})
	return (endIslandHeight(endIslandNoise.simplex, x/8, z/8) - 8) / 128
}

func endIslandHeight(noise *simplexNoise, x, z int) float64 {
	x2, z2 := x/2, z/2
	oddX, oddZ := x%2, z%2
	height := 100 - float64(x*x+z*z)*8
	height = clampFloat(height, -100, 80)
	for dx := -12; dx <= 12; dx++ {
		for dz := -12; dz <= 12; dz++ {
			nx := int64(x2 + dx)
			nz := int64(z2 + dz)
			if nx*nx+nz*nz <= 4096 || !(noise.Value2(float64(nx), float64(nz)) < -0.9) {
				continue
			}
			radius := float64((nx*3439+nz*147)%13) + 9
			ex := float64(oddX - dx*2)
			ez := float64(oddZ - dz*2)
			candidate := 100 - math.Hypot(ex, ez)*radius
			candidate = clampFloat(candidate, -100, 80)
			if candidate > height {
				height = candidate
			}
		}
	}
	return height
}

// floorFloat 等价 Mth.floor(float)：向下取整，再转 int。
func floorFloat(value float64) int { return int(math.Floor(value)) }

func clampFloat(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// simplexNoise 是原版 SimplexNoise 的 2D 子集，足够 EndIslandDensityFunction 使用。
type simplexNoise struct {
	perm [512]int
}

func newSimplexNoise(seed, extraSeed int64) *simplexNoise {
	random := newLegacyRandom(seed)
	random.setSeed(extraSeed)
	// 原版构造函数先读三个偏移量（2D 求值不使用），再洗牌。
	_ = random.nextDouble()
	_ = random.nextDouble()
	_ = random.nextDouble()
	noise := &simplexNoise{}
	for i := 0; i < 256; i++ {
		noise.perm[i] = i
	}
	for i := 0; i < 256; i++ {
		j := random.nextIntBounded(256-i) + i
		noise.perm[i], noise.perm[j] = noise.perm[j], noise.perm[i]
	}
	copy(noise.perm[256:], noise.perm[:256])
	return noise
}

func (n *simplexNoise) grad(index int, x, y float64) float64 {
	gradient := [12][2]float64{
		{1, 1}, {-1, 1}, {1, -1}, {-1, -1},
		{1, 0}, {-1, 0}, {1, 0}, {-1, 0},
		{0, 1}, {0, -1}, {0, 1}, {0, -1},
	}
	index %= 12
	return gradient[index][0]*x + gradient[index][1]*y
}

// Value2 等价原版 SimplexNoise.getValue(double x, double y)。
func (n *simplexNoise) Value2(x, y float64) float64 {
	skew := 0.5 * (math.Sqrt(3) - 1)
	unskew := (3 - math.Sqrt(3)) / 6
	s := (x + y) * skew
	i := floorFloat(x + s)
	j := floorFloat(y + s)
	t := float64(i+j) * unskew
	x0 := x - (float64(i) - t)
	y0 := y - (float64(j) - t)
	var i1, j1 int
	if x0 > y0 {
		i1, j1 = 1, 0
	} else {
		i1, j1 = 0, 1
	}
	x1 := x0 - float64(i1) + unskew
	y1 := y0 - float64(j1) + unskew
	x2 := x0 - 1 + 2*unskew
	y2 := y0 - 1 + 2*unskew
	ii := i & 0xff
	jj := j & 0xff
	gi0 := n.perm[ii+n.perm[jj]] % 12
	gi1 := n.perm[ii+i1+n.perm[jj+j1]] % 12
	gi2 := n.perm[ii+1+n.perm[jj+1]] % 12
	n0 := simplexCorner(gi0, x0, y0)
	n1 := simplexCorner(gi1, x1, y1)
	n2 := simplexCorner(gi2, x2, y2)
	return 70 * (n0 + n1 + n2)
}

func simplexCorner(index int, x, y float64) float64 {
	attenuation := 0.5 - x*x - y*y
	if attenuation < 0 {
		return 0
	}
	attenuation *= attenuation
	gradient := [12][2]float64{
		{1, 1}, {-1, 1}, {1, -1}, {-1, -1},
		{1, 0}, {-1, 0}, {1, 0}, {-1, 0},
		{0, 1}, {0, -1}, {0, 1}, {0, -1},
	}
	index %= 12
	return attenuation * attenuation * (gradient[index][0]*x + gradient[index][1]*y)
}
