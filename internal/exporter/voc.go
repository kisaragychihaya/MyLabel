package exporter

import (
	"encoding/xml"
	"fmt"
	"math"
	"path"
	"strconv"
	"strings"

	"mylabel/internal/ir"
)

// VOC document shapes (docs/phase4-design.md §7). encoding/xml omits nil
// pointers, so an object carries exactly one of bndbox / robndbox.
type vocDoc struct {
	XMLName  xml.Name    `xml:"annotation"`
	Folder   string      `xml:"folder"`
	Filename string      `xml:"filename"`
	Size     vocSize     `xml:"size"`
	Objects  []vocObject `xml:"object"`
}

type vocSize struct {
	Width  int `xml:"width"`
	Height int `xml:"height"`
	Depth  int `xml:"depth"`
}

type vocObject struct {
	Name     string       `xml:"name"`
	BndBox   *vocBndBox   `xml:"bndbox,omitempty"`
	RobndBox *vocRobndBox `xml:"robndbox,omitempty"`
}

type vocBndBox struct {
	XMin int `xml:"xmin"`
	YMin int `xml:"ymin"`
	XMax int `xml:"xmax"`
	YMax int `xml:"ymax"`
}

// vocRobndBox keeps its numbers as strings so the radians survive verbatim
// (no fixed precision, no exponent notation).
type vocRobndBox struct {
	CX    string `xml:"cx"`
	CY    string `xml:"cy"`
	W     string `xml:"w"`
	H     string `xml:"h"`
	Angle string `xml:"angle"`
}

// vocXMLHeader is prepended because encoding/xml writes no declaration.
const vocXMLHeader = `<?xml version="1.0" encoding="UTF-8"?>` + "\n"

// buildVOCPlan builds the classic VOC layout (docs/phase4-design.md §7):
// flat JPEGImages/<basename>.<ext>, Annotations/<basename>.xml, and
// ImageSets/Main/{train,val}.txt (one id per line, id = basename without
// extension). XML via encoding/xml with the UTF-8 declaration; bndbox
// coordinates rounded to int, +1 when opts.VOCOneBased. Rotated boxes write
// robndbox (radians, IR derived cache as-is) unless opts.VOCDowngradeOBB
// (then enclosing bndbox, RuleObbToBndbox). Polygons are unsupported:
// skipped with RuleSegDropped plus one summary recorder.warn. Unknown
// categories drop with RuleDropped. Images without annotations still get an
// object-less xml. plan.Files must contain every xml and the ImageSets
// files (val.txt only when the val split is non-empty).
func buildVOCPlan(proj *ir.Project, images []ir.Image, splits map[string]string, opts Options) (*Plan, error) {
	cats, index := planCategories(proj.Categories)
	rec := newRecorder()
	plan := &Plan{Categories: cats}
	plan.Report.Images = len(images)

	ids := map[string][]string{}
	segSkipped := 0

	for _, img := range sortedImages(images) {
		rel := slashRel(img.File)
		base := path.Base(rel)
		stem := strings.TrimSuffix(base, path.Ext(base))
		split := splitOf(splits, img.File)

		plan.Copies = append(plan.Copies, ImageCopy{Src: img.File, Dst: "JPEGImages/" + base})
		ids[split] = append(ids[split], stem)

		doc := vocDoc{
			Folder:   "JPEGImages",
			Filename: base,
			Size:     vocSize{Width: img.Width, Height: img.Height, Depth: 3},
		}
		for _, a := range img.Annotations {
			cls, known := index[a.Category]
			if !known {
				plan.Report.AnnotationsDropped++
				rec.count(RuleDropped, img.File, a.ID, fmt.Sprintf("未知类别 %q", a.Category))
				continue
			}
			if a.Kind == ir.KindPolygon {
				plan.Report.AnnotationsDropped++
				segSkipped++
				rec.count(RuleSegDropped, img.File, a.ID, ruleMessage(RuleSegDropped))
				continue
			}
			obj, lossy, clipped, drop := vocConvert(a, cats[cls].Name, opts, img.Width, img.Height)
			if drop != "" {
				plan.Report.AnnotationsDropped++
				rec.count(RuleDropped, img.File, a.ID, drop)
				continue
			}
			if lossy != "" {
				rec.count(lossy, img.File, a.ID, ruleMessage(lossy))
			}
			if clipped {
				rec.count(RuleClipped, img.File, a.ID, ruleMessage(RuleClipped))
			}
			doc.Objects = append(doc.Objects, obj)
			plan.Report.Annotations++
		}
		if len(doc.Objects) > 0 {
			plan.Report.ImagesWithAnns++
		}

		data, err := marshalVOCXML(doc)
		if err != nil {
			return nil, err
		}
		plan.Files = append(plan.Files, OutFile{Rel: "Annotations/" + stem + ".xml", Data: data})
	}

	if segSkipped > 0 {
		rec.warn(fmt.Sprintf("VOC 导出不含分割，%d 条 seg 标注被跳过", segSkipped))
	}
	for _, name := range []string{"train", "val"} {
		if name == "val" && len(ids["val"]) == 0 {
			continue
		}
		plan.Files = append(plan.Files, OutFile{
			Rel:  "ImageSets/Main/" + name + ".txt",
			Data: idLines(ids[name]),
		})
	}

	plan.Report.Lossy = rec.rep.Lossy
	plan.Report.Warnings = rec.rep.Warnings
	return plan, nil
}

