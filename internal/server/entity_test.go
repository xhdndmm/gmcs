package server

import (
	"context"
	"net"
	"testing"
	"time"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
)

// joinServer 启动一台测试服务器并完成一名玩家的完整登录流程
// （握手 → 登录 → 配置 → 进入世界），返回服务器实例与已就绪的连接。
// 后台实体 Tick 被禁用，测试通过 instance.tick() 手动驱动以获得确定性。
func joinServer(t *testing.T, cfg config.Config, name string) (*Server, net.Conn) {
	t.Helper()
	instance, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	instance.keepAliveInterval = time.Hour // 测试期间不需要 Keep Alive
	instance.tickInterval = 0              // 手动驱动 Tick
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serveResult := make(chan error, 1)
	go func() { serveResult <- instance.Serve(ctx, listener) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-serveResult:
			if err != nil {
				t.Errorf("Serve() after cancellation: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Serve() did not stop after context cancellation")
		}
	})

	conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatal(err)
	}

	// 握手（next state = 2，登录）
	handshake := protocol.AppendVarInt(nil, 0)
	handshake = protocol.AppendVarInt(handshake, cfg.ProtocolVersion)
	handshake = appendTestString(handshake, "localhost")
	handshake = append(handshake, 0x63, 0xDD)
	handshake = protocol.AppendVarInt(handshake, 2)
	if err := protocol.WritePacket(conn, handshake); err != nil {
		t.Fatal(err)
	}

	// Login Start
	loginStart := protocol.AppendVarInt(nil, 0)
	loginStart = appendTestString(loginStart, name)
	uuid := protocol.OfflineUUID(name)
	loginStart = append(loginStart, uuid[:]...)
	if err := protocol.WritePacket(conn, loginStart); err != nil {
		t.Fatal(err)
	}

	// Set Compression
	setCompression, err := protocol.ReadPacket(conn)
	if err != nil {
		t.Fatal(err)
	}
	if id, _, err := protocol.DecodeVarInt(setCompression); err != nil || id != 3 {
		t.Fatalf("expected set compression, got %#x (err=%v)", id, err)
	}
	// Login Success
	if id, _ := readCompressedPacket(t, conn); id != 2 {
		t.Fatalf("expected login success, got %#x", id)
	}
	// Login Acknowledged
	if err := protocol.WritePacketWithCompression(conn, protocol.AppendVarInt(nil, 3), compressionThreshold); err != nil {
		t.Fatal(err)
	}

	// 配置阶段：Brand → Feature Flags → Known Packs
	for _, want := range []int32{0x01, 0x0C, 0x0E} {
		if id, _ := readCompressedPacket(t, conn); id != want {
			t.Fatalf("expected configuration packet %#x, got %#x", want, id)
		}
	}
	knownPacks := protocol.AppendVarInt(nil, 0x07)
	knownPacks = protocol.AppendVarInt(knownPacks, 1)
	knownPacks = appendTestString(knownPacks, "minecraft")
	knownPacks = appendTestString(knownPacks, "core")
	knownPacks = appendTestString(knownPacks, "1.21.11")
	if err := protocol.WritePacketWithCompression(conn, knownPacks, compressionThreshold); err != nil {
		t.Fatal(err)
	}
	for range registry.Synchronized() {
		if id, _ := readCompressedPacket(t, conn); id != 0x07 {
			t.Fatalf("expected registry data, got %#x", id)
		}
	}
	if id, _ := readCompressedPacket(t, conn); id != 0x0D {
		t.Fatalf("expected update tags, got %#x", id)
	}
	if id, _ := readCompressedPacket(t, conn); id != 0x03 {
		t.Fatalf("expected finish configuration, got %#x", id)
	}
	if err := protocol.WritePacketWithCompression(conn, protocol.AppendVarInt(nil, 0x03), compressionThreshold); err != nil {
		t.Fatal(err)
	}

	// Play 阶段初始包
	for _, want := range []int32{0x30, 0x5F, 0x26, 0x5C, 0x2C, 0x46, 0x44, 0x77, 0x10, 0x6A, 0x66} {
		expectPlayPacket(t, conn, want)
	}
	// 确认传送
	confirm := protocol.AppendVarInt(nil, 0x00)
	confirm = protocol.AppendVarInt(confirm, 1)
	if err := protocol.WritePacketWithCompression(conn, confirm, compressionThreshold); err != nil {
		t.Fatal(err)
	}
	return instance, conn
}

