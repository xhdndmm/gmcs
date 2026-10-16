//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"
	"net"
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 本文件验证挖掘系统（mining.go）对原版 ServerPlayerGameMode 语义的对齐：
// 挖掘时间（硬度/工具速度/正确工具除数）、STOP 阈值 0.7、延迟破坏、动画与
// Ack 包、正确工具掉落门控、工具耐久、创造模式限制与不可破坏方块。

// miningBlockState 返回方块默认状态的 ID。
func miningBlockState(t *testing.T, name string) uint16 {
	t.Helper()
	state, ok := registry.BlockStateIDs[name]
	if !ok {
		t.Fatalf("方块 %s 不在注册表中", name)
	}
	return state
}

// sendPlayerActionSeq 发送带指定位序号的 Player Action 包。
func sendPlayerActionSeq(t *testing.T, conn net.Conn, status int32, x, y, z int, face byte, sequence int32) {
	t.Helper()
	packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDPlayerAction)
	packet = protocol.AppendVarInt(packet, status)
	packet = protocol.AppendInt64(packet, protocol.PackPosition(x, y, z))
	packet = append(packet, face)
	packet = protocol.AppendVarInt(packet, sequence)
	if err := protocol.WritePacketWithCompression(conn, packet, compressionThreshold); err != nil {
		t.Fatal(err)
	}
}

// miningBarrier 用 /list 往返确保此前发送的 Player Action 已被读循环处理。
func miningBarrier(t *testing.T, conn net.Conn) {
	t.Helper()
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")
}

// miningTargetAtFeet 在玩家脚下的方块位置放置指定方块并返回坐标
// （保证在交互距离内，与出生点地形无关）。
func miningTargetAtFeet(t *testing.T, instance *Server, player *session, name string) (int, int, int) {
	t.Helper()
	px, py, pz, _, _ := player.playerPosition()
	x, z := int(math.Floor(px)), int(math.Floor(pz))
	y := int(math.Floor(py)) - 1
	if !instance.testWorld().SetBlock(x, y, z, miningBlockState(t, name)) {
		t.Fatalf("无法放置测试方块 %s 于 (%d,%d,%d)", name, x, y, z)
	}
	return x, y, z
}

// decodeBlockDestruction 解析 Block Destruction 包的负载（含包 ID），
// 返回方块坐标与破坏进度。
func decodeBlockDestruction(t *testing.T, payload []byte) ([3]int, int8) {
	t.Helper()
	_, offset, err := protocol.DecodeVarInt(payload) // 包 ID
	if err != nil {
		t.Fatal(err)
	}
	_, n, err := protocol.DecodeVarInt(payload[offset:]) // 实体 ID
	if err != nil {
		t.Fatal(err)
	}
	offset += n
	position, offset, err := protocol.DecodeInt64(payload, offset)
	if err != nil || offset >= len(payload) {
		t.Fatalf("解析 Block Destruction 失败：%v", err)
	}
	x, y, z := protocol.UnpackPosition(position)
	return [3]int{x, y, z}, int8(payload[offset])
}

// TestMiningTakesTimeAndDelayedCompletion 验证挖掘需要时间、提前停止不破坏、
// 延迟破坏按原开始刻继续计时直至完成（原版 ServerPlayerGameMode 语义）。
func TestMiningTakesTimeAndDelayedCompletion(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = nil
	instance, conn := joinServer(t, cfg, "MinerTime")
	player := findSession(t, instance, "MinerTime")
	player.setPlayerOnGround(true)
	w := instance.testWorld()
	x, y, z := miningTargetAtFeet(t, instance, player, "minecraft:stone")

	// 空手挖石头：每 tick 进度 = 1/1.5/100 ≈ 0.00667。10 tick 后 STOP 远达不到
	// 0.7 → 进入延迟破坏而不是立即破坏。
	sendPlayerAction(t, conn, 0, x, y, z, 1)
	miningBarrier(t, conn)
	for i := 0; i < 10; i++ {
		instance.tick()
	}
	sendPlayerAction(t, conn, 2, x, y, z, 1)
	miningBarrier(t, conn)
	if got := w.BlockAt(x, y, z); got != world.StoneBlock {
		t.Fatalf("提前停止后方块 = %d，want 石头（进度 < 0.7 不应立即破坏）", got)
	}
	// 延迟破坏继续按原开始刻计时：总进度 1 需要约 150 tick。
	broken := false
	for i := 0; i < 400; i++ {
		instance.tick()
		if w.BlockAt(x, y, z) == world.AirBlock {
			broken = true
			break
		}
	}
	if !broken {
		t.Fatal("延迟破坏未在期限内完成")
	}
	expectBlockUpdate(t, conn, x, y, z, int32(world.AirBlock))
}

