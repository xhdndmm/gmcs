//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"strconv"

	"gmcs/internal/item"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 红石组件：中继器、比较器、红石火把、侦测器（放大行为对照原版 1.21.11
// 反编译结果实现）。
//
// 语义要点：
//   - 中继器/比较器的 facing 指向“输入侧”（原版 block state 定义：
//     从输出侧指向输入侧；输出在 facing 的反方向）。放置时 facing =
//     玩家视线方向的反方向；
//   - 中继器延迟 delay×2 tick、支持侧向锁定（仅二极管可锁定）；
//   - 比较器支持 compare/subtract 模式、读取容器填充度；
//   - 红石火把被强充能或直接供能时熄灭（2 tick 延迟），高频切换会熔断
//     （60 tick 内 8 次→熄灭 160 tick）；
//   - 侦测器观察 facing 方向的方块变化，输出 2 tick 脉冲到反方向。
//
// 已知近似（见 docs/TODO.md）：计划刻无优先级；比较器不读取物品展示框
// （gmcs 无展示框实体）。

const (
	// repeaterTicksPerDelay 是每级延迟对应的 tick 数（1 红石刻 = 2 tick）。
	repeaterTicksPerDelay = 2
	// comparatorDelayTicks 是比较器的固定延迟（2 tick）。
	comparatorDelayTicks = 2
	// observerPulseTicks 是侦测器脉冲长度（tick）。
	observerPulseTicks = 2
	// torchToggleWindow 是火把熔断统计窗口（tick）。
	torchToggleWindow = 60
	// torchMaxToggles 是窗口内允许的最大切换次数（达到即熔断）。
	torchMaxToggles = 8
	// torchBurnoutTicks 是熔断后的熄灭时长（tick）。
	torchBurnoutTicks = 160
)

// facingOffsets 是朝向属性到单位偏移的映射（north = −Z，与原版一致）。
var facingOffsets = map[string][3]int{
	"north": {0, 0, -1},
	"south": {0, 0, 1},
	"west":  {-1, 0, 0},
	"east":  {1, 0, 0},
	"up":    {0, 1, 0},
	"down":  {0, -1, 0},
}

// clockwiseDir / counterClockwiseDir 返回水平朝向的顺时针/逆时针方向
// （俯视视角：north→east→south→west）。
func clockwiseDir(dir string) string {
	switch dir {
	case "north":
		return "east"
	case "east":
		return "south"
	case "south":
		return "west"
	case "west":
		return "north"
	}
	return dir
}

func counterClockwiseDir(dir string) string {
	return clockwiseDir(clockwiseDir(clockwiseDir(dir)))
}

// yawToFacing 把玩家朝向（yaw）转换为水平朝向名（0=南、90=西、180=北、270=东）。
func yawToFacing(yaw float32) string {
	normalized := yaw
	for normalized < 0 {
		normalized += 360
	}
	for normalized >= 360 {
		normalized -= 360
	}
	switch {
	case normalized >= 315 || normalized < 45:
		return "south"
	case normalized < 135:
		return "west"
	case normalized < 225:
		return "north"
	default:
		return "east"
	}
}

// orientPlacementState 为方向类红石方块设置放置朝向：
//   - 中继器/比较器/活塞：facing = 玩家视线方向的反方向（原版放置规则；
//     活塞按俯仰角支持上下）；
//   - 侦测器：facing = 玩家视线方向。
func orientPlacementState(state uint16, yaw, pitch float32) uint16 {
	name, props, ok := registry.StateProps(state)
	if !ok {
		return state
	}
	next := cloneProps(props)
	switch name {
	case repeaterName, comparatorName:
		next["facing"] = oppositeDir(yawToFacing(yaw))
	case observerName:
		next["facing"] = yawToFacing(yaw)
	case pistonName, stickyPistonName:
		look := yawToFacing(yaw)
		switch {
		case pitch < -45:
			look = "up"
		case pitch > 45:
			look = "down"
		}
		next["facing"] = oppositeDir(look)
		next["extended"] = "false"
	case dispenserBlockName, dropperBlockName:
		// 发射器/投掷器：facing = 玩家最近视线方向的反方向（原版
		// DispenserBlock.getStateForPlacement），支持上下。
		look := yawToFacing(yaw)
		switch {
		case pitch < -45:
			look = "up"
		case pitch > 45:
			look = "down"
		}
		next["facing"] = oppositeDir(look)
		next["triggered"] = "false"
	case anvilBlockName, chippedAnvilBlockName, damagedAnvilBlockName:
		// 铁砧：facing = 玩家视线方向的顺时针（原版 AnvilBlock.getStateForPlacement）。
		next["facing"] = clockwiseDir(yawToFacing(yaw))
	default:
		return state
	}
	if oriented, ok := registry.BlockStateWithProps(name, next); ok {
		return oriented
	}
	return state
}

