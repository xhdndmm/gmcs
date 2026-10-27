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

- [x] 多区块加载与视距控制（进入世界发送视距内区块；跨区块移动增量发送与卸载，Set Center Chunk）

- [x] 种子地形生成（值噪声高度图、水域与沙滩、橡树；同种子确定性生成，出生点取实际地形）

- [x] 首次运行时随机生成世界种子并写入配置文件（之后固定）

- [x] 世界边界（world_border_size：初始化边界包、越界移动回正、生物不越界生成/移动）

- [x] 游戏模式（配置默认模式、/gamemode 切换、创造/旁观免伤）

- [x] 玩家位置跟踪（移动数据包解析、NaN/越界校验）

- [x] 生物/NPC（僵尸生成与上限、追击/游荡 AI、Entity Position Sync 广播、死亡/远离清理）

- [x] 伤害系统（玩家攻击生物、僵尸攻击玩家、伤害事件/痛动画/音效/生命同步、死亡消息与重生流程）

- [x] 战斗手感完善（攻击抬手 0.5 秒与挥手动画、受伤动画、视线遮挡判定、受击无敌帧、击退）

- [x] 真实 1.21.11 客户端实机验证（OptiFine，完成登录 → 配置 → 进入世界 → 渲染区块）

- [x] 生物持久化（世界目录 entities.json：位置/生命/朝向；周期自动保存与关闭保存，重启恢复）

- [x] 生物行为完善（转向时广播头部朝向、被挡住时随机侧移避障、移动仅广播给一定距离内的玩家）

- [x] 初始物品数量（`starting_items` 支持 `name*count`，上限 64）

- [x] 关键路径 benchmark（区块生成/编码、实体包、服务器 Tick；见 docs/PERFORMANCE.md）

- [x] 区块编码热路径优化（调色板线性查找、消除中间数组与缓冲区重复增长；实测约 12.8× 提速）

- [x] 区块内存卸载（远离所有玩家的区块移出内存缓存；脏区块卸载前先写盘，重新接近时从磁盘恢复）

- [x] 玩家数据持久化（players.json：位置/生命/饥饿/经验/游戏模式/物品栏；退出/关闭与
  周期（30 秒）保存，重连恢复；死亡状态持久化，重连后保持死亡需重生）

- [x] 正版模式完善（服务器列表 enforcesSecureChat、玩家列表携带皮肤属性；快速重连不再误删新会话）

- [x] 玩家实体同步（Add Entity/移动与朝向广播/退出移除；攻击其他玩家与挥手动画）

- [x] 方块交互（生存/创意破坏与放置、Block Update 广播、创造物品栏、修改随区块保存持久化）

- [x] 贡献指南与迁移指南（docs/CONTRIBUTING.md、docs/MIGRATION.md）

- [x] CI/CD（GitHub Actions：gofmt/vet/测试/race/交叉编译矩阵；tag 推送构建并发布多平台产物）

- [x] 完整挖掘系统（对齐原版 ServerPlayerGameMode 1.21.11 字节码）：
  1104 个方块硬度表（含 requiresCorrectToolForDrops）与 38 种工具的
  minecraft:tool 规则（正确工具除数 30 / 非正确 100）；挖掘进度（0.7 阈值
  完成、延迟破坏、瞬时破坏）、Block Destruction 进度动画、Block Changed Ack
  （下一 tick 回显 sequence）、方块交互距离按原版 4.5/5.0 + 1.0 容差
  （眼睛到碰撞盒）、悬空挖掘 ÷5；掉落仅在“正确工具”满足时生成；
  工具耐久按 damage_per_block 损耗（损坏时移除并广播 Entity Event 0）；
  创造模式：剑/三叉戟/重锤不可破坏（can_destroy_blocks_in_creative）、
  命令方块/结构方块等 GameMasterBlock 不可破坏（无权限系统按非管理员处理）、
  可破坏基岩（与原版一致）；F 键主副手交换（状态 6）；
  破坏粒子/音效（LevelEvent 2001）；
  实测对照（官方服务端探针）：Block Changed Ack 回显 sequence（仅
  START/ABORT/STOP，同一 tick 多个动作取最后序号、下一 tick 下发）、
  无 STOP 不破坏、新目标 START 时对旧跟踪位置回传 Block Update 纠正；
  掉落状态编号 3=DROP_ALL_ITEMS / 4=DROP_ITEM 由 1.21.11 包枚举顺序
  （readEnum 即序号）与 ServerPlayer.drop(boolean) 分支字节码确定；
  探针为伪客户端（官方服务端因未完成区块跟踪不下发区块包、setblock 报
  "Could not set the block"），挖掘时序/破坏动画/掉落生成的原版端到端
  对照未完成，待真实客户端复测

- [x] 玩家摔落伤害（着地标志跟踪、下落 >3 格落地掉血、落水免疫、`minecraft:fall` 伤害类型）

- [x] 命令权限系统（配置 `ops` 名单；管理命令按玩家过滤命令树并在执行时再次校验）

- [x] 命令补全建议（Command Suggestion Request/Response；命令名与 `/gamemode` 参数，按权限过滤）

- [x] 构建产物优化（PGO：`cmd/gmcs/default.pgo` + `scripts/genpgo.sh`；链接参数 `-buildid=`；实测与体积数据见 docs/PERFORMANCE.md）

- [x] 内存与热路径优化（压缩对象池、区块发送缓冲复用、列高度缓存；实测见 docs/PERFORMANCE.md）

- [x] 区块 heightmap（Chunk Data 发送 WORLD_SURFACE/MOTION_BLOCKING，9 位/列、37 long，随方块修改即时生效）

- [x] 掉落物与拾取（Q/Ctrl+Q 丢弃与视线抛掷、重力/摩擦物理、拾取合并与动画音效、死亡掉落、到期/虚空消失、entities.json 持久化）

- [x] 区块批量协议（进入世界与跨区块发送以 Chunk Batch Start/Finished 包裹；接收并记录客户端 Chunk Batch Received 速率）

- [x] 命令权限等级与 `/kick`（`ops` 支持 `name` 或 `name:level`（1–4）；`/say`、`/gamemode` 需 2 级，`/kick` 需 3 级；`/kick` 支持玩家名补全）

- [x] 摔落保护方块（干草堆 ×0.2、床 ×0.5、粘液/蜂蜜/细雪免疫；落水免疫已有）

- [x] 方块破坏掉落（简化掉落表：石头→圆石、草方块/土径/耕地→泥土、矿石→原矿/宝石、
  黏土→4 黏土球等；玻璃/树叶/冰/水/基岩无掉落；创造模式不掉落；
  掉落按原版“正确工具”门控（见下“完整挖掘系统”）；无精准采集/时运/概率掉落）

- [x] 单包位移校验（超过 100 格/包的位置包被拒绝并回拉；已有 NaN/越界 Y/世界边界校验）

- [x] pprof 诊断监听（配置 `pprof_address`，默认关闭；`/debug/pprof/*` 提供 CPU/内存分析）

- [x] 负载压测工具（`cmd/gmcsload`：离线模式模拟并发客户端，输出进入世界耗时、
  收包量、流量与包类型分布；单机 50 玩家实测见 docs/PERFORMANCE.md 3.7）

- [x] 区块紧凑存储（section 调色板 + 位流；每区块内存 ≈72 KiB → ≈9 KiB，
  1600 区块 HeapAlloc ≈111.9 MiB → ≈12.3 MiB；生成/编码同步提速，实测见
  docs/PERFORMANCE.md 3.8）

- [x] 区块存储管线优化（按槽位读取不整区解压、保存时未更新槽位按原始压缩数据复制、
  压缩器对象池、精确大小写入；保存编码+压缩分配 ≈1.39 MB/区块 → ≈129 KB/区块）

- [x] 容器交互与方块实体（箱子/陷阱箱/木桶/发射器/投掷器/漏斗/潜影盒 17 色；
  打开/关闭窗口、取放/Shift 快速移动/数字键交换/创造复制/Q 丢弃/拖拽分发/双击收集、
  玩家物品栏窗口 0 的非合成槽位、内容随区块持久化、破坏掉落、开合动画与距离校验；
  方块实体随区块负载 v2 存储，向后兼容读取 v1）

- [x] 服务器端移动物理（逐 tick 速度上限 10×√tick、穿墙检测、服务器端地面检测驱动的
  摔落伤害；不再依赖客户端的着地标志）

- [x] 掉落物物理完善（逐轴 AABB 碰撞、周期合并（每 4 tick、0.5 格内、不隔墙）、
  按来源区分拾取延迟（挖掘 10 tick / 玩家与死亡 40 tick）、岩浆/火/仙人掌销毁、
  水中漂浮、未加载区块冻结）

- [x] 命令级节点权限（`permissions` 按玩家授予节点，支持 `gmcs.command.*` 与 `*` 通配、
  `"*"` 玩家条目；等级或节点任一满足；命令树/补全/执行统一校验）与聊天玩家名补全

- [x] 聊天消息签名（消息链跟踪、RSA-SHA256 签名验证、消息确认窗口（offset/位图/校验和）
  与签名转发；失败按原版组件断开连接）与聊天会话分发（解析并保存 `chat_session_update` 公钥、
  通过 Player Info 的 Initialize Chat 动作分发给其他玩家；重复上报忽略、公钥降级断开）

- [x] 饥饿系统（疲劳度驱动：疾跑/跳跃/攻击/受伤；饥饿 ≥18 自然恢复、饥饿 0 饥饿伤害；
  进食支持 44 种食物，数值来自官方物品数据）与经验（击杀获得、随玩家数据持久化、死亡清空）

- [x] 昼夜循环与夜晚刷怪（24000 tick/天、Update Time 同步；敌对生物只在夜晚生成）

- [x] 生物掉落与跳跃暴击（僵尸掉落腐肉与稀有物品；下落攻击 ×1.5 伤害）

- [x] 按物品数据的堆叠上限（官方 `minecraft:max_stack_size`，1505 项；背包/容器/创造/
  掉落物统一按实际上限处理）

- [x] 移动广播零分配（实体包编码精确预分配、Append 变体 + sync.Pool 缓冲复用；
  EncodeEntityPositionSync 0 分配、ServerTick 分配 −92%，实测见 docs/PERFORMANCE.md 3.9）

- [x] 合成系统（玩家物品栏 2×2 与工作台 3×3：有序/无序/标签配方匹配（706 有序、304 无序、
  116 烹饪，由 `tools/genregistries` 从官方 recipe 数据生成）、结果槽实时计算、
  取走消耗原料、Shift 快速合成、关闭窗口合成格归还）

- [x] 熔炉家族（熔炉/高炉/烟熏炉：熔炼/燃烧双进度、燃料表（wiki 数值）、
  高炉烟熏炉双倍速、熔炼经验、Set Container Property 进度条广播、
  燃料槽限制（只接受燃料，空桶例外）与湿海绵特判（空桶→水桶）、
  燃烧/烹饪进度随区块负载 v3 持久化（修复 v2 方块实体 Y 坐标截断缺陷））

- [x] 末影箱（按玩家独立存储 27 槽、窗口取放/快速移动、随 players.json 的
  `ender_inventory` 字段持久化）

- [x] Shift 快速移动对齐原版（对照 1.21.11 服务端字节码逐条移植 `quickMoveStack`：
  每个窗口的分支顺序与方向、合并后仅放入一个空槽、“命中即不回退”；
  熔炉燃料/可熔炼、酿造原料/火药/瓶子、切石机输入、附魔台单件搬运、
  铁砧/砂轮/锻造台输入判定全部按原版；酿造台药水槽堆叠上限 1；
  修复熔炉结果槽点击未走结果槽处理的缺陷）

- [x] 拖拽分发按原版实现（对照 1.21.11 服务端字节码逐条移植，并以官方服务端实测校验：
  逐槽校验只把可接受的槽位加入集合、集合去重、非创造拖拽要求“携带数量 > 已收集槽位数”、
  结束阶段左键 `floor(数量/槽位数)` 均分与右键每槽 1 个（中键创造整栈）、
  单槽拖拽回退为普通点击、拖拽中收到的其他点击被取消并丢弃）

- [x] 玩家装备槽对齐原版（从 `reports/items.json` 的 `minecraft:equippable` 组件提取
  可由玩家装备的物品 → 部位，共 39 项：头盔类 16（含头颅/南瓜）、胸甲 8（含鞘翅）、
  护腿 7、靴子 7、盾牌 1；马铠/狼铠/挽具/鞍因 allowed_entities 不含玩家而排除）：
  盔甲槽 `Slot.mayPlace` 与槽位上限 1（可堆叠装备也只能放 1 个）、整栈点到空装备槽
  只放入上限数量、不同物品数量不超上限才允许交换、右键不同物品按原版整栈交换、
  Shift 点击自动装备（对应装备槽为空时只尝试那一个槽位，否则回退主背包 ↔ 快捷栏）；
  均为字节码 + 官方服务端实测双重校验

- [x] 玩家实体元数据（皮肤层：Avatar 索引 16 Set Entity Data，随实体生成下发、
  Client Information 变更时广播）

- [x] 方块碰撞形状（collision shapes）：`tools/genregistries` 生成全部方块状态的碰撞盒表
  （1/16 单位；常量取自原版方块类字节码：半砖/台阶/栅栏/墙/栅栏门/门/活板门/雪层/
  仙人掌/蛋糕/耕地/床/箱子/压力板/营火/切石机/酿造台/讲台/灯笼/铃等）；`internal/world`
  提供共享 AABB 查询（Collides、逐轴扫描 ClipMove、支撑面 SurfaceBelow），
  玩家防穿墙/站立/摔落判定与掉落物物理统一走形状级碰撞

- [x] 移动校验升级：潜行边缘保护（无支撑的目标沿边缘滑动或保持原位）与
  悬空上升限制（防飞行：一次腾空累计上升 ≤1.3 格，覆盖原版跳跃弧顶 1.2522 格；
  落地/入水清零；被回拉不清零，防止“分段上升”绕过）

- [x] 多玩家共享与性能（50 玩家实测：峰值内存 −26%、常驻 −38%、CPU −68%）：
  区块/注册表数据包压缩一次后跨连接共享（2048 项 LRU 区块帧缓存，按修改计数
  失效）、编码缓冲池化、保存压缩移出世界锁、区域文件句柄缓存

- [x] 网络安全加固：会话级令牌桶限流（聊天/命令、移动、交互），防止包洪泛
  放大广播成本（长度/包大小限制已有）

