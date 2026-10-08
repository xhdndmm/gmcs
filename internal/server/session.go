package server

import (
	"bytes"
	"crypto/aes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"errors"
	"io"
	"log/slog"
	"math"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gmcs/internal/config"
	"gmcs/internal/item"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

const (
	// 各阶段的读超时：超时未收到任何数据即断开连接。
	loginTimeout         = 30 * time.Second
	configurationTimeout = 30 * time.Second
	playTimeout          = 30 * time.Second
	// 压缩阈值，与 Set Compression 发送的值一致。
	compressionThreshold = 256
	// 默认的 Keep Alive 发送间隔（测试中可调整）。
	defaultKeepAliveInterval = 10 * time.Second
)

// corePack 是本服务器参与 Known Packs 协商的 1.21.11 原版数据包。
// 协商成功后，同步注册表条目可以省略 NBT 内容，由客户端本地数据包提供。
var corePack = protocol.KnownPack{Namespace: "minecraft", ID: "core", Version: "1.21.11"}

// supportedProtocolVersion 是本服务器实现的 Minecraft 协议版本（1.21.11）。
// 其他版本的客户端会被拒绝，避免协议不匹配导致的错误行为。
const supportedProtocolVersion = 774

// session 承载一个已握手连接从登录到进入游戏的生命周期。
type session struct {
	server          *Server
	conn            net.Conn
	protocolVersion int32

	// reader/writer 是会话的读写通道；正版登录后包装为 AES/CFB8 加密流。
	reader io.Reader
	writer io.Writer

	writeMu sync.Mutex

	name       string
	uuid       [16]byte
	entityID   int32
	teleportID int32
	clientInfo protocol.ClientInformation

	// profileProperties 是会话服务器返回的玩家属性（在线模式）。
	profileProperties []protocol.GameProfileProperty

	// chatSession 是客户端上报的聊天会话公钥（chat_session_update）；
	// 由会话读循环写入，其他玩家读取时需要加锁。
	chatSessionMu sync.Mutex
	chatSession   *protocol.ChatSession

	// 区块流式加载状态（由会话 goroutine 串行访问）。
	sentChunks  map[world.ChunkPos]struct{}
	centerChunk world.ChunkPos
	// desiredChunksPerTick 是客户端通过 Chunk Batch Received 回报的期望
	// 每 tick 区块数（仅记录，当前不做节流）。
	desiredChunksPerTick float32

	// inventory 是玩家物品栏，由会话串行访问。
	inventory item.Inventory
	// selectedSlot 是当前选中的快捷栏槽位（0–8），由会话串行访问。
	selectedSlot int

	// 容器窗口状态（由会话串行访问）：openContainer 是当前打开的容器，
	// windowID 是窗口编号（0 保留给玩家物品栏），windowState 是槽位状态号，
	// cursor 是鼠标持有的物品，drag 是进行中的拖拽分发。
	openContainer *containerState
	// invCraft / openCraft 是合成格状态：invCraft 为窗口 0 的 2×2 格
	// （随物品栏生命周期），openCraft 为工作台 3×3 格（随窗口生命周期）。
	invCraft  *craftingState
	openCraft *craftingState
	// enderChest 是玩家的末影箱内容（按玩家独立存储，随玩家数据持久化）。
	enderChest enderChestStorage
	// openEnder 标记末影箱窗口是否打开。
	openEnder   bool
	windowID    int32
	windowState int32
	cursor      item.Stack
	drag        *dragState
	// windowCounter 是窗口编号分配计数器。
	windowCounter int32
	// sneaking 记录客户端上报的潜行状态（潜行时交互不打开容器）。
	sneaking bool

	keepAliveMu      sync.Mutex
	pendingKeepAlive int64

	// chatIndex 是该玩家的聊天消息序号（从 0 递增）。
	chatIndex atomic.Int32

	// 玩家战斗与位置状态：由读循环与实体 Tick 共同访问，受 stateMu 保护。
	stateMu    sync.Mutex
	posX, posY float64
	posZ       float64
	yaw, pitch float32
	health     float32
	food       int32
	saturation float32
	// exhaustion 是饥饿系统的疲劳度：累计到 4 点消耗 1 点饥饿值。
	exhaustion float32
	// foodTimer 驱动自然恢复/饥饿伤害的 4 秒（80 tick）计时。
	foodTimer int
	// sprinting 记录客户端上报的疾跑状态（用于计算疲劳度）。
	sprinting bool
	// experience 是经验值（总量与等级）。
	experience    int32
	experienceBar float32
	dead          bool
	gameMode      uint8
	lastHurt      time.Time

	// 下落跟踪（摔落伤害）：以服务器端地面检测驱动，仅由会话读循环访问。
	fallStartY float64
	airborne   bool
	// airRise 是本次腾空累计上升（方块）：悬空上升限制（防飞行）用，
	// 落地/入水时清零。
	airRise float64

	// 高频包限流（令牌桶，仅读循环访问）：聊天/命令与移动都会转发给
	// 其他玩家，防止单客户端包洪泛放大广播成本。
	chatBudget   packetBudget
	moveBudget   packetBudget
	actionBudget packetBudget

	// lastMoveTime 是上一个被接受的移动包时间（逐 tick 速度上限用）。
	lastMoveTime time.Time

	// joined 在初始数据包全部发送后置位；在此之前生物不会索敌该玩家。
	joined atomic.Bool
}

// nextWindowID 分配一个新的容器窗口编号（1–100 循环，0 保留给玩家物品栏）。
func (s *session) nextWindowID() int32 {
	s.windowCounter++
	if s.windowCounter > 100 {
		s.windowCounter = 1
	}
	return s.windowCounter
}

// setChatSession 保存客户端上报的聊天会话公钥。
func (s *session) setChatSession(session protocol.ChatSession) {
	s.chatSessionMu.Lock()
	s.chatSession = &session
	s.chatSessionMu.Unlock()
}

// chatSessionSnapshot 返回当前聊天会话（没有时为 nil）。
func (s *session) chatSessionSnapshot() *protocol.ChatSession {
	s.chatSessionMu.Lock()
	defer s.chatSessionMu.Unlock()
	return s.chatSession
}

// markJoined 标记玩家已完成进入世界的初始化。
func (s *session) markJoined() {
	s.joined.Store(true)
}

// isJoined 报告玩家的初始数据包是否已发送完毕。
func (s *session) isJoined() bool {
	return s.joined.Load()
}

func newSession(server *Server, conn net.Conn, protocolVersion int32) *session {
	return &session{
		server:          server,
		conn:            conn,
		protocolVersion: protocolVersion,
		reader:          conn,
		writer:          conn,
		health:          maxPlayerHealth,
		food:            maxPlayerFood,
		saturation:      playerSaturation,
		gameMode:        server.defaultGameMode,
		sentChunks:      make(map[world.ChunkPos]struct{}),
	}
}

// playerPosition 返回玩家的最新位置与朝向。
func (s *session) playerPosition() (x, y, z float64, yaw, pitch float32) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.posX, s.posY, s.posZ, s.yaw, s.pitch
}

// setPlayerPosition 更新位置与朝向（由移动数据包与传送路径调用）。
func (s *session) setPlayerPosition(x, y, z float64, yaw, pitch float32) {
	s.stateMu.Lock()
	s.posX, s.posY, s.posZ, s.yaw, s.pitch = x, y, z, yaw, pitch
	s.stateMu.Unlock()
}

