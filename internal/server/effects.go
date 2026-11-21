//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
)

// 状态效果（MobEffect）的最小运行时。效果 ID 来自官方静态
// minecraft:mob_effect 注册表；持续时间和等级来自 Potions 类（见
// registry/potion_effects.go）。效果实例按 ID 存在固定数组中，避免每个
// 玩家持有 map；数组由 stateMu 保护。
const mobEffectCount = 40

const (
	// 效果结算周期（tick）：原版 MobEffect 的 applyEffectTick 频率。
	effectRegenerationInterval = 50
	effectPoisonInterval       = 25
	effectWitherInterval       = 40
)

type mobEffectInstance struct {
	active    bool
	duration  int32
	amplifier int32
	ambient   bool
	visible   bool
	showIcon  bool
	// nextTick 是下一次周期结算的倒计时（regeneration/poison/wither）。
	nextTick int32
}

// effectSnapshot 是持久化/测试用的效果副本。
type effectSnapshot struct {
	ID        int32
	Duration  int32
	Amplifier int32
	Ambient   bool
	Visible   bool
	ShowIcon  bool
}

func (s *session) effectLocked(id int32) *mobEffectInstance {
	if id < 0 || id >= mobEffectCount {
		return nil
	}
	return &s.effects[id]
}

// hasEffect 报告玩家是否拥有某效果（等级存在即 true）。
func (s *session) hasEffect(id int32) bool {
	s.stateMu.Lock()
	active := id >= 0 && id < mobEffectCount && s.effects[id].active
	s.stateMu.Unlock()
	return active
}

// effectLevel 返回效果等级（amplifier + 1）；不存在返回 0。
func (s *session) effectLevel(id int32) int32 {
	s.stateMu.Lock()
	level := int32(0)
	if id >= 0 && id < mobEffectCount && s.effects[id].active {
		level = s.effects[id].amplifier + 1
	}
	s.stateMu.Unlock()
	return level
}

// effectPacket 构造 Update Mob Effect 包。
func effectPacket(entityID, effectID int32, effect mobEffectInstance) []byte {
	var flags uint8
	if effect.ambient {
		flags |= protocol.MobEffectFlagAmbient
	}
	if effect.visible {
		flags |= protocol.MobEffectFlagVisible
	}
	if effect.showIcon {
		flags |= protocol.MobEffectFlagShowIcon
	}
	return protocol.EncodeUpdateMobEffect(entityID, effectID, effect.amplifier, effect.duration, flags)
}

// applyEffect 添加/合并一个持续效果，返回需要发送的 Update Mob Effect 包。
// 相同效果按原版 MobEffectInstance.update：取较高等级；等级相同时延长到较长
// 持续时间。instant 效果由调用方先结算，不进入持续效果数组。
func (s *session) applyEffect(id, duration, amplifier int32, ambient bool) []byte {
	if id < 0 || id >= mobEffectCount || duration <= 0 {
		return nil
	}
	s.stateMu.Lock()
	effect := &s.effects[id]
	if effect.active {
		if amplifier > effect.amplifier {
			effect.amplifier = amplifier
			effect.duration = duration
		} else if amplifier == effect.amplifier && duration > effect.duration {
			effect.duration = duration
		}
		// 重新施加时重置周期计时，保证立即开始按新等级结算。
		if id == registry.MobEffectRegeneration {
			effect.nextTick = effectRegenerationInterval >> max(effect.amplifier, 0)
		} else if id == registry.MobEffectPoison {
			effect.nextTick = effectPoisonInterval >> max(effect.amplifier, 0)
		} else if id == registry.MobEffectWither {
			effect.nextTick = effectWitherInterval >> max(effect.amplifier, 0)
		}
		effect.ambient = effect.ambient || ambient
		effect.visible = true
		effect.showIcon = true
		packet := effectPacket(s.entityID, id, *effect)
		s.stateMu.Unlock()
		return packet
	}
	*effect = mobEffectInstance{active: true, duration: duration, amplifier: amplifier, ambient: ambient, visible: true, showIcon: true}
	switch id {
	case registry.MobEffectRegeneration:
		effect.nextTick = effectRegenerationInterval >> max(amplifier, 0)
	case registry.MobEffectPoison:
		effect.nextTick = effectPoisonInterval >> max(amplifier, 0)
	case registry.MobEffectWither:
		effect.nextTick = effectWitherInterval >> max(amplifier, 0)
	}
	packet := effectPacket(s.entityID, id, *effect)
	s.stateMu.Unlock()
	return packet
}

