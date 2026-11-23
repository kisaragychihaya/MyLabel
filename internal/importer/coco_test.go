package importer

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"mylabel/internal/ir"
)

// --- helpers -------------------------------------------------------------

func cocoWriteFixture(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// cocoParseFixture writes content to a temp COCO JSON file and parses it.
func cocoParseFixture(t *testing.T, content string) (*Dataset, []ir.Issue) {
	t.Helper()
	path := cocoWriteFixture(t, t.TempDir(), "coco.json", content)
	ds, issues, err := parseCOCO(path)
	if err != nil {
		t.Fatalf("parseCOCO: %v", err)
	}
	if ds == nil {
		t.Fatal("parseCOCO returned a nil dataset")
	}
	return ds, issues
}

func cocoImageByFile(t *testing.T, ds *Dataset, file string) ir.Image {
	t.Helper()
	for _, img := range ds.Images {
		if img.File == file {
			return img
		}
	}
	var have []string
	for _, img := range ds.Images {
		have = append(have, img.File)
	}
	t.Fatalf("image %q not in dataset, have %v", file, have)
	return ir.Image{}
}

func cocoWarningsContaining(t *testing.T, ws []ir.Issue, substr string) []ir.Issue {
	t.Helper()
	var out []ir.Issue
	for _, w := range ws {
		if strings.Contains(w.Message, substr) {
			out = append(out, w)
		}
	}
	return out
}

func cocoRingEqual(a, b [][2]float64) bool { return reflect.DeepEqual(a, b) }

// cocoEncodeCountsString is the test-side encoder mirroring pycocotools
// rleToString, used to cross-check decodeRLEString: counts from index 3 on are
// delta encoded against counts[i-2], then each value is split into 5-bit
// chunks, low chunk first, with continuation bit 0x20 and ascii offset '0'.
// The loop stops once the remaining value and the chunk's 0x10 (sign) bit
// agree, so the decoder can sign extend the terminating chunk.
func cocoEncodeCountsString(counts []int64) string {
	var b strings.Builder
	for i, n := range counts {
		x := n
		if i > 2 {
			x -= counts[i-2]
		}
		for more := true; more; {
			c := byte(x & 0x1f)
			x >>= 5
			more = !((x == 0 && c&0x10 == 0) || (x == -1 && c&0x10 != 0))
			if more {
				c |= 0x20
			}
			b.WriteByte(c + '0')
		}
	}
	return b.String()
}

// --- full dataset --------------------------------------------------------

func TestParseCOCOFullDataset(t *testing.T) {
	ds, issues := cocoParseFixture(t, `{
	  "info": {"description": "unit test", "version": "1.0"},
	  "licenses": [{"id": 1, "name": "CC-BY"}],
	  "images": [
	    {"id": 1, "file_name": "a.jpg", "width": 640, "height": 480},
	    {"id": 2, "file_name": "sub/b.png", "width": 200, "height": 100},
	    {"id": 10, "file_name": "nested\\dir\\c.jpg", "width": 64, "height": 64}
	  ],
	  "annotations": [
	    {"id": 100, "image_id": 1, "category_id": 1, "bbox": [10, 20, 30, 40], "iscrowd": 0},
	    {"id": 101, "image_id": 1, "category_id": 90, "segmentation": [[50, 60, 90, 60, 90, 100, 50, 100]], "iscrowd": 0},
	    {"id": 102, "image_id": 2, "category_id": 7, "segmentation": [[1, 1, 5, 1, 5, 5]], "iscrowd": 0},
	    {"id": 103, "image_id": 2, "category_id": 1, "segmentation": [[10, 10, 60, 10, 60, 60, 10, 60]], "iscrowd": 0}
	  ],
	  "categories": [
	    {"id": 1, "name": "person", "supercategory": "human"},
	    {"id": 7, "name": "bike"},
	    {"id": 90, "name": "toothbrush", "supercategory": "indoor"}
	  ]
	}`)

	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %+v", issues)
	}
	if len(ds.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %+v", ds.Warnings)
	}

	// Categories: index follows the array order, key is the name, the sparse
	// COCO ids (1/7/90) are preserved verbatim in COCOID.
	if len(ds.Categories) != 3 {
		t.Fatalf("categories = %d, want 3", len(ds.Categories))
	}
	wantCats := []struct {
		key, name, super string
		index, id        int
	}{
		{"person", "person", "human", 0, 1},
		{"bike", "bike", "", 1, 7},
		{"toothbrush", "toothbrush", "indoor", 2, 90},
	}
	for i, want := range wantCats {
		got := ds.Categories[i]
		if got.Key != want.key || got.Name != want.name || got.SuperCategory != want.super || got.Index != want.index {
			t.Errorf("categories[%d] = %+v, want key=%q name=%q super=%q index=%d",
				i, got, want.key, want.name, want.super, want.index)
		}
		if got.COCOID == nil || *got.COCOID != want.id {
			t.Errorf("categories[%d].COCOID = %v, want %d", i, got.COCOID, want.id)
		}
	}

	// Images keep the array order; file names are slash separated.
	if len(ds.Images) != 3 {
		t.Fatalf("images = %d, want 3", len(ds.Images))
	}
	wantFiles := []string{"a.jpg", "sub/b.png", "nested/dir/c.jpg"}
	for i, want := range wantFiles {
		if ds.Images[i].File != want {
			t.Errorf("images[%d].File = %q, want %q", i, ds.Images[i].File, want)
		}
	}
	if img := cocoImageByFile(t, ds, "a.jpg"); img.Width != 640 || img.Height != 480 {
		t.Errorf("a.jpg dims = %dx%d, want 640x480", img.Width, img.Height)
	}

	a := cocoImageByFile(t, ds, "a.jpg")
	if len(a.Annotations) != 2 {
		t.Fatalf("a.jpg annotations = %d, want 2", len(a.Annotations))
	}
	box := a.Annotations[0]
	if box.Kind != ir.KindBBox || box.X != 10 || box.Y != 20 || box.W != 30 || box.H != 40 {
		t.Errorf("bbox ann = %+v, want bbox (10,20,30,40)", box)
	}
	if box.ID != "100" || box.Category != "person" {
		t.Errorf("bbox ann id/category = %q/%q, want 100/person", box.ID, box.Category)
	}
	if len(box.Rings) != 0 || len(box.Attributes) != 0 {
		t.Errorf("bbox ann has unexpected rings/attrs: %+v", box)
	}
	poly := a.Annotations[1]
	if poly.Kind != ir.KindPolygon || poly.Category != "toothbrush" {
		t.Fatalf("poly ann = %+v, want polygon/toothbrush", poly)
	}
	wantRing := [][2]float64{{50, 60}, {90, 60}, {90, 100}, {50, 100}}
	if len(poly.Rings) != 1 || !cocoRingEqual(poly.Rings[0], wantRing) {
		t.Errorf("poly rings = %v, want [%v]", poly.Rings, wantRing)
	}

	b := cocoImageByFile(t, ds, "sub/b.png")
	if len(b.Annotations) != 2 {
		t.Fatalf("sub/b.png annotations = %d, want 2", len(b.Annotations))
	}

	// Images without annotations stay in the dataset as empty images.
	if empty := cocoImageByFile(t, ds, "nested/dir/c.jpg"); len(empty.Annotations) != 0 {
		t.Errorf("c.jpg annotations = %d, want 0", len(empty.Annotations))
	}
}

