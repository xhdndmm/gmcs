package protocol

import (
	"encoding/binary"
	"fmt"
	"math"
)

// 1.21.11 Play 阶段实体与伤害相关的 clientbound 包 ID。
const (
	// PlayPacketIDAddEntity 是 Add Entity（生成实体）。
	PlayPacketIDAddEntity = 0x01
	// PlayPacketIDAnimate 是 Animate（挥手等动画）。
	PlayPacketIDAnimate = 0x02
	// PlayPacketIDDamageEvent 是 Damage Event。
	PlayPacketIDDamageEvent = 0x19
	// PlayPacketIDEntityEvent 是 Entity Event（死亡动画等状态）。
	PlayPacketIDEntityEvent = 0x22
	// PlayPacketIDEntityPositionSync 是 Entity Position Sync（绝对坐标 + 速度）。
	PlayPacketIDEntityPositionSync = 0x23
	// PlayPacketIDHurtAnimation 是 Hurt Animation（受伤闪红）。
	PlayPacketIDHurtAnimation = 0x29
	// PlayPacketIDInitializeWorldBorder 是 Initialize World Border（初始化世界边界）。
	PlayPacketIDInitializeWorldBorder = 0x2A
	// PlayPacketIDEntityDestroy 是 Remove Entities（移除实体）。
	PlayPacketIDEntityDestroy = 0x4B
	// PlayPacketIDRespawn 是 Respawn（重生）。
	PlayPacketIDRespawn = 0x50
	// PlayPacketIDEntityHeadRotation 是 Rotate Head（头部朝向）。
	PlayPacketIDEntityHeadRotation = 0x51
	// PlayPacketIDEntityMetadata 是 Set Entity Data（实体元数据）。
	PlayPacketIDEntityMetadata = 0x61
	// PlayPacketIDUpdateHealth 是 Set Health（生命/饥饿/饱食度）。
	PlayPacketIDUpdateHealth = 0x66
	// PlayPacketIDEntitySoundEffect 是 Entity Sound Effect。
	PlayPacketIDEntitySoundEffect = 0x72
	// PlayPacketIDSoundEffect 是 Sound Effect（按坐标播放）。
	PlayPacketIDSoundEffect = 0x73
)

// Play 阶段实体交互相关的 serverbound 包 ID。
const (
	// PlayServerboundPacketIDInteract 是 Interact（攻击/交互实体；旧名 Use Entity）。
	PlayServerboundPacketIDInteract = 0x19
)

// 实体事件（Entity Event 包）中常用的状态码。
const (
	// EntityEventDeath 是生物死亡动画。
	EntityEventDeath = 3
)

// Interact 动作（Interact 包的第二个 varint）。
const (
	InteractActionInteract   = 0
	InteractActionAttack     = 1
	InteractActionInteractAt = 2
)

// 声音类别（soundSource）。
const (
	SoundCategoryMaster  = 0
	SoundCategoryMusic   = 1
	SoundCategoryRecord  = 2
	SoundCategoryWeather = 3
	SoundCategoryBlock   = 4
	SoundCategoryHostile = 5
	SoundCategoryNeutral = 6
	SoundCategoryPlayer  = 7
	SoundCategoryAmbient = 8
	SoundCategoryVoice   = 9
	SoundCategoryUI      = 10
)

// LpVec3（低精度向量）常量，与 1.21.2+ 的官方实现一致。
const (
	lpVec3MinValue     = 3.051944088384301e-5
	lpVec3MaxValue     = 1.7179869183e10
	lpVec3QuantizedMax = 32766.0
)

