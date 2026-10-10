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

// 熔炉类容器（熔炉/高炉/烟熏炉）：原料 + 燃料 + 产物三槽窗口与逐 tick 烹饪。
//
// 运行时状态由 Server.furnaceStates 权威持有（无窗口打开时同样推进），
// 方块实体保存槽位与燃烧/烹饪进度（存储格式 v3），重启后恢复。
//
// 行为对齐原版 AbstractFurnaceBlockEntity：
//   - 点燃条件：有匹配配方且产物可叠加、燃料可用；点燃立即消耗 1 个燃料
//     （岩浆桶留下空桶）；
//   - 高炉/烟熏炉燃料燃烧速度是熔炉的 2 倍（燃烧时长减半），烹饪时间
//     由配方给出（熔炉 200 / 高炉与烟熏炉 100 tick）；
//   - 熄火时部分烹饪进度以 2 倍速度回退；
//   - 经验累积在产物中，玩家手动取走产物时结算。
//
// 已知简化（见 docs/TODO.md）：燃料槽允许放入任意物品（原版限制为燃料）；
// 湿海绵 + 空桶的特殊转化未实现。

// furnaceState 是一个熔炉方块的运行时状态（含槽位与进度）。
type furnaceState struct {
	mu     sync.Mutex
	dim    world.Dimension
	pos    [3]int
	typeID int32
	kind   registry.CookingKind
	slots  [3]item.Stack // 0 原料、1 燃料、2 产物

	burnRemaining int32
	burnTotal     int32
	cookProgress  int32
	cookTotal     int32
	cookXP        float64

	dirty bool // 有未写回方块实体的变更
}

// furnaceTypeIDs 返回熔炉类方块实体类型 ID 集合（扫描注册用）。
func (s *Server) furnaceTypeIDs() map[int32]bool {
	ids := make(map[int32]bool, 3)
	for _, name := range []string{"minecraft:furnace", "minecraft:blast_furnace", "minecraft:smoker"} {
		if id, ok := registry.StaticEntryID("minecraft:block_entity_type", name); ok {
			ids[id] = true
		}
	}
	return ids
}

// registerFurnace 注册（或返回已有的）熔炉运行时状态。
// 方块实体已带内容/进度时载入状态。由会话 goroutine 与 Tick 调用。
func (s *Server) registerFurnace(dim world.Dimension, x, y, z int) *furnaceState {
	key := containerKey{dim: dim, x: x, y: y, z: z}
	s.furnaceMu.Lock()
	if state, ok := s.furnaces[key]; ok {
		s.furnaceMu.Unlock()
		return state
	}
	s.furnaceMu.Unlock()

	w := s.worldFor(dim)
	entity, err := w.BlockEntityAt(x, y, z)
	if err != nil {
		return nil
	}
	// 优先按方块名解析类型（空方块实体的 TypeID 零值会与 furnace 的 ID 0 混淆）。
	blockState := w.BlockAt(x, y, z)
	blockName, nameOK := s.blockNames[blockState]
	var typeID int32
	if nameOK {
		typeID, _ = registry.StaticEntryID("minecraft:block_entity_type", blockName)
	}
	if !s.furnaceKindOf(typeID) {
		// 方块名不可用时退回方块实体类型。
		typeID = entity.TypeID
		if !s.furnaceKindOf(typeID) {
			return nil
		}
	}
	state := &furnaceState{
		dim:           dim,
		pos:           [3]int{x, y, z},
		typeID:        typeID,
		kind:          s.cookKindOf(typeID),
		burnRemaining: entity.BurnRemaining,
		burnTotal:     entity.BurnTotal,
		cookProgress:  entity.CookProgress,
		cookTotal:     entity.CookTotal,
		dirty:         true,
	}
	for i := range state.slots {
		if i < len(entity.Items) {
			state.slots[i] = item.Stack{ItemID: entity.Items[i].ItemID, Count: entity.Items[i].Count}
		}
	}

	s.furnaceMu.Lock()
	defer s.furnaceMu.Unlock()
	if existing, ok := s.furnaces[key]; ok {
		return existing
	}
	s.furnaces[key] = state
	return state
}

// furnaceKindOf 报告方块实体类型是否为熔炉类。
func (s *Server) furnaceKindOf(typeID int32) bool {
	_, ok := s.cookKinds[typeID]
	return ok
}

