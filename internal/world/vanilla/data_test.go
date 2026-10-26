//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestLoadData 验证嵌入的原版世界生成数据可加载且自洽。
func TestLoadData(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if data.Version == "" {
		t.Fatal("数据缺少版本标记")
	}
	if len(data.Noises) == 0 {
		t.Fatal("缺少噪声参数")
	}
	if len(data.Functions) == 0 {
		t.Fatal("缺少密度函数")
	}
	if _, err := data.NoiseSetting("minecraft:overworld"); err != nil {
		t.Fatal(err)
	}
	if problems := data.Validate(); len(problems) > 0 {
		t.Fatalf("数据校验失败：%v", problems)
	}
}

// TestOverworldRouterResolves 验证 overworld 噪声路由表的关键项都能解析为
// 密度函数，且落雷/地形相关的名字与预期一致。
func TestOverworldRouterResolves(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	setting, err := data.NoiseSetting("minecraft:overworld")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"continents", "erosion", "depth", "ridges", "final_density",
		"preliminary_surface_level", "temperature", "vegetation",
		"barrier", "fluid_level_floodedness", "fluid_level_spread", "lava",
		"vein_toggle", "vein_ridged", "vein_gap",
	} {
		if _, ok := setting.Router[key]; !ok {
			t.Fatalf("overworld 噪声路由缺少 %s", key)
		}
	}
	if setting.SeaLevel != 63 {
		t.Fatalf("sea_level = %d, want 63", setting.SeaLevel)
	}
	for _, name := range []string{
		"minecraft:overworld/offset", "minecraft:overworld/factor",
		"minecraft:overworld/jaggedness", "minecraft:overworld/sloped_cheese",
		"minecraft:overworld/ridges_folded",
	} {
		if _, ok := data.Function(name); !ok {
			t.Fatalf("缺少密度函数 %s", name)
		}
	}
}

// TestNoiseParametersShape 验证噪声参数（firstOctave/amplitudes）的关键不变量：
// 振幅非零，且 continents 的倍频跨度符合原版定义。
func TestNoiseParametersShape(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	continents, ok := data.Noises["minecraft:continentalness"]
	if !ok {
		t.Fatal("缺少噪声参数 minecraft:continentalness")
	}
	if continents.FirstOctave != -9 || len(continents.Amplitudes) != 9 {
		t.Fatalf("continentalness 参数 = %+v，期望 firstOctave=-9、9 个倍频", continents)
	}
	for name, params := range data.Noises {
		if len(params.Amplitudes) == 0 {
			t.Fatalf("%s 缺少振幅", name)
		}
	}
}

// TestUnsupportedNodeTypeIsRejected 验证未知节点类型会让校验失败
// （避免将来升级数据包后静默地把新节点当成 0）。
func TestUnsupportedNodeTypeIsRejected(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	clone := &Data{
		Version:       data.Version,
		NoiseSettings: data.NoiseSettings,
		Functions:     map[string]json.RawMessage{},
		Noises:        data.Noises,
		BiomeSources:  data.BiomeSources,
	}
	for name, raw := range data.Functions {
		clone.Functions[name] = raw
	}
	clone.Functions["minecraft:overworld/continents"] = json.RawMessage(`{"type":"minecraft:not_a_real_type"}`)
	problems := clone.Validate()
	if len(problems) == 0 {
		t.Fatal("未知节点类型未被拒绝")
	}
	found := false
	for _, problem := range problems {
		if strings.Contains(problem.Detail, "不支持的节点类型") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("错误信息未指出未知类型：%v", problems)
	}
}

// TestMissingNoiseReferenceIsRejected 验证缺少噪声参数时校验失败。
func TestMissingNoiseReferenceIsRejected(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	clone := &Data{
		Version:       data.Version,
		NoiseSettings: data.NoiseSettings,
		Functions:     data.Functions,
		Noises:        map[string]NoiseParameters{},
		BiomeSources:  data.BiomeSources,
	}
	problems := clone.Validate()
	if len(problems) == 0 {
		t.Fatal("缺少噪声参数未被拒绝")
	}
	if !strings.Contains(problems[0].Detail, "缺少噪声参数") {
		t.Fatalf("错误信息不符合预期：%v", problems[0])
	}
}
