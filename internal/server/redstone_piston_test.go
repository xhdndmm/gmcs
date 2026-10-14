//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// testPlayer 返回首个已加入的玩家会话（测试辅助）。
func testPlayer(t *testing.T, instance *Server) *session {
	t.Helper()
	players := instance.playerSnapshot()
	if len(players) == 0 {
		t.Fatal("no joined player session")
	}
	return players[0]
}

// placePiston 在 (x, y, z) 放置未伸展的活塞。
func placePiston(t *testing.T, instance *Server, x, y, z int, name, facing string) uint16 {
	t.Helper()
	return placeState(t, instance, x, y, z, name, map[string]string{
		"facing": facing, "extended": "false",
	})
}

// clearRedstoneArea 把测试区域清空。
func clearRedstoneArea(instance *Server, x, y, z, dx, dy, dz int) {
	for ix := 0; ix <= dx; ix++ {
		for iy := 0; iy <= dy; iy++ {
			for iz := 0; iz <= dz; iz++ {
				instance.testWorld().SetBlock(x+ix, y+iy, z+iz, world.AirBlock)
			}
		}
	}
}

// toggleLever 切换拉杆状态并断言成功。
func toggleLever(t *testing.T, instance *Server, x, y, z int) {
	t.Helper()
	state := instance.testWorld().BlockAt(x, y, z)
	if !instance.handleRedstoneUse(instance.testWorld(), x, y, z, state) {
		t.Fatalf("lever toggle at (%d,%d,%d) failed", x, y, z)
	}
}

// TestPistonExtendsAndRetracts 验证活塞：拉杆供电后伸展（放置活塞头），
// 断电后收回。
func TestPistonExtendsAndRetracts(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	instance, _ := joinServer(t, cfg, "PistonPlayer")

	baseX, baseY, baseZ := redstoneBase(instance)
	y := baseY + 1
	clearRedstoneArea(instance, baseX-1, y, baseZ, 4, 1, 0)
	placeState(t, instance, baseX-1, y, baseZ, leverName,
		map[string]string{"face": "floor", "facing": "north", "powered": "false"})
	placePiston(t, instance, baseX, y, baseZ, pistonName, "east")

	// 供电：活塞安排 1 tick 计划刻后伸展。
	toggleLever(t, instance, baseX-1, y, baseZ)
	if _, props := blockProps(t, instance, baseX, y, baseZ); props["extended"] != "false" {
		t.Fatal("piston extended before its scheduled tick")
	}
	instance.tickRedstone()
	if name, props := blockProps(t, instance, baseX, y, baseZ); name != pistonName || props["extended"] != "true" {
		t.Fatalf("piston after power = %s %v, want extended=true", name, props)
	}
	headName, headProps := blockProps(t, instance, baseX+1, y, baseZ)
	if headName != pistonHeadName || headProps["facing"] != "east" || headProps["type"] != "normal" {
		t.Fatalf("piston head = %s %v, want piston_head facing=east type=normal", headName, headProps)
	}

	// 断电：再次安排计划刻后收回，头部消失。
	toggleLever(t, instance, baseX-1, y, baseZ)
	instance.tickRedstone()
	if _, props := blockProps(t, instance, baseX, y, baseZ); props["extended"] != "false" {
		t.Fatal("piston still extended after losing power")
	}
	if state := instance.testWorld().BlockAt(baseX+1, y, baseZ); state != world.AirBlock {
		t.Fatalf("piston head still present after retraction (state=%d)", state)
	}
}

