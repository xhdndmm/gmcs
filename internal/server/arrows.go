package server

import (
	"math"

	"gmcs/internal/protocol"
	"gmcs/internal/world"
)

// 箭矢（骷髅的远程攻击）与爆炸（苦力怕）的实现。
//
// 箭矢是服务器控制的投射物实体：重力 + 阻力推进、逐轴方块碰撞、
// 命中玩家结算伤害，超时/命中/落地后移除。箭矢不持久化（重启后消失）。
//
// 爆炸为简化模型：球半径内的方块（跳过高抗性方块）直接销毁（无掉落），
// 半径 2 倍内的玩家按线性衰减受到伤害并被击退。

// arrowEntity 是一支飞行中的箭。字段由 Server.entityMu 保护。
type arrowEntity struct {
	ID   int32
	UUID [16]byte
	Dim  world.Dimension

	X, Y, Z          float64
	VelX, VelY, VelZ float64
	Damage           float32
	AgeTicks         int
}

// explosionResistantBlocks 是爆炸不会摧毁的方块（与原版高爆炸抗性方块一致）。
var explosionResistantBlocks = map[string]bool{
	"minecraft:bedrock":              true,
	"minecraft:obsidian":             true,
	"minecraft:crying_obsidian":      true,
	"minecraft:ancient_debris":       true,
	"minecraft:respawn_anchor":       true,
	"minecraft:enchanting_table":     true,
	"minecraft:end_portal_frame":     true,
	"minecraft:end_portal":           true,
	"minecraft:nether_portal":        true,
	"minecraft:anvil":                true,
	"minecraft:netherite_block":      true,
	"minecraft:reinforced_deepslate": true,
}

// fireArrow 生成一支由骷髅射出的箭，并广播生成包与射击音效。
func (s *Server) fireArrow(p pendingArrow) {
	if !s.arrowsEnabled {
		return
	}
	dx, dy, dz := p.targetX-p.x, p.targetY-p.y, p.targetZ-p.z
	distance := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if distance < 1e-3 {
		distance = 1e-3
	}
	speed := skeletonArrowSpeed
	arrow := &arrowEntity{
		ID:     s.entityIDs.Add(1),
		UUID:   newEntityUUID(),
		Dim:    p.dim,
		X:      p.x,
		Y:      p.y,
		Z:      p.z,
		VelX:   dx / distance * speed,
		VelY:   dy/distance*speed + 0.08,
		VelZ:   dz / distance * speed,
		Damage: p.damage,
	}
	s.entityMu.Lock()
	s.arrows[arrow.ID] = arrow
	s.entityMu.Unlock()

	s.writeToNearbyPlayers(protocol.EncodeAddEntity(arrow.ID, arrow.UUID, s.arrowTypeID,
		arrow.X, arrow.Y, arrow.Z, arrow.VelX, arrow.VelY, arrow.VelZ, 0, 0), p.dim, arrow.X, arrow.Z, nil)
	s.broadcastToNearby(p.dim,
		protocol.EncodeEntitySoundEffect(s.soundArrowShoot, protocol.SoundCategoryHostile, p.mobID, 1, 1.2, 0),
		arrow.X, arrow.Z, s.playerSnapshot())
}

// arrowHit 是一次待结算的箭矢命中（位置为命中瞬间的快照）。
type arrowHit struct {
	player  *session
	damage  float32
	x, y, z float64
}

// arrowRemoval 是一支待移除的箭（原因用于选择音效）。
type arrowRemoval struct {
	id      int32
	dim     world.Dimension
	x, y, z float64
	block   bool
}

