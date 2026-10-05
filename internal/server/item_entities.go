package server

import (
	"log/slog"
	"math"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 掉落物（物品实体）：Q 键丢弃、死亡掉落、重力/滑动/水面漂浮、周期合并、
// 拾取与过期消失。
//
// 实现说明（与限制见 docs/TODO.md）：
//   - 逐轴 AABB 碰撞（0.25×0.25×0.25，与原版 ItemEntity 尺寸一致），
//     按 Y→X→Z 顺序推进并在碰撞处停下；
//   - 合并为周期性（每 4 tick、0.5 格内、需视线可通），合并时保留两者中
//     较长的拾取延迟与较短的存在时间；
//   - 水/岩浆/火/仙人掌会改变或销毁掉落物（岩浆/火/仙人掌销毁）；
//   - 掉入虚空（远低于世界底部）直接移除；
//   - 速度不持久化（重启后掉落物停在保存位置）。
const (
	// itemEntityTypeName 是掉落物的实体类型（静态注册表 minecraft:entity_type）。
	itemEntityTypeName = "minecraft:item"
	// itemPickupDelayMining 是挖掘/生物掉落物的拾取延迟（10 tick，与原版一致）。
	itemPickupDelayMining = 10
	// itemPickupDelayPlayer 是玩家丢弃/死亡掉落的拾取延迟（40 tick = 2 秒）。
	itemPickupDelayPlayer = 40
	// itemDespawnTicks 是掉落物的存活时间（6000 tick = 5 分钟）。
	itemDespawnTicks = 6000
	// itemGravityPerTick 是每 tick 的重力速度增量（方块/tick）。
	itemGravityPerTick = 0.04
	// itemAirDrag 是空中每 tick 的速度衰减。
	itemAirDrag = 0.98
	// itemGroundFriction 是落地后水平速度的衰减。
	itemGroundFriction = 0.6
	// itemMinVelocity 是水平速度归零阈值（方块/tick）。
	itemMinVelocity = 0.001
	// itemTerminalVelocity 是下落速度上限（方块/tick，近似）。
	itemTerminalVelocity = -1.6
	// itemPickupRange 是可拾取的水平距离（方块）。
	itemPickupRange = 1.0
	// itemPickupVerticalRange 是可拾取的垂直距离（方块）。
	itemPickupVerticalRange = 1.5
	// itemMergeRange 是周期合并的检查距离（方块）。
	itemMergeRange = 0.5
	// itemMergeIntervalTicks 是合并检查的间隔（与原版一致：每 4 tick）。
	itemMergeIntervalTicks = 4
	// 掉落物碰撞盒（与原版 ItemEntity 一致：0.25 见方）。
	itemHalfWidth = 0.125
	itemHeight    = 0.25
	// itemWaterBuoyancy 是水中每 tick 的向上速度增量。
	itemWaterBuoyancy = 0.02
	// itemWaterDrag 是水中每 tick 的速度衰减。
	itemWaterDrag = 0.8
)

// itemEntity 是一个掉落物。字段由 Server.entityMu 保护。
type itemEntity struct {
	ID    int32
	UUID  [16]byte
	Stack item.Stack

	X, Y, Z          float64
	VelX, VelY, VelZ float64

	// AgeTicks 是存活时间；达到 itemDespawnTicks 后消失。
	AgeTicks int
	// PickupDelayTicks 是剩余不可拾取时间。
	PickupDelayTicks int
}

// resolveItemRegistryIDs 解析掉落物需要的注册表 ID；缺项时禁用掉落物系统
// （与生物系统一致：宁可不启用，也不发送错误的协议数据）。
func (s *Server) resolveItemRegistryIDs() {
	typeID, ok := registry.StaticEntryID("minecraft:entity_type", itemEntityTypeName)
	if !ok {
		slog.Warn("缺少物品实体类型注册表项，掉落物系统已禁用")
		return
	}
	sound, ok := registry.StaticEntryID("minecraft:sound_event", "minecraft:entity.item.pickup")
	if !ok {
		slog.Warn("缺少拾取音效注册表项，掉落物系统已禁用")
		return
	}
	s.itemTypeID = typeID
	s.soundItemPickup = sound
	s.itemsEnabled = true
}

// spawnItem 生成一个掉落物：广播 Add Entity 与 Item 元数据，并立即尝试与
// 附近的同类堆叠合并（原版在生成后由周期合并处理；这里首帧不合并，
// 由 tickItems 的周期合并统一负责）。pickupDelay 为可拾取延迟（tick）。
func (s *Server) spawnItem(stack item.Stack, x, y, z, vx, vy, vz float64, pickupDelay int) *itemEntity {
	if !s.itemsEnabled || stack.IsEmpty() {
		return nil
	}
	if limit := stack.MaxStack(); stack.Count > limit {
		stack.Count = limit
	}
	e := &itemEntity{
		ID:               s.entityIDs.Add(1),
		UUID:             newEntityUUID(),
		Stack:            stack,
		X:                x,
		Y:                y,
		Z:                z,
		VelX:             vx,
		VelY:             vy,
		VelZ:             vz,
		PickupDelayTicks: pickupDelay,
	}
	s.entityMu.Lock()
	s.items[e.ID] = e
	s.entityMu.Unlock()
	s.broadcastPacket(protocol.EncodeAddEntity(e.ID, e.UUID, s.itemTypeID, e.X, e.Y, e.Z, e.VelX, e.VelY, e.VelZ, 0, 0))
	s.broadcastPacket(protocol.EncodeEntityMetadataItem(e.ID, e.Stack.AppendSlot(nil)))
	return e
}

// dropItemFromPlayer 处理 Q 键丢弃：dropStack 为 true 时丢出整组（Ctrl+Q），
// 否则只丢 1 个；掉落实体沿视线方向抛出。
func (s *Server) dropItemFromPlayer(player *session, dropStack bool) {
	if player.isDead() {
		return
	}
	if player.gameModeID() != uint8(config.GameModeSurvival) && player.gameModeID() != uint8(config.GameModeCreative) {
		return
	}
	slot := player.selectedSlot
	stack := player.inventory.Get(slot)
	if stack.IsEmpty() {
		return
	}
	dropped := stack
	if !dropStack {
		dropped.Count = 1
	}
	stack.Count -= dropped.Count
	player.inventory.Set(slot, stack)
	player.tryWrite(protocol.EncodeSetPlayerInventory(int32(slot), stack.AppendSlot(nil)))

	// 沿视线方向抛出（wiki：单位向量 x = -cos(pitch)·sin(yaw)，y = -sin(pitch)，
	// z = cos(pitch)·cos(yaw)）。
	x, y, z, yaw, pitch := player.playerPosition()
	yawRad := float64(yaw) * math.Pi / 180
	pitchRad := float64(pitch) * math.Pi / 180
	dirX := -math.Cos(pitchRad) * math.Sin(yawRad)
	dirY := -math.Sin(pitchRad)
	dirZ := math.Cos(pitchRad) * math.Cos(yawRad)
	const throwSpeed = 0.3
	s.spawnItem(dropped, x, y+1.0, z, dirX*throwSpeed, dirY*throwSpeed+0.1, dirZ*throwSpeed, itemPickupDelayPlayer)
}

// dropPlayerInventory 在玩家死亡时把整个物品栏（含护甲与副手）掉落为掉落物，
// 并同步清空后的槽位（与原版一致：死亡会失去物品）。
func (s *Server) dropPlayerInventory(player *session) {
	px, py, pz, _, _ := player.playerPosition()
	for slot := 0; slot < item.InventorySlots; slot++ {
		stack := player.inventory.Get(slot)
		if stack.IsEmpty() {
			continue
		}
		player.inventory.Set(slot, item.Empty())
		player.tryWrite(protocol.EncodeSetPlayerInventory(int32(slot), item.Empty().AppendSlot(nil)))
		// 小幅随机速度让掉落物散开。
		vx := (float64(s.nextRandom()%1000)/1000 - 0.5) * 0.2
		vz := (float64(s.nextRandom()%1000)/1000 - 0.5) * 0.2
		s.spawnItem(stack, px, py+0.5, pz, vx, 0.2, vz, itemPickupDelayPlayer)
	}
}

// itemCollides 报告掉落物碰撞盒（以位置为中心的水平 0.25、垂直 0.25）
// 是否与固体方块相交。
func (s *Server) itemCollides(x, y, z float64) bool {
	minX, maxX := x-itemHalfWidth, x+itemHalfWidth
	minZ, maxZ := z-itemHalfWidth, z+itemHalfWidth
	minY, maxY := y, y+itemHeight
	for blockX := int(math.Floor(minX)); blockX <= int(math.Floor(maxX)); blockX++ {
		for blockY := int(math.Floor(minY)); blockY <= int(math.Floor(maxY)); blockY++ {
			for blockZ := int(math.Floor(minZ)); blockZ <= int(math.Floor(maxZ)); blockZ++ {
				if s.isSolidBlock(blockX, blockY, blockZ) {
					return true
				}
			}
		}
	}
	return false
}

// moveItemAxis 沿单轴推进掉落物，遇到固体方块时停在上一步。
// 返回是否发生碰撞。以 ≤0.05 的步长推进，避免高速穿透。
func (s *Server) moveItemAxis(e *itemEntity, axis int, delta float64) bool {
	if delta == 0 {
		return false
	}
	steps := int(math.Ceil(math.Abs(delta) / 0.05))
	if steps > 16 {
		steps = 16
	}
	step := delta / float64(steps)
	for i := 0; i < steps; i++ {
		x, y, z := e.X, e.Y, e.Z
		switch axis {
		case 0:
			x += step
		case 1:
			y += step
		case 2:
			z += step
		}
		if s.itemCollides(x, y, z) {
			return true
		}
		e.X, e.Y, e.Z = x, y, z
	}
	return false
}

// itemHazardAt 报告掉落物所在位置的破坏性方块（岩浆/火/灵魂火/仙人掌）。
func (s *Server) itemHazardAt(x, y, z float64) bool {
	name, ok := s.blockNames[s.world.BlockAt(int(math.Floor(x)), int(math.Floor(y+0.1)), int(math.Floor(z)))]
	if !ok {
		return false
	}
	switch name {
	case "minecraft:lava", "minecraft:fire", "minecraft:soul_fire", "minecraft:cactus":
		return true
	}
	return false
}

// itemInWater 报告掉落物是否位于水中（漂浮：不受重力、缓慢上浮）。
func (s *Server) itemInWater(x, y, z float64) bool {
	return s.world.BlockAt(int(math.Floor(x)), int(math.Floor(y+0.1)), int(math.Floor(z))) == world.WaterBlock
}

// mergeItems 执行一次周期合并：把同类型、0.5 格内且视线可通的掉落物
// 合并进当前堆叠（上限 64），保留较长的拾取延迟与较短的存在时间。
// 调用方必须持有 entityMu。返回需要广播的元数据更新与移除列表。
func (s *Server) mergeItems(e *itemEntity) (updates [][]byte, removals []int32) {
	limit := e.Stack.MaxStack()
	if e.Stack.Count >= limit {
		return nil, nil
	}
	changed := false
	for id, other := range s.items {
		if other == e || other.Stack.ItemID != e.Stack.ItemID {
			continue
		}
		if math.Abs(other.X-e.X) > itemMergeRange || math.Abs(other.Y-e.Y) > itemMergeRange ||
			math.Abs(other.Z-e.Z) > itemMergeRange {
			continue
		}
		if other.Stack.Count >= limit {
			continue
		}
		if !s.attackPathClear(e.X, e.Y+0.1, e.Z, other.X, other.Y+0.1, other.Z) {
			continue // 隔墙不合并（与原版 Paper 的修复一致）
		}
		total := e.Stack.Count + other.Stack.Count
		e.Stack.Count = min(total, limit)
		other.Stack.Count = total - e.Stack.Count
		e.PickupDelayTicks = max(e.PickupDelayTicks, other.PickupDelayTicks)
		e.AgeTicks = min(e.AgeTicks, other.AgeTicks)
		changed = true
		if other.Stack.Count <= 0 {
			delete(s.items, id)
			removals = append(removals, id)
		} else {
			updates = append(updates, protocol.EncodeEntityMetadataItem(other.ID, other.Stack.AppendSlot(nil)))
		}
		if e.Stack.Count >= limit {
			break
		}
	}
	if changed {
		updates = append(updates, protocol.EncodeEntityMetadataItem(e.ID, e.Stack.AppendSlot(nil)))
	}
	return updates, removals
}

// itemPickup 记录一次拾取（锁外发送数据包）。
type itemPickup struct {
	player   *session
	entityID int32
	x, y, z  float64
	added    int32
	slots    []int
}

// tickItems 推进全部掉落物：重力/滑动/漂浮、过期与虚空销毁、玩家拾取。
// 由 tick() 每帧调用。
func (s *Server) tickItems(players []*session) {
	if !s.itemsEnabled {
		return
	}
	type itemMove struct {
		packet []byte
		x, z   float64
	}
	var (
		moves    []itemMove
		removals []int32
		updates  [][]byte
		pickups  []itemPickup
	)

	s.entityMu.Lock()
	for id, e := range s.items {
		e.AgeTicks++
		if e.AgeTicks >= itemDespawnTicks {
			delete(s.items, id)
			removals = append(removals, id)
			continue
		}
		if e.PickupDelayTicks > 0 {
			e.PickupDelayTicks--
		}
		// 区块未加载时冻结：不推进物理，也不强制重新加载区块。
		if !s.world.ChunkLoaded(int(math.Floor(e.X)), int(math.Floor(e.Z))) {
			continue
		}
		// 破坏性方块（岩浆/火/仙人掌）销毁掉落物。
		if s.itemHazardAt(e.X, e.Y, e.Z) {
			delete(s.items, id)
			removals = append(removals, id)
			continue
		}
		// 周期合并（每 4 tick，与原版一致）。
		if e.AgeTicks%itemMergeIntervalTicks == 0 {
			mergedUpdates, mergedRemovals := s.mergeItems(e)
			updates = append(updates, mergedUpdates...)
			removals = append(removals, mergedRemovals...)
		}

		startX, startY, startZ := e.X, e.Y, e.Z
		inWater := s.itemInWater(e.X, e.Y, e.Z)
		if inWater {
			// 漂浮：缓慢上浮 + 强阻力。
			e.VelY += itemWaterBuoyancy
			if e.VelY > 0.1 {
				e.VelY = 0.1
			}
			e.VelX *= itemWaterDrag
			e.VelZ *= itemWaterDrag
		} else {
			e.VelY -= itemGravityPerTick
			if e.VelY < itemTerminalVelocity {
				e.VelY = itemTerminalVelocity
			}
		}
		// 逐轴移动（Y → X → Z，与原版一致）：碰撞时沿该轴停下。
		if s.moveItemAxis(e, 1, e.VelY) {
			e.VelY = 0
		}
		if math.Abs(e.VelX) > itemMinVelocity {
			if !s.insideBorder(e.X+e.VelX, e.Z) || s.moveItemAxis(e, 0, e.VelX) {
				e.VelX = 0
			}
		}
		if math.Abs(e.VelZ) > itemMinVelocity {
			if !s.insideBorder(e.X, e.Z+e.VelZ) || s.moveItemAxis(e, 2, e.VelZ) {
				e.VelZ = 0
			}
		}
		// 速度衰减：空中弱阻尼，落地/水中强摩擦。
		if !inWater && !s.itemCollides(e.X, e.Y-0.05, e.Z) {
			e.VelX *= itemAirDrag
			e.VelZ *= itemAirDrag
		} else {
			e.VelX *= itemGroundFriction
			e.VelZ *= itemGroundFriction
			if math.Abs(e.VelX) <= itemMinVelocity {
				e.VelX = 0
			}
			if math.Abs(e.VelZ) <= itemMinVelocity {
				e.VelZ = 0
			}
		}
		if e.Y < float64(world.WorldMinY)-16 {
			delete(s.items, id)
			removals = append(removals, id)
			continue
		}
		if e.X != startX || e.Y != startY || e.Z != startZ {
			moves = append(moves, itemMove{
				packet: protocol.EncodeEntityPositionSync(e.ID, e.X, e.Y, e.Z, e.VelX, e.VelY, e.VelZ, 0, 0, true),
				x:      e.X,
				z:      e.Z,
			})
		}
	}
	// 拾取检查：每个存活玩家一帧内可拾取多个掉落物。
	if len(s.items) > 0 {
		for _, player := range players {
			if !player.isJoined() || player.isDead() {
				continue
			}
			switch player.gameModeID() {
			case uint8(config.GameModeCreative), uint8(config.GameModeSurvival):
			default:
				continue // 旁观/冒险模式不拾取。
			}
			px, py, pz, _, _ := player.playerPosition()
			for id, e := range s.items {
				if e.PickupDelayTicks > 0 {
					continue
				}
				if math.Abs(e.X-px) > itemPickupRange || math.Abs(e.Z-pz) > itemPickupRange {
					continue
				}
				if e.Y < py-itemPickupVerticalRange || e.Y > py+itemPickupVerticalRange {
					continue
				}
				remaining, changed := player.inventory.Add(e.Stack)
				added := e.Stack.Count - remaining
				if added <= 0 {
					continue
				}
				pickups = append(pickups, itemPickup{
					player: player, entityID: id, x: e.X, y: e.Y, z: e.Z, added: added, slots: changed,
				})
				if remaining == 0 {
					delete(s.items, id)
					removals = append(removals, id)
				} else {
					e.Stack.Count = remaining
					updates = append(updates, protocol.EncodeEntityMetadataItem(e.ID, e.Stack.AppendSlot(nil)))
				}
			}
		}
	}
	s.entityMu.Unlock()

	for _, move := range moves {
		s.broadcastToNearby(move.packet, move.x, move.z, players)
	}
	for _, packet := range updates {
		s.broadcastPacket(packet)
	}
	// 先发拾取动画（引用尚未移除的实体），再广播移除。
	for _, pickup := range pickups {
		for _, slot := range pickup.slots {
			pickup.player.tryWrite(protocol.EncodeSetPlayerInventory(int32(slot), pickup.player.inventory.Get(slot).AppendSlot(nil)))
		}
		s.writeToNearbyPlayers(protocol.EncodeCollect(pickup.entityID, pickup.player.entityID, pickup.added), pickup.x, pickup.z, nil)
		s.writeToNearbyPlayers(protocol.EncodeSoundEffect(s.soundItemPickup, protocol.SoundCategoryPlayer, pickup.x, pickup.y, pickup.z, 0.2, 2.0, 0), pickup.x, pickup.z, nil)
	}
	if len(removals) > 0 {
		s.broadcastPacket(protocol.EncodeEntityDestroy(removals))
	}
}
