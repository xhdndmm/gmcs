// genregistries 从 Minecraft 客户端 jar 的内置数据包与官方服务端数据生成器报告
// 生成注册表数据（internal/registry/data_generated.go）。
//
// 生成内容：
//   - 每个同步注册表的全部条目名（发送 Registry Data 时省略 NBT，
//     由客户端 Known Packs 提供定义）；
//   - 需要服务器通过 Update Tags 发送的全部标签：同步注册表的标签以条目顺序
//     映射 ID，静态注册表（block、item、entity_type 等）的标签使用客户端内置的
//     全局注册表 ID（来自 reports/registries.json 的 protocol_id）。
//
// reports/registries.json 的生成方式（需与服务器协议版本一致的服务端 jar）：
//
//	java -DbundlerMainClass=net.minecraft.data.Main -jar server.jar --reports
//
// 用法：
//
//	go run ./tools/genregistries -jar <client.jar> -reports <registries.json> [-out internal/registry/data_generated.go]
package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"io"
	"os"
	"path"
	"sort"
	"strings"
)

// syncRegistryDirs 定义 1.21.11 的全部同步注册表及其在数据包中的目录。
// 该列表来自 Minecraft Wiki（Java Edition protocol/Registries#List of synchronized registries）
// 与客户端数据包实际内容核对。
var syncRegistryDirs = []struct {
	Name string
	Dir  string
}{
	{"minecraft:banner_pattern", "data/minecraft/banner_pattern"},
	{"minecraft:cat_variant", "data/minecraft/cat_variant"},
	{"minecraft:chat_type", "data/minecraft/chat_type"},
	{"minecraft:chicken_variant", "data/minecraft/chicken_variant"},
	{"minecraft:cow_variant", "data/minecraft/cow_variant"},
	{"minecraft:damage_type", "data/minecraft/damage_type"},
	{"minecraft:dialog", "data/minecraft/dialog"},
	{"minecraft:dimension_type", "data/minecraft/dimension_type"},
	{"minecraft:enchantment", "data/minecraft/enchantment"},
	{"minecraft:frog_variant", "data/minecraft/frog_variant"},
	{"minecraft:instrument", "data/minecraft/instrument"},
	{"minecraft:jukebox_song", "data/minecraft/jukebox_song"},
	{"minecraft:painting_variant", "data/minecraft/painting_variant"},
	{"minecraft:pig_variant", "data/minecraft/pig_variant"},
	{"minecraft:test_environment", "data/minecraft/test_environment"},
	{"minecraft:test_instance", "data/minecraft/test_instance"},
	{"minecraft:timeline", "data/minecraft/timeline"},
	{"minecraft:trim_material", "data/minecraft/trim_material"},
	{"minecraft:trim_pattern", "data/minecraft/trim_pattern"},
	{"minecraft:wolf_sound_variant", "data/minecraft/wolf_sound_variant"},
	{"minecraft:wolf_variant", "data/minecraft/wolf_variant"},
	{"minecraft:worldgen/biome", "data/minecraft/worldgen/biome"},
	{"minecraft:zombie_nautilus_variant", "data/minecraft/zombie_nautilus_variant"},
}

// staticTagRegistries 是 tags 存在于数据包、条目 ID 由客户端内置注册表决定的
// 静态注册表（tags 目录名 → 注册表名）。它们的条目 ID 取 reports/registries.json
// 的 protocol_id，不能使用数据包文件排序。
var staticTagRegistries = map[string]string{
	"block":                  "minecraft:block",
	"item":                   "minecraft:item",
	"entity_type":            "minecraft:entity_type",
	"fluid":                  "minecraft:fluid",
	"game_event":             "minecraft:game_event",
	"point_of_interest_type": "minecraft:point_of_interest_type",
}

// skippedTagDirs 是数据包中存在 tags、但服务器不通过 Update Tags 发送的
// 世界生成注册表：它们不是同步注册表，客户端从本地数据包加载其内容与标签。
var skippedTagDirs = []string{
	"worldgen/structure",
	"worldgen/world_preset",
	"worldgen/flat_level_generator_preset",
}

