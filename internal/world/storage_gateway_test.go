//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package world

import "testing"

func TestBlockEntityEndGatewayRoundTrip(t *testing.T) {
	chunk := FlatGenerator{}.GenerateChunk(0, 0)
	chunk.SetBlockEntity(3, FlatGroundLevel+1, 4, BlockEntity{
		TypeID:       22,
		GatewayExit:  [3]int{100, 50, -7},
		GatewayExact: true,
	})
	decoded, err := decodeChunkPayload(encodeChunkPayload(chunk))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := decoded.BlockEntityAt(3, FlatGroundLevel+1, 4)
	if !ok {
		t.Fatal("折跃门方块实体丢失")
	}
	if !got.GatewayExact || got.GatewayExit != [3]int{100, 50, -7} {
		t.Fatalf("折跃门目标 = %+v exact=%v", got.GatewayExit, got.GatewayExact)
	}
}
