//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"fmt"
	"testing"

	"gmcs/internal/protocol"
	"gmcs/internal/registry"
)

// recipeBookAddReader 按 1.21.11 客户端解码器逐字段解析 Recipe Book Add，
// 并施加客户端编解码链路中的强制校验（如有序显示原料数 = 宽×高）。
type recipeBookAddReader struct {
	t   *testing.T
	buf []byte
	pos int
}

func (r *recipeBookAddReader) failf(format string, args ...any) {
	r.t.Helper()
	r.t.Fatalf(format, args...)
}

func (r *recipeBookAddReader) varint(field string) int32 {
	r.t.Helper()
	value, size, err := protocol.DecodeVarInt(r.buf[r.pos:])
	if err != nil {
		r.failf("字段 %s 解码失败（偏移 %d）：%v", field, r.pos, err)
	}
	r.pos += size
	return value
}

func (r *recipeBookAddReader) byteField(field string) byte {
	r.t.Helper()
	if r.pos >= len(r.buf) {
		r.failf("字段 %s 越界（偏移 %d）", field, r.pos)
	}
	value := r.buf[r.pos]
	r.pos++
	return value
}

func (r *recipeBookAddReader) itemID(field string) {
	r.t.Helper()
	id := r.varint(field)
	if _, ok := registry.ItemName(id); !ok {
		r.failf("%s：物品 ID %d 不在 1.21.11 物品注册表中", field, id)
	}
}

func (r *recipeBookAddReader) slotDisplay(field string) {
	r.t.Helper()
	switch kind := r.varint(field + " 显示类型"); kind {
	case protocol.SlotDisplayEmpty, protocol.SlotDisplayAnyFuel:
	case protocol.SlotDisplayItem:
		r.itemID(field + " 物品")
	case protocol.SlotDisplayItemStack:
		if count := r.varint(field + " 数量"); count <= 0 {
			r.failf("%s：物品堆数量 = %d（客户端要求 > 0）", field, count)
		}
		r.itemID(field + " 物品")
		added := r.varint(field + " 附加组件数")
		removed := r.varint(field + " 移除组件数")
		if added != 0 || removed != 0 {
			r.failf("%s：组件数 = %d/%d, want 0/0", field, added, removed)
		}
	case protocol.SlotDisplayTag:
		length := r.varint(field + " 标签长度")
		r.pos += int(length)
		if r.pos > len(r.buf) {
			r.failf("%s：标签字符串越界（长度 %d）", field, length)
		}
	case protocol.SlotDisplaySmithingTrim:
		r.slotDisplay(field)
		r.slotDisplay(field)
		r.varint(field + " 锻造模板")
	case protocol.SlotDisplayWithRemainder:
		r.slotDisplay(field)
		r.slotDisplay(field)
	case protocol.SlotDisplayComposite:
		for n := r.varint(field + " 项数"); n > 0; n-- {
			r.slotDisplay(field)
		}
	default:
		r.failf("%s：未知 SlotDisplay 类型 %d", field, kind)
	}
}

func (r *recipeBookAddReader) recipeDisplay(index int, field string) {
	r.t.Helper()
	switch kind := r.varint(field + " 配方显示类型"); kind {
	case protocol.RecipeDisplayCraftingShapeless:
		for n := r.varint(field + " 原料数"); n > 0; n-- {
			r.slotDisplay(field + " 原料")
		}
		r.slotDisplay(field + " 产物")
		r.slotDisplay(field + " 工作站")
	case protocol.RecipeDisplayCraftingShaped:
		width := r.varint(field + " 宽")
		height := r.varint(field + " 高")
		count := r.varint(field + " 原料数")
		if count != width*height {
			r.failf("第 %d 条有序显示：%d×%d 但原料数为 %d（客户端要求 %d，否则数据包解码失败）",
				index, width, height, count, width*height)
		}
		for n := count; n > 0; n-- {
			r.slotDisplay(field + " 原料")
		}
		r.slotDisplay(field + " 产物")
		r.slotDisplay(field + " 工作站")
	case protocol.RecipeDisplayFurnace:
		r.slotDisplay(field + " 原料")
		r.slotDisplay(field + " 燃料")
		r.slotDisplay(field + " 产物")
		r.slotDisplay(field + " 工作站")
		r.varint(field + " 时长")
		if r.pos+4 > len(r.buf) {
			r.failf("%s：经验值越界", field)
		}
		r.pos += 4
	case protocol.RecipeDisplayStonecutter:
		r.slotDisplay(field + " 原料")
		r.slotDisplay(field + " 产物")
		r.slotDisplay(field + " 工作站")
	case protocol.RecipeDisplaySmithing:
		for i := 0; i < 5; i++ {
			r.slotDisplay(field + " 槽位")
		}
	default:
		r.failf("%s：未知 RecipeDisplay 类型 %d", field, kind)
	}
}

// TestRecipeBookAddPacketClientDecodable 模拟 1.21.11 客户端逐字段解码预编码的
// Recipe Book Add 数据包。客户端的 ShapedCraftingRecipeDisplay 构造函数强制
// 要求有序显示的原料数量等于 width×height（空格以 empty 显示占位），不满足时
// 解码抛出 "Invalid shaped recipe display contents" 并以
// "Failed to decode packet 'clientbound/minecraft:recipe_book_add'" 断开连接。
func TestRecipeBookAddPacketClientDecodable(t *testing.T) {
	instance := &Server{}
	instance.buildRecipeBook()
	book := instance.recipeBook
	if book == nil {
		t.Fatal("配方书未构建")
	}

	r := &recipeBookAddReader{t: t, buf: book.addPacket}
	if id := r.varint("包 ID"); id != protocol.PlayPacketIDRecipeBookAdd {
		r.failf("包 ID = %#x, want %#x", id, protocol.PlayPacketIDRecipeBookAdd)
	}
	count := r.varint("条目数")
	if int(count) != len(book.entries) {
		r.failf("条目数 = %d, want %d", count, len(book.entries))
	}
	for i := int32(0); i < count; i++ {
		field := fmt.Sprintf("条目 %d", i)
		if displayID := r.varint(field + " 显示编号"); displayID != i {
			r.failf("%s 显示编号 = %d, want %d", field, displayID, i)
		}
		r.recipeDisplay(int(i), field)
		r.varint(field + " 分组")
		if category := r.varint(field + " 分类"); category < 0 || category > 12 {
			r.failf("%s 分类 = %d 不在 1.21.11 配方书分类注册表内", field, category)
		}
		if required := r.byteField(field + " 配方要求前缀"); required != 0 {
			r.failf("%s 配方要求前缀 = %d, want 0（缺省）", field, required)
		}
		if flags := r.byteField(field + " 标志"); flags > 3 {
			r.failf("%s 标志 = %d 非法", field, flags)
		}
	}
	if replace := r.byteField("replace"); replace != 1 {
		r.failf("replace = %d, want 1", replace)
	}
	if r.pos != len(r.buf) {
		r.failf("包尾有 %d 字节多余数据", len(r.buf)-r.pos)
	}
}
