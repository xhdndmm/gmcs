//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 门/活板门/栅栏门开合音效测试（原版 DoorBlock/TrapDoorBlock/FenceGateBlock 的
// level.playSound(... SoundSource.BLOCKS, 1.0F, random*0.1F+0.9F)）。

// decodeSoundEffectID 解析 Sound Effect 包的音效 ID（协议里存的是 ID+1）。
func decodeSoundEffectID(t *testing.T, payload []byte) int32 {
	t.Helper()
	_, n, err := protocol.DecodeVarInt(payload) // 包 ID
	if err != nil {
		t.Fatalf("解析包 ID：%v", err)
	}
	id, _, err := protocol.DecodeVarInt(payload[n:])
	if err != nil {
		t.Fatalf("解析音效 ID：%v", err)
	}
	return id - 1
}

// TestOpenableSoundGroups 验证音效集合分组与注册表解析（铁/铜/木/竹/樱花/下界木）。
func TestOpenableSoundGroups(t *testing.T) {
	cases := []struct {
		name string
		want int
	}{
		{"minecraft:oak_door", openableSoundWooden},
		{"minecraft:oak_trapdoor", openableSoundWooden},
		{"minecraft:iron_door", openableSoundIron},
		{"minecraft:iron_trapdoor", openableSoundIron},
		{"minecraft:copper_door", openableSoundCopper},
		{"minecraft:waxed_oxidized_copper_trapdoor", openableSoundCopper},
		{"minecraft:bamboo_door", openableSoundBamboo},
		{"minecraft:cherry_trapdoor", openableSoundCherry},
		{"minecraft:crimson_door", openableSoundNether},
		{"minecraft:warped_trapdoor", openableSoundNether},
	}
	for _, testCase := range cases {
		if got := openableSoundGroupOf(testCase.name); got != testCase.want {
			t.Fatalf("%s 分组 = %d, want %d", testCase.name, got, testCase.want)
		}
	}

	instance := newHopperTestServer(t)
	if instance.soundDoorOpen[openableSoundWooden] == 0 || instance.soundDoorClose[openableSoundWooden] == 0 {
		t.Fatal("木质门音效未解析")
	}
	if instance.soundDoorOpen[openableSoundIron] == 0 || instance.soundTrapdoorOpen[openableSoundIron] == 0 {
		t.Fatal("铁质门/活板门音效未解析")
	}
	if instance.soundDoorOpen[openableSoundCopper] == 0 || instance.soundTrapdoorClose[openableSoundCopper] == 0 {
		t.Fatal("铜质门/活板门音效未解析")
	}
	if instance.soundFenceGateOpen == 0 || instance.soundFenceGateClose == 0 {
		t.Fatal("栅栏门音效未解析")
	}
	// 铁质与木质必须是不同的音效（分组没有退化成同一套）。
	if instance.soundDoorOpen[openableSoundIron] == instance.soundDoorOpen[openableSoundWooden] {
		t.Fatal("铁质门与木质门解析到了同一个音效")
	}
}

// TestTrapdoorTogglePlaysSound 验证右键活板门广播开合音效。
func TestTrapdoorTogglePlaysSound(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "TrapdoorSound")
	defer conn.Close()

	x, groundY, z := farmTestSpot(t, instance)
	w := instance.testWorld()
	closed, ok := registry.PlacementTrapdoors["minecraft:oak_trapdoor"]
	if !ok {
		t.Fatal("缺少活板门放置状态表")
	}
	state := closed[0][0]
	w.SetBlock(x, groundY+1, z, state)

	sendUseItemOn(t, conn, x, groundY+1, z, 1)
	waitFor(t, func() bool { return w.BlockAt(x, groundY+1, z) != state }, "活板门未打开")
	wantOpen := instance.soundTrapdoorOpen[openableSoundWooden]
	if got := decodeSoundEffectID(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSoundEffect)); got != wantOpen {
		t.Fatalf("开活板门音效 = %d, want %d", got, wantOpen)
	}
}

