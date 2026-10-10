package server

import (
	"math"
	"net"
	"testing"
	"time"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/world"
)

// sendUseItem 发送 Use Item（使用物品，如进食）包。
func sendUseItem(t *testing.T, conn net.Conn) {
	t.Helper()
	packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDUseItem)
	packet = protocol.AppendVarInt(packet, 0) // hand
	packet = protocol.AppendVarInt(packet, 0) // sequence
	packet = append(packet, 0x00)             // rotation（无）
	if err := protocol.WritePacketWithCompression(conn, packet, compressionThreshold); err != nil {
		t.Fatal(err)
	}
}

// TestHealthRegeneration 验证饥饿驱动的自然恢复：
// 受伤后饥饿值 ≥ 18 时，每 4 秒（80 tick）恢复 1 点，并同步给客户端。
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
	if _, food, _ := player.healthStatus(); food < playerRegenFoodThreshold {
		t.Fatalf("food = %d, want >= %d", food, playerRegenFoodThreshold)
	}

	// 一个恢复周期后恢复 1 点。
	for i := 0; i < playerFoodTickInterval; i++ {
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

	// 饥饿值低于阈值时不恢复：把饥饿值降到 17 后推进一个周期。
	player.stateMu.Lock()
	player.food = playerRegenFoodThreshold - 1
	player.saturation = 0
	player.foodTimer = 0
	player.stateMu.Unlock()
	for i := 0; i < playerFoodTickInterval; i++ {
		instance.tick()
	}
	if health, _, _ := player.healthStatus(); health != maxPlayerHealth-4 {
		t.Fatalf("hunger below threshold should not regen: health = %v", health)
	}
}

// TestStarvationDamage 验证饥饿值为 0 时每 4 秒受到 1 点伤害（最低到 1）。
func TestStarvationDamage(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Starving")
	player := findSession(t, instance, "Starving")

	player.stateMu.Lock()
	player.food = 0
	player.saturation = 0
	player.foodTimer = 0
	player.health = 2
	player.stateMu.Unlock()

	for i := 0; i < playerFoodTickInterval; i++ {
		instance.tick()
	}
	if health, _, _ := player.healthStatus(); health != 1 {
		t.Fatalf("starvation damage: health = %v, want 1", health)
	}
	// 生命降到 1 后不再因饥饿受损。
	for i := 0; i < playerFoodTickInterval*2; i++ {
		instance.tick()
	}
	if health, _, _ := player.healthStatus(); health != 1 {
		t.Fatalf("starvation should stop at 1: health = %v", health)
	}
}

// TestEatingRestoresHunger 验证进食恢复饥饿值、消耗物品并同步数据包。
func TestEatingRestoresHunger(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:apple*2"}
	instance, conn := joinServer(t, cfg, "Eater")
	player := findSession(t, instance, "Eater")

	// 先消耗到饥饿值 10。
	player.stateMu.Lock()
	player.food = 10
	player.saturation = 0
	player.stateMu.Unlock()

	sendUseItem(t, conn)
	// 1.6 秒进食过程：开始时立即检查不消耗。
	time.Sleep(100 * time.Millisecond)
	if stack := player.inventory.Get(item.SlotHotbarStart); stack.Count != 2 {
		t.Fatalf("apple consumed before the eating duration: %+v", stack)
	}
	time.Sleep(1600 * time.Millisecond)
	sendUseItem(t, conn) // 再发一个数据包驱动进度检查。
	// 期望：槽位数量 2 → 1，然后 Update Health（饥饿值 +4）。
	slotPacket := expectPlayPacket(t, conn, protocol.PlayPacketIDSetPlayerInventory)
	if count := slotCount(t, slotPacket); count != 1 {
		t.Fatalf("apple count after eating = %d, want 1", count)
	}
	expectPlayPacket(t, conn, protocol.PlayPacketIDUpdateHealth)
	if _, food, _ := player.healthStatus(); food != 14 {
		t.Fatalf("food after eating apple = %d, want 14", food)
	}
	if stack := player.inventory.Get(item.SlotHotbarStart); stack.Count != 1 {
		t.Fatalf("inventory apple count = %+v", stack)
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

	spawnX, spawnY, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	mob := instance.addMob(world.DimensionOverworld, mobZombie, spawnX, spawnY, spawnZ+1.9)
	expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)

	// 铺平生物周围（新地形可能起伏/有植被），再立一道两格高的墙。
	blockX := int(math.Floor(spawnX))
	blockZ := int(math.Floor(spawnZ))
	baseY := int(math.Floor(spawnY)) - 1
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 3; dz++ {
			instance.testWorld().SetBlock(blockX+dx, baseY, blockZ+dz, world.StoneBlock)
			for dy := 1; dy <= 3; dy++ {
				instance.testWorld().SetBlock(blockX+dx, baseY+dy, blockZ+dz, world.AirBlock)
			}
		}
	}
	instance.testWorld().SetBlock(blockX, baseY+1, blockZ+1, world.StoneBlock)
	instance.testWorld().SetBlock(blockX, baseY+2, blockZ+1, world.StoneBlock)

	// 直接调用侧移：应能找到合法落点并移动。
	instance.entityMu.Lock()
	moved := instance.trySideStep(instance.testWorld(), mob)
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
