//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"

	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 发射器/投掷器（对照 1.21.11 反编译的 DispenserBlock/DropperBlock/
// DispenserBlockEntity/DispenseItemBehavior 逐字节码移植）。
//
// 语义要点：
//   - 红石触发：neighborChanged 里 isPowered = hasNeighborSignal(pos) ||
//     hasNeighborSignal(pos.above())；上升沿调度 4 tick 计划刻并把
//     triggered 置 true，下降沿置 false；到期 tick 在 triggered 为 true 时
//     执行一次发射；
//   - 发射位置 = 方块中心 + 0.7 × facing；默认弹出行为的物品实体位置在
//     水平朝向时 y −= 0.15625、竖直朝向时 y −= 0.125，速度 =
//     triangle(发射方向 × speed, 0.0172275 × 6)（speed = rand × 0.1 + 0.2，
//     triangle(mean, dev) = mean + dev × (rand − rand)）；
//   - 随机槽位 = 蓄水池采样（每满槽 1/count 概率替换，count 从 1 递增）；
//   - 空槽位/放不下时播放失败音效（世界事件 1001）；成功播放 1000 与
//     朝向粒子动画（世界事件 2000，data = 朝向序号）；
//   - 投掷器：前方是容器时插入 1 个（面规则与漏斗一致），否则按默认行为
//     弹出；插入成功时槽位物品数 −1，失败保持不变。
//
// 已实现的行为（其余物品回退为默认弹出，与原版的差异见 docs/TODO.md）：
//   - 箭/光灵箭：按 facing 发射箭矢（速度 1.1、散布 6°，与原版
//     ProjectileDispenseBehavior 的 DispenseConfig 默认值一致）；
//   - 打火石：目标位置可放火时放置火方块（下方为支撑面），点击 TNT 时点燃；
//   - TNT：生成点燃的 TNT 实体（80 tick 引信）；
//   - 生成蛋：在主世界/下界/末地生成对应生物（不支持的种类回退为弹出）；
//   - 潜影盒：在发射位置放置潜影盒方块；
//   - 水桶/岩浆桶/空桶：放置/收集流体（见 fluids.go）。

const (
	dispenserBlockName = "minecraft:dispenser"
	dropperBlockName   = "minecraft:dropper"
	// dispenserTriggerDelay 是触发后到发射的计划刻延迟（原版 TRIGGER_DURATION）。
	dispenserTriggerDelay = 4
)

// dispenserFacingOffset 返回 facing 属性对应的方块偏移（协议朝向序号顺序）。
func dispenserFacingOffset(facing string) [3]int {
	switch facing {
	case "down":
		return [3]int{0, -1, 0}
	case "up":
		return [3]int{0, 1, 0}
	case "north":
		return [3]int{0, 0, -1}
	case "south":
		return [3]int{0, 0, 1}
	case "west":
		return [3]int{-1, 0, 0}
	case "east":
		return [3]int{1, 0, 0}
	}
	return [3]int{0, -1, 0}
}

// dispenserFacingOrdinal 返回方向的协议序号（down=0、up=1、north=2、
// south=3、west=4、east=5；世界事件 2000 的 data）。
func dispenserFacingOrdinal(facing string) int32 {
	switch facing {
	case "down":
		return 0
	case "up":
		return 1
	case "north":
		return 2
	case "south":
		return 3
	case "west":
		return 4
	case "east":
		return 5
	}
	return 0
}

// isDispenserBlockName 报告方块名是否为发射器/投掷器。
func isDispenserBlockName(name string) bool {
	return name == dispenserBlockName || name == dropperBlockName
}

// isDispenserState 报告方块状态是否为发射器/投掷器（用于注册表扫描）。
func isDispenserState(state uint16) bool {
	return registry.IsPropertyBlockState(dispenserBlockName, state) ||
		registry.IsPropertyBlockState(dropperBlockName, state)
}

// refreshDispenser 处理发射器/投掷器的邻居变化（原版 DispenserBlock.neighborChanged）：
// 电源上升沿调度 4 tick 后的发射计划刻并置 triggered；下降沿清除 triggered。
func (s *Server) refreshDispenser(w *world.World, pos [3]int, state uint16) {
	name, props, ok := registry.StateProps(state)
	if !ok || !isDispenserBlockName(name) {
		return
	}
	above := [3]int{pos[0], pos[1] + 1, pos[2]}
	powered := s.bestNeighborSignal(w, pos) > 0 || s.bestNeighborSignal(w, above) > 0
	triggered := props["triggered"] == "true"
	switch {
	case powered && !triggered:
		next := cloneProps(props)
		next["triggered"] = "true"
		if s.setRedstoneBlock(w, pos[0], pos[1], pos[2], name, next) {
			// 原版 scheduleTick 在已有计划刻时更新为更早的时间；这里直接覆盖
			// 延迟，保证快速脉冲的多次上升沿都能得到自己的发射。
			s.rescheduleRedstoneTick(w.Dimension(), pos, dispenserTriggerDelay)
		}
	case !powered && triggered:
		next := cloneProps(props)
		next["triggered"] = "false"
		s.setRedstoneBlock(w, pos[0], pos[1], pos[2], name, next)
	}
}

