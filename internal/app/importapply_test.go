package app

import (
	"maps"
	"reflect"
	"strings"
	"testing"

	"mylabel/internal/importer"
	"mylabel/internal/ir"
)

// importFixture is a parsed dataset over the images of an App opened by
// storeApp: a.jpg with one bbox, sub/b.jpg with a polygon and a rotated box.
func importFixture() *importer.Dataset {
	return &importer.Dataset{
		Categories: []ir.Category{{Key: "person", Name: "person"}, {Key: "bike", Name: "bike"}},
		Images: []ir.Image{
			{
				File: "a.png", Width: 100, Height: 50,
				Annotations: []ir.Annotation{
					{ID: "i1", Category: "person", Kind: ir.KindBBox, X: 10, Y: 5, W: 20, H: 10},
					{ID: "i2", Category: "bike", Kind: ir.KindRotated, Points: [][2]float64{{40, 10}, {60, 10}, {60, 20}, {40, 20}}},
				},
			},
			{
				File: "sub/b.png", Width: 100, Height: 50,
				Annotations: []ir.Annotation{
					{ID: "i3", Category: "person", Kind: ir.KindPolygon, Rings: [][][2]float64{{{1, 2}, {30, 2}, {30, 20}}}},
				},
			},
		},
	}
}

// importApp opens a folder with a.png and sub/b.png whose category list is
// person/car, so a dataset naming "person" reuses a category and "bike" needs a
// new one.
func importApp(t *testing.T) *App {
	t.Helper()
	a := storeApp(t, "a.png", "sub/b.png")
	a.categories = []Category{{Name: "person"}, {Name: "car"}}
	a.curCat = 0
	return a
}

// TestImportDatasetMapping applies a dataset whose first category matches an
// existing one and whose second is new (docs/phase3-design.md §4.2 step 3).
func TestImportDatasetMapping(t *testing.T) {
	a := importApp(t)
	ds := importFixture()

	choices := a.defaultChoices(ds)
	if len(choices) != 2 {
		t.Fatalf("默认映射 %d 行，期望 2", len(choices))
	}
	if choices[0].NewCat || choices[0].Target != 0 {
		t.Errorf("person 应复用 0 号类别，得到 %+v", choices[0])
	}
	if !choices[1].NewCat {
		t.Errorf("bike 应新建，得到 %+v", choices[1])
	}

	res := a.importDataset(ds, choices)
	if res.Images != 2 || res.Annots != 3 {
		t.Fatalf("摘要 = %+v，期望 2 张图 3 条标注", res)
	}
	if res.NewCategories != 1 {
		t.Errorf("NewCategories = %d，期望 1", res.NewCategories)
	}
	if res.SkippedImages != 0 || res.SkippedAnns != 0 {
		t.Errorf("不该有跳过: %+v", res)
	}
	if len(a.categories) != 3 || a.categories[2].Name != "bike" {
		t.Fatalf("类别 = %+v，期望追加 bike", a.categories)
	}

	// person → 0, bike → 2.
	if got := a.images[0].Anns; len(got) != 2 || got[0].Cat != 0 || got[1].Cat != 2 {
		t.Fatalf("a.png 标注类别 = %+v，期望 [0 2]", got)
	}
	if got := a.images[1].Anns; len(got) != 1 || got[0].Cat != 0 {
		t.Fatalf("sub/b.png 标注 = %+v", got)
	}

	// Geometry survives: the bbox keeps its center, the rotated box its corners.
	box := a.images[0].Anns[0]
	if box.Kind != KindRect || !near(box.CX, 20) || !near(box.CY, 10) || !near(box.W, 20) || !near(box.H, 10) || !near(box.Angle, 0) {
		t.Errorf("bbox = %+v，期望中心 (20,10) 20x10", box)
	}
	obb := a.images[0].Anns[1]
	assertSameShape(t, "rotated", &obb, &Ann{CX: 50, CY: 15, W: 20, H: 10})
	if !isPoly(&a.images[1].Anns[0]) || len(a.images[1].Anns[0].Points) != 3 {
		t.Errorf("多边形 = %+v", a.images[1].Anns[0])
	}
	if a.images[1].Anns[0].Points[2] != (Point{30, 20}) {
		t.Errorf("多边形顶点被改写: %v", a.images[1].Anns[0].Points)
	}

	// Exactly the two imported images are dirty, and a save writes them.
	wantDirty := map[int64]bool{a.images[0].ID: true, a.images[1].ID: true}
	if !reflect.DeepEqual(a.dirtyImages, wantDirty) {
		t.Errorf("dirtyImages = %v，期望 %v", a.dirtyImages, wantDirty)
	}
	if err := a.save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	b := reopen(t, a.dir)
	if len(b.images) != 2 {
		t.Fatalf("重开后 %d 张图", len(b.images))
	}
	if got := b.images[1].Anns; len(got) != 1 || !isPoly(&got[0]) || len(got[0].Points) != 3 {
		t.Errorf("重开后 sub/b.png 标注 = %+v", got)
	}
	if len(b.categories) != 3 || b.categories[2].Name != "bike" {
		t.Errorf("重开后类别 = %+v", b.categories)
	}
}

