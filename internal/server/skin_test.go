package server

import (
	"testing"

	"gmcs/internal/protocol"
)

// TestPlayerSkinPacket 测试皮肤层元数据编码：玩家实体生成时
// 附带的 Set Entity Data 包应包含索引 16 与客户端上报的掩码。
func TestPlayerSkinPacket(t *testing.T) {
	player := &session{entityID: 42}
	player.setClientInfo(protocol.ClientInformation{SkinParts: 0x3F})

	packet := (&Server{}).playerSkinPacket(player)
	// 解析：包 ID、实体 ID、索引、类型、值、终止符。
	packetID, n, err := protocol.DecodeVarInt(packet)
	if err != nil || packetID != protocol.PlayPacketIDEntityMetadata {
		t.Fatalf("unexpected packet ID %#x (err=%v)", packetID, err)
	}
	entityID, n2, err := protocol.DecodeVarInt(packet[n:])
	if err != nil || entityID != 42 {
		t.Fatalf("entity ID = %d (err=%v)", entityID, err)
	}
	pos := n + n2
	if packet[pos] != protocol.EntityMetadataSkinPartsIndex {
		t.Fatalf("metadata index = %d, want %d", packet[pos], protocol.EntityMetadataSkinPartsIndex)
	}
	pos += 2 // 索引 + 类型 varint（单字节 0）
	if packet[pos] != 0x3F {
		t.Fatalf("skin parts value = %#x, want 0x3f", packet[pos])
	}
	if packet[pos+1] != 0xFF {
		t.Fatalf("missing metadata terminator")
	}
}

// TestSetClientInfoRoundTrip 测试 clientInfo 的线程安全访问。
func TestSetClientInfoRoundTrip(t *testing.T) {
	player := &session{}
	old := player.setClientInfo(protocol.ClientInformation{SkinParts: 0x11})
	if old.SkinParts != 0 {
		t.Fatalf("old skin parts = %#x, want 0", old.SkinParts)
	}
	if got := player.skinParts(); got != 0x11 {
		t.Fatalf("skinParts() = %#x, want 0x11", got)
	}
	old = player.setClientInfo(protocol.ClientInformation{SkinParts: 0x22})
	if old.SkinParts != 0x11 {
		t.Fatalf("old skin parts = %#x, want 0x11", old.SkinParts)
	}
}