// TestPistonPushesBlocks 验证活塞把前方方块整体推动一格。
func TestPistonPushesBlocks(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	instance, _ := joinServer(t, cfg, "PistonPush")

	baseX, baseY, baseZ := redstoneBase(instance)
	y := baseY + 1
	clearRedstoneArea(instance, baseX-1, y, baseZ, 6, 1, 0)
	placeState(t, instance, baseX-1, y, baseZ, leverName,
		map[string]string{"face": "floor", "facing": "north", "powered": "false"})
	placePiston(t, instance, baseX, y, baseZ, pistonName, "east")
	instance.testWorld().SetBlock(baseX+1, y, baseZ, world.StoneBlock)
	instance.testWorld().SetBlock(baseX+2, y, baseZ, world.DirtBlock)

	toggleLever(t, instance, baseX-1, y, baseZ)
	instance.tickRedstone()

	if name, _ := blockProps(t, instance, baseX+1, y, baseZ); name != pistonHeadName {
		t.Fatalf("head block = %s, want piston_head", name)
	}
	if instance.testWorld().BlockAt(baseX+2, y, baseZ) != world.StoneBlock {
		t.Fatal("stone block did not move one cell")
	}
	if instance.testWorld().BlockAt(baseX+3, y, baseZ) != world.DirtBlock {
		t.Fatal("dirt block did not move one cell")
	}
}

// TestPistonPushLimit 验证 12 格推动上限：13 个方块时活塞不伸展，
// 减少到 12 个后可以伸展。
func TestPistonPushLimit(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	instance, _ := joinServer(t, cfg, "PistonLimit")

	baseX, baseY, baseZ := redstoneBase(instance)
	y := baseY + 1
	clearRedstoneArea(instance, baseX-1, y, baseZ, 15, 1, 0)
	placeState(t, instance, baseX-1, y, baseZ, leverName,
		map[string]string{"face": "floor", "facing": "north", "powered": "false"})
	placePiston(t, instance, baseX, y, baseZ, pistonName, "east")
	for dx := 1; dx <= 13; dx++ {
		instance.testWorld().SetBlock(baseX+dx, y, baseZ, world.StoneBlock)
	}

	toggleLever(t, instance, baseX-1, y, baseZ)
	instance.tickRedstone()
	if _, props := blockProps(t, instance, baseX, y, baseZ); props["extended"] != "false" {
		t.Fatal("piston extended although 13 blocks exceed the 12-block limit")
	}
	if instance.testWorld().BlockAt(baseX+1, y, baseZ) != world.StoneBlock {
		t.Fatal("front block moved although the piston should be blocked")
	}

	// 去掉第 13 个方块，重新供电后可以推动 12 个方块。
	instance.testWorld().SetBlock(baseX+13, y, baseZ, world.AirBlock)
	toggleLever(t, instance, baseX-1, y, baseZ) // 关
	instance.tickRedstone()
	toggleLever(t, instance, baseX-1, y, baseZ) // 开
	instance.tickRedstone()
	if _, props := blockProps(t, instance, baseX, y, baseZ); props["extended"] != "true" {
		t.Fatal("piston did not extend with 12 blocks in front")
	}
	if instance.testWorld().BlockAt(baseX+13, y, baseZ) != world.StoneBlock {
		t.Fatal("12-block chain did not shift by one")
	}
}

// TestPistonBlockedByImmovable 验证黑曜石阻挡活塞。
func TestPistonBlockedByImmovable(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	instance, _ := joinServer(t, cfg, "PistonObsidian")

	baseX, baseY, baseZ := redstoneBase(instance)
	y := baseY + 1
	clearRedstoneArea(instance, baseX-1, y, baseZ, 3, 1, 0)
	placeState(t, instance, baseX-1, y, baseZ, leverName,
		map[string]string{"face": "floor", "facing": "north", "powered": "false"})
	placePiston(t, instance, baseX, y, baseZ, pistonName, "east")
	instance.testWorld().SetBlock(baseX+1, y, baseZ, world.ObsidianBlock)

	toggleLever(t, instance, baseX-1, y, baseZ)
	instance.tickRedstone()
	if _, props := blockProps(t, instance, baseX, y, baseZ); props["extended"] != "false" {
		t.Fatal("piston extended into obsidian")
	}
}

