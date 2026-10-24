//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"log/slog"
	"math"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 方块交互：破坏与放置。服务端校验距离（原版 4.5/5.0 格 + 1.0 容差）与方块后
// 修改世界，并把 Block Update 广播给附近玩家；体素修改会随区块保存
// （Flush/卸载）持久化。
//
// 破坏走完整的挖掘系统（硬度/工具速度/正确工具判定/进度动画，见 mining.go）；
// 掉落物按简化掉落表生成（无精准采集/时运与概率掉落，见 block_drops.go），
// 放置只支持不携带数据组件的方块物品；冒险与旁观模式不支持交互。

const (
	// playerEyeHeight 是玩家眼睛相对脚部的高度。
	playerEyeHeight = 1.62
)

// canInteractBlocks 报告玩家当前是否允许破坏/放置方块。
func (s *session) canInteractBlocks() bool {
	switch s.gameModeID() {
	case uint8(config.GameModeCreative), uint8(config.GameModeSurvival):
		return true
	}
	return false
}

// handlePlayerAction 处理 Player Action（block_dig）包：破坏方块、丢弃物品、
// 与副手交换与释放使用键。状态编号与原版一致（1.21.11 枚举序号，包按序号编解码）：
// 0 START_DESTROY_BLOCK、1 ABORT_DESTROY_BLOCK、2 STOP_DESTROY_BLOCK、
// 3 DROP_ALL_ITEMS、4 DROP_ITEM、5 RELEASE_USE_ITEM、6 SWAP_ITEM_WITH_OFFHAND。
func (s *Server) handlePlayerAction(player *session, action protocol.PlayerAction) {
	const (
		statusDropStack  = 3 // Ctrl+Q：丢弃整组（原版 DROP_ALL_ITEMS，1.21.11 枚举顺序）。
		statusDropItem   = 4 // Q：丢弃 1 个（原版 DROP_ITEM）。
		statusReleaseUse = 5 // 释放使用键：取消进食等使用中的动作。
	)
	switch action.Status {
	case statusStartDestroy, statusAbortDestroy, statusStopDestroy:
		// 原版对三个破坏动作统一记录 ack（下一个服务器 Tick 发送）。
		player.markBlockChangedAck(action.Sequence)
		if player.isDead() {
			return
		}
		s.handleBlockBreakAction(player, action)
	case statusDropStack:
		s.dropItemFromPlayer(player, true)
	case statusDropItem:
		s.dropItemFromPlayer(player, false)
	case statusReleaseUse:
		s.releaseUseItem(player)
	case statusSwapOffhand:
		s.swapItemWithOffhand(player)
	}
}

// statusSwapOffhand 是 Player Action 的“与副手交换”状态（原版 F 键）。
const statusSwapOffhand = 6

// swapItemWithOffhand 交换主手选中槽与副手槽的物品（原版
// ServerGamePacketListenerImpl 的 SWAP_ITEM_WITH_OFFHAND 分支；旁观者不处理），
// 交换后同步两个槽位。
func (s *Server) swapItemWithOffhand(player *session) {
	if player.gameModeID() == uint8(config.GameModeSpectator) || player.isDead() {
		return
	}
	player.cancelUseItem()
	slot := player.selectedHotbarSlot()
	main := player.inventory.Get(slot)
	off := player.inventory.Get(item.SlotOffhand)
	player.inventory.Set(slot, off)
	player.inventory.Set(item.SlotOffhand, main)
	player.tryWrite(protocol.EncodeSetPlayerInventory(int32(slot), off.AppendSlot(nil)))
	player.tryWrite(protocol.EncodeSetPlayerInventory(int32(item.SlotOffhand), main.AppendSlot(nil)))
}

