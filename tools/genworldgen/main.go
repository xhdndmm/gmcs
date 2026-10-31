//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

// genworldgen 从 Minecraft 服务端/客户端 jar 提取原版世界生成数据，
// 打包为 internal/world/vanilla/data/worldgen.json.gz。
//
// 提取内容（全部是原版数据包中的 JSON，按注册名索引）：
//   - data/minecraft/worldgen/noise_settings/*.json（噪声设置与噪声路由表）
//   - data/minecraft/worldgen/density_function/**.json（密度函数图）
//   - data/minecraft/worldgen/noise/*.json（噪声参数：firstOctave/amplitudes）
//   - data/minecraft/worldgen/multi_noise_biome_source_parameter_list/*.json
//     （多噪声群系源的参数范围表）
//   - data/minecraft/worldgen/configured_carver/*.json 与群系 carvers 引用
//   - configured_feature / placed_feature 定义与群系 features 阶段引用
//
// 只保留原版 overworld 相关条目，避免把整个数据包塞进仓库；条目以
// “minecraft:<相对路径去扩展名>” 作为键（如 minecraft:overworld/continents）。
//
// 用法：
//
//	go run ./tools/genworldgen -jar <server.jar>
//	go run ./tools/genworldgen -jar <server.jar> -out internal/world/vanilla/data/worldgen.json.gz
package main

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
)

// bundle 是打包后的数据文件结构。
type bundle struct {
	Version            string                     `json:"version"`
	Source             string                     `json:"source"`
	NoiseSettings      map[string]json.RawMessage `json:"noise_settings"`
	Functions          map[string]json.RawMessage `json:"density_functions"`
	Noises             map[string]json.RawMessage `json:"noises"`
	BiomeSources       map[string]json.RawMessage `json:"multi_noise_biome_sources"`
	ConfiguredCarvers  map[string]json.RawMessage `json:"configured_carvers"`
	BiomeCarvers       map[string]json.RawMessage `json:"biome_carvers"`
	ConfiguredFeatures map[string]json.RawMessage `json:"configured_features"`
	PlacedFeatures     map[string]json.RawMessage `json:"placed_features"`
	BiomeFeatures      map[string]json.RawMessage `json:"biome_features"`
	CarverReplaceables []string                   `json:"carver_replaceables"`
	// Biomes 只保留气候相关字段（temperature/downfall/has_precipitation/
	// temperature_modifier），地表规则的 temperature 条件与降雪判定需要它们。
	Biomes map[string]json.RawMessage `json:"biomes"`
}

