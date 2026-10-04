package protocol

import (
	"crypto/md5"
	"fmt"
)

// 1.21.11（协议 774）登录阶段的包 ID。
const (
	LoginPacketIDStart          = 0x00
	LoginPacketIDSuccess        = 0x02
	LoginPacketIDSetCompression = 0x03

	LoginServerboundPacketIDAcknowledged = 0x03
)

// LoginStart 是客户端发出的登录起始数据。
// 自 1.20.2 起该数据包附带玩家 UUID；离线服务器可忽略并自行推导。
type LoginStart struct {
	Name string
	UUID [16]byte
}

func ParseLoginStart(packet []byte) (LoginStart, error) {
	var login LoginStart
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != LoginPacketIDStart {
		return login, fmt.Errorf("invalid login start packet")
	}
	name, offset, err := readStringAt(packet, offset)
	if err != nil {
		return login, fmt.Errorf("read login username: %w", err)
	}
	if len(packet)-offset != len(login.UUID) {
		return login, fmt.Errorf("invalid login start UUID length %d", len(packet)-offset)
	}
	copy(login.UUID[:], packet[offset:])
	login.Name = name
	return login, nil
}

// ParseLoginAcknowledged 校验 Login Acknowledged 包，该包将连接切换到 Configuration 阶段。
func ParseLoginAcknowledged(packet []byte) error {
	packetID, size, err := DecodeVarInt(packet)
	if err != nil || packetID != LoginServerboundPacketIDAcknowledged || size != len(packet) {
		return fmt.Errorf("invalid login acknowledged packet")
	}
	return nil
}

func EncodeSetCompression(threshold int32) []byte {
	packet := AppendVarInt(nil, int32(LoginPacketIDSetCompression))
	packet = AppendVarInt(packet, threshold)
	return packet
}

// EncodeLoginSuccess 编码 Login Success 包（1.21.11）。
// 结构：UUID（16 字节二进制）、用户名（String）、属性数量（VarInt）。
func EncodeLoginSuccess(uuid [16]byte, username string) []byte {
	packet := AppendVarInt(nil, int32(LoginPacketIDSuccess))
	packet = append(packet, uuid[:]...)
	packet = appendString(packet, username)
	packet = AppendVarInt(packet, 0) // 离线模式没有纹理签名属性
	return packet
}

// OfflineUUID 按原版离线模式规则生成 UUID：MD5("OfflinePlayer:"+username)，并设置 v3 变体位。
func OfflineUUID(username string) [16]byte {
	sum := md5.Sum([]byte("OfflinePlayer:" + username))
	sum[6] = (sum[6] & 0x0f) | 0x30
	sum[8] = (sum[8] & 0x3f) | 0x80
	return sum
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
