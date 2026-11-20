//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import "math"

// 本文件移植 1.21.11 中几个最常用的复杂树木放置器：
// ForkingTrunkPlacer + AcaciaFoliagePlacer、FancyTrunkPlacer +
// FancyFoliagePlacer、DarkOakTrunkPlacer + DarkOakFoliagePlacer。
//
// 原版 TreeFeature 的顺序是：height → foliageHeight → foliageRadius →
// placeTrunk（会消耗随机数并返回树冠挂点）→ 每个挂点 createFoliage。
// 旧实现把树叶放在 trunk 之后并按自己的顺序消费随机数，复杂树形会系统性偏移；
// 这里按原版顺序实现，同时保留原实现作为其他 placer 的回退。

type treeAttachment struct {
	x, y, z      int
	radiusOffset int
	doubleTrunk  bool
}

type treeFoliageCoords struct {
	attachment treeAttachment
	branchBase int
}

func (r *Router) placeForkingTree(chunk *GeneratedChunk, random *legacyRandom, configuration treeConfiguration, x, y, z int) {
	height := configuration.baseHeight + random.nextIntBounded(configuration.heightRandA+1) + random.nextIntBounded(configuration.heightRandB+1)
	foliageHeight := 0
	radius := configuration.foliageRadius.sample(random)
	offset := configuration.foliageOffset.sample(random)
	if !r.treeFits(chunk, configuration, x, y, z, height) {
		return
	}
	r.setTreeDirt(chunk, x, y-1, z)
	attachments := make([]treeAttachment, 0, 2)
	direction := randomHorizontalDirection(random)
	branchHeight := height - random.nextIntBounded(4) - 1
	branchSteps := 3 - random.nextIntBounded(3)
	bx, bz := x, z
	top := y
	for step := 0; step < height; step++ {
		by := y + step
		if step >= branchHeight && branchSteps > 0 {
			bx += direction[0]
			bz += direction[1]
			branchSteps--
		}
		if r.placeTreeLog(chunk, configuration, bx, by, bz) {
			top = by + 1
		}
	}
	attachments = append(attachments, treeAttachment{x: bx, y: top, z: bz, radiusOffset: 1})
	bx, bz = x, z
	secondDirection := randomHorizontalDirection(random)
	if secondDirection != direction {
		secondHeight := branchHeight - random.nextIntBounded(2) - 1
		secondSteps := 1 + random.nextIntBounded(3)
		top = y
		for stepY := secondHeight; stepY < height && secondSteps > 0; stepY, secondSteps = stepY+1, secondSteps-1 {
			if stepY < 1 {
				continue
			}
			bx += secondDirection[0]
			bz += secondDirection[1]
			if r.placeTreeLog(chunk, configuration, bx, y+stepY, bz) {
				top = y + stepY + 1
			}
		}
		attachments = append(attachments, treeAttachment{x: bx, y: top, z: bz})
	}
	leaves := make([]featurePosition, 0, 64)
	for _, attachment := range attachments {
		r.placeAcaciaFoliage(chunk, random, configuration, attachment, foliageHeight, radius, offset, &leaves)
	}
	r.applyTreeDecorators(chunk, random, configuration, x, y, z, height, leaves)
}

