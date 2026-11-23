package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"mylabel/internal/exporter"
)

// The export wizard tests drive the dialog the way a user does: click a page's
// controls, then 下一步. The platform dialog is out of reach without a window, so
// the folder fields are written straight into the wizard's state, which is what
// browsing would do (docs/phase4-design.md §9).
var (
	expAppRef *App
	expTester *ui.Tester
)

func expView(c *ui.Context) {
	a := expAppRef
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Children(func() {
			if ui.Button(c.Key("expOpen"), "导出数据集").Clicked() {
				a.openExportWizard()
			}
		})
		a.exportModal(c)
		a.statusBar(c)
	})
	a.flushToast(c)
}

// exportFor shows a in the shared export Tester (mygo issue #1902: one Tester per
// package, swap the App behind it).
func exportFor(t *testing.T, a *App) *ui.Tester {
	t.Helper()
	expAppRef = a
	if expTester == nil {
		expTester = ui.NewTester(expView, testWinW, testWinH)
	} else {
		expTester.Frame()
	}
	return expTester
}

// clickText clicks the element with the given text and fails when it is absent,
// listing what did render.
func clickText(t *testing.T, tt *ui.Tester, text string) {
	t.Helper()
	if err := tt.Click(text); err != nil {
		t.Fatalf("点击 %q: %v（texts=%v）", text, err, tt.Texts())
	}
}

// setAnnsOn puts anns on the image with the given relative path without saving:
// the export reads the session, so a test can prove it needs no save.
func setAnnsOn(t *testing.T, a *App, rel string, anns []Ann) {
	t.Helper()
	for i := range a.images {
		if a.images[i].RelPath == rel {
			a.images[i].Anns = anns
			return
		}
	}
	t.Fatalf("没有 %q 这张图（共 %d 张）", rel, len(a.images))
}

// mustExist asserts that path exists.
func mustExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("缺少 %s: %v", path, err)
	}
}

