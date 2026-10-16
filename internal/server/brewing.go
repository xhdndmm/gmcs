//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"log/slog"
	"math"
	"sync"

	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 酿造台容器：5 槽窗口（0–2 瓶子、3 材料、4 燃料）与逐 tick 酿造。
//
// 运行时状态由 Server.brewings 权威持有（无窗口打开时同样推进），
// 方块实体保存槽位与进度（存储格式 v5），重启后恢复。
//
// 行为对齐原版 BrewingStandBlockEntity（1.21.11）：
//   - 燃料：槽 4 的烈焰粉在燃料耗尽时补充 20 次（1 个 = 20 次酿造）；
//   - 酿造：有可酿造配方且燃料 > 0 时开始，时长 400 tick；开始时记录当前
//     材料物品，酿造期间材料被换掉/拿走则取消（不退还燃料）；
//   - 完成：对每个非空瓶子执行 PotionBrewing.mix（先容器配方改瓶子物品，
//     再药水配方改药水种类），材料消耗 1 个，广播 1035 号世界事件
//     （酿造完成音效与粒子）；
//   - 方块状态的 has_bottle_0/1/2 随瓶子槽空/非空变化更新并广播；
//   - 槽位规则（窗口与漏斗共用 canPlace 语义）：瓶子槽只接受药水类或
//     玻璃瓶且必须为空；材料槽只接受酿造材料；燃料槽只接受烈焰粉；
//     漏斗从材料槽只允许抽出玻璃瓶（canTakeItemThroughFace）。
//
// 已知简化（见 docs/TODO.md）：酿造配方为字节码对照的手写表（见
// registry/brewing.go），材料槽的 crafting remainder 未实现（原版全部
// 酿造材料都没有剩余物，当前行为等价）。

const (
	// brewingSlots 是酿造台的槽位数（0–2 瓶子、3 材料、4 燃料）。
	brewingSlots = 5
	// brewingTimeTicks 是一次酿造的时长（原版 PotionBrewing.BREWING_TIME_SECONDS = 400）。
	brewingTimeTicks = 400
	// brewingFuelUses 是 1 个烈焰粉提供的酿造次数（原版 BrewingStandBlockEntity.FUEL_USES）。
	brewingFuelUses = 20
	// brewingStandBlockName 是酿造台的方块注册名。
	brewingStandBlockName = "minecraft:brewing_stand"
)

// brewingState 是一个酿造台方块的运行时状态（含槽位与进度）。
type brewingState struct {
	mu         sync.Mutex
	dim        world.Dimension
	pos        [3]int
	slots      [brewingSlots]item.Stack
	brewTime   int32   // 酿造进度：400 → 0
	fuel       int32   // 剩余酿造次数
	ingredient int32   // 酿造开始时的材料物品 ID（中途更换即取消）
	bottles    [3]bool // 上次的 has_bottle_0/1/2 状态（用于方块状态更新）
	dirty      bool    // 有未写回方块实体的变更
}

// 酿造相关物品与方块实体类型 ID（initBrewingItems 解析；缺失时对应规则退化）。
var (
	blazePowderItemID  int32
	glassBottleItemID  int32
	brewingStandTypeID int32
)

// initBrewingItems 解析酿造台相关的物品与方块实体类型 ID。
func (s *Server) initBrewingItems() {
	blazePowderItemID, _ = registry.ItemID("minecraft:blaze_powder")
	glassBottleItemID, _ = registry.ItemID("minecraft:glass_bottle")
	brewingStandTypeID, _ = registry.StaticEntryID("minecraft:block_entity_type", brewingStandBlockName)
}

