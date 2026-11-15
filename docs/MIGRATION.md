# gmcs 与原版服务器迁移指南

本指南只说明 **gmcs 与原版 Minecraft 服务器之间迁移地图** 的流程。

- 目标版本：Minecraft Java Edition 1.21.11（协议 774）。
- 迁移工具：`tools/mapconvert`，详见 [MAP_CONVERT.md](MAP_CONVERT.md)。
- 更换 / 升级 Minecraft 版本的开发流程不在本指南中，见 [CONTRIBUTING.md](CONTRIBUTING.md) 第 3 节。

> **重要：先备份。**
>
> 转换工具只迁移区块地形、方块、基础方块实体与地表格群系。
> 它不迁移 `level.dat`、玩家数据、实体、POI、结构引用、光照和完整物品组件。
> 不要把本工具描述为“完整存档迁移”。

## 1. gmcs → 原版服务器

### 1.1 准备

1. 停止 gmcs 服务器，确认 `world/` 不再被写入：
   ```bash
   # 建议先备份
   cp -a world world.backup
   ```
2. 准备一个 **1.21.11 原版世界目录**，其中应已有 `level.dat`。工具不会伪造
   完整的 `level.dat` / WorldGenSettings；最稳妥的做法是让原版服务器先生成
   一个同版本世界，再把转换后的区域覆盖进去。
3. 构建转换工具：
   ```bash
   go build -o mapconvert ./tools/mapconvert
   ```

### 1.2 转换主世界

gmcs 主世界区块位于 `world/r.x.z.mca`；原版主世界位于
`<vanilla-world>/region/r.x.z.mca`。

```bash
./mapconvert gmcs2vanilla \
  -src ./world \
  -dst ./vanilla-world
```

目标区域文件已存在时默认拒绝覆盖；确认目标世界可以替换后加 `-overwrite`：

```bash
./mapconvert gmcs2vanilla \
  -src ./world \
  -dst ./vanilla-world \
  -overwrite
```

### 1.3 转换下界和末地

gmcs 维度目录：

| 维度 | gmcs 源目录 | 原版目标目录 |
| --- | --- | --- |
| 主世界 | `world/` | `vanilla-world/region/` |
| 下界 | `world/the_nether/` | `vanilla-world/DIM-1/region/` |
| 末地 | `world/the_end/` | `vanilla-world/DIM1/region/` |

```bash
./mapconvert gmcs2vanilla -src ./world -dst ./vanilla-world -dimension the_nether
./mapconvert gmcs2vanilla -src ./world -dst ./vanilla-world -dimension the_end
```

### 1.4 启动原版服务器

确认目标世界中至少存在：

```text
vanilla-world/
├── level.dat
├── region/
│   └── r.x.z.mca
├── DIM-1/region/     # 如果转换了下界
└── DIM1/region/      # 如果转换了末地
```

然后使用 1.21.11 原版服务端启动。若日志出现：

```text
No existing world data, creating new world
```

说明 `level.dat` 缺失或 `level-name` 指向了错误目录，而不是区块转换失败。

### 1.5 gmcs → 原版会丢失什么

- 玩家数据：`players.json` 不会转换为原版 `playerdata/`。
- 实体：gmcs 的生物/掉落物/展示框不会迁移。
- `level.dat`：世界名称、种子、时间、天气、游戏规则等不会自动生成。
- POI、结构引用、刻计划、光照数据。
- gmcs 只保存地表格 4×4 群系；原版 3D 群系会被统一到对应地表格。
- 物品只保留 ID、数量和少量基础组件；附魔、自定义名称等完整 NBT 可能丢失。

## 2. 原版服务器 → gmcs

### 2.1 准备

1. 停止原版服务器，备份原版世界。
2. 准备 gmcs 的 `world_dir`。可以新建空目录，也可以写入已有 gmcs 世界；
   gmcs 会读取已有区块，不会自动重建它们。
3. 构建工具：
   ```bash
   go build -o mapconvert ./tools/mapconvert
   ```

### 2.2 转换主世界

原版主世界区块位于 `<vanilla-world>/region/`：

```bash
./mapconvert vanilla2gmcs \
  -src ./vanilla-world \
  -dst ./gmcs-world
```

转换后的 gmcs 区域文件位于：

```text
gmcs-world/r.x.z.mca
```

