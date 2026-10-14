//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package registry

import "testing"

// TestBrewingTableShape 验证酿造配方表与 1.21.11 反编译结果一致：
// 63 条药水配方（含 addStartMix 展开的水瓶 → 平凡条目）与 2 条容器配方。
// 数字来自对 PotionBrewing.addVanillaMixes 字节码的逐条清点，
// 变动配方时必须同步更新（详见 brewing.go 顶部说明）。
func TestBrewingTableShape(t *testing.T) {
	tables := brewingTableData()
	if len(tables.potionMixes) != 63 {
		t.Fatalf("药水配方数 = %d, want 63", len(tables.potionMixes))
	}
	if len(tables.containerMixes) != 2 {
		t.Fatalf("容器配方数 = %d, want 2", len(tables.containerMixes))
	}
	if len(tables.ingredients) != 21 {
		t.Fatalf("材料数 = %d, want 21", len(tables.ingredients))
	}
	if len(tables.containerItems) != 3 {
		t.Fatalf("容器物品数 = %d, want 3", len(tables.containerItems))
	}
}

// TestBrewingBaseRecipes 验证基础与起始配方（水瓶链路）。
func TestBrewingBaseRecipes(t *testing.T) {
	netherWart := itemIDOf(t, "minecraft:nether_wart")
	sugar := itemIDOf(t, "minecraft:sugar")
	redstone := itemIDOf(t, "minecraft:redstone")
	glowstone := itemIDOf(t, "minecraft:glowstone_dust")

	cases := []struct {
		from, item, to int32
	}{
		{PotionWater, netherWart, PotionAwkward}, // 水瓶 + 下界疣 → 粗制
		{PotionWater, redstone, PotionMundane},   // 水瓶 + 红石 → 平凡
		{PotionWater, glowstone, PotionThick},    // 水瓶 + 荧石粉 → 浓稠
		{PotionWater, sugar, PotionMundane},      // addStartMix：水瓶 → 平凡
		{PotionAwkward, sugar, PotionSwiftness},  // addStartMix：粗制 → 迅捷
	}
	for _, tc := range cases {
		to, ok := BrewingPotionMix(tc.from, tc.item)
		if !ok || to != tc.to {
			t.Fatalf("配方 (药水 %d + 物品 %d) = (%d, %v), want %d", tc.from, tc.item, to, ok, tc.to)
		}
	}
}

// TestBrewingUpgradeRecipes 验证升级链（红石延长 / 荧石强化 / 发酵蛛眼腐化）。
func TestBrewingUpgradeRecipes(t *testing.T) {
	redstone := itemIDOf(t, "minecraft:redstone")
	glowstone := itemIDOf(t, "minecraft:glowstone_dust")
	spiderEye := itemIDOf(t, "minecraft:fermented_spider_eye")

	to, ok := BrewingPotionMix(PotionNightVision, redstone)
	if !ok || to != PotionLongNightVision {
		t.Fatalf("夜视 + 红石 = (%d, %v), want 延长夜视", to, ok)
	}
	to, ok = BrewingPotionMix(PotionStrength, glowstone)
	if !ok || to != PotionStrongStrength {
		t.Fatalf("力量 + 荧石 = (%d, %v), want 强化力量", to, ok)
	}
	to, ok = BrewingPotionMix(PotionNightVision, spiderEye)
	if !ok || to != PotionInvisibility {
		t.Fatalf("夜视 + 发酵蛛眼 = (%d, %v), want 隐身", to, ok)
	}
	// 强化治疗 + 发酵蛛眼 → 强化伤害（目标不是延长型）。
	to, ok = BrewingPotionMix(PotionStrongHealing, spiderEye)
	if !ok || to != PotionStrongHarming {
		t.Fatalf("强化治疗 + 发酵蛛眼 = (%d, %v), want 强化伤害", to, ok)
	}
}

// TestBrewingContainerRecipes 验证容器配方（喷溅 / 滞留）。
func TestBrewingContainerRecipes(t *testing.T) {
	potion := itemIDOf(t, "minecraft:potion")
	splash := itemIDOf(t, "minecraft:splash_potion")
	lingering := itemIDOf(t, "minecraft:lingering_potion")
	gunpowder := itemIDOf(t, "minecraft:gunpowder")
	dragonBreath := itemIDOf(t, "minecraft:dragon_breath")

	to, ok := BrewingContainerMix(potion, gunpowder)
	if !ok || to != splash {
		t.Fatalf("药水 + 火药 = (%d, %v), want 喷溅药水", to, ok)
	}
	to, ok = BrewingContainerMix(splash, dragonBreath)
	if !ok || to != lingering {
		t.Fatalf("喷溅 + 龙息 = (%d, %v), want 滞留药水", to, ok)
	}
	// 龙息不能直接用于普通药水。
	if _, ok := BrewingContainerMix(potion, dragonBreath); ok {
		t.Fatal("药水 + 龙息不应有容器配方")
	}
}

// TestBrewingIngredientAndContainer 验证材料与容器判定。
func TestBrewingIngredientAndContainer(t *testing.T) {
	ingredients := []string{
		"minecraft:nether_wart", "minecraft:redstone", "minecraft:glowstone_dust",
		"minecraft:fermented_spider_eye", "minecraft:gunpowder", "minecraft:dragon_breath",
		"minecraft:ghast_tear", "minecraft:blaze_powder", "minecraft:stone",
	}
	for _, name := range ingredients {
		if !BrewingIsIngredient(itemIDOf(t, name)) {
			t.Fatalf("%s 应为酿造材料", name)
		}
	}
	notIngredients := []string{"minecraft:diamond", "minecraft:glass_bottle", "minecraft:potion"}
	for _, name := range notIngredients {
		if BrewingIsIngredient(itemIDOf(t, name)) {
			t.Fatalf("%s 不应为酿造材料", name)
		}
	}
	for _, name := range []string{"minecraft:potion", "minecraft:splash_potion", "minecraft:lingering_potion"} {
		if !BrewingIsContainerItem(itemIDOf(t, name)) {
			t.Fatalf("%s 应为药水容器", name)
		}
	}
	if BrewingIsContainerItem(itemIDOf(t, "minecraft:glass_bottle")) {
		t.Fatal("玻璃瓶不应为药水容器")
	}
}

// TestBrewingHasMix 验证 hasMix 的匹配范围（含水瓶直酿与需先粗制的配方）。
func TestBrewingHasMix(t *testing.T) {
	potion := itemIDOf(t, "minecraft:potion")
	netherWart := itemIDOf(t, "minecraft:nether_wart")
	goldenCarrot := itemIDOf(t, "minecraft:golden_carrot")

	if !BrewingHasMix(potion, PotionWater, netherWart) {
		t.Fatal("水瓶 + 下界疣 应可酿造")
	}
	if BrewingHasMix(potion, PotionWater, goldenCarrot) {
		t.Fatal("水瓶 + 金胡萝卜 不应可酿造（需先粗制）")
	}
	if !BrewingHasMix(potion, PotionAwkward, goldenCarrot) {
		t.Fatal("粗制 + 金胡萝卜 应可酿造")
	}
	if BrewingHasMix(potion, PotionAwkward, itemIDOf(t, "minecraft:diamond")) {
		t.Fatal("钻石不应可酿造")
	}
}

// itemIDOf 是测试辅助：按名称查询物品 ID。
func itemIDOf(t *testing.T, name string) int32 {
	t.Helper()
	id, err := ItemID(name)
	if err != nil {
		t.Fatalf("缺少物品 %s: %v", name, err)
	}
	return id
}
