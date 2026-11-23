package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mylabel/internal/ir"
)

// writeIRProject writes an IR project into dir, as phase 4's exporter will.
func writeIRProject(t *testing.T, dir string, p *ir.Project, images []ir.Image) {
	t.Helper()
	if err := ir.WriteProject(dir, p, images); err != nil {
		t.Fatalf("ir.WriteProject: %v", err)
	}
}

// irFixture is the project the direct-open tests import: two categories, one
// bbox, one rotated box and one polygon.
func irFixture() (*ir.Project, []ir.Image) {
	p := &ir.Project{
		IRVersion: ir.Version,
		Name:      "demo",
		Task:      ir.TaskMixed,
		Categories: []ir.Category{
			{Key: "person", Index: 0, Name: "person"},
			{Key: "bike", Index: 1, Name: "bike"},
		},
	}
	images := []ir.Image{
		{
			File: "a.png", Width: 100, Height: 50,
			Annotations: []ir.Annotation{
				{ID: "a1", Category: "person", Kind: ir.KindBBox, X: 10, Y: 5, W: 20, H: 10},
				{ID: "a2", Category: "bike", Kind: ir.KindRotated, Points: [][2]float64{{40, 10}, {60, 10}, {60, 20}, {40, 20}}},
			},
		},
		{
			File: "sub/b.png", Width: 100, Height: 50,
			Annotations: []ir.Annotation{
				{ID: "b1", Category: "person", Kind: ir.KindPolygon, Rings: [][][2]float64{{{1, 2}, {30, 2}, {30, 20}}}},
			},
		},
	}
	return p, images
}

// TestOpenDirImportsIRProject: opening a folder whose root carries project.json
// imports it through the same pipeline the wizard uses (§4.2 直接打开 IR).
func TestOpenDirImportsIRProject(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "a.png"), 100, 50)
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	writePNG(t, filepath.Join(dir, "sub", "b.png"), 100, 50)
	p, images := irFixture()
	writeIRProject(t, dir, p, images)

	a := New()
	t.Cleanup(a.Close)
	a.openDir(dir)

	if !strings.Contains(a.lastStatus, "已导入 IR 项目 2 张图片 3 条标注") {
		t.Fatalf("lastStatus = %q，期望含 IR 导入摘要", a.lastStatus)
	}
	if len(a.images) != 2 {
		t.Fatalf("载入 %d 张图片", len(a.images))
	}
	// A new folder starts with no categories, so the project's two are 0 and 1.
	if got := a.images[0].Anns; len(got) != 2 || got[0].Cat != 0 || got[1].Cat != 1 {
		t.Fatalf("a.png 标注 = %+v，期望 2 条（类别 0/1）", got)
	}
	if got := a.images[1].Anns; len(got) != 1 || !isPoly(&got[0]) {
		t.Fatalf("sub/b.png 标注 = %+v，期望 1 条多边形", got)
	}
	// Categories: exactly the two the project names, as the folder had none.
	if len(a.categories) != 2 {
		t.Fatalf("类别数 = %d，期望 2", len(a.categories))
	}
	if a.categories[0].Name != "person" || a.categories[1].Name != "bike" {
		t.Errorf("类别 = %+v，期望 person/bike", a.categories)
	}
	// Geometry: the bbox keeps its center, the rotated box its corners.
	box := a.images[0].Anns[0]
	if !near(box.CX, 20) || !near(box.CY, 10) || !near(box.W, 20) || !near(box.H, 10) {
		t.Errorf("bbox = %+v，期望中心 (20,10) 20x10", box)
	}
	assertSameShape(t, "rotated", &a.images[0].Anns[1], &Ann{CX: 50, CY: 15, W: 20, H: 10})

	// The import is persisted, and the project file is left alone.
	if _, err := os.Stat(filepath.Join(dir, irProjectName)); err != nil {
		t.Errorf("project.json 不该被删除: %v", err)
	}
	b := reopen(t, dir)
	if len(b.images[0].Anns) != 2 || len(b.images[1].Anns) != 1 {
		t.Fatalf("重开后标注 = %d/%d，期望 2/1", len(b.images[0].Anns), len(b.images[1].Anns))
	}
	if len(b.categories) != 2 {
		t.Errorf("重开后类别数 = %d，期望 2", len(b.categories))
	}
}

// TestOpenDirIRImportsOnce: the second open finds a repository that already has
// annotations, so nothing is imported twice.
func TestOpenDirIRImportsOnce(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "a.png"), 100, 50)
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	writePNG(t, filepath.Join(dir, "sub", "b.png"), 100, 50)
	p, images := irFixture()
	writeIRProject(t, dir, p, images)

	a := New()
	a.openDir(dir)
	first := len(a.images[0].Anns) + len(a.images[1].Anns)
	cats := len(a.categories)
	a.Close()

	b := New()
	t.Cleanup(b.Close)
	b.openDir(dir)
	if got := len(b.images[0].Anns) + len(b.images[1].Anns); got != first {
		t.Fatalf("第二次打开后标注 = %d，期望 %d（不重复导入）", got, first)
	}
	if len(b.categories) != cats {
		t.Errorf("第二次打开后类别数 = %d，期望 %d", len(b.categories), cats)
	}
	if strings.Contains(b.lastStatus, "已导入 IR 项目") {
		t.Errorf("lastStatus = %q，不该再导入", b.lastStatus)
	}
}

