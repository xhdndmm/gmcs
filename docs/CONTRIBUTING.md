# 贡献指南

欢迎为 gmcs 贡献代码。本指南说明开发环境、修改约定、测试要求与提交流程，
面向人类贡献者与 LLM Agent。

> 项目最高优先级的开发规范是 [AGENTS.md](../AGENTS.md)（正确性、原版兼容、
> 诚实汇报、测试要求等）。本指南是它在日常协作流程上的补充；两者冲突时以
> AGENTS.md 为准。

## 1. 开始之前

建议按以下顺序了解项目：

1. [AGENTS.md](../AGENTS.md)：开发原则与硬性规则；
2. [README](README.md)：功能、配置与使用方式；
3. [TODO](TODO.md)：已完成内容、待办与已知限制；
4. [PERFORMANCE](PERFORMANCE.md)：关键路径基准与复现方式。

几条最常用的底线（摘自 AGENTS.md）：

- 正确性优先于性能；不得为优化改变可观察的 Minecraft 行为（§2.1、§4）
- 不确定的行为不要假装确定，先查证或加测试（§2.2、§25）
- 不要把未测试的东西说成已测试，不要把部分实现说成完整实现（§13、§28）
- 性能结论必须有 benchmark / profile 数据支撑（§4.1）
- 修复 bug 后，能用自动化测试表达的必须留下回归测试（§10.2）

## 2. 开发环境

- Go 工具链：版本下限见 [go.mod](../go.mod)（当前为 1.26）
- 项目只使用标准库，无需安装第三方依赖
- `go test -race` 需要 CGO 与受支持的平台；不支持时使用
  `scripts/test.sh --no-race`
- 只有"更换 Minecraft 版本、重新生成数据"才需要 Java（官方服务端 jar）与
  官方客户端 jar，步骤见 [MIGRATION.md](MIGRATION.md) 第 3 节

常用命令：

```bash
go build ./...              # 构建全部包
go run ./cmd/gmcs           # 本地运行（首次生成 gmcs.json 与 world/）
scripts/build.sh            # 构建当前平台产物（dist/）
scripts/build.sh --all      # 交叉编译全部常用平台
```

格式、静态检查与测试：

```bash
gofmt -l .                  # 格式检查（应无输出）
go vet ./...
go test ./...
go test -race ./...
scripts/test.sh             # gofmt + build + vet + test + race，一步到位
scripts/test.sh --no-race   # 平台不支持 race 时
scripts/test.sh --bench     # 附加 benchmark
go test -run=^$ -bench=. -benchmem ./...   # 仅 benchmark
```

## 3. 项目结构

| 路径 | 职责 | 注意点 |
| --- | --- | --- |
| `cmd/gmcs` | 入口：加载配置、启动服务器 | 新增启动参数需同步 README |
| `internal/config` | 配置结构、JSON 持久化与校验 | 新字段需同步 README 配置表与 MIGRATION 字段演化表 |
| `internal/protocol` | 协议编解码（握手/登录/配置/Play、NBT、Slot、加密等） | 数据包 ID 与格式严格对应 1.21.11（协议 774） |
| `internal/registry` | 注册表、方块状态、物品与静态 ID 数据 | `*_generated.go` 由工具生成，禁止手改 |
| `internal/world` | 区块模型、地形生成、区域文件存储 | 存储负载为 gmcs 自定义格式（magic `GMCS`） |
| `internal/item` | 物品堆栈与玩家物品栏 | 与注册表物品 ID 表配合 |
| `internal/server` | 会话生命周期、世界循环、实体/战斗、命令 | 并发与生命周期集中在这里 |
| `tools/genregistries` | 数据生成器 | 调整数据管线时改它并重新生成 |
| `scripts` | 构建与测试脚本 | 新增检查请同步 CI（`.github/workflows/ci.yml`） |
| `docs` | 文档 | 更新场景见第 6 节 |

## 4. 修改约定

### 4.1 保持 diff 清晰

- 只改与任务直接相关的文件；不做无关重构或大规模格式化
- 不覆盖工作区中未提交的改动；提交前先看 `git status` / `git diff`
- 不删除无关文件；不重写整个文件只为了改几行

### 4.2 错误处理

- 认真处理 error：返回、包装（`%w`）或记录；运行时路径禁止 `panic`
- 禁止无理由的 `_ = err`，也不要吞掉错误后返回空值

### 4.3 并发

- 禁止无界 goroutine（每包、每实体、每连接直接 `go func()`）；使用 worker、
  有界队列或明确归属（ownership）模型
