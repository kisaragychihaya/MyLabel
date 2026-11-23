package app

import (
	"math"
	"reflect"
	"sort"
	"testing"

	"mylabel/internal/ir"
)

// geom tests compare against the 1e-9 tolerance the phase-3 contract asks for.
const convEps = 1e-9

func near(a, b float64) bool { return math.Abs(a-b) <= convEps }

// annPointSet returns an annotation's vertices as a sorted set: two
// annotations with the same set cover the same region, whatever order or
// starting vertex their representations use.
func annPointSet(an *Ann) [][2]float64 {
	var pts [][2]float64
	if isPoly(an) {
		for _, p := range an.Points {
			pts = append(pts, [2]float64{p.X, p.Y})
		}
	} else {
		c := corners(an)
		for _, p := range c {
			pts = append(pts, [2]float64{p.X, p.Y})
		}
	}
	sort.Slice(pts, func(i, j int) bool {
		if near(pts[i][0], pts[j][0]) {
			return pts[i][1] < pts[j][1]
		}
		return pts[i][0] < pts[j][0]
	})
	return pts
}

// assertSameShape compares two annotations geometrically.
func assertSameShape(t *testing.T, what string, got, want *Ann) {
	t.Helper()
	g, w := annPointSet(got), annPointSet(want)
	if len(g) != len(w) {
		t.Fatalf("%s: 顶点数 = %d，期望 %d", what, len(g), len(w))
	}
	for i := range w {
		if !near(g[i][0], w[i][0]) || !near(g[i][1], w[i][1]) {
			t.Fatalf("%s: 顶点 %d = %v，期望 %v", what, i, g[i], w[i])
		}
	}
}

// irRoundTrip converts one app annotation to a normalized IR document and
// back, returning the IR document, the app annotation and the ok flag of the
// IR→app step.
func irRoundTrip(t *testing.T, an Ann, catOf map[string]int, keys []string) (ir.Annotation, Ann, bool) {
	t.Helper()
	x := annToIR(&an, keys)
	img := ir.Image{File: "a.jpg", Width: 4000, Height: 4000, Annotations: []ir.Annotation{x}}
	if w := ir.Normalize(&ir.Project{}, &img); len(w) != 0 {
		t.Fatalf("Normalize warnings = %+v，期望 0 条", w)
	}
	back, ok := irAnnToApp(img.Annotations[0], catOf, &convStats{})
	return img.Annotations[0], back, ok
}

// assertSameIR compares two IR annotations geometrically, ignoring the last
// bits of floating point noise from the atan2/hypot round trip.
func assertSameIR(t *testing.T, what string, got, want ir.Annotation) {
	t.Helper()
	if got.Kind != want.Kind {
		t.Fatalf("%s: kind = %q，期望 %q", what, got.Kind, want.Kind)
	}
	if got.Category != want.Category || got.ID != want.ID {
		t.Fatalf("%s: category/id = %q/%q，期望 %q/%q", what, got.Category, got.ID, want.Category, want.ID)
	}
	switch want.Kind {
	case ir.KindBBox:
		if !near(got.X, want.X) || !near(got.Y, want.Y) || !near(got.W, want.W) || !near(got.H, want.H) {
			t.Fatalf("%s: bbox = %v,%v,%v,%v，期望 %v,%v,%v,%v", what, got.X, got.Y, got.W, got.H, want.X, want.Y, want.W, want.H)
		}
	case ir.KindPolygon:
		if len(got.Rings) != len(want.Rings) {
			t.Fatalf("%s: 环数 = %d，期望 %d", what, len(got.Rings), len(want.Rings))
		}
		for r := range want.Rings {
			if len(got.Rings[r]) != len(want.Rings[r]) {
				t.Fatalf("%s: 环 %d 点数 = %d，期望 %d", what, r, len(got.Rings[r]), len(want.Rings[r]))
			}
			for i := range want.Rings[r] {
				if !near(got.Rings[r][i][0], want.Rings[r][i][0]) || !near(got.Rings[r][i][1], want.Rings[r][i][1]) {
					t.Fatalf("%s: 环 %d 顶点 %d = %v，期望 %v", what, r, i, got.Rings[r][i], want.Rings[r][i])
				}
			}
		}
	case ir.KindRotated:
		if len(got.Points) != 4 || len(want.Points) != 4 {
			t.Fatalf("%s: 点数 = %d/%d，期望 4/4", what, len(got.Points), len(want.Points))
		}
		for i := range want.Points {
			if !near(got.Points[i][0], want.Points[i][0]) || !near(got.Points[i][1], want.Points[i][1]) {
				t.Fatalf("%s: 顶点 %d = %v，期望 %v", what, i, got.Points[i], want.Points[i])
			}
		}
		if !near(got.CX, want.CX) || !near(got.CY, want.CY) || !near(got.RW, want.RW) ||
			!near(got.RH, want.RH) || !near(got.Angle, want.Angle) {
			t.Fatalf("%s: 派生缓存 = %v,%v,%v,%v,%v，期望 %v,%v,%v,%v,%v", what,
				got.CX, got.CY, got.RW, got.RH, got.Angle, want.CX, want.CY, want.RW, want.RH, want.Angle)
		}
	}
}

