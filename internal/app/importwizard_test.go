package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"mylabel/internal/ir"
)

// The wizard tests render the dialog over the status bar and an open button, and
// drive it the way a user does: click a step, then the next. The path is written
// straight into the App's wizard state, which is what the platform dialog would
// do — without a window there is nothing to click (§4.3).
var (
	wizAppRef *App
	wizTester *ui.Tester
)

func wizView(c *ui.Context) {
	a := wizAppRef
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Children(func() {
			if ui.Button(c.Key("wizOpen"), "导入标注").Clicked() {
				a.openImportWizard()
			}
		})
		a.importModal(c)
		a.statusBar(c)
	})
}

// wizardFor shows a in the shared wizard Tester (mygo issue #1902: one Tester
// per package, swap the App behind it).
func wizardFor(t *testing.T, a *App) *ui.Tester {
	t.Helper()
	wizAppRef = a
	if wizTester == nil {
		wizTester = ui.NewTester(wizView, testWinW, testWinH)
	} else {
		wizTester.Frame()
	}
	return wizTester
}

// wizardIRFixture writes an IR project into dir: two categories (one new, one
// that matches the default category "cat1"), two convertible annotations and one
// polygon Validate rejects.
func wizardIRFixture(t *testing.T, dir string) {
	t.Helper()
	p := &ir.Project{
		IRVersion: ir.Version,
		Task:      ir.TaskMixed,
		Categories: []ir.Category{
			{Key: "person", Name: "person"},
			{Key: "cat1", Name: "cat1"},
		},
	}
	images := []ir.Image{{
		File: "a.png", Width: 100, Height: 50,
		Annotations: []ir.Annotation{
			{ID: "p1", Category: "person", Kind: ir.KindBBox, X: 10, Y: 5, W: 20, H: 10},
			{ID: "c1", Category: "cat1", Kind: ir.KindBBox, X: 40, Y: 20, W: 10, H: 10},
			{ID: "bad", Category: "person", Kind: ir.KindPolygon, Rings: [][][2]float64{{{0, 0}, {1, 1}}}},
		},
	}}
	writeIRProject(t, dir, p, images)
}

