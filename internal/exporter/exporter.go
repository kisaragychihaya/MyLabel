// Package exporter turns IR (internal/ir) into YOLO/COCO/VOC dataset
// folders. The pipeline is two-phase: BuildPlan computes every output file
// and the dry-run report without touching the disk; WriteExport assembles a
// Plan into a fresh directory atomically. The binding contract is
// docs/phase4-design.md.
package exporter

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"mylabel/internal/ir"
)

// Format is the target dataset format.
type Format string

const (
	FormatYOLO Format = "yolo"
	FormatCOCO Format = "coco"
	FormatVOC  Format = "voc"
)

// Task is the YOLO task flavour; only meaningful for FormatYOLO.
type Task string

const (
	TaskDet Task = "det"
	TaskSeg Task = "seg"
	TaskObb Task = "obb"
)

// SplitMode picks how images are divided into train/val.
type SplitMode string

const (
	// SplitPreserve keeps a leading train/val path segment, defaulting the
	// rest to train. It is the zero-value default.
	SplitPreserve SplitMode = "preserve"
	// SplitRandom shuffles deterministically by Seed and cuts at TrainRatio.
	SplitRandom SplitMode = "random"
)

// SplitPolicy configures assignSplits. The zero value is SplitPreserve.
type SplitPolicy struct {
	Mode       SplitMode `json:"mode"`
	TrainRatio float64   `json:"train_ratio,omitempty"` // SplitRandom only
	Seed       int64     `json:"seed,omitempty"`        // SplitRandom only
}

// Options is one export request.
type Options struct {
	Format Format      `json:"format"`
	Task   Task        `json:"task,omitempty"` // YOLO only
	Split  SplitPolicy `json:"split"`
	// VOCOneBased writes VOC coordinates 1-based (VOC official convention);
	// false keeps them 0-based. Ignored by other formats.
	VOCOneBased bool `json:"voc_one_based,omitempty"`
	// VOCDowngradeOBB writes rotated boxes as enclosing bndbox instead of
	// the robndbox private extension. Lossy; must be an explicit user choice.
	VOCDowngradeOBB bool `json:"voc_downgrade_obb,omitempty"`
}

// Lossy rule identifiers shared by the report and the manifest.
const (
	RuleObbToDet    = "obb→det 外接框（丢方向）"
	RuleSegToDet    = "seg→det 外接框"
	RuleObbToSeg    = "obb→seg 四点轮廓"
	RuleDetToSeg    = "det→seg 矩形四点"
	RuleDetToObb    = "det→obb 矩形四点"
	RuleSegToObb    = "seg→obb 最小外接旋转矩形（仅适用近似矩形目标）"
	RuleMultiRing   = "多环 seg→单环细桥接"
	RuleObbToCOCO   = "obb→COCO 四点 segmentation + 私有扩展（非标准）"
	RuleObbToBndbox = "obb→VOC bndbox 降级（丢方向）"
	RuleSegDropped  = "seg 不随 VOC 导出（已跳过）"
	RuleClipped     = "越界坐标裁剪"
	RuleDropped     = "无法转换或未知类别（已跳过）"
)

