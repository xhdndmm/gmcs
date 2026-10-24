//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"gmcs/internal/item"
	"gmcs/internal/protocol"
)

// 玩家装备同步（Set Equipment，0x64）：让其他玩家看到手持物品与盔甲。
//
// 变更检测：物品栏由会话串行访问，因此 6 个装备槽的快照在会话读循环里维护
// （每收到一个数据包刷新一次；原版客户端每 tick 都会发包），Tick 线程只读取
// 快照，在有变更时把全部 6 个槽位广播给附近玩家（与原版 `LivingEntity`
// 逐 tick 检测装备变更后 `broadcastAndSend` 一致；一次发全 6 槽可保证
// “槽位变空”也能同步给所有观察者）。
//
// 槽位来源：主手 = 当前选中的快捷栏槽位，副手 = 40，靴子/护腿/胸甲/头盔 = 36–39。
// 生物装备（僵尸穿盔甲等）gmcs 未实现，因此本文件只处理玩家。

// playerEquipmentStacks 把玩家的 6 个装备槽写入 out（索引即 Set Equipment 槽位编号）。
func playerEquipmentStacks(player *session, out *[protocol.EquipmentSlotCount]item.Stack) {
	out[protocol.EquipmentMainHand] = player.inventory.Get(player.selectedHotbarSlot())
	out[protocol.EquipmentOffHand] = player.inventory.Get(item.SlotOffhand)
	out[protocol.EquipmentFeet] = player.inventory.Get(item.SlotFeet)
	out[protocol.EquipmentLegs] = player.inventory.Get(item.SlotLegs)
	out[protocol.EquipmentChest] = player.inventory.Get(item.SlotChest)
	out[protocol.EquipmentHead] = player.inventory.Get(item.SlotHead)
}

// encodeEquipmentPacket 把 6 个装备槽编码为 Set Equipment 包（含空槽，
// 空槽负载为单个 0x00，客户端会清空该槽位）。
func encodeEquipmentPacket(entityID int32, stacks *[protocol.EquipmentSlotCount]item.Stack) []byte {
	entries := make([]protocol.EquipmentSlotEntry, 0, protocol.EquipmentSlotCount)
	for slot := byte(0); slot < protocol.EquipmentSlotCount; slot++ {
		entries = append(entries, protocol.EquipmentSlotEntry{
			Slot: slot,
			Data: stacks[slot].AppendSlot(nil),
		})
	}
	return protocol.EncodeSetEquipment(entityID, entries)
}

// refreshEquipmentSnapshot 重算装备快照并在有变化时标记待广播。
// 必须在会话读循环（会话 goroutine）内调用：物品栏由会话串行访问。
func (s *session) refreshEquipmentSnapshot() {
	var current [protocol.EquipmentSlotCount]item.Stack
	playerEquipmentStacks(s, &current)
	s.stateMu.Lock()
	changed := false
	for i := range current {
		if !current[i].Equal(s.equipSnapshot[i]) {
			changed = true
			break
		}
	}
	if changed {
		s.equipSnapshot = current
		s.equipPending = true
	}
	// 呼吸附魔影响氧气上限：与装备快照一起更新（Tick 线程只读）。
	if level := enchantLevel(current[protocol.EquipmentHead], enchants().respiration); level > 0 {
		s.airCapacity.Store(int32(maxPlayerAir + int(level)*20))
	} else {
		s.airCapacity.Store(maxPlayerAir)
	}
	s.stateMu.Unlock()
}

// equipmentSnapshot 返回当前装备快照（只读，不清除待广播标记）。
func (s *session) equipmentSnapshot() [protocol.EquipmentSlotCount]item.Stack {
	s.stateMu.Lock()
	snapshot := s.equipSnapshot
	s.stateMu.Unlock()
	return snapshot
}

// takeEquipmentChange 返回快照并报告自上次调用以来是否有变化（Tick 线程调用）。
func (s *session) takeEquipmentChange() ([protocol.EquipmentSlotCount]item.Stack, bool) {
	s.stateMu.Lock()
	snapshot := s.equipSnapshot
	pending := s.equipPending
	s.equipPending = false
	s.stateMu.Unlock()
	return snapshot, pending
}

// tickPlayerEquipment 把装备变化同步给附近玩家（每 tick 检查一次快照）。
func (s *Server) tickPlayerEquipment(players []*session) {
	if !s.playerEntitiesEnabled {
		return
	}
	for _, player := range players {
		if !player.isJoined() {
			continue
		}
		snapshot, changed := player.takeEquipmentChange()
		if !changed {
			continue
		}
		x, _, z, _, _ := player.playerPosition()
		s.writeToNearbyPlayers(encodeEquipmentPacket(player.entityID, &snapshot),
			player.dimensionID(), x, z, nil)
	}
}

// playerEquipmentPacket 构造玩家当前装备的完整 Set Equipment 包
// （用于玩家实体出现在其他玩家视野中：进入世界、维度切换、新玩家加入时）。
func (s *Server) playerEquipmentPacket(player *session) []byte {
	snapshot := player.equipmentSnapshot()
	return encodeEquipmentPacket(player.entityID, &snapshot)
}
