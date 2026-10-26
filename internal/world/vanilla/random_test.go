//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// oracleFile 是 tools/vgoracle 从官方 jar 导出的对照数据。
// 用它来逐值校验本包的实现是否与原版一致（不是在 Go 里重新“推导”原版行为）。
type oracleFile struct {
	NextLong []struct {
		Seed   int64   `json:"seed"`
		Values []int64 `json:"values"`
	} `json:"nextLong"`
	ImprovedNoise []struct {
		Seed   int64     `json:"seed"`
		Values []float64 `json:"values"`
	} `json:"improvedNoise"`
	NormalNoise []struct {
		Noise  string    `json:"noise"`
		Seed   int64     `json:"seed"`
		Values []float64 `json:"values"`
	} `json:"normalNoise"`
	ImprovedNoiseState []struct {
		Seed int64   `json:"seed"`
		XO   float64 `json:"xo"`
		YO   float64 `json:"yo"`
		ZO   float64 `json:"zo"`
		P    []int   `json:"p"`
	} `json:"improvedNoiseState"`
}

// loadOracle 读取对照数据文件。
func loadOracle(t *testing.T) oracleFile {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("testdata", "noise_oracle.json"))
	if err != nil {
		t.Fatalf("读取对照数据失败：%v", err)
	}
	var oracle oracleFile
	if err := json.Unmarshal(payload, &oracle); err != nil {
		t.Fatalf("解析对照数据失败：%v", err)
	}
	if len(oracle.NextLong) == 0 {
		t.Fatal("对照数据缺少 nextLong 序列")
	}
	return oracle
}

// TestOracleFixtureShape 校验对照数据本身的结构（作为后续阶段的对拍基线）。
func TestOracleFixtureShape(t *testing.T) {
	oracle := loadOracle(t)
	if len(oracle.NextLong) != 4 {
		t.Fatalf("nextLong 序列组数 = %d, want 4", len(oracle.NextLong))
	}
	for _, group := range oracle.NextLong {
		if len(group.Values) != 8 {
			t.Fatalf("seed=%d 的 nextLong 数量 = %d, want 8", group.Seed, len(group.Values))
		}
	}
	if len(oracle.ImprovedNoise) != 4 {
		t.Fatalf("improvedNoise 组数 = %d, want 4", len(oracle.ImprovedNoise))
	}
	if len(oracle.NormalNoise) != 8 {
		t.Fatalf("normalNoise 组数 = %d, want 8（2 种噪声 × 4 个种子）", len(oracle.NormalNoise))
	}
	for _, group := range oracle.NormalNoise {
		for _, value := range group.Values {
			if value < -1 || value > 1 {
				t.Fatalf("%s seed=%d 的取值 %v 超出 [-1,1]（NormalNoise 的理论范围）", group.Noise, group.Seed, value)
			}
		}
	}
}

// TestXoroshiroMatchesVanilla 逐值比对 nextLong 序列：这是“随机源与原版一致”
// 的唯一依据（数据来自官方 jar 的 XoroshiroRandomSource）。
func TestXoroshiroMatchesVanilla(t *testing.T) {
	oracle := loadOracle(t)
	for _, group := range oracle.NextLong {
		random := NewXoroshiro(group.Seed)
		for i, want := range group.Values {
			if got := random.NextLong(); got != want {
				t.Fatalf("seed=%d 第 %d 个 nextLong = %d, want %d", group.Seed, i, got, want)
			}
		}
	}
}

// TestXoroshiroNextDoubleRange 校验 NextDouble 落在 [0,1)。
func TestXoroshiroNextDoubleRange(t *testing.T) {
	random := NewXoroshiro(12345)
	for i := 0; i < 1000; i++ {
		value := random.NextDouble()
		if value < 0 || value >= 1 {
			t.Fatalf("NextDouble = %v 超出 [0,1)", value)
		}
	}
}

// TestXoroshiroNextIntRange 校验 NextInt 落在 [0,bound)。
func TestXoroshiroNextIntRange(t *testing.T) {
	random := NewXoroshiro(42)
	for _, bound := range []int{1, 2, 7, 256, 1000} {
		for i := 0; i < 500; i++ {
			value := random.NextInt(bound)
			if value < 0 || value >= bound {
				t.Fatalf("NextInt(%d) = %d 超出范围", bound, value)
			}
		}
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("NextInt(0) 应当 panic（原版对非正 bound 抛 IllegalArgumentException）")
			}
		}()
		random.NextInt(0)
	}()
}
