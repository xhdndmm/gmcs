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

// sendPlayerAction 发送 Player Action（block_dig）包。
func sendPlayerAction(t *testing.T, conn net.Conn, status int32, x, y, z int, face byte) {
	t.Helper()
	packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDPlayerAction)
	packet = protocol.AppendVarInt(packet, status)
	packet = protocol.AppendInt64(packet, protocol.PackPosition(x, y, z))
	packet = append(packet, face)
	packet = protocol.AppendVarInt(packet, 0) // sequence
	if err := protocol.WritePacketWithCompression(conn, packet, compressionThreshold); err != nil {
		t.Fatal(err)
	}
}

// breakBlockSurvival 以生存挖掘流程破坏方块：发送 START_DESTROY_BLOCK，推进
// 服务端 tick 使累计进度超过 STOP 阈值（原版客户端在完成时发送 STOP），再发送
// STOP_DESTROY_BLOCK 完成破坏。默认推进 200 tick（空手挖石头需要 105 tick，
// 工具更快）；期间产生的挖掘动画/Ack/粒子包由 expectPlayPacket 自动跳过。
func breakBlockSurvival(t *testing.T, conn net.Conn, instance *Server, player *session, x, y, z int) {
	t.Helper()
	breakBlockSurvivalTicks(t, conn, instance, player, x, y, z, 200)
}

// breakBlockSurvivalTicks 是 breakBlockSurvival 的可控版本（指定推进的 tick 数，
// 用于测试提前停止与延迟破坏等场景）。
func breakBlockSurvivalTicks(t *testing.T, conn net.Conn, instance *Server, player *session, x, y, z int, ticks int) {
	t.Helper()
	// 客户端在登录与移动包里上报着地状态（悬空挖掘速度 ÷5）；测试无移动包，
	// 显式置位以模拟站立挖掘。
	player.setPlayerOnGround(true)
	sendPlayerAction(t, conn, 0, x, y, z, 1)
	// /list 往返作为处理屏障：确保 START 已被读循环记录开始刻。
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")
	for i := 0; i < ticks; i++ {
		instance.tick()
	}
	sendPlayerAction(t, conn, 2, x, y, z, 1)
}

// sendUseItemOn 发送 Use Item On（block_place）包（主手、光标居中）。
func sendUseItemOn(t *testing.T, conn net.Conn, x, y, z int, direction int32) {
	t.Helper()
	packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDUseItemOn)
	packet = protocol.AppendVarInt(packet, 0) // hand
	packet = protocol.AppendInt64(packet, protocol.PackPosition(x, y, z))
	packet = protocol.AppendVarInt(packet, direction)
	packet = protocol.AppendFloat32(packet, 0.5)
	packet = protocol.AppendFloat32(packet, 0.5)
	packet = protocol.AppendFloat32(packet, 0.5)
	packet = protocol.AppendBool(packet, false) // insideBlock
	packet = protocol.AppendBool(packet, false) // worldBorderHit
	packet = protocol.AppendVarInt(packet, 0)   // sequence
	if err := protocol.WritePacketWithCompression(conn, packet, compressionThreshold); err != nil {
		t.Fatal(err)
	}
}

// sendSetCarriedItem 发送快捷栏切换包。
func sendSetCarriedItem(t *testing.T, conn net.Conn, slot int32) {
	t.Helper()
	packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDSetCarriedItem)
	packet = protocol.AppendInt16(packet, int16(slot))
	if err := protocol.WritePacketWithCompression(conn, packet, compressionThreshold); err != nil {
		t.Fatal(err)
	}
}

// menuSlotForInventory 把物品栏内部槽位映射为“玩家物品栏菜单”槽位号
// （Set Creative Mode Slot 线上使用的编号，与服务端 handleSetCreativeSlot 的
// 反向映射 playerInvForMenuSlot 对应）。
func menuSlotForInventory(slot int) int16 {
	switch {
	case slot >= item.SlotHotbarStart && slot <= item.SlotHotbarEnd:
		return int16(36 + slot)
	case slot >= item.SlotMainStart && slot <= item.SlotMainEnd:
		return int16(slot)
	case slot >= item.SlotFeet && slot <= item.SlotHead:
		return int16(44 - slot) // 39→5（头）… 36→8（脚）
	case slot == item.SlotOffhand:
		return 45
	default:
		return -1
	}
}

