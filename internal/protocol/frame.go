package protocol

import (
	"bytes"
	"compress/zlib"
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
	return readFrame(reader)
}

func ReadPacketWithCompression(reader io.Reader, threshold int32) ([]byte, error) {
	if threshold < 0 {
		return nil, fmt.Errorf("compression threshold must not be negative")
	}
	frame, err := readFrame(reader)
	if err != nil {
		return nil, err
	}
	dataLength, offset, err := DecodeVarInt(frame)
	if err != nil {
		return nil, fmt.Errorf("read uncompressed packet length: %w", err)
	}
	if dataLength == 0 {
		packet := frame[offset:]
		if int64(len(packet)) >= int64(threshold) {
			return nil, fmt.Errorf("uncompressed packet length %d meets compression threshold %d", len(packet), threshold)
		}
		return packet, nil
	}
	if dataLength < 0 || dataLength > MaxPacketSize || dataLength < threshold {
		return nil, fmt.Errorf("invalid uncompressed packet length %d for threshold %d", dataLength, threshold)
	}
	decompressor, err := zlib.NewReader(bytes.NewReader(frame[offset:]))
	if err != nil {
		return nil, fmt.Errorf("create packet decompressor: %w", err)
	}
	packet, readErr := io.ReadAll(io.LimitReader(decompressor, MaxPacketSize+1))
	closeErr := decompressor.Close()
	if readErr != nil {
		return nil, fmt.Errorf("decompress packet: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close packet decompressor: %w", closeErr)
	}
	if len(packet) > MaxPacketSize || int32(len(packet)) != dataLength {
		return nil, fmt.Errorf("decompressed packet length %d does not match declared length %d", len(packet), dataLength)
	}
	return packet, nil
}

func readFrame(reader io.Reader) ([]byte, error) {
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
	return writeFrame(writer, packet)
}

func WritePacketWithCompression(writer io.Writer, packet []byte, threshold int32) error {
	if threshold < 0 {
		return fmt.Errorf("compression threshold must not be negative")
	}
	if len(packet) > MaxPacketSize {
		return fmt.Errorf("packet length %d exceeds the maximum %d", len(packet), MaxPacketSize)
	}
	if int64(len(packet)) < int64(threshold) {
		payload := AppendVarInt(nil, 0)
		payload = append(payload, packet...)
		return writeFrame(writer, payload)
	}
	var compressed bytes.Buffer
	compressor := zlib.NewWriter(&compressed)
	if _, err := compressor.Write(packet); err != nil {
		return fmt.Errorf("compress packet: %w", err)
	}
	if err := compressor.Close(); err != nil {
		return fmt.Errorf("close packet compressor: %w", err)
	}
	payload := AppendVarInt(nil, int32(len(packet)))
	payload = append(payload, compressed.Bytes()...)
	return writeFrame(writer, payload)
}

func writeFrame(writer io.Writer, packet []byte) error {
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
