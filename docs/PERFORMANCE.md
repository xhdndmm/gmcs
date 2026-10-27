# 性能基准

本文件记录 gmcs 关键路径的 Go micro-benchmark 方法与实测结果。

> 说明：这些数字是**开发机参考基线**，用于发现回归与对比优化前后差异，
> 不代表真实服务器吞吐。真实多玩家负载压测仍在计划中（见第 4 节）。

## 1. 环境与复现

- 环境：Linux amd64，12th Gen Intel(R) Core(TM) i7-12700F（20 逻辑核心），Go 1.27
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
| `BenchmarkGenerateChunk` | world | ≈446 µs/op | 25,900 B/op | 52 allocs/op |
| `BenchmarkEncodeChunkDataPacket` | world | ≈81.9 µs/op | 159,676 B/op | 2 allocs/op |
| `BenchmarkAppendChunkDataPacketReuse` | world | ≈49.0 µs/op | 6 B/op | 0 allocs/op |
| `BenchmarkEncodeChunkPayload` | world | ≈65.1 µs/op | 98,304 B/op | 1 allocs/op |
| `BenchmarkEncodeCompressedChunkPayload` | world | ≈267 µs/op | ≈112 KB/op | 2 allocs/op |
| `BenchmarkEncodeEntityPositionSync` | protocol | ≈6.7 ns/op | 0 B/op | 0 allocs/op |
| `BenchmarkEncodeAddEntity` | protocol | ≈53.7 ns/op | 80 B/op | 1 allocs/op |
| `BenchmarkWritePacketWithCompression`（60 KB） | protocol | ≈70–75 µs/op | 17–250 B/op | 1 allocs/op |
| `BenchmarkServerTick`（32 生物） | server | ≈18.5 µs/op | 48 B/op | 1 allocs/op |
| `BenchmarkServerTickManyPlayers`（32 生物 + 100 玩家） | server | ≈332 µs/op | ≈3.8 KB/op | 32 allocs/op |
| `BenchmarkItemTick`（128 掉落物） | server | ≈30.9 µs/op | 92 B/op | 2 allocs/op |
| `BenchmarkItemMergeScan`（256 掉落物） | server | ≈208 µs/op | 14.7 KB/op | 229 allocs/op |
| `BenchmarkEntityInsideBlocks`（32 包围盒） | server | ≈1.35 µs/op | 0 B/op | 0 allocs/op |
| `BenchmarkHopperTick`（64 漏斗，持续搬运） | server | ≈35.8 µs/op | ≈48.6 KB/op | 50 allocs/op |
| `BenchmarkHopperChunkScan`（1 区块 16 漏斗） | server | ≈36.4 µs/op | 720 B/op | 4 allocs/op |
| `BenchmarkHopperScan`（64 漏斗全量扫描） | server | ≈251 µs/op | 3.0 KB/op | 7 allocs/op |
| `BenchmarkFindPath`（绕墙 ≈32 步） | world | ≈7.6 µs/op | 769 B/op | 1 allocs/op |
| `BenchmarkFindPathUnreachable`（512 节点耗尽） | world | ≈16.5 µs/op | 0 B/op | 0 allocs/op |
| `BenchmarkCollides`（单盒相交检测） | world | ≈50.1 ns/op | 0 B/op | 0 allocs/op |
| `BenchmarkClipMove`（单轴扫掠裁剪） | world | ≈94.2 ns/op | 0 B/op | 0 allocs/op |
| `BenchmarkLightningTickClear`（非雷暴） | server | ≈20.7 ns/op | 0 B/op | 0 allocs/op |
| `BenchmarkLightningTickThunder`（4 玩家雷暴） | server | ≈74.7 ns/op | 0 B/op | 0 allocs/op |
| `BenchmarkImprovedNoise`（单个 perlin 八度） | vanilla | ≈32.8 ns/op | 0 B/op | 0 allocs/op |
| `BenchmarkNormalNoiseValue`（continentalness，18 个八度） | vanilla | ≈948 ns/op | 0 B/op | 0 allocs/op |
| `BenchmarkNoiseRegistryLookup`（缓存命中） | vanilla | ≈10.5 ns/op | 0 B/op | 0 allocs/op |
| `BenchmarkBlendedNoiseValue`（old_blended_noise，24 个八度） | vanilla | ≈1.15 µs/op | 0 B/op | 0 allocs/op |

注：区块类基准的数据量随地形批次增长（洞穴/矿物/24 群系使单个区块从 ≈9.4 KB
增至 ≈22.6 KB，编码输出从 ≈66 KB 增至 ≈160 KB），耗时上升主要是内容量而非算法退化；
实体包与压缩路径与上一批持平。server 包多行为 2026-10-17 复测（见 3.25/3.26）：
其中 `ItemMergeScan` 的扫描节奏在 3.24 随掉落物物理对齐调整（见 3.24 的
≈224 µs 记录），`HopperTick` 为 64 漏斗持续搬运口径（≈0.56 µs/漏斗/tick，
含每次搬运的槽位数组拷贝，与 3.16 正文的 36.0 µs/50 allocs 一致；旧行
18.8 µs/557 B 为更早的空闲路径口径）。

各基准覆盖的内容：

- `BenchmarkGenerateChunk`：用固定种子生成一个区块（高度图 + 层次 + 水域/沙滩 + 植被），
  覆盖地形生成热路径（含跨区块树冠扫描与洞穴雕刻）。
- `BenchmarkEncodeChunkDataPacket`：把已生成区块编码为 1.21.11 Chunk Data and Update
  Light 包（调色板容器 + heightmap + 全亮天空光）。
- `BenchmarkAppendChunkDataPacketReuse`：复用输出缓冲连续编码（进入世界/移动时
  区块流式发送的实际路径，与上者相同，含 heightmap）。
- `BenchmarkEncodeChunkPayload` / `BenchmarkEncodeCompressedChunkPayload`：区块
  存储负载（未压缩 / zlib 压缩）的编码，对应 Flush/UnloadFar 的保存路径（见 3.8）。
- `BenchmarkWritePacketWithCompression`：60 KB 大包的压缩发送路径
  （长度前缀 + zlib + 帧写出）。
- `BenchmarkEncodeEntityPositionSync` / `BenchmarkEncodeAddEntity`：高频实体包的编码成本
  （含 LpVec3）。
- `BenchmarkServerTick`：`Server.tick()` 一次，含 32 只生物的游荡移动、受阻时的
  A* 寻路（4 邻接地表网格，512 节点上限，复用搜索工作区）与广播筛选，以及实体
  进入方块（压力板）扫描与经验球 Tick；场景中无玩家（走游荡分支、无经验球）。
  基准使用**蜘蛛**并固定为夜晚：僵尸/骷騷在白天阳光下会持续着火并死亡（原版行为），
  长时间基准的剩余生物数会漂移。
  开阔地形走直线（与寻路引入前同量级成本），每帧仅 1 次分配（寻路返回的路径）；
  按 20 TPS 折算 32 生物约 0.24 ms/s。**不覆盖**追击/攻击分支、区块加载/保存、
  玩家会话与网络发送。
- `BenchmarkItemTick`：`Server.tick()` 一次，含 128 个掉落物的年龄/拾取延迟、
  重力与摩擦、合并扫描（每 4 tick 一轮，空间索引）与拾取判定；场景中无玩家
  （走不拾取分支）。
- `BenchmarkEntityInsideBlocks`：对 32 个生物包围盒各调用一次实体进入方块扫描
  （普通地表、无压力板），覆盖区块解析、按列批量读取与状态数组过滤的常见路径
  （≈46 ns/包围盒、零分配）。
- `BenchmarkHopperTick`：64 个漏斗（每个下方有箱子）持续搬运（基准内每
  512 tick 补充物品并清空箱子，否则漏斗排空/箱满后只会测到空闲路径），
  覆盖冷却递减、搬运、写回方块实体与冷却持久化（≈0.56 µs/漏斗/tick；
  含按位置排序的确定推进顺序；场景中无玩家，广播走“无接收者”分支）。
- `BenchmarkHopperChunkScan`：区块加载时的增量注册路径（单区块扫描 +
  16 个漏斗逐个注册），成本与新增区块数成正比（≈36.4 µs/区块）。
- `BenchmarkHopperScan`：600 tick 一次的全量扫描兜底（64 漏斗 + 64 箱子场景），
  按 20 TPS 折算均值 ≈0.42 µs/tick（见 3.16）。
- `BenchmarkItemMergeScan`：256 个掉落物按 0.5 格间距密集排布（同类型间距
  1.0 格超出合并半径，种群稳定不合并），覆盖“密集物品”下每 4 tick 一轮的
  合并扫描（空间索引重建 + 逐物品邻桶扫描），见 3.17。
- `BenchmarkFindPath` / `BenchmarkFindPathUnreachable`：A* 寻路的单次成本
  （绕墙 ≈32 步；目标不可达时 512 节点耗尽的最坏情形），见 3.17。
- `BenchmarkPlayerEquipmentSnapshot`：装备快照刷新（会话读循环每收到一个数据包
  刷新一次，用于 Set Equipment 变更检测）≈126 ns/op、零分配；按每玩家 ~40 包/s
  与 100 玩家折算约 0.5 ms/s（见 3.19）。
- `BenchmarkCollides` / `BenchmarkClipMove`：单个实体碰撞盒的相交检测与
  单轴扫掠裁剪（普通地表），见 3.17。

推算（基于上表，仅供规划参考）：视距 10 进入世界需发送 21×21＝441 个区块，
按编码 49 µs/区块计算约 22 ms 纯编码时间（不含地形生成与网络 IO）。

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
当前代码的对照如下（`-count=6` 中位数，Go 1.27，i7-12700F）：

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

一次实测（2026-10-05，紧凑存储上线后）：生成并缓存 40×40 = 1600 个区块后
`HeapAlloc ≈ 12.3 MiB`（约 7.9 KiB/区块）；调用卸载（保留中心 11×11 = 121
个区块）并 GC 后 `HeapAlloc ≈ 1.7 MiB`，即区块数据被真正回收（约 10.6 MiB 回落）。
紧凑存储之前的对应数字为 `≈111.9 MiB` / `≈9.2 MiB`（约 70 KiB/区块），
见 3.8。

注意：这一数字是单次手工测量，仅作量级参考；Go 运行时不保证把已回收的堆立即
归还操作系统，进程 RSS 可能下降较慢，但区块数据本身不再被引用。

### 3.5 区块 heightmap（a59a859，实测）

Chunk Data 包此前发送空 heightmaps；现在发送客户端渲染/光照所需的
WORLD_SURFACE（1）与 MOTION_BLOCKING（4）：9 位/列、每 long 7 个值、
37 个 long，两份共用同一份打包数据。本世界的方块非固体即流体，两者取值
一致（列最高非空气方块 y - WorldMinY + 1）。

编码开销（同一台机器交叉复测的 `-count=8` 中位数，Go 1.27，i7-12700F；
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

### 3.7 单机负载压测（gmcsload，单次实测）

新增 `cmd/gmcsload`（离线模式模拟客户端：登录 → 配置 → 进入世界 → 持续移动）
与 `pprof_address` 诊断监听，并在本机（i7-12700F，Linux，loopback）做了一次
单机压测：**50 个并发客户端、20 秒、每客户端 4 格/秒水平移动**，服务器配置为
视距 6、无生物、无边界。命令：

```bash
go run ./cmd/gmcsload -addr 127.0.0.1:25599 -players 50 -duration 20s -ramp 20ms
```

实测结果（客户端侧统计 + `/proc/<pid>` 服务器侧计数）：

