//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Node 是密度函数图中的一个节点（原版 DensityFunction 的 JSON 形式）。
//
// 解析阶段只做结构校验与引用解析：未知类型、缺少必需参数、引用了不存在的
// 密度函数/噪声参数都会报错，而不是在计算时静默当作 0。
// 采样求值（第 2 阶段）在此结构上实现。
type Node struct {
	// Type 是节点类型（已去掉 minecraft: 前缀，如 "add"）。
	Type string
	// Children 是子节点，顺序随类型而定（见 parseNode）。
	Children []*Node
	// Noise 是引用的噪声参数名（noise/shifted_noise/weird_scaled_sampler）。
	Noise string
	// Value 是 constant 的取值。
	Value float64
	// Min / Max 是 clamp、range_choice 的边界。
	Min float64
	Max float64
	// XZScale / YScale 是 noise、shifted_noise、old_blended_noise 的缩放。
	XZScale float64
	YScale  float64
	// Spline 是 spline 节点的控制点表。
	Spline *Spline
	// RarityMapper 是 weird_scaled_sampler 的映射方式（type_1/type_2）。
	RarityMapper string
	// RefName 是被引用密度函数的注册名（本节点来自字符串引用时填充）。
	RefName string
	// CellHeight 是 find_top_surface 的网格高度。
	CellHeight int
	// LowerBound 是 find_top_surface 的搜索下界。
	LowerBound int
	// FromY / ToY / FromValue / ToValue 是 y_clamped_gradient 的四个端点参数。
	FromY     float64
	ToY       float64
	FromValue float64
	ToValue   float64
	// From 是 old_blended_noise 的遗留参数（按原版 JSON 顺序保存）。
	Legacy [5]float64
}

// Spline 是原版 spline 密度函数（沿某个坐标做三次插值）。
type Spline struct {
	// Coordinate 是被插值的密度函数（如 weirdness）。
	Coordinate *Node
	// Points 是控制点，按 location 升序。
	Points []SplinePoint
}

// SplinePoint 是 spline 的一个控制点；Value 与 Sub 互斥
// （Sub 非空表示该点嵌套了子 spline）。
//
// Derivative 是原版控制点的 derivative（float 精度）。原版允许省略并自动补全，
// 本包目前要求数据显式提供：1.21.11 的原版数据 54 个控制点全部带 derivative。
type SplinePoint struct {
	Location   float64
	Value      float64
	Derivative float64
	Sub        *Spline
}

// nodeFields 描述每种节点类型需要的字段名。
type nodeFields struct {
	// nodes 是需要按密度函数节点解析的字段（顺序即 Children 顺序）。
	nodes []string
	// numbers 是需要数值的字段（顺序即写回语义）。
	numbers []string
	// refs 是需要噪声参数名的字段。
	refs []string
	// enums 是枚举字符串字段。
	enums []string
	// spline 表示该类型带 spline 字段。
	spline bool
}

// nodeSchemas 是原版 overworld 路由实际用到的节点类型表。
var nodeSchemas = map[string]nodeFields{
	"constant":           {numbers: []string{"argument"}},
	"add":                {nodes: []string{"argument1", "argument2"}},
	"mul":                {nodes: []string{"argument1", "argument2"}},
	"min":                {nodes: []string{"argument1", "argument2"}},
	"max":                {nodes: []string{"argument1", "argument2"}},
	"abs":                {nodes: []string{"argument"}},
	"square":             {nodes: []string{"argument"}},
	"cube":               {nodes: []string{"argument"}},
	"half_negative":      {nodes: []string{"argument"}},
	"quarter_negative":   {nodes: []string{"argument"}},
	"squeeze":            {nodes: []string{"argument"}},
	"clamp":              {nodes: []string{"input"}, numbers: []string{"min", "max"}},
	"y_clamped_gradient": {numbers: []string{"from_y", "to_y", "from_value", "to_value"}},
	"noise":              {refs: []string{"noise"}, numbers: []string{"xz_scale", "y_scale"}},
	"shifted_noise":      {refs: []string{"noise"}, numbers: []string{"xz_scale", "y_scale"}, nodes: []string{"shift_x", "shift_y", "shift_z"}},
	"shift_a":            {refs: []string{"argument"}},
	"shift_b":            {refs: []string{"argument"}},
	// shift 的参数和 shift_a/shift_b 一样是噪声参数名（原版 Shift 记录持有
	// NoiseHolder，JSON 里就是噪声 ID 字符串），不是密度函数引用。
	"shift":                {refs: []string{"argument"}},
	"weird_scaled_sampler": {nodes: []string{"input"}, refs: []string{"noise"}, enums: []string{"rarity_value_mapper"}},
	"old_blended_noise":    {numbers: []string{"xz_scale", "y_scale", "xz_factor", "y_factor", "smear_scale_multiplier"}},
	"spline":               {spline: true},
	"range_choice":         {nodes: []string{"input", "when_in_range", "when_out_of_range"}, numbers: []string{"min_inclusive", "max_exclusive"}},
	"interpolated":         {nodes: []string{"argument"}},
	"flat_cache":           {nodes: []string{"argument"}},
	"cache_2d":             {nodes: []string{"argument"}},
	"cache_once":           {nodes: []string{"argument"}},
	"cache_all_in_cell":    {nodes: []string{"argument"}},
	"blend_density":        {nodes: []string{"argument"}},
	"blend_alpha":          {},
	"blend_offset":         {},
	"end_islands":          {},
	"invert":               {nodes: []string{"argument"}},
	"find_top_surface":     {nodes: []string{"density", "upper_bound"}, numbers: []string{"lower_bound", "cell_height"}},
}