// rescheduleRedstoneTick 覆盖式安排计划刻（与 scheduleRedstoneTick 的
// “已有则忽略”相对；用于发射器快速脉冲语义）。
func (s *Server) rescheduleRedstoneTick(dim world.Dimension, pos [3]int, ticks int) {
	if ticks < 1 {
		ticks = 1
	}
	s.redstoneMu.Lock()
	if s.redstoneTicks == nil {
		s.redstoneTicks = make(map[containerKey]int)
	}
	s.redstoneTicks[containerKey{dim: dim, x: pos[0], y: pos[1], z: pos[2]}] = ticks
	s.redstoneMu.Unlock()
}

// tickDispenser 处理发射器的到期计划刻（原版 DispenserBlock.tick）：
// triggered 为 true 时执行一次发射。
func (s *Server) tickDispenser(w *world.World, dim world.Dimension, pos [3]int, state uint16) {
	name, props, ok := registry.StateProps(state)
	if !ok || !isDispenserBlockName(name) || props["triggered"] != "true" {
		return
	}
	s.dispenseFrom(w, dim, pos, name, props["facing"])
}

// dispenseFrom 执行一次发射（原版 DispenserBlock.dispenseFrom / DropperBlock.dispenseFrom）：
// 取随机非空槽位，应用对应行为，把结果写回槽位。
func (s *Server) dispenseFrom(w *world.World, dim world.Dimension, pos [3]int, blockName, facing string) {
	target := s.acquireTransferTarget(dim, pos)
	if target == nil {
		return
	}
	defer target.close()
	slot := s.dispenserRandomSlot(target.items)
	if slot < 0 || target.items[slot].IsEmpty() {
		s.broadcastWorldEvent(dim, pos, 1001, 0)
		return
	}
	stack := target.items[slot]
	var (
		remaining item.Stack
		side      item.Stack
	)
	if blockName == dropperBlockName {
		remaining = s.dropperDispense(dim, pos, facing, stack)
	} else {
		remaining, side = s.dispenserBehavior(w, dim, pos, facing, stack)
	}
	if !remaining.Equal(stack) {
		target.items[slot] = remaining
		target.markTouched(slot)
	}
	if !side.IsEmpty() {
		s.dispenserHandleSideItem(target, dim, pos, facing, side)
	}
	target.commit()
}

// dispenserHandleSideItem 把行为的副产物（如装满的桶）放回发射器，
// 放不下时弹出到世界（原版 DefaultDispenseItemBehavior.addToInventoryOrDispense）。
func (s *Server) dispenserHandleSideItem(target *transferTarget, dim world.Dimension, pos [3]int, facing string, side item.Stack) {
	for i := range target.items {
		existing := target.items[i]
		if existing.IsEmpty() {
			target.items[i] = side
			target.markTouched(i)
			return
		}
		if existing.SameKind(side) && existing.Count < existing.MaxStack() {
			existing.Count++
			target.items[i] = existing
			target.markTouched(i)
			return
		}
	}
	if !s.itemsEnabled {
		return
	}
	offset := dispenserFacingOffset(facing)
	x := float64(pos[0]) + 0.5 + float64(offset[0])*0.7
	y := float64(pos[1]) + 0.5 + float64(offset[1])*0.7
	z := float64(pos[2]) + 0.5 + float64(offset[2])*0.7
	if offset[1] != 0 {
		y -= 0.125
	} else {
		y -= 0.15625
	}
	speed := float64(s.nextRandom()%1000)/1000*0.1 + 0.2
	s.spawnItem(dim, side, x, y, z,
		s.dispenseTriangle(float64(offset[0])*speed, 0.0172275*6),
		s.dispenseTriangle(0.2, 0.0172275*6),
		s.dispenseTriangle(float64(offset[2])*speed, 0.0172275*6),
		itemPickupDelayMining)
}

// dispenserRandomSlot 按原版蓄水池采样选择随机非空槽位（无物品返回 -1）。
func (s *Server) dispenserRandomSlot(slots []item.Stack) int {
	slot := -1
	count := uint64(1)
	for i, stack := range slots {
		if stack.IsEmpty() {
			continue
		}
		if s.nextRandom()%count == 0 {
			slot = i
		}
		count++
	}
	return slot
}

