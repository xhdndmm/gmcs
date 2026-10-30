//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

// vancmp 读取官方服务端生成的区域文件（Anvil），与 gmcs 的原版生成器
// 逐方块对比。配合"纯地形"数据包（清空群系地物与雕刻器、关闭矿脉）使用，
// 这样官方区块只包含 密度 + 含水层 + 表面规则 的结果。
//
// 用法：
//
//	go run ./tools/vancmp -mca /tmp/mcserver/Mojang-1.21.11/terrain/region/r.0.0.mca -seed 4321
package main

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gmcs/internal/world/vanilla"
)

func main() {
	mca := flag.String("mca", "", "区域文件路径")
	seed := flag.Int64("seed", 4321, "世界种子")
	limit := flag.Int("limit", 3, "最多对比多少个区块")
	names := flag.Bool("names", false, "只打印区块的方块名统计（不做对比）")
	nameLimit := flag.Int("top", 12, "-names 模式打印多少种方块")
	hist := flag.Bool("hist", false, "打印按 Y 的差异直方图")
	gridNode := flag.String("grid", "", "打印某个密度函数在网格上的取值")
	gridX := flag.Int("gx", 0, "网格起点 x")
	gridZ := flag.Int("gz", 0, "网格起点 z")
	gridY := flag.Int("gy", 64, "网格所在 y")
	dumpNode := flag.String("dump", "", "打印某个密度函数的 JSON 定义")
	listNodes := flag.Bool("list", false, "列出所有密度函数名")
	pairY := flag.Int("pair", -1000, "打印某个 y 的官方/gmcs 符号对照")
	shiftDX := flag.Int("dx", 0, "对比时 gmcs 的 x 偏移")
	shiftDY := flag.Int("dy", 0, "对比时 gmcs 的 y 偏移")
	shiftDZ := flag.Int("dz", 0, "对比时 gmcs 的 z 偏移")
	swapXZ := flag.Bool("swapxz", false, "对比时交换 gmcs 的 x/z")
	export := flag.String("export", "", "导出官方密度符号位图到该路径（配合 -density）")
	density := flag.Bool("density", false, "对比密度符号（配合密度探针世界）")
	flag.StringVar(&densityNode, "density-node", "", "把 final_density 替换成给定的 JSON 表达式")
	column := flag.String("col", "", "打印单个方块列（x,z）的官方与 gmcs 对比")
	aquifers := flag.Bool("aquifers", true, "对比时是否启用含水层（false 表示单独验证密度函数）")
	flag.Parse()
	if *mca == "" && *dumpNode == "" && !*listNodes && *gridNode == "" {
		fmt.Fprintln(os.Stderr, "缺少 -mca 参数")
		os.Exit(2)
	}

	if *dumpNode != "" || *listNodes {
		data, err := vanilla.Load()
		if err != nil {
			fmt.Fprintf(os.Stderr, "加载世界生成数据失败: %v\n", err)
			os.Exit(1)
		}
		if *listNodes {
			names := make([]string, 0, len(data.Functions))
			for name := range data.Functions {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				fmt.Println(name)
			}
			return
		}
		raw, ok := data.Functions[*dumpNode]
		if !ok {
			fmt.Fprintf(os.Stderr, "%s: 不存在\n", *dumpNode)
			os.Exit(1)
		}
		var pretty any
		if err := json.Unmarshal(raw, &pretty); err != nil {
			fmt.Fprintf(os.Stderr, "解析失败: %v\n", err)
			os.Exit(1)
		}
		out, err := json.MarshalIndent(pretty, "", " ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "格式化失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(out))
		return
	}

	if *gridNode != "" {
		data, err := vanilla.Load()
		if err != nil {
			fmt.Fprintf(os.Stderr, "加载世界生成数据失败: %v\n", err)
			os.Exit(1)
		}
		router, err := vanilla.NewRouter(data, "minecraft:overworld", *seed)
		if err != nil {
			fmt.Fprintf(os.Stderr, "构造路由表失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("节点 %s 在 y=%d 的取值网格：\n", *gridNode, *gridY)
		for z := *gridZ; z < *gridZ+16; z++ {
			line := ""
			for x := *gridX; x < *gridX+16; x++ {
				value, err := router.EvaluateNamed(*gridNode, vanilla.Context{X: x, Y: *gridY, Z: z})
				if err != nil {
					fmt.Fprintf(os.Stderr, "%v\n", err)
					os.Exit(1)
				}
				line += fmt.Sprintf("%9.4f", value)
			}
			fmt.Println(line)
		}
		return
	}

	region, err := readRegion(*mca)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取区域文件失败: %v\n", err)
		os.Exit(1)
	}
	if len(region) == 0 {
		fmt.Fprintln(os.Stderr, "区域文件里没有区块")
		os.Exit(1)
	}
	positions := make([]chunkPos, 0, len(region))
	for pos := range region {
		positions = append(positions, pos)
	}
	sort.Slice(positions, func(i, j int) bool {
		if positions[i].z != positions[j].z {
			return positions[i].z < positions[j].z
		}
		return positions[i].x < positions[j].x
	})

	if *names {
		for _, pos := range positions {
			counts := map[string]int{}
			for _, name := range region[pos] {
				counts[name]++
			}
			keys := make([]string, 0, len(counts))
			for name := range counts {
				keys = append(keys, name)
			}
			sort.Slice(keys, func(i, j int) bool { return counts[keys[i]] > counts[keys[j]] })
			fmt.Printf("chunk (%d,%d) blocks=%d\n", pos.x, pos.z, len(region[pos]))
			for i, name := range keys {
				if i >= *nameLimit {
					fmt.Printf("   ... 共 %d 种\n", len(keys))
					break
				}
				fmt.Printf("   %-40s %d\n", name, counts[name])
			}
		}
		return
	}

	data, err := vanilla.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载世界生成数据失败: %v\n", err)
		os.Exit(1)
	}
	if *density {
		if err := applyDensityProbe(data); err != nil {
			fmt.Fprintf(os.Stderr, "构造密度探针设置失败: %v\n", err)
			os.Exit(1)
		}
	}
	if !*aquifers {
		if err := overrideAquifers(data); err != nil {
			fmt.Fprintf(os.Stderr, "关闭含水层失败: %v\n", err)
			os.Exit(1)
		}
	}
	router, err := vanilla.NewRouter(data, "minecraft:overworld", *seed)
	if err != nil {
		fmt.Fprintf(os.Stderr, "构造路由表失败: %v\n", err)
		os.Exit(1)
	}
	setting := router.Setting()
	if setting.Height <= 0 || setting.Height > math.MaxInt/(16*16) {
		fmt.Fprintf(os.Stderr, "无效世界高度: %d\n", setting.Height)
		os.Exit(1)
	}
	blocksPerChunk := 16 * 16 * setting.Height

	if *density && *column == "" {
		total, bad := 0, 0
		perY := map[int]int{}
		for i, pos := range positions {
			if i >= *limit {
				break
			}
			official := region[pos]
			sampler := router.NewChunkSampler(pos.x, pos.z)
			badChunk := 0
			for y := setting.MinY; y < setting.MinY+setting.Height; y++ {
				for z := 0; z < 16; z++ {
					for x := 0; x < 16; x++ {
						index := ((y - setting.MinY) * 256) + (z * 16) + x
						want := official[index] == "minecraft:stone"
						probeX, probeY, probeZ := pos.x*16+x, y, pos.z*16+z
						if *swapXZ {
							probeX, probeZ = probeZ, probeX
						}
						probeX += *shiftDX
						probeY += *shiftDY
						probeZ += *shiftDZ
						got := sampler.Density(probeX, probeY, probeZ) > 0
						total++
						if want != got {
							bad++
							badChunk++
							perY[y]++
						}
					}
				}
			}
			fmt.Printf("chunk (%d,%d): 密度符号不一致 %d / %d\n", pos.x, pos.z, badChunk, blocksPerChunk)
			if *export != "" && i == 0 {
				bits := make([]byte, (blocksPerChunk+7)/8)
				for y := setting.MinY; y < setting.MinY+setting.Height; y++ {
					for z := 0; z < 16; z++ {
						for x := 0; x < 16; x++ {
							index := ((y - setting.MinY) * 256) + (z * 16) + x
							if official[index] == "minecraft:stone" {
								bits[index/8] |= 1 << (index % 8)
							}
						}
					}
				}
				if err := writeGzip(*export, bits); err != nil {
					fmt.Fprintf(os.Stderr, "写导出文件失败: %v\n", err)
					os.Exit(1)
				}
				fmt.Printf("已导出官方密度符号到 %s（%d 字节）\n", *export, len(bits))
			}
			if i == 0 && *pairY > -1000 {
				pos := positions[0]
				official := region[pos]
				sampler := router.NewChunkSampler(pos.x, pos.z)
				fmt.Printf("y=%d 官方(+ = 石头) / gmcs(+ = 密度>0)：\n", *pairY)
				for z := 0; z < 16; z++ {
					wantLine, gotLine := "", ""
					for x := 0; x < 16; x++ {
						index := ((*pairY - setting.MinY) * 256) + (z * 16) + x
						if official[index] == "minecraft:stone" {
							wantLine += "+"
						} else {
							wantLine += "."
						}
						if sampler.Density(pos.x*16+x, *pairY, pos.z*16+z) > 0 {
							gotLine += "+"
						} else {
							gotLine += "."
						}
					}
					fmt.Printf("  %s   %s\n", wantLine, gotLine)
				}
			}
			if i == 0 {
				fmt.Println("每列最高的石头高度差 (官方 - gmcs)：")
				for z := 0; z < 16; z++ {
					line := ""
					for x := 0; x < 16; x++ {
						wantTop, gotTop := -999, -999
						for y := setting.MinY + setting.Height - 1; y >= setting.MinY; y-- {
							index := ((y - setting.MinY) * 256) + (z * 16) + x
							if wantTop == -999 && official[index] == "minecraft:stone" {
								wantTop = y
							}
							if gotTop == -999 && sampler.Density(pos.x*16+x, y, pos.z*16+z) > 0 {
								gotTop = y
							}
							if wantTop != -999 && gotTop != -999 {
								break
							}
						}
						line += fmt.Sprintf("%4d", wantTop-gotTop)
					}
					fmt.Println(line)
				}
			}
		}
		fmt.Printf("合计：%d / %d 个方块的密度符号不一致\n", bad, total)
		if *hist {
			for y := setting.MinY; y < setting.MinY+setting.Height; y++ {
				if perY[y] == 0 {
					continue
				}
				fmt.Printf("   y=%4d %5d %s\n", y, perY[y], strings.Repeat("#", min(perY[y]/8, 60)))
			}
		}
		if bad != 0 {
			os.Exit(1)
		}
		return
	}

	if *column != "" {
		var cx, cz int
		if _, err := fmt.Sscanf(*column, "%d,%d", &cx, &cz); err != nil {
			fmt.Fprintf(os.Stderr, "-col 需要形如 15,32 的方块坐标: %v\n", err)
			os.Exit(2)
		}
		pos := chunkPos{x: floorDiv16(cx), z: floorDiv16(cz)}
		official, ok := region[pos]
		if !ok {
			fmt.Fprintf(os.Stderr, "区域文件里没有区块 (%d,%d)\n", pos.x, pos.z)
			os.Exit(1)
		}
		chunk, err := router.GenerateChunk(pos.x, pos.z)
		if err != nil {
			fmt.Fprintf(os.Stderr, "生成区块失败: %v\n", err)
			os.Exit(1)
		}
		sampler := router.NewChunkSampler(pos.x, pos.z)
		localX := ((cx % 16) + 16) % 16
		localZ := ((cz % 16) + 16) % 16
		for y := setting.MinY + setting.Height - 1; y >= setting.MinY; y-- {
			index := ((y - setting.MinY) * 256) + localZ*16 + localX
			want := official[index]
			got := chunk.BlockAt(cx, y, cz).Name
			mark := " "
			if want != got {
				mark = "*"
			}
			fmt.Printf("%s y=%4d 官方=%-30s gmcs=%-30s 密度=%9.5f\n",
				mark, y, want, got, sampler.Density(cx, y, cz))
		}
		return
	}

	totalBlocks, totalMismatch := 0, 0
	for i, pos := range positions {
		if i >= *limit {
			break
		}
		official := region[pos]
		chunk, err := router.GenerateChunk(pos.x, pos.z)
		if err != nil {
			fmt.Fprintf(os.Stderr, "生成区块 (%d,%d) 失败: %v\n", pos.x, pos.z, err)
			os.Exit(1)
		}
		mismatches := 0
		examples := 0
		perName := map[string]int{}
		perY := map[int]int{}
		for y := setting.MinY; y < setting.MinY+setting.Height; y++ {
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					index := ((y - setting.MinY) * 256) + (z * 16) + x
					if index < 0 || index >= len(official) {
						continue
					}
					want := official[index]
					got := chunk.BlockAt(pos.x*16+x, y, pos.z*16+z).Name
					totalBlocks++
					if want != got {
						mismatches++
						perY[y]++
						perName[want+" -> "+got]++
						if examples < 5 {
							fmt.Printf("   (%d,%d,%d) 官方=%s gmcs=%s\n",
								pos.x*16+x, y, pos.z*16+z, want, got)
							examples++
						}
					}
				}
			}
		}
		totalMismatch += mismatches
		fmt.Printf("chunk (%d,%d): 不一致 %d / %d\n", pos.x, pos.z, mismatches, 16*16*setting.Height)
		if *hist {
			for y := setting.MinY; y < setting.MinY+setting.Height; y++ {
				if perY[y] == 0 {
					continue
				}
				fmt.Printf("   y=%4d %5d %s\n", y, perY[y], strings.Repeat("#", min(perY[y]/8, 60)))
			}
		}
		keys := make([]string, 0, len(perName))
		for key := range perName {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool { return perName[keys[i]] > perName[keys[j]] })
		for i, key := range keys {
			if i >= 6 {
				fmt.Printf("   ... 共 %d 种差异\n", len(keys))
				break
			}
			fmt.Printf("   %-60s %d\n", key, perName[key])
		}
	}
	fmt.Printf("合计：%d / %d 个方块不一致\n", totalMismatch, totalBlocks)
	if totalMismatch != 0 {
		os.Exit(1)
	}
}