// TestMiningStopThresholdWithTool 验证正确工具下挖掘明显更快并在 STOP 时完成：
// 木镐挖石头每 tick 进度 = 2/1.5/30 ≈ 0.0444，20 tick 已超过 0.7。
func TestMiningStopThresholdWithTool(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:wooden_pickaxe"}
	instance, conn := joinServer(t, cfg, "MinerTool")
	player := findSession(t, instance, "MinerTool")
	w := instance.testWorld()
	x, y, z := miningTargetAtFeet(t, instance, player, "minecraft:stone")

	breakBlockSurvivalTicks(t, conn, instance, player, x, y, z, 20)
	expectBlockUpdate(t, conn, x, y, z, int32(world.AirBlock))
	if got := w.BlockAt(x, y, z); got != world.AirBlock {
		t.Fatalf("20 tick 后 STOP 应完成破坏，方块 = %d", got)
	}
	// 正确工具掉落（石头 → 圆石）。
	miningBarrier(t, conn)
	cobbleID, err := registry.ItemID("minecraft:cobblestone")
	if err != nil {
		t.Fatal(err)
	}
	instance.entityMu.Lock()
	var items []*itemEntity
	for _, e := range instance.items {
		items = append(items, e)
	}
	instance.entityMu.Unlock()
	if len(items) != 1 || items[0].Stack.ItemID != cobbleID || items[0].Stack.Count != 1 {
		t.Fatalf("掉落物 = %+v，want 1 个圆石", items)
	}
	// 耐久：木镐挖硬度非 0 的方块损耗 damage_per_block=1。
	if stack := player.inventory.Get(0); stack.ItemID != mustItemID(t, "minecraft:wooden_pickaxe") || stack.Damage != 1 {
		t.Fatalf("木镐耐久 = %+v，want 损耗 1", stack)
	}
}

// TestMiningEmptyHandNoDrops 验证不满足“正确工具”时不掉落
// （原版 Player.hasCorrectToolForDrops 门控）。
func TestMiningEmptyHandNoDrops(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = nil
	instance, conn := joinServer(t, cfg, "MinerBare")
	player := findSession(t, instance, "MinerBare")
	w := instance.testWorld()
	x, y, z := miningTargetAtFeet(t, instance, player, "minecraft:stone")

	breakBlockSurvival(t, conn, instance, player, x, y, z)
	expectBlockUpdate(t, conn, x, y, z, int32(world.AirBlock))
	if got := w.BlockAt(x, y, z); got != world.AirBlock {
		t.Fatalf("方块 = %d，want 空气", got)
	}
	miningBarrier(t, conn)
	instance.entityMu.Lock()
	count := len(instance.items)
	instance.entityMu.Unlock()
	if count != 0 {
		t.Fatalf("空手挖石头掉落了 %d 个物品，want 0（需要正确工具）", count)
	}
}

// TestMiningInstaBreakZeroHardness 验证硬度 0 的方块（火把）在 START 时立即破坏，
// 无需 STOP（原版进度为 +Inf 的“insta mine”路径）。
func TestMiningInstaBreakZeroHardness(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = nil
	instance, conn := joinServer(t, cfg, "MinerTorch")
	player := findSession(t, instance, "MinerTorch")
	player.setPlayerOnGround(true)
	w := instance.testWorld()
	x, y, z := miningTargetAtFeet(t, instance, player, "minecraft:torch")

	// 不需要 STOP：START 即完成破坏。
	sendPlayerAction(t, conn, 0, x, y, z, 1)
	expectBlockUpdate(t, conn, x, y, z, int32(world.AirBlock))
	if got := w.BlockAt(x, y, z); got != world.AirBlock {
		t.Fatalf("火把破坏后 = %d，want 空气", got)
	}
}

