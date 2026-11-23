package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"mylabel/internal/exporter"
)

// The export wizard (docs/phase4-design.md §9): 格式 → split → dry-run 报告 →
// 输出目录 → 后台落盘. It follows the import wizard's shape — all state lives on
// App so a frame never owns it, every step is a plain function, and the dialog
// only reads that state — so the whole flow is drivable from a test without a
// window.

// defaultExportDirName is the folder the export creates under the chosen parent.
const defaultExportDirName = "mylabel-export"

// defaultExportRatio and defaultExportSeed seed the random split's controls.
const (
	defaultExportRatio = 0.8
	defaultExportSeed  = "42"
)

// exportStep is the wizard's current page.
type exportStep int

const (
	expStepFormat exportStep = iota // 1. format (+ task / VOC options)
	expStepSplit                    // 2. split policy
	expStepReport                   // 3. dry-run report
	expStepOutput                   // 4. output folder + run
)

// title is the step's name, shown in the header.
func (s exportStep) title() string {
	switch s {
	case expStepFormat:
		return "格式"
	case expStepSplit:
		return "划分"
	case expStepReport:
		return "试运行报告"
	case expStepOutput:
		return "输出目录"
	}
	return ""
}

// exportFormats is the wizard's format radio order.
var exportFormats = []exporter.Format{exporter.FormatYOLO, exporter.FormatCOCO, exporter.FormatVOC}

// exportTasks is the YOLO task radio order.
var exportTasks = []exporter.Task{exporter.TaskDet, exporter.TaskSeg, exporter.TaskObb}

// exportFormatLabel is the radio label of a format.
func exportFormatLabel(f exporter.Format) string {
	switch f {
	case exporter.FormatYOLO:
		return "YOLO"
	case exporter.FormatCOCO:
		return "COCO"
	case exporter.FormatVOC:
		return "VOC"
	}
	return string(f)
}

// exportTaskLabel is the radio label of a YOLO task.
func exportTaskLabel(t exporter.Task) string {
	switch t {
	case exporter.TaskDet:
		return "det 检测框"
	case exporter.TaskSeg:
		return "seg 分割"
	case exporter.TaskObb:
		return "obb 旋转框"
	}
	return string(t)
}

// exportState is the export wizard's whole state, owned by App.
type exportState struct {
	Open bool
	Step exportStep

	// step 1
	Format       exporter.Format
	Task         exporter.Task // YOLO only
	VOCOneBased  bool          // VOC only, default true
	VOCDowngrade bool          // VOC only, default false

	// step 2
	SplitMode exporter.SplitMode
	Ratio     float64 // SplitRandom only
	Seed      string  // SplitRandom only; parsed on use, so a half-typed number is not a crash

	// step 3
	Plan *exporter.Plan
	Err  string // option or BuildPlan error, shown on the page that produced it

	// step 4
	Parent  string // folder the export goes into
	DirName string // new folder's name
	DirErr  string // live validation of the target folder, refreshed every frame
	RunErr  string // the last run's failure, which outlives the frame that showed it
	Busy    bool   // WriteExport runs off the main thread
	Done    bool   // the last run finished successfully
	OutPath string // where it landed
	Written int    // progress numerator: files written
	Total   int    // progress denominator: copies + files + manifest
}

// openExportWizard opens the wizard at its first step, dropping any previous
// run. Without an open folder there is nothing to export, so the toolbar
// reports it instead of opening an empty dialog.
func (a *App) openExportWizard() {
	if a.dir == "" || len(a.images) == 0 {
		a.lastStatus = "请先打开图片文件夹再导出数据集"
		return
	}
	a.ex = exportState{
		Open:        true,
		Step:        expStepFormat,
		Format:      exporter.FormatYOLO,
		Task:        a.inferExportTask(),
		VOCOneBased: true,
		SplitMode:   exporter.SplitPreserve,
		Ratio:       defaultExportRatio,
		Seed:        defaultExportSeed,
		// Sibling of the workspace by default: an export inside the image root
		// would be picked up by the next scan as more images to label.
		Parent:  filepath.Dir(a.dir),
		DirName: defaultExportDirName,
	}
}