// TestOpenDirIRBeatsLegacyJSON: both files present, the IR project wins
// (§4.2 直接打开 IR).
func TestOpenDirIRBeatsLegacyJSON(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "a.png"), 100, 50)
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	writePNG(t, filepath.Join(dir, "sub", "b.png"), 100, 50)

	legacy := docFile{
		IRVersion:  docIRVersion,
		Categories: []string{"legacycat"},
		Images: []ImageInfo{
			{RelPath: "a.png", Width: 100, Height: 50, Anns: []Ann{{ID: "old", Cat: 0, CX: 5, CY: 5, W: 4, H: 4}}},
		},
	}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, docName), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	p, images := irFixture()
	writeIRProject(t, dir, p, images)

	a := New()
	t.Cleanup(a.Close)
	a.openDir(dir)

	if strings.Contains(a.lastStatus, "已迁移旧 JSON") {
		t.Errorf("lastStatus = %q，IR 项目应优先于旧 JSON", a.lastStatus)
	}
	got := a.images[0].Anns
	if len(got) != 2 {
		t.Fatalf("a.png 标注 = %d，期望 IR 的 2 条（旧 JSON 的 1 条不该进来）: %+v", len(got), got)
	}
	for _, an := range got {
		if an.ID == "old" {
			t.Errorf("旧 JSON 的标注被导入了: %+v", an)
		}
	}
	// The legacy categories never made it into the list either.
	if a.categoryIndexByName("legacycat") >= 0 {
		t.Errorf("旧 JSON 的类别被导入了: %+v", a.categories)
	}
}

// TestOpenDirLegacyJSONStillWorks: without project.json the legacy document is
// still migrated, so the new branch does not break the old one.
func TestOpenDirLegacyJSONStillWorks(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "a.png"), 100, 50)
	legacy := docFile{
		IRVersion:  docIRVersion,
		Categories: []string{"legacycat"},
		Images: []ImageInfo{
			{RelPath: "a.png", Width: 100, Height: 50, Anns: []Ann{{ID: "old", Cat: 0, CX: 5, CY: 5, W: 4, H: 4}}},
		},
	}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, docName), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	a := New()
	t.Cleanup(a.Close)
	a.openDir(dir)
	if len(a.images[0].Anns) != 1 || a.images[0].Anns[0].ID != "old" {
		t.Fatalf("旧 JSON 未迁移: %+v（status %q）", a.images[0].Anns, a.lastStatus)
	}
	if !strings.Contains(a.lastStatus, "已迁移旧 JSON 标注 1 张") {
		t.Errorf("lastStatus = %q", a.lastStatus)
	}
}

// TestOpenDirBrokenIRProject: a corrupt project.json is reported and the folder
// still opens, with no annotations.
func TestOpenDirBrokenIRProject(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "a.png"), 100, 50)
	if err := os.WriteFile(filepath.Join(dir, irProjectName), []byte("{ 不是 JSON"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := New()
	t.Cleanup(a.Close)
	a.openDir(dir)
	if len(a.images) != 1 {
		t.Fatalf("图片未打开: %+v（status %q）", a.images, a.lastStatus)
	}
	if len(a.images[0].Anns) != 0 {
		t.Errorf("坏项目不该导入标注: %+v", a.images[0].Anns)
	}
	if !strings.Contains(a.lastStatus, "IR 项目导入失败") {
		t.Errorf("lastStatus = %q，期望报 IR 导入失败", a.lastStatus)
	}
}

// TestOpenIRProjectSkipsSkippedAnns: an IR annotation the app model cannot hold
// is counted in the status line instead of aborting the import.
func TestImportIRProjectSkipsBadAnns(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "a.png"), 100, 50)
	p := &ir.Project{IRVersion: ir.Version, Task: ir.TaskDet, Categories: []ir.Category{{Key: "person", Name: "person"}}}
	images := []ir.Image{{
		File: "a.png", Width: 100, Height: 50,
		Annotations: []ir.Annotation{
			{ID: "good", Category: "person", Kind: ir.KindBBox, X: 1, Y: 1, W: 10, H: 10},
			{ID: "bad", Category: "person", Kind: ir.KindPolygon, Rings: [][][2]float64{{{0, 0}, {1, 1}}}},
		},
	}}
	writeIRProject(t, dir, p, images)

	a := New()
	t.Cleanup(a.Close)
	a.openDir(dir)
	if len(a.images[0].Anns) != 1 {
		t.Fatalf("标注 = %+v，期望只导入正常的一条", a.images[0].Anns)
	}
	if !strings.Contains(a.lastStatus, "1 条标注无法转换已跳过") {
		t.Errorf("lastStatus = %q，期望报跳过 1 条", a.lastStatus)
	}
}
