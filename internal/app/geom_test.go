package app

import (
	"math"
	"math/rand"
	"testing"
)

// geomNear fails when |got-want| > tol.
func geomNear(t *testing.T, what string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("%s = %.12f, want %.12f (tol %g)", what, got, want, tol)
	}
}

// geomNearPoint fails when got and want differ by more than tol in any axis.
func geomNearPoint(t *testing.T, what string, got, want Point, tol float64) {
	t.Helper()
	if math.Abs(got.X-want.X) > tol || math.Abs(got.Y-want.Y) > tol {
		t.Fatalf("%s = (%.12f, %.12f), want (%.12f, %.12f) (tol %g)",
			what, got.X, got.Y, want.X, want.Y, tol)
	}
}

func geomRandAnn(r *rand.Rand) Ann {
	return Ann{
		ID:    "rand",
		Cat:   r.Intn(9),
		CX:    r.Float64()*4000 - 2000,
		CY:    r.Float64()*4000 - 2000,
		W:     1 + r.Float64()*800,
		H:     1 + r.Float64()*800,
		Angle: r.Float64()*2*math.Pi - math.Pi,
	}
}

func TestZoomAroundPointKeepsImagePointFixed(t *testing.T) {
	cases := []struct {
		pan    Point
		zoom   float64
		cursor Point
		factor float64
	}{
		{Point{0, 0}, 1, Point{100, 50}, 2},
		{Point{-320.5, 12.25}, 0.37, Point{640, 400}, 1.8},
		{Point{1234, -567}, 3.1, Point{0, 0}, 0.5},
		{Point{17.5, 23.5}, 12.5, Point{800, 600}, 0.9},
		{Point{-5, -5}, 0.02, Point{10, 10}, 3},
	}
	for _, c := range cases {
		imgX := (c.cursor.X - c.pan.X) / c.zoom
		imgY := (c.cursor.Y - c.pan.Y) / c.zoom

		np, nz := zoomAroundPoint(c.pan, c.zoom, c.cursor, c.factor)

		gotX := (c.cursor.X - np.X) / nz
		gotY := (c.cursor.Y - np.Y) / nz
		if math.Abs(gotX-imgX) > 1e-6 || math.Abs(gotY-imgY) > 1e-6 {
			t.Fatalf("cursor image point moved: got (%.9f, %.9f), want (%.9f, %.9f)",
				gotX, gotY, imgX, imgY)
		}

		wantZoom := c.zoom * c.factor
		if wantZoom < minZoom {
			wantZoom = minZoom
		}
		if wantZoom > maxZoom {
			wantZoom = maxZoom
		}
		geomNear(t, "zoom", nz, wantZoom, 1e-12)
	}
}

func TestZoomClampsToLimits(t *testing.T) {
	pan := Point{40, -10}
	cursor := Point{300, 200}
	imgX := (cursor.X - pan.X) / 1.0
	imgY := (cursor.Y - pan.Y) / 1.0

	np, nz := zoomAroundPoint(pan, 1, cursor, 1e6)
	geomNear(t, "max zoom", nz, maxZoom, 1e-12)
	geomNear(t, "imgX at max zoom", (cursor.X-np.X)/nz, imgX, 1e-9)
	geomNear(t, "imgY at max zoom", (cursor.Y-np.Y)/nz, imgY, 1e-9)

	np, nz = zoomAroundPoint(pan, 1, cursor, 1e-6)
	geomNear(t, "min zoom", nz, minZoom, 1e-12)
	geomNear(t, "imgX at min zoom", (cursor.X-np.X)/nz, imgX, 1e-6)
	geomNear(t, "imgY at min zoom", (cursor.Y-np.Y)/nz, imgY, 1e-6)
}

func TestToLocalRoundTrip(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	for i := 0; i < 200; i++ {
		an := geomRandAnn(r)
		p := Point{r.Float64()*6000 - 3000, r.Float64()*6000 - 3000}

		back := toWorld(&an, toLocal(&an, p))
		geomNearPoint(t, "toWorld(toLocal(p))", back, p, 1e-9)
	}
}

func TestToLocalCenterIsOrigin(t *testing.T) {
	an := Ann{CX: 123.25, CY: -456.5, W: 80, H: 40, Angle: 1.1}
	geomNearPoint(t, "toLocal(center)", toLocal(&an, Point{an.CX, an.CY}), Point{0, 0}, 1e-12)

	// a point on the local +X axis must land on the rotated world direction
	p := toWorld(&an, Point{10, 0})
	got := toLocal(&an, p)
	geomNearPoint(t, "local +X axis", got, Point{10, 0}, 1e-9)
}

func TestCornersAxisAligned(t *testing.T) {
	an := Ann{CX: 100, CY: 200, W: 40, H: 20}
	c := corners(&an)
	want := [4]Point{{80, 190}, {120, 190}, {120, 210}, {80, 210}}
	for i := range want {
		geomNearPoint(t, "corner", c[i], want[i], 1e-12)
	}
}

