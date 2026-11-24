package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestNewWorkspaceHasNoCategories: a folder with no stored categories opens
// with an empty list and no current category — the panel's 添加 makes the first
// one (contract §1, §2).
func TestNewWorkspaceHasNoCategories(t *testing.T) {
	a := New()
	if len(a.categories) != 0 || a.curCat != -1 {
		t.Fatalf("新 App 类别 = %v / curCat = %d，期望空表与 -1", a.categories, a.curCat)
	}

	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "a.png"), 20, 10)
	b := New()
	t.Cleanup(b.Close)
	b.openDir(dir)
	if len(b.categories) != 0 {
		t.Errorf("新文件夹载入 %d 个类别，期望 0：%+v", len(b.categories), b.categories)
	}
	if b.curCat != -1 {
		t.Errorf("curCat = %d，期望 -1", b.curCat)
	}
	if len(b.images) != 1 {
		t.Fatalf("载入 %d 张图片，期望 1：%s", len(b.images), b.lastStatus)
	}
}

// emptyCatApp returns the shared Tester's App with its categories cleared, the
// state of a freshly opened folder.
func emptyCatApp(t *testing.T) (*App, *ui.Tester) {
	t.Helper()
	a, tt := tester(t)
	a.categories = nil
	a.curCat = -1
	return a, tt
}

// TestEmptyCategoriesBlockAllTools: with no category to point at, none of the
// three tools starts a draft, and the status bar says what is missing
// (contract §3).
func TestEmptyCategoriesBlockAllTools(t *testing.T) {
	a, tt := emptyCatApp(t)
	tt.Frame()

	// Rectangle: a plain drag on empty canvas.
	a.setTool(ToolRect)
	dragImage(t, a, tt, Point{100, 100}, Point{300, 250})
	if got := anns(a); len(got) != 0 {
		t.Fatalf("空类别表下创建了 %d 条标注：%+v", len(got), got)
	}
	if a.mode != ModeIdle {
		t.Errorf("mode = %v，期望 Idle（不该进入创建手势）", a.mode)
	}
	if !strings.Contains(a.lastStatus, "请先在右侧面板添加类别") {
		t.Errorf("lastStatus = %q，期望提示先添加类别", a.lastStatus)
	}

	// Polygon: two clicks would normally be a draft.
	a.setTool(ToolPoly)
	clickImage(t, a, tt, Point{120, 120})
	clickImage(t, a, tt, Point{220, 160})
	if a.mode == ModePolyCreate || len(a.polyDraft) != 0 {
		t.Errorf("多边形草稿被启动了：mode=%v draft=%v", a.mode, a.polyDraft)
	}
	if len(anns(a)) != 0 {
		t.Errorf("空类别表下多边形写出了标注：%+v", anns(a))
	}

	// Rotated box: four clicks would make the box.
	a.setTool(ToolObb)
	for _, p := range []Point{{100, 100}, {200, 100}, {200, 150}, {100, 150}} {
		clickImage(t, a, tt, p)
	}
	if len(anns(a)) != 0 {
		t.Errorf("空类别表下旋转框写出了标注：%+v", anns(a))
	}
	if !strings.Contains(a.lastStatus, "请先在右侧面板添加类别") {
		t.Errorf("lastStatus = %q，期望提示先添加类别", a.lastStatus)
	}
}

// TestEmptyCategoryPanelRendersHint: the panel explains the empty state instead
// of rendering nothing.
func TestEmptyCategoryPanelRendersHint(t *testing.T) {
	a := newTestApp()
	a.categories = nil
	a.curCat = -1
	tt := panelFor(t, a)
	tt.Frame()

	if !tt.HasText("暂无类别，点“添加”新建") {
		t.Errorf("面板缺少空态提示：%v", tt.Texts())
	}
	// 添加 is still there, 删除 is a no-op, and neither panics.
	if err := tt.Click("添加"); err != nil {
		t.Fatalf("点击添加: %v", err)
	}
	if len(a.categories) != 1 || a.curCat != 0 {
		t.Fatalf("添加后类别 = %v / curCat = %d，期望 1 个类别且 curCat 0", a.categories, a.curCat)
	}
	a.deleteCategory(0)
	tt.Frame()
	if len(a.categories) != 0 || a.curCat != -1 {
		t.Fatalf("删光后类别 = %v / curCat = %d，期望空表与 -1", a.categories, a.curCat)
	}
	if err := tt.Click("删除"); err != nil {
		t.Fatalf("空表点删除: %v", err)
	}
	if a.curCat != -1 || len(a.categories) != 0 {
		t.Errorf("空表点删除改变了状态：类别 %v curCat %d", a.categories, a.curCat)
	}
}

