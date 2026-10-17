//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"
	"testing"
	"time"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// TestBowFiresArrow 验证弓的蓄力/释放流程：消耗箭、生成箭矢并命中生物。
func TestBowFiresArrow(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = config.StringList{"minecraft:bow*1", "minecraft:arrow*16"}
	instance, conn := joinServer(t, cfg, "Archer")
	player := findSession(t, instance, "Archer")

	spawnX, spawnY, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	baseX, baseZ := int(math.Floor(spawnX)), int(math.Floor(spawnZ))
	clearCorridor(instance, baseX, int(spawnY)-1, baseZ, 1, 8)
	// 朝 +Z 方向射击（yaw 0）。
	player.setPlayerPosition(spawnX, spawnY, spawnZ, 0, 0)

	sendUseItem(t, conn)
	time.Sleep(1100 * time.Millisecond) // 满蓄力
	sendPlayerActionSeq(t, conn, 5, 0, 0, 0, 0, 0)
	waitFor(t, func() bool {
		instance.entityMu.Lock()
		defer instance.entityMu.Unlock()
		return len(instance.arrows) == 1
	}, "弓没有生成箭矢")
	arrowSlot := findArrowSlot(player)
	if arrowSlot < 0 || player.inventory.Get(arrowSlot).Count != 15 {
		t.Fatalf("发射后箭数量 = %d（槽 %d）, want 15", player.inventory.Get(arrowSlot).Count, arrowSlot)
	}

	// 在箭的飞行路径上放置僵尸（+3 格），推进一帧应命中并扣除 6 点（满蓄力）。
	zombie := instance.addMob(world.DimensionOverworld, mobZombie, spawnX, spawnY, spawnZ+3)
	for i := 0; i < 4; i++ {
		instance.tick()
	}
	instance.entityMu.Lock()
	health := zombie.Health
	instances := len(instance.arrows)
	instance.entityMu.Unlock()
	if health >= mobKinds[mobZombie].health {
		t.Fatalf("箭矢没有命中生物：health=%v", health)
	}
	if instances != 0 {
		t.Fatalf("命中后仍存在 %d 支箭", instances)
	}
}

// TestBowRequiresArrow 验证没有箭时不会发射。
func TestBowRequiresArrow(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = config.StringList{"minecraft:bow*1"}
	instance, conn := joinServer(t, cfg, "NoArrows")

	sendUseItem(t, conn)
	time.Sleep(1100 * time.Millisecond)
	sendPlayerActionSeq(t, conn, 5, 0, 0, 0, 0, 0)
	time.Sleep(100 * time.Millisecond)
	instance.entityMu.Lock()
	count := len(instance.arrows)
	instance.entityMu.Unlock()
	if count != 0 {
		t.Fatalf("没有箭却生成了 %d 支箭", count)
	}
}

// TestBowInfinityKeepsArrow 验证无限附魔不消耗箭。
func TestBowInfinityKeepsArrow(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = config.StringList{"minecraft:bow*1", "minecraft:arrow*16"}
	instance, conn := joinServer(t, cfg, "Infinity")
	player := findSession(t, instance, "Infinity")
	infinityID, ok := registry.EnchantmentID("minecraft:infinity")
	if !ok {
		t.Fatal("缺少无限附魔")
	}
	bow := item.Stack{ItemID: mustItemID(t, "minecraft:bow"), Count: 1}.WithEnchantment(infinityID, 1)
	player.inventory.Set(0, bow)

	sendUseItem(t, conn)
	time.Sleep(1100 * time.Millisecond)
	sendPlayerActionSeq(t, conn, 5, 0, 0, 0, 0, 0)
	waitFor(t, func() bool {
		instance.entityMu.Lock()
		defer instance.entityMu.Unlock()
		return len(instance.arrows) == 1
	}, "无限附魔没有发射箭矢")
	arrowSlot := findArrowSlot(player)
	if arrowSlot < 0 || player.inventory.Get(arrowSlot).Count != 16 {
		t.Fatalf("无限附魔消耗了箭：槽 %d count=%d", arrowSlot, player.inventory.Get(arrowSlot).Count)
	}
}

// TestShieldBlocksFrontDamage 验证盾牌格挡正面伤害并损耗耐久，背后伤害不受影响。
func TestShieldBlocksFrontDamage(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = config.StringList{"minecraft:shield*1"}
	instance, conn := joinServer(t, cfg, "Blocker")
	player := findSession(t, instance, "Blocker")
	px, py, pz, _, _ := player.playerPosition()
	player.setPlayerPosition(px, py, pz, 0, 0)

	sendUseItem(t, conn)
	waitFor(t, player.blocking.Load, "盾牌未进入格挡状态")

	front := [3]float64{px, py, pz + 3}
	if !instance.damagePlayer(player, 5, "Zombie", 0, instance.mobAttackDamageTypeID, &front) {
		t.Fatal("格挡伤害未生效")
	}
	if health, _, _ := player.healthStatus(); health != maxPlayerHealth {
		t.Fatalf("正面格挡后生命 = %v, want %v", health, maxPlayerHealth)
	}
	shield := player.inventory.Get(0)
	if shield.Damage != 6 {
		t.Fatalf("盾牌耐久损耗 = %d, want 6", shield.Damage)
	}

	// 背后攻击不被格挡。
	back := [3]float64{px, py, pz - 3}
	if !instance.damagePlayer(player, 5, "Zombie", 0, instance.mobAttackDamageTypeID, &back) {
		t.Fatal("背后伤害未生效")
	}
	if health, _, _ := player.healthStatus(); health != maxPlayerHealth-5 {
		t.Fatalf("背后受伤后生命 = %v, want %v", health, maxPlayerHealth-5)
	}
}

// TestShieldDoesNotBlockBypassingDamage 验证 bypasses_shield 伤害不被格挡。
func TestShieldDoesNotBlockBypassingDamage(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = config.StringList{"minecraft:shield*1"}
	instance, conn := joinServer(t, cfg, "Voided")
	player := findSession(t, instance, "Voided")
	px, py, pz, _, _ := player.playerPosition()
	player.setPlayerPosition(px, py, pz, 0, 0)
	sendUseItem(t, conn)
	waitFor(t, player.blocking.Load, "盾牌未进入格挡状态")

	bypassID, ok := registry.SyncEntryID("minecraft:damage_type", "minecraft:out_of_world")
	if !ok {
		t.Fatal("缺少 out_of_world 伤害类型")
	}
	front := [3]float64{px, py, pz + 3}
	if !instance.damagePlayer(player, 5, "void", 0, bypassID, &front) {
		t.Fatal("穿透盾牌伤害未生效")
	}
	if health, _, _ := player.healthStatus(); health != maxPlayerHealth-5 {
		t.Fatalf("穿透盾牌伤害后生命 = %v, want %v", health, maxPlayerHealth-5)
	}
	if got := player.inventory.Get(0).Damage; got != 0 {
		t.Fatalf("穿透盾牌伤害不应损耗盾牌：%d", got)
	}
}
