//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"log/slog"
	"math"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 经验球（ExperienceOrb）：生物死亡、玩家死亡掉落，玩家靠近后拾取。
//
// 实现与 1.21.11 原版对齐（对照官方 mappings 与反编译的 ExperienceOrb）：
//
//   - 生成：经验值按原版拆分表（2477/1237/617/307/149/73/37/17/7/3/1）
//     分成多个经验球；生成时先尝试并入附近同组（实体 ID % 40）同值经验球
//     （1 格范围，并入后计数 +1、年龄归零），否则创建新经验球；
//   - 物理：重力 0.03、速度衰减 0.98（落地时按方块摩擦 0.6 × 0.98）、
//     水中上浮（dy = min(dy + 0.0005, 0.06)、水平 ×0.99）、
//     岩浆中随机向上弹跳；落地前下落速度超过重力时按 40% 速度弹起；
//     逐轴 AABB 碰撞（0.5×0.5×0.5，与原版 ExperienceOrb 尺寸一致）；
//   - 追踪：每 tick 向 8 格内最近的生存玩家加速（方向归一化后按
//     (1 - √距离/8)² × 0.1 叠加到速度；目标失效或平方距离 > 64 时重选）；
//   - 合并：每 20 tick（tickCount % 20 == 1）把 0.5 格内同组同值的经验球
//     并入自身（计数相加、年龄取较小值）；
//   - 拾取：玩家包围盒外扩 (1.0, 0.5, 1.0) 与经验球相交时拾取一次
//     （拾取后该经验球有 2 tick 冷却；每次拾取给予 `值` 点经验、计数 -1，
//     计数归零后移除）；拾取时广播 Take Item Entity 包（客户端据此播放
//     拾取音效与动画）；
//   - 生命周期：6000 tick 后消失；掉入虚空（远低于世界底部）直接移除。
//
// 已知限制（见 docs/TODO.md）：
//   - 经验球不随区块保存/加载（重启后未拾取的经验球消失）；
//   - 不实现修复（经验修补）附魔：拾取的经验直接计入玩家经验条。

// 经验球常量（与原版一致）。
const (
	// xpOrbEntityTypeName 是经验球的实体类型名（静态注册表 minecraft:entity_type）。
	xpOrbEntityTypeName = "minecraft:experience_orb"
	// xpOrbLifetime 是经验球的存活时间（6000 tick = 5 分钟）。
	xpOrbLifetime = 6000
	// xpOrbMergeInterval 是合并扫描间隔（原版 tickCount % 20 == 1）。
	xpOrbMergeInterval = 20
	// xpOrbSpawnMergeRange 是生成时并入已存在经验球的检测距离（方块）。
	xpOrbSpawnMergeRange = 1.0
	// xpOrbMergeRange 是周期合并的检测距离（方块）。
	xpOrbMergeRange = 0.5
	// xpOrbFollowRange 是追踪玩家的搜索半径（方块）。
	xpOrbFollowRange = 8.0
	// xpOrbFollowBreakSqr 是放弃当前跟随目标的平方距离（8² = 64）。
	xpOrbFollowBreakSqr = 64.0
	// xpOrbGroupsPerArea 是经验球合并分组数（实体 ID % 40）。
	xpOrbGroupsPerArea = 40
	// xpOrbGravityPerTick 是每 tick 的重力速度增量（原版 0.03）。
	xpOrbGravityPerTick = 0.03
	// xpOrbAirDrag 是空中每 tick 的速度衰减（原版 0.98）。
	xpOrbAirDrag = 0.98
	// xpOrbGroundDrag 是落地后的速度衰减（方块摩擦 0.6 × 0.98）。
	xpOrbGroundDrag = 0.6 * 0.98
	// xpOrbBounceFactor 是落地弹起的速度比例（原版 0.4）。
	xpOrbBounceFactor = 0.4
	// xpOrbPickupCooldownTicks 是同一经验球两次拾取的间隔（原版 takeXpDelay = 2）。
	xpOrbPickupCooldownTicks = 2
	// 经验球碰撞盒（与原版 ExperienceOrb 一致：0.5 见方）。
	xpOrbHalfWidth = 0.25
	xpOrbHeight    = 0.5
	// xpOrbMinVelocity 是水平速度归零阈值。
	xpOrbMinVelocity = 1e-6
)

