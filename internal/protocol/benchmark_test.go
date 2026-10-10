package protocol

import (
	"io"
	"testing"
)

// BenchmarkEncodeEntityPositionSync 衡量实体位置同步包（高频生物移动）的编码成本。
func BenchmarkEncodeEntityPositionSync(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_ = EncodeEntityPositionSync(1, 1.5, 64, 2.5, 0, 0, 0, 90, 0, true)
	}
}

// BenchmarkEncodeAddEntity 衡量 Add Entity 包（含 LpVec3）的编码成本。
func BenchmarkEncodeAddEntity(b *testing.B) {
	uuid := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	b.ReportAllocs()
	for b.Loop() {
		_ = EncodeAddEntity(1, uuid, 150, 1.5, 64, 2.5, 0.01, 0, -0.02, 90, 0)
	}
}

// BenchmarkWritePacketWithCompression 衡量大包（区块级，约 60 KB）的压缩
// 发送路径：数据长度前缀 + zlib 压缩 + 帧写出。
func BenchmarkWritePacketWithCompression(b *testing.B) {
	packet := make([]byte, 60000)
	for i := range packet {
		packet[i] = byte(i % 7)
	}
	b.SetBytes(int64(len(packet)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := WritePacketWithCompression(io.Discard, packet, 256); err != nil {
			b.Fatal(err)
		}
	}
}
