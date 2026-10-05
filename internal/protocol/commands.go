package protocol

import "fmt"

// Play 阶段的命令包 ID。
const (
	// PlayPacketIDDeclareCommands 是 Commands（clientbound）。
	PlayPacketIDDeclareCommands = 0x10
	// PlayServerboundPacketIDChatCommand 是 Chat Command（无签名）。
	PlayServerboundPacketIDChatCommand = 0x06
	// PlayServerboundPacketIDChatCommandSigned 是 Chat Command（带签名）。
	PlayServerboundPacketIDChatCommandSigned = 0x07
)

// maxChatCommandLength 是命令文本的最大字节数（与 1.21.11 一致）。
const maxChatCommandLength = 32500

// command_node.flags 的位定义（1.21.11：高位到低位为
// unused(2) | allows_restricted | has_custom_suggestions | has_redirect_node |
// has_command | type(2)）。
const (
	commandNodeTypeLiteral  = 0x01
	commandNodeTypeArgument = 0x02
	commandNodeHasCommand   = 0x04
)

// BrigadierStringParser 是 brigadier:string 的 parser ID（1.21.11）。
const BrigadierStringParser = 5

// GameModeParser 是 minecraft:gamemode 的 parser ID（1.21.11）。
const GameModeParser = 42

// StringGreedyPhrase 是 brigadier:string 的 greedy 属性（读入整行剩余内容）。
const StringGreedyPhrase = 2

// CommandDef 定义服务器向客户端声明的一个命令。
type CommandDef struct {
	// Name 是命令名（不含前导斜杠）。
	Name string
	// ArgName 非空时该命令带一个参数（如 /say <message>）。
	ArgName string
	// ArgParser 是参数的 parser ID；0 表示默认的 greedy 字符串
	// （brigadier:string + GREEDY_PHRASE）。
	ArgParser int32
}

// EncodeDeclareCommands 编码 Commands 包：命令树为 root + 每个命令的
// literal 节点（可带一个 greedy 字符串 argument 子节点）。
// 节点编号从 0 开始，0 号固定为 root。
func EncodeDeclareCommands(commands []CommandDef) []byte {
	packet := AppendVarInt(nil, int32(PlayPacketIDDeclareCommands))

	nodeCount := 1
	for _, command := range commands {
		nodeCount++
		if command.ArgName != "" {
			nodeCount++
		}
	}
	packet = AppendVarInt(packet, int32(nodeCount))

	// root 节点：type=0，children 为各命令的 literal 节点。
	children := make([]int32, 0, len(commands))
	next := int32(1)
	for _, command := range commands {
		children = append(children, next)
		next++
		if command.ArgName != "" {
			next++
		}
	}
	packet = append(packet, 0x00) // root flags
	packet = AppendVarInt(packet, int32(len(children)))
	for _, child := range children {
		packet = AppendVarInt(packet, child)
	}

	// 各命令节点，编号与上面的 children 一致。
	index := int32(1)
	for _, command := range commands {
		if command.ArgName == "" {
			// 可执行 literal。
			packet = append(packet, commandNodeTypeLiteral|commandNodeHasCommand)
			packet = AppendVarInt(packet, 0) // 无子节点
			packet = appendString(packet, command.Name)
			index++
			continue
		}
		// literal（不可执行）→ argument 子节点。
		packet = append(packet, commandNodeTypeLiteral)
		packet = AppendVarInt(packet, 1)
		packet = AppendVarInt(packet, index+1)
		packet = appendString(packet, command.Name)
		index++

		// argument 节点：可执行。
		parser := command.ArgParser
		if parser == 0 {
			parser = BrigadierStringParser
		}
		packet = append(packet, commandNodeTypeArgument|commandNodeHasCommand)
		packet = AppendVarInt(packet, 0) // 无子节点
		packet = appendString(packet, command.ArgName)
		packet = AppendVarInt(packet, parser)
		if parser == BrigadierStringParser {
			packet = AppendVarInt(packet, StringGreedyPhrase)
		}
		index++
	}

	// rootIndex。
	return AppendVarInt(packet, 0)
}

// ParseChatCommand 解析 Chat Command（0x06 无签名与 0x07 带签名）包的
// 第一个字段：命令文本（不含前导 "/"）。
func ParseChatCommand(packet []byte) (string, error) {
	packetID, offset, err := DecodeVarInt(packet)
	if err != nil {
		return "", fmt.Errorf("invalid chat command packet")
	}
	if packetID != PlayServerboundPacketIDChatCommand && packetID != PlayServerboundPacketIDChatCommandSigned {
		return "", fmt.Errorf("invalid chat command packet ID %#x", packetID)
	}
	command, _, err := readStringAt(packet, offset)
	if err != nil {
		return "", err
	}
	if len(command) == 0 || len(command) > maxChatCommandLength {
		return "", fmt.Errorf("chat command length %d out of range", len(command))
	}
	return command, nil
}
