package server

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net"
	"testing"
	"time"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
)

func TestServerListPingAndGracefulShutdown(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.MOTD = "test server"
	cfg.VersionName = "test-version"
	cfg.ProtocolVersion = 765
	cfg.MaxPlayers = 12
	server, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serveResult := make(chan error, 1)
	go func() { serveResult <- server.Serve(ctx, listener) }()

	conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		cancel()
		t.Fatal(err)
	}

	handshake := protocol.AppendVarInt(nil, 0)
	handshake = protocol.AppendVarInt(handshake, 765)
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
	var status struct {
		Version struct {
			Name     string `json:"name"`
			Protocol int    `json:"protocol"`
		} `json:"version"`
		Players struct {
			Max    int `json:"max"`
			Online int `json:"online"`
		} `json:"players"`
		Description struct {
			Text string `json:"text"`
		} `json:"description"`
	}
	if err := json.Unmarshal([]byte(statusJSON), &status); err != nil {
		t.Fatal(err)
	}
	if status.Version.Name != cfg.VersionName || status.Version.Protocol != int(cfg.ProtocolVersion) || status.Players.Max != cfg.MaxPlayers || status.Description.Text != cfg.MOTD {
		t.Fatalf("unexpected server status: %+v", status)
	}

	ping := protocol.AppendVarInt(nil, 1)
	var payload [8]byte
	binary.BigEndian.PutUint64(payload[:], 0x1234567890abcdef)
	ping = append(ping, payload[:]...)
	if err := protocol.WritePacket(conn, ping); err != nil {
		t.Fatal(err)
	}
	pong, err := protocol.ReadPacket(conn)
	if err != nil {
		t.Fatal(err)
	}
	pongID, pongIDSize, err := protocol.DecodeVarInt(pong)
	if err != nil || pongID != 1 || len(pong)-pongIDSize != len(payload) || binary.BigEndian.Uint64(pong[pongIDSize:]) != binary.BigEndian.Uint64(payload[:]) {
		t.Fatalf("unexpected pong packet: %v (decode error: %v)", pong, err)
	}

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

func TestServerClosesActiveConnectionOnCancellation(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	server, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serveResult := make(chan error, 1)
	go func() { serveResult <- server.Serve(ctx, listener) }()

	conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		cancel()
		t.Fatal(err)
	}
	handshake := protocol.AppendVarInt(nil, 0)
	handshake = protocol.AppendVarInt(handshake, 765)
	handshake = protocol.AppendVarInt(handshake, 0)
	handshake = append(handshake, 0, 0)
	handshake = protocol.AppendVarInt(handshake, 1)
	if err := protocol.WritePacket(conn, handshake); err != nil {
		cancel()
		t.Fatal(err)
	}

	cancel()
	select {
	case err := <-serveResult:
		if err != nil {
			t.Fatalf("Serve() after cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve() did not stop after context cancellation")
	}
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("active connection remained open after server shutdown")
	}
}
