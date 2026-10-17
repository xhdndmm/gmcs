//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"net"
	"testing"
	"time"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// activeSession 返回指定玩家名的会话（测试用）。
func activeSession(t *testing.T, instance *Server, name string) *session {
	t.Helper()
	instance.mu.Lock()
	defer instance.mu.Unlock()
	for _, player := range instance.players {
		if player.name == name {
			return player
		}
	}
	t.Fatalf("找不到会话 %s", name)
	return nil
}

// sendContainerClick 发送 Container Click 包（包含 changedSlots 与光标提示，
// 与真实客户端一致；服务器只使用权威状态）。
func sendContainerClick(t *testing.T, conn net.Conn, windowID, stateID int32, slot int16, button int8, mode int32) {
	t.Helper()
	packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDContainerClick)
	packet = protocol.AppendVarInt(packet, windowID)
	packet = protocol.AppendVarInt(packet, stateID)
	packet = append(packet, byte(slot>>8), byte(slot))
	packet = append(packet, byte(button))
	packet = protocol.AppendVarInt(packet, mode)
	packet = protocol.AppendVarInt(packet, 0)   // changedSlots
	packet = protocol.AppendBool(packet, false) // cursorItem
	if err := protocol.WritePacketWithCompression(conn, packet, compressionThreshold); err != nil {
		t.Fatal(err)
	}
}

// sendPlayerInput 发送 Player Input 包（输入位标志）。
func sendPlayerInput(t *testing.T, conn net.Conn, flags uint8) {
	t.Helper()
	packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDPlayerInput)
	packet = append(packet, flags)
	if err := protocol.WritePacketWithCompression(conn, packet, compressionThreshold); err != nil {
		t.Fatal(err)
	}
}

// openScreen 读取 Open Screen 包并返回窗口编号与菜单类型。
func openScreen(t *testing.T, conn net.Conn) (windowID, menuType int32) {
	t.Helper()
	payload := expectPlayPacket(t, conn, protocol.PlayPacketIDOpenScreen)
	_, offset, err := protocol.DecodeVarInt(payload) // 跳过包 ID
	if err != nil {
		t.Fatal(err)
	}
	windowID, size, err := protocol.DecodeVarInt(payload[offset:])
	if err != nil {
		t.Fatal(err)
	}
	offset += size
	menuType, _, err = protocol.DecodeVarInt(payload[offset:])
	if err != nil {
		t.Fatal(err)
	}
	return windowID, menuType
}

// containerContent 从 Container Set Content 包中解析槽位内容。
func containerContent(t *testing.T, payload []byte) (windowID int32, slots []itemStack) {
	t.Helper()
	_, offset, err := protocol.DecodeVarInt(payload) // 跳过包 ID
	if err != nil {
		t.Fatal(err)
	}
	windowID, size, err := protocol.DecodeVarInt(payload[offset:])
	if err != nil {
		t.Fatal(err)
	}
	offset += size
	stateSize := 0
	_, stateSize, err = protocol.DecodeVarInt(payload[offset:])
	if err != nil {
		t.Fatal(err)
	}
	offset += stateSize
	count, countSize, err := protocol.DecodeVarInt(payload[offset:])
	if err != nil {
		t.Fatal(err)
	}
	offset += countSize
	for i := int32(0); i < count; i++ {
		itemID, itemCount, err := protocol.ParseSlotItem(payload[offset:])
		if err != nil {
			t.Fatal(err)
		}
		// 跳过该槽位的字节（ParseSlotItem 只解析头部，需要重新定位）。
		_, newOffset := parseSlotLength(t, payload, offset)
		offset = newOffset
		slots = append(slots, itemStack{ID: itemID, Count: itemCount})
	}
	return windowID, slots
}

// parseSlotLength 返回槽位数据结束的偏移。
func parseSlotLength(t *testing.T, payload []byte, offset int) (int, int) {
	t.Helper()
	count, pos, err := protocol.DecodeVarInt(payload[offset:])
	if err != nil {
		t.Fatal(err)
	}
	offset += pos
	if count <= 0 {
		return 0, offset
	}
	if _, pos, err = protocol.DecodeVarInt(payload[offset:]); err != nil {
		t.Fatal(err)
	}
	offset += pos
	added, pos, err := protocol.DecodeVarInt(payload[offset:])
	if err != nil {
		t.Fatal(err)
	}
	offset += pos
	removed, pos, err := protocol.DecodeVarInt(payload[offset:])
	if err != nil {
		t.Fatal(err)
	}
	offset += pos
	if added != 0 || removed != 0 {
		t.Fatalf("测试不支持带组件槽位（added=%d removed=%d）", added, removed)
	}
	return int(count), offset
}

