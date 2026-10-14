//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf16"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 铁砧：输入槽 0/1 + 结果槽 2 + 玩家物品栏 3–38。
// 结果与等级成本实时计算，对照 1.21.11 的 AnvilMenu.createResult 实现：
//   - 材料修复：输入可损坏且添加物是其修复材料（minecraft:repairable）时，
//     每个添加物修复 min(当前损坏, 最大耐久/4) 点耐久、成本 +1；
//   - 同物品合并（牺牲）：两个可损坏的同种物品合并剩余耐久与 12% 奖励，
//     有效果时成本 +2；
//   - 重命名：名称与物品当前悬停名不同时设置 custom_name、成本 +1；
//     未输入名称（或全空白）时移除已有 custom_name、成本 +1；
//   - 耐久合并：同物品（非附魔书）合并时剩余耐久相加并奖励 12% 最大耐久；
//   - 附魔合并：逐个处理添加物附魔，目标等级取高等级（同级 +1）并钳制到最大
//     等级，冲突附魔追加成本但不生效（详见 anvilMergeEnchantments）；
//   - 成本上限：达到 40 级且非创造模式时无法取走（纯重命名降为 39 级上限）；
//   - 累积惩罚（repair_cost）：非纯重命名时按 min(2n+1, MaxInt32) 递增。
//
// 与切石机/锻造台一致，输入槽是窗口临时状态（原版 AnvilMenu 的输入容器不
// 持久化），关闭窗口时归还背包（放不下则掉落）。
//
// 附魔合并读取物品自带的 enchantments/stored_enchantments 组件（见 item.Stack）；
// 附魔书入库/取出使用 stored_enchantments（41），其余物品使用 enchantments（13），
// 与附魔台产出一致。

// 铁砧窗口的槽位布局。
const (
	anvilInputSlot    = 0
	anvilAdditionSlot = 1
	anvilResultSlot   = 2
	anvilSlots        = 39 // 2 输入 + 结果 + 36 物品栏

	// anvilMaxNameLength 是重命名长度上限（原版 AnvilMenu 为 50 个 UTF-16 码元）。
	anvilMaxNameLength = 50
	// anvilCostLimit 是等级成本上限：达到 40 级且非创造模式时无法取走结果。
	anvilCostLimit = 40
	// anvilCostRepairSacrifice 是同物品合并（牺牲）的等级成本。
	anvilCostRepairSacrifice = 2
	// anvilReach 是窗口有效距离：原版 ItemCombinerMenu.stillValid 用
	// canInteractWithBlock(pos, 4.0)，即「方块交互距离属性（默认 4.5）+ 4.0」
	// ≈ 8.5 格；与普通容器一致，按水平/垂直距离用 8.0 盒近似。
	anvilReach = 8.0
)

// anvilState 是一个打开的铁砧窗口状态。
type anvilState struct {
	mu sync.Mutex
	// dim/pos 是铁砧方块的位置（取走结果时的损坏判定与窗口有效性校验）。
	dim world.Dimension
	pos [3]int
	// inputs 是输入槽（0、1）。
	inputs [2]item.Stack
	// name 是玩家输入的名称（已过滤）；nameSet 表示收到过重命名包
	// （对应原版 AnvilMenu.itemName != null）。
	name    string
	nameSet bool
	// result/cost/repairItemCountCost/onlyRenaming 是最近一次计算的结果
	// （对应原版结果容器与菜单的 cost 数据槽、repairItemCountCost、onlyRenaming）。
	result              item.Stack
	cost                int32
	repairItemCountCost int32
	onlyRenaming        bool
	// sentCost 是最近一次发给客户端的成本（窗口属性；sentValid 表示已发送过）。
	sentCost  int32
	sentValid bool
}

// inputStack 返回输入槽内容（0/1）。
func (st *anvilState) inputStack(index int) item.Stack {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.inputs[index]
}

// setInputs 写入两个输入槽。
func (st *anvilState) setInputs(first, second item.Stack) {
	st.mu.Lock()
	st.inputs[0] = first
	st.inputs[1] = second
	st.mu.Unlock()
}

