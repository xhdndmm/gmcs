//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"
	"net"
	"testing"
	"time"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
	"gmcs/internal/world"
)

// clearPlayerArea 在玩家坐标周围铺平一块地面并清空上方，保证环境测试
// 的方块布局确定。
func clearPlayerArea(instance *Server, cx, floorY, cz, radius, height int) {
	for dx := -radius; dx <= radius; dx++ {
		for dz := -radius; dz <= radius; dz++ {
			instance.testWorld().SetBlock(cx+dx, floorY, cz+dz, world.StoneBlock)
			for dy := 1; dy <= height; dy++ {
				instance.testWorld().SetBlock(cx+dx, floorY+dy, cz+dz, world.AirBlock)
			}
		}
	}
}

// processBarrier 发送 /list 并等待系统消息，确保此前的数据包已被读循环处理完
// （此后测试可以安全地直接写会话状态）。
func processBarrier(t *testing.T, conn net.Conn) {
	t.Helper()
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")
}

// clearHurtCooldown 清除受伤无敌帧，使下一次环境伤害立即可生效（测试用）。
func clearHurtCooldown(player *session) {
	player.stateMu.Lock()
	player.lastHurt = time.Time{}
	player.stateMu.Unlock()
}

// TestLavaIgnitesAndDamagesPlayer 验证岩浆：立即结算 4 点伤害、点燃 15 秒
// （同步 Entity Data 的着火标志），入水后熄灭。
func TestLavaIgnitesAndDamagesPlayer(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Lavaed")
	player := findSession(t, instance, "Lavaed")
	processBarrier(t, conn)

	x, y, z, _, _ := player.playerPosition()
	bx, by, bz := int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))
	clearPlayerArea(instance, bx, by-1, bz, 1, 4)

	// 玩家脚部所在方块替换为岩浆。
	if !instance.testWorld().SetBlock(bx, by, bz, world.LavaBlock) {
		t.Fatal("无法设置岩浆方块")
	}
	player.setPlayerPosition(x, float64(by), z, 0, 0)

	instance.tick()
	health, _, _ := player.healthStatus()
	if want := float32(maxPlayerHealth - envLavaDamage); health != want {
		t.Fatalf("岩浆伤害后生命 = %v, want %v", health, want)
	}
	if ticks := player.fireTicks.Load(); ticks <= 0 {
		t.Fatalf("岩浆未点燃玩家：fireTicks = %d", ticks)
	}
	// 着火标志同步（索引 0、值 1）。
	payload := expectPlayPacket(t, conn, protocol.PlayPacketIDEntityMetadata)
	if flags := metadataByteValue(t, payload, 0); flags&1 == 0 {
		t.Fatalf("着火标志未置位：%#x", flags)
	}

	// 换成水：着火立即熄灭，并同步清除标志。
	if !instance.testWorld().SetBlock(bx, by, bz, world.WaterBlock) ||
		!instance.testWorld().SetBlock(bx, by+1, bz, world.WaterBlock) {
		t.Fatal("无法设置水方块")
	}
	instance.tick()
	if ticks := player.fireTicks.Load(); ticks != 0 {
		t.Fatalf("入水后 fireTicks = %d, want 0", ticks)
	}
	payload = expectPlayPacket(t, conn, protocol.PlayPacketIDEntityMetadata)
	if flags := metadataByteValue(t, payload, 0); flags&1 != 0 {
		t.Fatalf("熄灭后着火标志仍置位：%#x", flags)
	}
}

