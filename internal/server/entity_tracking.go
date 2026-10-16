//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"gmcs/internal/protocol"
	"gmcs/internal/world"
)

// 实体跟踪生命周期：为每名玩家维护“客户端当前持有的实体集合”，
// 在实体进入/离开范围时补发 Add Entity / Remove Entities。
//
// 原实现只在实体生成的一瞬间把它发给 64 格内的玩家，由此带来三类问题：
//   - 之后才靠近的玩家永远收不到 Add Entity（生物/掉落物/箭/TNT/经验球/
//     玩家在多人游戏里互相不可见）；
//   - 离开范围的实体不会被移除，客户端留下冻结的“幽灵实体”；
//   - 维度切换/重生后客户端清空实体列表，但没有重新补发。
//
// 分工：
//   - 生成路径（spawnItem/addMob/fireArrow/spawnXPOrb/spawnTnt 等）在实体
//     入表时调用 markTrackedNearbyLocked 登记“当时在范围内”的玩家并立即
//     发送（低延迟）；
//   - tickEntityTracking 每 tick 对每名玩家做增量同步：补发进入范围的、
//     移除离开范围的，并为“跟踪集中已不存在”的实体补发一次移除
//     （幂等：客户端对未知实体 ID 的移除包忽略，可治愈销毁包与本帧
//     补发包发生线上乱序的极端情形）；
//   - 维度切换/重生时先 suspend（客户端重建世界的窗口内不下发），
//     写完全部重置包后 resume，由随后的一帧统一补发。
//
// 锁序约定：entityMu → s.mu（playerSnapshot）→ stateMu（玩家位置/维度）。
// 发送（tryWrite 会同步写连接）一律在 entityMu 之外执行。
const (
	// entityTrackingRange 是实体下发范围（与实体移动广播一致：64 格）。
	entityTrackingRange = playerMoveBroadcastRange
	// entityTrackingRemoveRange 是移除范围（下发范围 + 16 格滞回，
	// 避免实体在边界附近抖动时反复 Add/Remove）。
	entityTrackingRemoveRange = entityTrackingRange + 16
)

// sessionTracker 是单名玩家已下发的实体集合，受 Server.entityMu 保护。
type sessionTracker struct {
	// dim 是跟踪集所属维度；维度变化（未走 suspend 路径时）整体清空重建。
	dim world.Dimension
	// suspended 表示客户端正处于维度切换/重生的“世界重建”窗口：
	// 窗口内不向该玩家下发实体，等 resumeEntityTracking 后统一补发。
	suspended bool
	// ids 是客户端当前持有的实体 ID 集合。
	ids map[int32]struct{}
}

// trackAddOp 是一条待发送的实体加入通知（包只读共享，可在锁外发送）。
type trackAddOp struct {
	player  *session
	packets [3][]byte
	count   int
}

// trackRemoveOp 是一条待发送的实体移除通知（已按玩家批量编码）。
type trackRemoveOp struct {
	player *session
	packet []byte
}

// trackPos 是跟踪存活索引中的实体水平位置。
type trackPos struct {
	x, z float64
}

// ensureTrackerLocked 返回玩家的跟踪状态（按需创建）。调用方必须持有 entityMu。
func (s *Server) ensureTrackerLocked(player *session) *sessionTracker {
	tr := s.entityTrack[player]
	if tr == nil {
		tr = &sessionTracker{ids: make(map[int32]struct{})}
		s.entityTrack[player] = tr
	}
	return tr
}

// suspendEntityTracking 暂停某玩家的实体跟踪（维度切换/重生的世界重建窗口，
// 必须在发送 Respawn 之前调用）。
func (s *Server) suspendEntityTracking(player *session) {
	s.entityMu.Lock()
	tr := s.ensureTrackerLocked(player)
	tr.suspended = true
	clear(tr.ids)
	s.entityMu.Unlock()
}

// resumeEntityTracking 结束暂停并切换到新维度：跟踪集清空，
// 由随后的一帧 tickEntityTracking 统一补发范围内的实体。
func (s *Server) resumeEntityTracking(player *session, dim world.Dimension) {
	s.entityMu.Lock()
	tr := s.ensureTrackerLocked(player)
	tr.suspended = false
	tr.dim = dim
	clear(tr.ids)
	s.entityMu.Unlock()
}

// dropEntityTracking 丢弃玩家的跟踪状态（断开连接时）。
func (s *Server) dropEntityTracking(player *session) {
	s.entityMu.Lock()
	delete(s.entityTrack, player)
	s.entityMu.Unlock()
}

