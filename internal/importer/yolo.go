package importer

import (
	"fmt"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"mylabel/internal/ir"
)

// maxYOLOClassID bounds synthesized class ids so a corrupt label file cannot
// make us allocate millions of categories.
const maxYOLOClassID = 65535

// yoloTaskAuto is Options.Task's "guess from the first data line" mode; it is
// an importer-level option, not an ir.Project.Task value.
const yoloTaskAuto = "auto"

// yoloImage is one image's parse result before category keys are known.
type yoloImage struct {
	file string // slash-relative image path (the dims key)
	w, h int
	anns []yoloAnn
}

// yoloAnn is one parsed label line with normalized values already denormalized
// to pixels.
type yoloAnn struct {
	class int
	kind  ir.Kind
	box   [4]float64   // bbox: x, y, w, h
	pts   [][2]float64 // polygon ring or rotated quad
}

// maxLabelsSearchDepth bounds the hunt for a labels/ subdirectory so that a
// flat labels tree (the backward-compatible "you passed the labels dir" case)
// does not trigger a full-tree walk.
const maxLabelsSearchDepth = 3

// findLabelsDir resolves which directory actually holds the label files. A
// directory containing a labels/ subdirectory is an Ultralytics dataset root
// (<root>/images/... + <root>/labels/...), and the shallowest such labels
// directory wins (lexically first on ties). Without one the argument is
// already the labels dir.
func findLabelsDir(base string) string {
	direct := filepath.Join(base, "labels")
	if st, err := os.Stat(direct); err == nil && st.IsDir() {
		return direct
	}
	best, bestDepth := "", 0
	_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() || p == base {
			return nil
		}
		rel, relErr := filepath.Rel(base, p)
		if relErr != nil {
			return filepath.SkipDir
		}
		depth := strings.Count(filepath.ToSlash(rel), "/")
		if strings.EqualFold(d.Name(), "labels") {
			if best == "" || depth < bestDepth {
				best, bestDepth = p, depth
			}
			if depth == 1 {
				return fs.SkipAll // cannot get shallower than a direct child
			}
			return filepath.SkipDir
		}
		if depth >= maxLabelsSearchDepth {
			return filepath.SkipDir
		}
		return nil
	})
	return best
}

// yoloSuffixHit is one suffix of a dims stem together with how many stems
// share it; only count==1 is usable for matching.
type yoloSuffixHit struct {
	entry dimEntry
	count int
}

// yoloSuffixIndex maps every trailing path suffix of every dims stem to its
// entry, so a label path can still be matched when dims are keyed from a
// parent of the dataset directory (coco8/images/train/a.jpg for
// labels/train/a.txt). Keys are lowercased: the Windows filesystem is
// case-insensitive, and the uniqueness rule below keeps a case-only collision
// from matching anything.
func yoloSuffixIndex(byStem map[string]dimEntry) map[string]yoloSuffixHit {
	out := make(map[string]yoloSuffixHit, len(byStem))
	for stem, entry := range byStem {
		segs := strings.Split(stem, "/")
		for i := range segs {
			key := strings.ToLower(strings.Join(segs[i:], "/"))
			hit := out[key]
			hit.count++
			if hit.count == 1 {
				hit.entry = entry
			}
			out[key] = hit
		}
	}
	return out
}

// yoloUniqueSuffix is the last-resort match: a candidate that is a trailing
// path suffix of exactly one dims entry. Ambiguity yields no match, so an
// unrelated image tree can never be attached by guesswork.
func yoloUniqueSuffix(bySuffix map[string]yoloSuffixHit, candidates []string) (dimEntry, bool) {
	for _, c := range candidates {
		if hit, ok := bySuffix[strings.ToLower(c)]; ok && hit.count == 1 {
			return hit.entry, true
		}
	}
	return dimEntry{}, false
}

// yoloMatchStems lists the dims stems a label file may correspond to, in
// priority order:
//
//  1. its own path under the labels dir (mirrored trees, or dims relative to
//     the labels dir itself);
//  2. the Ultralytics mirror of its path relative to the directory the caller
//     opened: the last "labels" segment becomes "images" (labels/train/a.txt
//     -> images/train/a);
//  3. "images/" + its path, for a caller who pointed straight at the labels
//     dir while dims are keyed from its parent (images/train/a.jpg);
//  4. its bare file name, for a workspace opened inside the image tree
//     (images/train/a.jpg is the dims key "a.jpg").
//
// Candidates 1-3 may be looked up by exact stem; 2-4 only make sense through
// the unique-suffix index, which is where the bare name gets its uniqueness
// check grouped by file name rather than by full path suffix.
func yoloMatchStems(relToLabels, relToBase string) []string {
	out := []string{yoloStem(relToLabels)}
	add := func(s string) {
		for _, have := range out {
			if have == s {
				return
			}
		}
		out = append(out, s)
	}
	if mirrored, ok := mirrorLabelsPath(relToBase); ok {
		add(yoloStem(mirrored))
	}
	add("images/" + yoloStem(relToLabels))
	add(yoloStem(path.Base(relToLabels)))
	return out
}

