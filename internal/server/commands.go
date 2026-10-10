package server

import (
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"strings"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
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
	{Name: "spawn"},
	{Name: "seed", Level: 2, Node: "gmcs.command.seed"},
	{Name: "say", ArgName: "message", Level: 2, Node: "gmcs.command.say"},
	{Name: "gamemode", ArgName: "mode", ArgParser: protocol.GameModeParser, Level: 2, Node: "gmcs.command.gamemode"},
	{Name: "tp", ArgName: "target", Level: 2, Node: "gmcs.command.tp"},
	{Name: "give", ArgName: "item", Level: 2, Node: "gmcs.command.give"},
	{Name: "time", ArgName: "value", Level: 2, Node: "gmcs.command.time"},
	{Name: "xp", ArgName: "amount", Level: 2, Node: "gmcs.command.xp"},
	{Name: "clear", ArgName: "target", Level: 2, Node: "gmcs.command.clear"},
	{Name: "kick", ArgName: "player", Level: 3, Node: "gmcs.command.kick"},
	{Name: "kill", ArgName: "target", Level: 3, Node: "gmcs.command.kill"},
	{Name: "dimension", ArgName: "name", Level: 3, Node: "gmcs.command.dimension"},
	{Name: "save-all", Level: 3, Node: "gmcs.command.save-all"},
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
	s.auditLog("command", "player", player.name, "command", commandLine)
	switch name {
	case "help":
		player.tryWrite(protocol.EncodeSystemChat(s.helpText(player)))
	case "list":
		names := s.onlineNames()
		player.tryWrite(protocol.EncodeSystemChat(fmt.Sprintf(
			"当前有 %d/%d 名玩家在线：%s", len(names), s.config.MaxPlayers, strings.Join(names, ", "))))
	case "seed":
		player.tryWrite(protocol.EncodeSystemChat(fmt.Sprintf("世界种子：%d", s.config.WorldSeed)))
	case "say":
		message := commandArgument(commandLine, fields)
		if message == "" {
			player.tryWrite(protocol.EncodeSystemChat("用法：/say <消息>"))
			return
		}
		s.broadcastPacket(protocol.EncodeSystemChat("[Server] " + message))
		slog.Info("server say", "name", player.name, "message", message)
	case "spawn":
		player.teleportToSpawn()
	case "gamemode":
		argument := strings.ToLower(commandArgument(commandLine, fields))
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
	case "tp":
		s.handleTeleportCommand(player, commandArgument(commandLine, fields))
	case "give":
		s.handleGiveCommand(player, commandArgument(commandLine, fields))
	case "time":
		s.handleTimeCommand(player, commandArgument(commandLine, fields))
	case "xp":
		s.handleXPCommand(player, commandArgument(commandLine, fields))
	case "clear":
		s.handleClearCommand(player, commandArgument(commandLine, fields))
	case "kill":
		s.handleKillCommand(player, commandArgument(commandLine, fields))
	case "dimension":
		s.handleDimensionCommand(player, commandArgument(commandLine, fields))
	case "save-all":
		if err := s.saveAllWorlds(); err != nil {
			player.tryWrite(protocol.EncodeSystemChat("保存失败：" + err.Error()))
			return
		}
		player.tryWrite(protocol.EncodeSystemChat("已保存全部世界。"))
		slog.Info("worlds saved", "by", player.name)
	case "kick":
		targetName := commandArgument(commandLine, fields)
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
		s.auditLog("kick", "player", victim.name, "by", player.name)
	}
}

// commandArgument 返回命令文本中第一个空格之后的全部内容（去掉首尾空白）。
func commandArgument(commandLine string, fields []string) string {
	if len(fields) == 0 {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(commandLine), fields[0]))
}

