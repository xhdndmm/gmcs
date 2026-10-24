//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"math"

	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 天气系统：对齐原版 ServerLevel.advanceWeatherCycle（1.21.11 字节码核对）与
// Level.getPrecipitationAt / isRainingAt。
//
// 原版把天气状态存放在主世界的 level.dat（ServerLevelData 的 raining、
// thundering、rainTime、thunderTime、clearWeatherTime），只有具有天空光照的
// 维度推进周期；客户端通过 ClientboundGameEventPacket 接收变化：
//
//	1  START_RAINING        (value 0)
//	2  STOP_RAINING         (value 0)
//	7  RAIN_LEVEL_CHANGE    (rainLevel 0..1)
//	8  THUNDER_LEVEL_CHANGE (thunderLevel 0..1)
//
// 每个 tick 的顺序（原版字节码 对称移植）：
//
//  1. clearWeatherTime > 0：递减，thunderTime = thundering ? 0 : 1，
//     rainTime = raining ? 0 : 1，然后 raining/thundering 置 false；
//  2. 否则：thunderTime > 0 递减，减到 0 时翻转 thundering；为 0 时按当前状态
//     重掷区间（打雷 12000–24000，否则 12000–180000）。rainTime 同理
//     （下雨 3600–15600，否则 12000–180000）；
//  3. thunderLevel/rainLevel 以 0.01 的步长逼近 0/1 并 clamp，值变化时广播
//     事件 8 / 7；
//  4. raining 发生翻转时广播 2（停止）或 1（开始），并补发一次 7、8。
//
// 已知差异（无客户端对测，记录于 docs/TODO.md）：
//   - 天气状态只在内存中，重载服务器后从“晴朗”重新开始（原版写入 level.dat）；
//   - 群系降水类型（雪/无降水）与温度未建模：主世界所有群系按降雨处理；
//   - 雷暴的闪电（LightningBolt）、炼药锅积水、雪层堆积未实现。
const (
	// gameEventStartRaining/gameEventStopRaining 对应 ClientboundGameEventPacket.Type 1/2。
	gameEventStartRaining  = 1
	gameEventStopRaining   = 2
	gameEventRainLevel     = 7
	gameEventThunderLevel  = 8
	weatherLevelStep       = 0.01
	weatherDefaultRandSeed = 0x2545F4914F6CDD1D

	// 雨天浇灭火焰的概率（原版 FireBlock.tick：0.2F + age * 0.03F）。
	weatherFireExtinguishBase   = 0.2
	weatherFireExtinguishPerAge = 0.03
	// weatherMaxCommandSeconds 是 /weather 时长的上限（原版整数参数上限 1000000 秒）。
	weatherMaxCommandSeconds = 1000000
)

// 原版 ServerLevel 的天气计时区间（IntProvider.uniform 的上下界，含两端），
// 来自 1.21.11 static 初始化：b/c 为雷，B/d 为雨。
const (
	weatherThunderTimeThunderingMin = 12000
	weatherThunderTimeThunderingMax = 24000
	weatherThunderTimeIdleMin       = 12000
	weatherThunderTimeIdleMax       = 180000
	weatherRainTimeRainingMin       = 3600
	weatherRainTimeRainingMax       = 15600
	weatherRainTimeIdleMin          = 12000
	weatherRainTimeIdleMax          = 180000
)

// weatherState 是主世界天气状态（原版 ServerLevelData 的对应字段 + 客户端平滑用的等级）。
type weatherState struct {
	raining    bool
	thundering bool
	// clearTicks 是 /weather clear 指定的强制放晴剩余 tick（原版 clearWeatherTime）。
	clearTicks int32
	// rainTicks/thunderTicks 是当前状态剩余 tick（原版 rainTime/thunderTime）。
	rainTicks    int32
	thunderTicks int32
	// rainLevel/thunderLevel 是 0..1 的降水强度（客户端按 oRainLevel 插值渲染），
	// oRainLevel/oThunderLevel 是上一 tick 的值。
	rainLevel     float32
	oRainLevel    float32
	thunderLevel  float32
	oThunderLevel float32
}

// weatherRandom 返回天气系统专用的确定性伪随机数。
// 与 nextRandom 分开是为了不让天气抽取打乱生物/火焰等既有随机序列
// （否则所有既有测试的随机流都会改变）。
func (s *Server) weatherRandom() uint64 {
	for {
		state := s.weatherRand.Load()
		next := state + 0x9E3779B97F4A7C15
		if s.weatherRand.CompareAndSwap(state, next) {
			z := next
			z ^= z >> 30
			z *= 0xBF58476D1CE4E5B9
			z ^= z >> 27
			z *= 0x94D049BB133111EB
			return z ^ (z >> 31)
		}
	}
}

// weatherUniform 返回 [min, max] 区间内的确定性随机整数（原版 IntProvider.uniform）。
func (s *Server) weatherUniform(min, max int) int {
	if max <= min {
		return min
	}
	return min + int(s.weatherRandom()%uint64(max-min+1))
}

