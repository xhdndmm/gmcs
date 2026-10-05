package server

import (
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// TestBlockDropTable 校验简化掉落表：同名默认、不同名特例、无掉落方块。
func TestBlockDropTable(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer instance.world.Close()

	blockState := func(name string) uint16 {
		state, err := registry.BlockStateID(name)
		if err != nil {
			t.Fatalf("未知方块 %s：%v", name, err)
		}
		return state
	}

	cases := []struct {
		block string
		item  string
		count int32
	}{
		{"minecraft:stone", "minecraft:cobblestone", 1},
		{"minecraft:cobblestone", "minecraft:cobblestone", 1},
		{"minecraft:grass_block", "minecraft:dirt", 1},
		{"minecraft:dirt", "minecraft:dirt", 1},
		{"minecraft:sand", "minecraft:sand", 1},
		{"minecraft:oak_log", "minecraft:oak_log", 1},
		{"minecraft:clay", "minecraft:clay_ball", 4},
		{"minecraft:snow_block", "minecraft:snowball", 4},
		{"minecraft:melon", "minecraft:melon_slice", 5},
		{"minecraft:coal_ore", "minecraft:coal", 1},
		{"minecraft:deepslate_coal_ore", "minecraft:coal", 1},
		{"minecraft:iron_ore", "minecraft:raw_iron", 1},
		{"minecraft:deepslate_iron_ore", "minecraft:raw_iron", 1},
		{"minecraft:copper_ore", "minecraft:raw_copper", 1},
		{"minecraft:gold_ore", "minecraft:raw_gold", 1},
		{"minecraft:diamond_ore", "minecraft:diamond", 1},
		{"minecraft:emerald_ore", "minecraft:emerald", 1},
		{"minecraft:lapis_ore", "minecraft:lapis_lazuli", 1},
		{"minecraft:redstone_ore", "minecraft:redstone", 1},
		{"minecraft:nether_quartz_ore", "minecraft:quartz", 1},
		{"minecraft:oak_leaves", "", 0},
		{"minecraft:glass", "", 0},
		{"minecraft:white_stained_glass", "", 0},
		{"minecraft:glass_pane", "", 0},
		{"minecraft:ice", "", 0},
		{"minecraft:water", "", 0},
		{"minecraft:bedrock", "", 0},
		{"minecraft:oak_wall_sign", "", 0}, // 无对应物品的方块
	}
	for _, tc := range cases {
		item, count := instance.blockDrop(blockState(tc.block))
		if item != tc.item || count != tc.count {
			t.Errorf("blockDrop(%s) = (%q, %d)，want (%q, %d)", tc.block, item, count, tc.item, tc.count)
		}
	}

	// 未知状态 ID 不掉落。
	if item, count := instance.blockDrop(0xFFFF); item != "" || count != 0 {
		t.Errorf("blockDrop(未知状态) = (%q, %d)，want 无掉落", item, count)
	}
	// 所有特例条目都必须真实存在于物品注册表（防止表里写错名字）。
	for block, spec := range blockDropOverrides {
		if _, err := registry.ItemID(spec.Item); err != nil {
			t.Errorf("特例 %s 的掉落物 %s 不在物品注册表中", block, spec.Item)
		}
	}
}

// TestSurvivalBlockBreakDrops 验证生存模式破坏方块会生成掉落物
// （实体 + Item 元数据包），创意模式破坏不掉落。
func TestSurvivalBlockBreakDrops(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Miner")

	x, y, z := findDigTarget(t, instance)
	state := instance.world.BlockAt(x, y, z)
	wantName := ""
	switch state {
	case world.GrassBlock:
		wantName = "minecraft:dirt"
	case world.SandBlock:
		wantName = "minecraft:sand"
	case world.StoneBlock:
		wantName = "minecraft:cobblestone"
	default:
		t.Skipf("出生点顶层方块 %d 没有断言用的预期掉落", state)
	}
	wantItemID, err := registry.ItemID(wantName)
	if err != nil {
		t.Fatal(err)
	}

	// 生存模式：客户端完成挖掘后提交 STOP_DESTROY_BLOCK（状态 2）。
	sendPlayerAction(t, conn, 2, x, y, z, 1)
	expectBlockUpdate(t, conn, x, y, z, int32(world.AirBlock))
	expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)
	metadata := expectPlayPacket(t, conn, protocol.PlayPacketIDEntityMetadata)
	// 元数据包字段：包 ID、实体 ID、索引（8 = item）、类型（7 = item_stack）、
	// 槽位数量、物品 ID、组件增量/减量（均为 0）。
	if index := packetField(t, metadata, 3); index != protocol.EntityMetadataItemStackIndex {
		t.Fatalf("元数据索引 = %d，want %d", index, protocol.EntityMetadataItemStackIndex)
	}
	if entryType := packetField(t, metadata, 4); entryType != 7 {
		t.Fatalf("元数据类型 = %d，want 7（item_stack）", entryType)
	}
	if count := packetField(t, metadata, 5); count != 1 {
		t.Fatalf("掉落物数量 = %d，want 1", count)
	}
	if itemID := packetField(t, metadata, 6); itemID != wantItemID {
		t.Fatalf("掉落物物品 ID = %d，want %d（%s）", itemID, wantItemID, wantName)
	}

	instance.entityMu.Lock()
	dropped := make([]*itemEntity, 0, len(instance.items))
	for _, e := range instance.items {
		dropped = append(dropped, e)
	}
	instance.entityMu.Unlock()
	if len(dropped) != 1 || dropped[0].Stack.ItemID != wantItemID || dropped[0].Stack.Count != 1 {
		t.Fatalf("掉落物实体 = %+v，want 1 个 %s", dropped, wantName)
	}

	// 创意模式：破坏不掉落物品。
	findSession(t, instance, "Miner").setGameMode(uint8(config.GameModeCreative))
	x2, y2, z2 := findDigTarget(t, instance)
	sendPlayerAction(t, conn, 0, x2, y2, z2, 1)
	expectBlockUpdate(t, conn, x2, y2, z2, int32(world.AirBlock))
	instance.entityMu.Lock()
	remaining := len(instance.items)
	instance.entityMu.Unlock()
	if remaining != 1 {
		t.Fatalf("创意模式破坏后掉落物数量 = %d，want 1（不应新增）", remaining)
	}
}
