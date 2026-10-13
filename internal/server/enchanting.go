package server

// 附魔台（原版 EnchantmentMenu/EnchantmentTableBlock）：输入槽（最大 1 个）+
// 青金石槽 + 三个随机选项；书架提升选项等级；取走附魔消耗等级与青金石。
//
// 算法逐字节码翻译自 1.21.11 反编译（dif=EnchantmentMenu、dsq=EnchantmentHelper、
// ech=EnchantmentTableBlock），随机源使用与 java.util.Random（LegacyRandomSource）
// 逐位一致的 48 位 LCG，因此给定相同种子/输入时选项与原版一致。
//
// 槽位布局（38 槽）：0 输入、1 青金石、2-37 玩家背包（主背包 27 + 快捷栏 9）。
// 数据槽（10 个）：0-2 选项成本、3-5 附魔线索（-1 无）、6-8 等级线索（-1 无）、
// 9 附魔种子。
//
// 已知限制（见 docs/TODO.md）：附魔的数值效果（伤害/保护/挖掘等）未实现
// （无战斗/挖掘系统），仅保存与展示附魔数据；附魔统计与进度未实现。

import (
	"math"
	"sync"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

const (
	// enchantingInputSlot 是输入槽（原版 EnchantmentMenu：0，最大堆叠 1）。
	enchantingInputSlot = 0
	// enchantingLapisSlot 是青金石槽（原版：1，仅接受青金石）。
	enchantingLapisSlot = 1
	// enchantingSlots 是附魔台菜单的总槽位数（2 个容器槽 + 36 玩家槽）。
	enchantingSlots = 38
	// enchantingOptionCount 是随机选项数量（原版固定 3）。
	enchantingOptionCount = 3
	// enchantingMaxBookshelves 是书架数量上限（原版：超过 15 按 15 计）。
	enchantingMaxBookshelves = 15
	// enchantingBookName 是普通书（附魔台可把书附魔为附魔书）。
	enchantingBookName = "minecraft:book"
	// enchantingTableName 是附魔台方块的注册名。
	enchantingTableName = "minecraft:enchanting_table"
)

// 物品 ID 的惰性查询（缺失时返回 0：0 不会与真实物品匹配成功，行为退化为
// 「书附魔特例不生效」，不影响其余逻辑）。
var (
	enchantingLapisItemID = sync.OnceValue(func() int32 {
		id, _ := registry.ItemID("minecraft:lapis_lazuli")
		return id
	})
	enchantingBookItemID = sync.OnceValue(func() int32 {
		id, _ := registry.ItemID(enchantingBookName)
		return id
	})
)

// javaRandom 复刻 java.util.Random（=原版 LegacyRandomSource 的算法）；
// 附魔台的选项计算要求随机序列与原版逐位一致。
type javaRandom struct {
	seed uint64 // 48 位有效
}

// javaRandomMask 是 LCG 的 48 位掩码。
const javaRandomMask = (1 << 48) - 1

// setSeed 复刻 java.util.Random#setSeed（seed 为 int 时按符号扩展）。
func (r *javaRandom) setSeed(seed int64) {
	r.seed = (uint64(seed) ^ 0x5DEECE66D) & javaRandomMask
}

// next 返回下一个最多 bits 位的随机数（java.util.Random#next）。
func (r *javaRandom) next(bits uint) int32 {
	r.seed = (r.seed*0x5DEECE66D + 0xB) & javaRandomMask
	return int32(r.seed >> (48 - bits))
}

// nextInt 复刻 java.util.Random#nextInt(bound)：2 的幂走快速路径，
// 否则使用带溢出检测的拒绝采样。
func (r *javaRandom) nextInt(bound int32) int32 {
	if bound <= 0 {
		return 0
	}
	if bound&(bound-1) == 0 {
		return int32((int64(bound) * int64(r.next(31))) >> 31)
	}
	for {
		bits := r.next(31)
		val := bits % bound
		if bits-val+(bound-1) >= 0 {
			return val
		}
	}
}

// nextFloat 复刻 java.util.Random#nextFloat（[0,1)）。
func (r *javaRandom) nextFloat() float32 {
	return float32(r.next(24)) / float32(1<<24)
}

// enchantmentInstance 是一个附魔实例（附魔注册表 ID + 等级）。
type enchantmentInstance struct {
	id    int32
	level int32
}

// enchantingState 是一个已打开的附魔台窗口；服务器侧权威状态。
type enchantingState struct {
	mu  sync.Mutex
	dim world.Dimension
	pos [3]int
	// inputs 是容器槽位：0 输入、1 青金石。
	inputs [2]item.Stack
	// costs 是三个选项的成本（0 表示无效选项）。
	costs [enchantingOptionCount]int32
	// enchantClue / levelClue 是选项展示的附魔线索（-1 表示无）。
	enchantClue [enchantingOptionCount]int32
	levelClue   [enchantingOptionCount]int32
	// seed 是玩家的附魔种子（原版菜单独立 DataSlot，初值取自玩家）。
	seed int32
}

// inputStack 返回容器槽位内容（线程安全）。
func (st *enchantingState) inputStack(index int) item.Stack {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.inputs[index]
}

// setInput 设置容器槽位内容（线程安全）。
func (st *enchantingState) setInput(index int, stack item.Stack) {
	st.mu.Lock()
	st.inputs[index] = stack
	st.mu.Unlock()
}

// isEnchantingTableState 报告方块状态是否为附魔台。
// 附魔台是无属性方块，用全量状态名表查询（StateProps 只覆盖有属性的方块）。
func isEnchantingTableState(state uint16) bool {
	name, ok := registry.BlockNameByState(state)
	return ok && name == enchantingTableName
}

// openEnchanting 打开附魔台窗口（右键附魔台）。由会话 goroutine 调用。
func (s *Server) openEnchanting(player *session, x, y, z int) {
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
	menuType, ok := registry.StaticEntryID("minecraft:menu", "minecraft:enchantment")
	if !ok {
		return
	}
	state := &enchantingState{
		dim:  player.dimensionID(),
		pos:  [3]int{x, y, z},
		seed: player.getEnchantmentSeed(),
	}
	for i := 0; i < enchantingOptionCount; i++ {
		state.enchantClue[i] = -1
		state.levelClue[i] = -1
	}
	player.setEnchanting(state)
	player.windowID = player.nextWindowID()
	player.windowState = 0
	// 原版附魔台窗口的标题是「Enchant」（container.enchant）。
	if err := player.writePacket(protocol.EncodeOpenScreen(player.windowID, menuType, "Enchant")); err != nil {
		return
	}
	// 打开时计算一次选项（输入槽为空时全部清零，与原版一致）。
	s.refreshEnchantingOptions(state)
	s.sendEnchantingContent(player)
	s.sendEnchantingProperties(player, state)
}

// sendEnchantingContent 把附魔台窗口的全部槽位内容发送给玩家（打开时调用）。
func (s *Server) sendEnchantingContent(player *session) {
	state := player.getEnchanting()
	if state == nil {
		return
	}
	backing := enchantingSlotBacking{player: player, state: state}
	slots := make([][]byte, 0, enchantingSlots)
	for i := 0; i < enchantingSlots; i++ {
		slots = append(slots, backing.Get(i).AppendSlot(nil))
	}
	_ = player.writePacket(protocol.EncodeContainerSetContent(
		player.windowID, player.windowState, slots, player.getCursor().AppendSlot(nil)))
}

// sendEnchantingSlot 把单个槽位变更发送给玩家。
func (s *Server) sendEnchantingSlot(player *session, slot int, stack item.Stack) {
	player.windowState++
	player.tryWrite(protocol.EncodeSetContainerSlot(
		player.windowID, player.windowState, int16(slot), stack.AppendSlot(nil)))
}

// sendEnchantingProperties 同步全部 10 个数据槽（成本/线索/种子）。
// 与原版一致：值按 short 截断发送。
func (s *Server) sendEnchantingProperties(player *session, state *enchantingState) {
	state.mu.Lock()
	values := [enchantingOptionCount*3 + 1]int16{
		int16(state.costs[0]), int16(state.costs[1]), int16(state.costs[2]),
		int16(state.enchantClue[0]), int16(state.enchantClue[1]), int16(state.enchantClue[2]),
		int16(state.levelClue[0]), int16(state.levelClue[1]), int16(state.levelClue[2]),
		int16(state.seed),
	}
	state.mu.Unlock()
	for index, value := range values {
		player.tryWrite(protocol.EncodeSetContainerProperty(player.windowID, int16(index), value))
	}
}

// closeEnchanting 关闭附魔台窗口：两个输入槽归还背包（放不下则掉落）。
// sendClose 表示是否向客户端发送 Close Container。
// 由会话 goroutine 调用。
func (s *Server) closeEnchanting(player *session, sendClose bool) {
	state := player.getEnchanting()
	if state == nil {
		return
	}
	player.setEnchanting(nil)
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

// enchantingStillValid 报告附魔台窗口是否仍有效（原版 EnchantmentMenu.stillValid
// = ContainerLevelAccess 的有效距离检查）：方块仍是附魔台且在交互距离内。
// 与原版一致使用容器距离（交互距离属性 4.5 + 4.0 = 8.5，这里用 8.0 盒近似）。
func (s *Server) enchantingStillValid(player *session, state *enchantingState) bool {
	state.mu.Lock()
	dim, pos := state.dim, state.pos
	state.mu.Unlock()
	if !isEnchantingTableState(s.worldFor(dim).BlockAt(pos[0], pos[1], pos[2])) {
		return false
	}
	px, py, pz, _, _ := player.playerPosition()
	return math.Hypot(px-(float64(pos[0])+0.5), pz-(float64(pos[2])+0.5)) <= containerReach &&
		math.Abs(py-(float64(pos[1])+0.5)) <= containerReach
}

// refreshEnchantingOptions 重算三个选项（原版 EnchantmentMenu#slotsChanged 的
// onContentsChanged 部分）：统计书架、以种子重播随机源、计算成本与线索。
// 由会话 goroutine 调用。
func (s *Server) refreshEnchantingOptions(state *enchantingState) {
	state.mu.Lock()
	input := state.inputs[enchantingInputSlot]
	seed := state.seed
	state.mu.Unlock()

	value, enchantable := registry.EnchantableValue(input.ItemID)
	// 原版：空物品或不可附魔（无 enchantable 组件或已有附魔）时全部清零。
	if input.IsEmpty() || !enchantable || len(input.Enchantments) > 0 {
		state.mu.Lock()
		for i := 0; i < enchantingOptionCount; i++ {
			state.costs[i] = 0
			state.enchantClue[i] = -1
			state.levelClue[i] = -1
		}
		state.mu.Unlock()
		return
	}

	bookshelves := s.countBookshelves(state.dim, state.pos)
	rng := &javaRandom{}
	rng.setSeed(int64(seed))
	state.mu.Lock()
	for slot := int32(0); slot < enchantingOptionCount; slot++ {
		cost := enchantmentCost(rng, slot, bookshelves, value)
		if cost < slot+1 {
			cost = 0
		}
		state.costs[slot] = cost
		state.enchantClue[slot] = -1
		state.levelClue[slot] = -1
	}
	state.mu.Unlock()

	for slot := int32(0); slot < enchantingOptionCount; slot++ {
		state.mu.Lock()
		cost := state.costs[slot]
		state.mu.Unlock()
		if cost <= 0 {
			continue
		}
		// 与原版一致：每个选项先以 seed+slot 重播随机源再取候选。
		list := enchantingResults(rng, int64(seed), input, slot, cost)
		if len(list) == 0 {
			continue
		}
		inst := list[rng.nextInt(int32(len(list)))]
		state.mu.Lock()
		state.enchantClue[slot] = inst.id
		state.levelClue[slot] = inst.level
		state.mu.Unlock()
	}
}

// countBookshelves 统计附魔台周围的有效书架数量（原版 EnchantmentTableBlock）：
// 32 个偏移（x/z ∈ [-2,2] 的外圈、y ∈ {0,1}）处是书架、且附魔台到该位置的
// 中点（偏移/2）是传播方块（#replaceable 等）才算数。
func (s *Server) countBookshelves(dim world.Dimension, pos [3]int) int32 {
	w := s.worldFor(dim)
	count := int32(0)
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			if absInt(dx) != 2 && absInt(dz) != 2 {
				continue
			}
			for dy := 0; dy <= 1; dy++ {
				if s.isBookshelf(w, pos[0]+dx, pos[1]+dy, pos[2]+dz) &&
					s.isPowerTransmitter(w, pos[0]+dx/2, pos[1]+dy, pos[2]+dz/2) {
					count++
				}
			}
		}
	}
	return count
}

