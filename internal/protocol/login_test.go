package protocol

import (
	"fmt"
	"testing"
)

func TestParseLoginStart(t *testing.T) {
	uuid := OfflineUUID("Steve")
	packet := AppendVarInt(nil, 0)
	packet = appendString(packet, "Steve")
	packet = append(packet, uuid[:]...)
	login, err := ParseLoginStart(packet)
	if err != nil {
		t.Fatal(err)
	}
	if login.Name != "Steve" {
		t.Fatalf("unexpected login name: %q", login.Name)
	}
	if login.UUID != uuid {
		t.Fatalf("unexpected login uuid: %x", login.UUID)
	}
}

func TestParseLoginStartRejectsTruncatedUUID(t *testing.T) {
	packet := AppendVarInt(nil, 0)
	packet = appendString(packet, "Steve")
	packet = append(packet, 1, 2, 3)
	if _, err := ParseLoginStart(packet); err == nil {
		t.Fatal("expected error for truncated UUID")
	}
}

func TestEncodeLoginSuccess(t *testing.T) {
	uuid := OfflineUUID("Steve")
	encoded := EncodeLoginSuccess(uuid, "Steve")
	packetID, offset, err := DecodeVarInt(encoded)
	if err != nil || packetID != LoginPacketIDSuccess {
		t.Fatalf("expected packet ID %d, got %d, err=%v", LoginPacketIDSuccess, packetID, err)
	}
	if len(encoded)-offset < len(uuid) {
		t.Fatal("expected 16-byte UUID in login success packet")
	}
	if string(encoded[offset:offset+len(uuid)]) != string(uuid[:]) {
		t.Fatal("login success UUID mismatch")
	}
	name, offset, err := readStringAt(encoded, offset+len(uuid))
	if err != nil || name != "Steve" {
		t.Fatalf("unexpected login success name %q, err=%v", name, err)
	}
	properties, size, err := DecodeVarInt(encoded[offset:])
	if err != nil || properties != 0 || size != len(encoded)-offset {
		t.Fatalf("unexpected properties count %d (err=%v)", properties, err)
	}
}

func TestOfflineUUIDMatchesVanilla(t *testing.T) {
	uuid := OfflineUUID("Notch")
	formatted := fmt.Sprintf("%x-%x-%x-%x-%x", uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16])
	const want = "b50ad385-829d-3141-a216-7e7d7539ba7f"
	if formatted != want {
		t.Fatalf("offline UUID mismatch: got %s, want %s", formatted, want)
	}
}

func TestParseLoginAcknowledged(t *testing.T) {
	if err := ParseLoginAcknowledged(AppendVarInt(nil, 3)); err != nil {
		t.Fatalf("valid packet rejected: %v", err)
	}
	if err := ParseLoginAcknowledged([]byte{0x03, 0x00}); err == nil {
		t.Fatal("expected trailing data to be rejected")
	}
	if err := ParseLoginAcknowledged([]byte{0x01}); err == nil {
		t.Fatal("expected wrong packet ID to be rejected")
	}
}
