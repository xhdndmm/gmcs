package server

import (
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
)

// commandLevels 记录需要权限等级的命令（等级参考原版：/say 与 /gamemode
// 需要等级 2，/kick 需要等级 3）。未列出的命令（/help、/list、/spawn）所有玩家可用。
var commandLevels = map[string]int{
	"say":      2,
	"gamemode": 2,
	"kick":     3,
}

// serverCommands 返回服务器向客户端声明的命令（按玩家权限等级过滤；
// 与原版一致：无权限的命令不出现在命令树中）。
func serverCommands(level int) []protocol.CommandDef {
	commands := []protocol.CommandDef{
		{Name: "help"},
		{Name: "list"},
	}
	if level >= commandLevels["say"] {
		commands = append(commands, protocol.CommandDef{Name: "say", ArgName: "message"})
	}
	if level >= commandLevels["gamemode"] {
		commands = append(commands, protocol.CommandDef{Name: "gamemode", ArgName: "mode", ArgParser: protocol.GameModeParser})
	}
	if level >= commandLevels["kick"] {
		commands = append(commands, protocol.CommandDef{Name: "kick", ArgName: "player"})
	}
	return append(commands, protocol.CommandDef{Name: "spawn"})
}

// opLevel 返回玩家在 ops 名单中的权限等级（0 = 非管理员）。
// 名单项支持 "name"（等价 name:4，同原版 /op）与 "name:level"（1–4）。
func (s *Server) opLevel(name string) int {
	for _, entry := range s.config.Ops {
		entryName, level := splitOpEntry(entry)
		if entryName != "" && strings.EqualFold(entryName, name) {
			return level
		}
	}
	return 0
}

// splitOpEntry 解析 ops 列表项：返回玩家名与权限等级（缺省/无效等级按 4，
// 超出 1–4 时截断）。
func splitOpEntry(entry string) (string, int) {
	trimmed := strings.TrimSpace(entry)
	if index := strings.LastIndex(trimmed, ":"); index >= 0 {
		if parsed, err := strconv.Atoi(strings.TrimSpace(trimmed[index+1:])); err == nil {
			name := strings.TrimSpace(trimmed[:index])
			if name == "" {
				return "", 0
			}
			return name, min(max(parsed, 1), 4)
		}
	}
	return trimmed, 4
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
	if required := commandLevels[name]; required > 0 && s.opLevel(player.name) < required {
		player.tryWrite(protocol.EncodeSystemChat(fmt.Sprintf("你没有权限使用该命令（需要权限等级 %d）", required)))
		return
	}
	switch name {
	case "help":
		player.tryWrite(protocol.EncodeSystemChat(strings.Join([]string{
			"可用命令：",
			"/help — 显示此帮助",
			"/list — 列出在线玩家",
			"/say <消息> — 向所有玩家广播消息（权限等级 ≥2）",
			"/spawn — 传送到出生点",
			"/gamemode <模式> — 切换游戏模式（权限等级 ≥2，survival/creative/adventure/spectator）",
			"/kick <玩家> — 踢出玩家（权限等级 ≥3）",
			"管理员在 gmcs.json 的 ops 中配置，支持 name 或 name:等级（1–4）",
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
	case "kick":
		targetName := strings.TrimSpace(strings.TrimPrefix(commandLine, fields[0]))
		if targetName == "" {
			player.tryWrite(protocol.EncodeSystemChat("用法：/kick <玩家名>"))
			return
		}
		victim := s.findJoinedPlayer(targetName)
		if victim == nil {
			player.tryWrite(protocol.EncodeSystemChat("玩家不在线：" + targetName))
			return
		}
		// 发送断开提示后关闭连接（读循环随即退出并走正常的退出清理）。
		_ = victim.writePacket(protocol.EncodePlayDisconnect("Kicked by an operator"))
		_ = victim.conn.Close()
		s.broadcastPacket(protocol.EncodeSystemChat(victim.name + " was kicked by " + player.name))
		slog.Info("player kicked", "name", victim.name, "by", player.name)
	default:
		player.tryWrite(protocol.EncodeSystemChat("未知命令：" + name + "（输入 /help 查看可用命令）"))
	}
}

// findJoinedPlayer 按玩家名查找已进入世界的会话（大小写不敏感）。
func (s *Server) findJoinedPlayer(name string) *session {
	for _, candidate := range s.playerSnapshot() {
		if candidate.isJoined() && strings.EqualFold(candidate.name, name) {
			return candidate
		}
	}
	return nil
}

// handleTabComplete 处理命令补全请求（Tab 键），返回候选列表。
// 玩家只能看到自己权限等级内的命令与参数候选。
func (s *Server) handleTabComplete(player *session, transactionID int32, text string) {
	matches, start, length := s.commandSuggestions(text, s.opLevel(player.name))
	player.tryWrite(protocol.EncodeTabCompleteResponse(transactionID, int32(start), int32(length), matches))
}

// gameModeNames 是 /gamemode 参数的补全候选（与 config.GameModeID 一致）。
var gameModeNames = []string{"survival", "creative", "adventure", "spectator"}

// commandSuggestions 为补全请求生成候选，并返回替换区间 [start, start+length)。
// 仅处理以 "/" 开头的命令文本；首个词补全命令名（候选带斜杠），
// /gamemode 补全模式参数，/kick 补全在线玩家名。
func (s *Server) commandSuggestions(text string, level int) (matches []string, start, length int) {
	if !strings.HasPrefix(text, "/") {
		return nil, 0, 0
	}
	body := text[1:]
	space := strings.IndexByte(body, ' ')
	if space < 0 {
		// 补全命令名：替换整段文本（含斜杠），候选也带斜杠。
		prefix := strings.ToLower(body)
		for _, command := range serverCommands(level) {
			if strings.HasPrefix(command.Name, prefix) {
				matches = append(matches, "/"+command.Name)
			}
		}
		return matches, 0, len(text)
	}
	commandName := strings.ToLower(body[:space])
	argument := body[space+1:]
	if commandName == "gamemode" && level >= commandLevels["gamemode"] && !strings.Contains(argument, " ") {
		prefix := strings.ToLower(argument)
		for _, mode := range gameModeNames {
			if strings.HasPrefix(mode, prefix) {
				matches = append(matches, mode)
			}
		}
		return matches, space + 2, len(argument)
	}
	if commandName == "kick" && level >= commandLevels["kick"] && !strings.Contains(argument, " ") {
		prefix := strings.ToLower(argument)
		for _, name := range s.onlineNames() {
			if strings.HasPrefix(strings.ToLower(name), prefix) {
				matches = append(matches, name)
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
