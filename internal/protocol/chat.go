//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package protocol

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"fmt"
)

// 聊天会话（chat_session_update）与带签名的聊天消息。
//
// 本文件实现 1.21.11 的聊天签名线格式：
//   - Serverbound Chat Message：消息文本、时间戳、盐、可选 256 字节签名、
//     消息确认窗口（LastSeenMessages.Update）；
//   - 签名字节序列（ChatSignaturePayload）：服务器用它校验客户端签名；
//   - 消息确认窗口的校验和（ChatLastSeenChecksum）：与原版客户端保持一致，
//     避免因窗口不同步而误判。
//
// 移植依据：1.21.11 服务端反编译（SignedMessage/SignedMessageLink/
// SignedMessageBody/LastSeenMessages/MessageSignature），逐条对照字节码实现。

// ChatSession 是玩家上报的聊天会话（客户端持有的聊天签名公钥）。
type ChatSession struct {
	// SessionUUID 是会话标识。
	SessionUUID [16]byte
	// ExpireTime 是会话过期时间（Unix 毫秒）。
	ExpireTime int64
	// PublicKey 是客户端聊天公钥（DER 编码的 RSA 公钥）。
	PublicKey []byte
	// KeySignature 是 Mojang 会话服务对该公钥的签名。
	KeySignature []byte
}

// maxChatKeySize 是聊天公钥/签名的长度上限（防超大包占用内存）。
const maxChatKeySize = 4096

// ParseChatSessionUpdate 解析 Serverbound Chat Session Update 包。
func ParseChatSessionUpdate(packet []byte) (ChatSession, error) {
	var session ChatSession
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != PlayServerboundPacketIDChatSessionUpdate {
		return session, fmt.Errorf("invalid chat session update packet")
	}
	if len(packet)-offset < 16 {
		return session, fmt.Errorf("chat session update packet truncated")
	}
	copy(session.SessionUUID[:], packet[offset:offset+16])
	offset += 16
	if session.ExpireTime, offset, err = DecodeInt64(packet, offset); err != nil {
		return session, err
	}
	keyLength, size, err := DecodeVarInt(packet[offset:])
	if err != nil {
		return session, err
	}
	offset += size
	if keyLength < 0 || keyLength > maxChatKeySize || len(packet)-offset < int(keyLength) {
		return session, fmt.Errorf("invalid chat public key length")
	}
	session.PublicKey = append([]byte(nil), packet[offset:offset+int(keyLength)]...)
	offset += int(keyLength)
	signatureLength, size, err := DecodeVarInt(packet[offset:])
	if err != nil {
		return session, err
	}
	offset += size
	if signatureLength < 0 || signatureLength > maxChatKeySize || len(packet)-offset < int(signatureLength) {
		return session, fmt.Errorf("invalid chat key signature length")
	}
	session.KeySignature = append([]byte(nil), packet[offset:offset+int(signatureLength)]...)
	return session, nil
}

// EncodePlayerInfoChatSession 编码 Player Info Update 包，
// 动作为 Initialize Chat（向其他客户端分发玩家的聊天会话公钥）。
func EncodePlayerInfoChatSession(uuid [16]byte, session ChatSession) []byte {
	const actionInitializeChat = 0x02
	packet := AppendVarInt(nil, int32(PlayPacketIDPlayerInfoUpdate))
	packet = append(packet, actionInitializeChat)
	packet = AppendVarInt(packet, 1) // 玩家数量
	packet = append(packet, uuid[:]...)
	packet = append(packet, 0x01) // 存在聊天会话
	packet = append(packet, session.SessionUUID[:]...)
	packet = AppendInt64(packet, session.ExpireTime)
	packet = AppendVarInt(packet, int32(len(session.PublicKey)))
	packet = append(packet, session.PublicKey...)
	packet = AppendVarInt(packet, int32(len(session.KeySignature)))
	return append(packet, session.KeySignature...)
}

// SignedChatMessage 是 Serverbound Chat Message 包的解析结果。
type SignedChatMessage struct {
	// Message 是消息文本。
	Message string
	// Timestamp 是客户端签名时的时间戳（Unix 毫秒）。
	Timestamp int64
	// Salt 是签名盐（原版用于防止重放/预计算）。
	Salt int64
	// Signature 是 256 字节消息签名；nil 表示未签名。
	Signature []byte
	// Offset 是消息确认窗口的偏移。
	Offset int32
	// Acknowledged 是 3 字节的已确认位图。
	Acknowledged [3]byte
	// Checksum 是客户端计算的校验和。
	Checksum uint8
}

// MaxChatSignatureLength 是消息签名长度（RSA-2048 / SHA-256）。
const MaxChatSignatureLength = 256

// ChatLastSeenWindow 是消息确认窗口大小（原版 LastSeenMessages.MAX_LENGTH）。
// 客户端的确认位图与服务器的确认列表都以该值为界。
const ChatLastSeenWindow = 20

// chatSignatureVersion 是签名负载的版本号（原版 SignedMessage.updateSignature
// 固定写入 Ints.toByteArray(1)）。
const chatSignatureVersion = 1