// parseNode 解析一个密度函数节点（raw 可以是字符串引用或对象）。
// data 用于解析引用；where 用于报错定位。
func parseNode(data *Data, where string, raw json.RawMessage) (*Node, []Problem) {
	if len(raw) == 0 {
		return nil, []Problem{{Where: where, Detail: "空节点"}}
	}
	// 字符串形式：对密度函数的引用。
	var ref string
	if err := json.Unmarshal(raw, &ref); err == nil {
		name := qualify(ref)
		payload, ok := data.Functions[name]
		if !ok {
			// 原版内置的坐标密度函数没有 JSON 文件，直接按名字识别
			// （DensityFunctions.Y/X/Z）。
			if builtinCoordinateNode(name) {
				return &Node{Type: "coordinate", RefName: name}, nil
			}
			return nil, []Problem{{Where: where, Detail: "缺少密度函数 " + name}}
		}
		node, problems := parseNode(data, name, payload)
		if node != nil {
			node.RefName = name
		}
		return node, problems
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		// 数据文件也可以是纯数字（如 minecraft:zero = 0），等价于 constant。
		var number float64
		if numberErr := json.Unmarshal(raw, &number); numberErr == nil {
			return &Node{Type: "constant", Value: number}, nil
		}
		return nil, []Problem{{Where: where, Detail: "节点不是 JSON 对象"}}
	}
	typeName, _ := object["type"].(string)
	if typeName == "" {
		return nil, []Problem{{Where: where, Detail: "节点缺少 type 字段"}}
	}
	short := strings.TrimPrefix(typeName, "minecraft:")
	schema, ok := nodeSchemas[short]
	if !ok {
		return nil, []Problem{{Where: where, Detail: "不支持的节点类型 " + typeName}}
	}
	node := &Node{Type: short}
	var problems []Problem

	for _, key := range schema.nodes {
		value, ok := object[key]
		if !ok {
			problems = append(problems, Problem{Where: where, Detail: "缺少字段 " + key})
			continue
		}
		payload, err := json.Marshal(value)
		if err != nil {
			problems = append(problems, Problem{Where: where, Detail: "字段 " + key + " 无法重新序列化"})
			continue
		}
		// 算术节点的参数可以是常数也可以是密度函数（原版 JSON 两种都出现，
		// 例如 mul 的 argument1 = 0.64、shifted_noise 的 shift_x = 0.0）。
		if number, ok := value.(float64); ok {
			node.Children = append(node.Children, &Node{Type: "constant", Value: number})
			continue
		}
		child, childProblems := parseNode(data, where+"/"+key, payload)
		problems = append(problems, childProblems...)
		if child != nil {
			node.Children = append(node.Children, child)
		}
	}
	for _, key := range schema.numbers {
		if _, present := object[key]; !present {
			problems = append(problems, Problem{Where: where, Detail: "缺少字段 " + key})
			continue
		}
		value, ok := object[key].(float64)
		if !ok {
			problems = append(problems, Problem{Where: where, Detail: "字段 " + key + " 不是数字"})
			continue
		}
		switch {
		case short == "constant" && key == "argument":
			node.Value = value
		case short == "clamp" && key == "min":
			node.Min = value
		case short == "clamp" && key == "max":
			node.Max = value
		case short == "range_choice" && key == "min_inclusive":
			node.Min = value
		case short == "range_choice" && key == "max_exclusive":
			node.Max = value
		case key == "xz_scale":
			node.XZScale = value
		case key == "y_scale":
			node.YScale = value
		case short == "old_blended_noise":
			node.Legacy[indexOf(schema.numbers, key)] = value
		case short == "find_top_surface" && key == "cell_height":
			node.CellHeight = int(value)
		case short == "find_top_surface" && key == "lower_bound":
			node.LowerBound = int(value)
		case short == "y_clamped_gradient" && key == "from_y":
			node.FromY = value
		case short == "y_clamped_gradient" && key == "to_y":
			node.ToY = value
		case short == "y_clamped_gradient" && key == "from_value":
			node.FromValue = value
		case short == "y_clamped_gradient" && key == "to_value":
			node.ToValue = value
		}
	}
	for _, key := range schema.refs {
		value, ok := object[key]
		if !ok {
			problems = append(problems, Problem{Where: where, Detail: "缺少字段 " + key})
			continue
		}
		name, ok := value.(string)
		if !ok {
			problems = append(problems, Problem{Where: where, Detail: "字段 " + key + " 不是字符串"})
			continue
		}
		noise := qualify(name)
		if _, ok := data.Noises[noise]; !ok {
			problems = append(problems, Problem{Where: where, Detail: "缺少噪声参数 " + noise})
			continue
		}
		node.Noise = noise
	}
	for _, key := range schema.enums {
		value, ok := object[key].(string)
		if !ok {
			problems = append(problems, Problem{Where: where, Detail: "缺少字段 " + key})
			continue
		}
		node.RarityMapper = value
	}
	if schema.spline {
		value, ok := object["spline"]
		if !ok {
			problems = append(problems, Problem{Where: where, Detail: "缺少字段 spline"})
		} else {
			spline, splineProblems := parseSpline(data, where+"/spline", value)
			problems = append(problems, splineProblems...)
			node.Spline = spline
		}
	}
	return node, problems
}