// ---------------------------------------------------------------------------
// 中继器
// ---------------------------------------------------------------------------

// repeaterLocked 报告中继器是否被侧向的二极管锁定（原版 isLocked：
// 左右两侧的二极管 direct signal > 0）。
func (s *Server) repeaterLocked(w *world.World, pos [3]int, facing string) bool {
	for _, dir := range []string{clockwiseDir(facing), counterClockwiseDir(facing)} {
		side := offsetDir(pos, dir)
		if s.controlInputSignal(w, side, dir, true) > 0 {
			return true
		}
	}
	return false
}

// checkDiodeUpdate 在方块更新后重算中继器/比较器：
// 中继器补充 locked 状态与延迟切换调度；比较器调度输出刷新。
func (s *Server) checkDiodeUpdate(w *world.World, dim world.Dimension, pos [3]int, state uint16) {
	name, props, ok := registry.StateProps(state)
	if !ok {
		return
	}
	if name == repeaterName {
		facing := props["facing"]
		locked := s.repeaterLocked(w, pos, facing)
		if locked != (props["locked"] == "true") {
			next := cloneProps(props)
			next["locked"] = strconv.FormatBool(locked)
			s.setRedstoneBlock(w, pos[0], pos[1], pos[2], repeaterName, next)
			return // setRedstoneBlock 已重入本函数（locked 更新后收敛）
		}
		if locked {
			return
		}
		want := s.diodeInputPower(w, pos, facing) > 0
		if want != (props["powered"] == "true") && !s.hasRedstoneTick(dim, pos) {
			delay := repeaterDelay(props)
			s.scheduleRedstoneTick(dim, pos, delay*repeaterTicksPerDelay)
		}
		return
	}
	// 比较器
	if s.comparatorShouldTurnOn(w, pos, state) != (props["powered"] == "true") && !s.hasRedstoneTick(dim, pos) {
		s.scheduleRedstoneTick(dim, pos, comparatorDelayTicks)
	}
}

// repeaterDelay 解析中继器的 delay 属性（1–4，缺省 1）。
func repeaterDelay(props map[string]string) int {
	delay, err := strconv.Atoi(props["delay"])
	if err != nil || delay < 1 || delay > 4 {
		return 1
	}
	return delay
}

// tickRepeater 处理中继器计划刻（原版 DiodeBlock.tick）。
func (s *Server) tickRepeater(w *world.World, dim world.Dimension, pos [3]int, state uint16) {
	name, props, ok := registry.StateProps(state)
	if !ok || name != repeaterName || props["locked"] == "true" {
		return
	}
	powered := props["powered"] == "true"
	want := s.diodeInputPower(w, pos, props["facing"]) > 0
	switch {
	case powered && !want:
		next := cloneProps(props)
		next["powered"] = "false"
		s.setRedstoneBlock(w, pos[0], pos[1], pos[2], repeaterName, next)
	case !powered:
		next := cloneProps(props)
		next["powered"] = "true"
		s.setRedstoneBlock(w, pos[0], pos[1], pos[2], repeaterName, next)
		if !want {
			// 原版：导通后再次断电会在延迟后结算（脉冲拉伸）。
			s.scheduleRedstoneTick(dim, pos, repeaterDelay(props)*repeaterTicksPerDelay)
		}
	}
}

// ---------------------------------------------------------------------------
// 比较器
// ---------------------------------------------------------------------------