// registerBrewing 注册（或返回已有的）酿造台运行时状态。
// 方块实体已带内容/进度时载入状态。由会话 goroutine 与 Tick 调用。
func (s *Server) registerBrewing(dim world.Dimension, x, y, z int) *brewingState {
	key := containerKey{dim: dim, x: x, y: y, z: z}
	s.brewingMu.Lock()
	if state, ok := s.brewings[key]; ok {
		s.brewingMu.Unlock()
		return state
	}
	s.brewingMu.Unlock()

	w := s.worldFor(dim)
	if w == nil {
		return nil
	}
	// 只接受仍在原位的酿造台方块（方块被替换后不再注册）。
	blockName, ok := s.blockNameOf(w.BlockAt(x, y, z))
	if !ok || blockName != brewingStandBlockName {
		return nil
	}
	entity, err := w.BlockEntityAt(x, y, z)
	if err != nil {
		return nil
	}
	state := &brewingState{
		dim:      dim,
		pos:      [3]int{x, y, z},
		brewTime: entity.BrewTime,
		fuel:     entity.BrewFuel,
		dirty:    true,
	}
	for i := range state.slots {
		if i < len(entity.Items) {
			state.slots[i] = item.Stack{
				ItemID:   entity.Items[i].ItemID,
				Count:    entity.Items[i].Count,
				PotionID: entity.Items[i].PotionID,
			}
		}
	}
	// 原版 loadAdditional：BrewTime > 0 时把材料槽当前物品记为酿造材料。
	if state.brewTime > 0 {
		state.ingredient = state.slots[3].ItemID
	}
	for i := 0; i < 3; i++ {
		state.bottles[i] = !state.slots[i].IsEmpty()
	}

	s.brewingMu.Lock()
	defer s.brewingMu.Unlock()
	if existing, ok := s.brewings[key]; ok {
		return existing
	}
	s.brewings[key] = state
	return state
}

// tickBrewingStands 每 tick 推进全部已注册酿造台的酿造进度。
// 每 20 tick 重新扫描加载区块，注册未跟踪的酿造台（含从磁盘恢复的）。
func (s *Server) tickBrewingStands() {
	s.brewingScanCounter++
	if s.brewingScanCounter >= 20 {
		s.brewingScanCounter = 0
		s.scanBrewingStands()
	}
	s.brewingMu.Lock()
	states := make([]*brewingState, 0, len(s.brewings))
	for _, state := range s.brewings {
		states = append(states, state)
	}
	s.brewingMu.Unlock()
	for _, state := range states {
		if s.tickBrewing(state) {
			s.flushBrewing(state)
			// 有玩家查看窗口时同步进度与槽位。
			key := containerKey{dim: state.dim, x: state.pos[0], y: state.pos[1], z: state.pos[2]}
			s.containerMu.Lock()
			container := s.containers[key]
			s.containerMu.Unlock()
			if container != nil && container.brewing == state {
				s.refreshBrewingWindow(state)
			}
		}
		// has_bottle 方块状态可能因窗口点击或漏斗搬运而变化。
		s.updateBrewingBottles(state)
	}
}

// scanBrewingStands 把已加载区块中的酿造台方块实体注册到运行时表（全部已加载维度）。
func (s *Server) scanBrewingStands() {
	if brewingStandTypeID == 0 {
		return
	}
	typeIDs := map[int32]bool{brewingStandTypeID: true}
	s.worldMu.Lock()
	type loadedWorld struct {
		dim   world.Dimension
		world *world.World
	}
	worlds := make([]loadedWorld, 0, len(s.worlds))
	for dim, w := range s.worlds {
		worlds = append(worlds, loadedWorld{dim: dim, world: w})
	}
	s.worldMu.Unlock()
	for _, item := range worlds {
		for _, pos := range item.world.FurnaceBlockPositions(typeIDs) {
			s.registerBrewing(item.dim, pos[0], pos[1], pos[2])
		}
	}
}

// tickBrewing 推进单个酿造台一 tick；返回是否有变更需要写回。
// 对照原版 BrewingStandBlockEntity.serverTick。
func (s *Server) tickBrewing(state *brewingState) bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	changed := false

	// 燃料耗尽时用烈焰粉补充（1 个 = 20 次）。
	fuelStack := &state.slots[4]
	if state.fuel <= 0 && !fuelStack.IsEmpty() && fuelStack.ItemID == blazePowderItemID {
		state.fuel = brewingFuelUses
		fuelStack.Count--
		if fuelStack.Count <= 0 {
			*fuelStack = item.Empty()
		}
		changed = true
	}

	brewable := brewingBrewable(&state.slots)
	ingredientStack := &state.slots[3]
	if state.brewTime > 0 {
		// 酿造中：推进进度；完成且有配方时产出，否则（材料被换走）取消。
		state.brewTime--
		changed = true
		if state.brewTime == 0 && brewable {
			s.doBrew(state)
		} else if !brewable || ingredientStack.IsEmpty() || ingredientStack.ItemID != state.ingredient {
			state.brewTime = 0
		}
	} else if brewable && state.fuel > 0 {
		// 开始酿造：消耗 1 次燃料，记录材料物品。
		state.fuel--
		state.brewTime = brewingTimeTicks
		state.ingredient = ingredientStack.ItemID
		changed = true
	}
	return changed
}

