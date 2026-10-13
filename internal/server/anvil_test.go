package server

import (
	"math"
	"net"
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
)

// ---------------------------------------------------------------------------
// 计算逻辑单元测试（对照 1.21.11 AnvilMenu.createResult 的字节码翻译）

func testStack(t *testing.T, name string, count int32) item.Stack {
	t.Helper()
	stack, err := item.FromName(name, count)
	if err != nil {
		t.Fatal(err)
	}
	return stack
}

func TestComputeAnvilResultEmptyInput(t *testing.T) {
	res := computeAnvilResult(item.Empty(), item.Empty(), "", false, false)
	if !res.result.IsEmpty() || res.cost != 0 {
		t.Fatalf("空输入应无结果，得到 %+v", res)
	}
}

func TestComputeAnvilResultMaterialRepair(t *testing.T) {
	sword := testStack(t, "minecraft:diamond_sword", 1)
	sword.Damage = 100
	material := testStack(t, "minecraft:diamond", 1)
	res := computeAnvilResult(sword, material, "", false, false)
	if res.result.IsEmpty() {
		t.Fatal("材料修复应有结果")
	}
	if res.result.Damage != 0 {
		t.Fatalf("一个材料应修复 min(100, max/4) 点耐久：Damage = %d, want 0", res.result.Damage)
	}
	if res.cost != 1 {
		t.Fatalf("成本 = %d, want 1", res.cost)
	}
	if res.repairItemCountCost != 1 {
		t.Fatalf("材料消耗 = %d, want 1", res.repairItemCountCost)
	}
	if res.result.RepairCost != 1 {
		t.Fatalf("累积惩罚 = %d, want 1（0 递增一级）", res.result.RepairCost)
	}
}

func TestComputeAnvilResultMaterialRepairPartial(t *testing.T) {
	sword := testStack(t, "minecraft:diamond_sword", 1)
	sword.Damage = 1000
	material := testStack(t, "minecraft:diamond", 2)
	maxDamage, ok := registry.MaxDamage(sword.ItemID)
	if !ok {
		t.Fatal("钻石剑应有耐久上限")
	}
	res := computeAnvilResult(sword, material, "", false, false)
	step := maxDamage / 4
	want := int32(1000) - 2*step
	if res.result.Damage != want {
		t.Fatalf("两个材料各修复 %d 点：Damage = %d, want %d", step, res.result.Damage, want)
	}
	if res.cost != 2 || res.repairItemCountCost != 2 {
		t.Fatalf("成本/材料消耗 = %d/%d, want 2/2", res.cost, res.repairItemCountCost)
	}
}

func TestComputeAnvilResultMaterialRepairUndamaged(t *testing.T) {
	sword := testStack(t, "minecraft:diamond_sword", 1)
	material := testStack(t, "minecraft:diamond", 1)
	res := computeAnvilResult(sword, material, "", false, false)
	if !res.result.IsEmpty() || res.cost != 0 {
		t.Fatalf("未损坏物品 + 材料应无结果，得到 %+v", res)
	}
}

func TestComputeAnvilResultSacrificeRepair(t *testing.T) {
	left := testStack(t, "minecraft:diamond_sword", 1)
	left.Damage = 100
	right := testStack(t, "minecraft:diamond_sword", 1)
	right.Damage = 200
	res := computeAnvilResult(left, right, "", false, false)
	if res.result.IsEmpty() {
		t.Fatal("同物品合并应有结果")
	}
	// 剩余耐久 1461 + (1361 + 1561*12/100=187) = 3009 > 1561 → 修满。
	if res.result.Damage != 0 {
		t.Fatalf("合并应修满：Damage = %d, want 0", res.result.Damage)
	}
	if res.cost != 2 {
		t.Fatalf("成本 = %d, want 2", res.cost)
	}
	if res.repairItemCountCost != 0 {
		t.Fatalf("合并不应消耗材料计数：%d", res.repairItemCountCost)
	}
}

func TestComputeAnvilResultSacrificeNoGain(t *testing.T) {
	left := testStack(t, "minecraft:diamond_sword", 1)
	right := testStack(t, "minecraft:diamond_sword", 1)
	res := computeAnvilResult(left, right, "", false, false)
	if !res.result.IsEmpty() || res.cost != 0 {
		t.Fatalf("满耐久合并无改善应无结果，得到 cost=%d", res.cost)
	}
}

func TestComputeAnvilResultMismatchedItems(t *testing.T) {
	sword := testStack(t, "minecraft:diamond_sword", 1)
	sword.Damage = 100
	stone := testStack(t, "minecraft:stone", 1)
	res := computeAnvilResult(sword, stone, "", false, false)
	if !res.result.IsEmpty() || res.cost != 0 {
		t.Fatalf("不同物品且非材料应无结果，得到 cost=%d", res.cost)
	}
}