// markTrackedNearbyLocked 把实体登记进附近玩家的跟踪集，并返回需要发送
// Add Entity 的目标玩家（同维度、已完成进入世界、水平距离 ≤ 64 格）。
// 已经跟踪该实体的玩家不会被返回（避免重复下发）。
// 调用方必须持有 entityMu，并在释放锁之后向返回的玩家发送数据包。
//
// 处于 suspend 窗口的玩家不登记（resume 后会由 tickEntityTracking 补发）。
func (s *Server) markTrackedNearbyLocked(dim world.Dimension, x, z float64, except *session, id int32) []*session {
	var targets []*session
	rangeSq := float64(entityTrackingRange * entityTrackingRange)
	for _, player := range s.playerSnapshot() {
		if player == except || !player.isJoined() || player.dimensionID() != dim {
			continue
		}
		px, _, pz, _, _ := player.playerPosition()
		dx := px - x
		dz := pz - z
		if dx*dx+dz*dz > rangeSq {
			continue
		}
		tr := s.ensureTrackerLocked(player)
		if tr.suspended {
			continue
		}
		if _, ok := tr.ids[id]; ok {
			continue
		}
		tr.ids[id] = struct{}{}
		targets = append(targets, player)
	}
	return targets
}

// untrackEntityEverywhere 从全部玩家的跟踪集中移除某个实体 ID。
// 玩家跨维度传送时调用：旧维度的观察者已收到 Remove Entities，
// 清掉跟踪记录以便玩家回到旧维度时能重新下发。
func (s *Server) untrackEntityEverywhere(id int32) {
	s.entityMu.Lock()
	for _, tr := range s.entityTrack {
		delete(tr.ids, id)
	}
	s.entityMu.Unlock()
}

// sendPlayerEntityAdd 把玩家实体（含皮肤层与装备）下发给附近玩家，
// 并把实体登记进他们的跟踪集（进入世界、维度切换与其他实体同一套逻辑）。
func (s *Server) sendPlayerEntityAdd(player *session, dim world.Dimension, x, z float64) {
	packets := [3][]byte{
		s.playerSpawnPacket(player),
		s.playerSkinPacket(player),
		s.playerEquipmentPacket(player),
	}
	s.entityMu.Lock()
	targets := s.markTrackedNearbyLocked(dim, x, z, player, player.entityID)
	s.entityMu.Unlock()
	for _, other := range targets {
		for _, packet := range packets {
			if packet == nil {
				break
			}
			other.tryWrite(packet)
		}
	}
}

// markVisiblePlayersForJoin 返回新加入玩家视野内（≤ 64 格、同维度）的其他
// 玩家，并把这些玩家实体登记进新玩家的跟踪集（避免 tickEntityTracking
// 在下一帧重复下发）。
func (s *Server) markVisiblePlayersForJoin(player *session, others []*session, dim world.Dimension, x, z float64) []*session {
	rangeSq := float64(entityTrackingRange * entityTrackingRange)
	s.entityMu.Lock()
	tr := s.ensureTrackerLocked(player)
	tr.suspended = false
	tr.dim = dim
	var visible []*session
	for _, other := range others {
		if !other.isJoined() || other.dimensionID() != dim {
			continue
		}
		ox, _, oz, _, _ := other.playerPosition()
		dx := ox - x
		dz := oz - z
		if dx*dx+dz*dz > rangeSq {
			continue
		}
		if _, ok := tr.ids[other.entityID]; ok {
			continue
		}
		tr.ids[other.entityID] = struct{}{}
		visible = append(visible, other)
	}
	s.entityMu.Unlock()
	return visible
}

// trackedPlayer 是 tick 跟踪的玩家快照（每帧只读取一次位置与维度）。
type trackedPlayer struct {
	player *session
	dim    world.Dimension
	x, z   float64
	tr     *sessionTracker
}

// inTrackingRange 报告水平距离是否在给定的（含端点）范围内（先做盒式粗筛）。
func inTrackingRange(px, pz, x, z float64, limit float64) bool {
	dx := x - px
	dz := z - pz
	if dx > limit || dx < -limit || dz > limit || dz < -limit {
		return false
	}
	return dx*dx+dz*dz <= limit*limit
}

