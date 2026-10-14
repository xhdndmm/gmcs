//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"gmcs/internal/config"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 活塞：PistonBaseBlock / PistonStructureResolver / PistonHeadBlock 的移植
// （对照 1.21.11 官方 mappings 反编译结果）。
//
// 语义要点：
//   - 活塞激活判定 = 除 facing 外的五向邻居发射值 + 上方格的“准连接”
//     （QC）检查（原版 getNeighborSignal）；
//   - 伸展前用结构解算器判定：最多推 12 格、拉取粘性方块链、碰撞重排、
//     DESTROY 反应方块直接破坏、BLOCK 反应/不可破坏方块阻挡、带方块实体
//     的方块不可推动、PUSH_ONLY 不可拉取；
//   - 粘性活塞收回时拉回前方一格方块（反应必须为 NORMAL，或对方是活塞）；
//   - 活塞头与基座联动：破坏基座时头部消失；破坏头部时基座被移除
//     （生存模式掉落活塞物品，原版 PistonHeadBlock.playerWillDestroy /
//     affectNeighborsAfterRemoval 的行为）；
//   - 被推动方块上的实体被推移到活塞最终位置之外（对应原版
//     PistonMovingBlockEntity 的实体推动）。
//
// 已知限制（见 docs/TODO.md）：
//   - 无移动动画：原版把被推方块短暂替换为 MOVING_PISTON 移动方块实体，
//     2 tick 后落地；本实现直接在同 tick 内落位（红石判定使用落位后的
//     状态，因此依赖“移动中”状态的红石时序（如移动活塞未落地时的信号）
//     与原版有差异）；
//   - 收回时不推动实体（原版移动方块实体在收回动画中会把实体推出）。

const (
	pistonName       = "minecraft:piston"
	stickyPistonName = "minecraft:sticky_piston"
	pistonHeadName   = "minecraft:piston_head"

	// pistonMaxPushDepth 是原版 PistonStructureResolver.MAX_PUSH_DEPTH。
	pistonMaxPushDepth = 12
)

// pistonImmovableNames 是原版 isPushable 中 getDestroySpeed == -1.0F 的方块
// （提取自 1.21.11 Blocks.java 中 strength(-1.0F) 的注册项），
// 以及显式排除的黑曜石类方块。
var pistonImmovableNames = map[string]bool{
	"minecraft:obsidian":             true,
	"minecraft:crying_obsidian":      true,
	"minecraft:respawn_anchor":       true,
	"minecraft:reinforced_deepslate": true,

	"minecraft:barrier":                 true,
	"minecraft:bedrock":                 true,
	"minecraft:chain_command_block":     true,
	"minecraft:command_block":           true,
	"minecraft:end_gateway":             true,
	"minecraft:end_portal":              true,
	"minecraft:end_portal_frame":        true,
	"minecraft:jigsaw":                  true,
	"minecraft:light":                   true,
	"minecraft:moving_piston":           true,
	"minecraft:nether_portal":           true,
	"minecraft:repeating_command_block": true,
	"minecraft:structure_block":         true,
	"minecraft:test_block":              true,
	"minecraft:test_instance_block":     true,
}

// pistonBlockEntityNames 是“带方块实体因而不可推动”（原版 !state.hasBlockEntity()）
// 的方块名。仅需覆盖推反应为 NORMAL/PUSH_ONLY 的方块：DESTROY/BLOCK 反应
// 在同名判定之前就已决定结果。
var pistonBlockEntityNames = map[string]bool{
	"minecraft:barrel":                  true,
	"minecraft:beacon":                  true,
	"minecraft:bee_nest":                true,
	"minecraft:beehive":                 true,
	"minecraft:blast_furnace":           true,
	"minecraft:brewing_stand":           true,
	"minecraft:calibrated_sculk_sensor": true,
	"minecraft:campfire":                true,
	"minecraft:chest":                   true,
	"minecraft:chiseled_bookshelf":      true,
	"minecraft:conduit":                 true,
	"minecraft:crafter":                 true,
	"minecraft:dispenser":               true,
	"minecraft:dropper":                 true,
	"minecraft:enchanting_table":        true,
	"minecraft:ender_chest":             true,
	"minecraft:furnace":                 true,
	"minecraft:hopper":                  true,
	"minecraft:jukebox":                 true,
	"minecraft:lectern":                 true,
	"minecraft:oak_hanging_sign":        true,
	"minecraft:oak_sign":                true,
	"minecraft:pale_oak_hanging_sign":   true,
	"minecraft:pale_oak_sign":           true,
	"minecraft:sculk_sensor":            true,
	"minecraft:shelf":                   true,
	"minecraft:smoker":                  true,
	"minecraft:soul_campfire":           true,
	"minecraft:spawner":                 true,
	"minecraft:trapped_chest":           true,
	"minecraft:trial_spawner":           true,
	"minecraft:vault":                   true,
	"minecraft:white_banner":            true,
}

