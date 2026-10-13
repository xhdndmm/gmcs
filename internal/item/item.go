// Package item 提供物品堆栈模型、玩家物品栏与协议编解码辅助。
package item

import (
	"fmt"

	"gmcs/internal/protocol"
	"gmcs/internal/registry"
)

// Stack 是一个物品堆栈。
//
// 仅携带一种物品组件覆盖（药水内容 PotionID），其余组件（客户端使用物品
// 默认组件，来自 Known Packs 的内置数据）；堆叠上限按官方物品数据
// （registry.MaxStackSize）处理，其余组件派生属性（如工具耐久）暂不校验。
type Stack struct {
	// ItemID 是物品的注册表 ID（客户端内置全局 ID）。
	ItemID int32
	// Count 是物品数量；<= 0 表示空堆栈。
	Count int32
	// PotionID 是药水内容（minecraft:potion_contents）覆盖：
	// 0 表示未覆盖（药水物品等效于水瓶，其余物品无组件）；
	// N > 0 表示药水注册表 ID N-1（registry.PotionWater 等）。
	PotionID int32
}

// Empty 返回空堆栈。
func Empty() Stack {
	return Stack{}
}

// IsEmpty 报告堆栈是否为空。
func (s Stack) IsEmpty() bool {
	return s.Count <= 0
}

// MaxStack 返回该物品的堆叠上限（来自官方物品数据；未知物品按 64）。
func (s Stack) MaxStack() int32 {
	return registry.MaxStackSize(s.ItemID)
}

// SameKind 报告两个堆栈是否同类（同物品且同药水内容）。
// 物品合并、堆叠与拖拽收集必须用它而非仅比较 ItemID：
// 不同药水的 potion 物品共享同一 ItemID。
func (s Stack) SameKind(other Stack) bool {
	return s.ItemID == other.ItemID && s.PotionID == other.PotionID
}

// FromName 按命名空间 ID 与数量创建堆栈。
func FromName(name string, count int32) (Stack, error) {
	if count <= 0 {
		return Stack{}, fmt.Errorf("物品数量必须为正数（%d）", count)
	}
	id, err := registry.ItemID(name)
	if err != nil {
		return Stack{}, err
	}
	return Stack{ItemID: id, Count: count}, nil
}

// potionContentsComponentID 是 minecraft:potion_contents 数据组件类型 ID
// （reports/registries.json 的 minecraft:data_component_type，1.21.11）。
const potionContentsComponentID = 49

// AppendSlot 按 1.21.11 的 Slot 结构把堆栈追加到 dst。
//
// Slot 结构：count（VarInt）；count != 0 时依次为 itemId（VarInt）、
// 添加的组件数、移除的组件数、组件的补丁数据。PotionID > 0 时添加一个
// minecraft:potion_contents 组件（负载见 appendPotionContents），
// 其余情况不带组件。
func (s Stack) AppendSlot(dst []byte) []byte {
	if s.IsEmpty() {
		return protocol.AppendVarInt(dst, 0)
	}
	dst = protocol.AppendVarInt(dst, s.Count)
	dst = protocol.AppendVarInt(dst, s.ItemID)
	if s.PotionID > 0 {
		dst = protocol.AppendVarInt(dst, 1) // 添加的组件数
		dst = protocol.AppendVarInt(dst, 0) // 移除的组件数
		return appendPotionContents(dst, s.PotionID-1)
	}
	dst = protocol.AppendVarInt(dst, 0)  // 添加的组件数
	return protocol.AppendVarInt(dst, 0) // 移除的组件数
}

// appendPotionContents 追加一个 minecraft:potion_contents 组件：
// 组件 ID（VarInt）+ PotionContents.STREAM_CODEC 负载。
//
// 负载结构（对照 1.21.11 反编译的 PotionContents.STREAM_CODEC）：
// optional(potion)：bool + VarInt(注册表 ID + 1)；
// optional(customColor)：bool；
// customEffects：VarInt 列表长度；
// optional(customName)：bool。
// 这里只携带药水种类，其余字段均为空。
func appendPotionContents(dst []byte, potionID int32) []byte {
	dst = protocol.AppendVarInt(dst, potionContentsComponentID)
	dst = append(dst, 1)                         // optional(potion) 存在
	dst = protocol.AppendVarInt(dst, potionID+1) // Holder：注册表 ID + 1
	dst = append(dst, 0)                         // optional(customColor) 为空
	dst = protocol.AppendVarInt(dst, 0)          // customEffects 空列表
	return append(dst, 0)                        // optional(customName) 为空
}
