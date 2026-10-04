package protocol

import (
	"errors"
	"fmt"
	"io"
)

const MaxPacketSize = 2 << 20

var ErrMalformedVarInt = errors.New("malformed VarInt")

func ReadVarInt(reader io.ByteReader) (int32, error) {
	var value uint32
	for index := 0; index < 5; index++ {
		current, err := reader.ReadByte()
		if err != nil {
			return 0, err
		}
		if index == 4 && current&0xf0 != 0 {
			return 0, ErrMalformedVarInt
		}
		value |= uint32(current&0x7f) << (7 * index)
		if current&0x80 == 0 {
			return int32(value), nil
		}
	}
	return 0, ErrMalformedVarInt
}

func AppendVarInt(dst []byte, value int32) []byte {
	encoded := uint32(value)
	for encoded&^uint32(0x7f) != 0 {
		dst = append(dst, byte(encoded&0x7f)|0x80)
		encoded >>= 7
	}
	return append(dst, byte(encoded))
}

func DecodeVarInt(data []byte) (int32, int, error) {
	var value uint32
	for index := 0; index < 5; index++ {
		if index >= len(data) {
			return 0, 0, ErrMalformedVarInt
		}
		current := data[index]
		if index == 4 && current&0xf0 != 0 {
			return 0, 0, ErrMalformedVarInt
		}
		value |= uint32(current&0x7f) << (7 * index)
		if current&0x80 == 0 {
			return int32(value), index + 1, nil
		}
	}
	return 0, 0, ErrMalformedVarInt
}

func ReadPacket(reader io.Reader) ([]byte, error) {
	buffered, ok := reader.(io.ByteReader)
	if !ok {
		buffered = newByteReader(reader)
	}
	length, err := ReadVarInt(buffered)
	if err != nil {
		return nil, fmt.Errorf("read packet length: %w", err)
	}
	if length < 0 || length > MaxPacketSize {
		return nil, fmt.Errorf("packet length %d is outside the allowed range", length)
	}
	packet := make([]byte, int(length))
	if _, err := io.ReadFull(reader, packet); err != nil {
		return nil, fmt.Errorf("read packet body: %w", err)
	}
	return packet, nil
}

func WritePacket(writer io.Writer, packet []byte) error {
	if len(packet) > MaxPacketSize {
		return fmt.Errorf("packet length %d exceeds the maximum %d", len(packet), MaxPacketSize)
	}
	frame := AppendVarInt(make([]byte, 0, 5+len(packet)), int32(len(packet)))
	frame = append(frame, packet...)
	for len(frame) > 0 {
		written, err := writer.Write(frame)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		frame = frame[written:]
	}
	return nil
}

type byteReader struct {
	reader io.Reader
	buffer [1]byte
}

func newByteReader(reader io.Reader) *byteReader {
	return &byteReader{reader: reader}
}

func (r *byteReader) ReadByte() (byte, error) {
	_, err := io.ReadFull(r.reader, r.buffer[:])
	return r.buffer[0], err
}
