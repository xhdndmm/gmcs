//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"log/slog"
	"strings"

	"gmcs/internal/item"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 方块破坏掉落表。
//
// 掉落语义对照 1.21.11 服务端的方块战利品表（data/minecraft/loot_table/blocks）：
//   - 默认掉落与方块同名的物品（若物品注册表中存在）；
//   - 精准采集：玻璃/冰/树叶/草方块/石头/矿石/西瓜/黏土/雪块等掉落方块本身；
//     刷怪笼/试炼刷怪笼/宝库/霜冰/紫水晶母岩/强化深板岩无论附魔如何都不掉落；
//   - 时运：矿石按 ore_drops（随机倍数）、红石/西瓜按 uniform_bonus_count、
//     砂砾的燧石概率按 table_bonus、树叶的树苗/木棍概率按 table_bonus；
//   - 剪刀采集树叶掉落树叶本身；
//   - 作物按方块状态 age 判定成熟度（小麦/甜菜根/胡萝卜/马铃薯/下界疣），
//     时运按原版 apply_bonus（binomial_with_bonus_count / uniform_bonus_count）；
//   - 少数与原版一致的“不同名”特例见 blockDropOverrides；
//   - 无工具要求：挖掘系统已按“正确工具”门控掉落（miningHasCorrectToolForDrops）。
//
// 尚未覆盖（docs/TODO.md）：甘蔗/竹笋等需剪刀或者非 age 属性的特殊表、
// 爆炸破坏（gmcs 的爆炸不产生掉落）、生物头颅等极少见掉落。

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
}

// oreDrops 是使用 ore_drops 公式（随机 1..等级+1 倍）的原版掉落。
var oreDrops = map[string]string{
	"minecraft:coal_ore":              "minecraft:coal",
	"minecraft:deepslate_coal_ore":    "minecraft:coal",
	"minecraft:iron_ore":              "minecraft:raw_iron",
	"minecraft:deepslate_iron_ore":    "minecraft:raw_iron",
	"minecraft:copper_ore":            "minecraft:raw_copper",
	"minecraft:deepslate_copper_ore":  "minecraft:raw_copper",
	"minecraft:gold_ore":              "minecraft:raw_gold",
	"minecraft:deepslate_gold_ore":    "minecraft:raw_gold",
	"minecraft:diamond_ore":           "minecraft:diamond",
	"minecraft:deepslate_diamond_ore": "minecraft:diamond",
	"minecraft:emerald_ore":           "minecraft:emerald",
	"minecraft:deepslate_emerald_ore": "minecraft:emerald",
	"minecraft:nether_quartz_ore":     "minecraft:quartz",
}

// blocksNeverDropping 是无论是否精准采集都不掉落的方块。
func blocksNeverDropping(name string) bool {
	switch name {
	case "minecraft:water", "minecraft:lava", "minecraft:bedrock",
		"minecraft:spawner", "minecraft:trial_spawner", "minecraft:vault",
		"minecraft:frosted_ice", "minecraft:budding_amethyst",
		"minecraft:reinforced_deepslate":
		return true
	}
	return false
}

// blockNameOf 返回方块状态所属方块的命名空间名（全部状态，不只默认状态）。
func (s *Server) blockNameOf(state uint16) (string, bool) {
	return registry.BlockNameByState(state)
}

// isShearsItem 报告物品是否为剪刀。
func isShearsItem(itemID int32) bool {
	name, ok := registry.ItemName(itemID)
	return ok && name == "minecraft:shears"
}

// randIntBetween 返回 [min, max] 的均匀随机整数（调用方保证 max >= min）。
func (s *Server) randIntBetween(minValue, maxValue int32) int32 {
	if maxValue <= minValue {
		return minValue
	}
	return minValue + int32(s.nextRandom()%uint64(maxValue-minValue+1))
}

// oreDropsMultiplier 复刻原版 OreDrops：nextInt(等级+2)-1 后取 max(0)，再 +1。
func (s *Server) oreDropsMultiplier(fortune int32) int32 {
	value := int32(s.nextRandom()%uint64(fortune+2)) - 1
	if value < 0 {
		value = 0
	}
	return value + 1
}

// uniformBonus 复刻原版 UniformBonusCount：额外 0..等级 个。
func (s *Server) uniformBonus(fortune int32) int32 {
	return s.uniformBonusMultiplier(fortune, 1)
}

// uniformBonusMultiplier 复刻原版 UniformBonusCount 的 bonusMultiplier 参数。
func (s *Server) uniformBonusMultiplier(fortune, multiplier int32) int32 {
	if fortune <= 0 {
		return 0
	}
	return int32(s.nextRandom()%uint64(fortune+1)) * multiplier
}