// ParseSignedChatMessage 解析 Serverbound Chat Message 包（含签名与确认字段）。
func ParseSignedChatMessage(packet []byte) (SignedChatMessage, error) {
	var message SignedChatMessage
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != PlayServerboundPacketIDChatMessage {
		return message, fmt.Errorf("invalid chat message packet")
	}
	text, offset, err := readStringAt(packet, offset)
	if err != nil {
		return message, err
	}
	if len(text) > MaxChatMessageLength {
		return message, fmt.Errorf("chat message too long: %d", len(text))
	}
	message.Message = text
	if message.Timestamp, offset, err = DecodeInt64(packet, offset); err != nil {
		return message, err
	}
	if message.Salt, offset, err = DecodeInt64(packet, offset); err != nil {
		return message, err
	}
	if message.Signature = nil; len(packet) <= offset {
		return message, fmt.Errorf("chat message packet truncated at signature")
	}
	hasSignature := packet[offset] != 0
	offset++
	if hasSignature {
		if len(packet)-offset < MaxChatSignatureLength {
			return message, fmt.Errorf("chat message signature truncated")
		}
		message.Signature = append([]byte(nil), packet[offset:offset+MaxChatSignatureLength]...)
		offset += MaxChatSignatureLength
	}
	// decodeVarIntAt 返回新的偏移。
	if message.Offset, offset, err = decodeVarIntAt(packet, offset); err != nil {
		return message, err
	}
	// acknowledged（3 字节）+ checksum（1 字节）。
	if len(packet)-offset < 4 {
		return message, fmt.Errorf("chat message packet truncated at acknowledged")
	}
	copy(message.Acknowledged[:], packet[offset:offset+3])
	message.Checksum = packet[offset+3]
	return message, nil
}

// AcknowledgedBit 返回确认位图中第 i 位。
// 原版用 FriendlyByteBuf.readFixedBitSet（BitSet.valueOf(byte[])），即位序为
// 小端：bit i = bytes[i/8] & (1 << (i%8))。
func (m SignedChatMessage) AcknowledgedBit(i int) bool {
	if i < 0 || i >= ChatLastSeenWindow {
		return false
	}
	return m.Acknowledged[i/8]&(1<<uint(i%8)) != 0
}

// AcknowledgedLength 返回确认位图的长度（最高置位 + 1），
// 对应原版 BitSet.length()（0 表示没有任何位被置位）。
func (m SignedChatMessage) AcknowledgedLength() int {
	length := 0
	for i := 0; i < ChatLastSeenWindow; i++ {
		if m.AcknowledgedBit(i) {
			length = i + 1
		}
	}
	return length
}

// ChatSignaturePayload 返回原版聊天签名的待签名字节序列。
//
// 与原版 SignedMessage.updateSignature 一致：
//
//	int32(1)                      // 版本号（Ints.toByteArray(1)）
//	senderUUID(16)
//	sessionUUID(16)
//	int32(index)                  // 消息在会话链中的序号
//	int64(salt)
//	int64(timestampEpochSecond)   // Instant.getEpochSecond()
//	int32(len(contentUTF8)) + contentUTF8
//	int32(len(lastSeen)) + 每条 256 字节签名
//
// lastSeen 是服务器按消息确认窗口重新构造的列表（与客户端签名时一致）。
func ChatSignaturePayload(senderUUID, sessionUUID [16]byte, index int32, salt, timestampMillis int64, content string, lastSeen [][]byte) []byte {
	payload := make([]byte, 0, 4+16+16+4+8+8+4+len(content)+4+len(lastSeen)*MaxChatSignatureLength)
	payload = AppendInt32(payload, chatSignatureVersion)
	payload = append(payload, senderUUID[:]...)
	payload = append(payload, sessionUUID[:]...)
	payload = AppendInt32(payload, index)
	payload = AppendInt64(payload, salt)
	payload = AppendInt64(payload, timestampMillis/1000)
	payload = AppendInt32(payload, int32(len(content)))
	payload = append(payload, content...)
	payload = AppendInt32(payload, int32(len(lastSeen)))
	for _, signature := range lastSeen {
		payload = append(payload, signature...)
	}
	return payload
}

// VerifyChatSignature 校验聊天签名（SHA-256 + RSA PKCS#1 v1.5，对应原版
// SignatureValidator/Signature.getInstance("SHA256withRSA")）。
func VerifyChatSignature(key *rsa.PublicKey, payload, signature []byte) bool {
	if key == nil || len(signature) != MaxChatSignatureLength {
		return false
	}
	digest := sha256.Sum256(payload)
	return rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature) == nil
}

// ChatLastSeenChecksum 计算消息确认窗口的校验和，对应原版
// LastSeenMessages.computeChecksum：
//
//	byte h = 1; for (sig : entries) h = 31 * h + Arrays.hashCode(sig);
//	return h == 0 ? 1 : h;
func ChatLastSeenChecksum(entries [][]byte) uint8 {
	h := int32(1)
	for _, entry := range entries {
		h = 31*h + javaByteArrayHash(entry)
	}
	result := uint8(h)
	if result == 0 {
		return 1
	}
	return result
}

// javaByteArrayHash 对应 java.util.Arrays.hashCode(byte[])：
// 初始值 1，逐个累加 31 * h + b[i]（byte 为有符号值，int 溢出按二补码回绕）。
func javaByteArrayHash(value []byte) int32 {
	h := int32(1)
	for _, b := range value {
		h = 31*h + int32(int8(b))
	}
	return h
}
