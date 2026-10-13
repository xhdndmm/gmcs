package registry

import "testing"

// TestCompostChances 验证堆肥表规模与代表性数值
// （数据来自 1.21.11 ComposterBlock.bootstrap 字节码提取）。
func TestCompostChances(t *testing.T) {
	if len(compostChances) != 115 {
		t.Fatalf("堆肥表条目 = %d, want 115", len(compostChances))
	}
	for name, chance := range compostChances {
		if chance <= 0 || chance > 1 {
			t.Fatalf("%s 的概率 %v 超出 (0,1]", name, chance)
		}
	}
	// 代表性数值（与 wiki 公认值一致）。
	cases := map[string]float32{
		"minecraft:cake":          1.00,
		"minecraft:pumpkin_pie":   1.00,
		"minecraft:hay_block":     0.85,
		"minecraft:apple":         0.65,
		"minecraft:tall_grass":    0.50,
		"minecraft:wheat_seeds":   0.30,
		"minecraft:oak_leaves":    0.30,
		"minecraft:jungle_leaves": 0.30,
	}
	for name, want := range cases {
		id, err := ItemID(name)
		if err != nil {
			t.Fatalf("缺少物品 %s：%v", name, err)
		}
		if got := CompostChance(id); got != want {
			t.Fatalf("%s 的堆肥概率 = %v, want %v", name, got, want)
		}
	}
	// 不可堆肥物品返回 0。
	stone, err := ItemID("minecraft:stone")
	if err != nil {
		t.Fatal(err)
	}
	if got := CompostChance(stone); got != 0 {
		t.Fatalf("石头不应可堆肥：%v", got)
	}
	if got := CompostChance(0); got != 0 {
		t.Fatalf("未知物品不应可堆肥：%v", got)
	}
}
