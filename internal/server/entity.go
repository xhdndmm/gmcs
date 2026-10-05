package server

import (
	"context"
	"crypto/rand"
	"log/slog"
	"math"
	"time"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 生物与战斗参数。数值以“简单、可验证”为先，不追求与原版数值完全一致
// （原版僵尸为 20 生命、2/3/4 点难度伤害；此处固定 2 点）。
const (
	zombieMaxHealth = 20
	zombieWalkSpeed = 0.055 // 方块/tick（约 1.1 格/秒）
	mobWanderSpeed  = 0.03
	mobFollowRange  = 32
	mobDespawnRange = 64
	mobAttackRange  = 1.9
	// mobAttackVerticalRange 是攻击允许的高度差（超出则打不到）。
	mobAttackVerticalRange = 2.0
	// mobAttackWindupTicks 是进入攻击距离后的抬手时间（0.5 秒），
	// 避免“贴脸瞬间受伤”。
	mobAttackWindupTicks = 10
	mobAttackCooldown    = 20
	mobAttackDamage      = 2
	// mobHurtCooldownTicks 是生物受击后的无敌帧（0.5 秒）。
	mobHurtCooldownTicks = 10
	// mobKnockback 是生物被击中时沿攻击者反方向推开的距离（方块）。
	mobKnockback  = 0.4
	mobDeathTicks = 20
	// mobEyeHeight 是近似眼睛高度（视线检查用）。
	mobEyeHeight = 1.5

	playerAttackDamage = 4
	playerAttackRange  = 3.5
	playerHurtCooldown = 500 * time.Millisecond

	// 玩家状态常量（暂无饥饿系统，饱食度固定）。
	maxPlayerHealth  = 20
	maxPlayerFood    = 20
	playerSaturation = 5

	// spawnCheckInterval 是生成检查的间隔（tick，20 TPS 下为 5 秒）。
	spawnCheckInterval = 100
	// mobBroadcastRange 之外的生物移动不再广播（视野外更新留给后续区块跟踪）。
	mobBroadcastRange = 48
)

// mob 是一只服务器控制的生物。字段由 Server.entityMu 保护，
// 只在 Tick 与攻击处理中修改。
type mob struct {
	ID     int32
	UUID   [16]byte
	TypeID int32

	X, Y, Z    float64
	Yaw, Pitch float32

	Health float32
	// Dead 为 true 时播放死亡动画，DeadTicks 到达上限后移除。
	Dead      bool
	DeadTicks int

	AttackCooldown int
	// WasInRange 记录上一帧是否处于可攻击状态（用于进入攻击距离时的抬手）。
	WasInRange bool
	// HurtCooldown 是受击无敌帧剩余 tick。
	HurtCooldown int
	// WanderX/WanderZ 是游荡目标；WanderTicks 归零时重新选择。
	WanderX, WanderZ float64
	WanderTicks      int
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

// tick 推进一帧世界逻辑（生物 AI、生成与清理）。由 tickLoop 按 tickInterval 调用，
// 测试中可直接调用以获得确定性。
func (s *Server) tick() {
	players := s.playerSnapshot()
	s.tickMobs(players)
	s.spawnTicks++
	if s.spawnTicks >= spawnCheckInterval {
		s.spawnTicks = 0
		s.trySpawnMob(players)
	}
}

// tickMobs 更新全部生物：追击/游荡、攻击玩家、清理死亡与远离的生物。
func (s *Server) tickMobs(players []*session) {
	if !s.mobsEnabled {
		return
	}
	type pendingAttack struct {
		mob    *mob
		player *session
	}
	var (
		attacks  []pendingAttack
		moves    [][]byte
		removals []int32
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

		// 寻找最近的可攻击玩家。
		var nearest *session
		nearestDistance := math.MaxFloat64
		for _, player := range players {
			if !player.canBeAttacked() {
				continue
			}
			px, _, pz, _, _ := player.playerPosition()
			distance := math.Hypot(px-m.X, pz-m.Z)
			if distance < nearestDistance {
				nearestDistance = distance
				nearest = player
			}
		}
		if nearestDistance > mobDespawnRange {
			delete(s.mobs, id)
			removals = append(removals, id)
			continue
		}

		moved := false
		if nearest != nil && nearestDistance <= mobFollowRange {
			px, py, pz, _, _ := nearest.playerPosition()
			// 只有距离、高度差与视线都满足时才攻击；被方块挡住则继续尝试靠近。
			inAttack := nearestDistance <= mobAttackRange && math.Abs(py-m.Y) < mobAttackVerticalRange
			clear := inAttack && s.attackPathClear(m.X, m.Y+mobEyeHeight, m.Z, px, py+mobEyeHeight, pz)
			if clear && !m.WasInRange && m.AttackCooldown < mobAttackWindupTicks {
				// 刚进入攻击距离：先抬手 0.5 秒再出手。
				m.AttackCooldown = mobAttackWindupTicks
			}
			m.WasInRange = clear
			if clear && m.AttackCooldown <= 0 {
				m.AttackCooldown = mobAttackCooldown
				attacks = append(attacks, pendingAttack{mob: m, player: nearest})
			} else if !clear {
				moved = s.moveMobToward(m, px, pz, zombieWalkSpeed)
			}
		} else {
			m.WanderTicks--
			if m.WanderTicks <= 0 {
				m.WanderTicks = 40 + int(s.nextRandom()%80)
				angle := float64(s.nextRandom()%6283185) / 1000000
				m.WanderX = m.X + math.Cos(angle)*6
				m.WanderZ = m.Z + math.Sin(angle)*6
			}
			moved = s.moveMobToward(m, m.WanderX, m.WanderZ, mobWanderSpeed)
		}

		if m.HurtCooldown > 0 {
			m.HurtCooldown--
		}
		if m.AttackCooldown > 0 {
			m.AttackCooldown--
		}
		if moved && nearestDistance <= mobBroadcastRange {
			moves = append(moves, protocol.EncodeEntityPositionSync(
				m.ID, m.X, m.Y, m.Z, 0, 0, 0, m.Yaw, m.Pitch, true))
		}
	}
	s.entityMu.Unlock()

	for _, packet := range moves {
		s.broadcastPacket(packet)
	}
	if len(removals) > 0 {
		s.broadcastPacket(protocol.EncodeEntityDestroy(removals))
	}
	for _, attack := range attacks {
		s.performMobAttack(attack.mob, attack.player)
	}
}

// performMobAttack 让生物攻击玩家：先广播挥手动画并面向玩家，再结算伤害，
// 使玩家能看到攻击动作而不是“凭空掉血”。
func (s *Server) performMobAttack(m *mob, player *session) {
	px, _, pz, _, _ := player.playerPosition()
	m.Yaw = float32(math.Atan2(-(px-m.X), pz-m.Z) * 180 / math.Pi)
	s.broadcastPacket(protocol.EncodeAnimate(m.ID, 0))
	position := [3]float64{m.X, m.Y + 1, m.Z}
	s.damagePlayer(player, mobAttackDamage, "Zombie", m.ID, &position)
}

// moveMobToward 让生物朝目标水平移动一步；路径被水或高低差挡住时返回 false。
// 会同步更新朝向。调用方必须持有 entityMu。
func (s *Server) moveMobToward(m *mob, targetX, targetZ, speed float64) bool {
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
		newX, newZ := m.X+offsetX, m.Z+offsetZ
		if !s.insideBorder(newX, newZ) {
			return false
		}
		blockX, blockZ := int(math.Floor(newX)), int(math.Floor(newZ))
		state, _, ok := s.world.TopBlock(blockX, blockZ)
		if !ok || state == world.WaterBlock {
			return false
		}
		groundY, ok := s.world.GroundY(blockX, blockZ)
		if !ok || math.Abs(groundY-m.Y) > 1.1 {
			return false
		}
		m.X, m.Y, m.Z = newX, groundY, newZ
		m.Yaw = float32(math.Atan2(-dx, dz) * 180 / math.Pi)
		return true
	}
	if move(stepX, stepZ) {
		return true
	}
	// 被挡住时尝试沿单轴滑动。
	return move(stepX, 0) || move(0, stepZ)
}

// attackPathClear 粗略检查两点（眼睛高度）之间是否被方块挡住：
// 采样 35% 与 70% 处的方块，空气与水视为可穿过。
// 用于避免隔墙攻击（生物打玩家与玩家打生物共用）。
func (s *Server) attackPathClear(fromX, fromY, fromZ, toX, toY, toZ float64) bool {
	for _, t := range []float64{0.35, 0.7} {
		x := int(math.Floor(fromX + (toX-fromX)*t))
		y := int(math.Floor(fromY + (toY-fromY)*t))
		z := int(math.Floor(fromZ + (toZ-fromZ)*t))
		state := s.world.BlockAt(x, y, z)
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
	blockX, blockZ := int(math.Floor(newX)), int(math.Floor(newZ))
	state, _, ok := s.world.TopBlock(blockX, blockZ)
	if !ok || state == world.WaterBlock {
		return nil
	}
	groundY, ok := s.world.GroundY(blockX, blockZ)
	if !ok || math.Abs(groundY-m.Y) > 1.1 {
		return nil
	}
	m.X, m.Y, m.Z = newX, groundY, newZ
	return protocol.EncodeEntityPositionSync(m.ID, m.X, m.Y, m.Z, 0, 0, 0, m.Yaw, m.Pitch, true)
}

// trySpawnMob 在随机玩家附近尝试生成一只生物。
func (s *Server) trySpawnMob(players []*session) {
	if !s.mobsEnabled || !s.config.SpawnMonsters || s.config.MaxMobs <= 0 || len(players) == 0 {
		return
	}
	s.entityMu.Lock()
	count := len(s.mobs)
	s.entityMu.Unlock()
	if count >= s.config.MaxMobs {
		return
	}

	player := players[s.nextRandom()%uint64(len(players))]
	px, _, pz, _, _ := player.playerPosition()
	angle := float64(s.nextRandom()%6283185) / 1000000
	distance := 12 + float64(s.nextRandom()%1200)/100
	blockX := int(math.Floor(px + math.Cos(angle)*distance))
	blockZ := int(math.Floor(pz + math.Sin(angle)*distance))
	if !s.insideBorder(float64(blockX)+0.5, float64(blockZ)+0.5) {
		return
	}

	state, _, ok := s.world.TopBlock(blockX, blockZ)
	if !ok || state == world.WaterBlock {
		return
	}
	groundY, ok := s.world.GroundY(blockX, blockZ)
	if !ok {
		return
	}
	s.addMob(float64(blockX)+0.5, groundY, float64(blockZ)+0.5)
}

// addMob 生成一只僵尸并把 Add Entity 广播给全部玩家。
func (s *Server) addMob(x, y, z float64) *mob {
	m := &mob{
		ID:     s.entityIDs.Add(1),
		UUID:   newEntityUUID(),
		TypeID: s.zombieTypeID,
		X:      x,
		Y:      y,
		Z:      z,
		Health: zombieMaxHealth,
	}
	s.entityMu.Lock()
	s.mobs[m.ID] = m
	s.entityMu.Unlock()
	s.broadcastPacket(protocol.EncodeAddEntity(m.ID, m.UUID, m.TypeID, m.X, m.Y, m.Z, 0, 0, 0, m.Yaw, m.Pitch))
	slog.Debug("mob spawned", "id", m.ID, "x", m.X, "y", m.Y, "z", m.Z)
	return m
}

// sendExistingMobs 把世界中已有的生物发送给新加入的玩家。
func (s *Server) sendExistingMobs(player *session) error {
	s.entityMu.Lock()
	packets := make([][]byte, 0, len(s.mobs))
	for _, m := range s.mobs {
		if m.Dead {
			continue
		}
		packets = append(packets, protocol.EncodeAddEntity(
			m.ID, m.UUID, m.TypeID, m.X, m.Y, m.Z, 0, 0, 0, m.Yaw, m.Pitch))
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
	if !s.mobsEnabled || player.isDead() {
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
		return
	}
	if math.Hypot(px-m.X, pz-m.Z) > playerAttackRange || math.Abs(py-m.Y) > 3 {
		s.entityMu.Unlock()
		return // 超出攻击距离：忽略（简单的服务端校验）。
	}
	if !s.attackPathClear(px, py+mobEyeHeight, pz, m.X, m.Y+mobEyeHeight, m.Z) {
		s.entityMu.Unlock()
		return // 隔墙攻击：忽略。
	}
	if m.HurtCooldown > 0 {
		s.entityMu.Unlock()
		return // 受击无敌帧内：忽略。
	}
	m.HurtCooldown = mobHurtCooldownTicks
	m.Health -= playerAttackDamage
	m.Yaw = float32(math.Atan2(-(px-m.X), pz-m.Z) * 180 / math.Pi)
	position := [3]float64{m.X, m.Y + 1, m.Z}
	packets := [][]byte{
		protocol.EncodeHurtAnimation(m.ID, m.Yaw),
		protocol.EncodeDamageEvent(m.ID, s.playerAttackDamageTypeID, player.entityID, player.entityID, &position),
		protocol.EncodeEntitySoundEffect(s.soundMobHurt, protocol.SoundCategoryHostile, m.ID, 1, 1, 0),
	}
	if m.Health <= 0 {
		m.Health = 0
		m.Dead = true
		m.DeadTicks = 0
		packets = append(packets,
			protocol.EncodeEntityEvent(m.ID, protocol.EntityEventDeath),
			protocol.EncodeEntitySoundEffect(s.soundMobDeath, protocol.SoundCategoryHostile, m.ID, 1, 1, 0),
		)
	} else if packet := s.knockbackMob(m, px, pz); packet != nil {
		// 未死亡：沿攻击者反方向击退一小段。
		packets = append(packets, packet)
	}
	s.entityMu.Unlock()

	for _, packet := range packets {
		s.broadcastPacket(packet)
	}
	if m.Dead {
		slog.Info("mob killed", "id", m.ID, "player", player.name)
	}
}

// damagePlayer 对玩家造成伤害并发送伤害事件、生命值与音效；
// 冷却期内、死亡后或创造/旁观模式不生效。返回是否实际造成伤害。
func (s *Server) damagePlayer(player *session, amount float32, sourceName string, sourceMobID int32, sourcePosition *[3]float64) bool {
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
	player.tryWrite(protocol.EncodeDamageEvent(player.entityID, s.mobAttackDamageTypeID, sourceMobID, sourceMobID, sourcePosition))
	player.tryWrite(protocol.EncodeUpdateHealth(health, food, saturation))
	player.tryWrite(protocol.EncodeSoundEffect(s.soundPlayerHurt, protocol.SoundCategoryPlayer, px, py, pz, 1, 1, 0))
	if died {
		s.broadcastPacket(protocol.EncodeSystemChat(player.name + " was slain by " + sourceName))
		slog.Info("player died", "name", player.name, "source", sourceName)
	}
	return true
}

// respawnPlayer 处理玩家的重生请求：重置状态、发送 Respawn 包并传送回出生点。
func (s *Server) respawnPlayer(player *session) {
	if !player.markRespawned() {
		return // 未死亡或已在重生中。
	}
	gameMode := player.gameModeID()
	spawn := s.spawnInfo(gameMode)
	if err := player.writePacket(protocol.EncodeRespawn(spawn, 0)); err != nil {
		return
	}
	// 重生后客户端会清空世界：重发出生区块与位置。
	chunk, err := s.world.Chunk(0, 0)
	if err != nil {
		slog.Error("failed to load spawn chunk for respawn", "name", player.name, "error", err)
		return
	}
	if err := player.writePacket(protocol.EncodeSetCenterChunk(0, 0)); err != nil {
		return
	}
	if err := player.writePacket(world.EncodeChunkDataPacket(chunk)); err != nil {
		return
	}
	player.teleportToSpawn()
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

// resolveMobRegistryIDs 查询生物与伤害系统需要的注册表 ID。
// 任何条目缺失都会禁用生物系统，而不是用错误的 ID 发送协议数据。
func (s *Server) resolveMobRegistryIDs() {
	type lookup struct {
		registry string
		entry    string
		static   bool
		target   *int32
	}
	lookups := []lookup{
		{"minecraft:entity_type", "minecraft:zombie", true, &s.zombieTypeID},
		{"minecraft:damage_type", "minecraft:mob_attack", false, &s.mobAttackDamageTypeID},
		{"minecraft:damage_type", "minecraft:player_attack", false, &s.playerAttackDamageTypeID},
		{"minecraft:sound_event", "minecraft:entity.zombie.hurt", true, &s.soundMobHurt},
		{"minecraft:sound_event", "minecraft:entity.zombie.death", true, &s.soundMobDeath},
		{"minecraft:sound_event", "minecraft:entity.player.hurt", true, &s.soundPlayerHurt},
	}
	s.mobsEnabled = true
	for _, item := range lookups {
		var (
			id int32
			ok bool
		)
		if item.static {
			id, ok = registry.StaticEntryID(item.registry, item.entry)
		} else {
			id, ok = registry.SyncEntryID(item.registry, item.entry)
		}
		if !ok {
			s.mobsEnabled = false
			slog.Error("缺少注册表条目，生物系统将被禁用", "registry", item.registry, "entry", item.entry)
			return
		}
		*item.target = id
	}
}
