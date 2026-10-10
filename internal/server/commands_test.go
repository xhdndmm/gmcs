package server

import (
	"bytes"
	"compress/zlib"
	"io"
	"strings"
	"testing"
	"time"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
)

// sessionOutputText 解码会话写到 buffer 的全部数据包为文本。
// 超过压缩阈值的包为 zlib 压缩（[数据长度][zlib 流]），其余为明文。
func sessionOutputText(t *testing.T, data []byte) string {
	t.Helper()
	var text strings.Builder
	for len(data) > 0 {
		frameLen, n, err := protocol.DecodeVarInt(data)
		if err != nil || frameLen < 0 || int(frameLen) > len(data)-n {
			t.Fatalf("bad frame: len=%d err=%v", frameLen, err)
		}
		body := data[n : n+int(frameLen)]
		data = data[n+int(frameLen):]
		// 帧负载：未压缩为 varint(0) + 原包；压缩为 varint(原长度) + zlib 流。
		declared, n2, err := protocol.DecodeVarInt(body)
		if err != nil {
			t.Fatalf("bad data length: %v", err)
		}
		if declared == 0 {
			text.Write(body[n2:])
			continue
		}
		reader, err := zlib.NewReader(bytes.NewReader(body[n2:]))
		if err != nil {
			t.Fatalf("zlib reader: %v", err)
		}
		raw, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil {
			t.Fatalf("zlib read: %v", err)
		}
		text.Write(raw)
	}
	return text.String()
}

// TestHandleCommand 以离线方式覆盖命令分派（不经过网络）。
func TestHandleCommand(t *testing.T) {
	cfg := config.Default()
	cfg.Ops = []string{"Tester"}
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

	if output := run("/help"); !strings.Contains(output, "可用命令") || !strings.Contains(output, "/spawn") {
		t.Fatalf("help output unexpected: %q", output)
	}
	if output := run("/list"); !strings.Contains(output, "Tester") {
		t.Fatalf("list output unexpected: %q", output)
	}
	// 命令名大小写不敏感。
	if output := run("/LIST"); !strings.Contains(output, "Tester") {
		t.Fatalf("uppercase list output unexpected: %q", output)
	}
	if output := run("/say hello world"); !strings.Contains(output, "[Server] hello world") {
		t.Fatalf("say output unexpected: %q", output)
	}
	if output := run("/say"); !strings.Contains(output, "用法") {
		t.Fatalf("say without argument should show usage: %q", output)
	}
	if output := run("/doesnotexist"); !strings.Contains(output, "未知命令") {
		t.Fatalf("unknown command output unexpected: %q", output)
	}
	if output := run("/spawn"); output == "" {
		t.Fatal("spawn should send a teleport packet")
	}
	if player.teleportID != 1 {
		t.Fatalf("teleportID = %d, want 1", player.teleportID)
	}
	if output := run("/"); output != "" {
		t.Fatalf("bare slash should be ignored, got %q", output)
	}

	// 非管理员不能使用管理命令。
	guest := &session{server: instance, writer: &buffer, name: "Guest"}
	buffer.Reset()
	instance.handleCommand(guest, "/gamemode creative")
	if !strings.Contains(sessionOutputText(t, buffer.Bytes()), "你没有权限") {
		t.Fatalf("guest /gamemode should be denied: %q", buffer.Bytes())
	}
	buffer.Reset()
	instance.handleCommand(guest, "/say hi")
	if !strings.Contains(sessionOutputText(t, buffer.Bytes()), "你没有权限") {
		t.Fatalf("guest /say should be denied: %q", buffer.Bytes())
	}
}

