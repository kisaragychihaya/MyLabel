package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// openExportWizardToStep drives the wizard to a page with the given number of
// 下一步 clicks, the way a user would (docs/phase5-design.md §5.2, §7.2).
func openExportWizardToStep(t *testing.T, tt *ui.Tester, next int) {
	t.Helper()
	clickText(t, tt, "导出数据集")
	for i := 0; i < next; i++ {
		clickText(t, tt, "下一步")
	}
}

// exportPlanWrites reports whether the plan writes a file under prefix.
func exportPlanWrites(p *exporter.Plan, prefix string) bool {
	for _, f := range p.Files {
		if strings.HasPrefix(f.Rel, prefix) {
			return true
		}
	}
	return false
}

// exportReportCount is how often the plan's report counted rule.
func exportReportCount(p *exporter.Plan, rule string) int {
	for _, stat := range p.Report.Lossy {
		if stat.Rule == rule {
			return stat.Count
		}
	}
	return 0
}

// clickRow clicks a row of the per-image dialog as a separate click: a list
// reports the second half of a double click as RowDoubleClicked, so two clicks in
// a row would be one click and one double click (§7.2).
func clickRow(t *testing.T, tt *ui.Tester, text string) {
	t.Helper()
	tt.Advance(time.Second)
	clickText(t, tt, text)
}

// TestExportCancelStopsBeforeAnyFile: a cancelled run reports itself as cancelled
// rather than failed, is not Done, drops its Cancel, and leaves no folder behind,
// so the same plan can run again (§5.2).
func TestExportCancelStopsBeforeAnyFile(t *testing.T) {
	a := storeApp(t, "a.png", "b.png")
	tt := exportFor(t, a)
	openExportWizardToStep(t, tt, 3)

	s := &a.ex
	if s.Step != expStepOutput {
		t.Fatalf("step = %v，期望输出步", s.Step)
	}
	parent := t.TempDir()
	s.Parent = parent
	tt.Frame()
	if s.DirErr != "" {
		t.Fatalf("目标被拒: %s", s.DirErr)
	}

	// A cancel that arrives before the first file boundary: what 取消导出 does to
	// a run that has not reached a check yet.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	outDir := filepath.Join(parent, defaultExportDirName)
	a.finishExport(outDir, exporter.WriteExport(ctx, outDir, a.dir, s.Plan, nil))

	if s.Busy {
		t.Error("取消后仍在忙")
	}
	if s.Done {
		t.Error("取消不该算作完成")
	}
	if s.Cancel != nil {
		t.Error("取消后 Cancel 没有清空")
	}
	if want := "已取消导出（未写出任何文件）"; s.RunErr != want {
		t.Errorf("RunErr = %q，期望 %q", s.RunErr, want)
	}
	if want := "导出已取消"; a.toast != want {
		t.Errorf("toast = %q，期望 %q", a.toast, want)
	}
	if _, err := os.Stat(outDir); !os.IsNotExist(err) {
		t.Errorf("取消后留下了 %s: %v", outDir, err)
	}
	if _, err := os.Stat(outDir + ".mylabel-tmp"); !os.IsNotExist(err) {
		t.Errorf("取消后留下了临时目录: %v", err)
	}

	a.toast = ""
	tt.Frame()
	if !tt.HasText("已取消导出（未写出任何文件）") {
		t.Errorf("输出页没有显示取消结果: %v", tt.Texts())
	}
	// Nothing is Done, so the same plan is still ready to run.
	if s.Plan == nil {
		t.Error("取消丢掉了计划")
	}
	if !strings.Contains(s.RunErr, "取消") {
		t.Errorf("RunErr = %q", s.RunErr)
	}
}