// sendSetCreativeSlotMenu 按线上“玩家菜单”槽位号发送创造物品栏设置包
// （无数据组件）。
func sendSetCreativeSlotMenu(t *testing.T, conn net.Conn, menuSlot int16, itemID int32, count int32) {
	t.Helper()
	packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDSetCreativeSlot)
	packet = append(packet, byte(menuSlot>>8), byte(menuSlot))
	packet = protocol.AppendVarInt(packet, count)
	if count > 0 {
		packet = protocol.AppendVarInt(packet, itemID)
		packet = protocol.AppendVarInt(packet, 0) // added components
		packet = protocol.AppendVarInt(packet, 0) // removed components
	}
	if err := protocol.WritePacketWithCompression(conn, packet, compressionThreshold); err != nil {
		t.Fatal(err)
	}
}

// sendSetCreativeSlot 按物品栏内部槽位发送创造物品栏设置包
// （自动映射为菜单槽位号，与客户端在创造物品栏中的行为一致）。
func sendSetCreativeSlot(t *testing.T, conn net.Conn, invSlot int16, itemID int32, count int32) {
	t.Helper()
	menuSlot := menuSlotForInventory(int(invSlot))
	if menuSlot < 0 {
		t.Fatalf("无效的物品栏槽位 %d", invSlot)
	}
	sendSetCreativeSlotMenu(t, conn, menuSlot, itemID, count)
}

// expectBlockUpdate 读取一个方块更新包并校验位置与状态。
func expectBlockUpdate(t *testing.T, conn net.Conn, x, y, z int, state int32) {
	t.Helper()
	payload := expectPlayPacket(t, conn, protocol.PlayPacketIDBlockUpdate)
	_, offset, err := protocol.DecodeVarInt(payload)
	if err != nil {
		t.Fatal(err)
	}
	position, offset, err := protocol.DecodeInt64(payload, offset)
	if err != nil {
		t.Fatal(err)
	}
	px, py, pz := protocol.UnpackPosition(position)
	if px != x || py != y || pz != z {
		t.Fatalf("方块更新位置 = (%d,%d,%d)，want (%d,%d,%d)", px, py, pz, x, y, z)
	}
	got, _, err := protocol.DecodeVarInt(payload[offset:])
	if err != nil || got != state {
		t.Fatalf("方块更新状态 = %d (err=%v)，want %d", got, err, state)
	}
}

// findDigTarget 在出生点附近寻找一个可破坏的方块列（返回顶面方块坐标）。
func findDigTarget(t *testing.T, instance *Server) (int, int, int) {
	t.Helper()
	return digTargetNearSpawn(t, instance)
}

// TestPlacementPushesPlayerUp 验证在玩家脚下放置方块时玩家被推到方块顶面
// （原版 pushEntitiesUp 行为），不会嵌入方块而被反穿墙校验反复回拉。
func TestPlacementPushesPlayerUp(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:stone*64"}
	instance, conn := joinServer(t, cfg, "Stacker")
	player := findSession(t, instance, "Stacker")
	x, y, z, _, _ := player.playerPosition()
	baseX, baseY, baseZ := int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))

	// 往玩家脚下的格子放石头（点击下方方块的顶面）。
	sendUseItemOn(t, conn, baseX, baseY-1, baseZ, 1)
	expectPlayPacket(t, conn, protocol.PlayPacketIDBlockUpdate)
	if got := instance.testWorld().BlockAt(baseX, baseY, baseZ); got != world.StoneBlock {
		t.Fatalf("placed block = %d, want stone", got)
	}
	// 玩家应被推到石头顶面（y = baseY+1），水平位置不变。
	// 位置包在方块更新之后发送，等待它到达再断言服务器侧位置（避免与推起
	// 执行时序竞争）。
	expectPlayPacket(t, conn, protocol.PlayPacketIDSynchronizePlayerPos)
	_, gotY, _, _, _ := player.playerPosition()
	if math.Abs(gotY-float64(baseY+1)) > 1e-9 {
		t.Fatalf("player y = %v, want %v", gotY, baseY+1)
	}
	// 推起后的移动不应被回拉（不会卡在方块里）。/list 往返作为处理屏障。
	sendPlayerPosition(t, conn, x+1, float64(baseY+1), z, true)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")
	gotX, _, gotZ, _, _ := player.playerPosition()
	if math.Abs(gotX-(x+1)) > 1e-9 || math.Abs(gotZ-z) > 1e-9 {
		t.Fatalf("move rejected after push-up: (%v,%v), want (%v,%v)", gotX, gotZ, x+1, z)
	}
}

