// Package world 包含 gmcs 的世界数据模型、地形生成、持久化与网络序列化。
package world

import (
	"fmt"
	"sync"

	"gmcs/internal/protocol"
	"gmcs/internal/registry"
)

// 1.18+ 原版主世界的高度模型。
const (
	// WorldMinY 是世界最低方块的 Y 坐标。
	WorldMinY = -64
	// WorldHeight 是世界总高度（方块数）。
	WorldHeight = 384
	// SectionCount 是一个区块列包含的 section 数量。
	SectionCount = WorldHeight / 16
	// SectionSize 是一个 section 的边长（方块数）。
	SectionSize = 16
	// SectionVolume 是一个 section 的方块数（16³）。
	SectionVolume = SectionSize * SectionSize * SectionSize
)

// 常用方块的默认方块状态 ID，来自 registry 生成数据（客户端内置全局 ID）。
var (
	AirBlock       = mustBlockState("minecraft:air")
	StoneBlock     = mustBlockState("minecraft:stone")
	BedrockBlock   = mustBlockState("minecraft:bedrock")
	DirtBlock      = mustBlockState("minecraft:dirt")
	GrassBlock     = mustBlockState("minecraft:grass_block")
	SandBlock      = mustBlockState("minecraft:sand")
	WaterBlock     = mustBlockState("minecraft:water")
	OakLogBlock    = mustBlockState("minecraft:oak_log")
	OakLeavesBlock = mustBlockState("minecraft:oak_leaves")
)

// mustBlockState 查询方块默认状态 ID；生成数据缺少必需方块属于部署错误，立即失败。
func mustBlockState(name string) uint16 {
	id, err := registry.BlockStateID(name)
	if err != nil {
		panic(fmt.Sprintf("world: %v", err))
	}
	return id
}

// BiomePlains 是同步注册表中平原生物群系的 ID，与 tools/genregistries 生成的数据一致。
// 注意：世界数据中的生物群系必须使用同步注册表 ID，不能使用全局 biome 枚举。
const BiomePlains = registry.BiomePlainsID

// Chunk 是 16×16 的水平方块区域，横跨整个世界高度。
//
// 方块按 16×16×16 的 section 逐格存储；未分配的 section 表示全空气。
// Chunk 不做内部加锁：并发访问由上层（World）负责串行化。
type Chunk struct {
	X, Z int

	// mu 保护 sections：运行时方块修改（SetBlockState）会与方块查询、
	// 区块编码与保存等并发读取同时发生。
	mu       sync.RWMutex
	sections [SectionCount]*section
	// heights 是列高度缓存（惰性分配；SetBlockState 失效对应列）。
	heights *columnHeights
	// blockEntities 是方块实体数据（索引见 blockEntityIndex）；零值为空。
	blockEntities map[int]BlockEntity
}

// columnHeights 是惰性计算的列高度缓存。
// 值 = y - WorldMinY + 1；0 表示未计算，-1 表示该列没有对应方块。
type columnHeights struct {
	top   [SectionSize * SectionSize]int16
	solid [SectionSize * SectionSize]int16
}

// NewChunk 创建全空（空气）区块。
// section 及其紧凑方块存储见 section.go。
func NewChunk(x, z int) *Chunk {
	return &Chunk{X: x, Z: z}
}

// SectionIndex 返回给定方块 Y 坐标所在的 section 下标；
// Y 越界时第二个返回值为 false。
func SectionIndex(blockY int) (int, bool) {
	if blockY < WorldMinY || blockY >= WorldMinY+WorldHeight {
		return 0, false
	}
	return (blockY - WorldMinY) / SectionSize, true
}

// blockIndex 返回 section 内局部坐标的数组索引。
func blockIndex(x, y, z int) int {
	return (y << 8) | (z << 4) | x
}

// GetBlockState 返回世界坐标处的方块状态；越界或未分配返回空气。
func (c *Chunk) GetBlockState(x, y, z int) uint16 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.getBlockStateLocked(x, y, z)
}

