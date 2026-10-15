//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"strconv"

	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 流体模拟：水与岩浆的扩散（对照 1.21.11 反编译的 FlowingFluid/WaterFluid/
// LavaFluid/LiquidBlock 实现）。
//
// 方块状态编码与原版一致：level 0 = 源方块（amount 8），level 1–7 = 流动
// （amount = 8 − level，1 最厚），level ≥ 8 = 下落流体。
//
// 实现语义（逐字节码核对）：
//   - 计划刻延迟：水 5 tick；岩浆主世界 30、下界 10；
//   - dropOff：水 1；岩浆主世界 2、下界 1；slope 搜索距离：水 4；岩浆主世界 2、下界 4；
//   - tick：非源方块先重算（getNewLiquid：相邻两个同源 + 下方固体 → 变源
//     （无限水）；下方同流体 → 下落 level 8；否则 level = 相邻最大 amount −
//     dropOff；≤0 则消失）；然后按 spread 扩散：下方可扩散优先向下（并有
//     3+ 源邻居时同时向侧面），否则（源方块，或下方不是“水孔”）向侧面扩散；
//   - 侧向扩散按原版 getSpread/getSlopeDistance：只在能到达下落点的最短
//     路径方向、且在 slope 搜索距离内扩散；
//   - 砖石交互：岩浆方块的邻居更新里检查上方与水平的同流体；
//     水源 + 岩浆 → 黑曜石、流动水 + 岩浆 → 圆石（灵魂土 + 蓝冰 → 玄武岩）
//     并播放 1501 咝咝声；
//   - 水在下界立即蒸发（不放置/移除）；
//   - 桶：空桶收集源流体，水桶/岩浆桶放置源流体（下界倒水蒸发）。
//
// 已知简化（见 docs/TODO.md）：
//   - 不支持含水方块（waterlogged）：流体不进入楼梯/台阶等可含水方块，
//     桶不能给这类方块填充/取出流体；
//   - 流体摧毁可替换方块（草/花/火把/红石线等）时不产生掉落物
//     （原版会掉落）；
//   - 岩浆不点燃相邻可燃方块、不随机造成伤害（伤害由环境的岩浆方块处理）；
//   - canPassThroughWall 的面遮挡检查简化为“目标可被流体替换即通过”
//     （对完整/常见方块结果一致）；
//   - 水不因下雨/细雪等外部因素更新。

// waterBlockName / lavaBlockName 是流体方块名。
const (
	waterBlockName = "minecraft:water"
	lavaBlockName  = "minecraft:lava"
)

// isWaterState 报告状态是否为任意等级的水。
func isWaterState(state uint16) bool {
	return registry.IsPropertyBlockState(waterBlockName, state)
}

// isLavaState 报告状态是否为任意等级的岩浆。
func isLavaState(state uint16) bool {
	return registry.IsPropertyBlockState(lavaBlockName, state)
}

// fluidInfo 描述一个流体方块状态。
type fluidInfo struct {
	name    string
	source  bool
	falling bool
	// amount 是原版 FluidState.getAmount()：源 8、流动 8−level、下落 8。
	amount int
}

// fluidInfoOf 解析水/岩浆方块状态（非流体返回 false）。
func fluidInfoOf(state uint16) (fluidInfo, bool) {
	name, props, ok := registry.StateProps(state)
	if !ok || (name != waterBlockName && name != lavaBlockName) {
		return fluidInfo{}, false
	}
	level, err := strconv.Atoi(props["level"])
	if err != nil {
		return fluidInfo{}, false
	}
	info := fluidInfo{name: name}
	switch {
	case level == 0:
		info.source = true
		info.amount = 8
	case level >= 8:
		info.falling = true
		info.amount = 8
	default:
		info.amount = 8 - level
	}
	return info, true
}

// fluidSourceState 返回指定流体的源方块状态（level 0）。
func fluidSourceState(name string) (uint16, bool) {
	return registry.BlockStateWithProps(name, map[string]string{"level": "0"})
}

// fluidFallingState 返回指定流体的下落状态（level 8）。
func fluidFallingState(name string) (uint16, bool) {
	return registry.BlockStateWithProps(name, map[string]string{"level": "8"})
}

