package registry

import (
	"strings"
	"testing"
)

// TestSynchronizedRegistriesNonEmpty 验证每个同步注册表都有条目，
// 条目唯一且都是命名空间 ID。
// 客户端会按发送顺序为条目分配数字 ID，条目缺失或重复都会破坏世界数据。
func TestSynchronizedRegistriesNonEmpty(t *testing.T) {
	registries := Synchronized()
	if len(registries) == 0 {
		t.Fatal("no synchronized registries generated")
	}
	for _, reg := range registries {
		if !strings.Contains(reg.Name, ":") {
			t.Errorf("registry %q is not a namespaced ID", reg.Name)
		}
		if len(reg.Entries) == 0 {
			t.Errorf("registry %s has no entries", reg.Name)
		}
		seen := make(map[string]bool, len(reg.Entries))
		for _, entry := range reg.Entries {
			if !strings.Contains(entry, ":") {
				t.Errorf("registry %s entry %q is not a namespaced ID", reg.Name, entry)
			}
			if seen[entry] {
				t.Errorf("registry %s has duplicate entry %q", reg.Name, entry)
			}
			seen[entry] = true
		}
	}
}

// TestSynchronizedRegistrySet 验证 1.21.11 的全部同步注册表都已生成。
// 客户端在配置阶段会重建同步注册表：未发送的注册表会变为空，
// 缺少任何一个都会导致客户端拒绝完成配置。
func TestSynchronizedRegistrySet(t *testing.T) {
	want := []string{
		"minecraft:banner_pattern",
		"minecraft:cat_variant",
		"minecraft:chat_type",
		"minecraft:chicken_variant",
		"minecraft:cow_variant",
		"minecraft:damage_type",
		"minecraft:dialog",
		"minecraft:dimension_type",
		"minecraft:enchantment",
		"minecraft:frog_variant",
		"minecraft:instrument",
		"minecraft:jukebox_song",
		"minecraft:painting_variant",
		"minecraft:pig_variant",
		"minecraft:test_environment",
		"minecraft:test_instance",
		"minecraft:timeline",
		"minecraft:trim_material",
		"minecraft:trim_pattern",
		"minecraft:wolf_sound_variant",
		"minecraft:wolf_variant",
		"minecraft:worldgen/biome",
		"minecraft:zombie_nautilus_variant",
	}
	have := make(map[string]bool)
	for _, reg := range Synchronized() {
		have[reg.Name] = true
	}
	for _, name := range want {
		if !have[name] {
			t.Errorf("missing synchronized registry %s", name)
		}
	}
	if len(Synchronized()) != len(want) {
		t.Errorf("generated %d registries, want %d", len(Synchronized()), len(want))
	}
}

// TestFixedIDs 验证生成的固定 ID 常量确实指向预期条目。
func TestFixedIDs(t *testing.T) {
	for _, reg := range Synchronized() {
		switch reg.Name {
		case "minecraft:dimension_type":
			if DimensionTypeOverworldID < 0 || DimensionTypeOverworldID >= len(reg.Entries) {
				t.Fatalf("DimensionTypeOverworldID %d out of range [0,%d)", DimensionTypeOverworldID, len(reg.Entries))
			}
			if got := reg.Entries[DimensionTypeOverworldID]; got != "minecraft:overworld" {
				t.Errorf("DimensionTypeOverworldID=%d maps to %s, want minecraft:overworld", DimensionTypeOverworldID, got)
			}
		case "minecraft:worldgen/biome":
			if BiomePlainsID < 0 || BiomePlainsID >= len(reg.Entries) {
				t.Fatalf("BiomePlainsID %d out of range [0,%d)", BiomePlainsID, len(reg.Entries))
			}
			if got := reg.Entries[BiomePlainsID]; got != "minecraft:plains" {
				t.Errorf("BiomePlainsID=%d maps to %s, want minecraft:plains", BiomePlainsID, got)
			}
		}
	}
}

