//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"gmcs/internal/protocol"
	"gmcs/internal/world"
)

// 生物的环境燃烧：接触火/灵魂火方块与岩浆的点燃、着火伤害与入水熄灭。
//
// 对照来源：官方 1.21.11 服务端反汇编——BaseFireBlock 的方块内效果
// （dzd.a(Entity)：非火免实体在火中被点燃 8 秒）、Entity.baseTick 的岩浆/着火结算
// （岩浆点燃 15 秒、着火每秒 1 点伤害、入水熄灭）与 igniteForTicks 的
// “取较大值”语义。
//
// 着火状态通过实体共享标志（Set Entity Data 索引 0 的 bit0）同步给附近玩家，
// 变化时才发送；随 entities.json 持久化（原版实体存档的 Fire 字段）。
//
// 判定范围：扫描生物碰撞盒覆盖的方块（收缩 1e-7，与原版
// Entity.checkInsideBlocks 对 AABB 的处理一致），而不是只看脚部与上方一格。
// 生物碰撞盒为统一的 0.6×1.95（僵尸尺寸，见 mobBox）；没有天气（原版雨水
// 会熄灭着火）；gmcs 的生物种类均无火免（原版烈焰人等火免生物未实现）。
const (
	// mobFireBlockTicks 是站在火方块上点燃的时长（原版 igniteForSeconds(8)）。
	mobFireBlockTicks = 8 * 20
	// mobFireDamageInterval 是着火伤害间隔（原版每秒 1 点）。
	mobFireDamageInterval = 20
	// maxStoredFireTicks 是读取 entities.json 时着火时长的上限（按最长点燃值限幅）。
	maxStoredFireTicks = lavaFireTicks
)

// mobEnvironment 是一次生物环境燃烧结算产生的广播与掉落（持锁时收集，解锁后处理）。
type mobEnvironment struct {
	dim     world.Dimension
	kind    mobKind
	x, z    float64
	packets [][]byte
	drop    bool
	lootX   float64
	lootY   float64
	lootZ   float64
}

// tickMobEnvironment 结算单个生物一帧的环境燃烧。调用方必须持有 entityMu；
// ok 为 false 时无需广播（不分配结果结构）。
func (s *Server) tickMobEnvironment(w *world.World, m *mob) (env mobEnvironment, ok bool) {
	if w == nil || m.Dead {
		return mobEnvironment{}, false
	}
	// 扫描碰撞盒覆盖的方块（按列批量读取；盒最多 2×2 列、每列 1–3 层）。
	inFire, inWater, inLava := environmentInBox(w, mobBox(m.X, m.Y, m.Z))

	env = mobEnvironment{dim: m.Dim, kind: m.Kind, x: m.X, z: m.Z}

	// 入水/降雨熄灭（原版 Entity.baseTick：入水、雨中或气泡柱时 clearFire）。
	if inWater || s.entityInRain(m.Dim, m.X, m.Y, m.Z, mobBox(m.X, m.Y, m.Z).MaxY) {
		m.FireTicks = 0
	}
	// 岩浆：结算岩浆伤害（原版 lavaHurt；点燃在下方，只有伤害生效才点燃）。
	if inLava {
		s.damageMob(m, envLavaDamage, s.lavaDamageTypeID, &env)
	}
	// 着火：每秒 1 点火焰伤害并递减；岩浆中由岩浆伤害取代（与玩家环境效果一致）。
	if m.FireTicks > 0 {
		if m.FireTicks%mobFireDamageInterval == 0 && !inLava {
			s.damageMob(m, envFireDamage, s.fireDamageTypeID, &env)
		}
		// 死亡时 damageMob 已把着火清零（原版死亡实体不再燃烧）。
		if m.FireTicks > 0 {
			m.FireTicks--
		}
	}
	// 点燃（原版顺序：baseTick 先结算，方块内效果在移动后应用，因此入火当帧
	// 结束时着火时长就是完整值）：火/灵魂火方块 8 秒，岩浆 15 秒（取较大值）。
	if !m.Dead {
		if inLava && m.FireTicks < lavaFireTicks {
			m.FireTicks = lavaFireTicks
		}
		if inFire && m.FireTicks < mobFireBlockTicks {
			m.FireTicks = mobFireBlockTicks
		}
	}
	// 着火标志变化时同步（原版 Set Entity Data 索引 0 的 bit0）。
	onFire := m.FireTicks > 0
	if onFire != m.FireSynced {
		m.FireSynced = onFire
		var flags byte
		if onFire {
			flags = 1
		}
		env.packets = append(env.packets, protocol.EncodeEntityMetadataByte(m.ID, 0, flags))
	}
	return env, len(env.packets) > 0
}

// damageMob 对生物结算一次环境伤害（无敌帧内忽略）：广播受击动画、伤害事件与
// 受击音效；生命值归零时标记死亡并记录掉落（掉落由调用方在解锁后生成）。
// 返回 true 表示伤害已生效。调用方必须持有 entityMu。
func (s *Server) damageMob(m *mob, damage float32, damageTypeID int32, env *mobEnvironment) bool {
	if m.HurtCooldown > 0 || m.Dead {
		return false
	}
	m.HurtCooldown = mobHurtCooldownTicks
	m.Health -= damage
	position := [3]float64{m.X, m.Y + 1, m.Z}
	env.packets = append(env.packets,
		protocol.EncodeHurtAnimation(m.ID, m.Yaw),
		protocol.EncodeDamageEvent(m.ID, damageTypeID, 0, 0, &position),
		protocol.EncodeEntitySoundEffect(s.mobHurtSound(m.Kind), protocol.SoundCategoryHostile, m.ID, 1, 1, 0),
	)
	if m.Health <= 0 {
		m.Health = 0
		m.Dead = true
		m.DeadTicks = 0
		env.packets = append(env.packets,
			protocol.EncodeEntityEvent(m.ID, protocol.EntityEventDeath),
			protocol.EncodeEntitySoundEffect(s.mobDeathSound(m.Kind), protocol.SoundCategoryHostile, m.ID, 1, 1, 0),
		)
		env.drop = true
		env.lootX, env.lootY, env.lootZ = m.X, m.Y, m.Z
		// 死亡时清除着火（原版死亡实体不再显示火焰）。
		if m.FireSynced || m.FireTicks != 0 {
			m.FireTicks = 0
			m.FireSynced = false
			env.packets = append(env.packets, protocol.EncodeEntityMetadataByte(m.ID, 0, 0))
		}
	}
	return true
}
