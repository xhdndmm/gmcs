//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package world

import "testing"

func TestBlockEntitySpawnerAndSignRoundTrip(t *testing.T) {
	chunk := FlatGenerator{}.GenerateChunk(0, 0)
	y := FlatGroundLevel + 1
	chunk.SetBlockEntity(2, y, 3, BlockEntity{
		TypeID: 30, SpawnerEntity: "minecraft:silverfish",
		SpawnerDelay: 20, SpawnerMinDelay: 200, SpawnerMaxDelay: 800,
	})
	chunk.SetBlockEntity(4, y, 5, BlockEntity{
		TypeID: 31, FrontText: [4]string{"第一行", "", "第三行", ""},
		BackText: [4]string{"back", "", "", ""},
	})
	decoded, err := decodeChunkPayload(encodeChunkPayload(chunk))
	if err != nil {
		t.Fatal(err)
	}
	spawner, ok := decoded.BlockEntityAt(2, y, 3)
	if !ok || spawner.SpawnerEntity != "minecraft:silverfish" || spawner.SpawnerMinDelay != 200 {
		t.Fatalf("刷怪笼 = %+v ok=%v", spawner, ok)
	}
	sign, ok := decoded.BlockEntityAt(4, y, 5)
	if !ok || sign.FrontText[0] != "第一行" || sign.FrontText[2] != "第三行" || sign.BackText[0] != "back" {
		t.Fatalf("告示牌 = %+v ok=%v", sign, ok)
	}
}
