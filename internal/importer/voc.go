package importer

import (
	"encoding/xml"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"mylabel/internal/ir"
)

// vocDoc is the subset of a Pascal VOC annotation file we read.
type vocDoc struct {
	Folder   string      `xml:"folder"`
	Filename string      `xml:"filename"`
	Size     *vocSize    `xml:"size"`
	Objects  []vocObject `xml:"object"`
}

// vocSize keeps the raw text so a missing element is distinguishable from 0.
type vocSize struct {
	Width  string `xml:"width"`
	Height string `xml:"height"`
}

type vocObject struct {
	Name      string       `xml:"name"`
	Difficult string       `xml:"difficult"`
	Truncated string       `xml:"truncated"`
	BndBox    *vocBndBox   `xml:"bndbox"`
	RobndBox  *vocRobndBox `xml:"robndbox"`
}

// vocBndBox holds the raw bndbox text ("xmin" may be missing or non-numeric).
type vocBndBox struct {
	XMin string `xml:"xmin"`
	YMin string `xml:"ymin"`
	XMax string `xml:"xmax"`
	YMax string `xml:"ymax"`
}

// vocRobndBox is roLabelImg's rotated box (angle in radians).
type vocRobndBox struct {
	CX    string `xml:"cx"`
	CY    string `xml:"cy"`
	W     string `xml:"w"`
	H     string `xml:"h"`
	Angle string `xml:"angle"`
}

// parseVOC implements ParseVOC: robndbox wins over bndbox, difficult and
// truncated become attributes, a missing or invalid <size> skips the file.
func parseVOC(xmlDir string, opts Options) (*Dataset, []ir.Issue, error) {
	st, err := os.Stat(xmlDir)
	if err != nil {
		return nil, nil, fmt.Errorf("importer: voc: %w", err)
	}
	if !st.IsDir() {
		return nil, nil, fmt.Errorf("importer: voc: %s 不是目录", xmlDir)
	}

	var (
		issues []ir.Issue
		ds     = &Dataset{}
		seen   = map[string]bool{}
		catIDs = map[string]int{}
	)
	walkErr := filepath.WalkDir(xmlDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(p), ".xml") {
			return nil
		}
		rel, relErr := filepath.Rel(xmlDir, p)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)

		data, readErr := os.ReadFile(p)
		if readErr != nil {
			issues = append(issues, ir.Issue{
				File:    rel,
				Index:   -1,
				Message: "读取失败，已跳过该文件：" + readErr.Error(),
			})
			return nil
		}
		var doc vocDoc
		if xmlErr := xml.Unmarshal(data, &doc); xmlErr != nil {
			issues = append(issues, ir.Issue{
				File:    rel,
				Index:   -1,
				Message: "XML 解析失败，已跳过该文件：" + xmlErr.Error(),
			})
			return nil
		}

		name := vocImageName(doc.Filename)
		if name == "" {
			issues = append(issues, ir.Issue{
				File:    rel,
				Index:   -1,
				Field:   "filename",
				Message: "缺少 <filename>，已跳过该文件",
			})
			return nil
		}
		if seen[name] {
			issues = append(issues, ir.Issue{
				File:    rel,
				Index:   -1,
				Field:   "filename",
				Message: fmt.Sprintf("图片 %q 已由前一个 xml 声明，已跳过该文件", name),
			})
			return nil
		}
		w, h, sizeIssue := vocSizeOf(doc.Size)
		if sizeIssue != "" {
			issues = append(issues, ir.Issue{File: rel, Index: -1, Field: "size", Message: sizeIssue})
			return nil
		}
		seen[name] = true

		img := ir.Image{File: name, Width: w, Height: h, Annotations: []ir.Annotation{}}
		for oi, obj := range doc.Objects {
			at := fmt.Sprintf("第 %d 个 object ", oi+1)
			key := strings.TrimSpace(obj.Name)
			if key == "" {
				issues = append(issues, ir.Issue{
					File:    rel,
					Index:   oi,
					Field:   "name",
					Message: at + "缺少 <name>，已跳过",
				})
				continue
			}
			ann, geomIssue, warn := vocGeometry(obj, opts.VOCOneBased, rel, oi, at)
			if geomIssue != nil {
				issues = append(issues, *geomIssue)
				continue
			}
			if warn != "" {
				ds.Warnings = append(ds.Warnings, ir.Issue{File: rel, Index: oi, Field: "robndbox", Message: warn})
			}
			if _, ok := catIDs[key]; !ok {
				catIDs[key] = len(ds.Categories)
				ds.Categories = append(ds.Categories, ir.Category{
					Key:   key,
					Index: catIDs[key],
					Name:  key,
				})
			}
			ann.ID = strconv.Itoa(len(img.Annotations) + 1)
			ann.Category = key
			ann.Attributes = vocAttributes(obj)
			img.Annotations = append(img.Annotations, ann)
		}
		ds.Images = append(ds.Images, img)
		return nil
	})
	if walkErr != nil {
		return nil, nil, fmt.Errorf("importer: voc: %w", walkErr)
	}

	ds.Warnings = normalImages(ir.TaskMixed, ds.Categories, ds.Images, ds.Warnings)
	return ds, issues, nil
}

