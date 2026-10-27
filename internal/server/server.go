//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"gmcs/internal/config"
	"gmcs/internal/logging"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

const clientTimeout = 5 * time.Second

// defaultTickInterval 是实体 Tick 的默认间隔（20 TPS）。
const defaultTickInterval = 50 * time.Millisecond

type Server struct {
	config  config.Config
	clients chan struct{}

	// worlds 是各维度的世界（惰性创建）；worldMu 保护 worlds 与 spawns。
	// 读多写少（tick 热路径按实体取世界），使用读写锁。
	// worldsCache 是 worlds 的无锁读快照（见 publishWorldsCacheLocked）。
	worldMu     sync.RWMutex
	worlds      map[world.Dimension]*world.World
	worldsCache atomic.Pointer[[3]*world.World]
	spawns      map[world.Dimension]spawnPoint

	// 实体数据按维度惰性加载（维度世界首次创建时加载该维度的 entities.json）。
	entityLoadMu   sync.Mutex
	entitiesLoaded map[world.Dimension]bool

	// keepAliveInterval 等测试钩子之外的最小构造同样可用。
	mu      sync.Mutex
	conns   map[net.Conn]struct{}
	players map[[16]byte]*session
	// playersSnap 是在线玩家列表的无锁快照（每次进出时重建）：
	// 写缓冲 drain 在读循环每个包后遍历它（零分配零锁）。
	playersSnap atomic.Pointer[[]*session]
	wg          sync.WaitGroup
	closing     bool

	entityIDs         atomic.Int32
	keepAliveInterval time.Duration
	// worldAge 是世界时间（tick，每次 tick 递增；用于昼夜与 Update Time）。
	worldAge atomic.Int64

	// 实体状态：mobs、items 与各自的字段受 entityMu 保护。
	entityMu   sync.Mutex
	mobs       map[int32]*mob
	items      map[int32]*itemEntity
	spawnTicks int
	randState  atomic.Uint64
	// entityTrack 是各玩家已下发的实体集合（为靠近的玩家补发实体、
	// 为离开范围的实体补发移除；见 entity_tracking.go），受 entityMu 保护。
	entityTrack map[*session]*sessionTracker

	// tickInterval 是实体 Tick 间隔；<= 0 时禁用后台 Tick（测试手动驱动）。
	tickInterval time.Duration

	// chunkUnloadInterval 是区块内存卸载扫描的周期；<= 0 时禁用后台周期卸载
	// （测试通过显式调用 unloadFarChunks 驱动，保证断言不被周期卸载干扰）。
	chunkUnloadInterval time.Duration
	// chunkUnloadGrace 是区块卸载宽限期（见 unloadFarChunks）：区块连续
	// 远离所有玩家超过该时长才真正卸载，避免玩家在卸载边界来回移动时
	// 反复加载/卸载/写盘；<= 0 时立即卸载。
	chunkUnloadGrace time.Duration
	// dimensionIdleGrace 是“无已加入玩家的已打开维度”释放全部区块前的
	// 空闲宽限（玩家切维度/传送后旧维度短期保留，快速切回无需重建）。
	dimensionIdleGrace time.Duration
	// dimIdleSince 记录各维度最近一次“无已加入玩家”的时刻（受 dimIdleMu 保护）。
	dimIdleMu    sync.Mutex
	dimIdleSince map[world.Dimension]time.Time

	// unloadNow 由最后一名玩家退出时唤醒（容量 1、non-blocking），
	// 使空闲服务器立即释放区块资源而不必等待下一个扫描周期。
	unloadNow chan struct{}

	// playerAutosaveInterval 是玩家数据的周期保存间隔；<= 0 时禁用
	// （测试可显式调用 saveAllPlayerData 驱动）。
	playerAutosaveInterval time.Duration

	// 出生点（世界坐标，脚部位置）；按维度惰性计算。
	// borderHalfSize 是世界边界的半边长（方块）；0 表示未启用边界。
	borderHalfSize float64
	// defaultGameMode 是新玩家的游戏模式。
	defaultGameMode uint8
	// mobsEnabled 为 false 时禁用全部生物逻辑（注册表数据缺失时）。
	mobsEnabled bool
	// 玩家实体同步（注册表缺少玩家实体类型时禁用）。
	playerTypeID          int32
	playerEntitiesEnabled bool
	// 生物/伤害系统使用的注册表 ID（按种类索引）。
	zombieTypeID             int32
	mobTypeIDs               map[mobKind]int32
	mobHurtSounds            map[mobKind]int32
	mobDeathSounds           map[mobKind]int32
	mobAttackDamageTypeID    int32
	playerAttackDamageTypeID int32
	fallDamageTypeID         int32
	arrowDamageTypeID        int32
	explosionDamageTypeID    int32
	// 环境伤害（岩浆/着火/溺水/岩浆块/仙人掌）。
	lavaDamageTypeID     int32
	fireDamageTypeID     int32
	drownDamageTypeID    int32
	hotFloorDamageTypeID int32
	cactusDamageTypeID   int32
	soundMobHurt         int32
	soundMobDeath        int32
	soundPlayerHurt      int32
	soundShieldBlock     int32

	// 箭矢系统（骷髅的远程攻击；与生物系统一同启用/禁用）。
	arrowTypeID        int32
	soundArrowShoot    int32
	soundArrowHit      int32
	soundArrowHitBlock int32
	soundCreeperPrime  int32
	arrowsEnabled      bool
	// arrows 是飞行中的箭矢（受 entityMu 保护）。
	arrows map[int32]*arrowEntity

	// TNT 实体（发射器/打火石点燃；注册表缺少时禁用）。
	tntTypeID     int32
	soundTntPrime int32
	tntsEnabled   bool
	// tnts 是点燃中的 TNT（受 entityMu 保护）。
	tnts map[int32]*primedTnt

	// 传送门音效（点燃/激活/传送；New 中解析）。
	soundFlintUse           int32
	soundEndPortalFrameFill int32
	soundEndPortalSpawn     int32
	soundPortalTravel       int32
	// soundEndermanTeleport 是末影人传送音效（缺失时静默跳过）。
	soundEndermanTeleport int32

	// 掉落物系统（注册表缺少物品实体类型或拾取音效时禁用）。
	itemTypeID      int32
	soundItemPickup int32
	itemsEnabled    bool

	// 经验球系统（注册表缺少经验球实体类型时禁用）；xpOrbs 受 entityMu 保护。
	xpOrbTypeID   int32
	xpOrbsEnabled bool
	xpOrbs        map[int32]*xpOrb

	// 雷暴闪电（注册表缺少闪电实体/音效/伤害类型时禁用）；
	// lightningBolts 是仍在显示的闪电实体（受 entityMu 保护，见 lightning.go）。
	lightningTypeID       int32
	soundLightningThunder int32
	soundLightningImpact  int32
	lightningDamageTypeID int32
	lightningEnabled      bool
	lightningBolts        map[int32]*lightningBolt

	// precipitationCursor 是群系降水抽样的玩家轮转游标（只由主 tick 线程访问，
	// 见 precipitation.go）。
	precipitationCursor int

	// 漏斗传输：hoppers 是已注册的漏斗运行时状态（受 hopperMu 保护）；
	// containerTransferMu 串行化“从方块实体载入容器内容”与漏斗搬运事务。
	hopperMu            sync.Mutex
	hoppers             map[containerKey]*hopperState
	hopperTypeID        int32
	hoppersEnabled      bool
	hopperScanCounter   int
	containerTransferMu sync.Mutex
	// gameTicks 是单调递增的游戏刻计数（漏斗错峰规则用）。
	gameTicks atomic.Int64
	// 堆肥桶定时转换：composterDue 是待转换堆肥桶的到期表（受 composterMu
	// 保护）；composterScanCounter 是 level-7 兜底扫描计数。
	composterMu          sync.Mutex
	composterDue         map[composterKey]int64
	composterScanCounter int
	// 农业：crops/farmlands 是已登记的作物与耕地位置（受 farmMu 保护）；
	// 两个 scratch 切片是 tick 热路径复用的键快照（仅 tick goroutine 使用）。
	farmMu              sync.Mutex
	crops               map[containerKey]struct{}
	farmlands           map[containerKey]struct{}
	farmCropScratch     []containerKey
	farmFarmlandScratch []containerKey
	// soundHoeTill/soundCropPlant/soundBoneMealUse 是农业音效（缺失时为 0，静默）。
	soundHoeTill     int32
	soundCropPlant   int32
	soundBoneMealUse int32

	// 天气（见 weather.go）：weather 受 weatherMu 保护（tick goroutine 写、
	// 命令与登录路径读）；rainingFlag 是只读快照，供实体环境扫描等热路径
	// 无锁查询（每只生物每 tick 都会问一次）；fxRand 是与玩法随机流
	// （randState）分离的确定性随机源，天气与音高抖动共用，避免改变
	// 既有测试依赖的 nextRandom 序列；weatherEnabled 为 false 时
	// 完全不推进天气（测试通过 joinServer 关闭，保证既有用例的确定性）。
	weatherMu      sync.Mutex
	weather        weatherState
	rainingFlag    atomic.Bool
	fxRand         atomic.Uint64
	weatherEnabled bool
	// tickScratch 是 tick 热路径复用的临时切片（由单线程 tick 驱动填充，
	// 见 tickScratch 的说明）。
	tickScratch tickScratch

	// 门/活板门/栅栏门开合音效（按 BlockSetType 分组；缺失时为 0，静默）。
	soundDoorOpen       [openableSoundGroups]int32
	soundDoorClose      [openableSoundGroups]int32
	soundTrapdoorOpen   [openableSoundGroups]int32
	soundTrapdoorClose  [openableSoundGroups]int32
	soundFenceGateOpen  int32
	soundFenceGateClose int32

	// 压力板点击音效（按木质/石质/金属分组；缺失时播放时静默跳过）。
	plateClickOn  [plateSoundGroups]int32
	plateClickOff [plateSoundGroups]int32

	// 容器系统：containerDefs 是方块名 → 容器定义（注册表数据缺失时为空）；
	// 红石：计划刻表、火把熔断统计与 tick 计数（均受 redstoneMu 保护）。
	redstoneMu        sync.Mutex
	redstoneTicks     map[containerKey]int
	torchToggles      map[containerKey][]int64
	redstoneTickCount int64

	// 流体（水/岩浆）计划刻表（受 fluidMu 保护，见 fluids.go）。
	fluidMu    sync.Mutex
	fluidTicks map[containerKey]int

	// containers 是已打开容器（按维度 + 方块坐标索引），受 containerMu 保护。
	containerDefs map[string]*containerDef
	containerMu   sync.Mutex
	containers    map[containerKey]*containerState

	// 熔炉系统：furnaces 是已注册的熔炉运行时状态（按维度 + 方块坐标索引），
	// 受 furnaceMu 保护；cookKinds 是方块实体类型 → 烹饪类型映射。
	furnaceMu          sync.Mutex
	furnaces           map[containerKey]*furnaceState
	cookKinds          map[int32]registry.CookingKind
	furnaceScanCounter int

	// 酿造台系统：brewings 是已注册的酿造台运行时状态（按维度 + 方块坐标索引），
	// 受 brewingMu 保护。
	brewingMu          sync.Mutex
	brewings           map[containerKey]*brewingState
	brewingScanCounter int

	// fallDamageMultipliers 是落点方块的摔落伤害倍率（New 中解析）。
	fallDamageMultipliers map[uint16]float32

	// recipeBook 是启动时构建的配方书共享数据（注册表缺失时为 nil）。
	recipeBook *recipeBook

	// 配置阶段共享的预编码数据包（内容与玩家无关，只读共享）。
	registryPackets [][]byte
	tagsPacket      []byte

	// chunkPackets 是跨玩家共享的区块数据包缓存（见 chunkPacket）。
	chunkPackets chunkPacketCache

	// rsaKey 用于正版登录的加密握手（仅在线模式生成）。
	rsaKey *rsa.PrivateKey
	// httpClient 用于访问会话服务器。
	httpClient *http.Client

	// 玩家数据（players.json）：受 playerDataMu 保护，按 UUID 索引。
	playerDataMu sync.Mutex
	playerData   map[[16]byte]playerRecord

	// permissionIndex 是 players → 权限节点的索引（玩家名小写；
	// "*" 条目对所有玩家生效）。在 New 中从配置构建。
	permissionIndex map[string][]string

	// audit 是审计日志（聊天/命令/玩家进出，JSONL 格式）；
	// auditClose 在服务器关闭时关闭文件。
	audit      *slog.Logger
	auditClose io.Closer
}

