package protocol

import (
	"fmt"

	"gmcs/internal/registry"
)

// 1.21.11（协议 774）Play 阶段的包 ID（仅列出当前实现使用的部分）。
const (
	PlayPacketIDDisconnect           = 0x20 // clientbound
	PlayPacketIDGameEvent            = 0x26 // clientbound
	PlayPacketIDKeepAlive            = 0x2B // clientbound
	PlayPacketIDChunkData            = 0x2C // clientbound
	PlayPacketIDLogin                = 0x30 // clientbound
	PlayPacketIDPlayerChat           = 0x3F // clientbound
	PlayPacketIDPlayerInfoRemove     = 0x43 // clientbound
	PlayPacketIDPlayerInfoUpdate     = 0x44 // clientbound
	PlayPacketIDSynchronizePlayerPos = 0x46 // clientbound
	// PlayPacketIDForgetLevelChunk 是 Forget Level Chunk（卸载客户端区块缓存）。
	PlayPacketIDForgetLevelChunk   = 0x25 // clientbound
	PlayPacketIDSetCenterChunk     = 0x5C // clientbound
	PlayPacketIDSetDefaultSpawn    = 0x5F // clientbound
	PlayPacketIDSetPlayerInventory = 0x6A // clientbound
	PlayPacketIDSystemChat         = 0x77 // clientbound

	PlayServerboundPacketIDConfirmTeleportation   = 0x00
	PlayServerboundPacketIDChatMessage            = 0x08
	PlayServerboundPacketIDClientCommand          = 0x0B
	PlayServerboundPacketIDClientTickEnd          = 0x0C
	PlayServerboundPacketIDClientInformation      = 0x0D
	PlayServerboundPacketIDKeepAlive              = 0x1B
	PlayServerboundPacketIDPlayerPosition         = 0x1D
	PlayServerboundPacketIDPlayerPositionRotation = 0x1E
	PlayServerboundPacketIDPlayerRotation         = 0x1F
	PlayServerboundPacketIDPlayerMovementFlags    = 0x20
	PlayServerboundPacketIDPlayerInput            = 0x2A
	PlayServerboundPacketIDPlayerLoaded           = 0x2B
)

// LoginPlayData 是 Login (play) 数据包的内容。
type LoginPlayData struct {
	EntityID            int32
	Hardcore            bool
	DimensionNames      []string
	MaxPlayers          int32
	ViewDistance        int32
	SimulationDistance  int32
	ReducedDebugInfo    bool
	EnableRespawnScreen bool
	LimitedCrafting     bool
	Spawn               SpawnInfo
	EnforcesSecureChat  bool
}

// SpawnInfo 是 Login 与 Respawn 包共用的世界状态（SpawnInfo）。
type SpawnInfo struct {
	DimensionTypeID  int32
	DimensionName    string
	HashedSeed       int64
	GameMode         uint8
	PreviousGameMode uint8 // 0xFF 表示未定义
	Debug            bool
	Flat             bool
	HasDeathLocation bool
	DeathDimension   string
	DeathPosition    [3]int
	PortalCooldown   int32
	SeaLevel         int32
}

// appendSpawnInfo 追加 SpawnInfo 字段。
func appendSpawnInfo(packet []byte, spawn SpawnInfo) []byte {
	packet = AppendVarInt(packet, spawn.DimensionTypeID)
	packet = appendString(packet, spawn.DimensionName)
	packet = AppendInt64(packet, spawn.HashedSeed)
	packet = append(packet, spawn.GameMode)
	packet = append(packet, spawn.PreviousGameMode)
	packet = AppendBool(packet, spawn.Debug)
	packet = AppendBool(packet, spawn.Flat)
	packet = AppendBool(packet, spawn.HasDeathLocation)
	if spawn.HasDeathLocation {
		packet = appendString(packet, spawn.DeathDimension)
		packet = AppendInt64(packet, PackPosition(spawn.DeathPosition[0], spawn.DeathPosition[1], spawn.DeathPosition[2]))
	}
	packet = AppendVarInt(packet, spawn.PortalCooldown)
	return AppendVarInt(packet, spawn.SeaLevel)
}

// EncodeLoginPlay 编码 Login (play) 包。
func EncodeLoginPlay(play LoginPlayData) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDLogin))
	packet = AppendInt32(packet, play.EntityID)
	packet = AppendBool(packet, play.Hardcore)
	packet = AppendVarInt(packet, int32(len(play.DimensionNames)))
	for _, name := range play.DimensionNames {
		packet = appendString(packet, name)
	}
	packet = AppendVarInt(packet, play.MaxPlayers)
	packet = AppendVarInt(packet, play.ViewDistance)
	packet = AppendVarInt(packet, play.SimulationDistance)
	packet = AppendBool(packet, play.ReducedDebugInfo)
	packet = AppendBool(packet, play.EnableRespawnScreen)
	packet = AppendBool(packet, play.LimitedCrafting)
	packet = appendSpawnInfo(packet, play.Spawn)
	packet = AppendBool(packet, play.EnforcesSecureChat)
	return packet
}

