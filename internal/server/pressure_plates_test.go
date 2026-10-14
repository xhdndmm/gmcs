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

// 压力板测试：检测盒、实体过滤（木质=全部实体、石质=仅生物）、
// 加权信号公式、松开延迟与红石传播。

// setupPlateArea 在玩家附近铺出测试场地并返回压力板坐标（玩家脚部所在方块）。
func setupPlateArea(t *testing.T, instance *Server, player *session, offsetX, offsetZ int) (bx, by, bz int) {
	t.Helper()
	x, y, z, _, _ := player.playerPosition()
	px, py, pz := int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))
	clearPlayerArea(instance, px+offsetX, py-1, pz+offsetZ, 2, 4)
	return px + offsetX, py, pz + offsetZ
}

// TestWoodenPressurePlateRespondsToPlayer 验证木质压力板：玩家站上立即按下
// （powered=true 并点亮相邻红石灯），离开后 20 tick 弹起、灯熄灭。
func TestWoodenPressurePlateRespondsToPlayer(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "PlateWalker")
	player := findSession(t, instance, "PlateWalker")
	processBarrier(t, conn)

	bx, by, bz := setupPlateArea(t, instance, player, 3, 0)
	placeState(t, instance, bx, by, bz, "minecraft:oak_pressure_plate", map[string]string{"powered": "false"})
	placeState(t, instance, bx+1, by, bz, lampName, map[string]string{"lit": "false"})

	// 玩家站到压力板上：立即按下。
	player.setPlayerPosition(float64(bx)+0.5, float64(by), float64(bz)+0.5, 0, 0)
	if name, props := blockProps(t, instance, bx, by, bz); name != "minecraft:oak_pressure_plate" || props["powered"] != "true" {
		t.Fatalf("踩上后压力板 = %s %v, want powered=true", name, props)
	}
	// 红石灯点亮（相邻方块供电）。
	instance.tickRedstone()
	if name, props := blockProps(t, instance, bx+1, by, bz); name != lampName || props["lit"] != "true" {
		t.Fatalf("灯 = %s %v, want lit=true", name, props)
	}

	// 玩家离开：20 tick 后弹起。
	player.setPlayerPosition(float64(bx)+8.5, float64(by), float64(bz)+0.5, 0, 0)
	for i := 0; i <= plateLightReleaseTicks+1; i++ {
		instance.tickRedstone()
	}
	if name, props := blockProps(t, instance, bx, by, bz); name != "minecraft:oak_pressure_plate" || props["powered"] != "false" {
		t.Fatalf("离开后压力板 = %s %v, want powered=false", name, props)
	}
}

// TestStonePressurePlateIgnoresItems 验证石质压力板只检测生物：
// 掉落物不会触发石质压力板，但会触发木质压力板。
func TestStonePressurePlateIgnoresItems(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "PlateItems")
	player := findSession(t, instance, "PlateItems")
	processBarrier(t, conn)

	stoneX, by, stoneZ := setupPlateArea(t, instance, player, 5, 0)
	woodX, _, woodZ := setupPlateArea(t, instance, player, 5, 3)
	placeState(t, instance, stoneX, by, stoneZ, "minecraft:stone_pressure_plate", map[string]string{"powered": "false"})
	placeState(t, instance, woodX, by, woodZ, "minecraft:oak_pressure_plate", map[string]string{"powered": "false"})

	// 在每块压力板正上方各放一个掉落物（拾取延迟很长，避免被玩家拾取）。
	stoneStack, err := item.FromName("minecraft:stone", 1)
	if err != nil {
		t.Fatal(err)
	}
	woodStack, err := item.FromName("minecraft:dirt", 1)
	if err != nil {
		t.Fatal(err)
	}
	instance.spawnItem(world.DimensionOverworld, stoneStack, float64(stoneX)+0.5, float64(by)+0.2, float64(stoneZ)+0.5, 0, 0, 0, 10000)
	instance.spawnItem(world.DimensionOverworld, woodStack, float64(woodX)+0.5, float64(by)+0.2, float64(woodZ)+0.5, 0, 0, 0, 10000)

	// 推进若干 tick 让掉落物落到压力板上。
	for i := 0; i < 10; i++ {
		instance.tick()
	}
	if _, props := blockProps(t, instance, stoneX, by, stoneZ); props["powered"] != "false" {
		t.Fatalf("石质压力板被掉落物触发：%v", props)
	}
	if _, props := blockProps(t, instance, woodX, by, woodZ); props["powered"] != "true" {
		t.Fatalf("木质压力板未被掉落物触发：%v", props)
	}

	// 掉落物移走后木质压力板恢复（20 tick 释放 + 网络重算）。
	instance.entityMu.Lock()
	instance.items = make(map[int32]*itemEntity)
	instance.entityMu.Unlock()
	for i := 0; i <= plateLightReleaseTicks+1; i++ {
		instance.tickRedstone()
	}
	if _, props := blockProps(t, instance, woodX, by, woodZ); props["powered"] != "false" {
		t.Fatalf("掉落物移除后木质压力板未弹起：%v", props)
	}
}