// pistonDirectionOrder 是原版 Direction.values() 的顺序（DOWN、UP、NORTH、
// SOUTH、WEST、EAST）。
var pistonDirectionOrder = [6]string{"down", "up", "north", "south", "west", "east"}

// axisOf 返回方向的轴（"x"、"y" 或 "z"）。
func axisOf(dir string) string {
	switch dir {
	case "up", "down":
		return "y"
	case "east", "west":
		return "x"
	default:
		return "z"
	}
}

// isAirState 报告状态是否为空气类方块（原版 state.isAir()）。
func (s *Server) isAirState(state uint16) bool {
	name, ok := s.blockNameOf(state)
	if !ok {
		return state == world.AirBlock
	}
	switch name {
	case "minecraft:air", "minecraft:cave_air", "minecraft:void_air":
		return true
	}
	return false
}

// isStickyState 报告状态是否为粘性方块（粘液块/蜂蜜块）。
func (s *Server) isStickyState(state uint16) bool {
	name, ok := s.blockNameOf(state)
	return ok && (name == "minecraft:slime_block" || name == "minecraft:honey_block")
}

// canStickToEachOther 对应原版 PistonStructureResolver.canStickToEachOther：
// 粘液块与蜂蜜块不能互相粘连，其余情况任一方为粘性方块即可。
func (s *Server) canStickToEachOther(a, b uint16) bool {
	aName, aOK := s.blockNameOf(a)
	bName, bOK := s.blockNameOf(b)
	if aOK && bOK && ((aName == "minecraft:honey_block" && bName == "minecraft:slime_block") ||
		(aName == "minecraft:slime_block" && bName == "minecraft:honey_block")) {
		return false
	}
	return s.isStickyState(a) || s.isStickyState(b)
}

// isPistonState 报告状态是否为活塞基座（普通或粘性）。
func (s *Server) isPistonState(state uint16) bool {
	name, ok := s.blockNameOf(state)
	return ok && (name == pistonName || name == stickyPistonName)
}

// isPistonHeadState 报告状态是否为活塞头。
func (s *Server) isPistonHeadState(state uint16) bool {
	name, ok := s.blockNameOf(state)
	return ok && name == pistonHeadName
}

// pistonReaction 返回方块的推反应（原版 getPistonPushReaction）。
func (s *Server) pistonReaction(state uint16) registry.PushReaction {
	name, ok := s.blockNameOf(state)
	if !ok {
		return registry.PushReactionBlock
	}
	return registry.PushReactionOf(prefixlessName(name))
}

// pistonHasBlockEntity 报告方块是否带方块实体（原版 state.hasBlockEntity()）。
func (s *Server) pistonHasBlockEntity(state uint16) bool {
	name, ok := s.blockNameOf(state)
	if !ok {
		return true
	}
	if pistonBlockEntityNames[name] {
		return true
	}
	_, container := s.containerDefs[name]
	return container
}

// pistonNeighborSignal 对应原版 PistonBaseBlock.getNeighborSignal：
// 除 facing 外的五向邻居发射值，加上活塞上方一格的准连接（QC）检查。
// 原版还对活塞自身调用 hasSignal(pos, DOWN)，但基座方块的 getSignal 恒为 0
// 且活塞不导强充能，该检查对活塞是死代码，此处省略。
func (s *Server) pistonNeighborSignal(w *world.World, pos [3]int, facing string) bool {
	for _, dir := range pistonDirectionOrder {
		if dir == facing {
			continue
		}
		if s.emittedSignal(w, offsetDir(pos, dir), dir) > 0 {
			return true
		}
	}
	above := [3]int{pos[0], pos[1] + 1, pos[2]}
	for _, dir := range pistonDirectionOrder {
		if dir == "down" {
			continue
		}
		if s.emittedSignal(w, offsetDir(above, dir), dir) > 0 {
			return true
		}
	}
	return false
}

