package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// The auto-load tests drive the wizard without a window: openImportWizard,
// then step onto the path step, write the path and click 下一步 by calling
// wizardNext — exactly what the dialog's button does (contract §2).

// yoloAutoFixture writes an Ultralytics dataset with two splits: train/a.png
// and val/b.png, one label each, and a data.yaml naming the two classes.
func yoloAutoFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mkdirs(t, root, "images/train", "images/val", "labels/train", "labels/val")
	writePNG(t, filepath.Join(root, "images", "train", "a.png"), 100, 50)
	writePNG(t, filepath.Join(root, "images", "val", "b.png"), 40, 30)
	writeText(t, filepath.Join(root, "labels", "train", "a.txt"), "0 0.5 0.5 0.4 0.2\n")
	writeText(t, filepath.Join(root, "labels", "val", "b.txt"), "1 0.5 0.5 0.5 0.5\n")
	writeText(t, filepath.Join(root, "data.yaml"), "path: .\nnc: 2\nnames: ['cat', 'dog']\n")
	return root
}

// vocAutoFixture writes a VOC directory holding one image and its XML.
func vocAutoFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "0001.png"), 100, 50)
	writeText(t, filepath.Join(dir, "0001.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<annotation>
  <filename>0001.png</filename>
  <size><width>100</width><height>50</height></size>
  <object>
    <name>dog</name>
    <bndbox><xmin>10</xmin><ymin>5</ymin><xmax>40</xmax><ymax>20</ymax></bndbox>
  </object>
</annotation>
`)
	return dir
}

// cocoAutoFixture writes a dataset root whose images sit next to an
// annotations/ directory, the COCO export layout: root/a.png +
// root/annotations/instances.json.
func cocoAutoFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writePNG(t, filepath.Join(root, "a.png"), 100, 50)
	mkdirs(t, root, "annotations")
	writeText(t, filepath.Join(root, "annotations", "instances.json"), `{
  "images": [{"id": 1, "file_name": "a.png", "width": 100, "height": 50}],
  "annotations": [{"id": 100, "image_id": 1, "category_id": 1, "bbox": [10, 5, 30, 15]}],
  "categories": [{"id": 1, "name": "person"}]
}`)
	return root
}

// wizardAtPath puts a fresh wizard on its path step for the given format and
// path, the state the user reaches by choosing a format and walking forward.
func wizardAtPath(a *App, f importFormat, path string) {
	a.openImportWizard()
	a.wiz.Step = wizStepPath
	a.wiz.Format = f
	a.wiz.Path = path
}

// TestImportAutoLoadYOLORoot: with no workspace open, choosing the dataset root
// loads its images and the parse matches every split, with no issues.
func TestImportAutoLoadYOLORoot(t *testing.T) {
	root := yoloAutoFixture(t)
	a := New()
	t.Cleanup(a.Close)

	wizardAtPath(a, formatYOLO, root)
	a.wizardNext()

	if a.dir != root {
		t.Fatalf("a.dir = %q，期望数据集根 %q：%s", a.dir, root, a.lastStatus)
	}
	if len(a.images) != 2 {
		t.Fatalf("载入 %d 张图片，期望 2：%s", len(a.images), a.lastStatus)
	}
	if a.wiz.Step != wizStepMapping {
		t.Fatalf("step = %v（err %q），期望映射步", a.wiz.Step, a.wiz.Err)
	}
	if len(a.wiz.Issues) != 0 {
		t.Fatalf("解析错误 = %v，期望没有", a.wiz.Issues)
	}
	if got := totalAnnots(a.wiz.DS); got != 2 {
		t.Fatalf("解析出 %d 条标注，期望 2", got)
	}
	if len(a.wiz.Choices) != 2 {
		t.Errorf("类别映射 = %d 行，期望 2: %+v", len(a.wiz.Choices), a.wiz.Choices)
	}

	a.wizardApply()
	per := map[string]int{}
	for _, img := range a.images {
		per[img.RelPath] = len(img.Anns)
	}
	if per["images/train/a.png"] != 1 || per["images/val/b.png"] != 1 {
		t.Errorf("导入后标注分布 = %v，期望 train 与 val 各 1 条", per)
	}
}

// TestImportAutoLoadFromLabelsDir: choosing the labels directory opens its
// parent as the workspace and still matches every split.
func TestImportAutoLoadFromLabelsDir(t *testing.T) {
	root := yoloAutoFixture(t)
	a := New()
	t.Cleanup(a.Close)

	wizardAtPath(a, formatYOLO, filepath.Join(root, "labels"))
	a.wizardNext()

	if a.dir != root {
		t.Fatalf("a.dir = %q，期望 %q：%s", a.dir, root, a.lastStatus)
	}
	if len(a.images) != 2 {
		t.Fatalf("载入 %d 张图片，期望 2：%s", len(a.images), a.lastStatus)
	}
	if a.wiz.Step != wizStepMapping {
		t.Fatalf("step = %v（err %q），期望映射步", a.wiz.Step, a.wiz.Err)
	}
	if got := totalAnnots(a.wiz.DS); got != 2 {
		t.Errorf("解析出 %d 条标注，期望 2（err %q）", got, a.wiz.Err)
	}
}

// TestImportAutoLoadFromTrainSplit is the reported user story: the folder the
// user thinks of first is coco8/images/train, and train plus val must land in
// one workspace.
func TestImportAutoLoadFromTrainSplit(t *testing.T) {
	root := yoloAutoFixture(t)
	a := New()
	t.Cleanup(a.Close)

	wizardAtPath(a, formatYOLO, filepath.Join(root, "images", "train"))
	a.wizardNext()

	if a.dir != root {
		t.Fatalf("a.dir = %q，期望数据集根 %q（train/val 同一工作区）：%s", a.dir, root, a.lastStatus)
	}
	if len(a.images) != 2 {
		t.Fatalf("载入 %d 张图片，期望 train+val 共 2：%s", len(a.images), a.lastStatus)
	}
	if a.wiz.Step != wizStepMapping {
		t.Fatalf("step = %v（err %q），期望映射步", a.wiz.Step, a.wiz.Err)
	}
	if got := totalAnnots(a.wiz.DS); got != 2 {
		t.Errorf("解析出 %d 条标注，期望 2（两个 split 都被解析）", got)
	}
}

// TestImportAutoLoadVOC: the chosen XML directory becomes the workspace.
func TestImportAutoLoadVOC(t *testing.T) {
	dir := vocAutoFixture(t)
	a := New()
	t.Cleanup(a.Close)

	wizardAtPath(a, formatVOC, dir)
	a.wizardNext()

	if a.dir != dir {
		t.Fatalf("a.dir = %q，期望 %q：%s", a.dir, dir, a.lastStatus)
	}
	if a.wiz.Step != wizStepMapping {
		t.Fatalf("step = %v（err %q），期望映射步", a.wiz.Step, a.wiz.Err)
	}
	if len(a.wiz.Issues) != 0 {
		t.Errorf("解析错误 = %v，期望没有", a.wiz.Issues)
	}
	a.wizardApply()
	if got := len(a.images[0].Anns); got != 1 {
		t.Errorf("写入 %d 条标注，期望 1", got)
	}
}

// TestImportAutoLoadCOCO: the JSON's annotations/ sibling is skipped, so the
// dataset root (the JSON's parent's parent) becomes the workspace.
func TestImportAutoLoadCOCO(t *testing.T) {
	root := cocoAutoFixture(t)
	a := New()
	t.Cleanup(a.Close)

	wizardAtPath(a, formatCOCO, filepath.Join(root, "annotations", "instances.json"))
	a.wizardNext()

	if a.dir != root {
		t.Fatalf("a.dir = %q，期望数据集根 %q：%s", a.dir, root, a.lastStatus)
	}
	if len(a.images) != 1 {
		t.Fatalf("载入 %d 张图片，期望 1：%s", len(a.images), a.lastStatus)
	}
	if a.wiz.Step != wizStepMapping {
		t.Fatalf("step = %v（err %q），期望映射步", a.wiz.Step, a.wiz.Err)
	}
	a.wizardApply()
	if got := len(a.images[0].Anns); got != 1 {
		t.Errorf("写入 %d 条标注，期望 1：%+v", got, a.images[0].Anns)
	}
}

// TestImportAutoLoadNoImagesStaysOnPathStep: a source folder without images
// loads nothing, so the wizard stays on the path step with an actionable
// message instead of parsing against an empty workspace (contract §2).
func TestImportAutoLoadNoImagesStaysOnPathStep(t *testing.T) {
	empty := t.TempDir()
	a := New()
	t.Cleanup(a.Close)
	a.openImportWizard()
	a.wiz.Step = wizStepPath
	a.wiz.Format = formatYOLO
	a.wiz.Path = empty

	a.wizardNext()

	if a.wiz.Step != wizStepPath {
		t.Fatalf("step = %v，期望停在路径步", a.wiz.Step)
	}
	if !strings.Contains(a.wiz.Err, "没有找到图片") || !strings.Contains(a.wiz.Err, empty) {
		t.Errorf("err = %q，期望提到 %q 下没有图片", a.wiz.Err, empty)
	}
	if a.wiz.DS != nil {
		t.Errorf("失败时不该有解析结果: %+v", a.wiz.DS)
	}
}

// TestImportAutoLoadKeepsExistingWorkspace: with a workspace already open the
// wizard matches against it and never replaces it (contract §3).
func TestImportAutoLoadKeepsExistingWorkspace(t *testing.T) {
	a := storeApp(t, "a.png")
	open := a.dir
	root := yoloAutoFixture(t)

	wizardAtPath(a, formatYOLO, root)
	a.wizardNext()

	if a.dir != open {
		t.Fatalf("a.dir = %q，期望保持 %q（不应自动换目录）", a.dir, open)
	}
	if len(a.images) != 1 || a.images[0].RelPath != "a.png" {
		t.Fatalf("工作区图片被替换: %+v", a.images)
	}
}

// TestImportAutoLoadIRDoesNotDoubleImport: opening an IR project root imports
// the project through openDir's direct path, so the wizard closes instead of
// applying the same annotations a second time (contract §6).
func TestImportAutoLoadIRDoesNotDoubleImport(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "a.png"), 100, 50)
	wizardIRFixture(t, dir)
	a := New()
	t.Cleanup(a.Close)

	wizardAtPath(a, formatIR, dir)
	a.wizardNext()

	if a.wiz.Open {
		t.Fatalf("IR 项目已在打开目录时导入，向导应关闭：step=%v err=%q", a.wiz.Step, a.wiz.Err)
	}
	if a.wiz.Err != "" {
		t.Errorf("err = %q，期望不报错", a.wiz.Err)
	}
	if a.dir != dir {
		t.Fatalf("a.dir = %q，期望 %q：%s", a.dir, dir, a.lastStatus)
	}
	if len(a.images) != 1 {
		t.Fatalf("载入 %d 张图片，期望 1：%s", len(a.images), a.lastStatus)
	}
	anns := a.images[0].Anns
	if len(anns) != 2 {
		t.Fatalf("标注 = %+v，期望 openir 导入的 2 条（1 条非法多边形被跳过）", anns)
	}
	ids := map[string]bool{}
	for _, an := range anns {
		if ids[an.ID] {
			t.Errorf("标注 ID 重复 %q：%+v", an.ID, anns)
		}
		ids[an.ID] = true
	}
	// One import step from openir, none from the wizard.
	if len(a.undoStack) != 1 {
		t.Errorf("undo 栈 = %d 条，期望 1（只有 openir 的那次导入）", len(a.undoStack))
	}
	if !strings.Contains(a.lastStatus, "已导入 IR 项目") {
		t.Errorf("lastStatus = %q，期望由 openDir 的摘要负责", a.lastStatus)
	}
}

// TestImportWizardWithoutWorkspaceUI: the toolbar opens the wizard with no
// folder open, and the path step explains that images will be loaded from the
// dataset directory.
func TestImportWizardWithoutWorkspaceUI(t *testing.T) {
	a := New()
	t.Cleanup(a.Close)
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			a.toolbar(c)
			a.importModal(c)
			a.statusBar(c)
		})
	}, testWinW, testWinH)
	tt.Advance(600 * time.Millisecond)

	if err := tt.Click("导入标注"); err != nil {
		t.Fatalf("点击导入标注: %v", err)
	}
	if !a.wiz.Open || a.wiz.Step != wizStepFormat {
		t.Fatalf("wizard = %+v，期望无工作区也能打开在第 1 步", a.wiz)
	}
	if err := tt.Click("下一步"); err != nil {
		t.Fatalf("进入路径步: %v", err)
	}
	if !tt.HasText("未打开图片文件夹：将自动从数据集目录载入图片") {
		t.Errorf("路径步缺少自动载入提示: %v", tt.Texts())
	}
	if err := tt.Click("取消"); err != nil {
		t.Fatalf("取消: %v", err)
	}
	if a.wiz.Open {
		t.Error("取消后应关闭")
	}
}

// writeText writes a small text fixture, creating its directory.
func writeText(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
