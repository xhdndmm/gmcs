package server

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"math"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"
)

// readSavedPlayer 从 players.json 读取指定玩家的记录（测试辅助，避免直接访问会话物品栏）。
func readSavedPlayer(t *testing.T, worldDir string, uuid [16]byte) playerRecord {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(worldDir, playerFileName))
	if err != nil {
		t.Fatal(err)
	}
	var file playerFile
	if err := json.Unmarshal(content, &file); err != nil {
		t.Fatal(err)
	}
	want := hex.EncodeToString(uuid[:])
	for _, record := range file.Players {
		if record.UUID == want {
			return record
		}
	}
	t.Fatalf("players.json 中没有 UUID %s 的记录", want)
	return playerRecord{}
}

// waitPlayerOffline 等待玩家会话结束并落盘（退出路径先保存数据，再注销会话）。
func waitPlayerOffline(t *testing.T, instance *Server, uuid [16]byte) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		instance.mu.Lock()
		_, online := instance.players[uuid]
		instance.mu.Unlock()
		if !online {
			if _, saved := instance.playerDataSnapshot(uuid); saved {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("等待玩家数据保存超时")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestPlayerDataSavedOnDisconnect 验证玩家退出时会保存位置/生命/游戏模式/物品栏，
// 重新连接后状态被恢复，且不会重复发放初始物品。
func TestPlayerDataSavedOnDisconnect(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.ViewDistance = 2 // 与 joinServer 内部设置一致（同一 cfg 值在重连时复用）
	// 初始物品支持数量：首次加入会发放 stone×42，重连时应原样保留（不重复发放）。
	cfg.StartingItems = []string{"minecraft:stone*42"}
	instance, conn := joinServer(t, cfg, "Saver")
	addr := conn.RemoteAddr().String()

	sess := findSession(t, instance, "Saver")
	sess.setPlayerPosition(200.5, 66, -120.5, 90, 10)
	sess.stateMu.Lock()
	sess.health = 13
	sess.food = 7
	sess.saturation = 2.5
	sess.gameMode = uint8(config.GameModeCreative)
	sess.stateMu.Unlock()

	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	waitPlayerOffline(t, instance, sess.uuid)

	// 磁盘记录应包含位置/状态与首次加入发放的 stone×42。
	record := readSavedPlayer(t, cfg.WorldDir, sess.uuid)
	if math.Abs(record.X-200.5) > 1e-6 || math.Abs(record.Y-66) > 1e-6 || math.Abs(record.Z-(-120.5)) > 1e-6 ||
		math.Abs(float64(record.Yaw-90)) > 1e-6 || math.Abs(float64(record.Pitch-10)) > 1e-6 {
		t.Fatalf("位置记录不符：%+v", record)
	}
	if record.Health != 13 || record.Food != 7 || record.GameMode != "creative" {
		t.Fatalf("状态记录不符：%+v", record)
	}
	if len(record.Inventory) != 1 || record.Inventory[0].Slot != item.SlotHotbarStart ||
		record.Inventory[0].Item != "minecraft:stone" || record.Inventory[0].Count != 42 {
		t.Fatalf("物品栏记录不符：%+v", record.Inventory)
	}

	// 重连前修改初始物品配置：恢复逻辑不应再发放（否则下次落盘会变成 dirt×3）。
	instance.config.StartingItems = []string{"minecraft:dirt*3"}
	conn2 := joinAt(t, cfg, addr, "Saver", 0)
	sess2 := findSession(t, instance, "Saver")
	x, y, z, yaw, pitch := sess2.playerPosition()
	if math.Abs(x-200.5) > 1e-6 || math.Abs(y-66) > 1e-6 || math.Abs(z-(-120.5)) > 1e-6 ||
		math.Abs(float64(yaw-90)) > 1e-6 || math.Abs(float64(pitch-10)) > 1e-6 {
		t.Fatalf("位置未恢复：(%v,%v,%v) yaw=%v pitch=%v", x, y, z, yaw, pitch)
	}
	sess2.stateMu.Lock()
	health, food, gameMode := sess2.health, sess2.food, sess2.gameMode
	sess2.stateMu.Unlock()
	if health != 13 || food != 7 || gameMode != uint8(config.GameModeCreative) {
		t.Fatalf("状态未恢复：health=%v food=%v gameMode=%v", health, food, gameMode)
	}

	// 再次断开并读取磁盘记录：物品栏应仍是 stone×42，证明没有重复发放初始物品。
	if err := conn2.Close(); err != nil {
		t.Fatal(err)
	}
	waitPlayerOffline(t, instance, sess2.uuid)
	record = readSavedPlayer(t, cfg.WorldDir, sess2.uuid)
	if len(record.Inventory) != 1 || record.Inventory[0].Item != "minecraft:stone" || record.Inventory[0].Count != 42 {
		t.Fatalf("恢复后物品栏被改动：%+v", record.Inventory)
	}
}

// TestPlayerDeadStatePersists 验证死亡状态会保存：断开重连后仍处于死亡状态
// （生命 0），发送重生请求后才恢复。
func TestPlayerDeadStatePersists(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.ViewDistance = 2 // 与 joinServer 内部设置一致（重连时复用）
	instance, conn := joinServer(t, cfg, "DeadSaver")
	addr := conn.RemoteAddr().String()
	sess := findSession(t, instance, "DeadSaver")

	// 击杀玩家（最高伤害类型 mob_attack）。
	if !instance.damagePlayer(sess, maxPlayerHealth, "Zombie", -1, instance.mobAttackDamageTypeID, nil) {
		t.Fatal("lethal damage should apply")
	}
	if !sess.isDead() {
		t.Fatal("伤害后玩家应处于死亡状态")
	}

	// 不发重生请求直接断开：重新连接应保持死亡。
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	waitPlayerOffline(t, instance, sess.uuid)
	record := readSavedPlayer(t, cfg.WorldDir, sess.uuid)
	if !record.Dead {
		t.Fatalf("磁盘记录应为死亡状态：%+v", record)
	}

	conn2 := joinAt(t, cfg, addr, "DeadSaver", 0)
	sess2 := findSession(t, instance, "DeadSaver")
	if !sess2.isDead() {
		t.Fatal("重连后应保持死亡状态")
	}
	if health, _, _ := sess2.healthStatus(); health != 0 {
		t.Fatalf("重连后生命值 = %v, want 0", health)
	}

	// 发送重生请求（Client Command 0）：恢复满生命。
	respawn := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDClientCommand)
	respawn = protocol.AppendVarInt(respawn, 0)
	if err := protocol.WritePacketWithCompression(conn2, respawn, compressionThreshold); err != nil {
		t.Fatal(err)
	}
	expectPlayPacket(t, conn2, protocol.PlayPacketIDRespawn)
	deadline := time.Now().Add(5 * time.Second)
	for sess2.isDead() {
		if time.Now().After(deadline) {
			t.Fatal("重连后重生请求未生效")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if health, _, _ := sess2.healthStatus(); health != maxPlayerHealth {
		t.Fatalf("重生后生命值 = %v, want %v", health, maxPlayerHealth)
	}
}

// readPlayerFileRecord 从磁盘读取指定玩家的记录（文件不存在或损坏时返回 false）。
func readPlayerFileRecord(path string, uuid [16]byte) (playerRecord, bool) {
	content, err := os.ReadFile(path)
	if err != nil {
		return playerRecord{}, false
	}
	var file playerFile
	if err := json.Unmarshal(content, &file); err != nil {
		return playerRecord{}, false
	}
	want := hex.EncodeToString(uuid[:])
	for _, record := range file.Players {
		if record.UUID == want {
			return record, true
		}
	}
	return playerRecord{}, false
}

// TestPlayerPeriodicAutosave 验证周期自动保存会把在线玩家的最新位置写入磁盘。
func TestPlayerPeriodicAutosave(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.ViewDistance = 2
	instance, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	instance.keepAliveInterval = time.Hour
	instance.tickInterval = 0
	instance.chunkUnloadInterval = time.Hour
	instance.playerAutosaveInterval = 50 * time.Millisecond
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
		case <-serveResult:
		case <-time.After(5 * time.Second):
			t.Error("服务器未在超时内关闭")
		}
	})

	conn := joinAt(t, cfg, listener.Addr().String(), "AutoSaver", len(cfg.StartingItems))
	t.Cleanup(func() { _ = conn.Close() })
	sess := findSession(t, instance, "AutoSaver")
	moveX, moveZ := 12.5, -8.25
	sess.setPlayerPosition(moveX, 66, moveZ, 180, 5)

	// 等待周期保存把新位置写入磁盘（首次保存可能仍是出生点记录）。
	path := filepath.Join(cfg.WorldDir, playerFileName)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if record, ok := readPlayerFileRecord(path, sess.uuid); ok &&
			math.Abs(record.X-moveX) < 1e-6 && math.Abs(record.Z-moveZ) < 1e-6 && !record.Dead {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("等待周期自动保存超时")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestLoadPlayerDataRejectsCorruptFile 验证损坏的 players.json 会返回错误（服务器记录日志后继续运行）。
func TestLoadPlayerDataRejectsCorruptFile(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	instance, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer instance.testWorld().Close()
	if err := os.WriteFile(filepath.Join(cfg.WorldDir, playerFileName), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := instance.loadPlayerData(); err == nil {
		t.Fatal("损坏的 players.json 应返回错误")
	}
}