// rawTag 是数据包中一个 tag 文件的原始内容。
type rawTag struct {
	values []rawValue
}

// rawValue 是 tag 中的一个引用：普通条目或嵌套 tag（以 # 开头）。
type rawValue struct {
	id       string
	required bool
}

func main() {
	jarPath := flag.String("jar", "", "Minecraft 客户端 jar 路径")
	reportsPath := flag.String("reports", "", "服务端数据生成器输出的 reports/registries.json 路径")
	outPath := flag.String("out", "internal/registry/data_generated.go", "输出 Go 文件路径")
	version := flag.String("version", "1.21.11", "Minecraft 版本（写入文件注释）")
	flag.Parse()
	if *jarPath == "" {
		fmt.Fprintln(os.Stderr, "错误：必须提供 -jar 参数（客户端 jar 路径）")
		flag.Usage()
		os.Exit(2)
	}
	if *reportsPath == "" {
		fmt.Fprintln(os.Stderr, "错误：必须提供 -reports 参数（reports/registries.json 路径）")
		flag.Usage()
		os.Exit(2)
	}

	reports, err := loadReports(*reportsPath)
	if err != nil {
		fatal("读取 reports：%v", err)
	}

	reader, err := zip.OpenReader(*jarPath)
	if err != nil {
		fatal("打开 jar：%v", err)
	}
	defer reader.Close()

	generated, err := collect(&reader.Reader, reports)
	if err != nil {
		fatal("解析数据包：%v", err)
	}
	if err := writeOutput(*outPath, *version, generated); err != nil {
		fatal("写入 %s：%v", *outPath, err)
	}
	fmt.Printf("已生成 %s：%d 个同步注册表，%d 个注册表含 tags（其中 %d 个静态注册表使用内置 ID）\n",
		*outPath, len(generated.entries), len(generated.tags), len(generated.staticSizes))
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "错误："+format+"\n", args...)
	os.Exit(1)
}

type generated struct {
	// entries[注册表名] = 有序条目名
	entries map[string][]string
	// tags[注册表名] = 有序 tag（名 + 条目 ID）
	tags map[string][]resolvedTag
	// staticSizes[注册表名] = 静态注册表的条目数（用于校验标签 ID 范围）
	staticSizes map[string]int32
}

type resolvedTag struct {
	name    string
	entries []int32
}