// mirrorLabelsPath swaps the last "labels" path segment for "images".
func mirrorLabelsPath(rel string) (string, bool) {
	segs := strings.Split(filepath.ToSlash(rel), "/")
	for i := len(segs) - 1; i >= 0; i-- {
		if strings.EqualFold(segs[i], "labels") {
			segs[i] = "images"
			return strings.Join(segs, "/"), true
		}
	}
	return "", false
}

// dimEntry is the resolved image size for one label file.
type dimEntry struct {
	file string
	w, h int
}

// parseYOLO implements ParseYOLO: dataset root or labels dir in, one image per
// txt with Ultralytics labels<->images mirroring, class names from the
// dataset's yaml descriptor.
func parseYOLO(labelsDir string, dims map[string][2]int, opts Options) (*Dataset, []ir.Issue, error) {
	baseDir := filepath.Clean(labelsDir)
	st, err := os.Stat(baseDir)
	if err != nil {
		return nil, nil, fmt.Errorf("importer: yolo: %w", err)
	}
	if !st.IsDir() {
		return nil, nil, fmt.Errorf("importer: yolo: %s 不是目录", baseDir)
	}
	labels := findLabelsDir(baseDir)
	if labels == "" {
		labels = baseDir
	}
	task, err := yoloTask(opts.Task)
	if err != nil {
		return nil, nil, err
	}

	names := yoloNamesFor(labels)
	namesMax := -1
	for id := range names {
		if id > namesMax {
			namesMax = id
		}
	}
	byStem := yoloDimsByStem(dims)
	bySuffix := yoloSuffixIndex(byStem)

	var (
		issues []ir.Issue
		images []yoloImage
		maxID  = -1
	)
	walkErr := filepath.WalkDir(labels, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(p), ".txt") {
			return nil
		}
		rel, relErr := filepath.Rel(labels, p)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		relToBase, baseErr := filepath.Rel(baseDir, p)
		if baseErr != nil {
			relToBase = rel
		}
		var (
			entry dimEntry
			ok    bool
		)
		stemCandidates := yoloMatchStems(rel, filepath.ToSlash(relToBase))
		for _, stem := range stemCandidates {
			if entry, ok = byStem[stem]; ok {
				break
			}
		}
		if !ok {
			entry, ok = yoloUniqueSuffix(bySuffix, stemCandidates)
		}
		if !ok {
			issues = append(issues, ir.Issue{
				File:    rel,
				Index:   -1,
				Message: "找不到对应的图片尺寸（dims），已跳过该文件",
			})
			return nil
		}
		data, readErr := os.ReadFile(p)
		if readErr != nil {
			issues = append(issues, ir.Issue{
				File:    rel,
				Index:   -1,
				Message: "读取失败，已跳过该文件：" + readErr.Error(),
			})
			return nil
		}
		img := yoloImage{file: entry.file, w: entry.w, h: entry.h}
		content := strings.TrimPrefix(string(data), "\ufeff")
		for i, raw := range strings.Split(content, "\n") {
			line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
			if line == "" {
				continue
			}
			if task == yoloTaskAuto {
				task = guessYOLOTask(line)
			}
			ann, issue := parseYOLOLine(line, i+1, rel, task, entry, names, namesMax)
			if issue != nil {
				issues = append(issues, *issue)
				continue
			}
			if ann.class > maxID {
				maxID = ann.class
			}
			img.anns = append(img.anns, ann)
		}
		images = append(images, img)
		return nil
	})
	if walkErr != nil {
		return nil, nil, fmt.Errorf("importer: yolo: %w", walkErr)
	}

	dataset := &Dataset{}
	classKey := make(map[int]string)
	if names != nil {
		used := make(map[string]string, len(names))
		for id := 0; id <= namesMax; id++ {
			name := names[id]
			if name == "" {
				name = fmt.Sprintf("class_%d", id)
			}
			if key, dup := used[name]; dup {
				classKey[id] = key
				continue
			}
			used[name] = name
			classKey[id] = name
			dataset.Categories = append(dataset.Categories, ir.Category{Key: name, Index: id, Name: name})
		}
	} else if maxID >= 0 {
		for id := 0; id <= maxID; id++ {
			name := fmt.Sprintf("class_%d", id)
			classKey[id] = name
			dataset.Categories = append(dataset.Categories, ir.Category{Key: name, Index: id, Name: name})
		}
	}

	for _, yi := range images {
		img := ir.Image{File: yi.file, Width: yi.w, Height: yi.h, Annotations: []ir.Annotation{}}
		for i, a := range yi.anns {
			var ann ir.Annotation
			switch a.kind {
			case ir.KindBBox:
				ann = ir.Annotation{Kind: a.kind, X: a.box[0], Y: a.box[1], W: a.box[2], H: a.box[3]}
			case ir.KindPolygon:
				ann = ir.Annotation{Kind: a.kind, Rings: [][][2]float64{a.pts}}
			case ir.KindRotated:
				ann = rotatedFromPoints(a.pts)
			}
			ann.ID = strconv.Itoa(i + 1)
			ann.Category = classKey[a.class]
			img.Annotations = append(img.Annotations, ann)
		}
		dataset.Images = append(dataset.Images, img)
	}
	if task == yoloTaskAuto {
		task = ir.TaskDet // no data line at all: nothing to guess from
	}
	dataset.Warnings = normalImages(task, dataset.Categories, dataset.Images, dataset.Warnings)
	return dataset, issues, nil
}

