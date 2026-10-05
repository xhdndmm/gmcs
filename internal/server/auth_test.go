package server

import (
	"context"
	"crypto/aes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
)

// readTestString 从字节流中解析带 VarInt 长度前缀的字符串。
func readTestString(t *testing.T, data []byte, offset int) (string, int) {
	t.Helper()
	length, size, err := protocol.DecodeVarInt(data[offset:])
	if err != nil {
		t.Fatalf("decode string length: %v", err)
	}
	offset += size
	if length < 0 || offset+int(length) > len(data) {
		t.Fatalf("string out of bounds: length %d at offset %d (total %d)", length, offset, len(data))
	}
	return string(data[offset : offset+int(length)]), offset + int(length)
}

// readTestByteArray 从字节流中解析带 VarInt 长度前缀的字节数组。
func readTestByteArray(t *testing.T, data []byte, offset int) ([]byte, int) {
	t.Helper()
	length, size, err := protocol.DecodeVarInt(data[offset:])
	if err != nil {
		t.Fatalf("decode byte array length: %v", err)
	}
	offset += size
	if length < 0 || offset+int(length) > len(data) {
		t.Fatalf("byte array out of bounds: length %d at offset %d (total %d)", length, offset, len(data))
	}
	out := make([]byte, length)
	copy(out, data[offset:offset+int(length)])
	return out, offset + int(length)
}

func TestParseUndashedUUID(t *testing.T) {
	want := [16]byte{0x06, 0x9a, 0x79, 0xf4, 0x44, 0xe9, 0x47, 0x26, 0xa5, 0xbe, 0xfc, 0xa9, 0x0e, 0x38, 0xaa, 0xf5}
	for _, value := range []string{
		"069a79f444e94726a5befca90e38aaf5",
		"069a79f4-44e9-4726-a5be-fca90e38aaf5",
	} {
		got, err := parseUndashedUUID(value)
		if err != nil {
			t.Fatalf("parseUndashedUUID(%q): %v", value, err)
		}
		if got != want {
			t.Fatalf("parseUndashedUUID(%q) = %x, want %x", value, got, want)
		}
	}
	for _, value := range []string{"", "0629a79f4", "069a79f444e94726a5befca90e38aaf5ff", "zz9a79f444e94726a5befca90e38aaf5"} {
		if _, err := parseUndashedUUID(value); err == nil {
			t.Fatalf("parseUndashedUUID(%q) expected error", value)
		}
	}
}

func TestVerifySession(t *testing.T) {
	profileJSON := `{"id":"069a79f444e94726a5befca90e38aaf5","name":"OnlinePlayer","properties":[` +
		`{"name":"textures","value":"txt","signature":"sig"},{"name":"unsigned","value":"v"}]}`
	var lastServerID string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/session/minecraft/hasJoined" {
			http.NotFound(w, r)
			return
		}
		lastServerID = r.URL.Query().Get("serverId")
		switch r.URL.Query().Get("username") {
		case "OnlinePlayer":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, profileJSON)
		case "Rejected":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "boom", http.StatusInternalServerError)
		}
	})
	testServer := httptest.NewServer(handler)
	defer testServer.Close()

	cfg := config.Default()
	// 故意带尾斜杠，验证 URL 拼接会去除它。
	cfg.SessionServerURL = testServer.URL + "/"
	instance := &Server{config: cfg, httpClient: testServer.Client()}

	profile, err := instance.verifySession("OnlinePlayer", "somehash")
	if err != nil {
		t.Fatalf("verifySession: %v", err)
	}
	if lastServerID != "somehash" {
		t.Fatalf("serverId query = %q, want %q", lastServerID, "somehash")
	}
	if profile.UUID != [16]byte{0x06, 0x9a, 0x79, 0xf4, 0x44, 0xe9, 0x47, 0x26, 0xa5, 0xbe, 0xfc, 0xa9, 0x0e, 0x38, 0xaa, 0xf5} {
		t.Fatalf("unexpected UUID: %x", profile.UUID)
	}
	if profile.Name != "OnlinePlayer" {
		t.Fatalf("unexpected name: %q", profile.Name)
	}
	if len(profile.Properties) != 2 || profile.Properties[0].Name != "textures" || profile.Properties[0].Value != "txt" ||
		!profile.Properties[0].Signed || profile.Properties[0].Signature != "sig" {
		t.Fatalf("unexpected properties: %+v", profile.Properties)
	}
	if profile.Properties[1].Signed || profile.Properties[1].Signature != "" {
		t.Fatalf("unsigned property should not be marked signed: %+v", profile.Properties[1])
	}

	if _, err := instance.verifySession("Rejected", "somehash"); !errors.Is(err, errSessionRejected) {
		t.Fatalf("expected errSessionRejected, got %v", err)
	}
	if _, err := instance.verifySession("Other", "somehash"); err == nil || errors.Is(err, errSessionRejected) {
		t.Fatalf("expected server error, got %v", err)
	}
}

