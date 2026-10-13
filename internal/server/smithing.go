package server

import (
	"sync"

	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
)

// 锻造台：模板（0）+ 基底（1）+ 添加物（2）+ 结果（3）+ 玩家物品栏（4–39）。
// 结果由 registry.MatchSmithing 实时计算；取走产物消耗三个输入各 1 个。
//
// 与切石机一致，输入的三个槽位是窗口临时状态，关闭窗口时归还背包。

// smithingState 是一个打开的锻造台窗口状态。
type smithingState struct {
	mu     sync.Mutex
	inputs [3]item.Stack // 模板、基底、添加物
}

// inputStack 返回指定输入槽内容（0–2）。
func (st *smithingState) inputStack(index int) item.Stack {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.inputs[index]
}

// setInput 更新指定输入槽。
func (st *smithingState) setInput(index int, stack item.Stack) {
	st.mu.Lock()
	st.inputs[index] = stack
	st.mu.Unlock()
}

// resultStack 返回当前输入对应的锻造产物（无匹配时为空）。
func (st *smithingState) resultStack() item.Stack {
	st.mu.Lock()
	defer st.mu.Unlock()
	recipe, ok := registry.MatchSmithing(st.inputs[0].ItemID, st.inputs[1].ItemID, st.inputs[2].ItemID)
	if !ok {
		return item.Empty()
	}
	return item.Stack{ItemID: recipe.Result, Count: recipe.ResultCount}
}

// consumeInputs 取走一次产物时消耗三个输入各 1 个。
func (st *smithingState) consumeInputs() {
	st.mu.Lock()
	defer st.mu.Unlock()
	for i := range st.inputs {
		if st.inputs[i].IsEmpty() {
			continue
		}
		st.inputs[i].Count--
		if st.inputs[i].Count <= 0 {
			st.inputs[i] = item.Empty()
		}
	}
}

// 锻造台窗口的槽位布局。
const (
	smithingTemplateSlot = 0
	smithingBaseSlot     = 1
	smithingAdditionSlot = 2
	smithingResultSlot   = 3
	smithingSlots        = 40 // 4 输入/结果 + 36 物品栏
)

// smithingSlotBacking 是锻造台窗口的槽位访问层。
type smithingSlotBacking struct {
	player *session
	state  *smithingState
}

// SlotCount 返回窗口槽位总数。
func (b smithingSlotBacking) SlotCount() int { return smithingSlots }

// Get 返回槽位内容（结果槽实时计算）。
func (b smithingSlotBacking) Get(index int) item.Stack {
	switch {
	case index >= 0 && index <= 2:
		return b.state.inputStack(index)
	case index == smithingResultSlot:
		return b.state.resultStack()
	default:
		return b.player.inventory.Get(containerPlayerInvSlot(index - 4))
	}
}

// Set 设置槽位内容（结果槽忽略）。
func (b smithingSlotBacking) Set(index int, stack item.Stack) {
	switch {
	case index >= 0 && index <= 2:
		b.state.setInput(index, stack)
	case index == smithingResultSlot:
	default:
		b.player.inventory.Set(containerPlayerInvSlot(index-4), stack)
	}
}

// Supported 报告槽位是否可交互（结果槽只取不放）。
func (b smithingSlotBacking) Supported(index int) bool {
	return index != smithingResultSlot
}

// openSmithing 打开锻造台窗口（右键锻造台方块）。
// 由会话 goroutine 调用。
func (s *Server) openSmithing(player *session) {
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
	if player.getGrindstone() != nil {
		s.closeGrindstone(player, false)
	}
	if player.getEnchanting() != nil {
		s.closeEnchanting(player, false)
	}
	menuType, ok := registry.StaticEntryID("minecraft:menu", "minecraft:smithing")
	if !ok {
		return
	}
	player.setSmithing(&smithingState{})
	player.windowID = player.nextWindowID()
	player.windowState = 0
	if err := player.writePacket(protocol.EncodeOpenScreen(player.windowID, menuType, "Smithing")); err != nil {
		return
	}
	s.sendSmithingContent(player)
}

// sendSmithingContent 把锻造台窗口的全部槽位内容发送给玩家。
func (s *Server) sendSmithingContent(player *session) {
	state := player.getSmithing()
	if state == nil {
		return
	}
	backing := smithingSlotBacking{player: player, state: state}
	slots := make([][]byte, 0, smithingSlots)
	for i := 0; i < smithingSlots; i++ {
		slots = append(slots, backing.Get(i).AppendSlot(nil))
	}
	_ = player.writePacket(protocol.EncodeContainerSetContent(
		player.windowID, player.windowState, slots, player.getCursor().AppendSlot(nil)))
}

// closeSmithing 关闭锻造台窗口：三个输入槽归还背包（放不下则掉落）。
// sendClose 表示是否向客户端发送 Close Container。
// 由会话 goroutine 调用。
func (s *Server) closeSmithing(player *session, sendClose bool) {
	state := player.getSmithing()
	if state == nil {
		return
	}
	player.setSmithing(nil)
	windowID := player.windowID
	s.releaseCursor(player)
	for i := 0; i < 3; i++ {
		stack := state.inputStack(i)
		if stack.IsEmpty() {
			continue
		}
		remaining, changed := player.inventory.Add(stack)
		for _, slot := range changed {
			player.tryWrite(protocol.EncodeSetPlayerInventory(int32(slot), player.inventory.Get(slot).AppendSlot(nil)))
		}
		if remaining > 0 {
			left := stack
			left.Count = remaining
			s.dropFromContainer(player, left)
		}
	}
	if sendClose {
		player.tryWrite(protocol.EncodeContainerClose(windowID))
	}
}

// smithingResultClick 处理结果槽点击（取走锻造产物），返回新的光标物品。
func (s *Server) smithingResultClick(player *session, b smithingSlotBacking, working []item.Stack, cursor item.Stack, click protocol.ContainerClick) item.Stack {
	quickMoveOnce := func() bool {
		result := b.state.resultStack()
		if result.IsEmpty() {
			return false
		}
		stack := result
		for _, target := range [][2]int{{4, 31}, {31, 40}} {
			stack = mergeIntoArea(b, working, stack, target[0], target[1])
			if stack.IsEmpty() {
				break
			}
		}
		if !stack.IsEmpty() {
			return false
		}
		b.state.consumeInputs()
		for i := 0; i < 3; i++ {
			working[i] = b.state.inputStack(i)
		}
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
		b.state.consumeInputs()
		for i := 0; i < 3; i++ {
			working[i] = b.state.inputStack(i)
		}
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
			b.state.consumeInputs()
			for i := 0; i < 3; i++ {
				working[i] = b.state.inputStack(i)
			}
			s.dropFromContainer(player, result)
		}
		return cursor
	}
	return cursor
}
