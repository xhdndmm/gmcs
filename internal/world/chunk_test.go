package world

import (
	"encoding/binary"
	"testing"

	"gmcs/internal/protocol"
)

func TestChunkSectionAccess(t *testing.T) {
	chunk := NewChunk(0, 0)
	if got := chunk.SectionBlock(0); got != AirBlock {
		t.Fatalf("expected air in empty section, got %d", got)
	}
	chunk.SetSection(0, StoneBlock)
	if got := chunk.SectionBlock(0); got != StoneBlock {
		t.Fatalf("expected stone, got %d", got)
	}
	chunk.SetSection(SectionCount, StoneBlock)
	if got := chunk.SectionBlock(SectionCount); got != AirBlock {
		t.Fatalf("expected out-of-range section to stay air, got %d", got)
	}
	if got := chunk.SectionBlock(-1); got != AirBlock {
		t.Fatalf("expected negative section index to be air, got %d", got)
	}
}

func TestNewFlatChunk(t *testing.T) {
	chunk := NewFlatChunk(2, -3)
	if chunk.X != 2 || chunk.Z != -3 {
		t.Fatalf("unexpected chunk position: %d,%d", chunk.X, chunk.Z)
	}
	if got := chunk.SectionBlock(0); got != StoneBlock {
		t.Fatalf("expected stone platform at section 0, got %d", got)
	}
	if got := chunk.SectionBlock(1); got != AirBlock {
		t.Fatalf("expected air above platform, got %d", got)
	}
	if SectionIndex(PlatformTopY-1) != 0 {
		t.Fatalf("expected platform top to be inside section 0")
	}
}

func TestEncodeChunkDataPacketStructure(t *testing.T) {
	chunk := NewFlatChunk(1, -2)
	packet := EncodeChunkDataPacket(chunk)

	packetID, offset, err := protocol.DecodeVarInt(packet)
	if err != nil || packetID != protocol.PlayPacketIDChunkData {
		t.Fatalf("unexpected packet ID %#x (err=%v)", packetID, err)
	}
	x, offset, err := protocol.DecodeInt32(packet, offset)
	if err != nil || x != 1 {
		t.Fatalf("unexpected chunk X %d (err=%v)", x, err)
	}
	z, offset, err := protocol.DecodeInt32(packet, offset)
	if err != nil || z != -2 {
		t.Fatalf("unexpected chunk Z %d (err=%v)", z, err)
	}

	// heightmaps 为空数组。
	heightmaps, offset := decodeTestVarInt(t, packet, offset)
	if heightmaps != 0 {
		t.Fatalf("expected empty heightmaps, got %d", heightmaps)
	}

	// 区块数据主体：24 个 section，最底部为石头、其余为空气。
	dataLength, offset := decodeTestVarInt(t, packet, offset)
	if dataLength <= 0 || offset+int(dataLength) > len(packet) {
		t.Fatalf("invalid chunk data length %d", dataLength)
	}
	data := packet[offset : offset+int(dataLength)]
	offset += int(dataLength)
	for i := 0; i < SectionCount; i++ {
		if len(data) < 6 {
			t.Fatalf("section %d: truncated section data", i)
		}
		blockCount := int16(binary.BigEndian.Uint16(data[0:2]))
		fluidCount := int16(binary.BigEndian.Uint16(data[2:4]))
		data = data[4:]
		if data[0] != 0x00 {
			t.Fatalf("section %d: expected single-valued block palette, got BPE %d", i, data[0])
		}
		data = data[1:]
		blockState, size, err := protocol.DecodeVarInt(data)
		if err != nil {
			t.Fatalf("section %d: decode block state: %v", i, err)
		}
		data = data[size:]
		if data[0] != 0x00 {
			t.Fatalf("section %d: expected single-valued biome palette, got BPE %d", i, data[0])
		}
		data = data[1:]
		biome, size, err := protocol.DecodeVarInt(data)
		if err != nil {
			t.Fatalf("section %d: decode biome: %v", i, err)
		}
		data = data[size:]

		wantBlock, wantCount := int32(AirBlock), int16(0)
		if i == 0 {
			wantBlock, wantCount = StoneBlock, 4096
		}
		if blockState != wantBlock || blockCount != wantCount || fluidCount != 0 {
			t.Fatalf("section %d: got block %d count %d fluid %d, want block %d count %d",
				i, blockState, blockCount, fluidCount, wantBlock, wantCount)
		}
		if biome != BiomePlains {
			t.Fatalf("section %d: unexpected biome %d", i, biome)
		}
	}
	if len(data) != 0 {
		t.Fatalf("%d trailing bytes in chunk data", len(data))
	}

	// 方块实体为空。
	blockEntities, offset := decodeTestVarInt(t, packet, offset)
	if blockEntities != 0 {
		t.Fatalf("expected no block entities, got %d", blockEntities)
	}

	// 光照：天空光掩码为全部 SectionCount+2 位。
	maskLongs, offset := decodeTestVarInt(t, packet, offset)
	if maskLongs != 1 {
		t.Fatalf("expected one sky light mask long, got %d", maskLongs)
	}
	mask, offset, err := protocol.DecodeInt64(packet, offset)
	if err != nil || mask != int64(1)<<(SectionCount+2)-1 {
		t.Fatalf("unexpected sky light mask %#x (err=%v)", mask, err)
	}
	for _, name := range []string{"block light mask", "empty sky light mask", "empty block light mask"} {
		count, next := decodeTestVarInt(t, packet, offset)
		if count != 0 {
			t.Fatalf("expected empty %s, got %d", name, count)
		}
		offset = next
	}
	skyLightArrays, offset := decodeTestVarInt(t, packet, offset)
	if skyLightArrays != SectionCount+2 {
		t.Fatalf("expected %d sky light arrays, got %d", SectionCount+2, skyLightArrays)
	}
	for i := 0; i < int(skyLightArrays); i++ {
		length, next := decodeTestVarInt(t, packet, offset)
		if length != lightValuesPerSection || next+int(length) > len(packet) {
			t.Fatalf("sky light array %d has invalid length %d", i, length)
		}
		offset = next
		for _, value := range packet[offset : offset+int(length)] {
			if value != 0xFF {
				t.Fatal("expected full-bright sky light values")
			}
		}
		offset += int(length)
	}
	blockLightArrays, offset := decodeTestVarInt(t, packet, offset)
	if blockLightArrays != 0 {
		t.Fatalf("expected no block light arrays, got %d", blockLightArrays)
	}
	if offset != len(packet) {
		t.Fatalf("%d trailing bytes in chunk packet", len(packet)-offset)
	}
}

func decodeTestVarInt(t *testing.T, data []byte, offset int) (int32, int) {
	t.Helper()
	value, size, err := protocol.DecodeVarInt(data[offset:])
	if err != nil {
		t.Fatalf("decode VarInt at %d: %v", offset, err)
	}
	return value, offset + size
}