// metadataByteValue 解析 Set Entity Data 包中的字节字段（index 匹配时返回值）。
func metadataByteValue(t *testing.T, payload []byte, wantIndex uint8) byte {
	t.Helper()
	_, offset, err := protocol.DecodeVarInt(payload) // 包 ID
	if err != nil {
		t.Fatal(err)
	}
	_, size, err := protocol.DecodeVarInt(payload[offset:]) // 实体 ID
	if err != nil {
		t.Fatal(err)
	}
	offset += size
	for offset < len(payload) && payload[offset] != 0xFF {
		index := payload[offset]
		offset++
		typeID, size, err := protocol.DecodeVarInt(payload[offset:])
		if err != nil {
			t.Fatal(err)
		}
		offset += size
		switch typeID {
		case 0: // byte
			if offset >= len(payload) {
				t.Fatal("元数据截断")
			}
			value := payload[offset]
			offset++
			if index == wantIndex {
				return value
			}
		case 1: // varint
			_, size, err := protocol.DecodeVarInt(payload[offset:])
			if err != nil {
				t.Fatal(err)
			}
			offset += size
		default:
			t.Fatalf("未预期的元数据类型 %d", typeID)
		}
	}
	t.Fatalf("未找到元数据索引 %d", wantIndex)
	return 0
}

// TestFireDamagePerSecond 验证着火每秒 1 点火焰伤害与递减。
func TestFireDamagePerSecond(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Burning")
	player := findSession(t, instance, "Burning")
	processBarrier(t, conn)

	clearHurtCooldown(player)
	player.fireTicks.Store(20)
	instance.tick()
	if health, _, _ := player.healthStatus(); health != maxPlayerHealth-envFireDamage {
		t.Fatalf("火焰伤害后生命 = %v, want %v", health, maxPlayerHealth-envFireDamage)
	}
	if ticks := player.fireTicks.Load(); ticks != 19 {
		t.Fatalf("fireTicks = %d, want 19", ticks)
	}
}

// TestDrowningDamagesPlayer 验证溺水：氧气耗尽后结算伤害并归零，
// 头部出水后氧气恢复。
func TestDrowningDamagesPlayer(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Drowned")
	player := findSession(t, instance, "Drowned")
	processBarrier(t, conn)

	x, y, z, _, _ := player.playerPosition()
	bx, by, bz := int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))
	clearPlayerArea(instance, bx, by-1, bz, 1, 5)
	// 3 格深的水柱（脚、头、水上）。
	for dy := 0; dy <= 2; dy++ {
		if !instance.testWorld().SetBlock(bx, by+dy, bz, world.WaterBlock) {
			t.Fatal("无法设置水方块")
		}
	}
	player.setPlayerPosition(x, float64(by), z, 0, 0)
	clearHurtCooldown(player)
	player.air.Store(-19)

	instance.tick()
	if health, _, _ := player.healthStatus(); health != maxPlayerHealth-envDrownDamage {
		t.Fatalf("溺水伤害后生命 = %v, want %v", health, maxPlayerHealth-envDrownDamage)
	}
	if air := player.air.Load(); air != 0 {
		t.Fatalf("溺水后 air = %d, want 0", air)
	}

	// 头部出水：氧气立即恢复。
	if !instance.testWorld().SetBlock(bx, by+1, bz, world.AirBlock) ||
		!instance.testWorld().SetBlock(bx, by+2, bz, world.AirBlock) {
		t.Fatal("无法清除水方块")
	}
	instance.tick()
	if air := player.air.Load(); air != int32(maxPlayerAir) {
		t.Fatalf("出水后 air = %d, want %d", air, maxPlayerAir)
	}
}

// TestMagmaBlockDamagesPlayer 验证站在岩浆块上受到周期性伤害。
func TestMagmaBlockDamagesPlayer(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Magmaed")
	player := findSession(t, instance, "Magmaed")
	processBarrier(t, conn)

	x, y, z, _, _ := player.playerPosition()
	bx, by, bz := int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))
	clearPlayerArea(instance, bx, by-1, bz, 1, 4)
	if !instance.testWorld().SetBlock(bx, by-1, bz, world.MagmaBlock) {
		t.Fatal("无法设置岩浆块")
	}
	player.setPlayerPosition(x, float64(by), z, 0, 0)
	clearHurtCooldown(player)

	instance.tick()
	if health, _, _ := player.healthStatus(); health != maxPlayerHealth-envMagmaDamage {
		t.Fatalf("岩浆块伤害后生命 = %v, want %v", health, maxPlayerHealth-envMagmaDamage)
	}
}
