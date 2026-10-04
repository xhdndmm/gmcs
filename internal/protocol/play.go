package protocol

import "fmt"

// 1.21.11（协议 774）Play 阶段的包 ID（仅列出当前实现使用的部分）。
const (
	PlayPacketIDDisconnect           = 0x20 // clientbound
	PlayPacketIDGameEvent            = 0x26 // clientbound
	PlayPacketIDKeepAlive            = 0x2B // clientbound
	PlayPacketIDChunkData            = 0x2C // clientbound
	PlayPacketIDLogin                = 0x30 // clientbound
	PlayPacketIDPlayerInfoUpdate     = 0x44 // clientbound
	PlayPacketIDSynchronizePlayerPos = 0x46 // clientbound
	PlayPacketIDSetCenterChunk       = 0x5C // clientbound
	PlayPacketIDSetDefaultSpawn      = 0x5F // clientbound
	PlayPacketIDSystemChat           = 0x77 // clientbound

	PlayServerboundPacketIDConfirmTeleportation   = 0x00
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

	// worldState（SpawnInfo）。
	DimensionTypeID  int32
	DimensionName    string
	HashedSeed       int64
	GameMode         uint8
	PreviousGameMode uint8 // 0 表示未定义；否则为游戏模式 + 1
	Debug            bool
	Flat             bool
	PortalCooldown   int32
	SeaLevel         int32

	EnforcesSecureChat bool
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
	// worldState
	packet = AppendVarInt(packet, play.DimensionTypeID)
	packet = appendString(packet, play.DimensionName)
	packet = AppendInt64(packet, play.HashedSeed)
	packet = append(packet, play.GameMode)
	packet = append(packet, play.PreviousGameMode)
	packet = AppendBool(packet, play.Debug)
	packet = AppendBool(packet, play.Flat)
	packet = AppendBool(packet, false) // 暂无死亡位置
	packet = AppendVarInt(packet, play.PortalCooldown)
	packet = AppendVarInt(packet, play.SeaLevel)
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

// EncodeKeepAlivePlay 编码 Play 阶段的 Keep Alive 包。
func EncodeKeepAlivePlay(id int64) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDKeepAlive))
	return AppendInt64(packet, id)
}

// EncodePlayerInfoAddPlayer 编码 Player Info Update 包，仅包含 Add Player 与 Update Listed 动作。
func EncodePlayerInfoAddPlayer(uuid [16]byte, username string) []byte {
	const (
		actionAddPlayer    = 0x01
		actionUpdateListed = 0x08
	)
	packet := AppendVarInt(nil, int32(PlayPacketIDPlayerInfoUpdate))
	packet = append(packet, actionAddPlayer|actionUpdateListed)
	packet = AppendVarInt(packet, 1) // 玩家数量
	packet = append(packet, uuid[:]...)
	packet = appendString(packet, username)
	packet = AppendVarInt(packet, 0) // 属性数量
	return AppendBool(packet, true)  // Listed
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
