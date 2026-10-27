//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 挖掘系统：对齐原版 ServerPlayerGameMode（1.21.11）。
//
// 流程（与原版逐行对应）：
//   - START_DESTROY_BLOCK：距离/高度校验 → 创造模式立即破坏（仍受物品
//     can_destroy_blocks_in_creative 与 GameMasterBlock 限制）→ 生存模式记录
//     开始刻并发送破坏进度；进度 ≥ 1（硬度 0 或瞬时工具）立即破坏；
//   - STOP_DESTROY_BLOCK：只处理与记录目标一致的请求；进度 ≥ 0.7 完成破坏，
//     否则转入“延迟破坏”（按原开始刻继续计时，进度满后破坏）；
//   - ABORT_DESTROY_BLOCK：清除跟踪并广播进度 -1；
//   - 每服务器 tick 推进进度并把 (int)(进度×10) 的 Block Destruction 包广播给
//     附近玩家（只在变化时发送，与原版 n 缓存一致）；
//   - 破坏动作完成后回 Block Changed Ack（原版在下一个连接 tick 发送）。
//
// 破坏结算：掉落只在生存模式且“正确工具”判定通过时生成（原版
// Player.hasCorrectToolForDrops；方块不要求正确工具时任何物品都掉落）；
// 工具按 minecraft:tool 的 damage_per_block 损耗耐久（硬度 0 的方块不损耗），
// 耐久耗尽按原版移除物品并广播 Entity Event 0。
//
// 已实现效率/水下速掘附魔与不灭耐久（见 enchants.go）；精准采集/时运/剪刀
// 与概率掉落见 block_drops.go。
//
// 已知限制（见 docs/TODO.md）：
//   - 挖掘疲劳/导管之力/急迫等状态效果加成未实现（无药水效果系统）；
//   - 无出生点保护（mayInteract）与 mayBuild（冒险模式整类不支持）；
//   - 延迟破坏的耐久损耗由读循环在下一个数据包处补发（Tick 线程不访问物品栏）。

const (
	// 破坏动作状态（原版 ServerboundPlayerActionPacket.Action 的协议编号）。
	statusStartDestroy = 0
	statusAbortDestroy = 1
	statusStopDestroy  = 2

	// maxBuildHeight 是世界高度上限：y ≥ 该值的位置没有方块
	// （原版 Level.getMaxBuildHeight，主世界/末地 320；y > 320 的动作被拒绝）。
	maxBuildHeight = 320

	// worldEventBlockBreak 是方块破坏粒子与音效事件（原版 2001，data 为方块状态 ID）。
	worldEventBlockBreak = 2001

	// 方块交互距离（相对玩家眼睛到方块碰撞盒的距离，另加 1.0 容差，
	// 见 withinBlockReach）：原版 block_interaction_range 属性默认 4.5，
	// 创造模式附加 creative_mode_block_range +0.5。
	blockInteractionRangeSurvival = 4.5
	blockInteractionRangeCreative = 5.0
	// blockInteractionSlack 是原版 ServerPlayerGameMode 对距离判定的固定容差。
	blockInteractionSlack = 1.0
	// containerInteractionSlack 是原版容器窗口的距离附加量
	// （Container.stillValidBlockEntity 与 AbstractContainerMenu.stillValid 均传入 4.0F）。
	containerInteractionSlack = 4.0
	stopDestroyProgress       = 0.7
	// instaMineProgress 是 START 时立即破坏所需的初始进度（原版 1.0）。
	instaMineProgress = 1.0
)

// gameMasterBlockNames 是原版 GameMasterBlock 方块（命令方块/结构方块/拼图/
// 测试方块及其子类）：非管理员（权限等级 < 2）不能破坏。gmcs 没有权限系统，
// 统一按非管理员处理，并回发方块状态纠正客户端预测。
var gameMasterBlockNames = map[string]bool{
	"minecraft:command_block":           true,
	"minecraft:chain_command_block":     true,
	"minecraft:repeating_command_block": true,
	"minecraft:structure_block":         true,
	"minecraft:jigsaw":                  true,
	"minecraft:test_block":              true,
	"minecraft:test_instance_block":     true,
}

