package protocol

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestPackPositionRoundTrip(t *testing.T) {
	cases := []struct{ x, y, z int }{
		{0, 0, 0},
		{1, -64, 2},
		{-1, 320, -2},
		{33554431, 2047, -33554432},
		{-33554432, -2048, 33554431},
	}
	for _, test := range cases {
		packed := PackPosition(test.x, test.y, test.z)
		x := int(packed >> 38)
		y := int(packed << 52 >> 52)
		z := int(packed << 26 >> 38)
		if x != test.x || y != test.y || z != test.z {
			t.Fatalf("position round trip failed: got (%d,%d,%d), want (%d,%d,%d)",
				x, y, z, test.x, test.y, test.z)
		}
	}
}

func TestEncodeLoginPlay(t *testing.T) {
	data := LoginPlayData{
		EntityID:            7,
		DimensionNames:      []string{"minecraft:overworld"},
		MaxPlayers:          20,
		ViewDistance:        10,
		SimulationDistance:  8,
		EnableRespawnScreen: true,
		DimensionTypeID:     0,
		DimensionName:       "minecraft:overworld",
		GameMode:            1,
		SeaLevel:            63,
	}
	packet := EncodeLoginPlay(data)
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != PlayPacketIDLogin {
		t.Fatalf("unexpected packet ID %#x (err=%v)", packetID, err)
	}
	entityID, offset, err := DecodeInt32(packet, offset)
	if err != nil || entityID != 7 {
		t.Fatalf("unexpected entity ID %d (err=%v)", entityID, err)
	}
	hardcore, offset, err := DecodeBool(packet, offset)
	if err != nil || hardcore {
		t.Fatalf("unexpected hardcore flag (err=%v)", err)
	}
	worldCount, offset, err := decodeVarIntAt(packet, offset)
	if err != nil || worldCount != 1 {
		t.Fatalf("unexpected dimension name count %d (err=%v)", worldCount, err)
	}
	name, offset, err := readStringAt(packet, offset)
	if err != nil || name != "minecraft:overworld" {
		t.Fatalf("unexpected dimension name %q (err=%v)", name, err)
	}
	maxPlayers, offset, err := decodeVarIntAt(packet, offset)
	if err != nil || maxPlayers != 20 {
		t.Fatalf("unexpected max players %d (err=%v)", maxPlayers, err)
	}
}

func TestEncodeGameEvent(t *testing.T) {
	packet := EncodeGameEvent(13, 0)
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != PlayPacketIDGameEvent {
		t.Fatalf("unexpected packet ID %#x (err=%v)", packetID, err)
	}
	if len(packet)-offset != 5 || packet[offset] != 13 {
		t.Fatalf("unexpected game event payload: % x", packet[offset:])
	}
}

func TestEncodeSynchronizePlayerPosition(t *testing.T) {
	packet := EncodeSynchronizePlayerPosition(9, 0.5, 64, -0.25, 1, 2, 3, 90, -45)
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != PlayPacketIDSynchronizePlayerPos {
		t.Fatalf("unexpected packet ID %#x (err=%v)", packetID, err)
	}
	teleportID, offset, err := decodeVarIntAt(packet, offset)
	if err != nil || teleportID != 9 {
		t.Fatalf("unexpected teleport ID %d (err=%v)", teleportID, err)
	}
	nextFloat64 := func() float64 {
		t.Helper()
		bits, next, err := DecodeInt64(packet, offset)
		if err != nil {
			t.Fatal(err)
		}
		offset = next
		return math.Float64frombits(uint64(bits))
	}
	for _, want := range []float64{0.5, 64, -0.25, 1, 2, 3} {
		if got := nextFloat64(); got != want {
			t.Fatalf("unexpected coordinate %v, want %v", got, want)
		}
	}
	yawBits, offset, err := DecodeInt32(packet, offset)
	if err != nil || math.Float32frombits(uint32(yawBits)) != 90 {
		t.Fatalf("unexpected yaw (err=%v)", err)
	}
	pitchBits, offset, err := DecodeInt32(packet, offset)
	if err != nil || math.Float32frombits(uint32(pitchBits)) != -45 {
		t.Fatalf("unexpected pitch (err=%v)", err)
	}
	flags, offset, err := DecodeInt32(packet, offset)
	if err != nil || flags != 0 || offset != len(packet) {
		t.Fatalf("unexpected teleport flags %d (err=%v)", flags, err)
	}
}