// spawnPoint 是一个维度的出生点（脚部世界坐标）。
type spawnPoint struct {
	x, y, z float64
}

// worldFor 返回维度对应的世界；未创建时惰性创建（下界/末地按需加载，
// 避免不进入这些维度的服务器/测试承担启动成本）。无法创建时回退主世界。
// 首次创建维度时加载该维度的实体数据。
func (s *Server) worldFor(dim world.Dimension) *world.World {
	// 快路径：已创建的维度直接返回（tick 热路径每个实体每帧调用一次）。
	// 走无锁快照，避免每实体一次 RWMutex；快照在创建/替换维度时重建。
	if cache := s.worldsCache.Load(); cache != nil && int(dim) < len(cache) {
		if existing := cache[dim]; existing != nil {
			return existing
		}
	}
	s.worldMu.RLock()
	if existing, ok := s.worlds[dim]; ok {
		s.worldMu.RUnlock()
		return existing
	}
	s.worldMu.RUnlock()
	s.worldMu.Lock()
	_, existed := s.worlds[dim]
	gameWorld := s.worldForLocked(dim)
	s.worldMu.Unlock()
	if !existed && gameWorld != nil && s.config.WorldDir != "" && gameWorld.Dimension() == dim {
		s.ensureEntitiesLoaded(dim)
	}
	return gameWorld
}

