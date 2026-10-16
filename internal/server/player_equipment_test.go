//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"encoding/hex"
	"net"
	"testing"
	"time"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
)

// equipmentEntries 解析 Set Equipment 包为 (实体 ID, 槽位 → 物品)。
func equipmentEntries(t *testing.T, payload []byte) (int32, map[byte]itemStack) {
	t.Helper()
	_, offset, err := protocol.DecodeVarInt(payload)
	if err != nil {
		t.Fatal(err)
	}
	entityID, size, err := protocol.DecodeVarInt(payload[offset:])
	if err != nil {
		t.Fatal(err)
	}
	offset += size
	slots := make(map[byte]itemStack)
	for {
		if offset >= len(payload) {
			t.Fatal("Set Equipment 包在槽位列表中途结束")
		}
		raw := payload[offset]
		offset++
		slot := raw &^ 0x80
		itemID, count, err := protocol.ParseSlotItem(payload[offset:])
		if err != nil {
			t.Fatal(err)
		}
		_, next := parseSlotLength(t, payload, offset)
		offset = next
		slots[slot] = itemStack{ID: itemID, Count: count}
		// 高位为“后面还有条目”（抓包实测：首条 0x80、末条不带高位）。
		if raw&0x80 == 0 {
			return entityID, slots
		}
	}
}

// TestSetEquipmentEncodingMatchesVanilla 逐字节对照官方服务端抓包：
// 带主手钻石剑与头盔的实体的配对数据为
// 64 56 80 01 a8 07 00 00 05 01 ca 07 00 00
// （0x64 包 ID、0x56 实体、0x80 主手且“后续还有条目”、0x05 末条头盔、
// 槽位负载为 count + 物品 ID + 组件计数 0/0）。
func TestSetEquipmentEncodingMatchesVanilla(t *testing.T) {
	swordID, err := registry.ItemID("minecraft:diamond_sword")
	if err != nil {
		t.Fatal(err)
	}
	helmetID, err := registry.ItemID("minecraft:diamond_helmet")
	if err != nil {
		t.Fatal(err)
	}
	if swordID != 936 || helmetID != 970 {
		t.Fatalf("物品 ID 变化（剑 %d、盔 %d），需要重新核对抓包", swordID, helmetID)
	}
	packet := protocol.EncodeSetEquipment(0x56, []protocol.EquipmentSlotEntry{
		{Slot: protocol.EquipmentMainHand, Data: item.Stack{ItemID: swordID, Count: 1}.AppendSlot(nil)},
		{Slot: protocol.EquipmentHead, Data: item.Stack{ItemID: helmetID, Count: 1}.AppendSlot(nil)},
	})
	want := "64568001a80700000501ca070000"
	if got := hex.EncodeToString(packet); got != want {
		t.Fatalf("Set Equipment 编码 = %s, want %s（对照官方抓包）", got, want)
	}
}

// expectEquipmentFor 读取 Set Equipment 包直到出现指定实体的装备包
// （同一时刻附近玩家的装备包可能先到）。
func expectEquipmentFor(t *testing.T, conn net.Conn, entityID int32) map[byte]itemStack {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatalf("未收到实体 %d 的装备包", entityID)
		}
		payload := expectPlayPacket(t, conn, protocol.PlayPacketIDSetEquipment)
		gotID, slots := equipmentEntries(t, payload)
		if gotID == entityID {
			return slots
		}
	}
}

