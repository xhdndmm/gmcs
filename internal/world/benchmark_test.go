//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

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

// BenchmarkFindPath 衡量 A* 寻路的单次成本：x = 5 处有一堵 z ≤ 10 的墙，
// 起点 (0,0,0) 到目标 (10,0,0) 必须绕到 z = 11 的缺口（约 32 步），
// 覆盖网格查询、节点扩展、松弛与路径重建。
func BenchmarkFindPath(b *testing.B) {
	grid := func(x, z int) (int, bool) {
		if x == 5 && z <= 10 {
			return 0, false
		}
		return 64, true
	}
	from := PathNode{X: 0, Y: 64, Z: 0}
	goal := PathNode{X: 10, Y: 64, Z: 0}
	limits := DefaultPathLimits()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		path := FindPath(grid, from, goal, limits)
		if len(path) == 0 {
			b.Fatal("未找到路径")
		}
	}
}

// BenchmarkFindPathUnreachable 衡量无路径时的最坏情形成本：目标被完全
// 围住，搜索耗尽 MaxNodes 节点后返回 nil。
func BenchmarkFindPathUnreachable(b *testing.B) {
	grid := func(x, z int) (int, bool) {
		if x == 20 && z == 20 {
			return 64, true
		}
		if (x >= 19 && x <= 21) && (z >= 19 && z <= 21) {
			return 0, false // 内圈围死
		}
		if (x >= 18 && x <= 22) && (z >= 18 && z <= 22) {
			return 64, true // 外圈可走（可接近但到不了目标）
		}
		return 64, true
	}
	from := PathNode{X: 0, Y: 64, Z: 0}
	goal := PathNode{X: 20, Y: 64, Z: 20}
	limits := DefaultPathLimits()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if path := FindPath(grid, from, goal, limits); path != nil {
			b.Fatalf("不应找到路径，得到 %d 步", len(path))
		}
	}
}