// publishWorldsCacheLocked 在持有 worldMu 时重建 worldFor 的无锁读快照。
// 快照只包含已创建的维度（nil 槽走慢路径，语义不变）。
func (s *Server) publishWorldsCacheLocked() {
	var cache [3]*world.World
	for dim, gameWorld := range s.worlds {
		if int(dim) < len(cache) {
			cache[dim] = gameWorld
		}
	}
	s.worldsCache.Store(&cache)
}

// ensureEntitiesLoaded 首次访问维度时加载该维度的持久化实体（幂等）。
func (s *Server) ensureEntitiesLoaded(dim world.Dimension) {
	if !s.mobsEnabled && !s.itemsEnabled {
		return
	}
	s.entityLoadMu.Lock()
	defer s.entityLoadMu.Unlock()
	if s.entitiesLoaded == nil {
		s.entitiesLoaded = make(map[world.Dimension]bool, 3)
	}
	if s.entitiesLoaded[dim] {
		return
	}
	s.entitiesLoaded[dim] = true
	if err := s.loadEntitiesFor(dim); err != nil {
		slog.Error("加载实体数据失败", "dimension", dim, "error", err)
	}
}

// worldForLocked 在持有 worldMu 时获取/创建维度世界。
// 世界目录为空（测试中的轻量服务器）或创建失败时返回 nil / 已加载的主世界。
func (s *Server) worldForLocked(dim world.Dimension) *world.World {
	if existing, ok := s.worlds[dim]; ok {
		return existing
	}
	if s.config.WorldDir == "" {
		return nil // 未配置世界目录（仅命令/权限测试的轻量服务器）
	}
	if s.worlds == nil {
		s.worlds = make(map[world.Dimension]*world.World)
	}
	dir := s.config.WorldDir
	if sub := world.DimensionDir(dim); sub != "" {
		dir = filepath.Join(dir, sub)
	}
	gameWorld, err := world.OpenDimension(dir, world.NewGenerator(dim, s.config.WorldSeed), dim)
	if err != nil {
		slog.Error("打开维度世界失败，回退到主世界", "dimension", dim, "error", err)
		return s.worlds[world.DimensionOverworld]
	}
	// 区块加载跟踪：新加载的区块里可能有漏斗，需要在加载后立即注册。
	gameWorld.EnableChunkLoadTracking()
	s.worlds[dim] = gameWorld
	s.publishWorldsCacheLocked()
	slog.Info("dimension loaded", "dimension", dim, "dir", dir)
	return gameWorld
}

