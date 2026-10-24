//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"
	"sync"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 农业系统：锄头耕地、作物种植、作物随机刻生长、耕地湿度与骨粉催熟。
//
// 原版对照（1.21.11 服务端字节码，映射来自官方 server_mappings）：
//   - HoeItem.useOn（dlj）：可耕作方块表 = 草方块/土径/泥土 → 耕地、
//     砂土 → 泥土、缠根泥土 → 泥土并掉落 1 个垂滴根须；要求点击面不是
//     底面且目标上方是空气；播放 item.hoe.till、消耗 1 点耐久；
//   - CropBlock.randomTick/getGrowthSpeed/performBonemeal（ebp）、
//     NetherWartBlock.randomTick（efg）；
//   - FarmBlock.randomTick（ecs）：4 格内水面 → 湿度 7；否则湿度递减，
//     湿度 0 且上方不是 maintains_farmland 标签方块时变为泥土；
//   - BoneMealItem.useOn/applyBonemeal（dkd）：对可催熟方块先作用于被点击
//     方块、再作用于点击面外侧方块；成功时消耗 1 个骨粉并广播世界事件 1505。
//
// 已知近似（见 docs/TODO.md 已知限制）：
//   - gmcs 无光照引擎，天光近似为“作物上方整列没有方块时记为 15，否则 0”，
//     因此只有露天农田可以种植与生长（火把照明、玻璃温室、树荫下均不生长）；
//   - 无天气，FarmBlock 的 isRainingAt 分支恒为 false；
//   - 随机刻作用于内存中所有已加载区块（原版仅模拟距离内），表现为玩家离开
//     后视距+4 范围内的作物仍会生长到区块卸载宽限结束；
//   - 踩踏农田只对玩家判定（生物不跟踪下落距离）；
//   - 未实现 pitcher_pod（双格作物）、西瓜/南瓜茎结果、可可豆（需丛林原木）。

const (
	// cropRandomTickNumerator/cropRandomTickDenominator 是每个方块每 tick
	// 被随机刻选中的概率。原版每 section（4096 格）每 tick 选 3 个随机位置。
	cropRandomTickNumerator   = 3
	cropRandomTickDenominator = 4096
	// cropGrowthLight 是作物随机刻生长要求的最低光照（原版 getRawBrightness >= 9）。
	cropGrowthLight = 9
	// cropPlaceLight 是作物 canSurvive 要求的最低光照（原版 >= 8）。
	cropPlaceLight = 8
	// farmlandWaterRadius 是耕地判定“附近有水”的水平半径与竖直范围。
	farmlandWaterRadius = 4
	// boneMealGrowthEvent 是骨粉催熟的世界事件（原版 LevelEvent 1505，
	// 数据值为冒出的粒子数 15）。
	boneMealGrowthEvent = 1505
)

// plantableCrops 是物品名 → 可种植的作物方块名（BlockItem 放置出的方块）。
var plantableCrops = map[string]string{
	"minecraft:wheat_seeds":       "minecraft:wheat",
	"minecraft:beetroot_seeds":    "minecraft:beetroots",
	"minecraft:carrot":            "minecraft:carrots",
	"minecraft:potato":            "minecraft:potatoes",
	"minecraft:torchflower_seeds": "minecraft:torchflower_crop",
	"minecraft:nether_wart":       "minecraft:nether_wart",
}

// growableCropBlocks 是参与随机刻生长的作物方块（均受 registry.BlockStateAge
// 支持；nether_wart 使用独立的 1/10 增长规则，其余使用 CropBlock 生长公式）。
var growableCropBlocks = []string{
	"minecraft:wheat",
	"minecraft:beetroots",
	"minecraft:carrots",
	"minecraft:potatoes",
	"minecraft:torchflower_crop",
	"minecraft:nether_wart",
}

// farmlandBlockName 是耕地方块名（原版 1.20.5+ 类名为 FarmBlock）。
const farmlandBlockName = "minecraft:farmland"

// netherWartBlockName 是下界疣方块名。
const netherWartBlockName = "minecraft:nether_wart"