// collect 读取 jar 并收集全部注册表条目与 tags。
func collect(reader *zip.Reader, reports *reportsData) (*generated, error) {
	dirToRegistry := make(map[string]string, len(syncRegistryDirs))
	registryPaths := make([]string, 0, len(syncRegistryDirs))
	for _, item := range syncRegistryDirs {
		dirToRegistry[item.Dir] = item.Name
		registryPaths = append(registryPaths, strings.TrimPrefix(item.Name, "minecraft:"))
	}
	// 按路径长度降序排列，便于 tags 的最长前缀匹配。
	sort.Slice(registryPaths, func(i, j int) bool { return len(registryPaths[i]) > len(registryPaths[j]) })

	entries := make(map[string][]string, len(syncRegistryDirs))
	tagFiles := make(map[string]map[string]rawTag) // 注册表 → tag 名 → 原始内容
	byDir := make(map[string][]string, len(syncRegistryDirs))

	for _, file := range reader.File {
		name := file.Name
		if file.FileInfo().IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		dir := path.Dir(name)
		if registryName, ok := dirToRegistry[dir]; ok {
			base := strings.TrimSuffix(path.Base(name), ".json")
			byDir[registryName] = append(byDir[registryName], "minecraft:"+base)
			continue
		}
		rest, ok := strings.CutPrefix(name, "data/minecraft/tags/")
		if !ok {
			continue
		}
		registryName, tagDir := matchTagRegistry(rest, registryPaths)
		if registryName == "" {
			if !isSkippedTagDir(rest) {
				fmt.Fprintf(os.Stderr, "警告：跳过未知的 tags 目录 %s\n", name)
			}
			continue
		}
		// rest 形如 "<tagDir>/<tagPath>.json"，tagPath 可能含子目录（如 pattern_item/xxx）。
		tagName := "minecraft:" + strings.TrimSuffix(rest[len(tagDir)+1:], ".json")

		content, err := readZipFile(file)
		if err != nil {
			return nil, fmt.Errorf("读取 %s：%w", name, err)
		}
		tag, err := parseTagFile(content)
		if err != nil {
			return nil, fmt.Errorf("解析 %s：%w", name, err)
		}
		if tagFiles[registryName] == nil {
			tagFiles[registryName] = make(map[string]rawTag)
		}
		tagFiles[registryName][tagName] = tag
	}

	// 汇总条目（排序保证确定性）。
	for _, item := range syncRegistryDirs {
		list := byDir[item.Name]
		if len(list) == 0 {
			return nil, fmt.Errorf("注册表 %s 在 jar 中没有数据（版本不匹配？预期目录 %s）", item.Name, item.Dir)
		}
		sort.Strings(list)
		entries[item.Name] = list
	}

	// 静态注册表的条目 ID 来自客户端内置注册表（reports/registries.json）。
	staticSizes := make(map[string]int32, len(staticTagRegistries))
	for _, registryName := range staticTagRegistries {
		ids, err := reports.entryIDs(registryName)
		if err != nil {
			return nil, err
		}
		staticSizes[registryName] = int32(len(ids))
	}

	// 展开 tags。
	tags := make(map[string][]resolvedTag, len(tagFiles))
	for registryName, raw := range tagFiles {
		index := make(map[string]int32, len(entries[registryName]))
		if size, ok := staticSizes[registryName]; ok {
			// 静态注册表：条目 ID 是客户端内置的 protocol_id。
			ids, err := reports.entryIDs(registryName)
			if err != nil {
				return nil, err
			}
			for entry, id := range ids {
				if id < 0 || id >= size {
					return nil, fmt.Errorf("注册表 %s 的条目 %s protocol_id %d 超出范围 [0,%d)", registryName, entry, id, size)
				}
				index[entry] = id
			}
		} else {
			for id, entry := range entries[registryName] {
				index[entry] = int32(id)
			}
		}
		resolver := &tagResolver{
			registryName: registryName,
			raw:          raw,
			index:        index,
			memo:         make(map[string][]int32, len(raw)),
			visiting:     make(map[string]bool),
		}
		names := make([]string, 0, len(raw))
		for tagName := range raw {
			names = append(names, tagName)
		}
		sort.Strings(names)
		for _, tagName := range names {
			values, err := resolver.resolve(tagName)
			if err != nil {
				return nil, err
			}
			tags[registryName] = append(tags[registryName], resolvedTag{name: tagName, entries: values})
		}
	}

	return &generated{entries: entries, tags: tags, staticSizes: staticSizes}, nil
}

// matchTagRegistry 判断 tags 路径属于哪个注册表，返回注册表名与匹配的目录前缀。
// 优先匹配同步注册表（含 worldgen/biome 等多级路径），再匹配静态注册表。
func matchTagRegistry(rest string, registryPaths []string) (registryName, tagDir string) {
	if matched := matchRegistryPrefix(rest, registryPaths); matched != "" {
		return "minecraft:" + matched, matched
	}
	for dir, registry := range staticTagRegistries {
		if strings.HasPrefix(rest, dir+"/") {
			return registry, dir
		}
	}
	return "", ""
}

// isSkippedTagDir 报告 rest 是否属于已知的“不发送”的 tags 目录。
func isSkippedTagDir(rest string) bool {
	for _, dir := range skippedTagDirs {
		if strings.HasPrefix(rest, dir+"/") {
			return true
		}
	}
	return false
}

// reportsData 保存服务端数据生成器 reports/registries.json 中的静态注册表条目 ID。
type reportsData struct {
	// registries[注册表名][条目名] = protocol_id
	registries map[string]map[string]int32
}

