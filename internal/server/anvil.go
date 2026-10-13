package server

import (
	"math"
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
//   - 成本上限：达到 40 级且非创造模式时无法取走（纯重命名降为 39 级上限）；
//   - 累积惩罚（repair_cost）：非纯重命名时按 min(2n+1, MaxInt32) 递增。
//
// 与切石机/锻造台一致，输入槽是窗口临时状态（原版 AnvilMenu 的输入容器不
// 持久化），关闭窗口时归还背包（放不下则掉落）。
//
// 已知限制（见 docs/TODO.md）：附魔合并没有实现——gmcs 的物品模型尚无附魔数据
// （全部物品的 enchantments/stored_enchantments 视为空），空数据下本实现与原版
// 行为一致（附魔合并段为空操作），但带附魔物品的合并不可用。

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
	// anvilReach 是窗口有效距离（原版 ItemCombinerMenu.stillValid 用
	// 玩家与方块中心的 4 格；本实现与普通容器一致，按水平/垂直距离近似）。
	anvilReach = 4.0
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
				// 同物品合并：剩余耐久相加，附加 12% 最大耐久奖励。
				additionalMaxDamage, _ := registry.MaxDamage(additional.ItemID)
				remainLeft := inputMaxDamage - input.Damage
				remainRight := additionalMaxDamage - additional.Damage
				bonus := remainRight + additionalMaxDamage*12/100
				newDamage := resultMaxDamage - (remainLeft + bonus)
				if newDamage < 0 {
					newDamage = 0
				}
				if newDamage < result.Damage {
					result.Damage = newDamage
					cost += anvilCostRepairSacrifice
				}
			}
			// 附魔合并（原版字节码 434-726）：gmcs 无附魔数据，该段为空操作
			// （对原版而言，输入不带附魔数据时该段同样不产生任何效果）。
			// 附魔系统实现后必须在此补上（含附魔书减半与 40 级特例）。
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
				stack = mergeIntoArea(working, stack, target[0], target[1])
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