// blockInteractionRange 返回玩家的方块交互距离（原版
// Player.getBlockInteractionRange）。
func (s *session) blockInteractionRange() float64 {
	if s.gameModeID() == uint8(config.GameModeCreative) {
		return blockInteractionRangeCreative
	}
	return blockInteractionRangeSurvival
}

// withinBlockReach 报告方块是否在交互距离内（原版 Player.canInteractWithBlock：
// 眼睛位置到方块碰撞盒的距离 < 交互距离 + 1.0，按平方比较）。
func (s *session) withinBlockReach(x, y, z int) bool {
	return s.withinReach(x, y, z, blockInteractionSlack)
}

// withinContainerReach 报告方块是否在容器窗口交互距离内（原版
// Player.isWithinBlockInteractionRange(pos, 4.0)：眼睛位置到方块碰撞盒的距离
// < 方块交互距离 + 4.0，按平方比较）。
func (s *session) withinContainerReach(x, y, z int) bool {
	return s.withinReach(x, y, z, containerInteractionSlack)
}

// withinReach 报告眼睛位置到方块碰撞盒的距离是否小于交互距离加给定容差
// （原版 Player.isWithinBlockInteractionRange 的 AABB 距离算法）。
func (s *session) withinReach(x, y, z int, slack float64) bool {
	px, py, pz, _, _ := s.playerPosition()
	eyeY := py + playerEyeHeight
	dx := math.Max(math.Max(float64(x)-px, px-(float64(x)+1)), 0)
	dy := math.Max(math.Max(float64(y)-eyeY, eyeY-(float64(y)+1)), 0)
	dz := math.Max(math.Max(float64(z)-pz, pz-(float64(z)+1)), 0)
	limit := s.blockInteractionRange() + slack
	return dx*dx+dy*dy+dz*dz < limit*limit
}

// setPlayerOnGround 记录客户端上报的着地状态（移动数据包）。
func (s *session) setPlayerOnGround(onGround bool) {
	s.stateMu.Lock()
	s.onGround = onGround
	s.stateMu.Unlock()
}

// playerOnGround 返回最近一次客户端上报的着地状态。
func (s *session) playerOnGround() bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.onGround
}

// markBlockChangedAck 记录待回的 Block Changed Ack 序号（取最大值，与原版
// ServerGamePacketListenerImpl.ackBlockChangesUpTo 一致；在下一次服务器
// Tick 发送）。
func (s *session) markBlockChangedAck(sequence int32) {
	if sequence < 0 {
		return
	}
	s.stateMu.Lock()
	if sequence > s.miningAckSequence {
		s.miningAckSequence = sequence
	}
	s.stateMu.Unlock()
}

// takeBlockChangedAck 取出待发送的 ack 序号；没有时返回 -1。
func (s *session) takeBlockChangedAck() int32 {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	sequence := s.miningAckSequence
	s.miningAckSequence = -1
	return sequence
}

// resetMiningState 清除挖掘跟踪（重生时调用，与原版重建
// ServerPlayerGameMode 一致）。
func (s *session) resetMiningState() {
	s.stateMu.Lock()
	s.miningDestroying = false
	s.miningDelayed = false
	s.miningDeferredCompletion = false
	s.miningLastProgress = -1
	s.stateMu.Unlock()
	s.miningPendingDamage.Store(0)
}

// miningSpeedFor 返回玩家对指定方块的挖掘速度（原版 Player.getDestroySpeed）：
// 工具速度，效率附魔 +等级²+1（仅在工具速度 > 1 时），悬空时 ÷5，
// 头部浸水时 ÷5（水下速掘附魔可免除），与原版顺序一致。
func (s *Server) miningSpeedFor(player *session, held item.Stack, state uint16) float32 {
	speed := float32(1)
	if tool, ok := registry.ItemTool(held.ItemID); ok {
		speed = tool.MiningSpeed(state)
	}
	speed = miningEnchantSpeed(held, speed)
	if !player.playerOnGround() {
		speed /= 5
	}
	if w := player.playerWorld(); w != nil {
		x, y, z, _, _ := player.playerPosition()
		if headInWater(w, x, y, z) && enchantLevel(held, enchants().aquaAffinity) <= 0 {
			speed /= 5
		}
	}
	return speed
}

