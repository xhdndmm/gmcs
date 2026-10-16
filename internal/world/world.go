//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package world

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"sync"
	"time"
)

// ChunkPos 是一个区块坐标。
type ChunkPos struct {
	X, Z int
}

// World 管理一个维度的区块：内存缓存、磁盘持久化与地形生成。
//
// 并发模型：World 的公开方法可由多个 goroutine 调用（内部加锁）；
// 返回的 *Chunk 由调用方独占使用——当前阶段游戏逻辑不会并发修改区块，
// 保存时在锁内编码，从而避免与 Flush 的数据竞争。
type World struct {
	dir       string
	generator Generator
	// dimension 是该世界所属维度（区块加载/生成后统一赋给 Chunk，
	// 网络编码按维度裁剪 section 范围）。
	dimension Dimension

	mu     sync.RWMutex
	chunks map[ChunkPos]*Chunk
	dirty  map[ChunkPos]struct{}
	closed bool

	// 异步预生成：pending 记录进行中的区块请求（去重）；genQueue 是待生成
	// 队列；quit 在 Close 时关闭以停止 worker。
	pending       map[ChunkPos]*chunkFuture
	genQueue      chan ChunkPos
	quit          chan struct{}
	closeQuitOnce sync.Once

	// saveMu 串行化磁盘写入，防止并发 Flush 互相覆盖区域文件。
	saveMu sync.Mutex

	// unloadMu 串行化区块卸载与关闭，避免卸载与最终保存交错造成数据丢失。
	unloadMu sync.Mutex

	// 区块加载跟踪：开启后（EnableChunkLoadTracking）把新进入内存缓存的区块
	// 位置记入 loadedChunks，由上层用 TakeLoadedChunks 取走并按需注册方块级
	// 运行时状态（例如漏斗）。未开启时不记录，避免无人消费时持续增长。
	trackChunks  bool
	loadedChunks []ChunkPos
}

// chunkFuture 表示一次进行中的区块加载/生成；done 关闭后可从 chunks 读取
// 结果（失败时读 future.err）。
type chunkFuture struct {
	done chan struct{}
	err  error
}

// AsyncPrefetchWorkers 是异步预生成的 worker 数量上限。
// 生成是 CPU 密集型纯函数（只读种子），可以安全并行。
const AsyncPrefetchWorkers = 8

// chunkLoadTrackLimit 是待消费“新加载区块”列表的长度上限。
// 超过后丢弃多余记录（调用方应配合低频全量扫描兜底）。
const chunkLoadTrackLimit = 8192

// EnableChunkLoadTracking 开启区块加载跟踪：此后每个进入内存缓存的区块
// （从磁盘加载或由生成器生成）都会被记录，直到 TakeLoadedChunks 取走。
// 用于上层在区块加载后同步方块级运行时状态（例如漏斗注册表）。
func (w *World) EnableChunkLoadTracking() {
	w.mu.Lock()
	w.trackChunks = true
	w.mu.Unlock()
}

// TakeLoadedChunks 返回并清空自上次调用以来新进入内存缓存的区块位置；
// 未开启跟踪时返回 nil。列表最多保留 chunkLoadTrackLimit 项，超出部分
// 被丢弃（调用方应有低频全量扫描兜底）。
func (w *World) TakeLoadedChunks() []ChunkPos {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.loadedChunks) == 0 {
		return nil
	}
	chunks := w.loadedChunks
	w.loadedChunks = nil
	return chunks
}

// trackChunkLoaded 记录新加载的区块（调用方持有 w.mu 写锁）。
func (w *World) trackChunkLoaded(pos ChunkPos) {
	if !w.trackChunks || len(w.loadedChunks) >= chunkLoadTrackLimit {
		return
	}
	w.loadedChunks = append(w.loadedChunks, pos)
}

// prefetchQueueLimit 是预生成队列长度上限；队列满时丢弃请求（调用方
// 回退到同步生成）。
const prefetchQueueLimit = 4096

// Open 打开（或创建）主世界目录。
func Open(dir string, generator Generator) (*World, error) {
	return OpenDimension(dir, generator, DimensionOverworld)
}