// TestCommandSuggestions 验证补全候选、替换区间与权限过滤（离线）。
func TestCommandSuggestions(t *testing.T) {
	cfg := config.Default()
	cfg.Ops = []string{"Op"}
	instance := &Server{config: cfg, permissionIndex: buildPermissionIndex(cfg)}
	op := &session{name: "Op"}
	guest := &session{name: "Guest"}

	// 管理员补全命令名：替换整段文本，候选带斜杠。
	matches, start, length := instance.commandSuggestions(op, "/ga")
	if len(matches) != 1 || matches[0] != "/gamemode" || start != 0 || length != 3 {
		t.Fatalf("op /ga = %v [%d,%d)", matches, start, length)
	}
	// 非管理员看不到 /gamemode。
	if matches, _, _ = instance.commandSuggestions(guest, "/ga"); len(matches) != 0 {
		t.Fatalf("non-op /ga = %v, want no matches", matches)
	}
	// 非管理员可以看到公共命令。
	if matches, _, _ = instance.commandSuggestions(guest, "/h"); len(matches) != 1 || matches[0] != "/help" {
		t.Fatalf("non-op /h = %v", matches)
	}
	// 参数补全：/gamemode 的模式名。
	matches, start, length = instance.commandSuggestions(op, "/gamemode c")
	if len(matches) != 1 || matches[0] != "creative" || start != 10 || length != 1 {
		t.Fatalf("gamemode c = %v [%d,%d)", matches, start, length)
	}
	matches, start, length = instance.commandSuggestions(op, "/gamemode ")
	if len(matches) != 4 || start != 10 || length != 0 {
		t.Fatalf("gamemode all = %v [%d,%d)", matches, start, length)
	}
	// 非管理员没有参数补全；聊天文本按玩家名补全（无在线玩家时为空）。
	if matches, _, _ = instance.commandSuggestions(guest, "/gamemode c"); len(matches) != 0 {
		t.Fatalf("non-op gamemode arg = %v", matches)
	}
	if matches, _, _ = instance.commandSuggestions(op, "hello"); len(matches) != 0 {
		t.Fatalf("chat text = %v, want no matches", matches)
	}
	// 聊天补全：提及玩家名（非命令文本）补全在线玩家。
	instance.players = make(map[[16]byte]*session)
	instance.players[[16]byte{1}] = &session{name: "Alice"}
	if matches, start, length = instance.commandSuggestions(op, "hi Al"); len(matches) != 1 ||
		matches[0] != "Alice" || start != 3 || length != 2 {
		t.Fatalf("chat mention = %v [%d,%d), want [Alice] [3,5)", matches, start, length)
	}
	// 参数补全：/kick 的在线玩家名（需要等级 3）。
	if matches, _, _ = instance.commandSuggestions(op, "/kick Al"); len(matches) != 1 || matches[0] != "Alice" {
		t.Fatalf("kick Al = %v", matches)
	}
	level2 := &session{name: "Level2"}
	cfg.Ops = []string{"Level2:2"}
	instance = &Server{config: cfg, permissionIndex: buildPermissionIndex(cfg)}
	instance.players = make(map[[16]byte]*session)
	instance.players[[16]byte{1}] = &session{name: "Alice"}
	if matches, _, _ = instance.commandSuggestions(level2, "/kick Al"); len(matches) != 0 {
		t.Fatalf("level 2 kick arg = %v", matches)
	}
}

// TestNodePermissions 验证细粒度权限节点：
//   - 拥有节点即可使用对应命令（无需管理员等级）；
//   - 前缀通配 gmcs.command.* 覆盖全部命令，* 覆盖一切；
//   - "*" 玩家条目对所有玩家生效；
//   - 无节点且无等级时命令被拒绝，且不出现在命令树中。
func TestNodePermissions(t *testing.T) {
	cfg := config.Default()
	cfg.Permissions = map[string]config.StringList{
		"Alice": {"gmcs.command.gamemode"}, // 仅授予 /gamemode
		"Bob":   {"gmcs.command.*"},        // 全部命令
		"*":     {"gmcs.command.spawn"},    // 所有玩家
	}
	instance := &Server{config: cfg, permissionIndex: buildPermissionIndex(cfg)}
	instance.players = make(map[[16]byte]*session)

	names := func(player *session) string {
		list := make([]string, 0, 6)
		for _, command := range instance.serverCommands(player) {
			list = append(list, command.Name)
		}
		return strings.Join(list, ",")
	}
	if got := names(&session{name: "Alice"}); got != "help,list,spawn,gamemode" {
		t.Fatalf("Alice commands = %q", got)
	}
	if got := names(&session{name: "Bob"}); got != "help,list,spawn,seed,say,gamemode,tp,give,time,xp,clear,kick,kill,dimension,save-all" {
		t.Fatalf("Bob commands = %q", got)
	}
	if got := names(&session{name: "Guest"}); got != "help,list,spawn" {
		t.Fatalf("Guest commands = %q", got)
	}
	if got := names(&session{name: "alice"}); got != "help,list,spawn,gamemode" {
		t.Fatalf("alice commands = %q", got)
	}

	// 执行路径同样按节点放行。
	var buffer bytes.Buffer
	alice := &session{server: instance, writer: &buffer, name: "Alice"}
	instance.players[alice.uuid] = alice
	instance.handleCommand(alice, "/gamemode creative")
	if output := sessionOutputText(t, buffer.Bytes()); strings.Contains(output, "没有权限") {
		t.Fatalf("Alice should be allowed via node: %q", output)
	}
	if alice.gameModeID() != uint8(config.GameModeCreative) {
		t.Fatalf("Alice game mode = %d, want creative", alice.gameModeID())
	}
	buffer.Reset()
	instance.handleCommand(alice, "/say hi")
	if output := sessionOutputText(t, buffer.Bytes()); !strings.Contains(output, "没有权限") {
		t.Fatalf("Alice should be denied for /say: %q", output)
	}
}

