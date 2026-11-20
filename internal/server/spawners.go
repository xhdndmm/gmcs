//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"

	"gmcs/internal/world"
)

// spawnerMobs 是结构刷怪笼实体名到 gmcs 支持生物种类的映射。
var spawnerMobs = map[string]mobKind{
	"minecraft:zombie":      mobZombie,
	"minecraft:skeleton":    mobSkeleton,
	"minecraft:spider":      mobSpider,
	"minecraft:cave_spider": mobSpider,
	"minecraft:silverfish":  mobSpider,
	"minecraft:enderman":    mobEnderman,
	"minecraft:shulker":     mobShulker,
}

type spawnerEntry struct {
	dim     world.Dimension
	x, y, z int
	entity  world.BlockEntity
}

// tickSpawners 每 20 tick 推进结构刷怪笼。原版刷怪笼有玩家半径、光照与
// 生成数量限制；gmcs 先实现玩家半径与延迟，不声称完全复刻原版生成算法。
func (s *Server) tickSpawners(players []*session) {
	if !s.mobsEnabled {
		return
	}
	s.worldMu.Lock()
	worlds := make([]*world.World, 0, len(s.worlds))
	for _, gameWorld := range s.worlds {
		worlds = append(worlds, gameWorld)
	}
	s.worldMu.Unlock()
	entries := make([]spawnerEntry, 0)
	for _, gameWorld := range worlds {
		gameWorld.ForEachBlockEntity(func(dim world.Dimension, x, y, z int, entity world.BlockEntity) {
			if entity.SpawnerEntity != "" {
				entries = append(entries, spawnerEntry{dim: dim, x: x, y: y, z: z, entity: entity})
			}
		})
	}
	for _, entry := range entries {
		if entry.entity.SpawnerDelay > 0 {
			entry.entity.SpawnerDelay--
			_ = s.worldFor(entry.dim).SetBlockEntity(entry.x, entry.y, entry.z, entry.entity)
			continue
		}
		kind, ok := spawnerMobs[entry.entity.SpawnerEntity]
		if !ok {
			entry.entity.SpawnerDelay = 200
			_ = s.worldFor(entry.dim).SetBlockEntity(entry.x, entry.y, entry.z, entry.entity)
			continue
		}
		near := false
		for _, player := range players {
			if !player.isJoined() || player.dimensionID() != entry.dim {
				continue
			}
			px, _, pz, _, _ := player.playerPosition()
			if math.Hypot(px-(float64(entry.x)+0.5), pz-(float64(entry.z)+0.5)) <= 16 {
				near = true
				break
			}
		}
		if near {
			gameWorld := s.worldFor(entry.dim)
			if gameWorld != nil && s.spawnLightAllowed(gameWorld, entry.dim, entry.x, entry.y+1, entry.z, false) {
				s.addMob(entry.dim, kind, float64(entry.x)+0.5, float64(entry.y), float64(entry.z)+0.5)
			}
		}
		// 200–800 tick 的近似原版延迟。
		entry.entity.SpawnerDelay = 200 + int32(s.nextRandom()%601)
		_ = s.worldFor(entry.dim).SetBlockEntity(entry.x, entry.y, entry.z, entry.entity)
	}
}
