package registry

import "sync"

// 酿造配方表：对照 1.21.11 官方服务端的 PotionBrewing.addVanillaMixes
// （经官方 mappings 翻译的 javap 字节码，2026-10 核对）逐条移植。
//
// 原版配方在代码中硬编码（不在数据包中），因此本文件为手写数据；
// 修改任意条目时必须重新对照字节码，不要凭 wiki 猜测。
//
// 结构（对照 PotionBrewing）：
//   - potionMixes    药水配方：from 药水 + 材料 → to 药水（瓶子物品不变）；
//   - containerMixes 容器配方：from 瓶子物品 + 材料 → to 瓶子物品（药水不变）；
//   - ingredients    全部材料（= 上述两类配方的材料并集，PotionBrewing.isIngredient）。
//
// 原版 addStartMix(item, to) 会展开为两条药水配方：
// 水瓶 + item → 平凡的药水（MUNDANE）、粗制的药水 + item → to。

// 药水种类 ID（minecraft:potion 注册表的 protocol_id，客户端内置）。
const (
	PotionWater              = 0
	PotionMundane            = 1
	PotionThick              = 2
	PotionAwkward            = 3
	PotionNightVision        = 4
	PotionLongNightVision    = 5
	PotionInvisibility       = 6
	PotionLongInvisibility   = 7
	PotionLeaping            = 8
	PotionLongLeaping        = 9
	PotionStrongLeaping      = 10
	PotionFireResistance     = 11
	PotionLongFireResistance = 12
	PotionSwiftness          = 13
	PotionLongSwiftness      = 14
	PotionStrongSwiftness    = 15
	PotionSlowness           = 16
	PotionLongSlowness       = 17
	PotionStrongSlowness     = 18
	PotionTurtleMaster       = 19
	PotionLongTurtleMaster   = 20
	PotionStrongTurtleMaster = 21
	PotionWaterBreathing     = 22
	PotionLongWaterBreathing = 23
	PotionHealing            = 24
	PotionStrongHealing      = 25
	PotionHarming            = 26
	PotionStrongHarming      = 27
	PotionPoison             = 28
	PotionLongPoison         = 29
	PotionStrongPoison       = 30
	PotionRegeneration       = 31
	PotionLongRegeneration   = 32
	PotionStrongRegeneration = 33
	PotionStrength           = 34
	PotionLongStrength       = 35
	PotionStrongStrength     = 36
	PotionWeakness           = 37
	PotionLongWeakness       = 38
	PotionLuck               = 39
	PotionSlowFalling        = 40
	PotionLongSlowFalling    = 41
	PotionWindCharged        = 42
	PotionWeaving            = 43
	PotionOozing             = 44
	PotionInfested           = 45
)

// brewingTables 是解析后的酿造表（物品 ID 在首次使用时解析）。
type brewingTables struct {
	// potionMixes 键 = from 药水 ID << 32 | 材料物品 ID，值 = to 药水 ID。
	potionMixes map[uint64]int32
	// containerMixes 键 = from 瓶子物品 ID << 32 | 材料物品 ID，值 = to 瓶子物品 ID。
	containerMixes map[uint64]int32
	// ingredients 是全部材料物品（isIngredient）。
	ingredients map[int32]struct{}
	// containerItems 是全部药水容器物品（isContainer）。
	containerItems map[int32]struct{}
}

