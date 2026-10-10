package server

import (
	"fmt"
	"math"
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
	"gmcs/internal/world"
)

// TestDimensionCommandNetworkFlow 验证 /dimension 的完整切换流程：
// Respawn → GameEvent(开始等待区块) → SetCenterChunk → 区块 → 同步位置，
// 再切换回主世界。
func TestDimensionCommandNetworkFlow(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.Ops = []string{"Traveller"}
	instance, conn := joinServer(t, cfg, "Traveller")
	player := findSession(t, instance, "Traveller")

	// 主世界 → 下界。
	sendChatCommand(t, conn, "/dimension the_nether")
	expectPlayPacket(t, conn, protocol.PlayPacketIDRespawn)
	expectPlayPacket(t, conn, protocol.PlayPacketIDGameEvent)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetCenterChunk)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSynchronizePlayerPos)
	if got := player.dimensionID(); got != world.DimensionNether {
		t.Fatalf("dimension after /dimension = %v, want nether", got)
	}
	netherX, netherY, netherZ := instance.spawnPositionFor(world.DimensionNether)
	px, py, pz, _, _ := player.playerPosition()
	if math.Abs(px-netherX) > 1e-6 || math.Abs(py-netherY) > 1e-6 || math.Abs(pz-netherZ) > 1e-6 {
		t.Fatalf("position after switch = (%v,%v,%v), want nether spawn (%v,%v,%v)",
			px, py, pz, netherX, netherY, netherZ)
	}
	// 持久化记录应包含新维度（重连恢复用）。
	instance.savePlayerData(player)
	record, ok := instance.playerDataSnapshot(player.uuid)
	if !ok {
		t.Fatal("player record missing after save")
	}
	if record.Dimension != world.DimensionNether.Name() {
		t.Fatalf("record dimension = %q, want %q", record.Dimension, world.DimensionNether.Name())
	}

	// 下界 → 主世界。
	sendChatCommand(t, conn, "/dimension overworld")
	expectPlayPacket(t, conn, protocol.PlayPacketIDRespawn)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetCenterChunk)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSynchronizePlayerPos)
	if got := player.dimensionID(); got != world.DimensionOverworld {
		t.Fatalf("dimension after switching back = %v, want overworld", got)
	}
	overX, overY, overZ := instance.spawnPositionFor(world.DimensionOverworld)
	px, py, pz, _, _ = player.playerPosition()
	if math.Abs(px-overX) > 1e-6 || math.Abs(py-overY) > 1e-6 || math.Abs(pz-overZ) > 1e-6 {
		t.Fatalf("position after switching back = (%v,%v,%v), want overworld spawn (%v,%v,%v)",
			px, py, pz, overX, overY, overZ)
	}
}

// TestDimensionUnknownName 验证 /dimension 对未知维度给出错误提示且不传送。
func TestDimensionUnknownName(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.Ops = []string{"Lost"}
	instance, conn := joinServer(t, cfg, "Lost")
	player := findSession(t, instance, "Lost")

	sendChatCommand(t, conn, "/dimension nowhere")
	expectSystemChat(t, conn, "未知维度")
	if got := player.dimensionID(); got != world.DimensionOverworld {
		t.Fatalf("dimension changed on invalid name: %v", got)
	}
}

// TestNetherAndEndWorldsGenerated 验证服务器按需生成的维度世界：
// 下界有基岩地板/天花板与实体地面，末地主岛由末地石构成。
func TestNetherAndEndWorldsGenerated(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Explorer")

	// 下界：出生点下方为实体方块，y=0 与 y=127 为基岩。
	netherX, netherY, netherZ := instance.spawnPositionFor(world.DimensionNether)
	nether := instance.worldFor(world.DimensionNether)
	if nether == nil {
		t.Fatal("nether world not created")
	}
	blockX, blockZ := int(math.Floor(netherX)), int(math.Floor(netherZ))
	if state := nether.BlockAt(blockX, 0, blockZ); state != world.BedrockBlock {
		t.Fatalf("nether floor at y=0 = %d, want bedrock", state)
	}
	if state := nether.BlockAt(blockX, 127, blockZ); state != world.BedrockBlock {
		t.Fatalf("nether roof at y=127 = %d, want bedrock", state)
	}
	if state := nether.BlockAt(blockX, int(math.Floor(netherY))-1, blockZ); state == world.AirBlock || state == world.WaterBlock || state == world.LavaBlock {
		t.Fatalf("nether spawn ground = %d, want solid non-fluid block", state)
	}

	// 末地：出生点下方为末地石。
	endX, endY, endZ := instance.spawnPositionFor(world.DimensionEnd)
	end := instance.worldFor(world.DimensionEnd)
	if end == nil {
		t.Fatal("end world not created")
	}
	endBlockX, endBlockZ := int(math.Floor(endX)), int(math.Floor(endZ))
	if state := end.BlockAt(endBlockX, int(math.Floor(endY))-1, endBlockZ); state != world.EndStoneBlock {
		t.Fatalf("end spawn ground = %d, want end stone", state)
	}
}