// itemStack 是测试用的简化物品堆栈。
type itemStack struct {
	ID    int32
	Count int32
}

// setContainerSlotPacket 解析 Set Container Slot 包。
func setContainerSlotPacket(t *testing.T, payload []byte) (windowID, stateID int32, slot int16, stack itemStack) {
	t.Helper()
	_, offset, err := protocol.DecodeVarInt(payload) // 跳过包 ID
	if err != nil {
		t.Fatal(err)
	}
	windowID, size, err := protocol.DecodeVarInt(payload[offset:])
	if err != nil {
		t.Fatal(err)
	}
	offset += size
	stateSize := 0
	stateID, stateSize, err = protocol.DecodeVarInt(payload[offset:])
	if err != nil {
		t.Fatal(err)
	}
	offset += stateSize
	if len(payload)-offset < 2 {
		t.Fatal("截断的 Set Container Slot 包")
	}
	slot = int16(uint16(payload[offset])<<8 | uint16(payload[offset+1]))
	offset += 2
	itemID, count, err := protocol.ParseSlotItem(payload[offset:])
	if err != nil {
		t.Fatal(err)
	}
	return windowID, stateID, slot, itemStack{ID: itemID, Count: count}
}

// containerContentCarried 解析 Container Set Content 包末尾的 carried（光标）物品。
func containerContentCarried(t *testing.T, payload []byte) itemStack {
	t.Helper()
	_, offset, err := protocol.DecodeVarInt(payload) // 跳过包 ID
	if err != nil {
		t.Fatal(err)
	}
	_, size, err := protocol.DecodeVarInt(payload[offset:]) // 窗口编号
	if err != nil {
		t.Fatal(err)
	}
	offset += size
	_, size, err = protocol.DecodeVarInt(payload[offset:]) // 状态号
	if err != nil {
		t.Fatal(err)
	}
	offset += size
	count, size, err := protocol.DecodeVarInt(payload[offset:])
	if err != nil {
		t.Fatal(err)
	}
	offset += size
	for i := int32(0); i < count; i++ {
		_, offset = parseSlotLength(t, payload, offset)
	}
	itemID, itemCount, err := protocol.ParseSlotItem(payload[offset:])
	if err != nil {
		t.Fatal(err)
	}
	return itemStack{ID: itemID, Count: itemCount}
}

// cursorItemPacket 解析 Set Cursor Item 包（鼠标光标物品）。
// 1.21.11 客户端用该专用包同步光标：Set Container Slot 的槽位必须在窗口槽位
// 范围内，用 slot=-1 会让客户端抛越界异常并断线（网络协议错误）。
func cursorItemPacket(t *testing.T, payload []byte) itemStack {
	t.Helper()
	_, offset, err := protocol.DecodeVarInt(payload) // 跳过包 ID
	if err != nil {
		t.Fatal(err)
	}
	itemID, count, err := protocol.ParseSlotItem(payload[offset:])
	if err != nil {
		t.Fatal(err)
	}
	return itemStack{ID: itemID, Count: count}
}

// placeChest 在指定坐标放置一个箱子方块（直接写世界，模拟放置后的状态）。
func placeChest(t *testing.T, instance *Server, x, y, z int) {
	t.Helper()
	state, ok := registry.BlockStateIDs["minecraft:chest"]
	if !ok {
		t.Fatal("缺少箱子方块状态")
	}
	if !instance.testWorld().SetBlock(x, y, z, state) {
		t.Fatal("无法放置箱子")
	}
}

// chestPositions 返回玩家附近可用的箱子位置（地面之上、交互距离内）。
func chestPositions(t *testing.T, instance *Server) (int, int, int) {
	t.Helper()
	return buildSpotNearSpawn(t, instance)
}

