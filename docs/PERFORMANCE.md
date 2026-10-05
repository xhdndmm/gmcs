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

- **更新于 2026-10-05**（Go 1.27.1）；表内为 `-pgo=off` 基线中位数，绝对值
  随工具链/硬件/负载变化，仅用于同机对比。
- 所有 benchmark 均为单进程、单线程逻辑（`-cpu` 未指定），数字随硬件与
  系统负载波动，同机对比时请保证条件一致。

## 基准一览（实测）

| Benchmark | 包 | 耗时 | 内存 | 分配次数 |
| --- | --- | --- | --- | --- |
| `BenchmarkGenerateChunk` | world | ≈735 µs/op | 72,767 B/op | 18 allocs/op |
| `BenchmarkEncodeChunkDataPacket` | world | ≈57.1 µs/op | 6,150 B/op | 3 allocs/op |
| `BenchmarkAppendChunkDataPacketReuse` | world | ≈56.5 µs/op | 6,150 B/op | 3 allocs/op |
| `BenchmarkEncodeEntityPositionSync` | protocol | ≈68.7 ns/op | 120 B/op | 4 allocs/op |
| `BenchmarkEncodeAddEntity` | protocol | ≈104.0 ns/op | 176 B/op | 4 allocs/op |
| `BenchmarkWritePacketWithCompression`（60 KB） | protocol | ≈75.7 µs/op | 50 B/op | 1 allocs/op |
| `BenchmarkServerTick`（32 生物） | server | ≈8.0 µs/op | 3,209 B/op | 64 allocs/op |

各基准覆盖的内容：

- `BenchmarkGenerateChunk`：用固定种子生成一个区块（高度图 + 层次 + 水域/沙滩 + 植被），
  覆盖地形生成热路径。
- `BenchmarkEncodeChunkDataPacket`：把已生成区块编码为 1.21.11 Chunk Data and Update
  Light 包（调色板容器 + 全亮天空光）。
- `BenchmarkAppendChunkDataPacketReuse`：复用输出缓冲连续编码（进入世界/移动时
  区块流式发送的实际路径）。
- `BenchmarkWritePacketWithCompression`：60 KB 大包的压缩发送路径
  （长度前缀 + zlib + 帧写出）。
- `BenchmarkEncodeEntityPositionSync` / `BenchmarkEncodeAddEntity`：高频实体包的编码成本
  （含 LpVec3）。
- `BenchmarkServerTick`：`Server.tick()` 一次，含 32 只僵尸的游荡移动、卡住判定与
  广播筛选；场景中无玩家（走游荡分支）。**不覆盖**追击/攻击分支、区块加载/保存、
  玩家会话与网络发送。

推算（基于上表，仅供规划参考）：视距 10 进入世界需发送 21×21＝441 个区块，
按编码 57 µs/区块计算约 25 ms 纯编码时间（不含地形生成与网络 IO）。

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

## 内存与热路径优化：压缩池化、发送缓冲复用、列高度缓存（实测）

来自 CPU/分配 profile 的三处热点与对应处理（同机会话内对比，`-count=6` 中位数）：

| 指标 | 优化前 | 优化后 | 变化 |
| --- | --- | --- | --- |
| 区块编码 `BenchmarkEncodeChunkDataPacket` | 67.5 µs/op；71,680 B/op（4 次） | 57.1 µs/op；6,150 B/op（3 次） | ≈ -15%；分配 ≈ -91% |
| 大包压缩发送 `BenchmarkWritePacketWithCompression` | 200.4 µs/op；1,076,443 B/op（20 次） | 75.7 µs/op；50 B/op（1 次） | ≈ -62%；分配 ≈ -99.99% |
| 服务器 Tick `BenchmarkServerTick`（32 生物） | 36.8 µs/op | 8.0 µs/op | ≈ -78% |

优化内容：

1. **协议压缩路径对象池**（internal/protocol/frame.go）：此前每个 ≥256 字节的包
   都新建 `zlib.Writer`（窗口与哈希表合计约 1 MB 分配）与两个缓冲；现在复用
   writer/buffer，并把帧分三次顺序写出，省去压缩数据的整段拷贝。
2. **区块发送缓冲复用**（internal/world/chunk.go、internal/server/chunks.go）：
   新增 `AppendChunkDataPacket`，会话流式发送区块时复用同一 64 KB 缓冲
   （写出同步，可安全覆盖）；section 数据暂存改用 `sync.Pool`。
3. **列高度缓存**（internal/world/chunk.go `Column`/`ColumnAt`、
   internal/server/entity.go）：此前生物每步移动要做 2 次×约 380 格的整列
   扫描（`TopBlock` + `TopSolidY`）；现在一次查询获得两者，结果按列缓存，
   `SetBlockState` 自动失效（内存开销：仅在被查询过的区块上约 1 KB/区块）。
