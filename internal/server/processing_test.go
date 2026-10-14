//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"net"
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
)

// sendContainerButtonClick 发送 Container Button Click 包（切石机选择配方）。
func sendContainerButtonClick(t *testing.T, conn net.Conn, windowID, buttonID int32) {
	t.Helper()
	packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDContainerButtonClick)
	packet = protocol.AppendVarInt(packet, windowID)
	packet = protocol.AppendVarInt(packet, buttonID)
	if err := protocol.WritePacketWithCompression(conn, packet, compressionThreshold); err != nil {
		t.Fatal(err)
	}
}

// sendPlaceRecipe 发送 Place Recipe 包（配方书点击）。
func sendPlaceRecipe(t *testing.T, conn net.Conn, windowID, displayID int32, makeAll bool) {
	t.Helper()
	packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDPlaceRecipe)
	packet = protocol.AppendVarInt(packet, windowID)
	packet = protocol.AppendVarInt(packet, displayID)
	packet = protocol.AppendBool(packet, makeAll)
	if err := protocol.WritePacketWithCompression(conn, packet, compressionThreshold); err != nil {
		t.Fatal(err)
	}
}

// placeMenuBlock 在出生点附近放置一个方块并返回坐标。
func placeMenuBlock(t *testing.T, instance *Server, blockName string) (int, int, int) {
	t.Helper()
	state, ok := registry.BlockStateIDs[blockName]
	if !ok {
		t.Fatalf("缺少方块状态 %s", blockName)
	}
	x, y, z := buildSpotNearSpawn(t, instance)
	if !instance.testWorld().SetBlock(x, y, z, state) {
		t.Fatalf("无法放置 %s", blockName)
	}
	return x, y, z
}

// TestStonecutterCutsStone 验证切石机：放入石头自动选中第一个配方、
// Container Button Click 切换配方、取走产物消耗输入。
func TestStonecutterCutsStone(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.GameMode = "creative"
	instance, conn := joinServer(t, cfg, "Cutter")

	stoneID, err := registry.ItemID("minecraft:stone")
	if err != nil {
		t.Fatal(err)
	}
	x, y, z := placeMenuBlock(t, instance, "minecraft:stonecutter")
	sendSetCreativeSlot(t, conn, 0, stoneID, 1)

	sendUseItemOn(t, conn, x, y, z, 1)
	windowID, menuType := openScreen(t, conn)
	wantMenu, ok := registry.StaticEntryID("minecraft:menu", "minecraft:stonecutter")
	if !ok {
		t.Fatal("缺少 stonecutter 菜单类型")
	}
	if menuType != wantMenu {
		t.Fatalf("菜单类型 = %d, want %d", menuType, wantMenu)
	}
	content := expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)
	if gotWindow, slots := containerContent(t, content); gotWindow != windowID || len(slots) != stonecutterSlots {
		t.Fatalf("窗口 = %d 槽位 = %d, want %d/%d", gotWindow, len(slots), windowID, stonecutterSlots)
	}

	// 快捷栏槽 0 在切石机窗口中的槽位 = 29；拾取后放入输入槽。
	sendContainerClick(t, conn, windowID, 0, 29, 0, protocol.ClickModePickup)
	sendContainerClick(t, conn, windowID, 0, stonecutterInputSlot, 0, protocol.ClickModePickup)

	player := activeSession(t, instance, "Cutter")
	processBarrier(t, conn)
	state := player.getStonecutter()
	if state == nil {
		t.Fatal("切石机窗口未打开")
	}
	if input := state.inputStack(); input.ItemID != stoneID || input.Count != 1 {
		t.Fatalf("输入槽 = %+v, want 石头 x1", input)
	}
	matches := registry.StonecuttingRecipesFor(stoneID)
	if len(matches) < 3 {
		t.Fatalf("石头应有至少 3 个切石机配方，实际 %d", len(matches))
	}
	first, _ := registry.StonecuttingRecipeAt(matches[0])
	if result := state.resultStack(); result.ItemID != first.Result || result.Count != first.ResultCount {
		t.Fatalf("自动选中的结果 = %+v, want %+v", result, first)
	}

	// 选择第 3 个配方。
	sendContainerButtonClick(t, conn, windowID, 2)
	processBarrier(t, conn)
	third, _ := registry.StonecuttingRecipeAt(matches[2])
	if selected := state.selectedIndex(); selected != matches[2] {
		t.Fatalf("选中下标 = %d, want %d", selected, matches[2])
	}
	if result := state.resultStack(); result.ItemID != third.Result || result.Count != third.ResultCount {
		t.Fatalf("切换后的结果 = %+v, want %+v", result, third)
	}

	// 取走产物：光标获得产物，输入槽清空。
	sendContainerClick(t, conn, windowID, 0, stonecutterResultSlot, 0, protocol.ClickModePickup)
	processBarrier(t, conn)
	if cursor := player.getCursor(); cursor.ItemID != third.Result || cursor.Count != third.ResultCount {
		t.Fatalf("取走后的光标 = %+v, want %+v", cursor, third)
	}
	if input := state.inputStack(); !input.IsEmpty() {
		t.Fatalf("取走产物后输入槽 = %+v, want 空", input)
	}
}

