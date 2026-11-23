package ir

import (
	"cmp"
	"math"
	"slices"
)

// finite reports whether v is neither NaN nor an infinity.
func finite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

// centroid returns the mean of pts; the zero point for an empty slice.
func centroid(pts [][2]float64) [2]float64 {
	if len(pts) == 0 {
		return [2]float64{}
	}
	var sx, sy float64
	for _, p := range pts {
		sx += p[0]
		sy += p[1]
	}
	n := float64(len(pts))
	return [2]float64{sx / n, sy / n}
}

// dist is the euclidean distance between a and b.
func dist(a, b [2]float64) float64 {
	return math.Hypot(b[0]-a[0], b[1]-a[1])
}

// hasQuadParams reports whether the derived rotated_bbox cache describes a
// usable box. A missing size cannot be told apart from a zero one, so a
// positive RW and RH is what marks the cache as present.
func hasQuadParams(a *Annotation) bool {
	return a.RW > 0 && a.RH > 0
}

// orderQuad reorders the 4 vertices of a rotated_bbox into the canonical
// order: counter-clockwise around the centroid (increasing atan2 in image
// coordinates, i.e. the polygon runs p0->p1->p2->p3), starting at the
// leftmost vertex, ties broken by the topmost one.
//
// The input is not modified.
func orderQuad(pts [][2]float64) [][2]float64 {
	out := make([][2]float64, len(pts))
	copy(out, pts)
	c := centroid(out)
	slices.SortStableFunc(out, func(a, b [2]float64) int {
		return cmp.Compare(
			math.Atan2(a[1]-c[1], a[0]-c[0]),
			math.Atan2(b[1]-c[1], b[0]-c[0]),
		)
	})
	start := 0
	for i := 1; i < len(out); i++ {
		if out[i][0] < out[start][0] || (out[i][0] == out[start][0] && out[i][1] < out[start][1]) {
			start = i
		}
	}
	if start == 0 {
		return out
	}
	rot := make([][2]float64, 0, len(out))
	rot = append(rot, out[start:]...)
	return append(rot, out[:start]...)
}

// quadParams recomputes the derived rotated_bbox cache from 4 canonically
// ordered vertices: the centroid, the opposite-edge averages as width and
// height, and the p0->p1 direction as angle.
func quadParams(pts [][2]float64) (cx, cy, rw, rh, angle float64) {
	c := centroid(pts)
	rw = (dist(pts[0], pts[1]) + dist(pts[2], pts[3])) / 2
	rh = (dist(pts[1], pts[2]) + dist(pts[3], pts[0])) / 2
	angle = math.Atan2(pts[1][1]-pts[0][1], pts[1][0]-pts[0][0])
	return c[0], c[1], rw, rh, angle
}

// quadFromParams builds the 4 vertices of a rotated box from the derived
// cache: the axis-aligned corners of rw x rh around the centre, then rotated
// by angle, so that p0->p1 points along angle and the polygon order matches
// quadParams.
func quadFromParams(cx, cy, rw, rh, angle float64) [][2]float64 {
	cos, sin := math.Cos(angle), math.Sin(angle)
	local := [4][2]float64{
		{-rw / 2, -rh / 2},
		{rw / 2, -rh / 2},
		{rw / 2, rh / 2},
		{-rw / 2, rh / 2},
	}
	pts := make([][2]float64, 4)
	for i, p := range local {
		pts[i] = [2]float64{
			cx + p[0]*cos - p[1]*sin,
			cy + p[0]*sin + p[1]*cos,
		}
	}
	return pts
}

// ringAbsArea is the absolute shoelace area of a closed ring.
func ringAbsArea(ring [][2]float64) float64 {
	if len(ring) < 3 {
		return 0
	}
	var sum float64
	for i, p := range ring {
		q := ring[(i+1)%len(ring)]
		sum += p[0]*q[1] - q[0]*p[1]
	}
	return math.Abs(sum) / 2
}

// clampCoord clips v into [0, max]; NaN is left alone so Validate can report
// it instead of Normalize silently turning it into 0.
func clampCoord(v float64, max int) (float64, bool) {
	if math.IsNaN(v) {
		return v, false
	}
	if v < 0 {
		return 0, true
	}
	if lim := float64(max); v > lim {
		return lim, true
	}
	return v, false
}

// clipPoints clips pts in place; it reports whether any coordinate moved.
func clipPoints(pts [][2]float64, width, height int) bool {
	clipped := false
	for i := range pts {
		if v, ok := clampCoord(pts[i][0], width); ok {
			pts[i][0], clipped = v, true
		}
		if v, ok := clampCoord(pts[i][1], height); ok {
			pts[i][1], clipped = v, true
		}
	}
	return clipped
}
