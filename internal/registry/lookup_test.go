package registry

import "testing"

// TestEntryIDLookups 验证实体类型/音效（静态）与伤害类型（同步）的 ID 查询。
// 期望值与官方 reports/registries.json 及同步注册表顺序一致。
func TestEntryIDLookups(t *testing.T) {
	tests := []struct {
		name     string
		static   bool
		registry string
		entry    string
		want     int32
	}{
		{"zombie entity type", true, "minecraft:entity_type", "minecraft:zombie", 150},
		{"zombie hurt sound", true, "minecraft:sound_event", "minecraft:entity.zombie.hurt", 1807},
		{"zombie death sound", true, "minecraft:sound_event", "minecraft:entity.zombie.death", 1800},
		{"player hurt sound", true, "minecraft:sound_event", "minecraft:entity.player.hurt", 1251},
		{"mob attack damage type", false, "minecraft:damage_type", "minecraft:mob_attack", 28},
		{"player attack damage type", false, "minecraft:damage_type", "minecraft:player_attack", 34},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var (
				id int32
				ok bool
			)
			if test.static {
				id, ok = StaticEntryID(test.registry, test.entry)
			} else {
				id, ok = SyncEntryID(test.registry, test.entry)
			}
			if !ok || id != test.want {
				t.Fatalf("lookup(%s, %s) = %d, %v; want %d, true",
					test.registry, test.entry, id, ok, test.want)
			}
		})
	}

	if _, ok := StaticEntryID("minecraft:entity_type", "minecraft:does_not_exist"); ok {
		t.Fatal("static lookup should fail for unknown entry")
	}
	if _, ok := SyncEntryID("minecraft:unknown_registry", "minecraft:zombie"); ok {
		t.Fatal("sync lookup should fail for unknown registry")
	}
}
