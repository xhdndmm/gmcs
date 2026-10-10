package world

import (
	"math"
	"sync"
)

// 洞穴生成：奶酪洞穴（3D 噪声粗网格 + 三线性插值）与蠕虫隧道
// （确定性随机游走 + 球体雕刻）。
//
// 设计约束：
//   - 确定性：只依赖 (种子, 区块坐标)，与生成顺序无关；
//   - 只雕刻区块内部的方块，隧道在区块边界处收束（不会修改相邻区块）；
//   - 保持“地表密封”：每列最高 1–2 格方块不雕刻（height-1 及以上），
//     避免出现“草皮悬空 / 湖底漏洞”；斜坡处仍会自然形成洞口；
//   - 深处（y ≤ caveLavaLevel）雕刻出的空腔用岩浆填充，与原版岩浆湖一致；
//   - 不雕刻水方块，避免湖水“漏进”洞穴。

const (
	// caveLavaLevel 是洞穴岩浆的液面高度。
	caveLavaLevel = -54
	// caveCheeseSalt/caveWormSalt 是两类洞穴的随机盐。
	caveCheeseSalt = 0x1DEA5C0FFEE12345
	caveWormSalt   = 0x0BADC0FFEE0DDF00
	// caveGridStep 是 3D 噪声的粗网格步长（方块）。
	caveGridStep = 4
	// caveGridStepY 是 Y 方向的粗网格步长（比水平方向粗，减少采样）。
	caveGridStepY = 8
	// caveNoisePeriod 是洞穴噪声的格点周期。
	caveNoisePeriod = 42
	// caveHeightLimit 之上不再雕刻洞穴（与原版高山洞穴稀少一致）。
	caveHeightLimit = 110
)

// carveCaves 在已填充地形上雕刻洞穴与隧道。只操作未发布的区块（无锁）。
func (g SeededGenerator) carveCaves(chunk *Chunk, columns *[SectionSize * SectionSize]columnData, maxHeight int) {
	sampler := newCaveSampler(g.Seed^caveCheeseSalt, maxHeight)
	g.carveCheeseCaves(chunk, columns, sampler)
	sampler.release()
	g.carveWormTunnels(chunk, columns)
}

// carveCheeseCaves 按 3D 噪声雕刻大型洞穴。
//
// 性能：粗网格上做两级判断——先看单元格 8 个角的噪声最大值，
// 全部低于阈值时整格（4×8×4 = 128 个方块）直接跳过，避免逐块插值；
// 只有“可能含洞”的单元格才逐块三线性插值。
func (g SeededGenerator) carveCheeseCaves(chunk *Chunk, columns *[SectionSize * SectionSize]columnData, sampler *caveSampler) {
	const threshold = 0.66
	for cellX := 0; cellX < SectionSize; cellX += caveGridStep {
		for cellZ := 0; cellZ < SectionSize; cellZ += caveGridStep {
			// 单元格覆盖的最大地表高度（用于提前跳过近地表/天空部分）。
			maxHeight := WorldMinY
			for x := cellX; x < cellX+caveGridStep; x++ {
				for z := cellZ; z < cellZ+caveGridStep; z++ {
					maxHeight = max(maxHeight, columns[x*SectionSize+z].height)
				}
			}
			maxY := min(maxHeight-2, caveHeightLimit)
			for cellY := WorldMinY; cellY <= maxY; cellY += caveGridStepY {
				if !sampler.cellMaybeCave(cellX, cellY, cellZ, threshold) {
					continue
				}
				highY := min(cellY+caveGridStepY-1, maxY)
				for x := cellX; x < cellX+caveGridStep; x++ {
					for z := cellZ; z < cellZ+caveGridStep; z++ {
						height := columns[x*SectionSize+z].height
						columnMaxY := min(height-2, caveHeightLimit)
						for y := max(cellY, WorldMinY+2); y <= highY && y <= columnMaxY; y++ {
							if sampler.at(x, y, z) > threshold {
								g.carveBlock(chunk, x, y, z)
							}
						}
					}
				}
			}
		}
	}
}

// carveWormTunnels 雕刻蠕虫隧道：每条隧道由区块自身的哈希派生，
// 起点、方向与长度都是确定性的；隧道被约束在本区块内（边界反弹）。
func (g SeededGenerator) carveWormTunnels(chunk *Chunk, columns *[SectionSize * SectionSize]columnData) {
	worms := 0
	if hashUnit(g.Seed^caveWormSalt, chunk.X, chunk.Z) < 0.55 {
		worms = 1
	}
	if hashUnit(g.Seed^caveWormSalt^0x5555, chunk.X, chunk.Z) < 0.22 {
		worms++
	}
	for i := 0; i < worms; i++ {
		sequence := newHashSeq(g.Seed^caveWormSalt^int64(uint64(i)<<32), chunk.X*31+i, chunk.Z*17-i)
		g.carveWorm(chunk, columns, sequence)
	}
}

