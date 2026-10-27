//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package world

// Precipitation 等价原版 Biome.Precipitation：某位置是否会降水、以及降的是雨还是雪。
type Precipitation uint8

const (
	// PrecipitationNone 表示该群系不降水（沙漠、热带草原、恶地等）。
	PrecipitationNone Precipitation = iota
	// PrecipitationRain 表示降雨。
	PrecipitationRain
	// PrecipitationSnow 表示降雪。
	PrecipitationSnow
)

// biomePrecipitation 是各群系的降水类型，取值依据原版 biome JSON 的
// `has_precipitation` 与 `Biome.getPrecipitationAt`（temperature < 0.15 时降雪）：
//   - has_precipitation = false：desert / savanna / badlands（及其变种）；
//   - temperature < 0.15：snowy_plains / snowy_taiga / snowy_beach；
//   - 其余为降雨。
//
// 只列出本模块生成器会用到的群系，其它群系按降雨处理（与原版“大多数群系降雨”一致）。
var biomePrecipitation = map[uint16]Precipitation{
	biomeDesert:          PrecipitationNone,
	biomeSavanna:         PrecipitationNone,
	biomeBadlands:        PrecipitationNone,
	biomeSnowyPlains:     PrecipitationSnow,
	biomeSnowyTaiga:      PrecipitationSnow,
	biomeSnowyBeach:      PrecipitationSnow,
	biomeDeepOcean:       PrecipitationRain,
	biomeOcean:           PrecipitationRain,
	biomeBeach:           PrecipitationRain,
	biomeStonyShore:      PrecipitationRain,
	biomeRiver:           PrecipitationRain,
	biomeForest:          PrecipitationRain,
	biomeFlowerForest:    PrecipitationRain,
	biomeBirchForest:     PrecipitationRain,
	biomeDarkForest:      PrecipitationRain,
	biomeTaiga:           PrecipitationRain,
	biomeSwamp:           PrecipitationRain,
	biomeJungle:          PrecipitationRain,
	biomeSunflowerPlains: PrecipitationRain,
	BiomePlains:          PrecipitationRain,
}

// PrecipitationAt 返回群系的降水类型（未知群系按降雨处理）。
func PrecipitationAt(biome uint16) Precipitation {
	if precipitation, ok := biomePrecipitation[biome]; ok {
		return precipitation
	}
	return PrecipitationRain
}

// 常用群系 ID 的导出别名（降水判定与测试使用；其它群系常量仍为包内私有）。
var (
	// BiomeSnowyPlains 是雪原。
	BiomeSnowyPlains = biomeSnowyPlains
	// BiomeSnowyTaiga 是积雪针叶林。
	BiomeSnowyTaiga = biomeSnowyTaiga
	// BiomeSnowyBeach 是积雪沙滩。
	BiomeSnowyBeach = biomeSnowyBeach
	// BiomeDesert 是沙漠。
	BiomeDesert = biomeDesert
	// BiomeSavanna 是热带草原。
	BiomeSavanna = biomeSavanna
	// BiomeBadlands 是恶地。
	BiomeBadlands = biomeBadlands
)

// BiomePrecipitation 是 PrecipitationAt 的别名，便于调用方按“群系”语义使用。
func BiomePrecipitation(biome uint16) Precipitation {
	return PrecipitationAt(biome)
}