// irPoints returns an IR annotation's vertices whatever its kind: a bbox
// contributes its four corners, so a bbox and a rotated_bbox of the same region
// compare equal.
func irPoints(x ir.Annotation) [][2]float64 {
	switch x.Kind {
	case ir.KindBBox:
		return [][2]float64{
			{x.X, x.Y}, {x.X + x.W, x.Y}, {x.X + x.W, x.Y + x.H}, {x.X, x.Y + x.H},
		}
	case ir.KindPolygon:
		if len(x.Rings) == 0 {
			return nil
		}
		return x.Rings[0]
	case ir.KindRotated:
		return x.Points
	}
	return nil
}

// assertSameIRShape compares two IR annotations by the region they cover,
// ignoring which of the two rectangle kinds represents it.
func assertSameIRShape(t *testing.T, what string, got, want ir.Annotation) {
	t.Helper()
	g, w := sortedPts(irPoints(got)), sortedPts(irPoints(want))
	if len(g) != len(w) {
		t.Fatalf("%s: 顶点数 = %d，期望 %d", what, len(g), len(w))
	}
	for i := range w {
		if !near(g[i][0], w[i][0]) || !near(g[i][1], w[i][1]) {
			t.Fatalf("%s: 顶点 %d = %v，期望 %v", what, i, g[i], w[i])
		}
	}
}

// sortedPts sorts a vertex list by x then y for set comparison.
func sortedPts(pts [][2]float64) [][2]float64 {
	out := append([][2]float64(nil), pts...)
	sort.Slice(out, func(i, j int) bool {
		if near(out[i][0], out[j][0]) {
			return out[i][1] < out[j][1]
		}
		return out[i][0] < out[j][0]
	})
	return out
}