// carveWorm 雕刻一条随机游走的隧道。
func (g SeededGenerator) carveWorm(chunk *Chunk, columns *[SectionSize * SectionSize]columnData, sequence *hashSeq) {
	positionX := 2 + sequence.nextFloat()*12
	positionZ := 2 + sequence.nextFloat()*12
	height := columns[int(positionX)*SectionSize+int(positionZ)].height
	lowY := float64(WorldMinY + 6)
	highY := float64(min(height-8, 96))
	if highY <= lowY {
		return
	}
	positionY := lowY + sequence.nextFloat()*(highY-lowY)

	angle := sequence.nextFloat() * 2 * math.Pi
	pitch := (sequence.nextFloat() - 0.5) * 0.6
	length := 60 + sequence.nextInt(60)
	radius := 1.4 + sequence.nextFloat()*0.9

	for step := 0; step < length; step++ {
		// 方向随机扰动 + 前进。
		angle += (sequence.nextFloat() - 0.5) * 0.5
		pitch += (sequence.nextFloat() - 0.5) * 0.35
		pitch = math.Max(-0.8, math.Min(0.8, pitch))
		positionX += math.Cos(angle) * 0.9
		positionZ += math.Sin(angle) * 0.9
		positionY += pitch * 0.9

		// 边界反弹（隧道保持在区块内，跨区块一致性由相邻区块共同保证：
		// 隧道不越界，因此不需要跨区块雕刻）。
		if positionX < 1 {
			positionX, angle = 1, math.Pi-angle
		} else if positionX > SectionSize-2 {
			positionX, angle = SectionSize-2, math.Pi-angle
		}
		if positionZ < 1 {
			positionZ, angle = 1, -angle
		} else if positionZ > SectionSize-2 {
			positionZ, angle = SectionSize-2, -angle
		}
		// 垂直约束：保持在地下（随所在列的地形高度动态调整）。
		columnX, columnZ := int(positionX), int(positionZ)
		columnHeight := columns[columnX*SectionSize+columnZ].height
		maxY := float64(min(columnHeight-6, 96))
		if positionY > maxY {
			positionY, pitch = maxY, -math.Abs(pitch)
		}
		if positionY < float64(WorldMinY+5) {
			positionY, pitch = float64(WorldMinY+5), math.Abs(pitch)
		}

		g.carveSphere(chunk, columns, positionX, positionY, positionZ, radius)
	}
}

// carveSphere 在 (cx, cy, cz) 处雕刻一个球状空腔（半径 r）。
// 半径逐块递减的变体会使隧道更平滑，但保持简单：固定半径 + 每步前进 < r。
func (g SeededGenerator) carveSphere(chunk *Chunk, columns *[SectionSize * SectionSize]columnData, centerX, centerY, centerZ, radius float64) {
	radiusSquared := radius * radius
	baseX, baseY, baseZ := int(math.Floor(centerX)), int(math.Floor(centerY)), int(math.Floor(centerZ))
	for dx := -int(radius) - 1; dx <= int(radius)+1; dx++ {
		for dy := -int(radius) - 1; dy <= int(radius)+1; dy++ {
			for dz := -int(radius) - 1; dz <= int(radius)+1; dz++ {
				fx, fy, fz := float64(dx), float64(dy), float64(dz)
				if fx*fx+fy*fy+fz*fz > radiusSquared {
					continue
				}
				x, y, z := baseX+dx, baseY+dy, baseZ+dz
				if x < 0 || x >= SectionSize || z < 0 || z >= SectionSize {
					continue
				}
				height := columns[x*SectionSize+z].height
				if y <= WorldMinY+1 || y >= height-1 {
					continue // 保留基岩与顶部密封（至少 1 格）
				}
				if y > caveHeightLimit {
					continue
				}
				g.carveBlock(chunk, x, y, z)
			}
		}
	}
}

// carveBlock 把一个方块变为洞（空气）或岩浆（深处），保留水与已有空气。
func (g SeededGenerator) carveBlock(chunk *Chunk, x, y, z int) {
	state := chunk.getBlockStateLocked(x, y, z)
	if state == AirBlock || state == WaterBlock {
		return
	}
	if state == BedrockBlock {
		return
	}
	if y <= caveLavaLevel {
		chunk.setBlockStateDirect(x, y, z, LavaBlock)
		return
	}
	chunk.setBlockStateDirect(x, y, z, AirBlock)
}

// caveSampler 是 3D 洞穴噪声的粗网格采样器：在格点 (4, 8, 4) 上求值，
// 块级查询用三线性插值还原，避免对每个方块调用 8 次哈希的 3D 噪声。
type caveSampler struct {
	// values 是网格采样值（复用缓冲池，避免每次生成分配）。
	values []float32
	yBase  int
	yCount int
	// strideX/strideZ 是扁平索引步长（index = xi*strideX + zi*strideZ + yi）。
	strideX, strideZ int
}