// TestContainerOpenAndMoveItem 验证容器交互主流程：
// 打开箱子 → 收到窗口与内容 → 点击把物品移入 → 关闭时写回区块。
func TestContainerOpenAndMoveItem(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:stone*64"}
	instance, conn := joinServer(t, cfg, "Trader")

	cx, cy, cz := chestPositions(t, instance)
	placeChest(t, instance, cx, cy, cz)

	// 右键打开箱子。
	sendUseItemOn(t, conn, cx, cy, cz, 1)
	windowID, menuType := openScreen(t, conn)
	if menuType != protocol.MenuTypeGeneric9x3 {
		t.Fatalf("菜单类型 = %d, want %d", menuType, protocol.MenuTypeGeneric9x3)
	}
	content := expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)
	gotWindow, slots := containerContent(t, content)
	if gotWindow != windowID {
		t.Fatalf("窗口编号 = %d, want %d", gotWindow, windowID)
	}
	if len(slots) != 27+36 {
		t.Fatalf("窗口槽位数 = %d, want %d", len(slots), 27+36)
	}
	if slots[0].Count != 0 {
		t.Fatalf("新箱子应为空，槽位 0 = %+v", slots[0])
	}

	// 左键点击容器窗口中的快捷栏槽位（54 = 快捷栏 0），把石头捡到光标。
	stoneID, err := registry.ItemID("minecraft:stone")
	if err != nil {
		t.Fatal(err)
	}
	sendContainerClick(t, conn, windowID, 0, 54, 0, protocol.ClickModePickup)
	// 光标拾取 64 个石头。
	if _, _, slot, stack := setContainerSlotPacket(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)); slot != 54 || stack.Count != 0 {
		t.Fatalf("玩家槽位应被清空，得到 slot=%d stack=%+v", slot, stack)
	}
	if stack := cursorItemPacket(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)); stack.ID != stoneID || stack.Count != 64 {
		t.Fatalf("光标应持有 64 石头，得到 %+v", stack)
	}
	// 左键点击容器槽 0：放下。
	sendContainerClick(t, conn, windowID, 0, 0, 0, protocol.ClickModePickup)
	if _, _, slot, stack := setContainerSlotPacket(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)); slot != 0 || stack.Count != 64 {
		t.Fatalf("容器槽 0 应收到 64 石头，得到 slot=%d stack=%+v", slot, stack)
	}
	// 光标清空。
	if stack := cursorItemPacket(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)); stack.Count != 0 {
		t.Fatalf("光标应清空，得到 %+v", stack)
	}

	// 关闭窗口（客户端主动关闭）。
	closePacket := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDContainerClose)
	closePacket = protocol.AppendVarInt(closePacket, windowID)
	if err := protocol.WritePacketWithCompression(conn, closePacket, compressionThreshold); err != nil {
		t.Fatal(err)
	}
	// 等待服务器处理（发送任意包以确认顺序）：直接轮询世界状态。
	waitFor(t, func() bool {
		entity, err := instance.testWorld().BlockEntityAt(cx, cy, cz)
		if err != nil || len(entity.Items) == 0 {
			return false
		}
		return entity.Items[0].ItemID == stoneID && entity.Items[0].Count == 64
	}, "箱子内容应写回区块")
}

// TestPlayerInventoryCursorSync 复现实机 1.21.11 客户端的光标同步约束：
// 窗口 0 的槽位更新必须落在 InventoryMenu 的 46 个槽位（0–45）内，
// 光标必须用 Set Cursor Item 下发 —— 用 slot=-1 的 Set Container Slot 会让
// 客户端在 AbstractContainerMenu#getSlot(-1) 抛出越界异常并以网络协议错误断线。
func TestPlayerInventoryCursorSync(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:stone*32"}
	_, conn := joinServer(t, cfg, "Cursor")

	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 36, 0, protocol.ClickModePickup)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		id, payload := readCompressedPacket(t, conn)
		switch id {
		case 0x2B:
			replyKeepAlive(t, conn, payload)
		case protocol.PlayPacketIDSetContainerSlot:
			windowID, _, slot, stack := setContainerSlotPacket(t, payload)
			if windowID == protocol.PlayerInventoryWindowID && (slot < 0 || slot > 45) {
				t.Fatalf("窗口 0 收到越界槽位 %d（stack=%+v）：实机客户端会以网络协议错误断线", slot, stack)
			}
		case protocol.PlayPacketIDSetCursorItem:
			if stack := cursorItemPacket(t, payload); stack.Count != 32 {
				t.Fatalf("光标 = %+v, want 32 石头", stack)
			}
			return
		}
	}
	t.Fatal("未收到 Set Cursor Item 光标同步包")
}

