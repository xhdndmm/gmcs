//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// BenchmarkServerTick 衡量实体 Tick 的成本（32 只生物的移动、避障与广播判定）。
func BenchmarkServerTick(b *testing.B) {
	cfg := config.Default()
	cfg.WorldDir = b.TempDir()
	cfg.SpawnMonsters = false
	instance, err := New(cfg)
	if err != nil {
		b.Fatal(err)
	}
	defer instance.worldFor(world.DimensionOverworld).Close()

	spawnX, spawnY, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	_ = spawnY
	gameWorld := instance.worldFor(world.DimensionOverworld)
	for i := 0; i < 32; i++ {
		x := int(math.Floor(spawnX)) + i%8
		z := int(math.Floor(spawnZ)) + i/8
		groundY, ok := gameWorld.GroundY(x, z)
		if !ok {
			b.Fatal("no ground at spawn area")
		}
		instance.addMob(world.DimensionOverworld, mobZombie, float64(x)+0.5, groundY, float64(z)+0.5)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		instance.tick()
	}
	b.StopTimer()

	instance.entityMu.Lock()
	remaining := len(instance.mobs)
	instance.entityMu.Unlock()
	if remaining != 32 {
		b.Fatalf("剩余生物 %d，期望 32（基准可能未在测量真实路径）", remaining)
	}
}

// BenchmarkItemTick 衡量掉落物 Tick 的成本（128 个掉落物的物理、合并
// 扫描与拾取判定；场景中无玩家，走不拾取分支）。
func BenchmarkItemTick(b *testing.B) {
	cfg := config.Default()
	cfg.WorldDir = b.TempDir()
	cfg.SpawnMonsters = false
	instance, err := New(cfg)
	if err != nil {
		b.Fatal(err)
	}
	defer instance.worldFor(world.DimensionOverworld).Close()

	stack, err := item.FromName("minecraft:stone", 1)
	if err != nil {
		b.Fatal(err)
	}
	spawnX, _, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	gameWorld := instance.worldFor(world.DimensionOverworld)
	for i := 0; i < 128; i++ {
		x := float64(int(math.Floor(spawnX))+i%8*2) + 0.5
		z := float64(int(math.Floor(spawnZ))+i/8) + 0.5
		groundY, ok := gameWorld.GroundY(int(x), int(z))
		if !ok {
			b.Fatal("no ground at spawn area")
		}
		e := instance.spawnItem(world.DimensionOverworld, stack, x, groundY+1, z, 0, 0, 0, itemPickupDelayMining)
		if e == nil {
			b.Fatal("spawnItem returned nil")
		}
		// 基准期间不允许到期消失（迭代次数可能远超 6000 tick）。
		e.AgeTicks = -1 << 30
	}

	instance.entityMu.Lock()
	spawned := len(instance.items)
	instance.entityMu.Unlock()
	if spawned != 128 {
		b.Fatalf("掉落物 %d，期望 128", spawned)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		instance.tick()
	}
	b.StopTimer()

	instance.entityMu.Lock()
	remaining := len(instance.items)
	instance.entityMu.Unlock()
	if remaining != 128 {
		b.Fatalf("剩余掉落物 %d，期望 128（基准可能未在测量真实路径）", remaining)
	}
}

// BenchmarkEntityInsideBlocks 衡量“实体进入方块”（压力板检测）扫描的成本：
// 每次迭代对 32 个生物包围盒各扫描一次。场景为普通地表（无压力板），
// 覆盖区块解析、按列批量读取与状态数组过滤的常见路径。
func BenchmarkEntityInsideBlocks(b *testing.B) {
	cfg := config.Default()
	cfg.WorldDir = b.TempDir()
	cfg.SpawnMonsters = false
	instance, err := New(cfg)
	if err != nil {
		b.Fatal(err)
	}
	defer instance.worldFor(world.DimensionOverworld).Close()

	spawnX, _, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	gameWorld := instance.worldFor(world.DimensionOverworld)
	boxes := make([]world.Box, 0, 32)
	for i := 0; i < 32; i++ {
		x := int(math.Floor(spawnX)) + i%8
		z := int(math.Floor(spawnZ)) + i/8
		groundY, ok := gameWorld.GroundY(x, z)
		if !ok {
			b.Fatal("no ground at spawn area")
		}
		boxes = append(boxes, mobBox(float64(x)+0.5, float64(groundY), float64(z)+0.5))
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for _, box := range boxes {
			instance.entityInsideBlocks(gameWorld, world.DimensionOverworld, box)
		}
	}
}

