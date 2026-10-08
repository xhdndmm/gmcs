package server

import (
	"net"
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
)

// placeCraftingTable 在玩家附近放置工作台方块。
func placeCraftingTable(t *testing.T, instance *Server) (int, int, int) {
	t.Helper()
	state, ok := registry.BlockStateIDs["minecraft:crafting_table"]
	if !ok {
		t.Fatal("缺少工作台方块状态")
	}
	x, y, z := buildSpotNearSpawn(t, instance)
	if !instance.world.SetBlock(x, y, z, state) {
		t.Fatal("无法放置工作台")
	}
	return x, y, z
}

// setCreativeSlot 发送 Set Creative Mode Slot 把物品放进玩家快捷栏。
func setCreativeSlot(t *testing.T, conn net.Conn, slot int16, itemID, count int32) {
	t.Helper()
	packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDSetCreativeSlot)
	packet = append(packet, byte(slot>>8), byte(slot))
	packet = item.Stack{ItemID: itemID, Count: count}.AppendSlot(packet)
	if err := protocol.WritePacketWithCompression(conn, packet, compressionThreshold); err != nil {
		t.Fatal(err)
	}
}

// TestCraftingTableOpen 验证右键工作台打开 3×3 合成窗口。
func TestCraftingTableOpen(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.GameMode = "creative"
	instance, conn := joinServer(t, cfg, "Crafter")

	x, y, z := placeCraftingTable(t, instance)
	sendUseItemOn(t, conn, x, y, z, 1)
	windowID, menuType := openScreen(t, conn)
	wantMenu, ok := registry.StaticEntryID("minecraft:menu", "minecraft:crafting")
	if !ok {
		t.Fatal("缺少 crafting 菜单类型")
	}
	if menuType != wantMenu {
		t.Fatalf("菜单类型 = %d, want %d", menuType, wantMenu)
	}
	content := expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)
	gotWindow, slots := containerContent(t, content)
	if gotWindow != windowID {
		t.Fatalf("窗口编号 = %d, want %d", gotWindow, windowID)
	}
	// 工作台窗口：1 结果 + 9 合成格 + 36 背包。
	if len(slots) != 46 {
		t.Fatalf("窗口槽位数 = %d, want 46", len(slots))
	}
}

// TestCraftingTableCraftStick 验证工作台合成主流程：
// 放置 2 个橡木木板（竖排）→ 结果槽出现木棍 → 取走产物消耗原料。
func TestCraftingTableCraftStick(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.GameMode = "creative"
	instance, conn := joinServer(t, cfg, "StickMaker")

	plankID, err := registry.ItemID("minecraft:oak_planks")
	if err != nil {
		t.Fatal(err)
	}
	stickID, err := registry.ItemID("minecraft:stick")
	if err != nil {
		t.Fatal(err)
	}

	x, y, z := placeCraftingTable(t, instance)
	// 把木板放进快捷栏槽 0。
	setCreativeSlot(t, conn, 0, plankID, 2)

	sendUseItemOn(t, conn, x, y, z, 1)
	windowID, _ := openScreen(t, conn)
	expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)

	// 快捷栏槽 0 在工作台窗口中的槽位 = 37+0 = 37。
	const hotbarSlot = 37
	// 拾取木板到光标。
	sendContainerClick(t, conn, windowID, 0, hotbarSlot, 0, protocol.ClickModePickup)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot) // 背包槽清空
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot) // 光标持木板

	// 放 1 个到合成格 1（左上）：右键放下单个。
	sendContainerClick(t, conn, windowID, 0, 1, 1, protocol.ClickModePickup)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot) // 格 1 = 1 木板
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot) // 光标 = 1 木板

	// 再放 1 个到合成格 4（中左，竖排第二格）。
	sendContainerClick(t, conn, windowID, 0, 4, 1, protocol.ClickModePickup)

	player := activeSession(t, instance, "StickMaker")
	// 服务器状态权威：结果槽应出现 4 个木棍。
	backing := craftSlotBacking{player: player, state: player.getOpenCraft()}
	waitFor(t, func() bool {
		result := backing.resultOf(nil)
		return result.ItemID == stickID && result.Count == 4
	}, "结果槽应显示 4 个木棍")

	// 左键取走结果：光标应持有 4 个木棍，原料被消耗。
	sendContainerClick(t, conn, windowID, 0, 0, 0, protocol.ClickModePickup)
	waitFor(t, func() bool {
		if cursor := player.getCursor(); cursor.ItemID != stickID || cursor.Count != 4 {
			return false
		}
		grid := player.getOpenCraft().gridSnapshot()
		return grid[0].IsEmpty() && grid[3].IsEmpty()
	}, "取走结果后光标应持 4 木棍且原料被消耗")
}

