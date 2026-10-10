package server

import (
	"math"
	"net"
	"testing"

	"gmcs/internal/world"
)

// sendGroundMove 把一个位于地面上的合法位置发送给服务器
// （y 取生成器地形高度的上方：树木/雪层等装饰不计入，避免触发穿墙校验）。
func sendGroundMove(t *testing.T, conn net.Conn, instance *Server, x, z float64) {
	t.Helper()
	sendPlayerPosition(t, conn, x, float64(instance.testWorld().SurfaceY(int(math.Floor(x)), int(math.Floor(z)))+1), z, true)
}

// walkTo 以小步长沿直线走向目标点（4 格/步，3D 位移远低于逐 tick 速度上限
// 10 格），每步都贴合地形高度（爬坡不触发穿墙/速度校验）。
func walkTo(t *testing.T, conn net.Conn, instance *Server, targetX, targetZ float64) {
	t.Helper()
	player := firstJoinedSession(t, instance)
	startX, _, startZ, _, _ := player.playerPosition()
	distance := math.Hypot(targetX-startX, targetZ-startZ)
	steps := int(distance/4) + 1
	for i := 1; i <= steps; i++ {
		fraction := float64(i) / float64(steps)
		x := startX + (targetX-startX)*fraction
		z := startZ + (targetZ-startZ)*fraction
		sendGroundMove(t, conn, instance, x, z)
	}
}

// buildSpotNearSpawn 返回出生点附近可放置方块的位置（地表之上一格；
// 雪层/草丛等装饰会被放置替换，不作为排除条件），距离玩家较近以满足交互校验。
func buildSpotNearSpawn(t *testing.T, instance *Server) (int, int, int) {
	t.Helper()
	bx, _, bz := instance.spawnPositionFor(world.DimensionOverworld)
	baseX, baseZ := int(math.Floor(bx)), int(math.Floor(bz))
	for _, off := range spawnNearOffsets {
		x, z := baseX+off[0], baseZ+off[1]
		surface := instance.testWorld().SurfaceY(x, z)
		if surface <= world.SeaLevel {
			continue
		}
		return x, surface + 1, z
	}
	t.Fatal("出生点附近找不到可放置方块的位置")
	return 0, 0, 0
}

// digTargetNearSpawn 返回出生点附近可破坏的地表方块坐标（非水面；
// 顶层被挖空的列下探一层，便于重复选取）。
func digTargetNearSpawn(t *testing.T, instance *Server) (int, int, int) {
	t.Helper()
	bx, _, bz := instance.spawnPositionFor(world.DimensionOverworld)
	baseX, baseZ := int(math.Floor(bx)), int(math.Floor(bz))
	for _, off := range spawnNearOffsets {
		x, z := baseX+off[0], baseZ+off[1]
		surface := instance.testWorld().SurfaceY(x, z)
		if surface <= world.SeaLevel {
			continue
		}
		for y := surface; y > surface-3; y-- {
			if state := instance.testWorld().BlockAt(x, y, z); state != world.AirBlock {
				return x, y, z
			}
		}
	}
	t.Fatal("出生点附近找不到可破坏的方块")
	return 0, 0, 0
}

// spawnNearOffsets 是出生点附近的列偏移（由近及远的方形环，含四个方向）。
var spawnNearOffsets = func() [][2]int {
	offsets := make([][2]int, 0, 80)
	for r := 1; r <= 4; r++ {
		for dx := -r; dx <= r; dx++ {
			for dz := -r; dz <= r; dz++ {
				if max(absInt(dx), absInt(dz)) == r {
					offsets = append(offsets, [2]int{dx, dz})
				}
			}
		}
	}
	return offsets
}()

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