// dispenseTriangle 返回 Java RandomSource.triangle 的等价采样：
// mean + deviation × (rand − rand)。
func (s *Server) dispenseTriangle(mean, deviation float64) float64 {
	a := float64(s.nextRandom()%1_000_000) / 1_000_000
	b := float64(s.nextRandom()%1_000_000) / 1_000_000
	return mean + deviation*(a-b)
}

// dispenseEject 按原版 DefaultDispenseItemBehavior 弹出 1 个物品：
// 返回消耗后的堆栈（物品系统不可用时原样返回并播放失败音效）。
func (s *Server) dispenseEject(dim world.Dimension, pos [3]int, facing string, stack item.Stack) item.Stack {
	if !s.itemsEnabled || stack.IsEmpty() {
		s.broadcastWorldEvent(dim, pos, 1001, 0)
		return stack
	}
	offset := dispenserFacingOffset(facing)
	x := float64(pos[0]) + 0.5 + float64(offset[0])*0.7
	y := float64(pos[1]) + 0.5 + float64(offset[1])*0.7
	z := float64(pos[2]) + 0.5 + float64(offset[2])*0.7
	if offset[1] != 0 {
		y -= 0.125
	} else {
		y -= 0.15625
	}
	speed := float64(s.nextRandom()%1000)/1000*0.1 + 0.2
	vx := s.dispenseTriangle(float64(offset[0])*speed, 0.0172275*6)
	vy := s.dispenseTriangle(0.2, 0.0172275*6)
	vz := s.dispenseTriangle(float64(offset[2])*speed, 0.0172275*6)

	ejected := stack
	ejected.Count = 1
	remaining := stack
	remaining.Count--
	if remaining.Count <= 0 {
		remaining = item.Empty()
	}
	s.spawnItem(dim, ejected, x, y, z, vx, vy, vz, itemPickupDelayMining)
	s.broadcastWorldEvent(dim, pos, 1000, 0)
	s.broadcastWorldEvent(dim, pos, 2000, dispenserFacingOrdinal(facing))
	return remaining
}

// dropperDispense 处理投掷器发射：前方有容器时尝试插入 1 个，
// 否则按默认行为弹出（原版 DropperBlock.dispenseFrom）。
func (s *Server) dropperDispense(dim world.Dimension, pos [3]int, facing string, stack item.Stack) item.Stack {
	offset := dispenserFacingOffset(facing)
	front := [3]int{pos[0] + offset[0], pos[1] + offset[1], pos[2] + offset[2]}
	if _, ok := world.SectionIndex(front[1]); !ok {
		return s.dispenseEject(dim, pos, facing, stack)
	}
	// 调用方（dispenseFrom）已持有 containerTransferMu（自身槽位事务），
	// 这里使用 locked 变体避免对箱子类目标重复加锁导致自锁。
	target := s.acquireTransferTargetLocked(dim, front)
	if target == nil {
		return s.dispenseEject(dim, pos, facing, stack)
	}
	defer target.close()
	one := stack
	one.Count = 1
	remainder := target.insert(one, oppositeDir(facing))
	if remainder.IsEmpty() {
		target.commit()
		remaining := stack
		remaining.Count--
		if remaining.Count <= 0 {
			remaining = item.Empty()
		}
		return remaining
	}
	return stack
}

