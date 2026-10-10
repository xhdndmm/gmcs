package server

import (
	"bytes"
	"strings"
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// testItemID 返回注册表中的物品 ID（测试辅助）。
func testItemID(t *testing.T, name string) int32 {
	t.Helper()
	id, err := registry.ItemID(name)
	if err != nil {
		t.Fatalf("registry.ItemID(%q): %v", name, err)
	}
	return id
}

// TestNewCommandsOffline 以离线方式验证新增命令的参数校验与状态副作用
// （不依赖世界与网络的命令：/seed、/xp、/give、/clear、/time、/save-all）。
func TestNewCommandsOffline(t *testing.T) {
	cfg := config.Default()
	cfg.Ops = []string{"Tester"}
	cfg.WorldSeed = 987654321
	instance := &Server{config: cfg}
	instance.players = make(map[[16]byte]*session)

	var buffer bytes.Buffer
	player := &session{server: instance, writer: &buffer, name: "Tester"}
	instance.players[player.uuid] = player

	run := func(command string) string {
		buffer.Reset()
		instance.handleCommand(player, command)
		return sessionOutputText(t, buffer.Bytes())
	}

	// /seed 输出配置中的世界种子。
	if output := run("/seed"); !strings.Contains(output, "987654321") {
		t.Fatalf("seed output unexpected: %q", output)
	}

	// /xp add：经验值增加并下发经验条包。
	if output := run("/xp add 120"); output == "" {
		t.Fatal("/xp add should send an experience packet")
	}
	if _, _, total := player.experienceStatus(); total != 120 {
		t.Fatalf("experience total = %d, want 120", total)
	}
	if output := run("/xp add abc"); !strings.Contains(output, "数量无效") {
		t.Fatalf("invalid xp amount: %q", output)
	}

	// /give：把物品放入物品栏并同步槽位。
	if output := run("/give stone 5"); output == "" {
		t.Fatal("/give should send an inventory packet")
	}
	stoneID := testItemID(t, "minecraft:stone")
	hasStone := false
	for slot := 0; slot < 36; slot++ {
		stack := player.inventory.Get(slot)
		if stack.ItemID == stoneID && stack.Count == 5 {
			hasStone = true
		}
	}
	if !hasStone {
		t.Fatal("stone×5 not found in inventory after /give")
	}
	if output := run("/give stone 0"); !strings.Contains(output, "数量无效") {
		t.Fatalf("zero count should be rejected: %q", output)
	}
	if output := run("/give not_an_item 1"); !strings.Contains(output, "未知物品") {
		t.Fatalf("unknown item should be rejected: %q", output)
	}

	// /clear：清空物品栏。
	if output := run("/clear"); !strings.Contains(output, "物品栏已清空") {
		t.Fatalf("clear output unexpected: %q", output)
	}
	for slot := 0; slot < 36; slot++ {
		if !player.inventory.Get(slot).IsEmpty() {
			t.Fatalf("slot %d not empty after /clear", slot)
		}
	}

	// /time set night 与 /time add：世界时间状态与查询输出。
	run("/time set night")
	if dayTime := instance.worldAge.Load() % worldDayLength; dayTime != 13000 {
		t.Fatalf("dayTime after /time set night = %d, want 13000", dayTime)
	}
	run("/time add 500")
	if dayTime := instance.worldAge.Load() % worldDayLength; dayTime != 13500 {
		t.Fatalf("dayTime after /time add 500 = %d, want 13500", dayTime)
	}
	if output := run("/time query"); !strings.Contains(output, "13500") {
		t.Fatalf("time query output unexpected: %q", output)
	}
	if output := run("/time set notanumber"); !strings.Contains(output, "时间值无效") {
		t.Fatalf("invalid time value should be rejected: %q", output)
	}

	// /save-all 在没有已加载世界时也成功。
	if output := run("/save-all"); !strings.Contains(output, "已保存全部世界") {
		t.Fatalf("save-all output unexpected: %q", output)
	}

	// /kill 对离线会话（无世界）输出用法外的安全行为：目标是自己且未死亡时执行。
	// 这里只验证命令解析不 panic；完整死亡流程由网络测试覆盖。
	if output := run("/kill Guest"); !strings.Contains(output, "玩家不在线") {
		t.Fatalf("kill unknown player output unexpected: %q", output)
	}
}

// TestNewCommandsNetwork 通过网络验证新增命令的完整行为：
// /tp 坐标、/give、/time set、/xp add、/clear、/kill、/save-all。
func TestNewCommandsNetwork(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.Ops = []string{"Cmdr"}
	instance, conn := joinServer(t, cfg, "Cmdr")
	player := findSession(t, instance, "Cmdr")

	// /tp <x> <y> <z>：传送并下发同步位置包。
	sendChatCommand(t, conn, "/tp 10.5 70 30.5")
	expectPlayPacket(t, conn, protocol.PlayPacketIDSynchronizePlayerPos)
	px, py, pz, _, _ := player.playerPosition()
	if px != 10.5 || py != 70 || pz != 30.5 {
		t.Fatalf("position after /tp = (%v,%v,%v), want (10.5,70,30.5)", px, py, pz)
	}
	if got := player.dimensionID(); got != world.DimensionOverworld {
		t.Fatalf("dimension after /tp = %v, want overworld", got)
	}

	// /give minecraft:diamond 3。
	runGive := func(item string) {
		sendChatCommand(t, conn, "/give "+item)
		expectPlayPacket(t, conn, protocol.PlayPacketIDSetPlayerInventory)
	}
	runGive("minecraft:diamond 3")
	diamondID := testItemID(t, "minecraft:diamond")
	found := false
	for slot := 0; slot < 36; slot++ {
		stack := player.inventory.Get(slot)
		if stack.ItemID == diamondID && stack.Count == 3 {
			found = true
		}
	}
	if !found {
		t.Fatal("diamond×3 not found in inventory after /give")
	}

	// /time set noon：广播时间包，dayTime 归 6000。
	sendChatCommand(t, conn, "/time set noon")
	expectPlayPacket(t, conn, protocol.PlayPacketIDUpdateTime)
	if dayTime := instance.worldAge.Load() % worldDayLength; dayTime != 6000 {
		t.Fatalf("dayTime after /time set noon = %d, want 6000", dayTime)
	}

	// /xp add 30：下发经验条包并累计经验。
	sendChatCommand(t, conn, "/xp add 30")
	expectPlayPacket(t, conn, protocol.PlayPacketIDSetExperience)
	if _, _, total := player.experienceStatus(); total < 30 {
		t.Fatalf("experience total = %d, want >= 30", total)
	}

	// /clear：清空物品栏（包括初始的石头）。
	sendChatCommand(t, conn, "/clear")
	expectSystemChat(t, conn, "物品栏已清空")
	for slot := 0; slot < 36; slot++ {
		if !player.inventory.Get(slot).IsEmpty() {
			t.Fatalf("slot %d not empty after /clear", slot)
		}
	}

	// /save-all：保存全部已加载世界。
	sendChatCommand(t, conn, "/save-all")
	expectSystemChat(t, conn, "已保存全部世界")

	// /kill：目标（自己）进入死亡状态，生命值归零。
	sendChatCommand(t, conn, "/kill")
	expectPlayPacket(t, conn, protocol.PlayPacketIDUpdateHealth)
	if !player.isDead() {
		t.Fatal("player should be dead after /kill")
	}
	if health, _, _ := player.healthStatus(); health != 0 {
		t.Fatalf("health after /kill = %v, want 0", health)
	}
}
