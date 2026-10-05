// Package item 提供物品堆栈模型、玩家物品栏与协议编解码辅助。
package item

import (
	"fmt"

	"gmcs/internal/protocol"
	"gmcs/internal/registry"
)

// Stack 是一个物品堆栈。
//
// 当前不携带物品组件覆盖（客户端使用物品默认组件，来自 Known Packs 的
// 内置数据）；堆叠上限等组件派生属性暂不校验，由上层决定。
type Stack struct {
	// ItemID 是物品的注册表 ID（客户端内置全局 ID）。
	ItemID int32
	// Count 是物品数量；<= 0 表示空堆栈。
	Count int32
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

// AppendSlot 按 1.21.11 的 Slot 结构把堆栈追加到 dst。
//
// Slot 结构：count（VarInt）；count != 0 时依次为 itemId（VarInt）、
// 添加的组件数、移除的组件数（当前均为 0）。
func (s Stack) AppendSlot(dst []byte) []byte {
	if s.IsEmpty() {
		return protocol.AppendVarInt(dst, 0)
	}
	dst = protocol.AppendVarInt(dst, s.Count)
	dst = protocol.AppendVarInt(dst, s.ItemID)
	dst = protocol.AppendVarInt(dst, 0)  // 添加的组件数
	return protocol.AppendVarInt(dst, 0) // 移除的组件数
}
