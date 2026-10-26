//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package registry

import (
	"sort"
	"strconv"
	"strings"
	"sync"
)

// PropertyState 是一个方块状态及其属性（由 tools/genregistries 生成）。
type PropertyState struct {
	ID    uint16
	Props map[string]string
}

// blockStateRanges 是属性方块的最小/最大状态 ID（原版方块的状态 ID 连续，
// 因此可用区间快速判断“某个状态是否属于该方块”）。
var blockStateRanges = sync.OnceValue(func() map[string][2]uint16 {
	ranges := make(map[string][2]uint16, len(BlockPropertyStates))
	for name, states := range BlockPropertyStates {
		minID, maxID := uint16(0xFFFF), uint16(0)
		for _, state := range states {
			if state.ID < minID {
				minID = state.ID
			}
			if state.ID > maxID {
				maxID = state.ID
			}
		}
		ranges[name] = [2]uint16{minID, maxID}
	}
	return ranges
})

// IsPropertyBlockState 报告状态 ID 是否属于指定的属性方块
// （如 "minecraft:lever"）。利用状态 ID 连续的性质做区间判断。
//
// 注意：若调用方处于每 tick 每方块高频路径（如实体环境扫描），
// 优先使用 StateKindOf 的 O(1) 位表查询，避免字符串 map 查找。
func IsPropertyBlockState(name string, id uint16) bool {
	ranges, ok := blockStateRanges()[name]
	if !ok {
		return false
	}
	return id >= ranges[0] && id <= ranges[1]
}

// StateKind 是方块状态类别位（可组合），用于水/岩浆/火的高频判定。
type StateKind uint8

// 状态类别位。
const (
	StateKindWater StateKind = 1 << iota
	StateKindLava
	// StateKindFire 表示火或灵魂火。
	StateKindFire
	// StateKindCactus 表示仙人掌（任意 age；原版 CactusBlock 的方块内效果
	// 对所有 age 生效）。
	StateKindCactus
)

// stateKindFlags 是按方块状态 ID 索引的类别位表。实体环境判定与流体逻辑
// 在 tick 热路径对每个方块状态查询多次，字符串 map 查找（IsPropertyBlockState）
// 会成为 profile 热点（tick 中 environmentInBox 每格最多 3 次查找）；
// 这里在包初始化时把水/岩浆/火/灵魂火方块的全部状态 ID 打进位表，
// 查询退化为一次数组访问（表长约 26 KiB，一次性分配）。
var stateKindFlags = buildStateKindFlags()

func buildStateKindFlags() []uint8 {
	maxID := 0
	for _, states := range BlockPropertyStates {
		for _, state := range states {
			if int(state.ID) > maxID {
				maxID = int(state.ID)
			}
		}
	}
	table := make([]uint8, maxID+1)
	mark := func(blockName string, flag StateKind) {
		for _, state := range BlockPropertyStates[blockName] {
			table[state.ID] |= uint8(flag)
		}
	}
	mark("minecraft:water", StateKindWater)
	mark("minecraft:lava", StateKindLava)
	mark("minecraft:fire", StateKindFire)
	mark("minecraft:soul_fire", StateKindFire)
	// 仙人掌不在属性方块表（BlockPropertyStates）中，按全量状态区间表标记。
	for _, r := range blockStateNameRanges {
		if r.Name != "minecraft:cactus" {
			continue
		}
		for id := int(r.Start); id <= int(r.End) && id < len(table); id++ {
			table[id] |= uint8(StateKindCactus)
		}
	}
	return table
}

// StateKindOf 返回方块状态的类别位；表范围外（不可能是合法状态）返回 0。
func StateKindOf(id uint16) StateKind {
	if int(id) < len(stateKindFlags) {
		return StateKind(stateKindFlags[id])
	}
	return 0
}

// IsCactusState 报告方块状态是否为仙人掌（任意 age）。
func IsCactusState(id uint16) bool { return StateKindOf(id)&StateKindCactus != 0 }

// IsWaterState 报告方块状态是否为任意等级的水（源与流动）。
func IsWaterState(id uint16) bool { return StateKindOf(id)&StateKindWater != 0 }

// IsLavaState 报告方块状态是否为任意等级的岩浆。
func IsLavaState(id uint16) bool { return StateKindOf(id)&StateKindLava != 0 }

// IsFireState 报告方块状态是否为火或灵魂火。
func IsFireState(id uint16) bool { return StateKindOf(id)&StateKindFire != 0 }

// statePropertyIndex 是状态 ID → 方块名与属性集合的反查表。
var statePropertyIndex = sync.OnceValue(func() map[uint16]PropertyStateRef {
	index := make(map[uint16]PropertyStateRef)
	for name, states := range BlockPropertyStates {
		for _, state := range states {
			index[state.ID] = PropertyStateRef{Name: name, Props: state.Props}
		}
	}
	return index
})

