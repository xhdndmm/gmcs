# 迁移指南

本文说明升级 gmcs、更换 Minecraft 版本，以及与其他服务器互迁时的行为变化与
操作步骤。

> 说明：本项目目前没有发布 tag，本文中的"版本"指 git 提交（commit）。
> 表格中给出的 commit 用于定位历史；`docs/`、`internal/` 中的文件路径均相对于
> 仓库根目录。

## 1. 兼容性一览

| 资产 | 格式 | 升级行为 |
| --- | --- | --- |
| `gmcs.json` | JSON（snake_case 字段） | 缺失字段回退默认值；仅在文件不存在时写回磁盘，运行期间不会重写配置 |
| `world/r.x.z.mca` | 区域文件框架（头部/扇区/zlib）与原版一致；区块负载为 gmcs 自定义格式（magic `GMCS`，version=1） | 负载严格校验魔数与版本；未知格式会拒绝加载 |
| `world/entities.json` | gmcs 自定义 JSON（生物：位置/生命/朝向；掉落物：物品/数量/位置） | 增量引入（`c58bd09`，`items` 字段为后续批次）；文件不存在时视为无实体 |
| `world/players.json` | gmcs 自定义 JSON（位置/生命/死亡状态/游戏模式/物品栏，物品按命名空间 ID） | 增量引入；文件不存在时按新玩家处理（发放初始物品） |
| 游戏协议 | 单一目标版本（当前 1.21.11，协议 774） | 无跨版本兼容层 |

## 2. 升级 gmcs（Minecraft 版本不变）

### 2.1 配置文件

加载逻辑（`internal/config`）以默认值为基准合并文件内容：
**已存在的配置文件中，缺失字段使用默认值，但不会自动写回文件**
（配置只在"首次生成"时写入磁盘）。字段清单与默认值见
[README 的配置表](README.md)。

配置字段演化：

| 引入版本（commit） | 字段变化 |
| --- | --- |
| `2a2a39d` | 初始字段：`listen_address`、`motd`、`version_name`、`protocol_version`、`max_players`、`max_connections`、`view_distance`、`world_dir`、`autosave_seconds`、`starting_items` |
| `ae31626` | 新增 `online_mode`、`session_server_url` |
| `d731b01` | 新增 `world_seed`、`game_mode`、`spawn_monsters`、`max_mobs` |
| `947939b` | 新增 `world_border_size`；开始"首次生成配置文件时分配随机种子" |
| `c58bd09` | 无新字段；`starting_items` 开始支持 `name*count`（数量上限 64） |
| `d49dd70` | 无新字段；新增区块内存卸载（周期性卸载远离玩家的区块） |
| `514acbb` | 无新字段；正版模式完善与玩家数据持久化（新增 `world/players.json`） |
| `7fa03b5` | 无新字段；方块交互与玩家实体同步（玩家数据仍存 players.json） |
| `78e0af5` | 新增 `ops`（管理员玩家名列表，默认空）；摔落伤害与 Tab 补全（无其它配置变化） |
| `a59a859` | 无新字段；区块 heightmap 与玩家数据周期保存/死亡状态持久化（行为变化见 2.4） |
| `3c3913a` | 无新字段；`ops` 项支持 `name:level`（1–4，省略按 4）（行为变化见 2.4） |

两个容易踩的点：

- **`world_seed` 的随机化只发生在"首次生成配置文件"时。** 从旧版本升级时配置
  文件已存在，缺失的 `world_seed` 会按 `0` 处理（一个合法的固定种子），与
  "新装服务器随机种子"不同。想要随机地形：手动填写任意非 0 种子，或删除
  `gmcs.json` 重新生成（注意会同时重置其它字段）。
- **更换 `world_seed` 不会重建已保存区块。** 旧区块保持原样，只有新生成的区块
  使用当前种子的地形（见 2.2 的地形接缝）。
- **列表字段也接受单个字符串。** `ops` 与 `starting_items` 既可以写数组
  （`["Alice"]`），也可以写单个字符串（`"Alice"`），避免写错类型导致启动失败；
  `ops` 项还支持 `name:level` 形式限定权限等级（1–4，省略按 4），见 2.4。

启动时校验失败会给出具体字段的错误，例如：

- `view distance must be between 2 and 32`
- `world border size must be at least 16 blocks`（`world_border_size` 为 0 表示不限制）
- `unknown game mode "..."`（合法值：survival / creative / adventure / spectator）
- `session server URL must not be empty in online mode`

### 2.2 存档（`world/`）

区域文件布局自 `2a2a39d` 引入后未变：1024 个位置表项 + 1024 个时间戳 +
4KB 对齐的区块数据，负载使用 zlib 压缩（压缩类型 2）。在 `2a2a39d` 之前，
服务器不把区块写入磁盘。

区块负载格式：

```text
magic "GMCS" (4B) | version u16（当前为 1） | x i32 | z i32 | 24 × section
section: flags u8（bit0 = 含方块数据）| biome u16 |（可选）4096 × u16 方块状态
```

