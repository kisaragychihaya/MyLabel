package exporter

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"mylabel/internal/ir"
)

// buildYOLOPlan builds the Ultralytics dataset plan
// (docs/phase4-design.md §5): images/<split>/<rel>, mirrored
// labels/<split>/<rel>.txt (one object per line, class 0-based, normalized
// to [0,1] after clipping, 6-decimal trimmed formatting), and a
// hand-concatenated data.yaml (ordered names list, single-quote escaping).
// Images without annotations get no txt. Lossy conversions follow the
// per-task matrix and are counted via recorder.count; annotations that name
// an unknown category or clip away entirely are dropped (RuleDropped).
// Attributes have no YOLO field: one summary recorder.warn per export.
//
// plan.Categories must carry Index == position (0-based class id).
// plan.Files must contain data.yaml; plan.Splits/plan.Opts are filled by
// BuildPlan, not here.
func buildYOLOPlan(proj *ir.Project, images []ir.Image, splits map[string]string, opts Options) (*Plan, error) {
	cats, index := planCategories(proj.Categories)
	rec := newRecorder()
	plan := &Plan{Categories: cats}
	plan.Report.Images = len(images)

	hasVal := false
	for _, s := range splits {
		if s == "val" {
			hasVal = true
			break
		}
	}

	attrs := 0
	for _, img := range sortedImages(images) {
		split := splitOf(splits, img.File)
		rel := exportRel(slashRel(img.File))
		plan.Copies = append(plan.Copies, ImageCopy{
			Src: img.File,
			Dst: "images/" + split + "/" + rel,
		})

		var b strings.Builder
		written := 0
		for _, a := range img.Annotations {
			if img.Width <= 0 || img.Height <= 0 {
				plan.Report.AnnotationsDropped++
				rec.count(RuleDropped, img.File, a.ID, "图片尺寸无效，无法归一化")
				continue
			}
			cls, known := index[a.Category]
			if !known {
				plan.Report.AnnotationsDropped++
				rec.count(RuleDropped, img.File, a.ID, fmt.Sprintf("未知类别 %q", a.Category))
				continue
			}
			cv := yoloConvert(a, opts.Task, img.Width, img.Height)
			if cv.drop != "" {
				plan.Report.AnnotationsDropped++
				rec.count(RuleDropped, img.File, a.ID, cv.drop)
				continue
			}
			if cv.lossy != "" {
				rec.count(cv.lossy, img.File, a.ID, ruleMessage(cv.lossy))
			}
			if cv.clipped {
				rec.count(RuleClipped, img.File, a.ID, ruleMessage(RuleClipped))
			}
			b.WriteString(yoloLine(cls, normalizeYOLO(cv.vals, img.Width, img.Height)))
			written++
			if len(a.Attributes) > 0 {
				attrs++
			}
		}

		plan.Report.Annotations += written
		if written == 0 {
			continue
		}
		plan.Report.ImagesWithAnns++
		plan.Files = append(plan.Files, OutFile{
			Rel:  "labels/" + split + "/" + replaceExt(rel, ".txt"),
			Data: []byte(b.String()),
		})
	}

	if attrs > 0 {
		rec.warn(fmt.Sprintf("%d 条标注的属性不随 YOLO 导出", attrs))
	}
	plan.Files = append(plan.Files, OutFile{
		Rel:  "data.yaml",
		Data: yoloDataYAML(cats, hasVal),
	})
	plan.Report.Lossy = rec.rep.Lossy
	plan.Report.Warnings = rec.rep.Warnings
	return plan, nil
}

// yoloConv is one annotation converted to pixel-space YOLO fields.
type yoloConv struct {
	// vals are pixel-space values: det is cx, cy, w, h; seg/obb are
	// alternating x, y vertices (points reports which).
	vals    []float64
	points  bool
	lossy   string // lossy rule that fired, "" when the conversion is exact
	clipped bool
	drop    string // non-empty: the annotation cannot be converted
}

// yoloConvert projects one annotation onto the task's geometry
// (docs/phase4-design.md §5 conversion matrix).
func yoloConvert(a ir.Annotation, task Task, iw, ih int) yoloConv {
	switch task {
	case TaskDet:
		return yoloDet(a, iw, ih)
	case TaskSeg:
		return yoloSeg(a, iw, ih)
	case TaskObb:
		return yoloObb(a, iw, ih)
	}
	return yoloConv{drop: fmt.Sprintf("未知的 YOLO 任务 %q", task)}
}