// TestStickyPistonPullsBlock 验证粘性活塞收回时把前方第二格方块拉回。
func TestStickyPistonPullsBlock(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	instance, _ := joinServer(t, cfg, "PistonSticky")

	baseX, baseY, baseZ := redstoneBase(instance)
	y := baseY + 1
	clearRedstoneArea(instance, baseX-1, y, baseZ, 4, 1, 0)
	placeState(t, instance, baseX-1, y, baseZ, leverName,
		map[string]string{"face": "floor", "facing": "north", "powered": "false"})
	placePiston(t, instance, baseX, y, baseZ, stickyPistonName, "east")
	instance.testWorld().SetBlock(baseX+2, y, baseZ, world.StoneBlock)

	toggleLever(t, instance, baseX-1, y, baseZ)
	instance.tickRedstone()
	if name, props := blockProps(t, instance, baseX, y, baseZ); name != stickyPistonName || props["extended"] != "true" {
		t.Fatalf("sticky piston after power = %s %v, want extended=true", name, props)
	}
	if name, props := blockProps(t, instance, baseX+1, y, baseZ); name != pistonHeadName || props["type"] != "sticky" {
		t.Fatalf("sticky piston head = %s %v, want type=sticky", name, props)
	}

	// 断电收回：第二格的石头被拉到头部原来的格子（+1）。
	toggleLever(t, instance, baseX-1, y, baseZ)
	instance.tickRedstone()
	if _, props := blockProps(t, instance, baseX, y, baseZ); props["extended"] != "false" {
		t.Fatal("sticky piston still extended after losing power")
	}
	if instance.testWorld().BlockAt(baseX+1, y, baseZ) != world.StoneBlock {
		t.Fatal("sticky piston did not pull the block back")
	}
	if instance.testWorld().BlockAt(baseX+2, y, baseZ) != world.AirBlock {
		t.Fatal("pulled block left a copy behind")
	}
}

// TestStickyPistonDoesNotPullPushOnly 验证粘性活塞不会拉回 PUSH_ONLY
// 反应方块（如滑石/釉面陶瓦）。
func TestStickyPistonDoesNotPullPushOnly(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	instance, _ := joinServer(t, cfg, "PistonPushOnly")

	baseX, baseY, baseZ := redstoneBase(instance)
	y := baseY + 1
	clearRedstoneArea(instance, baseX-1, y, baseZ, 4, 1, 0)
	placeState(t, instance, baseX-1, y, baseZ, leverName,
		map[string]string{"face": "floor", "facing": "north", "powered": "false"})
	placePiston(t, instance, baseX, y, baseZ, stickyPistonName, "east")
	glazed, ok := registry.BlockStateIDs["minecraft:white_glazed_terracotta"]
	if !ok {
		t.Fatal("missing white_glazed_terracotta state")
	}
	instance.testWorld().SetBlock(baseX+2, y, baseZ, glazed)

	toggleLever(t, instance, baseX-1, y, baseZ)
	instance.tickRedstone()
	toggleLever(t, instance, baseX-1, y, baseZ)
	instance.tickRedstone()
	if instance.testWorld().BlockAt(baseX+2, y, baseZ) != glazed {
		t.Fatal("PUSH_ONLY block was pulled back by the sticky piston")
	}
}

// TestPistonDestroysDESTROYBlocks 验证伸展时具有 DESTROY 推反应的方块
// 被直接破坏（并掉落物品）。
func TestPistonDestroysDESTROYBlocks(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	instance, _ := joinServer(t, cfg, "PistonDestroy")

	baseX, baseY, baseZ := redstoneBase(instance)
	y := baseY + 1
	clearRedstoneArea(instance, baseX-1, y, baseZ, 3, 1, 0)
	placeState(t, instance, baseX-1, y, baseZ, leverName,
		map[string]string{"face": "floor", "facing": "north", "powered": "false"})
	placePiston(t, instance, baseX, y, baseZ, pistonName, "east")
	sapling, ok := registry.BlockStateIDs["minecraft:oak_sapling"]
	if !ok {
		t.Fatal("missing oak_sapling state")
	}
	instance.testWorld().SetBlock(baseX+1, y, baseZ, sapling)

	instance.entityMu.Lock()
	itemsBefore := len(instance.items)
	instance.entityMu.Unlock()

	toggleLever(t, instance, baseX-1, y, baseZ)
	instance.tickRedstone()
	if name, _ := blockProps(t, instance, baseX+1, y, baseZ); name != pistonHeadName {
		t.Fatalf("head block = %s, want piston_head (sapling should be destroyed)", name)
	}
	if instance.itemsEnabled {
		instance.entityMu.Lock()
		itemsAfter := len(instance.items)
		instance.entityMu.Unlock()
		if itemsAfter <= itemsBefore {
			t.Fatal("destroyed sapling did not drop an item")
		}
	}
}