// TestCreativeBlockBreakAndPlace 验证创意模式的破坏与放置：
// 世界体素被修改、附近玩家收到 Block Update、破坏与放置的距离校验生效。
func TestCreativeBlockBreakAndPlace(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.GameMode = "creative"
	instance, conn := joinServer(t, cfg, "Builder")

	x, y, z := findDigTarget(t, instance)
	original := instance.testWorld().BlockAt(x, y, z)

	// 破坏：创意模式在 START_DESTROY_BLOCK 立即生效。
	sendPlayerAction(t, conn, 0, x, y, z, 1)
	expectBlockUpdate(t, conn, x, y, z, int32(world.AirBlock))
	if got := instance.testWorld().BlockAt(x, y, z); got != world.AirBlock {
		t.Fatalf("破坏后方块 = %d, want 空气", got)
	}

	// 放置：点击下方方块的顶面，把石头放回刚挖掉的位置（快捷栏 0 槽的初始石头）。
	sendUseItemOn(t, conn, x, y-1, z, 1)
	expectBlockUpdate(t, conn, x, y, z, int32(world.StoneBlock))
	if got := instance.testWorld().BlockAt(x, y, z); got != world.StoneBlock {
		t.Fatalf("放置后方块 = %d, want 石头（原方块 %d）", got, original)
	}

	// 距离校验：远处的破坏请求被忽略。
	fx, fy, fz := x+100, y, z+100
	before := instance.testWorld().BlockAt(fx, fy, fz)
	sendPlayerAction(t, conn, 0, fx, fy, fz, 1)
	if got := instance.testWorld().BlockAt(fx, fy, fz); got != before {
		t.Fatal("超出距离的破坏不应生效")
	}
}

// TestCreativeInventoryAndPlace 验证创造模式物品栏设置（Set Creative Mode Slot）
// 后可以放置对应方块。
func TestCreativeInventoryAndPlace(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.GameMode = "creative"
	cfg.StartingItems = nil // 清空初始物品，只用创造物品栏提供的物品
	instance, conn := joinServer(t, cfg, "Builder")

	// 把泥土放进快捷栏 2 槽并切换到该槽位。
	dirtID, err := registry.ItemID("minecraft:dirt")
	if err != nil {
		t.Fatal(err)
	}
	sendSetCreativeSlot(t, conn, 2, dirtID, 64)
	sendSetCarriedItem(t, conn, 2)

	x, y, z := findDigTarget(t, instance)
	sendPlayerAction(t, conn, 0, x, y, z, 1) // 先清出位置
	expectBlockUpdate(t, conn, x, y, z, int32(world.AirBlock))

	sendUseItemOn(t, conn, x, y-1, z, 1)
	expectBlockUpdate(t, conn, x, y, z, int32(world.DirtBlock))
	if got := instance.testWorld().BlockAt(x, y, z); got != world.DirtBlock {
		t.Fatalf("放置后方块 = %d, want 泥土", got)
	}
}

// TestHotbarSwitchUsesSelectedSlot 验证快捷栏切换按实机编码（Short）解析：
// 切到空槽位后右键地面不能从槽位 0 取物品放置，切到有物品的槽位则放置该槽位物品。
func TestHotbarSwitchUsesSelectedSlot(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:stone*8"}
	instance, conn := joinServer(t, cfg, "Switcher")

	x, y, z := findDigTarget(t, instance)
	// 生存模式：完整挖掘流程（START → 推进进度 → STOP 完成破坏）。
	breakBlockSurvival(t, conn, instance, activeSession(t, instance, "Switcher"), x, y, z)
	expectBlockUpdate(t, conn, x, y, z, int32(world.AirBlock))

	// 切到空的快捷栏槽位 4：右键不应放置任何方块（此前槽位号被误读为 0）。
	sendSetCarriedItem(t, conn, 4)
	sendUseItemOn(t, conn, x, y-1, z, 1)
	sendChatCommand(t, conn, "/list") // 处理屏障
	expectSystemChat(t, conn, "当前有")
	if got := instance.testWorld().BlockAt(x, y, z); got != world.AirBlock {
		t.Fatalf("空手右键不应放置方块，得到 %d", got)
	}
	if stack := activeSession(t, instance, "Switcher").inventory.Get(item.SlotHotbarStart); stack.Count != 8 {
		t.Fatalf("槽位 0 的物品不应被取用：%+v", stack)
	}

	// 把石头从快捷栏 0 移到快捷栏 4（窗口 0 菜单槽位 36 → 40），并切换过去。
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 0, 36, 0, protocol.ClickModePickup)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)
	sendContainerClick(t, conn, protocol.PlayerInventoryWindowID, 1, 40, 0, protocol.ClickModePickup)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetContainerSlot)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetCursorItem)
	sendSetCarriedItem(t, conn, 4)

	sendUseItemOn(t, conn, x, y-1, z, 1)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")
	if got := instance.testWorld().BlockAt(x, y, z); got != world.StoneBlock {
		t.Fatalf("切换到快捷栏 4 后放置的方块 = %d, want 石头", got)
	}
	if stack := activeSession(t, instance, "Switcher").inventory.Get(4); stack.Count != 7 {
		t.Fatalf("快捷栏 4 的石头应剩余 7：%+v", stack)
	}
}