// TestOnlineLoginFlow 模拟正版客户端完成加密握手：
// 登录开始 → 加密请求 → 加密响应 → 会话验证 → 加密的 Login Success。
func TestOnlineLoginFlow(t *testing.T) {
	profileUUID := [16]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}
	var receivedName, receivedServerID string
	sessionServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedName = r.URL.Query().Get("username")
		receivedServerID = r.URL.Query().Get("serverId")
		_, _ = fmt.Fprintf(w, `{"id":"%x","name":"OnlinePlayer","properties":[{"name":"textures","value":"txt","signature":"sig"}]}`, profileUUID)
	}))
	defer sessionServer.Close()

	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.OnlineMode = true
	cfg.SessionServerURL = sessionServer.URL
	instance, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
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
		cancel()
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		cancel()
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

	// Login Start
	loginStart := protocol.AppendVarInt(nil, 0)
	loginStart = appendTestString(loginStart, "OnlinePlayer")
	loginStart = append(loginStart, 0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, 0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99)
	if err := protocol.WritePacket(conn, loginStart); err != nil {
		t.Fatal(err)
	}

	// Encryption Request（明文）
	encryptionRequest, err := protocol.ReadPacket(conn)
	if err != nil {
		t.Fatal(err)
	}
	requestID, offset, err := protocol.DecodeVarInt(encryptionRequest)
	if err != nil || requestID != 0x01 {
		t.Fatalf("expected encryption request, got %#x (err=%v)", requestID, err)
	}
	serverID, offset := readTestString(t, encryptionRequest, offset)
	if serverID != "" {
		t.Fatalf("expected empty server ID, got %q", serverID)
	}
	publicKeyDER, offset := readTestByteArray(t, encryptionRequest, offset)
	verifyToken, offset := readTestByteArray(t, encryptionRequest, offset)
	if len(verifyToken) != 4 {
		t.Fatalf("expected 4-byte verify token, got %d bytes", len(verifyToken))
	}
	if offset >= len(encryptionRequest) || encryptionRequest[offset] == 0 {
		t.Fatal("expected shouldAuthenticate = true")
	}

	// Encryption Response（明文）：用服务器公钥加密共享密钥与验证令牌。
	publicKey, err := x509.ParsePKIXPublicKey(publicKeyDER)
	if err != nil {
		t.Fatalf("parse public key: %v", err)
	}
	rsaPublicKey, ok := publicKey.(*rsa.PublicKey)
	if !ok {
		t.Fatal("expected RSA public key")
	}
	sharedSecret := make([]byte, 16)
	if _, err := rand.Read(sharedSecret); err != nil {
		t.Fatal(err)
	}
	encryptedSecret, err := rsa.EncryptPKCS1v15(rand.Reader, rsaPublicKey, sharedSecret)
	if err != nil {
		t.Fatal(err)
	}
	encryptedToken, err := rsa.EncryptPKCS1v15(rand.Reader, rsaPublicKey, verifyToken)
	if err != nil {
		t.Fatal(err)
	}
	response := protocol.AppendVarInt(nil, 0x01)
	response = protocol.AppendVarInt(response, int32(len(encryptedSecret)))
	response = append(response, encryptedSecret...)
	response = protocol.AppendVarInt(response, int32(len(encryptedToken)))
	response = append(response, encryptedToken...)
	if err := protocol.WritePacket(conn, response); err != nil {
		t.Fatal(err)
	}

	// 此后通道已加密：包装客户端读写。
	block, err := aes.NewCipher(sharedSecret)
	if err != nil {
		t.Fatal(err)
	}
	clientReader := protocol.NewStreamReader(conn, protocol.NewCFB8Decrypter(block, sharedSecret))
	clientWriter := protocol.NewStreamWriter(conn, protocol.NewCFB8Encrypter(block, sharedSecret))

	// Set Compression（加密，仍为未压缩帧格式）
	setCompression, err := protocol.ReadPacket(clientReader)
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

	// Login Success（加密 + 压缩帧）：UUID/名称来自会话服务器，包含签名属性。
	loginSuccess, err := protocol.ReadPacketWithCompression(clientReader, compressionThreshold)
	if err != nil {
		t.Fatal(err)
	}
	loginSuccessID, offset, err := protocol.DecodeVarInt(loginSuccess)
	if err != nil || loginSuccessID != 2 {
		t.Fatalf("expected login success, got %#x (err=%v)", loginSuccessID, err)
	}
	if len(loginSuccess)-offset < 16 || [16]byte(loginSuccess[offset:offset+16]) != profileUUID {
		t.Fatalf("login success UUID mismatch: % x", loginSuccess[offset:min(offset+16, len(loginSuccess))])
	}
	offset += 16
	name, offset := readTestString(t, loginSuccess, offset)
	if name != "OnlinePlayer" {
		t.Fatalf("login success name = %q, want OnlinePlayer", name)
	}
	propertyCount, size, err := protocol.DecodeVarInt(loginSuccess[offset:])
	if err != nil || propertyCount != 1 {
		t.Fatalf("expected 1 property, got %d (err=%v)", propertyCount, err)
	}
	offset += size
	propertyName, offset := readTestString(t, loginSuccess, offset)
	propertyValue, offset := readTestString(t, loginSuccess, offset)
	if propertyName != "textures" || propertyValue != "txt" {
		t.Fatalf("unexpected property %q=%q", propertyName, propertyValue)
	}
	if offset >= len(loginSuccess) || loginSuccess[offset] != 1 {
		t.Fatal("expected signed property flag")
	}
	offset++
	signature, offset := readTestString(t, loginSuccess, offset)
	if signature != "sig" {
		t.Fatalf("unexpected property signature %q", signature)
	}
	if offset != len(loginSuccess) {
		t.Fatalf("trailing bytes in login success: %d", len(loginSuccess)-offset)
	}

	// 会话服务器应收到正确的用户名与哈希。
	if receivedName != "OnlinePlayer" {
		t.Fatalf("session server username = %q, want OnlinePlayer", receivedName)
	}
	if want := protocol.ServerHash("", sharedSecret, publicKeyDER); receivedServerID != want {
		t.Fatalf("session server serverId = %q, want %q", receivedServerID, want)
	}

	// Login Acknowledged（加密 + 压缩帧）
	if err := protocol.WritePacketWithCompression(clientWriter, protocol.AppendVarInt(nil, 3), compressionThreshold); err != nil {
		t.Fatal(err)
	}
	// 配置阶段首个包应为 Brand，说明加密后流程继续正常工作。
	brand, err := protocol.ReadPacketWithCompression(clientReader, compressionThreshold)
	if err != nil {
		t.Fatal(err)
	}
	brandID, _, err := protocol.DecodeVarInt(brand)
	if err != nil || brandID != 0x01 {
		t.Fatalf("expected brand packet, got %#x (err=%v)", brandID, err)
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
}

