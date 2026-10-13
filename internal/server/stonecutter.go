package server

import (
	"sync"

	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
)

// 切石机：输入槽（0）+ 结果槽（1）+ 玩家物品栏（2–37）。
// 配方选择来自 Container Button Click（按钮编号 = 输入物品过滤后的配方下标，
// 顺序与 Update Recipes 的切石机列表一致）。
//
// 与熔炉不同，输入槽是窗口临时状态（原版 StonecutterMenu 的输入容器不持久化），
// 关闭窗口时归还玩家背包（放不下则掉落）。
//
// 简化：输入变化时自动选中第一个可用配方（原版需要客户端主动选择；
// 客户端的配方列表同步后行为一致）。结果槽为实时计算，不参与普通取放。

// stonecutterState 是一个打开的切石机窗口状态。selected 是选中的切石机
// 配方全局下标（-1 = 未选中）。mu 保护字段（测试与其他 goroutine 经访问器读取）。
type stonecutterState struct {
	mu       sync.Mutex
	input    item.Stack
	selected int32
}

// setInput 更新输入槽（并校验选中配方是否仍适用）。
func (st *stonecutterState) setInput(stack item.Stack) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.input = stack
	if !st.selectedValidLocked() {
		st.selected = -1
	}
}

// inputStack 返回输入槽内容。
func (st *stonecutterState) inputStack() item.Stack {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.input
}

// selectedIndex 返回当前选中的配方下标（-1 = 无）。
func (st *stonecutterState) selectedIndex() int32 {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.selected
}

// selectRecipe 按“过滤后下标”选中配方；非法下标返回 false。
func (st *stonecutterState) selectRecipe(filteredIndex int32) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	matches := registry.StonecuttingRecipesFor(st.input.ItemID)
	if filteredIndex < 0 || int(filteredIndex) >= len(matches) {
		return false
	}
	st.selected = matches[filteredIndex]
	return true
}

// selectedValidLocked 报告当前选中配方是否仍适用于输入（调用方需持有锁）。
func (st *stonecutterState) selectedValidLocked() bool {
	if st.selected < 0 {
		return false
	}
	recipe, ok := registry.StonecuttingRecipeAt(st.selected)
	if !ok {
		return false
	}
	for _, id := range recipe.Ingredient {
		if id == st.input.ItemID && !st.input.IsEmpty() {
			return true
		}
	}
	return false
}

// autoSelectLocked 在没有有效选中时自动选中第一个可用配方
// （调用方需持有锁）。
func (st *stonecutterState) autoSelectLocked() {
	if st.selectedValidLocked() {
		return
	}
	st.selected = -1
	matches := registry.StonecuttingRecipesFor(st.input.ItemID)
	if len(matches) > 0 {
		st.selected = matches[0]
	}
}

// resultStack 返回当前选中配方对当前输入的产物（无有效选择时为空）。
func (st *stonecutterState) resultStack() item.Stack {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.autoSelectLocked()
	if st.selected < 0 {
		return item.Empty()
	}
	recipe, ok := registry.StonecuttingRecipeAt(st.selected)
	if !ok {
		return item.Empty()
	}
	return item.Stack{ItemID: recipe.Result, Count: recipe.ResultCount}
}

// consumeInput 消耗 1 个输入物品（产物被取走时调用）。
func (st *stonecutterState) consumeInput() {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.input.IsEmpty() {
		return
	}
	st.input.Count--
	if st.input.Count <= 0 {
		st.input = item.Empty()
	}
	st.autoSelectLocked()
}

// 切石机窗口的槽位布局。
const (
	stonecutterInputSlot  = 0
	stonecutterResultSlot = 1
	stonecutterSlots      = 38 // 1 输入 + 1 结果 + 36 物品栏
)

// stonecutterSlotBacking 是切石机窗口的槽位访问层。
type stonecutterSlotBacking struct {
	player *session
	state  *stonecutterState
}

// SlotCount 返回窗口槽位总数。
func (b stonecutterSlotBacking) SlotCount() int { return stonecutterSlots }

// Get 返回槽位内容（结果槽按配方实时计算）。
func (b stonecutterSlotBacking) Get(index int) item.Stack {
	switch index {
	case stonecutterInputSlot:
		return b.state.inputStack()
	case stonecutterResultSlot:
		return b.state.resultStack()
	default:
		return b.player.inventory.Get(containerPlayerInvSlot(index - 2))
	}
}

// Set 设置槽位内容（结果槽忽略）。
func (b stonecutterSlotBacking) Set(index int, stack item.Stack) {
	switch index {
	case stonecutterInputSlot:
		b.state.setInput(stack)
	case stonecutterResultSlot:
	default:
		b.player.inventory.Set(containerPlayerInvSlot(index-2), stack)
	}
}

// Supported 报告槽位是否可交互（结果槽只取不放）。
func (b stonecutterSlotBacking) Supported(index int) bool {
	return index != stonecutterResultSlot
}

