//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"encoding/binary"
	"testing"
	"time"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"
)

// 堆肥桶测试：
//   - 右键填入/取出骨粉的窗口外交互 e2e（含世界事件与消耗）；
//   - level 7 → 8 的 20 tick 定时转换与兜底扫描恢复；
//   - 漏斗交互（上方插入、下方抽取、错误面无效）。

// composterLevelAt 读取指定位置堆肥桶的 level（非堆肥桶返回 -1）。
func composterLevelAt(t *testing.T, instance *Server, x, y, z int) int {
	t.Helper()
	return composterLevelOf(instance.testWorld().BlockAt(x, y, z))
}

// setComposterLevel 直接把指定位置设置为给定 level 的堆肥桶。
func setComposterLevel(t *testing.T, instance *Server, x, y, z, level int) {
	t.Helper()
	state, ok := composterStateWithLevel(level)
	if !ok {
		t.Fatalf("缺少堆肥桶 level=%d 的状态", level)
	}
	if !instance.testWorld().SetBlock(x, y, z, state) {
		t.Fatalf("SetBlock(composter level=%d) 失败", level)
	}
}

// TestComposterUseFlow 验证右键堆肥桶全流程：
// 空桶必定成功（level 0 → 1，世界事件 1500 成功、消耗 1 个物品）；
// 用 100% 成功率的物品填满至 7 → 20 tick 后就绪（8）→ 空手取出骨粉重置为 0。
func TestComposterUseFlow(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "ComposterUser")
	defer conn.Close()

	x, y, z := chestPositions(t, instance)
	setComposterLevel(t, instance, x, y, z, 0)

	player := activeSession(t, instance, "ComposterUser")
	player.inventory.Set(item.SlotHotbarStart, testStack(t, "minecraft:pumpkin_pie", 8))

	// 第 1 次使用：空桶必定成功（世界事件 1500 成功）。
	sendUseItemOn(t, conn, x, y, z, 1)
	event := expectPlayPacket(t, conn, protocol.PlayPacketIDWorldEvent)
	_, eventOffset, err := protocol.DecodeVarInt(event)
	if err != nil {
		t.Fatal(err)
	}
	if len(event) < eventOffset+16 ||
		binary.BigEndian.Uint32(event[eventOffset:eventOffset+4]) != composterFillEvent ||
		binary.BigEndian.Uint32(event[eventOffset+12:eventOffset+16]) != 1 {
		t.Fatalf("世界事件应为 1500/成功：% x", event)
	}
	waitFor(t, func() bool { return composterLevelAt(t, instance, x, y, z) == 1 }, "空桶填入应必定成功（level 0 → 1）")
	waitFor(t, func() bool { return player.inventory.Get(item.SlotHotbarStart).Count == 7 }, "填入应消耗 1 个物品")

	// 继续填满至 7（南瓜派成功率 1.0，必定成功）。
	for want := 2; want <= 7; want++ {
		sendUseItemOn(t, conn, x, y, z, 1)
		w := want
		waitFor(t, func() bool { return composterLevelAt(t, instance, x, y, z) == w }, "堆肥桶应提升到 level")
	}
	// level 7：使用可堆肥物品不填入、不消耗（原版直接返回成功）。
	// joinServer 禁用后台 Tick（tickInterval = 0），等待读循环处理后再断言。
	sendUseItemOn(t, conn, x, y, z, 1)
	time.Sleep(100 * time.Millisecond)
	if level := composterLevelAt(t, instance, x, y, z); level != 7 {
		t.Fatalf("level 7 的使用不应改变状态：level=%d", level)
	}
	if got := player.inventory.Get(item.SlotHotbarStart); got.Count != 1 {
		t.Fatalf("level 7 的使用不应消耗物品：%+v", got)
	}

	// 手动推进 Tick：level 7 → 8 由 20 tick 定时转换驱动。
	for i := 0; i < composterReadyDelay+5 && composterLevelAt(t, instance, x, y, z) != 8; i++ {
		instance.tick()
	}
	if level := composterLevelAt(t, instance, x, y, z); level != 8 {
		t.Fatalf("level 7 应在 %d tick 后就绪：level=%d", composterReadyDelay, level)
	}

	// 空手取出骨粉：生成掉落物、重置为 0。
	player.inventory.Set(item.SlotHotbarStart, item.Empty())
	sendUseItemOn(t, conn, x, y, z, 1)
	expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)
	waitFor(t, func() bool { return composterLevelAt(t, instance, x, y, z) == 0 }, "取出骨粉后应重置为 0")
}