// closeExportWizard closes the dialog and forgets the run. A background export
// keeps running: WriteExport is not cancellable (contract §1).
func (a *App) closeExportWizard() {
	if a.ex.Busy {
		a.ex.Open = false
		return
	}
	a.ex = exportState{}
}

// exportBack goes one step back, if there is one.
func (a *App) exportBack() {
	s := &a.ex
	if s.Busy || s.Step <= expStepFormat {
		return
	}
	s.Step--
	s.Err = ""
}

// exportNext advances one step. Leaving the split step runs BuildPlan, whose
// error is reported on the report step; an option the exporter would reject is
// caught before that and keeps the user on the split step.
func (a *App) exportNext() {
	s := &a.ex
	switch s.Step {
	case expStepFormat:
		s.Err = ""
		s.Step = expStepSplit
	case expStepSplit:
		if _, err := s.options(); err != nil {
			s.Err = err.Error()
			return
		}
		a.exportBuildPlan()
	case expStepReport:
		if s.Plan == nil {
			return
		}
		s.Err = ""
		s.Step = expStepOutput
	}
}

// options turns the wizard's choices into exporter.Options, validating the
// random split's seed.
func (s *exportState) options() (exporter.Options, error) {
	opts := exporter.Options{
		Format:          s.Format,
		Split:           exporter.SplitPolicy{Mode: s.SplitMode},
		VOCOneBased:     s.VOCOneBased,
		VOCDowngradeOBB: s.VOCDowngrade,
	}
	if s.Format == exporter.FormatYOLO {
		opts.Task = s.Task
	}
	if s.SplitMode == exporter.SplitRandom {
		seed, err := strconv.ParseInt(strings.TrimSpace(s.Seed), 10, 64)
		if err != nil {
			return opts, fmt.Errorf("随机种子必须是整数：%q", strings.TrimSpace(s.Seed))
		}
		opts.Split.TrainRatio, opts.Split.Seed = s.Ratio, seed
	}
	return opts, nil
}

// exportBuildPlan assembles the IR from the session and asks the exporter for a
// plan. It never touches the disk: this is the dry-run the report step shows.
func (a *App) exportBuildPlan() {
	s := &a.ex
	s.Plan, s.Err = nil, ""
	opts, err := s.options()
	if err != nil {
		s.Err = err.Error()
		s.Step = expStepReport
		return
	}
	proj, images := a.exportProjectIR()
	plan, err := exporter.BuildPlan(proj, images, opts)
	s.Step = expStepReport
	if err != nil {
		s.Err = err.Error()
		return
	}
	s.Plan = plan
}

// exportOutDir is the folder the export creates.
func (s *exportState) exportOutDir() string {
	parent, name := strings.TrimSpace(s.Parent), strings.TrimSpace(s.DirName)
	if parent == "" || name == "" {
		return ""
	}
	return filepath.Join(parent, name)
}

// checkExportTarget validates the folder the export is about to create. The
// exporter checks it again (and refuses to merge); this copy exists so the page
// can say why before the user presses 开始导出.
func checkExportTarget(outDir string) error {
	if strings.TrimSpace(outDir) == "" {
		return errors.New("请选择输出位置并填写目录名")
	}
	if _, err := os.Stat(outDir); err == nil {
		return fmt.Errorf("目录已存在，导出不会覆盖：%s", outDir)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("无法检查目标目录：%v", err)
	}
	parent := filepath.Dir(outDir)
	if st, err := os.Stat(parent); err != nil || !st.IsDir() {
		return fmt.Errorf("上级目录不存在：%s", parent)
	}
	return nil
}

// exportNameValid rejects folder names that are not a single path element: the
// name is joined onto the parent, so a separator or ".." would escape it.
func exportNameValid(name string) error {
	switch strings.TrimSpace(name) {
	case "":
		return errors.New("请填写目录名")
	case ".", "..":
		return fmt.Errorf("目录名不能是 %q", name)
	}
	if strings.ContainsAny(name, `/\:`) {
		return errors.New("目录名不能包含路径分隔符")
	}
	return nil
}

