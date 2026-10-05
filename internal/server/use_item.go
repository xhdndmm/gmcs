package server

import (
	"log/slog"

	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
)

// 使用物品（Use Item，serverbound 0x40）：目前用于进食。
//
// 说明：未实现原版 1.6 秒的进食过程（进度条、被打断/移动取消）；服务器在收到
// 使用请求时立即结算，并同步该槽位数量。食物数值来自官方物品数据
// （registry.FoodValues）。

// handleUseItem 处理使用物品请求：手持食物时进食。
// 由会话读循环调用。
func (s *Server) handleUseItem(player *session) {
	if player.isDead() {
		return
	}
	switch player.gameModeID() {
	case 0, 1, 2: // 生存/创造/冒险可以进食（与原版一致：仅旁观不能）。
	default:
		return
	}
	slot := player.selectedSlot
	stack := player.inventory.Get(slot)
	if stack.IsEmpty() {
		return
	}
	consumed := stack
	if !player.eatFood(stack.ItemID) {
		return // 不是食物或已吃饱。
	}
	stack.Count--
	if stack.Count <= 0 {
		stack = item.Empty()
	}
	player.inventory.Set(slot, stack)
	player.tryWrite(protocol.EncodeSetPlayerInventory(int32(slot), stack.AppendSlot(nil)))
	health, food, saturation := player.healthStatus()
	player.tryWrite(protocol.EncodeUpdateHealth(health, food, saturation))

	if name, ok := registry.ItemName(consumed.ItemID); ok {
		slog.Debug("player ate food", "name", player.name, "item", name)
	}
}
