//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// 配方提取：解析 jar 内置数据包的 data/minecraft/recipe/*.json，生成
// recipes_generated.go（合成与烹饪配方表，原料标签在生成期展开为物品 ID）。
//
// 支持的配方类型：
//   - crafting_shaped（有序合成，含镜像匹配所需的原始图案）
//   - crafting_shapeless（无序合成）
//   - smelting / blasting / smoking / campfire_cooking（烹饪类）
//   - stonecutting（切石机）
//   - smithing_transform（锻造升级：模板/基底/添加物 → 产物）
//   - smithing_trim（锻造纹饰：模板/基底/添加物 + 纹饰名 → 基底副本 + trim 组件）
//
// 跳过的类型（结果带组件、特殊逻辑或需要额外注册表支持）：
//   - crafting_transmute / crafting_decorated_pot / crafting_special_*
//
// 原料格式支持字符串（物品或 #标签）、对象（{"item":...} / {"tag":...}）
// 与字符串数组（备选列表）三种形式。

// recipeIngredientSpec 是配方原料的原始表示（物品名或 #标签名）。
type recipeIngredientSpec struct {
	Items []string
	Tags  []string
}

// recipeKind 是烹饪配方的类型。
type recipeKind int

const (
	recipeSmelting recipeKind = iota
	recipeBlasting
	recipeSmoking
	recipeCampfire
)

// parsedRecipe 是解析后的配方（原料尚未展开为 ID 集合）。
type parsedRecipe struct {
	name        string
	group       string
	shaped      bool
	width       int
	height      int
	pattern     []string // shaped：每行的 key 字符
	key         map[string]recipeIngredientSpec
	ingredients []recipeIngredientSpec // shapeless / 烹饪 / 切石机原料
	result      string
	resultCount int32
	kind        recipeKind
	experience  float64
	cookTime    int32
	// stonecutting 标记切石机配方；smithing 标记锻造升级配方（模板/基底/添加物）。
	stonecutting bool
	smithing     bool
	// smithingTrim 标记锻造纹饰配方（无产物物品，结果为基底副本 + trim 组件）。
	smithingTrim bool
	// trimPattern 是纹饰配方的纹饰名（如 minecraft:bolt）。
	trimPattern string
	template    recipeIngredientSpec
	base        recipeIngredientSpec
	addition    recipeIngredientSpec
}

// collectRecipes 读取 jar 中 data/minecraft/recipe/*.json 并解析配方。
func collectRecipes(reader *zip.Reader) ([]parsedRecipe, error) {
	var recipes []parsedRecipe
	for _, file := range reader.File {
		name := file.Name
		if file.FileInfo().IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		dir := path.Dir(name)
		if dir != "data/minecraft/recipe" {
			continue
		}
		content, err := readZipFile(file)
		if err != nil {
			return nil, fmt.Errorf("读取 %s：%w", name, err)
		}
		recipe, ok, err := parseRecipeFile(content, strings.TrimSuffix(path.Base(name), ".json"))
		if err != nil {
			return nil, fmt.Errorf("解析 %s：%w", name, err)
		}
		if ok {
			recipes = append(recipes, recipe)
		}
	}
	sort.Slice(recipes, func(i, j int) bool { return recipes[i].name < recipes[j].name })
	return recipes, nil
}

