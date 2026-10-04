# AGENTS.md

## 1. 项目概述

**项目名称：gmcs**

`gmcs` 是一个使用 **Go** 编写的高性能 Minecraft Java Edition 服务器实现。

项目的核心目标：

1. **高性能**
2. **低内存占用**
3. **高并发能力**
4. **保持与原版 Minecraft Java Edition 的高度兼容**
5. **在不破坏协议、游戏逻辑和客户端兼容性的前提下，解决 Java 原版服务器的性能瓶颈**
6. **保持代码可维护、可测试、可验证**

gmcs 不是简单的 Minecraft 协议代理或服务器包装器，而是一个独立的服务器实现。

---

# 2. 核心原则

所有 Agent 在修改项目时，都必须遵守以下原则。

## 2.1 正确性优先

性能优化不能以破坏 Minecraft 行为兼容性为代价。

优先级：

```text
正确性
  ↓
原版兼容性
  ↓
稳定性
  ↓
性能
  ↓
内存占用
  ↓
代码简洁性
```

除非任务明确要求，否则不能为了性能而改变 Minecraft 的可观察行为。

---

## 2.2 原版兼容优先

gmcs 的目标是：

> 对 Minecraft 客户端、插件/服务端生态以及协议行为尽可能表现得像原版 Minecraft Server。

任何可能影响兼容性的修改都必须明确分析。

特别注意：

* Minecraft 网络协议
* 数据包顺序
* 数据包内容
* Tick 行为
* 世界生成
* 方块更新
* 实体行为
* Redstone
* 方块实体
* 物品行为
* 战斗
* 碰撞
* AI
* 随机刻
* 区块加载与卸载
* 区块保存
* 玩家状态
* 玩家权限
* 命令
* NBT
* 资源包相关行为
* 加密与认证
* 压缩
* Server List Ping
* Login / Configuration / Play 生命周期
* Java Edition 不同版本之间的协议差异

如果无法确认某个行为是否符合原版：

**不要假装确定。**

应该：

1. 明确指出不确定性。
2. 查阅官方资料、协议文档、已有测试或实际行为。
3. 如果仍无法确认，则增加测试或记录 TODO。
4. 不要凭猜测声称“与原版完全一致”。

---

# 3. 性能目标

性能是 gmcs 的核心设计目标之一。

## 3.1 CPU

重点解决传统 Java Minecraft Server 的：

* 单线程瓶颈
* Tick 阻塞
* 区块加载阻塞
* 世界保存阻塞
* 网络处理阻塞
* 大量实体更新造成的 CPU 峰值
* GC 导致的停顿
* 锁竞争

Go 的 goroutine 和多核能力应被合理利用。

但是：

> **不能为了“多线程”而盲目并行。**

Minecraft 世界逻辑存在大量状态依赖。

应该优先考虑：

* 分区（partition）
* 工作队列
* Actor-like 模型
* region/chunk 级并行
* 无锁/低锁数据结构
* 批处理
* cache locality
* 异步 IO
* CPU 密集任务并行化

而不是简单地把所有逻辑放进 goroutine。

---

## 3.2 Tick

Tick 系统必须尽量避免被单个慢任务阻塞。

例如：

```text
网络 IO
磁盘 IO
区块加载
区块保存
压缩
NBT 编解码
世界生成
路径计算
异步任务
```

原则：

> 可以异步的 IO 不应该阻塞主 Tick。

但是涉及世界状态的操作必须保证确定性和线程安全。

---

## 3.3 内存

gmcs 应明显关注内存占用。

避免：

* 不必要的堆分配
* 高频临时对象
* 大量 interface{}
* 不必要的 map
* 重复字符串
* 重复 NBT 数据
* 每个实体都拥有巨大结构
* 每个方块都单独分配对象
* 无意义的数据复制

优先考虑：

* 紧凑的数据结构
* 数组
* slice
* bitset
* packed representation
* 对象复用
* buffer pool
* arena-like 生命周期管理
* 数据局部性
* 延迟分配

但是：

> 不要为了减少几个 allocation 而让代码变得不可维护。

所有重要内存优化都应该尽可能通过 benchmark 或 profiling 验证。

---

# 4. 性能优化规则

## 4.1 不允许凭感觉优化

不要使用以下形式作为性能论据：

> “这个应该更快。”

应该尽可能使用：

* benchmark
* pprof
* CPU profile
* memory profile
* allocation profile
* trace
* metrics
* 实际服务器测试

证明优化效果。

---

## 4.2 优化必须考虑实际收益

如果优化：

```text
复杂度显著增加
+
维护成本显著增加
+
兼容性风险增加
+
性能收益极小
```

则通常不应该采用。

---

