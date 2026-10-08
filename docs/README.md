<div align="center">

# gmcs

一个用 Go 语言编写的**高性能** Minecraft Java Edition 服务器实现（当前适配 1.21.11，协议 774）。

<p>
  <a href="https://github.com/xhdndmm/gmcs/stargazers"><img src="https://img.shields.io/github/stars/xhdndmm/gmcs" alt="GitHub Stars"></a>
  <a href="https://github.com/xhdndmm/gmcs/issues"><img src="https://img.shields.io/github/issues/xhdndmm/gmcs" alt="GitHub Issues"></a>
  <a href="./LICENSE"><img src="https://img.shields.io/badge/license-MIT-green" alt="MIT License"></a>
  <a href="https://go.dev/dl/"><img src="https://img.shields.io/badge/Go-1.26%2B-blue" alt="Go 1.26+"></a>
  <a href="https://github.com/xhdndmm/gmcs/releases"><img src="https://img.shields.io/github/v/tag/xhdndmm/gmcs?label=release" alt="Latest Release"></a>
  <a href="https://github.com/xhdndmm/gmcs/releases"><img src="https://img.shields.io/github/downloads/xhdndmm/gmcs/total" alt="Downloads"></a>
</p>

</div>

## 快速开始

```bash
scripts/build.sh            # 构建当前平台（产物在 dist/）
scripts/build.sh --all      # 交叉编译全部常用平台
go run ./cmd/gmcs           # 运行；首次启动生成 gmcs.json 与 world/
```

构建产物为纯 Go 静态二进制（`CGO_ENABLED=0`、`-trimpath`、剥离符号）并默认启用
**PGO**（配置随仓库提交，`scripts/genpgo.sh` 可重新生成；`PGO=off` 可关闭）。
性能与产物体积数据见 [PERFORMANCE.md](PERFORMANCE.md)。

## 配置（gmcs.json）

首次运行自动生成；缺失字段回退默认值；命令行 `-listen` 可覆盖监听地址。

| 字段 | 说明 | 默认值 |
| --- | --- | --- |
| `listen_address` | TCP 监听地址 | `:25565` |
| `motd` | 服务器列表消息 | `A gmcs 1.21.11 development server` |
| `version_name` | 列表中显示的版本名 | `gmcs-1.21.11` |
| `protocol_version` | 协议号（同时用于拒绝不兼容客户端） | `774` |
| `max_players` | 列表显示的最大玩家数 | `100` |
| `max_connections` | 最大并发连接数 | `256` |
| `view_distance` | 发送给客户端的视距（区块，2–32） | `10` |
| `world_dir` | 地图数据目录 | `world` |
| `world_seed` | 地形生成种子（首次启动随机生成并固定） | 随机 |
| `world_border_size` | 世界边界边长（方块，正方形，以 0,0 为中心；0 不限制） | `0` |
| `game_mode` | 新玩家的游戏模式（survival/creative/adventure/spectator） | `survival` |
| `spawn_monsters` | 是否在玩家附近生成敌对生物 | `true` |
| `max_mobs` | 同时存在的生物上限（0 表示不生成） | `8` |
| `autosave_seconds` | 自动保存间隔（秒，0 表示禁用） | `300` |
| `online_mode` | 启用正版验证（通过会话服务器确认账号） | `false` |
| `session_server_url` | 会话验证服务基地址（在线模式使用） | `https://sessionserver.mojang.com` |
| `starting_items` | 新玩家初始物品（命名空间 ID；`name` 或 `name*数量`，数量上限 64；可写数组或单个字符串） | `["minecraft:stone"]` |
| `ops` | 管理员玩家名列表（使用管理命令；项可写 `name`（等价 4 级）或 `name:level`（1–4）；可写数组或单个字符串） | `[]` |
| `permissions` | 细粒度命令权限节点（按玩家名，大小写不敏感；`"*"` 表示所有玩家；支持 `gmcs.command.*` 前缀通配与 `*` 全通配） | `{}` |
| `pprof_address` | pprof 诊断监听地址（如 `127.0.0.1:6060`，空值表示关闭；仅本地诊断，勿暴露公网） | `""` |