func TestCornersRotatedQuarterTurn(t *testing.T) {
	an := Ann{CX: 100, CY: 200, W: 40, H: 20, Angle: math.Pi / 2}
	c := corners(&an)
	// local TL (-20,-10) rotated by +90deg is (10,-20)
	want := [4]Point{{110, 180}, {110, 220}, {90, 220}, {90, 180}}
	for i := range want {
		geomNearPoint(t, "rotated corner", c[i], want[i], 1e-9)
	}
}

func TestHandlePointsOrderAndRotateOffset(t *testing.T) {
	an := Ann{CX: 50, CY: -30, W: 100, H: 60, Angle: 0.35}

	hp := handlePoints(&an, 1)
	c := corners(&an)
	for i := 0; i < 4; i++ {
		geomNearPoint(t, "corner handle", hp[i], c[i], 1e-12)
	}

	// edge midpoints, in order T, R, B, L
	mid := func(h0, h1 Point) Point {
		return Point{(h0.X + h1.X) / 2, (h0.Y + h1.Y) / 2}
	}
	geomNearPoint(t, "T handle", hp[4], mid(c[0], c[1]), 1e-12)
	geomNearPoint(t, "R handle", hp[5], mid(c[1], c[2]), 1e-12)
	geomNearPoint(t, "B handle", hp[6], mid(c[2], c[3]), 1e-12)
	geomNearPoint(t, "L handle", hp[7], mid(c[3], c[0]), 1e-12)

	// rotate handle: handleRotateOffsetDIP/zoom above T, i.e. inverse in zoom
	for _, zoom := range []float64{0.5, 1, 2, 8} {
		h := handlePoints(&an, zoom)
		got := distance(h[4], h[8])
		geomNear(t, "rotate handle offset", got, handleRotateOffsetDIP/zoom, 1e-9)
		if got <= 0 {
			t.Fatalf("rotate handle offset must be positive, got %g", got)
		}
	}
}

func TestResizeHandleCornerKeepsOppositeCorner(t *testing.T) {
	angles := []float64{0, 0.6, -2.1}
	// per handle: local start point and the fixed (opposite) corner in the
	// original local frame
	starts := [4]Point{{-1, -1}, {1, -1}, {1, 1}, {-1, 1}}
	fixedSign := [4]Point{{1, 1}, {-1, 1}, {-1, -1}, {1, -1}}
	opposite := [4]int{2, 3, 0, 1}

	for _, angle := range angles {
		for handle := 0; handle < 4; handle++ {
			an := Ann{ID: "x", Cat: 2, CX: 300, CY: -120, W: 80, H: 50, Angle: angle}
			hw, hh := an.W/2, an.H/2

			start := Point{starts[handle].X * hw, starts[handle].Y * hh}
			local := Point{start.X + 37, start.Y - 21}

			out := resizeHandle(an, handle, local)
			if out.ID != an.ID || out.Cat != an.Cat || out.Angle != an.Angle {
				t.Fatalf("handle %d: identity fields changed: %+v", handle, out)
			}

			wantW := math.Abs(local.X - fixedSign[handle].X*hw)
			wantH := math.Abs(local.Y - fixedSign[handle].Y*hh)
			geomNear(t, "resized W", out.W, wantW, 1e-9)
			geomNear(t, "resized H", out.H, wantH, 1e-9)

			// the opposite corner must not move in world space
			fixedWorld := corners(&an)[opposite[handle]]
			nc := corners(&out)
			best := math.Inf(1)
			for _, q := range nc {
				if d := distance(q, fixedWorld); d < best {
					best = d
				}
			}
			if best > 1e-6 {
				t.Fatalf("handle %d angle %g: opposite corner moved by %g", handle, angle, best)
			}

			// the dragged corner follows the pointer in world space
			draggedWorld := corners(&out)
			best = math.Inf(1)
			want := toWorld(&an, local)
			for _, q := range draggedWorld {
				if d := distance(q, want); d < best {
					best = d
				}
			}
			if best > 1e-6 {
				t.Fatalf("handle %d angle %g: dragged corner off by %g", handle, angle, best)
			}
		}
	}
}