// setPlayerRotation 更新朝向。
func (s *session) setPlayerRotation(yaw, pitch float32) {
	s.stateMu.Lock()
	s.yaw, s.pitch = yaw, pitch
	s.stateMu.Unlock()
}

// isDead 报告玩家是否处于死亡状态。
func (s *session) isDead() bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.dead
}

// canBeAttacked 报告玩家能否成为生物的攻击目标：已完成进入世界的初始化、
// 未死亡，且不是创造/旁观模式。
func (s *session) canBeAttacked() bool {
	if !s.isJoined() {
		return false
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return !s.dead && s.gameMode != uint8(config.GameModeCreative) && s.gameMode != uint8(config.GameModeSpectator)
}

// maxMoveDistance 是单个移动数据包允许的最大位移（方块）。
// 超出该值的位移（瞬移式作弊的典型特征）会被拒绝并回拉。
// 更细的速度限制见 maxMoveSpeedPerTick。
const maxMoveDistance = 100.0

// moveSpeedPerTick 是逐 tick 允许的位移基数（方块）。
// 与原版（ServerGamePacketListenerImpl）一致：允许移动距离的平方不超过
// 100 × 经过的 tick 数，即距离 ≤ 10 × √tick 数；tick 数按距上一个被接受
// 的移动包的时间折算（至少 1 tick）。闲置超过 moveTicksReset（1 秒）时
// 按 1 tick 计，避免“先挂机再瞬移”绕过限制。
const (
	moveSpeedPerTick = 10.0
	moveTicksReset   = 20.0
)

// handleMove 处理玩家移动（位置或位置+朝向）：位移/速度、穿墙、悬空上升
// （防飞行）与潜行边缘保护校验通过后更新位置并广播；否则回拉。
// 仅由会话读循环调用。
func (s *session) handleMove(x, y, z float64, yaw, pitch float32, rotate bool, now time.Time) {
	if !s.moveBudget.allow(now, moveRate, moveBurst) {
		return // 移动包洪泛：静默丢弃（服务器位置保持不变）
	}
	prevX, prevY, prevZ, prevYaw, prevPitch := s.playerPosition()
	if !rotate {
		yaw, pitch = prevYaw, prevPitch
	}
	if !s.server.insideBorder(x, z) || !s.acceptMove(x, y, z, now) || !s.server.positionClear(x, y, z) {
		s.resyncPosition()
		return
	}
	if !s.acceptRise(prevX, prevY, x, y, z) {
		s.resyncPosition()
		return
	}
	if s.sneaking {
		x, z = s.sneakClamp(prevX, prevZ, x, y, z)
	}
	s.lastMoveTime = now
	s.setPlayerPosition(x, y, z, yaw, pitch)
	s.updateFallState(x, y, z)
	s.addMovementExhaustion(math.Hypot(x-prevX, z-prevZ), y > prevY+0.5)
	s.server.broadcastPlayerMove(s)
	s.updateChunks(x, z)
}

// maxJumpRise 是一次腾空允许的累计上升（方块）：原版跳跃弧顶约 0.96，
// 击退/活塞等留出余量；生存/冒险模式超过即视为飞行并回拉。
const maxJumpRise = 1.2

// packetBudget 是简单的令牌桶（每会话）：限制高频数据包的处理速率。
// 仅由会话读循环访问，无需加锁。
type packetBudget struct {
	tokens float64
	last   time.Time
}

// allow 消耗一个令牌；rate 为每秒补充速率、burst 为桶容量（首包自动满桶）。
func (b *packetBudget) allow(now time.Time, rate, burst float64) bool {
	if b.last.IsZero() {
		b.last = now
		b.tokens = burst
	}
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = min(b.tokens+elapsed*rate, burst)
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// 高频包限流参数：正常客户端约 20–30 移动包/秒、聊天远低于 2 条/秒；
// 额度给突发（滞后追赶/粘贴）留出余量，超限静默丢弃。
const (
	chatRate    = 2.0
	chatBurst   = 10.0
	moveRate    = 60.0
	moveBurst   = 80.0
	actionRate  = 50.0
	actionBurst = 100.0
)

// acceptRise 校验悬空状态下的上升（简易飞行检测）：一次腾空累计上升
// 不得超过 maxJumpRise。创造/旁观豁免；落地或入水时清零（游泳/跳跃合法）。
// 注意：被回拉（resync）不清零——否则作弊者可“分段上升”绕过限制。
func (s *session) acceptRise(prevX, prevY, x, y, z float64) bool {
	switch s.gameModeID() {
	case uint8(config.GameModeCreative), uint8(config.GameModeSpectator):
		return true
	}
	if s.server.feetInWater(x, y, z) || s.server.feetInWater(prevX, prevY, z) {
		s.airRise = 0
		return true
	}
	if s.server.supportedAt(x, y, z) {
		// 落到支撑面（落地/走上台阶）：本次腾空结束。
		s.airRise = 0
		return true
	}
	if y <= prevY {
		return true // 悬空中的下降或水平移动
	}
	s.airRise += y - prevY
	return s.airRise <= maxJumpRise
}

// sneakClamp 潜行边缘保护：目标位置没有支撑面时沿边缘滑动
// （先试只改 X、再试只改 Z），都不行则保持原位。
func (s *session) sneakClamp(prevX, prevZ float64, x, y, z float64) (float64, float64) {
	if s.server.supportedAt(x, y, z) {
		return x, z
	}
	if s.server.supportedAt(x, y, prevZ) && s.server.positionClear(x, y, prevZ) {
		return x, prevZ
	}
	if s.server.supportedAt(prevX, y, z) && s.server.positionClear(prevX, y, z) {
		return prevX, z
	}
	return prevX, prevZ
}

// acceptMove 报告目标位置是否在允许的位移与速度范围内。
func (s *session) acceptMove(x, y, z float64, now time.Time) bool {
	px, py, pz, _, _ := s.playerPosition()
	dx, dy, dz := x-px, y-py, z-pz
	distanceSq := dx*dx + dy*dy + dz*dz
	if distanceSq > maxMoveDistance*maxMoveDistance {
		return false
	}
	elapsedTicks := 1.0
	if !s.lastMoveTime.IsZero() {
		elapsedTicks = math.Max(1, now.Sub(s.lastMoveTime).Seconds()*20)
		if elapsedTicks > moveTicksReset {
			elapsedTicks = 1
		}
	}
	allowed := moveSpeedPerTick * math.Sqrt(elapsedTicks)
	return math.Sqrt(distanceSq) <= allowed
}

// isSolidBlock 报告世界坐标处是否为固体方块（非空气、非水）。
// 仅用于视线遮挡等粗略判定；移动碰撞请使用 positionClear（形状级 AABB）。
func (s *Server) isSolidBlock(x, y, z int) bool {
	state := s.world.BlockAt(x, y, z)
	return state != world.AirBlock && state != world.WaterBlock
}

// playerBox 返回玩家碰撞盒（宽 0.6、高 1.8，脚底在 y）。
func playerBox(x, y, z float64) world.Box {
	return world.Box{
		MinX: x - 0.3, MinY: y, MinZ: z - 0.3,
		MaxX: x + 0.3, MaxY: y + 1.8, MaxZ: z + 0.3,
	}
}

// positionClear 报告玩家碰撞盒是否未嵌入方块碰撞形状。
// 用于检测穿墙（no-clip）式移动：客户端自身的碰撞不允许进入方块形状，
// 因此“终点嵌在形状里”只可能来自作弊或状态错乱。查询为形状级 AABB
// （支持半砖/台阶/栅栏/门等非完整碰撞体），相触不算相交。
func (s *Server) positionClear(x, y, z float64) bool {
	return !s.world.Collides(playerBox(x, y, z))
}

// supportedAt 报告玩家脚下是否有可站立的碰撞面（服务器端重力模拟）。
// 形状级判定：脚底 0.1 格以内存在水平相交的碰撞面顶面即视为支撑
// （半砖/台阶/雪层/栅栏顶面等均正确）。
func (s *Server) supportedAt(x, y, z float64) bool {
	_, _, ok := s.world.SurfaceBelow(playerBox(x, y, z), 0.1)
	return ok
}

// feetInWater 报告玩家脚部是否位于水中（落水重置下落高度）。
func (s *Server) feetInWater(x, y, z float64) bool {
	return s.world.BlockAt(int(math.Floor(x)), int(math.Floor(y+0.1)), int(math.Floor(z))) == world.WaterBlock
}

// resyncPosition 把客户端拉回服务器记录的位置（拒绝越界或无效移动）。
// 仅在会话读循环中调用。
func (s *session) resyncPosition() {
	x, y, z, yaw, pitch := s.playerPosition()
	s.teleportID++
	s.resetFallState()
	packet := protocol.EncodeSynchronizePlayerPosition(s.teleportID, x, y, z, 0, 0, 0, yaw, pitch)
	if err := s.writePacket(packet); err != nil {
		_ = s.conn.Close()
		return
	}
	// 其他玩家看到的实体也要拉回同一位置。
	s.server.broadcastPlayerMove(s)
}

// fallDamageThreshold 是摔落伤害的下落阈值（原版：下落超过 3 格后
// 每多 1 格造成 1 点伤害）。
const fallDamageThreshold = 3.0

// updateFallState 以服务器端支撑面检测（脚底碰撞形状顶面）跟踪下落并结算
// 摔落伤害，不依赖客户端上报的着地标志。落点是水时不受伤害；
// 减伤按提供支撑面的方块计算（干草堆/床/粘液块/蜂蜜块/细雪）。
// 创造/旁观模式由 applyDamage 直接忽略。仅由会话读循环调用。
func (s *session) updateFallState(x, y, z float64) {
	top, surface, supported := s.server.world.SurfaceBelow(playerBox(x, y, z), 0.1)
	if !supported {
		if s.server.feetInWater(x, y, z) {
			// 落水/游泳中断下落，避免把“高处落水再上岸”算成摔落。
			s.airborne = false
			s.fallStartY = y
			return
		}
		if !s.airborne {
			s.airborne = true
			s.fallStartY = y
		} else if y > s.fallStartY {
			s.fallStartY = y
		}
		return
	}
	if !s.airborne {
		return
	}
	s.airborne = false
	fall := s.fallStartY - top
	if fall <= fallDamageThreshold {
		return
	}
	damage := float32(math.Ceil(fall - fallDamageThreshold))
	damage *= s.server.fallDamageMultiplier(surface)
	if damage > 0 {
		s.server.damagePlayer(s, damage, "a fall", 0, s.server.fallDamageTypeID, nil)
	}
}

// resetFallState 清除下落跟踪（传送/重生/拉回后调用，避免误判摔落伤害）。
// 腾空上升预算（airRise）不在此清零：它只在落地/入水时重置，
// 否则被回拉后“分段上升”可绕过飞行检测。
func (s *session) resetFallState() {
	s.airborne = false
}

// isFalling 报告玩家当前是否处于下落状态（用于跳跃暴击判定）。
// 仅由会话读循环调用（与 updateFallState 同 goroutine）。
func (s *session) isFalling() bool {
	return s.airborne
}

// addMovementExhaustion 依据位移与输入计算疲劳度（疾跑 0.1/格、跳跃 0.05/次）。
// 仅由会话读循环调用（sprinting 由读循环维护）。
func (s *session) addMovementExhaustion(distance float64, jumped bool) {
	exhaustion := float32(0)
	if s.sprinting {
		exhaustion += float32(distance) * playerExhaustionSprint
	}
	if jumped {
		exhaustion += playerExhaustionJump
	}
	if exhaustion > 0 {
		s.addExhaustion(exhaustion)
	}
}

// fallDamageMultiplier 返回落点方块对摔落伤害的倍率（默认 1；干草堆 0.2，
// 床 0.5，粘液块/蜂蜜块/细雪完全免疫）。
func (s *Server) fallDamageMultiplier(landed uint16) float32 {
	if factor, ok := s.fallDamageMultipliers[landed]; ok {
		return factor
	}
	return 1
}

// resolveFallDamageBlocks 解析摔落伤害减免方块的状态 ID。
// 按默认状态匹配：这些方块在当前实现中都只能以默认状态放置
// （旋转过的干草堆等状态的减免效果暂未覆盖，见 docs/TODO.md）。
func (s *Server) resolveFallDamageBlocks() {
	multipliers := make(map[uint16]float32)
	for name, factor := range map[string]float32{
		"minecraft:hay_block":   0.2,
		"minecraft:red_bed":     0.5,
		"minecraft:slime_block": 0,
		"minecraft:honey_block": 0,
		"minecraft:powder_snow": 0,
	} {
		if state, ok := registry.BlockStateIDs[name]; ok {
			multipliers[state] = factor
		}
	}
	s.fallDamageMultipliers = multipliers
}

// gameModeID 返回当前游戏模式。
func (s *session) gameModeID() uint8 {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.gameMode
}

// setGameMode 设置游戏模式。
func (s *session) setGameMode(mode uint8) {
	s.stateMu.Lock()
	s.gameMode = mode
	s.stateMu.Unlock()
}

// healthStatus 返回当前生命、饥饿度与饱食度。
func (s *session) healthStatus() (health float32, food int32, saturation float32) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.health, s.food, s.saturation
}

// applyDamage 扣减生命。处于死亡、无敌帧或创造/旁观模式时不生效。
// 返回扣减后的状态与是否生效、是否因此死亡。
func (s *session) applyDamage(amount float32, now time.Time) (health float32, food int32, saturation float32, applied, died bool) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.dead || s.gameMode == uint8(config.GameModeCreative) || s.gameMode == uint8(config.GameModeSpectator) {
		return s.health, s.food, s.saturation, false, false
	}
	if !s.lastHurt.IsZero() && now.Sub(s.lastHurt) < playerHurtCooldown {
		return s.health, s.food, s.saturation, false, false
	}
	s.lastHurt = now
	// 受伤会积累疲劳度（与原版一致：0.1）。
	s.exhaustion += playerExhaustionDamage
	s.health -= amount
	if s.health <= 0 {
		s.health = 0
		s.dead = true
		return s.health, s.food, s.saturation, true, true
	}
	return s.health, s.food, s.saturation, true, false
}