// appendLpVec3 追加低精度向量（1.21.2+：全零为单字节 0x00，否则
// 6 字节打包值，尺度 >3 时追加一个 VarInt）。
// 字段布局参考 node-minecraft-protocol 的 lpVec3 实现。
func appendLpVec3(dst []byte, x, y, z float64) []byte {
	x, y, z = sanitizeLpVec3(x), sanitizeLpVec3(y), sanitizeLpVec3(z)
	maxValue := math.Max(math.Abs(x), math.Max(math.Abs(y), math.Abs(z)))
	if maxValue < lpVec3MinValue {
		return append(dst, 0x00)
	}
	scale := math.Ceil(maxValue)
	var markers uint64
	if scale > 3 {
		markers = uint64(int64(scale))%4 | 4
	} else {
		markers = uint64(int64(scale))
	}
	packed := markers +
		packLpVec3(x/scale)*0x8 +
		packLpVec3(y/scale)*0x40000 +
		packLpVec3(z/scale)*0x200000000
	dst = append(dst, byte(packed%0x100), byte(packed/0x100%0x100))
	var rest [4]byte
	binary.BigEndian.PutUint32(rest[:], uint32(packed/0x10000%0x100000000))
	dst = append(dst, rest[:]...)
	if scale > 3 {
		dst = AppendVarInt(dst, int32(math.Floor(scale/4)))
	}
	return dst
}

func packLpVec3(value float64) uint64 {
	return uint64(math.Round((value*0.5 + 0.5) * lpVec3QuantizedMax))
}

func sanitizeLpVec3(value float64) float64 {
	if math.IsNaN(value) {
		return 0
	}
	return math.Min(math.Max(value, -lpVec3MaxValue), lpVec3MaxValue)
}

// angleByte 把角度（度）转换为 1/256 圈的字节角度。
func angleByte(degrees float32) byte {
	return byte(int8(math.Round(float64(degrees) * 256 / 360)))
}

// EncodeAddEntity 编码 Add Entity 包（1.21.9+：位置后跟低精度速度向量）。
// velocity 为方块/tick。
func EncodeAddEntity(id int32, uuid [16]byte, typeID int32, x, y, z, velocityX, velocityY, velocityZ float64, yaw, pitch float32) []byte {
	// 固定布局 ≈70 字节：包 ID 2 + 实体 ID 5 + UUID 16 + 类型 5 + 坐标 24
	// + 速度 ≤7 + 角度 3 + 对象数据 5；预分配避免倍增扩容。
	packet := AppendVarInt(make([]byte, 0, 80), int32(PlayPacketIDAddEntity))
	packet = AppendVarInt(packet, id)
	packet = append(packet, uuid[:]...)
	packet = AppendVarInt(packet, typeID)
	packet = AppendFloat64(packet, x)
	packet = AppendFloat64(packet, y)
	packet = AppendFloat64(packet, z)
	packet = appendLpVec3(packet, velocityX, velocityY, velocityZ)
	packet = append(packet, angleByte(pitch))
	packet = append(packet, angleByte(yaw))
	packet = append(packet, angleByte(yaw)) // head yaw
	return AppendVarInt(packet, 0)          // object data（生物为 0）
}

// EntityPositionSyncMaxSize 是 Entity Position Sync 包的编码上限
// （包 ID 2 + 实体 ID 5 + 6×float64 48 + 2×float32 8 + bool 1）。
const EntityPositionSyncMaxSize = 63

// EncodeEntityPositionSync 编码 Entity Position Sync 包：
// 绝对坐标、速度（方块/tick）、视角与是否着地。
func EncodeEntityPositionSync(id int32, x, y, z, velocityX, velocityY, velocityZ float64, yaw, pitch float32, onGround bool) []byte {
	return AppendEntityPositionSync(make([]byte, 0, EntityPositionSyncMaxSize),
		id, x, y, z, velocityX, velocityY, velocityZ, yaw, pitch, onGround)
}

// AppendEntityPositionSync 把 Entity Position Sync 包追加到 dst
// （调用方可传池化/栈上缓冲，实现移动广播零堆分配）。
func AppendEntityPositionSync(dst []byte, id int32, x, y, z, velocityX, velocityY, velocityZ float64, yaw, pitch float32, onGround bool) []byte {
	packet := AppendVarInt(dst, int32(PlayPacketIDEntityPositionSync))
	packet = AppendVarInt(packet, id)
	packet = AppendFloat64(packet, x)
	packet = AppendFloat64(packet, y)
	packet = AppendFloat64(packet, z)
	packet = AppendFloat64(packet, velocityX)
	packet = AppendFloat64(packet, velocityY)
	packet = AppendFloat64(packet, velocityZ)
	packet = AppendFloat32(packet, yaw)
	packet = AppendFloat32(packet, pitch)
	return AppendBool(packet, onGround)
}

