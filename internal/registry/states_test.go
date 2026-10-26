//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package registry

import "testing"

// TestIsCactusState 验证仙人掌状态位表：所有 age 都识别为仙人掌，
// 且不会误判其它方块（例如同为方块名的沙/草方块）。
func TestIsCactusState(t *testing.T) {
	base, err := BlockStateID("minecraft:cactus")
	if err != nil {
		t.Fatalf("缺少仙人掌默认状态: %v", err)
	}
	// 仙人掌有 16 个 age 状态（状态 ID 连续）。
	for state := base; state < base+16; state++ {
		if !IsCactusState(state) {
			t.Fatalf("state %d 应被识别为仙人掌", state)
		}
	}
	if IsCactusState(base + 16) {
		t.Fatalf("state %d 不在仙人掌区间内", base+16)
	}
	for _, name := range []string{
		"minecraft:air", "minecraft:stone", "minecraft:grass_block",
		"minecraft:sand", "minecraft:oak_leaves", "minecraft:water",
	} {
		state, err := BlockStateID(name)
		if err != nil {
			t.Fatalf("缺少方块状态 %s: %v", name, err)
		}
		if IsCactusState(state) {
			t.Fatalf("%s 不应被识别为仙人掌", name)
		}
	}
	if IsCactusState(0) {
		t.Fatal("空气不应被识别为仙人掌")
	}
}

// TestStateKindBitsAreDisjoint 验证水/岩浆/火/仙人掌类别位互不重叠
// （环境扫描依赖单一类别判断）。
func TestStateKindBitsAreDisjoint(t *testing.T) {
	water, _ := BlockStateID("minecraft:water")
	lava, _ := BlockStateID("minecraft:lava")
	fire, _ := BlockStateID("minecraft:fire")
	cactus, _ := BlockStateID("minecraft:cactus")
	if IsWaterState(lava) || IsLavaState(water) || IsWaterState(fire) ||
		IsFireState(cactus) || IsCactusState(fire) || IsCactusState(water) {
		t.Fatal("方块状态类别位存在重叠")
	}
}
