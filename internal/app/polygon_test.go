package app

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// Helpers. Test-local names are prefixed to stay clear of other test files.

func polyString(poly []Point) string {
	if poly == nil {
		return "nil"
	}
	var b strings.Builder
	b.WriteString("[")
	for i, p := range poly {
		if i > 0 {
			b.WriteString(" ")
		}
		fmt.Fprintf(&b, "(%g,%g)", p.X, p.Y)
	}
	b.WriteString("]")
	return b.String()
}

func polyEqual(a, b []Point) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// wantPoly asserts that got equals want.
func wantPoly(t *testing.T, name string, got, want []Point) {
	t.Helper()
	if !polyEqual(got, want) {
		t.Fatalf("%s = %s, want %s", name, polyString(got), polyString(want))
	}
}

// wantAliased asserts that got and orig do not share a backing array. Both
// slices must be non-empty.
func wantNotAliased(t *testing.T, name string, got, orig []Point) {
	t.Helper()
	if len(got) == 0 || len(orig) == 0 {
		t.Fatalf("%s: need non-empty slices, got %d and %d", name, len(got), len(orig))
	}
	if &got[0] == &orig[0] {
		t.Fatalf("%s: result aliases the input backing array", name)
	}
}

func closeTo(got, want, eps float64) bool {
	return math.Abs(got-want) <= eps
}

// Square is the reference convex polygon.
var (
	squarePoly = []Point{{0, 0}, {10, 0}, {10, 10}, {0, 10}}

	// lShape is concave: the region is x∈[0,10],y∈[0,4] plus x∈[0,4],y∈[4,10],
	// so the notch (x>4, y>4) is outside.
	lShapePoly = []Point{{0, 0}, {10, 0}, {10, 4}, {4, 4}, {4, 10}, {0, 10}}

	// stepPoly has a horizontal shelf at y=3 whose endpoints (12,3) and (6,3)
	// are vertices: a +x ray from (2,3) passes exactly through both.
	stepPoly = []Point{{0, 0}, {12, 0}, {12, 3}, {6, 3}, {6, 6}, {0, 6}}

	trianglePoly = []Point{{0, 0}, {10, 0}, {0, 10}}

	negSquarePoly = []Point{{-5, -3}, {2, -3}, {2, 7}, {-5, 7}}
)

func TestPointInPolygon(t *testing.T) {
	tests := []struct {
		name string
		p    Point
		poly []Point
		want bool
	}{
		// convex square
		{"square center", Point{5, 5}, squarePoly, true},
		{"square left outside", Point{-0.5, 5}, squarePoly, false},
		{"square right outside", Point{10.5, 5}, squarePoly, false},
		{"square above outside", Point{5, -0.5}, squarePoly, false},
		{"square below outside", Point{5, 10.5}, squarePoly, false},
		{"square on left edge", Point{0, 5}, squarePoly, true},
		{"square on bottom edge", Point{5, 0}, squarePoly, true},
		{"square on right edge", Point{10, 7}, squarePoly, true},
		{"square on vertex", Point{0, 0}, squarePoly, true},
		{"square on far vertex", Point{10, 10}, squarePoly, true},
		{"square within eps of edge", Point{5, -1e-10}, squarePoly, true},
		{"square beyond eps of edge", Point{5, -1e-6}, squarePoly, false},

		// concave L: notch must be outside
		{"L in lower arm", Point{8, 2}, lShapePoly, true},
		{"L in left arm", Point{2, 8}, lShapePoly, true},
		{"L notch", Point{8, 8}, lShapePoly, false},
		{"L notch near corner", Point{4.5, 4.5}, lShapePoly, false},
		{"L on reflex edge", Point{4, 7}, lShapePoly, true},
		{"L just right of reflex edge", Point{4.0000001, 8}, lShapePoly, false},
		{"L on inner corner vertex", Point{4, 4}, lShapePoly, true},

		// ray passing exactly through vertices at the test point's y
		{"step on shelf line through vertices", Point{2, 3}, stepPoly, true},
		{"step lower half inside", Point{8, 2}, stepPoly, true},
		{"step upper right outside", Point{8, 4}, stepPoly, false},
		{"step upper left inside", Point{3, 4}, stepPoly, true},
		{"step on shelf edge", Point{11, 3}, stepPoly, true},
		{"step on left edge at shelf y", Point{0, 3}, stepPoly, true},

		// triangle
		{"triangle inside", Point{2, 2}, trianglePoly, true},
		{"triangle inside near hypotenuse", Point{6, 3}, trianglePoly, true},
		{"triangle outside hypotenuse", Point{6, 6}, trianglePoly, false},
		{"triangle on hypotenuse", Point{5, 5}, trianglePoly, true},
		{"triangle far vertex", Point{10, 0}, trianglePoly, true},
		{"triangle below base", Point{5, -1}, trianglePoly, false},

		// negative coordinates
		{"negative square inside", Point{0, 0}, negSquarePoly, true},
		{"negative square outside", Point{-6, 0}, negSquarePoly, false},
		{"negative square vertex", Point{-5, 7}, negSquarePoly, true},

		// degenerate inputs
		{"nil polygon", Point{0, 0}, nil, false},
		{"empty polygon", Point{5, 5}, []Point{}, false},
		{"single point on probe", Point{5, 5}, []Point{{5, 5}}, false},
		{"two points on probe", Point{5, 5}, []Point{{0, 0}, {10, 10}}, false},
		{"collinear triple", Point{2, 1}, []Point{{0, 0}, {5, 0}, {10, 0}}, false},
		// Degenerate zero-area polygon: its outline is still the closed
		// polygon, so a point on it counts as inside.
		{"collinear triple on outline", Point{2, 0}, []Point{{0, 0}, {5, 0}, {10, 0}}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := pointInPolygon(tc.p, tc.poly); got != tc.want {
				t.Fatalf("pointInPolygon(%v, %s) = %v, want %v", tc.p, polyString(tc.poly), got, tc.want)
			}
		})
	}
}

