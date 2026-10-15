//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"net"
	"testing"
	"time"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
)

// testChatSession 生成一份测试用聊天会话（RSA-2048 公钥 + 私钥 + 会话 UUID）。
func testChatSession(t *testing.T) (*rsa.PrivateKey, protocol.ChatSession) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	return key, protocol.ChatSession{
		SessionUUID:  [16]byte{0x51, 0x52, 0x53, 0x54},
		ExpireTime:   time.Now().Add(time.Hour).UnixMilli(),
		PublicKey:    der,
		KeySignature: bytes.Repeat([]byte{0x11}, 256),
	}
}

// signChatPayload 用私钥对一条聊天消息签名（与客户端行为一致）。
func signChatPayload(t *testing.T, key *rsa.PrivateKey, sender, session [16]byte, index int32, salt, timestamp int64, text string, lastSeen [][]byte) []byte {
	t.Helper()
	payload := protocol.ChatSignaturePayload(sender, session, index, salt, timestamp, text, lastSeen)
	digest := sha256.Sum256(payload)
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign chat payload: %v", err)
	}
	return signature
}

// sendChatMessagePacket 发送 Serverbound Chat Message 包。
func sendChatMessagePacket(t *testing.T, conn net.Conn, text string, timestamp, salt int64, signature []byte, offset int32, acknowledged [3]byte, checksum byte) {
	t.Helper()
	packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDChatMessage)
	packet = protocol.AppendVarInt(packet, int32(len(text)))
	packet = append(packet, text...)
	packet = protocol.AppendInt64(packet, timestamp)
	packet = protocol.AppendInt64(packet, salt)
	if len(signature) == protocol.MaxChatSignatureLength {
		packet = append(packet, 0x01)
		packet = append(packet, signature...)
	} else {
		packet = append(packet, 0x00)
	}
	packet = protocol.AppendVarInt(packet, offset)
	packet = append(packet, acknowledged[:]...)
	packet = append(packet, checksum)
	if err := protocol.WritePacketWithCompression(conn, packet, compressionThreshold); err != nil {
		t.Fatal(err)
	}
}

// expectKickComponent 读取断开包并返回其中的翻译键（可翻译组件）。
func expectKickComponent(t *testing.T, conn net.Conn) string {
	t.Helper()
	payload := expectPlayPacket(t, conn, protocol.PlayPacketIDDisconnect)
	_, offset, err := protocol.DecodeVarInt(payload) // 跳过包 ID
	if err != nil {
		t.Fatal(err)
	}
	if payload[offset] != 0x0A { // TAG_Compound
		t.Fatalf("disconnect reason tag = %#x, want compound", payload[offset])
	}
	offset++
	if payload[offset] != 0x08 { // TAG_String
		t.Fatalf("disconnect reason key tag = %#x, want string", payload[offset])
	}
	offset++
	name, offset := readNBTTestString(t, payload, offset)
	if name != "translate" {
		t.Fatalf("disconnect component key = %q, want translate", name)
	}
	value, _ := readNBTTestString(t, payload, offset)
	return value
}

// readNBTTestString 读取网络 NBT 字符串负载（u16 大端长度前缀）。
func readNBTTestString(t *testing.T, data []byte, offset int) (string, int) {
	t.Helper()
	if len(data)-offset < 2 {
		t.Fatal("NBT string truncated")
	}
	length := int(data[offset])<<8 | int(data[offset+1])
	offset += 2
	if len(data)-offset < length {
		t.Fatal("NBT string payload truncated")
	}
	return string(data[offset : offset+length]), offset + length
}

// playerChatPacket 是测试解析出的 Player Chat 包字段。
type playerChatPacket struct {
	globalIndex int32
	sender      [16]byte
	index       int32
	signature   []byte
	content     string
	timestamp   int64
	salt        int64
	lastSeen    [][]byte
}

