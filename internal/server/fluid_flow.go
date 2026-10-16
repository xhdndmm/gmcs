//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"

	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 流体流向与实体推动。
//
// 对照来源：官方 1.21.11 服务端反汇编——
//   - FlowingFluid.getFlow（obf fkz.a(BlockGetter, BlockPos, FluidState)）：
//     水平方向按四邻域水位差求和，下落流体在水平方向被实心面挡住时附加 -6 的
//     垂直分量，最后归一化；
//   - Entity.updateInWaterStateAndDoWaterCurrentPushing / updateFluidHeightAndDoFluidPushing
//     （obf cgk.bH/bG/a(TagKey, double)）：实体碰撞盒覆盖的方块中取水中流向平均，
//     非玩家实体归一化，乘运动比例 0.014；当水平速度与流向都很小时把流向
//     放大到 0.0045（避免“贴着水面不动”）。
//
// gmcs 目前只把水流推动应用在掉落物上（玩家由客户端移动负责，生物寻路不建模
// 水流漂流）；水面高度用 FluidState.getAmount()/9 近似（原版 getHeight 还包含
// 上方流体的修正）。
const (
	// fluidFlowMotionScale 是水中每 tick 的流向推动比例（原版 0.014）。
	fluidFlowMotionScale = 0.014
	// fluidMinMotion 是“水平速度足够小”的阈值（原版 0.003）。
	fluidMinMotion = 0.003
	// fluidMinFlow 是低速时流向被抬升到的最小长度（原版 0.0045）。
	fluidMinFlow = 0.0045
	// fluidFlowLowHeight 是流向按流体高度缩放的阈值（原版 0.4）。
	fluidFlowLowHeight = 0.4
)

// fluidHorizontalDirs 是原版 Direction.Plane.HORIZONTAL 的顺序与方向名。
var fluidHorizontalDirs = []struct {
	dx, dy, dz int
	dir        string
}{
	{0, 0, -1, "north"},
	{1, 0, 0, "east"},
	{0, 0, 1, "south"},
	{-1, 0, 0, "west"},
}

// fluidFlowVector 返回 (x, y, z) 处流体的流向（原版 FlowingFluid.getFlow）。
// 非流体位置返回零向量；静态平地水面的流向为零。
func (s *Server) fluidFlowVector(w *world.World, pos [3]int) (float64, float64, float64) {
	own, ok := fluidInfoOf(w.BlockAt(pos[0], pos[1], pos[2]))
	if !ok {
		return 0, 0, 0
	}
	ownHeight := float64(own.amount) / 9.0
	var sumX, sumZ float64
	for _, dir := range fluidHorizontalDirs {
		nx, ny, nz := pos[0]+dir.dx, pos[1]+dir.dy, pos[2]+dir.dz
		if _, ok := world.SectionIndex(ny); !ok {
			continue
		}
		neighborState := w.BlockAt(nx, ny, nz)
		neighbor, neighborIsFluid := fluidInfoOf(neighborState)
		// 原版 affectsFlow：空流体或同种流体才参与水位差计算。
		if neighborIsFluid && neighbor.name != own.name {
			continue
		}
		neighborHeight := 0.0
		if neighborIsFluid {
			neighborHeight = float64(neighbor.amount) / 9.0
		}
		var offset float64
		if neighborHeight == 0 {
			if !fluidBlocksMotion(neighborState) {
				if by := ny - 1; by >= 0 {
					if _, ok := world.SectionIndex(by); ok {
						if below, belowOK := fluidInfoOf(w.BlockAt(nx, by, nz)); belowOK && below.name == own.name {
							neighborHeight = float64(below.amount) / 9.0
							if neighborHeight > 0 {
								offset = ownHeight - (neighborHeight - 0.8888889)
							}
						}
					}
				}
			}
		} else {
			offset = ownHeight - neighborHeight
		}
		if offset != 0 {
			sumX += float64(dir.dx) * offset
			sumZ += float64(dir.dz) * offset
		}
	}
	var vy float64
	if own.falling {
		// 下落水流被水平方向的实心面挡住时整体向下。
		for _, dir := range fluidHorizontalDirs {
			nx, ny, nz := pos[0]+dir.dx, pos[1]+dir.dy, pos[2]+dir.dz
			if s.fluidSolidFace(w, nx, ny, nz, dir.dir) || s.fluidSolidFace(w, nx, ny-1, nz, dir.dir) {
				// 原版先归一化水平分量，再叠加 -6 的垂直分量。
				if length := math.Hypot(sumX, sumZ); length > 0 {
					sumX /= length
					sumZ /= length
				}
				vy = -6
				break
			}
		}
	}
	length := math.Sqrt(sumX*sumX + sumZ*sumZ + vy*vy)
	if length == 0 {
		return 0, 0, 0
	}
	return sumX / length, vy / length, sumZ / length
}