// addExhaustion 累加疲劳度并按原版规则消耗饥饿值。
// 由会话读循环调用（移动/攻击）或实体 Tick（通过 addExhaustionLocked）。
func (s *session) addExhaustion(amount float32) {
	s.stateMu.Lock()
	s.exhaustion += amount
	s.drainFoodLocked()
	s.stateMu.Unlock()
}

// drainFoodLocked 把累计到阈值的疲劳度折算为饥饿值（每 4 点疲劳消耗 1 点）。
// 调用方必须持有 stateMu。
func (s *session) drainFoodLocked() {
	for s.exhaustion >= playerExhaustionPerFood && s.food > 0 {
		s.exhaustion -= playerExhaustionPerFood
		s.food--
	}
	if s.food <= 0 {
		s.food = 0
		s.exhaustion = min(s.exhaustion, playerExhaustionPerFood)
	}
}

// playerHungerStatus 是 tickPlayerHunger 的结果。
type playerHungerStatus struct {
	health, saturation float32
	food               int32
	changed            bool
}

// applyHungerTick 推进饥饿系统一帧（每帧调用；内部按 80 tick 计时）：
//   - 饥饿值 ≥ 18 且未满血时自然恢复：优先消耗饱食度（更快），
//     否则每 4 秒恢复 1 点并积累疲劳；
//   - 饥饿值为 0 时每 4 秒受到 1 点饥饿伤害（普通难度）。
//
// 不再使用“脱战回血”的简化模型。
func (s *session) applyHungerTick() playerHungerStatus {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	status := playerHungerStatus{health: s.health, food: s.food, saturation: s.saturation}
	if s.dead || s.gameMode == uint8(config.GameModeCreative) || s.gameMode == uint8(config.GameModeSpectator) {
		return status
	}
	s.foodTimer++
	if s.foodTimer < playerFoodTickInterval {
		return status
	}
	s.foodTimer = 0
	switch {
	case s.food >= playerRegenFoodThreshold && s.health < maxPlayerHealth && s.health > 0:
		// 自然恢复：有饱食度时消耗饱食度（不消耗饥饿值），否则消耗饥饿值。
		if s.saturation > 0 {
			s.saturation = max(0, s.saturation-playerSaturationPerRegen)
		} else {
			s.exhaustion += playerExhaustionRegen
			s.drainFoodLocked()
		}
		s.health++
		status.changed = true
	case s.food <= 0 && s.health > 1:
		// 饥饿伤害：生命降到 1 为止（与普通难度一致）。
		s.health--
		status.changed = true
		if s.health < 1 {
			s.health = 1
		}
	}
	status.health, status.food, status.saturation = s.health, s.food, s.saturation
	return status
}

