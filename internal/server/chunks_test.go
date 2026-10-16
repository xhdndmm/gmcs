//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"
	"sync"
	"testing"
	"time"

	"gmcs/internal/config"
	"gmcs/internal/protocol"

	"gmcs/internal/world"
)

// TestChunkStreamingOnMovement 验证玩家跨越区块边界后服务器增量发送新区块
// （Set Center Chunk → Chunk Data）并卸载超出视距的旧区块（Forget Level Chunk）。
func TestChunkStreamingOnMovement(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Walker")
	_, _, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)

	// 移动到区块 (10, 0)：距原中心 10 > 视距 2+2，区块 (0,0) 等应被卸载。
	// 逐 tick 速度上限为 10 格/包，这里按 8 格一步分步移动。
	walkTo(t, conn, instance, 10*16+0.5, spawnZ)

	expectPlayPacket(t, conn, protocol.PlayPacketIDSetCenterChunk)
	expectPlayPacket(t, conn, protocol.PlayPacketIDChunkData)
	unload := expectPlayPacket(t, conn, protocol.PlayPacketIDForgetLevelChunk)
	// Forget Level Chunk 的字段顺序为 Z、X。
	_, offset, err := protocol.DecodeVarInt(unload)
	if err != nil {
		t.Fatal(err)
	}
	z, offset, err := protocol.DecodeInt32(unload, offset)
	if err != nil {
		t.Fatal(err)
	}
	x, _, err := protocol.DecodeInt32(unload, offset)
	if err != nil {
		t.Fatal(err)
	}
	if absInt(int(x-10)) <= 4 && absInt(int(z)) <= 4 {
		t.Fatalf("unloaded chunk (%d,%d) is still within the view limit", x, z)
	}
}

// TestChunkUnloadFarFromPlayers 验证远离所有玩家的区块会从世界内存缓存中卸载
// （长时间跑图后内存占用保持有界），玩家附近的区块保留，
// 且被卸载的区块可以正常重新加载。
func TestChunkUnloadFarFromPlayers(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Explorer")

	nearCount := instance.testWorld().ChunkCount()
	if nearCount == 0 {
		t.Fatal("玩家附近应已加载区块")
	}

	// 模拟跑图：加载一组远离出生点的区块（如玩家绕了一圈后离开）。
	for i := 0; i < 500; i++ {
		if _, err := instance.testWorld().Chunk(100+i%50, 100+i/50); err != nil {
			t.Fatal(err)
		}
	}
	if got := instance.testWorld().ChunkCount(); got != nearCount+500 {
		t.Fatalf("加载跑图区块后 ChunkCount() = %d, want %d", got, nearCount+500)
	}

	if err := instance.unloadFarChunks(); err != nil {
		t.Fatal(err)
	}
	if got := instance.testWorld().ChunkCount(); got != nearCount {
		t.Fatalf("卸载后 ChunkCount() = %d, want %d（跑图区块应被移出内存）", got, nearCount)
	}

	// 被卸载的区块在再次访问时应能从磁盘恢复。
	chunk, err := instance.testWorld().Chunk(100, 100)
	if err != nil {
		t.Fatal(err)
	}
	if chunk == nil {
		t.Fatal("重新加载被卸载的区块返回 nil")
	}
}

// TestChunkUnloadEvictsPacketCache 验证区块卸载时同步驱逐区块帧缓存：
// 帧缓存条目持有 *world.Chunk 指针，若不驱逐会阻止已卸载区块被 GC，
// 长时间跑图后内存占用只增不减。回归背景：区块卸载只清 world 缓存，
// 帧缓存仍然强引用旧区块。
func TestChunkUnloadEvictsPacketCache(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "CacheWalker")
	gameWorld := instance.testWorld()
	dim := world.DimensionOverworld

	// 构造一个远离玩家的区块并填充帧缓存。
	pos := world.ChunkPos{X: 100, Z: 100}
	chunk, err := gameWorld.Chunk(pos.X, pos.Z)
	if err != nil {
		t.Fatal(err)
	}
	if frame := instance.chunkPacket(dim, pos, chunk); frame == nil {
		t.Fatal("chunkPacket 返回 nil")
	}
	key := chunkPacketKey{dim: dim, pos: pos}
	instance.chunkPackets.mu.Lock()
	_, cached := instance.chunkPackets.entries[key]
	instance.chunkPackets.mu.Unlock()
	if !cached {
		t.Fatal("帧缓存缺少刚编码的区块条目")
	}

	if err := instance.unloadFarChunks(); err != nil {
		t.Fatal(err)
	}
	instance.chunkPackets.mu.Lock()
	_, cachedAfter := instance.chunkPackets.entries[key]
	instance.chunkPackets.mu.Unlock()
	if cachedAfter {
		t.Fatal("区块卸载后帧缓存仍持有该区块（阻止 GC，内存持续增长）")
	}

	// 世界缓存中该区块也已卸载：再次访问应重新加载（新指针）。
	reloaded, err := gameWorld.Chunk(pos.X, pos.Z)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded == chunk {
		t.Fatal("区块未被卸载（重新访问返回同一指针）")
	}
}

