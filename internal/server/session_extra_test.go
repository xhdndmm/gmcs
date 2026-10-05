package server

import (
	"math"
	"testing"
	"time"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
	"gmcs/internal/world"
)

// TestHealthRegeneration 验证脱战回血：受伤后 8 秒内不回血，
// 之后每 4 秒（80 tick）恢复 1 点，并同步给客户端。
func TestHealthRegeneration(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Healer")
	player := findSession(t, instance, "Healer")

	if !instance.damagePlayer(player, 5, "Zombie", -1, instance.mobAttackDamageTypeID, nil) {
		t.Fatal("damage should apply")
	}
	// 排空受伤产生的数据包（伤害事件 → 生命值 → 音效）。
	expectPlayPacket(t, conn, protocol.PlayPacketIDDamageEvent)
	expectPlayPacket(t, conn, protocol.PlayPacketIDUpdateHealth)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSoundEffect)
	if health, _, _ := player.healthStatus(); health != maxPlayerHealth-5 {
		t.Fatalf("health = %v after damage", health)
	}

	// 脱战延迟内不恢复。
	for i := 0; i < playerRegenIntervalTicks; i++ {
		instance.tick()
	}
	if health, _, _ := player.healthStatus(); health != maxPlayerHealth-5 {
		t.Fatalf("health = %v during regen delay, want %v", health, maxPlayerHealth-5)
	}

	// 把上次受伤时间提前到延迟之外，推进一个恢复周期。
	player.stateMu.Lock()
	player.lastHurt = time.Now().Add(-playerRegenDelay - time.Second)
	player.stateMu.Unlock()
	for i := 0; i < playerRegenIntervalTicks; i++ {
		instance.tick()
	}
	if health, _, _ := player.healthStatus(); health != maxPlayerHealth-4 {
		t.Fatalf("health = %v after regen cycle, want %v", health, maxPlayerHealth-4)
	}
	healthPacket := expectPlayPacket(t, conn, protocol.PlayPacketIDUpdateHealth)
	_, offset, err := protocol.DecodeVarInt(healthPacket)
	if err != nil {
		t.Fatal(err)
	}
	health, _, err := protocol.DecodeFloat32(healthPacket, offset)
	if err != nil || health != maxPlayerHealth-4 {
		t.Fatalf("update health packet = %v (err=%v)", health, err)
	}
}

// TestSplitItemCount 验证初始物品数量解析。
func TestSplitItemCount(t *testing.T) {
	cases := []struct {
		entry string
		name  string
		count int32
	}{
		{"minecraft:stone", "minecraft:stone", 1},
		{"minecraft:stone*64", "minecraft:stone", 64},
		{" minecraft:stone * 16 ", "minecraft:stone", 16},
		{"minecraft:stone*abc", "minecraft:stone", 1},
		{"minecraft:stone*0", "minecraft:stone", 1},
		{"minecraft:stone*999", "minecraft:stone", 64},
	}
	for _, test := range cases {
		name, count := splitItemCount(test.entry)
		if name != test.name || count != test.count {
			t.Fatalf("splitItemCount(%q) = (%q, %d), want (%q, %d)",
				test.entry, name, count, test.name, test.count)
		}
	}
}

// TestMobSideStepAvoidance 验证被方块挡住的生物会触发随机侧移（简单避障）。
func TestMobSideStepAvoidance(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Blocked")

	spawnX, spawnY, spawnZ := instance.spawnPosition()
	mob := instance.addMob(spawnX, spawnY, spawnZ+1.9)
	expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)

	// 在生物与玩家之间立一道两格高的墙（脚部与头部）。
	chunk, err := instance.world.Chunk(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	blockX := int(math.Floor(spawnX))
	blockZ := int(math.Floor(spawnZ)) + 1
	baseY := int(math.Floor(spawnY))
	chunk.SetBlockState(blockX, baseY, blockZ, world.StoneBlock)
	chunk.SetBlockState(blockX, baseY+1, blockZ, world.StoneBlock)

	// 直接调用侧移：应能找到合法落点并移动。
	instance.entityMu.Lock()
	moved := instance.trySideStep(mob)
	instance.entityMu.Unlock()
	if !moved {
		t.Fatal("expected side step to find a valid position")
	}
	if math.Abs(mob.X-spawnX) < 0.3 && math.Abs(mob.Z-(spawnZ+1.9)) < 0.3 {
		t.Fatalf("mob did not move: (%v, %v)", mob.X, mob.Z)
	}

	// 追击被墙挡住时：推进 tick 后应触发侧移（位置发生变化）。
	before := [2]float64{mob.X, mob.Z}
	for i := 0; i < mobStuckTicks+5; i++ {
		instance.tick()
	}
	if mob.X == before[0] && mob.Z == before[1] {
		t.Fatal("blocked mob never moved after stuck threshold")
	}
}