// caveSamplePool 复用洞穴噪声采样缓冲（约 4.7 KiB/次）。
var caveSamplePool = sync.Pool{
	New: func() any {
		buffer := make([]float32, 0, 1600)
		return &buffer
	},
}

// 采样网格尺寸：X/Z 为区块坐标 -4..20（步长 caveGridStep），Y 为
// WorldMinY..maxHeight+8（步长 caveGridStepY）。
const caveXZCount = SectionSize/caveGridStep + 3

// newCaveSampler 采样并缓存一个区块范围的洞穴噪声。
func newCaveSampler(seed int64, maxHeight int) *caveSampler {
	yCount := (maxHeight+caveGridStepY-WorldMinY)/caveGridStepY + 2
	needed := caveXZCount * caveXZCount * yCount
	buffer := caveSamplePool.Get().(*[]float32)
	values := (*buffer)[:0]
	if cap(values) < needed {
		values = make([]float32, needed)
	}
	values = values[:needed]
	index := 0
	for xi := 0; xi < caveXZCount; xi++ {
		worldX := xi*caveGridStep - caveGridStep
		for zi := 0; zi < caveXZCount; zi++ {
			worldZ := zi*caveGridStep - caveGridStep
			for yi := 0; yi < yCount; yi++ {
				worldY := WorldMinY + yi*caveGridStepY
				values[index] = float32(valueNoise3D(seed, worldX, worldY, worldZ, caveNoisePeriod))
				index++
			}
		}
	}
	*buffer = values
	// 缓冲在 at() 使用期间一直被 sampler 持有；归还由调用方完成。
	return &caveSampler{
		values:  values,
		yBase:   WorldMinY,
		yCount:  yCount,
		strideX: caveXZCount * yCount,
		strideZ: yCount,
	}
}

// release 把采样缓冲归还池（必须在 sampler 不再使用后调用）。
func (s *caveSampler) release() {
	buffer := s.values[:0]
	caveSamplePool.Put(&buffer)
	s.values = nil
}

// cellMaybeCave 检查单元格（对齐到采样网格）的 8 个角是否可能超过阈值。
// 只要有一个角的噪声超过阈值就需要逐块插值。
func (s *caveSampler) cellMaybeCave(cellX, cellY, cellZ int, threshold float32) bool {
	xi := cellX/caveGridStep + 1
	zi := cellZ/caveGridStep + 1
	yi := (cellY - s.yBase) / caveGridStepY
	if yi < 0 || yi+1 >= s.yCount || xi < 0 || xi+1 >= caveXZCount || zi < 0 || zi+1 >= caveXZCount {
		// 越界单元格按“可能需要雕刻”处理（at 内部会做边界钳制）。
		return true
	}
	base := xi*s.strideX + zi*s.strideZ + yi
	values := s.values
	if values[base] > threshold || values[base+1] > threshold ||
		values[base+s.strideZ] > threshold || values[base+s.strideZ+1] > threshold ||
		values[base+s.strideX] > threshold || values[base+s.strideX+1] > threshold ||
		values[base+s.strideX+s.strideZ] > threshold || values[base+s.strideX+s.strideZ+1] > threshold {
		return true
	}
	return false
}

// at 返回区块局部坐标 (x, y, z) 处的洞穴噪声（三线性插值）。
func (s *caveSampler) at(x, y, z int) float64 {
	fx := float64(x+caveGridStep) / caveGridStep
	fy := float64(y-s.yBase) / caveGridStepY
	fz := float64(z+caveGridStep) / caveGridStep

	xi := int(fx)
	yi := int(fy)
	zi := int(fz)
	xi = max(0, min(xi, caveXZCount-2))
	zi = max(0, min(zi, caveXZCount-2))
	yi = max(0, min(yi, s.yCount-2))

	tx := clamp01(fx - float64(xi))
	ty := clamp01(fy - float64(yi))
	tz := clamp01(fz - float64(zi))

	values := s.values
	base := xi*s.strideX + zi*s.strideZ + yi
	strideX, strideZ := s.strideX, s.strideZ
	v000 := float64(values[base])
	v100 := float64(values[base+strideX])
	v010 := float64(values[base+1])
	v110 := float64(values[base+strideX+1])
	v001 := float64(values[base+strideZ])
	v101 := float64(values[base+strideX+strideZ])
	v011 := float64(values[base+strideZ+1])
	v111 := float64(values[base+strideX+strideZ+1])

	bottomX := v000 + (v100-v000)*tx
	bottomY := v010 + (v110-v010)*tx
	topX := v001 + (v101-v001)*tx
	topY := v011 + (v111-v011)*tx
	bottom := bottomX + (bottomY-bottomX)*ty
	top := topX + (topY-topX)*ty
	return bottom + (top-bottom)*tz
}