// fluidFlowingState 返回指定流体按 amount（1–7）对应的流动状态。
func fluidFlowingState(name string, amount int) (uint16, bool) {
	if amount <= 0 || amount > 7 {
		return 0, false
	}
	return registry.BlockStateWithProps(name, map[string]string{"level": strconv.Itoa(8 - amount)})
}

// ultraWarm 报告维度是否是主世界外的“超热”维度（下界；水会蒸发）。
func ultraWarm(dim world.Dimension) bool {
	return dim == world.DimensionNether
}

// fluidTickDelay 返回流体的计划刻延迟（水 5；岩浆 30/10）。
func fluidTickDelay(name string, dim world.Dimension) int {
	if name == waterBlockName {
		return 5
	}
	if ultraWarm(dim) {
		return 10
	}
	return 30
}

// fluidDropOff 返回每格流体的等级衰减（水 1；岩浆 2/1）。
func fluidDropOff(name string, dim world.Dimension) int {
	if name == waterBlockName {
		return 1
	}
	if ultraWarm(dim) {
		return 1
	}
	return 2
}

// fluidSlopeDistance 返回流体的“找坡”搜索距离（水 4；岩浆 2/4）。
func fluidSlopeDistance(name string, dim world.Dimension) int {
	if name == waterBlockName {
		return 4
	}
	if ultraWarm(dim) {
		return 4
	}
	return 2
}

// fluidReplaceableNames 是会被流动液体摧毁替换的小型方块（近似原版
// canBeReplaced 判定；流体会倒入并覆盖这些方块，见文件头“已知简化”）。
var fluidReplaceableNames = map[string]bool{
	"minecraft:short_grass":         true,
	"minecraft:tall_grass":          true,
	"minecraft:fern":                true,
	"minecraft:large_fern":          true,
	"minecraft:dead_bush":           true,
	"minecraft:dandelion":           true,
	"minecraft:poppy":               true,
	"minecraft:cornflower":          true,
	"minecraft:oak_sapling":         true,
	"minecraft:spruce_sapling":      true,
	"minecraft:birch_sapling":       true,
	"minecraft:jungle_sapling":      true,
	"minecraft:acacia_sapling":      true,
	"minecraft:dark_oak_sapling":    true,
	"minecraft:sugar_cane":          true,
	"minecraft:snow":                true,
	"minecraft:fire":                true,
	"minecraft:soul_fire":           true,
	"minecraft:torch":               true,
	"minecraft:wall_torch":          true,
	"minecraft:redstone_torch":      true,
	"minecraft:redstone_wall_torch": true,
	"minecraft:redstone_wire":       true,
	"minecraft:lily_pad":            true,
	"minecraft:rail":                true,
	"minecraft:powered_rail":        true,
	"minecraft:detector_rail":       true,
	"minecraft:activator_rail":      true,
	"minecraft:oak_button":          true,
	"minecraft:lever":               true,
	"minecraft:seagrass":            true,
	"minecraft:tall_seagrass":       true,
}

// fluidReplaceable 报告方块状态是否可被指定流体的扩散替换
// （近似原版 canHoldAnyFluid/canHoldSpecificFluid；waterlogging 不支持）。
func (s *Server) fluidReplaceable(state uint16) bool {
	if state == world.AirBlock || isWaterState(state) || isLavaState(state) {
		return true
	}
	name, ok := s.blockNameOf(state)
	if !ok {
		return false
	}
	return fluidReplaceableNames[name]
}

// fluidCanBeReplacedWith 报告目标流体状态能否被新流体按 dir 方向替换：
// 同种流体互相合并（升级/降级），不同流体不直接替换（转换由
// fluidShouldSpread 的岩浆判定完成），空气/小方块可替换。
func (s *Server) fluidCanBeReplacedWith(targetState uint16, incoming string, dir string) bool {
	if targetInfo, ok := fluidInfoOf(targetState); ok {
		return targetInfo.name == incoming
	}
	return s.fluidReplaceable(targetState)
}

// 流体计划刻：位置的挂起 tick（受 fluidMu 保护）。

