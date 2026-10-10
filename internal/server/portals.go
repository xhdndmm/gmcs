package server

import (
	"math"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 传送门系统：下界传送门（黑曜石框 + 打火石点燃）与末地传送门（末地传送门框 +
// 末影之眼激活），以及玩家进入传送门方块时的维度传送。
//
// 近似实现说明（未与原版逐项对比，见 docs/TODO.md）：
//   - 框架检测为有界矩形穷举（内部宽 2–21、高 3–21），不实现原版 PortalShape
//     的搜索顺序细节；
//   - 目标维度没有已存在的传送门时构建 2×3 传送门与黑曜石平台（布局为简化实现）；
//   - 已存在传送门的搜索半径为水平 16 格、垂直 ±8 格（原版按维度使用更大范围）；
//   - 进入传送门即传送（无原版进入延迟），传送后冷却 300 tick（15 秒）防回环；
//   - 末地出口传送门在主岛中心上方按简化结构生成；末地到达平台固定在 (100, 50, 0)。
//   - 主世界与下界的坐标缩放为 1:8，与原版一致。
const (
	// portalCooldownTicks 是传送后的冷却（近似原版 15 秒）。
	portalCooldownTicks = 300
	// portalSearchRadius 是查找已有下界传送门的水平半径（方块）。
	portalSearchRadius = 16
	// portalSearchHeight 是查找已有下界传送门的垂直范围（方块）。
	portalSearchHeight = 8
	// netherScale 是主世界与下界之间的坐标缩放（原版 1:8）。
	netherScale = 8
	// 传送门内部尺寸范围（与原版一致：2–21 宽、3–21 高）。
	portalMinWidth  = 2
	portalMaxWidth  = 21
	portalMinHeight = 3
	portalMaxHeight = 21

	// endPlatformY 是末地到达平台的脚部高度（与原版平台高度一致：y=50）。
	endPlatformY = 50
)

var (
	flintAndSteelName = "minecraft:flint_and_steel"
	enderEyeName      = "minecraft:ender_eye"
)

// isNetherPortalState 报告方块状态是否为下界传送门方块。
func isNetherPortalState(state uint16) bool {
	name, _, ok := registry.StateProps(state)
	return ok && name == "minecraft:nether_portal"
}

// isEndPortalState 报告方块状态是否为末地传送门方块（无属性，按状态 ID 比较）。
func isEndPortalState(state uint16) bool {
	id, ok := registry.BlockStateIDs["minecraft:end_portal"]
	return ok && state == id
}

// portalBlockState 构造指定朝向的下界传送门方块状态。
// axis "x" 表示门面沿 X 轴展开（宽度沿 X）；"z" 表示沿 Z 轴展开。
func portalBlockState(axis string) (uint16, bool) {
	return registry.BlockStateWithProps("minecraft:nether_portal", map[string]string{"axis": axis})
}

// portalInterior 是检测到的传送门内部区域。
// alongX 为 true 时 h=h1..h2 表示 x 范围（z=fixed）；否则 h 表示 z 范围（x=fixed）。
type portalInterior struct {
	alongX     bool
	h1, h2     int
	y1, y2     int
	fixedCoord int
}

// findPortalInterior 以点击的黑曜石为出发点，穷举包含它的矩形框架，
// 返回第一个合法的内部区域（内部全为空气或已点亮的传送门方块，边框为黑曜石）。
func findPortalInterior(w *world.World, alongX bool, hClick, yClick, fixed int) (portalInterior, bool) {
	block := func(h, y int) uint16 {
		if alongX {
			return w.BlockAt(h, y, fixed)
		}
		return w.BlockAt(fixed, y, h)
	}
	try := func(h1, y1, h2, y2 int) (portalInterior, bool) {
		if h2-h1+1 < portalMinWidth || h2-h1+1 > portalMaxWidth {
			return portalInterior{}, false
		}
		if y2-y1+1 < portalMinHeight || y2-y1+1 > portalMaxHeight {
			return portalInterior{}, false
		}
		// 上下横梁（含四角）。
		for h := h1 - 1; h <= h2+1; h++ {
			if block(h, y1-1) != world.ObsidianBlock || block(h, y2+1) != world.ObsidianBlock {
				return portalInterior{}, false
			}
		}
		// 左右侧柱。
		for y := y1; y <= y2; y++ {
			if block(h1-1, y) != world.ObsidianBlock || block(h2+1, y) != world.ObsidianBlock {
				return portalInterior{}, false
			}
		}
		// 内部：空气或已有的传送门方块（允许重复点燃）。
		for h := h1; h <= h2; h++ {
			for y := y1; y <= y2; y++ {
				state := block(h, y)
				if state != world.AirBlock && !isNetherPortalState(state) {
					return portalInterior{}, false
				}
			}
		}
		return portalInterior{alongX: alongX, h1: h1, h2: h2, y1: y1, y2: y2, fixedCoord: fixed}, true
	}
	// 情况 1：点击在底部横梁（内部从上一格开始）。
	for h1 := hClick - portalMaxWidth + 1; h1 <= hClick+1; h1++ {
		for h2 := hClick; h2 <= h1+portalMaxWidth-1; h2++ {
			if h2-h1+1 < portalMinWidth || h1-1 > hClick || hClick > h2+1 {
				continue
			}
			y1 := yClick + 1
			for height := portalMinHeight; height <= portalMaxHeight; height++ {
				if interior, ok := try(h1, y1, h2, y1+height-1); ok {
					return interior, true
				}
			}
		}
	}
	// 情况 2：点击在顶部横梁（内部到下一格结束）。
	for h1 := hClick - portalMaxWidth + 1; h1 <= hClick+1; h1++ {
		for h2 := hClick; h2 <= h1+portalMaxWidth-1; h2++ {
			if h2-h1+1 < portalMinWidth || h1-1 > hClick || hClick > h2+1 {
				continue
			}
			y2 := yClick - 1
			for height := portalMinHeight; height <= portalMaxHeight; height++ {
				if interior, ok := try(h1, y2-height+1, h2, y2); ok {
					return interior, true
				}
			}
		}
	}
	// 情况 3：点击在低坐标侧柱（内部从坐标 +1 开始）。
	for y1 := yClick - portalMaxHeight; y1 <= yClick+1; y1++ {
		for y2 := yClick; y2 <= y1+portalMaxHeight-1; y2++ {
			if y2-y1+1 < portalMinHeight || y1-1 > yClick || yClick > y2+1 {
				continue
			}
			h1 := hClick + 1
			for width := portalMinWidth; width <= portalMaxWidth; width++ {
				if interior, ok := try(h1, y1, h1+width-1, y2); ok {
					return interior, true
				}
			}
		}
	}
	// 情况 4：点击在高坐标侧柱（内部到坐标 −1 结束）。
	for y1 := yClick - portalMaxHeight; y1 <= yClick+1; y1++ {
		for y2 := yClick; y2 <= y1+portalMaxHeight-1; y2++ {
			if y2-y1+1 < portalMinHeight || y1-1 > yClick || yClick > y2+1 {
				continue
			}
			h2 := hClick - 1
			for width := portalMinWidth; width <= portalMaxWidth; width++ {
				if interior, ok := try(h2-width+1, y1, h2, y2); ok {
					return interior, true
				}
			}
		}
	}
	return portalInterior{}, false
}

// tryUseFlintAndSteel 处理打火石点击黑曜石：点燃下界传送门（仅主世界/下界）。
// 返回 true 表示已处理（调用方跳过常规放置流程）。
func (s *Server) tryUseFlintAndSteel(player *session, w *world.World, x, y, z int, current uint16) bool {
	dim := w.Dimension()
	if dim == world.DimensionEnd || current != world.ObsidianBlock {
		return false
	}
	axis, interior, ok := "", portalInterior{}, false
	for _, alongX := range []bool{true, false} {
		if alongX {
			axis = "x"
		} else {
			axis = "z"
		}
		hClick := xOrZ(alongX, x, z)
		fixed := xOrZ(!alongX, x, z)
		if interior, ok = findPortalInterior(w, alongX, hClick, y, fixed); ok {
			break
		}
	}
	if !ok {
		return false
	}
	state, ok := portalBlockState(axis)
	if !ok {
		return false
	}
	changed := 0
	for h := interior.h1; h <= interior.h2; h++ {
		for yy := interior.y1; yy <= interior.y2; yy++ {
			bx, bz := h, interior.fixedCoord
			if !interior.alongX {
				bx, bz = interior.fixedCoord, h
			}
			if w.SetBlock(bx, yy, bz, state) {
				s.broadcastBlockUpdate(dim, bx, yy, bz, int32(state))
				changed++
			}
		}
	}
	if changed == 0 {
		return false
	}
	s.broadcastToNearby(dim,
		protocol.EncodeSoundEffect(s.soundFlintUse, protocol.SoundCategoryBlock, float64(x), float64(y), float64(z), 1, 1, 0),
		float64(x), float64(z), s.playerSnapshot())
	return true
}

// xOrZ 按方向返回水平坐标（alongX 为 true 时取 x，否则取 z）。
func xOrZ(alongX bool, x, z int) int {
	if alongX {
		return x
	}
	return z
}

// endPortalFrameOffsets 是末地传送门框相对中心（框架环半径 2）的 12 个位置
// （不含四角）；内部为 |dx|≤1、|dz|≤1 的 3×3。
var endPortalFrameOffsets = [12][2]int{
	{-2, -1}, {-2, 0}, {-2, 1},
	{2, -1}, {2, 0}, {2, 1},
	{-1, -2}, {0, -2}, {1, -2},
	{-1, 2}, {0, 2}, {1, 2},
}

// tryPlaceEnderEye 处理末影之眼点击末地传送门框：填充眼睛并在 12 个眼睛
// 齐备时激活传送门（中心 3×3 填为末地传送门方块）。
// 返回 true 表示已处理（调用方跳过常规放置流程）。
func (s *Server) tryPlaceEnderEye(player *session, w *world.World, x, y, z int, current uint16) bool {
	name, props, ok := registry.StateProps(current)
	if !ok || name != "minecraft:end_portal_frame" {
		return false
	}
	dim := w.Dimension()
	if props["eye"] != "true" {
		next := map[string]string{"eye": "true", "facing": props["facing"]}
		state, ok := registry.BlockStateWithProps("minecraft:end_portal_frame", next)
		if !ok || !w.SetBlock(x, y, z, state) {
			return true
		}
		s.broadcastBlockUpdate(dim, x, y, z, int32(state))
		s.broadcastToNearby(dim,
			protocol.EncodeSoundEffect(s.soundEndPortalFrameFill, protocol.SoundCategoryBlock, float64(x), float64(y), float64(z), 1, 1, 0),
			float64(x), float64(z), s.playerSnapshot())
		if player != nil && player.gameModeID() != uint8(config.GameModeCreative) {
			s.consumeSelectedItem(player, enderEyeName)
		}
	}
	// 眼睛齐备时激活：对每个候选中心校验 12 个框架。
	for _, offset := range endPortalFrameOffsets {
		centerX, centerZ := x-offset[0], z-offset[1]
		if !s.endPortalRingComplete(w, centerX, y, centerZ) {
			continue
		}
		if s.activateEndPortal(w, centerX, y, centerZ) {
			s.broadcastToNearby(dim,
				protocol.EncodeSoundEffect(s.soundEndPortalSpawn, protocol.SoundCategoryBlock,
					float64(centerX)+0.5, float64(y), float64(centerZ)+0.5, 1, 1, 0),
				float64(centerX)+0.5, float64(centerZ)+0.5, s.playerSnapshot())
			break
		}
	}
	return true
}

// endPortalRingComplete 报告以 (centerX, y, centerZ) 为中心的 12 个末地传送门
// 框是否都已填充眼睛。
func (s *Server) endPortalRingComplete(w *world.World, centerX, y, centerZ int) bool {
	for _, offset := range endPortalFrameOffsets {
		name, props, ok := registry.StateProps(w.BlockAt(centerX+offset[0], y, centerZ+offset[1]))
		if !ok || name != "minecraft:end_portal_frame" || props["eye"] != "true" {
			return false
		}
	}
	return true
}

// activateEndPortal 把中心 3×3 填为末地传送门方块；已有传送门时返回 false。
func (s *Server) activateEndPortal(w *world.World, centerX, y, centerZ int) bool {
	state, ok := registry.BlockStateIDs["minecraft:end_portal"]
	if !ok {
		return false
	}
	dim := w.Dimension()
	placed := 0
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			bx, bz := centerX+dx, centerZ+dz
			if isEndPortalState(w.BlockAt(bx, y, bz)) {
				continue
			}
			if w.SetBlock(bx, y, bz, state) {
				s.broadcastBlockUpdate(dim, bx, y, bz, int32(state))
				placed++
			}
		}
	}
	return placed > 0
}