// TestAllTags 验证标签数据完整：注册表均已同步或为静态注册表、
// 条目 ID 在范围内、无重复，并且包含客户端解析本地数据所需的关键标签。
func TestAllTags(t *testing.T) {
	entryCounts := make(map[string]int)
	for _, reg := range Synchronized() {
		entryCounts[reg.Name] = len(reg.Entries)
	}
	for name, size := range staticRegistrySizes {
		entryCounts[name] = int(size)
	}

	seenTags := make(map[string]map[string]bool)
	for _, reg := range AllTags() {
		entryCount, ok := entryCounts[reg.Name]
		if !ok {
			t.Errorf("tags for unsynchronized registry %s", reg.Name)
			continue
		}
		if len(reg.Tags) == 0 {
			t.Errorf("registry %s has no tags", reg.Name)
		}
		if seenTags[reg.Name] == nil {
			seenTags[reg.Name] = make(map[string]bool)
		}
		for _, tag := range reg.Tags {
			if !strings.Contains(tag.Name, ":") {
				t.Errorf("registry %s tag %q is not a namespaced ID", reg.Name, tag.Name)
			}
			if seenTags[reg.Name][tag.Name] {
				t.Errorf("registry %s has duplicate tag %s", reg.Name, tag.Name)
			}
			seenTags[reg.Name][tag.Name] = true

			// 原版数据中存在空 tag（如 dialog 的 pause_screen_additions），因此不要求非空。
			seen := make(map[int32]bool, len(tag.Entries))
			for _, id := range tag.Entries {
				if id < 0 || int(id) >= entryCount {
					t.Fatalf("registry %s tag %s references out-of-range ID %d (entry count %d)", reg.Name, tag.Name, id, entryCount)
				}
				if seen[id] {
					t.Errorf("registry %s tag %s has duplicate ID %d", reg.Name, tag.Name, id)
				}
				seen[id] = true
			}
		}
	}

	// 实机客户端会立即校验这些标签：缺失会导致 "Failed to parse local data" /
	// "Unbound tags in registry ..."，随后客户端以网络协议错误断开。
	required := map[string][]string{
		"minecraft:banner_pattern": {
			"minecraft:pattern_item/bordure_indented",
			"minecraft:pattern_item/creeper",
			"minecraft:pattern_item/field_masoned",
			"minecraft:pattern_item/flow",
			"minecraft:pattern_item/flower",
			"minecraft:pattern_item/globe",
			"minecraft:pattern_item/guster",
			"minecraft:pattern_item/mojang",
			"minecraft:pattern_item/piglin",
			"minecraft:pattern_item/skull",
		},
		"minecraft:damage_type": {
			"minecraft:bypasses_shield",
			"minecraft:is_explosion",
			"minecraft:is_fire",
		},
		"minecraft:timeline": {
			"minecraft:in_overworld",
		},
		// 以下为 enchantment 本地数据（Known Packs）解析时引用的静态注册表标签。
		"minecraft:item": {
			"minecraft:enchantable/head_armor",
			"minecraft:enchantable/armor",
			"minecraft:enchantable/durability",
			"minecraft:enchantable/bow",
		},
		"minecraft:block": {
			"minecraft:lightning_rods",
			"minecraft:soul_speed_blocks",
			"minecraft:blocks_wind_charge_explosions",
		},
		"minecraft:entity_type": {
			"minecraft:arrows",
			"minecraft:sensitive_to_smite",
			"minecraft:sensitive_to_bane_of_arthropods",
			"minecraft:sensitive_to_impaling",
		},
	}
	for registryName, tags := range required {
		have := seenTags[registryName]
		for _, tag := range tags {
			if !have[tag] {
				t.Errorf("missing required tag %s in registry %s", tag, registryName)
			}
		}
	}
}

// TestStaticRegistryTags 验证静态注册表标签齐全且条目数与官方注册表一致。
// 静态注册表的条目 ID 是客户端内置的全局注册表 ID，不随发送顺序变化。
func TestStaticRegistryTags(t *testing.T) {
	// 条目数下限用于发现 reports 与客户端版本不匹配（例如误用旧版本报告）。
	minimumSizes := map[string]int32{
		"minecraft:block":                  1000,
		"minecraft:item":                   1000,
		"minecraft:entity_type":            100,
		"minecraft:fluid":                  2,
		"minecraft:game_event":             50,
		"minecraft:point_of_interest_type": 10,
	}
	for name, min := range minimumSizes {
		size, ok := staticRegistrySizes[name]
		if !ok {
			t.Errorf("static registry %s missing from staticRegistrySizes", name)
			continue
		}
		if size < min {
			t.Errorf("static registry %s has %d entries, want at least %d", name, size, min)
		}
	}

	have := make(map[string]bool)
	for _, reg := range AllTags() {
		if _, ok := staticRegistrySizes[reg.Name]; ok {
			have[reg.Name] = true
		}
	}
	for name := range staticRegistrySizes {
		if !have[name] {
			t.Errorf("static registry %s has no tags", name)
		}
	}
}