- [x] 地形多样性与生物群系：大陆度/山脊/丘陵/细节噪声高度图（深海—平原—山地）、
  温度/湿度噪声划分 12 种群系（雪原/针叶林/平原/森林/沼泽/沙漠/稀树草原/丛林/海洋/
  深海/沙滩）、群系化地表与植被（雪层/橡树/云杉/金合欢/仙人掌/草丛）、4×4 格群系网格
  随区块数据下发（单值或 1–3 位调色板）；出生点在世界边界内螺旋搜索陆地

- [x] 放置方块推起实体（原版 Block.pushEntitiesUp 行为）：在实体脚下放置方块时把
  玩家/生物/掉落物推到方块顶面并同步位置，修复“脚下放方块卡进方块被反复回拉”

- [x] 修复 ChunkLoaded 坐标语义（入参误当区块坐标）：掉落物在非原点区块被误判
  “未加载”而冻结物理；同时修复摔落结算/测试辅助的地形假设

- [x] 区块生成完善：24 种群系（雪原/针叶/桦木/深色橡木/沼泽/沙漠/恶地/稀树/丛林/
  草甸/雪坡/冻峰/石峰/河流/沙滩/石岸/雪滩/海洋/深海等）、河流噪声雕刻、深板岩
  （y<0）、3D 噪声大型洞穴（cheese caves）与随机游走隧道（worm，保留地表密封与
  基岩）、8 种矿物随机游走成脉（含深板岩变种）、新增植被（桦木/深色橡木/丛林/
  金合欢/仙人掌/甘蔗/向日葵/莲蓬等）；生成器 benchmark 458µs/区块（见 PERFORMANCE.md）

- [x] 多维度世界：下界（基岩地板/天花板、净岩、岩浆海、石英/远古残骸、萤石/灵魂沙/
  玄武岩装饰、3D 洞穴）与末地（主岛/黑曜石柱/外岛/虚空）生成器；按维度独立区块
  高度与 section 范围、按需创建与持久化（`world/the_nether/`、`world/the_end/`）、
  玩家维度持久化与重连恢复、`/dimension` 切换（Respawn 流程经实机协议验证）

- [x] 生物扩展：骷髅（保持距离射箭：箭矢实体 + 重力/碰撞/命中判定）、苦力怕
  （引信点燃与爆炸：球形销毁方块、伤害与击退）、蜘蛛；按权重生成与掉落表
  （骨头/箭/火药/线/蜘蛛眼）；生物数据按维度分文件持久化

- [x] 红石系统：拉杆/按钮（石 20 tick、木 30 tick 自动弹起）/红石块/红石火把电源，
  红石线 BFS 衰减（15−距离、多电源取最大）与连接方向渲染，红石灯联动，
  放置/破坏/切换后重算并仅广播变化（含 `TestRedstone*` 测试）

- [x] 命令扩展：`/tp`（坐标或玩家，含跨维度）、`/give`、`/time set|add|query`、
  `/xp add`、`/clear`、`/kill`、`/seed`、`/save-all`、`/dimension`；补全与会话审计

- [x] 日志与审计：`internal/logging`（按大小轮转的文件输出、TeeHandler、JSONL 审计）、
  配置 `log_file`/`log_level`/`log_max_size_mb`/`audit_log_file`（含单元测试）

- [x] 异步区块预生成：进入世界/跨区块发送前由 8 个 worker 并行生成并去重（请求 future），
  主流程只等待结果；预生成与同步生成结果一致性测试

- [x] 长时多玩家压测（100 玩家 5 分钟 loopback，结果见 docs/PERFORMANCE.md 3.7）

- [x] 修复区块包调色板容器线上布局错误：5–7 位网络的方块状态数据数组改为与客户端
  BitStorage 一致的“每 long 独立”打包（长度 = ceil(4096/(64/bits))）；此前按连续位流
  打包会少发 long，客户端解析到包尾越界并断线（IndexOutOfBoundsException）

- [x] 修复悬空上升限制截断原版跳跃：预算 1.2 → 1.3（原版跳跃弧顶约 1.2522），
  正常跳跃不再被回拉

- [x] 修复创造模式物品栏槽位错乱：Set Creative Mode Slot 线上使用玩家菜单槽位号
  （5–8 盔甲、9–35 主背包、36–44 快捷栏、45 副手，经官方 mappings + 反汇编字节码确认），
  服务端改为映射到物品栏内部槽位后再存储与回执；负数槽位与原版一致地掉落到世界。
  修复前创造模式把物品放到快捷栏会存进盔甲/副手槽（并回执错误槽位），
  导致客户端物品错乱、丢弃/拾取结果不符

- [x] 修复区块卸载未驱逐区块帧缓存：帧缓存条目强引用 *world.Chunk，阻止已卸载区块
  被 GC（长时间跑图内存只增不减）；UnloadFar 现返回卸载位置，卸载时同步驱逐缓存条目

- [x] 空闲服务器释放资源：最后一名玩家退出时唤醒卸载循环，立即卸载全部维度的
  全部区块（脏区块先写盘）、驱逐区块帧缓存，并回收区域文件句柄缓存
  （`UnloadAll` / `ReleaseRegionHandles`；含 TestIdleServerReleasesChunks 等测试）

- [x] 传送门：下界传送门（黑曜石框 2×3–21×21 + 打火石点燃、1:8 坐标缩放、到达侧
  自动建造出口、回程与冷却）、末地传送门（12 个框架 + 末影之眼激活、第一次进入生成
  出口平台、主世界出生点返回）；破坏框架清理附近传送门方块（含 `TestPortal*` 测试）

- [x] 红石扩展：中继器（朝向单向、右键 1–4 档延迟、每档 2 tick）与侦测器（面向方块
  变化输出 2 tick 脉冲），参与网络重算（含 `TestRepeater*`/`TestObserver*` 测试）

- [x] 红石完善（对照 1.21.11 官方 mappings 反编译的组件类移植）：
  比较器（比较/减法模式、右键切换、容器填充度读取、侧输入压制、后方容器/穿过
  导体方块的读取）、活塞与粘性活塞（结构解算器 12 格限制、粘性链与分支、
  DESTROY/BLOCK/PUSH_ONLY 推反应、不可推动方块、活塞头与基座联动、准连接 QC、
  实体推动）、方块强充能（`:genregistries` 从反编译数据生成推反应/非导体表；
  中继器/比较器/拉杆/火把的 direct signal 经导体方块传导）、红石线连接方向按原版
  `getConnectionState` 计算（含直线贯通规则、上下跨格连接、向机构方块的指向规则）、
  火把熔断（60 tick 内 8 次切换 → 160 tick）、中继器侧向锁定、红石灯 4 tick
  熄灭延迟
  （含 `TestPiston*`/`TestSticky*`/`TestComparator*`/`TestTorch*`/`TestWire*` 测试）

- [x] 生物扩展：牛/猪/羊（被动、白天地表草地生成、掉落）与末影人（被注视/受击仇恨、
  随机瞬移、末地生成、掉落末影珍珠）

- [x] 进食过程：长按 1.6 秒完成进食，切换槽位/松开使用键中断（含 `TestEating*` 测试）

- [x] 爆炸实体交互：对范围内生物按距离线性衰减结算伤害（致死走死亡动画与掉落），
  并击飞范围内的掉落物（`TestExplosionDamagesMobsAndItems`）

- [x] 生物 A* 寻路：地表 4 邻接网格（绕障、上跳 1 格、下落 ≤3 格、
  512 节点/48 格搜索上限），路径每 10 tick 重算，被完全堵住时回退随机侧移；
  生物可走下最多 3 格落差（含 `TestZombiePathsAroundWall`/`TestMobFallsOffLedge`）

- [x] 玩家环境效果：接触岩浆 4 点/0.5 秒并着火 15 秒（每秒 1 点火焰伤害、入水熄灭）、
  溺水（头部浸水 15 秒后每秒 2 点）、岩浆块每 0.5 秒 1 点、着火/氧气状态同步
  （`TestLava*`/`TestFireDamage*`/`TestDrowning*`/`TestMagma*`）

- [x] 水中游泳与台阶：入水时悬空上升预算清零（可持续上游），
  走上半砖/台阶（≤1 格高差）被正确接受（`TestSwimUpwardInWater`/`TestStepUpOntoSlab`）

- [x] 压力板红石（对照 1.21.11 反编译的 BasePressurePlateBlock/PressurePlateBlock/
  WeightedPressurePlateBlock）：全部 16 种压力板（12 木质 + 石质/抛光黑石 + 金/铁加权）；
  检测盒 (1/16–15/16, 0–4/16, 1/16–15/16)；木质检测全部实体、石质仅生物、
  加权检测全部实体；加权信号 = ceil(min(数量, 上限) × 15 / 上限)（金 15/铁 150），
  松开延迟 20/10 tick；实体进入立即重算、计划刻续期、按下/弹起音效
  （`TestWoodenPressurePlate*`/`TestStonePressurePlate*`/`TestWeightedPressurePlate*`/
  `TestPressurePlateStateTable`）

- [x] 经验球实体（对照反编译的 ExperienceOrb）：按原版拆分表（2477/…/3/1）生成，
  生成时并入同组（ID % 40）同值经验球；重力 0.03/阻力 0.98、水中上浮、岩浆随机跳、
  落地弹起；8 格内追踪玩家（(1−√距离/8)² × 0.1）、每 20 tick 合并 0.5 格内同值经验球；
  拾取后发放经验并广播拾取动画（2 tick 冷却、合并计数）；击杀生物与玩家死亡
  （min(等级×7, 100)）掉落（`TestXPOrb*`/`TestPlayerDeathDropsExperienceOrbs`/
  `TestMobKillRewardsExperience`）

- [x] 实体进入方块与方块读取热路径：`Entity.checkInsideBlocks` 等价扫描
  （边界 1e-7 收缩）+ 状态数组查表过滤；世界层新增区块读锁快路径与按列批量读取
  （`world.LoadedColumn`/`Chunk.CopyColumn`，区块只解析/加锁一次）；压力板扫描
  46 ns/实体（`BenchmarkEntityInsideBlocks`，见 PERFORMANCE.md 3.15）

- [x] 配方体系扩展：`tools/genregistries` 新增 stonecutting（254 条）与
  smithing_transform（12 条下界合金升级）配方生成；切石机与锻造台窗口
  （打开/取放/Shift 快速移动/数字键/拖拽、结果实时计算、取走消耗输入、
  关闭归还输入；切石机配方选择走 Container Button Click，含 `TestStonecutter*`/`TestSmithing*`）

- [x] 配方书 UI：Update Recipes（1138 条配方 + 254 条切石机）、
  Recipe Book Add（全部解锁，含合成/烹饪/锻造显示条目）、Recipe Book Settings，
  处理 Place Recipe（把原料放入当前合成格，`makeAll` 循环合成；
  含 `TestRecipeBook*`）

- [x] 漏斗物品传输（对照 1.21.11 反编译的 `HopperBlockEntity`/`Hopper` 接口）：
  每 tick 推出/吸入 1 个（最左优先；两者互不排斥，结果取或）、成功搬运后
  冷却 8 tick（`MOVE_ITEM_SPEED`）、插入前完全为空的目标漏斗按 `custom cooldown`
  设置错峰冷却（目标最近搬运时刻不早于本漏斗时 7，否则 8）；
  推出的面规则与槽位过滤（`getSlotsForFace`/`canPlaceItem`：箱子类全槽、
  熔炉上方→原料槽/侧面→燃料槽/产物槽不可插入且燃料槽不抽走桶装物）；
  吸入盒 (0, 11/16, 0)–(1, 2, 1)（`SUCK_AABB`）内的掉落物整堆收集，
  上方有容器时按原版优先抽取（含蜂巢/蜂箱 `does_not_block_hoppers` 例外：
  非完整碰撞形状的方块不阻挡吸入）；
  红石锁定（`enabled=false` 不推不吸，仍可被输入/抽取）；
  放置朝向按点击面反方向（竖直退化为 DOWN）；内容随区块持久化、破坏掉落、
  与打开的窗口共享同一份槽位状态（含事务化搬运与窗口点击的并发保护）；
  注册表新增漏斗方块状态表（`enabled`/`facing`）；
  区块加载时增量注册（`world.TakeLoadedChunks` + 单区块扫描）、
  卸载时丢弃注册、每 600 tick 全量扫描兜底
  （`TestHopper*`/`TestChunkLoadTracking`/`TestChunkBlockStatePositions`）

- [x] 酿造台（对照 1.21.11 反编译的 `BrewingStandBlockEntity`/`PotionBrewing` 逐条移植）：
  5 槽窗口（3 瓶子 + 材料 + 燃料）、400 tick 酿造周期、烈焰粉 1 个 = 20 次；
  63 条药水配方 + 2 条容器配方（水瓶→粗制→效果→延长/强化/腐化；火药→喷溅、
  龙息→滞留）为字节码对照的手写表（`registry/brewing.go`）；完成时广播
  World Event 1035 音效/粒子；`has_bottle_*` 方块状态联动；瓶子/材料/燃料槽
  放置规则（窗口 + 漏斗统一）；药水以 `potion_contents` 组件随 Slot 下发
  （客户端显示颜色与名称），药水 ID 随区块存储 v5 与玩家数据持久化；
  槽位规则：瓶子槽只接受药水类/玻璃瓶且必须为空、材料槽只接受酿造材料、
  燃料槽只接受烈焰粉；漏斗面规则与抽取过滤同原版
  （含 `TestBrewing*`/`TestHopperIntoBrewingStand` 测试）

- [x] 铁砧（对照 1.21.11 反编译的 `AnvilMenu.createResult`/`onTake` 逐字节码移植）：
  材料修复（每材料修复 min(当前损坏, 耐久上限/4)、成本 +1/个）、同物品合并
  （剩余耐久 + 12% 上限奖励、成本 +2）、附魔合并（同名附魔取高等级/同级 +1
  并钳制最大等级；supported_items 筛选 + 创造/附魔书豁免；冲突附魔每项 +1 成本
  且不生效、全部冲突整单无效；anvil_cost × 等级、附魔书减半、堆叠 >1 成本 40）、
  重命名（custom_name、成本 +1；与默认
  悬停名相同则不设组件；空白输入清除名字）；成本 = 两输入 repair_cost 之和 + 操作成本，
  40 级上限（纯重命名封顶 39）、取走扣等级（创造豁免）/消耗材料/清空输入、
  12% 损坏降级（事件 1029/1030）；damage/custom_name/repair_cost 与附魔
  （物品 enchantments 13、附魔书 stored_enchantments 41）组件随
  Slot 编码与存储 v6/玩家数据持久化；放置朝向按原版（视线顺时针）；
  （含 `TestComputeAnvilResult*`/`TestAnvilMergeEnchantments`/
  `TestComputeAnvilResultEnchantmentMerge`/`TestAnvilWindowRenameAndTake`/
  `TestAnvilWindowEnchantMerge`/`TestAnvilCloseReturnsInputs`）

