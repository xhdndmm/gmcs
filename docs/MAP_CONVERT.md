# 地图转换工具（mapconvert）

`tools/mapconvert` 用于在 **gmcs 自有区块格式** 与 **Minecraft Java 1.21.11 Anvil 格式** 之间转换区域地图。

> 该工具转换的是区块数据，不是完整存档。`level.dat`、POI、实体文件、结构引用、玩家数据不会复制。

## 构建

```bash
go build -o mapconvert ./tools/mapconvert
# 或直接运行
go run ./tools/mapconvert --help
```

## gmcs -> 原版

```bash
mapconvert gmcs2vanilla \
  -src ./world \
  -dst ./vanilla-world
```

- `-src` 是 gmcs 世界根目录（区块文件位于 `world/r.x.z.mca`）。
- `-dst` 是原版世界根目录，区块写入 `vanilla-world/region/r.x.z.mca`。
- 默认只转换主世界。
- 下界：
  ```bash
  mapconvert gmcs2vanilla -src ./world -dst ./vanilla-world -dimension the_nether
  # 读取 world/the_nether，写入 vanilla-world/DIM-1/region
  ```
- 末地：
  ```bash
  mapconvert gmcs2vanilla -src ./world -dst ./vanilla-world -dimension the_end
  # 读取 world/the_end，写入 vanilla-world/DIM1/region
  ```
- 目标区域文件已存在时默认拒绝覆盖；加 `-overwrite` 覆盖。

如果目标目录没有 `level.dat`，需要先准备一个 1.21.11 原版世界的 `level.dat`（或把转换结果放进已有原版世界）。工具当前不会伪造完整的 `level.dat`/WorldGenSettings。

## 原版 -> gmcs

```bash
mapconvert vanilla2gmcs \
  -src ./vanilla-world \
  -dst ./world
```

- 原版主世界区块在 `vanilla-world/region/`。
- 下界 `vanilla-world/DIM-1/region/` 对应 gmcs `world/the_nether/`。
- 末地 `vanilla-world/DIM1/region/` 对应 gmcs `world/the_end/`。
- 写入 gmcs 时会通过 `world.SavePayloads` 合并到已有区域文件。

## 转换范围与限制

已转换：

- 方块状态（名称 + 属性）与 4096/区块 section。
- 4×4 地表格群系（gmcs 不支持 3D 群系；原版 3D 群系只取地面层）。
- 容器物品、战利品表字段、刷怪笼、告示牌、末地折跃门方块实体字段。
- 区块坐标、section Y、高度图基础字段。

未转换 / 会丢失：

- `level.dat`、WorldGenSettings、时间、天气、种子（gmcs 的种子在 `gmcs.json`）。
- 实体（生物、展示框、掉落物等）。
- POI、区块结构引用、刻计划、光照数据。
- 原版 3D 生物群系和完整 NBT 组件（物品附魔/自定义组件只保留基础数量与 ID）。
- 区块状态 `Status`、blending、below_zero_retrogen 等生成元数据。

因此该工具适合迁移地形/建筑/箱子等基础区块数据，不应被描述为完整存档迁移。