// TestPistonQC 验证准连接（QC）：活塞上方的格子被供电时激活。
func TestPistonQC(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	instance, _ := joinServer(t, cfg, "PistonQC")

	baseX, baseY, baseZ := redstoneBase(instance)
	y := baseY + 1
	clearRedstoneArea(instance, baseX-1, y, baseZ, 4, 3, 0)
	placePiston(t, instance, baseX, y, baseZ, pistonName, "east")
	// 活塞上方的格子放一个通电的拉杆：活塞通过 QC 被激活。
	placeState(t, instance, baseX, y+1, baseZ, leverName,
		map[string]string{"face": "floor", "facing": "north", "powered": "true"})
	instance.updateRedstoneAround(world.DimensionOverworld, baseX, y+1, baseZ)
	instance.tickRedstone()
	if _, props := blockProps(t, instance, baseX, y, baseZ); props["extended"] != "true" {
		t.Fatal("piston with QC power above did not extend")
	}
}

// TestPistonHeadBreakRemovesBase 验证破坏活塞头会移除基座。
func TestPistonHeadBreakRemovesBase(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	instance, _ := joinServer(t, cfg, "PistonHeadDig")
	player := testPlayer(t, instance)

	baseX, baseY, baseZ := redstoneBase(instance)
	y := baseY + 1
	clearRedstoneArea(instance, baseX-1, y, baseZ, 4, 1, 0)
	placeState(t, instance, baseX-1, y, baseZ, leverName,
		map[string]string{"face": "floor", "facing": "north", "powered": "false"})
	placePiston(t, instance, baseX, y, baseZ, pistonName, "east")
	toggleLever(t, instance, baseX-1, y, baseZ)
	instance.tickRedstone()

	head := instance.testWorld().BlockAt(baseX+1, y, baseZ)
	if name, _ := blockProps(t, instance, baseX+1, y, baseZ); name != pistonHeadName {
		t.Fatalf("expected a piston head before breaking, got %s", name)
	}
	if !instance.handlePistonBreak(world.DimensionOverworld, player, instance.testWorld(),
		baseX+1, y, baseZ, head) {
		t.Fatal("handlePistonBreak(head) = false, want true")
	}
	if state := instance.testWorld().BlockAt(baseX, y, baseZ); state != world.AirBlock {
		t.Fatal("piston base still present after breaking the head")
	}
	if state := instance.testWorld().BlockAt(baseX+1, y, baseZ); state != world.AirBlock {
		t.Fatal("piston head still present after being broken")
	}
}

// TestPistonBaseBreakRemovesHead 验证破坏伸展的活塞基座时头部一并消失。
func TestPistonBaseBreakRemovesHead(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	instance, _ := joinServer(t, cfg, "PistonBaseDig")
	player := testPlayer(t, instance)

	baseX, baseY, baseZ := redstoneBase(instance)
	y := baseY + 1
	clearRedstoneArea(instance, baseX-1, y, baseZ, 4, 1, 0)
	placeState(t, instance, baseX-1, y, baseZ, leverName,
		map[string]string{"face": "floor", "facing": "north", "powered": "false"})
	placePiston(t, instance, baseX, y, baseZ, pistonName, "east")
	toggleLever(t, instance, baseX-1, y, baseZ)
	instance.tickRedstone()

	base := instance.testWorld().BlockAt(baseX, y, baseZ)
	if !instance.handlePistonBreak(world.DimensionOverworld, player, instance.testWorld(),
		baseX, y, baseZ, base) {
		t.Fatal("handlePistonBreak(base) = false, want true")
	}
	if state := instance.testWorld().BlockAt(baseX+1, y, baseZ); state != world.AirBlock {
		t.Fatal("piston head survived after breaking the base")
	}
}

