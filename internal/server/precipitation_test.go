//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"
	"testing"

	"gmcs/internal/config"

	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 群系降水类型与落雪/结冰的测试。

// TestPrecipitationAtBiomes 校验降水类型表：沙漠/热带草原/恶地不降水，
// 积雪群系降雪，其余降雨。
func TestPrecipitationAtBiomes(t *testing.T) {
	for _, biome := range []uint16{world.BiomeDesert, world.BiomeSavanna, world.BiomeBadlands} {
		if got := world.PrecipitationAt(biome); got != world.PrecipitationNone {
			t.Fatalf("群系 %d 的降水类型 = %v, want NONE", biome, got)
		}
	}
	for _, biome := range []uint16{world.BiomeSnowyPlains, world.BiomeSnowyTaiga, world.BiomeSnowyBeach} {
		if got := world.PrecipitationAt(biome); got != world.PrecipitationSnow {
			t.Fatalf("群系 %d 的降水类型 = %v, want SNOW", biome, got)
		}
	}
	if got := world.PrecipitationAt(world.BiomePlains); got != world.PrecipitationRain {
		t.Fatalf("平原降水类型 = %v, want RAIN", got)
	}
}

// setColumnBiome 把 (x, z) 所在区块的 4×4 群系网格整体设为给定群系。
func setColumnBiome(t *testing.T, instance *Server, x, z int, biome uint16) {
	t.Helper()
	chunk, err := instance.testWorld().Chunk(x>>4, z>>4)
	if err != nil {
		t.Fatalf("加载区块失败：%v", err)
	}
	var grid [16]uint16
	for i := range grid {
		grid[i] = biome
	}
	chunk.SetBiomeGrid(grid)
}

// snowPad 铺一层石头地面并清空上方，返回地面方块坐标。
func snowPad(t *testing.T, instance *Server, x, z int) int {
	t.Helper()
	w := instance.testWorld()
	groundYf, ok := w.GroundY(x, z)
	if !ok {
		t.Fatalf("(%d,%d) 没有地面", x, z)
	}
	groundY := int(math.Floor(groundYf)) - 1
	w.SetBlock(x, groundY, z, world.StoneBlock)
	for dy := 1; dy <= 6; dy++ {
		w.SetBlock(x, groundY+dy, z, world.AirBlock)
	}
	return groundY
}

// TestTryPrecipitationSnow 验证雪原群系会在最高方块上方落一层雪。
func TestTryPrecipitationSnow(t *testing.T) {
	instance := newHopperTestServer(t)
	x, groundY, z := farmTestSpot(t, instance)
	setColumnBiome(t, instance, x, z, world.BiomeSnowyPlains)
	w := instance.testWorld()
	groundY = snowPad(t, instance, x, z)

	if !instance.tryPrecipitation(w, x, z) {
		t.Fatal("雪原群系应放置雪层")
	}
	snow, ok := snowLayerState(1)
	if !ok {
		t.Fatal("缺少雪层方块状态")
	}
	if got := w.BlockAt(x, groundY+1, z); got != snow {
		t.Fatalf("雪层位置方块 = %d, want %d", got, snow)
	}
	if groundY == 0 {
		t.Fatalf("unexpected groundY %d", groundY)
	}
}

// TestTryPrecipitationFreezesWater 验证雪原群系会把表层水冻成冰。
func TestTryPrecipitationFreezesWater(t *testing.T) {
	instance := newHopperTestServer(t)
	x, groundY, z := farmTestSpot(t, instance)
	setColumnBiome(t, instance, x, z, world.BiomeSnowyPlains)
	w := instance.testWorld()
	water, ok := registry.BlockStateIDs["minecraft:water"]
	if !ok {
		t.Fatal("缺少水方块状态")
	}
	w.SetBlock(x, groundY, z, world.StoneBlock)
	w.SetBlock(x, groundY+1, z, water)
	if !instance.tryPrecipitation(w, x, z) {
		t.Fatal("雪原群系应把表层水冻成冰")
	}
	ice, ok := registry.BlockStateIDs["minecraft:ice"]
	if !ok {
		t.Fatal("缺少冰方块状态")
	}
	if got := w.BlockAt(x, groundY+1, z); got != ice {
		t.Fatalf("水面方块 = %d, want 冰 %d", got, ice)
	}
}

// TestTryPrecipitationStacksSnowLayers 验证已有雪层时会加厚一层（原版积雪堆积）。
func TestTryPrecipitationStacksSnowLayers(t *testing.T) {
	instance := newHopperTestServer(t)
	x, groundY, z := farmTestSpot(t, instance)
	setColumnBiome(t, instance, x, z, world.BiomeSnowyPlains)
	w := instance.testWorld()
	groundY = snowPad(t, instance, x, z)
	first, ok := snowLayerState(1)
	if !ok {
		t.Fatal("缺少雪层方块状态")
	}
	w.SetBlock(x, groundY+1, z, first)
	if !instance.tryPrecipitation(w, x, z) {
		t.Fatal("已有雪层时应加厚一层")
	}
	want, ok := snowLayerState(2)
	if !ok {
		t.Fatal("缺少 2 层雪状态")
	}
	if got := w.BlockAt(x, groundY+1, z); got != want {
		t.Fatalf("雪层状态 = %d, want %d（layers=2）", got, want)
	}
}

