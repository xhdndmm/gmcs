//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"strconv"
	"strings"

	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 火方块（打火石/发射器点燃；简化实现，见 docs/TODO.md）。
//
//   - 放置规则（近似原版 BaseFireBlock.canBePlacedAt 的地面分支）：目标位置
//     为空或可替换，下方是完整固体方块或可燃方块；
//   - 引燃来源：打火石（玩家 useOn 或发射器）与 TNT 点燃；
//   - 生命周期（近似原版 FireBlock.tick）：每 30–39 tick 推进一次 age，
//     age 到 15 后检查周围（含下方）是否还有可燃方块，没有则熄灭；
//   - 不做火焰蔓延（不点燃相邻可燃方块）、不因下雨/细雪熄灭。
const (
	fireBlockName     = "minecraft:fire"
	soulFireBlockName = "minecraft:soul_fire"
	// fireMaxAge 是火的最大 age 属性值（原版 15）。
	fireMaxAge = 15
)

// fireFlammableNames 是可燃方块名（火焰存续判定用的近似集合，见文件头注释）。
var fireFlammableNames = map[string]bool{
	"minecraft:coal_block":       true,
	"minecraft:hay_block":        true,
	"minecraft:bookshelf":        true,
	"minecraft:tnt":              true,
	"minecraft:dead_bush":        true,
	"minecraft:short_grass":      true,
	"minecraft:tall_grass":       true,
	"minecraft:fern":             true,
	"minecraft:large_fern":       true,
	"minecraft:bamboo":           true,
	"minecraft:scaffolding":      true,
	"minecraft:dried_kelp_block": true,
}

// fireFlammableSuffixes 是按名称后缀判定的可燃方块（木板/原木/树叶/羊毛等）。
var fireFlammableSuffixes = []string{
	"_planks", "_log", "_wood", "_leaves", "_wool", "_carpet", "_fence", "_fence_gate",
	"_stairs", "_slab", "_door", "_trapdoor", "_sign", "_button", "_pressure_plate",
}

