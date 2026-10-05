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

// StackLimit 是单槽位物品数量上限（本服务器统一 64；物品组件派生的
// 堆叠上限暂不校验）。
const StackLimit = 64

// Inventory 是玩家物品栏（41 槽）。
//
// 并发安全：槽位由内部互斥锁保护（周期自动保存会在会话 goroutine
// 之外读取物品栏）。
type Inventory struct {
	mu    sync.Mutex
	slots [InventorySlots]Stack
}

// Add 尝试把堆栈放入物品栏（快捷栏→主背包顺序）：先并入同类未满堆栈，
// 再放入空槽位。返回未能放入的数量与变更的槽位列表（升序）。
// 盔甲与副手槽不参与自动放入。
func (inv *Inventory) Add(stack Stack) (remaining int32, changed []int) {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	if stack.IsEmpty() {
		return 0, nil
	}
	remaining = stack.Count
	// 先并入同类未满堆栈。
	for slot := SlotHotbarStart; slot <= SlotMainEnd && remaining > 0; slot++ {
		existing := inv.slots[slot]
		if existing.IsEmpty() || existing.ItemID != stack.ItemID || existing.Count >= StackLimit {
			continue
		}
		moved := min(StackLimit-existing.Count, remaining)
		existing.Count += moved
		inv.slots[slot] = existing
		remaining -= moved
		changed = append(changed, slot)
	}
	// 再放入空槽位。
	for slot := SlotHotbarStart; slot <= SlotMainEnd && remaining > 0; slot++ {
		if !inv.slots[slot].IsEmpty() {
			continue
		}
		placed := min(int32(StackLimit), remaining)
		inv.slots[slot] = Stack{ItemID: stack.ItemID, Count: placed}
		remaining -= placed
		changed = append(changed, slot)
	}
	return remaining, changed
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
