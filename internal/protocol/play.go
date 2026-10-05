package protocol

import (
	"errors"
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
	PlayPacketIDBlockUpdate          = 0x08 // clientbound
	// PlayPacketIDBlockAction 是 Block Action（方块动画，如箱子开合）。
	PlayPacketIDBlockAction    = 0x07 // clientbound
	PlayPacketIDRemoveEntities = 0x4B // clientbound
	// PlayPacketIDForgetLevelChunk 是 Forget Level Chunk（卸载客户端区块缓存）。
	PlayPacketIDForgetLevelChunk   = 0x25 // clientbound
	PlayPacketIDSetCenterChunk     = 0x5C // clientbound
	PlayPacketIDSetDefaultSpawn    = 0x5F // clientbound
	PlayPacketIDSetPlayerInventory = 0x6A // clientbound
	PlayPacketIDSystemChat         = 0x77 // clientbound
	// PlayPacketIDChunkBatchFinished 是 Chunk Batch Finished（批次结束）。
	PlayPacketIDChunkBatchFinished = 0x0B // clientbound
	// PlayPacketIDChunkBatchStart 是 Chunk Batch Start（批次开始）。
	PlayPacketIDChunkBatchStart = 0x0C // clientbound
	// PlayPacketIDCollect 是 Pickup Item（拾取物品动画）。
	PlayPacketIDCollect = 0x7A // clientbound

	PlayServerboundPacketIDConfirmTeleportation = 0x00
	PlayServerboundPacketIDChatMessage          = 0x08
	// PlayServerboundPacketIDChatSessionUpdate 是 Chat Session Update（聊天会话公钥）。
	PlayServerboundPacketIDChatSessionUpdate      = 0x09
	PlayServerboundPacketIDChunkBatchReceived     = 0x0A
	PlayServerboundPacketIDClientCommand          = 0x0B
	PlayServerboundPacketIDClientTickEnd          = 0x0C
	PlayServerboundPacketIDClientInformation      = 0x0D
	PlayServerboundPacketIDKeepAlive              = 0x1B
	PlayServerboundPacketIDPlayerPosition         = 0x1D
	PlayServerboundPacketIDPlayerPositionRotation = 0x1E
	PlayServerboundPacketIDPlayerRotation         = 0x1F
	PlayServerboundPacketIDPlayerMovementFlags    = 0x20
	PlayServerboundPacketIDPlayerAction           = 0x28
	PlayServerboundPacketIDPlayerInput            = 0x2A
	PlayServerboundPacketIDPlayerLoaded           = 0x2B
	PlayServerboundPacketIDSetCarriedItem         = 0x34
	PlayServerboundPacketIDSetCreativeSlot        = 0x37
	PlayServerboundPacketIDSwingArm               = 0x3C
	PlayServerboundPacketIDUseItemOn              = 0x3F
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

// EncodeChunkBatchStart 编码 Chunk Batch Start 包（空负载）。
// 客户端用它测量批次耗时，并向服务器回报期望的每 tick 区块数。
func EncodeChunkBatchStart() []byte {
	return AppendVarInt(nil, int32(PlayPacketIDChunkBatchStart))
}

// EncodeChunkBatchFinished 编码 Chunk Batch Finished 包（批次中的区块数量）。
func EncodeChunkBatchFinished(batchSize int32) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDChunkBatchFinished))
	return AppendVarInt(packet, batchSize)
}

// EncodeCollect 编码 Pickup Item 包（拾取动画）：collectedID 为被拾取的
// 掉落物实体，collectorID 为拾取者，count 为拾取数量。
func EncodeCollect(collectedID, collectorID, count int32) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDCollect))
	packet = AppendVarInt(packet, collectedID)
	packet = AppendVarInt(packet, collectorID)
	return AppendVarInt(packet, count)
}

// ParseChunkBatchReceived 解析 Chunk Batch Received 包，返回客户端期望的
// 每 tick 区块数（f32）。非法值（负数/NaN）返回错误。
func ParseChunkBatchReceived(packet []byte) (float32, error) {
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != PlayServerboundPacketIDChunkBatchReceived {
		return 0, fmt.Errorf("invalid chunk batch received packet")
	}
	value, _, err := DecodeFloat32(packet, offset)
	if err != nil {
		return 0, err
	}
	if value < 0 || value != value {
		return 0, fmt.Errorf("invalid chunks per tick %v", value)
	}
	return value, nil
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

// UnpackPosition 解包协议的 Position 类型（与 PackPosition 互逆）。
func UnpackPosition(value int64) (x, y, z int) {
	return int(value >> 38), int(value << 52 >> 52), int(value << 26 >> 38)
}

// EncodeBlockUpdate 编码 Block Update 包（单个方块状态变更）。
func EncodeBlockUpdate(x, y, z int, state int32) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDBlockUpdate))
	packet = AppendInt64(packet, PackPosition(x, y, z))
	return AppendVarInt(packet, state)
}

// EncodeBlockAction 编码 Block Action 包（方块动画与事件，如箱子开合）。
// blockID 是方块注册表 ID（不是方块状态 ID）。
func EncodeBlockAction(x, y, z int, paramA, paramB uint8, blockID int32) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDBlockAction))
	packet = AppendInt64(packet, PackPosition(x, y, z))
	packet = append(packet, paramA, paramB)
	return AppendVarInt(packet, blockID)
}

// ParsePlayerInput 解析 Player Input 包，返回原始输入位标志。
// 位定义（低位在前）：0 前进、1 后退、2 左、3 右、4 跳跃、5 潜行、6 冲刺。
func ParsePlayerInput(packet []byte) (uint8, error) {
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != PlayServerboundPacketIDPlayerInput {
		return 0, fmt.Errorf("invalid player input packet")
	}
	if len(packet) <= offset {
		return 0, fmt.Errorf("player input packet missing flags")
	}
	return packet[offset], nil
}