func (r *Router) placeFancyTree(chunk *GeneratedChunk, random *legacyRandom, configuration treeConfiguration, x, y, z int) {
	height := configuration.baseHeight + random.nextIntBounded(configuration.heightRandA+1) + random.nextIntBounded(configuration.heightRandB+1)
	foliageHeight := configuration.foliageHeight
	radius := configuration.foliageRadius.sample(random)
	offset := configuration.foliageOffset.sample(random)
	if foliageHeight <= 0 {
		foliageHeight = 4
	}
	if !r.treeFits(chunk, configuration, x, y, z, height) {
		return
	}
	r.setTreeDirt(chunk, x, y-1, z)
	trunkHeight := height + 2
	top := y + int(math.Floor(float64(trunkHeight)*0.618))
	clusterCount := int(math.Min(1, math.Floor(1.382+math.Pow(float64(trunkHeight)/13.0, 2.0))))
	if clusterCount < 1 {
		clusterCount = 1
	}
	coords := make([]treeFoliageCoords, 0, 16)
	coords = append(coords, treeFoliageCoords{attachment: treeAttachment{x: x, y: y, z: z}, branchBase: top})
	for current := trunkHeight - 5; current >= 0; current-- {
		shape := fancyTreeShape(trunkHeight, current)
		if shape < 0 {
			continue
		}
		for attempt := 0; attempt < clusterCount; attempt++ {
			spread := 1.0 * shape * (float64(random.nextFloat()) + 0.328)
			angle := float64(random.nextFloat()*2) * math.Pi
			px := x + int(math.Floor(spread*math.Sin(angle)+0.5))
			pz := z + int(math.Floor(spread*math.Cos(angle)+0.5))
			py := y + current - 1
			destination := [3]int{px, py + 5, pz}
			if !r.fancyLimb(chunk, configuration, random, [3]int{px, py, pz}, destination, false) {
				continue
			}
			dx := x - px
			dz := z - pz
			branchY := float64(py) - math.Sqrt(float64(dx*dx+dz*dz))*0.381
			baseY := int(branchY)
			if branchY > float64(top) {
				baseY = top
			}
			if !r.fancyLimb(chunk, configuration, random, [3]int{x, baseY, z}, [3]int{px, py, pz}, false) {
				continue
			}
			coords = append(coords, treeFoliageCoords{attachment: treeAttachment{x: px, y: py, z: pz}, branchBase: baseY})
		}
	}
	_ = r.fancyLimb(chunk, configuration, random, [3]int{x, y, z}, [3]int{x, top, z}, true)
	for _, coordinate := range coords {
		if coordinate.attachment.x == x && coordinate.attachment.y == top && coordinate.attachment.z == z {
			continue
		}
		if !fancyTrimBranches(trunkHeight, coordinate.branchBase-y) {
			continue
		}
		_ = r.fancyLimb(chunk, configuration, random, [3]int{x, coordinate.branchBase, z}, [3]int{coordinate.attachment.x, coordinate.attachment.y, coordinate.attachment.z}, true)
	}
	leaves := make([]featurePosition, 0, 96)
	for _, coordinate := range coords {
		if !fancyTrimBranches(trunkHeight, coordinate.branchBase-y) {
			continue
		}
		r.placeFancyFoliage(chunk, random, configuration, coordinate.attachment, foliageHeight, radius, offset, &leaves)
	}
	r.applyTreeDecorators(chunk, random, configuration, x, y, z, height, leaves)
}

func (r *Router) placeDarkOakTree(chunk *GeneratedChunk, random *legacyRandom, configuration treeConfiguration, x, y, z int) {
	height := configuration.baseHeight + random.nextIntBounded(configuration.heightRandA+1) + random.nextIntBounded(configuration.heightRandB+1)
	foliageHeight := 4
	radius := configuration.foliageRadius.sample(random)
	offset := configuration.foliageOffset.sample(random)
	if !r.treeFits(chunk, configuration, x, y, z, height) {
		return
	}
	for dx := 0; dx < 2; dx++ {
		for dz := 0; dz < 2; dz++ {
			r.setTreeDirt(chunk, x+dx, y-1, z+dz)
		}
	}
	direction := randomHorizontalDirection(random)
	branchHeight := height - random.nextIntBounded(4)
	branchSteps := 2 - random.nextIntBounded(3)
	bx, bz := x, z
	topY := y + height - 1
	for step := 0; step < height; step++ {
		by := y + step
		if step >= branchHeight && branchSteps > 0 {
			bx += direction[0]
			bz += direction[1]
			branchSteps--
		}
		for dx := 0; dx < 2; dx++ {
			for dz := 0; dz < 2; dz++ {
				if r.placeTreeLog(chunk, configuration, bx+dx, by, bz+dz) {
					// 原版这里只检查 isAirOrLeaves，不处理返回值。
				}
			}
		}
	}
	attachments := []treeAttachment{{x: bx, y: topY, z: bz, doubleTrunk: true}}
	for dx := -1; dx <= 2; dx++ {
		for dz := -1; dz <= 2; dz++ {
			if (dx >= 0 && dx <= 1 && dz >= 0 && dz <= 1) || random.nextIntBounded(3) == 0 {
				length := random.nextIntBounded(3) + 2
				for n := 0; n < length; n++ {
					r.placeTreeLog(chunk, configuration, x+dx, topY-n-1, z+dz)
				}
				attachments = append(attachments, treeAttachment{x: x + dx, y: topY, z: z + dz})
			}
		}
	}
	leaves := make([]featurePosition, 0, 96)
	for _, attachment := range attachments {
		r.placeDarkOakFoliage(chunk, random, configuration, attachment, foliageHeight, radius, offset, &leaves)
	}
	r.applyTreeDecorators(chunk, random, configuration, x, y, z, height, leaves)
}

