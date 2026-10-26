//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package protocol

import (
	"bytes"
	"testing"
)

// TestEncodePlayerCombatKillLayout 逐字段校验 Player Combat Kill 包的布局。
//
// 字段表来自 1.21.11 客户端 ClientboundPlayerCombatKillPacket：
//
//	playerID VarInt
//	message  Component（网络 NBT，本实现为只含文本的 String Tag）
//
// 该包是客户端显示死亡界面的唯一触发源；字段错位会让客户端断开连接。
func TestEncodePlayerCombatKillLayout(t *testing.T) {
	packet := EncodePlayerCombatKill(300, "Steve was slain by Zombie")

	offset := 0
	id, n, err := DecodeVarInt(packet[offset:])
	if err != nil {
		t.Fatalf("解码包 ID 失败：%v", err)
	}
	offset += n
	if id != PlayPacketIDPlayerCombatKill {
		t.Fatalf("包 ID = %#x, want %#x", id, PlayPacketIDPlayerCombatKill)
	}
	playerID, n, err := DecodeVarInt(packet[offset:])
	if err != nil {
		t.Fatalf("解码 playerID 失败：%v", err)
	}
	offset += n
	if playerID != 300 {
		t.Fatalf("playerID = %d, want 300", playerID)
	}
	if offset >= len(packet) {
		t.Fatal("缺少死亡消息组件")
	}
	if tag := packet[offset]; tag != nbtTagString {
		t.Fatalf("组件根标签 = %#x, want TAG_String %#x", tag, nbtTagString)
	}
	offset++
	if offset+2 > len(packet) {
		t.Fatal("缺少字符串长度前缀")
	}
	length := int(packet[offset])<<8 | int(packet[offset+1])
	offset += 2
	if offset+length != len(packet) {
		t.Fatalf("字符串长度 %d 与剩余字节 %d 不符", length, len(packet)-offset)
	}
	if got := string(packet[offset:]); got != "Steve was slain by Zombie" {
		t.Fatalf("死亡消息 = %q", got)
	}
}

// TestEncodePlayerCombatKillEmptyMessage 验证空消息仍然编码为合法的
// String Tag（不会被截断成缺少标签的负载）。
func TestEncodePlayerCombatKillEmptyMessage(t *testing.T) {
	packet := EncodePlayerCombatKill(1, "")
	want := []byte{byte(PlayPacketIDPlayerCombatKill), 0x01, nbtTagString, 0x00, 0x00}
	if !bytes.Equal(packet, want) {
		t.Fatalf("空消息编码 = % x, want % x", packet, want)
	}
}