// TestAnnToIRRoundTrip: app → IR → app keeps the geometry within 1e-9 for every
// kind, and a second trip is a fixed point. The first trip is not: a rotated box
// whose canonical start vertex moves has its width/height swapped and its angle
// turned by a quarter, and a box at 90° becomes an axis-aligned bbox — both are
// geometry-preserving consequences of ir.Normalize's canonical form
// (docs/phase3-design.md §4.1, §2.2).
func TestAnnToIRRoundTrip(t *testing.T) {
	cells := []struct {
		name      string
		ann       Ann
		wantKind  ir.Kind // first trip
		wantKind2 ir.Kind // second trip: the fixed point's kind
	}{
		{"bbox", Ann{ID: "b1", Cat: 0, CX: 50, CY: 30, W: 20, H: 10}, ir.KindBBox, ir.KindBBox},
		{"obb 30°", Ann{ID: "r1", Cat: 0, CX: 50, CY: 30, W: 20, H: 10, Angle: 0.3}, ir.KindRotated, ir.KindRotated},
		{"obb 60°（宽高换位）", Ann{ID: "r2", Cat: 0, CX: 50, CY: 30, W: 40, H: 20, Angle: math.Pi / 3}, ir.KindRotated, ir.KindRotated},
		{"obb 90°（第二次往返成 bbox）", Ann{ID: "r3", Cat: 0, CX: 50, CY: 30, W: 20, H: 10, Angle: math.Pi / 2}, ir.KindRotated, ir.KindBBox},
		{"obb 负角", Ann{ID: "r4", Cat: 0, CX: 50, CY: 30, W: 20, H: 10, Angle: -0.9}, ir.KindRotated, ir.KindRotated},
		{"poly", newPolyAnn("p1", 0, []Point{{10, 10}, {40, 10}, {40, 30}, {10, 30}}), ir.KindPolygon, ir.KindPolygon},
	}
	keys := []string{"cat1"}
	catOf := map[string]int{"cat1": 0}

	for _, tc := range cells {
		t.Run(tc.name, func(t *testing.T) {
			want := tc.ann
			ir1, app1, ok := irRoundTrip(t, want, catOf, keys)
			if !ok {
				t.Fatal("irAnnToApp 拒绝了合法标注")
			}
			if ir1.Kind != tc.wantKind {
				t.Fatalf("kind = %q，期望 %q", ir1.Kind, tc.wantKind)
			}
			if app1.Cat != 0 || app1.ID != want.ID {
				t.Errorf("Cat/ID = %d/%q，期望 0/%q", app1.Cat, app1.ID, want.ID)
			}
			assertSameShape(t, "IR→app", &app1, &want)

			// The second trip covers the same region, and the third is a strict
			// fixed point of the second: the canonical form may still change the
			// representation once, never the geometry.
			ir2, app2, ok := irRoundTrip(t, app1, catOf, keys)
			if !ok {
				t.Fatal("第二次 IR→app 失败")
			}
			assertSameIRShape(t, "IR 往返几何", ir2, ir1)
			if ir2.Kind != tc.wantKind2 {
				t.Fatalf("第二次往返 kind = %q，期望 %q", ir2.Kind, tc.wantKind2)
			}
			assertSameShape(t, "app 往返", &app2, &app1)

			ir3, _, ok := irRoundTrip(t, app2, catOf, keys)
			if !ok {
				t.Fatal("第三次 IR→app 失败")
			}
			assertSameIR(t, "IR 不动点", ir3, ir2)
		})
	}
}

// TestAnnToIRKinds pins the IR kind and payload each app kind produces.
func TestAnnToIRKinds(t *testing.T) {
	keys := []string{"person"}

	box := Ann{ID: "b", Cat: 0, CX: 50, CY: 30, W: 20, H: 10}
	x := annToIR(&box, keys)
	if x.Kind != ir.KindBBox || !near(x.X, 40) || !near(x.Y, 25) || !near(x.W, 20) || !near(x.H, 10) {
		t.Errorf("bbox = %+v，期望 x/y/w/h = 40/25/20/10", x)
	}
	if x.Category != "person" || x.ID != "b" {
		t.Errorf("category/id = %q/%q", x.Category, x.ID)
	}

	obb := Ann{ID: "r", Cat: 0, CX: 50, CY: 30, W: 20, H: 10, Angle: 0.4}
	r := annToIR(&obb, keys)
	if r.Kind != ir.KindRotated || len(r.Points) != 4 {
		t.Fatalf("obb = %+v，期望 rotated_bbox 四点", r)
	}
	if !near(r.CX, 50) || !near(r.CY, 30) || !near(r.RW, 20) || !near(r.RH, 10) || !near(r.Angle, 0.4) {
		t.Errorf("obb 派生缓存 = %+v，期望 cx/cy/rw/rh/angle = 50/30/20/10/0.4", r)
	}

	poly := newPolyAnn("p", 0, []Point{{0, 0}, {10, 0}, {10, 10}})
	p := annToIR(&poly, keys)
	if p.Kind != ir.KindPolygon || len(p.Rings) != 1 || len(p.Rings[0]) != 3 {
		t.Fatalf("poly = %+v，期望单环三点", p)
	}
	if p.Rings[0][2] != [2]float64{10, 10} {
		t.Errorf("poly 顶点 = %v", p.Rings[0])
	}

	// An out-of-range category has no key: the converter reports "" instead of
	// panicking, and the validator then flags it.
	if got := annToIR(&Ann{Cat: 7}, keys); got.Category != "" {
		t.Errorf("越界类别 = %q，期望空 key", got.Category)
	}
}

