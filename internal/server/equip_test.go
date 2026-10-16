// 玩家装备槽（盔甲与副手）行为测试。
//
// 对照对象是 Minecraft 1.21.11 的 InventoryMenu / ArmorSlot / LivingEntity
// （getEquipmentSlotForItem、canUseSlot）与 AbstractContainerMenu#doClick：
//   - 盔甲槽只接受 minecraft:equippable 组件槽位匹配的物品（slot 为
//     head/chest/legs/feet 且 allowed_entities 允许玩家）；
//   - 盔甲槽的堆叠上限为 1（ArmorSlot.getMaxStackSize），整栈点到空槽时只放入 1 个；
//   - 槽位已有不同物品时必须整栈不超过槽位上限才允许交换；
//   - Shift 点击时，可装备物品若对应装备槽为空则只尝试放入那一个槽位，
//     否则回退到主背包 ↔ 快捷栏（副手槽同规则）。
package server

import (
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
)

// armorMenuSlot 返回玩家物品栏窗口中盔甲槽对应的物品栏槽位（5→39 头盔 … 8→36 靴子）。
func armorMenuSlot(menu int) int { return 44 - menu }

// TestPlayerEquippableData 校验生成的装备数据（minecraft:equippable 组件提取）。
func TestPlayerEquippableData(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"minecraft:carved_pumpkin", "head"},
		{"minecraft:creeper_head", "head"},
		{"minecraft:diamond_helmet", "head"},
		{"minecraft:netherite_chestplate", "chest"},
		{"minecraft:elytra", "chest"},
		{"minecraft:leather_leggings", "legs"},
		{"minecraft:iron_boots", "feet"},
		{"minecraft:shield", "offhand"},
		{"minecraft:stone", ""},
		{"minecraft:saddle", ""},        // slot 为 saddle，玩家不使用该槽位
		{"minecraft:white_harness", ""}, // slot 为 body 且 allowed_entities 不含玩家
	}
	for _, tc := range cases {
		id := mustItemID(t, tc.name)
		if got := registry.PlayerEquippableSlot(id); got != tc.want {
			t.Errorf("PlayerEquippableSlot(%s) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestRightClickSwapsDifferentItem 验证右键点击不同物品的槽位时按原版整栈交换
// （要求携带数量不超过槽位上限）。官方服务端实测：1 个金锭右键点击泥土槽会互换。
func TestRightClickSwapsDifferentItem(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:dirt*32", "minecraft:gold_ingot"}
	instance, conn := joinServer(t, cfg, "RightSwap")

	dirt := mustItemID(t, "minecraft:dirt")
	gold := mustItemID(t, "minecraft:gold_ingot")

	// 把泥土放进主背包槽 9，再拿起金锭。
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 36, 0, protocol.ClickModePickup)
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 9, 0, protocol.ClickModePickup)
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 37, 0, protocol.ClickModePickup)
	// 右键点击泥土槽 → 交换。
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 9, 1, protocol.ClickModePickup)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")

	player := activeSession(t, instance, "RightSwap")
	if stack := player.inventory.Get(9); stack.ItemID != gold || stack.Count != 1 {
		t.Fatalf("槽位 9 = %+v, want 1 个金锭（交换）", stack)
	}
	if cursor := player.getCursor(); cursor.ItemID != dirt || cursor.Count != 32 {
		t.Fatalf("光标 = %+v, want 32 个泥土（交换到光标）", cursor)
	}
}

// TestArmorSlotRejectsWrongItem 验证盔甲槽拒绝非对应部位物品：
// 石头的装备槽是 mainhand，点到空头盔槽应无操作（原版 ArmorSlot.mayPlace）。
func TestArmorSlotRejectsWrongItem(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:stone*64"}
	instance, conn := joinServer(t, cfg, "ArmorReject")

	// 拿起快捷栏 0（菜单槽位 36）的石头，点到空头盔槽（菜单槽位 5）。
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 36, 0, protocol.ClickModePickup)
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 5, 0, protocol.ClickModePickup)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")

	player := activeSession(t, instance, "ArmorReject")
	if stack := player.inventory.Get(armorMenuSlot(5)); !stack.IsEmpty() {
		t.Fatalf("头盔槽不应接受石头，得到 %+v", stack)
	}
	if cursor := player.getCursor(); cursor.Count != 64 {
		t.Fatalf("拒绝放入时光标应保持 64 石头，得到 %+v", cursor)
	}
}

