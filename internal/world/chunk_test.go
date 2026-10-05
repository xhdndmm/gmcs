package world

import (
	"encoding/binary"
	"testing"

	"gmcs/internal/protocol"
)

func TestChunkBlockAccess(t *testing.T) {
	chunk := NewChunk(0, 0)
	if got := chunk.GetBlockState(0, WorldMinY, 0); got != AirBlock {
		t.Fatalf("expected air in new chunk, got %d", got)
	}
	chunk.SetBlockState(3, WorldMinY+5, 7, StoneBlock)
	if got := chunk.GetBlockState(3, WorldMinY+5, 7); got != StoneBlock {
		t.Fatalf("expected stone, got %d", got)
	}
	if got := chunk.GetBlockState(3, WorldMinY+5, 8); got != AirBlock {
		t.Fatalf("expected neighboring cell to stay air, got %d", got)
	}
	// 越界写入被忽略，越界读取返回空气。
	chunk.SetBlockState(16, WorldMinY, 0, StoneBlock)
	chunk.SetBlockState(0, WorldMinY-1, 0, StoneBlock)
	chunk.SetBlockState(0, WorldMinY+WorldHeight, 0, StoneBlock)
	if got := chunk.GetBlockState(16, WorldMinY, 0); got != AirBlock {
		t.Fatalf("expected out-of-range write to be ignored, got %d", got)
	}
	if got := chunk.GetBlockState(0, WorldMinY-1, 0); got != AirBlock {
		t.Fatalf("expected below-world read to be air, got %d", got)
	}
	// 跨 section：顶部的方块。
	topY := WorldMinY + WorldHeight - 1
	chunk.SetBlockState(0, topY, 15, GrassBlock)
	if got := chunk.GetBlockState(0, topY, 15); got != GrassBlock {
		t.Fatalf("expected grass at top section, got %d", got)
	}
	if index, ok := SectionIndex(topY); !ok || index != SectionCount-1 {
		t.Fatalf("SectionIndex(%d) = %d,%v", topY, index, ok)
	}
	if _, ok := SectionIndex(WorldMinY - 1); ok {
		t.Fatal("expected below-world SectionIndex to be invalid")
	}
}

func TestSectionBiome(t *testing.T) {
	chunk := NewChunk(0, 0)
	if got := chunk.SectionBiome(0); got != BiomePlains {
		t.Fatalf("expected default biome plains, got %d", got)
	}
	chunk.SetSectionBiome(2, 7)
	if got := chunk.SectionBiome(2); got != 7 {
		t.Fatalf("expected biome 7, got %d", got)
	}
	chunk.SetSectionBiome(SectionCount, 7) // 越界被忽略
	if got := chunk.SectionBiome(1); got != BiomePlains {
		t.Fatalf("expected untouched biome to stay plains, got %d", got)
	}
}

func TestFlatGenerator(t *testing.T) {
	chunk := FlatGenerator{}.GenerateChunk(2, -3)
	if chunk.X != 2 || chunk.Z != -3 {
		t.Fatalf("unexpected chunk position: %d,%d", chunk.X, chunk.Z)
	}
	if got := chunk.GetBlockState(0, WorldMinY, 0); got != BedrockBlock {
		t.Fatalf("expected bedrock at bottom, got %d", got)
	}
	if got := chunk.GetBlockState(5, WorldMinY+1, 9); got != DirtBlock {
		t.Fatalf("expected dirt layer, got %d", got)
	}
	if got := chunk.GetBlockState(15, FlatGroundLevel, 15); got != GrassBlock {
		t.Fatalf("expected grass surface, got %d", got)
	}
	if got := chunk.GetBlockState(0, FlatSpawnY, 0); got != AirBlock {
		t.Fatalf("expected air at spawn level, got %d", got)
	}
	if FlatGroundLevel != WorldMinY+3 || FlatSpawnY != FlatGroundLevel+1 {
		t.Fatalf("unexpected flat levels: ground=%d spawn=%d", FlatGroundLevel, FlatSpawnY)
	}
}

