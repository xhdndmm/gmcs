package server

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"gmcs/internal/config"
)

// TestMobPersistence 验证生物保存到 entities.json 并在重启后恢复。
func TestMobPersistence(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	first, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	spawnX, spawnY, spawnZ := first.spawnPosition()
	savedMob := first.addMob(spawnX+1, spawnY, spawnZ+1)
	savedMob.Health = 7

	if err := first.saveMobs(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cfg.WorldDir, mobFileName)); err != nil {
		t.Fatalf("expected %s to be written: %v", mobFileName, err)
	}
	if err := first.world.Close(); err != nil {
		t.Fatal(err)
	}

	// 模拟重启：用同一世界目录新建服务器。
	second, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer second.world.Close()
	second.entityMu.Lock()
	restored := make([]*mob, 0, len(second.mobs))
	for _, restoredMob := range second.mobs {
		restored = append(restored, restoredMob)
	}
	second.entityMu.Unlock()

	if len(restored) != 1 {
		t.Fatalf("expected 1 restored mob, got %d", len(restored))
	}
	got := restored[0]
	if got.TypeID != second.zombieTypeID {
		t.Fatalf("restored type = %d, want %d", got.TypeID, second.zombieTypeID)
	}
	if got.Health != 7 {
		t.Fatalf("restored health = %v, want 7", got.Health)
	}
	if math.Abs(got.X-(spawnX+1)) > 1e-6 || math.Abs(got.Y-spawnY) > 1e-6 || math.Abs(got.Z-(spawnZ+1)) > 1e-6 {
		t.Fatalf("restored position = (%v, %v, %v)", got.X, got.Y, got.Z)
	}
	if got.ID <= 0 {
		t.Fatalf("restored mob has invalid entity ID %d", got.ID)
	}
}

// TestMobPersistenceSkipsUnknownType 验证未知生物类型被跳过而不是导致失败。
func TestMobPersistenceSkipsUnknownType(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	content := []byte(`{"mobs":[
		{"type":"minecraft:creeper","x":1,"y":64,"z":1,"health":20},
		{"type":"minecraft:zombie","x":2,"y":64,"z":2,"yaw":90,"pitch":0,"health":5}
	]}`)
	if err := os.WriteFile(filepath.Join(cfg.WorldDir, mobFileName), content, 0o644); err != nil {
		t.Fatal(err)
	}
	instance, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer instance.world.Close()
	instance.entityMu.Lock()
	count := len(instance.mobs)
	instance.entityMu.Unlock()
	if count != 1 {
		t.Fatalf("expected only the zombie to load, got %d mobs", count)
	}
}
