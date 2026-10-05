package world

import (
	"math/rand"
	"testing"

	"gmcs/internal/protocol"
)

// TestSectionStorageSequence 用确定性伪随机序列校验紧凑存储的
// 写入/读取、位宽增长（1→2→4→8→16）与清空回退。
func TestSectionStorageSequence(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	section := &section{}
	want := make([]uint16, SectionVolume) // 0 即空气
	for step := 0; step < 300000; step++ {
		index := rng.Intn(SectionVolume)
		state := uint16(rng.Intn(400)) // 400 种状态会依次触发全部位宽增长
		if state == 0 {
			state = 1
		}
		previous := want[index]
		if changed := section.setBlock(index, state); changed != (previous != state) {
			t.Fatalf("step %d: setBlock 返回 %v，期望 %v", step, changed, previous != state)
		}
		want[index] = state
	}
	for i := range want {
		if got := section.blockState(i); got != want[i] {
			t.Fatalf("index %d: got %d, want %d", i, got, want[i])
		}
	}
	if section.nonAir != SectionVolume {
		t.Fatalf("nonAir = %d, want %d", section.nonAir, SectionVolume)
	}
	// 重复写入相同值不应变化。
	if section.setBlock(0, want[0]) {
		t.Fatal("重复写入相同值应返回 false")
	}
	// 逐个清空：应回退为零值全空气存储并释放数组。
	for i := 0; i < SectionVolume; i++ {
		if !section.setBlock(i, AirBlock) {
			t.Fatalf("index %d: 清空应报告变化", i)
		}
	}
	if !section.storage.allAir() || section.nonAir != 0 {
		t.Fatalf("清空后 allAir=%v nonAir=%d", section.storage.allAir(), section.nonAir)
	}
	if section.storage.data != nil || section.storage.palette != nil {
		t.Fatal("清空后应释放位流与调色板")
	}
}

// TestSectionStorageUniformNetwork 校验“所有位置同一种方块”的 section
// 在网络编码时走单值调色板：紧凑存储可能保留未使用的历史条目
// （例如升级调色板时写入的空气），不能据此退化为 2 KiB 的调色板数据。
func TestSectionStorageUniformNetwork(t *testing.T) {
	section := &section{}
	for i := 0; i < SectionVolume; i++ {
		section.setBlock(i, StoneBlock)
	}
	data := appendBlockStates(nil, &section.storage)
	if data[0] != 0x00 {
		t.Fatalf("期望单值调色板，实际 BPE=%d", data[0])
	}
	state, consumed, err := protocol.DecodeVarInt(data[1:])
	if err != nil || state != int32(StoneBlock) || 1+consumed != len(data) {
		t.Fatalf("state=%d err=%v len=%d", state, err, len(data))
	}
}

// TestSectionStorageUniform 校验 uniform 存储（含非空气统一状态）的读写。
func TestSectionStorageUniform(t *testing.T) {
	section := &section{}
	section.setBlock(5, StoneBlock)
	section.setBlock(6, StoneBlock)
	section.setBlock(7, StoneBlock)
	// 移除两个后仍剩一个（palette 存储），全部移除后回退 uniform。
	section.setBlock(5, AirBlock)
	section.setBlock(6, AirBlock)
	if got := section.blockState(7); got != StoneBlock || section.nonAir != 1 {
		t.Fatalf("got %d nonAir=%d，期望 1 个石头", got, section.nonAir)
	}
	section.setBlock(7, AirBlock)
	if !section.storage.allAir() {
		t.Fatal("全部清空后应为全空气存储")
	}
}
