# TODO

> 当前适配版本：1.21.11（协议 774）

## 已完成

- [x] 项目初始化与 Go 模块骨架
- [x] 配置项与命令行参数
- [x] VarInt 与数据包帧处理
- [x] Minecraft 握手与状态协议解析
- [x] Server List Ping 响应
- [x] ping / pong 往返
- [x] 基础测试与 race 检查
- [x] 玩家登录（Login Start → Set Compression → Login Success → Login Acknowledged）
- [x] Configuration 阶段（Brand、Feature Flags、Known Packs 协商、Registry Data、Update Tags、Finish Configuration）
- [x] 全部 23 个同步注册表同步（完整条目列表，条目 NBT 由 Known Packs 提供）
- [x] 全部所需标签同步（Update Tags：8 个同步注册表 + 6 个静态注册表，静态注册表 ID 来自官方数据生成器报告）
- [x] Play 阶段进入世界流程（Login、Spawn、Game Event、Center Chunk、Chunk Data、Synchronize Player Position、Player Info、System Chat）
- [x] Play 阶段 Keep Alive 与并发写保护
- [x] 区块 1.21.11 序列化（单值调色板、全亮天空光）
- [x] 离线 UUID 与用户名校验
- [x] 真实 1.21.11 客户端实机验证（OptiFine，完成登录 → 配置 → 进入世界 → 渲染区块）

## 待完成

- [ ] 多区块加载与视距控制：当前仅发送出生点区块，视距固定为 10
- [ ] 区块逐格存储与调色板压缩：当前每个 section 只支持单一方块状态
- [ ] 区块 heightmap 数据
- [ ] 玩家移动、物理与位置校验
- [ ] 实体与玩家状态同步
- [ ] 物品、交互与方块更新
- [ ] 命令和权限系统
- [ ] 真实性能 benchmark 与 profiling

## 已知限制

- 注册表数据由 `tools/genregistries` 生成（客户端 jar + 官方 `reports/registries.json`）；
  更换 Minecraft 版本时需要重新生成
- 非同步世界生成注册表（worldgen/structure 等）的标签不发送，依赖客户端本地数据包
- 区块光照固定全亮，且未发送 heightmap
- 地形仅为临时石头平台，无世界生成