// brewingBrewable 报告槽位中是否存在可执行的酿造
// （对照原版 BrewingStandBlockEntity.isBrewable）。
func brewingBrewable(slots *[brewingSlots]item.Stack) bool {
	ingredient := slots[3]
	if ingredient.IsEmpty() || !registry.BrewingIsIngredient(ingredient.ItemID) {
		return false
	}
	for i := 0; i < 3; i++ {
		bottle := slots[i]
		if bottle.IsEmpty() {
			continue
		}
		potion, ok := brewingPotionOf(bottle)
		if !ok {
			continue
		}
		if registry.BrewingHasMix(bottle.ItemID, potion, ingredient.ItemID) {
			return true
		}
	}
	return false
}

// brewingPotionOf 返回药水瓶子的有效药水种类：
// PotionID > 0 时为对应药水；否则药水物品的默认组件为水瓶。
// 非药水容器物品返回 false。
func brewingPotionOf(stack item.Stack) (int32, bool) {
	if !registry.BrewingIsContainerItem(stack.ItemID) {
		return 0, false
	}
	if stack.PotionID > 0 {
		return stack.PotionID - 1, true
	}
	return registry.PotionWater, true
}

// brewingMix 计算一次酿造的结果（对照原版 PotionBrewing.mix）：
// 先匹配容器配方（瓶子物品变化、药水保留），再匹配药水配方（药水变化、
// 物品保留）；无匹配时原样返回。返回值总是数量 1 的新堆栈。
func brewingMix(ingredient, input item.Stack) item.Stack {
	if input.IsEmpty() {
		return input
	}
	potion, ok := brewingPotionOf(input)
	if !ok {
		return input
	}
	if toItem, ok := registry.BrewingContainerMix(input.ItemID, ingredient.ItemID); ok {
		return item.Stack{ItemID: toItem, Count: 1, PotionID: potion + 1}
	}
	if toPotion, ok := registry.BrewingPotionMix(potion, ingredient.ItemID); ok {
		return item.Stack{ItemID: input.ItemID, Count: 1, PotionID: toPotion + 1}
	}
	return input
}

// doBrew 执行一次酿造（调用方持有 state.mu 且确认可酿造）：
// 三个瓶子槽按配方转换，材料消耗 1 个，广播完成事件。
func (s *Server) doBrew(state *brewingState) {
	ingredient := &state.slots[3]
	for i := 0; i < 3; i++ {
		state.slots[i] = brewingMix(*ingredient, state.slots[i])
	}
	ingredient.Count--
	if ingredient.Count <= 0 {
		*ingredient = item.Empty()
	}
	s.broadcastWorldEvent(state.dim, state.pos, protocol.WorldEventBrewingStandBrew, 0)
}

// broadcastWorldEvent 把一个世界事件发给同维度附近的玩家（距离与方块更新一致）。
func (s *Server) broadcastWorldEvent(dim world.Dimension, pos [3]int, eventID int32, data int32) {
	packet := protocol.EncodeWorldEvent(eventID, pos[0], pos[1], pos[2], data, false)
	cx := float64(pos[0]) + 0.5
	cz := float64(pos[2]) + 0.5
	for _, other := range s.playerSnapshot() {
		if !other.isJoined() || other.dimensionID() != dim {
			continue
		}
		px, _, pz, _, _ := other.playerPosition()
		if math.Hypot(px-cx, pz-cz) > mobMoveBroadcastRange {
			continue
		}
		other.tryWrite(packet)
	}
}