// growableCropStateTable 是按方块状态 ID 索引的“可生长作物”位表。
// 区块加载扫描与随机刻路径会逐方块查询，使用 O(1) 数组而不是名称查找。
var growableCropStateTable = sync.OnceValue(func() []bool {
	maxState := uint16(0)
	ranges := make([]registry.BlockStateRange, 0, len(growableCropBlocks))
	for _, name := range growableCropBlocks {
		defaultState, ok := registry.BlockStateIDs[name]
		if !ok {
			continue
		}
		r, ok := registry.BlockStateRangeOf(defaultState)
		if !ok {
			continue
		}
		ranges = append(ranges, r)
		if r.End > maxState {
			maxState = r.End
		}
	}
	table := make([]bool, int(maxState)+1)
	for _, r := range ranges {
		for state := int(r.Start); state <= int(r.End); state++ {
			table[state] = true
		}
	}
	return table
})

// isGrowableCropState 报告方块状态是否为已实现随机刻生长的作物。
func isGrowableCropState(state uint16) bool {
	table := growableCropStateTable()
	return int(state) < len(table) && table[state]
}

// isFarmlandState 报告方块状态是否为耕地（任意含水量）。
func isFarmlandState(state uint16) bool {
	return registry.IsPropertyBlockState(farmlandBlockName, state)
}

// registerCrop 登记一个作物位置（随机刻生长用）。
func (s *Server) registerCrop(dim world.Dimension, x, y, z int) {
	key := containerKey{dim: dim, x: x, y: y, z: z}
	s.farmMu.Lock()
	if s.crops == nil {
		s.crops = make(map[containerKey]struct{})
	}
	s.crops[key] = struct{}{}
	s.farmMu.Unlock()
}

// unregisterCrop 取消作物登记。
func (s *Server) unregisterCrop(dim world.Dimension, x, y, z int) {
	s.farmMu.Lock()
	delete(s.crops, containerKey{dim: dim, x: x, y: y, z: z})
	s.farmMu.Unlock()
}

// registerFarmland 登记一块耕地（随机刻湿度与退化用）。
func (s *Server) registerFarmland(dim world.Dimension, x, y, z int) {
	key := containerKey{dim: dim, x: x, y: y, z: z}
	s.farmMu.Lock()
	if s.farmlands == nil {
		s.farmlands = make(map[containerKey]struct{})
	}
	s.farmlands[key] = struct{}{}
	s.farmMu.Unlock()
}

// unregisterFarmland 取消耕地登记。
func (s *Server) unregisterFarmland(dim world.Dimension, x, y, z int) {
	s.farmMu.Lock()
	delete(s.farmlands, containerKey{dim: dim, x: x, y: y, z: z})
	s.farmMu.Unlock()
}

// unregisterFarm 取消同一位置上的作物与耕地登记（方块被移除时调用）。
func (s *Server) unregisterFarm(dim world.Dimension, x, y, z int) {
	key := containerKey{dim: dim, x: x, y: y, z: z}
	s.farmMu.Lock()
	delete(s.crops, key)
	delete(s.farmlands, key)
	s.farmMu.Unlock()
}

// dropFarmBlocksInChunks 丢弃已从内存卸载的区块中的作物/耕地登记。
// 重新加载时由 registerLoadedChunkBlocks 的增量扫描重新登记。
func (s *Server) dropFarmBlocksInChunks(dim world.Dimension, unloaded []world.ChunkPos) {
	if len(unloaded) == 0 {
		return
	}
	type chunkKey [2]int
	keys := make(map[chunkKey]struct{}, len(unloaded))
	for _, pos := range unloaded {
		keys[chunkKey{pos.X, pos.Z}] = struct{}{}
	}
	s.farmMu.Lock()
	for key := range s.crops {
		if key.dim != dim {
			continue
		}
		if _, ok := keys[chunkKey{key.x >> 4, key.z >> 4}]; ok {
			delete(s.crops, key)
		}
	}
	for key := range s.farmlands {
		if key.dim != dim {
			continue
		}
		if _, ok := keys[chunkKey{key.x >> 4, key.z >> 4}]; ok {
			delete(s.farmlands, key)
		}
	}
	s.farmMu.Unlock()
}

// tickCrops 推进作物与耕地的随机刻（由主 tick 每帧调用）。
// 每个已登记位置按 3/4096 的概率被选中，与原版“每 section 3 个随机位置”
// 的期望一致。
func (s *Server) tickCrops() {
	s.farmMu.Lock()
	crops := s.appendFarmKeys(&s.farmCropScratch, s.crops)
	farmlands := s.appendFarmKeys(&s.farmFarmlandScratch, s.farmlands)
	s.farmMu.Unlock()
	for _, key := range crops {
		s.tickCropAt(key)
	}
	for _, key := range farmlands {
		s.tickFarmlandAt(key)
	}
}

