//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package item

import (
	"sync"

	"gmcs/internal/registry"
)

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

// StackLimit 是默认堆叠上限（未知物品的退估值）；具体物品的上限见
// registry.MaxStackSize（如雪球 16、工具 1）。
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
	limit := stack.MaxStack()
	// 先并入同类未满堆栈。
	for slot := SlotHotbarStart; slot <= SlotMainEnd && remaining > 0; slot++ {
		existing := inv.slots[slot]
		if existing.IsEmpty() || !existing.SameKind(stack) || existing.Count >= limit {
			continue
		}
		moved := min(limit-existing.Count, remaining)
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
		placed := stack
		placed.Count = min(limit, remaining)
		inv.slots[slot] = placed
		remaining -= placed.Count
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

// Take 在持锁状态下取走槽位物品并清空槽位。
// 越界或空槽位返回空堆栈。用于死亡掉落等“读取并清空”场景，
// 避免 Get 后 Set(Empty) 之间被其他 goroutine 的写入覆盖。
func (inv *Inventory) Take(slot int) Stack {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	if slot < 0 || slot >= InventorySlots {
		return Empty()
	}
	stack := inv.slots[slot]
	inv.slots[slot] = Empty()
	return stack
}

// Damage 在持锁状态下对槽位施加耐久损耗，返回损耗前（若损坏则为损坏时）
// 的堆栈与其最大耐久。
//
// 槽位越界、为空、物品不可损坏或 amount <= 0 时返回 ok=false 且不修改槽位。
// 耐久达到上限时槽位被清空，返回的堆栈仍保留 Damage 值（调用方可据此
// 播放损坏音效）；否则返回更新后的堆栈。
func (inv *Inventory) Damage(slot int, amount int32) (stack Stack, maxDamage int32, ok bool) {
	if amount <= 0 {
		return Empty(), 0, false
	}
	inv.mu.Lock()
	defer inv.mu.Unlock()
	if slot < 0 || slot >= InventorySlots {
		return Empty(), 0, false
	}
	current := inv.slots[slot]
	if current.IsEmpty() {
		return current, 0, false
	}
	limit, damageable := registry.MaxDamage(current.ItemID)
	if !damageable {
		return current, 0, false
	}
	current.Damage += amount
	if current.Damage >= limit {
		inv.slots[slot] = Empty()
		return current, limit, true
	}
	inv.slots[slot] = current
	return current, limit, true
}

// Repair 在持锁状态下修复槽位物品的耐久（原版经验修补每点经验修复 2 点耐久）。
//
// points 是本次可用于修复的经验点数。返回修复后的堆栈与实际消耗的经验点
// （向上取整）。槽位越界、为空、不可损坏或没有耐久损耗时返回 ok=false。
func (inv *Inventory) Repair(slot int, points int32) (stack Stack, used int32, ok bool) {
	if points <= 0 {
		return Empty(), 0, false
	}
	inv.mu.Lock()
	defer inv.mu.Unlock()
	if slot < 0 || slot >= InventorySlots {
		return Empty(), 0, false
	}
	current := inv.slots[slot]
	if current.IsEmpty() {
		return current, 0, false
	}
	limit, damageable := registry.MaxDamage(current.ItemID)
	if !damageable || current.Damage <= 0 || current.Damage >= limit {
		return current, 0, false
	}
	repair := min(2*points, current.Damage)
	used = (repair + 1) / 2
	current.Damage -= repair
	inv.slots[slot] = current
	return current, used, true
}
