//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package registry

import "testing"

// TestFarmlandMoistureStates 验证耕地含水量状态与反查。
func TestFarmlandMoistureStates(t *testing.T) {
	for moisture := 0; moisture <= 7; moisture++ {
		state, ok := FarmlandStateWithMoisture(moisture)
		if !ok {
			t.Fatalf("缺少耕地 moisture=%d 状态", moisture)
		}
		got, ok := FarmlandMoisture(state)
		if !ok || got != moisture {
			t.Fatalf("FarmlandMoisture(%d) = %d, %v; want %d, true", state, got, ok, moisture)
		}
	}
	if _, ok := FarmlandStateWithMoisture(8); ok {
		t.Fatal("moisture=8 不应有对应状态")
	}
	if _, ok := FarmlandMoisture(BlockStateIDs["minecraft:dirt"]); ok {
		t.Fatal("泥土不应被识别为耕地")
	}
}

// TestBlockStateAgeRoundTrip 验证作物年龄读写与最大年龄。
func TestBlockStateAgeRoundTrip(t *testing.T) {
	cases := []struct {
		name   string
		maxAge int
	}{
		{"minecraft:wheat", 7},
		{"minecraft:beetroots", 3},
		{"minecraft:carrots", 7},
		{"minecraft:potatoes", 7},
		{"minecraft:torchflower_crop", 1},
		{"minecraft:nether_wart", 3},
	}
	for _, tc := range cases {
		base := BlockStateIDs[tc.name]
		if maxAge, ok := BlockStateMaxAge(base); !ok || maxAge != tc.maxAge {
			t.Fatalf("%s: BlockStateMaxAge = %d, %v; want %d", tc.name, maxAge, ok, tc.maxAge)
		}
		for age := 0; age <= tc.maxAge; age++ {
			state, ok := BlockStateWithAge(base, age)
			if !ok {
				t.Fatalf("%s: 缺少 age=%d 状态", tc.name, age)
			}
			got, ok := BlockStateAge(state)
			if !ok || got != age {
				t.Fatalf("%s: BlockStateAge(%d) = %d, %v; want %d", tc.name, state, got, ok, age)
			}
		}
		if _, ok := BlockStateWithAge(base, tc.maxAge+1); ok {
			t.Fatalf("%s: 超出最大年龄应返回 false", tc.name)
		}
	}
	// 非作物方块不支持年龄推导。
	if _, ok := BlockStateAge(BlockStateIDs["minecraft:stone"]); ok {
		t.Fatal("石头不应有作物年龄")
	}
	if _, ok := BlockStateMaxAge(BlockStateIDs["minecraft:stone"]); ok {
		t.Fatal("石头不应有最大年龄")
	}
}
