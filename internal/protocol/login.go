package protocol

import (
	"crypto/md5"
	"fmt"
)

const (
	LoginPacketIDStart          = 0x00
	LoginPacketIDSuccess        = 0x02
	LoginPacketIDSetCompression = 0x03
)

type LoginStart struct {
	Name string
}

func ParseLoginStart(packet []byte) (LoginStart, error) {
	var login LoginStart
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != LoginPacketIDStart {
		return login, fmt.Errorf("invalid login start packet")
	}
	name, _, err := readStringAt(packet, offset)
	if err != nil {
		return login, fmt.Errorf("read login username: %w", err)
	}
	login.Name = name
	return login, nil
}

func EncodeSetCompression(threshold int32) []byte {
	packet := AppendVarInt(nil, int32(LoginPacketIDSetCompression))
	packet = AppendVarInt(packet, threshold)
	return packet
}

func EncodeLoginSuccess(username string) []byte {
	packet := AppendVarInt(nil, int32(LoginPacketIDSuccess))
	packet = appendString(packet, OfflineUUID(username))
	packet = appendString(packet, username)
	return packet
}

func OfflineUUID(username string) string {
	sum := md5.Sum([]byte("OfflinePlayer:" + username))
	sum[6] = (sum[6] & 0x0f) | 0x30
	sum[8] = (sum[8] & 0x3f) | 0x80
	return fmt.Sprintf("%02x%02x%02x%02x-%02x%02x-%02x%02x-%02x%02x-%02x%02x%02x%02x%02x%02x",
		sum[0], sum[1], sum[2], sum[3],
		sum[4], sum[5],
		sum[6], sum[7],
		sum[8], sum[9],
		sum[10], sum[11], sum[12], sum[13], sum[14], sum[15],
	)
}

func appendString(dst []byte, value string) []byte {
	encoded := []byte(value)
	dst = AppendVarInt(dst, int32(len(encoded)))
	return append(dst, encoded...)
}

func readStringAt(data []byte, offset int) (string, int, error) {
	length, size, err := DecodeVarInt(data[offset:])
	if err != nil {
		return "", offset, err
	}
	if length < 0 || int(length) > len(data)-offset-size {
		return "", offset, fmt.Errorf("string length out of range")
	}
	start := offset + size
	end := start + int(length)
	return string(data[start:end]), end, nil
}
