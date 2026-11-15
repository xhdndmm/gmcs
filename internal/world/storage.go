//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

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
	"sort"
	"sync"
	"time"
)

// regionFileCache 是打开的区域文件句柄缓存（磁盘缓存）：读路径反复
// open/close 同一区域文件的系统调用开销可省；句柄按 LRU 有界。
//
// 缓存项使用引用计数：读者持有期间即使发生 LRU 淘汰、写入替换或
// ReleaseRegionHandles，也不会关闭正在 ReadAt/复制的文件；最后一个引用
// 归还时才真正 Close，避免旧实现依赖 GC 回收 fd 造成的句柄堆积。
type regionFileCache struct {
	mu      sync.Mutex
	entries map[string]*list.Element
	order   *list.List // 前端最新
}

type regionFileEntry struct {
	path    string
	file    *os.File
	refs    int
	retired bool // 已从缓存移除，等待最后一个引用释放
}

// regionCacheMax 是缓存的区域文件句柄上限（每个句柄占一个 fd）。
const regionCacheMax = 32

var regionCache = &regionFileCache{
	entries: make(map[string]*list.Element),
	order:   list.New(),
}

// openRegion 获取区域文件句柄并增加引用；调用方必须调用 entry.release()。
func openRegion(path string) (*regionFileEntry, error) {
	regionCache.mu.Lock()
	defer regionCache.mu.Unlock()
	if elem, ok := regionCache.entries[path]; ok {
		regionCache.order.MoveToFront(elem)
		entry := elem.Value.(*regionFileEntry)
		entry.refs++
		return entry, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	entry := &regionFileEntry{path: path, file: file, refs: 1}
	regionCache.entries[path] = regionCache.order.PushFront(entry)
	if regionCache.order.Len() > regionCacheMax {
		oldest := regionCache.order.Back()
		retire := oldest.Value.(*regionFileEntry)
		regionCache.order.Remove(oldest)
		delete(regionCache.entries, retire.path)
		retire.retired = true
		if retire.refs == 0 {
			_ = retire.file.Close()
		}
	}
	return entry, nil
}

// release 归还一次引用；缓存项已淘汰/替换时关闭最后一个引用。
func (e *regionFileEntry) release() {
	regionCache.mu.Lock()
	defer regionCache.mu.Unlock()
	if e.refs > 0 {
		e.refs--
	}
	if e.refs == 0 && e.retired {
		_ = e.file.Close()
	}
}

// dropRegion 丢弃缓存的句柄（写入重命名后调用）；正在读取的引用会保持
// 旧文件打开，直到 release。
func dropRegion(path string) {
	regionCache.mu.Lock()
	defer regionCache.mu.Unlock()
	if elem, ok := regionCache.entries[path]; ok {
		entry := elem.Value.(*regionFileEntry)
		regionCache.order.Remove(elem)
		delete(regionCache.entries, path)
		entry.retired = true
		if entry.refs == 0 {
			_ = entry.file.Close()
		}
	}
}

// ReleaseRegionHandles 清空全部缓存句柄（服务器空闲时释放 fd）；
// 活跃引用在 release 时关闭，避免关闭正在使用的文件。
func ReleaseRegionHandles() {
	regionCache.mu.Lock()
	defer regionCache.mu.Unlock()
	for elem := regionCache.order.Front(); elem != nil; elem = elem.Next() {
		entry := elem.Value.(*regionFileEntry)
		entry.retired = true
		if entry.refs == 0 {
			_ = entry.file.Close()
		}
	}
	regionCache.entries = make(map[string]*list.Element)
	regionCache.order.Init()
}

// 存储布局与 Minecraft 的区域文件一致：每个区域文件覆盖 32×32 个区块，
// 文件头为 1024 个位置表项（4 字节：3 字节偏移扇区 + 1 字节长度扇区）
// 与 1024 个时间戳（4 字节，Unix 秒），随后是 4KB 对齐的区块数据。
//
// 区块负载为 gmcs 自有格式（不是原版 NBT，不能与原版互读）：
//
//	magic "GMCS" (4B) | version u16 | x i32 | z i32 | 24 × section
//	section: flags u8（bit0 = 含方块数据）| biome u16 |（可选）方块状态
//	(version 13+) 方块状态使用调色板 + 位流：
//	  bits u8（0 = 单值，1/2/4/8 = 调色板索引，16 = 直接状态）
//	  bits == 0：state uvarint
//	  bits <= 8：paletteCount uvarint | paletteCount × state uvarint |
//	             ceil(4096×bits/64) × u64（小端位流，条目连续）
//	  bits == 16：ceil(4096×16/64) × u64（直接状态位流）
//	(version 1–12) 方块状态为 4096 × u16 大端（旧格式，读取兼容）
//	(version 4+) 16 × u16 群系网格（4×4 列分辨率，索引见 biomeCellIndex）
//	(version 2+) blockEntityCount varint | 每项: index uvarint | type varint
//	  | slotCount varint | slotCount × (itemID varint, count varint[, potionID varint]
//	    [, damage varint, repairCost varint, nameLen varint + name]）
//	  | (version 3+) burnRemaining | burnTotal | cookProgress | cookTotal（均为 varint）
//	  | (version 5+) brewTime | brewFuel（均为 varint）
//
// version 1 的负载不含方块实体（读取时视为空），version 2 起追加；
// version 5 起槽位携带药水 ID、方块实体携带酿造台进度；
// version 6 起槽位携带耐久损耗、铁砧累积惩罚与自定义名称；
// version 7 起槽位携带附魔列表；
// version 8 起槽位携带纹饰图案与材质名；
// version 9 起携带漏斗传送冷却，version 10 起携带末地折跃门目标，
// version 11 起携带刷怪笼/告示牌/战利品表，version 12 起携带 LootTable。
// 头部、坐标、群系与旧版方块状态为大端；v13 位流按 little-endian u64 存储。
// 负载使用 zlib 压缩（区域文件压缩类型 2）。
const (
	// maxConcurrentRegionSaves 限制一次批量保存中并行重写区域文件的数量：
	// 不同区域文件互不共享数据，并行可掩盖 fsync/磁盘延迟；上限避免
	// 一次 Flush 产生无界 goroutine 与过多页缓存压力。
	maxConcurrentRegionSaves = 4
	regionShift              = 5
	regionSize               = 1 << regionShift // 32 个区块
	sectorSize               = 4096
	headerSectors            = 2
	// chunkVersion 是当前存储负载版本：v13 使用调色板/位流的 section 方块
	// 编码（减小磁盘体积）；v11 追加刷怪笼/告示牌 NBT，v10 追加折跃门，
	// v4 追加 4×4 群系网格（section 内的 biome 字段仅作兼容保留），
	// v5 追加槽位药水 ID 与酿造台进度，v6 追加槽位耐久/铁砧惩罚/自定义名称，
	// v7 追加槽位附魔列表，v8 追加槽位纹饰图案/材质名，v9 追加漏斗传送冷却，
	// v10 追加末地折跃门目标。
	chunkVersion = 13
	// maxChunkPayload 是解压后区块负载的大小上限（防压缩炸弹）。
	maxChunkPayload = 1 << 20
	// maxStoredNameLength 是容器槽位自定义名称的存储长度上限（协议字符串上限）。
	maxStoredNameLength = 0xFFFF
	// maxStoredEnchantments 是容器槽位附魔数量的存储上限（附魔总量不超过
	// 注册表条目数，这里留出充足余量以防损坏数据造成超大分配）。
	maxStoredEnchantments = 128
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
	lock := regionLock(path)
	// 崩溃后遗留的日志必须先重放。快速路径只做一次 stat；正常读取不会
	// 与保存并发（保存持有同一分片的写锁）。
	if regionHasJournal(path) {
		lock.Lock()
		err := recoverRegion(path)
		lock.Unlock()
		if err != nil {
			return nil, err
		}
	}
	lock.RLock()
	defer lock.RUnlock()
	handle, err := openRegion(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer handle.release()
	file := handle.file

	slot := regionSlot(x, z)
	var location [4]byte
	if _, err := file.ReadAt(location[:], int64(slot)*4); err != nil {
		return nil, fmt.Errorf("区域文件 %s 读取位置表失败：%w", path, err)
	}
	locationEntry := binary.BigEndian.Uint32(location[:])
	if locationEntry == 0 {
		return nil, nil
	}
	offset := int64(locationEntry>>8) * sectorSize
	sectors := int64(locationEntry & 0xFF)
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

// maxRegionSectors 是区域位置表中 24 位扇区偏移的上限。
const maxRegionSectors = 1 << 24

// scanRegionUsed 返回头部引用的扇区位图、最大已用扇区，并校验旧头部
// 不指向文件范围之外。位图多留一个槽位便于追加分配。
func scanRegionUsed(header []byte, fileSize int64) ([]bool, uint32, error) {
	if len(header) != regionHeaderSize {
		return nil, 0, fmt.Errorf("区域头部长度 %d 无效", len(header))
	}
	if fileSize < 0 {
		return nil, 0, fmt.Errorf("区域文件大小 %d 无效", fileSize)
	}
	fileSectors := uint32((fileSize + sectorSize - 1) / sectorSize)
	if fileSectors > maxRegionSectors {
		return nil, 0, fmt.Errorf("区域文件扇区数 %d 过大", fileSectors)
	}
	if fileSize > 0 && fileSize < int64(regionHeaderSize) {
		return nil, 0, fmt.Errorf("区域文件长度 %d 小于头部", fileSize)
	}
	size := int(fileSectors)
	if size < headerSectors {
		size = headerSectors
	}
	used := make([]bool, size+1)
	maxUsed := uint32(headerSectors)
	for slot := 0; slot < regionSize*regionSize; slot++ {
		entry := binary.BigEndian.Uint32(header[slot*4:])
		if entry == 0 {
			continue
		}
		offset := entry >> 8
		count := entry & 0xFF
		if count == 0 || offset < headerSectors {
			return nil, 0, fmt.Errorf("区域槽位 %d 的位置表项无效", slot)
		}
		end := offset + count
		if end > fileSectors || end > maxRegionSectors {
			return nil, 0, fmt.Errorf("区域槽位 %d 超出文件范围", slot)
		}
		if int(end) >= len(used) {
			used = append(used, make([]bool, int(end)-len(used)+1)...)
		}
		for sector := offset; sector < end; sector++ {
			used[sector] = true
		}
		if end > maxUsed {
			maxUsed = end
		}
	}
	return used, maxUsed, nil
}

// findFreeSectors 在 used 中寻找第一个连续 count 个空闲扇区；找不到返回 -1。
func findFreeSectors(used []bool, count int) int {
	for start := headerSectors; start+count <= len(used); start++ {
		free := true
		for i := 0; i < count; i++ {
			if used[start+i] {
				free = false
				break
			}
		}
		if free {
			return start
		}
	}
	return -1
}

// saveRegion 以 copy-on-write + sidecar 日志增量更新区域文件：
// 新负载写入旧头部未引用的扇区，提交头部后旧扇区自然变成空闲区，
// 后续保存可复用；文件末尾的空闲扇区在提交后 truncate 回收。
func saveRegion(path string, payloads map[int][]byte) error {
	lock := regionLock(path)
	lock.Lock()
	defer lock.Unlock()
	if err := recoverRegion(path); err != nil {
		return err
	}
	if len(payloads) == 0 {
		return nil
	}

	oldEntry, err := openRegion(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var (
		oldFile  *os.File
		fileSize int64
		header   = make([]byte, regionHeaderSize)
	)
	if oldEntry != nil {
		defer oldEntry.release()
		oldFile = oldEntry.file
		info, err := oldFile.Stat()
		if err != nil {
			return err
		}
		fileSize = info.Size()
		if fileSize < int64(regionHeaderSize) {
			return fmt.Errorf("区域文件 %s 长度 %d 小于头部", path, fileSize)
		}
		if _, err := oldFile.ReadAt(header, 0); err != nil {
			return fmt.Errorf("区域文件 %s 头部读取失败：%w", path, err)
		}
	}
	used, maxUsed, err := scanRegionUsed(header, fileSize)
	if err != nil {
		return fmt.Errorf("区域文件 %s: %w", path, err)
	}
	nextSector := uint32((fileSize + sectorSize - 1) / sectorSize)
	if nextSector < headerSectors {
		nextSector = headerSectors
	}

	slots := make([]int, 0, len(payloads))
	for slot := range payloads {
		if slot < 0 || slot >= regionSize*regionSize {
			return fmt.Errorf("区域 %s 槽位 %d 越界", path, slot)
		}
		slots = append(slots, slot)
	}
	sort.Ints(slots)

	now := uint32(time.Now().Unix())
	writes := make([]regionWrite, 0, len(slots))
	for _, slot := range slots {
		payload := payloads[slot]
		total := 1 + len(payload)
		stored := 4 + total
		sectorCount := (stored + sectorSize - 1) / sectorSize
		if sectorCount == 0 || sectorCount > 255 {
			return fmt.Errorf("区域文件 %s 槽位 %d 的区块负载过大（压缩后 %d 字节）", path, slot, len(payload))
		}
		start := findFreeSectors(used, sectorCount)
		if start < 0 {
			start = int(nextSector)
			nextSector += uint32(sectorCount)
			if nextSector > maxRegionSectors {
				return fmt.Errorf("区域文件 %s 空间不足", path)
			}
			if start+sectorCount > len(used) {
				used = append(used, make([]bool, start+sectorCount-len(used))...)
			}
		}
		if start < headerSectors || start+sectorCount > maxRegionSectors {
			return fmt.Errorf("区域文件 %s 槽位 %d 的扇区分配无效", path, slot)
		}
		for i := 0; i < sectorCount; i++ {
			used[start+i] = true
		}
		end := uint32(start + sectorCount)
		if end > maxUsed {
			maxUsed = end
		}
		if end > nextSector {
			nextSector = end
		}
		data := make([]byte, sectorCount*sectorSize)
		binary.BigEndian.PutUint32(data[:4], uint32(total))
		data[4] = 2 // zlib
		copy(data[5:], payload)
		writes = append(writes, regionWrite{offset: int64(start) * sectorSize, data: data})

		binary.BigEndian.PutUint32(header[slot*4:], uint32(start)<<8|uint32(sectorCount))
		binary.BigEndian.PutUint32(header[regionSize*regionSize*4+slot*4:], now)
	}
	finalSize := int64(maxUsed) * sectorSize
	if err := writeRegionJournal(path, header, finalSize, writes); err != nil {
		return fmt.Errorf("写入区域日志 %s: %w", path, err)
	}
	if err := applyRegionJournal(path, header, finalSize, writes); err != nil {
		// 保留日志，下一次读取/保存会重放。
		return fmt.Errorf("应用区域日志 %s: %w", path, err)
	}
	if err := os.Remove(regionJournalPath(path)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("删除区域日志 %s: %w", path, err)
	}
	syncDir(path)
	dropRegion(path)
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
	paths := make([]string, 0, len(byRegion))
	for path := range byRegion {
		paths = append(paths, path)
	}
	// 固定顺序便于诊断；并行只改变 I/O 调度，不改变每个区域的内容。
	sort.Strings(paths)
	if len(paths) == 1 {
		return saveRegion(paths[0], byRegion[paths[0]])
	}
	workers := maxConcurrentRegionSaves
	if workers > len(paths) {
		workers = len(paths)
	}
	jobs := make(chan string)
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range jobs {
				if err := saveRegion(path, byRegion[path]); err != nil {
					errMu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					errMu.Unlock()
				}
			}
		}()
	}
	for _, path := range paths {
		jobs <- path
	}
	close(jobs)
	wg.Wait()
	return firstErr
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
			size += compactSectionSize(&s.storage)
		}
	}
	// 方块实体：数量前缀 + 每项 1 字节坐标 + type/槽位数的 varint 上限，
	// 每个槽位按 itemID/count 两个 varint 的上限（10 字节）估算，
	// 加上自定义名称与附魔列表的精确长度；
	// 熔炉进度 4 个 varint 上限 20 字节。
	size += 5
	for _, entity := range chunk.blockEntities {
		size += 31 + len(entity.Items)*10
		size += len(entity.SpawnerEntity) + 2 + 5*5
		size += len(entity.LootTable) + 2
		for _, line := range entity.FrontText {
			size += len(line) + 2
		}
		for _, line := range entity.BackText {
			size += len(line) + 2
		}
		for _, slot := range entity.Items {
			size += len(slot.CustomName) + 2
			size += 2 + len(slot.Enchantments)*8
			size += len(slot.TrimPattern) + len(slot.TrimMaterial) + 4
		}
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
			offset = writeCompactSection(payload, offset, &s.storage)
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
		// 槽位数量与内容（v5 起含药水 ID，v6 起含耐久/铁砧惩罚/名称）。
		offset += binary.PutUvarint(dst[offset:], uint64(len(entity.Items)))
		for _, slot := range entity.Items {
			offset += binary.PutUvarint(dst[offset:], uint64(uint32(slot.ItemID)))
			offset += binary.PutUvarint(dst[offset:], uint64(slot.Count))
			offset += binary.PutUvarint(dst[offset:], uint64(uint32(slot.PotionID)))
			offset += binary.PutUvarint(dst[offset:], uint64(uint32(slot.Damage)))
			offset += binary.PutUvarint(dst[offset:], uint64(uint32(slot.RepairCost)))
			offset += binary.PutUvarint(dst[offset:], uint64(len(slot.CustomName)))
			offset += copy(dst[offset:], slot.CustomName)
			// 附魔列表（v7）：数量 + 数量 × (id, level)。
			offset += binary.PutUvarint(dst[offset:], uint64(len(slot.Enchantments)))
			for _, ench := range slot.Enchantments {
				offset += binary.PutUvarint(dst[offset:], uint64(uint32(ench.ID)))
				offset += binary.PutUvarint(dst[offset:], uint64(uint32(ench.Level)))
			}
			// 纹饰（v8）：图案与材质名（长度前缀变量）。
			offset += binary.PutUvarint(dst[offset:], uint64(len(slot.TrimPattern)))
			offset += copy(dst[offset:], slot.TrimPattern)
			offset += binary.PutUvarint(dst[offset:], uint64(len(slot.TrimMaterial)))
			offset += copy(dst[offset:], slot.TrimMaterial)
		}
		// 熔炉类进度（非熔炉为 0，仍写出以保证格式统一）。
		offset += binary.PutUvarint(dst[offset:], uint64(uint32(entity.BurnRemaining)))
		offset += binary.PutUvarint(dst[offset:], uint64(uint32(entity.BurnTotal)))
		offset += binary.PutUvarint(dst[offset:], uint64(uint32(entity.CookProgress)))
		offset += binary.PutUvarint(dst[offset:], uint64(uint32(entity.CookTotal)))
		// 酿造台进度（v5；非酿造台为 0，仍写出以保证格式统一）。
		offset += binary.PutUvarint(dst[offset:], uint64(uint32(entity.BrewTime)))
		offset += binary.PutUvarint(dst[offset:], uint64(uint32(entity.BrewFuel)))
		// 漏斗传送冷却（v9；非漏斗为 0，仍写出以保证格式统一）。
		offset += binary.PutUvarint(dst[offset:], uint64(uint32(entity.TransferCooldown)))
		// 末地折跃门目标（v10；非折跃门为 0）。
		gatewayFlags := uint64(0)
		if entity.GatewayExact {
			gatewayFlags = 1
		}
		offset += binary.PutUvarint(dst[offset:], gatewayFlags)
		for _, coordinate := range entity.GatewayExit {
			offset += binary.PutUvarint(dst[offset:], uint64(uint32(coordinate)))
		}
		// v11：刷怪笼与告示牌数据。
		offset += binary.PutUvarint(dst[offset:], uint64(len(entity.SpawnerEntity)))
		offset += copy(dst[offset:], entity.SpawnerEntity)
		for _, value := range []int32{entity.SpawnerDelay, entity.SpawnerMinDelay, entity.SpawnerMaxDelay} {
			offset += binary.PutUvarint(dst[offset:], uint64(uint32(value)))
		}
		for _, line := range entity.FrontText {
			offset += binary.PutUvarint(dst[offset:], uint64(len(line)))
			offset += copy(dst[offset:], line)
		}
		for _, line := range entity.BackText {
			offset += binary.PutUvarint(dst[offset:], uint64(len(line)))
			offset += copy(dst[offset:], line)
		}
		offset += binary.PutUvarint(dst[offset:], uint64(len(entity.LootTable)))
		offset += copy(dst[offset:], entity.LootTable)
	}
	return offset
}