func TestEncodeChunkDataPacketStructure(t *testing.T) {
	chunk := NewChunk(1, -2)
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

	offset = skipHeightmaps(t, packet, offset)

	// 区块数据主体：24 个全空气 section（单值调色板）。
	dataLength, offset := decodeTestVarInt(t, packet, offset)
	if dataLength <= 0 || offset+int(dataLength) > len(packet) {
		t.Fatalf("invalid chunk data length %d", dataLength)
	}
	data := packet[offset : offset+int(dataLength)]
	offset += int(dataLength)
	for i := 0; i < SectionCount; i++ {
		data = checkEmptySection(t, i, data)
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

// checkEmptySection 校验一个全空气、单值调色板的 section（6 字节），返回剩余数据。
func checkEmptySection(t *testing.T, index int, data []byte) []byte {
	t.Helper()
	if len(data) < 6 {
		t.Fatalf("section %d: truncated section data", index)
	}
	blockCount := int16(binary.BigEndian.Uint16(data[0:2]))
	if blockCount != 0 {
		t.Fatalf("section %d: block count = %d, want 0", index, blockCount)
	}
	data = data[2:]
	if data[0] != 0x00 {
		t.Fatalf("section %d: expected single-valued block palette, got BPE %d", index, data[0])
	}
	state, size, err := protocol.DecodeVarInt(data[1:])
	if err != nil || state != int32(AirBlock) {
		t.Fatalf("section %d: block state %d (err=%v)", index, state, err)
	}
	data = data[1+size:]
	if data[0] != 0x00 {
		t.Fatalf("section %d: expected single-valued biome palette, got BPE %d", index, data[0])
	}
	biome, size, err := protocol.DecodeVarInt(data[1:])
	if err != nil || biome != int32(BiomePlains) {
		t.Fatalf("section %d: biome %d (err=%v)", index, biome, err)
	}
	return data[1+size:]
}

// TestEncodeChunkDataPacketPalette 校验超平坦地形的 4 种方块走 4 位间接调色板，
// 并解包校验每个方块位置。
func TestEncodeChunkDataPacketPalette(t *testing.T) {
	chunk := FlatGenerator{}.GenerateChunk(0, 0)
	packet := EncodeChunkDataPacket(chunk)
	data := chunkDataOf(t, packet)

	// section 0：基岩/泥土/草/空气 共 4 种状态。
	if len(data) < 4 {
		t.Fatal("section 0: truncated")
	}
	blockCount := int(binary.BigEndian.Uint16(data[0:2]))
	if want := SectionSize * SectionSize * 4; blockCount != want {
		t.Fatalf("section 0 block count = %d, want %d", blockCount, want)
	}
	if data[2] != 4 {
		t.Fatalf("section 0 bits per block = %d, want 4", data[2])
	}
	offset := 3
	paletteSize, size, err := protocol.DecodeVarInt(data[offset:])
	if err != nil || paletteSize != 4 {
		t.Fatalf("section 0 palette size = %d (err=%v)", paletteSize, err)
	}
	offset += size
	palette := make([]int32, paletteSize)
	for i := range palette {
		value, size, err := protocol.DecodeVarInt(data[offset:])
		if err != nil {
			t.Fatal(err)
		}
		palette[i] = value
		offset += size
	}
	// 调色板按首次出现顺序：y=-64 的基岩、泥土、草方块、空气。
	wantPalette := []int32{int32(BedrockBlock), int32(DirtBlock), int32(GrassBlock), int32(AirBlock)}
	for i, want := range wantPalette {
		if palette[i] != want {
			t.Fatalf("palette[%d] = %d, want %d", i, palette[i], want)
		}
	}
	// 数据数组无长度前缀：直接是 256 个 long。
	const longCount = SectionVolume * 4 / 64
	longs := make([]int64, longCount)
	for i := range longs {
		value, next, err := protocol.DecodeInt64(data, offset)
		if err != nil {
			t.Fatal(err)
		}
		longs[i] = value
		offset = next
	}
	values := unpackBits(t, longs, SectionVolume, 4)

	blockStateAt := func(x, y, z int) uint16 {
		return uint16(palette[values[blockIndex(x, y-WorldMinY, z)]])
	}
	for x := 0; x < SectionSize; x++ {
		for z := 0; z < SectionSize; z++ {
			if got := blockStateAt(x, WorldMinY, z); got != BedrockBlock {
				t.Fatalf("(%d,%d): expected bedrock, got %d", x, z, got)
			}
			if got := blockStateAt(x, WorldMinY+1, z); got != DirtBlock {
				t.Fatalf("(%d,%d): expected dirt, got %d", x, z, got)
			}
			if got := blockStateAt(x, FlatGroundLevel, z); got != GrassBlock {
				t.Fatalf("(%d,%d): expected grass, got %d", x, z, got)
			}
			if got := blockStateAt(x, FlatSpawnY, z); got != AirBlock {
				t.Fatalf("(%d,%d): expected air, got %d", x, z, got)
			}
		}
	}

	// 生物群系：单值调色板。
	if data[offset] != 0x00 {
		t.Fatalf("section 0 biome palette BPE = %d, want 0", data[offset])
	}
	biome, size, err := protocol.DecodeVarInt(data[offset+1:])
	if err != nil || biome != int32(BiomePlains) {
		t.Fatalf("section 0 biome = %d (err=%v)", biome, err)
	}
	offset += 1 + size

	// 其余 section 必须是空 section。
	rest := data[offset:]
	for i := 1; i < SectionCount; i++ {
		rest = checkEmptySection(t, i, rest)
	}
	if len(rest) != 0 {
		t.Fatalf("%d trailing bytes after sections", len(rest))
	}
}

// TestPackBitsRoundTrip 验证紧密位流打包与解包互为逆操作。
func TestPackBitsRoundTrip(t *testing.T) {
	for _, bits := range []int{4, 5, 8, 15} {
		values := make([]uint16, SectionVolume)
		mask := uint16(1)<<uint(bits) - 1
		for i := range values {
			values[i] = uint16(i*7) & mask
		}
		longs := packBits(values, bits)
		if want := (len(values)*bits + 63) / 64; len(longs) != want {
			t.Fatalf("bits=%d: %d longs, want %d", bits, len(longs), want)
		}
		got := unpackBits(t, longs, len(values), bits)
		for i := range values {
			if got[i] != values[i] {
				t.Fatalf("bits=%d index %d: got %d, want %d", bits, i, got[i], values[i])
			}
		}
	}
}

// TestPackIndirectMatchesPackBits 验证生产路径 packIndirect（线性查找调色板
// 且不生成中间索引数组）与参考实现 packBits 的打包结果完全一致。
func TestPackIndirectMatchesPackBits(t *testing.T) {
	for _, bits := range []int{4, 5, 6, 8} {
		paletteSize := 1 << uint(bits)
		palette := make([]uint16, paletteSize)
		for i := range palette {
			palette[i] = uint16(100 + i*13)
		}
		blocks := make([]uint16, SectionVolume)
		for i := range blocks {
			blocks[i] = palette[(i*17+i/7)%paletteSize]
		}
		values := make([]uint16, len(blocks))
		for i, state := range blocks {
			for j, candidate := range palette {
				if candidate == state {
					values[i] = uint16(j)
					break
				}
			}
		}
		want := packBits(values, bits)
		got := packIndirect(blocks, palette, bits)
		if len(got) != len(want) {
			t.Fatalf("bits=%d: %d longs, want %d", bits, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("bits=%d long %d: got %#x, want %#x", bits, i, got[i], want[i])
			}
		}
	}
}

// TestPackBitsPaddedRoundTrip 验证全局调色板的“每 long 独立”打包。
func TestPackBitsPaddedRoundTrip(t *testing.T) {
	const bits = 15
	values := make([]uint16, SectionVolume)
	for i := range values {
		values[i] = uint16(i * 3 & 0x7FFF)
	}
	longs := packBitsPadded(values, bits)
	perLong := 64 / bits
	if want := (len(values) + perLong - 1) / perLong; len(longs) != want {
		t.Fatalf("%d longs, want %d", len(longs), want)
	}
	for i, value := range values {
		index := i / perLong
		offset := uint(i%perLong) * uint(bits)
		got := uint16(uint64(longs[index])>>offset) & (1<<bits - 1)
		if got != value {
			t.Fatalf("index %d: got %d, want %d", i, got, value)
		}
	}
}

// chunkDataOf 跳过包头与 heightmaps，返回区块数据主体。
func chunkDataOf(t *testing.T, packet []byte) []byte {
	t.Helper()
	_, offset, err := protocol.DecodeVarInt(packet)
	if err != nil {
		t.Fatal(err)
	}
	if _, offset, err = protocol.DecodeInt32(packet, offset); err != nil {
		t.Fatal(err)
	}
	if _, offset, err = protocol.DecodeInt32(packet, offset); err != nil {
		t.Fatal(err)
	}
	offset = skipHeightmaps(t, packet, offset)
	length, offset := decodeTestVarInt(t, packet, offset)
	if length <= 0 || offset+int(length) > len(packet) {
		t.Fatalf("invalid chunk data length %d", length)
	}
	return packet[offset : offset+int(length)]
}

// unpackBits 以紧密位流解包（与 packBits 互为逆操作）。
func unpackBits(t *testing.T, longs []int64, count, bits int) []uint16 {
	t.Helper()
	values := make([]uint16, count)
	position := 0
	for i := range values {
		index := position >> 6
		shift := uint(position & 63)
		if index >= len(longs) {
			t.Fatalf("unpack overflow at index %d", i)
		}
		value := uint64(longs[index]) >> shift
		if shift+uint(bits) > 64 {
			if index+1 >= len(longs) {
				t.Fatalf("unpack cross-boundary overflow at index %d", i)
			}
			value |= uint64(longs[index+1]) << (64 - shift)
		}
		values[i] = uint16(value & (1<<uint(bits) - 1))
		position += bits
	}
	return values
}

func decodeTestVarInt(t *testing.T, data []byte, offset int) (int32, int) {
	t.Helper()
	value, size, err := protocol.DecodeVarInt(data[offset:])
	if err != nil {
		t.Fatalf("decode VarInt at %d: %v", offset, err)
	}
	return value, offset + size
}

// TestColumnCache 验证列高度查询结果正确，并随方块修改自动失效。
func TestColumnCache(t *testing.T) {
	chunk := FlatGenerator{}.GenerateChunk(0, 0)
	// 超平坦：基岩 + 两层泥土 + 草方块，最高固体为草方块。
	column := chunk.Column(1, 1)
	if !column.HasTop || column.TopState != GrassBlock || column.TopY != WorldMinY+3 {
		t.Fatalf("column = %+v", column)
	}
	if !column.HasSolid || column.SolidY != WorldMinY+3 {
		t.Fatalf("solid = %+v", column)
	}

	// 放置方块：缓存失效并反映新高度。
	chunk.SetBlockState(1, WorldMinY+5, 1, StoneBlock)
	column = chunk.Column(1, 1)
	if !column.HasTop || column.TopY != WorldMinY+5 || column.TopState != StoneBlock {
		t.Fatalf("after place: %+v", column)
	}
	if !column.HasSolid || column.SolidY != WorldMinY+5 {
		t.Fatalf("after place solid: %+v", column)
	}

	// 挖掉新方块：回到草方块。
	chunk.SetBlockState(1, WorldMinY+5, 1, AirBlock)
	column = chunk.Column(1, 1)
	if column.TopY != WorldMinY+3 || column.TopState != GrassBlock || column.SolidY != WorldMinY+3 {
		t.Fatalf("after dig: %+v", column)
	}
}

// TestColumnWaterSemantics 验证水对“最高方块”与“最高固体”的区分。
func TestColumnWaterSemantics(t *testing.T) {
	chunk := NewChunk(0, 0)
	chunk.SetBlockState(3, 64, 3, StoneBlock)
	chunk.SetBlockState(3, 65, 3, WaterBlock)
	chunk.SetBlockState(3, 66, 3, WaterBlock)
	column := chunk.Column(3, 3)
	if !column.HasTop || column.TopY != 66 || column.TopState != WaterBlock {
		t.Fatalf("top = %+v", column)
	}
	if !column.HasSolid || column.SolidY != 64 {
		t.Fatalf("solid = %+v", column)
	}

	// 无水无方块的列与越界坐标都返回空列。
	if empty := chunk.Column(4, 4); empty.HasTop || empty.HasSolid {
		t.Fatalf("empty = %+v", empty)
	}
	if out := chunk.Column(-1, 0); out.HasTop || out.HasSolid {
		t.Fatalf("out of range = %+v", out)
	}
}

// skipHeightmaps 解析并跳过 Chunk Data 包中的 heightmaps 数组，返回数据体偏移。
func skipHeightmaps(t *testing.T, packet []byte, offset int) int {
	t.Helper()
	count, next := decodeTestVarInt(t, packet, offset)
	offset = next
	var longCount int32
	for i := int32(0); i < count; i++ {
		_, next = decodeTestVarInt(t, packet, offset) // 类型（mapper）
		offset = next
		longCount, next = decodeTestVarInt(t, packet, offset)
		offset = next
		for j := int32(0); j < longCount; j++ {
			var err error
			if _, offset, err = protocol.DecodeInt64(packet, offset); err != nil {
				t.Fatal(err)
			}
		}
	}
	return offset
}

// heightmapEntry 是一个解析出的 heightmap。
type heightmapEntry struct {
	kind   int32
	longs  []int64
	values []uint16
}

// parseHeightmaps 解析 Chunk Data 包的 heightmaps 数组。
func parseHeightmaps(t *testing.T, packet []byte) []heightmapEntry {
	t.Helper()
	_, offset, err := protocol.DecodeVarInt(packet)
	if err != nil {
		t.Fatal(err)
	}
	if _, offset, err = protocol.DecodeInt32(packet, offset); err != nil {
		t.Fatal(err)
	}
	if _, offset, err = protocol.DecodeInt32(packet, offset); err != nil {
		t.Fatal(err)
	}
	count, offset := decodeTestVarInt(t, packet, offset)
	entries := make([]heightmapEntry, 0, count)
	var (
		kind int32
		next int
	)
	for i := int32(0); i < count; i++ {
		kind, next = decodeTestVarInt(t, packet, offset)
		offset = next
		var longCount int32
		longCount, next = decodeTestVarInt(t, packet, offset)
		offset = next
		longs := make([]int64, longCount)
		for j := range longs {
			if longs[j], offset, err = protocol.DecodeInt64(packet, offset); err != nil {
				t.Fatal(err)
			}
		}
		entries = append(entries, heightmapEntry{
			kind:   kind,
			longs:  longs,
			values: unpackPadded(t, longs, SectionSize*SectionSize, 9),
		})
	}
	return entries
}

// unpackPadded 以“每 long 独立、高位填充”的方式解包（packBitsPadded 的逆操作）。
func unpackPadded(t *testing.T, longs []int64, count, bits int) []uint16 {
	t.Helper()
	perLong := 64 / bits
	mask := uint64(1)<<uint(bits) - 1
	values := make([]uint16, count)
	for i := range values {
		values[i] = uint16((uint64(longs[i/perLong]) >> (uint(i%perLong) * uint(bits))) & mask)
	}
	return values
}

// TestChunkHeightmaps 验证 Chunk Data 包发送的 heightmaps：
// WORLD_SURFACE（1）与 MOTION_BLOCKING（4）、9 位、每 261 列 37 个 long、
// 逐列取值 = 最高非空气方块 y - WorldMinY + 1（空列为 0，水面计入）。
func TestChunkHeightmaps(t *testing.T) {
	check := func(name string, chunk *Chunk) {
		t.Helper()
		entries := parseHeightmaps(t, EncodeChunkDataPacket(chunk))
		if len(entries) != 2 {
			t.Fatalf("%s: heightmap count = %d, want 2", name, len(entries))
		}
		for index, wantKind := range []int32{1, 4} {
			entry := entries[index]
			if entry.kind != wantKind {
				t.Fatalf("%s: heightmap[%d] type = %d, want %d", name, index, entry.kind, wantKind)
			}
			if len(entry.longs) != 37 {
				t.Fatalf("%s: heightmap[%d] longs = %d, want 37", name, index, len(entry.longs))
			}
			for z := 0; z < SectionSize; z++ {
				for x := 0; x < SectionSize; x++ {
					want := uint16(0)
					if _, y, ok := chunk.TopBlock(x, z); ok {
						want = uint16(y - WorldMinY + 1)
					}
					if got := entry.values[(z<<4)|x]; got != want {
						t.Fatalf("%s: heightmap[%d] (%d,%d) = %d, want %d", name, index, x, z, got, want)
					}
				}
			}
		}
	}

	// 超平坦：基岩 + 两层泥土 + 草方块，所有列高度一致。
	check("flat", FlatGenerator{}.GenerateChunk(0, 0))
	// 种子地形：起伏高度与树木。
	check("seeded", SeededGenerator{Seed: 42}.GenerateChunk(0, 0))
	// 手工构造：水柱取水面高度，空列为 0。
	handmade := NewChunk(0, 0)
	handmade.SetBlockState(1, 60, 2, StoneBlock)
	handmade.SetBlockState(1, 61, 2, WaterBlock)
	check("handmade", handmade)
	values := parseHeightmaps(t, EncodeChunkDataPacket(handmade))[0].values
	if got, want := values[(2<<4)|1], uint16(61-WorldMinY+1); got != want {
		t.Fatalf("water column height = %d, want %d", got, want)
	}
	if got := values[(3<<4)|1]; got != 0 {
		t.Fatalf("empty column height = %d, want 0", got)
	}
}