// TestExportWizardYOLOEndToEnd clicks through all four pages of a YOLO export
// and checks the folder it wrote: data.yaml, mirrored images and labels per
// split, the manifest, and the class ids of the in-memory annotations (§9).
func TestExportWizardYOLOEndToEnd(t *testing.T) {
	a := storeApp(t, "train/a.png", "val/b.png")
	setAnnsOn(t, a, "train/a.png", []Ann{{ID: "a1", Cat: 0, Kind: KindRect, CX: 50, CY: 25, W: 20, H: 10}})
	setAnnsOn(t, a, "val/b.png", []Ann{{ID: "b1", Cat: 1, Kind: KindRect, CX: 30, CY: 20, W: 10, H: 20, Angle: 0.4}})
	tt := exportFor(t, a)

	clickText(t, tt, "导出数据集")
	s := &a.ex
	if !s.Open || s.Step != expStepFormat {
		t.Fatalf("wizard = %+v，期望打开在第 1 步", *s)
	}
	if s.Format != exporter.FormatYOLO || s.Task != exporter.TaskObb {
		t.Fatalf("默认 %v/%v，期望 yolo/obb（会话里有旋转框）", s.Format, s.Task)
	}
	if !tt.HasText("选择目标格式") || !tt.HasText("obb 旋转框") {
		t.Fatalf("第 1 步没有渲染格式与任务: %v", tt.Texts())
	}

	clickText(t, tt, "COCO")
	if s.Format != exporter.FormatCOCO {
		t.Fatalf("Format = %v，期望 COCO", s.Format)
	}
	if tt.HasText("obb 旋转框") {
		t.Error("COCO 不该显示 YOLO 任务选项")
	}
	clickText(t, tt, "YOLO")

	clickText(t, tt, "下一步")
	if s.Step != expStepSplit {
		t.Fatalf("step = %v，期望划分步", s.Step)
	}
	if s.SplitMode != exporter.SplitPreserve || s.Ratio != defaultExportRatio || s.Seed != defaultExportSeed {
		t.Fatalf("划分默认 = %v/%v/%q，期望 preserve/0.8/42", s.SplitMode, s.Ratio, s.Seed)
	}
	clickText(t, tt, "按比例随机划分（同种子可复现）")
	if s.SplitMode != exporter.SplitRandom {
		t.Fatalf("SplitMode = %v，期望 random", s.SplitMode)
	}
	s.Ratio, s.Seed = 0.5, "7"
	a.dirty = true // the report says an unsaved session exports what is on screen

	clickText(t, tt, "下一步")
	if s.Step != expStepReport {
		t.Fatalf("step = %v（err %q），期望报告步", s.Step, s.Err)
	}
	if s.Err != "" || s.Plan == nil {
		t.Fatalf("试运行失败: err=%q", s.Err)
	}
	if rep := s.Plan.Report; rep.Images != 2 || rep.Annotations != 2 || rep.AnnotationsDropped != 0 {
		t.Errorf("报告 = %+v，期望 2 图 2 标注 0 丢弃", rep)
	}
	if !tt.HasText("试运行报告：2 张图片（2 张有标注）/ 2 条标注，丢弃 0 条") {
		t.Errorf("报告页没有渲染统计: %v", tt.Texts())
	}
	if train, val := splitCounts(s.Plan.Splits); train != 1 || val != 1 {
		t.Errorf("划分 = train %d / val %d，期望各 1", train, val)
	}
	if !tt.HasText("当前有未保存的修改：导出按屏幕上的内容进行，不要求先保存") {
		t.Errorf("报告页没有说明导出按屏幕内容进行: %v", tt.Texts())
	}

	clickText(t, tt, "下一步")
	if s.Step != expStepOutput {
		t.Fatalf("step = %v，期望输出步", s.Step)
	}
	parent := t.TempDir()
	s.Parent = parent
	tt.Frame()
	if s.DirErr != "" {
		t.Fatalf("目标被拒: %s", s.DirErr)
	}

	clickText(t, tt, "开始导出")
	outDir := filepath.Join(parent, defaultExportDirName)
	if !s.Done || s.OutPath != outDir {
		t.Fatalf("导出后 = done %v path %q（err %q），期望 %q", s.Done, s.OutPath, s.DirErr, outDir)
	}
	if want := "已导出数据集到 " + outDir; a.lastStatus != want {
		t.Errorf("lastStatus = %q，期望 %q", a.lastStatus, want)
	}

	mustExist(t, filepath.Join(outDir, "data.yaml"))
	mustExist(t, filepath.Join(outDir, "mylabel-export.json"))
	splits := s.Plan.Splits
	if len(splits) != 2 {
		t.Fatalf("划分表 = %v，期望 2 项", splits)
	}
	for _, img := range a.images {
		split := splits[img.RelPath]
		if split == "" {
			t.Fatalf("%q 没有 split", img.RelPath)
		}
		base := filepath.Base(img.RelPath)
		mustExist(t, filepath.Join(outDir, "images", split, base))
		label := strings.TrimSuffix(base, filepath.Ext(base)) + ".txt"
		mustExist(t, filepath.Join(outDir, "labels", split, label))
		data, err := os.ReadFile(filepath.Join(outDir, "labels", split, label))
		if err != nil {
			t.Fatalf("读 %s: %v", label, err)
		}
		// The annotation's category index is the class id of the written line.
		wantPrefix := "0 "
		if img.Anns[0].Cat == 1 {
			wantPrefix = "1 "
		}
		if !strings.HasPrefix(string(data), wantPrefix) {
			t.Errorf("%s 内容 = %q，期望以 %q 开头", label, data, wantPrefix)
		}
	}
	manifest, err := os.ReadFile(filepath.Join(outDir, "mylabel-export.json"))
	if err != nil {
		t.Fatalf("读 manifest: %v", err)
	}
	if !strings.Contains(string(manifest), "yolo") || !strings.Contains(string(manifest), `"train_ratio"`) {
		t.Errorf("manifest 没记录格式与划分: %s", manifest)
	}
}

