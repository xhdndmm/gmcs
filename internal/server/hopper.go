//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"log/slog"
	"slices"
	"sync"

	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 漏斗（Hopper）的物品传输。
//
// 语义对照 1.21.11 官方 mappings 反编译的 HopperBlockEntity / Hopper 接口
// （javap 字节码逐方法确认）：
//
//   - 每游戏刻：传送冷却 -1；冷却 <= 0 时清零并尝试一次搬运（tryMoveItems）；
//     被红石锁定的漏斗（方块属性 enabled=false）不推不吸（但仍可被其他
//     漏斗/投掷器输入、也可被其他漏斗抽取）；
//   - 推出（ejectItems）：本漏斗非空时，把最左非空槽的 1 个物品插入朝向容器
//     （原版 getAttachedContainer = 朝向前方一格），插入方向为“朝向的反方向”；
//     插入按目标的面规则（getSlotsForFace）与槽位过滤（canPlaceItem，
//     如熔炉：上方只能进原料槽、侧面只能进燃料槽、产物槽不可插入）；
//     成功 → 冷却 = 8；目标为漏斗且插入前该漏斗完全为空时，按原版错峰规则
//     同时设置目标冷却：目标最近搬运时刻不早于本漏斗时取 7（8-1），否则 8；
//   - 吸入（suckItemsIntoHopper）：本漏斗未满时，优先从上方容器沿 DOWN 面规则
//     抽 1 个（含 canTakeItemThroughFace：漏斗从下方抽取熔炉燃料槽时不可抽走
//     空桶/水桶等）；上方没有容器且未被“碰撞形状为完整方块”的方块挡住时
//     （原版 does_not_block_hoppers 标签例外：蜂巢/蜂箱），收集吸入盒内的
//     掉落物（整个堆叠，放入最左可接受槽），一次只吸收一个实体；
//   - 推出与吸入互不排斥：同一 tick 内可以既推出又吸入（结果取或）；
//   - 吸入盒：方块坐标 (0, 11/16, 0)–(1, 2, 1)（原版 SUCK_AABB = Block.box(16,11,32)），
//     覆盖漏斗内腔与上方一格；
//   - 离开区块（未加载）的漏斗不参与 Tick；新加载的区块按增量扫描注册其中的
//     漏斗（含尚无方块实体记录的空漏斗），卸载时丢弃注册，另有低频全量扫描兜底。
//
// 已知限制（见 docs/TODO.md）：
//   - 传送冷却随方块实体持久化（原版 TransferCooldown）；
//   - 不实现实体容器（漏斗矿车/箱子矿车/箱子船）与未实现的处理型容器
//     （酿造台/堆肥桶等）；
//   - 漏斗被破坏时按容器处理（掉落内容），不实现“物品留在原地的特殊顺序”；
//   - 掉落物收集顺序按实体 ID 升序（原版按区块实体列表顺序）。

// 漏斗常量（与原版一致）。
const (
	// hopperBlockName 是漏斗的方块名。
	hopperBlockName = "minecraft:hopper"
	// hopperSlots 是漏斗槽位数（原版 HOPPER_CONTAINER_SIZE = 5）。
	hopperSlots = 5
	// hopperMoveItemSpeed 是一次成功搬运后的冷却（原版 MOVE_ITEM_SPEED = 8 tick）。
	hopperMoveItemSpeed = 8
	// hopperChainedCooldown 是向“刚搬运过的漏斗”推送时目标获得的冷却
	// （原版 custom cooldown：8 - 1 = 7）。
	hopperChainedCooldown = 7
	// hopperSuctionMinY 是吸入盒下沿（原版 SUCK_AABB 的 11/16）。
	hopperSuctionMinY = 11.0 / 16
	// hopperSuctionMaxY 是吸入盒上沿（原版 SUCK_AABB 的 32/16）。
	hopperSuctionMaxY = 2.0
	// hopperFullScanInterval 是全量扫描的间隔（tick）。日常注册走“新加载区块”
	// 增量路径，全量扫描仅作低频兜底（覆盖命令放置等非交互路径）。
	hopperFullScanInterval = 600
)

// hopperState 是一个漏斗方块的运行时状态（槽位 + 传送冷却）。
type hopperState struct {
	mu     sync.Mutex
	dim    world.Dimension
	pos    [3]int
	typeID int32
	slots  [hopperSlots]item.Stack
	// cooldown 是剩余传送冷却（原版 cooldownTime）。
	cooldown int
	// lastTick 是最近一次 Tick 的游戏刻（原版 tickedGameTime，用于错峰规则）。
	lastTick int64
}

// resolveHopperRegistryIDs 解析漏斗需要的注册表 ID；缺项时禁用漏斗传输。
func (s *Server) resolveHopperRegistryIDs() {
	typeID, ok := registry.StaticEntryID("minecraft:block_entity_type", hopperBlockName)
	if !ok {
		slog.Warn("缺少漏斗方块实体类型注册表项，漏斗传输已禁用")
		return
	}
	s.hopperTypeID = typeID
	s.hoppersEnabled = true
}

// registerHopper 注册（或返回已有的）漏斗运行时状态；方块实体已有内容时载入。
func (s *Server) registerHopper(dim world.Dimension, x, y, z int) *hopperState {
	key := containerKey{dim: dim, x: x, y: y, z: z}
	s.hopperMu.Lock()
	if state, ok := s.hoppers[key]; ok {
		s.hopperMu.Unlock()
		return state
	}
	s.hopperMu.Unlock()

	w := s.worldFor(dim)
	entity, err := w.BlockEntityAt(x, y, z)
	if err != nil {
		return nil
	}
	state := &hopperState{
		dim:    dim,
		pos:    [3]int{x, y, z},
		typeID: s.hopperTypeID,
		// 传送冷却随方块实体持久化（原版 TransferCooldown）。
		cooldown: int(entity.TransferCooldown),
	}
	for i := range state.slots {
		if i < len(entity.Items) {
			state.slots[i] = stackFromContainer(entity.Items[i])
		}
	}

	s.hopperMu.Lock()
	defer s.hopperMu.Unlock()
	if existing, ok := s.hoppers[key]; ok {
		return existing
	}
	s.hoppers[key] = state
	return state
}

