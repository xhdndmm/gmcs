//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
)

// TestDeathScreenPacketOnDeath 验证玩家死亡时会收到 Player Combat Kill
// （0x42）：客户端只有收到该包才显示死亡界面，仅广播 System Chat
// 死亡消息是不够的（此前 gmcs 的死亡界面因此不显示）。
func TestDeathScreenPacketOnDeath(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Doomed")
	instance.worldAge.Store(worldNightStart) // 排除日光/夜间刷怪的干扰
	player := findSession(t, instance, "Doomed")

	// 一击致死（首次受伤不受无敌帧限制）。
	if !instance.damagePlayer(player, 100, "test source", 0, 0, nil) {
		t.Fatal("伤害未生效")
	}
	if !player.dead.Load() {
		t.Fatal("玩家未进入死亡状态")
	}

	payload := expectPlayPacket(t, conn, protocol.PlayPacketIDPlayerCombatKill)
	_, offset, err := protocol.DecodeVarInt(payload) // 跳过包 ID
	if err != nil {
		t.Fatal(err)
	}
	entityID, n, err := protocol.DecodeVarInt(payload[offset:])
	if err != nil {
		t.Fatalf("解码 playerID 失败：%v", err)
	}
	offset += n
	if entityID != player.entityID {
		t.Fatalf("死亡界面的玩家实体 ID = %d, want %d", entityID, player.entityID)
	}
	if offset >= len(payload) || payload[offset] != 0x08 {
		t.Fatalf("死亡消息组件不是 NBT String Tag：%#x", payload[offset:])
	}
	offset++
	message, _ := readNBTTestString(t, payload, offset)
	if want := "Doomed was slain by test source"; message != want {
		t.Fatalf("死亡消息 = %q, want %q", message, want)
	}
	// 死亡消息同时进聊天栏（原版行为）。
	if chat := expectPlayPacket(t, conn, protocol.PlayPacketIDSystemChat); len(chat) == 0 {
		t.Fatal("未收到死亡消息的 System Chat")
	}
}

// TestDeathScreenUsesCustomTemplate 验证带死亡模板的伤害（如岩浆/仙人掌）
// 会把模板文本同时写进死亡界面。
func TestDeathScreenUsesCustomTemplate(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Burny")
	instance.worldAge.Store(worldNightStart)
	player := findSession(t, instance, "Burny")

	if !instance.damagePlayerWithMessage(player, 100, "lava", 0, 0, nil, "%s tried to swim in lava") {
		t.Fatal("伤害未生效")
	}
	payload := expectPlayPacket(t, conn, protocol.PlayPacketIDPlayerCombatKill)
	_, offset, err := protocol.DecodeVarInt(payload)
	if err != nil {
		t.Fatal(err)
	}
	_, n, err := protocol.DecodeVarInt(payload[offset:])
	if err != nil {
		t.Fatal(err)
	}
	offset += n
	if offset >= len(payload) || payload[offset] != 0x08 {
		t.Fatalf("死亡消息组件不是 NBT String Tag：%#x", payload[offset:])
	}
	offset++
	message, _ := readNBTTestString(t, payload, offset)
	if want := "Burny tried to swim in lava"; message != want {
		t.Fatalf("死亡消息 = %q, want %q", message, want)
	}
}
