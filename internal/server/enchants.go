//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"
	"sync"

	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
)

// 附魔与护甲的数值效果。
//
// 数据来源（1.21.11）：
//   - 附魔数值来自服务端 jar 的 data/minecraft/enchantment/*.json（effects 段），
//     与 Enchantments 注册表 ID 一起由 registry 查询；
//   - 护甲值与韧性来自 reports/items.json 的 minecraft:attribute_modifiers
//     （registry.ArmorStats）；
//   - 伤害类型分类使用 data/minecraft/tags/damage_type 的标签
//     （registry.DamageTypeInTag：bypasses_armor / is_fall / is_fire /
//     is_explosion / is_projectile）。
//
// 实现范围（对照原版 LivingEntity / Player 字节码）：
//   - 护甲减伤 CombatRules.getDamageAfterAbsorb（护甲值、韧性、伤害值三者参与）；
//   - 护甲耐久：每次不穿透护甲的伤害按 max(1, 伤害/4) 损耗每件护甲，
//     不灭附魔按原版概率免除（护甲 (2L)/(10+5(L-1))、非护甲 L/(L+1)）；
//   - 保护类附魔：protection 每级 1 EPF、火焰/爆炸/弹射保护每级 2 EPF、
//     摔落保护每级 3 EPF，EPF 上限 20，最终伤害 ×(1 - EPF/25)；
//   - 近战：锋利 +1+0.5×(等级-1)、亡灵杀手/节肢杀手对对应标签 +2.5×等级、
//     击退 +0.5×等级、火焰附加 4 秒×等级、抢夺增加掉落；
//   - 挖掘：效率 +等级²+1（仅在工具速度 > 1 时）、水下挖掘 ÷5（水下速掘无效化）、
//     精准采集、时运（矿石/红石/青金石/西瓜/燧石/树苗掉落）、剪刀采集树叶；
//   - 经验修补：拾取经验时修复装备/物品栏中带 mending 的受损物品（每点经验 2 点耐久）；
//   - 绑定诅咒：生存模式不能从盔甲槽取出；消失诅咒：死亡时不掉落。
//
// 未覆盖（docs/TODO.md 已记录）：横扫之刃、穿刺/引雷/忠诚等三叉戟附魔、
// 深海探索者/迅捷潜行等移动属性（玩家移动由客户端负责）、经验修补在
// 铁砧/砂轮之外的统计行为、附魔对生物装备的效果（gmcs 生物不穿戴装备）。

// enchantIDs 是本次实现用到的附魔注册表 ID（首次使用时解析一次）。
type enchantIDs struct {
	sharpness            int32
	smite                int32
	baneOfArthropods     int32
	knockback            int32
	fireAspect           int32
	looting              int32
	efficiency           int32
	unbreaking           int32
	fortune              int32
	silkTouch            int32
	mending              int32
	protection           int32
	fireProtection       int32
	blastProtection      int32
	projectileProtection int32
	featherFalling       int32
	respiration          int32
	aquaAffinity         int32
	bindingCurse         int32
	vanishingCurse       int32
}

// enchants 返回附魔 ID 表；缺失条目返回 -1（调用方按“无此附魔”处理）。
var enchants = sync.OnceValue(func() enchantIDs {
	lookup := func(name string) int32 {
		id, ok := registry.EnchantmentID(name)
		if !ok {
			return -1
		}
		return id
	}
	return enchantIDs{
		sharpness:            lookup("minecraft:sharpness"),
		smite:                lookup("minecraft:smite"),
		baneOfArthropods:     lookup("minecraft:bane_of_arthropods"),
		knockback:            lookup("minecraft:knockback"),
		fireAspect:           lookup("minecraft:fire_aspect"),
		looting:              lookup("minecraft:looting"),
		efficiency:           lookup("minecraft:efficiency"),
		unbreaking:           lookup("minecraft:unbreaking"),
		fortune:              lookup("minecraft:fortune"),
		silkTouch:            lookup("minecraft:silk_touch"),
		mending:              lookup("minecraft:mending"),
		protection:           lookup("minecraft:protection"),
		fireProtection:       lookup("minecraft:fire_protection"),
		blastProtection:      lookup("minecraft:blast_protection"),
		projectileProtection: lookup("minecraft:projectile_protection"),
		featherFalling:       lookup("minecraft:feather_falling"),
		respiration:          lookup("minecraft:respiration"),
		aquaAffinity:         lookup("minecraft:aqua_affinity"),
		bindingCurse:         lookup("minecraft:binding_curse"),
		vanishingCurse:       lookup("minecraft:vanishing_curse"),
	}
})

