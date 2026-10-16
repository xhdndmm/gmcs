//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"
	"slices"
	"sync"

	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 砂轮机（对照 1.21.11 反编译的 GrindstoneMenu 逐字节码移植）：
//   - 窗口布局与铁砧一致：输入槽 0/1 + 结果槽 2 + 36 物品栏（共 39 槽）；
//   - 输入槽只接受可损坏物品或带附魔物品（原版 ij$2/ij$3 的 mayPlace）；
//   - createResult（dij.a(dlt,dlt)）：
//     两输入全空或任一输入数量 > 1 → 空结果；
//     只有一侧有物品时：物品必须带附魔，移除全部非诅咒附魔
//     （附魔书清空后变为普通书），累积惩罚重置为与剩余诅咒数对应；
//     两侧都有物品时（dij.b(dlt,dlt)）：要求同物品；可损坏物品合并剩余耐久
//     （+5% 上限奖励、耐久上限取两者较大值），不可损坏物品要求堆叠上限 >= 2
//     且组件完全一致（结果为 2 个）；合并后附魔只保留双方的诅咒；
//   - 取走（ij$4.onTake）：砂轮机使用世界事件 1042，输入槽全部清空，
//     经验球按两输入附魔的最低成本之和计算（ceil(total/2) + 随机(0, ceil/2)）；
//   - 输入槽为窗口临时状态，关闭窗口归还背包。
//
// 已知与原版的差异：原版砂轮机的输入容器/结果容器是菜单内联的 SimpleContainer
// （不持久化），gmcs 同样只在窗口生命周期内保留（本文件状态）。

// 砂轮机窗口的槽位布局与常量。
const (
	grindstoneInputSlot    = 0
	grindstoneAdditionSlot = 1
	grindstoneResultSlot   = 2
	grindstoneSlots        = 39 // 2 输入 + 结果 + 36 物品栏

	// grindstoneUseEvent 是取走结果时的世界事件（原版 LevelEvent.SOUND_GRINDSTONE_USED）。
	grindstoneUseEvent = 1042
	// grindstoneReach 是窗口有效距离（与铁砧/普通容器一致的 8.0 盒近似）。
	grindstoneReach = 8.0
	// grindstoneBlockName 是砂轮机方块名。
	grindstoneBlockName = "minecraft:grindstone"
	// grindstoneTitle 是窗口标题（en_us: container.grindstone_title）。
	grindstoneTitle = "Repair & Disenchant"
	// grindstoneDurabilityBonusPercent 是同物品合并的耐久上限奖励百分比。
	grindstoneDurabilityBonusPercent = 5
)

// plainBookItemID 返回普通书（minecraft:book）的物品 ID。
var plainBookItemID = sync.OnceValue(func() int32 {
	id, _ := registry.ItemID("minecraft:book")
	return id
})

// grindstoneState 是一个打开的砂轮机窗口状态。
type grindstoneState struct {
	mu sync.Mutex
	// dim/pos 是砂轮机方块的位置（窗口有效性校验）。
	dim world.Dimension
	pos [3]int
	// inputs 是输入槽（0、1）；result 是最近一次计算的结果。
	inputs [2]item.Stack
	result item.Stack
}

// inputStack 返回输入槽内容（0/1）。
func (st *grindstoneState) inputStack(index int) item.Stack {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.inputs[index]
}

// setInputs 写入两个输入槽。
func (st *grindstoneState) setInputs(first, second item.Stack) {
	st.mu.Lock()
	st.inputs[0] = first
	st.inputs[1] = second
	st.mu.Unlock()
}

// snapshotInputs 返回两个输入槽的快照。
func (st *grindstoneState) snapshotInputs() [2]item.Stack {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.inputs
}

// currentResult 返回最近一次计算的结果快照。
func (st *grindstoneState) currentResult() item.Stack {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.result
}

// refreshResult 重新计算结果并保存，返回结果。
func (st *grindstoneState) refreshResult() item.Stack {
	first := st.inputStack(grindstoneInputSlot)
	second := st.inputStack(grindstoneAdditionSlot)
	result := grindstoneComputeResult(first, second)
	st.mu.Lock()
	st.result = result
	st.mu.Unlock()
	return result
}

// grindstoneAcceptsStack 报告物品能否放入砂轮机输入槽
// （原版 mayPlace：可损坏物品或带附魔物品）。
func grindstoneAcceptsStack(stack item.Stack) bool {
	if stack.IsEmpty() {
		return false
	}
	if _, damageable := registry.MaxDamage(stack.ItemID); damageable {
		return true
	}
	return len(stack.Enchantments) > 0
}

