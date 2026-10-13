package server

import (
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
)

// 砂轮机测试：
//   - removeNonCurses（去魔、诅咒保留、附魔书变书、惩罚重置）单元；
//   - computeResult（合并、诅咒并集、堆叠特例、组件一致性）单元；
//   - 经验价值与窗口全流程 e2e。

func grindstoneTestEnchant(t *testing.T, name string) int32 {
	t.Helper()
	id, ok := registry.SyncEntryID("minecraft:enchantment", name)
	if !ok {
		t.Fatalf("缺少附魔 %s", name)
	}
	return id
}

func TestGrindstoneRemoveNonCurses(t *testing.T) {
	sharpness := grindstoneTestEnchant(t, "minecraft:sharpness")
	curse := grindstoneTestEnchant(t, "minecraft:binding_curse")

	// 剑：附魔全部移除、累积惩罚清零。
	sword := testStack(t, "minecraft:diamond_sword", 1)
	sword.Damage = 50
	sword.RepairCost = 7
	sword.Enchantments = []item.EnchantmentEntry{{ID: sharpness, Level: 3}}
	stripped := grindstoneRemoveNonCurses(sword)
	if len(stripped.Enchantments) != 0 || stripped.RepairCost != 0 {
		t.Fatalf("去魔结果 = %+v, want 无附魔/惩罚 0", stripped)
	}
	if stripped.ItemID != sword.ItemID || stripped.Damage != 50 || stripped.Count != 1 {
		t.Fatalf("去魔不应改变物品/耐久/数量：%+v", stripped)
	}

	// 诅咒保留（惩罚 = 1 次递增 = 1）。
	cursed := testStack(t, "minecraft:diamond_sword", 1)
	cursed.Enchantments = []item.EnchantmentEntry{{ID: curse, Level: 1}, {ID: sharpness, Level: 3}}
	out := grindstoneRemoveNonCurses(cursed)
	if len(out.Enchantments) != 1 || out.Enchantments[0] != (item.EnchantmentEntry{ID: curse, Level: 1}) {
		t.Fatalf("诅咒应保留：%+v", out.Enchantments)
	}
	if out.RepairCost != 1 {
		t.Fatalf("一个诅咒的惩罚 = %d, want 1", out.RepairCost)
	}

	// 两个诅咒：惩罚 = min(2*1+1) = 3。
	double := testStack(t, "minecraft:diamond_sword", 1)
	double.Enchantments = []item.EnchantmentEntry{
		{ID: curse, Level: 1},
		{ID: grindstoneTestEnchant(t, "minecraft:vanishing_curse"), Level: 1},
	}
	if got := grindstoneRemoveNonCurses(double); got.RepairCost != 3 {
		t.Fatalf("两个诅咒的惩罚 = %d, want 3", got.RepairCost)
	}

	// 附魔书：附魔清空后变为普通书；诅咒书保持附魔书。
	bookID, err := registry.ItemID("minecraft:book")
	if err != nil {
		t.Fatal(err)
	}
	enchantedBook := testStack(t, "minecraft:enchanted_book", 1)
	enchantedBook.RepairCost = 5
	enchantedBook.Enchantments = []item.EnchantmentEntry{{ID: sharpness, Level: 1}}
	plain := grindstoneRemoveNonCurses(enchantedBook)
	if plain.ItemID != bookID || len(plain.Enchantments) != 0 || plain.RepairCost != 0 {
		t.Fatalf("附魔书去魔后应为普通书：%+v", plain)
	}
	cursedBook := testStack(t, "minecraft:enchanted_book", 1)
	cursedBook.Enchantments = []item.EnchantmentEntry{{ID: curse, Level: 1}}
	kept := grindstoneRemoveNonCurses(cursedBook)
	if kept.ItemID != enchantedBook.ItemID || len(kept.Enchantments) != 1 {
		t.Fatalf("仅诅咒的附魔书应保持附魔书：%+v", kept)
	}
}