// xpOrb 是一个经验球。字段由 Server.entityMu 保护。
type xpOrb struct {
	ID int32
	// UUID 是实体 UUID（Add Entity 与实体跟踪补发共用）。
	UUID [16]byte
	Dim  world.Dimension
	// Value 是经验球携带的基础经验值（合并时不变，同步给客户端）。
	Value int32
	// Count 是剩余可拾取次数（合并/生成并入时累加）。
	Count int32

	X, Y, Z          float64
	VelX, VelY, VelZ float64

	// AgeTicks 是存活时间；达到 xpOrbLifetime 后消失。
	AgeTicks int
	// FollowPlayerID 是当前追踪的玩家实体 ID（0 表示没有目标）。
	FollowPlayerID int32
	// PickupCooldown 是拾取冷却（>0 时不可再次被拾取）。
	PickupCooldown int
	// Grounded 记录上一帧是否落地（摩擦计算与位置同步用）。
	Grounded bool
}

// resolveXPOrbRegistryIDs 解析经验球需要的注册表 ID；缺项时禁用经验球系统。
func (s *Server) resolveXPOrbRegistryIDs() {
	typeID, ok := registry.StaticEntryID("minecraft:entity_type", xpOrbEntityTypeName)
	if !ok {
		slog.Warn("缺少经验球实体类型注册表项，经验球系统已禁用")
		return
	}
	s.xpOrbTypeID = typeID
	s.xpOrbsEnabled = true
}

// xpOrbSplitValue 返回原版经验值拆分表给出的单次拆分值
// （ExperienceOrb.getExperienceValue）。
func xpOrbSplitValue(amount int32) int32 {
	switch {
	case amount >= 2477:
		return 2477
	case amount >= 1237:
		return 1237
	case amount >= 617:
		return 617
	case amount >= 307:
		return 307
	case amount >= 149:
		return 149
	case amount >= 73:
		return 73
	case amount >= 37:
		return 37
	case amount >= 17:
		return 17
	case amount >= 7:
		return 7
	case amount >= 3:
		return 3
	}
	return 1
}

// spawnXPOrbs 在指定位置生成经验球（原版 ExperienceOrb.award）：把 amount
// 按拆分表分成多个经验球；每个经验球先尝试并入附近同值的已存在经验球。
func (s *Server) spawnXPOrbs(dim world.Dimension, x, y, z float64, amount int32) {
	if !s.xpOrbsEnabled || amount <= 0 {
		return
	}
	for amount > 0 {
		value := xpOrbSplitValue(amount)
		amount -= value
		if s.mergeXPOrbIntoExisting(dim, x, y, z, value) {
			continue
		}
		s.spawnXPOrb(dim, x, y, z, value)
	}
}

// mergeXPOrbIntoExisting 尝试把新经验值并入原版意义上的同组经验球
// （实体 ID % 40 与随机组相同、值相同、1 格内）：并入时计数 +1、年龄归零。
// 返回是否已并入。
func (s *Server) mergeXPOrbIntoExisting(dim world.Dimension, x, y, z float64, value int32) bool {
	group := int32(s.nextRandom() % xpOrbGroupsPerArea)
	s.entityMu.Lock()
	defer s.entityMu.Unlock()
	for _, orb := range s.xpOrbs {
		if orb.Dim != dim || orb.Value != value || orb.ID%xpOrbGroupsPerArea != group {
			continue
		}
		if math.Abs(orb.X-x) > xpOrbSpawnMergeRange || math.Abs(orb.Y-y) > xpOrbSpawnMergeRange ||
			math.Abs(orb.Z-z) > xpOrbSpawnMergeRange {
			continue
		}
		orb.Count++
		orb.AgeTicks = 0
		return true
	}
	return false
}