// EncodeGameEvent 编码 Game Event 包（13 = 开始等待区块）。
func EncodeGameEvent(event uint8, value float32) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDGameEvent))
	packet = append(packet, event)
	return AppendFloat32(packet, value)
}

// PackPosition 将方块坐标打包为协议的 Position 类型（x 26 位、z 26 位、y 12 位）。
func PackPosition(x, y, z int) int64 {
	return int64(x&0x3FFFFFF)<<38 | int64(z&0x3FFFFFF)<<12 | int64(y&0xFFF)
}

// EncodeSetDefaultSpawnPosition 编码 Set Default Spawn Position 包。
func EncodeSetDefaultSpawnPosition(dimension string, x, y, z int, yaw, pitch float32) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDSetDefaultSpawn))
	packet = appendString(packet, dimension)
	packet = AppendInt64(packet, PackPosition(x, y, z))
	packet = AppendFloat32(packet, yaw)
	return AppendFloat32(packet, pitch)
}

// EncodeSynchronizePlayerPosition 编码 Synchronize Player Position 包。
// 所有轴均为绝对坐标，flags 为 0。
func EncodeSynchronizePlayerPosition(teleportID int32, x, y, z, dx, dy, dz float64, yaw, pitch float32) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDSynchronizePlayerPos))
	packet = AppendVarInt(packet, teleportID)
	packet = AppendFloat64(packet, x)
	packet = AppendFloat64(packet, y)
	packet = AppendFloat64(packet, z)
	packet = AppendFloat64(packet, dx)
	packet = AppendFloat64(packet, dy)
	packet = AppendFloat64(packet, dz)
	packet = AppendFloat32(packet, yaw)
	packet = AppendFloat32(packet, pitch)
	return AppendInt32(packet, 0) // Teleport Flags
}

// EncodeSetCenterChunk 编码 Set Center Chunk（区块缓存中心）包。
func EncodeSetCenterChunk(x, z int32) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDSetCenterChunk))
	packet = AppendVarInt(packet, x)
	return AppendVarInt(packet, z)
}

// EncodeForgetLevelChunk 编码 Forget Level Chunk 包（卸载客户端区块缓存）。
// 注意字段顺序为 Z、X（与 Set Center Chunk 的 X、Z 相反）。
func EncodeForgetLevelChunk(x, z int32) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDForgetLevelChunk))
	packet = AppendInt32(packet, z)
	return AppendInt32(packet, x)
}

// EncodeKeepAlivePlay 编码 Play 阶段的 Keep Alive 包。
func EncodeKeepAlivePlay(id int64) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDKeepAlive))
	return AppendInt64(packet, id)
}

// EncodePlayerInfoAddPlayer 编码 Player Info Update 包，包含 Add Player 与
// Update Listed 动作；properties 是玩家档案属性（正版模式下会话服务器返回的
// 皮肤纹理等；签名一并携带，客户端据此校验并加载皮肤）。
func EncodePlayerInfoAddPlayer(uuid [16]byte, username string, properties []GameProfileProperty) []byte {
	const (
		actionAddPlayer    = 0x01
		actionUpdateListed = 0x08
	)
	packet := AppendVarInt(nil, int32(PlayPacketIDPlayerInfoUpdate))
	packet = append(packet, actionAddPlayer|actionUpdateListed)
	packet = AppendVarInt(packet, 1) // 玩家数量
	packet = append(packet, uuid[:]...)
	packet = appendString(packet, username)
	packet = AppendVarInt(packet, int32(len(properties)))
	for _, property := range properties {
		packet = appendString(packet, property.Name)
		packet = appendString(packet, property.Value)
		if property.Signed {
			packet = append(packet, 0x01)
			packet = appendString(packet, property.Signature)
		} else {
			packet = append(packet, 0x00)
		}
	}
	return AppendBool(packet, true) // Listed
}

// EncodeSystemChat 编码 System Chat Message 包；内容为只含文本的 NBT String Tag。
func EncodeSystemChat(text string) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDSystemChat))
	packet = AppendNBTString(packet, text)
	return AppendBool(packet, false) // 聊天栏而非操作栏
}

// EncodePlayDisconnect 编码 Play 阶段的 Disconnect 包。
func EncodePlayDisconnect(reason string) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDDisconnect))
	return AppendNBTString(packet, reason)
}

