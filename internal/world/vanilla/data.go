//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

// Package vanilla 承载“按原版算法生成区块”的实现。
//
// 当前进度（见 docs/TODO.md 的分阶段计划）：原版世界生成数据提取与校验、
// Xoroshiro 随机源、ImprovedNoise/PerlinNoise/NormalNoise、密度函数图求值、
// 区块级密度 lattice 采样、含水层逐方块判定（密度 + 水/岩浆/空气）、
// 多噪声群系源，以及**表面规则**都已经实现并有测试；
// Router.GenerateChunk 已接入 cave/canyon 雕刻器初版，但官方逐方块差异尚未收敛；
// 地物与结构尚未实现，服务器仍使用 internal/world 中的自研生成器。
//
// 数据由 tools/genworldgen 从官方 jar 提取并压缩为 data/worldgen.json.gz
// （overworld 的噪声设置/密度函数/噪声参数/群系气候）；overworld 的多噪声
// 群系参数表是代码内建的，由 tools/vgoracle/BiomeParameters.java 反射导出为
// data/biome_parameters.json.gz。仓库里不保存原始数据包文件。
package vanilla

import (
	"bytes"
	"compress/gzip"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

//go:embed data/worldgen.json.gz data/biome_parameters.json.gz
var dataFiles embed.FS

// DataFileName 是嵌入的数据包文件名。
const DataFileName = "data/worldgen.json.gz"

// NoiseParameters 是一条噪声参数（原版 data/minecraft/worldgen/noise/*.json）：
// firstOctave 与每个倍频的振幅。
type NoiseParameters struct {
	FirstOctave int
	Amplitudes  []float64
}

// Data 是加载后的原版世界生成数据。
type Data struct {
	// Version 是数据来源版本（如 "1.21.11"）。
	Version string
	// NoiseSettings 是噪声设置（含噪声路由表与表面规则），按注册名索引。
	NoiseSettings map[string]json.RawMessage
	// Functions 是密度函数图，按注册名索引（原始 JSON）。
	Functions map[string]json.RawMessage
	// Noises 是噪声参数，按注册名索引。
	Noises map[string]NoiseParameters
	// BiomeSources 是多噪声群系源的参数范围表（原始 JSON）。
	BiomeSources map[string]json.RawMessage
	// ConfiguredCarvers 是配置雕刻器定义，按注册名索引。
	ConfiguredCarvers map[string]json.RawMessage
	// BiomeCarvers 是群系的雕刻器引用列表，按群系注册名索引。
	BiomeCarvers map[string]json.RawMessage
	// CarverReplaceables 是原版 overworld_carver_replaceables 标签递归展开结果。
	CarverReplaceables []string
	// Biomes 是群系气候数据（只保留 temperature/downfall/has_precipitation/
	// temperature_modifier，原始 JSON）。
	Biomes map[string]json.RawMessage
}

// Problem 描述一条数据一致性问题。
type Problem struct {
	Where  string // 出错位置（如 minecraft:overworld/final_density）
	Detail string
}

func (p Problem) String() string { return p.Where + ": " + p.Detail }

// 内置的加载结果缓存（数据是只读常量，加载一次即可）。
var (
	loadedData *Data
	loadedErr  error
)

// Load 加载并校验嵌入的原版世界生成数据（首次调用后缓存结果）。
func Load() (*Data, error) {
	if loadedData != nil || loadedErr != nil {
		return loadedData, loadedErr
	}
	data, err := loadFrom(dataFiles, DataFileName)
	if err != nil {
		loadedErr = err
		return nil, err
	}
	if problems := data.Validate(); len(problems) > 0 {
		loadedErr = fmt.Errorf("原版世界生成数据校验失败：%s", problems[0].String())
		return nil, loadedErr
	}
	loadedData = data
	return data, nil
}

// rawBundle 是数据文件的序列化结构（字段名与 tools/genworldgen 一致）。
type rawBundle struct {
	Version            string                     `json:"version"`
	Source             string                     `json:"source"`
	NoiseSettings      map[string]json.RawMessage `json:"noise_settings"`
	Functions          map[string]json.RawMessage `json:"density_functions"`
	Noises             map[string]json.RawMessage `json:"noises"`
	BiomeSources       map[string]json.RawMessage `json:"multi_noise_biome_sources"`
	ConfiguredCarvers  map[string]json.RawMessage `json:"configured_carvers"`
	BiomeCarvers       map[string]json.RawMessage `json:"biome_carvers"`
	CarverReplaceables []string                   `json:"carver_replaceables"`
	Biomes             map[string]json.RawMessage `json:"biomes"`
}

// loadFrom 从文件系统读取并解析数据包。
func loadFrom(fs embed.FS, name string) (*Data, error) {
	compressed, err := fs.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("读取 %s 失败：%w", name, err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, fmt.Errorf("解压 %s 失败：%w", name, err)
	}
	defer reader.Close()
	payload, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("读取 %s 内容失败：%w", name, err)
	}
	var raw rawBundle
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, fmt.Errorf("解析 %s 失败：%w", name, err)
	}
	data := &Data{
		Version:            raw.Version,
		NoiseSettings:      raw.NoiseSettings,
		Functions:          raw.Functions,
		Noises:             make(map[string]NoiseParameters, len(raw.Noises)),
		BiomeSources:       raw.BiomeSources,
		ConfiguredCarvers:  raw.ConfiguredCarvers,
		BiomeCarvers:       raw.BiomeCarvers,
		CarverReplaceables: raw.CarverReplaceables,
		Biomes:             raw.Biomes,
	}
	for name, payload := range raw.Noises {
		var params struct {
			FirstOctave int       `json:"firstOctave"`
			Amplitudes  []float64 `json:"amplitudes"`
		}
		if err := json.Unmarshal(payload, &params); err != nil {
			return nil, fmt.Errorf("解析噪声参数 %s 失败：%w", name, err)
		}
		data.Noises[name] = NoiseParameters{FirstOctave: params.FirstOctave, Amplitudes: params.Amplitudes}
	}
	return data, nil
}