// getBlockStateLocked 是 GetBlockState 的无锁实现；调用方必须持有 c.mu。
func (c *Chunk) getBlockStateLocked(x, y, z int) uint16 {
	if x < 0 || x >= SectionSize || z < 0 || z >= SectionSize {
		return AirBlock
	}
	sectionIndex, ok := SectionIndex(y)
	if !ok {
		return AirBlock
	}
	s := c.sections[sectionIndex]
	if s == nil {
		return AirBlock
	}
	localY := y - (WorldMinY + sectionIndex*SectionSize)
	return s.blockState(blockIndex(x, localY, z))
}

// SetBlockState 设置世界坐标处的方块状态；越界时忽略。
func (c *Chunk) SetBlockState(x, y, z int, state uint16) {
	if x < 0 || x >= SectionSize || z < 0 || z >= SectionSize {
		return
	}
	sectionIndex, ok := SectionIndex(y)
	if !ok {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.sections[sectionIndex]
	if s == nil {
		if state == AirBlock {
			return // 全空气 section 保持 nil
		}
		s = &section{biome: BiomePlains}
		c.sections[sectionIndex] = s
	}
	localY := y - (WorldMinY + sectionIndex*SectionSize)
	if !s.setBlock(blockIndex(x, localY, z), state) {
		return
	}
	if c.heights != nil {
		column := (z << 4) | x
		c.heights.top[column] = 0
		c.heights.solid[column] = 0
	}
}

// setBlockStateDirect 是生成期的无锁写入：调用方必须保证区块尚未发布
// （生成器正在构建一个新区块，其他 goroutine 不可见）。不维护列高度缓存
// （生成期缓存必然为 nil，无需失效）。
func (c *Chunk) setBlockStateDirect(x, y, z int, state uint16) {
	if x < 0 || x >= SectionSize || z < 0 || z >= SectionSize {
		return
	}
	sectionIndex, ok := SectionIndex(y)
	if !ok {
		return
	}
	s := c.sections[sectionIndex]
	if s == nil {
		if state == AirBlock {
			return
		}
		s = &section{biome: BiomePlains}
		c.sections[sectionIndex] = s
	}
	localY := y - (WorldMinY + sectionIndex*SectionSize)
	s.setBlock(blockIndex(x, localY, z), state)
}

// Column 描述一列方块的高度信息：一次查询即可获得原先 TopBlock 与
// TopSolidY 各自需要的结果，且结果按列缓存。
type Column struct {
	// TopState/TopY 是最高非空气方块（包括水）；HasTop 为 false 时无。
	TopState uint16
	TopY     int
	HasTop   bool
	// SolidY 是最高固体（非空气、非水）方块 Y；HasSolid 为 false 时无。
	SolidY   int
	HasSolid bool
}

// Column 返回列 (x, z) 的高度信息；结果按列缓存，方块修改后自动失效。
// 越界坐标返回空列。
func (c *Chunk) Column(x, z int) Column {
	if x < 0 || x >= SectionSize || z < 0 || z >= SectionSize {
		return Column{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.columnLocked(x, z)
}

// columnLocked 返回列信息，必要时计算并填充缓存。调用方必须持有写锁。
func (c *Chunk) columnLocked(x, z int) Column {
	if c.heights == nil {
		c.heights = &columnHeights{}
	}
	index := (z << 4) | x
	topValue := c.heights.top[index]
	solidValue := c.heights.solid[index]
	if topValue == 0 || solidValue == 0 {
		topValue, solidValue = c.computeColumnLocked(x, z)
		c.heights.top[index] = topValue
		c.heights.solid[index] = solidValue
	}
	column := Column{}
	if topValue > 0 {
		y := WorldMinY + int(topValue) - 1
		column.TopState = c.getBlockStateLocked(x, y, z)
		column.TopY = y
		column.HasTop = true
	}
	if solidValue > 0 {
		column.SolidY = WorldMinY + int(solidValue) - 1
		column.HasSolid = true
	}
	return column
}

// computeColumnLocked 从顶向下扫描一列，返回最高的非空气方块与最高的
// （非空气、非水）方块的编码值（y - WorldMinY + 1）；没有时返回 -1。
// 按 section 扫描并跳过未分配的 section（全空气）以减少遍历。
func (c *Chunk) computeColumnLocked(x, z int) (topValue, solidValue int16) {
	topValue, solidValue = -1, -1
	for sectionIndex := SectionCount - 1; sectionIndex >= 0; sectionIndex-- {
		s := c.sections[sectionIndex]
		if s == nil || s.storage.allAir() {
			continue
		}
		base := sectionIndex * SectionSize
		for localY := SectionSize - 1; localY >= 0; localY-- {
			state := s.blockState(blockIndex(x, localY, z))
			if state == AirBlock {
				continue
			}
			if topValue < 0 {
				topValue = int16(base + localY + 1)
			}
			if state != WaterBlock {
				solidValue = int16(base + localY + 1)
				return topValue, solidValue
			}
		}
	}
	return topValue, solidValue
}

// TopBlock 返回该列最高的非空气方块（包括水）的方块状态与 Y 坐标。
func (c *Chunk) TopBlock(x, z int) (uint16, int, bool) {
	column := c.Column(x, z)
	if !column.HasTop {
		return AirBlock, 0, false
	}
	return column.TopState, column.TopY, true
}

// TopSolidY 返回该列最高的固体（非空气、非水）方块的 Y 坐标。
func (c *Chunk) TopSolidY(x, z int) (int, bool) {
	column := c.Column(x, z)
	return column.SolidY, column.HasSolid
}

// SectionBiome 返回指定 section 的生物群系 ID。
func (c *Chunk) SectionBiome(index int) uint16 {
	if index < 0 || index >= SectionCount {
		return BiomePlains
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.sections[index] == nil {
		return BiomePlains
	}
	return c.sections[index].biome
}

// SetSectionBiome 设置指定 section 的生物群系 ID。
func (c *Chunk) SetSectionBiome(index int, biomeID uint16) {
	if index < 0 || index >= SectionCount {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sections[index] == nil {
		if biomeID == BiomePlains {
			return // nil 已表示默认生物群系
		}
		c.sections[index] = &section{biome: biomeID}
		return
	}
	c.sections[index].biome = biomeID
}

// EncodeChunkDataPacket 按 1.21.11（协议 774）的格式序列化
// Chunk Data and Update Light 包。
//
// 行为：
//   - heightmaps 发送 WORLD_SURFACE（1）与 MOTION_BLOCKING（4）：
//     9 位/列、每 long 7 个值、共 37 个 long（1.21.5+ 格式）；
//   - 方块状态使用调色板容器：单值（Bits Per Entry = 0）、间接调色板（4–8 位）
//     或全局调色板（15 位），采用 1.16+ 的紧密位流打包；
//   - 不包含方块实体；
//   - 天空光全亮（15）、方块光为空。
//
// chunkDataPacketCapacity 是 Chunk Data 包的预分配容量：区块数据数组
// 加上固定的全亮光照数据（约 53 KiB），避免 append 增长时的反复拷贝。
const chunkDataPacketCapacity = 64 * 1024

// EncodeChunkDataPacket 编码一个独立的 Chunk Data 包。
// blockEntityTypeID 是方块实体类型 ID（chunk 中的方块实体共用；
// 无方块实体时不会写入）。
func EncodeChunkDataPacket(chunk *Chunk) []byte {
	return AppendChunkDataPacket(make([]byte, 0, chunkDataPacketCapacity), chunk)
}

// chunkDataScratchPool 复用 section 数据缓冲（约 16 KB/次），
// 流式发送大量区块时避免每次分配。
var chunkDataScratchPool = sync.Pool{
	New: func() any {
		buffer := make([]byte, 0, 16*1024)
		return &buffer
	},
}

// AppendChunkDataPacket 把 Chunk Data 包追加到 dst 并返回。
// dst 容量足够（≥ chunkDataPacketCapacity）时无额外分配；
// 供区块流式发送复用同一缓冲区。
func AppendChunkDataPacket(dst []byte, chunk *Chunk) []byte {
	// 与运行时方块修改互斥：整个编码期间持有读锁。
	chunk.mu.RLock()
	defer chunk.mu.RUnlock()

	dst = protocol.AppendVarInt(dst, protocol.PlayPacketIDChunkData)
	dst = protocol.AppendInt32(dst, int32(chunk.X))
	dst = protocol.AppendInt32(dst, int32(chunk.Z))
	// heightmaps：WORLD_SURFACE（1）与 MOTION_BLOCKING（4）。本世界的方块
	// 非固体即流体，两者对“占用高度”的定义结果一致（列最高非空气方块），
	// 因此共用同一份打包数据（每 long 7 个值，共 37 个 long），
	// 直接从栈上数组打包输出，不分配中间 long 数组。
	var heights [SectionSize * SectionSize]uint16
	for columnZ := 0; columnZ < SectionSize; columnZ++ {
		for columnX := 0; columnX < SectionSize; columnX++ {
			heights[(columnZ<<4)|columnX] = chunk.topHeightLocked(columnX, columnZ)
		}
	}
	dst = protocol.AppendVarInt(dst, 2)
	dst = appendPackedHeightmap(dst, heightmapTypeWorldSurface, heights[:], heightmapBits)
	dst = appendPackedHeightmap(dst, heightmapTypeMotionBlocking, heights[:], heightmapBits)

	scratch := chunkDataScratchPool.Get().(*[]byte)
	data := (*scratch)[:0]
	for index := range chunk.sections {
		data = appendSection(data, chunk.sections[index])
	}
	dst = protocol.AppendVarInt(dst, int32(len(data)))
	dst = append(dst, data...)
	*scratch = data
	chunkDataScratchPool.Put(scratch)

	dst = chunk.appendBlockEntityPacketData(dst)

	return appendFullSkyLight(dst)
}

// heightmap 类型（1.21.11 的 mapper：0 world_surface_wg、1 world_surface、
// 2 ocean_floor_wg、3 ocean_floor、4 motion_blocking、5 motion_blocking_no_leaves）。
const (
	heightmapTypeWorldSurface   = 1
	heightmapTypeMotionBlocking = 4
	// heightmapBits 是 heightmap 值的位宽：ceil(log2(WorldHeight+1)) = 9。
	heightmapBits = 9
)

// appendPackedHeightmap 追加一个 heightmap：类型 + 带长度前缀的 long 数组。
// 值按“每 long 独立”的方式打包（每 long 7 个 9 位值、高位填充、不跨 long），
// 直接追加到 dst，不分配中间 long 数组。
func appendPackedHeightmap(dst []byte, kind int32, values []uint16, bits int) []byte {
	perLong := 64 / bits
	longCount := (len(values) + perLong - 1) / perLong
	dst = protocol.AppendVarInt(dst, kind)
	dst = protocol.AppendVarInt(dst, int32(longCount))
	var accumulator uint64
	for i, value := range values {
		slot := i % perLong
		accumulator |= uint64(value) << (uint(slot) * uint(bits))
		if slot == perLong-1 {
			dst = protocol.AppendInt64(dst, int64(accumulator))
			accumulator = 0
		}
	}
	if len(values)%perLong != 0 {
		dst = protocol.AppendInt64(dst, int64(accumulator))
	}
	return dst
}

// topHeightLocked 返回列 (x, z) 的 heightmap 编码值：最高非空气方块的
// y - WorldMinY + 1；全空列为 0。按 section 自上而下扫描并跳过未分配的
// section（全空气）。调用方必须持有 c.mu（读或写）。
func (c *Chunk) topHeightLocked(x, z int) uint16 {
	for sectionIndex := SectionCount - 1; sectionIndex >= 0; sectionIndex-- {
		s := c.sections[sectionIndex]
		if s == nil || s.storage.allAir() {
			continue
		}
		for localY := SectionSize - 1; localY >= 0; localY-- {
			if s.blockState(blockIndex(x, localY, z)) != AirBlock {
				return uint16(sectionIndex*SectionSize + localY + 1)
			}
		}
	}
	return 0
}

// appendSection 按 1.21.11 的 Chunk Section 结构追加一个 section：
// block count（i16）+ 方块状态调色板容器 + 生物群系调色板容器。
//
// 注意：fluid count 是 26.1 才加入的字段，1.21.11 没有该字段
// （参考 ViaVersion ChunkSectionType1_18 / ChunkSectionType26_1，并经实机验证）。
func appendSection(dst []byte, s *section) []byte {
	var (
		blockCount uint16
		biome      = uint16(BiomePlains)
		storage    *sectionStorage
	)
	if s != nil {
		blockCount = s.nonAir
		biome = s.biome
		storage = &s.storage
	}
	// Block count 为大端 short。
	dst = append(dst, byte(blockCount>>8), byte(blockCount))

	dst = appendBlockStates(dst, storage)
	// 生物群系：单值调色板（BPE = 0 + VarInt 值，无数据数组）。
	dst = append(dst, 0x00)
	return protocol.AppendVarInt(dst, int32(biome))
}

// appendBlockStates 追加方块状态调色板容器。
// storage 为 nil 或全空气表示全空气 section。
//
// 1.21.5+ 的调色板格式：数据数组不带长度前缀（数量由位宽计算），
// 单值调色板只跟随一个 VarInt（无空数据数组）。
// 参考 ViaVersion PaletteType1_21_5。
//
// 性能：直接从紧凑存储打包输出到 dst，不产生中间 long 数组，
// 也不做调色板线性查找（索引已存储在 section 中）。
// 调色板索引按网络位宽（4–8 位）重新紧密打包；
// 调色板超过 256 项（bits == 16）时使用全局调色板（15 位、每 long 独立）。
func appendBlockStates(dst []byte, storage *sectionStorage) []byte {
	if storage == nil || storage.bits == 0 {
		// 单值调色板。
		state := AirBlock
		if storage != nil {
			state = storage.uniform
		}
		dst = append(dst, 0x00)
		return protocol.AppendVarInt(dst, int32(state))
	}
	// 紧凑存储可能包含历史遗留的未使用条目（如被完全覆盖的空气）：
	// 打包条目全部相同时按单值调色板发送，省去 2 KiB 数据数组。
	if state, uniform := storage.uniformState(); uniform {
		dst = append(dst, 0x00)
		return protocol.AppendVarInt(dst, int32(state))
	}
	if storage.bits == 16 {
		// 全局调色板：直接使用全局方块状态 ID（15 位、每 long 独立打包，
		// 4 项/long）。当前全部方块状态 ID < 32768（29670），
		// 如需更多请扩展位宽。
		dst = append(dst, 15)
		for _, word := range storage.data {
			first := word & 0xFFFF
			second := (word >> 16) & 0xFFFF
			third := (word >> 32) & 0xFFFF
			fourth := word >> 48
			packed := (first & 0x7FFF) | (second&0x7FFF)<<15 | (third&0x7FFF)<<30 | (fourth&0x7FFF)<<45
			dst = protocol.AppendInt64(dst, int64(packed))
		}
		return dst
	}
	palette := storage.palette
	bits := bitsFor(len(palette))
	dst = append(dst, byte(bits))
	dst = protocol.AppendVarInt(dst, int32(len(palette)))
	for _, state := range palette {
		dst = protocol.AppendVarInt(dst, int32(state))
	}
	return appendPackedIndices(dst, storage, bits)
}

// appendPackedIndices 把紧凑存储中的调色板索引重新打包为网络位宽的紧密
// 位流（允许跨 long 边界，位宽 ≤ 8），直接追加到 dst，不分配中间数组。
func appendPackedIndices(dst []byte, storage *sectionStorage, bits int) []byte {
	sourceBits := int(storage.bits)
	mask := uint64(1)<<uint(sourceBits) - 1
	var (
		accumulator uint64
		accumBits   uint
	)
	for i := 0; i < SectionVolume; i++ {
		position := i * sourceBits
		value := (storage.data[position>>6] >> uint(position&63)) & mask
		accumulator |= value << accumBits
		accumBits += uint(bits)
		if accumBits >= 64 {
			dst = protocol.AppendInt64(dst, int64(accumulator))
			if overflow := accumBits - 64; overflow > 0 {
				accumulator = value >> (uint(bits) - overflow)
				accumBits = overflow
			} else {
				accumulator = 0
				accumBits = 0
			}
		}
	}
	if accumBits > 0 {
		dst = protocol.AppendInt64(dst, int64(accumulator))
	}
	return dst
}

// bitsFor 返回间接调色板的位宽：最小 4 位（与原版一致）、最大 8 位。
func bitsFor(paletteSize int) int {
	bits := 4
	for 1<<bits < paletteSize {
		bits++
	}
	return bits
}

// packBits 按 1.16+ 的紧密位流把值打包为 long 数组（允许跨 long 边界）。
// 生产路径为 appendPackedIndices；此实现作为格式参考用于测试校验。
func packBits(values []uint16, bits int) []int64 {
	longs := make([]int64, (len(values)*bits+63)/64)
	position := 0
	for _, value := range values {
		v := uint64(value)
		index := position >> 6
		offset := uint(position & 63)
		longs[index] |= int64(v << offset)
		if offset+uint(bits) > 64 {
			longs[index+1] |= int64(v >> (64 - offset))
		}
		position += bits
	}
	return longs
}

// packBitsPadded 按“每 long 独立”的方式打包（允许内部填充）。
// 生产路径为 appendPackedHeightmap 与 appendBlockStates 的全局调色板分支；
// 此实现作为格式参考用于测试校验。
func packBitsPadded(values []uint16, bits int) []int64 {
	perLong := 64 / bits
	longs := make([]int64, (len(values)+perLong-1)/perLong)
	for i, value := range values {
		index := i / perLong
		offset := uint(i%perLong) * uint(bits)
		longs[index] |= int64(uint64(value) << offset)
	}
	return longs
}

// lightValuesPerSection 是一个 section 光照数组的长度：4096 个值 × 半字节。
const lightValuesPerSection = 2048

var fullBrightSkyLight = func() []byte {
	buf := make([]byte, lightValuesPerSection)
	for i := range buf {
		buf[i] = 0xFF
	}
	return buf
}()

// appendFullSkyLight 追加 Light Data：为全部 section（含上下边缘共 SectionCount+2 位）
// 提供值为 15 的天空光，方块光与空掩码均为空。
func appendFullSkyLight(dst []byte) []byte {
	const lightSections = SectionCount + 2

	// Sky Light Mask：全部位置位。
	skyLightMask := int64(1)<<lightSections - 1
	dst = protocol.AppendVarInt(dst, 1) // BitSet 的 long 数量
	dst = protocol.AppendInt64(dst, skyLightMask)
	// Block Light Mask、Empty Sky Light Mask、Empty Block Light Mask：空。
	dst = protocol.AppendVarInt(dst, 0)
	dst = protocol.AppendVarInt(dst, 0)
	dst = protocol.AppendVarInt(dst, 0)
	// 天空光数组：每个置位一个 2048 字节的数组。
	dst = protocol.AppendVarInt(dst, lightSections)
	for i := 0; i < lightSections; i++ {
		dst = protocol.AppendVarInt(dst, lightValuesPerSection)
		dst = append(dst, fullBrightSkyLight...)
	}
	// 方块光数组：空。
	dst = protocol.AppendVarInt(dst, 0)
	return dst
}
