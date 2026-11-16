//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"encoding/json"

	"gmcs/internal/registry"
)

type sculkPatchConfiguration struct {
	chargeCount      int
	amountPerCharge  int
	spreadAttempts   int
	spreadRounds     int
	growthRounds     int
	catalystChance   float64
	extraRareGrowths int
}

func parseSculkPatchConfiguration(raw json.RawMessage) (*sculkPatchConfiguration, error) {
	var encoded struct {
		ChargeCount      int             `json:"charge_count"`
		AmountPerCharge  int             `json:"amount_per_charge"`
		SpreadAttempts   int             `json:"spread_attempts"`
		SpreadRounds     int             `json:"spread_rounds"`
		GrowthRounds     int             `json:"growth_rounds"`
		CatalystChance   float64         `json:"catalyst_chance"`
		ExtraRareGrowths json.RawMessage `json:"extra_rare_growths"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, err
	}
	config := &sculkPatchConfiguration{
		chargeCount:     encoded.ChargeCount,
		amountPerCharge: encoded.AmountPerCharge,
		spreadAttempts:  encoded.SpreadAttempts,
		spreadRounds:    encoded.SpreadRounds,
		growthRounds:    encoded.GrowthRounds,
		catalystChance:  encoded.CatalystChance,
	}
	if config.chargeCount <= 0 {
		config.chargeCount = 10
	}
	if config.amountPerCharge <= 0 {
		config.amountPerCharge = 32
	}
	if config.spreadAttempts <= 0 {
		config.spreadAttempts = 64
	}
	if config.spreadRounds <= 0 {
		config.spreadRounds = 1
	}
	var constant int
	if json.Unmarshal(encoded.ExtraRareGrowths, &constant) == nil {
		config.extraRareGrowths = constant
	} else {
		var provider intProvider
		if json.Unmarshal(encoded.ExtraRareGrowths, &provider) == nil {
			config.extraRareGrowths = provider.sample(newLegacyRandom(0))
		}
	}
	return config, nil
}

func sculkReplaceable(block SurfaceBlock) bool {
	if block.IsAir() {
		return false
	}
	state, err := registry.BlockStateID(block.Name)
	return err == nil && registry.BlockTagContains("minecraft:sculk_replaceable_world_gen", state)
}

// sculkCanSpreadFrom 等价 SculkPatchFeature.canSpreadFrom：起点必须是
// SculkBehaviour 方块（这里只识别 sculk 系列），或者空气/水源且周围存在
// 完整碰撞方块。否则这个随机高度点只是地表上方的空气，不能凭空生成 sculk。
func (r *Router) sculkCanSpreadFrom(chunk *GeneratedChunk, x, y, z int) bool {
	if !chunk.InRange(x, y, z) {
		return false
	}
	block := chunk.BlockAt(x, y, z)
	switch block.Name {
	case "minecraft:sculk", "minecraft:sculk_vein", "minecraft:sculk_catalyst",
		"minecraft:sculk_sensor", "minecraft:sculk_shrieker":
		return true
	}
	waterSource := block.Name == "minecraft:water" && block.Properties["level"] == "0"
	if !block.IsAir() && !waterSource {
		return false
	}
	for _, direction := range [][3]int{{0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0}, {0, -1, 0}, {0, 1, 0}} {
		if isSolidBlock(chunk.BlockAt(x+direction[0], y+direction[1], z+direction[2])) {
			return true
		}
	}
	return false
}

func (r *Router) placeSculkPatch(chunk *GeneratedChunk, random *legacyRandom, raw json.RawMessage, x, y, z int) {
	config, err := parseSculkPatchConfiguration(raw)
	if err != nil {
		return
	}
	if !r.sculkCanSpreadFrom(chunk, x, y, z) {
		return
	}
	type sculkCursor struct{ x, y, z int }
	cursors := make([]sculkCursor, 0, config.chargeCount)
	for charge := 0; charge < config.chargeCount; charge++ {
		cursor := sculkCursor{x: x, y: y, z: z}
		for step := 0; step < config.amountPerCharge; step++ {
			if chunk.InRange(cursor.x, cursor.y, cursor.z) && sculkReplaceable(chunk.BlockAt(cursor.x, cursor.y, cursor.z)) {
				chunk.SetBlock(cursor.x, cursor.y, cursor.z, SurfaceBlock{Name: "minecraft:sculk"})
			}
			switch random.nextIntBounded(6) {
			case 0:
				cursor.x++
			case 1:
				cursor.x--
			case 2:
				cursor.y++
			case 3:
				cursor.y--
			case 4:
				cursor.z++
			default:
				cursor.z--
			}
		}
		cursors = append(cursors, cursor)
	}
	for round := 0; round < config.spreadRounds; round++ {
		for attempt := 0; attempt < config.spreadAttempts; attempt++ {
			base := cursors[random.nextIntBounded(len(cursors))]
			px := base.x + random.nextIntBounded(9) - 4
			py := base.y + random.nextIntBounded(9) - 4
			pz := base.z + random.nextIntBounded(9) - 4
			if chunk.InRange(px, py, pz) && sculkReplaceable(chunk.BlockAt(px, py, pz)) {
				chunk.SetBlock(px, py, pz, SurfaceBlock{Name: "minecraft:sculk"})
			}
		}
	}
	if random.nextDouble() < config.catalystChance && chunk.InRange(x, y-1, z) &&
		isSolidBlock(chunk.BlockAt(x, y-1, z)) {
		chunk.SetBlock(x, y, z, SurfaceBlock{Name: "minecraft:sculk_catalyst"})
	}
	for growth := 0; growth < config.extraRareGrowths; growth++ {
		px := x + random.nextIntBounded(5) - 2
		pz := z + random.nextIntBounded(5) - 2
		if !chunk.InRange(px, y, pz) {
			continue
		}
		if !chunk.BlockAt(px, y, pz).IsAir() || !isSolidBlock(chunk.BlockAt(px, y-1, pz)) {
			continue
		}
		chunk.SetBlock(px, y, pz, SurfaceBlock{Name: "minecraft:sculk_shrieker",
			Properties: map[string]string{"can_summon": "true", "waterlogged": "false"}})
	}
}
