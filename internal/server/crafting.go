//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"sync"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 合成系统：玩家物品栏 2×2 合成格与工作台 3×3 合成格。
//
// 结果槽（槽位 0）始终由合成格内容实时计算（registry.MatchCrafting），
// 不参与普通取放；取走产物时消耗 1 个每个占用格的原料。支持：
//   - 单击取走一次合成（左/右键语义一致：整组产物合并到光标）；
//   - Shift 取走（尽可能多次合成并放入背包，放不下为止）；
//   - Q 丢弃（合成一次并沿视线抛出）。
//
// 合成格内容保存在会话状态（invCraft / openCraft）：窗口 0 的 2×2 格在
// 物品栏窗口生命周期内保留；工作台 3×3 格在窗口关闭时归还到背包（放不下掉落）。

// craftingState 是一个合成窗口的合成格内容（结果槽由格子计算，不存储）。
// mu 保护 grid：会话 goroutine 写入、测试/其他 goroutine 读取快照。
type craftingState struct {
	mu    sync.Mutex
	grid  []item.Stack
	width int
	// dim/pos 是工作台方块的位置（窗口 0 的 2×2 合成格没有方块，hasPos=false）。
	dim    world.Dimension
	pos    [3]int
	hasPos bool
}

// gridSnapshot 返回合成格内容的副本（线程安全）。
func (s *craftingState) gridSnapshot() []item.Stack {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]item.Stack, len(s.grid))
	copy(out, s.grid)
	return out
}

// newCraftingState 创建 width×width 的合成格状态。
func newCraftingState(width int) *craftingState {
	return &craftingState{grid: make([]item.Stack, width*width), width: width}
}

// craftSlotBacking 是合成窗口的槽位访问层。两种布局（均 46 槽）：
//
//	玩家物品栏窗口 0：0 结果、1–4 合成格、5–8 盔甲、9–35 主背包、36–44 快捷栏、45 副手
//	工作台窗口：        0 结果、1–9 合成格、10–36 主背包、37–45 快捷栏
type craftSlotBacking struct {
	player     *session
	state      *craftingState
	windowZero bool
}

// gridCount 返回合成格数量。
func (b craftSlotBacking) gridCount() int {
	return len(b.state.grid)
}

// playerStart 返回窗口内玩家槽位区的起点。
func (b craftSlotBacking) playerStart() int {
	return 1 + b.gridCount()
}

// SlotCount 返回窗口槽位总数。
func (b craftSlotBacking) SlotCount() int {
	if b.windowZero {
		return 1 + b.gridCount() + 41 // 合成格 + 盔甲 4 + 主背包 27 + 快捷栏 9 + 副手 1
	}
	return 1 + b.gridCount() + 36
}

// invSlot 把窗口槽位映射到物品栏槽位（非玩家槽位返回 false）。
func (b craftSlotBacking) invSlot(index int) (int, bool) {
	if b.windowZero {
		return playerInvForMenuSlot(index)
	}
	switch {
	case index >= 10 && index <= 36:
		return index - 1, true
	case index >= 37 && index <= 45:
		return index - 37, true
	}
	return 0, false
}

// Get 返回槽位内容；结果槽返回按合成格计算的产物。
func (b craftSlotBacking) Get(index int) item.Stack {
	switch {
	case index == 0:
		return b.resultOf(nil)
	case index <= b.gridCount():
		b.state.mu.Lock()
		stack := b.state.grid[index-1]
		b.state.mu.Unlock()
		return stack
	default:
		if slot, ok := b.invSlot(index); ok {
			return b.player.inventory.Get(slot)
		}
		return item.Empty()
	}
}

// Set 设置槽位内容；结果槽忽略（由合成格计算）。
func (b craftSlotBacking) Set(index int, stack item.Stack) {
	switch {
	case index == 0:
		return
	case index <= b.gridCount():
		b.state.mu.Lock()
		b.state.grid[index-1] = stack
		b.state.mu.Unlock()
	default:
		if slot, ok := b.invSlot(index); ok {
			b.player.inventory.Set(slot, stack)
		}
	}
}

// Supported 报告槽位是否可交互（结果槽只取不放，由专用路径处理）。
func (b craftSlotBacking) Supported(index int) bool {
	return index != 0
}

// windowZeroEquipSlots 是玩家物品栏窗口（window 0）的盔甲槽：菜单槽位 → 装备槽名
// （原版 InventoryMenu：5..8 头/胸/腿/脚；45 副手）。
var windowZeroEquipSlots = map[int]string{5: "head", 6: "chest", 7: "legs", 8: "feet"}

