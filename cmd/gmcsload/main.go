// gmcsload 是 gmcs 的简易负载压测客户端：模拟若干离线模式客户端完成
// 握手 → 登录 → 配置 → 进入世界，随后持续移动并回复 Keep Alive /
// Chunk Batch Received，用于观测服务器在并发连接下的表现。
//
// 说明：
//   - 仅支持离线模式（不实现加密/正版验证）；
//   - 报告的是客户端侧观测值（进入世界耗时、收包量与流量），
//     服务器侧 CPU/内存请配合 `pprof_address` 与系统工具观测；
//   - 单机 loopback 压测不代表真实网络环境。
//
// 用法：
//
//	go run ./cmd/gmcsload -addr 127.0.0.1:25565 -players 50 -duration 20s
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math"
	"net"
	"os"
	"os/signal"
	"sort"
	"sync/atomic"
	"syscall"
	"time"

	"gmcs/internal/protocol"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:25565", "服务器地址")
	players := flag.Int("players", 10, "并发客户端数")
	duration := flag.Duration("duration", 20*time.Second, "压测持续时间")
	ramp := flag.Duration("ramp", 50*time.Millisecond, "客户端启动间隔")
	prefix := flag.String("prefix", "LoadBot", "客户端用户名前缀")
	protocolVersion := flag.Int("protocol", 774, "协议版本（握手用）")
	moveInterval := flag.Duration("move-interval", 50*time.Millisecond, "移动包发送间隔（0 表示静止）")
	verbose := flag.Bool("verbose", false, "打印每个客户端的分阶段耗时")
	flag.Parse()

	if *players < 1 {
		fmt.Fprintln(os.Stderr, "players 必须为正数")
		os.Exit(2)
	}
	if *duration <= 0 {
		fmt.Fprintln(os.Stderr, "duration 必须为正数")
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *duration)
	defer cancel()

	results := make([]clientResult, *players)
	var started int64
	done := make(chan int, *players)
	start := time.Now()

	for i := 0; i < *players; i++ {
		cfg := clientConfig{
			addr:            *addr,
			name:            fmt.Sprintf("%s%d", *prefix, i),
			protocolVersion: int32(*protocolVersion),
			moveInterval:    *moveInterval,
			verbose:         *verbose,
		}
		go func(index int, cfg clientConfig) {
			atomic.AddInt64(&started, 1)
			results[index] = runClient(ctx, cfg)
			done <- index
		}(i, cfg)
		if *ramp > 0 && i < *players-1 {
			select {
			case <-ctx.Done():
			case <-time.After(*ramp):
			}
		}
	}
	for i := 0; i < *players; i++ {
		<-done
	}

	report(results, time.Since(start), *players)
}

// clientConfig 是单个压测客户端的参数。
type clientConfig struct {
	addr            string
	name            string
	protocolVersion int32
	moveInterval    time.Duration
	verbose         bool
}

// clientResult 是单个压测客户端的观测结果。
type clientResult struct {
	name       string
	joinTime   time.Duration // 从连接建立到收到首个 Play 包
	packets    int64
	bytes      int64
	byID       map[int32]int64
	bytesByID  map[int32]int64
	disconnect string
	err        error
}

// readTimeout 是单次读取的限制（每次读取前重置）。
const readTimeout = 30 * time.Second

// corePackVersion 是回复 Known Packs 时声明的原版数据包版本（对应服务器版本）。
const corePackVersion = "1.21.11"