// isBookshelf 报告坐标处是否为书架（enchantment_power_provider 标签）。
func (s *Server) isBookshelf(w interface {
	BlockAt(x, y, z int) uint16
}, x, y, z int) bool {
	name, ok := registry.BlockNameByState(w.BlockAt(x, y, z))
	if !ok {
		return false
	}
	blockID, ok := registry.StaticEntryID("minecraft:block", name)
	return ok && registry.EnchantmentPowerProvider(blockID)
}

// isPowerTransmitter 报告坐标处是否为附魔能量传播方块
// （enchantment_power_transmitter 标签 = #replaceable）。
func (s *Server) isPowerTransmitter(w interface {
	BlockAt(x, y, z int) uint16
}, x, y, z int) bool {
	name, ok := registry.BlockNameByState(w.BlockAt(x, y, z))
	if !ok {
		return false
	}
	blockID, ok := registry.StaticEntryID("minecraft:block", name)
	return ok && registry.EnchantmentPowerTransmitter(blockID)
}

// enchantmentCost 复刻 EnchantmentHelper.getEnchantmentCost（dsq.a）：
// 书架上限 15；base = nextInt(8)+1 + 书架/2 + nextInt(书架+1)；
// 选项 0 为 base/3（至少 1）、选项 1 为 base*2/3+1、选项 2 取 base 与 书架*2 的较大值。
func enchantmentCost(rng *javaRandom, slot, bookshelves, value int32) int32 {
	if bookshelves > enchantingMaxBookshelves {
		bookshelves = enchantingMaxBookshelves
	}
	base := rng.nextInt(8) + 1 + (bookshelves >> 1) + rng.nextInt(bookshelves+1)
	switch slot {
	case 0:
		if base/3 < 1 {
			return 1
		}
		return base / 3
	case 1:
		return base*2/3 + 1
	default:
		if bookshelves*2 > base {
			return bookshelves * 2
		}
		return base
	}
}