// PropertyStateRef 是一个属性状态的所属方块名与属性集合。
type PropertyStateRef struct {
	Name  string
	Props map[string]string
}

// StateProps 返回属性状态的方块名与属性（非属性方块状态返回 false）。
func StateProps(id uint16) (string, map[string]string, bool) {
	ref, ok := statePropertyIndex()[id]
	if !ok {
		return "", nil, false
	}
	return ref.Name, ref.Props, true
}

// BlockStateWithProps 在 BlockPropertyStates 中查找属性完全匹配的状态 ID。
// props 中的每一项都必须匹配；props 为空时匹配无属性的状态（只有一个时）。
// 查询结果带缓存（红石热路径会高频调用）。
func BlockStateWithProps(name string, props map[string]string) (uint16, bool) {
	key := stateCacheKey(name, props)
	if cached, ok := stateLookupCache.Load(key); ok {
		entry := cached.(stateCacheEntry)
		return entry.id, entry.ok
	}
	states, exists := BlockPropertyStates[name]
	if !exists {
		stateLookupCache.Store(key, stateCacheEntry{})
		return 0, false
	}
	var (
		found uint16
		ok    bool
	)
	for _, state := range states {
		if len(state.Props) != len(props) {
			continue
		}
		match := true
		for prop, value := range props {
			if state.Props[prop] != value {
				match = false
				break
			}
		}
		if match {
			found, ok = state.ID, true
			break
		}
	}
	stateLookupCache.Store(key, stateCacheEntry{id: found, ok: ok})
	return found, ok
}

type stateCacheEntry struct {
	id uint16
	ok bool
}

// cropAgeInfo 描述作物状态区间中 age 的排列：age 是主属性（从 0 升序），
// 每个年龄对应 stride 个状态（其余属性在其后排列）。
type cropAgeInfo struct {
	maxAge int
	stride int
}

// cropAges 是可由状态区间推导 age 的作物方块。
// stride=1：只有 age 属性；cocoa 为 age×facing（4 个朝向）；
// pitcher_crop 为 age×half（upper/lower）。顺序已按 blocks.json 核对。
var cropAges = map[string]cropAgeInfo{
	"minecraft:wheat":            {7, 1},
	"minecraft:carrots":          {7, 1},
	"minecraft:potatoes":         {7, 1},
	"minecraft:beetroots":        {3, 1},
	"minecraft:nether_wart":      {3, 1},
	"minecraft:sweet_berry_bush": {3, 1},
	"minecraft:torchflower_crop": {1, 1},
	"minecraft:melon_stem":       {7, 1},
	"minecraft:pumpkin_stem":     {7, 1},
	"minecraft:cocoa":            {2, 4},
	"minecraft:pitcher_crop":     {4, 2},
}

// BlockStateRangeOf 返回状态所属方块的连续状态 ID 区间。
// 注意 BlockStateID 返回的是方块的默认状态，不一定是区间起点
// （例如双格植物默认 half=lower）。
func BlockStateRangeOf(state uint16) (BlockStateRange, bool) {
	return blockStateRangeOf(state)
}

// blockStateRangeOf 返回状态所属方块的连续状态 ID 区间。
func blockStateRangeOf(state uint16) (BlockStateRange, bool) {
	lo, hi := 0, len(blockStateNameRanges)-1
	for lo <= hi {
		mid := (lo + hi) / 2
		r := blockStateNameRanges[mid]
		switch {
		case state < r.Start:
			hi = mid - 1
		case state > r.End:
			lo = mid + 1
		default:
			return r, true
		}
	}
	return BlockStateRange{}, false
}

// BlockStateAge 返回作物方块状态的成熟度（0..最大值）。
// 仅支持本文件 cropAges 中列出的作物；其他方块返回 false。
// 实现前提：状态区间按 age 主序排列，区间长度必须等于 (最大 age+1)×stride，
// 否则返回 false 而不猜测。
func BlockStateAge(state uint16) (int, bool) {
	r, ok := blockStateRangeOf(state)
	if !ok {
		return 0, false
	}
	info, ok := cropAges[r.Name]
	if !ok || int(r.End-r.Start) != (info.maxAge+1)*info.stride-1 {
		return 0, false
	}
	return int(state-r.Start) / info.stride, true
}

// BlockStateMaxAge 返回作物方块的最高年龄；非 cropAges 中的方块返回 false。
func BlockStateMaxAge(state uint16) (int, bool) {
	r, ok := blockStateRangeOf(state)
	if !ok {
		return 0, false
	}
	info, ok := cropAges[r.Name]
	if !ok || int(r.End-r.Start) != (info.maxAge+1)*info.stride-1 {
		return 0, false
	}
	return info.maxAge, true
}

