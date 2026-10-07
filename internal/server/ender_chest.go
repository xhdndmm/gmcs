package server

import (
	"sync"

	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
)

// 末影箱：按玩家独立存储的 27 槽容器（原版行为：内容随玩家走）。
//
// 内容保存在玩家数据（players.json 的 ender_inventory 字段），随退出/周期
// 自动保存持久化；末影箱方块本身只是入口，破坏/放置不影响内容。
//
// 窗口布局（generic_9x3 菜单）：0–26 末影箱槽、27–62 背包（27 主背包 + 9 快捷栏）。

// enderChestSlots 是末影箱容量（27 = 3×9）。
const enderChestSlots = 27

// enderChest 是玩家的末影箱内容（由会话串行访问 + 持久化路径加锁）。
type enderChestStorage struct {
	mu    sync.Mutex
	slots [enderChestSlots]item.Stack
}

// Get 返回槽位内容。
func (e *enderChestStorage) Get(slot int) item.Stack {
	e.mu.Lock()
	defer e.mu.Unlock()
	if slot < 0 || slot >= enderChestSlots {
		return item.Empty()
	}
	return e.slots[slot]
}

// Set 设置槽位内容。
func (e *enderChestStorage) Set(slot int, stack item.Stack) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if slot < 0 || slot >= enderChestSlots {
		return
	}
	e.slots[slot] = stack
}

// enderSlotBacking 是末影箱窗口的槽位访问层。
type enderSlotBacking struct {
	player *session
}

// SlotCount 返回窗口槽位总数（27 + 36）。
func (b enderSlotBacking) SlotCount() int { return enderChestSlots + 36 }

// invSlot 把窗口槽位映射到物品栏槽位。
func (b enderSlotBacking) invSlot(index int) (int, bool) {
	index -= enderChestSlots
	switch {
	case index >= 0 && index <= 26:
		return index + item.SlotMainStart, true
	case index >= 27 && index <= 35:
		return index - 27, true
	}
	return 0, false
}

// Get 返回槽位内容。
func (b enderSlotBacking) Get(index int) item.Stack {
	if index < enderChestSlots {
		return b.player.enderChest.Get(index)
	}
	if slot, ok := b.invSlot(index); ok {
		return b.player.inventory.Get(slot)
	}
	return item.Empty()
}

// Set 设置槽位内容。
func (b enderSlotBacking) Set(index int, stack item.Stack) {
	if index < enderChestSlots {
		b.player.enderChest.Set(index, stack)
		return
	}
	if slot, ok := b.invSlot(index); ok {
		b.player.inventory.Set(slot, stack)
	}
}

// Supported 报告槽位是否可交互。
func (b enderSlotBacking) Supported(int) bool { return true }

// openEnderChest 打开玩家自己的末影箱窗口。
// 由会话 goroutine 调用。
func (s *Server) openEnderChest(player *session) {
	// 关闭上一个窗口。
	if player.openContainer != nil {
		s.closeContainer(player, false)
	}
	if player.getOpenCraft() != nil {
		s.closeCrafting(player, false)
	}
	menuType, ok := registry.StaticEntryID("minecraft:menu", "minecraft:generic_9x3")
	if !ok {
		menuType = protocol.MenuTypeGeneric9x3
	}
	player.setOpenEnder(true)
	player.windowID = player.nextWindowID()
	player.windowState = 0
	player.setCursor(item.Empty())

	if err := player.writePacket(protocol.EncodeOpenScreen(player.windowID, menuType, "Ender Chest")); err != nil {
		return
	}
	backing := enderSlotBacking{player: player}
	slots := make([][]byte, 0, backing.SlotCount())
	for i := 0; i < backing.SlotCount(); i++ {
		slots = append(slots, backing.Get(i).AppendSlot(nil))
	}
	_ = player.writePacket(protocol.EncodeContainerSetContent(
		player.windowID, player.windowState, slots, player.getCursor().AppendSlot(nil)))
}

// closeEnderChest 关闭末影箱窗口（内容已实时保存在会话状态，随玩家数据持久化）。
// sendClose 表示是否向客户端发送 Close Container。
// 由会话 goroutine 调用。
func (s *Server) closeEnderChest(player *session, sendClose bool) {
	if !player.getOpenEnder() {
		return
	}
	player.setOpenEnder(false)
	if sendClose {
		player.tryWrite(protocol.EncodeContainerClose(player.windowID))
	}
}
