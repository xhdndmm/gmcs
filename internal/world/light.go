//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package world

import (
	"strconv"
	"strings"
	"sync"

	"gmcs/internal/protocol"
	"gmcs/internal/registry"
)

// 光照引擎实现（区块级增量光照 + 网络 Light Data）。
//
// 与原版相比，这里使用 gmcs 紧凑的 nibble 数组与本地/跨区块 BFS：
//   - 天空光：从世界顶部向下按方块光衰减；透明方块不衰减，垂直直射不衰减，
//     水平/向上传播每格至少衰减 1；
//   - 方块光：发光方块作为 BFS 源，按目标方块不透明度衰减；
//   - 方块修改时从旧光照值执行移除 BFS，再从新光源/邻居重新传播；
//   - 光照数组按 section（4096 个值，2048 字节）保存，并生成区块包与
//     Light Update 包所需的边界 section。
//
// 注意：这里不是原版 LightEngine 的逐位复刻。复杂透明形状（台阶、树叶、
// 水生植物等）使用碰撞形状/显式规则近似；原版专用的 light block 数据
// 尚未从官方资源包导入。所有近似均有测试覆盖，且不影响未修改区块的
// 既有行为。

const (
	lightMaxLevel       = 15
	lightSectionValues  = SectionVolume / 2 // 4096 个 nibble
	lightSectionsStored = SectionCount
	lightStateCount     = 1 << 16
)

// chunkLight 是一个区块的天空光/方块光 nibble 数组以及待发送的 section。
// 所有字段由 Chunk.lightMu 保护；数组按 section 连续存放。
type chunkLight struct {
	sky   []byte
	block []byte
	ready bool

	dirtySky   map[int]struct{}
	dirtyBlock map[int]struct{}
}

// lightProperty 是方块状态的光照属性。
type lightProperty struct {
	emission uint8
	opacity  uint8
}

var (
	lightPropsOnce sync.Once
	lightProps     [lightStateCount]lightProperty
)

// lightPropertyOf 返回方块状态的光照属性。表在首次使用时初始化：
// 区块生成/编码热路径不重复查询注册表或字符串。
func lightPropertyOf(state uint16) lightProperty {
	lightPropsOnce.Do(buildLightProperties)
	return lightProps[state]
}

func buildLightProperties() {
	for id := 0; id < lightStateCount; id++ {
		state := uint16(id)
		name, ok := registry.BlockNameByState(state)
		if !ok {
			continue
		}
		var props map[string]string
		if propName, propValues, has := registry.StateProps(state); has {
			name = propName
			props = propValues
		}
		emission := lightEmissionFor(name, props)
		opacity := uint8(0)
		switch {
		case name == "minecraft:lava":
			opacity = lightMaxLevel
		case name == "minecraft:water":
			opacity = 1
		case name == "minecraft:barrier":
			opacity = lightMaxLevel
		case strings.HasSuffix(name, "_leaves"):
			// 原版树叶不是完整遮挡，但会削弱穿过它的光；用 1 近似，
			// 避免树冠下完全过亮或整块变黑。
			opacity = 1
		case name == "minecraft:powder_snow":
			opacity = 1
		case name == "minecraft:glass", name == "minecraft:glass_pane",
			strings.HasSuffix(name, "_stained_glass"),
			strings.HasSuffix(name, "_stained_glass_pane"):
			opacity = 0
		case name == "minecraft:light", name == "minecraft:structure_void",
			name == "minecraft:ice", name == "minecraft:blue_ice",
			name == "minecraft:frosted_ice", name == "minecraft:slime_block",
			name == "minecraft:honey_block":
			// 这些方块有完整/接近完整的碰撞形状，但原版对光照基本透明。
			opacity = 0
		case isFullCube(state):
			opacity = lightMaxLevel
		}
		lightProps[id] = lightProperty{emission: emission, opacity: opacity}
	}
}

// isFullCube 报告方块碰撞形状是否为完整 16×16×16 立方体。原版
// `canOcclude`（getLightBlock=15）只对完整方块成立，不能用“任意碰撞形状”
// 代替，否则台阶/楼梯/栅栏会把光照错误地完全遮住。
func isFullCube(state uint16) bool {
	shape := registry.BlockStateShape(state)
	if len(shape) != 1 {
		return false
	}
	box := shape[0]
	return box == (registry.ShapeBox{X1: 0, Y1: 0, Z1: 0, X2: 16, Y2: 16, Z2: 16})
}

