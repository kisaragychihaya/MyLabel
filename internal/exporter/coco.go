package exporter

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"mylabel/internal/ir"
)

// COCO document shapes (docs/phase4-design.md §6). Everything is emitted with
// encoding/json's compact form; the two mylabel_* keys are private extensions
// and are declared once through recorder.warn.
type cocoJSONDoc struct {
	Info        cocoJSONInfo         `json:"info"`
	Images      []cocoJSONImage      `json:"images"`
	Annotations []cocoJSONAnnotation `json:"annotations"`
	Categories  []cocoJSONCategory   `json:"categories"`
}

// cocoJSONInfo carries no date_generated on purpose: exports stay byte-for-byte
// reproducible.
type cocoJSONInfo struct {
	Description string `json:"description"`
	Version     string `json:"version"`
}

type cocoJSONImage struct {
	ID       int    `json:"id"`
	FileName string `json:"file_name"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

type cocoJSONCategory struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	Supercategory string `json:"supercategory,omitempty"`
}

type cocoJSONAnnotation struct {
	ID           int               `json:"id"`
	ImageID      int               `json:"image_id"`
	CategoryID   int               `json:"category_id"`
	BBox         []float64         `json:"bbox"`
	Area         float64           `json:"area"`
	Segmentation [][]float64       `json:"segmentation"`
	ISCrowd      int               `json:"iscrowd"`
	OBB          *cocoJSONOBB      `json:"mylabel_obb,omitempty"`
	Attributes   map[string]string `json:"mylabel_attributes,omitempty"`
}

// cocoJSONOBB is the private rotated-box extension: the four vertices (the
// authoritative geometry) plus the angle IR keeps for them, in radians.
type cocoJSONOBB struct {
	Points [][2]float64 `json:"points"`
	Angle  float64      `json:"angle"`
}

// cocoNonstandardNote is the single warning recorded per COCO export.
const cocoNonstandardNote = "非标准扩展：mylabel_obb（旋转框四点+角度）与 mylabel_attributes（标注属性）不在 COCO 规范内，通用 COCO 工具会忽略"

// buildCOCOPlan builds per-split COCO JSON (docs/phase4-design.md §6):
// images/<split>/<rel> plus annotations/instances_<split>.json for every
// non-empty split. Compact encoding/json output. Per file: info
// {description, version}, categories with id = IR COCOID when set else
// 1-based position, images id from 1 in sorted-File order, annotations id
// from 1. bbox is absolute pixels [x,y,w,h] top-left after clipping, area =
// w*h, iscrowd = 0. Polygons keep all rings as segmentation parts; bboxes
// add a four-corner segmentation (RuleDetToSeg); rotated boxes become a
// four-point segmentation + enclosing bbox + private "mylabel_obb"
// extension {points, angle} (RuleObbToCOCO). Attributes pass through as
// "mylabel_attributes" (omitempty); the non-standard extensions are
// declared once via recorder.warn. Unknown categories or annotations that
// clip away are dropped (RuleDropped).
func buildCOCOPlan(proj *ir.Project, images []ir.Image, splits map[string]string, opts Options) (*Plan, error) {
	rec := newRecorder()
	rec.warn(cocoNonstandardNote)

	categories, catID := cocoEffectiveCategories(proj.Categories)
	plan := &Plan{Categories: categories}

	// Copy order and per-file image numbering follow the sorted File order so
	// that the same input always yields the same Plan.
	sorted := make([]ir.Image, len(images))
	copy(sorted, images)
	slices.SortFunc(sorted, func(a, b ir.Image) int { return cmp.Compare(a.File, b.File) })

	bySplit := make(map[string][]ir.Image, 2)
	var splitNames []string
	for _, img := range sorted {
		sp := splits[img.File]
		if sp == "" {
			sp = "train"
		}
		if _, seen := bySplit[sp]; !seen {
			splitNames = append(splitNames, sp)
		}
		bySplit[sp] = append(bySplit[sp], img)
		plan.Copies = append(plan.Copies, ImageCopy{
			Src: filepath.ToSlash(img.File),
			Dst: path.Join("images", sp, exportRel(img.File)),
		})
	}
	slices.Sort(splitNames)

	description := strings.TrimSpace(proj.Name)
	if description == "" {
		description = "mylabel export"
	}
	catJSON := cocoJSONCategories(categories)

	var emitted, dropped, withAnns int
	for _, sp := range splitNames {
		group := bySplit[sp]
		// A split without images gets no file at all (splitNames only holds
		// splits that have members), but every file always carries arrays.
		doc := cocoJSONDoc{
			Info:        cocoJSONInfo{Description: description, Version: ir.Version},
			Images:      make([]cocoJSONImage, 0, len(group)),
			Annotations: []cocoJSONAnnotation{},
			Categories:  catJSON,
		}
		annID := 0
		for i, img := range group {
			imageID := i + 1
			doc.Images = append(doc.Images, cocoJSONImage{
				ID:       imageID,
				FileName: exportRel(img.File),
				Width:    img.Width,
				Height:   img.Height,
			})
			kept := 0
			for _, a := range img.Annotations {
				out, ok := cocoConvertAnnotation(img, a, catID, imageID, annID+1, rec)
				if !ok {
					dropped++
					continue
				}
				annID++
				doc.Annotations = append(doc.Annotations, out)
				kept++
			}
			emitted += kept
			if kept > 0 {
				withAnns++
			}
		}
		data, err := json.Marshal(doc)
		if err != nil {
			return nil, fmt.Errorf("exporter: 生成 COCO JSON 失败: %w", err)
		}
		plan.Files = append(plan.Files, OutFile{
			Rel:  path.Join("annotations", "instances_"+sp+".json"),
			Data: data,
		})
	}

	plan.Report.Images = len(sorted)
	plan.Report.ImagesWithAnns = withAnns
	plan.Report.Annotations = emitted
	plan.Report.AnnotationsDropped = dropped
	plan.Report.Lossy = rec.rep.Lossy
	plan.Report.Warnings = rec.rep.Warnings
	return plan, nil
}

// cocoEffectiveCategories returns a copy of the project categories with the
// COCO category id resolved (existing COCOID kept, otherwise 1-based position)
// plus a Key -> id lookup for annotations. Index is set to the array position.
// The input slice and its COCOID pointers are never mutated.
func cocoEffectiveCategories(in []ir.Category) ([]ir.Category, map[string]int) {
	out := make([]ir.Category, len(in))
	ids := make(map[string]int, len(in))
	for i, c := range in {
		id := i + 1
		if c.COCOID != nil {
			id = *c.COCOID
		}
		copied := c
		copied.Index = i
		assigned := id
		copied.COCOID = &assigned
		out[i] = copied
		if _, dup := ids[c.Key]; !dup {
			ids[c.Key] = id
		}
	}
	return out, ids
}

// cocoJSONCategories renders the resolved categories for embedding in each
// per-split file.
func cocoJSONCategories(cats []ir.Category) []cocoJSONCategory {
	out := make([]cocoJSONCategory, len(cats))
	for i, c := range cats {
		id := i + 1
		if c.COCOID != nil {
			id = *c.COCOID
		}
		name := c.Name
		if name == "" {
			name = c.Key
		}
		out[i] = cocoJSONCategory{ID: id, Name: name, Supercategory: c.SuperCategory}
	}
	return out
}

// cocoConvertAnnotation turns one IR annotation into a COCO annotation. ok is
// false when the annotation cannot be represented (unknown category, unusable
// geometry); the reason is recorded as RuleDropped.
func cocoConvertAnnotation(img ir.Image, a ir.Annotation, catID map[string]int, imageID, annID int, rec *recorder) (cocoJSONAnnotation, bool) {
	cid, known := catID[a.Category]
	if !known {
		rec.count(RuleDropped, img.File, a.ID, fmt.Sprintf("标注类别 %q 不在项目类别中，已跳过", a.Category))
		return cocoJSONAnnotation{}, false
	}
	drop := func(msg string) (cocoJSONAnnotation, bool) {
		rec.count(RuleDropped, img.File, a.ID, msg)
		return cocoJSONAnnotation{}, false
	}
	// A missing image size means there are no bounds to clip against.
	bounded := img.Width > 0 && img.Height > 0

	out := cocoJSONAnnotation{ID: annID, ImageID: imageID, CategoryID: cid}
	switch a.Kind {
	case ir.KindBBox:
		x, y, w, h := a.X, a.Y, a.W, a.H
		clipped := false
		if bounded {
			x, y, w, h, clipped = clipBBox(x, y, w, h, img.Width, img.Height)
		}
		if w <= 0 || h <= 0 {
			return drop("bbox 裁剪后宽或高不为正，已跳过")
		}
		if clipped {
			rec.count(RuleClipped, img.File, a.ID, "bbox 越界坐标已裁剪")
		}
		out.BBox = []float64{x, y, w, h}
		out.Segmentation = [][]float64{{x, y, x + w, y, x + w, y + h, x, y + h}}
		rec.count(RuleDetToSeg, img.File, a.ID, "bbox 补充矩形四点 segmentation")

	case ir.KindPolygon:
		rings := make([][][2]float64, 0, len(a.Rings))
		clipped := false
		for _, ring := range a.Rings {
			if len(ring) < 3 {
				continue
			}
			pts := ring
			if bounded {
				var changed bool
				pts, changed = clipPoints(ring, img.Width, img.Height)
				if changed {
					clipped = true
				}
			}
			rings = append(rings, pts)
		}
		if len(rings) == 0 {
			return drop("多边形没有点数 >= 3 的环，已跳过")
		}
		x, y, w, h := aabbOfRings(rings)
		if w <= 0 || h <= 0 {
			return drop("多边形外接框裁剪后宽或高不为正，已跳过")
		}
		if clipped {
			rec.count(RuleClipped, img.File, a.ID, "多边形越界坐标已裁剪")
		}
		out.BBox = []float64{x, y, w, h}
		out.Segmentation = make([][]float64, len(rings))
		for i, ring := range rings {
			out.Segmentation[i] = cocoFlattenRing(ring)
		}

	case ir.KindRotated:
		if len(a.Points) != 4 {
			return drop(fmt.Sprintf("旋转框顶点数为 %d（需要 4），已跳过", len(a.Points)))
		}
		pts := a.Points
		clipped := false
		if bounded {
			pts, clipped = clipPoints(a.Points, img.Width, img.Height)
		}
		x, y, w, h := aabbOfPoints(pts)
		if w <= 0 || h <= 0 {
			return drop("旋转框外接框裁剪后宽或高不为正，已跳过")
		}
		if clipped {
			rec.count(RuleClipped, img.File, a.ID, "旋转框顶点越界坐标已裁剪")
		}
		out.BBox = []float64{x, y, w, h}
		out.Segmentation = [][]float64{cocoFlattenRing(pts)}
		out.OBB = &cocoJSONOBB{Points: pts, Angle: a.Angle}
		rec.count(RuleObbToCOCO, img.File, a.ID, "旋转框写为四点 segmentation 加 mylabel_obb 私有扩展")

	default:
		return drop(fmt.Sprintf("未知几何类型 %q，已跳过", a.Kind))
	}

	out.Area = out.BBox[2] * out.BBox[3]
	if len(a.Attributes) > 0 {
		out.Attributes = maps.Clone(a.Attributes)
	}
	return out, true
}

// cocoFlattenRing flattens a ring into COCO's [x1,y1,x2,y2,...] form.
func cocoFlattenRing(ring [][2]float64) []float64 {
	out := make([]float64, 0, len(ring)*2)
	for _, p := range ring {
		out = append(out, p[0], p[1])
	}
	return out
}
