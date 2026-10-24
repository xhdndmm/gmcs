//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"time"

	"gmcs/internal/item"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 实体持久化：各维度世界目录下的 entities.json（生物 + 掉落物 + 箭矢 + 经验球）。
//
// 说明：gmcs 的区块存储是自定义格式（不含实体），因此实体单独保存在
// 维度目录的 entities.json 中（主世界为世界目录，下界/末地为其子目录）；
// 文件采用“临时文件 + 重命名”的原子写入。恢复时保留类型、位置、朝向、
// 生命、物品堆叠与 UUID（实体 ID 重新分配）；掉落物的速度、存活时间与
// 拾取延迟同样保存（原版 `ItemEntity` 保存 Age/PickupDelay，速度属于
// 实体基础字段），箭矢的速度与伤害保存；经验球保存 Value/Count/Age/速度
// （原版 ExperienceOrb 保存 Age 与 Value）。

// entityFileName 是世界目录中实体数据的文件名。
const entityFileName = "entities.json"

// entityFilePath 返回维度实体数据的文件路径。
func (s *Server) entityFilePath(dim world.Dimension) string {
	dir := s.config.WorldDir
	if sub := world.DimensionDir(dim); sub != "" {
		dir = filepath.Join(dir, sub)
	}
	return filepath.Join(dir, entityFileName)
}

type entityFile struct {
	Mobs   []mobRecord   `json:"mobs"`
	Items  []itemRecord  `json:"items,omitempty"`
	Arrows []arrowRecord `json:"arrows,omitempty"`
	XPOrbs []xpOrbRecord `json:"xp_orbs,omitempty"`
}

// xpOrbRecord 是一条持久化的经验球。Count 是合并后的可拾取次数
// （gmcs 的合并计数；原版每个经验球只有 Value）。
type xpOrbRecord struct {
	UUID     string  `json:"uuid"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Z        float64 `json:"z"`
	VelX     float64 `json:"velX,omitempty"`
	VelY     float64 `json:"velY,omitempty"`
	VelZ     float64 `json:"velZ,omitempty"`
	Value    int32   `json:"value"`
	Count    int32   `json:"count,omitempty"`
	AgeTicks int     `json:"ageTicks,omitempty"`
}

// arrowRecord 是一条持久化的箭矢（保留速度与伤害）。
type arrowRecord struct {
	UUID     string  `json:"uuid"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Z        float64 `json:"z"`
	VelX     float64 `json:"velX"`
	VelY     float64 `json:"velY"`
	VelZ     float64 `json:"velZ"`
	Damage   float32 `json:"damage"`
	AgeTicks int     `json:"ageTicks"`
}

// itemRecord 是一条持久化的掉落物（含速度/存活时间/拾取延迟；组件覆盖
// 与玩家物品栏的 playerItem 字段一致）。
type itemRecord struct {
	Item         string              `json:"item"`
	Count        int32               `json:"count"`
	UUID         string              `json:"uuid"`
	X            float64             `json:"x"`
	Y            float64             `json:"y"`
	Z            float64             `json:"z"`
	VelX         float64             `json:"velX,omitempty"`
	VelY         float64             `json:"velY,omitempty"`
	VelZ         float64             `json:"velZ,omitempty"`
	AgeTicks     int                 `json:"ageTicks,omitempty"`
	PickupDelay  int                 `json:"pickupDelay,omitempty"`
	Potion       int32               `json:"potion,omitempty"`
	Damage       int32               `json:"damage,omitempty"`
	RepairCost   int32               `json:"repair_cost,omitempty"`
	CustomName   string              `json:"custom_name,omitempty"`
	Enchantments []storedEnchantment `json:"enchantments,omitempty"`
	TrimPattern  string              `json:"trim_pattern,omitempty"`
	TrimMaterial string              `json:"trim_material,omitempty"`
}

