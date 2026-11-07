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