// vocConvert renders one annotation as a VOC object. drop is non-empty when
// the annotation cannot be written; lossy names the rule that fired.
func vocConvert(a ir.Annotation, name string, opts Options, iw, ih int) (obj vocObject, lossy string, clipped bool, drop string) {
	obj.Name = name
	switch a.Kind {
	case ir.KindBBox:
		if !(a.W > 0) || !(a.H > 0) {
			return vocObject{}, "", false, "矩形为空"
		}
		nx, ny, nw, nh, moved := clipBBox(a.X, a.Y, a.W, a.H, iw, ih)
		if !(nw > 0) || !(nh > 0) {
			return vocObject{}, "", false, "矩形裁剪后为空"
		}
		obj.BndBox = vocBox(nx, ny, nw, nh, opts.VOCOneBased)
		return obj, "", moved, ""
	case ir.KindRotated:
		quad, ok := quadOf(a)
		if !ok {
			return vocObject{}, "", false, "旋转框既无顶点也无派生缓存"
		}
		if opts.VOCDowngradeOBB {
			x, y, w, h := aabbOfPoints(quad)
			if !(w > 0) || !(h > 0) {
				return vocObject{}, "", false, "旋转框的外接框为空"
			}
			nx, ny, nw, nh, moved := clipBBox(x, y, w, h, iw, ih)
			if !(nw > 0) || !(nh > 0) {
				return vocObject{}, "", false, "旋转框的外接框裁剪后为空"
			}
			obj.BndBox = vocBox(nx, ny, nw, nh, opts.VOCOneBased)
			return obj, RuleObbToBndbox, moved, ""
		}
		cx, cy, w, h, angle := a.CX, a.CY, a.RW, a.RH, a.Angle
		if !(w > 0) || !(h > 0) {
			if len(a.Points) != 4 {
				return vocObject{}, "", false, "旋转框缺少尺寸"
			}
			cx, cy, w, h, angle = quadParamsOf(a.Points)
		}
		if !(w > 0) || !(h > 0) {
			return vocObject{}, "", false, "旋转框尺寸无效"
		}
		obj.RobndBox = &vocRobndBox{
			CX:    formatXMLNum(cx),
			CY:    formatXMLNum(cy),
			W:     formatXMLNum(w),
			H:     formatXMLNum(h),
			Angle: formatXMLNum(angle),
		}
		return obj, "", false, ""
	}
	return vocObject{}, "", false, fmt.Sprintf("不支持的几何类型 %q", a.Kind)
}

// vocBox builds a bndbox from pixel coordinates in [0,iw]x[0,ih], rounding
// each edge and shifting to 1-based when requested.
func vocBox(x, y, w, h float64, oneBased bool) *vocBndBox {
	shift := 0
	if oneBased {
		shift = 1
	}
	return &vocBndBox{
		XMin: int(math.Round(x)) + shift,
		YMin: int(math.Round(y)) + shift,
		XMax: int(math.Round(x+w)) + shift,
		YMax: int(math.Round(y+h)) + shift,
	}
}

// formatXMLNum renders an XML number without exponent notation or trailing
// zeros, so values round-trip exactly.
func formatXMLNum(v float64) string {
	if v == 0 {
		return "0"
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// marshalVOCXML renders one annotation document with the UTF-8 declaration.
func marshalVOCXML(doc vocDoc) ([]byte, error) {
	body, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("exporter: 生成 VOC XML 失败: %w", err)
	}
	out := make([]byte, 0, len(vocXMLHeader)+len(body)+1)
	out = append(out, vocXMLHeader...)
	out = append(out, body...)
	return append(out, '\n'), nil
}

// idLines renders one id per line (empty data when there are no ids).
func idLines(ids []string) []byte {
	if len(ids) == 0 {
		return nil
	}
	return []byte(strings.Join(ids, "\n") + "\n")
}
