// Package config 提供服务器的配置结构与 JSON 文件持久化。
package config

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// StringList 是一个字符串列表，允许在 JSON 中写成数组或单个字符串。
// 例如 "ops": "Alice" 等价于 "ops": ["Alice"]，
// 避免把单个值写成字符串导致服务器无法启动。
type StringList []string

// UnmarshalJSON 接受字符串数组、单个字符串或 null。
func (l *StringList) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*l = nil
		return nil
	}
	if trimmed[0] == '"' {
		var single string
		if err := json.Unmarshal(trimmed, &single); err != nil {
			return err
		}
		*l = StringList{single}
		return nil
	}
	var list []string
	if err := json.Unmarshal(trimmed, &list); err != nil {
		return err
	}
	*l = list
	return nil
}

// Config 是服务器的全部配置。
// 字段与 JSON 中的 snake_case 名称一一对应；加载时缺失字段保留默认值。
type Config struct {
	// ListenAddress 是 TCP 监听地址。
	ListenAddress string `json:"listen_address"`
	// MOTD 是服务器列表中显示的消息。
	MOTD string `json:"motd"`
	// VersionName 是服务器列表中显示的版本名。
	VersionName string `json:"version_name"`
	// ProtocolVersion 是服务器列表中的协议号（同时用于拒绝不兼容客户端）。
	ProtocolVersion int32 `json:"protocol_version"`
	// MaxPlayers 是服务器列表显示的最大玩家数。
	MaxPlayers int `json:"max_players"`
	// MaxConnections 是允许的最大并发连接数。
	MaxConnections int `json:"max_connections"`
	// ViewDistance 是发送给客户端的视距（区块）。
	ViewDistance int `json:"view_distance"`
	// WorldDir 是地图数据目录。
	WorldDir string `json:"world_dir"`
	// WorldSeed 是地形生成种子：相同种子生成相同地形。
	// 首次运行（生成配置文件）时会写入一个随机值。
	WorldSeed int64 `json:"world_seed"`
	// WorldBorderSize 是世界边界的边长（方块，正方形，以 0,0 为中心）；
	// 0 表示不限制。启用后客户端显示边界，且生物不会生成/移动出边界。
	WorldBorderSize int `json:"world_border_size"`
	// GameMode 是新玩家的游戏模式（survival、creative、adventure、spectator）。
	GameMode string `json:"game_mode"`
	// SpawnMonsters 控制是否在玩家附近生成敌对生物。
	SpawnMonsters bool `json:"spawn_monsters"`
	// MaxMobs 是同时存在的生物数量上限（0 表示不生成）。
	MaxMobs int `json:"max_mobs"`
	// AutosaveSeconds 是自动保存间隔（秒）；0 表示禁用自动保存。
	AutosaveSeconds int `json:"autosave_seconds"`
	// OnlineMode 启用正版验证（通过会话服务器确认玩家身份）。
	OnlineMode bool `json:"online_mode"`
	// SessionServerURL 是会话验证服务基地址（在线模式使用）。
	SessionServerURL string `json:"session_server_url"`
	// StartingItems 是新玩家进入世界时获得的物品（命名空间 ID）。
	// JSON 中可写数组或单个字符串。
	StartingItems StringList `json:"starting_items"`
	// Ops 是管理员玩家名列表：名单中的玩家可以使用管理命令（按权限等级
	// 过滤）。列表项支持 "name"（等价 name:4，与原版 /op 相同）与
	// "name:level"（level 1–4，例如 "Alice:2"），命令要求等级见
	// internal/server/commands.go。JSON 中可写数组或单个字符串。
	Ops StringList `json:"ops"`
	// Permissions 是细粒度命令权限节点（按玩家名，大小写不敏感）。
	// 键为玩家名（"*" 表示所有玩家），值为节点列表，例如：
	//
	//	"permissions": {
	//	  "Alice": ["gmcs.command.gamemode", "gmcs.command.*"],
	//	  "*": "gmcs.command.spawn"
	//	}
	//
	// 支持精确节点、前缀通配（"gmcs.command.*"）与全通配（"*"）。
	// 拥有节点即可使用对应命令，无需管理员等级；内建节点见
	// internal/server/commands.go 的命令表。
	Permissions map[string]StringList `json:"permissions"`
	// PprofAddress 是 pprof 诊断监听地址（如 "127.0.0.1:6060"）；
	// 空值表示不启用。启用后在该地址上提供 /debug/pprof/*（CPU/内存/
	// goroutine 分析）。仅用于本地诊断，不要暴露到公网。
	PprofAddress string `json:"pprof_address"`
	// LogFile 是服务器日志文件路径（相对路径基于工作目录）；
	// 空表示只输出到标准错误。日志按 LogMaxSizeMB 轮转（备份为 <路径>.1）。
	LogFile string `json:"log_file"`
	// LogLevel 是日志级别（debug、info、warn、error）。
	LogLevel string `json:"log_level"`
	// LogMaxSizeMB 是单个日志文件的大小上限（MiB；0 表示默认 16）。
	LogMaxSizeMB int `json:"log_max_size_mb"`
	// AuditLogFile 是审计日志路径（聊天/命令/玩家进出，JSONL 格式）；
	// 空表示禁用审计日志。
	AuditLogFile string `json:"audit_log_file"`
}

