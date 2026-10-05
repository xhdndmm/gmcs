// Package world 包含 gmcs 的世界数据模型、地形生成、持久化与网络序列化。
package world

import (
	"fmt"

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

	sections [SectionCount]*section
}

// section 是一个 16×16×16 的方块段。
type section struct {
	// blocks 为 nil 时表示该 section 全部为空气；否则长度为 SectionVolume，
	// 索引 = y*256 + z*16 + x（section 内局部坐标）。
	blocks []uint16
	// biome 是该 section 统一的生物群系 ID（暂不支持 4×4×4 逐格生物群系）。
	biome uint16
}

// NewChunk 创建全空（空气）区块。
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
	if x < 0 || x >= SectionSize || z < 0 || z >= SectionSize {
		return AirBlock
	}
	sectionIndex, ok := SectionIndex(y)
	if !ok {
		return AirBlock
	}
	s := c.sections[sectionIndex]
	if s == nil || s.blocks == nil {
		return AirBlock
	}
	localY := y - (WorldMinY + sectionIndex*SectionSize)
	return s.blocks[blockIndex(x, localY, z)]
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
	s := c.sections[sectionIndex]
	if s == nil {
		s = &section{biome: BiomePlains}
		c.sections[sectionIndex] = s
	}
	if s.blocks == nil {
		if state == AirBlock {
			return // 未分配的全空气 section 保持 nil
		}
		s.blocks = make([]uint16, SectionVolume)
	}
	localY := y - (WorldMinY + sectionIndex*SectionSize)
	s.blocks[blockIndex(x, localY, z)] = state
}

// TopBlock 返回该列最高的非空气方块（包括水）的方块状态与 Y 坐标。
func (c *Chunk) TopBlock(x, z int) (uint16, int, bool) {
	for y := WorldMinY + WorldHeight - 1; y >= WorldMinY; y-- {
		if state := c.GetBlockState(x, y, z); state != AirBlock {
			return state, y, true
		}
	}
	return AirBlock, 0, false
}

// TopSolidY 返回该列最高的固体（非空气、非水）方块的 Y 坐标。
func (c *Chunk) TopSolidY(x, z int) (int, bool) {
	for y := WorldMinY + WorldHeight - 1; y >= WorldMinY; y-- {
		state := c.GetBlockState(x, y, z)
		if state != AirBlock && state != WaterBlock {
			return y, true
		}
	}
	return 0, false
}

// SectionBiome 返回指定 section 的生物群系 ID。
func (c *Chunk) SectionBiome(index int) uint16 {
	if index < 0 || index >= SectionCount || c.sections[index] == nil {
		return BiomePlains
	}
	return c.sections[index].biome
}

// SetSectionBiome 设置指定 section 的生物群系 ID。
func (c *Chunk) SetSectionBiome(index int, biomeID uint16) {
	if index < 0 || index >= SectionCount {
		return
	}
	if c.sections[index] == nil {
		c.sections[index] = &section{biome: biomeID}
		return
	}
	c.sections[index].biome = biomeID
}

// EncodeChunkDataPacket 按 1.21.11（协议 774）的格式序列化
// Chunk Data and Update Light 包。
//
// 行为：
//   - heightmaps 为空数组（wiki：客户端会以最小值初始化，不影响接受区块）；
//   - 方块状态使用调色板容器：单值（Bits Per Entry = 0）、间接调色板（4–8 位）
//     或全局调色板（15 位），采用 1.16+ 的紧密位流打包；
//   - 不包含方块实体；
//   - 天空光全亮（15）、方块光为空。
//
// chunkDataPacketCapacity 是 Chunk Data 包的预分配容量：区块数据数组
// 加上固定的全亮光照数据（约 53 KiB），避免 append 增长时的反复拷贝。
const chunkDataPacketCapacity = 64 * 1024

func EncodeChunkDataPacket(chunk *Chunk) []byte {
	packet := make([]byte, 0, chunkDataPacketCapacity)
	packet = protocol.AppendVarInt(packet, protocol.PlayPacketIDChunkData)
	packet = protocol.AppendInt32(packet, int32(chunk.X))
	packet = protocol.AppendInt32(packet, int32(chunk.Z))
	packet = protocol.AppendVarInt(packet, 0) // heightmaps 数量

	data := make([]byte, 0, 16*1024)
	for index := range chunk.sections {
		data = appendSection(data, chunk.sections[index])
	}
	packet = protocol.AppendVarInt(packet, int32(len(data)))
	packet = append(packet, data...)

	packet = protocol.AppendVarInt(packet, 0) // 方块实体数量

	return appendFullSkyLight(packet)
}

