package world

import (
	"bytes"
	"compress/zlib"
	"container/list"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// regionFileCache 是打开的区域文件句柄缓存（磁盘缓存）：读路径反复
// open/close 同一区域文件的系统调用开销可省；句柄按 LRU 有界。
//
// 句柄归还/淘汰时不立即 Close：可能有读者正持引用（ReadAt 并发安全），
// 交由 GC 终结器回收，避免关闭正在使用的句柄。写入（重命名替换）后
// 调用 dropRegion 丢弃缓存项，后续读取会重新打开新文件。
type regionFileCache struct {
	mu      sync.Mutex
	entries map[string]*list.Element
	order   *list.List // 前端最新
}

type regionFileEntry struct {
	path string
	file *os.File
}

// regionCacheMax 是缓存的区域文件句柄上限（每个句柄占一个 fd）。
const regionCacheMax = 32

var regionCache = &regionFileCache{
	entries: make(map[string]*list.Element),
	order:   list.New(),
}

// openRegion 返回区域文件句柄（缓存复用）；调用方不得 Close。
func openRegion(path string) (*os.File, error) {
	regionCache.mu.Lock()
	defer regionCache.mu.Unlock()
	if elem, ok := regionCache.entries[path]; ok {
		regionCache.order.MoveToFront(elem)
		return elem.Value.(*regionFileEntry).file, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	entry := &regionFileEntry{path: path, file: file}
	regionCache.entries[path] = regionCache.order.PushFront(entry)
	if regionCache.order.Len() > regionCacheMax {
		oldest := regionCache.order.Back()
		entry = oldest.Value.(*regionFileEntry)
		regionCache.order.Remove(oldest)
		delete(regionCache.entries, entry.path)
		// 不立即 Close（可能有读者持有）；GC 终结器会回收。
	}
	return file, nil
}

// dropRegion 丢弃缓存的句柄（写入重命名后调用）。
func dropRegion(path string) {
	regionCache.mu.Lock()
	defer regionCache.mu.Unlock()
	if elem, ok := regionCache.entries[path]; ok {
		regionCache.order.Remove(elem)
		delete(regionCache.entries, path)
	}
}

// 存储布局与 Minecraft 的区域文件一致：每个区域文件覆盖 32×32 个区块，
// 文件头为 1024 个位置表项（4 字节：3 字节偏移扇区 + 1 字节长度扇区）
// 与 1024 个时间戳（4 字节，Unix 秒），随后是 4KB 对齐的区块数据。
//
// 区块负载为 gmcs 自有格式（不是原版 NBT，不能与原版互读）：
//
//		magic "GMCS" (4B) | version u16 | x i32 | z i32 | 24 × section
//		section: flags u8（bit0 = 含方块数据）| biome u16 |（可选）4096 × u16 方块状态
//		(version 4+) 16 × u16 群系网格（4×4 列分辨率，索引见 biomeCellIndex）
//		(version 2+) blockEntityCount varint | 每项: packedXZ u8 | y i16 | type varint//     | slotCount varint | slotCount × (itemID varint, count varint)
//	  (version 3+) 熔炉类方块实体追加: burnRemaining varint | burnTotal varint
//	    | cookProgress varint | cookTotal varint//	                                          | itemCount varint | (itemID varint | count varint)*
//
// version 1 的负载不含方块实体（读取时视为空），version 2 起追加。
// 所有多字节整数均为大端；负载使用 zlib 压缩（区域文件压缩类型 2）。
const (
	regionShift   = 5
	regionSize    = 1 << regionShift // 32 个区块
	sectorSize    = 4096
	headerSectors = 2
	// chunkVersion 是当前存储负载版本：v2 追加方块实体，v3 追加熔炉进度，
	// v4 追加 4×4 群系网格（section 内的 biome 字段仅作兼容保留）。
	chunkVersion = 4
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

// loadChunkPayload 读取单个区块的负载（已解压）；区块不存在时返回 (nil, nil)。
// 只读取目标槽位，不加载同一区域文件的其余区块——避免加载单个区块时
// 对大区域文件产生“整区解压”的内存峰值。
func loadChunkPayload(dir string, x, z int) ([]byte, error) {
	path := regionPath(dir, x, z)
	file, err := openRegion(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	slot := regionSlot(x, z)
	var location [4]byte
	if _, err := file.ReadAt(location[:], int64(slot)*4); err != nil {
		return nil, fmt.Errorf("区域文件 %s 读取位置表失败：%w", path, err)
	}
	entry := binary.BigEndian.Uint32(location[:])
	if entry == 0 {
		return nil, nil
	}
	offset := int64(entry>>8) * sectorSize
	sectors := int64(entry & 0xFF)
	if sectors == 0 {
		return nil, fmt.Errorf("区域文件 %s 槽位 %d 的位置表项无效", path, slot)
	}

	var header [5]byte
	if _, err := file.ReadAt(header[:], offset); err != nil {
		return nil, fmt.Errorf("区域文件 %s 槽位 %d 读取负载头失败：%w", path, slot, err)
	}
	length := int64(binary.BigEndian.Uint32(header[:4]))
	if length < 2 || length > sectors*sectorSize-4 {
		return nil, fmt.Errorf("区域文件 %s 槽位 %d 的负载长度 %d 越界", path, slot, length)
	}
	if header[4] != 2 {
		return nil, fmt.Errorf("区域文件 %s 槽位 %d 使用了不支持的压缩类型 %d", path, slot, header[4])
	}
	compressed := make([]byte, length-1)
	if _, err := file.ReadAt(compressed, offset+5); err != nil {
		return nil, fmt.Errorf("区域文件 %s 槽位 %d 读取负载失败：%w", path, slot, err)
	}
	payload, err := zlibDecompress(compressed, maxChunkPayload)
	if err != nil {
		return nil, fmt.Errorf("区域文件 %s 槽位 %d 解压失败：%w", path, slot, err)
	}
	return payload, nil
}

// saveRegion 把若干槽位的新负载写入区域文件：未更新的槽位从原文件按
// 原始压缩数据复制（不解压、不重新压缩）。先写临时文件并 fsync，
// 再重命名，避免崩溃造成部分写入。
// payloads 必须是 zlib 压缩后的区块负载。
func saveRegion(path string, payloads map[int][]byte) error {
	old, err := openRegion(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		old = nil
	}

	const slotCount = regionSize * regionSize
	var locations, timestamps [slotCount]uint32
	if old != nil {
		header := make([]byte, headerSectors*sectorSize)
		if _, err := io.ReadFull(old, header); err != nil {
			return fmt.Errorf("区域文件 %s 头部读取失败：%w", path, err)
		}
		for slot := 0; slot < slotCount; slot++ {
			locations[slot] = binary.BigEndian.Uint32(header[slot*4:])
			timestamps[slot] = binary.BigEndian.Uint32(header[slotCount*4+slot*4:])
		}
	}

	temp := path + ".tmp"
	out, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	fail := func(err error) error {
		_ = out.Close()
		_ = os.Remove(temp)
		return err
	}

	// 头部占位（位置表与时间戳表在末尾回填）。
	if _, err := out.Write(make([]byte, headerSectors*sectorSize)); err != nil {
		return fail(err)
	}

	var (
		nextSector = uint32(headerSectors)
		now        = uint32(time.Now().Unix())
	)
	// 逐槽位写出：新负载直接写压缩数据，旧槽位保留原始压缩数据。
	for slot := 0; slot < slotCount; slot++ {
		if payload, ok := payloads[slot]; ok {
			// 负载 = 4 字节长度（含压缩类型字节）+ 1 字节压缩类型 + 压缩数据。
			total := 1 + len(payload)
			sectorCount := (4 + total + sectorSize - 1) / sectorSize
			if sectorCount > 255 {
				return fail(fmt.Errorf("区域文件 %s 槽位 %d 的区块负载过大（压缩后 %d 字节）", path, slot, len(payload)))
			}
			var length [4]byte
			binary.BigEndian.PutUint32(length[:], uint32(total))
			if _, err := out.Write(length[:]); err != nil {
				return fail(err)
			}
			if _, err := out.Write([]byte{2}); err != nil { // zlib
				return fail(err)
			}
			if _, err := out.Write(payload); err != nil {
				return fail(err)
			}
			if pad := sectorCount*sectorSize - 4 - total; pad > 0 {
				if _, err := out.Write(make([]byte, pad)); err != nil {
					return fail(err)
				}
			}
			locations[slot] = nextSector<<8 | uint32(sectorCount)
			timestamps[slot] = now
			nextSector += uint32(sectorCount)
			continue
		}
		if locations[slot] == 0 || old == nil {
			continue
		}
		// 未更新的槽位：原样复制旧数据（不解压、不重新压缩）。
		entry := locations[slot]
		offset := int64(entry>>8) * sectorSize
		sectors := int64(entry & 0xFF)
		if sectors == 0 {
			return fail(fmt.Errorf("区域文件 %s 槽位 %d 的位置表项无效", path, slot))
		}
		var length [4]byte
		if _, err := old.ReadAt(length[:], offset); err != nil {
			return fail(fmt.Errorf("区域文件 %s 槽位 %d 读取负载头失败：%w", path, slot, err))
		}
		total := int64(binary.BigEndian.Uint32(length[:]))
		if total < 2 || total > sectors*sectorSize-4 {
			return fail(fmt.Errorf("区域文件 %s 槽位 %d 的负载长度 %d 越界", path, slot, total))
		}
		stored := make([]byte, 4+total)
		copy(stored, length[:])
		if _, err := old.ReadAt(stored[4:], offset+4); err != nil {
			return fail(fmt.Errorf("区域文件 %s 槽位 %d 读取负载失败：%w", path, slot, err))
		}
		if stored[4] != 2 {
			return fail(fmt.Errorf("区域文件 %s 槽位 %d 使用了不支持的压缩类型 %d", path, slot, stored[4]))
		}
		sectorCount := uint32((len(stored) + sectorSize - 1) / sectorSize)
		if sectorCount > 255 {
			return fail(fmt.Errorf("区域文件 %s 槽位 %d 的区块负载过大", path, slot))
		}
		if _, err := out.Write(stored); err != nil {
			return fail(err)
		}
		if pad := int(sectorCount)*sectorSize - len(stored); pad > 0 {
			if _, err := out.Write(make([]byte, pad)); err != nil {
				return fail(err)
			}
		}
		locations[slot] = nextSector<<8 | uint32(sectorCount)
		nextSector += uint32(sectorCount)
	}

	// 回填位置表与时间戳表。
	header := make([]byte, headerSectors*sectorSize)
	for slot := 0; slot < slotCount; slot++ {
		binary.BigEndian.PutUint32(header[slot*4:], locations[slot])
		binary.BigEndian.PutUint32(header[slotCount*4+slot*4:], timestamps[slot])
	}
	if _, err := out.WriteAt(header, 0); err != nil {
		return fail(err)
	}
	if err := out.Sync(); err != nil {
		return fail(err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(temp)
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return err
	}
	// 重命名替换了文件：丢弃缓存的旧句柄，后续读取打开新文件。
	dropRegion(path)
	// 尽力同步目录项，保证重命名本身落盘；失败不影响正确性。
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

// LoadChunk 从世界目录读取区块；区块不存在时返回 (nil, nil)。
func LoadChunk(dir string, x, z int) (*Chunk, error) {
	payload, err := loadChunkPayload(dir, x, z)
	if err != nil {
		return nil, err
	}
	if payload == nil {
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

// SavePayloads 把区块负载写入磁盘：按区域文件分组，每个文件只做一次
// 读-改-写（原子替换）。payloads 必须是 zlib 压缩后的负载
// （encodeCompressedChunkPayload 的输出）；未更新的槽位按原始压缩数据复制。
func SavePayloads(dir string, payloads map[ChunkPos][]byte) error {
	if len(payloads) == 0 {
		return nil
	}
	byRegion := make(map[string]map[int][]byte)
	for pos, payload := range payloads {
		path := regionPath(dir, pos.X, pos.Z)
		slots := byRegion[path]
		if slots == nil {
			slots = make(map[int][]byte)
			byRegion[path] = slots
		}
		slots[regionSlot(pos.X, pos.Z)] = payload
	}
	for path, slots := range byRegion {
		if err := saveRegion(path, slots); err != nil {
			return err
		}
	}
	return nil
}

// encodeChunkPayload 把区块编码为存储负载（未压缩）。
// 负载大小可精确计算，先一次分配再按偏移写入，避免 append 反复增长与拷贝。
func encodeChunkPayload(chunk *Chunk) []byte {
	// 与运行时方块修改互斥（调用方可能持有 world.mu，但不会持有 chunk 锁）。
	chunk.mu.RLock()
	defer chunk.mu.RUnlock()

	size := 14 + SectionCount*3 + 16*2 // 32 字节群系网格
	for _, s := range chunk.sections {
		if s != nil && !s.storage.allAir() {
			size += SectionVolume * 2
		}
	}
	// 方块实体：数量前缀 + 每项 1 字节坐标 + type/槽位数的 varint 上限，
	// 每个槽位按 itemID/count 两个 varint 的上限（10 字节）估算；
	// 熔炉进度 4 个 varint 上限 20 字节。
	size += 5
	for _, entity := range chunk.blockEntities {
		size += 31 + len(entity.Items)*10
	}
	payload := make([]byte, size)
	copy(payload, "GMCS")
	binary.BigEndian.PutUint16(payload[4:6], chunkVersion)
	binary.BigEndian.PutUint32(payload[6:10], uint32(int32(chunk.X)))
	binary.BigEndian.PutUint32(payload[10:14], uint32(int32(chunk.Z)))
	offset := 14
	for _, s := range chunk.sections {
		var flags byte
		if s != nil && !s.storage.allAir() {
			flags = 1
		}
		payload[offset] = flags
		// section 内的 biome 字段为 v1–v3 兼容保留（v4 读取方忽略）。
		binary.BigEndian.PutUint16(payload[offset+1:offset+3], chunk.biomes[biomeCellIndex(2, 2)])
		offset += 3
		if flags&1 != 0 {
			offset = writeSectionPayload(payload, offset, &s.storage)
		}
	}
	// 群系网格（4×4 列分辨率）。
	for i := 0; i < 16; i++ {
		binary.BigEndian.PutUint16(payload[offset:offset+2], chunk.biomes[i])
		offset += 2
	}
	offset = writeBlockEntities(payload, offset, chunk)
	return payload[:offset]
}

// writeBlockEntities 把方块实体写入 dst 的 offset 处，返回新偏移。
// 按索引排序输出，保证同一区块的负载可复现。
func writeBlockEntities(dst []byte, offset int, chunk *Chunk) int {
	offset += binary.PutUvarint(dst[offset:], uint64(len(chunk.blockEntities)))
	if len(chunk.blockEntities) == 0 {
		return offset
	}
	indices := make([]int, 0, len(chunk.blockEntities))
	for index := range chunk.blockEntities {
		indices = append(indices, index)
	}
	sortInts(indices)
	for _, index := range indices {
		entity := chunk.blockEntities[index]
		// v3 起写完整索引（uvarint）：v2 的单字节写法丢失了 Y 高位，
		// 导致重新加载后按世界 Y 查询不到方块实体。
		offset += binary.PutUvarint(dst[offset:], uint64(uint32(index)))
		offset += binary.PutUvarint(dst[offset:], uint64(uint32(int64(entity.TypeID))))
		// 槽位数量与内容。
		offset += binary.PutUvarint(dst[offset:], uint64(len(entity.Items)))
		for _, slot := range entity.Items {
			offset += binary.PutUvarint(dst[offset:], uint64(uint32(slot.ItemID)))
			offset += binary.PutUvarint(dst[offset:], uint64(slot.Count))
		}
		// 熔炉类进度（非熔炉为 0，仍写出以保证格式统一）。
		offset += binary.PutUvarint(dst[offset:], uint64(uint32(entity.BurnRemaining)))
		offset += binary.PutUvarint(dst[offset:], uint64(uint32(entity.BurnTotal)))
		offset += binary.PutUvarint(dst[offset:], uint64(uint32(entity.CookProgress)))
		offset += binary.PutUvarint(dst[offset:], uint64(uint32(entity.CookTotal)))
	}
	return offset
}

// writeSectionPayload 把 v1 存储格式的 section 方块数据
// （4096 个方块状态 × u16 大端）写入 dst 的 offset 处，返回新偏移。
// 按存储位宽分派展开，避免逐方块调用 at() 的 switch 与调色板查找开销。
func writeSectionPayload(dst []byte, offset int, st *sectionStorage) int {
	switch st.bits {
	case 0:
		// uniform：重复写同一对字节。
		high, low := byte(st.uniform>>8), byte(st.uniform)
		for i := 0; i < SectionVolume; i++ {
			dst[offset] = high
			dst[offset+1] = low
			offset += 2
		}
	case 16:
		// 直接存储：每个 uint64 含 4 个 16 位状态，直接展开。
		for _, word := range st.data {
			binary.BigEndian.PutUint16(dst[offset:offset+2], uint16(word>>48))
			binary.BigEndian.PutUint16(dst[offset+2:offset+4], uint16(word>>32))
			binary.BigEndian.PutUint16(dst[offset+4:offset+6], uint16(word>>16))
			binary.BigEndian.PutUint16(dst[offset+6:offset+8], uint16(word))
			offset += 8
		}
	default:
		bits := int(st.bits)
		mask := uint64(1)<<uint(bits) - 1
		for _, word := range st.data {
			for shift := 0; shift < 64; shift += bits {
				binary.BigEndian.PutUint16(dst[offset:offset+2], st.palette[(word>>uint(shift))&mask])
				offset += 2
			}
		}
	}
	return offset
}

// decodeChunkPayload 解析存储负载（版本 1 与 2）。
func decodeChunkPayload(payload []byte) (*Chunk, error) {
	if len(payload) < 14 || string(payload[:4]) != "GMCS" {
		return nil, fmt.Errorf("区块负载头无效")
	}
	version := binary.BigEndian.Uint16(payload[4:6])
	if version < 1 || version > chunkVersion {
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
		offset += 3
		if flags&1 == 0 {
			continue
		}
		need := SectionVolume * 2
		if offset+need > len(payload) {
			return nil, fmt.Errorf("区块负载截断于 section %d 的方块数据", index)
		}
		s := &section{}
		// 顺序解码为紧凑存储：全空气保持零值；其余方块走增量维护。
		for i := 0; i < SectionVolume; i++ {
			state := binary.BigEndian.Uint16(payload[offset+2*i : offset+2*i+2])
			if state != AirBlock {
				s.setBlock(i, state)
			}
		}
		offset += need
		if !s.storage.allAir() {
			chunk.sections[index] = s
		}
	}
	if version >= 4 {
		if offset+32 > len(payload) {
			return nil, fmt.Errorf("区块负载截断于群系网格")
		}
		for i := 0; i < 16; i++ {
			chunk.biomes[i] = binary.BigEndian.Uint16(payload[offset : offset+2])
			offset += 2
		}
	}
	// v1–v3 负载没有群系网格：旧生成器只产出平原，保持 NewChunk 默认值即可。
	if version >= 2 {
		var err error
		offset, err = decodeBlockEntities(payload, offset, chunk, version)
		if err != nil {
			return nil, err
		}
	}
	if offset != len(payload) {
		return nil, fmt.Errorf("区块负载尾部有 %d 字节多余数据", len(payload)-offset)
	}
	return chunk, nil
}

// decodeBlockEntities 解析版本 2+ 负载中的方块实体部分（v3 起含熔炉进度）。
func decodeBlockEntities(payload []byte, offset int, chunk *Chunk, version uint16) (int, error) {
	count, size := binary.Uvarint(payload[offset:])
	if size <= 0 {
		return 0, fmt.Errorf("方块实体数量前缀无效")
	}
	offset += size
	if count > uint64(len(payload)) {
		return 0, fmt.Errorf("方块实体数量 %d 越界", count)
	}
	for i := uint64(0); i < count; i++ {
		if offset >= len(payload) {
			return 0, fmt.Errorf("方块实体数据截断")
		}
		var index int
		if version >= 3 {
			// v3：完整索引（含 Y 高位）。
			value, size := binary.Uvarint(payload[offset:])
			if size <= 0 {
				return 0, fmt.Errorf("方块实体索引无效")
			}
			offset += size
			index = int(uint32(value))
		} else {
			// v2：单字节 packed XZ（历史格式，Y 高位丢失）。
			index = int(payload[offset])
			offset++
		}
		typeID, size := binary.Uvarint(payload[offset:])
		if size <= 0 {
			return 0, fmt.Errorf("方块实体类型无效")
		}
		offset += size
		slotCount, size := binary.Uvarint(payload[offset:])
		if size <= 0 {
			return 0, fmt.Errorf("方块实体槽位数量无效")
		}
		offset += size
		if slotCount > SectionVolume {
			return 0, fmt.Errorf("方块实体槽位数量 %d 越界", slotCount)
		}
		entity := BlockEntity{TypeID: int32(typeID)}
		if slotCount > 0 {
			entity.Items = make([]ContainerItem, slotCount)
			for slot := range entity.Items {
				itemID, size := binary.Uvarint(payload[offset:])
				if size <= 0 {
					return 0, fmt.Errorf("容器槽位物品 ID 无效")
				}
				offset += size
				itemCount, size := binary.Uvarint(payload[offset:])
				if size <= 0 {
					return 0, fmt.Errorf("容器槽位数量无效")
				}
				offset += size
				entity.Items[slot] = ContainerItem{ItemID: int32(itemID), Count: int32(itemCount)}
			}
		}
		if version >= 3 {
			var values [4]int32
			for i := range values {
				value, size := binary.Uvarint(payload[offset:])
				if size <= 0 {
					return 0, fmt.Errorf("熔炉进度字段无效")
				}
				offset += size
				values[i] = int32(uint32(value))
			}
			entity.BurnRemaining, entity.BurnTotal = values[0], values[1]
			entity.CookProgress, entity.CookTotal = values[2], values[3]
		}
		if chunk.blockEntities == nil {
			chunk.blockEntities = make(map[int]BlockEntity)
		}
		if !entity.Empty() {
			chunk.blockEntities[index] = entity
		}
	}
	return offset, nil
}

// 存储压缩使用独立对象池：Flush/UnloadFar 会连续压缩大量区块，
// 每次新建 zlib.Writer（窗口 + 哈希表，数百 KB）会造成显著的分配与 GC 压力。
var (
	storageBufferPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}
	storageWriterPool = sync.Pool{New: func() any { return zlib.NewWriter(io.Discard) }}
)

// encodeCompressedChunkPayload 把区块编码为 zlib 压缩的存储负载。
func encodeCompressedChunkPayload(chunk *Chunk) ([]byte, error) {
	return zlibCompress(encodeChunkPayload(chunk))
}

// zlibCompress 压缩数据；复用 zlib.Writer 与缓冲区，
// 返回的切片与池中缓冲不共享底层数组。
func zlibCompress(data []byte) ([]byte, error) {
	buffer := storageBufferPool.Get().(*bytes.Buffer)
	buffer.Reset()
	writer := storageWriterPool.Get().(*zlib.Writer)
	writer.Reset(buffer)
	_, err := writer.Write(data)
	if err == nil {
		err = writer.Close()
	}
	storageWriterPool.Put(writer)
	var compressed []byte
	if err == nil {
		compressed = append([]byte(nil), buffer.Bytes()...)
	}
	// 防御性处理：不让对象池长期保留超大缓冲。
	if buffer.Cap() > maxChunkPayload {
		buffer = new(bytes.Buffer)
	}
	storageBufferPool.Put(buffer)
	return compressed, err
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
