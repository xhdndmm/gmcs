//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package world

import (
	"sync"
	"testing"
)

func TestParseGeneratorMode(t *testing.T) {
	cases := map[string]GeneratorMode{
		"":          GeneratorLegacy,
		"legacy":    GeneratorLegacy,
		"gmcs":      GeneratorLegacy,
		"vanilla":   GeneratorVanilla,
		"official":  GeneratorVanilla,
		" VANILLA ": GeneratorVanilla,
	}
	for input, want := range cases {
		got, ok := ParseGeneratorMode(input)
		if !ok || got != want {
			t.Fatalf("ParseGeneratorMode(%q) = %q,%v; want %q,true", input, got, ok, want)
		}
	}
	if _, ok := ParseGeneratorMode("unknown"); ok {
		t.Fatal("unknown generator mode accepted")
	}
}

func TestGeneratorModeFallbackForOtherDimensions(t *testing.T) {
	generator := NewGeneratorWithMode(DimensionNether, 1, GeneratorVanilla)
	if _, ok := generator.(NetherGenerator); !ok {
		t.Fatalf("nether generator = %T, want NetherGenerator", generator)
	}
	generator = NewGeneratorWithMode(DimensionOverworld, 1, GeneratorLegacy)
	if _, ok := generator.(SeededGenerator); !ok {
		t.Fatalf("overworld legacy generator = %T, want SeededGenerator", generator)
	}
}

func TestVanillaGeneratorChunk(t *testing.T) {
	generator, err := NewVanillaGenerator(4321)
	if err != nil {
		t.Fatalf("NewVanillaGenerator: %v", err)
	}
	if generator.Dimension() != DimensionOverworld {
		t.Fatalf("dimension = %v", generator.Dimension())
	}
	chunk := generator.GenerateChunk(2, 3)
	if chunk == nil {
		t.Fatal("GenerateChunk returned nil")
	}
	// 原版主世界在 y=-64 一定有基岩；至少多数列有固体地表。
	solid := 0
	for z := 0; z < SectionSize; z++ {
		for x := 0; x < SectionSize; x++ {
			column := chunk.Column(x, z)
			if column.HasSolid {
				solid++
				if column.SolidY < WorldMinY || column.SolidY >= WorldMinY+WorldHeight {
					t.Fatalf("column (%d,%d) solid y = %d out of range", x, z, column.SolidY)
				}
			}
		}
	}
	if solid < 256 {
		t.Fatalf("only %d/256 columns have solid blocks", solid)
	}
	grid := chunk.BiomeGrid()
	for i, biome := range grid {
		if biome == 0 {
			t.Fatalf("biome grid[%d] is 0", i)
		}
	}
	// 确定性：相同种子同一区块再次生成结果一致。
	second := generator.GenerateChunk(2, 3)
	for z := 0; z < SectionSize; z++ {
		for x := 0; x < SectionSize; x++ {
			a, b := chunk.Column(x, z), second.Column(x, z)
			if a.HasSolid != b.HasSolid || a.SolidY != b.SolidY || a.TopState != b.TopState {
				t.Fatalf("非确定性：(%d,%d) %+v vs %+v", x, z, a, b)
			}
		}
	}
	if got, want := generator.SurfaceY(2*16+5, 3*16+9), chunk.Column(5, 9).SolidY; got != want {
		t.Fatalf("SurfaceY = %d, want %d", got, want)
	}
}

func TestVanillaGeneratorConcurrent(t *testing.T) {
	generator, err := NewVanillaGenerator(4321)
	if err != nil {
		t.Fatalf("NewVanillaGenerator: %v", err)
	}
	want := generator.GenerateChunk(0, 0).Column(0, 0)
	var wg sync.WaitGroup
	failures := make(chan string, 16)
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			chunk := generator.GenerateChunk(0, 0)
			got := chunk.Column(0, 0)
			if got.HasSolid != want.HasSolid || got.SolidY != want.SolidY || got.TopState != want.TopState {
				failures <- "并发生成结果不一致"
			}
		}()
	}
	wg.Wait()
	close(failures)
	for message := range failures {
		t.Fatal(message)
	}
}

func BenchmarkVanillaGeneratorChunk(b *testing.B) {
	generator, err := NewVanillaGenerator(4321)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		generator.GenerateChunk(i%16, i/16)
	}
}