// eatFood 应用一次进食：恢复饥饿值与饱食度。返回是否实际进食。
func (s *session) eatFood(itemID int32) bool {
	food, ok := registry.Food(itemID)
	if !ok {
		return false
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.dead || s.food >= maxPlayerFood {
		return false
	}
	s.food = min(maxPlayerFood, s.food+food.Nutrition)
	s.saturation = min(float32(s.food), s.saturation+food.Saturation)
	return true
}

// addExperience 增加经验值并返回新的经验条状态（原版等级公式）。
func (s *session) addExperience(points int32) (bar float32, level, total int32) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.experience += points
	level, progress := experienceLevel(s.experience)
	s.experienceBar = progress
	return s.experienceBar, level, s.experience
}

// experienceStatus 返回当前经验条状态。
func (s *session) experienceStatus() (bar float32, level, total int32) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	level, progress := experienceLevel(s.experience)
	return progress, level, s.experience
}

// setSprinting 记录疾跑状态。
func (s *session) setSprinting(sprinting bool) {
	s.stateMu.Lock()
	s.sprinting = sprinting
	s.stateMu.Unlock()
}

// sprintingStatus 返回疾跑状态。
func (s *session) sprintingStatus() bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.sprinting
}

// experienceLevel 按原版公式把总经验换算为等级与经验条进度（0–1）。
func experienceLevel(total int32) (level int32, progress float32) {
	remaining := total
	for remaining >= experienceCost(level) {
		remaining -= experienceCost(level)
		level++
	}
	cost := experienceCost(level)
	if cost <= 0 {
		return level, 0
	}
	return level, float32(remaining) / float32(cost)
}

// experienceCost 返回从该等级升到下一级需要的经验（原版公式）。
func experienceCost(level int32) int32 {
	switch {
	case level >= 31:
		return 9*level - 158
	case level >= 16:
		return 5*level - 38
	default:
		return 2*level + 7
	}
}

// markRespawned 把死亡状态重置为满生命与满饥饿。返回 false 表示玩家未死亡。
func (s *session) markRespawned() bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if !s.dead {
		return false
	}
	s.dead = false
	s.health = maxPlayerHealth
	s.food = maxPlayerFood
	s.saturation = playerSaturation
	s.exhaustion = 0
	s.foodTimer = 0
	s.lastHurt = time.Time{}
	// 与原版一致：死亡会清空经验（不掉落经验球）。
	s.experience = 0
	s.experienceBar = 0
	return true
}

func (s *session) run() {
	if !s.runLogin() {
		return
	}
	if !s.runConfiguration() {
		return
	}
	s.runPlay()
}

// writeRawPacket 发送未压缩数据包，仅用于 Set Compression 之前的阶段。
func (s *session) writeRawPacket(packet []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return protocol.WritePacket(s.writer, packet)
}

// writeFrame 写出已构造好的完整帧字节（CompressPacketFrame 的结果）；
// 共享的广播包（区块数据/注册表）用它免去逐连接压缩。
func (s *session) writeFrame(frame []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return protocol.WriteFrameBytes(s.writer, frame)
}

// writePacket 以启用的压缩发送单个数据包；可被多个 goroutine 并发调用。
func (s *session) writePacket(packet []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return protocol.WritePacketWithCompression(s.writer, packet, compressionThreshold)
}

func (s *session) writePackets(packets ...[]byte) error {
	for _, packet := range packets {
		if err := s.writePacket(packet); err != nil {
			return err
		}
	}
	return nil
}

// tryWrite 尽力发送一个数据包；失败时关闭连接（读循环会随之退出）。
func (s *session) tryWrite(packet []byte) {
	if err := s.writePacket(packet); err != nil {
		_ = s.conn.Close()
	}
}

// readRawPacket 读取未压缩数据包（仅登录起始包使用）。
func (s *session) readRawPacket(timeout time.Duration) ([]byte, error) {
	if err := s.conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}
	return protocol.ReadPacket(s.reader)
}

// readPacket 重设读超时并读取一个压缩格式的数据包。
func (s *session) readPacket(timeout time.Duration) ([]byte, error) {
	if err := s.conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}
	return protocol.ReadPacketWithCompression(s.reader, compressionThreshold)
}