// OpenDimension 打开（或创建）指定维度的世界目录。
func OpenDimension(dir string, generator Generator, dimension Dimension) (*World, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建世界目录：%w", err)
	}
	workers := min(AsyncPrefetchWorkers, max(1, runtime.GOMAXPROCS(0)))
	w := &World{
		dir:       dir,
		generator: generator,
		dimension: dimension,
		chunks:    make(map[ChunkPos]*Chunk),
		dirty:     make(map[ChunkPos]struct{}),
		pending:   make(map[ChunkPos]*chunkFuture),
		genQueue:  make(chan ChunkPos, prefetchQueueLimit),
		quit:      make(chan struct{}),
	}
	for i := 0; i < workers; i++ {
		go w.prefetchWorker()
	}
	return w, nil
}

// Prefetch 请求在后台加载/生成给定区块（不阻塞）。重复请求与已缓存区块
// 会被跳过；队列满时静默丢弃（调用方随后同步加载）。
// 生成结果与同步路径一致：写入内存缓存并标记为待保存。
func (w *World) Prefetch(positions []ChunkPos) {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	w.mu.Unlock()
	for _, pos := range positions {
		w.mu.Lock()
		_, cached := w.chunks[pos]
		_, inFlight := w.pending[pos]
		if !cached && !inFlight {
			w.pending[pos] = &chunkFuture{done: make(chan struct{})}
		}
		w.mu.Unlock()
		if cached || inFlight {
			continue
		}
		select {
		case w.genQueue <- pos:
		default:
			// 队列已满：撤销请求（同步路径会处理）。
			w.mu.Lock()
			if future, ok := w.pending[pos]; ok {
				future.err = fmt.Errorf("预生成队列已满")
				close(future.done)
				delete(w.pending, pos)
			}
			w.mu.Unlock()
		}
	}
}

// prefetchWorker 消费预生成队列：加载磁盘区块或调用生成器，然后发布结果。
// quit 关闭后退会出（未处理的请求由 Close 统一解决）。
func (w *World) prefetchWorker() {
	for {
		var pos ChunkPos
		select {
		case <-w.quit:
			return
		case pos = <-w.genQueue:
		}

		w.mu.Lock()
		_, stillPending := w.pending[pos]
		closed := w.closed
		w.mu.Unlock()
		if !stillPending || closed {
			continue
		}

		chunk, err := LoadChunk(w.dir, pos.X, pos.Z)
		generated := false
		if err == nil && chunk == nil {
			chunk = w.generator.GenerateChunk(pos.X, pos.Z)
			generated = true
		}

		w.mu.Lock()
		w.publishChunkLoad(pos, chunk, generated, err)
		w.mu.Unlock()
	}
}

// publishChunkLoad 发布一次区块加载/生成的结果（调用方持有 w.mu 写锁）。
// Close 可能已提前解决并删除请求：此时丢弃结果并返回 false。
// 同步路径（Chunk）与异步预生成（prefetchWorker）共用，
// 保证两条路径的发布语义完全一致。
func (w *World) publishChunkLoad(pos ChunkPos, chunk *Chunk, generated bool, err error) bool {
	future, inFlight := w.pending[pos]
	if !inFlight {
		return false
	}
	if err != nil || w.closed {
		delete(w.pending, pos)
		future.err = err
		close(future.done)
		return false
	}
	if chunk.Dimension() != w.dimension {
		chunk.SetDimension(w.dimension)
	}
	w.chunks[pos] = chunk
	w.trackChunkLoaded(pos)
	if generated {
		w.dirty[pos] = struct{}{}
	}
	delete(w.pending, pos)
	future.err = nil
	close(future.done)
	return true
}

// Dimension 返回世界所属维度。
func (w *World) Dimension() Dimension {
	return w.dimension
}

// Dir 返回世界目录。
func (w *World) Dir() string {
	return w.dir
}

// SurfaceY 返回世界坐标 (x, z) 处生成器给出的地形表面 Y（最高固体方块）。
// 用于出生点等需要与生成器一致的场景。
func (w *World) SurfaceY(x, z int) int {
	return w.generator.SurfaceY(x, z)
}

