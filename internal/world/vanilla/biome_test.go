package vanilla

import "testing"

func TestEndBiomeSource(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(data, "minecraft:end", 4321)
	if err != nil {
		t.Fatal(err)
	}
	biomes, err := router.Biomes()
	if err != nil {
		t.Fatal(err)
	}
	if got := biomes.BiomeAtBlock(0, 64, 0); got != "minecraft:the_end" {
		t.Fatalf("主岛中心群系 = %q", got)
	}
	if got := biomes.BiomeAtBlock(5000, 64, 0); got == "" || got == "minecraft:the_end" {
		t.Fatalf("外岛群系 = %q", got)
	}
}

func TestBiomeTreeMatchesBruteForce(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(data, "minecraft:overworld", 4321)
	if err != nil {
		t.Fatal(err)
	}
	source, err := router.Biomes()
	if err != nil {
		t.Fatal(err)
	}
	random := newLegacyRandom(99)
	for sample := 0; sample < 2000; sample++ {
		var target climateTarget
		for i := range target.values {
			target.values[i] = int64(random.nextIntBounded(20001) - 10000)
		}
		best, bestDistance := 0, int64(1<<62)
		source.tree.query(source.tree.root, &target, &best, &bestDistance)
		want, wantDistance := 0, int64(1<<62)
		for i := range source.points {
			distance := source.points[i].distance(&target)
			if distance < wantDistance || (distance == wantDistance && i < want) {
				want, wantDistance = i, distance
			}
		}
		if best != want || bestDistance != wantDistance {
			t.Fatalf("KD 树与线性扫描不一致：tree=%d/%d brute=%d/%d target=%v",
				best, bestDistance, want, wantDistance, target.values)
		}
	}
}
