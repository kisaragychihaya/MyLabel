package app

import "math"

// Point is a 2D point in image pixel coordinates (unless documented otherwise).
type Point struct{ X, Y float64 }

const (
	minZoom = 0.02
	maxZoom = 64.0

	// handleRotateOffsetDIP is the screen-space distance of the rotate
	// handle above the top edge midpoint.
	handleRotateOffsetDIP = 24

	// minAnnSize is the smallest W or H a resize drag may produce, in image
	// pixels. It keeps the annotation invertible (non-zero area).
	minAnnSize = 1.0
)

// zoomAroundPoint returns the new pan and zoom that keep the image point
// under cursor fixed while zooming by factor. Pan is in screen DIP, cursor
// is in screen DIP relative to the canvas origin. Pure function.
func zoomAroundPoint(pan Point, zoom float64, cursor Point, factor float64) (newPan Point, newZoom float64) {
	z := zoom
	if !(z > 0) || math.IsInf(z, 0) || math.IsNaN(z) {
		z = minZoom
	}
	if !(factor > 0) || math.IsInf(factor, 0) || math.IsNaN(factor) {
		return pan, z
	}

	newZoom = clampFloat(z*factor, minZoom, maxZoom)

	// image point under the cursor before the zoom
	imgX := (cursor.X - pan.X) / z
	imgY := (cursor.Y - pan.Y) / z

	// place pan so that same image point lands under the cursor again
	newPan = Point{
		X: cursor.X - imgX*newZoom,
		Y: cursor.Y - imgY*newZoom,
	}
	return newPan, newZoom
}

// toLocal maps a world (image pixel) point into the annotation's local
// space: origin at the center, axes unrotated (rotate by -Angle).
func toLocal(an *Ann, p Point) Point {
	c, s := math.Cos(an.Angle), math.Sin(an.Angle)
	dx, dy := p.X-an.CX, p.Y-an.CY
	return Point{
		X: dx*c + dy*s,
		Y: -dx*s + dy*c,
	}
}

// toWorld maps a local-space point back to world (image pixel) coordinates.
func toWorld(an *Ann, p Point) Point {
	c, s := math.Cos(an.Angle), math.Sin(an.Angle)
	return Point{
		X: an.CX + p.X*c - p.Y*s,
		Y: an.CY + p.X*s + p.Y*c,
	}
}

// corners returns the 4 corners in world coordinates, in local order
// TL, TR, BR, BL (local -W/2,-H/2 is TL).
func corners(an *Ann) [4]Point {
	hw, hh := an.W/2, an.H/2
	return [4]Point{
		toWorld(an, Point{-hw, -hh}),
		toWorld(an, Point{hw, -hh}),
		toWorld(an, Point{hw, hh}),
		toWorld(an, Point{-hw, hh}),
	}
}

// handlePoints returns the 9 handle positions in world (image pixel)
// coordinates:
//   - [0..3] corners TL, TR, BR, BL
//   - [4..7] edge midpoints T, R, B, L
//   - [8]    rotate handle, handleRotateOffsetDIP/zoom above the T midpoint
func handlePoints(an *Ann, zoom float64) [9]Point {
	z := safeZoom(zoom)
	hw, hh := an.W/2, an.H/2
	var hs [9]Point
	hs[0] = toWorld(an, Point{-hw, -hh}) // TL
	hs[1] = toWorld(an, Point{hw, -hh})  // TR
	hs[2] = toWorld(an, Point{hw, hh})   // BR
	hs[3] = toWorld(an, Point{-hw, hh})  // BL
	hs[4] = toWorld(an, Point{0, -hh})   // T
	hs[5] = toWorld(an, Point{hw, 0})    // R
	hs[6] = toWorld(an, Point{0, hh})    // B
	hs[7] = toWorld(an, Point{-hw, 0})   // L
	hs[8] = toWorld(an, Point{0, -hh - handleRotateOffsetDIP/z})
	return hs
}

// snapAngle snaps a to the nearest multiple of step (radians).
func snapAngle(a, step float64) float64 {
	if !(step > 0) || math.IsInf(step, 0) || math.IsNaN(step) {
		return a
	}
	return math.Round(a/step) * step
}

// resizeHandle applies a resize-handle drag in local space. local is the
// pointer position in local coordinates; handle is 0..7 (corners TL,TR,BR,BL,
// edges T,R,B,L). It returns the updated annotation (W, H, CX, CY may change;
// opposite corner/edge stays fixed in world space).
func resizeHandle(an Ann, handle int, local Point) Ann {
	hw, hh := an.W/2, an.H/2

	// Fixed corner / fixed edge midpoint, in the ORIGINAL local frame (the
	// frame `local` is expressed in). Its world position must not move.
	var fx, fy float64
	switch handle {
	case 0: // TL dragged, BR fixed
		fx, fy = hw, hh
	case 1: // TR dragged, BL fixed
		fx, fy = -hw, hh
	case 2: // BR dragged, TL fixed
		fx, fy = -hw, -hh
	case 3: // BL dragged, TR fixed
		fx, fy = hw, -hh
	case 4: // T dragged, bottom edge fixed
		fx, fy = 0, hh
	case 5: // R dragged, left edge fixed
		fx, fy = -hw, 0
	case 6: // B dragged, top edge fixed
		fx, fy = 0, -hh
	case 7: // L dragged, right edge fixed
		fx, fy = hw, 0
	default:
		return an
	}

	// New center in the original local frame; 0 on the axis an edge drag
	// leaves alone keeps that axis' centerline where it was.
	var cx, cy float64
	w, h := an.W, an.H

	switch handle {
	case 0, 1, 2, 3:
		dx, dy := local.X-fx, local.Y-fy
		w = math.Max(math.Abs(dx), minAnnSize)
		h = math.Max(math.Abs(dy), minAnnSize)
		cx = fx + math.Copysign(w/2, dx)
		cy = fy + math.Copysign(h/2, dy)
	case 4, 6:
		dy := local.Y - fy
		h = math.Max(math.Abs(dy), minAnnSize)
		cy = fy + math.Copysign(h/2, dy)
	case 5, 7:
		dx := local.X - fx
		w = math.Max(math.Abs(dx), minAnnSize)
		cx = fx + math.Copysign(w/2, dx)
	}

	out := an
	out.W, out.H = w, h
	c := toWorld(&an, Point{cx, cy})
	out.CX, out.CY = c.X, c.Y
	return out
}