// GroundY 返回世界坐标 (x, z) 处可供站立的脚部 Y 坐标（最高非空气、
// 非水方块的上一格）；该列没有地面时返回 false。区块不存在时按需生成。
func (w *World) GroundY(x, z int) (float64, bool) {
	chunk, err := w.Chunk(floorDiv(x, SectionSize), floorDiv(z, SectionSize))
	if err != nil {
		return 0, false
	}
	y, ok := chunk.TopSolidY(floorMod(x, SectionSize), floorMod(z, SectionSize))
	if !ok {
		return 0, false
	}
	return float64(y + 1), true
}

// TopBlock 返回世界坐标 (x, z) 处最高的非空气方块（包括水）。
func (w *World) TopBlock(x, z int) (uint16, int, bool) {
	chunk, err := w.Chunk(floorDiv(x, SectionSize), floorDiv(z, SectionSize))
	if err != nil {
		return 0, 0, false
	}
	return chunk.TopBlock(floorMod(x, SectionSize), floorMod(z, SectionSize))
}

// ColumnAt 返回世界坐标 (x, z) 对应列的高度信息（按列缓存，方块修改后失效）。
// 一次调用即可获得 TopBlock 与 GroundY 两者所需的数据，供生物移动等
// 频繁查询的路径使用。区块不可用时返回 false。
func (w *World) ColumnAt(x, z int) (Column, bool) {
	chunk, err := w.Chunk(floorDiv(x, SectionSize), floorDiv(z, SectionSize))
	if err != nil {
		return Column{}, false
	}
	return chunk.Column(floorMod(x, SectionSize), floorMod(z, SectionSize)), true
}

// BlockAt 返回世界坐标处方块的方块状态；超出世界高度或区块不可用时返回空气。
func (w *World) BlockAt(x, y, z int) uint16 {
	if _, ok := SectionIndex(y); !ok {
		return AirBlock
	}
	cx, cz := floorDiv(x, SectionSize), floorDiv(z, SectionSize)
	localX, localZ := floorMod(x, SectionSize), floorMod(z, SectionSize)
	// 快路径：区块已加载时用读锁查询（实体物理/实体进入方块扫描等逐格读取
	// 非常频繁，避免每格取得排他锁与加载/生成分支）。
	w.mu.RLock()
	chunk, ok := w.chunks[ChunkPos{X: cx, Z: cz}]
	w.mu.RUnlock()
	if ok {
		return chunk.GetBlockState(localX, y, localZ)
	}
	// 慢路径：区块不在内存中时保持原有语义（从磁盘加载或生成）。
	loaded, err := w.Chunk(cx, cz)
	if err != nil {
		return AirBlock
	}
	return loaded.GetBlockState(localX, y, localZ)
}

// LoadedColumn 读取已加载区块上 (x, z) 列在 y ∈ [y1, y2] 闭区间内的方块状态，
// 按从低到高的顺序写入 dst（最多 len(dst) 个），返回写入数量；区块未加载时
// 返回 0。与逐格调用 BlockAt 相比，区块只解析/加锁一次，适合实体物理与
// 实体进入方块扫描这类小范围连续读取；不触发区块加载或地形生成。
func (w *World) LoadedColumn(x, y1, y2, z int, dst []uint16) int {
	if y2 < y1 || len(dst) == 0 {
		return 0
	}
	w.mu.RLock()
	chunk, ok := w.chunks[ChunkPos{X: floorDiv(x, SectionSize), Z: floorDiv(z, SectionSize)}]
	w.mu.RUnlock()
	if !ok {
		return 0
	}
	return chunk.CopyColumn(floorMod(x, SectionSize), y1, y2, floorMod(z, SectionSize), dst)
}

// SetBlock 设置世界坐标处方块的状态并标记区块待保存（下一次 Flush/卸载时落盘）。
// 超出世界高度或区块不可用时返回 false。
func (w *World) SetBlock(x, y, z int, state uint16) bool {
	if _, ok := SectionIndex(y); !ok {
		return false
	}
	chunk, err := w.Chunk(floorDiv(x, SectionSize), floorDiv(z, SectionSize))
	if err != nil {
		return false
	}
	chunk.SetBlockState(floorMod(x, SectionSize), y, floorMod(z, SectionSize), state)
	w.MarkDirty(chunk)
	return true
}

