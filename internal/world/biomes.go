//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package world

import "gmcs/internal/registry"

// 群系 ID（同步注册表 minecraft:worldgen/biome 的条目 ID）。
// 世界数据中的群系必须使用同步注册表 ID（与区块数据一致）。
var (
	biomeDeepOcean       = mustBiome("minecraft:deep_ocean")
	biomeOcean           = mustBiome("minecraft:ocean")
	biomeBeach           = mustBiome("minecraft:beach")
	biomeSnowyBeach      = mustBiome("minecraft:snowy_beach")
	biomeStonyShore      = mustBiome("minecraft:stony_shore")
	biomeRiver           = mustBiome("minecraft:river")
	biomeForest          = mustBiome("minecraft:forest")
	biomeFlowerForest    = mustBiome("minecraft:flower_forest")
	biomeBirchForest     = mustBiome("minecraft:birch_forest")
	biomeDarkForest      = mustBiome("minecraft:dark_forest")
	biomeTaiga           = mustBiome("minecraft:taiga")
	biomeSnowyPlains     = mustBiome("minecraft:snowy_plains")
	biomeSnowyTaiga      = mustBiome("minecraft:snowy_taiga")
	biomeDesert          = mustBiome("minecraft:desert")
	biomeBadlands        = mustBiome("minecraft:badlands")
	biomeSavanna         = mustBiome("minecraft:savanna")
	biomeSwamp           = mustBiome("minecraft:swamp")
	biomeJungle          = mustBiome("minecraft:jungle")
	biomeSunflowerPlains = mustBiome("minecraft:sunflower_plains")
	biomeMeadow          = mustBiome("minecraft:meadow")
	biomeGrove           = mustBiome("minecraft:grove")
	biomeSnowySlopes     = mustBiome("minecraft:snowy_slopes")
	biomeFrozenPeaks     = mustBiome("minecraft:frozen_peaks")
	biomeStonyPeaks      = mustBiome("minecraft:stony_peaks")
)

// mustBiome 查询同步注册表中的群系 ID；缺少必需群系属于部署错误，立即失败。
func mustBiome(name string) uint16 {
	id, ok := registry.SyncEntryID("minecraft:worldgen/biome", name)
	if !ok {
		panic("world: 同步注册表缺少群系 " + name)
	}
	return uint16(id)
}
