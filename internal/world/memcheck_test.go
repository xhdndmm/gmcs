package world

import (
	"os"
	"runtime"
	"testing"
)

// 手工内存测量（默认跳过，避免依赖 GC 时序的测量进入常规测试）：
//
//	GMCS_MEM_DEMO=1 go test -count=1 -run TestChunkMemoryDemo -v ./internal/world/
func TestChunkMemoryDemo(t *testing.T) {
	if os.Getenv("GMCS_MEM_DEMO") == "" {
		t.Skip("手工测量：设置 GMCS_MEM_DEMO=1 启用")
	}
	dir := t.TempDir()
	instance, err := Open(dir, SeededGenerator{Seed: 5})
	if err != nil {
		t.Fatal(err)
	}

	var before runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	// 模拟跑图：加载 40×40 = 1600 个区块。
	for x := 0; x < 40; x++ {
		for z := 0; z < 40; z++ {
			if _, err := instance.Chunk(x, z); err != nil {
				t.Fatal(err)
			}
		}
	}
	var loaded runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&loaded)
	t.Logf("加载 1600 区块后：HeapAlloc=%.1f MiB（缓存区块=%d）", float64(loaded.HeapAlloc)/1024/1024, instance.ChunkCount())

	// 玩家离开：只保留中心附近 11×11 区块。
	unloaded, err := instance.UnloadFar([]ChunkPos{{X: 20, Z: 20}}, 5)
	if err != nil {
		t.Fatal(err)
	}
	var after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&after)
	t.Logf("卸载 %d 个区块后：HeapAlloc=%.1f MiB（缓存区块=%d）", unloaded, float64(after.HeapAlloc)/1024/1024, instance.ChunkCount())
}