// runLogin 执行登录阶段：Login Start → Set Compression → Login Success → Login Acknowledged。
func (s *session) runLogin() bool {
	if s.protocolVersion != supportedProtocolVersion {
		slog.Info("rejecting client with unsupported protocol version",
			"remote", s.conn.RemoteAddr(), "protocol", s.protocolVersion, "supported", supportedProtocolVersion)
		return false
	}
	packet, err := s.readRawPacket(loginTimeout)
	if err != nil {
		return false
	}
	loginStart, err := protocol.ParseLoginStart(packet)
	if err != nil {
		slog.Debug("rejecting malformed login start", "remote", s.conn.RemoteAddr(), "error", err)
		return false
	}
	if !validUsername(loginStart.Name) {
		slog.Debug("rejecting invalid username", "remote", s.conn.RemoteAddr(), "name", loginStart.Name)
		return false
	}
	s.name = loginStart.Name
	// 离线模式：忽略客户端上报的 UUID，按原版规则从用户名推导。
	s.uuid = protocol.OfflineUUID(s.name)
	if s.server.config.OnlineMode {
		if !s.runOnlineAuthentication() {
			return false
		}
	}
	s.entityID = s.server.entityIDs.Add(1)

	if err := s.writeRawPacket(protocol.EncodeSetCompression(compressionThreshold)); err != nil {
		return false
	}
	if err := s.writePacket(protocol.EncodeLoginSuccessWithProperties(s.uuid, s.name, s.profileProperties)); err != nil {
		return false
	}

	packet, err = s.readPacket(loginTimeout)
	if err != nil {
		return false
	}
	if err := protocol.ParseLoginAcknowledged(packet); err != nil {
		slog.Debug("expected login acknowledged", "name", s.name, "error", err)
		return false
	}
	slog.Info("player logged in", "name", s.name, "remote", s.conn.RemoteAddr())
	return true
}

// runOnlineAuthentication 执行正版登录：发起加密请求、接收并解密响应，
// 然后通过会话服务器验证玩家身份。成功后用会话服务器的权威数据
// 覆盖 s.uuid、s.name 与 s.profileProperties。
func (s *session) runOnlineAuthentication() bool {
	verifyToken := make([]byte, 4)
	if _, err := rand.Read(verifyToken); err != nil {
		slog.Error("failed to generate verify token", "error", err)
		return false
	}
	publicKey, err := x509.MarshalPKIXPublicKey(&s.server.rsaKey.PublicKey)
	if err != nil {
		slog.Error("failed to marshal public key", "error", err)
		return false
	}
	if err := s.writeRawPacket(protocol.EncodeEncryptionRequest("", publicKey, verifyToken, true)); err != nil {
		return false
	}

	packet, err := s.readRawPacket(loginTimeout)
	if err != nil {
		return false
	}
	encryptedSecret, encryptedToken, err := protocol.ParseEncryptionResponse(packet)
	if err != nil {
		slog.Debug("rejecting malformed encryption response", "remote", s.conn.RemoteAddr(), "error", err)
		return false
	}
	sharedSecret, err := rsa.DecryptPKCS1v15(nil, s.server.rsaKey, encryptedSecret)
	if err != nil || len(sharedSecret) != 16 {
		slog.Debug("failed to decrypt shared secret", "remote", s.conn.RemoteAddr(), "error", err)
		return false
	}
	token, err := rsa.DecryptPKCS1v15(nil, s.server.rsaKey, encryptedToken)
	if err != nil || !bytes.Equal(token, verifyToken) {
		slog.Debug("encryption verify token mismatch", "remote", s.conn.RemoteAddr())
		return false
	}

	block, err := aes.NewCipher(sharedSecret)
	if err != nil {
		slog.Debug("failed to create cipher", "remote", s.conn.RemoteAddr(), "error", err)
		return false
	}
	// 加密从此生效：后续读写都经 AES/CFB8（IV 即共享密钥）。
	s.reader = protocol.NewStreamReader(s.conn, protocol.NewCFB8Decrypter(block, sharedSecret))
	s.writer = protocol.NewStreamWriter(s.conn, protocol.NewCFB8Encrypter(block, sharedSecret))

	profile, err := s.server.verifySession(s.name, protocol.ServerHash("", sharedSecret, publicKey))
	if err != nil {
		if errors.Is(err, errSessionRejected) {
			slog.Info("player failed session verification", "name", s.name, "remote", s.conn.RemoteAddr())
			// 尽力在已加密的通道上告知客户端，失败不影响断开。
			_ = s.writeRawPacket(protocol.EncodeLoginDisconnect("Failed to verify username!"))
		} else {
			slog.Error("session verification failed", "name", s.name, "remote", s.conn.RemoteAddr(), "error", err)
			// 会话服务器不可用：给出与"账号验证失败"不同的提示，便于排查。
			_ = s.writeRawPacket(protocol.EncodeLoginDisconnect("无法连接会话服务器，请稍后重试"))
		}
		return false
	}
	s.uuid = profile.UUID
	s.name = profile.Name
	s.profileProperties = profile.Properties
	return true
}

// runConfiguration 执行配置阶段：发送 Brand、功能开关与 Known Packs，
// 协商成功后同步注册表条目并等待客户端确认。
func (s *session) runConfiguration() bool {
	if err := s.writePackets(
		protocol.EncodeBrand(s.server.config.VersionName),
		protocol.EncodeFeatureFlags([]string{"minecraft:vanilla"}),
		protocol.EncodeKnownPacks([]protocol.KnownPack{corePack}),
	); err != nil {
		return false
	}

	for {
		packet, err := s.readPacket(configurationTimeout)
		if err != nil {
			return false
		}
		packetID, _, err := protocol.DecodeVarInt(packet)
		if err != nil {
			return false
		}
		switch packetID {
		case protocol.ConfigServerboundPacketIDKnownPacks:
			packs, err := protocol.ParseKnownPacksResponse(packet)
			if err != nil {
				slog.Debug("malformed known packs response", "name", s.name, "error", err)
				return false
			}
			if !containsCorePack(packs) {
				slog.Info("client is missing the vanilla 1.21.11 data pack; cannot complete configuration", "name", s.name)
				return false
			}
			if err := s.sendRegistries(); err != nil {
				return false
			}
			if err := s.writePacket(protocol.EncodeFinishConfiguration()); err != nil {
				return false
			}
			return s.awaitConfigurationFinished()
		case protocol.ConfigPacketIDClientInformation:
			if info, err := protocol.ParseClientInformation(packet); err == nil {
				s.setClientInfo(info)
			}
		default:
			// 忽略其余配置阶段数据包（Keep Alive、Pong 等）。
		}
	}
}

// sendRegistries 发送全部同步注册表的完整条目列表与标签。
// 客户端在配置阶段重建同步注册表：未发送的注册表会变为空（而不是回退内置数据），
// 因此即使 Known Packs 已提供条目定义，也必须逐个完整发送。
// 包内容对所有玩家一致，使用 New 时预编码的共享字节（只读），
// 多玩家加入不再重复分配/编码。
func (s *session) sendRegistries() error {
	for _, frame := range s.server.registryPackets {
		if err := s.writeFrame(frame); err != nil {
			return err
		}
	}
	return s.writeFrame(s.server.tagsPacket)
}

