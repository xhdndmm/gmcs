//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

// gmcsload 是 gmcs 的简易负载压测客户端：模拟若干离线模式客户端完成
// 握手 → 登录 → 配置 → 进入世界，随后持续移动并回复 Keep Alive /
// Chunk Batch Received，用于观测服务器在并发连接下的表现。
//
// 说明：
//   - 仅支持离线模式（不实现加密/正版验证）；
//   - 报告的是客户端侧观测值（进入世界耗时、收包量与流量），
//     服务器侧 CPU/内存请配合 `pprof_address` 与系统工具观测；
//   - 单机 loopback 压测不代表真实网络环境。
//   - -random-move 会让每个客户端在限定半径内随机换向，避免所有客户端
//     一直沿 +X 直线移动，适合观察玩家分散探索时的区块流式发送。
//   - 可选的输入/视角/跳跃/挥手/攻击/聊天/命令/死亡重生行为用于覆盖更多
//     真实客户端路径；默认仍保持“登录 + 移动”的轻量基线。
//
// 用法：
//
//	go run ./cmd/gmcsload -addr 127.0.0.1:25565 -players 50 -duration 20s
//	go run ./cmd/gmcsload -random-move -move-radius 128 -players 20
//	go run ./cmd/gmcsload -sprint -jump-interval 2s -attack-interval 1s \
//	  -chat-interval 5s -command-interval 10s -players 20 -duration 1m
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"sort"
	"strings"
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
	randomMove := flag.Bool("random-move", false, "随机方向移动（默认沿 +X 匀速移动）")
	moveRadius := flag.Float64("move-radius", 64, "随机移动距出生点的最大半径（0 表示不限）")
	moveTurn := flag.Duration("random-turn", 2*time.Second, "随机移动平均换向间隔（0 表示每个移动包都换向）")
	moveSeed := flag.Int64("move-seed", 1, "随机移动种子；每个客户端使用 seed+序号")
	sprint := flag.Bool("sprint", false, "发送疾跑输入，并允许更快的移动速度")
	sneak := flag.Bool("sneak", false, "持续发送潜行输入")
	speed := flag.Float64("speed", 0, "移动速度（方块/秒；0 表示按疾跑状态取 4 或 5.6）")
	jumpInterval := flag.Duration("jump-interval", 0, "跳跃间隔（0 表示不跳）")
	rotateInterval := flag.Duration("rotate-interval", 0, "随机视角更新间隔（0 表示不转视角）")
	swingInterval := flag.Duration("swing-interval", 0, "挥手间隔（0 表示不挥手）")
	attackInterval := flag.Duration("attack-interval", 0, "攻击最近实体间隔（0 表示不攻击）")
	chatInterval := flag.Duration("chat-interval", 0, "发送聊天消息间隔（0 表示不聊天）")
	chatMessage := flag.String("chat", "gmcsload", "聊天消息内容")
	commandInterval := flag.Duration("command-interval", 0, "发送命令间隔（0 表示不发送）")
	commandText := flag.String("command", "/list", "重复发送的命令（可带或不带前导 /）")
	tickEnd := flag.Bool("tick-end", true, "每 tick 发送 Client Tick End（更接近真实客户端）")
	autoRespawn := flag.Bool("auto-respawn", true, "死亡后自动发送重生请求")
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
	if *moveRadius < 0 {
		fmt.Fprintln(os.Stderr, "move-radius 不能为负数")
		os.Exit(2)
	}
	if *moveTurn < 0 {
		fmt.Fprintln(os.Stderr, "random-turn 不能为负数")
		os.Exit(2)
	}
	if *speed < 0 {
		fmt.Fprintln(os.Stderr, "speed 不能为负数")
		os.Exit(2)
	}
	if *speed == 0 {
		if *sprint {
			*speed = 5.6
		} else {
			*speed = 4.0
		}
	}
	if *jumpInterval < 0 || *rotateInterval < 0 || *swingInterval < 0 ||
		*attackInterval < 0 || *chatInterval < 0 || *commandInterval < 0 {
		fmt.Fprintln(os.Stderr, "各类 interval 不能为负数")
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
			randomMove:      *randomMove,
			moveRadius:      *moveRadius,
			randomTurn:      *moveTurn,
			moveSeed:        *moveSeed + int64(i)*7919,
			sprint:          *sprint,
			sneak:           *sneak,
			speed:           *speed,
			jumpInterval:    *jumpInterval,
			rotateInterval:  *rotateInterval,
			swingInterval:   *swingInterval,
			attackInterval:  *attackInterval,
			chatInterval:    *chatInterval,
			chatMessage:     *chatMessage,
			commandInterval: *commandInterval,
			commandText:     *commandText,
			tickEnd:         *tickEnd,
			autoRespawn:     *autoRespawn,
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
	randomMove      bool
	moveRadius      float64
	randomTurn      time.Duration
	moveSeed        int64
	sprint          bool
	sneak           bool
	speed           float64
	jumpInterval    time.Duration
	rotateInterval  time.Duration
	swingInterval   time.Duration
	attackInterval  time.Duration
	chatInterval    time.Duration
	chatMessage     string
	commandInterval time.Duration
	commandText     string
	tickEnd         bool
	autoRespawn     bool
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
	deaths     int64
	respawns   int64
	attacks    int64
	chats      int64
}