// enchantLevel 返回堆栈上指定附魔的等级（附魔 ID < 0 时为 0）。
func enchantLevel(stack item.Stack, id int32) int32 {
	if id < 0 {
		return 0
	}
	return stack.EnchantmentLevel(id)
}

// linearEnchantValue 展开附魔 JSON 中的 linear 数值：
// base + per_level_above_first × (level - 1)，level ≥ 1。
func linearEnchantValue(base, perLevel float32, level int32) float32 {
	if level <= 0 {
		return 0
	}
	return base + perLevel*float32(level-1)
}

// meleeEnchantDamage 返回锋利/亡灵杀手/节肢杀手对目标的额外伤害
// （附魔 JSON：sharpness 1 + 0.5×(L-1)，smite/bane 2.5×L）。
func (s *Server) meleeEnchantDamage(held item.Stack, m *mob) float32 {
	ids := enchants()
	bonus := linearEnchantValue(1, 0.5, enchantLevel(held, ids.sharpness))
	entityType := s.mobTypeIDs[m.Kind]
	if registry.EntityTypeInTag("minecraft:sensitive_to_smite", entityType) {
		bonus += linearEnchantValue(2.5, 2.5, enchantLevel(held, ids.smite))
	}
	if registry.EntityTypeInTag("minecraft:sensitive_to_bane_of_arthropods", entityType) {
		bonus += linearEnchantValue(2.5, 2.5, enchantLevel(held, ids.baneOfArthropods))
	}
	return bonus
}

// armorValues 返回玩家当前装备提供的护甲值与韧性之和
// （原版 LivingEntity.getArmorValue / ARMOR_TOUGHNESS 属性）。
func (s *session) armorValues() (armor, toughness float32) {
	for _, slot := range [...]int{item.SlotFeet, item.SlotLegs, item.SlotChest, item.SlotHead} {
		stack := s.inventory.Get(slot)
		if stack.IsEmpty() {
			continue
		}
		stats, ok := registry.ArmorStats(stack.ItemID)
		if !ok {
			continue
		}
		armor += stats.Armor
		toughness += stats.Toughness
	}
	return armor, toughness
}

// damageAfterArmor 复刻原版 CombatRules.getDamageAfterAbsorb：
// 有效护甲 = clamp(护甲 - 伤害/(2 + 韧性/4), 护甲×0.2, 20)，伤害 ×(1 - 有效护甲/25)。
func damageAfterArmor(amount, armor, toughness float32) float32 {
	if amount <= 0 || armor <= 0 {
		return amount
	}
	effective := armor - amount/(2+toughness/4)
	effective = max(effective, armor*0.2)
	effective = min(effective, 20)
	return amount * (1 - effective/25)
}

// protectionEPF 返回护甲的保护类附魔有效保护点数（原版
// EnchantmentHelper.getDamageProtection；上限 20）。
func (s *session) protectionEPF(damageTypeID int32) float32 {
	// 原版保护类附魔的 requirement 都要求伤害不绕过无敌帧。
	if registry.DamageTypeInTag("minecraft:bypasses_invulnerability", damageTypeID) {
		return 0
	}
	ids := enchants()
	epf := float32(0)
	for _, slot := range [...]int{item.SlotFeet, item.SlotLegs, item.SlotChest, item.SlotHead} {
		stack := s.inventory.Get(slot)
		if stack.IsEmpty() {
			continue
		}
		epf += float32(enchantLevel(stack, ids.protection))
		if registry.DamageTypeInTag("minecraft:is_fall", damageTypeID) {
			epf += 3 * float32(enchantLevel(stack, ids.featherFalling))
		}
		if registry.DamageTypeInTag("minecraft:is_fire", damageTypeID) {
			epf += 2 * float32(enchantLevel(stack, ids.fireProtection))
		}
		if registry.DamageTypeInTag("minecraft:is_explosion", damageTypeID) {
			epf += 2 * float32(enchantLevel(stack, ids.blastProtection))
		}
		if registry.DamageTypeInTag("minecraft:is_projectile", damageTypeID) {
			epf += 2 * float32(enchantLevel(stack, ids.projectileProtection))
		}
	}
	return min(epf, 20)
}