// buildRegistryPackets 预编码配置阶段的注册表与标签数据包（内容与会话无关）。
// 返回压缩后的完整帧：所有玩家共享同一份只读字节，加入风暴免去重复编码/压缩。
func buildRegistryPackets() (registryPackets [][]byte, tagsPacket []byte, err error) {
	for _, reg := range registry.Synchronized() {
		entries := make([]protocol.RegistryEntry, 0, len(reg.Entries))
		for _, name := range reg.Entries {
			entries = append(entries, protocol.RegistryEntry{Name: name})
		}
		frame, err := protocol.CompressPacketFrame(protocol.EncodeRegistryData(reg.Name, entries), compressionThreshold)
		if err != nil {
			return nil, nil, err
		}
		registryPackets = append(registryPackets, frame)
	}
	tagsPacket, err = protocol.CompressPacketFrame(protocol.EncodeUpdateTags(registryTags()), compressionThreshold)
	if err != nil {
		return nil, nil, err
	}
	return registryPackets, tagsPacket, nil
}

// registryTags 把全部需要发送的注册表标签转换为 Update Tags 包数据。
// 同步注册表与静态注册表（block、item 等）的标签都必须由服务器完整发送：
// 标签无法通过 Known Packs 获取，缺失会导致客户端解析本地数据失败。
func registryTags() []protocol.TagRegistry {
	synced := registry.AllTags()
	tags := make([]protocol.TagRegistry, 0, len(synced))
	for _, reg := range synced {
		entries := make([]protocol.TagEntry, 0, len(reg.Tags))
		for _, tag := range reg.Tags {
			entries = append(entries, protocol.TagEntry{Name: tag.Name, Entries: tag.Entries})
		}
		tags = append(tags, protocol.TagRegistry{Registry: reg.Name, Tags: entries})
	}
	return tags
}

// awaitConfigurationFinished 等待客户端的 Acknowledge Finish Configuration。
func (s *session) awaitConfigurationFinished() bool {
	for {
		packet, err := s.readPacket(configurationTimeout)
		if err != nil {
			return false
		}
		packetID, _, err := protocol.DecodeVarInt(packet)
		if err != nil {
			return false
		}
		switch packetID {
		case protocol.ConfigPacketIDFinishConfiguration:
			if err := protocol.ParseFinishConfigurationAck(packet); err != nil {
				return false
			}
			return true
		case protocol.ConfigPacketIDClientInformation:
			if info, err := protocol.ParseClientInformation(packet); err == nil {
				s.setClientInfo(info)
			}
		default:
			// 忽略其他数据包。
		}
	}
}

// runPlay 发送进入世界所需的初始数据包，注册到玩家列表，并进入游戏主循环。
func (s *session) runPlay() {
	s.teleportID = 1
	spawnX, spawnY, spawnZ := s.server.spawnPosition()
	// 恢复上次退出时的玩家数据（位置/生命/饥饿/游戏模式/物品栏）；
	// 没有记录时按新玩家处理（出生点 + 初始物品）。
	restored := false
	if record, ok := s.server.playerDataSnapshot(s.uuid); ok {
		s.applyPlayerRecord(record)
		restored = true
	} else {
		s.setPlayerPosition(spawnX, spawnY, spawnZ, 0, 0)
	}
	x, y, z, yaw, pitch := s.playerPosition()

	login := protocol.LoginPlayData{
		EntityID:            s.entityID,
		DimensionNames:      []string{"minecraft:overworld"},
		MaxPlayers:          int32(s.server.config.MaxPlayers),
		ViewDistance:        int32(s.server.config.ViewDistance),
		SimulationDistance:  int32(s.server.config.ViewDistance),
		EnableRespawnScreen: true,
		EnforcesSecureChat:  s.server.config.OnlineMode,
		Spawn:               s.server.spawnInfo(s.gameMode),
	}

	// 注册到玩家列表；离开时（任何返回路径）保存玩家数据并注销。
	others := s.server.registerPlayer(s)
	defer s.server.unregisterPlayer(s)
	defer s.server.savePlayerData(s)

	// 进入世界的前置包。
	packets := [][]byte{
		protocol.EncodeLoginPlay(login),
		protocol.EncodeSetDefaultSpawnPosition("minecraft:overworld",
			int(math.Floor(spawnX)), int(math.Floor(spawnY)), int(math.Floor(spawnZ)), 0, 0),
	}
	if s.server.borderHalfSize > 0 {
		// 世界边界（正方形，以 0,0 为中心；警告距离 5 格、警告时间 15 秒）。
		size := s.server.borderHalfSize * 2
		packets = append(packets, protocol.EncodeInitializeWorldBorder(0, 0, size, size, 0, 29999984, 5, 15))
	}
	centerX := int(math.Floor(x)) >> 4
	centerZ := int(math.Floor(z)) >> 4
	packets = append(packets,
		protocol.EncodeGameEvent(13, 0), // 开始等待区块
		protocol.EncodeSetCenterChunk(int32(centerX), int32(centerZ)),
	)
	if err := s.writePackets(packets...); err != nil {
		return
	}
	// 玩家周围视距内的全部区块（由近到远；跨区块移动时由 updateChunks 增量维护）。
	if err := s.syncChunks(centerX, centerZ); err != nil {
		slog.Error("failed to send spawn chunks", "name", s.name, "error", err)
		return
	}

	packets = [][]byte{
		protocol.EncodeSynchronizePlayerPosition(s.teleportID, x, y, z, 0, 0, 0, yaw, pitch),
		protocol.EncodePlayerInfoAddPlayer(s.uuid, s.name, s.profileProperties),
	}
	for _, other := range others {
		packets = append(packets, protocol.EncodePlayerInfoAddPlayer(other.uuid, other.name, other.profileProperties))
	}
	for _, other := range others {
		// 已上报聊天会话公钥的玩家：把会话信息也发给新玩家。
		if session := other.chatSessionSnapshot(); session != nil {
			packets = append(packets, protocol.EncodePlayerInfoChatSession(other.uuid, *session))
		}
	}
	for _, other := range others {
		// 其他玩家已在世界中的实体（皮肤来自上面的玩家列表档案属性，
		// 皮肤层显示掩码来自实体元数据）。
		packets = append(packets, s.server.playerSpawnPacket(other))
		packets = append(packets, s.server.playerSkinPacket(other))
	}
	packets = append(packets, protocol.EncodeSystemChat("Welcome to "+s.server.config.VersionName+"!"))
	// 向客户端声明命令树，使聊天栏支持 /help、/list、/say、/spawn、/gamemode。
	packets = append(packets, protocol.EncodeDeclareCommands(s.server.serverCommands(s)))
	if !restored {
		// 仅新玩家发放初始物品；恢复的玩家沿用其已保存的物品栏。
		packets = append(packets, s.giveStartingItems()...)
	}
	health, food, saturation := s.healthStatus()
	packets = append(packets, protocol.EncodeUpdateHealth(health, food, saturation))
	// 世界时间与经验条。
	packets = append(packets,
		protocol.EncodeUpdateTime(s.server.worldAge.Load(), s.server.worldAge.Load()%worldDayLength, true),
	)
	experienceBar, experienceLevel, experienceTotal := s.experienceStatus()
	packets = append(packets, protocol.EncodeSetExperience(experienceBar, experienceLevel, experienceTotal))
	if err := s.writePackets(packets...); err != nil {
		return
	}
	// 世界中已有的生物与掉落物也要发送给新玩家。
	if err := s.server.sendExistingEntities(s); err != nil {
		return
	}
	s.markJoined()
	// 通知其他玩家：新玩家加入（档案属性 + 实体）。
	s.server.broadcastPacketExcluding(protocol.EncodePlayerInfoAddPlayer(s.uuid, s.name, s.profileProperties), s)
	s.server.writeToNearbyPlayers(s.server.playerSpawnPacket(s), x, z, s)
	s.server.writeToNearbyPlayers(s.server.playerSkinPacket(s), x, z, s)
	s.server.broadcastPacketExcluding(protocol.EncodeSystemChat(s.name+" joined the game"), s)
	slog.Info("player joined the world", "name", s.name, "entityId", s.entityID)

	stop := make(chan struct{})
	var keepAliveWG sync.WaitGroup
	keepAliveWG.Add(1)
	go func() {
		defer keepAliveWG.Done()
		s.keepAliveLoop(stop)
	}()
	s.playReadLoop()
	close(stop)
	keepAliveWG.Wait()
	// 会话结束：保存并关闭可能仍打开的容器/合成窗口。
	s.server.closeContainer(s, false)
	s.server.closeCrafting(s, false)
	s.server.closeEnderChest(s, false)
	s.server.returnInventoryCraft(s)
	slog.Info("player disconnected", "name", s.name)
}