// spawnXPOrb 生成一个经验球实体并广播生成包与数据值。
func (s *Server) spawnXPOrb(dim world.Dimension, x, y, z float64, value int32) *xpOrb {
	// 初始速度沿用原版构造：x/z 为 (random×0.2 - 0.1)×2、y 为 random×0.4。
	vx := (randUnit(s)*0.2 - 0.1) * 2
	vy := randUnit(s) * 0.2 * 2
	vz := (randUnit(s)*0.2 - 0.1) * 2
	orb := &xpOrb{
		ID:    s.entityIDs.Add(1),
		UUID:  newEntityUUID(),
		Dim:   dim,
		Value: value,
		Count: 1,
		X:     x,
		Y:     y,
		Z:     z,
		VelX:  vx,
		VelY:  vy,
		VelZ:  vz,
	}
	add := protocol.EncodeAddEntity(orb.ID, orb.UUID, s.xpOrbTypeID, x, y, z, vx, vy, vz, 0, 0)
	meta := protocol.EncodeEntityMetadataVarInt(orb.ID, protocol.EntityMetadataXPValueIndex, value)
	s.entityMu.Lock()
	s.xpOrbs[orb.ID] = orb
	targets := s.markTrackedNearbyLocked(dim, x, z, nil, orb.ID)
	s.entityMu.Unlock()
	for _, player := range targets {
		player.tryWrite(add)
		player.tryWrite(meta)
	}
	return orb
}

// xpOrbBoxAt 返回经验球碰撞盒（0.5×0.5×0.5，与原版 ExperienceOrb 尺寸一致）。
func xpOrbBoxAt(x, y, z float64) world.Box {
	return world.Box{
		MinX: x - xpOrbHalfWidth, MinY: y, MinZ: z - xpOrbHalfWidth,
		MaxX: x + xpOrbHalfWidth, MaxY: y + xpOrbHeight, MaxZ: z + xpOrbHalfWidth,
	}
}

// moveXPOrbAxis 沿单轴推进经验球并在碰撞形状处停下。
// 返回是否发生碰撞。仅由 tickXPOrbs 调用（调用方持有 entityMu）。
func moveXPOrbAxis(w *world.World, orb *xpOrb, axis int, delta float64) bool {
	clipped := w.ClipMove(xpOrbBoxAt(orb.X, orb.Y, orb.Z), axis, delta)
	switch axis {
	case 0:
		orb.X += clipped
	case 1:
		orb.Y += clipped
	case 2:
		orb.Z += clipped
	}
	return clipped != delta
}

// xpOrbFluidAt 返回经验球所在位置的流体方块状态。
func xpOrbFluidAt(w *world.World, x, y, z float64) uint16 {
	return w.BlockAt(int(math.Floor(x)), int(math.Floor(y+0.1)), int(math.Floor(z)))
}

// xpOrbPickup 记录一次经验球拾取（锁外结算经验与发送数据包）。
type xpOrbPickup struct {
	player   *session
	entityID int32
	value    int32
	x, y, z  float64
}