// cookKindOf 返回方块实体类型对应的烹饪类型。
func (s *Server) cookKindOf(typeID int32) registry.CookingKind {
	return s.cookKinds[typeID]
}

// initFurnaceKinds 构建方块实体类型 → 烹饪类型映射。
func (s *Server) initFurnaceKinds() {
	s.cookKinds = make(map[int32]registry.CookingKind, 3)
	add := func(blockEntity, menu string, kind registry.CookingKind) {
		typeID, ok := registry.StaticEntryID("minecraft:block_entity_type", blockEntity)
		if !ok {
			slog.Warn("熔炉方块实体类型缺失", "name", blockEntity)
			return
		}
		s.cookKinds[typeID] = kind
	}
	add("minecraft:furnace", "minecraft:furnace", registry.CookingSmelting)
	add("minecraft:blast_furnace", "minecraft:blast_furnace", registry.CookingBlasting)
	add("minecraft:smoker", "minecraft:smoker", registry.CookingSmoking)
}

// tickFurnaces 每 tick 推进全部已注册熔炉的燃烧与烹饪进度。
// 每 20 tick 重新扫描加载区块，注册未跟踪的熔炉（含从磁盘恢复的）。
func (s *Server) tickFurnaces() {
	s.furnaceScanCounter++
	if s.furnaceScanCounter >= 20 {
		s.furnaceScanCounter = 0
		s.scanFurnaces()
	}
	s.furnaceMu.Lock()
	states := make([]*furnaceState, 0, len(s.furnaces))
	for _, state := range s.furnaces {
		states = append(states, state)
	}
	s.furnaceMu.Unlock()
	for _, state := range states {
		if s.tickFurnace(state) {
			s.flushFurnace(state)
			// 有玩家查看窗口时同步进度条与槽位。
			key := containerKey{dim: state.dim, x: state.pos[0], y: state.pos[1], z: state.pos[2]}
			s.containerMu.Lock()
			container := s.containers[key]
			s.containerMu.Unlock()
			if container != nil && container.furnace == state {
				s.broadcastFurnaceUpdate(container)
			}
		}
	}
}

// scanFurnaces 把已加载区块中的熔炉类方块实体注册到运行时表（全部已加载维度）。
func (s *Server) scanFurnaces() {
	typeIDs := s.furnaceTypeIDs()
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
			s.registerFurnace(item.dim, pos[0], pos[1], pos[2])
		}
	}
}

// tickFurnace 推进单个熔炉一 tick；返回是否有变更需要写回。
func (s *Server) tickFurnace(state *furnaceState) bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	changed := false

	if state.burnRemaining > 0 {
		state.burnRemaining--
		changed = true
	}
	recipe, ok := registry.CookingRecipeFor(state.kind, state.slots[0].ItemID)
	canOutput := ok && recipeOutputFits(state.slots[2], recipe.Result, recipe.ResultCount)

	// 未点燃时尝试点燃（消耗燃料）。
	if state.burnRemaining == 0 && canOutput {
		if ticks := s.furnaceBurnTicks(state, state.slots[1]); ticks > 0 {
			fuel := state.slots[1]
			fuel.Count--
			if fuel.Count <= 0 {
				// 岩浆桶留下空桶；其余燃料直接清空。
				if isLavaBucket(fuel.ItemID) {
					fuel = item.Stack{ItemID: bucketItemID, Count: 1}
				} else {
					fuel = item.Empty()
				}
			}
			state.slots[1] = fuel
			state.burnRemaining = ticks
			state.burnTotal = ticks
			changed = true
		}
	}

	if state.burnRemaining > 0 && canOutput {
		if state.cookTotal != recipe.CookTime {
			state.cookTotal = recipe.CookTime
			changed = true
		}
		state.cookProgress++
		changed = true
		if state.cookProgress >= recipe.CookTime {
			state.cookProgress = 0
			state.slots[0].Count--
			if state.slots[0].Count <= 0 {
				state.slots[0] = item.Empty()
			}
			if state.slots[2].IsEmpty() {
				state.slots[2] = item.Stack{ItemID: recipe.Result, Count: recipe.ResultCount}
			} else {
				state.slots[2].Count += recipe.ResultCount
			}
			state.cookXP += recipe.Experience
		}
	} else if state.burnRemaining == 0 && state.cookProgress > 0 {
		// 熄火：部分进度以 2 倍速度回退（原版行为）。
		state.cookProgress -= 2
		if state.cookProgress < 0 {
			state.cookProgress = 0
		}
		changed = true
	}
	return changed
}

