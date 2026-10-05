package server

import (
	"bytes"
	"compress/zlib"
	"io"
	"strings"
	"testing"

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
	instance := &Server{config: config.Default()}

	// 管理员补全命令名：替换整段文本，候选带斜杠。
	matches, start, length := instance.commandSuggestions("/ga", true)
	if len(matches) != 1 || matches[0] != "/gamemode" || start != 0 || length != 3 {
		t.Fatalf("op /ga = %v [%d,%d)", matches, start, length)
	}
	// 非管理员看不到 /gamemode。
	if matches, _, _ = instance.commandSuggestions("/ga", false); len(matches) != 0 {
		t.Fatalf("non-op /ga = %v, want no matches", matches)
	}
	// 非管理员可以看到公共命令。
	if matches, _, _ = instance.commandSuggestions("/h", false); len(matches) != 1 || matches[0] != "/help" {
		t.Fatalf("non-op /h = %v", matches)
	}
	// 参数补全：/gamemode 的模式名。
	matches, start, length = instance.commandSuggestions("/gamemode c", true)
	if len(matches) != 1 || matches[0] != "creative" || start != 10 || length != 1 {
		t.Fatalf("gamemode c = %v [%d,%d)", matches, start, length)
	}
	matches, start, length = instance.commandSuggestions("/gamemode ", true)
	if len(matches) != 4 || start != 10 || length != 0 {
		t.Fatalf("gamemode all = %v [%d,%d)", matches, start, length)
	}
	// 非管理员没有参数补全；聊天文本不补全。
	if matches, _, _ = instance.commandSuggestions("/gamemode c", false); len(matches) != 0 {
		t.Fatalf("non-op gamemode arg = %v", matches)
	}
	if matches, _, _ = instance.commandSuggestions("hello", true); len(matches) != 0 {
		t.Fatalf("chat text = %v, want no matches", matches)
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