// runClient 完整运行一个模拟客户端直到 ctx 结束（或被服务器断开）。
func runClient(ctx context.Context, cfg clientConfig) clientResult {
	result := clientResult{
		name:      cfg.name,
		byID:      make(map[int32]int64),
		bytesByID: make(map[int32]int64),
	}
	connectStart := time.Now()
	phase := "连接"
	if cfg.verbose {
		defer func() {
			fmt.Fprintf(os.Stderr, "[%s] 结束：阶段=%s 耗时=%s err=%v\n",
				cfg.name, phase, time.Since(connectStart).Round(time.Millisecond), result.err)
		}()
	}
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", cfg.addr)
	if err != nil {
		result.err = err
		return result
	}
	defer conn.Close()

	// 握手（next state = 2 登录）。
	handshake := protocol.AppendVarInt(nil, 0)
	handshake = protocol.AppendVarInt(handshake, cfg.protocolVersion)
	handshake = appendString(handshake, "gmcsload")
	handshake = append(handshake, 0x63, 0xDD)
	handshake = protocol.AppendVarInt(handshake, 2)
	if err := protocol.WritePacket(conn, handshake); err != nil {
		result.err = err
		return result
	}

	// Login Start（离线模式：UUID 由用户名推导）。
	login := protocol.AppendVarInt(nil, 0)
	login = appendString(login, cfg.name)
	uuid := protocol.OfflineUUID(cfg.name)
	login = append(login, uuid[:]...)
	if err := protocol.WritePacket(conn, login); err != nil {
		result.err = err
		return result
	}

	// Set Compression（未压缩）→ 之后的包都是压缩格式。
	phase = "读取 Set Compression"
	packet, err := readPacket(conn)
	if err != nil {
		result.err = err
		return result
	}
	packetID, offset, err := protocol.DecodeVarInt(packet)
	if err != nil || packetID != 0x03 {
		result.err = fmt.Errorf("期望 Set Compression（0x03），收到 %#x (err=%v)", packetID, err)
		return result
	}
	threshold, _, err := protocol.DecodeVarInt(packet[offset:])
	if err != nil {
		result.err = err
		return result
	}

	// Login Success → Login Acknowledged。
	phase = "读取 Login Success"
	if _, err := readPacketWithCompression(conn, threshold); err != nil {
		result.err = err
		return result
	}
	if err := protocol.WritePacketWithCompression(conn,
		protocol.AppendVarInt(nil, 0x03), threshold); err != nil {
		result.err = err
		return result
	}

	// 配置阶段：回复 Known Packs，收到 Finish Configuration（0x03）后确认。
	phase = "配置阶段"
	configured := false
	for !configured {
		packet, err := readPacketWithCompression(conn, threshold)
		if err != nil {
			result.err = err
			return result
		}
		packetID, _, err := protocol.DecodeVarInt(packet)
		if err != nil {
			result.err = err
			return result
		}
		switch packetID {
		case protocol.ConfigPacketIDKnownPacks:
			// 服务器询问客户端已有的数据包：回复原版 core 包（与真实客户端一致，
			// 否则服务器不会下发注册表数据）。
			known := protocol.AppendVarInt(nil, protocol.ConfigServerboundPacketIDKnownPacks)
			known = protocol.AppendVarInt(known, 1)
			known = appendString(known, "minecraft")
			known = appendString(known, "core")
			known = appendString(known, corePackVersion)
			if err := protocol.WritePacketWithCompression(conn, known, threshold); err != nil {
				result.err = err
				return result
			}
		case protocol.ConfigPacketIDFinishConfiguration:
			phase = "确认配置完成"
			if err := protocol.WritePacketWithCompression(conn,
				protocol.AppendVarInt(nil, protocol.ConfigPacketIDFinishConfiguration), threshold); err != nil {
				result.err = err
				return result
			}
			configured = true
		}
	}

	// Play 阶段：读包并做最小响应。
	phase = "Play 阶段"
	var (
		posX, posY, posZ float64
		havePos          bool
		lastMove         time.Time
		delta            = 0.2 // 每移动包的水平位移（4 格/秒，触发区块流式发送）
	)
	for {
		if ctx.Err() != nil {
			return result
		}
		// 短读超时以便定期检查 ctx 并发送移动包。
		if err := conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
			result.err = err
			return result
		}
		packet, err := readPacketWithCompression(conn, threshold)
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				if err := maybeMove(conn, threshold, &posX, &posY, &posZ, &havePos, &lastMove, delta, cfg.moveInterval); err != nil {
					result.err = err
					return result
				}
				continue
			}
			if ctx.Err() != nil {
				return result
			}
			result.err = err
			return result
		}
		result.packets++
		result.bytes += int64(len(packet))
		packetID, offset, err := protocol.DecodeVarInt(packet)
		if err != nil {
			result.err = err
			return result
		}
		result.byID[packetID]++
		result.bytesByID[packetID] += int64(len(packet))
		switch packetID {
		case protocol.PlayPacketIDKeepAlive:
			// 原样回复 Keep Alive。
			reply := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDKeepAlive)
			reply = append(reply, packet[offset:]...)
			if err := protocol.WritePacketWithCompression(conn, reply, threshold); err != nil {
				result.err = err
				return result
			}
		case protocol.PlayPacketIDSynchronizePlayerPos:
			// 确认传送并更新本地位置。
			teleportID, size, err := protocol.DecodeVarInt(packet[offset:])
			if err != nil {
				continue
			}
			offset += size
			if x, next, err := protocol.DecodeFloat64(packet, offset); err == nil {
				posX = x
				offset = next
				if y, next, err := protocol.DecodeFloat64(packet, offset); err == nil {
					posY = y
					offset = next
					if z, _, err := protocol.DecodeFloat64(packet, offset); err == nil {
						posZ = z
						havePos = true
					}
				}
			}
			confirm := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDConfirmTeleportation)
			confirm = protocol.AppendVarInt(confirm, teleportID)
			if err := protocol.WritePacketWithCompression(conn, confirm, threshold); err != nil {
				result.err = err
				return result
			}
		case protocol.PlayPacketIDChunkBatchFinished:
			// 回报期望的每 tick 区块数（原版客户端在每批结束后发送）。
			received := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDChunkBatchReceived)
			received = protocol.AppendFloat32(received, 20)
			if err := protocol.WritePacketWithCompression(conn, received, threshold); err != nil {
				result.err = err
				return result
			}
		case protocol.PlayPacketIDDisconnect:
			if message, _, err := readStringAt(packet, offset); err == nil {
				result.disconnect = message
			}
			return result
		case protocol.PlayPacketIDLogin:
			if result.joinTime == 0 {
				result.joinTime = time.Since(connectStart)
			}
		}
		if err := maybeMove(conn, threshold, &posX, &posY, &posZ, &havePos, &lastMove, delta, cfg.moveInterval); err != nil {
			result.err = err
			return result
		}
	}
}