- [x] 附魔台（对照 1.21.11 反编译的 `EnchantmentMenu`/`EnchantmentTableBlock` 逐字节码移植）：
  输入槽（至多 1 个）+ 青金石槽 + 三个随机选项；书架计数（32 偏移、书架 +
  中点 #replaceable，上限 15）；随机序列与 java.util.Random 逐位一致（JDK
  参考值验证）；附魔选取复刻 EnchantmentHelper（等级修正 ±15%、加权随机、
  互斥过滤、等级减半循环、书特例）；候选筛选按 supported_items 且
  primary_items 命中（书不受限制）；普通书 → 附魔书；附魔组件
  （物品 enchantments 13、附魔书 stored_enchantments 41）随槽位与存储 v7/玩家数据持久化；扣 id+1 级 + 重播附魔种子；
  数据槽 10 个（成本/线索/种子）；音效 block.enchantment_table.use；
  附魔注册表（43 条）与物品池由 genregistries 生成（ID = 文件路径字典序，
  与外部数据源交叉验证）；（含 `TestJavaRandomMatchesJava`/
  `TestEnchantmentCostFormula`/`TestEnchantingTablePrimaryItems`/
  `TestEnchantingWindowFlow`/`TestEnchantingBookshelfCount`/
  `TestEnchantingBookBecomesEnchantedBook` 测试）

- [x] 砂轮机（对照 1.21.11 反编译的 `GrindstoneMenu` 逐字节码移植）：
  输入槽 2 个 + 结果槽（39 槽）；输入只接受可损坏/带附魔物品；同物品合并
  （剩余耐久 + 5% 上限奖励、上限取两者较大值）；祛魔保留诅咒、重置 repair_cost、
  附魔书变为普通书；不可损坏的堆叠物品要求组件一致（结果 2 个）；
  取走发放经验球（`ceil(total/2) + random(0, ceil/2)`）与世界事件 1042；
  （含 `TestGrindstoneRemoveNonCurses`/`TestGrindstoneComputeResult`/
  `TestGrindstoneExperience`/`TestGrindstoneWindowDisenchant`/
  `TestGrindstoneCloseReturnsInputs` 测试）

- [x] 堆肥桶（对照 1.21.11 反编译的 `ComposterBlock` 逐字节码移植）：
  115 种可堆肥物品表（字节码提取，见 `internal/registry/compost.go`）；
  空桶必定成功、其余按概率掷骰（世界事件 1500）；level 7 → 8 的 20 tick
  定时转换（就绪音效）与周期兜底扫描；满桶使用弹出骨粉并重置；
  漏斗面规则（输入只允许上方插入、产出只允许下方抽出）；level 随方块状态
  随区块持久化；（含 `TestComposter*`/`TestCompostChances` 测试）

- [x] Tick 热路径优化（以 CPU/分配 profile 定位，实测见 docs/PERFORMANCE.md 3.17）：
  A* 寻路改为复用工作区（开放寻址表 + 显式堆，无逐节点堆分配与接口装箱；
  单次搜索 78 µs→7.6 µs、969→1 allocs）；`Chunk.Column`/`ChunkLoaded`/`worldFor`
  读锁快路径；tickMobs/tickItems/tickXPOrbs 临时切片复用（ServerTick 85→1 allocs）；
  掉落物/经验球合并改为空间索引（逐物品全表 O(n²) → 3×3 邻桶，256 物品
  243→87 µs）；碰撞查询按列批量读取（Collides 137→50 ns、ClipMove 269→94 ns）

- [x] 锻造纹饰（smithing_trim，对照 1.21.11 反编译的 `SmithingTrimRecipe`/`ArmorTrim`
  与官方服务端实测移植）：18 条纹饰配方（模板 + 基底 + 添加物 + 图案，由
  `tools/genregistries` 生成）；物品→纹饰材质映射取自 items.json 的
  `minecraft:provides_trim_material` 组件（原版 `TrimMaterials.createFromIngredient`）；
  产物为基底副本 + `minecraft:trim` 组件（保留耐久/附魔等其他组件），
  相同图案与材质重复应用无产物（原版 `applyTrim` 语义）；纹饰随
  players.json、容器区块负载（v8）与掉落物记录持久化；锻造台输入槽限制
  （模板/基底/添加物属性集，与原版槽位 mayPlace 一致）

- [x] 修复附魔与药水组件的 Holder 编码（实测修正）：`minecraft:enchantments`（13）
  与 `minecraft:potion_contents`（49）的注册表引用应为**裸 ID**（原版
  `holderRegistry` 无内联回退；gmcs 此前的 +1 会让客户端解析成错误条目），
  纹饰材质/图案（54）与音效等“可内联”Holder 仍为 ID+1；用官方服务端抓包
  逐字节回归（`internal/item/vanilla_components_test.go`）

- [x] 修复 Update Recipes（0x83）数据包语义：改为发送原版 7 个配方属性集
  （smithing_base/template/addition 与 furnace/blast_furnace/smoker/campfire_input）；
  客户端锻造台槽位的放入校验（`Slot.mayPlace`）依赖 smithing_* 属性集，
  此前按配方名发送伪条目导致客户端拒绝把任何物品放入锻造槽；物品集合规模
  与官方服务端实测一致（37/19/11/156/62/9/9）

- [x] 切石机选择语义对齐原版（对照 1.21.11 反编译的 `StonecutterMenu`）：
  输入物品类型变化（含变空）清空选择、放入原料不再自动选中（点击配方列表项
  后才计算产物）、取走产物后输入仍有剩余时保留选择（原版结果槽 `onTake`
  重新应用当前下标）、点击相同下标不重算；选中下标经 Set Container Property
  0 号属性同步（原版 `DataSlot` 槽 0 存过滤后下标），仅变化时发包

- [x] 漏斗传送冷却持久化（原版 `TransferCooldown`）：区块负载 v9 追加该字段；
  搬运后写回、逐 tick 递减就地写回（新增 `World.SetTransferCooldown`，不重建槽位
  数组避免每 tick 分配）、冷却归零且槽位已空时记录被清除；重启后从剩余冷却继续

- [x] 长时负载复测（单机 loopback，默认配置、视距 10）：100 玩家 × 3 分钟
  100/100 成功、无断开、服务器日志 0 ERROR/WARN；100 玩家 × 60 秒采样服务器
  CPU ≈1.12 核、VmHWM 76.6 MB（数据与阶段说明见 PERFORMANCE.md 3.18）

- [x] 掉落物持久化完善：速度、存活时间（Age）与拾取延迟随 entities.json 保存（对照
  原版 `ItemEntity` 的存档字段 Age/PickupDelay，速度属于实体基础字段）；读取时对
  异常值限幅（|速度| ≤ 100、年龄 ∈ [0, 6000]、拾取延迟 ∈ [0, 40]），并新增
  回归测试覆盖往返与异常值清理

- [x] 窗口有效性实时校验（对照原版 `ServerPlayer.doTick`：每 tick 检查
  `AbstractContainerMenu.stillValid`，失效即关闭并通知客户端）：容器/工作台/
  切石机/锻造台/铁砧/砂轮机/附魔台窗口在方块被破坏/替换或玩家离开交互距离时
  主动关闭（读循环处理完每个数据包后校验，客户端每 tick 都会发包）；
  容器被破坏时（含爆炸等非玩家来源）向所有观察者发送关闭包，各会话在
  下一个数据包时经校验清理自身状态与光标；合成格/输入槽物品按原规则归还背包

- [x] 玩家装备同步（Set Equipment，0x64，对照官方服务端抓包实测——
  带主手钻石剑与头盔的盔甲架配对数据 `64 56 80 01 a8 07 00 00 05 01 ca 07 00 00`）：
  其他玩家可见手持物品与盔甲；玩家实体出现在视野（进入世界/维度切换/新玩家加入）时
  下发全部 6 个装备槽（主手/副手/靴子/护腿/胸甲/头盔，槽位编号为原版 EquipmentSlot 顺序），
  此后变更（物品栏点击/拾取/切换快捷栏/盔甲变化）时广播给 64 格内玩家；
  空槽也下发（count=0）以避免观察者保留旧物品；槽位字节高位 0x80 表示“后面还有条目”；
  变更检测在会话读循环维护快照（物品栏由会话串行访问，每包刷新 ≈126 ns、零分配），
  Tick 线程只读取快照后广播；生物装备（僵尸穿盔甲）未实现

- [x] 火焰蔓延（对照 1.21.11 反编译的 `FireBlock`/`BaseFireBlock`/`SoulFireBlock`）：
  可燃数值表为机械提取的 206 个方块（鼓励值/燃烧速率，`internal/registry/fire_flammables.go`）；
  age 推进 `min(15, age+rand(0..2)/2)`；无燃料时“下方顶面完整且 age ≤ 3 才保留，
  否则熄灭”；有燃料时 age 到 15 后每 tick 1/4 概率在下方不是可燃方块时熄灭；
  六邻域燃烧判定（水平 300/垂直 250 基数，命中后按 `rand(age+10) < 5` 决定
  点燃该方块或直接烧毁，TNT 会被点燃）；3×3×6 空气蔓延
  （`(可燃度+40+难度×7)/(age+30)`，越往上越难，群系 `increased_fire_burnout` 减半）；
  维度永燃标签（下界岩/岩浆块）、灵魂火（无 tick、不蔓延、需灵魂沙/灵魂土）、
  放置规则“目标必须是空气”；火焰只在 128 格内有非旁观玩家时推进
  （原版规则 `fire_spread_radius_around_player` 的默认值；gmcs 无游戏规则）

- [x] 生物与玩家在火方块中着火（对照 1.21.11 反编译的 `BaseFireBlock` 方块内效果
  与 `Entity.baseTick`）：站在火/灵魂火方块上点燃 8 秒（玩家额外每 tick +rand(1..2)）、
  接触岩浆点燃 15 秒并受 4 点岩浆伤害、着火每秒 1 点火焰伤害（受受伤无敌帧限制）、
  入水立即熄灭；着火状态通过实体共享标志（Set Entity Data 索引 0 bit0，变化才发包）
  同步，并随 entities.json 持久化（原版实体存档的 Fire 字段，0 时省略字段，
  读档限幅 ≤300）；死亡时清除火焰标志并掉落战利品

- [x] 水流推动掉落物（对照 1.21.11 反编译的 `FlowingFluid.getFlow` 与
  `Entity.updateFluidHeightAndDoFluidPushing`）：水位差流向（含“邻格为空时看
  其下方同流体水位”的分支）、下落水流被侧面挡住时附加 -6 垂直分量、
  实体盒覆盖方块取平均后归一化、运动比例 0.014、低速时流向抬升到 0.0045；
  仅应用于掉落物（玩家由客户端移动负责，生物不建模水流漂流）；
  `BenchmarkItemWaterFlowPush` 实测 ≈0.39 µs/物品·tick（零分配）

- [x] 生物看向附近玩家（对照 1.21.11 反编译的 `LookAtPlayerGoal` 与 `LookControl`）：
  未移动的生物每 tick 2% 概率选中 8 格内最近玩家，注视 40–79 tick，
  以每 tick ≤10° 的速度把朝向（含俯仰）转向玩家；目标离开范围/换维度时立即停止；
  朝向变化时沿既有实体同步路径发包（含头部转向）

- [x] 实体跟踪生命周期（修复多人可见性缺陷）：为每名玩家维护“客户端当前持有的
  实体集合”（生物/掉落物/箭矢/TNT/经验球/其他玩家），每 tick 增量同步——
  实体进入 64 格补发 Add Entity（含元数据），离开 80 格（64+16 滞回）补发
  Remove Entities；修复三类问题：（1）实体只在生成瞬间发给范围内玩家，
  后来靠近的玩家永远收不到（多人游戏里生物/掉落物/玩家互相不可见）、
  （2）离开范围的实体不移除（客户端留下冻结的幽灵实体）、
  （3）维度切换/死亡重生后客户端清空实体列表但服务端不补发（新维度/重生点
  附近的实体不可见）；生成路径登记“当时在范围内”的玩家并即时下发（低延迟），
  越界与销毁由 tick 增量对账（对“跟踪集中已不存在”的实体补发一次幂等移除，
  可治愈销毁包与补发加入包发生线上乱序的极端情形）；加入时只发送 64 格内的
  实体（此前把整个维度的实体全部发给新玩家，玩家多/掉落物多时加入流量巨大），
  并补上此前完全漏发的经验球；
  `TestItemSentWhenPlayerApproaches`/`TestEntityRemovedWhenPlayerLeavesRange`/
  `TestMobSentToApproachingPlayer`/`TestPlayerEntitySentWhenOtherApproaches`/
  `TestRespawnResendsNearbyEntities`/`TestDimensionChangeResyncsEntities`；
  `BenchmarkEntityTracking` 实测 ≈4.7 µs/tick（1 玩家 + 128 掉落物 + 16 生物，
  零分配；`BenchmarkServerTick` 32 生物场景 15.8–16.4 → 16.4–17.0 µs/op）

- [x] 掉落物运动与原版 `ItemEntity` 对齐（对照 1.21.11 反编译）：水中分支改为
  水平 ×0.99 + 垂直提速 5.0E-4（阈值 0.06、不减重力、不设垂直上限；此前是
  ×0.8 阻尼 + 0.02/0.1 的“快速上浮”近似）；移除人造终端速度（改由垂直
  ×0.98 阻尼自然收敛，终端 ≈-1.96）；落地反弹（垂直速度取碰撞裁剪值 ×-0.5，
  此前直接归零）；水平摩擦改为“空中 0.98 / 落地 脚下方块 slipperiness×0.98”
  （普通方块 0.6×0.98、冰 0.98、蓝冰 0.989、粘液块 0.8；此前落地固定 ×0.6）；
  合并检查间隔对齐原版（跨方块边界移动时每 2 tick、静止时每 40 tick；此前
  固定每 4 tick）；`BenchmarkItemTick` 128 掉落物 ≈40 → ≈31 µs（移除多余的
  碰撞探测查询，顺带更快）

- [x] 火/水/岩浆判定改为按实体碰撞盒覆盖的方块（收缩 1e-7，与原版
  `Entity.checkInsideBlocks` 的 AABB 处理一致；此前只看“脚部 + 上方一格”单列，
  站在方块边缘、盒子越过边界时漏判，短实体被上方一格误判）：玩家与生物共用
  `environmentInBox`（按列批量读取，最多 2×2 列）；
  `TestEnvironmentBoxOverlapIgnites`（边缘生物/玩家被相邻火方块点燃）与
  `TestEnvironmentBoxDoesNotOverreach`（不重叠不点燃）

