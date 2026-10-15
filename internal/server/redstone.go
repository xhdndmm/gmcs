//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"strconv"

	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 红石系统核心：电源、红石线网络、用电器刷新与计划刻调度。
//
// 实现与 1.21.11 原版对齐（对照官方 mappings 与反编译的组件类）：
//
//   - 电源：拉杆/按钮（powered）、红石块、红石火把（lit）；
//   - 红石线：外部电源取“相邻方块发射值”最大值（方向语义见
//     redstone_signals.go），线间传播按原版 getIncomingWireSignal 的
//     水平相邻/上下越块规则，多轮松弛至收敛；连接方向属性按原版
//     getConnectionState 计算（含直线贯通规则与“向机构方块供电需指向”），
//     供电与客户端渲染同源；
//   - 用电器：红石灯按 hasNeighborSignal 判定（含导体强充能）；
//   - 组件（中继器/比较器/火把/侦测器/按钮/活塞）通过统一的计划刻表驱动，
//     “同一位置已有计划刻时忽略新的调度”（与原版一致）；
//   - 拉杆/按钮/中继器/比较器右键交互、按钮到时自动弹起；
//   - 变更传播按原版：拉杆/按钮额外通知附着方块的邻居，中继器/比较器/
//     侦测器额外通知输出侧方块及其邻居。
//
// 已知近似（见 docs/TODO.md）：计划刻无优先级（原版优先级影响同 tick 内的
// 结算顺序）。

const (
	buttonStoneTicks = 20
	buttonWoodTicks  = 30
	// lampOffDelayTicks 是红石灯从亮到灭的延迟（原版 4 tick）。
	lampOffDelayTicks = 4

	wireName              = "minecraft:redstone_wire"
	leverName             = "minecraft:lever"
	lampName              = "minecraft:redstone_lamp"
	stoneButtonName       = "minecraft:stone_button"
	oakButtonName         = "minecraft:oak_button"
	redstoneTorchName     = "minecraft:redstone_torch"
	redstoneWallTorchName = "minecraft:redstone_wall_torch"
	redstoneBlockName     = "minecraft:redstone_block"
	repeaterName          = "minecraft:repeater"
	comparatorName        = "minecraft:comparator"
	observerName          = "minecraft:observer"
)

// isWireState 报告方块状态是否为红石线。
func isWireState(state uint16) bool {
	return registry.IsPropertyBlockState(wireName, state)
}

// isLampState 报告方块状态是否为红石灯。
func isLampState(state uint16) bool {
	return registry.IsPropertyBlockState(lampName, state)
}

// isTorchState 报告方块状态是否为（立式或墙式）红石火把。
func isTorchState(state uint16) bool {
	return registry.IsPropertyBlockState(redstoneTorchName, state) ||
		registry.IsPropertyBlockState(redstoneWallTorchName, state)
}

// wirePowerOf 返回红石线状态当前的 power 属性值（非红石线返回 0）。
func wirePowerOf(state uint16) int {
	name, props, ok := registry.StateProps(state)
	if !ok || name != wireName {
		return 0
	}
	power, err := strconv.Atoi(props["power"])
	if err != nil {
		return 0
	}
	return power
}