| 指标 | 实测值 |
| --- | --- |
| 成功进入世界 | 50/50（进入世界耗时：平均 1 ms、p95 2 ms、最长 2 ms） |
| 收包总数 / 流量 | 128,015 个包 / 506 MiB（约 517 KiB/s/客户端） |
| 包类型（数量） | EntityPositionSync 43.9%、EntityHeadRotation 43.9%、ChunkData 6.9%、PlayerInfoUpdate 2.0%、AddEntity 1.9% |
| 包类型（字节） | ChunkData 99.3%（其余包合计 <1%） |
| 服务器 CPU | 20 秒内 2.15 s CPU（约单核 11%） |
| 服务器内存 | VmRSS ≈ 78 MB（50 玩家在线） |

结论与局限：

- 流量几乎全部是区块流式发送（字节占 99.3%），压缩是主要 CPU 热点
（同一时段 CPU profile 中 `compress/flate` 约占 32% 采样、区块序列化约占
20%）；实体同步与头部朝向占包数 88% 但字节可忽略。
- 进入世界几乎瞬时（视距 6 共 169 区块），未出现登录风暴拥塞；此结论**不适用**于
更大的视距与更多并发（区块发送仍为同步批量，见已知限制）。
- 本数据为**单机 loopback** 结果，客户端与服务端共享同一台机器的 CPU/内存，
不代表真实网络环境；长期运行与更多玩家数已在本批补测（100 玩家 5 分钟，
见 3.13），真实客户端渲染仍未验证（仍见第 4 节）。
- 复跑（同一命令、当前构建含新 PGO）：117,349 个包 / 487 MiB、服务器 CPU 2.22 s/20 s、
VmRSS ≈ 74 MB（与上表差异在 ±10% 以内，属单次运行波动）。

### 3.8 区块紧凑存储与存储管线（本批，实测）

本轮针对“内存占用、释放与缓存”做了两处结构性优化。

**1. section 紧凑存储（internal/world/section.go）**

旧实现为每个已分配的 16×16×16 section 恒定分配 `4096 × uint16` = 8 KiB 数组；
种子地形单个区块约 9 个非空 section，即 ≈72 KiB，其中绝大多数位置是少数几种
方块（石头/泥土/水/空气）。新实现改为与原版同思想的“调色板 + 位流”：

- 全同方块（uniform）不分配数组（零值 = 全空气）；
- 1–4 种方块用 1–2 位索引（512 B–1 KiB/section）；
- 最多 256 种用 4–8 位；超过时退化为 16 位直接存储；
- 调色板满 256 项时先压缩掉历史未使用条目，仍放不下才升级到直接存储；
- 方块被全部挖空后回退为零值全空气（释放数组）。

磁盘格式**不变**（仍是每方块 2 字节的 v1 负载，兼容已有存档），网络协议
格式也不变。网络编码直接从紧凑存储按协议位宽重新打包（不再构建中间调色板/
索引数组），并对“所有位置同一种方块”的 section 走单值调色板（紧凑存储可能
保留历史未使用条目，不能只看位宽）。

**2. 存储管线（internal/world/storage.go、world.go）**

- 加载单个区块只读取目标槽位（`ReadAt`），不再把整个区域文件（最多 1024 个
  区块）全部解压进内存；
- 保存区域文件时，未更新的槽位按原始压缩数据原样复制（不解压、不重新压缩）；
- zlib 压缩器与缓冲区使用 `sync.Pool`（此前每个区块新建 writer，分配数百 KB）；
- 区块负载大小可精确计算，一次分配后按偏移写入（消除 append 增长反复拷贝）；
- 生成器改为生成期无锁写入（区块在 `World.Chunk` 锁内生成、尚未发布），
  消除了旧实现每个方块一次加解锁的开销。

**实测（同机 `-count=3` 中位数，Go 1.27，i7-12700F）**

| Benchmark | 优化前 | 优化后 | 变化 |
| --- | --- | --- | --- |
| `BenchmarkGenerateChunk` | 730 µs/op；72,766 B/op；18 allocs | 222 µs/op；9,444 B/op；33 allocs | 3.3× 更快；分配字节 7.7× 更少 |
| `BenchmarkEncodeChunkDataPacket` | 72.5 µs/op；72,108 B/op；5 allocs | 40.5 µs/op；65,870 B/op；1 alloc | 1.8× 更快 |
| `BenchmarkAppendChunkDataPacketReuse` | 61.9 µs/op；6,477 B/op；4 allocs | 30.3 µs/op；0 B/op；0 allocs | 2.0× 更快；零分配 |
| `BenchmarkEncodeChunkPayload` | 41.1 µs/op；315,264 B/op；9 allocs | 50.4 µs/op；81,920 B/op；1 alloc | 分配字节 3.8× 更少（展开紧凑存储略慢） |
| `BenchmarkEncodeCompressedChunkPayload` | 355 µs/op；1,393,123 B/op；29 allocs | 190 µs/op；125,678 B/op；3 allocs | 1.9× 更快；分配字节 11× 更少 |

> 注：`BenchmarkEncodeChunkDataPacket` 本轮加入了全局 sink（防止编译器把丢弃
> 结果的编码调用当作死代码消除），优化前列按同一加 sink 口径复测。

区块内存（`GMCS_MEM_DEMO=1`，1600 区块）：`HeapAlloc ≈ 111.9 MiB → ≈ 12.3 MiB`
（约 70 KiB/区块 → 约 7.9 KiB/区块）；卸载后 `9.2 MiB → 1.7 MiB`。

正确性验证：新增/更新测试包括 `TestSectionStorageSequence`（30 万次随机写入
对比参考模型，覆盖全部位宽增长与清空回退）、`TestAppendBlockStatesPacking`
（多种调色板规模 / 网络位宽的逐方块校验）、`TestSectionStorageUniformNetwork`
（单值调色板回归）、`TestChunkPayloadRoundTripSeeded`（种子地形逐方块编解码
校验）、`TestChunkStorageFootprint`（确定性内存回归断言：种子地形平均
<16 KiB/区块、超平坦 <4 KiB/区块）；`scripts/test.sh`（gofmt/build/vet/test/
race）全部通过。已按热路径变化重新生成 `cmd/gmcs/default.pgo`（24,131 B →
≈32 KB，本次 31,980 B），第 3.3 节 PGO 对照表未重测。

已知取舍：

- 紧凑存储的调色板是追加式的：方块类型被完全替换后条目可能残留。网络编码对
  全同 section 会退化为单值调色板；8 位满时会先压缩再升级直接存储，残留不会
  无界增长。
- 未压缩存储负载编码比旧实现略慢（需把紧凑存储展开为每方块 2 字节）；但生产
  保存路径是压缩编码，整条路径由 ≈396 µs/区块 降至 ≈240 µs/区块（1.65×）。

### 3.9 移动广播零分配（本批，实测）

pprof（`BenchmarkServerTick`，alloc_objects）显示 tick 分配的 ≈95% 来自
`EncodeEntityPositionSync`：从 nil slice 起 `append` 倍增扩容（每次 4 个分配、
120 B，每 tick 每移动生物一次）。

优化内容：

1. **编码预分配**：`EncodeAddEntity` / `EncodeEntityPositionSync` /
   `EncodeEntityDestroy` / 未压缩帧 payload 按固定布局精确预分配容量，
   消除倍增扩容（4 allocs → 1 allocs）。
2. **Append 变体 + 池缓冲**：新增 `AppendEntityPositionSync` /
   `AppendEntityHeadRotation`（追加到调用方缓冲）；`tickMobs` 与
   `broadcastPlayerMove` 的移动广播改用 `sync.Pool` 缓冲顺序复用
   （包写入是同步的（writePacket 持写锁完成），广播完成即归还），
   并把 `pendingMove` 从"已编码包"改为参数快照，广播时才编码。
3. **未压缩帧路径预分配**：`WritePacketWithCompression` 阈值下的
   payload 按 5+包长一次成型。

实测对比（同一机器，`-benchtime=300ms`）：

| Benchmark | Before | After | 变化 |
| --- | --- | --- | --- |
| `EncodeEntityPositionSync` | 66.96 ns/op，120 B，4 allocs | 6.68 ns/op，0 B，0 allocs | ≈10× 提速，零分配 |
| `EncodeAddEntity` | 102.4 ns/op，176 B，4 allocs | 53.7 ns/op，80 B，1 allocs | ≈1.9× 提速 |
| `ServerTick`（32 生物） | 7907 ns/op，3127 B，65 allocs | 6212 ns/op，1277 B，5 allocs | ≈1.27× 提速，分配 −92% |

正确性验证：`go test ./...`、`go test -race ./...` 全部通过
（含 `TestEncodeEntityMetadataSkinParts`、`TestMobCombatFlow` 等实体广播路径测试）。

已知取舍：

- 池缓冲在"构造→同步广播→归还"的顺序中复用同一 `[]byte`：若未来把网络写改为
  异步队列，必须先改为每包独立缓冲或引用计数，否则会破坏包内容。
- `BenchmarkEncodeAddEntity` 仍有 1 次分配（返回的包被持有跨多个玩家的同步写出）；
  进一步归零需要调用方传入缓冲，收益低于复杂度，未做。

### 3.10 碰撞形状查询（本批，实测）

本批引入方块碰撞形状表与共享 AABB 查询（`internal/world/collision.go`）：
玩家防穿墙/站立检测与掉落物逐轴移动从“点采样/0.05 步长近似”改为形状级
扫描裁剪（半砖/台阶/栅栏/门等非完整碰撞体参与碰撞）。

| 基准 | 结果 |
| --- | --- |
| `BenchmarkCollides`（玩家碰撞盒） | 151 ns/op，0 B/op，0 allocs/op |
| `BenchmarkClipMove`（逐轴扫描裁剪） | 294 ns/op，0 B/op，0 allocs/op |
| `BenchmarkServerTick`（32 生物，复测） | 6335 ns/op，1277 B/op，5 allocs/op（与 3.9 基本持平） |

正确性验证：`go test ./...`、`go test -race ./...` 全部通过
（含新增 `internal/world/collision_test.go`、`internal/registry/block_shapes_test.go`）。

### 3.11 地形/群系生成与形状级掉落物碰撞（本批，实测）

本批重写地形生成（大陆度/山脊/丘陵/细节噪声 + 群系与植被）并修复
`ChunkLoaded` 坐标语义（掉落物在非原点区块被误判未加载而冻结）。

| 基准 | 变化 |
| --- | --- |
| `BenchmarkGenerateChunk` | 213 → 231 µs/op，9.5 → 9.7 KB/op（多一层群系/装饰，约 +8%） |
| `BenchmarkCollides` | 153 ns/op，0 alloc（与 3.10 持平） |
| `BenchmarkItemTick`（128 掉落物） | ≈9.5 µs → ≈75 µs/op（形状级碰撞替换步进近似；
  绝对成本仍 <0.2% CPU（128 掉落物 × 20 TPS ≈ 1.5 ms/s），暂不做微优化） |
| `BenchmarkServerTick`（32 生物） | ≈5.0 µs/op（地形内容不同，与 3.9/3.10 同量级） |

已知取舍：碰撞查询每次按格取区块（w.Chunk + chunk.RLock），比点采样多锁开销；
如未来掉落物规模显著增大，可改为按区块批量查询摊薄锁成本（当前收益低于复杂度）。

### 3.12 多玩家共享内存与帧缓存（本批，50 玩家实测）

本批针对多玩家场景做共享/复用优化（gmcsload 50 玩家、视距 10、20 秒、
同种子同配置对比，均为单机 loopback 实测）：

- **共享压缩帧**：区块数据包压缩一次后跨连接共享（`protocol.CompressPacketFrame`），
  免去逐玩家重复编码与 zlib 压缩；配置阶段的注册表/标签包同样预构造共享。
- **区块包缓存**：2048 项 LRU（压缩帧约 0.5–2 KiB/项），按 `*Chunk` 指针 +
  修改计数（revision）失效；编码缓冲走 sync.Pool。同区域多玩家近 100% 命中
  （单元测试断言每区块只编码一次）。
