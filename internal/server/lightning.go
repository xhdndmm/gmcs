//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"log/slog"
	"math"

	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 雷暴闪电（原版 LightningBolt / ServerLevel.tickChunk 的落雷判定）。
//
// 原版在区块 tick 中对每个可 tick 区块掷 1/100000 的骰子，命中且该位置
// `isRainingAt` 为真时生成闪电实体（客户端据此渲染闪电与雷声）。
// gmcs 没有区块 tick 网格，改为对每名主世界玩家掷一次“按视距内区块数放大”
// 的骰子：命中概率 = 区块数 / 100000，等效频率与原版同量级。
const (
	// lightningChancePerChunk 是原版每个区块每 tick 的落雷概率倒数。
	lightningChancePerChunk = 100000
	// lightningChunkRadius 是参与概率统计的区块半径（原版模拟距离 10 的近似）。
	lightningChunkRadius = 8
	// lightningLifetime 是闪电实体的存活 tick（原版 LightningBolt 为 2）。
	lightningLifetime = 2
	// lightningDamage 是落雷对范围内的生物与玩家造成的伤害（原版 5 点）。
	lightningDamage = 5
	// lightningHorizontalRadius / lightningVerticalRadius 是伤害范围
	// （原版 AABB.inflate(3, 6, 3)）。
	lightningHorizontalRadius = 3.0
	lightningVerticalRadius   = 6.0
	// lightningFireTicks 是落雷点燃实体的时长（原版 setSecondsOnFire(8)）。
	lightningFireTicks = 8 * 20
	// lightningThunderVolume 是雷声音量（原版 2.0，可听到更远距离）。
	lightningThunderVolume = 2
)

// lightningBolt 是一个正在显示的闪电实体（受 entityMu 保护）。
// 闪电只用于客户端渲染与落雷结算，不参与实体跟踪：存活 2 tick 后移除。
type lightningBolt struct {
	ID    int32
	UUID  [16]byte
	Dim   world.Dimension
	X     float64
	Y     float64
	Z     float64
	Ticks int
}

// resolveLightningIDs 查询闪电需要的注册表 ID。缺少条目时只禁用闪电，
// 不影响其它系统（与生物系统的“缺条目即禁用”策略一致）。
func (s *Server) resolveLightningIDs() {
	typeID, ok := registry.StaticEntryID("minecraft:entity_type", "minecraft:lightning_bolt")
	if !ok {
		slog.Warn("缺少闪电实体类型，雷暴闪电将被禁用")
		return
	}
	thunder, ok := registry.StaticEntryID("minecraft:sound_event", "minecraft:entity.lightning_bolt.thunder")
	if !ok {
		slog.Warn("缺少雷声音效，雷暴闪电将被禁用", "entry", "minecraft:entity.lightning_bolt.thunder")
		return
	}
	impact, ok := registry.StaticEntryID("minecraft:sound_event", "minecraft:entity.lightning_bolt.impact")
	if !ok {
		slog.Warn("缺少落雷音效，雷暴闪电将被禁用", "entry", "minecraft:entity.lightning_bolt.impact")
		return
	}
	damageType, ok := registry.SyncEntryID("minecraft:damage_type", "minecraft:lightning_bolt")
	if !ok {
		slog.Warn("缺少闪电伤害类型，雷暴闪电将被禁用", "entry", "minecraft:lightning_bolt")
		return
	}
	s.lightningTypeID = typeID
	s.soundLightningThunder = thunder
	s.soundLightningImpact = impact
	s.lightningDamageTypeID = damageType
	s.lightningBolts = make(map[int32]*lightningBolt)
	s.lightningEnabled = true
}

// tickLightning 推进闪电：先移除到期的闪电实体，再在雷暴中掷落雷判定
// （原版 LightningBolt 只存活 2 tick，落雷伤害/火焰在生成当帧结算）。
func (s *Server) tickLightning(players []*session) {
	if !s.lightningEnabled {
		return
	}
	s.expireLightningBolts(players)
	// 原版 tickChunk 的落雷条件只有 isThundering()（位置是否下雨由
	// isRainingAt 再判定一次），这里保持一致。
	if !s.isThundering() {
		return
	}
	chunks := float64((2*lightningChunkRadius + 1) * (2*lightningChunkRadius + 1))
	for _, player := range players {
		if !player.isJoined() || player.dimensionID() != world.DimensionOverworld {
			continue
		}
		// 单次掷骰，命中概率 = 视距内区块数 / 100000。
		if float64(s.nextInt(lightningChancePerChunk)) >= chunks {
			continue
		}
		px, _, pz, _, _ := player.playerPosition()
		centerX := int(math.Floor(px)) >> 4
		centerZ := int(math.Floor(pz)) >> 4
		cx := centerX + s.nextInt(2*lightningChunkRadius+1) - lightningChunkRadius
		cz := centerZ + s.nextInt(2*lightningChunkRadius+1) - lightningChunkRadius
		x := cx<<4 + s.nextInt(16)
		z := cz<<4 + s.nextInt(16)
		s.strikeLightning(x, z)
	}
}

// expireLightningBolts 递减已有闪电的寿命并广播移除。
func (s *Server) expireLightningBolts(players []*session) {
	var removed []int32
	s.entityMu.Lock()
	for id, bolt := range s.lightningBolts {
		bolt.Ticks--
		if bolt.Ticks <= 0 {
			removed = append(removed, id)
		}
	}
	for _, id := range removed {
		delete(s.lightningBolts, id)
	}
	s.entityMu.Unlock()
	if len(removed) == 0 {
		return
	}
	packet := protocol.EncodeRemoveEntities(removed)
	for _, player := range players {
		player.tryWrite(packet)
	}
}