// giveStartingItems 把配置中的初始物品发放到快捷栏，返回同步给客户端的数据包。
// 支持 "minecraft:stone" 与 "minecraft:stone*64" 两种写法。
func (s *session) giveStartingItems() [][]byte {
	var packets [][]byte
	slot := item.SlotHotbarStart
	for _, entry := range s.server.config.StartingItems {
		if slot > item.SlotHotbarEnd {
			break
		}
		name, count := splitItemCount(entry)
		stack, err := item.FromName(name, count)
		if err != nil {
			slog.Warn("unknown starting item", "name", s.name, "item", entry, "error", err)
			continue
		}
		s.inventory.Set(slot, stack)
		packets = append(packets, protocol.EncodeSetPlayerInventory(int32(slot), stack.AppendSlot(nil)))
		slot++
	}
	return packets
}

// splitItemCount 解析初始物品配置："minecraft:stone"（默认 1 个）或
// "minecraft:stone*64"（显式数量，上限一栈 64）。
func splitItemCount(entry string) (string, int32) {
	name := strings.TrimSpace(entry)
	index := strings.LastIndex(name, "*")
	if index < 0 {
		return name, 1
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(name[index+1:]), 10, 32)
	if err != nil || parsed < 1 {
		return strings.TrimSpace(name[:index]), 1
	}
	if parsed > 64 {
		parsed = 64
	}
	return strings.TrimSpace(name[:index]), int32(parsed)
}

// keepAliveLoop 周期发送 Keep Alive；写失败时关闭连接以唤醒读循环。
func (s *session) keepAliveLoop(stop <-chan struct{}) {
	ticker := time.NewTicker(s.server.keepAliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			id := time.Now().UnixMilli()
			s.keepAliveMu.Lock()
			s.pendingKeepAlive = id
			s.keepAliveMu.Unlock()
			if err := s.writePacket(protocol.EncodeKeepAlivePlay(id)); err != nil {
				_ = s.conn.Close()
				return
			}
		}
	}
}

// playReadLoop 处理 Play 阶段的客户端数据包。
func (s *session) playReadLoop() {
	for {
		packet, err := s.readPacket(playTimeout)
		if err != nil {
			return
		}
		packetID, _, err := protocol.DecodeVarInt(packet)
		if err != nil {
			return
		}
		switch packetID {
		case protocol.PlayServerboundPacketIDKeepAlive:
			if id, err := protocol.ParsePlayKeepAlive(packet); err == nil {
				s.keepAliveMu.Lock()
				if s.pendingKeepAlive == id {
					s.pendingKeepAlive = 0
				}
				s.keepAliveMu.Unlock()
			}
		case protocol.PlayServerboundPacketIDChatMessage:
			if !s.chatBudget.allow(time.Now(), chatRate, chatBurst) {
				continue // 聊天洪泛：丢弃（防止广播放大）
			}
			message, err := protocol.ParseSignedChatMessage(packet)
			if err != nil {
				slog.Debug("malformed chat message", "name", s.name, "error", err)
				continue
			}
			s.server.broadcastChat(s, message.Message)
		case protocol.PlayServerboundPacketIDChatSessionUpdate:
			session, err := protocol.ParseChatSessionUpdate(packet)
			if err != nil {
				slog.Debug("malformed chat session update", "name", s.name, "error", err)
				continue
			}
			s.setChatSession(session)
			// 把会话公钥分发给其他玩家（Player Info 的 Initialize Chat），
			// 使客户端能够识别该玩家的聊天会话信息。
			s.server.broadcastPacketExcluding(protocol.EncodePlayerInfoChatSession(s.uuid, session), s)
		case protocol.PlayServerboundPacketIDChatCommand, protocol.PlayServerboundPacketIDChatCommandSigned:
			if !s.chatBudget.allow(time.Now(), chatRate, chatBurst) {
				continue // 命令洪泛：与聊天同桶限流
			}
			command, err := protocol.ParseChatCommand(packet)
			if err != nil {
				slog.Debug("malformed chat command", "name", s.name, "error", err)
				continue
			}
			s.server.handleCommand(s, command)
		case protocol.PlayServerboundPacketIDTabComplete:
			transactionID, text, err := protocol.ParseTabCompleteRequest(packet)
			if err != nil {
				continue
			}
			s.server.handleTabComplete(s, transactionID, text)
		case protocol.PlayServerboundPacketIDConfirmTeleportation:
			if id, err := protocol.ParseConfirmTeleportation(packet); err == nil && id == s.teleportID {
				slog.Debug("player confirmed teleport", "name", s.name, "teleportId", id)
			}
		case protocol.PlayServerboundPacketIDInteract:
			targetID, action, err := protocol.ParseInteract(packet)
			if err != nil {
				slog.Debug("malformed interact packet", "name", s.name, "error", err)
				continue
			}
			if action == protocol.InteractActionAttack {
				s.server.handleAttack(s, targetID)
			}
		case protocol.PlayServerboundPacketIDClientCommand:
			if action, err := protocol.ParsePlayClientCommand(packet); err == nil && action == 0 {
				// 0 = 重生请求。
				s.server.respawnPlayer(s)
			}
		case protocol.PlayServerboundPacketIDChunkBatchReceived:
			// 客户端回报期望的每 tick 区块数。当前仅记录：区块仍按批立即发送
			// （与原版的按回报节流不同，见 docs/TODO.md 已知限制）。
			if rate, err := protocol.ParseChunkBatchReceived(packet); err == nil {
				s.desiredChunksPerTick = rate
			}
		case protocol.PlayServerboundPacketIDPlayerPosition:
			x, y, z, _, err := protocol.ParsePlayerPosition(packet)
			if err != nil || !validPlayerY(y) {
				continue
			}
			s.handleMove(x, y, z, 0, 0, false, time.Now())
		case protocol.PlayServerboundPacketIDPlayerPositionRotation:
			x, y, z, yaw, pitch, _, err := protocol.ParsePlayerPositionRotation(packet)
			if err != nil || !validPlayerY(y) {
				continue
			}
			s.handleMove(x, y, z, yaw, pitch, true, time.Now())
		case protocol.PlayServerboundPacketIDPlayerRotation:
			yaw, pitch, err := protocol.ParsePlayerRotation(packet)
			if err != nil {
				continue
			}
			if !s.moveBudget.allow(time.Now(), moveRate, moveBurst) {
				continue
			}
			s.setPlayerRotation(yaw, pitch)
			s.server.broadcastPlayerMove(s)
		case protocol.PlayServerboundPacketIDPlayerAction:
			action, err := protocol.ParsePlayerAction(packet)
			if err != nil {
				continue
			}
			if !s.actionBudget.allow(time.Now(), actionRate, actionBurst) {
				continue // 交互洪泛（挖掘/丢弃）
			}
			s.server.handlePlayerAction(s, action)
		case protocol.PlayServerboundPacketIDUseItemOn:
			use, err := protocol.ParseUseItemOn(packet)
			if err != nil {
				continue
			}
			if !s.actionBudget.allow(time.Now(), actionRate, actionBurst) {
				continue // 交互洪泛（放置/使用）
			}
			s.server.handleUseItemOn(s, use)
		case protocol.PlayServerboundPacketIDSetCarriedItem:
			if slot, err := protocol.ParseSetCarriedItem(packet); err == nil {
				s.server.handleSetCarriedItem(s, slot)
			}
		case protocol.PlayServerboundPacketIDSetCreativeSlot:
			slot, itemID, count, err := protocol.ParseSetCreativeSlot(packet)
			if err != nil {
				continue
			}
			s.server.handleSetCreativeSlot(s, slot, itemID, count)
		case protocol.PlayServerboundPacketIDSwingArm:
			if err := protocol.ParseSwingArm(packet); err == nil {
				s.server.broadcastSwing(s)
			}
		case protocol.PlayServerboundPacketIDContainerClick:
			click, err := protocol.ParseContainerClick(packet)
			if err != nil {
				slog.Debug("malformed container click", "name", s.name, "error", err)
				continue
			}
			s.server.handleContainerClick(s, click)
		case protocol.PlayServerboundPacketIDContainerClose:
			windowID, err := protocol.ParseContainerClose(packet)
			if err != nil {
				continue
			}
			if s.openContainer != nil && windowID == s.windowID {
				s.server.closeContainer(s, false)
			}
			if s.getOpenCraft() != nil && windowID == s.windowID {
				s.server.closeCrafting(s, false)
			}
			if s.getOpenEnder() && windowID == s.windowID {
				s.server.closeEnderChest(s, false)
			}
		case protocol.PlayServerboundPacketIDPlayerInput:
			if flags, err := protocol.ParsePlayerInput(packet); err == nil {
				s.sneaking = flags&protocol.PlayerInputShift != 0
				s.setSprinting(flags&protocol.PlayerInputSprint != 0)
			}
		case protocol.PlayServerboundPacketIDUseItem:
			// 使用物品（进食等）：手持食物且未满饥饿时立即食用。
			// 说明：未实现原版 1.6 秒的进食过程（无进度条与中断处理）。
			s.server.handleUseItem(s)
		case protocol.PlayServerboundPacketIDClientInformation:
			if info, err := protocol.ParseClientInformation(packet); err == nil {
				oldInfo := s.setClientInfo(info)
				// 皮肤层掩码变化时同步给附近玩家（Entity Metadata 索引 16）。
				if oldInfo.SkinParts != info.SkinParts {
					x, _, z, _, _ := s.playerPosition()
					s.server.writeToNearbyPlayers(
						protocol.EncodeEntityMetadataSkinParts(s.entityID, info.SkinParts), x, z, s)
				}
			}
		default:
			// 其他数据包（输入、快捷栏切换等）暂未实现，忽略。
		}
	}
}