// parsePlayerChatPacket 解析 Player Chat 包（payload 含包 ID）。
func parsePlayerChatPacket(t *testing.T, payload []byte) playerChatPacket {
	t.Helper()
	var chat playerChatPacket
	packetID, offset, err := protocol.DecodeVarInt(payload)
	if err != nil || packetID != protocol.PlayPacketIDPlayerChat {
		t.Fatalf("packet id = %#x, want player chat", packetID)
	}
	if value, size, err := protocol.DecodeVarInt(payload[offset:]); err != nil {
		t.Fatal(err)
	} else {
		chat.globalIndex, offset = value, offset+size
	}
	if len(payload)-offset < 16 {
		t.Fatal("packet truncated at sender UUID")
	}
	copy(chat.sender[:], payload[offset:offset+16])
	offset += 16
	if value, size, err := protocol.DecodeVarInt(payload[offset:]); err != nil {
		t.Fatal(err)
	} else {
		chat.index, offset = value, offset+size
	}
	if len(payload)-offset < 1 {
		t.Fatal("packet truncated at signature flag")
	}
	if payload[offset] == 0x01 {
		offset++
		if len(payload)-offset < protocol.MaxChatSignatureLength {
			t.Fatal("packet truncated at signature")
		}
		chat.signature = append([]byte(nil), payload[offset:offset+protocol.MaxChatSignatureLength]...)
		offset += protocol.MaxChatSignatureLength
	} else {
		offset++
	}
	chat.content, offset = readTestString(t, payload, offset)
	if chat.timestamp, offset, err = protocol.DecodeInt64(payload, offset); err != nil {
		t.Fatal(err)
	}
	if chat.salt, offset, err = protocol.DecodeInt64(payload, offset); err != nil {
		t.Fatal(err)
	}
	count, size, err := protocol.DecodeVarInt(payload[offset:])
	if err != nil {
		t.Fatal(err)
	}
	offset += size
	for i := int32(0); i < count; i++ {
		bad, size, err := protocol.DecodeVarInt(payload[offset:])
		if err != nil {
			t.Fatal(err)
		}
		if bad != 0 {
			t.Fatalf("last seen entry %d id = %d, want full signature", i, bad)
		}
		offset += size
		if len(payload)-offset < protocol.MaxChatSignatureLength {
			t.Fatal("packet truncated at last seen signature")
		}
		chat.lastSeen = append(chat.lastSeen, append([]byte(nil), payload[offset:offset+protocol.MaxChatSignatureLength]...))
		offset += protocol.MaxChatSignatureLength
	}
	return chat
}

// setAckBit 置位确认位图（小端位序，与原版 BitSet 一致）。
func setAckBit(acked *[3]byte, i int) {
	acked[i/8] |= 1 << uint(i%8)
}

// newTestSession 构造一个只用于聊天校验的会话（不建立网络连接）。
func newTestSession(uuid [16]byte, enforce bool) *session {
	cfg := config.Default()
	cfg.OnlineMode = enforce
	return &session{
		server: &Server{config: cfg},
		name:   "Tester",
		uuid:   uuid,
	}
}

// TestChatLastSeenValidator 验证消息确认窗口的移植逻辑：
// 初始窗口含 window 个占位；确认、偏移、校验和与错误分支。
func TestChatLastSeenValidator(t *testing.T) {
	validator := newChatLastSeenValidator(protocol.ChatLastSeenWindow)
	if count := validator.trackedCount(); count != protocol.ChatLastSeenWindow {
		t.Fatalf("initial tracked count = %d, want %d", count, protocol.ChatLastSeenWindow)
	}
	// 偏移必须是 0（列表长度等于窗口大小）。
	if err := validator.applyOffset(0); err != nil {
		t.Fatalf("applyOffset(0): %v", err)
	}
	if err := validator.applyOffset(1); err == nil {
		t.Fatal("applyOffset(1) should fail on empty window")
	}

	sig1 := bytes.Repeat([]byte{0x01}, protocol.MaxChatSignatureLength)
	sig2 := bytes.Repeat([]byte{0x02}, protocol.MaxChatSignatureLength)
	if count := validator.addPending(sig1); count != protocol.ChatLastSeenWindow+1 {
		t.Fatalf("count after addPending = %d, want %d", count, protocol.ChatLastSeenWindow+1)
	}
	// 重复添加同一签名（原版去重）不增加计数。
	if count := validator.addPending(sig1); count != protocol.ChatLastSeenWindow+1 {
		t.Fatalf("duplicate addPending count = %d, want %d", count, protocol.ChatLastSeenWindow+1)
	}
	validator.addPending(sig2)

	// 客户端确认最后两条消息（位于索引 20、21），偏移为 2 使窗口对齐。
	var acked [3]byte
	setAckBit(&acked, protocol.ChatLastSeenWindow-2)
	setAckBit(&acked, protocol.ChatLastSeenWindow-1)
	entries := [][]byte{sig1, sig2}
	update := protocol.SignedChatMessage{Offset: 2, Acknowledged: acked, Checksum: protocol.ChatLastSeenChecksum(entries)}
	got, err := validator.applyUpdate(update)
	if err != nil {
		t.Fatalf("applyUpdate: %v", err)
	}
	if len(got) != 2 || !bytes.Equal(got[0], sig1) || !bytes.Equal(got[1], sig2) {
		t.Fatalf("applyUpdate entries = %d, want the two pending signatures", len(got))
	}
	// 重新确认已确认的条目：原版认为状态异常。
	if _, err := validator.applyUpdate(update); err == nil {
		t.Fatal("re-acknowledging should fail")
	}

	// 校验和错误。
	fresh := newChatLastSeenValidator(protocol.ChatLastSeenWindow)
	fresh.addPending(sig1)
	var bit [3]byte
	setAckBit(&bit, protocol.ChatLastSeenWindow-1)
	badChecksum := protocol.SignedChatMessage{Offset: 1, Acknowledged: bit, Checksum: 0x7F}
	if _, err := fresh.applyUpdate(badChecksum); err == nil {
		t.Fatal("checksum mismatch should fail")
	}
	// 校验和为 0 时跳过校验（原版 IGNORE_CHECKSUM）。
	fresh2 := newChatLastSeenValidator(protocol.ChatLastSeenWindow)
	fresh2.addPending(sig1)
	if _, err := fresh2.applyUpdate(protocol.SignedChatMessage{Offset: 1, Acknowledged: bit, Checksum: 0}); err != nil {
		t.Fatalf("checksum 0 should be ignored: %v", err)
	}
	// 确认未发送过的消息（窗口内没有对应条目）。
	empty := newChatLastSeenValidator(protocol.ChatLastSeenWindow)
	if _, err := empty.applyUpdate(protocol.SignedChatMessage{Acknowledged: bit, Checksum: 0}); err == nil {
		t.Fatal("acknowledging an unknown message should fail")
	}
}