// BlockEntityAt 返回世界坐标处的方块实体数据；不存在时返回空数据。
func (w *World) BlockEntityAt(x, y, z int) (BlockEntity, error) {
	if _, ok := SectionIndex(y); !ok {
		return BlockEntity{}, nil
	}
	chunk, err := w.Chunk(floorDiv(x, SectionSize), floorDiv(z, SectionSize))
	if err != nil {
		return BlockEntity{}, err
	}
	entity, _ := chunk.BlockEntityAt(floorMod(x, SectionSize), y, floorMod(z, SectionSize))
	return entity, nil
}

// SetBlockEntity 设置世界坐标处的方块实体数据并标记区块待保存。
func (w *World) SetBlockEntity(x, y, z int, entity BlockEntity) error {
	if _, ok := SectionIndex(y); !ok {
		return nil
	}
	chunk, err := w.Chunk(floorDiv(x, SectionSize), floorDiv(z, SectionSize))
	if err != nil {
		return err
	}
	chunk.SetBlockEntity(floorMod(x, SectionSize), y, floorMod(z, SectionSize), entity)
	w.MarkDirty(chunk)
	return nil
}

// SetTransferCooldown 就地更新世界坐标处方块实体的传送冷却字段并标记区块待保存
// （漏斗每 tick 递减时调用：不重建槽位数组，也不产生闭包分配）。
// 为降低高频调用的锁开销，这里只取一次世界锁完成“查区块 + 标脏”，
// 且不触发区块加载（仅更新已在内存中的区块，与漏斗 Tick 的加载检查一致）；
// 记录不存在或区块不在内存时返回 false。
func (w *World) SetTransferCooldown(x, y, z int, cooldown int32) bool {
	if _, ok := SectionIndex(y); !ok {
		return false
	}
	pos := ChunkPos{X: floorDiv(x, SectionSize), Z: floorDiv(z, SectionSize)}
	w.mu.Lock()
	defer w.mu.Unlock()
	chunk, ok := w.chunks[pos]
	if !ok {
		return false
	}
	if !chunk.SetTransferCooldown(floorMod(x, SectionSize), y, floorMod(z, SectionSize), cooldown) {
		return false
	}
	w.dirty[pos] = struct{}{}
	return true
}

// RemoveBlockEntity 清除世界坐标处的方块实体数据并标记区块待保存。
func (w *World) RemoveBlockEntity(x, y, z int) error {
	if _, ok := SectionIndex(y); !ok {
		return nil
	}
	chunk, err := w.Chunk(floorDiv(x, SectionSize), floorDiv(z, SectionSize))
	if err != nil {
		return err
	}
	chunk.RemoveBlockEntity(floorMod(x, SectionSize), y, floorMod(z, SectionSize))
	w.MarkDirty(chunk)
	return nil
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

func floorMod(a, b int) int {
	m := a % b
	if m < 0 {
		m += b
	}
	return m
}

// Chunk 返回区块：依次尝试内存缓存、进行中的请求、磁盘文件，最后调用
// 生成器生成。新生成的区块会被标记为需要保存。
//
// 缓存命中（绝大多数调用：列/高度/方块查询经此入口）只取读锁，
// 并发查询互不阻塞；磁盘读取与地形生成都在 w.mu 之外执行：没有进行中
// 的请求时，本调用注册一个请求（与 Prefetch 共用去重表），在锁外完成
// IO/生成后发布；期间其他 goroutine 对同一区块的请求等待同一结果。
func (w *World) Chunk(x, z int) (*Chunk, error) {
	pos := ChunkPos{X: x, Z: z}
	for {
		// 快路径：缓存命中（读锁）。
		w.mu.RLock()
		chunk, ok := w.chunks[pos]
		w.mu.RUnlock()
		if ok {
			return chunk, nil
		}
		w.mu.Lock()
		if w.closed {
			w.mu.Unlock()
			return nil, fmt.Errorf("世界已关闭")
		}
		if chunk, ok := w.chunks[pos]; ok {
			w.mu.Unlock()
			return chunk, nil
		}
		if future, inFlight := w.pending[pos]; inFlight {
			w.mu.Unlock()
			// 等待进行中的异步加载/生成，然后重新读取结果。
			<-future.done
			if future.err != nil {
				return nil, future.err
			}
			continue
		}
		// 没有进行中的请求：注册本 goroutine 的请求并在锁外完成
		// 磁盘读取/地形生成（与其他 goroutine 的同一请求去重）。
		w.pending[pos] = &chunkFuture{done: make(chan struct{})}
		w.mu.Unlock()

		chunk, err := LoadChunk(w.dir, x, z)
		generated := false
		if err == nil && chunk == nil {
			chunk = w.generator.GenerateChunk(x, z)
			generated = true
		}

		w.mu.Lock()
		published := w.publishChunkLoad(pos, chunk, generated, err)
		w.mu.Unlock()
		if err != nil {
			return nil, err
		}
		if !published {
			// 请求被 Close 取消（世界已关闭）。
			return nil, fmt.Errorf("世界已关闭")
		}
		return chunk, nil
	}
}

// MarkDirty 标记区块需要保存。
func (w *World) MarkDirty(chunk *Chunk) {
	pos := ChunkPos{X: chunk.X, Z: chunk.Z}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.dirty[pos] = struct{}{}
}

// DirtyCount 返回待保存的区块数量。
func (w *World) DirtyCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.dirty)
}

