//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"gmcs/internal/config"
	"gmcs/internal/protocol"
)

// difficultyID 返回当前世界难度（原版 Difficulty 序号：0–3）。
func (s *Server) difficultyID() uint8 {
	return uint8(s.difficulty.Load())
}

// difficultyLocked 返回客户端是否不能修改难度。
func (s *Server) difficultyLocked() bool {
	return s.difficultyLockFlag.Load()
}

// difficultyPacket 构造 Change Difficulty 包。
func (s *Server) difficultyPacket() []byte {
	return protocol.EncodeChangeDifficulty(s.difficultyID(), s.difficultyLocked())
}

// setDifficulty 更新服务器难度并返回 Change Difficulty 包。
// 目前只由启动配置/测试调用；运行时命令可复用该入口。
func (s *Server) setDifficulty(difficulty config.Difficulty, locked bool) []byte {
	s.difficulty.Store(uint32(difficulty))
	s.difficultyLockFlag.Store(locked)
	return s.difficultyPacket()
}

// peaceful 报告当前是否为和平难度。
func (s *Server) peaceful() bool {
	return s.difficultyID() == uint8(config.DifficultyPeaceful)
}