// TestCursorItemSurvivesWindowSwitch 验证光标物品在窗口切换中的生命周期：
// 在物品栏拾起的物品随打开的箱子窗口转交（不丢失），关闭箱子后归还物品栏。
func TestCursorItemSurvivesWindowSwitch(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:stone*32"}
	instance, conn := joinServer(t, cfg, "Carrier")

	// 窗口 0 快捷栏 0 → 光标。
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 36, 0, protocol.ClickModePickup)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot) // 背包槽清空
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)    // 光标持 32 石头

	// 打开箱子：光标物品写入窗口内容的 carried 字段。
	cx, cy, cz := chestPositions(t, instance)
	placeChest(t, instance, cx, cy, cz)
	sendUseItemOn(t, conn, cx, cy, cz, 1)
	windowID, _ := openScreen(t, conn)
	content := expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)
	if stack := containerContentCarried(t, content); stack.Count != 32 {
		t.Fatalf("窗口内容 carried = %+v, want 32 石头", stack)
	}

	// 关闭窗口：光标物品放回物品栏（原版 dropOrPlaceInInventory 语义）。
	closePacket := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDContainerClose)
	closePacket = protocol.AppendVarInt(closePacket, windowID)
	if err := protocol.WritePacketWithCompression(conn, closePacket, compressionThreshold); err != nil {
		t.Fatal(err)
	}
	if stack := cursorItemPacket(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)); stack.Count != 0 {
		t.Fatalf("关闭窗口后光标应清空，得到 %+v", stack)
	}
	player := activeSession(t, instance, "Carrier")
	waitFor(t, func() bool {
		return player.inventory.Get(item.SlotHotbarStart).Count == 32
	}, "关闭窗口后光标物品应回到物品栏")
}

// TestQuickCraftOutsideWindowIsIgnored 验证实机客户端在窗口外按住鼠标拖动
// （开始/经过/结束三个阶段都携带 slot=-999）时的容错：光标为空时应丢弃拖拽，
// 不能按槽位索引导致越界崩溃。
func TestQuickCraftOutsideWindowIsIgnored(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:stone*32"}
	instance, conn := joinServer(t, cfg, "Dragger")

	// 实机客户端在窗口外拖动时整段序列的三个阶段都携带 slot=-999。
	// 左键拖拽：开始 button 0、经过 1、结束 2。
	for _, button := range []int8{0, 1, 2} {
		sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, -999, button, protocol.ClickModeQuickCraft)
	}
	// 处理屏障：服务器仍在正常响应（未崩溃、会话可用）。
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")
	if _, ok := instance.players[[16]byte{}]; ok {
		t.Fatal("会话表不应被破坏")
	}
	if stack := activeSession(t, instance, "Dragger").inventory.Get(item.SlotHotbarStart); stack.Count != 32 {
		t.Fatalf("物品栏不应被改动：%+v", stack)
	}
}

// TestQuickCraftLeftDragDistributes 验证左键拖拽（开始 0、经过 1、结束 2）按原版
// 把整栈平均分摊到经过的槽位（每格 floor(数量/槽位数)）。
func TestQuickCraftLeftDragDistributes(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:stone*32"}
	instance, conn := joinServer(t, cfg, "DragLeft")

	// 窗口 0：先把快捷栏 0（菜单槽位 36）的石头拿到光标。
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 36, 0, protocol.ClickModePickup)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)

	// 拖过主背包槽位 9、10、11（菜单槽位 9、10、11）。
	// 左键拖拽的 button 编码：开始 0、经过 1、结束 2（低 2 位为阶段，高 2 位为按键类型）。
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, -999, 0, protocol.ClickModeQuickCraft)
	for _, slot := range []int16{9, 10, 11} {
		sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, slot, 1, protocol.ClickModeQuickCraft)
	}
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, -999, 2, protocol.ClickModeQuickCraft)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")

	player := activeSession(t, instance, "DragLeft")
	for _, slot := range []int{9, 10, 11} {
		if stack := player.inventory.Get(slot); stack.Count != 10 {
			t.Fatalf("主背包槽位 %d = %+v, want 10 石头（32/3 分摊）", slot, stack)
		}
	}
	if cursor := player.getCursor(); cursor.Count != 2 {
		t.Fatalf("拖拽后光标 = %+v, want 2 石头", cursor)
	}
}

