package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"mylabel/internal/importer"
	"mylabel/internal/ir"
)

// The import wizard (docs/phase3-design.md §4.2): 格式 → 路径 → 解析预览与类别
// 映射 → 错误与有损报告 → 应用. Its state lives on App so a frame never owns
// it, and the parse/apply halves are plain functions, so the whole flow is
// testable without a window: a test writes wiz.Path and clicks through the
// steps.

// importFormat is the source format the wizard parses.
type importFormat int

const (
	formatYOLO importFormat = iota
	formatCOCO
	formatVOC
	formatIR
)

// importFormats is the wizard's radio order.
var importFormats = []importFormat{formatYOLO, formatCOCO, formatVOC, formatIR}

// label is the radio label of a format.
func (f importFormat) label() string {
	switch f {
	case formatYOLO:
		return "YOLO"
	case formatCOCO:
		return "COCO"
	case formatVOC:
		return "VOC"
	case formatIR:
		return "IR 项目"
	}
	return "未知"
}

// wantsDir reports whether the format's path is a directory rather than a
// single file (docs/phase3-design.md §4.2 step 2).
func (f importFormat) wantsDir() bool {
	switch f {
	case formatCOCO:
		return false
	}
	return true
}

// pathHint is the explanation the path step shows for the chosen format.
func (f importFormat) pathHint() string {
	switch f {
	case formatYOLO:
		return "选择 YOLO 数据集根目录（含 images/ 与 labels/）或 labels 目录；类别名取根目录的 data.yaml 或唯一 *.yaml"
	case formatCOCO:
		return "选择 COCO 标注 JSON 文件"
	case formatVOC:
		return "选择 VOC XML 目录"
	case formatIR:
		return "选择 IR 项目根目录（含 project.json）"
	}
	return ""
}

// yoloTasks are the YOLO readings the wizard offers; auto guesses from the
// first data line (docs/phase3-design.md §3.1).
var yoloTasks = []string{"auto", "det", "seg", "obb"}

// wizardStep is the wizard's current page.
type wizardStep int

const (
	wizStepFormat  wizardStep = iota // 1. format (+ format options)
	wizStepPath                      // 2. path
	wizStepMapping                   // 3. parse preview + category mapping
	wizStepReport                    // 4. errors and warnings + apply
)

// importChoice is one source category's mapping: reuse an existing category or
// create a new one (§4.2 step 3).
type importChoice struct {
	Key    string // source category key (the IR's stable key)
	Name   string // display name
	Pick   string // the label the mapping select shows; authoritative when set
	NewCat bool   // create a category instead of reusing one
	Target int    // index into a.categories when !NewCat
}

// wizardState is the import wizard's whole state, owned by App.
type wizardState struct {
	Open bool
	Step wizardStep

	// step 1
	Format      importFormat
	YoloTask    string
	VOCOneBased bool

	// step 2
	Path string
	Err  string // fatal parse error, shown on the path step

	// step 3 and 4
	DS       *importer.Dataset
	Issues   []ir.Issue // locatable parse errors
	Warnings []ir.Issue // lossy conversions from the parser
	Choices  []importChoice

	// step 4 / apply
	Applied bool
	Summary importResult
}

// openImportWizard opens the wizard at its first step, dropping any previous
// run.
func (a *App) openImportWizard() {
	a.wiz = wizardState{
		Open:     true,
		Step:     wizStepFormat,
		Format:   formatYOLO,
		YoloTask: yoloTasks[0],
		// Default to the open folder: importing labels that sit next to the
		// images is the common case.
		Path: a.dir,
	}
}

// closeImportWizard closes the dialog and forgets the run.
func (a *App) closeImportWizard() { a.wiz = wizardState{} }

// wizardBack goes one step back, if there is one.
func (a *App) wizardBack() {
	if a.wiz.Step > wizStepFormat {
		a.wiz.Step--
		a.wiz.Err = ""
	}
}

