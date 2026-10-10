package server

import (
	"math"
	"strconv"

	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 红石扩展：中继器（延迟 1–4 红石刻、右键调整、单向传输）与侦测器（观察前方
// 方块变化并输出脉冲）。
//
// 近似说明（未与原版逐项对比，见 docs/TODO.md）：
//   - 中继器不实现锁定（locked）行为：侧向输入不会锁定中继器；
//   - 中继器/侦测器放置时的朝向按玩家视线方向给出（原版细节未逐项核对）；
//   - 侦测器在通电期间再次观察到变化会延长脉冲（原版为重新触发）；
//   - 中继器不实现“输入脉冲短于延迟时丢失”等高级时序细节（按电平延迟）。

const (
	repeaterName = "minecraft:repeater"
	observerName = "minecraft:observer"

	// observerPulseTicks 是侦测器脉冲长度（tick）。
	observerPulseTicks = 2
	// repeaterTicksPerDelay 是每级延迟对应的 tick 数（1 红石刻 = 2 tick，与原版一致）。
	repeaterTicksPerDelay = 2
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

// isRepeaterState 报告方块状态是否为红石中继器。
func isRepeaterState(state uint16) bool {
	name, _, ok := registry.StateProps(state)
	return ok && name == repeaterName
}

// isObserverState 报告方块状态是否为侦测器。
func isObserverState(state uint16) bool {
	name, _, ok := registry.StateProps(state)
	return ok && name == observerName
}

// repeaterPending 是一次待结算的中继器状态切换。
type repeaterPending struct {
	ticks   int
	powered bool
}

// yawToFacing 把玩家朝向（yaw）转换为水平朝向名（0=南、90=西、180=北、270=东）。
func yawToFacing(yaw float32) string {
	normalized := math.Mod(float64(yaw), 360)
	if normalized < 0 {
		normalized += 360
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

// orientPlacementState 为中继器/侦测器设置放置朝向（按玩家视线方向；
// 原版放置朝向细节未逐项核对，见文件头说明）。
func orientPlacementState(state uint16, yaw float32) uint16 {
	name, props, ok := registry.StateProps(state)
	if !ok || (name != repeaterName && name != observerName) {
		return state
	}
	next := cloneProps(props)
	next["facing"] = yawToFacing(yaw)
	if oriented, ok := registry.BlockStateWithProps(name, next); ok {
		return oriented
	}
	return state
}

// repeaterInputPower 返回中继器输入侧（背面）的供电强度：
// 电源方块 15、红石线按当前 power、已通电的中继器/侦测器 15。
func (s *Server) repeaterInputPower(w *world.World, pos [3]int, facing string) int {
	offset, ok := facingOffsets[facing]
	if !ok {
		return 0
	}
	back := [3]int{pos[0] - offset[0], pos[1] - offset[1], pos[2] - offset[2]}
	state := w.BlockAt(back[0], back[1], back[2])
	if power := redstoneSourcePower(state); power > 0 {
		return power
	}
	if isWireState(state) {
		return wirePowerOf(state)
	}
	return 0
}

// evaluateRepeater 重新评估中继器的输入并在需要时安排延迟切换。
// 已有同目标计时不会被重置（与“输入保持时只切换一次”的行为一致）。
func (s *Server) evaluateRepeater(w *world.World, pos [3]int, state uint16) {
	_, props, ok := registry.StateProps(state)
	if !ok {
		return
	}
	delay, err := strconv.Atoi(props["delay"])
	if err != nil || delay < 1 || delay > 4 {
		delay = 1
	}
	wantPowered := s.repeaterInputPower(w, pos, props["facing"]) > 0
	if wantPowered == (props["powered"] == "true") {
		s.clearRepeater(w.Dimension(), pos)
		return
	}
	s.redstoneMu.Lock()
	if s.repeaters == nil {
		s.repeaters = make(map[containerKey]repeaterPending)
	}
	key := containerKey{dim: w.Dimension(), x: pos[0], y: pos[1], z: pos[2]}
	if pending, exists := s.repeaters[key]; !exists || pending.powered != wantPowered {
		s.repeaters[key] = repeaterPending{ticks: delay * repeaterTicksPerDelay, powered: wantPowered}
	}
	s.redstoneMu.Unlock()
}

// clearRepeater 清除指定位置的中继器挂起计时（状态已达到目标时调用）。
func (s *Server) clearRepeater(dim world.Dimension, pos [3]int) {
	s.redstoneMu.Lock()
	delete(s.repeaters, containerKey{dim: dim, x: pos[0], y: pos[1], z: pos[2]})
	s.redstoneMu.Unlock()
}

// scheduleObserver 设置/刷新侦测器的断电计时。
func (s *Server) scheduleObserver(dim world.Dimension, pos [3]int, ticks int) {
	s.redstoneMu.Lock()
	if s.observers == nil {
		s.observers = make(map[containerKey]int)
	}
	s.observers[containerKey{dim: dim, x: pos[0], y: pos[1], z: pos[2]}] = ticks
	s.redstoneMu.Unlock()
}

// notifyObservers 在方块更新广播前调用：让观察该方块的侦测器输出脉冲。
// 观察者朝向 = 被观察方块相对观察者的方向（facing 指向被观察的一侧）。
func (s *Server) notifyObservers(dim world.Dimension, x, y, z int) {
	w := s.worldFor(dim)
	for _, offset := range neighborOffsets6 {
		ox, oy, oz := x+offset[0], y+offset[1], z+offset[2]
		if _, ok := world.SectionIndex(oy); !ok {
			continue
		}
		name, props, ok := registry.StateProps(w.BlockAt(ox, oy, oz))
		if !ok || name != observerName {
			continue
		}
		expected := [3]int{-offset[0], -offset[1], -offset[2]}
		if facingOffsets[props["facing"]] != expected {
			continue
		}
		if props["powered"] == "true" {
			// 已通电：延长脉冲（近似原版重新触发），不重复置位。
			s.scheduleObserver(dim, [3]int{ox, oy, oz}, observerPulseTicks)
			continue
		}
		next := cloneProps(props)
		next["powered"] = "true"
		if !s.setRedstoneBlock(w, ox, oy, oz, observerName, next) {
			continue
		}
		s.scheduleObserver(dim, [3]int{ox, oy, oz}, observerPulseTicks)
	}
}

// tickRedstoneExtra 推进中继器与侦测器计时（由 tickRedstone 调用）。
func (s *Server) tickRedstoneExtra() {
	s.redstoneMu.Lock()
	type repeaterFire struct {
		key     containerKey
		powered bool
	}
	var fires []repeaterFire
	for key, pending := range s.repeaters {
		if pending.ticks <= 1 {
			fires = append(fires, repeaterFire{key: key, powered: pending.powered})
			delete(s.repeaters, key)
			continue
		}
		pending.ticks--
		s.repeaters[key] = pending
	}
	var observerOff []containerKey
	for key, ticks := range s.observers {
		if ticks <= 1 {
			observerOff = append(observerOff, key)
			delete(s.observers, key)
			continue
		}
		s.observers[key] = ticks - 1
	}
	s.redstoneMu.Unlock()

	for _, fire := range fires {
		w := s.worldFor(fire.key.dim)
		name, props, ok := registry.StateProps(w.BlockAt(fire.key.x, fire.key.y, fire.key.z))
		if !ok || name != repeaterName {
			continue // 中继器已被移除或替换
		}
		if (props["powered"] == "true") == fire.powered {
			continue
		}
		next := cloneProps(props)
		next["powered"] = strconv.FormatBool(fire.powered)
		s.setRedstoneBlock(w, fire.key.x, fire.key.y, fire.key.z, repeaterName, next)
	}
	for _, key := range observerOff {
		w := s.worldFor(key.dim)
		name, props, ok := registry.StateProps(w.BlockAt(key.x, key.y, key.z))
		if !ok || name != observerName || props["powered"] != "true" {
			continue
		}
		next := cloneProps(props)
		next["powered"] = "false"
		s.setRedstoneBlock(w, key.x, key.y, key.z, observerName, next)
	}
}
