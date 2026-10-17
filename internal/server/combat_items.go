//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"
	"time"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
)

// 玩家远程武器与盾牌：
//
//   - 弓：Use Item 开始蓄力（主手/副手均可），释放使用键时按蓄力比例
//     （0..1，1 秒满蓄力）发射箭矢；最低蓄力 0.1 才发射。箭速 = 蓄力×3 格/tick，
//     伤害 = ceil(箭速 × (2 + 0.5×力量等级))；力量/冲击/火矢/无限附魔生效，
//     无限仍需至少 1 支箭；创造模式不消耗箭与耐久；每次发射损耗 1 点弓耐久
//     （不灭附魔可免除）。箭矢命中生物/玩家并结算伤害、击退与火焰。
//   - 盾牌：Use Item 开始格挡，释放取消；受到来自正面、且不属于
//     minecraft:bypasses_shield 标签的伤害时完全格挡，并按 1+floor(伤害)
//     损耗盾牌耐久（不灭附魔可免除）；盾牌损坏时取消格挡。
//
// 已知限制（docs/TODO.md）：弩（蓄力/多重射击/穿透）、横扫之刃、三叉戟未实现；
// 箭矢不保留在世界上供拾取；盾牌不实现斧头破盾与 5 tick 禁用。

const (
	// bowFullCharge 是满蓄力时长（原版 20 tick = 1 秒）。
	bowFullCharge = time.Second
	// bowMinPower 是最低可发射蓄力（原版 getPowerForTime < 0.1 不发射）。
	bowMinPower = 0.1
	// bowArrowSpeed 是满蓄力箭速（格/tick）。
	bowArrowSpeed = 3.0
	// bowBaseDamage 是箭矢基础伤害系数（伤害 = 箭速 × (2 + 0.5×力量等级)）。
	bowBaseDamage = 2.0
	// shieldBlockFireTicks 是火矢命中后的着火时长（5 秒）。
	arrowFlameTicks = 5 * 20
)

// releaseUseItem 处理释放使用键：弓发射箭矢，其余使用状态取消。
func (s *Server) releaseUseItem(player *session) {
	if player.bowCharging {
		s.firePlayerBow(player)
	}
	player.cancelUseItem()
}

// findArrowSlot 返回物品栏中第一组箭的槽位（-1 表示没有）。
// 顺序为快捷栏/主背包，最后副手。
func findArrowSlot(player *session) int {
	for slot := item.SlotHotbarStart; slot <= item.SlotMainEnd; slot++ {
		stack := player.inventory.Get(slot)
		if stack.IsEmpty() {
			continue
		}
		if name, ok := registry.ItemName(stack.ItemID); ok && name == "minecraft:arrow" {
			return slot
		}
	}
	stack := player.inventory.Get(item.SlotOffhand)
	if !stack.IsEmpty() {
		if name, ok := registry.ItemName(stack.ItemID); ok && name == "minecraft:arrow" {
			return item.SlotOffhand
		}
	}
	return -1
}

// consumeArrow 从指定槽位消耗 1 支箭并同步客户端。
func consumeArrow(player *session, slot int) {
	stack := player.inventory.Get(slot)
	if stack.IsEmpty() {
		return
	}
	stack.Count--
	if stack.Count <= 0 {
		stack = item.Empty()
	}
	player.inventory.Set(slot, stack)
	player.tryWrite(protocol.EncodeSetPlayerInventory(int32(slot), stack.AppendSlot(nil)))
}