func TestComputeAnvilResultRename(t *testing.T) {
	sword := testStack(t, "minecraft:diamond_sword", 1)
	res := computeAnvilResult(sword, item.Empty(), "Excalibur", true, false)
	if res.result.IsEmpty() {
		t.Fatal("重命名应有结果")
	}
	if res.result.CustomName != "Excalibur" {
		t.Fatalf("CustomName = %q", res.result.CustomName)
	}
	if res.cost != 1 || !res.onlyRenaming {
		t.Fatalf("成本/纯重命名 = %d/%v, want 1/true", res.cost, res.onlyRenaming)
	}
	if res.result.RepairCost != 0 {
		t.Fatalf("纯重命名不增加累积惩罚：%d", res.result.RepairCost)
	}
}

func TestComputeAnvilResultRenameSameAsDefault(t *testing.T) {
	sword := testStack(t, "minecraft:diamond_sword", 1)
	defaultName := sword.HoverName()
	if defaultName == "" {
		t.Fatal("钻石剑应有默认显示名")
	}
	res := computeAnvilResult(sword, item.Empty(), defaultName, true, false)
	if !res.result.IsEmpty() || res.cost != 0 {
		t.Fatalf("与默认名相同应无结果，得到 cost=%d", res.cost)
	}
}

func TestComputeAnvilResultClearName(t *testing.T) {
	sword := testStack(t, "minecraft:diamond_sword", 1)
	sword.CustomName = "Foo"
	res := computeAnvilResult(sword, item.Empty(), "", true, false)
	if res.result.IsEmpty() {
		t.Fatal("清空名字应有结果")
	}
	if res.result.CustomName != "" {
		t.Fatalf("CustomName 应被清空，得到 %q", res.result.CustomName)
	}
	if res.cost != 1 {
		t.Fatalf("成本 = %d, want 1", res.cost)
	}
}

func TestComputeAnvilResultRenameOnlyCostCap(t *testing.T) {
	sword := testStack(t, "minecraft:diamond_sword", 1)
	sword.RepairCost = 39
	res := computeAnvilResult(sword, item.Empty(), "X", true, false)
	if res.result.IsEmpty() {
		t.Fatal("纯重命名在 40 级上限应被封顶为 39 而非清空")
	}
	if res.cost != 39 {
		t.Fatalf("成本应封顶 39，得到 %d", res.cost)
	}
	if !res.onlyRenaming {
		t.Fatal("应标记为纯重命名")
	}
}

func TestComputeAnvilResultCostLimit(t *testing.T) {
	sword := testStack(t, "minecraft:diamond_sword", 1)
	sword.Damage = 100
	sword.RepairCost = 40
	material := testStack(t, "minecraft:diamond", 1)
	res := computeAnvilResult(sword, material, "", false, false)
	if !res.result.IsEmpty() {
		t.Fatalf("成本达到 40 且非创造模式应无结果，得到 cost=%d", res.cost)
	}
	creative := computeAnvilResult(sword, material, "", false, true)
	if creative.result.IsEmpty() {
		t.Fatal("创造模式应保留 40 级以上的结果")
	}
	if creative.cost != 41 {
		t.Fatalf("创造模式成本 = %d, want 41", creative.cost)
	}
}

func TestComputeAnvilResultRepairCostPenalty(t *testing.T) {
	sword := testStack(t, "minecraft:diamond_sword", 1)
	sword.Damage = 100
	sword.RepairCost = 2
	material := testStack(t, "minecraft:diamond", 1)
	res := computeAnvilResult(sword, material, "", false, false)
	// 修复（非纯重命名）：惩罚从 max(2, 0) = 2 递增为 2*2+1 = 5。
	if res.result.RepairCost != 5 {
		t.Fatalf("累积惩罚 = %d, want 5", res.result.RepairCost)
	}

	sword2 := testStack(t, "minecraft:diamond_sword", 1)
	sword2.Damage = 100
	sword2.RepairCost = 1
	material2 := testStack(t, "minecraft:diamond", 1)
	material2.RepairCost = 5
	res2 := computeAnvilResult(sword2, material2, "", false, false)
	// 取两者较大值 5 再递增为 11。
	if res2.result.RepairCost != 11 {
		t.Fatalf("累积惩罚 = %d, want 11", res2.result.RepairCost)
	}
	// 成本 = baseCost（input 1 + 附加 5）+ 修复 1 = 7。
	if res2.cost != 7 {
		t.Fatalf("成本 = %d, want 7", res2.cost)
	}
}

func TestComputeAnvilResultStackRename(t *testing.T) {
	cobble := testStack(t, "minecraft:cobblestone", 64)
	res := computeAnvilResult(cobble, item.Empty(), "Rocks", true, false)
	if res.result.IsEmpty() || res.result.Count != 64 {
		t.Fatalf("整堆重命名应保留数量，得到 %+v", res.result)
	}
	if res.result.CustomName != "Rocks" || res.cost != 1 {
		t.Fatalf("CustomName/成本 = %q/%d", res.result.CustomName, res.cost)
	}
}

func TestComputeAnvilResultEmptyEnchantedBook(t *testing.T) {
	sword := testStack(t, "minecraft:diamond_sword", 1)
	book := testStack(t, "minecraft:enchanted_book", 1)
	res := computeAnvilResult(sword, book, "", false, false)
	// 空附魔书（无 stored_enchantments 数据）与原版行为一致：不产生任何效果。
	if !res.result.IsEmpty() || res.cost != 0 {
		t.Fatalf("空附魔书应无结果，得到 cost=%d", res.cost)
	}
}

