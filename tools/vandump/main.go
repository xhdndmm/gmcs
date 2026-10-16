//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

// vandump 是一个临时的兼容性验证工具：连接原版服务器，完成握手/登录/配置阶段，
// 然后把 Play 阶段收到的每个数据包（ID + 十六进制负载）追加写入日志文件。
// 用途：用官方服务端生成的真实数据包校验 gmcs 的编码（物品组件、Holder 等）。
// 该工具不属于服务器运行时组件。
package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"gmcs/internal/protocol"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:25565", "服务器地址")
	name := flag.String("name", "Dumper", "玩家名")
	out := flag.String("out", "/tmp/vt/dump.hex", "输出文件")
	stopAfter := flag.Int("stop-after", 0, "收到指定包 ID 后停止（-1 表示不停）")
	flag.Parse()

	if err := run(*addr, *name, *out, int32(*stopAfter)); err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		os.Exit(1)
	}
}

func run(addr, name, outPath string, stopAfter int32) error {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	// 握手（协议 774，next state = 2）。
	handshake := protocol.AppendVarInt(nil, 0)
	handshake = protocol.AppendVarInt(handshake, 774)
	handshake = appendString(handshake, addr)
	handshake = append(handshake, 0x63, 0xDD)
	handshake = protocol.AppendVarInt(handshake, 2)
	if err := protocol.WritePacket(conn, handshake); err != nil {
		return err
	}

	// Login Start（离线模式）。
	login := protocol.AppendVarInt(nil, 0)
	login = appendString(login, name)
	uuid := protocol.OfflineUUID(name)
	login = append(login, uuid[:]...)
	if err := protocol.WritePacket(conn, login); err != nil {
		return err
	}

	// Set Compression → 之后都是压缩帧。
	packet, err := protocol.ReadPacket(conn)
	if err != nil {
		return err
	}
	packetID, offset, err := protocol.DecodeVarInt(packet)
	if err != nil || packetID != 0x03 {
		return fmt.Errorf("期望 Set Compression（0x03），收到 %#x (err=%v)", packetID, err)
	}
	threshold, _, err := protocol.DecodeVarInt(packet[offset:])
	if err != nil {
		return err
	}

	// Login Success → Login Acknowledged。
	if _, err := protocol.ReadPacketWithCompression(conn, threshold); err != nil {
		return err
	}
	if err := protocol.WritePacketWithCompression(conn,
		protocol.AppendVarInt(nil, 0x03), threshold); err != nil {
		return err
	}

	// 配置阶段：回复 Known Packs（minecraft:core），Finish Configuration 后确认。
	configured := false
	for !configured {
		packet, err := protocol.ReadPacketWithCompression(conn, threshold)
		if err != nil {
			return fmt.Errorf("配置阶段读取失败：%w", err)
		}
		packetID, _, err := protocol.DecodeVarInt(packet)
		if err != nil {
			return err
		}
		switch packetID {
		case protocol.ConfigPacketIDKnownPacks:
			known := protocol.AppendVarInt(nil, protocol.ConfigServerboundPacketIDKnownPacks)
			known = protocol.AppendVarInt(known, 1)
			known = appendString(known, "minecraft")
			known = appendString(known, "core")
			known = appendString(known, "1.21.11")
			if err := protocol.WritePacketWithCompression(conn, known, threshold); err != nil {
				return err
			}
		case protocol.ConfigPacketIDFinishConfiguration:
			if err := protocol.WritePacketWithCompression(conn,
				protocol.AppendVarInt(nil, protocol.ConfigPacketIDFinishConfiguration), threshold); err != nil {
				return err
			}
			configured = true
		}
	}

	// Play 阶段：全量转储。
	file, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer file.Close()
	fmt.Println("已进入 Play 阶段，开始转储 ->", outPath)

	for {
		packet, err := protocol.ReadPacketWithCompression(conn, threshold)
		if err != nil {
			if err == io.EOF {
				fmt.Println("服务器关闭连接")
				return nil
			}
			return err
		}
		packetID, _, err := protocol.DecodeVarInt(packet)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(file, "%#04x %s\n", packetID, hex.EncodeToString(packet)); err != nil {
			return err
		}
		switch packetID {
		case protocol.PlayPacketIDKeepAlive:
			// 原样回包以维持连接。
			if _, size, err := protocol.DecodeVarInt(packet); err == nil {
				reply := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDKeepAlive)
				reply = append(reply, packet[size:]...)
				if err := protocol.WritePacketWithCompression(conn, reply, threshold); err != nil {
					return err
				}
			}
		case protocol.PlayPacketIDLogin:
			fmt.Println("收到 Login（进入世界）")
		}
		if stopAfter >= 0 && packetID == stopAfter {
			fmt.Printf("收到目标包 %#x，停止转储\n", packetID)
			return nil
		}
	}
}

// appendString 追加一个 VarInt 长度前缀的 UTF-8 字符串。
func appendString(dst []byte, value string) []byte {
	dst = protocol.AppendVarInt(dst, int32(len(value)))
	return append(dst, value...)
}
