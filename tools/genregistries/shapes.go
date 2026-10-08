package main

import (
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// 方块碰撞形状生成：把 reports/blocks.json 的每个方块状态映射为碰撞盒列表，
// 生成 internal/registry/block_shapes_generated.go。
//
// 形状常量来源（Minecraft 1.21.11）：
//   - SlabBlock/SnowLayerBlock/CactusBlock/CakeBlock/FarmBlock/DirtPathBlock/
//     BedBlock/BasePressurePlateBlock/ChestBlock/CampfireBlock/StonecutterBlock/
//     BrewingStandBlock/LecternBlock/LanternBlock/BellBlock/FenceBlock/WallBlock/
//     FenceGateBlock/DoorBlock/TrapDoorBlock/StairBlock 的字节码常量；
//   - 栅栏/墙“碰撞 1.5 格高、轮廓 1 格高”与 wiki 行为一致。
//
// 少数装饰性方块（花盆、蜡烛、蘑菇、灯笼挂链、末地烛等）未逐一核对原版形状，
// 按“无碰撞”或略小的近似盒处理，避免服务器碰撞盒大于客户端导致移动回拉；
// 近似项在 switch 中以“近似”注释标出。
//
// 盒子单位为 1/16 格（坐标 0–16，栅栏/墙的 y 可达 24）。

// shapeBox 是 1/16 单位的碰撞盒。
type shapeBox struct{ x1, y1, z1, x2, y2, z2 uint8 }

// stateShapeData 是一个方块状态及其属性。
type stateShapeData struct {
	id    uint16
	props map[string]string
}

// blockShapeData 是一个方块的定义类型与全部状态。
type blockShapeData struct {
	defType string
	states  []stateShapeData
}

// loadBlockShapeData 解析 blocks.json 的定义类型与状态属性。
func loadBlockShapeData(path string) (map[string]blockShapeData, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file map[string]struct {
		Definition struct {
			Type string `json:"type"`
		} `json:"definition"`
		States []struct {
			ID         uint16            `json:"id"`
			Properties map[string]string `json:"properties"`
		} `json:"states"`
	}
	if err := json.Unmarshal(content, &file); err != nil {
		return nil, err
	}
	result := make(map[string]blockShapeData, len(file))
	for name, block := range file {
		data := blockShapeData{defType: block.Definition.Type}
		for _, state := range block.States {
			data.states = append(data.states, stateShapeData{id: state.ID, props: state.Properties})
		}
		result[name] = data
	}
	return result, nil
}

// 方块定义类型 → 碰撞行为的分类（blocks.json 的 definition.type）。
// 未分类的类型会让生成失败，防止版本更新后形状表悄悄失准。
var (
	emptyTypes = map[string]bool{
		"minecraft:air": true, "minecraft:liquid": true,
		"minecraft:fire": true, "minecraft:soul_fire": true,
		"minecraft:light": true, "minecraft:structure_void": true,
		"minecraft:nether_portal": true, "minecraft:end_portal": true,
		"minecraft:end_gateway": true, "minecraft:bubble_column": true,
		// 火把与红石火把
		"minecraft:torch": true, "minecraft:wall_torch": true,
		"minecraft:redstone_torch": true, "minecraft:redstone_wall_torch": true,
		// 花草植物
		"minecraft:flower": true, "minecraft:tall_flower": true,
		"minecraft:double_plant": true, "minecraft:tall_grass": true,
		"minecraft:short_dry_grass": true, "minecraft:tall_dry_grass": true,
		"minecraft:dry_vegetation": true, "minecraft:flower_bed": true,
		"minecraft:leaf_litter": true, "minecraft:hanging_moss": true,
		"minecraft:hanging_roots": true, "minecraft:eyeblossom": true,
		"minecraft:mangrove_propagule": true, "minecraft:spore_blossom": true,
		"minecraft:firefly_bush": true, "minecraft:bush": true,
		"minecraft:cactus_flower": true, "minecraft:sapling": true,
		"minecraft:fungus": true, "minecraft:roots": true,
		"minecraft:nether_sprouts": true, "minecraft:bamboo_sapling": true,
		"minecraft:flower_pot": true, "minecraft:mushroom": true,
		"minecraft:azalea": true,
		// 作物
		"minecraft:crop": true, "minecraft:beetroot": true,
		"minecraft:carrot": true, "minecraft:potato": true,
		"minecraft:nether_wart": true, "minecraft:torchflower_crop": true,
		"minecraft:pitcher_crop": true, "minecraft:stem": true,
		"minecraft:attached_stem": true, "minecraft:cocoa": true,
		"minecraft:bamboo_stalk": true, "minecraft:sugar_cane": true,
		// 藤蔓/水草
		"minecraft:cave_vines": true, "minecraft:cave_vines_plant": true,
		"minecraft:twisting_vines": true, "minecraft:twisting_vines_plant": true,
		"minecraft:weeping_vines": true, "minecraft:weeping_vines_plant": true,
		"minecraft:kelp": true, "minecraft:kelp_plant": true,
		"minecraft:seagrass": true, "minecraft:tall_seagrass": true,
		"minecraft:vine": true, "minecraft:glow_lichen": true,
		"minecraft:sculk_vein": true, "minecraft:multiface": true,
		// 告示牌/旗帜/头颅
		"minecraft:banner": true, "minecraft:wall_banner": true,
		"minecraft:standing_sign": true, "minecraft:wall_sign": true,
		"minecraft:ceiling_hanging_sign": true, "minecraft:wall_hanging_sign": true,
		"minecraft:skull": true, "minecraft:wall_skull": true,
		"minecraft:player_head": true, "minecraft:player_wall_head": true,
		"minecraft:piglinwallskull": true, "minecraft:wither_skull": true,
		"minecraft:wither_wall_skull": true,
		// 红石元件（非完整方块部分）
		"minecraft:rail": true, "minecraft:powered_rail": true,
		"minecraft:detector_rail": true, "minecraft:redstone_wire": true,
		"minecraft:tripwire": true, "minecraft:trip_wire_hook": true,
		"minecraft:lever": true, "minecraft:comparator": true,
		"minecraft:repeater": true,
		// 其它无碰撞
		"minecraft:web": true, "minecraft:powder_snow": true,
		"minecraft:scaffolding": true, "minecraft:ladder": true,
		"minecraft:moving_piston": true, "minecraft:piston_head": true,
		"minecraft:amethyst_cluster": true, "minecraft:end_rod": true,
		"minecraft:sea_pickle": true, "minecraft:turtle_egg": true,
		"minecraft:sniffer_egg": true, "minecraft:frogspawn": true,
		"minecraft:dried_ghast": true, "minecraft:pointed_dripstone": true,
		"minecraft:big_dripleaf": true, "minecraft:big_dripleaf_stem": true,
		"minecraft:small_dripleaf": true, "minecraft:chorus_plant": true,
		"minecraft:chorus_flower": true, "minecraft:shelf": true,
		"minecraft:copper_golem_statue":            true,
		"minecraft:weathering_copper_golem_statue": true,
		"minecraft:weathering_copper_grate":        true,
		"minecraft:waterlogged_transparent":        true,
		"minecraft:sculk_sensor":                   true, "minecraft:sculk_shrieker": true,
		"minecraft:sculk_catalyst": true, "minecraft:calibrated_sculk_sensor": true,
		"minecraft:wither_rose": true,
		"minecraft:conduit":     true, "minecraft:lightning_rod": true,
		"minecraft:weathering_lightning_rod": true,
		"minecraft:base_coral_fan":           true, "minecraft:base_coral_plant": true,
		"minecraft:base_coral_wall_fan": true, "minecraft:coral_fan": true,
		"minecraft:coral_plant": true, "minecraft:coral_wall_fan": true,
	}

	fullTypes = map[string]bool{
		"minecraft:block": true, "minecraft:rotated_pillar": true,
		"minecraft:drop_experience": true, "minecraft:weathering_copper_full": true,
		"minecraft:infested": true, "minecraft:infested_rotated_pillar": true,
		"minecraft:huge_mushroom": true, "minecraft:nylium": true,
		"minecraft:sand": true, "minecraft:colored_falling": true,
		"minecraft:concrete_powder": true, "minecraft:glazed_terracotta": true,
		"minecraft:stained_glass": true, "minecraft:transparent": true,
		"minecraft:tinted_glass": true, "minecraft:half_transparent": true,
		"minecraft:ice": true, "minecraft:frosted_ice": true,
		"minecraft:sculk": true, "minecraft:magma": true, "minecraft:mud": true,
		"minecraft:mycelium": true, "minecraft:grass": true,
		"minecraft:snowy_dirt": true, "minecraft:rooted_dirt": true,
		"minecraft:netherrack": true, "minecraft:powered": true,
		"minecraft:hay": true, "minecraft:sponge": true, "minecraft:wet_sponge": true,
		"minecraft:slime": true, "minecraft:brushable": true,
		"minecraft:budding_amethyst": true, "minecraft:amethyst": true,
		"minecraft:crying_obsidian": true, "minecraft:barrier": true,
		"minecraft:barrel": true, "minecraft:beehive": true,
		"minecraft:blast_furnace": true, "minecraft:command": true,
		"minecraft:crafter": true, "minecraft:crafting_table": true,
		"minecraft:cartography_table": true, "minecraft:loom": true,
		"minecraft:smithing_table": true, "minecraft:dispenser": true,
		"minecraft:dropper": true, "minecraft:furnace": true,
		"minecraft:smoker": true, "minecraft:jukebox": true,
		"minecraft:note": true, "minecraft:target": true, "minecraft:tnt": true,
		"minecraft:pumpkin": true, "minecraft:jack_o_lantern": true,
		"minecraft:shulker_box": true, "minecraft:observer": true,
		"minecraft:spawner": true, "minecraft:vault": true,
		"minecraft:trial_spawner": true, "minecraft:structure": true,
		"minecraft:test": true, "minecraft:test_instance": true,
		"minecraft:jigsaw": true, "minecraft:redstone_lamp": true,
		"minecraft:chiseled_book_shelf": true, "minecraft:creaking_heart": true,
		"minecraft:bonemealable_feature_placer": true,
		"minecraft:weathering_copper_bulb":      true, "minecraft:copper_bulb_block": true,
		"minecraft:piston_base": true, "minecraft:mangrove_leaves": true,
		"minecraft:untinted_particle_leaves": true,
		"minecraft:tinted_particle_leaves":   true, "minecraft:coral": true,
		"minecraft:redstone_ore": true, "minecraft:beacon": true,
	}
)

// stateShape 计算单个方块状态的碰撞盒（1/16 单位）。
func stateShape(defType, name string, props map[string]string) ([]shapeBox, error) {
	// 特殊形状优先按定义类型分派。
	switch defType {
	case "minecraft:slab", "minecraft:weathering_copper_slab":
		return slabBoxes(props), nil
	case "minecraft:stair", "minecraft:weathering_copper_stair":
		return stairBoxes(props), nil
	case "minecraft:fence":
		return fenceBoxes(props), nil
	case "minecraft:wall":
		return wallBoxes(props), nil
	case "minecraft:fence_gate":
		return gateBoxes(props), nil
	case "minecraft:door", "minecraft:weathering_copper_door":
		return doorBoxes(props), nil
	case "minecraft:trapdoor", "minecraft:weathering_copper_trap_door":
		return trapdoorBoxes(props), nil
	case "minecraft:snow_layer":
		return []shapeBox{{0, 0, 0, 16, uint8(2 * atoi(props["layers"])), 16}}, nil
	case "minecraft:cake":
		return cakeBoxes(atoi(props["bites"])), nil
	case "minecraft:candle_cake":
		return cakeBoxes(0), nil
	case "minecraft:cactus":
		return []shapeBox{{1, 0, 1, 15, 15, 15}}, nil
	case "minecraft:farm", "minecraft:dirt_path":
		return []shapeBox{{0, 0, 0, 16, 15, 16}}, nil
	case "minecraft:carpet", "minecraft:wool_carpet", "minecraft:mossy_carpet":
		return []shapeBox{{0, 0, 0, 16, 1, 16}}, nil
	case "minecraft:pressure_plate", "minecraft:weighted_pressure_plate":
		return []shapeBox{{1, 0, 1, 15, 1, 15}}, nil
	case "minecraft:button":
		return buttonBoxes(props), nil
	case "minecraft:bed":
		return bedBoxes(props), nil
	case "minecraft:chest", "minecraft:trapped_chest", "minecraft:ender_chest",
		"minecraft:copper_chest", "minecraft:weathering_copper_chest":
		return []shapeBox{{1, 0, 1, 15, 14, 15}}, nil
	case "minecraft:sweet_berry_bush":
		switch atoi(props["age"]) {
		case 0, 1:
			return nil, nil
		case 2:
			return []shapeBox{{1, 0, 1, 15, 8, 15}}, nil
		default:
			return []shapeBox{{1, 0, 1, 15, 14, 15}}, nil
		}
	case "minecraft:iron_bars", "minecraft:stained_glass_pane", "minecraft:weathering_copper_bar":
		return paneBoxes(props), nil
	case "minecraft:campfire":
		return []shapeBox{{0, 0, 0, 16, 7, 16}}, nil
	case "minecraft:stonecutter":
		return []shapeBox{{0, 0, 0, 16, 9, 16}}, nil
	case "minecraft:brewing_stand":
		return []shapeBox{{1, 0, 1, 15, 2, 15}, {7, 2, 7, 9, 14, 9}}, nil
	case "minecraft:lectern":
		return []shapeBox{{0, 0, 0, 16, 2, 16}, {4, 2, 4, 12, 14, 12}}, nil
	case "minecraft:lantern", "minecraft:weathering_lantern":
		if props["hanging"] == "true" {
			return []shapeBox{{5, 9, 5, 11, 16, 11}, {6, 7, 6, 10, 9, 10}}, nil
		}
		return []shapeBox{{5, 0, 5, 11, 7, 11}, {6, 7, 6, 10, 9, 10}}, nil
	case "minecraft:bell":
		return []shapeBox{{4, 4, 4, 12, 6, 12}, {5, 6, 5, 11, 13, 11}}, nil
	case "minecraft:cauldron", "minecraft:lava_cauldron", "minecraft:layered_cauldron":
		// 近似：底 4 + 四壁 2 厚。
		return []shapeBox{
			{0, 0, 0, 16, 4, 16},
			{0, 4, 0, 16, 16, 2}, {0, 4, 14, 16, 16, 16},
			{0, 4, 2, 2, 16, 14}, {14, 4, 2, 16, 16, 14},
		}, nil
	case "minecraft:composter":
		// 近似：底 2 + 四壁 2 厚至 12 高。
		return []shapeBox{
			{0, 0, 0, 16, 2, 16},
			{0, 2, 0, 16, 12, 2}, {0, 2, 14, 16, 12, 16},
			{0, 2, 2, 2, 12, 14}, {14, 2, 2, 16, 12, 14},
		}, nil
	case "minecraft:hopper":
		// 近似：底 6 + 上口 4 宽。
		return []shapeBox{{0, 0, 0, 16, 6, 16}, {4, 6, 4, 12, 10, 12}}, nil
	case "minecraft:enchantment_table":
		return []shapeBox{{0, 0, 0, 16, 12, 16}}, nil
	case "minecraft:grindstone":
		// 近似：底座 + 磨轮。
		return []shapeBox{{2, 0, 2, 14, 7, 14}, {4, 7, 4, 12, 13, 12}}, nil
	case "minecraft:daylight_detector":
		return []shapeBox{{0, 0, 0, 16, 6, 16}}, nil
	case "minecraft:soul_sand", "minecraft:honey", "minecraft:mangrove_roots":
		return []shapeBox{{0, 0, 0, 16, 15, 16}}, nil
	case "minecraft:chain", "minecraft:weathering_copper_chain":
		return []shapeBox{{7, 0, 7, 9, 16, 9}}, nil
	case "minecraft:decorated_pot":
		return []shapeBox{{1, 0, 1, 15, 16, 15}}, nil
	case "minecraft:heavy_core":
		return []shapeBox{{0, 0, 0, 16, 9, 16}}, nil
	case "minecraft:dragon_egg":
		return []shapeBox{{1, 0, 1, 15, 16, 15}}, nil
	case "minecraft:respawn_anchor":
		return []shapeBox{{0, 0, 0, 16, 12, 16}}, nil
	case "minecraft:end_portal_frame":
		return []shapeBox{{0, 0, 0, 16, 15, 16}}, nil
	case "minecraft:candle":
		// 近似：忽略蜡烛数量，取单支蜡烛的盒。
		return []shapeBox{{6, 0, 6, 10, 6, 10}}, nil
	case "minecraft:anvil":
		// 近似：底座 + 柱 + 顶板。
		return []shapeBox{
			{2, 0, 2, 14, 4, 14}, {5, 4, 5, 11, 10, 11}, {2, 10, 2, 14, 14, 14},
		}, nil
	case "minecraft:waterlily":
		return []shapeBox{{1, 0, 1, 15, 1, 15}}, nil
	}
	if emptyTypes[defType] {
		return nil, nil
	}
	if fullTypes[defType] {
		return []shapeBox{{0, 0, 0, 16, 16, 16}}, nil
	}
	return nil, fmt.Errorf("方块 %s 的定义类型 %s 未分类", name, defType)
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// rotateY 把盒子绕方块中心顺时针（俯视）旋转 90°×times。
func rotateY(b shapeBox, times int) shapeBox {
	for i := 0; i < times%4; i++ {
		b = shapeBox{16 - b.z2, b.y1, b.x1, 16 - b.z1, b.y2, b.x2}
	}
	return b
}

// mirrorY 把盒子沿 y=8 平面翻转（楼梯顶半）。
func mirrorY(b shapeBox) shapeBox {
	return shapeBox{b.x1, 16 - b.y2, b.z1, b.x2, 16 - b.y1, b.z2}
}

// dirRot 返回水平朝向相对 north 的顺时针旋转次数（north=0、east=1、south=2、west=3）。
func dirRot(facing string) int {
	switch facing {
	case "east":
		return 1
	case "south":
		return 2
	case "west":
		return 3
	}
	return 0
}

func slabBoxes(props map[string]string) []shapeBox {
	switch props["type"] {
	case "top":
		return []shapeBox{{0, 8, 0, 16, 16, 16}}
	case "double":
		return []shapeBox{{0, 0, 0, 16, 16, 16}}
	default: // bottom
		return []shapeBox{{0, 0, 0, 16, 8, 16}}
	}
}

// stairBoxes 计算楼梯碰撞盒（朝向 = 上行方向；角落朝向为约定，见 docs/TODO.md）。
func stairBoxes(props map[string]string) []shapeBox {
	rot := dirRot(props["facing"])
	var boxes []shapeBox
	if props["half"] == "top" {
		boxes = append(boxes, mirrorY(shapeBox{0, 0, 0, 16, 8, 16}))
	} else {
		boxes = append(boxes, shapeBox{0, 0, 0, 16, 8, 16})
	}
	// 顶部台阶（以 facing=north 为基准，沿 -z 上行）。
	var tops []shapeBox
	switch props["shape"] {
	case "outer_left":
		tops = []shapeBox{{0, 8, 0, 8, 16, 8}}
	case "outer_right":
		tops = []shapeBox{{8, 8, 0, 16, 16, 8}}
	case "inner_left":
		tops = []shapeBox{{0, 8, 0, 16, 16, 8}, {0, 8, 0, 8, 16, 16}}
	case "inner_right":
		tops = []shapeBox{{0, 8, 0, 16, 16, 8}, {8, 8, 0, 16, 16, 16}}
	default: // straight
		tops = []shapeBox{{0, 8, 0, 16, 16, 8}}
	}
	for _, t := range tops {
		t = rotateY(t, rot)
		if props["half"] == "top" {
			t = mirrorY(t)
		}
		boxes = append(boxes, t)
	}
	return boxes
}

// fenceBoxes 计算栅栏碰撞盒（柱 4×24×4 + 连接臂 4 厚 24 高）。
func fenceBoxes(props map[string]string) []shapeBox {
	boxes := []shapeBox{{6, 0, 6, 10, 24, 10}}
	arm := shapeBox{6, 0, 0, 10, 24, 8} // north
	for i, side := range []string{"north", "east", "south", "west"} {
		if props[side] == "true" {
			boxes = append(boxes, rotateY(arm, i))
		}
	}
	return boxes
}

// wallBoxes 计算墙碰撞盒（柱 8×24×8 + 连接臂 6 厚 24 高；low/tall 高度相同）。
func wallBoxes(props map[string]string) []shapeBox {
	var boxes []shapeBox
	if props["up"] != "false" {
		boxes = append(boxes, shapeBox{4, 0, 4, 12, 24, 12})
	}
	arm := shapeBox{5, 0, 0, 11, 24, 11} // north
	for i, side := range []string{"north", "east", "south", "west"} {
		if props[side] != "none" {
			boxes = append(boxes, rotateY(arm, i))
		}
	}
	return boxes
}

// gateBoxes 计算栅栏门碰撞盒（关闭时 4 厚 24 高，打开时无碰撞）。
func gateBoxes(props map[string]string) []shapeBox {
	if props["open"] == "true" {
		return nil
	}
	if props["facing"] == "east" || props["facing"] == "west" {
		return []shapeBox{{6, 0, 0, 10, 24, 16}}
	}
	return []shapeBox{{0, 0, 6, 16, 24, 10}}
}

// panelAt 返回贴在 facing 边的 3/16 厚门板/活板门竖面板板。
func panelAt(facing string) shapeBox {
	switch facing {
	case "south":
		return shapeBox{0, 0, 13, 16, 16, 16}
	case "west":
		return shapeBox{0, 0, 0, 3, 16, 16}
	case "east":
		return shapeBox{13, 0, 0, 16, 16, 16}
	default: // north
		return shapeBox{0, 0, 0, 16, 16, 3}
	}
}

// rotateFacing 把朝向旋转一次（dir=1 顺时针、-1 逆时针，俯视）。
func rotateFacing(facing string, dir int) string {
	order := []string{"north", "east", "south", "west"}
	for i, f := range order {
		if f == facing {
			return order[(i+dir+4)%4]
		}
	}
	return facing
}

// doorBoxes 计算门碰撞盒。约定：关闭时门板贴在 facing 边；
// 打开时门板转到铰链侧（hinge=left 逆时针、right 顺时针）。见 docs/TODO.md。
func doorBoxes(props map[string]string) []shapeBox {
	facing := props["facing"]
	if props["open"] == "true" {
		if props["hinge"] == "left" {
			facing = rotateFacing(facing, -1)
		} else {
			facing = rotateFacing(facing, 1)
		}
	}
	return []shapeBox{panelAt(facing)}
}

// trapdoorBoxes 计算活板门碰撞盒：关闭时为水平板（贴上/下边），
// 打开时为贴在 facing 边的竖面板。
func trapdoorBoxes(props map[string]string) []shapeBox {
	if props["open"] == "true" {
		return []shapeBox{panelAt(props["facing"])}
	}
	if props["half"] == "top" {
		return []shapeBox{{0, 13, 0, 16, 16, 16}}
	}
	return []shapeBox{{0, 0, 0, 16, 3, 16}}
}

func cakeBoxes(bites int) []shapeBox {
	x1 := uint8(1 + 2*bites)
	return []shapeBox{{x1, 0, 1, 15, 8, 15}}
}

// buttonBoxes 计算按钮碰撞盒（近似值，与原版可能有 1–2 像素差异）。
func buttonBoxes(props map[string]string) []shapeBox {
	switch props["face"] {
	case "floor":
		return []shapeBox{{6, 0, 5, 10, 4, 11}}
	case "ceiling":
		return []shapeBox{{6, 12, 5, 10, 16, 11}}
	default: // wall
		return []shapeBox{rotateY(shapeBox{6, 5, 14, 10, 11, 16}, dirRot(props["facing"]))}
	}
}

// bedBoxes 计算床碰撞盒：床垫 3–9 高 + 两条对角腿（朝向旋转）。
func bedBoxes(props map[string]string) []shapeBox {
	rot := dirRot(props["facing"])
	return []shapeBox{
		{0, 3, 0, 16, 9, 16},
		rotateY(shapeBox{0, 0, 0, 3, 3, 3}, rot),
		rotateY(shapeBox{13, 0, 0, 16, 3, 3}, rot),
	}
}

// paneBoxes 计算玻璃板/铁栏杆碰撞盒（柱 2×16×2 + 连接臂 2 厚）。
func paneBoxes(props map[string]string) []shapeBox {
	boxes := []shapeBox{{7, 0, 7, 9, 16, 9}}
	arm := shapeBox{7, 0, 0, 9, 16, 8} // north
	for i, side := range []string{"north", "east", "south", "west"} {
		if props[side] == "true" {
			boxes = append(boxes, rotateY(arm, i))
		}
	}
	return boxes
}

// writeBlockShapes 生成 block_shapes_generated.go。
func writeBlockShapes(outDir, version string, blocks map[string]blockShapeData) error {
	names := make([]string, 0, len(blocks))
	for name := range blocks {
		names = append(names, name)
	}
	sort.Strings(names)

	maxState := 0
	for _, block := range blocks {
		for _, state := range block.states {
			if int(state.id) > maxState {
				maxState = int(state.id)
			}
		}
	}

	// 形状去重：key 为盒列表的序列化。
	type span struct{ start, count int }
	shapeIDs := map[string]uint16{"": 0}
	spans := []span{{0, 0}}
	boxes := []shapeBox{}
	stateShapes := make([]uint16, maxState+1)
	unknown := map[string]string{}

	for _, name := range names {
		block := blocks[name]
		for _, state := range block.states {
			shape, err := stateShape(block.defType, name, state.props)
			if err != nil {
				if strings.Contains(err.Error(), "未分类") {
					unknown[block.defType] = name
					continue
				}
				return err
			}
			key := shapeKey(shape)
			id, ok := shapeIDs[key]
			if !ok {
				id = uint16(len(spans))
				shapeIDs[key] = id
				spans = append(spans, span{len(boxes), len(shape)})
				boxes = append(boxes, shape...)
			}
			stateShapes[state.id] = id
		}
	}
	if len(unknown) > 0 {
		types := make([]string, 0, len(unknown))
		for defType := range unknown {
			types = append(types, fmt.Sprintf("%s（例：%s）", defType, unknown[defType]))
		}
		sort.Strings(types)
		return fmt.Errorf("以下定义类型未分类：%s", strings.Join(types, "、"))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "// Code generated by tools/genregistries from Minecraft %s reports/blocks.json; DO NOT EDIT.\n", version)
	fmt.Fprintf(&b, "//\n")
	fmt.Fprintf(&b, "// 方块状态 → 碰撞盒表（1/16 单位；坐标 0–16，栅栏/墙 y 可达 24）。\n")
	fmt.Fprintf(&b, "// 形状常量取自原版方块类字节码（见 tools/genregistries/shapes.go 注释）；\n")
	fmt.Fprintf(&b, "// 少数装饰性方块为近似或按无碰撞处理，见 docs/TODO.md 已知限制。\n\n")
	fmt.Fprintf(&b, "package registry\n\n")

	fmt.Fprintf(&b, "// ShapeBox 是 1/16 单位的碰撞盒。\ntype ShapeBox struct {\n\tX1, Y1, Z1, X2, Y2, Z2 uint8\n}\n\n")
	fmt.Fprintf(&b, "// blockShapeBoxes 扁平保存全部形状的碰撞盒。\nvar blockShapeBoxes = [...]ShapeBox{\n")
	for _, box := range boxes {
		fmt.Fprintf(&b, "\t{%d, %d, %d, %d, %d, %d},\n", box.x1, box.y1, box.z1, box.x2, box.y2, box.z2)
	}
	fmt.Fprintf(&b, "}\n\n")

	fmt.Fprintf(&b, "// blockShapeSpans[i] 是形状 i 在 blockShapeBoxes 中的 [start, end)。\nvar blockShapeSpans = [...][2]uint16{\n")
	for _, s := range spans {
		fmt.Fprintf(&b, "\t{%d, %d},\n", s.start, s.start+s.count)
	}
	fmt.Fprintf(&b, "}\n\n")

	fmt.Fprintf(&b, "// blockStateShapes 是方块状态 ID → 形状 ID（0 = 无碰撞）。\nvar blockStateShapes = [...]uint16{\n")
	for i := 0; i < len(stateShapes); i += 16 {
		end := i + 16
		if end > len(stateShapes) {
			end = len(stateShapes)
		}
		parts := make([]string, 0, 16)
		for _, v := range stateShapes[i:end] {
			parts = append(parts, strconv.Itoa(int(v)))
		}
		fmt.Fprintf(&b, "\t%s,\n", strings.Join(parts, ", "))
	}
	fmt.Fprintf(&b, "}\n\n")

	fmt.Fprintf(&b, `// BlockStateShape 返回方块状态的碰撞盒（1/16 单位）；无碰撞时返回空切片。
// 返回的切片引用全局表，调用方不得修改。
func BlockStateShape(state uint16) []ShapeBox {
	if int(state) >= len(blockStateShapes) {
		return nil
	}
	id := blockStateShapes[state]
	span := blockShapeSpans[id]
	return blockShapeBoxes[span[0]:span[1]]
}

// BlockStateCollides 报告方块状态是否有碰撞形状。
func BlockStateCollides(state uint16) bool {
	return int(state) < len(blockStateShapes) && blockStateShapes[state] != 0
}
`)

	formatted, err := format.Source([]byte(b.String()))
	if err != nil {
		return fmt.Errorf("格式化生成代码: %w", err)
	}
	return os.WriteFile(filepath.Join(outDir, "block_shapes_generated.go"), formatted, 0o644)
}

// shapeKey 把盒列表序列化为去重键。
func shapeKey(shape []shapeBox) string {
	if len(shape) == 0 {
		return ""
	}
	var b strings.Builder
	for _, box := range shape {
		fmt.Fprintf(&b, "%d,%d,%d,%d,%d,%d;", box.x1, box.y1, box.z1, box.x2, box.y2, box.z2)
	}
	return b.String()
}
