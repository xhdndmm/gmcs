//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"
	"strconv"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 压力板红石：轻型压力板（木质/石质）与加权压力板（金/铁）。
//
// 实现与 1.21.11 原版对齐（对照官方 mappings 与反编译的
// BasePressurePlateBlock / PressurePlateBlock / WeightedPressurePlateBlock）：
//
//   - 检测盒：方块坐标 (1/16..15/16, 0..4/16, 1/16..15/16)，
//     与原版 TOUCH_AABB 一致（Block.box(14, 0, 4) 的 1/16 单位版本）；
//   - 实体过滤：木质压力板检测全部实体（Entity.class），
//     石质/抛光黑石仅检测生物（LivingEntity.class），
//     加权（金/铁）压力板检测全部实体；均排除旁观者；
//   - 信号：
//     轻型：任意实体在检测盒内 → powered=true（信号 15），否则 0；
//     加权：ceil(min(数量, 上限) × 15 / 上限) 写入 power 属性（0–15），
//     金上限 15、铁上限 150；
//   - 松开延迟：轻型 20 tick、加权 10 tick（原版 getPressedTime）；
//   - 信号变化才更新方块（并向所有方向发射：轻型 15/0，加权 power 值；
//     direct signal 仅向上，即弱充能下方方块）；
//   - 实体进入（entityInside）时若未按下则立即重算；计划刻到期时重算，
//     仍有实体则续期（与原版 updateSignalStrength 一致）；
//   - 按下/弹起播放对应的压力板点击音效（木质/石质/金属三组）。
//
// 已知近似：不做原版“玩家进入检测盒时每 tick 的实体扫描节流”（原版在
// entityInside 中仅当 signal==0 时才扫描，本实现同）；其余行为一致。

// plateKind 是压力板类型。
type plateKind uint8

const (
	// plateLight 是轻型压力板（木质/石质）：二元信号，松开延迟 20 tick。
	plateLight plateKind = iota
	// plateWeighted 是加权压力板（金/铁）：按数量输出 0–15，松开延迟 10 tick。
	plateWeighted
)

// 压力板释放延迟（原版 getPressedTime：轻型 20、加权 10）。
const (
	plateLightReleaseTicks    = 20
	plateWeightedReleaseTicks = 10
)

// 加权压力板的最大重量（原版构造参数：金 15、铁 150）。
const (
	plateGoldMaxWeight = 15
	plateIronMaxWeight = 150
)

// 压力板音效分组（对应 BlockSetType 的 pressurePlateClickOn/Off 三组音效）。
const (
	plateSoundWood = iota
	plateSoundStone
	plateSoundMetal
	plateSoundGroups
)

// plateInfo 描述一种压力板的静态行为（注册表数据，只读）。
type plateInfo struct {
	kind plateKind
	// maxWeight 是加权压力板的最大重量（轻型为 0）。
	maxWeight int
	// livingOnly 为 true 时仅检测生物（石质/抛光黑石）。
	livingOnly bool
	// soundGroup 是点击音效分组（plateSoundWood/Stone/Metal）。
	soundGroup int
}

// plateInfos 是全部压力板的静态行为表（键为方块名）。
var plateInfos = map[string]plateInfo{
	// 木质轻型（BlockSetType 为各木材：检测全部实体，木质音效）。
	"minecraft:acacia_pressure_plate":              {kind: plateLight, soundGroup: plateSoundWood},
	"minecraft:bamboo_pressure_plate":              {kind: plateLight, soundGroup: plateSoundWood},
	"minecraft:birch_pressure_plate":               {kind: plateLight, soundGroup: plateSoundWood},
	"minecraft:cherry_pressure_plate":              {kind: plateLight, soundGroup: plateSoundWood},
	"minecraft:crimson_pressure_plate":             {kind: plateLight, soundGroup: plateSoundWood},
	"minecraft:dark_oak_pressure_plate":            {kind: plateLight, soundGroup: plateSoundWood},
	"minecraft:jungle_pressure_plate":              {kind: plateLight, soundGroup: plateSoundWood},
	"minecraft:mangrove_pressure_plate":            {kind: plateLight, soundGroup: plateSoundWood},
	"minecraft:oak_pressure_plate":                 {kind: plateLight, soundGroup: plateSoundWood},
	"minecraft:pale_oak_pressure_plate":            {kind: plateLight, soundGroup: plateSoundWood},
	"minecraft:spruce_pressure_plate":              {kind: plateLight, soundGroup: plateSoundWood},
	"minecraft:warped_pressure_plate":              {kind: plateLight, soundGroup: plateSoundWood},
	"minecraft:stone_pressure_plate":               {kind: plateLight, livingOnly: true, soundGroup: plateSoundStone},
	"minecraft:polished_blackstone_pressure_plate": {kind: plateLight, livingOnly: true, soundGroup: plateSoundStone},
	// 加权压力板（金 15、铁 150：检测全部实体，金属音效）。
	"minecraft:light_weighted_pressure_plate": {kind: plateWeighted, maxWeight: plateGoldMaxWeight, soundGroup: plateSoundMetal},
	"minecraft:heavy_weighted_pressure_plate": {kind: plateWeighted, maxWeight: plateIronMaxWeight, soundGroup: plateSoundMetal},
}

