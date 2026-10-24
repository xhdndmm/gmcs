//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"strconv"
	"sync"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 堆肥桶（对照 1.21.11 反编译的 ComposterBlock 逐字节码移植）：
//   - 方块状态 level 0..8（属性名 level）；
//   - 使用可堆肥物品（useItemOn）：level < 8 且物品可堆肥时尝试填入：
//     level == 0 必定成功（原版跳过随机判定），否则按物品概率掷骰；
//     填入结果通过世界事件 1500 同步（data 1=成功 / 0=失败，客户端据此播放
//     音效与粒子）；非创造模式消耗 1 个物品（level == 7 时直接返回成功但
//     不消耗、不填入）；
//   - 使用非可堆肥物品或空手（useWithoutItem）：level == 8 时弹出 1 个骨粉
//     掉落物、重置为 0 并播放 block.composter.empty 音效；
//   - level 7 → 8：填入成功达到 7 时调度 20 tick 后的转换（原版
//     level.scheduleTick(pos, this, 20)），触发时设置为 8 并播放
//     block.composter.ready 音效；
//   - 漏斗交互（原版 WorldlyContainer）：level < 7 时任意面可插入可堆肥物品
//     （每次 1 个并立即尝试填入）；level == 8 时任意面可抽出 1 个骨粉
//     （抽空后重置为 0）；level == 7 时无槽位；
//   - 比较器输出 level（本实现无比较器，跳过）；原版的 gameEvent(BLOCK_CHANGE)
//     因无幽匿传感器而无可观察效果，一并跳过。
//
// 定时转换本身不写入区块（原版把 scheduled tick 存入区块）：区块加载时
// 会扫描其中的 level 7 堆肥桶并立即重新登记，另有 600 tick 全量扫描兜底。

// 堆肥桶常量。
const (
	// composterBlockName 是堆肥桶方块名。
	composterBlockName = "minecraft:composter"
	// composterFillEvent 是填入结果的粒子/音效世界事件（原版
	// LevelEvent.COMPOSTER_FILL = 1500；data 1=成功、0=失败）。
	composterFillEvent = 1500
	// composterReadyDelay 是 level 7 → 8 的调度延迟（tick）。
	composterReadyDelay = 20
	// composterFullScanInterval 是 level-7 兜底扫描间隔（与漏斗兜底同量级）。
	composterFullScanInterval = 600
	// composterReach 是使用/交互距离（与普通方块交互一致的 8.0 盒近似）。
	composterReach = 8.0
)

// composterKey 是堆肥桶调度表的键（区块定位）。
type composterKey struct {
	dim world.Dimension
	x   int
	y   int
	z   int
}

// scheduleComposterReady 为 level == 7 的堆肥桶登记 20 tick 后的转换
// （existing 表示已登记时不覆盖；原版 scheduleTick 会覆盖为新的 20 tick，
// 这里仅用于"达到 7"的唯一路径 + 兜底扫描，重复登记无实际影响）。
func (s *Server) scheduleComposterReady(dim world.Dimension, x, y, z int) {
	key := composterKey{dim: dim, x: x, y: y, z: z}
	s.composterMu.Lock()
	if s.composterDue == nil {
		s.composterDue = make(map[composterKey]int64)
	}
	if _, exists := s.composterDue[key]; !exists {
		s.composterDue[key] = s.gameTicks.Load() + composterReadyDelay
	}
	s.composterMu.Unlock()
}

// tickComposters 推进堆肥定时转换：处理到期项（chunk 仍加载且仍是 level 7 时
// 置为 8 并播放就绪音效），并周期扫描已加载区块兜底重新登记 level 7 的堆肥桶
// （覆盖重启或区块卸载导致的丢失）。
func (s *Server) tickComposters() {
	now := s.gameTicks.Load()
	// 1) 到期转换。
	type dueEntry struct {
		key composterKey
		due int64
	}
	var due []dueEntry
	s.composterMu.Lock()
	for key, at := range s.composterDue {
		if at <= now {
			due = append(due, dueEntry{key: key, due: at})
		}
	}
	for _, entry := range due {
		delete(s.composterDue, entry.key)
	}
	s.composterMu.Unlock()
	for _, entry := range due {
		s.convertReadyComposter(entry.key)
	}
	// 2) 兜底扫描。
	s.composterScanCounter++
	if s.composterScanCounter < composterFullScanInterval {
		return
	}
	s.composterScanCounter = 0
	for _, loaded := range s.knownWorlds() {
		for _, pos := range loaded.world.BlockStatePositions(isComposterState) {
			state := loaded.world.BlockAt(pos[0], pos[1], pos[2])
			if composterLevelOf(state) != 7 {
				continue
			}
			s.scheduleComposterReady(loaded.dim, pos[0], pos[1], pos[2])
		}
	}
}