// wizardNext advances one step. Leaving the path step parses the source; a
// fatal parse error keeps the wizard there with the message shown.
func (a *App) wizardNext() {
	w := &a.wiz
	switch w.Step {
	case wizStepFormat:
		w.Step = wizStepPath
	case wizStepPath:
		if !a.wizardParse() {
			return
		}
		w.Step = wizStepMapping
	case wizStepMapping:
		w.Step = wizStepReport
	}
}

// wizardParse parses the path with the format's options into the wizard's
// dataset, issues and default mapping. It reports whether parsing succeeded.
func (a *App) wizardParse() bool {
	w := &a.wiz
	w.Err = ""
	w.DS, w.Issues, w.Warnings, w.Choices = nil, nil, nil, nil

	// An import may bring its own images: with no workspace yet, the source
	// path decides which folder to open. openDir runs first because it replaces
	// a.images, whose sizes the YOLO parse reads.
	if len(a.images) == 0 && !a.wizardAutoLoad() {
		return false
	}
	ds, issues, err := a.parseImport()
	if err != nil {
		w.Err = err.Error()
		return false
	}
	w.DS = ds
	w.Issues = issues
	w.Warnings = ds.Warnings
	w.Choices = a.defaultChoices(ds)
	return true
}

// wizardAutoLoad opens the folder the chosen source implies when no workspace
// is open, so importing does not require opening the right folder first. It
// reports whether the wizard should go on parsing.
func (a *App) wizardAutoLoad() bool {
	w := &a.wiz
	if strings.TrimSpace(w.Path) == "" {
		w.Err = "请先选择路径"
		return false
	}
	root := importImageRoot(w.Format, strings.TrimSpace(w.Path))
	a.openDir(root)
	if len(a.images) == 0 {
		// openDir leaves the real reason on the status bar (scan or store
		// error); the path step gets the actionable summary.
		w.Err = fmt.Sprintf("在 %s 下没有找到图片，请先打开图片文件夹或检查路径", root)
		return false
	}
	// An IR project imports itself while its folder opens (openir.go), so the
	// images and annotations are already in place; mapping the same project
	// again would duplicate every annotation. The workspace is ready, so the
	// wizard closes without an error.
	if w.Format == formatIR && a.workspaceHasAnnotations() {
		a.closeImportWizard()
		return false
	}
	// openDir widens a YOLO split to the dataset root (X/images/train → X).
	// Parsing from that root covers every split, which is the point of widening
	// the workspace at all: the chosen split's own labels mirror is empty.
	if w.Format == formatYOLO {
		w.Path = a.dir
	}
	return true
}

// workspaceHasAnnotations reports whether any image of the open folder already
// carries annotations.
func (a *App) workspaceHasAnnotations() bool {
	for i := range a.images {
		if len(a.images[i].Anns) > 0 {
			return true
		}
	}
	return false
}

// cocoAnnotationsDir is the directory COCO exports keep their JSON in; its
// parent is the dataset root.
const cocoAnnotationsDir = "annotations"

// importImageRoot derives the folder that becomes the workspace from the
// wizard's source path, following each format's layout:
//
//	YOLO  the path itself when it holds labels/, else the parent of a labels/
//	      directory, else the path
//	VOC   the path itself
//	COCO  the JSON's folder, or that folder's parent when it is annotations/
//	IR    the project root itself
//
// openDir widens X/images[/split] to X afterwards, so train and val end up in
// one workspace.
func importImageRoot(f importFormat, path string) string {
	clean := filepath.Clean(path)
	switch f {
	case formatYOLO:
		if hasChildDir(clean, yoloLabelsDir) {
			return clean
		}
		if strings.EqualFold(filepath.Base(clean), yoloLabelsDir) {
			return filepath.Dir(clean)
		}
		return clean
	case formatCOCO:
		dir := filepath.Dir(clean)
		if strings.EqualFold(filepath.Base(dir), cocoAnnotationsDir) {
			return filepath.Dir(dir)
		}
		return dir
	}
	return clean
}

