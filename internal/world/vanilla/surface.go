//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

// 本文件实现原版地表规则（net.minecraft.world.level.levelgen.SurfaceRules，
// 1.21.11 混淆名 evh）的解析与求值。
//
// 规则树有两层：
//   - ConditionSource（evh$f）→ Condition（evh$e），只回答"当前位置是否满足条件"；
//   - RuleSource（evh$o）→ SurfaceRule（evh$u），满足时给出替换方块（否则 null）。
//
// 语义逐条按字节码核对，见各类型的注释；上下文字段与原版 evh$g 的对应关系见
// surfacesystem.go 的 surfaceContext。

// SurfaceBlock 是地表规则里使用的方块状态（注册名 + 可选方块状态属性）。
type SurfaceBlock struct {
	Name       string
	Properties map[string]string
}

// IsAir 等价 BlockState.isAir()。
func (b SurfaceBlock) IsAir() bool { return b.Name == "minecraft:air" }

// IsWater 等价 BlockState.is(Blocks.WATER)。
func (b SurfaceBlock) IsWater() bool { return b.Name == "minecraft:water" }

// IsLava 等价 BlockState.is(Blocks.LAVA)。
func (b SurfaceBlock) IsLava() bool { return b.Name == "minecraft:lava" }

// HasFluid 等价 !BlockState.getFluidState().isEmpty()：只有水和岩浆算流体。
func (b SurfaceBlock) HasFluid() bool { return b.IsWater() || b.IsLava() }