// playerBreakHooks 运行破坏前的会话级钩子（活塞联动、容器内容物掉落、
// 破坏者打开的窗口关闭），返回 true 表示活塞联动已自行处理破坏。
// 仅由读循环调用（延迟破坏经 applyDeferredMiningEffects 在读循环补发）。
func (s *Server) playerBreakHooks(player *session, dim world.Dimension, w *world.World, x, y, z int, current uint16) bool {
	// 活塞基座/活塞头联动：破坏基座时头部消失；破坏头部时基座被移除
	// （原版 PistonHeadBlock 与 PistonBaseBlock 的行为）。
	if s.handlePistonBreak(dim, player, w, x, y, z, current) {
		return true
	}
	// 破坏容器方块：先关闭观察窗口，再把内容物掉落到世界中（与原版一致）。
	if def, _, ok := s.containerForState(current); ok {
		switch {
		case def.IsFurnace:
			s.destroyFurnace(dim, player, x, y, z, def)
		case def.IsHopper:
			s.destroyHopper(dim, player, x, y, z, def)
		case def.IsBrewing:
			s.destroyBrewing(dim, player, x, y, z, def)
		default:
			s.destroyContainer(dim, player, x, y, z, def)
		}
	}
	// 破坏铁砧：关闭破坏者正在使用的铁砧窗口（其他玩家的窗口在下次交互时
	// 通过 stillValid 校验关闭；破坏位置与窗口不符时不影响）。
	if isAnvilState(current) {
		if state := player.getAnvil(); state != nil {
			state.mu.Lock()
			anvilDim, anvilPos := state.dim, state.pos
			state.mu.Unlock()
			if anvilDim == dim && anvilPos == [3]int{x, y, z} {
				s.closeAnvil(player, false)
			}
		}
	}
	// 破坏砂轮机：关闭破坏者正在使用的砂轮机窗口（其他玩家的窗口在下次交互时
	// 通过 stillValid 校验关闭）。
	if state := player.getGrindstone(); state != nil {
		state.mu.Lock()
		grindstoneDim, grindstonePos := state.dim, state.pos
		state.mu.Unlock()
		if grindstoneDim == dim && grindstonePos == [3]int{x, y, z} {
			s.closeGrindstone(player, false)
		}
	}
	// 破坏附魔台：关闭破坏者正在使用的附魔台窗口（其他玩家的窗口在下次交互时
	// 通过 stillValid 校验关闭）。
	if isEnchantingTableState(current) {
		if state := player.getEnchanting(); state != nil {
			state.mu.Lock()
			enchantingDim, enchantingPos := state.dim, state.pos
			state.mu.Unlock()
			if enchantingDim == dim && enchantingPos == [3]int{x, y, z} {
				s.closeEnchanting(player, false)
			}
		}
	}
	return false
}

// blockFaceOffsets 是 Use Item On 的六个朝向对应的放置偏移（-Y、+Y、-Z、+Z、-X、+X）。
var blockFaceOffsets = [6][3]int{{0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0}}

// itemBlockOverrides 是物品名 → 方块名的特例表（物品放置出的方块名不同）。
var itemBlockOverrides = map[string]string{
	"minecraft:redstone": "minecraft:redstone_wire",
}

