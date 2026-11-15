//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"encoding/json"
	"math"
	"sort"
	"strings"
	"sync"

	"gmcs/internal/registry"
)

// 这是结构箱子/刷怪笼初始化的简化战利品表求值器。原版在容器首次打开时
// 才通过 LootContext 生成战利品；生成阶段只能使用固定随机源。这里在生成
// 模板方块实体时求值，并保留 LootTable 字段以便后续替换为延迟求值。
// 已支持：item/tag/empty/alternatives/sequence/group/loot_table、权重、
// rolls/bonus_rolls、set_count/limit_count、random_chance 条件。

type lootTableConfig struct {
	Pools []lootPoolConfig `json:"pools"`
}

type lootPoolConfig struct {
	Rolls      json.RawMessage   `json:"rolls"`
	BonusRolls json.RawMessage   `json:"bonus_rolls"`
	Entries    []lootEntryConfig `json:"entries"`
}

type lootEntryConfig struct {
	Type       string               `json:"type"`
	Name       string               `json:"name"`
	Table      string               `json:"value"`
	Weight     int                  `json:"weight"`
	Quality    int                  `json:"quality"`
	Children   []lootEntryConfig    `json:"children"`
	Entries    []lootEntryConfig    `json:"entries"`
	Functions  []lootFunctionConfig `json:"functions"`
	Conditions []json.RawMessage    `json:"conditions"`
}

type lootFunctionConfig struct {
	Function string          `json:"function"`
	Count    json.RawMessage `json:"count"`
	Add      bool            `json:"add"`
}

type lootNumber struct {
	Kind     string  `json:"type"`
	Constant float64 `json:"value"`
	Min      float64 `json:"min"`
	Max      float64 `json:"max"`
}

func (n *lootNumber) UnmarshalJSON(raw []byte) error {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		n.Kind = "minecraft:constant"
		n.Constant = 1
		return nil
	}
	if raw[0] != '{' {
		var value float64
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		n.Kind = "minecraft:constant"
		n.Constant = value
		return nil
	}
	type alias lootNumber
	var decoded alias
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	*n = lootNumber(decoded)
	if n.Kind == "" {
		n.Kind = "minecraft:uniform"
	}
	return nil
}

func (n lootNumber) sample(random *legacyRandom) float64 {
	switch n.Kind {
	case "minecraft:uniform":
		if n.Max <= n.Min {
			return n.Min
		}
		return n.Min + random.nextDouble()*(n.Max-n.Min)
	default:
		return n.Constant
	}
}

func (n lootNumber) sampleInt(random *legacyRandom) int {
	switch n.Kind {
	case "minecraft:uniform":
		minimum, maximum := int(math.Round(n.Min)), int(math.Round(n.Max))
		if maximum < minimum {
			maximum = minimum
		}
		return minimum + random.nextIntBounded(maximum-minimum+1)
	default:
		return int(math.Round(n.Constant))
	}
}

type lootRuntime struct {
	mu     sync.Mutex
	tables map[string][]GeneratedItem
}

var lootTablesCache sync.Map // string -> *lootTableConfig

func (d *structureData) LootItems(name string, random *legacyRandom) []GeneratedItem {
	name = qualify(name)
	raw, ok := d.LootTables[name]
	if !ok {
		return nil
	}
	var table *lootTableConfig
	if cached, ok := lootTablesCache.Load(name); ok {
		table = cached.(*lootTableConfig)
	} else {
		parsed := &lootTableConfig{}
		if err := json.Unmarshal(raw, parsed); err != nil {
			return nil
		}
		lootTablesCache.Store(name, parsed)
		table = parsed
	}
	return d.evalLootTable(table, random, 0)
}

func (d *structureData) evalLootTable(table *lootTableConfig, random *legacyRandom, depth int) []GeneratedItem {
	if depth > 8 {
		return nil
	}
	var items []GeneratedItem
	for _, pool := range table.Pools {
		rolls := 1
		if len(pool.Rolls) != 0 {
			var number lootNumber
			if json.Unmarshal(pool.Rolls, &number) == nil {
				rolls = number.sampleInt(random)
			}
		}
		if rolls < 0 {
			rolls = 0
		}
		for i := 0; i < rolls; i++ {
			entry, ok := chooseLootEntry(d, pool.Entries, random)
			if !ok {
				continue
			}
			items = append(items, d.evalLootEntry(entry, random, depth+1)...)
		}
	}
	return items
}

func chooseLootEntry(d *structureData, entries []lootEntryConfig, random *legacyRandom) (lootEntryConfig, bool) {
	total := 0
	for _, entry := range entries {
		if !lootConditionsPass(entry.Conditions, random) {
			continue
		}
		weight := entry.Weight
		if weight <= 0 {
			weight = 1
		}
		total += weight
	}
	if total <= 0 {
		return lootEntryConfig{}, false
	}
	roll := random.nextIntBounded(total)
	for _, entry := range entries {
		if !lootConditionsPass(entry.Conditions, random) {
			continue
		}
		weight := entry.Weight
		if weight <= 0 {
			weight = 1
		}
		if roll < weight {
			return entry, true
		}
		roll -= weight
	}
	return lootEntryConfig{}, false
}

