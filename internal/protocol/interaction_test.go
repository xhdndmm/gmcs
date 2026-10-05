package protocol

import (
	"encoding/binary"
	"errors"
	"testing"
)

// TestPackUnpackPosition 验证 Position 打包与解包互逆（含负坐标与边界）。
func TestPackUnpackPosition(t *testing.T) {
	for _, test := range []struct{ x, y, z int }{
		{0, 0, 0},
		{1, 64, -1},
		{-1000, -60, 2000},
		{33554431, 2047, -33554432}, // x 26 位、y 12 位边界
	} {
		x, y, z := UnpackPosition(PackPosition(test.x, test.y, test.z))
		if x != test.x || y != test.y || z != test.z {
			t.Fatalf("UnpackPosition(PackPosition(%d,%d,%d)) = (%d,%d,%d)", test.x, test.y, test.z, x, y, z)
		}
	}
}

// TestEncodeBlockUpdate 验证方块更新包的内容。
func TestEncodeBlockUpdate(t *testing.T) {
	packet := EncodeBlockUpdate(-3, 70, 12, 12345)
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != PlayPacketIDBlockUpdate {
		t.Fatalf("unexpected packet ID %#x (err=%v)", packetID, err)
	}
	position, offset, err := DecodeInt64(packet, offset)
	if err != nil {
		t.Fatal(err)
	}
	x, y, z := UnpackPosition(position)
	if x != -3 || y != 70 || z != 12 {
		t.Fatalf("位置 = (%d,%d,%d)", x, y, z)
	}
	state, offset, err := decodeVarIntAt(packet, offset)
	if err != nil || state != 12345 {
		t.Fatalf("状态 = %d (err=%v)", state, err)
	}
	if offset != len(packet) {
		t.Fatal("方块更新包存在多余数据")
	}
}

// TestEncodeRemoveEntities 验证实体移除包的内容。
func TestEncodeRemoveEntities(t *testing.T) {
	packet := EncodeRemoveEntities([]int32{7, 42})
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != PlayPacketIDRemoveEntities {
		t.Fatalf("unexpected packet ID %#x (err=%v)", packetID, err)
	}
	count, offset, err := decodeVarIntAt(packet, offset)
	if err != nil || count != 2 {
		t.Fatalf("count = %d (err=%v)", count, err)
	}
	first, offset, err := decodeVarIntAt(packet, offset)
	if err != nil || first != 7 {
		t.Fatalf("first = %d (err=%v)", first, err)
	}
	second, offset, err := decodeVarIntAt(packet, offset)
	if err != nil || second != 42 || offset != len(packet) {
		t.Fatalf("second = %d (err=%v)", second, err)
	}
}

// buildPlayerAction 构造一个 Player Action 包。
func buildPlayerAction(status int32, x, y, z int, face byte) []byte {
	packet := AppendVarInt(nil, PlayServerboundPacketIDPlayerAction)
	packet = AppendVarInt(packet, status)
	packet = AppendInt64(packet, PackPosition(x, y, z))
	packet = append(packet, face)
	return AppendVarInt(packet, 0) // sequence
}

// TestParsePlayerAction 验证 Player Action 包解析与非法输入。
func TestParsePlayerAction(t *testing.T) {
	action, err := ParsePlayerAction(buildPlayerAction(2, -5, 64, 9, 1))
	if err != nil {
		t.Fatal(err)
	}
	if action.Status != 2 || action.X != -5 || action.Y != 64 || action.Z != 9 || action.Face != 1 {
		t.Fatalf("解析结果不符：%+v", action)
	}
	if _, err := ParsePlayerAction([]byte{0x77}); err == nil {
		t.Fatal("错误的包 ID 应返回错误")
	}
	if _, err := ParsePlayerAction(buildPlayerAction(2, 0, 0, 0, 0)[:4]); err == nil {
		t.Fatal("截断的包应返回错误")
	}
}

