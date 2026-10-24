//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"strings"

	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 门/活板门/栅栏门的开合音效。
//
// 原版 DoorBlock/TrapDoorBlock/FenceGateBlock 在 setOpen/useWithoutItem 中调用
// level.playSound(null, pos, open ? xxxOpen() : xxxClose(), SoundSource.BLOCKS,
// 1.0F, random.nextFloat() * 0.1F + 0.9F)。音效集合来自 BlockSetType：
// 木质、铁、铜（含氧化/打蜡变体共用 copper_door/copper_trapdoor）、
// 竹、樱花木、下界木各有一套；栅栏门统一使用 block.fence_gate.*。
//
// 名称已核对 1.21.11 声效注册表（internal/registry/static_ids_generated.go）：
// minecraft:block.{wooden,iron,copper,bamboo_wood,cherry_wood,nether_wood}_door.{open,close}
// 与 ..._trapdoor.{open,close}、minecraft:block.fence_gate.{open,close}。
const (
	openableSoundWooden = iota
	openableSoundIron
	openableSoundCopper
	openableSoundBamboo
	openableSoundCherry
	openableSoundNether
	openableSoundGroups
)

// openableSoundGroupOf 按方块名返回音效集合（原版 BlockSetType 分组）。
func openableSoundGroupOf(name string) int {
	switch {
	case name == "minecraft:iron_door" || name == "minecraft:iron_trapdoor":
		return openableSoundIron
	case strings.Contains(name, "copper_door") || strings.Contains(name, "copper_trapdoor"):
		// 铜门/铜活板门的所有氧化与打蜡变体共用同一套音效。
		return openableSoundCopper
	case name == "minecraft:bamboo_door" || name == "minecraft:bamboo_trapdoor":
		return openableSoundBamboo
	case name == "minecraft:cherry_door" || name == "minecraft:cherry_trapdoor":
		return openableSoundCherry
	case name == "minecraft:crimson_door" || name == "minecraft:crimson_trapdoor" ||
		name == "minecraft:warped_door" || name == "minecraft:warped_trapdoor":
		return openableSoundNether
	}
	return openableSoundWooden
}

// resolveOpenableSounds 解析开合音效 ID（缺失时保持 0：播放时静默跳过）。
func (s *Server) resolveOpenableSounds() {
	var zero [openableSoundGroups]int32
	s.soundDoorOpen, s.soundDoorClose = zero, zero
	s.soundTrapdoorOpen, s.soundTrapdoorClose = zero, zero
	groups := [openableSoundGroups][2]string{
		{"minecraft:block.wooden_door", "minecraft:block.wooden_trapdoor"},
		{"minecraft:block.iron_door", "minecraft:block.iron_trapdoor"},
		{"minecraft:block.copper_door", "minecraft:block.copper_trapdoor"},
		{"minecraft:block.bamboo_wood_door", "minecraft:block.bamboo_wood_trapdoor"},
		{"minecraft:block.cherry_wood_door", "minecraft:block.cherry_wood_trapdoor"},
		{"minecraft:block.nether_wood_door", "minecraft:block.nether_wood_trapdoor"},
	}
	for group, names := range groups {
		doorBase, trapdoorBase := names[0], names[1]
		if id, ok := registry.StaticEntryID("minecraft:sound_event", doorBase+".open"); ok {
			s.soundDoorOpen[group] = id
		}
		if id, ok := registry.StaticEntryID("minecraft:sound_event", doorBase+".close"); ok {
			s.soundDoorClose[group] = id
		}
		if id, ok := registry.StaticEntryID("minecraft:sound_event", trapdoorBase+".open"); ok {
			s.soundTrapdoorOpen[group] = id
		}
		if id, ok := registry.StaticEntryID("minecraft:sound_event", trapdoorBase+".close"); ok {
			s.soundTrapdoorClose[group] = id
		}
	}
	// 栅栏门（原版所有木质共用）。
	if id, ok := registry.StaticEntryID("minecraft:sound_event", "minecraft:block.fence_gate.open"); ok {
		s.soundFenceGateOpen = id
	}
	if id, ok := registry.StaticEntryID("minecraft:sound_event", "minecraft:block.fence_gate.close"); ok {
		s.soundFenceGateClose = id
	}
}

// playDoorSound 广播门的开合音效。
func (s *Server) playDoorSound(dim world.Dimension, pos [3]int, name string, open bool) {
	group := openableSoundGroupOf(name)
	sound := s.soundDoorClose[group]
	if open {
		sound = s.soundDoorOpen[group]
	}
	s.playBlockOpenSound(dim, pos, sound)
}

// playTrapdoorSound 广播活板门的开合音效。
func (s *Server) playTrapdoorSound(dim world.Dimension, pos [3]int, name string, open bool) {
	group := openableSoundGroupOf(name)
	sound := s.soundTrapdoorClose[group]
	if open {
		sound = s.soundTrapdoorOpen[group]
	}
	s.playBlockOpenSound(dim, pos, sound)
}

// playFenceGateSound 广播栅栏门的开合音效。
func (s *Server) playFenceGateSound(dim world.Dimension, pos [3]int, open bool) {
	sound := s.soundFenceGateClose
	if open {
		sound = s.soundFenceGateOpen
	}
	s.playBlockOpenSound(dim, pos, sound)
}

// playBlockOpenSound 在方块中心播发音效（音高 0.9–1.0，与原版
// nextFloat() * 0.1F + 0.9F 一致）。
func (s *Server) playBlockOpenSound(dim world.Dimension, pos [3]int, sound int32) {
	if sound == 0 {
		return
	}
	// 音高抖动复用 fxRand（与天气共用一条独立的非玩法随机流）。
	pitch := 0.9 + s.fxNextFloat()*0.1
	x := float64(pos[0]) + 0.5
	y := float64(pos[1]) + 0.5
	z := float64(pos[2]) + 0.5
	packet := protocol.EncodeSoundEffect(sound, protocol.SoundCategoryBlock, x, y, z, 1, pitch, 0)
	s.writeToNearbyPlayers(packet, dim, x, z, nil)
}
