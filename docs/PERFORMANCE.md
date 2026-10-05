# 性能基准

本文件记录 gmcs 关键路径的 Go micro-benchmark 方法与实测结果。

> 说明：这些数字是**开发机参考基线**，用于发现回归与对比优化前后差异，
> 不代表真实服务器吞吐。真实多玩家负载压测与 CPU/内存 profiling 仍在计划中
> （见 [TODO.md](TODO.md)）。

## 环境与复现

- 环境：Linux amd64，12th Gen Intel(R) Core(TM) i7-12700F（20 逻辑核心），Go 1.27.1
- 复现命令：

```bash
scripts/test.sh --bench
# 等价于：
go test -run=^$ -bench=. -benchmem ./...
```

- 所有 benchmark 均为单进程、单线程逻辑（`-cpu` 未指定），数字随硬件与
  系统负载波动，同机对比时请保证条件一致。

## 基准一览（实测）

| Benchmark | 包 | 耗时 | 内存 | 分配次数 |
| --- | --- | --- | --- | --- |
| `BenchmarkGenerateChunk` | world | ≈112.5 µs/op | 72,556 B/op | 18 allocs/op |
| `BenchmarkEncodeChunkDataPacket` | world | ≈69.5 µs/op | 71,680 B/op | 4 allocs/op |
| `BenchmarkEncodeEntityPositionSync` | protocol | ≈68.3 ns/op | 120 B/op | 4 allocs/op |
| `BenchmarkEncodeAddEntity` | protocol | ≈104.0 ns/op | 176 B/op | 4 allocs/op |
| `BenchmarkServerTick`（32 生物） | server | ≈35.2 µs/op | 3,226 B/op | 64 allocs/op |

各基准覆盖的内容：

- `BenchmarkGenerateChunk`：用固定种子生成一个区块（高度图 + 层次 + 水域/沙滩 + 植被），
  覆盖地形生成热路径。
- `BenchmarkEncodeChunkDataPacket`：把已生成区块编码为 1.21.11 Chunk Data and Update
  Light 包（调色板容器 + 全亮天空光）。
- `BenchmarkEncodeEntityPositionSync` / `BenchmarkEncodeAddEntity`：高频实体包的编码成本
  （含 LpVec3）。
- `BenchmarkServerTick`：`Server.tick()` 一次，含 32 只僵尸的游荡移动、卡住判定与
  广播筛选；场景中无玩家（走游荡分支）。**不覆盖**追击/攻击分支、区块加载/保存、
  玩家会话与网络发送。

推算（基于上表，仅供规划参考）：视距 10 进入世界需发送 21×21＝441 个区块，
按编码 69.5 µs/区块计算约 31 ms 纯编码时间（不含地形生成与网络 IO）。

## 区块编码优化（实测优化前后对比）

对 `EncodeChunkDataPacket` 做了一次针对热路径的优化，同机前后对比如下：

| 指标 | 优化前 | 优化后 | 变化 |
| --- | --- | --- | --- |
| 耗时 | 889.8 µs/op | 69.5 µs/op | ≈12.8× 更快 |
| 内存 | 281,218 B/op | 71,680 B/op | ≈3.9× 更少 |
| 分配次数 | 50 allocs/op | 4 allocs/op | 12.5× 更少 |

优化内容（协议格式与输出字节不变）：

1. **调色板构建去 map 化**：原实现为每个 section 构建 `map[uint16]int` 并生成与
   section 等长的中间索引数组（4096 × uint16）；新实现用栈上局部数组（≤256 条目）
   做线性查找，典型调色板 ≤16 种方块时查找开销远低于 map。
2. **内联紧密位流打包**：新增 `packIndirect` 直接把方块写进 long 数组，省掉中间索引
   数组；原 `packBits` 保留为测试参考实现，并由 `TestPackIndirectMatchesPackBits`
   验证两者输出逐字节一致。
3. **包缓冲一次预分配**：`Chunk Data` 包按 64 KiB 预分配（区块数据 + 约 53 KiB 固定
   光照数据），消除 append 增长造成的反复拷贝。

正确性验证：`internal/world` 全部单元测试通过（含调色板/位流 round-trip 测试），
`go test -race ./...` 通过。

## 区块缓存内存行为（手工测量）

区块内存缓存过去只增不减：玩家跑图经过的每个区块都会常驻内存。现在服务器每
5 秒把距所有玩家都超过 `view_distance + 4` 的区块移出缓存（未保存的先写盘），
缓存大小只与“当前活跃区域 + 一个扫描周期内的移动距离”有关。

测量方法（默认跳过，避免依赖 GC 时序的测量进入常规测试）：

```bash
GMCS_MEM_DEMO=1 go test -count=1 -run TestChunkMemoryDemo -v ./internal/world/
```

一次实测（Go 1.27.1，i7-12700F）：生成并缓存 40×40 = 1600 个区块后
`HeapAlloc ≈ 111.8 MiB`（约 70 KiB/区块）；调用卸载（保留中心 11×11 = 121
个区块）并 GC 后 `HeapAlloc ≈ 9.1 MiB`，即区块数据被真正回收（约 100 MiB 回落）。

注意：这一数字是单次手工测量，仅作量级参考；Go 运行时不保证把已回收的堆立即
归还操作系统，进程 RSS 可能下降较慢，但区块数据本身不再被引用。

## 尚未覆盖

- 真实多玩家并发负载（登录风暴、区块流式加载压测、实体密度压力）
- `go tool pprof` CPU/heap/allocation 分析
- `go tool trace` 调度与阻塞分析
- 区块保存/加载（区域文件读写）路径的 benchmark
- 网络编解码端到端（压缩、加密、帧处理）的 benchmark