// loadReports 解析 reports/registries.json。
func loadReports(path string) (*reportsData, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file map[string]struct {
		Entries map[string]struct {
			ProtocolID int32 `json:"protocol_id"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(content, &file); err != nil {
		return nil, err
	}
	data := &reportsData{registries: make(map[string]map[string]int32, len(file))}
	for registryName, registry := range file {
		if len(registry.Entries) == 0 {
			continue
		}
		ids := make(map[string]int32, len(registry.Entries))
		for entryName, entry := range registry.Entries {
			ids[entryName] = entry.ProtocolID
		}
		data.registries[registryName] = ids
	}
	return data, nil
}

// entryIDs 返回一个静态注册表的条目 → protocol_id 映射。
func (r *reportsData) entryIDs(registryName string) (map[string]int32, error) {
	ids, ok := r.registries[registryName]
	if !ok {
		return nil, fmt.Errorf("reports 缺少注册表 %s（版本不匹配？）", registryName)
	}
	return ids, nil
}

// matchRegistryPrefix 返回 rest 前缀匹配的同步注册表路径（最长匹配），没有则返回空。
func matchRegistryPrefix(rest string, registryPaths []string) string {
	for _, regPath := range registryPaths {
		if strings.HasPrefix(rest, regPath+"/") {
			return regPath
		}
	}
	return ""
}

// tagResolver 展开一个注册表内的全部 tags（递归解析 # 嵌套引用）。
type tagResolver struct {
	registryName string
	raw          map[string]rawTag
	index        map[string]int32
	memo         map[string][]int32
	visiting     map[string]bool
}

func (r *tagResolver) resolve(name string) ([]int32, error) {
	if values, ok := r.memo[name]; ok {
		return values, nil
	}
	if r.visiting[name] {
		return nil, fmt.Errorf("注册表 %s 的 tag %s 存在循环引用", r.registryName, name)
	}
	raw, ok := r.raw[name]
	if !ok {
		return nil, fmt.Errorf("注册表 %s 缺少被引用的 tag %s", r.registryName, name)
	}
	r.visiting[name] = true
	defer delete(r.visiting, name)

	seen := make(map[int32]bool)
	var values []int32
	add := func(id int32) {
		if !seen[id] {
			seen[id] = true
			values = append(values, id)
		}
	}
	for _, value := range raw.values {
		if strings.HasPrefix(value.id, "#") {
			nested, err := r.resolve(normalizeTagName(value.id[1:]))
			if err != nil {
				return nil, err
			}
			for _, id := range nested {
				add(id)
			}
			continue
		}
		id, ok := r.index[normalizeEntryName(value.id)]
		if !ok {
			if value.required {
				return nil, fmt.Errorf("注册表 %s 的 tag %s 引用了不存在的条目 %s", r.registryName, name, value.id)
			}
			continue
		}
		add(id)
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	r.memo[name] = values
	return values, nil
}

func normalizeEntryName(name string) string {
	if strings.Contains(name, ":") {
		return name
	}
	return "minecraft:" + name
}

func normalizeTagName(name string) string {
	return normalizeEntryName(name)
}

// parseTagFile 解析 tag JSON 文件。values 中元素可以为字符串或 {"id":...,"required":...}。
func parseTagFile(content []byte) (rawTag, error) {
	var file struct {
		Values []json.RawMessage `json:"values"`
	}
	if err := json.Unmarshal(content, &file); err != nil {
		return rawTag{}, err
	}
	tag := rawTag{}
	for _, raw := range file.Values {
		var literal string
		if err := json.Unmarshal(raw, &literal); err == nil {
			tag.values = append(tag.values, rawValue{id: literal, required: true})
			continue
		}
		var object struct {
			ID       string `json:"id"`
			Required *bool  `json:"required"`
		}
		if err := json.Unmarshal(raw, &object); err != nil {
			return rawTag{}, fmt.Errorf("无法解析 tag 值 %s", raw)
		}
		required := true
		if object.Required != nil {
			required = *object.Required
		}
		tag.values = append(tag.values, rawValue{id: object.ID, required: required})
	}
	return tag, nil
}

func readZipFile(file *zip.File) ([]byte, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

// writeOutput 生成并格式化 Go 源文件。
func writeOutput(outPath, version string, data *generated) error {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "// Code generated by tools/genregistries from Minecraft %s client data; DO NOT EDIT.\n", version)
	buf.WriteString("//\n")
	buf.WriteString("// 数据来源：官方客户端 jar 中的 data/minecraft 内置数据包，以及官方服务端\n")
	buf.WriteString("// 数据生成器输出的 reports/registries.json（静态注册表 protocol_id）。\n")
	buf.WriteString("// 重新生成：go run ./tools/genregistries -jar <client.jar> -reports <registries.json>\n\n")
	buf.WriteString("package registry\n\n")

	registryNames := make([]string, 0, len(data.entries))
	for name := range data.entries {
		registryNames = append(registryNames, name)
	}
	sort.Strings(registryNames)

	buf.WriteString("// syncEntries 按注册表名保存全部条目；每个注册表内的顺序即客户端分配的数字 ID 顺序（从 0 起）。\n")
	buf.WriteString("var syncEntries = map[string][]string{\n")
	for _, name := range registryNames {
		fmt.Fprintf(&buf, "\t%q: {\n", name)
		for _, entry := range data.entries[name] {
			fmt.Fprintf(&buf, "\t\t%q,\n", entry)
		}
		buf.WriteString("\t},\n")
	}
	buf.WriteString("}\n\n")

	buf.WriteString("// tagData 是一个已展开（含嵌套引用）的注册表标签。\n")
	buf.WriteString("type tagData struct {\n\tname    string\n\tentries []int32\n}\n\n")
	buf.WriteString("// allTags 保存必须以 Update Tags 包完整发送的全部注册表标签：\n")
	buf.WriteString("// 同步注册表的 ID 为服务器发送顺序，静态注册表的 ID 为客户端内置的全局注册表 ID。\n")
	buf.WriteString("var allTags = map[string][]tagData{\n")
	tagRegistryNames := make([]string, 0, len(data.tags))
	for name := range data.tags {
		tagRegistryNames = append(tagRegistryNames, name)
	}
	sort.Strings(tagRegistryNames)
	for _, name := range tagRegistryNames {
		fmt.Fprintf(&buf, "\t%q: {\n", name)
		for _, tag := range data.tags[name] {
			fmt.Fprintf(&buf, "\t\t{%q, []int32{", tag.name)
			for i, id := range tag.entries {
				if i > 0 {
					buf.WriteString(", ")
				}
				fmt.Fprintf(&buf, "%d", id)
			}
			buf.WriteString("}},\n")
		}
		buf.WriteString("\t},\n")
	}
	buf.WriteString("}\n\n")

	overworldID, err := entryID(data.entries, "minecraft:dimension_type", "minecraft:overworld")
	if err != nil {
		return err
	}
	plainsID, err := entryID(data.entries, "minecraft:worldgen/biome", "minecraft:plains")
	if err != nil {
		return err
	}
	buf.WriteString("const (\n")
	fmt.Fprintf(&buf, "\t// DimensionTypeOverworldID 是 minecraft:overworld 维度类型的注册表 ID。\n\tDimensionTypeOverworldID = %d\n", overworldID)
	fmt.Fprintf(&buf, "\t// BiomePlainsID 是 minecraft:plains 生物群系的注册表 ID。\n\tBiomePlainsID = %d\n", plainsID)
	buf.WriteString(")\n\n")

	buf.WriteString("// staticRegistrySizes 是静态注册表（条目 ID 由客户端内置注册表决定）的条目数，\n")
	buf.WriteString("// 用于校验标签条目 ID 的范围。\n")
	buf.WriteString("var staticRegistrySizes = map[string]int32{\n")
	sizeNames := make([]string, 0, len(data.staticSizes))
	for name := range data.staticSizes {
		sizeNames = append(sizeNames, name)
	}
	sort.Strings(sizeNames)
	for _, name := range sizeNames {
		fmt.Fprintf(&buf, "\t%q: %d,\n", name, data.staticSizes[name])
	}
	buf.WriteString("}\n")

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return fmt.Errorf("格式化生成代码：%w", err)
	}
	return os.WriteFile(outPath, formatted, 0o644)
}

func entryID(entries map[string][]string, registryName, entryName string) (int32, error) {
	for id, name := range entries[registryName] {
		if name == entryName {
			return int32(id), nil
		}
	}
	return 0, fmt.Errorf("注册表 %s 缺少必需条目 %s", registryName, entryName)
}
