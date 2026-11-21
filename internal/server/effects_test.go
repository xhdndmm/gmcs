//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"bytes"
	"testing"
	"time"

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
	player.applyEffect(registry.MobEffectAbsorption, 200, 0, false)
	record := playerRecordFromSession(player)
	if len(record.Effects) != 2 {
		t.Fatalf("saved effects = %+v", record.Effects)
	}
	other := newTestSession(player.uuid, false)
	other.server = instance
	other.applyPlayerRecord(record)
	if got := other.effectLevel(registry.MobEffectRegeneration); got != 2 {
		t.Fatalf("restored regeneration level = %d, want 2", got)
	}
	if got := other.absorptionStatus(); got != 4 {
		t.Fatalf("restored absorption = %v, want 4", got)
	}
}

func TestGivePotionComponentCarriesPotionID(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, err := newServer(cfg, testGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	defer instance.testWorld().Close()
	player := newTestSession([16]byte{0xD1, 0x03}, false)
	player.server = instance
	player.writer = &bytes.Buffer{}

	instance.handleGiveCommand(player, `minecraft:potion[potion_contents={potion:"minecraft:regeneration"}]`)
	found := false
	for slot := 0; slot < 36; slot++ {
		stack := player.inventory.Get(slot)
		if stack.ItemID == testItemID(t, "minecraft:potion") {
			found = true
			if stack.PotionID != registry.PotionRegeneration+1 {
				t.Fatalf("potion ID = %d, want %d", stack.PotionID, registry.PotionRegeneration+1)
			}
		}
	}
	if !found {
		t.Fatal("/give potion 未放入物品栏")
	}
}

func TestGoldenAppleAppliesEffectsAtFullHunger(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, err := newServer(cfg, testGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	defer instance.testWorld().Close()
	player := newTestSession([16]byte{0xD1, 0x04}, false)
	player.server = instance
	player.writer = &bytes.Buffer{}
	appleID, err := registry.ItemID("minecraft:golden_apple")
	if err != nil {
		t.Fatal(err)
	}
	player.inventory.Set(0, item.Stack{ItemID: appleID, Count: 1})
	player.stateMu.Lock()
	player.food = maxPlayerFood
	player.saturation = maxPlayerFood
	player.stateMu.Unlock()
	player.eating = true
	player.eatingSlot = 0
	player.eatingItemID = appleID
	player.completeEating()
	if !player.hasEffect(registry.MobEffectAbsorption) || !player.hasEffect(registry.MobEffectRegeneration) {
		t.Fatal("满饥饿时金苹果仍应施加吸收与再生效果")
	}
	if got := player.absorptionStatus(); got != 4 {
		t.Fatalf("金苹果吸收值 = %v, want 4", got)
	}
	if !player.inventory.Get(0).IsEmpty() {
		t.Fatal("金苹果应被消耗")
	}
}

func TestCreativePotionSlotKeepsPotionID(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.GameMode = "creative"
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "CreativePotion")
	defer conn.Close()
	player := findSession(t, instance, "CreativePotion")
	potionID := testItemID(t, "minecraft:potion")
	packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDSetCreativeSlot)
	packet = append(packet, 0, 36) // 玩家菜单快捷栏 0
	packet = protocol.AppendVarInt(packet, 1)
	packet = protocol.AppendVarInt(packet, potionID)
	packet = protocol.AppendVarInt(packet, 1)
	packet = protocol.AppendVarInt(packet, 49) // potion_contents
	packet = append(packet, 1)
	packet = protocol.AppendVarInt(packet, registry.PotionRegeneration)
	packet = append(packet, 0, 0, 0)
	packet = protocol.AppendVarInt(packet, 0)
	if err := protocol.WritePacketWithCompression(conn, packet, compressionThreshold); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		stack := player.inventory.Get(0)
		return stack.ItemID == potionID && stack.PotionID == registry.PotionRegeneration+1
	}, "创造模式药水未保留 potion_contents")
}

func TestAbsorptionShieldsGoldenAppleDamage(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, err := newServer(cfg, testGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	defer instance.testWorld().Close()
	player := newTestSession([16]byte{0xD1, 0x05}, false)
	player.server = instance
	player.writer = &bytes.Buffer{}
	player.healImmediate(0)
	player.stateMu.Lock()
	player.health = maxPlayerHealth
	player.stateMu.Unlock()
	for _, packet := range instance.applyPotionEffects(player, []registry.PotionEffect{
		{ID: registry.MobEffectAbsorption, Duration: 2400, Amplifier: 0},
	}) {
		player.tryWrite(packet)
	}
	if got := player.absorptionStatus(); got != 4 {
		t.Fatalf("absorption = %v, want 4", got)
	}
	health, _, _, applied, _ := player.applyDamage(instance, 2, instance.magicDamageTypeID, time.Now())
	if !applied || health != maxPlayerHealth {
		t.Fatalf("2 点伤害应完全由吸收承受：health=%v applied=%v", health, applied)
	}
	if got := player.absorptionStatus(); got != 2 {
		t.Fatalf("吸收后 absorption = %v, want 2", got)
	}
	player.stateMu.Lock()
	player.lastHurt = time.Time{}
	player.stateMu.Unlock()
	health, _, _, applied, _ = player.applyDamage(instance, 5, instance.magicDamageTypeID, time.Now())
	if !applied || health != maxPlayerHealth-3 {
		t.Fatalf("吸收耗尽后应扣 3 点生命：health=%v applied=%v", health, applied)
	}
}