// grindstoneComputeResult 复刻原版 GrindstoneMenu.computeResult（dij.a(dlt,dlt)）：
// 返回空堆栈表示没有可执行操作。
func grindstoneComputeResult(input, additional item.Stack) item.Stack {
	if input.IsEmpty() && additional.IsEmpty() {
		return item.Empty()
	}
	// 任一输入数量大于 1：无结果（输入槽限制下实际不会出现）。
	if input.Count > 1 || additional.Count > 1 {
		return item.Empty()
	}
	both := !input.IsEmpty() && !additional.IsEmpty()
	if !both {
		// 只有一侧有物品：移除全部非诅咒附魔（无附魔则无结果）。
		only := input
		if only.IsEmpty() {
			only = additional
		}
		if len(only.Enchantments) == 0 {
			return item.Empty()
		}
		return grindstoneRemoveNonCurses(only)
	}
	return grindstoneMergeItems(input, additional)
}

// grindstoneRemoveNonCurses 复刻原版 GrindstoneMenu.removeNonCurses（dij.b(dlt)）：
//   - 移除全部非诅咒附魔（保留诅咒）；
//   - 附魔书在附魔清空后变为普通书（transmuteCopy(Items.BOOK)：新物品保留
//     组件补丁（如自定义名称），本模型的其余组件字段按原版语义保留）；
//   - 累积惩罚重置为「诅咒数」次 min(2n+1, MaxInt32) 递增（无诅咒时为 0，
//     即清除之前的铁砧惩罚）。
func grindstoneRemoveNonCurses(stack item.Stack) item.Stack {
	remaining := make([]item.EnchantmentEntry, 0, len(stack.Enchantments))
	for _, entry := range stack.Enchantments {
		if registry.EnchantmentIsCurse(entry.ID) {
			remaining = append(remaining, entry)
		}
	}
	if len(remaining) == 0 {
		remaining = nil
	}
	clone := stack
	clone.Enchantments = remaining
	if len(remaining) == 0 && registry.HasStoredEnchantments(clone.ItemID) {
		// 附魔书 → 普通书。
		clone.ItemID = plainBookItemID()
	}
	penalty := int32(0)
	for range remaining {
		penalty = increasedRepairCost(penalty)
	}
	clone.RepairCost = penalty
	return clone
}

// grindstoneMergeItems 复刻原版 GrindstoneMenu.mergeItems（dij.b(dlt,dlt)）：
// 合并两个同物品的输入。
func grindstoneMergeItems(input, additional item.Stack) item.Stack {
	if input.ItemID != additional.ItemID {
		return item.Empty()
	}
	inputMaxDamage, inputDamageable := registry.MaxDamage(input.ItemID)
	additionalMaxDamage, _ := registry.MaxDamage(additional.ItemID)
	maxDamage := max(inputMaxDamage, additionalMaxDamage)
	// 合并剩余耐久并附加 5% 上限奖励。
	combined := (inputMaxDamage - input.Damage) + (additionalMaxDamage - additional.Damage) +
		maxDamage*grindstoneDurabilityBonusPercent/100
	count := int32(1)
	if !inputDamageable {
		// 不可损坏物品：要求堆叠上限 >= 2 且组件完全一致（原版
		// ItemStack.matches：同物品、同数量、同组件）。
		if input.MaxStack() < 2 {
			return item.Empty()
		}
		if !input.Equal(additional) {
			return item.Empty()
		}
		count = 2
	}
	result := input
	result.Count = count
	if inputDamageable {
		// 原版把 MAX_DAMAGE 组件显式设为两者较大值（同物品时相同，无额外效果）；
		// 随后按合并耐久设置 damage。
		result.Damage = max(maxDamage-combined, 0)
	}
	// 把添加物的附魔并入结果（非诅咒写入后会被 removeNonCurses 清掉；
	// 净效果是双方的诅咒取并集，结果已有的同名诅咒保留原等级）。
	result.Enchantments = grindstoneMergeEnchantments(result.Enchantments, additional.Enchantments)
	return grindstoneRemoveNonCurses(result)
}

