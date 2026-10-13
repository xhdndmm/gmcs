package server

import "gmcs/internal/item"

// hopperSlotBacking 是漏斗窗口的槽位访问层：
// 0–4 漏斗槽位、5–31 主背包、32–40 快捷栏（共 41 槽）。
//
// 漏斗槽位由 hopperState 权威持有（与传输 Tick 共享），因此这里统一在
// hopperState.mu 下读写，避免“窗口点击”与“搬运事务”并发修改同一份槽位。
type hopperSlotBacking struct {
	server *Server
	player *session
	state  *hopperState
}

// SlotCount 返回窗口槽位总数（5 + 36）。
func (b hopperSlotBacking) SlotCount() int { return hopperSlots + 36 }

// Get 返回槽位内容。
func (b hopperSlotBacking) Get(index int) item.Stack {
	if index < hopperSlots {
		b.state.mu.Lock()
		defer b.state.mu.Unlock()
		return b.state.slots[index]
	}
	return b.player.inventory.Get(containerPlayerInvSlot(index - hopperSlots))
}

// Set 设置槽位内容；漏斗部分立即写回方块实体并同步窗口观察者。
func (b hopperSlotBacking) Set(index int, stack item.Stack) {
	if index < hopperSlots {
		b.state.mu.Lock()
		changed := b.state.slots[index] != stack
		b.state.slots[index] = stack
		b.state.mu.Unlock()
		if changed {
			b.server.flushHopper(b.state)
			b.server.refreshHopperWindow(b.state)
		}
		return
	}
	b.player.inventory.Set(containerPlayerInvSlot(index-hopperSlots), stack)
}

// Supported 报告槽位是否可交互（漏斗全部槽位可自由取放）。
func (b hopperSlotBacking) Supported(int) bool { return true }