// binomialBonus 复刻原版 BinomialWithBonusCount：在 base 上追加
// Binomial(extra+时运等级, probability) 次成功（每次成功 +1）。
func (s *Server) binomialBonus(base, extra, fortune int32, probability float64) int32 {
	count := base
	threshold := uint64(probability * 1_000_000)
	trials := extra + fortune
	for i := int32(0); i < trials; i++ {
		if s.nextRandom()%1_000_000 < threshold {
			count++
		}
	}
	return count
}

// cropDrops 返回作物的掉落（对照 1.21.11 官方 loot_table/blocks 逐条移植）。
// handled=false 表示该方块不是本函数处理的作物。
// 注意：原版作物表没有精准采集条件，因此精准采集不影响作物掉落。
func (s *Server) cropDrops(name string, state uint16, fortune int32) ([]blockDropSpec, bool) {
	switch name {
	case "minecraft:wheat", "minecraft:beetroots", "minecraft:carrots",
		"minecraft:potatoes", "minecraft:nether_wart", "minecraft:sweet_berry_bush",
		"minecraft:torchflower_crop", "minecraft:cocoa", "minecraft:pitcher_crop",
		"minecraft:melon_stem", "minecraft:pumpkin_stem":
	default:
		return nil, false
	}
	age, ok := registry.BlockStateAge(state)
	if !ok {
		return nil, false
	}
	switch name {
	case "minecraft:wheat":
		if age >= 7 {
			return []blockDropSpec{
				{Item: "minecraft:wheat", Count: 1},
				{Item: "minecraft:wheat_seeds", Count: s.binomialBonus(1, 3, fortune, 0.5714286)},
			}, true
		}
		return []blockDropSpec{{Item: "minecraft:wheat_seeds", Count: 1}}, true
	case "minecraft:beetroots":
		if age >= 3 {
			return []blockDropSpec{
				{Item: "minecraft:beetroot", Count: 1},
				{Item: "minecraft:beetroot_seeds", Count: s.binomialBonus(1, 3, fortune, 0.5714286)},
			}, true
		}
		return []blockDropSpec{{Item: "minecraft:beetroot_seeds", Count: 1}}, true
	case "minecraft:carrots":
		count := int32(1)
		if age >= 7 {
			count = s.binomialBonus(1, 3, fortune, 0.5714286)
		}
		return []blockDropSpec{{Item: "minecraft:carrot", Count: count}}, true
	case "minecraft:potatoes":
		count := int32(1)
		drops := []blockDropSpec{{Item: "minecraft:potato", Count: count}}
		if age >= 7 {
			drops[0].Count = s.binomialBonus(1, 3, fortune, 0.5714286)
			// 成熟马铃薯额外 2% 概率掉一个毒马铃薯。
			if s.nextRandom()%1000 < 20 {
				drops = append(drops, blockDropSpec{Item: "minecraft:poisonous_potato", Count: 1})
			}
		}
		return drops, true
	case "minecraft:sweet_berry_bush":
		// age 3：2–3 + uniform(0..时运)；age 2：1–2 + uniform；更低无掉落。
		switch age {
		case 3:
			return []blockDropSpec{{Item: "minecraft:sweet_berries",
				Count: s.randIntBetween(2, 3) + s.uniformBonus(fortune)}}, true
		case 2:
			return []blockDropSpec{{Item: "minecraft:sweet_berries",
				Count: s.randIntBetween(1, 2) + s.uniformBonus(fortune)}}, true
		default:
			return nil, true
		}
	case "minecraft:torchflower_crop":
		// 成熟与否都只掉 1 个火把花种子（官方表无 age 条件）。
		return []blockDropSpec{{Item: "minecraft:torchflower_seeds", Count: 1}}, true
	case "minecraft:cocoa":
		// age 2 掉 3 个可可豆，其余掉 1 个。
		count := int32(1)
		if age >= 2 {
			count = 3
		}
		return []blockDropSpec{{Item: "minecraft:cocoa_beans", Count: count}}, true
	case "minecraft:pitcher_crop":
		// 破坏任一半都按该年龄的下半格掉落（瓶子上半格由移除逻辑清理）。
		itemName := "minecraft:pitcher_pod"
		if age >= 4 {
			itemName = "minecraft:pitcher_plant"
		}
		return []blockDropSpec{{Item: itemName, Count: 1}}, true
	case "minecraft:melon_stem", "minecraft:pumpkin_stem":
		// 官方表：Bin(3, (age+1)/15) 个种子（无时运加成）。
		seedItem := "minecraft:melon_seeds"
		if name == "minecraft:pumpkin_stem" {
			seedItem = "minecraft:pumpkin_seeds"
		}
		count := s.binomialBonus(0, 3, 0, float64(age+1)/15)
		if count <= 0 {
			return nil, true
		}
		return []blockDropSpec{{Item: seedItem, Count: count}}, true
	default: // minecraft:nether_wart
		if age >= 3 {
			count := s.randIntBetween(2, 4) + s.uniformBonus(fortune)
			return []blockDropSpec{{Item: "minecraft:nether_wart", Count: count}}, true
		}
		return []blockDropSpec{{Item: "minecraft:nether_wart", Count: 1}}, true
	}
}