// TestPistonPlacementOrientation 验证活塞按玩家视线方向放置
// （facing = 视线反方向，俯仰角决定上下）。
func TestPistonPlacementOrientation(t *testing.T) {
	state, ok := registry.BlockStateIDs[pistonName]
	if !ok {
		t.Fatal("missing piston state")
	}
	yawCases := []struct {
		yaw  float32
		want string
	}{
		// 原版：朝向 = 视线方向的反方向（活塞/中继器/比较器都朝玩家方向）。
		{0, "north"},   // 看向南 → 朝北
		{90, "east"},   // 看向西 → 朝东
		{180, "south"}, // 看向北 → 朝南
		{270, "west"},  // 看向东 → 朝西
	}
	for _, tc := range yawCases {
		got := orientPlacementState(state, tc.yaw, 0)
		_, props, ok := registry.StateProps(got)
		if !ok || props["facing"] != tc.want {
			t.Fatalf("yaw %v: facing = %v, want %s", tc.yaw, props, tc.want)
		}
		if props["extended"] != "false" {
			t.Fatalf("yaw %v: placed piston is extended", tc.yaw)
		}
	}
	orientFacing := func(pitch float32) string {
		got := orientPlacementState(state, 0, pitch)
		_, props, _ := registry.StateProps(got)
		return props["facing"]
	}
	if got := orientFacing(-90); got != "down" {
		t.Fatalf("looking straight up placed a piston facing %s, want down", got)
	}
	if got := orientFacing(90); got != "up" {
		t.Fatalf("looking straight down placed a piston facing %s, want up", got)
	}
}

// TestComparatorModeCycleAndOutput 验证比较器右键切换模式，以及容器读取：
// 空箱输出 0、满箱输出 15。
func TestComparatorModeCycleAndOutput(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	instance, _ := joinServer(t, cfg, "ComparatorPlayer")

	baseX, baseY, baseZ := redstoneBase(instance)
	y := baseY + 1
	clearRedstoneArea(instance, baseX-1, y, baseZ, 3, 1, 0)
	chest, ok := registry.BlockStateIDs["minecraft:chest"]
	if !ok {
		t.Fatal("missing chest state")
	}
	instance.testWorld().SetBlock(baseX+1, y, baseZ, chest)
	state := placeState(t, instance, baseX, y, baseZ, comparatorName,
		map[string]string{"facing": "east", "mode": "compare", "powered": "false"})

	// 右键循环 compare → subtract → compare。
	if !instance.handleRedstoneUse(instance.testWorld(), baseX, y, baseZ, state) {
		t.Fatal("comparator right-click should be handled")
	}
	if _, props := blockProps(t, instance, baseX, y, baseZ); props["mode"] != "subtract" {
		t.Fatal("comparator mode did not switch to subtract")
	}
	state = instance.testWorld().BlockAt(baseX, y, baseZ)
	instance.handleRedstoneUse(instance.testWorld(), baseX, y, baseZ, state)
	if _, props := blockProps(t, instance, baseX, y, baseZ); props["mode"] != "compare" {
		t.Fatal("comparator mode did not switch back to compare")
	}

	pos := [3]int{baseX, y, baseZ}
	// 空箱：输出 0。
	if signal := instance.comparatorOutputSignal(instance.testWorld(), pos,
		instance.testWorld().BlockAt(baseX, y, baseZ)); signal != 0 {
		t.Fatalf("empty chest output = %d, want 0", signal)
	}

	// 满箱：输出 15。
	itemID, err := registry.ItemID("minecraft:stone")
	if err != nil {
		t.Fatal(err)
	}
	entity := world.BlockEntity{Items: make([]world.ContainerItem, 27)}
	for i := range entity.Items {
		entity.Items[i] = world.ContainerItem{ItemID: itemID, Count: 64}
	}
	if err := instance.testWorld().SetBlockEntity(baseX+1, y, baseZ, entity); err != nil {
		t.Fatal(err)
	}
	if signal := instance.comparatorOutputSignal(instance.testWorld(), pos,
		instance.testWorld().BlockAt(baseX, y, baseZ)); signal != 15 {
		t.Fatalf("full chest output = %d, want 15", signal)
	}
}

