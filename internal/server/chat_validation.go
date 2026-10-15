//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

// 聊天消息签名验证与消息确认窗口（对照 1.21.11 反编译逐条移植）。
//
// 移植对照表（原版 → 本文件）：
//   - SignedMessageChain                  → chatChainState
//   - SignedMessageChain.Decoder.unpack   → session.resolveChatMessage
//   - SignedMessageChain.Decoder.unsigned → session.resolveChatMessage 的未签名分支
//   - LastSeenMessagesValidator           → chatLastSeenValidator
//   - LastSeenMessagesValidator$PendingMessage → chatPendingMessage
//   - LastSeenMessages.computeChecksum    → protocol.ChatLastSeenChecksum
//   - ServerGamePacketListenerImpl.applyLastSeenMessages / handleChat 的踢出原因
//
// 与原版的已知差异（记录在 docs/TODO.md）：
//   * 不校验会话公钥的 Mojang 签名（原版在 chat_session_update 时用内置的
//     会话服务公钥验证 KeySignature）；gmcs 只保证公钥可解析、消息签名正确。
//   * ServerboundChatCommandSigned 的命令参数签名未校验。
//   * 公钥过期只按客户端的过期时间戳判断（原版还会重新拉取档案密钥）。

import (
	"bytes"
	"crypto/rsa"
	"crypto/x509"
	"fmt"
	"log/slog"
	"time"

	"gmcs/internal/protocol"
)

const (
	// chatPendingLimit 是单个玩家的待确认消息上限（原版 4096）。
	chatPendingLimit = 4096
	// chatMessageExpiry 是消息过期时长（原版 PlayerChatMessage 的 2 分钟）。
	// 过期消息只记录警告，不拒绝（与原版链解码器一致）。
	chatMessageExpiry = 2 * time.Minute
)

// chatPendingMessage 对应原版 LastSeenMessagesValidator$PendingMessage：
// 一条等待客户端确认的消息签名。
type chatPendingMessage struct {
	signature []byte
	// unacked 为 true 表示尚未被确认（原版布尔位：addPending 添加时为 true，
	// 客户端确认后置为 false）。
	unacked bool
}

// acknowledged 返回“已确认”副本（原版 PendingMessage.acknowledge）。
func (p *chatPendingMessage) acknowledged() *chatPendingMessage {
	if !p.unacked {
		return p
	}
	return &chatPendingMessage{signature: p.signature}
}

// chatLastSeenValidator 是原版 LastSeenMessagesValidator 的移植：
// 跟踪服务器发给该客户端的消息签名，并校验客户端的确认窗口更新。
type chatLastSeenValidator struct {
	// window 是确认窗口大小（原版 lastSeenCount，为 LastSeenMessages.MAX_LENGTH = 20）。
	window int
	// tracked 是按发送顺序排列的待确认消息；nil 表示该位置已被确认并移除。
	tracked []*chatPendingMessage
	// lastPending 是最近一次 addPending 的签名（原版去重逻辑）。
	lastPending []byte
}

// newChatLastSeenValidator 创建确认窗口校验器。
// 与原版 LastSeenMessagesValidator 构造函数一致：先预填 window 个空占位，
// 使客户端确认位图的前 window 位总能映射到（可能为空的）窗口位置。
func newChatLastSeenValidator(window int) *chatLastSeenValidator {
	v := &chatLastSeenValidator{window: window}
	v.tracked = make([]*chatPendingMessage, window)
	return v
}

// addPending 记录一条新发出的消息签名，返回当前待确认数量。
// 原版会跳过与上一条相同的签名（客户端可能对同一条消息重复确认）。
func (v *chatLastSeenValidator) addPending(signature []byte) int {
	if v.lastPending != nil && bytes.Equal(v.lastPending, signature) {
		return len(v.tracked)
	}
	v.tracked = append(v.tracked, &chatPendingMessage{signature: signature, unacked: true})
	v.lastPending = signature
	return len(v.tracked)
}

// trackedCount 返回当前待确认消息数量（原版 trackedMessagesCount）。
func (v *chatLastSeenValidator) trackedCount() int {
	return len(v.tracked)
}

// applyOffset 丢弃最早的 offset 条待确认消息（原版 applyOffset）。
func (v *chatLastSeenValidator) applyOffset(offset int32) error {
	max := int32(len(v.tracked) - v.window)
	if offset < 0 || offset > max {
		return fmt.Errorf("invalid offset: %d (max: %d)", offset, max)
	}
	remaining := copy(v.tracked, v.tracked[offset:])
	for i := remaining; i < len(v.tracked); i++ {
		v.tracked[i] = nil
	}
	v.tracked = v.tracked[:remaining]
	return nil
}

