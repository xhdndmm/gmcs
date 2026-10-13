package protocol

import "testing"

// TestEncodeWorldEvent 验证 World Event（Level Event）包编码：
// 包 ID、事件编号（i32）、位置（packed Position）、数据（i32）与全局标志。
func TestEncodeWorldEvent(t *testing.T) {
	packet := EncodeWorldEvent(WorldEventBrewingStandBrew, 10, 64, -3, 0, false)
	id, offset, err := DecodeVarInt(packet)
	if err != nil || id != PlayPacketIDWorldEvent {
		t.Fatalf("包 ID = %d (err=%v), want %d", id, err, PlayPacketIDWorldEvent)
	}
	effectID, offset, err := DecodeInt32(packet, offset)
	if err != nil || effectID != WorldEventBrewingStandBrew {
		t.Fatalf("事件编号 = %d (err=%v), want %d", effectID, err, WorldEventBrewingStandBrew)
	}
	packed, offset, err := DecodeInt64(packet, offset)
	if err != nil || packed != PackPosition(10, 64, -3) {
		t.Fatalf("位置 = %d (err=%v), want %d", packed, err, PackPosition(10, 64, -3))
	}
	data, offset, err := DecodeInt32(packet, offset)
	if err != nil || data != 0 {
		t.Fatalf("数据 = %d (err=%v), want 0", data, err)
	}
	global, offset, err := DecodeBool(packet, offset)
	if err != nil || global {
		t.Fatalf("全局标志 = %v (err=%v), want false", global, err)
	}
	if offset != len(packet) {
		t.Fatalf("包尾有 %d 字节多余数据", len(packet)-offset)
	}
}