func (d *structureData) evalLootEntry(entry lootEntryConfig, random *legacyRandom, depth int) []GeneratedItem {
	switch strings.TrimPrefix(entry.Type, "minecraft:") {
	case "empty":
		return nil
	case "item":
		items := []GeneratedItem{{Name: qualify(entry.Name), Count: 1}}
		return applyLootFunctions(items, entry.Functions, random)
	case "tag":
		names := lootTagItems(entry.Name)
		if len(names) == 0 {
			return nil
		}
		items := []GeneratedItem{{Name: names[random.nextIntBounded(len(names))], Count: 1}}
		return applyLootFunctions(items, entry.Functions, random)
	case "alternatives":
		for _, child := range entry.Children {
			if lootConditionsPass(child.Conditions, random) {
				return d.evalLootEntry(child, random, depth+1)
			}
		}
		if len(entry.Entries) > 0 {
			return d.evalLootEntry(entry.Entries[0], random, depth+1)
		}
		return nil
	case "sequence":
		var output []GeneratedItem
		for _, child := range entry.Children {
			output = append(output, d.evalLootEntry(child, random, depth+1)...)
		}
		return output
	case "group":
		var output []GeneratedItem
		for _, child := range entry.Children {
			output = append(output, d.evalLootEntry(child, random, depth+1)...)
		}
		return output
	case "loot_table":
		tableName := entry.Table
		if tableName == "" {
			tableName = entry.Name
		}
		raw, ok := d.LootTables[qualify(tableName)]
		if !ok {
			return nil
		}
		table := &lootTableConfig{}
		if json.Unmarshal(raw, table) != nil {
			return nil
		}
		return d.evalLootTable(table, random, depth+1)
	default:
		return nil
	}
}

func applyLootFunctions(items []GeneratedItem, functions []lootFunctionConfig, random *legacyRandom) []GeneratedItem {
	for _, function := range functions {
		switch strings.TrimPrefix(function.Function, "minecraft:") {
		case "set_count":
			var number lootNumber
			if json.Unmarshal(function.Count, &number) == nil {
				count := number.sampleInt(random)
				for i := range items {
					if function.Add {
						items[i].Count += count
					} else {
						items[i].Count = count
					}
				}
			}
		case "limit_count":
			var limit struct {
				Min int `json:"min"`
				Max int `json:"max"`
			}
			if json.Unmarshal(function.Count, &limit) == nil {
				for i := range items {
					if limit.Max > 0 && items[i].Count > limit.Max {
						items[i].Count = limit.Max
					}
					if items[i].Count < limit.Min {
						items[i].Count = limit.Min
					}
				}
			}
		}
	}
	for i := range items {
		if items[i].Count > 64 {
			items[i].Count = 64
		}
	}
	return items
}

func lootConditionsPass(conditions []json.RawMessage, random *legacyRandom) bool {
	for _, raw := range conditions {
		var condition struct {
			Condition string  `json:"condition"`
			Chance    float64 `json:"chance"`
		}
		if json.Unmarshal(raw, &condition) != nil {
			continue
		}
		switch strings.TrimPrefix(condition.Condition, "minecraft:") {
		case "random_chance", "random_chance_with_luck":
			if random.nextDouble() >= condition.Chance {
				return false
			}
		}
	}
	return true
}

func lootTagItems(tag string) []string {
	tag = strings.TrimPrefix(qualify(tag), "#")
	names := make([]string, 0)
	for name, id := range registry.ItemIDs {
		if registry.ItemInTag(tag, id) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// initLootBlockEntity 在模板放置时填充战利品表内容；保留 LootTable 字段，
// 便于上层决定是否延迟求值。
func initLootBlockEntity(entity *GeneratedBlockEntity, random *legacyRandom) {
	data, err := LoadStructureData()
	if err != nil {
		return
	}
	if entity == nil || entity.LootTable == "" || len(entity.Items) != 0 {
		return
	}
	items := data.LootItems(entity.LootTable, random)
	if len(items) == 0 {
		return
	}
	// 原版战利品会装入随机槽位；这里同样打乱，最多 27 个槽位。
	slots := make([]int, 27)
	for i := range slots {
		slots[i] = i
	}
	for i := len(slots) - 1; i > 0; i-- {
		j := random.nextIntBounded(i + 1)
		slots[i], slots[j] = slots[j], slots[i]
	}
	placed := make([]GeneratedItem, 0, len(items))
	for i, item := range items {
		if i >= len(slots) {
			break
		}
		placed = append(placed, item)
	}
	entity.Items = placed
	entity.Data = nil
}