// enchantingResults 复刻 EnchantmentMenu#getEnchantmentResults（dif 私有方法）：
// 以 seed+slot 重播随机源，用 EnchantmentHelper.selectEnchantment（dsq.b）按
// 成本计算候选附魔列表，最后处理附魔书的「至多保留一个附魔」特例。
func enchantingResults(rng *javaRandom, seed int64, stack item.Stack, slot, cost int32) []enchantmentInstance {
	value, ok := registry.EnchantableValue(stack.ItemID)
	if !ok {
		return nil
	}
	rng.setSeed(seed + int64(slot))

	// selectEnchantment：等级修正 + ±15% 波动。
	level := cost + 1 + rng.nextInt(value/4+1) + rng.nextInt(value/4+1)
	span := (rng.nextFloat() + rng.nextFloat() - 1.0) * 0.15
	level = int32(math.Round(float64(level) * (1 + float64(span))))
	if level < 1 {
		level = 1
	}

	list := availableEnchantmentsByLevel(level, stack)
	result := make([]enchantmentInstance, 0, 4)
	if len(list) > 0 {
		if inst, ok := weightedPick(rng, list); ok {
			result = append(result, inst)
		}
		// 每次成功选取后：只要 nextInt(50) <= level 就再选一个（等级减半）。
		for rng.nextInt(50) <= level {
			if len(result) > 0 {
				list = removeIncompatible(list, result[len(result)-1])
			}
			if len(list) == 0 {
				break
			}
			if inst, ok := weightedPick(rng, list); ok {
				result = append(result, inst)
			}
			level /= 2
		}
	}

	// 原版特例：普通书在附魔台至多保留……删随机一个。
	return bookEnchantmentTrim(rng, stack, result)
}