## 4.3 优化必须避免回归

任何重要性能优化都应该考虑：

```text
Benchmark Before
Benchmark After
Memory Before
Memory After
Correctness
Compatibility
```

如果项目已经拥有 benchmark，应更新 benchmark。

如果没有，应考虑为关键路径增加 benchmark。

---

# 5. Minecraft 兼容性

gmcs 必须尽可能实现 Minecraft Java Edition 的真实行为，而不是“看起来能工作”。

## 5.1 协议

协议实现必须严格遵循对应 Minecraft 版本的协议。

注意：

* VarInt
* VarLong
* UUID
* Position
* NBT
* Slot
* Entity Metadata
* Chunk Data
* Paletted Container
* BitSet
* Compression
* Encryption
* Packet framing
* Packet ordering

不得随意修改协议格式。

---

## 5.2 版本支持

如果项目支持多个 Minecraft 版本：

* 必须明确版本边界。
* 不得混淆不同版本协议。
* 协议差异应该集中管理。
* 不要在整个代码库中散落大量版本判断。

优先使用：

```text
version-specific implementation
protocol abstraction
capability abstraction
```

而不是：

```go
if version >= xxx {
    ...
}
```

到处复制。

---

# 6. 并发模型

gmcs 是高性能服务器，因此并发模型必须经过设计。

## 6.1 Goroutine

不要：

```go
go func() {
    // 每个实体一个 goroutine
}()
```

或者：

```go
go func() {
    // 每个 packet 一个 goroutine
}()
```

这种无边界 goroutine 创建。

应该使用：

* worker pool
* bounded queue
* shard
* event loop
* region worker

等方式控制并发。

---

## 6.2 数据竞争

所有并发代码必须考虑：

* race condition
* deadlock
* livelock
* starvation
* data corruption

修改并发相关代码后，应尽可能运行：

```bash
go test -race ./...
```

---

## 6.3 锁

不要为了方便到处添加 mutex。

优先考虑：

1. 所有权
2. 单线程访问
3. shard
4. message passing
5. immutable data
6. atomic
7. mutex

但不要为了“无锁”而强行制造复杂的数据结构。

---

# 7. 世界与区块

Minecraft 世界是 gmcs 最重要的性能热点之一。

设计时应重点考虑：

* Chunk
* Section
* Palette
* Heightmap
* Block entities
* Entities
* Tick lists
* Lighting
* Biomes
* Chunk loading
* Chunk generation
* Chunk saving
* Chunk unloading

尽量避免：

```text
全局锁
全世界单一 Tick 队列
大量对象化 Block
频繁 Chunk 全量复制
同步磁盘 IO
```

可以考虑：

```text
region/shard
chunk ownership
dirty tracking
incremental save
async IO
compact storage
```

---

# 8. 网络

网络层必须关注：

* 零拷贝
* buffer reuse
* 减少 allocation
* 批量发送
* packet batching
* backpressure
* 限制单玩家发送队列
* 防止慢客户端拖垮服务器

不能为了优化而破坏：

* packet 顺序
* packet framing
* 协议兼容性
* 压缩语义
* 加密语义

---

# 9. IO

磁盘 IO 不应该无意义地阻塞 Tick。

特别注意：

* 世界保存
* 区块保存
* 区块加载
* 日志
* 配置
* 玩家数据

应尽量采用异步或批处理。

但是：

> 异步 IO 不代表可以忽略数据一致性。

服务器崩溃时必须尽量避免：

* 数据损坏
* 丢失大量玩家数据
* Chunk 数据损坏
* 部分写入导致无法读取

---

# 10. 测试要求

**测试不是可选项。**

任何重要功能都应该有测试。

至少考虑：

```text
Unit Test
Integration Test
Protocol Test
Compatibility Test
Benchmark
Regression Test
```

---

## 10.1 单元测试

对于纯逻辑模块，应优先编写单元测试。

例如：

* VarInt
* NBT
* Chunk
* Palette
* Entity
* Inventory
* Command parser
* Physics
* Redstone
* Serialization

---

## 10.2 回归测试

修复 bug 后：

> 如果 bug 可以被自动化测试表达，必须添加 regression test。

不要只修代码而不留下测试。

---

## 10.3 性能测试

对于关键性能路径，应该提供 benchmark：

```bash
go test -bench=. -benchmem ./...
```

如果性能优化是任务核心，必须尽可能给出优化前后的数据。

---

## 10.4 Race Test

并发相关修改应运行：

```bash
go test -race ./...
```

---

# 11. CI

项目必须保持 CI 健康。

至少应考虑：

```text
go test ./...
go test -race ./...
go vet ./...
go fmt
go test -bench
```