// trackedEntity 是模拟客户端通过 Add Entity 包观察到的实体。
type trackedEntity struct {
	x, y, z float64
	typeID  int32
}

// clientRuntime 保存一次连接内需要跨数据包维护的状态。
// 只由该客户端的 goroutine 访问。
type clientRuntime struct {
	entityID    int32
	health      float32
	dead        bool
	respawned   bool
	entities    map[int32]trackedEntity
	lastInput   uint8
	haveInput   bool
	lastTick    time.Time
	lastRotate  time.Time
	lastJump    time.Time
	lastSwing   time.Time
	lastAttack  time.Time
	lastChat    time.Time
	lastCommand time.Time
	result      *clientResult
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
	move := newMovementState(cfg.randomMove, cfg.moveRadius, cfg.randomTurn, cfg.moveSeed)
	sim := &clientRuntime{entities: make(map[int32]trackedEntity), result: &result}
	delta := cfg.speed / 20 // 每个移动包按 20 tick/秒折算
	for {
		if ctx.Err() != nil {
			return result
		}
		// 持续收到实体/区块包时读循环不会超时；在每个循环开头也检查一次
		// 移动周期，确保高频广播下仍按 moveInterval 发送位置包。
		if err := sim.tick(conn, threshold, cfg, move); err != nil {
			result.err = err
			return result
		}
		if err := move.maybeMove(conn, threshold, cfg.moveInterval, delta); err != nil {
			result.err = err
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
				if err := sim.tick(conn, threshold, cfg, move); err != nil {
					result.err = err
					return result
				}
				if err := move.maybeMove(conn, threshold, cfg.moveInterval, delta); err != nil {
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
			var x, y, z float64
			if value, next, err := protocol.DecodeFloat64(packet, offset); err == nil {
				x = value
				offset = next
				if value, next, err := protocol.DecodeFloat64(packet, offset); err == nil {
					y = value
					offset = next
					if value, next, err := protocol.DecodeFloat64(packet, offset); err == nil {
						z = value
						offset = next
						move.setPosition(x, y, z, true)
					}
				}
			}
			if yaw, next, err := protocol.DecodeFloat32(packet, offset); err == nil {
				if pitch, _, err := protocol.DecodeFloat32(packet, next); err == nil {
					move.yaw, move.pitch = yaw, pitch
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
		case protocol.PlayPacketIDLogin:
			if entityID, _, err := protocol.DecodeInt32(packet, offset); err == nil {
				sim.entityID = entityID
			}
			if result.joinTime == 0 {
				result.joinTime = time.Since(connectStart)
			}
		case protocol.PlayPacketIDUpdateHealth:
			if health, _, err := protocol.DecodeFloat32(packet, offset); err == nil {
				sim.health = health
				if health <= 0 && !sim.dead {
					sim.dead = true
					result.deaths++
					sim.respawned = false
				}
			}
		case protocol.PlayPacketIDPlayerCombatKill:
			if !sim.dead {
				sim.dead = true
				result.deaths++
				sim.respawned = false
			}
		case protocol.PlayPacketIDRespawn:
			sim.dead = false
			sim.respawned = false
			sim.entities = make(map[int32]trackedEntity)
		case protocol.PlayPacketIDAddEntity:
			if id, x, y, z, typeID, ok := parseAddEntity(packet, offset); ok {
				sim.entities[id] = trackedEntity{x: x, y: y, z: z, typeID: typeID}
			}
		case protocol.PlayPacketIDEntityPositionSync:
			if id, x, y, z, ok := parseEntityPosition(packet, offset); ok {
				if entity, exists := sim.entities[id]; exists {
					entity.x, entity.y, entity.z = x, y, z
					sim.entities[id] = entity
				}
			}
		case protocol.PlayPacketIDRemoveEntities:
			for _, id := range parseRemoveEntities(packet, offset) {
				delete(sim.entities, id)
			}
		case protocol.PlayPacketIDDisconnect:
			if message, _, err := readStringAt(packet, offset); err == nil {
				result.disconnect = message
			}
			return result
		}
		if err := sim.tick(conn, threshold, cfg, move); err != nil {
			result.err = err
			return result
		}
		if err := move.maybeMove(conn, threshold, cfg.moveInterval, delta); err != nil {
			result.err = err
			return result
		}
	}
}

// tick 发送真实客户端会周期性发送的输入、视角、挥手、聊天、命令与重生包。
// 每轮读循环/超时各调用一次，内部按各自间隔限频。
func (r *clientRuntime) tick(conn net.Conn, threshold int32, cfg clientConfig, move *movementState) error {
	now := time.Now()
	if cfg.tickEnd && (r.lastTick.IsZero() || now.Sub(r.lastTick) >= 50*time.Millisecond) {
		r.lastTick = now
		if err := protocol.WritePacketWithCompression(conn,
			protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDClientTickEnd), threshold); err != nil {
			return err
		}
	}
	if r.dead {
		if cfg.autoRespawn && !r.respawned {
			packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDClientCommand)
			packet = protocol.AppendVarInt(packet, 0) // 0 = 重生请求
			if err := protocol.WritePacketWithCompression(conn, packet, threshold); err != nil {
				return err
			}
			r.respawned = true
			r.result.respawns++
		}
		return nil
	}
	if !move.havePos {
		return nil
	}
	if cfg.jumpInterval > 0 && (r.lastJump.IsZero() || now.Sub(r.lastJump) >= cfg.jumpInterval) {
		r.lastJump = now
		move.startJump()
	}
	if cfg.rotateInterval > 0 && (r.lastRotate.IsZero() || now.Sub(r.lastRotate) >= cfg.rotateInterval) {
		r.lastRotate = now
		// 在 ±25° 内随机改变视角，避免所有客户端保持完全相同朝向。
		move.yaw += float32((move.rng.Float64() - 0.5) * 50)
		move.pitch = float32(math.Max(-80, math.Min(80, float64(move.pitch)+(move.rng.Float64()-0.5)*20)))
		packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDPlayerRotation)
		packet = protocol.AppendFloat32(packet, move.yaw)
		packet = protocol.AppendFloat32(packet, move.pitch)
		if err := protocol.WritePacketWithCompression(conn, packet, threshold); err != nil {
			return err
		}
	}
	if cfg.sprint || cfg.sneak {
		var flags uint8
		if cfg.sprint {
			flags |= 0x01 // forward
			flags |= protocol.PlayerInputSprint
		}
		if cfg.sneak {
			flags |= protocol.PlayerInputShift
		}
		if !r.haveInput || flags != r.lastInput {
			r.haveInput = true
			r.lastInput = flags
			packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDPlayerInput)
			packet = append(packet, flags)
			if err := protocol.WritePacketWithCompression(conn, packet, threshold); err != nil {
				return err
			}
		}
	}
	if cfg.swingInterval > 0 && (r.lastSwing.IsZero() || now.Sub(r.lastSwing) >= cfg.swingInterval) {
		r.lastSwing = now
		packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDSwingArm)
		packet = protocol.AppendVarInt(packet, 0) // main hand
		if err := protocol.WritePacketWithCompression(conn, packet, threshold); err != nil {
			return err
		}
	}
	if cfg.attackInterval > 0 && (r.lastAttack.IsZero() || now.Sub(r.lastAttack) >= cfg.attackInterval) {
		r.lastAttack = now
		if target, ok := r.nearestEntity(move.x, move.y, move.z, 4.5); ok {
			packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDInteract)
			packet = protocol.AppendVarInt(packet, target)
			packet = protocol.AppendVarInt(packet, protocol.InteractActionAttack)
			if err := protocol.WritePacketWithCompression(conn, packet, threshold); err != nil {
				return err
			}
			r.result.attacks++
		}
	}
	if cfg.chatInterval > 0 && (r.lastChat.IsZero() || now.Sub(r.lastChat) >= cfg.chatInterval) {
		r.lastChat = now
		packet := encodeUnsignedChat(cfg.chatMessage, time.Now())
		if err := protocol.WritePacketWithCompression(conn, packet, threshold); err != nil {
			return err
		}
		r.result.chats++
	}
	if cfg.commandInterval > 0 && (r.lastCommand.IsZero() || now.Sub(r.lastCommand) >= cfg.commandInterval) {
		r.lastCommand = now
		command := strings.TrimPrefix(strings.TrimSpace(cfg.commandText), "/")
		packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDChatCommand)
		packet = appendString(packet, command)
		if err := protocol.WritePacketWithCompression(conn, packet, threshold); err != nil {
			return err
		}
		r.result.chats++
	}
	return nil
}

