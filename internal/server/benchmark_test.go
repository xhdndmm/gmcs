//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"encoding/binary"
	"io"
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

// BenchmarkServerTickManyPlayers 衡量 100 名玩家 + 32 只生物时的一次
// Server.tick()：覆盖生物索敌/广播筛选（每 mob × 每玩家）与玩家环境扫描
// 在并发玩家规模下的成本。轻量会话直接注入在线列表（writer 丢弃，
// 不经过网络）；这个场景用于防止玩家状态读取路径退化为独占锁实现。
func BenchmarkServerTickManyPlayers(b *testing.B) {
	cfg := config.Default()
	cfg.WorldDir = b.TempDir()
	cfg.SpawnMonsters = false
	instance, err := New(cfg)
	if err != nil {
		b.Fatal(err)
	}
	defer instance.worldFor(world.DimensionOverworld).Close()

	spawnX, spawnY, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
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
	// 100 名轻量玩家围绕出生点铺开（部分位于生物广播半径内）。
	instance.mu.Lock()
	for i := 0; i < 100; i++ {
		sess := &session{
			server:   instance,
			entityID: instance.entityIDs.Add(1),
			health:   maxPlayerHealth,
			food:     maxPlayerFood,
			writer:   io.Discard,
		}
		sess.pose.Store(&playerPose{
			dim: world.DimensionOverworld,
			x:   spawnX + float64(i%10),
			y:   spawnY,
			z:   spawnZ + float64(i/10),
		})
		sess.markJoined()
		var id [16]byte
		binary.LittleEndian.PutUint64(id[:8], uint64(i)+1)
		binary.LittleEndian.PutUint64(id[8:], 0x4242424242424242)
		instance.players[id] = sess
	}
	instance.publishPlayersSnapshotLocked()
	instance.mu.Unlock()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		instance.tick()
	}
	b.StopTimer()
}

