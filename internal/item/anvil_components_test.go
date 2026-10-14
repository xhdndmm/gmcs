//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package item

import (
	"bytes"
	"testing"

	"gmcs/internal/protocol"
)

// TestStackAppendSlotWithComponents 验证槽位编码的组件补丁：
// damage（ID 3，VarInt）、custom_name（ID 6，NBT String Tag）、
// repair_cost（ID 19，VarInt）与 potion_contents（ID 49）按 ID 升序写出。
func TestStackAppendSlotWithComponents(t *testing.T) {
	stack := Stack{ItemID: 42, Count: 1, Damage: 7, RepairCost: 3, CustomName: "N", PotionID: 5}
	got := stack.AppendSlot(nil)

	want := protocol.AppendVarInt(nil, 1)  // count
	want = protocol.AppendVarInt(want, 42) // itemID
	want = protocol.AppendVarInt(want, 4)  // 添加的组件数
	want = protocol.AppendVarInt(want, 0)  // 移除的组件数
	want = protocol.AppendVarInt(want, damageComponentID)
	want = protocol.AppendVarInt(want, 7)
	want = protocol.AppendVarInt(want, customNameComponentID)
	want = append(want, 0x08, 0x00, 0x01, 'N') // NBT String Tag
	want = protocol.AppendVarInt(want, repairCostComponentID)
	want = protocol.AppendVarInt(want, 3)
	want = protocol.AppendVarInt(want, potionContentsComponentID)
	want = append(want, 1)                // optional(potion) 存在
	want = protocol.AppendVarInt(want, 5) // Holder：注册表 ID + 1
	want = append(want, 0)                // optional(customColor) 为空
	want = protocol.AppendVarInt(want, 0) // customEffects 空列表
	want = append(want, 0)                // optional(customName) 为空
	if !bytes.Equal(got, want) {
		t.Fatalf("槽位编码不符\n got=%x\nwant=%x", got, want)
	}
}

// TestStackAppendSlotDamageOnly 验证单组件（damage）编码。
func TestStackAppendSlotDamageOnly(t *testing.T) {
	stack := Stack{ItemID: 913, Count: 1, Damage: 1560}
	got := stack.AppendSlot(nil)
	want := protocol.AppendVarInt(nil, 1)
	want = protocol.AppendVarInt(want, 913)
	want = protocol.AppendVarInt(want, 1) // 添加的组件数
	want = protocol.AppendVarInt(want, 0)
	want = protocol.AppendVarInt(want, damageComponentID)
	want = protocol.AppendVarInt(want, 1560)
	if !bytes.Equal(got, want) {
		t.Fatalf("槽位编码不符\n got=%x\nwant=%x", got, want)
	}
}

// TestStackSameKindWithComponents 验证组件不同的堆栈不可堆叠。
func TestStackSameKindWithComponents(t *testing.T) {
	base := Stack{ItemID: 10, Count: 1}
	if !base.SameKind(Stack{ItemID: 10, Count: 2}) {
		t.Fatal("同物品应同类")
	}
	if base.SameKind(Stack{ItemID: 10, Count: 1, Damage: 1}) {
		t.Fatal("耐久不同不应同类")
	}
	if base.SameKind(Stack{ItemID: 10, Count: 1, RepairCost: 1}) {
		t.Fatal("累积惩罚不同不应同类")
	}
	if base.SameKind(Stack{ItemID: 10, Count: 1, CustomName: "X"}) {
		t.Fatal("自定义名称不同不应同类")
	}
	if base.SameKind(Stack{ItemID: 10, Count: 1, PotionID: 2}) {
		t.Fatal("药水不同不应同类")
	}
}

// TestStackHoverName 验证悬停名：自定义名称优先，否则为服务端语言名。
func TestStackHoverName(t *testing.T) {
	stack, err := FromName("minecraft:diamond_pickaxe", 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := stack.HoverName(); got != "Diamond Pickaxe" {
		t.Fatalf("默认悬停名 = %q", got)
	}
	stack.CustomName = "Digger"
	if got := stack.HoverName(); got != "Digger" {
		t.Fatalf("自定义悬停名 = %q", got)
	}
}