// equipSlotMenuSlot 是装备槽名 → 玩家物品栏窗口中的盔甲槽位
// （原版 InventoryMenu 的 8 - EquipmentSlot.getIndex()）。
var equipSlotMenuSlot = map[string]int{"head": 5, "chest": 6, "legs": 7, "feet": 8}

// CanPlace 实现原版各槽位的 Slot.mayPlace：玩家物品栏窗口的盔甲槽只接受
// 对应部位的装备（原版 ArmorSlot：物品 mincraft:equippable 组件的 slot 必须匹配），
// 结果槽不可放入；副手与合成格不限（原版为普通 Slot）。
func (b craftSlotBacking) CanPlace(slot int, stack item.Stack) bool {
	if slot == 0 {
		return false // 原版 ResultSlot.mayPlace 恒为 false
	}
	if !b.windowZero {
		return true
	}
	if equip, ok := windowZeroEquipSlots[slot]; ok {
		return registry.PlayerEquippableSlot(stack.ItemID) == equip
	}
	return true
}

// CanPickup 实现原版 ArmorSlot.mayPickup：生存/冒险模式下带绑定诅咒的盔甲
// 不能从装备槽取出（创造模式可以）。
func (b craftSlotBacking) CanPickup(slot int, player *session) bool {
	if !b.windowZero || player.gameModeID() == uint8(config.GameModeCreative) {
		return true
	}
	if _, ok := windowZeroEquipSlots[slot]; !ok {
		return true
	}
	return !hasBindingCurse(b.Get(slot))
}

// SlotLimit 实现原版 Slot.getMaxStackSize：玩家物品栏窗口的盔甲槽为 1
// （原版 ArmorSlot.getMaxStackSize 恒为 1；头颅/南瓜等可堆叠装备因此只能放 1 个）。
func (b craftSlotBacking) SlotLimit(slot int) int32 {
	if b.windowZero {
		if _, ok := windowZeroEquipSlots[slot]; ok {
			return 1
		}
	}
	return 0
}

// equipQuickMoveTarget 返回 Shift 点击的“自动装备”目标槽位（原版 InventoryMenu
// quickMoveStack 的装备分支）：物品可由玩家装备到某个空装备槽时返回该槽位；
// 返回 0 表示没有可用目标。副手槽（45）需要单独判断空位。
func equipQuickMoveTarget(working []item.Stack, stack item.Stack) int {
	equip := registry.PlayerEquippableSlot(stack.ItemID)
	if equip == "" {
		return 0
	}
	if target, ok := equipSlotMenuSlot[equip]; ok && working[target].IsEmpty() {
		return target
	}
	if equip == "offhand" && working[45].IsEmpty() {
		return 45
	}
	return 0
}

// resultOf 计算当前合成结果；working 非空时按 working 副本计算。
func (b craftSlotBacking) resultOf(working []item.Stack) item.Stack {
	ids := make([]int32, b.gridCount())
	grid := b.state.gridSnapshot()
	for i := range ids {
		stack := grid[i]
		if working != nil {
			stack = working[1+i]
		}
		if stack.IsEmpty() {
			ids[i] = 0
			continue
		}
		ids[i] = stack.ItemID
	}
	result, count, ok := registry.MatchCrafting(ids, int32(b.state.width), int32(b.state.width))
	if !ok {
		return item.Empty()
	}
	return item.Stack{ItemID: result, Count: count}
}

// consumeGrid 消耗一轮原料（每个占用格 −1）。
func (b craftSlotBacking) consumeGrid(working []item.Stack) {
	b.state.mu.Lock()
	defer b.state.mu.Unlock()
	for i := 0; i < b.gridCount(); i++ {
		stack := working[1+i]
		if stack.IsEmpty() {
			continue
		}
		stack.Count--
		if stack.Count <= 0 {
			stack = item.Empty()
		}
		working[1+i] = stack
	}
}

// invRange 返回玩家槽位区的起止（结果槽快速移动用；原版 move(9,45,true)
// 等反向填充）。
func (b craftSlotBacking) invRange() (int, int) {
	if b.windowZero {
		return 9, 45
	}
	return 10, 46
}

// invRanges 返回玩家槽位区的目标区间（Shift 移动用；按尝试顺序）。
func (b craftSlotBacking) invRanges() [][2]int {
	if b.windowZero {
		return [][2]int{{9, 36}, {36, 45}}
	}
	return [][2]int{{10, 37}, {37, 46}}
}

// fitsInArea 报告堆栈能否完整放入 [start, end) 区间（有空槽或同类有空间）。
func fitsInArea(working []item.Stack, start, end int, stack item.Stack) bool {
	limit := stack.MaxStack()
	for i := start; i < end; i++ {
		current := working[i]
		if current.IsEmpty() {
			return true
		}
		if current.SameKind(stack) && current.Count+stack.Count <= limit {
			return true
		}
	}
	return false
}

