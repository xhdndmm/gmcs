//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"testing"

	"gmcs/internal/config"
	"gmcs/internal/world"
)

// 天气基准：确认天气推进不会给主 Tick 带来可见开销。

// BenchmarkWeatherTick 衡量稳态天气推进（无状态翻转、无包编码）的固定成本：
// 只有两个计时递减、两个浮点强度推进与一次锁。
func BenchmarkWeatherTick(b *testing.B) {
	instance := newWeatherBenchServer(b)
	instance.weatherMu.Lock()
	instance.weather = weatherState{raining: true, rainTicks: 1 << 30, thunderTicks: 1 << 30}
	instance.weather.rainLevel = 1
	instance.weather.thunderLevel = 1
	instance.weatherMu.Unlock()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		instance.tickWeather(nil)
	}
}

// BenchmarkWeatherTickTransition 衡量下雨状态翻转那一 tick 的成本：
// 需要编码 3 个 Game Event 包（开始/停止 + 雷/雨强度）。
func BenchmarkWeatherTickTransition(b *testing.B) {
	instance := newWeatherBenchServer(b)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		// 每次迭代把雨计时压到 1，下一 tick 必然翻转并广播。
		instance.weatherMu.Lock()
		instance.weather.rainTicks = 1
		instance.weather.thunderTicks = 1000
		instance.weatherMu.Unlock()
		instance.tickWeather(nil)
	}
}

// newWeatherBenchServer 构造一个已启用天气的轻量服务器。
func newWeatherBenchServer(b *testing.B) *Server {
	b.Helper()
	cfg := config.Default()
	cfg.WorldDir = b.TempDir()
	cfg.SpawnMonsters = false
	instance, err := New(cfg)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = instance.worldFor(world.DimensionOverworld).Close() })
	return instance
}