// dispenserBehavior 按物品分派发射行为；返回（消耗后的槽位内容, 副产物）。
func (s *Server) dispenserBehavior(w *world.World, dim world.Dimension, pos [3]int, facing string, stack item.Stack) (item.Stack, item.Stack) {
	name, ok := registry.ItemName(stack.ItemID)
	if !ok || stack.IsEmpty() {
		return stack, item.Empty()
	}
	succeed := func() (item.Stack, item.Stack) {
		s.broadcastWorldEvent(dim, pos, 1000, 0)
		s.broadcastWorldEvent(dim, pos, 2000, dispenserFacingOrdinal(facing))
		return consumeOne(stack), item.Empty()
	}
	switch name {
	case "minecraft:arrow", "minecraft:spectral_arrow":
		if s.shootDispenserArrow(dim, pos, facing) {
			return succeed()
		}
	case "minecraft:flint_and_steel":
		if s.dispenserIgnite(dim, pos, facing) {
			return succeed()
		}
	case "minecraft:tnt":
		if s.primeTnt(dim, pos, facing) {
			return succeed()
		}
	case "minecraft:water_bucket", "minecraft:lava_bucket", "minecraft:powder_snow_bucket":
		if remaining, ok := s.dispenserEmptyBucket(w, dim, pos, facing, stack, name); ok {
			return succeedWith(remaining, item.Empty(), dim, pos, facing, s)
		}
		return s.dispenseEject(dim, pos, facing, stack), item.Empty()
	case "minecraft:bucket":
		if remaining, side, ok := s.dispenserFillBucket(w, dim, pos, facing, stack); ok {
			return succeedWith(remaining, side, dim, pos, facing, s)
		}
		return s.dispenseEject(dim, pos, facing, stack), item.Empty()
	case "minecraft:shulker_box":
		if s.dispenserPlaceBlock(w, dim, pos, facing, "minecraft:shulker_box") {
			return succeed()
		}
	case "minecraft:bone_meal":
		if s.dispenserBoneMeal(w, dim, pos, facing) {
			return succeed()
		}
	default:
		if kind, ok := spawnEggMobs[name]; ok {
			if s.dispenserSpawnEgg(w, dim, pos, facing, kind) {
				return succeed()
			}
		}
	}
	return s.dispenseEject(dim, pos, facing, stack), item.Empty()
}

// succeedWith 在成功音效后返回槽位与副产物。
func succeedWith(remaining, side item.Stack, dim world.Dimension, pos [3]int, facing string, s *Server) (item.Stack, item.Stack) {
	s.broadcastWorldEvent(dim, pos, 1000, 0)
	s.broadcastWorldEvent(dim, pos, 2000, dispenserFacingOrdinal(facing))
	return remaining, side
}

// consumeOne 消耗一个物品（数量归零时返回空堆栈）。
func consumeOne(stack item.Stack) item.Stack {
	stack.Count--
	if stack.Count <= 0 {
		return item.Empty()
	}
	return stack
}

// spawnEggMobs 是生成蛋物品名 → 生物种类（服务器支持的八种）。
var spawnEggMobs = map[string]mobKind{
	"minecraft:zombie_spawn_egg":   mobZombie,
	"minecraft:skeleton_spawn_egg": mobSkeleton,
	"minecraft:creeper_spawn_egg":  mobCreeper,
	"minecraft:spider_spawn_egg":   mobSpider,
	"minecraft:enderman_spawn_egg": mobEnderman,
	"minecraft:cow_spawn_egg":      mobCow,
	"minecraft:pig_spawn_egg":      mobPig,
	"minecraft:sheep_spawn_egg":    mobSheep,
}

// dispenserSpawnEgg 在发射位置生成生成蛋对应的生物（原版 SpawnEggItem 的
// 发射行为：在发射位置生成、无需地面校验）。注册表缺少该生物时返回 false。
func (s *Server) dispenserSpawnEgg(w *world.World, dim world.Dimension, pos [3]int, facing string, kind mobKind) bool {
	if !s.mobsEnabled {
		return false
	}
	if _, ok := s.mobTypeIDs[kind]; !ok {
		return false
	}
	offset := dispenserFacingOffset(facing)
	x := float64(pos[0]) + 0.5 + float64(offset[0])
	y := float64(pos[1]) + float64(offset[1])
	z := float64(pos[2]) + 0.5 + float64(offset[2])
	if _, ok := world.SectionIndex(int(math.Floor(y))); !ok {
		return false
	}
	return s.addMob(dim, kind, x, y, z) != nil
}

// shootDispenserArrow 按 facing 发射一支箭（原版 ProjectileDispenseBehavior +
// DispenseConfig 默认值：速度 1.1、散布 6°）；返回是否成功。
// 光灵箭在当前实现中使用普通箭实体的外观与伤害（差异见 docs/TODO.md）。
func (s *Server) shootDispenserArrow(dim world.Dimension, pos [3]int, facing string) bool {
	if !s.arrowsEnabled {
		return false
	}
	offset := dispenserFacingOffset(facing)
	x := float64(pos[0]) + 0.5
	y := float64(pos[1]) + 0.5
	z := float64(pos[2]) + 0.5
	const power = 1.1
	vx := float64(offset[0]) * power
	vy := float64(offset[1]) * power
	vz := float64(offset[2]) * power
	// Projectile.shoot 的散布：每轴加 6° 的随机扰动（velocity 归一化前）。
	spread := func(v float64) float64 {
		return v + s.dispenseTriangle(0, 0.0172275*6)*2
	}
	vx, vy, vz = spread(vx), spread(vy), spread(vz)
	arrow := &arrowEntity{
		ID:     s.entityIDs.Add(1),
		UUID:   newEntityUUID(),
		Dim:    dim,
		X:      x,
		Y:      y,
		Z:      z,
		VelX:   vx,
		VelY:   vy,
		VelZ:   vz,
		Damage: 2,
	}
	s.entityMu.Lock()
	s.arrows[arrow.ID] = arrow
	targets := s.markTrackedNearbyLocked(dim, x, z, nil, arrow.ID)
	s.entityMu.Unlock()
	add := protocol.EncodeAddEntity(arrow.ID, arrow.UUID, s.arrowTypeID,
		arrow.X, arrow.Y, arrow.Z, arrow.VelX, arrow.VelY, arrow.VelZ, 0, 0)
	for _, player := range targets {
		player.tryWrite(add)
	}
	return true
}