4. **试过但回退**：直接追加的融合位流打包（`appendPackedIndirect`）实测比
   `packIndirect` 慢约 25%（寄存器内累积的循环依赖），按“不为微小分配
   牺牲 CPU”的原则保留原实现。

推算（仅供规划参考）：进入世界（视距 10，441 区块）的编码+压缩耗时
≈118 ms → ≈58 ms；每区块临时分配 ≈1.15 MB → ≈6 KB（整次进入
≈507 MB → ≈2.7 MB，显著降低 GC 压力）。

复现命令：

```bash
go test -count=6 -run '^$' -bench=BenchmarkEncodeChunkDataPacket ./internal/world -benchmem
go test -count=6 -run '^$' -bench=BenchmarkServerTick ./internal/server -benchmem
go test -count=3 -run '^$' -bench=BenchmarkWritePacketWithCompression -benchtime=2s ./internal/protocol -benchmem
```

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

## 构建优化：PGO 与产物体积（实测）

构建脚本（`scripts/build.sh`）的发布参数：

| 参数 | 作用 |
| --- | --- |
| `CGO_ENABLED=0` | 纯 Go 静态二进制，无系统库依赖 |
| `-trimpath` | 去除本机构建路径（可复现构建，略减小体积） |
| `-ldflags "-s -w -buildid="` | 剥离符号表与 DWARF、去除构建 ID |
| `-pgo=auto` | 自动使用 `cmd/gmcs/default.pgo`（随仓库提交的 PGO 配置） |

### 产物体积（linux/amd64）

| 参数组合 | 体积 |
| --- | --- |
| `-trimpath -ldflags "-s -w"`（旧参数） | 7,786,656 B（7.43 MiB） |
| `-trimpath -ldflags "-s -w -buildid="`、`PGO=off` | 7,786,620 B |
| 新参数（含 PGO） | 7,835,772 B（7.47 MiB，+0.63%） |

说明：剥离符号（`-s -w`）与 `-trimpath` 之前已在使用；`-buildid=` 对体积影响可忽略
（主要用于可复现构建），PGO 因内联/去虚拟化会小幅增加体积。需要最小体积时用
`PGO=off scripts/build.sh`。

### PGO 前后 benchmark 对比

`cmd/gmcs/default.pgo` 由 `scripts/genpgo.sh` 从世界生成/编码、实体包、服务器
Tick 的 benchmark 采样并合并生成（约 21 KB）。同机会话内对比（`-count=6` 取中位数，
Go 1.27.1，i7-12700F）：

| Benchmark | `-pgo=off` | PGO | 变化 |
| --- | --- | --- | --- |
| `BenchmarkGenerateChunk` | ≈735.0 µs/op | ≈720.9 µs/op | ≈ -1.9% |
| `BenchmarkEncodeChunkDataPacket` | ≈68.0 µs/op | ≈66.6 µs/op | ≈ -2.1% |
| `BenchmarkEncodeEntityPositionSync` | ≈68.7 ns/op | ≈70.6 ns/op | ≈ +2.7%（轻微回退） |
| `BenchmarkEncodeAddEntity` | ≈104.0 ns/op | ≈98.5 ns/op | ≈ -5.2% |
| `BenchmarkServerTick`（32 生物） | ≈36.7 µs/op | ≈29.1 µs/op | ≈ -20.5% |

结论：主要工作负载（服务器 Tick、区块生成/编码、实体包）收益约 2%–20%；
`EncodeEntityPositionSync`（绝对量 <2 ns）出现约 2.7% 的轻微回退，实际影响
可忽略。以上为单机 micro-benchmark，不等于真实服务器吞吐；真实负载验证仍在
计划中（见“尚未覆盖”）。对比数据与顶部基准表的绝对值可能因工具链/系统状态
不同而有差异，请以同次会话内对比为准。注：PGO 数据测量于后续内存/热路径
优化之前，两种优化叠加后的最新数字见上方基准一览与“内存与热路径优化”一节。

### 重新生成 PGO 配置

```bash
scripts/genpgo.sh                 # 默认 benchtime 1s
scripts/genpgo.sh --benchtime=3s  # 更长采样，数值更稳
```

建议在热路径代码明显变化或升级 Minecraft/协议版本后重新生成并提交。

### 复现对比

```bash
go test -count=6 -run '^$' -bench=. -pgo=off ./internal/world ./internal/protocol ./internal/server
go test -count=6 -run '^$' -bench=. -pgo=cmd/gmcs/default.pgo ./internal/world ./internal/protocol ./internal/server
```

## 尚未覆盖

- 真实多玩家并发负载（登录风暴、区块流式加载压测、实体密度压力）
- `go tool pprof` CPU/heap/allocation 分析
- `go tool trace` 调度与阻塞分析
- 区块保存/加载（区域文件读写）路径的 benchmark
- 网络编解码端到端（压缩、加密、帧处理）的 benchmark
