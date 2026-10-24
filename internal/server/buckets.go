//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 桶（对照 1.21.11 反编译的 BucketItem 与发射器的桶行为 lg$10/lg$11）。
//
//   - 空桶点击源流体（level 0 的水/岩浆）→ 收起流体并把装满的桶交给玩家；
//   - 水桶/岩浆桶点击可替换位置 → 放置源流体并退还空桶；
//   - 下界倒水：不放置流体，播放咝咝声（世界事件 1501），桶仍被消耗；
//   - 发射器：空桶收集前方源流体（lg$11，consumeWithRemainder：数量归零时
//     返回装满的桶，否则把装满的桶放回发射器、放不下时弹出）；
//     水桶/岩浆桶在前方放置流体（lg$10，成功时变为空桶）。

const (
	bucketItemName      = "minecraft:bucket"
	waterBucketItemName = "minecraft:water_bucket"
	lavaBucketItemName  = "minecraft:lava_bucket"
	// powderSnowBucketItemName 在下界/放置逻辑中不可放置（无细雪方块模拟）：
	// 与一个普通桶一致地回退为移除/弹出。
	powderSnowBucketItemName = "minecraft:powder_snow_bucket"
)

// bucketFluidName 返回桶物品对应的流体名（非流体桶返回 false）。
func bucketFluidName(name string) (string, bool) {
	switch name {
	case waterBucketItemName:
		return waterBlockName, true
	case lavaBucketItemName:
		return lavaBlockName, true
	}
	return "", false
}

// playBucketSound 向附近玩家播放桶音效（空/满、水/岩浆）。
func (s *Server) playBucketSound(dim world.Dimension, x, y, z int, name string) {
	soundID, ok := registry.StaticEntryID("minecraft:sound_event", name)
	if !ok {
		return
	}
	s.writeToNearbyPlayers(protocol.EncodeSoundEffect(soundID, protocol.SoundCategoryBlock,
		float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1, 1, 0), dim, float64(x), float64(z), nil)
}

// placeFluidSource 在 (x,y,z) 放置流体源（可替换且非同一流体源时）。
// ultraWarmDim 为 true（下界）时不放置水（返回 fizz=true, placed=false）。
// 返回 (placed, fizz)。
func (s *Server) placeFluidSource(w *world.World, dim world.Dimension, x, y, z int, name string) (bool, bool) {
	if _, ok := world.SectionIndex(y); !ok {
		return false, false
	}
	state := w.BlockAt(x, y, z)
	if info, ok := fluidInfoOf(state); ok && info.name == name && info.source {
		return false, false // 原版：不把流体倒入同种源方块
	}
	if !s.fluidReplaceable(state) && state != world.AirBlock {
		return false, false
	}
	if name == waterBlockName && ultraWarm(dim) {
		return false, true
	}
	source, ok := fluidSourceState(name)
	if !ok {
		return false, false
	}
	if !w.SetBlock(x, y, z, source) {
		return false, false
	}
	s.broadcastBlockUpdate(dim, x, y, z, int32(source))
	s.updateFluidsAround(dim, x, y, z)
	s.scheduleFluidTick(dim, [3]int{x, y, z}, fluidTickDelay(name, dim))
	return true, false
}

// tryUseBucket 处理玩家右键方块时的桶逻辑（放在容器交互之后、方块放置之前）。
// 返回 true 表示已处理（调用方跳过后续放置流程）。
func (s *Server) tryUseBucket(player *session, w *world.World, use protocol.UseItemOn) bool {
	stack := player.inventory.Get(player.selectedHotbarSlot())
	if stack.IsEmpty() {
		return false
	}
	name, ok := registry.ItemName(stack.ItemID)
	if !ok {
		return false
	}
	dim := w.Dimension()
	offset := blockFaceOffsets[use.Direction]
	// 空桶：收集点击位置的源流体（原版 SOURCE_ONLY 射线检测）。
	if name == bucketItemName {
		targetState := w.BlockAt(use.X, use.Y, use.Z)
		info, isFluid := fluidInfoOf(targetState)
		if !isFluid || !info.source {
			return false
		}
		filled, err := registry.ItemID(filledBucketItemName(info.name))
		if err != nil {
			return false
		}
		if !w.SetBlock(use.X, use.Y, use.Z, world.AirBlock) {
			return false
		}
		s.broadcastBlockUpdate(dim, use.X, use.Y, use.Z, int32(world.AirBlock))
		s.updateFluidsAround(dim, use.X, use.Y, use.Z)
		s.playBucketSound(dim, use.X, use.Y, use.Z, bucketFillSound(info.name))
		s.replaceHeldItemWithRemainder(player, filled)
		return true
	}
	// 水桶/岩浆桶：在点击面外侧放置源流体。
	fluidName, isBucket := bucketFluidName(name)
	if !isBucket {
		return false
	}
	tx, ty, tz := use.X+offset[0], use.Y+offset[1], use.Z+offset[2]
	placed, fizz := s.placeFluidSource(w, dim, tx, ty, tz, fluidName)
	if fizz {
		s.broadcastWorldEvent(dim, [3]int{tx, ty, tz}, 1501, 0)
		placed = true // 桶被消耗但流体蒸发
	}
	if !placed {
		return false
	}
	s.playBucketSound(dim, tx, ty, tz, bucketEmptySound(fluidName))
	// 生存模式消耗水桶并退还空桶；创造模式保留当前桶（原版创造不消耗）。
	if player.gameModeID() == 1 {
		return true
	}
	bucketID, err := registry.ItemID(bucketItemName)
	if err != nil {
		return true
	}
	s.replaceHeldItemWithRemainder(player, bucketID)
	return true
}