// convertReadyComposter 把到期的 level 7 堆肥桶置为 level 8
// （原版 ComposterBlock.tick）。区块未加载时放弃本项（由兜底扫描重新登记）。
func (s *Server) convertReadyComposter(key composterKey) {
	w := s.worldFor(key.dim)
	if w == nil || !w.ChunkLoaded(key.x, key.z) {
		return
	}
	state := w.BlockAt(key.x, key.y, key.z)
	if composterLevelOf(state) != 7 {
		return
	}
	next, ok := composterStateWithLevel(8)
	if !ok {
		return
	}
	if w.SetBlock(key.x, key.y, key.z, next) {
		s.broadcastBlockUpdate(key.dim, key.x, key.y, key.z, int32(next))
	}
	s.playComposterSound(key.dim, key.x, key.y, key.z, "minecraft:block.composter.ready")
}

// isComposterState 报告方块状态是否为堆肥桶。
func isComposterState(state uint16) bool {
	return registry.IsPropertyBlockState(composterBlockName, state)
}

// composterLevelOf 返回堆肥桶状态的 level（非堆肥桶返回 -1）。
func composterLevelOf(state uint16) int {
	name, props, ok := registry.StateProps(state)
	if !ok || name != composterBlockName {
		return -1
	}
	level, err := strconv.Atoi(props["level"])
	if err != nil {
		return -1
	}
	return level
}

// composterStateWithLevel 返回指定 level 的堆肥桶方块状态。
func composterStateWithLevel(level int) (uint16, bool) {
	return registry.BlockStateWithProps(composterBlockName, map[string]string{"level": strconv.Itoa(level)})
}

// boneMealItemID 返回骨粉（minecraft:bone_meal）的物品 ID。
var boneMealItemID = sync.OnceValue(func() int32 {
	id, _ := registry.ItemID("minecraft:bone_meal")
	return id
})

// acquireComposterTarget 以事务方式获取堆肥桶的搬运视图
// （原版 ComposterBlock.getContainer）：level < 7 → 输入容器（1 槽、只进不出）；
// level == 7 → 空容器（0 槽）；level == 8 → 产出容器（1 槽骨粉、只出不进）。
func (s *Server) acquireComposterTarget(dim world.Dimension, pos [3]int, state uint16) *transferTarget {
	commit := &composterTransferCommit{server: s, dim: dim, pos: pos}
	switch level := composterLevelOf(state); {
	case level < 0:
		return nil
	case level == 7:
		return &transferTarget{kind: transferComposterInput, items: []item.Stack{}, inner: commit}
	case level == 8:
		commit.output = true
		return &transferTarget{
			kind:  transferComposterOutput,
			items: []item.Stack{{ItemID: boneMealItemID(), Count: 1}},
			inner: commit,
		}
	default:
		return &transferTarget{kind: transferComposterInput, items: []item.Stack{item.Empty()}, inner: commit}
	}
}

// composterTransferCommit 是堆肥桶搬运事务的提交逻辑：
//   - 输入（level < 7）：插入的物品立即尝试填入并广播世界事件 1500
//     （原版 InputContainer.setChanged：addItem + levelEvent + 清空槽位）；
//   - 产出（level == 8）：骨粉被抽空后把堆肥桶重置为 level 0
//     （原版 OutputContainer.setChanged：为空时 empty）。
type composterTransferCommit struct {
	server *Server
	dim    world.Dimension
	pos    [3]int
	output bool
}

// apply 把改动应用到堆肥桶方块（原版容器 setChanged 的语义）。
func (c *composterTransferCommit) apply(target *transferTarget) {
	w := c.server.worldFor(c.dim)
	if w == nil || len(target.items) == 0 {
		return
	}
	state := w.BlockAt(c.pos[0], c.pos[1], c.pos[2])
	if composterLevelOf(state) < 0 {
		return
	}
	if c.output {
		if !target.items[0].IsEmpty() || composterLevelOf(state) != 8 {
			return
		}
		c.server.composterEmpty(c.dim, w, c.pos[0], c.pos[1], c.pos[2])
		return
	}
	inserted := target.items[0]
	if inserted.IsEmpty() || composterLevelOf(state) >= 7 {
		return
	}
	success := c.server.composterAddItem(c.dim, w, c.pos[0], c.pos[1], c.pos[2], state, inserted.ItemID)
	data := int32(0)
	if success {
		data = 1
	}
	c.server.broadcastWorldEvent(c.dim, c.pos, composterFillEvent, data)
}

// setHopperCooldown 堆肥桶不是漏斗，无错峰冷却。
func (c *composterTransferCommit) setHopperCooldown(int64) {}