// grindstoneMergeEnchantments 复刻原版合并路径的附魔并入（dij.c 的 lambda）：
// 添加物的附魔合并进结果；诅咒仅在结果缺少时补上（已有的保留原等级）。
func grindstoneMergeEnchantments(base, extra []item.EnchantmentEntry) []item.EnchantmentEntry {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	merged := make(map[int32]int32, len(base)+len(extra))
	ids := make([]int32, 0, len(base)+len(extra))
	for _, entry := range base {
		if _, exists := merged[entry.ID]; !exists {
			ids = append(ids, entry.ID)
		}
		merged[entry.ID] = entry.Level
	}
	for _, entry := range extra {
		if _, exists := merged[entry.ID]; exists {
			// 结果已有该附魔：诅咒保留原等级；非诅咒覆盖（随后会被移除）。
			if registry.EnchantmentIsCurse(entry.ID) {
				continue
			}
			merged[entry.ID] = entry.Level
			continue
		}
		merged[entry.ID] = entry.Level
		ids = append(ids, entry.ID)
	}
	slices.Sort(ids)
	entries := make([]item.EnchantmentEntry, 0, len(ids))
	for _, id := range ids {
		entries = append(entries, item.EnchantmentEntry{ID: id, Level: merged[id]})
	}
	return entries
}

// grindstoneExperienceValue 复刻原版 GrindstoneMenu 结果槽的经验价值
// （ij$4.g(dlt)）：全部非诅咒附魔的 getMinCost(等级) 之和。
func grindstoneExperienceValue(stack item.Stack) int32 {
	total := int32(0)
	for _, entry := range stack.Enchantments {
		if registry.EnchantmentIsCurse(entry.ID) {
			continue
		}
		if minCost, _, ok := registry.EnchantmentCostRange(entry.ID, entry.Level); ok {
			total += minCost
		}
	}
	return total
}

// grindstoneExperienceAmount 复刻原版 getExperienceAmount（ij$4.a(dwo)）：
// 两输入价值之和；大于 0 时为 ceil(total/2) + 随机 [0, ceil(total/2))。
func (s *Server) grindstoneExperienceAmount(first, second item.Stack) int32 {
	total := grindstoneExperienceValue(first) + grindstoneExperienceValue(second)
	if total <= 0 {
		return 0
	}
	half := (total + 1) / 2 // ceil(total/2)，total > 0
	return half + int32(s.nextRandom()%uint64(half))
}

// openGrindstone 打开砂轮机窗口（右键砂轮机方块）。
// 由会话 goroutine 调用。
func (s *Server) openGrindstone(player *session, x, y, z int) {
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
	if player.getEnchanting() != nil {
		s.closeEnchanting(player, false)
	}
	if player.getGrindstone() != nil {
		s.closeGrindstone(player, false)
	}
	menuType, ok := registry.StaticEntryID("minecraft:menu", "minecraft:grindstone")
	if !ok {
		return
	}
	player.setGrindstone(&grindstoneState{dim: player.dimensionID(), pos: [3]int{x, y, z}})
	player.windowID = player.nextWindowID()
	player.windowState = 0
	if err := player.writePacket(protocol.EncodeOpenScreen(player.windowID, menuType, grindstoneTitle)); err != nil {
		return
	}
	s.sendGrindstoneContent(player)
}

// sendGrindstoneContent 把砂轮机窗口的全部槽位内容发送给玩家（打开时调用）。
func (s *Server) sendGrindstoneContent(player *session) {
	state := player.getGrindstone()
	if state == nil {
		return
	}
	state.refreshResult()
	backing := grindstoneSlotBacking{player: player, state: state}
	slots := make([][]byte, 0, grindstoneSlots)
	for i := 0; i < grindstoneSlots; i++ {
		slots = append(slots, backing.Get(i).AppendSlot(nil))
	}
	_ = player.writePacket(protocol.EncodeContainerSetContent(
		player.windowID, player.windowState, slots, player.getCursor().AppendSlot(nil)))
}