// fluidBlocksMotion 报告方块是否阻挡流体横向流动（原版 BlockState.blocksMotion：
// 碰撞形状为空时不阻挡；这里用生成的全部方块碰撞形状近似）。
func fluidBlocksMotion(state uint16) bool {
	return len(registry.BlockStateShape(state)) > 0
}

// fluidSolidFace 报告 (x, y, z) 方块在 dir 方向的表面是否完整（原版 isSolidFace，
// 使用原版 isFaceSturdy 的形状近似）。
func (s *Server) fluidSolidFace(w *world.World, x, y, z int, dir string) bool {
	if _, ok := world.SectionIndex(y); !ok {
		return false
	}
	return s.faceSturdy(w.BlockAt(x, y, z), dir)
}

// itemWaterFlowPush 返回掉落物一帧内受到的水流推动速度增量
// （原版 Entity.updateFluidHeightAndDoFluidPushing 的水分支）：
// 遍历碰撞盒覆盖的方块，取水中流向的平均值（流体高度低于 0.4 时按高度缩放），
// 非玩家实体按原版归一化并乘 0.014；水平速度与流向都很小时把流向抬到 0.0045。
func (s *Server) itemWaterFlowPush(w *world.World, box world.Box, velX, velZ float64) (float64, float64, float64) {
	x0 := int(math.Floor(box.MinX))
	x1 := int(math.Floor(box.MaxX))
	z0 := int(math.Floor(box.MinZ))
	z1 := int(math.Floor(box.MaxZ))
	y0 := int(math.Floor(box.MinY))
	y1 := int(math.Floor(box.MaxY))
	var sumX, sumY, sumZ float64
	count := 0
	for bx := x0; bx <= x1; bx++ {
		for by := y0; by <= y1; by++ {
			if _, ok := world.SectionIndex(by); !ok {
				continue
			}
			for bz := z0; bz <= z1; bz++ {
				info, ok := fluidInfoOf(w.BlockAt(bx, by, bz))
				if !ok || info.name != waterBlockName {
					continue
				}
				// 原版：流体顶面（y + getHeight）低于实体盒底部时不参与推动。
				height := float64(by) + float64(info.amount)/9.0 - box.MinY
				if height < 0 {
					continue
				}
				vx, vy, vz := s.fluidFlowVector(w, [3]int{bx, by, bz})
				if height < fluidFlowLowHeight {
					vx, vy, vz = vx*height, vy*height, vz*height
				}
				sumX += vx
				sumY += vy
				sumZ += vz
				count++
			}
		}
	}
	if count == 0 {
		return 0, 0, 0
	}
	sumX /= float64(count)
	sumY /= float64(count)
	sumZ /= float64(count)
	// 非玩家实体：归一化（玩家保留平均流向，由客户端移动逻辑处理）。
	if length := math.Sqrt(sumX*sumX + sumY*sumY + sumZ*sumZ); length > 0 {
		sumX /= length
		sumY /= length
		sumZ /= length
	}
	sumX *= fluidFlowMotionScale
	sumY *= fluidFlowMotionScale
	sumZ *= fluidFlowMotionScale
	flowLength := math.Sqrt(sumX*sumX + sumY*sumY + sumZ*sumZ)
	if math.Abs(velX) < fluidMinMotion && math.Abs(velZ) < fluidMinMotion && flowLength < fluidMinFlow && flowLength > 0 {
		scale := fluidMinFlow / flowLength
		sumX *= scale
		sumY *= scale
		sumZ *= scale
	}
	return sumX, sumY, sumZ
}