- **保存编码移出世界锁**：Flush 先快照脏区块列表再在锁外压缩，避免保存
  阻塞方块查询/移动碰撞；区域文件句柄 LRU 缓存（32 个）省去反复 open/close。
- **安全限流**：会话级令牌桶（聊天/命令 2/s、移动 60/s、交互 50/s，含突发），
  防止单客户端包洪泛放大广播成本。

| 指标（50 玩家 20s） | 优化前 | 优化后 | 变化 |
| --- | --- | --- | --- |
| 服务器峰值内存 VmHWM | 60.5 MB | 44.5 MB | −26% |
| 常驻内存 VmRSS | 60.5 MB | 37.7 MB | −38% |
| 进程 CPU 时间 | 4.41 s | 1.41 s | −68% |
| `BenchmarkChunkPacketCacheHit` | 27 µs/op（每次编码+压缩） | 23 ns/op 0 alloc | ≈1100× |

正确性验证：`go test ./...`、`go test -race ./...` 全部通过（新增缓存共享/失效、
命中率、限流与注册表共享测试）。

### 3.13 地形/维度批次与异步预生成（本批，实测）

本批大幅扩展了游戏内容（洞穴/矿物/24 群系/河流/深板岩、下界与末地、四种生物、
红石、命令与日志），因此分为“内容量驱动的变化”与“结构性优化”两类记录。

**1. 异步区块预生成（internal/world）**

进入世界/跨区块发送前，`syncChunks` 先把待发送区块坐标交给预生成队列：

- 8 个 worker（上限 `AsyncPrefetchWorkers`，受 GOMAXPROCS 限制）；
- 请求去重：同一坐标只排队/生成一次（`pending map[ChunkPos]*chunkFuture`）；
- 主流程对每个区块等待 future 后取回，异步与同步生成结果逐格一致（有单测断言）；
- `Close` 会解析悬挂请求，避免关闭时等待死锁。

效果：区块生成从发送临界路径旁移（并发填满多核），首次进入大地形世界
不再串行等待；50/100 玩家压测中进入世界耗时保持 1 ms（p95 1 ms）。

**2. 区块编码快路径（内容量增长后的回归修复）**

洞穴/矿物/深板岩使用大量新方块状态，调色板变深、位宽变大。编码器新增快路径：
当 section 存储位宽与网络位宽一致且 64%bits == 0 时直接按 word 复制，
避免逐格 unpack→pack（此时编码输出从 158 KB 量级回到 ≈82 µs/op、2 allocs/op）。

**3. 单机压测（loopback，视距默认 10）**

| 场景 | CPU 时间 | VmHWM | VmRSS | 流量 | 收包 |
| --- | --- | --- | --- | --- | --- |
| 优化前基线 50 玩家 60 s（旧地形） | 3.47 s | 55.4 MB | 44.9 MB | 1353.9 MiB | 885,737 |
| 本批 50 玩家 60 s | 4.05 s | 59.6 MB | 59.6 MB | 1614.6 MiB | 933,109 |
| 本批 100 玩家 5 分钟 | 88.6 s | 63.0 MB | 56.1 MB | 4050.5 MiB | 32,871,919 |

说明：

- 50 玩家场景流量 +19%（区块内容变大，属内容量驱动）；CPU +17%（编码/压缩
  数据量变大 + 生物/红石 tick），内存 +7.6%（区块存储内容变多）。
- 100 玩家 5 分钟：全部 100 个客户端成功进入世界（平均 1 ms、p95 1 ms），
  无失败断开；服务器平均 CPU ≈0.30 核（88.6 s / 300 s），峰值内存 63 MB，
  常驻 56 MB、线程 35；流量 4.0 GiB（约 13.8 MB/s）。
- 包类型（字节）：ChunkData 75.9%、EntityPositionSync 22.6%；包数上实体同步
  与头部朝向占 99%。
- 仍为单机 loopback（客户端与服务端共享 CPU/内存），不代表真实网络环境。

### 3.14 生物 A* 寻路与移动成本（本批，实测）

本批把“直线追击 + 被挡侧移”升级为 A* 地表寻路（`internal/world/pathfind.go`，
4 邻接、512 节点/48 格上限、上跳 1 格/下落 ≤3 格），并针对 Tick 成本做了两轮实测：

| 实现 | `BenchmarkServerTick`（32 生物） |
| --- | --- |
| 寻路前（直线 AI） | ≈6.3 µs/op（历史值，见 3.10） |
| 朴素 A*（每 10 tick 无条件重算） | 304.3 µs/op，155 KB/op，1784 allocs/op |
| 直线优先 + 路径跟随（最终） | 18.8 µs/op，8.6 KB/op，75 allocs/op |

优化手段（均有 pprof 依据，热点为 `FindPath` → `ColumnAt` 的区块锁与列查询）：

- **直线优先**：开阔地形先按旧方式直线移动，只有移动被挡住时才做 A*；
  墙后等受阻场景由路径接手。
- **路径跟随至完成**：路径不再按时间过期（时间过期 + 直线回退会造成障碍
  边缘的往返振荡，曾使绕墙测试失败）；目标移动超过 2 格才重算。
- **失败冷却**：搜索失败（不可达）后 `mobPathRefreshTicks` 内不重复搜索。

按 20 TPS 折算，32 只生物约 0.38 ms/s（<0.1% CPU）；受阻场景（绕墙测试）
在 1500 tick 内完成绕行。

### 3.15 压力板与实体进入方块扫描（本批，实测）

压力板要求“实体进入方块”检测（原版 `Entity.checkInsideBlocks`）：玩家移动、
生物/掉落物/经验球位置推进后都要扫描实体包围盒覆盖的方块。朴素实现
（逐格 `World.BlockAt` + map 查表）在 `BenchmarkServerTick`（32 只持续移动的
僵尸）使 Tick 从 20.1 µs 涨到 29.4 µs（pprof：`entityInsideBlocks` 占 26.9%，
其中 75% 在 `BlockAt`）。优化分三层，全部保持语义等价：

| 版本 | `BenchmarkServerTick` |
| --- | --- |
| 本批基线（无压力板扫描；stash 复测） | 20.1 µs/op，9.7 KB/op，89 allocs/op |
| 朴素扫描（逐格 `BlockAt` + map 过滤） | 29.4 µs/op，12.5 KB/op，86 allocs/op |
| 数组查表 + 复用已解析世界 | 26.5 µs/op，12.6 KB/op，77 allocs/op |
| 再叠加按列批量读取（最终） | 25.5 µs/op，13.1 KB/op，83 allocs/op |

- **状态数组查表**：压力板状态表（`plateStateTable`）额外构建一个
  `[65536]uint8` 过滤表（`plateStateFast`，64 KB 只读），热路径一次数组访问
  即可排除空气/石头等全部非压力板状态，省掉每格一次 map 查询。
- **复用已解析的世界**：`entityInsideBlocks` 接收调用方已解析的 `*world.World`
  （生物/掉落物/经验球/玩家都在自己的推进路径里已有世界指针），去掉每次扫描的
  `worldFor` 取世界开销。
- **按列批量读取**：世界层新增 `World.LoadedColumn` / `Chunk.CopyColumn`
  （区块只解析一次、加锁一次，一列连续 y 一次读完），扫描改为
  x→z→y 的列优先遍历；未加载区块直接跳过（与原版一致：未加载区块不触发
  实体进入方块，也避免实体扫描意外生成区块）。
- **区块读取快路径**（同时惠及其他逐格读取路径）：`World.BlockAt` 在区块
  已加载时用读锁查询（`sync.RWMutex`），不再每格走 `Chunk()` 的排他锁与
  加载/生成分支；`Chunk.GetBlockState` 去掉 defer 解锁。

最终净成本：32 只持续移动的僵尸下 Tick +5.4 µs（≈0.11 ms/s，20 TPS 折算），
其中扫描约 3.5 µs；新增微基准 `BenchmarkEntityInsideBlocks`（32 个生物包围盒
一次扫描）为 1.46 µs/op、0 allocs/op，即约 **46 ns/包围盒**（普通地表、无压力板
的常见路径）。

### 3.16 漏斗传输的注册与 Tick 成本（本批，实测）

漏斗是方块级状态（无实体），需要两件事：**找到已加载区块里的漏斗**（注册）
与**每 tick 推进已注册漏斗**。

注册策略（避免每 20 tick 扫全世界的方块）：

| 方案 | 成本 | 说明 |
| --- | --- | --- |
| 每 20 tick 全量扫描（初版） | 257 µs/扫描（本基准场景） | 与区块总体积成正比，大视距下会成为周期尖峰 |
| 新加载区块增量注册（最终） | 36.4 µs/新增区块 | 只在区块进入内存时扫描该区块一次 |
| 全量扫描（600 tick 兜底，最终保留） | 251 µs/600 tick ≈ 0.42 µs/tick | 覆盖命令等非交互写入路径与极端溢出 |

- 增量路径靠世界层新增的区块加载跟踪：`World.EnableChunkLoadTracking` +
  `TakeLoadedChunks`（在 `Chunk()` 同步加载与预生成 worker 发布结果时记录，
  队列上限 8192 项且有低频全量扫描兜底），服务器每 tick 取走新加载区块并用
  `World.ChunkBlockStatePositions`（单区块、共享 section 调色板预筛）注册其中的漏斗。
- 区块卸载时（`UnloadFar`/`UnloadAll` 返回值）丢弃对应注册，避免注册表随跑图增长；
  内容与传送冷却已在每次变更后写回方块实体，卸载时无需再写盘。
- `BenchmarkHopperTick`（64 漏斗，持续搬运；基准内每 512 tick 重置物品与箱子，
  使冷却/搬运/写回路径在测量区间内保持活跃）：
  **本批代码前（`git` HEAD，无冷却持久化）29.6 µs/op ≈ 0.46 µs/漏斗/tick**，
  本批为 **36.0 µs/op ≈ 0.56 µs/漏斗/tick**（+22%）；按位置排序（保证推进顺序
  确定，不随 map 迭代顺序变化）约占 2 µs/op。差值即下一条的冷却持久化成本。
- **冷却持久化成本（本批新增，实测）**：为对齐原版 `TransferCooldown` 的持久化
  语义（重启/重载后从剩余冷却继续，而不是按 0 重新计时），冷却每 tick 递减时
  就地写回方块实体（`World.SetTransferCooldown`：一次世界锁内完成“查区块 + 改字段
  + 标脏”，区块锁只取一次；不重建槽位数组、无新增分配）。同一基准
  29.6 → 36.0 µs/op（+6.4 µs/op ≈ +0.10 µs/漏斗/tick；处于冷却期的漏斗占 7/8 的
  tick，即每次写回 ≈0.11 µs），分配数保持 50 allocs/op。绝对开销按 1000 个活跃
  冷却漏斗折算约 0.10 ms/tick（50 ms tick 预算的 0.2%）。
- `BenchmarkServerTick`（32 僵尸、无漏斗场景）在插入漏斗 Tick（空注册表：一次取
  队列 + 一次 map 迭代）前后 5 次采样区间重叠（基线 26.6–28.2 µs，本批 25.8–29.8 µs），
  差异在当前测量噪声范围内。

### 3.17 Tick 热路径：寻路、列读取、切片复用与合并索引（本批，实测）

本批以 CPU + 分配 profile 定位（当前代码未优化前：`FindPath` 占服务器 Tick CPU
的 50%，`tickMobs` 占分配的 49.6%，`mergeItems` 全表扫描与逐格 `BlockAt` 的
锁竞争次之），做了四组独立优化。同机、同基准、`-count=3` 中位数对照（基线
=34fad95）：