### 2.3 转换下界和末地

| 维度 | 原版源目录 | gmcs 目标目录 |
| --- | --- | --- |
| 主世界 | `vanilla-world/region/` | `gmcs-world/` |
| 下界 | `vanilla-world/DIM-1/region/` | `gmcs-world/the_nether/` |
| 末地 | `vanilla-world/DIM1/region/` | `gmcs-world/the_end/` |

```bash
./mapconvert vanilla2gmcs -src ./vanilla-world -dst ./gmcs-world -dimension the_nether
./mapconvert vanilla2gmcs -src ./vanilla-world -dst ./gmcs-world -dimension the_end
```

### 2.4 配置 gmcs

在 `gmcs.json` 中把 `world_dir` 指向转换后的目录，例如：

```json
{
  "world_dir": "gmcs-world"
}
```

注意：

- `world_seed` 只影响 **新生成区块**。已有区块不会被重新生成，因此改种子不会
  重写已转换的地形。
- gmcs 不读取原版 `level.dat`；世界时间、天气等运行时状态从 gmcs 自己的
  `level.json` 开始。
- 原版玩家数据与实体不会自动迁移；如果需要玩家物品/位置，请另行处理
  `playerdata/`，不能直接期望 gmcs 读取。

### 2.5 原版 → gmcs 会丢失什么

- 原版实体、POI、区块结构引用、刻计划、光照数据。
- 原版 3D 生物群系；gmcs 只保留地表格 4×4 群系。
- 原版 `level.dat` 中的世界设置、时间、天气、游戏规则。
- 完整物品组件（附魔、药水、自定义名称等）；转换只保留基础物品 ID/数量。

## 3. 转换工具行为

### 3.1 已转换

- 区块方块状态（名称 + 属性）。
- 24 个 section 的方块数据。
- 容器物品、战利品表字段、刷怪笼、告示牌、末地折跃门基础字段。
- 地表格 4×4 群系。
- 基础高度图字段。
- 区块坐标与 section Y。

### 3.2 不转换

- 玩家、实体、掉落物、载具。
- `level.dat`、POI、结构引用、刻计划、光照。
- 原版 3D 群系。
- 完整 NBT 物品组件和大多数实体/方块实体自定义字段。

### 3.3 重复转换

- `gmcs2vanilla` 默认不覆盖已有目标 `.mca`，使用 `-overwrite` 才替换整个区域。
- `vanilla2gmcs` 使用 gmcs 的区域写入 API，按区块合并到目标区域文件。
- 建议每次迁移都在备份副本上执行；不要直接在唯一存档上反复试验。

## 4. 常见问题

### 4.1 原版提示“没有已有世界数据”

工具不创建 `level.dat`。请从同版本原版世界复制 `level.dat`，或先让原版服务器
生成一个世界，再转换区块到它的 `region/`。

### 4.2 转换后看不到新地形

- 检查 `gmcs.json` 的 `world_dir` 是否指向正确目录。
- 检查维度目录：gmcs 是 `the_nether` / `the_end`，原版是 `DIM-1` / `DIM1`。
- 已存在的区块不会因为 `world_seed` 改变而重建。

### 4.3 箱子、告示牌或刷怪笼内容不对

当前转换器只保留基础方块实体字段。复杂容器组件、实体数据和原版延迟战利品
逻辑可能不完整；请以转换工具报告和 `docs/MAP_CONVERT.md` 的限制为准。

### 4.4 玩家/生物/掉落物没有迁移

这是预期行为。迁移工具只处理区域区块，不处理实体文件、玩家文件与 POI。

### 4.5 回滚

迁移前保留的 `world.backup` 可以直接作为 `world_dir` 使用。不要在没有备份的
情况下删除原版世界目录。

## 5. 迁移验证清单

迁移完成后至少检查：

- [ ] 已停止源服务器并完成备份。
- [ ] 主世界 `region/` 或 gmcs `r.x.z.mca` 已生成。
- [ ] 下界/末地目录映射正确。
- [ ] 原版有版本匹配的 `level.dat`。
- [ ] 启动日志没有区块 NBT / 区块负载解析错误。
- [ ] 游戏内检查出生点、地形、容器、告示牌和刷怪笼。
- [ ] 明确接受未迁移的实体、玩家数据、POI 和 3D 群系。
