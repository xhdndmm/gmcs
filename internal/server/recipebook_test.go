//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
)

// TestRecipeBookPacketsSentOnJoin 验证进入世界时发送 Update Recipes、
// Recipe Book Add 与 Recipe Book Settings 三个数据包。
func TestRecipeBookPacketsSentOnJoin(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	seen := map[int32]bool{}
	instance, _ := joinServerObserved(t, cfg, "Bookworm", func(id int32, payload []byte) {
		seen[id] = true
	})
	if instance.recipeBook == nil {
		t.Fatal("配方书未构建")
	}
	for _, want := range []int32{
		protocol.PlayPacketIDUpdateRecipes,
		protocol.PlayPacketIDRecipeBookAdd,
		protocol.PlayPacketIDRecipeBookSettings,
	} {
		if !seen[want] {
			t.Fatalf("加入流程未发送数据包 %#x", want)
		}
	}
}

// TestRecipeBookDataBuilt 验证配方书共享数据的条目数与槽位一致性。
func TestRecipeBookDataBuilt(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, _ := joinServer(t, cfg, "BookData")
	book := instance.recipeBook
	if book == nil {
		t.Fatal("配方书未构建")
	}
	wantEntries := len(registry.ShapedRecipes) + len(registry.ShapelessRecipes) +
		len(registry.CookingRecipes) + len(registry.SmithingTransformRecipes)
	if len(book.entries) != wantEntries {
		t.Fatalf("配方书条目 = %d, want %d", len(book.entries), wantEntries)
	}
	// 合成条目必须指向有效的配方表下标。
	for i, entry := range book.entries {
		switch entry.kind {
		case bookShaped:
			if entry.index < 0 || int(entry.index) >= len(registry.ShapedRecipes) {
				t.Fatalf("条目 %d 的有序配方下标越界：%d", i, entry.index)
			}
		case bookShapeless:
			if entry.index < 0 || int(entry.index) >= len(registry.ShapelessRecipes) {
				t.Fatalf("条目 %d 的无序配方下标越界：%d", i, entry.index)
			}
		case bookSmithing:
			if entry.index < 0 || int(entry.index) >= len(registry.SmithingTransformRecipes) {
				t.Fatalf("条目 %d 的锻造配方下标越界：%d", i, entry.index)
			}
		}
	}
}

// findBookDisplayID 返回指定名称无序配方的显示编号。
func findBookDisplayID(t *testing.T, book *recipeBook, name string) int32 {
	t.Helper()
	for i, entry := range book.entries {
		if entry.kind != bookShapeless {
			continue
		}
		if registry.ShapelessRecipes[entry.index].Name == name {
			return int32(i)
		}
	}
	t.Fatalf("配方书中找不到无序配方 %s", name)
	return -1
}

// countInventoryItems 统计玩家物品栏中指定物品的总数（主背包 + 快捷栏）。
func countInventoryItems(player *session, itemID int32) int32 {
	total := int32(0)
	for slot := 0; slot < 36; slot++ {
		if stack := player.inventory.Get(slot); stack.ItemID == itemID {
			total += stack.Count
		}
	}
	return total
}

// TestRecipeBookPlaceCraft 验证配方书点击：Place Recipe 把原料放入 2×2
// 合成格并生成结果；makeAll 尽可能多次合成。
func TestRecipeBookPlaceCraft(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.GameMode = "creative"
	instance, conn := joinServer(t, cfg, "BookCrafter")

	logID, err := registry.ItemID("minecraft:oak_log")
	if err != nil {
		t.Fatal(err)
	}
	plankID, err := registry.ItemID("minecraft:oak_planks")
	if err != nil {
		t.Fatal(err)
	}
	displayID := findBookDisplayID(t, instance.recipeBook, "minecraft:oak_planks")

	// 给出 3 个原木，点击一次配方（不 makeAll）：合成格应放入 1 个原木、
	// 结果槽显示 4 个木板。
	sendSetCreativeSlot(t, conn, 0, logID, 3)
	sendPlaceRecipe(t, conn, protocol.PlayerInventoryWindowID, displayID, false)
	player := activeSession(t, instance, "BookCrafter")
	processBarrier(t, conn)
	grid := player.invCraftState().gridSnapshot()
	placed := 0
	for _, stack := range grid {
		if !stack.IsEmpty() {
			if stack.ItemID != logID || stack.Count != 1 {
				t.Fatalf("合成格内容 = %+v, want 原木 x1", stack)
			}
			placed++
		}
	}
	if placed != 1 {
		t.Fatalf("合成格放入数量 = %d, want 1", placed)
	}
	backing := craftSlotBacking{player: player, state: player.invCraftState(), windowZero: true}
	if result := backing.resultOf(nil); result.ItemID != plankID || result.Count != 4 {
		t.Fatalf("结果槽 = %+v, want 橡木木板 x4", result)
	}

	// makeAll：3 个原木应全部合成为 12 个木板（合成格清空）。
	sendPlaceRecipe(t, conn, protocol.PlayerInventoryWindowID, displayID, true)
	processBarrier(t, conn)
	if planks := countInventoryItems(player, plankID); planks != 12 {
		t.Fatalf("makeAll 后木板 = %d, want 12", planks)
	}
	if logs := countInventoryItems(player, logID); logs != 0 {
		t.Fatalf("makeAll 后原木 = %d, want 0", logs)
	}
	for _, stack := range player.invCraftState().gridSnapshot() {
		if !stack.IsEmpty() {
			t.Fatalf("makeAll 后合成格残留 %+v", stack)
		}
	}
}