// TestChatMessageChainVerification 验证消息链校验：
// 序号递增、时间戳递增、签名绑定会话密钥与序号。
func TestChatMessageChainVerification(t *testing.T) {
	key, chatSession := testChatSession(t)
	sender := [16]byte{0x0A, 0x0B}
	s := newTestSession(sender, true)
	if result := s.acceptChatSession(chatSession); result != chatSessionAccepted {
		t.Fatalf("acceptChatSession = %v, want accepted", result)
	}

	timestamp := time.Now().UnixMilli()
	salt := int64(0x1234)
	signature := signChatPayload(t, key, sender, chatSession.SessionUUID, 0, salt, timestamp, "hello", nil)
	message := protocol.SignedChatMessage{Message: "hello", Timestamp: timestamp, Salt: salt, Signature: signature}
	verified, reason := s.resolveChatMessage(message, nil)
	if reason != "" {
		t.Fatalf("valid message rejected: %s", reason)
	}
	if verified.index != 0 || verified.content != "hello" || !bytes.Equal(verified.signature, signature) {
		t.Fatalf("verified message = %+v", verified)
	}

	// 序号 1 的消息（链推进后）应通过。
	timestamp2 := timestamp + 1
	signature2 := signChatPayload(t, key, sender, chatSession.SessionUUID, 1, salt, timestamp2, "again", nil)
	message2 := protocol.SignedChatMessage{Message: "again", Timestamp: timestamp2, Salt: salt, Signature: signature2}
	if _, reason := s.resolveChatMessage(message2, nil); reason != "" {
		t.Fatalf("second message rejected: %s", reason)
	}

	// 缺少签名的消息在会话建立后必须被拒绝。
	unsigned := protocol.SignedChatMessage{Message: "raw", Timestamp: timestamp2 + 1}
	if _, reason := s.resolveChatMessage(unsigned, nil); reason != "chat.disabled.missingProfileKey" {
		t.Fatalf("unsigned message reason = %q", reason)
	}

	// 序号重复（重放第 0 条的签名）导致签名校验失败并使链损坏。
	replayTimestamp := timestamp2 + 10
	if _, reason := s.resolveChatMessage(protocol.SignedChatMessage{
		Message:   "hello",
		Timestamp: replayTimestamp,
		Salt:      salt,
		Signature: signature,
	}, nil); reason != "chat.disabled.invalid_signature" {
		t.Fatalf("replayed message reason = %q", reason)
	}
	timestamp3 := replayTimestamp + 1
	signature3 := signChatPayload(t, key, sender, chatSession.SessionUUID, 3, salt, timestamp3, "next", nil)
	broken := protocol.SignedChatMessage{Message: "next", Timestamp: timestamp3, Salt: salt, Signature: signature3}
	if _, reason := s.resolveChatMessage(broken, nil); reason != "chat.disabled.chain_broken" {
		t.Fatalf("broken chain reason = %q", reason)
	}
}

