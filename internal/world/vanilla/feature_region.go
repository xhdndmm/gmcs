//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import "sync"

// featureResult 是一个区块执行地物后产生的全部方块写入，包含跨出该区块
// 的部分。结果不保留基础地形，避免为每个缓存区块常驻一份完整区块。
type featureResult struct {
	writes   map[[3]int]SurfaceBlock
	entities map[[3]int]GeneratedBlockEntity
	// prepared 是已经应用完自身地物的区块，可在地物结果被邻域复用后
	// 直接作为中心区块使用，避免重新生成基础地形。按 preparedCacheLimit
	// 单独限量，不能跟着结果缓存的 writes 一起常驻。
	prepared *GeneratedChunk
}

type featureResultEntry struct {
	once   sync.Once
	result *featureResult
}

type featureResultCache struct {
	mu      sync.RWMutex
	entries map[chunkCoord]*featureResultEntry
	order   []chunkCoord
	limit   int
	// preparedOrder 只记录设置过 prepared 的 key，淘汰时无需扫描整个
	// order（96 项）；该扫描在 8 个预生成 worker 下会造成明显锁竞争。
	preparedOrder []chunkCoord
}

func newFeatureResultCache(limit int) *featureResultCache {
	return &featureResultCache{
		entries: make(map[chunkCoord]*featureResultEntry),
		limit:   limit,
	}
}

func (c *featureResultCache) peek(key chunkCoord) *featureResultEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.entries[key]
}

// clear 丢弃所有已完成/进行中的结果引用。进行中的 entry 仍可由其调用方
// 安全完成，只是不再留在缓存里；本方法用锁保证与 get/peek 并发安全。
const preparedCacheLimit = 16

// setPrepared 保存一个已应用自身地物的区块，并限制 prepared 区块数量。
func (c *featureResultCache) setPrepared(key chunkCoord, result *featureResult, chunk *GeneratedChunk) {
	c.mu.Lock()
	entry, ok := c.entries[key]
	if !ok || entry.result != result {
		c.mu.Unlock()
		return
	}
	result.prepared = chunk
	c.preparedOrder = append(c.preparedOrder, key)
	// 只淘汰最旧的 prepared 项。order 中可能已有 taken/evicted 的 key，
	// 跳过它们即可；正常情况下循环最多处理新增的少量项。
	for len(c.preparedOrder) > preparedCacheLimit {
		oldest := c.preparedOrder[0]
		c.preparedOrder = c.preparedOrder[1:]
		old, ok := c.entries[oldest]
		if !ok || old.result == nil || old.result.prepared == nil {
			continue
		}
		old.result.prepared = nil
	}
	c.mu.Unlock()
}

// takePrepared 取出并清空一个 prepared 区块；调用方拥有返回对象。
func (c *featureResultCache) takePrepared(key chunkCoord) *GeneratedChunk {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || entry.result == nil {
		return nil
	}
	chunk := entry.result.prepared
	entry.result.prepared = nil
	return chunk
}

func (c *featureResultCache) clear() {
	c.mu.Lock()
	c.entries = make(map[chunkCoord]*featureResultEntry)
	c.order = nil
	c.preparedOrder = nil
	c.mu.Unlock()
}

// resultFor 在锁内读取已完成结果；entry.result 由 setResult 在锁内发布。
func (c *featureResultCache) resultFor(entry *featureResultEntry) *featureResult {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return entry.result
}

func (c *featureResultCache) setResult(entry *featureResultEntry, result *featureResult) {
	// 即使条目在计算期间被淘汰，也要发布到本地 entry；当前调用方仍需
	// 使用完整写入结果，只是不再重新加入缓存。
	c.mu.Lock()
	entry.result = result
	c.mu.Unlock()
}

func (c *featureResultCache) get(key chunkCoord) *featureResultEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.entries[key]; ok {
		return entry
	}
	entry := &featureResultEntry{}
	c.entries[key] = entry
	c.order = append(c.order, key)
	if c.limit > 0 && len(c.order) > c.limit {
		oldest := c.order[0]
		c.order = c.order[1:]
		if old, ok := c.entries[oldest]; ok && old != entry {
			delete(c.entries, oldest)
		}
	}
	return entry
}

