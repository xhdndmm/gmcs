package item

import (
	"testing"

	"gmcs/internal/protocol"
)

func TestFromName(t *testing.T) {
	stack, err := FromName("minecraft:stone", 64)
	if err != nil {
		t.Fatal(err)
	}
	if stack.Count != 64 || stack.ItemID != 1 { // stone 的物品 ID 为 1（已验证）
		t.Fatalf("unexpected stack: %+v", stack)
	}
	if _, err := FromName("minecraft:not_a_real_item", 1); err == nil {
		t.Fatal("expected error for unknown item")
	}
	if _, err := FromName("minecraft:stone", 0); err == nil {
		t.Fatal("expected error for zero count")
	}
}

func TestAppendSlotEmpty(t *testing.T) {
	data := Empty().AppendSlot(nil)
	if len(data) != 1 || data[0] != 0x00 {
		t.Fatalf("empty slot encoding = % x, want 00", data)
	}
}

func TestAppendSlotRoundTrip(t *testing.T) {
	stack := Stack{ItemID: 1, Count: 64}
	data := stack.AppendSlot(nil)

	count, offset, err := protocol.DecodeVarInt(data)
	if err != nil || count != 64 {
		t.Fatalf("count = %d (err=%v)", count, err)
	}
	itemID, size, err := protocol.DecodeVarInt(data[offset:])
	if err != nil || itemID != 1 {
		t.Fatalf("item id = %d (err=%v)", itemID, err)
	}
	offset += size
	added, size, err := protocol.DecodeVarInt(data[offset:])
	if err != nil || added != 0 {
		t.Fatalf("added components = %d (err=%v)", added, err)
	}
	offset += size
	removed, size, err := protocol.DecodeVarInt(data[offset:])
	if err != nil || removed != 0 {
		t.Fatalf("removed components = %d (err=%v)", removed, err)
	}
	offset += size
	if offset != len(data) {
		t.Fatalf("%d trailing bytes", len(data)-offset)
	}
}

func TestInventory(t *testing.T) {
	inv := &Inventory{}
	if got := inv.Get(SlotHotbarStart); !got.IsEmpty() {
		t.Fatalf("expected empty slot, got %+v", got)
	}
	stone, err := FromName("minecraft:stone", 32)
	if err != nil {
		t.Fatal(err)
	}
	inv.Set(SlotHotbarStart, stone)
	if got := inv.Get(SlotHotbarStart); got != stone {
		t.Fatalf("stored stack mismatch: %+v", got)
	}
	inv.Set(SlotOffhand, stone)
	if got := inv.Get(SlotOffhand); got != stone {
		t.Fatalf("offhand mismatch: %+v", got)
	}
	// 越界操作被忽略。
	inv.Set(-1, stone)
	inv.Set(InventorySlots, stone)
	if got := inv.Get(-1); !got.IsEmpty() {
		t.Fatalf("expected empty for out-of-range get, got %+v", got)
	}
	// 清空槽位。
	inv.Set(SlotHotbarStart, Empty())
	if got := inv.Get(SlotHotbarStart); !got.IsEmpty() {
		t.Fatalf("expected cleared slot, got %+v", got)
	}
}