// --- multi segment polygons ---------------------------------------------

func TestParseCOCOMultiSegmentRings(t *testing.T) {
	ds, issues := cocoParseFixture(t, `{
	  "images": [{"id": 1, "file_name": "m.png", "width": 100, "height": 100}],
	  "annotations": [
	    {"id": 5, "image_id": 1, "category_id": 1, "iscrowd": 0,
	     "segmentation": [
	       [1,1,5,1,5,5],
	       [10,10,20,10,20,20,10,20],
	       [30,30,35,30,35,35,30,35]
	     ]}
	  ],
	  "categories": [{"id": 1, "name": "thing"}]
	}`)
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %+v", issues)
	}
	if len(ds.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %+v", ds.Warnings)
	}
	img := cocoImageByFile(t, ds, "m.png")
	if len(img.Annotations) != 1 {
		t.Fatalf("annotations = %d, want 1", len(img.Annotations))
	}
	ann := img.Annotations[0]
	if ann.Kind != ir.KindPolygon {
		t.Fatalf("kind = %q, want polygon", ann.Kind)
	}
	if len(ann.Rings) != 3 {
		t.Fatalf("rings = %d, want 3 (every segment kept)", len(ann.Rings))
	}
	// Order is Normalize's business (|area| descending); every segment must
	// survive exactly once.
	want := [][][2]float64{
		{{1, 1}, {5, 1}, {5, 5}},
		{{10, 10}, {20, 10}, {20, 20}, {10, 20}},
		{{30, 30}, {35, 30}, {35, 35}, {30, 35}},
	}
	for _, w := range want {
		found := 0
		for _, got := range ann.Rings {
			if cocoRingEqual(got, w) {
				found++
			}
		}
		if found != 1 {
			t.Errorf("ring %v found %d times in %v", w, found, ann.Rings)
		}
	}
}