// handleRedstoneUse 处理右键拉杆/按钮/中继器/比较器。
// 返回是否已处理该交互（true 时调用方不应再执行其他交互）。
func (s *Server) handleRedstoneUse(w *world.World, x, y, z int, state uint16) bool {
	name, props, ok := registry.StateProps(state)
	if !ok {
		return false
	}
	pos := [3]int{x, y, z}
	switch {
	case name == leverName:
		next := cloneProps(props)
		if next["powered"] == "true" {
			next["powered"] = "false"
		} else {
			next["powered"] = "true"
		}
		s.setRedstoneBlock(w, x, y, z, leverName, next)
		return true
	case name == repeaterName:
		// 右键循环调整延迟（1 → 2 → 3 → 4 → 1），与原版一致。
		delay, err := strconv.Atoi(props["delay"])
		if err != nil || delay < 1 || delay > 4 {
			delay = 1
		}
		next := cloneProps(props)
		next["delay"] = strconv.Itoa(delay%4 + 1)
		s.setRedstoneBlock(w, x, y, z, repeaterName, next)
		return true
	case name == comparatorName:
		// 右键切换 compare/subtract 模式并立即刷新输出（原版 refreshOutputState）。
		next := cloneProps(props)
		if next["mode"] == "subtract" {
			next["mode"] = "compare"
		} else {
			next["mode"] = "subtract"
		}
		if s.setRedstoneBlock(w, x, y, z, comparatorName, next) {
			s.refreshComparatorOutput(w, pos)
		}
		return true
	case isButtonName(name):
		if props["powered"] == "true" {
			return true // 已按下：忽略重复点击
		}
		next := cloneProps(props)
		next["powered"] = "true"
		if !s.setRedstoneBlock(w, x, y, z, name, next) {
			return true
		}
		ticks := buttonWoodTicks
		if name == stoneButtonName || name == "minecraft:polished_blackstone_button" {
			ticks = buttonStoneTicks
		}
		s.scheduleRedstoneTick(w.Dimension(), pos, ticks)
		return true
	}
	return false
}

// cloneProps 复制属性集合（避免修改注册表共享数据）。
func cloneProps(props map[string]string) map[string]string {
	cloned := make(map[string]string, len(props)+1)
	for key, value := range props {
		cloned[key] = value
	}
	return cloned
}

// setRedstoneBlock 设置红石方块的新状态并广播方块更新，随后重算周边红石；
// 成功返回 true。
func (s *Server) setRedstoneBlock(w *world.World, x, y, z int, name string, props map[string]string) bool {
	state, ok := registry.BlockStateWithProps(name, props)
	if !ok {
		return false
	}
	if !w.SetBlock(x, y, z, state) {
		return false
	}
	s.broadcastBlockUpdate(w.Dimension(), x, y, z, int32(state))
	s.updateRedstoneAround(w.Dimension(), x, y, z)
	return true
}

// scheduleRedstoneTick 为位置安排一次计划刻；该位置已有计划刻时忽略
// （与原版“同一方块同一类型的计划刻只保留一个”一致）。
func (s *Server) scheduleRedstoneTick(dim world.Dimension, pos [3]int, ticks int) {
	if ticks < 1 {
		ticks = 1
	}
	s.redstoneMu.Lock()
	if s.redstoneTicks == nil {
		s.redstoneTicks = make(map[containerKey]int)
	}
	key := containerKey{dim: dim, x: pos[0], y: pos[1], z: pos[2]}
	if _, exists := s.redstoneTicks[key]; !exists {
		s.redstoneTicks[key] = ticks
	}
	s.redstoneMu.Unlock()
}

// hasRedstoneTick 报告位置是否已有挂起的红石计划刻。
func (s *Server) hasRedstoneTick(dim world.Dimension, pos [3]int) bool {
	s.redstoneMu.Lock()
	_, exists := s.redstoneTicks[containerKey{dim: dim, x: pos[0], y: pos[1], z: pos[2]}]
	s.redstoneMu.Unlock()
	return exists
}

// redstoneTickCountNow 返回当前红石 tick 计数（火把熔断判定用）。
func (s *Server) redstoneTickCountNow() int64 {
	s.redstoneMu.Lock()
	defer s.redstoneMu.Unlock()
	return s.redstoneTickCount
}

// tickRedstone 推进红石计划刻（由主 tick 每帧调用）。
func (s *Server) tickRedstone() {
	s.redstoneMu.Lock()
	s.redstoneTickCount++
	var fires []containerKey
	for key, remaining := range s.redstoneTicks {
		remaining--
		if remaining <= 0 {
			fires = append(fires, key)
			delete(s.redstoneTicks, key)
			continue
		}
		s.redstoneTicks[key] = remaining
	}
	s.redstoneMu.Unlock()

	for _, key := range fires {
		s.fireRedstoneTick(key)
	}
}

