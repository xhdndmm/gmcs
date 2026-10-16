//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"strconv"
	"sync"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 火方块：打火石/发射器点燃，tick 逻辑按原版 FireBlock 反汇编移植。
//
// 对照来源：官方 1.21.11 服务端 jar（FireBlock = ecv、BaseFireBlock = dzd、
// SoulFireBlock = ehq、ServerLevel.canSpreadFireAround、ChunkMap.anyPlayerCloseEnoughTo、
// EnvironmentAttributes.INCREASED_FIRE_BURNOUT）；可燃数值表见
// internal/registry/fire_flammables.go。
//
//   - 放置（原版 FlintAndSteelItem.useOn → BaseFireBlock.canBePlacedAt/getState）：
//     目标必须是空气且放置的火焰状态能存活；下方属于
//     minecraft:soul_fire_base_blocks（灵魂沙/灵魂土）时放置灵魂火；
//   - 排程：放置时与每次 tick 都按 getFireTickDelay = 30 + rand(0..9) 重新排程；
//     灵魂火没有 tick（原版 SoulFireBlock 未覆写 tick），不推进、不蔓延、不自行熄灭；
//   - 玩家半径（原版 1.21.11 新增游戏规则 fire_spread_radius_around_player，默认 128）：
//     128 格内存在非旁观玩家时火焰才推进/蔓延，否则只重新排程
//     （远处火焰既不熄灭也不蔓延；区块未加载时同样只重新排程）；
//   - 普通火每次推进：age += rand(0..2)/2（上限 15）；六邻域内无可燃方块时，
//     下方顶面完整且 age ≤ 3 才保留，否则熄灭；有可燃方块时 age 到 15 后
//     每 tick 有 1/4 概率在“下方不是可燃方块”时熄灭；
//   - 燃烧判定（checkBurnOut）：对自身六邻域分别按 300/300/250/250/300/300 的
//     基数判定，命中后按 rand(age+10) < 5 决定“点燃该方块（age + rand(0..4)/4）”
//     或“直接烧毁（移除）”；被点燃方块是 TNT 时点燃 TNT；
//   - 蔓延：3×3×6 区域内以空气为目标，按周围可燃度、自身 age 与难度决定是否起火
//     （公式与随机数消耗顺序见 tickFire）；
//   - 群系属性（原版环境属性 minecraft:gameplay/increased_fire_burnout，原版在
//     蘑菇岛/尖峭山峰/沼泽/红树林沼泽/雪坡/竹林/冰封山峰/丛林为 true）：
//     燃烧判定基数 −50、蔓延概率减半；gmcs 只在已加载区块中查询群系，
//     对应 gmcs 会生成的群系为沼泽、丛林、雪坡、冰封山峰（其余 4 个原版群系不生成）；
//   - 已知偏差（gmcs 无对应系统）：没有天气（原版下雨会熄灭火焰或阻止点燃，
//     本实现从不因下雨熄灭/阻止）、没有难度设置（按原版默认 Easy、难度 ID 1；
//     原版 Hard 会显著提高蔓延概率）、连接面属性恒为 true（仅影响客户端渲染）、
//     没有传送门（原版火焰可放置在传送门附近）、空气判定只认 air（原版还含
//     cave_air/void_air）。
const (
	fireBlockName     = "minecraft:fire"
	soulFireBlockName = "minecraft:soul_fire"
	// fireMaxAge 是火的最大 age 属性值（原版 15）。
	fireMaxAge = 15
	// fireDifficultyID 是原版 Difficulty.getId() 的取值（Peaceful=0/Easy=1/Normal=2/Hard=3）。
	// gmcs 暂无难度设置，按原版 server.properties 默认难度 Easy 处理。
	fireDifficultyID = 1
	// fireSpreadRadiusAroundPlayer 是原版规则 fire_spread_radius_around_player 的默认值
	// （128 格；-1 表示不限制；gmcs 无游戏规则，按默认值处理）。
	fireSpreadRadiusAroundPlayer = 128.0
	// fireIncreasedBurnoutReduction 是群系属性开启时燃烧判定基数的减少量（原版 -50）。
	fireIncreasedBurnoutReduction = 50
)