// tickXPOrbs 推进全部经验球：物理、追踪、合并、拾取与生命周期。
// 拾取的经验在锁外结算（发送 Set Experience / Take Item Entity 包）。
// 由 tick() 每帧调用。
func (s *Server) tickXPOrbs(players []*session) {
	if !s.xpOrbsEnabled {
		return
	}
	// 复用临时切片：入口截断、退出时写回（容量保留供下一帧使用）。
	scratch := &s.tickScratch
	moves := scratch.orbMoves[:0]
	removals := scratch.orbRemovals[:0]
	pickups := scratch.orbPickups[:0]
	insides := scratch.orbInsides[:0]
	defer func() {
		scratch.orbMoves = moves
		scratch.orbRemovals = removals
		scratch.orbPickups = pickups
		scratch.orbInsides = insides
	}()
	mergeIndexBuilt := false

	s.entityMu.Lock()
	for id, orb := range s.xpOrbs {
		orb.AgeTicks++
		if orb.AgeTicks >= xpOrbLifetime {
			delete(s.xpOrbs, id)
			removals = append(removals, id)
			continue
		}
		if orb.PickupCooldown > 0 {
			orb.PickupCooldown--
		}
		w := s.worldFor(orb.Dim)
		if !w.ChunkLoaded(int(math.Floor(orb.X)), int(math.Floor(orb.Z))) {
			continue // 区块未加载时冻结
		}
		if orb.AgeTicks%xpOrbMergeInterval == 1 {
			if !mergeIndexBuilt {
				s.rebuildXPOrbMergeIndex()
				mergeIndexBuilt = true
			}
			removals = s.mergeXPOrbs(orb, removals)
		}
		startX, startY, startZ := orb.X, orb.Y, orb.Z

		// 流体与重力（顺序与原版一致：水 → 重力 → 岩浆随机跳）。
		fluid := xpOrbFluidAt(w, orb.X, orb.Y, orb.Z)
		if isWaterState(fluid) {
			orb.VelY = math.Min(orb.VelY+0.0005, 0.06)
			orb.VelX *= 0.99
			orb.VelZ *= 0.99
		} else {
			orb.VelY -= xpOrbGravityPerTick
		}
		if isLavaState(fluid) {
			orb.VelX = (randUnit(s) - randUnit(s)) * 0.2
			orb.VelY = 0.2
			orb.VelZ = (randUnit(s) - randUnit(s)) * 0.2
		}

		// 追踪最近的玩家，然后按追踪方向加速。
		s.updateXPOrbFollow(orb, players)
		applyXPOrbFollowVelocity(orb, players)

		// 逐轴移动（Y → X → Z）。
		deltaYBefore := orb.VelY
		bounced := false
		if moveXPOrbAxis(w, orb, 1, orb.VelY) {
			bounced = orb.VelY < 0
			orb.Grounded = bounced
			orb.VelY = 0
		} else {
			orb.Grounded = false
		}
		if math.Abs(orb.VelX) > xpOrbMinVelocity {
			if !s.insideBorder(orb.X+orb.VelX, orb.Z) || moveXPOrbAxis(w, orb, 0, orb.VelX) {
				orb.VelX = 0
			}
		}
		if math.Abs(orb.VelZ) > xpOrbMinVelocity {
			if !s.insideBorder(orb.X, orb.Z+orb.VelZ) || moveXPOrbAxis(w, orb, 2, orb.VelZ) {
				orb.VelZ = 0
			}
		}
		// 速度衰减：空中 0.98，落地按方块摩擦 ×0.98。
		drag := xpOrbAirDrag
		if orb.Grounded {
			drag = xpOrbGroundDrag
		}
		orb.VelX *= drag
		orb.VelY *= drag
		orb.VelZ *= drag
		// 落地弹起（原版 verticalCollisionBelow：落地前下落速度超过重力时）。
		if bounced && deltaYBefore < -xpOrbGravityPerTick {
			orb.VelY = -deltaYBefore * xpOrbBounceFactor
		}
		if orb.Y < float64(world.WorldMinY)-16 {
			delete(s.xpOrbs, id)
			removals = append(removals, id)
			continue
		}
		if orb.X != startX || orb.Y != startY || orb.Z != startZ {
			moves = append(moves, pendingPacket{
				dim:    orb.Dim,
				packet: protocol.EncodeEntityPositionSync(orb.ID, orb.X, orb.Y, orb.Z, orb.VelX, orb.VelY, orb.VelZ, 0, 0, orb.Grounded),
				x:      orb.X,
				z:      orb.Z,
			})
			insides = append(insides, insideCheck{dim: orb.Dim, box: xpOrbBoxAt(orb.X, orb.Y, orb.Z), w: w})
		}
	}
	// 拾取检查：每个存活玩家一帧内可拾取多个经验球（仅同维度）。
	if len(s.xpOrbs) > 0 {
		for _, player := range players {
			if !player.isJoined() || player.isDead() {
				continue
			}
			switch player.gameModeID() {
			case uint8(config.GameModeCreative), uint8(config.GameModeSurvival):
			default:
				continue // 旁观/冒险模式不拾取经验球
			}
			dim := player.dimensionID()
			x, y, z, _, _ := player.playerPosition()
			box := playerBox(x, y, z)
			box.MinX -= 1.0
			box.MaxX += 1.0
			box.MinY -= 0.5
			box.MaxY += 0.5
			box.MinZ -= 1.0
			box.MaxZ += 1.0
			for id, orb := range s.xpOrbs {
				if orb.Dim != dim || orb.PickupCooldown > 0 {
					continue
				}
				if !boxesOverlap(box, xpOrbBoxAt(orb.X, orb.Y, orb.Z)) {
					continue
				}
				orb.Count--
				orb.PickupCooldown = xpOrbPickupCooldownTicks
				pickups = append(pickups, xpOrbPickup{
					player: player, entityID: id, value: orb.Value, x: orb.X, y: orb.Y, z: orb.Z,
				})
				if orb.Count <= 0 {
					delete(s.xpOrbs, id)
					removals = append(removals, id)
				}
			}
		}
	}
	s.entityMu.Unlock()

	for _, move := range moves {
		s.writeToNearbyPlayers(move.packet, move.dim, move.x, move.z, nil)
	}
	// 实体进入方块（压力板）通知：仅在移动时检查（与原版一致，锁外执行）。
	for _, inside := range insides {
		s.entityInsideBlocks(inside.w, inside.dim, inside.box)
	}
	// 拾取动画必须在实体移除之前广播（否则客户端找不到实体，不播放音效与动画）。
	for _, pickup := range pickups {
		s.writeToNearbyPlayers(protocol.EncodeCollect(pickup.entityID, pickup.player.entityID, 1),
			pickup.player.dimensionID(), pickup.x, pickup.z, nil)
	}
	if len(removals) > 0 {
		s.broadcastPacket(protocol.EncodeEntityDestroy(removals))
	}
	// 拾取结算：发放经验并同步经验条（同一玩家的多次拾取先合并）。
	gains := scratch.orbGains[:0]
	for _, pickup := range pickups {
		merged := false
		for i := range gains {
			if gains[i].player == pickup.player {
				gains[i].amount += pickup.value
				merged = true
				break
			}
		}
		if !merged {
			gains = append(gains, xpGain{player: pickup.player, amount: pickup.value})
		}
	}
	for _, gain := range gains {
		bar, level, total := gain.player.addExperience(gain.amount)
		gain.player.tryWrite(protocol.EncodeSetExperience(bar, level, total))
	}
	scratch.orbGains = gains
}

