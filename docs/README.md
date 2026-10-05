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