// wallVariantItem 返回墙式方块对应的物品名（原版战利品表的自映射）。
func wallVariantItem(name string) string {
	switch {
	case strings.HasSuffix(name, "_wall_hanging_sign"):
		return strings.TrimSuffix(name, "_wall_hanging_sign") + "_hanging_sign"
	case strings.HasSuffix(name, "_wall_sign"):
		return strings.TrimSuffix(name, "_wall_sign") + "_sign"
	case strings.HasSuffix(name, "_wall_banner"):
		return strings.TrimSuffix(name, "_wall_banner") + "_banner"
	case name == "minecraft:wall_torch":
		return "minecraft:torch"
	case name == "minecraft:soul_wall_torch":
		return "minecraft:soul_torch"
	case name == "minecraft:redstone_wall_torch":
		return "minecraft:redstone_torch"
	}
	return ""
}

// tableBonus 复刻原版 TableBonus：按附魔等级在 chance 表中取值后掷骰
// （chance 单位为万分之一）。
func (s *Server) tableBonus(fortune int32, chances []uint32) bool {
	if len(chances) == 0 {
		return false
	}
	index := fortune
	if index < 0 {
		index = 0
	}
	if int(index) >= len(chances) {
		index = int32(len(chances) - 1)
	}
	return s.nextRandom()%10000 < uint64(chances[index])
}

// leavesSapling 返回树叶对应的树苗（无法映射时返回空）。
func leavesSapling(name string) string {
	switch name {
	case "minecraft:azalea_leaves", "minecraft:flowering_azalea_leaves":
		return "minecraft:azalea"
	case "minecraft:mangrove_leaves":
		return "minecraft:mangrove_propagule"
	}
	base, ok := strings.CutSuffix(name, "_leaves")
	if !ok {
		return ""
	}
	return base + "_sapling"
}

// doublePlants 是掉落只由下半格结算的双格植物。
var doublePlants = map[string]bool{"minecraft:tall_grass": true,
	"minecraft:large_fern":    true,
	"minecraft:tall_seagrass": true,
	"minecraft:sunflower":     true,
	"minecraft:lilac":         true,
	"minecraft:rose_bush":     true,
	"minecraft:peony":         true,
	"minecraft:pitcher_plant": true,
}

// removeDoublePlantPair 在破坏双格植物的一格后移除另一半（不掉落）。
// 原版由 DoublePlantBlock 的邻居更新完成；gmcs 的方块更新模型不处理
// “另一半缺失导致本格消失”，因此显式移除，避免留下悬空的半格。
func (s *Server) removeDoublePlantPair(w *world.World, dim world.Dimension, x, y, z int, state uint16) {
	name, ok := s.blockNameOf(state)
	if !ok {
		return
	}
	var upper, known bool
	switch {
	case doublePlants[name]:
		upper, known = registry.BlockStateHalfUpper(state)
	case name == "minecraft:pitcher_crop":
		upper, known = registry.BlockStateCropUpperHalf(state)
	}
	if !known {
		return
	}
	dy := 1
	if upper {
		dy = -1
	}
	ny := y + dy
	other := w.BlockAt(x, ny, z)
	if otherName, ok := s.blockNameOf(other); !ok || otherName != name {
		return
	}
	var otherUpper bool
	switch {
	case doublePlants[name]:
		otherUpper, known = registry.BlockStateHalfUpper(other)
	case name == "minecraft:pitcher_crop":
		otherUpper, known = registry.BlockStateCropUpperHalf(other)
	}
	if !known || otherUpper == upper {
		return
	}
	if w.SetBlock(x, ny, z, world.AirBlock) {
		s.broadcastBlockUpdate(dim, x, ny, z, int32(world.AirBlock))
	}
}

