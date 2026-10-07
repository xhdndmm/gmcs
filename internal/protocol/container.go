package protocol

import "fmt"

// 1.21.11（协议 774）容器相关的包 ID。
const (
	// PlayPacketIDCloseContainer 是 Close Container（clientbound，关闭客户端窗口）。
	PlayPacketIDCloseContainer = 0x11 // clientbound
	// PlayPacketIDSetContainerSlot 是 Set Container Slot（clientbound，单槽位更新）。
	PlayPacketIDSetContainerSlot = 0x14 // clientbound
	// PlayPacketIDSetContainerProperty 是 Set Container Property（clientbound，
	// 窗口属性更新：熔炉燃烧/烹饪进度条等）。
	PlayPacketIDSetContainerProperty = 0x13 // clientbound
	// PlayPacketIDContainerSetContent 是 Container Set Content（clientbound，全量内容）。
	PlayPacketIDContainerSetContent = 0x12 // clientbound
	// PlayPacketIDOpenScreen 是 Open Screen（clientbound，打开窗口）。
	PlayPacketIDOpenScreen = 0x39 // clientbound

	// PlayServerboundPacketIDContainerClick 是 Container Click（serverbound）。
	PlayServerboundPacketIDContainerClick = 0x11
	// PlayServerboundPacketIDContainerClose 是 Container Close（serverbound）。
	PlayServerboundPacketIDContainerClose = 0x12
)

// 原版菜单类型 ID（静态注册表 minecraft:menu 的 protocol_id；
// 客户端用它决定窗口布局）。
const (
	// MenuTypeGeneric9x3 是 27 槽通用容器（箱子、陷阱箱、木桶）。
	MenuTypeGeneric9x3 = 2
	// MenuTypeGeneric9x1 是 9 槽通用容器（发射器等）。
	MenuTypeGeneric9x1 = 0
)

// 玩家物品栏窗口 ID：客户端内置的常驻窗口，不通过 Open Screen 打开。
const PlayerInventoryWindowID = 0

// EncodeOpenScreen 编码 Open Screen 包：打开一个容器窗口。
// menuType 是原版菜单类型 ID（如 MenuTypeGeneric9x3）。
func EncodeOpenScreen(windowID, menuType int32, title string) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDOpenScreen))
	packet = AppendVarInt(packet, windowID)
	packet = AppendVarInt(packet, menuType)
	return AppendNBTString(packet, title)
}

// EncodeContainerSetContent 编码 Container Set Content 包：窗口内全部槽位的内容。
// slots 是已编码的 Slot 数据列表（顺序即槽位顺序）；carried 是鼠标持有的物品。
func EncodeContainerSetContent(windowID, stateID int32, slots [][]byte, carried []byte) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDContainerSetContent))
	packet = AppendVarInt(packet, windowID)
	packet = AppendVarInt(packet, stateID)
	packet = AppendVarInt(packet, int32(len(slots)))
	for _, slot := range slots {
		packet = append(packet, slot...)
	}
	return append(packet, carried...)
}

// EncodeSetContainerSlot 编码 Set Container Slot 包：更新窗口内的单个槽位。
func EncodeSetContainerSlot(windowID, stateID int32, slot int16, slotData []byte) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDSetContainerSlot))
	packet = AppendVarInt(packet, windowID)
	packet = AppendVarInt(packet, stateID)
	packet = AppendInt16(packet, slot)
	return append(packet, slotData...)
}

// EncodeSetContainerProperty 编码 Set Container Property 包：更新窗口属性
// （熔炉：0 燃烧剩余、1 燃料总时长、2 烹饪进度、3 烹饪总时长）。
func EncodeSetContainerProperty(windowID int32, property int16, value int16) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDSetContainerProperty))
	packet = AppendVarInt(packet, windowID)
	packet = AppendInt16(packet, property)
	packet = AppendInt16(packet, value)
	return packet
}

// EncodeContainerClose 编码 Close Container 包（clientbound）：要求客户端关闭窗口。
func EncodeContainerClose(windowID int32) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDCloseContainer))
	return AppendVarInt(packet, windowID)
}

// ContainerClick 是 Container Click（serverbound）包的解析结果。
//
// changedSlots 与 cursorItem 是客户端为反同步提供的提示，服务器以自身
// 权威状态为准重算结果，因此这里不再解析它们。
type ContainerClick struct {
	WindowID    int32
	StateID     int32
	Slot        int16
	MouseButton int8
	Mode        int32
}

// 容器点击模式（与原版 AbstractContainerMenu 的 ClickType 一致）。
const (
	ClickModePickup      = 0 // 左/右键取放（button 0 = 左键，1 = 右键）
	ClickModeQuickMove   = 1 // Shift 点击（快速移动）
	ClickModeSwapHotbar  = 2 // 数字键（与快捷栏交换）
	ClickModeClone       = 3 // 创造模式中键复制
	ClickModeThrow       = 4 // Q 丢弃（button 0 = 丢 1 个，1 = 丢整组）
	ClickModeQuickCraft  = 5 // 拖拽分发
	ClickModePickupAll   = 6 // 双击收集同类
	ClickButtonSecondary = 1
)

// ParseContainerClick 解析 Container Click 包。
func ParseContainerClick(packet []byte) (ContainerClick, error) {
	var click ContainerClick
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != PlayServerboundPacketIDContainerClick {
		return click, fmt.Errorf("invalid container click packet")
	}
	if click.WindowID, offset, err = decodeVarIntAt(packet, offset); err != nil {
		return click, err
	}
	if click.StateID, offset, err = decodeVarIntAt(packet, offset); err != nil {
		return click, err
	}
	rawSlot, offset, err := DecodeInt16(packet, offset)
	if err != nil {
		return click, err
	}
	click.Slot = rawSlot
	if len(packet) <= offset {
		return click, fmt.Errorf("container click packet missing mouse button")
	}
	click.MouseButton = int8(packet[offset])
	offset++
	if click.Mode, _, err = decodeVarIntAt(packet, offset); err != nil {
		return click, err
	}
	return click, nil
}

// ParseContainerClose 解析 Container Close（serverbound）包，返回窗口 ID。
func ParseContainerClose(packet []byte) (int32, error) {
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil || packetID != PlayServerboundPacketIDContainerClose {
		return 0, fmt.Errorf("invalid container close packet")
	}
	windowID, _, err := decodeVarIntAt(packet, offset)
	if err != nil {
		return 0, err
	}
	return windowID, nil
}

// ParseSlotItem 解析 Slot 结构的头部，返回物品 ID、数量与剩余的可选组件数据长度。
// 空槽位（count == 0）返回 (0, 0, 0, nil)；未实现的组件编码返回错误。
//
// 说明：本服务器只处理不带数据组件的堆栈（与原版默认物品行为一致），
// 带组件的槽位会被拒绝而不是静默丢弃，避免物品凭空消失。
func ParseSlotItem(data []byte) (itemID, count int32, err error) {
	count, offset, err := DecodeVarInt(data)
	if err != nil {
		return 0, 0, err
	}
	if count <= 0 {
		return 0, 0, nil
	}
	itemID, offset, err = decodeVarIntAt(data, offset)
	if err != nil {
		return 0, 0, err
	}
	added, offset, err := decodeVarIntAt(data, offset)
	if err != nil {
		return 0, 0, err
	}
	removed, _, err := decodeVarIntAt(data, offset)
	if err != nil {
		return 0, 0, err
	}
	if added != 0 || removed != 0 {
		return 0, 0, fmt.Errorf("slot carries data components, which are not supported")
	}
	return itemID, count, nil
}
