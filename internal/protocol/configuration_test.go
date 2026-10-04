package protocol

import "testing"

func TestEncodeBrand(t *testing.T) {
	packet := EncodeBrand("gmcs")
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != ConfigPacketIDPluginMessage {
		t.Fatalf("expected packet ID %#x, got %#x (err=%v)", ConfigPacketIDPluginMessage, packetID, err)
	}
	channel, offset, err := readStringAt(packet, offset)
	if err != nil || channel != "minecraft:brand" {
		t.Fatalf("unexpected brand channel %q (err=%v)", channel, err)
	}
	brand, offset, err := readStringAt(packet, offset)
	if err != nil || brand != "gmcs" || offset != len(packet) {
		t.Fatalf("unexpected brand %q (err=%v)", brand, err)
	}
}

func TestEncodeFeatureFlags(t *testing.T) {
	packet := EncodeFeatureFlags([]string{"minecraft:vanilla", "minecraft:test"})
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != ConfigPacketIDFeatureFlags {
		t.Fatalf("unexpected packet ID %#x (err=%v)", packetID, err)
	}
	count, offset, err := decodeVarIntAt(packet, offset)
	if err != nil || count != 2 {
		t.Fatalf("unexpected feature flag count %d (err=%v)", count, err)
	}
	for _, want := range []string{"minecraft:vanilla", "minecraft:test"} {
		flag, next, err := readStringAt(packet, offset)
		if err != nil || flag != want {
			t.Fatalf("unexpected feature flag %q, want %q (err=%v)", flag, want, err)
		}
		offset = next
	}
	if offset != len(packet) {
		t.Fatal("trailing data in feature flags packet")
	}
}

func TestEncodeKnownPacks(t *testing.T) {
	packet := EncodeKnownPacks([]KnownPack{{Namespace: "minecraft", ID: "core", Version: "1.21.11"}})
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != ConfigPacketIDKnownPacks {
		t.Fatalf("unexpected packet ID %#x (err=%v)", packetID, err)
	}
	count, offset, err := decodeVarIntAt(packet, offset)
	if err != nil || count != 1 {
		t.Fatalf("unexpected known pack count %d (err=%v)", count, err)
	}
	for _, want := range []string{"minecraft", "core", "1.21.11"} {
		value, next, err := readStringAt(packet, offset)
		if err != nil || value != want {
			t.Fatalf("unexpected known pack field %q, want %q (err=%v)", value, want, err)
		}
		offset = next
	}
	if offset != len(packet) {
		t.Fatal("trailing data in known packs packet")
	}
}

func TestParseKnownPacksResponse(t *testing.T) {
	packet := AppendVarInt(nil, ConfigServerboundPacketIDKnownPacks)
	packet = AppendVarInt(packet, 1)
	packet = appendTestString(t, packet, "minecraft")
	packet = appendTestString(t, packet, "core")
	packet = appendTestString(t, packet, "1.21.11")
	packs, err := ParseKnownPacksResponse(packet)
	if err != nil {
		t.Fatal(err)
	}
	if len(packs) != 1 || packs[0] != (KnownPack{Namespace: "minecraft", ID: "core", Version: "1.21.11"}) {
		t.Fatalf("unexpected known packs: %+v", packs)
	}

	if _, err := ParseKnownPacksResponse(append(packet, 0x00)); err == nil {
		t.Fatal("expected trailing data to be rejected")
	}
	if _, err := ParseKnownPacksResponse(AppendVarInt(nil, ConfigPacketIDFeatureFlags)); err == nil {
		t.Fatal("expected wrong packet ID to be rejected")
	}
}

func TestEncodeRegistryData(t *testing.T) {
	packet := EncodeRegistryData("minecraft:dimension_type", []RegistryEntry{{Name: "minecraft:overworld"}})
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != ConfigPacketIDRegistryData {
		t.Fatalf("unexpected packet ID %#x (err=%v)", packetID, err)
	}
	registry, offset, err := readStringAt(packet, offset)
	if err != nil || registry != "minecraft:dimension_type" {
		t.Fatalf("unexpected registry %q (err=%v)", registry, err)
	}
	count, offset, err := decodeVarIntAt(packet, offset)
	if err != nil || count != 1 {
		t.Fatalf("unexpected entry count %d (err=%v)", count, err)
	}
	name, offset, err := readStringAt(packet, offset)
	if err != nil || name != "minecraft:overworld" {
		t.Fatalf("unexpected entry %q (err=%v)", name, err)
	}
	if packet[offset] != 0x00 {
		t.Fatal("expected omitted NBT marker for known-pack entry")
	}
	if offset+1 != len(packet) {
		t.Fatal("trailing data in registry data packet")
	}
}

