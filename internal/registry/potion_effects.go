//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package registry

// PotionEffect 是一个药水携带的效果：效果注册表 ID、持续时间 tick 与等级。
type PotionEffect struct {
	ID        int32
	Duration  int32
	Amplifier int32
}

// potionEffects 对照 1.21.11 net.minecraft.world.item.alchemy.Potions 的
// cfm(effect, duration[, amplifier]) 构造；duration 为 0/1 的瞬间效果由调用方
// 立即结算。PotionID 与 PotionWater 等常量一致。
var potionEffects = map[int32][]PotionEffect{
	PotionNightVision:        {{MobEffectNightVision, 3600, 0}},
	PotionLongNightVision:    {{MobEffectNightVision, 9600, 0}},
	PotionInvisibility:       {{MobEffectInvisibility, 3600, 0}},
	PotionLongInvisibility:   {{MobEffectInvisibility, 9600, 0}},
	PotionLeaping:            {{MobEffectJumpBoost, 3600, 0}},
	PotionLongLeaping:        {{MobEffectJumpBoost, 9600, 0}},
	PotionStrongLeaping:      {{MobEffectJumpBoost, 1800, 1}},
	PotionFireResistance:     {{MobEffectFireResistance, 3600, 0}},
	PotionLongFireResistance: {{MobEffectFireResistance, 9600, 0}},
	PotionSwiftness:          {{MobEffectSpeed, 3600, 0}},
	PotionLongSwiftness:      {{MobEffectSpeed, 9600, 0}},
	PotionStrongSwiftness:    {{MobEffectSpeed, 1800, 1}},
	PotionSlowness:           {{MobEffectSlowness, 1800, 0}},
	PotionLongSlowness:       {{MobEffectSlowness, 4800, 0}},
	PotionStrongSlowness:     {{MobEffectSlowness, 400, 3}},
	PotionTurtleMaster:       {{MobEffectSlowness, 400, 3}, {MobEffectResistance, 400, 2}},
	PotionLongTurtleMaster:   {{MobEffectSlowness, 800, 3}, {MobEffectResistance, 800, 2}},
	PotionStrongTurtleMaster: {{MobEffectSlowness, 400, 5}, {MobEffectResistance, 400, 3}},
	PotionWaterBreathing:     {{MobEffectWaterBreathing, 3600, 0}},
	PotionLongWaterBreathing: {{MobEffectWaterBreathing, 9600, 0}},
	PotionHealing:            {{MobEffectInstantHealth, 1, 0}},
	PotionStrongHealing:      {{MobEffectInstantHealth, 1, 1}},
	PotionHarming:            {{MobEffectInstantDamage, 1, 0}},
	PotionStrongHarming:      {{MobEffectInstantDamage, 1, 1}},
	PotionPoison:             {{MobEffectPoison, 900, 0}},
	PotionLongPoison:         {{MobEffectPoison, 1800, 0}},
	PotionStrongPoison:       {{MobEffectPoison, 432, 1}},
	PotionRegeneration:       {{MobEffectRegeneration, 900, 0}},
	PotionLongRegeneration:   {{MobEffectRegeneration, 1800, 0}},
	PotionStrongRegeneration: {{MobEffectRegeneration, 450, 1}},
	PotionStrength:           {{MobEffectStrength, 3600, 0}},
	PotionLongStrength:       {{MobEffectStrength, 9600, 0}},
	PotionStrongStrength:     {{MobEffectStrength, 1800, 1}},
	PotionWeakness:           {{MobEffectWeakness, 1800, 0}},
	PotionLongWeakness:       {{MobEffectWeakness, 4800, 0}},
	PotionLuck:               {{MobEffectLuck, 6000, 0}},
	PotionSlowFalling:        {{MobEffectSlowFalling, 1800, 0}},
	PotionLongSlowFalling:    {{MobEffectSlowFalling, 4800, 0}},
	PotionWindCharged:        {{MobEffectWindCharged, 3600, 0}},
	PotionWeaving:            {{MobEffectWeaving, 3600, 0}},
	PotionOozing:             {{MobEffectOozing, 3600, 0}},
	PotionInfested:           {{MobEffectInfested, 3600, 0}},
}

// PotionEffectList 返回药水的效果列表；未知/无效果药水返回 nil。
func PotionEffectList(potionID int32) []PotionEffect {
	return potionEffects[potionID]
}
