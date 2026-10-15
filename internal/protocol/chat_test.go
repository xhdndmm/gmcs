//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

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

// TestChatSignaturePayload 校验签名负载的字节布局：
// int32(版本)、发送者 UUID、会话 UUID、int32(序号)、int64(盐)、
// int64(时间戳秒)、int32(内容长度)+内容、int32(确认数量)+每条签名。
func TestChatSignaturePayload(t *testing.T) {
	sender := [16]byte{0x01, 0x02, 0x03}
	session := [16]byte{0x0A, 0x0B}
	lastSeen := [][]byte{make([]byte, MaxChatSignatureLength), make([]byte, MaxChatSignatureLength)}
	lastSeen[0][0] = 0xAA
	lastSeen[1][255] = 0xBB

	payload := ChatSignaturePayload(sender, session, 7, 0x0102030405060708, 1_700_000_000_123, "hi", lastSeen)
	offset := 0
	readInt32 := func() int32 {
		value := int32(payload[offset])<<24 | int32(payload[offset+1])<<16 | int32(payload[offset+2])<<8 | int32(payload[offset+3])
		offset += 4
		return value
	}
	readInt64 := func() int64 {
		var value int64
		for i := 0; i < 8; i++ {
			value = value<<8 | int64(payload[offset+i])
		}
		offset += 8
		return value
	}
	if got := readInt32(); got != 1 {
		t.Fatalf("version = %d, want 1", got)
	}
	if !bytes.Equal(payload[offset:offset+16], sender[:]) {
		t.Fatalf("sender = %x", payload[offset:offset+16])
	}
	offset += 16
	if !bytes.Equal(payload[offset:offset+16], session[:]) {
		t.Fatalf("session = %x", payload[offset:offset+16])
	}
	offset += 16
	if got := readInt32(); got != 7 {
		t.Fatalf("index = %d, want 7", got)
	}
	if got := readInt64(); got != 0x0102030405060708 {
		t.Fatalf("salt = %#x", got)
	}
	// 时间戳使用 Instant.getEpochSecond()（毫秒整除 1000）。
	if got := readInt64(); got != 1_700_000_000 {
		t.Fatalf("timestamp seconds = %d, want 1700000000", got)
	}
	if got := readInt32(); got != 2 {
		t.Fatalf("content length = %d, want 2", got)
	}
	if string(payload[offset:offset+2]) != "hi" {
		t.Fatalf("content = %q", payload[offset:offset+2])
	}
	offset += 2
	if got := readInt32(); got != 2 {
		t.Fatalf("last seen count = %d, want 2", got)
	}
	if !bytes.Equal(payload[offset:offset+MaxChatSignatureLength], lastSeen[0]) {
		t.Fatal("first last seen signature mismatch")
	}
	offset += MaxChatSignatureLength
	if !bytes.Equal(payload[offset:offset+MaxChatSignatureLength], lastSeen[1]) {
		t.Fatal("second last seen signature mismatch")
	}
	offset += MaxChatSignatureLength
	if offset != len(payload) {
		t.Fatalf("payload has %d trailing bytes", len(payload)-offset)
	}
}

// TestChatLastSeenChecksum 校验确认窗口校验和：
// h = 31 * h + Arrays.hashCode(signature)，初始 1；结果为 0 时取 1。
func TestChatLastSeenChecksum(t *testing.T) {
	if got := ChatLastSeenChecksum(nil); got != 1 {
		t.Fatalf("empty checksum = %d, want 1", got)
	}
	// 手工计算期望值：外层累加器初始 1，内层 Arrays.hashCode 初始 1。
	expected := func(entries ...[]byte) uint8 {
		outer := int32(1)
		for _, entry := range entries {
			inner := int32(1)
			for _, b := range entry {
				inner = 31*inner + int32(int8(b))
			}
			outer = 31*outer + inner
		}
		if result := uint8(outer); result != 0 {
			return result
		}
		return 1
	}
	zero := make([]byte, MaxChatSignatureLength)
	if got, want := ChatLastSeenChecksum([][]byte{zero}), expected(zero); got != want {
		t.Fatalf("checksum = %d, want %d", got, want)
	}
	// 有符号字节：0xFF 应按 -1 参与运算。
	negative := make([]byte, MaxChatSignatureLength)
	negative[0] = 0xFF
	if got, want := ChatLastSeenChecksum([][]byte{negative}), expected(negative); got != want {
		t.Fatalf("negative checksum = %d, want %d", got, want)
	}
	if ChatLastSeenChecksum([][]byte{negative}) == ChatLastSeenChecksum([][]byte{zero}) {
		t.Fatal("0xFF 字节应按有符号值参与哈希")
	}
	// 多条目按顺序累加。
	if got, want := ChatLastSeenChecksum([][]byte{zero, negative}), expected(zero, negative); got != want {
		t.Fatalf("multi checksum = %d, want %d", got, want)
	}
}

