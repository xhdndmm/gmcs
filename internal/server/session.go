package server

import (
	"bytes"
	"crypto/aes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"errors"
	"io"
	"log/slog"
	"math"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

const (
	// 各阶段的读超时：超时未收到任何数据即断开连接。
	loginTimeout         = 30 * time.Second
	configurationTimeout = 30 * time.Second
	playTimeout          = 30 * time.Second
	// 压缩阈值，与 Set Compression 发送的值一致。
	compressionThreshold = 256
	// 默认的 Keep Alive 发送间隔（测试中可调整）。
	defaultKeepAliveInterval = 10 * time.Second
)

// corePack 是本服务器参与 Known Packs 协商的 1.21.11 原版数据包。
// 协商成功后，同步注册表条目可以省略 NBT 内容，由客户端本地数据包提供。
var corePack = protocol.KnownPack{Namespace: "minecraft", ID: "core", Version: "1.21.11"}

// supportedProtocolVersion 是本服务器实现的 Minecraft 协议版本（1.21.11）。
// 其他版本的客户端会被拒绝，避免协议不匹配导致的错误行为。
const supportedProtocolVersion = 774

// session 承载一个已握手连接从登录到进入游戏的生命周期。
type session struct {
	server          *Server
	conn            net.Conn
	protocolVersion int32

	// reader/writer 是会话的读写通道；正版登录后包装为 AES/CFB8 加密流。
	reader io.Reader
	writer io.Writer

	writeMu sync.Mutex

	name       string
	uuid       [16]byte
	entityID   int32
	teleportID int32
	clientInfo protocol.ClientInformation

	// profileProperties 是会话服务器返回的玩家属性（在线模式）。
	profileProperties []protocol.GameProfileProperty

	// 区块流式加载状态（由会话 goroutine 串行访问）。
	sentChunks  map[world.ChunkPos]struct{}
	centerChunk world.ChunkPos

	// inventory 是玩家物品栏，由会话串行访问。
	inventory item.Inventory
	// selectedSlot 是当前选中的快捷栏槽位（0–8），由会话串行访问。
	selectedSlot int
	// chunkSendBuf 是区块流式发送复用的编码缓冲，由会话 goroutine 串行访问。
	chunkSendBuf []byte

	keepAliveMu      sync.Mutex
	pendingKeepAlive int64

	// chatIndex 是该玩家的聊天消息序号（从 0 递增）。
	chatIndex atomic.Int32

	// 玩家战斗与位置状态：由读循环与实体 Tick 共同访问，受 stateMu 保护。
	stateMu    sync.Mutex
	posX, posY float64
	posZ       float64
	yaw, pitch float32
	health     float32
	food       int32
	saturation float32
	dead       bool
	gameMode   uint8
	lastHurt   time.Time
	// regenTicks 是脱战回血的计数（每 playerRegenIntervalTicks 恢复 1 点）。
	regenTicks int

	// 下落跟踪（摔落伤害）：仅由会话读循环串行访问。
	fallStartY  float64
	airborne    bool
	wasOnGround bool

	// joined 在初始数据包全部发送后置位；在此之前生物不会索敌该玩家。
	joined atomic.Bool
}

// markJoined 标记玩家已完成进入世界的初始化。
func (s *session) markJoined() {
	s.joined.Store(true)
}

// isJoined 报告玩家的初始数据包是否已发送完毕。
func (s *session) isJoined() bool {
	return s.joined.Load()
}

func newSession(server *Server, conn net.Conn, protocolVersion int32) *session {
	return &session{
		server:          server,
		conn:            conn,
		protocolVersion: protocolVersion,
		reader:          conn,
		writer:          conn,
		health:          maxPlayerHealth,
		food:            maxPlayerFood,
		saturation:      playerSaturation,
		gameMode:        server.defaultGameMode,
		sentChunks:      make(map[world.ChunkPos]struct{}),
		wasOnGround:     true,
	}
}

// playerPosition 返回玩家的最新位置与朝向。
func (s *session) playerPosition() (x, y, z float64, yaw, pitch float32) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.posX, s.posY, s.posZ, s.yaw, s.pitch
}

