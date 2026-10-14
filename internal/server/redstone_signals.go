//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"strconv"

	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 红石信号核心模型（对照原版 1.21.11 的 SignalGetter / 各组件字节码实现）。
//
// 方向语义：本文件的 direction 参数沿用原版 getSignal(pos, direction) /
// getDirectSignal(pos, direction) 的约定——direction 从“接收者”指向
// “发射者”，即接收者位于 emitter.relative(oppositeDir(direction))。
// 组件的发射规则（对照反编译结果）：
//
//   - 红石线：power>0 时向水平已连接方向与下方发射自身强度；不向上发射；
//   - 拉杆/按钮：powered 时向所有方向发射 15，且向“支撑方块方向”
//     （连接方向）提供 15 的 direct signal（弱充能）；
//   - 红石火把（立式）：lit 时除向上外全向 15；direct signal 向下（向
//     附着方块上方的方块强充能）。墙式：lit 时除附着方向外全向 15；
//     direct signal 向下（与原版 RedstoneWallTorchBlock 未覆盖父类行为一致）；
//   - 红石块：恒向所有方向发射 15（无 direct signal）；
//   - 中继器：powered 时向输出侧（facing 反方向）发射 15；
//   - 比较器：powered 时向输出侧发射计算出的 0–15 强度；
//   - 侦测器：powered 时向输出侧（facing 反方向）发射 15；
//   - 导强充能的方块（isRedstoneConductor）：查询其信号时会额外取
//     “六个相邻方块的 direct signal 最大值”（强充能），例如中继器指向
//     方块后，该方块可以为相邻红石线提供 15。
//
// 发射值的判定顺序与原版一致：先取方块自身发射值，再（仅导体方块）
// 叠加 direct signal 重定向。

// oppositeDir 返回某个方向的相反方向。
func oppositeDir(dir string) string {
	switch dir {
	case "north":
		return "south"
	case "south":
		return "north"
	case "west":
		return "east"
	case "east":
		return "west"
	case "up":
		return "down"
	case "down":
		return "up"
	}
	return dir
}

// dirBetween 返回从 from 指向 to 的单位方向名；两点必须六向相邻。
func dirBetween(from, to [3]int) (string, bool) {
	dx := to[0] - from[0]
	dy := to[1] - from[1]
	dz := to[2] - from[2]
	switch {
	case dx == 1 && dy == 0 && dz == 0:
		return "east", true
	case dx == -1 && dy == 0 && dz == 0:
		return "west", true
	case dx == 0 && dy == 1 && dz == 0:
		return "up", true
	case dx == 0 && dy == -1 && dz == 0:
		return "down", true
	case dx == 0 && dy == 0 && dz == 1:
		return "south", true
	case dx == 0 && dy == 0 && dz == -1:
		return "north", true
	}
	return "", false
}

// offsetDir 返回沿方向 dir 的单位偏移。
func offsetDir(pos [3]int, dir string) [3]int {
	offset, ok := facingOffsets[dir]
	if !ok {
		return pos
	}
	return [3]int{pos[0] + offset[0], pos[1] + offset[1], pos[2] + offset[2]}
}

// fullCubeState 报告方块状态的碰撞形状是否为完整方块（原版
// isCollisionShapeFullBlock 的对应判定，用于 isRedstoneConductor 默认值）。
func fullCubeState(state uint16) bool {
	boxes := registry.BlockStateShape(state)
	return len(boxes) == 1 && boxes[0] == registry.ShapeBox{X1: 0, Y1: 0, Z1: 0, X2: 16, Y2: 16, Z2: 16}
}

// conductorState 报告方块状态是否导强充能（原版 isRedstoneConductor）。
func (s *Server) conductorState(state uint16) bool {
	name, ok := s.blockNameOf(state)
	if !ok {
		return false
	}
	return registry.IsRedstoneConductor(prefixlessName(name), fullCubeState(state))
}

// prefixlessName 去掉命名空间的 "minecraft:" 前缀（寄存器行为表以短名索引）。
func prefixlessName(name string) string {
	const prefix = "minecraft:"
	if len(name) > len(prefix) && name[:len(prefix)] == prefix {
		return name[len(prefix):]
	}
	return name
}

// stateIsNamed 报告方块状态是否属于指定命名空间的方块名。
func (s *Server) stateIsNamed(state uint16, name string) bool {
	current, ok := s.blockNameOf(state)
	return ok && current == name
}

// isSignalSourceName 报告方块类型是否是原版意义上的“信号源”（用于比较器
// 侧面输入 getControlInputSignal 的判断）。
func isSignalSourceName(name string) bool {
	switch name {
	case leverName, redstoneBlockName, redstoneTorchName, redstoneWallTorchName,
		repeaterName, comparatorName, observerName:
		return true
	}
	if buttonNames[name] {
		return true
	}
	_, isPlate := plateInfos[name]
	return isPlate
}