func (r *Router) treeFits(chunk *GeneratedChunk, configuration treeConfiguration, x, y, z, height int) bool {
	if height <= 0 || y+height+configuration.foliageHeight+4 >= chunk.MinY+chunk.Height {
		return false
	}
	freeHeight := height
	for checkY := 0; checkY <= height+1; checkY++ {
		radius := treeMinimumWidth(configuration, height, checkY)
		for dx := -radius; dx <= radius; dx++ {
			for dz := -radius; dz <= radius; dz++ {
				block := chunk.BlockAt(x+dx, y+checkY, z+dz)
				if treeCanReplace(block) || block.Name == configuration.leaves.Name || block.Name == configuration.trunk.Name {
					continue
				}
				freeHeight = checkY - 2
				if freeHeight < height && (configuration.minClippedHeight == 0 || freeHeight < configuration.minClippedHeight) {
					return false
				}
				return freeHeight >= height
			}
		}
	}
	return true
}

func (r *Router) setTreeDirt(chunk *GeneratedChunk, x, y, z int) {
	if !chunk.InRange(x, y, z) {
		return
	}
	current := chunk.BlockAt(x, y, z)
	if current.Name == "minecraft:grass_block" || treeCanReplace(current) || current.Name == "minecraft:dirt" || current.Name == "minecraft:coarse_dirt" || current.Name == "minecraft:podzol" || current.Name == "minecraft:mycelium" || current.Name == "minecraft:rooted_dirt" || current.Name == "minecraft:mud" {
		chunk.SetBlock(x, y, z, SurfaceBlock{Name: "minecraft:dirt"})
	}
}

func treeCanReplace(block SurfaceBlock) bool {
	switch block.Name {
	case "", "minecraft:air", "minecraft:short_grass", "minecraft:tall_grass", "minecraft:fern", "minecraft:large_fern",
		"minecraft:dead_bush", "minecraft:dandelion", "minecraft:poppy", "minecraft:azure_bluet", "minecraft:oxeye_daisy", "minecraft:cornflower", "minecraft:lily_of_the_valley",
		"minecraft:blue_orchid", "minecraft:allium", "minecraft:white_tulip", "minecraft:orange_tulip",
		"minecraft:pink_tulip", "minecraft:red_tulip", "minecraft:snow", "minecraft:vine", "minecraft:water":
		return true
	}
	return false
}

func (r *Router) placeTreeLog(chunk *GeneratedChunk, configuration treeConfiguration, x, y, z int) bool {
	if !chunk.InRange(x, y, z) {
		return false
	}
	current := chunk.BlockAt(x, y, z)
	if !treeCanReplace(current) && current.Name != configuration.leaves.Name && current.Name != configuration.trunk.Name {
		return false
	}
	chunk.SetBlock(x, y, z, configuration.trunk)
	return true
}

func randomHorizontalDirection(random *legacyRandom) [2]int {
	directions := [4][2]int{{0, -1}, {1, 0}, {0, 1}, {-1, 0}}
	return directions[random.nextIntBounded(4)]
}

func fancyTreeShape(height, current int) float64 {
	if float64(current) < float64(height)*0.3 {
		return -1
	}
	half := float64(height) / 2
	distance := half - float64(current)
	var radius float64
	if distance == 0 {
		radius = half
	} else if math.Abs(distance) >= half {
		return 0
	} else {
		radius = math.Sqrt(half*half - distance*distance)
	}
	return radius * 0.5
}