// TestExportCancelButton: a run in flight shows 取消导出 on the output page, one
// click asks the run to stop and disarms the button so it cannot ask twice
// (§5.2).
func TestExportCancelButton(t *testing.T) {
	a := storeApp(t, "a.png")
	tt := exportFor(t, a)
	openExportWizardToStep(t, tt, 3)

	s := &a.ex
	s.Parent = t.TempDir()
	// Without a window exportStart runs inline, so the frame where a run is in
	// flight is set up directly; everything below it is the real state machine.
	calls := 0
	s.Busy, s.Written, s.Total = true, 1, 3
	s.Cancel = func() { calls++ }
	tt.Frame()
	if !tt.HasText("取消导出") {
		t.Fatalf("忙时没有渲染取消按钮: %v", tt.Texts())
	}
	if !tt.HasText("正在写入 1/3") {
		t.Errorf("没有渲染进度: %v", tt.Texts())
	}
	if a.ex.Written != 1 || a.ex.Total != 3 {
		t.Errorf("进度 = %d/%d，期望 1/3", a.ex.Written, a.ex.Total)
	}

	clickText(t, tt, "取消导出")
	if calls != 1 {
		t.Fatalf("取消被调用了 %d 次，期望 1", calls)
	}
	if s.Cancel != nil {
		t.Error("取消后 Cancel 没有清空")
	}
	if want := "正在取消导出…"; a.lastStatus != want {
		t.Errorf("lastStatus = %q，期望 %q", a.lastStatus, want)
	}

	// The second click is refused: the button is disabled and Cancel is gone.
	a.lastStatus = "sentinel"
	if err := tt.Click("取消导出"); err == nil {
		if calls != 1 {
			t.Errorf("第二次点击又调了一次取消，共 %d 次", calls)
		}
		if a.lastStatus != "sentinel" {
			t.Errorf("disabled 的按钮仍然生效: lastStatus = %q", a.lastStatus)
		}
	}
}

// TestExportCancelOnClose: closing the wizard during a run cancels the write
// instead of leaving a background export nobody can see, and what is left of the
// state is dropped when the wizard opens again (§5.2).
func TestExportCancelOnClose(t *testing.T) {
	a := storeApp(t, "a.png")
	tt := exportFor(t, a)
	clickText(t, tt, "导出数据集")

	s := &a.ex
	calls := 0
	s.Busy, s.Parent = true, t.TempDir()
	s.Cancel = func() { calls++ }
	a.closeExportWizard()

	if calls != 1 {
		t.Fatalf("关闭时取消被调用了 %d 次，期望 1", calls)
	}
	if s.Open {
		t.Error("关闭后对话框还开着")
	}
	if s.Cancel != nil {
		t.Error("关闭并取消后 Cancel 没有清空")
	}
	if !s.Busy {
		t.Error("取消后到 finishExport 之前状态应当保留，好让结果落进同一个向导")
	}

	// The outcome of the cancelled run still lands.
	a.finishExport("/nowhere", context.Canceled)
	if s.Busy || s.Done {
		t.Errorf("取消后 = busy %v done %v，期望都 false", s.Busy, s.Done)
	}
	if want := "已取消导出（未写出任何文件）"; s.RunErr != want {
		t.Errorf("RunErr = %q，期望 %q", s.RunErr, want)
	}

	// Reopening starts from a clean slate: no run, no overrides, step 1.
	a.openExportWizard()
	if !a.ex.Open || a.ex.Step != expStepFormat {
		t.Errorf("重开 = open %v step %v", a.ex.Open, a.ex.Step)
	}
	if a.ex.Busy || a.ex.RunErr != "" || len(a.ex.Overrides) != 0 || a.ex.Cancel != nil {
		t.Errorf("重开后残留: %+v", a.ex)
	}

	// A close outside a run forgets everything instead of hiding it.
	a.closeExportWizard()
	if a.ex.Open || a.ex.Parent != "" || a.ex.Step != expStepFormat {
		t.Errorf("空闲关闭后残留: %+v", a.ex)
	}
}

