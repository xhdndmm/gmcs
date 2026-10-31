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

func TestOverworldCarverData(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, biome := range []string{"minecraft:plains", "minecraft:deep_ocean"} {
		if _, ok := data.BiomeCarvers[biome]; !ok {
			t.Fatalf("缺少群系 %s 的雕刻器引用", biome)
		}
	}
	wantTypes := map[string]bool{"minecraft:cave": false, "minecraft:canyon": false}
	for name, raw := range data.ConfiguredCarvers {
		var configured struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &configured); err != nil {
			t.Fatalf("解析配置雕刻器 %s 失败：%v", name, err)
		}
		if _, ok := wantTypes[configured.Type]; ok {
			wantTypes[configured.Type] = true
		}
	}
	for name, found := range wantTypes {
		if !found {
			t.Errorf("配置雕刻器中缺少类型 %s", name)
		}
	}
	for _, problem := range data.Validate() {
		if strings.Contains(problem.Detail, "配置雕刻器") {
			t.Errorf("群系雕刻器引用无法解析：%s", problem)
		}
	}
}

func TestOverworldFeatureReferences(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(data.ConfiguredFeatures) == 0 || len(data.PlacedFeatures) == 0 {
		t.Fatal("缺少原版 configured_feature 或 placed_feature 数据")
	}
	for _, biome := range []string{"minecraft:plains", "minecraft:deep_ocean", "minecraft:cherry_grove"} {
		raw, ok := data.BiomeFeatures[biome]
		if !ok {
			t.Fatalf("缺少群系 %s 的地物列表", biome)
		}
		var steps [][]string
		if err := json.Unmarshal(raw, &steps); err != nil {
			t.Fatalf("解析群系 %s 地物列表失败：%v", biome, err)
		}
		if len(steps) != 11 {
			t.Fatalf("群系 %s 有 %d 个地物阶段，want 11", biome, len(steps))
		}
	}
	for _, problem := range data.Validate() {
		if strings.Contains(problem.Detail, "缺少放置地物") || strings.Contains(problem.Detail, "缺少配置地物") {
			t.Errorf("地物引用缺失：%s", problem)
		}
	}
}

func TestValidateRejectsMissingPlacedFeatureTarget(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	clone := *data
	var steps [][]string
	if err := json.Unmarshal(data.BiomeFeatures["minecraft:plains"], &steps); err != nil {
		t.Fatal(err)
	}
	var removed string
	for _, step := range steps {
		if len(step) > 0 {
			removed = qualify(step[0])
			break
		}
	}
	if removed == "" {
		t.Fatal("平原群系没有可用于测试的 placed_feature")
	}
	clone.PlacedFeatures = make(map[string]json.RawMessage, len(data.PlacedFeatures)-1)
	for name, raw := range data.PlacedFeatures {
		if name != removed {
			clone.PlacedFeatures[name] = raw
		}
	}
	for _, problem := range clone.Validate() {
		if problem.Where == "minecraft:plains" && strings.Contains(problem.Detail, "缺少放置地物 "+removed) {
			return
		}
	}
	t.Fatalf("删除 %s 后 Validate 未报告引用缺失", removed)
}

func TestParseCarverDefinitions(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := parseCarverDefinitions(data)
	if err != nil {
		t.Fatalf("解析 carver 配置失败：%v", err)
	}
	if len(definitions) != 4 {
		t.Fatalf("解析到 %d 个 carver 配置，want 4", len(definitions))
	}
	cave := definitions["minecraft:cave"]
	if cave.kind != "minecraft:cave" || cave.probability != 0.15 || cave.y.minimum.kind != "above_bottom" || cave.y.maximum.value != 180 {
		t.Fatalf("cave 配置解析错误：%+v", cave)
	}
	canyon := definitions["minecraft:canyon"]
	if canyon.kind != "minecraft:canyon" || canyon.probability != 0.01 || canyon.shape.widthSmoothness != 3 {
		t.Fatalf("canyon 配置解析错误：%+v", canyon)
	}
	replaceable := make(map[string]bool, len(data.CarverReplaceables))
	for _, name := range data.CarverReplaceables {
		replaceable[name] = true
	}
	for _, name := range []string{"minecraft:stone", "minecraft:deepslate", "minecraft:grass_block", "minecraft:iron_ore", "minecraft:water"} {
		if !replaceable[name] {
			t.Errorf("carver replaceable 列表缺少 %s", name)
		}
	}
	randomA := newLegacyRandom(9)
	randomB := newLegacyRandom(9)
	for _, provider := range []floatProvider{cave.yScale, canyon.shape.thickness} {
		first := provider.sample(randomA)
		second := provider.sample(randomB)
		if first != second {
			t.Fatalf("float provider 不确定：%v != %v", first, second)
		}
		if first < provider.minimum || first > provider.maximum {
			t.Fatalf("float provider %v 超出 [%v,%v]", first, provider.minimum, provider.maximum)
		}
	}
	if got := cave.y.sample(newLegacyRandom(12), NoiseSetting{MinY: -64, Height: 384}); got < -56 || got > 180 {
		t.Fatalf("height provider 采样 %d 超出 [-56,180]", got)
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