// spawnPositionFor 返回维度出生点（脚部世界坐标），惰性计算并缓存。
func (s *Server) spawnPositionFor(dim world.Dimension) (x, y, z float64) {
	s.worldMu.Lock()
	defer s.worldMu.Unlock()
	if point, ok := s.spawns[dim]; ok {
		return point.x, point.y, point.z
	}
	if s.spawns == nil {
		s.spawns = make(map[world.Dimension]spawnPoint)
	}
	gameWorld := s.worldForLocked(dim)
	if gameWorld == nil {
		return 0, world.FlatSpawnY, 0
	}
	x, y, z, ok := gameWorld.FindSpawn(s.borderHalfSize)
	if !ok {
		// 搜索范围内没有可用地势：回退到原点上方（生成器给出的高度）。
		x, y, z = 0.5, float64(gameWorld.SurfaceY(0, 0)+1), 0.5
		slog.Warn("未在搜索范围内找到出生点，回退到原点", "dimension", dim)
	}
	s.spawns[dim] = spawnPoint{x: x, y: y, z: z}
	return x, y, z
}

// closeWorlds 关闭全部已加载的维度世界（保存区块）。
func (s *Server) closeWorlds() error {
	s.worldMu.Lock()
	worlds := make([]*world.World, 0, len(s.worlds))
	for _, gameWorld := range s.worlds {
		worlds = append(worlds, gameWorld)
	}
	s.worldMu.Unlock()
	var firstErr error
	for _, gameWorld := range worlds {
		if err := gameWorld.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// autosaveLoop 周期保存全部已加载维度世界，直到 ctx 取消。
func (s *Server) autosaveLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.worldMu.Lock()
			worlds := make([]*world.World, 0, len(s.worlds))
			for _, gameWorld := range s.worlds {
				worlds = append(worlds, gameWorld)
			}
			s.worldMu.Unlock()
			for _, gameWorld := range worlds {
				if err := gameWorld.Flush(); err != nil {
					slog.Error("autosave failed", "error", err)
				}
			}
			// 世界时间与天气（原版写入主世界 level.dat）。
			if err := s.saveLevelState(); err != nil {
				slog.Error("保存世界级别状态失败", "error", err)
			}
		}
	}
}