// parseSpline 解析 spline 对象（coordinate + points，points 可递归嵌套）。
func parseSpline(data *Data, where string, value any) (*Spline, []Problem) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, []Problem{{Where: where, Detail: "spline 不是对象"}}
	}
	coordinate, ok := object["coordinate"]
	if !ok {
		return nil, []Problem{{Where: where, Detail: "spline 缺少 coordinate"}}
	}
	payload, err := json.Marshal(coordinate)
	if err != nil {
		return nil, []Problem{{Where: where, Detail: "coordinate 无法序列化"}}
	}
	coordNode, problems := parseNode(data, where+"/coordinate", payload)
	if coordNode == nil {
		return nil, problems
	}
	spline := &Spline{Coordinate: coordNode}
	points, ok := object["points"].([]any)
	if !ok || len(points) == 0 {
		return spline, append(problems, Problem{Where: where, Detail: "spline 缺少 points"})
	}
	for i, entry := range points {
		point, ok := entry.(map[string]any)
		if !ok {
			problems = append(problems, Problem{Where: where, Detail: fmt.Sprintf("第 %d 个控制点不是对象", i)})
			continue
		}
		location, ok := point["location"].(float64)
		if !ok {
			problems = append(problems, Problem{Where: where, Detail: fmt.Sprintf("第 %d 个控制点缺少 location", i)})
			continue
		}
		out := SplinePoint{Location: location}
		derivative, ok := point["derivative"].(float64)
		if !ok {
			problems = append(problems, Problem{Where: where, Detail: fmt.Sprintf("第 %d 个控制点缺少 derivative（本包未实现缺省导数补全）", i)})
			continue
		}
		out.Derivative = derivative
		switch typed := point["value"].(type) {
		case float64:
			out.Value = typed
		case map[string]any:
			sub, subProblems := parseSpline(data, where, typed)
			problems = append(problems, subProblems...)
			out.Sub = sub
		default:
			problems = append(problems, Problem{Where: where, Detail: fmt.Sprintf("第 %d 个控制点 value 非法", i)})
			continue
		}
		spline.Points = append(spline.Points, out)
	}
	sort.SliceStable(spline.Points, func(i, j int) bool {
		return spline.Points[i].Location < spline.Points[j].Location
	})
	return spline, problems
}

// indexOf 返回 key 在列表中的下标（不存在时返回 -1）。
func indexOf(list []string, key string) int {
	for i, item := range list {
		if item == key {
			return i
		}
	}
	return -1
}

// builtinCoordinateNode 报告该名字是否是原版内置的坐标密度函数
// （DensityFunctions.Y/X/Z，没有对应的数据文件）。
func builtinCoordinateNode(name string) bool {
	switch name {
	case "minecraft:x", "minecraft:y", "minecraft:z":
		return true
	}
	return false
}
