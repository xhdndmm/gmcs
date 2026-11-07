//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

// NBT 标签类型（与原版 NbtIo 一致）。
const (
	nbtTagEnd       byte = 0
	nbtTagByte      byte = 1
	nbtTagShort     byte = 2
	nbtTagInt       byte = 3
	nbtTagLong      byte = 4
	nbtTagFloat     byte = 5
	nbtTagDouble    byte = 6
	nbtTagByteArray byte = 7
	nbtTagString    byte = 8
	nbtTagList      byte = 9
	nbtTagCompound  byte = 10
	nbtTagIntArray  byte = 11
	nbtTagLongArray byte = 12
)

const (
	nbtMaxDepth    = 512
	nbtMaxElements = 1 << 24
)

// nbtCompound 是 NBT 复合标签的通用表示。
type nbtCompound map[string]any

// decodeNBT 解码未压缩或 gzip 压缩的 NBT 根复合标签。
// 结构模板文件是 gzip 压缩的 NBT；这里同时接受裸 NBT 便于测试。
func decodeNBT(raw []byte) (nbtCompound, error) {
	if len(raw) >= 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		reader, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("解压 NBT 失败：%w", err)
		}
		defer reader.Close()
		payload, err := io.ReadAll(reader)
		if err != nil {
			return nil, fmt.Errorf("读取 NBT 失败：%w", err)
		}
		raw = payload
	}
	decoder := &nbtDecoder{data: raw}
	tag, err := decoder.byte()
	if err != nil {
		return nil, err
	}
	if tag != nbtTagCompound {
		return nil, fmt.Errorf("NBT 根标签类型 %d，期望 compound", tag)
	}
	if _, err := decoder.string(); err != nil {
		return nil, err
	}
	root, err := decoder.compound(0)
	if err != nil {
		return nil, err
	}
	return root, nil
}

type nbtDecoder struct {
	data   []byte
	offset int
}

func (d *nbtDecoder) remaining() int { return len(d.data) - d.offset }

func (d *nbtDecoder) read(n int) ([]byte, error) {
	if n < 0 || d.remaining() < n {
		return nil, fmt.Errorf("NBT 截断：需要 %d 字节，剩余 %d", n, d.remaining())
	}
	value := d.data[d.offset : d.offset+n]
	d.offset += n
	return value, nil
}

func (d *nbtDecoder) byte() (byte, error) {
	value, err := d.read(1)
	if err != nil {
		return 0, err
	}
	return value[0], nil
}

func (d *nbtDecoder) int32() (int32, error) {
	value, err := d.read(4)
	if err != nil {
		return 0, err
	}
	return int32(binary.BigEndian.Uint32(value)), nil
}

func (d *nbtDecoder) int64() (int64, error) {
	value, err := d.read(8)
	if err != nil {
		return 0, err
	}
	return int64(binary.BigEndian.Uint64(value)), nil
}

func (d *nbtDecoder) float64() (float64, error) {
	value, err := d.read(8)
	if err != nil {
		return 0, err
	}
	return math.Float64frombits(binary.BigEndian.Uint64(value)), nil
}

func (d *nbtDecoder) float32() (float32, error) {
	value, err := d.read(4)
	if err != nil {
		return 0, err
	}
	return math.Float32frombits(binary.BigEndian.Uint32(value)), nil
}

func (d *nbtDecoder) string() (string, error) {
	length, err := d.read(2)
	if err != nil {
		return "", err
	}
	size := int(binary.BigEndian.Uint16(length))
	value, err := d.read(size)
	if err != nil {
		return "", err
	}
	return string(value), nil
}

func (d *nbtDecoder) compound(depth int) (nbtCompound, error) {
	if depth > nbtMaxDepth {
		return nil, fmt.Errorf("NBT 嵌套过深")
	}
	result := nbtCompound{}
	for {
		tag, err := d.byte()
		if err != nil {
			return nil, err
		}
		if tag == nbtTagEnd {
			return result, nil
		}
		name, err := d.string()
		if err != nil {
			return nil, err
		}
		value, err := d.value(tag, depth+1)
		if err != nil {
			return nil, fmt.Errorf("字段 %q：%w", name, err)
		}
		result[name] = value
	}
}

func (d *nbtDecoder) list(depth int) ([]any, error) {
	tag, err := d.byte()
	if err != nil {
		return nil, err
	}
	length, err := d.int32()
	if err != nil {
		return nil, err
	}
	if length < 0 || length > nbtMaxElements {
		return nil, fmt.Errorf("NBT 列表长度非法：%d", length)
	}
	values := make([]any, 0, length)
	for i := int32(0); i < length; i++ {
		value, err := d.value(tag, depth+1)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func (d *nbtDecoder) value(tag byte, depth int) (any, error) {
	if depth > nbtMaxDepth {
		return nil, fmt.Errorf("NBT 嵌套过深")
	}
	switch tag {
	case nbtTagByte:
		value, err := d.byte()
		return int8(value), err
	case nbtTagShort:
		value, err := d.read(2)
		if err != nil {
			return nil, err
		}
		return int16(binary.BigEndian.Uint16(value)), nil
	case nbtTagInt:
		return d.int32()
	case nbtTagLong:
		return d.int64()
	case nbtTagFloat:
		return d.float32()
	case nbtTagDouble:
		return d.float64()
	case nbtTagByteArray:
		length, err := d.int32()
		if err != nil {
			return nil, err
		}
		if length < 0 || int(length) > d.remaining() {
			return nil, fmt.Errorf("NBT byte array 长度非法：%d", length)
		}
		return d.read(int(length))
	case nbtTagString:
		return d.string()
	case nbtTagList:
		return d.list(depth)
	case nbtTagCompound:
		return d.compound(depth)
	case nbtTagIntArray:
		length, err := d.int32()
		if err != nil {
			return nil, err
		}
		if length < 0 || int(length) > d.remaining()/4 {
			return nil, fmt.Errorf("NBT int array 长度非法：%d", length)
		}
		values := make([]int32, length)
		for i := range values {
			values[i], err = d.int32()
			if err != nil {
				return nil, err
			}
		}
		return values, nil
	case nbtTagLongArray:
		length, err := d.int32()
		if err != nil {
			return nil, err
		}
		if length < 0 || int(length) > d.remaining()/8 {
			return nil, fmt.Errorf("NBT long array 长度非法：%d", length)
		}
		values := make([]int64, length)
		for i := range values {
			values[i], err = d.int64()
			if err != nil {
				return nil, err
			}
		}
		return values, nil
	default:
		return nil, fmt.Errorf("未知 NBT 标签类型 %d", tag)
	}
}

// nbtInts 读取 int/long/byte array 为 int 列表。
func nbtInts(value any) ([]int, bool) {
	switch list := value.(type) {
	case []any:
		result := make([]int, 0, len(list))
		for _, item := range list {
			result = append(result, nbtIntValue(item))
		}
		return result, true
	case []int32:
		result := make([]int, len(list))
		for i, item := range list {
			result[i] = int(item)
		}
		return result, true
	case []int64:
		result := make([]int, len(list))
		for i, item := range list {
			result[i] = int(item)
		}
		return result, true
	}
	return nil, false
}

func nbtStringValue(value any) (string, bool) {
	text, ok := value.(string)
	return text, ok
}

func nbtCompoundValue(value any) (nbtCompound, bool) {
	compound, ok := value.(nbtCompound)
	return compound, ok
}