// TestQuickCraftLeftDragSingleSlotPlacesStack 验证左键拖拽只划过一格时放下整栈：
// 原版每格放 floor(数量/槽位数) = 全部，此前实现每格只放 1 个，手上会剩 n−1。
func TestQuickCraftLeftDragSingleSlotPlacesStack(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:stone*64"}
	instance, conn := joinServer(t, cfg, "DragOne")

	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 36, 0, protocol.ClickModePickup)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)

	// 只划过主背包槽位 9（菜单槽位 9）。
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, -999, 0, protocol.ClickModeQuickCraft)
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 9, 1, protocol.ClickModeQuickCraft)
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, -999, 2, protocol.ClickModeQuickCraft)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")

	player := activeSession(t, instance, "DragOne")
	if stack := player.inventory.Get(9); stack.Count != 64 {
		t.Fatalf("主背包槽位 9 = %+v, want 64 石头", stack)
	}
	if cursor := player.getCursor(); !cursor.IsEmpty() {
		t.Fatalf("放下整栈后光标应为空，得到 %+v", cursor)
	}
}

// TestQuickCraftRightDragPlacesOne 验证右键拖拽（开始 4、经过 5、结束 6）
// 每个经过的槽位放 1 个。
func TestQuickCraftRightDragPlacesOne(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:stone*32"}
	instance, conn := joinServer(t, cfg, "DragRight")

	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 36, 0, protocol.ClickModePickup)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)

	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, -999, 4, protocol.ClickModeQuickCraft)
	for _, slot := range []int16{9, 10, 11} {
		sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, slot, 5, protocol.ClickModeQuickCraft)
	}
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, -999, 6, protocol.ClickModeQuickCraft)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")

	player := activeSession(t, instance, "DragRight")
	for _, slot := range []int{9, 10, 11} {
		if stack := player.inventory.Get(slot); stack.Count != 1 {
			t.Fatalf("主背包槽位 %d = %+v, want 1 石头", slot, stack)
		}
	}
	if cursor := player.getCursor(); cursor.Count != 29 {
		t.Fatalf("拖拽后光标 = %+v, want 29 石头", cursor)
	}
}

// TestQuickCraftSkipsRejectedSlots 验证“经过”阶段逐槽校验：槽位已有不同物品时
// 该槽不入集合（且不重置整次拖拽），结束阶段按剩余槽位数均分。
// 对照官方服务端实测：携带 64 个物品拖过 [异类槽, 空槽, 空槽] → 两个空槽各 32。
func TestQuickCraftSkipsRejectedSlots(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:dirt*32", "minecraft:stone*64"}
	instance, conn := joinServer(t, cfg, "DragSkip")

	// 把泥土从快捷栏 0（菜单槽位 36）放进主背包槽位 9，再拿起石头。
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 36, 0, protocol.ClickModePickup)
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 9, 0, protocol.ClickModePickup)
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 37, 0, protocol.ClickModePickup)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")

	// 左键拖过槽位 9（泥土，应跳过）、10、11。
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, -999, 0, protocol.ClickModeQuickCraft)
	for _, slot := range []int16{9, 10, 11} {
		sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, slot, 1, protocol.ClickModeQuickCraft)
	}
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, -999, 2, protocol.ClickModeQuickCraft)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")

	player := activeSession(t, instance, "DragSkip")
	if stack := player.inventory.Get(9); stack.Count != 32 || stack.ItemID != stackID(t, "minecraft:dirt") {
		t.Fatalf("槽位 9 应保持 32 泥土不变，得到 %+v", stack)
	}
	for _, slot := range []int{10, 11} {
		if stack := player.inventory.Get(slot); stack.Count != 32 {
			t.Fatalf("槽位 %d = %+v, want 32 石头", slot, stack)
		}
	}
	if cursor := player.getCursor(); !cursor.IsEmpty() {
		t.Fatalf("64 个全部放下后光标应为空，得到 %+v", cursor)
	}
}

