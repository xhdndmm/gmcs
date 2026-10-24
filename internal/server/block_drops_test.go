//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// TestBlockDropsCrops 验证作物掉落按官方 loot_table 的成熟度（age）与时运公式：
// 小麦/甜菜根未成熟只掉种子；胡萝卜/马铃薯未成熟掉 1 个自身；
// 成熟时按 binomial_with_bonus_count（extra=3, p=0.5714286）追加；
// 下界疣成熟时 2–4 + uniform(0..时运)。
func TestBlockDropsCrops(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer instance.testWorld().Close()

	state := func(name string, age int) uint16 {
		t.Helper()
		base, err := registry.BlockStateID(name)
		if err != nil {
			t.Fatalf("未知方块 %s：%v", name, err)
		}
		id := base + uint16(age)
		if got, ok := registry.BlockStateAge(id); !ok || got != age {
			t.Fatalf("%s 的 age=%d 状态推导失败：got=%d ok=%v", name, age, got, ok)
		}
		return id
	}
	find := func(drops []blockDropSpec, itemName string) (int32, bool) {
		for _, drop := range drops {
			if drop.Item == itemName {
				return drop.Count, true
			}
		}
		return 0, false
	}

	// 未成熟小麦：仅 1 个种子。
	if drops := instance.blockDrops(state("minecraft:wheat", 6), item.Empty()); len(drops) != 1 ||
		drops[0].Item != "minecraft:wheat_seeds" || drops[0].Count != 1 {
		t.Fatalf("未成熟小麦 = %+v，want 1 种子", drops)
	}
	// 成熟小麦：1 小麦 + 1..4 种子（时运 0：1 + Binomial(3)）。
	for i := 0; i < 32; i++ {
		drops := instance.blockDrops(state("minecraft:wheat", 7), item.Empty())
		if count, ok := find(drops, "minecraft:wheat"); !ok || count != 1 {
			t.Fatalf("成熟小麦 = %+v，want 1 小麦", drops)
		}
		if count, ok := find(drops, "minecraft:wheat_seeds"); !ok || count < 1 || count > 4 {
			t.Fatalf("成熟小麦种子 = %d，want 1..4", count)
		}
	}
	// 成熟胡萝卜：1..4（未成熟固定 1）。
	for i := 0; i < 32; i++ {
		drops := instance.blockDrops(state("minecraft:carrots", 7), item.Empty())
		if len(drops) != 1 || drops[0].Item != "minecraft:carrot" || drops[0].Count < 1 || drops[0].Count > 4 {
			t.Fatalf("成熟胡萝卜 = %+v，want 胡萝卜 1..4", drops)
		}
	}
	if drops := instance.blockDrops(state("minecraft:carrots", 0), item.Empty()); len(drops) != 1 || drops[0].Count != 1 {
		t.Fatalf("未成熟胡萝卜 = %+v，want 1 胡萝卜", drops)
	}
	// 成熟马铃薯：1..4 马铃薯，毒马铃薯最多 1 个。
	for i := 0; i < 32; i++ {
		drops := instance.blockDrops(state("minecraft:potatoes", 7), item.Empty())
		if count, ok := find(drops, "minecraft:potato"); !ok || count < 1 || count > 4 {
			t.Fatalf("成熟马铃薯 = %+v，want 马铃薯 1..4", drops)
		}
		if count, ok := find(drops, "minecraft:poisonous_potato"); ok && count != 1 {
			t.Fatalf("毒马铃薯数量 = %d，want 1", count)
		}
	}
	// 成熟甜菜根：1 甜菜根 + 1..4 种子；未成熟仅 1 种子。
	drops := instance.blockDrops(state("minecraft:beetroots", 3), item.Empty())
	if count, ok := find(drops, "minecraft:beetroot"); !ok || count != 1 {
		t.Fatalf("成熟甜菜根 = %+v，want 1 甜菜根", drops)
	}
	if count, ok := find(drops, "minecraft:beetroot_seeds"); !ok || count < 1 || count > 4 {
		t.Fatalf("成熟甜菜根种子 = %d，want 1..4", count)
	}
	if drops := instance.blockDrops(state("minecraft:beetroots", 2), item.Empty()); len(drops) != 1 ||
		drops[0].Item != "minecraft:beetroot_seeds" || drops[0].Count != 1 {
		t.Fatalf("未成熟甜菜根 = %+v，want 1 种子", drops)
	}
	// 下界疣：成熟 2..4，未成熟 1。
	for i := 0; i < 32; i++ {
		drops := instance.blockDrops(state("minecraft:nether_wart", 3), item.Empty())
		if len(drops) != 1 || drops[0].Item != "minecraft:nether_wart" || drops[0].Count < 2 || drops[0].Count > 4 {
			t.Fatalf("成熟下界疣 = %+v，want 2..4", drops)
		}
	}
	if drops := instance.blockDrops(state("minecraft:nether_wart", 1), item.Empty()); len(drops) != 1 || drops[0].Count != 1 {
		t.Fatalf("未成熟下界疣 = %+v，want 1", drops)
	}
	// 时运 3 提高种子数量上限（1 + Binomial(6) = 1..7），且精准采集不改变作物掉落。
	fortuneID, ok := registry.EnchantmentID("minecraft:fortune")
	if !ok {
		t.Fatal("缺少时运附魔")
	}
	silkID, ok := registry.EnchantmentID("minecraft:silk_touch")
	if !ok {
		t.Fatal("缺少精准采集附魔")
	}
	fortuneHeld := item.Stack{ItemID: mustItemID(t, "minecraft:diamond_hoe"), Count: 1,
		Enchantments: []item.EnchantmentEntry{{ID: fortuneID, Level: 3}}}
	silkHeld := item.Stack{ItemID: mustItemID(t, "minecraft:diamond_hoe"), Count: 1,
		Enchantments: []item.EnchantmentEntry{{ID: silkID, Level: 1}}}
	for i := 0; i < 32; i++ {
		drops = instance.blockDrops(state("minecraft:wheat", 7), fortuneHeld)
		if count, ok := find(drops, "minecraft:wheat_seeds"); !ok || count < 1 || count > 7 {
			t.Fatalf("时运 3 小麦种子 = %d，want 1..7", count)
		}
	}
	drops = instance.blockDrops(state("minecraft:wheat", 7), silkHeld)
	if count, ok := find(drops, "minecraft:wheat"); !ok || count != 1 {
		t.Fatalf("精准采集小麦 = %+v，want 仍为 1 小麦", drops)
	}

	// 甜浆果丛：age 3 → 2–3，age 2 → 1–2，更低无掉落。
	for i := 0; i < 32; i++ {
		drops = instance.blockDrops(state("minecraft:sweet_berry_bush", 3), item.Empty())
		if len(drops) != 1 || drops[0].Item != "minecraft:sweet_berries" || drops[0].Count < 2 || drops[0].Count > 3 {
			t.Fatalf("成熟甜浆果丛 = %+v，want 2..3", drops)
		}
	}
	for i := 0; i < 32; i++ {
		drops = instance.blockDrops(state("minecraft:sweet_berry_bush", 2), item.Empty())
		if len(drops) != 1 || drops[0].Count < 1 || drops[0].Count > 2 {
			t.Fatalf("半成熟甜浆果丛 = %+v，want 1..2", drops)
		}
	}
	if drops := instance.blockDrops(state("minecraft:sweet_berry_bush", 1), item.Empty()); len(drops) != 0 {
		t.Fatalf("未成熟甜浆果丛 = %+v，want 无掉落", drops)
	}
	// 火把花作物：任何年龄都只掉 1 个种子（age 只有 0–1 两个状态）。
	for _, age := range []int{0, 1} {
		drops = instance.blockDrops(state("minecraft:torchflower_crop", age), item.Empty())
		if len(drops) != 1 || drops[0].Item != "minecraft:torchflower_seeds" || drops[0].Count != 1 {
			t.Fatalf("火把花 age=%d = %+v，want 1 种子", age, drops)
		}
	}
}