// lightEmissionFor 返回方块状态的发光等级。属性方块（蜡烛、营火、红石灯、
// 红石矿等）根据 lit/berries 等状态区分；其余按方块名查表。
func lightEmissionFor(name string, props map[string]string) uint8 {
	lit := props == nil || props["lit"] != "false"
	switch name {
	case "minecraft:lava", "minecraft:fire", "minecraft:glowstone",
		"minecraft:jack_o_lantern", "minecraft:beacon", "minecraft:sea_lantern",
		"minecraft:end_portal", "minecraft:end_gateway", "minecraft:conduit",
		"minecraft:shroomlight", "minecraft:lantern", "minecraft:ochre_froglight",
		"minecraft:verdant_froglight", "minecraft:pearlescent_froglight",
		"minecraft:lava_cauldron", "minecraft:light":
		return 15
	case "minecraft:soul_fire", "minecraft:soul_lantern", "minecraft:crying_obsidian":
		return 10
	case "minecraft:campfire":
		if lit {
			return 15
		}
		return 0
	case "minecraft:soul_campfire":
		if lit {
			return 10
		}
		return 0
	case "minecraft:torch", "minecraft:wall_torch", "minecraft:copper_torch",
		"minecraft:copper_wall_torch", "minecraft:end_rod":
		return 14
	case "minecraft:soul_torch", "minecraft:soul_wall_torch":
		return 10
	case "minecraft:redstone_torch", "minecraft:redstone_wall_torch",
		"minecraft:enchanting_table", "minecraft:ender_chest", "minecraft:glow_lichen":
		return 7
	case "minecraft:redstone_lamp", "minecraft:copper_bulb", "minecraft:exposed_copper_bulb",
		"minecraft:weathered_copper_bulb", "minecraft:oxidized_copper_bulb",
		"minecraft:waxed_copper_bulb", "minecraft:waxed_exposed_copper_bulb",
		"minecraft:waxed_weathered_copper_bulb", "minecraft:waxed_oxidized_copper_bulb":
		if lit {
			return 15
		}
		return 0
	case "minecraft:furnace", "minecraft:blast_furnace", "minecraft:smoker":
		// 点燃的熔炉族原版发光 13。
		if props == nil || props["lit"] == "true" {
			return 13
		}
		return 0
	case "minecraft:redstone_ore", "minecraft:deepslate_redstone_ore":
		if props == nil || props["lit"] == "true" {
			return 9
		}
		return 0
	case "minecraft:amethyst_cluster":
		return 5
	case "minecraft:large_amethyst_bud":
		return 4
	case "minecraft:medium_amethyst_bud":
		return 2
	case "minecraft:small_amethyst_bud", "minecraft:brown_mushroom",
		"minecraft:brewing_stand", "minecraft:end_portal_frame", "minecraft:dragon_egg",
		"minecraft:sculk_sensor":
		return 1
	case "minecraft:magma_block":
		return 3
	case "minecraft:nether_portal":
		return 11
	case "minecraft:sculk_catalyst":
		return 6
	case "minecraft:calibrated_sculk_sensor":
		return 2
	case "minecraft:sea_pickle":
		count := 1
		if props != nil {
			if value, err := strconv.Atoi(props["pickles"]); err == nil {
				count = value
			}
		}
		level := 3 + 3*count
		if level > 15 {
			level = 15
		}
		return uint8(level)
	case "minecraft:respawn_anchor":
		charges := 0
		if props != nil {
			if value, err := strconv.Atoi(props["charges"]); err == nil {
				charges = value
			}
		}
		return uint8(charges * 15 / 4)
	case "minecraft:glow_berries":
		return 14
	case "minecraft:firefly_bush":
		return 2
	case "minecraft:cave_vines", "minecraft:cave_vines_plant":
		// 只有带浆果的状态发光；属性缺失时按原版默认（无浆果）处理。
		if props != nil && props["berries"] == "true" {
			return 14
		}
		return 0
	}
	if strings.HasSuffix(name, "_candle") || strings.HasSuffix(name, "_candle_cake") {
		if lit {
			return 3
		}
		return 0
	}
	return 0
}

// lightIndex 返回方块在 nibble 数组中的字节下标与半字节位置。
func lightIndex(x, y, z int) (int, uint) {
	sectionIndex, ok := SectionIndex(y)
	if !ok {
		return -1, 0
	}
	localY := y - (WorldMinY + sectionIndex*SectionSize)
	value := blockIndex(x, localY, z)
	return sectionIndex*lightSectionValues + value/2, uint(value & 1)
}