// TestChatMessageChainErrors 验证链校验的各类失败原因。
func TestChatMessageChainErrors(t *testing.T) {
	key, chatSession := testChatSession(t)
	sender := [16]byte{0x0C, 0x0D}
	timestamp := time.Now().UnixMilli()

	t.Run("out of order", func(t *testing.T) {
		s := newTestSession(sender, true)
		s.acceptChatSession(chatSession)
		first := signChatPayload(t, key, sender, chatSession.SessionUUID, 0, 1, timestamp, "a", nil)
		if _, reason := s.resolveChatMessage(protocol.SignedChatMessage{Message: "a", Timestamp: timestamp, Salt: 1, Signature: first}, nil); reason != "" {
			t.Fatalf("first message rejected: %s", reason)
		}
		// 时间戳回退 → 乱序。
		second := signChatPayload(t, key, sender, chatSession.SessionUUID, 1, 1, timestamp-1000, "b", nil)
		got, reason := s.resolveChatMessage(protocol.SignedChatMessage{Message: "b", Timestamp: timestamp - 1000, Salt: 1, Signature: second}, nil)
		if reason != "chat.disabled.out_of_order_chat" {
			t.Fatalf("out of order reason = %q (%+v)", reason, got)
		}
	})

	t.Run("wrong key", func(t *testing.T) {
		otherKey, _ := testChatSession(t)
		s := newTestSession(sender, true)
		s.acceptChatSession(chatSession)
		signature := signChatPayload(t, otherKey, sender, chatSession.SessionUUID, 0, 1, timestamp, "a", nil)
		if _, reason := s.resolveChatMessage(protocol.SignedChatMessage{Message: "a", Timestamp: timestamp, Salt: 1, Signature: signature}, nil); reason != "chat.disabled.invalid_signature" {
			t.Fatalf("wrong key reason = %q", reason)
		}
	})

	t.Run("expired key", func(t *testing.T) {
		s := newTestSession(sender, true)
		expired := chatSession
		expired.ExpireTime = time.Now().Add(-time.Minute).UnixMilli()
		s.acceptChatSession(expired)
		signature := signChatPayload(t, key, sender, expired.SessionUUID, 0, 1, timestamp, "a", nil)
		if _, reason := s.resolveChatSessionMessageForTest(signature, "a", timestamp); reason != "chat.disabled.expiredProfileKey" {
			t.Fatalf("expired key reason = %q", reason)
		}
	})
}

// resolveChatSessionMessageForTest 用固定盐值构造消息并校验（仅测试使用）。
func (s *session) resolveChatSessionMessageForTest(signature []byte, text string, timestamp int64) (playerChatMessage, string) {
	return s.resolveChatMessage(protocol.SignedChatMessage{
		Message:   text,
		Timestamp: timestamp,
		Salt:      1,
		Signature: signature,
	}, nil)
}

// TestChatUnsignedPolicy 验证未签名消息在“强制安全档案”开关下的取舍：
// 未建立会话时，开关关闭则接受（离线服务器行为），开启则拒绝。
func TestChatUnsignedPolicy(t *testing.T) {
	unsigned := protocol.SignedChatMessage{Message: "hello", Timestamp: time.Now().UnixMilli()}
	for _, enforce := range []bool{false, true} {
		s := newTestSession([16]byte{0x01}, enforce)
		verified, reason := s.resolveChatMessage(unsigned, nil)
		if enforce {
			if reason != "chat.disabled.missingProfileKey" {
				t.Fatalf("enforce=%v reason = %q, want missingProfileKey", enforce, reason)
			}
			continue
		}
		if reason != "" || verified.content != "hello" || verified.index != 0 {
			t.Fatalf("enforce=%v rejected unsigned message: %q", enforce, reason)
		}
	}
}

// TestChatSessionKeyDowngrade 验证会话公钥降级保护：
// 上报过期时间更早的新公钥时断开连接。
func TestChatSessionKeyDowngrade(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.ViewDistance = 2
	instance, conn := joinServer(t, cfg, "Downgrader")

	_, chatSession := testChatSession(t)
	chatSession.ExpireTime = time.Now().Add(2 * time.Hour).UnixMilli()
	sendChatSessionUpdate(t, conn, chatSession)

	_, older := testChatSession(t)
	older.ExpireTime = time.Now().Add(time.Hour).UnixMilli()
	sendChatSessionUpdate(t, conn, older)

	if got := expectKickComponent(t, conn); got != "multiplayer.disconnect.expired_public_key" {
		t.Fatalf("kick component = %q, want expired_public_key", got)
	}
	_ = instance
}