func fancyTrimBranches(height, branchBase int) bool {
	return float64(branchBase) >= float64(height)*0.2
}

func (r *Router) fancyLimb(chunk *GeneratedChunk, configuration treeConfiguration, random *legacyRandom, from, to [3]int, place bool) bool {
	if !place && from == to {
		return true
	}
	dx, dy, dz := to[0]-from[0], to[1]-from[1], to[2]-from[2]
	steps := maxInt(absFeatureInt(dx), maxInt(absFeatureInt(dy), absFeatureInt(dz)))
	if steps == 0 {
		return true
	}
	for step := 0; step <= steps; step++ {
		x := from[0] + int(math.Floor(0.5+float64(step*dx)/float64(steps)))
		y := from[1] + int(math.Floor(0.5+float64(step*dy)/float64(steps)))
		z := from[2] + int(math.Floor(0.5+float64(step*dz)/float64(steps)))
		if place {
			r.placeTreeLog(chunk, configuration, x, y, z)
			continue
		}
		if !treeCanReplace(chunk.BlockAt(x, y, z)) {
			return false
		}
	}
	return true
}

func (r *Router) placeAcaciaFoliage(chunk *GeneratedChunk, random *legacyRandom, configuration treeConfiguration, attachment treeAttachment, foliageHeight, radius, offset int, leaves *[]featurePosition) {
	baseY := attachment.y + offset
	row := func(rowRadius, dy int) {
		r.placeTreeLeavesRow(chunk, random, configuration, attachment, attachment.x, baseY+dy, attachment.z, rowRadius, leaves, func(dx, dz int) bool {
			if dy == 0 {
				return (absFeatureInt(dx) > 1 || absFeatureInt(dz) > 1) && dx != 0 && dz != 0
			}
			return absFeatureInt(dx) == rowRadius && absFeatureInt(dz) == rowRadius && rowRadius > 0
		})
	}
	row(radius+attachment.radiusOffset, -1-foliageHeight)
	row(radius-1, -foliageHeight)
	row(radius+attachment.radiusOffset-1, 0)
}

func (r *Router) placeFancyFoliage(chunk *GeneratedChunk, random *legacyRandom, configuration treeConfiguration, attachment treeAttachment, foliageHeight, radius, offset int, leaves *[]featurePosition) {
	baseY := attachment.y + offset
	for dy := 0; dy >= -foliageHeight; dy-- {
		rowRadius := radius
		if dy != 0 && dy != -foliageHeight {
			rowRadius++
		}
		r.placeTreeLeavesRow(chunk, random, configuration, attachment, attachment.x, baseY+dy, attachment.z, rowRadius, leaves, func(dx, dz int) bool {
			x := float64(dx) + 0.5
			z := float64(dz) + 0.5
			return x*x+z*z > float64(rowRadius*rowRadius)
		})
	}
}

func (r *Router) placeDarkOakFoliage(chunk *GeneratedChunk, random *legacyRandom, configuration treeConfiguration, attachment treeAttachment, foliageHeight, radius, offset int, leaves *[]featurePosition) {
	baseY := attachment.y + offset
	double := attachment.doubleTrunk
	row := func(rowRadius, dy int) {
		r.placeTreeLeavesRow(chunk, random, configuration, attachment, attachment.x, baseY+dy, attachment.z, rowRadius, leaves, func(dx, dz int) bool {
			if dy == -1 && !double {
				return absFeatureInt(dx) == rowRadius && absFeatureInt(dz) == rowRadius
			}
			if dy == 1 {
				return absFeatureInt(dx)+absFeatureInt(dz) > rowRadius*2-2
			}
			return false
		})
	}
	if double {
		row(radius+2, -1)
		row(radius+3, 0)
		row(radius+2, 1)
		if random.nextBoolean() {
			row(radius, 2)
		}
	} else {
		row(radius+2, -1)
		row(radius+1, 0)
	}
}