func lightAtArray(data []byte, x, y, z int, fallback uint8) uint8 {
	index, half := lightIndex(x, y, z)
	if index < 0 || index >= len(data) {
		return fallback
	}
	if half == 0 {
		return data[index] & 0x0F
	}
	return data[index] >> 4
}

func setLightArray(data []byte, x, y, z int, value uint8) {
	index, half := lightIndex(x, y, z)
	if index < 0 || index >= len(data) {
		return
	}
	value &= 0x0F
	if half == 0 {
		data[index] = data[index]&0xF0 | value
		return
	}
	data[index] = data[index]&0x0F | value<<4
}

// EnsureLight 保证区块拥有当前方块数据对应的光照数组。生成阶段或首次
// 编码时调用一次；之后方块修改走增量更新。
func (c *Chunk) EnsureLight() {
	c.lightOnce.Do(func() {
		c.computeLocalLight()
	})
}

// computeLocalLight 只使用当前区块的方块计算天空光/方块光。跨区块边界
// 的光照会在 World 的运行时更新路径中传播；新区块加载时先保证本地
// 光照正确，避免客户端收到全黑/全亮区块。
func (c *Chunk) computeLocalLight() {
	c.mu.RLock()
	defer c.mu.RUnlock()

	light := &chunkLight{
		sky:        make([]byte, lightSectionsStored*lightSectionValues),
		ready:      true,
		dirtySky:   make(map[int]struct{}),
		dirtyBlock: make(map[int]struct{}),
	}

	// 天空光按列向下计算。透明方块不衰减；不透明方块以下为 0。
	// 下界/末地没有天空光，数组保持全 0。
	skyNeedsSpread := false
	if c.dim.HasSkyLight() {
		for x := 0; x < SectionSize; x++ {
			for z := 0; z < SectionSize; z++ {
				level := uint8(lightMaxLevel)
				for y := WorldMinY + WorldHeight - 1; y >= WorldMinY; y-- {
					state := c.getBlockStateLocked(x, y, z)
					opacity := lightPropertyOf(state).opacity
					if opacity >= lightMaxLevel {
						level = 0
					} else if level <= opacity {
						level = 0
					} else {
						level -= opacity
					}
					setLightArray(light.sky, x, y, z, level)
					if opacity == 0 && level != lightMaxLevel {
						skyNeedsSpread = true
					}
				}
			}
		}
	}

	// 方块光源种子。
	queue := make([]lightCell, 0, 256)
	for y := WorldMinY; y < WorldMinY+WorldHeight; y++ {
		for z := 0; z < SectionSize; z++ {
			for x := 0; x < SectionSize; x++ {
				emission := lightPropertyOf(c.getBlockStateLocked(x, y, z)).emission
				if emission == 0 {
					continue
				}
				if light.block == nil {
					light.block = make([]byte, lightSectionsStored*lightSectionValues)
				}
				setLightArray(light.block, x, y, z, emission)
				queue = append(queue, lightCell{x: x, y: y, z: z, level: emission, sky: false})
			}
		}
	}
	localLightBFS(light.block, queue, c.getBlockStateLocked)

	// 天空光水平传播。平坦/全开阔区块的透明格全部为 15，不需要任何
	// 水平传播；直接跳过可避免每个区块 58 万次邻域检查。
	if skyNeedsSpread {
		queue = queue[:0]
		for y := WorldMinY; y < WorldMinY+WorldHeight; y++ {
			for z := 0; z < SectionSize; z++ {
				for x := 0; x < SectionSize; x++ {
					level := lightAtArray(light.sky, x, y, z, 0)
					if level == 0 {
						continue
					}
					if skyCanImprove(light.sky, c.getBlockStateLocked, x, y, z, level) {
						queue = append(queue, lightCell{x: x, y: y, z: z, level: level, sky: true})
					}
				}
			}
		}
		localLightBFS(light.sky, queue, c.getBlockStateLocked)
	}

	c.lightMu.Lock()
	c.light = light
	c.lightMu.Unlock()
}

type lightCell struct {
	x, y, z int
	level   uint8
	sky     bool
}

var lightDirections = [6][3]int{
	{1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0}, {0, 0, 1}, {0, 0, -1},
}

