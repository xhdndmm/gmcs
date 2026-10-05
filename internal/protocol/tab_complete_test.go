package protocol

import "testing"

// TestParseTabCompleteRequest 验证 Command Suggestion Request（0x0E）的解析。
func TestParseTabCompleteRequest(t *testing.T) {
	packet := AppendVarInt(nil, PlayServerboundPacketIDTabComplete)
	packet = AppendVarInt(packet, 7)
	packet = appendString(packet, "/gam")
	id, text, err := ParseTabCompleteRequest(packet)
	if err != nil || id != 7 || text != "/gam" {
		t.Fatalf("tab complete request = (%d, %q), err=%v", id, text, err)
	}
	if _, _, err := ParseTabCompleteRequest(AppendVarInt(nil, 0x7f)); err == nil {
		t.Fatal("expected error for wrong packet ID")
	}
}

// TestEncodeTabCompleteResponse 验证 Command Suggestions Response（0x0F）的编码。
func TestEncodeTabCompleteResponse(t *testing.T) {
	packet := EncodeTabCompleteResponse(9, 0, 4, []string{"/help", "/list"})
	packetID, cursor, err := DecodeVarInt(packet)
	if err != nil || packetID != PlayPacketIDTabComplete {
		t.Fatalf("packet id = %d, err=%v", packetID, err)
	}
	transactionID, n, err := DecodeVarInt(packet[cursor:])
	if err != nil || transactionID != 9 {
		t.Fatalf("transactionId = %d (err=%v), want 9", transactionID, err)
	}
	cursor += n
	start, n, err := DecodeVarInt(packet[cursor:])
	if err != nil || start != 0 {
		t.Fatalf("start = %d (err=%v)", start, err)
	}
	cursor += n
	length, n, err := DecodeVarInt(packet[cursor:])
	if err != nil || length != 4 {
		t.Fatalf("length = %d (err=%v)", length, err)
	}
	cursor += n
	count, n, err := DecodeVarInt(packet[cursor:])
	if err != nil || count != 2 {
		t.Fatalf("count = %d (err=%v)", count, err)
	}
	cursor += n
	match, next, err := readStringAt(packet, cursor)
	if err != nil || match != "/help" {
		t.Fatalf("match[0] = %q, err=%v", match, err)
	}
	cursor = next // readStringAt 返回字符串之后的绝对偏移。
	if cursor >= len(packet) || packet[cursor] != 0x00 {
		t.Fatalf("tooltip flag missing after match[0]")
	}
	cursor++
	match, next, err = readStringAt(packet, cursor)
	if err != nil || match != "/list" {
		t.Fatalf("match[1] = %q, err=%v", match, err)
	}
	cursor = next
	if cursor+1 != len(packet) || packet[cursor] != 0x00 {
		t.Fatalf("trailing bytes after last match: cursor=%d len=%d", cursor, len(packet))
	}
}
