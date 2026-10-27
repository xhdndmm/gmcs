//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"
)

// TestDensityMatchesOfficialSnapshot 用官方服务端（1.21.11，种子 4321）在
// "密度探针"配置下生成的区块做逐方块回归：
//
//	默认方块 = 石头、默认流体 = 空气、sea_level = -65、关闭含水层与表面规则，
//	并且 noise_router.final_density 保持原版。因此官方世界的每个方块就等于
//	final_density 的符号（stone = 密度 > 0），可以用来校验整条密度函数图，
//	包括 base_3d_noise（BlendedNoise）、三线性插值与各种映射节点。
//
// 快照来自 /tmp/mcserver/.../density/region/r.0.0.mca 的区块 (0,0)，
// 每个比特对应一个方块（1 = 石头），按 (y, z, x) 顺序展开。
func TestDensityMatchesOfficialSnapshot(t *testing.T) {
	want, err := readDensitySnapshot("testdata/official_density_0_0.bin.gz")
	if err != nil {
		t.Fatalf("读取官方快照失败：%v", err)
	}
	data, err := Load()
	if err != nil {
		t.Fatalf("加载数据失败：%v", err)
	}
	if err := applyDensityProbeForTest(data); err != nil {
		t.Fatalf("构造密度探针设置失败：%v", err)
	}
	router, err := NewRouter(data, "minecraft:overworld", 4321)
	if err != nil {
		t.Fatalf("构造路由表失败：%v", err)
	}
	setting := router.Setting()
	sampler := router.NewChunkSampler(0, 0)
	total, bad := 0, 0
	firstBad := ""
	for y := setting.MinY; y < setting.MinY+setting.Height; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				index := ((y - setting.MinY) * 256) + (z * 16) + x
				wantSolid := want[index/8]&(1<<(index%8)) != 0
				gotSolid := sampler.Density(x, y, z) > 0
				total++
				if wantSolid != gotSolid {
					bad++
					if firstBad == "" {
						firstBad = fmt.Sprintf("(%d,%d,%d) 官方=%v gmcs=%v", x, y, z, wantSolid, gotSolid)
					}
				}
			}
		}
	}
	if bad != 0 {
		t.Fatalf("密度符号与官方不一致 %d / %d，首个差异 %s", bad, total, firstBad)
	}
}

// applyDensityProbeForTest 复刻 tools/vancmp -density 对噪声设置的改动。
func applyDensityProbeForTest(data *Data) error {
	const name = "minecraft:overworld"
	raw, ok := data.NoiseSettings[name]
	if !ok {
		return fmt.Errorf("缺少 %s", name)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	decoded["aquifers_enabled"] = false
	decoded["ore_veins_enabled"] = false
	decoded["sea_level"] = -65
	decoded["default_block"] = map[string]any{"Name": "minecraft:stone"}
	decoded["default_fluid"] = map[string]any{"Name": "minecraft:air"}
	decoded["surface_rule"] = map[string]any{
		"type":         "minecraft:block",
		"result_state": map[string]any{"Name": "minecraft:stone"},
	}
	patched, err := json.Marshal(decoded)
	if err != nil {
		return err
	}
	data.NoiseSettings[name] = patched
	return nil
}

// readDensitySnapshot 读取 gzip 压缩的密度符号位图。
func readDensitySnapshot(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}