// parseImport dispatches to the importer of the chosen format. YOLO needs the
// folder's image sizes to denormalize its coordinates; they come from the open
// folder, whose rel paths are the scan's (docs/phase3-design.md §3.1).
func (a *App) parseImport() (*importer.Dataset, []ir.Issue, error) {
	w := &a.wiz
	p := strings.TrimSpace(w.Path)
	if p == "" {
		return nil, nil, errors.New("请先选择路径")
	}
	switch w.Format {
	case formatYOLO:
		dims := make(map[string][2]int, len(a.images))
		for i := range a.images {
			dims[a.images[i].RelPath] = [2]int{a.images[i].Width, a.images[i].Height}
		}
		task := w.YoloTask
		if task == "" {
			task = yoloTasks[0]
		}
		return importer.ParseYOLO(p, dims, importer.Options{Task: task})
	case formatVOC:
		return importer.ParseVOC(p, importer.Options{VOCOneBased: w.VOCOneBased})
	case formatCOCO:
		return importer.ParseCOCO(p)
	case formatIR:
		return readIRDataset(p)
	}
	return nil, nil, fmt.Errorf("未知的导入格式")
}

// readIRDataset reads an IR project as a Dataset: the project's category table
// and its image documents, normalized and validated the way the importers leave
// their own output (docs/phase3-design.md §3.5).
func readIRDataset(dir string) (*importer.Dataset, []ir.Issue, error) {
	proj, images, err := ir.ReadProject(dir)
	if err != nil {
		return nil, nil, err
	}
	ds := &importer.Dataset{Categories: proj.Categories, Images: images}
	var issues []ir.Issue
	for i := range ds.Images {
		ds.Warnings = append(ds.Warnings, ir.Normalize(proj, &ds.Images[i])...)
		issues = append(issues, ir.Validate(proj, &ds.Images[i])...)
	}
	return ds, issues, nil
}

// wizardApply writes the parsed dataset into the open folder as one undoable
// step and closes the dialog.
func (a *App) wizardApply() {
	w := &a.wiz
	if w.DS == nil || w.Applied {
		return
	}
	res := a.importDataset(w.DS, w.Choices)
	w.Summary = res
	w.Applied = true
	w.Open = false

	status := res.String()
	if len(res.Warnings) > 0 {
		status += "；" + res.Warnings[0]
		if len(res.Warnings) > 1 {
			status += fmt.Sprintf("（另有 %d 条警告）", len(res.Warnings)-1)
		}
	}
	a.lastStatus = status
}

// totalAnnots counts a dataset's annotations, for the preview line.
func totalAnnots(ds *importer.Dataset) int {
	n := 0
	if ds == nil {
		return 0
	}
	for i := range ds.Images {
		n += len(ds.Images[i].Annotations)
	}
	return n
}

// mappingOptions is the option list of one mapping row: every existing
// category, plus "create a new one" for this source category.
func (a *App) mappingOptions(name string) []string {
	opts := make([]string, 0, len(a.categories)+1)
	for i := range a.categories {
		opts = append(opts, a.existingOption(i))
	}
	return append(opts, newCatOption(name))
}

// issueLine renders one locatable issue for the report step.
func issueLine(kind string, is ir.Issue) string {
	loc := is.File
	if loc == "" {
		loc = "数据集"
	}
	if is.Line > 0 {
		loc = fmt.Sprintf("%s:%d", loc, is.Line)
	}
	if is.Index >= 0 {
		loc = fmt.Sprintf("%s 第 %d 条", loc, is.Index+1)
	}
	if is.Field != "" {
		loc += " " + is.Field
	}
	return fmt.Sprintf("[%s] %s：%s", kind, loc, is.Message)
}

