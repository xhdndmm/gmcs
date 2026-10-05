package protocol

import (
	"bytes"
	"testing"
)

// TestParseChatSessionUpdate 验证聊天会话公钥包的解析（字段顺序：
// 会话 UUID、过期时间、公钥字节数组、公钥签名）。
func TestParseChatSessionUpdate(t *testing.T) {
	sessionUUID := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	publicKey := bytes.Repeat([]byte{0xAB}, 294)
	keySignature := bytes.Repeat([]byte{0xCD}, 256)

	packet := AppendVarInt(nil, PlayServerboundPacketIDChatSessionUpdate)
	packet = append(packet, sessionUUID[:]...)
	packet = AppendInt64(packet, 1_700_000_000_000)
	packet = AppendVarInt(packet, int32(len(publicKey)))
	packet = append(packet, publicKey...)
	packet = AppendVarInt(packet, int32(len(keySignature)))
	packet = append(packet, keySignature...)

	session, err := ParseChatSessionUpdate(packet)
	if err != nil {
		t.Fatal(err)
	}
	if session.SessionUUID != sessionUUID {
		t.Fatalf("session UUID = %x", session.SessionUUID)
	}
	if session.ExpireTime != 1_700_000_000_000 {
		t.Fatalf("expire time = %d", session.ExpireTime)
	}
	if !bytes.Equal(session.PublicKey, publicKey) || !bytes.Equal(session.KeySignature, keySignature) {
		t.Fatal("公钥或签名内容不匹配")
	}

	// 截断的包必须被拒绝。
	if _, err := ParseChatSessionUpdate(packet[:len(packet)-1]); err == nil {
		t.Fatal("截断的聊天会话包应被拒绝")
	}
	// 超大公钥必须被拒绝（防内存滥用）。
	oversized := AppendVarInt(nil, PlayServerboundPacketIDChatSessionUpdate)
	oversized = append(oversized, sessionUUID[:]...)
	oversized = AppendInt64(oversized, 0)
	oversized = AppendVarInt(oversized, maxChatKeySize+1)
	if _, err := ParseChatSessionUpdate(oversized); err == nil {
		t.Fatal("超大公钥应被拒绝")
	}
}

// TestEncodePlayerInfoChatSession 校验 Initialize Chat 动作的字段顺序
// （与 1.21.11 协议一致）：动作位 0x02、UUID、会话存在位、会话 UUID、
// 过期时间、公钥（VarInt 长度）、签名（VarInt 长度）。
func TestEncodePlayerInfoChatSession(t *testing.T) {
	uuid := [16]byte{9, 9, 9, 9}
	session := ChatSession{
		SessionUUID:  [16]byte{8, 8, 8, 8},
		ExpireTime:   42,
		PublicKey:    []byte{1, 2, 3},
		KeySignature: []byte{4, 5},
	}
	packet := EncodePlayerInfoChatSession(uuid, session)

	id, offset, err := DecodeVarInt(packet)
	if err != nil || id != PlayPacketIDPlayerInfoUpdate {
		t.Fatalf("packet id = %#x (err=%v)", id, err)
	}
	if packet[offset] != 0x02 {
		t.Fatalf("action bits = %#x, want 0x02", packet[offset])
	}
	offset++
	count, size, err := DecodeVarInt(packet[offset:])
	if err != nil || count != 1 {
		t.Fatalf("entry count = %d (err=%v)", count, err)
	}
	offset += size
	if !bytes.Equal(packet[offset:offset+16], uuid[:]) {
		t.Fatal("UUID 不匹配")
	}
	offset += 16
	if packet[offset] != 0x01 {
		t.Fatal("缺少会话存在位")
	}
	offset++
	if !bytes.Equal(packet[offset:offset+16], session.SessionUUID[:]) {
		t.Fatal("会话 UUID 不匹配")
	}
	offset += 16
	expire, offset, err := DecodeInt64(packet, offset)
	if err != nil || expire != session.ExpireTime {
		t.Fatalf("过期时间 = %d (err=%v)", expire, err)
	}
	keyLength, size, err := DecodeVarInt(packet[offset:])
	if err != nil || keyLength != 3 {
		t.Fatalf("公钥长度 = %d (err=%v)", keyLength, err)
	}
	offset += size
	if !bytes.Equal(packet[offset:offset+3], session.PublicKey) {
		t.Fatal("公钥内容不匹配")
	}
	offset += 3
	sigLength, size, err := DecodeVarInt(packet[offset:])
	if err != nil || sigLength != 2 {
		t.Fatalf("签名长度 = %d (err=%v)", sigLength, err)
	}
	offset += size
	if !bytes.Equal(packet[offset:offset+2], session.KeySignature) {
		t.Fatal("签名内容不匹配")
	}
}

// TestParseSignedChatMessage 验证带签名聊天消息的解析
// （消息、时间戳、盐、256 字节签名、确认窗口与校验和）。
func TestParseSignedChatMessage(t *testing.T) {
	signature := bytes.Repeat([]byte{0x5A}, MaxChatSignatureLength)
	packet := AppendVarInt(nil, PlayServerboundPacketIDChatMessage)
	packet = appendString(packet, "hello world")
	packet = AppendInt64(packet, 123456789)
	packet = AppendInt64(packet, -987654321)
	packet = append(packet, 0x01)
	packet = append(packet, signature...)
	packet = AppendVarInt(packet, 7)
	packet = append(packet, 0x11, 0x22, 0x33, 0x44)

	message, err := ParseSignedChatMessage(packet)
	if err != nil {
		t.Fatal(err)
	}
	if message.Message != "hello world" {
		t.Fatalf("message = %q", message.Message)
	}
	if message.Timestamp != 123456789 || message.Salt != -987654321 {
		t.Fatalf("timestamp/salt = %d/%d", message.Timestamp, message.Salt)
	}
	if !bytes.Equal(message.Signature, signature) {
		t.Fatal("签名不匹配")
	}
	if message.Offset != 7 {
		t.Fatalf("offset = %d", message.Offset)
	}
	if message.Acknowledged != [3]byte{0x11, 0x22, 0x33} || message.Checksum != 0x44 {
		t.Fatalf("acknowledged/checksum = %x/%#x", message.Acknowledged, message.Checksum)
	}

	// 未签名消息：签名位为 0。
	unsigned := AppendVarInt(nil, PlayServerboundPacketIDChatMessage)
	unsigned = appendString(unsigned, "hi")
	unsigned = AppendInt64(unsigned, 1)
	unsigned = AppendInt64(unsigned, 2)
	unsigned = append(unsigned, 0x00)
	unsigned = AppendVarInt(unsigned, 0)
	unsigned = append(unsigned, 0, 0, 0, 0)
	parsed, err := ParseSignedChatMessage(unsigned)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Signature != nil {
		t.Fatal("未签名消息不应带签名")
	}
	if parsed.Message != "hi" {
		t.Fatalf("message = %q", parsed.Message)
	}
}
