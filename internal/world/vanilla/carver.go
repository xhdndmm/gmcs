//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"encoding/json"
	"fmt"
	"math"
)

type carverDefinition struct {
	name                       string
	kind                       string
	probability                float32
	y                          heightProvider
	yScale                     floatProvider
	lavaLevel                  heightAnchor
	horizontalRadiusMultiplier floatProvider
	verticalRadiusMultiplier   floatProvider
	floorLevel                 floatProvider
	verticalRotation           floatProvider
	shape                      canyonShape
}

type heightProvider struct {
	minimum heightAnchor
	maximum heightAnchor
}

type heightAnchor struct {
	kind  string
	value int
}

type floatProvider struct {
	kind     string
	minimum  float32
	maximum  float32
	plateau  float32
	constant float32
}

type canyonShape struct {
	distanceFactor              floatProvider
	horizontalRadiusFactor      floatProvider
	thickness                   floatProvider
	verticalRadiusCenterFactor  float32
	verticalRadiusDefaultFactor float32
	widthSmoothness             int
}

type carverRandom interface {
	carverInt(bound int) int
	carverFloat() float32
	carverLong() int64
}

func (r *legacyRandom) carverInt(bound int) int { return r.nextIntBounded(bound) }
func (r *legacyRandom) carverFloat() float32    { return r.nextFloat() }
func (r *legacyRandom) carverLong() int64       { return r.nextLong() }
func (r *Xoroshiro) carverInt(bound int) int    { return r.NextInt(bound) }
func (r *Xoroshiro) carverFloat() float32       { return r.NextFloat() }
func (r *Xoroshiro) carverLong() int64          { return r.NextLong() }

func (provider floatProvider) sample(random carverRandom) float32 {
	switch provider.kind {
	case "constant":
		return provider.constant
	case "minecraft:uniform":
		return provider.minimum + random.carverFloat()*(provider.maximum-provider.minimum)
	case "minecraft:trapezoid":
		rangeWidth := provider.maximum - provider.minimum
		plateauWidth := (rangeWidth - provider.plateau) / 2
		outerWidth := rangeWidth - plateauWidth
		return provider.minimum + random.carverFloat()*outerWidth + random.carverFloat()*plateauWidth
	default:
		panic("vanilla: 未解析的 carver float provider " + provider.kind)
	}
}

func (anchor heightAnchor) resolve(setting NoiseSetting) int {
	switch anchor.kind {
	case "absolute":
		return anchor.value
	case "above_bottom":
		return setting.MinY + anchor.value
	case "below_top":
		return setting.MinY + setting.Height - 1 - anchor.value
	default:
		panic("vanilla: 未解析的 carver height anchor " + anchor.kind)
	}
}

func (provider heightProvider) sample(random carverRandom, setting NoiseSetting) int {
	minimum := provider.minimum.resolve(setting)
	maximum := provider.maximum.resolve(setting)
	if maximum < minimum {
		return minimum
	}
	return minimum + random.carverInt(maximum-minimum+1)
}

