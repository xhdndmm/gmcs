//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package item

import (
	"encoding/hex"
	"testing"

	"gmcs/internal/registry"
)

// 本文件用官方 1.21.11 服务端实测数据包逐字节验证槽位（Slot）编码。
//
// 采集方式：离线启动官方服务端（超平坦），用离线客户端进入世界后用控制台
// /give 发放物品，抓取客户端收到的 Set Container Slot（0x14）负载。
// 以下 payload 均为“count + itemID + 组件补丁”的完整槽位字节。
//
// 对应命令与负载：
//   - /give Dumper minecraft:diamond 1
//   - /give Dumper minecraft:potion[minecraft:potion_contents={potion:"minecraft:swiftness"}] 1
//   - /give Dumper minecraft:netherite_chestplate[minecraft:trim={material:"minecraft:gold",pattern:"minecraft:bolt"}] 1
//   - /give Dumper minecraft:diamond_sword[minecraft:enchantments={"minecraft:sharpness":5,"minecraft:mending":1}] 1
//   - /give Dumper minecraft:diamond_pickaxe[minecraft:damage=5,minecraft:repair_cost=7,minecraft:custom_name="Test Pick"] 1
//
// 这些夹具同时验证：附魔/药水为裸注册表 ID、trim 为（ID+1）的 Holder、
// 自定义名称的 NBT 字符串为 u16 长度、组件类型 ID 与负载结构。

// mustItemID 查询物品注册表 ID；不存在时终止测试。
func mustItemID(t *testing.T, name string) int32 {
	t.Helper()
	id, err := registry.ItemID(name)
	if err != nil {
		t.Fatalf("物品 %s 不在注册表：%v", name, err)
	}
	return id
}

// mustHexPayload 解析十六进制字符串为字节切片。
func mustHexPayload(t *testing.T, s string) []byte {
	t.Helper()
	data, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("夹具十六进制无效：%v", err)
	}
	return data
}

// TestVanillaCapturedSlotPayloads 逐字节对照官方服务端实测的槽位负载。
func TestVanillaCapturedSlotPayloads(t *testing.T) {
	// 先锁定物品 ID 与实测一致（注册表重新生成时能立刻发现偏移）。
	ids := map[string]int32{
		"minecraft:diamond":              898,
		"minecraft:potion":               1121,
		"minecraft:netherite_chestplate": 979,
		"minecraft:diamond_sword":        936,
		"minecraft:diamond_pickaxe":      938,
	}
	for name, want := range ids {
		if got := mustItemID(t, name); got != want {
			t.Fatalf("物品 %s 的注册表 ID = %d, want %d（与原版抓包不一致）", name, got, want)
		}
	}
	if registry.PotionSwiftness != 13 {
		t.Fatalf("PotionSwiftness = %d, want 13（与原版抓包不一致）", registry.PotionSwiftness)
	}

	cases := []struct {
		name    string
		stack   Stack
		payload string
	}{
		{
			name:    "diamond_plain",
			stack:   Stack{ItemID: 898, Count: 1},
			payload: "0182070000",
		},
		{
			name: "potion_swiftness",
			stack: Stack{ItemID: 1121, Count: 1,
				PotionID: registry.PotionSwiftness + 1},
			payload: "01e108010031010d000000",
		},
		{
			name: "trim_gold_bolt",
			stack: Stack{ItemID: 979, Count: 1,
				TrimMaterial: "minecraft:gold", TrimPattern: "minecraft:bolt"},
			payload: "01d3070100360501",
		},
		{
			name: "enchanted_sword",
			stack: Stack{ItemID: 936, Count: 1,
				Enchantments: []EnchantmentEntry{{ID: 23, Level: 1}, {ID: 33, Level: 5}}},
			payload: "01a80701000d0217012105",
		},
		{
			// 注意：原版按 /give 命令的组件顺序写出（3、19、6），
			// gmcs 固定按组件类型 ID 升序（3、6、19）；两者对客户端等价。
			name: "pickaxe_damage_repair_name",
			stack: Stack{ItemID: 938, Count: 1,
				Damage: 5, RepairCost: 7, CustomName: "Test Pick"},
			payload: "01aa07030003050608000954" + "657374205069636b" + "1307",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := mustHexPayload(t, tc.payload)
			got := tc.stack.AppendSlot(nil)
			if len(got) != len(want) {
				t.Fatalf("负载 = %x, want %x", got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("负载 = %x, want %x（首个差异下标 %d）", got, want, i)
				}
			}
		})
	}
}

// TestTrimComponentRejectsUnknownNames 验证未知纹饰材质/图案不产生组件。
func TestTrimComponentRejectsUnknownNames(t *testing.T) {
	stack := Stack{ItemID: 979, Count: 1,
		TrimMaterial: "minecraft:unknown", TrimPattern: "minecraft:bolt"}
	got := stack.AppendSlot(nil)
	want := []byte{1, 0xd3, 0x07, 0, 0}
	if len(got) != len(want) {
		t.Fatalf("负载 = %x, want %x（未知材质不应写出组件）", got, want)
	}
	stack = Stack{ItemID: 979, Count: 1, TrimMaterial: "minecraft:gold"}
	got = stack.AppendSlot(nil)
	if len(got) != len(want) {
		t.Fatalf("负载 = %x, want %x（缺少图案不应写出组件）", got, want)
	}
}
