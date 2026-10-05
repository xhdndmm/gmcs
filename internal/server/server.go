package server

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

const clientTimeout = 5 * time.Second

// defaultTickInterval 是实体 Tick 的默认间隔（20 TPS）。
const defaultTickInterval = 50 * time.Millisecond

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

	// 实体状态：mobs、items 与各自的字段受 entityMu 保护。
	entityMu   sync.Mutex
	mobs       map[int32]*mob
	items      map[int32]*itemEntity
	spawnTicks int
	randState  atomic.Uint64

	// tickInterval 是实体 Tick 间隔；<= 0 时禁用后台 Tick（测试手动驱动）。
	tickInterval time.Duration

	// chunkUnloadInterval 是区块内存卸载扫描的周期；<= 0 时禁用后台周期卸载
	// （测试通过显式调用 unloadFarChunks 驱动，保证断言不被周期卸载干扰）。
	chunkUnloadInterval time.Duration

	// playerAutosaveInterval 是玩家数据的周期保存间隔；<= 0 时禁用
	// （测试可显式调用 saveAllPlayerData 驱动）。
	playerAutosaveInterval time.Duration

	// 出生点（世界坐标，脚部位置）。
	spawnX, spawnY, spawnZ float64
	// borderHalfSize 是世界边界的半边长（方块）；0 表示未启用边界。
	borderHalfSize float64
	// defaultGameMode 是新玩家的游戏模式。
	defaultGameMode uint8
	// mobsEnabled 为 false 时禁用全部生物逻辑（注册表数据缺失时）。
	mobsEnabled bool
	// 玩家实体同步（注册表缺少玩家实体类型时禁用）。
	playerTypeID          int32
	playerEntitiesEnabled bool
	// 生物/伤害系统使用的注册表 ID。
	zombieTypeID             int32
	mobAttackDamageTypeID    int32
	playerAttackDamageTypeID int32
	fallDamageTypeID         int32
	soundMobHurt             int32
	soundMobDeath            int32
	soundPlayerHurt          int32

	// 掉落物系统（注册表缺少物品实体类型或拾取音效时禁用）。
	itemTypeID      int32
	soundItemPickup int32
	itemsEnabled    bool

	// 容器系统：containerDefs 是方块名 → 容器定义（注册表数据缺失时为空）；
	// containers 是已打开容器（按方块坐标索引），受 containerMu 保护。
	containerDefs map[string]*containerDef
	containerMu   sync.Mutex
	containers    map[[3]int]*containerState

	// fallDamageMultipliers 是落点方块的摔落伤害倍率（New 中解析）。
	fallDamageMultipliers map[uint16]float32

	// blockNames 是方块状态 ID → 命名空间名的反查表（New 中构建），
	// 用于破坏方块时的掉落判定。
	blockNames map[uint16]string

	// rsaKey 用于正版登录的加密握手（仅在线模式生成）。
	rsaKey *rsa.PrivateKey
	// httpClient 用于访问会话服务器。
	httpClient *http.Client

	// 玩家数据（players.json）：受 playerDataMu 保护，按 UUID 索引。
	playerDataMu sync.Mutex
	playerData   map[[16]byte]playerRecord
}

func New(cfg config.Config) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	gameMode, ok := config.GameModeID(cfg.GameMode)
	if !ok {
		return nil, fmt.Errorf("未知游戏模式 %q", cfg.GameMode)
	}
	gameWorld, err := world.Open(cfg.WorldDir, world.SeededGenerator{Seed: cfg.WorldSeed})
	if err != nil {
		return nil, err
	}
	// 出生点：优先使用世界实际地形（已有存档可能与当前种子不同），
	// 无法确定时回退到生成器的高度。
	spawnY, found := gameWorld.GroundY(0, 0)
	if !found {
		spawnY = float64(gameWorld.SurfaceY(0, 0) + 1)
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
	server := &Server{
		config:                 cfg,
		world:                  gameWorld,
		clients:                make(chan struct{}, cfg.MaxConnections),
		conns:                  make(map[net.Conn]struct{}),
		players:                make(map[[16]byte]*session),
		keepAliveInterval:      defaultKeepAliveInterval,
		mobs:                   make(map[int32]*mob),
		items:                  make(map[int32]*itemEntity),
		tickInterval:           defaultTickInterval,
		chunkUnloadInterval:    defaultChunkUnloadInterval,
		playerAutosaveInterval: defaultPlayerAutosaveInterval,
		spawnX:                 0.5,
		spawnY:                 spawnY,
		spawnZ:                 0.5,
		borderHalfSize:         float64(cfg.WorldBorderSize) / 2,
		defaultGameMode:        uint8(gameMode),
		rsaKey:                 rsaKey,
		httpClient:             &http.Client{Timeout: 10 * time.Second},
		playerData:             make(map[[16]byte]playerRecord),
		containers:             make(map[[3]int]*containerState),
	}
	server.resolveMobRegistryIDs()
	server.resolvePlayerEntityType()
	server.resolveItemRegistryIDs()
	server.resolveFallDamageBlocks()
	server.resolveContainerDefs()
	server.blockNames = resolveBlockNames()
	if len(server.containerDefs) == 0 {
		slog.Warn("容器交互已禁用：注册表数据缺失")
	}
	if !server.mobsEnabled {
		slog.Warn("生物系统已禁用：注册表数据缺失")
	}
	if !server.itemsEnabled {
		slog.Warn("掉落物系统已禁用：注册表数据缺失")
	}
	// 恢复上次保存的生物与掉落物（entities.json；不存在时不做任何事）。
	if server.mobsEnabled || server.itemsEnabled {
		if err := server.loadEntities(); err != nil {
			slog.Error("加载实体数据失败", "error", err)
		}
	}
	// 恢复已保存的玩家数据（players.json；不存在时按新玩家处理）。
	if err := server.loadPlayerData(); err != nil {
		slog.Error("加载玩家数据失败", "error", err)
	}
	return server, nil
}