// getOpenCraft 返回打开的工作台合成状态（线程安全）。
func (s *session) getOpenCraft() *craftingState {
	s.stateMu.Lock()
	state := s.openCraft
	s.stateMu.Unlock()
	return state
}

// setOpenCraft 设置打开的工作台合成状态（线程安全）。
func (s *session) setOpenCraft(state *craftingState) {
	s.stateMu.Lock()
	s.openCraft = state
	s.stateMu.Unlock()
}

// invCraftState 返回窗口 0 的 2×2 合成状态；不存在时按需创建（线程安全）。
func (s *session) invCraftState() *craftingState {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.invCraft == nil {
		s.invCraft = newCraftingState(2)
	}
	return s.invCraft
}

// peekInvCraft 返回窗口 0 的 2×2 合成状态（可能为 nil，不创建）。
func (s *session) peekInvCraft() *craftingState {
	s.stateMu.Lock()
	state := s.invCraft
	s.stateMu.Unlock()
	return state
}

// clearInvCraft 取出并清空窗口 0 的合成状态（线程安全）。
func (s *session) clearInvCraft() *craftingState {
	s.stateMu.Lock()
	state := s.invCraft
	s.invCraft = nil
	s.stateMu.Unlock()
	return state
}

// getOpenEnder 返回末影箱窗口是否打开（线程安全）。
func (s *session) getOpenEnder() bool {
	s.stateMu.Lock()
	open := s.openEnder
	s.stateMu.Unlock()
	return open
}

// setOpenEnder 设置末影箱窗口是否打开（线程安全）。
func (s *session) setOpenEnder(open bool) {
	s.stateMu.Lock()
	s.openEnder = open
	s.stateMu.Unlock()
}

// getCursor 返回鼠标光标持有的物品（线程安全）。
func (s *session) getCursor() item.Stack {
	s.stateMu.Lock()
	cursor := s.cursor
	s.stateMu.Unlock()
	return cursor
}

// setCursor 设置鼠标光标持有的物品（线程安全）。
func (s *session) setCursor(cursor item.Stack) {
	s.stateMu.Lock()
	s.cursor = cursor
	s.stateMu.Unlock()
}

// setClientInfo 更新客户端信息并返回旧值（线程安全：
// 皮肤掩码会被其他会话的生成路径读取）。
func (s *session) setClientInfo(info protocol.ClientInformation) protocol.ClientInformation {
	s.stateMu.Lock()
	old := s.clientInfo
	s.clientInfo = info
	s.stateMu.Unlock()
	return old
}

// skinParts 返回客户端上报的皮肤层显示掩码（线程安全）。
func (s *session) skinParts() uint8 {
	s.stateMu.Lock()
	parts := s.clientInfo.SkinParts
	s.stateMu.Unlock()
	return parts
}

// validPlayerY 拒绝世界范围之外的坐标（简单的服务端校验）。
func validPlayerY(y float64) bool {
	return y >= world.WorldMinY-16 && y <= world.WorldMinY+world.WorldHeight+16
}

// validUsername 校验离线模式玩家名：1-16 个字符，只允许字母、数字与下划线
// （与 1.21.11 服务端的用户名限制一致）。
func validUsername(name string) bool {
	if len(name) == 0 || len(name) > 16 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return true
}

// containsCorePack 检查客户端上报的 Known Packs 是否包含本服务器的原版数据包。
func containsCorePack(packs []protocol.KnownPack) bool {
	for _, pack := range packs {
		if pack.Namespace == corePack.Namespace && pack.ID == corePack.ID && pack.Version == corePack.Version {
			return true
		}
	}
	return false
}