// scheduleFluidTick 安排位置的流体计划刻；已有计划刻时取更早的延迟
// （原版 LevelTicks.schedule 对同位置同类计划刻保留更早时间）。
func (s *Server) scheduleFluidTick(dim world.Dimension, pos [3]int, ticks int) {
	if ticks < 1 {
		ticks = 1
	}
	s.fluidMu.Lock()
	if s.fluidTicks == nil {
		s.fluidTicks = make(map[containerKey]int)
	}
	key := containerKey{dim: dim, x: pos[0], y: pos[1], z: pos[2]}
	if existing, ok := s.fluidTicks[key]; !ok || ticks < existing {
		s.fluidTicks[key] = ticks
	}
	s.fluidMu.Unlock()
}

// tickFluids 推进流体计划刻（由主 tick 每帧调用）。
func (s *Server) tickFluids() {
	s.fluidMu.Lock()
	if len(s.fluidTicks) == 0 {
		s.fluidMu.Unlock()
		return
	}
	var fires []containerKey
	for key, remaining := range s.fluidTicks {
		remaining--
		if remaining <= 0 {
			fires = append(fires, key)
			delete(s.fluidTicks, key)
			continue
		}
		s.fluidTicks[key] = remaining
	}
	s.fluidMu.Unlock()
	for _, key := range fires {
		s.fireFluidTick(key)
	}
}

// fireFluidTick 触发到期流体计划刻。
func (s *Server) fireFluidTick(key containerKey) {
	w := s.worldFor(key.dim)
	if w == nil {
		return
	}
	pos := [3]int{key.x, key.y, key.z}
	if _, ok := world.SectionIndex(pos[1]); !ok {
		return
	}
	state := w.BlockAt(pos[0], pos[1], pos[2])
	if _, ok := fluidInfoOf(state); !ok {
		return
	}
	s.tickFluid(w, key.dim, pos, state)
}

// updateFluidsAround 在方块变更后通知 1 环邻域内的流体（原版 neighbor 更新：
// LiquidBlock.neighborChanged 的 shouldSpreadLiquid 判定 + updateShape 的
// 源方块重调度），可能的变化由随后的计划刻处理。
func (s *Server) updateFluidsAround(dim world.Dimension, x, y, z int) {
	w := s.worldFor(dim)
	if w == nil {
		return
	}
	pos := [3]int{x, y, z}
	candidates := append([][3]int{pos}, neighborsOf(pos)...)
	for _, candidate := range candidates {
		if _, ok := world.SectionIndex(candidate[1]); !ok {
			continue
		}
		state := w.BlockAt(candidate[0], candidate[1], candidate[2])
		info, ok := fluidInfoOf(state)
		if !ok {
			continue
		}
		if !s.fluidShouldSpread(w, dim, candidate, state, info) {
			continue
		}
		s.scheduleFluidTick(dim, candidate, fluidTickDelay(info.name, dim))
	}
}

// fluidShouldSpread 对应原版 LiquidBlock.shouldSpreadLiquid：岩浆遇到上方或
// 水平的流体水时凝固为黑曜石（源）/圆石（流动），灵魂土旁的蓝冰转为玄武岩；
// 返回 false 表示本次已转换、不再安排扩散。水恒返回 true（正常调度扩散）。
func (s *Server) fluidShouldSpread(w *world.World, dim world.Dimension, pos [3]int, state uint16, info fluidInfo) bool {
	if info.name != lavaBlockName {
		return true
	}
	// 原版检查方向：pos.relative(opposite(dir))，dir ∈ {DOWN, N, S, E, W}
	// → 实际检查上方与四个水平邻居的流体状态是否为水。
	candidates := [5][3]int{
		{pos[0], pos[1] + 1, pos[2]},
		{pos[0], pos[1], pos[2] - 1},
		{pos[0], pos[1], pos[2] + 1},
		{pos[0] - 1, pos[1], pos[2]},
		{pos[0] + 1, pos[1], pos[2]},
	}
	soulSoilBelow := false
	if _, ok := world.SectionIndex(pos[1] - 1); ok {
		if name, ok := s.blockNameOf(w.BlockAt(pos[0], pos[1]-1, pos[2])); ok && name == "minecraft:soul_soil" {
			soulSoilBelow = true
		}
	}
	for _, candidate := range candidates {
		if _, ok := world.SectionIndex(candidate[1]); !ok {
			continue
		}
		candidateState := w.BlockAt(candidate[0], candidate[1], candidate[2])
		if candidateInfo, ok := fluidInfoOf(candidateState); ok && candidateInfo.name == waterBlockName {
			target := "minecraft:cobblestone"
			if info.source {
				target = "minecraft:obsidian"
			}
			if !s.setFluidBlock(w, dim, pos, target) {
				return true // 无法写回（区块未加载）：保留原状态
			}
			s.broadcastWorldEvent(dim, pos, 1501, 0)
			return false
		}
		if soulSoilBelow {
			if name, ok := s.blockNameOf(candidateState); ok && name == "minecraft:blue_ice" {
				if !s.setFluidBlock(w, dim, pos, "minecraft:basalt") {
					return true
				}
				s.broadcastWorldEvent(dim, pos, 1501, 0)
				return false
			}
		}
	}
	return true
}

