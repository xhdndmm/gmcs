//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package registry

import "testing"

// TestPlacementTablesConsistent 校验生成器的放置状态表：每个状态 ID 都必须
// 属于对应的方块，且同一方块的不同组合互不相同。
func TestPlacementTablesConsistent(t *testing.T) {
	check := func(tableName string, ids []uint16, blockName string) {
		t.Helper()
		seen := make(map[uint16]bool, len(ids))
		for _, id := range ids {
			name, ok := BlockNameByState(id)
			if !ok {
				t.Fatalf("%s: 状态 %d 不在状态区间表内", tableName, id)
			}
			if name != blockName {
				t.Fatalf("%s: 状态 %d 属于 %s，期望 %s", tableName, id, name, blockName)
			}
			if seen[id] {
				t.Fatalf("%s: 状态 %d 重复", tableName, id)
			}
			seen[id] = true
		}
	}

	if len(PlacementStairs) == 0 || len(PlacementSlabs) == 0 || len(PlacementAxis) == 0 ||
		len(PlacementFacing) == 0 || len(PlacementTrapdoors) == 0 ||
		len(PlacementTrapdoorsOpen) == 0 || len(PlacementWallTorches) == 0 ||
		len(PlacementLadders) == 0 || len(PlacementFaceAttached) == 0 ||
		len(PlacementDoors) == 0 || len(PlacementDoorsOpen) == 0 ||
		len(PlacementDoorsPowered) == 0 || len(PlacementTrapdoorsPowered) == 0 ||
		len(PlacementFenceGates) == 0 || len(PlacementFenceGatesOpen) == 0 ||
		len(PlacementFenceGatesPowered) == 0 {
		t.Fatal("放置形态表为空（生成器未运行？）")
	}

	for name, table := range PlacementStairs {
		var ids []uint16
		for _, row := range table {
			ids = append(ids, row[:]...)
		}
		check("PlacementStairs", ids, name)
	}
	for name, table := range PlacementSlabs {
		check("PlacementSlabs", table[:], name)
	}
	for name, table := range PlacementAxis {
		check("PlacementAxis", table[:], name)
	}
	for name, table := range PlacementFacing {
		var ids []uint16
		for _, id := range table {
			if id != 0 {
				ids = append(ids, id)
			}
		}
		check("PlacementFacing", ids, name)
	}
	for name, table := range PlacementTrapdoors {
		var ids []uint16
		for _, row := range table {
			ids = append(ids, row[:]...)
		}
		check("PlacementTrapdoors", ids, name)
	}
	for name, table := range PlacementTrapdoorsOpen {
		var ids []uint16
		for _, row := range table {
			ids = append(ids, row[:]...)
		}
		check("PlacementTrapdoorsOpen", ids, name)
	}
	for name, table := range PlacementWallTorches {
		check("PlacementWallTorches", table[:], name)
	}
	for name, table := range PlacementLadders {
		check("PlacementLadders", table[:], name)
	}
	for name, table := range PlacementFaceAttached {
		var ids []uint16
		for _, row := range table {
			ids = append(ids, row[:]...)
		}
		check("PlacementFaceAttached", ids, name)
	}
	for name, table := range PlacementDoors {
		var ids []uint16
		for _, half := range table {
			for _, row := range half {
				ids = append(ids, row[:]...)
			}
		}
		check("PlacementDoors", ids, name)
	}
	for name, table := range PlacementDoorsOpen {
		var ids []uint16
		for _, half := range table {
			for _, row := range half {
				ids = append(ids, row[:]...)
			}
		}
		check("PlacementDoorsOpen", ids, name)
	}
	for name, table := range PlacementDoorsPowered {
		var ids []uint16
		for _, half := range table {
			for _, row := range half {
				ids = append(ids, row[:]...)
			}
		}
		check("PlacementDoorsPowered", ids, name)
	}
	for name, table := range PlacementTrapdoorsPowered {
		var ids []uint16
		for _, row := range table {
			ids = append(ids, row[:]...)
		}
		check("PlacementTrapdoorsPowered", ids, name)
	}
	for _, gates := range []struct {
		name  string
		table map[string][2][4]uint16
	}{
		{"PlacementFenceGates", PlacementFenceGates},
		{"PlacementFenceGatesOpen", PlacementFenceGatesOpen},
		{"PlacementFenceGatesPowered", PlacementFenceGatesPowered},
	} {
		for name, table := range gates.table {
			check(gates.name, table[0][:], name)
			check(gates.name, table[1][:], name)
			// in_wall 必须真正区分状态（否则放置/邻块更新会写回同一状态）。
			if table[0] == table[1] {
				t.Fatalf("%s: %s 的 in_wall 状态相同", gates.name, name)
			}
		}
	}

	// 台阶与活板门的 bottom/top 状态必须不同。
	for name, table := range PlacementStairs {
		if table[0] == table[1] {
			t.Fatalf("%s: 台阶 bottom/top 状态相同", name)
		}
		break
	}
	// 半砖的 bottom/top/double 必须互不相同。
	for name, table := range PlacementSlabs {
		if table[0] == table[1] || table[1] == table[2] || table[0] == table[2] {
			t.Fatalf("%s: 半砖状态未区分 type", name)
		}
		break
	}
}