// TestTryPrecipitationDesertNoSnow 验证不降水的群系（沙漠）不会落雪。
func TestTryPrecipitationDesertNoSnow(t *testing.T) {
	instance := newHopperTestServer(t)
	x, _, z := farmTestSpot(t, instance)
	setColumnBiome(t, instance, x, z, world.BiomeDesert)
	w := instance.testWorld()
	groundY := snowPad(t, instance, x, z)

	if instance.tryPrecipitation(w, x, z) {
		t.Fatal("沙漠群系不应落雪")
	}
	if got := w.BlockAt(x, groundY+1, z); got != world.AirBlock {
		t.Fatalf("沙漠上方方块 = %d, want 空气", got)
	}
}

// TestIsRainingAtRespectsBiomePrecipitation 验证 isRainingAt 遵守群系降水类型：
// 下雨时沙漠为 false、平原为 true（降雪群系同样算“在下”）。
func TestIsRainingAtRespectsBiomePrecipitation(t *testing.T) {
	instance := newHopperTestServer(t)
	x, groundY, z := farmTestSpot(t, instance)
	w := instance.testWorld()
	groundY = snowPad(t, instance, x, z)
	instance.setWeatherParameters(0, 1000, true, false)

	cases := []struct {
		biome uint16
		want  bool
		name  string
	}{
		{world.BiomeDesert, false, "沙漠"},
		{world.BiomeSavanna, false, "热带草原"},
		{world.BiomePlains, true, "平原"},
		{world.BiomeSnowyPlains, true, "雪原"},
	}
	for _, tc := range cases {
		setColumnBiome(t, instance, x, z, tc.biome)
		got := instance.isRainingAt(world.DimensionOverworld, x, groundY+1, z)
		if got != tc.want {
			t.Fatalf("%s：isRainingAt = %v, want %v", tc.name, got, tc.want)
		}
	}
	if w == nil {
		t.Fatal("世界为空")
	}
}

// newPrecipitationBenchServer 构造降水基准用的轻量服务器。
func newPrecipitationBenchServer(b *testing.B) *Server {
	b.Helper()
	cfg := config.Default()
	cfg.WorldDir = b.TempDir()
	cfg.SpawnMonsters = false
	instance, err := New(cfg)
	if err != nil {
		b.Fatal(err)
	}
	instance.weatherEnabled = false
	b.Cleanup(func() { _ = instance.worldFor(world.DimensionOverworld).Close() })
	return instance
}

// precipitationBenchColumn 在出生点附近铺一块石头地面并清空上方，返回列坐标。
func precipitationBenchColumn(b *testing.B, instance *Server) (int, int) {
	b.Helper()
	w := instance.testWorld()
	bx, _, bz := instance.spawnPositionFor(world.DimensionOverworld)
	baseX, baseZ := int(math.Floor(bx)), int(math.Floor(bz))
	for _, off := range spawnNearOffsets {
		x, z := baseX+off[0], baseZ+off[1]
		groundYf, ok := w.GroundY(x, z)
		if !ok {
			continue
		}
		groundY := int(math.Floor(groundYf)) - 1
		if groundY <= world.SeaLevel {
			continue
		}
		w.SetBlock(x, groundY, z, world.StoneBlock)
		for dy := 1; dy <= 6; dy++ {
			w.SetBlock(x, groundY+dy, z, world.AirBlock)
		}
		return x, z
	}
	b.Fatal("出生点附近找不到陆地方块列")
	return 0, 0
}

// setBenchBiome 把 (x, z) 所在区块的群系设为给定值。
func setBenchBiome(b *testing.B, instance *Server, x, z int, biome uint16) {
	b.Helper()
	chunk, err := instance.testWorld().Chunk(x>>4, z>>4)
	if err != nil {
		b.Fatal(err)
	}
	var grid [16]uint16
	for i := range grid {
		grid[i] = biome
	}
	chunk.SetBiomeGrid(grid)
}

// BenchmarkTryPrecipitationNoSnow 衡量一次降水抽样的开销（不降水的群系路径：
// 群系查询后立即返回，是降雨时绝大多数抽样的实际代价）。
func BenchmarkTryPrecipitationNoSnow(b *testing.B) {
	instance := newPrecipitationBenchServer(b)
	x, z := precipitationBenchColumn(b, instance)
	setBenchBiome(b, instance, x, z, world.BiomeDesert)
	w := instance.testWorld()
	b.ResetTimer()
	for b.Loop() {
		instance.tryPrecipitation(w, x, z)
	}
}

// BenchmarkTickPrecipitationClear 衡量晴天时整轮降水 tick 的开销（只有一个
// isRaining 判定）。
func BenchmarkTickPrecipitationClear(b *testing.B) {
	instance := newPrecipitationBenchServer(b)
	players := []*session{}
	b.ResetTimer()
	for b.Loop() {
		instance.tickPrecipitation(players)
	}
}
