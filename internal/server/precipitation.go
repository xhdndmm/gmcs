//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"
	"strconv"

	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 群系降水（原版 ServerLevel.tickChunk → tickPrecipitation + Biome.shouldSnow /
// shouldFreeze）。
//
// 原版对每个可 tick 区块每 tick 抽一个随机方块列（视距 10 时约 441 列/tick），
// 按群系降水类型落雪/结冰。gmcs 没有区块 tick 网格，改为“每 tick 固定预算、
// 跨玩家轮转”的抽样：抽样总量与在线人数无关，积雪/结冰因此比原版慢，
// 但降雪位置与类型规则与原版一致。
const (
	// precipitationSampleBudget 是每 tick 的抽样列数上限（跨玩家轮转）。
	// 原版视距 10 时约 441 列/tick，这里约为其 1/7。
	precipitationSampleBudget = 64
	// precipitationRadiusChunks 是抽样范围（区块半径，与原版模拟距离同量级）。
	precipitationRadiusChunks = 8
	// snowMaxLayers 是雪层的最大层数（原版 SnowLayerBlock.MAX_LAYERS）。
	snowMaxLayers = 8
)

// snowLayerState 返回雪层方块在给定层数下的状态 ID。
func snowLayerState(layers int) (uint16, bool) {
	if layers < 1 {
		layers = 1
	}
	if layers > snowMaxLayers {
		layers = snowMaxLayers
	}
	return registry.BlockStateWithProps("minecraft:snow", map[string]string{
		"layers": strconv.Itoa(layers),
	})
}

// snowLayerCount 返回雪层方块的层数（非雪层返回 false）。
func snowLayerCount(state uint16) (int, bool) {
	name, props, ok := registry.StateProps(state)
	if !ok || name != "minecraft:snow" {
		return 0, false
	}
	layers, err := strconv.Atoi(props["layers"])
	if err != nil {
		return 0, false
	}
	return layers, true
}

// tickPrecipitation 在视距范围内抽样方块列并执行一次原版 tickPrecipitation。
// 只在降雨（含降雪）时工作；抽样预算固定，按玩家轮转分配。
func (s *Server) tickPrecipitation(players []*session) {
	if !s.isRaining() || len(players) == 0 {
		return
	}
	w := s.worldFor(world.DimensionOverworld)
	if w == nil {
		return
	}
	s.precipitationCursor++
	for i := 0; i < precipitationSampleBudget; i++ {
		player := players[(s.precipitationCursor+i)%len(players)]
		if !player.isJoined() || player.dimensionID() != world.DimensionOverworld {
			continue
		}
		px, _, pz, _, _ := player.playerPosition()
		centerX := int(math.Floor(px)) >> 4
		centerZ := int(math.Floor(pz)) >> 4
		cx := centerX + s.nextInt(2*precipitationRadiusChunks+1) - precipitationRadiusChunks
		cz := centerZ + s.nextInt(2*precipitationRadiusChunks+1) - precipitationRadiusChunks
		x := cx<<4 + s.nextInt(16)
		z := cz<<4 + s.nextInt(16)
		s.tryPrecipitation(w, x, z)
	}
}

// tryPrecipitation 对一个方块列执行原版 tickPrecipitation：
//
//   - 群系降雪：该列最上方是水时冻成冰；已是雪层时加厚一层（原版
//     SnowLayerBlock.randomTick 的增厚效果）；否则在最高方块上方放一层雪；
//   - 群系降雨/不降水：什么都不做（原版只有炼药锅会接雨水，gmcs 未实现）。
//
// 返回 true 表示方块发生了变化（已广播）。
func (s *Server) tryPrecipitation(w *world.World, x, z int) bool {
	biome, ok := w.BiomeAt(x, z)
	if !ok || world.PrecipitationAt(biome) != world.PrecipitationSnow {
		return false
	}
	column, ok := w.ColumnAt(x, z)
	if !ok || !column.HasTop {
		return false
	}
	// 水面结冰（原版 Biome.shouldFreeze：表层是水且群系足够冷）。
	if registry.IsWaterState(column.TopState) {
		ice, ok := registry.BlockStateIDs["minecraft:ice"]
		if !ok {
			return false
		}
		if !w.SetBlock(x, column.TopY, z, ice) {
			return false
		}
		s.broadcastBlockUpdate(world.DimensionOverworld, x, column.TopY, z, int32(ice))
		return true
	}
	// 已有雪层：加厚一层（原版积雪堆积）。
	if layers, isSnow := snowLayerCount(column.TopState); isSnow {
		if layers >= snowMaxLayers {
			return false
		}
		next, ok := snowLayerState(layers + 1)
		if !ok || !w.SetBlock(x, column.TopY, z, next) {
			return false
		}
		s.broadcastBlockUpdate(world.DimensionOverworld, x, column.TopY, z, int32(next))
		return true
	}
	// 新落雪：最高方块必须是可支撑雪的实心方块，且上方是空气。
	if column.TopY+1 >= world.WorldMinY+world.WorldHeight {
		return false
	}
	above := w.BlockAt(x, column.TopY+1, z)
	if above != world.AirBlock {
		return false
	}
	if len(registry.BlockStateShape(column.TopState)) == 0 {
		return false
	}
	snow, ok := snowLayerState(1)
	if !ok {
		return false
	}
	if !w.SetBlock(x, column.TopY+1, z, snow) {
		return false
	}
	s.broadcastBlockUpdate(world.DimensionOverworld, x, column.TopY+1, z, int32(snow))
	return true
}