// spawnInfo 构造带出生点信息的世界状态（Login 与 Respawn 共用）。
func (s *Server) spawnInfo(gameMode uint8) protocol.SpawnInfo {
	return protocol.SpawnInfo{
		DimensionTypeID:  registry.DimensionTypeOverworldID,
		DimensionName:    "minecraft:overworld",
		HashedSeed:       s.config.WorldSeed,
		GameMode:         gameMode,
		PreviousGameMode: 0xFF, // 未定义
		SeaLevel:         world.SeaLevel,
	}
}

// spawnPosition 返回出生点（脚部）的世界坐标。
func (s *Server) spawnPosition() (x, y, z float64) {
	return s.spawnX, s.spawnY, s.spawnZ
}

// insideBorder 报告坐标是否位于世界边界内（未启用边界时恒为 true）。
func (s *Server) insideBorder(x, z float64) bool {
	if s.borderHalfSize <= 0 {
		return true
	}
	return math.Abs(x) <= s.borderHalfSize && math.Abs(z) <= s.borderHalfSize
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

	// 实体 Tick 循环：推进生物 AI 并提供确定性测试入口（tickInterval <= 0 时禁用）。
	go s.tickLoop(ctx)

	// 实体数据周期保存：ctx 取消（服务器关闭）时退出。
	if s.mobsEnabled || s.itemsEnabled {
		go s.entityAutosaveLoop(ctx)
	}

	// 玩家数据周期保存：ctx 取消（服务器关闭）时退出。
	if s.playerAutosaveInterval > 0 {
		go s.playerAutosaveLoop(ctx)
	}

	// 区块卸载循环：周期性把远离所有玩家的区块移出内存缓存，
	// 避免长时间跑图导致内存占用持续增长。
	go s.chunkUnloadLoop(ctx)

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
	// 保存实体数据（生物与掉落物；与世界的保存相互独立）。
	if s.mobsEnabled || s.itemsEnabled {
		if err := s.saveEntities(); err != nil {
			slog.Error("failed to save entities", "error", err)
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
// 仅当列表中的会话仍是本会话时才删除（避免快速重连时旧会话
// 误删新会话的在线记录）。
func (s *Server) unregisterPlayer(player *session) {
	s.mu.Lock()
	if current, ok := s.players[player.uuid]; ok && current == player {
		delete(s.players, player.uuid)
	}
	s.mu.Unlock()

	s.broadcastPlayerRemove(player)
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

	s.mu.Lock()
	online := len(s.players)
	s.mu.Unlock()

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
		// EnforcesSecureChat = true 时客户端把服务器标记为“强制安全档案”
		// （去掉服务器列表的“未验证”警告）；正版模式为 true。
		EnforcesSecureChat bool `json:"enforcesSecureChat"`
		PreviewsChat       bool `json:"previewsChat"`
	}{
		Version: struct {
			Name     string `json:"name"`
			Protocol int32  `json:"protocol"`
		}{Name: s.config.VersionName, Protocol: s.config.ProtocolVersion},
		Players: struct {
			Max    int `json:"max"`
			Online int `json:"online"`
		}{Max: s.config.MaxPlayers, Online: online},
		Description: struct {
			Text string `json:"text"`
		}{Text: s.config.MOTD},
		EnforcesSecureChat: s.config.OnlineMode,
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