// PlayerInputShift 是 Player Input 位标志中的潜行位。
const PlayerInputShift = 1 << 5

// EncodeRemoveEntities 编码 Remove Entities 包（实体 ID 列表）。
func EncodeRemoveEntities(ids []int32) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDRemoveEntities))
	packet = AppendVarInt(packet, int32(len(ids)))
	for _, id := range ids {
		packet = AppendVarInt(packet, id)
	}
	return packet
}

// PlayerAction 是 Player Action（block_dig）包的解析结果。
type PlayerAction struct {
	Status   int32
	X, Y, Z  int
	Face     int8
	Sequence int32
}

// ParsePlayerAction 解析 Player Action（block_dig）包。
func ParsePlayerAction(packet []byte) (PlayerAction, error) {
	var action PlayerAction
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != PlayServerboundPacketIDPlayerAction {
		return action, fmt.Errorf("invalid player action packet")
	}
	if action.Status, offset, err = decodeVarIntAt(packet, offset); err != nil {
		return action, err
	}
	position, offset, err := DecodeInt64(packet, offset)
	if err != nil {
		return action, err
	}
	action.X, action.Y, action.Z = UnpackPosition(position)
	if len(packet) <= offset {
		return action, fmt.Errorf("player action packet missing face")
	}
	action.Face = int8(packet[offset])
	offset++
	action.Sequence, _, err = decodeVarIntAt(packet, offset)
	return action, err
}

// UseItemOn 是 Use Item On（block_place）包的解析结果。
type UseItemOn struct {
	Hand                      int32
	X, Y, Z                   int
	Direction                 int32
	CursorX, CursorY, CursorZ float32
	InsideBlock               bool
}

// ParseUseItemOn 解析 Use Item On（block_place）包。
func ParseUseItemOn(packet []byte) (UseItemOn, error) {
	var use UseItemOn
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != PlayServerboundPacketIDUseItemOn {
		return use, fmt.Errorf("invalid use item on packet")
	}
	if use.Hand, offset, err = decodeVarIntAt(packet, offset); err != nil {
		return use, err
	}
	position, offset, err := DecodeInt64(packet, offset)
	if err != nil {
		return use, err
	}
	use.X, use.Y, use.Z = UnpackPosition(position)
	if use.Direction, offset, err = decodeVarIntAt(packet, offset); err != nil {
		return use, err
	}
	if use.CursorX, offset, err = DecodeFloat32(packet, offset); err != nil {
		return use, err
	}
	if use.CursorY, offset, err = DecodeFloat32(packet, offset); err != nil {
		return use, err
	}
	if use.CursorZ, offset, err = DecodeFloat32(packet, offset); err != nil {
		return use, err
	}
	if use.InsideBlock, offset, err = DecodeBool(packet, offset); err != nil {
		return use, err
	}
	if _, _, err = DecodeBool(packet, offset); err != nil { // worldBorderHit
		return use, err
	}
	return use, nil
}

// ParseSetCarriedItem 解析 Set Carried Item（切换快捷栏槽位）包。
// 槽位越界时返回错误。
func ParseSetCarriedItem(packet []byte) (int32, error) {
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != PlayServerboundPacketIDSetCarriedItem {
		return 0, fmt.Errorf("invalid set carried item packet")
	}
	slot, _, err := decodeVarIntAt(packet, offset)
	if err != nil {
		return 0, err
	}
	if slot < 0 || slot > 8 {
		return 0, fmt.Errorf("carried slot %d out of range", slot)
	}
	return slot, nil
}

// errUnsupportedComponents 表示物品携带数据组件（附魔等），当前不支持解析。
var errUnsupportedComponents = errors.New("item with data components is not supported")

// ParseSetCreativeSlot 解析 Set Creative Mode Slot 包。
// 返回槽位、物品 ID 与数量；数量为 0 表示清空槽位。
// 带数据组件的物品返回 errUnsupportedComponents。
func ParseSetCreativeSlot(packet []byte) (slot int, itemID int32, count int32, err error) {
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != PlayServerboundPacketIDSetCreativeSlot {
		return 0, 0, 0, fmt.Errorf("invalid set creative slot packet")
	}
	rawSlot, offset, err := DecodeInt16(packet, offset)
	if err != nil {
		return 0, 0, 0, err
	}
	if count, offset, err = decodeVarIntAt(packet, offset); err != nil {
		return 0, 0, 0, err
	}
	slot = int(rawSlot)
	if count <= 0 {
		return slot, 0, 0, nil
	}
	if itemID, offset, err = decodeVarIntAt(packet, offset); err != nil {
		return 0, 0, 0, err
	}
	added, offset, err := decodeVarIntAt(packet, offset)
	if err != nil {
		return 0, 0, 0, err
	}
	removed, _, err := decodeVarIntAt(packet, offset)
	if err != nil {
		return 0, 0, 0, err
	}
	if added != 0 || removed != 0 {
		return 0, 0, 0, errUnsupportedComponents
	}
	return slot, itemID, count, nil
}

// ParseSwingArm 解析 Swing Arm（挥手）包，校验包 ID 与长度。
func ParseSwingArm(packet []byte) error {
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != PlayServerboundPacketIDSwingArm {
		return fmt.Errorf("invalid swing arm packet")
	}
	if _, _, err := decodeVarIntAt(packet, offset); err != nil { // hand
		return err
	}
	return nil
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