// appendFarmKeys 把注册表键复制到复用的临时切片（tick 单线程持有，
// 避免每 tick 重新分配）。
func (s *Server) appendFarmKeys(dst *[]containerKey, source map[containerKey]struct{}) []containerKey {
	out := (*dst)[:0]
	for key := range source {
		out = append(out, key)
	}
	*dst = out
	return out
}

// tickCropAt 对单个已登记作物位置执行一次随机刻。
func (s *Server) tickCropAt(key containerKey) {
	w := s.worldFor(key.dim)
	if w == nil || !w.ChunkLoaded(key.x, key.z) {
		s.unregisterCrop(key.dim, key.x, key.y, key.z)
		return
	}
	if s.nextRandom()%cropRandomTickDenominator >= cropRandomTickNumerator {
		return
	}
	state := w.BlockAt(key.x, key.y, key.z)
	if !isGrowableCropState(state) {
		// 已被破坏或替换（玩家收割、活塞推动、流体冲走等）。
		s.unregisterCrop(key.dim, key.x, key.y, key.z)
		return
	}
	var (
		next uint16
		grew bool
	)
	if name, ok := s.blockNameOf(state); ok && name == netherWartBlockName {
		next, grew = s.netherWartRandomTick(state)
	} else {
		next, grew = s.cropRandomTick(w, key.x, key.y, key.z, state)
	}
	if !grew {
		return
	}
	if w.SetBlock(key.x, key.y, key.z, next) {
		s.broadcastBlockUpdate(key.dim, key.x, key.y, key.z, int32(next))
	}
}

// tickFarmlandAt 对单个已登记耕地方块执行一次随机刻（原版 FarmBlock.randomTick）。
func (s *Server) tickFarmlandAt(key containerKey) {
	w := s.worldFor(key.dim)
	if w == nil || !w.ChunkLoaded(key.x, key.z) {
		s.unregisterFarmland(key.dim, key.x, key.y, key.z)
		return
	}
	if s.nextRandom()%cropRandomTickDenominator >= cropRandomTickNumerator {
		return
	}
	state := w.BlockAt(key.x, key.y, key.z)
	moisture, ok := registry.FarmlandMoisture(state)
	if !ok {
		s.unregisterFarmland(key.dim, key.x, key.y, key.z)
		return
	}
	if farmlandNearWater(w, key.x, key.y, key.z) {
		if moisture < 7 {
			if next, ok := registry.FarmlandStateWithMoisture(7); ok &&
				w.SetBlock(key.x, key.y, key.z, next) {
				s.broadcastBlockUpdate(key.dim, key.x, key.y, key.z, int32(next))
			}
		}
		return
	}
	if moisture > 0 {
		if next, ok := registry.FarmlandStateWithMoisture(moisture - 1); ok &&
			w.SetBlock(key.x, key.y, key.z, next) {
			s.broadcastBlockUpdate(key.dim, key.x, key.y, key.z, int32(next))
		}
		return
	}
	// 湿度 0 且上方没有 maintains_farmland 标签方块（作物）时退化为泥土。
	above := w.BlockAt(key.x, key.y+1, key.z)
	if registry.BlockTagContains("minecraft:maintains_farmland", above) {
		return
	}
	dirt, ok := registry.BlockStateIDs["minecraft:dirt"]
	if !ok || !w.SetBlock(key.x, key.y, key.z, dirt) {
		return
	}
	s.broadcastBlockUpdate(key.dim, key.x, key.y, key.z, int32(dirt))
	s.unregisterFarmland(key.dim, key.x, key.y, key.z)
}

// farmlandNearWater 报告耕地 4 格水平半径、y..y+1 范围内是否存在水
// （原版 FarmBlock.isNearWater：检查 FluidTags.WATER）。
func farmlandNearWater(w *world.World, x, y, z int) bool {
	var column [2]uint16
	for dx := -farmlandWaterRadius; dx <= farmlandWaterRadius; dx++ {
		for dz := -farmlandWaterRadius; dz <= farmlandWaterRadius; dz++ {
			n := w.LoadedColumn(x+dx, y, y+1, z+dz, column[:])
			for i := 0; i < n; i++ {
				if registry.IsWaterState(column[i]) {
					return true
				}
			}
		}
	}
	return false
}