// flushHopper 把漏斗槽位与传送冷却写回方块实体。
func (s *Server) flushHopper(state *hopperState) {
	state.mu.Lock()
	entity := s.hopperBlockEntityLocked(state)
	dim, pos := state.dim, state.pos
	state.mu.Unlock()

	if err := s.worldFor(dim).SetBlockEntity(pos[0], pos[1], pos[2], entity); err != nil {
		slog.Error("写回漏斗内容失败", "x", pos[0], "y", pos[1], "z", pos[2], "error", err)
	}
}

// syncHopperCooldown 把当前传送冷却就地写回方块实体（不重建槽位数组，
// 避免每 tick 的分配）；冷却归零且槽位已空时方块实体记录随之删除。
func (s *Server) syncHopperCooldown(state *hopperState) {
	state.mu.Lock()
	cooldown := int32(state.cooldown)
	dim, pos := state.dim, state.pos
	state.mu.Unlock()
	gameWorld := s.worldFor(dim)
	if gameWorld == nil {
		return
	}
	// 记录不存在时无需创建：冷却随上一次搬运写入的方块实体已经落盘，
	// 冷却归零时由世界层删除已空的记录。
	gameWorld.SetTransferCooldown(pos[0], pos[1], pos[2], cooldown)
}

// hopperBlockEntityLocked 构造漏斗的方块实体数据；调用方持有 state.mu。
func (s *Server) hopperBlockEntityLocked(state *hopperState) world.BlockEntity {
	entity := world.BlockEntity{
		TypeID:           state.typeID,
		Items:            make([]world.ContainerItem, hopperSlots),
		TransferCooldown: int32(state.cooldown),
	}
	for i, stack := range state.slots {
		entity.Items[i] = containerItemFromStack(stack)
	}
	return entity
}

// destroyHopper 处理漏斗被破坏：移除运行时状态后按普通容器掉落内容。
//
// 这里不持有 containerTransferMu：搬运事务只会在“目标为箱子类容器”时取它，
// 而 Tick 路径在持有漏斗锁的同时可能取 containerTransferMu（向箱子推送），
// 若这里反向持锁再取漏斗锁会构成锁序反转。
func (s *Server) destroyHopper(dim world.Dimension, breaker *session, x, y, z int, def *containerDef) {
	key := containerKey{dim: dim, x: x, y: y, z: z}
	s.hopperMu.Lock()
	state := s.hoppers[key]
	delete(s.hoppers, key)
	s.hopperMu.Unlock()
	if state != nil {
		// 每次变更都已写回方块实体；这里补一次写回确保掉落读取的是最新内容。
		s.flushHopper(state)
	}
	s.destroyContainer(dim, breaker, x, y, z, def)
}

// tickHoppers 每 tick 推进全部已注册漏斗，并处理区块加载/卸载带来的注册表变化：
// 新加载进内存的区块立即注册其中的漏斗（每 tick 消费一次，成本与新增区块数成正比）；
// 每 hopperFullScanInterval tick 做一次全量扫描兜底（覆盖命令放置等非交互路径，
// 以及极端情况下加载跟踪列表溢出导致的遗漏）。
func (s *Server) tickHoppers() {
	if !s.hoppersEnabled {
		return
	}
	s.hopperScanCounter++
	fullScan := false
	if s.hopperScanCounter >= hopperFullScanInterval {
		s.hopperScanCounter = 0
		fullScan = true
	}
	if fullScan {
		s.scanHoppers()
	} else {
		s.registerLoadedChunkHoppers()
	}
	s.hopperMu.Lock()
	states := make([]*hopperState, 0, len(s.hoppers))
	for _, state := range s.hoppers {
		states = append(states, state)
	}
	s.hopperMu.Unlock()
	// map 迭代顺序随机：按位置排序保证推进顺序确定（原版按区块/位置顺序推进）。
	if len(states) > 1 {
		slices.SortFunc(states, compareHopperState)
	}
	for _, state := range states {
		switch s.tickHopper(state) {
		case hopperTickChanged:
			s.flushHopper(state)
		case hopperTickCooldown:
			s.syncHopperCooldown(state)
		}
	}
}

// hopperTickResult 描述一次漏斗 Tick 造成的状态变更。
type hopperTickResult uint8

const (
	// hopperTickIdle：没有状态变化。
	hopperTickIdle hopperTickResult = iota
	// hopperTickCooldown：仅传送冷却变化（就地同步，不重建槽位数组）。
	hopperTickCooldown
	// hopperTickChanged：槽位发生搬运，需要整体写回。
	hopperTickChanged
)

// compareHopperState 给出漏斗推进的确定顺序（维度 → Y → X → Z）。
// 维度与坐标在注册后不变，无需加锁。
func compareHopperState(a, b *hopperState) int {
	if a.dim != b.dim {
		return int(a.dim) - int(b.dim)
	}
	for _, axis := range [3]int{1, 0, 2} {
		if a.pos[axis] != b.pos[axis] {
			return a.pos[axis] - b.pos[axis]
		}
	}
	return 0
}

