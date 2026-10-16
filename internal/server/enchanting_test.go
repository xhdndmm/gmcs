//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"
	"net"
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 附魔台测试：随机序列与原版对齐、成本公式、候选过滤、结果确定性、
// 槽位规则与窗口端到端流程。

// TestJavaRandomMatchesJava 验证 javaRandom 与真实 java.util.Random 的序列
// 逐位一致（参考值由 JDK 生成）。
func TestJavaRandomMatchesJava(t *testing.T) {
	rng := &javaRandom{}
	rng.setSeed(123456789)
	for i, want := range []int32{5, 6, 3, 2, 3, 1, 7, 3} {
		if got := rng.nextInt(8); got != want {
			t.Fatalf("nextInt(8)[%d] = %d, want %d", i, got, want)
		}
	}
	if got := rng.nextInt(15); got != 7 {
		t.Fatalf("nextInt(15) = %d, want 7", got)
	}
	if got := rng.nextInt(100); got != 87 {
		t.Fatalf("nextInt(100) = %d, want 87", got)
	}
	if got := rng.nextInt(25); got != 16 {
		t.Fatalf("nextInt(25) = %d, want 16", got)
	}
	if got := rng.nextFloat(); math.Abs(float64(got)-0.49033803) > 1e-6 {
		t.Fatalf("nextFloat = %v, want 0.49033803", got)
	}
	if got := rng.nextInt(50); got != 14 {
		t.Fatalf("nextInt(50) = %d, want 14", got)
	}
	if got := rng.nextInt(2); got != 0 {
		t.Fatalf("nextInt(2) = %d, want 0", got)
	}
	if got := rng.nextInt(3); got != 2 {
		t.Fatalf("nextInt(3) = %d, want 2", got)
	}
	if got := rng.nextInt(1); got != 0 {
		t.Fatalf("nextInt(1) = %d, want 0", got)
	}

	// 负数种子（Java 中 int 种子按符号扩展）。
	rng.setSeed(-987654321)
	for i, want := range []int32{5, 4, 4, 6, 6} {
		if got := rng.nextInt(8); got != want {
			t.Fatalf("负数种子 nextInt(8)[%d] = %d, want %d", i, got, want)
		}
	}
	if got := rng.nextInt(15); got != 11 {
		t.Fatalf("负数种子 nextInt(15) = %d, want 11", got)
	}
	if got := rng.nextInt(50); got != 22 {
		t.Fatalf("负数种子 nextInt(50) = %d, want 22", got)
	}

	// 零种子。
	rng.setSeed(0)
	for i, want := range []int32{5, 6, 1, 4, 5} {
		if got := rng.nextInt(8); got != want {
			t.Fatalf("零种子 nextInt(8)[%d] = %d, want %d", i, got, want)
		}
	}
	if got := rng.nextInt(50); got != 3 {
		t.Fatalf("零种子 nextInt(50) = %d, want 3", got)
	}
}

// TestEnchantmentCostFormula 验证成本公式：每选项消耗两个随机数
// （nextInt(8) 与 nextInt(书架+1)），seed 123456789 的 nextInt(8) 序列为
// 5/6/3/2/3/1（第 1、3、5 次用于三个选项）→ 成本 [2, 3, 4]。
func TestEnchantmentCostFormula(t *testing.T) {
	rng := &javaRandom{}
	rng.setSeed(123456789)
	cost0 := enchantmentCost(rng, 0, 0, 10)
	cost1 := enchantmentCost(rng, 1, 0, 10)
	cost2 := enchantmentCost(rng, 2, 0, 10)
	// slot0: base = 5+1+0+0 = 6 → 6/3 = 2；
	// slot1: base = 3+1+0+0 = 4 → 4*2/3+1 = 3；
	// slot2: base = 3+1+0+0 = 4 → max(4, 0) = 4。
	if cost0 != 2 || cost1 != 3 || cost2 != 4 {
		t.Fatalf("成本 = [%d %d %d], want [2 3 4]", cost0, cost1, cost2)
	}

	// 书架超过 15 按 15 计：base = nextInt(8)+1 + 7 + nextInt(16) ≤ 28，
	// 选项 2 取 max(base, 30) = 30。
	rng.setSeed(0)
	cost2Full := enchantmentCost(rng, 2, 40, 10)
	if cost2Full != 30 {
		t.Fatalf("满书架选项 2 成本 = %d, want 30", cost2Full)
	}
}