// close 无额外资源。
func (c *composterTransferCommit) close() {}

// playComposterSound 向堆肥桶附近玩家播放方块音效（原版 level.playSound(null, ...)）。
func (s *Server) playComposterSound(dim world.Dimension, x, y, z int, soundName string) {
	soundID, ok := registry.StaticEntryID("minecraft:sound_event", soundName)
	if !ok {
		return
	}
	packet := protocol.EncodeSoundEffect(soundID, protocol.SoundCategoryBlock,
		float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1, 1, 0)
	s.writeToNearbyPlayers(packet, dim, float64(x), float64(z), nil)
}

// composterAddItem 尝试把一个可堆肥物品填入堆肥桶（原版 ComposterBlock.addItem）：
// level == 0 必定成功，否则按概率掷骰；成功时 level + 1（达到 7 时调度就绪转换）。
// 返回是否成功。仅由会话读循环与漏斗提交路径调用。
func (s *Server) composterAddItem(dim world.Dimension, w *world.World, x, y, z int, state uint16, itemID int32) bool {
	level := composterLevelOf(state)
	if level < 0 || level >= 8 {
		return false
	}
	chance := float64(registry.CompostChance(itemID))
	if chance <= 0 {
		return false
	}
	success := level == 0 || randUnit(s) < chance
	if !success {
		return false
	}
	next, ok := composterStateWithLevel(level + 1)
	if !ok {
		return false
	}
	if w.SetBlock(x, y, z, next) {
		s.broadcastBlockUpdate(dim, x, y, z, int32(next))
	}
	if level+1 == 7 {
		s.scheduleComposterReady(dim, x, y, z)
	}
	return true
}

// tryComposterUse 处理右键堆肥桶（原版 useItemOn + useWithoutItem 的组合语义）。
// 返回 true 表示交互已被处理（不再进入放置/物品使用流程）。
// 仅由会话读循环调用。
func (s *Server) tryComposterUse(player *session, w *world.World, x, y, z int, state uint16) bool {
	level := composterLevelOf(state)
	if level < 0 {
		return false
	}
	held := player.inventory.Get(player.selectedSlot)
	if level < 8 && registry.CompostChance(held.ItemID) > 0 {
		if level < 7 {
			// 填入（level 7 时原版同样返回成功但不消耗、不填入）。
			success := s.composterAddItem(player.dimensionID(), w, x, y, z, state, held.ItemID)
			data := int32(0)
			if success {
				data = 1
			}
			s.broadcastWorldEvent(player.dimensionID(), [3]int{x, y, z}, composterFillEvent, data)
			if player.gameModeID() != uint8(config.GameModeCreative) {
				held.Count--
				if held.Count <= 0 {
					held = item.Empty()
				}
				player.inventory.Set(player.selectedSlot, held)
				player.tryWrite(protocol.EncodeSetPlayerInventory(int32(player.selectedSlot), held.AppendSlot(nil)))
			}
		}
		return true
	}
	if level == 8 {
		// 取出骨粉（原版 extractProduce）。
		s.composterExtractProduce(player.dimensionID(), w, x, y, z)
		return true
	}
	return false
}

// composterExtractProduce 弹出 1 个骨粉掉落物并把堆肥桶重置为 level 0
// （原版 ComposterBlock.extractProduce：位置 = 方块中心 + (0.5, 1.01, 0.5) 并
// 附加 (nextFloat-0.5)*0.7 的水平随机偏移；随后播放 emptied 音效）。
// 仅由会话读循环与漏斗提交路径调用。
func (s *Server) composterExtractProduce(dim world.Dimension, w *world.World, x, y, z int) {
	px := float64(x) + 0.5 + (randUnit(s)-0.5)*0.7
	py := float64(y) + 1.01
	pz := float64(z) + 0.5 + (randUnit(s)-0.5)*0.7
	vx := randUnit(s)*0.2 - 0.1
	vz := randUnit(s)*0.2 - 0.1
	s.spawnItem(dim, item.Stack{ItemID: boneMealItemID(), Count: 1}, px, py, pz, vx, 0.2, vz, itemPickupDelayMining)
	s.composterEmpty(dim, w, x, y, z)
	s.playComposterSound(dim, x, y, z, "minecraft:block.composter.empty")
}

// composterEmpty 把堆肥桶重置为 level 0（原版 ComposterBlock.empty）。
func (s *Server) composterEmpty(dim world.Dimension, w *world.World, x, y, z int) {
	next, ok := composterStateWithLevel(0)
	if !ok {
		return
	}
	if w.SetBlock(x, y, z, next) {
		s.broadcastBlockUpdate(dim, x, y, z, int32(next))
	}
}