func TestPointInPolygonDoesNotMutate(t *testing.T) {
	want := []Point{{0, 0}, {10, 0}, {10, 4}, {4, 4}, {4, 10}, {0, 10}}

	poly := clonePoints(lShapePoly)
	wantPoly(t, "clone", poly, want)

	pointInPolygon(Point{8, 8}, poly)
	pointInPolygon(Point{2, 2}, poly)

	wantPoly(t, "input after pointInPolygon", poly, want)
}

func TestDistPointSegment(t *testing.T) {
	const eps = 1e-12

	tests := []struct {
		name string
		p    Point
		a    Point
		b    Point
		want float64
	}{
		{"projection inside", Point{5, 5}, Point{0, 0}, Point{10, 0}, 5},
		{"projection before start", Point{-5, 0}, Point{0, 0}, Point{10, 0}, 5},
		{"projection past end", Point{15, 0}, Point{0, 0}, Point{10, 0}, 5},
		{"projection past start reversed", Point{5, -5}, Point{10, 0}, Point{0, 0}, 5},
		{"on the segment", Point{5, 0}, Point{0, 0}, Point{10, 0}, 0},
		{"on an endpoint", Point{0, 0}, Point{0, 0}, Point{10, 0}, 0},
		{"zero length segment", Point{3, 3}, Point{2, 2}, Point{2, 2}, math.Sqrt2},
		{"zero length at probe", Point{2, 2}, Point{2, 2}, Point{2, 2}, 0},
		{"diagonal projection", Point{4, 0}, Point{0, 0}, Point{4, 4}, 2 * math.Sqrt2},
		{"vertical segment", Point{7, 5}, Point{2, 0}, Point{2, 10}, 5},
		{"past end of vertical", Point{7, 14}, Point{2, 0}, Point{2, 10}, math.Hypot(5, 4)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := distPointSegment(tc.p, tc.a, tc.b)
			if !closeTo(got, tc.want, eps) {
				t.Fatalf("distPointSegment(%v, %v, %v) = %v, want %v", tc.p, tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestNearestEdge(t *testing.T) {
	tests := []struct {
		name     string
		p        Point
		poly     []Point
		wantEdge int
		wantDist float64
	}{
		{"below first edge", Point{5, -2}, squarePoly, 0, 2},
		{"right of second edge", Point{12, 5}, squarePoly, 1, 2},
		{"above third edge", Point{5, 12}, squarePoly, 2, 2},
		// The closing edge poly[3]->poly[0] must participate: it is the only
		// edge within 3 of this probe.
		{"left of closing edge", Point{-3, 5}, squarePoly, 3, 3},
		// All four edges are 5 away; lowest index wins (strict <).
		{"center tie-break", Point{5, 5}, squarePoly, 0, 5},
		{"concave notch floor", Point{8, 5}, lShapePoly, 2, 1},
		{"concave left wall", Point{2, 12}, lShapePoly, 4, 2},
		// edge 1 is the hypotenuse x+y=10: |4+8-10|/sqrt2
		{"triangle hypotenuse", Point{4, 8}, trianglePoly, 1, math.Sqrt2},
		{"single vertex polygon", Point{4, 5}, []Point{{1, 1}}, 0, 5},
		{"two vertex polygon", Point{5, 3}, []Point{{0, 0}, {10, 0}}, 0, 3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			edge, dist := nearestEdge(tc.p, tc.poly)
			if edge != tc.wantEdge || !closeTo(dist, tc.wantDist, 1e-12) {
				t.Fatalf("nearestEdge(%v, %s) = (%d, %v), want (%d, %v)",
					tc.p, polyString(tc.poly), edge, dist, tc.wantEdge, tc.wantDist)
			}
		})
	}
}

func TestNearestEdgeEmpty(t *testing.T) {
	edge, dist := nearestEdge(Point{1, 1}, nil)
	if edge != -1 {
		t.Fatalf("nearestEdge(nil) edge = %d, want -1", edge)
	}
	if !math.IsInf(dist, 1) {
		t.Fatalf("nearestEdge(nil) dist = %v, want +Inf", dist)
	}

	edge, dist = nearestEdge(Point{1, 1}, []Point{})
	if edge != -1 || !math.IsInf(dist, 1) {
		t.Fatalf("nearestEdge(empty) = (%d, %v), want (-1, +Inf)", edge, dist)
	}
}

func TestInsertPointOnEdge(t *testing.T) {
	wantOrig := []Point{{0, 0}, {10, 0}, {10, 10}, {0, 10}}

	tests := []struct {
		name string
		edge int
		p    Point
		want []Point
	}{
		{
			name: "middle edge",
			edge: 1,
			p:    Point{10, 5},
			want: []Point{{0, 0}, {10, 0}, {10, 5}, {10, 10}, {0, 10}},
		},
		{
			name: "first edge",
			edge: 0,
			p:    Point{5, 0},
			want: []Point{{0, 0}, {5, 0}, {10, 0}, {10, 10}, {0, 10}},
		},
		{
			// The closing edge (last->first) inserts at the tail.
			name: "closing edge",
			edge: 3,
			p:    Point{0, 5},
			want: []Point{{0, 0}, {10, 0}, {10, 10}, {0, 10}, {0, 5}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			poly := clonePoints(squarePoly)
			got := insertPointOnEdge(poly, tc.edge, tc.p)
			wantPoly(t, "insertPointOnEdge", got, tc.want)
			wantNotAliased(t, "insertPointOnEdge", got, poly)
			wantPoly(t, "input after insertPointOnEdge", poly, wantOrig)
		})
	}
}

func TestInsertPointOnEdgeOutOfRange(t *testing.T) {
	for _, edge := range []int{-1, 4, 99} {
		t.Run(fmt.Sprintf("edge=%d", edge), func(t *testing.T) {
			poly := clonePoints(squarePoly)
			got := insertPointOnEdge(poly, edge, Point{1, 1})
			wantPoly(t, "insertPointOnEdge", got, squarePoly)
			wantNotAliased(t, "insertPointOnEdge", got, poly)
			wantPoly(t, "input after insertPointOnEdge", poly, squarePoly)
		})
	}
}

func TestInsertPointOnEdgeEmpty(t *testing.T) {
	if got := insertPointOnEdge(nil, 0, Point{1, 1}); len(got) != 0 {
		t.Fatalf("insertPointOnEdge(nil) = %s, want empty", polyString(got))
	}
	if got := insertPointOnEdge([]Point{}, 0, Point{1, 1}); len(got) != 0 {
		t.Fatalf("insertPointOnEdge(empty) = %s, want empty", polyString(got))
	}
}

func TestRemoveVertex(t *testing.T) {
	pentagon := []Point{{0, 0}, {4, 0}, {6, 3}, {3, 6}, {0, 3}}
	pentagonCopy := clonePoints(pentagon)

	tests := []struct {
		name string
		poly []Point
		i    int
		want []Point
	}{
		{"quad first", squarePoly, 0, []Point{{10, 0}, {10, 10}, {0, 10}}},
		{"quad middle", squarePoly, 1, []Point{{0, 0}, {10, 10}, {0, 10}}},
		{"quad last", squarePoly, 3, []Point{{0, 0}, {10, 0}, {10, 10}}},
		{"pentagon middle", pentagon, 2, []Point{{0, 0}, {4, 0}, {3, 6}, {0, 3}}},
		{"pentagon first", pentagon, 0, []Point{{4, 0}, {6, 3}, {3, 6}, {0, 3}}},
		{"pentagon last", pentagon, 4, []Point{{0, 0}, {4, 0}, {6, 3}, {3, 6}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			poly := clonePoints(tc.poly)
			got := removeVertex(poly, tc.i)
			wantPoly(t, "removeVertex", got, tc.want)
			wantNotAliased(t, "removeVertex", got, poly)
			wantPoly(t, "input after removeVertex", poly, tc.poly)
		})
	}

	wantPoly(t, "pentagon untouched", pentagon, pentagonCopy)
}

func TestRemoveVertexTriangleAndOutOfRange(t *testing.T) {
	// len <= 3 must never degenerate the polygon.
	if got := removeVertex(trianglePoly, 1); got != nil {
		t.Fatalf("removeVertex(triangle) = %s, want nil", polyString(got))
	}
	if got := removeVertex([]Point{{0, 0}, {1, 1}}, 0); got != nil {
		t.Fatalf("removeVertex(2 points) = %s, want nil", polyString(got))
	}
	if got := removeVertex(nil, 0); got != nil {
		t.Fatalf("removeVertex(nil) = %s, want nil", polyString(got))
	}

	for _, i := range []int{-1, 4, 99} {
		t.Run(fmt.Sprintf("i=%d", i), func(t *testing.T) {
			poly := clonePoints(squarePoly)
			got := removeVertex(poly, i)
			wantPoly(t, "removeVertex", got, squarePoly)
			wantNotAliased(t, "removeVertex", got, poly)
			wantPoly(t, "input after removeVertex", poly, squarePoly)
		})
	}
}

func TestTranslatePolygon(t *testing.T) {
	poly := clonePoints(squarePoly)

	got := translatePolygon(poly, Point{X: 3, Y: -4})
	want := []Point{{3, -4}, {13, -4}, {13, 6}, {3, 6}}
	wantPoly(t, "translatePolygon", got, want)
	wantNotAliased(t, "translatePolygon", got, poly)
	wantPoly(t, "input after translatePolygon", poly, squarePoly)

	// Mutating the result must not reach the input.
	got[0] = Point{99, 99}
	wantPoly(t, "input after mutating result", poly, squarePoly)

	// Zero translation still returns an independent copy.
	same := translatePolygon(poly, Point{})
	wantPoly(t, "translatePolygon zero", same, squarePoly)
	wantNotAliased(t, "translatePolygon zero", same, poly)

	if got := translatePolygon(nil, Point{1, 1}); len(got) != 0 {
		t.Fatalf("translatePolygon(nil) = %s, want empty", polyString(got))
	}
}

func TestPolygonBBox(t *testing.T) {
	tests := []struct {
		name       string
		poly       []Point
		x, y, w, h float64
	}{
		{"square", squarePoly, 0, 0, 10, 10},
		{"negative coords", negSquarePoly, -5, -3, 7, 10},
		{"unsorted vertices", []Point{{2, 5}, {9, -3}, {4, 4}}, 2, -3, 7, 8},
		{"single point", []Point{{4, 7}}, 4, 7, 0, 0},
		{"empty", nil, 0, 0, 0, 0},
		{"empty slice", []Point{}, 0, 0, 0, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			x, y, w, h := polygonBBox(tc.poly)
			if x != tc.x || y != tc.y || w != tc.w || h != tc.h {
				t.Fatalf("polygonBBox(%s) = (%v, %v, %v, %v), want (%v, %v, %v, %v)",
					polyString(tc.poly), x, y, w, h, tc.x, tc.y, tc.w, tc.h)
			}
		})
	}
}