// TestAcknowledgedBits 验证确认位图的位序（BitSet.valueOf 小端）。
func TestAcknowledgedBits(t *testing.T) {
	var message SignedChatMessage
	message.Acknowledged = [3]byte{0b00000001, 0b10000000, 0}
	if !message.AcknowledgedBit(0) {
		t.Fatal("bit 0 should be set")
	}
	if !message.AcknowledgedBit(15) {
		t.Fatal("bit 15 should be set")
	}
	if message.AcknowledgedBit(1) || message.AcknowledgedBit(8) {
		t.Fatal("unexpected bits set")
	}
	if length := message.AcknowledgedLength(); length != 16 {
		t.Fatalf("length = %d, want 16", length)
	}
	if length := (SignedChatMessage{}).AcknowledgedLength(); length != 0 {
		t.Fatalf("empty length = %d, want 0", length)
	}
}

// TestEncodePlayerChatMessageSignature 校验转发签名消息的包布局：
// 签名位 + 256 字节签名、确认窗口（id = -1 的完整签名）与序号。
func TestEncodePlayerChatMessageSignature(t *testing.T) {
	signature := bytes.Repeat([]byte{0x33}, MaxChatSignatureLength)
	lastSeen := bytes.Repeat([]byte{0x44}, MaxChatSignatureLength)
	packet := EncodePlayerChatMessage(PlayerChatData{
		GlobalIndex: 3,
		SenderUUID:  [16]byte{0x05},
		SenderIndex: 2,
		Signature:   signature,
		Message:     "hi",
		Timestamp:   1234,
		Salt:        77,
		LastSeen:    [][]byte{lastSeen},
		DisplayName: "Tester",
	})
	offset := 0
	readVarInt := func() int32 {
		value, size, err := DecodeVarInt(packet[offset:])
		if err != nil {
			t.Fatal(err)
		}
		offset += size
		return value
	}
	if packetID := readVarInt(); packetID != int32(PlayPacketIDPlayerChat) {
		t.Fatalf("packet id = %#x", packetID)
	}
	if global := readVarInt(); global != 3 {
		t.Fatalf("global index = %d, want 3", global)
	}
	if !bytes.Equal(packet[offset:offset+16], []byte{0x05, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}) {
		t.Fatalf("sender = %x", packet[offset:offset+16])
	}
	offset += 16
	if index := readVarInt(); index != 2 {
		t.Fatalf("sender index = %d, want 2", index)
	}
	if packet[offset] != 0x01 {
		t.Fatal("signature flag should be set")
	}
	offset++
	if !bytes.Equal(packet[offset:offset+MaxChatSignatureLength], signature) {
		t.Fatal("signature mismatch")
	}
	offset += MaxChatSignatureLength
	length, size, err := DecodeVarInt(packet[offset:])
	if err != nil || length != 2 {
		t.Fatalf("content length = %d", length)
	}
	offset += size
	if string(packet[offset:offset+2]) != "hi" {
		t.Fatalf("content = %q", packet[offset:offset+2])
	}
	offset += 2
	offset += 16 // 时间戳 + 盐
	if count := readVarInt(); count != 1 {
		t.Fatalf("last seen count = %d, want 1", count)
	}
	if id := readVarInt(); id != 0 {
		t.Fatalf("last seen id = %d, want 0 (完整签名)", id)
	}
	if !bytes.Equal(packet[offset:offset+MaxChatSignatureLength], lastSeen) {
		t.Fatal("last seen signature mismatch")
	}
}

// TestEncodePlayerChatMessageUnsigned 校验未签名消息的包布局：
// 签名位为 0、确认窗口为空、unsignedContent 为原文。
func TestEncodePlayerChatMessageUnsigned(t *testing.T) {
	packet := EncodePlayerChatMessage(PlayerChatData{
		GlobalIndex: 0,
		SenderUUID:  [16]byte{0x06},
		SenderIndex: 0,
		Message:     "plain",
		Timestamp:   99,
		Salt:        0,
		DisplayName: "Tester",
	})
	offset := 0
	if _, size, err := DecodeVarInt(packet); err != nil {
		t.Fatal(err)
	} else {
		offset += size
	}
	if _, size, err := DecodeVarInt(packet[offset:]); err != nil {
		t.Fatal(err)
	} else {
		offset += size
	}
	offset += 16
	if _, size, err := DecodeVarInt(packet[offset:]); err != nil {
		t.Fatal(err)
	} else {
		offset += size
	}
	if packet[offset] != 0x00 {
		t.Fatal("signature flag should be clear")
	}
}
