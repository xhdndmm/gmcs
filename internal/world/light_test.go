package world

import (
	"testing"

	"gmcs/internal/protocol"
	"gmcs/internal/registry"
)

func TestChunkLocalLightSkyAndBlock(t *testing.T) {
	chunk := NewChunk(0, 0)
	// 整层屋顶：完全遮住天空光向下传播；随后在屋顶中留一块萤石。
	for x := 0; x < SectionSize; x++ {
		for z := 0; z < SectionSize; z++ {
			chunk.SetBlockState(x, 64, z, StoneBlock)
		}
	}
	chunk.SetBlockState(1, 65, 0, GlowstoneBlock)
	chunk.EnsureLight()

	if got := chunk.lightAt(true, 0, 65, 0); got != 15 {
		t.Fatalf("stone 上方的天空光 = %d, want 15", got)
	}
	if got := chunk.lightAt(true, 0, 64, 0); got != 0 {
		t.Fatalf("不透明方块处天空光 = %d, want 0", got)
	}
	if got := chunk.lightAt(true, 0, 63, 0); got != 0 {
		t.Fatalf("屋顶下方天空光 = %d, want 0", got)
	}
	if got := chunk.lightAt(false, 1, 65, 0); got != 15 {
		t.Fatalf("萤石方块光 = %d, want 15", got)
	}
	if got := chunk.lightAt(false, 2, 65, 0); got != 14 {
		t.Fatalf("萤石相邻方块光 = %d, want 14", got)
	}
	if got := chunk.lightAt(false, 0, 65, 0); got != 14 {
		t.Fatalf("萤石另一侧方块光 = %d, want 14", got)
	}
}

func TestWorldIncrementalLight(t *testing.T) {
	instance, err := Open(t.TempDir(), FlatGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	if _, err := instance.Chunk(0, 0); err != nil {
		t.Fatal(err)
	}
	if got := instance.LightAt(0, 100, 0, true); got != 15 {
		t.Fatalf("开阔天空光 = %d, want 15", got)
	}

	if !instance.SetBlock(0, 100, 0, StoneBlock) {
		t.Fatal("放置石头失败")
	}
	if got := instance.LightAt(0, 100, 0, true); got != 0 {
		t.Fatalf("放置方块后该格天空光 = %d, want 0", got)
	}
	if got := instance.LightAt(0, 99, 0, true); got != 14 {
		t.Fatalf("单个方块下方天空光 = %d, want 14", got)
	}
	packet := instance.EncodeLightUpdatePacket(0, 0)
	if packet == nil {
		t.Fatal("放置方块后应有 Light Update 包")
	}
	id, _, err := protocol.DecodeVarInt(packet)
	if err != nil || id != protocol.PlayPacketIDLightUpdate {
		t.Fatalf("Light Update 包 ID = %#x (err=%v)", id, err)
	}

	if !instance.SetBlock(0, 100, 0, AirBlock) {
		t.Fatal("移除石头失败")
	}
	if got := instance.LightAt(0, 100, 0, true); got != 15 {
		t.Fatalf("移除方块后天空光 = %d, want 15", got)
	}

	if !instance.SetBlock(0, 90, 0, GlowstoneBlock) {
		t.Fatal("放置萤石失败")
	}
	if got := instance.LightAt(0, 90, 0, false); got != 15 {
		t.Fatalf("萤石方块光 = %d, want 15", got)
	}
	if got := instance.LightAt(0, 91, 0, false); got != 14 {
		t.Fatalf("萤石上方方块光 = %d, want 14", got)
	}
	if !instance.SetBlock(0, 90, 0, AirBlock) {
		t.Fatal("移除萤石失败")
	}
	if got := instance.LightAt(0, 90, 0, false); got != 13 {
		t.Fatalf("移除萤石后方块光 = %d, want 13", got)
	}
}

func TestWorldLightCrossesChunkBoundary(t *testing.T) {
	instance, err := Open(t.TempDir(), FlatGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	if _, err := instance.Chunk(0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := instance.Chunk(1, 0); err != nil {
		t.Fatal(err)
	}
	// 先让两侧区块都完成本地光照初始化。
	instance.LightAt(0, 100, 0, false)
	instance.LightAt(16, 100, 0, false)
	if !instance.SetBlock(15, 100, 0, GlowstoneBlock) {
		t.Fatal("放置边界萤石失败")
	}
	if got := instance.LightAt(16, 100, 0, false); got != 14 {
		t.Fatalf("跨区块方块光 = %d, want 14", got)
	}
}

func TestWorldLightPropagatesWhenNeighborLoads(t *testing.T) {
	instance, err := Open(t.TempDir(), FlatGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	if _, err := instance.Chunk(0, 0); err != nil {
		t.Fatal(err)
	}
	instance.LightAt(15, 100, 0, false) // 初始化区块 0 光照
	if !instance.SetBlock(15, 100, 0, GlowstoneBlock) {
		t.Fatal("放置边界萤石失败")
	}
	if _, err := instance.Chunk(1, 0); err != nil {
		t.Fatal(err)
	}
	if got := instance.LightAt(16, 100, 0, false); got != 14 {
		t.Fatalf("邻区块加载后方块光 = %d, want 14", got)
	}
}

func BenchmarkChunkEnsureLightVanilla(b *testing.B) {
	generator, err := NewGenerator(DimensionOverworld, 4321)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		chunk := generator.GenerateChunk(i%32, (i/32)%32)
		chunk.EnsureLight()
	}
}

func TestNetherHasNoSkyLight(t *testing.T) {
	chunk := NewChunkDim(0, 0, DimensionNether)
	chunk.SetBlockState(0, 100, 0, GlowstoneBlock)
	chunk.EnsureLight()
	if got := chunk.lightAt(true, 0, 100, 0); got != 0 {
		t.Fatalf("下界天空光 = %d, want 0", got)
	}
	if got := chunk.lightAt(false, 0, 100, 0); got != 15 {
		t.Fatalf("下界萤石方块光 = %d, want 15", got)
	}
}

func TestLightPropertiesForCommonBlocks(t *testing.T) {
	cases := []struct {
		name     string
		emission uint8
		opacity  uint8
	}{
		{"minecraft:air", 0, 0},
		{"minecraft:stone", 0, 15},
		{"minecraft:glass", 0, 0},
		{"minecraft:water", 0, 1},
		{"minecraft:oak_leaves", 0, 1},
		{"minecraft:oak_slab", 0, 0},
		{"minecraft:oak_stairs", 0, 0},
		{"minecraft:oak_fence", 0, 0},
		{"minecraft:torch", 14, 0},
		{"minecraft:glowstone", 15, 15},
		{"minecraft:sea_lantern", 15, 15},
		{"minecraft:end_portal", 15, 0},
	}
	for _, tc := range cases {
		state, err := registry.BlockStateID(tc.name)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		prop := lightPropertyOf(state)
		if prop.emission != tc.emission || prop.opacity != tc.opacity {
			t.Fatalf("%s light = emit %d/opacity %d, want %d/%d",
				tc.name, prop.emission, prop.opacity, tc.emission, tc.opacity)
		}
	}
}
