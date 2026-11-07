package vanilla

import "testing"

func TestLoadStructureData(t *testing.T) {
	data, err := LoadStructureData()
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Structures) < 30 || len(data.StructureSets) < 15 || len(data.TemplatePools) < 100 || len(data.Templates) < 1000 {
		t.Fatalf("结构数据不完整：structures=%d sets=%d pools=%d templates=%d", len(data.Structures), len(data.StructureSets), len(data.TemplatePools), len(data.Templates))
	}
	raw, ok := data.Template("minecraft:village/plains/terminators/terminator_02")
	if !ok {
		t.Fatal("缺少 plains terminator 模板")
	}
	template, err := parseStructureTemplate(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(template.Blocks) == 0 {
		t.Fatal("模板没有方块")
	}
	poolRaw, ok := data.TemplatePool("minecraft:village/plains/terminators")
	if !ok {
		t.Fatal("缺少 plains terminators 模板池")
	}
	pool, err := parseTemplatePool(poolRaw)
	if err != nil {
		t.Fatal(err)
	}
	if len(pool.Elements) == 0 {
		t.Fatal("模板池没有元素")
	}
}