func TestAnvilFilterName(t *testing.T) {
	if got, ok := anvilFilterName("  Hero  "); !ok || got != "  Hero  " {
		t.Fatalf("空格应保留：%q/%v", got, ok)
	}
	// 原版只删除 § 字符本身（格式码的字母保留为普通字符）。
	if got, ok := anvilFilterName("Hero§cRed"); !ok || got != "HerocRed" {
		t.Fatalf("§ 字符应被过滤：%q/%v", got, ok)
	}
	if got, ok := anvilFilterName("a\tb"); !ok || got != "ab" {
		t.Fatalf("控制字符应被过滤：%q/%v", got, ok)
	}
	long := ""
	for i := 0; i < 51; i++ {
		long += "a"
	}
	if _, ok := anvilFilterName(long); ok {
		t.Fatal("超过 50 个码元应无效")
	}
	exact := long[:50]
	if got, ok := anvilFilterName(exact); !ok || got != exact {
		t.Fatalf("恰好 50 个码元应有效：%q/%v", got, ok)
	}
	// UTF-16 码元计数：25 个 emoji（代理对）恰为 50 个码元。
	emoji := ""
	for i := 0; i < 25; i++ {
		emoji += "😀"
	}
	if _, ok := anvilFilterName(emoji); !ok {
		t.Fatal("25 个 emoji 应恰好 50 个 UTF-16 码元并有效")
	}
	if _, ok := anvilFilterName(emoji + "😀"); ok {
		t.Fatal("52 个 UTF-16 码元应无效")
	}
}

func TestAnvilNameIsBlank(t *testing.T) {
	for _, blank := range []string{"", " ", "  \t ", "\u00A0", "\u3000"} {
		if !anvilNameIsBlank(blank) {
			t.Fatalf("%q 应视为空白", blank)
		}
	}
	for _, notBlank := range []string{"a", " a ", "\u0085"} {
		if anvilNameIsBlank(notBlank) {
			t.Fatalf("%q 不应视为空白", notBlank)
		}
	}
}

func TestExperienceForLevelInverse(t *testing.T) {
	for level := int32(0); level <= 100; level++ {
		total := experienceForLevel(level)
		gotLevel, progress := experienceLevel(total)
		if gotLevel != level || progress != 0 {
			t.Fatalf("level %d → total %d → (%d, %v)", level, total, gotLevel, progress)
		}
	}
	if experienceForLevel(16) != 352 || experienceForLevel(31) != 1507 || experienceForLevel(32) != 1628 {
		t.Fatalf("临界值：(16)=%d (31)=%d (32)=%d", experienceForLevel(16), experienceForLevel(31), experienceForLevel(32))
	}
}

func TestGiveExperienceLevels(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "XpUser")
	defer conn.Close()
	player := activeSession(t, instance, "XpUser")

	player.addExperience(experienceForLevel(10))
	bar, level, total := player.giveExperienceLevels(-1)
	if level != 9 || bar != 0 || total != experienceForLevel(9) {
		t.Fatalf("扣 1 级 = (bar %v, level %d, total %d)", bar, level, total)
	}
	bar, level, total = player.giveExperienceLevels(-100)
	if level != 0 || bar != 0 || total != 0 {
		t.Fatalf("扣过量的等级应清零：(bar %v, level %d, total %d)", bar, level, total)
	}
}

func TestGiveExperienceLevelsKeepsProgress(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "ProgressUser")
	defer conn.Close()
	player := activeSession(t, instance, "ProgressUser")

	// 10 级进度约 50%（10 级升级需 27 点）。
	player.addExperience(experienceForLevel(10) + 13)
	_, level, _ := player.experienceStatus()
	if level != 10 {
		t.Fatalf("准备状态应为 10 级，得到 %d", level)
	}
	bar, level, _ := player.giveExperienceLevels(-1)
	if level != 9 {
		t.Fatalf("扣 1 级后应为 9 级，得到 %d", level)
	}
	// 进度保留（近似：13/27 ≈ 0.4815 → 9 级段 0.4815*25 ≈ 12/25 = 0.48）。
	if bar < 0.44 || bar > 0.52 {
		t.Fatalf("进度应大致保留：bar = %v", bar)
	}
}

func TestParseRenameItem(t *testing.T) {
	packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDRenameItem)
	packet = protocol.AppendVarInt(packet, int32(len("Excalibur")))
	packet = append(packet, "Excalibur"...)
	name, err := protocol.ParseRenameItem(packet)
	if err != nil || name != "Excalibur" {
		t.Fatalf("解析 = %q, %v", name, err)
	}
	if _, err := protocol.ParseRenameItem(protocol.AppendVarInt(nil, 0x10)); err == nil {
		t.Fatal("错误包 ID 应报错")
	}
	truncated := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDRenameItem)
	truncated = protocol.AppendVarInt(truncated, 100)
	truncated = append(truncated, "abc"...)
	if _, err := protocol.ParseRenameItem(truncated); err == nil {
		t.Fatal("截断的字符串应报错")
	}
}

// ---------------------------------------------------------------------------
// 端到端：打开铁砧、改名、取走

