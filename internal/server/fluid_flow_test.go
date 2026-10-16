//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/world"
)

// TestFluidFlowVector 校验流向向量（原版 FlowingFluid.getFlow）：
// 静水、非流体为零；水面有坡度时指向低处；下落水流在侧面受阻时向下。
func TestFluidFlowVector(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "FlowProbe")
	w := instance.testWorld()
	baseX, floorY, baseZ := fluidBase(t, instance)

	// 非流体位置：零向量。
	if vx, vy, vz := instance.fluidFlowVector(w, [3]int{baseX, floorY, baseZ}); vx != 0 || vy != 0 || vz != 0 {
		t.Fatalf("空气中流向 = (%v, %v, %v), want 零向量", vx, vy, vz)
	}

	// 静水（整周都是同水位、下方是流体/实心）：零向量。
	// 用一池源方块保证四周水位相同（7×7 源方块）。
	source, ok := fluidSourceState(waterBlockName)
	if !ok {
		t.Fatal("缺少水源状态")
	}
	for dx := -3; dx <= 3; dx++ {
		for dz := -3; dz <= 3; dz++ {
			w.SetBlock(baseX+dx, floorY, baseZ+dz, source)
		}
	}
	if vx, vy, vz := instance.fluidFlowVector(w, [3]int{baseX, floorY, baseZ}); math.Abs(vx) > 1e-9 || math.Abs(vy) > 1e-9 || math.Abs(vz) > 1e-9 {
		t.Fatalf("静水中心流向 = (%v, %v, %v), want 零向量", vx, vy, vz)
	}

	// 坡度：中心为源、东侧依次为低水位 → 流向应指向东（x 为正）。
	for dx, amount := range []int{7, 6, 5} {
		low, ok := fluidFlowingState(waterBlockName, amount)
		if !ok {
			t.Fatal("缺少流动水状态")
		}
		w.SetBlock(baseX+1+dx, floorY, baseZ, low)
	}
	source2, _ := fluidSourceState(waterBlockName)
	w.SetBlock(baseX, floorY, baseZ, source2)
	vx, _, vz := instance.fluidFlowVector(w, [3]int{baseX, floorY, baseZ})
	if math.Sqrt(vx*vx+vz*vz) == 0 {
		t.Fatal("坡面水流向为零, want 指向低处")
	}
	if vx <= 0 || math.Abs(vx) < math.Abs(vz) {
		t.Fatalf("坡面水流向 = (%v, %v), want 主要指向东", vx, vz)
	}

	// 下落水流：水柱底部（下方有水）+ 四周实心 → 流向向下。
	falling, ok := fluidFallingState(waterBlockName)
	if !ok {
		t.Fatal("缺少下落水状态")
	}
	columnX, columnZ := baseX+8, baseZ+8
	for dy := 1; dy <= 3; dy++ {
		w.SetBlock(columnX, floorY+dy, columnZ, falling)
	}
	for _, dir := range fluidHorizontalDirs {
		for dy := 0; dy <= 3; dy++ {
			w.SetBlock(columnX+dir.dx, floorY+dy, columnZ+dir.dz, world.StoneBlock)
		}
	}
	fallX, fallY, fallZ := instance.fluidFlowVector(w, [3]int{columnX, floorY + 1, columnZ})
	if fallY >= 0 {
		t.Fatalf("受阻下落水流流向 = (%v, %v, %v), want vy < 0", fallX, fallY, fallZ)
	}
}

// TestItemPushedByWaterFlow 验证水流把掉落物推向下游（原版
// FlowingFluid.getFlow + Entity.updateFluidHeightAndDoFluidPushing）：
// 在静水最西端追加一个水源后，水面向东漫流，水中的掉落物应持续向东漂移。
func TestItemPushedByWaterFlow(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Drifter")
	w := instance.testWorld()
	baseX, floorY, baseZ := fluidBase(t, instance)

	// 一条向东的水道：西端为源，其余为空（水会向东漫流并保持坡面）。
	if _, ok := fluidSourceState(waterBlockName); !ok {
		t.Fatal("缺少水源状态")
	}
	for dx := 0; dx <= 6; dx++ {
		for dz := -1; dz <= 1; dz++ {
			if dx == 0 && dz == 0 {
				continue
			}
			w.SetBlock(baseX+dx, floorY, baseZ+dz, world.AirBlock)
		}
	}
	placeFluidSourceForTest(t, instance, baseX, floorY, baseZ, waterBlockName)
	// 让水流铺开（水计划刻 5 tick/格）。
	runTicks(instance, 60)
	if _, ok := fluidInfoOf(w.BlockAt(baseX+2, floorY, baseZ)); !ok {
		t.Fatal("水道未铺开（测试前提失效）")
	}

	stack, err := item.FromName("minecraft:stone", 1)
	if err != nil {
		t.Fatal(err)
	}
	entity := instance.spawnItem(world.DimensionOverworld, stack, float64(baseX+2)+0.5, float64(floorY)+0.1, float64(baseZ)+0.5, 0, 0, 0, 0)
	if entity == nil {
		t.Fatal("spawnItem 失败")
	}
	startX := entity.X
	for i := 0; i < 120; i++ {
		instance.tick()
	}
	if entity.X <= startX+0.1 {
		t.Fatalf("水中掉落物 X = %v（起始 %v），未见向东漂移", entity.X, startX)
	}
}
