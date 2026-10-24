//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"

	"gmcs/internal/world"
)

// unstickPlayerPosition 把与方块碰撞形状重叠的玩家位置推到最近的空位。
//
// 原版在放置玩家（首次进入世界 / 恢复存档 / 重生）时，玩家包围盒始终不会
// 与方块碰撞形状相交。gmcs 恢复旧存档位置、或出生点被后来生成/放置的方块
// 覆盖时如果不做同样的校验，玩家的包围盒会嵌在方块里：客户端每次发出的
// 移动都会因“穿墙”被服务器拒绝，表现为完全无法移动。
//
// 返回修正后的坐标以及是否发生了修正；无法修正时原样返回 false。
func unstickPlayerPosition(w *world.World, x, y, z float64) (float64, float64, float64, bool) {
	if w == nil || positionClear(w, x, y, z) {
		return x, y, z, false
	}
	// 优先向上推（被新放置的方块、雪层或抬高的地形埋住）。
	for dy := 1; dy <= 4; dy++ {
		if positionClear(w, x, y+float64(dy), z) {
			return x, y + float64(dy), z, true
		}
	}
	// 再在原位置附近逐圈搜索可站立且有支撑面的列。
	for radius := 0; radius <= 4; radius++ {
		for dx := -radius; dx <= radius; dx++ {
			for dz := -radius; dz <= radius; dz++ {
				if radius > 0 && dx != -radius && dx != radius && dz != -radius && dz != radius {
					continue
				}
				nx, nz := x+float64(dx), z+float64(dz)
				top, _, supported := w.SurfaceBelow(playerBox(nx, y+2, nz), 6)
				if !supported {
					continue
				}
				ny := math.Ceil(top)
				if positionClear(w, nx, ny, nz) {
					return nx, ny, nz, true
				}
			}
		}
	}
	return x, y, z, false
}