// platePressedProperty 返回压力板“是否按下”状态的属性名（powered/power）。
const (
	platePoweredProperty = "powered"
	platePowerProperty   = "power"
)

// plateStateEntry 是状态 ID → 压力板行为 + 当前信号的查表项。
type plateStateEntry struct {
	info   plateInfo
	signal int
}

// plateStateTable 由注册表状态表在首次使用时构建（方块状态 ID 全局唯一）。
var plateStateTable = buildPlateStateTable()

// plateStateFast 是“该状态 ID 是未按下的压力板”的数组查表（状态 ID 为 uint16）：
// 热路径（实体进入方块扫描）用一次数组访问代替 map 查询，绝大多数方块状态
// （空气、石头等）在本表中为 0，直接跳过。
var plateStateFast = buildPlateStateFast()

// buildPlateStateTable 从注册表属性状态表构建全部压力板状态。
func buildPlateStateTable() map[uint16]plateStateEntry {
	table := make(map[uint16]plateStateEntry)
	for name, info := range plateInfos {
		states, ok := registry.BlockPropertyStates[name]
		if !ok {
			continue
		}
		for _, state := range states {
			table[state.ID] = plateStateEntry{info: info, signal: plateSignalOfProps(info, state.Props)}
		}
	}
	return table
}

// buildPlateStateFast 构建实体进入扫描的数组查表。
func buildPlateStateFast() [65536]uint8 {
	var table [65536]uint8
	for id, entry := range plateStateTable {
		if entry.signal == 0 {
			table[id] = 1
		}
	}
	return table
}

// plateSignalOfProps 返回压力板状态属性对应的输出信号
// （原版 getSignalForState：轻型 powered ? 15 : 0；加权 power 属性值）。
func plateSignalOfProps(info plateInfo, props map[string]string) int {
	if info.kind == plateWeighted {
		power, err := strconv.Atoi(props[platePowerProperty])
		if err != nil || power < 0 {
			return 0
		}
		if power > 15 {
			return 15
		}
		return power
	}
	if props[platePoweredProperty] == "true" {
		return 15
	}
	return 0
}

// plateTouchBox 返回压力板检测盒（世界坐标）：与原版 TOUCH_AABB 一致。
func plateTouchBox(x, y, z int) world.Box {
	return world.Box{
		MinX: float64(x) + 1.0/16, MinY: float64(y), MinZ: float64(z) + 1.0/16,
		MaxX: float64(x) + 15.0/16, MaxY: float64(y) + 4.0/16, MaxZ: float64(z) + 15.0/16,
	}
}

// boxesOverlap 报告两个包围盒是否相交（相触不算，对应原版 AABB.intersects）。
func boxesOverlap(a, b world.Box) bool {
	return a.MinX < b.MaxX && a.MaxX > b.MinX &&
		a.MinY < b.MaxY && a.MaxY > b.MinY &&
		a.MinZ < b.MaxZ && a.MaxZ > b.MinZ
}

