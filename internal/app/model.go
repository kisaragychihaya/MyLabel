package app

import (
	"fmt"
	"maps"
	"strconv"
	"strings"
	"sync/atomic"

	"mylabel/internal/store"
)

// Annotation kinds, re-exported from the store so the app model and the
// database agree on the strings (docs/phase2-design.md §2.3).
const (
	KindRect = store.KindRect
	KindPoly = store.KindPoly
)

// minPolyVertices is the fewest vertices a polygon annotation may have.
const minPolyVertices = 3

// Ann is one annotation in image pixel coordinates: a rotatable rectangle, or
// a closed polygon when Kind is KindPoly.
//
// KindRect: CX/CY/W/H/Angle describe the box, Points is empty; Angle == 0 is
// an axis-aligned detection box (det), otherwise obb.
// KindPoly: Points holds the vertices (implicitly closed); CX/CY/W/H mirror
// their bounding box and Angle is 0. Points is authoritative — syncPolyBounds
// refreshes the mirror whenever the vertices change.
type Ann struct {
	ID    string  `json:"id"`
	Cat   int     `json:"cat"`  // category index, 0..8
	Kind  string  `json:"kind"` // KindRect | KindPoly; empty means KindRect
	CX    float64 `json:"cx"`   // center, image pixels
	CY    float64 `json:"cy"`
	W     float64 `json:"w"`
	H     float64 `json:"h"`
	Angle float64 `json:"angle"` // radians, around center (KindRect only)
	// Points holds a polygon's vertices, image pixels, implicitly closed.
	// Never shared with another Ann: undo snapshots and drafts hold copies
	// (cloneAnn), docs/phase2-design.md §2.3.
	Points []Point `json:"points,omitempty"`
	// Attributes holds the annotation's free-form properties (VOC
	// difficult/truncated, COCO iscrowd, a user's own keys), edited in the
	// properties dialog (docs/phase3-design.md §5.1). Never shared with another
	// Ann either: cloneAnn copies the map, so an undo snapshot cannot follow a
	// later edit.
	Attributes map[string]string `json:"attributes,omitempty"`
}

// annKind returns the annotation's kind, reading an empty Kind (a legacy
// document's annotation has no "kind" key) as KindRect.
func annKind(an *Ann) string {
	if an.Kind == KindPoly {
		return KindPoly
	}
	return KindRect
}

// isPoly reports whether an is a polygon annotation.
func isPoly(an *Ann) bool { return annKind(an) == KindPoly }

// cloneAnn returns a deep copy of an: Points and Attributes are copied, so no
// two annotations ever share a vertex slice or an attribute map. Every undo
// snapshot, draft and annotation insertion goes through it.
func cloneAnn(an Ann) Ann {
	out := an
	out.Points = clonePoints(an.Points)
	out.Attributes = cloneAttrs(an.Attributes)
	return out
}

// cloneAttrs returns a copy of an attribute map; a nil map stays nil, so "no
// attributes" survives a copy as the empty state.
func cloneAttrs(attrs map[string]string) map[string]string {
	if len(attrs) == 0 {
		return nil
	}
	out := make(map[string]string, len(attrs))
	for k, v := range attrs {
		out[k] = v
	}
	return out
}

// sameAnn reports whether a and b are equal field by field, Points and
// Attributes included. Ann holds a slice and a map and so is not comparable
// with ==.
func sameAnn(a, b Ann) bool {
	if a.ID != b.ID || a.Cat != b.Cat || a.Kind != b.Kind ||
		a.CX != b.CX || a.CY != b.CY || a.W != b.W || a.H != b.H || a.Angle != b.Angle {
		return false
	}
	if !maps.Equal(a.Attributes, b.Attributes) {
		return false
	}
	if len(a.Points) != len(b.Points) {
		return false
	}
	for i := range a.Points {
		if a.Points[i] != b.Points[i] {
			return false
		}
	}
	return true
}

// syncPolyBounds refreshes a polygon's redundant rectangle fields from its
// vertices, which are authoritative.
func syncPolyBounds(an *Ann) {
	x, y, w, h := polygonBBox(an.Points)
	an.CX, an.CY, an.W, an.H = x+w/2, y+h/2, w, h
	an.Angle = 0
}

// newPolyAnn returns a polygon annotation over pts with its bounds synced.
func newPolyAnn(id string, cat int, pts []Point) Ann {
	an := Ann{ID: id, Cat: cat, Kind: KindPoly, Points: clonePoints(pts)}
	syncPolyBounds(&an)
	return an
}