// BenchmarkHopperTick 衡量漏斗 Tick 的成本：64 个漏斗（每个下方有箱子、
// 内含 64 个物品）持续搬运，覆盖冷却递减、搬运与写回方块实体。
//
// 注意：漏斗搬运若干轮后会排空、箱满后停止搬运，若不在基准内补充物品，
// 绝大多数迭代只会测到“空闲漏斗”路径；这里每 512 tick 重置一次物品与箱子，
// 使冷却/搬运/写回路径在测量区间内保持活跃（重置开销按比例摊薄）。
func BenchmarkHopperTick(b *testing.B) {
	cfg := config.Default()
	cfg.WorldDir = b.TempDir()
	cfg.SpawnMonsters = false
	instance, err := New(cfg)
	if err != nil {
		b.Fatal(err)
	}
	defer instance.worldFor(world.DimensionOverworld).Close()

	spawnX, _, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	baseX, baseZ := int(math.Floor(spawnX))+8, int(math.Floor(spawnZ))+8
	stone, err := item.FromName("minecraft:stone", 64)
	if err != nil {
		b.Fatal(err)
	}
	gameWorld := instance.worldFor(world.DimensionOverworld)
	hoppers := make([]*hopperState, 0, 64)
	positions := make([][3]int, 0, 64)
	chestTypeID, ok := registry.StaticEntryID("minecraft:block_entity_type", "minecraft:chest")
	if !ok {
		b.Fatal("缺少箱子方块实体类型")
	}
	for i := 0; i < 64; i++ {
		x, z := baseX+i%8*2, baseZ+i/8*2
		var groundY int
		if y, ok := gameWorld.GroundY(x, z); ok {
			groundY = int(math.Floor(y))
		} else {
			b.Fatal("no ground at spawn area")
		}
		if state, ok := registry.BlockStateIDs["minecraft:chest"]; ok {
			gameWorld.SetBlock(x, groundY-1, z, state)
		}
		hopperState, ok := registry.BlockStateWithProps("minecraft:hopper", map[string]string{"facing": "down", "enabled": "true"})
		if !ok {
			b.Fatal("hopper state not found")
		}
		gameWorld.SetBlock(x, groundY, z, hopperState)
		state := instance.registerHopper(world.DimensionOverworld, x, groundY, z)
		if state == nil {
			b.Fatal("registerHopper failed")
		}
		setHopperSlot(state, 0, stone)
		hoppers = append(hoppers, state)
		positions = append(positions, [3]int{x, groundY, z})
	}

	// resetItems 把每个漏斗重新装满，并清空下方箱子（避免箱满后停止搬运）。
	resetItems := func() {
		emptyItems := make([]world.ContainerItem, 27)
		for i, state := range hoppers {
			setHopperSlot(state, 0, stone)
			if err := gameWorld.SetBlockEntity(positions[i][0], positions[i][1]-1, positions[i][2],
				world.BlockEntity{TypeID: chestTypeID, Items: emptyItems}); err != nil {
				b.Fatal(err)
			}
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		instance.tickHoppers()
		if i%512 == 511 {
			resetItems()
		}
	}
	b.StopTimer()

	instance.hopperMu.Lock()
	registered := len(instance.hoppers)
	instance.hopperMu.Unlock()
	if registered != 64 {
		b.Fatalf("注册漏斗 %d，期望 64（基准可能未在测量真实路径）", registered)
	}
}

// BenchmarkHopperScan 衡量漏斗全量扫描（600 tick 一次的兜底路径）的成本：
// 64 个已注册漏斗 + 64 个箱子，每次迭代扫描全部已加载区块的漏斗方块状态。
func BenchmarkHopperScan(b *testing.B) {
	cfg := config.Default()
	cfg.WorldDir = b.TempDir()
	cfg.SpawnMonsters = false
	instance, err := New(cfg)
	if err != nil {
		b.Fatal(err)
	}
	defer instance.worldFor(world.DimensionOverworld).Close()

	spawnX, _, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	baseX, baseZ := int(math.Floor(spawnX))+8, int(math.Floor(spawnZ))+8
	gameWorld := instance.worldFor(world.DimensionOverworld)
	hopperState, ok := registry.BlockStateWithProps("minecraft:hopper", map[string]string{"facing": "down", "enabled": "true"})
	if !ok {
		b.Fatal("hopper state not found")
	}
	for i := 0; i < 64; i++ {
		x, z := baseX+i%8*2, baseZ+i/8*2
		y, ok := gameWorld.GroundY(x, z)
		if !ok {
			b.Fatal("no ground at spawn area")
		}
		gameWorld.SetBlock(x, int(math.Floor(y))+1, z, hopperState)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		instance.scanHoppers()
	}
	b.StopTimer()

	instance.hopperMu.Lock()
	registered := len(instance.hoppers)
	instance.hopperMu.Unlock()
	if registered != 64 {
		b.Fatalf("扫描注册 %d，期望 64（基准可能未在测量真实路径）", registered)
	}
}

// BenchmarkHopperChunkScan 衡量漏斗的增量注册路径（区块加载时触发）：
// 单个区块（内含 16 个漏斗）的方块状态扫描 + 逐个注册，成本与新增区块数成正比。
func BenchmarkHopperChunkScan(b *testing.B) {
	cfg := config.Default()
	cfg.WorldDir = b.TempDir()
	cfg.SpawnMonsters = false
	instance, err := New(cfg)
	if err != nil {
		b.Fatal(err)
	}
	defer instance.worldFor(world.DimensionOverworld).Close()

	spawnX, _, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	baseX, baseZ := int(math.Floor(spawnX))+8, int(math.Floor(spawnZ))+8
	gameWorld := instance.worldFor(world.DimensionOverworld)
	hopperState, ok := registry.BlockStateWithProps("minecraft:hopper", map[string]string{"facing": "down", "enabled": "true"})
	if !ok {
		b.Fatal("hopper state not found")
	}
	var (
		cx, cz int
		placed int
	)
	for i := 0; i < 16; i++ {
		x, z := baseX+i%4*2, baseZ+i/4*2
		y, ok := gameWorld.GroundY(x, z)
		if !ok {
			b.Fatal("no ground at spawn area")
		}
		if !gameWorld.SetBlock(x, int(math.Floor(y))+1, z, hopperState) {
			b.Fatal("SetBlock failed")
		}
		cx = x >> 4
		cz = z >> 4
		placed++
	}
	if placed != 16 {
		b.Fatalf("放置 %d 个漏斗，期望 16", placed)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for _, pos := range gameWorld.ChunkBlockStatePositions(cx, cz, isHopperState) {
			instance.registerHopper(world.DimensionOverworld, pos[0], pos[1], pos[2])
		}
	}
	b.StopTimer()

	instance.hopperMu.Lock()
	registered := len(instance.hoppers)
	instance.hopperMu.Unlock()
	if registered != 16 {
		b.Fatalf("增量注册 %d，期望 16（基准可能未在测量真实路径）", registered)
	}
}

// BenchmarkItemMergeScan 衡量物品合并扫描的成本：256 个堆叠按 0.5 格间距
// 一字排开、石头/泥土交替（同类型相距 1.0 格，超出 0.5 的合并半径，因此
// 种群稳定、不发生合并），覆盖“密集物品但无可合并对象”的持续负载——
// 每 4 tick 一轮合并扫描（旧实现为逐物品全表 O(n²) 扫描，新实现为
// 空间索引重建 + 逐物品邻桶扫描）。
func BenchmarkItemMergeScan(b *testing.B) {
	cfg := config.Default()
	cfg.WorldDir = b.TempDir()
	cfg.SpawnMonsters = false
	instance, err := New(cfg)
	if err != nil {
		b.Fatal(err)
	}
	defer instance.worldFor(world.DimensionOverworld).Close()

	stone, err := item.FromName("minecraft:stone", 1)
	if err != nil {
		b.Fatal(err)
	}
	dirt, err := item.FromName("minecraft:dirt", 1)
	if err != nil {
		b.Fatal(err)
	}
	spawnX, _, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	gameWorld := instance.worldFor(world.DimensionOverworld)
	for i := 0; i < 256; i++ {
		x := spawnX + float64(i)*0.5
		z := spawnZ
		groundY, ok := gameWorld.GroundY(int(math.Floor(x)), int(math.Floor(z)))
		if !ok {
			b.Fatal("no ground at spawn area")
		}
		stack := stone
		if i%2 == 1 {
			stack = dirt
		}
		e := instance.spawnItem(world.DimensionOverworld, stack, x, groundY+1, z, 0, 0, 0, itemPickupDelayMining)
		if e == nil {
			b.Fatal("spawnItem returned nil")
		}
		e.AgeTicks = -1 << 30 // 基准期间不允许到期消失
	}

	instance.entityMu.Lock()
	spawned := len(instance.items)
	instance.entityMu.Unlock()
	if spawned != 256 {
		b.Fatalf("掉落物 %d，期望 256", spawned)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		instance.tick()
	}
	b.StopTimer()

	instance.entityMu.Lock()
	remaining := len(instance.items)
	instance.entityMu.Unlock()
	if remaining != 256 {
		b.Fatalf("剩余掉落物 %d，期望 256（基准可能未在测量真实路径）", remaining)
	}
}
