//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// 本文件从 jar 内置数据包生成「附魔」相关数据表（enchants_generated.go）：
//
//   - 每个附魔的注册表 ID：等于 data/minecraft/enchantment/<name>.json 的
//     文件路径字典序下标（与客户端内置注册表一致；已与外部数据源交叉验证）；
//   - 等级成本区间（min_cost / max_cost 展开为逐级数组）；
//   - supported_items（可附魔物品，标签已展开为物品 ID）；
//   - exclusive_set（互斥附魔，标签已展开为附魔 ID）；
//   - in_enchanting_table 标签（附魔台可选附魔集合）；
//   - enchantment_power_provider / enchantment_power_transmitter 方块标签
//     （书架与透光方块，用于附魔台书架计数）。

// parsedEnchantment 是一个附魔定义中 gmcs 需要的字段。
type parsedEnchantment struct {
	name         string
	maxLevel     int32
	weight       int32
	anvilCost    int32
	minCosts     []int32 // 索引 level-1
	maxCosts     []int32
	supported    []string // 物品名或 #标签
	primaryItems []string
	exclusiveSet string // "" = 无；"#..." = 标签；其余为单条目名
}

// enchantmentJSON 对应 data/minecraft/enchantment/*.json 的结构。
type enchantmentJSON struct {
	MaxLevel     int32               `json:"max_level"`
	Weight       int32               `json:"weight"`
	AnvilCost    int32               `json:"anvil_cost"`
	MinCost      levelBasedValueJSON `json:"min_cost"`
	MaxCost      levelBasedValueJSON `json:"max_cost"`
	Supported    json.RawMessage     `json:"supported_items"`
	Primary      json.RawMessage     `json:"primary_items"`
	ExclusiveSet json.RawMessage     `json:"exclusive_set"`
}

// levelBasedValueJSON 是 LevelBasedValue 的 flat 形式（1.21.11 数据包中全部为
// linear：base + per_level_above_first * (level-1)，calculate 取 int 截断）。
type levelBasedValueJSON struct {
	Base            float64 `json:"base"`
	PerLevelAboveF1 float64 `json:"per_level_above_first"`
}

// loadEnchantments 按注册表条目顺序（字典序，= 注册表 ID 顺序）读取附魔定义。
func loadEnchantments(reader *zip.Reader, order []string) ([]parsedEnchantment, error) {
	files := make(map[string]*zip.File, len(order))
	for _, file := range reader.File {
		name := file.Name
		if !strings.HasPrefix(name, "data/minecraft/enchantment/") || !strings.HasSuffix(name, ".json") {
			continue
		}
		base := strings.TrimSuffix(strings.TrimPrefix(name, "data/minecraft/enchantment/"), ".json")
		files[base] = file
	}
	enchants := make([]parsedEnchantment, 0, len(order))
	for _, fullName := range order {
		base := strings.TrimPrefix(fullName, "minecraft:")
		file, ok := files[base]
		if !ok {
			return nil, fmt.Errorf("附魔 %s 的 JSON 不存在（数据包版本不符？）", fullName)
		}
		content, err := readZipFile(file)
		if err != nil {
			return nil, fmt.Errorf("读取 %s：%w", file.Name, err)
		}
		var raw enchantmentJSON
		if err := json.Unmarshal(content, &raw); err != nil {
			return nil, fmt.Errorf("解析 %s：%w", file.Name, err)
		}
		if raw.MaxLevel < 1 || raw.MaxLevel > 255 {
			return nil, fmt.Errorf("附魔 %s 的 max_level 异常：%d", fullName, raw.MaxLevel)
		}
		if raw.Weight < 1 {
			return nil, fmt.Errorf("附魔 %s 的 weight 异常：%d", fullName, raw.Weight)
		}
		supported, err := parseOptionalItemList(raw.Supported)
		if err != nil {
			return nil, fmt.Errorf("解析 %s 的 supported_items：%w", file.Name, err)
		}
		primary, err := parseOptionalItemList(raw.Primary)
		if err != nil {
			return nil, fmt.Errorf("解析 %s 的 primary_items：%w", file.Name, err)
		}
		if len(raw.Primary) > 0 && string(raw.Primary) != "null" && len(primary) == 0 {
			return nil, fmt.Errorf("附魔 %s 的 primary_items 显式为空（不支持该形式）", fullName)
		}
		if len(supported) == 0 {
			return nil, fmt.Errorf("附魔 %s 的 supported_items 为空", fullName)
		}
		exclusive := ""
		if len(raw.ExclusiveSet) > 0 && string(raw.ExclusiveSet) != "null" {
			if err := json.Unmarshal(raw.ExclusiveSet, &exclusive); err != nil {
				return nil, fmt.Errorf("解析 %s 的 exclusive_set（仅支持字符串形式）：%w", file.Name, err)
			}
		}
		ench := parsedEnchantment{
			name:         fullName,
			maxLevel:     raw.MaxLevel,
			weight:       raw.Weight,
			anvilCost:    raw.AnvilCost,
			supported:    supported,
			primaryItems: primary,
			exclusiveSet: exclusive,
		}
		ench.minCosts = make([]int32, raw.MaxLevel)
		ench.maxCosts = make([]int32, raw.MaxLevel)
		for level := int32(0); level < raw.MaxLevel; level++ {
			ench.minCosts[level] = int32(raw.MinCost.Base + raw.MinCost.PerLevelAboveF1*float64(level))
			ench.maxCosts[level] = int32(raw.MaxCost.Base + raw.MaxCost.PerLevelAboveF1*float64(level))
		}
		enchants = append(enchants, ench)
	}
	return enchants, nil
}