// Issue locates one conversion finding.
type Issue struct {
	File    string `json:"file"`
	AnnID   string `json:"ann_id,omitempty"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

// LossyStat aggregates one rule: how often it fired plus a few examples.
type LossyStat struct {
	Rule    string  `json:"rule"`
	Count   int     `json:"count"`
	Samples []Issue `json:"samples,omitempty"` // at most 5
}

// Report is the dry-run outcome shown to the user and embedded verbatim in
// the export manifest.
type Report struct {
	Images             int         `json:"images"`
	ImagesWithAnns     int         `json:"images_with_annotations"`
	Annotations        int         `json:"annotations"`
	AnnotationsDropped int         `json:"annotations_dropped"`
	Lossy              []LossyStat `json:"lossy,omitempty"`
	Warnings           []string    `json:"warnings,omitempty"`
}

// OutFile is one non-image file to write under the export root.
type OutFile struct {
	Rel  string // slash-separated path relative to the export root
	Data []byte
}

// ImageCopy copies one source image into the export tree. Src is relative to
// the image root, Dst to the export root (both slash-separated).
type ImageCopy struct {
	Src string
	Dst string
}

// Plan is everything WriteExport needs. It is produced by BuildPlan and is
// safe to inspect before confirming.
type Plan struct {
	Opts       Options
	Splits     map[string]string // image.File -> "train" | "val"
	Categories []ir.Category     // Index/COCOID assigned for the target format
	Files      []OutFile
	Copies     []ImageCopy
	Report     Report
}

// maxSamples caps LossyStat.Samples per rule.
const maxSamples = 5

// recorder accumulates Report entries while a format builder converts.
type recorder struct {
	rep   Report
	stats map[string]*LossyStat
}

func newRecorder() *recorder {
	return &recorder{stats: map[string]*LossyStat{}}
}

// count records that rule fired once for the given file/annotation.
func (r *recorder) count(rule, file, annID, msg string) {
	st, ok := r.stats[rule]
	if !ok {
		st = &LossyStat{Rule: rule}
		r.stats[rule] = st
		r.rep.Lossy = append(r.rep.Lossy, LossyStat{Rule: rule})
	}
	st.Count++
	idx := -1
	for i := range r.rep.Lossy {
		if r.rep.Lossy[i].Rule == rule {
			idx = i
			break
		}
	}
	if len(st.Samples) < maxSamples {
		st.Samples = append(st.Samples, Issue{File: file, AnnID: annID, Rule: rule, Message: msg})
		r.rep.Lossy[idx].Samples = st.Samples
	}
	r.rep.Lossy[idx].Count = st.Count
}

// warn appends a free-form warning once per identical text.
func (r *recorder) warn(msg string) {
	for _, w := range r.rep.Warnings {
		if w == msg {
			return
		}
	}
	r.rep.Warnings = append(r.rep.Warnings, msg)
}

// BuildPlan validates the request, assigns splits, dispatches to the format
// builder, and runs the shared cross-format checks (docs/phase4-design.md
// §2.1). It performs no I/O.
func BuildPlan(proj *ir.Project, images []ir.Image, opts Options) (*Plan, error) {
	if proj == nil {
		return nil, errors.New("exporter: 项目为 nil")
	}
	switch opts.Format {
	case FormatYOLO:
		switch opts.Task {
		case TaskDet, TaskSeg, TaskObb:
		default:
			return nil, fmt.Errorf("exporter: YOLO 导出必须指定任务（det/seg/obb），得到 %q", opts.Task)
		}
	case FormatCOCO, FormatVOC:
	default:
		return nil, fmt.Errorf("exporter: 未知导出格式 %q", opts.Format)
	}
	if opts.Split.Mode == "" {
		opts.Split.Mode = SplitPreserve
	}
	if opts.Split.Mode == SplitRandom && !(opts.Split.TrainRatio > 0 && opts.Split.TrainRatio < 1) {
		return nil, fmt.Errorf("exporter: 随机划分的训练集比例须在 (0,1) 内，得到 %v", opts.Split.TrainRatio)
	}
	if len(images) == 0 {
		return nil, errors.New("exporter: 没有可导出的图片")
	}
	seen := make(map[string]bool, len(images))
	for _, img := range images {
		if strings.TrimSpace(img.File) == "" {
			return nil, errors.New("exporter: 存在 file 为空的图片")
		}
		if seen[img.File] {
			return nil, fmt.Errorf("exporter: 图片 %q 重复", img.File)
		}
		seen[img.File] = true
	}

	splits, err := assignSplits(images, opts.Split)
	if err != nil {
		return nil, err
	}

	var plan *Plan
	switch opts.Format {
	case FormatYOLO:
		plan, err = buildYOLOPlan(proj, images, splits, opts)
	case FormatCOCO:
		plan, err = buildCOCOPlan(proj, images, splits, opts)
	case FormatVOC:
		plan, err = buildVOCPlan(proj, images, splits, opts)
	}
	if err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, errors.New("exporter: 格式构建器返回空计划")
	}
	plan.Opts = opts
	plan.Splits = splits
	if err := checkPlanPaths(plan); err != nil {
		return nil, err
	}
	return plan, nil
}

// checkPlanPaths rejects unsafe paths and duplicate outputs (case-insensitive,
// since the primary target is Windows).
func checkPlanPaths(p *Plan) error {
	taken := map[string]string{}
	check := func(rel, what string) error {
		if _, err := cleanRel(rel); err != nil {
			return fmt.Errorf("exporter: %s路径非法: %w", what, err)
		}
		key := strings.ToLower(rel)
		if prev, dup := taken[key]; dup {
			return fmt.Errorf("exporter: 输出路径冲突: %q 与 %q 指向同一文件（大小写不敏感）", prev, rel)
		}
		taken[key] = rel
		return nil
	}
	for _, f := range p.Files {
		if err := check(f.Rel, "文件"); err != nil {
			return err
		}
	}
	for _, c := range p.Copies {
		if _, err := cleanRel(c.Src); err != nil {
			return fmt.Errorf("exporter: 图片源路径非法: %w", err)
		}
		if err := check(c.Dst, "图片"); err != nil {
			return err
		}
	}
	return nil
}

// cleanRel validates a slash- or OS-separated relative path stays inside its
// root, mirroring ir's path rules.
func cleanRel(rel string) (string, error) {
	if strings.TrimSpace(rel) == "" {
		return "", errors.New("路径为空")
	}
	if filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" || path.IsAbs(rel) {
		return "", fmt.Errorf("%q 必须是相对路径", rel)
	}
	slashed := path.Clean(filepath.ToSlash(rel))
	if slashed == "." || slashed == ".." || strings.HasPrefix(slashed, "../") {
		return "", fmt.Errorf("%q 越出目标根目录", rel)
	}
	return slashed, nil
}

const (
	// manifestName is the manifest file written at the export root.
	manifestName = "mylabel-export.json"
	tmpSuffix    = ".mylabel-tmp"
	copyBufSize  = 1 << 20
)

// WriteExport assembles plan into outDir atomically (docs/phase4-design.md
// §2.2): it writes into outDir+".mylabel-tmp" and renames on success. An
// existing outDir is an error — exports never merge or overwrite. progress,
// when non-nil, is called after every copied image, written file, and the
// manifest, with total == len(Copies)+len(Files)+1.
func WriteExport(outDir, imgRoot string, plan *Plan, progress func(done, total int)) error {
	if plan == nil {
		return errors.New("exporter: 计划为 nil")
	}
	if strings.TrimSpace(outDir) == "" {
		return errors.New("exporter: 输出目录为空")
	}
	if _, err := os.Stat(outDir); err == nil {
		return fmt.Errorf("exporter: 输出目录已存在: %s", outDir)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("exporter: 检查输出目录失败: %w", err)
	}

	tmp := outDir + tmpSuffix
	if err := os.RemoveAll(tmp); err != nil {
		return fmt.Errorf("exporter: 清理临时目录失败: %w", err)
	}
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return fmt.Errorf("exporter: 创建临时目录失败: %w", err)
	}
	cleanup := func(err error) error {
		_ = os.RemoveAll(tmp)
		return err
	}

	total := len(plan.Copies) + len(plan.Files) + 1
	done := 0
	tick := func() {
		done++
		if progress != nil {
			progress(done, total)
		}
	}

	buf := make([]byte, copyBufSize)
	for _, c := range plan.Copies {
		src, err := cleanRel(c.Src)
		if err != nil {
			return cleanup(fmt.Errorf("exporter: 图片源路径非法: %w", err))
		}
		dst, err := cleanRel(c.Dst)
		if err != nil {
			return cleanup(fmt.Errorf("exporter: 图片目标路径非法: %w", err))
		}
		if err := copyFile(filepath.Join(imgRoot, filepath.FromSlash(src)), filepath.Join(tmp, filepath.FromSlash(dst)), buf); err != nil {
			return cleanup(err)
		}
		tick()
	}
	for _, f := range plan.Files {
		rel, err := cleanRel(f.Rel)
		if err != nil {
			return cleanup(fmt.Errorf("exporter: 文件路径非法: %w", err))
		}
		target := filepath.Join(tmp, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return cleanup(fmt.Errorf("exporter: 创建目录失败: %w", err))
		}
		if err := os.WriteFile(target, f.Data, 0o644); err != nil {
			return cleanup(fmt.Errorf("exporter: 写入 %s 失败: %w", rel, err))
		}
		tick()
	}

	manifest, err := marshalManifest(plan)
	if err != nil {
		return cleanup(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, manifestName), manifest, 0o644); err != nil {
		return cleanup(fmt.Errorf("exporter: 写入 manifest 失败: %w", err))
	}
	tick()

	if err := os.Rename(tmp, outDir); err != nil {
		return cleanup(fmt.Errorf("exporter: 改名 %s 失败: %w", outDir, err))
	}
	return nil
}

// copyFile streams src to dst, creating dst's parent directories.
func copyFile(src, dst string, buf []byte) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("exporter: 打开图片 %s 失败: %w", src, err)
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("exporter: 创建目录失败: %w", err)
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("exporter: 创建 %s 失败: %w", dst, err)
	}
	if _, err := io.CopyBuffer(out, in, buf); err != nil {
		_ = out.Close()
		return fmt.Errorf("exporter: 复制 %s 失败: %w", src, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("exporter: 写入 %s 失败: %w", dst, err)
	}
	return nil
}