// encodeUnsignedChat 构造离线/未签名聊天消息（无签名 + 空确认窗口）。
// 服务端在未建立聊天密钥链时按原版 Decoder.unsigned 分支接受该消息；
// 空确认窗口的位图全 0、校验和为 0（服务端校验器允许 0）。
func encodeUnsignedChat(message string, now time.Time) []byte {
	packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDChatMessage)
	packet = appendString(packet, message)
	packet = protocol.AppendInt64(packet, now.UnixMilli())
	packet = protocol.AppendInt64(packet, 0)  // salt
	packet = append(packet, 0)                // 无签名
	packet = protocol.AppendVarInt(packet, 0) // acknowledged offset
	packet = append(packet, 0, 0, 0)          // acknowledged bitset（空）
	packet = append(packet, 0)                // checksum
	return packet
}

// nearestEntity 返回距离玩家最近且在 maxDistance 内的已追踪实体。
func (r *clientRuntime) nearestEntity(x, y, z, maxDistance float64) (int32, bool) {
	bestID := int32(0)
	best := maxDistance * maxDistance
	for id, entity := range r.entities {
		dx, dy, dz := entity.x-x, entity.y-y, entity.z-z
		distance := dx*dx + dy*dy + dz*dz
		if distance <= best {
			best = distance
			bestID = id
		}
	}
	return bestID, bestID != 0
}