// yoloTask normalizes opts.Task into yoloTaskAuto/Det/Seg/Obb.
func yoloTask(s string) (string, error) {
	switch t := strings.ToLower(strings.TrimSpace(s)); t {
	case "", yoloTaskAuto:
		return yoloTaskAuto, nil
	case ir.TaskDet, ir.TaskSeg, ir.TaskObb:
		return t, nil
	default:
		return "", fmt.Errorf("importer: yolo: 未知的 task %q（可选 auto/det/seg/obb）", s)
	}
}

// guessYOLOTask applies the auto rule: 5 fields det, 9 fields obb, else seg.
func guessYOLOTask(line string) string {
	switch len(strings.Fields(line)) {
	case 5:
		return ir.TaskDet
	case 9:
		return ir.TaskObb
	default:
		return ir.TaskSeg
	}
}

// yoloDimsByStem indexes dims by extension-less slash path so a label file
// matches any image extension. Colliding keys resolve to the
// lexicographically first image path, keeping the outcome deterministic.
func yoloDimsByStem(dims map[string][2]int) map[string]dimEntry {
	keys := make([]string, 0, len(dims))
	for k := range dims {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]dimEntry, len(keys))
	for _, k := range keys {
		rel := yoloCleanRel(k)
		stem := yoloStem(rel)
		if _, dup := out[stem]; dup {
			continue
		}
		out[stem] = dimEntry{file: rel, w: dims[k][0], h: dims[k][1]}
	}
	return out
}

// yoloCleanRel normalizes a relative path to slash form without a leading "./".
func yoloCleanRel(p string) string {
	p = filepath.ToSlash(p)
	p = strings.TrimPrefix(p, "./")
	return p
}

// yoloStem drops the extension of a slash path.
func yoloStem(p string) string {
	return strings.TrimSuffix(p, path.Ext(p))
}