func TestGrindstoneComputeResult(t *testing.T) {
	sharpness := grindstoneTestEnchant(t, "minecraft:sharpness")
	unbreaking := grindstoneTestEnchant(t, "minecraft:unbreaking")
	curse := grindstoneTestEnchant(t, "minecraft:binding_curse")
	vanishing := grindstoneTestEnchant(t, "minecraft:vanishing_curse")

	// 空输入。
	if !grindstoneComputeResult(item.Empty(), item.Empty()).IsEmpty() {
		t.Fatal("空输入应无结果")
	}
	// 单一无附魔物品：无结果。
	plain := testStack(t, "minecraft:diamond_sword", 1)
	if !grindstoneComputeResult(plain, item.Empty()).IsEmpty() {
		t.Fatal("无附魔物品应无结果")
	}
	// 数量 > 1：无结果。
	stacked := plain
	stacked.Count = 2
	if !grindstoneComputeResult(stacked, item.Empty()).IsEmpty() {
		t.Fatal("数量大于 1 应无结果")
	}
	// 异物品：无结果。
	pick := testStack(t, "minecraft:diamond_pickaxe", 1)
	if !grindstoneComputeResult(plain, pick).IsEmpty() {
		t.Fatal("不同物品应无结果")
	}

	// 两把同物品受损剑：耐久合并（+5% 上限奖励）、附魔全部清空。
	left := testStack(t, "minecraft:diamond_sword", 1)
	left.Damage = 300
	left.Enchantments = []item.EnchantmentEntry{{ID: sharpness, Level: 3}}
	right := testStack(t, "minecraft:diamond_sword", 1)
	right.Damage = 300
	right.Enchantments = []item.EnchantmentEntry{{ID: unbreaking, Level: 2}}
	maxDamage, ok := registry.MaxDamage(left.ItemID)
	if !ok {
		t.Fatal("钻石剑应为可损坏物品")
	}
	combined := (maxDamage - 300) + (maxDamage - 300) + maxDamage*5/100
	wantDamage := max(maxDamage-combined, 0)
	merged := grindstoneComputeResult(left, right)
	if merged.IsEmpty() || merged.Damage != wantDamage || merged.Count != 1 {
		t.Fatalf("合并结果 = %+v, want Damage=%d", merged, wantDamage)
	}
	if len(merged.Enchantments) != 0 || merged.RepairCost != 0 {
		t.Fatalf("合并应清空非诅咒附魔：%+v", merged.Enchantments)
	}

	// 单一附魔剑：去魔。
	enchanted := testStack(t, "minecraft:diamond_sword", 1)
	enchanted.Enchantments = []item.EnchantmentEntry{{ID: sharpness, Level: 1}}
	stripped := grindstoneComputeResult(enchanted, item.Empty())
	if stripped.IsEmpty() || len(stripped.Enchantments) != 0 {
		t.Fatalf("附魔剑去魔结果 = %+v", stripped)
	}

	// 合并时双方诅咒取并集；同名诅咒保留第一输入的等级。
	curseA := testStack(t, "minecraft:diamond_sword", 1)
	curseA.Enchantments = []item.EnchantmentEntry{{ID: curse, Level: 1}}
	curseB := testStack(t, "minecraft:diamond_sword", 1)
	curseB.Enchantments = []item.EnchantmentEntry{{ID: vanishing, Level: 1}}
	union := grindstoneComputeResult(curseA, curseB)
	if len(union.Enchantments) != 2 {
		t.Fatalf("诅咒应为并集：%+v", union.Enchantments)
	}
	curseC := testStack(t, "minecraft:diamond_sword", 1)
	curseC.Enchantments = []item.EnchantmentEntry{{ID: curse, Level: 2}}
	same := grindstoneComputeResult(curseA, curseC)
	if len(same.Enchantments) != 1 || same.Enchantments[0].Level != 1 {
		t.Fatalf("同名诅咒应保留第一输入等级：%+v", same.Enchantments)
	}

	// 两本附魔书（堆叠上限 1、不可损坏）：无结果（原版 maxStackSize < 2 分支）。
	bookA := testStack(t, "minecraft:enchanted_book", 1)
	bookA.Enchantments = []item.EnchantmentEntry{{ID: sharpness, Level: 1}}
	bookB := testStack(t, "minecraft:enchanted_book", 1)
	bookB.Enchantments = []item.EnchantmentEntry{{ID: unbreaking, Level: 1}}
	if !grindstoneComputeResult(bookA, bookB).IsEmpty() {
		t.Fatal("两本附魔书应无结果")
	}

	// 两本普通书（不可损坏、堆叠上限 >= 2、组件一致）：结果为 2 本。
	plainBookA := testStack(t, "minecraft:book", 1)
	plainBookB := testStack(t, "minecraft:book", 1)
	two := grindstoneComputeResult(plainBookA, plainBookB)
	if two.IsEmpty() || two.Count != 2 || two.ItemID != plainBookA.ItemID {
		t.Fatalf("两本普通书应为 2 本：%+v", two)
	}
	// 组件不一致（改名）：无结果。
	renamed := testStack(t, "minecraft:book", 1)
	renamed.CustomName = "X"
	if !grindstoneComputeResult(plainBookA, renamed).IsEmpty() {
		t.Fatal("组件不一致的堆叠物品应无结果")
	}

	// 单本附魔书：变为普通书。
	bookID, err := registry.ItemID("minecraft:book")
	if err != nil {
		t.Fatal(err)
	}
	single := grindstoneComputeResult(bookA, item.Empty())
	if single.IsEmpty() || single.ItemID != bookID || len(single.Enchantments) != 0 {
		t.Fatalf("单本附魔书应变为普通书：%+v", single)
	}
}