- 明确数据归属：例如会话读写由会话自身串行化，生物状态由 `entityMu` 保护
- 修改并发代码后必须运行 `go test -race ./...`

### 4.4 性能

- 不以"感觉更快"作为论据；用 `go test -bench` 或 profile 验证
- 关键路径（区块生成/编码、实体包、Tick）避免无谓分配；缓冲能复用就复用
  （参考 `EncodeChunkDataPacket` 的缓冲预分配）
- 性能改动应给出优化前后数据（记录到 [PERFORMANCE.md](PERFORMANCE.md)），
  并证明协议输出不变（可用对照测试，如 `TestPackIndirectMatchesPackBits`）

### 4.5 协议与版本

- 协议实现必须严格对应目标版本（当前 1.21.11 / 774）；不猜测字段顺序与编码
- 不同版本差异应集中管理（版本特定实现/能力抽象），不要散落
  `if version >= ...`
- 协议相关修改必须附带测试；无法确认真实行为时，明确说明不确定性并实验验证

### 4.6 生成文件

- `internal/registry/*_generated.go` 由 `tools/genregistries` 生成，禁止手改
- 数据源与重新生成方式见 [MIGRATION.md](MIGRATION.md) 第 3 节

## 5. 测试要求

### 5.1 什么时候必须加测试

| 改动类型 | 最低要求 |
| --- | --- |
| bug 修复 | 能用自动化测试表达的必须加回归测试 |
| 新功能 | 单元测试；涉及网络生命周期时加集成测试 |
| 协议编解码 | 协议测试（编码字节序列 + 解码健壮性/非法输入） |
| 并发 | `go test -race` 通过；必要时增加并发用例 |
| 性能优化 | benchmark + 优化前后数据 |

### 5.2 测试写法

- 与被测代码同包（`xxx_test.go`），优先表格驱动
- 集成测试通过真实 TCP 会话进行；`internal/server` 的测试提供了 `joinServer`
  等帮助函数（定义在 `internal/server/entity_test.go`，用法示例见
  `session_test.go`、`auth_test.go`），可复用其模式
- 测试不得依赖真实网络（显式集成测试监听 localhost 除外），不得依赖时序巧合
- benchmark 放在 `benchmark_test.go`，命名 `BenchmarkXxx`，避免与被测目标无关的开销

### 5.3 验证与汇报

- 提交前至少运行 `scripts/test.sh`（若未运行，必须在汇报中明确说明）
- 汇报时区分：已完成 / 已验证 / 未验证 / 已知限制（AGENTS.md §27）

## 6. 文档要求

代码行为变化时同步更新对应文档：

| 改动 | 需要更新 |
| --- | --- |
| 新增/修改配置字段 | README 配置表；MIGRATION 字段演化表 |
| 新增/修改命令 | README 内置命令表 |
| 可观察行为变化、限制解除或新增 | README 对应章节；TODO 已知限制 |
| 性能优化/新增 benchmark | PERFORMANCE.md |
| 协议号、适配版本变化 | README、TODO 头部、MIGRATION |
| 新增文件格式或存储变化 | MIGRATION 兼容性一览 |

## 7. 提交与 PR

### 7.1 提交信息

- 风格：`<type>: <简述>`，type 常用 `feat` / `fix` / `perf` / `refactor` /
  `test` / `chore` / `docs`；简述可用中文或英文
- 一个提交只做一件事；正文建议列出行为影响、验证方式与已知限制

示例：

```text
perf: 区块编码热路径优化（调色板线性查找 + 缓冲预分配）

- 实测 68.9 µs/op、4 allocs/op，协议输出逐字节不变
- 新增等价性测试与 benchmark

已验证：scripts/test.sh（含 race）、go test -bench=. ./internal/world/
```

### 7.2 PR 流程

1. 新建分支并提交改动；
2. 确保 CI（GitHub Actions：gofmt、vet、单元测试、race、交叉编译矩阵）全绿；
3. PR 描述包含：动机、改动摘要、验证方式（命令与结果）、兼容性影响与已知限制；
4. 评审重点：行为/协议兼容性、测试覆盖、性能数据、文档同步。

大型改动（协议重构、存储格式变化、架构调整）请先开 Issue 说明方案，避免返工。
提交入口：<https://github.com/xhdndmm/gmcs>。

## 8. 许可证

项目使用 [MIT License](../LICENSE)。提交贡献即表示同意以相同许可证发布。
