package server

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
	"gmcs/internal/world"
)

const clientTimeout = 5 * time.Second

type Server struct {
	config  config.Config
	world   *world.World
	clients chan struct{}

	mu      sync.Mutex
	conns   map[net.Conn]struct{}
	players map[[16]byte]*session
	wg      sync.WaitGroup
	closing bool

	entityIDs         atomic.Int32
	chatIndex         atomic.Int32
	keepAliveInterval time.Duration

	// rsaKey 用于正版登录的加密握手（仅在线模式生成）。
	rsaKey *rsa.PrivateKey
	// httpClient 用于访问会话服务器。
	httpClient *http.Client
}

func New(cfg config.Config) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	gameWorld, err := world.Open(cfg.WorldDir, world.FlatGenerator{})
	if err != nil {
		return nil, err
	}
	// 在线模式：为加密握手生成服务器密钥对（1024 位，与客户端兼容）。
	var rsaKey *rsa.PrivateKey
	if cfg.OnlineMode {
		key, err := rsa.GenerateKey(rand.Reader, 1024)
		if err != nil {
			return nil, fmt.Errorf("生成 RSA 密钥：%w", err)
		}
		rsaKey = key
	}
	return &Server{
		config:            cfg,
		world:             gameWorld,
		clients:           make(chan struct{}, cfg.MaxConnections),
		conns:             make(map[net.Conn]struct{}),
		players:           make(map[[16]byte]*session),
		keepAliveInterval: defaultKeepAliveInterval,
		rsaKey:            rsaKey,
		httpClient:        &http.Client{Timeout: 10 * time.Second},
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

	// 自动保存循环：ctx 取消（服务器关闭）时退出；最终的保存由 Serve 结尾执行。
	if interval := s.config.AutosaveSeconds; interval > 0 {
		go s.world.Autosave(ctx, time.Duration(interval)*time.Second)
	}

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
	// 全部会话结束：保存世界。
	if err := s.world.Close(); err != nil {
		slog.Error("failed to save the world", "error", err)
		if serveErr == nil {
			serveErr = err
		}
	}
	return serveErr
}

// registerPlayer 把玩家加入在线列表，返回当前在线的其他玩家。
func (s *Server) registerPlayer(player *session) []*session {
	s.mu.Lock()
	defer s.mu.Unlock()
	others := make([]*session, 0, len(s.players))
	for _, other := range s.players {
		others = append(others, other)
	}
	s.players[player.uuid] = player
	return others
}

// unregisterPlayer 从在线列表移除玩家，并通知其他玩家。
func (s *Server) unregisterPlayer(player *session) {
	s.mu.Lock()
	delete(s.players, player.uuid)
	s.mu.Unlock()

	s.broadcastPacketExcluding(protocol.EncodePlayerInfoRemove([][16]byte{player.uuid}), player)
	s.broadcastPacketExcluding(protocol.EncodeSystemChat(player.name+" left the game"), player)
}

// broadcastPacket 向所有在线玩家发送数据包（忽略单个玩家的写失败）。
func (s *Server) broadcastPacket(packet []byte) {
	s.broadcastPacketExcluding(packet, nil)
}

// broadcastPacketExcluding 向除 except 外的在线玩家发送数据包。
func (s *Server) broadcastPacketExcluding(packet []byte, except *session) {
	s.mu.Lock()
	targets := make([]*session, 0, len(s.players))
	for _, player := range s.players {
		if player != except {
			targets = append(targets, player)
		}
	}
	s.mu.Unlock()
	for _, player := range targets {
		player.tryWrite(packet)
	}
}

// broadcastChat 广播一条玩家聊天消息。
// 离线模式下消息未签名，客户端显示为“不安全”消息（与原版离线服务器一致）。
func (s *Server) broadcastChat(sender *session, message string) {
	chat := protocol.PlayerChatData{
		GlobalIndex: s.chatIndex.Add(1) - 1,
		SenderUUID:  sender.uuid,
		SenderIndex: sender.chatIndex.Add(1) - 1,
		Message:     message,
		Timestamp:   time.Now().UnixMilli(),
		DisplayName: sender.name,
	}
	s.broadcastPacket(protocol.EncodePlayerChatMessage(chat))
	slog.Info("chat", "name", sender.name, "message", message)
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
