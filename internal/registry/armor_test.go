//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package registry

import "testing"

// TestArmorStatsData 验证护甲值与韧性来自官方 attribute_modifiers。
func TestArmorStatsData(t *testing.T) {
	cases := []struct {
		name      string
		armor     float32
		toughness float32
	}{
		{"minecraft:leather_helmet", 1, 0},
		{"minecraft:iron_chestplate", 6, 0},
		{"minecraft:diamond_helmet", 3, 2},
		{"minecraft:diamond_chestplate", 8, 2},
		{"minecraft:netherite_leggings", 6, 3},
		{"minecraft:turtle_helmet", 2, 0},
	}
	for _, tc := range cases {
		itemID, err := ItemID(tc.name)
		if err != nil {
			t.Fatalf("缺少物品 %s：%v", tc.name, err)
		}
		stats, ok := ArmorStats(itemID)
		if !ok {
			t.Fatalf("%s 缺少护甲属性", tc.name)
		}
		if stats.Armor != tc.armor || stats.Toughness != tc.toughness {
			t.Errorf("%s = %+v, want {%v %v}", tc.name, stats, tc.armor, tc.toughness)
		}
	}
	// 非护甲物品不提供护甲值。
	stone, err := ItemID("minecraft:stone")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ArmorStats(stone); ok {
		t.Error("石头不应提供护甲值")
	}
}
