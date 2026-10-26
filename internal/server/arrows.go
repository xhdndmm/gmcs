//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"log/slog"
	"math"

	"gmcs/internal/item"
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
	// OwnerID 是射出该箭的玩家实体 ID（0 = 生物/环境箭矢）。
	// 用于避免自伤、决定击杀经验归属。
	OwnerID int32
	// ShooterMobID 是射出该箭的生物实体 ID（0 = 玩家/环境箭矢）。
	// 扫掠命中会跳过射手自身（箭矢生成在射手碰撞盒内）。
	ShooterMobID int32
	// Knockback 是冲击附魔等级（命中生物时额外击退）。
	Knockback int32
	// Flame 表示火焰附魔（命中时点燃目标 5 秒）。
	Flame bool
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

// spawnArrow 创建一个飞行中的箭并广播 Add Entity。
// ownerID 为 0 表示生物/环境箭矢。
func (s *Server) spawnArrow(dim world.Dimension, x, y, z, vx, vy, vz float64,
	damage float32, ownerID int32, knockback int32, flame bool) *arrowEntity {
	if !s.arrowsEnabled {
		return nil
	}
	arrow := &arrowEntity{
		ID:        s.entityIDs.Add(1),
		UUID:      newEntityUUID(),
		Dim:       dim,
		X:         x,
		Y:         y,
		Z:         z,
		VelX:      vx,
		VelY:      vy,
		VelZ:      vz,
		Damage:    damage,
		OwnerID:   ownerID,
		Knockback: knockback,
		Flame:     flame,
	}
	s.entityMu.Lock()
	s.arrows[arrow.ID] = arrow
	targets := s.markTrackedNearbyLocked(dim, arrow.X, arrow.Z, nil, arrow.ID)
	s.entityMu.Unlock()

	add := protocol.EncodeAddEntity(arrow.ID, arrow.UUID, s.arrowTypeID,
		arrow.X, arrow.Y, arrow.Z, arrow.VelX, arrow.VelY, arrow.VelZ, 0, 0)
	for _, player := range targets {
		player.tryWrite(add)
	}
	return arrow
}

// fireArrow 生成一支由骷髅射出的箭，并广播生成包与射击音效。
func (s *Server) fireArrow(p pendingArrow) {
	dx, dy, dz := p.targetX-p.x, p.targetY-p.y, p.targetZ-p.z
	distance := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if distance < 1e-3 {
		distance = 1e-3
	}
	speed := skeletonArrowSpeed
	arrow := s.spawnArrow(p.dim, p.x, p.y, p.z,
		dx/distance*speed, dy/distance*speed+0.08, dz/distance*speed, p.damage, 0, 0, false)
	if arrow == nil {
		return
	}
	s.entityMu.Lock()
	arrow.ShooterMobID = p.mobID
	s.entityMu.Unlock()
	s.broadcastToNearby(p.dim,
		protocol.EncodeEntitySoundEffect(s.soundArrowShoot, protocol.SoundCategoryHostile, p.mobID, 1, 1.2, 0),
		arrow.X, arrow.Z, s.playerSnapshot())
}

// segmentIntersectsBox 报告线段 (x0,y0,z0)-(x1,y1,z1) 是否与轴对齐盒相交
// （slab 方法）。用于箭矢的扫掠命中判定：箭速可达 3 格/tick，只在终点
// 检测会直接穿过 0.6 格宽的生物。
func segmentIntersectsBox(x0, y0, z0, x1, y1, z1 float64, box world.Box) bool {
	tmin, tmax := 0.0, 1.0
	if !clipSlab(x1-x0, x0, box.MinX, box.MaxX, &tmin, &tmax) {
		return false
	}
	if !clipSlab(y1-y0, y0, box.MinY, box.MaxY, &tmin, &tmax) {
		return false
	}
	return clipSlab(z1-z0, z0, box.MinZ, box.MaxZ, &tmin, &tmax)
}

// clipSlab 用一条轴上的区间裁剪参数区间 [tmin, tmax]。
func clipSlab(d, origin, minValue, maxValue float64, tmin, tmax *float64) bool {
	if math.Abs(d) < 1e-12 {
		return origin >= minValue && origin <= maxValue
	}
	t1 := (minValue - origin) / d
	t2 := (maxValue - origin) / d
	if t1 > t2 {
		t1, t2 = t2, t1
	}
	*tmin = max(*tmin, t1)
	*tmax = min(*tmax, t2)
	return *tmin <= *tmax
}

// expandBox 返回盒向外扩张 radius 的副本。
func expandBox(box world.Box, radius float64) world.Box {
	return world.Box{
		MinX: box.MinX - radius, MinY: box.MinY - radius, MinZ: box.MinZ - radius,
		MaxX: box.MaxX + radius, MaxY: box.MaxY + radius, MaxZ: box.MaxZ + radius,
	}
}