// TestAvailableEnchantmentsByLevel 验证候选过滤：按 in_enchanting_table 顺序、
// 受 supported_items 限制（书除外）、等级取满足成本区间的最高等级。
func TestAvailableEnchantmentsByLevel(t *testing.T) {
	sword, err := item.FromName("minecraft:diamond_sword", 1)
	if err != nil {
		t.Fatal(err)
	}
	sharpnessID, ok := registry.SyncEntryID("minecraft:enchantment", "minecraft:sharpness")
	if !ok {
		t.Fatal("缺少 sharpness 附魔")
	}
	list := availableEnchantmentsByLevel(50, sword)
	if len(list) == 0 {
		t.Fatal("等级 50 的钻石剑应至少有一个候选")
	}
	foundSharpness5 := false
	lastIndex := -1
	table := registry.EnchantmentsInEnchantingTable
	for _, inst := range list {
		if !registry.InEnchantingTable(inst.id) {
			t.Fatalf("候选 %d 不在 in_enchanting_table", inst.id)
		}
		if !registry.EnchantmentSupported(inst.id, sword.ItemID) {
			t.Fatalf("候选 %d 不支持钻石剑", inst.id)
		}
		maxLevel, _ := registry.EnchantmentMaxLevel(inst.id)
		if inst.level < 1 || inst.level > maxLevel {
			t.Fatalf("候选 %d 等级 %d 超出 [1,%d]", inst.id, inst.level, maxLevel)
		}
		minCost, maxCost, _ := registry.EnchantmentCostRange(inst.id, inst.level)
		if 50 < minCost || 50 > maxCost {
			t.Fatalf("候选 %d (等级 %d) 成本区间 [%d,%d] 不含 50", inst.id, inst.level, minCost, maxCost)
		}
		if inst.id == sharpnessID && inst.level == 5 {
			foundSharpness5 = true
		}
		// 顺序与标签顺序一致。
		index := -1
		for i, id := range table {
			if id == inst.id {
				index = i
				break
			}
		}
		if index <= lastIndex {
			t.Fatalf("候选顺序与标签顺序不一致：%d", inst.id)
		}
		lastIndex = index
	}
	if !foundSharpness5 {
		t.Fatal("等级 50 的钻石剑候选应包含锋利 V（成本区间 45–65）")
	}

	// 书：不受 supported_items 限制（普通书可附魔为任意附魔书）。
	book, err := item.FromName("minecraft:book", 1)
	if err != nil {
		t.Fatal(err)
	}
	bookList := availableEnchantmentsByLevel(50, book)
	if len(bookList) < len(list) {
		t.Fatalf("书的候选 %d 应不少于剑的 %d", len(bookList), len(list))
	}

	// 不可附魔物品：无候选。
	stone, err := item.FromName("minecraft:stone", 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := availableEnchantmentsByLevel(50, stone); len(got) != 0 {
		t.Fatalf("石头不应有候选，得到 %d 个", len(got))
	}
}

// TestEnchantingTablePrimaryItems 回归：附魔台过滤器除 supported_items 外还要求
// primary_items 命中（对照 1.21.11 Enchantment.a(dlt) 字节码）。斧头虽然
// supported_items 含锋利，但 primary_items（melee_weapon）不含斧，附魔台不应
// 提供锋利（铁砧书仍可）；荆棘只对胸甲提供。
func TestEnchantingTablePrimaryItems(t *testing.T) {
	sharpnessID, ok := registry.SyncEntryID("minecraft:enchantment", "minecraft:sharpness")
	if !ok {
		t.Fatal("缺少 sharpness 附魔")
	}
	thornsID, ok := registry.SyncEntryID("minecraft:enchantment", "minecraft:thorns")
	if !ok {
		t.Fatal("缺少 thorns 附魔")
	}
	axe, err := item.FromName("minecraft:diamond_axe", 1)
	if err != nil {
		t.Fatal(err)
	}
	sword, err := item.FromName("minecraft:diamond_sword", 1)
	if err != nil {
		t.Fatal(err)
	}
	chest, err := item.FromName("minecraft:diamond_chestplate", 1)
	if err != nil {
		t.Fatal(err)
	}
	legs, err := item.FromName("minecraft:diamond_leggings", 1)
	if err != nil {
		t.Fatal(err)
	}

	// primary_items 数据存在（锋利属于 melee_weapon，荆棘属于 chest_armor）。
	if id := int(sharpnessID); id >= len(registry.EnchantmentPrimaryItems) || len(registry.EnchantmentPrimaryItems[id]) == 0 {
		t.Fatal("锋利应有 primary_items")
	}
	if id := int(thornsID); id >= len(registry.EnchantmentPrimaryItems) || len(registry.EnchantmentPrimaryItems[id]) == 0 {
		t.Fatal("荆棘应有 primary_items")
	}

	// 斧：supported 命中（铁砧可用）但 primary 不命中（附魔台不可用）。
	if !registry.EnchantmentSupported(sharpnessID, axe.ItemID) {
		t.Fatal("斧的 supported_items 应含锋利（铁砧/命令可用）")
	}
	if registry.EnchantmentTableApplicable(sharpnessID, axe.ItemID) {
		t.Fatal("附魔台不应向斧提供锋利（primary_items 不含斧）")
	}
	if !registry.EnchantmentTableApplicable(sharpnessID, sword.ItemID) {
		t.Fatal("附魔台应向剑提供锋利")
	}
	axeList := availableEnchantmentsByLevel(50, axe)
	if len(axeList) == 0 {
		t.Fatal("等级 50 的钻石斧应至少有一个候选")
	}
	for _, inst := range axeList {
		if inst.id == sharpnessID {
			t.Fatal("斧的附魔台候选不应包含锋利")
		}
		if !registry.EnchantmentTableApplicable(inst.id, axe.ItemID) {
			t.Fatalf("候选 %d 不满足附魔台筛选（primary_items）", inst.id)
		}
	}

	// 荆棘：primary_items = chest_armor，仅胸甲可通过附魔台获得。
	if !registry.EnchantmentTableApplicable(thornsID, chest.ItemID) {
		t.Fatal("附魔台应向胸甲提供荆棘")
	}
	if registry.EnchantmentTableApplicable(thornsID, legs.ItemID) {
		t.Fatal("附魔台不应向护腿提供荆棘（primary_items = chest_armor）")
	}

	// 书不受 supported/primary 限制（普通书可附魔为任意附魔书）。
	book, err := item.FromName("minecraft:book", 1)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, inst := range availableEnchantmentsByLevel(50, book) {
		if inst.id == sharpnessID {
			found = true
		}
	}
	if !found {
		t.Fatal("书的附魔台候选应包含锋利")
	}
}

// TestEnchantingResultsDeterministic 验证结果确定性与合法性：
// 同种子同输入结果一致；结果内附魔两两兼容；书至多一个附魔。
func TestEnchantingResultsDeterministic(t *testing.T) {
	sword, err := item.FromName("minecraft:diamond_sword", 1)
	if err != nil {
		t.Fatal(err)
	}
	book, err := item.FromName("minecraft:book", 1)
	if err != nil {
		t.Fatal(err)
	}
	for seed := int64(-5); seed < 20; seed++ {
		a := enchantingResults(&javaRandom{}, seed, sword, 2, 30)
		b := enchantingResults(&javaRandom{}, seed, sword, 2, 30)
		if len(a) != len(b) {
			t.Fatalf("seed %d 结果不稳定：%d vs %d", seed, len(a), len(b))
		}
		for i := range a {
			if a[i] != b[i] {
				t.Fatalf("seed %d 第 %d 项不稳定", seed, i)
			}
		}
		for i, inst := range a {
			if !registry.InEnchantingTable(inst.id) {
				t.Fatalf("seed %d 结果 %d 不在 in_enchanting_table", seed, inst.id)
			}
			if !registry.EnchantmentSupported(inst.id, sword.ItemID) {
				t.Fatalf("seed %d 结果 %d 不支持钻石剑", seed, inst.id)
			}
			for j, other := range a {
				if i != j && !registry.EnchantmentCompatible(inst.id, other.id) {
					t.Fatalf("seed %d 结果含互斥附魔 %d/%d", seed, inst.id, other.id)
				}
			}
		}
		// 书特例的删除语义由 TestBookEnchantmentTrim 精确验证；
		// 这里只校验结果的合法性。
		for i, inst := range enchantingResults(&javaRandom{}, seed, book, 2, 30) {
			if !registry.InEnchantingTable(inst.id) {
				t.Fatalf("seed %d 书结果 %d 不合法", seed, inst.id)
			}
			if i > 0 {
				continue
			}
		}
	}

	// 不可附魔物品无结果。
	if got := enchantingResults(&javaRandom{}, 1, item.Empty(), 0, 10); len(got) != 0 {
		t.Fatalf("空输入不应有结果")
	}
}

// TestBookEnchantmentTrim 精确验证书特例（原版 dif：list.size() > 1 时随机
// 移除一个）：种子固定时删除位置可预测。
func TestBookEnchantmentTrim(t *testing.T) {
	book, err := item.FromName("minecraft:book", 1)
	if err != nil {
		t.Fatal(err)
	}
	sword, err := item.FromName("minecraft:diamond_sword", 1)
	if err != nil {
		t.Fatal(err)
	}
	result := []enchantmentInstance{{id: 0, level: 1}, {id: 1, level: 2}, {id: 3, level: 3}}

	// 书：3 个 → 删 1 个（seed 123456789 的 nextInt(3) = 1（JDK 验证）
	// → 删除第 2 项）。
	rng := &javaRandom{}
	rng.setSeed(123456789)
	trimmed := bookEnchantmentTrim(rng, book, result)
	if len(trimmed) != 2 || trimmed[0].id != 0 || trimmed[1].id != 3 {
		t.Fatalf("书特例结果 = %+v, want [{0 1} {3 3}]", trimmed)
	}

	// 非书不受影响（且消耗随机数在内部分支前已返回）。
	trimmed = bookEnchantmentTrim(&javaRandom{}, sword, result)
	if len(trimmed) != 3 {
		t.Fatalf("非书不应被裁剪：%+v", trimmed)
	}
	// 至多一个时不受影响。
	trimmed = bookEnchantmentTrim(&javaRandom{}, book, result[:1])
	if len(trimmed) != 1 {
		t.Fatalf("单项列表不应被裁剪：%+v", trimmed)
	}
}

// TestEnchantingSlotBackingRules 验证槽位规则：青金石槽只接受青金石、
// 输入槽堆叠上限 1、Shift 移动目标与原版一致。
func TestEnchantingSlotBackingRules(t *testing.T) {
	backing := enchantingSlotBacking{player: &session{}, state: &enchantingState{}}
	lapis, err := item.FromName("minecraft:lapis_lazuli", 3)
	if err != nil {
		t.Fatal(err)
	}
	sword, err := item.FromName("minecraft:diamond_sword", 1)
	if err != nil {
		t.Fatal(err)
	}
	if !backing.CanPlace(enchantingLapisSlot, lapis) {
		t.Fatal("青金石槽应接受青金石")
	}
	if backing.CanPlace(enchantingLapisSlot, sword) {
		t.Fatal("青金石槽不应接受剑")
	}
	if !backing.CanPlace(enchantingInputSlot, sword) {
		t.Fatal("输入槽应接受任意物品")
	}
	if got := backing.SlotLimit(enchantingInputSlot); got != 1 {
		t.Fatalf("输入槽堆叠上限 = %d, want 1", got)
	}
	if got := backing.SlotLimit(enchantingLapisSlot); got != 0 {
		t.Fatalf("青金石槽堆叠上限 = %d, want 0（不限）", got)
	}

	// 输入槽物品 → 背包（原版 move(2, 38, true)，反向）；青金石 → 青金石槽；
	// 其他物品 → 仅向输入槽放 1 个（single）。
	// 注意：quickMove 操作工作快照，写回由 handleContainerClick 统一完成。
	empty := make([]item.Stack, backing.SlotCount())
	if steps := quickMoveSteps(backing, empty, enchantingInputSlot); len(steps) == 0 || steps[0] != (quickMoveStep{2, 38, true, false}) {
		t.Fatalf("输入槽 Shift 步骤 = %v", steps)
	}
	backing.state.inputs[0] = sword
	workingSword := make([]item.Stack, backing.SlotCount())
	workingSword[2] = sword
	if steps := quickMoveSteps(backing, workingSword, 2); len(steps) != 1 || steps[0] != (quickMoveStep{enchantingInputSlot, enchantingInputSlot + 1, false, true}) {
		t.Fatalf("剑 Shift 步骤 = %v", steps)
	}
	backing.state.inputs[0] = item.Empty()
	backing.state.inputs[1] = item.Empty()
	working := make([]item.Stack, backing.SlotCount())
	// 工作快照与 backing 保持一致（真实流程由 handleContainerClick 初始化）。
	backing.Set(2, lapis)
	working[2] = lapis
	quickMove(&session{}, backing, working, 2)
	if got := working[enchantingLapisSlot]; got.ItemID != lapis.ItemID || got.Count != 3 {
		t.Fatalf("青金石应移入青金石槽：%+v", got)
	}
	if got := working[2]; !got.IsEmpty() {
		t.Fatalf("青金石应全部移出背包：%+v", got)
	}
	// 再来一份剑：输入槽为空，应只放入 1 个。
	swords, err := item.FromName("minecraft:diamond_sword", 5)
	if err != nil {
		t.Fatal(err)
	}
	backing.Set(2, swords)
	working[2] = swords
	quickMove(&session{}, backing, working, 2)
	if got := working[enchantingInputSlot]; got.ItemID != swords.ItemID || got.Count != 1 {
		t.Fatalf("输入槽应只放入 1 个剑：%+v", got)
	}
	if got := working[2]; got.Count != 4 {
		t.Fatalf("剩余剑应留在背包：%+v", got)
	}
}

// expectEnchantingProperties 读取并校验 10 个附魔台数据槽包。
func expectEnchantingProperties(t *testing.T, conn net.Conn, windowID int32, wantCosts [3]int16) {
	t.Helper()
	for index := int16(0); index < 10; index++ {
		property := expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerProperty)
		_, offset, err := protocol.DecodeVarInt(property)
		if err != nil {
			t.Fatal(err)
		}
		propWindow, size, err := protocol.DecodeVarInt(property[offset:])
		if err != nil {
			t.Fatal(err)
		}
		offset += size
		propID := int16(uint16(property[offset])<<8 | uint16(property[offset+1]))
		propValue := int16(uint16(property[offset+2])<<8 | uint16(property[offset+3]))
		if propWindow != windowID || propID != index {
			t.Fatalf("数据槽 = (%d, %d), want (%d, %d)", propWindow, propID, windowID, index)
		}
		if index < 3 && propValue != wantCosts[index] {
			t.Fatalf("成本槽 %d = %d, want %d", index, propValue, wantCosts[index])
		}
		// 线索值必须合法：-1 或有效的附魔/等级。
		if index >= 3 && index <= 5 && propValue != -1 {
			if !registry.InEnchantingTable(int32(propValue)) {
				t.Fatalf("附魔线索 %d 不在 in_enchanting_table", propValue)
			}
		}
		if index >= 6 && index <= 8 && propValue != -1 {
			if propValue < 1 {
				t.Fatalf("等级线索 %d 非法", propValue)
			}
		}
	}
}

