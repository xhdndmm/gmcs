//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/registry"
)

// TestPlayerTrimPersisted 验证纹饰组件随 players.json 持久化：
// 退出后磁盘记录保留图案/材质，重连后物品栏恢复相同的纹饰。
func TestPlayerTrimPersisted(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.ViewDistance = 2
	instance, conn := joinServer(t, cfg, "TrimKeeper")
	addr := conn.RemoteAddr().String()

	chestID, err := registry.ItemID("minecraft:netherite_chestplate")
	if err != nil {
		t.Fatal(err)
	}
	sess := findSession(t, instance, "TrimKeeper")
	sess.inventory.Set(item.SlotHotbarStart, item.Stack{
		ItemID: chestID, Count: 1,
		TrimPattern: "minecraft:bolt", TrimMaterial: "minecraft:gold",
	})

	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	waitPlayerOffline(t, instance, sess.uuid)

	record := readSavedPlayer(t, cfg.WorldDir, sess.uuid)
	if len(record.Inventory) != 1 {
		t.Fatalf("物品栏记录 = %+v, want 1 项", record.Inventory)
	}
	saved := record.Inventory[0]
	if saved.Item != "minecraft:netherite_chestplate" ||
		saved.TrimPattern != "minecraft:bolt" || saved.TrimMaterial != "minecraft:gold" {
		t.Fatalf("纹饰未持久化：%+v", saved)
	}

	conn2 := joinAt(t, cfg, addr, "TrimKeeper")
	sess2 := findSession(t, instance, "TrimKeeper")
	restored := sess2.inventory.Get(item.SlotHotbarStart)
	if restored.ItemID != chestID || restored.TrimPattern != "minecraft:bolt" ||
		restored.TrimMaterial != "minecraft:gold" {
		t.Fatalf("重连后纹饰未恢复：%+v", restored)
	}
	if err := conn2.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestPlayerTrimInvalidDropped 验证持久化中的未知纹饰名在恢复时被丢弃
// （避免旧版本/损坏记录导致写出非法组件）。
func TestPlayerTrimInvalidDropped(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.ViewDistance = 2 // 与 joinServer 内部设置一致（重连的 joinAt 依赖同一值）
	instance, conn := joinServer(t, cfg, "BadTrim")
	addr := conn.RemoteAddr().String()
	sess := findSession(t, instance, "BadTrim")
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	waitPlayerOffline(t, instance, sess.uuid)

	// 直接向内存快照写入一条带非法纹饰的记录（模拟旧版本/损坏数据），
	// 重连恢复时应丢弃非法纹饰但保留物品本身。
	instance.playerDataMu.Lock()
	record := instance.playerData[sess.uuid]
	record.Inventory = []playerItem{{
		Slot: item.SlotHotbarStart, Item: "minecraft:netherite_chestplate", Count: 1,
		TrimPattern: "minecraft:not_a_pattern", TrimMaterial: "minecraft:gold",
	}}
	instance.playerData[sess.uuid] = record
	instance.playerDataMu.Unlock()

	conn2 := joinAt(t, cfg, addr, "BadTrim")
	sess2 := findSession(t, instance, "BadTrim")
	restored := sess2.inventory.Get(item.SlotHotbarStart)
	if restored.TrimPattern != "" || restored.TrimMaterial != "" {
		t.Fatalf("非法纹饰应被丢弃：%+v", restored)
	}
	if restored.ItemID != mustItemName(t, "minecraft:netherite_chestplate") {
		t.Fatalf("物品本身应保留：%+v", restored)
	}
	if err := conn2.Close(); err != nil {
		t.Fatal(err)
	}
}

// mustItemName 查询物品注册表 ID（测试辅助）。
func mustItemName(t *testing.T, name string) int32 {
	t.Helper()
	id, err := registry.ItemID(name)
	if err != nil {
		t.Fatalf("未知物品 %s：%v", name, err)
	}
	return id
}