// brewingTableData 惰性构建酿造表；物品名缺失时跳过对应条目（官方注册表
// 数据完整时不会发生，测试会断言条目数量）。
var brewingTableData = sync.OnceValue(func() brewingTables {
	tables := brewingTables{
		potionMixes:    make(map[uint64]int32, 64),
		containerMixes: make(map[uint64]int32, 2),
		ingredients:    make(map[int32]struct{}, 32),
		containerItems: make(map[int32]struct{}, 3),
	}
	// item 解析物品名；注册表数据缺失时返回 -1（条目跳过）。
	item := func(name string) int32 {
		id, err := ItemID(name)
		if err != nil {
			return -1
		}
		return id
	}

	// —— 基础（水瓶直接酿造的三种）——
	reg := func(from int32, itemID int32, to int32) {
		if itemID < 0 {
			return
		}
		tables.potionMixes[brewMixKey(from, itemID)] = to
		tables.ingredients[itemID] = struct{}{}
	}
	reg(PotionWater, item("minecraft:glowstone_dust"), PotionThick)
	reg(PotionWater, item("minecraft:redstone"), PotionMundane)
	reg(PotionWater, item("minecraft:nether_wart"), PotionAwkward)
	// —— addStartMix 展开（水瓶 → 平凡；粗制 → 效果）——
	start := func(itemID int32, to int32) {
		if itemID < 0 {
			return
		}
		reg(PotionWater, itemID, PotionMundane)
		reg(PotionAwkward, itemID, to)
	}
	start(item("minecraft:breeze_rod"), PotionWindCharged)
	start(item("minecraft:slime_block"), PotionOozing)
	start(item("minecraft:stone"), PotionInfested)
	start(item("minecraft:cobweb"), PotionWeaving)
	reg(PotionAwkward, item("minecraft:golden_carrot"), PotionNightVision)
	reg(PotionNightVision, item("minecraft:redstone"), PotionLongNightVision)
	reg(PotionNightVision, item("minecraft:fermented_spider_eye"), PotionInvisibility)
	reg(PotionLongNightVision, item("minecraft:fermented_spider_eye"), PotionLongInvisibility)
	reg(PotionInvisibility, item("minecraft:redstone"), PotionLongInvisibility)
	start(item("minecraft:magma_cream"), PotionFireResistance)
	reg(PotionFireResistance, item("minecraft:redstone"), PotionLongFireResistance)
	start(item("minecraft:rabbit_foot"), PotionLeaping)
	reg(PotionLeaping, item("minecraft:redstone"), PotionLongLeaping)
	reg(PotionLeaping, item("minecraft:glowstone_dust"), PotionStrongLeaping)
	reg(PotionLeaping, item("minecraft:fermented_spider_eye"), PotionSlowness)
	reg(PotionLongLeaping, item("minecraft:fermented_spider_eye"), PotionLongSlowness)
	reg(PotionSlowness, item("minecraft:redstone"), PotionLongSlowness)
	reg(PotionSlowness, item("minecraft:glowstone_dust"), PotionStrongSlowness)
	reg(PotionAwkward, item("minecraft:turtle_helmet"), PotionTurtleMaster)
	reg(PotionTurtleMaster, item("minecraft:redstone"), PotionLongTurtleMaster)
	reg(PotionTurtleMaster, item("minecraft:glowstone_dust"), PotionStrongTurtleMaster)
	reg(PotionSwiftness, item("minecraft:fermented_spider_eye"), PotionSlowness)
	reg(PotionLongSwiftness, item("minecraft:fermented_spider_eye"), PotionLongSlowness)
	start(item("minecraft:sugar"), PotionSwiftness)
	reg(PotionSwiftness, item("minecraft:redstone"), PotionLongSwiftness)
	reg(PotionSwiftness, item("minecraft:glowstone_dust"), PotionStrongSwiftness)
	reg(PotionAwkward, item("minecraft:pufferfish"), PotionWaterBreathing)
	reg(PotionWaterBreathing, item("minecraft:redstone"), PotionLongWaterBreathing)
	start(item("minecraft:glistering_melon_slice"), PotionHealing)
	reg(PotionHealing, item("minecraft:glowstone_dust"), PotionStrongHealing)
	reg(PotionHealing, item("minecraft:fermented_spider_eye"), PotionHarming)
	reg(PotionStrongHealing, item("minecraft:fermented_spider_eye"), PotionStrongHarming)
	reg(PotionHarming, item("minecraft:glowstone_dust"), PotionStrongHarming)
	reg(PotionPoison, item("minecraft:fermented_spider_eye"), PotionHarming)
	reg(PotionLongPoison, item("minecraft:fermented_spider_eye"), PotionHarming)
	reg(PotionStrongPoison, item("minecraft:fermented_spider_eye"), PotionStrongHarming)
	start(item("minecraft:spider_eye"), PotionPoison)
	reg(PotionPoison, item("minecraft:redstone"), PotionLongPoison)
	reg(PotionPoison, item("minecraft:glowstone_dust"), PotionStrongPoison)
	start(item("minecraft:ghast_tear"), PotionRegeneration)
	reg(PotionRegeneration, item("minecraft:redstone"), PotionLongRegeneration)
	reg(PotionRegeneration, item("minecraft:glowstone_dust"), PotionStrongRegeneration)
	start(item("minecraft:blaze_powder"), PotionStrength)
	reg(PotionStrength, item("minecraft:redstone"), PotionLongStrength)
	reg(PotionStrength, item("minecraft:glowstone_dust"), PotionStrongStrength)
	reg(PotionWater, item("minecraft:fermented_spider_eye"), PotionWeakness)
	reg(PotionWeakness, item("minecraft:redstone"), PotionLongWeakness)
	reg(PotionAwkward, item("minecraft:phantom_membrane"), PotionSlowFalling)
	reg(PotionSlowFalling, item("minecraft:redstone"), PotionLongSlowFalling)

	// 容器配方（addContainerRecipe）：药水物品 + 材料 → 另一药水物品。
	potionItem := item("minecraft:potion")
	splashItem := item("minecraft:splash_potion")
	lingeringItem := item("minecraft:lingering_potion")
	containerRecipe := func(from, itemID, to int32) {
		if from < 0 || itemID < 0 || to < 0 {
			return
		}
		tables.containerMixes[brewMixKey(from, itemID)] = to
		tables.ingredients[itemID] = struct{}{}
	}
	containerRecipe(potionItem, item("minecraft:gunpowder"), splashItem)
	containerRecipe(splashItem, item("minecraft:dragon_breath"), lingeringItem)
	// 容器（addContainer：isContainer 判定）。
	for _, id := range []int32{potionItem, splashItem, lingeringItem} {
		if id < 0 {
			continue
		}
		tables.containerItems[id] = struct{}{}
	}
	return tables
})