// miningHasCorrectToolForDrops 报告手持物品对方块是否算“正确工具”
// （原版 Player.hasCorrectToolForDrops：方块不要求正确工具时为 true，
// 否则取物品 minecraft:tool 规则的 correct_for_drops）。
func miningHasCorrectToolForDrops(state uint16, held item.Stack) bool {
	_, requires := registry.BlockHardness(state)
	if !requires {
		return true
	}
	tool, ok := registry.ItemTool(held.ItemID)
	return ok && tool.CorrectForDrops(state)
}

// destroyProgressFor 返回一次 tick 的挖掘进度增量（原版
// BlockBehaviour.getDestroyProgress：速度 / 硬度 /（正确工具 ? 30 : 100）；
// 硬度 -1 的不可破坏方块返回 0，硬度 0 的方块为 +Inf 立即完成）。
func (s *Server) destroyProgressFor(player *session, held item.Stack, state uint16) float32 {
	hardness, _ := registry.BlockHardness(state)
	if hardness < 0 {
		return 0
	}
	divisor := float32(100)
	if miningHasCorrectToolForDrops(state, held) {
		divisor = 30
	}
	return s.miningSpeedFor(player, held, state) / hardness / divisor
}

// miningToolDamage 返回破坏方块应付出的工具耐久（原版 Item.mineBlock：
// 只有带 minecraft:tool 组件且 damage_per_block > 0 的物品、且方块硬度非 0
// 时损耗）。
func miningToolDamage(held item.Stack, state uint16) int32 {
	if held.IsEmpty() {
		return 0
	}
	tool, ok := registry.ItemTool(held.ItemID)
	if !ok || tool.DamagePerBlock <= 0 {
		return 0
	}
	hardness, _ := registry.BlockHardness(state)
	if hardness == 0 {
		return 0
	}
	return tool.DamagePerBlock
}

// sendBlockCorrection 向玩家回发某位置的当前方块状态（原版破坏被拒绝时
// 发送 ClientboundBlockUpdatePacket 纠正客户端预测）。
func (s *session) sendBlockCorrection(x, y, z int) {
	state := s.playerWorld().BlockAt(x, y, z)
	s.tryWrite(protocol.EncodeBlockUpdate(x, y, z, int32(state)))
}

// broadcastBlockDestruction 把破坏进度动画广播给附近玩家（原版
// ServerLevel.destroyBlockProgress 发送 ClientboundBlockDestructionPacket）。
func (s *Server) broadcastBlockDestruction(dim world.Dimension, entityID int32, x, y, z int, progress int8) {
	packet := protocol.EncodeBlockDestruction(entityID, x, y, z, progress)
	s.writeToNearbyPlayers(packet, dim, float64(x)+0.5, float64(z)+0.5, nil)
}

// blockBreakDenied 报告破坏是否被原版级规则拒绝（返回 true 时调用方应回发
// 方块状态纠正）：创造模式下持有 can_destroy_blocks_in_creative=false 的
// 物品（剑/三叉戟/重锤），或方块为 GameMasterBlock（非管理员不可破坏）。
func (s *Server) blockBreakDenied(player *session, held item.Stack, state uint16) bool {
	if player.gameModeID() == uint8(config.GameModeCreative) {
		if tool, ok := registry.ItemTool(held.ItemID); ok && !tool.CanDestroyInCreative {
			return true
		}
	}
	if name, ok := s.blockNameOf(state); ok && gameMasterBlockNames[name] {
		return true
	}
	return false
}

// removeBlockAndBroadcast 把方块置为空气并把变化广播给附近玩家、红石与流体系统。
// 返回是否实际移除了方块。不包含破坏前的会话级钩子（见 playerBreakHooks）。
func (s *Server) removeBlockAndBroadcast(dim world.Dimension, w *world.World, x, y, z int, current uint16) bool {
	if !w.SetBlock(x, y, z, world.AirBlock) {
		return false
	}
	s.unregisterFarm(dim, x, y, z)
	s.broadcastBlockUpdate(dim, x, y, z, int32(world.AirBlock))
	s.updateRedstoneAround(dim, x, y, z)
	s.updateFluidsAround(dim, x, y, z)
	// 台阶：邻居变化后重算形状（原版 StairsBlock.updateShape）。
	s.updateStairsAround(dim, w, x, y, z)
	// 树叶衰减：失去原木支撑的自然树叶掉落消失（原版 LeavesBlock 的 distance）。
	s.decayLeavesAround(dim, w, x, y, z, current)
	if current == world.ObsidianBlock {
		// 破坏黑曜石框后与之相连的传送门熄灭（近似原版行为）。
		s.clearPortalsNear(dim, x, y, z)
	}
	return true
}

