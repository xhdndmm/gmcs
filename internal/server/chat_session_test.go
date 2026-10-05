package server

import (
	"bytes"
	"net"
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
)

// sendChatSessionUpdate 发送聊天会话公钥包（格式与真实客户端一致）。
func sendChatSessionUpdate(t *testing.T, conn net.Conn, session protocol.ChatSession) {
	t.Helper()
	packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDChatSessionUpdate)
	packet = append(packet, session.SessionUUID[:]...)
	packet = protocol.AppendInt64(packet, session.ExpireTime)
	packet = protocol.AppendVarInt(packet, int32(len(session.PublicKey)))
	packet = append(packet, session.PublicKey...)
	packet = protocol.AppendVarInt(packet, int32(len(session.KeySignature)))
	packet = append(packet, session.KeySignature...)
	if err := protocol.WritePacketWithCompression(conn, packet, compressionThreshold); err != nil {
		t.Fatal(err)
	}
}

// TestChatSessionUpdateBroadcast 验证聊天会话公钥的处理：
//   - 上报后服务器保存会话；
//   - 其他在线玩家收到 Player Info 的 Initialize Chat 动作（含公钥与签名）；
//   - 后加入的玩家在加入流程中收到已有玩家的会话信息。
func TestChatSessionUpdateBroadcast(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.ViewDistance = 2
	instance, firstConn := joinServer(t, cfg, "First")
	addr := firstConn.RemoteAddr().String()

	// 第二名玩家先加入，便于观察 First 的会话广播。
	secondConn := joinAt(t, cfg, addr, "Second", len(cfg.StartingItems))

	session := protocol.ChatSession{
		SessionUUID:  [16]byte{0xA1, 0xA2},
		ExpireTime:   1_800_000_000_000,
		PublicKey:    bytes.Repeat([]byte{0x11}, 294),
		KeySignature: bytes.Repeat([]byte{0x22}, 256),
	}
	sendChatSessionUpdate(t, firstConn, session)

	// Second 收到 Initialize Chat 动作的数据包。
	payload := expectPlayPacket(t, secondConn, protocol.PlayPacketIDPlayerInfoUpdate)
	_, offset, err := protocol.DecodeVarInt(payload) // 跳过包 ID
	if err != nil {
		t.Fatal(err)
	}
	if payload[offset] != 0x02 {
		t.Fatalf("action bits = %#x, want 0x02 (initialize_chat)", payload[offset])
	}
	offset++
	_, size, err := protocol.DecodeVarInt(payload[offset:])
	if err != nil {
		t.Fatal(err)
	}
	offset += size
	first := findSession(t, instance, "First")
	if !bytes.Equal(payload[offset:offset+16], first.uuid[:]) {
		t.Fatal("Player Info 的 UUID 不是上报会话的玩家")
	}
	offset += 16
	if payload[offset] != 0x01 {
		t.Fatal("会话存在位应为 1")
	}
	offset++
	if !bytes.Equal(payload[offset:offset+16], session.SessionUUID[:]) {
		t.Fatal("会话 UUID 不匹配")
	}
	offset += 16
	expire, offset, err := protocol.DecodeInt64(payload, offset)
	if err != nil || expire != session.ExpireTime {
		t.Fatalf("过期时间 = %d (err=%v)", expire, err)
	}
	keyLength, size, err := protocol.DecodeVarInt(payload[offset:])
	if err != nil || int(keyLength) != len(session.PublicKey) {
		t.Fatalf("公钥长度 = %d (err=%v)", keyLength, err)
	}
	offset += size
	if !bytes.Equal(payload[offset:offset+int(keyLength)], session.PublicKey) {
		t.Fatal("公钥内容不匹配")
	}
	offset += int(keyLength)
	sigLength, size, err := protocol.DecodeVarInt(payload[offset:])
	if err != nil || int(sigLength) != len(session.KeySignature) {
		t.Fatalf("签名长度 = %d (err=%v)", sigLength, err)
	}
	offset += size
	if !bytes.Equal(payload[offset:offset+int(sigLength)], session.KeySignature) {
		t.Fatal("签名内容不匹配")
	}

	// 服务器侧保存了会话。
	if stored := first.chatSessionSnapshot(); stored == nil || stored.SessionUUID != session.SessionUUID {
		t.Fatal("会话未保存到玩家状态")
	}

	// Third 加入：应收到 First 的会话信息（Player Info initialize_chat）。
	// 加入流程会消费掉大部分初始包，因此用观察者回调收集。
	var sessions [][]byte
	observer := func(id int32, payload []byte) {
		if id != protocol.PlayPacketIDPlayerInfoUpdate || len(payload) < 2 {
			return
		}
		_, offset, err := protocol.DecodeVarInt(payload)
		if err != nil || payload[offset] != 0x02 {
			return
		}
		sessions = append(sessions, append([]byte(nil), payload...))
	}
	_ = joinAt(t, cfg, addr, "Third", len(cfg.StartingItems), observer)
	found := false
	for _, payload := range sessions {
		if bytes.Contains(payload, session.SessionUUID[:]) {
			found = true
		}
	}
	if !found {
		t.Fatal("新玩家加入时应收到已有玩家的聊天会话信息")
	}
}
