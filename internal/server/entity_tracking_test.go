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
	"gmcs/internal/world"
)

// trackedEntityIDs 返回会话当前跟踪（已下发给客户端）的实体 ID 集合。
func trackedEntityIDs(t *testing.T, instance *Server, player *session) map[int32]struct{} {
	t.Helper()
	instance.entityMu.Lock()
	defer instance.entityMu.Unlock()
	out := make(map[int32]struct{})
	if tr := instance.entityTrack[player]; tr != nil {
		for id := range tr.ids {
			out[id] = struct{}{}
		}
	}
	return out
}

// parseAddEntityID 解析 Add Entity 负载开头的实体 ID（负载含包 ID 前缀）。
func parseAddEntityID(t *testing.T, payload []byte) int32 {
	t.Helper()
	_, offset, err := protocol.DecodeVarInt(payload)
	if err != nil {
		t.Fatalf("parse AddEntity packet id: %v", err)
	}
	id, _, err := protocol.DecodeVarInt(payload[offset:])
	if err != nil {
		t.Fatalf("parse AddEntity id: %v", err)
	}
	return id
}

// parseRemovedEntityIDs 解析 Remove Entities 负载中的实体 ID 集合
// （负载含包 ID 前缀；DecodeVarInt 的偏移相对子切片，必须累计）。
func parseRemovedEntityIDs(t *testing.T, payload []byte) map[int32]struct{} {
	t.Helper()
	_, offset, err := protocol.DecodeVarInt(payload)
	if err != nil {
		t.Fatalf("parse RemoveEntities packet id: %v", err)
	}
	count, n, err := protocol.DecodeVarInt(payload[offset:])
	if err != nil {
		t.Fatalf("parse RemoveEntities count: %v", err)
	}
	offset += n
	out := make(map[int32]struct{}, count)
	for i := int32(0); i < count; i++ {
		id, n, err := protocol.DecodeVarInt(payload[offset:])
		if err != nil {
			t.Fatalf("parse RemoveEntities id %d: %v", i, err)
		}
		out[id] = struct{}{}
		offset += n
	}
	return out
}

// sendClientRespawn 发送 Client Status（action 0 = 请求重生）。
func sendClientRespawn(t *testing.T, conn net.Conn) {
	t.Helper()
	packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDClientCommand)
	packet = protocol.AppendVarInt(packet, 0)
	if err := protocol.WritePacketWithCompression(conn, packet, compressionThreshold); err != nil {
		t.Fatal(err)
	}
}

// TestItemSentWhenPlayerApproaches 验证：实体在玩家 64 格外生成时不下发，
// 玩家靠近后由实体跟踪补发 Add Entity（单一玩家用掉落物驱动：
// 掉落物不会按距离反弃用）。
func TestItemSentWhenPlayerApproaches(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Approacher")
	player := findSession(t, instance, "Approacher")
	processBarrier(t, conn)

	x, y, z, _, _ := player.playerPosition()
	stack, err := item.FromName("minecraft:dirt", 1)
	if err != nil {
		t.Fatal(err)
	}
	dropped := instance.spawnItem(world.DimensionOverworld, stack, x+100, y, z, 0, 0, 0, 200)
	if dropped == nil {
		t.Fatal("spawnItem returned nil")
	}
	// 生成点在 64 格外：不应进入跟踪集。
	if _, ok := trackedEntityIDs(t, instance, player)[dropped.ID]; ok {
		t.Fatal("far item was tracked at spawn")
	}

	// 玩家靠近到 3 格：下一帧应补发 Add Entity。
	player.setPlayerPosition(x+97, y, z, 0, 0)
	runTicks(instance, 1)
	payload := expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)
	if got := parseAddEntityID(t, payload); got != dropped.ID {
		t.Fatalf("AddEntity id = %d, want item %d", got, dropped.ID)
	}
	if _, ok := trackedEntityIDs(t, instance, player)[dropped.ID]; !ok {
		t.Fatal("item not tracked after approach")
	}
}

// TestEntityRemovedWhenPlayerLeavesRange 验证：实体离开移除范围（64+16 格滞回）
// 后，服务端向玩家下发 Remove Entities 并清理跟踪集。
func TestEntityRemovedWhenPlayerLeavesRange(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Leaver")
	player := findSession(t, instance, "Leaver")
	processBarrier(t, conn)

	x, y, z, _, _ := player.playerPosition()
	stack, err := item.FromName("minecraft:dirt", 1)
	if err != nil {
		t.Fatal(err)
	}
	dropped := instance.spawnItem(world.DimensionOverworld, stack, x+10, y, z, 0, 0, 0, 200)
	if dropped == nil {
		t.Fatal("spawnItem returned nil")
	}
	// 10 格内生成：立即下发（消费 Add Entity 与本帧的其他包）。
	payload := expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)
	if got := parseAddEntityID(t, payload); got != dropped.ID {
		t.Fatalf("AddEntity id = %d, want item %d", got, dropped.ID)
	}

	// 移动到 110 格外：超过移除范围（80 格）。
	player.setPlayerPosition(x+120, y, z, 0, 0)
	runTicks(instance, 1)
	removePayload := expectPlayPacket(t, conn, protocol.PlayPacketIDRemoveEntities)
	if _, ok := parseRemovedEntityIDs(t, removePayload)[dropped.ID]; !ok {
		t.Fatalf("RemoveEntities does not contain item %d", dropped.ID)
	}
	if _, ok := trackedEntityIDs(t, instance, player)[dropped.ID]; ok {
		t.Fatal("item still tracked after leaving range")
	}
}

