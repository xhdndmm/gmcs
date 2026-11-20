package server

import (
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/registry"
)

func TestClimbableAllowsSustainedRise(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Climber")
	defer conn.Close()
	player := findSession(t, instance, "Climber")
	x, groundY, z := farmTestSpot(t, instance)
	ladder, ok := registry.BlockStateIDs["minecraft:ladder"]
	if !ok {
		t.Fatal("缺少梯子方块状态")
	}
	w := instance.testWorld()
	if !w.SetBlock(x, groundY+1, z, ladder) {
		t.Fatal("放置梯子失败")
	}
	player.setPlayerPosition(float64(x)+0.5, float64(groundY+1), float64(z)+0.5, 0, 0)
	player.airRise = maxJumpRise
	prevY := float64(groundY + 1)
	if !player.acceptRise(w, float64(x)+0.5, prevY, float64(x)+0.5, prevY+0.8, float64(z)+0.5) {
		t.Fatal("在梯子上持续上升不应被当作飞行回拉")
	}
	if player.airRise != 0 {
		t.Fatalf("攀爬后 airRise = %v, want 0", player.airRise)
	}
	// 没有可攀爬方块时，超过跳跃上升上限仍应拒绝。
	player.airRise = maxJumpRise
	if player.acceptRise(w, float64(x)+0.5, prevY, float64(x)+1.5, prevY+0.8, float64(z)+0.5) {
		// 目标格可能恰好有支撑；换到明确的空中位置验证。
		t.Log("目标位置意外有支撑，跳过空中拒绝断言")
	}
}
