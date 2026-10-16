//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
)

// TestSmithingTableTrim 验证锻造台纹饰配方（smithing_trim）：
// 模板 + 基底 + 添加物 → 基底副本 + trim 组件；取走产物消耗三个输入各 1 个；
// 相同图案与材质重复应用时无产物（与原版 SmithingTrimRecipe.applyTrim 一致）。
func TestSmithingTableTrim(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.GameMode = "creative"
	instance, conn := joinServer(t, cfg, "Trimmed")

	templateID, err := registry.ItemID("minecraft:bolt_armor_trim_smithing_template")
	if err != nil {
		t.Fatal(err)
	}
	baseID, err := registry.ItemID("minecraft:netherite_chestplate")
	if err != nil {
		t.Fatal(err)
	}
	goldID, err := registry.ItemID("minecraft:gold_ingot")
	if err != nil {
		t.Fatal(err)
	}
	x, y, z := placeMenuBlock(t, instance, "minecraft:smithing_table")
	sendSetCreativeSlot(t, conn, 0, templateID, 1)
	sendSetCreativeSlot(t, conn, 1, baseID, 1)
	sendSetCreativeSlot(t, conn, 2, goldID, 1)

	sendUseItemOn(t, conn, x, y, z, 1)
	windowID, _ := openScreen(t, conn)
	expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)

	// 快捷栏 0–2 → 锻造台输入槽 0–2。
	for i := 0; i < 3; i++ {
		sendContainerClick(t, conn, windowID, 0, int16(31+i), 0, protocol.ClickModePickup)
		sendContainerClick(t, conn, windowID, 0, int16(i), 0, protocol.ClickModePickup)
	}
	player := activeSession(t, instance, "Trimmed")
	processBarrier(t, conn)
	state := player.getSmithing()
	if state == nil {
		t.Fatal("锻造台窗口未打开")
	}
	result := state.resultStack()
	if result.ItemID != baseID || result.Count != 1 ||
		result.TrimPattern != "minecraft:bolt" || result.TrimMaterial != "minecraft:gold" {
		t.Fatalf("纹饰结果 = %+v, want 下界合金胸甲 + bolt/gold 纹饰", result)
	}

	// 取走产物：光标获得带纹饰的胸甲，三个输入清空。
	sendContainerClick(t, conn, windowID, 0, smithingResultSlot, 0, protocol.ClickModePickup)
	processBarrier(t, conn)
	cursor := player.getCursor()
	if cursor.ItemID != baseID || cursor.TrimPattern != "minecraft:bolt" || cursor.TrimMaterial != "minecraft:gold" {
		t.Fatalf("取走后的光标 = %+v, want 带 gold/bolt 纹饰的下界合金胸甲", cursor)
	}
	for i := 0; i < 3; i++ {
		if input := state.inputStack(i); !input.IsEmpty() {
			t.Fatalf("取走产物后输入槽 %d = %+v, want 空", i, input)
		}
	}

	// 同一图案 + 同一材质重复应用：无产物；换材质可覆盖旧纹饰。
	state.setInput(smithingTemplateSlot, item.Stack{ItemID: templateID, Count: 1})
	state.setInput(smithingBaseSlot, cursor)
	state.setInput(smithingAdditionSlot, item.Stack{ItemID: goldID, Count: 1})
	if result := state.resultStack(); !result.IsEmpty() {
		t.Fatalf("相同纹饰重复应用应无产物，得到 %+v", result)
	}
	ironID, err := registry.ItemID("minecraft:iron_ingot")
	if err != nil {
		t.Fatal(err)
	}
	state.setInput(smithingAdditionSlot, item.Stack{ItemID: ironID, Count: 1})
	result = state.resultStack()
	if result.IsEmpty() || result.TrimMaterial != "minecraft:iron" || result.TrimPattern != "minecraft:bolt" {
		t.Fatalf("换材质后的纹饰结果 = %+v, want iron/bolt", result)
	}
	// 基底上的耐久等其他组件在纹饰后保留。
	state.setInput(smithingBaseSlot, item.Stack{ItemID: baseID, Count: 1, Damage: 42})
	result = state.resultStack()
	if result.Damage != 42 || result.TrimPattern != "minecraft:bolt" || result.TrimMaterial != "minecraft:iron" {
		t.Fatalf("纹饰结果应保留基底组件：%+v", result)
	}
}

// TestSmithingSlotRestrictions 验证锻造台输入槽的放入限制
// （原版槽位 mayPlace 由 smithing_template/base/addition 属性集驱动）。
func TestSmithingSlotRestrictions(t *testing.T) {
	templateID, err := registry.ItemID("minecraft:bolt_armor_trim_smithing_template")
	if err != nil {
		t.Fatal(err)
	}
	baseID, err := registry.ItemID("minecraft:diamond_chestplate")
	if err != nil {
		t.Fatal(err)
	}
	additionID, err := registry.ItemID("minecraft:gold_ingot")
	if err != nil {
		t.Fatal(err)
	}
	dirtID, err := registry.ItemID("minecraft:dirt")
	if err != nil {
		t.Fatal(err)
	}
	backing := smithingSlotBacking{state: &smithingState{}}
	if !backing.CanPlace(smithingTemplateSlot, itemStackFor(templateID, 1)) {
		t.Fatal("模板槽应接受锻造模板")
	}
	if backing.CanPlace(smithingTemplateSlot, itemStackFor(baseID, 1)) {
		t.Fatal("模板槽不应接受护甲基底")
	}
	if !backing.CanPlace(smithingBaseSlot, itemStackFor(baseID, 1)) {
		t.Fatal("基底槽应接受可锻造护甲")
	}
	if backing.CanPlace(smithingBaseSlot, itemStackFor(dirtID, 1)) {
		t.Fatal("基底槽不应接受泥土")
	}
	if !backing.CanPlace(smithingAdditionSlot, itemStackFor(additionID, 1)) {
		t.Fatal("添加物槽应接受纹饰材质物品")
	}
	if backing.CanPlace(smithingAdditionSlot, itemStackFor(dirtID, 1)) {
		t.Fatal("添加物槽不应接受泥土")
	}
	// 非输入槽（结果槽）不在 CanPlace 的限制范围内（Supported 已禁止交互）。
	if !backing.CanPlace(smithingResultSlot, itemStackFor(dirtID, 1)) {
		t.Fatal("结果槽的 CanPlace 应恒为 true（交互由 Supported 拦截）")
	}
}