// cropLightLevel 返回作物位置的近似光照值：正上方整列没有方块时记为 15，
// 否则记为 0（gmcs 无光照引擎，见文件头已知近似）。
func cropLightLevel(w *world.World, x, y, z int) int {
	column, ok := w.ColumnAt(x, z)
	if !ok {
		return 0
	}
	if column.HasTop && column.TopY > y {
		return 0
	}
	return 15
}

// cropRandomTick 是原版 CropBlock.randomTick 的随机数判定部分：
// 要求光照 >= 9、未成熟，并按 getGrowthSpeed 计算概率（nextInt(25/speed+1) == 0）。
func (s *Server) cropRandomTick(w *world.World, x, y, z int, state uint16) (uint16, bool) {
	age, ok := registry.BlockStateAge(state)
	if !ok {
		return 0, false
	}
	maxAge, ok := registry.BlockStateMaxAge(state)
	if !ok || age >= maxAge {
		return 0, false
	}
	if cropLightLevel(w, x, y, z) < cropGrowthLight {
		return 0, false
	}
	speed := s.cropGrowthSpeed(w, x, y, z, state)
	if speed <= 0 {
		return 0, false
	}
	if s.nextInt(int(float32(25.0)/speed)+1) != 0 {
		return 0, false
	}
	return registry.BlockStateWithAge(state, age+1)
}

// netherWartRandomTick 是原版 NetherWartBlock.randomTick：不看光照，
// 每随机刻以 1/10 概率提升一级年龄。
func (s *Server) netherWartRandomTick(state uint16) (uint16, bool) {
	age, ok := registry.BlockStateAge(state)
	if !ok || age >= 3 {
		return 0, false
	}
	if s.nextInt(10) != 0 {
		return 0, false
	}
	return registry.BlockStateWithAge(state, age+1)
}

// cropGrowthSpeed 移植原版 CropBlock.getGrowthSpeed：
// 以 3×3 耕地含水量求和，并对“行列种植”与“对角相邻”作物减半。
func (s *Server) cropGrowthSpeed(w *world.World, x, y, z int, state uint16) float32 {
	cropName, _ := s.blockNameOf(state)
	speed := float32(1)
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			bonus := float32(0)
			if moisture, ok := registry.FarmlandMoisture(s.loadedBlockAt(w, x+dx, y-1, z+dz)); ok {
				bonus = 1
				if moisture > 0 {
					bonus = 3
				}
			}
			if dx != 0 || dz != 0 {
				bonus /= 4
			}
			speed += bonus
		}
	}
	sameCrop := func(nx, nz int) bool {
		name, ok := s.blockNameOf(s.loadedBlockAt(w, nx, y, nz))
		return ok && name == cropName
	}
	westEast := sameCrop(x-1, z) || sameCrop(x+1, z)
	northSouth := sameCrop(x, z-1) || sameCrop(x, z+1)
	if westEast && northSouth {
		speed /= 2
	} else if sameCrop(x-1, z-1) || sameCrop(x+1, z-1) ||
		sameCrop(x+1, z+1) || sameCrop(x-1, z+1) {
		speed /= 2
	}
	return speed
}

// loadedBlockAt 读取已加载区块中的方块状态；区块未加载时返回空气（不触发
// 加载或地形生成，避免随机刻把已卸载的邻区块重新生成）。
func (s *Server) loadedBlockAt(w *world.World, x, y, z int) uint16 {
	if !w.ChunkLoaded(x, z) {
		return world.AirBlock
	}
	return w.BlockAt(x, y, z)
}

// tryFarmUse 处理农业相关的物品使用（锄头/种子/骨粉）。
// 返回 true 表示交互已被处理（与原版“物品 useOn 先于方块交互”一致）。
// 仅由会话读循环调用。
func (s *Server) tryFarmUse(player *session, w *world.World, use protocol.UseItemOn, current uint16) bool {
	held := player.inventory.Get(player.selectedHotbarSlot())
	if held.IsEmpty() {
		return false
	}
	name, ok := registry.ItemName(held.ItemID)
	if !ok {
		return false
	}
	if blockName, ok := plantableCrops[name]; ok {
		return s.tryPlantCrop(player, w, use, blockName)
	}
	if name == "minecraft:bone_meal" {
		return s.tryBoneMeal(player, w, use, current)
	}
	if isHoeItem(name) {
		return s.tryTillSoil(player, w, use, current)
	}
	return false
}