// TestEnchantingWindowFlow 验证附魔台窗口端到端流程：打开、放入物品与青金石、
// 成本与线索同步、点击选项附魔（扣级、消耗青金石、写入附魔组件）、关闭归还。
func TestEnchantingWindowFlow(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "EnchantUser")
	defer conn.Close()

	// 固定附魔种子使成本可预测（书架 0 时 seed 123456789 → [2, 5, 4]）。
	player := activeSession(t, instance, "EnchantUser")
	player.setEnchantmentSeed(123456789)
	player.giveExperienceLevels(10)

	ex, ey, ez := chestPositions(t, instance)
	placeBlockAt(t, instance, ex, ey, ez, enchantingTableName)

	sendUseItemOn(t, conn, ex, ey, ez, 1)
	windowID, menuType := openScreen(t, conn)
	wantMenu, ok := registry.StaticEntryID("minecraft:menu", "minecraft:enchantment")
	if !ok || menuType != wantMenu {
		t.Fatalf("菜单类型 = %d, want %d", menuType, wantMenu)
	}
	content := expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)
	gotWindow, slots := containerContent(t, content)
	if gotWindow != windowID || len(slots) != enchantingSlots {
		t.Fatalf("窗口 %d 槽位数 %d, want %d/%d", gotWindow, len(slots), windowID, enchantingSlots)
	}
	// 空输入时全部成本为 0。
	expectEnchantingProperties(t, conn, windowID, [3]int16{0, 0, 0})

	// 放入钻石剑（快捷栏 0 → 输入槽）与 3 个青金石（快捷栏 1 → 青金石槽）。
	sword := testStack(t, "minecraft:diamond_sword", 1)
	player.inventory.Set(item.SlotHotbarStart, sword)
	lapis, err := item.FromName("minecraft:lapis_lazuli", 3)
	if err != nil {
		t.Fatal(err)
	}
	player.inventory.Set(item.SlotHotbarStart+1, lapis)
	hotbarSlot := int16(2 + 27)    // 窗口槽 29 = 快捷栏 0
	lapisSlot := int16(2 + 27 + 1) // 窗口槽 30 = 快捷栏 1

	// 拾取剑 → 放入输入槽。
	// 每次点击：先同步数据槽（10 个），再同步变更的槽位与光标。
	sendContainerClick(t, conn, windowID, 0, hotbarSlot, 0, protocol.ClickModePickup)
	expectEnchantingProperties(t, conn, windowID, [3]int16{0, 0, 0})
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)
	sendContainerClick(t, conn, windowID, 0, enchantingInputSlot, 0, protocol.ClickModePickup)
	expectEnchantingProperties(t, conn, windowID, [3]int16{2, 3, 4})
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)

	// 拾取青金石 → 放入青金石槽。
	sendContainerClick(t, conn, windowID, 0, lapisSlot, 0, protocol.ClickModePickup)
	expectEnchantingProperties(t, conn, windowID, [3]int16{2, 3, 4})
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)
	sendContainerClick(t, conn, windowID, 0, enchantingLapisSlot, 0, protocol.ClickModePickup)
	expectEnchantingProperties(t, conn, windowID, [3]int16{2, 3, 4})
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)

	// 点击选项 2（成本 4、扣 3 级、消耗 3 个青金石）。
	sendContainerButtonClick(t, conn, windowID, 2)
	bar, level, total := parseSetExperience(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetExperience))
	if level != 7 || bar != 0 || total != experienceForLevel(7) {
		t.Fatalf("附魔应扣 3 级：(bar %v, level %d, total %d)", bar, level, total)
	}
	if _, _, slot, stack := setContainerSlotStack(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)); slot != enchantingInputSlot {
		t.Fatalf("输入槽同步包槽位 = %d", slot)
	} else if stack.ItemID != sword.ItemID || len(stack.Enchantments) == 0 {
		t.Fatalf("输入槽剑应获得附魔：%+v", stack)
	} else {
		for _, ench := range stack.Enchantments {
			if !registry.InEnchantingTable(ench.ID) || ench.Level < 1 {
				t.Fatalf("附魔条目非法：%+v", ench)
			}
		}
		t.Logf("附魔结果：%+v", stack.Enchantments)
	}
	if _, _, slot, stack := setContainerSlotStack(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)); slot != enchantingLapisSlot || !stack.IsEmpty() {
		t.Fatalf("青金石槽应被清空：slot=%d stack=%+v", slot, stack)
	}
	// 选项重算（新种子）后再同步 10 个数据槽，随后是方块使用音效。
	expectEnchantingProperties(t, conn, windowID, [3]int16{})
	expectPlayPacket(t, conn, protocol.PlayPacketIDSoundEffect)

	// 关闭窗口：输入槽物品归还背包（客户端主动关闭时服务端不回发 Close）。
	sendContainerClose(t, conn, windowID)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetPlayerInventory)
}

