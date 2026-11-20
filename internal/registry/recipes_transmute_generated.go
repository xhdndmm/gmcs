//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

// Code generated from Minecraft 1.21.11 data/minecraft/recipe crafting_transmute; DO NOT EDIT.
//
// 生成方式：读取官方数据包 recipe/*.json，展开 #minecraft:bundles 与
// #minecraft:shulker_boxes 标签为物品 ID。当前 1.21.11 共 32 个 transmute 配方
// （16 色 bundle / 16 色潜影盒，原料为对应颜色染料）。

package registry

// TransmuteRecipe 是 crafting_transmute：一个可染色物品 + 一份材料 → 新物品。
// Input/Material 为可接受物品 ID 集合（标签已在生成期展开）。
type TransmuteRecipe struct {
	Name        string
	Input       []int32
	Material    []int32
	Result      int32
	ResultCount int32
}

var TransmuteRecipes = []TransmuteRecipe{
	{Name: "minecraft:black_bundle", Input: []int32{1036, 1052, 1048, 1049, 1046, 1044, 1050, 1040, 1045, 1042, 1039, 1038, 1043, 1047, 1051, 1037, 1041}, Material: []int32{1081}, Result: 1052, ResultCount: 1},
	{Name: "minecraft:blue_bundle", Input: []int32{1036, 1052, 1048, 1049, 1046, 1044, 1050, 1040, 1045, 1042, 1039, 1038, 1043, 1047, 1051, 1037, 1041}, Material: []int32{1077}, Result: 1048, ResultCount: 1},
	{Name: "minecraft:brown_bundle", Input: []int32{1036, 1052, 1048, 1049, 1046, 1044, 1050, 1040, 1045, 1042, 1039, 1038, 1043, 1047, 1051, 1037, 1041}, Material: []int32{1078}, Result: 1049, ResultCount: 1},
	{Name: "minecraft:cyan_bundle", Input: []int32{1036, 1052, 1048, 1049, 1046, 1044, 1050, 1040, 1045, 1042, 1039, 1038, 1043, 1047, 1051, 1037, 1041}, Material: []int32{1075}, Result: 1046, ResultCount: 1},
	{Name: "minecraft:gray_bundle", Input: []int32{1036, 1052, 1048, 1049, 1046, 1044, 1050, 1040, 1045, 1042, 1039, 1038, 1043, 1047, 1051, 1037, 1041}, Material: []int32{1073}, Result: 1044, ResultCount: 1},
	{Name: "minecraft:green_bundle", Input: []int32{1036, 1052, 1048, 1049, 1046, 1044, 1050, 1040, 1045, 1042, 1039, 1038, 1043, 1047, 1051, 1037, 1041}, Material: []int32{1079}, Result: 1050, ResultCount: 1},
	{Name: "minecraft:light_blue_bundle", Input: []int32{1036, 1052, 1048, 1049, 1046, 1044, 1050, 1040, 1045, 1042, 1039, 1038, 1043, 1047, 1051, 1037, 1041}, Material: []int32{1069}, Result: 1040, ResultCount: 1},
	{Name: "minecraft:light_gray_bundle", Input: []int32{1036, 1052, 1048, 1049, 1046, 1044, 1050, 1040, 1045, 1042, 1039, 1038, 1043, 1047, 1051, 1037, 1041}, Material: []int32{1074}, Result: 1045, ResultCount: 1},
	{Name: "minecraft:lime_bundle", Input: []int32{1036, 1052, 1048, 1049, 1046, 1044, 1050, 1040, 1045, 1042, 1039, 1038, 1043, 1047, 1051, 1037, 1041}, Material: []int32{1071}, Result: 1042, ResultCount: 1},
	{Name: "minecraft:magenta_bundle", Input: []int32{1036, 1052, 1048, 1049, 1046, 1044, 1050, 1040, 1045, 1042, 1039, 1038, 1043, 1047, 1051, 1037, 1041}, Material: []int32{1068}, Result: 1039, ResultCount: 1},
	{Name: "minecraft:orange_bundle", Input: []int32{1036, 1052, 1048, 1049, 1046, 1044, 1050, 1040, 1045, 1042, 1039, 1038, 1043, 1047, 1051, 1037, 1041}, Material: []int32{1067}, Result: 1038, ResultCount: 1},
	{Name: "minecraft:pink_bundle", Input: []int32{1036, 1052, 1048, 1049, 1046, 1044, 1050, 1040, 1045, 1042, 1039, 1038, 1043, 1047, 1051, 1037, 1041}, Material: []int32{1072}, Result: 1043, ResultCount: 1},
	{Name: "minecraft:purple_bundle", Input: []int32{1036, 1052, 1048, 1049, 1046, 1044, 1050, 1040, 1045, 1042, 1039, 1038, 1043, 1047, 1051, 1037, 1041}, Material: []int32{1076}, Result: 1047, ResultCount: 1},
	{Name: "minecraft:red_bundle", Input: []int32{1036, 1052, 1048, 1049, 1046, 1044, 1050, 1040, 1045, 1042, 1039, 1038, 1043, 1047, 1051, 1037, 1041}, Material: []int32{1080}, Result: 1051, ResultCount: 1},
	{Name: "minecraft:white_bundle", Input: []int32{1036, 1052, 1048, 1049, 1046, 1044, 1050, 1040, 1045, 1042, 1039, 1038, 1043, 1047, 1051, 1037, 1041}, Material: []int32{1066}, Result: 1037, ResultCount: 1},
	{Name: "minecraft:yellow_bundle", Input: []int32{1036, 1052, 1048, 1049, 1046, 1044, 1050, 1040, 1045, 1042, 1039, 1038, 1043, 1047, 1051, 1037, 1041}, Material: []int32{1070}, Result: 1041, ResultCount: 1},
	{Name: "minecraft:black_shulker_box", Input: []int32{581, 597, 593, 594, 591, 589, 595, 585, 590, 587, 584, 583, 588, 592, 596, 582, 586}, Material: []int32{1081}, Result: 597, ResultCount: 1},
	{Name: "minecraft:blue_shulker_box", Input: []int32{581, 597, 593, 594, 591, 589, 595, 585, 590, 587, 584, 583, 588, 592, 596, 582, 586}, Material: []int32{1077}, Result: 593, ResultCount: 1},
	{Name: "minecraft:brown_shulker_box", Input: []int32{581, 597, 593, 594, 591, 589, 595, 585, 590, 587, 584, 583, 588, 592, 596, 582, 586}, Material: []int32{1078}, Result: 594, ResultCount: 1},
	{Name: "minecraft:cyan_shulker_box", Input: []int32{581, 597, 593, 594, 591, 589, 595, 585, 590, 587, 584, 583, 588, 592, 596, 582, 586}, Material: []int32{1075}, Result: 591, ResultCount: 1},
	{Name: "minecraft:gray_shulker_box", Input: []int32{581, 597, 593, 594, 591, 589, 595, 585, 590, 587, 584, 583, 588, 592, 596, 582, 586}, Material: []int32{1073}, Result: 589, ResultCount: 1},
	{Name: "minecraft:green_shulker_box", Input: []int32{581, 597, 593, 594, 591, 589, 595, 585, 590, 587, 584, 583, 588, 592, 596, 582, 586}, Material: []int32{1079}, Result: 595, ResultCount: 1},
	{Name: "minecraft:light_blue_shulker_box", Input: []int32{581, 597, 593, 594, 591, 589, 595, 585, 590, 587, 584, 583, 588, 592, 596, 582, 586}, Material: []int32{1069}, Result: 585, ResultCount: 1},
	{Name: "minecraft:light_gray_shulker_box", Input: []int32{581, 597, 593, 594, 591, 589, 595, 585, 590, 587, 584, 583, 588, 592, 596, 582, 586}, Material: []int32{1074}, Result: 590, ResultCount: 1},
	{Name: "minecraft:lime_shulker_box", Input: []int32{581, 597, 593, 594, 591, 589, 595, 585, 590, 587, 584, 583, 588, 592, 596, 582, 586}, Material: []int32{1071}, Result: 587, ResultCount: 1},
	{Name: "minecraft:magenta_shulker_box", Input: []int32{581, 597, 593, 594, 591, 589, 595, 585, 590, 587, 584, 583, 588, 592, 596, 582, 586}, Material: []int32{1068}, Result: 584, ResultCount: 1},
	{Name: "minecraft:orange_shulker_box", Input: []int32{581, 597, 593, 594, 591, 589, 595, 585, 590, 587, 584, 583, 588, 592, 596, 582, 586}, Material: []int32{1067}, Result: 583, ResultCount: 1},
	{Name: "minecraft:pink_shulker_box", Input: []int32{581, 597, 593, 594, 591, 589, 595, 585, 590, 587, 584, 583, 588, 592, 596, 582, 586}, Material: []int32{1072}, Result: 588, ResultCount: 1},
	{Name: "minecraft:purple_shulker_box", Input: []int32{581, 597, 593, 594, 591, 589, 595, 585, 590, 587, 584, 583, 588, 592, 596, 582, 586}, Material: []int32{1076}, Result: 592, ResultCount: 1},
	{Name: "minecraft:red_shulker_box", Input: []int32{581, 597, 593, 594, 591, 589, 595, 585, 590, 587, 584, 583, 588, 592, 596, 582, 586}, Material: []int32{1080}, Result: 596, ResultCount: 1},
	{Name: "minecraft:white_shulker_box", Input: []int32{581, 597, 593, 594, 591, 589, 595, 585, 590, 587, 584, 583, 588, 592, 596, 582, 586}, Material: []int32{1066}, Result: 582, ResultCount: 1},
	{Name: "minecraft:yellow_shulker_box", Input: []int32{581, 597, 593, 594, 591, 589, 595, 585, 590, 587, 584, 583, 588, 592, 596, 582, 586}, Material: []int32{1070}, Result: 586, ResultCount: 1},
}