func parseCarverDefinitions(data *Data) (map[string]carverDefinition, error) {
	definitions := make(map[string]carverDefinition, len(data.ConfiguredCarvers))
	for name, raw := range data.ConfiguredCarvers {
		var encoded struct {
			Type   string `json:"type"`
			Config struct {
				Probability                float32         `json:"probability"`
				Y                          json.RawMessage `json:"y"`
				YScale                     json.RawMessage `json:"yScale"`
				LavaLevel                  json.RawMessage `json:"lava_level"`
				HorizontalRadiusMultiplier json.RawMessage `json:"horizontal_radius_multiplier"`
				VerticalRadiusMultiplier   json.RawMessage `json:"vertical_radius_multiplier"`
				FloorLevel                 json.RawMessage `json:"floor_level"`
				VerticalRotation           json.RawMessage `json:"vertical_rotation"`
				Shape                      struct {
					DistanceFactor              json.RawMessage `json:"distance_factor"`
					HorizontalRadiusFactor      json.RawMessage `json:"horizontal_radius_factor"`
					Thickness                   json.RawMessage `json:"thickness"`
					VerticalRadiusCenterFactor  float32         `json:"vertical_radius_center_factor"`
					VerticalRadiusDefaultFactor float32         `json:"vertical_radius_default_factor"`
					WidthSmoothness             int             `json:"width_smoothness"`
				} `json:"shape"`
			} `json:"config"`
		}
		if err := json.Unmarshal(raw, &encoded); err != nil {
			return nil, fmt.Errorf("解析雕刻器 %s 失败：%w", name, err)
		}
		definition := carverDefinition{name: qualify(name), kind: qualify(encoded.Type), probability: encoded.Config.Probability}
		var err error
		if definition.y, err = parseHeightProvider(encoded.Config.Y); err != nil {
			return nil, fmt.Errorf("雕刻器 %s 的 y：%w", name, err)
		}
		if definition.yScale, err = parseFloatProvider(encoded.Config.YScale); err != nil {
			return nil, fmt.Errorf("雕刻器 %s 的 yScale：%w", name, err)
		}
		if definition.lavaLevel, err = parseHeightAnchor(encoded.Config.LavaLevel); err != nil {
			return nil, fmt.Errorf("雕刻器 %s 的 lava_level：%w", name, err)
		}
		if definition.horizontalRadiusMultiplier, err = parseFloatProvider(encoded.Config.HorizontalRadiusMultiplier); err != nil {
			return nil, fmt.Errorf("雕刻器 %s 的 horizontal_radius_multiplier：%w", name, err)
		}
		if definition.verticalRadiusMultiplier, err = parseFloatProvider(encoded.Config.VerticalRadiusMultiplier); err != nil {
			return nil, fmt.Errorf("雕刻器 %s 的 vertical_radius_multiplier：%w", name, err)
		}
		if definition.floorLevel, err = parseFloatProvider(encoded.Config.FloorLevel); err != nil {
			return nil, fmt.Errorf("雕刻器 %s 的 floor_level：%w", name, err)
		}
		if definition.verticalRotation, err = parseFloatProvider(encoded.Config.VerticalRotation); err != nil {
			return nil, fmt.Errorf("雕刻器 %s 的 vertical_rotation：%w", name, err)
		}
		if definition.shape.distanceFactor, err = parseFloatProvider(encoded.Config.Shape.DistanceFactor); err != nil {
			return nil, fmt.Errorf("雕刻器 %s 的 shape.distance_factor：%w", name, err)
		}
		if definition.shape.horizontalRadiusFactor, err = parseFloatProvider(encoded.Config.Shape.HorizontalRadiusFactor); err != nil {
			return nil, fmt.Errorf("雕刻器 %s 的 shape.horizontal_radius_factor：%w", name, err)
		}
		if definition.shape.thickness, err = parseFloatProvider(encoded.Config.Shape.Thickness); err != nil {
			return nil, fmt.Errorf("雕刻器 %s 的 shape.thickness：%w", name, err)
		}
		definition.shape.verticalRadiusCenterFactor = encoded.Config.Shape.VerticalRadiusCenterFactor
		definition.shape.verticalRadiusDefaultFactor = encoded.Config.Shape.VerticalRadiusDefaultFactor
		definition.shape.widthSmoothness = encoded.Config.Shape.WidthSmoothness
		definitions[definition.name] = definition
	}
	return definitions, nil
}

