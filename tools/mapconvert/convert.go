//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"gmcs/internal/world"
)

type convertOptions struct {
	Source       string
	Destination  string
	Dimension    string
	Seed         int64
	Overwrite    bool
	KeepExisting bool
}

func dimensionDir(root, dimension string, vanilla bool) (string, error) {
	if !vanilla {
		switch dimension {
		case "overworld", "minecraft:overworld":
			return root, nil
		case "the_nether", "nether", "minecraft:the_nether":
			return filepath.Join(root, "the_nether"), nil
		case "the_end", "end", "minecraft:the_end":
			return filepath.Join(root, "the_end"), nil
		}
		return "", fmt.Errorf("未知维度 %q", dimension)
	}
	switch dimension {
	case "overworld", "minecraft:overworld":
		return root, nil
	case "the_nether", "nether", "minecraft:the_nether":
		return filepath.Join(root, "DIM-1"), nil
	case "the_end", "end", "minecraft:the_end":
		return filepath.Join(root, "DIM1"), nil
	}
	return "", fmt.Errorf("未知维度 %q", dimension)
}

func dimensionValue(name string) world.Dimension {
	dim, _ := world.ParseDimension(name)
	return dim
}

func convertGMCS2Vanilla(opts convertOptions) error {
	srcDim, err := dimensionDir(opts.Source, opts.Dimension, false)
	if err != nil {
		return err
	}
	dstDim, err := dimensionDir(opts.Destination, opts.Dimension, true)
	if err != nil {
		return err
	}
	// gmcs 区域文件直接放在维度目录下（与 world.LoadChunk 一致），
	// 原版放在 region/ 子目录。
	srcRegionDir := srcDim
	dstRegionDir := filepath.Join(dstDim, "region")
	regions, err := listRegions(srcRegionDir)
	if err != nil {
		return fmt.Errorf("读取 gmcs 区域目录 %s: %w", srcRegionDir, err)
	}
	if len(regions) == 0 {
		return fmt.Errorf("gmcs 区域目录 %s 中没有 .mca 文件", srcRegionDir)
	}
	dim := dimensionValue(opts.Dimension)
	converted := 0
	for _, region := range regions {
		path := regionFile(srcRegionDir, region[0], region[1])
		payloads, err := readRegion(path)
		if err != nil {
			return err
		}
		out := make(map[int][]byte)
		for slot, payload := range payloads {
			chunk, err := world.DecodeChunkPayload(payload)
			if err != nil {
				return fmt.Errorf("%s 槽位 %d 解析 gmcs 区块失败: %w", path, slot, err)
			}
			root, err := buildVanillaChunk(chunk, dim)
			if err != nil {
				return err
			}
			data, err := encodeNBT(root)
			if err != nil {
				return err
			}
			out[slot] = data
			converted++
		}
		if len(out) == 0 {
			continue
		}
		if !opts.Overwrite {
			if _, err := os.Stat(regionFile(dstRegionDir, region[0], region[1])); err == nil {
				return fmt.Errorf("目标区域文件已存在（使用 -overwrite 覆盖）: %s", regionFile(dstRegionDir, region[0], region[1]))
			}
		}
		if err := writeRegion(regionFile(dstRegionDir, region[0], region[1]), out); err != nil {
			return err
		}
	}
	fmt.Printf("gmcs -> vanilla 完成：%d 个区块，维度 %s\n", converted, opts.Dimension)
	return nil
}

func convertVanilla2GMCS(opts convertOptions) error {
	srcDim, err := dimensionDir(opts.Source, opts.Dimension, true)
	if err != nil {
		return err
	}
	dstDim, err := dimensionDir(opts.Destination, opts.Dimension, false)
	if err != nil {
		return err
	}
	srcRegionDir := filepath.Join(srcDim, "region")
	regions, err := listRegions(srcRegionDir)
	if err != nil {
		return fmt.Errorf("读取原版区域目录 %s: %w", srcRegionDir, err)
	}
	if len(regions) == 0 {
		return fmt.Errorf("原版区域目录 %s 中没有 .mca 文件", srcRegionDir)
	}
	dim := dimensionValue(opts.Dimension)
	converted := 0
	for _, region := range regions {
		path := regionFile(srcRegionDir, region[0], region[1])
		payloads, err := readRegion(path)
		if err != nil {
			return err
		}
		if len(payloads) == 0 {
			continue
		}
		out := make(map[world.ChunkPos][]byte)
		for slot, payload := range payloads {
			root, err := decodeNBT(payload)
			if err != nil {
				return fmt.Errorf("%s 槽位 %d 解析原版 NBT 失败: %w", path, slot, err)
			}
			chunk, err := convertVanillaChunk(root, dim)
			if err != nil {
				return err
			}
			encoded := world.EncodeChunkPayload(chunk)
			compressed, err := world.CompressChunkPayload(encoded)
			if err != nil {
				return err
			}
			x, z := chunkCoords(region[0], region[1], slot)
			out[world.ChunkPos{X: x, Z: z}] = compressed
			converted++
		}
		if len(out) > 0 {
			if err := world.SavePayloads(dstDim, out); err != nil {
				return fmt.Errorf("写入 gmcs 区域 %s: %w", dstDim, err)
			}
		}
	}
	fmt.Printf("vanilla -> gmcs 完成：%d 个区块，维度 %s\n", converted, opts.Dimension)
	return nil
}
