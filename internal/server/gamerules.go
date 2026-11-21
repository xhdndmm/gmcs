//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"encoding/json"
	"fmt"
	"strconv"
	"sync/atomic"
)

// 游戏规则（GameRules）的最小可用子集。规则名与 1.21.11 客户端一致；
// 数值/默认值来自原版 GameRules 类（布尔规则默认 true/false，
// fire_spread_radius_around_player 默认 128，-1 表示不限制）。
//
// 这些规则存放在 level.json 的 gamerules 字段中，与 gmcs 的世界时间/天气
// 一样是 gmcs 自定义持久化（不是原版 level.dat）。未列出的原版规则不实现，
// /gamerule 会显式报错，而不是静默忽略。

type gameRuleKind uint8

const (
	gameRuleBool gameRuleKind = iota
	gameRuleInt
)

type gameRuleSpec struct {
	name        string
	kind        gameRuleKind
	defaultBool bool
	defaultInt  int32
	minInt      int32
	maxInt      int32
}

var gameRuleSpecs = []gameRuleSpec{
	{name: "doWeatherCycle", kind: gameRuleBool, defaultBool: true},
	{name: "doDaylightCycle", kind: gameRuleBool, defaultBool: true},
	{name: "keepInventory", kind: gameRuleBool, defaultBool: false},
	{name: "mobGriefing", kind: gameRuleBool, defaultBool: true},
	{name: "fire_spread_radius_around_player", kind: gameRuleInt, defaultInt: 128, minInt: -1, maxInt: 1 << 20},
	{name: "naturalRegeneration", kind: gameRuleBool, defaultBool: true},
	{name: "doMobSpawning", kind: gameRuleBool, defaultBool: true},
}

func gameRuleSpecByName(name string) (gameRuleSpec, bool) {
	for _, spec := range gameRuleSpecs {
		if spec.name == name {
			return spec, true
		}
	}
	return gameRuleSpec{}, false
}

type gameRuleState struct {
	doWeatherCycle               atomic.Bool
	doDaylightCycle              atomic.Bool
	keepInventory                atomic.Bool
	mobGriefing                  atomic.Bool
	naturalRegeneration          atomic.Bool
	doMobSpawning                atomic.Bool
	fireSpreadRadiusAroundPlayer atomic.Int32
}

func (r *gameRuleState) reset() {
	r.doWeatherCycle.Store(true)
	r.doDaylightCycle.Store(true)
	r.keepInventory.Store(false)
	r.mobGriefing.Store(true)
	r.naturalRegeneration.Store(true)
	r.doMobSpawning.Store(true)
	r.fireSpreadRadiusAroundPlayer.Store(128)
}

// get 返回规则的字符串值。
func (r *gameRuleState) get(name string) (string, bool) {
	spec, ok := gameRuleSpecByName(name)
	if !ok {
		return "", false
	}
	switch name {
	case "doWeatherCycle":
		return strconv.FormatBool(r.doWeatherCycle.Load()), true
	case "doDaylightCycle":
		return strconv.FormatBool(r.doDaylightCycle.Load()), true
	case "keepInventory":
		return strconv.FormatBool(r.keepInventory.Load()), true
	case "mobGriefing":
		return strconv.FormatBool(r.mobGriefing.Load()), true
	case "naturalRegeneration":
		return strconv.FormatBool(r.naturalRegeneration.Load()), true
	case "doMobSpawning":
		return strconv.FormatBool(r.doMobSpawning.Load()), true
	case "fire_spread_radius_around_player":
		return strconv.FormatInt(int64(r.fireSpreadRadiusAroundPlayer.Load()), 10), true
	default:
		_ = spec
		return "", false
	}
}

// set 设置规则；返回错误时保留旧值。
func (r *gameRuleState) set(name, value string) error {
	spec, ok := gameRuleSpecByName(name)
	if !ok {
		return fmt.Errorf("未知游戏规则 %q", name)
	}
	if spec.kind == gameRuleBool {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("规则 %s 需要 true/false", name)
		}
		switch name {
		case "doWeatherCycle":
			r.doWeatherCycle.Store(parsed)
		case "doDaylightCycle":
			r.doDaylightCycle.Store(parsed)
		case "keepInventory":
			r.keepInventory.Store(parsed)
		case "mobGriefing":
			r.mobGriefing.Store(parsed)
		case "naturalRegeneration":
			r.naturalRegeneration.Store(parsed)
		case "doMobSpawning":
			r.doMobSpawning.Store(parsed)
		default:
			return fmt.Errorf("规则 %s 不是布尔规则", name)
		}
		return nil
	}
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil || int32(parsed) < spec.minInt || int32(parsed) > spec.maxInt {
		return fmt.Errorf("规则 %s 需要 %d..%d 的整数", name, spec.minInt, spec.maxInt)
	}
	r.fireSpreadRadiusAroundPlayer.Store(int32(parsed))
	return nil
}

// raw 返回用于 level.json 的规则值。
func (r *gameRuleState) raw() map[string]json.RawMessage {
	values := make(map[string]json.RawMessage, len(gameRuleSpecs))
	for _, spec := range gameRuleSpecs {
		value, _ := r.get(spec.name)
		if spec.kind == gameRuleBool {
			values[spec.name] = json.RawMessage(value)
		} else {
			values[spec.name] = json.RawMessage(value)
		}
	}
	return values
}

// load 从 level.json 恢复规则；未知规则名报错，避免拼写错误被静默忽略。
func (r *gameRuleState) load(values map[string]json.RawMessage) error {
	for name, raw := range values {
		spec, ok := gameRuleSpecByName(name)
		if !ok {
			return fmt.Errorf("level.json 包含未知游戏规则 %q", name)
		}
		if spec.kind == gameRuleBool {
			var value bool
			if err := json.Unmarshal(raw, &value); err != nil {
				return fmt.Errorf("解析游戏规则 %s：%w", name, err)
			}
			if err := r.set(name, strconv.FormatBool(value)); err != nil {
				return err
			}
			continue
		}
		var value int32
		if err := json.Unmarshal(raw, &value); err != nil {
			return fmt.Errorf("解析游戏规则 %s：%w", name, err)
		}
		if err := r.set(name, strconv.FormatInt(int64(value), 10)); err != nil {
			return err
		}
	}
	return nil
}