// --- RLE -----------------------------------------------------------------

// 4x4 mask with a 2x2 square in the top-left corner. Column-major linear
// index = col*4 + row, so the set pixels are indices 0,1 (col 0 rows 0-1) and
// 4,5 (col 1 rows 0-1); the runs are
//
//	zeros 0-  (none), ones 0-1, zeros 2-3, ones 4-5, zeros 6-15
//
// i.e. counts [0,2,2,2,10], sum 16 == 4*4.
const cocoRLEArrayCountsJSON = `[0, 2, 2, 2, 10]`

func TestParseCOCORLEArrayCounts(t *testing.T) {
	ds, issues := cocoParseFixture(t, `{
	  "images": [{"id": 1, "file_name": "rle.jpg", "width": 4, "height": 4}],
	  "annotations": [
	    {"id": 7, "image_id": 1, "category_id": 1, "iscrowd": 1,
	     "segmentation": {"counts": `+cocoRLEArrayCountsJSON+`, "size": [4, 4]}}
	  ],
	  "categories": [{"id": 1, "name": "crowd"}]
	}`)
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %+v", issues)
	}
	img := cocoImageByFile(t, ds, "rle.jpg")
	if len(img.Annotations) != 1 {
		t.Fatalf("annotations = %d, want 1", len(img.Annotations))
	}
	ann := img.Annotations[0]
	if ann.Kind != ir.KindBBox {
		t.Fatalf("kind = %q, want bbox (RLE degrades to its bounding box)", ann.Kind)
	}
	if ann.X != 0 || ann.Y != 0 || ann.W != 2 || ann.H != 2 {
		t.Errorf("bbox = (%g,%g,%g,%g), want (0,0,2,2)", ann.X, ann.Y, ann.W, ann.H)
	}
	if got := ann.Attributes[attrIscrowd]; got != "1" {
		t.Errorf("attributes = %v, want iscrowd=1", ann.Attributes)
	}
	degraded := cocoWarningsContaining(t, ds.Warnings, "RLE")
	if len(degraded) != 1 {
		t.Fatalf("RLE warnings = %d (%+v), want exactly 1", len(degraded), ds.Warnings)
	}
	w := degraded[0]
	if w.File != "rle.jpg" || !strings.Contains(w.Message, "id=7") {
		t.Errorf("warning not locatable (want File rle.jpg and the annotation id): %+v", w)
	}
}

func TestParseCOCORLEStringCounts(t *testing.T) {
	// "02208" is the compressed form of [0,2,2,2,10]; derivation:
	//   i=0 count 0  -> chunk 0                       -> '0'
	//   i=1 count 2  -> chunk 2                       -> '2'
	//   i=2 count 2  -> chunk 2                       -> '2'
	//   i=3 count 2, delta vs counts[1] = 0 -> chunk 0 -> '0'
	//   i=4 count 10, delta vs counts[2] = 8 -> chunk 8 -> '8'
	// "0Xo0" is [0,1000] for size [1,1000]: 1000 = 0b1111101000 splits into
	// 5-bit chunks 8 and 31; 31 has the 0x10 sign bit set, so a third chunk 0
	// terminates the value.
	ds, issues := cocoParseFixture(t, `{
	  "images": [{"id": 1, "file_name": "str.png", "width": 1000, "height": 4}],
	  "annotations": [
	    {"id": 8, "image_id": 1, "category_id": 1, "iscrowd": 0,
	     "segmentation": {"counts": "02208", "size": [4, 4]}},
	    {"id": 9, "image_id": 1, "category_id": 1, "iscrowd": 0,
	     "segmentation": {"counts": "0Xo0", "size": [1, 1000]}}
	  ],
	  "categories": [{"id": 1, "name": "crowd"}]
	}`)
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %+v", issues)
	}
	img := cocoImageByFile(t, ds, "str.png")
	if len(img.Annotations) != 2 {
		t.Fatalf("annotations = %d, want 2", len(img.Annotations))
	}
	square := img.Annotations[0]
	if square.X != 0 || square.Y != 0 || square.W != 2 || square.H != 2 {
		t.Errorf("string RLE bbox = (%g,%g,%g,%g), want (0,0,2,2)", square.X, square.Y, square.W, square.H)
	}
	long := img.Annotations[1]
	if long.X != 0 || long.Y != 0 || long.W != 1000 || long.H != 1 {
		t.Errorf("multi chunk RLE bbox = (%g,%g,%g,%g), want (0,0,1000,1)", long.X, long.Y, long.W, long.H)
	}
	if got := len(cocoWarningsContaining(t, ds.Warnings, "RLE")); got != 2 {
		t.Errorf("RLE degradation warnings = %d, want 2 (%+v)", got, ds.Warnings)
	}
}