- 所有多字节整数为大端
- 解码时严格校验魔数与版本；版本不匹配会报错（形如 `不支持的区块负载版本 N`）
- 当前只有 version 1；未来若提升负载版本，会在本指南补充转换步骤

地形生成器变更与"地形接缝"：

| 时间点 | 地形生成器 |
| --- | --- |
| `2a2a39d` – `cd608ea` | 超平坦（基岩 + 泥土 + 草方块） |
| `d731b01` 起（当前） | 种子驱动的值噪声地形（水域、沙滩、橡树） |

保留旧 `world_dir` 升级时，已保存的旧区块不会被重新生成，越过旧边界后地形会
切换，出现"断崖"。处理方式：

| 目标 | 做法 |
| --- | --- |
| 保留旧地形与建筑 | 接受接缝（可手动破坏/放置方块过渡；`7fa03b5` 起支持方块交互） |
| 完整体验新地形 | 换用新的 `world_dir`（旧目录可备份保留） |
| 想要确定性地形 | 显式设置 `world_seed` 并保持不再更改 |

### 2.3 实体数据（`entities.json`）

`c58bd09` 起，生物会随世界保存到 `world/entities.json`；后续批次加入掉落物
（`items` 数组）。文件仅在实体系统可用时读写：

- 服务器关闭与周期（30 秒）时原子写入；启动时读取
- 文件不存在时不做任何事；未知生物类型与损坏条目会被跳过并记录警告
- 生物字段：`type`、`uuid`、`x`、`y`、`z`、`yaw`、`pitch`、`health`（自定义格式，非原版实体 NBT）
- 掉落物字段（`items`）：`item`（命名空间 ID）、`count`、`uuid`、`x`、`y`、`z`；
  恢复时数量截断到 64、拾取延迟重置，速度与存活时长不保存（重启后停在保存位置）
- 恢复时：**实体 ID 重新分配，UUID 保留**（无效 UUID 会重新生成），
  位置/朝向/生命保留；生命值缺失或非法时按僵尸满生命（20）处理
- 该文件在生物系统或掉落物系统任一可用时读写（均为注册表数据完整性检查）。
  `spawn_monsters = false` 或 `max_mobs = 0` 只停止生成新生物，不会停止读写：
  已存在与已恢复的生物仍会活动，并在周期保存与关闭时写回；掉落物系统不受
  `spawn_monsters` 影响（只要注册表完整即启用）

### 2.4 行为变更速查

升级到新提交后可能观察到的行为差异：

| commit | 可观察变化 | 迁移注意 |
| --- | --- | --- |
| `ae31626` | 新增内置命令（`/help`、`/list`、`/say`、`/spawn` 等）；支持正版登录 | 正版登录默认关闭 |
| `d731b01` | 地形从超平坦改为种子噪声；新增游戏模式（默认 survival，创造/旁观免伤）；僵尸会生成并攻击玩家 | 见 2.2 地形接缝；可用 `spawn_monsters` 关闭生物 |
| `947939b` | 可配置世界边界；生物攻击改为 0.5 秒抬手、隔墙不造成伤害；首次生成的配置带随机种子 | 旧配置种子保持 0 |
| `cd608ea` | 进入世界按视距发送周围区块，跨区块移动增量发送与卸载 | 修复此前只发送 (0,0) 造成的虚空 |
| `c58bd09` | 生物持久化、头部朝向广播、脱战回血；**服务器无玩家时不再清理生物**（与旧版本相反）；区块编码提速（协议输出不变） | 重启后生物仍在原位置 |
| `d49dd70` | 远离玩家的区块会周期性（5 秒）卸载（内存占用显著下降），重新接近时从磁盘恢复 | 无行为兼容性影响（脏区块卸载前先写盘） |
| `514acbb` | 正版模式标记 enforcesSecureChat 并下发皮肤属性；玩家数据持久化（players.json，退出与关闭时保存，重连恢复且不重发初始物品）；支持 SIGHUP 优雅退出 | 旧版本无 players.json，升级后玩家从默认状态开始 |
| `7fa03b5` | 方块交互（破坏/放置）与玩家实体同步、玩家间攻击 | 破坏/放置会修改并保存区块；无掉落物与挖掘时间 |
| `78e0af5` | 玩家摔落伤害（下落 >3 格落地掉血）；命令权限（`ops` 名单）与 Tab 补全 | 非名单玩家无法再使用 `/say`、`/gamemode`；新增配置字段 `ops` |
| `a59a859` | 区块包开始携带 heightmap（WORLD_SURFACE/MOTION_BLOCKING）；玩家数据每 30 秒自动保存；死亡状态持久化 | 死亡状态下线的玩家重连后保持死亡（需发送重生请求），不再自动满血 |
| `3c3913a` | 掉落物（丢弃/拾取/死亡掉落/entities.json 持久化）；区块突发携带 Chunk Batch Start/Finished；命令权限等级与 `/kick`；摔落保护方块（干草堆/床/粘液/蜂蜜/细雪） | 丢弃与拾取仅生存/创意；破坏方块仍无掉落物；`ops` 可写 `name:level`，`/say`、`/gamemode` 需 2 级，`/kick` 需 3 级 |