// comparatorInputSignal 返回比较器的背面输入（原版 ComparatorBlock.getInputSignal）：
// 基础值来自 DiodeBlock.getInputSignal（facing 侧方块的发射值 + 线 power）；
// 若 facing 侧是容器则改为读取其填充度；若 facing 侧是导体方块，则再读取
// 其后方一格的容器/展示框信号。
func (s *Server) comparatorInputSignal(w *world.World, pos [3]int, state uint16) int {
	_, props, ok := registry.StateProps(state)
	if !ok {
		return 0
	}
	facing := props["facing"]
	signal := s.diodeInputPower(w, pos, facing)
	back := offsetDir(pos, facing)
	backState := s.blockIfLoaded(w, back)
	if s.hasAnalogOutputSignal(backState) {
		signal = s.containerAnalogSignal(w, back)
		return signal
	}
	if signal < 15 && s.conductorState(backState) {
		further := offsetDir(back, facing)
		furtherState := s.blockIfLoaded(w, further)
		// gmcs 无物品展示框实体：仅检查更后方的容器（原版把展示框信号与
		// 容器信号取最大值后覆盖输入）。
		if s.hasAnalogOutputSignal(furtherState) {
			signal = s.containerAnalogSignal(w, further)
		}
	}
	return signal
}

// comparatorSideSignal 返回比较器左右两侧的控制输入最大值
// （原版 getAlternateSignal，非仅二极管）。
func (s *Server) comparatorSideSignal(w *world.World, pos [3]int, facing string) int {
	best := 0
	for _, dir := range []string{clockwiseDir(facing), counterClockwiseDir(facing)} {
		side := offsetDir(pos, dir)
		if signal := s.controlInputSignal(w, side, dir, false); signal > best {
			best = signal
		}
	}
	return best
}

// comparatorOutputSignal 返回比较器计算出的输出强度（原版 calculateOutputSignal）。
func (s *Server) comparatorOutputSignal(w *world.World, pos [3]int, state uint16) int {
	_, props, ok := registry.StateProps(state)
	if !ok {
		return 0
	}
	input := s.comparatorInputSignal(w, pos, state)
	if input == 0 {
		return 0
	}
	side := s.comparatorSideSignal(w, pos, props["facing"])
	if side > input {
		return 0
	}
	if props["mode"] == "subtract" {
		return input - side
	}
	return input
}

// comparatorShouldTurnOn 返回比较器是否应处于 powered 状态
// （原版 shouldTurnOn：输入大于侧输入，或 compare 模式下相等）。
func (s *Server) comparatorShouldTurnOn(w *world.World, pos [3]int, state uint16) bool {
	_, props, ok := registry.StateProps(state)
	if !ok {
		return false
	}
	input := s.comparatorInputSignal(w, pos, state)
	if input == 0 {
		return false
	}
	side := s.comparatorSideSignal(w, pos, props["facing"])
	if input > side {
		return true
	}
	return input == side && props["mode"] == "compare"
}

// tickComparator 处理比较器计划刻（原版 DiodeBlock.tick）。
func (s *Server) tickComparator(w *world.World, dim world.Dimension, pos [3]int, state uint16) {
	name, props, ok := registry.StateProps(state)
	if !ok || name != comparatorName {
		return
	}
	powered := props["powered"] == "true"
	want := s.comparatorShouldTurnOn(w, pos, state)
	switch {
	case powered && !want:
		next := cloneProps(props)
		next["powered"] = "false"
		s.setRedstoneBlock(w, pos[0], pos[1], pos[2], comparatorName, next)
	case !powered:
		next := cloneProps(props)
		next["powered"] = "true"
		s.setRedstoneBlock(w, pos[0], pos[1], pos[2], comparatorName, next)
		if !want {
			s.scheduleRedstoneTick(dim, pos, comparatorDelayTicks)
		}
	}
}

// refreshComparatorOutput 立即刷新比较器的 powered 状态
// （原版 ComparatorBlock.refreshOutputState，用于右键切换模式后）。
func (s *Server) refreshComparatorOutput(w *world.World, pos [3]int) {
	state := s.blockIfLoaded(w, pos)
	name, props, ok := registry.StateProps(state)
	if !ok || name != comparatorName {
		return
	}
	want := s.comparatorShouldTurnOn(w, pos, state)
	if want != (props["powered"] == "true") {
		next := cloneProps(props)
		next["powered"] = strconv.FormatBool(want)
		s.setRedstoneBlock(w, pos[0], pos[1], pos[2], comparatorName, next)
	}
}

