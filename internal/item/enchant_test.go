//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package item

import (
	"bytes"
	"testing"

	"gmcs/internal/protocol"
	"gmcs/internal/registry"
)

// TestEnchantmentsComponentEncoding 逐字节验证 minecraft:enchantments 组件
// （组件 ID 13）的编码：VarInt 数量 + 每项（Holder：VarInt(id+1)、VarInt(等级)）。
func TestEnchantmentsComponentEncoding(t *testing.T) {
	stack := Stack{
		ItemID: 913, Count: 1,
		Enchantments: []EnchantmentEntry{{ID: 33, Level: 1}, {ID: 40, Level: 2}},
	}
	got := stack.AppendSlot(nil)
	want := []byte{
		1,       // count
		0x91, 7, // itemID 913（VarInt）
		1,     // 添加的组件数
		0,     // 移除的组件数
		13,    // 组件 ID：enchantments
		2,     // 条目数
		34, 1, // (附魔 33 的 Holder id+1, 等级 1)
		41, 2, // (附魔 40 的 Holder id+1, 等级 2)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("AppendSlot = % x, want % x", got, want)
	}
}

// TestStackEnchantmentOps 验证附魔访问器的升序维护、替换与移除。
func TestStackEnchantmentOps(t *testing.T) {
	stack := Stack{ItemID: 1, Count: 1}
	stack = stack.WithEnchantment(40, 2)
	stack = stack.WithEnchantment(33, 1)
	if len(stack.Enchantments) != 2 ||
		stack.Enchantments[0] != (EnchantmentEntry{ID: 33, Level: 1}) ||
		stack.Enchantments[1] != (EnchantmentEntry{ID: 40, Level: 2}) {
		t.Fatalf("附魔应按 ID 升序：%+v", stack.Enchantments)
	}
	// 替换现有附魔等级。
	stack = stack.WithEnchantment(33, 3)
	if stack.EnchantmentLevel(33) != 3 || stack.EnchantmentLevel(40) != 2 {
		t.Fatalf("替换后等级不符：%+v", stack.Enchantments)
	}
	// 等级 0 移除。
	stack = stack.WithEnchantment(33, 0)
	if len(stack.Enchantments) != 1 || stack.Enchantments[0].ID != 40 {
		t.Fatalf("移除后应只剩附魔 40：%+v", stack.Enchantments)
	}
	if stack.EnchantmentLevel(33) != 0 {
		t.Fatalf("已移除附魔的等级应为 0")
	}

	// 组件变化必须影响 SameKind（堆叠约束）。
	other := Stack{ItemID: 1, Count: 1}
	if stack.SameKind(other) {
		t.Fatal("附魔不同的堆栈不应同类")
	}
	other = other.WithEnchantment(40, 2)
	if !stack.SameKind(other) {
		t.Fatal("附魔相同的堆栈应同类")
	}
	// Equal 含数量。
	if stack.Equal(other.WithEnchantment(40, 1)) {
		t.Fatal("附魔等级不同的堆栈不应相等")
	}
}

// TestStoredEnchantmentsComponentEncoding 验证附魔书使用 minecraft:stored_enchantments
// （组件 ID 41）编码；组件仍按类型 ID 升序（repair_cost 19 在前）。
func TestStoredEnchantmentsComponentEncoding(t *testing.T) {
	book, err := FromName("minecraft:enchanted_book", 1)
	if err != nil {
		t.Fatalf("缺少附魔书：%v", err)
	}
	if !registry.HasStoredEnchantments(book.ItemID) {
		t.Fatal("minecraft:enchanted_book 应使用 stored_enchantments")
	}
	book.RepairCost = 5
	book.Enchantments = []EnchantmentEntry{{ID: 33, Level: 1}}
	got := book.AppendSlot(nil)
	want := protocol.AppendVarInt(nil, 1)
	want = protocol.AppendVarInt(want, book.ItemID)
	want = protocol.AppendVarInt(want, 2) // 添加的组件数
	want = protocol.AppendVarInt(want, 0) // 移除的组件数
	want = protocol.AppendVarInt(want, 19)
	want = protocol.AppendVarInt(want, 5)
	want = protocol.AppendVarInt(want, 41)
	want = protocol.AppendVarInt(want, 1)
	want = protocol.AppendVarInt(want, 34) // Holder：33 + 1
	want = protocol.AppendVarInt(want, 1)
	if !bytes.Equal(got, want) {
		t.Fatalf("AppendSlot = % x, want % x", got, want)
	}
}