// parseOptionalItemList 解析可空的物品（或标签）引用：缺失或 null 返回 nil。
func parseOptionalItemList(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	return parseItemList(raw)
}

// enchantSource 汇总生成附魔表所需的全部输入。
type enchantSource struct {
	enchants  []parsedEnchantment
	enchIndex map[string]int32   // 附魔注册名 → ID
	enchTags  map[string][]int32 // minecraft:enchantment 的标签（已展开）
	blockTags map[string][]int32 // minecraft:block 的标签（protocol_id）
	itemIDs   map[string]int32   // 物品注册名 → ID
	itemTags  map[string][]int32 // minecraft:item 的标签
}

// tagEntries 把展开后的标签列表转为「标签名 → 条目 ID」映射。
func tagEntries(tags []resolvedTag) map[string][]int32 {
	m := make(map[string][]int32, len(tags))
	for _, tag := range tags {
		m[tag.name] = tag.entries
	}
	return m
}

// resolveEnchantmentRef 把 exclusive_set 引用（单条目或 #标签）展开为附魔 ID。
func resolveEnchantmentRef(ref string, src *enchantSource) ([]int32, error) {
	if ref == "" {
		return nil, nil
	}
	if strings.HasPrefix(ref, "#") {
		ids, ok := src.enchTags[ref[1:]]
		if !ok {
			return nil, fmt.Errorf("附魔标签 %s 不存在", ref)
		}
		return append([]int32(nil), ids...), nil
	}
	id, ok := src.enchIndex[ref]
	if !ok {
		return nil, fmt.Errorf("附魔条目 %s 不存在", ref)
	}
	return []int32{id}, nil
}