// removeEffect 移除一个效果并返回 Remove Mob Effect 包。
func (s *session) removeEffect(id int32) []byte {
	if id < 0 || id >= mobEffectCount {
		return nil
	}
	s.stateMu.Lock()
	effect := &s.effects[id]
	if !effect.active {
		s.stateMu.Unlock()
		return nil
	}
	effect.active = false
	s.stateMu.Unlock()
	return protocol.EncodeRemoveMobEffect(s.entityID, id)
}

// clearEffects 清除全部效果并返回 Remove 包。
func (s *session) clearEffects() [][]byte {
	s.stateMu.Lock()
	packets := make([][]byte, 0, 4)
	for id := range s.effects {
		if !s.effects[id].active {
			continue
		}
		s.effects[id].active = false
		packets = append(packets, protocol.EncodeRemoveMobEffect(s.entityID, int32(id)))
	}
	s.stateMu.Unlock()
	return packets
}

// activeEffects 返回当前效果副本（玩家数据持久化用）。
func (s *session) activeEffects() []effectSnapshot {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	result := make([]effectSnapshot, 0, 4)
	for id := range s.effects {
		effect := s.effects[id]
		if effect.active {
			result = append(result, effectSnapshot{
				ID: int32(id), Duration: effect.duration, Amplifier: effect.amplifier,
				Ambient: effect.ambient, Visible: effect.visible, ShowIcon: effect.showIcon,
			})
		}
	}
	return result
}

// applyEffectList 应用药水的效果列表：瞬间治疗/伤害立即结算，其余作为持续效果。
// 返回需要发送的 Update/Remove 包与瞬时生命变化后的最大生命值。
func (s *Server) applyPotionEffects(player *session, effects []registry.PotionEffect) [][]byte {
	packets := make([][]byte, 0, len(effects))
	for _, effect := range effects {
		switch effect.ID {
		case registry.MobEffectInstantHealth:
			amount := int32(4) << uint(max(effect.Amplifier, 0))
			player.healImmediate(float32(amount))
		case registry.MobEffectInstantDamage:
			amount := int32(6) << uint(max(effect.Amplifier, 0))
			s.damagePlayer(player, float32(amount), "magic", 0, s.magicDamageTypeID, nil)
		case registry.MobEffectSaturation:
			player.addSaturation(float32(effect.Amplifier + 1))
		default:
			if packet := player.applyEffect(effect.ID, effect.Duration, effect.Amplifier, false); packet != nil {
				packets = append(packets, packet)
			}
		}
	}
	if health, food, saturation := player.healthStatus(); health > 0 {
		packets = append(packets, protocol.EncodeUpdateHealth(health, food, saturation))
	}
	return packets
}

func (s *session) healImmediate(amount float32) {
	s.stateMu.Lock()
	if s.health > 0 {
		s.health = min(maxPlayerHealth, s.health+amount)
	}
	s.stateMu.Unlock()
}

func (s *session) addSaturation(amount float32) {
	s.stateMu.Lock()
	s.food = min(maxPlayerFood, s.food+int32(amount))
	s.saturation = min(float32(s.food), s.saturation+amount)
	s.stateMu.Unlock()
}

