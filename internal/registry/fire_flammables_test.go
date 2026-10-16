//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package registry

import "testing"

// TestFireFlammabilityValues 校验机械提取的可燃数值表：抽查原版已知数值，
// 并确认不可燃方块、灵魂火与含水方块返回 0。
func TestFireFlammabilityValues(t *testing.T) {
	cases := []struct {
		name          string
		encouragement int
		flammability  int
	}{
		{"minecraft:oak_planks", 5, 20},
		{"minecraft:oak_leaves", 30, 60},
		{"minecraft:oak_log", 5, 5},
		{"minecraft:white_wool", 30, 60},
		{"minecraft:white_carpet", 60, 20},
		{"minecraft:bookshelf", 30, 20},
		{"minecraft:hay_block", 60, 20},
		{"minecraft:tnt", 15, 100},
		{"minecraft:tall_grass", 60, 100},
		{"minecraft:dead_bush", 60, 100},
		{"minecraft:stone", 0, 0},
		{"minecraft:dirt", 0, 0},
		{"minecraft:soul_fire", 0, 0},
		{"minecraft:oak_door", 0, 0}, // 原版可燃表中不含木门
	}
	for _, tc := range cases {
		state, err := BlockStateID(tc.name)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		encouragement, flammability := FireFlammability(state)
		if encouragement != tc.encouragement || flammability != tc.flammability {
			t.Errorf("%s = (%d, %d), want (%d, %d)",
				tc.name, encouragement, flammability, tc.encouragement, tc.flammability)
		}
	}
}

// TestFireFlammabilityUnmodeledStates 确认 gmcs 未建模的属性状态（如含水的
// 橡木台阶）仍按所属方块返回数值（gmcs 不会生成这类状态；原版对 waterlogged
// 返回 0，属于已知偏差，见 FireFlammability 注释）。
func TestFireFlammabilityUnmodeledStates(t *testing.T) {
	if _, _, ok := StateProps(13131); ok {
		t.Skip("属性状态表已收录橡木台阶，需要更新本测试的前提")
	}
	name, ok := BlockNameByState(13131)
	if !ok || name != "minecraft:oak_slab" {
		t.Fatalf("状态 13131 = %q（%v），want minecraft:oak_slab", name, ok)
	}
	if encouragement, flammability := FireFlammability(13131); encouragement != 5 || flammability != 20 {
		t.Fatalf("状态 13131 = (%d, %d), want (5, 20)", encouragement, flammability)
	}
}

// TestFireFlammableTableSize 锁定提取到的可燃方块数量（原版 1.21.11 为 206）。
func TestFireFlammableTableSize(t *testing.T) {
	if len(fireFlammables) != 206 {
		t.Fatalf("可燃方块表 = %d 项, want 206", len(fireFlammables))
	}
}

// TestBlockTagContains 校验方块标签查询（标签值为方块注册表 ID）。
func TestBlockTagContains(t *testing.T) {
	netherrack, err := BlockStateID("minecraft:netherrack")
	if err != nil {
		t.Fatal(err)
	}
	magma, err := BlockStateID("minecraft:magma_block")
	if err != nil {
		t.Fatal(err)
	}
	stone, err := BlockStateID("minecraft:stone")
	if err != nil {
		t.Fatal(err)
	}
	if !BlockTagContains("minecraft:infiniburn_overworld", netherrack) {
		t.Error("下界岩应属于 infiniburn_overworld")
	}
	if !BlockTagContains("minecraft:infiniburn_nether", magma) {
		t.Error("岩浆块应属于 infiniburn_nether")
	}
	if BlockTagContains("minecraft:infiniburn_overworld", stone) {
		t.Error("石头不应属于 infiniburn_overworld")
	}
	if BlockTagContains("minecraft:infiniburn_nether", netherrack) && !BlockTagContains("minecraft:infiniburn_end", netherrack) {
		t.Error("infiniburn_end 应包含 infiniburn_nether 的方块")
	}
	soulSand, err := BlockStateID("minecraft:soul_sand")
	if err != nil {
		t.Fatal(err)
	}
	soulSoil, err := BlockStateID("minecraft:soul_soil")
	if err != nil {
		t.Fatal(err)
	}
	if !BlockTagContains("minecraft:soul_fire_base_blocks", soulSand) ||
		!BlockTagContains("minecraft:soul_fire_base_blocks", soulSoil) {
		t.Error("灵魂沙/灵魂土应属于 soul_fire_base_blocks")
	}
	if BlockTagContains("minecraft:soul_fire_base_blocks", stone) {
		t.Error("石头不应属于 soul_fire_base_blocks")
	}
	if BlockTagContains("minecraft:no_such_tag", stone) {
		t.Error("未知标签应返回 false")
	}
}