// dispenserIgnite 处理打火石发射行为：在发射位置放置火（可放置时），
// 或点燃发射位置的 TNT 方块（原版 FlintAndSteelItem 的发射行为）。
func (s *Server) dispenserIgnite(dim world.Dimension, pos [3]int, facing string) bool {
	w := s.worldFor(dim)
	if w == nil {
		return false
	}
	offset := dispenserFacingOffset(facing)
	target := [3]int{pos[0] + offset[0], pos[1] + offset[1], pos[2] + offset[2]}
	if _, ok := world.SectionIndex(target[1]); !ok {
		return false
	}
	state := w.BlockAt(target[0], target[1], target[2])
	if name, ok := s.blockNameOf(state); ok && name == "minecraft:tnt" {
		if s.primeTntBlock(dim, w, target[0], target[1], target[2]) {
			return true
		}
		return false
	}
	return s.placeFireAt(w, dim, target[0], target[1], target[2])
}

// dispenserPlaceBlock 在发射位置放置方块（潜影盒等）；目标不可替换时失败。
func (s *Server) dispenserPlaceBlock(w *world.World, dim world.Dimension, pos [3]int, facing, blockName string) bool {
	offset := dispenserFacingOffset(facing)
	x, y, z := pos[0]+offset[0], pos[1]+offset[1], pos[2]+offset[2]
	if _, ok := world.SectionIndex(y); !ok {
		return false
	}
	state, ok := registry.BlockStateIDs[blockName]
	if !ok {
		return false
	}
	existing := w.BlockAt(x, y, z)
	if existing != world.AirBlock && !isWaterState(existing) && !isLavaState(existing) {
		return false
	}
	if !w.SetBlock(x, y, z, state) {
		return false
	}
	s.broadcastBlockUpdate(dim, x, y, z, int32(state))
	s.updateRedstoneAround(dim, x, y, z)
	s.updateFluidsAround(dim, x, y, z)
	return true
}

// dispenserBoneMeal 处理发射器的骨粉行为：对发射位置的草方块催熟
// （近似原版 BoneMealItem：目标必须是草方块；在周围 3×3 的草方块上按
// 概率生成草与花）。成功（至少放置一个方块）时消耗骨粉。
// 不支持作物/树苗催熟（gmcs 无作物系统），见 docs/TODO.md。
func (s *Server) dispenserBoneMeal(w *world.World, dim world.Dimension, pos [3]int, facing string) bool {
	offset := dispenserFacingOffset(facing)
	tx, ty, tz := pos[0]+offset[0], pos[1]+offset[1], pos[2]+offset[2]
	if _, ok := world.SectionIndex(ty); !ok {
		return false
	}
	state := w.BlockAt(tx, ty, tz)
	if name, ok := s.blockNameOf(state); !ok || name != "minecraft:grass_block" {
		return false
	}
	changed := false
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			x, z := tx+dx, tz+dz
			if _, ok := world.SectionIndex(ty + 1); !ok {
				continue
			}
			ground := w.BlockAt(x, ty, z)
			if name, ok := s.blockNameOf(ground); !ok || name != "minecraft:grass_block" {
				continue
			}
			if w.BlockAt(x, ty+1, z) != world.AirBlock {
				continue
			}
			roll := s.nextRandom() % 100
			var plantName string
			switch {
			case roll < 20:
				switch s.nextRandom() % 3 {
				case 0:
					plantName = "minecraft:dandelion"
				case 1:
					plantName = "minecraft:poppy"
				default:
					plantName = "minecraft:cornflower"
				}
			case roll < 70:
				plantName = "minecraft:short_grass"
			default:
				continue
			}
			plantState, ok := registry.BlockStateIDs[plantName]
			if !ok {
				continue
			}
			if !w.SetBlock(x, ty+1, z, plantState) {
				continue
			}
			s.broadcastBlockUpdate(dim, x, ty+1, z, int32(plantState))
			changed = true
		}
	}
	return changed
}
