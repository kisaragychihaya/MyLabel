package app

import (
	"fmt"
	"math"
	"path"
	"path/filepath"
	"strings"

	"mylabel/internal/ir"
)

// IR exchange conversions (docs/phase3-design.md §4.1). The IR model lives in
// internal/ir and never replaces the app model: inside a session Ann is
// authoritative, and the two meet only at import (this wave) and export
// (phase 4).

// irProjectName is the IR project document a folder may carry.
const irProjectName = "project.json"

// irDirName is the IR subdirectory holding one document per image.
const irDirName = "ir"

// convStats counts what a conversion could not carry into the app model.
type convStats struct {
	// attrs counts imported annotations that carry Attributes. They are kept on
	// Ann since phase 3 W3 and land in the store's v4 attrs column, so this is
	// only a count for the import summary.
	attrs int
	// droppedRings counts polygon rings beyond the outer one: Ann holds a
	// single ring, so only the largest survives (§4.1).
	droppedRings int
}

// categoryKeys returns the IR key of every app category: its name, with a
// "~2", "~3", … suffix when the same name occurs again (§4.1). A nameless
// category is keyed after its position, so a key is never empty.
func categoryKeys(cats []Category) []string {
	keys := make([]string, len(cats))
	taken := make(map[string]bool, len(cats))
	for i, c := range cats {
		base := strings.TrimSpace(c.Name)
		if base == "" {
			base = fmt.Sprintf("cat%d", i+1)
		}
		key := base
		for n := 2; taken[key]; n++ {
			key = fmt.Sprintf("%s~%d", base, n)
		}
		taken[key] = true
		keys[i] = key
	}
	return keys
}

// keyOf returns keys[i], or "" when i is outside the list.
func keyOf(keys []string, i int) string {
	if i < 0 || i >= len(keys) {
		return ""
	}
	return keys[i]
}

// annToIR converts one app annotation to IR. keys is categoryKeys of the
// project's categories.
//
// A rectangle at angle 0 becomes a bbox (top-left origin), a rotated rectangle
// becomes a rotated_bbox whose Points are the four corners in TL,TR,BR,BL
// order (ir.Normalize re-orders them canonically), and a polygon becomes a
// polygon with its vertices as the outer ring. Attributes pass through as they
// are (§4.1).
func annToIR(an *Ann, keys []string) ir.Annotation {
	out := ir.Annotation{ID: an.ID, Category: keyOf(keys, an.Cat), Attributes: cloneAttrs(an.Attributes)}
	if isPoly(an) {
		out.Kind = ir.KindPolygon
		ring := make([][2]float64, len(an.Points))
		for i, p := range an.Points {
			ring[i] = [2]float64{p.X, p.Y}
		}
		out.Rings = [][][2]float64{ring}
		return out
	}

	out.CX, out.CY, out.RW, out.RH, out.Angle = an.CX, an.CY, an.W, an.H, an.Angle
	if an.Angle == 0 {
		out.Kind = ir.KindBBox
		out.X, out.Y, out.W, out.H = an.CX-an.W/2, an.CY-an.H/2, an.W, an.H
		return out
	}
	out.Kind = ir.KindRotated
	c := corners(an)
	out.Points = make([][2]float64, 4)
	for i, p := range c {
		out.Points[i] = [2]float64{p.X, p.Y}
	}
	return out
}

// annsToIR converts one image's annotations to IR.
func annsToIR(anns []Ann, keys []string) []ir.Annotation {
	if len(anns) == 0 {
		return nil
	}
	out := make([]ir.Annotation, len(anns))
	for i := range anns {
		out[i] = annToIR(&anns[i], keys)
	}
	return out
}

// irCategoryToApp converts one IR category into the app's category record. The
// IR key and the COCO id have no app counterpart (the store keeps name and
// color only), so they are used up here: the name falls back to the key, and a
// color only survives when it is a valid "#rrggbb".
func irCategoryToApp(c ir.Category) Category {
	name := strings.TrimSpace(c.Name)
	if name == "" {
		name = strings.TrimSpace(c.Key)
	}
	if name == "" {
		name = "类别"
	}
	out := Category{Name: name}
	if col, ok := parseHexColor(strings.TrimSpace(c.Color)); ok {
		out.Color = formatHexColor(col)
	}
	return out
}

