//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"math"
	"testing"
)

// TestMthGetSeedMatchesVanilla 用官方字节码（bgj.b(III)J）导出的参考值校验
// Mth.getSeed。含水层用它对每个格生成抖动偏移，错一位就会让流体格整体位移。
func TestMthGetSeedMatchesVanilla(t *testing.T) {
	cases := []struct {
		x, y, z int
		want    int64
	}{
		{0, 0, 0, 0},
		{1, 2, 3, -33674130277896},
		{-7, 64, -13, -36863722706148},
		{100, -64, 255, -20713652437915},
		{12345, -600, -54321, 124332306805978},
		{31, -1, 17, -40811794108490},
	}
	for _, c := range cases {
		if got := mthGetSeed(c.x, c.y, c.z); got != c.want {
			t.Fatalf("getSeed(%d, %d, %d) = %d, want %d", c.x, c.y, c.z, got, c.want)
		}
	}
}

// TestMthRoundTowardMatchesVanilla 校验 Mth.roundToward(double, int)：
// 官方实现是 floor(value / interval) * interval。
func TestMthRoundTowardMatchesVanilla(t *testing.T) {
	cases := []struct {
		value    float64
		interval int
		want     int
	}{
		{0, 3, 0},
		{1.5, 3, 0},
		{-1.5, 3, -3},
		{9.99, 3, 9},
		{-9.99, 3, -12},
		{3, 3, 3},
		{-0.1, 3, -3},
		{6, 3, 6},
	}
	for _, c := range cases {
		if got := roundToward(c.value, c.interval); got != c.want {
			t.Fatalf("roundToward(%v, %d) = %d, want %d", c.value, c.interval, got, c.want)
		}
	}
}

// TestAquiferPressureThreshold 校验原版静态常量 m：
// m = aquiferPressure(Mth.square(10), Mth.square(12)) = 1 - 44/25。
func TestAquiferPressureThreshold(t *testing.T) {
	if want := 1 - 44.0/25.0; aquiferPressureThreshold != want {
		t.Fatalf("压力阈值 = %v, want %v", aquiferPressureThreshold, want)
	}
}

// TestAquiferFluidPickerGlobal 校验全局流体选择器：
// y < min(-54, sea_level) 时是岩浆，否则是海平面高度的默认流体。
func TestAquiferFluidPickerGlobal(t *testing.T) {
	picker := newAquiferFluidPicker(63, fluidWater)
	if got, want := picker.computeFluid(0, -55, 0), (fluidStatus{level: -54, kind: fluidLava}); got != want {
		t.Fatalf("y=-55 的流体 = %+v, want %+v", got, want)
	}
	if got, want := picker.computeFluid(0, -54, 0), (fluidStatus{level: 63, kind: fluidWater}); got != want {
		t.Fatalf("y=-54 的流体 = %+v, want %+v", got, want)
	}
	if got, want := picker.computeFluid(0, 63, 0), fluidAir; got.at(63) != want || got.at(62) != fluidWater {
		t.Fatalf("y=63 的流体状态 = %+v", got)
	}

	// 海平面低于岩浆液面时，阈值取海平面。
	low := newAquiferFluidPicker(-80, fluidWater)
	if got, want := low.computeFluid(0, -81, 0), (fluidStatus{level: -54, kind: fluidLava}); got != want {
		t.Fatalf("海平面 -80、y=-81 的流体 = %+v, want %+v", got, want)
	}
	if got, want := low.computeFluid(0, -80, 0), (fluidStatus{level: -80, kind: fluidWater}); got != want {
		t.Fatalf("海平面 -80、y=-80 的流体 = %+v, want %+v", got, want)
	}
}

// TestAquiferSamplingOffsets 校验表面采样偏移表（原版
// SURFACE_SAMPLING_OFFSETS_IN_CHUNKS）：13 项、首项为中心。
func TestAquiferSamplingOffsets(t *testing.T) {
	if got, want := len(aquiferSamplingOffsets), 13; got != want {
		t.Fatalf("偏移数量 = %d, want %d", got, want)
	}
	if aquiferSamplingOffsets[0] != [2]int{0, 0} {
		t.Fatalf("首项 = %v, want [0 0]", aquiferSamplingOffsets[0])
	}
	for _, offset := range aquiferSamplingOffsets {
		if offset[0] < -3 || offset[0] > 1 || offset[1] < -1 || offset[1] > 1 {
			t.Fatalf("偏移 %v 超出原版范围", offset)
		}
	}
}