// hasAnalogOutputSignal 报告方块状态是否为比较器可读的容器
// （对应原版 hasAnalogOutputSignal；末影箱等无模拟输出）。
func (s *Server) hasAnalogOutputSignal(state uint16) bool {
	name, ok := s.blockNameOf(state)
	if !ok {
		return false
	}
	_, ok = s.containerDefs[name]
	return ok
}

// containerAnalogSignal 返回容器的比较器输出强度（原版
// getRedstoneSignalFromContainer：fill = Σ(数量/堆叠上限)/槽位总数，
// 信号 = floor(fill×14) + (非空 ? 1 : 0)）。
func (s *Server) containerAnalogSignal(w *world.World, pos [3]int) int {
	name, ok := s.blockNameOf(s.blockIfLoaded(w, pos))
	if !ok {
		return 0
	}
	def := s.containerDefs[name]
	if def == nil {
		return 0
	}
	slots := make([]item.Stack, def.Slots)

	key := containerKey{dim: w.Dimension(), x: pos[0], y: pos[1], z: pos[2]}
	s.containerMu.Lock()
	opened := s.containers[key]
	s.containerMu.Unlock()
	switch {
	case opened != nil && opened.furnace != nil:
		opened.furnace.mu.Lock()
		for i := 0; i < def.Slots && i < len(opened.furnace.slots); i++ {
			slots[i] = opened.furnace.slots[i]
		}
		opened.furnace.mu.Unlock()
	case opened != nil && opened.hopper != nil:
		// 漏斗槽位由 hopperState 权威持有（与传输 Tick 共用），在其锁内读取。
		opened.hopper.mu.Lock()
		for i := 0; i < def.Slots && i < len(opened.hopper.slots); i++ {
			slots[i] = opened.hopper.slots[i]
		}
		opened.hopper.mu.Unlock()
	case opened != nil:
		opened.mu.Lock()
		for i := 0; i < def.Slots && i < len(opened.slots); i++ {
			slots[i] = opened.slots[i]
		}
		opened.mu.Unlock()
	default:
		entity, err := w.BlockEntityAt(pos[0], pos[1], pos[2])
		if err != nil {
			return 0
		}
		for i := 0; i < def.Slots && i < len(entity.Items); i++ {
			slots[i] = stackFromContainer(entity.Items[i])
		}
	}

	fill := 0.0
	nonEmpty := 0
	for _, stack := range slots {
		if stack.IsEmpty() || stack.Count <= 0 {
			continue
		}
		maxStack := stack.MaxStack()
		if maxStack <= 0 {
			maxStack = 64
		}
		fill += float64(stack.Count) / float64(maxStack)
		nonEmpty++
	}
	fill /= float64(def.Slots)
	signal := int(fill * 14)
	if nonEmpty > 0 {
		signal++
	}
	if signal > 15 {
		signal = 15
	}
	return signal
}

// ---------------------------------------------------------------------------
// 红石火把
// ---------------------------------------------------------------------------

// torchInputSignal 报告火把的附着方块是否向其供电
// （立式：下方方块；墙式：附着方块）。
func (s *Server) torchInputSignal(w *world.World, pos [3]int, state uint16) bool {
	name, props, ok := registry.StateProps(state)
	if !ok {
		return false
	}
	if name == redstoneTorchName {
		below := [3]int{pos[0], pos[1] - 1, pos[2]}
		return s.emittedSignal(w, below, "down") > 0
	}
	// 墙式：facing 从支撑方块指向火把，检查支撑方块向火把方向的发射值。
	supportDir := oppositeDir(props["facing"])
	support := offsetDir(pos, supportDir)
	return s.emittedSignal(w, support, supportDir) > 0
}

// checkTorchUpdate 在方块更新后重算火把：当前亮灭与应然状态不一致时安排
// 2 tick 计划刻（原版 neighborChanged）。
func (s *Server) checkTorchUpdate(w *world.World, dim world.Dimension, pos [3]int, state uint16) {
	_, props, ok := registry.StateProps(state)
	if !ok {
		return
	}
	lit := props["lit"] == "true"
	signal := s.torchInputSignal(w, pos, state)
	if lit == signal && !s.hasRedstoneTick(dim, pos) {
		s.scheduleRedstoneTick(dim, pos, 2)
	}
}

