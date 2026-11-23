package exporter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mylabel/internal/ir"
)

// --- decode helpers (mirror the wire schema independently of coco.go) ----

type cocoTestInfo struct {
	Description   string  `json:"description"`
	Version       string  `json:"version"`
	DateGenerated *string `json:"date_generated"`
}

type cocoTestImage struct {
	ID       int    `json:"id"`
	FileName string `json:"file_name"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

type cocoTestCategory struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	Supercategory string `json:"supercategory"`
}

type cocoTestOBB struct {
	Points [][2]float64 `json:"points"`
	Angle  float64      `json:"angle"`
}

type cocoTestAnn struct {
	ID           int               `json:"id"`
	ImageID      int               `json:"image_id"`
	CategoryID   int               `json:"category_id"`
	BBox         []float64         `json:"bbox"`
	Area         float64           `json:"area"`
	Segmentation [][]float64       `json:"segmentation"`
	ISCrowd      int               `json:"iscrowd"`
	OBB          *cocoTestOBB      `json:"mylabel_obb"`
	Attributes   map[string]string `json:"mylabel_attributes"`
}

type cocoTestDoc struct {
	Info        cocoTestInfo       `json:"info"`
	Images      []cocoTestImage    `json:"images"`
	Annotations []cocoTestAnn      `json:"annotations"`
	Categories  []cocoTestCategory `json:"categories"`
}

// cocoTestRawDoc keeps object keys as raw values so tests can tell a missing
// key from a zero value.
type cocoTestRawDoc struct {
	Info        map[string]json.RawMessage   `json:"info"`
	Images      []map[string]json.RawMessage `json:"images"`
	Annotations []map[string]json.RawMessage `json:"annotations"`
	Categories  []map[string]json.RawMessage `json:"categories"`
}

const cocoEps = 1e-6

func cocoClose(got, want float64) bool {
	d := got - want
	return d < cocoEps && d > -cocoEps
}

func cocoAssertClose(t *testing.T, got, want float64, what string) {
	t.Helper()
	if !cocoClose(got, want) {
		t.Errorf("%s = %v, want %v (±%v)", what, got, want, cocoEps)
	}
}

func cocoAssertFloats(t *testing.T, got, want []float64, what string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s length = %d (%v), want %d (%v)", what, len(got), got, len(want), want)
	}
	for i := range want {
		if !cocoClose(got[i], want[i]) {
			t.Errorf("%s[%d] = %v, want %v (±%v)", what, i, got[i], want[i], cocoEps)
		}
	}
}

// cocoPlanFile returns the plan output file with the given relative path.
func cocoPlanFile(t *testing.T, plan *Plan, rel string) []byte {
	t.Helper()
	for _, f := range plan.Files {
		if f.Rel == rel {
			return f.Data
		}
	}
	var have []string
	for _, f := range plan.Files {
		have = append(have, f.Rel)
	}
	t.Fatalf("plan has no file %q, have %v", rel, have)
	return nil
}

func cocoPlanHasFile(plan *Plan, rel string) bool {
	for _, f := range plan.Files {
		if f.Rel == rel {
			return true
		}
	}
	return false
}

func cocoParseDoc(t *testing.T, data []byte) cocoTestDoc {
	t.Helper()
	var doc cocoTestDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("COCO JSON is not valid: %v\n%s", err, data)
	}
	return doc
}

// cocoAnnsOf returns the annotations of the named image.
func cocoAnnsOf(t *testing.T, doc cocoTestDoc, file string) []cocoTestAnn {
	t.Helper()
	id := 0
	for _, img := range doc.Images {
		if img.FileName == file {
			id = img.ID
		}
	}
	if id == 0 {
		t.Fatalf("image %q not in COCO images", file)
	}
	var out []cocoTestAnn
	for _, a := range doc.Annotations {
		if a.ImageID == id {
			out = append(out, a)
		}
	}
	return out
}

// cocoLossyCount returns how often a rule fired, 0 when it never did.
func cocoLossyCount(plan *Plan, rule string) int {
	for _, st := range plan.Report.Lossy {
		if st.Rule == rule {
			return st.Count
		}
	}
	return 0
}

func cocoWarnedAbout(plan *Plan, substr string) bool {
	for _, w := range plan.Report.Warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

func cocoIntPtr(v int) *int { return &v }

// --- categories and ids --------------------------------------------------

func TestBuildCOCOPlanCategoriesAndIDs(t *testing.T) {
	proj := &ir.Project{
		Name: "demo",
		Categories: []ir.Category{
			{Key: "person", Name: "person", SuperCategory: "human", COCOID: cocoIntPtr(17)},
			{Key: "bike", Name: "bike"},
			{Key: "car", Name: "car", COCOID: cocoIntPtr(90)},
		},
	}
	images := []ir.Image{
		{File: "b.jpg", Width: 40, Height: 30, Annotations: []ir.Annotation{
			{ID: "b1", Category: "person", Kind: ir.KindBBox, X: 1, Y: 2, W: 10, H: 10},
		}},
		{File: "a.jpg", Width: 20, Height: 10, Annotations: []ir.Annotation{
			{ID: "a1", Category: "car", Kind: ir.KindBBox, X: 0, Y: 0, W: 5, H: 5},
		}},
	}
	plan, err := BuildPlan(proj, images, Options{Format: FormatCOCO})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.Opts.Format != FormatCOCO {
		t.Fatalf("plan.Opts.Format = %q, want coco", plan.Opts.Format)
	}

	// COCOID is kept when present, otherwise the 1-based position is used;
	// Plan.Categories carries both.
	if len(plan.Categories) != 3 {
		t.Fatalf("plan categories = %d, want 3", len(plan.Categories))
	}
	wantIDs := []int{17, 2, 90}
	for i, want := range wantIDs {
		c := plan.Categories[i]
		if c.Index != i {
			t.Errorf("categories[%d].Index = %d, want %d", i, c.Index, i)
		}
		if c.COCOID == nil || *c.COCOID != want {
			t.Errorf("categories[%d].COCOID = %v, want %d", i, c.COCOID, want)
		}
	}
	// The input must not be mutated and must not be aliased by the plan.
	if proj.Categories[1].COCOID != nil {
		t.Errorf("input category mutated: COCOID = %v, want nil", *proj.Categories[1].COCOID)
	}
	if plan.Categories[0].COCOID == proj.Categories[0].COCOID {
		t.Error("plan categories must not share COCOID pointers with the input")
	}
	if *proj.Categories[0].COCOID != 17 {
		t.Errorf("input COCOID = %d, want 17 (unchanged)", *proj.Categories[0].COCOID)
	}

	doc := cocoParseDoc(t, cocoPlanFile(t, plan, "annotations/instances_train.json"))
	if doc.Info.Version != ir.Version || doc.Info.Description != "demo" {
		t.Errorf("info = %+v, want description demo version %s", doc.Info, ir.Version)
	}
	if doc.Info.DateGenerated != nil {
		t.Errorf("info.date_generated must be omitted, got %q", *doc.Info.DateGenerated)
	}
	if len(doc.Categories) != 3 {
		t.Fatalf("categories = %d, want 3", len(doc.Categories))
	}
	for i, want := range wantIDs {
		if doc.Categories[i].ID != want {
			t.Errorf("categories[%d].id = %d, want %d", i, doc.Categories[i].ID, want)
		}
	}
	if doc.Categories[0].Name != "person" || doc.Categories[0].Supercategory != "human" {
		t.Errorf("categories[0] = %+v, want person/human", doc.Categories[0])
	}

	// Images are numbered from 1 in sorted File order, annotations from 1
	// across the whole file.
	if len(doc.Images) != 2 {
		t.Fatalf("images = %d, want 2", len(doc.Images))
	}
	if doc.Images[0].FileName != "a.jpg" || doc.Images[0].ID != 1 {
		t.Errorf("images[0] = %+v, want a.jpg id 1", doc.Images[0])
	}
	if doc.Images[1].FileName != "b.jpg" || doc.Images[1].ID != 2 {
		t.Errorf("images[1] = %+v, want b.jpg id 2", doc.Images[1])
	}
	if got := cocoAnnsOf(t, doc, "a.jpg"); len(got) != 1 || got[0].ID != 1 || got[0].CategoryID != 90 {
		t.Errorf("a.jpg ann = %+v, want id 1 category_id 90", got)
	}
	if got := cocoAnnsOf(t, doc, "b.jpg"); len(got) != 1 || got[0].ID != 2 || got[0].CategoryID != 17 {
		t.Errorf("b.jpg ann = %+v, want id 2 category_id 17", got)
	}

	// Copies mirror the source path under images/<split>/.
	wantCopies := []ImageCopy{
		{Src: "a.jpg", Dst: "images/train/a.jpg"},
		{Src: "b.jpg", Dst: "images/train/b.jpg"},
	}
	if len(plan.Copies) != len(wantCopies) {
		t.Fatalf("copies = %+v, want %+v", plan.Copies, wantCopies)
	}
	for i, want := range wantCopies {
		if plan.Copies[i] != want {
			t.Errorf("copies[%d] = %+v, want %+v", i, plan.Copies[i], want)
		}
	}
	if plan.Report.Images != 2 || plan.Report.Annotations != 2 || plan.Report.AnnotationsDropped != 0 {
		t.Errorf("report = %+v, want 2 images / 2 annotations / 0 dropped", plan.Report)
	}
	if plan.Report.ImagesWithAnns != 2 {
		t.Errorf("images with annotations = %d, want 2", plan.Report.ImagesWithAnns)
	}
}

// --- segmentation union and extensions -----------------------------------

func TestBuildCOCOPlanSegmentationUnion(t *testing.T) {
	proj := &ir.Project{
		Categories: []ir.Category{
			{Key: "thing", Name: "thing"},
			{Key: "box", Name: "box"},
		},
	}
	rotated := ir.Annotation{
		ID: "rot", Category: "box", Kind: ir.KindRotated,
		Points: [][2]float64{{100, 100}, {140, 110}, {130, 145}, {90, 135}},
		CX:     115, CY: 122.5, RW: 41, RH: 36, Angle: 0.25,
	}
	images := []ir.Image{{
		File: "shapes.png", Width: 200, Height: 200,
		Annotations: []ir.Annotation{
			{ID: "det", Category: "thing", Kind: ir.KindBBox, X: 10.5, Y: 20.25, W: 30, H: 40},
			{ID: "poly", Category: "thing", Kind: ir.KindPolygon, Rings: [][][2]float64{
				{{1, 1}, {9, 1}, {9, 9}, {1, 9}},
				{{20, 20}, {30, 20}, {30, 30}, {20, 30}},
			}},
			rotated,
			{ID: "attr", Category: "thing", Kind: ir.KindBBox, X: 60, Y: 60, W: 10, H: 10,
				Attributes: map[string]string{"difficult": "1"}},
		},
	}}
	plan, err := BuildPlan(proj, images, Options{Format: FormatCOCO})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	data := cocoPlanFile(t, plan, "annotations/instances_train.json")
	doc := cocoParseDoc(t, data)
	if len(doc.Annotations) != 4 {
		t.Fatalf("annotations = %d, want 4", len(doc.Annotations))
	}

	// bbox: absolute top-left origin pixels + a four-corner segmentation.
	det := doc.Annotations[0]
	if det.ID != 1 {
		t.Errorf("first annotation id = %d, want 1", det.ID)
	}
	cocoAssertFloats(t, det.BBox, []float64{10.5, 20.25, 30, 40}, "bbox ann bbox")
	cocoAssertClose(t, det.Area, 30*40, "bbox ann area")
	if det.ISCrowd != 0 {
		t.Errorf("iscrowd = %d, want 0", det.ISCrowd)
	}
	if len(det.Segmentation) != 1 {
		t.Fatalf("bbox segmentation parts = %d, want 1", len(det.Segmentation))
	}
	cocoAssertFloats(t, det.Segmentation[0],
		[]float64{10.5, 20.25, 40.5, 20.25, 40.5, 60.25, 10.5, 60.25}, "bbox ann segmentation")
	if det.OBB != nil {
		t.Errorf("bbox ann must not carry mylabel_obb: %+v", det.OBB)
	}
	if cocoLossyCount(plan, RuleDetToSeg) != 2 {
		t.Errorf("RuleDetToSeg count = %d, want 2 (both bbox annotations)", cocoLossyCount(plan, RuleDetToSeg))
	}

	// polygon: every ring kept as its own segmentation part, flattened.
	poly := doc.Annotations[1]
	if len(poly.Segmentation) != 2 {
		t.Fatalf("polygon segmentation parts = %d, want 2 (all rings kept)", len(poly.Segmentation))
	}
	cocoAssertFloats(t, poly.Segmentation[0], []float64{1, 1, 9, 1, 9, 9, 1, 9}, "polygon ring 0")
	cocoAssertFloats(t, poly.Segmentation[1], []float64{20, 20, 30, 20, 30, 30, 20, 30}, "polygon ring 1")
	cocoAssertFloats(t, poly.BBox, []float64{1, 1, 29, 29}, "polygon bbox")
	cocoAssertClose(t, poly.Area, 29*29, "polygon area")
	if poly.ISCrowd != 0 {
		t.Errorf("polygon iscrowd = %d, want 0", poly.ISCrowd)
	}

	// rotated: four-point segmentation + enclosing bbox + private extension.
	obb := doc.Annotations[2]
	if obb.OBB == nil {
		t.Fatalf("rotated ann lost mylabel_obb: %+v", obb)
	}
	if len(obb.OBB.Points) != 4 {
		t.Fatalf("mylabel_obb points = %d, want 4", len(obb.OBB.Points))
	}
	for i, want := range rotated.Points {
		cocoAssertFloats(t, []float64{obb.OBB.Points[i][0], obb.OBB.Points[i][1]},
			[]float64{want[0], want[1]}, "mylabel_obb point")
	}
	cocoAssertClose(t, obb.OBB.Angle, 0.25, "mylabel_obb angle")
	cocoAssertFloats(t, obb.BBox, []float64{90, 100, 50, 45}, "rotated bbox")
	cocoAssertClose(t, obb.Area, 50*45, "rotated area")
	if len(obb.Segmentation) != 1 || len(obb.Segmentation[0]) != 8 {
		t.Fatalf("rotated segmentation = %v, want one flat 4-point ring", obb.Segmentation)
	}
	cocoAssertFloats(t, obb.Segmentation[0], []float64{100, 100, 140, 110, 130, 145, 90, 135}, "rotated segmentation")
	if cocoLossyCount(plan, RuleObbToCOCO) != 1 {
		t.Errorf("RuleObbToCOCO count = %d, want 1", cocoLossyCount(plan, RuleObbToCOCO))
	}

	// Attributes ride along in the private mylabel_attributes object.
	attr := doc.Annotations[3]
	if attr.Attributes["difficult"] != "1" {
		t.Errorf("attributes = %v, want difficult=1", attr.Attributes)
	}
	if doc.Annotations[0].Attributes != nil {
		t.Errorf("annotation without attributes must omit the key, got %v", doc.Annotations[0].Attributes)
	}

	if !cocoWarnedAbout(plan, "mylabel_obb") || !cocoWarnedAbout(plan, "mylabel_attributes") {
		t.Errorf("warnings must declare the non-standard extensions once, got %v", plan.Report.Warnings)
	}
	notes := 0
	for _, w := range plan.Report.Warnings {
		if strings.Contains(w, "非标准") {
			notes++
		}
	}
	if notes != 1 {
		t.Errorf("non-standard note appears %d times, want 1 (%v)", notes, plan.Report.Warnings)
	}

	// iscrowd is always written, even when zero; mylabel_obb/attributes are
	// omitted rather than null for annotations that do not use them.
	var raw cocoTestRawDoc
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("raw decode: %v", err)
	}
	if _, ok := raw.Annotations[0]["iscrowd"]; !ok {
		t.Error("annotation JSON is missing iscrowd")
	}
	if _, ok := raw.Annotations[0]["mylabel_obb"]; ok {
		t.Error("bbox annotation should omit mylabel_obb")
	}
	if _, ok := raw.Annotations[0]["mylabel_attributes"]; ok {
		t.Error("attribute-less annotation should omit mylabel_attributes")
	}
	if _, ok := raw.Annotations[2]["mylabel_obb"]; !ok {
		t.Error("rotated annotation is missing mylabel_obb")
	}
	if _, ok := raw.Info["date_generated"]; ok {
		t.Error("info must omit date_generated")
	}
	// Compact output: no indentation, no trailing newline.
	if strings.Contains(string(data), "\n") || strings.Contains(string(data), `"images": `) {
		t.Errorf("COCO JSON must be compact, got indented output: %s", data)
	}
}

// --- splits --------------------------------------------------------------

func TestBuildCOCOPlanPerSplitFiles(t *testing.T) {
	proj := &ir.Project{Categories: []ir.Category{{Key: "c", Name: "c"}}}
	images := []ir.Image{
		{File: "train/x.jpg", Width: 10, Height: 10, Annotations: []ir.Annotation{
			{ID: "1", Category: "c", Kind: ir.KindBBox, X: 1, Y: 1, W: 2, H: 2},
		}},
		{File: "val/y.jpg", Width: 10, Height: 10, Annotations: []ir.Annotation{
			{ID: "2", Category: "c", Kind: ir.KindBBox, X: 1, Y: 1, W: 2, H: 2},
		}},
		{File: "root/w.jpg", Width: 10, Height: 10},
	}
	plan, err := BuildPlan(proj, images, Options{Format: FormatCOCO})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if !cocoPlanHasFile(plan, "annotations/instances_train.json") || !cocoPlanHasFile(plan, "annotations/instances_val.json") {
		t.Fatalf("per-split files missing, have %v", plan.Files)
	}
	if hasVal := plan.Splits["val/y.jpg"]; hasVal != "val" {
		t.Errorf("split of val/y.jpg = %q, want val", hasVal)
	}

	train := cocoParseDoc(t, cocoPlanFile(t, plan, "annotations/instances_train.json"))
	if len(train.Images) != 2 {
		t.Fatalf("train images = %d, want 2", len(train.Images))
	}
	// Numbering restarts in every file, in sorted File order.
	if train.Images[0].FileName != "root/w.jpg" || train.Images[0].ID != 1 {
		t.Errorf("train images[0] = %+v, want root/w.jpg id 1", train.Images[0])
	}
	if train.Images[1].FileName != "x.jpg" || train.Images[1].ID != 2 {
		t.Errorf("train images[1] = %+v, want x.jpg id 2", train.Images[1])
	}
	if len(train.Annotations) != 1 || train.Annotations[0].ID != 1 || train.Annotations[0].ImageID != 2 {
		t.Errorf("train annotations = %+v, want one ann id 1 on image 2", train.Annotations)
	}
	val := cocoParseDoc(t, cocoPlanFile(t, plan, "annotations/instances_val.json"))
	if len(val.Images) != 1 || val.Images[0].FileName != "y.jpg" || val.Images[0].ID != 1 {
		t.Errorf("val images = %+v, want y.jpg id 1", val.Images)
	}
	if len(val.Annotations) != 1 || val.Annotations[0].ImageID != 1 {
		t.Errorf("val annotations = %+v", val.Annotations)
	}

	// Copies mirror the relative path under images/<split>/, with a leading
	// source split segment stripped (no doubled train/train).
	wantDst := map[string]string{
		"train/x.jpg": "images/train/x.jpg",
		"val/y.jpg":   "images/val/y.jpg",
		"root/w.jpg":  "images/train/root/w.jpg",
	}
	for _, c := range plan.Copies {
		if want := wantDst[c.Src]; c.Dst != want {
			t.Errorf("copy %q dst = %q, want %q", c.Src, c.Dst, want)
		}
	}
}

func TestBuildCOCOPlanOmitsEmptySplit(t *testing.T) {
	proj := &ir.Project{Categories: []ir.Category{{Key: "c", Name: "c"}}}
	images := []ir.Image{
		{File: "a.jpg", Width: 10, Height: 10},
		{File: "b.jpg", Width: 10, Height: 10},
	}
	plan, err := BuildPlan(proj, images, Options{Format: FormatCOCO})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(plan.Files) != 1 || plan.Files[0].Rel != "annotations/instances_train.json" {
		t.Fatalf("files = %+v, want only instances_train.json (empty val split omitted)", plan.Files)
	}
	// An image without annotations still has an images entry, and the
	// annotations array stays an empty JSON array rather than null.
	data := plan.Files[0].Data
	doc := cocoParseDoc(t, data)
	if len(doc.Images) != 2 {
		t.Errorf("images = %d, want 2 (annotation-less images are listed)", len(doc.Images))
	}
	if len(doc.Annotations) != 0 {
		t.Errorf("annotations = %+v, want none", doc.Annotations)
	}
	if !strings.Contains(string(data), `"annotations":[]`) {
		t.Errorf("empty annotations must serialize as [], got %s", data)
	}
	if plan.Report.ImagesWithAnns != 0 {
		t.Errorf("images with annotations = %d, want 0", plan.Report.ImagesWithAnns)
	}
}

// --- clipping and drops --------------------------------------------------

func TestBuildCOCOPlanClipsAndDrops(t *testing.T) {
	proj := &ir.Project{Categories: []ir.Category{{Key: "c", Name: "c"}}}
	images := []ir.Image{{
		File: "clip.jpg", Width: 100, Height: 50,
		Annotations: []ir.Annotation{
			// -10,-5,30,20 clips to 0,0,20,15.
			{ID: "clip", Category: "c", Kind: ir.KindBBox, X: -10, Y: -5, W: 30, H: 20},
			// Entirely outside the image: nothing survives.
			{ID: "gone", Category: "c", Kind: ir.KindBBox, X: 200, Y: 200, W: 10, H: 10},
			{ID: "ghost", Category: "nope", Kind: ir.KindBBox, X: 1, Y: 1, W: 5, H: 5},
			{ID: "badquad", Category: "c", Kind: ir.KindRotated, Points: [][2]float64{{0, 0}, {1, 1}, {2, 2}}},
			{ID: "thinpoly", Category: "c", Kind: ir.KindPolygon, Rings: [][][2]float64{{{0, 0}, {5, 5}}}},
		},
	}}
	plan, err := BuildPlan(proj, images, Options{Format: FormatCOCO})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	doc := cocoParseDoc(t, cocoPlanFile(t, plan, "annotations/instances_train.json"))
	if len(doc.Annotations) != 1 {
		t.Fatalf("annotations = %+v, want only the clipped one", doc.Annotations)
	}
	cocoAssertFloats(t, doc.Annotations[0].BBox, []float64{0, 0, 20, 15}, "clipped bbox")
	cocoAssertClose(t, doc.Annotations[0].Area, 20*15, "clipped area")

	if got := cocoLossyCount(plan, RuleClipped); got != 1 {
		t.Errorf("RuleClipped = %d, want 1", got)
	}
	if got := cocoLossyCount(plan, RuleDropped); got != 4 {
		t.Errorf("RuleDropped = %d, want 4 (%+v)", got, plan.Report.Lossy)
	}
	if plan.Report.Annotations != 1 || plan.Report.AnnotationsDropped != 4 {
		t.Errorf("report = %+v, want 1 kept / 4 dropped", plan.Report)
	}
	if plan.Report.ImagesWithAnns != 1 {
		t.Errorf("images with annotations = %d, want 1", plan.Report.ImagesWithAnns)
	}
	// Samples are capped and locatable.
	for _, st := range plan.Report.Lossy {
		if len(st.Samples) > maxSamples {
			t.Errorf("rule %q has %d samples, want <= %d", st.Rule, len(st.Samples), maxSamples)
		}
		for _, s := range st.Samples {
			if s.File != "clip.jpg" {
				t.Errorf("sample not locatable: %+v", s)
			}
		}
	}
}

// --- integration ---------------------------------------------------------

func TestWriteCOCOExportIntegration(t *testing.T) {
	root := t.TempDir()
	imgRoot := filepath.Join(root, "imgs")
	for _, f := range []string{"a.jpg", "val/b.png"} {
		p := filepath.Join(imgRoot, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("img:"+f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	proj := &ir.Project{
		Name:       "integration",
		Categories: []ir.Category{{Key: "dog", Name: "dog", COCOID: cocoIntPtr(3)}},
	}
	images := []ir.Image{
		{File: "a.jpg", Width: 100, Height: 80, Annotations: []ir.Annotation{
			{ID: "1", Category: "dog", Kind: ir.KindBBox, X: 10, Y: 20, W: 30, H: 40},
		}},
		{File: "val/b.png", Width: 50, Height: 50, Annotations: []ir.Annotation{
			{ID: "2", Category: "dog", Kind: ir.KindPolygon, Rings: [][][2]float64{{{1, 2}, {10, 2}, {10, 20}}}},
		}},
	}
	plan, err := BuildPlan(proj, images, Options{Format: FormatCOCO})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	out := filepath.Join(root, "out")

	var lastDone, lastTotal int
	if err := WriteExport(out, imgRoot, plan, func(done, total int) { lastDone, lastTotal = done, total }); err != nil {
		t.Fatalf("WriteExport: %v", err)
	}
	wantTotal := len(plan.Copies) + len(plan.Files) + 1
	if lastTotal != wantTotal || lastDone != wantTotal {
		t.Errorf("progress = %d/%d, want %d/%d", lastDone, lastTotal, wantTotal, wantTotal)
	}

	// Directory structure.
	for _, rel := range []string{
		"images/train/a.jpg",
		"images/val/b.png",
		"annotations/instances_train.json",
		"annotations/instances_val.json",
		manifestName,
	} {
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("missing %s in export: %v", rel, err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(out, "images", "train", "a.jpg")); err != nil || string(data) != "img:a.jpg" {
		t.Errorf("copied image = %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(out, "annotations", "instances_test.json")); !os.IsNotExist(err) {
		t.Errorf("unexpected split file, stat err = %v", err)
	}
	if _, err := os.Stat(out + tmpSuffix); !os.IsNotExist(err) {
		t.Errorf("tmp dir should be gone, stat err = %v", err)
	}

	trainData, err := os.ReadFile(filepath.Join(out, "annotations", "instances_train.json"))
	if err != nil {
		t.Fatalf("read instances_train.json: %v", err)
	}
	train := cocoParseDoc(t, trainData)
	if len(train.Images) != 1 || train.Images[0].FileName != "a.jpg" || train.Images[0].Width != 100 || train.Images[0].Height != 80 {
		t.Fatalf("train images = %+v", train.Images)
	}
	if len(train.Annotations) != 1 {
		t.Fatalf("train annotations = %+v", train.Annotations)
	}
	ann := train.Annotations[0]
	cocoAssertFloats(t, ann.BBox, []float64{10, 20, 30, 40}, "train bbox")
	if ann.CategoryID != 3 || ann.ImageID != 1 || ann.ISCrowd != 0 {
		t.Errorf("train annotation = %+v, want category 3 image 1 iscrowd 0", ann)
	}
	if len(train.Categories) != 1 || train.Categories[0].ID != 3 || train.Categories[0].Name != "dog" {
		t.Errorf("train categories = %+v", train.Categories)
	}

	valData, err := os.ReadFile(filepath.Join(out, "annotations", "instances_val.json"))
	if err != nil {
		t.Fatalf("read instances_val.json: %v", err)
	}
	val := cocoParseDoc(t, valData)
	if len(val.Annotations) != 1 || len(val.Annotations[0].Segmentation) != 1 {
		t.Fatalf("val annotations = %+v", val.Annotations)
	}
	cocoAssertFloats(t, val.Annotations[0].Segmentation[0], []float64{1, 2, 10, 2, 10, 20}, "val segmentation")
	cocoAssertFloats(t, val.Annotations[0].BBox, []float64{1, 2, 9, 18}, "val bbox")

	// Manifest mirrors the plan.
	raw, err := os.ReadFile(filepath.Join(out, manifestName))
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	var doc manifestDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("manifest is not JSON: %v", err)
	}
	if doc.Tool != "mylabel" || doc.Format != FormatCOCO || doc.IRVersion != ir.Version {
		t.Errorf("manifest header = %+v", doc)
	}
	if !strings.Contains(doc.CoordinateBasis, "绝对像素") {
		t.Errorf("coordinate_basis = %q, want absolute pixels", doc.CoordinateBasis)
	}
	if len(doc.Categories) != 1 || doc.Categories[0].COCOID == nil || *doc.Categories[0].COCOID != 3 {
		t.Errorf("manifest categories = %+v, want coco_id 3", doc.Categories)
	}
	if doc.Report.Images != 2 || doc.Report.Annotations != 2 || doc.Report.AnnotationsDropped != 0 {
		t.Errorf("manifest report = %+v, want 2/2/0", doc.Report)
	}
	found := false
	for _, note := range doc.NonstandardNotes {
		if strings.Contains(note, "mylabel_obb") {
			found = true
		}
	}
	if !found {
		t.Errorf("manifest nonstandard_notes = %v, want the mylabel_obb declaration", doc.NonstandardNotes)
	}
}