func TestIRCategoryKeys(t *testing.T) {
	cats := []Category{{Name: "person"}, {Name: "car"}, {Name: "person"}, {Name: ""}, {Name: "person~2"}}
	want := []string{"person", "car", "person~2", "cat4", "person~2~2"}
	if got := categoryKeys(cats); !reflect.DeepEqual(got, want) {
		t.Fatalf("categoryKeys = %v，期望 %v", got, want)
	}
	// Keys are stable for a list without duplicates.
	plain := []Category{{Name: "a"}, {Name: "b"}}
	if got := categoryKeys(plain); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("categoryKeys = %v，期望 [a b]", got)
	}
	if got := categoryKeys(nil); len(got) != 0 {
		t.Errorf("categoryKeys(nil) = %v，期望空", got)
	}
}

func TestIRCategoryToApp(t *testing.T) {
	cases := []struct {
		name string
		in   ir.Category
		want Category
	}{
		{"带颜色", ir.Category{Key: "person", Name: "人", Color: "#EF4444"}, Category{Name: "人", Color: "#ef4444"}},
		{"无名字用 key", ir.Category{Key: "person"}, Category{Name: "person"}},
		{"非法颜色丢弃", ir.Category{Key: "k", Name: "n", Color: "red"}, Category{Name: "n"}},
		{"全空", ir.Category{}, Category{Name: "类别"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := irCategoryToApp(tc.in); got != tc.want {
				t.Fatalf("irCategoryToApp(%+v) = %+v，期望 %+v", tc.in, got, tc.want)
			}
		})
	}

	irs := appCategoriesToIR([]Category{{Name: "person", Color: "#ff0000"}, {Name: "person"}})
	if len(irs) != 2 || irs[0].Key != "person" || irs[1].Key != "person~2" {
		t.Fatalf("appCategoriesToIR = %+v，期望 person / person~2", irs)
	}
	if irs[0].Index != 0 || irs[1].Index != 1 || irs[0].Color != "#ff0000" {
		t.Errorf("appCategoriesToIR = %+v", irs)
	}
}

func TestIRAnnToAppRejects(t *testing.T) {
	catOf := map[string]int{"cat1": 0}
	cases := []struct {
		name string
		in   ir.Annotation
	}{
		{"未知类别", ir.Annotation{Category: "nope", Kind: ir.KindBBox, W: 1, H: 1}},
		{"bbox 零宽", ir.Annotation{Category: "cat1", Kind: ir.KindBBox, W: 0, H: 1}},
		{"bbox 负高", ir.Annotation{Category: "cat1", Kind: ir.KindBBox, W: 1, H: -2}},
		{"poly 没有环", ir.Annotation{Category: "cat1", Kind: ir.KindPolygon}},
		{"poly 环点太少", ir.Annotation{Category: "cat1", Kind: ir.KindPolygon, Rings: [][][2]float64{{{0, 0}, {1, 1}}}}},
		{"rotated 点数不对", ir.Annotation{Category: "cat1", Kind: ir.KindRotated, Points: [][2]float64{{0, 0}, {1, 0}, {1, 1}}}},
		{"rotated 退化成线", ir.Annotation{Category: "cat1", Kind: ir.KindRotated, Points: [][2]float64{{0, 0}, {0, 0}, {1, 1}, {1, 1}}}},
		{"未知 kind", ir.Annotation{Category: "cat1", Kind: "ellipse"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if an, ok := irAnnToApp(tc.in, catOf, &convStats{}); ok {
				t.Fatalf("期望拒绝，得到 %+v", an)
			}
		})
	}
}

