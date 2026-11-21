//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

// Code generated from Minecraft 1.21.11 reports/registries.json (minecraft:potion); DO NOT EDIT.
// 药水是同步注册表，potion_contents 组件中的 Holder 使用裸注册表 ID。

package registry

import "strings"

// potionNameIDs 保存 minecraft:potion 注册表条目名 → protocol_id。
var potionNameIDs = map[string]int32{
	"minecraft:water":                0,
	"minecraft:mundane":              1,
	"minecraft:thick":                2,
	"minecraft:awkward":              3,
	"minecraft:night_vision":         4,
	"minecraft:long_night_vision":    5,
	"minecraft:invisibility":         6,
	"minecraft:long_invisibility":    7,
	"minecraft:leaping":              8,
	"minecraft:long_leaping":         9,
	"minecraft:strong_leaping":       10,
	"minecraft:fire_resistance":      11,
	"minecraft:long_fire_resistance": 12,
	"minecraft:swiftness":            13,
	"minecraft:long_swiftness":       14,
	"minecraft:strong_swiftness":     15,
	"minecraft:slowness":             16,
	"minecraft:long_slowness":        17,
	"minecraft:strong_slowness":      18,
	"minecraft:turtle_master":        19,
	"minecraft:long_turtle_master":   20,
	"minecraft:strong_turtle_master": 21,
	"minecraft:water_breathing":      22,
	"minecraft:long_water_breathing": 23,
	"minecraft:healing":              24,
	"minecraft:strong_healing":       25,
	"minecraft:harming":              26,
	"minecraft:strong_harming":       27,
	"minecraft:poison":               28,
	"minecraft:long_poison":          29,
	"minecraft:strong_poison":        30,
	"minecraft:regeneration":         31,
	"minecraft:long_regeneration":    32,
	"minecraft:strong_regeneration":  33,
	"minecraft:strength":             34,
	"minecraft:long_strength":        35,
	"minecraft:strong_strength":      36,
	"minecraft:weakness":             37,
	"minecraft:long_weakness":        38,
	"minecraft:luck":                 39,
	"minecraft:slow_falling":         40,
	"minecraft:long_slow_falling":    41,
	"minecraft:wind_charged":         42,
	"minecraft:weaving":              43,
	"minecraft:oozing":               44,
	"minecraft:infested":             45,
}

// PotionIDByName 按药水名返回注册表 ID；允许省略 "minecraft:" 命名空间。
func PotionIDByName(name string) (int32, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, false
	}
	if !strings.Contains(name, ":") {
		name = "minecraft:" + name
	}
	id, ok := potionNameIDs[name]
	return id, ok
}