// dialOnlineTestClient 启动在线模式测试服务器并建立连接（尚未握手）。
func dialOnlineTestClient(t *testing.T, cfg config.Config) net.Conn {
	t.Helper()
	instance, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serveResult := make(chan error, 1)
	go func() { serveResult <- instance.Serve(ctx, listener) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-serveResult:
			if err != nil {
				t.Errorf("Serve() after cancellation: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Serve() did not stop after context cancellation")
		}
	})
	conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return conn
}

// completeOnlineHandshake 完成登录开始 → 加密请求 → 加密响应的流程，
// 返回加密后的客户端读取器（可继续读取服务器发送的加密数据包）。
func completeOnlineHandshake(t *testing.T, conn net.Conn, name string, protocolVersion int32) *protocol.StreamReader {
	t.Helper()
	handshake := protocol.AppendVarInt(nil, 0)
	handshake = protocol.AppendVarInt(handshake, protocolVersion)
	handshake = appendTestString(handshake, "localhost")
	handshake = append(handshake, 0x63, 0xdd)
	handshake = protocol.AppendVarInt(handshake, 2)
	if err := protocol.WritePacket(conn, handshake); err != nil {
		t.Fatal(err)
	}
	loginStart := protocol.AppendVarInt(nil, 0)
	loginStart = appendTestString(loginStart, name)
	loginStart = append(loginStart, make([]byte, 16)...)
	if err := protocol.WritePacket(conn, loginStart); err != nil {
		t.Fatal(err)
	}

	encryptionRequest, err := protocol.ReadPacket(conn)
	if err != nil {
		t.Fatal(err)
	}
	requestID, offset, err := protocol.DecodeVarInt(encryptionRequest)
	if err != nil || requestID != 0x01 {
		t.Fatalf("expected encryption request, got %#x (err=%v)", requestID, err)
	}
	_, offset = readTestString(t, encryptionRequest, offset)
	publicKeyDER, offset := readTestByteArray(t, encryptionRequest, offset)
	verifyToken, _ := readTestByteArray(t, encryptionRequest, offset)

	publicKey, err := x509.ParsePKIXPublicKey(publicKeyDER)
	if err != nil {
		t.Fatal(err)
	}
	rsaPublicKey, ok := publicKey.(*rsa.PublicKey)
	if !ok {
		t.Fatal("expected RSA public key")
	}
	sharedSecret := make([]byte, 16)
	if _, err := rand.Read(sharedSecret); err != nil {
		t.Fatal(err)
	}
	encryptedSecret, err := rsa.EncryptPKCS1v15(rand.Reader, rsaPublicKey, sharedSecret)
	if err != nil {
		t.Fatal(err)
	}
	encryptedToken, err := rsa.EncryptPKCS1v15(rand.Reader, rsaPublicKey, verifyToken)
	if err != nil {
		t.Fatal(err)
	}
	response := protocol.AppendVarInt(nil, 0x01)
	response = protocol.AppendVarInt(response, int32(len(encryptedSecret)))
	response = append(response, encryptedSecret...)
	response = protocol.AppendVarInt(response, int32(len(encryptedToken)))
	response = append(response, encryptedToken...)
	if err := protocol.WritePacket(conn, response); err != nil {
		t.Fatal(err)
	}

	block, err := aes.NewCipher(sharedSecret)
	if err != nil {
		t.Fatal(err)
	}
	return protocol.NewStreamReader(conn, protocol.NewCFB8Decrypter(block, sharedSecret))
}

