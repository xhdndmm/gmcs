//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package protocol

// Set Equipment（clientbound）：把实体的装备槽内容同步给观察者。
//
// 1.21.11 抓包实测（官方服务端配对数据，含两件装备的盔甲架）：
//
//	64 56 80 01 a8 07 00 00 05 01 ca 07 00 00
//	│  │  │  └── 主手槽负载：count=1、物品 936（钻石剑）、组件 0/0
//	│  │  └── 槽位字节 0x80：槽位 0（主手）且高位表示“后面还有条目”
//	│  └── 实体 ID（VarInt）
//	└── 包 ID 0x64
//
// 因此槽位字节 = 槽位编号 | 0x80（最后一条不加高位），槽位负载就是普通
// 槽位（Slot）编码（空槽为单个 0x00）。
const (
	// PlayPacketIDSetEquipment 是 Set Equipment（clientbound）。
	PlayPacketIDSetEquipment = 0x64

	// 装备槽编号（原版 EquipmentSlot 的协议顺序）。
	EquipmentMainHand = 0
	EquipmentOffHand  = 1
	EquipmentFeet     = 2
	EquipmentLegs     = 3
	EquipmentChest    = 4
	EquipmentHead     = 5

	// EquipmentSlotCount 是协议中使用的装备槽数量（不含原版 1.21.5 新增的 body）。
	EquipmentSlotCount = 6
)

// EquipmentSlotEntry 是一个装备槽的编码条目：Slot 是槽位编号，
// Data 是已编码的槽位负载（物品堆栈）。
type EquipmentSlotEntry struct {
	Slot byte
	Data []byte
}

// EncodeSetEquipment 编码 Set Equipment 包。entries 顺序即槽位顺序，
// 除最后一条外槽位字节会置高位 0x80。
func EncodeSetEquipment(entityID int32, entries []EquipmentSlotEntry) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDSetEquipment))
	packet = AppendVarInt(packet, entityID)
	for i, entry := range entries {
		slot := entry.Slot
		if i != len(entries)-1 {
			slot |= 0x80
		}
		packet = append(packet, slot)
		packet = append(packet, entry.Data...)
	}
	return packet
}