| 基准 | 优化前 | 优化后 | 变化 |
| --- | --- | --- | --- |
| `BenchmarkFindPath` | 78.0 µs/op，969 allocs | 7.6 µs/op，1 allocs | ≈10.2× |
| `BenchmarkFindPathUnreachable` | 171.7 µs/op，2028 allocs | 16.5 µs/op，0 allocs | ≈10.4× |
| `BenchmarkServerTick`（32 生物） | 25.8 µs/op，82–95 allocs | 11.6 µs/op，1 allocs | ≈2.2× |
| `BenchmarkItemTick`（128 掉落物） | 77.9 µs/op | 34.1 µs/op | ≈2.3× |
| `BenchmarkItemMergeScan`（256 掉落物） | 243.5 µs/op，232 allocs | 87.5 µs/op，214 allocs | ≈2.8× |
| `BenchmarkHopperTick`（64 漏斗） | 21.1 µs/op | 18.8 µs/op | ≈11% |
| `BenchmarkCollides` | 136.8 ns/op | 50.1 ns/op | ≈2.7× |
| `BenchmarkClipMove` | 269.4 ns/op | 94.2 ns/op | ≈2.9× |

1. **A* 寻路重写**（`internal/world/pathfind.go`）：原实现每节点分配
   `*pathInfo`（map 指针值）+ `container/heap` 接口装箱（Pop/Push 经由 `any`），
   不可达情形单次搜索分配 169 KB。新实现：
   - 用 `sync.Pool` 复用搜索工作区（开放寻址哈希表 + 显式二叉堆，无接口装箱）；
   - 表按 (x, z) 索引——网格对同一列总是返回同一个脚部 Y，因此每列（含不可行走
     的列）每次搜索最多查询一次网格，消除了扩展时对同一列的重复 `ColumnAt`；
   - 世代号区分两次搜索的表内容，避免每次清零；容量按最大插入量推导，
     保证探测不会遇到满表；
   - 返回路径仍为新分配切片（调用方持有），单次搜索 1 allocs。
2. **区块读取快路径**：`Chunk.Column` 缓存命中改走读锁（原先逐次排他锁）；
   `World.ChunkLoaded` 改读锁；`World.worldFor` 增加已创建维度的读锁快路径
   （原先每实体每帧一次排他锁 + map）——三者都在 tick 热路径上（寻路网格、
   掉落物/经验球的区块冻结检查、逐实体取世界）。
3. **Tick 临时切片复用**：`tickMobs`/`tickItems`/`tickXPOrbs` 的 pending 切片
   （移动/移除/拾取/压力板通知等）原为每帧重新分配，现改为 `Server.tickScratch`
   复用缓冲（入口截断、退出写回；tick 由单 goroutine 驱动）。
4. **掉落物/经验球合并空间索引**：`mergeItems` 原为每物品全表 `for range s.items`
   扫描（O(n²)，且 map 迭代本身开销高于实际合并判定）。改为桶格 2×2 的空间
   索引：每合并帧重建一次（桶切片跨帧复用，映射膨胀时换新防无界增长），
   扫描时按 3×3 邻桶取候选；索引是建桶时刻快照，已被合并掉的条目通过
   “仍在本表”校验跳过。桶格 2 格 + 邻域 1 格保证相对水平位移远小于 3 格/帧
   的候选不会漏检。经验球合并（`mergeXPOrbs`）同策略改造。
5. **碰撞/物理按列批量读取**：`Collides`/`ClipMove`/`SurfaceBelow` 原逐格
   `BlockAt`（每格两次 RWMutex + 一次 map 哈希），改为按 (x, z) 列一次
   `LoadedColumn`（区块解析一次、单次加锁、连续 y 读取）；区块不在内存时
   回退逐格查询，保持按需加载语义。另将掉落物 Tick 中破坏性方块与水中
   判定合并为同一次读取。

正确性：`./scripts/test.sh`（gofmt/build/vet/test/race）全绿；新增三个合并
回归测试（`TestItemMergeAcrossBuckets`、`TestItemMergeStableWithFullStack`、
`TestXPOrbMergeAcrossBuckets`）与两组寻路基准；协议输出与游戏行为不变。

### 3.18 复测：100 玩家 loopback 负载与稳定性（本批，实测）

本批改动（切石机选择语义、漏斗冷却持久化、区块负载 v9）后用 `cmd/gmcsload`
复测单机 loopback 负载。环境与前文一致（i7-12700F / 20 线程、Linux、本机 loopback，
客户端与服务端共享 CPU）；服务器为默认配置（视距 10、生成敌对生物、离线模式），
全新世界（首次运行随机种子），二进制为 PGO 构建。

**A. 100 玩家 × 3 分钟（稳定性）**

```bash
./gmcsload -addr 127.0.0.1:25599 -players 100 -duration 3m -ramp 20ms
```

| 指标 | 实测值 |
| --- | --- |
| 进入世界 | 100/100，失败 0；最短 1 ms、平均 1 ms、p95 2 ms、最长 3 ms |
| 运行 | 3m0.001s，全部客户端正常结束（无错误断开） |
| 收包 | 61,700,428 个；流量 4949.4 MiB（均值约 27.5 MiB/s） |
| 包类型（数量） | EntityPositionSync 51.3%、EntityHeadRotation 48.1%、SyncPlayerPos 0.5%、ChunkData 0.1%、Update Time 0.03% |
| 包类型（字节） | ChunkData 61.8%、EntityPositionSync 36.0%、EntityHeadRotation 1.7% |
| 服务器日志 | 全程 0 条 ERROR/WARN；玩家退出后空闲区块释放（441 + 5 + 7 区块）正常触发 |

**B. 100 玩家 × 60 秒（服务器资源采样）**

```bash
# 采样 /proc/<server-pid>/stat 的 utime+stime 差值
./gmcsload -addr 127.0.0.1:25599 -players 100 -duration 60s -ramp 20ms
```

| 指标 | 实测值 |
| --- | --- |
| 进入世界 | 100/100，失败 0；平均 2 ms、p95 3 ms、最长 4 ms |
| 收包 | 22,889,663 个；流量 3784.7 MiB（≈63 MiB/s） |
| 包类型（字节） | ChunkData 80.9%、EntityPositionSync 17.8% |
| 服务器 CPU | 60 s 内 67.4 s ≈ **1.12 核**（20 线程机器的 5.6%） |
| 服务器内存 | VmHWM（峰值）76.6 MB、结束后 VmRSS 60.9 MB、线程 33 |

结论与局限：

- 双次运行均无异常、无断开、无 ERROR/WARN；100 玩家同时进入世界的区块流式发送
  仍由同步批量完成（ChunkData 占字节的 62–81%），是 CPU 的主要去向。
- 60 秒窗口实际测的是“开局峰值阶段”（100 名玩家各自流式接收 441 个区块，
  与 3 分钟运行的前一分钟同量级：22.9M vs 61.7M 包），**不代表稳态**；
  3 分钟运行的后段 ChunkData 字节占比明显下降，与前文 3.13“100 玩家 5 分钟
  平均 0.30 核”并不矛盾（统计窗口与阶段不同，如需稳态数据应延长采样窗口）。
- 内存峰值 76.6 MB（3 分钟运行含 100 名玩家实体同步与 441 区块驻留），
  玩家全部退出后区块随空闲释放回落（VmRSS 60.9 MB，含 Go 运行时不归还的堆）。
- 仍为单机 loopback（客户端与服务端共享 CPU），不代表真实网络环境；
  真实客户端渲染仍未验证（见第 4 节）。

### 3.19 玩家装备同步的成本（本批，实测）

本批新增玩家装备同步（Set Equipment 0x64：主手/副手/盔甲，变更时广播给
64 格内玩家）。成本分两部分：

- **变更检测**：物品栏由会话串行访问，因此快照在会话读循环里刷新（每收到一个
  数据包刷新一次 6 个槽位并与上次快照比较）。`BenchmarkPlayerEquipmentSnapshot`
  实测 **≈126 ns/op、0 allocs/op**；按每玩家 ~40 包/s、100 玩家折算约
  0.5 ms/s（可忽略）。
- **广播**：仅在快照变化时发包（一次发全 6 个槽位，含 count=0 的空槽，约
  50 字节/次），并复用既有的 64 格附近广播路径；未变化时 `tick` 不做任何发送
  （有测试断言不产生待广播标记）。

正确性：`./scripts/test.sh`（gofmt/build/vet/test/race）全绿；
`TestSetEquipmentEncodingMatchesVanilla` 逐字节对照官方服务端抓包
（`64 56 80 01 a8 07 00 00 05 01 ca 07 00 00`），另有双向可见性
（加入时下发/切快捷栏/穿盔甲/空槽清空）的端到端测试。

### 3.20 火焰蔓延的成本（本批，实测）

本批把火焰从“只推进 age”升级为原版 `FireBlock.tick` 的完整实现：一次推进含 6 次
燃烧判定（每次 1 次方块状态读取 + 均匀随机数）与 3×3×6 空气蔓延候选（每个候选
最多 6 次邻域方块读取）。`BenchmarkFireTick` 的布局为石质地面上的火 + 一块相邻
橡木木板（保证火焰不会自行熄灭且蔓延循环命中目标），每次迭代重置火焰与木板，
并直接调用 `fireTickBody` 以跳过玩家半径门控（无玩家时门控会直接返回，测不到
推进成本）。实测（`-count=5`）：

| 指标 | 数值 |
| --- | --- |
| 时间 | ≈13.5 µs/op（5 次采样 13.53–13.72 µs/op） |
| 内存 | 152 B/op、2 allocs/op |

折算：火焰每 30–39 tick 推进一次；若 128 格内玩家附近同时存在 200 处火焰，
约 200 × 13.5 µs / 35 ≈ 77 µs/tick（≈0.15% 的 50 ms tick），可接受。

门控：与原版 1.21.11 一致，`tickFire` 先检查区块是否加载，再做玩家半径检查
（`ServerLevel.canSpreadFireAround`，默认 128 格）；半径外或未加载区块的火焰
只重新排程，不推进也不蔓延，因此远离玩家的火区不会持续产生 tick 成本
（这与原版引入该规则的目的相同）。

正确性：`./scripts/test.sh`（gofmt/build/vet/test/race）全绿。`internal/server/fire_test.go`
覆盖放置规则（必须是空气、灵魂火选择）、无燃料熄灭的 age 边界（age ≤ 3 保留）、
可燃判定（`isNearFuel`/`getIgniteOdds` 对照数值表）、燃烧相邻燃料、TNT 被点燃、
蔓延到空气的路径、无燃料时不会点燃空气、玩家半径与旁观者排除、维度永燃标签，
以及经计划刻队列的端到端路径（玩家附近的木质平台在 600 tick 内被烧掉）；
`internal/registry/fire_flammables_test.go` 校验 206 项数值表与方块标签查询。
尚未验证：与原版服务端的真实局面（点燃一片森林后逐 tick 对比世界状态）未做。

### 3.21 生物环境燃烧的成本（本批，实测）

本批新增：玩家与生物站在火/灵魂火/岩浆中被点燃、着火每秒伤害、入水熄灭，
着火标志经 Set Entity Data 同步（变化时才发包）并随 entities.json 持久化。
每个生物每 tick 新增的工作是：一次 `World.LoadedColumn`（一次加锁读脚部与
上方一格的方块状态）与 6 次状态判定（水/岩浆/火的区间判定），不产生分配。

同机同会话实测（`go test -count=6 -run '^$' -bench=BenchmarkServerTick ./internal/server -benchmem`，
32 只僵尸）：

| 版本 | 时间 | 内存 |
| --- | --- | --- |
| 本批前（同会话较早测得基线） | 13.4–13.9 µs/op | 50–60 B/op、1 alloc/op |
| 本批后 | 15.8–16.4 µs/op | 50–54 B/op、1 alloc/op |

