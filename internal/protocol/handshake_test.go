package protocol

import "testing"

func TestParseHandshake(t *testing.T) {
	packet := AppendVarInt(nil, 0)
	packet = AppendVarInt(packet, 765)
	packet = AppendVarInt(packet, int32(len("localhost")))
	packet = append(packet, "localhost"...)
	packet = append(packet, 0x63, 0xdd)
	packet = AppendVarInt(packet, 1)

	handshake, err := ParseHandshake(packet)
	if err != nil {
		t.Fatal(err)
	}
	if handshake.ProtocolVersion != 765 || handshake.ServerAddress != "localhost" || handshake.ServerPort != 25565 || handshake.NextState != 1 {
		t.Fatalf("unexpected handshake: %+v", handshake)
	}
}

func TestParseHandshakeRejectsInvalidPackets(t *testing.T) {
	tests := []struct {
		name   string
		packet []byte
	}{
		{name: "wrong packet ID", packet: []byte{1}},
		{name: "truncated fields", packet: []byte{0, 1}},
		{name: "unsupported next state", packet: []byte{0, 1, 0, 0, 0, 3}},
		{name: "trailing bytes", packet: []byte{0, 1, 0, 0, 0, 1, 9}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseHandshake(test.packet); err == nil {
				t.Fatal("expected invalid handshake error")
			}
		})
	}
}
