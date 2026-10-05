package server

import (
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
)

// TestPlayerEntitySyncBetweenPlayers 验证多人可见性：
// 新玩家加入 → 其他玩家收到 Add Entity；移动 → Entity Position Sync；
// 攻击 → 受伤动画与生命扣减；退出 → Remove Entities。
func TestPlayerEntitySyncBetweenPlayers(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.ViewDistance = 2
	instance, observerConn := joinServer(t, cfg, "Observer")
	addr := observerConn.RemoteAddr().String()

	// 第二名玩家加入：Observer 应收到其实体（Add Entity）。
	moverConn := joinAt(t, cfg, addr, "Mover", len(cfg.StartingItems))
	expectPlayPacket(t, observerConn, protocol.PlayPacketIDAddEntity)

	mover := findSession(t, instance, "Mover")
	observer := findSession(t, instance, "Observer")

	// Mover 移动（保持与其他玩家 1 格距离，便于后续攻击）：Observer 收到位置同步。
	_, spawnY, _ := instance.spawnPosition()
	move := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDPlayerPosition)
	move = protocol.AppendFloat64(move, 1.5)
	move = protocol.AppendFloat64(move, spawnY)
	move = protocol.AppendFloat64(move, 0.5)
	move = protocol.AppendBool(move, true)
	if err := protocol.WritePacketWithCompression(moverConn, move, compressionThreshold); err != nil {
		t.Fatal(err)
	}
	expectPlayPacket(t, observerConn, protocol.PlayPacketIDEntityPositionSync)

	// Mover 攻击 Observer：受伤动画 + 生命从 20 扣到 16。
	sendAttack(t, moverConn, observer.entityID)
	expectPlayPacket(t, observerConn, protocol.PlayPacketIDHurtAnimation)
	observer.stateMu.Lock()
	health := observer.health
	observer.stateMu.Unlock()
	if health != maxPlayerHealth-playerAttackDamage {
		t.Fatalf("受伤后生命 = %v, want %v", health, maxPlayerHealth-playerAttackDamage)
	}

	// Mover 断开：Observer 收到实体移除。
	if err := moverConn.Close(); err != nil {
		t.Fatal(err)
	}
	expectPlayPacket(t, observerConn, protocol.PlayPacketIDRemoveEntities)
	_ = mover
}

// TestSwingBroadcast 验证挥手动画会广播给附近玩家。
func TestSwingBroadcast(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.ViewDistance = 2
	_, observerConn := joinServer(t, cfg, "Observer")
	addr := observerConn.RemoteAddr().String()

	swingerConn := joinAt(t, cfg, addr, "Swinger", len(cfg.StartingItems))
	expectPlayPacket(t, observerConn, protocol.PlayPacketIDAddEntity)

	packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDSwingArm)
	packet = protocol.AppendVarInt(packet, 0)
	if err := protocol.WritePacketWithCompression(swingerConn, packet, compressionThreshold); err != nil {
		t.Fatal(err)
	}
	expectPlayPacket(t, observerConn, protocol.PlayPacketIDAnimate)
}