// uvarintSize 返回 v 的 uvarint 编码长度。仅用于精确分配负载缓冲。
func uvarintSize(v uint64) int {
	n := 1
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n
}

// compactSectionSize 返回 v13 调色板/位流 section 的编码大小。
func compactSectionSize(st *sectionStorage) int {
	if st.bits == 0 {
		return 1 + uvarintSize(uint64(st.uniform))
	}
	if state, uniform := st.uniformState(); uniform {
		return 1 + uvarintSize(uint64(state))
	}
	if st.bits == 16 {
		return 1 + SectionVolume*2
	}
	size := 1 + uvarintSize(uint64(len(st.palette)))
	for _, state := range st.palette {
		size += uvarintSize(uint64(state))
	}
	return size + SectionVolume*int(st.bits)/8
}

// writeCompactSection 把 v13 格式的 section 方块数据写入 dst 的 offset 处，
// 返回新偏移。小端 uint64 位流与运行时 sectionStorage 的布局一致，
// 因此常见路径可直接复制 data，无需逐方块重新打包。
func writeCompactSection(dst []byte, offset int, st *sectionStorage) int {
	if st.bits == 0 {
		dst[offset] = 0
		return offset + 1 + binary.PutUvarint(dst[offset+1:], uint64(st.uniform))
	}
	if state, uniform := st.uniformState(); uniform {
		dst[offset] = 0
		return offset + 1 + binary.PutUvarint(dst[offset+1:], uint64(state))
	}
	if st.bits == 16 {
		dst[offset] = 16
		offset++
		for _, word := range st.data {
			binary.LittleEndian.PutUint64(dst[offset:], word)
			offset += 8
		}
		return offset
	}
	dst[offset] = st.bits
	offset++
	offset += binary.PutUvarint(dst[offset:], uint64(len(st.palette)))
	for _, state := range st.palette {
		offset += binary.PutUvarint(dst[offset:], uint64(state))
	}
	for _, word := range st.data[:SectionVolume*int(st.bits)/64] {
		binary.LittleEndian.PutUint64(dst[offset:], word)
		offset += 8
	}
	return offset
}