// handleUseItemOn 处理 Use Item On（block_place）包：打开容器或放置方块。
func (s *Server) handleUseItemOn(player *session, use protocol.UseItemOn) {
	if !player.canInteractBlocks() || player.isDead() || use.Hand != 0 {
		return
	}
	if use.Direction < 0 || int(use.Direction) >= len(blockFaceOffsets) {
		return
	}
	if !player.withinBlockReach(use.X, use.Y, use.Z) {
		return
	}
	// 右键容器方块：打开窗口（潜行时改为放置，与原版一致）。
	dim := player.dimensionID()
	w := s.worldFor(dim)
	// 打火石点燃下界传送门 / 末影之眼填充末地传送门框（不受潜行影响）。
	current := w.BlockAt(use.X, use.Y, use.Z)
	if held := player.inventory.Get(player.selectedHotbarSlot()); !held.IsEmpty() {
		if heldName, ok := registry.ItemName(held.ItemID); ok {
			switch heldName {
			case flintAndSteelName:
				if s.tryUseFlintAndSteel(player, w, use.X, use.Y, use.Z, current) {
					return
				}
				if s.tryUseFlintAndSteelFire(player, w, use, current) {
					return
				}
			case enderEyeName:
				if s.tryPlaceEnderEye(player, w, use.X, use.Y, use.Z, current) {
					return
				}
			}
		}
	}
	// 右键堆肥桶：填入可堆肥物品 / 取出骨粉（原版 ComposterBlock.useItemOn 与
	// useWithoutItem 的组合语义；潜行不影响）。
	if name, ok := s.blockNameOf(current); ok && name == composterBlockName {
		if s.tryComposterUse(player, w, use.X, use.Y, use.Z, current) {
			return
		}
	}
	if !player.sneaking {
		// 拉杆/按钮：切换红石状态。
		if s.handleRedstoneUse(w, use.X, use.Y, use.Z, current) {
			return
		}
		if def, blockName, ok := s.containerForState(current); ok {
			s.openContainer(player, use.X, use.Y, use.Z, def, blockName)
			return
		}
		// 右键工作台：打开 3×3 合成窗口。
		name, hasName := s.blockNameOf(current)
		if hasName && name == "minecraft:crafting_table" {
			s.openCraftingTable(player, use.X, use.Y, use.Z)
			return
		}
		// 右键切石机/锻造台：打开对应窗口。
		if hasName && name == "minecraft:stonecutter" {
			s.openStonecutter(player, use.X, use.Y, use.Z)
			return
		}
		if hasName && name == "minecraft:smithing_table" {
			s.openSmithing(player, use.X, use.Y, use.Z)
			return
		}
		// 右键铁砧：打开修复/重命名窗口。
		if hasName && isAnvilBlockName(name) {
			s.openAnvil(player, use.X, use.Y, use.Z)
			return
		}
		// 右键砂轮机：打开修复/祛魔窗口。
		if hasName && name == grindstoneBlockName {
			s.openGrindstone(player, use.X, use.Y, use.Z)
			return
		}
		// 右键附魔台：打开附魔窗口。
		if hasName && name == enchantingTableName {
			s.openEnchanting(player, use.X, use.Y, use.Z)
			return
		}
		// 右键末影箱：打开玩家自己的末影箱（内容随玩家）。
		if hasName && name == "minecraft:ender_chest" {
			s.openEnderChest(player)
			return
		}
	}
	offset := blockFaceOffsets[use.Direction]
	// 桶：放置/收集流体（在容器交互之后、方块放置之前，与原版交互顺序一致）。
	if s.tryUseBucket(player, w, use) {
		return
	}
	placeX := use.X + offset[0]
	placeY := use.Y + offset[1]
	placeZ := use.Z + offset[2]
	if _, ok := world.SectionIndex(placeY); !ok {
		return
	}
	existing := w.BlockAt(placeX, placeY, placeZ)
	if existing != world.AirBlock && !isWaterState(existing) && !isLavaState(existing) {
		return // 目标位置不可替换
	}
	stack := player.inventory.Get(player.selectedHotbarSlot())
	if stack.IsEmpty() {
		return
	}
	name, ok := registry.ItemName(stack.ItemID)
	if !ok {
		return
	}
	state, ok := registry.BlockStateIDs[name]
	if !ok {
		// 少数物品对应不同名的方块（如红石粉 → 红石线）。
		if blockName, exists := itemBlockOverrides[name]; exists {
			state, ok = registry.BlockStateIDs[blockName]
		}
	}
	if !ok {
		return // 手持物品不是可放置的方块
	}
	// 红石类方向方块（中继器/比较器/侦测器/活塞）按玩家视线方向设置朝向。
	_, _, _, placeYaw, placePitch := player.playerPosition()
	state = orientPlacementState(state, placeYaw, placePitch)
	if blockName, hasName := s.blockNameOf(state); hasName && blockName == hopperBlockName {
		// 漏斗：按点击面决定朝向（原版 HopperBlock.getStateForPlacement）。
		if hopperState, ok := orientHopperPlacement(int(use.Direction)); ok {
			state = hopperState
		}
	}
	if !w.SetBlock(placeX, placeY, placeZ, state) {
		return
	}
	s.broadcastBlockUpdate(dim, placeX, placeY, placeZ, int32(state))
	s.updateRedstoneAround(dim, placeX, placeY, placeZ)
	s.updateFluidsAround(dim, placeX, placeY, placeZ)
	if blockName, hasName := s.blockNameOf(state); hasName && blockName == hopperBlockName {
		// 新放置的漏斗立即注册（无需等待周期扫描）。
		s.registerHopper(dim, placeX, placeY, placeZ)
	}
	// 方块落入实体体积时把实体推到方块顶面（原版 pushEntitiesUp 行为）：
	// 否则站在其上的玩家会嵌入方块并被反穿墙校验反复回拉。
	s.pushEntitiesUp(dim, w, placeX, placeY, placeZ, state)
	if player.gameModeID() != uint8(config.GameModeCreative) {
		// 生存模式消耗一个物品并同步槽位（创意模式不消耗）。
		slot := player.selectedHotbarSlot()
		stack.Count--
		player.inventory.Set(slot, stack)
		player.tryWrite(protocol.EncodeSetPlayerInventory(int32(slot), stack.AppendSlot(nil)))
	}
}