## 内置命令

进入世界后可在聊天栏使用（已通过 Declare Commands 向客户端声明）：

| 命令 | 说明 |
| --- | --- |
| `/help` | 显示可用命令 |
| `/list` | 列出在线玩家 |
| `/say <消息>` | 向所有玩家广播消息（需要管理员） |
| `/spawn` | 传送回出生点 |
| `/gamemode <模式>` | 切换游戏模式（需要管理员；survival/creative/adventure/spectator） |
| `/kick <玩家>` | 踢出在线玩家（需要权限等级 3） |

命令补全（Tab）覆盖命令名、`/gamemode` 参数与 `/kick` 在线玩家名，聊天输入时还会补全在线玩家名；
`ops` 名单项支持 `name`（等价 4 级）或 `name:level`（1–4 级），命令树按玩家等级过滤并在执行时再次校验
（`/say`、`/gamemode` 需 2 级，`/kick` 需 3 级；`/help`、`/list`、`/spawn` 所有玩家可用）。
也可以不改 `ops`，而在 `permissions` 中按玩家授予权限节点（如
`"permissions": {"Alice": "gmcs.command.gamemode", "*": "gmcs.command.spawn"}`）：
拥有节点即可使用对应命令，节点形式为 `gmcs.command.<命令名>`。

## 功能一览

### 世界与地形

- **种子地形**（`world_seed`）：噪声高度图、层次方块、水（海平面约 Y=62）、沙滩与稀疏橡树；
  同种子同坐标结果恒定，出生点取实际地形。非原版算法（无洞穴/矿物/结构/群系差异）。
- **区块流式加载**：进入世界按由近到远发送视距内的区块；跨区块移动时增量发送、
  超出 视距+2 卸载；每 5 秒把距所有玩家超过 视距+4 的区块移出内存（未保存先落盘），
  跑图不会让内存持续增长。区块突发以 Chunk Batch Start/Finished 标记（客户端据此
  调整加载速率；暂未按客户端回报速率限流）。
- **heightmap**：区块数据携带 WORLD_SURFACE/MOTION_BLOCKING 高度图（方块修改
  即时生效），供客户端渲染与光照使用。
- **世界边界**（`world_border_size`）：正方形、以 (0,0) 为中心；下发官方边界包，
  越界位置被拉回，生物不会生成或移动出边界。
- 更换 `world_seed` 不会重建已有区块，交界处可能出现地形突变；需要全新地形时
  换用新的 `world_dir`。

### 生物与战斗

- 敌对生物只在**夜晚**（世界时间 13000–23000）的玩家周围 24–36 格生成，直至 `max_mobs`；
  距所有玩家超过 64 格清理（服务器无玩家时不清理）。僵尸追踪 32 格内最近玩家，进入 1.9 格且
  视线无遮挡时先抬手 0.5 秒再造成 2 点伤害；无目标时游荡，被挡久后随机侧移；创造/旁观不参与索敌。
- **昼夜循环**：24000 tick（20 分钟）一天，进入世界与每 20 tick 同步世界时间；`/time` 之类的
  时间命令未实现。无光照引擎，因此刷怪只看时间、不看亮度（洞穴内白天不刷怪）。
- 数值：玩家/僵尸各 20 点生命；玩家攻击 4 点，下落中攻击为跳跃暴击（×1.5）；受击无敌帧
  0.5 秒与击退；僵尸死亡掉落 0–2 腐肉与稀有铁锭/胡萝卜/土豆，击杀获得 5 点经验。
- **饥饿系统**：疾跑/跳跃/攻击/受伤积累疲劳度（每 4 点消耗 1 点饥饿值）；饥饿值 ≥18 时
  每 4 秒恢复 1 点生命（优先消耗饱食度），饥饿值归零后每 4 秒受 1 点饥饿伤害（最低到 1）；
  使用食物物品可恢复饥饿与饱食度（数值来自官方物品数据，共 44 种食物）。