// TestImportWizardIRFlow clicks through all four steps: format, path, mapping
// (changing one row through its select) and the report, then applies the correct
// part of a dataset that also holds an invalid annotation (§4.2, §4.3).
func TestImportWizardIRFlow(t *testing.T) {
	a := storeApp(t, "a.png")
	wizardIRFixture(t, a.dir)
	tt := wizardFor(t, a)

	if err := tt.Click("导入标注"); err != nil {
		t.Fatalf("点击导入标注: %v", err)
	}
	if !a.wiz.Open || a.wiz.Step != wizStepFormat {
		t.Fatalf("wizard = %+v，期望打开在第 1 步", a.wiz)
	}
	if !tt.HasText("选择来源格式") {
		t.Fatalf("第 1 步没有渲染格式选项: %v", tt.Texts())
	}

	if err := tt.Click("IR 项目"); err != nil {
		t.Fatalf("选择 IR 格式: %v", err)
	}
	if a.wiz.Format != formatIR {
		t.Fatalf("Format = %v，期望 IR", a.wiz.Format)
	}
	if err := tt.Click("下一步"); err != nil {
		t.Fatalf("进入路径步: %v", err)
	}
	if a.wiz.Step != wizStepPath {
		t.Fatalf("step = %v，期望路径步", a.wiz.Step)
	}
	if !tt.HasText("含 project.json") {
		t.Errorf("路径步没有提示 IR 项目根: %v", tt.Texts())
	}

	// What the platform dialog would set.
	a.wiz.Path = a.dir
	if err := tt.Click("下一步"); err != nil {
		t.Fatalf("解析: %v", err)
	}
	if a.wiz.Step != wizStepMapping {
		t.Fatalf("step = %v（err %q），期望映射步", a.wiz.Step, a.wiz.Err)
	}
	if len(a.wiz.Choices) != 2 {
		t.Fatalf("映射行 = %d，期望 2: %+v", len(a.wiz.Choices), a.wiz.Choices)
	}
	if !a.wiz.Choices[0].NewCat {
		t.Errorf("person 应默认新建: %+v", a.wiz.Choices[0])
	}
	if a.wiz.Choices[1].NewCat || a.wiz.Choices[1].Target != 0 {
		t.Errorf("cat1 应默认复用 0 号类别: %+v", a.wiz.Choices[1])
	}
	if !tt.HasText("解析预览：1 张图片 / 3 条标注") {
		t.Errorf("映射步没有预览: %v", tt.Texts())
	}

	// Change the second row's category through its select: 1. cat1 → 2. cat2.
	if err := tt.Click("映射:cat1"); err != nil {
		t.Fatalf("打开类别映射下拉: %v", err)
	}
	if err := tt.Click("2. cat2"); err != nil {
		t.Fatalf("选择 cat2: %v", err)
	}
	if got := a.wiz.Choices[1].Pick; got != "2. cat2" {
		t.Fatalf("Pick = %q，期望 2. cat2（下拉未写回）", got)
	}

	if err := tt.Click("下一步"); err != nil {
		t.Fatalf("进入报告步: %v", err)
	}
	if a.wiz.Step != wizStepReport {
		t.Fatalf("step = %v，期望报告步", a.wiz.Step)
	}
	if len(a.wiz.Issues) != 1 {
		t.Fatalf("Issues = %+v，期望 1 条（2 点多边形）", a.wiz.Issues)
	}
	if !tt.HasText("[错误] a.png 第 3 条 rings") {
		t.Errorf("报告没有渲染可定位错误: %v", tt.Texts())
	}
	if !tt.HasText("导入时错误条目会被跳过") {
		t.Errorf("报告缺少说明: %v", tt.Texts())
	}

	if err := tt.Click("导入正确部分"); err != nil {
		t.Fatalf("应用导入: %v", err)
	}
	if a.wiz.Open {
		t.Error("应用后应关闭对话框")
	}
	if !a.wiz.Applied {
		t.Error("Applied 应为 true")
	}
	anns := a.images[0].Anns
	if len(anns) != 2 {
		t.Fatalf("写入 %d 条标注，期望 2（正确部分）: %+v", len(anns), anns)
	}
	// person → the appended category (index 9), cat1 → the mapped cat2 (index 1).
	cats := map[int]bool{}
	for _, an := range anns {
		cats[an.Cat] = true
	}
	if !cats[1] || !cats[len(defaultCategories)] {
		t.Errorf("标注类别 = %v，期望 {1, %d}（映射到的 cat2 与新建的 person）", cats, len(defaultCategories))
	}
	if len(a.categories) != len(defaultCategories)+1 || a.categories[len(defaultCategories)].Name != "person" {
		t.Errorf("类别 = %+v，期望只新建 person", a.categories)
	}
	if got := a.wiz.Summary; got.Images != 1 || got.Annots != 2 || got.SkippedAnns != 1 || got.NewCategories != 1 {
		t.Errorf("摘要 = %+v，期望 1 图 2 标注 / 跳过 1 / 新建 1", got)
	}
	if !strings.Contains(a.lastStatus, "已导入 1 张图片 2 条标注") {
		t.Errorf("lastStatus = %q", a.lastStatus)
	}
	if !strings.Contains(a.lastStatus, "跳过 1 条无法转换的标注") {
		t.Errorf("lastStatus 没提跳过: %q", a.lastStatus)
	}
}

// TestImportWizardParseFailure: a broken source keeps the wizard on the path
// step, shows the error and imports nothing (§4.3).
func TestImportWizardParseFailure(t *testing.T) {
	a := storeApp(t, "a.png")
	tt := wizardFor(t, a)

	if err := tt.Click("导入标注"); err != nil {
		t.Fatalf("点击导入标注: %v", err)
	}
	if err := tt.Click("COCO"); err != nil {
		t.Fatalf("选择 COCO: %v", err)
	}
	if a.wiz.Format != formatCOCO {
		t.Fatalf("Format = %v，期望 COCO", a.wiz.Format)
	}
	if err := tt.Click("下一步"); err != nil {
		t.Fatalf("进入路径步: %v", err)
	}
	a.wiz.Path = filepath.Join(t.TempDir(), "nope.json")
	if err := tt.Click("下一步"); err != nil {
		t.Fatalf("解析: %v", err)
	}

	if a.wiz.Step != wizStepPath {
		t.Fatalf("step = %v，解析失败应停在路径步", a.wiz.Step)
	}
	if a.wiz.Err == "" {
		t.Fatal("没有错误信息")
	}
	if !tt.HasText("解析失败：") {
		t.Errorf("界面没有显示解析失败: %v", tt.Texts())
	}
	if len(a.images[0].Anns) != 0 {
		t.Error("解析失败不该写标注")
	}
	// Back still works and the wizard can be cancelled.
	if err := tt.Click("上一步"); err != nil {
		t.Fatalf("上一步: %v", err)
	}
	if a.wiz.Step != wizStepFormat {
		t.Fatalf("step = %v，期望回到格式步", a.wiz.Step)
	}
	if a.wiz.Err != "" {
		t.Errorf("上一步应清掉错误: %q", a.wiz.Err)
	}
	if err := tt.Click("取消"); err != nil {
		t.Fatalf("取消: %v", err)
	}
	if a.wiz.Open {
		t.Error("取消后对话框应关闭")
	}
	if a.wiz.DS != nil || len(a.wiz.Choices) != 0 {
		t.Errorf("取消应清空状态: %+v", a.wiz)
	}
}

