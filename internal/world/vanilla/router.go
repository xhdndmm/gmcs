//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"fmt"
	"sort"
)

// Router 是某个噪声设置（维度）的路由表求值入口。
//
// 它把 noise_router 里的每一项解析成密度函数图，并绑定一个按世界种子构造的
// 求值器。当前提供的是**密度值**层面的能力（final_density / Solid）：
// 含水层、表面规则、雕刻器与地物属于后续阶段。
type Router struct {
	setting   NoiseSetting
	evaluator *Evaluator
	nodes     map[string]*Node
	keys      []string
}

// NewRouter 解析指定噪声设置的全部路由项。
func NewRouter(data *Data, settingName string, seed int64) (*Router, error) {
	setting, err := data.NoiseSetting(settingName)
	if err != nil {
		return nil, err
	}
	registry := NewNoiseRegistry(data, seed)
	router := &Router{
		setting:   setting,
		evaluator: NewEvaluatorWithCells(registry, setting.CellWidth, setting.CellHeight),
		nodes:     make(map[string]*Node, len(setting.Router)),
	}
	for key := range setting.Router {
		router.keys = append(router.keys, key)
	}
	sort.Strings(router.keys)
	for _, key := range router.keys {
		node, problems := parseNode(data, settingName+"/"+key, setting.Router[key])
		if len(problems) > 0 {
			return nil, fmt.Errorf("%s 解析失败：%s", problems[0].Where, problems[0].Detail)
		}
		router.nodes[key] = node
	}
	return router, nil
}

// Setting 返回该路由表对应的噪声设置。
func (r *Router) Setting() NoiseSetting {
	return r.setting
}

// Evaluator 返回底层求值器（供诊断与基准使用）。
func (r *Router) Evaluator() *Evaluator {
	return r.evaluator
}

// Field 返回路由表中某一项的取值。name 必须是最终密度函数图（如 final_density、
// continentalness），未定义的键是编程错误，会 panic。
func (r *Router) Field(name string, ctx Context) float64 {
	node, ok := r.nodes[name]
	if !ok {
		panic("vanilla: 路由表中没有 " + name)
	}
	return r.evaluator.Evaluate(node, ctx)
}

// FinalDensity 返回 final_density 的取值（> 0 表示实心）。
func (r *Router) FinalDensity(ctx Context) float64 {
	return r.Field("final_density", ctx)
}

// Solid 报告该坐标是否实心：final_density > 0。
// 注意：这与原版"是否生成石头"还不完全等价——低于海平面时原版还要经过含水层
// 判定（水/空气），那部分属于阶段 3e 的后续工作。
func (r *Router) Solid(x, y, z int) bool {
	return r.FinalDensity(Context{X: x, Y: y, Z: z}) > 0
}
