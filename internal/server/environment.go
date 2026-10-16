//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"

	"gmcs/internal/protocol"
	"gmcs/internal/world"
)

// 玩家环境效果：岩浆/着火/溺水/岩浆块。数值与判定对齐原版
// Entity.baseTick / lavaHurt / lavaIgnite 与 LivingEntity 的氧气结算：
//
//   - 接触岩浆：点燃 15 秒，并受到 4 点岩浆伤害（伤害受 0.5 秒受伤
//     无敌帧限制，即每秒约 8 点）；
//   - 着火：每 20 tick（1 秒）受到 1 点火焰伤害；在岩浆中不结算
//     （由岩浆伤害取代）；身体入水立即熄灭；
//   - 溺水：头部浸水时氧气每 tick 下降，耗尽（-20）后归零并立即受到
//     2 点溺水伤害，之后约每秒一次；
//   - 岩浆块：站在其顶面时每 0.5 秒 1 点伤害（潜行不免疫）；
//   - 创造/旁观模式的免伤与死亡消息细节由 applyDamage / damagePlayerWithMessage 处理。
//
// 着火与氧气通过 Set Entity Data 同步（变化时才发送）：
// 索引 0 的字节是实体共享标志（bit0 = 着火），索引 1 的 VarInt 是氧气值。
const (
	// maxPlayerAir 是满氧气值（15 秒 = 300 tick）。
	maxPlayerAir = 300
	// lavaFireTicks 是岩浆点燃的着火时长（15 秒）。
	lavaFireTicks = 15 * 20
	// envLavaDamage 是每次岩浆伤害结算的数值（4 点）。
	envLavaDamage = 4
	// envFireDamage 是每秒火焰伤害（1 点）。
	envFireDamage = 1
	// envDrownDamage 是每次溺水伤害结算的数值（2 点）。
	envDrownDamage = 2
	// envMagmaDamage 是岩浆块每 0.5 秒的伤害（1 点）。
	envMagmaDamage = 1
)

// tickPlayerEnvironment 推进所有在线玩家的环境效果（由服务器 Tick 调用）。
func (s *Server) tickPlayerEnvironment(players []*session) {
	for _, player := range players {
		if !player.isJoined() {
			continue
		}
		player.tickEnvironment(s, players)
	}
}

// tickEnvironment 推进单个玩家一帧的环境效果。
// 仅由服务器 Tick goroutine 调用。
func (s *session) tickEnvironment(server *Server, players []*session) {
	w := s.playerWorld()
	if w == nil {
		return
	}
	x, y, z, _, _ := s.playerPosition()
	inLava := fluidKindAt(w, x, y, z) == lavaBlockName || fluidKindAt(w, x, y+1, z) == lavaBlockName
	inWater := fluidKindAt(w, x, y, z) == waterBlockName || fluidKindAt(w, x, y+1, z) == waterBlockName
	// 火/灵魂火方块（原版 BaseFireBlock 的方块内效果；用脚部与上方一格代替碰撞盒相交）。
	inFire := isFireBlockState(w.BlockAt(int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z)))) ||
		isFireBlockState(w.BlockAt(int(math.Floor(x)), int(math.Floor(y))+1, int(math.Floor(z))))

	// 岩浆：点燃 15 秒 + 结算岩浆伤害（无敌帧限制频率）。
	if inLava {
		if s.fireTicks < lavaFireTicks {
			s.fireTicks = lavaFireTicks
		}
		server.damagePlayerWithMessage(s, envLavaDamage, "lava", 0, server.lavaDamageTypeID, nil,
			"%s tried to swim in lava")
	}
	// 入水熄灭（原版还包含雨与气泡柱，gmcs 目前无天气）。
	if inWater {
		s.fireTicks = 0
	}
	// 着火：每秒 1 点火焰伤害并递减；岩浆中由岩浆伤害取代。
	if s.fireTicks > 0 {
		if s.fireTicks%20 == 0 && !inLava {
			server.damagePlayerWithMessage(s, envFireDamage, "fire", 0, server.fireDamageTypeID, nil,
				"%s burned to death")
		}
		s.fireTicks--
	}
	// 火方块：点燃 8 秒；玩家在火中每 tick 额外 +rand(1..2)（原版 BaseFireBlock
	// 的方块内效果：非火免实体先累加再取 igniteForSeconds(8) 下限；
	// 方块内效果在 baseTick 之后应用，因此入火当帧结束时着火时长就是完整值）。
	if inFire {
		s.fireTicks += 1 + int(server.nextRandom()%2)
		if s.fireTicks < mobFireBlockTicks {
			s.fireTicks = mobFireBlockTicks
		}
	}
	// 岩浆块：站在顶面时周期性伤害。
	if supportedAt(w, x, y, z) && fluidAt(w, x, y-0.1, z, world.MagmaBlock) {
		server.damagePlayerWithMessage(s, envMagmaDamage, "magma", 0, server.hotFloorDamageTypeID, nil,
			"%s discovered the floor was lava")
	}
	// 溺水：仅当眼睛（头部）浸水时氧气下降。
	if headInWater(w, x, y, z) {
		if s.air > -20 {
			s.air--
		}
		if s.air <= -20 {
			s.air = 0
			server.damagePlayerWithMessage(s, envDrownDamage, "drowning", 0, server.drownDamageTypeID, nil,
				"%s drowned")
		}
	} else if s.air < maxPlayerAir {
		s.air = maxPlayerAir
	}
	s.syncEnvironmentMetadata(server, players, x, z)
}

// syncEnvironmentMetadata 把着火与氧气状态变化同步给附近玩家（含本人）。
func (s *session) syncEnvironmentMetadata(server *Server, players []*session, x, z float64) {
	onFire := s.fireTicks > 0
	if onFire != s.envFireSynced {
		s.envFireSynced = onFire
		var flags byte
		if onFire {
			flags = 1 // 实体共享标志 bit0：着火。
		}
		packet := protocol.EncodeEntityMetadataByte(s.entityID, 0, flags)
		server.broadcastToNearby(s.dimensionID(), packet, x, z, players)
	}
	// 氧气：跨过 20 的整数倍、重置或耗尽时同步（降低包量；客户端气泡
	// 按步长刷新，视觉可接受）。
	if s.air != s.envAirSynced && (s.air == maxPlayerAir || s.air <= 0 || s.air%20 == 0) {
		s.envAirSynced = s.air
		packet := protocol.EncodeEntityMetadataVarInt(s.entityID, 1, int32(s.air))
		server.broadcastToNearby(s.dimensionID(), packet, x, z, players)
	}
}

// fluidAt 报告方块坐标处的方块状态是否为指定方块（水/岩浆/岩浆块）。
func fluidAt(w *world.World, x, y, z float64, state uint16) bool {
	return w.BlockAt(int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))) == state
}

// fluidKindAt 返回方块坐标处的流体种类（""=非流体、water、lava；任意等级）。
func fluidKindAt(w *world.World, x, y, z float64) string {
	state := w.BlockAt(int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z)))
	if isWaterState(state) {
		return waterBlockName
	}
	if isLavaState(state) {
		return lavaBlockName
	}
	return ""
}

// headInWater 报告玩家眼睛位置浸水（原版溺水判定按眼睛；任意等级的水）。
func headInWater(w *world.World, x, y, z float64) bool {
	return fluidKindAt(w, x, y+1.5, z) == waterBlockName
}
