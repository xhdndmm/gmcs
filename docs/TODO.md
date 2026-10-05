# TODO

> 当前适配版本：1.21.11（协议 774）

## 已完成

- [x] 项目初始化与 Go 模块骨架
- [x] 配置项与命令行参数
- [x] 配置文件持久化（gmcs.json，不存在时自动生成；缺失字段回退默认值；原子写入）
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
- [x] 区块 1.21.11 序列化（单值调色板与 4–8 位间接调色板、紧密位流打包、全亮天空光）
- [x] 区块逐格存储（16×16×16 section 惰性分配，支持任意方块混排）
- [x] 地形生成（超平坦：基岩 + 两层泥土 + 草方块，与原版超平坦预设一致）
- [x] 地图存储（区域文件：32×32 区块/文件、与原版一致的头部布局、zlib 压缩、临时文件+重命名原子写入、自动保存与关闭保存）
- [x] 物品基础（ItemStack 协议编解码、物品注册表 ID 表、玩家背包 41 槽、快捷栏初始物品下发）
- [x] 聊天（Serverbound Chat 解析、未签名 Player Chat 广播、加入/退出系统消息、玩家列表同步与移除）
- [x] 离线 UUID 与用户名校验
- [x] 客户端命令支持（Declare Commands 命令树；/help、/list、/say、/spawn、/gamemode 服务端执行）
- [x] 正版登录（online_mode：AES-128/CFB8 加密握手、会话服务器 hasJoined 验证、玩家属性透传）
- [x] 种子地形生成（值噪声高度图、水域与沙滩、橡树；同种子确定性生成，出生点取实际地形）
- [x] 游戏模式（配置默认模式、/gamemode 切换、创造/旁观免伤）
- [x] 玩家位置跟踪（移动数据包解析、NaN/越界校验）
- [x] 生物/NPC（僵尸生成与上限、追击/游荡 AI、Entity Position Sync 广播、死亡/远离清理）
- [x] 伤害系统（玩家攻击生物、僵尸攻击玩家、伤害事件/痛动画/音效/生命同步、死亡消息与重生流程）
- [x] 真实 1.21.11 客户端实机验证（OptiFine，完成登录 → 配置 → 进入世界 → 渲染区块）
- [x] CI/CD（GitHub Actions：gofmt/vet/测试/race/交叉编译矩阵；tag 推送构建并发布多平台产物）

## 待完成

- [ ] 多区块加载与视距控制：当前仅发送出生点区块
- [ ] 玩家实体同步（Add Entity/移动广播、皮肤；其他玩家当前只出现在 Tab 列表）
- [ ] 玩家物理（重力/落地/摔落伤害）与移动校验（当前仅记录位置与阻止 NaN/越界）
- [ ] 方块交互（破坏/放置）与方块更新广播
- [ ] 物品交互（丢弃、拾取、容器）与堆叠上限校验
- [ ] 命令补全建议与权限系统（当前仅内置 5 个基础命令，无权限分级）
- [ ] 有签名聊天（chat_session_update）与聊天命令补全
- [ ] 区块 heightmap 数据（客户端光照与渲染优化）
- [ ] 真实性能 benchmark 与 profiling

## 已知限制

- 注册表/方块/物品数据由 `tools/genregistries` 生成（官方客户端 jar + 服务端数据生成器报告）；
  更换 Minecraft 版本时必须重新生成
- 非同步世界生成注册表（worldgen/structure 等）的标签不发送，依赖客户端本地数据包
- 区块负载为 gmcs 自定义格式：区域文件框架（头部、扇区、zlib）与原版一致，
  但负载不是原版 NBT，不能与原版服务器互读
- 区块光照固定全亮（无光照引擎），且未发送 heightmap
- 聊天消息未签名：与原版离线服务器一致，客户端会显示“不安全”标记
- 命令参数不实现原版 Brigadier 语法（引号、目标选择器等），仅支持基础分词
- 正版模式拒绝登录时只发送文本原因（"Failed to verify username!"），不区分具体失败原因
- 正版模式未验证玩家属性签名（透传会话服务器返回的属性）
- 地形不是原版算法（无洞穴、矿物、结构与生物群系差异；海平面 62 为近似值），
  且更换 `world_seed` 不会重新生成已有存档中的区块
- 生物仅僵尸一种，AI 为简化直线追击/随机游荡（无寻路与避障）；
  生物生成不区分昼夜与光照，移动不广播给 48 格以外的玩家；头部朝向不更新
- 伤害数值固定（玩家 4 点、僵尸 2 点），无击退、暴击、护甲与掉落物/经验
- 没有饥饿系统与生命恢复（生命值仅在重生时恢复）
- 初始物品数量固定为 1（数量配置暂不支持）