func TestGrindstoneExperience(t *testing.T) {
	sharpness := grindstoneTestEnchant(t, "minecraft:sharpness")
	curse := grindstoneTestEnchant(t, "minecraft:binding_curse")

	// 价值 = 非诅咒附魔的 getMinCost 之和。
	sword := testStack(t, "minecraft:diamond_sword", 1)
	sword.Enchantments = []item.EnchantmentEntry{{ID: sharpness, Level: 3}}
	minCost, _, ok := registry.EnchantmentCostRange(sharpness, 3)
	if !ok {
		t.Fatal("缺少锋利 III 的成本区间")
	}
	if got := grindstoneExperienceValue(sword); got != minCost {
		t.Fatalf("经验价值 = %d, want %d", got, minCost)
	}
	cursed := testStack(t, "minecraft:diamond_sword", 1)
	cursed.Enchantments = []item.EnchantmentEntry{{ID: curse, Level: 1}}
	if got := grindstoneExperienceValue(cursed); got != 0 {
		t.Fatalf("诅咒不应计入经验价值：%d", got)
	}

	// 发放量 = ceil(total/2) + nextInt(ceil(total/2))：确定性 + 区间。
	s1, s2 := &Server{}, &Server{}
	amount1 := s1.grindstoneExperienceAmount(sword, item.Empty())
	amount2 := s2.grindstoneExperienceAmount(sword, item.Empty())
	if amount1 != amount2 {
		t.Fatalf("相同状态下发放量应确定：%d vs %d", amount1, amount2)
	}
	half := (minCost + 1) / 2
	if amount1 < half || amount1 > 2*half-1 {
		t.Fatalf("发放量 = %d, want [%d, %d]", amount1, half, 2*half-1)
	}
	// 无双附魔输入：0。
	if got := s1.grindstoneExperienceAmount(item.Empty(), item.Empty()); got != 0 {
		t.Fatalf("无附魔输入的经验 = %d, want 0", got)
	}
}

