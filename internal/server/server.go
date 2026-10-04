package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
)

const clientTimeout = 5 * time.Second

type Server struct {
	config  config.Config
	clients chan struct{}

	mu      sync.Mutex
	conns   map[net.Conn]struct{}
	wg      sync.WaitGroup
	closing bool

	entityIDs         atomic.Int32
	keepAliveInterval time.Duration
}

func New(cfg config.Config) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Server{
		config:            cfg,
		clients:           make(chan struct{}, cfg.MaxConnections),
		conns:             make(map[net.Conn]struct{}),
		keepAliveInterval: defaultKeepAliveInterval,
	}, nil
}

func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	var shutdownOnce sync.Once
	shutdownDone := make(chan struct{})
	shutdown := func() {
		shutdownOnce.Do(func() {
			_ = listener.Close()
			s.mu.Lock()
			s.closing = true
			for conn := range s.conns {
				_ = conn.Close()
			}
			s.mu.Unlock()
			close(shutdownDone)
		})
	}
	stopShutdown := context.AfterFunc(ctx, shutdown)

	var serveErr error
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() == nil && !errors.Is(err, net.ErrClosed) {
				serveErr = err
			}
			break
		}
		if ctx.Err() != nil {
			_ = conn.Close()
			break
		}
		select {
		case s.clients <- struct{}{}:
			s.mu.Lock()
			if s.closing {
				s.mu.Unlock()
				<-s.clients
				_ = conn.Close()
				break
			}
			s.conns[conn] = struct{}{}
			s.wg.Add(1)
			s.mu.Unlock()
			go s.serveClient(conn)
		default:
			_ = conn.Close()
		}
	}

	shutdown()
	if !stopShutdown() {
		<-shutdownDone
	}
	<-shutdownDone
	s.wg.Wait()
	return serveErr
}

func (s *Server) serveClient(conn net.Conn) {
	defer s.wg.Done()
	defer func() {
		_ = conn.Close()
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
		<-s.clients
	}()

	_ = conn.SetReadDeadline(time.Now().Add(clientTimeout))
	handshakePacket, err := protocol.ReadPacket(conn)
	if err != nil {
		return
	}
	handshake, err := protocol.ParseHandshake(handshakePacket)
	if err != nil {
		return
	}

	switch handshake.NextState {
	case 1:
		s.handleStatus(conn)
	case 2:
		newSession(s, conn, handshake.ProtocolVersion).run()
	default:
		return
	}
}

func (s *Server) handleStatus(conn net.Conn) {
	request, err := protocol.ReadPacket(conn)
	if err != nil {
		return
	}
	requestID, requestSize, err := protocol.DecodeVarInt(request)
	if err != nil || requestID != 0 || requestSize != len(request) {
		return
	}

	status, err := json.Marshal(struct {
		Version struct {
			Name     string `json:"name"`
			Protocol int32  `json:"protocol"`
		} `json:"version"`
		Players struct {
			Max    int `json:"max"`
			Online int `json:"online"`
		} `json:"players"`
		Description struct {
			Text string `json:"text"`
		} `json:"description"`
	}{
		Version: struct {
			Name     string `json:"name"`
			Protocol int32  `json:"protocol"`
		}{Name: s.config.VersionName, Protocol: s.config.ProtocolVersion},
		Players: struct {
			Max    int `json:"max"`
			Online int `json:"online"`
		}{Max: s.config.MaxPlayers},
		Description: struct {
			Text string `json:"text"`
		}{Text: s.config.MOTD},
	})
	if err != nil {
		return
	}
	response := protocol.AppendVarInt(nil, 0)
	response = protocol.AppendVarInt(response, int32(len(status)))
	response = append(response, status...)
	if err := protocol.WritePacket(conn, response); err != nil {
		return
	}

	ping, err := protocol.ReadPacket(conn)
	if err != nil {
		return
	}
	pingID, pingIDSize, err := protocol.DecodeVarInt(ping)
	if err != nil || pingID != 1 || len(ping)-pingIDSize != 8 {
		return
	}
	pong := protocol.AppendVarInt(nil, 1)
	pong = append(pong, ping[pingIDSize:]...)
	_ = protocol.WritePacket(conn, pong)
}

func DecodeStatusResponse(packet []byte) (string, error) {
	packetID, offset, err := protocol.DecodeVarInt(packet)
	if err != nil || packetID != 0 {
		return "", fmt.Errorf("invalid status response packet ID")
	}
	length, offset, err := decodeLength(packet, offset)
	if err != nil || length < 0 || int(length) != len(packet)-offset {
		return "", fmt.Errorf("invalid status response string length")
	}
	return string(packet[offset:]), nil
}

func decodeLength(packet []byte, offset int) (int32, int, error) {
	length, size, err := protocol.DecodeVarInt(packet[offset:])
	if err != nil {
		return 0, offset, err
	}
	return length, offset + size, nil
}