// TestQuickCraftRespectsCarriedCount 验证“经过”阶段的数量门控：非创造拖拽要求
// 携带数量严格大于已收集槽位数，否则该槽不加入集合。
// 对照官方服务端实测：携带 4 个物品拖过 5 个空槽 → 只有 4 个槽各放 1 个。
func TestQuickCraftRespectsCarriedCount(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:apple*4"}
	instance, conn := joinServer(t, cfg, "DragCount")

	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 36, 0, protocol.ClickModePickup)

	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, -999, 0, protocol.ClickModeQuickCraft)
	for _, slot := range []int16{9, 10, 11, 12, 13} {
		sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, slot, 1, protocol.ClickModeQuickCraft)
	}
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, -999, 2, protocol.ClickModeQuickCraft)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")

	player := activeSession(t, instance, "DragCount")
	for _, slot := range []int{9, 10, 11, 12} {
		if stack := player.inventory.Get(slot); stack.Count != 1 {
			t.Fatalf("槽位 %d = %+v, want 1 苹果（4 个物品摊到 4 个槽）", slot, stack)
		}
	}
	if stack := player.inventory.Get(13); !stack.IsEmpty() {
		t.Fatalf("槽位 13 不应被加入集合（数量 4 不大于 4），得到 %+v", stack)
	}
	if cursor := player.getCursor(); !cursor.IsEmpty() {
		t.Fatalf("拖拽后光标应为空，得到 %+v", cursor)
	}
}

// TestQuickCraftDeduplicatesSlots 验证“经过”阶段的槽位去重（原版 quickCraftSlots
// 为 Set）：同一槽位重复划过多次只算一个槽。
// 对照官方服务端实测：携带 8 个物品重复划过同一空槽 → 该槽 8 个全部放下。
func TestQuickCraftDeduplicatesSlots(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:apple*8"}
	instance, conn := joinServer(t, cfg, "DragDup")

	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 36, 0, protocol.ClickModePickup)

	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, -999, 0, protocol.ClickModeQuickCraft)
	for i := 0; i < 3; i++ {
		sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 9, 1, protocol.ClickModeQuickCraft)
	}
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, -999, 2, protocol.ClickModeQuickCraft)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")

	player := activeSession(t, instance, "DragDup")
	if stack := player.inventory.Get(9); stack.Count != 8 {
		t.Fatalf("槽位 9 = %+v, want 8 苹果（去重后按单槽处理）", stack)
	}
	if cursor := player.getCursor(); !cursor.IsEmpty() {
		t.Fatalf("拖拽后光标应为空，得到 %+v", cursor)
	}
}

// TestQuickCraftNormalClickCancelsDrag 验证拖拽进行中收到其他模式的点击时，
// 原版只取消拖拽并丢弃该次点击（不执行点击本身）；随后的点击恢复正常。
func TestQuickCraftNormalClickCancelsDrag(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:stone*32"}
	instance, conn := joinServer(t, cfg, "DragCancel")

	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 36, 0, protocol.ClickModePickup)

	// 开始拖拽并划过一个空槽，然后插入一次普通左键点击。
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, -999, 0, protocol.ClickModeQuickCraft)
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 9, 1, protocol.ClickModeQuickCraft)
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 9, 0, protocol.ClickModePickup)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")

	player := activeSession(t, instance, "DragCancel")
	if stack := player.inventory.Get(9); !stack.IsEmpty() {
		t.Fatalf("拖拽中的普通点击应被丢弃（槽位 9 应保持空），得到 %+v", stack)
	}
	if cursor := player.getCursor(); cursor.Count != 32 {
		t.Fatalf("拖拽中的普通点击不应改动光标，得到 %+v", cursor)
	}

	// 再点一次：此时应正常工作（拿起 → 放入）。
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 9, 0, protocol.ClickModePickup)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")
	player = activeSession(t, instance, "DragCancel")
	if stack := player.inventory.Get(9); stack.Count != 32 {
		t.Fatalf("取消拖拽后的普通点击应正常放入，槽位 9 = %+v", stack)
	}
	if cursor := player.getCursor(); !cursor.IsEmpty() {
		t.Fatalf("放入后光标应为空，得到 %+v", cursor)
	}
}