// TestPlayerEquipmentSync 验证其他玩家可见的装备同步：
// 加入时的完整装备、切换快捷栏（主手变化）、空槽位同步（count=0）。
func TestPlayerEquipmentSync(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.ViewDistance = 2
	cfg.StartingItems = []string{"minecraft:diamond_sword"}
	instance, watcherConn := joinServer(t, cfg, "Watcher")
	addr := watcherConn.RemoteAddr().String()
	swordID, err := registry.ItemID("minecraft:diamond_sword")
	if err != nil {
		t.Fatal(err)
	}

	// 第二名玩家加入：观察者收到其实体与（空的）装备。
	actorConn := joinAt(t, cfg, addr, "Actor")
	actor := findSession(t, instance, "Actor")
	if actor == nil {
		t.Fatal("未找到 Actor 会话")
	}
	slots := expectEquipmentFor(t, watcherConn, actor.entityID)
	if got, ok := slots[protocol.EquipmentMainHand]; !ok || got.Count != 0 {
		t.Fatalf("新玩家主手应为空，得到 %+v（存在=%v）", got, ok)
	}

	// 给 Actor 发放钻石剑（直接写物品栏，模拟拾取/命令），
	// 并发送一个数据包触发装备快照刷新。
	actor.inventory.Set(item.SlotHotbarStart, item.Stack{ItemID: swordID, Count: 1})
	sendPlayerPosition(t, actorConn, 0.5, 0, 0.5, true)
	processBarrier(t, actorConn)
	instance.tick()
	slots = expectEquipmentFor(t, watcherConn, actor.entityID)
	got := slots[protocol.EquipmentMainHand]
	if got.ID != swordID || got.Count != 1 {
		t.Fatalf("主手 = %+v, want 钻石剑 x1", got)
	}

	// 切换到空的快捷栏槽位：主手应变空（count=0 表示清空）。
	sendSetCarriedItem(t, actorConn, 1)
	processBarrier(t, actorConn)
	instance.tick()
	slots = expectEquipmentFor(t, watcherConn, actor.entityID)
	if got := slots[protocol.EquipmentMainHand]; got.Count != 0 {
		t.Fatalf("切换空槽后主手 = %+v, want 空槽", got)
	}

	// 头盔变化同样同步（护甲槽 36–39 映射到装备槽 2–5）。
	helmetID, err := registry.ItemID("minecraft:diamond_helmet")
	if err != nil {
		t.Fatal(err)
	}
	actor.inventory.Set(item.SlotHead, item.Stack{ItemID: helmetID, Count: 1})
	sendPlayerPosition(t, actorConn, 0.5, 0, 0.5, true)
	processBarrier(t, actorConn)
	instance.tick()
	slots = expectEquipmentFor(t, watcherConn, actor.entityID)
	if got := slots[protocol.EquipmentHead]; got.ID != helmetID || got.Count != 1 {
		t.Fatalf("头盔槽 = %+v, want 钻石头盔 x1", got)
	}
	// 同一次广播应包含全部 6 个槽位（空槽也要下发，否则观察者会保留旧物品）。
	if len(slots) != protocol.EquipmentSlotCount {
		t.Fatalf("装备包槽位数 = %d, want %d", len(slots), protocol.EquipmentSlotCount)
	}
}

// TestPlayerEquipmentNoSpuriousBroadcast 验证装备未变化时不会标记待广播
// （变更检测不应每 tick 都发包）。
func TestPlayerEquipmentNoSpuriousBroadcast(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.ViewDistance = 2
	instance, watcherConn := joinServer(t, cfg, "Watcher")
	addr := watcherConn.RemoteAddr().String()

	actorConn := joinAt(t, cfg, addr, "Actor")
	actor := findSession(t, instance, "Actor")
	// 一次屏障确保读循环已按当前物品栏刷新快照；消费掉加入流程留下的待广播标记。
	sendPlayerPosition(t, actorConn, 0.5, 0, 0.5, true)
	processBarrier(t, actorConn)
	instance.tick()

	// 不再发送数据包：连续 Tick 不应产生新的待广播标记（变更检测不重复发包）。
	for i := 0; i < 10; i++ {
		instance.tick()
		actor.stateMu.Lock()
		pending := actor.equipPending
		actor.stateMu.Unlock()
		if pending {
			t.Fatalf("第 %d 轮无变化却标记了待广播", i)
		}
	}
}