// TestImportDatasetExistingCategoryReuse maps a source category onto a
// different existing category through the mapping select.
func TestImportDatasetExistingCategoryReuse(t *testing.T) {
	a := importApp(t)
	ds := importFixture()
	choices := a.defaultChoices(ds)
	// Map "bike" onto the existing "car" (index 1).
	choices[1].Pick = a.existingOption(1)

	res := a.importDataset(ds, choices)
	if res.NewCategories != 0 || len(a.categories) != 2 {
		t.Fatalf("摘要 = %+v, 类别 = %+v，期望没有新建", res, a.categories)
	}
	if got := a.images[0].Anns[1].Cat; got != 1 {
		t.Errorf("bike 映射到 %d，期望 1", got)
	}
}

// TestImportDatasetSkippedImages: a dataset image with no counterpart in the
// folder is counted and named, and the rest still imports.
func TestImportDatasetSkippedImages(t *testing.T) {
	a := importApp(t)
	ds := importFixture()
	ds.Images = append(ds.Images, ir.Image{
		File: "nowhere/x.png", Width: 10, Height: 10,
		Annotations: []ir.Annotation{{ID: "x", Category: "person", Kind: ir.KindBBox, W: 1, H: 1}},
	})

	res := a.importDataset(ds, a.defaultChoices(ds))
	if res.Images != 2 || res.SkippedImages != 1 || res.Annots != 3 {
		t.Fatalf("摘要 = %+v，期望 2 张图 / 跳过 1 张 / 3 条标注", res)
	}
	if len(res.Warnings) == 0 || !strings.Contains(res.Warnings[0], "nowhere/x.png") {
		t.Fatalf("警告 = %v，期望点名未匹配的图片", res.Warnings)
	}
	if !strings.Contains(res.String(), "跳过 1 张不匹配图片") {
		t.Errorf("摘要文本 = %q", res.String())
	}
}

// TestImportDatasetSkippedAnns: annotations the app model cannot hold are
// skipped one by one, so a partly broken dataset still imports (§4.2 step 4).
func TestImportDatasetSkippedAnns(t *testing.T) {
	a := importApp(t)
	ds := &importer.Dataset{
		Categories: []ir.Category{{Key: "person", Name: "person"}},
		Images: []ir.Image{{
			File: "a.png", Width: 100, Height: 50,
			Annotations: []ir.Annotation{
				{ID: "ok", Category: "person", Kind: ir.KindBBox, X: 1, Y: 1, W: 5, H: 5},
				{ID: "short", Category: "person", Kind: ir.KindPolygon, Rings: [][][2]float64{{{0, 0}, {1, 1}}}},
				{ID: "zero", Category: "person", Kind: ir.KindBBox, W: 0, H: 5},
				{ID: "who", Category: "nobody", Kind: ir.KindBBox, W: 4, H: 4},
			},
		}},
	}
	res := a.importDataset(ds, a.defaultChoices(ds))
	if res.Annots != 1 || res.SkippedAnns != 3 {
		t.Fatalf("摘要 = %+v，期望 1 条写入 / 3 条跳过", res)
	}
	if len(a.images[0].Anns) != 1 || a.images[0].Anns[0].ID != "ok" {
		t.Fatalf("写入的标注 = %+v", a.images[0].Anns)
	}
	if !strings.Contains(res.String(), "跳过 3 条无法转换的标注") {
		t.Errorf("摘要文本 = %q", res.String())
	}
}

