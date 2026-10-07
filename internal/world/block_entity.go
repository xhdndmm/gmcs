package world

import "gmcs/internal/protocol"

// 方块实体（block entity）：目前仅支持容器类方块（箱子/陷阱箱/木桶等）的
// 槽位内容。数据随区块负载持久化，并在 Chunk Data 包中发送给客户端。
//
// 说明：原版方块实体是带类型的 NBT 结构，且带箱子 GUI 动画（Block Event
// 「open」）。本实现只保存槽位内容（类型由方块本身决定），不保存自定义名称、
// 锁定标记等原版字段。

// ContainerItem 是容器槽位中的物品（物品注册表 ID + 数量）。
type ContainerItem struct {
	ItemID int32
	Count  int32
}

// IsEmpty 报告槽位是否为空。
func (c ContainerItem) IsEmpty() bool {
	return c.Count <= 0 || c.ItemID <= 0
}

// BlockEntity 是一个方块实体的数据。
type BlockEntity struct {
	// TypeID 是方块实体类型 ID（静态注册表 minecraft:block_entity_type）。
	TypeID int32
	// Items 是容器槽位（顺序即槽位顺序）；nil 表示空容器。
	Items []ContainerItem
	// BurnRemaining / BurnTotal 是熔炉类方块的燃料剩余/总时长（tick）。
	BurnRemaining int32
	BurnTotal     int32
	// CookProgress / CookTotal 是熔炉类方块的烹饪进度/总时长（tick）。
	CookProgress int32
	CookTotal    int32
}

// Empty 报告方块实体是否没有任何内容（槽位全空且无熔炉进度）。
func (b BlockEntity) Empty() bool {
	if b.BurnRemaining > 0 || b.CookProgress > 0 {
		return false
	}
	for _, slot := range b.Items {
		if !slot.IsEmpty() {
			return false
		}
	}
	return true
}

// blockEntityIndex 把区块内局部坐标（x, z 为 0–15）映射为方块实体表键。
func blockEntityIndex(x, y, z int) int {
	return ((y - WorldMinY) << 8) | (z << 4) | x
}

// BlockEntityAt 返回区块内坐标 (x, y, z) 处的方块实体数据。
func (c *Chunk) BlockEntityAt(x, y, z int) (BlockEntity, bool) {
	if x < 0 || x >= SectionSize || z < 0 || z >= SectionSize {
		return BlockEntity{}, false
	}
	if _, ok := SectionIndex(y); !ok {
		return BlockEntity{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	entity, ok := c.blockEntities[blockEntityIndex(x, y, z)]
	return entity, ok
}

// SetBlockEntity 设置区块内坐标 (x, y, z) 处的方块实体数据。
func (c *Chunk) SetBlockEntity(x, y, z int, entity BlockEntity) {
	if x < 0 || x >= SectionSize || z < 0 || z >= SectionSize {
		return
	}
	if _, ok := SectionIndex(y); !ok {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	index := blockEntityIndex(x, y, z)
	if entity.Empty() {
		delete(c.blockEntities, index)
		if len(c.blockEntities) == 0 {
			c.blockEntities = nil
		}
		return
	}
	if c.blockEntities == nil {
		c.blockEntities = make(map[int]BlockEntity)
	}
	c.blockEntities[index] = entity
}

// RemoveBlockEntity 删除区块内坐标处的方块实体数据。
func (c *Chunk) RemoveBlockEntity(x, y, z int) {
	if x < 0 || x >= SectionSize || z < 0 || z >= SectionSize {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.blockEntities, blockEntityIndex(x, y, z))
	if len(c.blockEntities) == 0 {
		c.blockEntities = nil
	}
}

// BlockEntityCount 返回区块中的方块实体数量。
func (c *Chunk) BlockEntityCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.blockEntities)
}

// appendBlockEntityPacketData 把区块中的全部方块实体按 Chunk Data 包格式
// 追加到 dst。调用方必须持有 c.mu（读或写）。
//
// 每个条目为：packed XZ（u8：x<<4|z）、y（i16）、type（VarInt 方块实体类型 ID）、
// NBT（匿名 NBT，此处为含 Items 的空 compound）。
func (c *Chunk) appendBlockEntityPacketData(dst []byte) []byte {
	dst = protocol.AppendVarInt(dst, int32(len(c.blockEntities)))
	if len(c.blockEntities) == 0 {
		return dst
	}
	// 按索引排序输出，保证结果确定（map 遍历顺序随机）。
	indices := make([]int, 0, len(c.blockEntities))
	for index := range c.blockEntities {
		indices = append(indices, index)
	}
	sortInts(indices)
	for _, index := range indices {
		entity := c.blockEntities[index]
		x := index & 0xF
		z := (index >> 4) & 0xF
		y := (index >> 8) + WorldMinY
		dst = append(dst, byte((x<<4)|z))
		dst = protocol.AppendInt16(dst, int16(y))
		dst = protocol.AppendVarInt(dst, entity.TypeID)
		// NBT 负载：空 compound（TAG_Compound 0x0A + 空名 + TAG_End）。
		// 槽位内容不通过区块发送，改由打开容器时的 Container Set Content 下发。
		dst = append(dst, 0x0A, 0x00, 0x00)
	}
	return dst
}

// sortInts 对整数切片排序（小规模插入排序，避免引入 sort 依赖）。
func sortInts(values []int) {
	for i := 1; i < len(values); i++ {
		value := values[i]
		j := i - 1
		for j >= 0 && values[j] > value {
			values[j+1] = values[j]
			j--
		}
		values[j+1] = value
	}
}
