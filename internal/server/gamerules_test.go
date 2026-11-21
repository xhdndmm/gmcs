//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/registry"
)

func TestGameRuleCommandAndPersistence(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.Ops = []string{"Op"}
	instance, conn := joinServer(t, cfg, "Op")
	defer conn.Close()
	player := findSession(t, instance, "Op")
	instance.handleCommand(player, "/gamerule doDaylightCycle false")
	if instance.rules.doDaylightCycle.Load() {
		t.Fatal("doDaylightCycle was not disabled")
	}
	instance.handleCommand(player, "/gamerule fire_spread_radius_around_player 64")
	if got := instance.rules.fireSpreadRadiusAroundPlayer.Load(); got != 64 {
		t.Fatalf("fire radius = %d, want 64", got)
	}
	instance.handleCommand(player, "/gamerule nope true")
	if _, ok := instance.rules.get("nope"); ok {
		t.Fatal("unknown game rule was accepted")
	}

	content, err := os.ReadFile(filepath.Join(cfg.WorldDir, levelFileName))
	if err != nil {
		t.Fatal(err)
	}
	var state levelState
	if err := json.Unmarshal(content, &state); err != nil {
		t.Fatal(err)
	}
	if state.GameRules == nil {
		t.Fatal("gamerules missing from level.json")
	}
	loaded := &Server{config: cfg}
	loaded.rules.reset()
	if err := loaded.loadLevelState(); err != nil {
		t.Fatal(err)
	}
	if loaded.rules.doDaylightCycle.Load() {
		t.Fatal("doDaylightCycle not restored")
	}
	if got := loaded.rules.fireSpreadRadiusAroundPlayer.Load(); got != 64 {
		t.Fatalf("restored fire radius = %d, want 64", got)
	}
}

func TestDoDaylightCycleStopsWorldAge(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "NoDaylight")
	defer conn.Close()
	instance.rules.doDaylightCycle.Store(false)
	before := instance.worldAge.Load()
	instance.advanceWorldTime()
	if got := instance.worldAge.Load(); got != before {
		t.Fatalf("worldAge advanced from %d to %d with doDaylightCycle=false", before, got)
	}
}

func TestKeepInventoryPreventsDeathDrops(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "KeepInventory")
	defer conn.Close()
	player := findSession(t, instance, "KeepInventory")
	instance.rules.keepInventory.Store(true)
	instance.entityMu.Lock()
	beforeItems := len(instance.items)
	instance.entityMu.Unlock()
	if !instance.damagePlayer(player, maxPlayerHealth, "test", 0, instance.mobAttackDamageTypeID, nil) {
		t.Fatal("lethal damage was not applied")
	}
	instance.entityMu.Lock()
	afterItems := len(instance.items)
	instance.entityMu.Unlock()
	if afterItems != beforeItems {
		t.Fatalf("keepInventory dropped %d items", afterItems-beforeItems)
	}
	if player.inventory.Get(0).IsEmpty() {
		t.Fatal("keepInventory cleared the inventory")
	}
}

func TestMobGriefingPreventsCreeperBlockDamage(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Griefing")
	defer conn.Close()
	player := findSession(t, instance, "Griefing")
	x, y, z, _, _ := player.playerPosition()
	bx, by, bz := int(x)+2, int(y), int(z)
	// 先放一个确定的非空气方块，避免出生点原本是空气。
	stone, ok := registry.BlockStateIDs["minecraft:stone"]
	if !ok {
		t.Fatal("missing stone block state")
	}
	instance.testWorld().SetBlock(bx, by, bz, stone)
	instance.rules.mobGriefing.Store(false)
	instance.explode(player.dimensionID(), float64(bx)+0.5, float64(by)+0.5, float64(bz)+0.5, 1, 1, "Creeper")
	if got := instance.testWorld().BlockAt(bx, by, bz); got != stone {
		t.Fatalf("mobGriefing=false destroyed block: %d", got)
	}
}

func TestGameRuleCompletion(t *testing.T) {
	cfg := config.Default()
	cfg.Ops = []string{"Op"}
	instance := &Server{config: cfg, permissionIndex: buildPermissionIndex(cfg)}
	op := &session{name: "Op"}
	matches, start, length := instance.commandSuggestions(op, "/gamerule doWeatherCycle t")
	if len(matches) != 1 || matches[0] != "true" || start != len("/gamerule doWeatherCycle ") || length != 1 {
		t.Fatalf("gamerule completion = %v [%d,%d)", matches, start, length)
	}
}
