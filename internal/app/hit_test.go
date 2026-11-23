package app

import (
	"math"
	"testing"
)

// hitNear checks a full hit result.
func hitNear(t *testing.T, what string, got Hit, kind HitKind, ann, handle int) {
	t.Helper()
	if got.Kind != kind || got.Ann != ann || got.Handle != handle {
		t.Fatalf("%s: got {Ann:%d Kind:%d Handle:%d}, want {Ann:%d Kind:%d Handle:%d}",
			what, got.Ann, got.Kind, got.Handle, ann, kind, handle)
	}
}

func TestHitHandleBeatsEdgeAndBody(t *testing.T) {
	anns := []Ann{{ID: "a", CX: 0, CY: 0, W: 100, H: 60}}
	sel := 0

	// the TL corner is both a handle and (boundary) body; the handle wins
	hitNear(t, "TL handle", hitTestAnns(anns, sel, Point{-50, -30}, 1), HitResize, 0, 0)
	hitNear(t, "TR handle", hitTestAnns(anns, sel, Point{50, -30}, 1), HitResize, 0, 1)
	hitNear(t, "BR handle", hitTestAnns(anns, sel, Point{50, 30}, 1), HitResize, 0, 2)
	hitNear(t, "BL handle", hitTestAnns(anns, sel, Point{-50, 30}, 1), HitResize, 0, 3)
	hitNear(t, "T handle", hitTestAnns(anns, sel, Point{0, -30}, 1), HitResize, 0, 4)
	hitNear(t, "R handle", hitTestAnns(anns, sel, Point{50, 0}, 1), HitResize, 0, 5)
	hitNear(t, "B handle", hitTestAnns(anns, sel, Point{0, 30}, 1), HitResize, 0, 6)
	hitNear(t, "L handle", hitTestAnns(anns, sel, Point{-50, 0}, 1), HitResize, 0, 7)
	hitNear(t, "rotate handle", hitTestAnns(anns, sel, Point{0, -30 - handleRotateOffsetDIP}, 1), HitRotate, 0, 8)

	// near a corner handle but inside the body and off the edges: body
	hitNear(t, "body", hitTestAnns(anns, sel, Point{10, 0}, 1), HitBody, 0, -1)

	// far outside: nothing
	hitNear(t, "outside", hitTestAnns(anns, sel, Point{400, 400}, 1), HitNone, -1, -1)
}

func TestHitEdgeBeatsBody(t *testing.T) {
	anns := []Ann{{ID: "a", CX: 0, CY: 0, W: 100, H: 60}}
	sel := 0

	// a quarter along the top edge: outside the handle radius, on the edge
	hitNear(t, "top edge", hitTestAnns(anns, sel, Point{25, -30}, 1), HitResize, 0, 4)
	hitNear(t, "right edge", hitTestAnns(anns, sel, Point{50, 12}, 1), HitResize, 0, 5)
	hitNear(t, "bottom edge", hitTestAnns(anns, sel, Point{-25, 30}, 1), HitResize, 0, 6)
	hitNear(t, "left edge", hitTestAnns(anns, sel, Point{-50, -12}, 1), HitResize, 0, 7)
}

func TestHitThresholdScalesWithZoom(t *testing.T) {
	anns := []Ann{{ID: "a", CX: 0, CY: 0, W: 100, H: 60}}
	sel := 0

	// 4.24px diagonally outside the BR corner handle
	p := Point{53, 33}

	// zoom 1: handle radius is 5px -> hits the corner handle
	hitNear(t, "zoom 1", hitTestAnns(anns, sel, p, 1), HitResize, 0, 2)

	// zoom 2: handle radius is 2.5 image px, edge radius 1.5px -> nothing
	hitNear(t, "zoom 2", hitTestAnns(anns, sel, p, 2), HitNone, -1, -1)

	// 4px inside the top edge at x=20: body at zoom 1, edge at zoom 0.5
	q := Point{20, -26}
	hitNear(t, "edge band zoom 1", hitTestAnns(anns, sel, q, 1), HitBody, 0, -1)
	hitNear(t, "edge band zoom 0.5", hitTestAnns(anns, sel, q, 0.5), HitResize, 0, 4)

	// rotate handle width scales with 1/zoom as well
	r := handlePoints(&anns[0], 4)[8] // offset 6px at zoom 4
	hitNear(t, "rotate handle zoom 4", hitTestAnns(anns, sel, r, 4), HitRotate, 0, 8)
	// the same world point is far outside the 1.25px pick radius at zoom 4? no:
	// at zoom 4 the radius is 1.25 and the point is exactly on the handle, so
	// move 2px further out to prove the threshold shrank
	r2 := Point{r.X + 2, r.Y}
	hitNear(t, "just outside rotate handle", hitTestAnns(anns, sel, r2, 4), HitNone, -1, -1)
}