// parseSlotWithComponents 解析槽位数据（支持 damage/custom_name/repair_cost
// 组件），返回堆栈与新的偏移。
func parseSlotWithComponents(t *testing.T, payload []byte, offset int) (item.Stack, int) {
	t.Helper()
	count, size, err := protocol.DecodeVarInt(payload[offset:])
	if err != nil {
		t.Fatal(err)
	}
	offset += size
	if count <= 0 {
		return item.Empty(), offset
	}
	itemID, size, err := protocol.DecodeVarInt(payload[offset:])
	if err != nil {
		t.Fatal(err)
	}
	offset += size
	added, size, err := protocol.DecodeVarInt(payload[offset:])
	if err != nil {
		t.Fatal(err)
	}
	offset += size
	removed, size, err := protocol.DecodeVarInt(payload[offset:])
	if err != nil {
		t.Fatal(err)
	}
	offset += size
	if removed != 0 {
		t.Fatalf("测试不支持移除组件（removed=%d）", removed)
	}
	stack := item.Stack{ItemID: itemID, Count: count}
	for i := int32(0); i < added; i++ {
		componentID, size, err := protocol.DecodeVarInt(payload[offset:])
		if err != nil {
			t.Fatal(err)
		}
		offset += size
		switch componentID {
		case 3: // damage
			value, size, err := protocol.DecodeVarInt(payload[offset:])
			if err != nil {
				t.Fatal(err)
			}
			offset += size
			stack.Damage = value
		case 6: // custom_name（NBT String Tag）
			if payload[offset] != 0x08 {
				t.Fatalf("custom_name 应为 NBT String Tag，得到标签 %#x", payload[offset])
			}
			offset++
			length := int(payload[offset])<<8 | int(payload[offset+1])
			offset += 2
			if len(payload)-offset < length {
				t.Fatal("custom_name 截断")
			}
			stack.CustomName = string(payload[offset : offset+length])
			offset += length
		case 13, 41: // enchantments / stored_enchantments：编码相同
			// VarInt 数量 + 每项 (VarInt(id+1), VarInt(level))
			count, size, err := protocol.DecodeVarInt(payload[offset:])
			if err != nil {
				t.Fatal(err)
			}
			offset += size
			for j := int32(0); j < count; j++ {
				enchID, size, err := protocol.DecodeVarInt(payload[offset:])
				if err != nil {
					t.Fatal(err)
				}
				offset += size
				level, size, err := protocol.DecodeVarInt(payload[offset:])
				if err != nil {
					t.Fatal(err)
				}
				offset += size
				stack.Enchantments = append(stack.Enchantments,
					item.EnchantmentEntry{ID: enchID - 1, Level: level})
			}
		case 19: // repair_cost
			value, size, err := protocol.DecodeVarInt(payload[offset:])
			if err != nil {
				t.Fatal(err)
			}
			offset += size
			stack.RepairCost = value
		case 49: // potion_contents：测试不检查药水内容（避免错误解析）
			t.Fatal("测试不支持解析 potion_contents 组件")
		default:
			t.Fatalf("未预期的组件 ID %d", componentID)
		}
	}
	return stack, offset
}

// setContainerSlotStack 解析 Set Container Slot 包（含组件）。
func setContainerSlotStack(t *testing.T, payload []byte) (windowID, stateID int32, slot int16, stack item.Stack) {
	t.Helper()
	_, offset, err := protocol.DecodeVarInt(payload)
	if err != nil {
		t.Fatal(err)
	}
	windowID, size, err := protocol.DecodeVarInt(payload[offset:])
	if err != nil {
		t.Fatal(err)
	}
	offset += size
	stateID, size, err = protocol.DecodeVarInt(payload[offset:])
	if err != nil {
		t.Fatal(err)
	}
	offset += size
	if len(payload)-offset < 2 {
		t.Fatal("截断的 Set Container Slot 包")
	}
	slot = int16(uint16(payload[offset])<<8 | uint16(payload[offset+1]))
	offset += 2
	stack, _ = parseSlotWithComponents(t, payload, offset)
	return windowID, stateID, slot, stack
}

// parseSetExperience 解析 Set Experience 包。
func parseSetExperience(t *testing.T, payload []byte) (bar float32, level, total int32) {
	t.Helper()
	_, offset, err := protocol.DecodeVarInt(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload)-offset < 4 {
		t.Fatal("截断的 Set Experience 包")
	}
	bits := uint32(payload[offset])<<24 | uint32(payload[offset+1])<<16 | uint32(payload[offset+2])<<8 | uint32(payload[offset+3])
	bar = math.Float32frombits(bits)
	offset += 4
	level, size, err := protocol.DecodeVarInt(payload[offset:])
	if err != nil {
		t.Fatal(err)
	}
	offset += size
	total, _, err = protocol.DecodeVarInt(payload[offset:])
	if err != nil {
		t.Fatal(err)
	}
	return bar, level, total
}

// sendRenameItem 发送 Rename Item 包（铁砧重命名输入）。
func sendRenameItem(t *testing.T, conn net.Conn, name string) {
	t.Helper()
	packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDRenameItem)
	packet = protocol.AppendVarInt(packet, int32(len(name)))
	packet = append(packet, name...)
	if err := protocol.WritePacketWithCompression(conn, packet, compressionThreshold); err != nil {
		t.Fatal(err)
	}
}

