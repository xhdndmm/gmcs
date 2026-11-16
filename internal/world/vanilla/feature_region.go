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
}

type featureResultEntry struct {
	once   sync.Once
	result *featureResult
}

type featureResultCache struct {
	mu      sync.Mutex
	entries map[chunkCoord]*featureResultEntry
	order   []chunkCoord
	limit   int
}

func newFeatureResultCache(limit int) *featureResultCache {
	return &featureResultCache{
		entries: make(map[chunkCoord]*featureResultEntry),
		limit:   limit,
	}
}

func (c *featureResultCache) peek(key chunkCoord) *featureResultEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.entries[key]
}

// clear 丢弃所有已完成/进行中的结果引用。进行中的 entry 仍可由其调用方
// 安全完成，只是不再留在缓存里；本方法用锁保证与 get/peek 并发安全。
func (c *featureResultCache) clear() {
	c.mu.Lock()
	c.entries = make(map[chunkCoord]*featureResultEntry)
	c.order = nil
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
			writes:   make(map[[3]int]SurfaceBlock, 256),
			entities: make(map[[3]int]GeneratedBlockEntity),
		}
		chunk.capture = result.writes
		chunk.captureEntities = result.entities
		chunk.captureOutside = true
		r.applyFeatures(chunk, biomes)
		chunk.capture = nil
		chunk.captureEntities = nil
		chunk.captureOutside = false
		entry.result = result
	})
	return entry.result
}

// applyRegionFeatures 执行中心区块及 3×3 邻域的地物，并把所有落在当前区块
// 的写入合并进来。结果按 3×3 缓存，连续生成相邻区块时每个区块的地物只
// 执行一次。
func (r *Router) applyRegionFeatures(chunk *GeneratedChunk, biomes *BiomeSource) {
	if r.features == nil {
		return
	}
	if r.featureCache == nil || !r.regionsEnabled {
		r.applyFeatures(chunk, biomes)
		return
	}
	// 当前区块先执行自己的地物；否则邻域树冠会改变中心区块的放置判定，
	// 也会让地物执行明显变慢。
	key := chunkCoord{x: chunk.ChunkX, z: chunk.ChunkZ}
	if entry := r.featureCache.peek(key); entry != nil && entry.result != nil {
		r.applyFeatures(chunk, biomes)
	} else {
		r.ensureFeatureResult(chunk.ChunkX, chunk.ChunkZ, chunk, biomes)
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
	for dz := -1; dz <= 1; dz++ {
		for dx := -1; dx <= 1; dx++ {
			if dx == 0 && dz == 0 {
				continue
			}
			apply(r.ensureFeatureResult(chunk.ChunkX+dx, chunk.ChunkZ+dz, nil, biomes))
		}
	}
}
