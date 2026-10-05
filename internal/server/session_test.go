package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"os"
	"testing"
	"time"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
)

// appendTestString 手工构造带 VarInt 长度前缀的字符串。
func appendTestString(dst []byte, value string) []byte {
	dst = protocol.AppendVarInt(dst, int32(len(value)))
	return append(dst, value...)
}

// readCompressedPacket 读取一个压缩格式的数据包并返回其包 ID。
func readCompressedPacket(t *testing.T, conn net.Conn) (int32, []byte) {
	t.Helper()
	packet, err := protocol.ReadPacketWithCompression(conn, compressionThreshold)
	if err != nil {
		t.Fatalf("read packet: %v", err)
	}
	packetID, _, err := protocol.DecodeVarInt(packet)
	if err != nil {
		t.Fatalf("decode packet ID: %v", err)
	}
	return packetID, packet
}

// TestOfflineLoginAndPlayFlow 模拟 1.21.11 客户端完成
// 登录 → 配置 → 进入世界 的完整流程。
func TestOfflineLoginAndPlayFlow(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	instance, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	instance.keepAliveInterval = 50 * time.Millisecond
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveResult := make(chan error, 1)
	go func() { serveResult <- instance.Serve(ctx, listener) }()

	conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}

	// 握手（next state = 2，登录）
	handshake := protocol.AppendVarInt(nil, 0)
	handshake = protocol.AppendVarInt(handshake, cfg.ProtocolVersion)
	handshake = appendTestString(handshake, "localhost")
	handshake = append(handshake, 0x63, 0xdd)
	handshake = protocol.AppendVarInt(handshake, 2)
	if err := protocol.WritePacket(conn, handshake); err != nil {
		t.Fatal(err)
	}

	// Login Start（离线模式客户端仍携带 UUID 字段）
	uuid := protocol.OfflineUUID("TestPlayer")
	loginStart := protocol.AppendVarInt(nil, 0)
	loginStart = appendTestString(loginStart, "TestPlayer")
	loginStart = append(loginStart, uuid[:]...)
	if err := protocol.WritePacket(conn, loginStart); err != nil {
		t.Fatal(err)
	}

	// Set Compression
	setCompression, err := protocol.ReadPacket(conn)
	if err != nil {
		t.Fatal(err)
	}
	setCompressionID, offset, err := protocol.DecodeVarInt(setCompression)
	if err != nil || setCompressionID != 3 {
		t.Fatalf("expected set compression, got %#x (err=%v)", setCompressionID, err)
	}
	threshold, _, err := protocol.DecodeVarInt(setCompression[offset:])
	if err != nil || threshold != compressionThreshold {
		t.Fatalf("unexpected compression threshold %d (err=%v)", threshold, err)
	}

	// Login Success：应为二进制 UUID + 用户名 + 属性数量。
	loginSuccessID, loginSuccess := readCompressedPacket(t, conn)
	if loginSuccessID != 2 {
		t.Fatalf("expected login success, got %#x", loginSuccessID)
	}
	_, offset, err = protocol.DecodeVarInt(loginSuccess)
	if err != nil || len(loginSuccess)-offset < len(uuid) || string(loginSuccess[offset:offset+len(uuid)]) != string(uuid[:]) {
		t.Fatal("login success UUID mismatch")
	}

	// Login Acknowledged
	if err := protocol.WritePacketWithCompression(conn, protocol.AppendVarInt(nil, 3), compressionThreshold); err != nil {
		t.Fatal(err)
	}

	// 配置阶段：Brand → Feature Flags → Known Packs
	for _, want := range []int32{0x01, 0x0C, 0x0E} {
		id, _ := readCompressedPacket(t, conn)
		if id != want {
			t.Fatalf("expected configuration packet %#x, got %#x", want, id)
		}
	}

	// Known Packs 响应
	knownPacks := protocol.AppendVarInt(nil, 0x07)
	knownPacks = protocol.AppendVarInt(knownPacks, 1)
	knownPacks = appendTestString(knownPacks, "minecraft")
	knownPacks = appendTestString(knownPacks, "core")
	knownPacks = appendTestString(knownPacks, "1.21.11")
	if err := protocol.WritePacketWithCompression(conn, knownPacks, compressionThreshold); err != nil {
		t.Fatal(err)
	}

	// Registry Data ×N（全部同步注册表）→ Update Tags → Finish Configuration
	for i := range registry.Synchronized() {
		id, _ := readCompressedPacket(t, conn)
		if id != 0x07 {
			t.Fatalf("expected registry data #%d, got %#x", i, id)
		}
	}
	if id, _ := readCompressedPacket(t, conn); id != 0x0D {
		t.Fatalf("expected update tags, got %#x", id)
	}
	if id, _ := readCompressedPacket(t, conn); id != 0x03 {
		t.Fatalf("expected finish configuration, got %#x", id)
	}

	// Acknowledge Finish Configuration
	if err := protocol.WritePacketWithCompression(conn, protocol.AppendVarInt(nil, 0x03), compressionThreshold); err != nil {
		t.Fatal(err)
	}

	// Play 阶段初始包：
	// Login → Set Default Spawn → Game Event → Set Center Chunk → Chunk Data →
	// Synchronize Player Position → Player Info → System Chat → Set Player Inventory
	for _, want := range []int32{0x30, 0x5F, 0x26, 0x5C, 0x2C, 0x46, 0x44, 0x77, 0x6A} {
		id, _ := readCompressedPacket(t, conn)
		if id != want {
			t.Fatalf("expected play packet %#x, got %#x", want, id)
		}
	}

	// Confirm Teleportation 与 Client Information
	confirm := protocol.AppendVarInt(nil, 0x00)
	confirm = protocol.AppendVarInt(confirm, 1)
	if err := protocol.WritePacketWithCompression(conn, confirm, compressionThreshold); err != nil {
		t.Fatal(err)
	}
	clientInfo := protocol.AppendVarInt(nil, 0x0D)
	clientInfo = appendTestString(clientInfo, "en_US")
	clientInfo = append(clientInfo, 10)
	clientInfo = protocol.AppendVarInt(clientInfo, 0)
	clientInfo = protocol.AppendBool(clientInfo, true)
	clientInfo = append(clientInfo, 0x7F)
	clientInfo = protocol.AppendVarInt(clientInfo, 1)
	clientInfo = protocol.AppendBool(clientInfo, false)
	clientInfo = protocol.AppendBool(clientInfo, true)
	clientInfo = protocol.AppendVarInt(clientInfo, 0)
	if err := protocol.WritePacketWithCompression(conn, clientInfo, compressionThreshold); err != nil {
		t.Fatal(err)
	}

	// 聊天消息：服务器应广播回 Player Chat 包（0x3F），内容包含消息文本。
	chat := protocol.AppendVarInt(nil, 0x08)
	chat = appendTestString(chat, "hello world")
	chat = protocol.AppendInt64(chat, 0)
	if err := protocol.WritePacketWithCompression(conn, chat, compressionThreshold); err != nil {
		t.Fatal(err)
	}
	chatDeadline := time.Now().Add(5 * time.Second)
	for {
		if time.Now().After(chatDeadline) {
			t.Fatal("did not receive player chat broadcast")
		}
		id, payload := readCompressedPacket(t, conn)
		if id == 0x2B {
			// Keep Alive 可能穿插其中，先回复再继续等待。
			_, offset, err := protocol.DecodeVarInt(payload)
			if err != nil {
				t.Fatal(err)
			}
			reply := protocol.AppendVarInt(nil, 0x1B)
			reply = protocol.AppendInt64(reply, int64(binary.BigEndian.Uint64(payload[offset:])))
			if err := protocol.WritePacketWithCompression(conn, reply, compressionThreshold); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if id != 0x3F {
			t.Fatalf("expected player chat broadcast, got %#x", id)
		}
		if !bytes.Contains(payload, []byte("hello world")) {
			t.Fatalf("chat payload missing message: % x", payload)
		}
		break
	}

	// 等待并响应一个 Keep Alive，确认会话保持活跃。
	deadline := time.Now().Add(5 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("did not receive keep alive from server")
		}
		id, packet := readCompressedPacket(t, conn)
		if id != 0x2B {
			continue
		}
		_, offset, err := protocol.DecodeVarInt(packet)
		if err != nil || len(packet)-offset != 8 {
			t.Fatal("malformed keep alive packet")
		}
		keepAliveID := binary.BigEndian.Uint64(packet[offset:])
		reply := protocol.AppendVarInt(nil, 0x1B)
		reply = protocol.AppendInt64(reply, int64(keepAliveID))
		if err := protocol.WritePacketWithCompression(conn, reply, compressionThreshold); err != nil {
			t.Fatal(err)
		}
		break
	}

	// 服务器关闭后应正常退出。
	cancel()
	select {
	case err := <-serveResult:
		if err != nil {
			t.Fatalf("Serve() after cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve() did not stop after context cancellation")
	}

	// 关闭时应把世界（出生区块）保存到磁盘。
	entries, err := os.ReadDir(cfg.WorldDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("expected world files to be saved on shutdown")
	}
}
