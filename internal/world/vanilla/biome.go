//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
)

// BiomeParameterFileName 是 overworld 多噪声群系参数表的数据文件。
//
// 原版这张表是**代码内建**的（data/minecraft/worldgen/multi_noise_biome_source_parameter_list/
// overworld.json 只有一个 preset 引用），所以由 tools/vgoracle/BiomeParameters.java
// 用反射从官方类导出，坐标是原版的量化 long（float * 10000 截断）。
const BiomeParameterFileName = "data/biome_parameters.json.gz"

// BiomeParameterNetherFileName 是下界五个群系的多噪声参数表。
const BiomeParameterNetherFileName = "data/biome_parameters_nether.json.gz"

func biomeParameterFileName(settingName string) string {
	if settingName == "minecraft:nether" {
		return BiomeParameterNetherFileName
	}
	return BiomeParameterFileName
}

// climateQuantizeScale 是原版 Climate.quantizeCoord 的量化尺度。
const climateQuantizeScale = 10000

// biomeParameterSpan 等价原版 Climate.ParameterSpan（闭区间，量化为 long）。
type biomeParameterSpan struct {
	min int64
	max int64
}

// distance 等价原版 ParameterSpan.distance(long)：
// 落在区间内为 0，否则为到最近端点的距离。
func (s biomeParameterSpan) distance(value int64) int64 {
	if value > s.max {
		return value - s.max
	}
	if gap := s.min - value; gap > 0 {
		return gap
	}
	return 0
}

// biomeParameterPoint 等价原版 Climate.ParameterPoint。
// 六个维度固定顺序：温度、湿度、大陆性、侵蚀、深度、怪异度。
type biomeParameterPoint struct {
	biome  string
	spans  [6]biomeParameterSpan
	offset int64
}

// distance 等价原版 ParameterPoint.fitness(TargetPoint)：各维距离之和再加 offset 绝对值。
func (p *biomeParameterPoint) distance(target *climateTarget) int64 {
	var sum int64
	for i := range p.spans {
		sum += absInt64(p.spans[i].distance(target.values[i]))
	}
	return sum + absInt64(p.offset)
}

// climateTarget 等价原版 Climate.TargetPoint（量化后的六维坐标）。
type climateTarget struct {
	values [6]int64
}

// biomeParameterFile 是导出文件的序列化结构。
type biomeParameterFile struct {
	Source        string                 `json:"source"`
	QuantizeScale int                    `json:"quantize_scale"`
	Points        []biomeParameterRecord `json:"points"`
}

// biomeParameterRecord 是导出文件的单条记录。
type biomeParameterRecord struct {
	Biome           string   `json:"biome"`
	Temperature     [2]int64 `json:"temperature"`
	Humidity        [2]int64 `json:"humidity"`
	Continentalness [2]int64 `json:"continentalness"`
	Erosion         [2]int64 `json:"erosion"`
	Depth           [2]int64 `json:"depth"`
	Weirdness       [2]int64 `json:"weirdness"`
	Offset          int64    `json:"offset"`
}

// biomeClimate 是群系气候（原版 worldgen/biome/*.json 中与地表规则相关的字段）。
type biomeClimate struct {
	temperature      float32
	downfall         float32
	hasPrecipitation bool
	// frozen 对应 temperature_modifier = "frozen"，
	// 温度 <= 0 时原版会再用 FROZEN_TEMPERATURE_NOISE 修正（见 coldEnoughToSnow）。
	frozen bool
}

// biomeKey 是群系采样缓存的键。
type biomeKey struct {
	x, y, z int
}

// BiomeSource 是 overworld 的多噪声群系源（原版 MultiNoiseBiomeSource）。
//
// 采样语义按 1.21.11 字节码：六个气候密度函数**直接在 quart 坐标上求值**，
// 量化后与参数表逐维比较距离，取距离最小的点（同距离时先出现者优先）。
// 这与原版 ParameterList.findValue 的 k-d 树等价（原版用暴力实现做同结果校验）。
type BiomeSource struct {
	router   *Router
	points   []biomeParameterPoint
	climates map[string]biomeClimate
	cache    map[biomeKey]string
	end      bool
	tree     *biomeTree
}

