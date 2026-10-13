package world

import "testing"

// TestBlockEntityEnchantmentsRoundTrip 验证槽位附魔列表随区块负载持久化
// （存储格式 v7）。
func TestBlockEntityEnchantmentsRoundTrip(t *testing.T) {
	chunk := FlatGenerator{}.GenerateChunk(0, 0)
	entity := BlockEntity{
		TypeID: 1,
		Items: []ContainerItem{
			{
				ItemID: 913, Count: 1,
				Enchantments: []EnchantedItem{{ID: 33, Level: 5}, {ID: 40, Level: 3}},
			},
			{ItemID: 913, Count: 1},
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
	if len(first.Enchantments) != 2 {
		t.Fatalf("槽位 0 附魔数 = %d, want 2", len(first.Enchantments))
	}
	if first.Enchantments[0] != (EnchantedItem{ID: 33, Level: 5}) ||
		first.Enchantments[1] != (EnchantedItem{ID: 40, Level: 3}) {
		t.Fatalf("槽位 0 附魔不符：%+v", first.Enchantments)
	}
	if len(got.Items[1].Enchantments) != 0 {
		t.Fatalf("槽位 1 不应有附魔：%+v", got.Items[1].Enchantments)
	}
	if got.Items[2].Count != 64 {
		t.Fatalf("槽位 2 数量丢失：%+v", got.Items[2])
	}
}