// EncodeEntityDestroy 编码 Remove Entities 包。
func EncodeEntityDestroy(ids []int32) []byte {
	packet := AppendVarInt(make([]byte, 0, 4+5*len(ids)), int32(PlayPacketIDEntityDestroy))
	packet = AppendVarInt(packet, int32(len(ids)))
	for _, id := range ids {
		packet = AppendVarInt(packet, id)
	}
	return packet
}

// EntityMetadataItemStackIndex 是物品实体（minecraft:item）Item 字段的
// 元数据索引（基类实体占用 0–7；见 wiki 的实体元数据表）。
const EntityMetadataItemStackIndex = 8

// EncodeEntityMetadataItem 编码 Set Entity Data 包，设置物品实体的
// Item 字段（槽位数据由 item.Stack.AppendSlot 编码；元数据类型 7 = item_stack）。
func EncodeEntityMetadataItem(id int32, slotData []byte) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDEntityMetadata))
	packet = AppendVarInt(packet, id)
	packet = append(packet, EntityMetadataItemStackIndex)
	packet = AppendVarInt(packet, 7) // 元数据类型：item_stack
	packet = append(packet, slotData...)
	return append(packet, 0xFF) // 元数据数组结束标记
}

// EntityMetadataSkinPartsIndex 是玩家（Avatar 基类）Displayed Skin Parts 字段的
// 元数据索引（0–7 基类、8–14 生物、15 主手、16 皮肤层；见 wiki 实体元数据表）。
const EntityMetadataSkinPartsIndex = 16

// EncodeEntityMetadataByte 编码 Set Entity Data 包，设置单个字节元数据字段
// （元数据类型 0 = byte）。
func EncodeEntityMetadataByte(id int32, index uint8, value byte) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDEntityMetadata))
	packet = AppendVarInt(packet, id)
	packet = append(packet, index)
	packet = AppendVarInt(packet, 0) // 元数据类型：byte
	packet = append(packet, value)
	return append(packet, 0xFF) // 元数据数组结束标记
}

// EncodeEntityMetadataSkinParts 编码 Set Entity Data 包，设置玩家的皮肤层
// 显示掩码（与 Client Information 的 Displayed Skin Parts 一致）。
func EncodeEntityMetadataSkinParts(id int32, parts uint8) []byte {
	return EncodeEntityMetadataByte(id, EntityMetadataSkinPartsIndex, parts)
}

// EncodeEntityEvent 编码 Entity Event 包（entityId 为 int32）。
func EncodeEntityEvent(id int32, status uint8) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDEntityEvent))
	packet = AppendInt32(packet, id)
	return append(packet, status)
}

// EncodeHurtAnimation 编码 Hurt Animation 包（受伤闪红与音效）。
func EncodeHurtAnimation(id int32, yaw float32) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDHurtAnimation))
	packet = AppendVarInt(packet, id)
	return AppendFloat32(packet, yaw)
}

// EncodeAnimate 编码 Animate 包（实体挥手动画；玩家攻击时广播）。
func EncodeAnimate(id int32, animation uint8) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDAnimate))
	packet = AppendVarInt(packet, id)
	return append(packet, animation)
}

// EncodeDamageEvent 编码 Damage Event 包。
// sourceCauseID 与 sourceDirectID 为进攻者实体 ID（-1 表示无），
// 编码时 +1（与协议中 0 表示无进攻者的约定一致）。
func EncodeDamageEvent(victimID int32, damageTypeID, sourceCauseID, sourceDirectID int32, position *[3]float64) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDDamageEvent))
	packet = AppendVarInt(packet, victimID)
	packet = AppendVarInt(packet, damageTypeID)
	packet = AppendVarInt(packet, sourceCauseID+1)
	packet = AppendVarInt(packet, sourceDirectID+1)
	if position == nil {
		return AppendBool(packet, false)
	}
	packet = AppendBool(packet, true)
	packet = AppendFloat64(packet, position[0])
	packet = AppendFloat64(packet, position[1])
	return AppendFloat64(packet, position[2])
}