// checkPistonAt 在方块更新后/红石刷新时重算活塞（原版 neighborChanged →
// checkIfExtend）：供电状态与 extended 不一致时安排 1 tick 后结算；
// 伸展前先做一次结构解算（与原版一致：解算失败则不发事件）。
func (s *Server) checkPistonAt(w *world.World, dim world.Dimension, pos [3]int) {
	state := s.blockIfLoaded(w, pos)
	_, props, ok := registry.StateProps(state)
	if !ok {
		return
	}
	name, _ := s.blockNameOf(state)
	if name != pistonName && name != stickyPistonName {
		return
	}
	if s.hasRedstoneTick(dim, pos) {
		return
	}
	facing := props["facing"]
	extended := props["extended"] == "true"
	powered := s.pistonNeighborSignal(w, pos, facing)
	if powered == extended {
		return
	}
	if powered {
		resolver := s.newPistonResolver(w, pos, facing, true)
		if !resolver.resolve() {
			return // 被阻挡：不激活
		}
	}
	s.scheduleRedstoneTick(dim, pos, 1)
}

// tickPiston 结算活塞计划刻（对应原版 triggerEvent 的伸展/收回分支）。
func (s *Server) tickPiston(w *world.World, dim world.Dimension, pos [3]int, state uint16) {
	name, props, ok := registry.StateProps(state)
	if !ok || (name != pistonName && name != stickyPistonName) {
		return
	}
	facing := props["facing"]
	extended := props["extended"] == "true"
	powered := s.pistonNeighborSignal(w, pos, facing)
	if powered == extended {
		return // 事件与当前供电不符：放弃（原版 triggerEvent 的提前返回）
	}
	if powered {
		s.extendPiston(w, dim, pos, name, facing)
		return
	}
	s.retractPiston(w, dim, pos, name, facing)
}

// ---------------------------------------------------------------------------
// 结构解算（PistonStructureResolver）
// ---------------------------------------------------------------------------

// pistonResolver 对应原版 PistonStructureResolver。
type pistonResolver struct {
	s               *Server
	w               *world.World
	pistonPos       [3]int
	pistonDirection string
	extending       bool
	pushDirection   string
	startPos        [3]int
	toPush          [][3]int
	toDestroy       [][3]int
}

// newPistonResolver 构造解算器（拉伸时从 facing 侧一格起算，
// 收回时从 facing 侧两格起算并向反方向解算）。
func (s *Server) newPistonResolver(w *world.World, pos [3]int, direction string, extending bool) *pistonResolver {
	r := &pistonResolver{
		s:               s,
		w:               w,
		pistonPos:       pos,
		pistonDirection: direction,
		extending:       extending,
	}
	if extending {
		r.pushDirection = direction
		r.startPos = offsetDir(pos, direction)
	} else {
		r.pushDirection = oppositeDir(direction)
		r.startPos = offsetDir(pos, direction)
		r.startPos = offsetDir(r.startPos, direction)
	}
	return r
}

// resolve 对应原版 resolve()：为 true 时 toPush/toDestroy 可用。
func (r *pistonResolver) resolve() bool {
	r.toPush = r.toPush[:0]
	r.toDestroy = r.toDestroy[:0]
	startState := r.s.blockIfLoaded(r.w, r.startPos)
	if !r.s.pistonIsPushable(r.w, startState, r.startPos, r.pushDirection, false, r.pistonDirection) {
		if r.extending && !r.s.isAirState(startState) &&
			r.s.pistonReaction(startState) == registry.PushReactionDestroy {
			r.toDestroy = append(r.toDestroy, r.startPos)
			return true
		}
		return false
	}
	if !r.addBlockLine(r.startPos, r.pushDirection) {
		return false
	}
	for i := 0; i < len(r.toPush); i++ {
		pos := r.toPush[i]
		if r.s.isStickyState(r.s.blockIfLoaded(r.w, pos)) && !r.addBranchingBlocks(pos) {
			return false
		}
	}
	return true
}