func main() {
	jarPath := flag.String("jar", "", "Minecraft 服务端（或客户端）jar 路径")
	out := flag.String("out", "internal/world/vanilla/data/worldgen.json.gz", "输出文件")
	version := flag.String("version", "", "版本标记（默认从 jar 内的 version.json 读取）")
	flag.Parse()
	if *jarPath == "" {
		fmt.Fprintln(os.Stderr, "缺少 -jar 参数")
		flag.Usage()
		os.Exit(2)
	}
	reader, err := zip.OpenReader(*jarPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "打开 jar 失败: %v\n", err)
		os.Exit(1)
	}
	defer reader.Close()

	files := make(map[string]*zip.File, len(reader.File))
	for _, file := range reader.File {
		files[file.Name] = file
	}
	// 1.21 起服务端 jar 是 bundler：真实类与数据在 META-INF/versions/<ver>/ 内，
	// 因此这里同时接受带前缀与不带前缀两种布局。
	prefixes := []string{"", "META-INF/versions/"}
	var dataPrefix string
	for _, prefix := range prefixes {
		if _, ok := files[prefix+"data/minecraft/worldgen/noise_settings/overworld.json"]; ok {
			dataPrefix = prefix + "data/minecraft/"
			break
		}
	}
	if dataPrefix == "" {
		// bundler 的版本目录名不固定，扫描一次。
		for name := range files {
			if strings.HasPrefix(name, "META-INF/versions/") &&
				strings.HasSuffix(name, "/data/minecraft/worldgen/noise_settings/overworld.json") {
				dataPrefix = name[:strings.Index(name, "data/minecraft/")+len("data/minecraft/")]
				break
			}
		}
	}
	if dataPrefix == "" {
		fmt.Fprintln(os.Stderr, "jar 内缺少 data/minecraft/worldgen/noise_settings/overworld.json")
		os.Exit(1)
	}

	result := bundle{
		Version:            *version,
		Source:             path.Base(*jarPath),
		NoiseSettings:      map[string]json.RawMessage{},
		Functions:          map[string]json.RawMessage{},
		Noises:             map[string]json.RawMessage{},
		BiomeSources:       map[string]json.RawMessage{},
		ConfiguredCarvers:  map[string]json.RawMessage{},
		BiomeCarvers:       map[string]json.RawMessage{},
		ConfiguredFeatures: map[string]json.RawMessage{},
		PlacedFeatures:     map[string]json.RawMessage{},
		BiomeFeatures:      map[string]json.RawMessage{},
		Biomes:             map[string]json.RawMessage{},
	}
	if result.Version == "" {
		if file, ok := files[dataPrefix+"version.json"]; ok {
			if raw, err := readFile(file); err == nil {
				var meta struct {
					Name string `json:"name"`
				}
				if json.Unmarshal(raw, &meta) == nil {
					result.Version = meta.Name
				}
			}
		}
	}

	collect := func(dir string, keep func(key string) bool, dst map[string]json.RawMessage) error {
		prefix := dataPrefix + "worldgen/" + dir + "/"
		names := make([]string, 0, 32)
		for name := range files {
			if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, ".json") {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		for _, name := range names {
			key := "minecraft:" + strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".json")
			if keep != nil && !keep(key) {
				continue
			}
			raw, err := readFile(files[name])
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			var probe any
			if err := json.Unmarshal(raw, &probe); err != nil {
				return fmt.Errorf("%s: 非法 JSON: %w", name, err)
			}
			// 重新压缩为紧凑形式，避免把缩进/换行带进产物。
			compact, err := json.Marshal(probe)
			if err != nil {
				return err
			}
			dst[key] = compact
		}
		return nil
	}
	// 密度函数不做过滤：根目录下的 shift_x/shift_z 等被 overworld 路由引用，
	// 只保留 overworld 相关条目会漏掉它们。
	// 只保留 overworld 的噪声设置与群系参数表（下界/末地在各自批次再提取）；
	// 密度函数不做过滤：根目录下的 shift_x/shift_z 等被 overworld 路由引用，
	// 只保留 overworld/ 子目录会漏掉它们。
	overworldOnly := func(key string) bool { return key == "minecraft:overworld" }
	if err := collect("noise_settings", overworldOnly, result.NoiseSettings); err != nil {
		fmt.Fprintf(os.Stderr, "提取 noise_settings 失败: %v\n", err)
		os.Exit(1)
	}
	if err := collect("density_function", nil, result.Functions); err != nil {
		fmt.Fprintf(os.Stderr, "提取 density_function 失败: %v\n", err)
		os.Exit(1)
	}
	if err := collect("noise", nil, result.Noises); err != nil {
		fmt.Fprintf(os.Stderr, "提取 noise 失败: %v\n", err)
		os.Exit(1)
	}
	if err := collect("multi_noise_biome_source_parameter_list", overworldOnly, result.BiomeSources); err != nil {
		fmt.Fprintf(os.Stderr, "提取群系参数表失败: %v\n", err)
		os.Exit(1)
	}
	if err := collect("configured_carver", nil, result.ConfiguredCarvers); err != nil {
		fmt.Fprintf(os.Stderr, "提取 configured_carver 失败: %v\n", err)
		os.Exit(1)
	}
	if err := collect("configured_feature", nil, result.ConfiguredFeatures); err != nil {
		fmt.Fprintf(os.Stderr, "提取 configured_feature 失败: %v\n", err)
		os.Exit(1)
	}
	if err := collect("placed_feature", nil, result.PlacedFeatures); err != nil {
		fmt.Fprintf(os.Stderr, "提取 placed_feature 失败: %v\n", err)
		os.Exit(1)
	}
	result.CarverReplaceables, err = extractCarverReplaceables(files, dataPrefix)
	if err != nil {
		fmt.Fprintf(os.Stderr, "提取 overworld_carver_replaceables 失败: %v\n", err)
		os.Exit(1)
	}
	// 群系只保留气候字段：完整群系 JSON 含特征列表/生成设置，体积大且与地形无关。
	biomeData := map[string]json.RawMessage{}
	if err := collect("biome", nil, biomeData); err != nil {
		fmt.Fprintf(os.Stderr, "提取群系失败: %v\n", err)
		os.Exit(1)
	}
	climateKeys := []string{"temperature", "downfall", "has_precipitation", "temperature_modifier"}
	for name, raw := range biomeData {
		var full map[string]json.RawMessage
		if err := json.Unmarshal(raw, &full); err != nil {
			fmt.Fprintf(os.Stderr, "解析群系 %s 失败: %v\n", name, err)
			os.Exit(1)
		}
		trimmed := map[string]json.RawMessage{}
		for _, key := range climateKeys {
			if value, ok := full[key]; ok {
				trimmed[key] = value
			}
		}
		compact, err := json.Marshal(trimmed)
		if err != nil {
			fmt.Fprintf(os.Stderr, "序列化群系 %s 失败: %v\n", name, err)
			os.Exit(1)
		}
		result.Biomes[name] = compact
		if features, ok := full["features"]; ok {
			result.BiomeFeatures[name] = features
		}
		if carvers, ok := full["carvers"]; ok {
			var names []string
			if err := json.Unmarshal(carvers, &names); err == nil {
				// Overworld biomes use the carver list format. Nether biomes use a
				// single string reference and are outside this extraction stage.
				result.BiomeCarvers[name] = carvers
			}
		}
	}

	payload, err := json.Marshal(result)
	if err != nil {
		fmt.Fprintf(os.Stderr, "序列化失败: %v\n", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(path.Dir(*out), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "创建目录失败: %v\n", err)
		os.Exit(1)
	}
	var buf bytes.Buffer
	writer, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化 gzip 失败: %v\n", err)
		os.Exit(1)
	}
	if _, err := writer.Write(payload); err != nil {
		fmt.Fprintf(os.Stderr, "写入 gzip 失败: %v\n", err)
		os.Exit(1)
	}
	if err := writer.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "关闭 gzip 失败: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, buf.Bytes(), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "写入 %s 失败: %v\n", *out, err)
		os.Exit(1)
	}
	fmt.Printf("完成：%s（%d 条噪声设置 / %d 条密度函数 / %d 条噪声参数 / %d 条群系参数表 / %d 条配置雕刻器 / %d 条群系雕刻器 / %d 条配置地物 / %d 条放置地物 / %d 条群系地物 / %d 条群系气候，原始 %d 字节、压缩后 %d 字节）\n",
		*out, len(result.NoiseSettings), len(result.Functions), len(result.Noises), len(result.BiomeSources), len(result.ConfiguredCarvers), len(result.BiomeCarvers), len(result.ConfiguredFeatures), len(result.PlacedFeatures), len(result.BiomeFeatures), len(result.Biomes),
		len(payload), buf.Len())
}