// strikeLightning 在世界坐标 (x, z) 的地表落雷：生成闪电实体、播放雷声、
// 点燃方块并对范围内的实体结算伤害（原版 LightningBolt 的落雷效果）。
// 位置不满足“下雨 + 可见天空”时直接返回（与原版 tickChunk 一致）。
func (s *Server) strikeLightning(x, z int) {
	w := s.worldFor(world.DimensionOverworld)
	if w == nil {
		return
	}
	surface, ok := w.GroundY(x, z)
	if !ok {
		return
	}
	y := int(math.Floor(surface))
	if !s.isRainingAt(world.DimensionOverworld, x, y, z) {
		return
	}
	bolt := &lightningBolt{
		ID:    s.entityIDs.Add(1),
		UUID:  newEntityUUID(),
		Dim:   world.DimensionOverworld,
		X:     float64(x) + 0.5,
		Y:     float64(y),
		Z:     float64(z) + 0.5,
		Ticks: lightningLifetime,
	}
	s.entityMu.Lock()
	s.lightningBolts[bolt.ID] = bolt
	s.entityMu.Unlock()
	// 闪电实体只对 64 格内的玩家广播（原版实体跟踪范围一致）。
	s.writeToNearbyPlayers(
		protocol.EncodeAddEntity(bolt.ID, bolt.UUID, s.lightningTypeID, bolt.X, bolt.Y, bolt.Z, 0, 0, 0, 0, 0),
		bolt.Dim, bolt.X, bolt.Z, nil)
	// 雷声（音量 2，可听到更远；仍按 64 格广播范围发送）。
	s.writeToNearbyPlayers(
		protocol.EncodeSoundEffect(s.soundLightningThunder, protocol.SoundCategoryWeather, bolt.X, bolt.Y, bolt.Z, lightningThunderVolume, 0.8+float32(s.nextInt(5))/10, 0),
		bolt.Dim, bolt.X, bolt.Z, nil)
	// 雷击点方块若为空气且下方是固体，则点火（原版 LightningBolt 的 fire 判定）。
	if w.BlockAt(x, y, z) == world.AirBlock {
		if below := w.BlockAt(x, y-1, z); below != world.AirBlock && registry.BlockStateCollides(below) {
			if fireState, ok := registry.BlockStateIDs["minecraft:fire"]; ok {
				if w.SetBlock(x, y, z, fireState) {
					s.broadcastBlockUpdate(world.DimensionOverworld, x, y, z, int32(fireState))
				}
			}
		}
	}
	s.damageEntitiesInLightning(bolt)
	s.writeToNearbyPlayers(
		protocol.EncodeSoundEffect(s.soundLightningImpact, protocol.SoundCategoryWeather, bolt.X, bolt.Y, bolt.Z, 1, 1, 0),
		bolt.Dim, bolt.X, bolt.Z, nil)
	slog.Debug("lightning struck", "x", x, "y", y, "z", z)
}

// damageEntitiesInLightning 对闪电周围 AABB 内的生物与玩家结算伤害并点燃
// （原版 LightningBolt：生物 5 点 + 8 秒着火，玩家 5 点 + 8 秒着火）。
func (s *Server) damageEntitiesInLightning(bolt *lightningBolt) {
	type mobResult struct {
		kind    mobKind
		packets [][]byte
		drop    bool
	}
	var results []mobResult
	s.entityMu.Lock()
	for _, m := range s.mobs {
		if m.Dead || m.Dim != bolt.Dim {
			continue
		}
		if math.Abs(m.X-bolt.X) > lightningHorizontalRadius+0.5 ||
			math.Abs(m.Z-bolt.Z) > lightningHorizontalRadius+0.5 {
			continue
		}
		if m.Y < bolt.Y-lightningVerticalRadius || m.Y > bolt.Y+lightningVerticalRadius {
			continue
		}
		m.FireTicks = lightningFireTicks
		env := mobEnvironment{dim: m.Dim, kind: m.Kind, x: m.X, z: m.Z}
		if !s.damageMob(m, lightningDamage, s.lightningDamageTypeID, &env) {
			continue
		}
		results = append(results, mobResult{kind: m.Kind, packets: env.packets, drop: env.drop})
	}
	s.entityMu.Unlock()
	for _, entry := range results {
		for _, packet := range entry.packets {
			s.broadcastToNearby(bolt.Dim, packet, bolt.X, bolt.Z, s.playerSnapshot())
		}
		if entry.drop {
			s.dropMobLoot(entry.kind, bolt.Dim, bolt.X, bolt.Y, bolt.Z, 0)
		}
	}
	// 玩家：5 点伤害 + 8 秒着火（原版 LightningBolt.hitEntities）。
	position := [3]float64{bolt.X, bolt.Y, bolt.Z}
	for _, player := range s.playerSnapshot() {
		if !player.isJoined() || player.dimensionID() != bolt.Dim || player.isDead() {
			continue
		}
		px, py, pz, _, _ := player.playerPosition()
		if math.Abs(px-bolt.X) > lightningHorizontalRadius ||
			math.Abs(pz-bolt.Z) > lightningHorizontalRadius ||
			py < bolt.Y-lightningVerticalRadius || py > bolt.Y+lightningVerticalRadius {
			continue
		}
		if player.fireTicks.Load() < lightningFireTicks {
			player.fireTicks.Store(lightningFireTicks)
		}
		s.damagePlayerWithMessage(player, lightningDamage, "lightning bolt", 0, s.lightningDamageTypeID, &position, "%s was struck by lightning")
	}
}
