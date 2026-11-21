//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package main

import (
	"math"
	"testing"
	"time"

	"gmcs/internal/protocol"
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

func TestEncodeUnsignedChatParses(t *testing.T) {
	packet := encodeUnsignedChat("hello", time.Unix(123, 0))
	message, err := protocol.ParseSignedChatMessage(packet)
	if err != nil {
		t.Fatal(err)
	}
	if message.Message != "hello" || message.Timestamp != 123000 {
		t.Fatalf("解析结果 = %+v", message)
	}
}

func TestParseAddEntityAndPosition(t *testing.T) {
	packet := protocol.EncodeAddEntity(7, [16]byte{1}, 42, 1, 2, 3, 0, 0, 0, 0, 0)
	_, offset, err := protocol.DecodeVarInt(packet)
	if err != nil {
		t.Fatal(err)
	}
	id, x, y, z, typeID, ok := parseAddEntity(packet, offset)
	if !ok || id != 7 || x != 1 || y != 2 || z != 3 || typeID != 42 {
		t.Fatalf("parseAddEntity = (%d,%v,%v,%v,%d,%v)", id, x, y, z, typeID, ok)
	}

	sync := protocol.EncodeEntityPositionSync(7, 4, 5, 6, 0, 0, 0, 0, 0, true)
	_, offset, err = protocol.DecodeVarInt(sync)
	if err != nil {
		t.Fatal(err)
	}
	id, x, y, z, ok = parseEntityPosition(sync, offset)
	if !ok || id != 7 || x != 4 || y != 5 || z != 6 {
		t.Fatalf("parseEntityPosition = (%d,%v,%v,%v,%v)", id, x, y, z, ok)
	}
}

func TestParseRemoveEntities(t *testing.T) {
	packet := protocol.EncodeEntityDestroy([]int32{1, 2, 3})
	_, offset, err := protocol.DecodeVarInt(packet)
	if err != nil {
		t.Fatal(err)
	}
	ids := parseRemoveEntities(packet, offset)
	if len(ids) != 3 || ids[0] != 1 || ids[2] != 3 {
		t.Fatalf("ids = %v", ids)
	}
}

func TestMovementStateJumpArc(t *testing.T) {
	m := newMovementState(false, 0, 0, 1)
	m.setPosition(0, 64, 0, false)
	m.startJump()
	peak := m.y
	for i := 0; i < 40; i++ {
		m.advanceVertical()
		if m.y > peak {
			peak = m.y
		}
		if !m.jumping && m.onGround {
			break
		}
	}
	if peak <= 64.5 {
		t.Fatalf("跳跃峰值过低：%v", peak)
	}
	if m.y != 64 || !m.onGround {
		t.Fatalf("跳跃未落地：y=%v onGround=%v", m.y, m.onGround)
	}
}

func TestNearestEntity(t *testing.T) {
	r := &clientRuntime{entities: map[int32]trackedEntity{
		1: {x: 10, y: 0, z: 0},
		2: {x: 1, y: 0, z: 0},
	}}
	id, ok := r.nearestEntity(0, 0, 0, 4)
	if !ok || id != 2 {
		t.Fatalf("nearest = %d, ok=%v", id, ok)
	}
	if _, ok := r.nearestEntity(0, 0, 0, 0.5); ok {
		t.Fatal("超出距离仍返回实体")
	}
}
