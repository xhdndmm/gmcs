//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 掉落物运动与环境方块判定的原版对照测试
// （对照 1.21.11 反编译的 ItemEntity.u()/摩擦/反弹 与
// Entity.checkInsideBlocks 的方块覆盖判定）。

// TestItemWaterMotionVanilla 验证水中运动常量：水平 ×0.99（水中分支）再 ×0.98
// （摩擦步骤）、垂直提速 5.0E-4（仅当垂直速度 < 0.06）再 ×0.98、水中不减重力。
func TestItemWaterMotionVanilla(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "WaterPhys")
	w := instance.testWorld()
	baseX, floorY, baseZ := fluidBase(t, instance)

	// 静水：7×7 水源方块池（内部流向为零，隔离“水流推动”因素）。
	source, ok := fluidSourceState(waterBlockName)
	if !ok {
		t.Fatal("缺少水源状态")
	}
	for dx := -3; dx <= 3; dx++ {
		for dz := -3; dz <= 3; dz++ {
			w.SetBlock(baseX+dx, floorY, baseZ+dz, source)
		}
	}

	stack, err := item.FromName("minecraft:dirt", 1)
	if err != nil {
		t.Fatal(err)
	}
	e := instance.spawnItem(world.DimensionOverworld, stack,
		float64(baseX)+0.5, float64(floorY), float64(baseZ)+0.5, 0, 0, 0, 10_000)
	if e == nil {
		t.Fatal("spawnItem returned nil")
	}
	e.VelX = 0.1
	e.VelY = 0
	e.VelZ = 0

	instance.tick()
	// 期望：水平 = 0.1 × 0.99（水中）× 0.98（摩擦）= 0.09702；
	// 垂直 = (0 + 5.0E-4) × 0.98 = 4.9E-4（浮力提速，水中不加重力）。
	wantVX := 0.1 * itemWaterDrag * itemAirDrag
	wantVY := itemWaterBuoyancy * itemAirDrag
	if math.Abs(e.VelX-wantVX) > 1e-9 {
		t.Fatalf("水中 vx = %v, want %v", e.VelX, wantVX)
	}
	if math.Abs(e.VelZ) > 1e-12 {
		t.Fatalf("水中 vz = %v, want 0", e.VelZ)
	}
	if math.Abs(e.VelY-wantVY) > 1e-12 {
		t.Fatalf("水中 vy = %v, want %v（浮力提速后乘垂直阻尼）", e.VelY, wantVY)
	}

	// 垂直速度超过 0.06 时不再加浮力（只受摩擦步骤的 ×0.98）。
	e.VelY = 0.1
	instance.tick()
	if want := 0.1 * itemAirDrag; math.Abs(e.VelY-want) > 1e-9 {
		t.Fatalf("水中高速 vy = %v, want %v（超阈值不加浮力）", e.VelY, want)
	}

	// 对照：空气中的掉落物受重力（-0.04）后垂直 ×0.98，水平 ×0.98。
	air := instance.spawnItem(world.DimensionOverworld, stack,
		float64(baseX)-5+0.5, float64(floorY)+3, float64(baseZ)+0.5, 0.1, 0, 0, 10_000)
	if air == nil {
		t.Fatal("spawnItem(air) returned nil")
	}
	instance.tick()
	if want := 0.1 * itemAirDrag; math.Abs(air.VelX-want) > 1e-9 {
		t.Fatalf("空中 vx = %v, want %v", air.VelX, want)
	}
	if want := -itemGravityPerTick * itemAirDrag; math.Abs(air.VelY-want) > 1e-9 {
		t.Fatalf("空中 vy = %v, want %v（重力后乘垂直阻尼）", air.VelY, want)
	}
}

// TestItemBounceAndSettleVanilla 验证掉落物落地反弹（原版垂直速度 ×-0.5）
// 并在阻尼后静止于地面。
func TestItemBounceAndSettleVanilla(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Bouncer")
	baseX, floorY, baseZ := fluidBase(t, instance)

	stack, err := item.FromName("minecraft:dirt", 1)
	if err != nil {
		t.Fatal(err)
	}
	dropped := instance.spawnItem(world.DimensionOverworld, stack,
		float64(baseX)+0.5, float64(floorY)+3, float64(baseZ)+0.5, 0, 0, 0, 10_000)
	if dropped == nil {
		t.Fatal("spawnItem returned nil")
	}

	sawUpward := false
	for i := 0; i < 120; i++ {
		instance.tick()
		if dropped.VelY > 0 {
			sawUpward = true
		}
	}
	if !sawUpward {
		t.Fatal("掉落物落地后从未反弹（原版为垂直速度 ×-0.5）")
	}
	_, ey, ez, exists := itemState(t, instance, dropped.ID)
	if !exists {
		t.Fatal("掉落物意外消失")
	}
	if math.Abs(ey-float64(floorY)) > 0.05 {
		t.Fatalf("静止高度 y = %.3f, want ≈ %d", ey, floorY)
	}
	if math.Abs(ez-float64(baseZ)-0.5) > 0.01 {
		t.Fatalf("掉落物不应漂移：z = %.3f", ez)
	}
	if math.Abs(dropped.VelY) > 0.05 {
		t.Fatalf("静止后 vy = %v, want ≈ 0", dropped.VelY)
	}
}

