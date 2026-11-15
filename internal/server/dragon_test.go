//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/world"
)

func TestDamageDragonReducesHealthAndDies(t *testing.T) {
	instance := &Server{config: config.Default()}
	dragon := &dragonEntity{ID: 7, Dim: world.DimensionEnd, X: 1, Y: 80, Z: 1, Health: dragonMaxHealth}
	instance.dragon = dragon
	player := &session{server: instance}
	instance.damageDragon(player, dragon, 50)
	if dragon.Health != dragonMaxHealth-50 {
		t.Fatalf("health = %v", dragon.Health)
	}
	instance.damageDragon(player, dragon, dragonMaxHealth)
	if !dragon.Dead || dragon.Health != 0 {
		t.Fatalf("dragon dead=%v health=%v", dragon.Dead, dragon.Health)
	}
}