// tickArrows 推进全部箭矢：物理、方块碰撞、玩家命中与超时移除。
// 由 tick() 每帧调用。
func (s *Server) tickArrows(players []*session) {
	if !s.arrowsEnabled {
		return
	}
	type arrowMove struct {
		dim    world.Dimension
		packet []byte
		x, z   float64
	}
	var (
		moves    []arrowMove
		removals []arrowRemoval
		hits     []arrowHit
	)

	s.entityMu.Lock()
	for id, a := range s.arrows {
		a.AgeTicks++
		if a.AgeTicks > arrowLifetimeTicks {
			delete(s.arrows, id)
			removals = append(removals, arrowRemoval{id: id, dim: a.Dim, x: a.X, y: a.Y, z: a.Z})
			continue
		}
		w := s.worldFor(a.Dim)
		// 速度：重力 + 阻力。
		a.VelY -= arrowGravityPerTick
		a.VelX *= arrowDrag
		a.VelY *= arrowDrag
		a.VelZ *= arrowDrag

		startX, startY, startZ := a.X, a.Y, a.Z
		box := world.Box{
			MinX: a.X - 0.15, MinY: a.Y - 0.15, MinZ: a.Z - 0.15,
			MaxX: a.X + 0.15, MaxY: a.Y + 0.15, MaxZ: a.Z + 0.15,
		}
		hitBlock := false
		if clipped := w.ClipMove(box, 1, a.VelY); clipped != a.VelY {
			a.Y += clipped
			hitBlock = true
		} else {
			a.Y += a.VelY
		}
		box = world.Box{
			MinX: a.X - 0.15, MinY: a.Y - 0.15, MinZ: a.Z - 0.15,
			MaxX: a.X + 0.15, MaxY: a.Y + 0.15, MaxZ: a.Z + 0.15,
		}
		if clipped := w.ClipMove(box, 0, a.VelX); clipped != a.VelX {
			a.X += clipped
			hitBlock = true
		} else {
			a.X += a.VelX
		}
		box = world.Box{
			MinX: a.X - 0.15, MinY: a.Y - 0.15, MinZ: a.Z - 0.15,
			MaxX: a.X + 0.15, MaxY: a.Y + 0.15, MaxZ: a.Z + 0.15,
		}
		if clipped := w.ClipMove(box, 2, a.VelZ); clipped != a.VelZ {
			a.Z += clipped
			hitBlock = true
		} else {
			a.Z += a.VelZ
		}

		// 玩家命中（同维度，扩张碰撞盒包含箭尖）。
		hitPlayer := false
		for _, player := range players {
			if player.dimensionID() != a.Dim || !player.canBeAttacked() {
				continue
			}
			px, py, pz, _, _ := player.playerPosition()
			box := playerBox(px, py, pz)
			if a.X < box.MinX-arrowHitRadius || a.X > box.MaxX+arrowHitRadius ||
				a.Y < box.MinY-arrowHitRadius || a.Y > box.MaxY+arrowHitRadius ||
				a.Z < box.MinZ-arrowHitRadius || a.Z > box.MaxZ+arrowHitRadius {
				continue
			}
			hits = append(hits, arrowHit{player: player, damage: a.Damage, x: a.X, y: a.Y, z: a.Z})
			hitPlayer = true
			break
		}

		if hitPlayer || hitBlock || a.Y < float64(world.WorldMinY)-16 {
			delete(s.arrows, id)
			removals = append(removals, arrowRemoval{id: id, dim: a.Dim, x: a.X, y: a.Y, z: a.Z, block: hitBlock && !hitPlayer})
			continue
		}
		if a.X != startX || a.Y != startY || a.Z != startZ {
			moves = append(moves, arrowMove{
				dim: a.Dim,
				packet: protocol.EncodeEntityPositionSync(a.ID, a.X, a.Y, a.Z,
					a.VelX, a.VelY, a.VelZ, 0, 0, true),
				x: a.X, z: a.Z,
			})
		}
	}
	s.entityMu.Unlock()

	for _, move := range moves {
		s.broadcastToNearby(move.dim, move.packet, move.x, move.z, players)
	}
	for _, removal := range removals {
		s.broadcastPacket(protocol.EncodeEntityDestroy([]int32{removal.id}))
		sound := s.soundArrowHit
		if removal.block {
			sound = s.soundArrowHitBlock
		}
		s.broadcastToNearby(removal.dim,
			protocol.EncodeSoundEffect(sound, protocol.SoundCategoryNeutral, removal.x, removal.y, removal.z, 0.6, 1.2, 0),
			removal.x, removal.z, players)
	}
	for _, hit := range hits {
		position := [3]float64{hit.x, hit.y, hit.z}
		s.damagePlayer(hit.player, hit.damage, "an arrow", 0, s.arrowDamageTypeID, &position)
	}
}