// handleTeleportCommand 实现 /tp：
//
//	/tp <x> <y> <z>       传送到坐标（当前维度）
//	/tp <玩家>            传送到该玩家所在位置与维度
func (s *Server) handleTeleportCommand(player *session, argument string) {
	if argument == "" {
		player.tryWrite(protocol.EncodeSystemChat("用法：/tp <x> <y> <z> 或 /tp <玩家>"))
		return
	}
	fields := strings.Fields(argument)
	if len(fields) == 3 {
		x, errX := strconv.ParseFloat(fields[0], 64)
		y, errY := strconv.ParseFloat(fields[1], 64)
		z, errZ := strconv.ParseFloat(fields[2], 64)
		if errX != nil || errY != nil || errZ != nil || math.IsNaN(x) || math.IsNaN(y) || math.IsNaN(z) {
			player.tryWrite(protocol.EncodeSystemChat("坐标无效：/tp <x> <y> <z>"))
			return
		}
		dim := player.dimensionID()
		if !s.insideBorder(x, z) {
			x = math.Max(-s.borderHalfSize, math.Min(s.borderHalfSize, x))
			z = math.Max(-s.borderHalfSize, math.Min(s.borderHalfSize, z))
		}
		y = math.Max(float64(world.WorldMinY), math.Min(float64(world.WorldMinY+world.WorldHeight-2), y))
		if err := player.teleportTo(dim, x, y, z, 0, 0); err != nil {
			player.tryWrite(protocol.EncodeSystemChat("传送失败。"))
		}
		return
	}
	if len(fields) == 1 {
		target := s.findJoinedPlayer(fields[0])
		if target == nil {
			player.tryWrite(protocol.EncodeSystemChat("玩家不在线：" + fields[0]))
			return
		}
		tx, ty, tz, yaw, pitch := target.playerPosition()
		if err := player.teleportTo(target.dimensionID(), tx, ty, tz, yaw, pitch); err != nil {
			player.tryWrite(protocol.EncodeSystemChat("传送失败。"))
		}
		return
	}
	player.tryWrite(protocol.EncodeSystemChat("用法：/tp <x> <y> <z> 或 /tp <玩家>"))
}

// resolveItemName 解析命令中的物品名：接受带前缀（minecraft:stone）或
// 不带前缀（stone）的写法；返回注册表物品 ID。
func resolveItemName(name string) (int32, string, bool) {
	trimmed := strings.TrimSpace(strings.ToLower(name))
	if trimmed == "" {
		return 0, "", false
	}
	candidates := []string{trimmed}
	if !strings.Contains(trimmed, ":") {
		candidates = append(candidates, "minecraft:"+trimmed)
	}
	for _, candidate := range candidates {
		if id, err := registry.ItemID(candidate); err == nil {
			return id, candidate, true
		}
	}
	return 0, "", false
}

// handleGiveCommand 实现 /give <物品> [数量]：把物品放入物品栏，
// 放不下的部分掉落在脚下。
func (s *Server) handleGiveCommand(player *session, argument string) {
	fields := strings.Fields(argument)
	if len(fields) == 0 || len(fields) > 2 {
		player.tryWrite(protocol.EncodeSystemChat("用法：/give <物品> [数量]"))
		return
	}
	count := int32(1)
	if len(fields) == 2 {
		parsed, err := strconv.ParseInt(fields[1], 10, 32)
		if err != nil || parsed < 1 || parsed > 6400 {
			player.tryWrite(protocol.EncodeSystemChat("数量无效（1–6400）。"))
			return
		}
		count = int32(parsed)
	}
	_, name, ok := resolveItemName(fields[0])
	if !ok {
		player.tryWrite(protocol.EncodeSystemChat("未知物品：" + fields[0]))
		return
	}
	stack, err := item.FromName(name, count)
	if err != nil {
		player.tryWrite(protocol.EncodeSystemChat("无法创建物品：" + err.Error()))
		return
	}
	remaining, changed := player.inventory.Add(stack)
	for _, slot := range changed {
		player.tryWrite(protocol.EncodeSetPlayerInventory(int32(slot), player.inventory.Get(slot).AppendSlot(nil)))
	}
	if remaining > 0 {
		x, y, z, _, _ := player.playerPosition()
		stack.Count = remaining
		s.spawnItem(player.dimensionID(), stack, x, y+0.5, z, 0, 0, 0, itemPickupDelayPlayer)
	}
}