func parseHeightProvider(raw json.RawMessage) (heightProvider, error) {
	var encoded struct {
		Type         string          `json:"type"`
		Minimum      json.RawMessage `json:"min_inclusive"`
		Maximum      json.RawMessage `json:"max_inclusive"`
		MaximumBelow json.RawMessage `json:"max_exclusive"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return heightProvider{}, err
	}
	if len(encoded.Maximum) == 0 {
		encoded.Maximum = encoded.MaximumBelow
	}
	minimum, err := parseHeightAnchor(encoded.Minimum)
	if err != nil {
		return heightProvider{}, err
	}
	maximum, err := parseHeightAnchor(encoded.Maximum)
	if err != nil {
		return heightProvider{}, err
	}
	return heightProvider{minimum: minimum, maximum: maximum}, nil
}

func parseHeightAnchor(raw json.RawMessage) (heightAnchor, error) {
	var encoded map[string]int
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return heightAnchor{}, err
	}
	for kind, value := range encoded {
		switch kind {
		case "absolute", "above_bottom", "below_top":
			return heightAnchor{kind: kind, value: value}, nil
		}
	}
	return heightAnchor{}, fmt.Errorf("未知高度锚点 %s", raw)
}

func parseFloatProvider(raw json.RawMessage) (floatProvider, error) {
	if len(raw) == 0 {
		return floatProvider{}, nil
	}
	var constant float32
	if err := json.Unmarshal(raw, &constant); err == nil {
		return floatProvider{kind: "constant", constant: constant}, nil
	}
	var encoded struct {
		Type         string  `json:"type"`
		Minimum      float32 `json:"min_inclusive"`
		Maximum      float32 `json:"max_exclusive"`
		MinimumShort float32 `json:"min"`
		MaximumShort float32 `json:"max"`
		Plateau      float32 `json:"plateau"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return floatProvider{}, err
	}
	if encoded.Type == "minecraft:trapezoid" {
		return floatProvider{kind: encoded.Type, minimum: encoded.MinimumShort, maximum: encoded.MaximumShort, plateau: encoded.Plateau}, nil
	}
	if encoded.Type != "minecraft:uniform" {
		return floatProvider{}, fmt.Errorf("未知 float provider 类型 %q", encoded.Type)
	}
	return floatProvider{kind: encoded.Type, minimum: encoded.Minimum, maximum: encoded.Maximum}, nil
}

type carvingContext struct {
	router  *Router
	chunk   *GeneratedChunk
	aquifer *Aquifer
	mask    []bool
	water   SurfaceBlock
}

func (r *Router) carveChunk(chunk *GeneratedChunk, sampler *ChunkSampler, biomes *BiomeSource) error {
	if len(r.biomeCarvers) == 0 {
		return nil
	}
	context := &carvingContext{
		router:  r,
		chunk:   chunk,
		aquifer: sampler.Aquifer(),
		mask:    make([]bool, len(chunk.blocks)),
		water:   SurfaceBlock{Name: "minecraft:water", Properties: map[string]string{"level": "0"}},
	}
	random := newWorldgenRandom()
	for sourceX := chunk.ChunkX - 8; sourceX <= chunk.ChunkX+8; sourceX++ {
		for sourceZ := chunk.ChunkZ - 8; sourceZ <= chunk.ChunkZ+8; sourceZ++ {
			biome := biomes.BiomeAtBlock(sourceX*16, 0, sourceZ*16)
			for index, name := range r.biomeCarvers[biome] {
				definition, ok := r.carvers[qualify(name)]
				if !ok {
					return fmt.Errorf("群系 %s 引用了未知雕刻器 %s", biome, name)
				}
				random.setLargeFeatureSeed(r.seed+int64(index), sourceX, sourceZ)
				if random.nextFloat() > definition.probability {
					continue
				}
				switch definition.kind {
				case "minecraft:cave":
					context.carveCaves(definition, sourceX, sourceZ, random)
				case "minecraft:canyon":
					context.carveCanyon(definition, sourceX, sourceZ, random)
				default:
					return fmt.Errorf("不支持 overworld 雕刻器类型 %s", definition.kind)
				}
			}
		}
	}
	return nil
}