- [x] 并发与热路径性能批次（见 docs/PERFORMANCE.md 3.25）：
  方块状态类别位表（`StateKindOf`，替换 tick 热路径的字符串 map 查找）；
  玩家位置/维度/死亡状态改为原子快照（`playerPose` + atomic，消除实体 Tick
  每 mob × 每玩家的 `stateMu` 争用）；`mobKinds` 数组化、`worldFor` 无锁快照；
  会话写批处理（8 KiB `bufio` + Tick 尾/读循环底/进出世界刷出时机，多目标
  广播压缩帧惰性构造一次共享）；`World.Chunk` 缓存命中读锁、缺失时锁外
  加载/生成（`publishChunkLoad` 与预生成去重共用）；
  实测：`ServerTick` -27%、新增 `ServerTickManyPlayers`（32 生物 + 100 玩家）
  -34%/分配 -97%、100 玩家 loopback 压测 CPU -52%（基准与压测细节见文档）

- [x] 安全加固补充：`max_players` 人数上限强制执行
  （`multiplayer.disconnect.server_full`，同 UUID 重连允许替换）；
  容器点击 / 创造模式物品操作 / Tab 补全令牌桶限流（与移动/聊天一致的
  防洪泛策略）

- [x] 内存装载/释放与预加载批次（见 docs/PERFORMANCE.md 3.26）：
  卸载宽限（区块连续远离超过 15 秒才卸载，边界来回移动不再反复
  加载/卸载/写盘）；无玩家维度空闲 30 秒后释放全部区块（切维度后旧
  维度不再长期占用内存，快速切回无需重建）；预取范围扩大为视距+1
  （跨区块发送无需等待磁盘/生成器）；区块编码 singleflight（并发
  未命中只编码一次、共享帧）与帧缓存上限 2048→4096；卸载编码移出
  世界锁。实测：50 玩家视距 10 场景 CPU -80%、峰值内存 -30%，
  100 玩家视距 6 场景 CPU 持平、内存 -5%~-12%

- [x] 护甲、附魔数值效果与掉落表批次（对照 1.21.11 官方数据）：
  - 护甲值与韧性由 `tools/genregistries` 从 `reports/items.json` 的
    `minecraft:attribute_modifiers` 生成（41 项）；玩家受伤按原版
    `CombatRules.getDamageAfterAbsorb` 结算（韧性参与的钳制公式），保护类附魔
    （protection 1 EPF/级、火焰/爆炸/弹射 2 EPF/级、摔落 3 EPF/级，上限 20）
    再按 `getDamageAfterMagicAbsorb` 减免；`bypasses_armor`/`bypasses_enchantments`
    伤害类型标签来自数据包标签；
  - 护甲耐久：非穿甲伤害按 `max(1, 伤害/4)` 损耗每件护甲（不灭附魔按
    护甲 2L/(10+5(L-1))、非护甲 L/(L+1) 概率免除），损坏时移除并广播事件；
  - 近战附魔：锋利（1+0.5×(L-1)）、亡灵杀手/节肢杀手（2.5×L，按实体类型标签）、
    击退（0.4+0.5×L 格）、火焰附加（4 秒×L）、抢夺（常规掉落 +uniform(0..L)、
    稀有掉落 2.5%+1%×L）；
  - 挖掘附魔：效率（工具速度 >1 时 +L²+1）、水下速掘（免除头部浸水的 ÷5）、
    不灭；呼吸附魔提高氧气上限（300+L×20，缓存到原子字段避免每 tick 读物品栏）；
  - 精准采集/时运/剪刀掉落：玻璃/冰/树叶/草方块/矿石/西瓜/黏土/雪块保留自身；
    矿石 ore_drops、红石/西瓜 uniform_bonus_count、青金石/铜矿/下界金矿、
    砂砾燧石 table_bonus、树叶树苗/木棍/苹果概率；墙式告示牌/旗帜/火把掉落
    站立变体；
  - 经验修补（拾取经验修复带 mending 的物品，2 耐久/点经验）、
    绑定诅咒（生存不可取出盔甲）、消失诅咒（死亡销毁）；
  - 新增 `BenchmarkPlayerDamageMitigation`（336 ns/op，0 alloc）与
    `BenchmarkBlockDropsFortune`（60.5 ns/op，24 B/1 alloc）；
    `BenchmarkServerTick` 17.5 µs/1 alloc、`ServerTickManyPlayers` 327 µs
    （与 3.26 记录持平，未引入 tick 路径回退）；测试与 race 全绿

- [x] 玩家远程武器与盾牌：
  - 弓（主手/副手）蓄力 1 秒满蓄力，释放使用键时按蓄力发射；箭速 = 蓄力×3，
    伤害 = ceil(箭速×(2+0.5×力量等级))；力量/冲击/火矢/无限/不灭附魔生效；
    消耗箭与 1 点弓耐久（创造与无限不消耗箭）；箭矢支持玩家归属，命中
    生物或其他玩家（跳过射手本人），结算伤害/击退/点燃，玩家击杀生物
    仍掉落经验球；
  - 盾牌按住右键格挡正面伤害（`bypasses_shield` 标签伤害不可格挡），
    按 1+floor(伤害) 损耗耐久（不灭可免除），损坏自动解除；
    Use Item 包新增 hand/sequence/rotation 解析，主手与副手均可进食/使用；
  - 测试：`TestParseUseItem`、`TestBowFiresArrow`、`TestBowRequiresArrow`、
    `TestBowInfinityKeepsArrow`、`TestShieldBlocksFrontDamage`、
    `TestShieldDoesNotBlockBypassingDamage`；
  - 复测 `BenchmarkServerTick` 16.8 µs/1 alloc、`ServerTickManyPlayers` 321 µs、
    `BenchmarkEntityTracking` 5.1 µs/0 alloc（与上批持平）

- [x] 正确性与稳定性修复批次：
  - 箭矢命中改为线段扫掠（slab）判定，修复 3 格/tick 箭矢穿过 0.6 格宽生物
    的问题；箭矢移除只通知同维度玩家（此前向所有维度广播 Entity Destroy）；
  - 经验球持久化：各维度 entities.json 新增 `xp_orbs`（Value/Count/Age
    与速度；对照原版 `ExperienceOrb` 保存 Age/Value），重启/区块重载后恢复；
  - 持久化输入校验：生物/掉落物/箭矢/经验球与玩家记录拒绝非有限/越界坐标，
    限幅生命、伤害、耐久、堆叠数与药水 ID（异常存档不会产生 NaN 物理或
    协议编码异常）；
  - 新加载区块扫描同时为 level 7 堆肥桶重新登记 20 tick 转换（重启后不再
    等待 600 tick 兜底扫描），测试 `TestComposterScheduledOnChunkLoad`；
  - KeepAlive 严格校验：未请求/过期的响应按 `disconnect.timeout` 断开，
    上一轮未响应时不再发送新包而是超时断开；
  - 新增测试：`TestSegmentIntersectsBox`、`TestArrowHitsMobAtHighSpeed`、
    `TestXPOrbPersistence`、`TestEntityPersistenceRejectsInvalidCoordinates`、
    `TestUnsolicitedKeepAliveDisconnects`；
  - 复测 `BenchmarkServerTick` 15.8–16.3 µs/1 alloc、`BenchmarkEntityTracking`
    4.7–4.8 µs/0 alloc；本轮未新增箭矢专项基准，扫掠判定的单次成本未单独测量。

- [x] 并发安全与玩家数据完善批次：
  - 物品栏耐久/修复改为锁内读-改-写（`Inventory.Damage` / `Inventory.Repair`）：
    Tick 线程的护甲损耗、经验修补与读循环的槽位操作不再互相覆盖；
    旧的 `Get` → 修改 → `Set` 在并发下会丢失耐久更新（已加回归测试）；
  - 选中快捷栏槽位改为原子字段并持久化（`players.json` 新增可选
    `selected_slot`，对应原版 `SelectedItemSlot`）；进入世界、跨维度传送与
    重生后发送 Clientbound `set_held_slot`（0x67，VarInt 槽位），
    包 ID 取自 1.21.11 官方 `packets.json`，字段类型经
    `ClientboundSetHeldSlotPacket` 字节码（`ByteBufCodecs` 的 VarInt）核对；
    serverbound `set_carried_item` 的越界槽位被忽略
    （原版 `Inventory.setSelectedSlot` 会抛异常断开客户端）；
  - 周期自动保存（后台 goroutine）与玩家数据快照现在可安全读取选中槽位；
  - 新增测试：`TestInventoryDamageBreaksItem`、
    `TestInventoryDamageRejectsNonDamageable`、`TestInventoryRepair`、
    `TestInventoryDamageConcurrentAccumulates`、`TestEncodeSetHeldSlot`、
    `TestSelectedHotbarSlotPersists`；
  - 新增 `BenchmarkArrowSweepAgainstMobs`（每次 32 个目标，121–123 ns/op、
    0 alloc，约 3.8 ns/目标）；复测 `BenchmarkServerTick` 15.4–16.1 µs/1 alloc、
    `BenchmarkEntityTracking` 4.7–4.9 µs/0 alloc（与前两批持平）。

- [x] 玩家环境状态持久化批次：
  - `players.json` 新增可选字段 `air`（指针，区分“0 氧气”与旧记录缺失）、
    `fire_ticks`、`exhaustion`、`food_timer`，对应原版 playerdata 的
    `Air`/`Fire`/`foodExhaustionLevel`/`foodTickTimer`；重连后继续着火/溺水倒计时，
    不再无条件满氧气；旧存档缺省按满氧气处理；
  - 非法值限幅：氧气 [-20, 360]（360 = 300 + 呼吸 III）、着火 [0, 300]、
    疲劳度 [0, 40]、饥饿计时 [0, 80]；NaN 疲劳度归零；
  - `fireTicks`/`air` 改为 `atomic.Int32`（原为 Tick goroutine 独占字段），
    周期自动保存（后台 goroutine）可安全读取；环境 Tick 内改用 Load/Store，
    无新增分配；
  - 新增测试 `TestEnvironmentStatePersists`（保存 + 重连恢复）。

- [x] 作物与浆果掉落表批次：
  - 按 1.21.11 官方 `data/minecraft/loot_table/blocks` 逐条移植：
    小麦/甜菜根（未成熟只掉种子，成熟掉作物 + 1+Binomial(3+时运, 0.5714) 种子）、
    胡萝卜/马铃薯（成熟掉 1+Binomial(3+时运) 个，马铃薯额外 2% 毒马铃薯；
    未成熟掉 1 个）、下界疣（成熟 2–4 + uniform(0..时运)，未成熟 1）、
    甜浆果丛（age 3 → 2–3 + uniform、age 2 → 1–2 + uniform、更低无掉落）、
    火把花作物（任何年龄 1 种子）；
  - 新增 `registry.BlockStateAge`：作物方块只有 age 属性、状态区间按年龄升序，
    用状态区间反推 age 并校验区间长度（不依赖未收录作物的 `BlockPropertyStates`）；
  - 测试 `TestBlockDropsCrops` 覆盖成熟/未成熟/时运/精准采集不影响作物；
  - 行为变化：此前作物无论成熟度都固定掉 1 个同名物品，现在按原版掉落。

- [x] 剩余植物掉落与双格植物修复批次：
  - 可可豆（age 2 → 3 个，其余 1）、海泡菜（数量 = pickles 属性 1–4，
    含水与水上方块一致）、瓜类茎（Bin(3, (age+1)/15) 个种子）、
  - 瓶子草作物（age 0–3 掉荚、age 4 掉瓶子草，上下半格一致）；
  - 修复双格植物重复掉落与悬空半格：此前依赖 `StateProps` 读取 `half`，
    但 `BlockPropertyStates` 未收录高草/大型蕨等，导致上下半格各掉一次；
    现由 `registry.BlockStateHalfUpper`/`BlockStateCropUpperHalf` 从状态区间
    判定，破坏任一半时按下半格战利品表结算一次，并移除另一半（不掉落），
    对应原版 `DoublePlantBlock` 的邻居更新语义；
  - `registry.BlockStateAge` 支持 age 主序 + 固定 stride 的方块
    （可可豆 stride=4、瓶子草 stride=2）；新增 `BlockStateRangeOf` 便于按
    区间起点构造状态（`BlockStateID` 返回默认状态，不一定是起点）；
  - 新增测试：`TestBlockStateHalfUpper`、`TestBlockStateCropUpperHalf`、
    `TestBlockStatePickleCount`、`TestRemoveDoublePlantPair`，并扩展
    `TestBlockDropsCrops`；
  - 行为变化：双格植物不再双倍掉落，且不会留下悬空的另一半。

- [x] 农业系统批次：
  - 锄头耕地（对照 `HoeItem.useOn` 字节码）：草方块/土径/泥土 → 耕地
    （moisture=0），砂土 → 泥土，缠根泥土 → 泥土并掉落 1 个垂滴根须；
    要求点击面不是底面且上方为空气；播放 `item.hoe.till`、消耗 1 点耐久
    （不灭附魔按既有规则免除）；
  - 作物种植（`BlockItem.place` + `CropBlock.canSurvive`）：小麦/甜菜根/
    胡萝卜/马铃薯/火把花种在耕地上、下界疣只能种在灵魂沙；非创造模式消耗
    1 个物品，播放 `item.crop.plant`；新增耕地属性状态表（8 档 moisture）；
  - 作物随机刻（`CropBlock.randomTick`/`NetherWartBlock.randomTick`）：
    每登记位置 3/4096 概率被选中（等价原版“每 section 3 个随机位置”），
    作物按 `getGrowthSpeed`（3×3 耕地含水量、行列/对角减半）与
    `nextInt(25/speed+1) == 0` 生长；下界疣不看光照、按 1/10 概率生长；
  - 耕地随机刻（`FarmBlock.randomTick`）：4 格半径内有水 → 湿度 7；
    否则湿度递减，湿度 0 且上方不是 `maintains_farmland` 标签方块 → 泥土；
  - 踩踏农田（`FarmBlock.fallOn` 玩家分支）：下落 >0.5 格时按
    `nextFloat() < fall-0.5` 判定退化为泥土；
  - 骨粉（`BoneMealItem.useOn`/`applyBonemeal`）：先试被点击方块、再试点击
    面外侧；催熟 2–5 级（钳制到最大年龄），满龄不接受，成功时消耗 1 个并
    广播世界事件 1505；下界疣不是可催熟方块（原版如此）；
  - 作物/耕地注册：新加载区块增量扫描 + 种植/耕作时即时登记，方块移除时
    注销，区块卸载时丢弃；随机刻使用复用的键快照（零分配）；
  - 新增测试：`TestHoeTillsSoil`、`TestHoeTillingRequiresAirAbove`、
    `TestHoeTillsCoarseAndRootedDirt`、`TestPlantCropOnFarmland`、
    `TestPlantCropRequiresFarmland`、`TestPlantCropRequiresSkyLight`、
    `TestBoneMealGrowsCrop`、`TestNetherWartPlantAndGrowth`、
    `TestCropRandomTickGrowth`、`TestCropGrowthSpeed`、
    `TestFarmlandMoistureNearWater`、`TestFarmlandWithCropStaysFarmland`、
    `TestTrampleFarmland`（registry 侧：`TestFarmlandMoistureStates`、
    `TestBlockStateAgeRoundTrip`）；
  - 新增基准 `BenchmarkCropTick`（1 作物 + 1 耕地）与
    `BenchmarkCropTick256`（16×16 农田），实测见
    [PERFORMANCE.md](PERFORMANCE.md) 3.33。

