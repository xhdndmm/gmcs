package server

import (
	"log/slog"
	"math"
	"sync"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 容器交互：箱子、陷阱箱、木桶、发射器、投掷器、漏斗与潜影盒的槽位内容，
// 以及窗口内点击（取放、Shift 快速移动、数字键交换、丢弃、拖拽、双击收集）。
//
// 权威状态：容器内容保存在区块的方块实体中（随区块持久化）；打开期间由
// 服务器侧的 containerState 持有并在每次修改后写回区块，因此崩溃或重启
// 不会丢失已确认的物品移动。
//
// 限制（见 docs/TODO.md）：
//   - 熔炉/高炉/烟熏炉（熔炼）、酿造台、附魔台等“处理型”容器尚未实现：
//     它们需要烹饪/配方子系统，不属于存储容器；
//   - 末影箱内容按玩家存储（原版为每玩家独立），暂未实现；
//   - 玩家物品栏窗口（window 0）的 2×2 合成格与合成结果不参与点击处理
//     （无合成系统），对其余槽位的操作已完整支持。

// containerDef 描述一种容器方块对应的菜单与方块实体参数。
type containerDef struct {
	// BlockEntityTypeID 是方块实体类型 ID（minecraft:block_entity_type）。
	BlockEntityTypeID int32
	// MenuType 是打开窗口时的菜单类型 ID（minecraft:menu）。
	MenuType int32
	// Slots 是容器槽位数。
	Slots int
	// Title 是窗口标题（纯文本；原版使用翻译键，此处直接显示名称）。
	Title string
}

// containerState 是一个已打开的容器内容；服务器侧权威状态。
type containerState struct {
	mu    sync.Mutex
	pos   [3]int
	def   *containerDef
	slots []item.Stack
	// viewers 是当前打开该容器的玩家会话。
	viewers map[*session]struct{}
	// closed 表示容器方块已被破坏（窗口数据不再有效）。
	closed bool
}

// connContainer 返回会话当前打开的容器（由会话 goroutine 读写）。
func (s *session) connContainer() *containerState {
	return s.openContainer
}

// findBlockEntityType 查询方块实体类型与菜单类型 ID。
func resolveContainerDef(blockName, blockEntityName, menuName, title string, slots int) (*containerDef, bool) {
	typeID, ok := registry.StaticEntryID("minecraft:block_entity_type", blockEntityName)
	if !ok {
		return nil, false
	}
	menuType, ok := registry.StaticEntryID("minecraft:menu", menuName)
	if !ok {
		return nil, false
	}
	if _, ok := registry.BlockStateIDs[blockName]; !ok {
		return nil, false
	}
	return &containerDef{BlockEntityTypeID: typeID, MenuType: menuType, Slots: slots, Title: title}, true
}

// resolveContainerDefs 构建“方块名 → 容器定义”表；缺失注册表数据时跳过该
// 容器类型（宁可不支持，也不发送错误的协议数据）。
func (s *Server) resolveContainerDefs() {
	defs := make(map[string]*containerDef)
	add := func(blockName, blockEntityName, menuName, title string, slots int) {
		def, ok := resolveContainerDef(blockName, blockEntityName, menuName, title, slots)
		if !ok {
			slog.Warn("容器类型不可用：注册表数据缺失", "block", blockName)
			return
		}
		defs[blockName] = def
	}
	add("minecraft:chest", "minecraft:chest", "minecraft:generic_9x3", "Chest", 27)
	add("minecraft:trapped_chest", "minecraft:trapped_chest", "minecraft:generic_9x3", "Trapped Chest", 27)
	add("minecraft:barrel", "minecraft:barrel", "minecraft:generic_9x3", "Barrel", 27)
	add("minecraft:dispenser", "minecraft:dispenser", "minecraft:generic_3x3", "Dispenser", 9)
	add("minecraft:dropper", "minecraft:dropper", "minecraft:generic_3x3", "Dropper", 9)
	add("minecraft:hopper", "minecraft:hopper", "minecraft:hopper", "Hopper", 5)
	for _, name := range []string{
		"minecraft:shulker_box",
		"minecraft:white_shulker_box", "minecraft:orange_shulker_box", "minecraft:magenta_shulker_box",
		"minecraft:light_blue_shulker_box", "minecraft:yellow_shulker_box", "minecraft:lime_shulker_box",
		"minecraft:pink_shulker_box", "minecraft:gray_shulker_box", "minecraft:light_gray_shulker_box",
		"minecraft:cyan_shulker_box", "minecraft:purple_shulker_box", "minecraft:blue_shulker_box",
		"minecraft:brown_shulker_box", "minecraft:green_shulker_box", "minecraft:red_shulker_box",
		"minecraft:black_shulker_box",
	} {
		add(name, "minecraft:shulker_box", "minecraft:shulker_box", "Shulker Box", 27)
	}
	s.containerDefs = defs
}

// containerForState 返回方块状态对应的容器定义（非容器方块返回 nil）。
func (s *Server) containerForState(state uint16) (*containerDef, string, bool) {
	name, ok := s.blockNames[state]
	if !ok {
		return nil, "", false
	}
	def, ok := s.containerDefs[name]
	return def, name, ok
}

// openContainer 打开 (x, y, z) 处的容器：加载内容、登记视图、发送窗口数据。
// 由会话 goroutine 调用。
func (s *Server) openContainer(player *session, x, y, z int, def *containerDef, blockName string) {
	// 关闭上一个窗口（原版同一时间只允许一个容器窗口）。
	if player.openContainer != nil {
		s.closeContainer(player, false)
	}

	state, err := s.world.BlockEntityAt(x, y, z)
	if err != nil {
		slog.Debug("无法读取容器内容", "name", player.name, "error", err)
		return
	}
	items := make([]item.Stack, def.Slots)
	for i := range items {
		if i < len(state.Items) {
			slot := state.Items[i]
			if !slot.IsEmpty() {
				items[i] = item.Stack{ItemID: slot.ItemID, Count: slot.Count}
			}
		}
	}

	container := s.registerViewer(x, y, z, def, items, player)
	player.openContainer = container
	player.windowID = player.nextWindowID()
	player.windowState = 0
	player.cursor = item.Empty()

	if err := player.writePacket(protocol.EncodeOpenScreen(player.windowID, def.MenuType, def.Title)); err != nil {
		return
	}
	s.sendContainerContent(player, container)
	// 开合动画广播给附近所有玩家（包括打开者本人）。
	s.broadcastContainerAction(container, true, nil)
}

// registerViewer 把玩家加入容器的观察者集合；容器已在打开状态时复用同一状态。
func (s *Server) registerViewer(x, y, z int, def *containerDef, items []item.Stack, player *session) *containerState {
	key := [3]int{x, y, z}
	s.containerMu.Lock()
	container := s.containers[key]
	if container == nil {
		container = &containerState{pos: key, def: def, slots: items, viewers: make(map[*session]struct{})}
		s.containers[key] = container
	}
	s.containerMu.Unlock()

	container.mu.Lock()
	container.viewers[player] = struct{}{}
	container.mu.Unlock()
	return container
}

// closeContainer 关闭玩家当前的容器窗口：退出观察者集合，最后一个观察者
// 离开时把内容写回区块并移除服务器侧状态。sendClose 表示是否向客户端发送
// Close Container（客户端主动关闭时为 false）。
// 由会话 goroutine 调用。
func (s *Server) closeContainer(player *session, sendClose bool) {
	container := player.openContainer
	if container == nil {
		return
	}
	player.openContainer = nil
	player.cursor = item.Empty()
	player.drag = nil
	windowID := player.windowID

	container.mu.Lock()
	delete(container.viewers, player)
	empty := len(container.viewers) == 0
	closed := container.closed
	container.mu.Unlock()

	if !closed {
		// 容器仍有效：把内容写回区块。
		s.flushContainer(container)
	}
	if empty {
		key := container.pos
		s.containerMu.Lock()
		// 仅当映射仍指向同一状态时删除（避免与重新打开竞态）。
		if s.containers[key] == container {
			delete(s.containers, key)
		}
		s.containerMu.Unlock()
	}
	if sendClose {
		player.tryWrite(protocol.EncodeContainerClose(windowID))
	}
	if !closed {
		s.broadcastContainerAction(container, false, nil)
	}
}

// destroyContainer 处理容器方块被破坏：关闭所有观察窗口并把内容掉落到世界。
// breaker 是破坏方块的会话（它的窗口在同一 goroutine 内同步关闭）。
// 其他会话的窗口通过关闭包通知，其会话读循环会在下一次点击时清理状态。
func (s *Server) destroyContainer(x, y, z int, breaker *session, def *containerDef) {
	key := [3]int{x, y, z}
	s.containerMu.Lock()
	container := s.containers[key]
	if s.containers[key] == container {
		delete(s.containers, key)
	}
	s.containerMu.Unlock()

	if container != nil {
		container.mu.Lock()
		container.closed = true
		// 内容已掉落到世界：清空服务器侧副本，关闭窗口时不会写回物品。
		for i := range container.slots {
			container.slots[i] = item.Empty()
		}
		viewers := make([]*session, 0, len(container.viewers))
		for viewer := range container.viewers {
			viewers = append(viewers, viewer)
		}
		container.mu.Unlock()
		for _, viewer := range viewers {
			if viewer == breaker {
				continue
			}
			viewer.tryWrite(protocol.EncodeContainerClose(viewer.windowID))
		}
		if breaker.openContainer == container {
			s.closeContainer(breaker, true)
		}
		s.broadcastContainerAction(container, false, nil)
	}
	s.dropContainerContents(x, y, z, def)
}

// sessionForReadLoop 占位（保留以防误用）。
func (s *Server) sessionForReadLoop() *session { return nil }

// broadcastContainerAction 向附近玩家广播箱子开合动画（Block Action）。
// 原版中参数 B 是观察者数量，这里取当前窗口观察者数。
func (s *Server) broadcastContainerAction(container *containerState, open bool, except *session) {
	blockName, ok := s.blockNameForContainer(container)
	if !ok {
		return
	}
	blockID, ok := registry.StaticEntryID("minecraft:block", blockName)
	if !ok {
		return
	}
	container.mu.Lock()
	viewers := len(container.viewers)
	container.mu.Unlock()

	var paramA uint8
	if open {
		paramA = 1
	}
	paramB := uint8(min(viewers, 255))
	packet := protocol.EncodeBlockAction(container.pos[0], container.pos[1], container.pos[2], paramA, paramB, blockID)
	// 只发给附近玩家（距离与方块更新一致）。
	cx := float64(container.pos[0]) + 0.5
	cz := float64(container.pos[2]) + 0.5
	for _, other := range s.playerSnapshot() {
		if other == except || !other.isJoined() {
			continue
		}
		px, _, pz, _, _ := other.playerPosition()
		if math.Hypot(px-cx, pz-cz) > mobMoveBroadcastRange {
			continue
		}
		other.tryWrite(packet)
	}
}

// blockNameForContainer 返回容器方块的名字（用于 Block Action 的方块 ID）。
func (s *Server) blockNameForContainer(container *containerState) (string, bool) {
	for name, def := range s.containerDefs {
		if def == container.def {
			return name, true
		}
	}
	return "", false
}

// sendContainerContent 把容器窗口的全部槽位内容发送给玩家（打开时调用）。
func (s *Server) sendContainerContent(player *session, container *containerState) {
	container.mu.Lock()
	containerSlots := len(container.slots)
	slots := make([][]byte, 0, containerSlots+36)
	for _, stack := range container.slots {
		slots = append(slots, stack.AppendSlot(nil))
	}
	container.mu.Unlock()
	// 玩家物品栏部分（主背包 + 快捷栏）。
	for index := 0; index < 36; index++ {
		stack := player.inventory.Get(containerPlayerInvSlot(index))
		slots = append(slots, stack.AppendSlot(nil))
	}
	_ = player.writePacket(protocol.EncodeContainerSetContent(
		player.windowID, player.windowState, slots, player.cursor.AppendSlot(nil)))
}

// containerPlayerInvSlot 把容器菜单中玩家部分的槽位（0–35）映射到物品栏槽位：
// 0–26 → 主背包（9–35），27–35 → 快捷栏（0–8）。
func containerPlayerInvSlot(index int) int {
	if index < 27 {
		return index + item.SlotMainStart
	}
	return index - 27
}

// flushContainer 把容器内容写回区块的方块实体并标记待保存。
func (s *Server) flushContainer(container *containerState) {
	container.mu.Lock()
	items := make([]world.ContainerItem, len(container.slots))
	for i, stack := range container.slots {
		items[i] = world.ContainerItem{ItemID: stack.ItemID, Count: stack.Count}
	}
	pos := container.pos
	typeID := container.def.BlockEntityTypeID
	container.mu.Unlock()

	if err := s.world.SetBlockEntity(pos[0], pos[1], pos[2], world.BlockEntity{
		TypeID: typeID,
		Items:  items,
	}); err != nil {
		slog.Error("保存容器内容失败", "x", pos[0], "y", pos[1], "z", pos[2], "error", err)
	}
}

// dropContainerContents 把容器内容掉落到世界中并清空方块实体
// （方块被破坏时调用）。由会话 goroutine 调用。
func (s *Server) dropContainerContents(x, y, z int, def *containerDef) {
	state, err := s.world.BlockEntityAt(x, y, z)
	if err != nil || len(state.Items) == 0 {
		return
	}
	for _, slot := range state.Items {
		if slot.IsEmpty() {
			continue
		}
		stack := item.Stack{ItemID: slot.ItemID, Count: slot.Count}
		vx := (float64(s.nextRandom()%1000)/1000 - 0.5) * 0.2
		vz := (float64(s.nextRandom()%1000)/1000 - 0.5) * 0.2
		s.spawnItem(stack, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, vx, 0.2, vz, itemPickupDelayTicks)
	}
	if err := s.world.RemoveBlockEntity(x, y, z); err != nil {
		slog.Error("清除容器内容失败", "x", x, "y", y, "z", z, "error", err)
	}
}

// containerSlotBacking 是容器窗口的槽位访问层。
type containerSlotBacking struct {
	server    *Server
	player    *session
	container *containerState
}

// slotBacking 是窗口槽位的通用访问接口（用于统一处理窗口 0 与容器窗口）。
type slotBacking interface {
	SlotCount() int
	Get(index int) item.Stack
	Set(index int, stack item.Stack)
	// Supported 报告槽位是否可交互（窗口 0 的合成格不可）。
	Supported(index int) bool
}

// SlotCount 返回窗口槽位总数（容器 + 玩家背包 + 快捷栏）。
func (b containerSlotBacking) SlotCount() int {
	return len(b.container.slots) + 36
}

// Get 返回槽位内容。
func (b containerSlotBacking) Get(index int) item.Stack {
	if index < len(b.container.slots) {
		b.container.mu.Lock()
		defer b.container.mu.Unlock()
		return b.container.slots[index]
	}
	return b.player.inventory.Get(containerPlayerInvSlot(index - len(b.container.slots)))
}

// Set 设置槽位内容；容器部分立即写回区块。
func (b containerSlotBacking) Set(index int, stack item.Stack) {
	if index < len(b.container.slots) {
		b.container.mu.Lock()
		changed := b.container.slots[index] != stack
		b.container.slots[index] = stack
		b.container.mu.Unlock()
		if changed {
			b.server.flushContainer(b.container)
			// 其他观察者也需要看到本次变更。
			b.server.broadcastContainerSlot(b.container, index, stack, b.player)
		}
		return
	}
	b.player.inventory.Set(containerPlayerInvSlot(index-len(b.container.slots)), stack)
}

// Supported 报告槽位是否可交互。
func (b containerSlotBacking) Supported(int) bool { return true }

// broadcastContainerSlot 把单个槽位变更发送给容器的其他观察者。
func (s *Server) broadcastContainerSlot(container *containerState, index int, stack item.Stack, except *session) {
	container.mu.Lock()
	viewers := make([]*session, 0, len(container.viewers))
	for viewer := range container.viewers {
		if viewer != except {
			viewers = append(viewers, viewer)
		}
	}
	container.mu.Unlock()
	for _, viewer := range viewers {
		viewer.tryWrite(protocol.EncodeSetContainerSlot(viewer.windowID, viewer.windowState, int16(index), stack.AppendSlot(nil)))
	}
}

// 玩家物品栏窗口（window 0）的槽位布局（与原版 InventoryMenu 一致）：
//
//	0 合成结果、1–4 合成格、5 头盔、6 胸甲、7 护腿、8 靴子、
//	9–35 主背包、36–44 快捷栏、45 副手。
const playerMenuSlots = 46

// playerInvForMenuSlot 把窗口 0 的槽位映射到物品栏槽位。
func playerInvForMenuSlot(index int) (int, bool) {
	switch {
	case index >= 5 && index <= 8:
		return 44 - index, true // 5→39（头盔）… 8→36（靴子）
	case index >= 9 && index <= 35:
		return index, true
	case index >= 36 && index <= 44:
		return index - 36, true
	case index == 45:
		return item.SlotOffhand, true
	}
	return 0, false
}

// playerSlotBacking 是玩家物品栏窗口（window 0）的槽位访问层。
type playerSlotBacking struct {
	player *session
}

// SlotCount 返回窗口 0 的槽位总数。
func (b playerSlotBacking) SlotCount() int { return playerMenuSlots }

// Get 返回槽位内容；合成格返回空（不支持合成）。
func (b playerSlotBacking) Get(index int) item.Stack {
	if slot, ok := playerInvForMenuSlot(index); ok {
		return b.player.inventory.Get(slot)
	}
	return item.Empty()
}

// Set 设置槽位内容；合成格忽略。
func (b playerSlotBacking) Set(index int, stack item.Stack) {
	if slot, ok := playerInvForMenuSlot(index); ok {
		b.player.inventory.Set(slot, stack)
	}
}

// Supported 报告槽位是否可交互（合成格不可）。
func (b playerSlotBacking) Supported(index int) bool {
	_, ok := playerInvForMenuSlot(index)
	return ok
}

// handleContainerClick 处理窗口点击（Container Click 包）。
// 由会话读循环调用。
func (s *Server) handleContainerClick(player *session, click protocol.ContainerClick) {
	var (
		backing  slotBacking
		windowID int32
	)
	switch {
	case click.WindowID == protocol.PlayerInventoryWindowID:
		backing = playerSlotBacking{player}
		windowID = protocol.PlayerInventoryWindowID
	case player.openContainer != nil && click.WindowID == player.windowID:
		container := player.openContainer
		// 容器方块已被破坏：清理窗口状态（内容已掉落）。
		container.mu.Lock()
		closed := container.closed
		container.mu.Unlock()
		if closed {
			s.closeContainer(player, true)
			return
		}
		// 距离校验：离开容器过远时关闭窗口（与原版一致）。
		px, py, pz, _, _ := player.playerPosition()
		if math.Hypot(px-(float64(container.pos[0])+0.5), pz-(float64(container.pos[2])+0.5)) > containerReach ||
			math.Abs(py-(float64(container.pos[1])+0.5)) > containerReach {
			s.closeContainer(player, true)
			return
		}
		backing = containerSlotBacking{server: s, player: player, container: container}
		windowID = player.windowID
	default:
		return
	}

	before := make([]item.Stack, backing.SlotCount())
	for i := range before {
		before[i] = backing.Get(i)
	}
	working := make([]item.Stack, len(before))
	copy(working, before)
	cursor := player.cursor

	cursor = s.applyContainerClick(player, backing, working, cursor, click)

	player.windowState++
	for i := range working {
		if working[i] != before[i] {
			backing.Set(i, working[i])
			player.tryWrite(protocol.EncodeSetContainerSlot(windowID, player.windowState, int16(i), working[i].AppendSlot(nil)))
		}
	}
	if cursor != player.cursor {
		player.cursor = cursor
		player.tryWrite(protocol.EncodeSetContainerSlot(windowID, player.windowState, -1, cursor.AppendSlot(nil)))
	}
}

// containerReach 是打开容器后允许的最大距离（与原版 8 格一致的近似值）。
const containerReach = 8.0

// applyContainerClick 在 working 副本上执行一次点击，返回新的光标物品。
// 行为对齐原版 AbstractContainerMenu#doClick 的主要模式。
func (s *Server) applyContainerClick(player *session, backing slotBacking, working []item.Stack, cursor item.Stack, click protocol.ContainerClick) item.Stack {
	slot := int(click.Slot)
	if click.Mode != protocol.ClickModeQuickCraft && (slot < 0 || slot >= len(working) || !backing.Supported(slot)) {
		return cursor
	}
	limit := int32(item.StackLimit)
	switch click.Mode {
	case protocol.ClickModePickup:
		if click.MouseButton == 0 {
			return clickPickupLeft(working, slot, cursor, limit)
		}
		return clickPickupRight(working, slot, cursor, limit)
	case protocol.ClickModeQuickMove:
		quickMove(player, backing, working, slot)
		return cursor
	case protocol.ClickModeSwapHotbar:
		hotbar := int(click.MouseButton)
		if hotbar < 0 || hotbar > 8 {
			return cursor
		}
		hotbarSlot := swapHotbarIndex(backing, hotbar)
		if hotbarSlot < 0 || hotbarSlot == slot {
			return cursor
		}
		working[hotbarSlot], working[slot] = working[slot], working[hotbarSlot]
		return cursor
	case protocol.ClickModeClone:
		if player.gameModeID() != uint8(config.GameModeCreative) {
			return cursor
		}
		stack := working[slot]
		if stack.IsEmpty() {
			return cursor
		}
		clone := stack
		clone.Count = limit
		return clone
	case protocol.ClickModeThrow:
		stack := working[slot]
		if stack.IsEmpty() {
			return cursor
		}
		dropped := stack
		if click.MouseButton == 0 {
			dropped.Count = 1
		}
		stack.Count -= dropped.Count
		if stack.Count <= 0 {
			stack = item.Empty()
		}
		working[slot] = stack
		s.dropFromContainer(player, dropped)
		return cursor
	case protocol.ClickModeQuickCraft:
		return s.applyQuickCraft(player, working, cursor, click)
	case protocol.ClickModePickupAll:
		return clickPickupAll(working, cursor, limit)
	}
	return cursor
}

// swapHotbarIndex 把快捷栏下标（0–8）映射为当前窗口中的槽位下标。
func swapHotbarIndex(backing slotBacking, hotbar int) int {
	switch b := backing.(type) {
	case containerSlotBacking:
		return len(b.container.slots) + 27 + hotbar
	case playerSlotBacking:
		return 36 + hotbar
	}
	return -1
}

// clickPickupLeft 处理左键取放（mode 0、button 0）。
func clickPickupLeft(working []item.Stack, slot int, cursor item.Stack, limit int32) item.Stack {
	current := working[slot]
	switch {
	case cursor.IsEmpty():
		working[slot] = item.Empty()
		return current
	case current.IsEmpty():
		working[slot] = cursor
		return item.Empty()
	case current.ItemID == cursor.ItemID:
		space := limit - current.Count
		moved := min(space, cursor.Count)
		current.Count += moved
		cursor.Count -= moved
		working[slot] = current
		if cursor.Count <= 0 {
			return item.Empty()
		}
		return cursor
	default:
		working[slot] = cursor
		return current
	}
}

// clickPickupRight 处理右键取放（mode 0、button 1）：取一半 / 放一个。
func clickPickupRight(working []item.Stack, slot int, cursor item.Stack, limit int32) item.Stack {
	current := working[slot]
	switch {
	case cursor.IsEmpty():
		if current.IsEmpty() {
			return cursor
		}
		half := (current.Count + 1) / 2
		picked := current
		picked.Count = half
		current.Count -= half
		if current.Count <= 0 {
			current = item.Empty()
		}
		working[slot] = current
		return picked
	case current.IsEmpty():
		placed := cursor
		placed.Count = 1
		cursor.Count--
		working[slot] = placed
		if cursor.Count <= 0 {
			return item.Empty()
		}
		return cursor
	case current.ItemID == cursor.ItemID && current.Count < limit:
		current.Count++
		cursor.Count--
		working[slot] = current
		if cursor.Count <= 0 {
			return item.Empty()
		}
		return cursor
	}
	return cursor
}

// clickPickupAll 处理双击收集（mode 6）：把容器中同类物品收集到光标。
func clickPickupAll(working []item.Stack, cursor item.Stack, limit int32) item.Stack {
	if cursor.IsEmpty() {
		return cursor
	}
	for i := range working {
		if cursor.Count >= limit {
			break
		}
		stack := working[i]
		if stack.IsEmpty() || stack.ItemID != cursor.ItemID {
			continue
		}
		moved := min(limit-cursor.Count, stack.Count)
		cursor.Count += moved
		stack.Count -= moved
		if stack.Count <= 0 {
			stack = item.Empty()
		}
		working[i] = stack
	}
	return cursor
}

// dragState 记录一次拖拽分发（mode 5）的中间状态。
type dragState struct {
	button int8
	slots  []int
}

// applyQuickCraft 处理拖拽分发（mode 5）。
// button 0/4/8 = 左键开始/经过/结束，1/5/9 = 右键开始/经过/结束（游戏内编码）。
func (s *Server) applyQuickCraft(player *session, working []item.Stack, cursor item.Stack, click protocol.ContainerClick) item.Stack {
	stage := (click.MouseButton / 4) // 0 开始、1 经过、2 结束
	left := click.MouseButton%4 == 0
	switch stage {
	case 0: // 开始
		player.drag = &dragState{button: click.MouseButton}
		return cursor
	case 1: // 经过
		if player.drag == nil {
			return cursor
		}
		player.drag.slots = append(player.drag.slots, int(click.Slot))
		return cursor
	default: // 结束（slot = -999）
		drag := player.drag
		player.drag = nil
		if drag == nil {
			return cursor
		}
		if left {
			// 左键拖拽：每个经过的槽位放 1 个。
			for _, index := range drag.slots {
				if cursor.IsEmpty() {
					break
				}
				if index < 0 || index >= len(working) {
					continue
				}
				stack := working[index]
				if stack.IsEmpty() {
					placed := cursor
					placed.Count = 1
					working[index] = placed
					cursor.Count--
				} else if stack.ItemID == cursor.ItemID && stack.Count < int32(item.StackLimit) {
					stack.Count++
					working[index] = stack
					cursor.Count--
				}
			}
		} else {
			// 右键拖拽：按经过顺序逐个分配 1 个。
			placed := int32(0)
			for _, index := range drag.slots {
				if placed >= cursor.Count {
					break
				}
				if index < 0 || index >= len(working) {
					continue
				}
				stack := working[index]
				if stack.IsEmpty() {
					one := cursor
					one.Count = 1
					working[index] = one
					placed++
				} else if stack.ItemID == cursor.ItemID && stack.Count < int32(item.StackLimit) {
					stack.Count++
					working[index] = stack
					placed++
				}
			}
			cursor.Count -= placed
		}
		if cursor.Count <= 0 {
			return item.Empty()
		}
		return cursor
	}
}

// quickMove 处理 Shift 点击（mode 1）：把整栈在容器与玩家背包之间移动。
// 目标区间按原版的顺序尝试（如容器 → 主背包 → 快捷栏）。
func quickMove(player *session, backing slotBacking, working []item.Stack, slot int) {
	stack := working[slot]
	if stack.IsEmpty() {
		return
	}
	for _, target := range quickMoveTargets(backing, slot) {
		stack = mergeIntoArea(working, stack, target[0], target[1])
		if stack.IsEmpty() {
			break
		}
	}
	working[slot] = stack
}

// quickMoveTargets 返回 Shift 点击的目标槽位区间（按尝试顺序）。
func quickMoveTargets(backing slotBacking, slot int) [][2]int {
	switch b := backing.(type) {
	case containerSlotBacking:
		containerSlots := len(b.container.slots)
		if slot < containerSlots {
			// 容器 → 主背包 → 快捷栏。
			return [][2]int{
				{containerSlots, containerSlots + 27},
				{containerSlots + 27, containerSlots + 36},
			}
		}
		return [][2]int{{0, containerSlots}}
	case playerSlotBacking:
		switch {
		case slot >= 9 && slot <= 35: // 主背包 → 快捷栏
			return [][2]int{{36, 45}}
		case slot >= 36 && slot <= 44: // 快捷栏 → 主背包
			return [][2]int{{9, 36}}
		case slot >= 5 && slot <= 8: // 盔甲 → 主背包/快捷栏
			return [][2]int{{9, 45}}
		case slot == 45: // 副手 → 主背包
			return [][2]int{{9, 36}}
		}
	}
	return nil
}

// mergeIntoArea 把 stack 合并/放入 [start, end) 区间，返回剩余部分。
func mergeIntoArea(working []item.Stack, stack item.Stack, start, end int) item.Stack {
	limit := int32(item.StackLimit)
	// 先并入同类未满堆叠。
	for i := start; i < end && stack.Count > 0; i++ {
		current := working[i]
		if current.IsEmpty() || current.ItemID != stack.ItemID || current.Count >= limit {
			continue
		}
		moved := min(limit-current.Count, stack.Count)
		current.Count += moved
		stack.Count -= moved
		working[i] = current
	}
	// 再放入空槽位。
	for i := start; i < end && stack.Count > 0; i++ {
		if !working[i].IsEmpty() {
			continue
		}
		placed := stack
		placed.Count = min(limit, stack.Count)
		working[i] = placed
		stack.Count -= placed.Count
	}
	if stack.Count <= 0 {
		return item.Empty()
	}
	return stack
}

// dropFromContainer 把物品从窗口丢到世界中（Q 键；原版会沿视线抛出）。
func (s *Server) dropFromContainer(player *session, stack item.Stack) {
	if stack.IsEmpty() {
		return
	}
	x, y, z, yaw, pitch := player.playerPosition()
	yawRad := float64(yaw) * math.Pi / 180
	pitchRad := float64(pitch) * math.Pi / 180
	dirX := -math.Cos(pitchRad) * math.Sin(yawRad)
	dirY := -math.Sin(pitchRad)
	dirZ := math.Cos(pitchRad) * math.Cos(yawRad)
	const throwSpeed = 0.3
	s.spawnItem(stack, x, y+1.0, z, dirX*throwSpeed, dirY*throwSpeed+0.1, dirZ*throwSpeed, itemPickupDelayTicks)
}
