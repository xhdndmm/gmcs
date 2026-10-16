//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// closeContainerPacket 解析 Close Container 包并返回窗口编号。
func closeContainerPacket(t *testing.T, payload []byte) int32 {
	t.Helper()
	_, offset, err := protocol.DecodeVarInt(payload)
	if err != nil {
		t.Fatal(err)
	}
	windowID, _, err := protocol.DecodeVarInt(payload[offset:])
	if err != nil {
		t.Fatal(err)
	}
	return windowID
}

// TestWindowClosesWhenContainerDestroyed 验证容器方块被破坏（含非玩家来源，
// 如爆炸）时其他观察者立即收到 Close Container，并在下一个数据包时清理会话状态
// （窗口字段由各会话串行访问，不能跨 goroutine 直接修改）。
func TestWindowClosesWhenContainerDestroyed(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Watcher")
	player := activeSession(t, instance, "Watcher")

	cx, cy, cz := chestPositions(t, instance)
	placeChest(t, instance, cx, cy, cz)
	sendUseItemOn(t, conn, cx, cy, cz, 1)
	windowID, _ := openScreen(t, conn)
	expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)

	def := instance.containerDefs["minecraft:chest"]
	if def == nil {
		t.Fatal("缺少箱子容器定义")
	}
	// 非玩家来源（breaker = nil）：与其他玩家破坏同一条销毁路径。
	instance.destroyContainer(world.DimensionOverworld, nil, cx, cy, cz, def)

	payload := expectPlayPacket(t, conn, protocol.PlayPacketIDCloseContainer)
	if got := closeContainerPacket(t, payload); got != windowID {
		t.Fatalf("关闭窗口编号 = %d, want %d", got, windowID)
	}
	// 下一个数据包触发 stillValid 校验 → 清理会话状态（并补发一次关闭包）。
	// 会话窗口字段由会话 goroutine 串行访问：用 processBarrier 等待处理完成后再读取。
	sendPlayerPosition(t, conn, float64(cx)+0.5, float64(cy), float64(cz)+0.5, true)
	processBarrier(t, conn)
	if state := player.connContainer(); state != nil {
		t.Fatal("销毁容器后会话状态未被清理")
	}
}

// TestWindowClosesWhenPlayerWalksAway 验证玩家离开交互距离后窗口被主动关闭
// （原版 stillValid 每 tick 校验）。
func TestWindowClosesWhenPlayerWalksAway(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Walker")
	player := activeSession(t, instance, "Walker")

	cx, cy, cz := chestPositions(t, instance)
	placeChest(t, instance, cx, cy, cz)
	sendUseItemOn(t, conn, cx, cy, cz, 1)
	windowID, _ := openScreen(t, conn)
	expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)

	// 先走到 2 格外：窗口应保持打开。
	sendPlayerPosition(t, conn, float64(cx)+2.5, float64(cy), float64(cz)+0.5, true)
	processBarrier(t, conn)
	if player.connContainer() == nil {
		t.Fatal("距离内的窗口不应被关闭")
	}

	// 走到 20 格外：下一个数据包触发校验并关闭窗口。
	// 直接设置会话位置（等价于玩家走过去；避免单包位移速度上限的干扰）。
	player.setPlayerPosition(float64(cx)+20.5, float64(cy), float64(cz)+0.5, 0, 0)
	sendPlayerPosition(t, conn, float64(cx)+20.5, float64(cy), float64(cz)+0.5, true)
	payload := expectPlayPacket(t, conn, protocol.PlayPacketIDCloseContainer)
	if got := closeContainerPacket(t, payload); got != windowID {
		t.Fatalf("关闭窗口编号 = %d, want %d", got, windowID)
	}
	if state := player.connContainer(); state != nil {
		t.Fatal("远离后会话仍持有窗口状态")
	}
}

// TestStonecutterWindowClosesWhenBlockReplaced 验证切石机方块被替换（非破坏者）
// 后窗口被主动关闭。
func TestStonecutterWindowClosesWhenBlockReplaced(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.GameMode = "creative"
	instance, conn := joinServer(t, cfg, "CutterClose")
	player := activeSession(t, instance, "CutterClose")

	x, y, z := placeMenuBlock(t, instance, "minecraft:stonecutter")
	sendUseItemOn(t, conn, x, y, z, 1)
	windowID, _ := openScreen(t, conn)
	expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)
	if player.getStonecutter() == nil {
		t.Fatal("切石机窗口未打开")
	}

	// 模拟另一方把方块替换掉（切石机没有容器状态，不走 destroyContainer）。
	if !instance.testWorld().SetBlock(x, y, z, world.AirBlock) {
		t.Fatal("无法替换切石机方块")
	}
	sendPlayerPosition(t, conn, float64(x)+0.5, float64(y)+1, float64(z)+0.5, true)
	payload := expectPlayPacket(t, conn, protocol.PlayPacketIDCloseContainer)
	if got := closeContainerPacket(t, payload); got != windowID {
		t.Fatalf("关闭窗口编号 = %d, want %d", got, windowID)
	}
	if player.getStonecutter() != nil {
		t.Fatal("方块被替换后切石机窗口状态未清理")
	}
	// 方块替换后输入槽中的物品应已归还（此处未放物品，仅验证状态清理不 panic）。
}