// reportIssues returns the issues of the report step in a stable order, errors
// before warnings.
func (w *wizardState) reportLines() []string {
	lines := make([]string, 0, len(w.Issues)+len(w.Warnings))
	for _, is := range w.Issues {
		lines = append(lines, issueLine("错误", is))
	}
	warns := append([]ir.Issue(nil), w.Warnings...)
	sort.SliceStable(warns, func(i, j int) bool {
		if warns[i].File != warns[j].File {
			return warns[i].File < warns[j].File
		}
		return warns[i].Line < warns[j].Line
	})
	for _, is := range warns {
		lines = append(lines, issueLine("警告", is))
	}
	return lines
}

// browseImportPath asks the platform for the path of the chosen format. It
// needs a window; without one the path field stays editable by hand, which is
// also how tests drive the wizard.
func (a *App) browseImportPath() {
	if a.win == nil {
		return
	}
	opts := mygo.OpenDialogOptions{
		Parent:    a.win,
		Title:     "选择" + a.wiz.Format.label() + " 标注来源",
		Directory: a.wiz.Format.wantsDir(),
	}
	if !a.wiz.Format.wantsDir() {
		opts.Filters = []mygo.FileFilter{{Name: "标注文件", Extensions: []string{"json"}}}
	}
	paths, err := mygo.Dialog.Open(opts)
	if err != nil || len(paths) == 0 {
		return
	}
	a.wiz.Path = paths[0]
}

// importModal is the wizard's dialog. The step pages share one footer with
// 上一步 / 下一步 / 导入 / 取消.
func (a *App) importModal(c *ui.Context) {
	w := &a.wiz
	t := c.Theme()
	ui.Modal(c, &w.Open, func() {
		ui.Column(c).Width(760).Gap(10).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "导入标注").FontSize(14).Bold()
				ui.Textf(c, "第 %d/4 步 · %s", int(w.Step)+1, w.Step.title()).FontSize(11).TextColor(t.TextMuted)
			})
			switch w.Step {
			case wizStepFormat:
				a.wizardFormatPage(c)
			case wizStepPath:
				a.wizardPathPage(c)
			case wizStepMapping:
				a.wizardMappingPage(c)
			case wizStepReport:
				a.wizardReportPage(c)
			}
			a.wizardFooter(c)
		})
	})
}

// title is the step's name, shown in the header.
func (s wizardStep) title() string {
	switch s {
	case wizStepFormat:
		return "格式"
	case wizStepPath:
		return "路径"
	case wizStepMapping:
		return "解析预览与类别映射"
	case wizStepReport:
		return "错误与警告"
	}
	return ""
}

// wizardFormatPage is step 1: the source format, plus the YOLO task and the VOC
// 1-based switch that belong to it (§4.2).
func (a *App) wizardFormatPage(c *ui.Context) {
	w := &a.wiz
	t := c.Theme()
	ui.Text(c, "1. 选择来源格式").FontSize(12)
	ui.RadioGroup(c, func() {
		for _, f := range importFormats {
			ui.Radio(c, &w.Format, f, f.label())
		}
	}).Row().Gap(16).Label("导入格式")
	if w.Format == formatYOLO {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "YOLO 任务").FontSize(11).TextColor(t.TextMuted)
			ui.Select(c.Key("wizYoloTask"), &w.YoloTask, yoloTasks).Label("YOLO 任务")
			ui.Text(c, "9 字段歧义（obb 还是 4 点 seg）由此裁决，auto 按首行字段数猜").FontSize(10).TextColor(t.TextMuted)
		})
	}
	if w.Format == formatVOC {
		ui.Checkbox(c.Key("wizVocOneBased"), &w.VOCOneBased, "bndbox 是 1-based（读取时减 1）").Label("VOC 1-based")
	}
}