// TestComparatorSideSignalBlocksOutput 验证比较器侧输入：侧向信号大于输入
// 时输出归零（原版 calculateOutputSignal 的侧输入压制规则）。
func TestComparatorSideSignalBlocksOutput(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	instance, _ := joinServer(t, cfg, "ComparatorSide")

	baseX, baseY, baseZ := redstoneBase(instance)
	y := baseY + 1
	clearRedstoneArea(instance, baseX-3, y-1, baseZ-2, 7, 3, 4)
	// 输入：拉杆 → 线(15) → 线(14) → 比较器输入侧（facing=west）。
	placeState(t, instance, baseX-3, y, baseZ, leverName,
		map[string]string{"face": "floor", "facing": "north", "powered": "false"})
	placeState(t, instance, baseX-2, y, baseZ, wireName,
		map[string]string{"east": "none", "north": "none", "power": "0", "south": "none", "west": "none"})
	placeState(t, instance, baseX-1, y, baseZ, wireName,
		map[string]string{"east": "none", "north": "none", "power": "0", "south": "none", "west": "none"})
	placeState(t, instance, baseX, y, baseZ, comparatorName,
		map[string]string{"facing": "west", "mode": "compare", "powered": "false"})

	toggleLever(t, instance, baseX-3, y, baseZ)
	pos := [3]int{baseX, y, baseZ}
	state := instance.testWorld().BlockAt(baseX, y, baseZ)
	if signal := instance.comparatorOutputSignal(instance.testWorld(), pos, state); signal != 14 {
		t.Fatalf("comparator output with wire input = %d, want 14", signal)
	}

	// 侧向 15（线紧邻拉杆）：侧 > 输入 → 输出归零。
	placeState(t, instance, baseX, y, baseZ-2, leverName,
		map[string]string{"face": "floor", "facing": "north", "powered": "false"})
	placeState(t, instance, baseX, y, baseZ-1, wireName,
		map[string]string{"east": "none", "north": "none", "power": "0", "south": "none", "west": "none"})
	toggleLever(t, instance, baseX, y, baseZ-2)
	instance.updateRedstoneAround(world.DimensionOverworld, baseX, y, baseZ-1)
	state = instance.testWorld().BlockAt(baseX, y, baseZ)
	if signal := instance.comparatorOutputSignal(instance.testWorld(), pos, state); signal != 0 {
		t.Fatalf("comparator output with side signal = %d, want 0", signal)
	}

	// 减法模式：输入 14 - 侧 15 < 0 → 0；先去掉侧输入改成 3 再验证减法。
	placeState(t, instance, baseX, y, baseZ-1, wireName,
		map[string]string{"east": "none", "north": "none", "power": "3", "south": "none", "west": "none"})
	state = placeState(t, instance, baseX, y, baseZ, comparatorName,
		map[string]string{"facing": "west", "mode": "subtract", "powered": "false"})
	if signal := instance.comparatorOutputSignal(instance.testWorld(), pos, state); signal != 11 {
		t.Fatalf("subtract comparator output = %d, want 11", signal)
	}
}

// TestRepeaterLockedBySideDiode 验证中继器被侧向中继器锁定：锁定期间输出冻结。
func TestRepeaterLockedBySideDiode(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	instance, _ := joinServer(t, cfg, "RepeaterLock")

	baseX, baseY, baseZ := redstoneBase(instance)
	y := baseY + 1
	clearRedstoneArea(instance, baseX-2, y, baseZ-2, 5, 1, 2)
	placeState(t, instance, baseX-1, y, baseZ, leverName,
		map[string]string{"face": "floor", "facing": "north", "powered": "false"})
	placeRepeater(t, instance, baseX, y, baseZ, "west", "1")
	// 侧面（北侧）中继器朝南指向被锁定的中继器：facing=north 表示输入在北侧，
	// 输出在南侧。
	placeState(t, instance, baseX, y, baseZ-2, leverName,
		map[string]string{"face": "floor", "facing": "north", "powered": "true"})
	placeRepeater(t, instance, baseX, y, baseZ-1, "north", "1")

	// 侧向中继器通电（拉杆直接放置为 powered=true，手动触发更新）→ 主中继器锁定。
	instance.updateRedstoneAround(world.DimensionOverworld, baseX, y, baseZ-2)
	for i := 0; i < 4; i++ {
		instance.tickRedstone()
	}
	sideName, sideProps := blockProps(t, instance, baseX, y, baseZ-1)
	if sideName != repeaterName || sideProps["powered"] != "true" {
		t.Fatalf("side repeater = %s %v, want powered=true", sideName, sideProps)
	}
	if _, props := blockProps(t, instance, baseX, y, baseZ); props["locked"] != "true" {
		t.Fatalf("repeater not locked by the side diode: %v", props)
	}

	// 锁定期间给输入供电：输出保持关闭。
	toggleLever(t, instance, baseX-1, y, baseZ)
	for i := 0; i < 6; i++ {
		instance.tickRedstone()
	}
	if _, props := blockProps(t, instance, baseX, y, baseZ); props["powered"] != "false" {
		t.Fatal("locked repeater turned on")
	}
}