// TestImportWizardCancelChangesNothing: walking to the report and cancelling
// leaves the project untouched — no categories, no annotations, no undo step.
func TestImportWizardCancelChangesNothing(t *testing.T) {
	a := storeApp(t, "a.png")
	wizardIRFixture(t, a.dir)
	before := append([]Category(nil), a.categories...)
	tt := wizardFor(t, a)

	for _, step := range []string{"导入标注", "IR 项目", "下一步", "下一步", "下一步", "取消"} {
		if step == "下一步" && a.wiz.Step == wizStepPath {
			a.wiz.Path = a.dir
		}
		if err := tt.Click(step); err != nil {
			t.Fatalf("点击 %q: %v", step, err)
		}
	}
	if a.wiz.Open {
		t.Fatal("取消后应关闭")
	}
	if len(a.images[0].Anns) != 0 {
		t.Errorf("取消后写了标注: %+v", a.images[0].Anns)
	}
	if len(a.categories) != len(before) {
		t.Errorf("取消后类别变了: %+v", a.categories)
	}
	if len(a.undoStack) != 0 {
		t.Errorf("取消后压了 undo 命令: %+v", a.undoStack)
	}
}

// TestImportModalNoWindowBrowse: without a window the browse button is a no-op
// instead of a panic, so the path field is the only way in (which tests use).
func TestImportModalNoWindowBrowse(t *testing.T) {
	a := storeApp(t, "a.png")
	a.win = nil
	a.openImportWizard()
	before := a.wiz.Path
	a.browseImportPath()
	if a.wiz.Path != before {
		t.Errorf("无窗口时浏览改变了路径: %q → %q", before, a.wiz.Path)
	}
}

// TestWriteIRProjectFixtureIsReadable guards the test fixture itself: the wizard
// parses exactly what ir.WriteProject wrote.
func TestWizardIRFixtureParses(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	wizardIRFixture(t, dir)
	ds, issues, err := readIRDataset(dir)
	if err != nil {
		t.Fatalf("readIRDataset: %v", err)
	}
	if len(ds.Images) != 1 || len(ds.Images[0].Annotations) != 3 {
		t.Fatalf("dataset = %+v", ds)
	}
	if len(issues) != 1 {
		t.Fatalf("issues = %+v，期望 1 条", issues)
	}
	if issues[0].File != "a.png" || issues[0].Index != 2 || issues[0].Field != "rings" {
		t.Errorf("issue = %+v，期望定位到 a.png 第 3 条 rings", issues[0])
	}
}

// TestParseYOLOWithoutImagesAutoLoads guards the UX footgun of running the
// wizard with no folder open: the source path is opened as the workspace first
// (contract §2), and when that holds no images the path step stops with an
// actionable message instead of parsing against nothing.
func TestParseYOLOWithoutImagesAutoLoads(t *testing.T) {
	empty := t.TempDir()
	a := New()
	t.Cleanup(a.Close)
	a.wiz = wizardState{Open: true, Step: wizStepPath, Format: formatYOLO, Path: empty}

	a.wizardNext()

	if a.wiz.Step != wizStepPath {
		t.Fatalf("step = %v，期望停在路径步", a.wiz.Step)
	}
	if !strings.Contains(a.wiz.Err, "没有找到图片") {
		t.Fatalf("err = %v，期望“没有找到图片”的提示", a.wiz.Err)
	}
	if a.wiz.DS != nil {
		t.Errorf("失败时不该有解析结果: %+v", a.wiz.DS)
	}
}