// String 返回可读形式（属性按键排序，便于测试与日志）。
func (b SurfaceBlock) String() string {
	if len(b.Properties) == 0 {
		return b.Name
	}
	keys := make([]string, 0, len(b.Properties))
	for key := range b.Properties {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	builder.WriteString(b.Name)
	builder.WriteString("[")
	for i, key := range keys {
		if i > 0 {
			builder.WriteString(",")
		}
		builder.WriteString(key)
		builder.WriteString("=")
		builder.WriteString(b.Properties[key])
	}
	builder.WriteString("]")
	return builder.String()
}

// surfaceCondition 等价原版 SurfaceRules.Condition（evh$e）。
type surfaceCondition interface {
	test(c *surfaceContext) bool
}

// surfaceRule 等价原版 SurfaceRules.SurfaceRule（evh$u.tryApply）：
// 返回 ok=false 表示该位置不替换方块。
type surfaceRule interface {
	apply(c *surfaceContext, x, y, z int) (SurfaceBlock, bool)
}

// ---- 规则 ----

// blockRule 等价 evh$r（BlockRuleSource）：直接给出方块。
type blockRule struct{ state SurfaceBlock }

func (r blockRule) apply(*surfaceContext, int, int, int) (SurfaceBlock, bool) {
	return r.state, true
}

// sequenceRule 等价 evh$p：按顺序求值，第一个非空结果胜出。
type sequenceRule struct{ rules []surfaceRule }

func (r sequenceRule) apply(c *surfaceContext, x, y, z int) (SurfaceBlock, bool) {
	for _, rule := range r.rules {
		if state, ok := rule.apply(c, x, y, z); ok {
			return state, true
		}
	}
	return SurfaceBlock{}, false
}

// conditionRule 等价 evh$w：条件成立时才求值子规则。
type conditionRule struct {
	condition surfaceCondition
	then      surfaceRule
}

func (r conditionRule) apply(c *surfaceContext, x, y, z int) (SurfaceBlock, bool) {
	if !r.condition.test(c) {
		return SurfaceBlock{}, false
	}
	return r.then.apply(c, x, y, z)
}

// bandlandsRule 等价 evh$b：按粘土带纹理给方块。
type bandlandsRule struct{}

func (r bandlandsRule) apply(c *surfaceContext, _ int, y int, _ int) (SurfaceBlock, bool) {
	return c.system.band(c.blockX, y, c.blockZ), true
}

// ---- 条件 ----

// abovePreliminarySurfaceCondition 等价 SurfaceRules.AbovePreliminarySurface：
// blockY >= 当前列的初步地表高度（含 -8 偏移后的插值结果）。
type abovePreliminarySurfaceCondition struct{}

func (abovePreliminarySurfaceCondition) test(c *surfaceContext) bool {
	return c.blockY >= c.preliminarySurfaceLevel
}

// biomeCondition 等价 evh$c：当前方块所在群系属于给定集合。
type biomeCondition struct{ names map[string]struct{} }

func (cond biomeCondition) test(c *surfaceContext) bool {
	_, ok := cond.names[c.biome()]
	return ok
}

// holeCondition 等价 SurfaceRules.hole()：列级"最小地表高度"不超过 0。
type holeCondition struct{}

func (holeCondition) test(c *surfaceContext) bool { return c.minSurfaceLevel <= 0 }

// steepCondition 等价 SurfaceRules.steep()：四个相邻列的高度差达到 4 格。
type steepCondition struct{}

func (steepCondition) test(c *surfaceContext) bool {
	return c.steep()
}

// temperatureCondition 等价 SurfaceRules.temperature()：
// 群系在当前方块位置是否"冷到足以下雪"。
type temperatureCondition struct{}

func (temperatureCondition) test(c *surfaceContext) bool {
	return c.coldEnoughToSnow()
}

// noiseThresholdCondition 等价 evh$l：噪声在 (x, 0, z) 的取值落在闭区间内。
type noiseThresholdCondition struct {
	noise   *NormalNoise
	minimum float64
	maximum float64
}

func (cond noiseThresholdCondition) test(c *surfaceContext) bool {
	value := cond.noise.Value(float64(c.blockX), 0, float64(c.blockZ))
	return value >= cond.minimum && value <= cond.maximum
}

// notCondition 等价 evh$m：条件取反。
type notCondition struct{ inner surfaceCondition }

func (cond notCondition) test(c *surfaceContext) bool { return !cond.inner.test(c) }

// stoneDepthCondition 等价 evh$t：
//
//	ceiling ? surfaceDepth : stoneDepthAbove
//	  <= 1 + offset + (addSurfaceDepth ? minSurfaceLevel : 0) + secondaryRandom
//
// 其中 secondaryRandom = secondaryDepthRange == 0 ? 0
//
//	: (int) lerp(inverseLerp(getSurfaceDepth(x,z), -1, 1), 0, secondaryDepthRange)。
type stoneDepthCondition struct {
	offset              int
	addSurfaceDepth     bool
	secondaryDepthRange int
	ceiling             bool
}

func (cond stoneDepthCondition) test(c *surfaceContext) bool {
	depth := c.stoneDepthAbove
	if cond.ceiling {
		depth = c.surfaceDepth
	}
	add := 0
	if cond.addSurfaceDepth {
		add = c.minSurfaceLevel
	}
	secondary := 0
	if cond.secondaryDepthRange != 0 {
		secondary = int(lerpUnclamped(inverseLerp(c.system.surfaceDepth(c.blockX, c.blockZ), -1, 1),
			0, float64(cond.secondaryDepthRange)))
	}
	return depth <= 1+cond.offset+add+secondary
}

// waterCondition 等价 evh$z：
//
//	stoneDepthBelow == MIN ? true
//	  : blockY + (addStoneDepth ? stoneDepthAbove : 0)
//	      >= stoneDepthBelow + offset + minSurfaceLevel * surfaceDepthMultiplier
type waterCondition struct {
	offset                 int
	surfaceDepthMultiplier int
	addStoneDepth          bool
}

func (cond waterCondition) test(c *surfaceContext) bool {
	if c.stoneDepthBelow == noStoneDepth {
		return true
	}
	y := c.blockY
	if cond.addStoneDepth {
		y += c.stoneDepthAbove
	}
	return y >= c.stoneDepthBelow+cond.offset+c.minSurfaceLevel*cond.surfaceDepthMultiplier
}

// yAboveCondition 等价 evh$aa：
//
//	blockY + (addStoneDepth ? stoneDepthAbove : 0)
//	  >= anchor.resolveY(minY) + minSurfaceLevel * surfaceDepthMultiplier
type yAboveCondition struct {
	anchor                 verticalAnchor
	surfaceDepthMultiplier int
	addStoneDepth          bool
}

func (cond yAboveCondition) test(c *surfaceContext) bool {
	y := c.blockY
	if cond.addStoneDepth {
		y += c.stoneDepthAbove
	}
	return y >= cond.anchor.resolveY(c.system.setting.MinY)+c.minSurfaceLevel*cond.surfaceDepthMultiplier
}

// verticalGradientCondition 等价 evh$y：
// 真值随高度从 trueAtAndBelow 到 falseAtAndAbove 线性衰减，并与逐格随机数比较。
type verticalGradientCondition struct {
	randomName string
	trueAt     verticalAnchor
	falseAt    verticalAnchor
}

func (cond verticalGradientCondition) test(c *surfaceContext) bool {
	trueY := cond.trueAt.resolveY(c.system.setting.MinY)
	falseY := cond.falseAt.resolveY(c.system.setting.MinY)
	y := c.blockY
	if y <= trueY {
		return true
	}
	if y >= falseY {
		return false
	}
	chance := lerpUnclamped(inverseLerp(float64(y), float64(trueY), float64(falseY)), 1, 0)
	random := c.system.gradientRandom(cond.randomName, c.blockX, y, c.blockZ)
	return float64(random.NextFloat()) < chance
}

// noStoneDepth 等价原版 Integer.MIN_VALUE 哨兵：上方没有流体。
const noStoneDepth = math.MinInt32

// verticalAnchor 等价原版 VerticalAnchor（evk）。
type verticalAnchor struct {
	absolute    *int
	aboveBottom *int
	belowTop    *int
}

// resolveY 等价原版 VerticalAnchor.resolveY(WorldGenerationContext)。
func (a verticalAnchor) resolveY(minY int) int {
	switch {
	case a.absolute != nil:
		return *a.absolute
	case a.aboveBottom != nil:
		return minY + *a.aboveBottom
	case a.belowTop != nil:
		return minY + 384 - *a.belowTop
	default:
		panic("vanilla: 空的 VerticalAnchor")
	}
}

// parseVerticalAnchor 解析 vertical_anchor 对象。
func parseVerticalAnchor(where string, raw json.RawMessage) (verticalAnchor, []Problem) {
	var parsed map[string]int
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return verticalAnchor{}, []Problem{{Where: where, Detail: "非法 vertical_anchor：" + err.Error()}}
	}
	anchor := verticalAnchor{}
	for key, value := range parsed {
		v := value
		switch key {
		case "absolute":
			anchor.absolute = &v
		case "above_bottom":
			anchor.aboveBottom = &v
		case "below_top":
			anchor.belowTop = &v
		default:
			return verticalAnchor{}, []Problem{{Where: where, Detail: "未知的 vertical_anchor 字段 " + key}}
		}
	}
	if anchor.absolute == nil && anchor.aboveBottom == nil && anchor.belowTop == nil {
		return verticalAnchor{}, []Problem{{Where: where, Detail: "vertical_anchor 为空"}}
	}
	return anchor, nil
}

