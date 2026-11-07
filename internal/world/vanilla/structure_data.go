//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package vanilla

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
)

// structureData 是官方结构定义/模板池/模板 NBT 的只读集合。
// 数据由 tools/genstructures 打包为 structures.tar.gz；模板按需解析。
type structureData struct {
	Structures    map[string]json.RawMessage
	StructureSets map[string]json.RawMessage
	TemplatePools map[string]json.RawMessage
	Templates     map[string][]byte
}

var (
	structureDataOnce sync.Once
	structureDataAll  *structureData
	structureDataErr  error
)

// LoadStructureData 读取并缓存全部结构数据。模板 NBT 只保存原始字节，
// 解析发生在实际放置时，避免启动时构造上万个小对象。
func LoadStructureData() (*structureData, error) {
	structureDataOnce.Do(func() {
		structureDataAll, structureDataErr = loadStructureData()
	})
	return structureDataAll, structureDataErr
}

func loadStructureData() (*structureData, error) {
	compressed, err := dataFiles.ReadFile("data/structures.tar.gz")
	if err != nil {
		return nil, fmt.Errorf("读取 structures.tar.gz 失败：%w", err)
	}
	gzipReader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, fmt.Errorf("解压 structures.tar.gz 失败：%w", err)
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	data := &structureData{
		Structures:    make(map[string]json.RawMessage),
		StructureSets: make(map[string]json.RawMessage),
		TemplatePools: make(map[string]json.RawMessage),
		Templates:     make(map[string][]byte),
	}
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("读取 structures.tar.gz 条目失败：%w", err)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		raw, err := io.ReadAll(reader)
		if err != nil {
			return nil, fmt.Errorf("读取 %s 失败：%w", header.Name, err)
		}
		name := header.Name
		switch {
		case strings.HasPrefix(name, "worldgen/structure/"):
			data.Structures[structureKey(name, "worldgen/structure/", ".json")] = raw
		case strings.HasPrefix(name, "worldgen/structure_set/"):
			data.StructureSets[structureKey(name, "worldgen/structure_set/", ".json")] = raw
		case strings.HasPrefix(name, "worldgen/template_pool/"):
			data.TemplatePools[structureKey(name, "worldgen/template_pool/", ".json")] = raw
		case strings.HasPrefix(name, "structure/"):
			data.Templates[structureKey(name, "structure/", ".nbt")] = raw
		}
	}
	return data, nil
}

func structureKey(name, prefix, suffix string) string {
	key := strings.TrimPrefix(name, prefix)
	key = strings.TrimSuffix(key, suffix)
	return qualify("minecraft:" + key)
}

// Template 返回按注册名索引的模板 NBT 原始字节。
func (d *structureData) Template(name string) ([]byte, bool) {
	raw, ok := d.Templates[qualify(name)]
	return raw, ok
}

// TemplatePool 返回按注册名索引的 template_pool JSON。
func (d *structureData) TemplatePool(name string) (json.RawMessage, bool) {
	raw, ok := d.TemplatePools[qualify(name)]
	return raw, ok
}
