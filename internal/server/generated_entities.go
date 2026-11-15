//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// frameEntity 是生成阶段产生的物品展示框。展示框是固定装饰实体，
// 不参与生物 AI/寻路；目前不做旋转、掉落或持久化。
type frameEntity struct {
	ID      int32
	UUID    [16]byte
	Dim     world.Dimension
	X, Y, Z float64
	ItemID  int32
	Facing  string
}

// tickGeneratedEntities 把各维度生成器产生的实体交给服务器实体系统。
// 这是区块生成器与服务器实体生命周期的桥；同一批实体只会被取走一次。
func (s *Server) tickGeneratedEntities() {
	s.worldMu.Lock()
	worlds := make([]*world.World, 0, len(s.worlds))
	for _, gameWorld := range s.worlds {
		worlds = append(worlds, gameWorld)
	}
	s.worldMu.Unlock()
	for _, gameWorld := range worlds {
		for _, entity := range gameWorld.TakeGeneratedEntities() {
			switch entity.Type {
			case "minecraft:item_frame":
				s.spawnGeneratedFrame(entity)
			case "minecraft:shulker":
				if _, ok := s.mobTypeIDs[mobShulker]; ok {
					s.addMob(entity.Dim, mobShulker, entity.X, entity.Y, entity.Z)
				}
			}
		}
	}
}

func (s *Server) spawnGeneratedFrame(entity world.PendingEntity) {
	if s.itemFrameTypeID == 0 || entity.Item == "" {
		return
	}
	itemID, err := registry.ItemID(entity.Item)
	if err != nil {
		return
	}
	frame := &frameEntity{
		ID:     s.entityIDs.Add(1),
		UUID:   newEntityUUID(),
		Dim:    entity.Dim,
		X:      entity.X,
		Y:      entity.Y,
		Z:      entity.Z,
		ItemID: itemID,
		Facing: entity.Facing,
	}
	s.entityMu.Lock()
	s.frames[frame.ID] = frame
	targets := s.markTrackedNearbyLocked(frame.Dim, frame.X, frame.Z, nil, frame.ID)
	s.entityMu.Unlock()
	packets := [][]byte{
		protocol.EncodeAddEntity(frame.ID, frame.UUID, s.itemFrameTypeID,
			frame.X, frame.Y, frame.Z, 0, 0, 0, 0, 0),
		protocol.EncodeEntityMetadataItem(frame.ID, item.Stack{ItemID: frame.ItemID, Count: 1}.AppendSlot(nil)),
	}
	for _, player := range targets {
		for _, packet := range packets {
			player.tryWrite(packet)
		}
	}
}