// setPlayerPosition 更新位置与朝向（由移动数据包与传送路径调用）。
func (s *session) setPlayerPosition(x, y, z float64, yaw, pitch float32) {
	s.stateMu.Lock()
	s.posX, s.posY, s.posZ, s.yaw, s.pitch = x, y, z, yaw, pitch
	s.stateMu.Unlock()
}

// setPlayerRotation 更新朝向。
func (s *session) setPlayerRotation(yaw, pitch float32) {
	s.stateMu.Lock()
	s.yaw, s.pitch = yaw, pitch
	s.stateMu.Unlock()
}

// isDead 报告玩家是否处于死亡状态。
func (s *session) isDead() bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.dead
}

// canBeAttacked 报告玩家能否成为生物的攻击目标：已完成进入世界的初始化、
// 未死亡，且不是创造/旁观模式。
func (s *session) canBeAttacked() bool {
	if !s.isJoined() {
		return false
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return !s.dead && s.gameMode != uint8(config.GameModeCreative) && s.gameMode != uint8(config.GameModeSpectator)
}

// resyncPosition 把客户端拉回服务器记录的位置（拒绝越界或无效移动）。
// 仅在会话读循环中调用。
func (s *session) resyncPosition() {
	x, y, z, yaw, pitch := s.playerPosition()
	s.teleportID++
	s.resetFallState()
	packet := protocol.EncodeSynchronizePlayerPosition(s.teleportID, x, y, z, 0, 0, 0, yaw, pitch)
	if err := s.writePacket(packet); err != nil {
		_ = s.conn.Close()
		return
	}
	// 其他玩家看到的实体也要拉回同一位置。
	s.server.broadcastPlayerMove(s)
}

// fallDamageThreshold 是摔落伤害的下落阈值（原版：下落超过 3 格后
// 每多 1 格造成 1 点伤害）。
const fallDamageThreshold = 3.0

// updateFallState 根据移动包的着地标志跟踪下落并结算摔落伤害。
// 落点是水时不受伤害；创造/旁观模式由 applyDamage 直接忽略。
// 仅由会话读循环调用。
func (s *session) updateFallState(x, y, z float64, onGround bool) {
	switch {
	case !onGround:
		if s.wasOnGround {
			s.airborne = true
			s.fallStartY = y
		} else if s.airborne && y > s.fallStartY {
			s.fallStartY = y
		}
	case s.airborne:
		s.airborne = false
		fall := s.fallStartY - y
		if fall > fallDamageThreshold {
			landY := int(math.Floor(y))
			landed := s.server.world.BlockAt(int(math.Floor(x)), landY, int(math.Floor(z)))
			if landed != world.WaterBlock {
				damage := float32(math.Ceil(fall - fallDamageThreshold))
				s.server.damagePlayer(s, damage, "a fall", 0, s.server.fallDamageTypeID, nil)
			}
		}
	}
	s.wasOnGround = onGround
}

// resetFallState 清除下落跟踪（传送/重生/拉回后调用，避免误判摔落伤害）。
func (s *session) resetFallState() {
	s.airborne = false
	s.wasOnGround = true
}

// gameModeID 返回当前游戏模式。
func (s *session) gameModeID() uint8 {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.gameMode
}

// setGameMode 设置游戏模式。
func (s *session) setGameMode(mode uint8) {
	s.stateMu.Lock()
	s.gameMode = mode
	s.stateMu.Unlock()
}

// healthStatus 返回当前生命、饥饿度与饱食度。
func (s *session) healthStatus() (health float32, food int32, saturation float32) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.health, s.food, s.saturation
}

// applyDamage 扣减生命。处于死亡、无敌帧或创造/旁观模式时不生效。
// 返回扣减后的状态与是否生效、是否因此死亡。
func (s *session) applyDamage(amount float32, now time.Time) (health float32, food int32, saturation float32, applied, died bool) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.dead || s.gameMode == uint8(config.GameModeCreative) || s.gameMode == uint8(config.GameModeSpectator) {
		return s.health, s.food, s.saturation, false, false
	}
	if !s.lastHurt.IsZero() && now.Sub(s.lastHurt) < playerHurtCooldown {
		return s.health, s.food, s.saturation, false, false
	}
	s.lastHurt = now
	s.regenTicks = 0
	s.health -= amount
	if s.health <= 0 {
		s.health = 0
		s.dead = true
		return s.health, s.food, s.saturation, true, true
	}
	return s.health, s.food, s.saturation, true, false
}

