package server

import (
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gmcs/internal/config"
	"gmcs/internal/item"
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

// TestLoadPlayerDataRejectsCorruptFile 验证损坏的 players.json 会返回错误（服务器记录日志后继续运行）。
func TestLoadPlayerDataRejectsCorruptFile(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	instance, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer instance.world.Close()
	if err := os.WriteFile(filepath.Join(cfg.WorldDir, playerFileName), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := instance.loadPlayerData(); err == nil {
		t.Fatal("损坏的 players.json 应返回错误")
	}
}