// carveCaves 对应原版 CaveWorldCarver.carve（1.21.11 字节码逐条复刻）。
func (c *carvingContext) carveCaves(definition carverDefinition, sourceX, sourceZ int, random *worldgenRandom) {
	// CaveWorldCarver.carve：count = nextInt(nextInt(nextInt(15) + 1) + 1)。
	count := random.nextIntBounded(random.nextIntBounded(random.nextIntBounded(15)+1) + 1)
	for range count {
		x := float64(sourceX*16 + random.nextIntBounded(16))
		y := float64(definition.y.sample(random, c.router.setting))
		z := float64(sourceZ*16 + random.nextIntBounded(16))
		horizontal := definition.horizontalRadiusMultiplier.sample(random)
		vertical := definition.verticalRadiusMultiplier.sample(random)
		floor := definition.floorLevel.sample(random)
		shouldSkip := func(nx, ny, nz float64, _ int) bool {
			return ny > float64(floor) || nx*nx+ny*ny+nz*nz >= 1
		}
		tunnelCount := 1
		if random.nextIntBounded(4) == 0 {
			yScale := definition.yScale.sample(random)
			roomRadius := float32(1) + random.nextFloat()*6
			// CaveWorldCarver.createRoom：水平半径 = 1.5 + sin(pi/2) * roomRadius，
			// 垂直半径 = 水平半径 * yScale。
			horizontalRadius := 1.5 + float64(float32(math.Sin(math.Pi/2))*roomRadius)
			verticalRadius := horizontalRadius * float64(yScale)
			c.carveEllipsoid(x+1, y, z, horizontalRadius, verticalRadius, horizontalRadius, definition, shouldSkip)
			tunnelCount += random.nextIntBounded(4)
		}
		for range tunnelCount {
			yaw := random.nextFloat() * 2 * float32(math.Pi)
			pitch := (random.nextFloat() - 0.5) / 4
			thickness := caveThickness(random)
			// getRange() 固定为 4：SectionPos.sectionToBlockCoord(4*2-1) = 112。
			length := 112 - random.nextIntBounded(112/4)
			seed := random.nextLong()
			c.carveCaveTunnel(definition, seed, x, y, z, thickness, yaw, pitch, float64(horizontal), float64(vertical), length, 0, shouldSkip)
		}
	}
}

func caveThickness(random *worldgenRandom) float32 {
	thickness := random.nextFloat() + random.nextFloat()*2
	if random.nextIntBounded(10) == 0 {
		thickness *= random.nextFloat()*random.nextFloat()*3 + 1
	}
	return thickness
}

// carveCaveTunnel 对应原版 CaveWorldCarver.createTunnel。
// 注意：隧道内部的随机流是 RandomSource.create(seed)，即 LegacyRandomSource。
func (c *carvingContext) carveCaveTunnel(definition carverDefinition, seed int64, x, y, z float64, thickness, yaw, pitch float32, horizontal, vertical float64, length, start int, shouldSkip func(float64, float64, float64, int) bool) {
	random := newLegacyRandom(seed)
	branchStep := random.nextIntBounded(length/2) + length/4
	branch := random.nextIntBounded(6) == 0
	yawChange, pitchChange := float32(0), float32(0)
	for step := start; step < length; step++ {
		radius := 1.5 + float64(float32(math.Sin(math.Pi*float64(float32(step)/float32(length))))*thickness)
		radiusX := radius * horizontal
		radiusY := radius * vertical
		radiusZ := radius * horizontal
		cosPitch := float32(math.Cos(float64(pitch)))
		x += float64(float32(math.Cos(float64(yaw))) * cosPitch)
		y += float64(float32(math.Sin(float64(pitch))))
		z += float64(float32(math.Sin(float64(yaw))) * cosPitch)
		if branch {
			pitch *= 0.92
		} else {
			pitch *= 0.7
		}
		pitch += pitchChange * 0.1
		yaw += yawChange * 0.1
		yawChange *= 0.9
		pitchChange *= 0.75
		yawChange += (random.nextFloat() - random.nextFloat()) * random.nextFloat() * 2
		pitchChange += (random.nextFloat() - random.nextFloat()) * random.nextFloat() * 4
		if step == branchStep && thickness > 1 {
			branchThickness := thickness * (random.nextFloat()*0.5 + 0.5)
			c.carveCaveTunnel(definition, random.nextLong(), x, y, z, branchThickness, yaw-float32(math.Pi/2), pitch/3, horizontal, vertical, length, step, shouldSkip)
			c.carveCaveTunnel(definition, random.nextLong(), x, y, z, branchThickness, yaw+float32(math.Pi/2), pitch/3, horizontal, vertical, length, step, shouldSkip)
			return
		}
		if random.nextIntBounded(4) == 0 {
			continue
		}
		if !canReachChunk(c.chunk, x, z, step, length, thickness) {
			return
		}
		c.carveEllipsoid(x, y, z, radiusX, radiusY, radiusZ, definition, shouldSkip)
	}
}