// loadedWorld 是已打开世界（维度 + 世界）的快照项。
type loadedWorld struct {
	dim   world.Dimension
	world *world.World
}

// knownWorlds 返回当前已打开世界的快照。
func (s *Server) knownWorlds() []loadedWorld {
	s.worldMu.Lock()
	defer s.worldMu.Unlock()
	worlds := make([]loadedWorld, 0, len(s.worlds))
	for dim, w := range s.worlds {
		worlds = append(worlds, loadedWorld{dim: dim, world: w})
	}
	return worlds
}

// registerLoadedChunkHoppers 只扫描自上次调用以来新加载进内存的区块，
// 注册其中的漏斗（含尚无方块实体记录的空漏斗）。
func (s *Server) registerLoadedChunkHoppers() {
	for _, loaded := range s.knownWorlds() {
		for _, pos := range loaded.world.TakeLoadedChunks() {
			for _, block := range loaded.world.ChunkBlockStatePositions(pos.X, pos.Z, isHopperState) {
				s.registerHopper(loaded.dim, block[0], block[1], block[2])
			}
		}
	}
}

// scanHoppers 全量扫描已加载区块中的漏斗方块并注册（低频兜底路径）。
func (s *Server) scanHoppers() {
	for _, loaded := range s.knownWorlds() {
		for _, pos := range loaded.world.BlockStatePositions(isHopperState) {
			s.registerHopper(loaded.dim, pos[0], pos[1], pos[2])
		}
	}
}

// dropHoppersInChunks 丢弃已从内存卸载的区块中的漏斗注册。
// 卸载前世界已把修改写盘（flushHopper 在每次变更后写回方块实体），
// 因此这里不需要（也不能）再写回：写回会重新加载刚卸载的区块。
func (s *Server) dropHoppersInChunks(dim world.Dimension, unloaded []world.ChunkPos) {
	if !s.hoppersEnabled || len(unloaded) == 0 {
		return
	}
	type chunkKey [2]int
	keys := make(map[chunkKey]struct{}, len(unloaded))
	for _, pos := range unloaded {
		keys[chunkKey{pos.X, pos.Z}] = struct{}{}
	}
	s.hopperMu.Lock()
	for key := range s.hoppers {
		if key.dim != dim {
			continue
		}
		chunk := chunkKey{key.x >> 4, key.z >> 4}
		if _, ok := keys[chunk]; ok {
			delete(s.hoppers, key)
		}
	}
	s.hopperMu.Unlock()
}

// isHopperState 报告方块状态是否为漏斗。
func isHopperState(state uint16) bool {
	return registry.IsPropertyBlockState(hopperBlockName, state)
}

