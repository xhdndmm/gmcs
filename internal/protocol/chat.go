package protocol

import "fmt"

// 聊天会话（chat_session_update）与带签名的聊天消息。
//
// 说明：gmcs 目前分发玩家的聊天会话公钥（Player Info 的 initialize_chat 动作），
// 使客户端能够识别聊天会话信息；但对聊天签名不做服务器端验证，广播消息时
// 仍按未签名消息发送（见 docs/TODO.md）。签名相关字段照常解析，便于后续实现
// 完整验证与转发。

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