- **经验**：击杀生物获得；随玩家数据持久化，死亡清空（同原版）。
- 反馈完整：挥手/受伤闪红/死亡动画、Damage Event、音效、生命/饥饿/经验同步、头部朝向广播。
- 生物持久化到 `world/entities.json`（生物位置/生命/朝向与掉落物物品/数量/位置），重启后恢复。
- 限制：仅僵尸一种生物、直线 AI（无寻路）、无护甲/经验球实体、无光照引擎；
  完整列表见 [TODO.md](TODO.md)。

### 方块交互与玩家物理

- 生存/创造可破坏与放置方块：修改广播附近玩家并随区块保存；生存挖掘完成后生效、
  放置消耗物品；创造即时且不消耗；支持创造物品栏取放；冒险/旁观不可交互；无挖掘时间。
- **容器交互**：右键箱子/陷阱箱/木桶（27 槽）、发射器/投掷器（9 槽）、漏斗（5 槽）与
  潜影盒（含 16 色）打开窗口；支持取放（左/右）、Shift 快速移动、数字键交换、创造复制、
  Q 丢弃、拖拽分发与双击收集；内容随区块持久化，破坏时内容掉落；潜行右键改为放置方块。
- 破坏掉落：生存模式按简化掉落表生成掉落物（石头→圆石、草方块→泥土、矿石→原矿/宝石、
  玻璃/树叶/冰无掉落等，见 `internal/server/block_drops.go`）；创造模式不掉落。
  不实现工具要求、精准采集、时运与概率掉落（砂砾的燧石等）。
- 掉落物与拾取：生存/创意下 Q 丢 1 个、Ctrl+Q 丢整组（沿视线抛出）；掉落物有重力、
  逐轴 AABB 碰撞与摩擦，落地或浮于水面后停稳，同类堆叠周期合并（每 4 tick、0.5 格内、
  不隔墙），走近 1 格内自动拾取（含拾取动画与音效）；5 分钟消失、掉入虚空/岩浆/火/仙人掌移除；
  死亡掉落整个物品栏；随 `world/entities.json` 持久化。
- 堆叠上限按物品数据（官方 `minecraft:max_stack_size`，如雪球 16、工具 1），
  背包合并/容器点击/创造物品栏/掉落物都按实际上限处理。
- **合成**：玩家物品栏 2×2 与工作台（右键打开）3×3 合成；配方数据驱动
  （官方 recipe JSON：706 有序、304 无序、116 烹饪；支持标签原料），
  结果槽实时计算，取走产物消耗原料，支持 Shift 快速合成；关闭窗口合成格归还背包。
- **熔炉家族**：熔炉/高炉/烟熏炉（右键打开）：原料+燃料熔炼，双进度条
  （Set Container Property），高炉/烟熏炉双倍速，熔炼获得经验，
  燃烧与烹饪进度随区块保存（断线/重启后继续）。
- **末影箱**：右键打开按玩家独立的 27 槽空间（所有末影箱共享同一份内容），
  随玩家数据（`players.json` 的 `ender_inventory`）持久化。
- **玩家皮肤层**：其他玩家可见的皮肤层（披风/外衣/袖子/裤腿/帽子）按客户端
  Client Information 设置显示（实体元数据索引 16）。
- **方块碰撞形状**：全部方块状态的碰撞盒表（半砖/台阶/栅栏/墙/栅栏门/门/活板门/
  雪层/仙人掌/蛋糕/耕地/床/箱子/压力板等，常量取自原版实现），玩家移动、摔落与
  掉落物物理统一使用形状级 AABB 碰撞（相触可贴面滑动）。
- 摔落伤害：下落超过 3 格落地受伤，每多 1 格 1 点（`ceil(下落−3)`）；改由**服务器端
  支撑面检测**（脚底碰撞形状顶面）判定落地（不依赖客户端标志），落水/游泳中断下落；
  落在干草堆（×0.2）/床（×0.5）/粘液块/蜂蜜块/细雪上免伤或减伤；传送/重生/回拉后重置。
- 移动校验：拒绝 NaN/越界 Y、单包超过 100 格与**逐 tick 速度超限**（允许距离 ≤10×√tick 数，
  与原版一致）的位移，并检测**穿墙**（终点嵌入方块碰撞形状时拒绝并拉回）。

### 多人