func lightCost(sky bool, dy int, targetOpacity uint8) uint8 {
	if sky && dy < 0 && targetOpacity == 0 {
		return 0
	}
	return 1 + targetOpacity
}

// skyCanImprove 判断某格当前天空光能否通过六邻域提高另一格。
func skyCanImprove(data []byte, blockAt func(int, int, int) uint16, x, y, z int, level uint8) bool {
	for _, direction := range lightDirections {
		nx, ny, nz := x+direction[0], y+direction[1], z+direction[2]
		if nx < 0 || nx >= SectionSize || nz < 0 || nz >= SectionSize ||
			ny < WorldMinY || ny >= WorldMinY+WorldHeight {
			continue
		}
		// 先比较光照值；开阔天空的邻居通常是 15，直接跳过昂贵的
		// 方块状态/不透明度查询。水平方向至少衰减 1，向下直射才可能
		// 保持同级；同级的向下邻居也不会被提高。
		target := lightAtArray(data, nx, ny, nz, 0)
		if target >= level {
			continue
		}
		opacity := lightPropertyOf(blockAt(nx, ny, nz)).opacity
		cost := lightCost(true, direction[1], opacity)
		if level > cost && level-cost > target {
			return true
		}
	}
	return false
}

func localLightBFS(data []byte, queue []lightCell, blockAt func(int, int, int) uint16) {
	for head := 0; head < len(queue); head++ {
		cell := queue[head]
		for _, direction := range lightDirections {
			nx, ny, nz := cell.x+direction[0], cell.y+direction[1], cell.z+direction[2]
			if nx < 0 || nx >= SectionSize || nz < 0 || nz >= SectionSize ||
				ny < WorldMinY || ny >= WorldMinY+WorldHeight {
				continue
			}
			opacity := lightPropertyOf(blockAt(nx, ny, nz)).opacity
			cost := lightCost(cell.sky, direction[1], opacity)
			if cell.level <= cost {
				continue
			}
			next := cell.level - cost
			if next <= lightAtArray(data, nx, ny, nz, 0) {
				continue
			}
			setLightArray(data, nx, ny, nz, next)
			queue = append(queue, lightCell{x: nx, y: ny, z: nz, level: next, sky: cell.sky})
		}
	}
}

// lightValueLocked 读取区块内某格的光照；未计算时天空光默认为 15，
// 方块光默认为 0（保持旧版“初始全亮”的网络行为）。
func (c *Chunk) lightValueLocked(light *chunkLight, sky bool, x, y, z int) uint8 {
	if light == nil || !light.ready {
		if sky {
			return lightMaxLevel
		}
		return 0
	}
	data := light.block
	if sky {
		data = light.sky
	}
	return lightAtArray(data, x, y, z, 0)
}

// lightAt 返回区块内某格的光照（世界 Y 坐标）。
func (c *Chunk) lightAt(sky bool, x, y, z int) uint8 {
	c.lightMu.RLock()
	defer c.lightMu.RUnlock()
	return c.lightValueLocked(c.light, sky, x, y, z)
}

// setLight 更新区块内某格光照并标记所在 section 待发送。
func (c *Chunk) setLight(sky bool, x, y, z int, value uint8) bool {
	c.lightMu.Lock()
	defer c.lightMu.Unlock()
	if c.light == nil || !c.light.ready {
		return false
	}
	data := c.light.block
	dirty := c.light.dirtyBlock
	if sky {
		data = c.light.sky
		dirty = c.light.dirtySky
	} else if data == nil {
		data = make([]byte, lightSectionsStored*lightSectionValues)
		c.light.block = data
	}
	previous := lightAtArray(data, x, y, z, 0)
	if previous == value {
		return false
	}
	setLightArray(data, x, y, z, value)
	section, ok := SectionIndex(y)
	if ok {
		dirty[section] = struct{}{}
	}
	return true
}

// takeLightDirty 取出待发送的光照 section 集合并清空。返回按 section 排序。
func (c *Chunk) takeLightDirty() (sky, block []int) {
	c.lightMu.Lock()
	defer c.lightMu.Unlock()
	if c.light == nil {
		return nil, nil
	}
	sky = sortedLightSections(c.light.dirtySky)
	block = sortedLightSections(c.light.dirtyBlock)
	c.light.dirtySky = make(map[int]struct{})
	c.light.dirtyBlock = make(map[int]struct{})
	return sky, block
}