// consumeSelectedItem 消耗手持物品一个（按物品名匹配；不匹配时不做任何事）。
// 用于末影之眼等按次消耗的物品。
func (s *Server) consumeSelectedItem(player *session, itemName string) {
	slot := player.selectedSlot
	stack := player.inventory.Get(slot)
	if stack.IsEmpty() {
		return
	}
	name, ok := registry.ItemName(stack.ItemID)
	if !ok || name != itemName {
		return
	}
	stack.Count--
	if stack.Count <= 0 {
		stack = item.Empty()
	}
	player.inventory.Set(slot, stack)
	player.tryWrite(protocol.EncodeSetPlayerInventory(int32(slot), stack.AppendSlot(nil)))
}

// checkPortalTravel 检查玩家所在位置的传送门方块并执行维度传送。
// 仅由会话读循环（移动处理后）调用；传送为即时（无原版进入延迟）。
func (s *Server) checkPortalTravel(player *session) {
	if !player.isJoined() || player.isDead() {
		return
	}
	if player.gameModeID() == 2 { // 旁观者不传送
		return
	}
	if player.portalCooldown.Load() > 0 {
		return
	}
	x, y, z, yaw, pitch := player.playerPosition()
	dim := player.dimensionID()
	w := s.worldFor(dim)
	feet := w.BlockAt(int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z)))
	head := w.BlockAt(int(math.Floor(x)), int(math.Floor(y+1)), int(math.Floor(z)))
	switch {
	case (dim == world.DimensionOverworld || dim == world.DimensionNether) &&
		(isNetherPortalState(feet) || isNetherPortalState(head)):
		s.travelThroughNetherPortal(player, x, y, z, yaw, pitch)
	case isEndPortalState(feet) || isEndPortalState(head):
		s.travelThroughEndPortal(player, x, y, z, yaw, pitch)
	}
}