// TestWeightedPressurePlateSignalByEntityCount 验证加权压力板信号公式：
// 金板 ceil(n × 15 / 15) = n；铁板 ceil(n × 15 / 150) = 1（3 个实体）。
func TestWeightedPressurePlateSignalByEntityCount(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "PlateWeight")
	player := findSession(t, instance, "PlateWeight")
	processBarrier(t, conn)

	goldX, by, goldZ := setupPlateArea(t, instance, player, 5, 0)
	ironX, _, ironZ := setupPlateArea(t, instance, player, 5, 3)
	placeState(t, instance, goldX, by, goldZ, "minecraft:light_weighted_pressure_plate", map[string]string{"power": "0"})
	placeState(t, instance, ironX, by, ironZ, "minecraft:heavy_weighted_pressure_plate", map[string]string{"power": "0"})
	// 相邻红石线（先摆好，压力板按下时会重算线网）。
	placeState(t, instance, ironX+1, by, ironZ, wireName, map[string]string{
		"north": "none", "east": "none", "south": "none", "west": "side", "power": "0",
	})

	// 每种压力板上放 3 个不同物品的掉落物（避免同类掉落物合并）。
	names := []string{"minecraft:stone", "minecraft:dirt", "minecraft:sand"}
	for i, name := range names {
		stack, err := item.FromName(name, 1)
		if err != nil {
			t.Fatal(err)
		}
		dx := float64(i) * 0.1
		instance.spawnItem(world.DimensionOverworld, stack, float64(goldX)+0.5+dx, float64(by)+0.2, float64(goldZ)+0.5, 0, 0, 0, 10000)
		stack2, err := item.FromName(name, 1)
		if err != nil {
			t.Fatal(err)
		}
		instance.spawnItem(world.DimensionOverworld, stack2, float64(ironX)+0.5+dx, float64(by)+0.2, float64(ironZ)+0.5, 0, 0, 0, 10000)
	}
	for i := 0; i < 10; i++ {
		instance.tick()
	}
	if _, props := blockProps(t, instance, goldX, by, goldZ); props["power"] != "3" {
		t.Fatalf("金压力板 power = %q, want 3", props["power"])
	}
	if _, props := blockProps(t, instance, ironX, by, ironZ); props["power"] != "1" {
		t.Fatalf("铁压力板 power = %q, want 1", props["power"])
	}

	// 铁压力板信号强度参与红石传播：相邻红石线 power 变为 1。
	instance.tickRedstone()
	if _, props := blockProps(t, instance, ironX+1, by, ironZ); props["power"] == "0" {
		t.Fatalf("红石线未收到压力板信号：%v", props)
	}
}

// TestWeightedPressurePlateReleasesAfterTenTicks 验证加权压力板松开延迟为 10 tick。
func TestWeightedPressurePlateReleasesAfterTenTicks(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "PlateRelease")
	player := findSession(t, instance, "PlateRelease")
	processBarrier(t, conn)

	bx, by, bz := setupPlateArea(t, instance, player, 3, 0)
	placeState(t, instance, bx, by, bz, "minecraft:light_weighted_pressure_plate", map[string]string{"power": "0"})

	player.setPlayerPosition(float64(bx)+0.5, float64(by), float64(bz)+0.5, 0, 0)
	// 金压力板：1 个实体 → ceil(1 × 15 / 15) = 1。
	if _, props := blockProps(t, instance, bx, by, bz); props["power"] != "1" {
		t.Fatalf("玩家站上金压力板 power = %q, want 1", props["power"])
	}
	player.setPlayerPosition(float64(bx)+8.5, float64(by), float64(bz)+0.5, 0, 0)
	// 9 tick 时仍按下（释放延迟 10 tick）。
	for i := 0; i < plateWeightedReleaseTicks-1; i++ {
		instance.tickRedstone()
	}
	if _, props := blockProps(t, instance, bx, by, bz); props["power"] != "1" {
		t.Fatalf("9 tick 时金压力板 = %q, want 1", props["power"])
	}
	instance.tickRedstone()
	if _, props := blockProps(t, instance, bx, by, bz); props["power"] != "0" {
		t.Fatalf("10 tick 后金压力板 = %q, want 0", props["power"])
	}
}

// TestPressurePlateStateTable 验证压力板状态表按注册表数据构建
// （全部 16 种压力板均有状态；非压力板不在表中）。
func TestPressurePlateStateTable(t *testing.T) {
	if len(plateInfos) != 16 {
		t.Fatalf("压力板种类 = %d, want 16", len(plateInfos))
	}
	if entry, ok := plateStateTable[placeStateProbe(t, "minecraft:oak_pressure_plate", "powered", "true")]; !ok || entry.signal != 15 {
		t.Fatalf("木质压力板按下状态未登记或信号错误：%+v", entry)
	}
	if entry, ok := plateStateTable[placeStateProbe(t, "minecraft:heavy_weighted_pressure_plate", "power", "7")]; !ok || entry.signal != 7 {
		t.Fatalf("铁压力板 power=7 状态信号错误：%+v", entry)
	}
	if _, ok := plateStateTable[world.StoneBlock]; ok {
		t.Fatal("普通方块不应出现在压力板状态表中")
	}
}

// placeStateProbe 返回指定方块状态的注册表 ID（不写入世界）。
func placeStateProbe(t *testing.T, name, key, value string) uint16 {
	t.Helper()
	state, ok := registry.BlockStateWithProps(name, map[string]string{key: value})
	if !ok {
		t.Fatalf("state not found: %s %s=%s", name, key, value)
	}
	return state
}
