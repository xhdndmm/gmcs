//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
	"gmcs/internal/world"
)

func TestJoinSendsChangeDifficulty(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.Difficulty = "hard"
	cfg.DifficultyLocked = true
	var gotDifficulty byte
	var gotLocked bool
	var seen bool
	instance, _ := joinServerObserved(t, cfg, "DifficultyJoin", func(id int32, payload []byte) {
		if id != protocol.PlayPacketIDChangeDifficulty {
			return
		}
		_, offset, err := protocol.DecodeVarInt(payload)
		if err != nil || offset+2 > len(payload) {
			t.Fatalf("invalid change difficulty payload: %x", payload)
		}
		gotDifficulty, gotLocked, seen = payload[offset], payload[offset+1] != 0, true
	})
	if !seen || gotDifficulty != byte(config.DifficultyHard) || !gotLocked {
		t.Fatalf("difficulty packet = seen=%v difficulty=%d locked=%v", seen, gotDifficulty, gotLocked)
	}
	if instance.difficultyID() != byte(config.DifficultyHard) || !instance.difficultyLocked() {
		t.Fatalf("server difficulty = %d/%v", instance.difficultyID(), instance.difficultyLocked())
	}
}

func TestPeacefulRemovesHostileMobs(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.Difficulty = "peaceful"
	instance, conn := joinServer(t, cfg, "PeacefulHostiles")
	defer conn.Close()
	player := findSession(t, instance, "PeacefulHostiles")
	x, y, z, _, _ := player.playerPosition()
	m := instance.addMob(world.DimensionOverworld, mobZombie, x, y, z)
	if m == nil {
		t.Fatal("could not add zombie")
	}
	instance.tick()
	instance.entityMu.Lock()
	_, exists := instance.mobs[m.ID]
	instance.entityMu.Unlock()
	if exists {
		t.Fatal("peaceful did not remove hostile mob")
	}
}

func TestHungerDifficultyRules(t *testing.T) {
	cases := []struct {
		name       string
		difficulty config.Difficulty
		health     float32
		wantHealth float32
	}{
		{"peaceful heals", config.DifficultyPeaceful, 10, 11},
		{"easy does not starve", config.DifficultyEasy, 10, 10},
		{"normal starves above ten", config.DifficultyNormal, 11, 10},
		{"normal starves at ten", config.DifficultyNormal, 10, 9},
		{"hard starves above one", config.DifficultyHard, 2, 1},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.WorldDir = t.TempDir()
			cfg.SpawnMonsters = false
			cfg.DifficultyLocked = false
			name := "Hunger" + string(rune('A'+i))
			instance, conn := joinServer(t, cfg, name)
			defer conn.Close()
			player := findSession(t, instance, name)
			instance.setDifficulty(tc.difficulty, false)
			player.stateMu.Lock()
			player.health = tc.health
			player.food = 0
			player.saturation = 0
			player.foodTimer = playerFoodTickInterval - 1
			player.stateMu.Unlock()
			player.applyHungerTick()
			health, _, _ := player.healthStatus()
			if health != tc.wantHealth {
				t.Fatalf("health = %v, want %v", health, tc.wantHealth)
			}
		})
	}
}
