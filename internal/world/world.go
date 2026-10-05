package world

import (
	"context"
	"fmt"
	"log/slog"
	"os"
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

	mu     sync.Mutex
	chunks map[ChunkPos]*Chunk
	dirty  map[ChunkPos]struct{}
	closed bool

	// saveMu 串行化磁盘写入，防止并发 Flush 互相覆盖区域文件。
	saveMu sync.Mutex

	// unloadMu 串行化区块卸载与关闭，避免卸载与最终保存交错造成数据丢失。
	unloadMu sync.Mutex
}

// Open 打开（或创建）世界目录。
func Open(dir string, generator Generator) (*World, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建世界目录：%w", err)
	}
	return &World{
		dir:       dir,
		generator: generator,
		chunks:    make(map[ChunkPos]*Chunk),
		dirty:     make(map[ChunkPos]struct{}),
	}, nil
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

// Chunk 返回区块：依次尝试内存缓存与磁盘文件，最后调用生成器生成。
// 新生成的区块会被标记为需要保存。
func (w *World) Chunk(x, z int) (*Chunk, error) {
	pos := ChunkPos{X: x, Z: z}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil, fmt.Errorf("世界已关闭")
	}
	if chunk, ok := w.chunks[pos]; ok {
		return chunk, nil
	}
	// 磁盘 IO 在锁内执行会阻塞其他调用者。当前阶段调用频率低（玩家进入
	// 世界时加载出生区块），以简单正确为先；未成为瓶颈前不引入异步加载。
	chunk, err := LoadChunk(w.dir, x, z)
	if err != nil {
		return nil, err
	}
	if chunk == nil {
		chunk = w.generator.GenerateChunk(x, z)
		w.dirty[pos] = struct{}{}
	}
	w.chunks[pos] = chunk
	return chunk, nil
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

// Flush 把所有待保存的区块写入磁盘。
// 编码在锁内完成（快），磁盘 IO 在锁外执行（慢）；保存失败的区块会被
// 重新标记为待保存以便重试。可被多个 goroutine 并发调用。
func (w *World) Flush() error {
	w.saveMu.Lock()
	defer w.saveMu.Unlock()

	w.mu.Lock()
	payloads := make(map[ChunkPos][]byte, len(w.dirty))
	for pos := range w.dirty {
		chunk, ok := w.chunks[pos]
		if !ok {
			continue
		}
		payloads[pos] = encodeChunkPayload(chunk)
	}
	clear(w.dirty)
	w.mu.Unlock()

	if len(payloads) == 0 {
		return nil
	}
	if err := SavePayloads(w.dir, payloads); err != nil {
		w.mu.Lock()
		for pos := range payloads {
			w.dirty[pos] = struct{}{}
		}
		w.mu.Unlock()
		return err
	}
	slog.Debug("world flushed", "chunks", len(payloads))
	return nil
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
	)
	for pos, chunk := range w.chunks {
		if withinAny(pos, centers, radius) {
			continue
		}
		candidates = append(candidates, pos)
		if _, dirty := w.dirty[pos]; dirty {
			payloads[pos] = encodeChunkPayload(chunk)
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
			// 编码后又发生修改：保留在内存中，等待下一次卸载/保存。
			continue
		}
		if _, ok := w.chunks[pos]; ok {
			delete(w.chunks, pos)
			unloaded++
		}
	}
	w.mu.Unlock()
	return unloaded, nil
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
func (w *World) Close() error {
	w.unloadMu.Lock()
	defer w.unloadMu.Unlock()
	err := w.Flush()
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	return err
}