// travelThroughNetherPortal 执行下界传送门的维度切换（坐标 1:8 缩放，
// 目标维度没有已存在传送门时构建传送门与平台）。
func (s *Server) travelThroughNetherPortal(player *session, x, y, z float64, yaw, pitch float32) {
	from := player.dimensionID()
	var to world.Dimension
	if from == world.DimensionNether {
		to = world.DimensionOverworld
	} else {
		to = world.DimensionNether
	}
	// 坐标缩放（下界 ↔ 主世界 1:8）。
	targetX, targetZ := x, z
	if to == world.DimensionNether {
		targetX, targetZ = x/netherScale, z/netherScale
	} else {
		targetX, targetZ = x*netherScale, z*netherScale
	}
	w := s.worldFor(to)
	blockX, blockZ := int(math.Floor(targetX)), int(math.Floor(targetZ))
	blockY := int(math.Floor(y))
	// 边界裁剪：缩放到世界边界之外时拉回边界内。
	if s.borderHalfSize > 0 {
		limit := s.borderHalfSize - 8
		blockX = int(math.Max(-limit, math.Min(limit, float64(blockX))))
		blockZ = int(math.Max(-limit, math.Min(limit, float64(blockZ))))
	}
	var destX, destY, destZ float64
	if px, py, pz, ok := s.findExistingNetherPortal(w, blockX, blockY, blockZ); ok {
		destX, destY, destZ = px, py, pz
	} else {
		destX, destY, destZ = s.buildNetherPortal(w, to, blockX, blockY, blockZ)
	}
	s.playPortalSound(player, x, y, z)
	if err := player.teleportTo(to, destX, destY, destZ, yaw, pitch); err != nil {
		player.tryWrite(protocol.EncodeSystemChat("传送失败。"))
		return
	}
	player.portalCooldown.Store(portalCooldownTicks)
	s.playPortalSound(player, destX, destY, destZ)
}