// TestOpLevels 验证 ops 名单的权限等级解析与命令树过滤（离线）。
func TestOpLevels(t *testing.T) {
	cfg := config.Default()
	cfg.Ops = []string{"Alice", "Bob:2", "Carol:3", "Dave:9", "Eve:0", "  Frank : 1 "}
	instance := &Server{config: cfg, permissionIndex: buildPermissionIndex(cfg)}

	cases := []struct {
		name string
		want int
	}{
		{"Alice", 4}, // 无等级后缀：与原版 /op 一致（4）。
		{"alice", 4}, // 大小写不敏感。
		{"Bob", 2},
		{"Carol", 3},
		{"Dave", 4},  // 超出 4：截断为 4。
		{"Eve", 1},   // 低于 1：截断为 1。
		{"Frank", 1}, // 允许空白。
		{"Guest", 0},
	}
	for _, c := range cases {
		if got := instance.opLevel(c.name); got != c.want {
			t.Fatalf("opLevel(%q) = %d, want %d", c.name, got, c.want)
		}
	}

	commandNames := func(name string) []string {
		player := &session{name: name}
		names := make([]string, 0, 6)
		for _, command := range instance.serverCommands(player) {
			names = append(names, command.Name)
		}
		return names
	}
	join := strings.Join
	if got := join(commandNames("Guest"), ","); got != "help,list,spawn" {
		t.Fatalf("level 0 commands = %q", got)
	}
	if got := join(commandNames("Frank"), ","); got != "help,list,spawn" {
		t.Fatalf("level 1 commands = %q", got)
	}
	if got := join(commandNames("Bob"), ","); got != "help,list,spawn,seed,say,gamemode,tp,give,time,xp,clear" {
		t.Fatalf("level 2 commands = %q", got)
	}
	if got := join(commandNames("Carol"), ","); got != "help,list,spawn,seed,say,gamemode,tp,give,time,xp,clear,kick,kill,dimension,save-all" {
		t.Fatalf("level 3 commands = %q", got)
	}
}

// TestKickPlayer 验证 /kick：权限校验、断开提示与其他玩家收到的广播（经网络）。
func TestKickPlayer(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.ViewDistance = 2
	cfg.Ops = []string{"Admin"}
	instance, adminConn := joinServer(t, cfg, "Admin")
	addr := adminConn.RemoteAddr().String()
	guestConn := joinAt(t, cfg, addr, "Guest", 1)

	// 无权限的玩家不能用 /kick。
	packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDChatCommand)
	packet = appendTestString(packet, "/kick Admin")
	if err := protocol.WritePacketWithCompression(guestConn, packet, compressionThreshold); err != nil {
		t.Fatal(err)
	}
	expectSystemChat(t, guestConn, "需要权限等级 3")
	if instance.findJoinedPlayer("Guest") == nil {
		t.Fatal("Guest 不应被踢出")
	}

	// 管理员踢出 Guest：Guest 收到断开提示，Admin 收到广播。
	packet = protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDChatCommand)
	packet = appendTestString(packet, "/kick Guest")
	if err := protocol.WritePacketWithCompression(adminConn, packet, compressionThreshold); err != nil {
		t.Fatal(err)
	}
	expectSystemChat(t, adminConn, "was kicked by Admin")
	expectPlayPacket(t, guestConn, protocol.PlayPacketIDDisconnect)

	// 踢出后 Guest 会话应被注销。
	deadline := time.Now().Add(5 * time.Second)
	for instance.findJoinedPlayer("Guest") != nil {
		if time.Now().After(deadline) {
			t.Fatal("Guest 未被踢出")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestTabCompleteNetwork 验证完整的补全请求/响应流程（经网络）。
func TestTabCompleteNetwork(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.Ops = []string{"Admin"}
	_, conn := joinServer(t, cfg, "Admin")

	packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDTabComplete)
	packet = protocol.AppendVarInt(packet, 42)
	packet = appendTestString(packet, "/ga")
	if err := protocol.WritePacketWithCompression(conn, packet, compressionThreshold); err != nil {
		t.Fatal(err)
	}
	response := expectPlayPacket(t, conn, protocol.PlayPacketIDTabComplete)
	if !bytes.Contains(response, []byte("/gamemode")) {
		t.Fatalf("response missing /gamemode: %x", response)
	}
	_, offset, err := protocol.DecodeVarInt(response)
	if err != nil {
		t.Fatal(err)
	}
	transactionID, _, err := protocol.DecodeVarInt(response[offset:])
	if err != nil || transactionID != 42 {
		t.Fatalf("transactionId = %d (err=%v), want 42", transactionID, err)
	}
}