// firePlacementState 返回在 (x, y, z) 放置火焰时应使用的方块状态
// （原版 BaseFireBlock.getState + FireBlock.getStateForPlacement）：
// 下方属于 minecraft:soul_fire_base_blocks 时为灵魂火（没有 age 属性），
// 否则为 age 指定的普通火。连接面属性（east/north/south/up/west）近似的
// 取 true，只影响客户端渲染。
func (s *Server) firePlacementState(w *world.World, x, y, z, age int) (uint16, string, bool) {
	if _, ok := world.SectionIndex(y - 1); ok {
		if registry.BlockTagContains("minecraft:soul_fire_base_blocks", w.BlockAt(x, y-1, z)) {
			state, ok := registry.BlockStateWithProps(soulFireBlockName, nil)
			return state, soulFireBlockName, ok
		}
	}
	state, ok := fireStateForAge(fireBlockName, age)
	return state, fireBlockName, ok
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

// fireCanSurvive 报告火焰在当前位置能否存活（原版 FireBlock.canSurvive /
// SoulFireBlock.canSurvive）：普通火要求下方顶面完整或六邻域内有可燃方块；
// 灵魂火要求下方属于 minecraft:soul_fire_base_blocks。
func (s *Server) fireCanSurvive(w *world.World, name string, pos [3]int) bool {
	if _, ok := world.SectionIndex(pos[1] - 1); !ok {
		return false
	}
	below := w.BlockAt(pos[0], pos[1]-1, pos[2])
	if name == soulFireBlockName {
		return registry.BlockTagContains("minecraft:soul_fire_base_blocks", below)
	}
	return s.faceSturdy(below, "up") || s.fireNearFuel(w, pos)
}

// placeFireAt 按原版打火石/发射器的放置规则在 (x, y, z) 放置火焰：
// 目标必须是空气，且放置的火焰状态必须能存活（原版 BaseFireBlock.canBePlacedAt）；
// 下方为灵魂沙/灵魂土时放置灵魂火。成功返回 true。
func (s *Server) placeFireAt(w *world.World, dim world.Dimension, x, y, z int) bool {
	if _, ok := world.SectionIndex(y); !ok {
		return false
	}
	if w.BlockAt(x, y, z) != world.AirBlock {
		return false
	}
	state, name, ok := s.firePlacementState(w, x, y, z, 0)
	if !ok || !s.fireCanSurvive(w, name, [3]int{x, y, z}) {
		return false
	}
	if !w.SetBlock(x, y, z, state) {
		return false
	}
	s.broadcastBlockUpdate(dim, x, y, z, int32(state))
	s.updateRedstoneAround(dim, x, y, z)
	if name == fireBlockName {
		// 灵魂火没有 tick（原版 SoulFireBlock 不排程）。
		s.scheduleRedstoneTick(dim, [3]int{x, y, z}, fireTickDelay(s))
	}
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

// fireNeighborOffsets 是原版 Direction.values() 的六邻域顺序
// （DOWN、UP、NORTH、SOUTH、WEST、EAST）；保持一致可让随机数消耗顺序与原版相同。
var fireNeighborOffsets = [6][3]int{{0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0}}

// fireIsFuel 报告方块状态是否为燃料（原版 FireBlock.isFuel：鼓励值 > 0）。
func (s *Server) fireIsFuel(state uint16) bool {
	encouragement, _ := registry.FireFlammability(state)
	return encouragement > 0
}

// fireNearFuel 报告位置六邻域内是否存在可燃方块（原版 FireBlock.isNearFuel）。
func (s *Server) fireNearFuel(w *world.World, pos [3]int) bool {
	for _, offset := range fireNeighborOffsets {
		y := pos[1] + offset[1]
		if _, ok := world.SectionIndex(y); !ok {
			continue
		}
		if s.fireIsFuel(w.BlockAt(pos[0]+offset[0], y, pos[2]+offset[2])) {
			return true
		}
	}
	return false
}

// fireIgniteOdds 返回目标位置的引燃值（原版 FireBlock.getIgniteOdds）：
// 目标不是空气时为 0，否则取六邻域可燃度的最大值。
func (s *Server) fireIgniteOdds(w *world.World, x, y, z int) int {
	if w.BlockAt(x, y, z) != world.AirBlock {
		return 0
	}
	odds := 0
	for _, offset := range fireNeighborOffsets {
		ny := y + offset[1]
		if _, ok := world.SectionIndex(ny); !ok {
			continue
		}
		encouragement, _ := registry.FireFlammability(w.BlockAt(x+offset[0], ny, z+offset[2]))
		if encouragement > odds {
			odds = encouragement
		}
	}
	return odds
}

// fireInfiniburnTag 返回维度对应的“永久燃烧”方块标签（原版 dimensionType.infiniburn()）。
func fireInfiniburnTag(dim world.Dimension) string {
	switch dim {
	case world.DimensionNether:
		return "minecraft:infiniburn_nether"
	case world.DimensionEnd:
		return "minecraft:infiniburn_end"
	default:
		return "minecraft:infiniburn_overworld"
	}
}

// fireSpreadAllowed 报告该位置是否允许火焰推进与蔓延
// （原版 ServerLevel.canSpreadFireAround → 规则 fire_spread_radius_around_player，
// 默认 128 格；ChunkMap.anyPlayerCloseEnoughTo 用玩家实体位置与方块角点的三维
// 欧氏距离与半径比较，旁观者不计入）。gmcs 无游戏规则，按默认值处理。
func (s *Server) fireSpreadAllowed(dim world.Dimension, pos [3]int) bool {
	radiusSquared := fireSpreadRadiusAroundPlayer * fireSpreadRadiusAroundPlayer
	for _, player := range s.playerSnapshot() {
		if player.dimensionID() != dim || player.gameModeID() == uint8(config.GameModeSpectator) {
			continue
		}
		x, y, z, _, _ := player.playerPosition()
		dx := x - float64(pos[0])
		dy := y - float64(pos[1])
		dz := z - float64(pos[2])
		if dx*dx+dy*dy+dz*dz < radiusSquared {
			return true
		}
	}
	return false
}

// fireIncreasedBurnoutBiomes 是设置了原版环境属性
// minecraft:gameplay/increased_fire_burnout 的群系中 gmcs 会生成的那部分。
var fireIncreasedBurnoutBiomes = sync.OnceValue(func() map[uint16]bool {
	// 原版另有 mushroom_fields、jagged_peaks、mangrove_swamp、bamboo_jungle，
	// gmcs 的世界生成不会产生这些群系。
	names := []string{
		"minecraft:swamp",
		"minecraft:jungle",
		"minecraft:snowy_slopes",
		"minecraft:frozen_peaks",
	}
	biomes := make(map[uint16]bool, len(names))
	for _, name := range names {
		if id, ok := registry.SyncEntryID("minecraft:worldgen/biome", name); ok {
			biomes[uint16(id)] = true
		}
	}
	return biomes
})

// fireIncreasedBurnout 报告位置所在群系是否设置了原版环境属性
// minecraft:gameplay/increased_fire_burnout（区块未加载时按 false 处理）。
func (s *Server) fireIncreasedBurnout(w *world.World, pos [3]int) bool {
	biome, ok := w.BiomeAt(pos[0], pos[2])
	if !ok {
		return false
	}
	return fireIncreasedBurnoutBiomes()[biome]
}

// fireCheckBurnOut 处理火焰对相邻方块的燃烧判定（原版 FireBlock.checkBurnOut）：
// chance 是判定基数（水平 300、垂直 250，群系属性开启时整体再减 50），
// age 是当前火焰的 age。
func (s *Server) fireCheckBurnOut(w *world.World, dim world.Dimension, pos [3]int, chance, age int) {
	if _, ok := world.SectionIndex(pos[1]); !ok {
		return
	}
	_, flammability := registry.FireFlammability(w.BlockAt(pos[0], pos[1], pos[2]))
	// 原版无论可燃度是否为 0 都会消耗一次随机数，这里保持一致。
	if s.nextInt(chance) >= flammability {
		return
	}
	target := w.BlockAt(pos[0], pos[1], pos[2])
	// 原版此处还有“正在下雨且附近下雨 → 直接烧毁”的短路；gmcs 无天气，恒走点燃分支。
	if s.nextInt(age+10) < 5 {
		newAge := age + s.nextInt(5)/4
		if newAge > fireMaxAge {
			newAge = fireMaxAge
		}
		if state, _, ok := s.firePlacementState(w, pos[0], pos[1], pos[2], newAge); ok {
			if w.SetBlock(pos[0], pos[1], pos[2], state) {
				s.broadcastBlockUpdate(dim, pos[0], pos[1], pos[2], int32(state))
				s.updateRedstoneAround(dim, pos[0], pos[1], pos[2])
			}
		}
	} else if w.SetBlock(pos[0], pos[1], pos[2], world.AirBlock) {
		s.broadcastBlockUpdate(dim, pos[0], pos[1], pos[2], int32(world.AirBlock))
		s.updateRedstoneAround(dim, pos[0], pos[1], pos[2])
	}
	// 原版：被点燃的方块是 TNT 时点燃 TNT（FireBlock.checkBurnOut → TntBlock.primeTnt）。
	if name, ok := s.blockNameOf(target); ok && name == "minecraft:tnt" {
		s.primeTntBlock(dim, w, pos[0], pos[1], pos[2])
	}
}

// tickFire 是普通火方块的一次计划刻（原版 FireBlock.tick 的完整移植）。
func (s *Server) tickFire(w *world.World, dim world.Dimension, pos [3]int, state uint16) {
	name, props, ok := registry.StateProps(state)
	if !ok {
		return
	}
	if name == soulFireBlockName {
		// 原版 SoulFireBlock 没有 tick：灵魂火不推进 age、不蔓延、不自行熄灭，
		// 这里不再重新排程（失去支撑由邻接方块更新路径处理）。
		return
	}
	if name != fireBlockName {
		return
	}
	// 原版 tick 第一步就重新排程（无论后续走哪个分支）。
	s.scheduleRedstoneTick(dim, pos, fireTickDelay(s))
	if !w.ChunkLoaded(pos[0], pos[2]) {
		// 区块未加载时不做推进（原版只 tick 已加载区块），保留排程等待加载。
		return
	}
	if !s.fireSpreadAllowed(dim, pos) {
		// 原版：玩家半径之外只重新排程，不推进、不蔓延。
		return
	}
	s.fireTickBody(w, dim, pos, props)
}

// fireTickBody 执行一次火焰推进（原版 FireBlock.tick 在 canSurvive/年龄/
// 燃烧判定/蔓延部分的行为；区块与玩家半径门控在 tickFire 中）。
func (s *Server) fireTickBody(w *world.World, dim world.Dimension, pos [3]int, props map[string]string) {
	if !s.fireCanSurvive(w, fireBlockName, pos) {
		// 原版：失去支撑时先移除，然后继续执行后面的分支（可能以新 age 重新放置）。
		s.removeFire(w, dim, pos)
	}
	var belowID uint16
	if _, ok := world.SectionIndex(pos[1] - 1); ok {
		belowID = w.BlockAt(pos[0], pos[1]-1, pos[2])
	}
	infiniburn := registry.BlockTagContains(fireInfiniburnTag(dim), belowID)
	age, err := strconv.Atoi(props["age"])
	if err != nil {
		age = 0
	}
	// 原版在此处检查“下雨且附近下雨 → 有 0.2 + age × 0.03 概率熄灭”；gmcs 无天气。
	newAge := age + s.nextInt(3)/2
	if newAge > fireMaxAge {
		newAge = fireMaxAge
	}
	if newAge != age {
		if updated, ok := fireStateForAge(fireBlockName, newAge); ok && w.SetBlock(pos[0], pos[1], pos[2], updated) {
			s.broadcastBlockUpdate(dim, pos[0], pos[1], pos[2], int32(updated))
		}
	}
	increasedBurnout := s.fireIncreasedBurnout(w, pos)
	if !infiniburn {
		if !s.fireNearFuel(w, pos) {
			// 原版：下方顶面不再完整，或顶面完整但 age 已超过 3 → 熄灭；
			// 只有“下方顶面完整且 age ≤ 3”的无燃料火焰才继续燃烧。
			if !s.faceSturdy(belowID, "up") || age > 3 {
				s.removeFire(w, dim, pos)
			}
			return
		}
		if age == fireMaxAge && s.nextInt(4) == 0 && !s.fireIsFuel(belowID) {
			s.removeFire(w, dim, pos)
			return
		}
	}
	// 燃烧判定：原版顺序为 EAST、WEST、下方、上方、NORTH、SOUTH，
	// 基数为 300/300/250/250/300/300（群系属性开启时整体 -50）。
	chance := 300
	if increasedBurnout {
		chance -= fireIncreasedBurnoutReduction
	}
	s.fireCheckBurnOut(w, dim, [3]int{pos[0] + 1, pos[1], pos[2]}, chance, age)
	s.fireCheckBurnOut(w, dim, [3]int{pos[0] - 1, pos[1], pos[2]}, chance, age)
	s.fireCheckBurnOut(w, dim, [3]int{pos[0], pos[1] - 1, pos[2]}, chance-fireIncreasedBurnoutReduction, age)
	s.fireCheckBurnOut(w, dim, [3]int{pos[0], pos[1] + 1, pos[2]}, chance-fireIncreasedBurnoutReduction, age)
	s.fireCheckBurnOut(w, dim, [3]int{pos[0], pos[1], pos[2] - 1}, chance, age)
	s.fireCheckBurnOut(w, dim, [3]int{pos[0], pos[1], pos[2] + 1}, chance, age)
	// 蔓延：原版循环顺序为 x → z → y（影响随机数消耗顺序，保持一致）。
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			for dy := -1; dy <= 4; dy++ {
				if dx == 0 && dy == 0 && dz == 0 {
					continue
				}
				tx, ty, tz := pos[0]+dx, pos[1]+dy, pos[2]+dz
				if _, ok := world.SectionIndex(ty); !ok {
					continue
				}
				// 原版：越往上越难蔓延（dy > 1 时每格基数 +100）。
				spreadChance := 100
				if dy > 1 {
					spreadChance += (dy - 1) * 100
				}
				fuel := s.fireIgniteOdds(w, tx, ty, tz)
				if fuel <= 0 {
					continue
				}
				// 原版公式：(可燃度 + 40 + 难度 × 7) / (age + 30)，群系属性开启时再除以 2。
				odds := (fuel + 40 + fireDifficultyID*7) / (age + 30)
				if increasedBurnout {
					odds /= 2
				}
				if odds <= 0 {
					continue
				}
				if s.nextInt(spreadChance) > odds {
					continue
				}
				// 原版此处还有“正在下雨且目标附近下雨 → 不点燃”的短路；gmcs 无天气。
				targetAge := age + s.nextInt(5)/4
				if targetAge > fireMaxAge {
					targetAge = fireMaxAge
				}
				if state, _, ok := s.firePlacementState(w, tx, ty, tz, targetAge); ok {
					if w.SetBlock(tx, ty, tz, state) {
						s.broadcastBlockUpdate(dim, tx, ty, tz, int32(state))
						s.updateRedstoneAround(dim, tx, ty, tz)
					}
				}
			}
		}
	}
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