// weatherSnapshot 返回当前天气状态的副本。
func (s *Server) weatherSnapshot() weatherState {
	s.weatherMu.Lock()
	state := s.weather
	s.weatherMu.Unlock()
	return state
}

// isRaining 报告主世界当前是否下雨（原版 LevelData.isRaining）。
// 无锁读取快照：实体环境扫描每 tick 都会询问，避免互斥开销。
func (s *Server) isRaining() bool {
	return s.rainingFlag.Load()
}

// isThundering 报告主世界当前是否雷暴（原版 LevelData.isThundering）。
func (s *Server) isThundering() bool {
	s.weatherMu.Lock()
	thundering := s.weather.thundering
	s.weatherMu.Unlock()
	return thundering
}

// setWeatherParameters 设置天气（原版 ServerLevel.setWeatherParameters）：
// clearTicks 是强制放晴时长，weatherTicks 同时写入 rainTime 与 thunderTime。
func (s *Server) setWeatherParameters(clearTicks, weatherTicks int32, raining, thundering bool) {
	s.weatherMu.Lock()
	s.weather.clearTicks = clearTicks
	s.weather.rainTicks = weatherTicks
	s.weather.thunderTicks = weatherTicks
	s.weather.raining = raining
	s.weather.thundering = thundering
	s.weatherMu.Unlock()
	s.rainingFlag.Store(raining)
}

// tickWeather 推进一帧天气并广播变化（由主 tick 调用）。
// weatherEnabled 为 false 时不做任何事（测试用确定性钩子，见 joinServer）。
func (s *Server) tickWeather(players []*session) {
	if !s.weatherEnabled {
		return
	}
	s.weatherMu.Lock()
	previousRaining := s.weather.raining
	if s.weather.clearTicks > 0 {
		s.weather.clearTicks--
		// 原版：强制放晴期间把两个计时压到 0/1，并把状态清零
		// （thunderTime = thundering ? 0 : 1、rainTime = raining ? 0 : 1）。
		if s.weather.thundering {
			s.weather.thunderTicks = 0
		} else {
			s.weather.thunderTicks = 1
		}
		if s.weather.raining {
			s.weather.rainTicks = 0
		} else {
			s.weather.rainTicks = 1
		}
		s.weather.raining = false
		s.weather.thundering = false
	} else {
		// 雷计时。
		if s.weather.thunderTicks > 0 {
			s.weather.thunderTicks--
			if s.weather.thunderTicks == 0 {
				s.weather.thundering = !s.weather.thundering
			}
		} else if s.weather.thundering {
			s.weather.thunderTicks = int32(s.weatherUniform(weatherThunderTimeThunderingMin, weatherThunderTimeThunderingMax))
		} else {
			s.weather.thunderTicks = int32(s.weatherUniform(weatherThunderTimeIdleMin, weatherThunderTimeIdleMax))
		}
		// 雨计时。
		if s.weather.rainTicks > 0 {
			s.weather.rainTicks--
			if s.weather.rainTicks == 0 {
				s.weather.raining = !s.weather.raining
			}
		} else if s.weather.raining {
			s.weather.rainTicks = int32(s.weatherUniform(weatherRainTimeRainingMin, weatherRainTimeRainingMax))
		} else {
			s.weather.rainTicks = int32(s.weatherUniform(weatherRainTimeIdleMin, weatherRainTimeIdleMax))
		}
	}
	// 雷暴强度平滑（原版先雷雨后雨）。
	s.weather.oThunderLevel = s.weather.thunderLevel
	s.weather.thunderLevel = advanceWeatherLevel(s.weather.thunderLevel, s.weather.thundering)
	thunderChanged := s.weather.oThunderLevel != s.weather.thunderLevel
	s.weather.oRainLevel = s.weather.rainLevel
	s.weather.rainLevel = advanceWeatherLevel(s.weather.rainLevel, s.weather.raining)
	rainChanged := s.weather.oRainLevel != s.weather.rainLevel
	raining := s.weather.raining
	transitioned := previousRaining != raining
	thunderLevel := s.weather.thunderLevel
	rainLevel := s.weather.rainLevel
	s.weatherMu.Unlock()
	// 发布无锁快照（IsRaining 热路径读）。
	s.rainingFlag.Store(raining)

	var packets [3][]byte
	count := 0
	if thunderChanged {
		packets[count] = protocol.EncodeGameEvent(gameEventThunderLevel, thunderLevel)
		count++
	}
	if rainChanged {
		packets[count] = protocol.EncodeGameEvent(gameEventRainLevel, rainLevel)
		count++
	}
	if transitioned {
		event := uint8(gameEventStartRaining)
		if !raining {
			event = gameEventStopRaining
		}
		s.broadcastToOverworld(protocol.EncodeGameEvent(event, 0), players)
		// 原版在翻转后补发一次雷/雨强度，保证客户端立刻对齐。
		s.broadcastToOverworld(protocol.EncodeGameEvent(gameEventThunderLevel, thunderLevel), players)
		s.broadcastToOverworld(protocol.EncodeGameEvent(gameEventRainLevel, rainLevel), players)
	}
	for i := 0; i < count; i++ {
		s.broadcastToOverworld(packets[i], players)
	}
}