type biomeTreeNode struct {
	index        int
	left, right  int
	min, max     [6]int64
	minAbsOffset int64
}

type biomeTree struct {
	points []biomeParameterPoint
	nodes  []biomeTreeNode
	root   int
}

// NewBiomeSource 为给定路由表创建群系源，并加载内嵌的参数表与群系气候数据。
func NewBiomeSource(router *Router) (*BiomeSource, error) {
	data, err := Load()
	if err != nil {
		return nil, err
	}
	if router.setting.Name == "minecraft:end" {
		// 末地使用 TheEndBiomeSource（固定五群系规则），不需要多噪声参数表。
		return &BiomeSource{
			router:   router,
			climates: data.BiomeClimates(),
			cache:    make(map[biomeKey]string),
			end:      true,
		}, nil
	}
	fileName := biomeParameterFileName(router.setting.Name)
	points, err := loadBiomeParameters(fileName)
	if err != nil {
		return nil, err
	}
	source := &BiomeSource{
		router:   router,
		points:   points,
		climates: data.BiomeClimates(),
		cache:    make(map[biomeKey]string),
		end:      router.setting.Name == "minecraft:end",
	}
	source.tree = newBiomeTree(points)
	return source, nil
}

// loadBiomeParameters 读取内嵌的群系参数表。
func loadBiomeParameters(fileName string) ([]biomeParameterPoint, error) {
	compressed, err := dataFiles.ReadFile(fileName)
	if err != nil {
		return nil, fmt.Errorf("读取 %s 失败：%w", fileName, err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, fmt.Errorf("解压 %s 失败：%w", fileName, err)
	}
	defer reader.Close()
	payload, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("读取 %s 内容失败：%w", fileName, err)
	}
	var file biomeParameterFile
	if err := json.Unmarshal(payload, &file); err != nil {
		return nil, fmt.Errorf("解析 %s 失败：%w", fileName, err)
	}
	if len(file.Points) == 0 {
		return nil, fmt.Errorf("%s 没有群系参数点", fileName)
	}
	points := make([]biomeParameterPoint, len(file.Points))
	for i, record := range file.Points {
		points[i] = biomeParameterPoint{
			biome: qualify(record.Biome),
			spans: [6]biomeParameterSpan{
				{min: record.Temperature[0], max: record.Temperature[1]},
				{min: record.Humidity[0], max: record.Humidity[1]},
				{min: record.Continentalness[0], max: record.Continentalness[1]},
				{min: record.Erosion[0], max: record.Erosion[1]},
				{min: record.Depth[0], max: record.Depth[1]},
				{min: record.Weirdness[0], max: record.Weirdness[1]},
			},
			offset: record.Offset,
		}
	}
	return points, nil
}

// ResetCache 清空群系采样缓存。不同区块并行生成时为每个会话调用一次，
// 避免缓存无限增长；已缓存的值本身是确定性的，清空不影响结果。
func (b *BiomeSource) ResetCache() {
	b.cache = make(map[biomeKey]string)
}

// Climate 返回指定群系的气候数据。未登记的群系返回零值（温度 0、无降水）。
func (b *BiomeSource) Climate(name string) biomeClimate {
	return b.climates[qualify(name)]
}

// climateNodes 缓存六个气候密度函数节点，避免每次采样都查 map。
func (b *BiomeSource) climateNodes() [6]*Node {
	names := [6]string{"temperature", "vegetation", "continents", "erosion", "depth", "ridges"}
	var nodes [6]*Node
	for i, name := range names {
		node, ok := b.router.nodes[name]
		if !ok {
			panic("vanilla: 噪声路由表缺少气候项 " + name)
		}
		nodes[i] = node
	}
	return nodes
}