- 玩家以实体互相可见：加入/退出、移动与朝向在 64 格内同步；皮肤来自正版档案属性。
- 可攻击其他玩家：伤害/受伤动画/生命同步与生物战斗一致；挥空也播放挥手动画。

### 移动校验与诊断

- 位置校验：拒绝 NaN/越界 Y、单包超过 100 格的位移与逐 tick 速度超限（见上文），
  并把客户端拉回服务器记录的位置。
- `pprof_address`：启用后在 `/debug/pprof/*` 提供 CPU/内存/goroutine 分析
  （`go tool pprof http://127.0.0.1:6060/debug/pprof/profile?seconds=20`）。
- `cmd/gmcsload`：离线模式负载压测客户端（登录 → 配置 → 进入世界 → 持续移动），
  输出进入世界耗时、收包量、流量与包类型分布；单机 loopback 结果见
  [docs/PERFORMANCE.md](PERFORMANCE.md)。

### 玩家数据（players.json）

- 退出/关服与每 30 秒周期保存位置、生命/饥饿/饱和、经验、游戏模式与物品栏，重连恢复且不重复发放
  `starting_items`；死亡状态同样持久化（重连后保持死亡，发送重生请求后复活）；
  物品按命名空间 ID 存储，跨版本可读。
- gmcs 自定义格式（非原版 `playerdata`）；强杀或崩溃最坏丢失约 30 秒进度。

### 正版验证（online_mode）

- 启用后完成 AES-128/CFB8 加密握手并通过 `hasJoined` 验证账号，透传签名属性；
  服务器列表与游戏内标记 `enforcesSecureChat=true`，玩家列表携带皮肤。
- 聊天会话（`chat_session_update`）：服务器保存客户端上报的会话公钥，并通过 Player Info 的
  Initialize Chat 动作分发给其他玩家；但消息签名不做服务器端验证，广播仍按未签名消息发送
  （客户端显示“未验证”标记）。
- 默认离线模式：UUID 按原版离线规则由用户名推导。

## 测试

```bash
scripts/test.sh          # gofmt + build + vet + 单元测试 + race
scripts/test.sh --bench  # 附加 benchmark
go test -race ./...      # 仅数据竞争检测

go run ./cmd/gmcsload -addr 127.0.0.1:25565 -players 50 -duration 20s  # 负载压测
```

关键路径的 benchmark 方法与实测数据见 [docs/PERFORMANCE.md](PERFORMANCE.md)。

## 数据生成（更换 Minecraft 版本时）

```bash
# 1. 用官方服务端 jar 生成数据报告（reports/registries.json、blocks.json、items.json 等）
java -DbundlerMainClass=net.minecraft.data.Main -jar server.jar --reports

# 2. 从官方客户端 jar 与报告生成本仓库的注册表/方块/物品/食物/堆叠上限数据
go run ./tools/genregistries \
  -jar <client.jar> \
  -reports generated/reports/registries.json \
  -blocks generated/reports/blocks.json \
  -items generated/reports/items.json
```

## 目录结构

```text
cmd/gmcs            服务器入口
cmd/gmcsload        负载压测客户端（离线模式模拟并发玩家）
internal/config     配置结构与 JSON 持久化
internal/protocol   协议包编解码（握手/登录/配置/Play、NBT、Slot 等）
internal/registry   生成的注册表、方块状态与物品 ID 数据
internal/world      区块模型、地形生成、区域文件存储、世界管理器
internal/item       物品堆栈与玩家物品栏
internal/server     服务器、会话生命周期、玩家列表与广播
tools/genregistries 数据生成器
scripts             跨平台构建与测试脚本
docs                文档（贡献指南、迁移指南、TODO、性能基准）
```

## 相关文档

- [docs/TODO.md](TODO.md)：进度、待办与已知限制（路线图）
- [docs/PERFORMANCE.md](PERFORMANCE.md)：性能基准与复现方式
- [docs/CONTRIBUTING.md](CONTRIBUTING.md)：贡献指南（开发环境、测试与提交流程）
- [docs/MIGRATION.md](MIGRATION.md)：迁移指南（升级、更换 Minecraft 版本、与原版互迁）

## 许可证
[MIT License](../LICENSE)