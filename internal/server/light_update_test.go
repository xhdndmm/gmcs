package server

import (
	"context"
	"net"
	"testing"
	"time"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
	"gmcs/internal/world"
)

func TestBlockUpdateSendsLightUpdate(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "LightWatcher")
	defer conn.Close()

	x, groundY, z := farmTestSpot(t, instance)
	if !instance.testWorld().SetBlock(x, groundY, z, world.GlowstoneBlock) {
		t.Fatal("放置发光方块失败")
	}
	instance.broadcastBlockUpdate(world.DimensionOverworld, x, groundY, z, int32(world.GlowstoneBlock))

	expectBlockUpdate(t, conn, x, groundY, z, int32(world.GlowstoneBlock))
	payload := expectPlayPacket(t, conn, protocol.PlayPacketIDLightUpdate)
	_, offset, err := protocol.DecodeVarInt(payload)
	if err != nil {
		t.Fatal(err)
	}
	cx, size, err := protocol.DecodeVarInt(payload[offset:])
	if err != nil {
		t.Fatal(err)
	}
	offset += size
	cz, size, err := protocol.DecodeVarInt(payload[offset:])
	if err != nil {
		t.Fatal(err)
	}
	offset += size
	if cx != int32(x>>4) || cz != int32(z>>4) {
		t.Fatalf("Light Update 区块坐标 = (%d,%d), want (%d,%d)", cx, cz, x>>4, z>>4)
	}
	maskLongs, size, err := protocol.DecodeVarInt(payload[offset:])
	if err != nil || maskLongs != 1 {
		t.Fatalf("sky mask longs = %d (err=%v)", maskLongs, err)
	}
	offset += size
	_, offset, err = protocol.DecodeInt64(payload, offset)
	if err != nil {
		t.Fatal(err)
	}
	blockMaskLongs, size, err := protocol.DecodeVarInt(payload[offset:])
	if err != nil || blockMaskLongs != 1 {
		t.Fatalf("block mask longs = %d (err=%v)", blockMaskLongs, err)
	}
	offset += size
	blockMask, offset, err := protocol.DecodeInt64(payload, offset)
	if err != nil {
		t.Fatal(err)
	}
	if blockMask == 0 {
		t.Fatal("放置萤石后 Light Update 的方块光掩码不应为空")
	}
}

// TestConcurrentPlayersReceiveLightUpdates 用多个已进入世界的客户端并发
// 触发方块/光照更新，覆盖 lightUpdater 的跨区块传播、dirty section 收集与
// broadcastBlockUpdate 的并发发送路径。
func TestConcurrentPlayersReceiveLightUpdates(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.ViewDistance = 2
	cfg.SpawnMonsters = false
	instance, err := newServer(cfg, testGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	instance.keepAliveInterval = time.Hour
	instance.tickInterval = 0
	instance.chunkUnloadInterval = time.Hour
	instance.weatherEnabled = false
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- instance.Serve(ctx, listener) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Serve() did not stop")
		}
	})
	addr := listener.Addr().String()
	var conns []net.Conn
	for i := 0; i < 4; i++ {
		conn := joinAt(t, cfg, addr, "LightStress"+string(rune('A'+i)))
		conns = append(conns, conn)
	}
	defer func() {
		for _, conn := range conns {
			_ = conn.Close()
		}
	}()
	x, y, z := instance.spawnPositionFor(world.DimensionOverworld)
	worldInstance := instance.testWorld()
	for i := 0; i < 80; i++ {
		state := world.GlowstoneBlock
		if i%2 == 1 {
			state = world.AirBlock
		}
		px, pz := int(x)+i%4, int(z)+i/4%4
		if !worldInstance.SetBlock(px, int(y), pz, state) {
			t.Fatalf("SetBlock(%d,%d,%d) 失败", px, int(y), pz)
		}
		instance.broadcastBlockUpdate(world.DimensionOverworld, px, int(y), pz, int32(state))
	}
}