// tickTorch 处理火把计划刻（原版 RedstoneTorchBlock.tick，含熔断）。
func (s *Server) tickTorch(w *world.World, dim world.Dimension, pos [3]int, state uint16) {
	name, props, ok := registry.StateProps(state)
	if !ok || (name != redstoneTorchName && name != redstoneWallTorchName) {
		return
	}
	lit := props["lit"] == "true"
	signal := s.torchInputSignal(w, pos, state)
	if lit {
		if signal {
			next := cloneProps(props)
			next["lit"] = "false"
			s.setRedstoneBlock(w, pos[0], pos[1], pos[2], name, next)
			if s.torchToggledTooFrequently(dim, pos, true) {
				// 熔断：熄灭 160 tick 后再尝试恢复。
				s.scheduleRedstoneTick(dim, pos, torchBurnoutTicks)
			}
		}
		return
	}
	if !signal && !s.torchToggledTooFrequently(dim, pos, false) {
		next := cloneProps(props)
		next["lit"] = "true"
		s.setRedstoneBlock(w, pos[0], pos[1], pos[2], name, next)
	}
}

// torchToggledTooFrequently 统计火把在窗口内的切换次数；add=true 时把本次
// 切换计入（原版 isToggledTooFrequently）。
func (s *Server) torchToggledTooFrequently(dim world.Dimension, pos [3]int, add bool) bool {
	s.redstoneMu.Lock()
	defer s.redstoneMu.Unlock()
	now := s.redstoneTickCount
	if s.torchToggles == nil {
		s.torchToggles = make(map[containerKey][]int64)
	}
	key := containerKey{dim: dim, x: pos[0], y: pos[1], z: pos[2]}
	list := s.torchToggles[key]
	pruned := list[:0]
	for _, at := range list {
		if now-at <= torchToggleWindow {
			pruned = append(pruned, at)
		}
	}
	if add {
		pruned = append(pruned, now)
	}
	if len(pruned) == 0 {
		delete(s.torchToggles, key)
	} else {
		s.torchToggles[key] = pruned
	}
	return len(pruned) >= torchMaxToggles
}

// ---------------------------------------------------------------------------
// 侦测器
// ---------------------------------------------------------------------------

// notifyObservers 在方块更新广播前调用：观察该方块的侦测器安排 2 tick 脉冲。
func (s *Server) notifyObservers(dim world.Dimension, x, y, z int) {
	w := s.worldFor(dim)
	if w == nil {
		return
	}
	changed := [3]int{x, y, z}
	for _, offset := range neighborOffsets6 {
		observer := [3]int{x + offset[0], y + offset[1], z + offset[2]}
		if _, ok := world.SectionIndex(observer[1]); !ok {
			continue
		}
		state := w.BlockAt(observer[0], observer[1], observer[2])
		name, props, ok := registry.StateProps(state)
		if !ok || name != observerName {
			continue
		}
		facing := props["facing"]
		if offsetDir(observer, facing) != changed {
			continue // 不是本侦测器观察的方向
		}
		if props["powered"] == "true" || s.hasRedstoneTick(dim, observer) {
			continue // 脉冲期间不重新触发（原版 updateShape 的 powered 检查）
		}
		s.scheduleRedstoneTick(dim, observer, observerPulseTicks)
	}
}

// tickObserver 处理侦测器计划刻（原版 ObserverBlock.tick）：
// 未通电→通电并再排一次（构成 2 tick 脉冲）；已通电→断电。
func (s *Server) tickObserver(w *world.World, dim world.Dimension, pos [3]int, state uint16) {
	name, props, ok := registry.StateProps(state)
	if !ok || name != observerName {
		return
	}
	if props["powered"] == "true" {
		next := cloneProps(props)
		next["powered"] = "false"
		s.setRedstoneBlock(w, pos[0], pos[1], pos[2], observerName, next)
		return
	}
	next := cloneProps(props)
	next["powered"] = "true"
	s.setRedstoneBlock(w, pos[0], pos[1], pos[2], observerName, next)
	s.scheduleRedstoneTick(dim, pos, observerPulseTicks)
}
