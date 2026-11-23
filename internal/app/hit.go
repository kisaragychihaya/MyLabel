package app

import "math"

// HitKind classifies a hit-test result.
type HitKind int

const (
	HitNone   HitKind = iota
	HitBody           // inside an annotation's body
	HitResize         // on a resize handle; Handle is 0..7
	HitRotate         // on the rotate handle; Handle == 8
	HitVertex         // on a polygon vertex handle; Handle is the vertex index
)

// Hit is the result of a hit test.
type Hit struct {
	Ann    int // annotation index, -1 when Kind == HitNone
	Kind   HitKind
	Handle int // 0..7 resize handles, 8 = rotate handle, 0..n-1 poly vertices
}

const (
	// handleRadiusDIP is the pick radius of handles on screen.
	handleRadiusDIP = 5
	// edgeRadiusDIP is the pick radius of the selected annotation's edges.
	edgeRadiusDIP = 3
	// polyEdgeRadiusDIP is the pick radius of a selected polygon's edges: the
	// band a double-click must land in to insert a vertex (§4.3). It is wider
	// than edgeRadiusDIP because it only ever triggers that insertion, never a
	// drag.
	polyEdgeRadiusDIP = 5
)

// noHit is the empty hit result.
var noHit = Hit{Ann: -1, Kind: HitNone, Handle: -1}

// hitTestAnns tests point p (image pixels) against anns. Handles, vertices and
// edges are only tested for the annotation sel, which is the sole selection
// (a.soleSelection(), -1 = none): resize, rotate and vertex drags edit one
// annotation (§5). Annotations later in the slice are on top (drawn last).
// Priority: selected handles/vertices > selected edge > body (topmost first).
// zoom converts screen DIP thresholds to image pixels.
//
// For a selected rectangle an edge hit is reported as HitResize with the edge's
// handle index (4..7): the annotation's border band resizes by that edge,
// exactly like its midpoint handle with a smaller pick radius. A polygon has
// vertex handles only — no resize or rotate handles — and its edges are not a
// hit at all: the band is used by the double-click insertion alone, so a plain
// click on an edge selects the polygon like the rest of its body.
func hitTestAnns(anns []Ann, sel int, p Point, zoom float64) Hit {
	z := safeZoom(zoom)

	if sel >= 0 && sel < len(anns) {
		an := &anns[sel]

		if isPoly(an) {
			// 1. vertex handles of the selected polygon, nearest wins
			best, bestDist := -1, handleRadiusDIP/z
			for i := range an.Points {
				if d := distance(p, an.Points[i]); d <= bestDist {
					best, bestDist = i, d
				}
			}
			if best >= 0 {
				return Hit{Ann: sel, Kind: HitVertex, Handle: best}
			}
		} else {
			// 1. handles of the selected annotation, nearest wins
			hp := handlePoints(an, z)
			best, bestDist := -1, handleRadiusDIP/z
			for i := range hp {
				if d := distance(p, hp[i]); d <= bestDist {
					best, bestDist = i, d
				}
			}
			if best >= 0 {
				kind := HitResize
				if best == 8 {
					kind = HitRotate
				}
				return Hit{Ann: sel, Kind: kind, Handle: best}
			}

			// 2. edges of the selected annotation
			if h, d := nearestRectEdge(*an, p); d <= edgeRadiusDIP/z {
				return Hit{Ann: sel, Kind: HitResize, Handle: h}
			}
		}
	}

	// 3. body, topmost (last drawn) first
	for i := len(anns) - 1; i >= 0; i-- {
		if insideAnn(&anns[i], p) {
			return Hit{Ann: i, Kind: HitBody, Handle: -1}
		}
	}

	return noHit
}

// insideAnn reports whether p is inside the annotation, borders included: the
// rotated rectangle in local coordinates, or pointInPolygon for a polygon.
func insideAnn(an *Ann, p Point) bool {
	if isPoly(an) {
		return pointInPolygon(p, an.Points)
	}
	l := toLocal(an, p)
	const eps = 1e-9 // tolerate float round-trip error on exact borders
	return math.Abs(l.X) <= an.W/2+eps && math.Abs(l.Y) <= an.H/2+eps
}

// nearestRectEdge returns the edge handle index (4=T, 5=R, 6=B, 7=L) nearest
// to p and the distance from p to that edge segment. (Named distinctly from
// the polygon helper nearestEdge in polygon.go.)
func nearestRectEdge(an Ann, p Point) (handle int, dist float64) {
	c := corners(&an)
	handles := [4]int{4, 5, 6, 7} // TL-TR, TR-BR, BR-BL, BL-TL
	handle, dist = -1, math.Inf(1)
	for i, h := range handles {
		if d := distPointSegment(p, c[i], c[(i+1)%4]); d < dist {
			handle, dist = h, d
		}
	}
	return handle, dist
}

// distance is the Euclidean distance between two points.
func distance(a, b Point) float64 {
	return math.Hypot(a.X-b.X, a.Y-b.Y)
}