// tickHopper 推进单个漏斗一 tick；返回本次 tick 的状态变更类型。
func (s *Server) tickHopper(state *hopperState) hopperTickResult {
	w := s.worldFor(state.dim)
	if w == nil {
		return hopperTickIdle
	}
	pos := state.pos
	if !w.ChunkLoaded(pos[0], pos[2]) {
		return hopperTickIdle // 未加载区块中的漏斗冻结（方块的方块实体随区块一起休眠）
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	// 原版 pushItemsTick：先递减冷却并记录游戏刻，再判断是否搬运。
	previousCooldown := state.cooldown
	state.cooldown--
	state.lastTick = s.gameTicks.Load()
	if state.cooldown > 0 {
		// 冷却值变化需要持久化（原版 TransferCooldown），这里只就地更新该字段。
		return hopperTickCooldown
	}
	state.cooldown = 0
	// 冷却归零后命中失败时仍需写回一次方块实体（值由正变零）。
	cooldownExpired := previousCooldown > 0

	blockState := w.BlockAt(pos[0], pos[1], pos[2])
	name, props, ok := registry.StateProps(blockState)
	if !ok || name != hopperBlockName {
		if cooldownExpired {
			return hopperTickCooldown
		}
		return hopperTickIdle // 方块已被替换（状态清理由破坏/扫描负责）
	}
	if props["enabled"] != "true" {
		if cooldownExpired {
			return hopperTickCooldown
		}
		return hopperTickIdle // 被红石锁定：不推不吸
	}

	moved := false
	if !hopperEmpty(state) {
		moved = s.hopperPush(state, props["facing"])
	}
	// 原版两者不互斥：推出成功后仍会尝试吸入（结果取或）。
	if !hopperFull(state) {
		moved = s.hopperSuck(state, w) || moved
	}
	if !moved {
		if cooldownExpired {
			return hopperTickCooldown
		}
		return hopperTickIdle
	}
	state.cooldown = hopperMoveItemSpeed
	return hopperTickChanged
}

// hopperEmpty 报告漏斗是否为空。
func hopperEmpty(state *hopperState) bool {
	for _, stack := range state.slots {
		if !stack.IsEmpty() {
			return false
		}
	}
	return true
}

// hopperFull 报告漏斗是否已满（所有槽位都装满；原版 isFull）。
func hopperFull(state *hopperState) bool {
	for _, stack := range state.slots {
		if stack.IsEmpty() || stack.Count != stack.MaxStack() {
			return false
		}
	}
	return true
}

// hopperPush 把漏斗最左非空槽的 1 个物品推入朝向容器（原版 ejectItems）。
// 调用方持有 state.mu；返回是否搬运成功。
func (s *Server) hopperPush(state *hopperState, facing string) bool {
	target := s.acquireTransferTarget(state.dim, offsetDir(state.pos, facing))
	if target == nil {
		return false // 朝向不是容器：原版不向世界喷出物品
	}
	defer target.close()
	direction := oppositeDir(facing)
	if target.full(direction) {
		return false
	}
	// 原版错峰冷却只在“目标容器插入前完全为空”时设置（isEmpty 在插入前取值）。
	targetWasEmpty := target.isEmpty()
	for i := 0; i < hopperSlots; i++ {
		stack := state.slots[i]
		if stack.IsEmpty() {
			continue
		}
		taken := stack
		taken.Count = 1
		leftover := target.insert(taken, direction)
		if !leftover.IsEmpty() {
			continue // 该槽位物品放不进去：尝试下一个槽位
		}
		stack.Count--
		if stack.Count <= 0 {
			state.slots[i] = item.Empty()
		} else {
			state.slots[i] = stack
		}
		// 目标为漏斗且插入前为空时，按原版错峰规则设置目标冷却。
		if target.kind == transferHopper && targetWasEmpty {
			target.setHopperCooldown(state.lastTick)
		}
		target.commit()
		return true
	}
	return false
}

// hopperSuck 从上方容器抽 1 个物品、或收集吸入盒内的掉落物
// （原版 suckItemsIntoHopper）。调用方持有 state.mu；返回是否搬运成功。
func (s *Server) hopperSuck(state *hopperState, w *world.World) bool {
	pos := state.pos
	above := [3]int{pos[0], pos[1] + 1, pos[2]}
	target := s.acquireTransferTarget(state.dim, above)
	if target != nil {
		defer target.close()
		for _, index := range target.slotsForFace("down") {
			stack := target.items[index]
			if stack.IsEmpty() || !target.canTake(index, stack) {
				continue
			}
			slot, ok := hopperAcceptSlot(state, stack)
			if !ok {
				continue // 漏斗放不下这种物品：尝试下一个槽位（原版行为）
			}
			taken := stack
			taken.Count = 1
			target.removeOne(index)
			hopperInsertAt(state, slot, taken)
			target.commit()
			return true
		}
		return false
	}
	// 上方没有容器：被完整方块挡住时（蜂巢/蜂箱除外）不收集掉落物。
	if s.hopperSuctionBlocked(w, above) {
		return false
	}
	return s.collectItemEntities(state, w)
}

// hopperSuctionBlocked 报告上方方块是否阻挡掉落物收集（原版 suckInItems：
// `isCollisionShapeFullBlock` 即碰撞形状为完整方块，且不在
// does_not_block_hoppers 标签（1.21.11 = #minecraft:beehives）中）。
func (s *Server) hopperSuctionBlocked(w *world.World, above [3]int) bool {
	state := w.BlockAt(above[0], above[1], above[2])
	name, ok := s.blockNameOf(state)
	if !ok {
		return false
	}
	if name == "minecraft:bee_nest" || name == "minecraft:beehive" {
		return false
	}
	return fullCubeState(state)
}

// hopperSuctionBox 返回漏斗的吸入盒（世界坐标；原版 SUCK_AABB = box(16, 11, 32)）。
func hopperSuctionBox(x, y, z int) world.Box {
	return world.Box{
		MinX: float64(x), MinY: float64(y) + hopperSuctionMinY, MinZ: float64(z),
		MaxX: float64(x) + 1, MaxY: float64(y) + hopperSuctionMaxY, MaxZ: float64(z) + 1,
	}
}

// itemCandidate 是吸入盒内候选掉落物的快照（实体 ID + 坐标）。
type itemCandidate struct {
	id      int32
	x, y, z float64
}

// collectItemEntities 收集吸入盒内的掉落物（原版 getItemsAtAndAbove +
// addItem(Container, ItemEntity)）：整堆尝试放入漏斗，放不下的部分留在实体上。
// 原版按区块实体列表顺序逐个尝试；本实现按实体 ID 升序（创建顺序的近似）保证确定性。
// 调用方持有 state.mu；返回是否至少收集到物品。
func (s *Server) collectItemEntities(state *hopperState, w *world.World) bool {
	if !s.itemsEnabled {
		return false
	}
	pos := state.pos
	suction := hopperSuctionBox(pos[0], pos[1], pos[2])
	var candidates []itemCandidate
	s.entityMu.Lock()
	for id, entity := range s.items {
		if entity.Dim != state.dim || entity.Stack.IsEmpty() {
			continue
		}
		if !boxesOverlap(suction, itemBoxAt(entity.X, entity.Y, entity.Z)) {
			continue
		}
		candidates = append(candidates, itemCandidate{id: id, x: entity.X, y: entity.Y, z: entity.Z})
	}
	if len(candidates) > 1 {
		slices.SortStableFunc(candidates, func(a, b itemCandidate) int { return int(a.id - b.id) })
	}
	// 按顺序尝试：吸收能装下的第一个实体（原版逐个尝试直到成功）。
	var (
		absorbed  bool
		entityID  int32
		leftover  item.Stack
		remainder item.Stack
	)
	for _, candidate := range candidates {
		entity := s.items[candidate.id]
		if entity == nil || entity.Stack.IsEmpty() {
			continue
		}
		copy := entity.Stack
		remainder = hopperInsert(state, copy)
		if remainder.Count == copy.Count {
			continue // 漏斗完全放不下：跳过并尝试下一个实体（原版行为）
		}
		entity.Stack = remainder
		absorbed, entityID, leftover = true, candidate.id, remainder
		break // 原版 tick 路径一次只吸收一个实体
	}
	s.entityMu.Unlock()
	if !absorbed {
		return false
	}
	x, z := itemCandidatePos(candidates, entityID)
	if leftover.IsEmpty() {
		s.writeToNearbyPlayers(protocol.EncodeEntityDestroy([]int32{entityID}), state.dim, x, z, nil)
	} else {
		s.writeToNearbyPlayers(protocol.EncodeEntityMetadataItem(entityID, leftover.AppendSlot(nil)), state.dim, x, z, nil)
	}
	return true
}

// itemCandidatePos 返回候选列表中指定实体的水平坐标（广播定位用）。
func itemCandidatePos(candidates []itemCandidate, id int32) (x, z float64) {
	for _, candidate := range candidates {
		if candidate.id == id {
			return candidate.x, candidate.z
		}
	}
	return 0, 0
}

// hopperAcceptSlot 返回漏斗接受该物品的槽位（原版 addItem：按槽位顺序取
// 第一个“空槽或同类且未满”的槽位，即最左侧可用槽）。
func hopperAcceptSlot(state *hopperState, stack item.Stack) (int, bool) {
	for i, existing := range state.slots {
		if existing.IsEmpty() {
			return i, true
		}
		if existing.SameKind(stack) && existing.Count < existing.MaxStack() {
			return i, true
		}
	}
	return 0, false
}

// hopperInsertAt 把 1 个物品放入指定槽位。
func hopperInsertAt(state *hopperState, slot int, stack item.Stack) {
	existing := state.slots[slot]
	if existing.IsEmpty() {
		state.slots[slot] = stack
		return
	}
	existing.Count += stack.Count
	state.slots[slot] = existing
}

// hopperInsert 把整个堆叠尽可能放入漏斗（原版 addItem(Container, ItemStack)），
// 返回未能放入的剩余。
func hopperInsert(state *hopperState, stack item.Stack) item.Stack {
	for i := range state.slots {
		if stack.IsEmpty() {
			break
		}
		existing := state.slots[i]
		if existing.IsEmpty() {
			state.slots[i] = stack
			return item.Empty()
		}
		if !existing.SameKind(stack) {
			continue
		}
		limit := existing.MaxStack()
		if existing.Count >= limit {
			continue
		}
		move := min(limit-existing.Count, stack.Count)
		existing.Count += move
		state.slots[i] = existing
		stack.Count -= move
	}
	return stack
}

// 传输目标：搬运事务。

// transferKind 是搬运目标的类型（决定面规则与插入/抽取许可）。
type transferKind uint8

const (
	// transferChest 是箱子类容器（全部槽位、无方向限制）。
	transferChest transferKind = iota
	// transferHopper 是漏斗。
	transferHopper
	// transferFurnace 是熔炉类（上方只能进原料、侧面只能进燃料、产物槽只出不进）。
	transferFurnace
	// transferBrewing 是酿造台（上方只能进材料；侧面/下方可抽瓶子，
	// 下方还可抽材料槽的玻璃瓶）。
	transferBrewing
	// transferComposterInput 是堆肥桶输入容器（level < 7）：只有上方可插入
	// 可堆肥物品（原版 ComposterBlock.InputContainer）。
	transferComposterInput
	// transferComposterOutput 是堆肥桶产出容器（level == 8）：只有下方可抽出
	// 骨粉（原版 ComposterBlock.OutputContainer）。
	transferComposterOutput
)

// transferSlotIndices 是槽位下标表（供箱子类/漏斗返回全部槽位）。
var transferSlotIndices = func() [27]int {
	var table [27]int
	for i := range table {
		table[i] = i
	}
	return table
}()

// transferTarget 是一次搬运事务的目标视图：
// 事务开始时复制槽位内容，修改后由 commit 写回（按改动槽位增量应用），
// 从而避免与玩家窗口点击并发时互相覆盖。
type transferTarget struct {
	kind  transferKind
	items []item.Stack
	// touched 记录被修改的槽位下标。
	touched []int
	// inner 是具体目标的提交逻辑。
	inner transferCommit
}

// transferCommit 是具体搬运目标的提交接口。
type transferCommit interface {
	// apply 把改动槽位写回目标并广播。
	apply(target *transferTarget)
	// setHopperCooldown 按原版错峰规则设置“目标为漏斗”时的冷却。
	setHopperCooldown(sourceTick int64)
	// close 释放事务资源（锁/串行化令牌）。
	close()
}

// close 释放事务资源；commit 之后调用（幂等由具体实现保证）。
func (t *transferTarget) close() {
	if t.inner != nil {
		t.inner.close()
		t.inner = nil
	}
}

// setHopperCooldown 按原版错峰规则设置“目标为漏斗”时的冷却。
func (t *transferTarget) setHopperCooldown(sourceTick int64) {
	if t.inner != nil {
		t.inner.setHopperCooldown(sourceTick)
	}
}

// isEmpty 报告目标在事务开始时是否为空（原版 isEmpty，用于错峰冷却判定）。
func (t *transferTarget) isEmpty() bool {
	for _, stack := range t.items {
		if !stack.IsEmpty() {
			return false
		}
	}
	return true
}

// markTouched 记录改动槽位（去重）。
func (t *transferTarget) markTouched(index int) {
	for _, existing := range t.touched {
		if existing == index {
			return
		}
	}
	t.touched = append(t.touched, index)
}

// full 报告目标在给定方向上是否已满（原版 isFullContainer）。
func (t *transferTarget) full(dir string) bool {
	for _, index := range t.slotsForFace(dir) {
		stack := t.items[index]
		if stack.IsEmpty() || stack.Count < stack.MaxStack() {
			return false
		}
	}
	return true
}

// slotsForFace 返回沿方向 dir 允许插入/抽取的槽位（原版 getSlotsForFace）。
func (t *transferTarget) slotsForFace(dir string) []int {
	switch t.kind {
	case transferFurnace:
		switch dir {
		case "up":
			return []int{0}
		case "down":
			return []int{2, 1}
		default:
			return []int{1}
		}
	case transferBrewing:
		// 原版 BrewingStandBlockEntity.getSlotsForFace：上方只进材料，
		// 下方可抽三个瓶子与材料，其余面可抽瓶子与燃料。
		switch dir {
		case "up":
			return []int{3}
		case "down":
			return []int{0, 1, 2, 3}
		default:
			return []int{0, 1, 2, 4}
		}
	case transferComposterInput:
		// 原版 InputContainer.getSlotsForFace：只有上方（level == 7 时无槽位）。
		if dir == "up" && len(t.items) > 0 {
			return []int{0}
		}
		return nil
	case transferComposterOutput:
		// 原版 OutputContainer.getSlotsForFace：只有下方。
		if dir == "down" && len(t.items) > 0 {
			return []int{0}
		}
		return nil
	default:
		return transferSlotIndices[:len(t.items)]
	}
}

// canPlace 报告槽位是否可插入该物品（原版 canPlaceItem）。
func (t *transferTarget) canPlace(index int, stack item.Stack) bool {
	switch t.kind {
	case transferFurnace:
		switch index {
		case 1:
			// 燃料槽只接受燃料（原版 isFuel）。
			return registry.FuelBurnTicks(stack.ItemID) > 0
		case 2:
			return false // 产物槽不可插入
		}
		return true
	case transferBrewing:
		// 对照原版 BrewingStandBlockEntity.canPlaceItem。
		switch index {
		case 0, 1, 2:
			if !registry.BrewingIsContainerItem(stack.ItemID) && stack.ItemID != glassBottleItemID {
				return false
			}
			return t.items[index].IsEmpty()
		case 3:
			return registry.BrewingIsIngredient(stack.ItemID)
		case 4:
			return stack.ItemID == blazePowderItemID
		}
		return true
	case transferComposterInput:
		// 原版 InputContainer.canPlaceItem：只有上方、可堆肥物品。
		return index == 0 && registry.CompostChance(stack.ItemID) > 0
	case transferComposterOutput:
		// 原版 OutputContainer.canPlaceItem：恒 false（只能抽取）。
		return false
	}
	return true
}

// canTake 报告槽位是否可被抽出（原版 canTakeItemThroughFace；
// 漏斗从下方抽取熔炉燃料槽时，空桶/水桶等桶装物不可抽走；
// 从下方抽取酿造台材料槽时只允许玻璃瓶）。
func (t *transferTarget) canTake(index int, stack item.Stack) bool {
	if t.kind == transferComposterInput {
		// 原版 InputContainer.canTakeItemThroughFace：恒 false。
		return false
	}
	if t.kind == transferComposterOutput {
		// 原版 OutputContainer.canTakeItemThroughFace：只允许骨粉。
		return index == 0 && stack.ItemID == boneMealItemID()
	}
	if t.kind == transferBrewing {
		if index == 3 {
			return stack.ItemID == glassBottleItemID
		}
		return true
	}
	if t.kind != transferFurnace || index != 1 {
		return true
	}
	return !hopperProtectedBucket(stack.ItemID)
}

// hopperProtectedBucket 报告物品是否是“漏斗不可从熔炉燃料槽抽走”的桶装物
// （原版排除空桶、水桶与 1.21.11 新增的空桶变体；这里按名称保守判断）。
func hopperProtectedBucket(itemID int32) bool {
	name, ok := registry.ItemName(itemID)
	if !ok {
		return false
	}
	switch name {
	case "minecraft:bucket", "minecraft:water_bucket", "minecraft:air_bucket", "minecraft:milk_bucket":
		return true
	}
	return false
}

// insert 把 stack 尽可能插入目标（原版 addItem）：沿面槽位顺序先合并同类、
// 再放入空槽，返回未能放入的剩余。
func (t *transferTarget) insert(stack item.Stack, dir string) item.Stack {
	for _, index := range t.slotsForFace(dir) {
		if stack.IsEmpty() {
			break
		}
		existing := t.items[index]
		if existing.IsEmpty() {
			if !t.canPlace(index, stack) {
				continue
			}
			t.items[index] = stack
			t.markTouched(index)
			return item.Empty()
		}
		if !existing.SameKind(stack) || !t.canPlace(index, stack) {
			continue
		}
		limit := existing.MaxStack()
		if existing.Count >= limit {
			continue
		}
		move := min(limit-existing.Count, stack.Count)
		existing.Count += move
		t.items[index] = existing
		stack.Count -= move
		t.markTouched(index)
	}
	return stack
}

// removeOne 从槽位移除 1 个物品。
func (t *transferTarget) removeOne(index int) {
	stack := t.items[index]
	stack.Count--
	if stack.Count <= 0 {
		t.items[index] = item.Empty()
	} else {
		t.items[index] = stack
	}
	t.markTouched(index)
}

// commit 把改动写回目标并广播。
func (t *transferTarget) commit() {
	if t.inner == nil || len(t.touched) == 0 {
		return
	}
	t.inner.apply(t)
	t.touched = t.touched[:0]
}

// acquireTransferTarget 解析坐标处的可搬运容器（箱子类/漏斗/熔炉并注册运行时状态）。
// 非容器返回 nil。调用方必须调用返回值的 close（defer 即可）。
func (s *Server) acquireTransferTarget(dim world.Dimension, pos [3]int) *transferTarget {
	return s.acquireTransferTargetInternal(dim, pos, false)
}

// acquireTransferTargetLocked 与 acquireTransferTarget 相同，但假定调用方已持有
// containerTransferMu（例如发射器自身槽位的事务），箱子类目标不再重复加锁，
// 避免自锁（非可重入互斥锁）。
func (s *Server) acquireTransferTargetLocked(dim world.Dimension, pos [3]int) *transferTarget {
	return s.acquireTransferTargetInternal(dim, pos, true)
}

// acquireTransferTargetInternal 实现目标解析；locked 表示已持有 containerTransferMu。
func (s *Server) acquireTransferTargetInternal(dim world.Dimension, pos [3]int, locked bool) *transferTarget {
	w := s.worldFor(dim)
	if w == nil {
		return nil
	}
	state := w.BlockAt(pos[0], pos[1], pos[2])
	name, ok := s.blockNameOf(state)
	if !ok {
		return nil
	}
	switch {
	case name == hopperBlockName:
		hopper := s.registerHopper(dim, pos[0], pos[1], pos[2])
		if hopper == nil {
			return nil
		}
		hopper.mu.Lock()
		target := &transferTarget{
			kind:  transferHopper,
			items: append([]item.Stack(nil), hopper.slots[:]...),
			inner: &hopperTransferCommit{server: s, state: hopper},
		}
		hopper.mu.Unlock()
		return target
	case s.furnaceKindOfName(name):
		furnace := s.registerFurnace(dim, pos[0], pos[1], pos[2])
		if furnace == nil {
			return nil
		}
		furnace.mu.Lock()
		target := &transferTarget{
			kind:  transferFurnace,
			items: append([]item.Stack(nil), furnace.slots[:]...),
			inner: &furnaceTransferCommit{server: s, state: furnace},
		}
		furnace.mu.Unlock()
		return target
	case name == brewingStandBlockName:
		brewing := s.registerBrewing(dim, pos[0], pos[1], pos[2])
		if brewing == nil {
			return nil
		}
		brewing.mu.Lock()
		target := &transferTarget{
			kind:  transferBrewing,
			items: append([]item.Stack(nil), brewing.slots[:]...),
			inner: &brewingTransferCommit{server: s, state: brewing},
		}
		brewing.mu.Unlock()
		return target
	case name == composterBlockName:
		return s.acquireComposterTarget(dim, pos, state)
	default:
		def, known := s.containerDefs[name]
		if !known || def.IsFurnace {
			return nil
		}
		return s.acquireChestTarget(dim, pos, def, locked)
	}
}

// furnaceKindOfName 报告方块名是否为熔炉类（熔炉/高炉/烟熏炉）。
func (s *Server) furnaceKindOfName(name string) bool {
	def, ok := s.containerDefs[name]
	return ok && def.IsFurnace
}

// acquireChestTarget 以事务方式获取箱子类容器的槽位：
// 容器窗口打开时直接读写 containerState（与点击共用同一把锁），
// 否则读取方块实体副本并在提交时写回；locked 为 false 时用 containerTransferMu
// 与“打开窗口时从方块实体载入”的过程串行化（locked 为 true 时调用方已持有）。
func (s *Server) acquireChestTarget(dim world.Dimension, pos [3]int, def *containerDef, locked bool) *transferTarget {
	if !locked {
		s.containerTransferMu.Lock()
	}
	key := containerKey{dim: dim, x: pos[0], y: pos[1], z: pos[2]}
	s.containerMu.Lock()
	container := s.containers[key]
	s.containerMu.Unlock()
	if container != nil {
		container.mu.Lock()
		target := &transferTarget{
			kind:  transferChest,
			items: append([]item.Stack(nil), container.slots...),
			inner: &chestTransferCommit{server: s, container: container, pos: pos, def: def, locked: locked},
		}
		container.mu.Unlock()
		return target
	}
	entity, err := s.worldFor(dim).BlockEntityAt(pos[0], pos[1], pos[2])
	if err != nil {
		if !locked {
			s.containerTransferMu.Unlock()
		}
		return nil
	}
	slots := make([]item.Stack, def.Slots)
	for i := range slots {
		if i < len(entity.Items) {
			slots[i] = stackFromContainer(entity.Items[i])
		}
	}
	return &transferTarget{
		kind:  transferChest,
		items: slots,
		inner: &chestEntityTransferCommit{server: s, dim: dim, pos: pos, def: def, locked: locked},
	}
}

// hopperTransferCommit 提交对漏斗目标的事务。
type hopperTransferCommit struct {
	server *Server
	state  *hopperState
}

func (c *hopperTransferCommit) apply(target *transferTarget) {
	c.state.mu.Lock()
	for _, index := range target.touched {
		if index < len(c.state.slots) {
			c.state.slots[index] = target.items[index]
		}
	}
	c.state.mu.Unlock()
	c.server.flushHopper(c.state)
	c.server.refreshHopperWindow(c.state)
}

func (c *hopperTransferCommit) setHopperCooldown(sourceTick int64) {
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	if c.state.cooldown > hopperMoveItemSpeed {
		return // 已有“自定义冷却”（原版 isOnCustomCooldown）
	}
	if c.state.lastTick >= sourceTick {
		c.state.cooldown = hopperChainedCooldown
	} else {
		c.state.cooldown = hopperMoveItemSpeed
	}
}

func (c *hopperTransferCommit) close() {}

// furnaceTransferCommit 提交对熔炉目标的事务。
type furnaceTransferCommit struct {
	server *Server
	state  *furnaceState
}

func (c *furnaceTransferCommit) apply(target *transferTarget) {
	c.state.mu.Lock()
	for _, index := range target.touched {
		if index < len(c.state.slots) {
			c.state.slots[index] = target.items[index]
		}
	}
	c.state.mu.Unlock()
	c.server.flushFurnace(c.state)
	c.server.refreshFurnaceWindow(c.state)
}

func (c *furnaceTransferCommit) setHopperCooldown(int64) {}

func (c *furnaceTransferCommit) close() {}

// brewingTransferCommit 提交对酿造台目标的事务。
type brewingTransferCommit struct {
	server *Server
	state  *brewingState
}

func (c *brewingTransferCommit) apply(target *transferTarget) {
	c.state.mu.Lock()
	for _, index := range target.touched {
		if index < len(c.state.slots) {
			c.state.slots[index] = target.items[index]
		}
	}
	c.state.dirty = true
	c.state.mu.Unlock()
	c.server.flushBrewing(c.state)
	c.server.refreshBrewingWindow(c.state)
}

func (c *brewingTransferCommit) setHopperCooldown(int64) {}

func (c *brewingTransferCommit) close() {}

// chestTransferCommit 提交对“已打开窗口”的箱子类容器事务。
type chestTransferCommit struct {
	server    *Server
	container *containerState
	pos       [3]int
	def       *containerDef
	// locked 为 true 时 containerTransferMu 由外层持有，close 不解锁。
	locked bool
}

func (c *chestTransferCommit) apply(target *transferTarget) {
	c.container.mu.Lock()
	for _, index := range target.touched {
		if index < len(c.container.slots) {
			c.container.slots[index] = target.items[index]
		}
	}
	c.container.mu.Unlock()
	c.server.flushContainer(c.container)
	// 广播改动槽位（观察者需要看到漏斗搬运的结果）。
	for _, index := range target.touched {
		c.container.mu.Lock()
		stack := item.Empty()
		if index < len(c.container.slots) {
			stack = c.container.slots[index]
		}
		c.container.mu.Unlock()
		c.server.broadcastContainerSlot(c.container, index, stack, nil)
	}
}

func (c *chestTransferCommit) setHopperCooldown(int64) {}

func (c *chestTransferCommit) close() {
	if !c.locked {
		c.server.containerTransferMu.Unlock()
	}
}

// chestEntityTransferCommit 提交对“未打开”的箱子类容器事务（读写方块实体）。
type chestEntityTransferCommit struct {
	server *Server
	dim    world.Dimension
	pos    [3]int
	def    *containerDef
	// locked 为 true 时 containerTransferMu 由外层持有，close 不解锁。
	locked bool
}

func (c *chestEntityTransferCommit) apply(target *transferTarget) {
	entity := world.BlockEntity{
		TypeID: c.def.BlockEntityTypeID,
		Items:  make([]world.ContainerItem, len(target.items)),
	}
	for i, stack := range target.items {
		entity.Items[i] = containerItemFromStack(stack)
	}
	if err := c.server.worldFor(c.dim).SetBlockEntity(c.pos[0], c.pos[1], c.pos[2], entity); err != nil {
		slog.Error("写回容器内容失败", "x", c.pos[0], "y", c.pos[1], "z", c.pos[2], "error", err)
	}
}

func (c *chestEntityTransferCommit) setHopperCooldown(int64) {}

func (c *chestEntityTransferCommit) close() {
	if !c.locked {
		c.server.containerTransferMu.Unlock()
	}
}

// refreshHopperWindow 在漏斗窗口打开时把槽位同步给观察者。
func (s *Server) refreshHopperWindow(state *hopperState) {
	key := containerKey{dim: state.dim, x: state.pos[0], y: state.pos[1], z: state.pos[2]}
	s.containerMu.Lock()
	container := s.containers[key]
	s.containerMu.Unlock()
	if container == nil {
		return
	}
	state.mu.Lock()
	slots := append([]item.Stack(nil), state.slots[:]...)
	state.mu.Unlock()
	for index, stack := range slots {
		s.broadcastContainerSlot(container, index, stack, nil)
	}
}

// refreshFurnaceWindow 在熔炉窗口打开时把槽位同步给观察者。
func (s *Server) refreshFurnaceWindow(state *furnaceState) {
	key := containerKey{dim: state.dim, x: state.pos[0], y: state.pos[1], z: state.pos[2]}
	s.containerMu.Lock()
	container := s.containers[key]
	s.containerMu.Unlock()
	if container == nil || container.furnace != state {
		return
	}
	state.mu.Lock()
	slots := append([]item.Stack(nil), state.slots[:]...)
	state.mu.Unlock()
	for index, stack := range slots {
		s.broadcastContainerSlot(container, index, stack, nil)
	}
}

// updateHopperEnabled 按相邻信号更新漏斗的 enabled 属性
// （原版 HopperBlock.checkPoweredState：enabled = !hasNeighborSignal）。
func (s *Server) refreshHopperEnabled(w *world.World, pos [3]int, state uint16) {
	name, props, ok := registry.StateProps(state)
	if !ok || name != hopperBlockName {
		return
	}
	powered := s.bestNeighborSignal(w, pos) > 0
	if powered == (props["enabled"] == "false") {
		return // 已一致
	}
	next := cloneProps(props)
	if powered {
		next["enabled"] = "false"
	} else {
		next["enabled"] = "true"
	}
	s.setRedstoneBlock(w, pos[0], pos[1], pos[2], hopperBlockName, next)
}

// orientHopperPlacement 按点击面设置漏斗朝向（原版 HopperBlock.getStateForPlacement：
// facing = 点击面反方向，竖直方向退化为 DOWN；enabled 初始为 true）。
func orientHopperPlacement(clickedFace int) (uint16, bool) {
	faces := [6]string{"down", "up", "north", "south", "west", "east"}
	if clickedFace < 0 || clickedFace >= len(faces) {
		return 0, false
	}
	facing := oppositeDir(faces[clickedFace])
	if facing == "up" || facing == "down" {
		facing = "down"
	}
	state, ok := registry.BlockStateWithProps(hopperBlockName, map[string]string{
		"facing":  facing,
		"enabled": "true",
	})
	return state, ok
}