// TestBlockDropTable 校验简化掉落表：同名默认、不同名特例、无掉落方块。
func TestBlockDropTable(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer instance.testWorld().Close()

	blockState := func(name string) uint16 {
		state, err := registry.BlockStateID(name)
		if err != nil {
			t.Fatalf("未知方块 %s：%v", name, err)
		}
		return state
	}

	cases := []struct {
		block    string
		item     string
		minCount int32
		maxCount int32
	}{
		{"minecraft:stone", "minecraft:cobblestone", 1, 1},
		{"minecraft:cobblestone", "minecraft:cobblestone", 1, 1},
		{"minecraft:grass_block", "minecraft:dirt", 1, 1},
		{"minecraft:dirt", "minecraft:dirt", 1, 1},
		{"minecraft:sand", "minecraft:sand", 1, 1},
		{"minecraft:oak_log", "minecraft:oak_log", 1, 1},
		{"minecraft:clay", "minecraft:clay_ball", 4, 4},
		{"minecraft:snow_block", "minecraft:snowball", 4, 4},
		{"minecraft:melon", "minecraft:melon_slice", 3, 7},
		{"minecraft:coal_ore", "minecraft:coal", 1, 1},
		{"minecraft:deepslate_coal_ore", "minecraft:coal", 1, 1},
		{"minecraft:iron_ore", "minecraft:raw_iron", 1, 1},
		{"minecraft:deepslate_iron_ore", "minecraft:raw_iron", 1, 1},
		{"minecraft:copper_ore", "minecraft:raw_copper", 2, 5},
		{"minecraft:gold_ore", "minecraft:raw_gold", 1, 1},
		{"minecraft:nether_gold_ore", "minecraft:gold_nugget", 2, 6},
		{"minecraft:diamond_ore", "minecraft:diamond", 1, 1},
		{"minecraft:emerald_ore", "minecraft:emerald", 1, 1},
		{"minecraft:lapis_ore", "minecraft:lapis_lazuli", 4, 8},
		{"minecraft:redstone_ore", "minecraft:redstone", 4, 5},
		{"minecraft:nether_quartz_ore", "minecraft:quartz", 1, 1},
		{"minecraft:glass", "", 0, 0},
		{"minecraft:white_stained_glass", "", 0, 0},
		{"minecraft:glass_pane", "", 0, 0},
		{"minecraft:ice", "", 0, 0},
		{"minecraft:water", "", 0, 0},
		{"minecraft:bedrock", "", 0, 0},
		// 墙式告示牌掉落对应的站立告示牌。
		{"minecraft:oak_wall_sign", "minecraft:oak_sign", 1, 1},
	}
	for _, tc := range cases {
		item, count := instance.blockDrop(blockState(tc.block))
		if item != tc.item || count < tc.minCount || count > tc.maxCount {
			t.Errorf("blockDrop(%s) = (%q, %d)，want (%q, %d..%d)",
				tc.block, item, count, tc.item, tc.minCount, tc.maxCount)
		}
	}

	// 未知状态 ID 不掉落。
	if item, count := instance.blockDrop(0xFFFF); item != "" || count != 0 {
		t.Errorf("blockDrop(未知状态) = (%q, %d)，want 无掉落", item, count)
	}
	// 所有特例条目都必须真实存在于物品注册表（防止表里写错名字）。
	for block, spec := range blockDropOverrides {
		if _, err := registry.ItemID(spec.Item); err != nil {
			t.Errorf("特例 %s 的掉落物 %s 不在物品注册表中", block, spec.Item)
		}
	}
}