// isHoeItem 报告物品名是否为锄头（原版 Items 中的 7 种锄头）。
func isHoeItem(name string) bool {
	switch name {
	case "minecraft:wooden_hoe", "minecraft:stone_hoe", "minecraft:copper_hoe",
		"minecraft:iron_hoe", "minecraft:golden_hoe", "minecraft:diamond_hoe",
		"minecraft:netherite_hoe":
		return true
	}
	return false
}

// tryTillSoil 处理锄头耕地（原版 HoeItem.useOn）。
func (s *Server) tryTillSoil(player *session, w *world.World, use protocol.UseItemOn, current uint16) bool {
	if use.Direction == 0 { // 不能点击方块底面
		return false
	}
	if w.BlockAt(use.X, use.Y+1, use.Z) != world.AirBlock {
		return false
	}
	name, ok := s.blockNameOf(current)
	if !ok {
		return false
	}
	dim := player.dimensionID()
	var (
		nextState uint16
		dropRoots bool
	)
	switch name {
	case "minecraft:grass_block", "minecraft:dirt", "minecraft:dirt_path":
		var ok bool
		nextState, ok = registry.BlockStateIDs[farmlandBlockName]
		if !ok {
			return false
		}
		s.registerFarmland(dim, use.X, use.Y, use.Z)
	case "minecraft:coarse_dirt":
		var ok bool
		nextState, ok = registry.BlockStateIDs["minecraft:dirt"]
		if !ok {
			return false
		}
	case "minecraft:rooted_dirt":
		var ok bool
		nextState, ok = registry.BlockStateIDs["minecraft:dirt"]
		if !ok {
			return false
		}
		dropRoots = true
	default:
		return false
	}
	if !w.SetBlock(use.X, use.Y, use.Z, nextState) {
		return false
	}
	s.broadcastBlockUpdate(dim, use.X, use.Y, use.Z, int32(nextState))
	s.updateRedstoneAround(dim, use.X, use.Y, use.Z)
	s.updateFluidsAround(dim, use.X, use.Y, use.Z)
	if dropRoots {
		if roots, err := item.FromName("minecraft:hanging_roots", 1); err == nil {
			s.spawnItem(dim, roots,
				float64(use.X)+0.5, float64(use.Y)+1, float64(use.Z)+0.5,
				0, 0, 0, 10)
		}
	}
	s.playFarmSound(dim, use.X, use.Y, use.Z, s.soundHoeTill, 1, 1)
	player.applyToolDamage(1)
	return true
}

// tryTrampleFarmland 实现原版 FarmBlock.fallOn 的玩家踩踏分支：
// 落到耕地上时，若 fallDistance 满足随机判定则退化为泥土。
// gmcs 的生物不跟踪下落距离，因此只有玩家会踩踏农田（见 docs/TODO.md）。
func (s *Server) tryTrampleFarmland(dim world.Dimension, w *world.World, surface uint16, topY, x, z, fall float64) {
	if _, ok := registry.FarmlandMoisture(surface); !ok {
		return
	}
	if fall <= 0.5 {
		return
	}
	if randUnit(s) >= fall-0.5 {
		return
	}
	dirt, ok := registry.BlockStateIDs["minecraft:dirt"]
	if !ok {
		return
	}
	bx, by, bz := int(math.Floor(x)), int(math.Floor(topY)), int(math.Floor(z))
	if !w.SetBlock(bx, by, bz, dirt) {
		return
	}
	s.broadcastBlockUpdate(dim, bx, by, bz, int32(dirt))
	s.unregisterFarm(dim, bx, by, bz)
}

// tryPlantCrop 处理种子/作物种植（原版 BlockItem.place + CropBlock.canSurvive）。
func (s *Server) tryPlantCrop(player *session, w *world.World, use protocol.UseItemOn, blockName string) bool {
	offset := blockFaceOffsets[use.Direction]
	x, y, z := use.X+offset[0], use.Y+offset[1], use.Z+offset[2]
	if _, ok := world.SectionIndex(y); !ok {
		return false
	}
	if w.BlockAt(x, y, z) != world.AirBlock {
		return false
	}
	belowName, ok := s.blockNameOf(w.BlockAt(x, y-1, z))
	if !ok {
		return false
	}
	if blockName == netherWartBlockName {
		// 下界疣只能种在灵魂沙上。
		if belowName != "minecraft:soul_sand" {
			return false
		}
	} else {
		// 其余作物要求下方是耕地。
		if belowName != farmlandBlockName {
			return false
		}
	}
	state, ok := registry.BlockStateIDs[blockName]
	if !ok {
		return false
	}
	// 原版 CropBlock.canSurvive 要求光照 >= 8（下界疣无此限制）。
	if blockName != netherWartBlockName && cropLightLevel(w, x, y, z) < cropPlaceLight {
		return false
	}
	if !w.SetBlock(x, y, z, state) {
		return false
	}
	s.broadcastBlockUpdate(player.dimensionID(), x, y, z, int32(state))
	s.registerCrop(player.dimensionID(), x, y, z)
	s.playFarmSound(player.dimensionID(), x, y, z, s.soundCropPlant, 1, 0.8)
	s.consumeHeldItem(player)
	return true
}