## 3. 更换 / 升级 Minecraft 版本（开发向）

调整目标 Minecraft 版本时按顺序执行：

1. **准备数据源**：目标版本的官方客户端 jar 与服务端 jar。
2. **生成数据报告**（需要 Java）：

   ```bash
   java -DbundlerMainClass=net.minecraft.data.Main -jar server.jar --reports
   ```

3. **重新生成注册表数据**：

   ```bash
   go run ./tools/genregistries \
     -jar <client.jar> \
     -reports generated/reports/registries.json \
     -blocks generated/reports/blocks.json
   ```

   会更新 `internal/registry/` 下的 `data_generated.go`、`blocks_generated.go`、
   `items_generated.go`、`static_ids_generated.go`。**不要手改生成文件**，
   需要调整时修改 `tools/genregistries`。

4. **更新协议常量与版本信息**：
   - `internal/config`：默认 `protocol_version`（当前 774）与 `version_name`、`motd`
   - `internal/protocol`：数据包 ID 常量（如 `PlayPacketIDChunkData`）、
     Configuration 阶段的协商内容（Known Packs、Registry Data 等）
   - 依赖生成数据的 ID（方块状态、物品、实体类型、声音、伤害类型等）无需手改

5. **核对版本差异**：对照官方协议文档与第三方实现（ViaVersion、
   node-minecraft-protocol、minecraft-data 等）逐项核对本仓库使用的包与字段。
   本仓库已知的历史变更点示例：
   - 1.21.2 起实体速度与位置使用 LpVec3 打包（`internal/protocol`）
   - 1.21.5 起调色板数据数组不再带长度前缀（`internal/world/chunk.go` 注释）
   - 之后版本可能新增字段（如 26.1 的 section fluid count）——gmcs 当前按
     1.21.11 处理，升级时需逐项确认
   - 版本差异应集中实现（版本特定实现/能力抽象），不要散落版本判断

6. **验证**：
   - `scripts/test.sh` 全绿（含 race）
   - 数据相关测试：`internal/registry/lookup_test.go`、`internal/world` 编码测试
   - 用目标版本客户端实机联机：登录 → 配置 → 进入世界 → 区块渲染 → 基础交互

7. **更新文档**：
   - README（适配版本、协议号等）、TODO 头部"当前适配版本"
   - 本指南第 1、2 节；如需重测基准，更新 [PERFORMANCE.md](PERFORMANCE.md) 的环境说明

## 4. 与原版服务器互迁

**双向互读均不支持**：

- 原版 → gmcs：gmcs 的区块负载是自定义格式（非原版 NBT），区域文件仅框架
  （头部、扇区、zlib 压缩类型）与原版一致。把原版 `.mca` 放进 gmcs 的
  `world_dir` 会在解码负载时报错（魔数/版本校验不通过）。
- gmcs → 原版：同样原因，原版服务器无法读取 gmcs 写入的区块负载。

可行的替代方案：在 gmcs 中使用新的 `world_dir` 生成近似地形（仅地形，
无原版建筑与结构）。若要真正互读，需要实现原版区块 NBT 的编解码
（当前未实现，也不在本指南的承诺范围内）。

## 5. 常见问题

- **升级后出生点或地形不同？** 出生点取实际地形；地形生成器变更后，所有新
  生成的区域都会变化，见 2.2。
- **改了 `world_seed` 但旧区块没变？** 预期行为：已保存区块不会重建。
- **重启后生物位置和生命保留了，但实体 ID 变了？** 恢复时重新分配实体 ID、
  保留 UUID；位置/朝向/生命保留。
- **重启后玩家位置/物品会保留吗？** 会：玩家退出、服务器关闭与每 30 秒周期
  保存到 `world/players.json`（含死亡状态）。进程被强杀（SIGKILL）或崩溃最坏
  丢失约一个保存周期的进度。
- **破坏/放置的方块会被保存吗？** 会：修改会标记区块待保存，随后续自动保存、
  区块卸载与服务器关闭写盘（区块负载仍是 gmcs 自定义格式）。
- **服务器没有玩家时生物会消失吗？** 不会（`c58bd09` 起）。只有在存在玩家时
  才会按距离清理（>64 格）；更早版本会把所有生物清空。
- **可以用 MCEdit 等地图工具打开 gmcs 存档吗？** 工具通常能识别区域文件框架，
  但方块数据不是 NBT，无法读取。
- **启动日志出现"生物系统已禁用：注册表数据缺失"？** `internal/registry`
  生成数据缺失或损坏，重新执行第 3 节的生成步骤。

## 6. 报告迁移问题

遇到迁移相关问题时，请附上：gmcs 版本（commit）、操作系统与 Go 版本、
配置文件中与问题相关的字段、复现步骤与日志。

提交入口：<https://github.com/xhdndmm/gmcs/issues>。
