//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// TestArmorDamageFormula 验证护甲减伤公式（CombatRules.getDamageAfterAbsorb）。
func TestArmorDamageFormula(t *testing.T) {
	cases := []struct {
		amount, armor, toughness, want float32
	}{
		{10, 0, 0, 10},
		{10, 20, 0, 4},          // clamp(20-10/2=15) → ×0.4
		{4, 8, 2, 2.976},        // clamp(8-4/2.5=6.4) → ×0.744
		{100, 20, 0, 84},        // clamp(20-50→4) → ×0.84
		{1, 20, 20, 0.20571429}, // clamp(20-1/7≈19.86) → ×0.2057
		{-5, 20, 0, -5},         // 非正伤害原样返回
	}
	for _, tc := range cases {
		got := damageAfterArmor(tc.amount, tc.armor, tc.toughness)
		if math.Abs(float64(got-tc.want)) > 1e-3 {
			t.Errorf("damageAfterArmor(%v,%v,%v) = %v, want %v",
				tc.amount, tc.armor, tc.toughness, got, tc.want)
		}
	}
}

// TestProtectionEPFAndAbsorb 验证保护类附魔的 EPF 与最终减伤。
func TestProtectionEPFAndAbsorb(t *testing.T) {
	if got := damageAfterEnchantments(10, 0); got != 10 {
		t.Errorf("EPF 0 = %v, want 10", got)
	}
	if got := damageAfterEnchantments(10, 20); math.Abs(float64(got-2)) > 1e-3 {
		t.Errorf("EPF 20 = %v, want 2", got)
	}
	// EPF 上限 20：EPF 40 与 20 相同。
	if got := damageAfterEnchantments(10, 40); math.Abs(float64(got-2)) > 1e-3 {
		t.Errorf("EPF 40 = %v, want 2（上限 20）", got)
	}
}

// TestArmorValuesAndProtection 验证从装备槽汇总护甲值与保护 EPF。
func TestArmorValuesAndProtection(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer instance.testWorld().Close()

	player := &session{server: instance}
	protectionID, ok := registry.EnchantmentID("minecraft:protection")
	if !ok {
		t.Fatal("缺少保护附魔")
	}
	featherID, ok := registry.EnchantmentID("minecraft:feather_falling")
	if !ok {
		t.Fatal("缺少摔落保护附魔")
	}
	player.inventory.Set(item.SlotHead, item.Stack{ItemID: mustItemID(t, "minecraft:diamond_helmet"), Count: 1,
		Enchantments: []item.EnchantmentEntry{{ID: protectionID, Level: 4}}})
	player.inventory.Set(item.SlotChest, item.Stack{ItemID: mustItemID(t, "minecraft:diamond_chestplate"), Count: 1,
		Enchantments: []item.EnchantmentEntry{{ID: protectionID, Level: 4}}})
	player.inventory.Set(item.SlotLegs, item.Stack{ItemID: mustItemID(t, "minecraft:diamond_leggings"), Count: 1})
	player.inventory.Set(item.SlotFeet, item.Stack{ItemID: mustItemID(t, "minecraft:diamond_boots"), Count: 1,
		Enchantments: []item.EnchantmentEntry{{ID: featherID, Level: 4}}})

	armor, toughness := player.armorValues()
	if armor != 20 || toughness != 8 {
		t.Fatalf("护甲值 = %v/%v, want 20/8", armor, toughness)
	}
	// 普通伤害：保护 8 EPF；摔落：保护 8 + 摔落保护 12 = 20。
	normalType := instance.mobAttackDamageTypeID
	if epf := player.protectionEPF(normalType); epf != 8 {
		t.Errorf("普通伤害 EPF = %v, want 8", epf)
	}
	if epf := player.protectionEPF(instance.fallDamageTypeID); epf != 20 {
		t.Errorf("摔落伤害 EPF = %v, want 20", epf)
	}
	// 摔落伤害穿透护甲（bypasses_armor），但仍受保护附魔减免。
	final, armorDamage := instance.computePlayerDamage(player, 10, instance.fallDamageTypeID)
	if math.Abs(float64(final-2)) > 1e-3 {
		t.Errorf("摔落最终伤害 = %v, want 2", final)
	}
	if armorDamage != 0 {
		t.Errorf("摔落不应损耗护甲耐久：%v", armorDamage)
	}
}

// TestPlayerDamageArmorDurability 验证近战伤害的护甲减伤与每件护甲耐久损耗。
func TestPlayerDamageArmorDurability(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Armored")
	player := findSession(t, instance, "Armored")
	for _, slot := range []int{item.SlotFeet, item.SlotLegs, item.SlotChest, item.SlotHead} {
		player.inventory.Set(slot, item.Stack{ItemID: mustItemID(t, armors[slot]), Count: 1})
	}
	// 钻石全套：20 护甲、8 韧性；10 点伤害 → 10×(1-17.5/25) = 3。
	if !instance.damagePlayer(player, 10, "Zombie", 0, instance.mobAttackDamageTypeID, nil) {
		t.Fatal("伤害未生效")
	}
	health, _, _ := player.healthStatus()
	if math.Abs(float64(health-17)) > 1e-3 {
		t.Fatalf("受伤后生命 = %v, want 17", health)
	}
	// 每件护甲损耗 max(1, 10/4) = 2。
	for _, slot := range []int{item.SlotFeet, item.SlotLegs, item.SlotChest, item.SlotHead} {
		if got := player.inventory.Get(slot).Damage; got != 2 {
			t.Errorf("槽位 %d 护甲损耗 = %d, want 2", slot, got)
		}
	}
}

