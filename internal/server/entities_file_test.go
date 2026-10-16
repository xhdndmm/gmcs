//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/item"

	"gmcs/internal/world"
)

// TestMobPersistence 验证生物保存到 entities.json 并在重启后恢复。
func TestMobPersistence(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	first, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	spawnX, spawnY, spawnZ := first.spawnPositionFor(world.DimensionOverworld)
	savedMob := first.addMob(world.DimensionOverworld, mobZombie, spawnX+1, spawnY, spawnZ+1)
	savedMob.Health = 7

	if err := first.saveEntities(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cfg.WorldDir, entityFileName)); err != nil {
		t.Fatalf("expected %s to be written: %v", entityFileName, err)
	}
	if err := first.testWorld().Close(); err != nil {
		t.Fatal(err)
	}

	// 模拟重启：用同一世界目录新建服务器。
	second, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer second.testWorld().Close()
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
		{"type":"minecraft:blaze","x":1,"y":64,"z":1,"health":20},
		{"type":"minecraft:zombie","x":2,"y":64,"z":2,"yaw":90,"pitch":0,"health":5}
	]}`)
	if err := os.WriteFile(filepath.Join(cfg.WorldDir, entityFileName), content, 0o644); err != nil {
		t.Fatal(err)
	}
	instance, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer instance.testWorld().Close()
	instance.entityMu.Lock()
	count := len(instance.mobs)
	instance.entityMu.Unlock()
	if count != 1 {
		t.Fatalf("expected only the zombie to load, got %d mobs", count)
	}
}

// TestItemPersistence 验证掉落物保存到 entities.json 并在重启后恢复
// （位置与物品保留，速度不保存）。
func TestItemPersistence(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	first, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	spawnX, spawnY, spawnZ := first.spawnPositionFor(world.DimensionOverworld)
	stack, err := item.FromName("minecraft:stone", 7)
	if err != nil {
		t.Fatal(err)
	}
	// 带速度与拾取延迟生成，验证它们一并持久化。
	spawned := first.spawnItem(world.DimensionOverworld, stack, spawnX+1, spawnY, spawnZ+1, 0.25, -0.5, 0.125, 40)
	if spawned == nil {
		t.Fatal("spawnItem returned nil")
	}
	first.entityMu.Lock()
	spawned.AgeTicks = 1234
	first.entityMu.Unlock()
	if err := first.saveEntities(); err != nil {
		t.Fatal(err)
	}
	if err := first.testWorld().Close(); err != nil {
		t.Fatal(err)
	}

	// 模拟重启：用同一世界目录新建服务器。
	second, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer second.testWorld().Close()
	second.entityMu.Lock()
	restored := make([]*itemEntity, 0, len(second.items))
	for _, e := range second.items {
		restored = append(restored, e)
	}
	second.entityMu.Unlock()
	if len(restored) != 1 {
		t.Fatalf("expected 1 restored item, got %d", len(restored))
	}
	got := restored[0]
	if got.Stack.ItemID != stack.ItemID || got.Stack.Count != 7 {
		t.Fatalf("restored stack = %+v, want stone x7", got.Stack)
	}
	if math.Abs(got.X-(spawnX+1)) > 1e-6 || math.Abs(got.Y-spawnY) > 1e-6 || math.Abs(got.Z-(spawnZ+1)) > 1e-6 {
		t.Fatalf("restored position = (%v, %v, %v)", got.X, got.Y, got.Z)
	}
	// 速度/存活时间/拾取延迟同样恢复（对照原版 ItemEntity 保存的字段）。
	if got.VelX != 0.25 || got.VelY != -0.5 || got.VelZ != 0.125 {
		t.Fatalf("restored velocity = (%v, %v, %v), want (0.25, -0.5, 0.125)", got.VelX, got.VelY, got.VelZ)
	}
	if got.AgeTicks != 1234 {
		t.Fatalf("restored age = %d, want 1234", got.AgeTicks)
	}
	if got.PickupDelayTicks != 40 {
		t.Fatalf("restored pickup delay = %d, want 40", got.PickupDelayTicks)
	}
}

// TestItemPersistenceSanitizesInvalidState 验证存档中的异常掉落物字段被限幅：
// 过大速度、负年龄、越界拾取延迟都不会进入运行时状态。
func TestItemPersistenceSanitizesInvalidState(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	payload := `{"items":[{"item":"minecraft:stone","count":1,"uuid":"00112233445566778899aabbccddeeff",` +
		`"x":1,"y":64,"z":2,"velX":1e9,"velY":-1e9,"ageTicks":-5,"pickupDelay":999}]}`
	if err := os.WriteFile(filepath.Join(cfg.WorldDir, entityFileName), []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	instance, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer instance.testWorld().Close()
	instance.entityMu.Lock()
	restored := make([]*itemEntity, 0, len(instance.items))
	for _, e := range instance.items {
		restored = append(restored, e)
	}
	instance.entityMu.Unlock()
	if len(restored) != 1 {
		t.Fatalf("expected 1 restored item, got %d", len(restored))
	}
	got := restored[0]
	if got.VelX != 100 || got.VelY != -100 {
		t.Fatalf("速度应被限幅到 ±100，得到 (%v, %v)", got.VelX, got.VelY)
	}
	if got.AgeTicks != 0 {
		t.Fatalf("负年龄应归零，得到 %d", got.AgeTicks)
	}
	if got.PickupDelayTicks != itemPickupDelayPlayer {
		t.Fatalf("拾取延迟应限制在 %d，得到 %d", itemPickupDelayPlayer, got.PickupDelayTicks)
	}
}

// TestArrowPersistence 验证箭矢（含速度与伤害）保存到 entities.json 并在
// 重启后恢复。
func TestArrowPersistence(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	first, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !first.arrowsEnabled {
		t.Skip("arrows disabled (missing registry data)")
	}
	spawnX, spawnY, spawnZ := first.spawnPositionFor(world.DimensionOverworld)
	first.entityMu.Lock()
	saved := &arrowEntity{
		ID:       first.entityIDs.Add(1),
		UUID:     newEntityUUID(),
		Dim:      world.DimensionOverworld,
		X:        spawnX + 1,
		Y:        spawnY + 1,
		Z:        spawnZ + 1,
		VelX:     0.5,
		VelY:     -0.25,
		VelZ:     0.125,
		Damage:   4,
		AgeTicks: 37,
	}
	first.arrows[saved.ID] = saved
	first.entityMu.Unlock()

	if err := first.saveEntities(); err != nil {
		t.Fatal(err)
	}
	if err := first.testWorld().Close(); err != nil {
		t.Fatal(err)
	}

	// 模拟重启：用同一世界目录新建服务器。
	second, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer second.testWorld().Close()
	second.entityMu.Lock()
	restored := make([]*arrowEntity, 0, len(second.arrows))
	for _, a := range second.arrows {
		restored = append(restored, a)
	}
	second.entityMu.Unlock()
	if len(restored) != 1 {
		t.Fatalf("expected 1 restored arrow, got %d", len(restored))
	}
	got := restored[0]
	if got.Dim != world.DimensionOverworld {
		t.Fatalf("restored dimension = %v", got.Dim)
	}
	if math.Abs(got.X-(spawnX+1)) > 1e-6 || math.Abs(got.Y-(spawnY+1)) > 1e-6 || math.Abs(got.Z-(spawnZ+1)) > 1e-6 {
		t.Fatalf("restored position = (%v, %v, %v)", got.X, got.Y, got.Z)
	}
	if got.VelX != 0.5 || got.VelY != -0.25 || got.VelZ != 0.125 {
		t.Fatalf("restored velocity = (%v, %v, %v)", got.VelX, got.VelY, got.VelZ)
	}
	if got.Damage != 4 || got.AgeTicks != 37 {
		t.Fatalf("restored damage/age = %v/%d", got.Damage, got.AgeTicks)
	}
	if got.ID <= 0 {
		t.Fatalf("restored arrow has invalid entity ID %d", got.ID)
	}
}
