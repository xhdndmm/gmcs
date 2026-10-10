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

// 方块交互：破坏与放置。服务端校验距离与方块后修改世界，并把
// Block Update 广播给附近玩家；体素修改会随区块保存（Flush/卸载）持久化。
//
// 当前为简化实现：挖掘没有时间（生存模式在客户端完成后提交
// STOP_DESTROY_BLOCK，创意模式在按下瞬间生效），掉落物按简化掉落表生成
// （无工具/精准采集/时运与概率掉落，见 block_drops.go），放置只支持
// 不携带数据组件的方块物品；冒险与旁观模式不支持交互。

const (
	// survivalReach / creativeReach 是方块交互的最大距离（相对玩家眼睛）。
	// 与原版相比略宽松，避免网络延迟造成的误判。
	survivalReach = 5.0
	creativeReach = 6.0
	// playerEyeHeight 是玩家眼睛相对脚部的高度。
	playerEyeHeight = 1.62
)

// interactReach 返回会话对应游戏模式的方块交互距离。
func (s *session) interactReach() float64 {
	if s.gameModeID() == uint8(config.GameModeCreative) {
		return creativeReach
	}
	return survivalReach
}

// canInteractBlocks 报告玩家当前是否允许破坏/放置方块。
func (s *session) canInteractBlocks() bool {
	switch s.gameModeID() {
	case uint8(config.GameModeCreative), uint8(config.GameModeSurvival):
		return true
	}
	return false
}

// withinBlockReach 报告方块中心是否落在玩家的交互距离内。
func (s *session) withinBlockReach(x, y, z int) bool {
	px, py, pz, _, _ := s.playerPosition()
	dx := px - (float64(x) + 0.5)
	dy := py + playerEyeHeight - (float64(y) + 0.5)
	dz := pz - (float64(z) + 0.5)
	return math.Sqrt(dx*dx+dy*dy+dz*dz) <= s.interactReach()
}

