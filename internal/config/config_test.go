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
	if !equalConfigs(cfg, Default()) {
		t.Fatalf("expected default config, got %+v", cfg)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected config file to be created: %v", err)
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