// TestTorchExtinguishAndRelight 验证红石火把：支撑方块被供电时熄灭，
// 断电后重新点亮（各 2 tick 延迟）。
func TestTorchExtinguishAndRelight(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	instance, _ := joinServer(t, cfg, "TorchPlayer")

	baseX, baseY, baseZ := redstoneBase(instance)
	y := baseY + 1
	clearRedstoneArea(instance, baseX-1, y-1, baseZ, 4, 3, 1)
	instance.testWorld().SetBlock(baseX, y, baseZ, world.StoneBlock)
	placeState(t, instance, baseX, y+1, baseZ, redstoneTorchName, map[string]string{"lit": "true"})
	// 墙式拉杆贴在支撑方块西侧：facing 指向附着方块方向与其相反的方向
	// （原版 connectedDirection = facing），支撑在东侧故 facing=west。
	placeState(t, instance, baseX-1, y, baseZ, leverName,
		map[string]string{"face": "wall", "facing": "west", "powered": "false"})

	toggleLever(t, instance, baseX-1, y, baseZ)
	instance.tickRedstone()
	if _, props := blockProps(t, instance, baseX, y+1, baseZ); props["lit"] != "true" {
		t.Fatal("torch went out before the 2-tick delay")
	}
	instance.tickRedstone()
	if _, props := blockProps(t, instance, baseX, y+1, baseZ); props["lit"] != "false" {
		t.Fatal("torch did not go out when its support block was powered")
	}

	// 断电后重新点亮。
	toggleLever(t, instance, baseX-1, y, baseZ)
	instance.tickRedstone()
	instance.tickRedstone()
	if _, props := blockProps(t, instance, baseX, y+1, baseZ); props["lit"] != "true" {
		t.Fatal("torch did not relight after the support block lost power")
	}
}

// TestStrongPoweringFromRepeater 验证中继器指向导体方块时的强充能：
// 方块上方的红石线被点亮（中继器 → 方块 → 红石线）。
func TestStrongPoweringFromRepeater(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	instance, _ := joinServer(t, cfg, "StrongPower")

	baseX, baseY, baseZ := redstoneBase(instance)
	y := baseY + 1
	clearRedstoneArea(instance, baseX-1, y, baseZ, 5, 2, 0)
	placeState(t, instance, baseX-1, y, baseZ, leverName,
		map[string]string{"face": "floor", "facing": "north", "powered": "false"})
	placeRepeater(t, instance, baseX, y, baseZ, "west", "1")
	// 中继器输出侧（东）的导体方块，其上方一格放红石线。
	instance.testWorld().SetBlock(baseX+1, y, baseZ, world.StoneBlock)
	placeState(t, instance, baseX+1, y+1, baseZ, wireName,
		map[string]string{"east": "none", "north": "none", "power": "0", "south": "none", "west": "none"})

	toggleLever(t, instance, baseX-1, y, baseZ)
	for i := 0; i < 4; i++ {
		instance.tickRedstone()
	}
	power := wirePowerOf(instance.testWorld().BlockAt(baseX+1, y+1, baseZ))
	if power == 0 {
		t.Fatal("wire above the strongly powered block is not powered")
	}
}