// fireRedstoneTick 触发到期计划刻：按当前位置的方块类型分派。
func (s *Server) fireRedstoneTick(key containerKey) {
	w := s.worldFor(key.dim)
	if w == nil {
		return
	}
	pos := [3]int{key.x, key.y, key.z}
	if _, ok := world.SectionIndex(pos[1]); !ok {
		return
	}
	state := w.BlockAt(pos[0], pos[1], pos[2])
	name, _, ok := registry.StateProps(state)
	if !ok {
		return
	}
	switch {
	case name == repeaterName:
		s.tickRepeater(w, key.dim, pos, state)
	case name == comparatorName:
		s.tickComparator(w, key.dim, pos, state)
	case name == redstoneTorchName || name == redstoneWallTorchName:
		s.tickTorch(w, key.dim, pos, state)
	case name == observerName:
		s.tickObserver(w, key.dim, pos, state)
	case name == pistonName || name == stickyPistonName:
		s.tickPiston(w, key.dim, pos, state)
	case isDispenserBlockName(name):
		s.tickDispenser(w, key.dim, pos, state)
	case name == fireBlockName || name == soulFireBlockName:
		s.tickFire(w, key.dim, pos, state)
	case isButtonName(name):
		s.tickButton(w, key.dim, pos, state)
	case isLampState(state):
		s.tickLamp(w, key.dim, pos, state)
	default:
		if _, ok := plateStateTable[state]; ok {
			s.tickPressurePlate(w, key.dim, pos, state)
		}
	}
}

// tickButton 处理按钮到期：弹起并重算周边红石。
func (s *Server) tickButton(w *world.World, dim world.Dimension, pos [3]int, state uint16) {
	name, props, ok := registry.StateProps(state)
	if !ok || !isButtonName(name) || props["powered"] != "true" {
		return
	}
	next := cloneProps(props)
	next["powered"] = "false"
	s.setRedstoneBlock(w, pos[0], pos[1], pos[2], name, next)
}

// neighborOffsets6 是六个正交方向的偏移。
var neighborOffsets6 = [6][3]int{
	{1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0}, {0, 0, 1}, {0, 0, -1},
}

// updateRedstoneAround 在方块变更后重算红石网络与组件：
// 重算连通的红石线网络（批量广播变化），并刷新周边用电器/二极管/火把/活塞。
//
// 除变更点自身外，还按原版规则把“传播源”计入更新范围：
//   - 拉杆/按钮：附着方块（LeverBlock/ButtonBlock.updateNeighbours
//     会更新附着方块及其邻居，如火把放在附着方块上时靠它被通知）；
//   - 中继器/比较器/侦测器：输出侧方块（DiodeBlock.updateNeighborsInFront
//     更新输出侧方块及其邻居，如“中继器 → 方块 → 上方红石线”）。
func (s *Server) updateRedstoneAround(dim world.Dimension, x, y, z int) {
	w := s.worldFor(dim)
	if w == nil {
		return
	}
	pos := [3]int{x, y, z}
	origins := append([][3]int{pos}, s.redstonePropagationOrigins(w, pos)...)

	// 种子：每个源自身与六个相邻位置中的红石线。
	seeds := make([][3]int, 0, 7*len(origins))
	for _, origin := range origins {
		if isWireState(s.blockIfLoaded(w, origin)) {
			seeds = append(seeds, origin)
		}
		for _, offset := range neighborOffsets6 {
			neighbor := [3]int{origin[0] + offset[0], origin[1] + offset[1], origin[2] + offset[2]}
			if isWireState(s.blockIfLoaded(w, neighbor)) {
				seeds = append(seeds, neighbor)
			}
		}
	}

	changed := s.recomputeWireNetworks(w, dim, seeds)
	points := append(append([][3]int(nil), origins...), changed...)
	for _, point := range points {
		s.refreshRedstoneConsumers(w, dim, point)
	}
}

// redstonePropagationOrigins 返回原版组件在状态变化时额外通知的方块位置
// （见 updateRedstoneAround 的注释）。
func (s *Server) redstonePropagationOrigins(w *world.World, pos [3]int) [][3]int {
	state := s.blockIfLoaded(w, pos)
	name, props, ok := registry.StateProps(state)
	if !ok {
		return nil
	}
	switch {
	case name == leverName || isButtonName(name):
		// connectedDirection 指向“支撑方块 → 组件”，附着方块在其反方向。
		support := offsetDir(pos, oppositeDir(connectedDirection(props)))
		return [][3]int{support}
	case name == repeaterName || name == comparatorName:
		output := offsetDir(pos, oppositeDir(props["facing"]))
		return [][3]int{output}
	case name == observerName:
		output := offsetDir(pos, oppositeDir(props["facing"]))
		return [][3]int{output}
	case name == redstoneTorchName:
		return nil
	}
	return nil
}