// TestMobSentToApproachingPlayer 验证多人场景：生物在距玩家 B 较远处生成
// （B 未收到 Add Entity），B 靠近后由实体跟踪补发；B 再次离开后收到移除。
func TestMobSentToApproachingPlayer(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.ViewDistance = 2 // 与 joinServer 内部设置一致（第二个玩家的 joinAt 依赖同一值）
	instance, connA := joinServer(t, cfg, "Alpha")
	playerA := findSession(t, instance, "Alpha")
	processBarrier(t, connA)

	connB := joinAt(t, cfg, connA.RemoteAddr().String(), "Beta")
	playerB := findSession(t, instance, "Beta")
	processBarrier(t, connB)

	ax, ay, az, _, _ := playerA.playerPosition()
	bx, by, bz, _, _ := playerB.playerPosition()
	_ = bx
	_ = by

	// B 远离出生点：A 的玩家实体被移除。
	playerB.setPlayerPosition(bx+300, by, bz, 0, 0)
	runTicks(instance, 1)
	removePayload := expectPlayPacket(t, connB, protocol.PlayPacketIDRemoveEntities)
	if _, ok := parseRemovedEntityIDs(t, removePayload)[playerA.entityID]; !ok {
		t.Fatalf("RemoveEntities does not contain player entity %d", playerA.entityID)
	}

	// 生物在 A 附近生成：B 距离过远，不进入 B 的跟踪集。
	mob := instance.addMob(world.DimensionOverworld, mobZombie, ax+8, ay, az)
	if mob == nil {
		t.Fatal("addMob returned nil")
	}
	if _, ok := trackedEntityIDs(t, instance, playerB)[mob.ID]; ok {
		t.Fatal("far mob was tracked for Beta")
	}

	// B 靠近生物：下一帧补发 Add Entity（先于 A 的玩家实体）。
	playerB.setPlayerPosition(ax+5, ay, az, 0, 0)
	runTicks(instance, 1)
	payload := expectPlayPacket(t, connB, protocol.PlayPacketIDAddEntity)
	if got := parseAddEntityID(t, payload); got != mob.ID {
		t.Fatalf("AddEntity id = %d, want mob %d", got, mob.ID)
	}
	tracked := trackedEntityIDs(t, instance, playerB)
	if _, ok := tracked[mob.ID]; !ok {
		t.Fatal("mob not tracked after approach")
	}

	// B 再次离开：生物与 A 的玩家实体都应被移除。
	playerB.setPlayerPosition(ax+300, ay, az, 0, 0)
	runTicks(instance, 1)
	removePayload = expectPlayPacket(t, connB, protocol.PlayPacketIDRemoveEntities)
	removed := parseRemovedEntityIDs(t, removePayload)
	if _, ok := removed[mob.ID]; !ok {
		t.Fatalf("RemoveEntities does not contain mob %d", mob.ID)
	}
	if _, ok := removed[playerA.entityID]; !ok {
		t.Fatalf("RemoveEntities does not contain player entity %d", playerA.entityID)
	}
}

// TestPlayerEntitySentWhenOtherApproaches 验证玩家实体也走同一套跟踪：
// 远处玩家的实体在加入时不下发，靠近后补发（含皮肤与装备）。
func TestPlayerEntitySentWhenOtherApproaches(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.ViewDistance = 2 // 与 joinServer 内部设置一致（第二个玩家的 joinAt 依赖同一值）
	instance, connA := joinServer(t, cfg, "FarAway")
	playerA := findSession(t, instance, "FarAway")
	processBarrier(t, connA)

	// A 远离出生点（同维度传送，不下发实体）。
	ax, ay, az, _, _ := playerA.playerPosition()
	playerA.setPlayerPosition(ax+500, ay, az, 0, 0)
	runTicks(instance, 1)

	// B 在出生点加入：A 不在视野内，B 不应跟踪 A 的实体。
	connB := joinAt(t, cfg, connA.RemoteAddr().String(), "Newcomer")
	playerB := findSession(t, instance, "Newcomer")
	processBarrier(t, connB)
	if _, ok := trackedEntityIDs(t, instance, playerB)[playerA.entityID]; ok {
		t.Fatal("distant player was tracked at join")
	}

	// A 靠近 B：下一帧补发 A 的玩家实体（Add Entity 后跟皮肤元数据与装备）。
	bx, by, bz, _, _ := playerB.playerPosition()
	playerA.setPlayerPosition(bx+5, by, bz, 0, 0)
	runTicks(instance, 1)
	payload := expectPlayPacket(t, connB, protocol.PlayPacketIDAddEntity)
	if got := parseAddEntityID(t, payload); got != playerA.entityID {
		t.Fatalf("AddEntity id = %d, want player %d", got, playerA.entityID)
	}
	// 随后应收到 Set Equipment（0x64，全部 6 槽）。
	expectPlayPacket(t, connB, protocol.PlayPacketIDSetEquipment)
	if _, ok := trackedEntityIDs(t, instance, playerB)[playerA.entityID]; !ok {
		t.Fatal("player entity not tracked after approach")
	}
}

