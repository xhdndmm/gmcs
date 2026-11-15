//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package main

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const (
	regionSize    = 32
	sectorSize    = 4096
	headerSectors = 2
	// maxRegionChunkPayload 限制单个区块解压后的 NBT 大小，防止压缩炸弹。
	maxRegionChunkPayload = 64 << 20
)

var zeroSector [sectorSize]byte

func writeZeroPadding(file *os.File, count int) error {
	for count > 0 {
		n := count
		if n > len(zeroSector) {
			n = len(zeroSector)
		}
		if _, err := file.Write(zeroSector[:n]); err != nil {
			return err
		}
		count -= n
	}
	return nil
}

type regionChunk struct {
	x, z int
	data []byte // 未压缩 NBT 或 gmcs 负载（由调用方解释）
}

// readRegion 返回区域文件中的全部非空区块（未解压）。
func readRegion(path string) (map[int][]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var header [headerSectors * sectorSize]byte
	if _, err := io.ReadFull(file, header[:]); err != nil {
		return nil, fmt.Errorf("读取区域头 %s: %w", path, err)
	}
	result := make(map[int][]byte)
	for slot := 0; slot < regionSize*regionSize; slot++ {
		entry := binary.BigEndian.Uint32(header[slot*4:])
		if entry == 0 {
			continue
		}
		sector := int64(entry>>8) * sectorSize
		count := int64(entry & 0xFF)
		if count == 0 {
			continue
		}
		var length [4]byte
		if _, err := file.ReadAt(length[:], sector); err != nil {
			return nil, err
		}
		total := int64(binary.BigEndian.Uint32(length[:]))
		if total < 2 || total > count*sectorSize-4 {
			return nil, fmt.Errorf("区域 %s 槽位 %d 长度越界", path, slot)
		}
		payload := make([]byte, total)
		if _, err := file.ReadAt(payload, sector+4); err != nil {
			return nil, err
		}
		compression := payload[0]
		raw := payload[1:]
		var data []byte
		switch compression {
		case 1:
			reader, err := gzip.NewReader(bytes.NewReader(raw))
			if err != nil {
				return nil, err
			}
			data, err = io.ReadAll(io.LimitReader(reader, maxRegionChunkPayload+1))
			_ = reader.Close()
			if err != nil {
				return nil, err
			}
		case 2:
			reader, err := zlib.NewReader(bytes.NewReader(raw))
			if err != nil {
				return nil, err
			}
			data, err = io.ReadAll(io.LimitReader(reader, maxRegionChunkPayload+1))
			_ = reader.Close()
			if err != nil {
				return nil, err
			}
		case 3:
			data = append([]byte(nil), raw...)
		default:
			return nil, fmt.Errorf("区域 %s 槽位 %d 压缩类型 %d 不受支持", path, slot, compression)
		}
		if len(data) > maxRegionChunkPayload {
			return nil, fmt.Errorf("区域 %s 槽位 %d 解压后超过上限 %d 字节", path, slot, maxRegionChunkPayload)
		}
		result[slot] = data
	}
	return result, nil
}

// writeRegion 写入一个完整区域文件；payloads 是未压缩 NBT/自定义负载。
func writeRegion(path string, payloads map[int][]byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp := path + ".tmp"
	file, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	fail := func(err error) error {
		_ = file.Close()
		_ = os.Remove(temp)
		return err
	}
	if _, err := file.Write(make([]byte, headerSectors*sectorSize)); err != nil {
		return fail(err)
	}
	var locations [regionSize * regionSize]uint32
	sectors := uint32(headerSectors)
	slots := make([]int, 0, len(payloads))
	for slot := range payloads {
		slots = append(slots, slot)
	}
	sort.Ints(slots)
	for _, slot := range slots {
		compressed, err := zlibCompress(payloads[slot])
		if err != nil {
			return fail(err)
		}
		total := 1 + len(compressed)
		sectorCount := (4 + total + sectorSize - 1) / sectorSize
		if sectorCount > 255 {
			return fail(fmt.Errorf("区域槽位 %d 负载过大", slot))
		}
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(total))
		if _, err := file.Write(length[:]); err != nil {
			return fail(err)
		}
		if _, err := file.Write([]byte{2}); err != nil {
			return fail(err)
		}
		if _, err := file.Write(compressed); err != nil {
			return fail(err)
		}
		if pad := sectorCount*sectorSize - 4 - total; pad > 0 {
			if err := writeZeroPadding(file, pad); err != nil {
				return fail(err)
			}
		}
		locations[slot] = sectors<<8 | uint32(sectorCount)
		sectors += uint32(sectorCount)
	}
	// 回填位置表与时间戳。
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fail(err)
	}
	for _, location := range locations {
		var raw [4]byte
		binary.BigEndian.PutUint32(raw[:], location)
		if _, err := file.Write(raw[:]); err != nil {
			return fail(err)
		}
	}
	var timestamps [regionSize * regionSize]byte
	if _, err := file.Write(timestamps[:]); err != nil {
		return fail(err)
	}
	if err := file.Sync(); err != nil {
		return fail(err)
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return err
	}
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

var (
	mapConvertBufferPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}
	mapConvertWriterPool = sync.Pool{New: func() any { return zlib.NewWriter(io.Discard) }}
)

// zlibCompress 复用压缩器与输出缓冲，批量转换时避免每个区块分配
// zlib.Writer 的窗口/哈希表。
func zlibCompress(data []byte) ([]byte, error) {
	buffer := mapConvertBufferPool.Get().(*bytes.Buffer)
	buffer.Reset()
	writer := mapConvertWriterPool.Get().(*zlib.Writer)
	writer.Reset(buffer)
	_, err := writer.Write(data)
	if err == nil {
		err = writer.Close()
	}
	mapConvertWriterPool.Put(writer)
	var compressed []byte
	if err == nil {
		compressed = append([]byte(nil), buffer.Bytes()...)
	}
	if buffer.Cap() > maxRegionChunkPayload {
		buffer = new(bytes.Buffer)
	}
	mapConvertBufferPool.Put(buffer)
	return compressed, err
}

// listRegions 按区域坐标列出目录中的 r.<x>.<z>.mca。
func listRegions(dir string) ([][2]int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	result := make([][2]int, 0)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "r.") || !strings.HasSuffix(name, ".mca") {
			continue
		}
		parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(name, "r."), ".mca"), ".")
		if len(parts) != 2 {
			continue
		}
		x, errX := strconv.Atoi(parts[0])
		z, errZ := strconv.Atoi(parts[1])
		if errX == nil && errZ == nil {
			result = append(result, [2]int{x, z})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i][1] != result[j][1] {
			return result[i][1] < result[j][1]
		}
		return result[i][0] < result[j][0]
	})
	return result, nil
}

func chunkCoords(regionX, regionZ, slot int) (int, int) {
	return regionX*regionSize + slot%regionSize, regionZ*regionSize + slot/regionSize
}

func regionFile(dir string, x, z int) string {
	return filepath.Join(dir, fmt.Sprintf("r.%d.%d.mca", x, z))
}

var errNoRegion = errors.New("区域目录不存在")