func sortedLightSections(set map[int]struct{}) []int {
	if len(set) == 0 {
		return nil
	}
	result := make([]int, 0, len(set))
	for section := range set {
		result = append(result, section)
	}
	sortInts(result)
	return result
}

// appendLightData 把当前区块的完整 Light Data 追加到区块数据包。
// 掩码/数组按协议顺序：Sky Light Mask、Block Light Mask、Empty Sky Mask、
// Empty Block Mask、天空光数组、方块光数组。边界 section（维度最低-1 与
// 最高）由本函数合成：下方全 0，上方天空光全 15。
func (c *Chunk) appendLightData(dst []byte) []byte {
	low, high := c.dim.SectionRange()
	slots := make([]int, 0, high-low+2)
	for slot := low - 1; slot <= high; slot++ {
		slots = append(slots, slot)
	}
	skyMask, emptySky, skyArrays := c.collectLight(true, slots)
	blockMask, emptyBlock, blockArrays := c.collectLight(false, slots)
	return appendLightFields(dst, skyMask, blockMask, emptySky, emptyBlock, skyArrays, blockArrays)
}

// appendLightUpdatePacket 把待发送的光照 section 编码为 Light Update 包。
// 没有待发送 section 时返回 nil。
func (c *Chunk) appendLightUpdatePacket(dst []byte) []byte {
	skySections, blockSections := c.takeLightDirty()
	if len(skySections) == 0 && len(blockSections) == 0 {
		return nil
	}
	skyMask, emptySky, skyArrays := c.collectLight(true, skySections)
	blockMask, emptyBlock, blockArrays := c.collectLight(false, blockSections)
	dst = protocol.AppendVarInt(dst, protocol.PlayPacketIDLightUpdate)
	dst = protocol.AppendVarInt(dst, int32(c.X))
	dst = protocol.AppendVarInt(dst, int32(c.Z))
	return appendLightFields(dst, skyMask, blockMask, emptySky, emptyBlock, skyArrays, blockArrays)
}

// collectLight 收集指定 section 的掩码和数组。slots 是绝对 section 下标
// （边界为 low-1/high）。内部数组会复制，避免网络编码与 BFS 并发写冲突。
func (c *Chunk) collectLight(sky bool, slots []int) (mask, empty uint64, arrays [][]byte) {
	c.lightMu.RLock()
	defer c.lightMu.RUnlock()
	low, high := c.dim.SectionRange()
	for _, slot := range slots {
		bit := slot - low + 1
		if bit < 0 || bit >= 64 {
			continue
		}
		data := c.lightSectionLocked(sky, slot, low, high)
		if allZeroLight(data) {
			empty |= uint64(1) << uint(bit)
			continue
		}
		mask |= uint64(1) << uint(bit)
		arrays = append(arrays, data)
	}
	return mask, empty, arrays
}

// lightSectionLocked 返回指定绝对 section 的 nibble 数组。边界 section 返回
// 共享的零数组/全亮天空光数组；内部 section 返回副本。
func (c *Chunk) lightSectionLocked(sky bool, slot, low, high int) []byte {
	if slot < low {
		return zeroLightSection
	}
	if slot >= high {
		if sky && c.dim.HasSkyLight() {
			return fullBrightSkyLight
		}
		return zeroLightSection
	}
	if c.light == nil || !c.light.ready {
		if sky && c.dim.HasSkyLight() {
			return fullBrightSkyLight
		}
		return zeroLightSection
	}
	source := c.light.block
	if sky {
		source = c.light.sky
	}
	start := slot * lightSectionValues
	if source == nil || start < 0 || start+lightSectionValues > len(source) {
		return zeroLightSection
	}
	result := make([]byte, lightSectionValues)
	copy(result, source[start:start+lightSectionValues])
	return result
}

var zeroLightSection = make([]byte, lightSectionValues)

func allZeroLight(data []byte) bool {
	for _, value := range data {
		if value != 0 {
			return false
		}
	}
	return true
}