// applyUpdate 校验一条 LastSeenMessages.Update（消息确认窗口更新），
// 返回客户端确认过的签名列表（顺序与客户端一致）。
// 对应原版 LastSeenMessagesValidator.applyUpdate，任何不一致都返回错误并由
// 调用方按原版行为断开连接（multiplayer.disconnect.chat_validation_failed）。
func (v *chatLastSeenValidator) applyUpdate(msg protocol.SignedChatMessage) ([][]byte, error) {
	if err := v.applyOffset(msg.Offset); err != nil {
		return nil, err
	}
	if length := msg.AcknowledgedLength(); length > v.window {
		return nil, fmt.Errorf("invalid acknowledged count: %d (expected: %d)", length, v.window)
	}
	entries := make([][]byte, 0, v.window)
	for i := 0; i < v.window; i++ {
		acked := msg.AcknowledgedBit(i)
		var pending *chatPendingMessage
		if i < len(v.tracked) {
			pending = v.tracked[i]
		}
		if acked {
			if pending == nil {
				return nil, fmt.Errorf("missing last seen entry at index %d", i)
			}
			v.tracked[i] = pending.acknowledged()
			entries = append(entries, pending.signature)
			continue
		}
		if pending != nil && !pending.unacked {
			return nil, fmt.Errorf("unacknowledged entry at index %d", i)
		}
		if i < len(v.tracked) {
			v.tracked[i] = nil
		}
	}
	if checksum := protocol.ChatLastSeenChecksum(entries); msg.Checksum != 0 && msg.Checksum != checksum {
		return nil, fmt.Errorf("checksum mismatch on last seen update: the client and server must have desynced")
	}
	return entries, nil
}

// chatChainState 是玩家的聊天消息链状态（原版 SignedMessageChain）。
type chatChainState struct {
	// sessionUUID 是 Chat Session Update 上报的会话 UUID。
	sessionUUID [16]byte
	// publicKey 是会话公钥（nil 表示上报的公钥无法解析）。
	publicKey *rsa.PublicKey
	// expireMillis 是公钥过期时间（Unix 毫秒，来自客户端上报）。
	expireMillis int64
	// nextIndex 是下一条消息期望的链序号（原版 nextLink.index）。
	nextIndex int32
	// lastMillis 是上一条通过校验消息的时间戳（原版 lastTimeStamp）。
	lastMillis int64
	// broken 表示消息链已损坏（原版 setChainBroken）。
	broken bool
}

// playerChatMessage 是通过校验、准备广播的玩家聊天消息。
type playerChatMessage struct {
	content         string
	timestampMillis int64
	salt            int64
	index           int32
	signature       []byte
	lastSeen        [][]byte
}

// parseChatPublicKey 解析客户端聊天会话公钥（DER 编码的 X.509 RSA 公钥）。
func parseChatPublicKey(encoded []byte) (*rsa.PublicKey, error) {
	parsed, err := x509.ParsePKIXPublicKey(encoded)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("chat public key is not RSA")
	}
	return key, nil
}

// chatSessionResult 是 acceptChatSession 的结果。
type chatSessionResult int

const (
	// chatSessionAccepted 表示会话已接受，需要分发给其他玩家。
	chatSessionAccepted chatSessionResult = iota
	// chatSessionDuplicate 表示与现有会话相同的重复上报（原版忽略）。
	chatSessionDuplicate
	// chatSessionRejected 表示会话被拒绝（可能已断开连接）。
	chatSessionRejected
)

// acceptChatSession 处理客户端的 Chat Session Update，与原版
// ServerGamePacketListenerImpl.handleChatSessionUpdate 对应：
//   - 与现有会话相同：忽略；
//   - 新公钥过期时间早于现有公钥：断开（防密钥降级）；
//   - 公钥可解析：重建消息链状态。
//
// 与原版的差异：原版在安装会话前用内置的会话服务公钥验证 KeySignature，
// gmcs 没有该公钥，只验证公钥可解析（见文件头说明）。
func (s *session) acceptChatSession(session protocol.ChatSession) chatSessionResult {
	key, err := parseChatPublicKey(session.PublicKey)
	if err != nil {
		slog.Info("invalid chat public key", "name", s.name, "error", err)
		s.kickWithComponent("multiplayer.disconnect.invalid_public_key_signature")
		return chatSessionRejected
	}
	s.chatMu.Lock()
	if existing := s.chatSession; existing != nil {
		if existing.SessionUUID == session.SessionUUID && existing.ExpireTime == session.ExpireTime &&
			bytes.Equal(existing.PublicKey, session.PublicKey) {
			s.chatMu.Unlock()
			return chatSessionDuplicate
		}
		if session.ExpireTime < existing.ExpireTime {
			s.chatMu.Unlock()
			slog.Info("chat session key downgrade", "name", s.name)
			s.kickWithComponent("multiplayer.disconnect.expired_public_key")
			return chatSessionRejected
		}
	}
	s.chatSession = &session
	// 原版在设置会话时重建链解码器：链序号归零、时间戳重置、解除链损坏标记。
	s.chatChain = &chatChainState{
		sessionUUID:  session.SessionUUID,
		publicKey:    key,
		expireMillis: session.ExpireTime,
	}
	s.chatMu.Unlock()
	return chatSessionAccepted
}