// finishBreakBlock 执行一次破坏的公共部分：原版级规则校验、破坏粒子、
// 破坏前钩子与方块移除。
// 返回 (removed, correction)：removed 表示方块被移除；correction 表示应回发
// 方块状态纠正客户端（与原版不同，流体的“破坏”返回 (false, false)——原版
// destroyBlock 对流体不改变世界且不回发纠正）。
// 仅在读循环线程调用（会运行触碰会话窗口状态的钩子）。
func (s *Server) finishBreakBlock(player *session, held item.Stack, dim world.Dimension, x, y, z int, state uint16) (removed, correction bool) {
	if s.blockBreakDenied(player, held, state) {
		return false, true
	}
	if isWaterState(state) || isLavaState(state) {
		return false, false
	}
	w := s.worldFor(dim)
	if s.playerBreakHooks(player, dim, w, x, y, z, state) {
		return true, false
	}
	// 破坏粒子与音效（原版 Level.destroyBlock 的 2001 事件；流体不发送）。
	s.broadcastWorldEvent(dim, [3]int{x, y, z}, worldEventBlockBreak, int32(state))
	if !s.removeBlockAndBroadcast(dim, w, x, y, z, state) {
		return false, false
	}
	// 双格植物：同步移除另一半（不掉落，与 DoublePlantBlock 的邻居更新等价）。
	s.removeDoublePlantPair(w, dim, x, y, z, state)
	// 门：同步移除另一半（掉落由被破坏格的战利品表结算）。
	s.removeDoorPair(w, dim, x, y, z, state)
	// 床：同步移除另一半。
	s.removeBedPair(w, dim, x, y, z, state)
	return true, false
}

// destroyBlockByPlayer 完成一次破坏的结算（破坏 + 掉落 + 耐久），
// 仅由读循环调用（直接访问物品栏）。
func (s *Server) destroyBlockByPlayer(player *session, held item.Stack, dim world.Dimension, x, y, z int, state uint16) {
	removed, correction := s.finishBreakBlock(player, held, dim, x, y, z, state)
	if correction {
		player.sendBlockCorrection(x, y, z)
		return
	}
	if !removed {
		return
	}
	if player.gameModeID() == uint8(config.GameModeCreative) {
		// 原版：创造模式破坏不产生掉落也不损耗耐久。
		return
	}
	// 门的上半格不掉落（原版战利品表带 half=lower 条件）；耐久损耗照常。
	dropAllowed := true
	if name, ok := s.blockNameOf(state); ok {
		if half, ok := doorHalfOf(name, state); ok && half == 1 {
			dropAllowed = false
		}
	}
	if dropAllowed && miningHasCorrectToolForDrops(state, held) {
		s.dropBlockItem(dim, state, x, y, z, held)
	}
	if damage := miningToolDamage(held, state); damage > 0 {
		player.applyToolDamage(damage)
	}
}

// applyToolDamage 对当前选中槽位的物品施加耐久损耗；耐久耗尽时按原版移除物品
// 并广播 Entity Event 0（主手物品损坏音效）。仅由读循环调用。
func (s *session) applyToolDamage(amount int32) {
	if amount <= 0 {
		return
	}
	slot := s.selectedHotbarSlot()
	stack := s.inventory.Get(slot)
	if stack.IsEmpty() {
		return
	}
	if _, ok := registry.MaxDamage(stack.ItemID); !ok {
		return // 不可损坏物品
	}
	if unbreakingSavesDurability(s.server, stack, false) {
		return // 不灭附魔免除了本次损耗
	}
	// Damage 在锁内完成读-改-写：读循环可能同时修改选中槽位（容器/丢弃）。
	stack, maxDamage, ok := s.inventory.Damage(slot, amount)
	if !ok {
		return // 槽位在判定后被清空
	}
	if stack.Damage >= maxDamage {
		s.tryWrite(protocol.EncodeSetPlayerInventory(int32(slot), item.Empty().AppendSlot(nil)))
		x, _, z, _, _ := s.playerPosition()
		s.server.writeToNearbyPlayers(protocol.EncodeEntityEvent(s.entityID, 0), s.dimensionID(), x, z, nil)
		return
	}
	s.tryWrite(protocol.EncodeSetPlayerInventory(int32(slot), stack.AppendSlot(nil)))
}