// sample 在 quart 坐标上求值六个气候函数并量化。
// 原版 Climate.Sampler.sample 先把 quart 坐标左移 2 变成方块坐标
// （QuartPos.fromQuart），再对这些方块坐标求值。
func (b *BiomeSource) sample(nodes *[6]*Node, quartX, quartY, quartZ int) climateTarget {
	ctx := Context{X: quartX * 4, Y: quartY * 4, Z: quartZ * 4}
	var target climateTarget
	for i := range nodes {
		target.values[i] = quantizeCoord(b.router.evaluator.Evaluate(nodes[i], ctx))
	}
	return target
}

// BiomeAt 返回给定 quart 坐标处的群系注册名（原版 MultiNoiseBiomeSource.getNoiseBiome）。
func (b *BiomeSource) BiomeAt(quartX, quartY, quartZ int) string {
	key := biomeKey{x: quartX, y: quartY, z: quartZ}
	if name, ok := b.cache[key]; ok {
		return name
	}
	if b.end {
		name := endBiomeAt(quartX, quartZ)
		b.cache[key] = name
		return name
	}
	nodes := b.climateNodes()
	target := b.sample(&nodes, quartX, quartY, quartZ)
	best := 0
	bestDistance := int64(math.MaxInt64)
	if b.tree != nil {
		b.tree.query(b.tree.root, &target, &best, &bestDistance)
	} else {
		for i := range b.points {
			if distance := b.points[i].distance(&target); distance < bestDistance {
				best = i
				bestDistance = distance
			}
		}
	}
	b.cache[key] = b.points[best].biome
	return b.points[best].biome
}

// BiomeAtBlock 返回该方块坐标所属的群系（按原版 4 格对齐规则取 quart 坐标）。
func (b *BiomeSource) BiomeAtBlock(x, y, z int) string {
	return b.BiomeAt(blockToQuart(x), blockToQuart(y), blockToQuart(z))
}

func newBiomeTree(points []biomeParameterPoint) *biomeTree {
	if len(points) == 0 {
		return nil
	}
	tree := &biomeTree{points: points, nodes: make([]biomeTreeNode, 0, len(points))}
	indices := make([]int, len(points))
	for i := range indices {
		indices[i] = i
	}
	tree.root = tree.build(indices)
	return tree
}

func (t *biomeTree) build(indices []int) int {
	if len(indices) == 0 {
		return -1
	}
	node := len(t.nodes)
	t.nodes = append(t.nodes, biomeTreeNode{index: -1, left: -1, right: -1})
	// 选择跨度最大的维度做中位数切分，降低查询剪枝成本。
	dim := 0
	var widest int64 = -1
	for d := 0; d < 6; d++ {
		minValue, maxValue := int64(math.MaxInt64), int64(math.MinInt64)
		for _, idx := range indices {
			span := t.points[idx].spans[d]
			if span.min < minValue {
				minValue = span.min
			}
			if span.max > maxValue {
				maxValue = span.max
			}
		}
		if width := maxValue - minValue; width > widest {
			widest, dim = width, d
		}
	}
	sort.Slice(indices, func(i, j int) bool {
		left, right := t.points[indices[i]].spans[dim], t.points[indices[j]].spans[dim]
		if left.min != right.min {
			return left.min < right.min
		}
		return left.max < right.max
	})
	mid := len(indices) / 2
	pointIndex := indices[mid]
	entry := biomeTreeNode{index: pointIndex, left: -1, right: -1, minAbsOffset: absInt64(t.points[pointIndex].offset)}
	for d := 0; d < 6; d++ {
		entry.min[d] = t.points[pointIndex].spans[d].min
		entry.max[d] = t.points[pointIndex].spans[d].max
	}
	t.nodes[node] = entry
	if mid > 0 {
		left := t.build(indices[:mid])
		t.nodes[node].left = left
		if left >= 0 {
			for d := 0; d < 6; d++ {
				t.nodes[node].min[d] = min(t.nodes[node].min[d], t.nodes[left].min[d])
				t.nodes[node].max[d] = max(t.nodes[node].max[d], t.nodes[left].max[d])
			}
			t.nodes[node].minAbsOffset = min(t.nodes[node].minAbsOffset, t.nodes[left].minAbsOffset)
		}
	}
	if mid+1 < len(indices) {
		right := t.build(indices[mid+1:])
		t.nodes[node].right = right
		if right >= 0 {
			for d := 0; d < 6; d++ {
				t.nodes[node].min[d] = min(t.nodes[node].min[d], t.nodes[right].min[d])
				t.nodes[node].max[d] = max(t.nodes[node].max[d], t.nodes[right].max[d])
			}
			t.nodes[node].minAbsOffset = min(t.nodes[node].minAbsOffset, t.nodes[right].minAbsOffset)
		}
	}
	return node
}