// applyRegen 推进脱战回血：距离上次受伤超过 playerRegenDelay 后，
// 每 playerRegenIntervalTicks 恢复 1 点生命。返回是否发生恢复。
func (s *session) applyRegen(now time.Time) (health float32, food int32, saturation float32, healed bool) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.dead || s.health <= 0 || s.health >= maxPlayerHealth {
		return s.health, s.food, s.saturation, false
	}
	if !s.lastHurt.IsZero() && now.Sub(s.lastHurt) < playerRegenDelay {
		s.regenTicks = 0
		return s.health, s.food, s.saturation, false
	}
	s.regenTicks++
	if s.regenTicks < playerRegenIntervalTicks {
		return s.health, s.food, s.saturation, false
	}
	s.regenTicks = 0
	s.health++
	return s.health, s.food, s.saturation, true
}

// markRespawned 把死亡状态重置为满生命。返回 false 表示玩家未死亡。
func (s *session) markRespawned() bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if !s.dead {
		return false
	}
	s.dead = false
	s.health = maxPlayerHealth
	s.food = maxPlayerFood
	s.saturation = playerSaturation
	s.lastHurt = time.Time{}
	s.regenTicks = 0
	return true
}

func (s *session) run() {
	if !s.runLogin() {
		return
	}
	if !s.runConfiguration() {
		return
	}
	s.runPlay()
}

// writeRawPacket 发送未压缩数据包，仅用于 Set Compression 之前的阶段。
func (s *session) writeRawPacket(packet []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return protocol.WritePacket(s.writer, packet)
}

// writePacket 以启用的压缩发送单个数据包；可被多个 goroutine 并发调用。
func (s *session) writePacket(packet []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return protocol.WritePacketWithCompression(s.writer, packet, compressionThreshold)
}

func (s *session) writePackets(packets ...[]byte) error {
	for _, packet := range packets {
		if err := s.writePacket(packet); err != nil {
			return err
		}
	}
	return nil
}

// tryWrite 尽力发送一个数据包；失败时关闭连接（读循环会随之退出）。
func (s *session) tryWrite(packet []byte) {
	if err := s.writePacket(packet); err != nil {
		_ = s.conn.Close()
	}
}

// readRawPacket 读取未压缩数据包（仅登录起始包使用）。
func (s *session) readRawPacket(timeout time.Duration) ([]byte, error) {
	if err := s.conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}
	return protocol.ReadPacket(s.reader)
}

// readPacket 重设读超时并读取一个压缩格式的数据包。
func (s *session) readPacket(timeout time.Duration) ([]byte, error) {
	if err := s.conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}
	return protocol.ReadPacketWithCompression(s.reader, compressionThreshold)
}

// runLogin 执行登录阶段：Login Start → Set Compression → Login Success → Login Acknowledged。
func (s *session) runLogin() bool {
	if s.protocolVersion != supportedProtocolVersion {
		slog.Info("rejecting client with unsupported protocol version",
			"remote", s.conn.RemoteAddr(), "protocol", s.protocolVersion, "supported", supportedProtocolVersion)
		return false
	}
	packet, err := s.readRawPacket(loginTimeout)
	if err != nil {
		return false
	}
	loginStart, err := protocol.ParseLoginStart(packet)
	if err != nil {
		slog.Debug("rejecting malformed login start", "remote", s.conn.RemoteAddr(), "error", err)
		return false
	}
	if !validUsername(loginStart.Name) {
		slog.Debug("rejecting invalid username", "remote", s.conn.RemoteAddr(), "name", loginStart.Name)
		return false
	}
	s.name = loginStart.Name
	// 离线模式：忽略客户端上报的 UUID，按原版规则从用户名推导。
	s.uuid = protocol.OfflineUUID(s.name)
	if s.server.config.OnlineMode {
		if !s.runOnlineAuthentication() {
			return false
		}
	}
	s.entityID = s.server.entityIDs.Add(1)

	if err := s.writeRawPacket(protocol.EncodeSetCompression(compressionThreshold)); err != nil {
		return false
	}
	if err := s.writePacket(protocol.EncodeLoginSuccessWithProperties(s.uuid, s.name, s.profileProperties)); err != nil {
		return false
	}

	packet, err = s.readPacket(loginTimeout)
	if err != nil {
		return false
	}
	if err := protocol.ParseLoginAcknowledged(packet); err != nil {
		slog.Debug("expected login acknowledged", "name", s.name, "error", err)
		return false
	}
	slog.Info("player logged in", "name", s.name, "remote", s.conn.RemoteAddr())
	return true
}