// TestAquiferPreliminarySurfaceLevel 校验 preliminary_surface_level：
// 采样点按 4 格对齐、结果按列缓存、与原版 noiseChunk.preliminarySurfaceLevel
// 一样取 floor(find_top_surface)。
func TestAquiferPreliminarySurfaceLevel(t *testing.T) {
	router := mustRouter(t, 1234)
	sampler := router.NewChunkSampler(0, 0)
	aquifer := sampler.Aquifer()

	// 对齐到同一 4 格列的坐标必须命中同一缓存项。
	before := len(aquifer.preliminaryCache)
	if got, want := aquifer.preliminarySurfaceLevel(5, 7), aquifer.preliminarySurfaceLevel(4, 4); got != want {
		t.Fatalf("同列取样不一致：%d vs %d", got, want)
	}
	if got := len(aquifer.preliminaryCache); got != before {
		t.Fatalf("同列取样后缓存项从 %d 变成 %d", before, got)
	}

	// 与直接求值保持一致。
	node := router.nodes["preliminary_surface_level"]
	for _, p := range [][2]int{{0, 0}, {4, 4}, {-8, 12}, {16, -4}} {
		want := int(math.Floor(router.Evaluator().Evaluate(node, Context{X: p[0], Y: 0, Z: p[1]})))
		if got := aquifer.preliminarySurfaceLevel(p[0], p[1]); got != want {
			t.Fatalf("preliminarySurfaceLevel(%d, %d) = %d, want %d", p[0], p[1], got, want)
		}
	}

	// 四参数重载取范围内的最大值。
	best := aquifer.preliminarySurfaceLevelRange(0, 0, 16, 16)
	for z := 0; z <= 16; z += 4 {
		for x := 0; x <= 16; x += 4 {
			if level := aquifer.preliminarySurfaceLevel(x, z); level > best {
				t.Fatalf("范围最大值 %d 小于 (%d,%d) 处的 %d", best, x, z, level)
			}
		}
	}
}

// TestAquiferSubstanceFollowsDensity 校验密度与含水层判定的衔接：
// 密度为正时必须返回 defaultBlock，密度为负且远高于地表时必须是空气。
func TestAquiferSubstanceFollowsDensity(t *testing.T) {
	router := mustRouter(t, 4321)
	sampler := router.NewChunkSampler(0, 0)

	maxY := router.Setting().MinY + router.Setting().Height - 1
	for x := 0; x < 16; x += 5 {
		for z := 0; z < 16; z += 5 {
			if kind, defaultBlock := sampler.Substance(x, maxY, z); defaultBlock || kind != fluidAir {
				t.Fatalf("(%d,%d,%d) 应在地表以上，got kind=%v defaultBlock=%v", x, maxY, z, kind, defaultBlock)
			}
			density := sampler.Density(x, maxY, z)
			if density > 0 {
				t.Fatalf("(%d,%d,%d) 的密度 = %v，测试前提不成立", x, maxY, z, density)
			}
		}
	}

	// 底部若干层应当绝大多数是实心（密度为正）。
	solid := 0
	total := 0
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			for y := router.Setting().MinY; y < router.Setting().MinY+8; y++ {
				total++
				if _, defaultBlock := sampler.Substance(x, y, z); defaultBlock {
					solid++
				}
			}
		}
	}
	if solid*2 < total {
		t.Fatalf("底部实心比例过低：%d/%d", solid, total)
	}
}

// TestAquiferSubstanceMarksFluidUpdate 校验含水层格机制真的被走到：
// 在含水层格覆盖了地形的区块里，必须存在被标记为"需要重新调度流体更新"的方块。
func TestAquiferSubstanceMarksFluidUpdate(t *testing.T) {
	router := mustRouter(t, 4321)
	sampler := router.NewChunkSampler(62, 62)

	scheduled := 0
	for x := 62 * 16; x < 62*16+16; x++ {
		for z := 62 * 16; z < 62*16+16; z++ {
			for y := -64; y < 320; y++ {
				kind, defaultBlock := sampler.Aquifer().ComputeSubstance(x, y, z, sampler.Density(x, y, z))
				if defaultBlock {
					continue
				}
				if kind != fluidAir && kind != fluidWater && kind != fluidLava {
					t.Fatalf("(%d,%d,%d) 返回未知流体 %d", x, y, z, kind)
				}
				if sampler.Aquifer().ShouldScheduleFluidUpdate() {
					scheduled++
				}
			}
		}
	}
	if scheduled == 0 {
		t.Fatalf("该区块没有任何方块被标记为需要调度流体更新")
	}
}

// TestAquiferDeterministic 校验同一区块两次构造得到完全相同的判定。
func TestAquiferDeterministic(t *testing.T) {
	router := mustRouter(t, 98765)
	first := router.NewChunkSampler(-3, 7)
	second := router.NewChunkSampler(-3, 7)
	for x := -3 * 16; x < -3*16+16; x += 4 {
		for z := 7 * 16; z < 7*16+16; z += 4 {
			for y := -32; y < 96; y += 7 {
				firstKind, firstDefault := first.Substance(x, y, z)
				secondKind, secondDefault := second.Substance(x, y, z)
				if firstKind != secondKind || firstDefault != secondDefault {
					t.Fatalf("(%d,%d,%d) 两次判定不同：(%v,%v) vs (%v,%v)",
						x, y, z, firstKind, firstDefault, secondKind, secondDefault)
				}
			}
		}
	}
}

// BenchmarkAquiferFillChunk 度量"密度 + 含水层"填满一个区块的耗时。
func BenchmarkAquiferFillChunk(b *testing.B) {
	data, err := Load()
	if err != nil {
		b.Fatalf("加载数据失败：%v", err)
	}
	router, err := NewRouter(data, "minecraft:overworld", 1234)
	if err != nil {
		b.Fatalf("构造路由表失败：%v", err)
	}
	setting := router.Setting()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sampler := router.NewChunkSampler(0, 0)
		for x := 0; x < 16; x++ {
			for z := 0; z < 16; z++ {
				for y := setting.MinY; y < setting.MinY+setting.Height; y++ {
					sampler.Substance(x, y, z)
				}
			}
		}
	}
}
