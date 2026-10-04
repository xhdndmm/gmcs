// Package world 包含 gmcs 的世界数据模型与网络序列化。
package world

import (
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
)

// 1.21.11 全局方块状态（block state）ID 中的常用值。
// 来源：Minecraft 内置数据生成器报告（经 minecraft-data 1.21.11 blocks.json 核对）。
const (
	AirBlock   = 0
	StoneBlock = 1
)

// BiomePlains 是同步注册表中平原生物群系的 ID，与 tools/genregistries 生成的数据一致。
// 注意：世界数据中的生物群系必须使用同步注册表 ID，不能使用全局 biome 枚举。
const BiomePlains = registry.BiomePlainsID

// PlatformTopY 是 NewFlatChunk 平台表面上方一格（玩家脚部）的 Y 坐标。
const PlatformTopY = WorldMinY + 16

// Chunk 是 16×16 的水平方块区域，横跨整个世界高度。
//
// 当前实现为简化模型：每个 section 只能存放单一方块状态，无法表达逐格混合的
// section。逐格存储与调色板压缩将在后续版本实现。
type Chunk struct {
	X, Z int

	sections [SectionCount]section
}

type section struct {
	blockState uint16
	biomeID    uint16
	filled     bool
}

// NewChunk 创建全空（空气）区块，所有 section 的生物群系为平原。
func NewChunk(x, z int) *Chunk {
	chunk := &Chunk{X: x, Z: z}
	for i := range chunk.sections {
		chunk.sections[i].biomeID = BiomePlains
	}
	return chunk
}

// NewFlatChunk 创建当前开发阶段的临时地形：最底部 section 全部为石头，其余为空气。
func NewFlatChunk(x, z int) *Chunk {
	chunk := NewChunk(x, z)
	chunk.SetSection(0, StoneBlock)
	return chunk
}

// SectionIndex 返回给定方块 Y 坐标所在的 section 下标。
func SectionIndex(blockY int) int {
	return (blockY - WorldMinY) / 16
}

// SetSection 用统一方块状态填充指定 section。
func (c *Chunk) SetSection(index int, blockState uint16) {
	if index < 0 || index >= SectionCount {
		return
	}
	c.sections[index] = section{blockState: blockState, biomeID: c.sections[index].biomeID, filled: true}
}

// SetSectionBiome 设置指定 section 的生物群系 ID。
func (c *Chunk) SetSectionBiome(index int, biomeID uint16) {
	if index < 0 || index >= SectionCount {
		return
	}
	c.sections[index].biomeID = biomeID
}

// SectionBlock 返回 section 的统一方块状态；未填充的 section 返回空气。
func (c *Chunk) SectionBlock(index int) uint16 {
	if index < 0 || index >= SectionCount || !c.sections[index].filled {
		return AirBlock
	}
	return c.sections[index].blockState
}

// EncodeChunkDataPacket 按 1.21.11（协议 774）的格式序列化
// Chunk Data and Update Light 包。
//
// 当前行为：
//   - heightmaps 为空数组（wiki：客户端会以最小值初始化，不影响接受区块）；
//   - 不包含方块实体；
//   - 天空光全亮（15）、方块光为空，适合平台/虚空世界。
func EncodeChunkDataPacket(chunk *Chunk) []byte {
	packet := protocol.AppendVarInt(nil, protocol.PlayPacketIDChunkData)
	packet = protocol.AppendInt32(packet, int32(chunk.X))
	packet = protocol.AppendInt32(packet, int32(chunk.Z))
	packet = protocol.AppendVarInt(packet, 0) // heightmaps 数量

	data := make([]byte, 0, SectionCount*4)
	for i := range chunk.sections {
		data = appendSection(data, &chunk.sections[i])
	}
	packet = protocol.AppendVarInt(packet, int32(len(data)))
	packet = append(packet, data...)

	packet = protocol.AppendVarInt(packet, 0) // 方块实体数量

	return appendFullSkyLight(packet)
}

// appendSection 按 1.21.5+ 的 Chunk Section 结构追加一个 section：
// block count（i16）、fluid count（i16）、方块状态调色板容器、生物群系调色板容器。
// 当前仅使用单值调色板（Bits Per Entry = 0 + VarInt 值）。
func appendSection(dst []byte, s *section) []byte {
	blockState := s.blockState
	if !s.filled {
		blockState = AirBlock
	}
	blockCount := int16(0)
	if blockState != AirBlock {
		blockCount = 4096
	}
	// Block count 与 fluid count 均为大端 short。
	dst = append(dst, byte(blockCount>>8), byte(blockCount))
	dst = append(dst, 0, 0)
	// 方块状态：单值调色板。
	dst = append(dst, 0x00)
	dst = protocol.AppendVarInt(dst, int32(blockState))
	// 生物群系：单值调色板。
	dst = append(dst, 0x00)
	dst = protocol.AppendVarInt(dst, int32(s.biomeID))
	return dst
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