// flushBrewing 把酿造台状态写回方块实体（槽位 + 进度）。
func (s *Server) flushBrewing(state *brewingState) {
	state.mu.Lock()
	entity := world.BlockEntity{
		TypeID:   brewingStandTypeID,
		Items:    make([]world.ContainerItem, brewingSlots),
		BrewTime: state.brewTime,
		BrewFuel: state.fuel,
	}
	for i, stack := range state.slots {
		entity.Items[i] = containerItemFromStack(stack)
	}
	state.dirty = false
	state.mu.Unlock()

	if err := s.worldFor(state.dim).SetBlockEntity(state.pos[0], state.pos[1], state.pos[2], entity); err != nil {
		slog.Error("写回酿造台状态失败", "x", state.pos[0], "y", state.pos[1], "z", state.pos[2], "error", err)
	}
}

// destroyBrewing 处理酿造台方块被破坏：写回状态后按普通容器掉落内容。
func (s *Server) destroyBrewing(dim world.Dimension, breaker *session, x, y, z int, def *containerDef) {
	key := containerKey{dim: dim, x: x, y: y, z: z}
	s.brewingMu.Lock()
	state := s.brewings[key]
	delete(s.brewings, key)
	s.brewingMu.Unlock()
	if state != nil {
		s.flushBrewing(state)
	}
	s.destroyContainer(dim, breaker, x, y, z, def)
}

// updateBrewingBottles 检查瓶子槽空/非空状态并更新方块状态 has_bottle_0/1/2。
// 由 tick 在状态变化后调用（原版在 serverTick 末尾做同样的比较）。
func (s *Server) updateBrewingBottles(state *brewingState) {
	state.mu.Lock()
	var bottles [3]bool
	for i := 0; i < 3; i++ {
		bottles[i] = !state.slots[i].IsEmpty()
	}
	changed := bottles != state.bottles
	if changed {
		state.bottles = bottles
	}
	dim, pos := state.dim, state.pos
	state.mu.Unlock()
	if !changed {
		return
	}
	s.setBrewingBottleState(dim, pos, bottles)
}

// setBrewingBottleState 把 has_bottle 属性写入方块状态并广播（原版
// level.setBlock(pos, state, 2)：不触发邻居更新）。
func (s *Server) setBrewingBottleState(dim world.Dimension, pos [3]int, bottles [3]bool) {
	w := s.worldFor(dim)
	if w == nil {
		return
	}
	current := w.BlockAt(pos[0], pos[1], pos[2])
	if !registry.IsPropertyBlockState(brewingStandBlockName, current) {
		return // 方块已被替换或破坏
	}
	boolText := func(v bool) string {
		if v {
			return "true"
		}
		return "false"
	}
	next, ok := registry.BlockStateWithProps(brewingStandBlockName, map[string]string{
		"has_bottle_0": boolText(bottles[0]),
		"has_bottle_1": boolText(bottles[1]),
		"has_bottle_2": boolText(bottles[2]),
	})
	if !ok || next == current {
		return
	}
	if !w.SetBlock(pos[0], pos[1], pos[2], next) {
		return
	}
	s.broadcastBlockUpdate(dim, pos[0], pos[1], pos[2], int32(next))
}

// refreshBrewingWindow 在酿造台窗口打开时把进度与槽位同步给观察者
// （Set Container Property 进度条 + Set Container Slot 槽位）。
func (s *Server) refreshBrewingWindow(state *brewingState) {
	key := containerKey{dim: state.dim, x: state.pos[0], y: state.pos[1], z: state.pos[2]}
	s.containerMu.Lock()
	container := s.containers[key]
	s.containerMu.Unlock()
	if container == nil || container.brewing != state {
		return
	}
	state.mu.Lock()
	slots := append([]item.Stack(nil), state.slots[:]...)
	properties := [2]int16{
		int16(clampI32(state.brewTime, 32767)),
		int16(clampI32(state.fuel, 32767)),
	}
	state.mu.Unlock()
	for index, stack := range slots {
		s.broadcastContainerSlot(container, index, stack, nil)
	}
	s.broadcastBrewingProperties(container, properties)
}