// setFluidBlock 把位置的流体替换为普通方块（转换用）；成功返回 true。
func (s *Server) setFluidBlock(w *world.World, dim world.Dimension, pos [3]int, name string) bool {
	state, ok := registry.BlockStateIDs[name]
	if !ok {
		return false
	}
	if !w.SetBlock(pos[0], pos[1], pos[2], state) {
		return false
	}
	s.broadcastBlockUpdate(dim, pos[0], pos[1], pos[2], int32(state))
	return true
}

// tickFluid 推进单个流体方块（原版 FlowingFluid.tick + LiquidBlock.tick）。
func (s *Server) tickFluid(w *world.World, dim world.Dimension, pos [3]int, state uint16) {
	info, ok := fluidInfoOf(state)
	if !ok {
		return
	}
	// 水在下界蒸发（近似原版：流体 tick 时移除 + 咝咝声）。
	if info.name == waterBlockName && ultraWarm(dim) {
		if w.SetBlock(pos[0], pos[1], pos[2], world.AirBlock) {
			s.broadcastBlockUpdate(dim, pos[0], pos[1], pos[2], int32(world.AirBlock))
			s.updateFluidsAround(dim, pos[0], pos[1], pos[2])
		}
		return
	}
	if !info.source {
		newState, ok := s.fluidNewLiquid(w, dim, pos, info.name)
		if !ok || newState == 0 {
			// 无法流动：消失（原版 newLiquid 为空 → 置空气）。
			if w.SetBlock(pos[0], pos[1], pos[2], world.AirBlock) {
				s.broadcastBlockUpdate(dim, pos[0], pos[1], pos[2], int32(world.AirBlock))
				s.updateFluidsAround(dim, pos[0], pos[1], pos[2])
			}
			return
		}
		if newState != state {
			if !w.SetBlock(pos[0], pos[1], pos[2], newState) {
				return
			}
			s.broadcastBlockUpdate(dim, pos[0], pos[1], pos[2], int32(newState))
			s.updateFluidsAround(dim, pos[0], pos[1], pos[2])
			state = newState
			if updated, ok := fluidInfoOf(state); ok {
				info = updated
			}
			// 原版 setBlock 的 onPlace 会重新调度自身（继续扩散/收敛）。
			s.scheduleFluidTick(dim, pos, fluidTickDelay(info.name, dim))
			return
		}
	}
	s.fluidSpread(w, dim, pos, state, info)
}