// exportStart writes the plan to the chosen folder. The work happens off the
// main thread and reports progress through win.Update (design rule 2); without
// a window there is no main thread to hand it to (unit tests), so the same work
// runs inline.
func (a *App) exportStart() {
	s := &a.ex
	if s.Busy || s.Done || s.Plan == nil {
		return
	}
	if err := exportNameValid(s.DirName); err != nil {
		s.DirErr = err.Error()
		return
	}
	outDir := s.exportOutDir()
	if err := checkExportTarget(outDir); err != nil {
		s.DirErr = err.Error()
		return
	}
	s.DirErr, s.RunErr = "", ""
	s.Busy, s.Written = true, 0
	s.Total = len(s.Plan.Copies) + len(s.Plan.Files) + 1
	plan, root, win := s.Plan, a.dir, a.win
	if win == nil {
		a.finishExport(outDir, exporter.WriteExport(outDir, root, plan, nil))
		return
	}
	go func() {
		err := exporter.WriteExport(outDir, root, plan, func(done, total int) {
			win.Update(func() {
				if a.ex.Plan != plan {
					return // the wizard was reopened meanwhile: drop stale progress
				}
				a.ex.Written, a.ex.Total = done, total
			})
		})
		win.Update(func() { a.finishExport(outDir, err) })
	}()
}

// finishExport records the outcome, announces it on the status bar and as a
// toast, and keeps the dialog open so the path stays readable.
func (a *App) finishExport(outDir string, err error) {
	s := &a.ex
	s.Busy = false
	if err != nil {
		// RunErr, not DirErr: the page refreshes DirErr from the live check every
		// frame, which would wipe a message about what already went wrong.
		s.RunErr = "导出失败：" + err.Error()
		a.toast = "导出失败：" + err.Error()
		return
	}
	s.RunErr = ""
	s.Done = true
	s.OutPath = outDir
	s.Written = s.Total
	a.lastStatus = "已导出数据集到 " + outDir
	a.toast = "已导出数据集到 " + outDir
}

// browseExportParent asks the platform for the folder the export goes into. Like
// the import wizard's browse it needs a window; without one the field stays
// editable by hand, which is how tests drive the page.
func (a *App) browseExportParent() {
	if a.win == nil {
		return
	}
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:    a.win,
		Title:     "选择导出位置",
		Directory: true,
	})
	if err != nil || len(paths) == 0 {
		return
	}
	a.ex.Parent = paths[0]
}

// exportModal is the wizard's dialog. The step pages share one footer.
func (a *App) exportModal(c *ui.Context) {
	s := &a.ex
	if !s.Open {
		return
	}
	t := c.Theme()
	ui.Modal(c, &s.Open, func() {
		ui.Column(c).Width(760).Gap(10).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "导出数据集").FontSize(14).Bold()
				ui.Textf(c, "第 %d/4 步 · %s", int(s.Step)+1, s.Step.title()).FontSize(11).TextColor(t.TextMuted)
				ui.Textf(c, "%d 张图片 / %d 条标注", len(a.images), a.exportAnnCount()).FontSize(11).TextColor(t.TextMuted)
			})
			switch s.Step {
			case expStepFormat:
				a.exportFormatPage(c)
			case expStepSplit:
				a.exportSplitPage(c)
			case expStepReport:
				a.exportReportPage(c)
			case expStepOutput:
				a.exportOutputPage(c)
			}
			a.exportFooter(c)
		})
	})
}

// exportFormatPage is step 1: the target format and its own options.
func (a *App) exportFormatPage(c *ui.Context) {
	s := &a.ex
	t := c.Theme()
	ui.Text(c, "1. 选择目标格式").FontSize(12)
	ui.RadioGroup(c, func() {
		for _, f := range exportFormats {
			ui.Radio(c.Key("expFmt"+string(f)), &s.Format, f, exportFormatLabel(f))
		}
	}).Row().Gap(16).Label("导出格式")
	if s.Format == exporter.FormatYOLO {
		ui.Text(c, "YOLO 任务（默认按当前标注内容推断）").FontSize(11).TextColor(t.TextMuted)
		ui.RadioGroup(c, func() {
			for _, task := range exportTasks {
				ui.Radio(c.Key("expTask"+string(task)), &s.Task, task, exportTaskLabel(task))
			}
		}).Row().Gap(16).Label("YOLO 任务")
	}
	if s.Format == exporter.FormatVOC {
		ui.Checkbox(c.Key("expVocOneBased"), &s.VOCOneBased, "bndbox 写 1-based 坐标（VOC 官方惯例）").Label("VOC 1-based")
		ui.Checkbox(c.Key("expVocDowngrade"), &s.VOCDowngrade, "旋转框降级为外接 bndbox（有损，丢方向）").Label("VOC 降级 OBB")
	}
	if s.Format == exporter.FormatCOCO {
		ui.Text(c, "COCO 导出旋转框会写非标准扩展字段 mylabel_obb，报告里会列出条数").FontSize(10).TextColor(t.TextMuted)
	}
}

