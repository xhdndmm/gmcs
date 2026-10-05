package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDefaultConfigIsValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("default config should be valid: %v", err)
	}
}

// TestStringListAcceptsSingleString 验证 ops/starting_items 允许写成单个字符串，
// 避免用户把单值写成字符串导致服务器无法启动。
func TestStringListAcceptsSingleString(t *testing.T) {
	var cfg Config
	if err := json.Unmarshal([]byte(`{"ops": "Alice", "starting_items": "minecraft:stone*3"}`), &cfg); err != nil {
		t.Fatalf("single string lists should be accepted: %v", err)
	}
	if len(cfg.Ops) != 1 || cfg.Ops[0] != "Alice" {
		t.Fatalf("ops = %v, want [Alice]", cfg.Ops)
	}
	if len(cfg.StartingItems) != 1 || cfg.StartingItems[0] != "minecraft:stone*3" {
		t.Fatalf("starting_items = %v", cfg.StartingItems)
	}

	// 数组写法保持可用。
	var array Config
	if err := json.Unmarshal([]byte(`{"ops": ["A", "B"], "starting_items": ["minecraft:dirt"]}`), &array); err != nil {
		t.Fatalf("arrays should still be accepted: %v", err)
	}
	if len(array.Ops) != 2 || array.Ops[1] != "B" {
		t.Fatalf("ops = %v, want [A B]", array.Ops)
	}

	// null 等价于清空列表。
	var nullable Config
	if err := json.Unmarshal([]byte(`{"ops": null}`), &nullable); err != nil || nullable.Ops != nil {
		t.Fatalf("null should clear the list: %v (err=%v)", nullable.Ops, err)
	}

	// 其它类型仍然报错。
	var bad Config
	if err := json.Unmarshal([]byte(`{"ops": 42}`), &bad); err == nil {
		t.Fatal("numeric ops should be rejected")
	}
}

func TestGameModeID(t *testing.T) {
	cases := map[string]GameMode{
		"survival":  GameModeSurvival,
		"creative":  GameModeCreative,
		"adventure": GameModeAdventure,
		"spectator": GameModeSpectator,
	}
	for name, want := range cases {
		got, ok := GameModeID(name)
		if !ok || got != want {
			t.Fatalf("GameModeID(%q) = %d, %v; want %d, true", name, got, ok, want)
		}
	}
	if _, ok := GameModeID("HARDCORE"); ok {
		t.Fatal("unknown game mode should not resolve")
	}
}

func TestValidateRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "empty listen address", mutate: func(c *Config) { c.ListenAddress = "" }},
		{name: "empty version", mutate: func(c *Config) { c.VersionName = "" }},
		{name: "negative protocol", mutate: func(c *Config) { c.ProtocolVersion = -1 }},
		{name: "zero players", mutate: func(c *Config) { c.MaxPlayers = 0 }},
		{name: "zero connections", mutate: func(c *Config) { c.MaxConnections = 0 }},
		{name: "view distance too small", mutate: func(c *Config) { c.ViewDistance = 1 }},
		{name: "view distance too large", mutate: func(c *Config) { c.ViewDistance = 33 }},
		{name: "empty world dir", mutate: func(c *Config) { c.WorldDir = "" }},
		{name: "negative autosave", mutate: func(c *Config) { c.AutosaveSeconds = -1 }},
		{name: "unknown game mode", mutate: func(c *Config) { c.GameMode = "hardcore" }},
		{name: "negative max mobs", mutate: func(c *Config) { c.MaxMobs = -1 }},
		{name: "negative world border", mutate: func(c *Config) { c.WorldBorderSize = -1 }},
		{name: "world border too small", mutate: func(c *Config) { c.WorldBorderSize = 8 }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := Default()
			test.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestLoadCreatesDefaultFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gmcs.json")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	// 首次运行会分配随机种子，其余字段与默认值一致。
	if cfg.WorldSeed == 0 {
		t.Fatal("expected a random non-zero world seed on first run")
	}
	want := Default()
	want.WorldSeed = cfg.WorldSeed
	if !equalConfigs(cfg, want) {
		t.Fatalf("expected default config with a random seed, got %+v", cfg)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected config file to be created: %v", err)
	}
	// 种子必须写入文件：重启服务器后地形保持一致。
	saved, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.WorldSeed != cfg.WorldSeed {
		t.Fatalf("world seed not persisted: %d != %d", saved.WorldSeed, cfg.WorldSeed)
	}
}

func TestLoadKeepsWorldSeedFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gmcs.json")
	if err := os.WriteFile(path, []byte(`{"world_seed": 12345}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorldSeed != 12345 {
		t.Fatalf("explicit world seed was overwritten: %d", cfg.WorldSeed)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gmcs.json")
	cfg := Default()
	cfg.MOTD = "round trip"
	cfg.WorldDir = "custom-world"
	cfg.ViewDistance = 16
	cfg.StartingItems = []string{"minecraft:stone", "minecraft:diamond_sword"}
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !equalConfigs(loaded, cfg) {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", loaded, cfg)
	}
}

func TestLoadMergesDefaultsForMissingFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gmcs.json")
	// 只提供部分字段，其余应保留默认值。
	content := []byte(`{"motd": "partial"}`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.MOTD != "partial" {
		t.Fatalf("motd = %q", loaded.MOTD)
	}
	if loaded.ListenAddress != Default().ListenAddress || loaded.WorldDir != Default().WorldDir {
		t.Fatalf("missing fields not defaulted: %+v", loaded)
	}
}

func TestLoadRejectsInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gmcs.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestSaveIsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gmcs.json")
	if err := Save(path, Default()); err != nil {
		t.Fatal(err)
	}
	// 临时文件不应残留。
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "gmcs.json" {
			t.Fatalf("unexpected leftover file %q", entry.Name())
		}
	}
	// 保存的内容必须是合法 JSON。
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(content, &decoded); err != nil {
		t.Fatalf("saved config is not valid JSON: %v", err)
	}
	if decoded["world_dir"] != Default().WorldDir {
		t.Fatalf("world_dir = %v", decoded["world_dir"])
	}
}

// equalConfigs 比较两个配置（包含切片字段）。
func equalConfigs(a, b Config) bool {
	return reflect.DeepEqual(a, b)
}