// findSession 返回指定名字的在线会话。
func findSession(t *testing.T, instance *Server, name string) *session {
	t.Helper()
	instance.mu.Lock()
	defer instance.mu.Unlock()
	for _, player := range instance.players {
		if player.name == name {
			return player
		}
	}
	t.Fatalf("session %q not found", name)
	return nil
}

// sendChatCommand 发送一条聊天栏命令。
func sendChatCommand(t *testing.T, conn net.Conn, command string) {
	t.Helper()
	packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDChatCommand)
	packet = appendTestString(packet, command)
	if err := protocol.WritePacketWithCompression(conn, packet, compressionThreshold); err != nil {
		t.Fatal(err)
	}
}

// sendAttack 发送一次攻击实体的交互包。
func sendAttack(t *testing.T, conn net.Conn, targetID int32) {
	t.Helper()
	packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDInteract)
	packet = protocol.AppendVarInt(packet, targetID)
	packet = protocol.AppendVarInt(packet, protocol.InteractActionAttack)
	packet = protocol.AppendBool(packet, false)
	if err := protocol.WritePacketWithCompression(conn, packet, compressionThreshold); err != nil {
		t.Fatal(err)
	}
}

// TestMobCombatFlow 验证：生成生物 → 生物攻击玩家（伤害事件/生命/音效）→
// 玩家攻击并击杀生物（痛动画 → 死亡事件 → 移除）。
func TestMobCombatFlow(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Fighter")
	player := findSession(t, instance, "Fighter")

	// 在玩家旁边生成僵尸（1 格距离，在攻击范围内）。
	spawnX, spawnY, spawnZ := instance.spawnPosition()
	mob := instance.addMob(spawnX, spawnY, spawnZ+1)

	spawnPacket := expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)
	_, offset, err := protocol.DecodeVarInt(spawnPacket)
	if err != nil {
		t.Fatal(err)
	}
	entityID, _, err := protocol.DecodeVarInt(spawnPacket[offset:])
	if err != nil || entityID != mob.ID {
		t.Fatalf("add entity ID = %d, want %d (err=%v)", entityID, mob.ID, err)
	}

	// 推进一帧：僵尸应立即攻击玩家。
	instance.tick()
	damagePacket := expectPlayPacket(t, conn, protocol.PlayPacketIDDamageEvent)
	_, offset, err = protocol.DecodeVarInt(damagePacket)
	if err != nil {
		t.Fatal(err)
	}
	victimID, tail, err := protocol.DecodeVarInt(damagePacket[offset:])
	if err != nil || victimID != player.entityID {
		t.Fatalf("damage event victim = %d, want %d (err=%v)", victimID, player.entityID, err)
	}
	offset += tail
	damageType, tail, err := protocol.DecodeVarInt(damagePacket[offset:])
	if err != nil || damageType != instance.mobAttackDamageTypeID {
		t.Fatalf("damage type = %d, want %d (err=%v)", damageType, instance.mobAttackDamageTypeID, err)
	}
	offset += tail
	causeID, _, err := protocol.DecodeVarInt(damagePacket[offset:])
	if err != nil || causeID != mob.ID+1 {
		t.Fatalf("damage cause = %d, want %d (err=%v)", causeID, mob.ID+1, err)
	}
	expectPlayPacket(t, conn, protocol.PlayPacketIDUpdateHealth)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSoundEffect)
	health, _, _ := player.healthStatus()
	if want := float32(maxPlayerHealth - mobAttackDamage); health != want {
		t.Fatalf("player health = %v, want %v", health, want)
	}

	// 攻击 5 次（每次 4 点）击杀僵尸；前 4 次各应收到痛动画。
	for i := 0; i < 4; i++ {
		sendAttack(t, conn, mob.ID)
		expectPlayPacket(t, conn, protocol.PlayPacketIDHurtAnimation)
	}
	sendAttack(t, conn, mob.ID)
	expectPlayPacket(t, conn, protocol.PlayPacketIDEntityEvent)

	// 死亡动画结束后应移除实体。
	for i := 0; i < mobDeathTicks; i++ {
		instance.tick()
	}
	expectPlayPacket(t, conn, protocol.PlayPacketIDEntityDestroy)
	instance.entityMu.Lock()
	remaining := len(instance.mobs)
	instance.entityMu.Unlock()
	if remaining != 0 {
		t.Fatalf("expected no mobs left, got %d", remaining)
	}
}