// craftResultClick 处理结果槽点击（取走合成产物），返回新的光标。
func (s *Server) craftResultClick(player *session, b craftSlotBacking, working []item.Stack, cursor item.Stack, click protocol.ContainerClick) item.Stack {
	switch click.Mode {
	case protocol.ClickModePickup:
		result := b.resultOf(working)
		if result.IsEmpty() {
			return cursor
		}
		switch {
		case cursor.IsEmpty():
			b.consumeGrid(working)
			return result
		case cursor.ItemID == result.ItemID && cursor.Count+result.Count <= cursor.MaxStack():
			b.consumeGrid(working)
			cursor.Count += result.Count
			return cursor
		}
		return cursor
	case protocol.ClickModeQuickMove:
		// Shift 取走：尽可能多次合成并放入背包。原版：结果槽 →
		// moveItemStackTo(9, 45, true)/(10, 46, true)——反向填充（先快捷栏末尾）；
		// 放不下则停止。
		start, end := b.invRange()
		for i := 0; i < 64; i++ {
			result := b.resultOf(working)
			if result.IsEmpty() {
				break
			}
			if !fitsInArea(working, start, end, result) {
				break
			}
			quickMoveArea(b, working, result, start, end, true)
			b.consumeGrid(working)
		}
		return cursor
	case protocol.ClickModeThrow:
		result := b.resultOf(working)
		if !result.IsEmpty() {
			b.consumeGrid(working)
			s.dropFromContainer(player, result)
		}
		return cursor
	}
	return cursor
}

// openCraftingTable 打开工作台 3×3 合成窗口（右键工作台方块）。
// 由会话 goroutine 调用。
func (s *Server) openCraftingTable(player *session, x, y, z int) {
	// 关闭上一个窗口（原版同一时间只允许一个活动窗口）。
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
	menuType, ok := registry.StaticEntryID("minecraft:menu", "minecraft:crafting")
	if !ok {
		return
	}
	craft := newCraftingState(3)
	// 记录工作台位置：窗口有效性需要校验“仍是工作台且在交互距离内”
	// （原版 CraftingMenu.stillValid）。
	craft.dim, craft.pos, craft.hasPos = player.dimensionID(), [3]int{x, y, z}, true
	player.setOpenCraft(craft)
	player.windowID = player.nextWindowID()
	player.windowState = 0

	if err := player.writePacket(protocol.EncodeOpenScreen(player.windowID, menuType, "Crafting")); err != nil {
		return
	}
	s.sendCraftContent(player, craftSlotBacking{player: player, state: player.getOpenCraft()})
}

// sendCraftContent 把合成窗口的全部槽位内容发送给玩家。
func (s *Server) sendCraftContent(player *session, b craftSlotBacking) {
	slots := make([][]byte, 0, b.SlotCount())
	for i := 0; i < b.SlotCount(); i++ {
		slots = append(slots, b.Get(i).AppendSlot(nil))
	}
	_ = player.writePacket(protocol.EncodeContainerSetContent(
		player.windowID, player.windowState, slots, player.getCursor().AppendSlot(nil)))
}

// closeCrafting 关闭工作台合成窗口：合成格内容归还到背包（放不下则掉落）。
// sendClose 表示是否向客户端发送 Close Container（客户端主动关闭时为 false）。
// 由会话 goroutine 调用。
func (s *Server) closeCrafting(player *session, sendClose bool) {
	state := player.getOpenCraft()
	if state == nil {
		return
	}
	player.setOpenCraft(nil)
	windowID := player.windowID
	s.releaseCursor(player)
	s.returnCraftGrid(player, state)
	if sendClose {
		player.tryWrite(protocol.EncodeContainerClose(windowID))
	}
}

// returnInventoryCraft 把窗口 0（玩家物品栏）2×2 合成格的内容归还背包。
// 会话结束时调用；窗口 0 生命周期内合成格随物品栏保留（与原版一致）。
func (s *Server) returnInventoryCraft(player *session) {
	state := player.clearInvCraft()
	if state == nil {
		return
	}
	s.returnCraftGrid(player, state)
}

// returnCraftGrid 把合成格内容归还到背包，放不下的部分掉落。
func (s *Server) returnCraftGrid(player *session, state *craftingState) {
	state.mu.Lock()
	defer state.mu.Unlock()
	for i := range state.grid {
		stack := state.grid[i]
		if stack.IsEmpty() {
			continue
		}
		remaining, _ := player.inventory.Add(stack)
		if remaining > 0 {
			left := stack
			left.Count = remaining
			s.dropFromContainer(player, left)
		}
		state.grid[i] = item.Empty()
	}
}
