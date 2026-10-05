package world

import "testing"

// BenchmarkGenerateChunk 衡量种子地形生成的单区块成本（含树木）。
func BenchmarkGenerateChunk(b *testing.B) {
	generator := SeededGenerator{Seed: 42}
	chunkX := 0
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = generator.GenerateChunk(chunkX, 0)
		chunkX++ // 变换坐标，避免重复测量同一区块
	}
}

// BenchmarkEncodeChunkDataPacket 衡量区块数据包（含调色板与光照）的编码成本。
func BenchmarkEncodeChunkDataPacket(b *testing.B) {
	chunk := SeededGenerator{Seed: 42}.GenerateChunk(0, 0)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = EncodeChunkDataPacket(chunk)
	}
}

// BenchmarkAppendChunkDataPacketReuse 衡量复用输出缓冲的流式编码成本
// （对应进入世界/移动时连续发送多个区块的实际路径）。
func BenchmarkAppendChunkDataPacketReuse(b *testing.B) {
	chunk := SeededGenerator{Seed: 42}.GenerateChunk(0, 0)
	buffer := make([]byte, 0, chunkDataPacketCapacity)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buffer = AppendChunkDataPacket(buffer[:0], chunk)
	}
}