// TestExportWizardVOCOptions: the format's own switches default to the contract
// and reach exporter.Options unchanged (§2, §9).
func TestExportWizardVOCOptions(t *testing.T) {
	a := storeApp(t, "a.png")
	tt := exportFor(t, a)
	clickText(t, tt, "导出数据集")

	clickText(t, tt, "VOC")
	s := &a.ex
	if s.Format != exporter.FormatVOC {
		t.Fatalf("Format = %v，期望 VOC", s.Format)
	}
	if !s.VOCOneBased || s.VOCDowngrade {
		t.Fatalf("VOC 默认 = oneBased %v downgrade %v，期望 true/false", s.VOCOneBased, s.VOCDowngrade)
	}
	clickText(t, tt, "bndbox 写 1-based 坐标（VOC 官方惯例）")
	clickText(t, tt, "旋转框降级为外接 bndbox（有损，丢方向）")
	opts, err := s.options()
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	if opts.Format != exporter.FormatVOC || opts.VOCOneBased || !opts.VOCDowngradeOBB {
		t.Errorf("options = %+v，期望 VOC / 0-based / 降级 obb", opts)
	}
	if opts.Task != "" {
		t.Errorf("VOC 不该带 YOLO 任务，得到 %q", opts.Task)
	}
	if opts.Split.Mode != exporter.SplitPreserve {
		t.Errorf("划分 = %v，期望 preserve", opts.Split.Mode)
	}
}

// TestExportWizardRejectsBadSeed: a seed that is not a number keeps the wizard
// on the split page with the reason, instead of failing in the exporter.
func TestExportWizardRejectsBadSeed(t *testing.T) {
	a := storeApp(t, "a.png")
	tt := exportFor(t, a)
	clickText(t, tt, "导出数据集")
	clickText(t, tt, "下一步")
	clickText(t, tt, "按比例随机划分（同种子可复现）")
	a.ex.Seed = "abc"

	clickText(t, tt, "下一步")
	if a.ex.Step != expStepSplit {
		t.Fatalf("step = %v，期望留在划分步", a.ex.Step)
	}
	if !strings.Contains(a.ex.Err, "随机种子") {
		t.Errorf("Err = %q，期望提到随机种子", a.ex.Err)
	}
	if !tt.HasText("设置无效：随机种子必须是整数：\"abc\"") {
		t.Errorf("划分步没有渲染原因: %v", tt.Texts())
	}
}

// TestExportWizardPlanError: when BuildPlan itself refuses the request the report
// page shows the whole error and offers no 下一步. Two folders holding the same
// basename collide in VOC's flat layout (§7).
func TestExportWizardPlanError(t *testing.T) {
	a := storeApp(t, "x/a.png", "y/a.png")
	tt := exportFor(t, a)
	clickText(t, tt, "导出数据集")
	clickText(t, tt, "VOC")
	clickText(t, tt, "下一步")
	clickText(t, tt, "下一步")

	s := &a.ex
	if s.Step != expStepReport {
		t.Fatalf("step = %v，期望报告步", s.Step)
	}
	if s.Plan != nil {
		t.Fatalf("不该有计划: %+v", s.Plan)
	}
	if !strings.Contains(s.Err, "冲突") {
		t.Fatalf("Err = %q，期望名字冲突", s.Err)
	}
	if !tt.HasText("无法导出") {
		t.Errorf("报告页没有渲染错误标题: %v", tt.Texts())
	}
	if err := tt.Click("下一步"); err == nil {
		t.Error("计划失败时不该再给出下一步")
	}
}