func appendLightFields(dst []byte, skyMask, blockMask, emptySky, emptyBlock uint64, skyArrays, blockArrays [][]byte) []byte {
	dst = protocol.AppendVarInt(dst, 1)
	dst = protocol.AppendInt64(dst, int64(skyMask))
	dst = protocol.AppendVarInt(dst, 1)
	dst = protocol.AppendInt64(dst, int64(blockMask))
	dst = protocol.AppendVarInt(dst, 1)
	dst = protocol.AppendInt64(dst, int64(emptySky))
	dst = protocol.AppendVarInt(dst, 1)
	dst = protocol.AppendInt64(dst, int64(emptyBlock))
	dst = protocol.AppendVarInt(dst, int32(len(skyArrays)))
	for _, data := range skyArrays {
		dst = protocol.AppendVarInt(dst, int32(len(data)))
		dst = append(dst, data...)
	}
	dst = protocol.AppendVarInt(dst, int32(len(blockArrays)))
	for _, data := range blockArrays {
		dst = protocol.AppendVarInt(dst, int32(len(data)))
		dst = append(dst, data...)
	}
	return dst
}

// lightUpdater 在已加载区块之间执行增量光照移除/传播。
// 区块指针从 World.chunks 快照读取；区块被卸载时指针仍然有效，写入一个
// 已卸载区块的光照数组不会影响世界数据，下一次重新加载会重新计算。
type lightUpdater struct {
	w       *World
	current ChunkPos
	chunk   *Chunk
	queue   []lightCellWorld
	touched map[*Chunk]struct{}
}

type lightCellWorld struct {
	x, y, z int
	level   uint8
	sky     bool
}

func (w *World) updateLight(x, y, z int, oldState, newState uint16) {
	if oldState == newState {
		return
	}
	oldProps := lightPropertyOf(oldState)
	newProps := lightPropertyOf(newState)
	if oldProps.opacity == newProps.opacity && oldProps.emission == newProps.emission {
		// 红石/门/活板门等状态变化通常不改变遮光与发光属性；没有
		// 光照影响时不能进入 BFS（否则高频红石更新会产生大量无谓工作）。
		return
	}
	opacityChanged := oldProps.opacity != newProps.opacity
	emissionChanged := oldProps.emission != newProps.emission
	pos := ChunkPos{X: floorDiv(x, SectionSize), Z: floorDiv(z, SectionSize)}
	w.mu.RLock()
	chunk := w.chunks[pos]
	w.mu.RUnlock()
	if chunk == nil {
		return
	}
	chunk.EnsureLight()

	updater := &lightUpdater{
		w:       w,
		current: pos,
		chunk:   chunk,
		touched: make(map[*Chunk]struct{}),
	}
	for _, sky := range []bool{true, false} {
		if sky && !opacityChanged {
			continue // 只有发光属性变化时不会改变天空光
		}
		if !sky && !opacityChanged && !emissionChanged {
			continue
		}
		oldLevel := chunk.lightAt(sky, floorMod(x, SectionSize), y, floorMod(z, SectionSize))
		if oldLevel > 0 {
			updater.remove(x, y, z, oldLevel, sky)
		}
		if !sky {
			if emission := newProps.emission; emission > 0 {
				updater.seed(x, y, z, emission, false)
			}
		}
		// 移除 BFS 已经把边界重新入队；这里再播种六个邻居，覆盖“方块
		// 变得更透明后邻居光进入本格”的情况。
		for _, direction := range lightDirections {
			nx, ny, nz := x+direction[0], y+direction[1], z+direction[2]
			if level := updater.lightAt(nx, ny, nz, sky); level > 0 {
				updater.seed(nx, ny, nz, level, sky)
			}
		}
	}
	updater.flush()
	for touched := range updater.touched {
		touched.touchLightRevision()
	}
}

func (u *lightUpdater) chunkAt(x, z int) *Chunk {
	pos := ChunkPos{X: floorDiv(x, SectionSize), Z: floorDiv(z, SectionSize)}
	if u.chunk != nil && pos == u.current {
		return u.chunk
	}
	u.w.mu.RLock()
	chunk := u.w.chunks[pos]
	u.w.mu.RUnlock()
	u.current, u.chunk = pos, chunk
	if chunk != nil {
		chunk.EnsureLight()
	}
	return chunk
}

func (u *lightUpdater) blockAt(x, y, z int) uint16 {
	chunk := u.chunkAt(x, z)
	if chunk == nil {
		return AirBlock
	}
	return chunk.GetBlockState(floorMod(x, SectionSize), y, floorMod(z, SectionSize))
}

func (u *lightUpdater) lightAt(x, y, z int, sky bool) uint8 {
	chunk := u.chunkAt(x, z)
	if chunk == nil {
		return 0
	}
	return chunk.lightAt(sky, floorMod(x, SectionSize), y, floorMod(z, SectionSize))
}

