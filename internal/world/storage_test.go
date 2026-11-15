//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package world

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// compressedPayload 返回区块的 zlib 压缩存储负载（SavePayloads 的输入格式）。
func compressedPayload(t testing.TB, chunk *Chunk) []byte {
	t.Helper()
	payload, err := zlibCompress(encodeChunkPayload(chunk))
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestChunkPayloadRoundTrip(t *testing.T) {
	chunk := FlatGenerator{}.GenerateChunk(-5, 7)
	chunk.SetBlockState(1, WorldMinY+100, 2, StoneBlock)
	var grid [16]uint16
	for i := range grid {
		grid[i] = BiomePlains
	}
	grid[biomeCellIndex(2, 1)] = 42
	chunk.SetBiomeGrid(grid)

	payload := encodeChunkPayload(chunk)
	decoded, err := decodeChunkPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.X != -5 || decoded.Z != 7 {
		t.Fatalf("decoded position %d,%d", decoded.X, decoded.Z)
	}
	if got := decoded.GetBlockState(0, WorldMinY, 0); got != BedrockBlock {
		t.Fatalf("bedrock lost: %d", got)
	}
	if got := decoded.GetBlockState(15, FlatGroundLevel, 15); got != GrassBlock {
		t.Fatalf("grass lost: %d", got)
	}
	if got := decoded.GetBlockState(1, WorldMinY+100, 2); got != StoneBlock {
		t.Fatalf("stone lost: %d", got)
	}
	if got := decoded.GetBlockState(1, WorldMinY+101, 2); got != AirBlock {
		t.Fatalf("unexpected block: %d", got)
	}
	if got := decoded.ColumnBiome(9, 5); got != 42 {
		t.Fatalf("biome lost: %d", got)
	}
	if got := decoded.ColumnBiome(0, 0); got != BiomePlains {
		t.Fatalf("unexpected biome: %d", got)
	}
}

// TestChunkPayloadRoundTripSeeded 逐方块校验测试地形区块的存储负载编解码
// （覆盖紧凑存储的展开与重建，包括 1/2/4/8 位调色板路径）。
func TestChunkPayloadRoundTripSeeded(t *testing.T) {
	chunk := FlatGenerator{}.GenerateChunk(-3, 4)
	decoded, err := decodeChunkPayload(encodeChunkPayload(chunk))
	if err != nil {
		t.Fatal(err)
	}
	for sectionIndex := 0; sectionIndex < SectionCount; sectionIndex++ {
		for x := 0; x < SectionSize; x++ {
			for z := 0; z < SectionSize; z++ {
				for localY := 0; localY < SectionSize; localY++ {
					y := WorldMinY + sectionIndex*SectionSize + localY
					want := chunk.GetBlockState(x, y, z)
					if got := decoded.GetBlockState(x, y, z); got != want {
						t.Fatalf("(%d,%d,%d)：got %d, want %d", x, y, z, got, want)
					}
				}
			}
		}
	}
}

func TestDecodeChunkPayloadRejectsCorrupt(t *testing.T) {
	valid := encodeChunkPayload(NewChunk(0, 0))

	badVersion := append([]byte{}, valid...)
	badVersion[5] = 99

	cases := map[string][]byte{
		"empty":       nil,
		"bad magic":   append([]byte("XXXX"), valid[4:]...),
		"truncated":   valid[:len(valid)-1],
		"trailing":    append(append([]byte{}, valid...), 0x00),
		"bad version": badVersion,
	}
	for name, payload := range cases {
		if _, err := decodeChunkPayload(payload); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestSaveAndLoadChunk(t *testing.T) {
	dir := t.TempDir()
	chunk := FlatGenerator{}.GenerateChunk(3, -9)
	payloads := map[ChunkPos][]byte{{X: 3, Z: -9}: compressedPayload(t, chunk)}
	if err := SavePayloads(dir, payloads); err != nil {
		t.Fatal(err)
	}
	// 3>>5=0、-9>>5=-1。
	if _, err := os.Stat(filepath.Join(dir, "r.0.-1.mca")); err != nil {
		t.Fatalf("region file missing: %v", err)
	}

	loaded, err := LoadChunk(dir, 3, -9)
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil {
		t.Fatal("expected chunk on disk")
	}
	if got := loaded.GetBlockState(0, FlatGroundLevel, 0); got != GrassBlock {
		t.Fatalf("grass lost: %d", got)
	}

	missing, err := LoadChunk(dir, 4, -9)
	if err != nil || missing != nil {
		t.Fatalf("expected missing chunk, got %v err=%v", missing, err)
	}
}

func TestRegionFileKeepsNeighborChunks(t *testing.T) {
	dir := t.TempDir()
	first := FlatGenerator{}.GenerateChunk(0, 0)
	if err := SavePayloads(dir, map[ChunkPos][]byte{{X: 0, Z: 0}: compressedPayload(t, first)}); err != nil {
		t.Fatal(err)
	}
	second := NewChunk(1, 1)
	second.SetBlockState(2, WorldMinY+200, 3, StoneBlock)
	if err := SavePayloads(dir, map[ChunkPos][]byte{{X: 1, Z: 1}: compressedPayload(t, second)}); err != nil {
		t.Fatal(err)
	}

	chunk, err := LoadChunk(dir, 0, 0)
	if err != nil || chunk == nil {
		t.Fatalf("first chunk lost: %v", err)
	}
	if got := chunk.GetBlockState(7, FlatGroundLevel, 7); got != GrassBlock {
		t.Fatalf("first chunk content lost: %d", got)
	}
	chunk2, err := LoadChunk(dir, 1, 1)
	if err != nil || chunk2 == nil {
		t.Fatalf("second chunk lost: %v", err)
	}
	if got := chunk2.GetBlockState(2, WorldMinY+200, 3); got != StoneBlock {
		t.Fatalf("second chunk content lost: %d", got)
	}
}

func TestWorldLifecycle(t *testing.T) {
	dir := t.TempDir()
	w, err := Open(dir, FlatGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	chunk, err := w.Chunk(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := chunk.GetBlockState(0, WorldMinY, 0); got != BedrockBlock {
		t.Fatalf("generated chunk missing terrain: %d", got)
	}
	if got := w.DirtyCount(); got != 1 {
		t.Fatalf("dirty = %d, want 1", got)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := w.DirtyCount(); got != 0 {
		t.Fatalf("dirty after flush = %d, want 0", got)
	}
	// 已保存的区块再次访问不应重新标脏。
	if _, err := w.Chunk(0, 0); err != nil {
		t.Fatal(err)
	}
	if got := w.DirtyCount(); got != 0 {
		t.Fatalf("dirty after reload = %d, want 0", got)
	}

	// 修改并关闭。
	chunk.SetBlockState(5, WorldMinY+50, 5, StoneBlock)
	w.MarkDirty(chunk)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Chunk(1, 1); err == nil {
		t.Fatal("expected error on closed world")
	}

	// 重新打开：应从磁盘加载修改后的区块。
	w2, err := Open(dir, FlatGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w2.Close() }()
	reloaded, err := w2.Chunk(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.GetBlockState(5, WorldMinY+50, 5); got != StoneBlock {
		t.Fatalf("saved modification lost: %d", got)
	}
}

func TestWorldConcurrentChunkAccess(t *testing.T) {
	dir := t.TempDir()
	w, err := Open(dir, FlatGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for j := 0; j < 16; j++ {
				if _, err := w.Chunk(seed%4, j%4); err != nil {
					t.Errorf("chunk: %v", err)
					return
				}
			}
			if err := w.Flush(); err != nil {
				t.Errorf("flush: %v", err)
			}
		}(i)
	}
	wg.Wait()
}

func TestZlibDecompressLimit(t *testing.T) {
	big := bytes.Repeat([]byte{0xAB}, 4096)
	compressed, err := zlibCompress(big)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zlibDecompress(compressed, 1024); err == nil {
		t.Fatal("expected limit error")
	}
	decoded, err := zlibDecompress(compressed, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, big) {
		t.Fatal("round trip mismatch")
	}
}

// TestCompactSectionRoundTrip 覆盖 v13 调色板/位流 section：单值、普通调色板
// 以及调色板超过 256 项时的直接 16 位存储。
func TestCompactSectionRoundTrip(t *testing.T) {
	chunk := NewChunk(2, -3)
	chunk.SetBlockState(0, WorldMinY, 0, StoneBlock) // 单值 section
	for i := 0; i < 300; i++ {
		x, z := i&15, (i>>4)&15
		y := WorldMinY + SectionSize + i/256
		chunk.SetBlockState(x, y, z, uint16(i+1))
	}
	decoded, err := decodeChunkPayload(encodeChunkPayload(chunk))
	if err != nil {
		t.Fatal(err)
	}
	for sectionIndex := 0; sectionIndex < SectionCount; sectionIndex++ {
		for x := 0; x < SectionSize; x++ {
			for z := 0; z < SectionSize; z++ {
				for localY := 0; localY < SectionSize; localY++ {
					y := WorldMinY + sectionIndex*SectionSize + localY
					want := chunk.GetBlockState(x, y, z)
					if got := decoded.GetBlockState(x, y, z); got != want {
						t.Fatalf("(%d,%d,%d)：got %d, want %d", x, y, z, got, want)
					}
				}
			}
		}
	}
}

func TestCompactSectionRejectsPaletteIndexOverflow(t *testing.T) {
	// bits=2、调色板只有 1 项，但位流第一个索引为 1：必须报错而不是越界 panic。
	payload := []byte{2, 1, 7}
	payload = append(payload, make([]byte, SectionVolume*2/8)...)
	payload[3] = 1
	if _, err := decodeCompactSection(payload, 0, NewChunk(0, 0), 0); err == nil {
		t.Fatal("expected palette index error")
	}
}

// TestDecodeLegacyV12Payload 确认 v13 改动没有破坏旧地图读取。
func TestDecodeLegacyV12Payload(t *testing.T) {
	chunk := FlatGenerator{}.GenerateChunk(4, -2)
	payload := legacyV12PayloadForTest(chunk)
	decoded, err := decodeChunkPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	for sectionIndex := 0; sectionIndex < SectionCount; sectionIndex++ {
		for x := 0; x < SectionSize; x++ {
			for z := 0; z < SectionSize; z++ {
				y := WorldMinY + sectionIndex*SectionSize
				want := chunk.GetBlockState(x, y, z)
				if got := decoded.GetBlockState(x, y, z); got != want {
					t.Fatalf("(%d,%d,%d)：got %d, want %d", x, y, z, got, want)
				}
			}
		}
	}
}

func legacyV12PayloadForTest(chunk *Chunk) []byte {
	chunk.mu.RLock()
	defer chunk.mu.RUnlock()
	size := 14 + SectionCount*3 + 32 + 5
	for _, s := range chunk.sections {
		if s != nil && !s.storage.allAir() {
			size += SectionVolume * 2
		}
	}
	payload := make([]byte, size)
	copy(payload, "GMCS")
	binary.BigEndian.PutUint16(payload[4:6], 12)
	binary.BigEndian.PutUint32(payload[6:10], uint32(int32(chunk.X)))
	binary.BigEndian.PutUint32(payload[10:14], uint32(int32(chunk.Z)))
	offset := 14
	for _, s := range chunk.sections {
		var flags byte
		if s != nil && !s.storage.allAir() {
			flags = 1
		}
		payload[offset] = flags
		binary.BigEndian.PutUint16(payload[offset+1:offset+3], chunk.biomes[biomeCellIndex(2, 2)])
		offset += 3
		if flags == 1 {
			for i := 0; i < SectionVolume; i++ {
				binary.BigEndian.PutUint16(payload[offset:offset+2], s.storage.at(i))
				offset += 2
			}
		}
	}
	for i := 0; i < 16; i++ {
		binary.BigEndian.PutUint16(payload[offset:offset+2], chunk.biomes[i])
		offset += 2
	}
	offset = writeBlockEntities(payload, offset, chunk)
	return payload[:offset]
}

func TestSavePayloadsManyRegionsConcurrent(t *testing.T) {
	dir := t.TempDir()
	payloads := make(map[ChunkPos][]byte)
	for i := 0; i < 8; i++ {
		chunk := FlatGenerator{}.GenerateChunk(i*32, i*32)
		payloads[ChunkPos{X: i * 32, Z: i * 32}] = compressedPayload(t, chunk)
	}
	if err := SavePayloads(dir, payloads); err != nil {
		t.Fatal(err)
	}
	for pos := range payloads {
		chunk, err := LoadChunk(dir, pos.X, pos.Z)
		if err != nil || chunk == nil {
			t.Fatalf("(%d,%d) 未保存成功: %v", pos.X, pos.Z, err)
		}
		if got := chunk.GetBlockState(0, FlatGroundLevel, 0); got != GrassBlock {
			t.Fatalf("(%d,%d) 方块丢失: %d", pos.X, pos.Z, got)
		}
	}
}

func TestRegionHandleReferenceCount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.0.0.mca")
	if err := os.WriteFile(path, make([]byte, headerSectors*sectorSize), 0o644); err != nil {
		t.Fatal(err)
	}
	entry, err := openRegion(path)
	if err != nil {
		t.Fatal(err)
	}
	dropRegion(path)
	if _, err := entry.file.Stat(); err != nil {
		t.Fatalf("有引用时文件被提前关闭: %v", err)
	}
	entry.release()
	entry.release() // 防御性重复归还不应把引用减成负数或 panic
	if _, err := entry.file.Stat(); err == nil {
		t.Fatal("最后一个引用归还后文件仍未关闭")
	}
}

func TestDecodeCompactSectionRejectsMalformed(t *testing.T) {
	cases := [][]byte{
		{},           // 缺少 bits
		{3},          // 非法位宽
		{1},          // 缺少调色板数量
		{2, 0},       // 调色板数量为 0
		{2, 1, 0x80}, // 调色板 uvarint 截断
		{16, 1, 2},   // 直接存储位流截断
		append([]byte{1, 2, 7, 8}, make([]byte, 64)...), // 位流只有 64 字节，应为 64×8 字节
	}
	for i, payload := range cases {
		if _, err := decodeCompactSection(payload, 0, NewChunk(0, 0), i); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
}

func TestRegionJournalRecovery(t *testing.T) {
	dir := t.TempDir()
	chunk := FlatGenerator{}.GenerateChunk(0, 0)
	if err := SavePayloads(dir, map[ChunkPos][]byte{{X: 0, Z: 0}: compressedPayload(t, chunk)}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "r.0.0.mca")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(original) <= regionHeaderSize || (len(original)-regionHeaderSize)%sectorSize != 0 {
		t.Fatalf("区域文件长度不符合测试前提: %d", len(original))
	}
	header := append([]byte(nil), original[:regionHeaderSize]...)
	write := regionWrite{offset: regionHeaderSize, data: append([]byte(nil), original[regionHeaderSize:]...)}
	if err := writeRegionJournal(path, header, int64(len(original)), []regionWrite{write}); err != nil {
		t.Fatal(err)
	}
	// 模拟日志已落盘但区域文件只写了一部分：头部与数据被破坏。
	if err := os.WriteFile(path, make([]byte, len(original)), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadChunk(dir, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil || loaded.GetBlockState(0, FlatGroundLevel, 0) != GrassBlock {
		t.Fatal("日志恢复后区块内容错误")
	}
	if _, err := os.Stat(regionJournalPath(path)); !os.IsNotExist(err) {
		t.Fatalf("恢复后日志仍存在: %v", err)
	}
}

func TestRegionIncrementalReusesFreedSectors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r.0.0.mca")
	small := func(x, z int) []byte {
		chunk := NewChunk(x, z)
		chunk.SetBlockState(1, WorldMinY+100, 1, StoneBlock)
		return compressedPayload(t, chunk)
	}
	if err := SavePayloads(dir, map[ChunkPos][]byte{
		{X: 0, Z: 0}: small(0, 0),
		{X: 1, Z: 0}: small(1, 0),
	}); err != nil {
		t.Fatal(err)
	}
	header := make([]byte, regionHeaderSize)
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.ReadAt(header, 0); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	_ = file.Close()
	oldEntry := binary.BigEndian.Uint32(header[0*4:])
	oldOffset := oldEntry >> 8

	// 更新槽位 0：旧扇区在提交前必须保持可达，因此新数据写到别处；
	// 提交后旧扇区成为空闲区。
	if err := SavePayloads(dir, map[ChunkPos][]byte{{X: 0, Z: 0}: small(0, 0)}); err != nil {
		t.Fatal(err)
	}
	// 新槽位应该优先复用刚释放的槽位 0 扇区，而不是无限追加。
	if err := SavePayloads(dir, map[ChunkPos][]byte{{X: 2, Z: 0}: small(2, 0)}); err != nil {
		t.Fatal(err)
	}
	header = make([]byte, regionHeaderSize)
	file, err = os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.ReadAt(header, 0); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	_ = file.Close()
	newEntry := binary.BigEndian.Uint32(header[2*4:])
	if got := newEntry >> 8; got != oldOffset {
		t.Fatalf("新槽位没有复用空闲扇区：old=%d new=%d", oldOffset, got)
	}
	for _, pos := range []ChunkPos{{X: 0, Z: 0}, {X: 1, Z: 0}, {X: 2, Z: 0}} {
		loaded, err := LoadChunk(dir, pos.X, pos.Z)
		if err != nil || loaded == nil {
			t.Fatalf("区块 (%d,%d) 丢失: %v", pos.X, pos.Z, err)
		}
		if loaded.GetBlockState(1, WorldMinY+100, 1) != StoneBlock {
			t.Fatalf("区块 (%d,%d) 内容错误", pos.X, pos.Z)
		}
	}
}

func TestRegionIncrementalTruncatesTrailingFreeSectors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r.0.0.mca")
	small := func(x, z int) []byte {
		chunk := NewChunk(x, z)
		chunk.SetBlockState(1, WorldMinY+100, 1, StoneBlock)
		return compressedPayload(t, chunk)
	}
	if err := SavePayloads(dir, map[ChunkPos][]byte{
		{X: 0, Z: 0}: small(0, 0),
		{X: 1, Z: 0}: small(1, 0),
	}); err != nil {
		t.Fatal(err)
	}
	// 更新槽位 0，使旧槽位 0 的扇区成为空洞；再人为在文件末尾追加空闲扇区。
	if err := SavePayloads(dir, map[ChunkPos][]byte{{X: 0, Z: 0}: small(0, 0)}); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(make([]byte, 2*sectorSize)); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	// 更新槽位 1：应复用前面的空洞，并在提交后 truncate 掉尾部空闲扇区。
	if err := SavePayloads(dir, map[ChunkPos][]byte{{X: 1, Z: 0}: small(1, 0)}); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() >= before.Size()+2*sectorSize {
		t.Fatalf("尾部空闲扇区未回收: before=%d after=%d", before.Size(), after.Size())
	}
	for _, pos := range []ChunkPos{{X: 0, Z: 0}, {X: 1, Z: 0}} {
		if loaded, err := LoadChunk(dir, pos.X, pos.Z); err != nil || loaded == nil {
			t.Fatalf("区块 (%d,%d) 丢失: %v", pos.X, pos.Z, err)
		}
	}
}