// surfaceRuleSchema 描述各规则/条件类型允许的字段。
type surfaceNodeSchema struct {
	nodes   []string
	numbers []string
	bools   []string
	strings []string
}

// surfaceRuleSchemas 覆盖 1.21.11 overworld 地表规则里出现的全部类型。
var surfaceRuleSchemas = map[string]surfaceNodeSchema{
	"block":     {nodes: []string{"result_state"}},
	"sequence":  {nodes: []string{"sequence"}},
	"condition": {nodes: []string{"if_true", "then_run"}},
	"bandlands": {},
}

// surfaceConditionSchemas 是条件类型的字段表。
var surfaceConditionSchemas = map[string]surfaceNodeSchema{
	"above_preliminary_surface": {},
	"biome":                     {nodes: []string{"biome_is"}},
	"hole":                      {},
	"noise_threshold":           {numbers: []string{"min_threshold", "max_threshold"}, strings: []string{"noise"}},
	"not":                       {nodes: []string{"invert"}},
	"steep":                     {},
	"stone_depth":               {numbers: []string{"offset", "secondary_depth_range"}, bools: []string{"add_surface_depth"}, strings: []string{"surface_type"}},
	"temperature":               {},
	"vertical_gradient":         {nodes: []string{"true_at_and_below", "false_at_and_above"}, strings: []string{"random_name"}},
	"water":                     {numbers: []string{"offset", "surface_depth_multiplier"}, bools: []string{"add_stone_depth"}},
	"y_above":                   {nodes: []string{"anchor"}, numbers: []string{"surface_depth_multiplier"}, bools: []string{"add_stone_depth"}},
}