// Validate 检查数据包的结构一致性：
//   - 每个密度函数都能解析（类型受支持、必需字段齐全、引用存在）；
//   - 噪声参数的 amplitudes 非空且没有 0 振幅；
//   - overworld 噪声设置的路由表每一项都能解析。
//
// 校验失败意味着数据文件与实现不匹配（例如升级数据包后出现新节点类型），
// 必须显式报错：静默地把未知节点当成 0 计算会产出错误地形。
func (d *Data) Validate() []Problem {
	var problems []Problem
	for biome, raw := range d.BiomeCarvers {
		var names []string
		if err := json.Unmarshal(raw, &names); err != nil {
			problems = append(problems, Problem{Where: biome, Detail: fmt.Sprintf("解析群系雕刻器失败：%v", err)})
			continue
		}
		for _, name := range names {
			name = qualify(name)
			if _, ok := d.ConfiguredCarvers[name]; !ok {
				problems = append(problems, Problem{Where: biome, Detail: "缺少配置雕刻器 " + name})
			}
		}
	}
	noiseSettings, err := d.NoiseSetting("minecraft:overworld")
	if err != nil {
		return append(problems, Problem{Where: "minecraft:overworld", Detail: err.Error()})
	}
	names := make([]string, 0, len(d.Functions))
	for name := range d.Functions {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		_, functionProblems := parseNode(d, name, d.Functions[name])
		problems = append(problems, functionProblems...)
	}
	noiseNames := make([]string, 0, len(d.Noises))
	for name := range d.Noises {
		noiseNames = append(noiseNames, name)
	}
	sort.Strings(noiseNames)
	for _, name := range noiseNames {
		params := d.Noises[name]
		if len(params.Amplitudes) == 0 {
			problems = append(problems, Problem{Where: name, Detail: "amplitudes 为空"})
		}
	}
	routerKeys := make([]string, 0, len(noiseSettings.Router))
	for key := range noiseSettings.Router {
		routerKeys = append(routerKeys, key)
	}
	sort.Strings(routerKeys)
	for _, key := range routerKeys {
		_, routerProblems := parseNode(d, "minecraft:overworld/"+key, noiseSettings.Router[key])
		problems = append(problems, routerProblems...)
	}
	return problems
}

// qualify 把不带命名空间的引用补成 minecraft: 前缀。
func qualify(ref string) string {
	if ref == "" {
		return ""
	}
	if strings.Contains(ref, ":") {
		return ref
	}
	return "minecraft:" + ref
}