// ensureFeatureResult 返回某区块的地物写入结果。base 可选：如果调用方已经
// 生成了该区块的基础地形，可直接复用，避免重复地形生成。
func (r *Router) ensureFeatureResult(chunkX, chunkZ int, base *GeneratedChunk, biomes *BiomeSource) *featureResult {
	if r.featureCache == nil {
		return nil
	}
	key := chunkCoord{x: chunkX, z: chunkZ}
	entry := r.featureCache.get(key)
	entry.once.Do(func() {
		chunk := base
		if chunk == nil {
			var err error
			chunk, _, err = r.generateBaseChunk(chunkX, chunkZ)
			if err != nil {
				return
			}
			biomes, _ = r.Biomes()
		}
		if chunk == nil || biomes == nil || r.features == nil {
			return
		}
		result := &featureResult{
			writes:   make(map[[3]int]SurfaceBlock, 512),
			entities: make(map[[3]int]GeneratedBlockEntity),
		}
		// 记录完整结果（包括当前区块内的写入），这样该区块以后作为
		// 中心区块生成时可以直接重放结果，而不必再次执行全部地物。
		// 邻域合并时仍只取落在中心区块内的坐标。
		chunk.capture = result.writes
		chunk.captureEntities = result.entities
		chunk.captureOutside = false
		r.applyFeatures(chunk, biomes)
		chunk.capture = nil
		chunk.captureEntities = nil
		r.featureCache.setResult(entry, result)
		// 只有邻域探测生成的独立 base chunk 才能交给 prepared 缓存。
		// 中心生成路径传入的 base 就是当前返回值，后续结构/调用方还会
		// 修改它；若缓存指针会导致下次复用时重复应用结构。
		if base == nil {
			r.featureCache.setPrepared(key, result, chunk)
		}
	})
	return r.featureCache.resultFor(entry)
}

// applyRegionFeatures 执行中心区块及 3×3 邻域的地物，并把所有落在当前区块
// 的写入合并进来。结果按 3×3 缓存，连续生成相邻区块时每个区块的地物只
// 执行一次。
func (r *Router) applyRegionFeatures(chunk *GeneratedChunk, biomes *BiomeSource, prepared bool) {
	if r.features == nil {
		return
	}
	if r.featureCache == nil || !r.regionsEnabled {
		r.applyFeatures(chunk, biomes)
		return
	}
	apply := func(result *featureResult) {
		if result == nil {
			return
		}
		for pos, block := range result.writes {
			if chunk.InRange(pos[0], pos[1], pos[2]) {
				chunk.SetBlock(pos[0], pos[1], pos[2], block)
			}
		}
		for pos, entity := range result.entities {
			if chunk.InRange(pos[0], pos[1], pos[2]) {
				chunk.SetBlockEntity(pos[0], pos[1], pos[2], entity)
			}
		}
	}
	// 当前区块的完整地物结果（含自身写入）已经在计算邻域时缓存。
	// 直接重放它，避免同一区块从邻域变成中心时重复执行全部地物。
	key := chunkCoord{x: chunk.ChunkX, z: chunk.ChunkZ}
	entry := r.featureCache.peek(key)
	centerResult := (*featureResult)(nil)
	if entry != nil {
		centerResult = r.featureCache.resultFor(entry)
	}
	if centerResult != nil && !prepared {
		apply(centerResult)
	} else if centerResult == nil {
		// 首次生成中心区块：ensureFeatureResult 已经把自身地物写入
		// base chunk，这里不再重复回放。
		r.ensureFeatureResult(chunk.ChunkX, chunk.ChunkZ, chunk, biomes)
	}
	for dz := -1; dz <= 1; dz++ {
		for dx := -1; dx <= 1; dx++ {
			if dx == 0 && dz == 0 {
				continue
			}
			apply(r.ensureFeatureResult(chunk.ChunkX+dx, chunk.ChunkZ+dz, nil, biomes))
		}
	}
}