// TestExportWizardNoCategoriesDropsEverything: with no categories every
// annotation names a category the export does not know, so the plan succeeds
// with everything dropped and says so (§9 的提示).
func TestExportWizardNoCategoriesDropsEverything(t *testing.T) {
	a := storeApp(t, "a.png")
	a.categories = nil
	a.curCat = -1
	setAnnsOn(t, a, "a.png", []Ann{{ID: "a1", Cat: 0, Kind: KindRect, CX: 50, CY: 25, W: 20, H: 10}})
	tt := exportFor(t, a)
	clickText(t, tt, "导出数据集")
	clickText(t, tt, "下一步")
	clickText(t, tt, "下一步")

	s := &a.ex
	if s.Err != "" || s.Plan == nil {
		t.Fatalf("空类别的计划不该失败: err=%q", s.Err)
	}
	rep := s.Plan.Report
	if rep.Annotations != 0 || rep.AnnotationsDropped != 1 {
		t.Errorf("报告 = %+v，期望 0 写出 1 丢弃", rep)
	}
	if !tt.HasText("当前工作区没有类别：所有标注都会被丢弃") {
		t.Errorf("报告页没有提示类别缺失: %v", tt.Texts())
	}
	if !tt.HasText("无法转换或未知类别（已跳过） ×1") {
		t.Errorf("报告页没有列出丢弃规则: %v", tt.Texts())
	}
}

// TestExportWizardNoWorkspace: without an open folder the toolbar reports it
// instead of opening a dialog with nothing to export.
func TestExportWizardNoWorkspace(t *testing.T) {
	a := New()
	tt := exportFor(t, a)
	clickText(t, tt, "导出数据集")
	if a.ex.Open {
		t.Error("无工作区不该打开向导")
	}
	if !strings.Contains(a.lastStatus, "请先打开图片文件夹") {
		t.Errorf("lastStatus = %q", a.lastStatus)
	}
	if !tt.HasText("请先打开图片文件夹再导出数据集") {
		t.Errorf("状态栏没有渲染提示: %v", tt.Texts())
	}
}