// maybeMove 按 moveInterval 周期发送 Player Position（水平匀速移动，
// 触发服务端的区块流式发送与玩家同步路径）。
func maybeMove(conn net.Conn, threshold int32, posX, posY, posZ *float64, havePos *bool, lastMove *time.Time, delta float64, moveInterval time.Duration) error {
	if moveInterval <= 0 || !*havePos || time.Since(*lastMove) < moveInterval {
		return nil
	}
	*lastMove = time.Now()
	*posX += delta
	packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDPlayerPosition)
	packet = protocol.AppendFloat64(packet, *posX)
	packet = protocol.AppendFloat64(packet, *posY)
	packet = protocol.AppendFloat64(packet, *posZ)
	packet = append(packet, 0x01) // onGround
	return protocol.WritePacketWithCompression(conn, packet, threshold)
}

// readPacket 读取一个未压缩包（每次读取重置读超时）。
func readPacket(conn net.Conn) ([]byte, error) {
	if err := conn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
		return nil, err
	}
	return protocol.ReadPacket(conn)
}

// readPacketWithCompression 读取一个压缩格式的包（每次读取重置读超时；
// 配置阶段的大包与 50ms 轮询不允许沿用旧超时）。
func readPacketWithCompression(conn net.Conn, threshold int32) ([]byte, error) {
	if err := conn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
		return nil, err
	}
	return protocol.ReadPacketWithCompression(conn, threshold)
}

// appendString 写入 length-prefixed 字符串。
func appendString(dst []byte, value string) []byte {
	dst = protocol.AppendVarInt(dst, int32(len(value)))
	return append(dst, value...)
}

// readStringAt 读取 offset 处的 length-prefixed 字符串。
func readStringAt(data []byte, offset int) (string, int, error) {
	length, size, err := protocol.DecodeVarInt(data[offset:])
	if err != nil {
		return "", offset, err
	}
	offset += size
	if length < 0 || offset+int(length) > len(data) {
		return "", offset, fmt.Errorf("字符串长度越界：%d", length)
	}
	return string(data[offset : offset+int(length)]), offset + int(length), nil
}