// TestArmorSlotPartialInsert 验证盔甲槽上限为 1：64 个雕刻南瓜点到空头盔槽
// 只放入 1 个（原版 Slot.safeInsert 受 ArmorSlot.getMaxStackSize=1 限制）。
func TestArmorSlotPartialInsert(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:carved_pumpkin*64"}
	instance, conn := joinServer(t, cfg, "ArmorPartial")

	pumpkin := mustItemID(t, "minecraft:carved_pumpkin")
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 36, 0, protocol.ClickModePickup)
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 5, 0, protocol.ClickModePickup)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")

	player := activeSession(t, instance, "ArmorPartial")
	helmet := player.inventory.Get(armorMenuSlot(5))
	if helmet.ItemID != pumpkin || helmet.Count != 1 {
		t.Fatalf("头盔槽 = %+v, want 1 个雕刻南瓜", helmet)
	}
	cursor := player.getCursor()
	if cursor.ItemID != pumpkin || cursor.Count != 63 {
		t.Fatalf("光标 = %+v, want 63 个雕刻南瓜", cursor)
	}

	// 再次点击（同类、槽位已满）：无操作。
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 5, 0, protocol.ClickModePickup)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")
	player = activeSession(t, instance, "ArmorPartial")
	if cursor := player.getCursor(); cursor.Count != 63 {
		t.Fatalf("槽位已满时光标应保持 63，得到 %+v", cursor)
	}
}

// TestArmorSlotSwapNeedsCountFit 验证盔甲槽已有不同物品时，整栈超过槽位上限
// 不能交换（原版 doClick 的 `carried.count <= slot.getMaxStackSize` 检查）。
func TestArmorSlotSwapNeedsCountFit(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:leather_helmet", "minecraft:carved_pumpkin*64"}
	instance, conn := joinServer(t, cfg, "ArmorSwap")

	helmet := mustItemID(t, "minecraft:leather_helmet")
	pumpkin := mustItemID(t, "minecraft:carved_pumpkin")

	// 把皮革帽子装备到头盔槽。
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 36, 0, protocol.ClickModePickup)
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 5, 0, protocol.ClickModePickup)
	// 拿起 64 个南瓜点到头盔槽（不同物品、数量超过上限 1）→ 应无操作。
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 37, 0, protocol.ClickModePickup)
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 5, 0, protocol.ClickModePickup)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")

	player := activeSession(t, instance, "ArmorSwap")
	if stack := player.inventory.Get(armorMenuSlot(5)); stack.ItemID != helmet || stack.Count != 1 {
		t.Fatalf("头盔槽应保持 1 个皮革帽子，得到 %+v", stack)
	}
	if cursor := player.getCursor(); cursor.ItemID != pumpkin || cursor.Count != 64 {
		t.Fatalf("光标应保持 64 个南瓜，得到 %+v", cursor)
	}
}

// TestShiftClickEquipsArmor 验证 Shift 点击背包/快捷栏中的可装备物品会放入
// 对应空盔甲槽，且只放入 1 个（原版 InventoryMenu.quickMoveStack 装备分支）。
func TestShiftClickEquipsArmor(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:carved_pumpkin*64"}
	instance, conn := joinServer(t, cfg, "EquipShift")

	pumpkin := mustItemID(t, "minecraft:carved_pumpkin")
	// Shift 点击快捷栏 0（菜单槽位 36）→ 头盔槽（菜单槽位 5）取 1 个。
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 36, 0, protocol.ClickModeQuickMove)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")

	player := activeSession(t, instance, "EquipShift")
	helmet := player.inventory.Get(armorMenuSlot(5))
	if helmet.ItemID != pumpkin || helmet.Count != 1 {
		t.Fatalf("头盔槽 = %+v, want 1 个雕刻南瓜", helmet)
	}
	// 装备后槽位仍有同类物品，原版 quickMoveStack 会被重复调用：剩余 63 个
	// 回退到“主背包 ↔ 快捷栏”分支，从快捷栏 0 移入主背包槽 9。
	if rest := player.inventory.Get(9); rest.ItemID != pumpkin || rest.Count != 63 {
		t.Fatalf("剩余 63 个南瓜应移入主背包槽 9，得到 %+v", rest)
	}
	if rest := player.inventory.Get(item.SlotHotbarStart); !rest.IsEmpty() {
		t.Fatalf("快捷栏 0 应被清空，得到 %+v", rest)
	}
}