// TestGrindstoneWindowDisenchant 验证砂轮机窗口全流程：
// 放入带附魔与累积惩罚的受损剑 → 结果槽出现去魔剑 → 取走（经验球）→ 输入清空。
func TestGrindstoneWindowDisenchant(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "GrindstoneUser")
	defer conn.Close()

	x, y, z := chestPositions(t, instance)
	placeBlockAt(t, instance, x, y, z, grindstoneBlockName)
	sendUseItemOn(t, conn, x, y, z, 1)
	windowID, menuType := openScreen(t, conn)
	wantMenu, ok := registry.StaticEntryID("minecraft:menu", "minecraft:grindstone")
	if !ok || menuType != wantMenu {
		t.Fatalf("菜单类型 = %d, want %d", menuType, wantMenu)
	}
	content := expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)
	gotWindow, slots := containerContent(t, content)
	if gotWindow != windowID || len(slots) != grindstoneSlots {
		t.Fatalf("窗口 %d 槽位数 %d, want %d/%d", gotWindow, len(slots), windowID, grindstoneSlots)
	}

	// 服务器侧放入带锋利 III 与累积惩罚 5 的受损钻石剑（快捷栏 0）。
	player := activeSession(t, instance, "GrindstoneUser")
	sharpness := grindstoneTestEnchant(t, "minecraft:sharpness")
	sword := testStack(t, "minecraft:diamond_sword", 1)
	sword.Damage = 100
	sword.RepairCost = 5
	sword.Enchantments = []item.EnchantmentEntry{{ID: sharpness, Level: 3}}
	player.inventory.Set(item.SlotHotbarStart, sword)

	// 快捷栏 0 → 输入槽 0；输入槽与结果槽依次同步。
	sendContainerClick(t, conn, windowID, 0, 3+27, 0, protocol.ClickModePickup)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)
	sendContainerClick(t, conn, windowID, 0, grindstoneInputSlot, 0, protocol.ClickModePickup)
	if _, _, slot, stack := setContainerSlotStack(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)); slot != grindstoneInputSlot ||
		len(stack.Enchantments) != 1 || stack.Enchantments[0].ID != sharpness {
		t.Fatalf("输入槽应收到附魔剑：slot=%d stack=%+v", slot, stack)
	}
	if _, _, slot, stack := setContainerSlotStack(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)); slot != grindstoneResultSlot ||
		stack.IsEmpty() || len(stack.Enchantments) != 0 || stack.RepairCost != 0 || stack.Damage != 100 {
		t.Fatalf("结果槽应为去魔剑：slot=%d stack=%+v", slot, stack)
	}
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)

	// 取走：经验球先生成（活体实体包），随后输入槽与结果槽清空，
	// 光标获得去魔剑（惩罚清零）。
	sendContainerClick(t, conn, windowID, 0, grindstoneResultSlot, 0, protocol.ClickModePickup)
	expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)
	if _, _, slot, stack := setContainerSlotStack(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)); slot != grindstoneInputSlot || !stack.IsEmpty() {
		t.Fatalf("输入槽应被清空：slot=%d stack=%+v", slot, stack)
	}
	if _, _, slot, stack := setContainerSlotStack(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)); slot != grindstoneResultSlot || !stack.IsEmpty() {
		t.Fatalf("结果槽应被清空：slot=%d stack=%+v", slot, stack)
	}
	cursor := expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)
	_, cursorOffset, err := protocol.DecodeVarInt(cursor)
	if err != nil {
		t.Fatal(err)
	}
	cursorItem, _ := parseSlotWithComponents(t, cursor, cursorOffset)
	if cursorItem.Damage != 100 || len(cursorItem.Enchantments) != 0 || cursorItem.RepairCost != 0 {
		t.Fatalf("光标应为去魔剑：%+v", cursorItem)
	}
}

// TestGrindstoneCloseReturnsInputs 验证关闭窗口时输入槽归还背包。
func TestGrindstoneCloseReturnsInputs(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "GrindstoneCloser")
	defer conn.Close()

	x, y, z := chestPositions(t, instance)
	placeBlockAt(t, instance, x, y, z, grindstoneBlockName)
	sendUseItemOn(t, conn, x, y, z, 1)
	windowID, _ := openScreen(t, conn)
	expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)

	player := activeSession(t, instance, "GrindstoneCloser")
	sword := testStack(t, "minecraft:diamond_sword", 1)
	sword.Damage = 100
	sword.Enchantments = []item.EnchantmentEntry{{ID: grindstoneTestEnchant(t, "minecraft:sharpness"), Level: 1}}
	player.inventory.Set(item.SlotHotbarStart, sword)
	sendContainerClick(t, conn, windowID, 0, 3+27, 0, protocol.ClickModePickup)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)
	sendContainerClick(t, conn, windowID, 0, grindstoneInputSlot, 0, protocol.ClickModePickup)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot) // 结果槽
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)

	closePacket := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDContainerClose)
	closePacket = protocol.AppendVarInt(closePacket, windowID)
	if err := protocol.WritePacketWithCompression(conn, closePacket, compressionThreshold); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		for slot := 0; slot < item.InventorySlots; slot++ {
			stack := player.inventory.Get(slot)
			if stack.ItemID == sword.ItemID && stack.Damage == 100 {
				return true
			}
		}
		return false
	}, "关闭砂轮机窗口应归还输入槽物品")
}