// runOnlineAuthentication 执行正版登录：发起加密请求、接收并解密响应，
// 然后通过会话服务器验证玩家身份。成功后用会话服务器的权威数据
// 覆盖 s.uuid、s.name 与 s.profileProperties。
func (s *session) runOnlineAuthentication() bool {
	verifyToken := make([]byte, 4)
	if _, err := rand.Read(verifyToken); err != nil {
		slog.Error("failed to generate verify token", "error", err)
		return false
	}
	publicKey, err := x509.MarshalPKIXPublicKey(&s.server.rsaKey.PublicKey)
	if err != nil {
		slog.Error("failed to marshal public key", "error", err)
		return false
	}
	if err := s.writeRawPacket(protocol.EncodeEncryptionRequest("", publicKey, verifyToken, true)); err != nil {
		return false
	}

	packet, err := s.readRawPacket(loginTimeout)
	if err != nil {
		return false
	}
	encryptedSecret, encryptedToken, err := protocol.ParseEncryptionResponse(packet)
	if err != nil {
		slog.Debug("rejecting malformed encryption response", "remote", s.conn.RemoteAddr(), "error", err)
		return false
	}
	sharedSecret, err := rsa.DecryptPKCS1v15(nil, s.server.rsaKey, encryptedSecret)
	if err != nil || len(sharedSecret) != 16 {
		slog.Debug("failed to decrypt shared secret", "remote", s.conn.RemoteAddr(), "error", err)
		return false
	}
	token, err := rsa.DecryptPKCS1v15(nil, s.server.rsaKey, encryptedToken)
	if err != nil || !bytes.Equal(token, verifyToken) {
		slog.Debug("encryption verify token mismatch", "remote", s.conn.RemoteAddr())
		return false
	}

	block, err := aes.NewCipher(sharedSecret)
	if err != nil {
		slog.Debug("failed to create cipher", "remote", s.conn.RemoteAddr(), "error", err)
		return false
	}
	// 加密从此生效：后续读写都经 AES/CFB8（IV 即共享密钥）。
	s.reader = protocol.NewStreamReader(s.conn, protocol.NewCFB8Decrypter(block, sharedSecret))
	s.writer = protocol.NewStreamWriter(s.conn, protocol.NewCFB8Encrypter(block, sharedSecret))

	profile, err := s.server.verifySession(s.name, protocol.ServerHash("", sharedSecret, publicKey))
	if err != nil {
		if errors.Is(err, errSessionRejected) {
			slog.Info("player failed session verification", "name", s.name, "remote", s.conn.RemoteAddr())
			// 尽力在已加密的通道上告知客户端，失败不影响断开。
			_ = s.writeRawPacket(protocol.EncodeLoginDisconnect("Failed to verify username!"))
		} else {
			slog.Error("session verification failed", "name", s.name, "remote", s.conn.RemoteAddr(), "error", err)
			// 会话服务器不可用：给出与"账号验证失败"不同的提示，便于排查。
			_ = s.writeRawPacket(protocol.EncodeLoginDisconnect("无法连接会话服务器，请稍后重试"))
		}
		return false
	}
	s.uuid = profile.UUID
	s.name = profile.Name
	s.profileProperties = profile.Properties
	return true
}

