//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package item

import (
	"testing"

	"gmcs/internal/protocol"
)

// TestAppendSlotWithPotionContents 验证携带药水内容的堆栈会写入
// minecraft:potion_contents 组件（组件 ID 49；负载为 optional 药水 +
// optional 颜色 + 效果列表 + optional 名称，这里只携带药水）。
func TestAppendSlotWithPotionContents(t *testing.T) {
	// PotionID = registry.PotionAwkward + 1 = 4（药水注册表 ID 3）。
	stack := Stack{ItemID: 1126, Count: 1, PotionID: 4}
	data := stack.AppendSlot(nil)

	count, offset, err := protocol.DecodeVarInt(data)
	if err != nil || count != 1 {
		t.Fatalf("count = %d (err=%v)", count, err)
	}
	itemID, size, err := protocol.DecodeVarInt(data[offset:])
	if err != nil || itemID != 1126 {
		t.Fatalf("item id = %d (err=%v)", itemID, err)
	}
	offset += size
	added, size, err := protocol.DecodeVarInt(data[offset:])
	if err != nil || added != 1 {
		t.Fatalf("added components = %d (err=%v), want 1", added, err)
	}
	offset += size
	removed, size, err := protocol.DecodeVarInt(data[offset:])
	if err != nil || removed != 0 {
		t.Fatalf("removed components = %d (err=%v), want 0", removed, err)
	}
	offset += size
	componentID, size, err := protocol.DecodeVarInt(data[offset:])
	if err != nil || componentID != potionContentsComponentID {
		t.Fatalf("component id = %d (err=%v), want %d", componentID, err, potionContentsComponentID)
	}
	offset += size
	// optional(potion) 的 bool 标志 + Holder（裸注册表 ID，无 +1）。
	if data[offset] != 1 {
		t.Fatalf("potion present flag = %d, want 1", data[offset])
	}
	offset++
	potionID, size, err := protocol.DecodeVarInt(data[offset:])
	if err != nil || potionID != 3 {
		t.Fatalf("potion holder id = %d (err=%v), want 3", potionID, err)
	}
	offset += size
	// optional(customColor)=false、customEffects 空列表、optional(customName)=false。
	wantTail := []byte{0, 0, 0}
	if offset+len(wantTail) != len(data) {
		t.Fatalf("payload 长度 = %d, want %d", len(data), offset+len(wantTail))
	}
	for i, b := range wantTail {
		if data[offset+i] != b {
			t.Fatalf("payload 尾部[%d] = %d, want %d", i, data[offset+i], b)
		}
	}
}

// TestAppendSlotWithoutPotion 验证 PotionID 为 0 时不写组件（保持旧行为）。
func TestAppendSlotWithoutPotion(t *testing.T) {
	data := Stack{ItemID: 1, Count: 64}.AppendSlot(nil)
	if len(data) != 4 || data[1] != 1 || data[2] != 0 || data[3] != 0 {
		t.Fatalf("slot encoding = % x, want 40 01 00 00", data)
	}
}