// filledBucketItemName 返回流体对应的装满桶物品名。
func filledBucketItemName(fluidName string) string {
	if fluidName == lavaBlockName {
		return lavaBucketItemName
	}
	return waterBucketItemName
}

// bucketFillSound / bucketEmptySound 返回桶音效名（水/岩浆不同）。
func bucketFillSound(fluidName string) string {
	if fluidName == lavaBlockName {
		return "minecraft:item.bucket.fill_lava"
	}
	return "minecraft:item.bucket.fill"
}

func bucketEmptySound(fluidName string) string {
	if fluidName == lavaBlockName {
		return "minecraft:item.bucket.empty_lava"
	}
	return "minecraft:item.bucket.empty"
}

// replaceHeldItemWithRemainder 消耗手中 1 个物品并把 result 放回（原版
// consumeWithRemainder 的玩家版本）：数量归零时直接替换槽位物品，
// 否则把 result 放入背包（放不下时掉落）。
func (s *Server) replaceHeldItemWithRemainder(player *session, resultItemID int32) {
	slot := player.selectedHotbarSlot()
	stack := player.inventory.Get(slot)
	stack.Count--
	if stack.Count <= 0 {
		replacement := item.Stack{ItemID: resultItemID, Count: 1}
		player.inventory.Set(slot, replacement)
		player.tryWrite(protocol.EncodeSetPlayerInventory(int32(slot), replacement.AppendSlot(nil)))
		return
	}
	player.inventory.Set(slot, stack)
	player.tryWrite(protocol.EncodeSetPlayerInventory(int32(slot), stack.AppendSlot(nil)))
	remainder := item.Stack{ItemID: resultItemID, Count: 1}
	remaining, changed := player.inventory.Add(remainder)
	for _, changedSlot := range changed {
		player.tryWrite(protocol.EncodeSetPlayerInventory(int32(changedSlot), player.inventory.Get(changedSlot).AppendSlot(nil)))
	}
	if remaining > 0 {
		remainder.Count = remaining
		s.dropItemFromPlayerStack(player, remainder)
	}
}

// dropItemFromPlayerStack 把剩余物品以掉落物形式在玩家脚边生成。
func (s *Server) dropItemFromPlayerStack(player *session, stack item.Stack) {
	if stack.IsEmpty() {
		return
	}
	x, y, z, _, _ := player.playerPosition()
	s.spawnItem(player.dimensionID(), stack, x, y+0.25, z, 0, 0.1, 0, itemPickupDelayPlayer)
}

// dispenserEmptyBucket 处理发射器的“倒出流体”行为（原版 lg$10）：
// 在前方放置流体，成功时返回空桶；失败时返回 (stack, false) 由调用方弹出。
func (s *Server) dispenserEmptyBucket(w *world.World, dim world.Dimension, pos [3]int, facing string, stack item.Stack, name string) (item.Stack, bool) {
	fluidName, ok := bucketFluidName(name)
	if !ok {
		return stack, false
	}
	offset := dispenserFacingOffset(facing)
	x, y, z := pos[0]+offset[0], pos[1]+offset[1], pos[2]+offset[2]
	placed, fizz := s.placeFluidSource(w, dim, x, y, z, fluidName)
	if fizz {
		s.broadcastWorldEvent(dim, [3]int{x, y, z}, 1501, 0)
	}
	if !placed && !fizz {
		return stack, false
	}
	bucketID, err := registry.ItemID(bucketItemName)
	if err != nil {
		return stack, false
	}
	return item.Stack{ItemID: bucketID, Count: 1}, true
}

// dispenserFillBucket 处理发射器的“装满桶”行为（原版 lg$11）：
// 前方是源流体时收起并消耗 1 个空桶；返回 (槽位剩余, 装满的桶, 是否成功)。
// 装满的桶由调用方放回发射器（放不下时弹出）。
func (s *Server) dispenserFillBucket(w *world.World, dim world.Dimension, pos [3]int, facing string, stack item.Stack) (item.Stack, item.Stack, bool) {
	offset := dispenserFacingOffset(facing)
	x, y, z := pos[0]+offset[0], pos[1]+offset[1], pos[2]+offset[2]
	if _, ok := world.SectionIndex(y); !ok {
		return stack, item.Empty(), false
	}
	state := w.BlockAt(x, y, z)
	info, isFluid := fluidInfoOf(state)
	if !isFluid || !info.source {
		return stack, item.Empty(), false
	}
	filledName := filledBucketItemName(info.name)
	filledID, err := registry.ItemID(filledName)
	if err != nil {
		return stack, item.Empty(), false
	}
	if !w.SetBlock(x, y, z, world.AirBlock) {
		return stack, item.Empty(), false
	}
	s.broadcastBlockUpdate(dim, x, y, z, int32(world.AirBlock))
	s.updateFluidsAround(dim, x, y, z)
	s.broadcastWorldEvent(dim, [3]int{x, y, z}, 1000, 0)
	// consumeWithRemainder：槽位减少 1；数量归零时返回装满的桶作为槽位内容。
	remaining := stack
	remaining.Count--
	filled := item.Stack{ItemID: filledID, Count: 1}
	if remaining.Count <= 0 {
		return filled, item.Empty(), true
	}
	return remaining, filled, true
}
