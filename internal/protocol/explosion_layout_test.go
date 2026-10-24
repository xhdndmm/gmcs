//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package protocol

import (
	"math"
	"testing"
)

// TestEncodeExplosionLayout 逐字段校验 Explode 包的字节布局。
//
// 字段表来自 1.21.11 客户端 ClientboundExplodePacket 的 STREAM_CODEC
// （7 个字段，按序）：
//
//	center            Vec3（f64 × 3）
//	power             f32
//	blockCount        i32（定长 4 字节，不是 VarInt）
//	playerKnockback   optional<Vec3>（此处 0x00 表示无击退）
//	explosionParticle VarInt（ParticleTypes.STREAM_CODEC）
//	explosionSound    VarInt（Holder<SoundEvent>：注册表 ID + 1）
//	blockParticles    VarInt 数量（List<ExplosionParticleInfo>）
func TestEncodeExplosionLayout(t *testing.T) {
	packet := EncodeExplosion(1.5, 64.25, -8.75, 4, 123, ParticleExplosionEmitter)

	offset := 0
	read := func(size int) []byte {
		t.Helper()
		if offset+size > len(packet) {
			t.Fatalf("包长度不足：需要 %d 字节，实际 %d", offset+size, len(packet))
		}
		out := packet[offset : offset+size]
		offset += size
		return out
	}
	readFloat64 := func() float64 {
		t.Helper()
		b := read(8)
		var bits uint64
		for _, v := range b {
			bits = bits<<8 | uint64(v)
		}
		return math.Float64frombits(bits)
	}
	readFloat32 := func() float32 {
		t.Helper()
		b := read(4)
		var bits uint32
		for _, v := range b {
			bits = bits<<8 | uint32(v)
		}
		return math.Float32frombits(bits)
	}
	readInt32 := func() int32 {
		t.Helper()
		b := read(4)
		var v int32
		for _, x := range b {
			v = v<<8 | int32(x)
		}
		return v
	}
	readVarInt := func() int32 {
		t.Helper()
		value, size, err := DecodeVarInt(packet[offset:])
		if err != nil {
			t.Fatalf("VarInt 解码失败：%v", err)
		}
		offset += size
		return value
	}

	if id := readVarInt(); id != int32(PlayPacketIDExplosion) {
		t.Fatalf("包 ID = %#x, want %#x", id, PlayPacketIDExplosion)
	}
	if got := readFloat64(); got != 1.5 {
		t.Fatalf("center.x = %v, want 1.5", got)
	}
	if got := readFloat64(); got != 64.25 {
		t.Fatalf("center.y = %v, want 64.25", got)
	}
	if got := readFloat64(); got != -8.75 {
		t.Fatalf("center.z = %v, want -8.75", got)
	}
	if got := readFloat32(); got != 4 {
		t.Fatalf("power = %v, want 4", got)
	}
	if got := readInt32(); got != 123 {
		t.Fatalf("blockCount = %d, want 123（i32 定长 4 字节）", got)
	}
	if got := read(1)[0]; got != 0x00 {
		t.Fatalf("playerKnockback 选项 = %#x, want 0x00", got)
	}
	if got := readVarInt(); got != ParticleExplosionEmitter {
		t.Fatalf("explosionParticle = %d, want %d", got, ParticleExplosionEmitter)
	}
	if got := readVarInt(); got != soundEntityGenericExplode+1 {
		t.Fatalf("explosionSound Holder = %d, want %d", got, soundEntityGenericExplode+1)
	}
	if got := readVarInt(); got != 0 {
		t.Fatalf("blockParticles 数量 = %d, want 0", got)
	}
	if offset != len(packet) {
		t.Fatalf("包尾多出 %d 字节（客户端会忽略，但布局已偏离协议）", len(packet)-offset)
	}
}