// furnaceBurnTicks 返回该燃料在该类熔炉中的燃烧时长（0 = 非燃料）。
// 高炉/烟熏炉燃烧速度是熔炉的 2 倍（时长减半）。
func (s *Server) furnaceBurnTicks(state *furnaceState, fuel item.Stack) int32 {
	ticks := registry.FuelBurnTicks(fuel.ItemID)
	if ticks <= 0 {
		return 0
	}
	if state.kind != registry.CookingSmelting && ticks > 1 {
		ticks /= 2
	}
	return ticks
}

// recipeOutputFits 报告产物槽能否容纳本次产出。
func recipeOutputFits(result item.Stack, itemID, count int32) bool {
	if result.IsEmpty() {
		return true
	}
	return result.ItemID == itemID && result.Count+count <= result.MaxStack()
}

// isLavaBucket 报告物品是否为岩浆桶（燃料耗尽留下空桶）。
func isLavaBucket(itemID int32) bool {
	return itemID == bucketLavaItemID
}

// bucketItemID / bucketLavaItemID 由 initFurnaceKinds 解析。
var (
	bucketItemID     int32
	bucketLavaItemID int32
)

// initFurnaceItems 解析燃料相关物品 ID。
func initFurnaceItems() (int32, int32) {
	bucket, _ := registry.ItemID("minecraft:bucket")
	lava, _ := registry.ItemID("minecraft:lava_bucket")
	return bucket, lava
}

// broadcastFurnaceUpdate 把熔炉进度与槽位变化同步给正在查看窗口的玩家
// （Set Container Property 进度条 + Set Container Slot 槽位）。
func (s *Server) broadcastFurnaceUpdate(container *containerState) {
	state := container.furnace
	if state == nil {
		return
	}
	container.mu.Lock()
	viewers := make([]*session, 0, len(container.viewers))
	for viewer := range container.viewers {
		viewers = append(viewers, viewer)
	}
	container.mu.Unlock()
	if len(viewers) == 0 {
		return
	}
	state.mu.Lock()
	properties := [4]int16{
		int16(clampI32(state.burnRemaining, 32767)),
		int16(clampI32(state.burnTotal, 32767)),
		int16(clampI32(state.cookProgress, 32767)),
		int16(clampI32(state.cookTotal, 32767)),
	}
	slots := make([]item.Stack, 3)
	copy(slots, state.slots[:])
	state.mu.Unlock()

	for _, viewer := range viewers {
		windowID := viewer.windowID
		stateID := viewer.windowState
		for i, value := range properties {
			viewer.tryWrite(protocol.EncodeSetContainerProperty(windowID, int16(i), value))
		}
		for i, stack := range slots {
			viewer.tryWrite(protocol.EncodeSetContainerSlot(windowID, stateID, int16(i), stack.AppendSlot(nil)))
		}
	}
}

// clampI32 把值限制在 [0, max]。
func clampI32(value, max int32) int32 {
	if value < 0 {
		return 0
	}
	if value > max {
		return max
	}
	return value
}

// flushFurnace 把熔炉状态写回方块实体（槽位 + 进度）。
func (s *Server) flushFurnace(state *furnaceState) {
	state.mu.Lock()
	entity := world.BlockEntity{
		TypeID:        state.typeID,
		Items:         make([]world.ContainerItem, 3),
		BurnRemaining: state.burnRemaining,
		BurnTotal:     state.burnTotal,
		CookProgress:  state.cookProgress,
		CookTotal:     state.cookTotal,
	}
	for i, stack := range state.slots {
		entity.Items[i] = world.ContainerItem{ItemID: stack.ItemID, Count: stack.Count}
	}
	state.dirty = false
	state.mu.Unlock()

	if err := s.worldFor(state.dim).SetBlockEntity(state.pos[0], state.pos[1], state.pos[2], entity); err != nil {
		slog.Error("写回熔炉状态失败", "x", state.pos[0], "y", state.pos[1], "z", state.pos[2], "error", err)
	}
}