// travelThroughEndPortal 执行末地传送门传送：进入末地使用 (100, 50, 0) 平台，
// 离开末地回到主世界出生点。
func (s *Server) travelThroughEndPortal(player *session, x, y, z float64, yaw, pitch float32) {
	from := player.dimensionID()
	if from == world.DimensionEnd {
		sx, sy, sz := s.spawnPositionFor(world.DimensionOverworld)
		s.playPortalSound(player, x, y, z)
		if err := player.teleportTo(world.DimensionOverworld, sx, sy, sz, yaw, pitch); err != nil {
			player.tryWrite(protocol.EncodeSystemChat("传送失败。"))
			return
		}
		player.portalCooldown.Store(portalCooldownTicks)
		s.playPortalSound(player, sx, sy, sz)
		return
	}
	// 进入末地：确保到达平台存在（首次传送时构建）。
	end := s.worldFor(world.DimensionEnd)
	const platformX, platformZ = 100, 0
	s.ensureEndPlatform(end, platformX, platformZ)
	destX := float64(platformX) + 0.5
	destY := float64(endPlatformY)
	destZ := float64(platformZ) + 0.5
	s.playPortalSound(player, x, y, z)
	if err := player.teleportTo(world.DimensionEnd, destX, destY, destZ, yaw, pitch); err != nil {
		player.tryWrite(protocol.EncodeSystemChat("传送失败。"))
		return
	}
	player.portalCooldown.Store(portalCooldownTicks)
	s.playPortalSound(player, destX, destY, destZ)
}