// parseAddEntity 解析 Add Entity 的实体 ID、坐标与类型，忽略 UUID 与速度。
func parseAddEntity(packet []byte, offset int) (id int32, x, y, z float64, typeID int32, ok bool) {
	var size int
	id, size, err := protocol.DecodeVarInt(packet[offset:])
	if err != nil {
		return 0, 0, 0, 0, 0, false
	}
	offset += size
	offset += 16 // UUID
	typeID, size, err = protocol.DecodeVarInt(packet[offset:])
	if err != nil {
		return 0, 0, 0, 0, 0, false
	}
	offset += size
	x, offset, err = protocol.DecodeFloat64(packet, offset)
	if err != nil {
		return 0, 0, 0, 0, 0, false
	}
	y, offset, err = protocol.DecodeFloat64(packet, offset)
	if err != nil {
		return 0, 0, 0, 0, 0, false
	}
	z, _, err = protocol.DecodeFloat64(packet, offset)
	if err != nil {
		return 0, 0, 0, 0, 0, false
	}
	return id, x, y, z, typeID, true
}

// parseEntityPosition 解析 Entity Position Sync 的实体 ID 与绝对坐标。
func parseEntityPosition(packet []byte, offset int) (id int32, x, y, z float64, ok bool) {
	var size int
	id, size, err := protocol.DecodeVarInt(packet[offset:])
	if err != nil {
		return 0, 0, 0, 0, false
	}
	offset += size
	x, offset, err = protocol.DecodeFloat64(packet, offset)
	if err != nil {
		return 0, 0, 0, 0, false
	}
	y, offset, err = protocol.DecodeFloat64(packet, offset)
	if err != nil {
		return 0, 0, 0, 0, false
	}
	z, _, err = protocol.DecodeFloat64(packet, offset)
	if err != nil {
		return 0, 0, 0, 0, false
	}
	return id, x, y, z, true
}