// openStonecutter 打开切石机窗口（右键切石机方块）。
// 由会话 goroutine 调用。
func (s *Server) openStonecutter(player *session) {
	if player.openContainer != nil {
		s.closeContainer(player, false)
	}
	if player.getOpenCraft() != nil {
		s.closeCrafting(player, false)
	}
	if player.getStonecutter() != nil {
		s.closeStonecutter(player, false)
	}
	if player.getSmithing() != nil {
		s.closeSmithing(player, false)
	}
	if player.getAnvil() != nil {
		s.closeAnvil(player, false)
	}
	menuType, ok := registry.StaticEntryID("minecraft:menu", "minecraft:stonecutter")
	if !ok {
		return
	}
	player.setStonecutter(&stonecutterState{selected: -1})
	player.windowID = player.nextWindowID()
	player.windowState = 0
	if err := player.writePacket(protocol.EncodeOpenScreen(player.windowID, menuType, "Stonecutter")); err != nil {
		return
	}
	s.sendStonecutterContent(player)
}

// sendStonecutterContent 把切石机窗口的全部槽位内容发送给玩家。
func (s *Server) sendStonecutterContent(player *session) {
	state := player.getStonecutter()
	if state == nil {
		return
	}
	backing := stonecutterSlotBacking{player: player, state: state}
	slots := make([][]byte, 0, stonecutterSlots)
	for i := 0; i < stonecutterSlots; i++ {
		slots = append(slots, backing.Get(i).AppendSlot(nil))
	}
	_ = player.writePacket(protocol.EncodeContainerSetContent(
		player.windowID, player.windowState, slots, player.getCursor().AppendSlot(nil)))
}

// closeStonecutter 关闭切石机窗口：输入槽归还背包（放不下则掉落）。
// sendClose 表示是否向客户端发送 Close Container。
// 由会话 goroutine 调用。
func (s *Server) closeStonecutter(player *session, sendClose bool) {
	state := player.getStonecutter()
	if state == nil {
		return
	}
	player.setStonecutter(nil)
	windowID := player.windowID
	s.releaseCursor(player)
	input := state.inputStack()
	if !input.IsEmpty() {
		remaining, changed := player.inventory.Add(input)
		for _, slot := range changed {
			player.tryWrite(protocol.EncodeSetPlayerInventory(int32(slot), player.inventory.Get(slot).AppendSlot(nil)))
		}
		if remaining > 0 {
			left := input
			left.Count = remaining
			s.dropFromContainer(player, left)
		}
	}
	if sendClose {
		player.tryWrite(protocol.EncodeContainerClose(windowID))
	}
}

// handleStonecutterButton 处理切石机的 Container Button Click（选择配方）。
// 由会话读循环调用。
func (s *Server) handleStonecutterButton(player *session, buttonID int32) {
	state := player.getStonecutter()
	if state == nil {
		return
	}
	if !state.selectRecipe(buttonID) {
		return
	}
	s.broadcastStonecutterResult(player)
}

// broadcastStonecutterResult 把结果槽的当前内容发送给玩家（结果变化时调用）。
func (s *Server) broadcastStonecutterResult(player *session) {
	state := player.getStonecutter()
	if state == nil {
		return
	}
	player.windowState++
	backing := stonecutterSlotBacking{player: player, state: state}
	result := backing.Get(stonecutterResultSlot)
	player.tryWrite(protocol.EncodeSetContainerSlot(
		player.windowID, player.windowState, stonecutterResultSlot, result.AppendSlot(nil)))
}

// stonecutterResultClick 处理结果槽点击（取走产物），返回新的光标物品。
// 与合成结果一致：取走 1 次消耗 1 个输入；Shift 尽可能多次。
func (s *Server) stonecutterResultClick(player *session, b stonecutterSlotBacking, working []item.Stack, cursor item.Stack, click protocol.ContainerClick) item.Stack {
	quickMoveOnce := func() bool {
		result := b.state.resultStack()
		if result.IsEmpty() {
			return false
		}
		stack := result
		for _, target := range [][2]int{{2, 29}, {29, 38}} {
			stack = mergeIntoArea(working, stack, target[0], target[1])
			if stack.IsEmpty() {
				break
			}
		}
		if !stack.IsEmpty() {
			return false // 背包放不下
		}
		b.state.consumeInput()
		working[stonecutterInputSlot] = b.state.inputStack()
		return true
	}
	switch click.Mode {
	case protocol.ClickModePickup:
		result := b.state.resultStack()
		if result.IsEmpty() {
			return cursor
		}
		switch {
		case cursor.IsEmpty():
			cursor = result
		case cursor.ItemID == result.ItemID && cursor.Count+result.Count <= cursor.MaxStack():
			cursor.Count += result.Count
		default:
			return cursor
		}
		b.state.consumeInput()
		working[stonecutterInputSlot] = b.state.inputStack()
		return cursor
	case protocol.ClickModeQuickMove:
		for i := 0; i < 64; i++ {
			if !quickMoveOnce() {
				break
			}
		}
		return cursor
	case protocol.ClickModeThrow:
		result := b.state.resultStack()
		if !result.IsEmpty() {
			b.state.consumeInput()
			working[stonecutterInputSlot] = b.state.inputStack()
			s.dropFromContainer(player, result)
		}
		return cursor
	}
	return cursor
}
