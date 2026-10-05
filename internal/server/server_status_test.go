package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
)

// fetchStatusJSON 向服务器发送一次 Server List Ping 并返回状态 JSON。
func fetchStatusJSON(t *testing.T, addr string, protocolVersion int32) string {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	handshake := protocol.AppendVarInt(nil, 0)
	handshake = protocol.AppendVarInt(handshake, protocolVersion)
	handshake = protocol.AppendVarInt(handshake, int32(len("localhost")))
	handshake = append(handshake, "localhost"...)
	handshake = append(handshake, 0x63, 0xdd)
	handshake = protocol.AppendVarInt(handshake, 1)
	if err := protocol.WritePacket(conn, handshake); err != nil {
		t.Fatal(err)
	}
	if err := protocol.WritePacket(conn, []byte{0}); err != nil {
		t.Fatal(err)
	}
	statusPacket, err := protocol.ReadPacket(conn)
	if err != nil {
		t.Fatal(err)
	}
	statusJSON, err := DecodeStatusResponse(statusPacket)
	if err != nil {
		t.Fatal(err)
	}
	return statusJSON
}

// TestServerListPingSecureChatFlag 验证 online_mode 开启时状态响应会标记
// enforcesSecureChat=true（客户端据此不再显示“服务器未验证”警告）；
// 离线模式保持 false。
func TestServerListPingSecureChatFlag(t *testing.T) {
	for _, onlineMode := range []bool{false, true} {
		t.Run(fmt.Sprintf("online=%v", onlineMode), func(t *testing.T) {
			cfg := config.Default()
			cfg.WorldDir = t.TempDir()
			cfg.OnlineMode = onlineMode
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
			statusJSON := fetchStatusJSON(t, listener.Addr().String(), cfg.ProtocolVersion)
			cancel()
			if err := <-serveResult; err != nil {
				t.Fatalf("Serve() after cancellation: %v", err)
			}
			var status struct {
				EnforcesSecureChat bool `json:"enforcesSecureChat"`
			}
			if err := json.Unmarshal([]byte(statusJSON), &status); err != nil {
				t.Fatal(err)
			}
			if status.EnforcesSecureChat != onlineMode {
				t.Fatalf("online=%v 时 enforcesSecureChat=%v", onlineMode, status.EnforcesSecureChat)
			}
		})
	}
}
