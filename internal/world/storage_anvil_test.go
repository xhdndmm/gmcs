//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package world

import "testing"

// TestBlockEntityAnvilRoundTrip 验证槽位耐久、铁砧累积惩罚与自定义名称随区块
// 负载持久化（存储格式 v6）。
func TestBlockEntityAnvilRoundTrip(t *testing.T) {
	chunk := FlatGenerator{}.GenerateChunk(0, 0)
	entity := BlockEntity{
		TypeID: 1,
		Items: []ContainerItem{
			{ItemID: 913, Count: 1, Damage: 100, RepairCost: 5, CustomName: "Excalibur"},
			{ItemID: 913, Count: 1, PotionID: 4},
			{ItemID: 1, Count: 64},
		},
	}
	chunk.SetBlockEntity(3, FlatGroundLevel+1, 4, entity)

	payload := encodeChunkPayload(chunk)
	decoded, err := decodeChunkPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := decoded.BlockEntityAt(3, FlatGroundLevel+1, 4)
	if !ok {
		t.Fatal("方块实体丢失")
	}
	if len(got.Items) != 3 {
		t.Fatalf("槽位数 = %d, want 3", len(got.Items))
	}
	first := got.Items[0]
	if first.Damage != 100 || first.RepairCost != 5 || first.CustomName != "Excalibur" {
		t.Fatalf("槽位 0 组件丢失：%+v", first)
	}
	second := got.Items[1]
	if second.PotionID != 4 || second.Damage != 0 || second.RepairCost != 0 || second.CustomName != "" {
		t.Fatalf("槽位 1 数据不符：%+v", second)
	}
	if got.Items[2].Count != 64 {
		t.Fatalf("槽位 2 数量丢失：%+v", got.Items[2])
	}
}
