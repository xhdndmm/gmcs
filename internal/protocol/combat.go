package protocol

// 1.21.11 战斗与爆炸相关的 clientbound 包编码。

// Play 阶段战斗相关的包 ID。
const (
	// PlayPacketIDExplosion 是 Explode（爆炸效果；官方名 minecraft:explode）。
	PlayPacketIDExplosion = 0x24
	// PlayPacketIDEntityVelocity 是 Set Entity Motion（设置实体速度）。
	PlayPacketIDEntityVelocity = 0x63
)

// 爆炸粒子类型 ID（客户端内置 Particle 注册表的固定编号）。
// 参考官方数据生成器：22 = explosion_emitter、23 = explosion。
const (
	ParticleExplosionEmitter = 22
	ParticleExplosion        = 23
)

// 爆炸音效（注册表引用使用 ID+1 的 Holder 语义）。
const soundEntityGenericExplode = 668

// EncodeExplosion 编码 Explode 包：
//
//	center:   f64 × 3
//	radius:   f32
//	blockCount: i32（破坏的方块数，仅用于统计）
//	playerKnockback: option<f64 × 3>（此处固定为 false：击退用 Set Entity Motion
//	                逐玩家发送，便于按距离计算）
//	particle:  VarInt（粒子类型 ID）
//	sound:     VarInt（音效 Holder，ID+1）
//	blockParticles: VarInt 数量（此处为 0：不发送方块碎片数据）
//
// 字段布局来自 1.21.11 官方协议（packet_explosion）。
func EncodeExplosion(x, y, z float64, radius float32, blockCount int32, particleID int32) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDExplosion))
	packet = AppendFloat64(packet, x)
	packet = AppendFloat64(packet, y)
	packet = AppendFloat64(packet, z)
	packet = AppendFloat32(packet, radius)
	packet = AppendInt32(packet, blockCount)
	packet = append(packet, 0x00) // playerKnockback: 无
	packet = AppendVarInt(packet, particleID)
	packet = AppendVarInt(packet, soundEntityGenericExplode+1)
	packet = AppendVarInt(packet, 0) // blockParticles: 空数组
	return packet
}

// EncodeEntityVelocity 编码 Set Entity Motion 包（entityId + LpVec3 速度）。
func EncodeEntityVelocity(entityID int32, vx, vy, vz float64) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDEntityVelocity))
	packet = AppendVarInt(packet, entityID)
	return appendLpVec3(packet, vx, vy, vz)
}