// plateSignalStrength 返回检测盒内实体对应的信号强度
// （原版 getSignalStrength：轻型 >0 → 15；加权 ceil(min(n, maxWeight) × 15 / maxWeight)）。
func (s *Server) plateSignalStrength(dim world.Dimension, pos [3]int, info plateInfo) int {
	count := s.countEntitiesInPlateBox(dim, plateTouchBox(pos[0], pos[1], pos[2]), info.livingOnly)
	if count <= 0 {
		return 0
	}
	if info.kind == plateLight {
		return 15
	}
	if count > info.maxWeight {
		count = info.maxWeight
	}
	// ceil(count × 15 / maxWeight)：整除后有余数时进一（原版 Mth.ceil）。
	signal := count * 15 / info.maxWeight
	if count*15%info.maxWeight != 0 {
		signal++
	}
	if signal > 15 {
		signal = 15
	}
	return signal
}

// countEntitiesInPlateBox 统计检测盒内的实体数量。livingOnly 为 true 时
// 只统计玩家与生物（原版 LivingEntity）；否则统计全部实体
// （玩家、生物、掉落物、经验球、箭矢）。旁观玩家不计入。
// 由 tick 路径调用（entityMu 在函数内自行加锁）。
func (s *Server) countEntitiesInPlateBox(dim world.Dimension, box world.Box, livingOnly bool) int {
	count := 0
	for _, player := range s.playerSnapshot() {
		if !player.isJoined() || player.dimensionID() != dim {
			continue
		}
		if player.gameModeID() == uint8(config.GameModeSpectator) {
			continue // 旁观者不计入（原版 EntitySelector.NO_SPECTATORS）
		}
		x, y, z, _, _ := player.playerPosition()
		if boxesOverlap(box, playerBox(x, y, z)) {
			count++
		}
	}
	s.entityMu.Lock()
	defer s.entityMu.Unlock()
	for _, m := range s.mobs {
		if m.Dim != dim {
			continue
		}
		if boxesOverlap(box, mobBox(m.X, m.Y, m.Z)) {
			count++
		}
	}
	if livingOnly {
		return count
	}
	for _, e := range s.items {
		if e.Dim != dim {
			continue
		}
		if boxesOverlap(box, itemBoxAt(e.X, e.Y, e.Z)) {
			count++
		}
	}
	for _, orb := range s.xpOrbs {
		if orb.Dim != dim {
			continue
		}
		if boxesOverlap(box, xpOrbBoxAt(orb.X, orb.Y, orb.Z)) {
			count++
		}
	}
	// 箭矢与经验球同为 0.5×0.5×0.5 碰撞盒。
	for _, arrow := range s.arrows {
		if arrow.Dim != dim {
			continue
		}
		if boxesOverlap(box, xpOrbBoxAt(arrow.X, arrow.Y, arrow.Z)) {
			count++
		}
	}
	return count
}

// updatePressurePlate 重算压力板的信号并把变化写回世界
// （原版 BasePressurePlateBlock.updateSignalStrength）：
// 从当前位置的方块状态重新读取类型（防止方块已被替换后使用陈旧信息），
// 信号变化时更新方块状态（含红石传播），仍在按下状态时安排释放计划刻。
func (s *Server) updatePressurePlate(w *world.World, dim world.Dimension, pos [3]int) {
	state := w.BlockAt(pos[0], pos[1], pos[2])
	name, props, ok := registry.StateProps(state)
	if !ok {
		return
	}
	info, known := plateInfos[name]
	if !known {
		return // 方块已被替换为非压力板
	}
	oldSignal := plateSignalOfProps(info, props)
	newSignal := s.plateSignalStrength(dim, pos, info)
	if oldSignal != newSignal {
		next := cloneProps(props)
		if info.kind == plateWeighted {
			next[platePowerProperty] = strconv.Itoa(newSignal)
		} else if newSignal > 0 {
			next[platePoweredProperty] = "true"
		} else {
			next[platePoweredProperty] = "false"
		}
		if s.setRedstoneBlock(w, pos[0], pos[1], pos[2], name, next) {
			s.playPlateSound(dim, pos, info, newSignal > 0)
		}
	}
	if newSignal > 0 {
		ticks := plateLightReleaseTicks
		if info.kind == plateWeighted {
			ticks = plateWeightedReleaseTicks
		}
		s.scheduleRedstoneTick(dim, pos, ticks)
	}
}

// tickPressurePlate 处理压力板到期的计划刻：仍在按下状态时重算
// （原版 tick：仅当当前信号 > 0 时重算；重算后仍有实体则自动续期）。
func (s *Server) tickPressurePlate(w *world.World, dim world.Dimension, pos [3]int, state uint16) {
	entry, ok := plateStateTable[state]
	if !ok || entry.signal <= 0 {
		return
	}
	s.updatePressurePlate(w, dim, pos)
}

