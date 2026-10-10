package server

import (
	"strconv"

	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 红石系统（简化但功能可用）：
//
//   - 电源：拉杆/按钮（powered=true）、红石块、红石火把（常亮 15）；
//   - 红石线（redstone_wire）：同一网络内按“电源 15、每格 -1”衰减，
//     多个电源取最大值（BFS 重算，等价于原版取最大值的传播规则）；
//   - 用电器：红石灯（相邻电源或 power>0 的红石线点亮）；
//   - 触发：放置/破坏方块、切换拉杆/按钮后重算相连网络；
//   - 按钮：按下后 20 tick（石头）/30 tick（木质）自动弹起。
//
// 已知简化（见 docs/TODO.md）：不实现中继器/比较器/活塞/侦测器、方块
// 强充能（电源只通过红石线与相邻用电器作用）、原版的逐方向连接强度规则；
// 红石线的连接方向按“同高相邻为侧连、上一格为升线”近似渲染。
const (
	buttonStoneTicks = 20
	buttonWoodTicks  = 30

	wireName              = "minecraft:redstone_wire"
	leverName             = "minecraft:lever"
	lampName              = "minecraft:redstone_lamp"
	stoneButtonName       = "minecraft:stone_button"
	oakButtonName         = "minecraft:oak_button"
	redstoneTorchName     = "minecraft:redstone_torch"
	redstoneWallTorchName = "minecraft:redstone_wall_torch"
	redstoneBlockName     = "minecraft:redstone_block"
)

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

// isWireState 报告方块状态是否为红石线。
func isWireState(state uint16) bool {
	return registry.IsPropertyBlockState(wireName, state)
}

// isLampState 报告方块状态是否为红石灯。
func isLampState(state uint16) bool {
	return registry.IsPropertyBlockState(lampName, state)
}

// isButtonState 报告方块状态是否为按钮。
func isButtonState(state uint16) bool {
	return registry.IsPropertyBlockState(stoneButtonName, state) ||
		registry.IsPropertyBlockState(oakButtonName, state)
}

// redstoneSourcePower 返回方块作为电源的强度（0 表示不是电源）。
// 本实现中电源强度固定为 15。红石火把只有点亮（lit=true）时供电；
// 拉杆/按钮看 powered 属性；红石块恒为电源。
func redstoneSourcePower(state uint16) int {
	name, props, ok := registry.StateProps(state)
	if !ok {
		return 0
	}
	switch name {
	case redstoneTorchName, redstoneWallTorchName:
		if props["lit"] == "true" {
			return 15
		}
	case leverName, stoneButtonName, oakButtonName:
		if props["powered"] == "true" {
			return 15
		}
	case redstoneBlockName:
		return 15
	case repeaterName, observerName:
		// 已通电的中继器/侦测器向输出侧供电 15。
		if props["powered"] == "true" {
			return 15
		}
	}
	return 0
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

// handleRedstoneUse 处理右键拉杆/按钮：切换状态并重算红石网络。
// 返回是否已处理该交互（true 时调用方不应再执行其他交互）。
func (s *Server) handleRedstoneUse(w *world.World, x, y, z int, state uint16) bool {
	name, props, ok := registry.StateProps(state)
	if !ok {
		// 红石块/火把等无交互电源；红石线无交互。
		return false
	}
	switch name {
	case leverName:
		next := cloneProps(props)
		if next["powered"] == "true" {
			next["powered"] = "false"
		} else {
			next["powered"] = "true"
		}
		s.setRedstoneBlock(w, x, y, z, leverName, next)
		return true
	case repeaterName:
		// 右键循环调整延迟（1 → 2 → 3 → 4 → 1），与原版一致。
		delay, err := strconv.Atoi(props["delay"])
		if err != nil || delay < 1 || delay > 4 {
			delay = 1
		}
		next := cloneProps(props)
		next["delay"] = strconv.Itoa(delay%4 + 1)
		s.setRedstoneBlock(w, x, y, z, repeaterName, next)
		return true
	case stoneButtonName, oakButtonName:
		if props["powered"] == "true" {
			return true // 已按下：忽略重复点击
		}
		next := cloneProps(props)
		next["powered"] = "true"
		if !s.setRedstoneBlock(w, x, y, z, name, next) {
			return true
		}
		ticks := buttonStoneTicks
		if name == oakButtonName {
			ticks = buttonWoodTicks
		}
		s.redstoneMu.Lock()
		if s.redstoneButtons == nil {
			s.redstoneButtons = make(map[containerKey]int)
		}
		s.redstoneButtons[containerKey{dim: w.Dimension(), x: x, y: y, z: z}] = ticks
		s.redstoneMu.Unlock()
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

// setRedstoneBlock 设置红石方块的新状态并广播方块更新；成功返回 true。
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

// tickRedstone 推进按钮弹起计时（由 tick 每帧调用）。
func (s *Server) tickRedstone() {
	s.redstoneMu.Lock()
	var expired []containerKey
	for key, ticks := range s.redstoneButtons {
		if ticks <= 1 {
			expired = append(expired, key)
			delete(s.redstoneButtons, key)
			continue
		}
		s.redstoneButtons[key] = ticks - 1
	}
	s.redstoneMu.Unlock()
	for _, key := range expired {
		w := s.worldFor(key.dim)
		state := w.BlockAt(key.x, key.y, key.z)
		name, props, ok := registry.StateProps(state)
		if !ok || (name != stoneButtonName && name != oakButtonName) || props["powered"] != "true" {
			continue // 按钮已被破坏或替换
		}
		next := cloneProps(props)
		next["powered"] = "false"
		s.setRedstoneBlock(w, key.x, key.y, key.z, name, next)
	}
	// 中继器延迟切换与侦测器脉冲。
	s.tickRedstoneExtra()
}

// neighborOffsets6 是六个正交方向的偏移。
var neighborOffsets6 = [6][3]int{
	{1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0}, {0, 0, 1}, {0, 0, -1},
}

// updateRedstoneAround 在方块变更后重算与 (x, y, z) 相连的红石网络：
// 重算红石线的 power 与连接方向，并刷新相邻红石灯的亮灭。
func (s *Server) updateRedstoneAround(dim world.Dimension, x, y, z int) {
	w := s.worldFor(dim)

	// 种子：自身与六个相邻位置中的红石线。
	var seeds [][3]int
	if isWireState(w.BlockAt(x, y, z)) {
		seeds = append(seeds, [3]int{x, y, z})
	}
	for _, offset := range neighborOffsets6 {
		pos := [3]int{x + offset[0], y + offset[1], z + offset[2]}
		if isWireState(w.BlockAt(pos[0], pos[1], pos[2])) {
			seeds = append(seeds, pos)
		}
	}

	visited := make(map[[3]int]bool)
	network := make([][3]int, 0, len(seeds))
	queue := append([][3]int(nil), seeds...)
	for len(queue) > 0 {
		pos := queue[0]
		queue = queue[1:]
		if visited[pos] {
			continue
		}
		visited[pos] = true
		if !isWireState(w.BlockAt(pos[0], pos[1], pos[2])) {
			continue
		}
		network = append(network, pos)
		for _, offset := range neighborOffsets6 {
			queue = append(queue, [3]int{pos[0] + offset[0], pos[1] + offset[1], pos[2] + offset[2]})
		}
	}

	if len(network) > 0 {
		power := s.computeWirePower(w, network)
		var changed []struct {
			pos   [3]int
			state uint16
		}
		for _, pos := range network {
			state := s.wireStateFor(w, pos, power[pos])
			current := w.BlockAt(pos[0], pos[1], pos[2])
			if state == current {
				continue
			}
			if w.SetBlock(pos[0], pos[1], pos[2], state) {
				changed = append(changed, struct {
					pos   [3]int
					state uint16
				}{pos: pos, state: state})
			}
		}
		for _, change := range changed {
			s.broadcastBlockUpdate(dim, change.pos[0], change.pos[1], change.pos[2], int32(change.state))
		}
	}

	// 用电器：变更点周边与网络中的红线周边。
	consumers := make([][3]int, 0, len(network)+1)
	consumers = append(consumers, [3]int{x, y, z})
	consumers = append(consumers, network...)
	for _, pos := range consumers {
		s.refreshRedstoneConsumers(w, pos)
	}
}

// computeWirePower 计算网络内每条红石线的供电强度（BFS 从电源衰减）。
func (s *Server) computeWirePower(w *world.World, network [][3]int) map[[3]int]int {
	power := make(map[[3]int]int, len(network))
	inNetwork := make(map[[3]int]bool, len(network))
	for _, pos := range network {
		inNetwork[pos] = true
	}
	type node struct {
		pos   [3]int
		level int
	}
	var queue []node
	// 电源：网络线相邻（六向）的电源方块直接给该线供电 15。
	for _, pos := range network {
		for _, offset := range neighborOffsets6 {
			state := w.BlockAt(pos[0]+offset[0], pos[1]+offset[1], pos[2]+offset[2])
			if sourcePower := redstoneSourcePower(state); sourcePower > 0 && power[pos] < sourcePower {
				power[pos] = sourcePower
				queue = append(queue, node{pos: pos, level: sourcePower})
			}
		}
	}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if power[current.pos] != current.level || current.level <= 1 {
			continue
		}
		for _, offset := range neighborOffsets6 {
			next := [3]int{current.pos[0] + offset[0], current.pos[1] + offset[1], current.pos[2] + offset[2]}
			if !inNetwork[next] {
				continue
			}
			if power[next] < current.level-1 {
				power[next] = current.level - 1
				queue = append(queue, node{pos: next, level: current.level - 1})
			}
		}
	}
	return power
}

// wireStateFor 构造红石线在指定供电强度下的方块状态（含连接方向近似）。
func (s *Server) wireStateFor(w *world.World, pos [3]int, power int) uint16 {
	if power < 0 {
		power = 0
	}
	props := map[string]string{"power": strconv.Itoa(power)}
	for _, direction := range redstoneDirections {
		nx, nz := pos[0]+direction.dx, pos[2]+direction.dz
		switch {
		case isWireState(w.BlockAt(nx, pos[1], nz)):
			props[direction.prop] = "side"
		case isWireState(w.BlockAt(nx, pos[1]+1, nz)):
			props[direction.prop] = "up"
		default:
			props[direction.prop] = "none"
		}
	}
	state, ok := registry.BlockStateWithProps(wireName, props)
	if !ok {
		// 理论上不可能（属性组合均存在）；回退到默认状态避免发送无效 ID。
		return world.AirBlock
	}
	return state
}

// refreshRedstoneConsumers 刷新 (x, y, z) 及其六邻域中的红石灯亮灭与
// 中继器输入评估。
func (s *Server) refreshRedstoneConsumers(w *world.World, pos [3]int) {
	candidates := append([][3]int{pos}, neighborsOf(pos)...)
	for _, candidate := range candidates {
		state := w.BlockAt(candidate[0], candidate[1], candidate[2])
		if isRepeaterState(state) {
			// 中继器：输入侧变化时安排延迟切换。
			s.evaluateRepeater(w, candidate, state)
			continue
		}
		if !isLampState(state) {
			continue
		}
		name, props, ok := registry.StateProps(state)
		if !ok || name != lampName {
			continue
		}
		lit := s.lampPoweredAt(w, candidate)
		wantLit := props["lit"] == "true"
		if lit == wantLit {
			continue
		}
		next := cloneProps(props)
		if lit {
			next["lit"] = "true"
		} else {
			next["lit"] = "false"
		}
		newState, ok := registry.BlockStateWithProps(lampName, next)
		if !ok {
			continue
		}
		if w.SetBlock(candidate[0], candidate[1], candidate[2], newState) {
			s.broadcastBlockUpdate(w.Dimension(), candidate[0], candidate[1], candidate[2], int32(newState))
		}
	}
}

// lampPoweredAt 报告红石灯是否应被点亮：相邻六向中存在电源或 power>0 的红石线。
func (s *Server) lampPoweredAt(w *world.World, pos [3]int) bool {
	for _, offset := range neighborOffsets6 {
		state := w.BlockAt(pos[0]+offset[0], pos[1]+offset[1], pos[2]+offset[2])
		if redstoneSourcePower(state) > 0 {
			return true
		}
		if isWireState(state) && wirePowerOf(state) > 0 {
			return true
		}
	}
	return false
}

// neighborsOf 返回六个正交邻居坐标。
func neighborsOf(pos [3]int) [][3]int {
	neighbors := make([][3]int, 0, 6)
	for _, offset := range neighborOffsets6 {
		neighbors = append(neighbors, [3]int{pos[0] + offset[0], pos[1] + offset[1], pos[2] + offset[2]})
	}
	return neighbors
}