// carveCanyon 对应原版 CanyonWorldCarver.carve / doCarve。
func (c *carvingContext) carveCanyon(definition carverDefinition, sourceX, sourceZ int, random *worldgenRandom) {
	// getRange()*2-1 = 7，再乘 16 得到 112。
	baseLength := float32((4*2 - 1) * 16)
	x := float64(sourceX*16 + random.nextIntBounded(16))
	y := float64(definition.y.sample(random, c.router.setting))
	z := float64(sourceZ*16 + random.nextIntBounded(16))
	yaw := random.nextFloat() * 2 * float32(math.Pi)
	verticalRotation := definition.verticalRotation.sample(random)
	// CanyonWorldCarver.carve 的采样顺序：verticalRotation → yScale → thickness →
	// distanceFactor（length 由 distanceFactor 决定）。
	yScale := definition.yScale.sample(random)
	thickness := definition.shape.thickness.sample(random)
	length := int(baseLength * definition.shape.distanceFactor.sample(random))
	seed := random.nextLong()
	c.doCarveCanyon(definition, seed, x, y, z, thickness, yaw, verticalRotation, length, yScale)
}

func (c *carvingContext) doCarveCanyon(definition carverDefinition, seed int64, x, y, z float64, thickness, yaw, verticalRotation float32, length int, yScale float32) {
	random := newLegacyRandom(seed)
	widthFactors := make([]float32, c.chunk.Height)
	factor := float32(1)
	for index := range widthFactors {
		if index == 0 || random.nextIntBounded(definition.shape.widthSmoothness) == 0 {
			factor = 1 + random.nextFloat()*random.nextFloat()
		}
		widthFactors[index] = factor * factor
	}
	yawChange, pitchChange := float32(0), float32(0)
	for step := 0; step < length; step++ {
		radius := 1.5 + float64(float32(math.Sin(math.Pi*float64(float32(step)/float32(length))))*thickness)
		horizontalRadius := radius * float64(definition.shape.horizontalRadiusFactor.sample(random))
		verticalRadius := canyonVerticalRadius(definition, random, radius*float64(yScale), float32(length), float32(step))
		cosPitch := float32(math.Cos(float64(verticalRotation)))
		x += float64(float32(math.Cos(float64(yaw))) * cosPitch)
		y += float64(float32(math.Sin(float64(verticalRotation))))
		z += float64(float32(math.Sin(float64(yaw))) * cosPitch)
		verticalRotation = verticalRotation*0.7 + pitchChange*0.05
		yaw = yaw + yawChange*0.05
		yawChange *= 0.8
		pitchChange *= 0.5
		yawChange += (random.nextFloat() - random.nextFloat()) * random.nextFloat() * 2
		pitchChange += (random.nextFloat() - random.nextFloat()) * random.nextFloat() * 4
		if random.nextIntBounded(4) == 0 {
			continue
		}
		if !canReachChunk(c.chunk, x, z, step, length, thickness) {
			return
		}
		shouldSkip := func(nx, ny, nz float64, blockY int) bool {
			index := blockY - c.chunk.MinY - 1
			if index < 0 || index >= len(widthFactors) {
				return true
			}
			return (nx*nx+nz*nz)*float64(widthFactors[index])+ny*ny/6 >= 1
		}
		c.carveEllipsoid(x, y, z, horizontalRadius, verticalRadius, horizontalRadius, definition, shouldSkip)
	}
}

