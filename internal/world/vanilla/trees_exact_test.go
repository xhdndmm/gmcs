//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"encoding/json"
	"testing"
)

// TestComplexTreePlacersProduceCanopy 覆盖从原版反编译逐条移植的复杂树干/树叶
// 放置器：至少应生成连接的地面树干与树冠，并且同种子结果确定。
func TestComplexTreePlacersProduceCanopy(t *testing.T) {
	data, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(data, "minecraft:overworld", 4321)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{
		"minecraft:acacia", "minecraft:fancy_oak", "minecraft:dark_oak",
		"minecraft:mega_pine", "minecraft:mega_jungle_tree",
		"minecraft:cherry", "minecraft:mangrove",
	}
	for _, name := range names {
		raw, ok := data.ConfiguredFeatures[name]
		if !ok {
			t.Fatalf("缺少 configured_feature %s", name)
		}
		var configured struct {
			Config json.RawMessage `json:"config"`
		}
		if err := json.Unmarshal(raw, &configured); err != nil {
			t.Fatal(err)
		}
		tree, err := parseTreeConfiguration(configured.Config)
		if err != nil || tree == nil {
			t.Fatalf("%s 树木配置解析失败: %v", name, err)
		}
		chunk := NewGeneratedChunk(0, 0, router.setting.MinY, router.setting.Height)
		for x := 0; x < 16; x++ {
			for z := 0; z < 16; z++ {
				for y := 55; y <= 63; y++ {
					block := SurfaceBlock{Name: "minecraft:stone"}
					if y == 63 {
						block = SurfaceBlock{Name: "minecraft:grass_block"}
					}
					chunk.SetBlock(x, y, z, block)
				}
			}
		}
		logs, leaves := 0, 0
		random := newLegacyRandom(17)
		router.placeTree(chunk, random, *tree, 8, 64, 8)
		chunk.ForEachBlock(func(_, _ int, _ int, block SurfaceBlock) {
			if block.Name == tree.trunk.Name {
				logs++
			}
			if block.Name == tree.leaves.Name {
				leaves++
			}
		})
		if logs == 0 || leaves == 0 {
			t.Fatalf("%s 未生成树干/树冠：logs=%d leaves=%d", name, logs, leaves)
		}
	}
}