// mergeXPOrbs 执行一次合并扫描（原版 ExperienceOrb 周期性的
// tryMergeToExisting）：把 0.5 格内同组（实体 ID % 40）同值的经验球并入自身。
// 候选来自本帧的空间索引（见 rebuildXPOrbMergeIndex），被并入的条目通过
// “仍在本表中”校验跳过。被合并掉的实体 ID 就地追加到 removals（调用方广播移除）。
// 调用方持有 entityMu。
func (s *Server) mergeXPOrbs(orb *xpOrb, removals []int32) []int32 {
	group := int32(orb.ID % xpOrbGroupsPerArea)
	index := s.tickScratch.orbBuckets
	baseX, baseZ := entityBucketCoord(orb.X), entityBucketCoord(orb.Z)
	for dcx := -1; dcx <= 1; dcx++ {
		for dcz := -1; dcz <= 1; dcz++ {
			key := entityBucketKey(orb.Dim, baseX+dcx, baseZ+dcz)
			for _, other := range index[key] {
				if other == orb || other.Dim != orb.Dim || other.Value != orb.Value {
					continue
				}
				if other.ID%xpOrbGroupsPerArea != group {
					continue
				}
				// 索引是建桶时刻的快照：已被合并掉的条目不再处理。
				if current, ok := s.xpOrbs[other.ID]; !ok || current != other {
					continue
				}
				if math.Abs(other.X-orb.X) > xpOrbMergeRange || math.Abs(other.Y-orb.Y) > xpOrbMergeRange ||
					math.Abs(other.Z-orb.Z) > xpOrbMergeRange {
					continue
				}
				orb.Count += other.Count
				orb.AgeTicks = min(orb.AgeTicks, other.AgeTicks)
				delete(s.xpOrbs, other.ID)
				removals = append(removals, other.ID)
			}
		}
	}
	return removals
}