func TestResizeHandleEdgeChangesOneDimension(t *testing.T) {
	edges := []struct {
		handle       int
		start        Point // local, relative to W/2,H/2
		delta        Point // local drag
		fixedMid     Point // fixed edge midpoint, relative to W/2,H/2
		fixedMidSign Point // which midpoint of the result must coincide
		changesH     bool
	}{
		{4, Point{0, -1}, Point{0, -12}, Point{0, 1}, Point{0, 1}, true},  // T: bottom fixed
		{6, Point{0, 1}, Point{0, 12}, Point{0, -1}, Point{0, -1}, true},  // B: top fixed
		{5, Point{1, 0}, Point{12, 0}, Point{-1, 0}, Point{-1, 0}, false}, // R: left fixed
		{7, Point{-1, 0}, Point{-12, 0}, Point{1, 0}, Point{1, 0}, false}, // L: right fixed
	}

	for _, e := range edges {
		an := Ann{CX: 200, CY: 100, W: 120, H: 60, Angle: 0.8}
		hw, hh := an.W/2, an.H/2
		local := Point{e.start.X*hw + e.delta.X, e.start.Y*hh + e.delta.Y}

		out := resizeHandle(an, e.handle, local)

		if e.changesH {
			geomNear(t, "W unchanged", out.W, an.W, 1e-12)
			wantH := math.Abs(local.Y - e.fixedMid.Y*hh)
			geomNear(t, "resized H", out.H, wantH, 1e-9)
		} else {
			geomNear(t, "H unchanged", out.H, an.H, 1e-12)
			wantW := math.Abs(local.X - e.fixedMid.X*hw)
			geomNear(t, "resized W", out.W, wantW, 1e-9)
		}
		if out.Angle != an.Angle {
			t.Fatalf("handle %d: angle changed", e.handle)
		}

		// the fixed edge midpoint must stay where it was, in world space
		fixedWorld := toWorld(&an, Point{e.fixedMid.X * hw, e.fixedMid.Y * hh})
		afterWorld := toWorld(&out, Point{e.fixedMidSign.X * out.W / 2, e.fixedMidSign.Y * out.H / 2})
		geomNearPoint(t, "fixed edge midpoint", afterWorld, fixedWorld, 1e-6)

		// the center may only move across the dragged edge, never along it
		c := toLocal(&an, Point{out.CX, out.CY})
		if e.changesH {
			geomNear(t, "center stays on the vertical centerline", c.X, 0, 1e-9)
		} else {
			geomNear(t, "center stays on the horizontal centerline", c.Y, 0, 1e-9)
		}
	}
}

func TestResizeHandleMinimumSize(t *testing.T) {
	an := Ann{CX: 10, CY: 10, W: 80, H: 40, Angle: 0.3}
	// drag the BR handle exactly onto the TL corner
	out := resizeHandle(an, 2, Point{-40, -20})
	geomNear(t, "min W", out.W, minAnnSize, 1e-12)
	geomNear(t, "min H", out.H, minAnnSize, 1e-12)
}

func TestResizeHandleIgnoresRotateHandle(t *testing.T) {
	an := Ann{CX: 10, CY: 10, W: 80, H: 40, Angle: 0.3}
	out := resizeHandle(an, 8, Point{5, 5})
	if !sameAnn(out, an) {
		t.Fatalf("handle 8 must not resize: got %+v", out)
	}
}

func TestRotateToHandleUp(t *testing.T) {
	an := Ann{CX: 200, CY: 150, W: 80, H: 40}
	out := rotateTo(an, Point{200, 50}) // straight above the center
	geomNear(t, "angle with handle up", out.Angle, 0, 1e-12)
}

func TestRotateToMatchesPointerDirection(t *testing.T) {
	an := Ann{CX: -30, CY: 45, W: 100, H: 60, Angle: 0.4}
	pts := []Point{{0, 0}, {100, 45}, {-30, 500}, {260, 13}, {-130, 45}}
	for _, p := range pts {
		out := rotateTo(an, p)
		if out.W != an.W || out.H != an.H || out.CX != an.CX || out.CY != an.CY {
			t.Fatalf("rotateTo changed geometry: %+v", out)
		}
		// world direction from the center to the rotate handle
		h := handlePoints(&out, 1)[8]
		gotDir := math.Atan2(h.Y-out.CY, h.X-out.CX)
		wantDir := math.Atan2(p.Y-out.CY, p.X-out.CX)
		d := math.Abs(normalizeAngle(gotDir - wantDir))
		if d > 1e-9 {
			t.Fatalf("handle dir %.12f != pointer dir %.12f (p=%v)", gotDir, wantDir, p)
		}
	}
}

func TestRotateToQuarterTurns(t *testing.T) {
	an := Ann{CX: 0, CY: 0, W: 100, H: 40}
	geomNear(t, "pointer right", rotateTo(an, Point{50, 0}).Angle, math.Pi/2, 1e-12)
	geomNear(t, "pointer above", rotateTo(an, Point{0, -50}).Angle, 0, 1e-12)
	a := rotateTo(an, Point{0, 50}).Angle
	if math.Abs(math.Abs(a)-math.Pi) > 1e-12 {
		t.Fatalf("pointer below: angle %.12f, want +-pi", a)
	}
}

func TestSnapAngle(t *testing.T) {
	step := 15 * math.Pi / 180
	deg17 := 17 * math.Pi / 180
	geomNear(t, "snap 17deg", snapAngle(deg17, step), step, 1e-12)
	geomNear(t, "snap 7deg", snapAngle(7*math.Pi/180, step), 0, 1e-12)
	geomNear(t, "snap 8deg", snapAngle(8*math.Pi/180, step), step, 1e-12)
	geomNear(t, "snap -17deg", snapAngle(-deg17, step), -step, 1e-12)
	geomNear(t, "snap 44deg", snapAngle(44*math.Pi/180, step), 45*math.Pi/180, 1e-12)
	// snapping already-snapped values is idempotent
	a := snapAngle(deg17, step)
	geomNear(t, "snap idempotent", snapAngle(a, step), a, 1e-12)
	// a non-positive step leaves the angle alone
	geomNear(t, "zero step", snapAngle(deg17, 0), deg17, 1e-15)
}