// TestExportAdjustDialog: the report step opens the per-image dialog, a click
// flips one image between train and val while keeping Overrides minimal, the
// filter is a case-insensitive substring, 全部重置 clears the overrides, and
// closing rebuilds the plan so the report and step 4 agree (§7.2).
func TestExportAdjustDialog(t *testing.T) {
	a := storeApp(t, "train/a.png", "val/b.png")
	tt := exportFor(t, a)
	openExportWizardToStep(t, tt, 2)

	s := &a.ex
	if s.Step != expStepReport || s.Plan == nil || s.Err != "" {
		t.Fatalf("报告步 = step %v plan %v err %q", s.Step, s.Plan, s.Err)
	}
	if s.Plan.Splits["train/a.png"] != "train" || s.Plan.Splits["val/b.png"] != "val" {
		t.Fatalf("划分 = %v，期望 a=train b=val", s.Plan.Splits)
	}

	clickText(t, tt, "逐图调整…")
	if !s.Adjust {
		t.Fatalf("对话框没有打开（texts=%v）", tt.Texts())
	}
	if got := strings.Join(s.adjustFiles, ","); got != "train/a.png,val/b.png" {
		t.Fatalf("行 = %q，期望按文件名排序的两张图", got)
	}
	if !tt.HasText("逐图调整划分") || !tt.HasText("train/a.png") {
		t.Errorf("对话框没有渲染行: %v", tt.Texts())
	}

	// A click flips the row; the plan itself waits for the dialog to close.
	clickRow(t, tt, "train/a.png")
	if got := s.Overrides["train/a.png"]; got != "val" {
		t.Fatalf("Overrides = %v，期望 train/a.png→val", s.Overrides)
	}
	if s.Plan.Splits["train/a.png"] != "train" {
		t.Error("对话框还开着就重建了计划")
	}

	// A second click lands back on the automatic split, which drops the key.
	clickRow(t, tt, "train/a.png")
	if len(s.Overrides) != 0 {
		t.Fatalf("改回自动结果后应删键，得到 %v", s.Overrides)
	}

	// The filter matches a substring of the file, case-insensitively.
	s.AdjustFilter = "B.PNG"
	tt.Frame()
	if got := strings.Join(s.adjustFiles, ","); got != "val/b.png" {
		t.Errorf("筛选后行 = %q，期望只有 val/b.png", got)
	}
	s.AdjustFilter = "zzz"
	tt.Frame()
	if len(s.adjustFiles) != 0 {
		t.Errorf("无匹配时行 = %v", s.adjustFiles)
	}
	if !tt.HasText("没有匹配的图片") {
		t.Errorf("无匹配时没有提示: %v", tt.Texts())
	}
	s.AdjustFilter = ""
	tt.Frame()
	if len(s.adjustFiles) != 2 {
		t.Fatalf("清空筛选后行 = %v", s.adjustFiles)
	}

	clickRow(t, tt, "train/a.png")
	clickText(t, tt, "完成")
	if s.Adjust {
		t.Error("完成后对话框还开着")
	}
	if s.adjustBase != nil || s.adjustAuto != nil || s.adjustFiles != nil {
		t.Errorf("关闭后对话框状态没清: %+v", s)
	}
	if got := s.Plan.Splits["train/a.png"]; got != "val" {
		t.Fatalf("关闭后划分 = %v，期望 train/a.png 已被改成 val", s.Plan.Splits)
	}
	if train, val := splitCounts(s.Plan.Splits); train != 0 || val != 2 {
		t.Errorf("关闭后 = train %d / val %d，期望 0/2", train, val)
	}

	// Reopening shows the override, and clicking it again restores the automatic
	// split: a flip that agrees with the policy drops the key.
	clickText(t, tt, "逐图调整…")
	if got := s.splitOf("train/a.png"); got != "val" {
		t.Fatalf("重开后行显示 %q，期望 val", got)
	}
	clickRow(t, tt, "train/a.png")
	if len(s.Overrides) != 0 {
		t.Fatalf("改回自动值应删键，得到 %v", s.Overrides)
	}
	clickText(t, tt, "完成")
	if train, val := splitCounts(s.Plan.Splits); train != 1 || val != 1 {
		t.Errorf("恢复后 = train %d / val %d，期望 1/1", train, val)
	}

	// 全部重置 drops every override at once.
	clickText(t, tt, "逐图调整…")
	clickRow(t, tt, "train/a.png")
	clickRow(t, tt, "val/b.png")
	if len(s.Overrides) != 2 {
		t.Fatalf("两次点击后 Overrides = %v，期望 2 项", s.Overrides)
	}
	clickText(t, tt, "全部重置")
	if len(s.Overrides) != 0 {
		t.Fatalf("全部重置后 Overrides = %v", s.Overrides)
	}
	clickText(t, tt, "完成")
	if train, val := splitCounts(s.Plan.Splits); train != 1 || val != 1 {
		t.Errorf("重置后 = train %d / val %d，期望 1/1", train, val)
	}
}