// TestAnvilWindowRenameAndTake 验证铁砧窗口主流程：
// 打开 → 放入受损物品 → 改名（结果与成本同步）→ 取走（扣等级、清空输入）。
func TestAnvilWindowRenameAndTake(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "AnvilUser")
	defer conn.Close()

	ax, ay, az := chestPositions(t, instance)
	placeBlockAt(t, instance, ax, ay, az, anvilBlockName)

	sendUseItemOn(t, conn, ax, ay, az, 1)
	windowID, menuType := openScreen(t, conn)
	wantMenu, ok := registry.StaticEntryID("minecraft:menu", "minecraft:anvil")
	if !ok || menuType != wantMenu {
		t.Fatalf("菜单类型 = %d, want %d", menuType, wantMenu)
	}
	content := expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)
	gotWindow, slots := containerContent(t, content)
	if gotWindow != windowID || len(slots) != anvilSlots {
		t.Fatalf("窗口 %d 槽位数 %d, want %d/%d", gotWindow, len(slots), windowID, anvilSlots)
	}

	// 服务器侧放入受损钻石剑（快捷栏 0），玩家经验设为 10 级。
	player := activeSession(t, instance, "AnvilUser")
	sword := testStack(t, "minecraft:diamond_sword", 1)
	sword.Damage = 100
	player.inventory.Set(item.SlotHotbarStart, sword)
	player.giveExperienceLevels(10)

	// 点击窗口槽 30（快捷栏 0）拾取到光标，再点击输入槽 0 放入。
	sendContainerClick(t, conn, windowID, 0, 3+27, 0, protocol.ClickModePickup)
	if _, _, slot, stack := setContainerSlotStack(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)); slot != 3+27 || stack.Count != 0 {
		t.Fatalf("快捷栏应被清空：slot=%d stack=%+v", slot, stack)
	}
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)
	sendContainerClick(t, conn, windowID, 0, anvilInputSlot, 0, protocol.ClickModePickup)
	if _, _, slot, stack := setContainerSlotStack(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)); slot != anvilInputSlot || stack.Damage != 100 {
		t.Fatalf("输入槽应收到受损剑：slot=%d stack=%+v", slot, stack)
	}
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)

	// 发送改名包：成本 1、结果槽出现带 custom_name 的剑。
	sendRenameItem(t, conn, "Excalibur")
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
	if propWindow != windowID || propID != 0 || propValue != 1 {
		t.Fatalf("成本属性 = (%d, %d, %d), want (%d, 0, 1)", propWindow, propID, propValue, windowID)
	}
	if _, _, slot, stack := setContainerSlotStack(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)); slot != anvilResultSlot ||
		stack.CustomName != "Excalibur" || stack.Damage != 100 || stack.Count != 1 {
		t.Fatalf("结果槽 = slot=%d stack=%+v", slot, stack)
	}

	// 光标空时点击结果槽取走：扣 1 级、输入清空、光标获得结果。
	sendContainerClick(t, conn, windowID, 0, anvilResultSlot, 0, protocol.ClickModePickup)
	bar, level, total := parseSetExperience(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetExperience))
	if level != 9 || bar != 0 || total != experienceForLevel(9) {
		t.Fatalf("取走应扣 1 级：(bar %v, level %d, total %d)", bar, level, total)
	}
	if _, _, slot, stack := setContainerSlotStack(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)); slot != anvilInputSlot || !stack.IsEmpty() {
		t.Fatalf("输入槽应被清空：slot=%d stack=%+v", slot, stack)
	}
	if _, _, slot, stack := setContainerSlotStack(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)); slot != anvilResultSlot || !stack.IsEmpty() {
		t.Fatalf("结果槽应被清空：slot=%d stack=%+v", slot, stack)
	}
	cursor := expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)
	// 光标物品应为 Excalibur（复用组件解析）。
	_, cursorOffset, err := protocol.DecodeVarInt(cursor)
	if err != nil {
		t.Fatal(err)
	}
	cursorItem, _ := parseSlotWithComponents(t, cursor, cursorOffset)
	if cursorItem.CustomName != "Excalibur" || cursorItem.Damage != 100 {
		t.Fatalf("光标应为 Excalibur：%+v", cursorItem)
	}
}

// TestAnvilCloseReturnsInputs 验证关闭窗口时输入槽归还背包。
func TestAnvilCloseReturnsInputs(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "AnvilCloser")
	defer conn.Close()

	ax, ay, az := chestPositions(t, instance)
	placeBlockAt(t, instance, ax, ay, az, anvilBlockName)
	sendUseItemOn(t, conn, ax, ay, az, 1)
	windowID, _ := openScreen(t, conn)
	expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)

	// 服务器侧放入材料后客户端主动关闭窗口。
	player := activeSession(t, instance, "AnvilCloser")
	diamond := testStack(t, "minecraft:diamond", 3)
	player.inventory.Set(item.SlotHotbarStart, diamond)
	sendContainerClick(t, conn, windowID, 0, 3+27, 0, protocol.ClickModePickup)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)
	sendContainerClick(t, conn, windowID, 0, anvilAdditionSlot, 0, protocol.ClickModePickup)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)

	closePacket := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDContainerClose)
	closePacket = protocol.AppendVarInt(closePacket, windowID)
	if err := protocol.WritePacketWithCompression(conn, closePacket, compressionThreshold); err != nil {
		t.Fatal(err)
	}
	// 等待输入槽归还（3 个钻石回到背包）。
	waitFor(t, func() bool {
		total := int32(0)
		for slot := 0; slot < item.InventorySlots; slot++ {
			stack := player.inventory.Get(slot)
			if name, ok := registry.ItemName(stack.ItemID); ok && name == "minecraft:diamond" {
				total += stack.Count
			}
		}
		return total == 3
	}, "关闭铁砧窗口应归还输入槽物品")
}