// parseRecipeFile 解析单个配方 JSON。ok=false 表示该配方类型/形式被跳过。
func parseRecipeFile(content []byte, baseName string) (parsedRecipe, bool, error) {
	var raw struct {
		Type        string                     `json:"type"`
		Group       string                     `json:"group"`
		Key         map[string]json.RawMessage `json:"key"`
		Pattern     json.RawMessage            `json:"pattern"`
		Ingredients []json.RawMessage          `json:"ingredients"`
		Ingredient  json.RawMessage            `json:"ingredient"`
		Template    json.RawMessage            `json:"template"`
		Base        json.RawMessage            `json:"base"`
		Addition    json.RawMessage            `json:"addition"`
		Result      *struct {
			ID         string          `json:"id"`
			Count      int32           `json:"count"`
			Components json.RawMessage `json:"components"`
		} `json:"result"`
		Experience  float64 `json:"experience"`
		CookingTime int32   `json:"cookingtime"`
	}
	if err := json.Unmarshal(content, &raw); err != nil {
		return parsedRecipe{}, false, err
	}
	recipe := parsedRecipe{name: "minecraft:" + baseName, group: raw.Group}
	switch raw.Type {
	case "minecraft:crafting_shaped":
		recipe.shaped = true
	case "minecraft:crafting_shapeless":
	case "minecraft:smelting":
		recipe.kind = recipeSmelting
	case "minecraft:blasting":
		recipe.kind = recipeBlasting
	case "minecraft:smoking":
		recipe.kind = recipeSmoking
	case "minecraft:campfire_cooking":
		recipe.kind = recipeCampfire
	case "minecraft:stonecutting":
		recipe.stonecutting = true
	case "minecraft:smithing_transform":
		recipe.smithing = true
	case "minecraft:smithing_trim":
		recipe.smithingTrim = true
	default:
		// 其余类型（transmute/special 等）暂不支持。
		return parsedRecipe{}, false, nil
	}
	if recipe.smithingTrim {
		// 纹饰配方没有 result 字段：产物是基底副本 + trim 组件。
		if len(raw.Pattern) == 0 {
			return parsedRecipe{}, false, fmt.Errorf("纹饰配方缺少 pattern")
		}
		if err := json.Unmarshal(raw.Pattern, &recipe.trimPattern); err != nil {
			return parsedRecipe{}, false, fmt.Errorf("解析纹饰名：%w", err)
		}
		recipe.trimPattern = normalizeEntryName(recipe.trimPattern)
	} else {
		if raw.Result == nil || raw.Result.ID == "" {
			return parsedRecipe{}, false, nil
		}
		// 结果带组件（如可疑的炖菜）需要物品组件支持，跳过。
		if len(raw.Result.Components) > 0 && string(raw.Result.Components) != "{}" {
			return parsedRecipe{}, false, nil
		}
		recipe.result = normalizeEntryName(raw.Result.ID)
		recipe.resultCount = raw.Result.Count
		if recipe.resultCount <= 0 {
			recipe.resultCount = 1
		}
		recipe.experience = raw.Experience
		recipe.cookTime = raw.CookingTime
	}

	switch {
	case recipe.stonecutting:
		if len(raw.Ingredient) == 0 {
			return parsedRecipe{}, false, fmt.Errorf("切石机配方缺少 ingredient")
		}
		spec, err := parseIngredientSpec(raw.Ingredient)
		if err != nil {
			return parsedRecipe{}, false, err
		}
		recipe.ingredients = []recipeIngredientSpec{spec}
	case recipe.smithing, recipe.smithingTrim:
		if len(raw.Template) == 0 || len(raw.Base) == 0 || len(raw.Addition) == 0 {
			return parsedRecipe{}, false, fmt.Errorf("锻造配方缺少 template/base/addition")
		}
		var err error
		if recipe.template, err = parseIngredientSpec(raw.Template); err != nil {
			return parsedRecipe{}, false, fmt.Errorf("template：%w", err)
		}
		if recipe.base, err = parseIngredientSpec(raw.Base); err != nil {
			return parsedRecipe{}, false, fmt.Errorf("base：%w", err)
		}
		if recipe.addition, err = parseIngredientSpec(raw.Addition); err != nil {
			return parsedRecipe{}, false, fmt.Errorf("addition：%w", err)
		}
	case recipe.shaped:
		// 注意：smithing 等类型的 pattern 是字符串（纹饰名），只对 shaped 解析为行。
		var pattern []string
		if err := json.Unmarshal(raw.Pattern, &pattern); err != nil {
			return parsedRecipe{}, false, fmt.Errorf("解析图案：%w", err)
		}
		if len(pattern) == 0 || len(pattern) > 3 {
			return parsedRecipe{}, false, fmt.Errorf("图案行数 %d 越界", len(pattern))
		}
		recipe.pattern = pattern
		recipe.height = len(pattern)
		for _, row := range pattern {
			if len(row) > recipe.width {
				recipe.width = len(row)
			}
		}
		if recipe.width < 1 || recipe.width > 3 {
			return parsedRecipe{}, false, fmt.Errorf("图案宽度 %d 越界", recipe.width)
		}
		recipe.key = make(map[string]recipeIngredientSpec, len(raw.Key))
		for char, rawValue := range raw.Key {
			spec, err := parseIngredientSpec(rawValue)
			if err != nil {
				return parsedRecipe{}, false, fmt.Errorf("key %s：%w", char, err)
			}
			if len(char) != 1 {
				return parsedRecipe{}, false, fmt.Errorf("key 字符 %q 长度非法", char)
			}
			recipe.key[char] = spec
		}
	case len(raw.Ingredients) > 0:
		if len(raw.Ingredients) > 9 {
			return parsedRecipe{}, false, fmt.Errorf("原料数 %d 越界", len(raw.Ingredients))
		}
		for _, value := range raw.Ingredients {
			spec, err := parseIngredientSpec(value)
			if err != nil {
				return parsedRecipe{}, false, err
			}
			recipe.ingredients = append(recipe.ingredients, spec)
		}
	default:
		if len(raw.Ingredient) == 0 {
			return parsedRecipe{}, false, fmt.Errorf("缺少 ingredient")
		}
		spec, err := parseIngredientSpec(raw.Ingredient)
		if err != nil {
			return parsedRecipe{}, false, err
		}
		recipe.ingredients = []recipeIngredientSpec{spec}
		if recipe.cookTime <= 0 {
			return parsedRecipe{}, false, fmt.Errorf("烹饪时间 %d 非法", recipe.cookTime)
		}
	}
	return recipe, true, nil
}