// finiteFloat 报告浮点数是否为有限值（NaN/Inf 会破坏物理与协议编码）。
func finiteFloat(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

// finitePosition 报告坐标是否全部为有限值。
func finitePosition(x, y, z float64) bool {
	return finiteFloat(x) && finiteFloat(y) && finiteFloat(z)
}

// validStoredY 报告 Y 是否在合理世界范围内（留 64 格余量，兼容掉入虚空/高空的实体）。
func validStoredY(y float64) bool {
	return y >= float64(world.WorldMinY-64) && y <= float64(world.WorldMinY+world.WorldHeight+64)
}

// maxStoredHorizontal 是存档坐标允许的最大水平绝对值（原版世界边界上限
// 约 3000 万，额外留余量）。
const maxStoredHorizontal = 30_000_000.0

// validStoredPosition 同时校验有限性、水平范围与 Y 范围。
func validStoredPosition(x, y, z float64) bool {
	return finitePosition(x, y, z) && validStoredY(y) &&
		math.Abs(x) <= maxStoredHorizontal && math.Abs(z) <= maxStoredHorizontal
}

// storedVelocity 过滤存档中的非有限速度（NaN/Inf 会让物理与协议编码异常），
// 并对过大速度做限幅（正常玩法下远小于该值）。
func storedVelocity(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	const limit = 100
	if v > limit {
		return limit
	}
	if v < -limit {
		return -limit
	}
	return v
}

// storedDamage 过滤非有限/负伤害并限幅（异常存档不会产生秒杀或 NaN 伤害）。
func storedDamage(v float32) float32 {
	if !finiteFloat(float64(v)) || v < 0 {
		return 0
	}
	const limit = 1000
	if v > limit {
		return limit
	}
	return v
}

// clampStoredInt 把存档中的整数限制在 [min, max]。
func clampStoredInt(v, minValue, maxValue int) int {
	if v < minValue {
		return minValue
	}
	if v > maxValue {
		return maxValue
	}
	return v
}

type mobRecord struct {
	Type   string  `json:"type"`
	UUID   string  `json:"uuid"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Z      float64 `json:"z"`
	Yaw    float32 `json:"yaw"`
	Pitch  float32 `json:"pitch"`
	Health float32 `json:"health"`
	// FireTicks 是剩余着火 tick（原版实体存档的 Fire 字段；0 时省略字段，
	// 旧存档不含该字段时按 0 恢复）。
	FireTicks int `json:"fire_ticks,omitempty"`
}

// saveEntities 把全部维度中的生物、掉落物与箭矢写入各自目录（原子写入）。
func (s *Server) saveEntities() error {
	type dimFile struct {
		dim    world.Dimension
		mobs   []mobRecord
		items  []itemRecord
		arrows []arrowRecord
		xpOrbs []xpOrbRecord
	}
	files := make(map[world.Dimension]*dimFile, 3)
	get := func(dim world.Dimension) *dimFile {
		file := files[dim]
		if file == nil {
			file = &dimFile{dim: dim}
			files[dim] = file
		}
		return file
	}

	s.entityMu.Lock()
	for _, m := range s.mobs {
		if m.Dead {
			continue
		}
		file := get(m.Dim)
		file.mobs = append(file.mobs, mobRecord{
			Type:      mobKinds[m.Kind].name,
			UUID:      hex.EncodeToString(m.UUID[:]),
			X:         m.X,
			Y:         m.Y,
			Z:         m.Z,
			Yaw:       m.Yaw,
			Pitch:     m.Pitch,
			Health:    m.Health,
			FireTicks: m.FireTicks,
		})
	}
	for _, e := range s.items {
		name, ok := registry.ItemName(e.Stack.ItemID)
		if !ok {
			continue
		}
		file := get(e.Dim)
		file.items = append(file.items, itemRecord{
			Item:  name,
			Count: e.Stack.Count,
			UUID:  hex.EncodeToString(e.UUID[:]),
			X:     e.X,
			Y:     e.Y,
			Z:     e.Z,
			VelX:  e.VelX, VelY: e.VelY, VelZ: e.VelZ,
			AgeTicks:    e.AgeTicks,
			PickupDelay: e.PickupDelayTicks,
			Potion:      e.Stack.PotionID, Damage: e.Stack.Damage,
			RepairCost: e.Stack.RepairCost, CustomName: e.Stack.CustomName,
			Enchantments: storedEnchantments(e.Stack.Enchantments),
			TrimPattern:  e.Stack.TrimPattern, TrimMaterial: e.Stack.TrimMaterial,
		})
	}
	for _, a := range s.arrows {
		file := get(a.Dim)
		file.arrows = append(file.arrows, arrowRecord{
			UUID:     hex.EncodeToString(a.UUID[:]),
			X:        a.X,
			Y:        a.Y,
			Z:        a.Z,
			VelX:     a.VelX,
			VelY:     a.VelY,
			VelZ:     a.VelZ,
			Damage:   a.Damage,
			AgeTicks: a.AgeTicks,
		})
	}
	for _, o := range s.xpOrbs {
		file := get(o.Dim)
		file.xpOrbs = append(file.xpOrbs, xpOrbRecord{
			UUID:     hex.EncodeToString(o.UUID[:]),
			X:        o.X,
			Y:        o.Y,
			Z:        o.Z,
			VelX:     o.VelX,
			VelY:     o.VelY,
			VelZ:     o.VelZ,
			Value:    o.Value,
			Count:    o.Count,
			AgeTicks: o.AgeTicks,
		})
	}
	s.entityMu.Unlock()

	var firstErr error
	for _, file := range files {
		content, err := json.MarshalIndent(entityFile{Mobs: file.mobs, Items: file.items, Arrows: file.arrows, XPOrbs: file.xpOrbs}, "", "  ")
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		content = append(content, '\n')
		path := s.entityFilePath(file.dim)
		temp := path + ".tmp"
		if err := os.WriteFile(temp, content, 0o644); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("写入 %s：%w", temp, err)
			}
			continue
		}
		if err := os.Rename(temp, path); err != nil {
			_ = os.Remove(temp)
			if firstErr == nil {
				firstErr = fmt.Errorf("重命名 %s：%w", path, err)
			}
		}
	}
	return firstErr
}

// loadEntitiesFor 从维度的实体文件恢复生物与掉落物。文件不存在时不做任何事；
// 未知类型与损坏的条目会被跳过（只记录警告）。
func (s *Server) loadEntitiesFor(dim world.Dimension) error {
	path := s.entityFilePath(dim)
	content, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("读取 %s：%w", path, err)
	}
	var file entityFile
	if err := json.Unmarshal(content, &file); err != nil {
		return fmt.Errorf("解析 %s：%w", path, err)
	}

	kindByName := make(map[string]mobKind, mobKindCount)
	for kind := mobKind(0); kind < mobKindCount; kind++ {
		kindByName[mobKinds[kind].name] = kind
	}

	restored := 0
	restoredItems := 0
	restoredArrows := 0
	restoredOrbs := 0
	s.entityMu.Lock()
	for _, record := range file.Mobs {
		kind, ok := kindByName[record.Type]
		if !ok {
			slog.Warn("跳过不支持的生物类型", "type", record.Type)
			continue
		}
		if !validStoredPosition(record.X, record.Y, record.Z) {
			slog.Warn("跳过坐标无效的生物记录", "type", record.Type)
			continue
		}
		typeID, ok := s.mobTypeIDs[kind]
		if !ok {
			slog.Warn("生物类型注册表缺失，跳过", "type", record.Type)
			continue
		}
		uuid, uuidErr := parseMobUUID(record.UUID)
		if uuidErr != nil {
			slog.Warn("生物的 UUID 无效，重新生成", "uuid", record.UUID, "error", uuidErr)
			uuid = newEntityUUID()
		}
		stats := mobKinds[kind]
		health := record.Health
		if !finiteFloat(float64(health)) || health <= 0 || health > stats.health {
			health = stats.health
		}
		yaw, pitch := record.Yaw, record.Pitch
		if !finiteFloat(float64(yaw)) {
			yaw = 0
		}
		if !finiteFloat(float64(pitch)) {
			pitch = 0
		}
		m := &mob{
			ID:     s.entityIDs.Add(1),
			UUID:   uuid,
			TypeID: typeID,
			Kind:   kind,
			Dim:    dim,
			X:      record.X,
			Y:      record.Y,
			Z:      record.Z,
			Yaw:    yaw,
			Pitch:  pitch,
			Health: health,
		}
		// 着火时长限幅（异常存档不会让生物永久燃烧）。
		m.FireTicks = clampStoredInt(record.FireTicks, 0, maxStoredFireTicks)
		m.HeadYaw = m.Yaw
		s.mobs[m.ID] = m
		restored++
	}
	for _, record := range file.Items {
		if !validStoredPosition(record.X, record.Y, record.Z) {
			slog.Warn("跳过坐标无效的掉落物记录", "item", record.Item)
			continue
		}
		stack, itemErr := item.FromName(record.Item, record.Count)
		if itemErr != nil {
			slog.Warn("跳过无效的掉落物记录", "item", record.Item, "error", itemErr)
			continue
		}
		stack.PotionID = record.Potion
		stack.Damage = record.Damage
		stack.RepairCost = record.RepairCost
		stack.CustomName = record.CustomName
		stack.Enchantments = itemEnchantments(record.Enchantments)
		if item.IsValidTrim(record.TrimPattern, record.TrimMaterial) {
			stack.TrimPattern = record.TrimPattern
			stack.TrimMaterial = record.TrimMaterial
		}
		stack = sanitizeStoredStack(stack)
		uuid, uuidErr := parseMobUUID(record.UUID)
		if uuidErr != nil {
			slog.Warn("掉落物的 UUID 无效，重新生成", "uuid", record.UUID, "error", uuidErr)
			uuid = newEntityUUID()
		}
		e := &itemEntity{
			ID:               s.entityIDs.Add(1),
			UUID:             uuid,
			Stack:            stack,
			Dim:              dim,
			X:                record.X,
			Y:                record.Y,
			Z:                record.Z,
			VelX:             storedVelocity(record.VelX),
			VelY:             storedVelocity(record.VelY),
			VelZ:             storedVelocity(record.VelZ),
			AgeTicks:         clampStoredInt(record.AgeTicks, 0, itemDespawnTicks),
			PickupDelayTicks: clampStoredInt(record.PickupDelay, 0, itemPickupDelayPlayer),
		}
		s.items[e.ID] = e
		restoredItems++
	}
	for _, record := range file.Arrows {
		if !s.arrowsEnabled {
			break
		}
		if !validStoredPosition(record.X, record.Y, record.Z) {
			slog.Warn("跳过坐标无效的箭矢记录")
			continue
		}
		uuid, uuidErr := parseMobUUID(record.UUID)
		if uuidErr != nil {
			slog.Warn("箭矢的 UUID 无效，重新生成", "uuid", record.UUID, "error", uuidErr)
			uuid = newEntityUUID()
		}
		a := &arrowEntity{
			ID:       s.entityIDs.Add(1),
			UUID:     uuid,
			Dim:      dim,
			X:        record.X,
			Y:        record.Y,
			Z:        record.Z,
			VelX:     storedVelocity(record.VelX),
			VelY:     storedVelocity(record.VelY),
			VelZ:     storedVelocity(record.VelZ),
			Damage:   storedDamage(record.Damage),
			AgeTicks: clampStoredInt(record.AgeTicks, 0, arrowLifetimeTicks),
		}
		s.arrows[a.ID] = a
		restoredArrows++
	}
	if s.xpOrbsEnabled {
		for _, record := range file.XPOrbs {
			if !validStoredPosition(record.X, record.Y, record.Z) {
				slog.Warn("跳过坐标无效的经验球记录")
				continue
			}
			if record.Value <= 0 {
				slog.Warn("跳过数值无效的经验球记录", "value", record.Value)
				continue
			}
			uuid, uuidErr := parseMobUUID(record.UUID)
			if uuidErr != nil {
				slog.Warn("经验球的 UUID 无效，重新生成", "uuid", record.UUID, "error", uuidErr)
				uuid = newEntityUUID()
			}
			count := record.Count
			if count <= 0 {
				count = 1
			}
			o := &xpOrb{
				ID:       s.entityIDs.Add(1),
				UUID:     uuid,
				Dim:      dim,
				Value:    int32(clampStoredInt(int(record.Value), 1, 32767)),
				Count:    int32(clampStoredInt(int(count), 1, 100000)),
				X:        record.X,
				Y:        record.Y,
				Z:        record.Z,
				VelX:     storedVelocity(record.VelX),
				VelY:     storedVelocity(record.VelY),
				VelZ:     storedVelocity(record.VelZ),
				AgeTicks: clampStoredInt(record.AgeTicks, 0, xpOrbLifetime),
			}
			s.xpOrbs[o.ID] = o
			restoredOrbs++
		}
	}
	s.entityMu.Unlock()
	if restored > 0 {
		slog.Info("restored mobs from disk", "dimension", dim, "count", restored)
	}
	if restoredItems > 0 {
		slog.Info("restored items from disk", "dimension", dim, "count", restoredItems)
	}
	if restoredArrows > 0 {
		slog.Info("restored arrows from disk", "dimension", dim, "count", restoredArrows)
	}
	if restoredOrbs > 0 {
		slog.Info("restored experience orbs from disk", "dimension", dim, "count", restoredOrbs)
	}
	return nil
}

// parseMobUUID 解析十六进制（32 位）UUID 字符串。
func parseMobUUID(value string) ([16]byte, error) {
	var uuid [16]byte
	if len(value) != 32 {
		return uuid, fmt.Errorf("invalid UUID length %d", len(value))
	}
	if _, err := hex.Decode(uuid[:], []byte(value)); err != nil {
		return uuid, err
	}
	return uuid, nil
}

// entityAutosaveLoop 周期保存生物与掉落物数据；ctx 取消时退出。
func (s *Server) entityAutosaveLoop(ctx context.Context) {
	ticker := time.NewTicker(mobAutosaveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.saveEntities(); err != nil {
				slog.Error("entity autosave failed", "error", err)
			}
		}
	}
}
