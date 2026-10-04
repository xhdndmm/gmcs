// Package registry 提供 1.21.11 注册表数据与标签。
//
// 数据由 tools/genregistries 从官方客户端的内置数据包与官方服务端
// 数据生成器报告（reports/registries.json）生成，见 data_generated.go。
// 发送 Registry Data 时省略条目 NBT，由 Known Packs 协商（minecraft:core）
// 让客户端从本地数据包读取定义；标签无法通过 Known Packs 获取，
// 必须由服务器通过 Update Tags 完整发送（见 AllTags）。
package registry

import (
	"sort"
	"sync"
)

// Registry 是一个同步注册表：条目顺序即客户端分配的数字 ID 顺序。
type Registry struct {
	Name    string
	Entries []string
}

// Tag 是一个注册表标签，Entries 为注册表条目 ID（已展开嵌套引用）。
type Tag struct {
	Name    string
	Entries []int32
}

// RegistryTags 是一个注册表的全部标签。
type RegistryTags struct {
	Name string
	Tags []Tag
}

var synchronized = sync.OnceValue(func() []Registry {
	names := make([]string, 0, len(syncEntries))
	for name := range syncEntries {
		names = append(names, name)
	}
	sort.Strings(names)
	registries := make([]Registry, 0, len(names))
	for _, name := range names {
		registries = append(registries, Registry{Name: name, Entries: syncEntries[name]})
	}
	return registries
})

// Synchronized 返回全部同步注册表（按注册表名排序）。
// 调用者不得修改返回的切片内容。
func Synchronized() []Registry {
	return synchronized()
}

var cachedTags = sync.OnceValue(func() []RegistryTags {
	names := make([]string, 0, len(allTags))
	for name := range allTags {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]RegistryTags, 0, len(names))
	for _, name := range names {
		items := make([]Tag, 0, len(allTags[name]))
		for _, tag := range allTags[name] {
			items = append(items, Tag{Name: tag.name, Entries: tag.entries})
		}
		result = append(result, RegistryTags{Name: name, Tags: items})
	}
	return result
})

// AllTags 返回需要由服务器在配置阶段通过 Update Tags 完整发送的全部注册表标签：
// 同步注册表（ID 为服务器发送顺序）与静态注册表（ID 为客户端内置的全局注册表 ID）。
// 标签无法通过 Known Packs 获取，缺失会导致客户端解析本地数据失败。
// 调用者不得修改返回的切片内容。
func AllTags() []RegistryTags {
	return cachedTags()
}