// vocImageName reduces <filename> to a bare file name in slash form: the image
// lives next to its xml in VOC datasets, so no directory part is kept.
func vocImageName(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\\", "/")
	if s == "" {
		return ""
	}
	base := path.Base(s)
	if base == "." || base == "/" || base == ".." {
		return ""
	}
	return base
}

// vocSizeOf validates <size> and returns the image dimensions.
func vocSizeOf(size *vocSize) (int, int, string) {
	if size == nil {
		return 0, 0, "缺少 <size>，已跳过该文件"
	}
	w, wErr := strconv.ParseFloat(strings.TrimSpace(size.Width), 64)
	h, hErr := strconv.ParseFloat(strings.TrimSpace(size.Height), 64)
	if wErr != nil || hErr != nil {
		return 0, 0, fmt.Sprintf("<size> 不是数字（width=%q height=%q），已跳过该文件", size.Width, size.Height)
	}
	if w <= 0 || h <= 0 || math.IsNaN(w) || math.IsNaN(h) {
		return 0, 0, fmt.Sprintf("<size> 无效（width=%q height=%q），已跳过该文件", size.Width, size.Height)
	}
	return int(math.Round(w)), int(math.Round(h)), ""
}

// vocGeometry builds the annotation geometry of one object: robndbox when it
// is present and parseable, otherwise bndbox. A malformed robndbox that falls
// back to bndbox is reported through the returned warning.
func vocGeometry(obj vocObject, oneBased bool, file string, oi int, at string) (ir.Annotation, *ir.Issue, string) {
	warn := ""
	if obj.RobndBox != nil {
		vals, ok := parseVOCFloats(obj.RobndBox.CX, obj.RobndBox.CY, obj.RobndBox.W, obj.RobndBox.H, obj.RobndBox.Angle)
		if ok && vals[2] > 0 && vals[3] > 0 {
			return rotatedFromParams(vals[0], vals[1], vals[2], vals[3], vals[4]), nil, ""
		}
		if obj.BndBox == nil {
			return ir.Annotation{}, &ir.Issue{
				File:    file,
				Index:   oi,
				Field:   "robndbox",
				Message: at + "的 robndbox 不完整或不是数字，且没有 bndbox，已跳过",
			}, ""
		}
		warn = at + "的 robndbox 不完整或不是数字，已改用 bndbox"
	}
	if obj.BndBox == nil {
		return ir.Annotation{}, &ir.Issue{
			File:    file,
			Index:   oi,
			Field:   "bndbox",
			Message: at + "既没有 robndbox 也没有 bndbox，已跳过",
		}, ""
	}
	vals, ok := parseVOCFloats(obj.BndBox.XMin, obj.BndBox.YMin, obj.BndBox.XMax, obj.BndBox.YMax)
	if !ok {
		return ir.Annotation{}, &ir.Issue{
			File:  file,
			Index: oi,
			Field: "bndbox",
			Message: fmt.Sprintf("%s的 bndbox 不完整或不是数字（xmin=%q ymin=%q xmax=%q ymax=%q），已跳过",
				at, obj.BndBox.XMin, obj.BndBox.YMin, obj.BndBox.XMax, obj.BndBox.YMax),
		}, ""
	}
	if oneBased {
		for i := range vals {
			vals[i]--
		}
	}
	return ir.Annotation{
		Kind: ir.KindBBox,
		X:    vals[0],
		Y:    vals[1],
		W:    math.Abs(vals[2] - vals[0]),
		H:    math.Abs(vals[3] - vals[1]),
	}, nil, warn
}

// parseVOCFloats parses all fields as finite numbers, reporting whether every
// one was valid.
func parseVOCFloats(fields ...string) ([]float64, bool) {
	vals := make([]float64, len(fields))
	for i, f := range fields {
		v, err := strconv.ParseFloat(strings.TrimSpace(f), 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, false
		}
		vals[i] = v
	}
	return vals, true
}

// vocAttributes maps difficult/truncated flags to annotation attributes,
// omitting zero values.
func vocAttributes(obj vocObject) map[string]string {
	attrs := map[string]string{}
	if strings.TrimSpace(obj.Difficult) == "1" {
		attrs["difficult"] = "1"
	}
	if strings.TrimSpace(obj.Truncated) == "1" {
		attrs["truncated"] = "1"
	}
	if len(attrs) == 0 {
		return nil
	}
	return attrs
}

// rotatedFromParams builds a rotated_bbox from roLabelImg parameters. Points
// are left for ir.Normalize to derive (contract: parameters in, vertices out).
func rotatedFromParams(cx, cy, rw, rh, angle float64) ir.Annotation {
	return ir.Annotation{Kind: ir.KindRotated, CX: cx, CY: cy, RW: rw, RH: rh, Angle: angle}
}