// runConfiguration 执行配置阶段：发送 Brand、功能开关与 Known Packs，
// 协商成功后同步注册表条目并等待客户端确认。
func (s *session) runConfiguration() bool {
	if err := s.writePackets(
		protocol.EncodeBrand(s.server.config.VersionName),
		protocol.EncodeFeatureFlags([]string{"minecraft:vanilla"}),
		protocol.EncodeKnownPacks([]protocol.KnownPack{corePack}),
	); err != nil {
		return false
	}

	for {
		packet, err := s.readPacket(configurationTimeout)
		if err != nil {
			return false
		}
		packetID, _, err := protocol.DecodeVarInt(packet)
		if err != nil {
			return false
		}
		switch packetID {
		case protocol.ConfigServerboundPacketIDKnownPacks:
			packs, err := protocol.ParseKnownPacksResponse(packet)
			if err != nil {
				slog.Debug("malformed known packs response", "name", s.name, "error", err)
				return false
			}
			if !containsCorePack(packs) {
				slog.Info("client is missing the vanilla 1.21.11 data pack; cannot complete configuration", "name", s.name)
				return false
			}
			if err := s.sendRegistries(); err != nil {
				return false
			}
			if err := s.writePackets(
				protocol.EncodeUpdateTags(registryTags()),
				protocol.EncodeFinishConfiguration(),
			); err != nil {
				return false
			}
			return s.awaitConfigurationFinished()
		case protocol.ConfigPacketIDClientInformation:
			if info, err := protocol.ParseClientInformation(packet); err == nil {
				s.clientInfo = info
			}
		default:
			// 忽略其余配置阶段数据包（Keep Alive、Pong 等）。
		}
	}
}

// sendRegistries 发送全部同步注册表的完整条目列表。
// 客户端在配置阶段重建同步注册表：未发送的注册表会变为空（而不是回退内置数据），
// 因此即使 Known Packs 已提供条目定义，也必须逐个完整发送。
func (s *session) sendRegistries() error {
	for _, reg := range registry.Synchronized() {
		entries := make([]protocol.RegistryEntry, 0, len(reg.Entries))
		for _, name := range reg.Entries {
			entries = append(entries, protocol.RegistryEntry{Name: name})
		}
		if err := s.writePacket(protocol.EncodeRegistryData(reg.Name, entries)); err != nil {
			return err
		}
	}
	return nil
}

// registryTags 把全部需要发送的注册表标签转换为 Update Tags 包数据。
// 同步注册表与静态注册表（block、item 等）的标签都必须由服务器完整发送：
// 标签无法通过 Known Packs 获取，缺失会导致客户端解析本地数据失败。
func registryTags() []protocol.TagRegistry {
	synced := registry.AllTags()
	tags := make([]protocol.TagRegistry, 0, len(synced))
	for _, reg := range synced {
		entries := make([]protocol.TagEntry, 0, len(reg.Tags))
		for _, tag := range reg.Tags {
			entries = append(entries, protocol.TagEntry{Name: tag.Name, Entries: tag.Entries})
		}
		tags = append(tags, protocol.TagRegistry{Registry: reg.Name, Tags: entries})
	}
	return tags
}

// awaitConfigurationFinished 等待客户端的 Acknowledge Finish Configuration。
func (s *session) awaitConfigurationFinished() bool {
	for {
		packet, err := s.readPacket(configurationTimeout)
		if err != nil {
			return false
		}
		packetID, _, err := protocol.DecodeVarInt(packet)
		if err != nil {
			return false
		}
		switch packetID {
		case protocol.ConfigPacketIDFinishConfiguration:
			if err := protocol.ParseFinishConfigurationAck(packet); err != nil {
				return false
			}
			return true
		case protocol.ConfigPacketIDClientInformation:
			if info, err := protocol.ParseClientInformation(packet); err == nil {
				s.clientInfo = info
			}
		default:
			// 忽略其他数据包。
		}
	}
}