// TestPlayerDeathAndRespawn 验证玩家死亡（生命归零、死亡消息、免疫后续伤害）
// 与重生（Respawn → 区块重发 → 位置 → 满生命）。
func TestPlayerDeathAndRespawn(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Victim")
	player := findSession(t, instance, "Victim")

	if !instance.damagePlayer(player, maxPlayerHealth, "Zombie", -1, nil) {
		t.Fatal("lethal damage should apply")
	}
	expectPlayPacket(t, conn, protocol.PlayPacketIDDamageEvent)
	healthPacket := expectPlayPacket(t, conn, protocol.PlayPacketIDUpdateHealth)
	_, offset, err := protocol.DecodeVarInt(healthPacket)
	if err != nil {
		t.Fatal(err)
	}
	health, _, err := protocol.DecodeFloat32(healthPacket, offset)
	if err != nil || health != 0 {
		t.Fatalf("health after death = %v (err=%v)", health, err)
	}
	expectPlayPacket(t, conn, protocol.PlayPacketIDSoundEffect)
	expectSystemChat(t, conn, "was slain by Zombie")
	if !player.isDead() {
		t.Fatal("player should be dead")
	}
	if instance.damagePlayer(player, 5, "Zombie", -1, nil) {
		t.Fatal("damage while dead must not apply")
	}

	// 客户端发送重生请求。
	respawn := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDClientCommand)
	respawn = protocol.AppendVarInt(respawn, 0)
	if err := protocol.WritePacketWithCompression(conn, respawn, compressionThreshold); err != nil {
		t.Fatal(err)
	}
	expectPlayPacket(t, conn, protocol.PlayPacketIDRespawn)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetCenterChunk)
	expectPlayPacket(t, conn, protocol.PlayPacketIDChunkData)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSynchronizePlayerPos)
	expectPlayPacket(t, conn, protocol.PlayPacketIDUpdateHealth)

	health, _, _ = player.healthStatus()
	if health != maxPlayerHealth || player.isDead() {
		t.Fatalf("after respawn: health=%v dead=%v", health, player.isDead())
	}
}

// TestGameModeCommand 验证 /gamemode 命令切换模式、发送 Game Event
// 以及创造模式的伤害免疫。
func TestGameModeCommand(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Builder")
	player := findSession(t, instance, "Builder")
	if player.gameModeID() != uint8(config.GameModeSurvival) {
		t.Fatalf("default game mode = %d, want survival", player.gameModeID())
	}

	sendChatCommand(t, conn, "/gamemode creative")
	modePacket := expectPlayPacket(t, conn, protocol.PlayPacketIDGameEvent)
	_, offset, err := protocol.DecodeVarInt(modePacket)
	if err != nil {
		t.Fatal(err)
	}
	reason := modePacket[offset]
	value, _, err := protocol.DecodeFloat32(modePacket, offset+1)
	if err != nil || reason != 3 || value != float32(config.GameModeCreative) {
		t.Fatalf("game event = reason %d value %v (err=%v)", reason, value, err)
	}
	expectSystemChat(t, conn, "已将你的游戏模式设为 creative")
	if player.gameModeID() != uint8(config.GameModeCreative) {
		t.Fatalf("game mode = %d, want creative", player.gameModeID())
	}
	if instance.damagePlayer(player, 5, "Zombie", -1, nil) {
		t.Fatal("creative players must be immune to damage")
	}

	// 未知模式被拒绝。
	sendChatCommand(t, conn, "/gamemode hardcore")
	expectSystemChat(t, conn, "未知游戏模式")

	// 切回生存后可以受伤。
	sendChatCommand(t, conn, "/gamemode survival")
	expectPlayPacket(t, conn, protocol.PlayPacketIDGameEvent)
	expectSystemChat(t, conn, "已将你的游戏模式设为 survival")
	if !instance.damagePlayer(player, 5, "Zombie", -1, nil) {
		t.Fatal("damage should apply after switching back to survival")
	}
}

// TestMobSpawnAndCap 验证自动生成（尝试多次后必定生成）与数量上限。
func TestMobSpawnAndCap(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = true
	cfg.MaxMobs = 1
	instance, conn := joinServer(t, cfg, "Spawner")
	players := instance.playerSnapshot()

	spawned := false
	for attempt := 0; attempt < 50 && !spawned; attempt++ {
		instance.trySpawnMob(players)
		instance.entityMu.Lock()
		spawned = len(instance.mobs) > 0
		instance.entityMu.Unlock()
	}
	if !spawned {
		t.Fatal("expected a mob to spawn within 50 attempts")
	}
	expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)

	// 达到上限后不再生成。
	instance.trySpawnMob(players)
	instance.entityMu.Lock()
	count := len(instance.mobs)
	instance.entityMu.Unlock()
	if count != 1 {
		t.Fatalf("mob cap not respected: %d mobs", count)
	}
}
