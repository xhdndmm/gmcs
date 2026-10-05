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
	current := s.world.BlockAt(action.X, action.Y, action.Z)
	if current == world.AirBlock || current == world.WaterBlock || current == world.BedrockBlock {
		return // 空气/水/基岩不可破坏
	}
	if !s.world.SetBlock(action.X, action.Y, action.Z, world.AirBlock) {
		return
	}
	s.broadcastBlockUpdate(action.X, action.Y, action.Z, int32(world.AirBlock))
	if player.gameModeID() != uint8(config.GameModeCreative) {
		// 生存模式掉落（创意模式破坏不掉落物品，与原版一致）。
		s.dropBlockItem(current, action.X, action.Y, action.Z)
	}
}

// blockFaceOffsets 是 Use Item On 的六个朝向对应的放置偏移（-Y、+Y、-Z、+Z、-X、+X）。
var blockFaceOffsets = [6][3]int{{0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0}}

// handleUseItemOn 处理 Use Item On（block_place）包：放置方块。
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
	offset := blockFaceOffsets[use.Direction]
	placeX := use.X + offset[0]
	placeY := use.Y + offset[1]
	placeZ := use.Z + offset[2]
	if _, ok := world.SectionIndex(placeY); !ok {
		return
	}
	existing := s.world.BlockAt(placeX, placeY, placeZ)
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
		return // 手持物品不是可放置的方块
	}
	if !s.world.SetBlock(placeX, placeY, placeZ, state) {
		return
	}
	s.broadcastBlockUpdate(placeX, placeY, placeZ, int32(state))
	if player.gameModeID() != uint8(config.GameModeCreative) {
		// 生存模式消耗一个物品并同步槽位（创意模式不消耗）。
		stack.Count--
		player.inventory.Set(player.selectedSlot, stack)
		player.tryWrite(protocol.EncodeSetPlayerInventory(int32(player.selectedSlot), stack.AppendSlot(nil)))
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

// broadcastBlockUpdate 把方块更新发送给附近已完成进入世界的玩家。
func (s *Server) broadcastBlockUpdate(x, y, z int, state int32) {
	packet := protocol.EncodeBlockUpdate(x, y, z, state)
	bx := float64(x) + 0.5
	bz := float64(z) + 0.5
	for _, player := range s.playerSnapshot() {
		if !player.isJoined() {
			continue
		}
		px, _, pz, _, _ := player.playerPosition()
		if math.Hypot(px-bx, pz-bz) > mobMoveBroadcastRange {
			continue
		}
		player.tryWrite(packet)
	}
}