// TestRespawnResendsNearbyEntities 验证死亡重生后：客户端清空世界，
// 服务端在 resume 后重新补发范围内实体。
func TestRespawnResendsNearbyEntities(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Reborn")
	player := findSession(t, instance, "Reborn")
	processBarrier(t, conn)

	x, y, z, _, _ := player.playerPosition()
	mob := instance.addMob(world.DimensionOverworld, mobZombie, x+8, y, z)
	if mob == nil {
		t.Fatal("addMob returned nil")
	}
	payload := expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)
	if got := parseAddEntityID(t, payload); got != mob.ID {
		t.Fatalf("initial AddEntity id = %d, want mob %d", got, mob.ID)
	}

	// 击杀玩家并请求重生。
	if !instance.damagePlayer(player, 1000, "test", 0, instance.mobAttackDamageTypeID, nil) {
		t.Fatal("damagePlayer did not apply")
	}
	// 死亡伤害与生命值更新（0），作为死亡流程的屏障。
	expectPlayPacket(t, conn, protocol.PlayPacketIDUpdateHealth)
	sendClientRespawn(t, conn)
	expectPlayPacket(t, conn, protocol.PlayPacketIDRespawn)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSynchronizePlayerPos)
	// UpdateHealth 在 resume 之后发送：收到它说明重生流程已离开暂停窗口。
	expectPlayPacket(t, conn, protocol.PlayPacketIDUpdateHealth)

	runTicks(instance, 1)
	payload = expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)
	if got := parseAddEntityID(t, payload); got != mob.ID {
		t.Fatalf("AddEntity after respawn = %d, want mob %d", got, mob.ID)
	}
	if _, ok := trackedEntityIDs(t, instance, player)[mob.ID]; !ok {
		t.Fatal("mob not tracked after respawn")
	}
}

// TestDimensionChangeResyncsEntities 验证跨维度传送后：新维度范围内的实体
// 会在 resume 后补发（旧实现只在玩家加入时下发一次实体）。
func TestDimensionChangeResyncsEntities(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.Ops = []string{"Traveler"}
	instance, conn := joinServer(t, cfg, "Traveler")
	player := findSession(t, instance, "Traveler")

	spawnX, spawnY, spawnZ := instance.spawnPositionFor(world.DimensionNether)
	baseX, baseZ := int(math.Floor(spawnX)), int(math.Floor(spawnZ))
	floorY := int(math.Floor(spawnY)) - 1
	nether := instance.worldFor(world.DimensionNether)
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			nether.SetBlock(baseX+dx, floorY, baseZ+dz, world.NetherrackBlock)
			for dy := 1; dy <= 3; dy++ {
				nether.SetBlock(baseX+dx, floorY+dy, baseZ+dz, world.AirBlock)
			}
		}
	}
	mob := instance.addMob(world.DimensionNether, mobZombie, spawnX, spawnY, spawnZ+2)
	if mob == nil {
		t.Fatal("addMob(nether) returned nil")
	}
	if _, ok := trackedEntityIDs(t, instance, player)[mob.ID]; ok {
		t.Fatal("nether mob was tracked while player is in overworld")
	}

	sendChatCommand(t, conn, "/dimension the_nether")
	expectPlayPacket(t, conn, protocol.PlayPacketIDRespawn)
	expectPlayPacket(t, conn, protocol.PlayPacketIDSynchronizePlayerPos)
	// UpdateHealth 在 resume 之后发送：作为“已离开暂停窗口”的屏障。
	expectPlayPacket(t, conn, protocol.PlayPacketIDUpdateHealth)

	runTicks(instance, 1)
	payload := expectPlayPacket(t, conn, protocol.PlayPacketIDAddEntity)
	if got := parseAddEntityID(t, payload); got != mob.ID {
		t.Fatalf("AddEntity in nether = %d, want mob %d", got, mob.ID)
	}
	if id := player.dimensionID(); id != world.DimensionNether {
		t.Fatalf("player dimension = %v, want nether", id)
	}
}