// recomputeWireNetworks 重算包含种子的红石线网络：线间传播迭代至收敛，
// 变化的状态批量写回并广播。返回发生变化的线位置。
func (s *Server) recomputeWireNetworks(w *world.World, dim world.Dimension, seeds [][3]int) [][3]int {
	if len(seeds) == 0 {
		return nil
	}
	// BFS 收集网络（六向连通 + 原版 getIncomingWireSignal 的上下跨格关系）。
	visited := make(map[[3]int]bool)
	network := make([][3]int, 0, len(seeds))
	queue := append([][3]int(nil), seeds...)
	for len(queue) > 0 {
		point := queue[0]
		queue = queue[1:]
		if visited[point] {
			continue
		}
		visited[point] = true
		if !isWireState(s.blockIfLoaded(w, point)) {
			continue
		}
		network = append(network, point)
		queue = append(queue, s.wireNetworkNeighbors(w, point)...)
	}
	if len(network) == 0 {
		return nil
	}

	// 初始值：外部电源（相邻方块发射值，红石线按原版在查询中返回 0）。
	power := make(map[[3]int]int, len(network))
	for _, point := range network {
		power[point] = s.wireExternalPower(w, point)
	}

	// 线间传播：max(0, incoming-1)，多轮松弛至收敛（与原版多轮
	// 邻居更新的稳态一致）。
	horizontal := [4]string{"north", "south", "west", "east"}
	for iter := 0; iter < 16; iter++ {
		changed := false
		for _, point := range network {
			incoming := 0
			above := [3]int{point[0], point[1] + 1, point[2]}
			aboveState := s.blockIfLoaded(w, above)
			for _, dir := range horizontal {
				offset := facingOffsets[dir]
				neighbor := [3]int{point[0] + offset[0], point[1] + offset[1], point[2] + offset[2]}
				neighborState := s.blockIfLoaded(w, neighbor)
				if isWireState(neighborState) {
					if value := power[neighbor]; value > incoming {
						incoming = value
					}
				}
				// 原版 getIncomingWireSignal：邻居是导体且本线上方不是导体时，
				// 取邻居“上方”的线（向上爬）；邻居不是导体时取邻居“下方”的线
				// （从更低一层绕行上来/向下走）。
				if s.conductorState(neighborState) && !s.conductorState(aboveState) {
					aboveNeighbor := [3]int{neighbor[0], neighbor[1] + 1, neighbor[2]}
					if isWireState(s.blockIfLoaded(w, aboveNeighbor)) {
						if value := power[aboveNeighbor]; value > incoming {
							incoming = value
						}
					}
				} else if !s.conductorState(neighborState) {
					belowNeighbor := [3]int{neighbor[0], neighbor[1] - 1, neighbor[2]}
					if isWireState(s.blockIfLoaded(w, belowNeighbor)) {
						if value := power[belowNeighbor]; value > incoming {
							incoming = value
						}
					}
				}
			}
			if incoming > 0 {
				incoming--
			}
			candidate := power[point]
			if incoming > candidate {
				candidate = incoming
			}
			if candidate != power[point] {
				power[point] = candidate
				changed = true
			}
		}
		if !changed {
			break
		}
	}

	// 写回变化。
	var changed [][3]int
	for _, point := range network {
		current := s.blockIfLoaded(w, point)
		next := s.wireStateFor(w, point, power[point])
		if next == current {
			continue
		}
		if w.SetBlock(point[0], point[1], point[2], next) {
			s.broadcastBlockUpdate(dim, point[0], point[1], point[2], int32(next))
			changed = append(changed, point)
		}
	}
	return changed
}