func (r *Router) placeTreeLeavesRow(chunk *GeneratedChunk, random *legacyRandom, configuration treeConfiguration, attachment treeAttachment, baseX, baseY, baseZ, radius int, leaves *[]featurePosition, skip func(dx, dz int) bool) {
	extra := 0
	if attachment.doubleTrunk {
		extra = 1
	}
	for dx := -radius; dx <= radius+extra; dx++ {
		for dz := -radius; dz <= radius+extra; dz++ {
			signedX, signedZ := dx, dz
			if attachment.doubleTrunk {
				signedX = minInt(absFeatureInt(dx), absFeatureInt(dx-1))
				signedZ = minInt(absFeatureInt(dz), absFeatureInt(dz-1))
			}
			if skip != nil && skip(signedX, signedZ) {
				continue
			}
			x, y, z := baseX+dx, baseY, baseZ+dz
			target := chunk.BlockAt(x, y, z)
			if !treeCanReplace(target) || target.Name == configuration.leaves.Name {
				continue
			}
			chunk.SetBlock(x, y, z, configuration.leaves)
			*leaves = append(*leaves, featurePosition{x: x, y: y, z: z})
		}
	}
}

func (r *Router) placeGiantTree(chunk *GeneratedChunk, random *legacyRandom, configuration treeConfiguration, x, y, z int) {
	height := configuration.baseHeight + random.nextIntBounded(configuration.heightRandA+1) + random.nextIntBounded(configuration.heightRandB+1)
	foliageHeight := configuration.crownHeight.sample(random)
	if foliageHeight <= 0 {
		foliageHeight = configuration.foliageHeight
	}
	if foliageHeight <= 0 {
		foliageHeight = 1
	}
	radius := configuration.foliageRadius.sample(random)
	offset := configuration.foliageOffset.sample(random)
	if !r.treeFits(chunk, configuration, x, y, z, height) {
		return
	}
	for dx := 0; dx < 2; dx++ {
		for dz := 0; dz < 2; dz++ {
			r.setTreeDirt(chunk, x+dx, y-1, z+dz)
		}
	}
	for step := 0; step < height; step++ {
		r.placeTreeLog(chunk, configuration, x, y+step, z)
		if step >= height-1 {
			continue
		}
		r.placeTreeLog(chunk, configuration, x+1, y+step, z)
		r.placeTreeLog(chunk, configuration, x+1, y+step, z+1)
		r.placeTreeLog(chunk, configuration, x, y+step, z+1)
	}
	attachment := treeAttachment{x: x, y: y + height, z: z, doubleTrunk: true}
	leaves := make([]featurePosition, 0, 128)
	attachment.y += offset
	r.placeMegaPineFoliage(chunk, random, configuration, attachment, foliageHeight, radius, &leaves)
	r.applyTreeDecorators(chunk, random, configuration, x, y, z, height, leaves)
}

func (r *Router) placeMegaJungleTree(chunk *GeneratedChunk, random *legacyRandom, configuration treeConfiguration, x, y, z int) {
	height := configuration.baseHeight + random.nextIntBounded(configuration.heightRandA+1) + random.nextIntBounded(configuration.heightRandB+1)
	foliageHeight := configuration.foliageHeight
	radius := configuration.foliageRadius.sample(random)
	offset := configuration.foliageOffset.sample(random)
	if !r.treeFits(chunk, configuration, x, y, z, height) {
		return
	}
	for dx := 0; dx < 2; dx++ {
		for dz := 0; dz < 2; dz++ {
			r.setTreeDirt(chunk, x+dx, y-1, z+dz)
		}
	}
	for step := 0; step < height; step++ {
		r.placeTreeLog(chunk, configuration, x, y+step, z)
		if step >= height-1 {
			continue
		}
		r.placeTreeLog(chunk, configuration, x+1, y+step, z)
		r.placeTreeLog(chunk, configuration, x+1, y+step, z+1)
		r.placeTreeLog(chunk, configuration, x, y+step, z+1)
	}
	attachments := []treeAttachment{{x: x, y: y + height, z: z, doubleTrunk: true}}
	for current := height - 2 - random.nextIntBounded(4); current > height/2; current -= 2 + random.nextIntBounded(4) {
		angle := float64(random.nextFloat()) * math.Pi * 2
		dx, dz := 0, 0
		for step := 0; step < 5; step++ {
			dx = int(1.5 + math.Cos(angle)*float64(step))
			dz = int(1.5 + math.Sin(angle)*float64(step))
			r.placeTreeLog(chunk, configuration, x+dx, y+current-3+step/2, z+dz)
		}
		attachments = append(attachments, treeAttachment{x: x + dx, y: y + current, z: z + dz, radiusOffset: -2})
	}
	leaves := make([]featurePosition, 0, 128)
	for _, attachment := range attachments {
		attachment.y += offset
		r.placeMegaJungleFoliage(chunk, random, configuration, attachment, foliageHeight, radius, &leaves)
	}
	r.applyTreeDecorators(chunk, random, configuration, x, y, z, height, leaves)
}