// blockDrops 返回破坏方块后的全部掉落物。held 用于精准采集/时运/剪刀判定。
func (s *Server) blockDrops(state uint16, held item.Stack) []blockDropSpec {
	name, ok := s.blockNameOf(state)
	if !ok {
		return nil
	}
	// 原版双格植物：破坏任一半都会移除另一半，掉落由“下半格”的战利品表
	// 结算一次（例如破坏向日葵上半格时，下半格因邻居更新被移除并掉花）。
	// 因此上半格改用下半格状态继续结算，避免双倍/零掉落。
	if doublePlants[name] {
		r, ok := registry.BlockStateRangeOf(state)
		if !ok || r.End-r.Start != 1 {
			return nil
		}
		if upper, ok := registry.BlockStateHalfUpper(state); ok && upper {
			state = r.Start + 1 // lower
		}
	}
	if blocksNeverDropping(name) {
		return nil
	}
	silk := hasSilkTouch(held) > 0
	fortune := fortuneLevel(held)
	shears := isShearsItem(held.ItemID)

	// 作物：按方块状态 age 判定成熟度（必须在精准采集分支之前：
	// 原版作物战利品表没有精准采集条件）。
	if drops, handled := s.cropDrops(name, state, fortune); handled {
		return drops
	}
	// 海泡菜：掉落数量等于 pickles 属性（1–4），不受精准采集/时运影响。
	if name == "minecraft:sea_pickle" {
		if count, ok := registry.BlockStatePickleCount(state); ok {
			return []blockDropSpec{{Item: name, Count: int32(count)}}
		}
	}

	// 树叶：剪刀或精准采集掉树叶本身；否则按原版概率表掉树苗/木棍/苹果。
	if strings.HasSuffix(name, "_leaves") {
		if silk || shears {
			return []blockDropSpec{{Item: name, Count: 1}}
		}
		var drops []blockDropSpec
		if sapling := leavesSapling(name); sapling != "" {
			// 原版 table_bonus：[0.05, 0.0625, 1/12, 0.1]
			if s.tableBonus(fortune, []uint32{500, 625, 833, 1000}) {
				drops = append(drops, blockDropSpec{Item: sapling, Count: 1})
			}
		}
		// 木棍：[0.02, 0.0222, 0.025, 0.0333, 0.1]
		if s.tableBonus(fortune, []uint32{200, 222, 250, 333, 1000}) {
			drops = append(drops, blockDropSpec{Item: "minecraft:stick", Count: s.randIntBetween(1, 2)})
		}
		// 苹果：仅橡木/深色橡木，固定 0.5%。
		if name == "minecraft:oak_leaves" || name == "minecraft:dark_oak_leaves" {
			if s.nextRandom()%1000 < 5 {
				drops = append(drops, blockDropSpec{Item: "minecraft:apple", Count: 1})
			}
		}
		return drops
	}

	// 草/蕨类/藤蔓等：只有剪刀能采集（精准采集对这些方块无效）；
	// 否则按原版概率表掉落种子/木棍。
	switch name {
	case "minecraft:short_grass", "minecraft:fern":
		if shears {
			return []blockDropSpec{{Item: name, Count: 1}}
		}
		if s.nextRandom()%1000 < 125 {
			return []blockDropSpec{{Item: "minecraft:wheat_seeds", Count: 1 + s.uniformBonusMultiplier(fortune, 2)}}
		}
		return nil
	case "minecraft:tall_grass", "minecraft:large_fern":
		if shears {
			return []blockDropSpec{{Item: "minecraft:short_grass", Count: 2}}
		}
		if s.nextRandom()%1000 < 125 {
			return []blockDropSpec{{Item: "minecraft:wheat_seeds", Count: 1 + s.uniformBonusMultiplier(fortune, 2)}}
		}
		return nil
	case "minecraft:dead_bush":
		if shears {
			return []blockDropSpec{{Item: name, Count: 1}}
		}
		if count := s.randIntBetween(0, 2); count > 0 {
			return []blockDropSpec{{Item: "minecraft:stick", Count: count}}
		}
		return nil
	case "minecraft:vine", "minecraft:glow_lichen", "minecraft:hanging_roots",
		"minecraft:seagrass", "minecraft:tall_seagrass":
		if shears {
			return []blockDropSpec{{Item: name, Count: 1}}
		}
		return nil
	}

	if silk {
		// 原版可被精准采集保留的方块：掉落方块本身（存在对应物品时）。
		itemName := name
		if wallItem := wallVariantItem(name); wallItem != "" {
			itemName = wallItem
		}
		if _, err := registry.ItemID(itemName); err == nil {
			return []blockDropSpec{{Item: itemName, Count: 1}}
		}
		return nil
	}

	// 玻璃/冰类：只有精准采集才掉落。
	if name == "minecraft:glass" || strings.HasSuffix(name, "_glass") ||
		name == "minecraft:glass_pane" || strings.HasSuffix(name, "_glass_pane") ||
		name == "minecraft:ice" || name == "minecraft:packed_ice" || name == "minecraft:blue_ice" {
		return nil
	}

	// 砂砾：原版 10% 燧石，时运概率 [0.1, 0.1429, 0.25, 1.0]。
	if name == "minecraft:gravel" {
		if s.tableBonus(fortune, []uint32{1000, 1429, 2500, 10000}) {
			return []blockDropSpec{{Item: "minecraft:flint", Count: 1}}
		}
		return []blockDropSpec{{Item: "minecraft:gravel", Count: 1}}
	}

	// 红石：4–5 + uniform(0..等级)。
	if name == "minecraft:redstone_ore" || name == "minecraft:deepslate_redstone_ore" {
		return []blockDropSpec{{Item: "minecraft:redstone", Count: s.randIntBetween(4, 5) + s.uniformBonus(fortune)}}
	}

	// 铜矿：2–5 个粗铜，再乘 ore_drops 倍数。
	if name == "minecraft:copper_ore" || name == "minecraft:deepslate_copper_ore" {
		count := s.randIntBetween(2, 5) * s.oreDropsMultiplier(fortune)
		return []blockDropSpec{{Item: "minecraft:raw_copper", Count: count}}
	}

	// 下界金矿：2–6 个金粒，再乘 ore_drops 倍数。
	if name == "minecraft:nether_gold_ore" {
		count := s.randIntBetween(2, 6) * s.oreDropsMultiplier(fortune)
		return []blockDropSpec{{Item: "minecraft:gold_nugget", Count: count}}
	}

	// 青金石：4–8 个，再乘 ore_drops 倍数。
	if name == "minecraft:lapis_ore" || name == "minecraft:deepslate_lapis_ore" {
		count := s.randIntBetween(4, 8) * s.oreDropsMultiplier(fortune)
		return []blockDropSpec{{Item: "minecraft:lapis_lazuli", Count: count}}
	}

	// 西瓜：3–7 + uniform(0..等级)，上限 9。
	if name == "minecraft:melon" {
		count := s.randIntBetween(3, 7) + s.uniformBonus(fortune)
		return []blockDropSpec{{Item: "minecraft:melon_slice", Count: min(count, 9)}}
	}

	// 雪块：4 个雪球（时运不影响）。
	if name == "minecraft:snow_block" {
		return []blockDropSpec{{Item: "minecraft:snowball", Count: 4}}
	}

	// 矿石：ore_drops（base 1）。
	if drop, ok := oreDrops[name]; ok {
		return []blockDropSpec{{Item: drop, Count: s.oreDropsMultiplier(fortune)}}
	}

	// 黏土/雪块：固定数量。
	if spec, ok := blockDropOverrides[name]; ok {
		return []blockDropSpec{spec}
	}
	// 墙式告示牌/旗帜/火把掉落对应的站立变体。
	itemName := name
	if wallItem := wallVariantItem(name); wallItem != "" {
		itemName = wallItem
	}
	if _, err := registry.ItemID(itemName); err != nil {
		return nil // 没有对应物品的方块（如墙上的告示牌）不掉落
	}
	return []blockDropSpec{{Item: itemName, Count: 1}}
}

// blockDrop 返回主掉落（兼容旧测试与调用方）：无掉落时返回空字符串与 0。
func (s *Server) blockDrop(state uint16) (string, int32) {
	drops := s.blockDrops(state, item.Empty())
	if len(drops) == 0 {
		return "", 0
	}
	return drops[0].Item, drops[0].Count
}

// dropBlockItem 在方块中心生成挖掘掉落物（仅生存模式调用）。
// 掉落物带原版风格的小幅随机水平速度与 0.2 的向上速度。
func (s *Server) dropBlockItem(dim world.Dimension, state uint16, x, y, z int, held item.Stack) {
	for _, spec := range s.blockDrops(state, held) {
		if spec.Item == "" || spec.Count <= 0 {
			continue
		}
		stack, err := item.FromName(spec.Item, spec.Count)
		if err != nil {
			slog.Warn("无法生成方块掉落物", "item", spec.Item, "error", err)
			continue
		}
		vx := (float64(s.nextRandom()%1000)/1000 - 0.5) * 0.2
		vz := (float64(s.nextRandom()%1000)/1000 - 0.5) * 0.2
		s.spawnItem(dim, stack, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, vx, 0.2, vz, itemPickupDelayMining)
	}
}
