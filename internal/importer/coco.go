package importer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"mylabel/internal/ir"
)

// attrIscrowd marks annotations that COCO flagged as crowd regions.
const attrIscrowd = "iscrowd"

// cocoDoc is the top-level COCO document. info/licenses and any other
// top-level keys are ignored. Array entries stay raw so that a single
// malformed entry yields a locatable Issue instead of failing the whole file;
// only broken document syntax is fatal.
type cocoDoc struct {
	Images      []json.RawMessage `json:"images"`
	Annotations []json.RawMessage `json:"annotations"`
	Categories  []json.RawMessage `json:"categories"`
}

type cocoCategoryEntry struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	Supercategory string `json:"supercategory"`
}

type cocoImageEntry struct {
	ID       int    `json:"id"`
	FileName string `json:"file_name"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

type cocoAnnotationEntry struct {
	ID           int             `json:"id"`
	ImageID      int             `json:"image_id"`
	CategoryID   int             `json:"category_id"`
	BBox         []float64       `json:"bbox"`
	Segmentation json.RawMessage `json:"segmentation"`
	Iscrowd      int             `json:"iscrowd"`
}

// cocoSegKind is the segmentation union discriminant.
type cocoSegKind int

const (
	cocoSegNone    cocoSegKind = iota // absent, null or empty
	cocoSegPolygon                    // list of polygons
	cocoSegRLE                        // RLE object ({counts, size})
)

// cocoSegmentation is the COCO segmentation union. A JSON array becomes one
// ring per segment (flat [x1,y1,x2,y2,...] coordinates converted to points);
// a JSON object becomes a decoded RLE mask plus its bounding box.
type cocoSegmentation struct {
	Kind  cocoSegKind
	Rings [][][2]float64 // polygon segments, image pixels
	Mask  *cocoRLEMask   // RLE run lengths and mask bounding box
}

// UnmarshalJSON implements the segmentation union.
func (s *cocoSegmentation) UnmarshalJSON(data []byte) error {
	*s = cocoSegmentation{}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	switch trimmed[0] {
	case '[':
		var polys [][]float64
		if err := json.Unmarshal(trimmed, &polys); err != nil {
			return fmt.Errorf("segmentation polygons: %w", err)
		}
		for i, flat := range polys {
			if len(flat) == 0 {
				continue
			}
			if len(flat)%2 != 0 {
				return fmt.Errorf("segmentation polygon %d: odd coordinate count %d", i, len(flat))
			}
			if len(flat) < 6 {
				return fmt.Errorf("segmentation polygon %d: %d point(s), need at least 3", i, len(flat)/2)
			}
			ring := make([][2]float64, len(flat)/2)
			for j := range ring {
				ring[j] = [2]float64{flat[2*j], flat[2*j+1]}
			}
			s.Rings = append(s.Rings, ring)
		}
		if len(s.Rings) > 0 {
			s.Kind = cocoSegPolygon
		}
		return nil
	case '{':
		var obj struct {
			Counts cocoRLECounts `json:"counts"`
			Size   []float64     `json:"size"`
		}
		if err := json.Unmarshal(trimmed, &obj); err != nil {
			return fmt.Errorf("segmentation RLE: %w", err)
		}
		if !obj.Counts.IsString && obj.Counts.Array == nil {
			return errors.New("segmentation RLE: missing counts")
		}
		height, width, err := cocoRLESize(obj.Size)
		if err != nil {
			return err
		}
		counts := obj.Counts.Array
		if obj.Counts.IsString {
			counts, err = decodeRLEString(obj.Counts.String)
			if err != nil {
				return err
			}
		}
		mask, err := newCOCOMask(counts, height, width)
		if err != nil {
			return err
		}
		s.Kind = cocoSegRLE
		s.Mask = mask
		return nil
	}
	return fmt.Errorf("segmentation: unexpected JSON value starting with %q", trimmed[0])
}

// cocoRLECounts is the RLE counts union: the compressed string form or the
// uncompressed run-length array form.
type cocoRLECounts struct {
	Array    []int64
	String   string
	IsString bool
}

// UnmarshalJSON accepts either a JSON string (compressed) or a JSON array of
// integral run lengths (uncompressed).
func (c *cocoRLECounts) UnmarshalJSON(data []byte) error {
	*c = cocoRLECounts{}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return errors.New("rle: missing counts")
	}
	switch trimmed[0] {
	case '"':
		if err := json.Unmarshal(trimmed, &c.String); err != nil {
			return fmt.Errorf("rle: counts string: %w", err)
		}
		c.IsString = true
		return nil
	case '[':
		var nums []json.Number
		if err := json.Unmarshal(trimmed, &nums); err != nil {
			return fmt.Errorf("rle: counts array: %w", err)
		}
		c.Array = make([]int64, 0, len(nums))
		for i, n := range nums {
			if v, err := n.Int64(); err == nil {
				c.Array = append(c.Array, v)
				continue
			}
			f, err := n.Float64()
			if err != nil || f != math.Trunc(f) {
				return fmt.Errorf("rle: counts[%d] = %s is not an integer", i, n.String())
			}
			c.Array = append(c.Array, int64(f))
		}
		return nil
	}
	return fmt.Errorf("rle: counts must be a string or an array, got %q", trimmed[0])
}

// cocoRLESize validates the RLE [height, width] pair.
func cocoRLESize(size []float64) (height, width int, err error) {
	if len(size) != 2 {
		return 0, 0, fmt.Errorf("rle: size must be [height,width], got %v", size)
	}
	h, w := size[0], size[1]
	if h != math.Trunc(h) || w != math.Trunc(w) || h <= 0 || w <= 0 {
		return 0, 0, fmt.Errorf("rle: invalid size %v", size)
	}
	return int(h), int(w), nil
}

// parseCOCO is the implementation behind ParseCOCO (see dataset.go).
func parseCOCO(path string) (*Dataset, []ir.Issue, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("importer: read COCO file: %w", err)
	}
	var doc cocoDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, nil, fmt.Errorf("importer: parse COCO file %q: %w", filepath.Base(path), err)
	}
	docName := filepath.Base(path)

	ds := &Dataset{}
	var issues []ir.Issue

	// Categories: Index follows the categories array order, Key is the name,
	// the original COCO id is preserved in COCOID.
	catIndex := make(map[int]int, len(doc.Categories))
	catKeys := make(map[string]bool, len(doc.Categories))
	for i, raw := range doc.Categories {
		var e cocoCategoryEntry
		if err := json.Unmarshal(raw, &e); err != nil {
			issues = append(issues, ir.Issue{
				File: docName, Index: i, Field: "categories",
				Message: fmt.Sprintf("categories[%d]: %v", i, err),
			})
			continue
		}
		name := e.Name
		key := name
		if key == "" {
			key = "category_" + strconv.Itoa(e.ID)
			name = key
		}
		if catKeys[key] {
			for n := 2; ; n++ {
				candidate := key + "~" + strconv.Itoa(n)
				if !catKeys[candidate] {
					key = candidate
					break
				}
			}
		}
		catKeys[key] = true
		id := e.ID
		ds.Categories = append(ds.Categories, ir.Category{
			Key:           key,
			Index:         len(ds.Categories),
			Name:          name,
			SuperCategory: e.Supercategory,
			COCOID:        &id,
		})
		if _, seen := catIndex[e.ID]; !seen {
			catIndex[e.ID] = len(ds.Categories) - 1
		}
	}

	// Images: array order kept, file names normalized to slash separators.
	imgIndex := make(map[int]int, len(doc.Images))
	for i, raw := range doc.Images {
		var e cocoImageEntry
		if err := json.Unmarshal(raw, &e); err != nil {
			issues = append(issues, ir.Issue{
				File: docName, Index: i, Field: "images",
				Message: fmt.Sprintf("images[%d]: %v", i, err),
			})
			continue
		}
		file := cocoSlashPath(e.FileName)
		if file == "" {
			issues = append(issues, ir.Issue{
				File: docName, Index: i, Field: "file_name",
				Message: fmt.Sprintf("images[%d] (id=%d): empty file_name, skipped", i, e.ID),
			})
			continue
		}
		if first, dup := imgIndex[e.ID]; dup {
			issues = append(issues, ir.Issue{
				File: file, Field: "images",
				Message: fmt.Sprintf("duplicate image id=%d, keeping the first entry %q", e.ID, ds.Images[first].File),
			})
			continue
		}
		imgIndex[e.ID] = len(ds.Images)
		ds.Images = append(ds.Images, ir.Image{
			File:        file,
			Width:       e.Width,
			Height:      e.Height,
			Annotations: []ir.Annotation{},
		})
	}

	// Annotations: dropped entries are reported as Issues, never silently.
	for i, raw := range doc.Annotations {
		var e cocoAnnotationEntry
		if err := json.Unmarshal(raw, &e); err != nil {
			issues = append(issues, ir.Issue{
				File: docName, Index: i, Field: "annotations",
				Message: fmt.Sprintf("annotations[%d]: %v", i, err),
			})
			continue
		}
		imgIdx, ok := imgIndex[e.ImageID]
		if !ok {
			issues = append(issues, ir.Issue{
				File: docName, Index: i, Field: "image_id",
				Message: fmt.Sprintf("annotation id=%d references unknown image_id=%d, skipped", e.ID, e.ImageID),
			})
			continue
		}
		img := &ds.Images[imgIdx]
		annIdx := len(img.Annotations)
		catIdx, ok := catIndex[e.CategoryID]
		if !ok {
			issues = append(issues, ir.Issue{
				File: img.File, Index: annIdx, Field: "category_id",
				Message: fmt.Sprintf("annotation id=%d references unknown category_id=%d, skipped", e.ID, e.CategoryID),
			})
			continue
		}
		ann, warns, annIssues := cocoAnnotationToIR(e, ds.Categories[catIdx].Key, img.File, annIdx)
		issues = append(issues, annIssues...)
		ds.Warnings = append(ds.Warnings, warns...)
		if ann == nil {
			continue
		}
		img.Annotations = append(img.Annotations, *ann)
	}

	proj := &ir.Project{IRVersion: ir.Version, Task: cocoTask(ds.Images), Categories: ds.Categories}
	for i := range ds.Images {
		ds.Warnings = append(ds.Warnings, ir.Normalize(proj, &ds.Images[i])...)
	}
	return ds, issues, nil
}

// cocoAnnotationToIR converts one COCO annotation. A nil annotation means the
// entry could not yield any geometry and must be skipped (reported through
// issues). Lossy conversions are reported through warnings.
func cocoAnnotationToIR(e cocoAnnotationEntry, catKey, file string, idx int) (ann *ir.Annotation, warnings, issues []ir.Issue) {
	var seg cocoSegmentation
	if len(bytes.TrimSpace(e.Segmentation)) > 0 {
		if err := json.Unmarshal(e.Segmentation, &seg); err != nil {
			issues = append(issues, ir.Issue{
				File: file, Index: idx, Field: "segmentation",
				Message: fmt.Sprintf("annotation id=%d: %v", e.ID, err),
			})
			seg = cocoSegmentation{}
		}
	}

	out := &ir.Annotation{ID: strconv.Itoa(e.ID), Category: catKey}

	switch {
	case seg.Kind == cocoSegPolygon:
		// Segmentation wins over bbox; COCO stores both redundantly.
		out.Kind = ir.KindPolygon
		out.Rings = seg.Rings
		if e.Iscrowd != 0 {
			out.Attributes = map[string]string{attrIscrowd: "1"}
		}
		return out, nil, issues
	case seg.Kind == cocoSegRLE && seg.Mask != nil && seg.Mask.HasPixels:
		// RLE is degraded to the mask bounding box (no contour tracing this
		// phase), whatever the iscrowd flag says.
		m := seg.Mask
		out.Kind = ir.KindBBox
		out.X = float64(m.MinX)
		out.Y = float64(m.MinY)
		out.W = float64(m.MaxX - m.MinX + 1)
		out.H = float64(m.MaxY - m.MinY + 1)
		out.Attributes = map[string]string{attrIscrowd: "1"}
		warnings = append(warnings, ir.Issue{
			File: file, Index: idx, Field: "segmentation",
			Message: fmt.Sprintf("annotation id=%d (iscrowd=%d): RLE mask degraded to its bounding box", e.ID, e.Iscrowd),
		})
		return out, warnings, issues
	}

	if len(e.BBox) == 4 {
		out.Kind = ir.KindBBox
		out.X, out.Y, out.W, out.H = e.BBox[0], e.BBox[1], e.BBox[2], e.BBox[3]
		if seg.Kind == cocoSegRLE {
			warnings = append(warnings, ir.Issue{
				File: file, Index: idx, Field: "segmentation",
				Message: fmt.Sprintf("annotation id=%d: empty RLE mask, fell back to bbox", e.ID),
			})
		}
		return out, warnings, issues
	}

	issues = append(issues, ir.Issue{
		File: file, Index: idx, Field: "bbox",
		Message: fmt.Sprintf("annotation id=%d: no usable segmentation or bbox, skipped", e.ID),
	})
	return nil, warnings, issues
}

// cocoTask derives the IR task of a COCO import from the annotation kinds.
func cocoTask(images []ir.Image) string {
	var haveBBox, havePoly bool
	for _, img := range images {
		for _, a := range img.Annotations {
			switch a.Kind {
			case ir.KindBBox:
				haveBBox = true
			case ir.KindPolygon:
				havePoly = true
			}
		}
	}
	switch {
	case haveBBox && havePoly:
		return ir.TaskMixed
	case havePoly:
		return ir.TaskSeg
	default:
		return ir.TaskDet
	}
}

// cocoSlashPath keeps COCO file names relative and slash separated.
func cocoSlashPath(p string) string {
	p = strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	return strings.TrimPrefix(p, "./")
}
