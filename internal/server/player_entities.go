package server

import (
	"log/slog"
	"math"

	"gmcs/internal/protocol"
	"gmcs/internal/registry"
)

// 玩家实体同步：让其他玩家看到彼此（Add Entity / Entity Position Sync /
// 头部朝向 / Remove Entities）。皮肤由 Player Info 的档案属性提供；
// 注册表缺少玩家实体类型时整体禁用（其他功能不受影响）。
const playerMoveBroadcastRange = 64

// resolvePlayerEntityType 解析玩家实体类型 ID；失败时禁用玩家实体同步。
func (s *Server) resolvePlayerEntityType() {
	id, ok := registry.StaticEntryID("minecraft:entity_type", "minecraft:player")
	if !ok {
		slog.Warn("缺少玩家实体类型注册表项，玩家实体同步已禁用")
		return
	}
	s.playerTypeID = id
	s.playerEntitiesEnabled = true
}

// playerSpawnPacket 构造把玩家加入世界所需的 Add Entity 包。
func (s *Server) playerSpawnPacket(player *session) []byte {
	x, y, z, yaw, pitch := player.playerPosition()
	return protocol.EncodeAddEntity(player.entityID, player.uuid, s.playerTypeID, x, y, z, 0, 0, 0, yaw, pitch)
}

// playerSkinPacket 构造玩家皮肤层显示掩码的实体元数据
// （Avatar 基类索引 16，值来自客户端 Client Information）。
func (s *Server) playerSkinPacket(player *session) []byte {
	return protocol.EncodeEntityMetadataSkinParts(player.entityID, player.skinParts())
}

// writeToNearbyPlayers 把数据包写给附近（水平距离 ≤ playerMoveBroadcastRange）
// 且已完成进入世界的玩家。
func (s *Server) writeToNearbyPlayers(packet []byte, x, z float64, except *session) {
	for _, player := range s.playerSnapshot() {
		if player == except || !player.isJoined() {
			continue
		}
		px, _, pz, _, _ := player.playerPosition()
		if math.Hypot(px-x, pz-z) <= playerMoveBroadcastRange {
			player.tryWrite(packet)
		}
	}
}

// broadcastPlayerMove 把玩家的位置与朝向同步给附近玩家（会话读循环调用）。
func (s *Server) broadcastPlayerMove(player *session) {
	if !s.playerEntitiesEnabled || !player.isJoined() {
		return
	}
	x, y, z, yaw, pitch := player.playerPosition()
	s.writeToNearbyPlayers(protocol.EncodeEntityPositionSync(player.entityID, x, y, z, 0, 0, 0, yaw, pitch, true), x, z, player)
	s.writeToNearbyPlayers(protocol.EncodeEntityHeadRotation(player.entityID, yaw), x, z, player)
}

// broadcastSwing 广播玩家的挥手动画（攻击挥空时其他玩家也能看到动作）。
func (s *Server) broadcastSwing(player *session) {
	if !s.playerEntitiesEnabled || !player.isJoined() {
		return
	}
	x, _, z, _, _ := player.playerPosition()
	s.writeToNearbyPlayers(protocol.EncodeAnimate(player.entityID, 0), x, z, player)
}

// broadcastPlayerRemove 通知其他玩家移除该玩家实体。
func (s *Server) broadcastPlayerRemove(player *session) {
	if !s.playerEntitiesEnabled {
		return
	}
	s.broadcastPacketExcluding(protocol.EncodeRemoveEntities([]int32{player.entityID}), player)
}

// handlePlayerAttack 处理玩家攻击玩家：距离与视线校验后结算伤害。
func (s *Server) handlePlayerAttack(attacker *session, targetID int32, px, py, pz float64) {
	if !s.playerEntitiesEnabled {
		return
	}
	var target *session
	for _, candidate := range s.playerSnapshot() {
		if candidate.entityID == targetID && candidate.isJoined() && candidate.canBeAttacked() {
			target = candidate
			break
		}
	}
	if target == nil {
		return
	}
	tx, ty, tz, _, _ := target.playerPosition()
	if math.Hypot(px-tx, pz-tz) > playerAttackRange || math.Abs(py-ty) > 3 {
		return
	}
	if !s.attackPathClear(px, py+mobEyeHeight, pz, tx, ty+mobEyeHeight, tz) {
		return
	}
	position := [3]float64{px, py + 1, pz}
	if s.damagePlayer(target, playerAttackDamage, attacker.name, attacker.entityID, s.playerAttackDamageTypeID, &position) {
		s.broadcastPacket(protocol.EncodeAnimate(attacker.entityID, 0))
	}
}
