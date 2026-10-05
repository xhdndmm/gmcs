# 性能基准

本文件记录 gmcs 关键路径的 Go micro-benchmark 方法与实测结果。

> 说明：这些数字是**开发机参考基线**，用于发现回归与对比优化前后差异，
> 不代表真实服务器吞吐。真实多玩家负载压测仍在计划中（见第 4 节）。

## 1. 环境与复现

- 环境：Linux amd64，12th Gen Intel(R) Core(TM) i7-12700F（20 逻辑核心），Go 1.27.1
- **更新于 2026-10-05**：第 2 节与第 3.3 节随最新代码全部复测；
  表中为 `-count=6` 中位数（压缩基准 `-count=3`）。
- internal 包基准未启用 PGO（内置 PGO 配置 `cmd/gmcs/default.pgo` 仅影响
  主程序构建）；PGO 对照见第 3.3 节。
- 所有 benchmark 均为单进程、单线程逻辑（`-cpu` 未指定），数字随硬件与
  系统负载波动，同机对比时请保证条件一致。

复现命令：

```bash
scripts/test.sh --bench          # 全部基准
# 等价于：
go test -run=^$ -bench=. -benchmem ./...
```

单基准模板：`go test -count=6 -run '^$' -bench=基準名 ./内部包 -benchmem`。

## 2. 当前基准快照（实测）

本节始终对应最新代码；各次优化的历史过程见第 3 节。

| Benchmark | 包 | 耗时 | 内存 | 分配次数 |
| --- | --- | --- | --- | --- |
| `BenchmarkGenerateChunk` | world | ≈732 µs/op | 72,766 B/op | 18 allocs/op |
| `BenchmarkEncodeChunkDataPacket` | world | ≈63.0 µs/op | 6,473 B/op | 4 allocs/op |
| `BenchmarkAppendChunkDataPacketReuse` | world | ≈62.4 µs/op | 6,474 B/op | 4 allocs/op |
| `BenchmarkEncodeEntityPositionSync` | protocol | ≈68.7 ns/op | 120 B/op | 4 allocs/op |
| `BenchmarkEncodeAddEntity` | protocol | ≈104.3 ns/op | 176 B/op | 4 allocs/op |
| `BenchmarkWritePacketWithCompression`（60 KB） | protocol | ≈75.3 µs/op | 16–85 B/op | 1 allocs/op |
| `BenchmarkServerTick`（32 生物） | server | ≈8.1 µs/op | 3,190 B/op | 64 allocs/op |
| `BenchmarkItemTick`（128 掉落物） | server | ≈9.5 µs/op | 1 B/op | 0 allocs/op |

各基准覆盖的内容：

- `BenchmarkGenerateChunk`：用固定种子生成一个区块（高度图 + 层次 + 水域/沙滩 + 植被），
  覆盖地形生成热路径。
- `BenchmarkEncodeChunkDataPacket`：把已生成区块编码为 1.21.11 Chunk Data and Update
  Light 包（调色板容器 + heightmap + 全亮天空光）。
- `BenchmarkAppendChunkDataPacketReuse`：复用输出缓冲连续编码（进入世界/移动时
  区块流式发送的实际路径，与上者相同，含 heightmap）。
- `BenchmarkWritePacketWithCompression`：60 KB 大包的压缩发送路径
  （长度前缀 + zlib + 帧写出）。
- `BenchmarkEncodeEntityPositionSync` / `BenchmarkEncodeAddEntity`：高频实体包的编码成本
  （含 LpVec3）。
- `BenchmarkServerTick`：`Server.tick()` 一次，含 32 只僵尸的游荡移动、卡住判定与
  广播筛选；场景中无玩家（走游荡分支）。**不覆盖**追击/攻击分支、区块加载/保存、
  玩家会话与网络发送。
- `BenchmarkItemTick`：`Server.tick()` 一次，含 128 个掉落物的年龄/拾取延迟、
  重力与摩擦、合并扫描与拾取判定；场景中无玩家（走不拾取分支）。

推算（基于上表，仅供规划参考）：视距 10 进入世界需发送 21×21＝441 个区块，
按编码 63 µs/区块计算约 28 ms 纯编码时间（不含地形生成与网络 IO）。

## 3. 优化记录