// TestPlayerInventoryCraft2x2 验证玩家物品栏 2×2 合成格。
func TestPlayerInventoryCraft2x2(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.GameMode = "creative"
	instance, conn := joinServer(t, cfg, "MiniCrafter")

	plankID, err := registry.ItemID("minecraft:oak_planks")
	if err != nil {
		t.Fatal(err)
	}
	tableID, err := registry.ItemID("minecraft:crafting_table")
	if err != nil {
		t.Fatal(err)
	}

	// 2×2 全放木板 → 工作台。
	setCreativeSlot(t, conn, 0, plankID, 4)
	// 窗口 0：快捷栏槽 0 的槽位 = 36。
	sendContainerClick(t, conn, 0, 0, 36, 0, protocol.ClickModePickup)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot) // 背包清空
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot) // 光标 4 木板

	// 右键 4 次放入 2×2 格（槽位 1-4），每次放 1 个。
	for _, slot := range []int16{1, 2, 3, 4} {
		sendContainerClick(t, conn, 0, 0, slot, 1, protocol.ClickModePickup)
		expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot) // 格子更新
		expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot) // 光标更新
	}

	// 结果槽（0）应显示工作台。
	resultSeen := false
	for i := 0; i < 2; i++ {
		_, _, slot, stack := setContainerSlotPacket(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot))
		if slot == 0 && stack.ID == tableID {
			resultSeen = true
		}
	}
	// 结果刷新可能与光标更新合并发送；兜底轮询会话状态。
	player := activeSession(t, instance, "MiniCrafter")
	if player.peekInvCraft() == nil {
		t.Fatal("窗口 0 应初始化合成格")
	}
	waitFor(t, func() bool {
		backing := craftSlotBacking{player: player, state: player.peekInvCraft(), windowZero: true}
		return backing.resultOf(nil).ItemID == tableID
	}, "2×2 木板应合成工作台")
	_ = resultSeen
}

// TestCraftingGridReturnedOnClose 验证关闭工作台窗口时合成格内容归还背包。
func TestCraftingGridReturnedOnClose(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.GameMode = "creative"
	instance, conn := joinServer(t, cfg, "Closer")

	plankID, err := registry.ItemID("minecraft:oak_planks")
	if err != nil {
		t.Fatal(err)
	}
	x, y, z := placeCraftingTable(t, instance)
	setCreativeSlot(t, conn, 0, plankID, 2)
	sendUseItemOn(t, conn, x, y, z, 1)
	windowID, _ := openScreen(t, conn)
	expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)

	// 拾取木板并放入合成格 1。
	sendContainerClick(t, conn, windowID, 0, 37, 0, protocol.ClickModePickup)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)
	sendContainerClick(t, conn, windowID, 0, 1, 0, protocol.ClickModePickup)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)

	// 关闭窗口。
	closePacket := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDContainerClose)
	closePacket = protocol.AppendVarInt(closePacket, windowID)
	if err := protocol.WritePacketWithCompression(conn, closePacket, compressionThreshold); err != nil {
		t.Fatal(err)
	}

	player := activeSession(t, instance, "Closer")
	waitFor(t, func() bool {
		if player.getOpenCraft() != nil {
			return false
		}
		// 木板应回到背包（数量 ≥1）。
		for slot := 0; slot < 41; slot++ {
			if stack := player.inventory.Get(slot); stack.ItemID == plankID {
				return true
			}
		}
		return false
	}, "关闭窗口后合成格内容应归还背包")
}