即每只生物每 tick 约 +70 ns（32 只合计约 +2.3 µs/op），分配数不变。折算：
1000 只生物的服务器约 +70 µs/tick（≈0.14% 的 50 ms tick），可接受；这也是
把“是否处于火/水/岩浆”与“是否着火”检查放在同一个读锁下的原因。

实现过程中的两次修正（均用该基准验证，见提交记录）：

- 初版每帧无条件构造结果结构（指针返回）→ 32 allocs/op、23.2 µs/op；
  改为按值返回（无需广播时不分配）后回到 1 alloc/op、≈16.5 µs/op；
- 初版每只生物两次 `World.BlockAt`（两次加锁）并用方块名二分查找火方块 →
  改为一次 `LoadedColumn` + 状态区间判定后降到 15.8–16.4 µs/op。

正确性：`./scripts/test.sh`（含 `-race`）全绿；`mob_fire_test.go` 覆盖火中点燃
（时长、标志包、伤害）、入水熄灭、岩浆点燃与无敌帧、烧死掉落与标志清除、
`fire_ticks` 存档往返，以及“玩家站在火中被点燃并受伤、入水熄灭”。
尚未验证：与原版服务端逐 tick 对比生物着火时长与伤害序列未做。

### 3.22 水流推动掉落物的成本（本批，实测）

每个处于水中的掉落物每 tick 增加一次水流推动计算：`itemWaterFlowPush` 遍历
碰撞盒覆盖的方块（0.25×0.25 的盒最多 2×2 个），对每个水方块调用
`fluidFlowVector`（原版 `FlowingFluid.getFlow` 的移植，每格 4 次邻域读取）。
`BenchmarkItemWaterFlowPush`（2×2 上坡水道，含“多方块求平均”的常见情形）实测：

| 指标 | 数值 |
| --- | --- |
| 时间 | ≈0.39 µs/op（5 次采样 0.387–0.400 µs/op） |
| 内存 | 0 B/op、0 allocs/op |

只有在水中的掉落物才付出这份成本（干燥掉落物走早退分支，路径不变）：
100 个水中掉落物约 +39 µs/tick（≈0.08% 的 50 ms tick）。

正确性：`./scripts/test.sh`（含 `-race`）全绿；`fluid_flow_test.go` 校验流向
（空气/静水为零、坡面指向低处、受阻下落水流向下）以及“水道中的掉落物在
120 tick 内持续向下游漂移”。尚未验证：与原版服务端逐 tick 对比掉落物在水道
中的漂移轨迹未做。

### 3.23 实体跟踪 Tick 的成本（本批，实测）

实体跟踪（`internal/server/entity_tracking.go`）每 tick 为每名玩家做增量同步：
建立存活索引（各维度 id → 水平位置）→ 供加入判定与“已销毁”清理；随后
实体表 × 玩家表的两两距离检查（先做盒式粗筛，再做平方距离），
只对“未跟踪且进入 64 格”/“已跟踪且离开 80 格”的组合编码数据包。
数据包编码在 `entityMu` 内完成，发送在锁外（`tryWrite` 会同步写连接）。

`BenchmarkEntityTracking`（1 名玩家 + 128 个掉落物 + 16 只生物，全部在 64 格内，
稳态无新增/移除）实测：

| 指标 | 数值 |
| --- | --- |
| 时间 | ≈4.7 µs/op（3 次采样 4.704–4.749 µs/op） |
| 内存 | 0 B/op、0 allocs/op |

对既有基准的影响（同机复测，`-count=3`）：

| 基准 | 引入实体跟踪前（3.21/3.22 记录） | 现在 |
| --- | --- | --- |
| `BenchmarkServerTick`（32 生物、无玩家） | 15.8–16.4 µs/op | 16.4–17.0 µs/op（+≈0.6 µs，+4%） |
| `BenchmarkItemTick`（128 掉落物、无玩家） | 本次未单列旧值 | ≈40 µs/op（含跟踪存活索引构建） |

开销主要来自每 tick 的存活索引构建（与实体总数线性，Map 复用、零分配）与
实体 × 玩家距离检查；100 玩家 + 数百实体时约 0.1–0.5 ms/tick 量级（线性估计，
未在 100 玩家压测中单独采样）。若未来实体总量显著增大，可改为按区块分桶查询。

正确性：`./scripts/test.sh`（含 `-race`）全绿；6 个新测试覆盖
“靠近补发/离开移除/多人互见/玩家实体补发/重生补发/维度切换补发”。
尚未验证：真实正版客户端的多人可见性肉眼验证与 100 玩家 loopback 压测的
重跑（代码路径不涉及协议格式变化，协议内容与既有 Add/Remove 包一致）。

### 3.24 掉落物物理对齐与环境判定改造的成本（本批，实测）

掉落物运动改为逐轴 `ClipMove` 后直接取裁剪速度（原版 `Entity.move` 语义），
不再需要“下一 tick 是否贴地”的额外 `Collides` 探测（原实现每 tick 多一次
包围盒形状查询）；合并检查也从“固定每 4 tick”改为原版节奏（跨方块边界移动时
每 2 tick、静止时每 40 tick），静止掉落物的合并扫描次数下降到原来的 1/10。

| 基准 | 改造前（3.23 记录） | 现在 |
| --- | --- | --- |
| `BenchmarkItemTick`（128 掉落物、无玩家） | ≈40 µs/op | ≈31 µs/op（约 -22%） |
| `BenchmarkItemWaterFlowPush`（水道推动） | ≈0.39 µs/op | ≈0.41 µs/op（噪声级） |
| `BenchmarkItemMergeScan` | 未单列旧值 | ≈224 µs/op（扫描规模与场景相关） |

环境判定（火/水/岩浆）从“单列两格 `BlockAt`”改为“碰撞盒覆盖方块按列批量读取”
（玩家 1.8 高、0.6 宽：盒在位内时 1 列 2–3 层，贴在方块边缘时最多 2×2 列），
常见情形读取量与原先相当，边缘情形少量增加（每实体每 tick 至多几次列读取）。

正确性：`./scripts/test.sh`（含 `-race`）全绿；新增
`TestItemWaterMotionVanilla`（水中水平 0.99×0.98、垂直 5.0E-4×0.98、超阈值
不加浮力）、`TestItemBounceAndSettleVanilla`（落地反弹后静止于地面）、
`TestItemFrictionOnIceVanilla`（冰面明显比石面滑）、
`TestEnvironmentBoxOverlapIgnites`/`TestEnvironmentBoxDoesNotOverreach`
（盒子边缘相交点燃、不重叠不点燃）。尚未验证：与原版服务端逐 tick 对比
掉落物轨迹与合并时序未做。

### 3.25 注册表热路径、玩家状态原子化与写批处理（本批，基准 + loopback 压测）

本批以 `BenchmarkServerTick` 的 CPU profile（`environmentInBox` 占 33% cum、
其中字符串 map 查找约 28%）与端到端压测定位，做了五组改动。

**1. 方块状态类别位表（registry/states.go）**

生物环境扫描对每个方块状态最多做 3 次“水/岩浆/火”字符串 map 查找
（`IsPropertyBlockState`）。新增按状态 ID 索引的类别位表 `StateKindOf`
（约 26 KiB，包初始化时构建），`isWaterState`/`isLavaState`/
`registry.IsFireState` 与 `environmentInBox` 的逐格判定退化为一次数组访问。

**2. 玩家状态的原子快照（session/entity）**

实体 Tick 按“每个 mob × 每个玩家”读取位置/维度/可攻击状态，原实现经
`stateMu` 独占锁（100 玩家 + 32 生物时每次 tick 约 6400 次加解锁）。
`playerPose`（位置/朝向/维度）改为不可变快照 + `atomic.Pointer` 发布
（写者持 `stateMu` 读改写），`dead`/`gameMode` 原子存储，读路径无锁。
同时 `mobKinds` 由 map 改为按枚举索引的数组，`worldFor` 增加无锁读快照
（`Server.worldsCache`，创建维度时重建）。

**3. 会话写批处理与共享广播帧（session/server/entity）**

`session.writer` 包装 8 KiB `bufio` 写缓冲：`tryWrite`（广播）、
`writeFrame`（区块/注册表）与 `writePackets`（成串发送）写入缓冲，
由固定时机批量刷出——服务器 Tick 结尾（`drainPlayerWrites`，遍历在线玩家
无锁快照，只对有写入的会话取锁）、读循环每个数据包处理完后、进入世界
流程结束、`unregisterPlayer`、Keep Alive（即时）；交互响应 `writePacket`
保持即时刷出。后台 Tick 被禁用时（测试）`Serve` 启动 5 ms 刷出循环。
多目标广播（`broadcastToNearby`/`broadcastPacketExcluding`/
`writeToNearbyPlayers`）改为**压缩帧惰性构造一次、范围内玩家共享**
（与区块帧缓存同思路）；无目标时不产生压缩与分配。

**4. 区块同步加载移出世界锁（world）**

`World.Chunk` 的同步路径原先在 `w.mu` 写锁内读盘/生成地形，一次慢盘
IO 会阻塞所有实体 Tick 与世界查询；缓存命中路径原先也取写锁。现在缓存
命中只取读锁；缺失时注册请求（与 `Prefetch` 共用去重表）并在锁外完成
`LoadChunk`/`GenerateChunk`，经 `publishChunkLoad` 统一发布（与预生成
worker 共用），并发请求同一区块共享同一结果。行为保留：关闭时解决
悬挂请求并返回错误（`TestPrefetchAfterClose`）。

**5. 安全性**

- 人数上限强制执行：超过 `max_players` 的新玩家收到
  `multiplayer.disconnect.server_full`（同 UUID 重连仍允许替换加入）；
- 容器点击 / 创造模式物品操作 / Tab 补全加入令牌桶限流（与移动/聊天
  一致的策略），防止包洪泛放大（每次点击触发槽位校验与广播、每次补全
  构造命令树响应）。

**基准（同机 `-count=3` 中位数，`-benchtime=2s`，Go 1.27，i7-12700F；
对比为同一工作区 HEAD 构建）**

| Benchmark | 优化前（HEAD） | 优化后 | 变化 |
| --- | --- | --- | --- |
| `BenchmarkServerTick`（32 生物） | ≈23.7 µs/op | ≈17.2 µs/op | -27%（1 alloc/op 不变） |
| `BenchmarkServerTickManyPlayers`（32 生物 + 100 玩家，本批新增） | ≈496 µs/op；≈53 KB/op；≈826 allocs/op | ≈325 µs/op；≈3.6 KB/op；≈23 allocs/op | -34%；分配字节 -93%、次数 -97% |
| `BenchmarkItemTick`（128 掉落物） | ≈31.4 µs/op | ≈30.9 µs/op | 噪声范围 |

**单机 loopback 压测（`cmd/gmcsload` + `/proc` 计数；新旧二进制同机构建）**

| 场景 | 指标 | 旧 | 新 | 变化 |
| --- | --- | --- | --- | --- |
| A：100 玩家、视距 6、30 s 移动 | 服务器 CPU（utime+stime） | 4.33 s | 2.08 s | **-52%** |
| A：同场景（反向顺序复测） | 服务器 CPU | 4.34 s | 2.07 s | -52%（复现） |
| B：50 玩家、视距 10、30 s 移动 | 服务器 CPU | 5.80 s | 5.30 s | -8.6% |
| A：进入世界耗时 / 流量 | gmcsload 统计 | 平均 1 ms / 1289.4 MiB | 平均 1 ms / 1289.3 MiB | 一致 |
| A：内存（100 玩家 10 s 后静态 70 s 观测） | 稳态 RSS | 48.2 MB | 48.2 MB | 持平 |
| A：同场景 | VmHWM | 65.9 MB | 63.9 MB | -3% |