func New(cfg config.Config) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	gameMode, ok := config.GameModeID(cfg.GameMode)
	if !ok {
		return nil, fmt.Errorf("未知游戏模式 %q", cfg.GameMode)
	}
	gameWorld, err := world.OpenDimension(cfg.WorldDir, world.NewGenerator(world.DimensionOverworld, cfg.WorldSeed), world.DimensionOverworld)
	if err != nil {
		return nil, err
	}
	// 区块加载跟踪：新加载的区块里可能有漏斗，需要在加载后立即注册。
	gameWorld.EnableChunkLoadTracking()
	// 在线模式：为加密握手生成服务器密钥对（1024 位，与客户端兼容）。
	var rsaKey *rsa.PrivateKey
	if cfg.OnlineMode {
		key, err := rsa.GenerateKey(rand.Reader, 1024)
		if err != nil {
			// 世界已启动预生成 worker：初始化失败时必须关闭，避免 goroutine 泄漏。
			_ = gameWorld.Close()
			return nil, fmt.Errorf("生成 RSA 密钥：%w", err)
		}
		rsaKey = key
	}
	server := &Server{
		config:                 cfg,
		clients:                make(chan struct{}, cfg.MaxConnections),
		conns:                  make(map[net.Conn]struct{}),
		players:                make(map[[16]byte]*session),
		keepAliveInterval:      defaultKeepAliveInterval,
		mobs:                   make(map[int32]*mob),
		items:                  make(map[int32]*itemEntity),
		xpOrbs:                 make(map[int32]*xpOrb),
		entityTrack:            make(map[*session]*sessionTracker),
		hoppers:                make(map[containerKey]*hopperState),
		tickInterval:           defaultTickInterval,
		chunkUnloadInterval:    defaultChunkUnloadInterval,
		chunkUnloadGrace:       defaultChunkUnloadGrace,
		dimensionIdleGrace:     defaultDimensionIdleGrace,
		unloadNow:              make(chan struct{}, 1),
		playerAutosaveInterval: defaultPlayerAutosaveInterval,
		borderHalfSize:         float64(cfg.WorldBorderSize) / 2,
		defaultGameMode:        uint8(gameMode),
		rsaKey:                 rsaKey,
		httpClient:             &http.Client{Timeout: 10 * time.Second},
		playerData:             make(map[[16]byte]playerRecord),
		containers:             make(map[containerKey]*containerState),
		permissionIndex:        buildPermissionIndex(cfg),
		worlds:                 map[world.Dimension]*world.World{world.DimensionOverworld: gameWorld},
		spawns:                 make(map[world.Dimension]spawnPoint),
		weatherEnabled:         true,
	}
	server.fxRand.Store(fxRandDefaultSeed)
	// 预计算主世界出生点（进入世界的第一个玩家必须立即得到可用坐标）。
	server.spawnPositionFor(world.DimensionOverworld)
	server.resolveMobRegistryIDs()
	server.resolvePlayerEntityType()
	server.resolveItemRegistryIDs()
	server.resolveXPOrbRegistryIDs()
	server.resolveHopperRegistryIDs()
	server.resolveTntRegistryIDs()
	server.resolveLightningIDs()
	server.resolvePlateSounds()
	server.resolveOpenableSounds()
	server.resolveFallDamageBlocks()
	server.resolveContainerDefs()
	server.initFurnaceKinds()
	server.buildRecipeBook()
	server.furnaces = make(map[containerKey]*furnaceState)
	server.brewings = make(map[containerKey]*brewingState)
	server.initBrewingItems()
	bucketItemID, bucketLavaItemID = initFurnaceItems()
	if server.registryPackets, server.tagsPacket, err = buildRegistryPackets(); err != nil {
		// 世界预生成 worker 已启动，构造注册表失败时同样需要释放。
		_ = gameWorld.Close()
		return nil, fmt.Errorf("构造注册表数据包：%w", err)
	}
	if len(server.containerDefs) == 0 {
		slog.Warn("容器交互已禁用：注册表数据缺失")
	}
	if !server.mobsEnabled {
		slog.Warn("生物系统已禁用：注册表数据缺失")
	}
	if !server.itemsEnabled {
		slog.Warn("掉落物系统已禁用：注册表数据缺失")
	}
	if !server.xpOrbsEnabled {
		slog.Warn("经验球系统已禁用：注册表数据缺失")
	}
	if !server.hoppersEnabled {
		slog.Warn("漏斗传输已禁用：注册表数据缺失")
	}
	// 恢复世界时间与天气（level.json；不存在时从 0 开始，与原版新世界一致）。
	if err := server.loadLevelState(); err != nil {
		slog.Error("加载世界级别状态失败", "error", err)
	}
	// 恢复上次保存的生物与掉落物（各维度的 entities.json；不存在时不做任何事）。
	// 下界/末地在对应维度第一次被访问时加载（见 worldFor/ensureEntitiesLoaded）。
	server.ensureEntitiesLoaded(world.DimensionOverworld)
	// 恢复已保存的玩家数据（players.json；不存在时按新玩家处理）。
	if err := server.loadPlayerData(); err != nil {
		slog.Error("加载玩家数据失败", "error", err)
	}
	// 审计日志（聊天/命令/玩家事件）；初始化失败不阻止启动。
	auditLogger, auditCloser, err := logging.NewAuditLogger(cfg.AuditLogFile, int64(cfg.LogMaxSizeMB)<<20)
	if err != nil {
		slog.Error("初始化审计日志失败，已禁用", "error", err)
	} else {
		server.audit = auditLogger
		server.auditClose = auditCloser
	}
	return server, nil
}

// auditLog 写一条审计日志（event 为事件类型：chat/command/join/leave/kick 等）。
func (s *Server) auditLog(event string, attrs ...any) {
	if s.audit == nil {
		return
	}
	s.audit.Info(event, attrs...)
}

