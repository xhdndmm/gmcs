package server

import (
	"math"
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// placeState 在测试世界中放置一个属性方块状态并断言成功。
func placeState(t *testing.T, instance *Server, x, y, z int, name string, props map[string]string) uint16 {
	t.Helper()
	state, ok := registry.BlockStateWithProps(name, props)
	if !ok {
		t.Fatalf("state not found: %s %v", name, props)
	}
	if !instance.testWorld().SetBlock(x, y, z, state) {
		t.Fatalf("SetBlock failed at (%d,%d,%d)", x, y, z)
	}
	return state
}

// redstoneBase 给出红石测试用的基准位置（玩家出生点上方一格）。
func redstoneBase(instance *Server) (int, int, int) {
	spawnX, spawnY, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	return int(math.Floor(spawnX)), int(math.Floor(spawnY)), int(math.Floor(spawnZ))
}

// blockProps 返回世界中方块的状态名与属性。
func blockProps(t *testing.T, instance *Server, x, y, z int) (string, map[string]string) {
	t.Helper()
	name, props, ok := registry.StateProps(instance.testWorld().BlockAt(x, y, z))
	if !ok {
		t.Fatalf("no property state at (%d,%d,%d)", x, y, z)
	}
	return name, props
}

// TestRedstoneLeverPowersLamp 验证拉杆切换会点亮/熄灭相邻红石灯。
func TestRedstoneLeverPowersLamp(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	instance, _ := joinServer(t, cfg, "LeverPlayer")

	baseX, baseY, baseZ := redstoneBase(instance)
	leverY := baseY + 1
	// 清理拉杆与灯所在的位置（避免地形方块干扰）。
	for dx := 0; dx <= 1; dx++ {
		for dy := 0; dy <= 1; dy++ {
			instance.testWorld().SetBlock(baseX+dx, leverY+dy, baseZ, world.AirBlock)
		}
	}
	lever := placeState(t, instance, baseX, leverY, baseZ, leverName,
		map[string]string{"face": "floor", "facing": "north", "powered": "false"})
	placeState(t, instance, baseX+1, leverY, baseZ, lampName, map[string]string{"lit": "false"})

	// 第一次切换：拉杆 powered=true，相邻灯点亮。
	if !instance.handleRedstoneUse(instance.testWorld(), baseX, leverY, baseZ, lever) {
		t.Fatal("handleRedstoneUse(lever) = false, want true")
	}
	if name, props := blockProps(t, instance, baseX, leverY, baseZ); name != leverName || props["powered"] != "true" {
		t.Fatalf("lever state after toggle = %s %v, want powered=true", name, props)
	}
	if name, props := blockProps(t, instance, baseX+1, leverY, baseZ); name != lampName || props["lit"] != "true" {
		t.Fatalf("lamp after lever on = %s %v, want lit=true", name, props)
	}

	// 第二次切换：灯熄灭。
	currentLever := instance.testWorld().BlockAt(baseX, leverY, baseZ)
	if !instance.handleRedstoneUse(instance.testWorld(), baseX, leverY, baseZ, currentLever) {
		t.Fatal("handleRedstoneUse(lever) second time = false, want true")
	}
	if name, props := blockProps(t, instance, baseX+1, leverY, baseZ); name != lampName || props["lit"] != "false" {
		t.Fatalf("lamp after lever off = %s %v, want lit=false", name, props)
	}
}

// TestRedstoneButtonAutoRelease 验证按钮按下后自动弹起（石质 20 tick）。
func TestRedstoneButtonAutoRelease(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	instance, _ := joinServer(t, cfg, "ButtonPlayer")

	baseX, baseY, baseZ := redstoneBase(instance)
	buttonY := baseY + 1
	for dx := -1; dx <= 1; dx++ {
		for dy := 0; dy <= 2; dy++ {
			instance.testWorld().SetBlock(baseX+dx, buttonY+dy, baseZ, world.AirBlock)
		}
	}
	button := placeState(t, instance, baseX, buttonY, baseZ, stoneButtonName,
		map[string]string{"face": "floor", "facing": "north", "powered": "false"})
	placeState(t, instance, baseX+1, buttonY, baseZ, lampName, map[string]string{"lit": "false"})

	if !instance.handleRedstoneUse(instance.testWorld(), baseX, buttonY, baseZ, button) {
		t.Fatal("handleRedstoneUse(button) = false, want true")
	}
	if name, props := blockProps(t, instance, baseX, buttonY, baseZ); name != stoneButtonName || props["powered"] != "true" {
		t.Fatalf("button after press = %s %v, want powered=true", name, props)
	}
	if name, props := blockProps(t, instance, baseX+1, buttonY, baseZ); name != lampName || props["lit"] != "true" {
		t.Fatalf("lamp during press = %s %v, want lit=true", name, props)
	}

	// 计时器在 20 tick 后弹起。
	for i := 0; i < buttonStoneTicks-1; i++ {
		instance.tickRedstone()
	}
	if _, props := blockProps(t, instance, baseX, buttonY, baseZ); props["powered"] != "true" {
		t.Fatalf("button released too early (tick %d)", buttonStoneTicks-1)
	}
	instance.tickRedstone()
	if _, props := blockProps(t, instance, baseX, buttonY, baseZ); props["powered"] != "false" {
		t.Fatalf("button still powered after %d ticks", buttonStoneTicks)
	}
	if _, props := blockProps(t, instance, baseX+1, buttonY, baseZ); props["lit"] != "false" {
		t.Fatal("lamp still lit after the button released")
	}
}

// TestRedstoneWoodButtonTiming 验证木质按钮的弹起时间为 30 tick。
func TestRedstoneWoodButtonTiming(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	instance, _ := joinServer(t, cfg, "WoodButtonPlayer")

	baseX, baseY, baseZ := redstoneBase(instance)
	buttonY := baseY + 1
	instance.testWorld().SetBlock(baseX, buttonY, baseZ, world.AirBlock)
	button := placeState(t, instance, baseX, buttonY, baseZ, oakButtonName,
		map[string]string{"face": "floor", "facing": "north", "powered": "false"})
	if !instance.handleRedstoneUse(instance.testWorld(), baseX, buttonY, baseZ, button) {
		t.Fatal("handleRedstoneUse(wood button) = false, want true")
	}
	for i := 0; i < buttonWoodTicks-1; i++ {
		instance.tickRedstone()
	}
	if _, props := blockProps(t, instance, baseX, buttonY, baseZ); props["powered"] != "true" {
		t.Fatalf("wood button released too early (tick %d)", buttonWoodTicks-1)
	}
	instance.tickRedstone()
	if _, props := blockProps(t, instance, baseX, buttonY, baseZ); props["powered"] != "false" {
		t.Fatalf("wood button still powered after %d ticks", buttonWoodTicks)
	}
}

// TestRedstoneWireChain 验证红石线按“每格 -1”衰减的供电传播与灯的联动。
func TestRedstoneWireChain(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	instance, _ := joinServer(t, cfg, "WirePlayer")

	baseX, baseY, baseZ := redstoneBase(instance)
	wireY := baseY + 1
	for dx := 0; dx <= 4; dx++ {
		for dy := 0; dy <= 1; dy++ {
			instance.testWorld().SetBlock(baseX+dx, wireY+dy, baseZ, world.AirBlock)
		}
	}
	lever := placeState(t, instance, baseX, wireY, baseZ, leverName,
		map[string]string{"face": "floor", "facing": "north", "powered": "false"})
	// 三条红石线接在拉杆东侧。
	for dx := 1; dx <= 3; dx++ {
		placeState(t, instance, baseX+dx, wireY, baseZ, wireName,
			map[string]string{"east": "none", "north": "none", "power": "0", "south": "none", "west": "none"})
	}
	placeState(t, instance, baseX+4, wireY, baseZ, lampName, map[string]string{"lit": "false"})

	// 打开拉杆：线 power = 15、14、13，灯亮。
	if !instance.handleRedstoneUse(instance.testWorld(), baseX, wireY, baseZ, lever) {
		t.Fatal("handleRedstoneUse(lever) = false, want true")
	}
	wantPowers := []int{15, 14, 13}
	for dx := 1; dx <= 3; dx++ {
		state := instance.testWorld().BlockAt(baseX+dx, wireY, baseZ)
		if !isWireState(state) {
			t.Fatalf("block at offset %d is not a wire", dx)
		}
		if power := wirePowerOf(state); power != wantPowers[dx-1] {
			t.Fatalf("wire %d power = %d, want %d", dx, power, wantPowers[dx-1])
		}
	}
	if _, props := blockProps(t, instance, baseX+4, wireY, baseZ); props["lit"] != "true" {
		t.Fatal("lamp not lit by the powered wire")
	}

	// 关闭拉杆：全部归零，灯灭。
	currentLever := instance.testWorld().BlockAt(baseX, wireY, baseZ)
	if !instance.handleRedstoneUse(instance.testWorld(), baseX, wireY, baseZ, currentLever) {
		t.Fatal("handleRedstoneUse(lever off) = false, want true")
	}
	for dx := 1; dx <= 3; dx++ {
		if power := wirePowerOf(instance.testWorld().BlockAt(baseX+dx, wireY, baseZ)); power != 0 {
			t.Fatalf("wire %d power = %d after lever off, want 0", dx, power)
		}
	}
	if _, props := blockProps(t, instance, baseX+4, wireY, baseZ); props["lit"] != "false" {
		t.Fatal("lamp still lit after the lever turned off")
	}
}

// TestRedstoneBlockRemovedButtonTimer 验证按钮被破坏后计时器不再修改方块。
func TestRedstoneBlockRemovedButtonTimer(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	instance, _ := joinServer(t, cfg, "ButtonGone")

	baseX, baseY, baseZ := redstoneBase(instance)
	buttonY := baseY + 1
	instance.testWorld().SetBlock(baseX, buttonY, baseZ, world.AirBlock)
	button := placeState(t, instance, baseX, buttonY, baseZ, stoneButtonName,
		map[string]string{"face": "floor", "facing": "north", "powered": "false"})
	if !instance.handleRedstoneUse(instance.testWorld(), baseX, buttonY, baseZ, button) {
		t.Fatal("handleRedstoneUse(button) = false, want true")
	}
	// 破坏按钮（替换为空气）后计时器到期不应 panic 或写回按钮。
	instance.testWorld().SetBlock(baseX, buttonY, baseZ, world.AirBlock)
	for i := 0; i < buttonStoneTicks+2; i++ {
		instance.tickRedstone()
	}
	if state := instance.testWorld().BlockAt(baseX, buttonY, baseZ); state != world.AirBlock {
		t.Fatalf("removed button was restored by the timer: %d", state)
	}
}

// TestRedstoneRedstoneBlockSource 验证红石块作为恒定电源点亮相邻红石灯
// （通过放置红石块后调用 updateRedstoneAround 触发）。
func TestRedstoneBlockSource(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	instance, _ := joinServer(t, cfg, "BlockPlayer")

	baseX, baseY, baseZ := redstoneBase(instance)
	y := baseY + 1
	for dx := 0; dx <= 1; dx++ {
		instance.testWorld().SetBlock(baseX+dx, y, baseZ, world.AirBlock)
	}
	redstoneBlockState, ok := registry.BlockStateIDs[redstoneBlockName]
	if !ok {
		t.Fatalf("registry missing %s", redstoneBlockName)
	}
	if !instance.testWorld().SetBlock(baseX, y, baseZ, redstoneBlockState) {
		t.Fatal("SetBlock(redstone_block) failed")
	}
	placeState(t, instance, baseX+1, y, baseZ, lampName, map[string]string{"lit": "false"})
	instance.updateRedstoneAround(world.DimensionOverworld, baseX, y, baseZ)
	if _, props := blockProps(t, instance, baseX+1, y, baseZ); props["lit"] != "true" {
		t.Fatal("lamp not lit next to a redstone block")
	}
	// 拆除红石块后灯熄灭。
	instance.testWorld().SetBlock(baseX, y, baseZ, world.AirBlock)
	instance.updateRedstoneAround(world.DimensionOverworld, baseX, y, baseZ)
	if _, props := blockProps(t, instance, baseX+1, y, baseZ); props["lit"] != "false" {
		t.Fatal("lamp still lit after the redstone block was removed")
	}
}
