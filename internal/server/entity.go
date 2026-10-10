package server

import (
	"context"
	"crypto/rand"
	"log/slog"
	"math"
	"time"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 生物与战斗参数。数值以“简单、可验证”为先，不追求与原版数值完全一致。
const (
	// zombieTypeName 是僵尸的类型名（兼容旧测试与持久化）。
	zombieTypeName  = "minecraft:zombie"
	mobWanderSpeed  = 0.03
	mobDespawnRange = 64 // 存在玩家时，距离所有玩家都超过该距离的生物会被移除
	mobAttackRange  = 1.9
	// mobAttackVerticalRange 是攻击允许的高度差（超出则打不到）。
	mobAttackVerticalRange = 2.0
	// mobAttackWindupTicks 是进入攻击距离后的抬手时间（0.5 秒），
	// 避免“贴脸瞬间受伤”。
	mobAttackWindupTicks = 10
	mobAttackCooldown    = 20
	// mobHurtCooldownTicks 是生物受击后的无敌帧（0.5 秒）。
	mobHurtCooldownTicks = 10
	// mobKnockback 是生物被击中时沿攻击者反方向推开的距离（方块）。
	mobKnockback  = 0.4
	mobDeathTicks = 20
	// mobEyeHeight 是近似眼睛高度（视线检查用）。
	mobEyeHeight = 1.5

	// 骷髅：保持距离射箭。
	skeletonMinRange      = 3.0
	skeletonMaxRange      = 16.0
	skeletonShootCooldown = 40
	skeletonArrowSpeed    = 1.6
	skeletonArrowDamage   = 2

	// 苦力怕：引信与爆炸。
	creeperTriggerRange    = 3.0
	creeperResetRange      = 6.0
	creeperFuseTicks       = 30
	creeperExplosionRadius = 3.0
	// creeperExplosionPower 是爆心最大伤害（按距离线性衰减）。
	creeperExplosionPower = 24
	// creeperKnockback 是爆炸对玩家的水平击退速度。
	creeperKnockback = 0.8

	// 箭矢物理。
	arrowGravityPerTick = 0.05
	arrowDrag           = 0.99
	arrowLifetimeTicks  = 200
	arrowHitRadius      = 0.5

	playerAttackDamage = 4
	playerAttackRange  = 3.5
	playerHurtCooldown = 500 * time.Millisecond
	// playerCritMultiplier 是跳跃暴击的伤害倍率（原版 1.5 倍）。
	playerCritMultiplier = 1.5

	// 玩家状态常量与饥饿系统参数（与原版普通难度一致）。
	maxPlayerHealth  = 20
	maxPlayerFood    = 20
	playerSaturation = 5
	// playerFoodTickInterval 是自然恢复/饥饿伤害的结算间隔（4 秒）。
	playerFoodTickInterval = 80
	// playerRegenFoodThreshold 是自然恢复所需的最低饥饿值（原版 18）。
	playerRegenFoodThreshold = 18
	// playerExhaustionPerFood 是每消耗 1 点饥饿值所需的疲劳度（原版 4.0）。
	playerExhaustionPerFood = 4.0
	// playerExhaustionSprint 是每格疾跑产生的疲劳度（原版 0.1）。
	playerExhaustionSprint = 0.1
	// playerExhaustionJump 是每次跳跃产生的疲劳度（原版 0.05，疾跑跳跃 0.2）。
	playerExhaustionJump = 0.05
	// playerExhaustionAttack 是每次攻击产生的疲劳度（原版 0.1）。
	playerExhaustionAttack = 0.1
	// playerExhaustionDamage 是每次受伤产生的疲劳度（原版 0.1）。
	playerExhaustionDamage = 0.1
	// playerExhaustionRegen 是自然恢复 1 点生命消耗的疲劳度（原版 6.0）。
	playerExhaustionRegen = 6.0
	// playerSaturationPerRegen 是自然恢复 1 点生命优先消耗的饱食度。
	playerSaturationPerRegen = 1.0

	// spawnCheckInterval 是生成检查的间隔（tick，20 TPS 下为 5 秒）。
	spawnCheckInterval = 100
	// mobMoveBroadcastRange 是生物移动/头部朝向的广播半径（只发送给范围内的玩家）。
	mobMoveBroadcastRange = 64
	// mobStuckTicks 是追击/游荡被挡住多少次后触发随机侧移（简单避障）。
	mobStuckTicks = 40
	// mobSideStep 是避障侧移的步长（方块）。
	mobSideStep = 0.4
	// mobHeadYawThreshold 是发送 Rotate Head 的最小转角（度）。
	mobHeadYawThreshold = 20

	// 世界时间：24000 tick 为一天（20 分钟）；怪物只在夜间生成。
	// worldNightStart/worldNightEnd 是夜晚的 tick 区间（与原版一致）。
	worldDayLength  = 24000
	worldNightStart = 13000
	worldNightEnd   = 23000
	// worldTimeBroadcastInterval 是时间同步包的发送间隔（tick）。
	worldTimeBroadcastInterval = 20

	// mobDropPickupDelay 是生物掉落物的拾取延迟（与原版挖掘掉落一致）。
	mobDropPickupDelay = 10
	// mobRareDropChance 是稀有掉落（铁锭/胡萝卜/土豆）的概率（1/40，近似原版）。
	mobRareDropChance = 40

	// mobAutosaveInterval 是生物数据的自动保存间隔。
	mobAutosaveInterval = 30 * time.Second
)

// mobKind 是服务器支持的生物种类。
type mobKind uint8

// 生物种类常量。
const (
	mobZombie mobKind = iota
	mobSkeleton
	mobCreeper
	mobSpider
)

// mobStats 描述一种生物的基础数值与音效名。
type mobStats struct {
	// name 是静态注册表 minecraft:entity_type 的条目名。
	name string
	// health 是最大生命（也是生成时的生命值）。
	health float32
	// walkSpeed 是追击速度（方块/tick）。
	walkSpeed float64
	// followRange 是索敌半径（方块）。
	followRange float64
	// damage 是近战伤害（骷髅使用箭矢伤害）。
	damage float32
	// experience 是击杀获得的经验值。
	experience int32
	// hurtSound/deathSound 是受击/死亡音效的注册表条目名。
	hurtSound  string
	deathSound string
	// spawnWeight 是生成权重（0 表示不参与自然生成）。
	spawnWeight int
}

// mobKinds 是全部生物的基础数值（生命 20、伤害 2–3，与原版普通难度接近）。
var mobKinds = map[mobKind]mobStats{
	mobZombie: {
		name: "minecraft:zombie", health: 20, walkSpeed: 0.055, followRange: 32, damage: 2,
		experience: 5, hurtSound: "minecraft:entity.zombie.hurt", deathSound: "minecraft:entity.zombie.death",
		spawnWeight: 40,
	},
	mobSkeleton: {
		name: "minecraft:skeleton", health: 20, walkSpeed: 0.05, followRange: 24, damage: skeletonArrowDamage,
		experience: 5, hurtSound: "minecraft:entity.skeleton.hurt", deathSound: "minecraft:entity.skeleton.death",
		spawnWeight: 25,
	},
	mobCreeper: {
		name: "minecraft:creeper", health: 20, walkSpeed: 0.05, followRange: 24, damage: 0,
		experience: 5, hurtSound: "minecraft:entity.creeper.hurt", deathSound: "minecraft:entity.creeper.death",
		spawnWeight: 25,
	},
	mobSpider: {
		name: "minecraft:spider", health: 16, walkSpeed: 0.08, followRange: 24, damage: 2,
		experience: 5, hurtSound: "minecraft:entity.spider.hurt", deathSound: "minecraft:entity.spider.death",
		spawnWeight: 10,
	},
}

// mob 是一只服务器控制的生物。字段由 Server.entityMu 保护，
// 只在 Tick 与攻击处理中修改。
type mob struct {
	ID     int32
	UUID   [16]byte
	TypeID int32
	Kind   mobKind
	// Dim 是该生物所在维度（生成后不变）。
	Dim world.Dimension

	X, Y, Z    float64
	Yaw, Pitch float32
	// HeadYaw 是最近一次发送给客户端的头部朝向（用于控制 Rotate Head 频率）。
	HeadYaw float32

	Health float32
	// Dead 为 true 时播放死亡动画，DeadTicks 到达上限后移除。
	Dead      bool
	DeadTicks int

	AttackCooldown int
	// WasInRange 记录上一帧是否处于可攻击状态（用于进入攻击距离时的抬手）。
	WasInRange bool
	// HurtCooldown 是受击无敌帧剩余 tick。
	HurtCooldown int
	// StuckTicks 记录被挡住而无法移动的 tick 数（触发侧移避障）。
	StuckTicks int
	// WanderX/WanderZ 是游荡目标；WanderTicks 归零时重新选择。
	WanderX, WanderZ float64
	WanderTicks      int
	// Fuse 是苦力怕引信剩余 tick（>0 表示已点燃）。
	Fuse int
	// ShootCooldown 是骷髅的射击冷却（tick）。
	ShootCooldown int
}

// pendingAttack 是一次待结算的生物攻击（位置为判定时的快照）。
type pendingAttack struct {
	mobID   int32
	dim     world.Dimension
	player  *session
	damage  float32
	name    string
	x, y, z float64
}

// pendingArrow 是一次待发射的骷髅箭矢（位置/目标为判定时的快照）。
type pendingArrow struct {
	mobID   int32
	dim     world.Dimension
	player  *session
	x, y, z float64
	targetX float64
	targetY float64
	targetZ float64
	damage  float32
}

// pendingPriming 是苦力怕点燃引信时的事件（播放 priming 音效）。
type pendingPriming struct {
	mobID   int32
	dim     world.Dimension
	x, y, z float64
}

// pendingExplosion 是一次待结算的爆炸（苦力怕引信结束）。
type pendingExplosion struct {
	mobID   int32
	dim     world.Dimension
	x, y, z float64
}

// playerSnapshot 返回在线玩家列表快照。
func (s *Server) playerSnapshot() []*session {
	s.mu.Lock()
	players := make([]*session, 0, len(s.players))
	for _, player := range s.players {
		players = append(players, player)
	}
	s.mu.Unlock()
	return players
}

// tick 推进一帧世界逻辑（玩家生命恢复、生物 AI、生成与清理、掉落物、世界时间）。
// 由 tickLoop 按 tickInterval 调用，测试中可直接调用以获得确定性。
func (s *Server) tick() {
	players := s.playerSnapshot()
	s.tickHunger(players)
	s.tickMobs(players)
	s.tickArrows(players)
	s.tickItems(players)
	s.tickWorldTime(players)
	s.tickFurnaces()
	s.tickRedstone()
	s.spawnTicks++
	if s.spawnTicks >= spawnCheckInterval {
		s.spawnTicks = 0
		s.trySpawnMob(players)
	}
}

// tickHunger 推进玩家的饥饿系统并把变化同步给客户端。
func (s *Server) tickHunger(players []*session) {
	for _, player := range players {
		if !player.isJoined() {
			continue
		}
		status := player.applyHungerTick()
		if status.changed {
			player.tryWrite(protocol.EncodeUpdateHealth(status.health, status.food, status.saturation))
		}
	}
}

// tickWorldTime 推进世界时间并按间隔广播（首个玩家加入时也会单独发送）。
func (s *Server) tickWorldTime(players []*session) {
	dayTime := s.advanceWorldTime()
	if dayTime%worldTimeBroadcastInterval != 0 {
		return
	}
	packet := protocol.EncodeUpdateTime(s.worldAge.Load(), dayTime, true)
	for _, player := range players {
		if player.isJoined() {
			player.tryWrite(packet)
		}
	}
}

// advanceWorldTime 推进一帧世界时间并返回当前时刻（0–23999）。
func (s *Server) advanceWorldTime() int64 {
	age := s.worldAge.Add(1)
	return age % worldDayLength
}

// isNight 报告服务器当前是否处于夜晚（怪物只在夜晚生成）。
func (s *Server) isNight() bool {
	dayTime := s.worldAge.Load() % worldDayLength
	return dayTime >= worldNightStart && dayTime < worldNightEnd
}

// tickMobs 更新全部生物：追击/游荡、攻击玩家、清理死亡与远离的生物。
func (s *Server) tickMobs(players []*session) {
	if !s.mobsEnabled {
		return
	}
	type pendingMove struct {
		id         int32
		dim        world.Dimension
		x, y, z    float64
		yaw, pitch float32
	}
	var (
		attacks    []pendingAttack
		arrows     []pendingArrow
		priming    []pendingPriming
		explosions []pendingExplosion
		moves      []pendingMove
		heads      []pendingMove
		removals   []int32
	)

	s.entityMu.Lock()
	for id, m := range s.mobs {
		if m.Dead {
			m.DeadTicks++
			if m.DeadTicks >= mobDeathTicks {
				delete(s.mobs, id)
				removals = append(removals, id)
			}
			continue
		}

		// 寻找最近的玩家（含旁观者，用于反弃用检查）与最近的可攻击玩家（AI 目标）。
		// 只有同维度的玩家可见/可攻击该生物。
		var (
			nearest         *session
			nearestDistance = math.MaxFloat64
			nearestPlayer   *session
			playerDistance  = math.MaxFloat64
		)
		for _, player := range players {
			if player.dimensionID() != m.Dim {
				continue
			}
			px, _, pz, _, _ := player.playerPosition()
			distance := math.Hypot(px-m.X, pz-m.Z)
			if distance < playerDistance {
				playerDistance = distance
				nearestPlayer = player
			}
			if player.canBeAttacked() && distance < nearestDistance {
				nearestDistance = distance
				nearest = player
			}
		}
		// 反弃用（despawn）：与原版一致，只有在服务器存在玩家时才按距离删除生物；
		// 完全没有玩家时保留生物，保证持久化的生物不会在空闲服务器上被清空。
		if nearestPlayer != nil && playerDistance > mobDespawnRange {
			delete(s.mobs, id)
			removals = append(removals, id)
			continue
		}

		attemptedMove := false
		moved := false
		stats := mobKinds[m.Kind]
		mobWorld := s.worldFor(m.Dim)
		if nearest != nil && nearestDistance <= stats.followRange {
			px, py, pz, _, _ := nearest.playerPosition()
			switch m.Kind {
			case mobSkeleton:
				clear := attackPathClear(mobWorld, m.X, m.Y+mobEyeHeight, m.Z, px, py+mobEyeHeight, pz)
				switch {
				case clear && nearestDistance >= skeletonMinRange && nearestDistance <= skeletonMaxRange:
					// 在射程内且视线通畅：站定射击（转向目标）。
					m.Yaw = float32(math.Atan2(-(px-m.X), pz-m.Z) * 180 / math.Pi)
					if m.ShootCooldown > 0 {
						m.ShootCooldown--
					}
					if m.ShootCooldown <= 0 {
						m.ShootCooldown = skeletonShootCooldown
						arrows = append(arrows, pendingArrow{
							mobID: m.ID, dim: m.Dim, player: nearest,
							x: m.X, y: m.Y + mobEyeHeight, z: m.Z,
							targetX: px, targetY: py + 1.0, targetZ: pz, damage: stats.damage,
						})
					}
				case clear && nearestDistance < skeletonMinRange:
					// 玩家贴脸：后退保持距离。
					attemptedMove = true
					moved = s.moveMobAway(mobWorld, m, px, pz, stats.walkSpeed)
				default:
					attemptedMove = true
					moved = s.moveMobToward(mobWorld, m, px, pz, stats.walkSpeed)
				}
			case mobCreeper:
				clear := nearestDistance <= creeperResetRange && math.Abs(py-m.Y) < 3 &&
					attackPathClear(mobWorld, m.X, m.Y+mobEyeHeight, m.Z, px, py+mobEyeHeight, pz)
				if clear && nearestDistance <= creeperTriggerRange {
					if m.Fuse == 0 {
						priming = append(priming, pendingPriming{mobID: m.ID, dim: m.Dim, x: m.X, y: m.Y, z: m.Z})
					}
					m.Fuse++
					if m.Fuse >= creeperFuseTicks {
						// 引信结束：从表中移除并在锁外结算爆炸。
						delete(s.mobs, id)
						removals = append(removals, id)
						explosions = append(explosions, pendingExplosion{
							mobID: m.ID, dim: m.Dim, x: m.X, y: m.Y + 0.5, z: m.Z,
						})
					}
				} else {
					m.Fuse = 0
					attemptedMove = true
					moved = s.moveMobToward(mobWorld, m, px, pz, stats.walkSpeed)
				}
			default:
				// 僵尸/蜘蛛：抬手近战。
				inAttack := nearestDistance <= mobAttackRange && math.Abs(py-m.Y) < mobAttackVerticalRange
				clear := inAttack && attackPathClear(mobWorld, m.X, m.Y+mobEyeHeight, m.Z, px, py+mobEyeHeight, pz)
				if clear && !m.WasInRange && m.AttackCooldown < mobAttackWindupTicks {
					// 刚进入攻击距离：先抬手 0.5 秒再出手。
					m.AttackCooldown = mobAttackWindupTicks
				}
				m.WasInRange = clear
				if clear && m.AttackCooldown <= 0 {
					m.AttackCooldown = mobAttackCooldown
					attacks = append(attacks, pendingAttack{
						mobID: m.ID, dim: m.Dim, player: nearest, damage: stats.damage,
						name: stats.name, x: m.X, y: m.Y, z: m.Z,
					})
				} else if !clear {
					attemptedMove = true
					moved = s.moveMobToward(mobWorld, m, px, pz, stats.walkSpeed)
				}
			}
		} else {
			m.WanderTicks--
			if m.WanderTicks <= 0 {
				m.WanderTicks = 40 + int(s.nextRandom()%80)
				angle := float64(s.nextRandom()%6283185) / 1000000
				m.WanderX = m.X + math.Cos(angle)*6
				m.WanderZ = m.Z + math.Sin(angle)*6
			}
			attemptedMove = true
			moved = s.moveMobToward(mobWorld, m, m.WanderX, m.WanderZ, mobWanderSpeed)
		}

		if moved {
			m.StuckTicks = 0
		} else if attemptedMove {
			// 被挡住：积累一段时间后随机侧移一步（简单避障，无寻路）。
			m.StuckTicks++
			if m.StuckTicks >= mobStuckTicks {
				m.StuckTicks = 0
				moved = s.trySideStep(mobWorld, m)
			}
		}

		if m.HurtCooldown > 0 {
			m.HurtCooldown--
		}
		if m.AttackCooldown > 0 {
			m.AttackCooldown--
		}
		if moved {
			moves = append(moves, pendingMove{id: m.ID, dim: m.Dim, x: m.X, y: m.Y, z: m.Z, yaw: m.Yaw, pitch: m.Pitch})
			if absAngleDelta(m.Yaw, m.HeadYaw) >= mobHeadYawThreshold {
				m.HeadYaw = m.Yaw
				heads = append(heads, pendingMove{id: m.ID, dim: m.Dim, x: m.X, y: m.Y, z: m.Z, yaw: m.Yaw, pitch: m.Pitch})
			}
		}
	}
	s.entityMu.Unlock()

	// 池缓冲构造移动包：同一缓冲顺序复用，广播是同步写，完成即归还。
	buf := packetBufferPool.Get().(*[]byte)
	for _, move := range moves {
		packet := protocol.AppendEntityPositionSync((*buf)[:0],
			move.id, move.x, move.y, move.z, 0, 0, 0, move.yaw, move.pitch, true)
		*buf = packet
		s.broadcastToNearby(move.dim, packet, move.x, move.z, players)
	}
	for _, head := range heads {
		packet := protocol.AppendEntityHeadRotation((*buf)[:0], head.id, head.yaw)
		*buf = packet
		s.broadcastToNearby(head.dim, packet, head.x, head.z, players)
	}
	packetBufferPool.Put(buf)
	if len(removals) > 0 {
		s.broadcastPacket(protocol.EncodeEntityDestroy(removals))
	}
	for _, prime := range priming {
		s.broadcastPacket(protocol.EncodeEntitySoundEffect(s.soundCreeperPrime, protocol.SoundCategoryHostile, prime.mobID, 1, 1, 0))
	}
	for _, attack := range attacks {
		s.performMobAttack(attack)
	}
	for _, arrow := range arrows {
		s.fireArrow(arrow)
	}
	for _, explosion := range explosions {
		s.explode(explosion.dim, explosion.x, explosion.y, explosion.z, creeperExplosionRadius, creeperExplosionPower, "Creeper")
	}
}

// broadcastToNearby 把数据包发送给指定维度内 (x, z) 半径
// mobMoveBroadcastRange 内的玩家。
func (s *Server) broadcastToNearby(dim world.Dimension, packet []byte, x, z float64, players []*session) {
	for _, player := range players {
		if player.dimensionID() != dim {
			continue
		}
		px, _, pz, _, _ := player.playerPosition()
		if math.Hypot(px-x, pz-z) <= mobMoveBroadcastRange {
			player.tryWrite(packet)
		}
	}
}

// performMobAttack 让生物攻击玩家：先广播挥手动画，再结算伤害，
// 使玩家能看到攻击动作而不是“凭空掉血”。位置取攻击判定时的快照，
// 不在锁外修改生物状态（避免与读循环的攻击处理并发）。
func (s *Server) performMobAttack(attack pendingAttack) {
	s.broadcastToNearby(attack.dim, protocol.EncodeAnimate(attack.mobID, 0), attack.x, attack.z, s.playerSnapshot())
	position := [3]float64{attack.x, attack.y + 1, attack.z}
	s.damagePlayer(attack.player, attack.damage, attack.name, attack.mobID, s.mobAttackDamageTypeID, &position)
}

// absAngleDelta 返回两个角度（度）之间的最小差值（0–180）。
func absAngleDelta(a, b float32) float32 {
	difference := math.Mod(float64(a-b), 360)
	if difference < 0 {
		difference += 360
	}
	if difference > 180 {
		difference = 360 - difference
	}
	return float32(difference)
}

// moveMobToward 让生物朝目标水平移动一步；路径被水或高低差挡住时返回 false。
// 会同步更新朝向。调用方必须持有 entityMu。
func (s *Server) moveMobToward(w *world.World, m *mob, targetX, targetZ, speed float64) bool {
	dx, dz := targetX-m.X, targetZ-m.Z
	distance := math.Hypot(dx, dz)
	if distance < 0.05 {
		return false
	}
	stepX := dx / distance * speed
	stepZ := dz / distance * speed
	if stepX == 0 && stepZ == 0 {
		return false
	}
	move := func(offsetX, offsetZ float64) bool {
		if offsetX == 0 && offsetZ == 0 {
			return false
		}
		if !s.tryMoveMob(w, m, m.X+offsetX, m.Z+offsetZ) {
			return false
		}
		m.Yaw = float32(math.Atan2(-dx, dz) * 180 / math.Pi)
		return true
	}
	if move(stepX, stepZ) {
		return true
	}
	// 被挡住时尝试沿单轴滑动。
	return move(stepX, 0) || move(0, stepZ)
}

// tryMoveMob 尝试把生物移动到指定位置：校验边界、水面与地面高度差，
// 成功时更新位置。调用方必须持有 entityMu。
func (s *Server) tryMoveMob(w *world.World, m *mob, newX, newZ float64) bool {
	if !s.insideBorder(newX, newZ) {
		return false
	}
	blockX, blockZ := int(math.Floor(newX)), int(math.Floor(newZ))
	column, ok := w.ColumnAt(blockX, blockZ)
	if !ok || !column.HasTop || column.TopState == world.WaterBlock || !column.HasSolid {
		return false
	}
	groundY := float64(column.SolidY + 1)
	if math.Abs(groundY-m.Y) > 1.1 {
		return false
	}
	m.X, m.Y, m.Z = newX, groundY, newZ
	return true
}

// trySideStep 让被挡住的生物随机侧移一步（简单避障，无寻路）。
// 调用方必须持有 entityMu。
func (s *Server) trySideStep(w *world.World, m *mob) bool {
	offsets := [4][2]float64{
		{mobSideStep, 0}, {-mobSideStep, 0}, {0, mobSideStep}, {0, -mobSideStep},
	}
	start := int(s.nextRandom() % 4)
	for i := 0; i < len(offsets); i++ {
		offset := offsets[(start+i)%len(offsets)]
		if s.tryMoveMob(w, m, m.X+offset[0], m.Z+offset[1]) {
			m.Yaw = float32(math.Atan2(-offset[0], offset[1]) * 180 / math.Pi)
			return true
		}
	}
	return false
}

// attackPathClear 粗略检查两点（眼睛高度）之间是否被方块挡住：
// 采样 35% 与 70% 处的方块，空气与水视为可穿过。
// 用于避免隔墙攻击（生物打玩家与玩家打生物共用）。
func attackPathClear(w *world.World, fromX, fromY, fromZ, toX, toY, toZ float64) bool {
	for _, t := range []float64{0.35, 0.7} {
		x := int(math.Floor(fromX + (toX-fromX)*t))
		y := int(math.Floor(fromY + (toY-fromY)*t))
		z := int(math.Floor(fromZ + (toZ-fromZ)*t))
		state := w.BlockAt(x, y, z)
		if state != world.AirBlock && state != world.WaterBlock {
			return false
		}
	}
	return true
}

// knockbackMob 把生物沿远离攻击者的方向推开一小段距离（简单击退）。
// 调用方必须持有 entityMu；返回位置同步包（无法移动时为 nil）。
func (s *Server) knockbackMob(m *mob, fromX, fromZ float64) []byte {
	dx, dz := m.X-fromX, m.Z-fromZ
	distance := math.Hypot(dx, dz)
	if distance < 1e-6 {
		return nil
	}
	newX := m.X + dx/distance*mobKnockback
	newZ := m.Z + dz/distance*mobKnockback
	if !s.insideBorder(newX, newZ) {
		return nil
	}
	w := s.worldFor(m.Dim)
	blockX, blockZ := int(math.Floor(newX)), int(math.Floor(newZ))
	column, ok := w.ColumnAt(blockX, blockZ)
	if !ok || !column.HasTop || column.TopState == world.WaterBlock || !column.HasSolid {
		return nil
	}
	groundY := float64(column.SolidY + 1)
	if math.Abs(groundY-m.Y) > 1.1 {
		return nil
	}
	m.X, m.Y, m.Z = newX, groundY, newZ
	return protocol.EncodeEntityPositionSync(m.ID, m.X, m.Y, m.Z, 0, 0, 0, m.Yaw, m.Pitch, true)
}

// trySpawnMob 在随机玩家附近尝试生成一只生物。
// 与原版一致：敌对生物只在夜晚生成（本服务器无光照引擎，因此不区分亮度），
// 且不在玩家附近近距离生成。末地不生成（无末影人实现）；下界与主世界相同。
func (s *Server) trySpawnMob(players []*session) {
	if !s.mobsEnabled || !s.config.SpawnMonsters || s.config.MaxMobs <= 0 || len(players) == 0 {
		return
	}
	if !s.isNight() {
		return
	}
	s.entityMu.Lock()
	count := len(s.mobs)
	s.entityMu.Unlock()
	if count >= s.config.MaxMobs {
		return
	}

	player := players[s.nextRandom()%uint64(len(players))]
	dim := player.dimensionID()
	if dim == world.DimensionEnd {
		return // 末地没有可生成的敌对生物（待末影人实现）
	}
	kind, ok := s.pickSpawnKind()
	if !ok {
		return
	}
	px, _, pz, _, _ := player.playerPosition()
	angle := float64(s.nextRandom()%6283185) / 1000000
	// 与原版一致：距玩家 24 格外生成。
	distance := 24 + float64(s.nextRandom()%1200)/100
	blockX := int(math.Floor(px + math.Cos(angle)*distance))
	blockZ := int(math.Floor(pz + math.Sin(angle)*distance))
	if !s.insideBorder(float64(blockX)+0.5, float64(blockZ)+0.5) {
		return
	}

	w := s.worldFor(dim)
	column, ok := w.ColumnAt(blockX, blockZ)
	if !ok || !column.HasTop || column.TopState == world.WaterBlock || !column.HasSolid {
		return
	}
	if column.TopState == world.LavaBlock || column.TopState == world.MagmaBlock {
		return
	}
	s.addMob(dim, kind, float64(blockX)+0.5, float64(column.SolidY+1), float64(blockZ)+0.5)
}

// pickSpawnKind 按权重挑选一种可生成的生物（僵尸/骷髅/苦力怕/蜘蛛）；
// 无可用种类时返回 false。
func (s *Server) pickSpawnKind() (mobKind, bool) {
	kinds := []mobKind{mobZombie, mobSkeleton, mobCreeper, mobSpider}
	total := 0
	for _, kind := range kinds {
		if _, ok := s.mobTypeIDs[kind]; !ok {
			continue
		}
		total += mobKinds[kind].spawnWeight
	}
	if total <= 0 {
		return mobZombie, false
	}
	roll := int(s.nextRandom() % uint64(total))
	for _, kind := range kinds {
		if _, ok := s.mobTypeIDs[kind]; !ok {
			continue
		}
		weight := mobKinds[kind].spawnWeight
		if roll < weight {
			return kind, true
		}
		roll -= weight
	}
	return mobZombie, false
}

// addMob 生成一只指定种类的生物并把 Add Entity 广播给同维度的附近玩家。
func (s *Server) addMob(dim world.Dimension, kind mobKind, x, y, z float64) *mob {
	stats, ok := mobKinds[kind]
	typeID, typeOK := s.mobTypeIDs[kind]
	if !ok || !typeOK {
		return nil
	}
	m := &mob{
		ID:     s.entityIDs.Add(1),
		UUID:   newEntityUUID(),
		TypeID: typeID,
		Kind:   kind,
		Dim:    dim,
		X:      x,
		Y:      y,
		Z:      z,
		Health: stats.health,
	}
	s.entityMu.Lock()
	s.mobs[m.ID] = m
	s.entityMu.Unlock()
	s.broadcastToNearby(dim, protocol.EncodeAddEntity(m.ID, m.UUID, m.TypeID, m.X, m.Y, m.Z, 0, 0, 0, m.Yaw, m.Pitch), m.X, m.Z, s.playerSnapshot())
	slog.Debug("mob spawned", "id", m.ID, "kind", stats.name, "x", m.X, "y", m.Y, "z", m.Z)
	return m
}

// sendExistingEntities 把同维度中已有的生物与掉落物发送给新加入的玩家。
func (s *Server) sendExistingEntities(player *session) error {
	dim := player.dimensionID()
	s.entityMu.Lock()
	packets := make([][]byte, 0, len(s.mobs)+2*len(s.items))
	for _, m := range s.mobs {
		if m.Dead || m.Dim != dim {
			continue
		}
		packets = append(packets, protocol.EncodeAddEntity(
			m.ID, m.UUID, m.TypeID, m.X, m.Y, m.Z, 0, 0, 0, m.Yaw, m.Pitch))
	}
	for _, e := range s.items {
		if e.Dim != dim {
			continue
		}
		packets = append(packets,
			protocol.EncodeAddEntity(e.ID, e.UUID, s.itemTypeID, e.X, e.Y, e.Z, e.VelX, e.VelY, e.VelZ, 0, 0),
			protocol.EncodeEntityMetadataItem(e.ID, e.Stack.AppendSlot(nil)),
		)
	}
	if s.arrowsEnabled {
		for _, a := range s.arrows {
			if a.Dim != dim {
				continue
			}
			packets = append(packets, protocol.EncodeAddEntity(
				a.ID, a.UUID, s.arrowTypeID, a.X, a.Y, a.Z, a.VelX, a.VelY, a.VelZ, 0, 0))
		}
	}
	s.entityMu.Unlock()
	for _, packet := range packets {
		if err := player.writePacket(packet); err != nil {
			return err
		}
	}
	return nil
}

// handleAttack 处理玩家攻击实体（Interact 包的 attack 动作）。
func (s *Server) handleAttack(player *session, targetID int32) {
	if player.isDead() {
		return
	}
	if player.gameModeID() == uint8(config.GameModeSpectator) {
		return // 旁观模式不能攻击。
	}
	px, py, pz, _, _ := player.playerPosition()

	s.entityMu.Lock()
	m := s.mobs[targetID]
	if m == nil || m.Dead {
		s.entityMu.Unlock()
		// 目标不是生物：可能是其他玩家实体（玩家间战斗）。
		s.handlePlayerAttack(player, targetID, px, py, pz)
		return
	}
	if m.Dim != player.dimensionID() {
		s.entityMu.Unlock()
		return // 不同维度：忽略。
	}
	if math.Hypot(px-m.X, pz-m.Z) > playerAttackRange || math.Abs(py-m.Y) > 3 {
		s.entityMu.Unlock()
		return // 超出攻击距离：忽略（简单的服务端校验）。
	}
	if !attackPathClear(s.worldFor(m.Dim), px, py+mobEyeHeight, pz, m.X, m.Y+mobEyeHeight, m.Z) {
		s.entityMu.Unlock()
		return // 隔墙攻击：忽略。
	}
	if m.HurtCooldown > 0 {
		s.entityMu.Unlock()
		return // 受击无敌帧内：忽略。
	}
	m.HurtCooldown = mobHurtCooldownTicks
	damage := float32(playerAttackDamage)
	// 跳跃暴击：下落中攻击造成 1.5 倍伤害（与原版一致）。
	if player.isFalling() {
		damage *= playerCritMultiplier
	}
	m.Health -= damage
	kind := m.Kind
	m.Yaw = float32(math.Atan2(-(px-m.X), pz-m.Z) * 180 / math.Pi)
	position := [3]float64{m.X, m.Y + 1, m.Z}
	packets := [][]byte{
		protocol.EncodeHurtAnimation(m.ID, m.Yaw),
		protocol.EncodeDamageEvent(m.ID, s.playerAttackDamageTypeID, player.entityID, player.entityID, &position),
		protocol.EncodeEntitySoundEffect(s.mobHurtSound(kind), protocol.SoundCategoryHostile, m.ID, 1, 1, 0),
	}
	died := m.Health <= 0
	if died {
		m.Health = 0
		m.Dead = true
		m.DeadTicks = 0
		packets = append(packets,
			protocol.EncodeEntityEvent(m.ID, protocol.EntityEventDeath),
			protocol.EncodeEntitySoundEffect(s.mobDeathSound(kind), protocol.SoundCategoryHostile, m.ID, 1, 1, 0),
		)
	} else if packet := s.knockbackMob(m, px, pz); packet != nil {
		// 未死亡：沿攻击者反方向击退一小段。
		packets = append(packets, packet)
	}
	x, y, z := m.X, m.Y, m.Z
	dim := m.Dim
	s.entityMu.Unlock()

	for _, packet := range packets {
		s.broadcastToNearby(dim, packet, x, z, s.playerSnapshot())
	}
	// 攻击消耗疲劳度（与原版一致）。
	player.addExhaustion(playerExhaustionAttack)
	if died {
		slog.Info("mob killed", "id", m.ID, "player", player.name)
		s.dropMobLoot(kind, dim, x, y, z)
		bar, level, total := player.addExperience(mobKinds[kind].experience)
		player.tryWrite(protocol.EncodeSetExperience(bar, level, total))
	}
}

// mobHurtSound/mobDeathSound 返回受击/死亡音效 ID（缺失时回退到僵尸音效）。
func (s *Server) mobHurtSound(kind mobKind) int32 {
	if id, ok := s.mobHurtSounds[kind]; ok {
		return id
	}
	return s.soundMobHurt
}

func (s *Server) mobDeathSound(kind mobKind) int32 {
	if id, ok := s.mobDeathSounds[kind]; ok {
		return id
	}
	return s.soundMobDeath
}

// dropMobLoot 生成生物的掉落物（与原版大致一致：0–2 个常规掉落；
// 僵尸的稀有掉落铁锭/胡萝卜/土豆概率 1/40；蜘蛛 1/3 掉落蜘蛛眼）。
func (s *Server) dropMobLoot(kind mobKind, dim world.Dimension, x, y, z float64) {
	spawn := func(name string, count int32) {
		if count <= 0 {
			return
		}
		stack, err := item.FromName(name, count)
		if err != nil {
			return
		}
		vx := (float64(s.nextRandom()%1000)/1000 - 0.5) * 0.2
		vz := (float64(s.nextRandom()%1000)/1000 - 0.5) * 0.2
		s.spawnItem(dim, stack, x, y+0.5, z, vx, 0.2, vz, mobDropPickupDelay)
	}
	switch kind {
	case mobZombie:
		spawn("minecraft:rotten_flesh", int32(s.nextRandom()%3))
		if s.nextRandom()%mobRareDropChance == 0 {
			rare := []string{"minecraft:iron_ingot", "minecraft:carrot", "minecraft:potato"}
			spawn(rare[s.nextRandom()%uint64(len(rare))], 1)
		}
	case mobSkeleton:
		spawn("minecraft:bone", int32(s.nextRandom()%3))
		spawn("minecraft:arrow", int32(s.nextRandom()%3))
	case mobCreeper:
		spawn("minecraft:gunpowder", int32(s.nextRandom()%3))
	case mobSpider:
		spawn("minecraft:string", int32(s.nextRandom()%3))
		if s.nextRandom()%3 == 0 {
			spawn("minecraft:spider_eye", 1)
		}
	}
}

// damagePlayer 对玩家造成伤害并发送伤害事件、生命值与音效；
// 冷却期内、死亡后或创造/旁观模式不生效。返回是否实际造成伤害。
// damagePlayer 对玩家应用伤害：扣血、发送受伤动画与事件、播放音效，
// 濒死时广播死亡消息。damageTypeID 是伤害类型注册表同步 ID（如
// mob_attack/player_attack/fall）。返回伤害是否实际生效。
func (s *Server) damagePlayer(player *session, amount float32, sourceName string, sourceMobID, damageTypeID int32, sourcePosition *[3]float64) bool {
	health, food, saturation, applied, died := player.applyDamage(amount, time.Now())
	if !applied {
		return false
	}
	px, py, pz, _, _ := player.playerPosition()
	if sourcePosition != nil {
		// 受伤动画的方向：攻击者相对玩家的方向。
		hurtYaw := float32(math.Atan2(-(sourcePosition[0]-px), sourcePosition[2]-pz) * 180 / math.Pi)
		player.tryWrite(protocol.EncodeHurtAnimation(player.entityID, hurtYaw))
	}
	player.tryWrite(protocol.EncodeDamageEvent(player.entityID, damageTypeID, sourceMobID, sourceMobID, sourcePosition))
	player.tryWrite(protocol.EncodeUpdateHealth(health, food, saturation))
	player.tryWrite(protocol.EncodeSoundEffect(s.soundPlayerHurt, protocol.SoundCategoryPlayer, px, py, pz, 1, 1, 0))
	if died {
		s.broadcastPacket(protocol.EncodeSystemChat(player.name + " was slain by " + sourceName))
		slog.Info("player died", "name", player.name, "source", sourceName)
		// 与原版一致：死亡时掉落全部物品。
		s.dropPlayerInventory(player)
	}
	return true
}

// respawnPlayer 处理玩家的重生请求：重置状态、发送 Respawn 包并传送回出生点。
func (s *Server) respawnPlayer(player *session) {
	if !player.markRespawned() {
		return // 未死亡或已在重生中。
	}
	dim := player.dimensionID()
	gameMode := player.gameModeID()
	spawn := s.spawnInfo(dim, gameMode)
	spawnX, _, spawnZ := s.spawnPositionFor(dim)
	if err := player.writePacket(protocol.EncodeRespawn(spawn, 0)); err != nil {
		return
	}
	// 1.20.2+ 协议要求：每次 Respawn 后都必须重发“开始等待区块”（Game Event 13），
	// 即使重生到同一维度；否则客户端会一直停在“正在加载地形”界面。
	// 与原版一致：该事件必须在区块数据之前发送。
	if err := player.writePacket(protocol.EncodeGameEvent(13, 0)); err != nil {
		return
	}
	// 重生后客户端会清空世界：重置区块记录并重发出生点视距内的区块。
	centerX := int(math.Floor(spawnX)) >> 4
	centerZ := int(math.Floor(spawnZ)) >> 4
	player.sentChunks = make(map[world.ChunkPos]struct{})
	if err := player.writePacket(protocol.EncodeSetCenterChunk(int32(centerX), int32(centerZ))); err != nil {
		return
	}
	if err := player.syncChunks(centerX, centerZ); err != nil {
		slog.Error("failed to send chunks after respawn", "name", player.name, "error", err)
		return
	}
	player.teleportToSpawn()
	s.broadcastPlayerMove(player)
	health, food, saturation := player.healthStatus()
	player.tryWrite(protocol.EncodeUpdateHealth(health, food, saturation))
	slog.Info("player respawned", "name", player.name)
}

// nextRandom 返回确定性伪随机数（splitmix64），用于生物生成与游荡方向。
// 不使用全局 rand 以避免锁竞争；测试可通过固定起始状态复现。
func (s *Server) nextRandom() uint64 {
	for {
		state := s.randState.Load()
		next := state + 0x9E3779B97F4A7C15
		if s.randState.CompareAndSwap(state, next) {
			z := next
			z ^= z >> 30
			z *= 0xBF58476D1CE4E5B9
			z ^= z >> 27
			z *= 0x94D049BB133111EB
			return z ^ (z >> 31)
		}
	}
}

// newEntityUUID 生成一个随机（v4）实体 UUID。
func newEntityUUID() [16]byte {
	var uuid [16]byte
	if _, err := rand.Read(uuid[:]); err != nil {
		return uuid
	}
	uuid[6] = (uuid[6] & 0x0F) | 0x40 // 版本 4
	uuid[8] = (uuid[8] & 0x3F) | 0x80 // RFC 4122 变体
	return uuid
}

// tickLoop 按 tickInterval 驱动实体 Tick；tickInterval <= 0 时后台 Tick 禁用（测试手动驱动）。
func (s *Server) tickLoop(ctx context.Context) {
	if s.tickInterval <= 0 {
		return
	}
	ticker := time.NewTicker(s.tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick()
		}
	}
}