// applyChatLastSeenUpdate 校验消息确认窗口，返回客户端确认的签名列表。
// 对应原版 ServerGamePacketListenerImpl.applyLastSeenMessages。
func (s *session) applyChatLastSeenUpdate(msg protocol.SignedChatMessage) ([][]byte, error) {
	s.chatMu.Lock()
	defer s.chatMu.Unlock()
	if s.chatLastSeen == nil {
		s.chatLastSeen = newChatLastSeenValidator(protocol.ChatLastSeenWindow)
	}
	return s.chatLastSeen.applyUpdate(msg)
}

// resolveChatMessage 校验一条聊天消息，返回可广播的结果。
// reason 非空时表示应断开连接，值为原版踢出组件的翻译键。
func (s *session) resolveChatMessage(msg protocol.SignedChatMessage, lastSeen [][]byte) (playerChatMessage, string) {
	s.chatMu.Lock()
	chain := s.chatChain
	s.chatMu.Unlock()

	if chain == nil {
		// 未建立聊天会话（离线模式、或客户端尚未上报会话）：
		// 与原版 Decoder.unsigned 一致——未启用安全档案时按未签名消息处理，
		// 即使带签名也不做链校验（原版该分支完全忽略签名）。
		if s.server.secureProfileEnforced() {
			return playerChatMessage{}, "chat.disabled.missingProfileKey"
		}
		return playerChatMessage{
			content:         msg.Message,
			timestampMillis: msg.Timestamp,
			salt:            msg.Salt,
			lastSeen:        lastSeen,
		}, ""
	}
	// 已建立聊天会话：原版链解码器要求必须带签名。
	if len(msg.Signature) != protocol.MaxChatSignatureLength {
		return playerChatMessage{}, "chat.disabled.missingProfileKey"
	}
	if chain.expireMillis > 0 && time.Now().UnixMilli() > chain.expireMillis {
		return playerChatMessage{}, "chat.disabled.expiredProfileKey"
	}
	if chain.broken || chain.publicKey == nil {
		return playerChatMessage{}, "chat.disabled.chain_broken"
	}
	if msg.Timestamp < chain.lastMillis {
		s.breakChatChain(chain)
		return playerChatMessage{}, "chat.disabled.out_of_order_chat"
	}
	payload := protocol.ChatSignaturePayload(s.uuid, chain.sessionUUID, chain.nextIndex, msg.Salt, msg.Timestamp, msg.Message, lastSeen)
	if !protocol.VerifyChatSignature(chain.publicKey, payload, msg.Signature) {
		s.breakChatChain(chain)
		return playerChatMessage{}, "chat.disabled.invalid_signature"
	}
	if sentAt := time.UnixMilli(msg.Timestamp); time.Since(sentAt) > chatMessageExpiry {
		slog.Warn("received expired chat, is the client/server system time unsynchronized?",
			"name", s.name, "timestamp", sentAt)
	}
	index := chain.nextIndex
	s.chatMu.Lock()
	chain.lastMillis = msg.Timestamp
	chain.nextIndex++
	s.chatMu.Unlock()
	return playerChatMessage{
		content:         msg.Message,
		timestampMillis: msg.Timestamp,
		salt:            msg.Salt,
		index:           index,
		signature:       msg.Signature,
		lastSeen:        lastSeen,
	}, ""
}

// breakChatChain 标记消息链损坏（原版 setChainBroken），之后的签名消息一律拒绝。
func (s *session) breakChatChain(chain *chatChainState) {
	s.chatMu.Lock()
	chain.broken = true
	s.chatMu.Unlock()
	slog.Info("chat message chain broken", "name", s.name)
}

// recordPendingChatSignature 把一条已发送给该玩家的消息签名记入确认窗口，
// 对应原版 ServerGamePacketListenerImpl.broadcastChatMessage 的 addPending；
// 待确认数量超过原版上限（4096）时断开连接。
func (s *session) recordPendingChatSignature(signature []byte) {
	s.chatMu.Lock()
	if s.chatLastSeen == nil {
		s.chatLastSeen = newChatLastSeenValidator(protocol.ChatLastSeenWindow)
	}
	count := s.chatLastSeen.addPending(signature)
	s.chatMu.Unlock()
	if count > chatPendingLimit {
		slog.Info("too many pending chats", "name", s.name, "count", count)
		s.kickWithComponent("multiplayer.disconnect.too_many_pending_chats")
	}
}

// nextChatGlobalIndex 返回该连接的下一个聊天序号（每个连接独立递增，
// 对应原版 ServerGamePacketListenerImpl.chatIndex）。
func (s *session) nextChatGlobalIndex() int32 {
	s.chatMu.Lock()
	defer s.chatMu.Unlock()
	index := s.chatGlobalIndex
	s.chatGlobalIndex++
	return index
}

// chatLastSeenCount 返回待确认消息数量（测试用）。
func (s *session) chatLastSeenCount() int {
	s.chatMu.Lock()
	defer s.chatMu.Unlock()
	if s.chatLastSeen == nil {
		return 0
	}
	return s.chatLastSeen.trackedCount()
}

// kickWithComponent 发送带翻译键组件的断开包并关闭连接（读循环随后退出）。
func (s *session) kickWithComponent(key string) {
	_ = s.writePacket(protocol.EncodePlayDisconnectTranslatable(key))
	_ = s.conn.Close()
}
