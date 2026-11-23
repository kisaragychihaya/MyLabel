package app

import "math"

// Pure polygon geometry for the seg annotation kind. All coordinates are
// image pixels; polygons are implicitly closed (last vertex connects back to
// the first). See docs/phase2-design.md §3.
//
// Every function here is pure: inputs are never mutated, and the functions
// that return a polygon always return a freshly allocated slice. Undo
// snapshots (phase 2 deep-copy discipline) rely on that.

// polyEps is the tolerance for "point lies on the polygon outline". It is in
// image pixels, so it is only a float round-trip guard, not a pick radius.
const polyEps = 1e-9

// pointInPolygon reports whether p lies inside the closed polygon poly using
// even-odd ray casting. Polygons with fewer than 3 vertices contain nothing.
// Points on an edge or vertex count as inside.
func pointInPolygon(p Point, poly []Point) bool {
	n := len(poly)
	if n < 3 {
		return false
	}

	// Outline first: the ray test alone classifies boundary points
	// inconsistently (a vertex hit may toggle twice, once, or never), so any
	// point within polyEps of an edge is inside by definition.
	for i := 0; i < n; i++ {
		if distPointSegment(p, poly[i], poly[(i+1)%n]) <= polyEps {
			return true
		}
	}

	// Half-open rule: count an edge as crossing the ray only when exactly one
	// end is strictly above p.Y. Horizontal edges never count, and a ray
	// passing exactly through a shared vertex counts that vertex once (on the
	// edge leaving it upward), never twice.
	inside := false
	for i, j := 0, n-1; i < n; j, i = i, i+1 {
		yi, yj := poly[i].Y, poly[j].Y
		if (yi > p.Y) == (yj > p.Y) {
			continue
		}
		x := poly[i].X + (p.Y-yi)*(poly[j].X-poly[i].X)/(yj-yi)
		if p.X < x {
			inside = !inside
		}
	}
	return inside
}

// distPointSegment returns the distance from p to the segment ab.
func distPointSegment(p, a, b Point) float64 {
	dx, dy := b.X-a.X, b.Y-a.Y
	if dx == 0 && dy == 0 {
		return math.Hypot(p.X-a.X, p.Y-a.Y)
	}
	t := ((p.X-a.X)*dx + (p.Y-a.Y)*dy) / (dx*dx + dy*dy)
	t = clampFloat(t, 0, 1)
	return math.Hypot(p.X-(a.X+t*dx), p.Y-(a.Y+t*dy))
}

// nearestEdge returns the index of the polygon edge closest to p and that
// distance. Edge i connects poly[i] to poly[(i+1)%len(poly)]. The result is
// (-1, +Inf) for an empty polygon.
func nearestEdge(p Point, poly []Point) (edge int, dist float64) {
	n := len(poly)
	if n == 0 {
		return -1, math.Inf(1)
	}
	edge, dist = -1, math.Inf(1)
	for i := 0; i < n; i++ {
		if d := distPointSegment(p, poly[i], poly[(i+1)%n]); d < dist {
			edge, dist = i, d
		}
	}
	return edge, dist
}

// insertPointOnEdge returns a new slice with p inserted between poly[edge]
// and poly[(edge+1)%n]. The input slice is never modified. An out-of-range
// edge yields a copy of poly (no insertion, no panic).
func insertPointOnEdge(poly []Point, edge int, p Point) []Point {
	if edge < 0 || edge >= len(poly) {
		return clonePoints(poly)
	}
	out := make([]Point, 0, len(poly)+1)
	out = append(out, poly[:edge+1]...)
	out = append(out, p)
	out = append(out, poly[edge+1:]...)
	return out
}

// removeVertex returns a new slice without poly[i]. Removing a vertex from a
// triangle would degenerate the polygon, so len(poly) <= 3 yields nil. The
// input slice is never modified. An out-of-range i yields a copy of poly.
func removeVertex(poly []Point, i int) []Point {
	if len(poly) <= 3 {
		return nil
	}
	if i < 0 || i >= len(poly) {
		return clonePoints(poly)
	}
	out := make([]Point, 0, len(poly)-1)
	out = append(out, poly[:i]...)
	out = append(out, poly[i+1:]...)
	return out
}

// polygonBBox returns the axis-aligned bounding box of poly. An empty
// polygon yields zeros.
func polygonBBox(poly []Point) (x, y, w, h float64) {
	if len(poly) == 0 {
		return 0, 0, 0, 0
	}
	minX, minY := poly[0].X, poly[0].Y
	maxX, maxY := minX, minY
	for _, q := range poly[1:] {
		minX = math.Min(minX, q.X)
		minY = math.Min(minY, q.Y)
		maxX = math.Max(maxX, q.X)
		maxY = math.Max(maxY, q.Y)
	}
	return minX, minY, maxX - minX, maxY - minY
}

// translatePolygon returns a new slice with every vertex moved by d. The
// input slice is never modified.
func translatePolygon(poly []Point, d Point) []Point {
	if poly == nil {
		return nil
	}
	out := make([]Point, len(poly))
	for i, q := range poly {
		out[i] = Point{X: q.X + d.X, Y: q.Y + d.Y}
	}
	return out
}

// clonePoints returns a copy of poly that shares no backing array with it.
func clonePoints(poly []Point) []Point {
	if poly == nil {
		return nil
	}
	out := make([]Point, len(poly))
	copy(out, poly)
	return out
}