// handleTimeCommand 实现 /time set <day|night|noon|midnight|<tick>>、
// /time add <tick> 与 /time query。
func (s *Server) handleTimeCommand(player *session, argument string) {
	fields := strings.Fields(strings.ToLower(argument))
	if len(fields) == 0 {
		player.tryWrite(protocol.EncodeSystemChat("用法：/time set <day|night|noon|midnight|tick> | /time add <tick> | /time query"))
		return
	}
	switch fields[0] {
	case "query":
		dayTime := s.worldAge.Load() % worldDayLength
		player.tryWrite(protocol.EncodeSystemChat(fmt.Sprintf(
			"世界时间：%d tick（%d 天 %02d:%02d）", dayTime, s.worldAge.Load()/worldDayLength, dayTime/1000, (dayTime%1000)*60/1000)))
	case "set":
		if len(fields) != 2 {
			player.tryWrite(protocol.EncodeSystemChat("用法：/time set <day|night|noon|midnight|tick>"))
			return
		}
		var target int64
		switch fields[1] {
		case "day":
			target = 1000
		case "noon":
			target = 6000
		case "night":
			target = 13000
		case "midnight":
			target = 18000
		default:
			parsed, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil || parsed < 0 {
				player.tryWrite(protocol.EncodeSystemChat("时间值无效。"))
				return
			}
			target = parsed % worldDayLength
		}
		s.setWorldTime(target)
		s.broadcastTime()
	case "add":
		if len(fields) != 2 {
			player.tryWrite(protocol.EncodeSystemChat("用法：/time add <tick>"))
			return
		}
		parsed, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || parsed == 0 {
			player.tryWrite(protocol.EncodeSystemChat("时间值无效。"))
			return
		}
		s.worldAge.Add(parsed)
		s.broadcastTime()
	default:
		player.tryWrite(protocol.EncodeSystemChat("用法：/time set … | /time add <tick> | /time query"))
	}
}

// handleXPCommand 实现 /xp add <数量>：增加经验值。
func (s *Server) handleXPCommand(player *session, argument string) {
	fields := strings.Fields(strings.ToLower(argument))
	if len(fields) != 2 || fields[0] != "add" {
		player.tryWrite(protocol.EncodeSystemChat("用法：/xp add <数量>"))
		return
	}
	amount, err := strconv.ParseInt(fields[1], 10, 32)
	if err != nil || amount <= 0 || amount > 1000000 {
		player.tryWrite(protocol.EncodeSystemChat("数量无效（1–1000000）。"))
		return
	}
	bar, level, total := player.addExperience(int32(amount))
	player.tryWrite(protocol.EncodeSetExperience(bar, level, total))
}

// handleClearCommand 实现 /clear：清空玩家物品栏（含光标）。
func (s *Server) handleClearCommand(player *session, argument string) {
	target := player
	if argument != "" {
		target = s.findJoinedPlayer(argument)
		if target == nil {
			player.tryWrite(protocol.EncodeSystemChat("玩家不在线：" + argument))
			return
		}
	}
	for slot := 0; slot < item.InventorySlots; slot++ {
		if target.inventory.Get(slot).IsEmpty() {
			continue
		}
		target.inventory.Set(slot, item.Empty())
		target.tryWrite(protocol.EncodeSetPlayerInventory(int32(slot), item.Empty().AppendSlot(nil)))
	}
	target.setCursor(item.Empty())
	target.tryWrite(protocol.EncodeSystemChat("物品栏已清空。"))
}

// handleKillCommand 实现 /kill [玩家]：对目标造成致命伤害（应用正常死亡流程）。
func (s *Server) handleKillCommand(player *session, argument string) {
	target := player
	if argument != "" {
		target = s.findJoinedPlayer(argument)
		if target == nil {
			player.tryWrite(protocol.EncodeSystemChat("玩家不在线：" + argument))
			return
		}
	}
	if target.isDead() {
		player.tryWrite(protocol.EncodeSystemChat(target.name + " 已经死亡。"))
		return
	}
	position := [3]float64{}
	x, y, z, _, _ := target.playerPosition()
	position = [3]float64{x, y, z}
	if s.damagePlayer(target, 1e6, "an operator", 0, s.mobAttackDamageTypeID, &position) {
		slog.Info("player killed by command", "name", target.name, "by", player.name)
	}
}