// readFile 读取 zip 条目内容。
func readFile(file *zip.File) ([]byte, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func extractCarverReplaceables(files map[string]*zip.File, dataPrefix string) ([]string, error) {
	const rootTag = "minecraft:overworld_carver_replaceables"
	base := strings.TrimSuffix(dataPrefix, "minecraft/")
	pending := []string{rootTag}
	seenTags := make(map[string]bool)
	blocks := make(map[string]bool)
	for len(pending) > 0 {
		tagName := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seenTags[tagName] {
			continue
		}
		seenTags[tagName] = true
		namespace, pathName, found := strings.Cut(tagName, ":")
		if !found {
			namespace, pathName = "minecraft", namespace
		}
		fileName := base + namespace + "/tags/block/" + pathName + ".json"
		file, ok := files[fileName]
		if !ok {
			return nil, fmt.Errorf("缺少标签文件 %s", fileName)
		}
		payload, err := readFile(file)
		if err != nil {
			return nil, fmt.Errorf("读取 %s 失败：%w", fileName, err)
		}
		var tag struct {
			Values []json.RawMessage `json:"values"`
		}
		if err := json.Unmarshal(payload, &tag); err != nil {
			return nil, fmt.Errorf("解析 %s 失败：%w", fileName, err)
		}
		for _, rawValue := range tag.Values {
			var value string
			if err := json.Unmarshal(rawValue, &value); err != nil {
				var entry struct {
					ID string `json:"id"`
				}
				if err := json.Unmarshal(rawValue, &entry); err != nil || entry.ID == "" {
					return nil, fmt.Errorf("%s 含无效标签值 %s", fileName, rawValue)
				}
				value = entry.ID
			}
			if strings.HasPrefix(value, "#") {
				pending = append(pending, strings.TrimPrefix(value, "#"))
			} else {
				blocks[value] = true
			}
		}
	}
	result := make([]string, 0, len(blocks))
	for name := range blocks {
		result = append(result, name)
	}
	sort.Strings(result)
	return result, nil
}