// pistonIsPushable 对应原版 PistonBaseBlock.isPushable。
// allowDestroy 为 true 时 DESTROY 反应方块视为“可处理”（调用方随后会把它
// 加入 toDestroy），其余情况返回 false。
func (s *Server) pistonIsPushable(w *world.World, state uint16, pos [3]int, pushDirection string, allowDestroy bool, pistonDirection string) bool {
	if _, ok := world.SectionIndex(pos[1]); !ok {
		return false
	}
	if s.isAirState(state) {
		return true
	}
	name, ok := s.blockNameOf(state)
	if !ok {
		return false
	}
	if pistonImmovableNames[name] {
		return false
	}
	if pushDirection == "down" && pos[1] == world.WorldMinY {
		return false
	}
	if pushDirection == "up" && pos[1] == world.WorldMinY+world.WorldHeight-1 {
		return false
	}
	if name != pistonName && name != stickyPistonName {
		switch s.pistonReaction(state) {
		case registry.PushReactionBlock:
			return false
		case registry.PushReactionDestroy:
			return allowDestroy
		case registry.PushReactionPushOnly:
			return pushDirection == pistonDirection
		}
	} else if _, props, ok := registry.StateProps(state); ok && props["extended"] == "true" {
		return false
	}
	return !s.pistonHasBlockEntity(state)
}

// addBlockLine 对应原版 addBlockLine：沿推方向收集一条方块线（含粘性方块
// 向后延伸的拖尾），遇到已有元素时按原版重排并结束。
func (r *pistonResolver) addBlockLine(pos [3]int, dir string) bool {
	state := r.s.blockIfLoaded(r.w, pos)
	if r.s.isAirState(state) {
		return true
	}
	if !r.s.pistonIsPushable(r.w, state, pos, r.pushDirection, false, dir) {
		return true
	}
	if pos == r.pistonPos {
		return true
	}
	if r.containsPush(pos) {
		return true
	}
	count := 1
	if count+len(r.toPush) > pistonMaxPushDepth {
		return false
	}
	for r.s.isStickyState(state) {
		next := pos
		for i := 0; i < count; i++ {
			next = offsetDir(next, oppositeDir(r.pushDirection))
		}
		previous := state
		state = r.s.blockIfLoaded(r.w, next)
		if r.s.isAirState(state) ||
			!r.s.canStickToEachOther(previous, state) ||
			!r.s.pistonIsPushable(r.w, state, next, r.pushDirection, false, oppositeDir(r.pushDirection)) ||
			next == r.pistonPos {
			break
		}
		count++
		if count+len(r.toPush) > pistonMaxPushDepth {
			return false
		}
	}
	added := 0
	for i := count - 1; i >= 0; i-- {
		cell := pos
		for k := 0; k < i; k++ {
			cell = offsetDir(cell, oppositeDir(r.pushDirection))
		}
		r.toPush = append(r.toPush, cell)
		added++
	}
	forward := 1
	for {
		next := pos
		for i := 0; i < forward; i++ {
			next = offsetDir(next, r.pushDirection)
		}
		if idx := r.indexOfPush(next); idx > -1 {
			r.reorderListAtCollision(added, idx)
			for i := 0; i <= idx+added; i++ {
				cell := r.toPush[i]
				if r.s.isStickyState(r.s.blockIfLoaded(r.w, cell)) && !r.addBranchingBlocks(cell) {
					return false
				}
			}
			return true
		}
		state = r.s.blockIfLoaded(r.w, next)
		if r.s.isAirState(state) {
			return true
		}
		if !r.s.pistonIsPushable(r.w, state, next, r.pushDirection, true, r.pushDirection) || next == r.pistonPos {
			return false
		}
		if r.s.pistonReaction(state) == registry.PushReactionDestroy {
			r.toDestroy = append(r.toDestroy, next)
			return true
		}
		if len(r.toPush) >= pistonMaxPushDepth {
			return false
		}
		r.toPush = append(r.toPush, next)
		added++
		forward++
	}
}

// containsPush 报告位置是否已在 toPush 中。
func (r *pistonResolver) containsPush(pos [3]int) bool {
	return r.indexOfPush(pos) > -1
}

// indexOfPush 返回位置在 toPush 中的下标（不存在时为 -1）。
func (r *pistonResolver) indexOfPush(pos [3]int) int {
	for i, cell := range r.toPush {
		if cell == pos {
			return i
		}
	}
	return -1
}

// reorderListAtCollision 对应原版 reorderListAtCollision：
// 把碰撞点到末尾的 added 个元素移到碰撞点之前。
func (r *pistonResolver) reorderListAtCollision(added, idx int) {
	size := len(r.toPush)
	head := append([][3]int(nil), r.toPush[:idx]...)
	tail := append([][3]int(nil), r.toPush[size-added:]...)
	middle := append([][3]int(nil), r.toPush[idx:size-added]...)
	r.toPush = r.toPush[:0]
	r.toPush = append(r.toPush, head...)
	r.toPush = append(r.toPush, tail...)
	r.toPush = append(r.toPush, middle...)
}