// ChunkCount 返回内存缓存中的区块数量（观测与测试用）。
func (w *World) ChunkCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.chunks)
}

// FurnaceBlockPositions 返回当前已加载区块中指定类型方块实体的世界坐标快照
// （熔炉 Tick 扫描用；调用方随后按坐标读写方块实体）。
func (w *World) FurnaceBlockPositions(typeIDs map[int32]bool) [][3]int {
	w.mu.Lock()
	defer w.mu.Unlock()
	var result [][3]int
	for pos, chunk := range w.chunks {
		chunk.mu.RLock()
		for index, entity := range chunk.blockEntities {
			if !typeIDs[entity.TypeID] {
				continue
			}
			x := index & 0xF
			z := (index >> 4) & 0xF
			y := (index >> 8) + WorldMinY
			result = append(result, [3]int{pos.X*SectionSize + x, y, pos.Z*SectionSize + z})
		}
		chunk.mu.RUnlock()
	}
	return result
}

// BlockStatePositions 返回当前已加载区块中含有满足 match 的方块状态的坐标快照
// （先用 section 调色板快速排除不含目标状态的 section；直连（16 位）模式逐格检查）。
// 用于按方块种类注册运行时状态（例如尚无方块实体记录的空漏斗）。
func (w *World) BlockStatePositions(match func(uint16) bool) [][3]int {
	w.mu.RLock()
	chunks := make(map[ChunkPos]*Chunk, len(w.chunks))
	for pos, chunk := range w.chunks {
		chunks[pos] = chunk
	}
	w.mu.RUnlock()
	var result [][3]int
	for pos, chunk := range chunks {
		chunk.mu.RLock()
		result = appendSectionMatches(result, chunk, pos, match)
		chunk.mu.RUnlock()
	}
	return result
}

// ChunkBlockStatePositions 返回指定区块（区块坐标）中匹配 match 的方块坐标快照；
// 区块不在内存缓存中时返回 nil（不触发加载或生成）。
func (w *World) ChunkBlockStatePositions(cx, cz int, match func(uint16) bool) [][3]int {
	pos := ChunkPos{X: cx, Z: cz}
	w.mu.RLock()
	chunk := w.chunks[pos]
	w.mu.RUnlock()
	if chunk == nil {
		return nil
	}
	chunk.mu.RLock()
	defer chunk.mu.RUnlock()
	return appendSectionMatches(nil, chunk, pos, match)
}