// wireNetworkNeighbors 返回与红石线 point 存在直接供能关系的线位置：
// 六向相邻的线，以及原版 getIncomingWireSignal 中的上下跨格关系
// （两个方向都算：point 的邻居上下方、point 的斜上/斜下邻居）。
// 额外的边只会扩大重算范围，不会引入错误的供电关系（供电判定本身
// 由 recomputeWireNetworks 中的规则给出）。
func (s *Server) wireNetworkNeighbors(w *world.World, point [3]int) [][3]int {
	neighbors := make([][3]int, 0, 14)
	for _, offset := range neighborOffsets6 {
		neighbors = append(neighbors, [3]int{point[0] + offset[0], point[1] + offset[1], point[2] + offset[2]})
	}
	aboveState := s.blockIfLoaded(w, [3]int{point[0], point[1] + 1, point[2]})
	belowState := s.blockIfLoaded(w, [3]int{point[0], point[1] - 1, point[2]})
	aboveOpen := !s.conductorState(aboveState)
	belowConductive := s.conductorState(belowState)
	for _, dir := range [4]string{"north", "south", "west", "east"} {
		offset := facingOffsets[dir]
		forward := [3]int{point[0] + offset[0], point[1], point[2] + offset[2]}
		backward := [3]int{point[0] - offset[0], point[1], point[2] - offset[2]}
		forwardState := s.blockIfLoaded(w, forward)
		backwardState := s.blockIfLoaded(w, backward)
		// 本线从“邻居下方/上方”绕行的线收到信号（incoming 方向）。
		if s.conductorState(forwardState) && aboveOpen {
			neighbors = append(neighbors, [3]int{forward[0], forward[1] + 1, forward[2]})
		} else if !s.conductorState(forwardState) {
			neighbors = append(neighbors, [3]int{forward[0], forward[1] - 1, forward[2]})
		}
		// 本线向“反方向的斜上/斜下”的线供电（outgoing 方向）。
		if aboveOpen {
			neighbors = append(neighbors, [3]int{backward[0], backward[1] + 1, backward[2]})
		}
		if belowConductive && !s.conductorState(backwardState) {
			neighbors = append(neighbors, [3]int{backward[0], backward[1] - 1, backward[2]})
		}
	}
	return neighbors
}

// wireExternalPower 计算红石线的外部电源（原版 getBlockSignal，即
// shouldSignal=false 下的 getBestNeighborSignal）：相邻六向方块的发射值最大值，
// 红石线按原版在该查询中返回 0。
func (s *Server) wireExternalPower(w *world.World, pos [3]int) int {
	best := 0
	for _, offset := range neighborOffsets6 {
		neighbor := [3]int{pos[0] + offset[0], pos[1] + offset[1], pos[2] + offset[2]}
		if _, ok := world.SectionIndex(neighbor[1]); !ok {
			continue
		}
		state := w.BlockAt(neighbor[0], neighbor[1], neighbor[2])
		if isWireState(state) {
			continue
		}
		dir, ok := dirBetween(pos, neighbor)
		if !ok {
			continue
		}
		if signal := s.emittedSignal(w, neighbor, dir); signal > best {
			best = signal
			if best >= 15 {
				return 15
			}
		}
	}
	return best
}

// wireStateFor 构造红石线在指定供电强度下的方块状态（含原版连接方向规则）。
func (s *Server) wireStateFor(w *world.World, pos [3]int, power int) uint16 {
	if power < 0 {
		power = 0
	}
	props := map[string]string{"power": strconv.Itoa(power)}
	aboves := s.blockIfLoaded(w, [3]int{pos[0], pos[1] + 1, pos[2]})
	canConnectUp := !s.conductorState(aboves)
	connected := map[string]bool{}
	for _, direction := range redstoneDirections {
		connection := s.wireConnectionTo(w, pos, direction, canConnectUp)
		props[direction.prop] = connection
		connected[direction.prop] = connection != "none"
	}
	// 原版 getConnectionState 的“直线贯通”规则：若南北方向都未连接，
	// 则东西方向强制连接；反之亦然（这让线段的端点也能指向机构方块）。
	noNS := !connected["north"] && !connected["south"]
	noEW := !connected["east"] && !connected["west"]
	if noNS {
		if !connected["west"] {
			props["west"] = "side"
		}
		if !connected["east"] {
			props["east"] = "side"
		}
	}
	if noEW {
		if !connected["north"] {
			props["north"] = "side"
		}
		if !connected["south"] {
			props["south"] = "side"
		}
	}
	state, ok := registry.BlockStateWithProps(wireName, props)
	if !ok {
		// 理论上不可能（属性组合均存在）；回退到默认状态避免发送无效 ID。
		return world.AirBlock
	}
	return state
}