// bookEnchantmentTrim 对普通书应用原版特例（dif：list.size() > 1 时随机移除
// 一个附魔）：附魔结果数量超过 1 时随机移除一个。
func bookEnchantmentTrim(rng *javaRandom, stack item.Stack, result []enchantmentInstance) []enchantmentInstance {
	if stack.ItemID != enchantingBookItemID() || len(result) <= 1 {
		return result
	}
	idx := rng.nextInt(int32(len(result)))
	return append(result[:idx], result[idx+1:]...)
}

// availableEnchantmentsByLevel 复刻 EnchantmentHelper.getAvailableEnchantmentResults
// （dsq.a(int, ItemStack, Stream)）：按标签顺序遍历附魔台可选附魔，取
// 「从最高等级向下第一个满足 minCost ≤ level ≤ maxCost」的等级。
// 普通书（isBook）不受 supported_items 限制。
func availableEnchantmentsByLevel(level int32, stack item.Stack) []enchantmentInstance {
	isBook := stack.ItemID == enchantingBookItemID()
	var out []enchantmentInstance
	// 保持 in_enchanting_table 的标签定义顺序（影响加权随机的序列）。
	for _, id := range registry.EnchantmentsInEnchantingTable {
		if !isBook && !registry.EnchantmentSupported(id, stack.ItemID) {
			continue
		}
		maxLevel, ok := registry.EnchantmentMaxLevel(id)
		if !ok {
			continue
		}
		for lvl := maxLevel; lvl >= 1; lvl-- {
			minCost, maxCost, ok := registry.EnchantmentCostRange(id, lvl)
			if !ok {
				continue
			}
			if level >= minCost && level <= maxCost {
				out = append(out, enchantmentInstance{id: id, level: lvl})
				break
			}
		}
	}
	return out
}

