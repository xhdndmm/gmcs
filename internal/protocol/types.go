package protocol

import (
	"encoding/binary"
	"fmt"
	"math"
)

// 本文件提供网络协议中固定长度标量的大端编解码辅助函数。

func AppendInt32(dst []byte, value int32) []byte {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], uint32(value))
	return append(dst, buf[:]...)
}

func AppendInt64(dst []byte, value int64) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(value))
	return append(dst, buf[:]...)
}

func AppendBool(dst []byte, value bool) []byte {
	if value {
		return append(dst, 0x01)
	}
	return append(dst, 0x00)
}

func AppendFloat32(dst []byte, value float32) []byte {
	return AppendInt32(dst, int32(math.Float32bits(value)))
}

func AppendFloat64(dst []byte, value float64) []byte {
	return AppendInt64(dst, int64(math.Float64bits(value)))
}

func DecodeInt32(data []byte, offset int) (int32, int, error) {
	if len(data)-offset < 4 {
		return 0, offset, fmt.Errorf("expected 4 bytes for int32")
	}
	return int32(binary.BigEndian.Uint32(data[offset:])), offset + 4, nil
}

func DecodeInt64(data []byte, offset int) (int64, int, error) {
	if len(data)-offset < 8 {
		return 0, offset, fmt.Errorf("expected 8 bytes for int64")
	}
	return int64(binary.BigEndian.Uint64(data[offset:])), offset + 8, nil
}

func DecodeBool(data []byte, offset int) (bool, int, error) {
	if len(data) <= offset {
		return false, offset, fmt.Errorf("expected 1 byte for bool")
	}
	return data[offset] != 0, offset + 1, nil
}