// TestMiningAbortPreventsBreak 验证 ABORT 清除挖掘跟踪：即便继续推进 tick，
// 方块也不会破坏、也不产生延迟破坏。
func TestMiningAbortPreventsBreak(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = nil
	instance, conn := joinServer(t, cfg, "MinerAbort")
	player := findSession(t, instance, "MinerAbort")
	player.setPlayerOnGround(true)
	w := instance.testWorld()
	x, y, z := miningTargetAtFeet(t, instance, player, "minecraft:dirt")

	sendPlayerAction(t, conn, 0, x, y, z, 1)
	miningBarrier(t, conn)
	for i := 0; i < 30; i++ {
		instance.tick()
	}
	sendPlayerAction(t, conn, 1, x, y, z, 1) // ABORT
	miningBarrier(t, conn)
	for i := 0; i < 400; i++ {
		instance.tick()
	}
	if got := w.BlockAt(x, y, z); got != world.DirtBlock {
		t.Fatalf("ABORT 后方块 = %d，want 泥土（不应破坏）", got)
	}
}

// TestMiningStopWithoutStartDoesNothing 验证没有 START 记录的 STOP 不产生效果
// （原版只处理与记录目标一致的 STOP）。
func TestMiningStopWithoutStartDoesNothing(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = nil
	instance, conn := joinServer(t, cfg, "MinerStopAlone")
	player := findSession(t, instance, "MinerStopAlone")
	player.setPlayerOnGround(true)
	w := instance.testWorld()
	x, y, z := miningTargetAtFeet(t, instance, player, "minecraft:dirt")

	sendPlayerAction(t, conn, 2, x, y, z, 1) // 直接 STOP
	miningBarrier(t, conn)
	if got := w.BlockAt(x, y, z); got != world.DirtBlock {
		t.Fatalf("无 START 的 STOP 破坏了方块（= %d）", got)
	}
	for i := 0; i < 100; i++ {
		instance.tick()
	}
	if got := w.BlockAt(x, y, z); got != world.DirtBlock {
		t.Fatalf("无 START 的 STOP 引发延迟破坏（= %d）", got)
	}
}

// TestMiningAnimationAndAck 验证 Block Destruction 进度动画与 Block Changed Ack：
//   - START 时立即广播进度包（值 = int(初始进度×10)），ABORT 广播 -1；
//   - Ack 在下一个服务器 Tick 发送，序号取报文中的 sequence。
func TestMiningAnimationAndAck(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = nil
	instance, conn := joinServer(t, cfg, "MinerAnim")
	player := findSession(t, instance, "MinerAnim")
	player.setPlayerOnGround(true)
	w := instance.testWorld()
	x, y, z := miningTargetAtFeet(t, instance, player, "minecraft:stone")

	// START（序号 7）：读循环处理时立即广播进度动画（收到该包即证明 START
	// 已处理）；下一次 Tick 收到 ack。
	sendPlayerActionSeq(t, conn, 0, x, y, z, 1, 7)
	payload := expectPlayPacket(t, conn, protocol.PlayPacketIDBlockDestruction)
	pos, progress := decodeBlockDestruction(t, payload)
	if pos != [3]int{x, y, z} || progress < 0 {
		t.Fatalf("START 进度动画 = %v/%d，want 目标位置且进度 ≥ 0", pos, progress)
	}
	instance.tick()
	ackPayload := expectPlayPacket(t, conn, protocol.PlayPacketIDBlockChangedAck)
	_, offset, err := protocol.DecodeVarInt(ackPayload)
	if err != nil {
		t.Fatal(err)
	}
	sequence, _, err := protocol.DecodeVarInt(ackPayload[offset:])
	if err != nil || sequence != 7 {
		t.Fatalf("ack 序号 = %d (err=%v)，want 7", sequence, err)
	}

	// ABORT（序号 8）：读循环广播进度 -1 清除动画；下一次 Tick 收到 ack 8。
	sendPlayerActionSeq(t, conn, 1, x, y, z, 1, 8)
	payload = expectPlayPacket(t, conn, protocol.PlayPacketIDBlockDestruction)
	if pos, progress = decodeBlockDestruction(t, payload); pos != [3]int{x, y, z} || progress != -1 {
		t.Fatalf("ABORT 进度动画 = %v/%d，want 目标位置与 -1", pos, progress)
	}
	instance.tick()
	ackPayload = expectPlayPacket(t, conn, protocol.PlayPacketIDBlockChangedAck)
	_, offset, err = protocol.DecodeVarInt(ackPayload)
	if err != nil {
		t.Fatal(err)
	}
	sequence, _, err = protocol.DecodeVarInt(ackPayload[offset:])
	if err != nil || sequence != 8 {
		t.Fatalf("ack 序号 = %d (err=%v)，want 8", sequence, err)
	}
	if got := w.BlockAt(x, y, z); got != world.StoneBlock {
		t.Fatalf("ABORT 后不应破坏（= %d）", got)
	}
}

