//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package world

import "testing"

// TestBlockEntityTransferCooldownRoundTrip 验证漏斗传送冷却随区块负载持久化
// （存储格式 v9：方块实体追加 transferCooldown 字段）。
func TestBlockEntityTransferCooldownRoundTrip(t *testing.T) {
	chunk := FlatGenerator{}.GenerateChunk(0, 0)
	hopper := BlockEntity{
		TypeID:           64,
		Items:            []ContainerItem{{ItemID: 100, Count: 3}},
		TransferCooldown: 7,
	}
	chunk.SetBlockEntity(3, FlatGroundLevel+1, 4, hopper)

	decoded, err := decodeChunkPayload(encodeChunkPayload(chunk))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := decoded.BlockEntityAt(3, FlatGroundLevel+1, 4)
	if !ok {
		t.Fatal("方块实体丢失")
	}
	if got.TransferCooldown != 7 {
		t.Fatalf("传送冷却 = %d, want 7", got.TransferCooldown)
	}
	if len(got.Items) != 1 || got.Items[0].ItemID != 100 || got.Items[0].Count != 3 {
		t.Fatalf("槽位丢失：%+v", got.Items)
	}

	// 仅剩冷却（槽位全空）时实体不应被视为空：原版漏斗方块实体始终存在，
	// 冷却需要写盘才能在重启后恢复节流。
	empty := BlockEntity{TransferCooldown: 1}
	if empty.Empty() {
		t.Fatal("有传送冷却的方块实体不应为空")
	}
	if !(BlockEntity{}).Empty() {
		t.Fatal("全默认字段的方块实体应为空")
	}
}

// TestSetTransferCooldownInPlace 验证就地更新冷却：只改该字段、不重建槽位切片，
// 记录不存在时不修改，更新后为空则删除记录。
func TestSetTransferCooldownInPlace(t *testing.T) {
	chunk := FlatGenerator{}.GenerateChunk(0, 0)
	items := []ContainerItem{{ItemID: 100, Count: 3}}
	chunk.SetBlockEntity(1, FlatGroundLevel+1, 1, BlockEntity{TypeID: 7, Items: items})

	if !chunk.SetTransferCooldown(1, FlatGroundLevel+1, 1, 5) {
		t.Fatal("就地更新应报告已修改")
	}
	got, _ := chunk.BlockEntityAt(1, FlatGroundLevel+1, 1)
	if got.TransferCooldown != 5 || len(got.Items) != 1 || got.Items[0].ItemID != 100 {
		t.Fatalf("就地更新结果错误：%+v", got)
	}

	// 记录不存在（空坐标）时不修改。
	if chunk.SetTransferCooldown(2, FlatGroundLevel+1, 1, 5) {
		t.Fatal("不存在的记录不应报告已修改")
	}

	// 更新后为空 → 删除记录（空漏斗只保留冷却字段，归零后无内容可存）。
	chunk.SetBlockEntity(4, FlatGroundLevel+1, 4, BlockEntity{TypeID: 64, TransferCooldown: 3})
	if !chunk.SetTransferCooldown(4, FlatGroundLevel+1, 4, 0) {
		t.Fatal("就地更新应报告已修改")
	}
	if _, ok := chunk.BlockEntityAt(4, FlatGroundLevel+1, 4); ok {
		t.Fatal("空记录应被删除")
	}

	// 记录不存在时不创建新记录（槽位为空且冷却归零时无内容可存）。
	if chunk.SetTransferCooldown(3, FlatGroundLevel+1, 1, 4) {
		t.Fatal("不存在的记录不应被创建")
	}
}