// BlockStateWithAge 返回与 state 同一作物方块、指定年龄的状态 ID。
// 对于 stride > 1 的作物（如 pitcher_crop），返回该年龄组的第一个状态
// （双格植物为上半格），调用方需自行确认适用的子状态。
func BlockStateWithAge(state uint16, age int) (uint16, bool) {
	r, ok := blockStateRangeOf(state)
	if !ok {
		return 0, false
	}
	info, ok := cropAges[r.Name]
	if !ok || int(r.End-r.Start) != (info.maxAge+1)*info.stride-1 {
		return 0, false
	}
	if age < 0 || age > info.maxAge {
		return 0, false
	}
	return r.Start + uint16(age*info.stride), true
}

// FarmlandMoisture 返回耕地状态的含水量（0..7）；非耕地返回 false。
func FarmlandMoisture(state uint16) (int, bool) {
	name, props, ok := StateProps(state)
	if !ok || name != farmLandBlockName {
		return 0, false
	}
	moisture, err := strconv.Atoi(props["moisture"])
	if err != nil || moisture < 0 || moisture > 7 {
		return 0, false
	}
	return moisture, true
}

// FarmlandStateWithMoisture 返回指定含水量的耕地状态（0..7）。
func FarmlandStateWithMoisture(moisture int) (uint16, bool) {
	if moisture < 0 || moisture > 7 {
		return 0, false
	}
	return BlockStateWithProps(farmLandBlockName, map[string]string{
		"moisture": strconv.Itoa(moisture),
	})
}

// farmLandBlockName 是耕地方块名（1.20.5+ 原版类名为 FarmBlock）。
const farmLandBlockName = "minecraft:farmland"

// BlockStateHalfUpper 报告双格植物状态是否为上半格。
// 双格植物的状态区间只有两种状态，按 upper→lower 排列（blocks.json 已核对）。
// ok=false 表示该状态不属于“只有 half 属性且两状态”的方块。
func BlockStateHalfUpper(state uint16) (upper, ok bool) {
	r, ok := blockStateRangeOf(state)
	if !ok || r.End-r.Start != 1 {
		return false, false
	}
	return state == r.Start, true
}

// BlockStateCropUpperHalf 报告作物状态是否为上半格（仅 pitcher_crop：
// age 主序、half 次序，upper 在偶数偏移）。ok=false 表示该方块无 half 次序。
func BlockStateCropUpperHalf(state uint16) (upper, ok bool) {
	r, ok := blockStateRangeOf(state)
	if !ok {
		return false, false
	}
	info, ok := cropAges[r.Name]
	if !ok || info.stride != 2 {
		return false, false
	}
	return (state-r.Start)%2 == 0, true
}

// BlockStatePickleCount 返回海泡菜状态的 pickles 数量（1..4）。
// 状态顺序为 pickles 主序、waterlogged 次序（blocks.json 已核对）。
func BlockStatePickleCount(state uint16) (int, bool) {
	r, ok := blockStateRangeOf(state)
	if !ok || r.Name != "minecraft:sea_pickle" || int(r.End-r.Start) != 4*2-1 {
		return 0, false
	}
	return int(state-r.Start)/2 + 1, true
}

var stateLookupCache sync.Map

// stateCacheKey 构造缓存键："name|k1=v1,k2=v2"（属性按名称排序，保证确定性）。
func stateCacheKey(name string, props map[string]string) string {
	if len(props) == 0 {
		return name + "|"
	}
	keys := make([]string, 0, len(props))
	for key := range props {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	builder.WriteString(name)
	builder.WriteByte('|')
	for i, key := range keys {
		if i > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(key)
		builder.WriteByte('=')
		builder.WriteString(props[key])
	}
	return builder.String()
}

// AnvilDamageNext 返回铁砧受损后应替换的方块状态（原版 AnvilBlock.damage）：
// anvil → chipped_anvil → damaged_anvil（保留 facing）；已损坏返回 false。
func AnvilDamageNext(state uint16) (uint16, bool) {
	name, props, ok := StateProps(state)
	if !ok {
		return 0, false
	}
	var next string
	switch name {
	case "minecraft:anvil":
		next = "minecraft:chipped_anvil"
	case "minecraft:chipped_anvil":
		next = "minecraft:damaged_anvil"
	default:
		return 0, false
	}
	facing := props["facing"]
	for _, state := range BlockPropertyStates[next] {
		if state.Props["facing"] == facing {
			return state.ID, true
		}
	}
	return 0, false
}