// TestAnvilWindowClosesWhenBlockReplaced 验证铁砧被替换后窗口被主动关闭
// （anvilStillValid 的周期性检查路径）。
func TestAnvilWindowClosesWhenBlockReplaced(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "AnvilClose")
	player := activeSession(t, instance, "AnvilClose")

	x, y, z := placeMenuBlock(t, instance, "minecraft:anvil")
	sendUseItemOn(t, conn, x, y, z, 1)
	windowID, _ := openScreen(t, conn)
	expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)
	if player.getAnvil() == nil {
		t.Fatal("铁砧窗口未打开")
	}

	if !instance.testWorld().SetBlock(x, y, z, world.AirBlock) {
		t.Fatal("无法替换铁砧方块")
	}
	sendPlayerPosition(t, conn, float64(x)+0.5, float64(y)+1, float64(z)+0.5, true)
	payload := expectPlayPacket(t, conn, protocol.PlayPacketIDCloseContainer)
	if got := closeContainerPacket(t, payload); got != windowID {
		t.Fatalf("关闭窗口编号 = %d, want %d", got, windowID)
	}
	if player.getAnvil() != nil {
		t.Fatal("方块被替换后铁砧窗口状态未清理")
	}
}

// TestCraftingWindowClosesWhenPlayerWalksAway 验证工作台窗口离开交互距离后关闭，
// 且合成格中的物品归还背包。
func TestCraftingWindowClosesWhenPlayerWalksAway(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:oak_planks*4"}
	instance, conn := joinServer(t, cfg, "CrafterClose")
	player := activeSession(t, instance, "CrafterClose")

	x, y, z := placeMenuBlock(t, instance, "minecraft:crafting_table")
	sendUseItemOn(t, conn, x, y, z, 1)
	windowID, _ := openScreen(t, conn)
	expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)
	if player.getOpenCraft() == nil {
		t.Fatal("工作台窗口未打开")
	}

	// 把快捷栏 0 的木板放进 3×3 合成格的第一个槽位（窗口槽位 1）。
	sendContainerClick(t, conn, windowID, 0, 37, 0, protocol.ClickModePickup)
	sendContainerClick(t, conn, windowID, 0, 1, 0, protocol.ClickModePickup)
	processBarrier(t, conn)

	// 离开交互距离：窗口关闭，合成格物品回到背包。
	// 直接设置会话位置（等价于玩家走过去；避免单包位移速度上限的干扰）。
	player.setPlayerPosition(float64(x)+20.5, float64(y), float64(z)+0.5, 0, 0)
	sendPlayerPosition(t, conn, float64(x)+20.5, float64(y), float64(z)+0.5, true)
	payload := expectPlayPacket(t, conn, protocol.PlayPacketIDCloseContainer)
	if got := closeContainerPacket(t, payload); got != windowID {
		t.Fatalf("关闭窗口编号 = %d, want %d", got, windowID)
	}
	if player.getOpenCraft() != nil {
		t.Fatal("远离后工作台窗口状态未清理")
	}
	planksID, err := registry.ItemID("minecraft:oak_planks")
	if err != nil {
		t.Fatal(err)
	}
	total := int32(0)
	for slot := 0; slot < item.InventorySlots; slot++ {
		if stack := player.inventory.Get(slot); stack.ItemID == planksID {
			total += stack.Count
		}
	}
	if total != 4 {
		t.Fatalf("关闭窗口后背包中的木板 = %d, want 4（合成格物品应归还）", total)
	}
}

// TestWindowStaysOpenWhenBlockIntact 验证方块与距离都正常时窗口不会被误关闭
// （防止校验逻辑误判导致玩家窗口被踢掉）。
func TestWindowStaysOpenWhenBlockIntact(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Stayer")
	player := activeSession(t, instance, "Stayer")

	cx, cy, cz := chestPositions(t, instance)
	placeChest(t, instance, cx, cy, cz)
	sendUseItemOn(t, conn, cx, cy, cz, 1)
	windowID, _ := openScreen(t, conn)
	expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)

	// 连续多次小幅移动（都在距离内）：窗口应一直保持打开。
	for i := 0; i < 5; i++ {
		sendPlayerPosition(t, conn, float64(cx)+0.5+float64(i)*0.5, float64(cy), float64(cz)+2.5, true)
		processBarrier(t, conn)
		if player.connContainer() == nil {
			t.Fatalf("第 %d 次移动后窗口被误关闭", i)
		}
	}

	// 竖直方向也在距离内（原版按方块距离判定，高度差 4 格仍有效）。
	sendPlayerPosition(t, conn, float64(cx)+0.5, float64(cy)+math.Floor(4), float64(cz)+0.5, true)
	processBarrier(t, conn)
	if player.connContainer() == nil {
		t.Fatal("高度差 4 格内窗口被误关闭")
	}

	// 主动关闭窗口：会话断开时的清理也会写窗口字段（窗口字段由会话串行访问），
	// 测试结束前先收起窗口，避免与会话 goroutine 的清理写并发。
	closePacket := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDContainerClose)
	closePacket = protocol.AppendVarInt(closePacket, windowID)
	if err := protocol.WritePacketWithCompression(conn, closePacket, compressionThreshold); err != nil {
		t.Fatal(err)
	}
	processBarrier(t, conn)
}
