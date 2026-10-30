//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"fmt"
	"math"
)

// denseGrid 是某个 interpolated 节点在一个区块范围内的密集 lattice。
// 相邻格点间距为 (cellWidth, cellHeight, cellWidth)，与点采样使用同一套坐标，
// 因此插值结果与逐点求值逐位一致（见 TestChunkSamplerMatchesPointwise）。
type denseGrid struct {
	originX, originY, originZ int
	countX, countY, countZ    int
	cellWidth, cellHeight     int
	data                      []float64
}

func (g *denseGrid) index(ix, iy, iz int) int {
	return (iy*g.countX+ix)*g.countZ + iz
}

// sample 在格点范围内做三线性插值；坐标落在范围外时返回 ok=false，
// 由调用方回退到逐点求值。
func (g *denseGrid) sample(ctx Context) (float64, bool) {
	localX := ctx.X - g.originX
	localY := ctx.Y - g.originY
	localZ := ctx.Z - g.originZ
	if localX < 0 || localY < 0 || localZ < 0 {
		return 0, false
	}
	ix := localX / g.cellWidth
	iy := localY / g.cellHeight
	iz := localZ / g.cellWidth
	if ix >= g.countX-1 || iy >= g.countY-1 || iz >= g.countZ-1 {
		return 0, false
	}
	deltaX := float64(localX-ix*g.cellWidth) / float64(g.cellWidth)
	deltaY := float64(localY-iy*g.cellHeight) / float64(g.cellHeight)
	deltaZ := float64(localZ-iz*g.cellWidth) / float64(g.cellWidth)

	c000 := g.data[g.index(ix, iy, iz)]
	c100 := g.data[g.index(ix+1, iy, iz)]
	c010 := g.data[g.index(ix, iy+1, iz)]
	c110 := g.data[g.index(ix+1, iy+1, iz)]
	c001 := g.data[g.index(ix, iy, iz+1)]
	c101 := g.data[g.index(ix+1, iy, iz+1)]
	c011 := g.data[g.index(ix, iy+1, iz+1)]
	c111 := g.data[g.index(ix+1, iy+1, iz+1)]

	plane0 := lerp(deltaY, lerp(deltaX, c000, c100), lerp(deltaX, c010, c110))
	plane1 := lerp(deltaY, lerp(deltaX, c001, c101), lerp(deltaX, c011, c111))
	return lerp(deltaZ, plane0, plane1), true
}

// ChunkSampler 在一个区块范围内预计算所有 interpolated 节点的 lattice，
// 之后逐方块求值只需按索引取值（原版 NoiseChunk 的做法）。
//
// 同一 Router 上的 ChunkSampler 不可并发使用（共享底层求值器）。
type ChunkSampler struct {
	router    *Router
	evaluator *Evaluator
	setting   NoiseSetting
	originX   int
	originZ   int
	interp    []*Node
	aquifer   *Aquifer
}

// NewChunkSampler 为指定区块预计算 lattice。返回后该 sampler 即为可用的
// 逐方块密度/实心判定入口。
func (r *Router) NewChunkSampler(chunkX, chunkZ int) *ChunkSampler {
	setting := r.setting
	sampler := &ChunkSampler{
		router:    r,
		evaluator: r.evaluator,
		setting:   setting,
		originX:   chunkX * 16,
		originZ:   chunkZ * 16,
	}
	r.evaluator.ResetInterpolationCache()
	r.evaluator.grids = make(map[*Node]*denseGrid)

	// 后序遍历收集 interpolated 节点：内层先算，外层取角点时直接命中内层网格。
	seen := make(map[*Node]bool)
	var walk func(*Node)
	walk = func(node *Node) {
		if node == nil || seen[node] {
			return
		}
		seen[node] = true
		for _, child := range node.Children {
			walk(child)
		}
		if node.Spline != nil {
			walk(node.Spline.Coordinate)
			var walkSpline func(*Spline)
			walkSpline = func(spline *Spline) {
				for _, point := range spline.Points {
					if point.Sub != nil {
						walkSpline(point.Sub)
					}
				}
			}
			walkSpline(node.Spline)
		}
		if node.Type == "interpolated" {
			sampler.interp = append(sampler.interp, node)
		}
	}
	for _, key := range r.keys {
		walk(r.nodes[key])
	}
	for _, node := range sampler.interp {
		r.evaluator.grids[node] = sampler.buildGrid(node)
	}
	return sampler
}

