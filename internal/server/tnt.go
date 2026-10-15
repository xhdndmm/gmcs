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

// 点燃的 TNT 实体（对照 1.21.11 反编译的 PrimedTnt 实现）。
//
//   - 生成位置 = 方块中心 x+0.5、z+0.5（竖直方向保持方块底），
//     初速度 = ((rand×2−1)×0.02, 0.2, (rand×2−1)×0.02)，引信 80 tick；
//   - 每 tick：速度 −0.04 重力、整体 ×0.98 阻力、碰撞移动（复用世界碰撞）；
//   - 引信耗尽时以爆炸半径 4 结算（方块销毁与伤害模型见 arrows.go 的 explode；
//     威力按原版 4 与苦力怕 3 的比例映射为 32）；
//   - 元数据：fuse（索引 8，VarInt）、block_state（索引 9，VarInt）——
//     后者让客户端渲染出 TNT 方块外观。
const (
	// tntFuseTicks 是引信时长（原版 80 tick = 4 秒）。
	tntFuseTicks = 80
	// tntExplosionRadius 是爆炸半径（原版爆炸威力 4）。
	tntExplosionRadius = 4.0
	// tntExplosionPower 是伤害标定（按原版威力比例映射；见 explode 的注释）。
	tntExplosionPower = 32.0
)

// primedTnt 是一只点燃的 TNT。字段由 Server.entityMu 保护。
type primedTnt struct {
	ID   int32
	UUID [16]byte
	Dim  world.Dimension

	X, Y, Z          float64
	VelX, VelY, VelZ float64
	// Fuse 是剩余引信 tick；<= 0 时爆炸。
	Fuse int
}

// tntTypeName 是 TNT 实体的静态注册表条目名。
const tntTypeName = "minecraft:tnt"

// resolveTntRegistryIDs 解析 TNT 系统需要的注册表 ID；缺项时禁用（发射器
// 的 TNT 行为会回退为弹出）。
func (s *Server) resolveTntRegistryIDs() {
	typeID, ok := registry.StaticEntryID("minecraft:entity_type", tntTypeName)
	if !ok {
		slog.Warn("缺少 TNT 实体类型注册表项，TNT 行为已禁用")
		return
	}
	s.tntTypeID = typeID
	if sound, ok := registry.StaticEntryID("minecraft:sound_event", "minecraft:entity.tnt.primed"); ok {
		s.soundTntPrime = sound
	}
	s.tntsEnabled = true
	s.tnts = make(map[int32]*primedTnt)
}

// primeTnt 在发射器发射位置生成一只点燃的 TNT（原版 lg$14 行为）。
func (s *Server) primeTnt(dim world.Dimension, pos [3]int, facing string) bool {
	if !s.tntsEnabled {
		return false
	}
	offset := dispenserFacingOffset(facing)
	x := float64(pos[0]+offset[0]) + 0.5
	y := float64(pos[1] + offset[1])
	z := float64(pos[2]+offset[2]) + 0.5
	if _, ok := world.SectionIndex(int(math.Floor(y))); !ok {
		return false
	}
	s.spawnPrimedTnt(dim, x, y, z, true)
	return true
}

// primeTntBlock 把方块位置的 TNT 变成点燃的实体并移除方块
// （原版 TntBlock.prime）。成功返回 true。
func (s *Server) primeTntBlock(dim world.Dimension, w *world.World, x, y, z int) bool {
	if !s.tntsEnabled {
		return false
	}
	if !w.SetBlock(x, y, z, world.AirBlock) {
		return false
	}
	s.broadcastBlockUpdate(dim, x, y, z, int32(world.AirBlock))
	s.updateRedstoneAround(dim, x, y, z)
	s.spawnPrimedTnt(dim, float64(x)+0.5, float64(y), float64(z)+0.5, true)
	return true
}

// spawnPrimedTnt 生成一只点燃的 TNT 实体并广播（randomize 时按原版赋予
// 随机水平初速度）。
func (s *Server) spawnPrimedTnt(dim world.Dimension, x, y, z float64, randomize bool) *primedTnt {
	vx, vz := 0.0, 0.0
	vy := 0.2
	if randomize {
		vx = (float64(s.nextRandom()%2_000_000)/1_000_000 - 1) * 0.02
		vz = (float64(s.nextRandom()%2_000_000)/1_000_000 - 1) * 0.02
	}
	t := &primedTnt{
		ID:   s.entityIDs.Add(1),
		UUID: newEntityUUID(),
		Dim:  dim,
		X:    x, Y: y, Z: z,
		VelX: vx, VelY: vy, VelZ: vz,
		Fuse: tntFuseTicks,
	}
	add := protocol.EncodeAddEntity(t.ID, t.UUID, s.tntTypeID, x, y, z, vx, vy, vz, 0, 0)
	metaFuse := protocol.EncodeEntityMetadataVarInt(t.ID, 8, int32(tntFuseTicks))
	metaBlock := protocol.EncodeEntityMetadataVarInt(t.ID, 9, int32(tntBlockState()))
	s.entityMu.Lock()
	s.tnts[t.ID] = t
	s.entityMu.Unlock()
	s.writeToNearbyPlayers(add, dim, x, z, nil)
	s.writeToNearbyPlayers(metaFuse, dim, x, z, nil)
	s.writeToNearbyPlayers(metaBlock, dim, x, z, nil)
	if s.soundTntPrime != 0 {
		s.writeToNearbyPlayers(protocol.EncodeSoundEffect(s.soundTntPrime, protocol.SoundCategoryBlock, x, y, z, 1, 1, 0), dim, x, z, nil)
	}
	return t
}