// pointsFromFlat converts a stored flat vertex list [x0,y0,x1,y1,...] into
// points. A trailing unpaired coordinate is ignored.
func pointsFromFlat(flat []float64) []Point {
	n := len(flat) / 2
	if n == 0 {
		return nil
	}
	pts := make([]Point, n)
	for i := range pts {
		pts[i] = Point{X: flat[2*i], Y: flat[2*i+1]}
	}
	return pts
}

// flatPoints flattens vertices for the store as [x0,y0,x1,y1,...].
func flatPoints(pts []Point) []float64 {
	if len(pts) == 0 {
		return nil
	}
	flat := make([]float64, 0, 2*len(pts))
	for _, p := range pts {
		flat = append(flat, p.X, p.Y)
	}
	return flat
}

// ImageInfo is one image in the opened folder.
type ImageInfo struct {
	ID      int64  `json:"-"`    // store row id, runtime only
	Path    string `json:"-"`    // absolute path, runtime only (= dir + RelPath)
	RelPath string `json:"file"` // path relative to the folder, slash-separated
	Name    string `json:"-"`    // base name, display only
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Status  string `json:"status,omitempty"` // new|labeling|done|reviewed|skipped
	Anns    []Ann  `json:"anns"`
	// SHA256 is the content hash the scan recorded (after EXIF normalization).
	// The export writes it into the IR; the exchange document has no place for
	// it, so it stays runtime only.
	SHA256 string `json:"-"`
}

// annsFromRecs converts stored annotation rows into app annotations. Angles are
// radians in both representations. A polygon's vertices are authoritative, so
// its bounding box is recomputed rather than trusted.
func annsFromRecs(recs []store.AnnRec) []Ann {
	if len(recs) == 0 {
		return nil
	}
	anns := make([]Ann, len(recs))
	for i, rec := range recs {
		kind := KindRect
		if rec.Kind == store.KindPoly {
			kind = KindPoly
		}
		anns[i] = Ann{ID: rec.ID, Cat: rec.Cat, Kind: kind, CX: rec.CX, CY: rec.CY, W: rec.W, H: rec.H, Angle: rec.Angle, Attributes: cloneAttrs(rec.Attrs)}
		if kind == KindPoly {
			anns[i].Points = pointsFromFlat(rec.Points)
			syncPolyBounds(&anns[i])
		}
	}
	return anns
}

// annsToRecs converts app annotations into stored annotation rows. A polygon
// stores its vertices plus the bounding box of those vertices with Angle 0
// (docs/phase2-design.md §2.1). Attributes are copied into the row, so the
// store never holds a map the live annotation can still edit.
func annsToRecs(anns []Ann) []store.AnnRec {
	if len(anns) == 0 {
		return nil
	}
	recs := make([]store.AnnRec, len(anns))
	for i, ann := range anns {
		recs[i] = store.AnnRec{ID: ann.ID, Cat: ann.Cat, Kind: KindRect, CX: ann.CX, CY: ann.CY, W: ann.W, H: ann.H, Angle: ann.Angle, Attrs: cloneAttrs(ann.Attributes)}
		if isPoly(&ann) {
			x, y, w, h := polygonBBox(ann.Points)
			recs[i].Kind = KindPoly
			recs[i].CX, recs[i].CY, recs[i].W, recs[i].H, recs[i].Angle = x+w/2, y+h/2, w, h, 0
			recs[i].Points = flatPoints(ann.Points)
		}
	}
	return recs
}

// Category is one annotation category, re-exported from the store so the app
// model and the database agree on the fields (docs/phase2-design.md §2.3). An
// empty Color means "the generated palette color of this index".
type Category = store.Category

// categoriesFromNames wraps plain category names for the store: the legacy
// import has names only, so every category keeps the generated palette color
// (docs/phase2-design.md §6).
func categoriesFromNames(names []string) []Category {
	cats := make([]Category, len(names))
	for i, name := range names {
		cats[i] = Category{Name: name}
	}
	return cats
}

// docFile is the PoC annotation document reached through
// <dir>/mylabel.annotations.json: since phase 1 it is a one-time import
// source (legacy import) and the format of the toolbar's "导出 JSON".
// Its "file" key holds ImageInfo.RelPath.
type docFile struct {
	IRVersion  string      `json:"ir_version"`
	Categories []string    `json:"categories"`
	Images     []ImageInfo `json:"images"`
}

const docIRVersion = "0.1-poc"

