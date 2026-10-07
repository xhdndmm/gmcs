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
//
// 跳过的类型（结果带组件、特殊逻辑或需要额外注册表支持）：
//   - crafting_transmute / crafting_decorated_pot / crafting_special_*
//   - smithing_transform / smithing_trim / stonecutting
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
	ingredients []recipeIngredientSpec // shapeless 或烹饪原料
	result      string
	resultCount int32
	kind        recipeKind
	experience  float64
	cookTime    int32
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
	default:
		// 其余类型（transmute/smithing/stonecutting/special 等）暂不支持。
		return parsedRecipe{}, false, nil
	}
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

	switch {
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
	shaped    []resolvedShapedRecipe
	shapeless []resolvedShapelessRecipe
	cooking   []resolvedCookingRecipe
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
		result, ok := itemIDs[recipe.result]
		if !ok {
			// 结果物品不在注册表中（版本差异），跳过而非报错。
			continue
		}
		switch {
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
	fmt.Fprintf(&out, "// Code generated by tools/genregistries from Minecraft %s data/minecraft/recipe; DO NOT EDIT.\n", version)
	out.WriteString("//\n")
	out.WriteString("// 数据来源：官方客户端 jar 内置数据包的配方文件；原料标签在生成期展开为物品 ID。\n")
	out.WriteString("// 支持 crafting_shaped / crafting_shapeless 与四类烹饪配方；\n")
	out.WriteString("// crafting_transmute / crafting_special_* / smithing / stonecutting 等类型不生成\n")
	out.WriteString("// （它们需要额外的匹配逻辑或注册表支持，见 docs/TODO.md）。\n\n")
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
	out.WriteString("}\n")

	formatted, err := format.Source(out.Bytes())
	if err != nil {
		return fmt.Errorf("格式化配方表：%w", err)
	}
	outPath := filepath.Join(outDir, "recipes_generated.go")
	return os.WriteFile(outPath, formatted, 0o644)
}
