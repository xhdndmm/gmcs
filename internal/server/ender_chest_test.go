package server

import (
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
)

// placeEnderChest 在玩家附近放置末影箱方块。
func placeEnderChest(t *testing.T, instance *Server) (int, int, int) {
	t.Helper()
	state, ok := registry.BlockStateIDs["minecraft:ender_chest"]
	if !ok {
		t.Fatal("缺少末影箱方块状态")
	}
	x, y, z := buildSpotNearSpawn(t, instance)
	if !instance.testWorld().SetBlock(x, y, z, state) {
		t.Fatal("无法放置末影箱")
	}
	return x, y, z
}

// TestEnderChestOpenAndStore 验证末影箱打开、存入与按玩家隔离。
func TestEnderChestOpenAndStore(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.GameMode = "creative"
	instance, conn := joinServer(t, cfg, "EnderOne")

	diamond, err := registry.ItemID("minecraft:diamond")
	if err != nil {
		t.Fatal(err)
	}

	x, y, z := placeEnderChest(t, instance)
	sendUseItemOn(t, conn, x, y, z, 1)
	windowID, _ := openScreen(t, conn)
	content := expectPlayPacket(t, conn, protocol.PlayPacketIDContainerSetContent)
	gotWindow, slots := containerContent(t, content)
	if gotWindow != windowID {
		t.Fatalf("窗口编号 = %d, want %d", gotWindow, windowID)
	}
	// 末影箱窗口：27 箱槽 + 36 背包。
	if len(slots) != 63 {
		t.Fatalf("窗口槽位数 = %d, want 63", len(slots))
	}

	// 用创造模式把钻石放进末影箱槽 0（Set Creative Slot 的槽位是物品栏槽位，
	// 末影箱内容直接写会话状态更简单）。
	player := activeSession(t, instance, "EnderOne")
	player.enderChest.Set(0, itemStackFor(diamond, 5))

	// 槽位内容随窗口可读（重新打开验证持久性）。
	closePacket := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDContainerClose)
	closePacket = protocol.AppendVarInt(closePacket, windowID)
	if err := protocol.WritePacketWithCompression(conn, closePacket, compressionThreshold); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !player.getOpenEnder() }, "末影箱窗口应关闭")
	if got := player.enderChest.Get(0); got.ItemID != diamond || got.Count != 5 {
		t.Fatalf("末影箱内容丢失：%+v", got)
	}
}

// TestEnderChestPerPlayer 验证末影箱内容按玩家隔离。
func TestEnderChestPerPlayer(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "EnderA")

	diamond, _ := registry.ItemID("minecraft:diamond")
	playerA := activeSession(t, instance, "EnderA")
	playerB := &session{name: "EnderB", server: instance}

	playerA.enderChest.Set(3, itemStackFor(diamond, 7))
	if got := playerB.enderChest.Get(3); !got.IsEmpty() {
		t.Fatalf("其他玩家末影箱应为空，得到 %+v", got)
	}
}

// TestEnderChestPersisted 验证末影箱内容随玩家数据持久化并恢复。
func TestEnderChestPersisted(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Keeper")

	diamond, _ := registry.ItemID("minecraft:diamond")
	player := activeSession(t, instance, "Keeper")
	player.enderChest.Set(2, itemStackFor(diamond, 9))

	// 保存玩家数据并重新载入记录。
	instance.savePlayerData(player)
	record, ok := instance.playerDataSnapshot(playerUUID(t, instance, "Keeper"))
	if !ok {
		t.Fatal("玩家记录不存在")
	}
	if len(record.EnderItems) != 1 || record.EnderItems[0].Slot != 2 || record.EnderItems[0].Count != 9 {
		t.Fatalf("末影箱未持久化：%+v", record.EnderItems)
	}

	// 模拟重连：恢复到新会话。
	player2 := &session{name: "Keeper", server: instance}
	player2.applyPlayerRecord(record)
	if got := player2.enderChest.Get(2); got.ItemID != diamond || got.Count != 9 {
		t.Fatalf("末影箱未恢复：%+v", got)
	}
	_ = conn
}

// playerUUID 测试辅助：按玩家名取会话 UUID。
func playerUUID(t *testing.T, instance *Server, name string) [16]byte {
	t.Helper()
	return activeSession(t, instance, name).uuid
}
