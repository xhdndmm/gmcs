//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import "math"

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