var debugChunk = os.Getenv("VANCMP_DEBUG") != ""

// densityNode 是 -density-node 的值：把 final_density 替换成这个 JSON 表达式，
// 用于逐个子节点与官方世界对拍。
var densityNode string

// applyDensityProbe 把内嵌数据改成"密度探针"设置：默认方块是石头、默认流体是空气、
// 海平面压到世界之下、关闭含水层与表面规则。这样官方世界的方块就直接等于
// final_density 的符号（stone = 密度为正），可与 gmcs 的密度逐方块对拍。
func applyDensityProbe(data *vanilla.Data) error {
	const name = "minecraft:overworld"
	raw, ok := data.NoiseSettings[name]
	if !ok {
		return fmt.Errorf("内嵌数据缺少 %s", name)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	decoded["aquifers_enabled"] = false
	decoded["ore_veins_enabled"] = false
	decoded["sea_level"] = -65
	decoded["default_block"] = map[string]any{"Name": "minecraft:stone"}
	decoded["default_fluid"] = map[string]any{"Name": "minecraft:air"}
	decoded["surface_rule"] = map[string]any{
		"type":         "minecraft:block",
		"result_state": map[string]any{"Name": "minecraft:stone"},
	}
	if densityNode != "" {
		router, ok := decoded["noise_router"].(map[string]any)
		if !ok {
			return fmt.Errorf("%s 缺少 noise_router", name)
		}
		var expr any
		if err := json.Unmarshal([]byte(densityNode), &expr); err != nil {
			return fmt.Errorf("-density-node 不是合法 JSON: %w", err)
		}
		router["final_density"] = expr
	}
	patched, err := json.Marshal(decoded)
	if err != nil {
		return err
	}
	data.NoiseSettings[name] = patched
	return nil
}

// floorDiv16 是把方块坐标对齐到区块坐标（Mth.sectionToChunk 的语义）。
func floorDiv16(v int) int {
	if v >= 0 {
		return v >> 4
	}
	return -((-v + 15) >> 4)
}

// writeGzip 把数据 gzip 压缩后写到 path（用于导出官方真值快照）。
func writeGzip(path string, data []byte) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	writer := gzip.NewWriter(file)
	if _, err := writer.Write(data); err != nil {
		return err
	}
	return writer.Close()
}