// NoiseSetting 是一个噪声设置（含噪声路由表与表面规则）。
type NoiseSetting struct {
	Name     string
	SeaLevel int
	// MinY / Height 是维度高度范围（noise.min_y 与 noise.height）。
	MinY   int
	Height int
	// CellWidth / CellHeight 是区块内插值网格的单元格尺寸：
	// 原版 NoiseChunk 用 4*size_horizontal 与 4*size_vertical。
	CellWidth  int
	CellHeight int
	// DefaultBlock / DefaultFluid 是密度为正与含水层判定为流体时使用的方块注册名
	// （原版 NoiseGeneratorSettings.defaultBlock / defaultFluid）。
	DefaultBlock string
	DefaultFluid string
	// AquifersEnabled 对应原版 NoiseGeneratorSettings.isAquifersEnabled()。
	AquifersEnabled bool
	Router          map[string]json.RawMessage
	Raw             json.RawMessage
}

// blockStateName 从原版方块状态 JSON 中取出注册名。
// 数据包里的写法可能是 {"Name": "minecraft:stone"} 或纯字符串。
func blockStateName(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var name string
	if err := json.Unmarshal(raw, &name); err == nil {
		return name
	}
	var state struct {
		Name string `json:"Name"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return ""
	}
	return state.Name
}

// NoiseSetting 返回指定名称的噪声设置。
func (d *Data) NoiseSetting(name string) (NoiseSetting, error) {
	raw, ok := d.NoiseSettings[name]
	if !ok {
		return NoiseSetting{}, fmt.Errorf("缺少噪声设置 %s", name)
	}
	var parsed struct {
		SeaLevel int `json:"sea_level"`
		Noise    struct {
			MinY           int `json:"min_y"`
			Height         int `json:"height"`
			SizeHorizontal int `json:"size_horizontal"`
			SizeVertical   int `json:"size_vertical"`
		} `json:"noise"`
		Router map[string]json.RawMessage `json:"noise_router"`
		// DefaultBlock / DefaultFluid 是方块状态 JSON，AquifersEnabled 决定是否
		// 启用含水层（原版 NoiseGeneratorSettings 的对应字段）。
		DefaultBlock    json.RawMessage `json:"default_block"`
		DefaultFluid    json.RawMessage `json:"default_fluid"`
		AquifersEnabled bool            `json:"aquifers_enabled"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return NoiseSetting{}, fmt.Errorf("解析噪声设置 %s 失败：%w", name, err)
	}
	if len(parsed.Router) == 0 {
		return NoiseSetting{}, fmt.Errorf("噪声设置 %s 缺少 noise_router", name)
	}
	// 原版的 size_horizontal / size_vertical 可以缺省（缺省视为 1 / 1）。
	sizeHorizontal := parsed.Noise.SizeHorizontal
	if sizeHorizontal <= 0 {
		sizeHorizontal = 1
	}
	sizeVertical := parsed.Noise.SizeVertical
	if sizeVertical <= 0 {
		sizeVertical = 1
	}
	return NoiseSetting{
		Name:            name,
		SeaLevel:        parsed.SeaLevel,
		MinY:            parsed.Noise.MinY,
		Height:          parsed.Noise.Height,
		CellWidth:       4 * sizeHorizontal,
		CellHeight:      4 * sizeVertical,
		DefaultBlock:    blockStateName(parsed.DefaultBlock),
		DefaultFluid:    blockStateName(parsed.DefaultFluid),
		AquifersEnabled: parsed.AquifersEnabled,
		Router:          parsed.Router,
		Raw:             raw,
	}, nil
}

// Function 返回指定名称的密度函数原始 JSON。
func (d *Data) Function(name string) (json.RawMessage, bool) {
	raw, ok := d.Functions[name]
	return raw, ok
}

// BiomeClimates 解析群系气候数据（温度、降水量、降水标记、温度修正器）。
func (d *Data) BiomeClimates() map[string]biomeClimate {
	climates := make(map[string]biomeClimate, len(d.Biomes))
	for name, raw := range d.Biomes {
		var parsed struct {
			Temperature      float32 `json:"temperature"`
			Downfall         float32 `json:"downfall"`
			HasPrecipitation *bool   `json:"has_precipitation"`
			Modifier         string  `json:"temperature_modifier"`
		}
		if err := json.Unmarshal(raw, &parsed); err != nil {
			continue
		}
		climate := biomeClimate{
			temperature: parsed.Temperature,
			downfall:    parsed.Downfall,
			frozen:      parsed.Modifier == "frozen",
		}
		if parsed.HasPrecipitation != nil {
			climate.hasPrecipitation = *parsed.HasPrecipitation
		}
		climates[name] = climate
	}
	return climates
}
