//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

// mapconvert 是 gmcs 自有区域地图与原版 Anvil 地图之间的双向转换工具。
//
// 用法：
//
//	go run ./tools/mapconvert gmcs2vanilla -src ./world -dst ./vanilla-world
//	go run ./tools/mapconvert vanilla2gmcs -src ./vanilla-world -dst ./world
//
// 默认转换主世界；-dimension the_nether/the_end 可转换下界/末地。
// 转换只处理 region/*.mca 中的区块，不复制 level.dat、POI、实体和结构引用。
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	mode := os.Args[1]
	flags := flag.NewFlagSet(mode, flag.ExitOnError)
	src := flags.String("src", "", "源世界目录")
	dst := flags.String("dst", "", "目标世界目录")
	dimension := flags.String("dimension", "overworld", "维度：overworld / the_nether / the_end")
	seed := flags.Int64("seed", 0, "写入 level.dat 的种子（当前仅保留参数）")
	overwrite := flags.Bool("overwrite", false, "覆盖已存在的目标区域文件")
	if err := flags.Parse(os.Args[2:]); err != nil {
		os.Exit(2)
	}
	if *src == "" || *dst == "" {
		usage()
	}
	opts := convertOptions{
		Source:      *src,
		Destination: *dst,
		Dimension:   *dimension,
		Seed:        *seed,
		Overwrite:   *overwrite,
	}
	var err error
	switch mode {
	case "gmcs2vanilla":
		err = convertGMCS2Vanilla(opts)
	case "vanilla2gmcs":
		err = convertVanilla2GMCS(opts)
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "转换失败:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `用法:
  mapconvert gmcs2vanilla -src <gmcs世界目录> -dst <原版世界目录> [选项]
  mapconvert vanilla2gmcs -src <原版世界目录> -dst <gmcs世界目录> [选项]

选项:
  -dimension overworld|the_nether|the_end   转换维度（默认主世界）
  -overwrite                                覆盖目标区域文件（gmcs2vanilla）
  -seed <int64>                             预留的 level.dat 种子参数

说明:
  - gmcs 区域文件是自有 GMCS 负载；原版是 1.21.11 Anvil NBT。
  - 只转换 region/*.mca，不转换 level.dat、POI、实体文件和结构引用。
  - gmcs 的 3D 群系未保存，转成原版时使用地表格群系复制到 section。
  - 原版生物群系转 gmcs 时只保留地表格 4×4 网格。`)
	os.Exit(1)
}