### 3.1 区块编码重构（c58bd09，当时实测）

对 `EncodeChunkDataPacket` 做了一次针对热路径的重构，同机前后对比如下
（注：本小节为当时记录；其“优化后”数字已由后续 3.2 节进一步改进，
当前值见第 2 节）：

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

### 3.2 内存与热路径优化（fbf619a，实测）

来自 CPU/分配 profile 的三处热点与对应处理（同机会话内对比，`-count=6` 中位数；
本表为当时的同窗对照，最新快照见第 2 节——编码 ≈57.4 µs、压缩 ≈74.3 µs、Tick ≈8.1 µs）：

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

### 3.3 构建优化：PGO 与产物体积（502ce61，数据复测于最新代码）

构建脚本（`scripts/build.sh`）的发布参数：

| 参数 | 作用 |
| --- | --- |
| `CGO_ENABLED=0` | 纯 Go 静态二进制，无系统库依赖 |
| `-trimpath` | 去除本机构建路径（可复现构建，略减小体积） |
| `-ldflags "-s -w -buildid="` | 剥离符号表与 DWARF、去除构建 ID |
| `-pgo=auto` | 自动使用 `cmd/gmcs/default.pgo`（随仓库提交的 PGO 配置） |

#### 产物体积（linux/amd64）

| 参数组合 | 体积 |
| --- | --- |
| `-trimpath -ldflags "-s -w"`（旧链接参数，PGO=off） | 7,794,848 B（7.43 MiB） |
| `-trimpath -ldflags "-s -w -buildid="`、`PGO=off` | 7,794,812 B |
| 新参数（含 PGO，`default.pgo` 24,131 B） | 7,852,156 B（7.49 MiB，+0.74%） |

说明：剥离符号（`-s -w`）与 `-trimpath` 之前已在使用；`-buildid=` 对体积影响可忽略
（主要用于可复现构建），PGO 因内联/去虚拟化会小幅增加体积。需要最小体积时用
`PGO=off scripts/build.sh`。

#### PGO 前后 benchmark 对比

`cmd/gmcs/default.pgo` 由 `scripts/genpgo.sh` 从世界生成/编码、实体包、服务器
Tick 的 benchmark 采样并合并生成（当前约 24 KB，随热路径变化重新生成后复测）。
当前代码的对照如下（`-count=6` 中位数，Go 1.27.1，i7-12700F）：

| Benchmark | `-pgo=off` | PGO | 变化 |
| --- | --- | --- | --- |
| `BenchmarkGenerateChunk` | ≈732 µs/op | ≈729 µs/op | ≈ -0.4% |
| `BenchmarkEncodeChunkDataPacket` | ≈63.0 µs/op | ≈61.1 µs/op | ≈ -3.1% |
| `BenchmarkEncodeEntityPositionSync` | ≈68.7 ns/op | ≈68.6 ns/op | ≈ -0.2%（噪声范围） |
| `BenchmarkEncodeAddEntity` | ≈104.3 ns/op | ≈99.0 ns/op | ≈ -5.1% |
| `BenchmarkServerTick`（32 生物） | ≈8.0 µs/op | ≈7.5 µs/op | ≈ -5.7% |

结论：收益约 0.2%–5.7%；`EncodeEntityPositionSync` 的 -0.2%（约 0.1 ns）在噪声
范围内。注：列高度缓存（3.2）消除了原先占 Tick 大半的整列扫描热点，Tick 的
PGO 增益从首次引入时的 ≈ -20.5%（当时热点仍在，见提交 502ce61）缩小到
≈ -5.7%；本轮 heightmap（3.5）引入后已重新生成 `default.pgo` 并复测；掉落物批次（3.6）
后再次重新采样（对照表数字仍为上一轮实测）。
以上为单机 micro-benchmark，不代表真实服务器吞吐；真实负载验证仍在计划中
（见第 4 节）。

#### 重新生成 PGO 配置

```bash
scripts/genpgo.sh                 # 默认 benchtime 1s
scripts/genpgo.sh --benchtime=3s  # 更长采样，数值更稳
```

建议在热路径代码明显变化或升级 Minecraft/协议版本后重新生成并提交。

复现 PGO 对比：

