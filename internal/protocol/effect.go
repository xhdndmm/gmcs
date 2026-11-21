//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package protocol

// 1.21.11 状态效果相关的 clientbound 包。
const (
	// PlayPacketIDRemoveMobEffect 是 Remove Mob Effect（官方名
	// minecraft:remove_mob_effect）。
	PlayPacketIDRemoveMobEffect = 0x4C
	// PlayPacketIDUpdateMobEffect 是 Update Mob Effect（官方名
	// minecraft:update_mob_effect）。
	PlayPacketIDUpdateMobEffect = 0x82
)

// UpdateMobEffect 的标志位（对照 ClientboundUpdateMobEffectPacket 的
// FLAG_AMBIENT/FLAG_VISIBLE/FLAG_SHOW_ICON/FLAG_BLEND）。
const (
	MobEffectFlagAmbient = 1 << iota
	MobEffectFlagVisible
	MobEffectFlagShowIcon
	MobEffectFlagBlend
)

// EncodeUpdateMobEffect 编码 Update Mob Effect 包。
// effectID 是静态 minecraft:mob_effect 注册表的原始 protocol_id；
// amplifier/duration 为 VarInt，flags 为单字节。
func EncodeUpdateMobEffect(entityID, effectID, amplifier, duration int32, flags uint8) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDUpdateMobEffect))
	packet = AppendVarInt(packet, entityID)
	packet = AppendVarInt(packet, effectID)
	packet = AppendVarInt(packet, amplifier)
	packet = AppendVarInt(packet, duration)
	return append(packet, flags)
}

// EncodeRemoveMobEffect 编码 Remove Mob Effect 包。
func EncodeRemoveMobEffect(entityID, effectID int32) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDRemoveMobEffect))
	packet = AppendVarInt(packet, entityID)
	return AppendVarInt(packet, effectID)
}