// wireConnectionTo 返回红石线对某水平方向的连接类型（none/side/up），
// 对应原版 RedStoneWireBlock.getConnectingSide。
func (s *Server) wireConnectionTo(w *world.World, pos [3]int, direction struct {
	prop string
	dx   int
	dz   int
}, canConnectUp bool) string {
	neighbor := [3]int{pos[0] + direction.dx, pos[1], pos[2] + direction.dz}
	if _, ok := world.SectionIndex(neighbor[1]); !ok {
		return "none"
	}
	neighborState := w.BlockAt(neighbor[0], neighbor[1], neighbor[2])
	if canConnectUp && s.canSurviveWireOn(neighborState) {
		aboveNeighbor := [3]int{neighbor[0], neighbor[1] + 1, neighbor[2]}
		aboveState := s.blockIfLoaded(w, aboveNeighbor)
		if isWireState(aboveState) {
			// 邻居上方的线让本线“爬升”过去：面完整时为 up，否则为 side。
			if s.faceSturdy(neighborState, direction.prop) {
				return "up"
			}
			return "side"
		}
	}
	if s.shouldConnectWireTo(neighborState, direction.prop) {
		return "side"
	}
	if s.conductorState(neighborState) {
		return "none"
	}
	belowNeighbor := [3]int{neighbor[0], neighbor[1] - 1, neighbor[2]}
	if isWireState(s.blockIfLoaded(w, belowNeighbor)) {
		// 邻居下方有红石线：连接（side 也表示向下连接）。
		return "side"
	}
	return "none"
}

// shouldConnectWireTo 对应原版 RedStoneWireBlock.shouldConnectTo（带方向版本）：
// 红石线连向线自身、中继器（两端均可）、侦测器（正面）以及所有信号源方块。
func (s *Server) shouldConnectWireTo(state uint16, dir string) bool {
	name, props, ok := registry.StateProps(state)
	if !ok {
		current, hasName := s.blockNameOf(state)
		return hasName && isSignalSourceName(current)
	}
	switch name {
	case wireName:
		return true
	case repeaterName:
		return props["facing"] == dir || oppositeDir(props["facing"]) == dir
	case observerName:
		return props["facing"] == dir
	}
	return isSignalSourceName(name)
}

// canSurviveWireOn 对应原版 RedStoneWireBlock.canSurviveOn：
// 方块顶面完整（可放置红石线）或是漏斗。
func (s *Server) canSurviveWireOn(state uint16) bool {
	if name, ok := s.blockNameOf(state); ok && name == "minecraft:hopper" {
		return true
	}
	return s.faceSturdy(state, "up")
}

// faceSturdy 报告方块在指定方向的表面是否完整（原版 isFaceSturdy 的形状近似）：
// 存在覆盖该整面的碰撞形状盒。
func (s *Server) faceSturdy(state uint16, dir string) bool {
	boxes := registry.BlockStateShape(state)
	for _, box := range boxes {
		switch dir {
		case "up":
			if box.Y2 == 16 && box.X1 == 0 && box.X2 == 16 && box.Z1 == 0 && box.Z2 == 16 {
				return true
			}
		case "down":
			if box.Y1 == 0 && box.X1 == 0 && box.X2 == 16 && box.Z1 == 0 && box.Z2 == 16 {
				return true
			}
		case "west":
			if box.X1 == 0 && box.Y1 == 0 && box.Y2 == 16 && box.Z1 == 0 && box.Z2 == 16 {
				return true
			}
		case "east":
			if box.X2 == 16 && box.Y1 == 0 && box.Y2 == 16 && box.Z1 == 0 && box.Z2 == 16 {
				return true
			}
		case "north":
			if box.Z1 == 0 && box.X1 == 0 && box.X2 == 16 && box.Y1 == 0 && box.Y2 == 16 {
				return true
			}
		case "south":
			if box.Z2 == 16 && box.X1 == 0 && box.X2 == 16 && box.Y1 == 0 && box.Y2 == 16 {
				return true
			}
		}
	}
	return false
}