// damageAfterEnchantments 复刻原版 CombatRules.getDamageAfterMagicAbsorb：
// 伤害 ×(1 - min(EPF,20)/25)。
func damageAfterEnchantments(amount, epf float32) float32 {
	if epf <= 0 {
		return amount
	}
	return amount * (1 - min(epf, 20)/25)
}

// computePlayerDamage 计算玩家受到的最终伤害与护甲耐久损耗量。
// 纯计算（除读取物品栏外无副作用）；护甲耐久由调用方在读/写锁外施加。
//
// 护甲耐久量按原版使用“减伤前的伤害”：max(1, 伤害/4)。
func (s *Server) computePlayerDamage(player *session, amount float32, damageTypeID int32) (final, armorDamage float32) {
	if amount <= 0 {
		return 0, 0
	}
	final = amount
	if !registry.DamageTypeInTag("minecraft:bypasses_armor", damageTypeID) {
		armor, toughness := player.armorValues()
		final = damageAfterArmor(final, armor, toughness)
		armorDamage = float32(math.Max(1, float64(amount)/4))
	}
	if !registry.DamageTypeInTag("minecraft:bypasses_enchantments", damageTypeID) {
		final = damageAfterEnchantments(final, player.protectionEPF(damageTypeID))
	}
	if final < 0 {
		final = 0
	}
	return final, armorDamage
}

// damagePlayerArmor 按原版 Player.hurtArmor 损耗每件护甲：
// amount 是 computePlayerDamage 已按 max(1, 伤害/4) 计算好的每件损耗
// （不灭附魔可能免除）。
func (s *Server) damagePlayerArmor(player *session, amount float32) {
	if amount <= 0 {
		return
	}
	damage := int32(math.Max(1, float64(amount)))
	for _, slot := range [...]int{item.SlotFeet, item.SlotLegs, item.SlotChest, item.SlotHead} {
		stack := player.inventory.Get(slot)
		if stack.IsEmpty() {
			continue
		}
		if _, ok := registry.MaxDamage(stack.ItemID); !ok {
			continue
		}
		if !unbreakingSavesDurability(player.server, stack, true) {
			player.damageInventoryItem(slot, damage)
		}
	}
}

// unbreakingSaveChance 返回不灭附魔免除本次耐久损耗的概率。
// 护甲：(2L)/(10+5(L-1))；非护甲：L/(L+1)
// （数值展开自 1.21.11 的 unbreaking 附魔 JSON）。
func unbreakingSaveChance(level int32, armor bool) float64 {
	if level <= 0 {
		return 0
	}
	if armor {
		numerator := 2.0 + 2.0*float64(level-1)
		denominator := 10.0 + 5.0*float64(level-1)
		return numerator / denominator
	}
	return float64(level) / float64(level+1)
}

// unbreakingSavesDurability 按原版不灭附魔的概率判定本次耐久损耗是否被免除。
func unbreakingSavesDurability(server *Server, stack item.Stack, armor bool) bool {
	chance := unbreakingSaveChance(enchantLevel(stack, enchants().unbreaking), armor)
	if chance <= 0 {
		return false
	}
	return float64(server.nextRandom()%10000) < chance*10000
}