// TestExportWizardTargetValidation: the output page refuses an existing folder
// and a name that is not a single path element before anything is written.
func TestExportWizardTargetValidation(t *testing.T) {
	a := storeApp(t, "a.png")
	tt := exportFor(t, a)
	clickText(t, tt, "导出数据集")
	clickText(t, tt, "下一步")
	clickText(t, tt, "下一步")
	clickText(t, tt, "下一步")

	s := &a.ex
	if s.Step != expStepOutput {
		t.Fatalf("step = %v，期望输出步", s.Step)
	}
	parent := t.TempDir()
	s.Parent = parent
	taken := filepath.Join(parent, "taken")
	if err := os.Mkdir(taken, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	s.DirName = "taken"
	tt.Frame()
	if !strings.Contains(s.DirErr, "已存在") {
		t.Fatalf("DirErr = %q，期望目录已存在", s.DirErr)
	}
	if !tt.HasText("目录已存在，导出不会覆盖：" + taken) {
		t.Errorf("页面没有渲染校验结果: %v", tt.Texts())
	}
	// The disabled button cannot start the run (the guard in exportStart refuses
	// too), so nothing was written and the folder stayed empty.
	_ = tt.Click("开始导出")
	if s.Done || s.Busy {
		t.Fatalf("已存在的目录不该开始导出: %+v", *s)
	}
	entries, err := os.ReadDir(taken)
	if err != nil || len(entries) != 0 {
		t.Errorf("目标目录被写入: %v / %d 项", err, len(entries))
	}

	for _, name := range []string{"a/b", "..", "  "} {
		s.DirName = name
		tt.Frame()
		if s.DirErr == "" {
			t.Errorf("目录名 %q 应被拒绝", name)
		}
	}
}

// TestExportWizardWriteFailure: a failure inside WriteExport is reported on the
// output page and as a toast, leaves no half-built folder, and keeps the dialog
// usable. Removing an image after the plan was built makes the copy fail.
func TestExportWizardWriteFailure(t *testing.T) {
	a := storeApp(t, "a.png")
	tt := exportFor(t, a)
	clickText(t, tt, "导出数据集")
	clickText(t, tt, "下一步")
	clickText(t, tt, "下一步")
	clickText(t, tt, "下一步")

	s := &a.ex
	parent := t.TempDir()
	s.Parent = parent
	tt.Frame()
	missing := filepath.Join(a.dir, "a.png")
	if err := os.Remove(missing); err != nil {
		t.Fatalf("删除图片: %v", err)
	}

	clickText(t, tt, "开始导出")
	if s.Done || s.Busy {
		t.Fatalf("失败的导出 = done %v busy %v", s.Done, s.Busy)
	}
	if !strings.Contains(s.RunErr, "导出失败") {
		t.Fatalf("RunErr = %q，期望导出失败", s.RunErr)
	}
	if s.DirErr != "" {
		t.Errorf("DirErr = %q，目标目录本身没有问题", s.DirErr)
	}
	if !tt.HasText(s.RunErr) {
		t.Errorf("输出步没有渲染失败原因: %v", tt.Texts())
	}
	if a.toast != "" {
		t.Errorf("失败消息没有作为 toast 交给主线程: %q", a.toast)
	}
	outDir := filepath.Join(parent, defaultExportDirName)
	if _, err := os.Stat(outDir); !os.IsNotExist(err) {
		t.Errorf("失败后不该留下 %s: %v", outDir, err)
	}
	if _, err := os.Stat(outDir + ".mylabel-tmp"); !os.IsNotExist(err) {
		t.Errorf("失败后不该留下临时目录: %v", err)
	}
	// The dialog stays usable: swapping in a fresh plan lets a retry start.
	if s.Plan == nil {
		t.Error("失败不该丢掉计划")
	}
}

// TestExportWizardSplitCarriesReportSamples: the dry-run page lists each lossy
// rule with its count and up to three example files, not five (§9).
func TestExportWizardSplitCarriesReportSamples(t *testing.T) {
	a := storeApp(t, "a.png", "b.png")
	// det 任务下多边形降级为外接框：5 条同规则示例，页面只列 3 条。
	for _, rel := range []string{"a.png", "b.png"} {
		setAnnsOn(t, a, rel, []Ann{
			{ID: "p1", Cat: 0, Kind: KindRect, CX: 20, CY: 20, W: 10, H: 10, Angle: 0.3},
			{ID: "p2", Cat: 0, Kind: KindRect, CX: 40, CY: 40, W: 10, H: 10, Angle: 0.9},
		})
	}
	tt := exportFor(t, a)
	clickText(t, tt, "导出数据集")
	clickText(t, tt, "det 检测框")
	clickText(t, tt, "下一步")
	clickText(t, tt, "下一步")

	s := &a.ex
	if s.Err != "" || s.Plan == nil {
		t.Fatalf("试运行失败: err=%q", s.Err)
	}
	var stat *exporter.LossyStat
	for i := range s.Plan.Report.Lossy {
		if s.Plan.Report.Lossy[i].Rule == exporter.RuleObbToDet {
			stat = &s.Plan.Report.Lossy[i]
		}
	}
	if stat == nil {
		t.Fatalf("没有 obb→det 规则: %+v", s.Plan.Report.Lossy)
	}
	if stat.Count != 4 {
		t.Errorf("规则计数 = %d，期望 4", stat.Count)
	}
	if !tt.HasText(exporter.RuleObbToDet + " ×4") {
		t.Errorf("报告页没有渲染规则计数: %v", tt.Texts())
	}
	samples := 0
	for _, line := range tt.Texts() {
		if strings.HasPrefix(line, "示例：") {
			samples++
		}
	}
	if samples != reportSamples {
		t.Errorf("示例行 = %d，期望 %d：%v", samples, reportSamples, tt.Texts())
	}
}
