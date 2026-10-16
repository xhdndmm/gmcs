//[https://github.com/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"
)

// 生物看向附近的玩家。
//
// 对照来源：官方 1.21.11 服务端反编译——Monster 的目标选择器注册了
// LookAtPlayerGoal（观察距离 8 格、触发概率 0.02、注视时长
// 40 + rand(40) tick），LookControl.tick 以每 tick ≤10° 的速度把朝向
// 转向目标（Mth.wrapDegrees + clamp）。
//
// 近似：原版分别维护头部（yHeadRot）与身体（yBodyRot）角度并按
// tickHeadTurn 平滑跟随，gmcs 用同一个朝向近似（因此注视时可见的转向
// 就是身体转向）；移动中不覆盖行走朝向。
const (
	// mobLookPlayerRange 是“看向玩家”的最大距离（原版 LookAtPlayerGoal 默认 8 格）。
	mobLookPlayerRange = 8.0
	// mobLookPlayerTicksMin/Range 是注视时长（原版 40 + rand(40)）。
	mobLookPlayerTicksMin   = 40
	mobLookPlayerTicksRange = 40
	// mobLookRotStep 是每 tick 的最大转向角（原版 LookControl 10°/tick）。
	mobLookRotStep = 10.0
	// mobLookTriggerModulo 是触发概率的分母（原版 2% 概率 → 每 50 tick 期望一次）。
	mobLookTriggerModulo = 50
)

// tickMobLook 让生物看向最近的玩家（原版 LookAtPlayerGoal + LookControl）。
// 返回 true 表示本 tick 改变了朝向（调用方据此发送朝向同步包）。
// 调用方必须持有 entityMu。
func (s *Server) tickMobLook(m *mob, nearestPlayer *session, playerDistance float64) bool {
	if m.LookTicks > 0 {
		m.LookTicks--
	}
	if m.LookTicks == 0 {
		m.LookTarget = nil
	}
	if m.LookTarget == nil {
		// 原版 LookAtPlayerGoal.canUse：每 tick 2% 概率（nextFloat() < 0.02）。
		if nearestPlayer == nil || playerDistance > mobLookPlayerRange || s.nextRandom()%mobLookTriggerModulo != 0 {
			return false
		}
		m.LookTarget = nearestPlayer
		m.LookTicks = mobLookPlayerTicksMin + int(s.nextRandom()%mobLookPlayerTicksRange)
	}
	target := m.LookTarget
	px, py, pz, _, _ := target.playerPosition()
	if target.dimensionID() != m.Dim || math.Hypot(px-m.X, pz-m.Z) > mobLookPlayerRange {
		m.LookTarget = nil
		m.LookTicks = 0
		return false
	}
	// 原版 LookControl：朝向目标的水平角与俯仰角，按步长截断后旋转。
	desiredYaw := float32(math.Atan2(-(px-m.X), pz-m.Z) * 180 / math.Pi)
	horizontal := math.Hypot(px-m.X, pz-m.Z)
	desiredPitch := float32(-math.Atan2(py-m.Y, horizontal) * 180 / math.Pi)
	changedYaw := rotateTowardAngle(&m.Yaw, desiredYaw, mobLookRotStep)
	changedPitch := rotateTowardAngle(&m.Pitch, desiredPitch, mobLookRotStep)
	return changedYaw || changedPitch
}

// rotateTowardAngle 把角度按最大步长转向目标（原版 LookControl.rotateTowards：
// wrapDegrees(target-current) 截断到 ±maxDelta 后累加），返回是否发生变化。
func rotateTowardAngle(current *float32, target, maxDelta float32) bool {
	delta := wrapDegrees(target - *current)
	if delta > maxDelta {
		delta = maxDelta
	} else if delta < -maxDelta {
		delta = -maxDelta
	}
	if delta == 0 {
		return false
	}
	*current = wrapDegrees(*current + delta)
	return true
}

// wrapDegrees 把角度规范到 (-180, 180]（原版 Mth.wrapDegrees）。
func wrapDegrees(degrees float32) float32 {
	degrees = float32(math.Mod(float64(degrees), 360))
	if degrees >= 180 {
		degrees -= 360
	} else if degrees < -180 {
		degrees += 360
	}
	return degrees
}
