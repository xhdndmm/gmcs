package protocol

import "testing"

func TestParseLoginStart(t *testing.T) {
	packet := AppendVarInt(nil, 0)
	packet = appendString(packet, "Steve")
	login, err := ParseLoginStart(packet)
	if err != nil {
		t.Fatal(err)
	}
	if login.Name != "Steve" {
		t.Fatalf("unexpected login name: %q", login.Name)
	}
}

func TestEncodeLoginSuccess(t *testing.T) {
	encoded := EncodeLoginSuccess("Steve")
	if len(encoded) == 0 {
		t.Fatal("login success packet should not be empty")
	}
	packetID, offset, err := DecodeVarInt(encoded)
	if err != nil || packetID != 2 {
		t.Fatalf("expected packet ID 2, got %d, err=%v", packetID, err)
	}
	uuid, _, err := readStringAt(encoded, offset)
	if err != nil {
		t.Fatal(err)
	}
	if uuid == "" {
		t.Fatal("expected UUID in login success packet")
	}
}
