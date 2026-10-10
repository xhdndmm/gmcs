package server

import (
	"net"
	"testing"
	"time"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
	"gmcs/internal/world"
)

// TestPacketBudget 验证令牌桶：突发额度用尽后按速率补充。
func TestPacketBudget(t *testing.T) {
	var b packetBudget
	now := time.Unix(1000, 0)
	for i := 0; i < 5; i++ {
		if !b.allow(now, 2, 5) {
			t.Fatalf("burst token %d denied", i)
		}
	}
	if b.allow(now, 2, 5) {
		t.Fatal("burst should be exhausted")
	}
	// 0.6 秒补充 1.2 个令牌（rate=2/s）：先放行一个、随后拒绝。
	if !b.allow(now.Add(600*time.Millisecond), 2, 5) {
		t.Fatal("refilled token denied")
	}
	if b.allow(now.Add(600*time.Millisecond), 2, 5) {
		t.Fatal("only one token should refill in 0.6s")
	}
	// 长时间静默后不超过 burst 上限。
	now = now.Add(time.Hour)
	for i := 0; i < 5; i++ {
		if !b.allow(now, 2, 5) {
			t.Fatalf("token %d after idle denied", i)
		}
	}
	if b.allow(now, 2, 5) {
		t.Fatal("burst cap exceeded after idle")
	}
}

// TestChatRateLimitSmoke 验证聊天洪泛被限流且连接不受影响：
// 超过突发额度的聊天被丢弃，稍后额度恢复仍可正常执行命令。
func TestChatRateLimitSmoke(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Flooder")

	// 洪泛 20 条聊天（burst=10）：超出部分被丢弃，连接不应被断开。
	for i := 0; i < 20; i++ {
		sendChatMessage(t, conn, "flood")
	}
	// 等待令牌恢复后命令应正常响应（连接存活且限流可恢复）。
	time.Sleep(600 * time.Millisecond)
	sendChatCommand(t, conn, "/list")
	expectSystemChat(t, conn, "当前有")
	_ = instance
}

// TestChunkPacketCache 验证共享区块包缓存：同区块复用同一份只读字节，
// 内容修改或重新生成后失效。
func TestChunkPacketCache(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Cacher")
	_ = conn

	chunk, err := instance.testWorld().Chunk(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	pos := world.ChunkPos{X: 0, Z: 0}
	first := instance.chunkPacket(world.DimensionOverworld, pos, chunk)
	second := instance.chunkPacket(world.DimensionOverworld, pos, chunk)
	if &first[0] != &second[0] {
		t.Fatal("expected cache hit to share the same packet bytes")
	}

	// 修改内容（石头 → 空气，确保状态变化）→ 修订号递增 → 缓存失效并重新编码。
	if !instance.testWorld().SetBlock(1, world.WorldMinY+40, 1, world.AirBlock) {
		t.Fatal("SetBlock failed")
	}
	third := instance.chunkPacket(world.DimensionOverworld, pos, chunk)
	if &third[0] == &first[0] {
		t.Fatal("expected cache invalidation after block change")
	}

	// 卸载并重新加载（新的 *Chunk）→ 指针不同 → 缓存失效。
	// 先保存再清空缓存（走真实卸载路径）。
	if err := instance.testWorld().Flush(); err != nil {
		t.Fatal(err)
	}
	instance.testWorld().UnloadFar([]world.ChunkPos{{X: 1000, Z: 1000}}, 0)
	reloaded, err := instance.testWorld().Chunk(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded == chunk {
		t.Fatal("expected a fresh chunk instance after unload")
	}
	fourth := instance.chunkPacket(world.DimensionOverworld, pos, reloaded)
	if &fourth[0] == &third[0] {
		t.Fatal("expected cache invalidation after chunk reload")
	}
}

// TestChunkPacketCacheHitRate 模拟多玩家加入同区域：每个区块只编码压缩一次。
func TestChunkPacketCacheHitRate(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	bx, _, bz := instance.spawnPositionFor(world.DimensionOverworld)
	centerX, centerZ := int(bx)>>4, int(bz)>>4
	const players, radius = 10, 10
	for player := 0; player < players; player++ {
		for dx := -radius; dx <= radius; dx++ {
			for dz := -radius; dz <= radius; dz++ {
				pos := world.ChunkPos{X: centerX + dx, Z: centerZ + dz}
				chunk, err := instance.testWorld().Chunk(pos.X, pos.Z)
				if err != nil {
					t.Fatal(err)
				}
				_ = instance.chunkPacket(world.DimensionOverworld, pos, chunk)
			}
		}
	}
	total := (2*radius + 1) * (2*radius + 1)
	if misses := instance.chunkPackets.misses.Load(); misses != uint64(total) {
		t.Fatalf("misses = %d, want %d（每个区块只应编码一次）", misses, total)
	}
	if hits := instance.chunkPackets.hits.Load(); hits != uint64((players-1)*total) {
		t.Fatalf("hits = %d, want %d", hits, (players-1)*total)
	}
}

// TestRegistryPacketsShared 验证配置阶段共享数据包只编码一次且内容稳定。
func TestRegistryPacketsShared(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "Sharer")
	_ = conn

	if len(instance.registryPackets) == 0 {
		t.Fatal("registry packets not built")
	}
	if len(instance.tagsPacket) == 0 {
		t.Fatal("tags packet not built")
	}
	// 再次构建应得到相同内容（内容与会话无关）。
	packets, tags, err := buildRegistryPackets()
	if err != nil {
		t.Fatal(err)
	}
	if len(packets) != len(instance.registryPackets) {
		t.Fatalf("registry packet count = %d, want %d", len(packets), len(instance.registryPackets))
	}
	for i := range packets {
		if string(packets[i]) != string(instance.registryPackets[i]) {
			t.Fatalf("registry packet %d differs between builds", i)
		}
	}
	if string(tags) != string(instance.tagsPacket) {
		t.Fatal("tags packet differs between builds")
	}
}

// sendChatMessage 发送未签名聊天消息（限流测试用）。
func sendChatMessage(t *testing.T, conn net.Conn, text string) {
	t.Helper()
	packet := protocol.AppendVarInt(nil, protocol.PlayServerboundPacketIDChatMessage)
	packet = protocol.AppendVarInt(packet, int32(len(text)))
	packet = append(packet, text...)
	packet = protocol.AppendInt64(packet, 0)  // timestamp
	packet = protocol.AppendInt64(packet, 0)  // salt
	packet = append(packet, 0)                // 无签名
	packet = protocol.AppendVarInt(packet, 0) // 消息计数偏移
	packet = append(packet, 0, 0, 0, 0)       // acknowledged(3 字节) + checksum(1 字节)
	if err := protocol.WritePacketWithCompression(conn, packet, compressionThreshold); err != nil {
		t.Fatal(err)
	}
}

// BenchmarkChunkPacketCacheHit 基准：共享缓存命中（多玩家同区块）。
func BenchmarkChunkPacketCacheHit(b *testing.B) {
	cfg := config.Default()
	cfg.WorldDir = b.TempDir()
	cfg.SpawnMonsters = false
	instance, err := New(cfg)
	if err != nil {
		b.Fatal(err)
	}
	chunk, err := instance.testWorld().Chunk(0, 0)
	if err != nil {
		b.Fatal(err)
	}
	pos := world.ChunkPos{X: 0, Z: 0}
	instance.chunkPacket(world.DimensionOverworld, pos, chunk) // 预热
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = instance.chunkPacket(world.DimensionOverworld, pos, chunk)
	}
}
