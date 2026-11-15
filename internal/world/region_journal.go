//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package world

// 本文件实现区域文件的增量更新与崩溃恢复。
//
// 写入采用 copy-on-write + 日志：
//   1. 新负载只写入旧头部未引用的空闲扇区（或文件末尾）；
//   2. 完整的新头部、写入列表与最终文件大小先原子写入 sidecar 日志并 fsync；
//   3. 再按“数据 → 头部 → truncate”顺序修改区域文件；
//   4. 最后删除日志并同步目录。
//
// 崩溃发生在任一步时：
//   - 日志尚未出现：区域文件保持旧版本；
//   - 日志存在：下次读取/保存先重放日志；重放是幂等的；
//   - 日志删除后：区域文件已是新版本。
//
// 日志不会被写到旧头部引用的扇区上，因此重放前旧区域始终可读。

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const (
	regionJournalSuffix = ".journal"
	regionJournalMagic  = "GMJ1"
	regionHeaderSize    = headerSectors * sectorSize
	// maxRegionJournalSize 防止损坏/恶意日志导致无界分配。
	maxRegionJournalSize = 256 << 20
	// maxRegionFileSize 是区域文件理论最大大小的保守上限（1 TiB），
	// 防止损坏日志让恢复 truncate 出异常巨大的文件。
	maxRegionFileSize = 1 << 40
	// maxRegionJournalRecord 是单条日志记录的最大长度（255 个扇区）。
	maxRegionJournalRecord  = 255 * sectorSize
	maxRegionJournalRecords = regionSize * regionSize
	regionLockCount         = 64
)

// regionWrite 是一条“绝对偏移 + 已按扇区对齐的数据”日志记录。
type regionWrite struct {
	offset int64
	data   []byte
}

// regionLocks 用固定数量的分片锁串行化同一区域文件的读者与写者。
// 分片可能碰撞，但只造成短暂额外等待，不会改变正确性；同时避免为
// 每个区域长期保存一个 mutex。
var regionLocks [regionLockCount]sync.RWMutex

func regionLock(path string) *sync.RWMutex {
	var hash uint32 = 2166136261
	for i := 0; i < len(path); i++ {
		hash ^= uint32(path[i])
		hash *= 16777619
	}
	return &regionLocks[hash%regionLockCount]
}

func regionJournalPath(path string) string { return path + regionJournalSuffix }

func regionHasJournal(path string) bool {
	_, err := os.Stat(regionJournalPath(path))
	return err == nil
}

func syncDir(path string) {
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
}

// writeRegionJournal 原子安装日志：先写 .journal.tmp，fsync 后 rename，
// 再同步目录。日志包含完整新头部、最终大小和所有新扇区数据。
func writeRegionJournal(path string, header []byte, finalSize int64, writes []regionWrite) error {
	if len(header) != regionHeaderSize {
		return fmt.Errorf("区域日志头部长度 %d 无效", len(header))
	}
	if finalSize < regionHeaderSize || finalSize%sectorSize != 0 || finalSize > maxRegionFileSize {
		return fmt.Errorf("区域日志最终大小 %d 无效", finalSize)
	}
	if len(writes) > maxRegionJournalRecords {
		return fmt.Errorf("区域日志记录数 %d 过多", len(writes))
	}
	totalSize := int64(4 + regionHeaderSize + 12 + 4)
	for _, item := range writes {
		totalSize += 12 + int64(len(item.data))
	}
	if totalSize > maxRegionJournalSize {
		return fmt.Errorf("区域日志过大：%d 字节（上限 %d）", totalSize, maxRegionJournalSize)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	journal := regionJournalPath(path)
	temp := journal + ".tmp"
	file, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	fail := func(err error) error {
		_ = file.Close()
		_ = os.Remove(temp)
		return err
	}
	writer := bufio.NewWriterSize(file, 16*1024)
	hash := crc32.NewIEEE()
	write := func(data []byte) error {
		if _, err := hash.Write(data); err != nil {
			return err
		}
		_, err := writer.Write(data)
		return err
	}
	if err := write([]byte(regionJournalMagic)); err != nil {
		return fail(err)
	}
	if err := write(header); err != nil {
		return fail(err)
	}
	var fixed [12]byte
	binary.BigEndian.PutUint64(fixed[:8], uint64(finalSize))
	binary.BigEndian.PutUint32(fixed[8:], uint32(len(writes)))
	if err := write(fixed[:]); err != nil {
		return fail(err)
	}
	for _, item := range writes {
		if item.offset < int64(regionHeaderSize) || item.offset%sectorSize != 0 {
			return fail(fmt.Errorf("区域日志写入偏移 %d 无效", item.offset))
		}
		if len(item.data) == 0 || len(item.data) > maxRegionJournalRecord || len(item.data)%sectorSize != 0 {
			return fail(fmt.Errorf("区域日志写入长度 %d 无效", len(item.data)))
		}
		var record [12]byte
		binary.BigEndian.PutUint64(record[:8], uint64(item.offset))
		binary.BigEndian.PutUint32(record[8:], uint32(len(item.data)))
		if err := write(record[:]); err != nil {
			return fail(err)
		}
		if err := write(item.data); err != nil {
			return fail(err)
		}
	}
	var checksum [4]byte
	binary.BigEndian.PutUint32(checksum[:], hash.Sum32())
	if _, err := writer.Write(checksum[:]); err != nil {
		return fail(err)
	}
	if err := writer.Flush(); err != nil {
		return fail(err)
	}
	if err := file.Sync(); err != nil {
		return fail(err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temp)
		return err
	}
	if err := os.Rename(temp, journal); err != nil {
		_ = os.Remove(temp)
		return err
	}
	syncDir(path)
	return nil
}

// readRegionJournal 读取并校验日志；任何结构/校验失败都拒绝重放。
func readRegionJournal(path string) (header []byte, finalSize int64, writes []regionWrite, err error) {
	file, err := os.Open(regionJournalPath(path))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, 0, nil, nil
		}
		return nil, 0, nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, 0, nil, err
	}
	if info.Size() > maxRegionJournalSize {
		return nil, 0, nil, fmt.Errorf("区域日志超过大小上限 %d", maxRegionJournalSize)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxRegionJournalSize+1))
	if err != nil {
		return nil, 0, nil, err
	}
	if len(data) > maxRegionJournalSize {
		return nil, 0, nil, fmt.Errorf("区域日志超过大小上限 %d", maxRegionJournalSize)
	}
	return decodeRegionJournal(data)
}