// wizardPathPage is step 2: the source path, picked through the platform dialog
// or typed in.
func (a *App) wizardPathPage(c *ui.Context) {
	w := &a.wiz
	t := c.Theme()
	ui.Text(c, "2. 选择路径").FontSize(12)
	ui.Text(c, w.Format.pathHint()).FontSize(11).TextColor(t.TextMuted)
	if a.dir == "" {
		ui.Text(c, "未打开图片文件夹：将自动从数据集目录载入图片").FontSize(11).TextColor(t.TextMuted)
	}
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.TextInput(c.Key("wizPath"), &w.Path).Grow(1).Label("导入路径")
		if ui.Button(c.Key("wizBrowse"), "浏览…").Clicked() {
			a.browseImportPath()
		}
	})
	if w.Err != "" {
		ui.Text(c, "解析失败："+w.Err).FontSize(11).TextColor(t.Danger)
	}
}

// wizardMappingPage is step 3: what was parsed, and one mapping row per source
// category (§4.2).
func (a *App) wizardMappingPage(c *ui.Context) {
	w := &a.wiz
	t := c.Theme()
	if w.DS == nil {
		ui.Text(c, "没有解析结果").FontSize(12).TextColor(t.Danger)
		return
	}
	ui.Textf(c, "解析预览：%d 张图片 / %d 条标注；警告 %d 条，可定位错误 %d 条",
		len(w.DS.Images), totalAnnots(w.DS), len(w.Warnings), len(w.Issues)).FontSize(12)
	ui.Text(c, "类别映射：每个源类别选一个现有类别，或新建").FontSize(11).TextColor(t.TextMuted)
	ui.Divider(c)
	ui.Scroll(c).Height(300).Children(func() {
		ui.Column(c).Gap(4).Children(func() {
			for i := range w.Choices {
				ch := &w.Choices[i]
				opts := a.mappingOptions(ch.Name)
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, ch.Name).FontSize(12).Width(220).SingleLine().TruncateMiddle()
					ui.Select(c.Key(fmt.Sprintf("wizMap%d", i)), &ch.Pick, opts).
						Label("映射:" + ch.Key)
					ui.Textf(c, "来源 %q", ch.Key).FontSize(10).TextColor(t.TextMuted)
				})
			}
		})
	})
}

// wizardReportPage is step 4: every locatable error and lossy warning, then the
// apply button. Errors are skipped entry by entry, so the correct part of a
// dataset still imports (§4.2).
func (a *App) wizardReportPage(c *ui.Context) {
	w := &a.wiz
	t := c.Theme()
	ui.Textf(c, "错误与警告：错误 %d 条，警告 %d 条；导入时错误条目会被跳过",
		len(w.Issues), len(w.Warnings)).FontSize(12)
	lines := w.reportLines()
	ui.Scroll(c).Height(300).Children(func() {
		ui.Column(c).Gap(2).Children(func() {
			if len(lines) == 0 {
				ui.Text(c, "没有错误或警告").FontSize(11).TextColor(t.TextMuted)
				return
			}
			for _, line := range lines {
				col := t.Warning
				if strings.HasPrefix(line, "[错误]") {
					col = t.Danger
				}
				ui.Text(c, line).FontSize(11).TextColor(col).SingleLine().TruncateMiddle()
			}
		})
	})
}

// wizardFooter draws the step buttons and the apply action.
func (a *App) wizardFooter(c *ui.Context) {
	w := &a.wiz
	ui.Row(c).Gap(8).Children(func() {
		if w.Step > wizStepFormat {
			if ui.Button(c.Key("wizBack"), "上一步").Clicked() {
				a.wizardBack()
			}
		}
		if w.Step < wizStepReport {
			if ui.Button(c.Key("wizNext"), "下一步").Clicked() {
				a.wizardNext()
			}
		} else if ui.PrimaryButton(c.Key("wizApply"), "导入正确部分").Clicked() {
			a.wizardApply()
		}
		if ui.Button(c.Key("wizCancel"), "取消").Clicked() {
			a.closeImportWizard()
		}
	})
}