// isFlammableBlockName 报告方块名是否可燃（近似集合）。
func isFlammableBlockName(name string) bool {
	if fireFlammableNames[name] {
		return true
	}
	if name == "minecraft:oak_sapling" || name == "minecraft:spruce_sapling" ||
		name == "minecraft:birch_sapling" || name == "minecraft:jungle_sapling" ||
		name == "minecraft:acacia_sapling" || name == "minecraft:dark_oak_sapling" {
		return true
	}
	for _, suffix := range fireFlammableSuffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// fireBaseAt 报告 (x, y, z) 是否可以支撑火（下方完整固体或可燃方块）。
func (s *Server) fireBaseAt(w *world.World, x, y, z int) bool {
	if _, ok := world.SectionIndex(y - 1); !ok {
		return false
	}
	below := w.BlockAt(x, y-1, z)
	if name, ok := s.blockNameOf(below); ok && isFlammableBlockName(name) {
		return true
	}
	return fullCubeState(below)
}

// fireStateForAge 构造火焰方块状态（含 5 个连接面属性，默认全部 true）。
func fireStateForAge(name string, age int) (uint16, bool) {
	return registry.BlockStateWithProps(name, map[string]string{
		"age":   strconv.Itoa(age),
		"east":  "true",
		"north": "true",
		"south": "true",
		"up":    "true",
		"west":  "true",
	})
}

// placeFireAt 在 (x, y, z) 放置火（目标可替换且下方可支撑时）；成功返回 true。
func (s *Server) placeFireAt(w *world.World, dim world.Dimension, x, y, z int) bool {
	if _, ok := world.SectionIndex(y); !ok {
		return false
	}
	existing := w.BlockAt(x, y, z)
	if existing != world.AirBlock {
		// 可替换的植被可以被火覆盖（近似原版 replaceable 判定）。
		name, ok := s.blockNameOf(existing)
		if !ok || !isFlammableBlockName(name) {
			return false
		}
	}
	if !s.fireBaseAt(w, x, y, z) {
		return false
	}
	state, ok := fireStateForAge(fireBlockName, 0)
	if !ok {
		return false
	}
	if !w.SetBlock(x, y, z, state) {
		return false
	}
	s.broadcastBlockUpdate(dim, x, y, z, int32(state))
	s.updateRedstoneAround(dim, x, y, z)
	s.scheduleRedstoneTick(dim, [3]int{x, y, z}, fireTickDelay(s))
	return true
}

// fireTickDelay 返回下一次火焰推进的延迟（原版 30 + rand(0..9)）。
func fireTickDelay(s *Server) int {
	return 30 + int(s.nextRandom()%10)
}

// tryUseFlintAndSteelFire 处理打火石的常规使用（原版 FlintAndSteelItem.useOn
// 的非营火分支）：点击 TNT 时点燃 TNT，否则在被点击面外侧尝试放置火。
// 返回 true 表示已处理（调用方跳过后续流程）。
func (s *Server) tryUseFlintAndSteelFire(player *session, w *world.World, use protocol.UseItemOn, current uint16) bool {
	dim := w.Dimension()
	if name, ok := s.blockNameOf(current); ok && name == "minecraft:tnt" {
		if !s.primeTntBlock(dim, w, use.X, use.Y, use.Z) {
			return false
		}
		s.broadcastToNearby(dim,
			protocol.EncodeSoundEffect(s.soundFlintUse, protocol.SoundCategoryBlock,
				float64(use.X)+0.5, float64(use.Y)+0.5, float64(use.Z)+0.5, 1, 1, 0),
			float64(use.X), float64(use.Z), s.playerSnapshot())
		return true
	}
	if use.Direction < 0 || int(use.Direction) >= len(blockFaceOffsets) {
		return false
	}
	offset := blockFaceOffsets[use.Direction]
	tx, ty, tz := use.X+offset[0], use.Y+offset[1], use.Z+offset[2]
	if !s.placeFireAt(w, dim, tx, ty, tz) {
		return false
	}
	s.broadcastToNearby(dim,
		protocol.EncodeSoundEffect(s.soundFlintUse, protocol.SoundCategoryBlock,
			float64(tx)+0.5, float64(ty)+0.5, float64(tz)+0.5, 1, 1, 0),
		float64(tx), float64(tz), s.playerSnapshot())
	return true
}

// tickFire 推进火焰 age：增长到 15 后无可燃邻居时熄灭。
func (s *Server) tickFire(w *world.World, dim world.Dimension, pos [3]int, state uint16) {
	name, props, ok := registry.StateProps(state)
	if !ok || (name != fireBlockName && name != soulFireBlockName) {
		return
	}
	age, err := strconv.Atoi(props["age"])
	if err != nil {
		age = 0
	}
	if !s.fireBaseAt(w, pos[0], pos[1], pos[2]) {
		s.removeFire(w, dim, pos)
		return
	}
	next := cloneProps(props)
	if age < fireMaxAge {
		next["age"] = strconv.Itoa(age + 1)
		if updated, ok := registry.BlockStateWithProps(name, next); ok {
			if w.SetBlock(pos[0], pos[1], pos[2], updated) {
				s.broadcastBlockUpdate(dim, pos[0], pos[1], pos[2], int32(updated))
			}
		}
		s.scheduleRedstoneTick(dim, pos, fireTickDelay(s))
		return
	}
	if s.fireFuelNearby(w, pos) {
		next["age"] = "0"
		if updated, ok := registry.BlockStateWithProps(name, next); ok {
			if w.SetBlock(pos[0], pos[1], pos[2], updated) {
				s.broadcastBlockUpdate(dim, pos[0], pos[1], pos[2], int32(updated))
			}
		}
		s.scheduleRedstoneTick(dim, pos, fireTickDelay(s))
		return
	}
	s.removeFire(w, dim, pos)
}

// fireFuelNearby 报告火周围（六邻域与下方）是否还有可燃方块。
func (s *Server) fireFuelNearby(w *world.World, pos [3]int) bool {
	candidates := append([][3]int{{pos[0], pos[1] - 1, pos[2]}}, neighborsOf(pos)...)
	for _, candidate := range candidates {
		if _, ok := world.SectionIndex(candidate[1]); !ok {
			continue
		}
		if name, ok := s.blockNameOf(w.BlockAt(candidate[0], candidate[1], candidate[2])); ok && isFlammableBlockName(name) {
			return true
		}
	}
	return false
}

// removeFire 移除火方块并广播（水流灌入、燃料耗尽或失去支撑时）。
func (s *Server) removeFire(w *world.World, dim world.Dimension, pos [3]int) {
	current := w.BlockAt(pos[0], pos[1], pos[2])
	name, _, ok := registry.StateProps(current)
	if !ok || (name != fireBlockName && name != soulFireBlockName) {
		return
	}
	if !w.SetBlock(pos[0], pos[1], pos[2], world.AirBlock) {
		return
	}
	s.broadcastBlockUpdate(dim, pos[0], pos[1], pos[2], int32(world.AirBlock))
	s.updateRedstoneAround(dim, pos[0], pos[1], pos[2])
}