// overrideAquifers 把内嵌数据里的 overworld 噪声设置改成 aquifers_enabled=false，
// 与官方服务端在同样的数据包设置下生成的对照世界一致。
func overrideAquifers(data *vanilla.Data) error {
	const name = "minecraft:overworld"
	raw, ok := data.NoiseSettings[name]
	if !ok {
		return fmt.Errorf("内嵌数据缺少 %s", name)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	decoded["aquifers_enabled"] = false
	patched, err := json.Marshal(decoded)
	if err != nil {
		return err
	}
	data.NoiseSettings[name] = patched
	return nil
}

// chunkPos 是区块坐标。
type chunkPos struct {
	x int
	z int
}

// readRegion 读取区域文件并解码每个区块的方块名（按 y→z→x 顺序，共 384×16×16）。
func readRegion(path string) (map[chunkPos][]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) < 8192 {
		return nil, fmt.Errorf("文件太小，不是区域文件")
	}
	base := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(path), ".mca"), ".r")
	_ = base
	regionX, regionZ, err := regionCoords(filepath.Base(path))
	if err != nil {
		return nil, err
	}
	result := make(map[chunkPos][]string)
	for i := 0; i < 1024; i++ {
		location := binary.BigEndian.Uint32(raw[i*4 : i*4+4])
		sectorOffset := int(location >> 8)
		sectorCount := int(location & 0xFF)
		if sectorOffset == 0 || sectorCount == 0 {
			continue
		}
		start := sectorOffset * 4096
		if start+5 > len(raw) {
			continue
		}
		length := int(binary.BigEndian.Uint32(raw[start : start+4]))
		if length == 0 || start+4+length > len(raw) {
			continue
		}
		compression := raw[start+4]
		payload := raw[start+5 : start+4+length]
		decoded, err := decompress(compression, payload)
		if err != nil {
			continue
		}
		names, err := decodeChunk(decoded)
		if err != nil {
			continue
		}
		result[chunkPos{x: regionX*32 + i%32, z: regionZ*32 + i/32}] = names
	}
	return result, nil
}