// rebuildXPOrbMergeIndex 重建经验球合并空间索引（桶策略与掉落物相同，
// 见 rebuildItemMergeIndex）。调用方必须持有 entityMu。
func (s *Server) rebuildXPOrbMergeIndex() {
	m := s.tickScratch.orbBuckets
	if m == nil || len(m) > 4*len(s.xpOrbs)+64 {
		m = make(map[uint64][]*xpOrb, len(s.xpOrbs))
		s.tickScratch.orbBuckets = m
	} else {
		for key, bucket := range m {
			m[key] = bucket[:0]
		}
	}
	for _, orb := range s.xpOrbs {
		key := entityBucketKey(orb.Dim, entityBucketCoord(orb.X), entityBucketCoord(orb.Z))
		m[key] = append(m[key], orb)
	}
}

// updateXPOrbFollow 更新经验球追踪的玩家：目标失效（死亡/跨维度/离开 64
// 平方格）时重新选择 8 格内最近的生存玩家（旁观者除外）。
func (s *Server) updateXPOrbFollow(orb *xpOrb, players []*session) {
	if orb.FollowPlayerID != 0 {
		target := findSessionByEntityID(players, orb.FollowPlayerID)
		if target != nil && target.dimensionID() == orb.Dim && !target.isDead() {
			x, y, z, _, _ := target.playerPosition()
			dx, dy, dz := x-orb.X, y-orb.Y, z-orb.Z
			if dx*dx+dy*dy+dz*dz <= xpOrbFollowBreakSqr {
				return
			}
		}
		orb.FollowPlayerID = 0
	}
	bestID := int32(0)
	bestSqr := xpOrbFollowRange * xpOrbFollowRange
	for _, player := range players {
		if !player.isJoined() || player.isDead() || player.dimensionID() != orb.Dim {
			continue
		}
		if player.gameModeID() == uint8(config.GameModeSpectator) {
			continue
		}
		x, y, z, _, _ := player.playerPosition()
		dx, dy, dz := x-orb.X, y-orb.Y, z-orb.Z
		sqr := dx*dx + dy*dy + dz*dz
		if sqr < bestSqr {
			bestSqr = sqr
			bestID = player.entityID
		}
	}
	orb.FollowPlayerID = bestID
}

// applyXPOrbFollowVelocity 让经验球向追踪目标加速（原版逻辑：
// 方向归一化后按 (1 - √距离/8)² × 0.1 叠加到速度上）。
func applyXPOrbFollowVelocity(orb *xpOrb, players []*session) {
	if orb.FollowPlayerID == 0 {
		return
	}
	target := findSessionByEntityID(players, orb.FollowPlayerID)
	if target == nil {
		return
	}
	x, y, z, _, _ := target.playerPosition()
	tx := x - orb.X
	ty := y + 0.9 - orb.Y // 原版目标高度为玩家位置 + 身高/2
	tz := z - orb.Z
	dist := math.Sqrt(tx*tx + ty*ty + tz*tz)
	if dist < 1e-4 {
		return
	}
	scale := 1 - math.Sqrt(dist)/xpOrbFollowRange
	factor := scale * scale * 0.1
	orb.VelX += tx / dist * factor
	orb.VelY += ty / dist * factor
	orb.VelZ += tz / dist * factor
}

// findSessionByEntityID 在玩家快照中按实体 ID 查找会话。
func findSessionByEntityID(players []*session, entityID int32) *session {
	for _, player := range players {
		if player.entityID == entityID {
			return player
		}
	}
	return nil
}

// randUnit 返回 [0,1) 的伪随机数（无锁；仅使用原子随机状态）。
func randUnit(s *Server) float64 {
	return float64(s.nextRandom()%1000000) / 1000000
}