func TestEncodePlayerInfoAddPlayer(t *testing.T) {
	uuid := OfflineUUID("Steve")
	packet := EncodePlayerInfoAddPlayer(uuid, "Steve")
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != PlayPacketIDPlayerInfoUpdate {
		t.Fatalf("unexpected packet ID %#x (err=%v)", packetID, err)
	}
	actions := packet[offset]
	offset++
	if actions != 0x01|0x08 {
		t.Fatalf("unexpected actions mask %#x", actions)
	}
	count, offset, err := decodeVarIntAt(packet, offset)
	if err != nil || count != 1 {
		t.Fatalf("unexpected player count %d (err=%v)", count, err)
	}
	if len(packet)-offset < len(uuid) || string(packet[offset:offset+len(uuid)]) != string(uuid[:]) {
		t.Fatal("unexpected player UUID")
	}
	offset += len(uuid)
	name, offset, err := readStringAt(packet, offset)
	if err != nil || name != "Steve" {
		t.Fatalf("unexpected player name %q (err=%v)", name, err)
	}
	properties, offset, err := decodeVarIntAt(packet, offset)
	if err != nil || properties != 0 {
		t.Fatalf("unexpected properties count %d (err=%v)", properties, err)
	}
	if offset >= len(packet) || packet[offset] != 0x01 {
		t.Fatal("expected listed=true")
	}
	if offset+1 != len(packet) {
		t.Fatal("trailing data in player info packet")
	}
}

func TestEncodeSystemChat(t *testing.T) {
	packet := EncodeSystemChat("hi")
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != PlayPacketIDSystemChat {
		t.Fatalf("unexpected packet ID %#x (err=%v)", packetID, err)
	}
	if packet[offset] != nbtTagString {
		t.Fatalf("expected NBT string tag, got %#x", packet[offset])
	}
	offset++
	if len(packet)-offset < 2 {
		t.Fatal("missing NBT string length")
	}
	length := int(binary.BigEndian.Uint16(packet[offset:]))
	offset += 2
	if len(packet)-offset < length+1 || string(packet[offset:offset+length]) != "hi" {
		t.Fatalf("unexpected NBT string payload: % x", packet[offset:])
	}
	offset += length
	if packet[offset] != 0x00 || offset+1 != len(packet) {
		t.Fatal("expected overlay=false as final field")
	}
}

func TestParsePlayKeepAlive(t *testing.T) {
	packet := AppendVarInt(nil, PlayServerboundPacketIDKeepAlive)
	packet = AppendInt64(packet, 12345)
	id, err := ParsePlayKeepAlive(packet)
	if err != nil || id != 12345 {
		t.Fatalf("unexpected keep alive ID %d (err=%v)", id, err)
	}
}

func TestParseConfirmTeleportation(t *testing.T) {
	packet := AppendVarInt(nil, PlayServerboundPacketIDConfirmTeleportation)
	packet = AppendVarInt(packet, 3)
	id, err := ParseConfirmTeleportation(packet)
	if err != nil || id != 3 {
		t.Fatalf("unexpected teleport ID %d (err=%v)", id, err)
	}
	if _, err := ParseConfirmTeleportation([]byte{PlayServerboundPacketIDKeepAlive}); err == nil {
		t.Fatal("expected wrong packet ID to be rejected")
	}
}

func TestParsePlayClientCommand(t *testing.T) {
	packet := AppendVarInt(nil, PlayServerboundPacketIDClientCommand)
	packet = AppendVarInt(packet, 0)
	action, err := ParsePlayClientCommand(packet)
	if err != nil || action != 0 {
		t.Fatalf("unexpected client command action %d (err=%v)", action, err)
	}
}