// TestBlockDropsSilkTouch 验证精准采集掉落方块本身。
func TestBlockDropsSilkTouch(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer instance.testWorld().Close()

	silkID, ok := registry.EnchantmentID("minecraft:silk_touch")
	if !ok {
		t.Fatal("缺少精准采集附魔")
	}
	held := item.Stack{ItemID: mustItemID(t, "minecraft:diamond_pickaxe"), Count: 1,
		Enchantments: []item.EnchantmentEntry{{ID: silkID, Level: 1}}}
	cases := []struct {
		block string
		item  string
	}{
		{"minecraft:stone", "minecraft:stone"},
		{"minecraft:grass_block", "minecraft:grass_block"},
		{"minecraft:glass", "minecraft:glass"},
		{"minecraft:white_stained_glass", "minecraft:white_stained_glass"},
		{"minecraft:ice", "minecraft:ice"},
		{"minecraft:coal_ore", "minecraft:coal_ore"},
		{"minecraft:melon", "minecraft:melon"},
		{"minecraft:clay", "minecraft:clay"},
		{"minecraft:oak_leaves", "minecraft:oak_leaves"},
		{"minecraft:gravel", "minecraft:gravel"},
		{"minecraft:snow_block", "minecraft:snow_block"},
	}
	for _, tc := range cases {
		state, err := registry.BlockStateID(tc.block)
		if err != nil {
			t.Fatalf("未知方块 %s：%v", tc.block, err)
		}
		drops := instance.blockDrops(state, held)
		if len(drops) != 1 || drops[0].Item != tc.item || drops[0].Count != 1 {
			t.Errorf("精准采集 %s = %+v，want %s×1", tc.block, drops, tc.item)
		}
	}
	// 刷怪笼与霜冰即使精准采集也不掉落。
	spawner, err := registry.BlockStateID("minecraft:spawner")
	if err != nil {
		t.Fatal(err)
	}
	if drops := instance.blockDrops(spawner, held); len(drops) != 0 {
		t.Errorf("精准采集刷怪笼 = %+v，want 无掉落", drops)
	}
}