func (t *biomeTree) query(node int, target *climateTarget, best *int, bestDistance *int64) {
	if node < 0 {
		return
	}
	entry := &t.nodes[node]
	var lower int64
	for d := 0; d < 6; d++ {
		value := target.values[d]
		switch {
		case value < entry.min[d]:
			lower += entry.min[d] - value
		case value > entry.max[d]:
			lower += value - entry.max[d]
		}
	}
	lower += entry.minAbsOffset
	if lower > *bestDistance {
		return
	}
	if entry.index >= 0 {
		distance := t.points[entry.index].distance(target)
		if distance < *bestDistance || (distance == *bestDistance && entry.index < *best) {
			*best = entry.index
			*bestDistance = distance
		}
	}
	// 先访问包围盒下界较小的一侧。
	leftLower, rightLower := int64(math.MaxInt64), int64(math.MaxInt64)
	if entry.left >= 0 {
		leftLower = t.nodeLower(entry.left, target)
	}
	if entry.right >= 0 {
		rightLower = t.nodeLower(entry.right, target)
	}
	if leftLower < rightLower {
		t.query(entry.left, target, best, bestDistance)
		t.query(entry.right, target, best, bestDistance)
	} else {
		t.query(entry.right, target, best, bestDistance)
		t.query(entry.left, target, best, bestDistance)
	}
}

func (t *biomeTree) nodeLower(node int, target *climateTarget) int64 {
	entry := &t.nodes[node]
	var lower int64
	for d := 0; d < 6; d++ {
		value := target.values[d]
		switch {
		case value < entry.min[d]:
			lower += entry.min[d] - value
		case value > entry.max[d]:
			lower += value - entry.max[d]
		}
	}
	return lower + entry.minAbsOffset
}

// endBiomeAt 复刻 TheEndBiomeSource.getNoiseBiome 的群系选择。
// 输入是 quart 坐标；原版先转成方块坐标，再按 section 坐标判断主岛。
func endBiomeAt(quartX, quartZ int) string {
	blockX := quartX * 4
	blockZ := quartZ * 4
	sectionX := blockX >> 4
	sectionZ := blockZ >> 4
	if sectionX*sectionX+sectionZ*sectionZ <= 4096 {
		return "minecraft:the_end"
	}
	value := endIslandsValue(blockX, blockZ)
	switch {
	case value > 0.25:
		return "minecraft:end_highlands"
	case value >= -0.0625:
		return "minecraft:end_midlands"
	case value < -0.21875:
		return "minecraft:small_end_islands"
	default:
		return "minecraft:end_barrens"
	}
}

// blockToQuart 等价原版 QuartPos.fromBlock（算术右移 2，即向下取整）。
func blockToQuart(block int) int {
	return block >> 2
}

// quantizeCoord 等价原版 Climate.quantizeCoord(float) = (long)(f * 10000.0F)。
func quantizeCoord(value float64) int64 {
	scaled := float32(value) * float32(climateQuantizeScale)
	if math.IsNaN(float64(scaled)) || math.IsInf(float64(scaled), 0) {
		return 0
	}
	return int64(scaled)
}

// absInt64 是 Mth.abs(long)。
func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}