// closeGrindstone 关闭砂轮机窗口：两个输入槽归还背包（放不下则掉落）。
// sendClose 表示是否向客户端发送 Close Container。
// 由会话 goroutine 调用。
func (s *Server) closeGrindstone(player *session, sendClose bool) {
	state := player.getGrindstone()
	if state == nil {
		return
	}
	player.setGrindstone(nil)
	windowID := player.windowID
	s.releaseCursor(player)
	for i := 0; i < 2; i++ {
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

// grindstoneStillValid 报告砂轮机窗口是否仍有效（原版 ItemCombinerMenu.stillValid）：
// 方块仍是砂轮机且在交互距离内。
func (s *Server) grindstoneStillValid(player *session, state *grindstoneState) bool {
	state.mu.Lock()
	dim, pos := state.dim, state.pos
	state.mu.Unlock()
	current := s.worldFor(dim).BlockAt(pos[0], pos[1], pos[2])
	if name, ok := s.blockNameOf(current); !ok || name != grindstoneBlockName {
		return false
	}
	px, py, pz, _, _ := player.playerPosition()
	return math.Hypot(px-(float64(pos[0])+0.5), pz-(float64(pos[2])+0.5)) <= grindstoneReach &&
		math.Abs(py-(float64(pos[1])+0.5)) <= grindstoneReach
}

// grindstoneTake 执行取走结果的副作用（对照原版 ij$4.onTake）：
// 按两输入的经验价值生成经验球、播放世界事件 1042、清空两个输入槽。
// 由会话 goroutine 调用。
func (s *Server) grindstoneTake(player *session, state *grindstoneState) {
	state.mu.Lock()
	first := state.inputs[0]
	second := state.inputs[1]
	dim, pos := state.dim, state.pos
	state.result = item.Empty()
	state.inputs[0] = item.Empty()
	state.inputs[1] = item.Empty()
	state.mu.Unlock()

	// 经验取走前计算（原版在清空输入前读取）。
	if amount := s.grindstoneExperienceAmount(first, second); amount > 0 {
		s.spawnXPOrbs(dim, float64(pos[0])+0.5, float64(pos[1])+0.5, float64(pos[2])+0.5, amount)
	}
	s.broadcastWorldEvent(dim, pos, grindstoneUseEvent, 0)
}

// grindstoneSlotBacking 是砂轮机窗口的槽位访问层。
type grindstoneSlotBacking struct {
	player *session
	state  *grindstoneState
}

// SlotCount 返回窗口槽位总数。
func (b grindstoneSlotBacking) SlotCount() int { return grindstoneSlots }

// Get 返回槽位内容（结果槽为最近一次计算的结果）。
func (b grindstoneSlotBacking) Get(index int) item.Stack {
	switch index {
	case grindstoneInputSlot, grindstoneAdditionSlot:
		return b.state.inputStack(index)
	case grindstoneResultSlot:
		return b.state.currentResult()
	default:
		return b.player.inventory.Get(containerPlayerInvSlot(index - 3))
	}
}

// Set 设置槽位内容（结果槽忽略）。
func (b grindstoneSlotBacking) Set(index int, stack item.Stack) {
	switch index {
	case grindstoneInputSlot, grindstoneAdditionSlot:
		inputs := b.state.snapshotInputs()
		inputs[index] = stack
		b.state.setInputs(inputs[0], inputs[1])
	case grindstoneResultSlot:
	default:
		b.player.inventory.Set(containerPlayerInvSlot(index-3), stack)
	}
}

// Supported 报告槽位是否可交互（结果槽只取不放）。
func (b grindstoneSlotBacking) Supported(index int) bool {
	return index != grindstoneResultSlot
}

// CanPlace 报告槽位是否接受该物品（原版输入槽 mayPlace：
// 可损坏物品或带附魔物品；结果槽不可放入）。
func (b grindstoneSlotBacking) CanPlace(slot int, stack item.Stack) bool {
	switch slot {
	case grindstoneInputSlot, grindstoneAdditionSlot:
		return grindstoneAcceptsStack(stack)
	case grindstoneResultSlot:
		return false
	}
	return true
}

// grindstoneResultClick 处理结果槽点击（取走结果）。
// 返回新的光标物品（working 由调用方在尾部写回与同步）。
func (s *Server) grindstoneResultClick(player *session, b grindstoneSlotBacking, working []item.Stack, cursor item.Stack, click protocol.ContainerClick) item.Stack {
	state := b.state
	// 取走一次结果并同步输入/结果工作副本。
	takeOnce := func() {
		s.grindstoneTake(player, state)
		inputs := state.snapshotInputs()
		working[grindstoneInputSlot] = inputs[0]
		working[grindstoneAdditionSlot] = inputs[1]
		working[grindstoneResultSlot] = state.currentResult()
	}
	switch click.Mode {
	case protocol.ClickModePickup:
		result := state.currentResult()
		if result.IsEmpty() {
			return cursor
		}
		switch {
		case cursor.IsEmpty():
			cursor = result
		case cursor.SameKind(result) && cursor.Count+result.Count <= cursor.MaxStack():
			cursor.Count += result.Count
		default:
			return cursor
		}
		takeOnce()
		return cursor
	case protocol.ClickModeQuickMove:
		// 结果移入背包（原版 move(3, 39, true)，反向）；放不下时整体失败。
		for i := 0; i < 64; i++ {
			result := state.currentResult()
			if result.IsEmpty() {
				break
			}
			remaining, _ := quickMoveArea(b, working, result, 3, 39, true)
			if !remaining.IsEmpty() {
				break
			}
			takeOnce()
		}
		return cursor
	case protocol.ClickModeThrow:
		result := state.currentResult()
		if result.IsEmpty() {
			return cursor
		}
		takeOnce()
		s.dropFromContainer(player, result)
		return cursor
	}
	return cursor
}