// fluidNewLiquid 对应原版 FlowingFluid.getNewLiquid：以指定流体类型计算
// 位置应处于的流体状态（源/流动/下落/消失）。返回 (0, true) 表示应为空（消失）。
func (s *Server) fluidNewLiquid(w *world.World, dim world.Dimension, pos [3]int, name string) (uint16, bool) {
	sourceCount := 0
	maxAmount := 0
	for _, direction := range horizontalDirections {
		target := [3]int{pos[0] + direction.dx, pos[1], pos[2] + direction.dz}
		if _, ok := world.SectionIndex(target[1]); !ok {
			continue
		}
		targetInfo, ok := fluidInfoOf(s.blockIfLoaded(w, target))
		if !ok || targetInfo.name != name {
			continue
		}
		if targetInfo.source {
			sourceCount++
		}
		if targetInfo.amount > maxAmount {
			maxAmount = targetInfo.amount
		}
	}
	// 无限水：相邻两个源 + 下方固体（或同流体源）→ 变为源。
	if sourceCount >= 2 && name == waterBlockName {
		below := [3]int{pos[0], pos[1] - 1, pos[2]}
		if _, ok := world.SectionIndex(below[1]); ok {
			belowState := w.BlockAt(below[0], below[1], below[2])
			belowInfo, belowIsFluid := fluidInfoOf(belowState)
			if fullCubeState(belowState) || (belowIsFluid && belowInfo.name == name && belowInfo.source) {
				if source, ok := fluidSourceState(name); ok {
					return source, true
				}
			}
		}
	}
	// 上方同流体：变为下落（level 8；原版 getNewLiquid 的 pos.relative(UP)）。
	above := [3]int{pos[0], pos[1] + 1, pos[2]}
	if _, ok := world.SectionIndex(above[1]); ok {
		aboveState := w.BlockAt(above[0], above[1], above[2])
		if aboveInfo, ok := fluidInfoOf(aboveState); ok && aboveInfo.name == name {
			if falling, ok := fluidFallingState(name); ok {
				return falling, true
			}
		}
	}
	newAmount := maxAmount - fluidDropOff(name, dim)
	if newAmount <= 0 {
		return 0, true
	}
	return fluidFlowingState(name, newAmount)
}

// fluidNameOfState 返回状态的流体名（调用方保证是流体）。
func fluidNameOfState(state uint16) string {
	info, _ := fluidInfoOf(state)
	return info.name
}

// horizontalDirections 是四个水平方向（固定顺序保证模拟确定性）。
var horizontalDirections = [4]struct {
	dx, dz int
	dir    string
}{
	{0, -1, "north"},
	{0, 1, "south"},
	{-1, 0, "west"},
	{1, 0, "east"},
}

// fluidSpread 对应原版 FlowingFluid.spread：优先向下方扩散；否则在
// “源方块或下方不是水孔”时向侧面扩散。
func (s *Server) fluidSpread(w *world.World, dim world.Dimension, pos [3]int, state uint16, info fluidInfo) {
	if state == 0 {
		return
	}
	below := [3]int{pos[0], pos[1] - 1, pos[2]}
	belowState := uint16(0)
	belowOK := false
	if _, ok := world.SectionIndex(below[1]); ok {
		belowState = w.BlockAt(below[0], below[1], below[2])
		belowOK = true
	}
	belowInfo, belowIsFluid := fluidInfoOf(belowState)
	if belowOK && s.fluidCanSpreadTo(w, dim, pos, state, info, "down", below, belowState, belowInfo, belowIsFluid) {
		newLiquid, ok := s.fluidNewLiquid(w, dim, below, info.name)
		if ok && newLiquid != 0 && s.fluidCanBeReplacedWith(belowState, fluidNameOfState(newLiquid), "down") {
			s.fluidSpreadTo(w, dim, below, belowState, newLiquid)
			if s.fluidSourceNeighborCount(w, pos, info.name) >= 3 {
				s.fluidSpreadToSides(w, dim, pos, state, info)
			}
			return
		}
	}
	if info.source || !s.fluidIsWaterHole(w, dim, pos, state, below, belowState, belowOK) {
		s.fluidSpreadToSides(w, dim, pos, state, info)
	}
}

// fluidCanSpreadTo 对应原版 canSpreadTo：目标不是同流体源、可持有流体。
func (s *Server) fluidCanSpreadTo(w *world.World, dim world.Dimension, pos [3]int, state uint16, info fluidInfo, dir string, target [3]int, targetState uint16, targetInfo fluidInfo, targetIsFluid bool) bool {
	if targetIsFluid && targetInfo.name == info.name && targetInfo.source {
		return false
	}
	return s.fluidReplaceable(targetState)
}