- [x] 方块放置形态批次：
  - 台阶（`StairBlock.getStateForPlacement`）：facing = 玩家水平朝向，
    half 由点击面/点击高度决定（点击顶面或侧面低处 → bottom）；
  - 半砖（`SlabBlock.getStateForPlacement`）：type 判定同上；点击同种单半砖
    时叠成双半砖（原版“单半砖可替换”，放置位置即被点击的半砖）；
  - 柱状方块（`RotatedPillarBlock`）：axis = 点击面的轴（原木/木头/菌柄/
    菌核/玄武岩/深板岩/锁链/干草块等 67 个方块）；
  - 朝向方块（`HorizontalDirectionalBlock`）：facing = 水平朝向的反方向
    （箱子/陷阱箱/末影箱/熔炉/烟熏炉/高炉/南瓜灯/织布机/切石机/蜂箱/营火/
    讲台/带釉陶瓦）；木桶/潜影盒使用六向 facing（含上下）；
  - 活板门（`TrapDoorBlock`）：facing = 水平朝向的反方向、half 由点击面
    决定；右键开合（`useWithoutItem`，保留 facing/half/powered）；
  - 梯子（`LadderBlock`）：只能贴在水平面，facing = 点击面的反方向；
  - 火把（`WallOrFloorItem`）：点击顶面放地面火把、点击侧面放墙式变体
    （facing = 点击面的反方向）、点击底面不放置；
  - 拉杆/按钮（`FaceAttachedHorizontalDirectionalBlock`）：按点击面决定
    face（floor/wall/ceiling）与 facing（顶面/底面用玩家水平朝向，侧面用
    点击面的反方向）；墙式附着方向直接决定红石供电目标（`connectedDirection`）；
  - 门（`DoorBlock`）：facing = 玩家水平朝向、hinge 按点击位置回退分支；
    放置时同时创建上下两格（需要两格空间）；右键开合同步两半（铁门只能由
    红石开合）；破坏任一半移除另一半，且只有下半格掉落 1 个门（对应战利品表
    的 half=lower 条件，`mining.go` 跳过上半格掉落）；
  - 栅栏门（`FenceGateBlock`）：facing = 玩家水平朝向，放置位置通电时直接以
    打开状态放置，右键开合；
  - 可开合方块的红石联动（`neighborChanged` 语义）：门/活板门/栅栏门在邻居
    信号变化时把 powered 与 open 一起设置（门的两半同步；手动打开且未通电
    的方块不会被误关）；新增 `server/openables.go`，接入
    `refreshRedstoneConsumers`；
  - 生成器新增 `PlacementDoorsPowered`/`PlacementTrapdoorsPowered` 与
    `PlacementFenceGates`/`Open`/`Powered` 表；
  - 新增生成器 `tools/genregistries/placement.go`：只生成放置会用到的属性
    组合（台阶 58、半砖 62、轴 67、朝向 48、活板门 21），找不到匹配状态时
    生成失败（版本升级后属性变化会立即暴露）；
  - 新增测试：`TestOrientPlacementStairs`/`Slab`/`Axis`/`Facing`/`Trapdoor`/
    `Ladder`/`Torch`/`FaceAttached`、`TestPlaceChestFacing`、`TestPlaceLogAxis`、
    `TestPlaceStairsOrientation`、`TestMergeSlabIntoDouble`、
    `TestToggleTrapdoor`、`TestPlaceTorchOnWall`、`TestPlaceLeverOnGround`、
    `TestDoorHingeIndex`、`TestPlaceDoorBothHalves`、`TestDoorRequiresTwoBlocks`、
    `TestDoorToggleBothHalves`、`TestDoorBreakDropsOnce`、
    `TestIronDoorNotToggledByHand`、`TestPlaceFenceGateFacing`、
    `TestToggleFenceGate`、`TestRedstoneOpensTrapdoor`、`TestRedstoneOpensDoor`、
    `TestRedstoneOpensFenceGate`；
    registry 侧 `TestPlacementTablesConsistent`（状态 ID 必须属于对应方块）。

- [x] 天气系统批次：
  - 周期推进（对照 `ServerLevel.advanceWeatherCycle` 字节码）：
    `clearWeatherTime` 强制放晴倒计时；`rainTime`/`thunderTime` 递减到 0 时
    翻转状态，随后按原版区间重掷（下雨 3600–15600、放晴 12000–180000、
    打雷 12000–24000、不打雷 12000–180000）；
  - 客户端同步：`rainLevel`/`thunderLevel` 以 0.01 步进逼近 0/1 并 clamp，
    值变化时广播 Game Event 7（雨强度）/8（雷强度）；下雨状态翻转时广播
    Game Event 1（开始）/2（停止）并补发一次强度；进入世界时额外同步一次
    当前天气（原版只在变化时广播，差异见 MIGRATION）；
  - `isRainingAt`（`Level.getPrecipitationAt`）：未下雨、维度不是主世界、
    区块未加载、上方有阻挡方块时为否；天空暴露用“有碰撞形状”近似
    `Heightmap.MOTION_BLOCKING`（水/火/草/花不阻挡降水）；
  - 联动：雨天滋润耕地（`FarmBlock.randomTick` 的 `isRainingAt(pos.above())`）、
    浇灭火焰（`FireBlock.tick`：自身 + 四个水平邻居任一降雨时按
    0.2 + age × 0.03 概率熄灭）、雨天阻止火焰蔓延、雨中清除玩家与生物着火
    （`Entity.baseTick` + `Entity.isInRain` 的脚部/包围盒顶部两次判定）；
  - 新增命令 `/weather <clear|rain|thunder> [秒]`（权限等级 2、
    节点 `gmcs.command.weather`，含 Tab 补全与帮助文本）；
  - 测试确定性：天气使用独立随机流（`weatherRand`），不影响既有的
    `nextRandom` 序列；`joinServer`/`newHopperTestServer` 关闭天气推进
    （`weatherEnabled=false`），天气行为由 `weather_test.go` 显式驱动；
  - 新增测试：`TestWeatherCycleTogglesRain`、`TestWeatherClearCountdownRestartsRain`、
    `TestWeatherBroadcastTransition`、`TestWeatherJoinSync`、
    `TestWeatherRainHydratesFarmland`、`TestWeatherExtinguishesFire`、
    `TestWeatherExtinguishesPlayerFire`、`TestWeatherCommand`、
    `TestWeatherSkyExposure`、`TestWeatherDisabled`；
  - 新增基准 `BenchmarkWeatherTick`（稳态）与 `BenchmarkWeatherTickTransition`
    （状态翻转 + 3 个包编码），实测见 [PERFORMANCE.md](PERFORMANCE.md) 3.35。

- [x] 可开合方块音效批次：
  - 门/活板门/栅栏门开合广播 Sound Effect 包（方块类别、音量 1、音高
    0.9–1.0），对应原版 `DoorBlock`/`TrapDoorBlock`/`FenceGateBlock` 的
    `level.playSound(null, pos, open ? xxxOpen : xxxClose, BLOCKS, 1.0F,
    random*0.1F+0.9F)`；手动右键与红石驱动两条路径都播放；
  - 音效集合按方块材质分组（对照 `BlockSetType`）：木质 `block.wooden_door.*`/
    `block.wooden_trapdoor.*`、铁质 `block.iron_*`、铜质（含氧化与打蜡变体）
    `block.copper_*`、竹 `block.bamboo_wood_*`、樱花木 `block.cherry_wood_*`、
    下界木 `block.nether_wood_*`，栅栏门统一 `block.fence_gate.*`；
    音效 ID 在 `New` 中解析（缺失时静默跳过）；
  - 音高抖动复用与天气共享的独立随机流 `fxRand`（不消耗玩法随机数
    `nextRandom`，因此不改变既有测试依赖的随机序列）；
  - 新增测试：`TestOpenableSoundGroups`、`TestTrapdoorTogglePlaysSound`、
    `TestFenceGateTogglePlaysSound`、`TestDoorTogglePlaysSound`、
    `TestRedstoneOpenablePlaysSound`。

- [x] 栅栏门 in_wall 批次：
  - 对照 1.21.11 `FenceGateBlock.getStateForPlacement` 与 `updateShape`（javap 字节码）：
    `in_wall` 由**沿 facing 轴的前后两个邻居**是否为 `minecraft:walls` 标签方块决定
    （不是垂直方向），放置时计算、邻块变化时重新计算，且不影响 open/powered；
  - 生成器：`PlacementFenceGates`/`Open`/`Powered` 从 `[facing]` 扩展为
    `[inWall][facing]`（每方块 24 个状态，整体表仍小于 1 KiB），
    registry 测试新增“in_wall 必须区分状态”断言；
  - 服务端：`placeFenceGate` 按邻居墙选择状态；`refreshOpenable` 对栅栏门同时
    维护 in_wall 与 open/powered（邻居区块未加载时保留原值，避免跨区块边界误判）；
    开合音效只在开合变化时播放，in_wall 变化静默；
  - 新增测试：`TestFenceGateInWallOnPlacement`、
    `TestFenceGateInWallUpdatesOnNeighborChange`、
    `TestFenceGateInWallIgnoresPerpendicularWalls`。

- [x] 门铰链与通电放置批次：
  - 对照 1.21.11 `DoorBlock.getHinge` 字节码：以点击面为基准，逆时针侧（含
    上方一格）每个“面坚固”邻居计 −1、顺时针侧计 +1 得到 i；逆时针侧是门的下
    半格且顺时针侧不是（或 i > 0）→ right；顺时针侧是下半格门且逆时针侧不是
    （或 i < 0）→ left；否则按点击位置相对门坐标的水平偏移回退（点击顶面/底面
    时恒为 left）；
  - `DoorBlock.getStateForPlacement`：放置位置或其上方一格有邻居信号时，
    门以 `open=true、powered=true` 放置（铁门因此能直接被红石打开）；
  - 旧实现的回退分支误用了玩家朝向而非**点击面**，导致点击顶面时铰链随点击
    横向位置变化（与原版不一致），本批一并修正；
  - 新增测试：`TestDoorHingeIndex`（回退、两侧坚固面、两侧下半格门）、
    `TestPlaceDoorPoweredWhenSignal`（无信号→关闭、相邻红石块→通电打开）。

- [x] 玩法缺陷修复批次（熔炉点燃/窗口自动关闭/树叶衰减/出生点卡住）：
  - 熔炉 `lit` 属性：对照原版 `AbstractFurnaceBlockEntity.setLit`，燃烧开始/结束时
    写回方块状态并广播 Block Update；为此把熔炉族（`furnace`/`blast_furnace`/
    `smoker`）加入生成器的属性状态表（`propStateBlocks`），使 `StateProps`/
    `BlockStateWithProps` 能读写 `facing`+`lit`；新增 `TestFurnaceLitPropertyFollowsBurning`；
  - 窗口自动关闭：原版在 `ServerPlayer#tick` 用 `containerMenu.stillValid` 关闭
    离得太远的窗口；gmcs 在读循环处理每个包前调用 `closeWindowOutOfReach`，
    覆盖箱子/木桶/漏斗等容器、熔炉族、酿造台、铁砧、砂轮机、附魔台、切石机、
    锻造台、工作台合成格（会话 goroutine 串行访问窗口字段，无跨线程竞态）；
    新增 `TestContainerClosesWhenOutOfReach`；
  - 树叶衰减：新增 `internal/server/leaves.go`。玩家放置的树叶用生成器产出的
    `LeafPersistentStates`（`persistent=true、distance=7`）且永不衰减；自然树叶
    在原木/树叶被移除（含爆炸摧毁）时对周围 15³ 做一次“6 格内是否仍有原木”
    判定，没有则按方块战利品表掉落并移除；新增 `TestLeavesDecayAfterLogRemoved`、
    `TestPlacedLeavesDoNotDecay`；
  - 出生点碰撞：`world.FindSpawn` 的候选列新增“玩家碰撞盒不与碰撞形状相交且确有
    支撑面”的校验（栅栏/墙/箱子顶面等此前会被当作可站立地面，导致玩家包围盒嵌进
    方块、所有移动被穿墙校验拒绝）；进入世界时再对恢复位置做一次兜底（
    `unstickPlayerPosition`：先向上推、再逐圈找空位）；新增
    `TestUnstickPlayerPosition`、`TestJoinPositionIsMovable`、
    `TestSpawnColumnAvoidsCollisionShapes`；
  - `TestPrefetchAfterClose` 偶发失败（CI 红）：`World.Chunk` 的缓存命中快速路径
    未读取 `closed`，预取 worker 在 Close 前发布的区块会在 Close 后被当作成功返回；
    改为在同一次 RLock 内读取 `closed`。