// advanceWeatherLevel 把天气强度朝目标值推进 0.01 并 clamp 到 [0, 1]。
func advanceWeatherLevel(level float32, rising bool) float32 {
	if rising {
		level += weatherLevelStep
	} else {
		level -= weatherLevelStep
	}
	if level < 0 {
		return 0
	}
	if level > 1 {
		return 1
	}
	return level
}

// broadcastToOverworld 把天气包发给主世界已加入的玩家（原版天气只影响主世界）。
func (s *Server) broadcastToOverworld(packet []byte, players []*session) {
	for _, player := range players {
		if player.isJoined() && player.dimensionID() == world.DimensionOverworld {
			player.tryWrite(packet)
		}
	}
}

// weatherJoinPackets 返回玩家进入主世界时需要补发的天气包。
// 原版只在天气变化时广播（ServerLevel.advanceWeatherCycle），新加入的玩家
// 依赖随后的变化包；gmcs 在进入世界时主动同步一次，避免中途加入的客户端
// 在雨停前看不到雨（差异记录于 docs/MIGRATION.md）。
func (s *Server) weatherJoinPackets() [][]byte {
	if !s.weatherEnabled {
		return nil
	}
	state := s.weatherSnapshot()
	packets := make([][]byte, 0, 3)
	if state.raining {
		packets = append(packets, protocol.EncodeGameEvent(gameEventStartRaining, 0))
	} else {
		packets = append(packets, protocol.EncodeGameEvent(gameEventStopRaining, 0))
	}
	packets = append(packets,
		protocol.EncodeGameEvent(gameEventThunderLevel, state.thunderLevel),
		protocol.EncodeGameEvent(gameEventRainLevel, state.rainLevel),
	)
	return packets
}

// isRainingAt 报告指定方块位置是否会下雨（原版 Level.isRainingAt →
// getPrecipitationAt：未下雨 / 看不到天空 / 上方有阻挡方块时为否）。
// 群系降水类型未建模，主世界一律按下雨处理。
func (s *Server) isRainingAt(dim world.Dimension, x, y, z int) bool {
	if dim != world.DimensionOverworld || !s.isRaining() {
		return false
	}
	w := s.worldFor(dim)
	if w == nil || !w.ChunkLoaded(x, z) {
		return false
	}
	return skyExposedAt(w, x, y, z)
}

// entityInRain 报告实体是否处于雨中（原版 Entity.isInRain：脚部方块与
// 包围盒顶部所在方块各判定一次）。maxY 是包围盒顶部的世界坐标。
func (s *Server) entityInRain(dim world.Dimension, x, y, z, maxY float64) bool {
	if !s.isRainingAt(dim, int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))) {
		return false
	}
	top := int(math.Floor(maxY))
	if top == int(math.Floor(y)) {
		return true
	}
	return s.isRainingAt(dim, int(math.Floor(x)), top, int(math.Floor(z)))
}

// skyExposedAt 报告方块位置上方（含自身）没有阻挡降水的方块。
// 原版用 Heightmap.MOTION_BLOCKING 与 BlockState.blocksMotion()；
// gmcs 用“该状态有碰撞形状”近似（水/火/草/花等不阻挡降水）。
func skyExposedAt(w *world.World, x, y, z int) bool {
	column, ok := w.ColumnAt(x, z)
	if !ok {
		return false
	}
	if !column.HasTop || column.TopY < y {
		// 该位置及以上没有任何方块。
		return true
	}
	var buf [16]uint16
	for start := y; start <= column.TopY; start += len(buf) {
		end := start + len(buf) - 1
		if end > column.TopY {
			end = column.TopY
		}
		n := w.LoadedColumn(x, start, end, z, buf[:])
		for i := 0; i < n; i++ {
			if registry.BlockStateCollides(buf[i]) {
				return false
			}
		}
	}
	return true
}

// fireRainingAround 报告火焰周围是否降雨（原版 FireBlock.isRainingAround：
// 火焰自身与四个水平相邻位置各判定一次）。
func (s *Server) fireRainingAround(dim world.Dimension, x, y, z int) bool {
	if s.isRainingAt(dim, x, y, z) {
		return true
	}
	return s.isRainingAt(dim, x-1, y, z) ||
		s.isRainingAt(dim, x+1, y, z) ||
		s.isRainingAt(dim, x, y, z-1) ||
		s.isRainingAt(dim, x, y, z+1)
}

// weatherNextFloat 返回 [0,1) 的确定性随机浮点数（原版 RandomSource.nextFloat）。
func (s *Server) weatherNextFloat() float32 {
	return float32(s.weatherRandom()>>40) / float32(1<<24)
}