// parseSurfaceRule 解析一条地表规则。
func parseSurfaceRule(noises *NoiseRegistry, where string, raw json.RawMessage) (surfaceRule, []Problem) {
	kind, body, problems := surfaceNodeHeader(where, raw)
	if len(problems) > 0 {
		return nil, problems
	}
	if schema, ok := surfaceRuleSchemas[kind]; !ok {
		return nil, []Problem{{Where: where, Detail: "未知的地表规则类型 " + kind}}
	} else if problems := validateSurfaceFields(where, kind, body, schema); len(problems) > 0 {
		return nil, problems
	}
	switch kind {
	case "block":
		state, problems := parseSurfaceBlock(where+"/result_state", body["result_state"])
		if len(problems) > 0 {
			return nil, problems
		}
		return blockRule{state: state}, nil
	case "sequence":
		var entries []json.RawMessage
		if err := json.Unmarshal(body["sequence"], &entries); err != nil {
			return nil, []Problem{{Where: where + "/sequence", Detail: "非法数组：" + err.Error()}}
		}
		rules := make([]surfaceRule, 0, len(entries))
		for i, entry := range entries {
			rule, entryProblems := parseSurfaceRule(noises, fmt.Sprintf("%s/sequence[%d]", where, i), entry)
			if len(entryProblems) > 0 {
				return nil, entryProblems
			}
			rules = append(rules, rule)
		}
		return sequenceRule{rules: rules}, nil
	case "condition":
		condition, problems := parseSurfaceCondition(noises, where+"/if_true", body["if_true"])
		if len(problems) > 0 {
			return nil, problems
		}
		then, problems := parseSurfaceRule(noises, where+"/then_run", body["then_run"])
		if len(problems) > 0 {
			return nil, problems
		}
		return conditionRule{condition: condition, then: then}, nil
	default: // bandlands
		return bandlandsRule{}, nil
	}
}