- [x] 玩法缺陷批次（地形观感/配方书/仙人掌/动物逃跑/旁观者/时间持久化/海洋生物/移动/日光）：
  - **地形**：洞穴噪声阈值 0.66→0.76、蠕虫隧道数量/半径/长度减半（地下空腔从
    20–25% 降到 1–7%）；河流带宽 0.045→0.09 并改为二次衰减的缓坡河岸（原来只有
    4 格宽、十余格深的笔直峭壁），高处下切强度在 88–108 连续渐隐（不再按高度硬
    截断产生断崖）；新增第二层丘陵噪声；树木装饰改为世界坐标，每个区块额外补上
    相邻区块树干伸入本区块的树叶（不再沿区块边界留 2 格无树走廊，也不会切掉树冠）；
  - **配方书**：对齐原版 1.21.2+ `ServerPlaceRecipe`：单击每格放 1 个（已摆好则
    每格 +1），Shift 把可用原料尽可能多地放进合成格（**不再自动合成**）；原料
    不足时清空合成格并发送 Place Ghost Recipe（0x3D），合成格物品退不回背包时
    不执行任何操作；
  - **仙人掌**：`StateKindCactus` 位表 + 玩家/生物接触每 0.5 秒 1 点伤害
    （原版 `CactusBlock.entityInside`，死亡消息 “was pricked to death”）；
  - **动物逃跑**：受击设置 `PanicTicks=100`/威胁位置，之后以 1.25 倍速度远离
    攻击者（原版 PanicGoal）；水生生物改为加速游动；
  - **旁观者**：移动校验跳过穿墙检查（原版 `Player.isSpectator` 的 noPhysics），
    可以下潜观察地下；
  - **移动**：位置校验加 1 毫米容差（浮点误差）；玩家已经嵌在方块里时（活塞/关闭
    的门/恢复存档）先把玩家推出到空位，而不是永久回拉（此前会“完全无法移动”）；
  - **世界时间**：新增 `world/level.json`（gmcs 自定义格式）持久化世界时间与天气，
    自动保存与关服时原子写入，重启后继续原来的昼夜与天气；
  - **海洋生物**：新增鳕鱼/鲑鱼/热带鱼/鱿鱼（实体类型、音效、掉落、持久化），
    水域生成（独立配额，避免占满总上限），水体内三维游动；
  - **日光**：僵尸/骷髅在白天、主世界、头顶可见天空、不在水/雨中时持续着火 8 秒
    （原版 `Mob.isSunBurnTick`），天亮后不再“怪物不在乎天亮”；
  - 新增测试：`TestTreeCanopyCrossesChunkBorder`、`TestUndergroundIsMostlySolid`、
    `TestRiverBanksAreSmooth`、`TestRecipeBookPlaceCraft`（更新为原版语义）、
    `TestRecipeBookGhostRecipeWhenInsufficient`、`TestCactusDamagesPlayerAndMob`、
    `TestPassiveMobPanicsWhenHurt`、`TestSpectatorMovesThroughBlocks`、
    `TestStuckPlayerIsPushedOut`、`TestSunlightBurnsUndead`、
    `TestAquaticMobsSpawnAndSwim`、`TestSpawnMobInOceanSpawnsAquatic`、
    `TestLevelStatePersistence`、`TestGhostRecipePacketEncoding`。

- [x] 玩家死亡界面与雷暴闪电（本批，已实现并测试）：
  - **Player Combat Kill（0x42）**：玩家死亡时先给自己下发该包（客户端据此
    显示死亡界面），再把死亡消息广播到聊天栏；字段为 VarInt 玩家实体 ID +
    死亡消息 Text Component（网络 NBT String Tag）；
  - **雷暴闪电**：按原版 `ServerLevel.tickChunk` 的概率落雷（每区块每 tick
    1/100000，gmcs 用“视距内 17×17 区块 / 100000”的等效单次掷骰，每名主世界
    玩家一次），落雷点需 `isRainingAt`（下雨 + 可见天空）；生成 Lightning Bolt
    实体（2 tick 后移除）、播放 `lightning_bolt.thunder`/`impact` 音效、
    雷击点空气且下方为固体时点火、`AABB.inflate(3,6,3)` 内生物与玩家受到
    5 点闪电伤害并点燃 8 秒（死亡消息 “%s was struck by lightning”）；
  - 注册表缺少闪电实体/音效/伤害类型时只禁用闪电，不影响其它系统；
  - 新增测试：`TestEncodePlayerCombatKillLayout`、
    `TestEncodePlayerCombatKillEmptyMessage`、`TestDeathScreenPacketOnDeath`、
    `TestDeathScreenUsesCustomTemplate`、`TestLightningStrikeEffects`、
    `TestLightningRequiresRain`、`TestLightningChancePerTick`；
    新增基准：`BenchmarkLightningTickClear`（≈ 20 ns/op、零分配）、
    `BenchmarkLightningTickThunder`（4 名玩家 ≈ 75 ns/op）。

## 待完成

- [ ] **原版区块生成器移植（分阶段，进行中）**：目标是把 `internal/world` 的
  自研生成器换成按原版算法（Noise Router + 密度函数 + 含水层 + 表面规则 +
  雕刻器 + 地物/结构）生成的区块。这是多批次工作，当前只完成第 1 阶段：
  - [x] **阶段 1：数据管线与密度函数图校验**（本批）：新增
    `tools/genworldgen` 从官方 jar 提取 overworld 的噪声设置、全部密度函数、
    全部噪声参数与多噪声群系参数表，压缩为
    `internal/world/vanilla/data/worldgen.json.gz`（约 7 KB，仓库不存放原始数据包
    文件）；新增 `internal/world/vanilla` 包，把密度函数解析为带类型的节点图
    （`Node`/`Spline`），对未知节点类型、缺少必需字段、引用缺失的密度函数
    或噪声参数、引用内置坐标函数（`minecraft:x/y/z`、纯数字文件如
    `minecraft:zero`）等情形一律 fail-fast 校验；
    测试：`TestLoadData`、`TestOverworldRouterResolves`、
    `TestNoiseParametersShape`、`TestUnsupportedNodeTypeIsRejected`、
    `TestMissingNoiseReferenceIsRejected`。
    当前 `internal/world` 的地形仍由自研生成器产生，服务器行为本批**未改变**。
  - [x] **阶段 2a：随机源已与原版逐值对拍**（本批）：实现
    `Xoroshiro`（`internal/world/vanilla/random.go`），种子扩展按 1.21.11 服务端
    字节码核对（`RandomSupport.upgradeSeedTo128bit`：`low = seed ^ 0x6A09E667F3BCC909`、
    `high = low + 0x9E3779B97F4A7C15`，再各做一次 mixStafford13），`NextLong`
    为 xoroshiro128++；用 `tools/vgoracle`（反射调用官方 jar 的
    `XoroshiroRandomSource`）导出的 4 个种子 × 8 个 `nextLong` 逐位一致
    （`testdata/noise_oracle.json`、`TestXoroshiroMatchesVanilla`）。
    踩坑记录：1.21.11 的 `RandomSource.create(long)` 返回 **LegacyRandomSource**
    （LCG），世界生成使用的是 `XoroshiroRandomSource`，对拍必须直接构造后者。
  - [x] **阶段 2b：ImprovedNoise（Perlin）已完成并逐值对拍**：`noise.go` 实现
    `ImprovedNoise`（xo/yo/zo + 置换表 + 8 角梯度点积 + 三轴五次插值），
    与原版一致的细节均已确认：`fkh` 的 16 项梯度表（末 4 项非顺序重复）、
    点积无归一化、`fade = d*d*d*(d*(d*6-15)+10)`、三轴插值顺序为 x→y→z。
    同时修正了 `NextInt`：原版用 **nextLong 的低 32 位** 做 32×32→64 乘法并取
    高 32 位（Lemire 有界随机 + 无符号余数阈值重试），这与高位实现不同，
    也是置换表不对的根因；`nextInt(0)` 与原版一样 panic。
    测试：`TestImprovedNoiseStateMatchesVanilla`（xo/yo/zo 与 256 项置换表逐项
    相等）、`TestImprovedNoiseMatchesVanilla`（4 种子 × 5 点逐值相等）。
  - [x] **阶段 2c：NormalNoise 已完成并逐值对拍**：`noise.go` 增加
    `PerlinNoise`（八度叠加）与 `NormalNoise`（双 Perlin + 归一化），按 1.21.11
    字节码逐条复刻：
    - `PerlinNoise.create`：先无条件构造一个 `ImprovedNoise`（作为最低频八度
      复用），仅当最低频八度下标落在振幅表内且振幅非零时才使用它，否则这份
      随机数被丢弃；随后从 `最低频八度-1` 递减到 0，振幅非零则新建
      `ImprovedNoise`，振幅为零（或越界）则 `consumeCount(262)` 跳过
      （`ImprovedNoise` 恰好消耗 3 个 double + 256 个 int，按 32 位 int 计为 262）；
    - `lowestFreqInputFactor = 2^firstOctave`、
      `lowestFreqValueFactor = 2^(n-1)/(2^n-1)`、
      `valueFactor(v) = Σ amp_i · v · (lowestFreqValueFactor / 2^i)`；
    - `getValue` 用 `wrap(x) = x - floor(x/33554432 + 0.5)·33554432` 折回坐标；
    - `NormalNoise`：两份同参数 `PerlinNoise`，第二份按 `INPUT_FACTOR =
      1.0181268882175227` 缩放坐标；`valueFactor = (1/6)/(0.1·(1+1/(span+1)))`，
      `maxValue = (first.maxValue + second.maxValue)·valueFactor`。
    踩坑记录：原 oracle 用“参数个数 + 类型”挑 `NormalNoise.create`，会随机命中
    混淆名 `a`（非 legacy）或 `b`（`createLegacy`，走 `forkPositional` 派生每个
    八度的随机源），此前导出的数据实际来自 legacy 路径，导致对拍必然失败。
    现已改为**显式按名字取 `a`**，并新增 `normalNoiseDetail`（`valueFactor`、
    `maxValue`、两份 Perlin 各自取值）以便区分“归一化因子错”与“随机源推进错”。
    测试：`TestNormalNoiseMatchesVanilla`、`TestNormalNoiseDetailMatchesVanilla`、
    `TestNormalNoisePerlinValueConsistency`（continentalness / cave_cheese ×
    4 种子 × 5 点全部相等，误差 < 1e-12）。
  - [x] **阶段 2d：世界生成真实的 legacy 噪声路径已完成并逐值对拍**：确认
    `Noises.createNormalNoise` 调用的是 `NormalNoise.createLegacy`（混淆名 `b`），
    其 `PerlinNoise` **不走**顺序消费随机数的路径，而是：
    `random.forkPositional()`（连取两个 `nextLong`）→ 每个非零振幅八度用
    `fromHashOf("octave_" + (firstOctave+i))` 派生独立随机流。`fromHashOf` 的
    算法为 `MD5(name)` 前 16 字节按大端拆成两个 int64，再与工厂种子的 lo/hi
    分别异或（`RandomSupport.seedFromHashOf` + `Hashing.md5()`）。
    这意味着**噪声实例的创建顺序不影响结果**，可以安全地按需惰性构造。
    新增 `NewPerlinNoiseLegacy` / `NewNormalNoiseLegacy` /
    `Xoroshiro.ForkPositional` / `positionalRandomFactory.FromHashOf`。
    测试：`TestNormalNoiseLegacyMatchesVanilla`（取值）、
    `TestNormalNoiseLegacyStateMatchesVanilla`（逐八度 xo/yo/zo 派生链）。
  - [x] **阶段 3a：密度函数求值器（标量子集）已完成**：新增
    `internal/world/vanilla/density.go`，按 1.21.11 字节码逐类核对后实现
    `constant`、内置坐标 `x/y/z`、`add`/`mul`/`min`/`max`、
    `abs`/`square`/`cube`/`half_negative`/`quarter_negative`/`invert`/`squeeze`、
    `clamp`、`y_clamped_gradient`（`Mth.clampedMap`）、`noise`（
    `noise(x*xz, y*y, z*xz)`）、`shifted_noise`（坐标先乘缩放再加位移）、
    `shift`/`shift_a`/`shift_b`（`noise(p*0.25)*4`，三种只差坐标取法：
    `(x,y,z)`/`(x,0,z)`/`(z,x,0)`）、`weird_scaled_sampler`（
    `scale*|noise(p/scale)|`，`type_1`/`type_2` 的分段映射按 `eux$a` 的
    `a`/`b` 两个静态方法抄写）、`range_choice`（上界开区间）、
    `blend_alpha`=1、`blend_offset`=0、`blend_density` 恒等、四种 cache 节点直通。
    未实现的节点（`spline`/`interpolated`/`find_top_surface`/`old_blended_noise`/
    `end_islands`）**显式 panic**，不静默返回 0。
    顺带修正 `shift` 节点的参数类型：它与 `shift_a`/`shift_b` 一样是噪声参数名，
    不是密度函数引用。
    测试：`TestEvaluateArithmetic`、`TestEvaluateBuiltinCoordinates`、
    `TestYClampedGradient`、`TestShiftNoiseNodes`、`TestNoiseAndShiftedNoise`、
    `TestWeirdScaledSampler`、`TestRangeChoiceUpperBoundExclusive`、
    `TestUnsupportedNodePanics`、`TestOverworldRouterUnsupportedTypes`
    （后者枚举 overworld 路由树里仍未覆盖的节点类型，作为下一批清单）。
  - [x] **阶段 3b：spline（CubicSpline）已完成**：按 `bfe$e` 字节码复刻
    `CubicSpline.Multipoint.apply`：区间外按端点导数线性外推
    （`value + derivative*(f-location)`，导数为 0 时取端点值），区间内为
    `lerp(t, y0, y1) + t*(1-t)*lerp(t, a, b)`，其中
    `a = d0*(loc1-loc0) - (y1-y0)`、`b = -d1*(loc1-loc0) + (y1-y0)`。
    整条链路保持**单精度**：坐标密度函数先求值成 double 再窄化成 float，
    样条内部与嵌套子样条都用 float 运算，最后才转回 double（与 `euk$x` 一致）。
    控制点解析新增 `derivative`；**未实现**原版的缺省导数补全（省略 derivative
    时直接报数据校验错误）——1.21.11 原版数据 54 个控制点全部显式给出。
    测试：`TestSplineSinglePoint`、`TestSplineInterpolation`（区间内插值 +
    两端线性外推）、`TestSplineNested`（嵌套子样条）。
  - [x] **阶段 3c：old_blended_noise（BlendedNoise）已完成**：`minecraft:overworld/
    base_3d_noise` 用的就是它。按 `fkb` 字节码复刻：
    - 三个 legacy `PerlinNoise`：`min_limit`/`max_limit` 为八度 `-15..0`（16 个振幅 1），
      `main` 为 `-7..0`（8 个振幅 1）；随机源是 `RandomState` 给的
      `positional.fromHashOf("minecraft:terrain")`（由 `eve$a` 的 visitor 重建），
      所以**与世界种子相关**；
    - 采样：先由 `main` 的 8 个八度算出 `mix=(v/10+1)/2`；再对 16 个八度分别累加
      `min_limit`（`mix<1` 时）与 `max_limit`（`mix>0` 时），最后
      `clampedLerp(mix, min/512, max/512)/128`；
    - 新增 `ImprovedNoise.NoiseSmeared`（原版 5 参数 `noise(x,y,z,yScale,yMax)`）：
      沿 y 按 `floor(bound/yScale + 1e-7)*yScale` 量化后取角点，但插值系数仍用未
      量化的 y 小数部分；`yScale==0` 时退化为普通采样（有专门测试）。
    字段 `o`（= `min_limit.valueFactor(k+2)`）就是 `maxValue()`，`minValue()` 是其相反数。
    测试：`TestOldBlendedNoiseParameters`、`TestNoiseSmearedWithZeroScale`、
    `TestBlendedNoiseDeterministic`（同种子逐点一致、不同种子不同、取值不超过上界）。
  - [ ] **阶段 3d：插值网格 / 顶面搜索**：`interpolated`（`NoiseChunk` 的 4×8×4
    网格与三线性插值）、`find_top_surface`。这两个是目前 overworld 路由树里
    仅剩的未实现类型（`TestOverworldRouterUnsupportedTypes` 会列出）。
  - [x] **阶段 3d：interpolated 与 find_top_surface 已完成**：
    - `interpolated` 按 `NoiseChunk.NoiseInterpolator` 语义实现：把子节点在
      `cellWidth×cellHeight×cellWidth` 的单元格角点上采样，再用方块在单元格内的
      相对偏移做**线性**三线性插值（`Mth.lerp2` 的嵌套顺序：x 最内、y 次之、z 最外）。
      单元格尺寸取自噪声设置的 `noise.size_horizontal/size_vertical` × 4
      （overworld 为 4×8），新增 `NewEvaluatorWithCells`；
    - `find_top_surface` 按 `euk$j` 实现：先 `floor(upper_bound/cell_height)*cell_height`
      对齐，再按 `cell_height` 步长向下找第一个 `density > 0` 的高度，找不到返回
      `lower_bound`；
    - 顺带修正 `find_top_surface` 的 `lower_bound`：它在 JSON 里是**整数**参数，
      原先被当成密度函数子节点。
    测试：`TestOverworldCellSize`、`TestInterpolatedReproducesLinearFunctions`
    （对 x/y/z 线性函数逐点还原）、`TestInterpolatedConstant`、
    `TestFindTopSurface`（正常/全实心/全空/上界低于下界）、`TestFloorDiv`。
    **`TestOverworldRouterEvaluatesEndToEnd` 现已通过**：overworld 路由表的每一项
    都能在多个坐标上求值且结果有限——密度函数这一层（阶段 3 求值部分）全部完成。
    已知限制：`interpolated` 目前是逐点重算 8 个角点（语义正确但比原版的网格复用慢），
    区块级填充时会另做一个"一次算一整格"的采样器。
  - [~] **阶段 3e：区块级地形填充（进行中）**：
    - [x] 新增 `Router`（`router.go`）：把 `noise_router` 的每一项解析成密度函数图，
      并按世界种子绑定求值器；提供 `Field(name, ctx)`、`FinalDensity(ctx)`、
      `Solid(x,y,z)`（= `final_density > 0`）。
    - [x] `interpolated` 加了两级缓存（lattice 角点 + 整格 8 角点），
      同一格内 128 个方块只查一次表；`ResetInterpolationCache()` 每区块调用。
    - [x] 测试：`TestRouterFieldsEvaluable`、`TestRouterSeedAffectsFinalDensity`、
      `TestRouterSolidMatchesDensitySign`、`TestRouterHasSurface`（典型地形存在
      上实下空的过渡，证明 spline/offset/factor/jaggedness 链路在工作）、
      `TestInterpolationCacheReducesWork`、`TestResetInterpolationCacheKeepsResults`。
    - [x] 区块级 dense lattice 填充器（`chunk.go`）：每个 `interpolated` 节点按后序
      顺序一次性算出整块 lattice（x/z 各 5 点、y 49 点），之后逐方块只做索引取值 +
      三线性插值。`TestChunkSamplerMatchesPointwise` / `TestChunkSamplerAcrossCells`
      验证它与逐点求值**逐位相同**（含单元格边界）。
    - [x] `flat_cache` / `cache_2d` 按原版语义实现为**按列缓存**（`euu$g` 用 (x,z) 索引数组、
      `euu$d` 缓存最后一次 (x,z)），同一列 384 个方块只算一次；
      `TestFlatCacheIsColumnConstant` 验证子节点确实与 y 无关。
    - [ ] **已知性能问题**：`BenchmarkChunkSamplerFillChunk` 实测 ≈ 20 ms/区块
      （≈788 KB）。按接列缓存后的 profile，剩余开销主要是每区块的 lattice 计算
      （5 个 interpolated 节点 × 1225 点 × 子树求值，其中 `PerlinNoise.Value`
      与 `evaluateSpline` 占大头），以及 `cache_once`/`cache_all_in_cell` 目前
      仍是直通（其粒度绑在 `NoiseChunk` 的欧值格 `euu.F` 上，语义待字节码确认）。
    - [ ] 含水层（aquifer barrier/floodedness/spread/lava）、`sloped_cheese`
      压缩与逐方块 stone/deepslate/water/air 判定。**类映射已定位**（官方映射文件
      丢失后按结构特征重新找到）：`euc` = `Aquifer` 接口
      （`create(NoiseChunk, ChunkPos, NoiseRouter, PositionalRandomFactory, minY, height,
      FluidPicker)`、`computeSubstance(FunctionContext, double) -> BlockState`、
      `shouldScheduleFluidUpdate()`）；`euc$c` = `NoiseBasedAquifer`
      （流体格网格 `FluidStatus[]` + `long[]` 键、压力插值、`getAquiferStatus`）；
      `euc$1` = 禁用版；`euc$a` = `FluidPicker`；`euc$b` = `FluidStatus`。
      下一步要读 `euc$c.a(euj$b, double)`（computeSubstance）与
      `a(int,int)`（压力插值）、`b(int,int,int)`（取格状态）等约 15 个私有方法。
      **已完成网格几何部分**（`aquifer.go`）：格尺寸
      （区块 → 3×35×3 = 315 格）、索引公式
      `((y-K)*sizeZ + (z-L))*sizeX + (x-J)`、压力函数
      `1 - (toY-fromY)/25`、方块↔格坐标换算，均有单元测试
      （`TestAquiferGridForChunk`/`TestAquiferGridIndex`/`TestAquiferPressure`/
      `TestAquiferGridRoundTrip`）。**未完成**：`computeSubstance`、
      `getFluidStatus`（`b(int,int,int)`，含 barrier 噪声与格缓存）、
      全局 `FluidPicker`、以及与 `ChunkSampler` 的接线。
  - [ ] **阶段 4：表面规则、雕刻器与地物**：overworld `surface_rule` 树、
    cave/canyon 雕刻器、矿物/树木/植被/lakes 地物与噪声群系源
    （`multi_noise_biome_source_parameter_list/overworld` 已随阶段 1 提取）；
  - [ ] **阶段 5：接线与验证**：以配置开关切换生成器（保留旧生成器供回退）、
    与官方服务端同种子区块逐方块对比（区域文件解码）、benchmark 与文档，
    通过后才把 default 切到原版生成器。

