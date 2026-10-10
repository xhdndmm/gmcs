package server

import (
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// placeRepeater 在 (x, y, z) 放置指定朝向与延迟的中继器。
func placeRepeater(t *testing.T, instance *Server, x, y, z int, facing string, delay string) uint16 {
	t.Helper()
	return placeState(t, instance, x, y, z, repeaterName, map[string]string{
		"delay": delay, "facing": facing, "locked": "false", "powered": "false",
	})
}

// placeObserver 在 (x, y, z) 放置指定朝向的侦测器。
func placeObserver(t *testing.T, instance *Server, x, y, z int, facing string) uint16 {
	t.Helper()
	return placeState(t, instance, x, y, z, observerName, map[string]string{
		"facing": facing, "powered": "false",
	})
}

// TestRepeaterDelayAndDirection 验证中继器：只从背面输入、按延迟切换、
// 输出驱动红石灯。
func TestRepeaterDelayAndDirection(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	instance, _ := joinServer(t, cfg, "RepeaterPlayer")

	baseX, baseY, baseZ := redstoneBase(instance)
	y := baseY + 1
	for dx := -1; dx <= 3; dx++ {
		for dy := 0; dy <= 1; dy++ {
			instance.testWorld().SetBlock(baseX+dx, y+dy, baseZ, world.AirBlock)
		}
	}
	lever := placeState(t, instance, baseX, y, baseZ, leverName,
		map[string]string{"face": "floor", "facing": "north", "powered": "false"})
	placeRepeater(t, instance, baseX+1, y, baseZ, "east", "1")
	placeState(t, instance, baseX+2, y, baseZ, lampName, map[string]string{"lit": "false"})

	// 打开拉杆：中继器输入接通，但需要 1 红石刻（2 tick）延迟。
	if !instance.handleRedstoneUse(instance.testWorld(), baseX, y, baseZ, lever) {
		t.Fatal("lever toggle failed")
	}
	if _, props := blockProps(t, instance, baseX+1, y, baseZ); props["powered"] != "false" {
		t.Fatal("repeater powered before the delay elapsed")
	}
	instance.tickRedstone()
	if _, props := blockProps(t, instance, baseX+1, y, baseZ); props["powered"] != "false" {
		t.Fatal("repeater powered after only 1 tick (delay 1 = 2 ticks)")
	}
	instance.tickRedstone()
	if _, props := blockProps(t, instance, baseX+1, y, baseZ); props["powered"] != "true" {
		t.Fatal("repeater not powered after the delay elapsed")
	}
	if _, props := blockProps(t, instance, baseX+2, y, baseZ); props["lit"] != "true" {
		t.Fatal("lamp not lit by the powered repeater")
	}

	// 关闭拉杆：同样需要延迟，然后灯熄灭。
	currentLever := instance.testWorld().BlockAt(baseX, y, baseZ)
	if !instance.handleRedstoneUse(instance.testWorld(), baseX, y, baseZ, currentLever) {
		t.Fatal("lever toggle off failed")
	}
	instance.tickRedstone()
	if _, props := blockProps(t, instance, baseX+1, y, baseZ); props["powered"] != "true" {
		t.Fatal("repeater powered off too early")
	}
	instance.tickRedstone()
	if _, props := blockProps(t, instance, baseX+1, y, baseZ); props["powered"] != "false" {
		t.Fatal("repeater still powered after the off delay")
	}
	if _, props := blockProps(t, instance, baseX+2, y, baseZ); props["lit"] != "false" {
		t.Fatal("lamp still lit after the repeater turned off")
	}

	// 方向性：从输出侧（东侧）供电不会点亮中继器。
	lever = placeState(t, instance, baseX+2, y, baseZ, leverName,
		map[string]string{"face": "floor", "facing": "north", "powered": "false"})
	instance.handleRedstoneUse(instance.testWorld(), baseX+2, y, baseZ, lever)
	for i := 0; i < 10; i++ {
		instance.tickRedstone()
	}
	if _, props := blockProps(t, instance, baseX+1, y, baseZ); props["powered"] != "false" {
		t.Fatal("repeater powered from its output side (should only accept input from the back)")
	}
}

// TestRepeaterDelay4 验证 delay=4 的中继器需要 8 tick 才切换。
func TestRepeaterDelay4(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	instance, _ := joinServer(t, cfg, "DelayPlayer")

	baseX, baseY, baseZ := redstoneBase(instance)
	y := baseY + 1
	for dx := -1; dx <= 2; dx++ {
		instance.testWorld().SetBlock(baseX+dx, y, baseZ, world.AirBlock)
	}
	lever := placeState(t, instance, baseX, y, baseZ, leverName,
		map[string]string{"face": "floor", "facing": "north", "powered": "false"})
	placeRepeater(t, instance, baseX+1, y, baseZ, "east", "4")

	instance.handleRedstoneUse(instance.testWorld(), baseX, y, baseZ, lever)
	for i := 0; i < 7; i++ {
		instance.tickRedstone()
	}
	if _, props := blockProps(t, instance, baseX+1, y, baseZ); props["powered"] != "false" {
		t.Fatal("delay-4 repeater powered before 8 ticks")
	}
	instance.tickRedstone()
	if _, props := blockProps(t, instance, baseX+1, y, baseZ); props["powered"] != "true" {
		t.Fatal("delay-4 repeater not powered after 8 ticks")
	}
}

// TestRepeaterRightClickCyclesDelay 验证右键循环调整延迟 1→2→3→4→1。
func TestRepeaterRightClickCyclesDelay(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	instance, _ := joinServer(t, cfg, "CyclePlayer")

	baseX, baseY, baseZ := redstoneBase(instance)
	y := baseY + 1
	instance.testWorld().SetBlock(baseX, y, baseZ, world.AirBlock)
	state := placeRepeater(t, instance, baseX, y, baseZ, "north", "1")

	for _, want := range []string{"2", "3", "4", "1"} {
		if !instance.handleRedstoneUse(instance.testWorld(), baseX, y, baseZ, state) {
			t.Fatal("repeater right-click should be handled")
		}
		state = instance.testWorld().BlockAt(baseX, y, baseZ)
		if _, props := blockProps(t, instance, baseX, y, baseZ); props["delay"] != want {
			t.Fatalf("delay after cycle = %s, want %s", props["delay"], want)
		}
	}
}

// TestObserverPulse 验证侦测器：观察的方块变化时输出 2 tick 脉冲，
// 脉冲驱动输出侧（背面）的红石灯，随后自动断电。
func TestObserverPulse(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	instance, _ := joinServer(t, cfg, "ObserverPlayer")

	baseX, baseY, baseZ := redstoneBase(instance)
	y := baseY + 1
	for dx := -2; dx <= 2; dx++ {
		for dy := 0; dy <= 1; dy++ {
			instance.testWorld().SetBlock(baseX+dx, y+dy, baseZ, world.AirBlock)
		}
	}
	placeObserver(t, instance, baseX, y, baseZ, "east") // 观察东侧方块
	instance.testWorld().SetBlock(baseX+1, y, baseZ, world.StoneBlock)
	placeState(t, instance, baseX-1, y, baseZ, lampName, map[string]string{"lit": "false"})

	// 与被观察方块无关的变化：不触发。
	instance.testWorld().SetBlock(baseX-2, y, baseZ, world.StoneBlock)
	instance.broadcastBlockUpdate(world.DimensionOverworld, baseX-2, y, baseZ, int32(world.StoneBlock))
	if _, props := blockProps(t, instance, baseX, y, baseZ); props["powered"] != "false" {
		t.Fatal("observer fired for an unrelated block change")
	}

	// 破坏被观察方块：侦测器立即通电，输出侧灯亮。
	if !instance.testWorld().SetBlock(baseX+1, y, baseZ, world.AirBlock) {
		t.Fatal("breaking the observed block failed")
	}
	instance.broadcastBlockUpdate(world.DimensionOverworld, baseX+1, y, baseZ, int32(world.AirBlock))
	if _, props := blockProps(t, instance, baseX, y, baseZ); props["powered"] != "true" {
		t.Fatal("observer did not fire when the observed block changed")
	}
	if _, props := blockProps(t, instance, baseX-1, y, baseZ); props["lit"] != "true" {
		t.Fatal("lamp behind the observer not lit during the pulse")
	}

	// 2 tick 后脉冲结束：侦测器断电，灯熄灭。
	instance.tickRedstone()
	if _, props := blockProps(t, instance, baseX, y, baseZ); props["powered"] != "true" {
		t.Fatal("observer pulse ended too early")
	}
	instance.tickRedstone()
	if _, props := blockProps(t, instance, baseX, y, baseZ); props["powered"] != "false" {
		t.Fatal("observer still powered after the pulse")
	}
	if _, props := blockProps(t, instance, baseX-1, y, baseZ); props["lit"] != "false" {
		t.Fatal("lamp still lit after the pulse ended")
	}
}

// TestObserverPowersWire 验证侦测器脉冲通过红石线传播（脉冲转瞬态信号）。
func TestObserverPowersWire(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	instance, _ := joinServer(t, cfg, "ObserverWire")

	baseX, baseY, baseZ := redstoneBase(instance)
	y := baseY + 1
	for dx := -2; dx <= 3; dx++ {
		for dy := 0; dy <= 1; dy++ {
			instance.testWorld().SetBlock(baseX+dx, y+dy, baseZ, world.AirBlock)
		}
	}
	placeObserver(t, instance, baseX, y, baseZ, "up") // 观察上方方块
	instance.testWorld().SetBlock(baseX, y+1, baseZ, world.StoneBlock)
	// 侦测器输出侧 = 下方（facing=up 的反方向）：线在下方一格。
	placeState(t, instance, baseX, y-1, baseZ, wireName,
		map[string]string{"east": "none", "north": "none", "power": "0", "south": "none", "west": "none"})
	placeState(t, instance, baseX+1, y-1, baseZ, lampName, map[string]string{"lit": "false"})

	instance.testWorld().SetBlock(baseX, y+1, baseZ, world.AirBlock)
	instance.broadcastBlockUpdate(world.DimensionOverworld, baseX, y+1, baseZ, int32(world.AirBlock))
	if power := wirePowerOf(instance.testWorld().BlockAt(baseX, y-1, baseZ)); power == 0 {
		t.Fatalf("wire below the observer not powered (power=%d)", power)
	}
	if _, props := blockProps(t, instance, baseX+1, y-1, baseZ); props["lit"] != "true" {
		t.Fatal("lamp at the end of the wire not lit during the observer pulse")
	}
	instance.tickRedstone()
	instance.tickRedstone()
	if power := wirePowerOf(instance.testWorld().BlockAt(baseX, y-1, baseZ)); power != 0 {
		t.Fatalf("wire still powered after the pulse (power=%d)", power)
	}
}

// TestRegistryHasRepeaterAndObserverStates 验证生成的状态表包含中继器/侦测器
// 关键属性组合（放置与切换逻辑依赖它们）。
func TestRegistryHasRepeaterAndObserverStates(t *testing.T) {
	for _, delay := range []string{"1", "2", "3", "4"} {
		if _, ok := registry.BlockStateWithProps(repeaterName, map[string]string{
			"delay": delay, "facing": "north", "locked": "false", "powered": "false",
		}); !ok {
			t.Fatalf("repeater state (delay=%s, powered=false) missing", delay)
		}
		if _, ok := registry.BlockStateWithProps(repeaterName, map[string]string{
			"delay": delay, "facing": "north", "locked": "false", "powered": "true",
		}); !ok {
			t.Fatalf("repeater state (delay=%s, powered=true) missing", delay)
		}
	}
	for _, facing := range []string{"north", "south", "west", "east", "up", "down"} {
		if _, ok := registry.BlockStateWithProps(observerName, map[string]string{
			"facing": facing, "powered": "false",
		}); !ok {
			t.Fatalf("observer state (facing=%s) missing", facing)
		}
	}
}