// runPlay 发送进入世界所需的初始数据包，注册到玩家列表，并进入游戏主循环。
func (s *session) runPlay() {
	s.teleportID = 1
	spawnX, spawnY, spawnZ := s.server.spawnPosition()
	// 恢复上次退出时的玩家数据（位置/生命/饥饿/游戏模式/物品栏）；
	// 没有记录时按新玩家处理（出生点 + 初始物品）。
	restored := false
	if record, ok := s.server.playerDataSnapshot(s.uuid); ok {
		s.applyPlayerRecord(record)
		restored = true
	} else {
		s.setPlayerPosition(spawnX, spawnY, spawnZ, 0, 0)
	}
	x, y, z, yaw, pitch := s.playerPosition()

	login := protocol.LoginPlayData{
		EntityID:            s.entityID,
		DimensionNames:      []string{"minecraft:overworld"},
		MaxPlayers:          int32(s.server.config.MaxPlayers),
		ViewDistance:        int32(s.server.config.ViewDistance),
		SimulationDistance:  int32(s.server.config.ViewDistance),
		EnableRespawnScreen: true,
		EnforcesSecureChat:  s.server.config.OnlineMode,
		Spawn:               s.server.spawnInfo(s.gameMode),
	}

	// 注册到玩家列表；离开时（任何返回路径）保存玩家数据并注销。
	others := s.server.registerPlayer(s)
	defer s.server.unregisterPlayer(s)
	defer s.server.savePlayerData(s)

	// 进入世界的前置包。
	packets := [][]byte{
		protocol.EncodeLoginPlay(login),
		protocol.EncodeSetDefaultSpawnPosition("minecraft:overworld",
			int(math.Floor(spawnX)), int(math.Floor(spawnY)), int(math.Floor(spawnZ)), 0, 0),
	}
	if s.server.borderHalfSize > 0 {
		// 世界边界（正方形，以 0,0 为中心；警告距离 5 格、警告时间 15 秒）。
		size := s.server.borderHalfSize * 2
		packets = append(packets, protocol.EncodeInitializeWorldBorder(0, 0, size, size, 0, 29999984, 5, 15))
	}
	centerX := int(math.Floor(x)) >> 4
	centerZ := int(math.Floor(z)) >> 4
	packets = append(packets,
		protocol.EncodeGameEvent(13, 0), // 开始等待区块
		protocol.EncodeSetCenterChunk(int32(centerX), int32(centerZ)),
	)
	if err := s.writePackets(packets...); err != nil {
		return
	}
	// 玩家周围视距内的全部区块（由近到远；跨区块移动时由 updateChunks 增量维护）。
	if err := s.syncChunks(centerX, centerZ); err != nil {
		slog.Error("failed to send spawn chunks", "name", s.name, "error", err)
		return
	}

	packets = [][]byte{
		protocol.EncodeSynchronizePlayerPosition(s.teleportID, x, y, z, 0, 0, 0, yaw, pitch),
		protocol.EncodePlayerInfoAddPlayer(s.uuid, s.name, s.profileProperties),
	}
	for _, other := range others {
		packets = append(packets, protocol.EncodePlayerInfoAddPlayer(other.uuid, other.name, other.profileProperties))
	}
	for _, other := range others {
		// 其他玩家已在世界中的实体（皮肤来自上面的玩家列表档案属性）。
		packets = append(packets, s.server.playerSpawnPacket(other))
	}
	packets = append(packets, protocol.EncodeSystemChat("Welcome to "+s.server.config.VersionName+"!"))
	// 向客户端声明命令树，使聊天栏支持 /help、/list、/say、/spawn、/gamemode。
	packets = append(packets, protocol.EncodeDeclareCommands(serverCommands(s.server.isOp(s.name))))
	if !restored {
		// 仅新玩家发放初始物品；恢复的玩家沿用其已保存的物品栏。
		packets = append(packets, s.giveStartingItems()...)
	}
	health, food, saturation := s.healthStatus()
	packets = append(packets, protocol.EncodeUpdateHealth(health, food, saturation))
	if err := s.writePackets(packets...); err != nil {
		return
	}
	// 世界中已有的生物（僵尸等）也要发送给新玩家。
	if err := s.server.sendExistingMobs(s); err != nil {
		return
	}
	s.markJoined()
	// 通知其他玩家：新玩家加入（档案属性 + 实体）。
	s.server.broadcastPacketExcluding(protocol.EncodePlayerInfoAddPlayer(s.uuid, s.name, s.profileProperties), s)
	s.server.writeToNearbyPlayers(s.server.playerSpawnPacket(s), x, z, s)
	s.server.broadcastPacketExcluding(protocol.EncodeSystemChat(s.name+" joined the game"), s)
	slog.Info("player joined the world", "name", s.name, "entityId", s.entityID)

	stop := make(chan struct{})
	var keepAliveWG sync.WaitGroup
	keepAliveWG.Add(1)
	go func() {
		defer keepAliveWG.Done()
		s.keepAliveLoop(stop)
	}()
	s.playReadLoop()
	close(stop)
	keepAliveWG.Wait()
	slog.Info("player disconnected", "name", s.name)
}

