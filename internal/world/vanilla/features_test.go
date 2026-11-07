package vanilla

import (
	"reflect"
	"strings"
	"testing"
)

func TestOreFeaturesGenerated(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(data, "minecraft:overworld", 4321)
	if err != nil {
		t.Fatal(err)
	}
	chunk, err := router.GenerateChunk(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	ores := map[string]int{}
	deepslate := 0
	chunk.ForEachBlock(func(_, _, _ int, block SurfaceBlock) {
		if strings.HasSuffix(block.Name, "_ore") {
			ores[block.Name]++
			if strings.HasPrefix(block.Name, "minecraft:deepslate_") {
				deepslate++
			}
		}
	})
	if len(ores) == 0 {
		t.Fatal("区块没有生成任何矿石")
	}
	for _, name := range []string{"minecraft:coal_ore", "minecraft:iron_ore"} {
		if ores[name] == 0 {
			t.Fatalf("缺少 %s（实际：%v）", name, ores)
		}
	}
	if deepslate == 0 {
		t.Fatalf("缺少深板岩矿石变体（实际：%v）", ores)
	}
}

func TestOreFeaturesDeterministic(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	first, err := NewRouter(data, "minecraft:overworld", 99)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewRouter(data, "minecraft:overworld", 99)
	if err != nil {
		t.Fatal(err)
	}
	a, err := first.GenerateChunk(3, -2)
	if err != nil {
		t.Fatal(err)
	}
	b, err := second.GenerateChunk(3, -2)
	if err != nil {
		t.Fatal(err)
	}
	a.ForEachBlock(func(x, y, z int, block SurfaceBlock) {
		if other := b.BlockAt(x, y, z); !reflect.DeepEqual(other, block) {
			t.Fatalf("(%d,%d,%d) = %+v, want %+v", x, y, z, other, block)
		}
	})
}

func TestPlacementCountAndHeightSampling(t *testing.T) {
	count := intProvider{kind: "minecraft:uniform", minimum: 2, maximum: 4}
	random := newLegacyRandom(1)
	for i := 0; i < 100; i++ {
		value := count.sample(random)
		if value < 2 || value > 4 {
			t.Fatalf("count sample = %d, want [2,4]", value)
		}
	}
	height := featureHeightProvider{
		kind:    "minecraft:trapezoid",
		minimum: heightAnchor{kind: "absolute", value: -64},
		maximum: heightAnchor{kind: "absolute", value: 64},
	}
	setting := NoiseSetting{MinY: -64, Height: 384}
	for i := 0; i < 100; i++ {
		value := height.sample(random, setting)
		if value < -64 || value > 64 {
			t.Fatalf("height sample = %d, want [-64,64]", value)
		}
	}
}