// arrowHit 是一次待结算的箭矢玩家命中（位置为命中瞬间的快照）。
type arrowHit struct {
	player           *session
	damage           float32
	x, y, z          float64
	srcX, srcY, srcZ float64
	flame            bool
}

// arrowMobHit 是一次待结算的箭矢生物命中（env 持锁时收集，解锁后广播/掉落）。
type arrowMobHit struct {
	env         mobEnvironment
	ownerPlayer bool
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
		mobHits  []arrowMobHit
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

		// 生物命中（在玩家之前检查；跳过死亡生物）。
		hitMob := false
		for _, m := range s.mobs {
			if m.Dim != a.Dim || m.Dead || m.ID == a.ShooterMobID {
				continue
			}
			box := expandBox(mobBox(m.X, m.Y, m.Z), arrowHitRadius)
			if !segmentIntersectsBox(startX, startY, startZ, a.X, a.Y, a.Z, box) {
				continue
			}
			env := mobEnvironment{dim: a.Dim, kind: m.Kind, x: m.X, z: m.Z}
			if s.damageMob(m, a.Damage, s.arrowDamageTypeID, &env) {
				// 被动生物中箭后逃跑（原版 PanicGoal 由 hurt 设置 hurtBy）。
				if mobKinds[m.Kind].passive && !m.Dead {
					m.PanicTicks = mobPanicTicks
					m.PanicX, m.PanicZ = a.X, a.Z
				}
				if a.Flame && !m.Dead {
					if m.FireTicks < arrowFlameTicks {
						m.FireTicks = arrowFlameTicks
					}
				}
				if a.Knockback > 0 && !m.Dead {
					if packet := s.knockbackMob(m, m.X-a.VelX, m.Z-a.VelZ,
						mobKnockback+0.5*float64(a.Knockback)); packet != nil {
						env.packets = append(env.packets, packet)
					}
				}
				mobHits = append(mobHits, arrowMobHit{env: env, ownerPlayer: a.OwnerID != 0})
			}
			hitMob = true
			break
		}

		// 玩家命中（同维度，扩张碰撞盒包含箭尖；跳过射手本人）。
		hitPlayer := false
		if !hitMob {
			for _, player := range players {
				if player.dimensionID() != a.Dim || !player.canBeAttacked() || player.entityID == a.OwnerID {
					continue
				}
				px, py, pz, _, _ := player.playerPosition()
				box := expandBox(playerBox(px, py, pz), arrowHitRadius)
				if !segmentIntersectsBox(startX, startY, startZ, a.X, a.Y, a.Z, box) {
					continue
				}
				hits = append(hits, arrowHit{
					player: player, damage: a.Damage, x: a.X, y: a.Y, z: a.Z,
					srcX: a.X - a.VelX, srcY: a.Y - a.VelY, srcZ: a.Z - a.VelZ,
					flame: a.Flame,
				})
				hitPlayer = true
				break
			}
		}

		if hitMob || hitPlayer || hitBlock || a.Y < float64(world.WorldMinY)-16 {
			delete(s.arrows, id)
			removals = append(removals, arrowRemoval{id: id, dim: a.Dim, x: a.X, y: a.Y, z: a.Z, block: hitBlock && !hitMob && !hitPlayer})
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
		destroy := protocol.EncodeEntityDestroy([]int32{removal.id})
		for _, player := range players {
			if player.dimensionID() == removal.dim {
				player.tryWrite(destroy)
			}
		}
		sound := s.soundArrowHit
		if removal.block {
			sound = s.soundArrowHitBlock
		}
		s.broadcastToNearby(removal.dim,
			protocol.EncodeSoundEffect(sound, protocol.SoundCategoryNeutral, removal.x, removal.y, removal.z, 0.6, 1.2, 0),
			removal.x, removal.z, players)
	}
	for _, hit := range hits {
		position := [3]float64{hit.srcX, hit.srcY, hit.srcZ}
		if s.damagePlayer(hit.player, hit.damage, "an arrow", 0, s.arrowDamageTypeID, &position) && hit.flame {
			if hit.player.fireTicks.Load() < arrowFlameTicks {
				hit.player.fireTicks.Store(arrowFlameTicks)
			}
		}
	}
	for _, hit := range mobHits {
		for _, packet := range hit.env.packets {
			s.broadcastToNearby(hit.env.dim, packet, hit.env.x, hit.env.z, players)
		}
		if hit.env.drop {
			s.dropMobLoot(hit.env.kind, hit.env.dim, hit.env.lootX, hit.env.lootY, hit.env.lootZ, 0)
			if hit.ownerPlayer {
				s.spawnXPOrbs(hit.env.dim, hit.env.lootX, hit.env.lootY+0.5, hit.env.lootZ,
					mobKinds[hit.env.kind].experience)
			}
		}
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
	var destroyedCells [][3]int
	var destroyedStates []uint16
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
				if state == world.AirBlock || isWaterState(state) || isLavaState(state) {
					continue
				}
				if name, ok := s.blockNameOf(state); ok && explosionResistantBlocks[name] {
					continue
				}
				if !w.SetBlock(bx, by, bz, world.AirBlock) {
					continue
				}
				destroyed++
				destroyedCells = append(destroyedCells, [3]int{bx, by, bz})
				destroyedStates = append(destroyedStates, state)
				updates = append(updates, protocol.EncodeBlockUpdate(bx, by, bz, int32(world.AirBlock)))
				// 容器/处理型方块被炸毁：注销运行时状态、关闭观察窗口并把
				// 内容物掉落到世界（原版爆炸摧毁容器会掉落内容物）。
				s.destroyBlockByExplosion(dim, w, bx, by, bz, state)
			}
		}
	}
	// 爆炸改变了大量方块：通知周围的流体重新评估（注：邻域扇出可能很大，
	// 但仅调度计划刻、不在此处扩散），并重新判定被摧毁原木/树叶周围的树叶衰减。
	for i, cell := range destroyedCells {
		s.updateFluidsAround(dim, cell[0], cell[1], cell[2])
		s.decayLeavesAround(dim, w, cell[0], cell[1], cell[2], destroyedStates[i])
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

// destroyBlockByExplosion 处理爆炸摧毁的方块：注销处理型容器的运行时状态、
// 关闭打开中的窗口并把内容物掉落到世界（原版爆炸摧毁容器会掉落内容物；
// 此处不再写回方块实体，因为方块已被置为空气）。
func (s *Server) destroyBlockByExplosion(dim world.Dimension, w *world.World, x, y, z int, state uint16) {
	name, hasName := s.blockNameOf(state)
	if !hasName {
		return
	}
	def, isContainer := s.containerDefs[name]
	key := containerKey{dim: dim, x: x, y: y, z: z}
	var dropped []item.Stack
	stateHandled := false
	if isContainer {
		switch {
		case def.IsFurnace:
			s.furnaceMu.Lock()
			state := s.furnaces[key]
			delete(s.furnaces, key)
			s.furnaceMu.Unlock()
			if state != nil {
				state.mu.Lock()
				dropped = append(dropped, state.slots[:]...)
				for i := range state.slots {
					state.slots[i] = item.Empty()
				}
				state.mu.Unlock()
				stateHandled = true
			}
		case def.IsHopper:
			s.hopperMu.Lock()
			state := s.hoppers[key]
			delete(s.hoppers, key)
			s.hopperMu.Unlock()
			if state != nil {
				state.mu.Lock()
				dropped = append(dropped, state.slots[:]...)
				for i := range state.slots {
					state.slots[i] = item.Empty()
				}
				state.mu.Unlock()
				stateHandled = true
			}
		case def.IsBrewing:
			s.brewingMu.Lock()
			state := s.brewings[key]
			delete(s.brewings, key)
			s.brewingMu.Unlock()
			if state != nil {
				state.mu.Lock()
				dropped = append(dropped, state.slots[:]...)
				for i := range state.slots {
					state.slots[i] = item.Empty()
				}
				state.mu.Unlock()
				stateHandled = true
			}
		}
		// 打开中的窗口：标记关闭并通知观察者（内容随掉落物离开）。
		s.containerMu.Lock()
		container := s.containers[key]
		if s.containers[key] == container {
			delete(s.containers, key)
		}
		s.containerMu.Unlock()
		if container != nil {
			container.mu.Lock()
			container.closed = true
			if !stateHandled && container.furnace == nil && container.hopper == nil && container.brewing == nil {
				dropped = append(dropped, container.slots...)
				for i := range container.slots {
					container.slots[i] = item.Empty()
				}
				stateHandled = true
			}
			viewers := make([]*session, 0, len(container.viewers))
			for viewer := range container.viewers {
				viewers = append(viewers, viewer)
			}
			container.mu.Unlock()
			for _, viewer := range viewers {
				viewer.tryWrite(protocol.EncodeContainerClose(viewer.windowID))
			}
			s.broadcastContainerAction(container, false, nil)
		}
	}
	// 未由运行时状态接管的内容：读取方块实体。
	if !stateHandled {
		if entity, err := w.BlockEntityAt(x, y, z); err == nil {
			for _, slot := range entity.Items {
				if !slot.IsEmpty() {
					dropped = append(dropped, stackFromContainer(slot))
				}
			}
		}
	}
	if err := w.RemoveBlockEntity(x, y, z); err != nil {
		slog.Error("清除爆炸方块实体失败", "x", x, "y", y, "z", z, "error", err)
	}
	for _, stack := range dropped {
		if stack.IsEmpty() {
			continue
		}
		vx := (float64(s.nextRandom()%1000)/1000 - 0.5) * 0.2
		vz := (float64(s.nextRandom()%1000)/1000 - 0.5) * 0.2
		s.spawnItem(dim, stack, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, vx, 0.2, vz, itemPickupDelayPlayer)
	}
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
			s.dropMobLoot(entry.kind, entry.dim, entry.lootX, entry.lootY, entry.lootZ, 0)
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