// sendBrewingProperties 把酿造台窗口属性发送给单个玩家（打开窗口时）。
func (s *Server) sendBrewingProperties(player *session, state *brewingState) {
	state.mu.Lock()
	properties := [2]int16{
		int16(clampI32(state.brewTime, 32767)),
		int16(clampI32(state.fuel, 32767)),
	}
	state.mu.Unlock()
	for index, value := range properties {
		player.tryWrite(protocol.EncodeSetContainerProperty(player.windowID, int16(index), value))
	}
}

// broadcastBrewingProperties 把窗口属性同步给容器的全部观察者。
func (s *Server) broadcastBrewingProperties(container *containerState, properties [2]int16) {
	container.mu.Lock()
	viewers := make([]*session, 0, len(container.viewers))
	for viewer := range container.viewers {
		viewers = append(viewers, viewer)
	}
	container.mu.Unlock()
	for _, viewer := range viewers {
		windowID := viewer.windowID
		for index, value := range properties {
			viewer.tryWrite(protocol.EncodeSetContainerProperty(windowID, int16(index), value))
		}
	}
}

// brewingSlotBacking 是酿造台窗口的槽位访问层：
// 0–2 瓶子、3 材料、4 燃料、5–31 主背包、32–40 快捷栏（共 41 槽）。
type brewingSlotBacking struct {
	server *Server
	player *session
	state  *brewingState
}

// brewingInvSlot 把窗口槽位（玩家部分）映射到物品栏槽位。
func brewingInvSlot(index int) int {
	if index >= brewingSlots+27 {
		return index - (brewingSlots + 27) // 快捷栏
	}
	return index + 4 // 主背包（9–35）
}

// SlotCount 返回窗口槽位总数（5 + 36）。
func (b brewingSlotBacking) SlotCount() int { return brewingSlots + 36 }

// Get 返回槽位内容。
func (b brewingSlotBacking) Get(index int) item.Stack {
	if index < brewingSlots {
		b.state.mu.Lock()
		defer b.state.mu.Unlock()
		return b.state.slots[index]
	}
	return b.player.inventory.Get(brewingInvSlot(index))
}

// Set 设置槽位内容；酿造槽立即写回方块实体并同步窗口观察者。
func (b brewingSlotBacking) Set(index int, stack item.Stack) {
	if index < brewingSlots {
		b.state.mu.Lock()
		changed := !b.state.slots[index].Equal(stack)
		b.state.slots[index] = stack
		b.state.dirty = true
		b.state.mu.Unlock()
		if changed {
			b.server.flushBrewing(b.state)
		}
		return
	}
	b.player.inventory.Set(brewingInvSlot(index), stack)
}

// Supported 报告槽位是否可交互（酿造台全部槽位可自由取放）。
func (b brewingSlotBacking) Supported(int) bool { return true }

// CanPlace 报告槽位是否可放入该物品（对照原版 BrewingStandMenu 各槽 mayPlace）：
// 瓶子槽只接受药水类或玻璃瓶且必须为空；材料槽只接受酿造材料；燃料槽只接受烈焰粉。
func (b brewingSlotBacking) CanPlace(slot int, stack item.Stack) bool {
	switch {
	case slot >= 0 && slot <= 2:
		if !registry.BrewingIsContainerItem(stack.ItemID) && stack.ItemID != glassBottleItemID {
			return false
		}
		b.state.mu.Lock()
		empty := b.state.slots[slot].IsEmpty()
		b.state.mu.Unlock()
		return empty
	case slot == 3:
		return registry.BrewingIsIngredient(stack.ItemID)
	case slot == 4:
		return stack.ItemID == blazePowderItemID
	}
	return true
}

// SlotLimit 报告槽位的堆叠数量上限（原版 PotionSlot.getMaxStackSize = 1：
// 每个瓶位只放 1 个；Shift 点击 16 个玻璃瓶时每次只进 1 个）。
func (b brewingSlotBacking) SlotLimit(slot int) int32 {
	if slot >= 0 && slot <= 2 {
		return 1
	}
	return 0
}