// tickEntityTracking 为每名玩家增量同步实体集合（加入/移除/治愈）。
// 由 tick() 每帧调用。实体编码在 entityMu 内完成（此时实体字段不会被
// 并发修改），发送在锁外进行（tryWrite 会同步写连接，不能持锁阻塞）。
func (s *Server) tickEntityTracking(players []*session) {
	scratch := &s.tickScratch
	adds := scratch.trackAdds[:0]
	removes := scratch.trackRemoves[:0]
	tracked := scratch.trackPlayers[:0]
	mobs := scratch.trackMobs[:0]
	items := scratch.trackItems[:0]
	arrows := scratch.trackArrows[:0]
	tnts := scratch.trackTnts[:0]
	orbs := scratch.trackOrbs[:0]
	defer func() {
		scratch.trackAdds = adds
		scratch.trackRemoves = removes
		scratch.trackPlayers = tracked
		scratch.trackMobs = mobs
		scratch.trackItems = items
		scratch.trackArrows = arrows
		scratch.trackTnts = tnts
		scratch.trackOrbs = orbs
	}()

	s.entityMu.Lock()
	// 存活索引（按维度分桶）：id → 水平位置。用于“离开范围”判定与
	// “已不存在”的清理（清理时同样补发一次移除，见文件头说明）。
	alive := &scratch.trackAlive
	for i := range alive {
		if alive[i] == nil {
			alive[i] = make(map[int32]trackPos)
		} else {
			clear(alive[i])
		}
	}
	for _, m := range s.mobs {
		mobs = append(mobs, m)
		alive[m.Dim][m.ID] = trackPos{x: m.X, z: m.Z}
	}
	for _, e := range s.items {
		items = append(items, e)
		alive[e.Dim][e.ID] = trackPos{x: e.X, z: e.Z}
	}
	if s.arrowsEnabled {
		for _, a := range s.arrows {
			arrows = append(arrows, a)
			alive[a.Dim][a.ID] = trackPos{x: a.X, z: a.Z}
		}
	}
	if s.tntsEnabled {
		for _, t := range s.tnts {
			tnts = append(tnts, t)
			alive[t.Dim][t.ID] = trackPos{x: t.X, z: t.Z}
		}
	}
	if s.xpOrbsEnabled {
		for _, o := range s.xpOrbs {
			orbs = append(orbs, o)
			alive[o.Dim][o.ID] = trackPos{x: o.X, z: o.Z}
		}
	}

	// 玩家快照（既是跟踪集的归属者，也是彼此可见性的“实体”来源）：
	// 位置与维度各读取一次。
	for _, player := range players {
		if !player.isJoined() {
			continue
		}
		x, _, z, _, _ := player.playerPosition()
		dim := player.dimensionID()
		tr := s.ensureTrackerLocked(player)
		if tr.dim != dim {
			// 未走 suspend 路径的维度变化：整体重建（正常情况下
			// teleportTo 会先 suspend/resume）。
			clear(tr.ids)
			tr.dim = dim
		}
		alive[dim][player.entityID] = trackPos{x: x, z: z}
		if tr.suspended {
			continue
		}
		tracked = append(tracked, trackedPlayer{player: player, dim: dim, x: x, z: z, tr: tr})
	}

	// 加入：非玩家实体为外层（同一实体只编码一次），玩家为内层扫描。
	// 只处理“未跟踪且进入 64 格”的组合。
	for _, m := range mobs {
		if m.Dead {
			continue // 死亡动画期间不再为新玩家补发（与原加入逻辑一致）。
		}
		var p1, p2 []byte
		built := false
		for i := range tracked {
			tp := &tracked[i]
			if tp.dim != m.Dim || !inTrackingRange(tp.x, tp.z, m.X, m.Z, entityTrackingRange) {
				continue
			}
			if _, ok := tp.tr.ids[m.ID]; ok {
				continue
			}
			if !built {
				p1 = protocol.EncodeAddEntity(m.ID, m.UUID, m.TypeID, m.X, m.Y, m.Z, 0, 0, 0, m.Yaw, m.Pitch)
				if m.FireTicks > 0 {
					p2 = protocol.EncodeEntityMetadataByte(m.ID, 0, 1)
				}
				built = true
			}
			tp.tr.ids[m.ID] = struct{}{}
			op := trackAddOp{player: tp.player, count: 1}
			op.packets[0] = p1
			if p2 != nil {
				op.packets[1] = p2
				op.count = 2
			}
			adds = append(adds, op)
		}
	}
	for _, e := range items {
		var p1, p2 []byte
		built := false
		for i := range tracked {
			tp := &tracked[i]
			if tp.dim != e.Dim || !inTrackingRange(tp.x, tp.z, e.X, e.Z, entityTrackingRange) {
				continue
			}
			if _, ok := tp.tr.ids[e.ID]; ok {
				continue
			}
			if !built {
				p1 = protocol.EncodeAddEntity(e.ID, e.UUID, s.itemTypeID, e.X, e.Y, e.Z, e.VelX, e.VelY, e.VelZ, 0, 0)
				p2 = protocol.EncodeEntityMetadataItem(e.ID, e.Stack.AppendSlot(nil))
				built = true
			}
			tp.tr.ids[e.ID] = struct{}{}
			adds = append(adds, trackAddOp{player: tp.player, packets: [3][]byte{p1, p2}, count: 2})
		}
	}
	for _, a := range arrows {
		var p1 []byte
		built := false
		for i := range tracked {
			tp := &tracked[i]
			if tp.dim != a.Dim || !inTrackingRange(tp.x, tp.z, a.X, a.Z, entityTrackingRange) {
				continue
			}
			if _, ok := tp.tr.ids[a.ID]; ok {
				continue
			}
			if !built {
				p1 = protocol.EncodeAddEntity(a.ID, a.UUID, s.arrowTypeID, a.X, a.Y, a.Z, a.VelX, a.VelY, a.VelZ, 0, 0)
				built = true
			}
			tp.tr.ids[a.ID] = struct{}{}
			adds = append(adds, trackAddOp{player: tp.player, packets: [3][]byte{p1}, count: 1})
		}
	}
	for _, t := range tnts {
		var p1, p2, p3 []byte
		built := false
		for i := range tracked {
			tp := &tracked[i]
			if tp.dim != t.Dim || !inTrackingRange(tp.x, tp.z, t.X, t.Z, entityTrackingRange) {
				continue
			}
			if _, ok := tp.tr.ids[t.ID]; ok {
				continue
			}
			if !built {
				p1 = protocol.EncodeAddEntity(t.ID, t.UUID, s.tntTypeID, t.X, t.Y, t.Z, t.VelX, t.VelY, t.VelZ, 0, 0)
				p2 = protocol.EncodeEntityMetadataVarInt(t.ID, 8, int32(t.Fuse))
				p3 = protocol.EncodeEntityMetadataVarInt(t.ID, 9, int32(tntBlockState()))
				built = true
			}
			tp.tr.ids[t.ID] = struct{}{}
			adds = append(adds, trackAddOp{player: tp.player, packets: [3][]byte{p1, p2, p3}, count: 3})
		}
	}
	for _, o := range orbs {
		var p1, p2 []byte
		built := false
		for i := range tracked {
			tp := &tracked[i]
			if tp.dim != o.Dim || !inTrackingRange(tp.x, tp.z, o.X, o.Z, entityTrackingRange) {
				continue
			}
			if _, ok := tp.tr.ids[o.ID]; ok {
				continue
			}
			if !built {
				p1 = protocol.EncodeAddEntity(o.ID, o.UUID, s.xpOrbTypeID, o.X, o.Y, o.Z, o.VelX, o.VelY, o.VelZ, 0, 0)
				p2 = protocol.EncodeEntityMetadataVarInt(o.ID, protocol.EntityMetadataXPValueIndex, o.Value)
				built = true
			}
			tp.tr.ids[o.ID] = struct{}{}
			adds = append(adds, trackAddOp{player: tp.player, packets: [3][]byte{p1, p2}, count: 2})
		}
	}
	// 玩家实体的加入：来源（被观察者）为外层，只发给尚未跟踪的其他玩家。
	for i := range tracked {
		q := &tracked[i]
		var p1, p2, p3 []byte
		built := false
		for j := range tracked {
			tp := &tracked[j]
			if tp.player == q.player || tp.dim != q.dim || !inTrackingRange(tp.x, tp.z, q.x, q.z, entityTrackingRange) {
				continue
			}
			if _, ok := tp.tr.ids[q.player.entityID]; ok {
				continue
			}
			if !built {
				p1 = s.playerSpawnPacket(q.player)
				p2 = s.playerSkinPacket(q.player)
				p3 = s.playerEquipmentPacket(q.player)
				built = true
			}
			tp.tr.ids[q.player.entityID] = struct{}{}
			adds = append(adds, trackAddOp{player: tp.player, packets: [3][]byte{p1, p2, p3}, count: 3})
		}
	}

	// 移除与清理：按玩家迭代跟踪集。
	for i := range tracked {
		tp := &tracked[i]
		var removeIDs []int32
		for id := range tp.tr.ids {
			pos, ok := alive[tp.dim][id]
			if !ok {
				// 实体已销毁/已离开该维度：其销毁路径已向所有玩家广播
				// Remove Entities，这里补发一次幂等移除并清理跟踪集
				// （可治愈“销毁包先于本帧补发的加入包到达”的极端乱序）。
				removeIDs = append(removeIDs, id)
				delete(tp.tr.ids, id)
				continue
			}
			if !inTrackingRange(tp.x, tp.z, pos.x, pos.z, entityTrackingRemoveRange) {
				removeIDs = append(removeIDs, id)
				delete(tp.tr.ids, id)
			}
		}
		if len(removeIDs) > 0 {
			removes = append(removes, trackRemoveOp{player: tp.player, packet: protocol.EncodeRemoveEntities(removeIDs)})
		}
	}
	s.entityMu.Unlock()

	// 发送阶段在锁外完成，不阻塞其他实体处理。
	for _, op := range adds {
		for i := 0; i < op.count; i++ {
			op.player.tryWrite(op.packets[i])
		}
	}
	for _, op := range removes {
		op.player.tryWrite(op.packet)
	}
}
