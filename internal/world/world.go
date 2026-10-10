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

	mu     sync.Mutex
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
		future, stillPending := w.pending[pos]
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
		// 发布前重新检查：Close 可能已解决并删除该请求。
		if future, stillPending = w.pending[pos]; stillPending && !w.closed && err == nil {
			if chunk.Dimension() != w.dimension {
				chunk.SetDimension(w.dimension)
			}
			w.chunks[pos] = chunk
			if generated {
				w.dirty[pos] = struct{}{}
			}
			delete(w.pending, pos)
			future.err = nil
			close(future.done)
		} else if stillPending {
			delete(w.pending, pos)
			future.err = err
			close(future.done)
		}
		w.mu.Unlock()
	}
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
	chunk, err := w.Chunk(floorDiv(x, SectionSize), floorDiv(z, SectionSize))
	if err != nil {
		return AirBlock
	}
	return chunk.GetBlockState(floorMod(x, SectionSize), y, floorMod(z, SectionSize))
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

// Chunk 返回区块：依次尝试内存缓存、进行中的异步请求、磁盘文件，
// 最后调用生成器生成。新生成的区块会被标记为需要保存。
func (w *World) Chunk(x, z int) (*Chunk, error) {
	pos := ChunkPos{X: x, Z: z}
	for {
		w.mu.Lock()
		if w.closed {
			w.mu.Unlock()
			return nil, fmt.Errorf("世界已关闭")
		}
		if chunk, ok := w.chunks[pos]; ok {
			w.mu.Unlock()
			return chunk, nil
		}
		future, pending := w.pending[pos]
		if !pending {
			// 同步加载/生成：磁盘 IO 在锁内执行（调用频率低时以简单正确
			// 为先；大视距批量加载走 Prefetch 的异步路径）。
			chunk, err := LoadChunk(w.dir, x, z)
			if err != nil {
				w.mu.Unlock()
				return nil, err
			}
			if chunk == nil {
				chunk = w.generator.GenerateChunk(x, z)
				w.dirty[pos] = struct{}{}
			}
			// 统一维度标记：从磁盘恢复的区块与生成器输出的区块都按本世界维度编码。
			if chunk.Dimension() != w.dimension {
				chunk.SetDimension(w.dimension)
			}
			w.chunks[pos] = chunk
			w.mu.Unlock()
			return chunk, nil
		}
		w.mu.Unlock()
		// 等待进行中的异步加载/生成，然后重新读取结果。
		<-future.done
		if future.err != nil {
			return nil, future.err
		}
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

// ChunkLoaded 报告方块坐标 (x, z) 所在区块当前是否在内存缓存中
// （不触发加载或生成）。注意入参是方块坐标而非区块坐标。
func (w *World) ChunkLoaded(x, z int) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, ok := w.chunks[ChunkPos{X: floorDiv(x, SectionSize), Z: floorDiv(z, SectionSize)}]
	return ok
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
// （切比雪夫距离，单位：区块），返回卸载数量。
//
// 待保存的区块会被先编码并在锁外写盘；保存失败时保留内存中的区块并重新
// 标记为待保存（返回错误）。与 Chunk 并发调用是安全的：已由调用方持有的
// *Chunk 指针仍然有效，后续访问同一位置会从磁盘或生成器重新加载。
func (w *World) UnloadFar(centers []ChunkPos, radius int) (int, error) {
	if len(centers) == 0 {
		return 0, nil
	}
	w.unloadMu.Lock()
	defer w.unloadMu.Unlock()

	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return 0, fmt.Errorf("世界已关闭")
	}
	var (
		candidates []ChunkPos
		payloads   = make(map[ChunkPos][]byte)
		encodeErr  error
	)
	for pos, chunk := range w.chunks {
		if withinAny(pos, centers, radius) {
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
			return 0, err
		}
	}

	w.mu.Lock()
	unloaded := 0
	for _, pos := range candidates {
		if _, dirty := w.dirty[pos]; dirty {
			// 编码后又发生修改（或编码失败）：保留在内存中，等待下一次卸载/保存。
			continue
		}
		if _, ok := w.chunks[pos]; ok {
			delete(w.chunks, pos)
			unloaded++
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
