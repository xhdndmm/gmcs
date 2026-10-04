package world

import "gmcs/internal/protocol"

const (
	GrassBlock = 2
	StoneBlock = 1
	AirBlock   = 0
)

type Chunk struct {
	X      int
	Z      int
	Blocks [16][16][256]uint16
}

func NewChunk(x, z int) *Chunk {
	return &Chunk{X: x, Z: z}
}

func (c *Chunk) SetBlock(x, y, z int, blockID uint16) {
	if x < 0 || x >= 16 || y < 0 || y >= 256 || z < 0 || z >= 16 {
		return
	}
	c.Blocks[x][z][y] = blockID
}

func (c *Chunk) BlockAt(x, y, z int) uint16 {
	if x < 0 || x >= 16 || y < 0 || y >= 256 || z < 0 || z >= 16 {
		return AirBlock
	}
	return c.Blocks[x][z][y]
}

func EncodeChunkDataPacket(chunk *Chunk) []byte {
	packet := protocol.AppendVarInt(nil, 0x20)
	packet = protocol.AppendVarInt(packet, int32(chunk.X))
	packet = protocol.AppendVarInt(packet, int32(chunk.Z))
	packet = protocol.AppendVarInt(packet, 1)
	packet = append(packet, 0x00, 0x00, 0x00)
	return packet
}