// parseIngredientSpec 解析原料（字符串 / 对象 / 字符串数组）。
func parseIngredientSpec(raw json.RawMessage) (recipeIngredientSpec, error) {
	var spec recipeIngredientSpec
	add := func(value string) {
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, "#") {
			spec.Tags = append(spec.Tags, normalizeTagName(strings.TrimPrefix(value, "#")))
			return
		}
		spec.Items = append(spec.Items, normalizeEntryName(value))
	}
	var literal string
	if err := json.Unmarshal(raw, &literal); err == nil {
		add(literal)
		return spec, nil
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		for _, value := range list {
			add(value)
		}
		return spec, nil
	}
	var object struct {
		Item string `json:"item"`
		Tag  string `json:"tag"`
	}
	if err := json.Unmarshal(raw, &object); err == nil {
		switch {
		case object.Item != "":
			add(object.Item)
		case object.Tag != "":
			add("#" + object.Tag)
		default:
			return spec, fmt.Errorf("原料对象缺少 item/tag：%s", raw)
		}
		return spec, nil
	}
	return spec, fmt.Errorf("无法解析原料：%s", raw)
}

// recipeTables 是展开物品 ID 后的配方表（写入生成文件）。
type recipeTables struct {
	shaped       []resolvedShapedRecipe
	shapeless    []resolvedShapelessRecipe
	cooking      []resolvedCookingRecipe
	stonecutting []resolvedStonecuttingRecipe
	smithing     []resolvedSmithingRecipe
	smithingTrim []resolvedSmithingTrimRecipe
}

type resolvedIngredient []int32

type resolvedShapedRecipe struct {
	name        string
	group       string
	width       int32
	height      int32
	cells       []int32 // width×height，值为 ingredients 下标；-1 = 空格
	ingredients []resolvedIngredient
	result      int32
	resultCount int32
}

type resolvedShapelessRecipe struct {
	name        string
	group       string
	ingredients []resolvedIngredient
	result      int32
	resultCount int32
}

type resolvedCookingRecipe struct {
	name        string
	group       string
	kind        recipeKind
	ingredient  resolvedIngredient
	result      int32
	resultCount int32
	experience  float64
	cookTime    int32
}

// resolvedStonecuttingRecipe 是切石机配方（单个原料 → 产物）。
type resolvedStonecuttingRecipe struct {
	name        string
	ingredient  resolvedIngredient
	result      int32
	resultCount int32
}

