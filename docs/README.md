# gmcs

一个用 Go 语言编写的高性能 Minecraft Java Edition 服务器实现（当前适配 1.21.11，协议 774）。

## 构建与运行

```bash
# 构建当前平台（产物在 dist/）
scripts/build.sh

# 交叉编译全部常用平台
scripts/build.sh --all

# 运行：首次运行会在工作目录生成 gmcs.json 与 world/
go run ./cmd/gmcs
go run ./cmd/gmcs -config /path/to/gmcs.json -listen :25565
```

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
| `world_seed` | 地形生成种子（相同种子生成相同地形） | `0` |
| `game_mode` | 新玩家的游戏模式（survival/creative/adventure/spectator） | `survival` |
| `spawn_monsters` | 是否在玩家附近生成敌对生物 | `true` |
| `max_mobs` | 同时存在的生物上限（0 表示不生成） | `8` |
| `autosave_seconds` | 自动保存间隔（秒，0 表示禁用） | `300` |
| `online_mode` | 启用正版验证（通过会话服务器确认账号） | `false` |
| `session_server_url` | 会话验证服务基地址（在线模式使用） | `https://sessionserver.mojang.com` |
| `starting_items` | 新玩家初始物品（命名空间 ID，每个 1 个） | `["minecraft:stone"]` |

## 内置命令

进入世界后可在聊天栏使用（已通过 Declare Commands 向客户端声明）：

| 命令 | 说明 |
| --- | --- |
| `/help` | 显示可用命令 |
| `/list` | 列出在线玩家 |
| `/say <消息>` | 向所有玩家广播消息 |
| `/spawn` | 传送回出生点 |
| `/gamemode <模式>` | 切换游戏模式（survival/creative/adventure/spectator） |

## 世界生成

地形由 **种子** 决定（`world_seed`）：多层值噪声的高度图（高度约 57–70）、
石头/泥土/草方块层次、低洼处的水（海平面 Y=62，近似值）、沙滩与稀疏的橡树。
相同种子、相同坐标总是生成相同方块，且不依赖区块生成顺序；出生点高度取实际地形。

注意：这不是原版算法，不含洞穴、矿物、结构、生物群系差异。已保存的区块不会因更换
`world_seed` 而重新生成：升级自旧版本（超平坦地形）或修改种子后，旧区块与新区块的
交界处可能出现地形突变；需要全新地形时请换用新的 `world_dir` 或清空旧存档。

## 生物与战斗

- 服务器会在玩家附近（12–24 格）生成僵尸，直至 `max_mobs` 上限；远离玩家（>64 格）自动清理。
- 僵尸会直线追击 32 格内的最近玩家（约 1.1 格/秒）并在 1.9 格内攻击（每次 2 点伤害）；
  没有目标时随机游荡。
- 生命值：玩家 20 点、僵尸 20 点；玩家徒手攻击 4 点（固定值）、僵尸攻击 2 点；
  玩家受伤后有 0.5 秒无敌帧，创造/旁观模式免疫伤害。
- 伤害反馈：Damage Event、Hurt Animation、实体音效与 Set Health 均已发送；
  生物死亡播放死亡动画后移除；玩家死亡显示死亡消息，重生后回到出生点并恢复满生命。
- 服务端会跟踪玩家移动数据包（含坐标合法性校验），并用于生物追击与攻击距离判定。

当前限制：AI 为简化的直线追击/游荡（无寻路与避障）；无饥饿系统与生命恢复；
无击退、暴击、护甲与掉落物/经验；生物生成不区分昼夜与光照；
生物头部朝向在移动后不单独更新（未发送 Rotate Head）。

## 正版验证（online_mode）

`online_mode = true` 时启用正版登录：服务器与客户端完成 AES-128/CFB8 加密握手，
并通过 `session_server_url` 的 `hasJoined` 接口验证账号，登录成功后透传玩家属性（如签名皮肤）。
`online_mode = false`（默认）时按离线模式运行，UUID 由用户名按原版规则推导。

## 测试

```bash
scripts/test.sh          # gofmt + build + vet + 单元测试 + race
scripts/test.sh --bench  # 附加 benchmark
go test -race ./...      # 仅数据竞争检测
```

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
docs                文档（TODO、路线图）
```

## 路线图

见 [docs/TODO.md](TODO.md)。

## 许可证
[MIT License](../LICENSE)