// TestIdleServerReleasesChunks 验证所有玩家退出后，卸载扫描会释放全部维度的
// 区块与区块帧缓存（空闲服务器不保留跑图积攒的内存）。
func TestIdleServerReleasesChunks(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Leaver")
	player := findSession(t, instance, "Leaver")
	gameWorld := instance.testWorld()
	dim := world.DimensionOverworld

	// 额外加载一个区块并做修改，同时填充区块帧缓存。
	pos := world.ChunkPos{X: 50, Z: 50}
	chunk, err := gameWorld.Chunk(pos.X, pos.Z)
	if err != nil {
		t.Fatal(err)
	}
	chunk.SetBlockState(0, 64, 0, world.StoneBlock)
	gameWorld.MarkDirty(chunk)
	if instance.chunkPacket(dim, pos, chunk) == nil {
		t.Fatal("chunkPacket 返回 nil")
	}
	key := chunkPacketKey{dim: dim, pos: pos}
	instance.chunkPackets.mu.Lock()
	_, cached := instance.chunkPackets.entries[key]
	instance.chunkPackets.mu.Unlock()
	if !cached {
		t.Fatal("帧缓存缺少测试区块条目")
	}
	if gameWorld.ChunkCount() == 0 {
		t.Fatal("玩家在线时应已加载区块")
	}

	// 所有玩家退出：卸载循环被唤醒并释放全部区块（轮询等待异步释放完成）。
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	waitPlayerOffline(t, instance, player.uuid)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && gameWorld.ChunkCount() != 0 {
		time.Sleep(2 * time.Millisecond)
	}
	if got := gameWorld.ChunkCount(); got != 0 {
		t.Fatalf("空闲服务器未释放区块：ChunkCount() = %d", got)
	}
	instance.chunkPackets.mu.Lock()
	remaining := len(instance.chunkPackets.entries)
	instance.chunkPackets.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("空闲服务器区块帧缓存剩余 %d 条", remaining)
	}

	// 被释放的修改已写盘：重新加载可读回。
	saved, err := world.LoadChunk(cfg.WorldDir, pos.X, pos.Z)
	if err != nil {
		t.Fatal(err)
	}
	if saved == nil {
		t.Fatal("释放前未把脏区块写入磁盘")
	}
	if got := saved.GetBlockState(0, 64, 0); got != world.StoneBlock {
		t.Fatalf("磁盘上的方块 = %d, want %d", got, world.StoneBlock)
	}
}

// TestChunkUnloadGraceWindow 验证服务器卸载宽限：区块首次判远后宽限期内
// 保留（不卸载），连续超出宽限后才真正释放——玩家在卸载边界来回移动时
// 不会反复加载/卸载/写盘。
func TestChunkUnloadGraceWindow(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Graced")
	// 宽限取值远大于两次扫描之间的间隔（100ms），慢机器（-race）下
	// 调度延迟也不会造成“宽限内被误卸载”的误判。
	instance.chunkUnloadGrace = 400 * time.Millisecond
	gameWorld := instance.testWorld()
	nearCount := gameWorld.ChunkCount()

	// 加载一组远离玩家的区块（模拟跑图后离开了该区域）。
	for i := 0; i < 40; i++ {
		if _, err := gameWorld.Chunk(100+i, 100); err != nil {
			t.Fatal(err)
		}
	}

	// 第一次扫描：区块首次判远，进入宽限期（保留）。
	if err := instance.unloadFarChunks(); err != nil {
		t.Fatal(err)
	}
	if got := gameWorld.ChunkCount(); got != nearCount+40 {
		t.Fatalf("宽限期内 ChunkCount() = %d, want %d", got, nearCount+40)
	}

	// 宽限期内再次扫描：仍保留（计时未超）。
	time.Sleep(100 * time.Millisecond)
	if err := instance.unloadFarChunks(); err != nil {
		t.Fatal(err)
	}
	if got := gameWorld.ChunkCount(); got != nearCount+40 {
		t.Fatalf("宽限期内第二次扫描后 ChunkCount() = %d, want %d", got, nearCount+40)
	}

	// 超过宽限后：远处区块被释放，玩家附近区块保留。
	time.Sleep(450 * time.Millisecond)
	if err := instance.unloadFarChunks(); err != nil {
		t.Fatal(err)
	}
	if got := gameWorld.ChunkCount(); got != nearCount {
		t.Fatalf("超出宽限后 ChunkCount() = %d, want %d", got, nearCount)
	}
}