// TestWireVerticalPropagation 验证红石线可以跨一格向上传播
// （原版 getIncomingWireSignal 的绕行规则）。
func TestWireVerticalPropagation(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	instance, _ := joinServer(t, cfg, "WireVertical")

	baseX, baseY, baseZ := redstoneBase(instance)
	y := baseY + 1
	clearRedstoneArea(instance, baseX-1, y, baseZ, 4, 3, 0)
	placeState(t, instance, baseX, y, baseZ, leverName,
		map[string]string{"face": "floor", "facing": "north", "powered": "false"})
	placeState(t, instance, baseX+1, y, baseZ, wireName,
		map[string]string{"east": "none", "north": "none", "power": "0", "south": "none", "west": "none"})
	// 第二格线在斜上方一格：邻居格为空气时按“从下方绕行”供电。
	placeState(t, instance, baseX+2, y+1, baseZ, wireName,
		map[string]string{"east": "none", "north": "none", "power": "0", "south": "none", "west": "none"})

	toggleLever(t, instance, baseX, y, baseZ)
	if power := wirePowerOf(instance.testWorld().BlockAt(baseX+1, y, baseZ)); power != 15 {
		t.Fatalf("wire beside the lever power = %d, want 15", power)
	}
	if power := wirePowerOf(instance.testWorld().BlockAt(baseX+2, y+1, baseZ)); power != 14 {
		t.Fatalf("wire one block up and over power = %d, want 14", power)
	}
}

// TestWireForcesStraightThroughConnection 验证线段的端点会生成贯通连接
// （原版 getConnectionState 的直线规则）：单格线能点亮旁边的红石灯。
func TestWireForcesStraightThroughConnection(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	instance, _ := joinServer(t, cfg, "WireForcing")

	baseX, baseY, baseZ := redstoneBase(instance)
	y := baseY + 1
	clearRedstoneArea(instance, baseX-1, y, baseZ, 4, 2, 2)
	placeState(t, instance, baseX, y, baseZ, leverName,
		map[string]string{"face": "floor", "facing": "north", "powered": "false"})
	placeState(t, instance, baseX+1, y, baseZ, wireName,
		map[string]string{"east": "none", "north": "none", "power": "0", "south": "none", "west": "none"})
	placeState(t, instance, baseX+2, y, baseZ, lampName, map[string]string{"lit": "false"})

	toggleLever(t, instance, baseX, y, baseZ)
	instance.updateRedstoneAround(world.DimensionOverworld, baseX+1, y, baseZ)
	connState := instance.testWorld().BlockAt(baseX+1, y, baseZ)
	_, props, ok := registry.StateProps(connState)
	if !ok {
		t.Fatal("wire state is not a property state")
	}
	if props["east"] != "side" {
		t.Fatalf("wire east connection = %s, want side (straight-through rule)", props["east"])
	}
	if _, lampProps := blockProps(t, instance, baseX+2, y, baseZ); lampProps["lit"] != "true" {
		t.Fatal("lamp beside the wire's forced connection is not lit")
	}
}

// TestWireDoesNotPowerBlockAbove 验证红石线不向上一格供电（原版
// getSignal 中 direction == DOWN 时的规则）。
func TestWireDoesNotPowerBlockAbove(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	instance, _ := joinServer(t, cfg, "WireAbove")

	baseX, baseY, baseZ := redstoneBase(instance)
	y := baseY + 1
	clearRedstoneArea(instance, baseX-1, y, baseZ, 4, 3, 0)
	placeState(t, instance, baseX, y, baseZ, leverName,
		map[string]string{"face": "floor", "facing": "north", "powered": "false"})
	placeState(t, instance, baseX+1, y, baseZ, wireName,
		map[string]string{"east": "none", "north": "none", "power": "0", "south": "none", "west": "none"})
	// 灯在线正上方一格。
	placeState(t, instance, baseX+1, y+1, baseZ, lampName, map[string]string{"lit": "false"})

	toggleLever(t, instance, baseX, y, baseZ)
	if _, props := blockProps(t, instance, baseX+1, y, baseZ); props["power"] != "15" {
		t.Fatalf("wire power = %s, want 15", props["power"])
	}
	if _, props := blockProps(t, instance, baseX+1, y+1, baseZ); props["lit"] != "false" {
		t.Fatal("lamp directly above a powered wire should not light")
	}
}
