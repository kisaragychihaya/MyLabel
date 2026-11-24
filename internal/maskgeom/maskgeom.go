// Package maskgeom turns a low-resolution segmentation mask into annotation
// geometry: the foreground bounding box, largest-connected-component
// filtering, Moore-neighbour boundary tracing and Douglas-Peucker
// simplification. Everything here is pure Go with no imports beyond the
// standard library and no global state, so each function is independently
// testable. Coordinates are mask grid pixels (a Pt names the centre of one
// cell); the caller scales them to image pixels.
// See docs/phase7-design.md §5.1 for the binding contract.
package maskgeom

import (
	"errors"
	"fmt"
)

// Pt is one vertex on the mask grid: X is the column, Y the row.
type Pt struct{ X, Y int }

// dirs is the Moore neighbourhood in clockwise order starting at north.
// Screen coordinates: Y grows downward.
var dirs = [8]Pt{
	{0, -1},  // N
	{1, -1},  // NE
	{1, 0},   // E
	{1, 1},   // SE
	{0, 1},   // S
	{-1, 1},  // SW
	{-1, 0},  // W
	{-1, -1}, // NW
}

// BBox returns the inclusive bounding box of every foreground pixel (a pixel is
// foreground when its value is greater than thresh). ok is false when the mask
// holds no foreground pixel, or when w/h do not describe the buffer.
func BBox(m []float32, w, h int, thresh float32) (minX, minY, maxX, maxY int, ok bool) {
	if _, valid := usable(m, w, h); !valid {
		return 0, 0, 0, 0, false
	}
	minX, minY = w, h
	maxX, maxY = -1, -1
	for y := 0; y < h; y++ {
		for x, v := range m[y*w : y*w+w] {
			if v <= thresh {
				continue
			}
			if x < minX {
				minX = x
			}
			if x > maxX {
				maxX = x
			}
			if y < minY {
				minY = y
			}
			if y > maxY {
				maxY = y
			}
		}
	}
	if maxX < 0 {
		return 0, 0, 0, 0, false
	}
	return minX, minY, maxX, maxY, true
}

// LargestComponent returns a copy of m holding only the largest 4-connected
// foreground component; every other pixel is set to 0. A mask without any
// foreground pixel yields an all-zero copy, and when two components tie the one
// found first in raster order wins. Values are copied through untouched, only
// the positions are filtered.
func LargestComponent(m []float32, w, h int, thresh float32) []float32 {
	out := make([]float32, len(m))
	n, valid := usable(m, w, h)
	if !valid {
		return out
	}
	copy(out, m)

	labels := make([]int32, n)
	var (
		stack     []int
		label     int32
		bestLabel int32
		bestSize  int
	)
	visit := func(q int) {
		if labels[q] == 0 && m[q] > thresh {
			labels[q] = label
			stack = append(stack, q)
		}
	}
	for i := 0; i < n; i++ {
		if labels[i] != 0 || m[i] <= thresh {
			continue
		}
		label++
		labels[i] = label
		size := 0
		stack = append(stack[:0], i)
		for len(stack) > 0 {
			p := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			size++
			x, y := p%w, p/w
			if x > 0 {
				visit(p - 1)
			}
			if x+1 < w {
				visit(p + 1)
			}
			if y > 0 {
				visit(p - w)
			}
			if y+1 < h {
				visit(p + w)
			}
		}
		if size > bestSize {
			bestSize, bestLabel = size, label
		}
	}
	for i := 0; i < n; i++ {
		if labels[i] != bestLabel {
			out[i] = 0
		}
	}
	return out
}

// TraceContour walks the outer boundary of the foreground region that contains
// the first foreground pixel in raster order, and returns its vertices in
// clockwise order with the start vertex not repeated at the end. Holes and
// other regions are ignored. A mask without any foreground pixel is an error;
// an isolated foreground pixel yields its single vertex.
//
// The walk is Moore-neighbour tracing; it stops once the start pixel is entered
// from the same direction as at the beginning (Jacob's criterion, expressed as
// a repeated (pixel, backtrack) state so thin structures cannot loop forever),
// with a step cap as a last-resort guard.
func TraceContour(m []float32, w, h int, thresh float32) ([]Pt, error) {
	n, valid := usable(m, w, h)
	if !valid {
		return nil, fmt.Errorf("maskgeom: buffer of %d values does not hold a %dx%d mask", len(m), w, h)
	}
	start, found := firstForeground(m, w, h, thresh)
	if !found {
		return nil, errors.New("maskgeom: mask has no foreground pixel to trace")
	}

	// The raster-order start pixel always has a background pixel to the west
	// (earlier rows have no foreground at all, earlier columns in its own row
	// are background), so entering the walk from the west is unambiguous.
	cur := start
	back := Pt{-1, 0}
	initBack := back
	contour := []Pt{start}

	// A (pixel, backtrack) pair fully determines the rest of the walk, so a
	// repeated pair proves the walk is cycling instead of closing.
	seen := make(map[[2]int]bool, 64)
	limit := 8*n + 16
	for step := 0; step < limit; step++ {
		next, nextBack, ok := mooreStep(m, w, h, cur, back, thresh)
		if !ok {
			break // isolated pixel: nowhere to walk to
		}
		cur, back = next, nextBack
		if cur == start && back == initBack {
			return contour, nil
		}
		state := [2]int{cur.Y*w + cur.X, dirIndex(back)}
		if seen[state] {
			break
		}
		seen[state] = true
		contour = append(contour, cur)
	}
	return contour, nil
}