// ParsePlayKeepAlive 解析 Keep Alive（serverbound）包。
func ParsePlayKeepAlive(packet []byte) (int64, error) {
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != PlayServerboundPacketIDKeepAlive {
		return 0, fmt.Errorf("invalid keep alive packet")
	}
	value, _, err := DecodeInt64(packet, offset)
	if err != nil {
		return 0, err
	}
	return value, nil
}

// ParseConfirmTeleportation 解析 Confirm Teleportation 包，返回 teleport ID。
func ParseConfirmTeleportation(packet []byte) (int32, error) {
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != PlayServerboundPacketIDConfirmTeleportation {
		return 0, fmt.Errorf("invalid confirm teleportation packet")
	}
	teleportID, _, err := decodeVarIntAt(packet, offset)
	if err != nil {
		return 0, err
	}
	return teleportID, nil
}

// ParsePlayClientCommand 解析 Client Status 包，返回 action ID。
func ParsePlayClientCommand(packet []byte) (int32, error) {
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != PlayServerboundPacketIDClientCommand {
		return 0, fmt.Errorf("invalid client command packet")
	}
	action, _, err := decodeVarIntAt(packet, offset)
	if err != nil {
		return 0, err
	}
	return action, nil
}

// PlayerChatData 是 Player Chat 包（未签名消息）的内容。
// 服务器在离线模式下无法为消息签名，因此签名与消息链为空，
// 客户端会将其显示为“不安全”消息（与原版离线服务器一致）。
type PlayerChatData struct {
	// GlobalIndex 是服务器范围内递增的消息序号。
	GlobalIndex int32
	// SenderUUID 是发送者 UUID。
	SenderUUID [16]byte
	// SenderIndex 是发送者个人的消息序号（从 0 递增）。
	SenderIndex int32
	// Message 是消息纯文本内容。
	Message string
	// Timestamp 是 Unix 毫秒时间戳。
	Timestamp int64
	// DisplayName 是发送者显示名（聊天栏中 <名字> 部分）。
	DisplayName string
}

// EncodePlayerChatMessage 编码未签名的 Player Chat 包。
func EncodePlayerChatMessage(chat PlayerChatData) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDPlayerChat))
	packet = AppendVarInt(packet, chat.GlobalIndex)
	packet = append(packet, chat.SenderUUID[:]...)
	packet = AppendVarInt(packet, chat.SenderIndex)
	packet = append(packet, 0x00) // 无消息签名
	packet = appendString(packet, chat.Message)
	packet = AppendInt64(packet, chat.Timestamp)
	packet = AppendInt64(packet, 0)  // salt
	packet = AppendVarInt(packet, 0) // previousMessages 为空
	// unsignedChatContent：Some(消息文本组件)。
	packet = append(packet, 0x01)
	packet = AppendNBTString(packet, chat.Message)
	packet = AppendVarInt(packet, 0) // filterType = 0（原样通过，无 mask）
	// type：chat_type 注册表引用（Holder 编码为 id + 1）。
	packet = AppendVarInt(packet, registry.ChatTypeChatID+1)
	packet = AppendNBTString(packet, chat.DisplayName) // networkName
	return append(packet, 0x00)                        // networkTargetName：无
}

// EncodePlayerInfoRemove 编码 Player Info Remove 包（从玩家列表移除条目）。
func EncodePlayerInfoRemove(uuids [][16]byte) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDPlayerInfoRemove))
	packet = AppendVarInt(packet, int32(len(uuids)))
	for _, uuid := range uuids {
		packet = append(packet, uuid[:]...)
	}
	return packet
}

// EncodeSetPlayerInventory 编码 Set Player Inventory 包（更新玩家物品栏单个槽位）。
// slot 编号：0–8 快捷栏、9–35 主背包、36–39 盔甲、40 副手。
// slotData 是已编码的 Slot 数据（见 item.Stack.AppendSlot）。
func EncodeSetPlayerInventory(slot int32, slotData []byte) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDSetPlayerInventory))
	packet = AppendVarInt(packet, slot)
	return append(packet, slotData...)
}

// MaxChatMessageLength 是聊天消息的最大字符数（与 1.21.11 一致）。
const MaxChatMessageLength = 256

// ParseChatMessage 解析 Serverbound Chat Message 包中的消息文本。
// 时间戳、签名、消息确认等字段当前不需要，不予解析。
func ParseChatMessage(packet []byte) (string, error) {
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != PlayServerboundPacketIDChatMessage {
		return "", fmt.Errorf("invalid chat message packet")
	}
	message, _, err := readStringAt(packet, offset)
	if err != nil {
		return "", err
	}
	if len(message) > MaxChatMessageLength {
		return "", fmt.Errorf("chat message too long: %d", len(message))
	}
	return message, nil
}