func canyonVerticalRadius(definition carverDefinition, random *legacyRandom, yScale float64, length, step float32) float64 {
	// 原版 CanyonWorldCarver.updateVerticalRadius 使用 Mth.floor 的分段函数：
	//   f = 1.0F - floor(0.5F - step / length) * 2.0F
	// 因此 f 在隧道前半段为 1，后半段为 3，而不是线性插值。
	progress := 1 - float32(math.Floor(float64(float32(0.5)-step/length)))*2
	factor := definition.shape.verticalRadiusDefaultFactor + definition.shape.verticalRadiusCenterFactor*progress
	jitter := float32(0.75) + random.nextFloat()*0.25
	return yScale * float64(factor*jitter)
}

// canReachChunk 对应 WorldCarver.canReach：判断隧道剩余长度是否仍可能触及本区块。
func canReachChunk(chunk *GeneratedChunk, x, z float64, step, length int, radius float32) bool {
	centerX := float64(chunk.MinX + 8)
	centerZ := float64(chunk.MinZ + 8)
	dx := x - centerX
	dz := z - centerZ
	remaining := float64(length - step)
	limit := float64(radius + 2 + 16)
	return dx*dx+dz*dz-remaining*remaining <= limit*limit
}

func (c *carvingContext) carveEllipsoid(centerX, centerY, centerZ, radiusX, radiusY, radiusZ float64, definition carverDefinition, shouldSkip func(float64, float64, float64, int) bool) {
	if radiusX <= 0 || radiusY <= 0 || radiusZ <= 0 {
		return
	}
	// WorldCarver.carveEllipsoid 的快速剔除：中心点到区块中心超过 16 + 2*半径时
	// 整个椭球都不可能落在区块内。
	centerBlockX := float64(c.chunk.MinX + 8)
	centerBlockZ := float64(c.chunk.MinZ + 8)
	maxDistance := 16 + radiusX*2
	if math.Abs(centerX-centerBlockX) > maxDistance || math.Abs(centerZ-centerBlockZ) > maxDistance {
		return
	}
	minX := max(c.chunk.MinX, int(math.Floor(centerX-radiusX))-1)
	maxX := min(c.chunk.MinX+15, int(math.Floor(centerX+radiusX)))
	// 原版非调试模式在世界顶部保留 7 格，且最低一格的基岩层不参与雕刻。
	minY := max(int(math.Floor(centerY-radiusY))-1, c.chunk.MinY+1)
	maxY := min(int(math.Floor(centerY+radiusY))+1, c.chunk.MinY+c.chunk.Height-1-7)
	minZ := max(c.chunk.MinZ, int(math.Floor(centerZ-radiusZ))-1)
	maxZ := min(c.chunk.MinZ+15, int(math.Floor(centerZ+radiusZ)))
	lavaLevel := definition.lavaLevel.resolve(c.router.setting)
	for x := minX; x <= maxX; x++ {
		nx := (float64(x) + 0.5 - centerX) / radiusX
		for z := minZ; z <= maxZ; z++ {
			nz := (float64(z) + 0.5 - centerZ) / radiusZ
			if nx*nx+nz*nz >= 1 {
				continue
			}
			for y := maxY; y > minY; y-- {
				ny := (float64(y) - 0.5 - centerY) / radiusY
				if shouldSkip(nx, ny, nz, y) {
					continue
				}
				index := c.chunk.index(x, y, z)
				if c.mask[index] {
					continue
				}
				c.mask[index] = true
				if _, ok := c.router.carverReplaceables[c.chunk.BlockAt(x, y, z).Name]; !ok {
					continue
				}
				if y <= lavaLevel {
					c.chunk.SetBlock(x, y, z, SurfaceBlock{Name: "minecraft:lava"})
					continue
				}
				kind, defaultBlock := c.aquifer.ComputeSubstance(x, y, z, 0)
				if defaultBlock {
					continue
				}
				switch kind {
				case fluidWater:
					c.chunk.SetBlock(x, y, z, c.water)
				case fluidLava:
					c.chunk.SetBlock(x, y, z, SurfaceBlock{Name: "minecraft:lava"})
				default:
					c.chunk.SetBlock(x, y, z, SurfaceBlock{Name: "minecraft:air"})
				}
			}
		}
	}
}

// carverRandom 是 carver 取样层的随机源接口（高度/浮点提供器共用）。