// explode 结算一次爆炸：销毁方块、广播爆炸包与更新，并对玩家造成伤害与击退。
// 方块销毁为简化模型：无掉落、跳过爆炸抗性方块（见 explosionResistantBlocks）。
func (s *Server) explode(dim world.Dimension, x, y, z float64, radius float32, power float32, sourceName string) {
	w := s.worldFor(dim)
	baseX, baseY, baseZ := int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))
	rangeInt := int(math.Ceil(float64(radius)))
	radiusSquared := float64(radius) * float64(radius)
	var updates [][]byte
	destroyed := 0
	for dx := -rangeInt; dx <= rangeInt; dx++ {
		for dy := -rangeInt; dy <= rangeInt; dy++ {
			for dz := -rangeInt; dz <= rangeInt; dz++ {
				if float64(dx*dx+dy*dy+dz*dz) > radiusSquared {
					continue
				}
				bx, by, bz := baseX+dx, baseY+dy, baseZ+dz
				if _, ok := world.SectionIndex(by); !ok {
					continue
				}
				state := w.BlockAt(bx, by, bz)
				if state == world.AirBlock || state == world.WaterBlock || state == world.LavaBlock {
					continue
				}
				if name, ok := s.blockNames[state]; ok && explosionResistantBlocks[name] {
					continue
				}
				if !w.SetBlock(bx, by, bz, world.AirBlock) {
					continue
				}
				destroyed++
				updates = append(updates, protocol.EncodeBlockUpdate(bx, by, bz, int32(world.AirBlock)))
			}
		}
	}

	explosionPacket := protocol.EncodeExplosion(x, y, z, radius, int32(destroyed), protocol.ParticleExplosionEmitter)
	players := s.playerSnapshot()
	for _, player := range players {
		if player.dimensionID() != dim || !player.isJoined() {
			continue
		}
		px, _, pz, _, _ := player.playerPosition()
		if math.Hypot(px-x, pz-z) > mobMoveBroadcastRange {
			continue
		}
		player.tryWrite(explosionPacket)
		for _, update := range updates {
			player.tryWrite(update)
		}
	}

	// 伤害与击退：半径 2 倍内线性衰减。
	damageRadius := float64(radius) * 2
	for _, player := range players {
		if player.dimensionID() != dim || !player.canBeAttacked() {
			continue
		}
		px, py, pz, _, _ := player.playerPosition()
		distance := math.Hypot(math.Hypot(px-x, py-(y-0.5)), pz-z)
		if distance > damageRadius {
			continue
		}
		damage := float32(1-distance/damageRadius) * power
		if damage < 1 {
			continue
		}
		position := [3]float64{x, y, z}
		if !s.damagePlayer(player, damage, sourceName, 0, s.explosionDamageTypeID, &position) {
			continue
		}
		knockX, knockZ := px-x, pz-z
		norm := math.Hypot(knockX, knockZ)
		if norm < 1e-3 {
			knockX, knockZ, norm = 0, 1, 1
		}
		scale := creeperKnockback * float32(1-distance/damageRadius)
		player.tryWrite(protocol.EncodeEntityVelocity(player.entityID,
			float64(knockX/norm)*float64(scale), float64(scale)*0.5, float64(knockZ/norm)*float64(scale)))
	}
	// 范围内的生物同样受到爆炸伤害；掉落物被炸飞（不销毁）。
	s.damageMobsInExplosion(dim, x, y, z, float32(damageRadius), power)
	s.knockbackItemsInExplosion(dim, x, y, z, float32(damageRadius))
}

// damageMobsInExplosion 对爆炸范围内的生物结算伤害（按距离线性衰减），
// 死亡时走正常的死亡动画与掉落流程。
func (s *Server) damageMobsInExplosion(dim world.Dimension, x, y, z float64, damageRadius, power float32) {
	type result struct {
		dim     world.Dimension
		kind    mobKind
		packets [][]byte
		drop    bool
		lootX   float64
		lootY   float64
		lootZ   float64
	}
	var results []result
	s.entityMu.Lock()
	for _, m := range s.mobs {
		if m.Dim != dim || m.Dead {
			continue
		}
		distance := math.Hypot(math.Hypot(m.X-x, m.Y-(y-0.5)), m.Z-z)
		if distance > float64(damageRadius) {
			continue
		}
		damage := float32(1-distance/float64(damageRadius)) * power
		if damage < 1 {
			continue
		}
		position := [3]float64{x, y, z}
		m.Health -= damage
		packets := [][]byte{
			protocol.EncodeHurtAnimation(m.ID, m.Yaw),
			protocol.EncodeDamageEvent(m.ID, s.explosionDamageTypeID, 0, 0, &position),
			protocol.EncodeEntitySoundEffect(s.mobHurtSound(m.Kind), protocol.SoundCategoryHostile, m.ID, 1, 1, 0),
		}
		entry := result{dim: m.Dim, kind: m.Kind, packets: packets}
		if m.Health <= 0 {
			m.Health = 0
			m.Dead = true
			m.DeadTicks = 0
			entry.packets = append(entry.packets,
				protocol.EncodeEntityEvent(m.ID, protocol.EntityEventDeath),
				protocol.EncodeEntitySoundEffect(s.mobDeathSound(m.Kind), protocol.SoundCategoryHostile, m.ID, 1, 1, 0),
			)
			entry.drop = true
			entry.lootX, entry.lootY, entry.lootZ = m.X, m.Y, m.Z
		}
		results = append(results, entry)
	}
	s.entityMu.Unlock()
	for _, entry := range results {
		for _, packet := range entry.packets {
			s.broadcastToNearby(entry.dim, packet, x, z, s.playerSnapshot())
		}
		if entry.drop {
			s.dropMobLoot(entry.kind, entry.dim, entry.lootX, entry.lootY, entry.lootZ)
		}
	}
}

// knockbackItemsInExplosion 对爆炸范围内的掉落物施加击飞速度（不销毁物品）。
func (s *Server) knockbackItemsInExplosion(dim world.Dimension, x, y, z float64, damageRadius float32) {
	s.entityMu.Lock()
	defer s.entityMu.Unlock()
	for _, e := range s.items {
		if e.Dim != dim {
			continue
		}
		dx, dy, dz := e.X-x, e.Y-y, e.Z-z
		distance := math.Hypot(math.Hypot(dx, dy), dz)
		if distance > float64(damageRadius) || distance < 1e-3 {
			continue
		}
		scale := 0.4 * (1 - distance/float64(damageRadius))
		e.VelX += dx / distance * scale
		e.VelY += scale * 0.8
		e.VelZ += dz / distance * scale
	}
}