// exportSplitPage is step 2: keep the source layout's split or shuffle by ratio.
func (a *App) exportSplitPage(c *ui.Context) {
	s := &a.ex
	t := c.Theme()
	ui.Text(c, "2. 训练/验证划分").FontSize(12)
	ui.RadioGroup(c, func() {
		ui.Radio(c.Key("expSplitPreserve"), &s.SplitMode, exporter.SplitPreserve, "保留源目录的 train/val，其余归 train")
		ui.Radio(c.Key("expSplitRandom"), &s.SplitMode, exporter.SplitRandom, "按比例随机划分（同种子可复现）")
	}).Label("划分方式")
	if s.SplitMode == exporter.SplitRandom {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "训练集比例").FontSize(11).TextColor(t.TextMuted)
			ui.NumberInput(c, &s.Ratio, 0.05, 0.95, 0.05).Label("训练集比例")
			ui.Text(c, "种子").FontSize(11).TextColor(t.TextMuted)
			ui.TextInput(c.Key("expSeed"), &s.Seed).Width(120).Label("随机种子")
			ui.Text(c, "同比例同种子每次划分结果一致").FontSize(10).TextColor(t.TextMuted)
		})
	}
	if s.Err != "" {
		ui.Text(c, "设置无效："+s.Err).FontSize(11).TextColor(t.Danger)
	}
}

// exportReportPage is step 3: the dry-run report, or the error that stopped it.
func (a *App) exportReportPage(c *ui.Context) {
	s := &a.ex
	t := c.Theme()
	if s.Err != "" {
		ui.Text(c, "无法导出").FontSize(12).TextColor(t.Danger)
		ui.Scroll(c).Height(300).Children(func() {
			ui.Text(c, s.Err).FontSize(11).TextColor(t.Danger)
		})
		return
	}
	rep := s.Plan.Report
	ui.Textf(c, "试运行报告：%d 张图片（%d 张有标注）/ %d 条标注，丢弃 %d 条",
		rep.Images, rep.ImagesWithAnns, rep.Annotations, rep.AnnotationsDropped).FontSize(12)
	train, val := splitCounts(s.Plan.Splits)
	ui.Textf(c, "划分：train %d 张，val %d 张；类别 %d 个（%s）",
		train, val, len(s.Plan.Categories), strings.Join(planCategoryNames(s.Plan), "、")).FontSize(11).TextColor(t.TextMuted)
	if a.dirty {
		// The export reads the session, not the store, so pending edits are
		// included; say so instead of demanding a save (§9 保存检查的反向做法).
		ui.Text(c, "当前有未保存的修改：导出按屏幕上的内容进行，不要求先保存").FontSize(10).TextColor(t.TextMuted)
	}
	ui.Divider(c)
	ui.Scroll(c).Height(280).Children(func() {
		ui.Column(c).Gap(4).Children(func() {
			if len(rep.Lossy) == 0 && len(rep.Warnings) == 0 {
				ui.Text(c, "没有有损转换或警告").FontSize(11).TextColor(t.TextMuted)
			}
			for _, stat := range rep.Lossy {
				ui.Textf(c, "%s ×%d", stat.Rule, stat.Count).FontSize(11).TextColor(t.Warning)
				for i, smp := range stat.Samples {
					if i >= reportSamples {
						break
					}
					line := "示例：" + smp.File
					if smp.AnnID != "" {
						line += " 标注 " + smp.AnnID
					}
					if smp.Message != "" {
						line += " — " + smp.Message
					}
					ui.Text(c, line).FontSize(10).TextColor(t.TextMuted).SingleLine().TruncateMiddle()
				}
			}
			for _, w := range rep.Warnings {
				ui.Text(c, "警告："+w).FontSize(11).TextColor(t.Warning).SingleLine().TruncateMiddle()
			}
			if len(a.categories) == 0 {
				ui.Text(c, "当前工作区没有类别：所有标注都会被丢弃").FontSize(11).TextColor(t.Danger)
			}
		})
	})
}