// appendSectionMatches 把 chunk 中匹配 match 的方块坐标追加到 result
// （调用方持有 chunk.mu 读锁）。先用 section 的调色板排除不含目标状态的
// section，再在命中的 section 内逐格检查。
func appendSectionMatches(result [][3]int, chunk *Chunk, pos ChunkPos, match func(uint16) bool) [][3]int {
	for sectionIndex, section := range chunk.sections {
		if section == nil || section.storage.allAir() {
			continue
		}
		if !section.storage.mayContainState(match) {
			continue
		}
		baseY := WorldMinY + sectionIndex*SectionSize
		for index := 0; index < SectionVolume; index++ {
			if !match(section.storage.at(index)) {
				continue
			}
			x := index & 0xF
			z := (index >> 4) & 0xF
			y := (index >> 8) + baseY
			result = append(result, [3]int{pos.X*SectionSize + x, y, pos.Z*SectionSize + z})
		}
	}
	return result
}

// ChunkLoaded 报告方块坐标 (x, z) 所在区块当前是否在内存缓存中
// （不触发加载或生成）。注意入参是方块坐标而非区块坐标。
func (w *World) ChunkLoaded(x, z int) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	_, ok := w.chunks[ChunkPos{X: floorDiv(x, SectionSize), Z: floorDiv(z, SectionSize)}]
	return ok
}

// BiomeAt 返回方块列 (x, z) 的群系 ID；区块不在内存缓存时返回 false
// （不触发加载或生成，供火焰等低频逻辑查询群系相关行为）。
func (w *World) BiomeAt(x, z int) (uint16, bool) {
	w.mu.RLock()
	chunk, ok := w.chunks[ChunkPos{X: floorDiv(x, SectionSize), Z: floorDiv(z, SectionSize)}]
	w.mu.RUnlock()
	if !ok {
		return 0, false
	}
	return chunk.ColumnBiome(x&(SectionSize-1), z&(SectionSize-1)), true
}

// Flush 把所有待保存的区块写入磁盘。
// 编码 + 压缩在锁内完成（区块数据在锁内保持一致性）；磁盘 IO 在锁外执行。
// 保存失败的区块会被重新标记为待保存以便重试。可被多个 goroutine 并发调用。
func (w *World) Flush() error {
	w.saveMu.Lock()
	defer w.saveMu.Unlock()

	w.mu.Lock()
	// 先快照待保存列表并释放世界锁：编码（zlib 压缩）在锁外进行，
	// 避免保存期间阻塞方块查询/移动碰撞等读路径。
	type saveItem struct {
		pos   ChunkPos
		chunk *Chunk
	}
	items := make([]saveItem, 0, len(w.dirty))
	for pos := range w.dirty {
		chunk, ok := w.chunks[pos]
		if !ok {
			delete(w.dirty, pos)
			continue
		}
		items = append(items, saveItem{pos: pos, chunk: chunk})
		delete(w.dirty, pos)
	}
	w.mu.Unlock()

	payloads := make(map[ChunkPos][]byte, len(items))
	var encodeErr error
	for _, item := range items {
		compressed, err := encodeCompressedChunkPayload(item.chunk)
		if err != nil {
			// 保留为待保存，下一次 Flush 重试。
			if encodeErr == nil {
				encodeErr = fmt.Errorf("区块 (%d,%d) 编码失败：%w", item.pos.X, item.pos.Z, err)
			}
			w.mu.Lock()
			w.dirty[item.pos] = struct{}{}
			w.mu.Unlock()
			continue
		}
		payloads[item.pos] = compressed
	}

	if len(payloads) > 0 {
		if err := SavePayloads(w.dir, payloads); err != nil {
			w.mu.Lock()
			for pos := range payloads {
				w.dirty[pos] = struct{}{}
			}
			w.mu.Unlock()
			return err
		}
	}
	slog.Debug("world flushed", "chunks", len(payloads))
	return encodeErr
}