说明：场景 A 的收益主要来自写批处理与共享广播帧（实体同步包占收包数
95%、每客户端约 439 KiB/s）；场景 B 中区块字节占 99.4%，区块编码/压缩
成本不变，故收益较小。流量与进入世界耗时新旧一致，协议行为无变化。
（压测结束后瞬间的 RSS 受 GC 时序影响会有约 ±10 MB 波动，稳态驻留与
峰值以上表为准。）

正确性验证：`go test ./...`、`go test -race ./...` 全绿；新增
`TestChunkConcurrentSingleLoad`（32 goroutine 并发请求同一区块只生成
一次、同一实例）、`TestChunkMixedPrefetchRace`（Prefetch 与同步请求
并发去重）；本批后重新生成 `cmd/gmcs/default.pgo` 并复测基准。
尚未验证：真实网络与真实客户端的多玩家负载（loopback 不覆盖网络栈
差异）；`writeDrainLoop` 仅在测试模式（后台 Tick 禁用）运行，生产
路径由 Tick 结尾刷出覆盖。

### 3.26 内存装载/释放、预加载与并发编码合并（本批，压测实测）

**背景与缺口**：内存释放此前只有“几何阈值”（区块距所有玩家超过
视距+4 才卸载、每 5 秒扫描一次），存在两个缺口：

1. 玩家在卸载边界来回移动时，区块会“卸载 → 重新加载（读盘/生成）→
   再次卸载”，脏区块还会反复写盘；
2. 玩家切维度后，旧维度（已打开、无玩家）的区块永不释放，直到服务器
   彻底空闲（`unloadAllChunks` 只在完全无玩家时触发）。

另外进入风暴下同一区块会被多个会话**并发重复编码+压缩**（各自缓存
未命中），且帧缓存上限（2048）在视距 10 时远不足以覆盖并发玩家的
活跃区块集合（50 玩家 × 441 区块 ≈ 2.2 万）。

**改动**：

1. **卸载宽限（防抖）**：`World.UnloadFar` 新增 grace 参数——区块首次
   判定“远离所有玩家”后只记录计时起点并保留，持续超出宽限（服务器
   默认 15 秒，约 3 个扫描周期）才真正卸载；期间重新变近会清零计时，
   距今超过宽限的旧计时不会沿用。玩家在边界来回移动不再触发
   “卸载 → 重加载 → 写盘”的循环。
2. **无玩家维度的延迟释放**：已打开但当前没有任何已加入玩家的维度，
   空闲超过 `dimensionIdleGrace`（默认 30 秒）后释放全部区块（脏区块
   先写盘，帧缓存与漏斗注册同步清理），快速切回维度无需重建。
3. **预加载多一圈**：`syncChunks` 的异步预取范围从“视距内”扩大为
   “视距+1”：玩家跨区块时，下一批要发送的区块已在后台加载/生成完毕，
   发送路径无需等待磁盘与生成器。预取范围仍在卸载半径（视距+4）内，
   不会被反复卸载/加载。
4. **并发编码合并（singleflight）**：`chunkPacket` 对同一区块的并发
   缓存未命中只编码一次，其余调用者等待并领取同一份共享帧——同时
   进入世界的多名玩家不再对同一区块重复编码+压缩；帧缓存上限
   2048 → 4096（压缩帧约 0.5–2 KiB/项，上限约 4–8 MiB）。
5. **卸载编码移出世界锁**：`unloadChunks` 的 zlib 编码（单区块数百
   微秒）从世界锁内移到锁外（与 `Flush` 相同的快照模式），批次卸载
   不再长时间阻塞实体 Tick 与方块查询。

**压测（gmcsload 单机 loopback，新旧二进制同机构建，30 秒移动；
两组为交替复测）**

| 场景 | 指标 | 旧 | 新 | 变化 |
| --- | --- | --- | --- | --- |
| B：50 玩家、视距 10 | 服务器 CPU | 5.24 s / 5.04 s | 1.07 s / 1.05 s | **-80%** |
| B：同场景 | VmHWM | 108.8 / 108.5 MB | 72.9 / 84.6 MB | 约 -30% |
| B：同场景 | 进入世界耗时（平均 / p95） | 4 ms / 10 ms | 3 ms / 6 ms | 更快 |
| A：100 玩家、视距 6 | 服务器 CPU | 2.16 s / 2.16 s | 2.13 s / 2.14 s | 持平 |
| A：同场景 | VmHWM | 82.6 / 78.5 MB | 72.2 / 74.2 MB | -5%~-12% |

说明：视距 10 时 50 名玩家的活跃区块集合远超原帧缓存上限，进入风暴
与移动中同一区块被多个会话重复编码/压缩（单区块约 170 µs）是 CPU 的
主要来源；singleflight 与更大的缓存消除该重复后 CPU 下降约 80%（复测
复现），并发编码的临时缓冲（每区块约 100–300 KB）峰值也随之消失，
峰值内存下降约 30%。视距 6 时活跃区块少、缓存命中率高，故 A 场景
CPU 持平、内存小幅下降。两场景的收发包总量与流量基本一致（±0.2%），
协议行为无变化。

正确性验证：`go test ./...`、`go test -race ./...` 全绿；新增
`TestUnloadFarGraceKeepsRecentlyFarChunks`/`TestUnloadFarGraceResetsWhenNear`
（宽限保留与计时清零）、`TestChunkUnloadGraceWindow`、
`TestIdleDimensionReleasedAfterGrace`（空闲维度释放与按需重载）、
`TestChunkPrefetchExtraRing`（视距+1 环带被预取）、
`TestChunkPacketSingleflight`（并发未命中只编码一次且领取共享帧）；
本批后重新生成 `cmd/gmcs/default.pgo`。尚未验证：宽限 15 秒 / 维度空闲
30 秒的实际手感（真实客户端长时间游玩）与真实网络负载。

复现方式：`git worktree add /tmp/gmcs-base <旧提交>` 后各自
`go build -o gmcs-<tag> ./cmd/gmcs`，用同一 `cmd/gmcsload` 依次运行
（本批场景：`-players 50 -duration 30s -ramp 10ms`，视距 6/10 两套
配置；每轮先预热 50 玩家 10 秒生成沿途地形再正式测量），服务器 CPU 取
运行前后 `/proc/<pid>/stat` 的 utime+stime 差值、内存取
`/proc/<pid>/status` 的 VmHWM/VmRSS。

### 3.27 护甲/附魔数值效果与掉落表（本批，实测）

本批新增的护甲减伤与附魔数值计算不在 Tick 热路径（只在受伤/挖掘/破坏方块时
发生），但仍补充了基准以防后续把它们移入热路径或退化为重量级实现。
环境：Linux amd64，Go 1.26，`-count=3` 取中位数。

| 基准 | 结果 | 说明 |
| --- | --- | --- |
| `BenchmarkPlayerDamageMitigation` | 336 ns/op，0 B/op，0 allocs/op | 4 件附魔钻石护甲 + 保护 IV 的护甲/EPF 计算（含 4 次物品栏读取） |
| `BenchmarkBlockDropsFortune` | 60.5 ns/op，24 B/op，1 allocs/op | 时运 III 煤矿石掉落（返回单元素切片） |
| `BenchmarkServerTick` | 17.3–17.5 µs/op，1 allocs/op | 与 3.25 的 17.2 µs 持平 |
| `BenchmarkServerTickManyPlayers` | 327 µs/op，32 allocs/op | 与 3.25 的 325 µs 持平（差值在噪声范围；分配数受生物行为路径影响） |

实现要点与避免回退的措施：

- 附魔 ID 通过 `sync.OnceValue` 解析一次，运行时只做堆栈内附魔切片的线性查找
  （通常 0–5 项）；
- 伤害类型/实体类型标签在首次使用时构建 `map[int32]struct{}` 索引，
  受伤路径只做 O(1) 查询；
- 呼吸附魔的氧气上限缓存在会话的 `atomic.Int32`（读循环在装备快照刷新时更新），
  避免 Tick 环境结算每玩家每帧读取物品栏（本项目此前在 `BenchmarkServerTickManyPlayers`
  上测到过每玩家每帧加锁的可见成本）；
- 精准采集/时运的掉落返回小切片；普通单物品掉落仍可走原有路径。

验证：`go test ./...`、`go test -race ./internal/server ./internal/world ./internal/protocol`
全绿；新增测试覆盖护甲公式、EPF、护甲耐久、不灭概率、效率、近战附魔、
经验修补、绑定/消失诅咒、精准采集/时运/剪刀掉落。尚未验证：与原版服务端的
逐伤害数值对照（公式来自官方数据与字节码，但未做端到端对拍）。

### 3.28 玩家弓与盾牌（本批，实测）

远程武器/盾牌逻辑只在 Use Item、释放使用键与受伤时运行，不在实体 Tick 热路径；
`tickArrows` 新增生物命中扫描，但仅在存在飞行箭矢时执行，现有基准场景不含箭矢。
复测（`-count=3` 中位数）：`BenchmarkServerTick` 16.8 µs/1 alloc、
`BenchmarkServerTickManyPlayers` 321 µs/32 allocs、`BenchmarkEntityTracking`
5.1 µs/0 alloc，与 3.27 及此前记录持平（差值在噪声范围）。

正确性验证：`go test ./...` 全绿；新增 `TestParseUseItem`、`TestBowFiresArrow`、
`TestBowRequiresArrow`、`TestBowInfinityKeepsArrow`、`TestShieldBlocksFrontDamage`、
`TestShieldDoesNotBlockBypassingDamage`；`go test -race` 覆盖弓/盾牌/进食/骷髅箭矢路径。
尚未验证：真实客户端拉弓动画/格挡动画与逐 tick 伤害数值对拍；弩/三叉戟未实现。

### 3.29 耐久原子化、快捷栏槽位持久化与箭矢扫掠基准（本批，实测）

改动内容与成本：

- 物品栏耐久/经验修补从 `Get` → 修改 → `Set` 改为 `Inventory.Damage` /
  `Inventory.Repair`（内部互斥锁内一次完成）。只影响受伤、挖掘完成与经验拾取
  路径，不在每 tick 的实体循环里；没有新增分配（两个方法均不逃逸）。
  回归测试 `TestInventoryDamageConcurrentAccumulates` 用 8 goroutine × 25 次
  并发扣耐久，验证总量不丢失（旧实现会丢更新）。
- 选中快捷栏槽位从 `int` 改为 `atomic.Int32`。读循环之外的读取者是每 30 秒的
  周期自动保存（后台 goroutine）与 `players.json` 快照；改前这些路径不读该字段，
  改后必须原子化才能避免数据竞争。原子 load/store 的成本在
  `BenchmarkServerTick` 噪声内（见下）。
- 新增 `BenchmarkArrowSweepAgainstMobs`：每次循环对 32 个扩张包围盒执行
  “线段 vs AABB”（`tickArrows` 的核心判定，线段长 1.2 格）：

```text
BenchmarkArrowSweepAgainstMobs-20   121.8 ns/op   0 B/op   0 allocs/op
BenchmarkArrowSweepAgainstMobs-20   123.4 ns/op   0 B/op   0 allocs/op
BenchmarkArrowSweepAgainstMobs-20   121.4 ns/op   0 B/op   0 allocs/op
```

折算约 3.8 ns/目标；箭矢数量较少时该扫描不构成 tick 热点（只有存在飞行箭矢时
才执行）。注意这是判定函数本身的微基准，不包含伤害结算、广播与掉落。

复测（`-benchtime=0.5s -count=3`）：

```text
BenchmarkServerTick-20       15892 / 16058 / 15381 ns/op   1 alloc/op
BenchmarkEntityTracking-20    4895 /  4691 /  4713 ns/op   0 allocs/op
```

与 3.27（17.3–17.5 µs）、3.28（16.8 µs）及上一批（15.8–16.3 µs）相比处于噪声
范围，未见回退。