func (u *lightUpdater) setLight(x, y, z int, level uint8, sky bool) bool {
	chunk := u.chunkAt(x, z)
	if chunk == nil {
		return false
	}
	if !chunk.setLight(sky, floorMod(x, SectionSize), y, floorMod(z, SectionSize), level) {
		return false
	}
	u.touched[chunk] = struct{}{}
	return true
}

// seed 即使目标当前已经有相同光照也入队，用于移除后从边界重新传播。
func (u *lightUpdater) seed(x, y, z int, level uint8, sky bool) {
	if level == 0 || u.chunkAt(x, z) == nil {
		return
	}
	if level > u.lightAt(x, y, z, sky) {
		u.setLight(x, y, z, level, sky)
	}
	u.queue = append(u.queue, lightCellWorld{x: x, y: y, z: z, level: level, sky: sky})
}

func (u *lightUpdater) remove(x, y, z int, level uint8, sky bool) {
	stack := []lightCellWorld{{x: x, y: y, z: z, level: level, sky: sky}}
	for len(stack) > 0 {
		cell := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if u.lightAt(cell.x, cell.y, cell.z, sky) == 0 {
			continue
		}
		u.setLight(cell.x, cell.y, cell.z, 0, sky)
		for _, direction := range lightDirections {
			nx, ny, nz := cell.x+direction[0], cell.y+direction[1], cell.z+direction[2]
			if ny < WorldMinY || ny >= WorldMinY+WorldHeight {
				continue
			}
			neighbor := u.lightAt(nx, ny, nz, sky)
			if neighbor == 0 {
				continue
			}
			opacity := lightPropertyOf(u.blockAt(nx, ny, nz)).opacity
			remove := neighbor < cell.level
			if sky && direction[1] < 0 && opacity == 0 && neighbor == cell.level {
				// 天空光向下穿过透明方块时不衰减，移除时必须继续清空
				// 下方同值的光，否则放置屋顶后地下仍保持全亮。
				remove = true
			}
			if remove {
				stack = append(stack, lightCellWorld{x: nx, y: ny, z: nz, level: neighbor, sky: sky})
			} else {
				u.seed(nx, ny, nz, neighbor, sky)
			}
		}
	}
}

func (u *lightUpdater) flush() {
	for head := 0; head < len(u.queue); head++ {
		cell := u.queue[head]
		for _, direction := range lightDirections {
			nx, ny, nz := cell.x+direction[0], cell.y+direction[1], cell.z+direction[2]
			if ny < WorldMinY || ny >= WorldMinY+WorldHeight {
				continue
			}
			opacity := lightPropertyOf(u.blockAt(nx, ny, nz)).opacity
			cost := lightCost(cell.sky, direction[1], opacity)
			if cell.level <= cost {
				continue
			}
			next := cell.level - cost
			if next <= u.lightAt(nx, ny, nz, cell.sky) {
				continue
			}
			if !u.setLight(nx, ny, nz, next, cell.sky) {
				continue
			}
			u.queue = append(u.queue, lightCellWorld{x: nx, y: ny, z: nz, level: next, sky: cell.sky})
		}
	}
}

// touchLightRevision 递增区块 revision，使共享区块包缓存失效。光照本身
// 通过 Light Update 下发，但重新编码区块时仍需读取新的 nibble 数组。
func (c *Chunk) touchLightRevision() {
	c.mu.Lock()
	c.revision++
	c.mu.Unlock()
}

