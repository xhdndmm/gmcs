package world

import "testing"

// benchSink 防止编译器把未使用返回值的编码调用当作死代码消除。
var benchSink []byte

// BenchmarkGenerateChunk 衡量种子地形生成的单区块成本（含树木）。
func BenchmarkGenerateChunk(b *testing.B) {
	generator := SeededGenerator{Seed: 42}
	chunkX := 0
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = generator.GenerateChunk(chunkX, 0)
		chunkX++ // 变换坐标，避免重复测量同一区块
	}
}

// BenchmarkEncodeChunkDataPacket 衡量区块数据包（含调色板与光照）的编码成本。
func BenchmarkEncodeChunkDataPacket(b *testing.B) {
	chunk := SeededGenerator{Seed: 42}.GenerateChunk(0, 0)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		benchSink = EncodeChunkDataPacket(chunk)
	}
}

// BenchmarkAppendChunkDataPacketReuse 衡量复用输出缓冲的流式编码成本
// （对应进入世界/移动时连续发送多个区块的实际路径）。
func BenchmarkAppendChunkDataPacketReuse(b *testing.B) {
	chunk := SeededGenerator{Seed: 42}.GenerateChunk(0, 0)
	buffer := make([]byte, 0, chunkDataPacketCapacity)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		buffer = AppendChunkDataPacket(buffer[:0], chunk)
	}
	benchSink = buffer
}

// BenchmarkEncodeChunkPayload 衡量区块存储负载（未压缩）的编码成本，
// 对应 Flush/UnloadFar 的保存路径。
func BenchmarkEncodeChunkPayload(b *testing.B) {
	chunk := SeededGenerator{Seed: 42}.GenerateChunk(0, 0)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = encodeChunkPayload(chunk)
	}
}

// BenchmarkEncodeCompressedChunkPayload 衡量区块存储负载（zlib 压缩）的编码成本
// （对象池复用，对应实际保存路径）。
func BenchmarkEncodeCompressedChunkPayload(b *testing.B) {
	chunk := SeededGenerator{Seed: 42}.GenerateChunk(0, 0)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := encodeCompressedChunkPayload(chunk); err != nil {
			b.Fatal(err)
		}
	}
}