// rotateTo returns the annotation with Angle set so the rotate handle points
// at world point p (image pixels). The rotate handle sits at local
// (0, -H/2-offset), so the angle is measured from the -Y axis.
func rotateTo(an Ann, p Point) Ann {
	out := an
	out.Angle = normalizeAngle(math.Atan2(p.Y-an.CY, p.X-an.CX) + math.Pi/2)
	return out
}

// obbCorners is how many clicks the obb tool takes: the four corners of the
// rotated box, in the order p0 (top left), p1 (top right), p2, p3
// (docs/phase3-design.md §5.2).
const obbCorners = 4

// minObbSide is the smallest W or H the four clicks may describe, in image
// pixels: a click that lands almost on top of the previous one describes a box
// with no area, which is dropped like a rectangle drag that is too small.
const minObbSide = 1.0

// obbFromPoints fits the rotated rectangle the obb tool's four clicked corners
// describe (docs/phase3-design.md §5.2): the first edge gives the angle and the
// width, the last edge the height, and the center is the four points' centroid.
// The resulting annotation is KindRect with a non-zero Angle — an obb is a
// rotated rectangle in the app model. It reports false when fewer than four
// points are given or the box would have no area. Pure function.
func obbFromPoints(pts []Point) (Ann, bool) {
	if len(pts) < obbCorners {
		return Ann{}, false
	}
	p0, p1, p3 := pts[0], pts[1], pts[3]
	w := math.Hypot(p1.X-p0.X, p1.Y-p0.Y)
	h := math.Hypot(p3.X-p0.X, p3.Y-p0.Y)
	if !(w >= minObbSide) || !(h >= minObbSide) {
		return Ann{}, false
	}
	var cx, cy float64
	for _, p := range pts[:obbCorners] {
		cx += p.X
		cy += p.Y
	}
	cx /= obbCorners
	cy /= obbCorners
	angle := normalizeAngle(math.Atan2(p1.Y-p0.Y, p1.X-p0.X))
	if math.IsNaN(cx) || math.IsNaN(cy) || math.IsNaN(angle) {
		return Ann{}, false
	}
	return Ann{Kind: KindRect, CX: cx, CY: cy, W: w, H: h, Angle: angle}, true
}

// bounds is an axis-aligned box in image pixels: the marquee selection's
// rectangle and the box an annotation occupies (§5).
type bounds struct{ MinX, MinY, MaxX, MaxY float64 }

// annBounds returns the axis-aligned bounding box of an annotation: the
// vertices' box for a polygon, the rotated corners' box for a rectangle, whose
// own W×H would not enclose a rotated one.
func annBounds(an *Ann) bounds {
	if isPoly(an) {
		x, y, w, h := polygonBBox(an.Points)
		return bounds{MinX: x, MinY: y, MaxX: x + w, MaxY: y + h}
	}
	c := corners(an)
	b := bounds{MinX: c[0].X, MinY: c[0].Y, MaxX: c[0].X, MaxY: c[0].Y}
	for _, q := range c[1:] {
		b.MinX = min(b.MinX, q.X)
		b.MinY = min(b.MinY, q.Y)
		b.MaxX = max(b.MaxX, q.X)
		b.MaxY = max(b.MaxY, q.Y)
	}
	return b
}

// boundsThrough returns the normalized box with a and b as opposite corners,
// as a marquee drag lays it out.
func boundsThrough(a, b Point) bounds {
	return bounds{
		MinX: min(a.X, b.X),
		MinY: min(a.Y, b.Y),
		MaxX: max(a.X, b.X),
		MaxY: max(a.Y, b.Y),
	}
}

// intersects reports whether the two boxes overlap. Touching borders count:
// a marquee that grazes an annotation selects it, as it does in editors.
func (b bounds) intersects(o bounds) bool {
	return b.MinX <= o.MaxX && o.MinX <= b.MaxX && b.MinY <= o.MaxY && o.MinY <= b.MaxY
}

// area returns the box's area in square image pixels, 0 when it is a line or
// a point.
func (b bounds) area() float64 {
	w, h := b.MaxX-b.MinX, b.MaxY-b.MinY
	if !(w > 0) || !(h > 0) {
		return 0
	}
	return w * h
}

// safeZoom replaces a non-positive or non-finite zoom with minZoom so that
// DIP-to-pixel conversions never divide by zero.
func safeZoom(zoom float64) float64 {
	if !(zoom > 0) || math.IsInf(zoom, 0) || math.IsNaN(zoom) {
		return minZoom
	}
	return zoom
}

// clampFloat clamps v into [lo, hi].
func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// normalizeAngle wraps a into (-Pi, Pi], keeping equal angles equal.
func normalizeAngle(a float64) float64 {
	a = math.Mod(a+math.Pi, 2*math.Pi)
	if a < 0 {
		a += 2 * math.Pi
	}
	return a - math.Pi
}