// brewMixKey 把 (from, item) 打包为配方查找键。
func brewMixKey(from, item int32) uint64 {
	return uint64(uint32(from))<<32 | uint64(uint32(item))
}

// BrewingIsIngredient 报告物品是否可用作酿造材料
// （原版 PotionBrewing.isIngredient：药水材料或容器材料）。
func BrewingIsIngredient(itemID int32) bool {
	_, ok := brewingTableData().ingredients[itemID]
	return ok
}

// BrewingIsContainerItem 报告物品是否是药水容器
// （原版 PotionBrewing.isContainer：potion/splash_potion/lingering_potion）。
func BrewingIsContainerItem(itemID int32) bool {
	_, ok := brewingTableData().containerItems[itemID]
	return ok
}

// BrewingContainerMix 查找容器配方：输入瓶子物品 + 材料 → 目标瓶子物品
// （药水种类保留）。未命中返回 (0, false)。
func BrewingContainerMix(inputItem, ingredientItem int32) (int32, bool) {
	to, ok := brewingTableData().containerMixes[brewMixKey(inputItem, ingredientItem)]
	return to, ok
}

// BrewingPotionMix 查找药水配方：输入药水 + 材料 → 目标药水。
// 未命中返回 (0, false)。
func BrewingPotionMix(inputPotion, ingredientItem int32) (int32, bool) {
	to, ok := brewingTableData().potionMixes[brewMixKey(inputPotion, ingredientItem)]
	return to, ok
}

// BrewingHasMix 报告（瓶子物品 + 药水 + 材料）是否存在任一种酿造配方
// （原版 PotionBrewing.hasMix，材料顺序）。
func BrewingHasMix(inputItem, inputPotion, ingredientItem int32) bool {
	if _, ok := brewingTableData().containerMixes[brewMixKey(inputItem, ingredientItem)]; ok {
		return true
	}
	_, ok := brewingTableData().potionMixes[brewMixKey(inputPotion, ingredientItem)]
	return ok
}