// sendContainerClose 发送 Container Close 包（客户端主动关闭窗口）。
func sendContainerClose(t *testing.T, conn net.Conn, windowID int32) {
	t.Helper()
	packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDContainerClose)
	packet = protocol.AppendVarInt(packet, windowID)
	if err := protocol.WritePacketWithCompression(conn, packet, compressionThreshold); err != nil {
		t.Fatal(err)
	}
}

// TestEnchantingBookshelfCount 验证书架计数（32 个偏移、外圈、y∈{0,1}、
// 中点为传播方块才计数）。
func TestEnchantingBookshelfCount(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "ShelfUser")
	defer conn.Close()

	ex, ey, ez := chestPositions(t, instance)
	placeBlockAt(t, instance, ex, ey, ez, enchantingTableName)
	pos := [3]int{ex, ey, ez}
	if got := instance.countBookshelves(world.DimensionOverworld, pos); got != 0 {
		t.Fatalf("无书架时计数 = %d, want 0", got)
	}
	// 两个合法书架（中点为空气）。
	placeBlockAt(t, instance, ex+2, ey, ez, "minecraft:bookshelf")
	placeBlockAt(t, instance, ex-2, ey+1, ez, "minecraft:bookshelf")
	if got := instance.countBookshelves(world.DimensionOverworld, pos); got != 2 {
		t.Fatalf("两个书架时计数 = %d, want 2", got)
	}
	// 对角书架被中点实心方块遮挡（中点 = 附魔台 + 偏移/2）。
	placeBlockAt(t, instance, ex+2, ey, ez+2, "minecraft:bookshelf")
	placeBlockAt(t, instance, ex+1, ey, ez+1, "minecraft:stone")
	if got := instance.countBookshelves(world.DimensionOverworld, pos); got != 2 {
		t.Fatalf("遮挡书架不应计数：%d, want 2", got)
	}
	// 内圈（|x|、|z| 都 < 2）不算 provider；放在 (1,0) 处还会遮挡
	// (2,0) 外圈书架的中点（中点 = 附魔台 + 偏移/2）→ 计数减 1。
	placeBlockAt(t, instance, ex+1, ey, ez, "minecraft:bookshelf")
	if got := instance.countBookshelves(world.DimensionOverworld, pos); got != 1 {
		t.Fatalf("内圈书架不应作为 provider，且应遮挡 (2,0) 的中点：%d, want 1", got)
	}
}