// handlePlayerAction 处理 Player Action（block_dig）包：破坏方块与丢弃物品。
func (s *Server) handlePlayerAction(player *session, action protocol.PlayerAction) {
	const (
		statusStartDestroy = 0
		statusStopDestroy  = 2
		statusDropStack    = 3 // Ctrl+Q：丢弃整组。
		statusDropItem     = 4 // Q：丢弃 1 个。
	)
	if action.Status == statusDropStack || action.Status == statusDropItem {
		s.dropItemFromPlayer(player, action.Status == statusDropStack)
		return
	}
	if !player.canInteractBlocks() || player.isDead() {
		return
	}
	if player.gameModeID() == uint8(config.GameModeCreative) {
		if action.Status != statusStartDestroy {
			return // 创意模式：按下即时破坏
		}
	} else if action.Status != statusStopDestroy {
		return // 生存模式：客户端完成挖掘后提交
	}
	if !player.withinBlockReach(action.X, action.Y, action.Z) {
		return
	}
	if _, ok := world.SectionIndex(action.Y); !ok {
		return
	}
	dim := player.dimensionID()
	w := s.worldFor(dim)
	current := w.BlockAt(action.X, action.Y, action.Z)
	if current == world.AirBlock || current == world.WaterBlock || current == world.BedrockBlock {
		return // 空气/水/基岩不可破坏
	}
	// 破坏容器方块：先关闭观察窗口，再把内容物掉落到世界中（与原版一致）。
	if def, _, ok := s.containerForState(current); ok {
		if def.IsFurnace {
			s.destroyFurnace(dim, player, action.X, action.Y, action.Z, def)
		} else {
			s.destroyContainer(dim, player, action.X, action.Y, action.Z, def)
		}
	}
	if !w.SetBlock(action.X, action.Y, action.Z, world.AirBlock) {
		return
	}
	s.broadcastBlockUpdate(dim, action.X, action.Y, action.Z, int32(world.AirBlock))
	s.updateRedstoneAround(dim, action.X, action.Y, action.Z)
	if player.gameModeID() != uint8(config.GameModeCreative) {
		// 生存模式掉落（创意模式破坏不掉落物品，与原版一致）。
		s.dropBlockItem(dim, current, action.X, action.Y, action.Z)
	}
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
	if !player.sneaking {
		current := w.BlockAt(use.X, use.Y, use.Z)
		// 拉杆/按钮：切换红石状态。
		if s.handleRedstoneUse(w, use.X, use.Y, use.Z, current) {
			return
		}
		if def, blockName, ok := s.containerForState(current); ok {
			s.openContainer(player, use.X, use.Y, use.Z, def, blockName)
			return
		}
		// 右键工作台：打开 3×3 合成窗口。
		name, hasName := s.blockNames[current]
		if hasName && name == "minecraft:crafting_table" {
			s.openCraftingTable(player)
			return
		}
		// 右键末影箱：打开玩家自己的末影箱（内容随玩家）。
		if hasName && name == "minecraft:ender_chest" {
			s.openEnderChest(player)
			return
		}
	}
	offset := blockFaceOffsets[use.Direction]
	placeX := use.X + offset[0]
	placeY := use.Y + offset[1]
	placeZ := use.Z + offset[2]
	if _, ok := world.SectionIndex(placeY); !ok {
		return
	}
	existing := w.BlockAt(placeX, placeY, placeZ)
	if existing != world.AirBlock && existing != world.WaterBlock {
		return // 目标位置不可替换
	}
	stack := player.inventory.Get(player.selectedSlot)
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
	if !w.SetBlock(placeX, placeY, placeZ, state) {
		return
	}
	s.broadcastBlockUpdate(dim, placeX, placeY, placeZ, int32(state))
	s.updateRedstoneAround(dim, placeX, placeY, placeZ)
	// 方块落入实体体积时把实体推到方块顶面（原版 pushEntitiesUp 行为）：
	// 否则站在其上的玩家会嵌入方块并被反穿墙校验反复回拉。
	s.pushEntitiesUp(dim, w, placeX, placeY, placeZ, state)
	if player.gameModeID() != uint8(config.GameModeCreative) {
		// 生存模式消耗一个物品并同步槽位（创意模式不消耗）。
		stack.Count--
		player.inventory.Set(player.selectedSlot, stack)
		player.tryWrite(protocol.EncodeSetPlayerInventory(int32(player.selectedSlot), stack.AppendSlot(nil)))
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
	player.selectedSlot = int(slot)
}

// handleSetCreativeSlot 处理创造模式物品栏设置（Set Creative Mode Slot）。
func (s *Server) handleSetCreativeSlot(player *session, slot int, itemID int32, count int32) {
	if player.gameModeID() != uint8(config.GameModeCreative) {
		slog.Debug("非创造模式忽略了物品栏设置", "name", player.name)
		return
	}
	if slot < 0 || slot >= item.InventorySlots {
		return
	}
	if count <= 0 || itemID <= 0 {
		player.inventory.Set(slot, item.Empty())
		player.tryWrite(protocol.EncodeSetPlayerInventory(int32(slot), item.Empty().AppendSlot(nil)))
		return
	}
	name, ok := registry.ItemName(itemID)
	if !ok {
		slog.Debug("未知的物品 ID", "name", player.name, "itemID", itemID)
		return
	}
	if count > 64 {
		count = 64
	}
	stack, err := item.FromName(name, count)
	if err != nil {
		return
	}
	player.inventory.Set(slot, stack)
	player.tryWrite(protocol.EncodeSetPlayerInventory(int32(slot), stack.AppendSlot(nil)))
}

// broadcastBlockUpdate 把方块更新发送给指定维度内附近的玩家。
func (s *Server) broadcastBlockUpdate(dim world.Dimension, x, y, z int, state int32) {
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