// TestShiftClickEquipsOffhand 验证 Shift 点击盾牌进入副手槽（原版装备分支）。
func TestShiftClickEquipsOffhand(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:shield"}
	instance, conn := joinServer(t, cfg, "EquipOffhand")

	shield := mustItemID(t, "minecraft:shield")
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 36, 0, protocol.ClickModeQuickMove)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")

	player := activeSession(t, instance, "EquipOffhand")
	offhand := player.inventory.Get(item.SlotOffhand)
	if offhand.ItemID != shield || offhand.Count != 1 {
		t.Fatalf("副手槽 = %+v, want 1 个盾牌", offhand)
	}
	if rest := player.inventory.Get(item.SlotHotbarStart); !rest.IsEmpty() {
		t.Fatalf("盾牌应完全移入副手，快捷栏 0 得到 %+v", rest)
	}
}

// TestShiftClickEquipFallbackToInventory 验证装备槽被占用时 Shift 点击装备物品
// 回退到主背包 ↔ 快捷栏（原版装备分支要求目标槽为空）。
func TestShiftClickEquipFallbackToInventory(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:leather_helmet", "minecraft:carved_pumpkin*64"}
	instance, conn := joinServer(t, cfg, "EquipFallback")

	pumpkin := mustItemID(t, "minecraft:carved_pumpkin")
	// 装备皮革帽子。
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 36, 0, protocol.ClickModePickup)
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 5, 0, protocol.ClickModePickup)
	// Shift 点击快捷栏 1 的南瓜（头盔槽被占用）→ 移入主背包（原版 move(9,36,false)）。
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 37, 0, protocol.ClickModeQuickMove)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")

	player := activeSession(t, instance, "EquipFallback")
	if helmet := player.inventory.Get(armorMenuSlot(5)); helmet.Count != 1 {
		t.Fatalf("头盔槽应保持 1 个帽子，得到 %+v", helmet)
	}
	if main := player.inventory.Get(9); main.ItemID != pumpkin || main.Count != 64 {
		t.Fatalf("头盔槽被占用时南瓜应移入主背包槽 9，得到 %+v", main)
	}
	if rest := player.inventory.Get(item.SlotHotbarStart + 1); !rest.IsEmpty() {
		t.Fatalf("快捷栏 1 应被清空，得到 %+v", rest)
	}
}

// TestShiftClickUnequipArmor 验证 Shift 点击盔甲槽把装备移回物品栏。
func TestShiftClickUnequipArmor(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:iron_helmet"}
	instance, conn := joinServer(t, cfg, "EquipOut")

	helmet := mustItemID(t, "minecraft:iron_helmet")
	// 先装备：Shift 点击 → 头盔槽。
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 36, 0, protocol.ClickModeQuickMove)
	// 再 Shift 点击头盔槽（菜单槽位 5）→ 回到物品栏。
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 5, 0, protocol.ClickModeQuickMove)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")

	player := activeSession(t, instance, "EquipOut")
	if slot := player.inventory.Get(armorMenuSlot(5)); !slot.IsEmpty() {
		t.Fatalf("头盔槽应被清空，得到 %+v", slot)
	}
	found := false
	for i := 0; i < 36; i++ {
		if stack := player.inventory.Get(i); stack.ItemID == helmet {
			found = true
		}
	}
	if !found {
		t.Fatal("取下的头盔应回到物品栏")
	}
}

// TestQuickCraftArmorSlotRejected 验证拖拽分发不会把非装备物品放进盔甲槽
// （拖拽“经过”阶段同样走 Slot.mayPlace 校验）。
func TestQuickCraftArmorSlotRejected(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:stone*32"}
	instance, conn := joinServer(t, cfg, "DragArmor")

	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 36, 0, protocol.ClickModePickup)
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, -999, 0, protocol.ClickModeQuickCraft)
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 5, 1, protocol.ClickModeQuickCraft)
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, -999, 2, protocol.ClickModeQuickCraft)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")

	player := activeSession(t, instance, "DragArmor")
	if stack := player.inventory.Get(armorMenuSlot(5)); !stack.IsEmpty() {
		t.Fatalf("拖拽不应把石头放进头盔槽，得到 %+v", stack)
	}
	if cursor := player.getCursor(); cursor.Count != 32 {
		t.Fatalf("被拒绝时光标应保持 32 石头，得到 %+v", cursor)
	}
}