// EncodeUpdateHealth 编码 Set Health 包。
func EncodeUpdateHealth(health float32, food int32, saturation float32) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDUpdateHealth))
	packet = AppendFloat32(packet, health)
	packet = AppendVarInt(packet, food)
	return AppendFloat32(packet, saturation)
}

// EncodeRespawn 编码 Respawn 包（世界状态 + 保留元数据标志）。
// 调用方应在之后重发区块与位置。
func EncodeRespawn(spawn SpawnInfo, copyMetadata uint8) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDRespawn))
	packet = appendSpawnInfo(packet, spawn)
	return append(packet, copyMetadata)
}

// EncodeEntitySoundEffect 编码 Entity Sound Effect 包。
// soundID 是静态注册表 minecraft:sound_event 中的内置 ID；
// 协议中的 Holder 编码为 ID+1（0 表示内联数据）。
func EncodeEntitySoundEffect(soundID, category, entityID int32, volume, pitch float32, seed int64) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDEntitySoundEffect))
	packet = AppendVarInt(packet, soundID+1)
	packet = AppendVarInt(packet, category)
	packet = AppendVarInt(packet, entityID)
	packet = AppendFloat32(packet, volume)
	packet = AppendFloat32(packet, pitch)
	return AppendInt64(packet, seed)
}

// EncodeSoundEffect 编码 Sound Effect 包（以坐标为中心播放，坐标精度 1/8 方块）。
// soundID 使用与 EncodeEntitySoundEffect 相同的 Holder 编码（ID+1）。
func EncodeSoundEffect(soundID, category int32, x, y, z float64, volume, pitch float32, seed int64) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDSoundEffect))
	packet = AppendVarInt(packet, soundID+1)
	packet = AppendVarInt(packet, category)
	packet = AppendInt32(packet, int32(math.Round(x*8)))
	packet = AppendInt32(packet, int32(math.Round(y*8)))
	packet = AppendInt32(packet, int32(math.Round(z*8)))
	packet = AppendFloat32(packet, volume)
	packet = AppendFloat32(packet, pitch)
	return AppendInt64(packet, seed)
}

// ParseInteract 解析 Interact（serverbound）包的实体 ID 与动作。
// action 为 InteractActionAttack 时表示攻击。
func ParseInteract(packet []byte) (targetID int32, action int32, err error) {
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != PlayServerboundPacketIDInteract {
		return 0, 0, fmt.Errorf("invalid interact packet")
	}
	targetID, offset, err = decodeVarIntAt(packet, offset)
	if err != nil {
		return 0, 0, err
	}
	action, _, err = decodeVarIntAt(packet, offset)
	if err != nil {
		return 0, 0, err
	}
	return targetID, action, nil
}

// EntityHeadRotationMaxSize 是 Rotate Head 包的编码上限（包 ID 2 + 实体 ID 5）。
const EntityHeadRotationMaxSize = 7

// AppendEntityHeadRotation 把 Rotate Head 包追加到 dst。
func AppendEntityHeadRotation(dst []byte, id int32, yaw float32) []byte {
	packet := AppendVarInt(dst, int32(PlayPacketIDEntityHeadRotation))
	packet = AppendVarInt(packet, id)
	return append(packet, angleByte(yaw))
}

// EncodeEntityHeadRotation 编码 Rotate Head 包（头部朝向，1/256 圈字节角度）。
func EncodeEntityHeadRotation(id int32, yaw float32) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDEntityHeadRotation))
	packet = AppendVarInt(packet, id)
	return append(packet, angleByte(yaw))
}

// EncodeInitializeWorldBorder 编码 Initialize World Border 包（正方形边界）。
// portalBoundary 是传送门生效距离（未使用时传 29999984）。
func EncodeInitializeWorldBorder(x, z, oldDiameter, newDiameter float64, speed, portalBoundary, warningBlocks, warningTime int32) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDInitializeWorldBorder))
	packet = AppendFloat64(packet, x)
	packet = AppendFloat64(packet, z)
	packet = AppendFloat64(packet, oldDiameter)
	packet = AppendFloat64(packet, newDiameter)
	packet = AppendVarInt(packet, speed)
	packet = AppendVarInt(packet, portalBoundary)
	packet = AppendVarInt(packet, warningBlocks)
	return AppendVarInt(packet, warningTime)
}