// uuidString 返回 UUID 的十六进制字符串形式（审计日志与持久化用）。
func uuidString(uuid [16]byte) string {
	return hex.EncodeToString(uuid[:])
}

// spawnInfo 构造带出生点信息的世界状态（Login 与 Respawn 共用）。
func (s *Server) spawnInfo(dim world.Dimension, gameMode uint8) protocol.SpawnInfo {
	return protocol.SpawnInfo{
		DimensionTypeID:  dim.TypeID(),
		DimensionName:    dim.Name(),
		HashedSeed:       s.config.WorldSeed,
		GameMode:         gameMode,
		PreviousGameMode: 0xFF, // 未定义
		SeaLevel:         world.SeaLevel,
	}
}

// insideBorder 报告坐标是否位于世界边界内（未启用边界时恒为 true）。
func (s *Server) insideBorder(x, z float64) bool {
	if s.borderHalfSize <= 0 {
		return true
	}
	return math.Abs(x) <= s.borderHalfSize && math.Abs(z) <= s.borderHalfSize
}

func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	var shutdownOnce sync.Once
	shutdownDone := make(chan struct{})
	shutdown := func() {
		shutdownOnce.Do(func() {
			_ = listener.Close()
			s.mu.Lock()
			s.closing = true
			for conn := range s.conns {
				_ = conn.Close()
			}
			s.mu.Unlock()
			close(shutdownDone)
		})
	}
	stopShutdown := context.AfterFunc(ctx, shutdown)

	// 自动保存循环：ctx 取消（服务器关闭）时退出；最终的保存由 Serve 结尾执行。
	if interval := s.config.AutosaveSeconds; interval > 0 {
		go s.autosaveLoop(ctx, time.Duration(interval)*time.Second)
	}

	// 实体 Tick 循环：推进生物 AI 并提供确定性测试入口（tickInterval <= 0 时禁用）。
	go s.tickLoop(ctx)

	// 后台 Tick 被禁用时（测试手动驱动 tick），启动低频写缓冲刷写循环：
	// 直接调用内部 API 产生的广播（不经读循环）也能在有限时间内到达
	// 客户端，不依赖调用方恰好触发其它 flush 时机。生产模式（tick > 0）
	// 由 Tick 结尾的 drain 覆盖，无需该循环。
	go s.writeDrainLoop(ctx)

	// 实体数据周期保存：ctx 取消（服务器关闭）时退出。
	if s.mobsEnabled || s.itemsEnabled {
		go s.entityAutosaveLoop(ctx)
	}

	// 玩家数据周期保存：ctx 取消（服务器关闭）时退出。
	if s.playerAutosaveInterval > 0 {
		go s.playerAutosaveLoop(ctx)
	}

	// 区块卸载循环：周期性把远离所有玩家的区块移出内存缓存，
	// 避免长时间跑图导致内存占用持续增长。
	go s.chunkUnloadLoop(ctx)

	var serveErr error
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() == nil && !errors.Is(err, net.ErrClosed) {
				serveErr = err
			}
			break
		}
		if ctx.Err() != nil {
			_ = conn.Close()
			break
		}
		select {
		case s.clients <- struct{}{}:
			s.mu.Lock()
			if s.closing {
				s.mu.Unlock()
				<-s.clients
				_ = conn.Close()
				break
			}
			s.conns[conn] = struct{}{}
			s.wg.Add(1)
			s.mu.Unlock()
			go s.serveClient(conn)
		default:
			_ = conn.Close()
		}
	}

	shutdown()
	if !stopShutdown() {
		<-shutdownDone
	}
	<-shutdownDone
	s.wg.Wait()
	// 全部会话结束：保存全部已加载维度世界。
	if err := s.closeWorlds(); err != nil {
		slog.Error("failed to save the world", "error", err)
		if serveErr == nil {
			serveErr = err
		}
	}
	if s.auditClose != nil {
		_ = s.auditClose.Close()
	}
	// 保存实体数据（生物与掉落物；与世界的保存相互独立）。
	if s.mobsEnabled || s.itemsEnabled {
		if err := s.saveEntities(); err != nil {
			slog.Error("failed to save entities", "error", err)
		}
	}
	// 保存世界时间与天气（最后一次，保证时间不因重启回退）。
	if err := s.saveLevelState(); err != nil {
		slog.Error("failed to save level state", "error", err)
	}
	return serveErr
}

// registerPlayer 把玩家加入在线列表，返回当前在线的其他玩家。
func (s *Server) registerPlayer(player *session) []*session {
	s.mu.Lock()
	defer s.mu.Unlock()
	others := make([]*session, 0, len(s.players))
	for _, other := range s.players {
		others = append(others, other)
	}
	s.players[player.uuid] = player
	s.publishPlayersSnapshotLocked()
	return others
}