// snapshotInputs 返回两个输入槽的快照。
func (st *anvilState) snapshotInputs() [2]item.Stack {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.inputs
}

// currentResult 返回最近一次计算的结果快照。
func (st *anvilState) currentResult() anvilResult {
	st.mu.Lock()
	defer st.mu.Unlock()
	return anvilResult{
		result:              st.result,
		cost:                st.cost,
		repairItemCountCost: st.repairItemCountCost,
		onlyRenaming:        st.onlyRenaming,
	}
}

// anvilResult 是铁砧计算的结果（对照原版 createResult 的输出）。
type anvilResult struct {
	result              item.Stack
	cost                int32
	repairItemCountCost int32
	onlyRenaming        bool
}

// computeAnvilResult 计算铁砧结果与等级成本（对照 1.21.11 AnvilMenu.createResult）。
// nameSet 对应原版保存的玩家输入名称是否存在（itemName != null）；creative 控制
// 成本上限豁免（原版 player 的无限材料判断，即创造模式）。
func computeAnvilResult(input, additional item.Stack, name string, nameSet, creative bool) anvilResult {
	res := anvilResult{}
	if input.IsEmpty() {
		return res // 结果为空、成本 0
	}
	// 原版此处还检查物品是否可以携带附魔（enchantments 组件存在性）：
	// 1.21.11 全部物品原型都携带该组件（已验证 1505/1505），该检查恒真，略去。

	// 原版用 input.copy() 作为结果（全部修复操作只改副本）。
	result := input
	inputMaxDamage, _ := registry.MaxDamage(input.ItemID)
	resultMaxDamage, resultDamageable := registry.MaxDamage(result.ItemID)
	// baseCost 是两个输入的累积惩罚之和（原版用 long 累加）。
	baseCost := int64(input.RepairCost) + int64(additional.RepairCost)
	var cost int64 // 原版 var2（int）：一次操作的累计成本
	renamed := false
	res.repairItemCountCost = 0

	if !additional.IsEmpty() {
		// 附魔书使用 stored_enchantments 组件（1.21.11 仅附魔书携带）。
		rightIsBook := registry.HasStoredEnchantments(additional.ItemID)
		if resultDamageable && registry.IsRepairMaterial(input.ItemID, additional.ItemID) {
			// 材料修复（原版字节码 178-287）：每个材料修复
			// min(当前损坏, 最大耐久/4) 点耐久，最多消耗添加物的全部数量。
			step := min(result.Damage, resultMaxDamage/4)
			if step <= 0 {
				return res // 未损坏：无结果
			}
			used := int32(0)
			for step > 0 && used < additional.Count {
				result.Damage -= step
				cost++
				step = min(result.Damage, resultMaxDamage/4)
				used++
			}
			res.repairItemCountCost = used
		} else {
			// 非材料修复（原版字节码 290-431）：非附魔书时要求同物品且可损坏。
			if !rightIsBook && (result.ItemID != additional.ItemID || !resultDamageable) {
				return res // 无法合并：无结果
			}
			if resultDamageable && !rightIsBook {
				// 同物品合并：剩余耐久相加，附加 12% 最大耐久奖励（原版奖励基于
				// 结果的 max_damage；同物品时与添加物的 max_damage 相同）。
				additionalMaxDamage, _ := registry.MaxDamage(additional.ItemID)
				remainLeft := inputMaxDamage - input.Damage
				remainRight := additionalMaxDamage - additional.Damage
				bonus := remainRight + resultMaxDamage*12/100
				newDamage := resultMaxDamage - (remainLeft + bonus)
				if newDamage < 0 {
					newDamage = 0
				}
				if newDamage < result.Damage {
					result.Damage = newDamage
					cost += anvilCostRepairSacrifice
				}
			}
			// 附魔合并（原版字节码 434-726）。
			if len(additional.Enchantments) > 0 {
				merge := anvilMergeEnchantments(input, additional, rightIsBook, creative)
				if merge.conflictOnly {
					return res // 全部附魔都因冲突未生效：无结果、成本 0
				}
				result.Enchantments = merge.enchantments
				cost += merge.costDelta
			}
		}
	}

	// 重命名（原版字节码 756-845）。
	if nameSet && !anvilNameIsBlank(name) {
		if name != input.HoverName() {
			renamed = true
			cost++
			result.CustomName = name
		}
	} else if input.CustomName != "" {
		renamed = true
		cost++
		result.CustomName = ""
	}

	// 最终成本（原版字节码 846-874）：cost 为 0 时成本为 0，否则
	// clamp(baseCost + cost, 0, MaxInt32)。
	finalCost := int32(0)
	if cost > 0 {
		combined := baseCost + cost
		if combined < 0 {
			combined = 0
		}
		if combined > math.MaxInt32 {
			combined = math.MaxInt32
		}
		finalCost = int32(combined)
	}
	// cost 为 0 表示没有任何可执行操作：结果为空（原版字节码 877-884）。
	if cost <= 0 {
		result = item.Empty()
	}
	// 纯重命名（renamed == cost == 1）时成本上限降为 39 级（原版字节码 886-920）。
	if renamed && cost == 1 {
		if finalCost >= anvilCostLimit {
			finalCost = anvilCostLimit - 1
		}
		res.onlyRenaming = true
	}
	// 成本达到 40 级且非创造模式：结果为空、无法取走（原版字节码 923-948）。
	if finalCost >= anvilCostLimit && !creative {
		result = item.Empty()
	}
	if !result.IsEmpty() {
		// 累积惩罚（原版字节码 950-1060）：取两者较大值；非纯重命名时递增。
		penalty := result.RepairCost
		if penalty < additional.RepairCost {
			penalty = additional.RepairCost
		}
		if !(renamed && cost == 1) {
			penalty = increasedRepairCost(penalty)
		}
		result.RepairCost = penalty
	}
	res.result = result
	res.cost = finalCost
	return res
}

