package protocol

import (
	"strings"
	"testing"
)

func TestEncodeDeclareCommands(t *testing.T) {
	packet := EncodeDeclareCommands([]CommandDef{
		{Name: "help"},
		{Name: "say", ArgName: "message"},
	})

	offset := 0
	packetID, offset, err := decodeVarIntAt(packet, offset)
	if err != nil || packetID != PlayPacketIDDeclareCommands {
		t.Fatalf("packet id %#x (err=%v)", packetID, err)
	}
	nodeCount, offset, err := decodeVarIntAt(packet, offset)
	if err != nil || nodeCount != 4 {
		t.Fatalf("node count %d (err=%v)", nodeCount, err)
	}

	// 节点 0：root（flags=0，children=[1,2]）。
	if packet[offset] != 0x00 {
		t.Fatalf("root flags = %#x", packet[offset])
	}
	offset++
	childCount, offset, err := decodeVarIntAt(packet, offset)
	if err != nil || childCount != 2 {
		t.Fatalf("root children %d (err=%v)", childCount, err)
	}
	for want := int32(1); want <= 2; want++ {
		child, next, err := decodeVarIntAt(packet, offset)
		if err != nil || child != want {
			t.Fatalf("root child %d, want %d (err=%v)", child, want, err)
		}
		offset = next
	}

	// 节点 1：literal "help"（可执行、无子节点）。
	if packet[offset] != 0x05 {
		t.Fatalf("help flags = %#x", packet[offset])
	}
	offset++
	if childCount, offset, err = decodeVarIntAt(packet, offset); err != nil || childCount != 0 {
		t.Fatalf("help children %d (err=%v)", childCount, err)
	}
	name, offset, err := readStringAt(packet, offset)
	if err != nil || name != "help" {
		t.Fatalf("help name %q (err=%v)", name, err)
	}

	// 节点 2：literal "say"（不可执行，children=[3]）。
	if packet[offset] != 0x01 {
		t.Fatalf("say flags = %#x", packet[offset])
	}
	offset++
	if childCount, offset, err = decodeVarIntAt(packet, offset); err != nil || childCount != 1 {
		t.Fatalf("say children %d (err=%v)", childCount, err)
	}
	argIndex, offset, err := decodeVarIntAt(packet, offset)
	if err != nil || argIndex != 3 {
		t.Fatalf("say child %d (err=%v)", argIndex, err)
	}
	name, offset, err = readStringAt(packet, offset)
	if err != nil || name != "say" {
		t.Fatalf("say name %q (err=%v)", name, err)
	}

	// 节点 3：argument "message"（greedy string、可执行）。
	if packet[offset] != 0x06 {
		t.Fatalf("argument flags = %#x", packet[offset])
	}
	offset++
	if childCount, offset, err = decodeVarIntAt(packet, offset); err != nil || childCount != 0 {
		t.Fatalf("argument children %d (err=%v)", childCount, err)
	}
	name, offset, err = readStringAt(packet, offset)
	if err != nil || name != "message" {
		t.Fatalf("argument name %q (err=%v)", name, err)
	}
	parser, offset, err := decodeVarIntAt(packet, offset)
	if err != nil || parser != BrigadierStringParser {
		t.Fatalf("parser %d (err=%v)", parser, err)
	}
	properties, offset, err := decodeVarIntAt(packet, offset)
	if err != nil || properties != StringGreedyPhrase {
		t.Fatalf("properties %d (err=%v)", properties, err)
	}

	// rootIndex = 0，且无多余数据。
	rootIndex, offset, err := decodeVarIntAt(packet, offset)
	if err != nil || rootIndex != 0 || offset != len(packet) {
		t.Fatalf("root index %d, offset %d/%d (err=%v)", rootIndex, offset, len(packet), err)
	}
}

func TestParseChatCommand(t *testing.T) {
	packet := AppendVarInt(nil, PlayServerboundPacketIDChatCommand)
	packet = appendTestString(t, packet, "say hello")
	command, err := ParseChatCommand(packet)
	if err != nil || command != "say hello" {
		t.Fatalf("command %q (err=%v)", command, err)
	}

	// 带签名的变体（0x07）第一个字段相同，后续字段不解析。
	signed := AppendVarInt(nil, PlayServerboundPacketIDChatCommandSigned)
	signed = appendTestString(t, signed, "help")
	signed = AppendInt64(signed, 1)
	signed = AppendInt64(signed, 2)
	if command, err := ParseChatCommand(signed); err != nil || command != "help" {
		t.Fatalf("signed command %q (err=%v)", command, err)
	}

	if _, err := ParseChatCommand(AppendVarInt(nil, 0x00)); err == nil {
		t.Fatal("expected wrong packet ID to be rejected")
	}
	empty := AppendVarInt(nil, PlayServerboundPacketIDChatCommand)
	empty = appendTestString(t, empty, "")
	if _, err := ParseChatCommand(empty); err == nil {
		t.Fatal("expected empty command to be rejected")
	}
	overlong := AppendVarInt(nil, PlayServerboundPacketIDChatCommand)
	overlong = appendTestString(t, overlong, strings.Repeat("a", maxChatCommandLength+1))
	if _, err := ParseChatCommand(overlong); err == nil {
		t.Fatal("expected overlong command to be rejected")
	}
}