// TestQuickCraftEndWithoutStartIsIgnored 验证未开始就收到“结束”包时只重置状态：
// 不做事，且不影响后续点击（原版状态迁移校验）。
func TestQuickCraftEndWithoutStartIsIgnored(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:stone*32"}
	instance, conn := joinServer(t, cfg, "DragEndOnly")

	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 36, 0, protocol.ClickModePickup)

	// 只发“结束”（button 2），未发“开始”。
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, -999, 2, protocol.ClickModeQuickCraft)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")

	player := activeSession(t, instance, "DragEndOnly")
	if stack := player.inventory.Get(9); !stack.IsEmpty() {
		t.Fatalf("只发结束包不应改动任何槽位，槽位 9 = %+v", stack)
	}
	if cursor := player.getCursor(); cursor.Count != 32 {
		t.Fatalf("只发结束包不应改动光标，得到 %+v", cursor)
	}

	// 后续点击仍正常工作。
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 9, 0, protocol.ClickModePickup)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")
	player = activeSession(t, instance, "DragEndOnly")
	if stack := player.inventory.Get(9); stack.Count != 32 {
		t.Fatalf("后续普通点击应正常工作，槽位 9 = %+v", stack)
	}
}

// TestQuickCraftMiddleDragInSurvivalIsIgnored 验证中键拖拽（类型 2）在非创造
// 模式下完全不生效（原版 canQuickCraftStart 要求 hasInfiniteMaterials）。
func TestQuickCraftMiddleDragInSurvivalIsIgnored(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:stone*32"}
	instance, conn := joinServer(t, cfg, "DragMiddle")

	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 36, 0, protocol.ClickModePickup)

	// 中键拖拽：开始 8、经过 9、结束 10。
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, -999, 8, protocol.ClickModeQuickCraft)
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 9, 9, protocol.ClickModeQuickCraft)
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, -999, 10, protocol.ClickModeQuickCraft)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")

	player := activeSession(t, instance, "DragMiddle")
	if stack := player.inventory.Get(9); !stack.IsEmpty() {
		t.Fatalf("生存模式的中键拖拽不应改动槽位，槽位 9 = %+v", stack)
	}
	if cursor := player.getCursor(); cursor.Count != 32 {
		t.Fatalf("生存模式的中键拖拽不应改动光标，得到 %+v", cursor)
	}
}

// stackID 返回物品名字对应的注册表 ID（测试辅助）。
func stackID(t *testing.T, name string) int32 {
	t.Helper()
	id, err := registry.ItemID(name)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TestContainerQuickMove 验证 Shift 点击（快速移动）把整栈移入容器。
func TestContainerQuickMove(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:apple*10"}
	instance, conn := joinServer(t, cfg, "Mover")

	cx, cy, cz := chestPositions(t, instance)
	placeChest(t, instance, cx, cy, cz)
	sendUseItemOn(t, conn, cx, cy, cz, 1)
	windowID, _ := openScreen(t, conn)
	expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)

	// Shift 点击容器窗口中的快捷栏槽位（54 = 快捷栏 0）→ 移入箱子槽 0。
	sendContainerClick(t, conn, windowID, 0, 54, 0, protocol.ClickModeQuickMove)
	appleID, err := registry.ItemID("minecraft:apple")
	if err != nil {
		t.Fatal(err)
	}
	// 期望：容器槽 0 收到 10 个苹果，玩家槽位清空。
	seenContainer := false
	seenPlayer := false
	for i := 0; i < 2; i++ {
		_, _, slot, stack := setContainerSlotPacket(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot))
		switch slot {
		case 0:
			seenContainer = stack.ID == appleID && stack.Count == 10
		case 54:
			seenPlayer = stack.Count == 0
		}
	}
	if !seenContainer || !seenPlayer {
		t.Fatalf("Shift 点击结果不符：容器=%v 玩家=%v", seenContainer, seenPlayer)
	}
}

// TestChestBreakDropsContents 验证破坏箱子时内容物掉落到世界。
func TestChestBreakDropsContents(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.GameMode = "creative"
	instance, conn := joinServer(t, cfg, "Breaker")

	cx, cy, cz := chestPositions(t, instance)
	placeChest(t, instance, cx, cy, cz)
	// 预置内容：3 个钻石。
	diamondID, err := registry.ItemID("minecraft:diamond")
	if err != nil {
		t.Fatal(err)
	}
	if err := instance.testWorld().SetBlockEntity(cx, cy, cz, world.BlockEntity{
		TypeID: 1, // minecraft:chest 的方块实体类型（由 resolveContainerDefs 校验）
		Items:  []world.ContainerItem{{ItemID: diamondID, Count: 3}},
	}); err != nil {
		t.Fatal(err)
	}

	// 创意模式破坏箱子。
	sendPlayerAction(t, conn, 0, cx, cy, cz, 1)
	expectBlockUpdate(t, conn, cx, cy, cz, int32(world.AirBlock))

	// 找到一个 3 个钻石的掉落物实体。
	instance.tick() // 推进掉落物一帧（生成时已放入世界）
	found := false
	instance.entityMu.Lock()
	for _, e := range instance.items {
		if e.Stack.ItemID == diamondID && e.Stack.Count == 3 {
			found = true
		}
	}
	instance.entityMu.Unlock()
	if !found {
		t.Fatal("破坏箱子后应掉落 3 个钻石")
	}
	if count := countBlockEntities(t, instance, cx, cy, cz); count != 0 {
		t.Fatalf("破坏后不应残留方块实体数据（count=%d）", count)
	}
}

