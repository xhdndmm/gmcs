//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package protocol

import (
	"bytes"
	"testing"
)

// TestEncodeRecipeBookAddShapedGrid 验证 Recipe Book Add 中有序合成显示的编码：
// 原料数量必须等于 width×height（空格编码为 empty 显示），与客户端
// ShapedCraftingRecipeDisplay 构造函数 "ingredients.size() == width*height"
// 的强制校验一致；否则客户端解码整个数据包失败并断开连接。
func TestEncodeRecipeBookAddShapedGrid(t *testing.T) {
	entries := []RecipeBookAddItem{{
		DisplayID: 7,
		Category:  RecipeCategoryCraftingMisc,
		Display: RecipeDisplay{
			Type:        RecipeDisplayCraftingShaped,
			Width:       2,
			Height:      2,
			Ingredients: [][]int32{{10}, nil, nil, {10}},
			ResultItem:  20,
			ResultCount: 4,
		},
	}}
	packet := EncodeRecipeBookAdd(entries, true)

	offset := 0
	readVar := func(name string, want int32) {
		t.Helper()
		got, size, err := DecodeVarInt(packet[offset:])
		if err != nil {
			t.Fatalf("%s 解码失败：%v", name, err)
		}
		offset += size
		if got != want {
			t.Fatalf("%s = %d, want %d", name, got, want)
		}
	}

	readVar("包 ID", PlayPacketIDRecipeBookAdd)
	readVar("条目数", 1)
	readVar("显示编号", 7)
	readVar("显示类型", RecipeDisplayCraftingShaped)
	readVar("宽", 2)
	readVar("高", 2)
	readVar("原料数（=宽×高）", 4)
	readVar("原料 0 类型", SlotDisplayItem)
	readVar("原料 0 物品", 10)
	readVar("原料 1 类型（空格）", SlotDisplayEmpty)
	readVar("原料 2 类型（空格）", SlotDisplayEmpty)
	readVar("原料 3 类型", SlotDisplayItem)
	readVar("原料 3 物品", 10)
	readVar("产物类型", SlotDisplayItemStack)
	readVar("产物数量", 4)
	readVar("产物物品", 20)
	readVar("产物附加组件数", 0)
	readVar("产物移除组件数", 0)
	readVar("工作站类型（空）", SlotDisplayEmpty)
	readVar("分组", 0)
	readVar("分类", RecipeCategoryCraftingMisc)
	readVar("配方要求前缀（缺省）", 0)
	flags := packet[offset]
	offset++
	if flags != 0 {
		t.Fatalf("标志 = %d, want 0", flags)
	}
	if offset >= len(packet) || packet[offset] != 1 {
		t.Fatalf("replace 字段缺失或非 1")
	}
	offset++
	if offset != len(packet) {
		t.Fatalf("包尾有 %d 字节多余数据", len(packet)-offset)
	}
}

// TestEncodeUpdateRecipesIDSetPlainIDs 验证切石机配方的 ID Set（HolderSet）
// 显式列表编码：前缀为 VarInt(数量+1)（0 保留给标签形式），每个物品使用
// 不带偏移的注册表 ID。
func TestEncodeUpdateRecipesIDSetPlainIDs(t *testing.T) {
	packet := EncodeUpdateRecipes(nil, []StonecutterRecipeEntry{{
		Input:       []int32{5, 7},
		ResultItem:  42,
		ResultCount: 2,
	}})

	want := AppendVarInt(nil, PlayPacketIDUpdateRecipes)
	want = AppendVarInt(want, 0) // 配方声明（属性集）数量
	want = AppendVarInt(want, 1) // 切石机配方数量
	want = AppendVarInt(want, 3) // ID Set：数量+1
	want = AppendVarInt(want, 5) // 物品 ID（无偏移）
	want = AppendVarInt(want, 7)
	want = AppendVarInt(want, SlotDisplayItemStack)
	want = AppendVarInt(want, 2) // 产物数量
	want = AppendVarInt(want, 42)
	want = AppendVarInt(want, 0)
	want = AppendVarInt(want, 0)

	if !bytes.Equal(packet, want) {
		t.Fatalf("编码 = % x, want % x", packet, want)
	}
}