func TestDecodeRLEString(t *testing.T) {
	// Hand derived encodings (see the derivation above and in coco_test.go
	// helpers): these pin the decoder to the pycocotools rleToString format.
	literals := []struct {
		counts []int64
		s      string
	}{
		{[]int64{0, 2, 2, 2, 10}, "02208"},
		{[]int64{0, 1000}, "0Xo0"},
	}
	for _, tc := range literals {
		got, err := decodeRLEString(tc.s)
		if err != nil {
			t.Fatalf("decodeRLEString(%q): %v", tc.s, err)
		}
		if !reflect.DeepEqual(got, tc.counts) {
			t.Errorf("decodeRLEString(%q) = %v, want %v", tc.s, got, tc.counts)
		}
		if enc := cocoEncodeCountsString(tc.counts); enc != tc.s {
			t.Errorf("cocoEncodeCountsString(%v) = %q, want %q", tc.counts, enc, tc.s)
		}
	}

	// Round trip through the encoder for values that need delta encoding
	// (index >= 3) and multi chunk chunks.
	roundTrip := [][]int64{
		{0},
		{16},
		{0, 1, 100, 3, 2, 5},
		{3, 10, 2, 20, 13},
		{0, 4, 0, 4, 0, 4, 0, 4},
	}
	for _, want := range roundTrip {
		got, err := decodeRLEString(cocoEncodeCountsString(want))
		if err != nil {
			t.Fatalf("decodeRLEString(%v): %v", want, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("round trip %v -> %q -> %v", want, cocoEncodeCountsString(want), got)
		}
	}

	if _, err := decodeRLEString("0X"); err == nil {
		t.Error("decodeRLEString on a truncated string: want error, got nil")
	}
}

func TestRLEStringEqualsArrayCounts(t *testing.T) {
	// Same run lengths, once as an unpacked array and once compressed: both
	// must yield the identical mask and bounding box.
	counts := []int64{3, 10, 2, 20, 13} // sums to 48 == 8*6
	arr, err := newCOCOMask(counts, 6, 8)
	if err != nil {
		t.Fatalf("newCOCOMask(array): %v", err)
	}
	str, err := newCOCOMask(mustDecodeRLEString(t, cocoEncodeCountsString(counts)), 6, 8)
	if err != nil {
		t.Fatalf("newCOCOMask(string): %v", err)
	}
	if arr.HasPixels != str.HasPixels ||
		arr.MinX != str.MinX || arr.MaxX != str.MaxX ||
		arr.MinY != str.MinY || arr.MaxY != str.MaxY {
		t.Fatalf("string mask %+v != array mask %+v", str, arr)
	}
	// Sanity check the expected box by hand: height 6, so the one runs cover
	// linear indices [3,13) (cols 0-2, from row 3 of col 0 to row 0 of col 2)
	// and [15,35) (cols 2-5, from row 3 of col 2 to row 4 of col 5). Columns
	// 1..4 are covered from row 0 to row 5, so the box is x 0..5, y 0..5.
	if arr.MinX != 0 || arr.MaxX != 5 || arr.MinY != 0 || arr.MaxY != 5 {
		t.Errorf("array mask box = (%d,%d)-(%d,%d), want (0,0)-(5,5)", arr.MinX, arr.MinY, arr.MaxX, arr.MaxY)
	}
}

func mustDecodeRLEString(t *testing.T, s string) []int64 {
	t.Helper()
	counts, err := decodeRLEString(s)
	if err != nil {
		t.Fatalf("decodeRLEString(%q): %v", s, err)
	}
	return counts
}

// --- iscrowd / bbox coexistence -----------------------------------------

func TestParseCOCOIscrowdPolygon(t *testing.T) {
	ds, issues := cocoParseFixture(t, `{
	  "images": [{"id": 1, "file_name": "c.png", "width": 50, "height": 50}],
	  "annotations": [
	    {"id": 11, "image_id": 1, "category_id": 1, "iscrowd": 1,
	     "bbox": [2, 3, 10, 10],
	     "segmentation": [[2,3,12,3,12,13,2,13]]}
	  ],
	  "categories": [{"id": 1, "name": "crowd"}]
	}`)
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %+v", issues)
	}
	if len(ds.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %+v", ds.Warnings)
	}
	ann := cocoImageByFile(t, ds, "c.png").Annotations[0]
	if ann.Kind != ir.KindPolygon {
		t.Fatalf("kind = %q, want polygon (segmentation wins over bbox)", ann.Kind)
	}
	wantRing := [][2]float64{{2, 3}, {12, 3}, {12, 13}, {2, 13}}
	if len(ann.Rings) != 1 || !cocoRingEqual(ann.Rings[0], wantRing) {
		t.Errorf("rings = %v, want [%v]", ann.Rings, wantRing)
	}
	if got := ann.Attributes[attrIscrowd]; got != "1" {
		t.Errorf("attributes = %v, want iscrowd=1", ann.Attributes)
	}
}

