package registry

// 熔炉燃料燃烧时长表。
//
// 数值与原版 net.minecraft.world.item.crafting.FuelValues（vanillaBurnTimes）
// 一致，公开对照见 Minecraft Wiki「Smelting#Fuel」表格（tick 数）。原版燃料
// 表是 Java 代码内置（不属于数据包，无法从 jar 数据提取），因此按物品/物品
// 标签在此显式列出；标签成员在初始化时展开为物品 ID。
//
// 高炉与烟熏炉的燃料燃烧速度是熔炉的 2 倍（每次操作消耗一半时间），因此
// 调用方需要按容器类型缩放（见 server/furnace.go）。

// fuelTagBurnTicks 是按物品标签分组的燃烧时长（tick）。
// 1.21.11 没有 wool_stairs / wool_slabs / wooden_signs 等标签，
// 对应物品在 fuelItemBurnTicks 中逐一列出。
var fuelTagBurnTicks = map[string]int32{
	"minecraft:logs_that_burn":         300,
	"minecraft:planks":                 300,
	"minecraft:wooden_stairs":          300,
	"minecraft:wooden_slabs":           150,
	"minecraft:wooden_pressure_plates": 300,
	"minecraft:wooden_trapdoors":       300,
	"minecraft:fence_gates":            300,
	"minecraft:wooden_fences":          300,
	"minecraft:wooden_buttons":         100,
	"minecraft:wooden_doors":           200,
	"minecraft:signs":                  200,
	"minecraft:hanging_signs":          200,
	"minecraft:banners":                300,
	"minecraft:saplings":               100,
	"minecraft:boats":                  1200,
	"minecraft:chest_boats":            1200,
	"minecraft:wool":                   100,
	"minecraft:wool_carpets":           67,
}

// fuelItemBurnTicks 是单个物品的燃烧时长（tick）；标签已覆盖的不重复列出。
var fuelItemBurnTicks = map[string]int32{
	"minecraft:lava_bucket":              20000,
	"minecraft:coal_block":               16000,
	"minecraft:dried_kelp_block":         4000,
	"minecraft:blaze_rod":                2400,
	"minecraft:coal":                     1600,
	"minecraft:charcoal":                 1600,
	"minecraft:bamboo_mosaic":            300,
	"minecraft:bamboo_mosaic_stairs":     300,
	"minecraft:bamboo_mosaic_slab":       150,
	"minecraft:chiseled_bookshelf":       300,
	"minecraft:block_of_bamboo":          300,
	"minecraft:stripped_block_of_bamboo": 300,
	"minecraft:mangrove_roots":           300,
	"minecraft:ladder":                   300,
	"minecraft:crafting_table":           300,
	"minecraft:cartography_table":        300,
	"minecraft:fletching_table":          300,
	"minecraft:smithing_table":           300,
	"minecraft:loom":                     300,
	"minecraft:bookshelf":                300,
	"minecraft:lectern":                  300,
	"minecraft:composter":                300,
	"minecraft:chest":                    300,
	"minecraft:trapped_chest":            300,
	"minecraft:barrel":                   300,
	"minecraft:daylight_detector":        300,
	"minecraft:jukebox":                  300,
	"minecraft:note_block":               300,
	"minecraft:crossbow":                 300,
	"minecraft:bow":                      300,
	"minecraft:fishing_rod":              300,
	"minecraft:bowl":                     100,
	"minecraft:stick":                    100,
	"minecraft:dead_bush":                100,
	"minecraft:azalea":                   100,
	"minecraft:flowering_azalea":         100,
	"minecraft:bamboo":                   50,
	"minecraft:scaffolding":              50,
}

// fuelBurnTicks 是展开后的燃料表（物品 ID → 燃烧时长）。
var fuelBurnTicks = func() map[int32]int32 {
	table := make(map[int32]int32, len(fuelItemBurnTicks)+256)
	for name, ticks := range fuelItemBurnTicks {
		if id, ok := ItemIDs[name]; ok {
			table[id] = ticks
		}
	}
	tagIndex := make(map[string][]int32)
	for _, registry := range cachedTags() {
		if registry.Name != "minecraft:item" {
			continue
		}
		for _, tag := range registry.Tags {
			tagIndex[tag.Name] = tag.Entries
		}
	}
	for name, ticks := range fuelTagBurnTicks {
		for _, id := range tagIndex[name] {
			if _, ok := table[id]; !ok {
				table[id] = ticks
			}
		}
	}
	// 木制工具（200 tick）：不放入燃料标签，按具体物品列出。
	for _, prefix := range []string{"minecraft:wooden_"} {
		for _, tool := range []string{"pickaxe", "shovel", "axe", "hoe", "sword", "spear"} {
			if id, ok := ItemIDs[prefix+tool]; ok {
				table[id] = 200
			}
		}
	}
	return table
}()

// FuelBurnTicks 返回物品作为熔炉燃料的燃烧时长（tick）；不是燃料时返回 0。
func FuelBurnTicks(itemID int32) int32 {
	return fuelBurnTicks[itemID]
}
