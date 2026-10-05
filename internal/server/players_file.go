package server

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/registry"
)

// 玩家数据持久化：世界目录下的 players.json。
//
// 与 entities.json 一样是 gmcs 自定义格式（不是原版的 playerdata/<uuid>.dat）：
// 保存位置/朝向、生命/饥饿/饱和、游戏模式与物品栏（物品按命名空间 ID 存储，
// 跨 Minecraft 版本仍可读）。写入为“临时文件 + 重命名”的原子替换。
//
// 保存时机：玩家退出（会话结束时）与服务器关闭（会话关闭同样走退出路径）；
// 服务器进程被强杀（SIGKILL）或崩溃时会丢失自上次退出以来的进度（见
// docs/TODO.md 已知限制）。

const playerFileName = "players.json"

type playerFile struct {
	Players []playerRecord `json:"players"`
}

// playerRecord 是一个玩家的持久化状态。死亡状态不保存：生命值 <= 0 的记录
// 在进入世界时按满生命处理。
type playerRecord struct {
	UUID       string       `json:"uuid"`
	Name       string       `json:"name"`
	X          float64      `json:"x"`
	Y          float64      `json:"y"`
	Z          float64      `json:"z"`
	Yaw        float32      `json:"yaw"`
	Pitch      float32      `json:"pitch"`
	Health     float32      `json:"health"`
	Food       int32        `json:"food"`
	Saturation float32      `json:"saturation"`
	GameMode   string       `json:"game_mode"`
	Inventory  []playerItem `json:"inventory"`
}

// playerItem 是物品栏中的一个堆栈；Item 为命名空间 ID。
type playerItem struct {
	Slot  int    `json:"slot"`
	Item  string `json:"item"`
	Count int32  `json:"count"`
}

// loadPlayerData 从世界目录读取玩家数据；文件不存在时保持为空。
// 文件格式损坏时返回错误（服务器记录日志后继续运行）；单条记录损坏时跳过。
func (s *Server) loadPlayerData() error {
	path := filepath.Join(s.config.WorldDir, playerFileName)
	content, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("读取 %s：%w", path, err)
	}
	var file playerFile
	if err := json.Unmarshal(content, &file); err != nil {
		return fmt.Errorf("解析 %s：%w", path, err)
	}
	s.playerDataMu.Lock()
	defer s.playerDataMu.Unlock()
	for _, record := range file.Players {
		uuid, uuidErr := parseMobUUID(record.UUID)
		if uuidErr != nil {
			slog.Warn("跳过无效的玩家记录", "uuid", record.UUID, "error", uuidErr)
			continue
		}
		s.playerData[uuid] = record
	}
	if len(s.playerData) > 0 {
		slog.Info("loaded player data", "count", len(s.playerData))
	}
	return nil
}

// playerDataSnapshot 返回某个玩家的持久化记录（副本）与是否存在。
func (s *Server) playerDataSnapshot(uuid [16]byte) (playerRecord, bool) {
	s.playerDataMu.Lock()
	defer s.playerDataMu.Unlock()
	record, ok := s.playerData[uuid]
	return record, ok
}

// savePlayerData 在会话结束时把玩家状态写入内存表并落盘。
// 必须由会话 goroutine 调用（物品栏不是并发安全的）。
func (s *Server) savePlayerData(player *session) {
	record := playerRecordFromSession(player)
	s.playerDataMu.Lock()
	s.playerData[player.uuid] = record
	err := s.writePlayerFileLocked()
	s.playerDataMu.Unlock()
	if err != nil {
		slog.Error("保存玩家数据失败", "name", player.name, "error", err)
	}
}

// writePlayerFileLocked 把全部玩家数据原子写入磁盘；调用方必须持有 playerDataMu。
func (s *Server) writePlayerFileLocked() error {
	records := make([]playerRecord, 0, len(s.playerData))
	for _, record := range s.playerData {
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].UUID < records[j].UUID })
	content, err := json.MarshalIndent(playerFile{Players: records}, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	path := filepath.Join(s.config.WorldDir, playerFileName)
	temp := path + ".tmp"
	if err := os.WriteFile(temp, content, 0o644); err != nil {
		return fmt.Errorf("写入 %s：%w", temp, err)
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("重命名 %s：%w", path, err)
	}
	return nil
}

// playerRecordFromSession 由会话当前状态构造持久化记录。
func playerRecordFromSession(player *session) playerRecord {
	x, y, z, yaw, pitch := player.playerPosition()
	player.stateMu.Lock()
	health := player.health
	food := player.food
	saturation := player.saturation
	gameMode := player.gameMode
	player.stateMu.Unlock()
	modeName, ok := config.GameModeName(config.GameMode(gameMode))
	if !ok {
		modeName = "survival"
	}
	items := make([]playerItem, 0, item.InventorySlots)
	for slot, stack := range player.inventory.Slots() {
		if stack.IsEmpty() {
			continue
		}
		name, ok := registry.ItemName(stack.ItemID)
		if !ok {
			continue
		}
		items = append(items, playerItem{Slot: slot, Item: name, Count: stack.Count})
	}
	return playerRecord{
		UUID:       hex.EncodeToString(player.uuid[:]),
		Name:       player.name,
		X:          x,
		Y:          y,
		Z:          z,
		Yaw:        yaw,
		Pitch:      pitch,
		Health:     health,
		Food:       food,
		Saturation: saturation,
		GameMode:   modeName,
		Inventory:  items,
	}
}

// applyPlayerRecord 把持久化的玩家状态应用到会话（进入世界前调用）。
// 位置非法（NaN/Inf）时回退到出生点；生命值非法或死亡时按满生命处理；
// 未知物品（例如来自其他版本的记录）会被跳过。
func (s *session) applyPlayerRecord(record playerRecord) {
	x, y, z := record.X, record.Y, record.Z
	if math.IsNaN(x) || math.IsNaN(y) || math.IsNaN(z) ||
		math.IsInf(x, 0) || math.IsInf(y, 0) || math.IsInf(z, 0) {
		x, y, z = s.server.spawnPosition()
	}
	s.setPlayerPosition(x, y, z, record.Yaw, record.Pitch)

	s.stateMu.Lock()
	if record.Health > 0 && record.Health <= maxPlayerHealth {
		s.health = record.Health
	} else {
		s.health = maxPlayerHealth
	}
	food := record.Food
	if food < 0 {
		food = 0
	} else if food > maxPlayerFood {
		food = maxPlayerFood
	}
	s.food = food
	saturation := record.Saturation
	if saturation < 0 || math.IsNaN(float64(saturation)) {
		saturation = 0
	} else if saturation > float32(maxPlayerFood) {
		saturation = float32(maxPlayerFood)
	}
	s.saturation = saturation
	if mode, ok := config.GameModeID(record.GameMode); ok {
		s.gameMode = uint8(mode)
	}
	s.stateMu.Unlock()

	for _, stored := range record.Inventory {
		if stored.Slot < 0 || stored.Slot >= item.InventorySlots || stored.Count <= 0 {
			continue
		}
		stack, err := item.FromName(stored.Item, stored.Count)
		if err != nil {
			slog.Warn("跳过未知的持久化物品", "name", s.name, "item", stored.Item, "error", err)
			continue
		}
		s.inventory.Set(stored.Slot, stack)
	}
}