// parseSurfaceCondition 解析一条地表条件。
func parseSurfaceCondition(noises *NoiseRegistry, where string, raw json.RawMessage) (surfaceCondition, []Problem) {
	kind, body, problems := surfaceNodeHeader(where, raw)
	if len(problems) > 0 {
		return nil, problems
	}
	schema, ok := surfaceConditionSchemas[kind]
	if !ok {
		return nil, []Problem{{Where: where, Detail: "未知的地表条件类型 " + kind}}
	}
	if problems := validateSurfaceFields(where, kind, body, schema); len(problems) > 0 {
		return nil, problems
	}
	switch kind {
	case "above_preliminary_surface":
		return abovePreliminarySurfaceCondition{}, nil
	case "biome":
		var names []string
		if err := json.Unmarshal(body["biome_is"], &names); err != nil {
			return nil, []Problem{{Where: where + "/biome_is", Detail: "非法数组：" + err.Error()}}
		}
		set := make(map[string]struct{}, len(names))
		for _, name := range names {
			set[qualify(name)] = struct{}{}
		}
		return biomeCondition{names: set}, nil
	case "hole":
		return holeCondition{}, nil
	case "steep":
		return steepCondition{}, nil
	case "temperature":
		return temperatureCondition{}, nil
	case "noise_threshold":
		var name string
		if err := json.Unmarshal(body["noise"], &name); err != nil {
			return nil, []Problem{{Where: where + "/noise", Detail: "非法噪声名：" + err.Error()}}
		}
		minimum, problems := surfaceNumber(body, "min_threshold", where, -1)
		if len(problems) > 0 {
			return nil, problems
		}
		maximum, problems := surfaceNumber(body, "max_threshold", where, 1)
		if len(problems) > 0 {
			return nil, problems
		}
		return noiseThresholdCondition{noise: noises.Noise(name), minimum: minimum, maximum: maximum}, nil
	case "not":
		inner, problems := parseSurfaceCondition(noises, where+"/invert", body["invert"])
		if len(problems) > 0 {
			return nil, problems
		}
		return notCondition{inner: inner}, nil
	case "stone_depth":
		offset, problems := surfaceInt(body, "offset", where, 0)
		if len(problems) > 0 {
			return nil, problems
		}
		secondary, problems := surfaceInt(body, "secondary_depth_range", where, 0)
		if len(problems) > 0 {
			return nil, problems
		}
		addSurfaceDepth, problems := surfaceBool(body, "add_surface_depth", where, false)
		if len(problems) > 0 {
			return nil, problems
		}
		var surfaceType string
		if err := json.Unmarshal(body["surface_type"], &surfaceType); err != nil {
			return nil, []Problem{{Where: where + "/surface_type", Detail: "非法取值：" + err.Error()}}
		}
		switch surfaceType {
		case "floor", "ceiling":
		default:
			return nil, []Problem{{Where: where + "/surface_type", Detail: "未知取值 " + surfaceType}}
		}
		return stoneDepthCondition{
			offset:              offset,
			addSurfaceDepth:     addSurfaceDepth,
			secondaryDepthRange: secondary,
			ceiling:             surfaceType == "ceiling",
		}, nil
	case "vertical_gradient":
		var randomName string
		if err := json.Unmarshal(body["random_name"], &randomName); err != nil {
			return nil, []Problem{{Where: where + "/random_name", Detail: "非法取值：" + err.Error()}}
		}
		trueAt, problems := parseVerticalAnchor(where+"/true_at_and_below", body["true_at_and_below"])
		if len(problems) > 0 {
			return nil, problems
		}
		falseAt, problems := parseVerticalAnchor(where+"/false_at_and_above", body["false_at_and_above"])
		if len(problems) > 0 {
			return nil, problems
		}
		return verticalGradientCondition{randomName: qualify(randomName), trueAt: trueAt, falseAt: falseAt}, nil
	case "water":
		offset, problems := surfaceInt(body, "offset", where, 0)
		if len(problems) > 0 {
			return nil, problems
		}
		multiplier, problems := surfaceInt(body, "surface_depth_multiplier", where, 0)
		if len(problems) > 0 {
			return nil, problems
		}
		addStoneDepth, problems := surfaceBool(body, "add_stone_depth", where, false)
		if len(problems) > 0 {
			return nil, problems
		}
		return waterCondition{offset: offset, surfaceDepthMultiplier: multiplier, addStoneDepth: addStoneDepth}, nil
	default: // y_above
		anchor, problems := parseVerticalAnchor(where+"/anchor", body["anchor"])
		if len(problems) > 0 {
			return nil, problems
		}
		multiplier, problems := surfaceInt(body, "surface_depth_multiplier", where, 0)
		if len(problems) > 0 {
			return nil, problems
		}
		addStoneDepth, problems := surfaceBool(body, "add_stone_depth", where, false)
		if len(problems) > 0 {
			return nil, problems
		}
		return yAboveCondition{anchor: anchor, surfaceDepthMultiplier: multiplier, addStoneDepth: addStoneDepth}, nil
	}
}