// Default 返回默认配置。
func Default() Config {
	return Config{
		ListenAddress:   ":25565",
		MOTD:            "A gmcs 1.21.11 development server",
		VersionName:     "gmcs-1.21.11",
		ProtocolVersion: 774, // Minecraft Java Edition 1.21.11
		MaxPlayers:      100,
		MaxConnections:  256,
		ViewDistance:    10,
		WorldDir:        "world",
		WorldSeed:       0,
		WorldBorderSize: 0,
		GameMode:        "survival",
		SpawnMonsters:   true,
		MaxMobs:         8,
		AutosaveSeconds: 300, OnlineMode: false,
		SessionServerURL: "https://sessionserver.mojang.com", StartingItems: StringList{"minecraft:stone"},
		LogFile: "logs/gmcs.log", LogLevel: "info", LogMaxSizeMB: 16,
		AuditLogFile: "logs/audit.jsonl",
	}
}

// Load 从 path 读取配置。文件不存在时写入默认配置并返回默认值；
// 首次生成配置文件时会分配随机世界种子（写入文件后固定）。
// 文件中的缺失字段保留默认值（以 Default 为基准合并）。
func Load(path string) (Config, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return Config{}, fmt.Errorf("读取 %s：%w", path, err)
		}
		cfg := Default()
		cfg.WorldSeed = randomSeed()
		if err := Save(path, cfg); err != nil {
			return Config{}, err
		}
		return cfg, nil
	}
	cfg := Default()
	if err := json.Unmarshal(content, &cfg); err != nil {
		return Config{}, fmt.Errorf("解析 %s：%w", path, err)
	}
	return cfg, nil
}

// Save 原子地把配置写入 path（先写临时文件再重命名，避免部分写入）。
func Save(path string, cfg Config) error {
	content, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	temp := path + ".tmp"
	if err := os.WriteFile(temp, content, 0o644); err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

// Validate 校验配置。
func (c Config) Validate() error {
	if c.ListenAddress == "" {
		return fmt.Errorf("listen address must not be empty")
	}
	if c.VersionName == "" {
		return fmt.Errorf("version name must not be empty")
	}
	if c.ProtocolVersion < 0 {
		return fmt.Errorf("protocol version must not be negative")
	}
	if c.MaxPlayers < 1 {
		return fmt.Errorf("max players must be positive")
	}
	if c.MaxConnections < 1 {
		return fmt.Errorf("max connections must be positive")
	}
	if c.ViewDistance < 2 || c.ViewDistance > 32 {
		return fmt.Errorf("view distance must be between 2 and 32")
	}
	if c.WorldDir == "" {
		return fmt.Errorf("world directory must not be empty")
	}
	if c.AutosaveSeconds < 0 {
		return fmt.Errorf("autosave seconds must not be negative")
	}
	if c.MaxMobs < 0 {
		return fmt.Errorf("max mobs must not be negative")
	}
	if c.WorldBorderSize < 0 {
		return fmt.Errorf("world border size must not be negative")
	}
	if c.WorldBorderSize > 0 && c.WorldBorderSize < 16 {
		return fmt.Errorf("world border size must be at least 16 blocks")
	}
	if _, ok := GameModeID(c.GameMode); !ok {
		return fmt.Errorf("unknown game mode %q", c.GameMode)
	}
	if c.OnlineMode && c.SessionServerURL == "" {
		return fmt.Errorf("session server URL must not be empty in online mode")
	}
	if c.LogLevel != "" {
		var level slog.Level
		if err := level.UnmarshalText([]byte(c.LogLevel)); err != nil {
			return fmt.Errorf("unknown log level %q", c.LogLevel)
		}
	}
	if c.LogMaxSizeMB < 0 {
		return fmt.Errorf("log max size must not be negative")
	}
	return nil
}

// GameMode 是游戏模式（与原版 Game Type ID 一致）。
type GameMode uint8

// 游戏模式常量。
const (
	GameModeSurvival  GameMode = 0
	GameModeCreative  GameMode = 1
	GameModeAdventure GameMode = 2
	GameModeSpectator GameMode = 3
)

// GameModeID 把配置中的游戏模式名解析为 ID。
func GameModeID(name string) (GameMode, bool) {
	switch name {
	case "survival":
		return GameModeSurvival, true
	case "creative":
		return GameModeCreative, true
	case "adventure":
		return GameModeAdventure, true
	case "spectator":
		return GameModeSpectator, true
	}
	return 0, false
}

// GameModeName 返回游戏模式的配置名（GameModeID 的反向映射）。
func GameModeName(mode GameMode) (string, bool) {
	switch mode {
	case GameModeSurvival:
		return "survival", true
	case GameModeCreative:
		return "creative", true
	case GameModeAdventure:
		return "adventure", true
	case GameModeSpectator:
		return "spectator", true
	}
	return "", false
}

// randomSeed 生成非零随机世界种子（加密随机源；失败时回退到时间戳）。
func randomSeed() int64 {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		seed := time.Now().UnixNano()
		if seed == 0 {
			return 1
		}
		return seed
	}
	seed := int64(binary.LittleEndian.Uint64(buf[:]))
	if seed == 0 {
		return 1
	}
	return seed
}
