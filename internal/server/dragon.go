//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"

	"gmcs/internal/protocol"
	"gmcs/internal/world"
)

// dragonEntity 是末地 Boss 的最小实现：固定环绕 (0, 80, 0) 飞行，
// 支持玩家近战伤害与死亡/移除。末地水晶、龙息、栖息阶段与真正原版
// EnderDragon 的路径点 AI 仍未移植；这里不把它伪装成完整 Boss 战。
type dragonEntity struct {
	ID      int32
	UUID    [16]byte
	Dim     world.Dimension
	X, Y, Z float64
	Yaw     float32
	Health  float32
	Angle   float64
	Dead    bool
}

const dragonMaxHealth = 200

func (s *Server) tickDragon(players []*session) {
	var inEnd bool
	for _, player := range players {
		if player.isJoined() && player.dimensionID() == world.DimensionEnd {
			inEnd = true
			break
		}
	}
	if !inEnd || s.dragonTypeID == 0 {
		return
	}
	s.entityMu.Lock()
	if s.dragon == nil {
		dragon := &dragonEntity{
			ID: s.entityIDs.Add(1), UUID: newEntityUUID(), Dim: world.DimensionEnd,
			X: 0, Y: 80, Z: 0, Health: dragonMaxHealth,
		}
		s.dragon = dragon
		s.entityMu.Unlock()
		s.sendDragonSpawn(dragon)
		return
	}
	dragon := s.dragon
	if dragon.Dead {
		s.entityMu.Unlock()
		return
	}
	// 半径 40 的水平环绕；速度约为原版巡航速度的近似值。
	dragon.Angle += 0.012
	dragon.X = math.Cos(dragon.Angle) * 40
	dragon.Z = math.Sin(dragon.Angle) * 40
	dragon.Y = 80 + math.Sin(dragon.Angle*2)*4
	dragon.Yaw = float32(math.Atan2(-math.Sin(dragon.Angle), math.Cos(dragon.Angle))*180/math.Pi) + 90
	x, y, z, yaw := dragon.X, dragon.Y, dragon.Z, dragon.Yaw
	s.entityMu.Unlock()
	packet := protocol.EncodeEntityPositionSync(dragon.ID, x, y, z, 0, 0, 0, yaw, 0, true)
	s.broadcastToNearby(world.DimensionEnd, packet, x, z, players)
}

func (s *Server) sendDragonSpawn(dragon *dragonEntity) {
	packet := protocol.EncodeAddEntity(dragon.ID, dragon.UUID, s.dragonTypeID, dragon.X, dragon.Y, dragon.Z, 0, 0, 0, dragon.Yaw, 0)
	for _, player := range s.playerSnapshot() {
		if player.isJoined() && player.dimensionID() == world.DimensionEnd {
			player.tryWrite(packet)
		}
	}
}

// damageDragon 结算玩家对末影龙的近战伤害。
func (s *Server) damageDragon(player *session, dragon *dragonEntity, damage float32) {
	s.entityMu.Lock()
	if dragon == nil || dragon.Dead || dragon.Health <= 0 {
		s.entityMu.Unlock()
		return
	}
	dragon.Health -= damage
	died := dragon.Health <= 0
	if died {
		dragon.Health = 0
		dragon.Dead = true
	}
	x, y, z := dragon.X, dragon.Y, dragon.Z
	id := dragon.ID
	s.entityMu.Unlock()
	position := [3]float64{x, y, z}
	s.broadcastToNearby(world.DimensionEnd, protocol.EncodeHurtAnimation(id, 0), x, z, s.playerSnapshot())
	s.broadcastToNearby(world.DimensionEnd, protocol.EncodeDamageEvent(id, s.playerAttackDamageTypeID, player.entityID, player.entityID, &position), x, z, s.playerSnapshot())
	if died {
		s.broadcastPacket(protocol.EncodeEntityEvent(id, protocol.EntityEventDeath))
		s.broadcastPacket(protocol.EncodeEntityDestroy([]int32{id}))
	}
}