// publishPlayersSnapshotLocked 重建在线玩家无锁快照（调用方持有 s.mu）。
// 快照供写缓冲 drain 使用（读循环每包一次遍历事务会话，必须零分配零锁）。
func (s *Server) publishPlayersSnapshotLocked() {
	snapshot := make([]*session, 0, len(s.players))
	for _, player := range s.players {
		snapshot = append(snapshot, player)
	}
	s.playersSnap.Store(&snapshot)
}

// drainPlayerWrites 把全部在线会话写缓冲中待发的数据刷出。
// 调用点：读循环每个包处理完后（跨会话广播 tryWrite 的目标会话由这里
// 收尾，不依赖目标会话自己的读循环或 Tick）、Tick 结尾、玩家进出收尾。
// 只对 writePending 的会话取锁（无写时不触碰锁）。
func (s *Server) drainPlayerWrites() {
	snapshot := s.playersSnap.Load()
	if snapshot == nil {
		return
	}
	for _, player := range *snapshot {
		player.flushWrites()
	}
}

// testWriteDrainInterval 是后台 Tick 禁用时写缓冲的刷写周期
// （生产模式由 Tick 尾 drain 覆盖，该循环不运行）。
const testWriteDrainInterval = 5 * time.Millisecond

// writeDrainLoop 在后台 Tick 禁用（tickInterval <= 0）时周期刷写会话写缓冲。
func (s *Server) writeDrainLoop(ctx context.Context) {
	if s.tickInterval > 0 {
		return
	}
	ticker := time.NewTicker(testWriteDrainInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.drainPlayerWrites()
		}
	}
}

// unregisterPlayer 从在线列表移除玩家，并通知其他玩家。
// 仅当列表中的会话仍是本会话时才删除（避免快速重连时旧会话
// 误删新会话的在线记录）。最后一名玩家退出时会唤醒卸载循环立即释放资源。
func (s *Server) unregisterPlayer(player *session) {
	s.mu.Lock()
	removed := false
	if current, ok := s.players[player.uuid]; ok && current == player {
		delete(s.players, player.uuid)
		removed = true
	}
	empty := removed && len(s.players) == 0
	if removed {
		s.publishPlayersSnapshotLocked()
	}
	s.mu.Unlock()
	if empty {
		s.wakeChunkUnload()
	}

	s.dropEntityTracking(player)
	s.broadcastPlayerRemove(player)
	s.broadcastPacketExcluding(protocol.EncodePlayerInfoRemove([][16]byte{player.uuid}), player)
	s.broadcastPacketExcluding(protocol.EncodeSystemChat(player.name+" left the game"), player)
	// 离开广播写给其他会话（tryWrite 进写缓冲）：立即刷出，
	// 否则要等下一个 Tick 或接收方的下一个包才会到达。
	s.drainPlayerWrites()
}

// isFullFor 报告服务器对给定会话是否已达人数上限。
// 同 UUID 的既有会话视为重连替换（registerPlayer 会覆盖并踢掉旧会话），
// 不占用新名额；MaxPlayers <= 0 视为不限制。
func (s *Server) isFullFor(player *session) bool {
	if s.config.MaxPlayers <= 0 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.players) < s.config.MaxPlayers {
		return false
	}
	_, replacing := s.players[player.uuid]
	return !replacing
}

// wakeChunkUnload 非阻塞地唤醒区块卸载循环（容量 1，重复信号会被合并）。
// 直接构造的最小 Server（命令测试等）没有 unloadNow 通道，此时忽略。
func (s *Server) wakeChunkUnload() {
	if s.unloadNow == nil {
		return
	}
	select {
	case s.unloadNow <- struct{}{}:
	default:
	}
}

// broadcastPacket 向所有在线玩家发送数据包（忽略单个玩家的写失败）。
func (s *Server) broadcastPacket(packet []byte) {
	s.broadcastPacketExcluding(packet, nil)
}

// broadcastPacketExcluding 向除 except 外的在线玩家发送数据包。
// 压缩帧只构造一次（内容与接收者无关），多玩家共享同一份只读字节：
// 广播规模（玩家数）不再重复压缩与分配；无接收者时不构造。
func (s *Server) broadcastPacketExcluding(packet []byte, except *session) {
	s.mu.Lock()
	targets := make([]*session, 0, len(s.players))
	for _, player := range s.players {
		if player != except {
			targets = append(targets, player)
		}
	}
	s.mu.Unlock()
	if len(targets) == 0 {
		return
	}
	frame, err := protocol.CompressPacketFrame(packet, compressionThreshold)
	if err != nil {
		slog.Error("构造广播帧失败", "error", err)
		return
	}
	for _, player := range targets {
		if err := player.writeFrame(frame); err != nil {
			_ = player.conn.Close()
		}
	}
}