// TestParseUseItemOn 验证 Use Item On 包解析。
func TestParseUseItemOn(t *testing.T) {
	packet := AppendVarInt(nil, PlayServerboundPacketIDUseItemOn)
	packet = AppendVarInt(packet, 0) // hand
	packet = AppendInt64(packet, PackPosition(1, 70, -2))
	packet = AppendVarInt(packet, 3) // direction
	packet = AppendFloat32(packet, 0.5)
	packet = AppendFloat32(packet, 0.25)
	packet = AppendFloat32(packet, 1)
	packet = AppendBool(packet, false) // insideBlock
	packet = AppendBool(packet, false) // worldBorderHit
	packet = AppendVarInt(packet, 9)   // sequence

	use, err := ParseUseItemOn(packet)
	if err != nil {
		t.Fatal(err)
	}
	if use.Hand != 0 || use.X != 1 || use.Y != 70 || use.Z != -2 || use.Direction != 3 {
		t.Fatalf("解析结果不符：%+v", use)
	}
	if use.CursorX != 0.5 || use.CursorY != 0.25 || use.CursorZ != 1 || use.InsideBlock {
		t.Fatalf("光标数据不符：%+v", use)
	}
	if _, err := ParseUseItemOn([]byte{0x00}); err == nil {
		t.Fatal("错误的包 ID 应返回错误")
	}
}

// TestParseSetCarriedItem 验证快捷栏切换解析与越界拒绝。
func TestParseSetCarriedItem(t *testing.T) {
	packet := AppendVarInt(nil, PlayServerboundPacketIDSetCarriedItem)
	packet = AppendVarInt(packet, 5)
	slot, err := ParseSetCarriedItem(packet)
	if err != nil || slot != 5 {
		t.Fatalf("slot = %d (err=%v)", slot, err)
	}
	bad := AppendVarInt(nil, PlayServerboundPacketIDSetCarriedItem)
	bad = AppendVarInt(bad, 9)
	if _, err := ParseSetCarriedItem(bad); err == nil {
		t.Fatal("越界槽位应返回错误")
	}
}

// TestParseSetCreativeSlot 验证创造模式物品栏设置解析（含带组件物品的拒绝）。
func TestParseSetCreativeSlot(t *testing.T) {
	// 清空槽位（数量 0）。
	empty := AppendVarInt(nil, PlayServerboundPacketIDSetCreativeSlot)
	buf := make([]byte, 2)
	binary.BigEndian.PutUint16(buf, uint16(5))
	empty = append(empty, buf...)
	empty = AppendVarInt(empty, 0)
	slot, itemID, count, err := ParseSetCreativeSlot(empty)
	if err != nil || slot != 5 || itemID != 0 || count != 0 {
		t.Fatalf("空槽位解析 = (%d,%d,%d,%v)", slot, itemID, count, err)
	}

	// 无组件的物品。
	withItem := AppendVarInt(nil, PlayServerboundPacketIDSetCreativeSlot)
	withItem = append(withItem, buf...)
	withItem = AppendVarInt(withItem, 64)  // count
	withItem = AppendVarInt(withItem, 123) // item ID
	withItem = AppendVarInt(withItem, 0)   // added components
	withItem = AppendVarInt(withItem, 0)   // removed components
	slot, itemID, count, err = ParseSetCreativeSlot(withItem)
	if err != nil || slot != 5 || itemID != 123 || count != 64 {
		t.Fatalf("物品解析 = (%d,%d,%d,%v)", slot, itemID, count, err)
	}

	// 带数据组件：拒绝。
	withComponents := AppendVarInt(nil, PlayServerboundPacketIDSetCreativeSlot)
	withComponents = append(withComponents, buf...)
	withComponents = AppendVarInt(withComponents, 1) // count
	withComponents = AppendVarInt(withComponents, 1) // item ID
	withComponents = AppendVarInt(withComponents, 1) // added components
	withComponents = AppendVarInt(withComponents, 0) // removed components
	if _, _, _, err := ParseSetCreativeSlot(withComponents); !errors.Is(err, errUnsupportedComponents) {
		t.Fatalf("err = %v, want errUnsupportedComponents", err)
	}
}

// TestParseSwingArm 验证挥手包解析。
func TestParseSwingArm(t *testing.T) {
	packet := AppendVarInt(nil, PlayServerboundPacketIDSwingArm)
	packet = AppendVarInt(packet, 0)
	if err := ParseSwingArm(packet); err != nil {
		t.Fatal(err)
	}
	if err := ParseSwingArm([]byte{0x00, 0x00}); err == nil {
		t.Fatal("错误的包 ID 应返回错误")
	}
}