// TestComposterReadyRecovery 验证 level 7 → 8 的兜底扫描：
// 未登记调度的 level 7（模拟重启恢复）会在扫描周期内被重新调度并转换。
func TestComposterReadyRecovery(t *testing.T) {
	instance := newHopperTestServer(t)
	x, z := 40, 40
	groundY := hopperTestPad(t, instance, x, z, 2)
	setComposterLevel(t, instance, x, groundY+1, z, 7)
	for i := 0; i < composterFullScanInterval+composterReadyDelay+20; i++ {
		instance.tick()
		if composterLevelAt(t, instance, x, groundY+1, z) == 8 {
			return
		}
	}
	t.Fatal("level 7 应在兜底扫描周期内转换为 8")
}

// TestComposterHopperInsert 验证上方漏斗向堆肥桶插入：
// 每次 1 个、立即尝试填入（南瓜派必定成功）、物品被消耗。
func TestComposterHopperInsert(t *testing.T) {
	instance := newHopperTestServer(t)
	x, z := 40, 40
	groundY := hopperTestPad(t, instance, x, z, 3)
	setComposterLevel(t, instance, x, groundY+1, z, 0)
	hopper := hopperAt(t, instance, x, groundY+2, z, "down")
	setHopperSlot(hopper, 0, testStack(t, "minecraft:pumpkin_pie", 1))

	for i := 0; i < 200 && composterLevelAt(t, instance, x, groundY+1, z) == 0; i++ {
		instance.tick()
	}
	if level := composterLevelAt(t, instance, x, groundY+1, z); level != 1 {
		t.Fatalf("上方漏斗应填入成功：level=%d, want 1", level)
	}
	if got := hopperSlotAt(hopper, 0); !got.IsEmpty() {
		t.Fatalf("插入的物品应被消耗：%+v", got)
	}
}

// TestComposterHopperExtract 验证下方漏斗从满桶（level 8）抽出骨粉，
// 抽空后堆肥桶重置为 0。
func TestComposterHopperExtract(t *testing.T) {
	instance := newHopperTestServer(t)
	x, z := 40, 40
	groundY := hopperTestPad(t, instance, x, z, 3)
	setComposterLevel(t, instance, x, groundY+1, z, 8)
	hopper := hopperAt(t, instance, x, groundY, z, "down")

	for i := 0; i < 200 && hopperSlotAt(hopper, 0).IsEmpty(); i++ {
		instance.tick()
	}
	got := hopperSlotAt(hopper, 0)
	boneMealID, err := item.FromName("minecraft:bone_meal", 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.IsEmpty() || got.ItemID != boneMealID.ItemID || got.Count != 1 {
		t.Fatalf("下方漏斗应获得 1 个骨粉：%+v", got)
	}
	if level := composterLevelAt(t, instance, x, groundY+1, z); level != 0 {
		t.Fatalf("抽空后堆肥桶应重置为 0：level=%d", level)
	}
}

// TestComposterHopperFaceRules 验证错误面无交互（原版 InputContainer 只有上方、
// OutputContainer 只有下方）：
//   - 侧面漏斗不能插入空桶；
//   - 上方漏斗不能向满桶插入。
func TestComposterHopperFaceRules(t *testing.T) {
	instance := newHopperTestServer(t)
	x, z := 40, 40
	groundY := hopperTestPad(t, instance, x, z, 3)

	// 侧面漏斗（东侧向西推入）：不能插入。
	setComposterLevel(t, instance, x, groundY+1, z, 0)
	side := hopperAt(t, instance, x+1, groundY+1, z, "west")
	setHopperSlot(side, 0, testStack(t, "minecraft:pumpkin_pie", 1))
	for i := 0; i < 120; i++ {
		instance.tick()
	}
	if level := composterLevelAt(t, instance, x, groundY+1, z); level != 0 {
		t.Fatalf("侧面漏斗不应能插入：level=%d", level)
	}
	if got := hopperSlotAt(side, 0); got.IsEmpty() {
		t.Fatal("原料应保留在侧面漏斗中")
	}

	// 上方漏斗向满桶插入：OutputContainer 无上方槽位，不生效。
	setComposterLevel(t, instance, x, groundY+1, z, 8)
	top := hopperAt(t, instance, x, groundY+2, z, "down")
	setHopperSlot(top, 0, testStack(t, "minecraft:pumpkin_pie", 1))
	for i := 0; i < 120; i++ {
		instance.tick()
	}
	if level := composterLevelAt(t, instance, x, groundY+1, z); level != 8 {
		t.Fatalf("满桶不应接受插入：level=%d", level)
	}
	if got := hopperSlotAt(top, 0); got.IsEmpty() {
		t.Fatal("原料应保留在漏斗中（满桶不可插入）")
	}
}