// countBlockEntities 返回指定位置区块中的方块实体数量。
func countBlockEntities(t *testing.T, instance *Server, x, y, z int) int {
	t.Helper()
	chunk, err := instance.testWorld().Chunk(x>>4, z>>4)
	if err != nil {
		t.Fatal(err)
	}
	return chunk.BlockEntityCount()
}

// TestPlayerInventoryClickMovesItem 验证玩家物品栏窗口（window 0）的点击：
// 快捷栏 → 主背包，且不涉及容器窗口。
func TestPlayerInventoryClickMovesItem(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:stone*32"}
	instance, conn := joinServer(t, cfg, "Sorter")

	// 窗口 0 布局：0–8 合成格与盔甲、9–35 主背包、36–44 快捷栏、45 副手。
	// 左键点击快捷栏槽位（36）→ 拾取石头；再点击主背包槽位（9）→ 放下。
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 36, 0, protocol.ClickModePickup)
	if _, _, slot, stack := setContainerSlotPacket(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)); slot != 36 || stack.Count != 0 {
		t.Fatalf("快捷栏槽位应被清空，得到 slot=%d stack=%+v", slot, stack)
	}
	stoneID, err := registry.ItemID("minecraft:stone")
	if err != nil {
		t.Fatal(err)
	}
	if stack := cursorItemPacket(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)); stack.ID != stoneID || stack.Count != 32 {
		t.Fatalf("光标应持有 32 石头，得到 %+v", stack)
	}
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 1, 9, 0, protocol.ClickModePickup)
	if _, _, slot, stack := setContainerSlotPacket(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)); slot != 9 || stack.Count != 32 {
		t.Fatalf("主背包槽位应收到 32 石头，得到 slot=%d stack=%+v", slot, stack)
	}
	// 服务器侧物品栏也应已更新。
	if stack := activeSession(t, instance, "Sorter").inventory.Get(item.SlotMainStart); stack.Count != 32 {
		t.Fatalf("服务器物品栏主背包槽位 = %+v, want 32 石头", stack)
	}
}

// TestContainerSneakPlaceInsteadOfOpen 验证潜行时右键容器方块不会打开窗口
// （改为放置方块，与原版一致）；取消潜行后右键才会打开窗口。
func TestContainerSneakPlaceInsteadOfOpen(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.GameMode = "creative"
	instance, conn := joinServer(t, cfg, "Sneaker")

	cx, cy, cz := chestPositions(t, instance)
	placeChest(t, instance, cx, cy, cz)

	// 潜行 + 右键箱子：放置方块而不是打开窗口。
	sendPlayerInput(t, conn, protocol.PlayerInputShift)
	sendUseItemOn(t, conn, cx, cy, cz, 1)
	expectBlockUpdate(t, conn, cx, cy+1, cz, int32(world.StoneBlock))
	instance.containerMu.Lock()
	opened := instance.containers[containerKey{dim: world.DimensionOverworld, x: cx, y: cy, z: cz}] != nil
	instance.containerMu.Unlock()
	if opened {
		t.Fatal("潜行时右键箱子不应打开容器窗口")
	}

	// 取消潜行后右键箱子：打开窗口。
	sendPlayerInput(t, conn, 0)
	sendUseItemOn(t, conn, cx, cy, cz, 1)
	if windowID, _ := openScreen(t, conn); windowID == 0 {
		t.Fatal("非潜行右键箱子应打开窗口")
	}
}

// waitFor 轮询条件，最多等待约 2 秒。
func waitFor(t *testing.T, condition func() bool, message string) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(message)
}