// surfaceNodeHeader 解析 {"type": "...", ...} 头并返回短类型名与字段表。
func surfaceNodeHeader(where string, raw json.RawMessage) (string, map[string]json.RawMessage, []Problem) {
	if len(raw) == 0 {
		return "", nil, []Problem{{Where: where, Detail: "空节点"}}
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		return "", nil, []Problem{{Where: where, Detail: "非法对象：" + err.Error()}}
	}
	var fullType string
	if err := json.Unmarshal(body["type"], &fullType); err != nil {
		return "", nil, []Problem{{Where: where, Detail: "缺少 type"}}
	}
	kind := strings.TrimPrefix(qualify(fullType), "minecraft:")
	return kind, body, nil
}

// validateSurfaceFields 检查未知字段（多出来的字段说明数据格式与实现不匹配）。
func validateSurfaceFields(where, kind string, body map[string]json.RawMessage, schema surfaceNodeSchema) []Problem {
	allowed := map[string]bool{"type": true}
	for _, name := range append(append(append([]string{}, schema.nodes...), schema.numbers...), schema.bools...) {
		allowed[name] = true
	}
	for _, name := range schema.strings {
		allowed[name] = true
	}
	names := make([]string, 0, len(body))
	for name := range body {
		names = append(names, name)
	}
	sort.Strings(names)
	var problems []Problem
	for _, name := range names {
		if !allowed[name] {
			problems = append(problems, Problem{Where: where, Detail: kind + " 不支持字段 " + name})
		}
	}
	return problems
}

// parseSurfaceBlock 解析 result_state（{"Name": ..., "Properties": {...}} 或纯字符串）。
func parseSurfaceBlock(where string, raw json.RawMessage) (SurfaceBlock, []Problem) {
	if len(raw) == 0 {
		return SurfaceBlock{}, []Problem{{Where: where, Detail: "空的方块状态"}}
	}
	if name := blockStateName(raw); name != "" && !strings.HasPrefix(strings.TrimSpace(string(raw)), "{") {
		return SurfaceBlock{Name: qualify(name)}, nil
	}
	var parsed struct {
		Name       string            `json:"Name"`
		Properties map[string]string `json:"Properties"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || parsed.Name == "" {
		return SurfaceBlock{}, []Problem{{Where: where, Detail: "非法方块状态 " + string(raw)}}
	}
	return SurfaceBlock{Name: qualify(parsed.Name), Properties: parsed.Properties}, nil
}

// surfaceInt 读取整数字段（缺省时使用 fallback）。
func surfaceInt(body map[string]json.RawMessage, name, where string, fallback int) (int, []Problem) {
	raw, ok := body[name]
	if !ok {
		return fallback, nil
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, []Problem{{Where: where + "/" + name, Detail: "非法整数：" + err.Error()}}
	}
	return value, nil
}

// surfaceNumber 读取浮点字段（缺省时使用 fallback）。
func surfaceNumber(body map[string]json.RawMessage, name, where string, fallback float64) (float64, []Problem) {
	raw, ok := body[name]
	if !ok {
		return fallback, nil
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, []Problem{{Where: where + "/" + name, Detail: "非法数字：" + err.Error()}}
	}
	return value, nil
}

// surfaceBool 读取布尔字段（缺省时使用 fallback）。
func surfaceBool(body map[string]json.RawMessage, name, where string, fallback bool) (bool, []Problem) {
	raw, ok := body[name]
	if !ok {
		return fallback, nil
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, []Problem{{Where: where + "/" + name, Detail: "非法布尔值：" + err.Error()}}
	}
	return value, nil
}
