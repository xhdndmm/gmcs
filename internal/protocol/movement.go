package protocol

import (
	"fmt"
	"math"
)

// ParsePlayerPosition 解析 Player Position（0x1D）包的坐标与着地标志。
// 着地标志来自末尾 flags 字节的 bit 0x01，用于跟踪下落与摔落伤害。
func ParsePlayerPosition(packet []byte) (x, y, z float64, onGround bool, err error) {
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != PlayServerboundPacketIDPlayerPosition {
		return 0, 0, 0, false, fmt.Errorf("invalid player position packet")
	}
	if x, offset, err = DecodeFloat64(packet, offset); err != nil {
		return 0, 0, 0, false, err
	}
	if y, offset, err = DecodeFloat64(packet, offset); err != nil {
		return 0, 0, 0, false, err
	}
	if z, offset, err = DecodeFloat64(packet, offset); err != nil {
		return 0, 0, 0, false, err
	}
	if offset >= len(packet) {
		return 0, 0, 0, false, fmt.Errorf("truncated player position packet")
	}
	flags := packet[offset]
	return x, y, z, flags&0x01 != 0, validFinite(x, y, z)
}

// ParsePlayerPositionRotation 解析 Player Position and Rotation（0x1E）包。
func ParsePlayerPositionRotation(packet []byte) (x, y, z float64, yaw, pitch float32, onGround bool, err error) {
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != PlayServerboundPacketIDPlayerPositionRotation {
		return 0, 0, 0, 0, 0, false, fmt.Errorf("invalid player position rotation packet")
	}
	if x, offset, err = DecodeFloat64(packet, offset); err != nil {
		return 0, 0, 0, 0, 0, false, err
	}
	if y, offset, err = DecodeFloat64(packet, offset); err != nil {
		return 0, 0, 0, 0, 0, false, err
	}
	if z, offset, err = DecodeFloat64(packet, offset); err != nil {
		return 0, 0, 0, 0, 0, false, err
	}
	if yaw, offset, err = DecodeFloat32(packet, offset); err != nil {
		return 0, 0, 0, 0, 0, false, err
	}
	if pitch, offset, err = DecodeFloat32(packet, offset); err != nil {
		return 0, 0, 0, 0, 0, false, err
	}
	if offset >= len(packet) {
		return 0, 0, 0, 0, 0, false, fmt.Errorf("truncated player position rotation packet")
	}
	flags := packet[offset]
	if err := validFinite(x, y, z); err != nil {
		return 0, 0, 0, 0, 0, false, err
	}
	return x, y, z, yaw, pitch, flags&0x01 != 0, nil
}

// ParsePlayerRotation 解析 Player Rotation（0x1F）包。
func ParsePlayerRotation(packet []byte) (yaw, pitch float32, err error) {
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != PlayServerboundPacketIDPlayerRotation {
		return 0, 0, fmt.Errorf("invalid player rotation packet")
	}
	if yaw, offset, err = DecodeFloat32(packet, offset); err != nil {
		return 0, 0, err
	}
	if pitch, _, err = DecodeFloat32(packet, offset); err != nil {
		return 0, 0, err
	}
	return yaw, pitch, nil
}

// validFinite 拒绝 NaN/Inf 坐标。
func validFinite(values ...float64) error {
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("non-finite coordinate")
		}
	}
	return nil
}