// parseRemoveEntities 解析 Remove Entities 的实体 ID 列表。
func parseRemoveEntities(packet []byte, offset int) []int32 {
	count, size, err := protocol.DecodeVarInt(packet[offset:])
	if err != nil || count < 0 || count > 4096 {
		return nil
	}
	offset += size
	ids := make([]int32, 0, count)
	for i := int32(0); i < count; i++ {
		id, size, err := protocol.DecodeVarInt(packet[offset:])
		if err != nil {
			return nil
		}
		offset += size
		ids = append(ids, id)
	}
	return ids
}

// movementState 维护模拟客户端的本地位置与随机漫游方向。
// 每个客户端独立持有，不跨 goroutine 共享。
type movementState struct {
	x, y, z  float64
	baseY    float64
	yaw      float32
	pitch    float32
	havePos  bool
	lastMove time.Time

	verticalVelocity float64
	jumping          bool
	onGround         bool

	random       bool
	radius       float64
	turnInterval time.Duration
	rng          *rand.Rand

	dirX, dirZ float64
	nextTurn   time.Time
	originX    float64
	originZ    float64
	originSet  bool
}

func newMovementState(random bool, radius float64, turnInterval time.Duration, seed int64) *movementState {
	m := &movementState{
		random:       random,
		radius:       radius,
		turnInterval: turnInterval,
		rng:          rand.New(rand.NewSource(seed)),
	}
	m.dirX, m.dirZ = m.randomDirection()
	return m
}

// setPosition 更新服务端确认的位置。corrected 表示这是服务器回拉/传送；
// 此时立即换向，避免客户端继续朝刚被拒绝的方向撞墙。
func (m *movementState) setPosition(x, y, z float64, corrected bool) {
	m.x, m.y, m.z = x, y, z
	m.baseY = y
	m.verticalVelocity = 0
	m.jumping = false
	m.onGround = true
	m.havePos = true
	if !m.originSet {
		m.originX, m.originZ = x, z
		m.originSet = true
	}
	if corrected {
		m.nextTurn = time.Time{}
	}
}

func (m *movementState) randomDirection() (float64, float64) {
	angle := m.rng.Float64() * 2 * math.Pi
	return math.Cos(angle), math.Sin(angle)
}

