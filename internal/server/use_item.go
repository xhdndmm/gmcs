//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"log/slog"
	"time"

	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
)

// 使用物品（Use Item，serverbound 0x40）：进食、弓蓄力与盾牌格挡。
//
// 与原版一致的 1.6 秒（32 tick）进食过程：首次使用开始进食，达到时长后
// 结算（消耗物品并应用饥饿/生命恢复）；收到 Player Action 的释放动作
// （release_use_item）时取消。进度检查在读循环中按到达的数据包执行——
// 客户端每 tick 发送 Client Tick End，因此实际接近逐 tick 检查。
// 不实现进食粒子/音效（客户端本地播放）。
//
// hand：0 = 主手（选中的快捷栏槽位），1 = 副手。弓与盾牌可来自任一手。

// eatDuration 是进食时长（原版 1.6 秒）。
const eatDuration = 1600 * time.Millisecond

// handleUseItem 处理使用物品请求：开始或继续进食、弓蓄力或盾牌格挡。
// 由会话读循环调用。
func (s *Server) handleUseItem(player *session, hand int32) {
	if player.isDead() {
		return
	}
	switch player.gameModeID() {
	case 0, 1, 2: // 生存/创造/冒险可以使用物品（与原版一致：仅旁观不能）。
	default:
		return
	}
	slot := player.selectedHotbarSlot()
	if hand == 1 {
		slot = item.SlotOffhand
	}
	stack := player.inventory.Get(slot)
	if stack.IsEmpty() {
		player.cancelUseItem()
		return
	}
	name, known := registry.ItemName(stack.ItemID)
	if !known {
		return
	}

	// 已在使用同类物品：保持状态（弓蓄力不重置开始时刻）。
	if player.eating && slot == player.eatingSlot && stack.ItemID == player.eatingItemID {
		return
	}
	if player.bowCharging && slot == player.bowSlot && stack.ItemID == player.bowItemID {
		return
	}
	if player.blocking.Load() && slot == int(player.blockSlot.Load()) && stack.ItemID == player.blockItemID.Load() {
		return
	}
	// 切换到其它物品：取消所有使用状态。
	player.cancelUseItem()

	switch name {
	case "minecraft:bow":
		player.bowCharging = true
		player.bowSlot = slot
		player.bowItemID = stack.ItemID
		player.bowStart = time.Now()
		return
	case "minecraft:shield":
		player.blocking.Store(true)
		player.blockSlot.Store(int32(slot))
		player.blockItemID.Store(stack.ItemID)
		return
	}

	// 药水、牛奶与金苹果也复用 1.6 秒使用过程；药水即使饥饿/生命已满也可饮用。
	consumable := name == "minecraft:potion" || name == "minecraft:milk_bucket" ||
		name == "minecraft:golden_apple" || name == "minecraft:enchanted_golden_apple"
	if _, isFood := registry.Food(stack.ItemID); !isFood && !consumable {
		return
	}
	if !consumable {
		if health, food, _ := player.healthStatus(); food >= maxPlayerFood && health >= maxPlayerHealth {
			return // 饥饿值与生命均满时不开始进食（近似原版）
		}
	}
	player.eating = true
	player.eatingSlot = slot
	player.eatingItemID = stack.ItemID
	player.eatDeadline = time.Now().Add(eatDuration)
}

// updateEating 由读循环在每个数据包后调用：到时完成进食。
func (s *session) updateEating() {
	if !s.eating {
		return
	}
	if time.Now().Before(s.eatDeadline) {
		return
	}
	s.completeEating()
}

// completeEating 结算进食：消耗物品并应用饥饿/生命恢复，同步物品槽与状态。
func (s *session) completeEating() {
	s.eating = false
	slot := s.eatingSlot
	stack := s.inventory.Get(slot)
	if stack.IsEmpty() || stack.ItemID != s.eatingItemID {
		return // 物品已被移动/替换
	}
	name, _ := registry.ItemName(s.eatingItemID)
	switch name {
	case "minecraft:potion":
		potionID := stack.PotionID
		if potionID > 0 {
			for _, packet := range s.server.applyPotionEffects(s, registry.PotionEffectList(potionID-1)) {
				s.tryWrite(packet)
			}
		}
	case "minecraft:milk_bucket":
		for _, packet := range s.clearEffects() {
			s.tryWrite(packet)
		}
		if bucket, err := item.FromName("minecraft:bucket", 1); err == nil {
			stack = bucket
			s.inventory.Set(slot, stack)
			s.tryWrite(protocol.EncodeSetPlayerInventory(int32(slot), stack.AppendSlot(nil)))
		} else {
			stack = item.Empty()
			s.inventory.Set(slot, stack)
			s.tryWrite(protocol.EncodeSetPlayerInventory(int32(slot), stack.AppendSlot(nil)))
		}
		return
	default:
		if name == "minecraft:golden_apple" || name == "minecraft:enchanted_golden_apple" {
			// 金苹果即使饥饿值已满也可以食用；效果仍应结算。
			s.eatFoodAllowFull(stack.ItemID, true)
		} else if !s.eatFood(stack.ItemID) {
			return // 不再是食物或已吃饱
		}
		if name == "minecraft:golden_apple" {
			for _, packet := range s.server.applyPotionEffects(s, []registry.PotionEffect{
				{ID: registry.MobEffectAbsorption, Duration: 2400, Amplifier: 0},
				{ID: registry.MobEffectRegeneration, Duration: 100, Amplifier: 1},
			}) {
				s.tryWrite(packet)
			}
		} else if name == "minecraft:enchanted_golden_apple" {
			for _, packet := range s.server.applyPotionEffects(s, []registry.PotionEffect{
				{ID: registry.MobEffectAbsorption, Duration: 2400, Amplifier: 3},
				{ID: registry.MobEffectRegeneration, Duration: 400, Amplifier: 1},
				{ID: registry.MobEffectResistance, Duration: 6000, Amplifier: 0},
				{ID: registry.MobEffectFireResistance, Duration: 6000, Amplifier: 0},
			}) {
				s.tryWrite(packet)
			}
		}
	}
	stack.Count--
	if stack.Count <= 0 {
		stack = item.Empty()
	}
	s.inventory.Set(slot, stack)
	s.tryWrite(protocol.EncodeSetPlayerInventory(int32(slot), stack.AppendSlot(nil)))
	health, food, saturation := s.healthStatus()
	s.tryWrite(protocol.EncodeUpdateHealth(health, food, saturation))
	if name != "" {
		slog.Debug("player consumed item", "name", s.name, "item", name)
	}
}

// cancelEating 取消进行中的进食（释放动作/切换物品/死亡等）。
func (s *session) cancelEating() {
	s.eating = false
}

// cancelUseItem 取消全部使用状态（进食/弓蓄力/盾牌格挡）。
// 由读循环调用。
func (s *session) cancelUseItem() {
	s.eating = false
	s.bowCharging = false
	s.blocking.Store(false)
}
