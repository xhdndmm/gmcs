//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package item

import (
	"sync"
	"testing"

	"gmcs/internal/registry"
)

func mustItem(t *testing.T, name string) int32 {
	t.Helper()
	id, err := registry.ItemID(name)
	if err != nil {
		t.Fatalf("未知物品 %s: %v", name, err)
	}
	return id
}

// TestInventoryDamageBreaksItem 验证耐久达到上限时槽位被清空，
// 并返回损坏前的堆栈（调用方据此播放损坏音效）。
func TestInventoryDamageBreaksItem(t *testing.T) {
	pickaxe := mustItem(t, "minecraft:diamond_pickaxe")
	maxDamage, ok := registry.MaxDamage(pickaxe)
	if !ok {
		t.Fatal("钻石镐不可损坏")
	}
	inv := &Inventory{}
	inv.Set(0, Stack{ItemID: pickaxe, Count: 1, Damage: maxDamage - 3})

	stack, limit, damaged := inv.Damage(0, 5)
	if !damaged {
		t.Fatal("Damage 未生效")
	}
	if limit != maxDamage {
		t.Errorf("最大耐久 = %d, want %d", limit, maxDamage)
	}
	if stack.Damage != maxDamage+2 {
		t.Errorf("返回堆栈 Damage = %d, want %d", stack.Damage, maxDamage+2)
	}
	if got := inv.Get(0); !got.IsEmpty() {
		t.Errorf("损坏后槽位应清空，实际 = %+v", got)
	}
}

// TestInventoryDamageRejectsNonDamageable 验证不可损坏物品与空槽位不被修改。
func TestInventoryDamageRejectsNonDamageable(t *testing.T) {
	inv := &Inventory{}
	stone := mustItem(t, "minecraft:stone")
	inv.Set(0, Stack{ItemID: stone, Count: 1})

	if _, _, ok := inv.Damage(0, 5); ok {
		t.Error("石头不应可损坏")
	}
	if got := inv.Get(0); got.Count != 1 || got.Damage != 0 {
		t.Errorf("不可损坏物品被修改：%+v", got)
	}
	if _, _, ok := inv.Damage(1, 5); ok {
		t.Error("空槽位不应可损坏")
	}
	if _, _, ok := inv.Damage(0, 0); ok {
		t.Error("零损耗不应生效")
	}
}

// TestInventoryRepair 验证经验修补按每点经验 2 点耐久修复，并向上取整消耗经验。
func TestInventoryRepair(t *testing.T) {
	pickaxe := mustItem(t, "minecraft:diamond_pickaxe")
	inv := &Inventory{}
	inv.Set(0, Stack{ItemID: pickaxe, Count: 1, Damage: 3})

	stack, used, ok := inv.Repair(0, 1) // 2 点耐久 = 1 点经验
	if !ok || used != 1 {
		t.Fatalf("Repair(1) = (%v, %d), want used=1", ok, used)
	}
	if stack.Damage != 1 {
		t.Errorf("修复后 Damage = %d, want 1", stack.Damage)
	}
	// 只剩 1 点耐久损耗：仍然消耗 1 点经验（向上取整）。
	stack, used, ok = inv.Repair(0, 10)
	if !ok || used != 1 {
		t.Fatalf("Repair(10) = (%v, %d), want used=1", ok, used)
	}
	if stack.Damage != 0 {
		t.Errorf("完全修复后 Damage = %d, want 0", stack.Damage)
	}
	// 已无损耗：不再消耗经验。
	if _, used, ok := inv.Repair(0, 10); ok || used != 0 {
		t.Errorf("无损耗时 Repair = (%v, %d), want (false, 0)", ok, used)
	}
}

// TestInventoryTakeClearsSlot 验证 Take 取走物品并清空槽位。
func TestInventoryTakeClearsSlot(t *testing.T) {
	stone := mustItem(t, "minecraft:stone")
	inv := &Inventory{}
	inv.Set(0, Stack{ItemID: stone, Count: 7})

	stack := inv.Take(0)
	if stack.ItemID != stone || stack.Count != 7 {
		t.Fatalf("Take = %+v, want 7 个石头", stack)
	}
	if got := inv.Get(0); !got.IsEmpty() {
		t.Fatalf("取走后槽位未清空：%+v", got)
	}
	if got := inv.Take(0); !got.IsEmpty() {
		t.Fatalf("空槽位 Take = %+v, want 空", got)
	}
	if got := inv.Take(InventorySlots); !got.IsEmpty() {
		t.Fatalf("越界 Take = %+v, want 空", got)
	}
}

// TestInventoryDamageConcurrentAccumulates 回归测试：并发耐久损耗必须全部累加。
// 旧的 Get→修改→Set 实现会丢失并发写入（读循环与 Tick 线程同时扣耐久）。
func TestInventoryDamageConcurrentAccumulates(t *testing.T) {
	pickaxe := mustItem(t, "minecraft:diamond_pickaxe")
	inv := &Inventory{}
	inv.Set(0, Stack{ItemID: pickaxe, Count: 1})

	const workers, perWorker = 8, 25
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perWorker; j++ {
				inv.Damage(0, 1)
			}
		}()
	}
	wg.Wait()

	if got, want := inv.Get(0).Damage, int32(workers*perWorker); got != want {
		t.Errorf("并发耐久损耗 = %d, want %d", got, want)
	}
}