func TestEncodeRegistryDataWithNBT(t *testing.T) {
	packet := EncodeRegistryData("minecraft:test", []RegistryEntry{{Name: "test:entry", Data: []byte{0x01, 0x02}}})
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != ConfigPacketIDRegistryData {
		t.Fatalf("unexpected packet ID %#x (err=%v)", packetID, err)
	}
	if _, offset, err = readStringAt(packet, offset); err != nil {
		t.Fatal(err)
	}
	if _, offset, err = decodeVarIntAt(packet, offset); err != nil {
		t.Fatal(err)
	}
	if _, offset, err = readStringAt(packet, offset); err != nil {
		t.Fatal(err)
	}
	if packet[offset] != 0x01 || packet[offset+1] != 0x01 || packet[offset+2] != 0x02 {
		t.Fatalf("unexpected NBT marker/payload at %d: % x", offset, packet[offset:])
	}
}

func TestEncodeUpdateTags(t *testing.T) {
	packet := EncodeUpdateTags([]TagRegistry{{
		Registry: "minecraft:damage_type",
		Tags: []TagEntry{
			{Name: "minecraft:is_explosion", Entries: []int32{3, 0, 1}},
			{Name: "minecraft:is_fire", Entries: []int32{7}},
		},
	}})
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != ConfigPacketIDTags {
		t.Fatalf("unexpected packet ID %#x (err=%v)", packetID, err)
	}
	registryCount, offset, err := decodeVarIntAt(packet, offset)
	if err != nil || registryCount != 1 {
		t.Fatalf("unexpected registry count %d (err=%v)", registryCount, err)
	}
	registry, offset, err := readStringAt(packet, offset)
	if err != nil || registry != "minecraft:damage_type" {
		t.Fatalf("unexpected registry %q (err=%v)", registry, err)
	}
	tagCount, offset, err := decodeVarIntAt(packet, offset)
	if err != nil || tagCount != 2 {
		t.Fatalf("unexpected tag count %d (err=%v)", tagCount, err)
	}
	tagName, offset, err := readStringAt(packet, offset)
	if err != nil || tagName != "minecraft:is_explosion" {
		t.Fatalf("unexpected tag %q (err=%v)", tagName, err)
	}
	entryCount, offset, err := decodeVarIntAt(packet, offset)
	if err != nil || entryCount != 3 {
		t.Fatalf("unexpected entry count %d (err=%v)", entryCount, err)
	}
	for _, want := range []int32{3, 0, 1} {
		id, next, err := decodeVarIntAt(packet, offset)
		if err != nil || id != want {
			t.Fatalf("unexpected tag entry %d, want %d (err=%v)", id, want, err)
		}
		offset = next
	}
	if tagName, offset, err = readStringAt(packet, offset); err != nil || tagName != "minecraft:is_fire" {
		t.Fatalf("unexpected tag %q (err=%v)", tagName, err)
	}
	entryCount, offset, err = decodeVarIntAt(packet, offset)
	if err != nil || entryCount != 1 {
		t.Fatalf("unexpected entry count %d (err=%v)", entryCount, err)
	}
	id, offset, err := decodeVarIntAt(packet, offset)
	if err != nil || id != 7 || offset != len(packet) {
		t.Fatalf("unexpected entry %d (err=%v)", id, err)
	}
}

func TestEncodeUpdateTagsEmpty(t *testing.T) {
	packet := EncodeUpdateTags(nil)
	if len(packet) != 2 || packet[0] != ConfigPacketIDTags || packet[1] != 0x00 {
		t.Fatalf("unexpected empty update tags packet: % x", packet)
	}
}

func TestParseClientInformation(t *testing.T) {
	packet := AppendVarInt(nil, ConfigPacketIDClientInformation)
	packet = appendTestString(t, packet, "zh_CN")
	packet = append(packet, 12) // view distance
	packet = AppendVarInt(packet, 0)
	packet = AppendBool(packet, true)
	packet = append(packet, 0x7F)
	packet = AppendVarInt(packet, 1)
	packet = AppendBool(packet, false)
	packet = AppendBool(packet, true)
	packet = AppendVarInt(packet, 2)

	info, err := ParseClientInformation(packet)
	if err != nil {
		t.Fatal(err)
	}
	want := ClientInformation{
		Locale: "zh_CN", ViewDistance: 12, ChatMode: 0, ChatColors: true,
		SkinParts: 0x7F, MainHand: 1, EnableServerListing: true, ParticleStatus: 2,
	}
	if info != want {
		t.Fatalf("unexpected client information: %+v, want %+v", info, want)
	}
}

func TestParseFinishConfigurationAck(t *testing.T) {
	if err := ParseFinishConfigurationAck(AppendVarInt(nil, ConfigPacketIDFinishConfiguration)); err != nil {
		t.Fatalf("valid packet rejected: %v", err)
	}
	if err := ParseFinishConfigurationAck([]byte{0x03, 0x01}); err == nil {
		t.Fatal("expected trailing data to be rejected")
	}
}

// appendTestString 手工构造带 VarInt 长度前缀的字符串。
func appendTestString(t *testing.T, dst []byte, value string) []byte {
	t.Helper()
	dst = AppendVarInt(dst, int32(len(value)))
	return append(dst, value...)
}
