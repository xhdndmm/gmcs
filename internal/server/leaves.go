//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"strings"

	"gmcs/internal/item"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 树叶衰减（原版 LeavesBlock）：
//   - 玩家放置的树叶是 persistent=true（`getStateForPlacement`），不会衰减；
//   - 自然生成的树叶（persistent=false）在原版用 distance 属性扩散
//     （updateShape → 计划刻 2 tick → updateDistance），6 格内没有原木可达时
//     distance 升到 7，下一次随机刻/计划刻即掉落消失；
//   - 衰减掉落沿用方块战利品表（树苗/木棍/苹果等，见 block_drops.go）。
//
// gmcs 的近似：不维护 distance 属性，而是在原木/树叶被移除（或爆炸摧毁）时
// 对周围 15³ 内的自然树叶做一次“6 格内是否有原木”的判定，没有则立即衰减。
// 相比原版最多约 14 tick 的连锁，表现是砍完树后树叶立刻消失。

// leafDecayRadius 是树叶判定半径（原版 distance ≤ 6 依附原木、到 7 即衰减）。
const leafDecayRadius = 6

// isLogState 报告方块状态是否属于 minecraft:logs 标签（含去皮原木/菌柄）。
func isLogState(state uint16) bool {
	return registry.BlockTagContains("minecraft:logs", state)
}

// isLeafName 报告方块名是否为树叶。
func isLeafName(name string) bool {
	return strings.HasPrefix(name, "minecraft:") && strings.HasSuffix(name, "_leaves")
}

// leafPersistentState 返回树叶方块“玩家放置”状态
// （persistent=true、distance=7、waterlogged=false）。
func leafPersistentState(name string) (uint16, bool) {
	state, ok := registry.LeafPersistentStates[name]
	return state, ok
}

// isLeafState 报告方块状态是否为树叶（含玩家放置的 persistent 变体）。
func isLeafState(state uint16) bool {
	name, ok := registry.BlockNameByState(state)
	return ok && isLeafName(name)
}

// isNaturalLeafState 报告该状态是否为自然生成（非 persistent）的树叶。
func isNaturalLeafState(state uint16) bool {
	name, ok := registry.BlockNameByState(state)
	if !ok || !isLeafName(name) {
		return false
	}
	if persistent, ok := leafPersistentState(name); ok && state == persistent {
		return false
	}
	return true
}

// decayLeavesAround 处理方块被移除后的树叶衰减：只有被移除的方块是原木或
// 树叶时才扫描周围；6 格内没有原木的自然树叶立即衰减。
func (s *Server) decayLeavesAround(dim world.Dimension, w *world.World, x, y, z int, removed uint16) {
	if w == nil || (!isLogState(removed) && !isLeafState(removed)) {
		return
	}
	r := leafDecayRadius
	var leaves, logs [][3]int
	for dx := -r; dx <= r; dx++ {
		for dy := -r; dy <= r; dy++ {
			for dz := -r; dz <= r; dz++ {
				bx, by, bz := x+dx, y+dy, z+dz
				if _, ok := world.SectionIndex(by); !ok {
					continue
				}
				if !w.ChunkLoaded(bx, bz) {
					continue
				}
				state := w.BlockAt(bx, by, bz)
				switch {
				case isLogState(state):
					logs = append(logs, [3]int{bx, by, bz})
				case isNaturalLeafState(state):
					leaves = append(leaves, [3]int{bx, by, bz})
				}
			}
		}
	}
	for _, pos := range leaves {
		supported := false
		for _, log := range logs {
			if absInt(pos[0]-log[0]) <= r && absInt(pos[1]-log[1]) <= r && absInt(pos[2]-log[2]) <= r {
				supported = true
				break
			}
		}
		if !supported {
			s.decayLeafAt(dim, w, pos[0], pos[1], pos[2])
		}
	}
}

// decayLeafAt 让单个树叶位置衰减：按方块战利品表掉落并移除。
// 由 Tick goroutine（爆炸）或读循环（破坏方块）调用。
func (s *Server) decayLeafAt(dim world.Dimension, w *world.World, x, y, z int) {
	state := w.BlockAt(x, y, z)
	if !isNaturalLeafState(state) {
		return
	}
	if !w.SetBlock(x, y, z, world.AirBlock) {
		return
	}
	s.broadcastBlockUpdate(dim, x, y, z, int32(world.AirBlock))
	s.updateRedstoneAround(dim, x, y, z)
	// 原版树叶衰减使用方块战利品表（空手 = 无精准采集；时运仍然生效）。
	s.dropBlockItem(dim, state, x, y, z, item.Empty())
}