// TestBlockDropsFortune 验证时运掉落公式。
func TestBlockDropsFortune(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer instance.testWorld().Close()

	fortuneID, ok := registry.EnchantmentID("minecraft:fortune")
	if !ok {
		t.Fatal("缺少时运附魔")
	}
	held := item.Stack{ItemID: mustItemID(t, "minecraft:diamond_pickaxe"), Count: 1,
		Enchantments: []item.EnchantmentEntry{{ID: fortuneID, Level: 3}}}
	blockState := func(name string) uint16 {
		state, err := registry.BlockStateID(name)
		if err != nil {
			t.Fatalf("未知方块 %s：%v", name, err)
		}
		return state
	}
	// 矿石：1..4 倍（时运 3 的 ore_drops）。
	for i := 0; i < 32; i++ {
		drops := instance.blockDrops(blockState("minecraft:coal_ore"), held)
		if len(drops) != 1 || drops[0].Item != "minecraft:coal" || drops[0].Count < 1 || drops[0].Count > 4 {
			t.Fatalf("时运煤矿 = %+v，want 煤 1..4", drops)
		}
	}
	// 砂砾：时运 3 的燧石概率为 100%。
	drops := instance.blockDrops(blockState("minecraft:gravel"), held)
	if len(drops) != 1 || drops[0].Item != "minecraft:flint" {
		t.Fatalf("时运 3 砂砾 = %+v，want 燧石", drops)
	}
	// 红石：4–5 + 0..3。
	drops = instance.blockDrops(blockState("minecraft:redstone_ore"), held)
	if len(drops) != 1 || drops[0].Item != "minecraft:redstone" || drops[0].Count < 4 || drops[0].Count > 8 {
		t.Fatalf("时运红石 = %+v，want 红石 4..8", drops)
	}
	// 树叶：剪刀掉落树叶本身。
	shears := item.Stack{ItemID: mustItemID(t, "minecraft:shears"), Count: 1}
	drops = instance.blockDrops(blockState("minecraft:oak_leaves"), shears)
	if len(drops) != 1 || drops[0].Item != "minecraft:oak_leaves" {
		t.Fatalf("剪刀采叶 = %+v，want 橡木树叶", drops)
	}
}