func (r *Router) placeUpwardsTree(chunk *GeneratedChunk, random *legacyRandom, configuration treeConfiguration, x, y, z int) {
	height := configuration.baseHeight + random.nextIntBounded(configuration.heightRandA+1) + random.nextIntBounded(configuration.heightRandB+1)
	foliageHeight := configuration.randomSpreadHeight.sample(random)
	radius := configuration.foliageRadius.sample(random)
	offset := configuration.foliageOffset.sample(random)
	if !r.treeFits(chunk, configuration, x, y, z, height) {
		return
	}
	r.setTreeDirt(chunk, x, y-1, z)
	attachments := make([]treeAttachment, 0, 8)
	bx, bz := x, z
	for step := 0; step < height; step++ {
		by := y + step
		if r.placeTreeLog(chunk, configuration, bx, by, bz) && step < height-1 && random.nextFloat() < configuration.branchProbability {
			direction := randomHorizontalDirection(random)
			length := configuration.extraBranchLength.sample(random)
			start := maxInt(0, length-configuration.extraBranchLength.sample(random)-1)
			steps := configuration.extraBranchSteps.sample(random)
			branchX, branchZ := bx, bz
			endY := by
			for i := start; i < height && steps > 0; i, steps = i+1, steps-1 {
				if i < 1 {
					continue
				}
				branchX += direction[0]
				branchZ += direction[1]
				if r.placeTreeLog(chunk, configuration, branchX, by+i, branchZ) {
					endY = by + i + 1
				}
				attachments = append(attachments, treeAttachment{x: branchX, y: by + i, z: branchZ})
			}
			if endY-by > 1 {
				attachments = append(attachments,
					treeAttachment{x: branchX, y: endY, z: branchZ},
					treeAttachment{x: branchX, y: endY - 2, z: branchZ})
			}
		}
		if step == height-1 {
			attachments = append(attachments, treeAttachment{x: bx, y: by + 1, z: bz})
		}
	}
	leaves := make([]featurePosition, 0, 128)
	for _, attachment := range attachments {
		attachment.y += offset
		r.placeRandomSpreadFoliage(chunk, random, configuration, attachment, foliageHeight, radius, &leaves)
	}
	r.applyTreeDecorators(chunk, random, configuration, x, y, z, height, leaves)
}

func (r *Router) placeBendingTree(chunk *GeneratedChunk, random *legacyRandom, configuration treeConfiguration, x, y, z int) {
	height := configuration.baseHeight + random.nextIntBounded(configuration.heightRandA+1) + random.nextIntBounded(configuration.heightRandB+1)
	foliageHeight := configuration.randomSpreadHeight.sample(random)
	radius := configuration.foliageRadius.sample(random)
	offset := configuration.foliageOffset.sample(random)
	if !r.treeFits(chunk, configuration, x, y, z, height) {
		return
	}
	r.setTreeDirt(chunk, x, y-1, z)
	direction := randomHorizontalDirection(random)
	top := height - 1
	bx, bz, by := x, z, y
	attachments := make([]treeAttachment, 0, height)
	for step := 0; step <= top; step++ {
		if step+1 >= top+random.nextIntBounded(2) {
			bx += direction[0]
			bz += direction[1]
		}
		r.placeTreeLog(chunk, configuration, bx, by, bz)
		if step >= configuration.minHeightForLeaves {
			attachments = append(attachments, treeAttachment{x: bx, y: by, z: bz})
		}
		by++
	}
	bend := configuration.bendLength.sample(random)
	for step := 0; step <= bend; step++ {
		r.placeTreeLog(chunk, configuration, bx, by, bz)
		attachments = append(attachments, treeAttachment{x: bx, y: by, z: bz})
		bx += direction[0]
		bz += direction[1]
	}
	leaves := make([]featurePosition, 0, 128)
	for _, attachment := range attachments {
		attachment.y += offset
		r.placeRandomSpreadFoliage(chunk, random, configuration, attachment, foliageHeight, radius, &leaves)
	}
	r.applyTreeDecorators(chunk, random, configuration, x, y, z, height, leaves)
}

