//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"

	"gmcs/internal/world"
)

// 窗口有效性校验（原版 ServerPlayer.doTick → AbstractContainerMenu.stillValid
// 每玩家 tick 检查一次，失效即关闭窗口并通知客户端）。
//
// gmcs 在读循环处理完每个数据包后调用（原版客户端每 tick 至少发送一次数据包，
// 因此节奏接近逐 tick）：方块被破坏/替换、玩家离开交互距离时主动关闭窗口。
// 玩家物品栏窗口（windowID=0）与末影箱（无位置约束）不校验。
//
// 距离沿用既有近似：水平与垂直距离都不超过 windowReach（原版均为
// canInteractWithBlock(pos, 4.0) ≈ 8.5 格，这里按 8.0 盒近似，与普通容器一致）。

// windowReach 是窗口方块的交互距离上限（与 containerReach/anvilReach 一致）。
const windowReach = 8.0

// withinWindowReach 报告玩家与方块中心的水平/垂直距离是否都在窗口距离内。
func withinWindowReach(player *session, pos [3]int) bool {
	px, py, pz, _, _ := player.playerPosition()
	return math.Hypot(px-(float64(pos[0])+0.5), pz-(float64(pos[2])+0.5)) <= windowReach &&
		math.Abs(py-(float64(pos[1])+0.5)) <= windowReach
}

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
	return withinWindowReach(player, pos)
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
	return withinWindowReach(player, pos)
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
	return withinWindowReach(player, pos)
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
	return withinWindowReach(player, pos)
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