// defaultCategories is the nine-category fixture tests inject when they need a
// workspace that can create annotations: a new workspace starts with no
// categories at all, so the app itself never fills this list in. The slots carry
// no custom colors, so they take the generated palette colors.
var defaultCategories = []Category{
	{Name: "cat1"}, {Name: "cat2"}, {Name: "cat3"},
	{Name: "cat4"}, {Name: "cat5"}, {Name: "cat6"},
	{Name: "cat7"}, {Name: "cat8"}, {Name: "cat9"},
}

// defaultCategoryList returns a fresh copy of the fixture categories: the App
// edits its list in place, so each caller must own its slice.
func defaultCategoryList() []Category {
	return append([]Category(nil), defaultCategories...)
}

var annIDCounter atomic.Uint64

func newAnnID() string {
	return fmt.Sprintf("ann_%d", annIDCounter.Add(1))
}

// seedAnnIDCounter advances the ID counter past every "ann_N" suffix already
// present in images. Without it a fresh process starts again at ann_1 and
// collides with annotations created by earlier sessions.
func seedAnnIDCounter(images []ImageInfo) {
	var max uint64
	for i := range images {
		for _, ann := range images[i].Anns {
			if n, ok := strings.CutPrefix(ann.ID, "ann_"); ok {
				if v, err := strconv.ParseUint(n, 10, 64); err == nil && v > max {
					max = v
				}
			}
		}
	}
	for {
		cur := annIDCounter.Load()
		if cur >= max || annIDCounter.CompareAndSwap(cur, max) {
			return
		}
	}
}

// ensureUniqueAnnIDs renames annotations whose ID is empty or duplicated
// within anns, preserving order. Imported documents (PoC JSON) reuse IDs
// across sessions, so duplicates are expected there, not an error.
func ensureUniqueAnnIDs(anns []Ann) {
	seen := make(map[string]bool, len(anns))
	for i := range anns {
		if anns[i].ID == "" || seen[anns[i].ID] {
			anns[i].ID = newAnnID()
		}
		seen[anns[i].ID] = true
	}
}

// Mode is the canvas interaction state machine state.
type Mode int

const (
	ModeIdle Mode = iota
	ModeCreating
	ModeMove
	ModeHandle
	ModePan
	// ModePolyCreate draws a draft vertex by vertex: a polygon (Enter, a click
	// on the first vertex or a double-click closes it, docs/phase2-design.md
	// §4.2), or a rotated box, whose fourth click makes it (§5.2).
	ModePolyCreate
	// ModeMarquee drags a selection rectangle over the canvas: Shift+drag starts
	// one on empty canvas in any tool, and the marquee tool starts one anywhere;
	// releasing selects every annotation whose box it touches
	// (docs/phase2-design.md §5, docs/phase6-design.md §3).
	ModeMarquee
	// ModeZoomClick arms one click of the zoom tool: a release that did not
	// travel zooms around the press point, a press that moved is a drag this
	// phase leaves undefined (docs/phase6-design.md §3).
	ModeZoomClick
)

// Tool is the tool the strip selects, one at a time (docs/phase6-design.md §1):
// the three annotation tools draw rectangles (docs/phase2-design.md §4.1),
// polygons (§4.2) and rotated boxes (§5.2); the three view tools pan, zoom and
// select with a rectangle without touching the annotations themselves.
type Tool int

const (
	// The annotation tools come first, so the zero Tool is the default one.
	ToolRect Tool = iota
	ToolPoly
	ToolObb
	// ToolPan drags the image with the left button, as Space does in any tool.
	ToolPan
	// ToolZoom zooms in around a click, out with Alt held.
	ToolZoom
	// ToolMarquee drags a selection rectangle without Shift.
	ToolMarquee
)

// dragState carries the in-progress drag information.
type dragState struct {
	annIndex int   // index into current image's Anns
	handle   int   // 0..7 resize (corners TL,TR,BR,BL then edges T,R,B,L), 8 rotate
	start    Point // pointer position at drag start, image pixels
	before   Ann   // snapshot for undo
	// group holds one deep snapshot per annotation a ModeMove drag moves: a
	// single one for a plain drag, every selected annotation for a
	// multi-selection move (§5). ModeHandle edits one annotation and uses
	// before instead.
	group []annSnapshot
	// clickProps marks a body press that arrived as the second click of a
	// double-click: released without dragging, it opens the properties dialog
	// instead of moving anything (§5.1).
	clickProps bool
}

// annSnapshot is one annotation's pre-drag state, as a group move restores and
// re-computes it.
type annSnapshot struct {
	index  int
	before Ann
}