// TestItemFrictionOnIceVanilla 验证落地摩擦取脚下方块的 slipperiness：
// 冰面（0.98）上的掉落物明显比石头（0.6）滑得远。
func TestItemFrictionOnIceVanilla(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Slider")
	w := instance.testWorld()
	baseX, floorY, baseZ := fluidBase(t, instance)

	ice, ok := registry.BlockStateIDs["minecraft:ice"]
	if !ok {
		t.Fatal("缺少冰方块状态")
	}
	for dx := -3; dx <= 3; dx++ {
		w.SetBlock(baseX+dx, floorY-1, baseZ, ice)
	}

	stack, err := item.FromName("minecraft:dirt", 1)
	if err != nil {
		t.Fatal(err)
	}
	onIce := instance.spawnItem(world.DimensionOverworld, stack,
		float64(baseX)-2+0.5, float64(floorY), float64(baseZ)+0.5, 0.2, 0, 0, 10_000)
	onStone := instance.spawnItem(world.DimensionOverworld, stack,
		float64(baseX)-2+0.5, float64(floorY), float64(baseZ)+2.5, 0.2, 0, 0, 10_000)
	if onIce == nil || onStone == nil {
		t.Fatal("spawnItem returned nil")
	}
	runTicks(instance, 5)
	// 冰：每 tick 约 ×0.98×0.98（微弹跳帧为 ×0.98）→ 0.2 缩到约 0.16；
	// 石头：落地帧 ×0.6×0.98 → 0.2 快速衰减到 0.05 以下。
	if onIce.VelX < 0.12 {
		t.Fatalf("冰面 vx = %v, want > 0.12（滑）", onIce.VelX)
	}
	if onStone.VelX > 0.06 {
		t.Fatalf("石面 vx = %v, want < 0.06（摩擦大）", onStone.VelX)
	}
}

// TestEnvironmentBoxOverlapIgnites 验证火/水/岩浆判定按实体碰撞盒覆盖的方块：
// 站在相邻方块边缘、盒子越过边界的生物与玩家会被点燃（旧实现只看中心列，
// 会漏判）。
func TestEnvironmentBoxOverlapIgnites(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "EdgeFire")
	player := findSession(t, instance, "EdgeFire")
	processBarrier(t, conn)
	w := instance.testWorld()
	baseX, floorY, baseZ := fluidBase(t, instance)

	fire, ok := fireStateForAge("minecraft:fire", 0)
	if !ok {
		t.Fatal("缺少火方块状态")
	}
	// 火在 (baseX+1, floorY, baseZ)：生物/玩家站在相邻列（盒子越过 x=baseX+1 边界）。
	w.SetBlock(baseX+1, floorY, baseZ, fire)

	mob := instance.addMob(world.DimensionOverworld, mobZombie,
		float64(baseX)+0.75, float64(floorY), float64(baseZ)+0.5)
	if mob == nil {
		t.Fatal("addMob returned nil")
	}
	runTicks(instance, 1)
	if mob.FireTicks < mobFireBlockTicks {
		t.Fatalf("边缘生物未被点燃：fireTicks = %d, want ≥ %d", mob.FireTicks, mobFireBlockTicks)
	}

	// 玩家：盒子 x ∈ [0.45, 1.05]（相对 baseX）覆盖火方块。
	player.setPlayerPosition(float64(baseX)+0.75, float64(floorY), float64(baseZ)+0.5, 0, 0)
	runTicks(instance, 1)
	if ticks := player.fireTicks.Load(); ticks < mobFireBlockTicks {
		t.Fatalf("边缘玩家未被点燃：fireTicks = %d, want ≥ %d", ticks, mobFireBlockTicks)
	}
}

// TestEnvironmentBoxDoesNotOverreach 验证判定不过度：火在相邻方块但
// 实体盒完全不重叠时不点燃（防止退回“脚部/上方一格”式的粗判）。
func TestEnvironmentBoxDoesNotOverreach(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "NoEdgeFire")
	player := findSession(t, instance, "NoEdgeFire")
	processBarrier(t, conn)
	w := instance.testWorld()
	baseX, floorY, baseZ := fluidBase(t, instance)

	fire, ok := fireStateForAge("minecraft:fire", 0)
	if !ok {
		t.Fatal("缺少火方块状态")
	}
	w.SetBlock(baseX+1, floorY, baseZ, fire)

	// 玩家盒 x ∈ [-0.2, 0.4]（相对 baseX），与火方块 [1, 2] 不重叠。
	player.setPlayerPosition(float64(baseX)+0.1, float64(floorY), float64(baseZ)+0.5, 0, 0)
	runTicks(instance, 1)
	if ticks := player.fireTicks.Load(); ticks != 0 {
		t.Fatalf("不重叠的火方块点燃了玩家：fireTicks = %d, want 0", ticks)
	}
}