// weightedPick 复刻 WeightedRandom.getRandomItem（权重 = 附魔 weight）。
func weightedPick(rng *javaRandom, list []enchantmentInstance) (enchantmentInstance, bool) {
	total := int32(0)
	for _, inst := range list {
		if weight, ok := registry.EnchantmentWeight(inst.id); ok {
			total += weight
		}
	}
	if total <= 0 {
		return enchantmentInstance{}, false
	}
	roll := rng.nextInt(total)
	for _, inst := range list {
		weight, _ := registry.EnchantmentWeight(inst.id)
		roll -= weight
		if roll < 0 {
			return inst, true
		}
	}
	return enchantmentInstance{}, false
}

// removeIncompatible 移除与 inst 不兼容的候选（原版 removeConflictFromList，
// 兼容判定为双向 exclusive_set 且排除自身）。
func removeIncompatible(list []enchantmentInstance, inst enchantmentInstance) []enchantmentInstance {
	kept := list[:0]
	for _, other := range list {
		if registry.EnchantmentCompatible(other.id, inst.id) {
			kept = append(kept, other)
		}
	}
	return kept
}

// handleEnchantingButton 处理附魔台的选项按钮（原版 EnchantmentMenu#clickMenuButton）。
// 由会话读循环调用。
func (s *Server) handleEnchantingButton(player *session, buttonID int32) {
	state := player.getEnchanting()
	if state == nil || buttonID < 0 || buttonID >= enchantingOptionCount {
		return
	}
	state.mu.Lock()
	input := state.inputs[enchantingInputSlot]
	lapis := state.inputs[enchantingLapisSlot]
	cost := state.costs[buttonID]
	state.mu.Unlock()

	costID := buttonID + 1
	creative := player.gameModeID() == uint8(config.GameModeCreative)
	// 青金石数量不足（创造模式除外）。
	if !lapis.IsEmpty() && lapis.Count < costID && !creative {
		return
	}
	_, level, _ := player.experienceStatus()
	// 原版：成本有效、输入非空、且（等级 ≥ costID 且等级 ≥ 成本）或创造模式。
	if cost <= 0 || input.IsEmpty() {
		return
	}
	if !creative && (level < costID || level < cost) {
		return
	}
	s.applyEnchantment(player, state, buttonID, costID)
}