// applyDeferredMiningEffects 在读循环应用 Tick 线程排队的挖掘副作用：
// 延迟破坏补发与工具耐久损耗（Tick 线程不能访问物品栏与窗口状态）。
func (s *session) applyDeferredMiningEffects() {
	if damage := s.miningPendingDamage.Swap(0); damage > 0 {
		s.applyToolDamage(damage)
	}
	s.stateMu.Lock()
	deferred := s.miningDeferredCompletion
	pos := s.miningDelayedPos
	s.miningDeferredCompletion = false
	s.stateMu.Unlock()
	if !deferred {
		return
	}
	dim := s.dimensionID()
	w := s.playerWorld()
	state := w.BlockAt(pos[0], pos[1], pos[2])
	if state == world.AirBlock {
		return
	}
	held := s.inventory.Get(s.selectedHotbarSlot())
	s.server.destroyBlockByPlayer(s, held, dim, pos[0], pos[1], pos[2], state)
}

// handleBlockBreakAction 处理方块破坏动作（原版
// ServerPlayerGameMode.handleBlockBreakAction 的外层：距离与高度校验）。
func (s *Server) handleBlockBreakAction(player *session, action protocol.PlayerAction) {
	if !player.withinBlockReach(action.X, action.Y, action.Z) {
		return // "too far"：与原版一致，不发送纠正
	}
	if action.Y > maxBuildHeight {
		player.sendBlockCorrection(action.X, action.Y, action.Z) // "too high"
		return
	}
	switch action.Status {
	case statusStartDestroy:
		s.startDestroyBlock(player, action)
	case statusAbortDestroy:
		s.abortDestroyBlock(player, action)
	case statusStopDestroy:
		s.stopDestroyBlock(player, action)
	}
}

// startDestroyBlock 处理 START_DESTROY_BLOCK。
func (s *Server) startDestroyBlock(player *session, action protocol.PlayerAction) {
	dim := player.dimensionID()
	w := s.worldFor(dim)
	state := w.BlockAt(action.X, action.Y, action.Z)
	held := player.inventory.Get(player.selectedHotbarSlot()) // 读循环：可直接访问物品栏
	if player.gameModeID() == uint8(config.GameModeCreative) {
		// 原版：创造模式在 START 时立即破坏（含 canDestroyBlock /
		// GameMasterBlock 校验；被拒绝时回发方块状态）。
		s.destroyBlockByPlayer(player, held, dim, action.X, action.Y, action.Z, state)
		return
	}
	if !player.canInteractBlocks() {
		// 旁观/冒险（原版 mayInteract/mayBuild 拒绝）：回发方块状态。
		player.sendBlockCorrection(action.X, action.Y, action.Z)
		return
	}
	progress := float32(instaMineProgress)
	if state != world.AirBlock {
		progress = s.destroyProgressFor(player, held, state)
	}
	if state != world.AirBlock && progress >= instaMineProgress {
		// 瞬时破坏（原版 "insta mine"）：不更新跟踪状态（原版同样直接返回）。
		s.destroyBlockByPlayer(player, held, dim, action.X, action.Y, action.Z, state)
		return
	}
	// 记录开始刻并切换跟踪目标；若此前正在挖掘其它方块，回发旧目标的
	// 方块状态（原版 "abort destroying since another started" 纠正）。
	oldPos, hadOld := player.beginMiningTrack(action.X, action.Y, action.Z, s.gameTicks.Load())
	if hadOld && oldPos != [3]int{action.X, action.Y, action.Z} {
		player.sendBlockCorrection(oldPos[0], oldPos[1], oldPos[2])
	}
	value := int32(int(progress * 10))
	s.broadcastBlockDestruction(dim, player.entityID, action.X, action.Y, action.Z, int8(value))
	player.setMiningLastProgress(value)
}

