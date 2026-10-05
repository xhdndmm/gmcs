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

// 命令系统：命令表（名称、参数、权限等级与权限节点）、权限判定、
// 命令执行与 Tab 补全。
//
// 权限模型：
//   - 每个命令声明一个最低权限等级（0 = 所有玩家）与一个权限节点；
//   - ops 名单中的玩家按等级放行（name 等价 4 级）；
//   - 拥有权限节点（permissions 配置）的玩家同样放行，可用于在不授予
//     管理员等级的前提下开放单个命令；
//   - 命令树与 Tab 补全都按同一判定过滤，执行时再次校验。

// commandSpec 描述一个命令。
type commandSpec struct {
	Name string
	// ArgName/ArgParser 是命令的唯一参数（空表示无参数）。
	ArgName   string
	ArgParser int32
	// Level 是使用命令所需的最低权限等级（0 = 所有玩家）。
	Level int
	// Node 是使用命令所需的权限节点（空表示无节点）。
	Node string
}

// commands 是全部内建命令（顺序即 /help 与命令树顺序）。
var commands = []commandSpec{
	{Name: "help"},
	{Name: "list"},
	{Name: "say", ArgName: "message", Level: 2, Node: "gmcs.command.say"},
	{Name: "gamemode", ArgName: "mode", ArgParser: protocol.GameModeParser, Level: 2, Node: "gmcs.command.gamemode"},
	{Name: "kick", ArgName: "player", Level: 3, Node: "gmcs.command.kick"},
	{Name: "spawn"},
}

// commandByName 按名查找命令。
func commandByName(name string) (commandSpec, bool) {
	for _, command := range commands {
		if command.Name == name {
			return command, true
		}
	}
	return commandSpec{}, false
}

// serverCommands 返回服务器向客户端声明的命令（按玩家权限过滤；
// 与原版一致：无权限的命令不出现在命令树中）。
func (s *Server) serverCommands(player *session) []protocol.CommandDef {
	defs := make([]protocol.CommandDef, 0, len(commands))
	for _, command := range commands {
		if !s.canUseCommand(player, command) {
			continue
		}
		defs = append(defs, protocol.CommandDef{
			Name:      command.Name,
			ArgName:   command.ArgName,
			ArgParser: command.ArgParser,
		})
	}
	return defs
}

// canUseCommand 报告玩家是否可以使用该命令（等级或权限节点任一满足）。
func (s *Server) canUseCommand(player *session, command commandSpec) bool {
	if s.opLevel(player.name) >= command.Level {
		return true
	}
	return s.hasNode(player.name, command.Node)
}

// hasNode 报告玩家是否拥有权限节点（大小写不敏感；支持 "*"、
// "gmcs.command.*" 前缀通配与 "*" 玩家名（所有玩家））。
func (s *Server) hasNode(name, node string) bool {
	if node == "" {
		return false
	}
	for _, granted := range s.permissionIndex[strings.ToLower(name)] {
		if nodeMatches(granted, node) {
			return true
		}
	}
	for _, granted := range s.permissionIndex["*"] {
		if nodeMatches(granted, node) {
			return true
		}
	}
	return false
}

// nodeMatches 报告授权节点 granted 是否覆盖目标节点 node。
func nodeMatches(granted, node string) bool {
	if granted == "*" || granted == node {
		return true
	}
	if prefix, ok := strings.CutSuffix(granted, "*"); ok {
		return strings.HasPrefix(node, prefix)
	}
	return false
}

// buildPermissionIndex 把配置中的权限表转换为小写名索引
// （玩家名统一小写，便于大小写不敏感匹配）。
func buildPermissionIndex(cfg config.Config) map[string][]string {
	index := make(map[string][]string, len(cfg.Permissions)+1)
	for name, nodes := range cfg.Permissions {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" {
			continue
		}
		for _, node := range nodes {
			trimmed := strings.TrimSpace(node)
			if trimmed == "" {
				continue
			}
			index[key] = append(index[key], trimmed)
		}
	}
	return index
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
	command, ok := commandByName(name)
	if !ok {
		player.tryWrite(protocol.EncodeSystemChat("未知命令：" + name + "（输入 /help 查看可用命令）"))
		return
	}
	if !s.canUseCommand(player, command) {
		player.tryWrite(protocol.EncodeSystemChat(permissionDeniedMessage(command)))
		return
	}
	switch name {
	case "help":
		player.tryWrite(protocol.EncodeSystemChat(s.helpText(player)))
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
	}
}

