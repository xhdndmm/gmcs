package server

import "gmcs/internal/world"

// testWorld 返回主世界（测试专用快捷方式：绝大多数测试只使用主世界，
// 维度相关的测试直接使用 worldFor）。
func (s *Server) testWorld() *world.World {
	return s.worldFor(world.DimensionOverworld)
}