// anvilEnchantMerge 是附魔合并的结果（anvilMergeEnchantments 的输出）。
type anvilEnchantMerge struct {
	// enchantments 是合并后的附魔列表（按附魔 ID 升序；输入自带的附魔即使
	// 本轮没有生效条目也会原样返回）。
	enchantments []item.EnchantmentEntry
	// costDelta 是附魔合并追加的等级成本。
	costDelta int64
	// conflictOnly 表示「存在冲突且没有任何附魔生效」（原版此时结果无效）。
	conflictOnly bool
}

// anvilMergeEnchantments 复刻原版 AnvilMenu.createResult 的附魔合并段
// （dhm 字节码 434-726）。规则：
//   - 逐个处理添加物（附魔书取 stored_enchantments，其余取 enchantments）的
//     附魔：目标等级为同名附魔的较高等级，两者相同时 +1（再钳制到最大等级）；
//   - 附魔必须可作用于输入物品（supported_items；创造模式或输入物品本身是
//     附魔书时豁免），并与结果已有附魔互相兼容：每个冲突追加 1 点成本，且该
//     附魔不生效；
//   - 生效附魔按 anvil_cost × 目标等级计入成本；添加物为附魔书时 anvil_cost
//     折半（至少 1）；输入堆叠数大于 1 时成本直接为 40（原版特例）；
//   - 所有附魔都因冲突未生效时整体无效（conflictOnly）。
//
// 原版逐个附魔使用哈希表迭代序，多冲突并存时保留哪些附魔取决于该顺序；
// gmcs 按物品组件的升序 ID 迭代（确定性），平局取舍可能与原版不同。
func anvilMergeEnchantments(input, additional item.Stack, additionIsBook, creative bool) anvilEnchantMerge {
	// 输入为附魔书时全部附魔可用（书可以承载任意附魔组合）。
	inputIsBook := registry.HasStoredEnchantments(input.ItemID)
	merged := make(map[int32]int32, len(input.Enchantments)+len(additional.Enchantments))
	for _, entry := range input.Enchantments {
		if existing, ok := merged[entry.ID]; !ok || entry.Level > existing {
			merged[entry.ID] = entry.Level
		}
	}
	var (
		cost     int64
		applied  bool
		conflict bool
	)
	for _, entry := range additional.Enchantments {
		current := merged[entry.ID]
		level := entry.Level
		switch {
		case current == level: // 同名同级：提升一级
			level++
		case current > level: // 已有更高等级：保留
			level = current
		}
		applicable := registry.EnchantmentSupported(entry.ID, input.ItemID)
		if creative || inputIsBook {
			applicable = true
		}
		for existingID := range merged {
			if existingID == entry.ID {
				continue
			}
			if !registry.EnchantmentCompatible(entry.ID, existingID) {
				applicable = false
				cost++ // 每个冲突附魔各追加 1 点成本（原版无 break）
			}
		}
		if !applicable {
			conflict = true
			continue // 冲突附魔不生效（原版 flag 不置位）
		}
		applied = true
		if maxLevel, ok := registry.EnchantmentMaxLevel(entry.ID); ok && level > maxLevel {
			level = maxLevel
		}
		merged[entry.ID] = level
		anvilCost, _ := registry.EnchantmentAnvilCost(entry.ID)
		if additionIsBook {
			anvilCost = max(1, anvilCost/2) // 附魔书减半（至少 1）
		}
		cost += int64(anvilCost) * int64(level)
		if input.Count > 1 {
			cost = 40 // 原版特例：输入堆叠数大于 1 时成本直接为 40
		}
	}
	if conflict && !applied {
		return anvilEnchantMerge{conflictOnly: true}
	}
	// item.Stack 约定：附魔按 ID 升序。
	ids := make([]int32, 0, len(merged))
	for id := range merged {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	enchantments := make([]item.EnchantmentEntry, 0, len(ids))
	for _, id := range ids {
		enchantments = append(enchantments, item.EnchantmentEntry{ID: id, Level: merged[id]})
	}
	return anvilEnchantMerge{enchantments: enchantments, costDelta: cost}
}

// increasedRepairCost 返回铁砧累积惩罚的下一级
// （原版 AnvilMenu.calculateIncreasedRepairCost：min(2n+1, MaxInt32)）。
func increasedRepairCost(penalty int32) int32 {
	next := int64(penalty)*2 + 1
	if next > math.MaxInt32 {
		next = math.MaxInt32
	}
	return int32(next)
}

// anvilFilterName 复刻原版 AnvilMenu.validateName 的名称处理：
// 过滤不允许的字符（§ 格式码、控制字符与 DEL；对照 StringUtil 的
// isAllowedChatCharacter），再检查长度上限。ok=false 表示输入无效（原版忽略）。
func anvilFilterName(name string) (string, bool) {
	var builder strings.Builder
	builder.Grow(len(name))
	for _, r := range name {
		if r == '§' || r < 32 || r == 127 {
			continue
		}
		builder.WriteRune(r)
	}
	filtered := builder.String()
	// 原版 String.length() 按 UTF-16 码元计数。
	if len(utf16.Encode([]rune(filtered))) > anvilMaxNameLength {
		return "", false
	}
	return filtered, true
}

// anvilNameIsBlank 复刻原版 StringUtil.isBlank 的空白判定
// （Character.isWhitespace 与 Character.isSpaceChar 的并集：
// Go 的 unicode.IsSpace 去除 U+0085 并加入 U+001C–U+001F 后与之一致）。
func anvilNameIsBlank(name string) bool {
	for _, r := range name {
		if r >= 0x1C && r <= 0x1F {
			continue
		}
		if r != 0x85 && unicode.IsSpace(r) {
			continue
		}
		return false
	}
	return true
}

// anvilSlotBacking 是铁砧窗口的槽位访问层。
type anvilSlotBacking struct {
	player *session
	state  *anvilState
}

// SlotCount 返回窗口槽位总数。
func (b anvilSlotBacking) SlotCount() int { return anvilSlots }

// Get 返回槽位内容（结果槽为最近一次计算的结果）。
func (b anvilSlotBacking) Get(index int) item.Stack {
	switch index {
	case anvilInputSlot, anvilAdditionSlot:
		return b.state.inputStack(index)
	case anvilResultSlot:
		return b.state.currentResult().result
	default:
		return b.player.inventory.Get(containerPlayerInvSlot(index - 3))
	}
}

// Set 设置槽位内容（结果槽忽略）。
func (b anvilSlotBacking) Set(index int, stack item.Stack) {
	switch index {
	case anvilInputSlot, anvilAdditionSlot:
		inputs := b.state.snapshotInputs()
		inputs[index] = stack
		b.state.setInputs(inputs[0], inputs[1])
	case anvilResultSlot:
	default:
		b.player.inventory.Set(containerPlayerInvSlot(index-3), stack)
	}
}

// Supported 报告槽位是否可交互（结果槽只取不放）。
func (b anvilSlotBacking) Supported(index int) bool {
	return index != anvilResultSlot
}

// 铁砧方块名（原版 BlockTags.ANVIL 的三个方块）。
const (
	anvilBlockName        = "minecraft:anvil"
	chippedAnvilBlockName = "minecraft:chipped_anvil"
	damagedAnvilBlockName = "minecraft:damaged_anvil"
)

// isAnvilBlockName 报告方块名是否为铁砧（anvil/chipped_anvil/damaged_anvil）。
func isAnvilBlockName(name string) bool {
	switch name {
	case anvilBlockName, chippedAnvilBlockName, damagedAnvilBlockName:
		return true
	}
	return false
}

// isAnvilState 报告方块状态是否为铁砧（对应原版 BlockTags.ANVIL）。
func isAnvilState(state uint16) bool {
	name, _, ok := registry.StateProps(state)
	return ok && isAnvilBlockName(name)
}

// openAnvil 打开铁砧窗口（右键铁砧方块）。
// 由会话 goroutine 调用。
func (s *Server) openAnvil(player *session, x, y, z int) {
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
	menuType, ok := registry.StaticEntryID("minecraft:menu", "minecraft:anvil")
	if !ok {
		return
	}
	player.setAnvil(&anvilState{dim: player.dimensionID(), pos: [3]int{x, y, z}})
	player.windowID = player.nextWindowID()
	player.windowState = 0
	// 原版铁砧窗口的标题是「Repair」（container.repair）。
	if err := player.writePacket(protocol.EncodeOpenScreen(player.windowID, menuType, "Repair")); err != nil {
		return
	}
	s.sendAnvilContent(player)
}

// sendAnvilContent 把铁砧窗口的全部槽位内容发送给玩家（打开时调用）。
func (s *Server) sendAnvilContent(player *session) {
	state := player.getAnvil()
	if state == nil {
		return
	}
	// 打开时先计算一次（原版菜单打开后会立即计算并同步成本属性）。
	s.refreshAnvil(player, state)
	backing := anvilSlotBacking{player: player, state: state}
	slots := make([][]byte, 0, anvilSlots)
	for i := 0; i < anvilSlots; i++ {
		slots = append(slots, backing.Get(i).AppendSlot(nil))
	}
	_ = player.writePacket(protocol.EncodeContainerSetContent(
		player.windowID, player.windowState, slots, player.getCursor().AppendSlot(nil)))
}

// closeAnvil 关闭铁砧窗口：两个输入槽归还背包（放不下则掉落）。
// sendClose 表示是否向客户端发送 Close Container。
// 由会话 goroutine 调用。
func (s *Server) closeAnvil(player *session, sendClose bool) {
	state := player.getAnvil()
	if state == nil {
		return
	}
	player.setAnvil(nil)
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

// anvilStillValid 报告铁砧窗口是否仍有效（原版 ItemCombinerMenu.stillValid）：
// 方块仍是铁砧且在交互距离内。
func (s *Server) anvilStillValid(player *session, state *anvilState) bool {
	state.mu.Lock()
	dim, pos := state.dim, state.pos
	state.mu.Unlock()
	if !isAnvilState(s.worldFor(dim).BlockAt(pos[0], pos[1], pos[2])) {
		return false
	}
	px, py, pz, _, _ := player.playerPosition()
	return math.Hypot(px-(float64(pos[0])+0.5), pz-(float64(pos[2])+0.5)) <= anvilReach &&
		math.Abs(py-(float64(pos[1])+0.5)) <= anvilReach
}

// refreshAnvil 重算铁砧结果并同步成本属性（结果槽由调用方发送）。
// 返回新的计算结果。由会话 goroutine 调用。
func (s *Server) refreshAnvil(player *session, state *anvilState) anvilResult {
	state.mu.Lock()
	inputs := state.inputs
	name, nameSet := state.name, state.nameSet
	sentCost, sentValid := state.sentCost, state.sentValid
	state.mu.Unlock()

	creative := player.gameModeID() == uint8(config.GameModeCreative)
	res := computeAnvilResult(inputs[0], inputs[1], name, nameSet, creative)

	state.mu.Lock()
	state.result = res.result
	state.cost = res.cost
	state.repairItemCountCost = res.repairItemCountCost
	state.onlyRenaming = res.onlyRenaming
	state.sentCost = res.cost
	state.sentValid = true
	state.mu.Unlock()

	// 成本通过窗口属性 0 同步（原版 DataSlot）。
	if !sentValid || sentCost != res.cost {
		player.tryWrite(protocol.EncodeSetContainerProperty(player.windowID, 0, int16(res.cost)))
	}
	return res
}

// anvilCanPickup 报告当前成本下玩家能否取走结果（原版 AnvilMenu.mayPickup）：
// 成本为正，且创造模式或经验等级不小于成本。
func anvilCanPickup(player *session, cost int32) bool {
	if cost <= 0 {
		return false
	}
	if player.gameModeID() == uint8(config.GameModeCreative) {
		return true
	}
	_, level, _ := player.experienceStatus()
	return level >= cost
}

// handleRenameItem 处理 Rename Item 包（铁砧窗口的名称输入）。
// 由会话读循环调用。
func (s *Server) handleRenameItem(player *session, name string) {
	state := player.getAnvil()
	if state == nil {
		return
	}
	if !s.anvilStillValid(player, state) {
		s.closeAnvil(player, true)
		return
	}
	filtered, ok := anvilFilterName(name)
	if !ok {
		return // 超过长度上限：忽略本次输入（原版 validateName 返回 null）
	}
	state.mu.Lock()
	unchanged := state.nameSet && state.name == filtered
	state.name = filtered
	state.nameSet = true
	state.mu.Unlock()
	if unchanged {
		return
	}
	res := s.refreshAnvil(player, state)
	// 名称影响结果物品的 custom_name 与成本：同步结果槽。
	player.windowState++
	player.tryWrite(protocol.EncodeSetContainerSlot(
		player.windowID, player.windowState, anvilResultSlot, res.result.AppendSlot(nil)))
}

// anvilResultClick 处理结果槽点击（取走结果）。
// 返回新的光标物品（working 由调用方在尾部写回与同步）。
func (s *Server) anvilResultClick(player *session, b anvilSlotBacking, working []item.Stack, cursor item.Stack, click protocol.ContainerClick) item.Stack {
	state := b.state
	// 取走一次结果并同步输入/结果工作副本。
	takeOnce := func() {
		s.anvilTake(player, state)
		inputs := state.snapshotInputs()
		working[anvilInputSlot] = inputs[0]
		working[anvilAdditionSlot] = inputs[1]
		working[anvilResultSlot] = s.refreshAnvil(player, state).result
	}
	switch click.Mode {
	case protocol.ClickModePickup:
		res := state.currentResult()
		if res.result.IsEmpty() || !anvilCanPickup(player, res.cost) {
			return cursor
		}
		switch {
		case cursor.IsEmpty():
			cursor = res.result
		case cursor.SameKind(res.result) && cursor.Count+res.result.Count <= cursor.MaxStack():
			cursor.Count += res.result.Count
		default:
			return cursor
		}
		takeOnce()
		return cursor
	case protocol.ClickModeQuickMove:
		// 结果移入背包；放不下时整体失败（与切石机/锻造台一致）。
		for i := 0; i < 64; i++ {
			res := state.currentResult()
			if res.result.IsEmpty() || !anvilCanPickup(player, res.cost) {
				break
			}
			stack := res.result
			for _, target := range [][2]int{{3, 30}, {30, 39}} {
				stack = mergeIntoArea(b, working, stack, target[0], target[1])
				if stack.IsEmpty() {
					break
				}
			}
			if !stack.IsEmpty() {
				break
			}
			takeOnce()
		}
		return cursor
	case protocol.ClickModeThrow:
		res := state.currentResult()
		if res.result.IsEmpty() || !anvilCanPickup(player, res.cost) {
			return cursor
		}
		takeOnce()
		s.dropFromContainer(player, res.result)
		return cursor
	}
	return cursor
}

// anvilTake 执行取走结果的副作用（对照原版 AnvilMenu.onTake）：
// 扣等级（非创造）、消耗修复材料或清空第二输入、清空第一输入、铁砧损坏判定。
// 由会话 goroutine 调用。
func (s *Server) anvilTake(player *session, state *anvilState) {
	state.mu.Lock()
	res := state.currentResultLocked()
	dim, pos := state.dim, state.pos
	state.mu.Unlock()
	creative := player.gameModeID() == uint8(config.GameModeCreative)

	// 扣经验等级（非创造模式；原版 giveExperienceLevels(-cost)）。
	if !creative {
		bar, level, total := player.giveExperienceLevels(-res.cost)
		player.tryWrite(protocol.EncodeSetExperience(bar, level, total))
	}

	state.mu.Lock()
	if res.repairItemCountCost > 0 {
		// 材料修复：消耗对应数量的修复材料（不足则清空槽位）。
		add := state.inputs[1]
		if !add.IsEmpty() && add.Count > res.repairItemCountCost {
			add.Count -= res.repairItemCountCost
			state.inputs[1] = add
		} else {
			state.inputs[1] = item.Empty()
		}
	} else if !res.onlyRenaming {
		// 非纯重命名：清空第二输入（原版行为）。
		state.inputs[1] = item.Empty()
	}
	state.inputs[0] = item.Empty()
	state.result = item.Empty()
	state.cost = 0
	state.repairItemCountCost = 0
	state.onlyRenaming = false
	state.mu.Unlock()

	// 铁砧损坏判定（原版 onTake 的方块回调）。
	s.anvilDamageChance(player, dim, pos)
}

// currentResultLocked 返回结果快照；调用方需持有 state.mu。
func (st *anvilState) currentResultLocked() anvilResult {
	return anvilResult{
		result:              st.result,
		cost:                st.cost,
		repairItemCountCost: st.repairItemCountCost,
		onlyRenaming:        st.onlyRenaming,
	}
}

// anvilDamageChance 按原版规则在取走结果后损坏铁砧：非创造模式、方块仍是铁砧
// 且 12% 概率命中时降级（anvil → chipped → damaged，已损坏时拆除）；否则仅播放
// 使用音效（世界事件 1030）。
func (s *Server) anvilDamageChance(player *session, dim world.Dimension, pos [3]int) {
	w := s.worldFor(dim)
	current := w.BlockAt(pos[0], pos[1], pos[2])
	creative := player.gameModeID() == uint8(config.GameModeCreative)
	if !creative && isAnvilState(current) && s.nextRandom()%1000 < 120 {
		if next, ok := registry.AnvilDamageNext(current); ok {
			if w.SetBlock(pos[0], pos[1], pos[2], next) {
				s.broadcastBlockUpdate(dim, pos[0], pos[1], pos[2], int32(next))
			}
			s.broadcastWorldEvent(dim, pos, 1030, 0)
			return
		}
		// 已损坏的铁砧：拆除并播放破坏音效（世界事件 1029）。
		if w.SetBlock(pos[0], pos[1], pos[2], world.AirBlock) {
			s.broadcastBlockUpdate(dim, pos[0], pos[1], pos[2], int32(world.AirBlock))
		}
		s.broadcastWorldEvent(dim, pos, 1029, 0)
		return
	}
	s.broadcastWorldEvent(dim, pos, 1030, 0)
}