// pushEntitiesUp 把嵌入新放置方块 (x, y, z) 的实体向上推到方块顶面
// （原版 Block.pushEntitiesUp 行为）。玩家/生物/掉落物都处理（仅同维度）；
// 上方被挡住时不推。被推起的玩家通过传送包同步客户端位置。
// 仅由会话读循环（放置方块）调用；生物/掉落物字段在 entityMu 下修改。
func (s *Server) pushEntitiesUp(dim world.Dimension, w *world.World, x, y, z int, state uint16) {
	boxes := registry.BlockStateShape(state)
	if len(boxes) == 0 {
		return
	}
	for _, p := range s.playerSnapshot() {
		if !p.isJoined() || p.dimensionID() != dim {
			continue
		}
		px, py, pz, yaw, pitch := p.playerPosition()
		delta := pushUpDelta(playerBox(px, py, pz), x, y, z, boxes)
		if delta <= 0 || w.Collides(playerBox(px, py+delta, pz)) {
			continue // 不相交或上方被挡住
		}
		p.setPlayerPosition(px, py+delta, pz, yaw, pitch)
		p.resyncPosition()
	}
	type pushMove struct {
		packet []byte
		x, z   float64
	}
	var moves []pushMove
	s.entityMu.Lock()
	for _, m := range s.mobs {
		if m.Dim != dim {
			continue
		}
		delta := pushUpDelta(mobBox(m.X, m.Y, m.Z), x, y, z, boxes)
		if delta <= 0 || w.Collides(mobBox(m.X, m.Y+delta, m.Z)) {
			continue
		}
		m.Y += delta
		moves = append(moves, pushMove{
			packet: protocol.EncodeEntityPositionSync(m.ID, m.X, m.Y, m.Z, 0, 0, 0, m.Yaw, m.Pitch, true),
			x:      m.X,
			z:      m.Z,
		})
	}
	for _, e := range s.items {
		if e.Dim != dim {
			continue
		}
		delta := pushUpDelta(itemBoxAt(e.X, e.Y, e.Z), x, y, z, boxes)
		if delta <= 0 || w.Collides(itemBoxAt(e.X, e.Y+delta, e.Z)) {
			continue
		}
		e.Y += delta
		moves = append(moves, pushMove{
			packet: protocol.EncodeEntityPositionSync(e.ID, e.X, e.Y, e.Z, e.VelX, e.VelY, e.VelZ, 0, 0, true),
			x:      e.X,
			z:      e.Z,
		})
	}
	s.entityMu.Unlock()
	for _, move := range moves {
		s.writeToNearbyPlayers(move.packet, dim, move.x, move.z, nil)
	}
}

// pushUpDelta 返回把碰撞盒推离方块 (x, y, z) 的形状所需的最小向上位移；
// 与形状不相交时返回 0。
func pushUpDelta(box world.Box, x, y, z int, boxes []registry.ShapeBox) float64 {
	delta := 0.0
	for _, shape := range boxes {
		sx1 := float64(x) + float64(shape.X1)/16
		sy1 := float64(y) + float64(shape.Y1)/16
		sz1 := float64(z) + float64(shape.Z1)/16
		sx2 := float64(x) + float64(shape.X2)/16
		sy2 := float64(y) + float64(shape.Y2)/16
		sz2 := float64(z) + float64(shape.Z2)/16
		if box.MinX >= sx2 || box.MaxX <= sx1 || box.MinZ >= sz2 || box.MaxZ <= sz1 {
			continue
		}
		if box.MinY >= sy2 || box.MaxY <= sy1 {
			continue
		}
		if d := sy2 - box.MinY; d > delta {
			delta = d
		}
	}
	return delta
}

// mobBox 返回生物碰撞盒（宽 0.6、高 1.95，僵尸尺寸）。
func mobBox(x, y, z float64) world.Box {
	return world.Box{
		MinX: x - 0.3, MinY: y, MinZ: z - 0.3,
		MaxX: x + 0.3, MaxY: y + 1.95, MaxZ: z + 0.3,
	}
}