// TestIRAnnToAppMultiRing: the app model holds one ring, so the outer (largest,
// already sorted by Normalize) survives and the rest is counted (§4.1).
func TestIRAnnToAppMultiRing(t *testing.T) {
	outer := [][2]float64{{0, 0}, {20, 0}, {20, 20}, {0, 20}}
	hole := [][2]float64{{5, 5}, {10, 5}, {10, 10}, {5, 10}}
	part := [][2]float64{{15, 15}, {18, 15}, {18, 18}}
	x := ir.Annotation{Category: "cat1", Kind: ir.KindPolygon, Rings: [][][2]float64{outer, hole, part}}

	var st convStats
	an, ok := irAnnToApp(x, map[string]int{"cat1": 0}, &st)
	if !ok {
		t.Fatal("多环多边形被拒绝")
	}
	if len(an.Points) != 4 {
		t.Fatalf("Points = %v，期望只保留外环", an.Points)
	}
	for i, p := range outer {
		if an.Points[i].X != p[0] || an.Points[i].Y != p[1] {
			t.Fatalf("外环被改写: %v", an.Points)
		}
	}
	if st.droppedRings != 2 {
		t.Errorf("droppedRings = %d，期望 2", st.droppedRings)
	}
	// The mirrored bounds of a polygon follow its vertices.
	if !near(an.CX, 10) || !near(an.CY, 10) || !near(an.W, 20) || !near(an.H, 20) || an.Kind != KindPoly {
		t.Errorf("包围盒 = %+v", an)
	}
}

// TestIRAnnToAppAttributes: attributes are read and counted, not silently
// dropped — there is nowhere to put them until W3 adds Ann.Attributes.
func TestIRAnnToAppAttributes(t *testing.T) {
	x := ir.Annotation{
		Category: "cat1", Kind: ir.KindBBox, W: 4, H: 5,
		Attributes: map[string]string{"difficult": "1", "iscrowd": "0"},
	}
	var st convStats
	if _, ok := irAnnToApp(x, map[string]int{"cat1": 0}, &st); !ok {
		t.Fatal("带属性的标注被拒绝")
	}
	if st.attrs != 1 {
		t.Fatalf("attrs = %d，期望 1（一条带属性的标注）", st.attrs)
	}
	// Without attributes nothing is counted.
	var st2 convStats
	x.Attributes = nil
	irAnnToApp(x, map[string]int{"cat1": 0}, &st2)
	if st2.attrs != 0 {
		t.Errorf("attrs = %d，期望 0", st2.attrs)
	}
}

func TestCleanImportPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a.jpg", "a.jpg"},
		{"sub/a.jpg", "sub/a.jpg"},
		{"./a.jpg", "a.jpg"},
		{"sub/./a.jpg", "sub/a.jpg"},
		{"sub//a.jpg", "sub/a.jpg"},
		{"  a.jpg  ", "a.jpg"},
		{"", ""},
		{".", ""},
	}
	for _, tc := range cases {
		if got := cleanImportPath(tc.in); got != tc.want {
			t.Errorf("cleanImportPath(%q) = %q，期望 %q", tc.in, got, tc.want)
		}
	}
	// Backslashes are what a Windows source may carry; RelPath is slash-only.
	if got := cleanImportPath(`sub\a.jpg`); got != "sub/a.jpg" {
		t.Errorf("cleanImportPath(反斜杠) = %q，期望 sub/a.jpg", got)
	}
}