// TestChatSignedMessageBroadcast 端到端验证签名消息：
// 服务器校验签名后转发签名与确认窗口，接收方与发送方都收到同一消息。
func TestChatSignedMessageBroadcast(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.ViewDistance = 2
	instance, firstConn := joinServer(t, cfg, "Signer")
	addr := firstConn.RemoteAddr().String()
	secondConn := joinAt(t, cfg, addr, "Watcher")

	key, chatSession := testChatSession(t)
	sendChatSessionUpdate(t, firstConn, chatSession)
	// Watcher 收到会话公钥分发（Player Info 的 initialize_chat 动作）。
	_ = expectPlayPacket(t, secondConn, protocol.PlayPacketIDPlayerInfoUpdate)

	signer := findSession(t, instance, "Signer")
	timestamp := time.Now().UnixMilli()
	salt := int64(0x2621)
	signature := signChatPayload(t, key, signer.uuid, chatSession.SessionUUID, 0, salt, timestamp, "signed hello", nil)
	sendChatMessagePacket(t, firstConn, "signed hello", timestamp, salt, signature, 0, [3]byte{}, 0)

	for i, conn := range []net.Conn{firstConn, secondConn} {
		chat := parsePlayerChatPacket(t, expectPlayPacket(t, conn, protocol.PlayPacketIDPlayerChat))
		if chat.content != "signed hello" {
			t.Fatalf("player %d content = %q", i, chat.content)
		}
		if !bytes.Equal(chat.signature, signature) {
			t.Fatalf("player %d signature not forwarded", i)
		}
		if chat.index != 0 {
			t.Fatalf("player %d index = %d, want 0", i, chat.index)
		}
		if chat.sender != signer.uuid {
			t.Fatalf("player %d sender = %x", i, chat.sender)
		}
		if len(chat.lastSeen) != 0 {
			t.Fatalf("player %d last seen = %d entries, want 0", i, len(chat.lastSeen))
		}
	}
	// 广播后接收方把消息记入确认窗口（初始 20 个占位 + 1 条待确认）。
	if count := findSession(t, instance, "Watcher").chatLastSeenCount(); count != protocol.ChatLastSeenWindow+1 {
		t.Fatalf("watcher pending count = %d, want %d", count, protocol.ChatLastSeenWindow+1)
	}
}

// TestChatInvalidSignatureKick 端到端验证签名错误时按原版断开连接。
func TestChatInvalidSignatureKick(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.ViewDistance = 2
	instance, conn := joinServer(t, cfg, "Forger")

	_, chatSession := testChatSession(t)
	sendChatSessionUpdate(t, conn, chatSession)

	otherKey, _ := testChatSession(t)
	forger := findSession(t, instance, "Forger")
	timestamp := time.Now().UnixMilli()
	signature := signChatPayload(t, otherKey, forger.uuid, chatSession.SessionUUID, 0, 1, timestamp, "forged", nil)
	sendChatMessagePacket(t, conn, "forged", timestamp, 1, signature, 0, [3]byte{}, 0)

	if got := expectKickComponent(t, conn); got != "chat.disabled.invalid_signature" {
		t.Fatalf("kick component = %q, want invalid_signature", got)
	}
}

// TestChatLastSeenChecksumKick 端到端验证确认窗口校验和错误时断开连接。
func TestChatLastSeenChecksumKick(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.ViewDistance = 2
	_, conn := joinServer(t, cfg, "Desynced")

	// 未确认任何消息时校验和为 1；发送一个错误的校验和触发断开。
	sendChatMessagePacket(t, conn, "hello", time.Now().UnixMilli(), 1, nil, 0, [3]byte{}, 0x7F)
	if got := expectKickComponent(t, conn); got != "multiplayer.disconnect.chat_validation_failed" {
		t.Fatalf("kick component = %q, want chat_validation_failed", got)
	}
}

// TestChatUnsignedMessageBroadcast 验证离线（未启用安全档案）路径：
// 未签名消息照常广播，签名字段为空。
func TestChatUnsignedMessageBroadcast(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.ViewDistance = 2
	_, firstConn := joinServer(t, cfg, "Plain")
	timestamp := time.Now().UnixMilli()
	sendChatMessagePacket(t, firstConn, "plain hello", timestamp, 0, nil, 0, [3]byte{}, 0)
	chat := parsePlayerChatPacket(t, expectPlayPacket(t, firstConn, protocol.PlayPacketIDPlayerChat))
	if chat.content != "plain hello" || chat.signature != nil || chat.index != 0 {
		t.Fatalf("unsigned chat = %+v", chat)
	}
}