// applyEnchantment 执行附魔（原版 clickMenuButton 的 access.execute 回调）：
// 重算附魔列表（空则完全不生效）、扣等级与重播种子、写附魔、消耗青金石。
// 由会话读循环调用。
func (s *Server) applyEnchantment(player *session, state *enchantingState, slot, costID int32) {
	state.mu.Lock()
	input := state.inputs[enchantingInputSlot]
	lapis := state.inputs[enchantingLapisSlot]
	cost := state.costs[slot]
	seed := state.seed
	state.mu.Unlock()

	rng := &javaRandom{}
	list := enchantingResults(rng, int64(seed), input, slot, cost)
	if len(list) == 0 {
		return
	}

	// 扣 costID 级并把玩家种子重播（原版 Player#onEnchantmentPerformed）。
	if player.gameModeID() != uint8(config.GameModeCreative) {
		bar, level, total := player.giveExperienceLevels(-costID)
		player.tryWrite(protocol.EncodeSetExperience(bar, level, total))
	}
	player.setEnchantmentSeed(s.newEnchantmentSeed())

	// 普通书变成附魔书（原版：new ItemStack(Items.ENCHANTED_BOOK)）。
	if input.ItemID == enchantingBookItemID() {
		if book, err := item.FromName("minecraft:enchanted_book", input.Count); err == nil {
			input = book
		}
	}
	for _, inst := range list {
		input = input.WithEnchantment(inst.id, inst.level)
	}

	// 消耗青金石（原版 lapis.shrink(costID)；耗尽则清空槽位）。
	lapis.Count -= costID
	if lapis.Count <= 0 {
		lapis = item.Empty()
	}

	state.mu.Lock()
	state.inputs[enchantingInputSlot] = input
	state.inputs[enchantingLapisSlot] = lapis
	state.seed = player.getEnchantmentSeed()
	state.mu.Unlock()

	s.sendEnchantingSlot(player, enchantingInputSlot, input)
	s.sendEnchantingSlot(player, enchantingLapisSlot, lapis)
	// 用新种子重算选项（原版：setChanged 后显式再调 slotsChanged）。
	s.refreshEnchantingOptions(state)
	s.sendEnchantingProperties(player, state)
	s.playEnchantingSound(state)
}

// newEnchantmentSeed 生成玩家附魔种子的新值（原版 = player.random.nextInt()。
// gmcs 使用服务器级随机源，语义同为均匀 32 位整数）。
func (s *Server) newEnchantmentSeed() int32 {
	return int32(s.nextRandom())
}

// playEnchantingSound 向附魔台附近玩家播放方块使用音效（原版：
// block.enchantment_table.use，音量 1，音高 0.9–1.0 随机）。
func (s *Server) playEnchantingSound(state *enchantingState) {
	soundID, ok := registry.StaticEntryID("minecraft:sound_event", "minecraft:block.enchantment_table.use")
	if !ok {
		return
	}
	state.mu.Lock()
	dim, pos := state.dim, state.pos
	state.mu.Unlock()
	pitch := 0.9 + float64(s.nextRandom()%100)/1000
	centerX := float64(pos[0]) + 0.5
	centerY := float64(pos[1]) + 0.5
	centerZ := float64(pos[2]) + 0.5
	packet := protocol.EncodeSoundEffect(soundID, protocol.SoundCategoryBlock,
		centerX, centerY, centerZ, 1, float32(pitch), 0)
	for _, other := range s.playerSnapshot() {
		if !other.isJoined() || other.dimensionID() != dim {
			continue
		}
		px, _, pz, _, _ := other.playerPosition()
		if math.Hypot(px-centerX, pz-centerZ) > mobMoveBroadcastRange {
			continue
		}
		other.tryWrite(packet)
	}
}

// enchantingSlotBacking 是附魔台窗口的槽位访问层。
type enchantingSlotBacking struct {
	player *session
	state  *enchantingState
}

// SlotCount 返回窗口槽位总数（2 个容器槽 + 玩家背包 + 快捷栏）。
func (b enchantingSlotBacking) SlotCount() int { return enchantingSlots }

// Get 返回槽位内容。
func (b enchantingSlotBacking) Get(index int) item.Stack {
	if index < 2 {
		return b.state.inputStack(index)
	}
	return b.player.inventory.Get(containerPlayerInvSlot(index - 2))
}

// Set 设置槽位内容。
func (b enchantingSlotBacking) Set(index int, stack item.Stack) {
	if index < 2 {
		b.state.setInput(index, stack)
		return
	}
	b.player.inventory.Set(containerPlayerInvSlot(index-2), stack)
}

// Supported 报告槽位是否可交互（附魔台全部槽位可交互）。
func (b enchantingSlotBacking) Supported(int) bool { return true }

// CanPlace 报告物品能否放入槽位（原版 Slot.mayPlace）：
// 青金石槽只接受青金石；输入槽接受任意物品（有效性与否由选项计算体现）。
func (b enchantingSlotBacking) CanPlace(slot int, stack item.Stack) bool {
	if slot == enchantingLapisSlot {
		return stack.ItemID == enchantingLapisItemID()
	}
	return true
}

// SlotLimit 报告槽位的堆叠数量上限（原版 Slot.getMaxStackSize）：
// 输入槽为 1（dif$2），其余不限。
func (b enchantingSlotBacking) SlotLimit(slot int) int32 {
	if slot == enchantingInputSlot {
		return 1
	}
	return 0
}