// stopDestroyBlock 处理 STOP_DESTROY_BLOCK。
func (s *Server) stopDestroyBlock(player *session, action protocol.PlayerAction) {
	pos := [3]int{action.X, action.Y, action.Z}
	tracked, destroying, start, delayed := player.miningSnapshot()
	if !destroying || tracked != pos {
		return // 与原版一致：不匹配记录目标的 STOP 不产生效果
	}
	dim := player.dimensionID()
	w := s.worldFor(dim)
	state := w.BlockAt(action.X, action.Y, action.Z)
	if state != world.AirBlock {
		held := player.inventory.Get(player.selectedHotbarSlot())
		progress := s.destroyProgressFor(player, held, state) * float32(s.gameTicks.Load()-start+1)
		if progress >= stopDestroyProgress {
			// 完成破坏：清除跟踪、广播进度 -1，再执行破坏（原版顺序）。
			player.clearMiningTrack()
			s.broadcastBlockDestruction(dim, player.entityID, action.X, action.Y, action.Z, -1)
			s.destroyBlockByPlayer(player, held, dim, action.X, action.Y, action.Z, state)
			return
		}
		if !delayed {
			// 进度不足：转入延迟破坏（按原开始刻继续计时，进度满 1 后破坏）。
			player.beginDelayedMining(pos, start)
		}
	}
}

// abortDestroyBlock 处理 ABORT_DESTROY_BLOCK：清除跟踪并广播进度 -1
// （记录目标与请求目标不一致时先清除旧目标的动画）。
func (s *Server) abortDestroyBlock(player *session, action protocol.PlayerAction) {
	pos := [3]int{action.X, action.Y, action.Z}
	tracked, _, _, _ := player.miningSnapshot()
	player.clearMiningTrack()
	dim := player.dimensionID()
	if tracked != pos {
		s.broadcastBlockDestruction(dim, player.entityID, tracked[0], tracked[1], tracked[2], -1)
	}
	s.broadcastBlockDestruction(dim, player.entityID, action.X, action.Y, action.Z, -1)
}

// beginMiningTrack 记录新的挖掘目标，返回旧目标与被跟踪标志。
func (s *session) beginMiningTrack(x, y, z int, tick int64) (oldPos [3]int, hadOld bool) {
	s.stateMu.Lock()
	oldPos, hadOld = s.miningPos, s.miningDestroying
	s.miningDestroying = true
	s.miningPos = [3]int{x, y, z}
	s.miningStartTick = tick
	s.miningDelayed = false
	s.stateMu.Unlock()
	return oldPos, hadOld
}

// clearMiningTrack 清除即时挖掘跟踪（不清除延迟破坏状态）。
func (s *session) clearMiningTrack() {
	s.stateMu.Lock()
	s.miningDestroying = false
	s.stateMu.Unlock()
}

// beginDelayedMining 转入延迟破坏（保留原开始刻）。
func (s *session) beginDelayedMining(pos [3]int, startTick int64) {
	s.stateMu.Lock()
	s.miningDestroying = false
	s.miningDelayed = true
	s.miningDelayedPos = pos
	s.miningDelayedStartTick = startTick
	s.stateMu.Unlock()
}

// miningSnapshot 返回挖掘跟踪状态（受 stateMu 保护）。
func (s *session) miningSnapshot() (pos [3]int, destroying bool, startTick int64, delayed bool) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.miningPos, s.miningDestroying, s.miningStartTick, s.miningDelayed
}

// setMiningLastProgress 记录最近发送的破坏进度（原版 n 缓存）。
func (s *session) setMiningLastProgress(value int32) {
	s.stateMu.Lock()
	s.miningLastProgress = value
	s.stateMu.Unlock()
}

// sendMiningProgress 计算并从缓存差值发送破坏进度（原版 ServerPlayerGameMode
// 的进度辅助方法）：返回本次累计进度。
func (s *session) sendMiningProgress(dim world.Dimension, pos [3]int, progress float32) float32 {
	value := int32(int(progress * 10))
	s.stateMu.Lock()
	last := s.miningLastProgress
	s.stateMu.Unlock()
	if value != last {
		s.server.broadcastBlockDestruction(dim, s.entityID, pos[0], pos[1], pos[2], int8(value))
		s.setMiningLastProgress(value)
	}
	return progress
}

