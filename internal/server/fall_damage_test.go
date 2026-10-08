package server

import (
	"math"
	"net"
	"testing"
	"time"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
)

// sendPlayerPosition 发送 Player Position 包（含着地标志）。
func sendPlayerPosition(t *testing.T, conn net.Conn, x, y, z float64, onGround bool) {
	t.Helper()
	packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDPlayerPosition)
	packet = protocol.AppendFloat64(packet, x)
	packet = protocol.AppendFloat64(packet, y)
	packet = protocol.AppendFloat64(packet, z)
	var flags byte
	if onGround {
		flags = 0x01
	}
	packet = append(packet, flags)
	if err := protocol.WritePacketWithCompression(conn, packet, compressionThreshold); err != nil {
		t.Fatal(err)
	}
}

// TestFallDamage 验证下落超过 3 格后落地受到摔落伤害（每多 1 格 1 点），
// 短距离下落不受伤害。
func TestFallDamage(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Faller")
	player := findSession(t, instance, "Faller")
	spawnX, spawnY, spawnZ := instance.spawnPosition()

	// 站立在出生点（着地）。
	sendPlayerPosition(t, conn, spawnX, spawnY, spawnZ, true)

	// 从 6 格高处下落：伤害 = ceil(6-3) = 3。
	sendPlayerPosition(t, conn, spawnX, spawnY+6, spawnZ, false)
	sendPlayerPosition(t, conn, spawnX, spawnY, spawnZ, true)
	expectPlayPacket(t, conn, protocol.PlayPacketIDDamageEvent)
	healthPacket := expectPlayPacket(t, conn, protocol.PlayPacketIDUpdateHealth)
	_, offset, err := protocol.DecodeVarInt(healthPacket)
	if err != nil {
		t.Fatal(err)
	}
	health, _, err := protocol.DecodeFloat32(healthPacket, offset)
	if err != nil || health != maxPlayerHealth-3 {
		t.Fatalf("health after fall = %v (err=%v), want %v", health, err, maxPlayerHealth-3)
	}
	expectPlayPacket(t, conn, protocol.PlayPacketIDSoundEffect)

	// 从 2 格高处下落：无伤害（用 /list 响应确保服务器已处理完移动包）。
	sendPlayerPosition(t, conn, spawnX, spawnY+2, spawnZ, false)
	sendPlayerPosition(t, conn, spawnX, spawnY, spawnZ, true)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")
	if remaining, _, _ := player.healthStatus(); remaining != maxPlayerHealth-3 {
		t.Fatalf("health after short fall = %v, want %v", remaining, maxPlayerHealth-3)
	}
}

// TestFallDamageProtection 验证受保护方块对摔落伤害的减免：
// 干草堆减 80%、床减 50%、粘液块/蜂蜜块完全免疫。
// 直接调用会话的下落跟踪（不经过网络）：先确保读循环已处理完站立包。
func TestFallDamageProtection(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Cushioned")
	player := findSession(t, instance, "Cushioned")
	spawnX, spawnY, spawnZ := instance.spawnPosition()
	sendPlayerPosition(t, conn, spawnX, spawnY, spawnZ, true)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")

	surfaceX, surfaceZ := int(math.Floor(spawnX)), int(math.Floor(spawnZ))
	surfaceY := int(math.Floor(spawnY)) - 1

	setHealth := func(value float32) {
		player.stateMu.Lock()
		player.health = value
		player.dead = false
		player.lastHurt = time.Time{} // 清除受伤冷却（避免连续测试互相抑制）
		player.stateMu.Unlock()
	}
	check := func(name string, state uint16, wantHealth float32) {
		t.Helper()
		if !instance.world.SetBlock(surfaceX, surfaceY, surfaceZ, state) {
			t.Fatalf("%s: SetBlock failed", name)
		}
		setHealth(maxPlayerHealth)
		player.resetFallState()
		// 落点取支撑面的形状顶面（床顶 9/16 等），与真实客户端落地高度一致。
		landY, _, ok := instance.world.SurfaceBelow(playerBox(spawnX, spawnY, spawnZ), 1.0)
		if !ok {
			t.Fatalf("%s: 落点没有支撑面", name)
		}
		player.updateFallState(spawnX, landY+20, spawnZ)
		player.updateFallState(spawnX, landY, spawnZ)
		health, _, _ := player.healthStatus()
		if math.Abs(float64(health-wantHealth)) > 1e-3 {
			t.Fatalf("%s: health = %v, want %v", name, health, wantHealth)
		}
	} // 下落 20 格：基础伤害 17 点。	check("hay", registry.BlockStateIDs["minecraft:hay_block"], maxPlayerHealth-17*0.2)
	check("bed", registry.BlockStateIDs["minecraft:red_bed"], maxPlayerHealth-17*0.5)
	check("slime", registry.BlockStateIDs["minecraft:slime_block"], maxPlayerHealth)
	check("honey", registry.BlockStateIDs["minecraft:honey_block"], maxPlayerHealth)
}