// moveMobAway 让生物朝远离目标的方向移动一步（骷髅保持射程）。
// 会同步更新朝向。调用方必须持有 entityMu。
func (s *Server) moveMobAway(w *world.World, m *mob, fromX, fromZ, speed float64) bool {
	dx, dz := m.X-fromX, m.Z-fromZ
	distance := math.Hypot(dx, dz)
	if distance < 1e-3 {
		return false
	}
	stepX := dx / distance * speed
	stepZ := dz / distance * speed
	m.Yaw = float32(math.Atan2(-(fromX-m.X), fromZ-m.Z) * 180 / math.Pi)
	if s.tryMoveMob(w, m, m.X+stepX, m.Z+stepZ) {
		return true
	}
	return s.tryMoveMob(w, m, m.X+stepX, m.Z) || s.tryMoveMob(w, m, m.X, m.Z+stepZ)
}

// resolveMobRegistryIDs 查询生物/箭矢/伤害系统需要的注册表 ID。
// 任何条目缺失都会禁用生物系统，而不是用错误的 ID 发送协议数据。
func (s *Server) resolveMobRegistryIDs() {
	staticLookup := func(registryName, entry string) (int32, bool) {
		return registry.StaticEntryID(registryName, entry)
	}
	s.mobTypeIDs = make(map[mobKind]int32, len(mobKinds))
	s.mobHurtSounds = make(map[mobKind]int32, len(mobKinds))
	s.mobDeathSounds = make(map[mobKind]int32, len(mobKinds))
	s.arrows = make(map[int32]*arrowEntity)

	s.mobsEnabled = true
	for kind, stats := range mobKinds {
		typeID, ok := staticLookup("minecraft:entity_type", stats.name)
		if !ok {
			slog.Error("缺少实体类型注册表条目，生物系统将被禁用", "entry", stats.name)
			s.mobsEnabled = false
			return
		}
		hurt, ok := staticLookup("minecraft:sound_event", stats.hurtSound)
		if !ok {
			slog.Error("缺少音效注册表条目，生物系统将被禁用", "entry", stats.hurtSound)
			s.mobsEnabled = false
			return
		}
		death, ok := staticLookup("minecraft:sound_event", stats.deathSound)
		if !ok {
			slog.Error("缺少音效注册表条目，生物系统将被禁用", "entry", stats.deathSound)
			s.mobsEnabled = false
			return
		}
		s.mobTypeIDs[kind] = typeID
		s.mobHurtSounds[kind] = hurt
		s.mobDeathSounds[kind] = death
	}

	syncLookup := func(registryName, entry string) (int32, bool) {
		return registry.SyncEntryID(registryName, entry)
	}
	for _, item := range []struct {
		registry string
		entry    string
		target   *int32
	}{
		{"minecraft:damage_type", "minecraft:mob_attack", &s.mobAttackDamageTypeID},
		{"minecraft:damage_type", "minecraft:player_attack", &s.playerAttackDamageTypeID},
		{"minecraft:damage_type", "minecraft:fall", &s.fallDamageTypeID},
		{"minecraft:damage_type", "minecraft:arrow", &s.arrowDamageTypeID},
		{"minecraft:damage_type", "minecraft:explosion", &s.explosionDamageTypeID},
	} {
		id, ok := syncLookup(item.registry, item.entry)
		if !ok {
			slog.Error("缺少伤害类型注册表条目，生物系统将被禁用", "entry", item.entry)
			s.mobsEnabled = false
			return
		}
		*item.target = id
	}

	// 箭矢与相关音效。
	arrowType, ok := staticLookup("minecraft:entity_type", "minecraft:arrow")
	if !ok {
		slog.Error("缺少箭矢实体类型，生物系统将被禁用")
		s.mobsEnabled = false
		return
	}
	s.arrowTypeID = arrowType
	for _, item := range []struct {
		entry  string
		target *int32
	}{
		{"minecraft:entity.arrow.shoot", &s.soundArrowShoot},
		{"minecraft:entity.arrow.hit", &s.soundArrowHit},
		{"minecraft:entity.arrow.hit", &s.soundArrowHitBlock},
		{"minecraft:entity.creeper.primed", &s.soundCreeperPrime},
		{"minecraft:entity.player.hurt", &s.soundPlayerHurt},
	} {
		id, ok := staticLookup("minecraft:sound_event", item.entry)
		if !ok {
			slog.Error("缺少音效注册表条目，生物系统将被禁用", "entry", item.entry)
			s.mobsEnabled = false
			return
		}
		*item.target = id
	}
	s.arrowsEnabled = s.mobsEnabled
	// 兼容字段：soundMobHurt/soundMobDeath 是僵尸音效（回退用）。
	s.soundMobHurt = s.mobHurtSounds[mobZombie]
	s.soundMobDeath = s.mobDeathSounds[mobZombie]
	s.zombieTypeID = s.mobTypeIDs[mobZombie]
}