func TestParseCOCOSegmentationWinsOverBBox(t *testing.T) {
	ds, issues := cocoParseFixture(t, `{
	  "images": [{"id": 1, "file_name": "both.png", "width": 40, "height": 40}],
	  "annotations": [
	    {"id": 12, "image_id": 1, "category_id": 1, "iscrowd": 0,
	     "bbox": [0, 0, 20, 20],
	     "segmentation": [[1,2,9,2,9,8]]}
	  ],
	  "categories": [{"id": 1, "name": "thing"}]
	}`)
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %+v", issues)
	}
	if len(ds.Warnings) != 0 {
		t.Fatalf("bbox next to segmentation is redundant, want no warning, got %+v", ds.Warnings)
	}
	ann := cocoImageByFile(t, ds, "both.png").Annotations[0]
	if ann.Kind != ir.KindPolygon {
		t.Fatalf("kind = %q, want polygon", ann.Kind)
	}
	if ann.X != 0 || ann.Y != 0 || ann.W != 0 || ann.H != 0 {
		t.Errorf("bbox fields = (%g,%g,%g,%g), want all zero", ann.X, ann.Y, ann.W, ann.H)
	}
	if len(ann.Attributes) != 0 {
		t.Errorf("attributes = %v, want none for iscrowd=0 polygon", ann.Attributes)
	}
}

// --- normalization pass through -----------------------------------------

func TestParseCOCONormalizeWarningsFlow(t *testing.T) {
	// Out-of-range coordinates are not my concern, but ir.Normalize clips
	// them: its warnings must end up in Dataset.Warnings, locatable per image.
	ds, issues := cocoParseFixture(t, `{
	  "images": [{"id": 1, "file_name": "big.jpg", "width": 20, "height": 20}],
	  "annotations": [
	    {"id": 1, "image_id": 1, "category_id": 1,
	     "segmentation": [[0,0, 100,0, 100,100]]}
	  ],
	  "categories": [{"id": 1, "name": "thing"}]
	}`)
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %+v", issues)
	}
	if len(ds.Warnings) == 0 {
		t.Fatal("want ir.Normalize warnings for out-of-range coordinates, got none")
	}
	for _, w := range ds.Warnings {
		if w.File != "big.jpg" {
			t.Errorf("normalize warning not locatable: %+v", w)
		}
	}
}

// --- malformed entries ---------------------------------------------------