// seedChunkBoundaryLight 在区块加载后与已加载的四邻域交换边界光照。
// 新加载区块的本地光照不知道邻区块的火把/天空光；只对“边界两侧光照不同”
// 的格子播种，平坦地形两边都是天空光 15 时不会产生队列。
func (w *World) seedChunkBoundaryLight(chunk *Chunk) {
	if chunk == nil {
		return
	}
	chunk.EnsureLight()
	pos := ChunkPos{X: chunk.X, Z: chunk.Z}
	w.mu.RLock()
	neighbors := [4]*Chunk{
		w.chunks[ChunkPos{X: pos.X - 1, Z: pos.Z}],
		w.chunks[ChunkPos{X: pos.X + 1, Z: pos.Z}],
		w.chunks[ChunkPos{X: pos.X, Z: pos.Z - 1}],
		w.chunks[ChunkPos{X: pos.X, Z: pos.Z + 1}],
	}
	w.mu.RUnlock()
	if neighbors[0] == nil && neighbors[1] == nil && neighbors[2] == nil && neighbors[3] == nil {
		return
	}
	updater := &lightUpdater{
		w:       w,
		current: pos,
		chunk:   chunk,
		touched: make(map[*Chunk]struct{}),
	}
	for index, neighbor := range neighbors {
		if neighbor == nil {
			continue
		}
		neighbor.EnsureLight()
		for y := WorldMinY; y < WorldMinY+WorldHeight; y++ {
			for along := 0; along < SectionSize; along++ {
				var ownX, ownZ, neighborX, neighborZ int
				switch index {
				case 0: // west
					ownX, ownZ, neighborX, neighborZ = 0, along, SectionSize-1, along
				case 1: // east
					ownX, ownZ, neighborX, neighborZ = SectionSize-1, along, 0, along
				case 2: // north
					ownX, ownZ, neighborX, neighborZ = along, 0, along, SectionSize-1
				default: // south
					ownX, ownZ, neighborX, neighborZ = along, SectionSize-1, along, 0
				}
				ownWorldX, ownWorldZ := chunk.X*SectionSize+ownX, chunk.Z*SectionSize+ownZ
				neighborWorldX := neighbor.X*SectionSize + neighborX
				neighborWorldZ := neighbor.Z*SectionSize + neighborZ
				for _, sky := range []bool{true, false} {
					ownLevel := chunk.lightAt(sky, ownX, y, ownZ)
					neighborLevel := neighbor.lightAt(sky, neighborX, y, neighborZ)
					switch {
					case ownLevel > neighborLevel:
						updater.seed(ownWorldX, y, ownWorldZ, ownLevel, sky)
					case neighborLevel > ownLevel:
						updater.seed(neighborWorldX, y, neighborWorldZ, neighborLevel, sky)
					}
				}
			}
		}
	}
	updater.flush()
	for touched := range updater.touched {
		touched.touchLightRevision()
	}
}

// EncodeLightUpdatePacket 编码指定区块待发送的 Light Update 包；没有变化
// 时返回 nil。区块坐标使用世界坐标右移 4（负数同样向下取整）。
func (w *World) EncodeLightUpdatePacket(chunkX, chunkZ int) []byte {
	w.mu.RLock()
	chunk := w.chunks[ChunkPos{X: chunkX, Z: chunkZ}]
	w.mu.RUnlock()
	if chunk == nil {
		return nil
	}
	chunk.EnsureLight()
	return chunk.appendLightUpdatePacket(nil)
}

// EncodeLightUpdatesAround 返回目标区块及其八邻域中所有待发送的 Light Update
// 包。方块光/天空光最多传播 15 格，边界光源最多影响相邻一个区块；把邻域
// 一起编码可避免观察者收到旧光照直到区块重发。
func (w *World) EncodeLightUpdatesAround(x, z int) [][]byte {
	cx, cz := floorDiv(x, SectionSize), floorDiv(z, SectionSize)
	type target struct {
		chunk *Chunk
	}
	targets := make([]target, 0, 9)
	w.mu.RLock()
	for dz := -1; dz <= 1; dz++ {
		for dx := -1; dx <= 1; dx++ {
			pos := ChunkPos{X: cx + dx, Z: cz + dz}
			if chunk := w.chunks[pos]; chunk != nil {
				targets = append(targets, target{chunk: chunk})
			}
		}
	}
	w.mu.RUnlock()
	packets := make([][]byte, 0, len(targets))
	for _, target := range targets {
		if packet := target.chunk.appendLightUpdatePacket(nil); packet != nil {
			packets = append(packets, packet)
		}
	}
	return packets
}

// LightAt 返回世界坐标处的光照值；区块未加载时返回 0，天空光在区块
// 未计算时保持旧版默认 15。供刷怪、光照观测与测试使用。
func (w *World) LightAt(x, y, z int, sky bool) uint8 {
	if _, ok := SectionIndex(y); !ok {
		return 0
	}
	pos := ChunkPos{X: floorDiv(x, SectionSize), Z: floorDiv(z, SectionSize)}
	w.mu.RLock()
	chunk := w.chunks[pos]
	w.mu.RUnlock()
	if chunk == nil {
		return 0
	}
	chunk.EnsureLight()
	return chunk.lightAt(sky, floorMod(x, SectionSize), y, floorMod(z, SectionSize))
}
