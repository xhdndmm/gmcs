package world

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// 存储布局与 Minecraft 的区域文件一致：每个区域文件覆盖 32×32 个区块，
// 文件头为 1024 个位置表项（4 字节：3 字节偏移扇区 + 1 字节长度扇区）
// 与 1024 个时间戳（4 字节，Unix 秒），随后是 4KB 对齐的区块数据。
//
// 区块负载为 gmcs 自有格式（不是原版 NBT，不能与原版互读）：
//
//	magic "GMCS" (4B) | version u16 | x i32 | z i32 | 24 × section
//	section: flags u8（bit0 = 含方块数据）| biome u16 |（可选）4096 × u16 方块状态
//
// 所有多字节整数均为大端；负载使用 zlib 压缩（区域文件压缩类型 2）。
const (
	regionShift   = 5
	regionSize    = 1 << regionShift // 32 个区块
	sectorSize    = 4096
	headerSectors = 2
	chunkVersion  = 1
	// maxChunkPayload 是解压后区块负载的大小上限（防压缩炸弹）。
	maxChunkPayload = 1 << 20
)

// regionSlot 返回区块在区域文件中的槽位（0..1023）。
// 负坐标通过按位与自动取模到 [0, 32)。
func regionSlot(x, z int) int {
	return (x & (regionSize - 1)) + (z&(regionSize-1))*regionSize
}

// regionPath 返回区块所在区域文件的路径。
// 算术右移对负数即向下取整除法，负坐标区域文件名为 r.-1.-1.mca 形式。
func regionPath(dir string, x, z int) string {
	return filepath.Join(dir, fmt.Sprintf("r.%d.%d.mca", x>>regionShift, z>>regionShift))
}

// regionFile 是一个已加载的区域文件：位置表、时间戳表与未压缩的区块负载。
type regionFile struct {
	locations  [regionSize * regionSize]uint32
	timestamps [regionSize * regionSize]uint32
	payloads   map[int][]byte
}

// loadRegionFile 读取区域文件；文件不存在时返回空表。
func loadRegionFile(path string) (*regionFile, error) {
	file := &regionFile{payloads: make(map[int][]byte)}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return file, nil
		}
		return nil, err
	}
	if len(data) < headerSectors*sectorSize {
		return nil, fmt.Errorf("区域文件 %s 过短（%d 字节）", path, len(data))
	}

	const slotCount = regionSize * regionSize
	for slot := 0; slot < slotCount; slot++ {
		entry := binary.BigEndian.Uint32(data[slot*4:])
		file.locations[slot] = entry
		file.timestamps[slot] = binary.BigEndian.Uint32(data[slotCount*4+slot*4:])
		if entry == 0 {
			continue
		}
		offset := int64(entry>>8) * sectorSize
		compressedLength := int64(entry & 0xFF)
		if compressedLength == 0 || offset+4 > int64(len(data)) {
			return nil, fmt.Errorf("区域文件 %s 槽位 %d 的位置表项无效", path, slot)
		}
		length := int64(binary.BigEndian.Uint32(data[offset:]))
		if length < 1 || offset+4+length > int64(len(data)) {
			return nil, fmt.Errorf("区域文件 %s 槽位 %d 的负载长度 %d 越界", path, slot, length)
		}
		compressionType := data[offset+4]
		if compressionType != 2 {
			return nil, fmt.Errorf("区域文件 %s 槽位 %d 使用了不支持的压缩类型 %d", path, slot, compressionType)
		}
		payload, err := zlibDecompress(data[offset+5:offset+4+length], maxChunkPayload)
		if err != nil {
			return nil, fmt.Errorf("区域文件 %s 槽位 %d 解压失败：%w", path, slot, err)
		}
		file.payloads[slot] = payload
	}
	return file, nil
}

// save 把区域文件写回磁盘。
// 先写临时文件并 fsync，再重命名，避免崩溃造成部分写入。
func (r *regionFile) save(path string) error {
	var buf bytes.Buffer
	buf.Write(make([]byte, headerSectors*sectorSize)) // 头部占位，稍后回填

	nextSector := uint32(headerSectors)
	slots := make([]int, 0, len(r.payloads))
	for slot := range r.payloads {
		slots = append(slots, slot)
	}
	sort.Ints(slots)
	for _, slot := range slots {
		compressed, err := zlibCompress(r.payloads[slot])
		if err != nil {
			return err
		}
		// 负载 = 4 字节长度（含压缩类型字节）+ 1 字节压缩类型 + 压缩数据。
		total := 1 + len(compressed)
		sectorCount := (4 + total + sectorSize - 1) / sectorSize
		if sectorCount > 255 {
			return fmt.Errorf("区块负载过大（压缩后 %d 字节）", len(compressed))
		}
		r.locations[slot] = nextSector<<8 | uint32(sectorCount)

		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(total))
		buf.Write(length[:])
		buf.WriteByte(2) // zlib
		buf.Write(compressed)
		if pad := sectorCount*sectorSize - 4 - total; pad > 0 {
			buf.Write(make([]byte, pad))
		}
		nextSector += uint32(sectorCount)
	}

	out := buf.Bytes()
	const slotCount = regionSize * regionSize
	for slot := 0; slot < slotCount; slot++ {
		binary.BigEndian.PutUint32(out[slot*4:], r.locations[slot])
		binary.BigEndian.PutUint32(out[slotCount*4+slot*4:], r.timestamps[slot])
	}

	temp := path + ".tmp"
	handle, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := handle.Write(out); err != nil {
		_ = handle.Close()
		return err
	}
	if err := handle.Sync(); err != nil {
		_ = handle.Close()
		return err
	}
	if err := handle.Close(); err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return err
	}
	// 尽力同步目录项，保证重命名本身落盘；失败不影响正确性。
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

