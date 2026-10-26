//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// 主世界级别状态持久化（gmcs 自定义 level.json，不是原版 level.dat）。
//
// 原版把世界时间与天气写入主世界的 level.dat（ServerLevelData 的
// dayTime/gameTime、raining、thundering、rainTime、thunderTime、
// clearWeatherTime）。gmcs 之前只把它们放在内存里，服务器重启后时间从
// 0 开始，表现为“重新连接后变成白天”。这里按同样的字段语义持久化，
// 并在自动保存与关闭时原子写盘。
const levelFileName = "level.json"

// levelState 是 level.json 的内容。
type levelState struct {
	// WorldAge 是游戏刻计数（原版 gameTime，同时用于 dayTime 取模）。
	WorldAge int64 `json:"world_age"`
	// 天气字段与原版 LevelData 一一对应。
	Raining      bool  `json:"raining,omitempty"`
	Thundering   bool  `json:"thundering,omitempty"`
	ClearTicks   int32 `json:"clear_ticks,omitempty"`
	RainTicks    int32 `json:"rain_ticks,omitempty"`
	ThunderTicks int32 `json:"thunder_ticks,omitempty"`
}

// levelSaveMu 串行化 level.json 的写入（自动保存 goroutine 与关闭路径并发）。
var levelSaveMu sync.Mutex

// loadLevelState 读取主世界级别状态；文件不存在时保持默认值（时间从 0 开始）。
// 文件损坏时返回错误，由调用方记录日志后继续运行。
func (s *Server) loadLevelState() error {
	path := filepath.Join(s.config.WorldDir, levelFileName)
	content, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("读取 %s：%w", path, err)
	}
	var state levelState
	if err := json.Unmarshal(content, &state); err != nil {
		return fmt.Errorf("解析 %s：%w", path, err)
	}
	if state.WorldAge < 0 {
		// 负数没有意义（原版 gameTime 单调递增），按 0 处理。
		state.WorldAge = 0
	}
	s.worldAge.Store(state.WorldAge)
	s.weatherMu.Lock()
	s.weather.raining = state.Raining
	s.weather.thundering = state.Thundering
	s.weather.clearTicks = state.ClearTicks
	s.weather.rainTicks = state.RainTicks
	s.weather.thunderTicks = state.ThunderTicks
	// 平滑等级直接对齐当前天气（避免重启后先播放一次过渡动画）。
	s.weather.rainLevel = boolLevel(state.Raining)
	s.weather.oRainLevel = s.weather.rainLevel
	s.weather.thunderLevel = boolLevel(state.Thundering)
	s.weather.oThunderLevel = s.weather.thunderLevel
	s.weatherMu.Unlock()
	s.rainingFlag.Store(state.Raining)
	slog.Info("loaded level state", "worldAge", state.WorldAge,
		"raining", state.Raining, "thundering", state.Thundering)
	return nil
}

// boolLevel 把布尔状态换算为 0/1 的天气强度。
func boolLevel(value bool) float32 {
	if value {
		return 1
	}
	return 0
}

// saveLevelState 原子写入主世界级别状态（世界时间与天气）。
func (s *Server) saveLevelState() error {
	state := levelState{WorldAge: s.worldAge.Load()}
	s.weatherMu.Lock()
	state.Raining = s.weather.raining
	state.Thundering = s.weather.thundering
	state.ClearTicks = s.weather.clearTicks
	state.RainTicks = s.weather.rainTicks
	state.ThunderTicks = s.weather.thunderTicks
	s.weatherMu.Unlock()

	content, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	path := filepath.Join(s.config.WorldDir, levelFileName)
	levelSaveMu.Lock()
	defer levelSaveMu.Unlock()
	temp := path + ".tmp"
	if err := os.WriteFile(temp, content, 0o644); err != nil {
		return fmt.Errorf("写入 %s：%w", temp, err)
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("重命名 %s：%w", path, err)
	}
	return nil
}