// tickMining 每服务器 tick 推进所有玩家的挖掘进度（原版
// ServerPlayerGameMode.tick）。
func (s *Server) tickMining(players []*session) {
	for _, player := range players {
		if !player.isJoined() {
			continue
		}
		// 先发送待回的 Block Changed Ack（原版在连接 tick 里发送）。
		if sequence := player.takeBlockChangedAck(); sequence >= 0 {
			player.tryWrite(protocol.EncodeBlockChangedAck(sequence))
		}
		s.advanceMining(player)
	}
}

// advanceMining 推进单个玩家的挖掘进度（Tick 线程；物品栏只读装备快照）。
func (s *Server) advanceMining(player *session) {
	pos, destroying, start, delayed := player.miningSnapshot()
	if !destroying && !delayed {
		return
	}
	dim := player.dimensionID()
	w := s.worldFor(dim)
	held := player.equipmentSnapshot()[protocol.EquipmentMainHand]
	tick := s.gameTicks.Load()
	if delayed {
		state := w.BlockAt(pos[0], pos[1], pos[2])
		if state == world.AirBlock {
			player.clearDelayedMining()
			return
		}
		progress := s.destroyProgressFor(player, held, state) * float32(tick-start+1)
		if progress >= instaMineProgress {
			player.clearDelayedMining()
			s.completeDelayedDestroy(player, held, dim, pos, state)
			return
		}
		player.sendMiningProgress(dim, pos, progress)
		return
	}
	// 即时挖掘：跟踪目标的方块消失时清除动画（原版 tick 的 g 分支）。
	state := w.BlockAt(pos[0], pos[1], pos[2])
	if state == world.AirBlock {
		player.clearMiningTrack()
		s.broadcastBlockDestruction(dim, player.entityID, pos[0], pos[1], pos[2], -1)
		player.setMiningLastProgress(-1)
		return
	}
	progress := s.destroyProgressFor(player, held, state) * float32(tick-start+1)
	player.sendMiningProgress(dim, pos, progress)
}

// clearDelayedMining 清除延迟破坏状态（保留坐标便于可能的读循环补发）。
func (s *session) clearDelayedMining() {
	s.stateMu.Lock()
	s.miningDelayed = false
	s.stateMu.Unlock()
}

// completeDelayedDestroy 执行延迟破坏的完成结算（Tick 线程）。
// 会触碰会话窗口状态的方块（容器/铁砧/砂轮机/附魔台/活塞联动）不能在此完成，
// 标记后由读循环补发；其余方块直接结算（耐久损耗排队给读循环）。
func (s *Server) completeDelayedDestroy(player *session, held item.Stack, dim world.Dimension, pos [3]int, state uint16) {
	if s.breakHasWindowHooks(state) {
		player.stateMu.Lock()
		player.miningDeferredCompletion = true
		player.miningDelayedPos = pos
		player.stateMu.Unlock()
		return
	}
	if s.blockBreakDenied(player, held, state) {
		player.sendBlockCorrection(pos[0], pos[1], pos[2])
		return
	}
	if isWaterState(state) || isLavaState(state) {
		return
	}
	w := s.worldFor(dim)
	s.broadcastWorldEvent(dim, pos, worldEventBlockBreak, int32(state))
	if !s.removeBlockAndBroadcast(dim, w, pos[0], pos[1], pos[2], state) {
		return
	}
	if player.gameModeID() == uint8(config.GameModeCreative) {
		return
	}
	if miningHasCorrectToolForDrops(state, held) {
		s.dropBlockItem(dim, state, pos[0], pos[1], pos[2], held)
	}
	if damage := miningToolDamage(held, state); damage > 0 {
		player.miningPendingDamage.Add(damage)
	}
}

// breakHasWindowHooks 报告破坏该方块是否会触碰破坏者会话的窗口状态
// （容器内容物、铁砧/砂轮机/附魔台窗口关闭、活塞联动）：这类方块的延迟破坏
// 由读循环完成，避免 Tick 线程并发访问读循环独占的字段。
func (s *Server) breakHasWindowHooks(state uint16) bool {
	if _, _, ok := s.containerForState(state); ok {
		return true
	}
	if isAnvilState(state) || s.isPistonState(state) || s.isPistonHeadState(state) {
		return true
	}
	name, ok := s.blockNameOf(state)
	if !ok {
		return true // 未知识别的状态按需要钩子处理（保守）
	}
	return name == grindstoneBlockName || name == enchantingTableName
}
