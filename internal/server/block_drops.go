package server

import (
	"log/slog"
	"strings"

	"gmcs/internal/item"
	"gmcs/internal/registry"
)

// 方块破坏掉落表。
//
// 规则（简化，见 docs/TODO.md 已知限制）：
//   - 默认掉落与方块同名的物品（若物品注册表中存在）；
//   - 少数与原版一致的“不同名”特例见 blockDropOverrides；
//   - 少数无掉落方块（玻璃、树叶、冰、水/岩浆/基岩等）见 blockDropsNothing；
//   - 不实现工具要求、精准采集、时运、剪刀与概率掉落（砂砾的燧石、
//     树叶的树苗/木棍等），挖掘也没有时间：生存模式仍以客户端提交的
//     STOP_DESTROY_BLOCK 为准。

// blockDropSpec 是一条掉落规则（Item 为空表示无掉落）。
type blockDropSpec struct {
	Item  string
	Count int32
}

// blockDropOverrides 是“掉落物与方块不同名”的特例表（按原版无工具默认掉落）。
var blockDropOverrides = map[string]blockDropSpec{
	"minecraft:stone":              {Item: "minecraft:cobblestone", Count: 1},
	"minecraft:grass_block":        {Item: "minecraft:dirt", Count: 1},
	"minecraft:dirt_path":          {Item: "minecraft:dirt", Count: 1},
	"minecraft:farmland":           {Item: "minecraft:dirt", Count: 1},
	"minecraft:clay":               {Item: "minecraft:clay_ball", Count: 4},
	"minecraft:gravel":             {Item: "minecraft:gravel", Count: 1}, // 原版 10% 燧石，此处固定为砂砾
	"minecraft:coal_ore":           {Item: "minecraft:coal", Count: 1},
	"minecraft:deepslate_coal_ore": {Item: "minecraft:coal", Count: 1},
	"minecraft:iron_ore":           {Item: "minecraft:raw_iron", Count: 1},
	"minecraft:deepslate_iron_ore": {Item: "minecraft:raw_iron", Count: 1},
	"minecraft:copper_ore":         {Item: "minecraft:raw_copper", Count: 1},
	"minecraft:deepslate_copper_ore": {
		Item:  "minecraft:raw_copper",
		Count: 1,
	},
	"minecraft:gold_ore":           {Item: "minecraft:raw_gold", Count: 1},
	"minecraft:deepslate_gold_ore": {Item: "minecraft:raw_gold", Count: 1},
	"minecraft:diamond_ore":        {Item: "minecraft:diamond", Count: 1},
	"minecraft:deepslate_diamond_ore": {
		Item:  "minecraft:diamond",
		Count: 1,
	},
	"minecraft:emerald_ore":           {Item: "minecraft:emerald", Count: 1},
	"minecraft:deepslate_emerald_ore": {Item: "minecraft:emerald", Count: 1},
	"minecraft:lapis_ore":             {Item: "minecraft:lapis_lazuli", Count: 1},
	"minecraft:deepslate_lapis_ore":   {Item: "minecraft:lapis_lazuli", Count: 1},
	"minecraft:redstone_ore":          {Item: "minecraft:redstone", Count: 1},
	"minecraft:deepslate_redstone_ore": {
		Item:  "minecraft:redstone",
		Count: 1,
	},
	"minecraft:nether_quartz_ore": {Item: "minecraft:quartz", Count: 1},
	// 原版魟鱼块掉落 3–7 片（随机），此处固定为中值 5。
	"minecraft:melon":      {Item: "minecraft:melon_slice", Count: 5},
	"minecraft:snow_block": {Item: "minecraft:snowball", Count: 4},
}

// blockDropsNothing 报告方块破坏后（无精准采集）是否不掉落任何物品。
func blockDropsNothing(name string) bool {
	switch name {
	case "minecraft:water", "minecraft:lava", "minecraft:bedrock",
		"minecraft:ice", "minecraft:packed_ice", "minecraft:blue_ice",
		"minecraft:frosted_ice", "minecraft:spawner":
		return true
	}
	// 玻璃与玻璃板、以及所有树叶需要精准采集才会掉落方块本身。
	return strings.HasSuffix(name, "_glass") || name == "minecraft:glass" ||
		strings.HasSuffix(name, "_glass_pane") || name == "minecraft:glass_pane" ||
		strings.HasSuffix(name, "_leaves")
}

// resolveBlockNames 构建方块状态 ID → 命名空间名的反查表（各方块的默认状态）。
// 掉落判定需要“状态 → 名字”，注册表只提供正向表。
func resolveBlockNames() map[uint16]string {
	names := make(map[uint16]string, len(registry.BlockStateIDs))
	for name, state := range registry.BlockStateIDs {
		names[state] = name
	}
	return names
}

// blockDrop 返回破坏方块后的掉落物（命名空间 ID 与数量）；
// 无掉落时返回空字符串与 0。
func (s *Server) blockDrop(state uint16) (string, int32) {
	name, ok := s.blockNames[state]
	if !ok {
		return "", 0
	}
	if spec, ok := blockDropOverrides[name]; ok {
		if spec.Item == "" || spec.Count <= 0 {
			return "", 0
		}
		return spec.Item, spec.Count
	}
	if blockDropsNothing(name) {
		return "", 0
	}
	if _, err := registry.ItemID(name); err != nil {
		return "", 0 // 没有对应物品的方块（如墙上的告示牌）不掉落
	}
	return name, 1
}

// dropBlockItem 在方块中心生成挖掘掉落物（仅生存模式调用）。
// 掉落物带原版风格的小幅随机水平速度与 0.2 的向上速度。
func (s *Server) dropBlockItem(state uint16, x, y, z int) {
	name, count := s.blockDrop(state)
	if count <= 0 {
		return
	}
	stack, err := item.FromName(name, count)
	if err != nil {
		slog.Warn("无法生成方块掉落物", "item", name, "error", err)
		return
	}
	vx := (float64(s.nextRandom()%1000)/1000 - 0.5) * 0.2
	vz := (float64(s.nextRandom()%1000)/1000 - 0.5) * 0.2
	s.spawnItem(stack, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, vx, 0.2, vz, itemPickupDelayTicks)
}
