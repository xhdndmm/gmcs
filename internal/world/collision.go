package world

import (
	"math"

	"gmcs/internal/registry"
)

// 轴对齐碰撞（AABB）与方块碰撞形状的查询。
//
// 形状数据来自 registry.BlockStateShape（1/16 单位的盒子列表，随方块状态生成）。
// 本文件提供共享的碰撞原语，供玩家反穿墙/站立检测、掉落物与生物物理复用。
//
// 约定：
//   - 相触（边界重合）不算相交，允许实体贴着方块表面滑动；
//   - 形状盒最高 24/16（栅栏/墙），查询时按此向下扩展扫描范围。
const (
	// shapeMaxRise 是碰撞形状高出所在方块的最大值（栅栏/墙 24/16）。
	shapeMaxRise = 1.5
	// touchEpsilon 是支撑面检测的容差（玩家脚底与表面的浮点误差）。
	touchEpsilon = 0.01
)

// Box 是轴对齐碰撞盒（方块单位）。
type Box struct {
	MinX, MinY, MinZ float64
	MaxX, MaxY, MaxZ float64
}

// shapeOverlaps 报告碰撞盒与方块 (bx, by, bz) 处的形状盒是否相交（相触不算）。
func (b Box) shapeOverlaps(bx, by, bz int, s registry.ShapeBox) bool {
	sx1 := float64(bx) + float64(s.X1)/16
	sy1 := float64(by) + float64(s.Y1)/16
	sz1 := float64(bz) + float64(s.Z1)/16
	sx2 := float64(bx) + float64(s.X2)/16
	sy2 := float64(by) + float64(s.Y2)/16
	sz2 := float64(bz) + float64(s.Z2)/16
	return b.MinX < sx2 && b.MaxX > sx1 &&
		b.MinY < sy2 && b.MaxY > sy1 &&
		b.MinZ < sz2 && b.MaxZ > sz1
}

// cellRange 返回盒子覆盖的方块坐标范围（y 向下扩展 shapeMaxRise，
// 覆盖从下方方块升起的形状）。
func (b Box) cellRange() (x1, y1, z1, x2, y2, z2 int) {
	x1 = int(math.Floor(b.MinX))
	y1 = int(math.Floor(b.MinY - shapeMaxRise))
	z1 = int(math.Floor(b.MinZ))
	x2 = int(math.Floor(b.MaxX))
	y2 = int(math.Floor(b.MaxY))
	z2 = int(math.Floor(b.MaxZ))
	return
}

// Collides 报告碰撞盒是否与世界中任意碰撞形状相交（相触不算）。
func (w *World) Collides(b Box) bool {
	x1, y1, z1, x2, y2, z2 := b.cellRange()
	for bx := x1; bx <= x2; bx++ {
		for by := y1; by <= y2; by++ {
			for bz := z1; bz <= z2; bz++ {
				for _, s := range registry.BlockStateShape(w.BlockAt(bx, by, bz)) {
					if b.shapeOverlaps(bx, by, bz, s) {
						return true
					}
				}
			}
		}
	}
	return false
}

// ClipMove 沿单轴（0=X、1=Y、2=Z）把碰撞盒移动 delta，
// 返回被碰撞形状裁剪后的实际位移（扫描式逐轴碰撞）。
func (w *World) ClipMove(b Box, axis int, delta float64) float64 {
	if delta == 0 {
		return 0
	}
	// 扫掠区域：盒子并上移动后的盒子。
	swept := b
	switch axis {
	case 0:
		if delta < 0 {
			swept.MinX += delta
		} else {
			swept.MaxX += delta
		}
	case 1:
		if delta < 0 {
			swept.MinY += delta
		} else {
			swept.MaxY += delta
		}
	case 2:
		if delta < 0 {
			swept.MinZ += delta
		} else {
			swept.MaxZ += delta
		}
	}
	x1, y1, z1, x2, y2, z2 := swept.cellRange()
	for bx := x1; bx <= x2; bx++ {
		for by := y1; by <= y2; by++ {
			for bz := z1; bz <= z2; bz++ {
				for _, s := range registry.BlockStateShape(w.BlockAt(bx, by, bz)) {
					delta = clipAgainst(b, axis, delta, bx, by, bz, s)
				}
			}
		}
	}
	return delta
}

