package registry

import "sync"

// 堆肥桶可堆肥物品表（对照 1.21.11 反编译的 ComposterBlock.bootstrap）。
//
// 数值是每个物品填入堆肥桶的成功率（0 < chance <= 1）。原版表是 Java 代码
// 内置（不属于数据包，无法从 jar 的 data/ 提取），因此在此显式列出；
// 数据来自 ComposterBlock.bootstrap 字节码逐条提取（共 115 项，已按概率分组）。
//
// 注意：空堆肥桶（level 0）填入任何可堆肥物品都必定成功（原版在
// level==0 时跳过随机判定），只有在 level > 0 时才按表中概率掷骰。
var compostChances = map[string]float32{
	"minecraft:acacia_leaves":           0.30,
	"minecraft:acacia_sapling":          0.30,
	"minecraft:azalea_leaves":           0.30,
	"minecraft:beetroot_seeds":          0.30,
	"minecraft:birch_leaves":            0.30,
	"minecraft:birch_sapling":           0.30,
	"minecraft:bush":                    0.30,
	"minecraft:cactus_flower":           0.30,
	"minecraft:cherry_leaves":           0.30,
	"minecraft:cherry_sapling":          0.30,
	"minecraft:dark_oak_leaves":         0.30,
	"minecraft:dark_oak_sapling":        0.30,
	"minecraft:dried_kelp":              0.30,
	"minecraft:dry_short_grass":         0.30,
	"minecraft:dry_tall_grass":          0.30,
	"minecraft:firefly_bush":            0.30,
	"minecraft:glow_berries":            0.30,
	"minecraft:hanging_roots":           0.30,
	"minecraft:jungle_leaves":           0.30,
	"minecraft:jungle_sapling":          0.30,
	"minecraft:kelp":                    0.30,
	"minecraft:leaf_litter":             0.30,
	"minecraft:mangrove_leaves":         0.30,
	"minecraft:mangrove_propagule":      0.30,
	"minecraft:mangrove_roots":          0.30,
	"minecraft:melon_seeds":             0.30,
	"minecraft:moss_carpet":             0.30,
	"minecraft:oak_leaves":              0.30,
	"minecraft:oak_sapling":             0.30,
	"minecraft:pale_hanging_moss":       0.30,
	"minecraft:pale_moss_carpet":        0.30,
	"minecraft:pale_oak_leaves":         0.30,
	"minecraft:pale_oak_sapling":        0.30,
	"minecraft:pink_petals":             0.30,
	"minecraft:pitcher_pod":             0.30,
	"minecraft:pumpkin_seeds":           0.30,
	"minecraft:seagrass":                0.30,
	"minecraft:short_grass":             0.30,
	"minecraft:small_dripleaf":          0.30,
	"minecraft:spruce_leaves":           0.30,
	"minecraft:spruce_sapling":          0.30,
	"minecraft:sweet_berries":           0.30,
	"minecraft:torchflower_seeds":       0.30,
	"minecraft:wheat_seeds":             0.30,
	"minecraft:wildflowers":             0.30,
	"minecraft:cactus":                  0.50,
	"minecraft:dried_kelp_block":        0.50,
	"minecraft:flowering_azalea_leaves": 0.50,
	"minecraft:glow_lichen":             0.50,
	"minecraft:melon_slice":             0.50,
	"minecraft:nether_sprouts":          0.50,
	"minecraft:sugar_cane":              0.50,
	"minecraft:tall_grass":              0.50,
	"minecraft:twisting_vines":          0.50,
	"minecraft:vine":                    0.50,
	"minecraft:weeping_vines":           0.50,
	"minecraft:allium":                  0.65,
	"minecraft:apple":                   0.65,
	"minecraft:azalea":                  0.65,
	"minecraft:azure_bluet":             0.65,
	"minecraft:beetroot":                0.65,
	"minecraft:big_dripleaf":            0.65,
	"minecraft:blue_orchid":             0.65,
	"minecraft:brown_mushroom":          0.65,
	"minecraft:carrot":                  0.65,
	"minecraft:carved_pumpkin":          0.65,
	"minecraft:closed_eyeblossom":       0.65,
	"minecraft:cocoa_beans":             0.65,
	"minecraft:cornflower":              0.65,
	"minecraft:crimson_fungus":          0.65,
	"minecraft:crimson_roots":           0.65,
	"minecraft:dandelion":               0.65,
	"minecraft:fern":                    0.65,
	"minecraft:large_fern":              0.65,
	"minecraft:lilac":                   0.65,
	"minecraft:lily_of_the_valley":      0.65,
	"minecraft:lily_pad":                0.65,
	"minecraft:melon":                   0.65,
	"minecraft:moss_block":              0.65,
	"minecraft:mushroom_stem":           0.65,
	"minecraft:nether_wart":             0.65,
	"minecraft:open_eyeblossom":         0.65,
	"minecraft:orange_tulip":            0.65,
	"minecraft:oxeye_daisy":             0.65,
	"minecraft:pale_moss_block":         0.65,
	"minecraft:peony":                   0.65,
	"minecraft:pink_tulip":              0.65,
	"minecraft:poppy":                   0.65,
	"minecraft:potato":                  0.65,
	"minecraft:pumpkin":                 0.65,
	"minecraft:red_mushroom":            0.65,
	"minecraft:red_tulip":               0.65,
	"minecraft:rose_bush":               0.65,
	"minecraft:sea_pickle":              0.65,
	"minecraft:shroomlight":             0.65,
	"minecraft:spore_blossom":           0.65,
	"minecraft:sunflower":               0.65,
	"minecraft:warped_fungus":           0.65,
	"minecraft:warped_roots":            0.65,
	"minecraft:wheat":                   0.65,
	"minecraft:white_tulip":             0.65,
	"minecraft:wither_rose":             0.65,
	"minecraft:baked_potato":            0.85,
	"minecraft:bread":                   0.85,
	"minecraft:brown_mushroom_block":    0.85,
	"minecraft:cookie":                  0.85,
	"minecraft:flowering_azalea":        0.85,
	"minecraft:hay_block":               0.85,
	"minecraft:nether_wart_block":       0.85,
	"minecraft:pitcher_plant":           0.85,
	"minecraft:red_mushroom_block":      0.85,
	"minecraft:torchflower":             0.85,
	"minecraft:warped_wart_block":       0.85,
	"minecraft:cake":                    1.00,
	"minecraft:pumpkin_pie":             1.00,
}

// compostIndex 是物品 ID → 堆肥成功率的查询副本。
var compostIndex = sync.OnceValue(func() map[int32]float32 {
	index := make(map[int32]float32, len(compostChances))
	for name, chance := range compostChances {
		if id, err := ItemID(name); err == nil {
			index[id] = chance
		}
	}
	return index
})

// CompostChance 返回物品的堆肥成功率（0 表示不是可堆肥物品）。
func CompostChance(itemID int32) float32 {
	return compostIndex()[itemID]
}