// readStoredName 读取一个长度前缀的存储字符串（uvarint 长度 + 字节），
// 返回读到的字符串与新偏移；长度越界或截断时返回错误。
func readStoredName(payload []byte, offset int, what string) (string, int, error) {
	length, size := binary.Uvarint(payload[offset:])
	if size <= 0 {
		return "", 0, fmt.Errorf("%s 长度无效", what)
	}
	offset += size
	if length > maxStoredNameLength || uint64(len(payload)-offset) < length {
		return "", 0, fmt.Errorf("%s 越界", what)
	}
	return string(payload[offset : offset+int(length)]), offset + int(length), nil
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
		var err error
		if version >= 13 {
			offset, err = decodeCompactSection(payload, offset, chunk, index)
		} else {
			offset, err = decodeLegacySection(payload, offset, chunk, index)
		}
		if err != nil {
			return nil, err
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

// decodeLegacySection 解析 v1–v12 的 4096 × u16 方块状态。
func decodeLegacySection(payload []byte, offset int, chunk *Chunk, index int) (int, error) {
	need := SectionVolume * 2
	if offset+need > len(payload) {
		return 0, fmt.Errorf("区块负载截断于 section %d 的方块数据", index)
	}
	s := &section{}
	// 顺序解码为紧凑存储：全空气保持零值；其余方块走增量维护。
	for i := 0; i < SectionVolume; i++ {
		state := binary.BigEndian.Uint16(payload[offset+2*i : offset+2*i+2])
		if state != AirBlock {
			s.setBlock(i, state)
		}
	}
	if !s.storage.allAir() {
		chunk.sections[index] = s
	}
	return offset + need, nil
}

// decodeCompactSection 解析 v13 调色板/位流 section。
func decodeCompactSection(payload []byte, offset int, chunk *Chunk, index int) (int, error) {
	if offset >= len(payload) {
		return 0, fmt.Errorf("区块负载截断于 section %d 的位宽", index)
	}
	bits := payload[offset]
	offset++
	if bits != 0 && bits != 1 && bits != 2 && bits != 4 && bits != 8 && bits != 16 {
		return 0, fmt.Errorf("section %d 的位宽 %d 无效", index, bits)
	}
	st := sectionStorage{bits: bits}
	if bits == 0 {
		state, size := binary.Uvarint(payload[offset:])
		if size <= 0 || state > 0xFFFF {
			return 0, fmt.Errorf("section %d 的单值状态无效", index)
		}
		offset += size
		st.uniform = uint16(state)
	} else {
		if bits != 16 {
			count, size := binary.Uvarint(payload[offset:])
			if size <= 0 || count == 0 || count > uint64(1)<<bits {
				return 0, fmt.Errorf("section %d 的调色板数量 %d 无效", index, count)
			}
			offset += size
			st.palette = make([]uint16, count)
			for i := range st.palette {
				state, size := binary.Uvarint(payload[offset:])
				if size <= 0 || state > 0xFFFF {
					return 0, fmt.Errorf("section %d 的调色板条目无效", index)
				}
				offset += size
				st.palette[i] = uint16(state)
			}
		}
		words := SectionVolume * int(bits) / 64
		if offset+words*8 > len(payload) {
			return 0, fmt.Errorf("区块负载截断于 section %d 的位流", index)
		}
		st.data = make([]uint64, words)
		for i := range st.data {
			st.data[i] = binary.LittleEndian.Uint64(payload[offset:])
			offset += 8
		}
	}

	s := &section{storage: st}
	for i := 0; i < SectionVolume; i++ {
		if bits != 0 && bits != 16 && int(st.wordAt(i)) >= len(st.palette) {
			return 0, fmt.Errorf("section %d 的调色板索引越界", index)
		}
		if st.at(i) != AirBlock {
			s.nonAir++
		}
	}
	if s.nonAir > 0 {
		chunk.sections[index] = s
	}
	return offset, nil
}

// decodeBlockEntities 解析版本 2+ 负载中的方块实体部分
// （v3 起含熔炉进度，v5 起槽位含药水 ID 且含酿造台进度，
// v6 起槽位含耐久/铁砧惩罚/名称，v7 起槽位含附魔列表，
// v8 起槽位含纹饰图案/材质名，v9 起含漏斗传送冷却，
// v10 起含末地折跃门 ExitPortal/exact）。
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
				potionID := uint64(0)
				if version >= 5 {
					potionID, size = binary.Uvarint(payload[offset:])
					if size <= 0 {
						return 0, fmt.Errorf("容器槽位药水 ID 无效")
					}
					offset += size
				}
				damage, repairCost := uint64(0), uint64(0)
				customName := ""
				if version >= 6 {
					if damage, size = binary.Uvarint(payload[offset:]); size <= 0 {
						return 0, fmt.Errorf("容器槽位耐久无效")
					}
					offset += size
					if repairCost, size = binary.Uvarint(payload[offset:]); size <= 0 {
						return 0, fmt.Errorf("容器槽位铁砧惩罚无效")
					}
					offset += size
					nameLen, nameSize := binary.Uvarint(payload[offset:])
					if nameSize <= 0 {
						return 0, fmt.Errorf("容器槽位名称长度无效")
					}
					offset += nameSize
					if nameLen > maxStoredNameLength || uint64(len(payload)-offset) < nameLen {
						return 0, fmt.Errorf("容器槽位名称越界")
					}
					customName = string(payload[offset : offset+int(nameLen)])
					offset += int(nameLen)
				}
				var enchantments []EnchantedItem
				if version >= 7 {
					enchCount, enchSize := binary.Uvarint(payload[offset:])
					if enchSize <= 0 {
						return 0, fmt.Errorf("容器槽位附魔数量无效")
					}
					offset += enchSize
					if enchCount > maxStoredEnchantments {
						return 0, fmt.Errorf("容器槽位附魔数量 %d 越界", enchCount)
					}
					if enchCount > 0 {
						enchantments = make([]EnchantedItem, 0, enchCount)
					}
					for j := uint64(0); j < enchCount; j++ {
						enchID, idSize := binary.Uvarint(payload[offset:])
						if idSize <= 0 {
							return 0, fmt.Errorf("容器槽位附魔 ID 无效")
						}
						offset += idSize
						enchLevel, levelSize := binary.Uvarint(payload[offset:])
						if levelSize <= 0 {
							return 0, fmt.Errorf("容器槽位附魔等级无效")
						}
						offset += levelSize
						enchantments = append(enchantments, EnchantedItem{ID: int32(enchID), Level: int32(enchLevel)})
					}
				}
				trimPattern, trimMaterial := "", ""
				if version >= 8 {
					var err error
					if trimPattern, offset, err = readStoredName(payload, offset, "容器槽位纹饰图案"); err != nil {
						return 0, err
					}
					if trimMaterial, offset, err = readStoredName(payload, offset, "容器槽位纹饰材质"); err != nil {
						return 0, err
					}
				}
				entity.Items[slot] = ContainerItem{
					ItemID: int32(itemID), Count: int32(itemCount), PotionID: int32(potionID),
					Damage: int32(damage), RepairCost: int32(repairCost), CustomName: customName,
					Enchantments: enchantments,
					TrimPattern:  trimPattern, TrimMaterial: trimMaterial,
				}
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
		if version >= 5 {
			var values [2]int32
			for i := range values {
				value, size := binary.Uvarint(payload[offset:])
				if size <= 0 {
					return 0, fmt.Errorf("酿造台进度字段无效")
				}
				offset += size
				values[i] = int32(uint32(value))
			}
			entity.BrewTime, entity.BrewFuel = values[0], values[1]
		}
		if version >= 9 {
			value, size := binary.Uvarint(payload[offset:])
			if size <= 0 {
				return 0, fmt.Errorf("漏斗传送冷却字段无效")
			}
			offset += size
			entity.TransferCooldown = int32(uint32(value))
		}
		if version >= 10 {
			flags, size := binary.Uvarint(payload[offset:])
			if size <= 0 {
				return 0, fmt.Errorf("折跃门标记字段无效")
			}
			offset += size
			entity.GatewayExact = flags&1 != 0
			for i := range entity.GatewayExit {
				value, size := binary.Uvarint(payload[offset:])
				if size <= 0 {
					return 0, fmt.Errorf("折跃门目标字段无效")
				}
				offset += size
				entity.GatewayExit[i] = int(int32(uint32(value)))
			}
		}
		if version >= 11 {
			var err error
			if entity.SpawnerEntity, offset, err = readStoredName(payload, offset, "刷怪笼实体"); err != nil {
				return 0, err
			}
			for _, target := range []*int32{&entity.SpawnerDelay, &entity.SpawnerMinDelay, &entity.SpawnerMaxDelay} {
				value, size := binary.Uvarint(payload[offset:])
				if size <= 0 {
					return 0, fmt.Errorf("刷怪笼延迟字段无效")
				}
				offset += size
				*target = int32(uint32(value))
			}
			for index := range entity.FrontText {
				if entity.FrontText[index], offset, err = readStoredName(payload, offset, "告示牌正面文本"); err != nil {
					return 0, err
				}
			}
			for index := range entity.BackText {
				if entity.BackText[index], offset, err = readStoredName(payload, offset, "告示牌背面文本"); err != nil {
					return 0, err
				}
			}
			if entity.LootTable, offset, err = readStoredName(payload, offset, "战利品表"); err != nil {
				return 0, err
			}
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
	// 解压读取器关闭失败不影响已读取的数据。
	defer func() { _ = reader.Close() }()
	decoded, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(decoded) > limit {
		return nil, fmt.Errorf("解压后的区块负载超过大小上限 %d", limit)
	}
	return decoded, nil
}