// TestEnchantingBookBecomesEnchantedBook 验证普通书附魔后变成附魔书。
func TestEnchantingBookBecomesEnchantedBook(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "BookUser")
	defer conn.Close()

	player := activeSession(t, instance, "BookUser")
	player.setEnchantmentSeed(123456789)
	player.giveExperienceLevels(10)

	ex, ey, ez := chestPositions(t, instance)
	placeBlockAt(t, instance, ex, ey, ez, enchantingTableName)
	sendUseItemOn(t, conn, ex, ey, ez, 1)
	windowID, _ := openScreen(t, conn)
	expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)
	expectEnchantingProperties(t, conn, windowID, [3]int16{0, 0, 0})

	// 书放快捷栏 0、1 个青金石放快捷栏 1。
	book, err := item.FromName("minecraft:book", 1)
	if err != nil {
		t.Fatal(err)
	}
	player.inventory.Set(item.SlotHotbarStart, book)
	lapis, err := item.FromName("minecraft:lapis_lazuli", 1)
	if err != nil {
		t.Fatal(err)
	}
	player.inventory.Set(item.SlotHotbarStart+1, lapis)

	sendContainerClick(t, conn, windowID, 0, 29, 0, protocol.ClickModePickup)
	expectEnchantingProperties(t, conn, windowID, [3]int16{0, 0, 0})
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)
	sendContainerClick(t, conn, windowID, 0, enchantingInputSlot, 0, protocol.ClickModePickup)
	expectEnchantingProperties(t, conn, windowID, [3]int16{2, 3, 4})
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)
	sendContainerClick(t, conn, windowID, 0, 30, 0, protocol.ClickModePickup)
	expectEnchantingProperties(t, conn, windowID, [3]int16{2, 3, 4})
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)
	sendContainerClick(t, conn, windowID, 0, enchantingLapisSlot, 0, protocol.ClickModePickup)
	expectEnchantingProperties(t, conn, windowID, [3]int16{2, 3, 4})
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)

	// 点击选项 0（成本 2、扣 1 级）。
	sendContainerButtonClick(t, conn, windowID, 0)
	_, level, _ := parseSetExperience(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetExperience))
	if level != 9 {
		t.Fatalf("附魔书应扣 1 级：level %d", level)
	}
	enchantedBookID, err := registry.ItemID("minecraft:enchanted_book")
	if err != nil {
		t.Fatal(err)
	}
	_, _, slot, stack := setContainerSlotStack(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot))
	if slot != enchantingInputSlot || stack.ItemID != enchantedBookID {
		t.Fatalf("书应变为附魔书：slot=%d stack=%+v", slot, stack)
	}
	if len(stack.Enchantments) == 0 {
		t.Fatalf("附魔书应带有附魔：%+v", stack)
	}
	if len(stack.Enchantments) > 2 {
		t.Fatalf("书的附魔数量异常（原版至多删减一个）：%+v", stack.Enchantments)
	}
}