func TestHitBodyZOrder(t *testing.T) {
	anns := []Ann{
		{ID: "bottom", CX: 0, CY: 0, W: 200, H: 200},
		{ID: "middle", CX: 0, CY: 0, W: 100, H: 100},
		{ID: "top", CX: 0, CY: 0, W: 40, H: 40},
	}

	// the last annotation drawn is on top and wins the body hit
	hitNear(t, "topmost", hitTestAnns(anns, -1, Point{0, 0}, 1), HitBody, 2, -1)
	hitNear(t, "middle", hitTestAnns(anns, -1, Point{30, 0}, 1), HitBody, 1, -1)
	hitNear(t, "bottom", hitTestAnns(anns, -1, Point{0, 80}, 1), HitBody, 0, -1)
	hitNear(t, "outside all", hitTestAnns(anns, -1, Point{0, 300}, 1), HitNone, -1, -1)
}

func TestHitNoSelectionOnlyHitsBody(t *testing.T) {
	anns := []Ann{{ID: "a", CX: 0, CY: 0, W: 100, H: 60}}

	// the rotate handle position with nothing selected: outside the body
	hitNear(t, "rotate handle, no sel", hitTestAnns(anns, -1, Point{0, -54}, 1), HitNone, -1, -1)

	// an outline point with nothing selected still hits the body (borders count)
	hitNear(t, "corner, no sel", hitTestAnns(anns, -1, Point{-50, -30}, 1), HitBody, 0, -1)
	hitNear(t, "inside, no sel", hitTestAnns(anns, -1, Point{1, 1}, 1), HitBody, 0, -1)

	// an out-of-range selection is treated as none
	hitNear(t, "sel out of range", hitTestAnns(anns, 5, Point{0, -54}, 1), HitNone, -1, -1)
	hitNear(t, "empty slice", hitTestAnns(nil, 0, Point{0, 0}, 1), HitNone, -1, -1)
}

func TestHitSelectedHandleBeatsTopmostBody(t *testing.T) {
	anns := []Ann{
		{ID: "small", CX: 0, CY: 0, W: 100, H: 60},
		{ID: "huge", CX: 0, CY: 0, W: 1000, H: 1000}, // covers everything, drawn on top
	}

	// BR handle of the selected annotation, underneath the huge one
	hitNear(t, "selected handle", hitTestAnns(anns, 0, Point{50, 30}, 1), HitResize, 0, 2)
	// a plain body point still resolves to the topmost annotation
	hitNear(t, "topmost body", hitTestAnns(anns, 0, Point{10, 10}, 1), HitBody, 1, -1)
	// selecting the huge annotation puts its own handles on top
	hitNear(t, "huge handle", hitTestAnns(anns, 1, Point{500, 500}, 1), HitResize, 1, 2)
}

func TestHitRotatedAnnotation(t *testing.T) {
	an := Ann{ID: "r", CX: 100, CY: 100, W: 80, H: 40, Angle: math.Pi / 2}
	anns := []Ann{an}

	// after a quarter turn the local TL corner sits at (cx+20, cy-40)
	hp := handlePoints(&an, 1)
	for i, h := range hp {
		want := HitResize
		if i == 8 {
			want = HitRotate
		}
		hitNear(t, "rotated handle", hitTestAnns(anns, 0, h, 1), want, 0, i)
	}

	// the center is a body hit; a point just outside the rotated rectangle is not
	hitNear(t, "rotated body", hitTestAnns(anns, 0, Point{100, 100}, 1), HitBody, 0, -1)
	hitNear(t, "rotated outside", hitTestAnns(anns, 0, Point{150, 100}, 1), HitNone, -1, -1)
}

func TestInsideAnnBorders(t *testing.T) {
	an := Ann{CX: 0, CY: 0, W: 100, H: 60, Angle: 0.5}
	for _, l := range []Point{{0, 0}, {50, 0}, {0, 30}, {-50, -30}, {49.9, 29.9}} {
		if !insideAnn(&an, toWorld(&an, l)) {
			t.Fatalf("local point %v should be inside", l)
		}
	}
	for _, l := range []Point{{50.1, 0}, {0, 30.1}, {-50.1, -30.1}} {
		if insideAnn(&an, toWorld(&an, l)) {
			t.Fatalf("local point %v should be outside", l)
		}
	}
}