如果项目引入 lint 工具，也应该纳入 CI。

CI 不应该只检查“能不能编译”，而应该尽可能发现：

* 测试失败
* race
* 格式错误
* 静态分析问题
* API 破坏
* 基础性能回归

---

# 12. 文档

代码功能发生变化时，应同步考虑文档。

重要内容包括：

* README
* 架构文档
* 配置文档
* 协议文档
* 开发文档
* 性能文档
* Benchmark 结果
* 兼容性说明
* 构建方式
* 部署方式

不要让代码与文档长期严重不一致。

---

# 13. LLM Agent 工作规范

本项目会大量使用 LLM Agent 辅助开发。

Agent 必须遵守以下规则。

## 13.1 诚实

**禁止编造。**

如果没有确认：

* 不要声称已经验证
* 不要声称已经测试
* 不要声称与原版一致
* 不要声称性能提升
* 不要声称某 API 存在
* 不要声称某功能已经完成

正确做法：

```text
我没有运行测试。
```

```text
这个行为目前没有兼容性测试，因此无法确认。
```

```text
这个优化理论上可能降低 allocation，但尚未 benchmark。
```

---

## 13.2 不得因为工作量大而省略实现

如果任务要求：

> 完整实现某个功能

不能只提交：

```text
TODO
```

```text
placeholder
```

```text
stub
```

```text
panic("not implemented")
```

然后声称完成。

也不能：

> 因为代码很多，所以只实现核心部分。

如果确实无法一次完成：

1. 明确说明未完成部分。
2. 列出剩余工作。
3. 不要把部分实现描述成完整实现。
4. 保持当前代码可编译、可测试（如果条件允许）。

---

## 13.3 不得为了减少输出而删除必要代码

LLM 的输出成本不是省略正确代码的理由。

如果任务需要完整代码：

> 必须提供完整代码。

尤其不能因为代码较长而：

```text
// 其余代码省略
```

```text
// ... existing implementation ...
```

除非用户明确要求只展示 diff 或摘要。

---

# 14. LLM 成本控制

虽然不能因为节省 token 而牺牲正确性，但 Agent 仍应该合理控制成本。

## 14.1 先理解，再修改

不要无目的读取整个代码库。

优先：

1. 查看项目结构。
2. 定位相关模块。
3. 阅读入口和接口。
4. 阅读相关测试。
5. 阅读相关文档。
6. 再开始修改。

---

## 14.2 避免重复读取

已经确认的内容不要反复读取。

可以维护自己的工作上下文：

```text
Relevant files
Known constraints
Current architecture
Tests
Open questions
```

---

## 14.3 小范围修改

除非架构重构确实必要，否则：

> 尽量只修改与任务直接相关的文件。

避免无意义的大规模格式化或重构。

---

## 14.4 优先使用工具验证

不要通过反复推理代替可以快速完成的验证。

例如：

```bash
go test ./...
```

比猜测“应该没问题”更可靠。

---

## 14.5 控制上下文

阅读代码时优先：

```text
相关文件
相关函数
相关测试
调用关系
接口定义
```

而不是默认读取整个 repository。

---

# 15. Git 修改规范

修改代码前应理解当前工作区状态。

不要：

* 覆盖用户未提交的修改
* 删除无关文件
* 修改无关代码
* 重写整个文件只为了修改几行

保持 diff 尽可能清晰。

---

# 16. 依赖管理

添加依赖之前必须考虑：

* 是否真的需要
* 是否可以使用标准库
* 性能影响
* 内存影响
* 维护状态
* License
* 安全性
* 编译体积
* 跨平台兼容性

不要为了一个很小的功能引入巨大的依赖。

---

# 17. 错误处理

Go 代码必须认真处理 error。

禁止无理由：

```go
_ = err
```

```go
if err != nil {
    return nil
}
```

或者：

```go
panic(err)
```

尤其是在服务器运行时路径。

错误应该：

* 返回
* 包装
* 记录
* 或者根据明确的错误策略处理

---

# 18. 日志

日志必须考虑高负载环境。

不要在高频 Tick 路径中无条件打印大量日志。

避免：

```go
for {
    log.Printf(...)
}
```

生产环境应考虑：

* log level
* sampling
* rate limit
* structured logging

日志本身不能成为性能瓶颈。

---

# 19. 安全

Minecraft Server 是网络服务。

必须考虑：

* 恶意客户端
* malformed packet
* 超大数据包
* VarInt overflow
* NBT bomb
* 压缩炸弹
* 内存耗尽
* CPU exhaustion
* 玩家数据攻击
* 区块请求攻击
* 命令权限

任何来自网络的数据都应视为不可信。

---

# 20. 代码风格

使用标准 Go 风格。

优先：

