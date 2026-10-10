package world

import (
	"fmt"

	"gmcs/internal/registry"
)

// Dimension 是 gmcs 支持的维度类型。
//
// 三个维度共用同一套内存区块模型（全局 Y = -64 起、384 格高），
// 但网络编码按各维度的实际高度范围裁剪：
//   - 主世界：min_y = -64、height = 384（24 个 section）；
//   - 下界/末地：min_y = 0、height = 256（16 个 section，对应全局
//     section 索引 [4, 20)）。
//
// 客户端根据同步注册表 dimension_type 的 min_y/height 解析区块数据，
// 因此发送越界的 section 会导致解析错位；编码路径必须使用 Dimension
// 提供的范围，不得直接使用全局 SectionCount。
type Dimension uint8

// 维度常量。
const (
	DimensionOverworld Dimension = iota
	DimensionNether
	DimensionEnd
)

// AllDimensions 返回全部维度（固定顺序，便于确定性遍历）。
func AllDimensions() []Dimension {
	return []Dimension{DimensionOverworld, DimensionNether, DimensionEnd}
}

// MinY 返回该维度最低方块的 Y 坐标（dimension_type 的 min_y）。
func (d Dimension) MinY() int {
	switch d {
	case DimensionNether, DimensionEnd:
		return 0
	default:
		return WorldMinY
	}
}

// Height 返回该维度的总高度（dimension_type 的 height）。
func (d Dimension) Height() int {
	switch d {
	case DimensionNether, DimensionEnd:
		return 256
	default:
		return WorldHeight
	}
}

// SectionCount 返回该维度需要发送/持久化的 section 数量。
func (d Dimension) SectionCount() int {
	return d.Height() / SectionSize
}

// SectionRange 返回该维度对应的全局 section 索引区间 [low, high)。
// 主世界为 [0, 24)；下界/末地为 [4, 20)（y = 0 对应全局 section 4）。
func (d Dimension) SectionRange() (int, int) {
	low := (d.MinY() - WorldMinY) / SectionSize
	return low, low + d.SectionCount()
}

// Name 返回维度的命名空间 ID（Login/Respawn 包的 dimension name）。
func (d Dimension) Name() string {
	switch d {
	case DimensionNether:
		return "minecraft:the_nether"
	case DimensionEnd:
		return "minecraft:the_end"
	default:
		return "minecraft:overworld"
	}
}

// TypeID 返回同步注册表 minecraft:dimension_type 中该维度的条目 ID
// （Login/Respawn 包的 dimension type）。缺失属于部署错误。
func (d Dimension) TypeID() int32 {
	id, ok := registry.SyncEntryID("minecraft:dimension_type", d.Name())
	if !ok {
		panic("world: 同步注册表缺少维度类型 " + d.Name())
	}
	return id
}

// ParseDimension 解析维度名（接受 "overworld"、"minecraft:overworld" 等写法，
// 大小写不敏感）；无法识别时返回 false。
func ParseDimension(name string) (Dimension, bool) {
	normalized := name
	if len(normalized) >= len("minecraft:") && normalized[:len("minecraft:")] == "minecraft:" {
		normalized = normalized[len("minecraft:"):]
	}
	switch lower(normalized) {
	case "overworld":
		return DimensionOverworld, true
	case "the_nether", "nether":
		return DimensionNether, true
	case "the_end", "end":
		return DimensionEnd, true
	}
	return 0, false
}

// String 返回维度名，用于日志。
func (d Dimension) String() string {
	return d.Name()
}

// lower 是 ASCII 小写化（避免为解析维度引入 strings 依赖的额外分配）。
func lower(s string) string {
	buffer := []byte(s)
	for i, c := range buffer {
		if c >= 'A' && c <= 'Z' {
			buffer[i] = c + ('a' - 'A')
		}
	}
	return string(buffer)
}

// NewGenerator 按维度创建地形生成器。
func NewGenerator(dimension Dimension, seed int64) Generator {
	switch dimension {
	case DimensionNether:
		return NetherGenerator{Seed: seed}
	case DimensionEnd:
		return EndGenerator{Seed: seed}
	default:
		return SeededGenerator{Seed: seed}
	}
}

// DimensionScale 返回该维度与世界坐标的换算比例（下界 1:8）。
// factor 表示“主世界坐标 = 本维度坐标 × factor”。
func (d Dimension) DimensionScale() float64 {
	if d == DimensionNether {
		return 8
	}
	return 1
}

// dimensionDir 返回维度在世界目录下的子目录名（主世界使用世界目录本身）。
func dimensionDir(d Dimension) string {
	switch d {
	case DimensionNether:
		return "the_nether"
	case DimensionEnd:
		return "the_end"
	default:
		return ""
	}
}

// DimensionDir 返回维度在世界目录下的子目录名（主世界为空字符串，
// 表示直接使用世界目录）。
func DimensionDir(d Dimension) string {
	return dimensionDir(d)
}

// MustDimension 在解析失败时返回错误（供配置/命令使用）。
func ParseDimensionOrError(name string) (Dimension, error) {
	dimension, ok := ParseDimension(name)
	if !ok {
		return 0, fmt.Errorf("未知维度 %q（可用：overworld、the_nether、the_end）", name)
	}
	return dimension, nil
}