// handleDimensionCommand 实现 /dimension <overworld|the_nether|the_end>：
// 传送到目标维度的出生点（管理员命令；下界传送门见 portals.go）。
func (s *Server) handleDimensionCommand(player *session, argument string) {
	dim, ok := world.ParseDimension(strings.TrimSpace(argument))
	if !ok {
		player.tryWrite(protocol.EncodeSystemChat("未知维度（可用：overworld、the_nether、the_end）"))
		return
	}
	x, y, z := s.spawnPositionFor(dim)
	if err := player.teleportTo(dim, x, y, z, 0, 0); err != nil {
		player.tryWrite(protocol.EncodeSystemChat("传送失败。"))
	}
}

// setWorldTime 把世界时间调整为指定时刻（保留天数）。
func (s *Server) setWorldTime(dayTime int64) {
	age := s.worldAge.Load()
	s.worldAge.Store(age - age%worldDayLength + dayTime)
}

// broadcastTime 立即把世界时间同步给主世界玩家（时间命令使用）。
func (s *Server) broadcastTime() {
	packet := protocol.EncodeUpdateTime(s.worldAge.Load(), s.worldAge.Load()%worldDayLength, true)
	for _, other := range s.playerSnapshot() {
		if other.isJoined() && other.dimensionID() == world.DimensionOverworld {
			other.tryWrite(packet)
		}
	}
}

// saveAllWorlds 保存全部已加载维度世界。
func (s *Server) saveAllWorlds() error {
	s.worldMu.Lock()
	worlds := make([]*world.World, 0, len(s.worlds))
	for _, gameWorld := range s.worlds {
		worlds = append(worlds, gameWorld)
	}
	s.worldMu.Unlock()
	var firstErr error
	for _, gameWorld := range worlds {
		if err := gameWorld.Flush(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
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
	descriptions := map[string]string{
		"help":      "/help — 显示此帮助",
		"list":      "/list — 列出在线玩家",
		"spawn":     "/spawn — 传送到出生点",
		"seed":      "/seed — 显示世界种子",
		"say":       "/say <消息> — 向所有玩家广播消息",
		"gamemode":  "/gamemode <模式> — 切换游戏模式（survival/creative/adventure/spectator）",
		"tp":        "/tp <x> <y> <z> 或 /tp <玩家> — 传送",
		"give":      "/give <物品> [数量] — 给予物品",
		"time":      "/time set|add|query — 查看/修改世界时间",
		"xp":        "/xp add <数量> — 增加经验值",
		"clear":     "/clear [玩家] — 清空物品栏",
		"kick":      "/kick <玩家> — 踢出玩家",
		"kill":      "/kill [玩家] — 杀死玩家",
		"dimension": "/dimension <overworld|the_nether|the_end> — 切换维度",
		"save-all":  "/save-all — 立即保存全部世界",
	}
	lines := []string{"可用命令："}
	for _, command := range commands {
		if !s.canUseCommand(player, command) {
			continue
		}
		if description, ok := descriptions[command.Name]; ok {
			lines = append(lines, description)
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
	case "kick", "tp", "clear", "kill":
		for _, name := range s.onlineNames() {
			if strings.HasPrefix(strings.ToLower(name), prefix) {
				matches = append(matches, name)
			}
		}
		return matches, space + 2, len(argument)
	case "dimension":
		for _, name := range dimensionNames() {
			if strings.HasPrefix(name, prefix) || strings.HasPrefix(strings.TrimPrefix(name, "minecraft:"), prefix) {
				matches = append(matches, strings.TrimPrefix(name, "minecraft:"))
			}
		}
		return matches, space + 2, len(argument)
	case "time":
		for _, word := range []string{"set", "add", "query"} {
			if strings.HasPrefix(word, prefix) {
				matches = append(matches, word)
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

// teleportToSpawn 把玩家传送回其当前维度的出生点。
// 由会话读循环调用，与读循环共享 teleportID。
func (s *session) teleportToSpawn() {
	x, y, z := s.server.spawnPositionFor(s.dimensionID())
	s.teleportID++
	s.setPlayerPosition(x, y, z, 0, 0)
	s.resetFallState()
	packet := protocol.EncodeSynchronizePlayerPosition(s.teleportID, x, y, z, 0, 0, 0, 0, 0)
	if err := s.writePacket(packet); err != nil {
		_ = s.conn.Close()
	}
}