// TestAnvilMergeEnchantments 单元验证附魔合并（对照 dhm 字节码 434-726）：
// 等级合并规则、附魔书成本减半、冲突附魔不生效与整单失效、创造模式/附魔书豁免、
// supported_items 限制（不受 primary_items 影响）与堆叠特例。
func TestAnvilMergeEnchantments(t *testing.T) {
	enchant := func(name string) int32 {
		t.Helper()
		id, ok := registry.SyncEntryID("minecraft:enchantment", name)
		if !ok {
			t.Fatalf("缺少附魔 %s", name)
		}
		return id
	}
	sharpness := enchant("minecraft:sharpness")
	smite := enchant("minecraft:smite")
	sharpnessCost, _ := registry.EnchantmentAnvilCost(sharpness)
	bookSharpness := int64(max(1, sharpnessCost/2)) // 附魔书成本减半（至少 1）

	sword := testStack(t, "minecraft:diamond_sword", 1)
	book := func(entries ...item.EnchantmentEntry) item.Stack {
		b := testStack(t, "minecraft:enchanted_book", 1)
		b.Enchantments = entries
		return b
	}

	// 锋利 I 书 → 剑：应用锋利 I，成本按书（减半）计。
	merge := anvilMergeEnchantments(sword, book(item.EnchantmentEntry{ID: sharpness, Level: 1}), true, false)
	if merge.conflictOnly || len(merge.enchantments) != 1 ||
		merge.enchantments[0] != (item.EnchantmentEntry{ID: sharpness, Level: 1}) {
		t.Fatalf("剑 + 锋利 I 书 = %+v", merge)
	}
	if merge.costDelta != bookSharpness {
		t.Fatalf("成本 = %d, want %d", merge.costDelta, bookSharpness)
	}

	// 锋利 I 剑 + 锋利 I 书：同名同级提升为 II。
	sharpSword := sword
	sharpSword.Enchantments = []item.EnchantmentEntry{{ID: sharpness, Level: 1}}
	merge = anvilMergeEnchantments(sharpSword, book(item.EnchantmentEntry{ID: sharpness, Level: 1}), true, false)
	if merge.conflictOnly || len(merge.enchantments) != 1 || merge.enchantments[0].Level != 2 {
		t.Fatalf("锋利 I + 锋利 I = %+v", merge)
	}
	if merge.costDelta != bookSharpness*2 {
		t.Fatalf("成本 = %d, want %d", merge.costDelta, bookSharpness*2)
	}

	// 锋利 III 剑 + 锋利 I 书：保留更高等级 III（成本按 III 计）。
	sharpSword.Enchantments = []item.EnchantmentEntry{{ID: sharpness, Level: 3}}
	merge = anvilMergeEnchantments(sharpSword, book(item.EnchantmentEntry{ID: sharpness, Level: 1}), true, false)
	if merge.conflictOnly || merge.enchantments[0].Level != 3 {
		t.Fatalf("锋利 III + 锋利 I = %+v", merge)
	}
	if merge.costDelta != bookSharpness*3 {
		t.Fatalf("成本 = %d, want %d", merge.costDelta, bookSharpness*3)
	}

	// 锋利 V 剑 + 锋利 V 书：提升被钳制回最大等级 V。
	sharpSword.Enchantments = []item.EnchantmentEntry{{ID: sharpness, Level: 5}}
	merge = anvilMergeEnchantments(sharpSword, book(item.EnchantmentEntry{ID: sharpness, Level: 5}), true, false)
	if merge.conflictOnly || len(merge.enchantments) != 1 || merge.enchantments[0].Level != 5 {
		t.Fatalf("锋利 V + 锋利 V = %+v", merge)
	}
	if merge.costDelta != bookSharpness*5 {
		t.Fatalf("成本 = %d, want %d", merge.costDelta, bookSharpness*5)
	}

	// 冲突（锋利 vs 亡灵杀手）且无其他提示：整单无效。
	merge = anvilMergeEnchantments(sharpSword, book(item.EnchantmentEntry{ID: smite, Level: 1}), true, false)
	if !merge.conflictOnly || len(merge.enchantments) != 0 {
		t.Fatalf("全冲突应整单无效：%+v", merge)
	}

	// 部分冲突：亡灵杀手被丢弃（追加 1 点成本），锋利正常升级为 II。
	partial := sword
	partial.Enchantments = []item.EnchantmentEntry{{ID: sharpness, Level: 1}}
	merge = anvilMergeEnchantments(partial, book(
		item.EnchantmentEntry{ID: smite, Level: 3},
		item.EnchantmentEntry{ID: sharpness, Level: 1},
	), true, false)
	if merge.conflictOnly || len(merge.enchantments) != 1 ||
		merge.enchantments[0] != (item.EnchantmentEntry{ID: sharpness, Level: 2}) {
		t.Fatalf("部分冲突 = %+v", merge)
	}
	if merge.costDelta != 1+bookSharpness*2 {
		t.Fatalf("部分冲突成本 = %d, want %d", merge.costDelta, 1+bookSharpness*2)
	}

	// 不适用物品：锋利书不能用于胸甲（supported_items 不含胸甲）→ 整单无效；
	// 创造模式跳过 supported 限制。
	chest := testStack(t, "minecraft:diamond_chestplate", 1)
	merge = anvilMergeEnchantments(chest, book(item.EnchantmentEntry{ID: sharpness, Level: 1}), true, false)
	if !merge.conflictOnly {
		t.Fatalf("胸甲 + 锋利书应无效：%+v", merge)
	}
	merge = anvilMergeEnchantments(chest, book(item.EnchantmentEntry{ID: sharpness, Level: 1}), true, true)
	if merge.conflictOnly || len(merge.enchantments) != 1 {
		t.Fatalf("创造模式胸甲 + 锋利书应应用：%+v", merge)
	}

	// 铁砧只受 supported_items 限制（不受 primary_items）：斧可以附锋利
	// （supported_items 含斧，primary_items 仅限制附魔台）。
	axe := testStack(t, "minecraft:diamond_axe", 1)
	merge = anvilMergeEnchantments(axe, book(item.EnchantmentEntry{ID: sharpness, Level: 1}), true, false)
	if merge.conflictOnly || len(merge.enchantments) != 1 {
		t.Fatalf("斧 + 锋利书应应用（铁砧不受 primary_items 限制）：%+v", merge)
	}

	// 附魔书 + 附魔书：输入是附魔书时豁免 supported（书可承载任意附魔）；
	// 不同附魔（锋利 + 耐久）互不冲突，全部保留。
	unbreaking := enchant("minecraft:unbreaking")
	unbreakingCost, _ := registry.EnchantmentAnvilCost(unbreaking)
	bookUnbreaking := int64(max(1, unbreakingCost/2))
	enchantedBook := testStack(t, "minecraft:enchanted_book", 1)
	enchantedBook.Enchantments = []item.EnchantmentEntry{{ID: sharpness, Level: 1}}
	merge = anvilMergeEnchantments(enchantedBook, book(item.EnchantmentEntry{ID: unbreaking, Level: 2}), true, false)
	if merge.conflictOnly || len(merge.enchantments) != 2 {
		t.Fatalf("附魔书 + 附魔书 = %+v", merge)
	}
	// 仅添加物的附魔计入成本（输入的锋利 I 不计）。
	if merge.costDelta != bookUnbreaking*2 {
		t.Fatalf("书 + 书成本 = %d, want %d", merge.costDelta, bookUnbreaking*2)
	}

	// 输入堆叠数大于 1：成本直接为 40（原版特例）。
	stacked := sword
	stacked.Count = 2
	merge = anvilMergeEnchantments(stacked, book(item.EnchantmentEntry{ID: sharpness, Level: 1}), true, false)
	if merge.costDelta != 40 {
		t.Fatalf("堆叠输入成本 = %d, want 40", merge.costDelta)
	}
}