// TestFenceGateTogglePlaysSound 验证右键栅栏门广播开合音效。
func TestFenceGateTogglePlaysSound(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "GateSound")
	defer conn.Close()

	x, groundY, z := farmTestSpot(t, instance)
	w := instance.testWorld()
	closed, ok := registry.PlacementFenceGates["minecraft:oak_fence_gate"]
	if !ok {
		t.Fatal("缺少栅栏门放置状态表")
	}
	state := closed[0][0]
	w.SetBlock(x, groundY+1, z, state)

	sendUseItemOn(t, conn, x, groundY+1, z, 1)
	waitFor(t, func() bool { return w.BlockAt(x, groundY+1, z) != state }, "栅栏门未打开")
	if got := decodeSoundEffectID(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSoundEffect)); got != instance.soundFenceGateOpen {
		t.Fatalf("开栅栏门音效 = %d, want %d", got, instance.soundFenceGateOpen)
	}
}

// TestDoorTogglePlaysSound 验证右键门广播对应材质（木质）的开合音效。
func TestDoorTogglePlaysSound(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "DoorSound")
	defer conn.Close()

	x, groundY, z := farmTestSpot(t, instance)
	w := instance.testWorld()
	player := activeSession(t, instance, "DoorSound")
	player.inventory.Set(item.SlotHotbarStart, testStack(t, "minecraft:oak_door", 1))
	_, _, _, yaw, _ := player.playerPosition()
	facing := yawToFacing(yaw)
	// 点击顶面放置 → 铰链恒为 left（原版 getHinge 回退分支）。
	hinge := 0
	facingIndex := horizontalFacingIndex(facing)
	closed := registry.PlacementDoors["minecraft:oak_door"]
	w.SetBlock(x, groundY+1, z, closed[0][facingIndex][hinge])
	w.SetBlock(x, groundY+2, z, closed[1][facingIndex][hinge])

	sendUseItemOn(t, conn, x, groundY+1, z, 1)
	waitFor(t, func() bool {
		return w.BlockAt(x, groundY+1, z) != closed[0][facingIndex][hinge]
	}, "门未打开")
	want := instance.soundDoorOpen[openableSoundWooden]
	if got := decodeSoundEffectID(t, expectPlayPacket(t, conn, protocol.PlayPacketIDSoundEffect)); got != want {
		t.Fatalf("开门音效 = %d, want %d", got, want)
	}
}

// TestRedstoneOpenablePlaysSound 验证红石驱动开合同样广播音效。
func TestRedstoneOpenablePlaysSound(t *testing.T) {
	instance := newHopperTestServer(t)
	x, z := 20, 20
	groundY := hopperTestPad(t, instance, x, z, 3)
	w := instance.testWorld()
	redstone, ok := registry.BlockStateIDs["minecraft:redstone_block"]
	if !ok {
		t.Fatal("缺少红石块方块状态")
	}
	closed, ok := registry.PlacementFenceGates["minecraft:oak_fence_gate"]
	if !ok {
		t.Fatal("缺少栅栏门放置状态表")
	}
	pos := [3]int{x, groundY + 1, z}
	w.SetBlock(pos[0], pos[1], pos[2], closed[0][0])
	w.SetBlock(x+1, pos[1], z, redstone)
	instance.broadcastBlockUpdate(world.DimensionOverworld, x+1, pos[1], z, int32(redstone))

	// 红石块相邻 → 栅栏门应被打开；没有玩家在线时音效包被丢弃，
	// 因此这里只断言状态变化与音效 ID 已解析（音效广播由上面的 e2e 用例覆盖）。
	instance.refreshOpenable(w, world.DimensionOverworld, pos, closed[0][0])
	powered, ok := registry.PlacementFenceGatesPowered["minecraft:oak_fence_gate"]
	if !ok {
		t.Fatal("缺少栅栏门通电状态表")
	}
	if got := w.BlockAt(pos[0], pos[1], pos[2]); got != powered[0][0] {
		t.Fatalf("红石未打开栅栏门：state=%d want %d", got, powered[0][0])
	}
	if instance.soundFenceGateOpen == 0 {
		t.Fatal("栅栏门音效未解析，红石开合将静默")
	}
}