// TestSmithingTableUpgrades 验证锻造台：模板/基底/添加物 → 下界合金升级，
// 取走产物消耗三个输入各 1 个。
func TestSmithingTableUpgrades(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.GameMode = "creative"
	instance, conn := joinServer(t, cfg, "Smithed")

	templateID, err := registry.ItemID("minecraft:netherite_upgrade_smithing_template")
	if err != nil {
		t.Fatal(err)
	}
	baseID, err := registry.ItemID("minecraft:diamond_chestplate")
	if err != nil {
		t.Fatal(err)
	}
	additionID, err := registry.ItemID("minecraft:netherite_ingot")
	if err != nil {
		t.Fatal(err)
	}
	resultID, err := registry.ItemID("minecraft:netherite_chestplate")
	if err != nil {
		t.Fatal(err)
	}
	x, y, z := placeMenuBlock(t, instance, "minecraft:smithing_table")
	sendSetCreativeSlot(t, conn, 0, templateID, 1)
	sendSetCreativeSlot(t, conn, 1, baseID, 1)
	sendSetCreativeSlot(t, conn, 2, additionID, 1)

	sendUseItemOn(t, conn, x, y, z, 1)
	windowID, menuType := openScreen(t, conn)
	wantMenu, ok := registry.StaticEntryID("minecraft:menu", "minecraft:smithing")
	if !ok {
		t.Fatal("缺少 smithing 菜单类型")
	}
	if menuType != wantMenu {
		t.Fatalf("菜单类型 = %d, want %d", menuType, wantMenu)
	}
	content := expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)
	if _, slots := containerContent(t, content); len(slots) != smithingSlots {
		t.Fatalf("窗口槽位数 = %d, want %d", len(slots), smithingSlots)
	}

	// 快捷栏 0–2 在锻造台窗口中的槽位 = 31–33。
	for i := 0; i < 3; i++ {
		sendContainerClick(t, conn, windowID, 0, int16(31+i), 0, protocol.ClickModePickup)
		sendContainerClick(t, conn, windowID, 0, int16(i), 0, protocol.ClickModePickup)
	}
	player := activeSession(t, instance, "Smithed")
	processBarrier(t, conn)
	state := player.getSmithing()
	if state == nil {
		t.Fatal("锻造台窗口未打开")
	}
	if result := state.resultStack(); result.ItemID != resultID || result.Count != 1 {
		t.Fatalf("锻造结果 = %+v, want 下界合金胸甲 x1", result)
	}

	// 取走产物：光标获得结果，三个输入清空。
	sendContainerClick(t, conn, windowID, 0, smithingResultSlot, 0, protocol.ClickModePickup)
	processBarrier(t, conn)
	if cursor := player.getCursor(); cursor.ItemID != resultID || cursor.Count != 1 {
		t.Fatalf("取走后的光标 = %+v, want 下界合金胸甲 x1", cursor)
	}
	for i := 0; i < 3; i++ {
		if input := state.inputStack(i); !input.IsEmpty() {
			t.Fatalf("取走产物后输入槽 %d = %+v, want 空", i, input)
		}
	}
}