// playPlateSound 广播压力板按下/弹起的点击音效。
func (s *Server) playPlateSound(dim world.Dimension, pos [3]int, info plateInfo, pressed bool) {
	sounds := s.plateClickOn
	if !pressed {
		sounds = s.plateClickOff
	}
	if info.soundGroup < 0 || info.soundGroup >= len(sounds) {
		return
	}
	sound := sounds[info.soundGroup]
	if sound == 0 {
		return // 注册表缺少音效：静默跳过
	}
	x := float64(pos[0]) + 0.5
	y := float64(pos[1]) + 0.1
	z := float64(pos[2]) + 0.5
	packet := protocol.EncodeSoundEffect(sound, protocol.SoundCategoryBlock, x, y, z, 0.3, 1, 0)
	s.writeToNearbyPlayers(packet, dim, x, z, nil)
}

// resolvePlateSounds 解析压力板点击音效 ID（缺失时保持 0：播放时静默跳过）。
func (s *Server) resolvePlateSounds() {
	s.plateClickOn = [plateSoundGroups]int32{}
	s.plateClickOff = [plateSoundGroups]int32{}
	names := [plateSoundGroups][2]string{
		{"minecraft:block.wooden_pressure_plate.click_on", "minecraft:block.wooden_pressure_plate.click_off"},
		{"minecraft:block.stone_pressure_plate.click_on", "minecraft:block.stone_pressure_plate.click_off"},
		{"minecraft:block.metal_pressure_plate.click_on", "minecraft:block.metal_pressure_plate.click_off"},
	}
	for group, pair := range names {
		if id, ok := registry.StaticEntryID("minecraft:sound_event", pair[0]); ok {
			s.plateClickOn[group] = id
		}
		if id, ok := registry.StaticEntryID("minecraft:sound_event", pair[1]); ok {
			s.plateClickOff[group] = id
		}
	}
}

// entityInsideBlocks 对应原版 Entity.checkInsideBlocks：遍历实体包围盒覆盖的
// 方块位置（边界按 1e-7 收缩，与原版 BlockPos.containing 一致），
// 向“实体进入方块”行为分发（当前仅压力板：未按下时立即重算）。
//
// w 是实体所在维度的世界（调用方通常已解析，避免重复取世界）。
// 逐 (x,z) 列批量读取方块状态（区块只解析/加锁一次）；未加载区块跳过
// （与原版一致：未加载区块不会触发实体进入方块）。扫描结果用
// plateStateFast 数组快速过滤，绝大多数方块（空气/石头等）一次数组访问即跳过。
//
// 由玩家移动、生物/掉落物/经验球的位置推进路径调用。
func (s *Server) entityInsideBlocks(w *world.World, dim world.Dimension, box world.Box) {
	if w == nil {
		return
	}
	const eps = 1e-7
	x1 := int(math.Floor(box.MinX + eps))
	x2 := int(math.Floor(box.MaxX - eps))
	y1 := int(math.Floor(box.MinY + eps))
	y2 := int(math.Floor(box.MaxY - eps))
	z1 := int(math.Floor(box.MinZ + eps))
	z2 := int(math.Floor(box.MaxZ - eps))
	if x2 < x1 || y2 < y1 || z2 < z1 {
		return
	}
	// 限制扫描范围（异常坐标防护：实体的包围盒不会超过 2×3×2 个单元格）。
	if (x2-x1+1)*(y2-y1+1)*(z2-z1+1) > 64 {
		return
	}
	// 列缓冲：常见实体盒的 y 跨度不超过 4 格，超出时临时分配。
	var stackBuf [4]uint16
	column := stackBuf[:0]
	if span := y2 - y1 + 1; span <= len(stackBuf) {
		column = stackBuf[:span]
	} else {
		column = make([]uint16, span)
	}
	for x := x1; x <= x2; x++ {
		for z := z1; z <= z2; z++ {
			n := w.LoadedColumn(x, y1, y2, z, column)
			for i := 0; i < n; i++ {
				if plateStateFast[column[i]] == 0 {
					continue
				}
				s.updatePressurePlate(w, dim, [3]int{x, y1 + i, z})
			}
		}
	}
}