func decodeRegionJournal(data []byte) (header []byte, finalSize int64, writes []regionWrite, err error) {
	const fixedSize = 4 + regionHeaderSize + 12 + 4
	if len(data) < fixedSize {
		return nil, 0, nil, fmt.Errorf("区域日志截断")
	}
	if string(data[:4]) != regionJournalMagic {
		return nil, 0, nil, fmt.Errorf("区域日志 magic 无效")
	}
	expected := binary.BigEndian.Uint32(data[len(data)-4:])
	if crc32.ChecksumIEEE(data[:len(data)-4]) != expected {
		return nil, 0, nil, fmt.Errorf("区域日志校验和错误")
	}
	header = append([]byte(nil), data[4:4+regionHeaderSize]...)
	offset := 4 + regionHeaderSize
	finalSize = int64(binary.BigEndian.Uint64(data[offset : offset+8]))
	offset += 8
	count := binary.BigEndian.Uint32(data[offset : offset+4])
	offset += 4
	if finalSize < regionHeaderSize || finalSize%sectorSize != 0 || finalSize > maxRegionFileSize {
		return nil, 0, nil, fmt.Errorf("区域日志最终大小 %d 无效", finalSize)
	}
	if count > maxRegionJournalRecords {
		return nil, 0, nil, fmt.Errorf("区域日志记录数 %d 过多", count)
	}
	writes = make([]regionWrite, 0, count)
	for i := uint32(0); i < count; i++ {
		if len(data)-offset < 12+4 {
			return nil, 0, nil, fmt.Errorf("区域日志记录截断")
		}
		writeOffset := int64(binary.BigEndian.Uint64(data[offset : offset+8]))
		length := binary.BigEndian.Uint32(data[offset+8 : offset+12])
		offset += 12
		if writeOffset < int64(regionHeaderSize) || writeOffset%sectorSize != 0 {
			return nil, 0, nil, fmt.Errorf("区域日志写入偏移 %d 无效", writeOffset)
		}
		if length == 0 || length > maxRegionJournalRecord || length%sectorSize != 0 {
			return nil, 0, nil, fmt.Errorf("区域日志写入长度 %d 无效", length)
		}
		if int64(length) > int64(len(data)-offset-4) {
			return nil, 0, nil, fmt.Errorf("区域日志写入数据截断")
		}
		if writeOffset+int64(length) > finalSize {
			return nil, 0, nil, fmt.Errorf("区域日志写入超出最终大小")
		}
		copyData := append([]byte(nil), data[offset:offset+int(length)]...)
		offset += int(length)
		writes = append(writes, regionWrite{offset: writeOffset, data: copyData})
	}
	if offset != len(data)-4 {
		return nil, 0, nil, fmt.Errorf("区域日志尾部有 %d 字节多余数据", len(data)-4-offset)
	}
	return header, finalSize, writes, nil
}

func applyRegionJournal(path string, header []byte, finalSize int64, writes []regionWrite) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	fail := func(err error) error {
		_ = file.Close()
		return err
	}
	// 数据必须先落盘，再提交头部；旧头部在新头部提交前仍指向旧扇区。
	for _, item := range writes {
		if _, err := file.WriteAt(item.data, item.offset); err != nil {
			return fail(err)
		}
	}
	if err := file.Sync(); err != nil {
		return fail(err)
	}
	if _, err := file.WriteAt(header, 0); err != nil {
		return fail(err)
	}
	if err := file.Sync(); err != nil {
		return fail(err)
	}
	if err := file.Truncate(finalSize); err != nil {
		return fail(err)
	}
	if err := file.Sync(); err != nil {
		return fail(err)
	}
	if err := file.Close(); err != nil {
		return err
	}
	return nil
}

// recoverRegion 重放 region 目录中的 sidecar 日志；调用方必须持有该区域的
// 写分片锁。没有日志时是快速路径（一次 stat）。
func recoverRegion(path string) error {
	header, finalSize, writes, err := readRegionJournal(path)
	if err != nil {
		return fmt.Errorf("读取区域日志 %s: %w", path, err)
	}
	if header == nil {
		return nil
	}
	if _, _, err := scanRegionUsed(header, finalSize); err != nil {
		return fmt.Errorf("区域日志头部无效 %s: %w", path, err)
	}
	if err := applyRegionJournal(path, header, finalSize, writes); err != nil {
		return fmt.Errorf("重放区域日志 %s: %w", path, err)
	}
	if err := os.Remove(regionJournalPath(path)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("删除区域日志 %s: %w", path, err)
	}
	syncDir(path)
	dropRegion(path)
	return nil
}
