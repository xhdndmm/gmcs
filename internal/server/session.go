package server

import (
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

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

	writeMu sync.Mutex

	name       string
	uuid       [16]byte
	entityID   int32
	teleportID int32
	clientInfo protocol.ClientInformation

	// inventory 是玩家物品栏，由会话串行访问。
	inventory item.Inventory

	keepAliveMu      sync.Mutex
	pendingKeepAlive int64

	// chatIndex 是该玩家的聊天消息序号（从 0 递增）。
	chatIndex atomic.Int32
}

func newSession(server *Server, conn net.Conn, protocolVersion int32) *session {
	return &session{server: server, conn: conn, protocolVersion: protocolVersion}
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
	return protocol.WritePacket(s.conn, packet)
}

// writePacket 以启用的压缩发送单个数据包；可被多个 goroutine 并发调用。
func (s *session) writePacket(packet []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return protocol.WritePacketWithCompression(s.conn, packet, compressionThreshold)
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
	return protocol.ReadPacket(s.conn)
}

// readPacket 重设读超时并读取一个压缩格式的数据包。
func (s *session) readPacket(timeout time.Duration) ([]byte, error) {
	if err := s.conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}
	return protocol.ReadPacketWithCompression(s.conn, compressionThreshold)
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
	s.entityID = s.server.entityIDs.Add(1)

	if err := s.writeRawPacket(protocol.EncodeSetCompression(compressionThreshold)); err != nil {
		return false
	}
	if err := s.writePacket(protocol.EncodeLoginSuccess(s.uuid, s.name)); err != nil {
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
	spawnChunk, err := s.server.world.Chunk(0, 0)
	if err != nil {
		slog.Error("failed to load spawn chunk", "name", s.name, "error", err)
		return
	}
	spawnY := float64(world.FlatSpawnY)

	login := protocol.LoginPlayData{
		EntityID:            s.entityID,
		DimensionNames:      []string{"minecraft:overworld"},
		MaxPlayers:          int32(s.server.config.MaxPlayers),
		ViewDistance:        int32(s.server.config.ViewDistance),
		SimulationDistance:  int32(s.server.config.ViewDistance),
		EnableRespawnScreen: true,
		DimensionTypeID:     registry.DimensionTypeOverworldID, // 同步注册表中 minecraft:overworld 的 ID
		DimensionName:       "minecraft:overworld",
		GameMode:            1, // 创造模式
		SeaLevel:            63,
	}

	// 注册到玩家列表；离开时（任何返回路径）注销并通知其他玩家。
	others := s.server.registerPlayer(s)
	defer s.server.unregisterPlayer(s)

	packets := [][]byte{
		protocol.EncodeLoginPlay(login),
		protocol.EncodeSetDefaultSpawnPosition("minecraft:overworld", 0, world.FlatSpawnY, 0, 0, 0),
		protocol.EncodeGameEvent(13, 0), // 开始等待区块
		protocol.EncodeSetCenterChunk(0, 0),
		world.EncodeChunkDataPacket(spawnChunk),
		protocol.EncodeSynchronizePlayerPosition(s.teleportID, 0.5, spawnY, 0.5, 0, 0, 0, 0, 0),
		protocol.EncodePlayerInfoAddPlayer(s.uuid, s.name),
	}
	for _, other := range others {
		packets = append(packets, protocol.EncodePlayerInfoAddPlayer(other.uuid, other.name))
	}
	packets = append(packets, protocol.EncodeSystemChat("Welcome to "+s.server.config.VersionName+"!"))
	packets = append(packets, s.giveStartingItems()...)
	if err := s.writePackets(packets...); err != nil {
		return
	}
	// 通知其他玩家：新玩家加入。
	s.server.broadcastPacketExcluding(protocol.EncodePlayerInfoAddPlayer(s.uuid, s.name), s)
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
// 每个物品发放 1 个（数量配置暂不支持）。
func (s *session) giveStartingItems() [][]byte {
	var packets [][]byte
	slot := item.SlotHotbarStart
	for _, name := range s.server.config.StartingItems {
		if slot > item.SlotHotbarEnd {
			break
		}
		stack, err := item.FromName(name, 1)
		if err != nil {
			slog.Warn("unknown starting item", "name", s.name, "item", name, "error", err)
			continue
		}
		s.inventory.Set(slot, stack)
		packets = append(packets, protocol.EncodeSetPlayerInventory(int32(slot), stack.AppendSlot(nil)))
		slot++
	}
	return packets
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
		case protocol.PlayServerboundPacketIDConfirmTeleportation:
			if id, err := protocol.ParseConfirmTeleportation(packet); err == nil && id == s.teleportID {
				slog.Debug("player confirmed teleport", "name", s.name, "teleportId", id)
			}
		case protocol.PlayServerboundPacketIDClientInformation:
			if info, err := protocol.ParseClientInformation(packet); err == nil {
				s.clientInfo = info
			}
		default:
			// 位置、输入等数据包暂未实现，忽略。
		}
	}
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
