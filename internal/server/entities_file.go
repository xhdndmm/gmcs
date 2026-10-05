package server

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"gmcs/internal/item"
	"gmcs/internal/registry"
)

// 实体持久化：世界目录下的 entities.json（生物 + 掉落物）。
//
// 说明：gmcs 的区块存储是自定义格式（不含实体），因此实体单独保存在
// 世界目录的 entities.json 中；文件采用“临时文件 + 重命名”的原子写入。
// 恢复时保留类型、位置、朝向、生命、物品堆叠与 UUID（实体 ID 重新分配）；
// 掉落物的速度不保存（重启后停在保存位置）。

// entityFileName 是世界目录中实体数据的文件名。
const entityFileName = "entities.json"

type entityFile struct {
	Mobs  []mobRecord  `json:"mobs"`
	Items []itemRecord `json:"items,omitempty"`
}

// itemRecord 是一条持久化的掉落物（速度不保存）。
type itemRecord struct {
	Item  string  `json:"item"`
	Count int32   `json:"count"`
	UUID  string  `json:"uuid"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Z     float64 `json:"z"`
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
}

// saveEntities 把世界中的生物与掉落物写入世界目录（原子写入）。
func (s *Server) saveEntities() error {
	s.entityMu.Lock()
	records := make([]mobRecord, 0, len(s.mobs))
	for _, m := range s.mobs {
		if m.Dead {
			continue
		}
		records = append(records, mobRecord{
			Type:   zombieTypeName,
			UUID:   hex.EncodeToString(m.UUID[:]),
			X:      m.X,
			Y:      m.Y,
			Z:      m.Z,
			Yaw:    m.Yaw,
			Pitch:  m.Pitch,
			Health: m.Health,
		})
	}
	items := make([]itemRecord, 0, len(s.items))
	for _, e := range s.items {
		name, ok := registry.ItemName(e.Stack.ItemID)
		if !ok {
			continue
		}
		items = append(items, itemRecord{
			Item:  name,
			Count: e.Stack.Count,
			UUID:  hex.EncodeToString(e.UUID[:]),
			X:     e.X,
			Y:     e.Y,
			Z:     e.Z,
		})
	}
	s.entityMu.Unlock()

	content, err := json.MarshalIndent(entityFile{Mobs: records, Items: items}, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	path := filepath.Join(s.config.WorldDir, entityFileName)
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

// loadEntities 从世界目录恢复生物与掉落物。文件不存在时不做任何事；
// 未知类型与损坏的条目会被跳过（只记录警告）。
func (s *Server) loadEntities() error {
	path := filepath.Join(s.config.WorldDir, entityFileName)
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

	restored := 0
	restoredItems := 0
	s.entityMu.Lock()
	for _, record := range file.Mobs {
		if record.Type != zombieTypeName {
			slog.Warn("跳过不支持的生物类型", "type", record.Type)
			continue
		}
		uuid, uuidErr := parseMobUUID(record.UUID)
		if uuidErr != nil {
			slog.Warn("生物的 UUID 无效，重新生成", "uuid", record.UUID, "error", uuidErr)
			uuid = newEntityUUID()
		}
		health := record.Health
		if health <= 0 {
			health = zombieMaxHealth
		}
		m := &mob{
			ID:     s.entityIDs.Add(1),
			UUID:   uuid,
			TypeID: s.zombieTypeID,
			X:      record.X,
			Y:      record.Y,
			Z:      record.Z,
			Yaw:    record.Yaw,
			Pitch:  record.Pitch,
			Health: health,
		}
		m.HeadYaw = m.Yaw
		s.mobs[m.ID] = m
		restored++
	}
	for _, record := range file.Items {
		stack, itemErr := item.FromName(record.Item, record.Count)
		if itemErr != nil {
			slog.Warn("跳过无效的掉落物记录", "item", record.Item, "error", itemErr)
			continue
		}
		stack.Count = min(stack.Count, stack.MaxStack())
		uuid, uuidErr := parseMobUUID(record.UUID)
		if uuidErr != nil {
			slog.Warn("掉落物的 UUID 无效，重新生成", "uuid", record.UUID, "error", uuidErr)
			uuid = newEntityUUID()
		}
		e := &itemEntity{
			ID:               s.entityIDs.Add(1),
			UUID:             uuid,
			Stack:            stack,
			X:                record.X,
			Y:                record.Y,
			Z:                record.Z,
			PickupDelayTicks: itemPickupDelayMining,
		}
		s.items[e.ID] = e
		restoredItems++
	}
	s.entityMu.Unlock()
	if restored > 0 {
		slog.Info("restored mobs from disk", "count", restored)
	}
	if restoredItems > 0 {
		slog.Info("restored items from disk", "count", restoredItems)
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
