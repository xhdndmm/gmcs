package server

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
)

// opCommands 记录需要管理员权限的命令名（对应配置 ops 名单）。
var opCommands = map[string]bool{
	"say":      true,
	"gamemode": true,
}

// serverCommands 返回服务器向客户端声明的命令。
// 非管理员不下发管理命令（与原版一致：无权限的命令不出现在命令树中）。
func serverCommands(isOp bool) []protocol.CommandDef {
	commands := []protocol.CommandDef{
		{Name: "help"},
		{Name: "list"},
	}
	if isOp {
		commands = append(commands,
			protocol.CommandDef{Name: "say", ArgName: "message"},
			protocol.CommandDef{Name: "gamemode", ArgName: "mode", ArgParser: protocol.GameModeParser},
		)
	}
	commands = append(commands, protocol.CommandDef{Name: "spawn"})
	return commands
}

// isOp 判断玩家名是否在配置的管理员名单中（大小写不敏感）。
func (s *Server) isOp(name string) bool {
	for _, op := range s.config.Ops {
		if strings.EqualFold(op, name) {
			return true
		}
	}
	return false
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
	if opCommands[name] && !s.isOp(player.name) {
		player.tryWrite(protocol.EncodeSystemChat("你没有权限使用该命令（需要管理员）"))
		return
	}
	switch name {
	case "help":
		player.tryWrite(protocol.EncodeSystemChat(strings.Join([]string{
			"可用命令：",
			"/help — 显示此帮助",
			"/list — 列出在线玩家",
			"/say <消息> — 向所有玩家广播消息（需要管理员）",
			"/spawn — 传送到出生点",
			"/gamemode <模式> — 切换游戏模式（需要管理员，survival/creative/adventure/spectator）",
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
	case "gamemode":
		argument := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(commandLine, fields[0])))
		if argument == "" {
			player.tryWrite(protocol.EncodeSystemChat("用法：/gamemode <survival|creative|adventure|spectator>"))
			return
		}
		mode, ok := config.GameModeID(argument)
		if !ok {
			player.tryWrite(protocol.EncodeSystemChat("未知游戏模式：" + argument))
			return
		}
		player.setGameMode(uint8(mode))
		// Game Event（reason 3 = 切换游戏模式，值为模式 ID）。
		player.tryWrite(protocol.EncodeGameEvent(3, float32(mode)))
		player.tryWrite(protocol.EncodeSystemChat("已将你的游戏模式设为 " + argument))
		slog.Info("game mode changed", "name", player.name, "mode", argument)
	default:
		player.tryWrite(protocol.EncodeSystemChat("未知命令：" + name + "（输入 /help 查看可用命令）"))
	}
}

// handleTabComplete 处理命令补全请求（Tab 键），返回候选列表。
// 非管理员只能看到自己可用的命令。
func (s *Server) handleTabComplete(player *session, transactionID int32, text string) {
	matches, start, length := s.commandSuggestions(text, s.isOp(player.name))
	player.tryWrite(protocol.EncodeTabCompleteResponse(transactionID, int32(start), int32(length), matches))
}

// gameModeNames 是 /gamemode 参数的补全候选（与 config.GameModeID 一致）。
var gameModeNames = []string{"survival", "creative", "adventure", "spectator"}

// commandSuggestions 为补全请求生成候选，并返回替换区间 [start, start+length)。
// 仅处理以 "/" 开头的命令文本；首个词补全命令名（候选带斜杠），
// /gamemode 补全模式参数。
func (s *Server) commandSuggestions(text string, isOp bool) (matches []string, start, length int) {
	if !strings.HasPrefix(text, "/") {
		return nil, 0, 0
	}
	body := text[1:]
	space := strings.IndexByte(body, ' ')
	if space < 0 {
		// 补全命令名：替换整段文本（含斜杠），候选也带斜杠。
		prefix := strings.ToLower(body)
		for _, command := range serverCommands(isOp) {
			if strings.HasPrefix(command.Name, prefix) {
				matches = append(matches, "/"+command.Name)
			}
		}
		return matches, 0, len(text)
	}
	commandName := strings.ToLower(body[:space])
	argument := body[space+1:]
	if commandName == "gamemode" && isOp && !strings.Contains(argument, " ") {
		prefix := strings.ToLower(argument)
		for _, mode := range gameModeNames {
			if strings.HasPrefix(mode, prefix) {
				matches = append(matches, mode)
			}
		}
		return matches, space + 2, len(argument)
	}
	return nil, 0, 0
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
	x, y, z := s.server.spawnPosition()
	s.teleportID++
	s.setPlayerPosition(x, y, z, 0, 0)
	s.resetFallState()
	packet := protocol.EncodeSynchronizePlayerPosition(s.teleportID, x, y, z, 0, 0, 0, 0, 0)
	if err := s.writePacket(packet); err != nil {
		_ = s.conn.Close()
	}
}