// ensureEndPlatform 构建末地到达平台（5×5 黑曜石，上方 3 格空气），
// 平台已存在时不做任何事。
func (s *Server) ensureEndPlatform(w *world.World, centerX, centerZ int) {
	if w.BlockAt(centerX, endPlatformY-1, centerZ) == world.ObsidianBlock {
		return
	}
	dim := world.DimensionEnd
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			x, z := centerX+dx, centerZ+dz
			if w.SetBlock(x, endPlatformY-1, z, world.ObsidianBlock) {
				s.broadcastBlockUpdate(dim, x, endPlatformY-1, z, int32(world.ObsidianBlock))
			}
			for dy := 0; dy <= 2; dy++ {
				if w.SetBlock(x, endPlatformY+dy, z, world.AirBlock) {
					s.broadcastBlockUpdate(dim, x, endPlatformY+dy, z, int32(world.AirBlock))
				}
			}
		}
	}
}

// findExistingNetherPortal 在目标坐标附近查找已存在的下界传送门方块，
// 返回其底部中心处的脚部坐标。
func (s *Server) findExistingNetherPortal(w *world.World, x, y, z int) (float64, float64, float64, bool) {
	bestX, bestY, bestZ := 0, 0, 0
	bestDistance := math.MaxFloat64
	for dy := -portalSearchHeight; dy <= portalSearchHeight; dy++ {
		scanY := y + dy
		if _, ok := world.SectionIndex(scanY); !ok {
			continue
		}
		for dx := -portalSearchRadius; dx <= portalSearchRadius; dx++ {
			for dz := -portalSearchRadius; dz <= portalSearchRadius; dz++ {
				if !isNetherPortalState(w.BlockAt(x+dx, scanY, z+dz)) {
					continue
				}
				distance := math.Hypot(float64(dx), float64(dz)) + math.Abs(float64(dy))*0.5
				if distance < bestDistance {
					bestDistance = distance
					bestX, bestY, bestZ = x+dx, scanY, z+dz
				}
			}
		}
	}
	if bestDistance == math.MaxFloat64 {
		return 0, 0, 0, false
	}
	// 沿该列向下找到传送门底部（玩家站在最底一格的高度）。
	for {
		if _, ok := world.SectionIndex(bestY - 1); !ok {
			break
		}
		if !isNetherPortalState(w.BlockAt(bestX, bestY-1, bestZ)) {
			break
		}
		bestY--
	}
	return float64(bestX) + 0.5, float64(bestY), float64(bestZ) + 0.5, true
}

// buildNetherPortal 在目标维度构建一个 2×3 的下界传送门与黑曜石平台，
// 返回玩家落点；平台位置根据地形选择（下界选实体地面，主世界避开水面）。
func (s *Server) buildNetherPortal(w *world.World, dim world.Dimension, x, y, z int) (float64, float64, float64) {
	state, ok := portalBlockState("x")
	if !ok {
		return float64(x) + 0.5, float64(y), float64(z) + 0.5
	}
	feetY := s.portalFeetY(w, dim, x, y, z)
	if s.borderHalfSize > 0 {
		limit := s.borderHalfSize - 4
		x = int(math.Max(-limit, math.Min(limit, float64(x))))
		z = int(math.Max(-limit, math.Min(limit, float64(z))))
	}
	// 平台与框架：内部 x..x+1（宽 2）、y..y+2（高 3），门面在 z 平面。
	for dx := -1; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			bx, bz := x+dx, z+dz
			setPortalBlock(s, w, dim, bx, feetY-1, bz, world.ObsidianBlock)
			for dy := 0; dy <= 4; dy++ {
				setPortalBlock(s, w, dim, bx, feetY+dy, bz, world.AirBlock)
			}
		}
	}
	// 侧面柱与上下横梁。
	for yy := feetY - 1; yy <= feetY+3; yy++ {
		setPortalBlock(s, w, dim, x-1, yy, z, world.ObsidianBlock)
		setPortalBlock(s, w, dim, x+2, yy, z, world.ObsidianBlock)
	}
	for dx := 0; dx <= 1; dx++ {
		setPortalBlock(s, w, dim, x+dx, feetY+3, z, world.ObsidianBlock)
	}
	// 内部传送门方块。
	for dx := 0; dx <= 1; dx++ {
		for yy := feetY; yy <= feetY+2; yy++ {
			setPortalBlock(s, w, dim, x+dx, yy, z, state)
		}
	}
	return float64(x) + 0.5, float64(feetY), float64(z) + 0.5
}