// effectTickResult 是 tickEffects 的结果；伤害在锁外通过 server.damagePlayer
// 结算，避免与 applyDamage 的 stateMu 锁互相递归。
type effectTickResult struct {
	packets      [][]byte
	health       float32
	changed      bool
	damage       float32
	damageEffect int32
}

// tickEffects 推进一个玩家的效果：递减持续时间、结算周期效果并移除到期效果。
// 仅由服务器 Tick goroutine 调用。
func (s *session) tickEffects() effectTickResult {
	result := effectTickResult{}
	s.stateMu.Lock()
	for id := range s.effects {
		effect := &s.effects[id]
		if !effect.active {
			continue
		}
		if effect.duration > 0 {
			effect.duration--
		}
		if effect.duration == 0 {
			effect.active = false
			result.packets = append(result.packets, protocol.EncodeRemoveMobEffect(s.entityID, int32(id)))
			continue
		}
		switch int32(id) {
		case registry.MobEffectRegeneration:
			effect.nextTick--
			if effect.nextTick <= 0 {
				effect.nextTick = effectRegenerationInterval >> max(effect.amplifier, 0)
				if s.health > 0 && s.health < maxPlayerHealth {
					s.health = min(maxPlayerHealth, s.health+1)
					result.changed = true
				}
			}
		case registry.MobEffectPoison:
			effect.nextTick--
			if effect.nextTick <= 0 {
				effect.nextTick = effectPoisonInterval >> max(effect.amplifier, 0)
				if s.health > 1 {
					result.damage = 1
					result.damageEffect = registry.MobEffectPoison
				}
			}
		case registry.MobEffectWither:
			effect.nextTick--
			if effect.nextTick <= 0 {
				effect.nextTick = effectWitherInterval >> max(effect.amplifier, 0)
				if s.health > 0 {
					result.damage = 1
					result.damageEffect = registry.MobEffectWither
				}
			}
		}
	}
	result.health = s.health
	s.stateMu.Unlock()
	return result
}

// tickPlayerEffects 推进全部玩家的状态效果并同步客户端。
func (s *Server) tickPlayerEffects(players []*session) {
	for _, player := range players {
		if !player.isJoined() || player.isDead() {
			continue
		}
		result := player.tickEffects()
		for _, packet := range result.packets {
			player.tryWrite(packet)
		}
		if result.damage > 0 {
			// 毒/凋零在原版属于魔法/凋零伤害；这里使用对应伤害类型，
			// 并让 applyDamage 的盔甲/附魔/抗性逻辑统一生效。
			damageType := s.magicDamageTypeID
			if result.damageEffect == registry.MobEffectWither {
				damageType = s.witherDamageTypeID
			}
			s.damagePlayer(player, result.damage, "magic", 0, damageType, nil)
			continue
		}
		if result.changed {
			health, food, saturation := player.healthStatus()
			player.tryWrite(protocol.EncodeUpdateHealth(health, food, saturation))
		}
	}
}

// resistanceMultiplier 返回抗性提升的伤害倍率（每级 20%，5 级免疫）。
func (s *session) resistanceMultiplier() float32 {
	s.stateMu.Lock()
	value := s.resistanceMultiplierLocked()
	s.stateMu.Unlock()
	return value
}

// resistanceMultiplierLocked 与 resistanceMultiplier 相同，调用方必须持有 stateMu。
func (s *session) resistanceMultiplierLocked() float32 {
	level := s.effects[registry.MobEffectResistance].amplifier + 1
	if !s.effects[registry.MobEffectResistance].active || level <= 0 {
		return 1
	}
	return max(0, 1-0.2*float32(level))
}

// effectPackets 返回当前全部持续效果的 Update Mob Effect 包（登录/重生同步）。
func (s *session) effectPackets() [][]byte {
	s.stateMu.Lock()
	packets := make([][]byte, 0, 4)
	for id := range s.effects {
		if s.effects[id].active {
			packets = append(packets, effectPacket(s.entityID, int32(id), s.effects[id]))
		}
	}
	s.stateMu.Unlock()
	return packets
}