// permissionDeniedMessage 返回权限不足的提示（含等级与可选节点信息）。
func permissionDeniedMessage(command commandSpec) string {
	if command.Node != "" {
		return fmt.Sprintf("你没有权限使用该命令（需要权限等级 %d 或节点 %s）", command.Level, command.Node)
	}
	return fmt.Sprintf("你没有权限使用该命令（需要权限等级 %d）", command.Level)
}

// helpText 生成 /help 的帮助文本（只列出玩家有权限使用的命令）。
func (s *Server) helpText(player *session) string {
	lines := []string{"可用命令："}
	for _, command := range commands {
		if !s.canUseCommand(player, command) {
			continue
		}
		switch command.Name {
		case "help":
			lines = append(lines, "/help — 显示此帮助")
		case "list":
			lines = append(lines, "/list — 列出在线玩家")
		case "say":
			lines = append(lines, "/say <消息> — 向所有玩家广播消息")
		case "spawn":
			lines = append(lines, "/spawn — 传送到出生点")
		case "gamemode":
			lines = append(lines, "/gamemode <模式> — 切换游戏模式（survival/creative/adventure/spectator）")
		case "kick":
			lines = append(lines, "/kick <玩家> — 踢出玩家")
		}
	}
	lines = append(lines, "权限：管理员在 gmcs.json 的 ops 中配置（name 或 name:等级）；",
		"也可在 permissions 中按玩家授予节点（如 \"Alice\": \"gmcs.command.gamemode\"，\"*\" 表示所有玩家）")
	return strings.Join(lines, "\n")
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
// 玩家只能看到自己权限内的命令与参数候选。
func (s *Server) handleTabComplete(player *session, transactionID int32, text string) {
	matches, start, length := s.commandSuggestions(player, text)
	player.tryWrite(protocol.EncodeTabCompleteResponse(transactionID, int32(start), int32(length), matches))
}

// gameModeNames 是 /gamemode 参数的补全候选（与 config.GameModeID 一致）。
var gameModeNames = []string{"survival", "creative", "adventure", "spectator"}

// commandSuggestions 为补全请求生成候选，并返回替换区间 [start, start+length)。
// 以 "/" 开头的文本按命令补全（命令名、/gamemode 参数、/kick 玩家名）；
// 其他文本按聊天补全（玩家名）。
func (s *Server) commandSuggestions(player *session, text string) (matches []string, start, length int) {
	if !strings.HasPrefix(text, "/") {
		return s.chatSuggestions(text)
	}
	body := text[1:]
	space := strings.IndexByte(body, ' ')
	if space < 0 {
		// 补全命令名：替换整段文本（含斜杠），候选也带斜杠。
		prefix := strings.ToLower(body)
		for _, command := range s.serverCommands(player) {
			if strings.HasPrefix(command.Name, prefix) {
				matches = append(matches, "/"+command.Name)
			}
		}
		return matches, 0, len(text)
	}
	commandName := strings.ToLower(body[:space])
	command, ok := commandByName(commandName)
	if !ok || !s.canUseCommand(player, command) {
		return nil, 0, 0
	}
	argument := body[space+1:]
	if strings.Contains(argument, " ") {
		return nil, 0, 0
	}
	prefix := strings.ToLower(argument)
	switch commandName {
	case "gamemode":
		for _, mode := range gameModeNames {
			if strings.HasPrefix(mode, prefix) {
				matches = append(matches, mode)
			}
		}
		return matches, space + 2, len(argument)
	case "kick":
		for _, name := range s.onlineNames() {
			if strings.HasPrefix(strings.ToLower(name), prefix) {
				matches = append(matches, name)
			}
		}
		return matches, space + 2, len(argument)
	}
	return nil, 0, 0
}

// chatSuggestions 为聊天文本（不以 "/" 开头）生成玩家名候选。
// 原版在聊天栏提及玩家时补全在线玩家名（取最后一个词作为前缀）。
func (s *Server) chatSuggestions(text string) (matches []string, start, length int) {
	wordStart := strings.LastIndexByte(text, ' ') + 1
	prefix := strings.ToLower(text[wordStart:])
	if prefix == "" {
		return nil, 0, 0
	}
	for _, name := range s.onlineNames() {
		if strings.HasPrefix(strings.ToLower(name), prefix) {
			matches = append(matches, name)
		}
	}
	return matches, wordStart, len(text) - wordStart
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