// TestMiningOutOfReachRejected 验证超出交互距离（原版 4.5 + 1.0 容差）的挖掘
// 请求被忽略。
func TestMiningOutOfReachRejected(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = nil
	instance, conn := joinServer(t, cfg, "MinerFar")
	player := findSession(t, instance, "MinerFar")
	player.setPlayerOnGround(true)
	px, py, pz, _, _ := player.playerPosition()
	x, z := int(math.Floor(px))+8, int(math.Floor(pz))
	y := int(math.Floor(py)) - 1
	w := instance.testWorld()
	if !w.SetBlock(x, y, z, world.StoneBlock) {
		t.Fatal("无法放置远处测试方块")
	}
	sendPlayerAction(t, conn, 0, x, y, z, 1)
	miningBarrier(t, conn)
	for i := 0; i < 200; i++ {
		instance.tick()
	}
	sendPlayerAction(t, conn, 2, x, y, z, 1)
	miningBarrier(t, conn)
	for i := 0; i < 100; i++ {
		instance.tick()
	}
	if got := w.BlockAt(x, y, z); got != world.StoneBlock {
		t.Fatalf("超出距离的方块被破坏（= %d）", got)
	}
}

// TestMiningSwordCannotBreakInCreative 验证创造模式下持有
// can_destroy_blocks_in_creative=false 的物品（剑）不能破坏方块，且服务端回发
// 方块状态纠正；换成镐后可以破坏。
func TestMiningSwordCannotBreakInCreative(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.GameMode = "creative"
	cfg.StartingItems = []string{"minecraft:wooden_sword"}
	instance, conn := joinServer(t, cfg, "MinerSword")
	player := findSession(t, instance, "MinerSword")
	w := instance.testWorld()
	x, y, z := miningTargetAtFeet(t, instance, player, "minecraft:stone")

	// 剑：破坏被拒绝 + 回发方块状态（石头）。
	sendPlayerAction(t, conn, 0, x, y, z, 1)
	expectBlockUpdate(t, conn, x, y, z, int32(world.StoneBlock))
	if got := w.BlockAt(x, y, z); got != world.StoneBlock {
		t.Fatalf("剑在创造模式破坏了方块（= %d）", got)
	}

	// 换钻石镐：破坏成功。
	sendSetCreativeSlot(t, conn, 0, mustItemID(t, "minecraft:diamond_pickaxe"), 1)
	sendPlayerAction(t, conn, 0, x, y, z, 1)
	expectBlockUpdate(t, conn, x, y, z, int32(world.AirBlock))
	if got := w.BlockAt(x, y, z); got != world.AirBlock {
		t.Fatalf("换上镐后仍未破坏（= %d）", got)
	}
}

