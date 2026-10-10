package server

import (
	"testing"
	"time"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
)

// TestEatingTakesTime 验证进食需要 1.6 秒：开始时不结算，时长到达后消耗物品
// 并恢复饥饿值。
func TestEatingTakesTime(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = config.StringList{"minecraft:bread*3"}
	instance, conn := joinServer(t, cfg, "Eater")
	player := findSession(t, instance, "Eater")

	// 受伤以允许进食（饥饿与生命均满时不吃）。
	if !instance.damagePlayer(player, 4, "Zombie", -1, instance.mobAttackDamageTypeID, nil) {
		t.Fatal("damage should apply")
	}
	player.stateMu.Lock()
	player.food = 10
	player.saturation = 0
	player.stateMu.Unlock()

	useItemPacket := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDUseItem)
	if err := protocol.WritePacketWithCompression(conn, useItemPacket, compressionThreshold); err != nil {
		t.Fatal(err)
	}
	// 立即检查：物品未消耗（1.6 秒过程未结束）。
	time.Sleep(100 * time.Millisecond)
	if stack := player.inventory.Get(0); stack.Count != 3 {
		t.Fatalf("bread consumed too early: count=%d", stack.Count)
	}

	// 等待过程结束，再发一个数据包驱动进度检查。
	time.Sleep(1600 * time.Millisecond)
	if err := protocol.WritePacketWithCompression(conn, useItemPacket, compressionThreshold); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if stack := player.inventory.Get(0); stack.Count == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("bread not consumed after the eating duration (count=%d)", player.inventory.Get(0).Count)
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, food, _ := player.healthStatus()
	if food <= 0 {
		t.Fatalf("food not restored after eating: %d", food)
	}
}

// TestEatingInterruptedByRelease 验证释放使用键会取消进食（不消耗物品）。
func TestEatingInterruptedByRelease(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = config.StringList{"minecraft:bread*3"}
	instance, conn := joinServer(t, cfg, "Quitter")
	player := findSession(t, instance, "Quitter")

	if !instance.damagePlayer(player, 4, "Zombie", -1, instance.mobAttackDamageTypeID, nil) {
		t.Fatal("damage should apply")
	}
	useItemPacket := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDUseItem)
	if err := protocol.WritePacketWithCompression(conn, useItemPacket, compressionThreshold); err != nil {
		t.Fatal(err)
	}
	// 过程进行中释放使用键（Player Action status=5）。
	release := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDPlayerAction)
	release = protocol.AppendVarInt(release, 5) // release_use_item
	release = protocol.AppendInt64(release, 0)  // position（忽略）
	release = append(release, 0)                // face
	release = protocol.AppendVarInt(release, 0) // sequence
	if err := protocol.WritePacketWithCompression(conn, release, compressionThreshold); err != nil {
		t.Fatal(err)
	}
	// 等待超过进食时长后再驱动一次检查：物品应保持不变。
	time.Sleep(1700 * time.Millisecond)
	if err := protocol.WritePacketWithCompression(conn, useItemPacket, compressionThreshold); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if stack := player.inventory.Get(0); stack.Count != 3 {
		t.Fatalf("interrupted eating still consumed bread: count=%d", stack.Count)
	}
}