// giveStartingItems 把配置中的初始物品发放到快捷栏，返回同步给客户端的数据包。
// 支持 "minecraft:stone" 与 "minecraft:stone*64" 两种写法。
func (s *session) giveStartingItems() [][]byte {
	var packets [][]byte
	slot := item.SlotHotbarStart
	for _, entry := range s.server.config.StartingItems {
		if slot > item.SlotHotbarEnd {
			break
		}
		name, count := splitItemCount(entry)
		stack, err := item.FromName(name, count)
		if err != nil {
			slog.Warn("unknown starting item", "name", s.name, "item", entry, "error", err)
			continue
		}
		s.inventory.Set(slot, stack)
		packets = append(packets, protocol.EncodeSetPlayerInventory(int32(slot), stack.AppendSlot(nil)))
		slot++
	}
	return packets
}

// splitItemCount 解析初始物品配置："minecraft:stone"（默认 1 个）或
// "minecraft:stone*64"（显式数量，上限一栈 64）。
func splitItemCount(entry string) (string, int32) {
	name := strings.TrimSpace(entry)
	index := strings.LastIndex(name, "*")
	if index < 0 {
		return name, 1
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(name[index+1:]), 10, 32)
	if err != nil || parsed < 1 {
		return strings.TrimSpace(name[:index]), 1
	}
	if parsed > 64 {
		parsed = 64
	}
	return strings.TrimSpace(name[:index]), int32(parsed)
}

// keepAliveLoop 周期发送 Keep Alive；写失败时关闭连接以唤醒读循环。
func (s *session) keepAliveLoop(stop <-chan struct{}) {
	ticker := time.NewTicker(s.server.keepAliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			id := time.Now().UnixMilli()
			s.keepAliveMu.Lock()
			s.pendingKeepAlive = id
			s.keepAliveMu.Unlock()
			if err := s.writePacket(protocol.EncodeKeepAlivePlay(id)); err != nil {
				_ = s.conn.Close()
				return
			}
		}
	}
}