// appCategoriesToIR converts the app's category list to IR (§4.1): the key is
// the name (deduplicated by categoryKeys), the index its position, and the
// super category and COCO id are empty, as the app does not track them.
func appCategoriesToIR(cats []Category) []ir.Category {
	keys := categoryKeys(cats)
	out := make([]ir.Category, len(cats))
	for i, c := range cats {
		out[i] = ir.Category{Key: keys[i], Index: i, Name: c.Name, Color: c.Color}
	}
	return out
}

// irAnnToApp converts one IR annotation, resolving its category key through
// catOf (source key → app category index). It reports false when the
// annotation has no usable geometry or names a category nobody mapped, so the
// caller can skip it and count it.
//
// A rotated_bbox is rebuilt from its Points, which are authoritative: the
// derived width/height/angle are recomputed from them, so a normalized IR box
// whose cache swapped width and height still yields the same four corners.
// Attributes are copied onto the annotation as they are (§4.1); they reach the
// store's attrs column through the ordinary save.
func irAnnToApp(x ir.Annotation, catOf map[string]int, st *convStats) (Ann, bool) {
	cat, ok := catOf[x.Category]
	if !ok {
		return Ann{}, false
	}
	if len(x.Attributes) > 0 && st != nil {
		st.attrs++
	}

	an := Ann{ID: x.ID, Cat: cat, Attributes: cloneAttrs(x.Attributes)}
	switch x.Kind {
	case ir.KindBBox:
		if !(x.W > 0) || !(x.H > 0) {
			return Ann{}, false
		}
		an.Kind = KindRect
		an.CX, an.CY = x.X+x.W/2, x.Y+x.H/2
		an.W, an.H = x.W, x.H
		return an, true

	case ir.KindPolygon:
		if len(x.Rings) == 0 || len(x.Rings[0]) < minPolyVertices {
			return Ann{}, false
		}
		if len(x.Rings) > 1 && st != nil {
			st.droppedRings += len(x.Rings) - 1
		}
		pts := make([]Point, len(x.Rings[0]))
		for i, p := range x.Rings[0] {
			pts[i] = Point{X: p[0], Y: p[1]}
		}
		an.Kind = KindPoly
		an.Points = pts
		syncPolyBounds(&an)
		return an, true

	case ir.KindRotated:
		if len(x.Points) != 4 {
			return Ann{}, false
		}
		w := pointDist(x.Points[0], x.Points[1])
		h := pointDist(x.Points[1], x.Points[2])
		if !(w > 0) || !(h > 0) {
			return Ann{}, false
		}
		an.Kind = KindRect
		an.CX = (x.Points[0][0] + x.Points[1][0] + x.Points[2][0] + x.Points[3][0]) / 4
		an.CY = (x.Points[0][1] + x.Points[1][1] + x.Points[2][1] + x.Points[3][1]) / 4
		an.W, an.H = w, h
		an.Angle = normalizeAngle(math.Atan2(x.Points[1][1]-x.Points[0][1], x.Points[1][0]-x.Points[0][0]))
		return an, true
	}
	return Ann{}, false
}

// pointDist is the euclidean distance between two IR vertices.
func pointDist(a, b [2]float64) float64 {
	return math.Hypot(b[0]-a[0], b[1]-a[1])
}

// cleanImportPath normalizes a dataset image path to the slash-separated,
// folder-relative form ImageInfo.RelPath uses, so an importer's and the
// folder's spelling of the same file match.
func cleanImportPath(file string) string {
	f := strings.TrimSpace(filepath.ToSlash(file))
	if f == "" {
		return ""
	}
	f = path.Clean(f)
	for strings.HasPrefix(f, "./") {
		f = f[2:]
	}
	if f == "." {
		return ""
	}
	return f
}
