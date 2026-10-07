package world

import "testing"

// TestBlockEntityFurnaceProgressRoundTrip 验证熔炉进度随区块负载持久化
// （存储格式 v3：槽位之后追加 burn/cook 四个 varint）。
func TestBlockEntityFurnaceProgressRoundTrip(t *testing.T) {
	chunk := FlatGenerator{}.GenerateChunk(0, 0)
	furnace := BlockEntity{
		TypeID:        0,
		Items:         []ContainerItem{{ItemID: 1, Count: 5}, {}, {ItemID: 2, Count: 1}},
		BurnRemaining: 120,
		BurnTotal:     1600,
		CookProgress:  77,
		CookTotal:     200,
	}
	chunk.SetBlockEntity(3, FlatGroundLevel+1, 4, furnace)

	payload := encodeChunkPayload(chunk)
	decoded, err := decodeChunkPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := decoded.BlockEntityAt(3, FlatGroundLevel+1, 4)
	if !ok {
		t.Fatal("方块实体丢失")
	}
	if got.BurnRemaining != 120 || got.BurnTotal != 1600 || got.CookProgress != 77 || got.CookTotal != 200 {
		t.Fatalf("熔炉进度丢失：%+v", got)
	}
	if len(got.Items) != 3 || got.Items[0].ItemID != 1 || got.Items[0].Count != 5 {
		t.Fatalf("槽位内容丢失：%+v", got.Items)
	}

	// 仅剩进度（槽位全空）时实体不应被视为空。
	empty := BlockEntity{BurnRemaining: 1}
	if empty.Empty() {
		t.Fatal("有燃烧进度的方块实体不应为空")
	}
}
