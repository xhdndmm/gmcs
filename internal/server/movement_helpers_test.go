package server

import (
	"math"
	"net"
	"testing"
)

// sendGroundMove 把一个位于地面上的合法位置发送给服务器
// （y 取该列最高固体方块的上方，避免触发穿墙校验）。
func sendGroundMove(t *testing.T, conn net.Conn, instance *Server, x, z float64) {
	t.Helper()
	column, ok := instance.world.ColumnAt(int(math.Floor(x)), int(math.Floor(z)))
	if !ok || !column.HasSolid {
		t.Fatalf("(%f,%f) 没有可站立的地面", x, z)
	}
	sendPlayerPosition(t, conn, x, float64(column.SolidY+1), z, true)
}

// walkTo 以不超过 maxWalkStep 的步长沿直线走向目标点（默认 8 格/步，
// 低于逐 tick 速度上限 10 格），每步都贴合地面高度。
func walkTo(t *testing.T, conn net.Conn, instance *Server, targetX, targetZ float64) {
	t.Helper()
	player := firstJoinedSession(t, instance)
	startX, _, startZ, _, _ := player.playerPosition()
	distance := math.Hypot(targetX-startX, targetZ-startZ)
	steps := int(distance/8) + 1
	for i := 1; i <= steps; i++ {
		fraction := float64(i) / float64(steps)
		x := startX + (targetX-startX)*fraction
		z := startZ + (targetZ-startZ)*fraction
		sendGroundMove(t, conn, instance, x, z)
	}
}

// firstJoinedSession 返回第一个已进入世界的会话（单玩家测试用）。
func firstJoinedSession(t *testing.T, instance *Server) *session {
	t.Helper()
	instance.mu.Lock()
	defer instance.mu.Unlock()
	for _, player := range instance.players {
		if player.isJoined() {
			return player
		}
	}
	t.Fatal("没有已进入世界的会话")
	return nil
}
