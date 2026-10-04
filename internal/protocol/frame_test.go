package protocol

import (
	"bytes"
	"errors"
	"testing"
)

func TestVarIntRoundTrip(t *testing.T) {
	for _, value := range []int32{0, 1, 127, 128, 255, 2147483647, -1, -2147483648} {
		encoded := AppendVarInt(nil, value)
		decoded, size, err := DecodeVarInt(encoded)
		if err != nil {
			t.Fatalf("DecodeVarInt(%d): %v", value, err)
		}
		if decoded != value || size != len(encoded) {
			t.Fatalf("round trip = (%d, %d), want (%d, %d)", decoded, size, value, len(encoded))
		}
	}
}

func TestDecodeVarIntRejectsMalformedAndIncompleteValues(t *testing.T) {
	for _, encoded := range [][]byte{{0x80}, {0xff, 0xff, 0xff, 0xff, 0x10}, {0x80, 0x80, 0x80, 0x80, 0x80, 0x00}} {
		if _, _, err := DecodeVarInt(encoded); !errors.Is(err, ErrMalformedVarInt) {
			t.Fatalf("DecodeVarInt(%v) error = %v, want ErrMalformedVarInt", encoded, err)
		}
	}
}

func TestPacketRoundTrip(t *testing.T) {
	want := []byte{0x00, 0x01, 0x02, 0x03}
	var framed bytes.Buffer
	if err := WritePacket(&framed, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadPacket(&framed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("ReadPacket() = %v, want %v", got, want)
	}
}

func TestReadPacketRejectsOversizedFrame(t *testing.T) {
	frame := AppendVarInt(nil, MaxPacketSize+1)
	if _, err := ReadPacket(bytes.NewReader(frame)); err == nil {
		t.Fatal("expected oversized frame error")
	}
}

func TestDecodeVarInt(t *testing.T) {
	for _, test := range []struct {
		encoded []byte
		want    int32
		size    int
	}{
		{encoded: []byte{0x00}, want: 0, size: 1},
		{encoded: []byte{0xac, 0x02}, want: 300, size: 2},
	} {
		got, size, err := DecodeVarInt(test.encoded)
		if err != nil || got != test.want || size != test.size {
			t.Fatalf("DecodeVarInt(%v) = (%d, %d, %v), want (%d, %d, nil)", test.encoded, got, size, err, test.want, test.size)
		}
	}
}