// destroyFurnace 处理熔炉方块被破坏：写回状态后按普通容器掉落内容。
func (s *Server) destroyFurnace(dim world.Dimension, breaker *session, x, y, z int, def *containerDef) {
	key := containerKey{dim: dim, x: x, y: y, z: z}
	s.furnaceMu.Lock()
	state := s.furnaces[key]
	delete(s.furnaces, key)
	s.furnaceMu.Unlock()
	if state != nil {
		s.flushFurnace(state)
	}
	s.destroyContainer(dim, breaker, x, y, z, def)
}

// awardFurnaceXP 把熔炉累积的经验发放给取走产物的玩家（原版行为）。
func (s *Server) awardFurnaceXP(player *session, state *furnaceState) {
	state.mu.Lock()
	total := state.cookXP
	state.cookXP = 0
	state.mu.Unlock()
	if total <= 0 {
		return
	}
	// 整数部分固定发放，小数部分按概率发放（原版算法）。
	points := int32(math.Floor(total))
	if frac := total - float64(points); frac > 0 && s.nextRandom()%1000 < uint64(frac*1000) {
		points++
	}
	if points > 0 {
		bar, level, exp := player.addExperience(points)
		player.tryWrite(protocol.EncodeSetExperience(bar, level, exp))
	}
}

// furnaceSlotBacking 是熔炉窗口的槽位访问层：
// 0 原料、1 燃料、2 产物、3–29 主背包、30–38 快捷栏（共 39 槽）。
type furnaceSlotBacking struct {
	player *session
	state  *furnaceState
}

// furnaceInvSlot 把窗口槽位映射到物品栏槽位。
func furnaceInvSlot(index int) int {
	if index >= 30 {
		return index - 30 // 快捷栏
	}
	return index + 6 // 主背包（9–35）
}

// SlotCount 返回窗口槽位总数（3 + 36）。
func (b furnaceSlotBacking) SlotCount() int { return 3 + 36 }

// Get 返回槽位内容。
func (b furnaceSlotBacking) Get(index int) item.Stack {
	if index < 3 {
		b.state.mu.Lock()
		defer b.state.mu.Unlock()
		return b.state.slots[index]
	}
	return b.player.inventory.Get(furnaceInvSlot(index))
}

// Set 设置槽位内容。
func (b furnaceSlotBacking) Set(index int, stack item.Stack) {
	if index < 3 {
		b.state.mu.Lock()
		b.state.slots[index] = stack
		b.state.dirty = true
		b.state.mu.Unlock()
		return
	}
	b.player.inventory.Set(furnaceInvSlot(index), stack)
}

// Supported 报告槽位是否可交互（产物槽只取不放，由专用路径处理）。
func (b furnaceSlotBacking) Supported(index int) bool {
	return index != 2
}

// furnaceResultClick 处理产物槽点击（只取不放），返回新的光标。
func (s *Server) furnaceResultClick(player *session, b furnaceSlotBacking, working []item.Stack, cursor item.Stack, click protocol.ContainerClick) item.Stack {
	result := working[2]
	if result.IsEmpty() {
		return cursor
	}
	switch click.Mode {
	case protocol.ClickModePickup:
		switch {
		case cursor.IsEmpty():
			working[2] = item.Empty()
			s.awardFurnaceXP(player, b.state)
			return result
		case cursor.ItemID == result.ItemID && cursor.Count+result.Count <= cursor.MaxStack():
			working[2] = item.Empty()
			cursor.Count += result.Count
			s.awardFurnaceXP(player, b.state)
			return cursor
		}
	case protocol.ClickModeQuickMove:
		for _, target := range [][2]int{{3, 30}, {30, 39}} {
			if !fitsInArea(working, target[0], target[1], result) {
				continue
			}
			mergeIntoArea(working, result, target[0], target[1])
			working[2] = item.Empty()
			s.awardFurnaceXP(player, b.state)
			break
		}
	case protocol.ClickModeThrow:
		working[2] = item.Empty()
		s.awardFurnaceXP(player, b.state)
		s.dropFromContainer(player, result)
	}
	return cursor
}