// buildGrid 计算单个 interpolated 节点的 lattice。
func (s *ChunkSampler) buildGrid(node *Node) *denseGrid {
	cellWidth := s.setting.CellWidth
	cellHeight := s.setting.CellHeight
	if cellWidth <= 0 || cellHeight <= 0 {
		panic(fmt.Sprintf("invalid cell size: width=%d height=%d", cellWidth, cellHeight))
	}
	countX := 16/cellWidth + 1
	countZ := 16/cellWidth + 1
	countY := s.setting.Height/cellHeight + 1
	if countX <= 0 || countY <= 0 || countZ <= 0 {
		panic(fmt.Sprintf("invalid grid size: countX=%d countY=%d countZ=%d", countX, countY, countZ))
	}
	if countX > math.MaxInt/countY {
		panic(fmt.Sprintf("grid allocation size overflows int: countX=%d countY=%d countZ=%d", countX, countY, countZ))
	}
	xy := countX * countY
	if xy > math.MaxInt/countZ {
		panic(fmt.Sprintf("grid allocation size overflows int: countX=%d countY=%d countZ=%d", countX, countY, countZ))
	}
	size := xy * countZ
	grid := &denseGrid{
		originX:    s.originX,
		originY:    s.setting.MinY,
		originZ:    s.originZ,
		countX:     countX,
		countY:     countY,
		countZ:     countZ,
		cellWidth:  cellWidth,
		cellHeight: cellHeight,
		data:       make([]float64, size),
	}
	for iy := 0; iy < countY; iy++ {
		y := s.setting.MinY + iy*cellHeight
		for ix := 0; ix < countX; ix++ {
			x := s.originX + ix*cellWidth
			row := (iy*countX + ix) * countZ
			for iz := 0; iz < countZ; iz++ {
				grid.data[row+iz] = s.evaluator.Evaluate(node.Children[0], Context{
					X: x,
					Y: y,
					Z: s.originZ + iz*cellWidth,
				})
			}
		}
	}
	return grid
}

// InterpolatedNodes 返回该区块预计算了 lattice 的 interpolated 节点数量（诊断用）。
func (s *ChunkSampler) InterpolatedNodes() int {
	return len(s.interp)
}

// Density 返回 final_density 在给定坐标的取值（必须位于该区块的 x/z 范围内）。
func (s *ChunkSampler) Density(x, y, z int) float64 {
	return s.evaluator.Evaluate(s.evaluator.finalDensity, Context{X: x, Y: y, Z: z})
}

// Solid 返回该坐标是否实心（final_density > 0）。
func (s *ChunkSampler) Solid(x, y, z int) bool {
	return s.Density(x, y, z) > 0
}

// Aquifer 返回该区块的含水层判定器（首次调用时构造并缓存）。
// 含水层与密度采样共享同一个求值器，因此必须先通过该 sampler 求密度，
// 二者才会看到一致的内插值网格。
func (s *ChunkSampler) Aquifer() *Aquifer {
	if s.aquifer == nil {
		s.aquifer = newAquifer(s)
	}
	return s.aquifer
}

// Substance 返回该方块在"密度 + 含水层"阶段的判定结果：
//
//   - defaultBlock 为 true 时表示密度为正，调用方应放置 noise_settings.default_block；
//   - 否则 kind 为该处原本的流体类别（空气 / 水 / 岩浆）。
//
// 这仍不是最终方块：表面规则（草方块、深板岩、基岩等）属于后续阶段。
func (s *ChunkSampler) Substance(x, y, z int) (kind fluidKind, defaultBlock bool) {
	return s.Aquifer().ComputeSubstance(x, y, z, s.Density(x, y, z))
}