// TestIdleDimensionReleasedAfterGrace 验证无玩家维度的延迟释放：玩家在
// 其他维度时，空闲维度的全部区块（含脏区块写盘）在宽限到期后被释放，
// 玩家所在维度的区块不受影响。
func TestIdleDimensionReleasedAfterGrace(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "DimensionHopper")
	// 宽限设为 0：单次扫描即视为“空闲超时”，行为等价且测试无需等待。
	instance.dimensionIdleGrace = 0

	nether := instance.worldFor(world.DimensionNether)
	if nether == nil {
		t.Fatal("下界世界不可用")
	}
	for i := 0; i < 3; i++ {
		if _, err := nether.Chunk(i, 0); err != nil {
			t.Fatal(err)
		}
	}
	if got := nether.ChunkCount(); got != 3 {
		t.Fatalf("下界 ChunkCount() = %d, want 3", got)
	}
	overworldCount := instance.testWorld().ChunkCount()
	if overworldCount == 0 {
		t.Fatal("主世界应已加载玩家附近区块")
	}

	// 玩家仍在主世界：下界无玩家 → 空闲释放；主世界区块保留。
	if err := instance.unloadFarChunks(); err != nil {
		t.Fatal(err)
	}
	if got := nether.ChunkCount(); got != 0 {
		t.Fatalf("空闲下界未释放区块：ChunkCount() = %d", got)
	}
	if got := instance.testWorld().ChunkCount(); got != overworldCount {
		t.Fatalf("玩家所在维度区块不应被释放：%d → %d", overworldCount, got)
	}

	// 释放后的下界可重新加载（按需从磁盘恢复）。
	reloaded, err := nether.Chunk(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded == nil {
		t.Fatal("重新加载下界区块返回 nil")
	}
}

// TestChunkPrefetchExtraRing 验证预取范围比发送范围多一圈：进入世界后
// 视距+1 环带的区块已在后台加载（跨区块时发送路径无需等待磁盘/生成器）。
func TestChunkPrefetchExtraRing(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Prefetcher") // joinServer 内部把视距设为 2
	player := findSession(t, instance, "Prefetcher")
	gameWorld := instance.testWorld()

	x, _, z, _, _ := player.playerPosition()
	centerChunkX := int(math.Floor(x)) >> 4
	centerChunkZ := int(math.Floor(z)) >> 4
	// 视距 2，预取半径 3：检查半径 3 的环带区块（视距之外、预取之内）。
	blockX := (centerChunkX + 3) * 16
	blockZ := centerChunkZ * 16

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if gameWorld.ChunkLoaded(blockX, blockZ) {
			return // 预取完成
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("视距+1 环带区块未被预取（跨区块时会发生同步等待）")
}

// TestChunkPacketSingleflight 验证同一区块的并发未命中请求只编码一次：
// 全部调用者领取同一份共享帧（并发进入世界的玩家不会重复编码/压缩）。
func TestChunkPacketSingleflight(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "Stormer")
	gameWorld := instance.testWorld()

	pos := world.ChunkPos{X: 120, Z: 120}
	chunk, err := gameWorld.Chunk(pos.X, pos.Z)
	if err != nil {
		t.Fatal(err)
	}
	dim := world.DimensionOverworld

	const workers = 8
	frames := make([][]byte, workers)
	missesBefore := instance.chunkPackets.misses.Load()
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			frames[index] = instance.chunkPacket(dim, pos, chunk)
		}(i)
	}
	close(start)
	wg.Wait()

	if got := instance.chunkPackets.misses.Load() - missesBefore; got != 1 {
		t.Fatalf("并发请求编码了 %d 次，want 1（singleflight 未合并）", got)
	}
	base := frames[0]
	if len(base) == 0 {
		t.Fatal("chunkPacket 返回空帧")
	}
	for i, frame := range frames {
		if len(frame) == 0 {
			t.Fatalf("调用者 %d 收到空帧", i)
		}
		if &frame[0] != &base[0] {
			t.Fatalf("调用者 %d 未领取共享帧（重复编码/分配）", i)
		}
	}
}
