package registry

import (
	"sort"
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
func IsPropertyBlockState(name string, id uint16) bool {
	ranges, ok := blockStateRanges()[name]
	if !ok {
		return false
	}
	return id >= ranges[0] && id <= ranges[1]
}

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
