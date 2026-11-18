//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package main

import (
	"math"
	"testing"
	"time"
)

func TestMovementStateStraightForward(t *testing.T) {
	m := newMovementState(false, 0, 0, 1)
	m.setPosition(10, 64, -5, false)
	dx, dz := m.step(time.Now(), 0.2)
	if dx != 0.2 || dz != 0 {
		t.Fatalf("直线移动 step=(%v,%v), want (0.2,0)", dx, dz)
	}
}

func TestMovementStateRandomStaysWithinRadius(t *testing.T) {
	const radius = 8.0
	m := newMovementState(true, radius, 0, 42)
	m.setPosition(0, 64, 0, false)
	base := time.Unix(0, 0)
	for i := 0; i < 20000; i++ {
		now := base.Add(time.Duration(i) * time.Millisecond)
		dx, dz := m.step(now, 0.2)
		m.x += dx
		m.z += dz
		if dist := math.Hypot(m.x, m.z); dist > radius+1e-9 {
			t.Fatalf("第 %d 步越界：dist=%v radius=%v", i, dist, radius)
		}
	}
}

func TestMovementStateCorrectedPositionTurnsImmediately(t *testing.T) {
	m := newMovementState(true, 64, time.Second, 1)
	m.setPosition(0, 64, 0, false)
	m.nextTurn = time.Now().Add(time.Hour)
	m.setPosition(3, 64, 4, true)
	if !m.nextTurn.IsZero() {
		t.Fatal("服务器回拉后应立即重新选择方向")
	}
}
