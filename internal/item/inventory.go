package item

import "sync"

// 玩家物品栏的槽位布局（与容器的槽位编号一致）。
const (
	// InventorySlots 是玩家物品栏的槽位总数。
	InventorySlots = 41
	// SlotHotbarStart / SlotHotbarEnd 是快捷栏槽位（含两端）。
	SlotHotbarStart = 0
	SlotHotbarEnd   = 8
	// SlotMainStart / SlotMainEnd 是主背包槽位（含两端）。
	SlotMainStart = 9
	SlotMainEnd   = 35
	// 盔甲槽位：脚、腿、胸、头。
	SlotFeet  = 36
	SlotLegs  = 37
	SlotChest = 38
	SlotHead  = 39
	// SlotOffhand 是副手槽。
	SlotOffhand = 40
)

// Inventory 是玩家物品栏（41 槽）。
//
// 并发安全：槽位由内部互斥锁保护（周期自动保存会在会话 goroutine
// 之外读取物品栏）。
type Inventory struct {
	mu    sync.Mutex
	slots [InventorySlots]Stack
}

// Get 返回槽位中的物品；越界返回空堆栈。
func (inv *Inventory) Get(slot int) Stack {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	if slot < 0 || slot >= InventorySlots {
		return Empty()
	}
	return inv.slots[slot]
}

// Set 设置槽位中的物品；越界时忽略。空堆栈表示清空槽位。
func (inv *Inventory) Set(slot int, stack Stack) {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	if slot < 0 || slot >= InventorySlots {
		return
	}
	inv.slots[slot] = stack
}

// Slots 返回全部槽位的快照。
func (inv *Inventory) Slots() [InventorySlots]Stack {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	return inv.slots
}