// TestDimensionMobsIsolated 验证生物 AI 的维度隔离：下界的僵尸不会攻击
// 主世界的玩家；玩家进入下界后才会被同一只僵尸攻击。
func TestDimensionMobsIsolated(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.Ops = []string{"Isolated"}
	instance, conn := joinServer(t, cfg, "Isolated")
	player := findSession(t, instance, "Isolated")

	spawnX, spawnY, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	baseX, baseZ := int(math.Floor(spawnX)), int(math.Floor(spawnZ))
	floorY := int(math.Floor(spawnY)) - 1
	// 两个维度中同一坐标处都铺平通道，保证视线/行走判定一致。
	clearCorridor(instance, baseX, floorY, baseZ, 1, 4)
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 4; dz++ {
			nether := instance.worldFor(world.DimensionNether)
			nether.SetBlock(baseX+dx, floorY, baseZ+dz, world.NetherrackBlock)
			for dy := 1; dy <= 4; dy++ {
				nether.SetBlock(baseX+dx, floorY+dy, baseZ+dz, world.AirBlock)
			}
		}
	}

	mob := instance.addMob(world.DimensionNether, mobZombie, spawnX, spawnY, spawnZ+1)
	if mob == nil {
		t.Fatal("addMob(nether zombie) returned nil")
	}
	// 玩家仍在主世界：僵尸不应攻击（不受伤、僵尸不被反弃用移除）。
	for i := 0; i < mobAttackWindupTicks+6; i++ {
		instance.tick()
	}
	if health, _, _ := player.healthStatus(); health != maxPlayerHealth {
		t.Fatalf("player in overworld was hit by a nether mob: %v", health)
	}
	instance.entityMu.Lock()
	_, stillAlive := instance.mobs[mob.ID]
	instance.entityMu.Unlock()
	if !stillAlive {
		t.Fatal("nether mob was despawned while no player was in its dimension")
	}

	// 玩家传入下界（经由命令，由读循环串行处理，避免与读循环
	// 并发访问会话状态）：传送到同一坐标后同一只僵尸可以攻击。
	sendChatCommand(t, conn, "/dimension the_nether")
	expectPlayPacket(t, conn, protocol.PlayPacketIDRespawn)
	sendChatCommand(t, conn, fmt.Sprintf("/tp %.2f %.2f %.2f", spawnX, spawnY, spawnZ))
	expectPlayPacket(t, conn, protocol.PlayPacketIDSynchronizePlayerPos)
	for i := 0; i < mobAttackWindupTicks+4; i++ {
		instance.tick()
	}
	health, _, _ := player.healthStatus()
	if want := float32(maxPlayerHealth - mobKinds[mobZombie].damage); health != want {
		t.Fatalf("player health in nether = %v, want %v", health, want)
	}
}

// TestDimensionPersistsAcrossReconnect 验证玩家所在维度会写入磁盘记录，
// 并在重连后恢复到该维度。
func TestDimensionPersistsAcrossReconnect(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.ViewDistance = 2 // 与 joinServer 内部设置一致（重连时复用）
	cfg.Ops = []string{"NetherDweller"}
	instance, conn := joinServer(t, cfg, "NetherDweller")
	addr := conn.RemoteAddr().String()
	player := findSession(t, instance, "NetherDweller")

	sendChatCommand(t, conn, "/dimension the_nether")
	expectPlayPacket(t, conn, protocol.PlayPacketIDRespawn)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetCenterChunk)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSynchronizePlayerPos)

	// 断开并检查磁盘记录。
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	waitPlayerOffline(t, instance, player.uuid)
	record := readSavedPlayer(t, cfg.WorldDir, player.uuid)
	if record.Dimension != world.DimensionNether.Name() {
		t.Fatalf("saved dimension = %q, want %q", record.Dimension, world.DimensionNether.Name())
	}

	// 重连：直接以下界身份进入（恢复维度与位置，不再发放初始物品）。
	conn2 := joinAt(t, cfg, addr, "NetherDweller", 0)
	_ = conn2
	restored := findSession(t, instance, "NetherDweller")
	if got := restored.dimensionID(); got != world.DimensionNether {
		t.Fatalf("dimension after reconnect = %v, want nether", got)
	}
	netherX, netherY, netherZ := instance.spawnPositionFor(world.DimensionNether)
	px, py, pz, _, _ := restored.playerPosition()
	if math.Abs(px-netherX) > 1e-6 || math.Abs(py-netherY) > 1e-6 || math.Abs(pz-netherZ) > 1e-6 {
		t.Fatalf("position after reconnect = (%v,%v,%v), want nether spawn (%v,%v,%v)",
			px, py, pz, netherX, netherY, netherZ)
	}
}