var armors = map[int]string{
	item.SlotFeet:  "minecraft:diamond_boots",
	item.SlotLegs:  "minecraft:diamond_leggings",
	item.SlotChest: "minecraft:diamond_chestplate",
	item.SlotHead:  "minecraft:diamond_helmet",
}

// TestUnbreakingSaveChance 验证不灭附魔的免除概率（原版附魔数据）。
func TestUnbreakingSaveChance(t *testing.T) {
	cases := []struct {
		level int32
		armor bool
		want  float64
	}{
		{0, false, 0},
		{1, false, 0.5},
		{2, false, 2.0 / 3.0},
		{3, false, 0.75},
		{1, true, 0.2},
		{2, true, 4.0 / 15.0},
		{3, true, 0.3},
	}
	for _, tc := range cases {
		if got := unbreakingSaveChance(tc.level, tc.armor); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("unbreakingSaveChance(%d,%v) = %v, want %v", tc.level, tc.armor, got, tc.want)
		}
	}
}

// TestMiningEnchantSpeed 验证效率附魔只提升工具速度 > 1 的挖掘。
func TestMiningEnchantSpeed(t *testing.T) {
	efficiencyID, ok := registry.EnchantmentID("minecraft:efficiency")
	if !ok {
		t.Fatal("缺少效率附魔")
	}
	held := item.Stack{ItemID: mustItemID(t, "minecraft:diamond_pickaxe"), Count: 1,
		Enchantments: []item.EnchantmentEntry{{ID: efficiencyID, Level: 3}}}
	if got := miningEnchantSpeed(held, 8); got != 18 {
		t.Errorf("效率 3 钻石镐 = %v, want 18", got)
	}
	if got := miningEnchantSpeed(held, 1); got != 1 {
		t.Errorf("空手速度不应被效率提升：%v", got)
	}
}

// TestMeleeEnchantDamage 验证锋利/亡灵杀手/节肢杀手的额外伤害。
func TestMeleeEnchantDamage(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer instance.testWorld().Close()

	sharpnessID, _ := registry.EnchantmentID("minecraft:sharpness")
	smiteID, _ := registry.EnchantmentID("minecraft:smite")
	baneID, _ := registry.EnchantmentID("minecraft:bane_of_arthropods")
	held := item.Stack{ItemID: mustItemID(t, "minecraft:diamond_sword"), Count: 1}
	held = held.WithEnchantment(sharpnessID, 5).WithEnchantment(smiteID, 5).WithEnchantment(baneID, 5)
	zombie := &mob{Kind: mobZombie, TypeID: instance.mobTypeIDs[mobZombie]}
	spider := &mob{Kind: mobSpider, TypeID: instance.mobTypeIDs[mobSpider]}
	cow := &mob{Kind: mobCow, TypeID: instance.mobTypeIDs[mobCow]}

	// 锋利 5 = 1 + 0.5×4 = 3；亡灵杀手 5 = 12.5。
	if got := instance.meleeEnchantDamage(held, zombie); got != 15.5 {
		t.Errorf("僵尸受附魔伤害 = %v, want 15.5", got)
	}
	// 蜘蛛：锋利 3 + 节肢杀手 12.5。
	if got := instance.meleeEnchantDamage(held, spider); got != 15.5 {
		t.Errorf("蜘蛛受附魔伤害 = %v, want 15.5", got)
	}
	// 牛只有锋利。
	if got := instance.meleeEnchantDamage(held, cow); got != 3 {
		t.Errorf("牛受附魔伤害 = %v, want 3", got)
	}
}

// TestMendingRepairsItems 验证经验修补消耗经验修复物品耐久。
func TestMendingRepairsItems(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Mender")
	player := findSession(t, instance, "Mender")
	mendingID, ok := registry.EnchantmentID("minecraft:mending")
	if !ok {
		t.Fatal("缺少经验修补附魔")
	}
	pickaxe := item.Stack{ItemID: mustItemID(t, "minecraft:diamond_pickaxe"), Count: 1, Damage: 30,
		Enchantments: []item.EnchantmentEntry{{ID: mendingID, Level: 1}}}
	player.inventory.Set(0, pickaxe)

	// 10 点经验修复 20 点耐久，消耗 10 点经验，全部用完。
	if used := player.repairWithExperience(10); used != 10 {
		t.Errorf("消耗经验 = %d, want 10", used)
	}
	if got := player.inventory.Get(0).Damage; got != 10 {
		t.Errorf("修复后耐久损耗 = %d, want 10", got)
	}
	// 剩余 10 点耐久只需 5 点经验。
	if used := player.repairWithExperience(10); used != 5 {
		t.Errorf("第二次消耗经验 = %d, want 5", used)
	}
	if got := player.inventory.Get(0).Damage; got != 0 {
		t.Errorf("完全修复后耐久损耗 = %d, want 0", got)
	}
}