// handleSetCarriedItem 记录玩家切换的快捷栏槽位（放置时使用）。
func (s *Server) handleSetCarriedItem(player *session, slot int32) {
	// 切换快捷栏物品会取消进食/弓蓄力/盾牌格挡（与原版切换物品一致）。
	player.cancelUseItem()
	player.setSelectedHotbarSlot(int(slot))
}

// handleSetCreativeSlot 处理创造模式物品栏设置（Set Creative Mode Slot）。
//
// 线上槽位号是“玩家物品栏菜单”编号（与 vanilla handleSetCreativeModeSlot
// 经 inventoryMenu 写入一致）：1–4 合成格、5–8 盔甲（头/胸/腿/脚）、9–35 主背包、
// 36–44 快捷栏、45 副手；需要映射到物品栏内部槽位后再存储与回执，
// 否则创造模式把物品放到快捷栏会被存进盔甲/副手槽，客户端物品错乱。
// 0–4（合成结果与合成格）在本实现中没有持久槽位，忽略。
// 负数槽位与原版一致：把物品掉落到世界中（创造界面丢弃手势）。
func (s *Server) handleSetCreativeSlot(player *session, slot int, itemID int32, count int32) {
	if player.gameModeID() != uint8(config.GameModeCreative) {
		slog.Debug("非创造模式忽略了物品栏设置", "name", player.name)
		return
	}
	if slot < 0 {
		s.dropCreativeTrash(player, itemID, count)
		return
	}
	invSlot, ok := playerInvForMenuSlot(slot)
	if !ok {
		return
	}
	if count <= 0 || itemID <= 0 {
		player.inventory.Set(invSlot, item.Empty())
		player.tryWrite(protocol.EncodeSetPlayerInventory(int32(invSlot), item.Empty().AppendSlot(nil)))
		return
	}
	name, ok := registry.ItemName(itemID)
	if !ok {
		slog.Debug("未知的物品 ID", "name", player.name, "itemID", itemID)
		return
	}
	stack, err := item.FromName(name, count)
	if err != nil {
		return
	}
	if stack.Count > stack.MaxStack() {
		return // 与原版一致：超过堆叠上限的设置被忽略
	}
	player.inventory.Set(invSlot, stack)
	player.tryWrite(protocol.EncodeSetPlayerInventory(int32(invSlot), stack.AppendSlot(nil)))
}

// dropCreativeTrash 处理创造模式物品栏的负数槽位设置：把物品沿视线抛出。
func (s *Server) dropCreativeTrash(player *session, itemID int32, count int32) {
	if count <= 0 || itemID <= 0 {
		return
	}
	name, ok := registry.ItemName(itemID)
	if !ok {
		return
	}
	stack, err := item.FromName(name, count)
	if err != nil || stack.Count > stack.MaxStack() {
		return
	}
	x, y, z, yaw, pitch := player.playerPosition()
	yawRad := float64(yaw) * math.Pi / 180
	pitchRad := float64(pitch) * math.Pi / 180
	const throwSpeed = 0.3
	s.spawnItem(player.dimensionID(), stack, x, y+1.0, z,
		-math.Cos(pitchRad)*math.Sin(yawRad)*throwSpeed,
		-math.Sin(pitchRad)*throwSpeed+0.1,
		math.Cos(pitchRad)*math.Cos(yawRad)*throwSpeed,
		itemPickupDelayPlayer)
}

// broadcastBlockUpdate 把方块更新发送给指定维度内附近的玩家。
func (s *Server) broadcastBlockUpdate(dim world.Dimension, x, y, z int, state int32) {
	// 方块变化通知观察该位置的侦测器（脉冲输出）。
	s.notifyObservers(dim, x, y, z)
	packet := protocol.EncodeBlockUpdate(x, y, z, state)
	bx := float64(x) + 0.5
	bz := float64(z) + 0.5
	for _, player := range s.playerSnapshot() {
		if !player.isJoined() || player.dimensionID() != dim {
			continue
		}
		px, _, pz, _, _ := player.playerPosition()
		if math.Hypot(px-bx, pz-bz) > mobMoveBroadcastRange {
			continue
		}
		player.tryWrite(packet)
	}
}