// TestImportDatasetNoMatchLeavesProjectAlone: nothing matched means no
// annotations and no half-created categories.
func TestImportDatasetNoMatchLeavesProjectAlone(t *testing.T) {
	a := importApp(t)
	ds := &importer.Dataset{
		Categories: []ir.Category{{Key: "alien", Name: "alien"}},
		Images: []ir.Image{{
			File: "nope.png", Width: 10, Height: 10,
			Annotations: []ir.Annotation{{ID: "1", Category: "alien", Kind: ir.KindBBox, W: 1, H: 1}},
		}},
	}
	res := a.importDataset(ds, a.defaultChoices(ds))
	if res.Annots != 0 || res.NewCategories != 0 {
		t.Fatalf("摘要 = %+v，期望什么都没写", res)
	}
	if len(a.categories) != 2 {
		t.Fatalf("类别 = %+v，期望保持 person/car", a.categories)
	}
	if len(a.undoStack) != 0 {
		t.Errorf("没有写入却压入了 undo 命令")
	}
}

// TestImportUndoRedo: the whole import is one undo step (§4.2 step 5).
func TestImportUndoRedo(t *testing.T) {
	a := importApp(t)
	ds := importFixture()
	res := a.importDataset(ds, a.defaultChoices(ds))
	if res.Annots != 3 {
		t.Fatalf("摘要 = %+v", res)
	}
	if len(a.undoStack) != 1 {
		t.Fatalf("undo 栈 %d 层，期望 1 层", len(a.undoStack))
	}

	a.undo()
	if n := len(a.images[0].Anns) + len(a.images[1].Anns); n != 0 {
		t.Fatalf("撤销后还剩 %d 条标注", n)
	}
	// Undo marks both images dirty again: their rows must be rewritten.
	if len(a.dirtyImages) != 2 {
		t.Errorf("撤销后 dirtyImages = %v，期望两张图", a.dirtyImages)
	}

	a.redo()
	if len(a.images[0].Anns) != 2 || len(a.images[1].Anns) != 1 {
		t.Fatalf("重做后标注 = %d/%d，期望 2/1", len(a.images[0].Anns), len(a.images[1].Anns))
	}
	// A redone import inserts fresh copies, never the live ones.
	a.images[0].Anns[0].CX = 999
	a.undo()
	a.redo()
	if got := a.images[0].Anns[0].CX; got == 999 {
		t.Errorf("重做复用了被改动的标注快照")
	}
}

// TestImportUniqueIDs: ids stay unique within an image, against the live
// annotations and among the imported ones; a unique imported id is kept.
func TestImportUniqueIDs(t *testing.T) {
	a := importApp(t)
	a.images[0].Anns = []Ann{{ID: "live", Cat: 0, CX: 5, CY: 5, W: 2, H: 2}}
	ds := &importer.Dataset{
		Categories: []ir.Category{{Key: "person", Name: "person"}},
		Images: []ir.Image{{
			File: "a.png", Width: 100, Height: 50,
			Annotations: []ir.Annotation{
				{ID: "live", Category: "person", Kind: ir.KindBBox, W: 2, H: 2},
				{ID: "kept", Category: "person", Kind: ir.KindBBox, W: 3, H: 3},
				{ID: "kept", Category: "person", Kind: ir.KindBBox, W: 4, H: 4},
				{ID: "", Category: "person", Kind: ir.KindBBox, W: 5, H: 5},
			},
		}},
	}
	a.importDataset(ds, a.defaultChoices(ds))

	seen := make(map[string]bool)
	for _, an := range a.images[0].Anns {
		if an.ID == "" {
			t.Fatalf("空 ID 未被改写: %+v", an)
		}
		if seen[an.ID] {
			t.Fatalf("ID %q 重复: %+v", an.ID, a.images[0].Anns)
		}
		seen[an.ID] = true
	}
	if !seen["kept"] {
		t.Errorf("唯一的导入 ID 应保留，得到 %v", seen)
	}
	if !seen["live"] {
		t.Errorf("原标注 ID 应保留，得到 %v", seen)
	}
}

