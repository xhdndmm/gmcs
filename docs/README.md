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
| `world_seed` | 地形生成种子（首次启动随机生成并固定） | 随机 |
| `world_border_size` | 世界边界边长（方块，正方形，以 0,0 为中心；0 不限制） | `0` |
| `game_mode` | 新玩家的游戏模式（survival/creative/adventure/spectator） | `survival` |
| `spawn_monsters` | 是否在玩家附近生成敌对生物 | `true` |
| `max_mobs` | 同时存在的生物上限（0 表示不生成） | `8` |
| `autosave_seconds` | 自动保存间隔（秒，0 表示禁用） | `300` |
| `online_mode` | 启用正版验证（通过会话服务器确认账号） | `false` |
| `session_server_url` | 会话验证服务基地址（在线模式使用） | `https://sessionserver.mojang.com` |
| `starting_items` | 新玩家初始物品（命名空间 ID；`name` 或 `name*数量`，数量上限 64） | `["minecraft:stone"]` |

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

服务器按 `view_distance` 持续维护玩家周围的区块：进入世界时由近到远发送视距内的
全部区块，跨越区块边界时增量发送新区块并卸载超出 视距+2 的旧区块，因此玩家在视距内
移动不会再看到虚空。

服务器同时控制内存占用：每 5 秒把距离所有玩家都超过 视距+4 的区块移出内存缓存
（未保存的区块会先写入磁盘），玩家再次接近时重新读取或重新生成，长时间跑图
不会让内存持续增长。

注意：这不是原版算法，不含洞穴、矿物、结构、生物群系差异。已保存的区块不会因更换
`world_seed` 而重新生成：升级自旧版本（超平坦地形）或修改种子后，旧区块与新区块的
交界处可能出现地形突变；需要全新地形时请换用新的 `world_dir` 或清空旧存档。

## 世界边界（world_border_size）

`world_border_size` 大于 0 时，服务器在玩家进入世界时下发官方世界边界包：
客户端显示淡蓝色边界线并阻止玩家走出；服务器也会拒绝越界的位置更新并把
玩家拉回最后合法位置，生物不会生成或移动出边界。边界为正方形，恒以 (0,0) 为中心。

## 生物与战斗

- 服务器会在玩家附近（12–24 格，且在边界内）生成僵尸，直至 `max_mobs` 上限；距离所有玩家超过 64 格时清理，服务器上没有玩家时不清理（与原版一致）。
- 僵尸会追踪 32 格内的最近玩家（约 1.1 格/秒）；进入 1.9 格且视线无遮挡时先抬手 0.5 秒，
  然后广播挥手动画并造成 2 点伤害；没有目标时随机游荡，被方块挡住一段时间后会随机侧移。
- 视线遮挡（隔墙）时生物不会造成伤害；创造/旁观模式玩家不会被索敌，旁观者也无法攻击。
- 生命值：玩家 20 点、僵尸 20 点；玩家攻击 4 点，生物被击中后有 0.5 秒受击无敌帧并被击退 0.4 格；
  玩家受伤后有 0.5 秒无敌帧；受伤后 8 秒未再受伤则开始每 4 秒恢复 1 点生命。
- 伤害反馈：受伤闪红（Hurt Animation）、Damage Event、挥手动画、实体音效与 Set Health 均已发送；
  生物转向时会广播头部朝向（Head Rotation）；生物死亡播放死亡动画后移除；玩家死亡显示死亡消息，重生后回到出生点并恢复满生命。
- 生物会随世界保存到 `world/entities.json`（位置、生命、朝向，周期自动保存与关闭保存），重启服务器后恢复。
- 服务端会跟踪玩家移动数据包（含坐标合法性、世界边界校验），并用于生物追击与攻击判定。

当前限制：AI 为简化的直线追击/游荡（无寻路；被挡住时仅随机侧移）；生物持久化为 gmcs 自定义
entities.json（非原版实体格式）；无饥饿系统（回血为脱战定时恢复，非原版饥饿驱动）；
无暴击、护甲与掉落物/经验；生物生成不区分昼夜与光照。

## 方块交互（破坏/放置）

- 生存与创造模式可以破坏与放置方块；修改会广播给附近玩家并随区块保存持久化
- 生存模式：客户端完成挖掘后生效，放置消耗物品；创造模式：破坏即时生效，放置不消耗
- 支持创造模式物品栏取放物品；冒险/旁观模式不支持方块交互
- 当前为简化实现：没有掉落物与挖掘时间（见 docs/TODO.md 已知限制）

## 多人

- 其他玩家会以实体出现：加入/退出、移动与朝向实时同步（64 格范围内），皮肤来自正版档案属性
- 可以攻击其他玩家：伤害、受伤动画与生命同步与生物战斗一致；挥空也有挥手动画

## 玩家数据（players.json）

玩家退出（或服务器关闭）时，位置/朝向、生命/饥饿/饱和、游戏模式与物品栏会保存到
`world/players.json`；再次进入时恢复，且不会重复发放初始物品（`starting_items`
只发给新玩家）。物品按命名空间 ID 存储，跨 Minecraft 版本仍可读。

该文件是 gmcs 自定义格式（不是原版的 `playerdata/<uuid>.dat`）；服务器被强杀
（SIGKILL）或崩溃时可能丢失自上次退出以来的进度。

## 正版验证（online_mode）

`online_mode = true` 时启用正版登录：服务器与客户端完成 AES-128/CFB8 加密握手，
并通过 `session_server_url` 的 `hasJoined` 接口验证账号，登录成功后透传玩家属性（如签名皮肤）。
服务器列表与游戏内会标记为强制安全档案（`enforcesSecureChat=true`），玩家列表条目
携带签名皮肤属性，客户端据此加载皮肤。注意：聊天消息仍未实现签名
（无 chat_session_update），客户端会对聊天显示“未验证”标记。
`online_mode = false`（默认）时按离线模式运行，UUID 由用户名按原版规则推导。

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