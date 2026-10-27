//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package main

import (
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// 方块放置形态生成：为“放置时需要按朝向/形态选择状态”的方块生成最小状态子集。
//
// 与 block_states_generated.go 不同，这里不保存全部属性（台阶每种有 80 个状态，
// 全部展开会显著增加启动内存），只保存放置会用到的组合：
//   - 台阶：half × facing（shape=straight、waterlogged=false）；
//   - 半砖：type（bottom/top/double，waterlogged=false）；
//   - 原木/柱状方块：axis；
//   - 朝向方块：facing（水平 4 向或全向 6 向）；
//   - 活板门：half × facing（open/powered/waterlogged=false）；
//   - 墙式火把与梯子：facing（waterlogged=false）。
//
// 生成依据 reports/blocks.json 的属性组合；找不到匹配状态时生成器报错
// （版本升级后属性变化会立即暴露，而不是静默产出错误 ID）。
//
// 生成产物：internal/registry/block_placement_generated.go。

// placementFixedProps 是各类朝向方块在放置时的固定属性（其余属性取状态表
// 最低 ID 状态的基线值，waterlogged 一律取 false）。
var placementFixedProps = map[string]map[string]string{
	"minecraft:chest":         {"type": "single"},
	"minecraft:trapped_chest": {"type": "single"},
	"minecraft:furnace":       {"lit": "false"},
	"minecraft:blast_furnace": {"lit": "false"},
	"minecraft:smoker":        {"lit": "false"},
	"minecraft:barrel":        {"open": "false"},
	"minecraft:campfire":      {"lit": "true", "signal_fire": "false"},
	"minecraft:soul_campfire": {"lit": "true", "signal_fire": "false"},
	"minecraft:lectern":       {"has_book": "false", "powered": "false"},
	"minecraft:beehive":       {"honey_level": "0"},
	"minecraft:bee_nest":      {"honey_level": "0"},
}

// placementHorizontalBlocks 是使用水平四向 facing 的方块
// （原版 HorizontalDirectionalBlock 系）。
var placementHorizontalBlocks = map[string]bool{
	"minecraft:furnace":        true,
	"minecraft:blast_furnace":  true,
	"minecraft:smoker":         true,
	"minecraft:chest":          true,
	"minecraft:trapped_chest":  true,
	"minecraft:ender_chest":    true,
	"minecraft:carved_pumpkin": true,
	"minecraft:jack_o_lantern": true,
	"minecraft:stonecutter":    true,
	"minecraft:loom":           true,
	"minecraft:beehive":        true,
	"minecraft:bee_nest":       true,
	"minecraft:campfire":       true,
	"minecraft:soul_campfire":  true,
	"minecraft:lectern":        true,
}

// placementVerticalFacingBlocks 是使用全向六向 facing 的方块。
var placementVerticalFacingBlocks = map[string]bool{
	"minecraft:barrel": true,
}

// placementWallTorchBlocks 是墙式火把方块（由地面火把放置时按点击面转换）。
var placementWallTorchBlocks = map[string]bool{
	"minecraft:wall_torch":          true,
	"minecraft:soul_wall_torch":     true,
	"minecraft:redstone_wall_torch": true,
}

// placementAxisExcluded 是不参与轴朝向放置的方块（无对应物品，或属性不是
// 标准六向柱状：下界传送门只有 x/z 两个轴）。
var placementAxisExcluded = map[string]bool{
	"minecraft:nether_portal": true,
}

// placementFacingValues 是 facing 属性按方向索引的取值（0=down … 5=east）。
var placementFacingValues = []string{"down", "up", "north", "south", "west", "east"}

// placementHorizontalFacingValues 是水平四向的取值（north/south/west/east）。
var placementHorizontalFacingValues = []string{"north", "south", "west", "east"}

// placementStairsShapeValues 是台阶 shape 属性的取值，顺序与原版
// BlockState.Shape 枚举一致（0=straight、1=inner_left、2=inner_right、
// 3=outer_left、4=outer_right）。
var placementStairsShapeValues = []string{"straight", "inner_left", "inner_right", "outer_left", "outer_right"}

// writePlacementStates 生成放置形态状态表。
func writePlacementStates(outDir, version string, blocks map[string]blockShapeData) error {
	stairs := make(map[string][2][4]uint16)
	slabs := make(map[string][3]uint16)
	axis := make(map[string][3]uint16)
	facing := make(map[string][6]uint16)
	trapdoors := make(map[string][2][4]uint16)
	trapdoorsOpen := make(map[string][2][4]uint16)
	faceAttached := make(map[string][3][4]uint16)
	doors := make(map[string][2][4][2]uint16)
	doorsOpen := make(map[string][2][4][2]uint16)
	doorsPowered := make(map[string][2][4][2]uint16)
	trapdoorsPowered := make(map[string][2][4]uint16)
	gates := make(map[string][2][4]uint16)
	gatesOpen := make(map[string][2][4]uint16)
	gatesPowered := make(map[string][2][4]uint16)
	wallTorches := make(map[string][4]uint16)
	ladders := make(map[string][4]uint16)
	// leaves：树叶方块 → 玩家放置状态（persistent=true、distance=7、waterlogged=false）。
	leaves := make(map[string]uint16)
	// stairsShapes：台阶方块 → [half][facing][shape]，用于邻居形状更新。
	stairsShapes := make(map[string][2][4][5]uint16)
	// beds：床 → [facing][part]（part：0=foot、1=head，occupied=false）。
	beds := make(map[string][4][2]uint16)
	// standingSigns：立式告示牌 → [rotation]（0–15）。
	standingSigns := make(map[string][16]uint16)
	// wallSigns：墙式告示牌 → [facing]。
	wallSigns := make(map[string][4]uint16)

	names := make([]string, 0, len(blocks))
	for name := range blocks {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		data := blocks[name]
		if len(data.states) == 0 {
			continue
		}
		baseProps := data.states[0].props
		switch {
		case strings.HasSuffix(name, "_leaves"):
			// 玩家放置的树叶：原版 LeavesBlock.getStateForPlacement 把 PERSISTENT 置 true。
			id, err := placementStateID(data, map[string]string{
				"persistent": "true", "distance": "7", "waterlogged": "false",
			})
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			leaves[name] = id
		case strings.HasSuffix(name, "_stairs"):
			var table [2][4]uint16
			var shapeTable [2][4][5]uint16
			for half, halfValue := range []string{"bottom", "top"} {
				for facingIndex, facingValue := range placementHorizontalFacingValues {
					for shapeIndex, shapeValue := range placementStairsShapeValues {
						id, err := placementStateID(data, map[string]string{
							"half": halfValue, "facing": facingValue, "shape": shapeValue,
						})
						if err != nil {
							return fmt.Errorf("%s: %w", name, err)
						}
						shapeTable[half][facingIndex][shapeIndex] = id
					}
					table[half][facingIndex] = shapeTable[half][facingIndex][0]
				}
			}
			stairs[name] = table
			stairsShapes[name] = shapeTable
		case strings.HasSuffix(name, "_bed"):
			// 床：facing = 玩家水平朝向，part 由放置逻辑决定（foot/head）。
			var table [4][2]uint16
			for facingIndex, facingValue := range placementHorizontalFacingValues {
				for partIndex, partValue := range []string{"foot", "head"} {
					id, err := placementStateID(data, map[string]string{
						"facing": facingValue, "part": partValue, "occupied": "false",
					})
					if err != nil {
						return fmt.Errorf("%s: %w", name, err)
					}
					table[facingIndex][partIndex] = id
				}
			}
			beds[name] = table
		case strings.HasSuffix(name, "_wall_hanging_sign"), strings.HasSuffix(name, "_wall_sign"):
			// 墙式告示牌：facing = 被贴附面的方向。
			var table [4]uint16
			for facingIndex, facingValue := range placementHorizontalFacingValues {
				id, err := placementStateID(data, map[string]string{"facing": facingValue})
				if err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
				table[facingIndex] = id
			}
			wallSigns[name] = table
		case strings.HasSuffix(name, "_hanging_sign"), strings.HasSuffix(name, "_sign"):
			// 立式告示牌：rotation 0–15（每 22.5° 一档）。
			extraProps := map[string]string{}
			if _, ok := data.states[0].props["attached"]; ok {
				// 悬挂告示牌的 attached 表示是否连在上方方块上，
				// 独立放置时恒为 false。
				extraProps["attached"] = "false"
			}
			var table [16]uint16
			for rotation := 0; rotation < 16; rotation++ {
				props := make(map[string]string, len(extraProps)+1)
				for k, v := range extraProps {
					props[k] = v
				}
				props["rotation"] = strconv.Itoa(rotation)
				id, err := placementStateID(data, props)
				if err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
				table[rotation] = id
			}
			standingSigns[name] = table
		case strings.HasSuffix(name, "_slab"):
			var table [3]uint16
			for i, typeValue := range []string{"bottom", "top", "double"} {
				id, err := placementStateID(data, map[string]string{"type": typeValue})
				if err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
				table[i] = id
			}
			slabs[name] = table
		case strings.HasSuffix(name, "_door"):
			var table [2][4][2]uint16
			var openTable [2][4][2]uint16
			var poweredTable [2][4][2]uint16
			for half, halfValue := range []string{"lower", "upper"} {
				for facingIndex, facingValue := range placementHorizontalFacingValues {
					for hingeIndex, hingeValue := range []string{"left", "right"} {
						id, err := placementStateID(data, map[string]string{
							"half": halfValue, "facing": facingValue, "hinge": hingeValue,
							"open": "false", "powered": "false",
						})
						if err != nil {
							return fmt.Errorf("%s: %w", name, err)
						}
						table[half][facingIndex][hingeIndex] = id
						openID, err := placementStateID(data, map[string]string{
							"half": halfValue, "facing": facingValue, "hinge": hingeValue,
							"open": "true", "powered": "false",
						})
						if err != nil {
							return fmt.Errorf("%s: %w", name, err)
						}
						openTable[half][facingIndex][hingeIndex] = openID
						poweredID, err := placementStateID(data, map[string]string{
							"half": halfValue, "facing": facingValue, "hinge": hingeValue,
							"open": "true", "powered": "true",
						})
						if err != nil {
							return fmt.Errorf("%s: %w", name, err)
						}
						poweredTable[half][facingIndex][hingeIndex] = poweredID
					}
				}
			}
			doors[name] = table
			doorsOpen[name] = openTable
			doorsPowered[name] = poweredTable
		case strings.HasSuffix(name, "_fence_gate"):
			// in_wall：0=false、1=true（沿 facing 轴的两侧邻居是否为墙）。
			var table [2][4]uint16
			var openTable [2][4]uint16
			var poweredTable [2][4]uint16
			for inWall, inWallValue := range []string{"false", "true"} {
				for facingIndex, facingValue := range placementHorizontalFacingValues {
					for variant, target := range []map[string]string{
						{"open": "false", "powered": "false"},
						{"open": "true", "powered": "false"},
						{"open": "true", "powered": "true"},
					} {
						props := map[string]string{"facing": facingValue, "in_wall": inWallValue}
						for k, v := range target {
							props[k] = v
						}
						id, err := placementStateID(data, props)
						if err != nil {
							return fmt.Errorf("%s: %w", name, err)
						}
						switch variant {
						case 0:
							table[inWall][facingIndex] = id
						case 1:
							openTable[inWall][facingIndex] = id
						default:
							poweredTable[inWall][facingIndex] = id
						}
					}
				}
			}
			gates[name] = table
			gatesOpen[name] = openTable
			gatesPowered[name] = poweredTable
		case strings.HasSuffix(name, "_trapdoor"):
			var table [2][4]uint16
			var openTable [2][4]uint16
			var poweredTable [2][4]uint16
			for half, halfValue := range []string{"bottom", "top"} {
				for facingIndex, facingValue := range placementHorizontalFacingValues {
					id, err := placementStateID(data, map[string]string{
						"half": halfValue, "facing": facingValue,
						"open": "false", "powered": "false",
					})
					if err != nil {
						return fmt.Errorf("%s: %w", name, err)
					}
					table[half][facingIndex] = id
					openID, err := placementStateID(data, map[string]string{
						"half": halfValue, "facing": facingValue,
						"open": "true", "powered": "false",
					})
					if err != nil {
						return fmt.Errorf("%s: %w", name, err)
					}
					openTable[half][facingIndex] = openID
					poweredID, err := placementStateID(data, map[string]string{
						"half": halfValue, "facing": facingValue,
						"open": "true", "powered": "true",
					})
					if err != nil {
						return fmt.Errorf("%s: %w", name, err)
					}
					poweredTable[half][facingIndex] = poweredID
				}
			}
			trapdoors[name] = table
			trapdoorsOpen[name] = openTable
			trapdoorsPowered[name] = poweredTable
		case name == "minecraft:lever", strings.HasSuffix(name, "_button"):
			var table [3][4]uint16
			for faceIndex, faceValue := range []string{"floor", "wall", "ceiling"} {
				for facingIndex, facingValue := range placementHorizontalFacingValues {
					id, err := placementStateID(data, map[string]string{
						"face": faceValue, "facing": facingValue, "powered": "false",
					})
					if err != nil {
						return fmt.Errorf("%s: %w", name, err)
					}
					table[faceIndex][facingIndex] = id
				}
			}
			faceAttached[name] = table
		case placementHorizontalBlocks[name], strings.HasSuffix(name, "_glazed_terracotta"):
			var table [6]uint16
			for facingIndex, facingValue := range placementFacingValues {
				if facingIndex < 2 {
					continue // 水平方块不使用上下朝向
				}
				id, err := placementStateID(data, placementOverrides(name, map[string]string{
					"facing": facingValue,
				}))
				if err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
				table[facingIndex] = id
			}
			facing[name] = table
		case placementVerticalFacingBlocks[name], strings.HasSuffix(name, "_shulker_box"):
			var table [6]uint16
			for facingIndex, facingValue := range placementFacingValues {
				id, err := placementStateID(data, placementOverrides(name, map[string]string{
					"facing": facingValue,
				}))
				if err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
				table[facingIndex] = id
			}
			facing[name] = table
		case placementWallTorchBlocks[name]:
			var table [4]uint16
			for facingIndex, facingValue := range placementHorizontalFacingValues {
				id, err := placementStateID(data, map[string]string{"facing": facingValue})
				if err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
				table[facingIndex] = id
			}
			wallTorches[name] = table
		case name == "minecraft:ladder":
			var table [4]uint16
			for facingIndex, facingValue := range placementHorizontalFacingValues {
				id, err := placementStateID(data, map[string]string{"facing": facingValue})
				if err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
				table[facingIndex] = id
			}
			ladders[name] = table
		default:
			if placementAxisExcluded[name] || !hasAxisOnly(baseProps) {
				continue
			}
			var table [3]uint16
			for i, axisValue := range []string{"x", "y", "z"} {
				id, err := placementStateID(data, map[string]string{"axis": axisValue})
				if err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
				table[i] = id
			}
			axis[name] = table
		}
	}

	var b strings.Builder
	b.WriteString(licenseHeader)
	fmt.Fprintf(&b, "// Code generated by tools/genregistries from Minecraft %s reports/blocks.json; DO NOT EDIT.\n", version)
	fmt.Fprintf(&b, "//\n")
	fmt.Fprintf(&b, "// 放置形态状态表（只含放置会用到的属性组合，见 tools/genregistries/placement.go）。\n")
	fmt.Fprintf(&b, "\npackage registry\n\n")

	writeStairsTable(&b, stairs)
	writeSlabsTable(&b, slabs)
	writeAxisTable(&b, axis)
	writeFacingTable(&b, facing)
	writeTrapdoorsTable(&b, "PlacementTrapdoors", "活板门的关闭放置状态", "open=false、powered=false", trapdoors)
	writeTrapdoorsTable(&b, "PlacementTrapdoorsOpen", "活板门的打开状态", "open=true、powered=false", trapdoorsOpen)
	writeTrapdoorsTable(&b, "PlacementTrapdoorsPowered", "活板门的通电打开状态", "open=true、powered=true", trapdoorsPowered)
	writeDoorsTable(&b, "PlacementDoors", "门的关闭放置状态", "open=false、powered=false", doors)
	writeDoorsTable(&b, "PlacementDoorsOpen", "门的打开状态", "open=true、powered=false", doorsOpen)
	writeDoorsTable(&b, "PlacementDoorsPowered", "门的通电打开状态", "open=true、powered=true", doorsPowered)
	writeGatesTable(&b, "PlacementFenceGates", "栅栏门的关闭放置状态", "open=false、powered=false", gates)
	writeGatesTable(&b, "PlacementFenceGatesOpen", "栅栏门的打开状态", "open=true、powered=false", gatesOpen)
	writeGatesTable(&b, "PlacementFenceGatesPowered", "栅栏门的通电打开状态", "open=true、powered=true", gatesPowered)
	writeFaceAttachedTable(&b, faceAttached)
	writeSimpleFacingTable(&b, "PlacementWallTorches", "墙式火把", wallTorches)
	writeSimpleFacingTable(&b, "PlacementLadders", "梯子", ladders)
	writeSimpleUint16Table(&b, "LeafPersistentStates",
		"树叶的玩家放置状态", "persistent=true、distance=7、waterlogged=false（原版放置不衰减）",
		leaves)
	writeStairsShapeTable(&b, stairsShapes)
	writeBedsTable(&b, beds)
	writeStandingSignsTable(&b, standingSigns)
	writeWallSignsTable(&b, wallSigns)

	source := []byte(b.String())
	formatted, err := format.Source(source)
	if err != nil {
		return fmt.Errorf("格式化生成代码：%w（原始内容长度 %d）", err, len(source))
	}
	path := filepath.Join(outDir, "block_placement_generated.go")
	if err := os.WriteFile(path, formatted, 0o644); err != nil {
		return err
	}
	fmt.Printf("已生成放置形态表（台阶 %d、半砖 %d、轴 %d、朝向 %d、活板门 %d）\n",
		len(stairs), len(slabs), len(axis), len(facing), len(trapdoors))
	return nil
}

// placementOverrides 合并方块固定属性与本次覆盖。
func placementOverrides(name string, overrides map[string]string) map[string]string {
	fixed := placementFixedProps[name]
	if len(fixed) == 0 {
		return overrides
	}
	merged := make(map[string]string, len(fixed)+len(overrides))
	for k, v := range fixed {
		merged[k] = v
	}
	for k, v := range overrides {
		merged[k] = v
	}
	return merged
}

// hasAxisOnly 报告默认属性只包含 axis/waterlogged（柱状方块）。
func hasAxisOnly(props map[string]string) bool {
	if _, ok := props["axis"]; !ok {
		return false
	}
	for key := range props {
		if key != "axis" && key != "waterlogged" {
			return false
		}
	}
	return true
}

// placementStateID 返回“基线属性（最低状态 ID）+ 覆盖”对应的状态 ID。
// waterlogged 在状态 ID 顺序中通常把 true 排在前面，放置时一律取 false。
func placementStateID(data blockShapeData, overrides map[string]string) (uint16, error) {
	if len(data.states) == 0 {
		return 0, fmt.Errorf("没有状态")
	}
	want := make(map[string]string, len(data.states[0].props)+len(overrides))
	for key, value := range data.states[0].props {
		want[key] = value
	}
	if _, ok := want["waterlogged"]; ok {
		if _, overridden := overrides["waterlogged"]; !overridden {
			want["waterlogged"] = "false"
		}
	}
	for key, value := range overrides {
		if _, ok := want[key]; !ok {
			return 0, fmt.Errorf("缺少属性 %q", key)
		}
		want[key] = value
	}
	for _, state := range data.states {
		if len(state.props) != len(want) {
			continue
		}
		match := true
		for key, value := range want {
			if state.props[key] != value {
				match = false
				break
			}
		}
		if match {
			return state.id, nil
		}
	}
	return 0, fmt.Errorf("找不到属性组合 %v 的状态", want)
}

func writeStairsTable(b *strings.Builder, table map[string][2][4]uint16) {
	b.WriteString("// PlacementStairs 是台阶方块的放置状态：map[方块名][half][facing]。\n")
	b.WriteString("// half：0=bottom、1=top；facing：0=north、1=south、2=west、3=east。\n")
	b.WriteString("// 其余属性取默认值（shape=straight、waterlogged=false）。\n")
	b.WriteString("var PlacementStairs = map[string][2][4]uint16{\n")
	for _, name := range sortedNames(table) {
		states := table[name]
		fmt.Fprintf(b, "\t%q: {{%d, %d, %d, %d}, {%d, %d, %d, %d}},\n", name,
			states[0][0], states[0][1], states[0][2], states[0][3],
			states[1][0], states[1][1], states[1][2], states[1][3])
	}
	b.WriteString("}\n\n")
}

func writeSlabsTable(b *strings.Builder, table map[string][3]uint16) {
	b.WriteString("// PlacementSlabs 是半砖方块的放置状态：map[方块名][type]。\n")
	b.WriteString("// type：0=bottom、1=top、2=double。\n")
	b.WriteString("var PlacementSlabs = map[string][3]uint16{\n")
	for _, name := range sortedNames(table) {
		states := table[name]
		fmt.Fprintf(b, "\t%q: {%d, %d, %d},\n", name, states[0], states[1], states[2])
	}
	b.WriteString("}\n\n")
}

func writeAxisTable(b *strings.Builder, table map[string][3]uint16) {
	b.WriteString("// PlacementAxis 是柱状方块的放置状态：map[方块名][axis]。\n")
	b.WriteString("// axis：0=x、1=y、2=z。\n")
	b.WriteString("var PlacementAxis = map[string][3]uint16{\n")
	for _, name := range sortedNames(table) {
		states := table[name]
		fmt.Fprintf(b, "\t%q: {%d, %d, %d},\n", name, states[0], states[1], states[2])
	}
	b.WriteString("}\n\n")
}

func writeFacingTable(b *strings.Builder, table map[string][6]uint16) {
	b.WriteString("// PlacementFacing 是朝向方块的放置状态：map[方块名][direction]。\n")
	b.WriteString("// direction：0=down、1=up、2=north、3=south、4=west、5=east。\n")
	b.WriteString("// 水平四向方块的 0/1 项为 0（不使用）。\n")
	b.WriteString("var PlacementFacing = map[string][6]uint16{\n")
	for _, name := range sortedNames(table) {
		states := table[name]
		fmt.Fprintf(b, "\t%q: {%d, %d, %d, %d, %d, %d},\n", name,
			states[0], states[1], states[2], states[3], states[4], states[5])
	}
	b.WriteString("}\n\n")
}

func writeTrapdoorsTable(b *strings.Builder, varName, title, extra string, table map[string][2][4]uint16) {
	fmt.Fprintf(b, "// %s 是%s：map[方块名][half][facing]。\n", varName, title)
	b.WriteString("// half：0=bottom、1=top；facing：0=north、1=south、2=west、3=east。\n")
	fmt.Fprintf(b, "// 其余属性为 %s、waterlogged=false。\n", extra)
	fmt.Fprintf(b, "var %s = map[string][2][4]uint16{\n", varName)
	for _, name := range sortedNames(table) {
		states := table[name]
		fmt.Fprintf(b, "\t%q: {{%d, %d, %d, %d}, {%d, %d, %d, %d}},\n", name,
			states[0][0], states[0][1], states[0][2], states[0][3],
			states[1][0], states[1][1], states[1][2], states[1][3])
	}
	b.WriteString("}\n\n")
}

func writeGatesTable(b *strings.Builder, varName, title, extra string, table map[string][2][4]uint16) {
	fmt.Fprintf(b, "// %s 是%s：map[方块名][inWall][facing]。\n", varName, title)
	b.WriteString("// inWall：0=两侧无墙、1=沿 facing 轴的一侧有墙（仅影响外观与碰撞盒）；\n")
	b.WriteString("// facing：0=north、1=south、2=west、3=east。\n")
	fmt.Fprintf(b, "// 其余属性为 %s。\n", extra)
	fmt.Fprintf(b, "var %s = map[string][2][4]uint16{\n", varName)
	for _, name := range sortedNames(table) {
		states := table[name]
		fmt.Fprintf(b, "\t%q: {{%d, %d, %d, %d}, {%d, %d, %d, %d}},\n", name,
			states[0][0], states[0][1], states[0][2], states[0][3],
			states[1][0], states[1][1], states[1][2], states[1][3])
	}
	b.WriteString("}\n\n")
}

func writeDoorsTable(b *strings.Builder, varName, title, extra string, table map[string][2][4][2]uint16) {
	fmt.Fprintf(b, "// %s 是%s：map[方块名][half][facing][hinge]。\n", varName, title)
	b.WriteString("// half：0=lower、1=upper；facing：0=north…3=east；hinge：0=left、1=right。\n")
	fmt.Fprintf(b, "// 其余属性为 %s、waterlogged=false。\n", extra)
	fmt.Fprintf(b, "var %s = map[string][2][4][2]uint16{\n", varName)
	for _, name := range sortedNames(table) {
		states := table[name]
		fmt.Fprintf(b, "\t%q: {", name)
		for half := 0; half < 2; half++ {
			fmt.Fprintf(b, "%s{", map[bool]string{true: ", ", false: ""}[half == 1])
			for facing := 0; facing < 4; facing++ {
				fmt.Fprintf(b, "%s{%d, %d}", map[bool]string{true: ", ", false: ""}[facing > 0],
					states[half][facing][0], states[half][facing][1])
			}
			fmt.Fprintf(b, "}")
		}
		fmt.Fprintf(b, "},\n")
	}
	b.WriteString("}\n\n")
}

func writeFaceAttachedTable(b *strings.Builder, table map[string][3][4]uint16) {
	b.WriteString("// PlacementFaceAttached 是拉杆/按钮的放置状态：map[方块名][face][facing]。\n")
	b.WriteString("// face：0=floor、1=wall、2=ceiling；facing：0=north、1=south、2=west、3=east。\n")
	b.WriteString("// 其余属性取默认值（powered=false）。\n")
	b.WriteString("var PlacementFaceAttached = map[string][3][4]uint16{\n")
	for _, name := range sortedNames(table) {
		states := table[name]
		fmt.Fprintf(b, "\t%q: {{%d, %d, %d, %d}, {%d, %d, %d, %d}, {%d, %d, %d, %d}},\n", name,
			states[0][0], states[0][1], states[0][2], states[0][3],
			states[1][0], states[1][1], states[1][2], states[1][3],
			states[2][0], states[2][1], states[2][2], states[2][3])
	}
	b.WriteString("}\n\n")
}

func writeSimpleFacingTable(b *strings.Builder, varName, comment string, table map[string][4]uint16) {
	fmt.Fprintf(b, "// %s 是%s的放置状态：map[方块名][facing]。\n", varName, comment)
	b.WriteString("// facing：0=north、1=south、2=west、3=east。\n")
	fmt.Fprintf(b, "var %s = map[string][4]uint16{\n", varName)
	for _, name := range sortedNames(table) {
		states := table[name]
		fmt.Fprintf(b, "\t%q: {%d, %d, %d, %d},\n", name,
			states[0], states[1], states[2], states[3])
	}
	b.WriteString("}\n\n")
}

func writeSimpleUint16Table(b *strings.Builder, varName, comment, extra string, table map[string]uint16) {
	fmt.Fprintf(b, "// %s 是%s：map[方块名]状态 ID。\n", varName, comment)
	fmt.Fprintf(b, "// 属性为 %s。\n", extra)
	fmt.Fprintf(b, "var %s = map[string]uint16{\n", varName)
	for _, name := range sortedNames(table) {
		fmt.Fprintf(b, "\t%q: %d,\n", name, table[name])
	}
	b.WriteString("}\n\n")
}

func sortedNames[T any](m map[string]T) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// writeStairsShapeTable 生成台阶的“全部 shape”状态表，供邻居形状更新使用。
func writeStairsShapeTable(b *strings.Builder, table map[string][2][4][5]uint16) {
	b.WriteString("// PlacementStairsShape 是台阶的完整形态状态：map[方块名][half][facing][shape]。\n")
	b.WriteString("// half：0=bottom、1=top；facing：0=north、1=south、2=west、3=east；\n")
	b.WriteString("// shape：0=straight、1=inner_left、2=inner_right、3=outer_left、4=outer_right。\n")
	b.WriteString("var PlacementStairsShape = map[string][2][4][5]uint16{\n")
	for _, name := range sortedNames(table) {
		states := table[name]
		fmt.Fprintf(b, "\t%q: {\n", name)
		for half := 0; half < 2; half++ {
			b.WriteString("\t\t{")
			for facing := 0; facing < 4; facing++ {
				if facing > 0 {
					b.WriteString(", ")
				}
				fmt.Fprintf(b, "{%d, %d, %d, %d, %d}",
					states[half][facing][0], states[half][facing][1], states[half][facing][2],
					states[half][facing][3], states[half][facing][4])
			}
			b.WriteString("},\n")
		}
		b.WriteString("\t},\n")
	}
	b.WriteString("}\n\n")
}

// writeBedsTable 生成床的放置状态表。
func writeBedsTable(b *strings.Builder, table map[string][4][2]uint16) {
	b.WriteString("// PlacementBeds 是床的放置状态：map[方块名][facing][part]。\n")
	b.WriteString("// facing：0=north、1=south、2=west、3=east；part：0=foot、1=head；occupied=false。\n")
	b.WriteString("var PlacementBeds = map[string][4][2]uint16{\n")
	for _, name := range sortedNames(table) {
		states := table[name]
		fmt.Fprintf(b, "\t%q: {{%d, %d}, {%d, %d}, {%d, %d}, {%d, %d}},\n", name,
			states[0][0], states[0][1], states[1][0], states[1][1],
			states[2][0], states[2][1], states[3][0], states[3][1])
	}
	b.WriteString("}\n\n")
}

// writeStandingSignsTable 生成立式告示牌的放置状态表（rotation 0–15）。
func writeStandingSignsTable(b *strings.Builder, table map[string][16]uint16) {
	b.WriteString("// PlacementStandingSigns 是立式告示牌的放置状态：map[方块名][rotation]。\n")
	b.WriteString("// rotation：0–15，每档 22.5°（0 朝南，顺时针递增）。\n")
	b.WriteString("var PlacementStandingSigns = map[string][16]uint16{\n")
	for _, name := range sortedNames(table) {
		states := table[name]
		fmt.Fprintf(b, "\t%q: {", name)
		for i := 0; i < 16; i++ {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(b, "%d", states[i])
		}
		b.WriteString("},\n")
	}
	b.WriteString("}\n\n")
}

// writeWallSignsTable 生成墙式告示牌的放置状态表。
func writeWallSignsTable(b *strings.Builder, table map[string][4]uint16) {
	b.WriteString("// PlacementWallSigns 是墙式告示牌的放置状态：map[方块名][facing]。\n")
	b.WriteString("// facing：0=north、1=south、2=west、3=east（被贴附面的方向）。\n")
	b.WriteString("var PlacementWallSigns = map[string][4]uint16{\n")
	for _, name := range sortedNames(table) {
		states := table[name]
		fmt.Fprintf(b, "\t%q: {%d, %d, %d, %d},\n", name, states[0], states[1], states[2], states[3])
	}
	b.WriteString("}\n\n")
}