// TestSurvivalBlockBreakDrops 验证生存模式破坏方块会生成掉落物
// （实体 + Item 元数据包），创意模式破坏不掉落。
func TestSurvivalBlockBreakDrops(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	// 空手挖石头不会掉落（原版要求正确工具）；带上木镐覆盖石头情形。
	cfg.StartingItems = []string{"minecraft:wooden_pickaxe"}
	instance, conn := joinServer(t, cfg, "Miner")

	x, y, z := findDigTarget(t, instance)
	state := instance.testWorld().BlockAt(x, y, z)
	wantName := ""
	switch state {
	case world.GrassBlock:
		wantName = "minecraft:dirt"
	case world.SandBlock:
		wantName = "minecraft:sand"
	case world.StoneBlock:
		wantName = "minecraft:cobblestone"
	default:
		t.Skipf("出生点顶层方块 %d 没有断言用的预期掉落", state)
	}
	wantItemID, err := registry.ItemID(wantName)
	if err != nil {
		t.Fatal(err)
	}

	// 生存模式：完整挖掘流程（START → 推进进度 → STOP 完成破坏）。
	breakBlockSurvival(t, conn, instance, findSession(t, instance, "Miner"), x, y, z)
	expectBlockUpdate(t, conn, x, y, z, int32(world.AirBlock))
	expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)
	metadata := expectPlayPacket(t, conn, protocol.PlayPacketIDEntityMetadata)
	// 元数据包字段：包 ID、实体 ID、索引（8 = item）、类型（7 = item_stack）、
	// 槽位数量、物品 ID、组件增量/减量（均为 0）。
	if index := packetField(t, metadata, 3); index != protocol.EntityMetadataItemStackIndex {
		t.Fatalf("元数据索引 = %d，want %d", index, protocol.EntityMetadataItemStackIndex)
	}
	if entryType := packetField(t, metadata, 4); entryType != 7 {
		t.Fatalf("元数据类型 = %d，want 7（item_stack）", entryType)
	}
	if count := packetField(t, metadata, 5); count != 1 {
		t.Fatalf("掉落物数量 = %d，want 1", count)
	}
	if itemID := packetField(t, metadata, 6); itemID != wantItemID {
		t.Fatalf("掉落物物品 ID = %d，want %d（%s）", itemID, wantItemID, wantName)
	}

	instance.entityMu.Lock()
	dropped := make([]*itemEntity, 0, len(instance.items))
	for _, e := range instance.items {
		dropped = append(dropped, e)
	}
	instance.entityMu.Unlock()
	if len(dropped) != 1 || dropped[0].Stack.ItemID != wantItemID || dropped[0].Stack.Count != 1 {
		t.Fatalf("掉落物实体 = %+v，want 1 个 %s", dropped, wantName)
	}

	// 创意模式：破坏不掉落物品。
	findSession(t, instance, "Miner").setGameMode(uint8(config.GameModeCreative))
	x2, y2, z2 := findDigTarget(t, instance)
	sendPlayerAction(t, conn, 0, x2, y2, z2, 1)
	expectBlockUpdate(t, conn, x2, y2, z2, int32(world.AirBlock))
	instance.entityMu.Lock()
	remaining := len(instance.items)
	instance.entityMu.Unlock()
	if remaining != 1 {
		t.Fatalf("创意模式破坏后掉落物数量 = %d，want 1（不应新增）", remaining)
	}
}