// TestSurvivalBlockPlaceConsumesItem 验证生存模式放置会消耗物品
// （初始石头 ×1，第二次放置因物品耗尽被忽略）。
func TestSurvivalBlockPlaceConsumesItem(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:stone*1"}
	instance, conn := joinServer(t, cfg, "Miner")

	x, y, z := findDigTarget(t, instance)
	// 生存模式：完整挖掘流程破坏目标方块。
	breakBlockSurvival(t, conn, instance, activeSession(t, instance, "Miner"), x, y, z)
	expectBlockUpdate(t, conn, x, y, z, int32(world.AirBlock))

	sendUseItemOn(t, conn, x, y-1, z, 1)
	expectBlockUpdate(t, conn, x, y, z, int32(world.StoneBlock))
	// 槽位同步（数量减为 0 → 空槽位）。
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetPlayerInventory)

	// 第二个目标列：物品已耗尽，放置不应生效。
	x2, y2, z2 := 0, 0, 0
	for _, cx := range []int{x + 1, x + 2, x + 3} {
		if state, cy, ok := instance.testWorld().TopBlock(cx, z); ok && state != world.WaterBlock {
			x2, y2, z2 = cx, cy, z
			break
		}
	}
	if x2 == 0 && y2 == 0 && z2 == 0 {
		t.Fatal("找不到第二个测试目标")
	}
	breakBlockSurvival(t, conn, instance, activeSession(t, instance, "Miner"), x2, y2, z2)
	expectBlockUpdate(t, conn, x2, y2, z2, int32(world.AirBlock))
	sendUseItemOn(t, conn, x2, y2-1, z2, 1)
	if got := instance.testWorld().BlockAt(x2, y2, z2); got != world.AirBlock {
		t.Fatalf("物品耗尽后不应能放置方块（方块 = %d）", got)
	}
}