- [ ] 完整移动物理（碰撞形状/逐轴碰撞/潜行边缘保护/悬空上升限制/游泳/台阶已完成；
  余下：服务端重力模拟与客户端预测回滚）

- [ ] 特殊界面容器（讲台/织布机/制图台等）：其余处理型容器已完成
  （熔炉族/酿造台/切石机/锻造台/铁砧/附魔台/砂轮机/堆肥桶/发射器/投掷器）

- [ ] 其他配方类型（特殊配方（旗帜图案、烟火、染色等）、crafting_special_*）

- [ ] 更多生物（末地/下界扩展、幼年变体、矿车/船等实体载具）

- [ ] 光照引擎（天空光/方块光传播与增量更新、Light Update 下发；
  当前区块固定全亮，刷怪只看昼夜不听亮度）
  实施前提与工作量评估（尚未动工，未验证）：需要
  （1）逐方块状态的光照属性表（`emitLight`/`filterLight`）——官方 `--reports`
  的 `blocks.json` 不含该数据，需从客户端代码行为或第三方数据（如 minecraft-data）
  另建生成管线并校验；
  （2）`Chunk` 增加逐 section 的天空光/方块光半字节数组（每区块约 9 个非空
  section × 2 × 2 KiB ≈ 36 KiB，需惰性分配，会明显改变当前 ≈9 KiB/区块的存储量级）；
  （3）光引擎（队列式 BFS 增删光、天空光按列初始化 + 水平低减 1、跨区块边界传播）
  与方块变更（放置/破坏/流体/活塞/爆炸）的联动；
  （4）协议侧：区块数据改为携带真实光数组（含掩码），方块变更时按原版发送
  Light Update（仅变化的 section），并重发受影响的邻居区块；
  （5）性能预算与 benchmark（光照更新需限流，不能阻塞 tick），以及真实客户端验证。
  建议作为独立批次实施，不要与行为修复混在一起。

- [ ] 真实网络环境的负载与长时间稳定性测试（工具与单机 loopback 基线已有，
  见 PERFORMANCE.md 3.7、3.13、3.18）

- [ ] **TNT 爆炸后客户端断线（未定位）**：用户报告“TNT 点燃爆炸后丢失连接，
  提示网络协议错误”。已用 1.21.11 客户端 jar + 客户端映射（`client.txt`）逐字节
  核对爆炸路径上的全部报文，**未发现编码缺陷**：
  - `Explode`（0x24）：客户端 `ClientboundExplodePacket` 的 7 个字段顺序与类型
    完全一致（Vec3、f32 power、**i32** blockCount、optional<Vec3>、
    `ParticleTypes.STREAM_CODEC`、`Holder<SoundEvent>`、`List<ExplosionParticleInfo>`），
    已加回归测试 `TestEncodeExplosionLayout`；
  - `Block Update`（0x08）、`Sound`（0x73）、`Set Entity Motion`（0x63，
    含 `LpVec3` 位布局与客户端 `xe` 类一致）、`Remove Entities`（0x4B，
    VarInt 数组）、`Add Entity`（0x01，字段含三个角度字节）、
    `Set Entity Data`（0x61）、`Entity Position Sync`（0x23）、`Respawn`（0x50，
    含 1.21.9+ 新增的 `seaLevel` 字段）字段与 ID 均与客户端一致；
  - 未验证/待排查方向：需要客户端侧完整断线文本或服务器日志（是否是 tick
    goroutine panic 导致连接被关闭、是否只在特定方块被炸毁时出现）。
    （原先记录的 “未发送 Player Combat Kill（0x42）” 缺口已在本批补齐，
    见下方“玩家死亡界面与雷暴闪电”条目。）

## 已知限制

- 注册表/方块/物品/食物/堆叠上限数据由 `tools/genregistries` 生成（官方客户端/服务端 jar 与
  数据生成器报告）；更换 Minecraft 版本时必须重新生成

- 非同步世界生成注册表（worldgen/structure 等）的标签不发送，依赖客户端本地数据包

- 区块负载为 gmcs 自定义格式：区域文件框架（头部、扇区、zlib）与原版一致，
  但负载不是原版 NBT（含自有的方块实体槽位编码），不能与原版服务器互读

- 区块光照固定全亮（无光照引擎）；heightmap 已随区块发送（WORLD_SURFACE/MOTION_BLOCKING）

- 聊天签名：服务器按原版校验消息签名与消息确认窗口（enforce-secure-profile，
  正版模式下默认开启），但**不校验会话公钥的 Mojang 签名**（gmcs 没有 Mojang 的
  会话服务公钥，原版会校验 `chat_session_update` 的 `KeySignature`）；
  `ServerboundChatCommandSigned` 的命令参数签名也未校验；
  实现依据 1.21.11 服务端字节码逐条移植（含客户端 `LastSeenMessagesTracker`），
  但**尚未与真实正版客户端进行在线对测**

- 玩家数据为 gmcs 自定义 players.json（非原版 playerdata）：退出/关闭与每 30 秒周期保存、
  死亡状态、经验与氧量/着火/饥饿疲劳度/饥饿计时持久化；强杀（SIGKILL）或崩溃
  最坏丢失约一个保存周期的进度。选中的快捷栏槽位已持久化（`selected_slot`），
  进入世界/跨维度传送/重生后发送 `set_held_slot` 同步客户端；末影珍珠冷却、
  属性修饰符、药水效果等尚未保存

- 方块交互：挖掘系统已按原版实现（见上“完整挖掘系统”），仍存差异：
  效率/水下速掘附魔已实现；急迫/挖掘疲劳等状态效果未实现（无药水效果系统）；
  精准采集/时运/剪刀与概率掉落已实现，作物（小麦/胡萝卜/马铃薯/甜菜根/
  下界疣/甜浆果丛/火把花/可可豆/瓜类茎/瓶子草）与海泡菜已按官方战利品表
  实现，双格植物破坏任一半只结算一次并移除另一半；仍未覆盖生物头颅等
  极少见掉落，爆炸仍无掉落；
  无出生点保护（mayInteract）与冒险模式 mayBuild；
  放置仅支持普通方块物品（带数据组件的物品不支持）；
  放置形态（台阶/半砖/柱状/朝向方块/活板门/梯子/火把/拉杆/按钮/门/栅栏门）
  已按原版实现（见上“方块放置形态批次”），但仍存差异：台阶不做邻居形状更新
  （shape 恒为 straight，拐角台阶客户端显示为直台阶）、含水状态一律 false；
  门的铰链已按原版 `DoorBlock.getHinge` 实现（邻居坚固面/相邻下半格门 + 点击
  位置回退），放置位置（或上方一格）有红石信号时直接以 open=true、powered=true
  放置；床/告示牌的放置朝向尚未实现；
  可开合方块已响应红石信号（门/活板门/栅栏门，铁门只能由红石开合）

- 容器限制：窗口距离已按原版实现（眼睛位置到方块碰撞盒 < 方块交互距离属性
  4.5 + 4.0 = 8.5，即 `Container.stillValidBlockEntity` 与
  `AbstractContainerMenu.stillValid` 的 `isWithinBlockInteractionRange(pos, 4.0)`）；
  装备槽已按 `minecraft:equippable` 组件校验（见上）；绑定诅咒已按
  `ArmorSlot.mayPickup` 阻止生存/冒险模式取出；生物专用槽
  （body/saddle：马铠/狼铠/挽具/鞍）未实现；自定义名称
  （custom_name）、耐久（damage）、铁砧累积惩罚（repair_cost）与附魔
  （物品 enchantments 13、附魔书 stored_enchantments 41）随槽位与持久化保留，其余物品组件不随槽位发送；
  酿造台槽位规则与快速移动（Shift）均已按原版实现（快速移动见上“Shift 快速移动对齐原版”）