// TestMiningGameMasterBlockDenied 验证命令方块（原版 GameMasterBlock）在
// 非管理员下不可破坏，且回发方块状态纠正（生存与创造一致）。
func TestMiningGameMasterBlockDenied(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.GameMode = "creative"
	cfg.StartingItems = []string{"minecraft:diamond_pickaxe"}
	instance, conn := joinServer(t, cfg, "MinerGM")
	player := findSession(t, instance, "MinerGM")
	w := instance.testWorld()
	commandBlock := miningBlockState(t, "minecraft:command_block")
	x, y, z := miningTargetAtFeet(t, instance, player, "minecraft:command_block")

	sendPlayerAction(t, conn, 0, x, y, z, 1)
	expectBlockUpdate(t, conn, x, y, z, int32(commandBlock))
	if got := w.BlockAt(x, y, z); got != commandBlock {
		t.Fatalf("命令方块被破坏（= %d）", got)
	}

	// 生存模式同样不可破坏。
	player.setGameMode(uint8(config.GameModeSurvival))
	sendPlayerAction(t, conn, 0, x, y, z, 1)
	miningBarrier(t, conn)
	for i := 0; i < 300; i++ {
		instance.tick()
	}
	sendPlayerAction(t, conn, 2, x, y, z, 1)
	miningBarrier(t, conn)
	for i := 0; i < 300; i++ {
		instance.tick()
	}
	if got := w.BlockAt(x, y, z); got != commandBlock {
		t.Fatalf("生存模式破坏了命令方块（= %d）", got)
	}
}

// TestMiningBedrockSurvivalOnlyCreative 验证基岩（硬度 -1）：生存模式无法破坏，
// 创造模式可以破坏（与原版一致）。
func TestMiningBedrockSurvivalOnlyCreative(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:diamond_pickaxe"}
	instance, conn := joinServer(t, cfg, "MinerBedrock")
	player := findSession(t, instance, "MinerBedrock")
	player.setPlayerOnGround(true)
	w := instance.testWorld()
	x, y, z := miningTargetAtFeet(t, instance, player, "minecraft:bedrock")

	// 生存：进度恒为 0，不破坏、不产生延迟破坏。
	sendPlayerAction(t, conn, 0, x, y, z, 1)
	miningBarrier(t, conn)
	for i := 0; i < 300; i++ {
		instance.tick()
	}
	sendPlayerAction(t, conn, 2, x, y, z, 1)
	miningBarrier(t, conn)
	for i := 0; i < 300; i++ {
		instance.tick()
	}
	if got := w.BlockAt(x, y, z); got != world.BedrockBlock {
		t.Fatalf("生存模式破坏了基岩（= %d）", got)
	}

	// 创造：START 立即破坏。
	player.setGameMode(uint8(config.GameModeCreative))
	sendPlayerAction(t, conn, 0, x, y, z, 1)
	expectBlockUpdate(t, conn, x, y, z, int32(world.AirBlock))
	if got := w.BlockAt(x, y, z); got != world.AirBlock {
		t.Fatalf("创造模式未破坏基岩（= %d）", got)
	}
}

// TestSwapItemWithOffhand 验证 F 键交换主手与副手（原版
// SWAP_ITEM_WITH_OFFHAND，状态 6）。
func TestSwapItemWithOffhand(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.StartingItems = []string{"minecraft:stone*2"}
	instance, conn := joinServer(t, cfg, "Swapper")
	player := findSession(t, instance, "Swapper")

	stoneID := mustItemID(t, "minecraft:stone")
	if stack := player.inventory.Get(0); stack.ItemID != stoneID || stack.Count != 2 {
		t.Fatalf("初始主手 = %+v，want 石头 x2", stack)
	}
	// 主手石头 → 副手（副手原为空）。
	sendPlayerAction(t, conn, 6, 0, 0, 0, 0)
	miningBarrier(t, conn)
	if stack := player.inventory.Get(0); !stack.IsEmpty() {
		t.Fatalf("交换后主手 = %+v，want 空", stack)
	}
	if stack := player.inventory.Get(item.SlotOffhand); stack.ItemID != stoneID || stack.Count != 2 {
		t.Fatalf("交换后副手 = %+v，want 石头 x2", stack)
	}
	// 再交换一次：石头回到主手。
	sendPlayerAction(t, conn, 6, 0, 0, 0, 0)
	miningBarrier(t, conn)
	if stack := player.inventory.Get(0); stack.ItemID != stoneID || stack.Count != 2 {
		t.Fatalf("二次交换后主手 = %+v，want 石头 x2", stack)
	}
	if stack := player.inventory.Get(item.SlotOffhand); !stack.IsEmpty() {
		t.Fatalf("二次交换后副手 = %+v，want 空", stack)
	}
}