func (r *Router) placeCherryTree(chunk *GeneratedChunk, random *legacyRandom, configuration treeConfiguration, x, y, z int) {
	height := configuration.baseHeight + random.nextIntBounded(configuration.heightRandA+1) + random.nextIntBounded(configuration.heightRandB+1)
	foliageHeight := configuration.foliageHeight
	radius := configuration.foliageRadius.sample(random)
	offset := configuration.foliageOffset.sample(random)
	firstStart := maxInt(0, height-1+configuration.branchStartProvider.sample(random))
	secondStart := maxInt(0, height-1+configuration.branchSecondStartProvider.sample(random))
	if secondStart >= firstStart {
		secondStart++
	}
	count := configuration.branchCount.sample(random)
	three := count == 3
	two := count >= 2
	mainHeight := firstStart + 1
	if three {
		mainHeight = height
	} else if two {
		mainHeight = maxInt(firstStart, secondStart) + 1
	}
	if !r.treeFits(chunk, configuration, x, y, z, height) {
		return
	}
	r.setTreeDirt(chunk, x, y-1, z)
	for step := 0; step < mainHeight; step++ {
		r.placeTreeLog(chunk, configuration, x, y+step, z)
	}
	attachments := make([]treeAttachment, 0, 3)
	if three {
		attachments = append(attachments, treeAttachment{x: x, y: y + mainHeight, z: z})
	}
	direction := randomHorizontalDirection(random)
	attachments = append(attachments, r.cherryBranch(chunk, random, configuration, x, y, z, height, direction, firstStart, firstStart < mainHeight-1))
	if two {
		attachments = append(attachments, r.cherryBranch(chunk, random, configuration, x, y, z, height, [2]int{-direction[0], -direction[1]}, secondStart, secondStart < mainHeight-1))
	}
	leaves := make([]featurePosition, 0, 96)
	for _, attachment := range attachments {
		attachment.y += offset
		r.placeCherryFoliage(chunk, random, configuration, attachment, foliageHeight, radius, &leaves)
	}
	r.applyTreeDecorators(chunk, random, configuration, x, y, z, height, leaves)
}

func (r *Router) cherryBranch(chunk *GeneratedChunk, random *legacyRandom, configuration treeConfiguration, x, y, z, height int, direction [2]int, start int, short bool) treeAttachment {
	bx, bz, by := x, z, y+start
	endOffset := height - 1 + configuration.branchEndProvider.sample(random)
	extended := short || endOffset < start
	length := configuration.branchHorizontal.sample(random)
	if extended {
		length++
	}
	endX, endZ, endY := x+direction[0]*length, z+direction[1]*length, y+endOffset
	count := 1
	if extended {
		count = 2
	}
	for i := 0; i < count; i++ {
		bx += direction[0]
		bz += direction[1]
		r.placeTreeLog(chunk, configuration, bx, by, bz)
	}
	up := endY > by
	for {
		distance := absFeatureInt(endX-bx) + absFeatureInt(endY-by) + absFeatureInt(endZ-bz)
		if distance == 0 {
			break
		}
		chance := float32(absFeatureInt(endY-by)) / float32(distance)
		if random.nextFloat() < chance {
			if up {
				by++
			} else {
				by--
			}
		} else {
			bx += direction[0]
			bz += direction[1]
		}
		r.placeTreeLog(chunk, configuration, bx, by, bz)
	}
	return treeAttachment{x: endX, y: endY + 1, z: endZ}
}