// fluidIsWaterHole 对应原版 isWaterHole：下方可被本流体填充（已有同流体或
// 可替换）时为“水孔”，此时本方块不向侧面扩散（水柱下落不散开）。
func (s *Server) fluidIsWaterHole(w *world.World, dim world.Dimension, pos [3]int, state uint16, below [3]int, belowState uint16, belowOK bool) bool {
	if !belowOK {
		return false
	}
	if belowInfo, ok := fluidInfoOf(belowState); ok && belowInfo.name == fluidNameOfState(state) {
		return true
	}
	return s.fluidReplaceable(belowState)
}

// fluidSpreadToSides 对应原版 spreadToSides：按 getSpread 选出的方向写入
// 新流体状态。
func (s *Server) fluidSpreadToSides(w *world.World, dim world.Dimension, pos [3]int, state uint16, info fluidInfo) {
	newAmount := info.amount - fluidDropOff(info.name, dim)
	if info.falling {
		newAmount = 7
	}
	if newAmount <= 0 {
		return
	}
	spread := s.fluidGetSpread(w, dim, pos, state, info)
	for _, entry := range spread {
		target := [3]int{pos[0] + entry.dx, pos[1], pos[2] + entry.dz}
		targetState := s.blockIfLoaded(w, target)
		s.fluidSpreadTo(w, dim, target, targetState, entry.newState)
	}
}

// fluidSpreadEntry 是一次侧向扩散的目标（方向与目标流体状态）。
type fluidSpreadEntry struct {
	dx, dz   int
	newState uint16
}

// fluidGetSpread 对应原版 getSpread：在 slope 搜索距离内，只保留能到达
// 下落点的最短路径方向（距离计算见 fluidSlopeDistance）；结果按固定顺序。
func (s *Server) fluidGetSpread(w *world.World, dim world.Dimension, pos [3]int, state uint16, info fluidInfo) []fluidSpreadEntry {
	minDistance := 1000
	result := make([]fluidSpreadEntry, 0, 4)
	var ctx *fluidSpreadContext
	for _, direction := range horizontalDirections {
		target := [3]int{pos[0] + direction.dx, pos[1], pos[2] + direction.dz}
		if _, ok := world.SectionIndex(target[1]); !ok {
			continue
		}
		targetState := s.blockIfLoaded(w, target)
		targetInfo, targetIsFluid := fluidInfoOf(targetState)
		if !s.fluidCanSpreadTo(w, dim, pos, state, info, direction.dir, target, targetState, targetInfo, targetIsFluid) {
			continue
		}
		newLiquid, ok := s.fluidNewLiquid(w, dim, target, info.name)
		if !ok || newLiquid == 0 {
			continue
		}
		if ctx == nil {
			ctx = newFluidSpreadContext(s, w)
		}
		distance := 0
		if !ctx.isHole(target) {
			distance = s.fluidSlopeDistanceTo(w, dim, target, 1, oppositeDir(direction.dir), targetState, info.name, ctx)
		}
		if distance < minDistance {
			result = result[:0]
		}
		if distance <= minDistance {
			if s.fluidCanBeReplacedWith(targetState, info.name, direction.dir) {
				result = append(result, fluidSpreadEntry{dx: direction.dx, dz: direction.dz, newState: newLiquid})
			}
			minDistance = distance
		}
	}
	// 原版 Map 迭代按枚举序；这里按固定方向序重排结果的等价实现：
	// result 已按 horizontalDirections 顺序追加，无需再排。
	return result
}

// fluidSlopeDistanceTo 对应原版 getSlopeDistance：从 (pos) 出发（限制
// slopeFindDistance 步），返回沿该方向到达最近下落点的距离；找不到返回 1000。
func (s *Server) fluidSlopeDistanceTo(w *world.World, dim world.Dimension, pos [3]int, distance int, fromDir string, state uint16, name string, ctx *fluidSpreadContext) int {
	min := 1000
	limit := fluidSlopeDistance(name, dim)
	for _, direction := range horizontalDirections {
		if direction.dir == fromDir {
			continue
		}
		target := [3]int{pos[0] + direction.dx, pos[1], pos[2] + direction.dz}
		if _, ok := world.SectionIndex(target[1]); !ok {
			continue
		}
		targetState := ctx.blockState(target)
		targetInfo, targetIsFluid := fluidInfoOf(targetState)
		// canPassThrough：目标可被同种流体替换（含已有同种流体）。
		passable := false
		if targetIsFluid && targetInfo.name == name {
			passable = true
		} else {
			passable = s.fluidReplaceable(targetState)
		}
		if !passable {
			continue
		}
		if ctx.isHole(target) {
			return distance
		}
		if distance < limit {
			sub := s.fluidSlopeDistanceTo(w, dim, target, distance+1, oppositeDir(direction.dir), targetState, name, ctx)
			if sub < min {
				min = sub
			}
		}
	}
	return min
}