// TestComputeAnvilResultEnchantmentMerge 验证铁砧整体计算结果中的附魔合并接线：
// 附魔生效、成本与累积惩罚递增；全部冲突时结果为空、成本 0。
func TestComputeAnvilResultEnchantmentMerge(t *testing.T) {
	enchant := func(name string) int32 {
		t.Helper()
		id, ok := registry.SyncEntryID("minecraft:enchantment", name)
		if !ok {
			t.Fatalf("缺少附魔 %s", name)
		}
		return id
	}
	sharpness := enchant("minecraft:sharpness")
	smite := enchant("minecraft:smite")

	sword := testStack(t, "minecraft:diamond_sword", 1)
	sword.Damage = 100
	book := testStack(t, "minecraft:enchanted_book", 1)
	book.Enchantments = []item.EnchantmentEntry{{ID: sharpness, Level: 1}}
	res := computeAnvilResult(sword, book, "", false, false)
	sharpnessCost, _ := registry.EnchantmentAnvilCost(sharpness)
	if want := int32(max(1, sharpnessCost/2)); res.cost != want {
		t.Fatalf("成本 = %d, want %d", res.cost, want)
	}
	if res.result.IsEmpty() || res.result.Damage != 100 || res.result.RepairCost != 1 ||
		len(res.result.Enchantments) != 1 ||
		res.result.Enchantments[0] != (item.EnchantmentEntry{ID: sharpness, Level: 1}) {
		t.Fatalf("结果 = %+v", res.result)
	}

	// 全部冲突：锋利剑 + 亡灵杀手书 → 空结果、成本 0。
	conflictSword := sword
	conflictSword.Enchantments = []item.EnchantmentEntry{{ID: sharpness, Level: 1}}
	smiteBook := testStack(t, "minecraft:enchanted_book", 1)
	smiteBook.Enchantments = []item.EnchantmentEntry{{ID: smite, Level: 1}}
	res = computeAnvilResult(conflictSword, smiteBook, "", false, false)
	if !res.result.IsEmpty() || res.cost != 0 {
		t.Fatalf("冲突应无结果，得到 cost=%d result=%+v", res.cost, res.result)
	}
}

