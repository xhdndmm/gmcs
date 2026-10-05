package server

import (
	"bytes"
	"strings"
	"testing"

	"gmcs/internal/config"
)

// TestHandleCommand 以离线方式覆盖命令分派（不经过网络）。
func TestHandleCommand(t *testing.T) {
	instance := &Server{config: config.Default()}
	instance.players = make(map[[16]byte]*session)

	var buffer bytes.Buffer
	player := &session{server: instance, writer: &buffer, name: "Tester"}
	instance.players[player.uuid] = player

	run := func(command string) string {
		buffer.Reset()
		instance.handleCommand(player, command)
		return buffer.String()
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
}