// TestBindingCursePreventsArmorPickup 验证绑定诅咒在生存模式阻止取出盔甲。
func TestBindingCursePreventsArmorPickup(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Bound")
	player := findSession(t, instance, "Bound")
	bindingID, ok := registry.EnchantmentID("minecraft:binding_curse")
	if !ok {
		t.Fatal("缺少绑定诅咒附魔")
	}
	helmet := item.Stack{ItemID: mustItemID(t, "minecraft:diamond_helmet"), Count: 1,
		Enchantments: []item.EnchantmentEntry{{ID: bindingID, Level: 1}}}
	player.inventory.Set(item.SlotHead, helmet)

	backing := craftSlotBacking{
		player:     player,
		state:      newCraftingState(2),
		windowZero: true,
	}
	// 窗口槽位 5 = 头盔。
	if backing.CanPickup(5, player) {
		t.Error("生存模式不应能从绑定诅咒盔甲槽取出物品")
	}
	player.setGameMode(uint8(config.GameModeCreative))
	if !backing.CanPickup(5, player) {
		t.Error("创造模式应允许取出绑定诅咒盔甲")
	}
}

// TestVanishingCurseNotDropped 验证死亡时消失诅咒物品被销毁。
func TestVanishingCurseNotDropped(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Vanisher")
	player := findSession(t, instance, "Vanisher")
	vanishingID, ok := registry.EnchantmentID("minecraft:vanishing_curse")
	if !ok {
		t.Fatal("缺少消失诅咒附魔")
	}
	player.inventory.Set(0, item.Stack{ItemID: mustItemID(t, "minecraft:diamond_sword"), Count: 1,
		Enchantments: []item.EnchantmentEntry{{ID: vanishingID, Level: 1}}})
	player.inventory.Set(1, item.Stack{ItemID: mustItemID(t, "minecraft:stone"), Count: 3})

	instance.dropPlayerInventory(player)
	instance.entityMu.Lock()
	defer instance.entityMu.Unlock()
	if len(instance.items) != 1 {
		t.Fatalf("掉落物数量 = %d, want 1（消失诅咒物品销毁）", len(instance.items))
	}
	for _, entity := range instance.items {
		if entity.Stack.ItemID != mustItemID(t, "minecraft:stone") {
			t.Errorf("掉落物 = %d, want 石头", entity.Stack.ItemID)
		}
	}
}

// TestMeleeFireAspectAndKnockback 验证火焰附加与击退附魔的实际效果。
func TestMeleeFireAspectAndKnockback(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Pyro")
	// 固定为夜晚：白天阳光下亡灵会持续被点燃（原版行为），
	// 避免日光燃烧干扰本用例的断言。
	instance.worldAge.Store(worldNightStart)
	player := findSession(t, instance, "Pyro")
	fireID, ok := registry.EnchantmentID("minecraft:fire_aspect")
	if !ok {
		t.Fatal("缺少火焰附加附魔")
	}
	knockbackID, ok := registry.EnchantmentID("minecraft:knockback")
	if !ok {
		t.Fatal("缺少击退附魔")
	}
	weapon := item.Stack{ItemID: mustItemID(t, "minecraft:diamond_sword"), Count: 1}
	weapon = weapon.WithEnchantment(fireID, 2).WithEnchantment(knockbackID, 3)
	player.inventory.Set(player.selectedHotbarSlot(), weapon)
	spawnX, spawnY, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	// 清出一块平地：出生点附近可能有树木/坡地，击退要求目标列有可站立地面。
	baseX, baseZ := int(math.Floor(spawnX)), int(math.Floor(spawnZ))
	baseY := int(math.Floor(spawnY)) - 1
	for dx := -3; dx <= 3; dx++ {
		for dz := -1; dz <= 4; dz++ {
			instance.testWorld().SetBlock(baseX+dx, baseY, baseZ+dz, world.StoneBlock)
			for dy := 1; dy <= 8; dy++ {
				instance.testWorld().SetBlock(baseX+dx, baseY+dy, baseZ+dz, world.AirBlock)
			}
		}
	}
	zombie := instance.addMob(world.DimensionOverworld, mobZombie, spawnX, spawnY, spawnZ+1)
	instance.handleAttack(player, zombie.ID)

	instance.entityMu.Lock()
	fireTicks := zombie.FireTicks
	zombieZ := zombie.Z
	instance.entityMu.Unlock()
	if fireTicks != 8*20 {
		t.Errorf("火焰附加 2 的着火时长 = %d, want %d", fireTicks, 8*20)
	}
	// 僵尸在玩家 +Z 一侧：击退 3（0.4 + 0.5×3 = 1.9 格）应把它推得更远。
	if zombieZ <= spawnZ+1.5 {
		t.Errorf("击退 3 后 Z = %v，位置没有明显变化（起点 %v）", zombieZ, spawnZ+1)
	}
}