// fluidSourceNeighborCount 对应原版 sourceNeighborCount：水平相邻的同流体
// 源方块数量。
func (s *Server) fluidSourceNeighborCount(w *world.World, pos [3]int, name string) int {
	count := 0
	for _, direction := range horizontalDirections {
		target := [3]int{pos[0] + direction.dx, pos[1], pos[2] + direction.dz}
		if _, ok := world.SectionIndex(target[1]); !ok {
			continue
		}
		if info, ok := fluidInfoOf(s.blockIfLoaded(w, target)); ok && info.name == name && info.source {
			count++
		}
	}
	return count
}

// fluidSpreadContext 缓存一次扩散计算中的方块状态与“水孔”判定
// （对应原版 FlowingFluid.SpreadContext）。
type fluidSpreadContext struct {
	server  *Server
	w       *world.World
	states  map[[3]int]uint16
	isHoles map[[3]int]bool
}

// newFluidSpreadContext 创建扩散上下文。
func newFluidSpreadContext(s *Server, w *world.World) *fluidSpreadContext {
	return &fluidSpreadContext{
		server:  s,
		w:       w,
		states:  make(map[[3]int]uint16, 32),
		isHoles: make(map[[3]int]bool, 16),
	}
}

// blockState 返回位置状态（带缓存）。
func (c *fluidSpreadContext) blockState(pos [3]int) uint16 {
	if state, ok := c.states[pos]; ok {
		return state
	}
	state := c.w.BlockAt(pos[0], pos[1], pos[2])
	c.states[pos] = state
	return state
}

// isHole 报告位置是否是“水孔”（下方可被同种或任意流体填充）。
func (c *fluidSpreadContext) isHole(pos [3]int) bool {
	if hole, ok := c.isHoles[pos]; ok {
		return hole
	}
	state := c.blockState(pos)
	below := [3]int{pos[0], pos[1] - 1, pos[2]}
	hole := false
	if _, ok := world.SectionIndex(below[1]); ok {
		belowState := c.blockState(below)
		belowInfo, belowIsFluid := fluidInfoOf(belowState)
		stateInfo, stateIsFluid := fluidInfoOf(state)
		switch {
		case belowIsFluid && stateIsFluid && belowInfo.name == stateInfo.name:
			hole = true
		case belowIsFluid:
			hole = false
		default:
			hole = c.server.fluidReplaceable(belowState)
		}
	}
	c.isHoles[pos] = hole
	return hole
}

// fluidSpreadTo 对应原版 spreadTo：把目标位置的方块替换为新流体状态，
// 并调度目标位置的后续扩散与邻居流体更新。目标已是相同状态时直接返回
// （避免无变化的重复调度形成无限 tick 级联）。
func (s *Server) fluidSpreadTo(w *world.World, dim world.Dimension, pos [3]int, targetState uint16, newState uint16) {
	if _, ok := world.SectionIndex(pos[1]); !ok {
		return
	}
	if targetState == newState {
		return
	}
	if !s.fluidCanBeReplacedWith(targetState, fluidNameOfState(newState), "down") &&
		targetState != world.AirBlock {
		// 仅当可替换时写入（callers 已判定；双保险）。
		if info, ok := fluidInfoOf(targetState); !ok || info.name != fluidNameOfState(newState) {
			if !s.fluidReplaceable(targetState) {
				return
			}
		}
	}
	if !w.SetBlock(pos[0], pos[1], pos[2], newState) {
		return
	}
	s.broadcastBlockUpdate(dim, pos[0], pos[1], pos[2], int32(newState))
	s.updateFluidsAround(dim, pos[0], pos[1], pos[2])
	info, _ := fluidInfoOf(newState)
	s.scheduleFluidTick(dim, pos, fluidTickDelay(info.name, dim))
}
