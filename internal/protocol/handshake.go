package protocol

import (
	"fmt"
	"unicode/utf8"
)

type Handshake struct {
	ProtocolVersion int32
	ServerAddress   string
	ServerPort      uint16
	NextState       int32
}

func ParseHandshake(packet []byte) (Handshake, error) {
	var handshake Handshake
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != 0 {
		return handshake, fmt.Errorf("invalid handshake packet ID")
	}
	handshake.ProtocolVersion, offset, err = decodeVarIntAt(packet, offset)
	if err != nil {
		return handshake, fmt.Errorf("read handshake protocol version: %w", err)
	}
	addressLength, offset, err := decodeVarIntAt(packet, offset)
	if err != nil || addressLength < 0 || addressLength > 255 || int(addressLength) > len(packet)-offset {
		return handshake, fmt.Errorf("invalid handshake server address length")
	}
	addressEnd := offset + int(addressLength)
	address := packet[offset:addressEnd]
	if !utf8.Valid(address) {
		return handshake, fmt.Errorf("handshake server address is not valid UTF-8")
	}
	handshake.ServerAddress = string(address)
	offset = addressEnd
	if len(packet)-offset < 3 {
		return handshake, fmt.Errorf("incomplete handshake port or next state")
	}
	handshake.ServerPort = uint16(packet[offset])<<8 | uint16(packet[offset+1])
	offset += 2
	handshake.NextState, offset, err = decodeVarIntAt(packet, offset)
	if err != nil {
		return handshake, fmt.Errorf("read handshake next state: %w", err)
	}
	if offset != len(packet) || (handshake.NextState != 1 && handshake.NextState != 2) {
		return handshake, fmt.Errorf("invalid handshake next state or trailing data")
	}
	return handshake, nil
}

func decodeVarIntAt(data []byte, offset int) (int32, int, error) {
	value, size, err := DecodeVarInt(data[offset:])
	if err != nil {
		return 0, offset, err
	}
	return value, offset + size, nil
}