// buttonNames 是全部按钮方块名（用于信号源判定与按下状态处理）。
var buttonNames = map[string]bool{
	"minecraft:acacia_button":              true,
	"minecraft:bamboo_button":              true,
	"minecraft:birch_button":               true,
	"minecraft:cherry_button":              true,
	"minecraft:crimson_button":             true,
	"minecraft:dark_oak_button":            true,
	"minecraft:jungle_button":              true,
	"minecraft:mangrove_button":            true,
	"minecraft:oak_button":                 true,
	"minecraft:pale_oak_button":            true,
	"minecraft:polished_blackstone_button": true,
	"minecraft:spruce_button":              true,
	"minecraft:stone_button":               true,
	"minecraft:warped_button":              true,
}

// isButtonName 报告方块名是否是按钮。
func isButtonName(name string) bool {
	return buttonNames[name]
}

// isButtonState 报告方块状态是否为按钮（全部木质/石质变体）。
func isButtonState(state uint16) bool {
	name, props, ok := registry.StateProps(state)
	if !ok || !buttonNames[name] {
		return false
	}
	_ = props
	return true
}

// connectedDirection 返回拉杆/按钮从“支撑方块”指向“组件自身”的方向
// （原版 getConnectedDirection：floor=支撑在下方→up，ceiling→down，wall→facing）。
func connectedDirection(props map[string]string) string {
	switch props["face"] {
	case "floor":
		return "up"
	case "ceiling":
		return "down"
	default:
		if facing, ok := props["facing"]; ok {
			return facing
		}
		return "north"
	}
}

// wireConnectionConnected 报告红石线某方向属性是否处于连接状态。
func wireConnectionConnected(props map[string]string, dir string) bool {
	value, ok := props[dir]
	return ok && value != "none"
}

// blockOwnSignal 返回方块自身沿 direction 发射的信号强度
// （原版 getSignal 中“方块行为”的部分，不含导体强充能重定向）。
func (s *Server) blockOwnSignal(w *world.World, pos [3]int, state uint16, dir string) int {
	name, props, ok := registry.StateProps(state)
	if !ok {
		return 0
	}
	switch {
	case name == wireName:
		power := wirePowerOf(state)
		if power == 0 || dir == "down" {
			return 0
		}
		if dir == "up" {
			return power
		}
		// 水平方向：仅当线在“接收者方向”连接时供电（原版
		// getConnectionState(...).getValue(direction.getOpposite()).isConnected()）。
		if wireConnectionConnected(props, oppositeDir(dir)) {
			return power
		}
		return 0
	case name == leverName || buttonNames[name]:
		if props["powered"] == "true" {
			return 15
		}
		return 0
	case name == redstoneBlockName:
		return 15
	case name == redstoneTorchName:
		// 立式火把：lit 时除向上外全向 15（不向下方方块供电）。
		if props["lit"] == "true" && dir != "up" {
			return 15
		}
		return 0
	case name == redstoneWallTorchName:
		// 墙式火把：lit 时除附着方向（facing）外全向 15。
		if props["lit"] == "true" && dir != props["facing"] {
			return 15
		}
		return 0
	case name == repeaterName:
		if props["powered"] == "true" && dir == props["facing"] {
			return 15
		}
		return 0
	case name == comparatorName:
		if props["powered"] == "true" && dir == props["facing"] {
			return s.comparatorOutputSignal(w, pos, state)
		}
		return 0
	case name == observerName:
		if props["powered"] == "true" && dir == props["facing"] {
			return 15
		}
		return 0
	}
	// 压力板：向所有方向发射自身信号（原版 BasePressurePlateBlock.getSignal：
	// 轻型 powered ? 15 : 0；加权 power 属性值）。
	if info, ok := plateInfos[name]; ok {
		return plateSignalOfProps(info, props)
	}
	return 0
}

// blockDirectSignal 返回方块沿 direction 提供的 direct signal
// （原版 getDirectSignal；用于导体方块的强充能重定向）。
func (s *Server) blockDirectSignal(w *world.World, pos [3]int, state uint16, dir string) int {
	name, props, ok := registry.StateProps(state)
	if !ok {
		return 0
	}
	switch {
	case name == redstoneTorchName || name == redstoneWallTorchName:
		// 两种火把的默认行为：direct signal 仅指向下方（点亮时）。
		if dir == "down" {
			return s.blockOwnSignal(w, pos, state, dir)
		}
		return 0
	case name == leverName || buttonNames[name]:
		if props["powered"] == "true" && dir == connectedDirection(props) {
			return 15
		}
		return 0
	case name == repeaterName || name == comparatorName || name == observerName:
		// 原版二极管/侦测器的 getDirectSignal 委托 getSignal。
		return s.blockOwnSignal(w, pos, state, dir)
	}
	// 压力板：仅向上提供 direct signal（弱充能下方方块）。
	if info, ok := plateInfos[name]; ok && dir == "up" {
		return plateSignalOfProps(info, props)
	}
	return 0
}

