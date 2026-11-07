package vanilla

import "testing"

func TestParseStructureTemplateNBT(t *testing.T) {
	for _, path := range []string{"testdata/terminator.nbt", "testdata/gold.nbt"} {
		raw := mustReadTestdata(t, path)
		template, err := parseStructureTemplate(raw)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if template.Size[0] <= 0 || template.Size[1] <= 0 || template.Size[2] <= 0 {
			t.Fatalf("%s: 非法模板尺寸 %v", path, template.Size)
		}
		if len(template.Palette) == 0 || len(template.Blocks) == 0 {
			t.Fatalf("%s: 模板没有 palette/blocks", path)
		}
		for _, block := range template.Blocks {
			if block.State < 0 || block.State >= len(template.Palette) {
				t.Fatalf("%s: 方块 palette 索引 %d 越界", path, block.State)
			}
		}
	}
}

func TestParseTemplatePool(t *testing.T) {
	pool, err := parseTemplatePool(mustReadTestdata(t, "testdata/terminators_pool.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pool.Elements) != 4 {
		t.Fatalf("element 数 = %d, want 4", len(pool.Elements))
	}
	for _, element := range pool.Elements {
		if element.Element.Type != "minecraft:legacy_single_pool_element" {
			t.Fatalf("element type = %q", element.Element.Type)
		}
		if element.Element.Location == "" {
			t.Fatal("element 缺少 location")
		}
	}
}