// addBranchingBlocks 对应原版 addBranchingBlocks：粘性方块垂直于推方向的
// 相邻粘性方块也纳入推动列表。
func (r *pistonResolver) addBranchingBlocks(pos [3]int) bool {
	state := r.s.blockIfLoaded(r.w, pos)
	for _, dir := range pistonDirectionOrder {
		if axisOf(dir) == axisOf(r.pushDirection) {
			continue
		}
		neighbor := offsetDir(pos, dir)
		neighborState := r.s.blockIfLoaded(r.w, neighbor)
		if r.s.canStickToEachOther(neighborState, state) && !r.addBlockLine(neighbor, dir) {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// 移动执行
// ---------------------------------------------------------------------------

// extendPiston 执行伸展：解算成功后立即移动方块并放置活塞头。
func (s *Server) extendPiston(w *world.World, dim world.Dimension, pos [3]int, name, facing string) {
	resolver := s.newPistonResolver(w, pos, facing, true)
	if !resolver.resolve() {
		return
	}
	s.pistonMoveBlocks(w, dim, pos, name, facing, true, resolver)
}

// retractPiston 执行收回：移除活塞头；粘性活塞在前方一格方块可拉取时
// 把它（及粘性链）拉回一格。
func (s *Server) retractPiston(w *world.World, dim world.Dimension, pos [3]int, name, facing string) {
	headPos := offsetDir(pos, facing)
	second := offsetDir(headPos, facing)
	pull := false
	if name == stickyPistonName {
		secondState := s.blockIfLoaded(w, second)
		secondName, ok := s.blockNameOf(secondState)
		if ok && !s.isAirState(secondState) &&
			s.pistonIsPushable(w, secondState, second, oppositeDir(facing), false, facing) {
			if s.pistonReaction(secondState) == registry.PushReactionNormal ||
				secondName == pistonName || secondName == stickyPistonName {
				pull = true
			}
		}
	}
	if pull {
		// 原版先移除头部再做结构解算（解算需要看到头部位置是空气）。
		if s.isPistonHeadState(s.blockIfLoaded(w, headPos)) {
			w.SetBlock(headPos[0], headPos[1], headPos[2], world.AirBlock)
		}
		resolver := s.newPistonResolver(w, pos, facing, false)
		if resolver.resolve() {
			s.pistonMoveBlocks(w, dim, pos, name, facing, false, resolver)
			return
		}
	}
	// 不拉取：仅移除头部并收回基座。
	changed := make([][3]int, 0, 2)
	if s.isPistonHeadState(s.blockIfLoaded(w, headPos)) {
		w.SetBlock(headPos[0], headPos[1], headPos[2], world.AirBlock)
		changed = append(changed, headPos)
	}
	if base := s.pistonBaseState(name, facing, false); base != world.AirBlock {
		if w.SetBlock(pos[0], pos[1], pos[2], base) {
			changed = append(changed, pos)
		}
	}
	s.pistonFinish(dim, changed)
}

// pistonMoveBlocks 对应原版 PistonBaseBlock.moveBlocks：按解算结果移动方块、
// 破坏 DESTROY 反应方块、放置活塞头、更新基座状态，最后广播与推动实体。
func (s *Server) pistonMoveBlocks(w *world.World, dim world.Dimension, pos [3]int, name, facing string, extending bool, resolver *pistonResolver) {
	moveDir := facing
	if !extending {
		moveDir = oppositeDir(facing)
	}
	headPos := offsetDir(pos, facing)

	// 快照被推动方块（原版同样先快照再移动）。
	moved := append([][3]int(nil), resolver.toPush...)
	states := make([]uint16, len(moved))
	for i, cell := range moved {
		states[i] = s.blockIfLoaded(w, cell)
	}

	changed := make([][3]int, 0, len(resolver.toPush)+len(resolver.toDestroy)+2)

	// 1. 破坏 DESTROY 反应方块（远→近）：原版 dropResources 后置空。
	for i := len(resolver.toDestroy) - 1; i >= 0; i-- {
		cell := resolver.toDestroy[i]
		state := s.blockIfLoaded(w, cell)
		if !s.isAirState(state) {
			s.dropBlockItem(dim, state, cell[0], cell[1], cell[2])
		}
		_ = w.RemoveBlockEntity(cell[0], cell[1], cell[2])
		if w.SetBlock(cell[0], cell[1], cell[2], world.AirBlock) {
			changed = append(changed, cell)
		}
	}

	// 2. 移动被推动方块（远→近）：方块实体与容器状态随方块迁移。
	for i := len(moved) - 1; i >= 0; i-- {
		from := moved[i]
		to := offsetDir(from, moveDir)
		if entity, err := w.BlockEntityAt(from[0], from[1], from[2]); err == nil {
			if err := w.SetBlockEntity(to[0], to[1], to[2], entity); err == nil {
				_ = w.RemoveBlockEntity(from[0], from[1], from[2])
			}
		}
		s.moveContainerKey(dim, from, to)
		if w.SetBlock(to[0], to[1], to[2], states[i]) {
			changed = append(changed, to)
		}
	}

	// 3. 清空来源格（跳过作为其它方块目标的格子）。
	destinations := make(map[[3]int]bool, len(moved))
	for _, cell := range moved {
		destinations[offsetDir(cell, moveDir)] = true
	}
	for _, cell := range moved {
		if destinations[cell] {
			continue
		}
		if w.SetBlock(cell[0], cell[1], cell[2], world.AirBlock) {
			changed = append(changed, cell)
		}
	}

	// 4. 伸展收尾：放置活塞头；基座进入 extended 状态。
	if extending {
		if head := s.pistonHeadState(facing, name == stickyPistonName); head != world.AirBlock {
			if w.SetBlock(headPos[0], headPos[1], headPos[2], head) {
				changed = append(changed, headPos)
			}
		}
	}
	if base := s.pistonBaseState(name, facing, extending); base != world.AirBlock {
		if w.SetBlock(pos[0], pos[1], pos[2], base) {
			changed = append(changed, pos)
		}
	}

	s.pistonFinish(dim, changed)

	// 5. 推动实体（仅伸展）。
	if extending {
		s.pistonPushEntities(w, dim, pos, facing, len(resolver.toPush)+1)
	}
}

// pistonFinish 广播所有变化格并刷新红石（对应原版移动完成后对每个受影响
// 方块调用 updateNeighbourShapes / neighborChanged）。
func (s *Server) pistonFinish(dim world.Dimension, changed [][3]int) {
	w := s.worldFor(dim)
	if w == nil {
		return
	}
	seen := make(map[[3]int]bool, len(changed))
	for _, cell := range changed {
		if seen[cell] {
			continue
		}
		seen[cell] = true
		state := w.BlockAt(cell[0], cell[1], cell[2])
		s.broadcastBlockUpdate(dim, cell[0], cell[1], cell[2], int32(state))
		s.updateRedstoneAround(dim, cell[0], cell[1], cell[2])
	}
}

// moveContainerKey 把容器（含熔炉）的运行时状态从 from 迁移到 to
// （对应原版移动方块实体时同步迁移方块实体与容器数据）。
func (s *Server) moveContainerKey(dim world.Dimension, from, to [3]int) {
	fromKey := containerKey{dim: dim, x: from[0], y: from[1], z: from[2]}
	toKey := containerKey{dim: dim, x: to[0], y: to[1], z: to[2]}

	s.containerMu.Lock()
	if container, ok := s.containers[fromKey]; ok {
		delete(s.containers, fromKey)
		container.mu.Lock()
		container.pos = to
		container.mu.Unlock()
		s.containers[toKey] = container
	}
	s.containerMu.Unlock()

	s.furnaceMu.Lock()
	if furnace, ok := s.furnaces[fromKey]; ok {
		delete(s.furnaces, fromKey)
		furnace.mu.Lock()
		furnace.pos = to
		furnace.mu.Unlock()
		s.furnaces[toKey] = furnace
	}
	s.furnaceMu.Unlock()
}

// pistonBaseState 构造活塞基座状态。
func (s *Server) pistonBaseState(name, facing string, extended bool) uint16 {
	if name != pistonName && name != stickyPistonName {
		return world.AirBlock
	}
	state, ok := registry.BlockStateWithProps(name, map[string]string{
		"facing":   facing,
		"extended": boolString(extended),
	})
	if !ok {
		return world.AirBlock
	}
	return state
}

// pistonHeadState 构造活塞头状态（type 由同名的粘性变体决定）。
func (s *Server) pistonHeadState(facing string, sticky bool) uint16 {
	headType := "normal"
	if sticky {
		headType = "sticky"
	}
	state, ok := registry.BlockStateWithProps(pistonHeadName, map[string]string{
		"facing": facing,
		"type":   headType,
		"short":  "false",
	})
	if !ok {
		return world.AirBlock
	}
	return state
}

// boolString 返回原版布尔属性字符串。
func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

// ---------------------------------------------------------------------------
// 破坏联动（原版 PistonHeadBlock.playerWillDestroy /
// affectNeighborsAfterRemoval 与基座的头部清理）
// ---------------------------------------------------------------------------

// handlePistonBreak 处理玩家破坏活塞基座/活塞头的联动；返回 true 表示
// 该次破坏已由活塞逻辑完整处理（调用方不应再执行通用破坏流程）。
//
// 原版行为：
//   - 破坏基座（伸展状态）时头部随之消失（不掉落）；
//   - 破坏头部时基座一并被移除：创造模式静默移除（preventsBlockDrops），
//     生存模式破坏基座并掉落活塞物品（原版活塞头掉落表为空）。
func (s *Server) handlePistonBreak(dim world.Dimension, player *session, w *world.World, x, y, z int, state uint16) bool {
	name, props, ok := registry.StateProps(state)
	if !ok {
		return false
	}
	pos := [3]int{x, y, z}
	survival := player.gameModeID() != uint8(config.GameModeCreative)
	switch {
	case name == pistonName || name == stickyPistonName:
		if props["extended"] != "true" {
			return false // 未伸展的基座按通用破坏流程处理
		}
		facing := props["facing"]
		headPos := offsetDir(pos, facing)
		changed := make([][3]int, 0, 2)
		if w.SetBlock(x, y, z, world.AirBlock) {
			changed = append(changed, pos)
		}
		if s.isPistonHeadState(s.blockIfLoaded(w, headPos)) {
			w.SetBlock(headPos[0], headPos[1], headPos[2], world.AirBlock)
			changed = append(changed, headPos)
		}
		s.pistonFinish(dim, changed)
		if survival {
			s.dropBlockItem(dim, state, x, y, z)
		}
		return true
	case name == pistonHeadName:
		facing := props["facing"]
		basePos := offsetDir(pos, oppositeDir(facing))
		baseState := s.blockIfLoaded(w, basePos)
		changed := make([][3]int, 0, 2)
		if baseName, baseProps, ok := registry.StateProps(baseState); ok &&
			(baseName == pistonName || baseName == stickyPistonName) &&
			baseProps["facing"] == facing && baseProps["extended"] == "true" {
			if w.SetBlock(basePos[0], basePos[1], basePos[2], world.AirBlock) {
				changed = append(changed, basePos)
			}
			if survival {
				s.dropBlockItem(dim, baseState, basePos[0], basePos[1], basePos[2])
			}
		}
		if w.SetBlock(x, y, z, world.AirBlock) {
			changed = append(changed, pos)
		}
		s.pistonFinish(dim, changed)
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// 实体推动
// ---------------------------------------------------------------------------

// pistonPushEntities 把位于活塞推动路径上的实体推移到活塞最终位置之外
// （对应原版 PistonMovingBlockEntity 在移动动画期间对实体的推动）。
// count 是推动柱的长度（方块数，含活塞头所在的格子）。
func (s *Server) pistonPushEntities(w *world.World, dim world.Dimension, pos [3]int, facing string, count int) {
	if count < 1 {
		return
	}
	// 扫掠区域：推动柱每一格的方块盒沿推动方向膨胀一格
	// （这样站在柱体上的实体也会被包含）。
	axis := axisOf(facing)
	positive := facing == "up" || facing == "east" || facing == "south"
	swept := make([]world.Box, 0, count)
	far := 0.0
	first := true
	for k := 1; k <= count; k++ {
		cell := pos
		for i := 0; i < k; i++ {
			cell = offsetDir(cell, facing)
		}
		box := world.Box{
			MinX: float64(cell[0]), MinY: float64(cell[1]), MinZ: float64(cell[2]),
			MaxX: float64(cell[0] + 1), MaxY: float64(cell[1] + 1), MaxZ: float64(cell[2] + 1),
		}
		switch axis {
		case "x":
			if positive {
				box.MaxX++
			} else {
				box.MinX--
			}
		case "y":
			if positive {
				box.MaxY++
			} else {
				box.MinY--
			}
		default:
			if positive {
				box.MaxZ++
			} else {
				box.MinZ--
			}
		}
		swept = append(swept, box)
		var edge float64
		switch axis {
		case "x":
			if positive {
				edge = box.MaxX
			} else {
				edge = box.MinX
			}
		case "y":
			if positive {
				edge = box.MaxY
			} else {
				edge = box.MinY
			}
		default:
			if positive {
				edge = box.MaxZ
			} else {
				edge = box.MinZ
			}
		}
		if first || (positive && edge > far) || (!positive && edge < far) {
			far = edge
			first = false
		}
	}

	// pushed 返回实体应沿推动方向移动的距离；不与扫掠区域相交时返回 false。
	pushed := func(box world.Box) (float64, bool) {
		intersects := false
		for _, sweptBox := range swept {
			if box.MinX >= sweptBox.MaxX || box.MaxX <= sweptBox.MinX ||
				box.MinY >= sweptBox.MaxY || box.MaxY <= sweptBox.MinY ||
				box.MinZ >= sweptBox.MaxZ || box.MaxZ <= sweptBox.MinZ {
				continue
			}
			intersects = true
			break
		}
		if !intersects {
			return 0, false
		}
		switch axis {
		case "x":
			if positive {
				return far - box.MinX, true
			}
			return box.MaxX - far, true
		case "y":
			if positive {
				return far - box.MinY, true
			}
			return box.MaxY - far, true
		default:
			if positive {
				return far - box.MinZ, true
			}
			return box.MaxZ - far, true
		}
	}

	// displace 按推动方向平移一个实体的坐标。
	displace := func(x, y, z, delta float64) (float64, float64, float64) {
		switch axis {
		case "x":
			if positive {
				x += delta
			} else {
				x -= delta
			}
		case "y":
			if positive {
				y += delta
			} else {
				y -= delta
			}
		default:
			if positive {
				z += delta
			} else {
				z -= delta
			}
		}
		return x, y, z
	}

	for _, player := range s.playerSnapshot() {
		if !player.isJoined() || player.dimensionID() != dim {
			continue
		}
		px, py, pz, yaw, pitch := player.playerPosition()
		delta, ok := pushed(playerBox(px, py, pz))
		if !ok || delta <= 0 {
			continue
		}
		nx, ny, nz := displace(px, py, pz, delta)
		if w.Collides(playerBox(nx, ny, nz)) {
			continue // 目标位置被挡住时保持原位（与原版的挤压行为有差异）
		}
		player.setPlayerPosition(nx, ny, nz, yaw, pitch)
		player.resyncPosition()
	}

	type pushMove struct {
		packet []byte
		x, z   float64
	}
	var moves []pushMove
	s.entityMu.Lock()
	for _, m := range s.mobs {
		if m.Dim != dim {
			continue
		}
		delta, ok := pushed(mobBox(m.X, m.Y, m.Z))
		if !ok || delta <= 0 {
			continue
		}
		nx, ny, nz := displace(m.X, m.Y, m.Z, delta)
		if w.Collides(mobBox(nx, ny, nz)) {
			continue
		}
		m.X, m.Y, m.Z = nx, ny, nz
		moves = append(moves, pushMove{
			packet: protocol.EncodeEntityPositionSync(m.ID, m.X, m.Y, m.Z, 0, 0, 0, m.Yaw, m.Pitch, true),
			x:      m.X,
			z:      m.Z,
		})
	}
	for _, e := range s.items {
		if e.Dim != dim {
			continue
		}
		delta, ok := pushed(itemBoxAt(e.X, e.Y, e.Z))
		if !ok || delta <= 0 {
			continue
		}
		nx, ny, nz := displace(e.X, e.Y, e.Z, delta)
		if w.Collides(itemBoxAt(nx, ny, nz)) {
			continue
		}
		e.X, e.Y, e.Z = nx, ny, nz
		moves = append(moves, pushMove{
			packet: protocol.EncodeEntityPositionSync(e.ID, e.X, e.Y, e.Z, e.VelX, e.VelY, e.VelZ, 0, 0, true),
			x:      e.X,
			z:      e.Z,
		})
	}
	s.entityMu.Unlock()
	for _, move := range moves {
		s.writeToNearbyPlayers(move.packet, dim, move.x, move.z, nil)
	}
}