// broadcastChatMessage 广播一条已通过校验的玩家聊天消息（含签名与确认窗口转发）。
//
// 与原版一致：
//   - 每条消息为每个接收连接独立编号（Player Chat 的 globalIndex）；
//   - 收到消息的连接把消息签名记入自己的确认窗口（addPending），
//     待确认数量超过上限时断开该连接。
func (s *Server) broadcastChatMessage(sender *session, message playerChatMessage) {
	s.mu.Lock()
	targets := make([]*session, 0, len(s.players))
	for _, player := range s.players {
		targets = append(targets, player)
	}
	s.mu.Unlock()
	for _, player := range targets {
		data := protocol.PlayerChatData{
			GlobalIndex: player.nextChatGlobalIndex(),
			SenderUUID:  sender.uuid,
			SenderIndex: message.index,
			Signature:   message.signature,
			Message:     message.content,
			Timestamp:   message.timestampMillis,
			Salt:        message.salt,
			LastSeen:    message.lastSeen,
			DisplayName: sender.name,
		}
		player.tryWrite(protocol.EncodePlayerChatMessage(data))
		if len(message.signature) == protocol.MaxChatSignatureLength {
			player.recordPendingChatSignature(message.signature)
		}
	}
	slog.Info("chat", "name", sender.name, "message", message.content)
	s.auditLog("chat", "player", sender.name, "message", message.content)
}

// secureProfileEnforced 报告服务器是否强制安全档案（原版 enforce-secure-profile）。
// 原版在离线模式下忽略该选项（客户端无法获取档案密钥）。
func (s *Server) secureProfileEnforced() bool {
	return s.config.OnlineMode && s.config.EnforceSecureProfile
}

func (s *Server) serveClient(conn net.Conn) {
	defer s.wg.Done()
	defer func() {
		_ = conn.Close()
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
		<-s.clients
	}()

	_ = conn.SetReadDeadline(time.Now().Add(clientTimeout))
	handshakePacket, err := protocol.ReadPacket(conn)
	if err != nil {
		return
	}
	handshake, err := protocol.ParseHandshake(handshakePacket)
	if err != nil {
		return
	}

	switch handshake.NextState {
	case 1:
		s.handleStatus(conn)
	case 2:
		newSession(s, conn, handshake.ProtocolVersion).run()
	default:
		return
	}
}

func (s *Server) handleStatus(conn net.Conn) {
	request, err := protocol.ReadPacket(conn)
	if err != nil {
		return
	}
	requestID, requestSize, err := protocol.DecodeVarInt(request)
	if err != nil || requestID != 0 || requestSize != len(request) {
		return
	}

	s.mu.Lock()
	online := len(s.players)
	s.mu.Unlock()

	status, err := json.Marshal(struct {
		Version struct {
			Name     string `json:"name"`
			Protocol int32  `json:"protocol"`
		} `json:"version"`
		Players struct {
			Max    int `json:"max"`
			Online int `json:"online"`
		} `json:"players"`
		Description struct {
			Text string `json:"text"`
		} `json:"description"`
		// EnforcesSecureChat = true 时客户端把服务器标记为“强制安全档案”
		// （去掉服务器列表的“未验证”警告）；正版模式且未关闭强制安全档案时为 true。
		EnforcesSecureChat bool `json:"enforcesSecureChat"`
		PreviewsChat       bool `json:"previewsChat"`
	}{
		Version: struct {
			Name     string `json:"name"`
			Protocol int32  `json:"protocol"`
		}{Name: s.config.VersionName, Protocol: s.config.ProtocolVersion},
		Players: struct {
			Max    int `json:"max"`
			Online int `json:"online"`
		}{Max: s.config.MaxPlayers, Online: online},
		Description: struct {
			Text string `json:"text"`
		}{Text: s.config.MOTD},
		EnforcesSecureChat: s.secureProfileEnforced(),
	})
	if err != nil {
		return
	}
	response := protocol.AppendVarInt(nil, 0)
	response = protocol.AppendVarInt(response, int32(len(status)))
	response = append(response, status...)
	if err := protocol.WritePacket(conn, response); err != nil {
		return
	}

	ping, err := protocol.ReadPacket(conn)
	if err != nil {
		return
	}
	pingID, pingIDSize, err := protocol.DecodeVarInt(ping)
	if err != nil || pingID != 1 || len(ping)-pingIDSize != 8 {
		return
	}
	pong := protocol.AppendVarInt(nil, 1)
	pong = append(pong, ping[pingIDSize:]...)
	_ = protocol.WritePacket(conn, pong)
}

func DecodeStatusResponse(packet []byte) (string, error) {
	packetID, offset, err := protocol.DecodeVarInt(packet)
	if err != nil || packetID != 0 {
		return "", fmt.Errorf("invalid status response packet ID")
	}
	length, offset, err := decodeLength(packet, offset)
	if err != nil || length < 0 || int(length) != len(packet)-offset {
		return "", fmt.Errorf("invalid status response string length")
	}
	return string(packet[offset:]), nil
}

func decodeLength(packet []byte, offset int) (int32, int, error) {
	length, size, err := protocol.DecodeVarInt(packet[offset:])
	if err != nil {
		return 0, offset, err
	}
	return length, offset + size, nil
}