正确性验证：`go test ./...` 全绿；新增 `TestInventoryDamageBreaksItem`、
`TestInventoryDamageRejectsNonDamageable`、`TestInventoryRepair`、
`TestInventoryDamageConcurrentAccumulates`、`TestEncodeSetHeldSlot`、
`TestSelectedHotbarSlotPersists`；`go test -race ./...` 覆盖周期保存读取
选中槽位的路径。尚未验证：真实客户端重启后快捷栏选中的视觉效果
（包 ID 与 VarInt 字段已对照官方 `packets.json` 与 1.21.11 服务端字节码）。

### 3.30 环境状态原子化与持久化（本批，实测）

`fireTicks`/`air` 从 Tick goroutine 独占的 `int` 改为 `atomic.Int32`，使每 30 秒的
后台自动保存可以读取它们而无需加锁；`exhaustion`/`foodTimer` 原本就受
`stateMu` 保护，保存时与生命/饥饿值在同一临界区内读取。环境 Tick 每玩家每帧新增
约 6 次原子 load/store（x86-64 上是普通内存操作，无锁前缀），无新增分配。

复测（`-benchtime=0.5s -count=3`）：

```text
BenchmarkServerTick-20             15547 / 15492 / 15853 ns/op   1 alloc/op
BenchmarkServerTickManyPlayers-20 311138 / 308270 / 311579 ns/op
                                   46–47 allocs/op
BenchmarkEntityTracking-20          4823 /  4740 /  4788 ns/op   0 allocs/op
```

`BenchmarkServerTickManyPlayers`（32 生物 + 100 玩家，包含每玩家环境 Tick）为
308–312 µs，与上一批记录（321–327 µs）相比在噪声范围内甚至略低；单生物
Tick 与实体跟踪同样持平。

正确性验证：`go test ./...` 全绿；新增 `TestEnvironmentStatePersists`
（保存 `air=120`/`fire_ticks=45`/`exhaustion=2.5`/`food_timer=33` 后重连恢复）；
既有环境测试（岩浆点燃、入水熄灭、每秒火焰伤害、溺水、判定盒范围）全部改为
原子访问后继续通过。尚未验证：与原版 playerdata 的 Air/Fire 数值逐项对拍
（字段语义来自原版 `Player` 存档字段，未做端到端存档对照）。

### 3.31 作物掉落表（本批，实测）

作物掉落只在方块被破坏时计算一次，不在 Tick 热路径。实现上先在方块名上做
switch（只有 7 个作物会进入后续的 `BlockStateAge` 区间二分），因此非作物方块
只多一次字符串比较。

复测 `BenchmarkBlockDropsFortune`（煤矿 + 时运 3，`-count=3`）：

```text
BenchmarkBlockDropsFortune-20   67.35 / 67.46 / 66.31 ns/op   24 B/op   1 alloc/op
```

与 3.27 记录的 60.5 ns/op 相差约 6 ns（多出的分支与测量波动）；相对一次方块
破坏的毫秒级耗时（含世界锁、广播与掉落物生成）可以忽略，按 AGENTS §4.2 不做
进一步微优化。行为正确性由 `TestBlockDropsCrops`（成熟度/时运/精准采集）与
`TestBlockStateAge`（区间推导边界）覆盖。

### 3.32 剩余植物掉落与双格植物判定（本批）

新增可可豆/海泡菜/瓜类茎/瓶子草掉落，并把双格植物上半格判定从
`StateProps`（未收录这些属性状态）改为状态区间推导；破坏双格植物任一半时
额外做一次 ±1 格查询与移除。每次方块破坏只会多一次二分查找与取模；没有新增
分配，也没有进入 Tick 路径，因此未新增基准。`BenchmarkBlockDropsFortune`
实测 71 ns/op、24 B/op、1 alloc（与 3.31 的 66–67 ns/op 同量级）；
`BenchmarkServerTick` 16.2 µs/op、61 B/op、1 alloc，
`BenchmarkServerTickManyPlayers` 303 µs/op、4904 B/op、31 alloc，均与上一批一致。

正确性验证：`TestBlockStateAge`、`TestBlockStateHalfUpper`、
`TestBlockStateCropUpperHalf`、`TestBlockStatePickleCount`、
`TestBlockDropsCrops`、`TestRemoveDoublePlantPair` 均通过；区间顺序
（可可豆 age×facing、海泡菜 pickles×waterlogged、瓶子草 age×half、
双格植物 upper→lower）逐一对照官方 `reports/blocks.json` 的状态列表核对；
双格植物的掉落语义对照 1.21.11 服务端 `DoublePlantBlock.playerWillDestroy`
（非创造模式按被破坏状态结算战利品表，创造模式调用
`preventDropFromBottomPart` 移除另一半）与官方方块战利品表得出。
尚未验证：未在原版服务器上逐方块实测掉落数量分布。

### 3.33 农业随机刻（本批，实测）

作物与耕地采用“逐登记位置掷骰”的随机刻：每个位置每 tick 以 3/4096 的概率
被选中（与原版“每 section 3 个随机位置”的期望一致），命中后才读取方块、
按 `CropBlock.getGrowthSpeed` 决定是否生长。因此绝大多数 tick 只消耗一次
`nextRandom()` 与一次 `ChunkLoaded` 查询。

基准（`-count=3`，日志不参与计时）：

```text
BenchmarkCropTick-20       143–148 ns/op    0 B/op   0 allocs/op   （1 作物 + 1 耕地）
BenchmarkCropTick256-20    26.5 µs/op       5 B/op   0 allocs/op   （256 作物 + 256 耕地）
BenchmarkServerTick-20     15.9 µs/op      51 B/op   1 alloc/op    （无作物，与上批一致）
```

即约 52 ns / 登记位置 / tick（线性），16×16 农田每 tick 约 26.5 µs（占 50 ms
tick 预算的 0.05%），64×64 农田按线性外推约 0.42 ms（0.8%）。没有新增分配，
`BenchmarkServerTick` 与上一批（15.4–16.1 µs）一致。

设计取舍：原版用“每 section 随机位置 → 查方块”的方式，成本与作物数量无关；
gmcs 逐个已登记位置掷骰，实现简单且不持有逐 section 的方块索引，但成本随农田
规模线性增长。在当前实测规模下开销可忽略，按 AGENTS §4.2 不做进一步的
per-section 索引（需要额外的位置→作物索引结构与其失效维护）。

正确性验证：`TestHoeTillsSoil`、`TestHoeTillingRequiresAirAbove`、
`TestHoeTillsCoarseAndRootedDirt`、`TestPlantCropOnFarmland`、
`TestPlantCropRequiresFarmland`、`TestPlantCropRequiresSkyLight`、
`TestBoneMealGrowsCrop`、`TestNetherWartPlantAndGrowth`、
`TestCropRandomTickGrowth`、`TestCropGrowthSpeed`、
`TestFarmlandMoistureNearWater`、`TestFarmlandWithCropStaysFarmland`、
`TestTrampleFarmland`，以及 registry 的 `TestFarmlandMoistureStates`、
`TestBlockStateAgeRoundTrip`。语义逐条对照 1.21.11 字节码
（`HoeItem`/`CropBlock`/`NetherWartBlock`/`FarmBlock`/`BoneMealItem`）实现；
尚未验证：未在真实客户端中观察生长动画与骨粉粒子，也未与原版服务器逐 tick
对拍生长分布。

### 3.34 放置形态/农业/可开合方块批次后的复测

本批（农业随机刻、方块放置形态、门/栅栏门与红石联动）只在交互路径与随机刻
路径增加工作；Tick 主循环新增的只有 `tickCrops()`（见 3.33）。复测关键基准
（`-count=3`，取三次范围）：

```text
BenchmarkServerTick-20            16032–16149 ns/op    50–56 B/op    1 alloc/op
BenchmarkServerTickManyPlayers-20   311.1–311.6 µs/op  ~4.6–5.0 KB   30–32 allocs/op
BenchmarkEntityTracking-20        4.79–4.87 µs/op      0 B/op        0 allocs/op
BenchmarkCropTick-20              143.1–145.7 ns/op    0 B/op        0 allocs/op
BenchmarkCropTick256-20           26.2–26.4 µs/op      5 B/op        0 allocs/op
```

`ServerTick`（16.1 µs）与 3.32 的 16.2 µs、`ManyPlayers`（311 µs）与 3.32 的
303–312 µs 同量级，未观察到本批引入的 Tick 回归。

放置形态与可开合方块的实现没有进入 Tick 热路径：红石开合只在
`refreshRedstoneConsumers` 命中门/活板门/栅栏门时执行（需要一次
`bestNeighborSignal` 与状态查表，均为低频事件）；生成表只保存放置用到的
组合（约 2.8 千个状态 ID ≈ 6 KiB 数据 + map 开销，源码约 45 KiB），
没有把台阶/半砖的全部 5–6 千个属性状态加入 `BlockPropertyStates`，
避免启动内存膨胀。

正确性验证：本批新增测试见 docs/TODO.md（放置形态、门/栅栏门、红石开合、
农业随机刻）；`go test ./...` 与 `go test -race ./internal/server` 均通过。
尚未验证：未在真实客户端观察放置/开合/生长表现，也未与原版服务器逐项对拍。

### 3.35 天气系统（本批，实测）

天气进入 Tick 主循环（`tickWeather`）并新增两条**逐实体**判定：
`Entity.baseTick` 语义的雨中灭火会为每个玩家与每只生物各查询一次
`isRainingAt`。为避免在非雨天付出锁开销，`isRaining()` 改为读取
`atomic.Bool` 快照（`rainingFlag`），`isRainingAt` 在未下雨时提前返回。

专用基准（`-count=3`，取范围）：

```text
BenchmarkWeatherTick-20             13.7 ns/op     0 B/op     0 allocs/op   （稳态：计时递减 + 强度推进 + 一次锁）
BenchmarkWeatherTickTransition-20   69.6 ns/op    32 B/op     4 allocs/op   （状态翻转：编码 3 个 Game Event 包）
```

主循环复测：本批提交（当前 `HEAD`）与父提交在不同时刻测量会有 5–10% 的
整机漂移，因此用 `git worktree add /tmp/gmcs-base <父提交>` 在同一时段做 A/B
（两组都 `-count=5`）：

```text
                                 父提交（64cf49e）          本批（天气）
BenchmarkServerTick-20           16.92–17.36 µs/op        17.09–17.89 µs/op
BenchmarkServerTickManyPlayers   331–336 µs/op            325–399 µs/op（中位 ~327，两次数值为系统噪声）
BenchmarkEntityTracking-20       5.11–5.22 µs/op          5.08–5.21 µs/op
BenchmarkCropTick256-20          28.2–29.4 µs/op          28.5–30.0 µs/op
```

结论：`ServerTick` 增加约 0.2–0.5 µs（32 只生物 + 1 名玩家各一次
`isRainingAt` 的无锁快速返回，约 10–20 ns/实体），在 50 ms 的 tick 预算中是
0.001% 量级；`EntityTracking`/`CropTick` 与天气无关，差异属测量噪声。
天气状态机本身每 tick 固定 13.7 ns、零分配；只有下雨状态翻转的那一 tick
才编码 3 个包（69.6 ns），强度平滑期间每 tick 最多 2 个包。

正确性验证：`weather_test.go` 10 个测试覆盖周期推进、强制放晴、广播顺序、
加入同步、耕地滋润、火焰与实体浇灭、`/weather` 参数、天空暴露判定与关闭
天气；`go test ./...`（`internal/server` 19.0 s）与
`go test -race ./internal/server ./internal/world ./internal/registry` 通过。
尚未验证：未与原版服务器逐 tick 对拍天气序列，也未在真实客户端观察降雨
渲染；闪电已在后续批次实现（见 3.37），炼药锅/积雪仍未实现（见 docs/TODO.md
天气限制）。