// resolvedSmithingRecipe 是锻造升级配方（模板 + 基底 + 添加物 → 产物）。
type resolvedSmithingRecipe struct {
	name        string
	template    resolvedIngredient
	base        resolvedIngredient
	addition    resolvedIngredient
	result      int32
	resultCount int32
}

// resolvedSmithingTrimRecipe 是锻造纹饰配方（模板 + 基底 + 添加物 →
// 基底副本 + trim 组件）；产物由运行时根据添加物物品的
// minecraft:provides_trim_material 组件计算（见 registry.TrimMaterialForItem）。
type resolvedSmithingTrimRecipe struct {
	name     string
	pattern  string // 纹饰名（如 minecraft:bolt）
	template resolvedIngredient
	base     resolvedIngredient
	addition resolvedIngredient
}

// resolveRecipes 把配方原料展开为物品 ID 集合。
func resolveRecipes(recipes []parsedRecipe, itemIDs map[string]int32, itemTags map[string][]int32) (*recipeTables, error) {
	tables := &recipeTables{}
	resolve := func(spec recipeIngredientSpec) (resolvedIngredient, error) {
		seen := make(map[int32]bool)
		var ids resolvedIngredient
		add := func(id int32) {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		for _, itemName := range spec.Items {
			id, ok := itemIDs[itemName]
			if !ok {
				return nil, fmt.Errorf("未知物品 %s", itemName)
			}
			add(id)
		}
		for _, tagName := range spec.Tags {
			entries, ok := itemTags[tagName]
			if !ok {
				return nil, fmt.Errorf("未知物品标签 %s", tagName)
			}
			for _, id := range entries {
				add(id)
			}
		}
		if len(ids) == 0 {
			return nil, fmt.Errorf("原料为空")
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		return ids, nil
	}
	for _, recipe := range recipes {
		if recipe.smithingTrim {
			template, err := resolve(recipe.template)
			if err != nil {
				return nil, fmt.Errorf("配方 %s template：%w", recipe.name, err)
			}
			base, err := resolve(recipe.base)
			if err != nil {
				return nil, fmt.Errorf("配方 %s base：%w", recipe.name, err)
			}
			addition, err := resolve(recipe.addition)
			if err != nil {
				return nil, fmt.Errorf("配方 %s addition：%w", recipe.name, err)
			}
			tables.smithingTrim = append(tables.smithingTrim, resolvedSmithingTrimRecipe{
				name: recipe.name, pattern: recipe.trimPattern,
				template: template, base: base, addition: addition,
			})
			continue
		}
		result, ok := itemIDs[recipe.result]
		if !ok {
			// 结果物品不在注册表中（版本差异），跳过而非报错。
			continue
		}
		switch {
		case recipe.stonecutting:
			ids, err := resolve(recipe.ingredients[0])
			if err != nil {
				return nil, fmt.Errorf("配方 %s：%w", recipe.name, err)
			}
			tables.stonecutting = append(tables.stonecutting, resolvedStonecuttingRecipe{
				name: recipe.name, ingredient: ids, result: result, resultCount: recipe.resultCount,
			})
		case recipe.smithing:
			template, err := resolve(recipe.template)
			if err != nil {
				return nil, fmt.Errorf("配方 %s template：%w", recipe.name, err)
			}
			base, err := resolve(recipe.base)
			if err != nil {
				return nil, fmt.Errorf("配方 %s base：%w", recipe.name, err)
			}
			addition, err := resolve(recipe.addition)
			if err != nil {
				return nil, fmt.Errorf("配方 %s addition：%w", recipe.name, err)
			}
			tables.smithing = append(tables.smithing, resolvedSmithingRecipe{
				name: recipe.name, template: template, base: base, addition: addition,
				result: result, resultCount: recipe.resultCount,
			})
		case recipe.shaped:
			resolved := resolvedShapedRecipe{
				name: recipe.name, group: recipe.group,
				width: int32(recipe.width), height: int32(recipe.height),
				result: result, resultCount: recipe.resultCount,
			}
			keyIndex := make(map[string]int32)
			for y, row := range recipe.pattern {
				for x := 0; x < recipe.width; x++ {
					char := byte(' ')
					if x < len(row) {
						char = row[x]
					}
					if char == ' ' {
						resolved.cells = append(resolved.cells, -1)
						continue
					}
					keyName := string(char)
					index, ok := keyIndex[keyName]
					if !ok {
						spec, ok := recipe.key[keyName]
						if !ok {
							return nil, fmt.Errorf("配方 %s 图案引用未定义的 key %q", recipe.name, keyName)
						}
						ids, err := resolve(spec)
						if err != nil {
							return nil, fmt.Errorf("配方 %s key %q：%w", recipe.name, keyName, err)
						}
						index = int32(len(resolved.ingredients))
						keyIndex[keyName] = index
						resolved.ingredients = append(resolved.ingredients, ids)
					}
					_ = y
					resolved.cells = append(resolved.cells, index)
				}
			}
			tables.shaped = append(tables.shaped, resolved)
		case len(recipe.ingredients) > 1 || recipe.cookTime == 0:
			resolved := resolvedShapelessRecipe{
				name: recipe.name, group: recipe.group,
				result: result, resultCount: recipe.resultCount,
			}
			for _, spec := range recipe.ingredients {
				ids, err := resolve(spec)
				if err != nil {
					return nil, fmt.Errorf("配方 %s：%w", recipe.name, err)
				}
				resolved.ingredients = append(resolved.ingredients, ids)
			}
			tables.shapeless = append(tables.shapeless, resolved)
		default:
			ids, err := resolve(recipe.ingredients[0])
			if err != nil {
				return nil, fmt.Errorf("配方 %s：%w", recipe.name, err)
			}
			tables.cooking = append(tables.cooking, resolvedCookingRecipe{
				name: recipe.name, group: recipe.group, kind: recipe.kind,
				ingredient: ids, result: result, resultCount: recipe.resultCount,
				experience: recipe.experience, cookTime: recipe.cookTime,
			})
		}
	}
	return tables, nil
}

// writeRecipes 生成 recipes_generated.go。
func writeRecipes(outDir, version string, tables *recipeTables) error {
	var out bytes.Buffer
	out.WriteString(licenseHeader)
	fmt.Fprintf(&out, "// Code generated by tools/genregistries from Minecraft %s data/minecraft/recipe; DO NOT EDIT.\n", version)
	out.WriteString("//\n")
	out.WriteString("// 数据来源：官方客户端/服务端 jar 内置数据包的配方文件；原料标签在生成期展开为物品 ID。\n")
	out.WriteString("// 支持 crafting_shaped / crafting_shapeless / 四类烹饪 / stonecutting /\n")
	out.WriteString("// smithing_transform / smithing_trim；crafting_transmute / crafting_special_* 不生成\n")
	out.WriteString("// （它们需要额外的匹配逻辑或组件支持，见 docs/TODO.md）。\n\n")
	out.WriteString("package registry\n\n")

	out.WriteString("// Ingredient 是一个配方原料槽可接受的物品 ID 集合（生成期展开标签，升序）。\n")
	out.WriteString("type Ingredient []int32\n\n")
	out.WriteString("// CookingKind 是烹饪配方所属的容器类型。\n")
	out.WriteString("type CookingKind int32\n\n")
	out.WriteString("const (\n")
	out.WriteString("	// CookingSmelting 是熔炉配方（200 tick）。\n	CookingSmelting CookingKind = iota\n")
	out.WriteString("	// CookingBlasting 是高炉配方（100 tick，仅矿物与装备）。\n	CookingBlasting\n")
	out.WriteString("	// CookingSmoking 是烟熏炉配方（100 tick，仅食物）。\n	CookingSmoking\n")
	out.WriteString("	// CookingCampfire 是营火配方（600 tick）。\n	CookingCampfire\n)\n\n")

	out.WriteString("// ShapedRecipe 是有序合成配方。Cells 为 Width×Height 行优先网格，\n")
	out.WriteString("// -1 表示空格，其余值为 Ingredients 的下标（同一原料可复用）。\n")
	out.WriteString("type ShapedRecipe struct {\n")
	out.WriteString("	Name        string\n	Group       string\n	Width       int32\n	Height      int32\n")
	out.WriteString("	Cells       []int32\n	Ingredients []Ingredient\n	Result      int32\n	ResultCount int32\n}\n\n")

	out.WriteString("// ShapedRecipes 保存全部有序合成配方。\n")
	out.WriteString("var ShapedRecipes = []ShapedRecipe{\n")
	for _, recipe := range tables.shaped {
		fmt.Fprintf(&out, "	{Name: %q, Group: %q, Width: %d, Height: %d, Result: %d, ResultCount: %d,\n",
			recipe.name, recipe.group, recipe.width, recipe.height, recipe.result, recipe.resultCount)
		out.WriteString("		Cells: []int32{")
		for j, cell := range recipe.cells {
			if j > 0 {
				out.WriteString(", ")
			}
			fmt.Fprintf(&out, "%d", cell)
		}
		out.WriteString("}, Ingredients: []Ingredient{")
		for _, ingredient := range recipe.ingredients {
			out.WriteString("{")
			for _, id := range ingredient {
				fmt.Fprintf(&out, "%d, ", id)
			}
			out.WriteString("}, ")
		}
		out.WriteString("}},\n")
	}
	out.WriteString("}\n\n")

	out.WriteString("// ShapelessRecipe 是无序合成配方。\n")
	out.WriteString("type ShapelessRecipe struct {\n")
	out.WriteString("	Name        string\n	Group       string\n	Ingredients []Ingredient\n")
	out.WriteString("	Result      int32\n	ResultCount int32\n}\n\n")
	out.WriteString("// ShapelessRecipes 保存全部无序合成配方。\n")
	out.WriteString("var ShapelessRecipes = []ShapelessRecipe{\n")
	for _, recipe := range tables.shapeless {
		fmt.Fprintf(&out, "	{Name: %q, Group: %q, Result: %d, ResultCount: %d, Ingredients: []Ingredient{",
			recipe.name, recipe.group, recipe.result, recipe.resultCount)
		for _, ingredient := range recipe.ingredients {
			out.WriteString("{")
			for _, id := range ingredient {
				fmt.Fprintf(&out, "%d, ", id)
			}
			out.WriteString("}, ")
		}
		out.WriteString("}},\n")
	}
	out.WriteString("}\n\n")

	out.WriteString("// CookingRecipe 是烹饪类配方（熔炉/高炉/烟熏炉/营火）。\n")
	out.WriteString("type CookingRecipe struct {\n")
	out.WriteString("	Name        string\n	Group       string\n	Kind        CookingKind\n	Ingredient  Ingredient\n")
	out.WriteString("	Result      int32\n	ResultCount int32\n	Experience  float64\n	CookTime    int32\n}\n\n")
	out.WriteString("// CookingRecipes 保存全部烹饪配方。\n")
	out.WriteString("var CookingRecipes = []CookingRecipe{\n")
	for _, recipe := range tables.cooking {
		fmt.Fprintf(&out, "	{Name: %q, Group: %q, Kind: %d, Result: %d, ResultCount: %d, Experience: %v, CookTime: %d, Ingredient: Ingredient{",
			recipe.name, recipe.group, int32(recipe.kind), recipe.result, recipe.resultCount, recipe.experience, recipe.cookTime)
		for i, id := range recipe.ingredient {
			if i > 0 {
				out.WriteString(", ")
			}
			fmt.Fprintf(&out, "%d", id)
		}
		out.WriteString("}},\n")
	}
	out.WriteString("}\n\n")

	out.WriteString("// StonecuttingRecipe 是切石机配方：单个原料 → 产物（可选多个结果）。\n")
	out.WriteString("type StonecuttingRecipe struct {\n")
	out.WriteString("\tName        string\n\tIngredient  Ingredient\n\tResult      int32\n\tResultCount int32\n}\n\n")
	out.WriteString("// StonecuttingRecipes 保存全部切石机配方（按配方名排序）。\n")
	out.WriteString("var StonecuttingRecipes = []StonecuttingRecipe{\n")
	for _, recipe := range tables.stonecutting {
		fmt.Fprintf(&out, "\t{Name: %q, Result: %d, ResultCount: %d, Ingredient: Ingredient{", recipe.name, recipe.result, recipe.resultCount)
		for i, id := range recipe.ingredient {
			if i > 0 {
				out.WriteString(", ")
			}
			fmt.Fprintf(&out, "%d", id)
		}
		out.WriteString("}},\n")
	}
	out.WriteString("}\n\n")

	out.WriteString("// SmithingTransformRecipe 是锻造台升级配方（模板 + 基底 + 添加物 → 产物）。\n")
	out.WriteString("type SmithingTransformRecipe struct {\n")
	out.WriteString("\tName        string\n\tTemplate    Ingredient\n\tBase        Ingredient\n\tAddition    Ingredient\n")
	out.WriteString("\tResult      int32\n\tResultCount int32\n}\n\n")
	out.WriteString("// SmithingTransformRecipes 保存全部锻造升级配方（按配方名排序）。\n")
	out.WriteString("var SmithingTransformRecipes = []SmithingTransformRecipe{\n")
	for _, recipe := range tables.smithing {
		fmt.Fprintf(&out, "\t{Name: %q, Result: %d, ResultCount: %d, Template: Ingredient{", recipe.name, recipe.result, recipe.resultCount)
		for i, id := range recipe.template {
			if i > 0 {
				out.WriteString(", ")
			}
			fmt.Fprintf(&out, "%d", id)
		}
		out.WriteString("}, Base: Ingredient{")
		for i, id := range recipe.base {
			if i > 0 {
				out.WriteString(", ")
			}
			fmt.Fprintf(&out, "%d", id)
		}
		out.WriteString("}, Addition: Ingredient{")
		for i, id := range recipe.addition {
			if i > 0 {
				out.WriteString(", ")
			}
			fmt.Fprintf(&out, "%d", id)
		}
		out.WriteString("}},\n")
	}
	out.WriteString("}\n\n")

	out.WriteString("// SmithingTrimRecipe 是锻造纹饰配方（模板 + 基底 + 添加物 → 基底副本 + trim 组件）。\n")
	out.WriteString("// Pattern 是纹饰名（如 minecraft:bolt）；产物材质由添加物物品的\n")
	out.WriteString("// minecraft:provides_trim_material 组件决定（见 TrimMaterialItems 与\n")
	out.WriteString("// registry.TrimMaterialForItem）。\n")
	out.WriteString("type SmithingTrimRecipe struct {\n")
	out.WriteString("\tName     string\n\tPattern  string\n\tTemplate Ingredient\n\tBase     Ingredient\n\tAddition Ingredient\n}\n\n")
	out.WriteString("// SmithingTrimRecipes 保存全部锻造纹饰配方（按配方名排序）。\n")
	out.WriteString("var SmithingTrimRecipes = []SmithingTrimRecipe{\n")
	for _, recipe := range tables.smithingTrim {
		fmt.Fprintf(&out, "\t{Name: %q, Pattern: %q, Template: Ingredient{", recipe.name, recipe.pattern)
		for i, id := range recipe.template {
			if i > 0 {
				out.WriteString(", ")
			}
			fmt.Fprintf(&out, "%d", id)
		}
		out.WriteString("}, Base: Ingredient{")
		for i, id := range recipe.base {
			if i > 0 {
				out.WriteString(", ")
			}
			fmt.Fprintf(&out, "%d", id)
		}
		out.WriteString("}, Addition: Ingredient{")
		for i, id := range recipe.addition {
			if i > 0 {
				out.WriteString(", ")
			}
			fmt.Fprintf(&out, "%d", id)
		}
		out.WriteString("}},\n")
	}
	out.WriteString("}\n")

	formatted, err := format.Source(out.Bytes())
	if err != nil {
		return fmt.Errorf("格式化配方表：%w", err)
	}
	outPath := filepath.Join(outDir, "recipes_generated.go")
	return os.WriteFile(outPath, formatted, 0o644)
}