// firePlayerBow 在释放使用键时按蓄力发射一支玩家箭矢。
// 仅由会话读循环调用（直接访问物品栏与使用状态）。
func (s *Server) firePlayerBow(player *session) {
	if !s.arrowsEnabled {
		return
	}
	slot := player.bowSlot
	bow := player.inventory.Get(slot)
	if bow.IsEmpty() || bow.ItemID != player.bowItemID {
		return
	}
	if name, ok := registry.ItemName(bow.ItemID); !ok || name != "minecraft:bow" {
		return
	}
	elapsed := time.Since(player.bowStart)
	power := float32(elapsed) / float32(bowFullCharge)
	power = min(max(power, 0), 1)
	if power < bowMinPower {
		return
	}

	creative := player.gameModeID() == uint8(config.GameModeCreative)
	ids := enchants()
	infinity := enchantLevel(bow, ids.infinity) > 0
	arrowSlot := findArrowSlot(player)
	if !creative && arrowSlot < 0 {
		return // 没有箭（无限附魔同样需要至少 1 支箭）
	}
	if !creative && !infinity {
		consumeArrow(player, arrowSlot)
	}

	x, y, z, yaw, pitch := player.playerPosition()
	yawRad := float64(yaw) * math.Pi / 180
	pitchRad := float64(pitch) * math.Pi / 180
	dirX := -math.Cos(pitchRad) * math.Sin(yawRad)
	dirY := -math.Sin(pitchRad)
	dirZ := math.Cos(pitchRad) * math.Cos(yawRad)
	speed := float64(power) * bowArrowSpeed

	damage := float32(math.Ceil(speed * (bowBaseDamage + 0.5*float64(enchantLevel(bow, ids.power)))))
	arrow := s.spawnArrow(player.dimensionID(), x, y+playerEyeHeight-0.1, z,
		dirX*speed, dirY*speed, dirZ*speed, damage, player.entityID,
		enchantLevel(bow, ids.punch), enchantLevel(bow, ids.flame) > 0)
	if arrow == nil {
		return
	}
	s.broadcastToNearby(player.dimensionID(),
		protocol.EncodeEntitySoundEffect(s.soundArrowShoot, protocol.SoundCategoryPlayer, player.entityID, 1, 1, 0),
		x, z, s.playerSnapshot())

	// 弓耐久：创造模式不损耗；不灭附魔按原版概率免除。
	if !creative && !unbreakingSavesDurability(s, bow, false) {
		player.damageInventoryItem(slot, 1)
	}
}

// tryBlockDamage 在玩家格挡且伤害来自正面时完全格挡本次伤害，
// 损耗盾牌耐久并播放格挡音效。返回是否已格挡。
// 可在 Tick 或读循环调用（只读写物品栏与会话使用状态）。
func (s *session) tryBlockDamage(server *Server, amount float32, damageTypeID int32, sourcePosition *[3]float64) bool {
	if amount <= 0 || !s.blocking.Load() || s.isDead() || sourcePosition == nil {
		return false
	}
	switch s.gameModeID() {
	case uint8(config.GameModeCreative), uint8(config.GameModeSpectator):
		return false // 创造/旁观免伤，不消耗盾牌
	}
	slot := int(s.blockSlot.Load())
	stack := s.inventory.Get(slot)
	if stack.IsEmpty() || stack.ItemID != s.blockItemID.Load() {
		s.blocking.Store(false)
		return false
	}
	if name, ok := registry.ItemName(stack.ItemID); !ok || name != "minecraft:shield" {
		s.blocking.Store(false)
		return false
	}
	if registry.DamageTypeInTag("minecraft:bypasses_shield", damageTypeID) {
		return false
	}
	// 方向：伤害来源必须位于玩家视线前方（水平方向点积 > 0）。
	px, _, pz, yaw, _ := s.playerPosition()
	yawRad := float64(yaw) * math.Pi / 180
	lookX := -math.Sin(yawRad)
	lookZ := math.Cos(yawRad)
	dx := sourcePosition[0] - px
	dz := sourcePosition[2] - pz
	if dx*lookX+dz*lookZ <= 0 {
		return false
	}

	// 盾牌耐久：原版 1 + floor(伤害)。
	damage := int32(1 + math.Floor(float64(amount)))
	if !unbreakingSavesDurability(server, stack, false) {
		s.damageInventoryItem(slot, damage)
	}
	if s.inventory.Get(slot).IsEmpty() {
		s.blocking.Store(false) // 盾牌损坏
	}
	if server.soundShieldBlock != 0 {
		x, y, z, _, _ := s.playerPosition()
		server.broadcastToNearby(s.dimensionID(),
			protocol.EncodeSoundEffect(server.soundShieldBlock, protocol.SoundCategoryPlayer,
				x, y+playerEyeHeight, z, 1, 1, 0),
			x, z, server.playerSnapshot())
	}
	return true
}
