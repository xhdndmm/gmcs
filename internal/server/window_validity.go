//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"gmcs/internal/world"
)

// 窗口有效性校验（原版 ServerPlayer.doTick → AbstractContainerMenu.stillValid
// 每玩家 tick 检查一次，失效即关闭窗口并通知客户端）。
//
// gmcs 在读循环处理完每个数据包后调用（原版客户端每 tick 至少发送一次数据包，
// 因此节奏接近逐 tick）：方块被破坏/替换、玩家离开交互距离时主动关闭窗口。
// 玩家物品栏窗口（windowID=0）与末影箱（无位置约束）不校验。
//
// 距离与原版一致（Player.isWithinBlockInteractionRange(pos, 4.0)：眼睛位置到
// 方块碰撞盒的距离 < 交互距离属性 4.5 + 4.0，见 withinContainerReach）。

// blockStillIs 报告指定维度坐标处的方块状态是否满足谓词（世界不可用时为 false）。
func (s *Server) blockStillIs(dim world.Dimension, pos [3]int, match func(state uint16) bool) bool {
	gameWorld := s.worldFor(dim)
	if gameWorld == nil {
		return false
	}
	return match(gameWorld.BlockAt(pos[0], pos[1], pos[2]))
}

// containerStillValid 报告容器窗口是否仍有效（原版 ChestMenu.stillValid 等）：
// 方块仍是同一种容器且在交互距离内。
func (s *Server) containerStillValid(player *session, container *containerState) bool {
	container.mu.Lock()
	dim, pos, def, closed := container.dim, container.pos, container.def, container.closed
	container.mu.Unlock()
	if closed {
		return false
	}
	ok := s.blockStillIs(dim, pos, func(state uint16) bool {
		gotDef, _, found := s.containerForState(state)
		return found && gotDef == def
	})
	if !ok {
		return false
	}
	return player.withinContainerReach(pos[0], pos[1], pos[2])
}

// craftStillValid 报告工作台窗口是否仍有效（原版 CraftingMenu.stillValid）：
// 方块仍是工作台且在交互距离内。玩家物品栏窗口的 2×2 合成格没有方块，恒有效。
func (s *Server) craftStillValid(player *session, state *craftingState) bool {
	state.mu.Lock()
	dim, pos, hasPos := state.dim, state.pos, state.hasPos
	state.mu.Unlock()
	if !hasPos {
		return true
	}
	if !s.blockStillIs(dim, pos, func(state uint16) bool {
		name, ok := s.blockNameOf(state)
		return ok && name == "minecraft:crafting_table"
	}) {
		return false
	}
	return player.withinContainerReach(pos[0], pos[1], pos[2])
}

// stonecutterStillValid 报告切石机窗口是否仍有效（原版 StonecutterMenu.stillValid）。
func (s *Server) stonecutterStillValid(player *session, state *stonecutterState) bool {
	state.mu.Lock()
	dim, pos := state.dim, state.pos
	state.mu.Unlock()
	if !s.blockStillIs(dim, pos, func(state uint16) bool {
		name, ok := s.blockNameOf(state)
		return ok && name == "minecraft:stonecutter"
	}) {
		return false
	}
	return player.withinContainerReach(pos[0], pos[1], pos[2])
}

// smithingStillValid 报告锻造台窗口是否仍有效（原版 SmithingMenu.stillValid）。
func (s *Server) smithingStillValid(player *session, state *smithingState) bool {
	state.mu.Lock()
	dim, pos := state.dim, state.pos
	state.mu.Unlock()
	if !s.blockStillIs(dim, pos, func(state uint16) bool {
		name, ok := s.blockNameOf(state)
		return ok && name == "minecraft:smithing_table"
	}) {
		return false
	}
	return player.withinContainerReach(pos[0], pos[1], pos[2])
}

// validateOpenWindow 校验玩家当前打开的窗口是否仍然有效，失效时主动关闭并
// 通知客户端（原版 stillValid 每 tick 检查）。由会话读循环在每个数据包后调用。
func (s *session) validateOpenWindow() {
	server := s.server
	if server == nil {
		return
	}
	switch {
	case s.connContainer() != nil:
		if !server.containerStillValid(s, s.connContainer()) {
			server.closeContainer(s, true)
		}
	case s.getOpenCraft() != nil:
		if !server.craftStillValid(s, s.getOpenCraft()) {
			server.closeCrafting(s, true)
		}
	case s.getStonecutter() != nil:
		if !server.stonecutterStillValid(s, s.getStonecutter()) {
			server.closeStonecutter(s, true)
		}
	case s.getSmithing() != nil:
		if !server.smithingStillValid(s, s.getSmithing()) {
			server.closeSmithing(s, true)
		}
	case s.getAnvil() != nil:
		if !server.anvilStillValid(s, s.getAnvil()) {
			server.closeAnvil(s, true)
		}
	case s.getGrindstone() != nil:
		if !server.grindstoneStillValid(s, s.getGrindstone()) {
			server.closeGrindstone(s, true)
		}
	case s.getEnchanting() != nil:
		if !server.enchantingStillValid(s, s.getEnchanting()) {
			server.closeEnchanting(s, true)
		}
	}
}
