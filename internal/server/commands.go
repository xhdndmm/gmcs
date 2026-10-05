package server

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"gmcs/internal/protocol"
	"gmcs/internal/world"
)

// serverCommands 返回服务器向客户端声明的命令。
func serverCommands() []protocol.CommandDef {
	return []protocol.CommandDef{
		{Name: "help"},
		{Name: "list"},
		{Name: "say", ArgName: "message"},
		{Name: "spawn"},
	}
}

// handleCommand 执行玩家发送的命令。客户端提交的命令字符串带前导 "/"
// （与聊天输入一致），如 "/say hello"。
func (s *Server) handleCommand(player *session, commandLine string) {
	fields := strings.Fields(commandLine)
	if len(fields) == 0 {
		return
	}
	name := strings.ToLower(strings.TrimPrefix(fields[0], "/"))
	if name == "" {
		return
	}
	switch name {
	case "help":
		player.tryWrite(protocol.EncodeSystemChat(strings.Join([]string{
			"可用命令：",
			"/help — 显示此帮助",
			"/list — 列出在线玩家",
			"/say <消息> — 向所有玩家广播消息",
			"/spawn — 传送到出生点",
		}, "\n")))
	case "list":
		names := s.onlineNames()
		player.tryWrite(protocol.EncodeSystemChat(fmt.Sprintf(
			"当前有 %d/%d 名玩家在线：%s", len(names), s.config.MaxPlayers, strings.Join(names, ", "))))
	case "say":
		message := strings.TrimSpace(strings.TrimPrefix(commandLine, fields[0]))
		if message == "" {
			player.tryWrite(protocol.EncodeSystemChat("用法：/say <消息>"))
			return
		}
		s.broadcastPacket(protocol.EncodeSystemChat("[Server] " + message))
		slog.Info("server say", "name", player.name, "message", message)
	case "spawn":
		player.teleportToSpawn()
	default:
		player.tryWrite(protocol.EncodeSystemChat("未知命令：" + name + "（输入 /help 查看可用命令）"))
	}
}

// onlineNames 返回排序后的在线玩家名列表。
func (s *Server) onlineNames() []string {
	s.mu.Lock()
	names := make([]string, 0, len(s.players))
	for _, player := range s.players {
		names = append(names, player.name)
	}
	s.mu.Unlock()
	sort.Strings(names)
	return names
}

// teleportToSpawn 把玩家传送回出生点。
// 由会话读循环调用，与读循环共享 teleportID。
func (s *session) teleportToSpawn() {
	s.teleportID++
	packet := protocol.EncodeSynchronizePlayerPosition(
		s.teleportID, 0.5, float64(world.FlatSpawnY), 0.5, 0, 0, 0, 0, 0)
	if err := s.writePacket(packet); err != nil {
		_ = s.conn.Close()
	}
}