```bash
gofmt
go vet
```

代码应该：

* 清晰
* 可维护
* 易测试
* 避免过度抽象
* 避免不必要的 interface
* 避免过深调用链
* 避免魔法数字

---

# 21. 架构设计原则

gmcs 应尽量形成清晰的层次：

```text
Network
   ↓
Protocol
   ↓
Session / Player
   ↓
Server
   ↓
World
   ↓
Chunk / Entity / Block
   ↓
Storage
```

具体结构可以根据实际项目调整。

不要为了符合这份文档而机械拆层。

---

# 22. 性能 Profiling

出现性能问题时，优先使用实际数据定位。

推荐工具：

```bash
go test -bench
go test -benchmem
go test -race
go tool pprof
go tool trace
```

必要时可以使用：

* CPU profiling
* heap profiling
* allocation profiling
* mutex profiling
* block profiling

不要根据函数名字猜测性能瓶颈。

---

# 23. Benchmark 规范

重要 benchmark 应尽量稳定。

例如：

```go
func BenchmarkChunkGetBlock(b *testing.B) {
    ...
}
```

应避免 benchmark 自己制造大量无关开销。

如果比较优化前后：

```text
旧实现
新实现
```

必须保证测试条件一致。

---

# 24. 兼容性验证

如果某个功能声称兼容原版，应尽可能使用：

```text
same input
same world state
same player action
same protocol sequence
```

比较：

```text
packet output
world state
entity state
inventory state
player state
persistent data
```

不能仅凭“客户端能连上”就认为兼容。

---

# 25. 遇到不确定问题

如果 Agent 遇到：

* Minecraft 内部行为不确定
* 协议细节不确定
* 原版 bug 行为不确定
* 性能数据缺失
* 项目设计意图不明确

不要猜测并继续大规模修改。

优先：

1. 搜索已有代码。
2. 搜索已有测试。
3. 查阅项目文档。
4. 查阅可靠资料。
5. 实验验证。
6. 必要时询问用户。

---

# 26. 完成任务前检查

任何任务完成前，Agent 应尽可能检查：

### 功能

* [ ] 功能是否真正实现？
* [ ] 是否存在 TODO / stub？
* [ ] 是否存在未实现路径？
* [ ] 是否处理错误？

### 测试

* [ ] 是否增加必要测试？
* [ ] 是否运行测试？
* [ ] 是否需要 race test？
* [ ] 是否需要 benchmark？

### 兼容性

* [ ] 是否破坏协议？
* [ ] 是否改变原版行为？
* [ ] 是否影响已有客户端？
* [ ] 是否影响已有世界数据？

### 性能

* [ ] 是否增加 allocation？
* [ ] 是否增加锁竞争？
* [ ] 是否增加 CPU？
* [ ] 是否造成新的 IO 阻塞？
* [ ] 优化是否经过 benchmark？

### 文档

* [ ] README 是否需要更新？
* [ ] API/配置文档是否需要更新？
* [ ] 架构文档是否需要更新？

### CI

* [ ] 是否可以通过 `go test ./...`
* [ ] 是否可以通过 `go vet ./...`
* [ ] 是否需要 `go test -race ./...`

---

# 27. 最终汇报规范

完成任务后，Agent 必须明确区分：

```text
已完成
已验证
未验证
已知限制
```

例如：

```text
已完成：
- 实现 Chunk cache
- 增加并发加载
- 增加 benchmark

已验证：
- go test ./...
- go test -race ./...
- benchmark

未验证：
- 尚未与原版服务器进行真实世界状态对比

已知限制：
- 当前 lighting 仍然是单 worker
```

不要把“代码写完”描述成“功能已经验证”。

---

# 28. 最重要的规则

如果只能记住几条规则，请记住：

1. **不要编造。**
2. **不要把未测试的东西说成已测试。**
3. **不要把部分实现说成完整实现。**
4. **不要因为代码很多就省略必要实现。**
5. **不要为了性能破坏原版兼容性。**
6. **不要凭感觉声称性能更好。**
7. **重要功能必须有测试。**
8. **重要性能优化必须尽可能有 benchmark/profile 数据。**
9. **并发代码必须考虑 race、deadlock 和数据一致性。**
10. **修改代码时保持 diff 清晰，避免无关重构。**
11. **优先解决真正的性能瓶颈，而不是追求表面上的“高性能代码”。**
12. **如果不知道，就明确说不知道，并进行验证。**

gmcs 的目标不是：

> “写一个能运行的 Minecraft Server。”

而是：

> **在保持 Minecraft Java Edition 行为与协议兼容性的前提下，构建一个真正高性能、低内存、高并发、可维护、可验证的 Minecraft Server。**