func TestParseCOCOUnknownReferences(t *testing.T) {
	ds, issues := cocoParseFixture(t, `{
	  "images": [{"id": 1, "file_name": "ok.jpg", "width": 20, "height": 20}],
	  "annotations": [
	    {"id": 1, "image_id": 999, "category_id": 1, "bbox": [1, 1, 5, 5]},
	    {"id": 2, "image_id": 1, "category_id": 404, "bbox": [1, 1, 5, 5]},
	    {"id": 3, "image_id": 1, "category_id": 1, "bbox": [2, 2, 6, 6]}
	  ],
	  "categories": [{"id": 1, "name": "thing"}]
	}`)
	if len(issues) != 2 {
		t.Fatalf("issues = %d (%+v), want 2", len(issues), issues)
	}
	byField := map[string]ir.Issue{}
	for _, is := range issues {
		byField[is.Field] = is
	}
	if is, ok := byField["image_id"]; !ok {
		t.Error("missing Issue for the unknown image_id")
	} else if !strings.Contains(is.Message, "999") || is.File != "coco.json" {
		t.Errorf("unknown image_id issue = %+v, want File coco.json and message naming 999", is)
	}
	if is, ok := byField["category_id"]; !ok {
		t.Error("missing Issue for the unknown category_id")
	} else if !strings.Contains(is.Message, "404") || is.File != "ok.jpg" || is.Index != 0 {
		t.Errorf("unknown category_id issue = %+v, want File ok.jpg Index 0 naming 404", is)
	}
	anns := cocoImageByFile(t, ds, "ok.jpg").Annotations
	if len(anns) != 1 || anns[0].ID != "3" {
		t.Fatalf("kept annotations = %+v, want only the valid one (id 3)", anns)
	}
}

func TestParseCOCOMallformedSegmentation(t *testing.T) {
	// A broken segmentation is reported and, when a bbox exists, the bbox
	// keeps the data instead of dropping the annotation.
	ds, issues := cocoParseFixture(t, `{
	  "images": [{"id": 1, "file_name": "badseg.jpg", "width": 30, "height": 30}],
	  "annotations": [
	    {"id": 21, "image_id": 1, "category_id": 1, "bbox": [5, 5, 10, 10],
	     "segmentation": [[1,1,5,1,5]]},
	    {"id": 22, "image_id": 1, "category_id": 1,
	     "segmentation": {"counts": [0, 4], "size": [0, 0]}}
	  ],
	  "categories": [{"id": 1, "name": "thing"}]
	}`)
	if len(issues) != 3 {
		t.Fatalf("issues = %d (%+v), want 3 (two segmentation findings plus the unusable annotation)", len(issues), issues)
	}
	var segIssues, boxIssues int
	for _, is := range issues {
		if is.File != "badseg.jpg" {
			t.Errorf("issue = %+v, want File=badseg.jpg", is)
		}
		switch is.Field {
		case "segmentation":
			segIssues++
		case "bbox":
			boxIssues++
		default:
			t.Errorf("unexpected issue field: %+v", is)
		}
	}
	if segIssues != 2 || boxIssues != 1 {
		t.Errorf("segmentation issues = %d, bbox issues = %d, want 2 and 1", segIssues, boxIssues)
	}
	anns := cocoImageByFile(t, ds, "badseg.jpg").Annotations
	if len(anns) != 1 {
		t.Fatalf("annotations = %d (%+v), want 1 (the one with a bbox)", len(anns), anns)
	}
	if anns[0].ID != "21" || anns[0].Kind != ir.KindBBox || anns[0].X != 5 || anns[0].W != 10 {
		t.Errorf("recovered annotation = %+v, want bbox (5,5,10,10) id 21", anns[0])
	}
}

func TestParseCOCOBadJSON(t *testing.T) {
	path := cocoWriteFixture(t, t.TempDir(), "broken.json", `{"images": [}`)
	ds, issues, err := parseCOCO(path)
	if err == nil {
		t.Fatalf("parseCOCO on broken JSON: want error, got ds=%+v issues=%+v", ds, issues)
	}
	if ds != nil || issues != nil {
		t.Errorf("broken JSON returned ds=%+v issues=%+v, want nils", ds, issues)
	}
	if !strings.Contains(err.Error(), "broken.json") {
		t.Errorf("error %q should name the offending file", err)
	}
}

func TestParseCOCOMissingFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.json")
	ds, issues, err := parseCOCO(missing)
	if err == nil {
		t.Fatalf("parseCOCO on a missing file: want error, got ds=%+v issues=%+v", ds, issues)
	}
	if ds != nil || issues != nil {
		t.Errorf("missing file returned ds=%+v issues=%+v, want nils", ds, issues)
	}
}
