package registry

import "testing"

func TestFuelBurnTicks(t *testing.T) {
	cases := []struct {
		item string
		tick int32
	}{
		{"minecraft:lava_bucket", 20000},
		{"minecraft:coal_block", 16000},
		{"minecraft:dried_kelp_block", 4000},
		{"minecraft:blaze_rod", 2400},
		{"minecraft:coal", 1600},
		{"minecraft:charcoal", 1600},
		{"minecraft:oak_planks", 300},
		{"minecraft:oak_log", 300},
		{"minecraft:oak_slab", 150},
		{"minecraft:oak_stairs", 300},
		{"minecraft:oak_door", 200},
		{"minecraft:oak_sign", 200},
		{"minecraft:oak_fence", 300},
		{"minecraft:oak_fence_gate", 300},
		{"minecraft:oak_button", 100},
		{"minecraft:stick", 100},
		{"minecraft:bowl", 100},
		{"minecraft:oak_sapling", 100},
		{"minecraft:oak_boat", 1200},
		{"minecraft:white_wool", 100},
		{"minecraft:white_carpet", 67},
		{"minecraft:crafting_table", 300},
		{"minecraft:chest", 300},
		{"minecraft:bookshelf", 300},
		{"minecraft:wooden_pickaxe", 200},
		{"minecraft:wooden_axe", 200},
		{"minecraft:bamboo", 50},
		{"minecraft:scaffolding", 50},
	}
	for _, c := range cases {
		id, err := ItemID(c.item)
		if err != nil {
			t.Fatalf("未知物品 %s：%v", c.item, err)
		}
		if got := FuelBurnTicks(id); got != c.tick {
			t.Errorf("%s 燃烧时长 = %d, want %d", c.item, got, c.tick)
		}
	}

	// 非燃料。
	if id, err := ItemID("minecraft:diamond"); err == nil {
		if got := FuelBurnTicks(id); got != 0 {
			t.Errorf("钻石不应是燃料，得到 %d", got)
		}
	}
}