// packetName 返回常见 clientbound 包的可读名字（未知 ID 显示为 0x..）。
func packetName(id int32) string {
	switch id {
	case protocol.PlayPacketIDChunkData:
		return "ChunkData"
	case protocol.PlayPacketIDChunkBatchStart:
		return "ChunkBatchStart"
	case protocol.PlayPacketIDChunkBatchFinished:
		return "ChunkBatchFinished"
	case protocol.PlayPacketIDKeepAlive:
		return "KeepAlive"
	case protocol.PlayPacketIDSynchronizePlayerPos:
		return "SyncPlayerPos"
	case protocol.PlayPacketIDEntityPositionSync:
		return "EntityPositionSync"
	case protocol.PlayPacketIDEntityHeadRotation:
		return "EntityHeadRotation"
	case protocol.PlayPacketIDAddEntity:
		return "AddEntity"
	case protocol.PlayPacketIDPlayerInfoUpdate:
		return "PlayerInfoUpdate"
	case protocol.PlayPacketIDLogin:
		return "Login"
	default:
		return fmt.Sprintf("0x%X", id)
	}
}

// report 汇总并打印压测结果。
func report(results []clientResult, elapsed time.Duration, requested int) {
	var (
		joined   int
		failed   int
		disconns int
		packets  int64
		bytes    int64
		joins    []time.Duration
		byID     = make(map[int32]int64)
		bytesID  = make(map[int32]int64)
	)
	for _, r := range results {
		packets += r.packets
		bytes += r.bytes
		for id, count := range r.byID {
			byID[id] += count
		}
		for id, count := range r.bytesByID {
			bytesID[id] += count
		}
		switch {
		case r.err != nil:
			failed++
		default:
			joined++
			if r.disconnect != "" {
				disconns++
			}
		}
		if r.joinTime > 0 {
			joins = append(joins, r.joinTime)
		}
	}
	sort.Slice(joins, func(i, j int) bool { return joins[i] < joins[j] })

	fmt.Printf("压测完成：请求 %d 个客户端，成功进入世界 %d，失败 %d，运行 %s\n",
		requested, joined, failed, elapsed.Round(time.Millisecond))
	if len(joins) > 0 {
		var sum time.Duration
		for _, d := range joins {
			sum += d
		}
		fmt.Printf("进入世界耗时：最短 %s，平均 %s，p95 %s，最长 %s\n",
			joins[0].Round(time.Millisecond),
			(sum / time.Duration(len(joins))).Round(time.Millisecond),
			joins[min(len(joins)-1, int(math.Ceil(float64(len(joins))*0.95))-1)].Round(time.Millisecond),
			joins[len(joins)-1].Round(time.Millisecond))
	}
	if joined > 0 {
		fmt.Printf("收包：合计 %d 个（平均每个客户端 %.1f 个），流量 %.2f MiB（平均 %.1f KiB/s/客户端）\n",
			packets, float64(packets)/float64(joined), float64(bytes)/(1<<20),
			float64(bytes)/1024/elapsed.Seconds()/float64(joined))
	}
	// 包类型分布（按数量取前若干）：便于判断流量构成（区块 / 实体同步 / 其他）。
	type idCount struct {
		id    int32
		count int64
	}
	counts := make([]idCount, 0, len(byID))
	for id, count := range byID {
		counts = append(counts, idCount{id, count})
	}
	sort.Slice(counts, func(i, j int) bool { return counts[i].count > counts[j].count })
	if len(counts) > 0 {
		fmt.Printf("包类型分布（前 5，按数量）：")
		for i, entry := range counts {
			if i == 5 {
				break
			}
			if i > 0 {
				fmt.Printf("、")
			}
			fmt.Printf("%s=%d (%.1f%% 包 / %.1f%% 字节)", packetName(entry.id), entry.count,
				100*float64(entry.count)/float64(packets),
				100*float64(bytesID[entry.id])/float64(bytes))
		}
		fmt.Println()
	}
	if disconns > 0 {
		fmt.Printf("被服务器断开：%d\n", disconns)
	}
	for _, r := range results {
		if r.err != nil {
			fmt.Printf("  客户端 %s 错误：%v\n", r.name, r.err)
		} else if r.disconnect != "" {
			fmt.Printf("  客户端 %s 被断开：%s\n", r.name, r.disconnect)
		}
	}
	if failed > 0 {
		os.Exit(1)
	}
}
