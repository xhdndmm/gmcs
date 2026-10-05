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

	// LoginPacketIDDisconnect 是 Login Disconnect（clientbound）。
	LoginPacketIDDisconnect = 0x00
	// LoginPacketIDEncryptionRequest 是 Encryption Request（clientbound，官方名 hello）。
	LoginPacketIDEncryptionRequest = 0x01

	LoginServerboundPacketIDAcknowledged = 0x03
	// LoginServerboundPacketIDEncryptionResponse 是 Encryption Response（官方名 key）。
	LoginServerboundPacketIDEncryptionResponse = 0x01
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

// GameProfileProperty 是 Login Success 包中的玩家属性（如皮肤纹理）。
type GameProfileProperty struct {
	Name      string
	Value     string
	Signature string
	// Signed 表示 Signature 有效（正版纹理带有 Mojang 签名）。
	Signed bool
}

// EncodeLoginSuccess 编码 Login Success 包（1.21.11，无属性）。
func EncodeLoginSuccess(uuid [16]byte, username string) []byte {
	return EncodeLoginSuccessWithProperties(uuid, username, nil)
}

// EncodeLoginSuccessWithProperties 编码 Login Success 包（1.21.11）。
// 结构：UUID（16 字节二进制）、用户名（String）、属性列表
// （每项：名称、值、可选签名）。
func EncodeLoginSuccessWithProperties(uuid [16]byte, username string, properties []GameProfileProperty) []byte {
	packet := AppendVarInt(nil, int32(LoginPacketIDSuccess))
	packet = append(packet, uuid[:]...)
	packet = appendString(packet, username)
	packet = AppendVarInt(packet, int32(len(properties)))
	for _, property := range properties {
		packet = appendString(packet, property.Name)
		packet = appendString(packet, property.Value)
		if property.Signed {
			packet = append(packet, 0x01)
			packet = appendString(packet, property.Signature)
		} else {
			packet = append(packet, 0x00)
		}
	}
	return packet
}

// EncodeLoginDisconnect 编码 Login Disconnect 包（登录阶段的踢出消息）。
func EncodeLoginDisconnect(reason string) []byte {
	packet := AppendVarInt(nil, int32(LoginPacketIDDisconnect))
	return AppendNBTString(packet, reason)
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