// playReadLoop 处理 Play 阶段的客户端数据包。
func (s *session) playReadLoop() {
	for {
		packet, err := s.readPacket(playTimeout)
		if err != nil {
			return
		}
		packetID, _, err := protocol.DecodeVarInt(packet)
		if err != nil {
			return
		}
		switch packetID {
		case protocol.PlayServerboundPacketIDKeepAlive:
			if id, err := protocol.ParsePlayKeepAlive(packet); err == nil {
				s.keepAliveMu.Lock()
				if s.pendingKeepAlive == id {
					s.pendingKeepAlive = 0
				}
				s.keepAliveMu.Unlock()
			}
		case protocol.PlayServerboundPacketIDChatMessage:
			message, err := protocol.ParseChatMessage(packet)
			if err != nil {
				slog.Debug("malformed chat message", "name", s.name, "error", err)
				continue
			}
			s.server.broadcastChat(s, message)
		case protocol.PlayServerboundPacketIDChatCommand, protocol.PlayServerboundPacketIDChatCommandSigned:
			command, err := protocol.ParseChatCommand(packet)
			if err != nil {
				slog.Debug("malformed chat command", "name", s.name, "error", err)
				continue
			}
			s.server.handleCommand(s, command)
		case protocol.PlayServerboundPacketIDTabComplete:
			transactionID, text, err := protocol.ParseTabCompleteRequest(packet)
			if err != nil {
				continue
			}
			s.server.handleTabComplete(s, transactionID, text)
		case protocol.PlayServerboundPacketIDConfirmTeleportation:
			if id, err := protocol.ParseConfirmTeleportation(packet); err == nil && id == s.teleportID {
				slog.Debug("player confirmed teleport", "name", s.name, "teleportId", id)
			}
		case protocol.PlayServerboundPacketIDInteract:
			targetID, action, err := protocol.ParseInteract(packet)
			if err != nil {
				slog.Debug("malformed interact packet", "name", s.name, "error", err)
				continue
			}
			if action == protocol.InteractActionAttack {
				s.server.handleAttack(s, targetID)
			}
		case protocol.PlayServerboundPacketIDClientCommand:
			if action, err := protocol.ParsePlayClientCommand(packet); err == nil && action == 0 {
				// 0 = 重生请求。
				s.server.respawnPlayer(s)
			}
		case protocol.PlayServerboundPacketIDPlayerPosition:
			x, y, z, onGround, err := protocol.ParsePlayerPosition(packet)
			if err != nil || !validPlayerY(y) {
				continue
			}
			if !s.server.insideBorder(x, z) {
				s.resyncPosition()
				continue
			}
			_, _, _, yaw, pitch := s.playerPosition()
			s.setPlayerPosition(x, y, z, yaw, pitch)
			s.updateFallState(x, y, z, onGround)
			s.server.broadcastPlayerMove(s)
			s.updateChunks(x, z)
		case protocol.PlayServerboundPacketIDPlayerPositionRotation:
			x, y, z, yaw, pitch, onGround, err := protocol.ParsePlayerPositionRotation(packet)
			if err != nil || !validPlayerY(y) {
				continue
			}
			if !s.server.insideBorder(x, z) {
				s.resyncPosition()
				continue
			}
			s.setPlayerPosition(x, y, z, yaw, pitch)
			s.updateFallState(x, y, z, onGround)
			s.server.broadcastPlayerMove(s)
			s.updateChunks(x, z)
		case protocol.PlayServerboundPacketIDPlayerRotation:
			yaw, pitch, err := protocol.ParsePlayerRotation(packet)
			if err != nil {
				continue
			}
			s.setPlayerRotation(yaw, pitch)
			s.server.broadcastPlayerMove(s)
		case protocol.PlayServerboundPacketIDPlayerAction:
			action, err := protocol.ParsePlayerAction(packet)
			if err != nil {
				continue
			}
			s.server.handlePlayerAction(s, action)
		case protocol.PlayServerboundPacketIDUseItemOn:
			use, err := protocol.ParseUseItemOn(packet)
			if err != nil {
				continue
			}
			s.server.handleUseItemOn(s, use)
		case protocol.PlayServerboundPacketIDSetCarriedItem:
			if slot, err := protocol.ParseSetCarriedItem(packet); err == nil {
				s.server.handleSetCarriedItem(s, slot)
			}
		case protocol.PlayServerboundPacketIDSetCreativeSlot:
			slot, itemID, count, err := protocol.ParseSetCreativeSlot(packet)
			if err != nil {
				continue
			}
			s.server.handleSetCreativeSlot(s, slot, itemID, count)
		case protocol.PlayServerboundPacketIDSwingArm:
			if err := protocol.ParseSwingArm(packet); err == nil {
				s.server.broadcastSwing(s)
			}
		case protocol.PlayServerboundPacketIDClientInformation:
			if info, err := protocol.ParseClientInformation(packet); err == nil {
				s.clientInfo = info
			}
		default:
			// 其他数据包（输入、快捷栏切换等）暂未实现，忽略。
		}
	}
}

// validPlayerY 拒绝世界范围之外的坐标（简单的服务端校验）。
func validPlayerY(y float64) bool {
	return y >= world.WorldMinY-16 && y <= world.WorldMinY+world.WorldHeight+16
}

// validUsername 校验离线模式玩家名：1-16 个字符，只允许字母、数字与下划线
// （与 1.21.11 服务端的用户名限制一致）。
func validUsername(name string) bool {
	if len(name) == 0 || len(name) > 16 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return true
}

// containsCorePack 检查客户端上报的 Known Packs 是否包含本服务器的原版数据包。
func containsCorePack(packs []protocol.KnownPack) bool {
	for _, pack := range packs {
		if pack.Namespace == corePack.Namespace && pack.ID == corePack.ID && pack.Version == corePack.Version {
			return true
		}
	}
	return false
}