func (r *Router) placeMegaPineFoliage(chunk *GeneratedChunk, random *legacyRandom, configuration treeConfiguration, attachment treeAttachment, foliageHeight, radius int, leaves *[]featurePosition) {
	if foliageHeight <= 0 {
		foliageHeight = 1
	}
	previous := 0
	baseY := attachment.y
	for y := baseY - foliageHeight; y <= baseY; y++ {
		dy := baseY - y
		current := radius + attachment.radiusOffset + int(math.Floor(float64(dy)/float64(foliageHeight)*3.5))
		if dy > 0 && current == previous && y&1 == 0 {
			current++
		}
		r.placeTreeLeavesRow(chunk, random, configuration, attachment, attachment.x, y, attachment.z, current, leaves, func(dx, dz int) bool {
			return absFeatureInt(dx)+absFeatureInt(dz) >= 7 || dx*dx+dz*dz > current*current
		})
		previous = current
	}
}

func (r *Router) placeMegaJungleFoliage(chunk *GeneratedChunk, random *legacyRandom, configuration treeConfiguration, attachment treeAttachment, foliageHeight, radius int, leaves *[]featurePosition) {
	layers := 1 + random.nextIntBounded(2)
	if attachment.doubleTrunk {
		layers = foliageHeight
	}
	for dy := 0; dy <= layers; dy++ {
		current := radius + attachment.radiusOffset + 1 - dy
		r.placeTreeLeavesRow(chunk, random, configuration, attachment, attachment.x, attachment.y-dy, attachment.z, current, leaves, func(dx, dz int) bool {
			return dx+dz >= 7 || dx*dx+dz*dz > current*current
		})
	}
}

func (r *Router) placeRandomSpreadFoliage(chunk *GeneratedChunk, random *legacyRandom, configuration treeConfiguration, attachment treeAttachment, foliageHeight, radius int, leaves *[]featurePosition) {
	attempts := configuration.leafPlacementAttempts
	if attempts <= 0 {
		attempts = 1
	}
	for attempt := 0; attempt < attempts; attempt++ {
		dx1, dx2 := random.nextIntBounded(radius), random.nextIntBounded(radius)
		dy1, dy2 := random.nextIntBounded(foliageHeight), random.nextIntBounded(foliageHeight)
		dz1, dz2 := random.nextIntBounded(radius), random.nextIntBounded(radius)
		dx := dx1 - dx2
		dy := dy1 - dy2
		dz := dz1 - dz2
		x, y, z := attachment.x+dx, attachment.y+dy, attachment.z+dz
		if treeCanReplace(chunk.BlockAt(x, y, z)) {
			chunk.SetBlock(x, y, z, configuration.leaves)
			*leaves = append(*leaves, featurePosition{x: x, y: y, z: z})
		}
	}
}

func (r *Router) placeCherryFoliage(chunk *GeneratedChunk, random *legacyRandom, configuration treeConfiguration, attachment treeAttachment, foliageHeight, radius int, leaves *[]featurePosition) {
	baseY := attachment.y
	rowRadius := radius + attachment.radiusOffset - 1
	if rowRadius < 0 {
		rowRadius = 0
	}
	if foliageHeight <= 0 {
		foliageHeight = 1
	}
	skip := func(dx, dz int) bool {
		if dx == radius || dz == radius {
			if random.nextFloat() < configuration.cherryWideBottom {
				return true
			}
		}
		corner := absFeatureInt(dx) == rowRadius && absFeatureInt(dz) == rowRadius
		if rowRadius > 2 {
			return corner || absFeatureInt(dx)+absFeatureInt(dz) > rowRadius*2-2 && random.nextFloat() < configuration.cherryCorner
		}
		return corner && random.nextFloat() < configuration.cherryCorner
	}
	r.placeTreeLeavesRow(chunk, random, configuration, attachment, attachment.x, baseY+foliageHeight-3, attachment.z, rowRadius-2, leaves, nil)
	r.placeTreeLeavesRow(chunk, random, configuration, attachment, attachment.x, baseY+foliageHeight-4, attachment.z, rowRadius-1, leaves, nil)
	for dy := foliageHeight - 5; dy >= 0; dy-- {
		r.placeTreeLeavesRow(chunk, random, configuration, attachment, attachment.x, baseY+dy, attachment.z, rowRadius, leaves, skip)
	}
	_ = baseY
}
