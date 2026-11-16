//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"math"
	"testing"
)

func TestBiomeSimplexNoiseDeterministic(t *testing.T) {
	first := newPerlinSimplexNoise(newLegacyRandom(3456), []int{-2, -1, 0})
	second := newPerlinSimplexNoise(newLegacyRandom(3456), []int{-2, -1, 0})
	for _, point := range [][2]float64{{0, 0}, {1.5, -2.25}, {-123.4, 567.8}} {
		a := first.Value(point[0], point[1], false)
		b := second.Value(point[0], point[1], false)
		if a != b {
			t.Fatalf("same seed mismatch at %v: %v != %v", point, a, b)
		}
		if math.IsNaN(a) || math.IsInf(a, 0) {
			t.Fatalf("noise at %v is not finite: %v", point, a)
		}
	}
}