// LoadChunk 从世界目录读取区块；区块不存在时返回 (nil, nil)。
func LoadChunk(dir string, x, z int) (*Chunk, error) {
	file, err := loadRegionFile(regionPath(dir, x, z))
	if err != nil {
		return nil, err
	}
	payload, ok := file.payloads[regionSlot(x, z)]
	if !ok {
		return nil, nil
	}
	chunk, err := decodeChunkPayload(payload)
	if err != nil {
		return nil, fmt.Errorf("区块 (%d,%d)：%w", x, z, err)
	}
	if chunk.X != x || chunk.Z != z {
		return nil, fmt.Errorf("区块 (%d,%d) 的负载坐标不匹配（存有 %d,%d）", x, z, chunk.X, chunk.Z)
	}
	return chunk, nil
}

// SavePayloads 把已编码的区块负载写入磁盘：按区域文件分组，
// 每个文件只做一次读-改-写（原子替换）。
func SavePayloads(dir string, payloads map[ChunkPos][]byte) error {
	if len(payloads) == 0 {
		return nil
	}
	byRegion := make(map[string][]ChunkPos)
	for pos := range payloads {
		path := regionPath(dir, pos.X, pos.Z)
		byRegion[path] = append(byRegion[path], pos)
	}
	for path, positions := range byRegion {
		file, err := loadRegionFile(path)
		if err != nil {
			return err
		}
		now := uint32(time.Now().Unix())
		for _, pos := range positions {
			slot := regionSlot(pos.X, pos.Z)
			file.payloads[slot] = payloads[pos]
			file.timestamps[slot] = now
		}
		if err := file.save(path); err != nil {
			return err
		}
	}
	return nil
}

// encodeChunkPayload 把区块编码为存储负载。
func encodeChunkPayload(chunk *Chunk) []byte {
	payload := make([]byte, 0, 14+SectionCount*3+SectionVolume*2)
	payload = append(payload, "GMCS"...)
	payload = binary.BigEndian.AppendUint16(payload, chunkVersion)
	payload = binary.BigEndian.AppendUint32(payload, uint32(int32(chunk.X)))
	payload = binary.BigEndian.AppendUint32(payload, uint32(int32(chunk.Z)))
	for _, s := range chunk.sections {
		var flags byte
		biome := uint16(BiomePlains)
		if s != nil {
			biome = s.biome
			if s.blocks != nil {
				flags = 1
			}
		}
		payload = append(payload, flags)
		payload = binary.BigEndian.AppendUint16(payload, biome)
		if flags&1 != 0 {
			for _, state := range s.blocks {
				payload = binary.BigEndian.AppendUint16(payload, state)
			}
		}
	}
	return payload
}

// decodeChunkPayload 解析存储负载。
func decodeChunkPayload(payload []byte) (*Chunk, error) {
	if len(payload) < 14 || string(payload[:4]) != "GMCS" {
		return nil, fmt.Errorf("区块负载头无效")
	}
	if version := binary.BigEndian.Uint16(payload[4:6]); version != chunkVersion {
		return nil, fmt.Errorf("不支持的区块负载版本 %d", version)
	}
	chunkX := int(int32(binary.BigEndian.Uint32(payload[6:10])))
	chunkZ := int(int32(binary.BigEndian.Uint32(payload[10:14])))
	chunk := NewChunk(chunkX, chunkZ)

	offset := 14
	for index := range chunk.sections {
		if offset+3 > len(payload) {
			return nil, fmt.Errorf("区块负载截断于 section %d", index)
		}
		flags := payload[offset]
		biome := binary.BigEndian.Uint16(payload[offset+1 : offset+3])
		offset += 3
		if flags&1 == 0 {
			if biome != BiomePlains {
				chunk.sections[index] = &section{biome: biome}
			}
			continue
		}
		need := SectionVolume * 2
		if offset+need > len(payload) {
			return nil, fmt.Errorf("区块负载截断于 section %d 的方块数据", index)
		}
		blocks := make([]uint16, SectionVolume)
		for i := range blocks {
			blocks[i] = binary.BigEndian.Uint16(payload[offset+2*i : offset+2*i+2])
		}
		offset += need
		chunk.sections[index] = &section{blocks: blocks, biome: biome}
	}
	if offset != len(payload) {
		return nil, fmt.Errorf("区块负载尾部有 %d 字节多余数据", len(payload)-offset)
	}
	return chunk, nil
}

// zlibCompress 压缩数据。
func zlibCompress(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	writer := zlib.NewWriter(&buf)
	if _, err := writer.Write(data); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// zlibDecompress 解压数据；limit 限制解压后的大小（防压缩炸弹）。
func zlibDecompress(data []byte, limit int) ([]byte, error) {
	reader, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	decoded, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(decoded) > limit {
		return nil, fmt.Errorf("解压后的区块负载超过大小上限 %d", limit)
	}
	return decoded, nil
}
