package protocol

import "testing"

// BenchmarkEncodeEntityPositionSync 衡量实体位置同步包（高频生物移动）的编码成本。
func BenchmarkEncodeEntityPositionSync(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = EncodeEntityPositionSync(1, 1.5, 64, 2.5, 0, 0, 0, 90, 0, true)
	}
}

// BenchmarkEncodeAddEntity 衡量 Add Entity 包（含 LpVec3）的编码成本。
func BenchmarkEncodeAddEntity(b *testing.B) {
	uuid := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = EncodeAddEntity(1, uuid, 150, 1.5, 64, 2.5, 0.01, 0, -0.02, 90, 0)
	}
}