// parseYOLOLine parses one non-empty label line for the given task.
func parseYOLOLine(line string, ln int, file, task string, de dimEntry, names map[int]string, namesMax int) (yoloAnn, *ir.Issue) {
	issue := func(field, format string, args ...any) (yoloAnn, *ir.Issue) {
		return yoloAnn{}, &ir.Issue{
			File:    file,
			Line:    ln,
			Index:   -1,
			Field:   field,
			Message: fmt.Sprintf(format, args...),
		}
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return issue("", "第 %d 行：只有 %d 个字段，至少需要 2 个（class 加坐标）", ln, len(fields))
	}
	id, convErr := strconv.Atoi(fields[0])
	if convErr != nil || id < 0 {
		return issue("class", "第 %d 行：类别 id %q 不是非负整数", ln, fields[0])
	}
	if names != nil {
		if id > namesMax {
			return issue("class", "第 %d 行：类别 id %d 超出 names 范围（0..%d）", ln, id, namesMax)
		}
	} else if id > maxYOLOClassID {
		return issue("class", "第 %d 行：类别 id %d 过大（上限 %d）", ln, id, maxYOLOClassID)
	}

	switch task {
	case ir.TaskDet:
		if len(fields) != 5 {
			return issue("", "第 %d 行：det 期望 5 字段，实际 %d", ln, len(fields))
		}
		vals, bad := parseYOLOFloats(fields[1:], ln, file, []string{"cx", "cy", "w", "h"})
		if bad != nil {
			return yoloAnn{}, bad
		}
		w, h := float64(de.w), float64(de.h)
		return yoloAnn{
			class: id,
			kind:  ir.KindBBox,
			box: [4]float64{
				(vals[0] - vals[2]/2) * w,
				(vals[1] - vals[3]/2) * h,
				vals[2] * w,
				vals[3] * h,
			},
		}, nil

	case ir.TaskObb:
		if len(fields) != 9 {
			return issue("", "第 %d 行：obb 期望 9 字段（4 个归一化点），实际 %d", ln, len(fields))
		}
		pts, bad := parseYOLOPoints(fields[1:], ln, file, de)
		if bad != nil {
			return yoloAnn{}, bad
		}
		return yoloAnn{class: id, kind: ir.KindRotated, pts: pts}, nil

	case ir.TaskSeg:
		if len(fields) < 7 || len(fields)%2 == 0 {
			return issue("", "第 %d 行：seg 期望 1+2n 字段（n≥3，至少 7），实际 %d", ln, len(fields))
		}
		pts, bad := parseYOLOPoints(fields[1:], ln, file, de)
		if bad != nil {
			return yoloAnn{}, bad
		}
		return yoloAnn{class: id, kind: ir.KindPolygon, pts: pts}, nil

	default:
		return issue("", "第 %d 行：未知的 task %q", ln, task)
	}
}

// parseYOLOFloats parses coordinate fields, naming the offending one.
func parseYOLOFloats(fields []string, ln int, file string, labels []string) ([]float64, *ir.Issue) {
	vals := make([]float64, len(fields))
	for i, f := range fields {
		v, err := strconv.ParseFloat(f, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			label := "值"
			if i < len(labels) {
				label = labels[i]
			}
			return nil, &ir.Issue{
				File:    file,
				Line:    ln,
				Index:   -1,
				Field:   label,
				Message: fmt.Sprintf("第 %d 行：字段 %s=%q 不是有效数字", ln, label, f),
			}
		}
		vals[i] = v
	}
	return vals, nil
}

// parseYOLOPoints parses normalized x,y pairs and denormalizes them.
func parseYOLOPoints(fields []string, ln int, file string, de dimEntry) ([][2]float64, *ir.Issue) {
	labels := make([]string, len(fields))
	for i := range labels {
		labels[i] = fmt.Sprintf("x%d", i/2+1)
		if i%2 == 1 {
			labels[i] = fmt.Sprintf("y%d", i/2+1)
		}
	}
	vals, bad := parseYOLOFloats(fields, ln, file, labels)
	if bad != nil {
		return nil, bad
	}
	pts := make([][2]float64, 0, len(vals)/2)
	for i := 0; i+1 < len(vals); i += 2 {
		pts = append(pts, [2]float64{vals[i] * float64(de.w), vals[i+1] * float64(de.h)})
	}
	return pts, nil
}

// rotatedFromPoints builds a rotated_bbox from its four vertices and fills the
// derived cache (ir.Normalize recomputes it canonically).
func rotatedFromPoints(pts [][2]float64) ir.Annotation {
	ann := ir.Annotation{Kind: ir.KindRotated, Points: pts}
	if len(pts) != 4 {
		return ann
	}
	ann.CX = (pts[0][0] + pts[1][0] + pts[2][0] + pts[3][0]) / 4
	ann.CY = (pts[0][1] + pts[1][1] + pts[2][1] + pts[3][1]) / 4
	edge := func(i, j int) float64 {
		return math.Hypot(pts[j][0]-pts[i][0], pts[j][1]-pts[i][1])
	}
	ann.RW = (edge(0, 1) + edge(3, 2)) / 2
	ann.RH = (edge(1, 2) + edge(0, 3)) / 2
	ann.Angle = math.Atan2(pts[1][1]-pts[0][1], pts[1][0]-pts[0][0])
	return ann
}

// normalImages runs ir.Normalize over every parsed image, appending its
// warnings to warnings.
func normalImages(task string, cats []ir.Category, images []ir.Image, warnings []ir.Issue) []ir.Issue {
	project := &ir.Project{IRVersion: ir.Version, Task: task, Categories: cats}
	for i := range images {
		warnings = append(warnings, ir.Normalize(project, &images[i])...)
	}
	return warnings
}