// turn 选择新的随机方向。接近活动半径时偏向出生点，避免长期卡在边界。
func (m *movementState) turn(now time.Time) {
	m.dirX, m.dirZ = m.randomDirection()
	if m.radius > 0 && m.originSet {
		dist := math.Hypot(m.x-m.originX, m.z-m.originZ)
		if dist > m.radius*0.8 {
			toOrigin := math.Atan2(m.originZ-m.z, m.originX-m.x)
			angle := toOrigin + (m.rng.Float64()-0.5)*math.Pi/2
			m.dirX, m.dirZ = math.Cos(angle), math.Sin(angle)
		}
	}
	m.scheduleTurn(now)
}

func (m *movementState) scheduleTurn(now time.Time) {
	if m.turnInterval <= 0 {
		m.nextTurn = now
		return
	}
	delay := m.turnInterval/2 + time.Duration(m.rng.Int63n(int64(m.turnInterval)))
	m.nextTurn = now.Add(delay)
}

// step 计算下一个水平位移；不修改位置，便于测试边界行为。
func (m *movementState) step(now time.Time, delta float64) (float64, float64) {
	if !m.random {
		return delta, 0
	}
	if m.nextTurn.IsZero() || !now.Before(m.nextTurn) {
		m.turn(now)
	}
	dx, dz := m.dirX*delta, m.dirZ*delta
	if m.radius > 0 && m.originSet {
		nx, nz := m.x+dx, m.z+dz
		if math.Hypot(nx-m.originX, nz-m.originZ) > m.radius {
			// 越过边界：直接朝出生点走，并重新安排换向。
			toOrigin := math.Atan2(m.originZ-m.z, m.originX-m.x)
			m.dirX, m.dirZ = math.Cos(toOrigin), math.Sin(toOrigin)
			m.scheduleTurn(now)
			dx, dz = m.dirX*delta, m.dirZ*delta
		}
	}
	return dx, dz
}

// maybeMove 按 moveInterval 周期发送 Player Position。
func (m *movementState) maybeMove(conn net.Conn, threshold int32, moveInterval time.Duration, delta float64) error {
	if moveInterval <= 0 || !m.havePos || time.Since(m.lastMove) < moveInterval {
		return nil
	}
	m.lastMove = time.Now()
	dx, dz := m.step(m.lastMove, delta)
	m.x += dx
	m.z += dz
	m.advanceVertical()
	packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDPlayerPosition)
	packet = protocol.AppendFloat64(packet, m.x)
	packet = protocol.AppendFloat64(packet, m.y)
	packet = protocol.AppendFloat64(packet, m.z)
	if m.onGround && !m.jumping {
		packet = append(packet, 0x01)
	} else {
		packet = append(packet, 0x00)
	}
	return protocol.WritePacketWithCompression(conn, packet, threshold)
}

// startJump 开始一次近似原版跳跃弧线（初速度 0.42、重力 0.08、阻力 0.98）。
// 只在当前着地且未跳跃时生效。
func (m *movementState) startJump() {
	if !m.havePos || m.jumping || !m.onGround {
		return
	}
	m.jumping = true
	m.onGround = false
	m.verticalVelocity = 0.42
}

// advanceVertical 推进跳跃弧线；落地时对齐到 baseY。
func (m *movementState) advanceVertical() {
	if !m.jumping {
		return
	}
	m.y += m.verticalVelocity
	m.verticalVelocity = (m.verticalVelocity - 0.08) * 0.98
	if m.y <= m.baseY {
		m.y = m.baseY
		m.verticalVelocity = 0
		m.jumping = false
		m.onGround = true
	}
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
		deaths   int64
		respawns int64
		attacks  int64
		chats    int64
		joins    []time.Duration
		byID     = make(map[int32]int64)
		bytesID  = make(map[int32]int64)
	)
	for _, r := range results {
		packets += r.packets
		bytes += r.bytes
		deaths += r.deaths
		respawns += r.respawns
		attacks += r.attacks
		chats += r.chats
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
	if deaths+attacks+chats > 0 {
		fmt.Printf("模拟行为：死亡 %d、重生 %d、攻击 %d、聊天/命令 %d\n",
			deaths, respawns, attacks, chats)
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
