//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"bytes"
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
)

func TestPotionEffectApplyTickAndRemove(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Effects")
	defer conn.Close()
	player := findSession(t, instance, "Effects")
	packet := player.applyEffect(registry.MobEffectRegeneration, 100, 1, false)
	if packet == nil || packet[0] != protocol.PlayPacketIDUpdateMobEffect {
		t.Fatalf("apply packet = %x", packet)
	}
	if got := player.effectLevel(registry.MobEffectRegeneration); got != 2 {
		t.Fatalf("regeneration level = %d, want 2", got)
	}
	player.stateMu.Lock()
	player.health = 10
	player.stateMu.Unlock()
	for i := 0; i < 25; i++ {
		player.tickEffects()
	}
	if health, _, _ := player.healthStatus(); health != 11 {
		t.Fatalf("regeneration health = %v, want 11", health)
	}
	for i := 0; i < 80; i++ {
		player.tickEffects()
	}
	if player.hasEffect(registry.MobEffectRegeneration) {
		t.Fatal("expired effect still active")
	}
}

func TestDrinkPotionAppliesEffects(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, err := newServer(cfg, testGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	defer instance.testWorld().Close()
	player := newTestSession([16]byte{0xD1, 0x02}, false)
	player.server = instance
	player.writer = &bytes.Buffer{}
	potionID, err := registry.ItemID("minecraft:potion")
	if err != nil {
		t.Fatal(err)
	}
	stack := item.Stack{ItemID: potionID, Count: 1, PotionID: registry.PotionRegeneration + 1}
	player.inventory.Set(0, stack)
	player.eating = true
	player.eatingSlot = 0
	player.eatingItemID = potionID
	player.completeEating()
	if !player.hasEffect(registry.MobEffectRegeneration) {
		t.Fatal("drinking regeneration potion did not apply effect")
	}
	if !player.inventory.Get(0).IsEmpty() {
		t.Fatalf("potion was not consumed: %+v", player.inventory.Get(0))
	}
}

func TestFireResistanceBlocksFireDamage(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "FireResist")
	defer conn.Close()
	player := findSession(t, instance, "FireResist")
	player.applyEffect(registry.MobEffectFireResistance, 600, 0, false)
	before, _, _ := player.healthStatus()
	if instance.damagePlayer(player, 4, "fire", 0, instance.fireDamageTypeID, nil) {
		t.Fatal("fire resistance did not block fire damage")
	}
	after, _, _ := player.healthStatus()
	if after != before {
		t.Fatalf("health changed by fire resistance: %v -> %v", before, after)
	}
}

func TestMilkClearsEffects(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Milk")
	defer conn.Close()
	player := findSession(t, instance, "Milk")
	player.applyEffect(registry.MobEffectSpeed, 100, 0, false)
	if !player.hasEffect(registry.MobEffectSpeed) {
		t.Fatal("effect was not applied")
	}
	packets := player.clearEffects()
	if len(packets) != 1 || packets[0][0] != protocol.PlayPacketIDRemoveMobEffect {
		t.Fatalf("remove packets = %x", packets)
	}
	if player.hasEffect(registry.MobEffectSpeed) {
		t.Fatal("effect was not cleared")
	}
}

func TestEffectPersistenceRoundTrip(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "EffectSave")
	defer conn.Close()
	player := findSession(t, instance, "EffectSave")
	player.applyEffect(registry.MobEffectRegeneration, 200, 1, false)
	record := playerRecordFromSession(player)
	if len(record.Effects) != 1 || record.Effects[0].ID != registry.MobEffectRegeneration {
		t.Fatalf("saved effects = %+v", record.Effects)
	}
	other := newTestSession(player.uuid, false)
	other.server = instance
	other.applyPlayerRecord(record)
	if got := other.effectLevel(registry.MobEffectRegeneration); got != 2 {
		t.Fatalf("restored regeneration level = %d, want 2", got)
	}
}