```bash
go test -count=6 -run '^$' -bench=. -pgo=off ./internal/world ./internal/protocol ./internal/server
go test -count=6 -run '^$' -bench=. -pgo=cmd/gmcs/default.pgo ./internal/world ./internal/protocol ./internal/server
```

### 3.4 区块缓存内存卸载（d49dd70，手工测量）

区块内存缓存过去只增不减：玩家跑图经过的每个区块都会常驻内存。现在服务器每
5 秒把距所有玩家都超过 `view_distance + 4` 的区块移出缓存（未保存的先写盘），
缓存大小只与“当前活跃区域 + 一个扫描周期内的移动距离”有关。

测量方法（默认跳过，避免依赖 GC 时序的测量进入常规测试）：

```bash
GMCS_MEM_DEMO=1 go test -count=1 -run TestChunkMemoryDemo -v ./internal/world/
```

一次实测（Go 1.27.1，i7-12700F）：生成并缓存 40×40 = 1600 个区块后
`HeapAlloc ≈ 111.9 MiB`（约 70 KiB/区块）；调用卸载（保留中心 11×11 = 121
个区块）并 GC 后 `HeapAlloc ≈ 9.2 MiB`，即区块数据被真正回收（约 100 MiB 回落）。

注意：这一数字是单次手工测量，仅作量级参考；Go 运行时不保证把已回收的堆立即
归还操作系统，进程 RSS 可能下降较慢，但区块数据本身不再被引用。

### 3.5 区块 heightmap（a59a859，实测）

Chunk Data 包此前发送空 heightmaps；现在发送客户端渲染/光照所需的
WORLD_SURFACE（1）与 MOTION_BLOCKING（4）：9 位/列、每 long 7 个值、
37 个 long，两份共用同一份打包数据。本世界的方块非固体即流体，两者取值
一致（列最高非空气方块 y - WorldMinY + 1）。

编码开销（同一台机器交叉复测的 `-count=8` 中位数，Go 1.27.1，i7-12700F；
对比对象为引入 heightmap 前的提交 6cef070）：

| 指标 | 引入前 | 引入后 | 变化 |
| --- | --- | --- | --- |
| `BenchmarkEncodeChunkDataPacket` | ≈56.7 µs/op | ≈62.6 µs/op | ≈ +10.4% |
| 每区块分配 | 6,150 B/op（3 次） | 6,473 B/op（4 次） | +323 B |

说明：高度扫描逐列进行且不写列高度缓存（编码持读锁，写缓存需要写锁，保持
无状态避免锁升级）；按视距 10（441 区块）推算，整次进入世界的纯编码增量
约 +2.6 ms（≈25→28 ms），相对地形生成与网络 IO 可忽略。引入后已重新生成
`default.pgo`（见 3.3）。

### 3.6 掉落物实体与区块批量（本批）

本批新增掉落物系统（丢弃/拾取/物理/持久化）与区块批量协议，Tick 路径新增
`tickItems`（遍历掉落物：年龄与拾取延迟、重力与摩擦、合并扫描、拾取判定）。

- 新增 `BenchmarkItemTick`（128 个掉落物、无玩家）：≈9.5 µs/op、≈1 B/op、0 allocs/op
  （`-count=5` 中位数，含 `Server.tick()` 固定开销）。
- 批次后复测 `BenchmarkServerTick`（32 生物、无掉落物）：≈8.1 µs/op、
  ≈3,190 B/op、64 allocs/op，与 3.2 记录的 ≈8.0 µs/op、64 allocs/op 一致
  （单次运行波动约 ±3%，差异在噪声范围内）。
- 掉落物列表为空时 `tickItems` 仅一次 map 遍历，不产生分配。
- `cmd/gmcs/default.pgo` 已在本批后重新采样生成（采样包含新基准）。

## 4. 尚未覆盖

- 真实多玩家并发负载（登录风暴、区块流式加载压测、实体密度压力）
- `go tool trace` 调度与阻塞分析
- 区块保存/加载（区域文件读写）路径的 benchmark
- 端到端连接压测（真实 socket + 加密 + 多包交织；当前仅覆盖编解码与压缩）
- 内存随在线时长/跑图的持续观测（当前仅区块卸载演示与快照测量）
- 掉落物高密度场景（数百掉落物的物理与广播）与长时间运行的内存增长观测
