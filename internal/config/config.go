// Package config 提供服务器的配置结构与 JSON 文件持久化。
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

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
	// AutosaveSeconds 是自动保存间隔（秒）；0 表示禁用自动保存。
	AutosaveSeconds int `json:"autosave_seconds"`
	// OnlineMode 启用正版验证（通过会话服务器确认玩家身份）。
	OnlineMode bool `json:"online_mode"`
	// SessionServerURL 是会话验证服务基地址（在线模式使用）。
	SessionServerURL string `json:"session_server_url"`
	// StartingItems 是新玩家进入世界时获得的物品（命名空间 ID）。
	StartingItems []string `json:"starting_items"`
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
		AutosaveSeconds: 300, OnlineMode: false,
		SessionServerURL: "https://sessionserver.mojang.com", StartingItems: []string{"minecraft:stone"},
	}
}

// Load 从 path 读取配置。文件不存在时写入默认配置并返回默认值；
// 文件中的缺失字段保留默认值（以 Default 为基准合并）。
func Load(path string) (Config, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return Config{}, fmt.Errorf("读取 %s：%w", path, err)
		}
		cfg := Default()
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
	if c.OnlineMode && c.SessionServerURL == "" {
		return fmt.Errorf("session server URL must not be empty in online mode")
	}
	return nil
}
