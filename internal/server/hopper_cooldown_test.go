//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"
	"testing"

	"gmcs/internal/item"
	"gmcs/internal/world"
)

// hopperEntityCooldown 读取漏斗方块实体里持久化的传送冷却。
func hopperEntityCooldown(t *testing.T, instance *Server, x, y, z int) (int32, bool) {
	t.Helper()
	entity, err := instance.testWorld().BlockEntityAt(x, y, z)
	if err != nil {
		t.Fatal(err)
	}
	return entity.TransferCooldown, len(entity.Items) > 0 || entity.TransferCooldown > 0
}

// TestHopperCooldownPersisted 验证漏斗传送冷却随方块实体持久化（原版 TransferCooldown）：
// 搬运后冷却=8 写盘；逐 tick 递减也会就地写回；重新加载后从冷却继续，而不是立即搬运。
func TestHopperCooldownPersisted(t *testing.T) {
	instance := newHopperTestServer(t)
	spawnX, _, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	x, z := int(math.Floor(spawnX))+8, int(math.Floor(spawnZ))+8
	groundY := hopperTestPad(t, instance, x, z, 4)

	placeBlockAt(t, instance, x, groundY-1, z, "minecraft:chest")
	hopper := hopperAt(t, instance, x, groundY, z, "down")
	stone, err := item.FromName("minecraft:stone", 3)
	if err != nil {
		t.Fatal(err)
	}
	setHopperSlot(hopper, 0, stone)

	// 第一 tick 搬运 1 个：冷却被置为 8 并写回方块实体。
	instance.tick()
	if cooldown, ok := hopperEntityCooldown(t, instance, x, groundY, z); !ok || cooldown != hopperMoveItemSpeed {
		t.Fatalf("搬运后方块实体冷却 = %d (存在=%v), want %d", cooldown, ok, hopperMoveItemSpeed)
	}

	// 再推进 3 tick：冷却逐 tick 递减并就地写回（7、6、5）。
	for i := 0; i < 3; i++ {
		instance.tick()
	}
	if cooldown, _ := hopperEntityCooldown(t, instance, x, groundY, z); cooldown != 5 {
		t.Fatalf("3 tick 后方块实体冷却 = %d, want 5", cooldown)
	}

	// 模拟重载：丢弃运行时状态后重新注册，冷却应从方块实体恢复。
	instance.dropHoppersInChunks(world.DimensionOverworld, []world.ChunkPos{{X: x >> 4, Z: z >> 4}})
	reloaded := instance.registerHopper(world.DimensionOverworld, x, groundY, z)
	if reloaded == nil {
		t.Fatal("重新注册失败")
	}
	reloaded.mu.Lock()
	restored := reloaded.cooldown
	reloaded.mu.Unlock()
	if restored != 5 {
		t.Fatalf("重载后冷却 = %d, want 5", restored)
	}

	// 重载后剩余 5 tick 内不应搬运（冷却真正生效，而非按 0 重新计时）。
	for i := 0; i < 4; i++ {
		instance.tick()
	}
	if got := hopperSlotAt(reloaded, 0).Count; got != 2 {
		t.Fatalf("重载后冷却未生效：漏斗剩余 = %d, want 2", got)
	}
	instance.tick() // 第 8 个 tick（重载前 4 + 重载后 4）
	if got := hopperSlotAt(reloaded, 0).Count; got != 1 {
		t.Fatalf("冷却结束后应继续搬运：漏斗剩余 = %d, want 1", got)
	}
}

// TestHopperCooldownRecordDroppedWhenIdle 验证漏斗排空且冷却归零后，
// 方块实体记录被清除（冷却>0 期间记录保留，才使重启后能恢复节流）。
func TestHopperCooldownRecordDroppedWhenIdle(t *testing.T) {
	instance := newHopperTestServer(t)
	spawnX, _, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	x, z := int(math.Floor(spawnX))+8, int(math.Floor(spawnZ))+8
	groundY := hopperTestPad(t, instance, x, z, 4)

	placeBlockAt(t, instance, x, groundY-1, z, "minecraft:chest")
	hopper := hopperAt(t, instance, x, groundY, z, "down")
	stone, err := item.FromName("minecraft:stone", 1)
	if err != nil {
		t.Fatal(err)
	}
	setHopperSlot(hopper, 0, stone)

	// 推出唯一的物品：漏斗排空但冷却仍 > 0，记录保留。
	instance.tick()
	if _, ok := hopperEntityCooldown(t, instance, x, groundY, z); !ok {
		t.Fatal("冷却期间漏斗方块实体记录应保留")
	}
	if cooldown, _ := hopperEntityCooldown(t, instance, x, groundY, z); cooldown != hopperMoveItemSpeed {
		t.Fatalf("排空后冷却 = %d, want %d", cooldown, hopperMoveItemSpeed)
	}

	// 冷却归零且槽位全空：记录被删除。
	for i := 0; i < hopperMoveItemSpeed; i++ {
		instance.tick()
	}
	entity, err := instance.testWorld().BlockEntityAt(x, groundY, z)
	if err != nil {
		t.Fatal(err)
	}
	if !entity.Empty() {
		t.Fatalf("冷却结束后的空漏斗不应保留方块实体：%+v", entity)
	}
}