// TestExportAdjustReachesStepFour: the plan the output step writes is the one the
// dialog produced, so an override changes where an image lands on disk (§7.2).
func TestExportAdjustReachesStepFour(t *testing.T) {
	a := storeApp(t, "train/a.png")
	tt := exportFor(t, a)
	openExportWizardToStep(t, tt, 2)

	s := &a.ex
	clickText(t, tt, "逐图调整…")
	clickRow(t, tt, "train/a.png")
	clickText(t, tt, "完成")
	if got := s.Plan.Splits["train/a.png"]; got != "val" {
		t.Fatalf("Splits = %v，期望 val", s.Plan.Splits)
	}

	clickText(t, tt, "下一步")
	parent := t.TempDir()
	s.Parent = parent
	tt.Frame()
	clickText(t, tt, "开始导出")
	if !s.Done {
		t.Fatalf("导出没完成: err %q", s.RunErr)
	}
	outDir := filepath.Join(parent, defaultExportDirName)
	mustExist(t, filepath.Join(outDir, "images", "val", "a.png"))
	if _, err := os.Stat(filepath.Join(outDir, "images", "train")); !os.IsNotExist(err) {
		t.Errorf("被改到 val 的图片仍写进了 train: %v", err)
	}
}

// TestExportVOCSegMasks: the VOC page's seg checkbox is on by default and reaches
// the exporter, so polygons keep their masks unless the user turns it off (§6).
func TestExportVOCSegMasks(t *testing.T) {
	a := storeApp(t, "a.png")
	setAnnsOn(t, a, "a.png", []Ann{{
		ID: "p1", Cat: 0, Kind: KindPoly,
		Points: []Point{{10, 10}, {60, 10}, {60, 40}, {10, 40}},
	}})
	tt := exportFor(t, a)
	clickText(t, tt, "导出数据集")

	s := &a.ex
	if !s.VOCSeg {
		t.Fatal("VOC seg 掩码默认应勾选")
	}
	clickText(t, tt, "VOC")
	const seg = "导出分割 PNG 掩码（SegmentationClass/Object）"
	if !tt.HasText(seg) {
		t.Fatalf("VOC 页没有渲染掩码选项: %v", tt.Texts())
	}
	opts, err := s.options()
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	if !opts.VOCSeg {
		t.Error("options 没接上 VOCSeg")
	}

	clickText(t, tt, seg)
	if s.VOCSeg {
		t.Error("点击后应取消勾选")
	}
	if opts, err = s.options(); err != nil || opts.VOCSeg {
		t.Errorf("取消勾选后 options = %+v / %v", opts, err)
	}
	clickText(t, tt, seg)
	if !s.VOCSeg {
		t.Fatal("再点一次应重新勾选")
	}

	clickText(t, tt, "下一步")
	clickText(t, tt, "下一步")
	if s.Err != "" || s.Plan == nil {
		t.Fatalf("试运行失败: err=%q", s.Err)
	}
	if !exportPlanWrites(s.Plan, "SegmentationClass/") || !exportPlanWrites(s.Plan, "SegmentationObject/") {
		t.Errorf("VOCSeg 开时没有写掩码: %v", s.Plan.Files)
	}
	if n := exportReportCount(s.Plan, exporter.RuleSegDropped); n != 0 {
		t.Errorf("%s ×%d，VOCSeg 开时不该丢弃 seg", exporter.RuleSegDropped, n)
	}

	// Turning it off returns to the old behaviour: polygons are skipped and counted.
	s.VOCSeg = false
	a.exportBuildPlan()
	if s.Plan == nil {
		t.Fatalf("关掉 VOCSeg 后试运行失败: %q", s.Err)
	}
	if exportPlanWrites(s.Plan, "SegmentationClass/") {
		t.Errorf("VOCSeg 关时仍写了掩码: %v", s.Plan.Files)
	}
	if n := exportReportCount(s.Plan, exporter.RuleSegDropped); n != 1 {
		t.Errorf("%s ×%d，期望 1", exporter.RuleSegDropped, n)
	}
}