### 3.36 地形观感批次（本批，实测）

本批修改地形生成（洞穴阈值/隧道规模、河流带宽与河岸曲线、第二层丘陵噪声、
跨区块树冠）与 Tick 路径（仙人掌状态位表、日光燃烧判定、海洋生物）。
基准在同一台机器上用 `git worktree add /tmp/gmcs-base HEAD` 做 A/B
（`-count=3`，`-benchtime=2s`）：

```text
                              父提交（059e4b6）           本批
BenchmarkGenerateChunk        527–600 µs/op               443–450 µs/op
                              24,651–24,665 B/op          25,574–25,916 B/op
                              51 allocs/op                52–53 allocs/op
BenchmarkServerTick           18.51–18.89 µs/op           18.52–19.15 µs/op
BenchmarkServerTickManyPlayers 332.0–334.3 µs/op          331.0–333.7 µs/op
```

- `GenerateChunk` 反而快了约 15%：洞穴噪声阈值提高后粗网格“整格跳过”的比例
  大幅上升、蠕虫隧道减半，抵消了新增的丘陵噪声与区块外扩 3 格的树冠扫描；
  分配字节 +1.2 KB（树冠扫描的临时状态，未新增堆对象之外的规模变化）。
- `ServerTick` / `ManyPlayers` 在相同地形与相同基准（蜘蛛、夜晚）下无回归；
  日光燃烧只在僵尸/骷髅且**白天**时查询一次天空暴露（`ColumnAt` + 列读取），
  夜晚/其他生物直接跳过。

**基准口径变化**：`BenchmarkServerTick`/`ManyPlayers` 改用**蜘蛛**并把世界时间
固定为夜晚——僵尸/骷髅在白天阳光下会持续着火并死亡（原版行为），长时间基准
的剩余生物数会漂移，导致新旧数值不可比（同一提交在不同 benchtime 下可相差 2 倍以上）。

正确性验证：`go test ./...`（server 19.6 s、world 1.2 s）与
`go test -race ./internal/server ./internal/world`（202 s / 8 s）通过。
未验证：未与真实客户端观察新地形观感（洞穴密度/河岸/树冠），也未与原版
逐区块对拍地形。

### 3.37 死亡界面与雷暴闪电（本批，实测）

本批新增两条服务端路径：死亡时下发 `Player Combat Kill`（0x42，常量编码），
以及每 tick 的闪电推进（`tickLightning`，仅在 `thundering` 时掷骰）。
闪电的判定放在 `tickWeather` 之后、玩家写缓冲刷出之前，沿用同一帧的
广播聚合。

专用基准（`-count=3`，`-benchtime=1s`，`internal/server`）：

```text
BenchmarkLightningTickClear-20        约 20.7 ns/op     0 B/op   0 allocs/op
   （非雷暴：一次 atomic 读取 + isThundering 的锁 + 空 map 检查）
BenchmarkLightningTickThunder-20      约 74.7 ns/op     0 B/op   0 allocs/op
   （雷暴：4 名玩家各掷一次骰子，无落雷时的常态开销）
```

按 50 ms tick 预算计算，4 名玩家 / 100 名玩家的雷暴常态开销分别约为
0.00015% / 0.0037%；真正的落雷（平均每 200–500 tick 一次）才会发生
实体广播、AABB 扫描与伤害结算，属于稀有事件。
`Player Combat Kill` 只在玩家死亡时编码一次（≈ msg 长度 + 少量字节），
不在热路径上。

正确性验证：`internal/protocol/combat_test.go` 逐字段校验包布局；
`internal/server/death_screen_test.go` 与 `internal/server/lightning_test.go`
覆盖死亡界面下发（含自定义死亡模板）、落雷的完整效果（实体/点火/生物
伤害与点燃/玩家伤害与点燃/2 tick 后移除）、非雷暴时掷骰频率量级。
未验证：未在真实客户端观察死亡界面与闪电渲染，也未与原版逐 tick 对拍
落雷序列（概率模型为等效近似，见 docs/TODO.md 天气限制）。

### 3.38 原版噪声（Perlin/NormalNoise）采样开销基线（本批，实测）

这是原版区块生成器的地基：`final_density` 及其上游全部密度函数最终都由
`NormalNoise` 取值驱动，因此先把单次采样的成本量出来，作为后续地形采样的
优化基线。

```text
BenchmarkImprovedNoise-20            约 32.8 ns/op     0 B/op   0 allocs/op
   （单个八度：8 角梯度点积 + 三轴五次插值）
BenchmarkNormalNoiseValue-20         约 948 ns/op      0 B/op   0 allocs/op
   （continentalness：2 份 Perlin × 9 个八度 = 18 次 ImprovedNoise）
BenchmarkNoiseRegistryLookup-20      约 10.5 ns/op     0 B/op   0 allocs/op
   （噪声实例按资源名缓存，命中路径只是一次 map 读）
```

18 次 `ImprovedNoise` 的理论下限约 590 ns，实测 948 ns，差额来自
`PerlinNoise.Value` 的八度循环与 `wrap` 调用。后续做区块级别的批量采样时
（同一八度连着算很多点）可以在外层缓存八度偏移与因子，进一步压到接近下限；
本批不提前优化，先保证与原版逐值一致。

正确性验证：`TestNormalNoiseMatchesVanilla`、`TestNormalNoiseLegacyMatchesVanilla`、
`TestNormalNoiseDetailMatchesVanilla`、`TestNormalNoiseLegacyStateMatchesVanilla`
（2 种噪声 × 4 种子 × 5 点，误差 < 1e-12；并逐八度校验 xo/yo/zo 的派生链）。

### 3.39 旧版混合噪声（old_blended_noise）采样开销（本批，实测）

`minecraft:overworld/base_3d_noise` 用的就是 `BlendedNoise`：3 个 legacy
`PerlinNoise`（2×16 个八度 + 8 个八度），单点采样最多做 8+16+16 = 40 次
`ImprovedNoise`。这是目前单次调用最贵的密度函数。

```text
BenchmarkBlendedNoiseValue-20        约 1.15 µs/op     0 B/op   0 allocs/op
```

它位于 `final_density` 的上游，每个区块只在一个 4×8×4 的粗网格上采样一次
（见 `interpolated`），因此单区块开销量级在百微秒以内；真正需要优化时，
优先看 `interpolated` 的网格复用而不是单次采样。

正确性验证：`TestOldBlendedNoiseParameters`（5 个参数落到正确字段）、
`TestNoiseSmearedWithZeroScale`（yScale=0 退化为普通采样）、
`TestBlendedNoiseDeterministic`（同种子逐点一致、不同种子不同、不超过上界）。
未验证：尚未与原版逐值对拍（缺少该节点的官方真值），将在阶段 5 用官方
服务端同种子区块对比覆盖。

### 3.40 区块级密度填充（本批，实测 + profile）

新增 `ChunkSampler`：每个 `interpolated` 节点一次算出整块 lattice，逐方块只做
索引取值 + 三线性插值（与逐点求值逐位相同，有测试保证）。

```text
BenchmarkChunkSamplerFillChunk-20     约 20.3 ms/区块   788 KB   107 allocs
   （16×384×16 = 98304 格，含 lattice 预计算）
```

过程记录：第一版四种 cache 节点全是直通，实测 67 ms/区块。`-cpuprofile` 显示
`PerlinNoise.Value` 累计占 35%、`evaluateSpline` 占 37%，原因是 `flat_cache`
包着的 `shift_a`/`shift_b`/`offset` 被**逐方块**重算。按 `euu$g`/`euu$d` 的字节码
把它们改成按 (x, z) 缓存后降到 20.3 ms（约 3.3×）。

剩余开销主要是每区块的 lattice 计算（5 个 `interpolated` 节点 × 1225 点），
以及 `cache_once`/`cache_all_in_cell` 仍是直通（语义待确认）。

### 3.41 含水层逐方块判定（本批，实测）

`Aquifer.ComputeSubstance` 在密度采样之上补齐原版 `NoiseBasedAquifer` 的
流体格判定（315 个格、最近 4 格压力插值、barrier 噪声），`ChunkSampler.Substance`
把两者串起来。基准同机同种子（`-count=1`，16×384×16 = 98304 格）：

```text
BenchmarkChunkSamplerFillChunk-20    20.2 ms/区块    788 KB    109 allocs
BenchmarkAquiferFillChunk-20         22.7 ms/区块   1295 KB    154 allocs
```

即含水层额外开销约 2.5 ms/区块（+12%），分配增加约 500 KB（未逐项归因，
已知其中约 5 KB 是 315 项 `FluidStatus`/`bool`/`int64` 格缓存，
其余主要落在求值器按列缓存随 `preliminary_surface_level` 扫描产生的条目上）。

`-cpuprofile`（120 次迭代，总样本 3.09 s）里含水层自身的累计占比：
`preliminarySurfaceLevel` + `preliminarySurfaceLevelRange` ≈ 6.8%
（其中 `evaluateFindTopSurface` 6.8%，`newAquifer` 4.85%），
`ComputeSubstance` ≈ 2.6%、`computeFluid` ≈ 2.3%。也就是说额外开销主要来自
**每区块 121 列的 `find_top_surface` 扫描**：每列要用步长 8 从粗上界向下扫到
首个密度 > 0 的高度。这与原版 `NoiseChunk.preliminarySurfaceLevel` 的按列缓存
行为一致，目前没有 profile 证据支持引入额外近似（AGENTS §4.1）。

### 3.42 完整区块生成（密度 + 含水层 + 表面规则）

`Router.GenerateChunk` 把三个阶段串起来。同机同种子（区块 62,62）：

```text
BenchmarkGenerateChunk-20    38.3 ms/区块    7.99 MB/op    10517 allocs/op
```

分解（前两节实测）：密度 lattice ≈ 20.2 ms、含水层 ≈ +2.5 ms，
剩余 ≈ 15 ms 是表面规则（逐方块条件求值 + 群系查询 + 恶地/冰山扩展）。

内存主要来自 `GeneratedChunk` 的方块数组：98304 个 `SurfaceBlock`
（注册名 string + 属性 map，24 B/个 ≈ 2.4 MB），其余是求值器的按列/按格缓存
（区块生命周期内复用）。

一次优化已做：`BiomeSource` 原本每次生成区块都重新解析 7593 点的参数表
（+8 MB / +15 ms），改为缓存在 `Router` 上后降到 38 ms / 8 MB。

未做（暂无证据支持）：表面规则的条件求值没有像原版那样按"上下文更新计数"
缓存 `temperature`/`steep`/`hole`；这些条件是纯函数，缓存只影响性能不影响结果。

## 4. 尚未覆盖- 真实多玩家并发负载（登录风暴、区块流式加载压测、实体密度压力）
  ——已用 `cmd/gmcsload` 做单机 50 玩家、100 玩家 × 5 分钟与 100 玩家 × 3 分钟
  loopback 压测（见 3.7、3.13、3.18），真实网络 / 真实客户端渲染仍待验证
- `go tool trace` 调度与阻塞分析
- 区块保存/加载（区域文件读写）路径的 benchmark
- 端到端连接压测（真实 socket + 加密 + 多包交织；当前仅覆盖编解码与压缩）
- 内存随在线时长/跑图的持续观测（已有：区块卸载演示与快照测量、3.26 的
  80 秒稳态 RSS/峰值观测、多玩家 loopback 压测的内存对比（3.7/3.13/3.18）；
  数小时级长时间运行与真实网络下的内存行为仍待观测）
- 掉落物高密度场景的物理与广播（合并扫描已有 `BenchmarkItemMergeScan` 覆盖，
  见 3.17；广播路径与长时间运行的内存增长仍待观测）
