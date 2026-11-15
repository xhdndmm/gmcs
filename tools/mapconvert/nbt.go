//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package main

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

// NBT 标签类型；与 Minecraft NbtIo 一致。
const (
	tagEnd       byte = 0
	tagByte      byte = 1
	tagShort     byte = 2
	tagInt       byte = 3
	tagLong      byte = 4
	tagFloat     byte = 5
	tagDouble    byte = 6
	tagByteArray byte = 7
	tagString    byte = 8
	tagList      byte = 9
	tagCompound  byte = 10
	tagIntArray  byte = 11
	tagLongArray byte = 12
)

const (
	maxNBTDepth    = 512
	maxNBTElements = 1 << 24
)

type compound map[string]any

func decodeGzipOrRaw(raw []byte) (compound, error) {
	if len(raw) >= 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		reader, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("解压 NBT: %w", err)
		}
		payload, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil {
			return nil, err
		}
		raw = payload
	}
	return decodeNBT(raw)
}

func decodeNBT(raw []byte) (compound, error) {
	decoder := &nbtDecoder{data: raw}
	tag, err := decoder.byte()
	if err != nil {
		return nil, err
	}
	if tag != tagCompound {
		return nil, fmt.Errorf("NBT 根标签类型 %d，期望 compound", tag)
	}
	if _, err := decoder.string(); err != nil {
		return nil, err
	}
	return decoder.compound(0)
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

func (d *nbtDecoder) float32() (float32, error) {
	value, err := d.read(4)
	if err != nil {
		return 0, err
	}
	return math.Float32frombits(binary.BigEndian.Uint32(value)), nil
}

func (d *nbtDecoder) float64() (float64, error) {
	value, err := d.read(8)
	if err != nil {
		return 0, err
	}
	return math.Float64frombits(binary.BigEndian.Uint64(value)), nil
}

func (d *nbtDecoder) string() (string, error) {
	length, err := d.read(2)
	if err != nil {
		return "", err
	}
	value, err := d.read(int(binary.BigEndian.Uint16(length)))
	if err != nil {
		return "", err
	}
	return string(value), nil
}

func (d *nbtDecoder) compound(depth int) (compound, error) {
	if depth > maxNBTDepth {
		return nil, fmt.Errorf("NBT 嵌套过深")
	}
	result := compound{}
	for {
		tag, err := d.byte()
		if err != nil {
			return nil, err
		}
		if tag == tagEnd {
			return result, nil
		}
		name, err := d.string()
		if err != nil {
			return nil, err
		}
		value, err := d.value(tag, depth+1)
		if err != nil {
			return nil, fmt.Errorf("字段 %q: %w", name, err)
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
	if length < 0 || length > maxNBTElements {
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
	if depth > maxNBTDepth {
		return nil, fmt.Errorf("NBT 嵌套过深")
	}
	switch tag {
	case tagByte:
		value, err := d.byte()
		return int8(value), err
	case tagShort:
		value, err := d.read(2)
		if err != nil {
			return nil, err
		}
		return int16(binary.BigEndian.Uint16(value)), nil
	case tagInt:
		return d.int32()
	case tagLong:
		return d.int64()
	case tagFloat:
		return d.float32()
	case tagDouble:
		return d.float64()
	case tagByteArray:
		length, err := d.int32()
		if err != nil {
			return nil, err
		}
		if length < 0 || int(length) > d.remaining() {
			return nil, fmt.Errorf("byte array 长度非法：%d", length)
		}
		return d.read(int(length))
	case tagString:
		return d.string()
	case tagList:
		return d.list(depth)
	case tagCompound:
		return d.compound(depth)
	case tagIntArray:
		length, err := d.int32()
		if err != nil {
			return nil, err
		}
		if length < 0 || int(length) > d.remaining()/4 {
			return nil, fmt.Errorf("int array 长度非法：%d", length)
		}
		values := make([]int32, length)
		for i := range values {
			values[i], err = d.int32()
			if err != nil {
				return nil, err
			}
		}
		return values, nil
	case tagLongArray:
		length, err := d.int32()
		if err != nil {
			return nil, err
		}
		if length < 0 || int(length) > d.remaining()/8 {
			return nil, fmt.Errorf("long array 长度非法：%d", length)
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

// encodeNBT 写出未压缩的根 compound（空名称）。
func encodeNBT(root compound) ([]byte, error) {
	var buffer bytes.Buffer
	buffer.WriteByte(tagCompound)
	writeNBTString(&buffer, "")
	if err := writeCompound(&buffer, root, 0); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func writeCompound(buffer *bytes.Buffer, value compound, depth int) error {
	if depth > maxNBTDepth {
		return fmt.Errorf("NBT 嵌套过深")
	}
	for name, field := range value {
		tag, err := nbtTagOf(field)
		if err != nil {
			return fmt.Errorf("字段 %q: %w", name, err)
		}
		buffer.WriteByte(tag)
		writeNBTString(buffer, name)
		if err := writeNBTValue(buffer, field, depth+1); err != nil {
			return fmt.Errorf("字段 %q: %w", name, err)
		}
	}
	buffer.WriteByte(tagEnd)
	return nil
}

func nbtTagOf(value any) (byte, error) {
	switch value.(type) {
	case int8:
		return tagByte, nil
	case int16:
		return tagShort, nil
	case int, int32:
		return tagInt, nil
	case int64:
		return tagLong, nil
	case float32:
		return tagFloat, nil
	case float64:
		return tagDouble, nil
	case []byte:
		return tagByteArray, nil
	case string:
		return tagString, nil
	case []any:
		return tagList, nil
	case compound:
		return tagCompound, nil
	case []int32:
		return tagIntArray, nil
	case []int64:
		return tagLongArray, nil
	default:
		return 0, fmt.Errorf("无法编码 NBT 类型 %T", value)
	}
}

func writeNBTValue(buffer *bytes.Buffer, value any, depth int) error {
	switch field := value.(type) {
	case int8:
		buffer.WriteByte(byte(field))
	case int16:
		var raw [2]byte
		binary.BigEndian.PutUint16(raw[:], uint16(field))
		buffer.Write(raw[:])
	case int:
		var raw [4]byte
		binary.BigEndian.PutUint32(raw[:], uint32(int32(field)))
		buffer.Write(raw[:])
	case int32:
		var raw [4]byte
		binary.BigEndian.PutUint32(raw[:], uint32(field))
		buffer.Write(raw[:])
	case int64:
		var raw [8]byte
		binary.BigEndian.PutUint64(raw[:], uint64(field))
		buffer.Write(raw[:])
	case float32:
		var raw [4]byte
		binary.BigEndian.PutUint32(raw[:], math.Float32bits(field))
		buffer.Write(raw[:])
	case float64:
		var raw [8]byte
		binary.BigEndian.PutUint64(raw[:], math.Float64bits(field))
		buffer.Write(raw[:])
	case []byte:
		var raw [4]byte
		binary.BigEndian.PutUint32(raw[:], uint32(len(field)))
		buffer.Write(raw[:])
		buffer.Write(field)
	case string:
		writeNBTString(buffer, field)
	case []any:
		return writeNBTList(buffer, field, depth)
	case compound:
		return writeCompound(buffer, field, depth)
	case []int32:
		var raw [4]byte
		binary.BigEndian.PutUint32(raw[:], uint32(len(field)))
		buffer.Write(raw[:])
		for _, item := range field {
			binary.BigEndian.PutUint32(raw[:], uint32(item))
			buffer.Write(raw[:])
		}
	case []int64:
		var count [4]byte
		binary.BigEndian.PutUint32(count[:], uint32(len(field)))
		buffer.Write(count[:])
		var raw [8]byte
		for _, item := range field {
			binary.BigEndian.PutUint64(raw[:], uint64(item))
			buffer.Write(raw[:])
		}
	default:
		return fmt.Errorf("无法编码 NBT 类型 %T", value)
	}
	return nil
}

func writeNBTList(buffer *bytes.Buffer, list []any, depth int) error {
	if len(list) == 0 {
		buffer.WriteByte(tagEnd)
		var raw [4]byte
		buffer.Write(raw[:])
		return nil
	}
	tag, err := nbtTagOf(list[0])
	if err != nil {
		return err
	}
	buffer.WriteByte(tag)
	var raw [4]byte
	binary.BigEndian.PutUint32(raw[:], uint32(len(list)))
	buffer.Write(raw[:])
	for _, item := range list {
		itemTag, err := nbtTagOf(item)
		if err != nil {
			return err
		}
		if itemTag != tag {
			return fmt.Errorf("NBT 列表元素类型不一致：%d/%d", tag, itemTag)
		}
		if err := writeNBTValue(buffer, item, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func writeNBTString(buffer *bytes.Buffer, value string) {
	var raw [2]byte
	binary.BigEndian.PutUint16(raw[:], uint16(len(value)))
	buffer.Write(raw[:])
	buffer.WriteString(value)
}
