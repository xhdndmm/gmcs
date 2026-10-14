//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package world

import "testing"

// TestBlockEntityBrewingRoundTrip 验证酿造台进度与槽位药水 ID 随区块负载持久化
// （存储格式 v5：槽位追加 potionID，进度追加 brewTime/brewFuel）。
func TestBlockEntityBrewingRoundTrip(t *testing.T) {
	chunk := FlatGenerator{}.GenerateChunk(0, 0)
	brewing := BlockEntity{
		TypeID: 385,
		Items: []ContainerItem{
			{ItemID: 1126, Count: 1, PotionID: 4}, // 粗制药水
			{},
			{ItemID: 1126, Count: 1}, // 水瓶（无组件）
			{ItemID: 100, Count: 3},  // 材料
			{ItemID: 101, Count: 2},  // 燃料
		},
		BrewTime: 123,
		BrewFuel: 7,
	}
	chunk.SetBlockEntity(5, FlatGroundLevel+1, 6, brewing)

	payload := encodeChunkPayload(chunk)
	decoded, err := decodeChunkPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := decoded.BlockEntityAt(5, FlatGroundLevel+1, 6)
	if !ok {
		t.Fatal("方块实体丢失")
	}
	if got.BrewTime != 123 || got.BrewFuel != 7 {
		t.Fatalf("酿造进度丢失：%+v", got)
	}
	if len(got.Items) != 5 {
		t.Fatalf("槽位数 = %d, want 5", len(got.Items))
	}
	if got.Items[0].PotionID != 4 || got.Items[0].ItemID != 1126 {
		t.Fatalf("槽位药水 ID 丢失：%+v", got.Items[0])
	}
	if got.Items[2].PotionID != 0 {
		t.Fatalf("无组件的槽位不应有药水 ID：%+v", got.Items[2])
	}
	if got.Items[3].ItemID != 100 || got.Items[3].Count != 3 {
		t.Fatalf("材料槽丢失：%+v", got.Items[3])
	}

	// 仅剩进度（槽位全空）时实体不应被视为空。
	empty := BlockEntity{BrewTime: 1}
	if empty.Empty() {
		t.Fatal("有酿造进度的方块实体不应为空")
	}
	empty = BlockEntity{BrewFuel: 1}
	if empty.Empty() {
		t.Fatal("有燃料的方块实体不应为空")
	}
}

// TestBlockEntityFurnacePotionSlotRoundTrip 验证熔炉（或任意容器）槽位中的
// 药水 ID 在 v5 负载中同样持久化。
func TestBlockEntityFurnacePotionSlotRoundTrip(t *testing.T) {
	chunk := FlatGenerator{}.GenerateChunk(0, 0)
	furnace := BlockEntity{
		TypeID: 0,
		Items:  []ContainerItem{{ItemID: 1126, Count: 1, PotionID: 25}},
	}
	chunk.SetBlockEntity(2, FlatGroundLevel+1, 2, furnace)

	decoded, err := decodeChunkPayload(encodeChunkPayload(chunk))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := decoded.BlockEntityAt(2, FlatGroundLevel+1, 2)
	if !ok || len(got.Items) != 1 || got.Items[0].PotionID != 25 {
		t.Fatalf("槽位药水 ID 丢失：%+v", got.Items)
	}
}