// yoloDet converts to cx, cy, w, h via the enclosing axis-aligned box.
func yoloDet(a ir.Annotation, iw, ih int) yoloConv {
	var (
		x, y, w, h float64
		cv         yoloConv
	)
	switch a.Kind {
	case ir.KindBBox:
		x, y, w, h = a.X, a.Y, a.W, a.H
	case ir.KindRotated:
		quad, ok := quadOf(a)
		if !ok {
			return yoloConv{drop: "旋转框既无顶点也无派生缓存"}
		}
		x, y, w, h = aabbOfPoints(quad)
		cv.lossy = RuleObbToDet
	case ir.KindPolygon:
		rings := nonEmptyRings(a.Rings)
		if len(rings) == 0 {
			return yoloConv{drop: "多边形没有顶点"}
		}
		x, y, w, h = aabbOfRings(rings)
		cv.lossy = RuleSegToDet
	default:
		return yoloConv{drop: fmt.Sprintf("不支持的几何类型 %q", a.Kind)}
	}
	if !(w > 0) || !(h > 0) {
		return yoloConv{drop: "外接框为空"}
	}
	nx, ny, nw, nh, clipped := clipBBox(x, y, w, h, iw, ih)
	cv.clipped = clipped
	if !(nw > 0) || !(nh > 0) {
		return yoloConv{drop: "外接框裁剪后为空"}
	}
	cv.vals = []float64{nx + nw/2, ny + nh/2, nw, nh}
	return cv
}

// yoloSeg converts to one closed ring of at least three vertices.
func yoloSeg(a ir.Annotation, iw, ih int) yoloConv {
	var (
		pts [][2]float64
		cv  yoloConv
	)
	switch a.Kind {
	case ir.KindBBox:
		if !(a.W > 0) || !(a.H > 0) {
			return yoloConv{drop: "矩形为空"}
		}
		pts = rectCorners(a.X, a.Y, a.W, a.H)
		cv.lossy = RuleDetToSeg
	case ir.KindRotated:
		quad, ok := quadOf(a)
		if !ok {
			return yoloConv{drop: "旋转框既无顶点也无派生缓存"}
		}
		pts = orderQuadPts(quad)
		cv.lossy = RuleObbToSeg
	case ir.KindPolygon:
		rings := nonEmptyRings(a.Rings)
		if len(rings) == 0 {
			return yoloConv{drop: "多边形没有顶点"}
		}
		if len(rings) > 1 {
			cv.lossy = RuleMultiRing
		}
		pts = bridgeRings(rings)
	default:
		return yoloConv{drop: fmt.Sprintf("不支持的几何类型 %q", a.Kind)}
	}
	clipped, moved := clipPoints(pts, iw, ih)
	if moved {
		cv.clipped = true
	}
	if len(clipped) < 3 || ringAbsArea(clipped) <= 1e-9 {
		return yoloConv{drop: "多边形裁剪后不足三点或面积为零"}
	}
	cv.vals = flattenPts(clipped)
	cv.points = true
	return cv
}

// yoloObb converts to four vertices in YOLO's canonical quad order.
func yoloObb(a ir.Annotation, iw, ih int) yoloConv {
	var (
		pts [][2]float64
		cv  yoloConv
	)
	switch a.Kind {
	case ir.KindBBox:
		if !(a.W > 0) || !(a.H > 0) {
			return yoloConv{drop: "矩形为空"}
		}
		pts = rectCorners(a.X, a.Y, a.W, a.H)
		cv.lossy = RuleDetToObb
	case ir.KindRotated:
		quad, ok := quadOf(a)
		if !ok {
			return yoloConv{drop: "旋转框既无顶点也无派生缓存"}
		}
		pts = quad
	case ir.KindPolygon:
		rings := nonEmptyRings(a.Rings)
		if len(rings) == 0 {
			return yoloConv{drop: "多边形没有顶点"}
		}
		var all [][2]float64
		for _, r := range rings {
			all = append(all, r...)
		}
		cx, cy, w, h, angle := minAreaRect(all)
		if !(w > 0) || !(h > 0) {
			return yoloConv{drop: "多边形的最小外接旋转矩形退化"}
		}
		pts = quadFromParamsPts(cx, cy, w, h, angle)
		cv.lossy = RuleSegToObb
	default:
		return yoloConv{drop: fmt.Sprintf("不支持的几何类型 %q", a.Kind)}
	}
	clipped, moved := clipPoints(orderQuadPts(pts), iw, ih)
	if moved {
		cv.clipped = true
	}
	if len(clipped) < 3 || ringAbsArea(clipped) <= 1e-9 {
		return yoloConv{drop: "四点裁剪后面积为零"}
	}
	cv.vals = flattenPts(clipped)
	cv.points = true
	return cv
}

