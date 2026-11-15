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
	if r.featureCache == nil {
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
	// 邻域只重放越界写入；当前区块自身最后执行，保持其地物优先级。
	for dz := -1; dz <= 1; dz++ {
		for dx := -1; dx <= 1; dx++ {
			if dx == 0 && dz == 0 {
				continue
			}
			apply(r.ensureFeatureResult(chunk.ChunkX+dx, chunk.ChunkZ+dz, nil, biomes))
		}
	}
	key := chunkCoord{x: chunk.ChunkX, z: chunk.ChunkZ}
	if entry := r.featureCache.peek(key); entry != nil && entry.result != nil {
		r.applyFeatures(chunk, biomes)
		return
	}
	r.ensureFeatureResult(chunk.ChunkX, chunk.ChunkZ, chunk, biomes)
}