// tryBoneMeal 处理骨粉催熟（原版 BoneMealItem.useOn：先试被点击方块，
// 再试点击面外侧方块；成功时消耗 1 个骨粉并广播世界事件 1505）。
func (s *Server) tryBoneMeal(player *session, w *world.World, use protocol.UseItemOn, current uint16) bool {
	dim := player.dimensionID()
	if s.applyBoneMeal(w, dim, [3]int{use.X, use.Y, use.Z}, current) {
		s.consumeHeldItem(player)
		return true
	}
	offset := blockFaceOffsets[use.Direction]
	target := [3]int{use.X + offset[0], use.Y + offset[1], use.Z + offset[2]}
	if _, ok := world.SectionIndex(target[1]); !ok {
		return false
	}
	// 原版第二分支要求被点击方块可被替换（例如点击空气）。
	if current != world.AirBlock && !isWaterState(current) && !isLavaState(current) {
		return false
	}
	current = w.BlockAt(target[0], target[1], target[2])
	if s.applyBoneMeal(w, dim, target, current) {
		s.consumeHeldItem(player)
		return true
	}
	return false
}

// applyBoneMeal 对一个方块应用骨粉；返回是否成功（是否消耗骨粉）。
// 仅实现作物（CropBlock）：原版 NetherWartBlock 不是 BonemealableBlock。
func (s *Server) applyBoneMeal(w *world.World, dim world.Dimension, pos [3]int, state uint16) bool {
	name, ok := s.blockNameOf(state)
	if !ok || name == netherWartBlockName || !isGrowableCropState(state) {
		return false
	}
	age, ok := registry.BlockStateAge(state)
	if !ok {
		return false
	}
	maxAge, ok := registry.BlockStateMaxAge(state)
	if !ok || age >= maxAge {
		return false // isValidBonemealTarget = !isMaxAge
	}
	newAge := age + 2 + s.nextInt(4) // Mth.nextInt(random, 2, 5)
	if newAge > maxAge {
		newAge = maxAge
	}
	next, ok := registry.BlockStateWithAge(state, newAge)
	if !ok {
		return false
	}
	if w.SetBlock(pos[0], pos[1], pos[2], next) {
		s.broadcastBlockUpdate(dim, pos[0], pos[1], pos[2], int32(next))
	}
	s.broadcastWorldEvent(dim, pos, boneMealGrowthEvent, 15)
	s.playFarmSound(dim, pos[0], pos[1], pos[2], s.soundBoneMealUse, 1, 1)
	return true
}

// consumeHeldItem 消耗选中的 1 个物品并同步槽位（创造模式不消耗）。
func (s *Server) consumeHeldItem(player *session) {
	if player.gameModeID() == uint8(config.GameModeCreative) {
		return
	}
	slot := player.selectedHotbarSlot()
	stack := player.inventory.Get(slot)
	if stack.IsEmpty() {
		return
	}
	stack.Count--
	if stack.Count <= 0 {
		stack = item.Empty()
	}
	player.inventory.Set(slot, stack)
	player.tryWrite(protocol.EncodeSetPlayerInventory(int32(slot), stack.AppendSlot(nil)))
}

// playFarmSound 向附近玩家播放方块农业音效；音效 ID 缺失时静默跳过。
func (s *Server) playFarmSound(dim world.Dimension, x, y, z int, soundID int32, volume, pitch float32) {
	if soundID == 0 {
		return
	}
	packet := protocol.EncodeSoundEffect(soundID, protocol.SoundCategoryBlock,
		float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, volume, pitch, 0)
	s.writeToNearbyPlayers(packet, dim, float64(x), float64(z), nil)
}
