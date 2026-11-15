//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package main

import (
	"os"
	"path/filepath"
	"testing"

	"gmcs/internal/registry"
	"gmcs/internal/world"
)

func TestVanillaChunkRoundTrip(t *testing.T) {
	chunk := world.NewChunkDim(3, -2, world.DimensionOverworld)
	stone, err := worldState("minecraft:stone", nil)
	if err != nil {
		t.Fatal(err)
	}
	chunk.SetBlockState(2, 64, 4, stone)
	chestType, ok := registry.StaticEntryID("minecraft:block_entity_type", "minecraft:chest")
	if !ok {
		t.Fatal("缺少 chest 方块实体类型")
	}
	chunk.SetBlockEntity(2, 64, 4, world.BlockEntity{
		TypeID: chestType, LootTable: "minecraft:chests/simple_dungeon",
		Items: []world.ContainerItem{{ItemID: 1, Count: 3}},
	})
	var grid [16]uint16
	for i := range grid {
		grid[i] = plainsBiomeID()
	}
	grid[0] = grid[0]
	chunk.SetBiomeGrid(grid)
	root, err := buildVanillaChunk(chunk, world.DimensionOverworld)
	if err != nil {
		t.Fatal(err)
	}
	data, err := encodeNBT(root)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeNBT(data)
	if err != nil {
		t.Fatal(err)
	}
	back, err := convertVanillaChunk(decoded, world.DimensionOverworld)
	if err != nil {
		t.Fatal(err)
	}
	if got := back.GetBlockState(2, 64, 4); got != stone {
		t.Fatalf("方块 = %d, want %d", got, stone)
	}
	entity, ok := back.BlockEntityAt(2, 64, 4)
	if !ok || entity.LootTable != "minecraft:chests/simple_dungeon" || len(entity.Items) == 0 {
		t.Fatalf("方块实体 = %+v ok=%v", entity, ok)
	}
}

func worldState(name string, props map[string]string) (uint16, error) {
	if len(props) > 0 {
		if id, ok := blockStateWithProps(name, props); ok {
			return id, nil
		}
	}
	return blockStateID(name)
}

func blockStateID(name string) (uint16, error) {
	return registryBlockStateID(name)
}

func blockStateWithProps(name string, props map[string]string) (uint16, bool) {
	return registryBlockStateWithProps(name, props)
}

func registryBlockStateID(name string) (uint16, error) {
	return registry.BlockStateID(name)
}

func registryBlockStateWithProps(name string, props map[string]string) (uint16, bool) {
	return registry.BlockStateWithProps(name, props)
}

func TestConversionRoundTripDirectories(t *testing.T) {
	src := t.TempDir()
	vanilla := t.TempDir()
	back := t.TempDir()
	chunk := world.NewChunkDim(0, 0, world.DimensionOverworld)
	state, err := registry.BlockStateID("minecraft:stone")
	if err != nil {
		t.Fatal(err)
	}
	chunk.SetBlockState(1, 64, 1, state)
	payload := world.EncodeChunkPayload(chunk)
	compressed, err := world.CompressChunkPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := world.SavePayloads(src, map[world.ChunkPos][]byte{{X: 0, Z: 0}: compressed}); err != nil {
		t.Fatal(err)
	}
	if err := convertGMCS2Vanilla(convertOptions{Source: src, Destination: vanilla, Dimension: "overworld"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(vanilla, "region", "r.0.0.mca")); err != nil {
		t.Fatal(err)
	}
	if err := convertVanilla2GMCS(convertOptions{Source: vanilla, Destination: back, Dimension: "overworld"}); err != nil {
		t.Fatal(err)
	}
	loaded, err := world.LoadChunk(back, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil || loaded.GetBlockState(1, 64, 1) != state {
		t.Fatalf("往返后区块/方块丢失: %#v", loaded)
	}
}
