package protocol

import (
	"fmt"
	"math"
)

// ParsePlayerPosition 解析 Player Position（0x1D）包的坐标。
func ParsePlayerPosition(packet []byte) (x, y, z float64, err error) {
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != PlayServerboundPacketIDPlayerPosition {
		return 0, 0, 0, fmt.Errorf("invalid player position packet")
	}
	if x, offset, err = DecodeFloat64(packet, offset); err != nil {
		return 0, 0, 0, err
	}
	if y, offset, err = DecodeFloat64(packet, offset); err != nil {
		return 0, 0, 0, err
	}
	if z, _, err = DecodeFloat64(packet, offset); err != nil {
		return 0, 0, 0, err
	}
	return x, y, z, validFinite(x, y, z)
}

// ParsePlayerPositionRotation 解析 Player Position and Rotation（0x1E）包。
func ParsePlayerPositionRotation(packet []byte) (x, y, z float64, yaw, pitch float32, err error) {
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != PlayServerboundPacketIDPlayerPositionRotation {
		return 0, 0, 0, 0, 0, fmt.Errorf("invalid player position rotation packet")
	}
	if x, offset, err = DecodeFloat64(packet, offset); err != nil {
		return 0, 0, 0, 0, 0, err
	}
	if y, offset, err = DecodeFloat64(packet, offset); err != nil {
		return 0, 0, 0, 0, 0, err
	}
	if z, offset, err = DecodeFloat64(packet, offset); err != nil {
		return 0, 0, 0, 0, 0, err
	}
	if yaw, offset, err = DecodeFloat32(packet, offset); err != nil {
		return 0, 0, 0, 0, 0, err
	}
	if pitch, _, err = DecodeFloat32(packet, offset); err != nil {
		return 0, 0, 0, 0, 0, err
	}
	if err := validFinite(x, y, z); err != nil {
		return 0, 0, 0, 0, 0, err
	}
	return x, y, z, yaw, pitch, nil
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
