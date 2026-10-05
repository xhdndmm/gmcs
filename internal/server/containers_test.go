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

// appendHashedSlot 追加 HashedSlot 结构（客户端为 Container Click 提供的
// 槽位哈希：物品 ID、数量、添加组件数、移除组件数）。
func appendHashedSlot(dst []byte, itemID, count int32) []byte {
	dst = protocol.AppendVarInt(dst, itemID)
	dst = protocol.AppendVarInt(dst, count)
	dst = protocol.AppendVarInt(dst, 0)  // 添加组件数
	return protocol.AppendVarInt(dst, 0) // 移除组件数
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

// placeChest 在指定坐标放置一个箱子方块（直接写世界，模拟放置后的状态）。
func placeChest(t *testing.T, instance *Server, x, y, z int) {
	t.Helper()
	state, ok := registry.BlockStateIDs["minecraft:chest"]
	if !ok {
		t.Fatal("缺少箱子方块状态")
	}
	if !instance.world.SetBlock(x, y, z, state) {
		t.Fatal("无法放置箱子")
	}
}

// chestPositions 返回玩家附近可用的箱子位置（地面之上、交互距离内）。
func chestPositions(t *testing.T, instance *Server) (int, int, int) {
	t.Helper()
	for _, x := range []int{1, 2, 3} {
		for _, z := range []int{1, 2, 3} {
			state, y, ok := instance.world.TopBlock(x, z)
			if !ok || state == world.WaterBlock || state == world.BedrockBlock {
				continue
			}
			return x, y + 1, z
		}
	}
	t.Fatal("出生点附近找不到可放置箱子的位置")
	return 0, 0, 0
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
	if _, _, slot, stack := setContainerSlotPacket(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)); slot != -1 || stack.ID != stoneID || stack.Count != 64 {
		t.Fatalf("光标应持有 64 石头，得到 slot=%d stack=%+v", slot, stack)
	}
	// 左键点击容器槽 0：放下。
	sendContainerClick(t, conn, windowID, 0, 0, 0, protocol.ClickModePickup)
	if _, _, slot, stack := setContainerSlotPacket(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)); slot != 0 || stack.Count != 64 {
		t.Fatalf("容器槽 0 应收到 64 石头，得到 slot=%d stack=%+v", slot, stack)
	}
	// 光标清空。
	if _, _, slot, stack := setContainerSlotPacket(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)); slot != -1 || stack.Count != 0 {
		t.Fatalf("光标应清空，得到 slot=%d stack=%+v", slot, stack)
	}

	// 关闭窗口（客户端主动关闭）。
	closePacket := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDContainerClose)
	closePacket = protocol.AppendVarInt(closePacket, windowID)
	if err := protocol.WritePacketWithCompression(conn, closePacket, compressionThreshold); err != nil {
		t.Fatal(err)
	}
	// 等待服务器处理（发送任意包以确认顺序）：直接轮询世界状态。
	waitFor(t, func() bool {
		entity, err := instance.world.BlockEntityAt(cx, cy, cz)
		if err != nil || len(entity.Items) == 0 {
			return false
		}
		return entity.Items[0].ItemID == stoneID && entity.Items[0].Count == 64
	}, "箱子内容应写回区块")
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
	if err := instance.world.SetBlockEntity(cx, cy, cz, world.BlockEntity{
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
	chunk, err := instance.world.Chunk(x>>4, z>>4)
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
	if _, _, slot, stack := setContainerSlotPacket(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)); slot != -1 || stack.ID != stoneID || stack.Count != 32 {
		t.Fatalf("光标应持有 32 石头，得到 slot=%d stack=%+v", slot, stack)
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
	opened := instance.containers[[3]int{cx, cy, cz}] != nil
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