// tntBlockState 返回 TNT 方块的默认状态 ID（客户端渲染用；缺失时返回 0）。
func tntBlockState() uint16 {
	if state, ok := registry.BlockStateIDs["minecraft:tnt"]; ok {
		return state
	}
	return 0
}

// tickTnts 推进全部点燃的 TNT：物理与引信，引信耗尽时爆炸。
func (s *Server) tickTnts(players []*session) {
	if !s.tntsEnabled {
		return
	}
	type tntMove struct {
		dim    world.Dimension
		packet []byte
		x, z   float64
	}
	type tntBlast struct {
		dim     world.Dimension
		x, y, z float64
		id      int32
	}
	var (
		moves  []tntMove
		blasts []tntBlast
	)
	s.entityMu.Lock()
	for id, t := range s.tnts {
		t.Fuse--
		if t.Fuse <= 0 {
			delete(s.tnts, id)
			blasts = append(blasts, tntBlast{dim: t.Dim, x: t.X, y: t.Y, z: t.Z, id: id})
			continue
		}
		w := s.worldFor(t.Dim)
		// 物理：重力 + 阻力 + 碰撞移动（Y→X→Z，与掉落物一致的逐轴扫描）。
		t.VelY -= 0.04
		t.VelX *= 0.98
		t.VelY *= 0.98
		t.VelZ *= 0.98
		box := world.Box{
			MinX: t.X - 0.49, MinY: t.Y, MinZ: t.Z - 0.49,
			MaxX: t.X + 0.49, MaxY: t.Y + 0.98, MaxZ: t.Z + 0.49,
		}
		moved := false
		if clipped := w.ClipMove(box, 1, t.VelY); clipped != t.VelY {
			t.Y += clipped
			t.VelY = 0
			// 落地摩擦（原版 Tnt: onGround 时水平速度 ×0.7）。
			t.VelX *= 0.7
			t.VelZ *= 0.7
			moved = true
		} else {
			t.Y += t.VelY
			moved = t.VelY != 0
		}
		box = world.Box{
			MinX: t.X - 0.49, MinY: t.Y, MinZ: t.Z - 0.49,
			MaxX: t.X + 0.49, MaxY: t.Y + 0.98, MaxZ: t.Z + 0.49,
		}
		if clipped := w.ClipMove(box, 0, t.VelX); clipped != t.VelX {
			t.X += clipped
			t.VelX = 0
			moved = true
		} else {
			t.X += t.VelX
			moved = moved || t.VelX != 0
		}
		box = world.Box{
			MinX: t.X - 0.49, MinY: t.Y, MinZ: t.Z - 0.49,
			MaxX: t.X + 0.49, MaxY: t.Y + 0.98, MaxZ: t.Z + 0.49,
		}
		if clipped := w.ClipMove(box, 2, t.VelZ); clipped != t.VelZ {
			t.Z += clipped
			t.VelZ = 0
			moved = true
		} else {
			t.Z += t.VelZ
			moved = moved || t.VelZ != 0
		}
		if moved {
			fuse := int32(t.Fuse)
			moves = append(moves, tntMove{
				dim:    t.Dim,
				packet: protocol.EncodeEntityPositionSync(t.ID, t.X, t.Y, t.Z, t.VelX, t.VelY, t.VelZ, 0, 0, false),
				x:      t.X, z: t.Z,
			})
			_ = fuse
		}
	}
	s.entityMu.Unlock()
	for _, move := range moves {
		s.broadcastToNearby(move.dim, move.packet, move.x, move.z, players)
	}
	for _, blast := range blasts {
		s.broadcastToNearby(blast.dim, protocol.EncodeEntityDestroy([]int32{blast.id}), blast.x, blast.z, players)
		s.explode(blast.dim, blast.x, blast.y+0.06125, blast.z, tntExplosionRadius, tntExplosionPower, "TNT")
	}
}

// sendExistingTnt 把同维度中已有的 TNT 实体发送给新加入的玩家。
func (s *Server) sendExistingTnt(player *session) {
	if !s.tntsEnabled {
		return
	}
	dim := player.dimensionID()
	s.entityMu.Lock()
	packets := make([][]byte, 0, 3*len(s.tnts))
	for _, t := range s.tnts {
		if t.Dim != dim {
			continue
		}
		packets = append(packets,
			protocol.EncodeAddEntity(t.ID, t.UUID, s.tntTypeID, t.X, t.Y, t.Z, t.VelX, t.VelY, t.VelZ, 0, 0),
			protocol.EncodeEntityMetadataVarInt(t.ID, 8, int32(t.Fuse)),
			protocol.EncodeEntityMetadataVarInt(t.ID, 9, int32(tntBlockState())),
		)
	}
	s.entityMu.Unlock()
	for _, packet := range packets {
		if err := player.writePacket(packet); err != nil {
			return
		}
	}
}