// writeEnchants 生成 enchants_generated.go。
func writeEnchants(outDir, version string, src *enchantSource) error {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "// Code generated by tools/genregistries from Minecraft %s 内置数据包; DO NOT EDIT.\n", version)
	buf.WriteString("//\n")
	buf.WriteString("// 数据来源：data/minecraft/enchantment/*.json 与 tags/{enchantment,block,item}。\n")
	buf.WriteString("// 附魔注册表 ID 等于数据包文件路径的字典序下标（与客户端内置注册表一致；\n")
	buf.WriteString("// 已与外部数据源 minecraft-data 1.21.11 交叉验证：id 0 = aqua_affinity）。\n\n")
	buf.WriteString("package registry\n\n")

	writeInt32List := func(varName, doc string, values []int32) {
		fmt.Fprintf(&buf, "// %s\nvar %s = []int32{", doc, varName)
		for i, v := range values {
			if i > 0 {
				buf.WriteString(", ")
			}
			fmt.Fprintf(&buf, "%d", v)
		}
		buf.WriteString("}\n\n")
	}

	buf.WriteString("// EnchantmentNames 是全部附魔的注册名；下标即附魔注册表 ID。\n")
	buf.WriteString("var EnchantmentNames = []string{\n")
	for _, ench := range src.enchants {
		fmt.Fprintf(&buf, "\t%q,\n", ench.name)
	}
	buf.WriteString("}\n\n")

	fmt.Fprintf(&buf, "// EnchantmentCount 是附魔总数（%d）。\nconst EnchantmentCount = %d\n\n", len(src.enchants), len(src.enchants))

	vals := make([]int32, len(src.enchants))
	for i, ench := range src.enchants {
		vals[i] = ench.maxLevel
	}
	writeInt32List("EnchantmentMaxLevels", "EnchantmentMaxLevels 保存每个附魔的最大等级。", vals)
	for i, ench := range src.enchants {
		vals[i] = ench.weight
	}
	writeInt32List("EnchantmentWeights", "EnchantmentWeights 保存每个附魔的权重（随机选取用）。", vals)
	for i, ench := range src.enchants {
		vals[i] = ench.anvilCost
	}
	writeInt32List("EnchantmentAnvilCosts", "EnchantmentAnvilCosts 保存每个附魔的铁砧惩罚系数。", vals)

	writeInt32Matrix := func(varName, doc string, pick func(parsedEnchantment) []int32) {
		fmt.Fprintf(&buf, "// %s\nvar %s = [][]int32{\n", doc, varName)
		for _, ench := range src.enchants {
			buf.WriteString("\t{")
			for i, v := range pick(ench) {
				if i > 0 {
					buf.WriteString(", ")
				}
				fmt.Fprintf(&buf, "%d", v)
			}
			buf.WriteString("},\n")
		}
		buf.WriteString("}\n\n")
	}
	writeInt32Matrix("EnchantmentMinCosts", "EnchantmentMinCosts 保存每个附魔逐级的最低成本（索引 level-1；\n// min_cost 的 linear 展开）。", func(e parsedEnchantment) []int32 { return e.minCosts })
	writeInt32Matrix("EnchantmentMaxCosts", "EnchantmentMaxCosts 保存每个附魔逐级的最高成本（索引 level-1）。", func(e parsedEnchantment) []int32 { return e.maxCosts })

	// supported_items / exclusive_set 展开。
	buf.WriteString("// EnchantmentSupportedItems 保存每个附魔可作用的物品 ID（supported_items，\n// 标签已展开，升序去重）。\nvar EnchantmentSupportedItems = [][]int32{\n")
	for _, ench := range src.enchants {
		ids, err := expandItemRefs(ench.supported, src.itemIDs, src.itemTags)
		if err != nil {
			return fmt.Errorf("展开 %s 的 supported_items：%w", ench.name, err)
		}
		buf.WriteString("\t{")
		for i, id := range ids {
			if i > 0 {
				buf.WriteString(", ")
			}
			fmt.Fprintf(&buf, "%d", id)
		}
		buf.WriteString("},\n")
	}
	buf.WriteString("}\n\n")

	buf.WriteString("// EnchantmentPrimaryItems 保存每个附魔 primary_items 的展开（升序去重；nil 表示定义缺省）。\n")
	buf.WriteString("// 原版附魔台要求物品命中 supported_items，且 primary_items 存在时必须同时命中\n")
	buf.WriteString("// primary_items（Enchantment 字节码：supported && (primary 为空 || 命中)）；\n")
	buf.WriteString("// 铁砧/命令等路径只要求 supported_items。\n")
	buf.WriteString("var EnchantmentPrimaryItems = [][]int32{\n")
	for _, ench := range src.enchants {
		if len(ench.primaryItems) == 0 {
			buf.WriteString("\tnil,\n")
			continue
		}
		ids, err := expandItemRefs(ench.primaryItems, src.itemIDs, src.itemTags)
		if err != nil {
			return fmt.Errorf("展开 %s 的 primary_items：%w", ench.name, err)
		}
		buf.WriteString("\t{")
		for i, id := range ids {
			if i > 0 {
				buf.WriteString(", ")
			}
			fmt.Fprintf(&buf, "%d", id)
		}
		buf.WriteString("},\n")
	}
	buf.WriteString("}\n\n")

	buf.WriteString("// EnchantmentExclusive 保存每个附魔的互斥附魔 ID 集合（exclusive_set 展开，\n// 升序；判定时双向检查）。\nvar EnchantmentExclusive = [][]int32{\n")
	for _, ench := range src.enchants {
		ids, err := resolveEnchantmentRef(ench.exclusiveSet, src)
		if err != nil {
			return fmt.Errorf("展开 %s 的 exclusive_set：%w", ench.name, err)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		buf.WriteString("\t{")
		for i, id := range ids {
			if i > 0 {
				buf.WriteString(", ")
			}
			fmt.Fprintf(&buf, "%d", id)
		}
		buf.WriteString("},\n")
	}
	buf.WriteString("}\n\n")

	// in_enchanting_table：保持标签定义顺序（影响随机选取序列，禁止重排）。
	inTable, ok := src.enchTags["minecraft:in_enchanting_table"]
	if !ok {
		return fmt.Errorf("标签 minecraft:in_enchanting_table 不存在（数据包版本不符？）")
	}
	inTable = append([]int32(nil), inTable...)
	writeInt32List("EnchantmentsInEnchantingTable", "EnchantmentsInEnchantingTable 是附魔台可选附魔的 ID 序列\n// （in_enchanting_table 标签展开；顺序即标签定义顺序，影响附魔台的\n// 随机选取序列，禁止重排）。", inTable)

	// 方块标签。
	provider, ok := src.blockTags["minecraft:enchantment_power_provider"]
	if !ok {
		return fmt.Errorf("方块标签 minecraft:enchantment_power_provider 不存在")
	}
	provider = append([]int32(nil), provider...)
	sort.Slice(provider, func(i, j int) bool { return provider[i] < provider[j] })
	writeInt32List("EnchantmentPowerProviders", "EnchantmentPowerProviders 是提供附魔能量的方块 ID（书架，升序）。", provider)

	transmitter, ok := src.blockTags["minecraft:enchantment_power_transmitter"]
	if !ok {
		return fmt.Errorf("方块标签 minecraft:enchantment_power_transmitter 不存在")
	}
	transmitter = append([]int32(nil), transmitter...)
	sort.Slice(transmitter, func(i, j int) bool { return transmitter[i] < transmitter[j] })
	writeInt32List("EnchantmentPowerTransmitters", "EnchantmentPowerTransmitters 是传播附魔能量的方块 ID\n// （enchantment_power_transmitter = #replaceable 展开，升序）。", transmitter)

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return fmt.Errorf("格式化附魔表：%w", err)
	}
	return os.WriteFile(filepath.Join(outDir, "enchants_generated.go"), formatted, 0o644)
}