// TestSetCreativeModeSlotMenuMapping 验证创造物品栏设置（Set Creative Mode Slot）
// 的槽位映射：线上槽位是玩家菜单编号（5–8 盔甲、9–35 主背包、36–44 快捷栏、
// 45 副手），服务端必须映射到物品栏内部槽位后再存储与回执。
// 回归背景：曾把菜单槽位直接当作内部槽位，创造模式把物品放到快捷栏会被存进
// 盔甲/副手槽（并回执错误的槽位），造成客户端物品错乱、丢弃/拾取结果不符。
func TestSetCreativeModeSlotMenuMapping(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.GameMode = "creative"
	cfg.StartingItems = nil
	instance, conn := joinServer(t, cfg, "MenuSlots")
	player := findSession(t, instance, "MenuSlots")

	stone, err := registry.ItemID("minecraft:stone")
	if err != nil {
		t.Fatal(err)
	}

	// 快捷栏 3 ← 菜单 39：内部槽位 3，且绝不能落到内部 39（头盔）。
	sendSetCreativeSlotMenu(t, conn, 39, stone, 7)
	reply := expectPlayPacket(t, conn, protocol.PlayPacketIDSetPlayerInventory)
	if slot, count := packetField(t, reply, 2), packetField(t, reply, 3); slot != 3 || count != 7 {
		t.Fatalf("回执槽位/数量 = %d/%d, want 3/7", slot, count)
	}
	if stack := player.inventory.Get(3); stack.ItemID != stone || stack.Count != 7 {
		t.Fatalf("内部槽位 3 = %+v, want 石头 x7", stack)
	}
	if stack := player.inventory.Get(39); !stack.IsEmpty() {
		t.Fatalf("头盔槽被误写：%+v", stack)
	}

	// 盔甲：菜单 5（头）→ 内部 39。
	sendSetCreativeSlotMenu(t, conn, 5, stone, 1)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetPlayerInventory)
	if stack := player.inventory.Get(item.SlotHead); stack.ItemID != stone || stack.Count != 1 {
		t.Fatalf("头盔槽 = %+v, want 石头 x1", stack)
	}

	// 主背包：菜单 20 → 内部 20。
	sendSetCreativeSlotMenu(t, conn, 20, stone, 64)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetPlayerInventory)
	if stack := player.inventory.Get(20); stack.ItemID != stone || stack.Count != 64 {
		t.Fatalf("主背包槽 20 = %+v, want 石头 x64", stack)
	}

	// 副手：菜单 45 → 内部 40。
	sendSetCreativeSlotMenu(t, conn, 45, stone, 3)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetPlayerInventory)
	if stack := player.inventory.Get(item.SlotOffhand); stack.ItemID != stone || stack.Count != 3 {
		t.Fatalf("副手槽 = %+v, want 石头 x3", stack)
	}

	// 合成格（菜单 1–4）、结果槽（0）、越界槽位（46）与超过堆叠上限（100）
	// 均不改动任何槽位。
	before := player.inventory.Slots()
	sendSetCreativeSlotMenu(t, conn, 2, stone, 64)
	sendSetCreativeSlotMenu(t, conn, 0, stone, 64)
	sendSetCreativeSlotMenu(t, conn, 46, stone, 64)
	sendSetCreativeSlotMenu(t, conn, 20, stone, 100)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")
	if after := player.inventory.Slots(); !item.EqualStacks(after[:], before[:]) {
		t.Fatalf("合成格/非法槽位不应改动物品栏")
	}

	// 负数槽位（创造界面丢弃手势）：与原版一致，物品沿视线掉落到世界。
	beforeDrop := player.inventory.Slots()
	sendSetCreativeSlotMenu(t, conn, -1, stone, 5)
	expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)
	instance.entityMu.Lock()
	droppedCount := 0
	var dropped item.Stack
	for _, e := range instance.items {
		droppedCount++
		dropped = e.Stack
	}
	instance.entityMu.Unlock()
	if droppedCount != 1 || dropped.ItemID != stone || dropped.Count != 5 {
		t.Fatalf("负数槽位丢弃 = %d 个实体、堆叠 %+v, want 1 个石头 x5", droppedCount, dropped)
	}
	if after := player.inventory.Slots(); !item.EqualStacks(after[:], beforeDrop[:]) {
		t.Fatal("负数槽位不应改动物品栏")
	}
}

// TestCreativeRedstoneTakeOutAndPlace 验证创造模式取出红石粉（经菜单槽位
// 映射到快捷栏）后能正常放置为红石线，且创造模式不消耗物品。
func TestCreativeRedstoneTakeOutAndPlace(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.GameMode = "creative"
	cfg.StartingItems = nil
	instance, conn := joinServer(t, cfg, "Redstoner")

	dust, err := registry.ItemID("minecraft:redstone")
	if err != nil {
		t.Fatal(err)
	}
	// 从创造物品栏取出红石粉到快捷栏 4（线上菜单槽位 40），并切换到该槽位。
	sendSetCreativeSlot(t, conn, 4, dust, 64)
	sendSetCarriedItem(t, conn, 4)

	x, y, z := findDigTarget(t, instance)
	sendPlayerAction(t, conn, 0, x, y, z, 1) // 先清出位置
	expectBlockUpdate(t, conn, x, y, z, int32(world.AirBlock))

	sendUseItemOn(t, conn, x, y-1, z, 1)
	// /list 往返作为处理屏障：放置与红石网络重算均已完成。
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")
	name, _, ok := registry.StateProps(instance.testWorld().BlockAt(x, y, z))
	if !ok || name != "minecraft:redstone_wire" {
		t.Fatalf("放置后方块 = %v (ok=%v), want 红石线", name, ok)
	}
	if stack := activeSession(t, instance, "Redstoner").inventory.Get(4); stack.ItemID != dust || stack.Count != 64 {
		t.Fatalf("创造模式放置后物品 = %+v, want 红石粉 x64", stack)
	}
}