// UnloadFar 把距所有中心点都超过 radius 的区块移出内存缓存
// （切比雪夫距离，单位：区块），返回被卸载的区块位置。
//
// 待保存的区块会被先编码并在锁外写盘；保存失败时保留内存中的区块并重新
// 标记为待保存（返回错误）。与 Chunk 并发调用是安全的：已由调用方持有的
// *Chunk 指针仍然有效，后续访问同一位置会从磁盘或生成器重新加载。
// 没有中心点时不做任何事（UnloadAll 用于释放全部区块）。
func (w *World) UnloadFar(centers []ChunkPos, radius int) ([]ChunkPos, error) {
	if len(centers) == 0 {
		return nil, nil
	}
	return w.unloadChunks(func(pos ChunkPos) bool {
		return withinAny(pos, centers, radius)
	})
}

// UnloadAll 把所有区块移出内存缓存（服务器没有任何玩家时释放资源），
// 返回被卸载的区块位置。未保存的区块会先写盘，重新访问时按需从磁盘加载
// 或由生成器重新生成。
func (w *World) UnloadAll() ([]ChunkPos, error) {
	return w.unloadChunks(nil)
}

// unloadChunks 卸载 keep 返回 false 的全部区块；keep 为 nil 时卸载全部。
// 与 Flush/Close 共用 unloadMu 保证不会在最终保存之后残留未保存数据。
func (w *World) unloadChunks(keep func(ChunkPos) bool) ([]ChunkPos, error) {
	w.unloadMu.Lock()
	defer w.unloadMu.Unlock()

	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil, fmt.Errorf("世界已关闭")
	}
	var (
		candidates []ChunkPos
		payloads   = make(map[ChunkPos][]byte)
		encodeErr  error
	)
	for pos, chunk := range w.chunks {
		if keep != nil && keep(pos) {
			continue
		}
		candidates = append(candidates, pos)
		if _, dirty := w.dirty[pos]; dirty {
			compressed, err := encodeCompressedChunkPayload(chunk)
			if err != nil {
				// 保留在内存中并保持待保存，避免丢失修改。
				if encodeErr == nil {
					encodeErr = fmt.Errorf("区块 (%d,%d) 编码失败：%w", pos.X, pos.Z, err)
				}
				continue
			}
			payloads[pos] = compressed
			delete(w.dirty, pos)
		}
	}
	w.mu.Unlock()

	if len(payloads) > 0 {
		w.saveMu.Lock()
		err := SavePayloads(w.dir, payloads)
		w.saveMu.Unlock()
		if err != nil {
			w.mu.Lock()
			for pos := range payloads {
				w.dirty[pos] = struct{}{}
			}
			w.mu.Unlock()
			return nil, err
		}
	}

	w.mu.Lock()
	unloaded := make([]ChunkPos, 0, len(candidates))
	for _, pos := range candidates {
		if _, dirty := w.dirty[pos]; dirty {
			// 编码后又发生修改（或编码失败）：保留在内存中，等待下一次卸载/保存。
			continue
		}
		if _, ok := w.chunks[pos]; ok {
			delete(w.chunks, pos)
			unloaded = append(unloaded, pos)
		}
	}
	w.mu.Unlock()
	return unloaded, encodeErr
}

// withinAny 报告 pos 是否位于任一中心点的 radius 半径内（切比雪夫距离）。
func withinAny(pos ChunkPos, centers []ChunkPos, radius int) bool {
	for _, center := range centers {
		if abs(pos.X-center.X) <= radius && abs(pos.Z-center.Z) <= radius {
			return true
		}
	}
	return false
}

// Autosave 周期保存待保存的区块，直到 ctx 取消。
// 应该在单独的 goroutine 中运行。
func (w *World) Autosave(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.Flush(); err != nil {
				slog.Error("autosave failed", "error", err)
			}
		}
	}
}

// Close 保存全部待保存区块并停止接受新的区块加载。
// 与 UnloadFar 互斥，保证卸载不会在最终保存之后残留未保存数据。
// 进行中的异步预生成请求会被解决为“世界已关闭”错误。
func (w *World) Close() error {
	w.unloadMu.Lock()
	defer w.unloadMu.Unlock()
	err := w.Flush()
	w.mu.Lock()
	w.closed = true
	for pos, future := range w.pending {
		future.err = fmt.Errorf("世界已关闭")
		close(future.done)
		delete(w.pending, pos)
	}
	w.mu.Unlock()
	w.closeQuitOnce.Do(func() { close(w.quit) })
	return err
}