// damageInventoryItem 对物品栏槽位施加耐久损耗；耐久耗尽时移除物品并同步。
// 仅修改物品栏并发送 Set Player Inventory（可在 Tick 或读循环调用）。
func (s *session) damageInventoryItem(slot int, amount int32) {
	if amount <= 0 {
		return
	}
	stack := s.inventory.Get(slot)
	if stack.IsEmpty() {
		return
	}
	maxDamage, ok := registry.MaxDamage(stack.ItemID)
	if !ok {
		return
	}
	stack.Damage += amount
	s.inventory.Set(slot, stack)
	if stack.Damage >= maxDamage {
		s.inventory.Set(slot, item.Empty())
		s.tryWrite(protocol.EncodeSetPlayerInventory(int32(slot), item.Empty().AppendSlot(nil)))
		// 原版物品损坏：Range/Entity Event 0（播放损坏音效）。
		x, _, z, _, _ := s.playerPosition()
		s.server.writeToNearbyPlayers(protocol.EncodeEntityEvent(s.entityID, 0), s.dimensionID(), x, z, nil)
		return
	}
	s.tryWrite(protocol.EncodeSetPlayerInventory(int32(slot), stack.AppendSlot(nil)))
}

// airCapacityValue 返回缓存的氧气上限（原版
// LivingEntity.getMaxAirSupply = 300 + oxygen_bonus×20）。
// 上限由 read loop 的 refreshEquipmentSnapshot 更新；未初始化的轻量
// 会话回退到基础值。
func (s *session) airCapacityValue() int {
	capacity := s.airCapacity.Load()
	if capacity <= 0 {
		return maxPlayerAir
	}
	return int(capacity)
}

// miningEnchantSpeed 返回效率附魔对挖掘速度的加成（原版
// Item.getDestroySpeed：工具速度 > 1 时 +等级²+1）。
func miningEnchantSpeed(held item.Stack, base float32) float32 {
	if base <= 1 {
		return base
	}
	level := enchantLevel(held, enchants().efficiency)
	if level <= 0 {
		return base
	}
	return base + float32(level*level+1)
}

// hasSilkTouch/hasFortune 返回附魔等级（0 表示无）。
func hasSilkTouch(held item.Stack) int32 {
	return enchantLevel(held, enchants().silkTouch)
}

func fortuneLevel(held item.Stack) int32 {
	return enchantLevel(held, enchants().fortune)
}

// lootingLevel 返回抢夺等级。
func lootingLevel(held item.Stack) int32 {
	return enchantLevel(held, enchants().looting)
}

// mendingItems 返回带经验修补的可损坏物品槽位。
// 按固定槽位顺序遍历（原版随机选择；gmcs 使用确定顺序以便测试）。
// 不读取 selectedSlot 等读循环独占字段：本函数可能在 Tick 线程调用。
func (s *session) mendingItems() []int {
	var slots []int
	mending := enchants().mending
	all := s.inventory.Slots() // 一次加锁取快照
	for slot, stack := range all {
		if stack.IsEmpty() || enchantLevel(stack, mending) <= 0 {
			continue
		}
		maxDamage, ok := registry.MaxDamage(stack.ItemID)
		if !ok || stack.Damage <= 0 || stack.Damage >= maxDamage {
			continue
		}
		slots = append(slots, slot)
	}
	return slots
}

// repairWithExperience 消耗经验修复带经验修补的物品，返回消耗的经验点数。
// 原版 repair_with_xp 的 multiply 因子为 2（每点经验修复 2 点耐久）。
func (s *session) repairWithExperience(points int32) int32 {
	if points <= 0 {
		return 0
	}
	remaining := points
	for _, slot := range s.mendingItems() {
		if remaining <= 0 {
			break
		}
		stack := s.inventory.Get(slot)
		maxDamage, ok := registry.MaxDamage(stack.ItemID)
		if !ok || stack.Damage <= 0 || stack.Damage >= maxDamage {
			continue
		}
		repair := min(int32(2)*remaining, stack.Damage)
		used := (repair + 1) / 2
		stack.Damage -= repair
		s.inventory.Set(slot, stack)
		s.tryWrite(protocol.EncodeSetPlayerInventory(int32(slot), stack.AppendSlot(nil)))
		remaining -= used
	}
	return points - remaining
}

// hasBindingCurse 报告物品是否带绑定诅咒。
func hasBindingCurse(stack item.Stack) bool {
	return enchantLevel(stack, enchants().bindingCurse) > 0
}

// hasVanishingCurse 报告物品是否带消失诅咒。
func hasVanishingCurse(stack item.Stack) bool {
	return enchantLevel(stack, enchants().vanishingCurse) > 0
}