// reportSamples is how many examples per lossy rule the report lists (the
// exporter keeps up to five; three fit the page without a wall of text).
const reportSamples = 3

// splitCounts counts the images of a plan's train and val halves.
func splitCounts(splits map[string]string) (train, val int) {
	for _, sp := range splits {
		if sp == "val" {
			val++
			continue
		}
		train++
	}
	return train, val
}

// planCategoryNames returns the plan's category names in index order, for the
// report's summary line.
func planCategoryNames(p *exporter.Plan) []string {
	names := make([]string, 0, len(p.Categories))
	for _, cat := range p.Categories {
		name := strings.TrimSpace(cat.Name)
		if name == "" {
			name = cat.Key
		}
		names = append(names, name)
	}
	return names
}

// exportOutputPage is step 4: where the export goes, and the run itself.
func (a *App) exportOutputPage(c *ui.Context) {
	s := &a.ex
	t := c.Theme()
	ui.Text(c, "4. 选择输出位置（导出会在这里新建一个目录，已存在的目录不会被覆盖）").FontSize(12)
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.TextInput(c.Key("expParent"), &s.Parent).Grow(1).Label("导出位置")
		if ui.Button(c.Key("expBrowse"), "浏览…").Clicked() {
			a.browseExportParent()
		}
	})
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "目录名").FontSize(11).TextColor(t.TextMuted)
		ui.TextInput(c.Key("expDirName"), &s.DirName).Width(240).Label("导出目录名")
	})
	outDir := s.exportOutDir()
	ui.Text(c, "将写入："+outDir).FontSize(11).TextColor(t.TextMuted).SingleLine().TruncateMiddle()

	// Live validation, so an existing folder is reported before the run instead
	// of by the exporter's error.
	s.DirErr = s.targetError()

	if s.Done {
		ui.Text(c, "导出完成："+s.OutPath).FontSize(12).TextColor(t.Accent)
	} else if s.Busy {
		ui.Progress(c, exportRatio(s.Written, s.Total)).Label("导出进度")
		ui.Textf(c, "正在写入 %d/%d", s.Written, s.Total).FontSize(11).TextColor(t.TextMuted)
	} else if s.RunErr != "" {
		ui.Text(c, s.RunErr).FontSize(11).TextColor(t.Danger)
	} else if s.DirErr != "" {
		ui.Text(c, s.DirErr).FontSize(11).TextColor(t.Danger)
	}
}

// targetError is the live verdict on the chosen output folder: empty when the
// export could start, the reason otherwise.
func (s *exportState) targetError() string {
	if s.Done {
		return ""
	}
	if err := exportNameValid(s.DirName); err != nil {
		return err.Error()
	}
	if err := checkExportTarget(s.exportOutDir()); err != nil {
		return err.Error()
	}
	return ""
}

// exportRatio is the progress bar's value: 0 until the total is known, then the
// fraction written.
func exportRatio(done, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(done) / float64(total)
}

// exportFooter draws the step buttons and the run action.
func (a *App) exportFooter(c *ui.Context) {
	s := &a.ex
	ui.Row(c).Gap(8).Children(func() {
		if s.Step > expStepFormat && !s.Busy && !s.Done {
			if ui.Button(c.Key("expBack"), "上一步").Clicked() {
				a.exportBack()
			}
		}
		switch {
		case s.Step == expStepOutput:
			if s.Done {
				break
			}
			btn := ui.PrimaryButton(c.Key("expStart"), "开始导出")
			if s.Busy || s.DirErr != "" {
				btn = btn.Disabled(true)
			}
			if btn.Clicked() {
				a.exportStart()
			}
		case s.Step == expStepReport && s.Plan == nil:
			// Nothing to confirm when the plan failed: the report is the error.
		case s.Step < expStepOutput:
			if ui.Button(c.Key("expNext"), "下一步").Clicked() {
				a.exportNext()
			}
		}
		if ui.Button(c.Key("expClose"), "关闭").Clicked() {
			a.closeExportWizard()
		}
	})
}