// TestAnvilWindowEnchantMerge 验证铁砧附魔合并的窗口全流程：
// 放入受损剑与锋利 I 附魔书 → 成本同步为 1、结果槽出现带附魔的剑 → 取走扣 1 级。
func TestAnvilWindowEnchantMerge(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "AnvilEnchanter")
	defer conn.Close()

	ax, ay, az := chestPositions(t, instance)
	placeBlockAt(t, instance, ax, ay, az, anvilBlockName)
	sendUseItemOn(t, conn, ax, ay, az, 1)
	windowID, _ := openScreen(t, conn)
	expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)

	sharpness, ok := registry.SyncEntryID("minecraft:enchantment", "minecraft:sharpness")
	if !ok {
		t.Fatal("缺少 sharpness 附魔")
	}
	player := activeSession(t, instance, "AnvilEnchanter")
	sword := testStack(t, "minecraft:diamond_sword", 1)
	sword.Damage = 100
	book := testStack(t, "minecraft:enchanted_book", 1)
	book.Enchantments = []item.EnchantmentEntry{{ID: sharpness, Level: 1}}
	player.inventory.Set(item.SlotHotbarStart, sword)
	player.inventory.Set(item.SlotHotbarStart+1, book)
	player.giveExperienceLevels(10)

	// 快捷栏 0（剑）→ 输入槽 0。
	sendContainerClick(t, conn, windowID, 0, 3+27, 0, protocol.ClickModePickup)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)
	sendContainerClick(t, conn, windowID, 0, anvilInputSlot, 0, protocol.ClickModePickup)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)

	// 快捷栏 1（附魔书）→ 添加槽 1；成本属性同步为 1。
	sendContainerClick(t, conn, windowID, 0, 3+28, 0, protocol.ClickModePickup)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)
	sendContainerClick(t, conn, windowID, 0, anvilAdditionSlot, 0, protocol.ClickModePickup)
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
	if propWindow != windowID || propID != 0 || propValue != 1 {
		t.Fatalf("成本属性 = (%d, %d, %d), want (%d, 0, 1)", propWindow, propID, propValue, windowID)
	}
	// 添加槽收到附魔书（stored_enchantments 组件）。
	if _, _, slot, stack := setContainerSlotStack(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)); slot != anvilAdditionSlot ||
		len(stack.Enchantments) != 1 || stack.Enchantments[0].Level != 1 {
		t.Fatalf("添加槽 = slot=%d stack=%+v", slot, stack)
	}
	// 结果槽出现带附魔与累积惩罚的剑。
	if _, _, slot, stack := setContainerSlotStack(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)); slot != anvilResultSlot ||
		len(stack.Enchantments) != 1 || stack.Enchantments[0].ID != sharpness || stack.Enchantments[0].Level != 1 ||
		stack.Damage != 100 || stack.RepairCost != 1 {
		t.Fatalf("结果槽 = slot=%d stack=%+v", slot, stack)
	}
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)

	// 光标空时点击结果槽取走：扣 1 级、输入清空、光标获得附魔剑。
	sendContainerClick(t, conn, windowID, 0, anvilResultSlot, 0, protocol.ClickModePickup)
	bar, level, total := parseSetExperience(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetExperience))
	if level != 9 || bar != 0 || total != experienceForLevel(9) {
		t.Fatalf("取走应扣 1 级：(bar %v, level %d, total %d)", bar, level, total)
	}
	// 成本回落为 0，三个容器槽清空（输入槽 / 添加槽 / 结果槽按序同步）。
	zeroProp := expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerProperty)
	if len(zeroProp) < 2 || zeroProp[len(zeroProp)-2] != 0 || zeroProp[len(zeroProp)-1] != 0 {
		t.Fatalf("取走后成本应回落 0：% x", zeroProp)
	}
	for want := int16(anvilInputSlot); want <= int16(anvilResultSlot); want++ {
		if _, _, slot, stack := setContainerSlotStack(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)); slot != want || !stack.IsEmpty() {
			t.Fatalf("槽 %d 应清空：slot=%d stack=%+v", want, slot, stack)
		}
	}
	cursor := expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)
	_, cursorOffset, err := protocol.DecodeVarInt(cursor)
	if err != nil {
		t.Fatal(err)
	}
	cursorItem, _ := parseSlotWithComponents(t, cursor, cursorOffset)
	if cursorItem.Damage != 100 || len(cursorItem.Enchantments) != 1 ||
		cursorItem.Enchantments[0].ID != sharpness || cursorItem.Enchantments[0].Level != 1 ||
		cursorItem.RepairCost != 1 {
		t.Fatalf("光标应为附魔剑：%+v", cursorItem)
	}
}