// redstoneDirections 是红石线连接属性的四个水平方向。
var redstoneDirections = [4]struct {
	prop string
	dx   int
	dz   int
}{
	{"north", 0, -1},
	{"south", 0, 1},
	{"west", -1, 0},
	{"east", 1, 0},
}

// refreshRedstoneConsumers 刷新 (x, y, z) 及其六邻域中的用电器与组件：
// 红石灯按 hasNeighborSignal 联动；二极管重算锁定/计划刻；火把重算计划刻；
// 活塞（含准连接）重算激活状态。
func (s *Server) refreshRedstoneConsumers(w *world.World, dim world.Dimension, pos [3]int) {
	candidates := append([][3]int{pos}, neighborsOf(pos)...)
	for _, candidate := range candidates {
		state := s.blockIfLoaded(w, candidate)
		switch {
		case isLampState(state):
			s.refreshLamp(w, candidate, state)
		case isDiodeState(state):
			s.checkDiodeUpdate(w, dim, candidate, state)
		case isTorchState(state):
			s.checkTorchUpdate(w, dim, candidate, state)
		case isHopperState(state):
			s.refreshHopperEnabled(w, candidate, state)
		}
		if name, _, ok := registry.StateProps(state); ok && isDispenserBlockName(name) {
			s.refreshDispenser(w, candidate, state)
		}
	}
	// 活塞：变更点与其邻居处的活塞（普通供电），以及它们下方的活塞（准连接）。
	for _, candidate := range candidates {
		s.checkPistonAt(w, dim, candidate)
		below := [3]int{candidate[0], candidate[1] - 1, candidate[2]}
		s.checkPistonAt(w, dim, below)
	}
}

// refreshLamp 按 hasNeighborSignal 更新红石灯亮灭（含导体强充能）。
// 原版 RedstoneLampBlock 语义：点亮立即生效；从亮转灭需要 4 tick
// （计划刻到点后再检查一次），熄灭延迟期间恢复供电则取消。
func (s *Server) refreshLamp(w *world.World, pos [3]int, state uint16) {
	_, props, ok := registry.StateProps(state)
	if !ok || props == nil {
		return
	}
	signal := s.bestNeighborSignal(w, pos) > 0
	if signal == (props["lit"] == "true") {
		return
	}
	if !signal {
		// 亮 → 灭：安排 4 tick 后的熄灭（已有计划刻则忽略）。
		if !s.hasRedstoneTick(w.Dimension(), pos) {
			s.scheduleRedstoneTick(w.Dimension(), pos, lampOffDelayTicks)
		}
		return
	}
	next := cloneProps(props)
	next["lit"] = "true"
	s.setRedstoneBlock(w, pos[0], pos[1], pos[2], lampName, next)
}

// tickLamp 处理红石灯计划刻（原版 RedstoneLampBlock.tick）：
// 仍处于熄灭延迟中且无信号时真正熄灭。
func (s *Server) tickLamp(w *world.World, dim world.Dimension, pos [3]int, state uint16) {
	_, props, ok := registry.StateProps(state)
	if !ok || props["lit"] != "true" {
		return
	}
	if s.bestNeighborSignal(w, pos) > 0 {
		return // 延迟期间恢复供电：保持点亮
	}
	next := cloneProps(props)
	next["lit"] = "false"
	s.setRedstoneBlock(w, pos[0], pos[1], pos[2], lampName, next)
}

// neighborsOf 返回六个正交邻居坐标。
func neighborsOf(pos [3]int) [][3]int {
	neighbors := make([][3]int, 0, 6)
	for _, offset := range neighborOffsets6 {
		neighbors = append(neighbors, [3]int{pos[0] + offset[0], pos[1] + offset[1], pos[2] + offset[2]})
	}
	return neighbors
}