// regionCoords 从 r.X.Z.mca 解析区域坐标。
func regionCoords(name string) (int, int, error) {
	var x, z int
	if _, err := fmt.Sscanf(name, "r.%d.%d.mca", &x, &z); err != nil {
		return 0, 0, err
	}
	return x, z, nil
}

// decompress 解压区块负载（1=gzip，2=zlib，3=未压缩）。
func decompress(kind byte, payload []byte) ([]byte, error) {
	switch kind {
	case 1:
		reader, err := gzip.NewReader(bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		return io.ReadAll(reader)
	case 2:
		reader, err := zlib.NewReader(bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		return io.ReadAll(reader)
	case 3:
		return payload, nil
	default:
		return nil, fmt.Errorf("未知压缩类型 %d", kind)
	}
}

// ---- NBT ----

// tag 是解析后的 NBT 标签。
type tag struct {
	kind byte
	str  string
	num  int64
	list []tag
	comp map[string]tag
	lngs []int64
}

const (
	tagEnd      = 0
	tagByte     = 1
	tagShort    = 2
	tagInt      = 3
	tagLong     = 4
	tagFloat    = 5
	tagDouble   = 6
	tagByteArr  = 7
	tagString   = 8
	tagList     = 9
	tagCompound = 10
	tagIntArr   = 11
	tagLongArr  = 12
)

// nbtReader 是顺序 NBT 读取器。
type nbtReader struct {
	data []byte
	pos  int
}

func (r *nbtReader) need(n int) error {
	if r.pos+n > len(r.data) {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func (r *nbtReader) u8() (byte, error) {
	if err := r.need(1); err != nil {
		return 0, err
	}
	value := r.data[r.pos]
	r.pos++
	return value, nil
}

func (r *nbtReader) i16() (int16, error) {
	if err := r.need(2); err != nil {
		return 0, err
	}
	value := int16(binary.BigEndian.Uint16(r.data[r.pos:]))
	r.pos += 2
	return value, nil
}

func (r *nbtReader) i32() (int32, error) {
	if err := r.need(4); err != nil {
		return 0, err
	}
	value := int32(binary.BigEndian.Uint32(r.data[r.pos:]))
	r.pos += 4
	return value, nil
}

func (r *nbtReader) i64() (int64, error) {
	if err := r.need(8); err != nil {
		return 0, err
	}
	value := int64(binary.BigEndian.Uint64(r.data[r.pos:]))
	r.pos += 8
	return value, nil
}

func (r *nbtReader) str() (string, error) {
	length, err := r.i16()
	if err != nil {
		return "", err
	}
	if err := r.need(int(length)); err != nil {
		return "", err
	}
	value := string(r.data[r.pos : r.pos+int(length)])
	r.pos += int(length)
	return value, nil
}

func (r *nbtReader) payload(kind byte) (tag, error) {
	switch kind {
	case tagByte:
		value, err := r.u8()
		return tag{kind: kind, num: int64(int8(value))}, err
	case tagShort:
		value, err := r.i16()
		return tag{kind: kind, num: int64(value)}, err
	case tagInt:
		value, err := r.i32()
		return tag{kind: kind, num: int64(value)}, err
	case tagLong:
		value, err := r.i64()
		return tag{kind: kind, num: value}, err
	case tagFloat:
		value, err := r.i32()
		return tag{kind: kind, num: int64(value)}, err
	case tagDouble:
		value, err := r.i64()
		return tag{kind: kind, num: value}, err
	case tagString:
		value, err := r.str()
		return tag{kind: kind, str: value}, err
	case tagByteArr:
		length, err := r.i32()
		if err != nil {
			return tag{}, err
		}
		if err := r.need(int(length)); err != nil {
			return tag{}, err
		}
		r.pos += int(length)
		return tag{kind: kind}, nil
	case tagIntArr:
		length, err := r.i32()
		if err != nil {
			return tag{}, err
		}
		if err := r.need(int(length) * 4); err != nil {
			return tag{}, err
		}
		values := make([]int64, length)
		for i := range values {
			values[i] = int64(int32(binary.BigEndian.Uint32(r.data[r.pos+i*4:])))
		}
		r.pos += int(length) * 4
		return tag{kind: kind, lngs: values}, nil
	case tagLongArr:
		length, err := r.i32()
		if err != nil {
			return tag{}, err
		}
		if err := r.need(int(length) * 8); err != nil {
			return tag{}, err
		}
		values := make([]int64, length)
		for i := range values {
			values[i] = int64(binary.BigEndian.Uint64(r.data[r.pos+i*8:]))
		}
		r.pos += int(length) * 8
		return tag{kind: kind, lngs: values}, nil
	case tagList:
		elementKind, err := r.u8()
		if err != nil {
			return tag{}, err
		}
		length, err := r.i32()
		if err != nil {
			return tag{}, err
		}
		items := make([]tag, 0, length)
		for i := int32(0); i < length; i++ {
			item, err := r.payload(elementKind)
			if err != nil {
				return tag{}, err
			}
			items = append(items, item)
		}
		return tag{kind: kind, list: items}, nil
	case tagCompound:
		compound := map[string]tag{}
		for {
			childKind, err := r.u8()
			if err != nil {
				return tag{}, err
			}
			if childKind == tagEnd {
				break
			}
			name, err := r.str()
			if err != nil {
				return tag{}, err
			}
			value, err := r.payload(childKind)
			if err != nil {
				return tag{}, err
			}
			compound[name] = value
		}
		return tag{kind: kind, comp: compound}, nil
	default:
		return tag{}, fmt.Errorf("未知 NBT 类型 %d", kind)
	}
}

// decodeChunk 从区块 NBT 解出全部方块名（y 从 minY 到 minY+height，z、x 依次）。
func decodeChunk(payload []byte) ([]string, error) {
	reader := &nbtReader{data: payload}
	kind, err := reader.u8()
	if err != nil {
		return nil, err
	}
	if kind != tagCompound {
		return nil, fmt.Errorf("根标签不是 compound")
	}
	if _, err := reader.str(); err != nil { // 根名
		return nil, err
	}
	root, err := reader.payload(tagCompound)
	if err != nil {
		return nil, err
	}
	sections := root.comp["sections"]
	if sections.kind != tagList {
		return nil, fmt.Errorf("缺少 sections")
	}
	if debugChunk {
		keys := make([]string, 0, len(root.comp))
		for key := range root.comp {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		fmt.Printf("[debug] root keys=%v sections=%d status=%q xPos=%d zPos=%d\n",
			keys, len(sections.list), root.comp["Status"].str, root.comp["xPos"].num, root.comp["zPos"].num)
		if hm, ok := root.comp["Heightmaps"]; ok {
			if mb, ok := hm.comp["MOTION_BLOCKING"]; ok {
				fmt.Printf("[debug] MOTION_BLOCKING longs=%d first=%d\n", len(mb.lngs), mb.lngs[0])
			}
		}
		if biomes, ok := sections.list[0].comp["biomes"]; ok {
			fmt.Printf("[debug] biomes kind=%d palette=%d\n", biomes.kind, len(biomes.comp["palette"].list))
		}
		if len(sections.list) > 0 {
			for _, section := range sections.list {
				y := int64(-999)
				if value, ok := section.comp["Y"]; ok {
					y = value.num
				}
				paletteSize := 0
				hasData := false
				if states, ok := section.comp["block_states"]; ok {
					paletteSize = len(states.comp["palette"].list)
					_, hasData = states.comp["data"]
				}
				fmt.Printf("[debug] section Y=%d palette=%d data=%v\n", y, paletteSize, hasData)
				if states, ok := section.comp["block_states"]; ok && hasData {
					for i, entry := range states.comp["palette"].list {
						if i >= 4 {
							break
						}
						fmt.Printf("[debug]    palette[%d] = %q kind=%d\n", i, entry.comp["Name"].str, entry.kind)
					}
				}
			}
		}
	}
	// 默认按 1.21 的 -64 起、384 高。
	const minY = -64
	const height = 384
	names := make([]string, height*256)
	for i := range names {
		names[i] = "minecraft:air"
	}
	for _, section := range sections.list {
		if section.kind != tagCompound {
			continue
		}
		yTag, ok := section.comp["Y"]
		if !ok {
			continue
		}
		sectionY := int(yTag.num)
		states, ok := section.comp["block_states"]
		if !ok || states.kind != tagCompound {
			continue
		}
		paletteTag, ok := states.comp["palette"]
		if !ok || paletteTag.kind != tagList {
			continue
		}
		palette := make([]string, 0, len(paletteTag.list))
		for _, entry := range paletteTag.list {
			name := "minecraft:air"
			if value, ok := entry.comp["Name"]; ok {
				name = value.str
			}
			palette = append(palette, name)
		}
		if len(palette) == 0 {
			continue
		}
		base := (sectionY*16 - minY) * 256
		if base < 0 || base+4096 > len(names) {
			continue
		}
		if len(palette) == 1 {
			for i := 0; i < 4096; i++ {
				names[base+i] = palette[0]
			}
			continue
		}
		bits := 4
		for (1 << bits) < len(palette) {
			bits++
		}
		dataTag, ok := states.comp["data"]
		if !ok || dataTag.kind != tagLongArr {
			continue
		}
		perLong := 64 / bits
		mask := uint64(1)<<bits - 1
		for i := 0; i < 4096; i++ {
			word := int(i / perLong)
			if word >= len(dataTag.lngs) {
				break
			}
			shift := uint(i%perLong) * uint(bits)
			value := (uint64(dataTag.lngs[word]) >> shift) & mask
			if int(value) < len(palette) {
				names[base+i] = palette[value]
			}
		}
	}
	return names, nil
}