// TestEmptyCategoriesKeepShortcutsSafe: digits, L and the annotation dialogs do
// nothing harmful on an empty list — no panic, no out-of-range index.
func TestEmptyCategoriesKeepShortcutsSafe(t *testing.T) {
	a, tt := emptyCatApp(t)
	tt.Frame()

	for k := ui.Key1; k <= ui.Key9; k++ {
		a.handleCanvasInput(ui.Element{}, polyKey(k))
		if a.curCat != -1 {
			t.Fatalf("按 %v 后 curCat = %d，期望 -1", k, a.curCat)
		}
	}
	a.handleCanvasInput(ui.Element{}, polyKey(ui.KeyL))
	if a.showLabels {
		t.Error("L 在空类别表下应照常切换标签")
	}
	a.handleCanvasInput(ui.Element{}, polyKey(ui.KeyL))
	if !a.showLabels {
		t.Error("再按 L 应切回显示")
	}

	// The dialogs that read the category list: the properties panel and the
	// palette. Neither opens here (no annotation, no swatch), so render the
	// panel to prove the closed modals stay harmless.
	a.openProps(0)
	if a.props.Open {
		t.Error("没有标注时属性面板不该打开")
	}
	panelFor(t, a).Frame()
	if a.props.Open {
		t.Error("属性面板不该自己打开")
	}
	// catColor and the chip measurement fall back instead of indexing.
	if got := a.catColor(0); got.A == 0 {
		t.Errorf("空表的 catColor(0) = %v，期望回退色", got)
	}
}

// TestFirstCategoryEnablesCreation: adding the first category makes it current
// and creation works again, tagged with it.
func TestFirstCategoryEnablesCreation(t *testing.T) {
	a, tt := emptyCatApp(t)
	tt.Frame()

	a.addCategory()
	if len(a.categories) != 1 || a.categories[0].Name != "cat1" {
		t.Fatalf("类别 = %+v，期望只有一个 cat1", a.categories)
	}
	if a.curCat != 0 {
		t.Fatalf("curCat = %d，期望 0", a.curCat)
	}
	drawRect(t, a, tt, Point{100, 100}, Point{300, 250})
	got := anns(a)
	if len(got) != 1 {
		t.Fatalf("添加类别后写了 %d 条标注，期望 1：%s", len(got), a.lastStatus)
	}
	if got[0].Cat != 0 || got[0].Kind != KindRect {
		t.Errorf("新标注 = %+v，期望类别 0 的矩形", got[0])
	}
}

// TestExportJSONWithNoCategories: the exchange document writes an empty array
// for an empty list, not null.
func TestExportJSONWithNoCategories(t *testing.T) {
	a := storeApp(t, "a.png")
	a.categories = nil
	a.curCat = -1

	if err := a.exportJSON(a.dir); err != nil {
		t.Fatalf("exportJSON: %v", err)
	}
	doc, err := loadDoc(a.dir)
	if err != nil || doc == nil {
		t.Fatalf("loadDoc = %v, %v", doc, err)
	}
	if doc.Categories == nil {
		t.Errorf("导出的 categories 为 null，期望空数组")
	}
	if len(doc.Categories) != 0 {
		t.Errorf("导出的 categories = %v，期望空", doc.Categories)
	}
	if len(doc.Images) != 1 {
		t.Errorf("导出的图片数 = %d，期望 1", len(doc.Images))
	}
}
