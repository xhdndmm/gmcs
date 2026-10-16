//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import "gmcs/internal/world"

// tickScratch 是单线程 tick 复用的临时切片集合：避免每帧为生物/掉落物/
// 经验球的移动与结算重新分配临时切片。
//
// tick 由 tickLoop 单 goroutine 驱动，或由测试串行调用；各 tick 函数在
// 入口截断自己的缓冲（[:0]），处理完成后把结果切片写回复用（容量保留），
// 下一帧继续复用同一批底层数组。
type tickScratch struct {
	// 生物 Tick。
	attacks    []pendingAttack
	arrows     []pendingArrow
	priming    []pendingPriming
	explosions []pendingExplosion
	moves      []pendingMove
	heads      []pendingMove
	removals   []int32
	insides    []insideCheck
	envs       []mobEnvironment

	// 掉落物 Tick。
	itemMoves    []pendingPacket
	itemRemovals []int32
	itemUpdates  [][]byte
	itemPickups  []itemPickup
	itemInsides  []insideCheck
	// itemBuckets 是掉落物合并的空间索引（分桶桶格 2×2，仅在合并帧重建，
	// 键 = (维度, floor(x/2), floor(z/2))；底层切片跨帧复用）。
	itemBuckets map[uint64][]*itemEntity

	// 经验球 Tick。
	orbMoves    []pendingPacket
	orbRemovals []int32
	orbPickups  []xpOrbPickup
	orbInsides  []insideCheck
	orbGains    []xpGain
	// orbBuckets 是经验球合并的空间索引（同 itemBuckets）。
	orbBuckets map[uint64][]*xpOrb

	// 实体跟踪 Tick（entity_tracking.go）。
	trackAdds    []trackAddOp
	trackRemoves []trackRemoveOp
	trackPlayers []trackedPlayer
	trackMobs    []*mob
	trackItems   []*itemEntity
	trackArrows  []*arrowEntity
	trackTnts    []*primedTnt
	trackOrbs    []*xpOrb
	// trackAlive 是各维度的存活实体索引（id → 水平位置），跨帧复用。
	trackAlive [3]map[int32]trackPos
}

// pendingMove 是一次实体位置/朝向同步（生物移动与头部转向）。
type pendingMove struct {
	id         int32
	dim        world.Dimension
	x, y, z    float64
	yaw, pitch float32
}

// pendingPacket 是一条待广播的实体位置包（掉落物与经验球）。
type pendingPacket struct {
	dim    world.Dimension
	packet []byte
	x, z   float64
}

// insideCheck 是一次“移动后进入方块”的压力板通知。
type insideCheck struct {
	dim world.Dimension
	box world.Box
	w   *world.World
}

// xpGain 是一次按玩家合并后的经验发放。
type xpGain struct {
	player *session
	amount int32
}