// TestImportDroppedRingsAndAttrs reports the two things the app model cannot
// hold yet (§4.1).
func TestImportDroppedRingsAndAttrs(t *testing.T) {
	a := importApp(t)
	ds := &importer.Dataset{
		Categories: []ir.Category{{Key: "person", Name: "person"}},
		Images: []ir.Image{{
			File: "a.png", Width: 100, Height: 50,
			Annotations: []ir.Annotation{{
				ID: "1", Category: "person", Kind: ir.KindPolygon,
				Rings: [][][2]float64{
					{{0, 0}, {20, 0}, {20, 20}, {0, 20}},
					{{5, 5}, {10, 5}, {10, 10}, {5, 10}},
				},
				Attributes: map[string]string{"iscrowd": "1"},
			}},
		}},
	}
	res := a.importDataset(ds, a.defaultChoices(ds))
	if res.DroppedRings != 1 || res.Attrs != 1 {
		t.Fatalf("摘要 = %+v，期望丢弃 1 环、1 条带属性", res)
	}
	got := a.images[0].Anns[0]
	if len(got.Points) != 4 {
		t.Errorf("只应保留外环，得到 %v", got.Points)
	}
	// W3: the attributes are kept on the annotation, not only counted.
	if want := map[string]string{"iscrowd": "1"}; !maps.Equal(got.Attributes, want) {
		t.Errorf("导入的 attributes = %v，期望 %v", got.Attributes, want)
	}
	text := res.String()
	if !strings.Contains(text, "丢弃内部环 1 个") || !strings.Contains(text, "1 条带属性") {
		t.Errorf("摘要文本 = %q", text)
	}
}

// TestImportAppendsToExisting: an import adds to what is already there instead
// of replacing it, and one undo takes only the imported part back.
func TestImportAppendsToExisting(t *testing.T) {
	a := importApp(t)
	seedAnns(t, a, 0, []Ann{{ID: "ann_old", Cat: 0, CX: 5, CY: 5, W: 4, H: 4}})
	a.importDataset(importFixture(), a.defaultChoices(importFixture()))
	if len(a.images[0].Anns) != 3 {
		t.Fatalf("a.png 标注 = %d，期望 1 原有 + 2 导入", len(a.images[0].Anns))
	}
	a.undo()
	got := a.images[0].Anns
	if len(got) != 1 || got[0].ID != "ann_old" {
		t.Fatalf("撤销后 = %+v，期望只剩原有标注", got)
	}
}

// TestParseImportDispatch covers the wizard's parse step without a window: a
// broken path is an error, and an IR project parses into a dataset.
func TestParseImportDispatch(t *testing.T) {
	a := importApp(t)

	a.wiz = wizardState{Format: formatCOCO, Path: ""}
	if _, _, err := a.parseImport(); err == nil {
		t.Error("空路径应报错")
	}

	a.wiz = wizardState{Format: formatCOCO, Path: t.TempDir() + "/missing.json"}
	if _, _, err := a.parseImport(); err == nil {
		t.Error("不存在的 COCO 文件应报错")
	}

	// A VOC directory that does not exist is a fatal error too.
	a.wiz = wizardState{Format: formatVOC, Path: t.TempDir() + "/missing"}
	if _, _, err := a.parseImport(); err == nil {
		t.Error("不存在的 VOC 目录应报错")
	}
}

// TestWizardParseFailureKeepsStep: a failed parse leaves the wizard on the path
// step with the message shown, and no dataset.
func TestWizardParseFailureKeepsStep(t *testing.T) {
	a := importApp(t)
	a.openImportWizard()
	a.wiz.Format = formatCOCO
	a.wiz.Step = wizStepPath
	a.wiz.Path = t.TempDir() + "/missing.json"

	a.wizardNext()
	if a.wiz.Step != wizStepPath {
		t.Fatalf("step = %v，期望留在路径步", a.wiz.Step)
	}
	if a.wiz.Err == "" {
		t.Error("解析失败应记录错误信息")
	}
	if a.wiz.DS != nil {
		t.Error("解析失败不该留下数据集")
	}
	if len(a.images[0].Anns) != 0 {
		t.Error("解析失败不该写标注")
	}
}
