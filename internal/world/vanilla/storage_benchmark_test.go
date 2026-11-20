// [...]
package vanilla_test

import (
	"testing"

	"gmcs/internal/world"
)

var storageBenchSink []byte

// BenchmarkEncodeGeneratedChunkPayload 衡量真实主世界区块的 v13 存储编码，
// 与 BenchmarkEncodeChunkPayload（超平坦测试区块）互补。
func BenchmarkEncodeGeneratedChunkPayload(b *testing.B) {
	generator, err := world.NewVanillaGenerator(1234)
	if err != nil {
		b.Fatal(err)
	}
	chunk := generator.GenerateChunk(62, 62)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		storageBenchSink = world.EncodeChunkPayload(chunk)
	}
}

// BenchmarkCompressGeneratedChunkPayload 衡量真实主世界区块的 zlib 存储压缩。
func BenchmarkCompressGeneratedChunkPayload(b *testing.B) {
	generator, err := world.NewVanillaGenerator(1234)
	if err != nil {
		b.Fatal(err)
	}
	chunk := generator.GenerateChunk(62, 62)
	payload := world.EncodeChunkPayload(chunk)
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		storageBenchSink, err = world.CompressChunkPayload(payload)
		if err != nil {
			b.Fatal(err)
		}
	}
}