// portalFeetY 选择新建传送门的脚部高度：下界从内存高度向下搜索实体地面；
// 主世界取列支撑面（水面以上）。
func (s *Server) portalFeetY(w *world.World, dim world.Dimension, x, y, z int) int {
	if dim == world.DimensionNether {
		start := y
		if start < 32 {
			start = 32
		}
		if start > 110 {
			start = 110
		}
		for scanY := start; scanY >= 8; scanY-- {
			below := w.BlockAt(x, scanY-1, z)
			if below == world.AirBlock || below == world.LavaBlock || below == world.BedrockBlock {
				continue
			}
			clear := true
			for dy := 0; dy <= 3; dy++ {
				state := w.BlockAt(x, scanY+dy, z)
				if state != world.AirBlock && !isNetherPortalState(state) {
					clear = false
					break
				}
			}
			if clear {
				return scanY
			}
		}
		return 32
	}
	column, ok := w.ColumnAt(x, z)
	if !ok || !column.HasSolid {
		fallback := y
		if fallback < world.SeaLevel+2 {
			fallback = world.SeaLevel + 2
		}
		return fallback
	}
	feet := column.SolidY + 1
	if feet <= world.SeaLevel+1 {
		feet = world.SeaLevel + 2
	}
	return feet
}

// setPortalBlock 设置方块并广播变更（传送门构建辅助）。
func setPortalBlock(s *Server, w *world.World, dim world.Dimension, x, y, z int, state uint16) {
	if !w.SetBlock(x, y, z, state) {
		return
	}
	s.broadcastBlockUpdate(dim, x, y, z, int32(state))
}

// playPortalSound 向玩家播放传送门音效（传送前/后各一次，与原版观感一致）。
func (s *Server) playPortalSound(player *session, x, y, z float64) {
	if s.soundPortalTravel == 0 {
		return
	}
	player.tryWrite(protocol.EncodeSoundEffect(s.soundPortalTravel,
		protocol.SoundCategoryAmbient, x, y, z, 1, 1, 0))
}

// clearPortalsNear 在破坏黑曜石后清除与之相连的传送门方块
// （近似原版“框架破坏后传送门熄灭”行为）。
func (s *Server) clearPortalsNear(dim world.Dimension, x, y, z int) {
	w := s.worldFor(dim)
	type point struct{ x, y, z int }
	var queue []point
	visited := make(map[point]bool)
	for _, offset := range neighborOffsets6 {
		queue = append(queue, point{x + offset[0], y + offset[1], z + offset[2]})
	}
	cleared := make(map[point]bool)
	for len(queue) > 0 && len(visited) < 1024 {
		pos := queue[0]
		queue = queue[1:]
		if visited[pos] {
			continue
		}
		visited[pos] = true
		if _, ok := world.SectionIndex(pos.y); !ok {
			continue
		}
		if !isNetherPortalState(w.BlockAt(pos.x, pos.y, pos.z)) {
			continue
		}
		cleared[pos] = true
		for _, offset := range neighborOffsets6 {
			queue = append(queue, point{pos.x + offset[0], pos.y + offset[1], pos.z + offset[2]})
		}
	}
	for pos := range cleared {
		if w.SetBlock(pos.x, pos.y, pos.z, world.AirBlock) {
			s.broadcastBlockUpdate(dim, pos.x, pos.y, pos.z, int32(world.AirBlock))
		}
	}
}