// BenchmarkPlayerEquipmentSnapshot 衡量装备快照刷新的成本（会话读循环每收到
// 一个数据包刷新一次，用于 Set Equipment 变更检测）。
func BenchmarkPlayerEquipmentSnapshot(b *testing.B) {
	sess := &session{}
	sess.inventory.Set(item.SlotHotbarStart, item.Stack{ItemID: 936, Count: 1})
	sess.inventory.Set(item.SlotHead, item.Stack{ItemID: 970, Count: 1})
	b.ReportAllocs()
	for b.Loop() {
		sess.refreshEquipmentSnapshot()
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

// BenchmarkEntityTracking 衡量实体跟踪 Tick 的成本：1 名玩家 + 128 个掉落物
// + 16 只生物，全部在 64 格内（稳态：除首帧下发外无新增/移除）。
func BenchmarkEntityTracking(b *testing.B) {
	cfg := config.Default()
	cfg.WorldDir = b.TempDir()
	cfg.SpawnMonsters = false
	instance, err := New(cfg)
	if err != nil {
		b.Fatal(err)
	}
	defer instance.worldFor(world.DimensionOverworld).Close()

	spawnX, spawnY, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	gameWorld := instance.worldFor(world.DimensionOverworld)
	stack, err := item.FromName("minecraft:stone", 1)
	if err != nil {
		b.Fatal(err)
	}
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
		e.AgeTicks = -1 << 30
	}
	for i := 0; i < 16; i++ {
		x := int(math.Floor(spawnX)) + i%4
		z := int(math.Floor(spawnZ)) + i/4
		groundY, ok := gameWorld.GroundY(x, z)
		if !ok {
			b.Fatal("no ground at spawn area")
		}
		instance.addMob(world.DimensionOverworld, mobZombie, float64(x)+0.5, groundY, float64(z)+0.5)
	}
	// 一名已加入的测试玩家（跟踪集的归属者）；写入丢弃（首帧登记会产生
	// Add Entity 包，基准无需真实连接）。
	sess := &session{
		server:   instance,
		entityID: instance.entityIDs.Add(1),
		health:   maxPlayerHealth,
		food:     maxPlayerFood,
		writer:   io.Discard,
	}
	sess.pose.Store(&playerPose{dim: world.DimensionOverworld})
	sess.markJoined()
	sess.setPlayerPosition(spawnX, spawnY, spawnZ, 0, 0)
	players := []*session{sess}

	instance.tickEntityTracking(players) // 首帧：把 64 格内的实体登记进跟踪集。
	instance.entityMu.Lock()
	initial := len(instance.entityTrack[sess].ids)
	instance.entityMu.Unlock()
	if initial == 0 {
		b.Fatal("首帧未登记任何实体，基准未覆盖真实路径")
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		instance.tickEntityTracking(players)
	}
	b.StopTimer()

	instance.entityMu.Lock()
	remaining := len(instance.entityTrack[sess].ids)
	instance.entityMu.Unlock()
	if remaining != initial {
		b.Fatalf("跟踪集大小从 %d 变为 %d（不再处于稳态）", initial, remaining)
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

// BenchmarkFireTick 衡量一次火焰推进（fireTickBody）的成本：燃烧判定 6 次
// （每次一次方块状态读取 + 随机数）与 3×3×6 蔓延候选（每次最多 6 次邻域读取）。
// 布局为石质地面上的火 + 一块相邻橡木木板（保证火焰不自行熄灭且蔓延循环
// 可以命中目标）；每次迭代前重置火焰与木板以保持被测量的工作量稳定。
// 注意：基准直接调用 fireTickBody，跳过 tickFire 的区块加载与玩家半径门控
// （无玩家时门控会直接返回，测不到推进成本）。
func BenchmarkFireTick(b *testing.B) {
	cfg := config.Default()
	cfg.WorldDir = b.TempDir()
	cfg.SpawnMonsters = false
	instance, err := New(cfg)
	if err != nil {
		b.Fatal(err)
	}
	defer instance.worldFor(world.DimensionOverworld).Close()

	spawnX, _, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	baseX, baseZ := int(math.Floor(spawnX))+16, int(math.Floor(spawnZ))+16
	gameWorld := instance.worldFor(world.DimensionOverworld)
	groundY, ok := gameWorld.GroundY(baseX, baseZ)
	if !ok {
		b.Fatal("出生点附近没有地面")
	}
	planks, err := registry.BlockStateID("minecraft:oak_planks")
	if err != nil {
		b.Fatal(err)
	}
	fire, ok := fireStateForAge(fireBlockName, 7)
	if !ok {
		b.Fatal("缺少火焰状态")
	}
	pos := [3]int{baseX, int(groundY), baseZ}
	planksPos := [3]int{baseX + 1, int(groundY), baseZ}
	props := map[string]string{"age": "7"}
	gameWorld.SetBlock(planksPos[0], planksPos[1], planksPos[2], planks)
	if !instance.fireCanSurvive(gameWorld, fireBlockName, pos) || !instance.fireNearFuel(gameWorld, pos) {
		b.Fatal("基准布局未让火焰处于“有燃料”状态，无法测到完整推进路径")
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		gameWorld.SetBlock(pos[0], pos[1], pos[2], fire)
		gameWorld.SetBlock(planksPos[0], planksPos[1], planksPos[2], planks)
		instance.fireTickBody(gameWorld, world.DimensionOverworld, pos, props)
	}
}

// BenchmarkItemWaterFlowPush 衡量掉落物水流推动的计算成本
// （原版 Entity.updateFluidHeightAndDoFluidPushing 的水分支：遍历碰撞盒
// 覆盖方块的 FluidState 与 FlowingFluid.getFlow）。布局为一条 2×2 宽的上坡
// 水道（源 → 流动水），覆盖“多方块求平均”的常见情况。
func BenchmarkItemWaterFlowPush(b *testing.B) {
	cfg := config.Default()
	cfg.WorldDir = b.TempDir()
	cfg.SpawnMonsters = false
	instance, err := New(cfg)
	if err != nil {
		b.Fatal(err)
	}
	defer instance.worldFor(world.DimensionOverworld).Close()

	spawnX, _, spawnZ := instance.spawnPositionFor(world.DimensionOverworld)
	baseX, baseZ := int(math.Floor(spawnX))+16, int(math.Floor(spawnZ))+16
	gameWorld := instance.worldFor(world.DimensionOverworld)
	groundY, ok := gameWorld.GroundY(baseX, baseZ)
	if !ok {
		b.Fatal("出生点附近没有地面")
	}
	channelY := int(groundY) - 1
	source, ok := fluidSourceState(waterBlockName)
	if !ok {
		b.Fatal("缺少水源状态")
	}
	// 源在 -x 端，-x 侧为高水位，向 +x 逐格降低（形成持续坡度）。
	gameWorld.SetBlock(baseX, channelY, baseZ, source)
	for dx := 1; dx <= 8; dx++ {
		amount := 8 - dx
		if amount < 1 {
			amount = 1
		}
		state, ok := fluidFlowingState(waterBlockName, amount)
		if !ok {
			b.Fatal("缺少流动水状态")
		}
		gameWorld.SetBlock(baseX+dx, channelY, baseZ, state)
		gameWorld.SetBlock(baseX+dx, channelY, baseZ+1, state)
	}
	box := itemBoxAt(float64(baseX+3)+0.5, float64(channelY)+0.1, float64(baseZ)+0.5)
	vx, vy, vz := instance.itemWaterFlowPush(gameWorld, box, 0, 0)
	if vx == 0 && vy == 0 && vz == 0 {
		b.Fatal("基准布局未产生水流推动（测不到真实路径）")
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		instance.itemWaterFlowPush(gameWorld, box, 0.01, 0.01)
	}
}