// appendSection 按 1.21.11 的 Chunk Section 结构追加一个 section：
// block count（i16）+ 方块状态调色板容器 + 生物群系调色板容器。
//
// 注意：fluid count 是 26.1 才加入的字段，1.21.11 没有该字段
// （参考 ViaVersion ChunkSectionType1_18 / ChunkSectionType26_1，并经实机验证）。
func appendSection(dst []byte, s *section) []byte {
	var blocks []uint16
	biome := uint16(BiomePlains)
	if s != nil {
		blocks = s.blocks
		biome = s.biome
	}
	blockCount := 0
	for _, state := range blocks {
		if state != AirBlock {
			blockCount++
		}
	}
	// Block count 为大端 short。
	dst = append(dst, byte(blockCount>>8), byte(blockCount))

	dst = appendBlockStates(dst, blocks)
	// 生物群系：单值调色板（BPE = 0 + VarInt 值，无数据数组）。
	dst = append(dst, 0x00)
	return protocol.AppendVarInt(dst, int32(biome))
}

// maxIndirectPalette 是间接调色板的最大条目数；超过时使用全局调色板（15 位）。
const maxIndirectPalette = 256

// appendBlockStates 追加方块状态调色板容器。
// blocks 为 nil 表示全空气 section。
//
// 1.21.5+ 的调色板格式：数据数组不带长度前缀（数量由位宽计算），
// 单值调色板只跟随一个 VarInt（无空数据数组）。
// 参考 ViaVersion PaletteType1_21_5。
//
// 性能：调色板用局部数组 + 线性查找（典型 ≤16 种方块），避免 map 与
// 中间索引数组的分配；全局调色板路径直接使用方块状态 ID。
func appendBlockStates(dst []byte, blocks []uint16) []byte {
	if blocks == nil {
		// 单值调色板：空气。
		dst = append(dst, 0x00)
		return protocol.AppendVarInt(dst, int32(AirBlock))
	}

	var paletteBuffer [maxIndirectPalette]uint16
	palette := paletteBuffer[:0]
	global := false
	for _, state := range blocks {
		found := false
		for _, existing := range palette {
			if existing == state {
				found = true
				break
			}
		}
		if found {
			continue
		}
		if len(palette) >= maxIndirectPalette {
			global = true
			break
		}
		palette = append(palette, state)
	}

	switch {
	case global:
		// 全局调色板：直接使用全局方块状态 ID（15 位、每 long 独立打包）。
		// 当前全部方块状态 ID < 32768（29670），如需更多请扩展位宽。
		const bits = 15
		dst = append(dst, byte(bits))
		return appendRawLongs(dst, packBitsPadded(blocks, bits))
	case len(palette) <= 1:
		// 单值调色板。
		state := AirBlock
		if len(palette) == 1 {
			state = palette[0]
		}
		dst = append(dst, 0x00)
		return protocol.AppendVarInt(dst, int32(state))
	default:
		// 间接调色板：位宽 4–8（方块调色板最小 4 位）。
		bits := bitsFor(len(palette))
		dst = append(dst, byte(bits))
		dst = protocol.AppendVarInt(dst, int32(len(palette)))
		for _, state := range palette {
			dst = protocol.AppendVarInt(dst, int32(state))
		}
		return appendRawLongs(dst, packIndirect(blocks, palette, bits))
	}
}

// appendRawLongs 追加 long 数组（无长度前缀）。
func appendRawLongs(dst []byte, longs []int64) []byte {
	for _, value := range longs {
		dst = protocol.AppendInt64(dst, value)
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

// packIndirect 把方块按调色板索引打包为紧密位流（允许跨 long 边界）。
// 索引通过调色板线性查找获得（典型 ≤16 种），避免生成中间索引数组。
func packIndirect(blocks []uint16, palette []uint16, bits int) []int64 {
	longs := make([]int64, (len(blocks)*bits+63)/64)
	position := 0
	for _, state := range blocks {
		value := 0
		for i, candidate := range palette {
			if candidate == state {
				value = i
				break
			}
		}
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

// packBits 按 1.16+ 的紧密位流把值打包为 long 数组（允许跨 long 边界）。
// 生产路径使用 packIndirect；此实现作为格式参考用于测试校验。
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

// packBitsPadded 按“每 long 独立”的方式打包（全局调色板，允许内部填充）。
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