// clipAgainst 用单个形状盒裁剪沿 axis 的位移。
func clipAgainst(b Box, axis int, delta float64, bx, by, bz int, s registry.ShapeBox) float64 {
	sx1 := float64(bx) + float64(s.X1)/16
	sy1 := float64(by) + float64(s.Y1)/16
	sz1 := float64(bz) + float64(s.Z1)/16
	sx2 := float64(bx) + float64(s.X2)/16
	sy2 := float64(by) + float64(s.Y2)/16
	sz2 := float64(bz) + float64(s.Z2)/16
	// 非移动轴必须严格相交（相触可沿面滑动）。
	switch axis {
	case 0:
		if b.MinY >= sy2 || b.MaxY <= sy1 || b.MinZ >= sz2 || b.MaxZ <= sz1 {
			return delta
		}
		if delta > 0 {
			if limit := sx1 - b.MaxX; limit < delta && limit >= 0 {
				return limit
			}
		} else if limit := sx2 - b.MinX; limit > delta && limit <= 0 {
			return limit
		}
	case 1:
		if b.MinX >= sx2 || b.MaxX <= sx1 || b.MinZ >= sz2 || b.MaxZ <= sz1 {
			return delta
		}
		if delta > 0 {
			if limit := sy1 - b.MaxY; limit < delta && limit >= 0 {
				return limit
			}
		} else if limit := sy2 - b.MinY; limit > delta && limit <= 0 {
			return limit
		}
	case 2:
		if b.MinX >= sx2 || b.MaxX <= sx1 || b.MinY >= sy2 || b.MaxY <= sy1 {
			return delta
		}
		if delta > 0 {
			if limit := sz1 - b.MaxZ; limit < delta && limit >= 0 {
				return limit
			}
		} else if limit := sz2 - b.MinZ; limit > delta && limit <= 0 {
			return limit
		}
	}
	return delta
}

// SurfaceBelow 返回碰撞盒底面下方 gap 范围内、与盒子水平范围相交的最高
// 碰撞面高度与提供该表面的方块状态（脚底支撑面检测）。
// 水平相触视为相交（贴边站立算支撑）。
func (w *World) SurfaceBelow(b Box, gap float64) (float64, uint16, bool) {
	low := b.MinY - gap
	x1 := int(math.Floor(b.MinX))
	z1 := int(math.Floor(b.MinZ))
	x2 := int(math.Floor(b.MaxX))
	z2 := int(math.Floor(b.MaxZ))
	y1 := int(math.Floor(low - shapeMaxRise))
	y2 := int(math.Floor(b.MinY))
	best, bestState, found := 0.0, uint16(0), false
	for bx := x1; bx <= x2; bx++ {
		for by := y1; by <= y2; by++ {
			for bz := z1; bz <= z2; bz++ {
				state := w.BlockAt(bx, by, bz)
				for _, s := range registry.BlockStateShape(state) {
					sx1 := float64(bx) + float64(s.X1)/16
					sy1 := float64(by) + float64(s.Y1)/16
					sz1 := float64(bz) + float64(s.Z1)/16
					sx2 := float64(bx) + float64(s.X2)/16
					top := float64(by) + float64(s.Y2)/16
					sz2 := float64(bz) + float64(s.Z2)/16
					if b.MinX > sx2 || b.MaxX < sx1 || b.MinZ > sz2 || b.MaxZ < sz1 {
						continue
					}
					if top < low || top > b.MinY+touchEpsilon {
						continue
					}
					if sy1 > b.MinY+touchEpsilon {
						continue // 形状整体高于脚底（属于侧向形状）
					}
					if !found || top > best {
						best, bestState, found = top, state, true
					}
				}
			}
		}
	}
	return best, bestState, found
}