// expectEncryptedDisconnect 读取加密通道上的 Login Disconnect，返回文本原因。
func expectEncryptedDisconnect(t *testing.T, reader *protocol.StreamReader) string {
	t.Helper()
	disconnect, err := protocol.ReadPacket(reader)
	if err != nil {
		t.Fatal(err)
	}
	disconnectID, offset, err := protocol.DecodeVarInt(disconnect)
	if err != nil || disconnectID != 0x00 {
		t.Fatalf("expected login disconnect, got %#x (err=%v)", disconnectID, err)
	}
	// NBT 字符串组件：0x08 类型 + 大端长度 + UTF-8 内容
	if len(disconnect) <= offset || disconnect[offset] != 0x08 {
		t.Fatalf("expected NBT string tag, got % x", disconnect[offset:])
	}
	offset++
	if len(disconnect)-offset < 2 {
		t.Fatal("truncated login disconnect")
	}
	length := int(binary.BigEndian.Uint16(disconnect[offset:]))
	offset += 2
	if length <= 0 || offset+length > len(disconnect) {
		t.Fatalf("invalid NBT string length %d", length)
	}
	return string(disconnect[offset : offset+length])
}

// TestOnlineLoginRejected 验证会话服务器拒绝（204）时客户端收到登录断开包。
func TestOnlineLoginRejected(t *testing.T) {
	sessionServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer sessionServer.Close()

	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.OnlineMode = true
	cfg.SessionServerURL = sessionServer.URL
	conn := dialOnlineTestClient(t, cfg)
	reader := completeOnlineHandshake(t, conn, "OfflineGuy", cfg.ProtocolVersion)

	if text := expectEncryptedDisconnect(t, reader); text != "Failed to verify username!" {
		t.Fatalf("unexpected disconnect reason %q", text)
	}
}

// TestOnlineLoginVerificationServerError 验证会话服务器不可用时给出独立的断开提示。
func TestOnlineLoginVerificationServerError(t *testing.T) {
	sessionServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer sessionServer.Close()

	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.OnlineMode = true
	cfg.SessionServerURL = sessionServer.URL
	conn := dialOnlineTestClient(t, cfg)
	reader := completeOnlineHandshake(t, conn, "OfflineGuy", cfg.ProtocolVersion)

	if text := expectEncryptedDisconnect(t, reader); text != "无法连接会话服务器，请稍后重试" {
		t.Fatalf("unexpected disconnect reason %q", text)
	}
}