- 漏斗传输限制：不实现实体容器（漏斗矿车/箱子矿车/箱子船）与尚未实现的处理型容器
  （发射器/投掷器等）；掉落物收集顺序按实体 ID 升序（原版按区块
  实体列表顺序，两者都是实现细节，但确定性的来源不同）；漏斗注册为“新加载区块
  增量注册 + 每 600 tick 全量扫描兜底”，非交互路径（若将来加入命令直接写方块）
  最坏延迟 30 秒生效

- 合成配方为数据驱动（官方 recipe JSON，含 smithing_transform 与 smithing_trim），
  但跳过了 transmute/special/decorated_pot 配方类型（见上“其他配方类型”）；
  燃料表为按物品/标签显式列出的数值表（与原版 `FuelValues` 一致；原版燃料表
  内置在 Java 代码中，无法从 jar 数据生成）；配方书显示数据由服务端预编码共享

- 配方书为“全部解锁”简化：进入世界即下发全部配方显示条目（无进度/解锁机制），
  不发送 craftingRequirements（客户端“可否合成”高亮与过滤可能不准）、
  不发送通知/高亮标志；Place Recipe 仅支持合成配方（烹饪/锻造条目不进入
  合成格放置流程），语义已对齐原版 1.21.2+（Shift 填充原料、原料不足时清空
  并发送 Place Ghost Recipe）

- 环境效果限制：无难度设置（火焰蔓延概率
  按原版默认 Easy、难度 ID 1；原版 Hard 会显著提高蔓延概率）、无游戏规则
  （火焰按原版默认 `fire_spread_radius_around_player=128` 只在玩家附近推进）、
  含水方块状态未建模（原版含水方块不参与燃烧）；着火不因细雪/药水清除；
  阳光燃烧用“头顶是否有碰撞方块”近似原版天空光（因此玻璃屋顶下的亡灵也会
  着火，与原版不同）；
  火/水/岩浆判定按实体碰撞盒覆盖的方块（收缩 1e-7，与原版 `checkInsideBlocks`
  一致），但方块内部的水位（level 1/9 等流体高度）不建模，按“方块重叠”近似；
  水/岩浆蔓延与衰减、水岩浆反应、水桶/岩浆桶使用与回收已实现（见上“流体模拟”），
  掉落物会被水流推动（对照原版 `getFlow` 与实体推动公式，见上）；
  玩家与生物不由服务端推动（玩家由客户端移动负责，生物不建模水流漂流）、
  下界水蒸发以外无其他流体交互；
  岩浆伤害为 4 点/0.5 秒（受受伤无敌帧限制），死亡消息按原版（岩浆/着火/溺水/岩浆块）

- 农业限制（见上“农业系统批次”）：无光照引擎，作物按“上方整列没有方块 =
  天光 15，否则 0”近似判定，因此**只有露天农田**可以种植与生长（火把照明、
  玻璃温室、树荫下都不生长）；原版 `CropBlock.canSurvive` 的邻块变化复检
  未实现（作物不会因上方被盖住而破坏）；雨天浇水已实现（见“天气系统批次”）；
  随机刻作用于内存中所有已加载区块（原版仅模拟距离内，表现为离开后
  视距+4 范围内的作物仍生长到 15 秒卸载宽限结束）；踩踏农田只对玩家判定
  （生物不跟踪下落距离）；pitcher_pod（双格作物）、西瓜/南瓜茎结果、
  可可豆（需丛林原木面）未实现；种植只接受“目标位置为空气”
  （原版可替换方块如草/雪层未覆盖）

- 天气限制（见上“天气系统批次”）：无群系降水类型与温度建模（主世界所有群系按
  降雨处理，没有雪原降雪/沙漠无雨）；闪电已实现（雷暴按原版概率落雷、点火、
  对范围内实体造成 5 点伤害并点燃 8 秒），但未实现避雷针引导、闪电使苦力怕
  充能、雷击不破坏方块（原版也不破坏）、不把猪变僵尸猪灵；
  炼药锅不积水、雪层不堆积；天空暴露用
  “有碰撞形状的方块”近似 `Heightmap.MOTION_BLOCKING`；无游戏规则
  `doWeatherCycle`（始终推进，需关闭时只能改代码/命令）；`/weather` 的
  duration 按整数秒解析（原版 TimeArgument 支持 `1d`/`30s` 等后缀）

- 附魔数值效果已实现（护甲减伤、锋利/亡灵杀手/节肢杀手、击退/火焰附加/
  抢夺、效率/水下速掘、保护类、摔落保护、呼吸、不灭、经验修补、精准采集/
  时运、绑定/消失诅咒）；弓附魔（力量/冲击/火矢/无限）与盾牌格挡已实现；
  仍缺：横扫之刃、弩/三叉戟相关附魔（无对应武器系统）、附魔统计与钓鱼等
  其他附魔途径；物品耐久由挖掘/护甲受伤/弓与盾牌使用消耗；
  远程武器与盾牌：弓按“释放时经过时间”计算蓄力（未建模客户端拉弓动作与
  移动减速）；箭矢不保留在世界上供拾取、不实现箭矢暴击与药水箭；
  弩/三叉戟未实现；盾牌不实现斧头破盾、禁用 tick 与击退减免，
  格挡方向仅按水平视线判定；
  堆肥桶的 level 7 → 8 定时转换本身不写入区块；区块加载时会立即扫描
  level 7 并重新登记 20 tick 转换，另有 600 tick 全量扫描兜底

- 生物寻路为地表模型：不考虑头顶净空（不能爬楼梯井/穿过 1 格高洞）、
  不在水中寻路、无对角线移动；寻路失败时回退直线/随机侧移；

- 玩家实体同步范围为 64 格（进入 64 格补发、离开 80 格移除，见上“实体跟踪”），
  移动与朝向仍只广播给 64 格内的玩家（64–80 格之间的实体在下一次移除前位置冻结）；
  皮肤层元数据（Avatar 索引 16）已随实体生成下发，但主手、额外生命/分数等其他
  玩家元数据未发送；死亡玩家的实体不会隐藏（跟随原版玩家死亡后实体保留的行为）

- 命令参数不实现原版 Brigadier 语法（引号、目标选择器等），仅支持基础分词；
  补全候选为命令名、`/gamemode`/`/time`/`/dimension` 参数、`/kick`/`/tp`/`/clear`/`/kill`
  与聊天中的在线玩家名

- 权限为四级等级 + 节点两级：`ops` 项支持 `name`（等价 4 级）或 `name:level`（1–4），
  `permissions` 可按玩家授予 `gmcs.command.*` 节点；无权限插件 API 兼容

- 摔落伤害由服务器端支撑面检测驱动（脚底碰撞形状顶面，不依赖客户端标志），
  落水/游泳中断下落；减伤按提供支撑面的方块计算；
  逐 tick 速度上限为原版阈值（10×√tick，闲置超过 1 秒按 1 tick 计），
  小幅连续加速作弊仍可能通过；悬空上升限制只覆盖上升（悬停式飞行、水平漂移
  不检测）；无缓降/滑翔/鞘翅与实体堆叠减免

- 正版模式拒绝登录时会区分“会话服务器不可用”（独立提示）与非 204 拒绝
  （"Failed to verify username!"），其余错误统一提示；不验证玩家属性签名（透传会话服务器返回的属性）

- 区块发送为同步批量并带 Chunk Batch 标记：进入世界/跨区块迁移的区块突发以
  Chunk Batch Start/Finished 包裹；区块生成已异步预生成（8 worker + 请求去重），
  但发送仍等原因；已接收并记录客户端 Chunk Batch Received 速率，尚未据此分帧限流；
  区块内存卸载为 5 秒周期扫描（非即时），卸载脏区块时同步写盘；
  所有玩家退出时立即释放全部区块、帧缓存与区域文件句柄（fd 交 GC 关闭）

- 掉落物物理对照原版 `ItemEntity`：逐轴 AABB 碰撞、重力 0.04、空中/落地阻尼
  （水平 0.98 或 脚下方块 slipperiness×0.98，冰面更滑；垂直恒 ×0.98）、落地反弹
  （垂直 ×-0.5）、水中水平 ×0.99 + 缓慢上浮（5.0E-4，阈值 0.06，不再减重力）
  与水流推动；爆炸会击飞掉落物（不销毁物品）；合并检查间隔对齐原版
  （跨方块边界移动时每 2 tick、静止时每 40 tick）；速度、存活时间与拾取延迟随
  entities.json 保存（原版 `ItemEntity` 保存 Age/PickupDelay，速度属于实体基础字段）；
  拾取只区分“挖掘（10 tick）”与“玩家/死亡（40 tick）”两类来源，不保存原版的
  Thrower（仅影响统计）；不实现“摩擦对实体数据的同步”（客户端自行弹动，无需同步）

- 碰撞形状覆盖常见方块（完整方块/半砖/台阶/栅栏/墙/栅栏门/门/活板门/雪层/仙人掌/
  蛋糕/耕地/床/箱子/压力板/按钮/营火/切石机/酿造台/讲台/灯笼/铃等）；花盆、蜡烛、蘑菇、
  可可豆、甘蔗、竹子、海龟蛋/嗅探蛋、货架、铜傀儡雕像、大型垂滴叶、滴水石锥、末地烛、
  链等少数装饰方块按**无碰撞**或略小的近似盒处理（有意偏小：避免服务器碰撞盒大于客户端
  导致移动回拉；表现为可穿过这类方块）；楼梯转角（outer/inner）朝向与门/活板门开合的
  铰链侧为约定实现，待真实客户端验证

- 地形不是原版算法：包含噪声洞穴（阈值已按原版观感调校，地下空腔约 1–7%）、矿物、
  宽河道与缓坡河岸、跨区块树冠与 24 种群系（温度/湿度/海拔近似划分，与原版群系
  边界无关，垂直方向不变），但没有原版噪声域扭曲、含水层、结构与地物
  （村庄/要塞/废弃矿坑等），树木形状是简化版（树冠层数与裁角近似原版）；
  三维度中下界/末地同样为简化生成器；海平面 62 为近似值；
  更换 `world_seed` 不会重新生成已有存档中的区块（新旧地形交界处会有接缝）。
  原版生成器的移植进度见“待完成”顶部的分阶段计划（当前仅完成阶段 1：
  数据管线与密度函数图校验，地形仍由自研生成器产生）

- 生物共十二种（僵尸/骷髅/苦力怕/蜘蛛/末影人；牛/猪/羊；鳕鱼/鲑鱼/热带鱼/鱿鱼），
  AI 为简化追击/游荡（敌对生物含 A* 地表寻路与直线追击、末影人瞬移为随机尝试；
  被动生物游荡，受击后按 PanicGoal 逃跑 5 秒；水生生物只在水体内三维游动，
  不会跳上岸）；
  敌对生物看昼夜时间（被动生物仅白天地表草地）、不看亮度（无光照引擎，洞穴内白天不刷怪）；
  僵尸/骷髅在白天阳光下持续着火（用“头顶是否有遮挡方块”近似天空光）；
  海洋生物只在海洋/河流水域生成（独立配额），不含海豚/守卫者/溺尸等；
  爆炸销毁方块无掉落、不触发红石/实体连锁（但会伤害范围内生物并击飞掉落物）；
  头部朝向：非移动状态的生物会看向 8 格内的最近玩家（原版 Monster 的
  LookAtPlayerGoal：2% 触发概率、注视 40–79 tick、10°/tick 转向）；移动中
  仍面向行走方向（原版用头部/身体双角度细分）；生物持久化为 gmcs 自定义 entities.json（按维度分文件，
  位置/生命/朝向、掉落物与飞行中的箭矢（含速度/伤害）、经验球
  （Value/Count/Age/速度，对照原版 `ExperienceOrb` 的 Age/Value 存档）

- 玩家装备已同步（Set Equipment 0x64：主手/副手/盔甲，64 格内变更广播）；
  生物的盔甲与手持物品未实现（gmcs 生物不会穿戴装备）

- 红石为高保真但非逐 tick 完全一致：组件语义按原版反编译实现（活塞 12 格限制、
  粘性链、比较器模式与容器读取、火把熔断、中继器锁定、红石线连接指向规则、
  方块强充能、压力板检测盒与加权信号公式）；差异点：活塞无移动动画（原版
  MOVING_PISTON 移动方块实体，本实现同 tick 落位，依赖“移动中”状态的红石时序不同，
  收回时不推动实体；推动后失去支撑的方块（如方块上的火把/告示牌）不自动掉落，
  属于方块更新模型的既有简化）、计划刻无优先级（同 tick 内结算顺序可能不同于原版）、
  比较器不读取物品展示框（gmcs 无展示框实体）、绊线/阳光传感器等其他电源未实现；
  压力板不参与“漏斗/发射器读取容器”类交互（无此类需求）；实体进入方块扫描
  仅在实体位置推进时触发（与原版实体移动路径一致）

- 维度切换有 `/dimension` 命令与下界/末地传送门；传送门为简化实现：下界回程按半径 16 格搜索已有门（非坐标精确匹配）、
  末地出口平台固定第一次生成于 (100,50,0)（无末地折跃门返回）、传送门方块不随框架部分破坏逐块熄灭（破坏框架时清除附近传送门方块）

- 伤害数值集中在 `mobKinds` 表（僵尸/蜘蛛近战 2 点、骷髅箭矢 2 点、苦力怕爆炸
  最多 24 点并线性衰减），玩家 4 点；玩家护甲按官方物品数据减伤；击杀经验按原版
  拆分表以经验球掉落，经验修补会消耗经验修复物品、经验球随 entities.json 持久化；
  无药水/状态效果；无难度设置（按普通难度结算饥饿）

- 饥饿系统按原版规则实现（疲劳度、饱食度优先、饥饿伤害）；进食有 1.6 秒过程（切换槽位/松开使用键中断），
  未实现金苹果等特殊食物效果，也无饮用药水

- 内存管理：区块在距所有玩家超过 视距+4 且**连续 15 秒**后才移出内存
  （防抖：玩家在卸载边界来回移动不会反复加载/卸载/写盘）；已打开但无玩家的
  维度空闲 30 秒后释放全部区块；服务器没有任何玩家时立即释放全部区块。
  以上释放都会先保存脏区块；重新接近时按需从磁盘恢复或重新生成。
  区块预取范围为视距+1（跨区块发送无需等待磁盘/生成器），同一区块的并发
  编码请求会合并为一次（singleflight）
