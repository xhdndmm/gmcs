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
| `ops` | 管理员玩家名列表（使用 `/say`、`/gamemode` 等管理命令；可写数组或单个字符串） | `[]` |

## 内置命令

进入世界后可在聊天栏使用（已通过 Declare Commands 向客户端声明）：

| 命令 | 说明 |
| --- | --- |
| `/help` | 显示可用命令 |
| `/list` | 列出在线玩家 |
| `/say <消息>` | 向所有玩家广播消息（需要管理员） |
| `/spawn` | 传送回出生点 |
| `/gamemode <模式>` | 切换游戏模式（需要管理员；survival/creative/adventure/spectator） |

命令补全（Tab）覆盖命令名与 `/gamemode` 参数；`ops` 名单内玩家可见/可用管理命令
（命令树按玩家过滤，执行时再次校验权限）。

## 功能一览

### 世界与地形

- **种子地形**（`world_seed`）：噪声高度图、层次方块、水（海平面约 Y=62）、沙滩与稀疏橡树；
  同种子同坐标结果恒定，出生点取实际地形。非原版算法（无洞穴/矿物/结构/群系差异）。
- **区块流式加载**：进入世界按由近到远发送视距内的区块；跨区块移动时增量发送、
  超出 视距+2 卸载；每 5 秒把距所有玩家超过 视距+4 的区块移出内存（未保存先落盘），
  跑图不会让内存持续增长。
- **heightmap**：区块数据携带 WORLD_SURFACE/MOTION_BLOCKING 高度图（方块修改
  即时生效），供客户端渲染与光照使用。
- **世界边界**（`world_border_size`）：正方形、以 (0,0) 为中心；下发官方边界包，
  越界位置被拉回，生物不会生成或移动出边界。
- 更换 `world_seed` 不会重建已有区块，交界处可能出现地形突变；需要全新地形时
  换用新的 `world_dir`。

### 生物与战斗

- 玩家附近（12–24 格）生成僵尸直至 `max_mobs`；距所有玩家超过 64 格清理
  （服务器无玩家时不清理）。僵尸追踪 32 格内最近玩家，进入 1.9 格且视线无遮挡时
  先抬手 0.5 秒再造成 2 点伤害；无目标时游荡，被挡久后随机侧移；创造/旁观不参与索敌。
- 数值：玩家/僵尸各 20 点生命；玩家攻击 4 点；受击无敌帧 0.5 秒与击退；
  受伤后 8 秒未再受伤则每 4 秒恢复 1 点。
- 反馈完整：挥手/受伤闪红/死亡动画、Damage Event、音效、生命同步、头部朝向广播。
- 生物持久化到 `world/entities.json`（位置/生命/朝向），重启后恢复。
- 限制：直线 AI（无寻路）、无饥饿/暴击/护甲/掉落物经验、生成不分昼夜光照；
  完整列表见 [TODO.md](TODO.md)。

### 方块交互与玩家物理

- 生存/创造可破坏与放置方块：修改广播附近玩家并随区块保存；生存挖掘完成后生效、
  放置消耗物品；创造即时且不消耗；支持创造物品栏取放；冒险/旁观不可交互；无掉落物与挖掘时间。
- 摔落伤害：下落超过 3 格落地受伤，每多 1 格 1 点（`ceil(下落−3)`）；落水免疫，
  传送/重生/回拉后重置；未实现服务器端重力、其它摔落保护与速度/穿墙校验。

### 多人

- 玩家以实体互相可见：加入/退出、移动与朝向在 64 格内同步；皮肤来自正版档案属性。
- 可攻击其他玩家：伤害/受伤动画/生命同步与生物战斗一致；挥空也播放挥手动画。

### 玩家数据（players.json）

- 退出/关服与每 30 秒周期保存位置、生命/饥饿/饱和、游戏模式与物品栏，重连恢复且不重复发放
  `starting_items`；死亡状态同样持久化（重连后保持死亡，发送重生请求后复活）；
  物品按命名空间 ID 存储，跨版本可读。
- gmcs 自定义格式（非原版 `playerdata`）；强杀或崩溃最坏丢失约 30 秒进度。

### 正版验证（online_mode）

- 启用后完成 AES-128/CFB8 加密握手并通过 `hasJoined` 验证账号，透传签名属性；
  服务器列表与游戏内标记 `enforcesSecureChat=true`，玩家列表携带皮肤。
- 聊天消息未签名（无 chat_session_update），客户端显示“未验证”标记。
- 默认离线模式：UUID 按原版离线规则由用户名推导。

## 测试

```bash
scripts/test.sh          # gofmt + build + vet + 单元测试 + race
scripts/test.sh --bench  # 附加 benchmark
go test -race ./...      # 仅数据竞争检测
```

关键路径的 benchmark 方法与实测数据见 [docs/PERFORMANCE.md](PERFORMANCE.md)。

## 数据生成（更换 Minecraft 版本时）

```bash
# 1. 用官方服务端 jar 生成数据报告（reports/registries.json、blocks.json 等）
java -DbundlerMainClass=net.minecraft.data.Main -jar server.jar --reports

# 2. 从官方客户端 jar 与报告生成本仓库的注册表/方块/物品数据
go run ./tools/genregistries \
  -jar <client.jar> \
  -reports generated/reports/registries.json \
  -blocks generated/reports/blocks.json
```

## 目录结构

```text
cmd/gmcs            服务器入口
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