// directSignalTo 返回“位置 pos 作为导体方块被强充能”的强度：取六个相邻方块
// direct signal 的最大值（原版 SignalGetter.getDirectSignalTo）。
func (s *Server) directSignalTo(w *world.World, pos [3]int) int {
	best := 0
	for _, offset := range neighborOffsets6 {
		neighbor := [3]int{pos[0] + offset[0], pos[1] + offset[1], pos[2] + offset[2]}
		if _, ok := world.SectionIndex(neighbor[1]); !ok {
			continue
		}
		dir, ok := dirBetween(pos, neighbor)
		if !ok {
			continue
		}
		state := w.BlockAt(neighbor[0], neighbor[1], neighbor[2])
		if signal := s.blockDirectSignal(w, neighbor, state, dir); signal > best {
			best = signal
		}
	}
	return best
}

// emittedSignal 对应原版 SignalGetter.getSignal(pos, direction)：
// 方块自身发射值与（仅导体方块的）强充能重定向取最大值。
func (s *Server) emittedSignal(w *world.World, pos [3]int, dir string) int {
	if _, ok := world.SectionIndex(pos[1]); !ok {
		return 0
	}
	state := w.BlockAt(pos[0], pos[1], pos[2])
	own := s.blockOwnSignal(w, pos, state, dir)
	if s.conductorState(state) {
		if redirect := s.directSignalTo(w, pos); redirect > own {
			return redirect
		}
	}
	return own
}

// bestNeighborSignal 对应原版 Level.getBestNeighborSignal：
// 六个相邻方块向本位置发射的信号最大值。
func (s *Server) bestNeighborSignal(w *world.World, pos [3]int) int {
	best := 0
	for _, offset := range neighborOffsets6 {
		neighbor := [3]int{pos[0] + offset[0], pos[1] + offset[1], pos[2] + offset[2]}
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

// wirePowerAt 返回红石线当前位置的 power 属性（非红石线返回 0）。
func (s *Server) wirePowerAt(w *world.World, pos [3]int) int {
	if _, ok := world.SectionIndex(pos[1]); !ok {
		return 0
	}
	return wirePowerOf(w.BlockAt(pos[0], pos[1], pos[2]))
}

// diodeInputPower 返回中继器/比较器的“背面输入”强度（原版 DiodeBlock.getInputSignal）：
// 输入方块位于 facing 方向，信号查询方向为 facing；红石线输入额外直接取其 power。
func (s *Server) diodeInputPower(w *world.World, pos [3]int, facing string) int {
	inputPos := offsetDir(pos, facing)
	signal := s.emittedSignal(w, inputPos, facing)
	if state := s.blockIfLoaded(w, inputPos); isWireState(state) {
		if power := wirePowerOf(state); power > signal {
			signal = power
		}
	}
	return signal
}

// blockIfLoaded 读取方块状态；坐标所在 section 未加载时返回空气。
func (s *Server) blockIfLoaded(w *world.World, pos [3]int) uint16 {
	if _, ok := world.SectionIndex(pos[1]); !ok {
		return world.AirBlock
	}
	return w.BlockAt(pos[0], pos[1], pos[2])
}

// controlInputSignal 对应原版 SignalGetter.getControlInputSignal：
// 比较器侧面输入（diodesOnly=true 时仅二极管）使用的信号。
func (s *Server) controlInputSignal(w *world.World, pos [3]int, dir string, diodesOnly bool) int {
	state := s.blockIfLoaded(w, pos)
	name, props, ok := registry.StateProps(state)
	if !ok {
		name, _ = s.blockNameOf(state)
	}
	if diodesOnly {
		if isDiodeState(state) {
			return s.blockDirectSignal(w, pos, state, dir)
		}
		return 0
	}
	if name == redstoneBlockName {
		return 15
	}
	if name == wireName {
		power, err := parseIntProp(props, "power")
		if err != nil {
			return 0
		}
		return power
	}
	if isSignalSourceName(name) {
		return s.blockDirectSignal(w, pos, state, dir)
	}
	return 0
}

// isDiodeState 报告方块状态是否为中继器或比较器。
func isDiodeState(state uint16) bool {
	name, _, ok := registry.StateProps(state)
	return ok && (name == repeaterName || name == comparatorName)
}

// parseIntProp 解析属性表中的整数字符串。
func parseIntProp(props map[string]string, key string) (int, error) {
	value, ok := props[key]
	if !ok {
		return 0, strconv.ErrSyntax
	}
	return strconv.Atoi(value)
}