// Simplify reduces an ordered outline with the Douglas-Peucker algorithm;
// epsilon is the maximum allowed deviation, in mask pixels, and outlines with
// two vertices or fewer come back unchanged. Vertices that sit exactly on the
// segment between their neighbours are dropped afterwards, which is lossless
// and keeps a traced rectangle at its four corners; the outline is treated as
// closed for that pass, as traced contours are.
func Simplify(pts []Pt, epsilon float64) []Pt {
	if len(pts) <= 2 {
		return pts
	}
	if !(epsilon > 0) {
		epsilon = 0
	}
	keep := make([]bool, len(pts))
	keep[0], keep[len(pts)-1] = true, true
	simplifyRange(pts, 0, len(pts)-1, epsilon*epsilon, keep)

	out := make([]Pt, 0, len(pts))
	for i, k := range keep {
		if k {
			out = append(out, pts[i])
		}
	}
	return dropCollinear(out)
}

// usable reports whether m can be indexed as w*h row-major pixels and returns
// that pixel count.
func usable(m []float32, w, h int) (int, bool) {
	if w <= 0 || h <= 0 || len(m) < w*h {
		return 0, false
	}
	return w * h, true
}

// fg reports whether the mask pixel at (x, y) is foreground. Pixels outside the
// grid count as background, which removes the edge cases from the walk.
func fg(m []float32, w, h, x, y int, thresh float32) bool {
	if x < 0 || y < 0 || x >= w || y >= h {
		return false
	}
	return m[y*w+x] > thresh
}

// firstForeground returns the first foreground pixel in raster order.
func firstForeground(m []float32, w, h int, thresh float32) (Pt, bool) {
	for y := 0; y < h; y++ {
		for x, v := range m[y*w : y*w+w] {
			if v > thresh {
				return Pt{x, y}, true
			}
		}
	}
	return Pt{}, false
}

// dirIndex maps a unit direction to its index in dirs. Unknown directions map
// to north; the walk only ever passes directions taken from dirs.
func dirIndex(p Pt) int {
	for i, d := range dirs {
		if d == p {
			return i
		}
	}
	return 0
}

// mooreStep advances the boundary walk by one pixel: it scans the Moore
// neighbourhood of cur clockwise starting just after the backtrack direction,
// returns the first foreground neighbour together with the backtrack direction
// for the next step (the last background neighbour examined, expressed as a
// direction relative to the new pixel).
func mooreStep(m []float32, w, h int, cur, back Pt, thresh float32) (next, nextBack Pt, ok bool) {
	bi := dirIndex(back)
	for k := 1; k <= len(dirs); k++ {
		i := (bi + k) % len(dirs)
		nx, ny := cur.X+dirs[i].X, cur.Y+dirs[i].Y
		if !fg(m, w, h, nx, ny, thresh) {
			continue
		}
		prev := dirs[(i+len(dirs)-1)%len(dirs)]
		return Pt{nx, ny}, Pt{prev.X - dirs[i].X, prev.Y - dirs[i].Y}, true
	}
	return Pt{}, Pt{}, false
}

// simplifyRange marks the vertices Douglas-Peucker keeps between first and last
// (both inclusive) by comparing squared deviations against epsilon2.
func simplifyRange(pts []Pt, first, last int, epsilon2 float64, keep []bool) {
	if last <= first+1 {
		return
	}
	a, b := pts[first], pts[last]
	maxDist, split := 0.0, first
	for i := first + 1; i < last; i++ {
		if d := pointSegDist2(pts[i], a, b); d > maxDist {
			maxDist, split = d, i
		}
	}
	if maxDist <= epsilon2 {
		return
	}
	keep[split] = true
	simplifyRange(pts, first, split, epsilon2, keep)
	simplifyRange(pts, split, last, epsilon2, keep)
}

// pointSegDist2 is the squared distance from p to the segment ab.
func pointSegDist2(p, a, b Pt) float64 {
	dx, dy := float64(b.X-a.X), float64(b.Y-a.Y)
	px, py := float64(p.X-a.X), float64(p.Y-a.Y)
	den := dx*dx + dy*dy
	if den == 0 {
		return px*px + py*py
	}
	t := (px*dx + py*dy) / den
	switch {
	case t < 0:
		t = 0
	case t > 1:
		t = 1
	}
	ex, ey := px-t*dx, py-t*dy
	return ex*ex + ey*ey
}

// dropCollinear removes vertices that lie exactly on the segment between their
// cyclic neighbours, so a traced outline densified along straight edges comes
// back as its corners only. Removing such a vertex does not change the outline.
func dropCollinear(pts []Pt) []Pt {
	for len(pts) > 2 {
		n := len(pts)
		drop := -1
		for i := 0; i < n; i++ {
			if onSegment(pts[(i+n-1)%n], pts[i], pts[(i+1)%n]) {
				drop = i
				break
			}
		}
		if drop < 0 {
			break
		}
		pts = append(pts[:drop], pts[drop+1:]...)
	}
	return pts
}

// onSegment reports whether b lies exactly on the segment ac.
func onSegment(a, b, c Pt) bool {
	if (b.X-a.X)*(c.Y-a.Y)-(b.Y-a.Y)*(c.X-a.X) != 0 {
		return false
	}
	return b.X >= min(a.X, c.X) && b.X <= max(a.X, c.X) &&
		b.Y >= min(a.Y, c.Y) && b.Y <= max(a.Y, c.Y)
}