// normalizeYOLO maps pixel values onto [0,1]: even indices are x (divided by
// the image width), odd indices y (divided by the image height).
func normalizeYOLO(vals []float64, iw, ih int) []float64 {
	out := make([]float64, len(vals))
	for i, v := range vals {
		if i%2 == 0 {
			out[i] = v / float64(iw)
		} else {
			out[i] = v / float64(ih)
		}
	}
	return out
}

// yoloLine renders one label line: the class id followed by the formatted
// values. Every line ends with a newline.
func yoloLine(cls int, vals []float64) string {
	var b strings.Builder
	b.WriteString(strconv.Itoa(cls))
	for _, v := range vals {
		b.WriteByte(' ')
		b.WriteString(formatNorm(v))
	}
	b.WriteByte('\n')
	return b.String()
}

// formatNorm renders one normalized coordinate: clamped to [0,1], rounded to
// six decimals, trailing zeros trimmed.
func formatNorm(v float64) string {
	if math.IsNaN(v) {
		return "0"
	}
	r := math.Round(clampF(v, 0, 1)*1e6) / 1e6
	if r == 0 {
		return "0"
	}
	return strconv.FormatFloat(r, 'f', -1, 64)
}

// yoloDataYAML hand-concatenates data.yaml (no YAML dependency allowed).
// There is no per-export root path in Plan, so the optional `path:` key is
// omitted: Ultralytics falls back to the yaml file's own directory, which is
// exactly the export root.
func yoloDataYAML(cats []ir.Category, hasVal bool) []byte {
	var b strings.Builder
	b.WriteString("train: images/train\n")
	if hasVal {
		b.WriteString("val: images/val\n")
	}
	if len(cats) == 0 {
		b.WriteString("names: []\n")
		return []byte(b.String())
	}
	b.WriteString("names:\n")
	for _, c := range cats {
		b.WriteString("  - ")
		b.WriteString(yamlScalar(c.Name))
		b.WriteString("\n")
	}
	return []byte(b.String())
}

// yamlScalar renders one name as a safe single-line YAML scalar: anything
// that could be read as another type or would break the flow of the block
// sequence is single-quoted, with embedded quotes doubled.
func yamlScalar(s string) string {
	if s == "" {
		return "''"
	}
	if strings.TrimSpace(s) != s || yamlNeedsQuote(s) {
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	return s
}

// yamlNeedsQuote reports whether s must be quoted to stay a plain string.
func yamlNeedsQuote(s string) bool {
	switch strings.ToLower(s) {
	case "true", "false", "yes", "no", "on", "off", "null", "~":
		return true
	}
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return true
	}
	if strings.ContainsAny(s, ":#'\"[]{},&*!|>%@`\t") {
		return true
	}
	return strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "? ")
}

// rectCorners returns a bbox's four vertices in the canonical quad order
// (positive shoelace in image coordinates, starting at the top-left corner).
func rectCorners(x, y, w, h float64) [][2]float64 {
	return [][2]float64{{x, y}, {x + w, y}, {x + w, y + h}, {x, y + h}}
}

// flattenPts renders a ring as alternating x, y values.
func flattenPts(pts [][2]float64) []float64 {
	out := make([]float64, 0, len(pts)*2)
	for _, p := range pts {
		out = append(out, p[0], p[1])
	}
	return out
}

// slashRel normalises an image's relative path to forward slashes.
func slashRel(rel string) string {
	return strings.ReplaceAll(rel, "\\", "/")
}

// replaceExt swaps rel's extension for ext.
func replaceExt(rel, ext string) string {
	if i := strings.LastIndexByte(rel, '.'); i > strings.LastIndexByte(rel, '/') {
		return rel[:i] + ext
	}
	return rel + ext
}
