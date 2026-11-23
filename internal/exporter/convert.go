package exporter

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"

	"mylabel/internal/ir"
)

// Lossy geometry conversions shared by the format builders
// (docs/phase4-design.md §4). All functions are pure: inputs are never
// mutated, outputs are freshly allocated. Coordinates are absolute image
// pixels in the IR convention (y down, angles in radians, angle = direction
// of the w edge, matching ir's quadParams).

// aabbOfPoints returns the axis-aligned bounding box (x, y, w, h) of pts.
// pts must be non-empty.
func aabbOfPoints(pts [][2]float64) (x, y, w, h float64) {
	x0, y0 := pts[0][0], pts[0][1]
	x1, y1 := x0, y0
	for _, p := range pts[1:] {
		x0 = math.Min(x0, p[0])
		y0 = math.Min(y0, p[1])
		x1 = math.Max(x1, p[0])
		y1 = math.Max(y1, p[1])
	}
	return x0, y0, x1 - x0, y1 - y0
}

// aabbOfRings returns the axis-aligned bounding box over every ring's
// vertices. rings must contain at least one non-empty ring.
func aabbOfRings(rings [][][2]float64) (x, y, w, h float64) {
	started := false
	var x0, y0, x1, y1 float64
	for _, ring := range rings {
		for _, p := range ring {
			if !started {
				x0, y0, x1, y1 = p[0], p[1], p[0], p[1]
				started = true
				continue
			}
			x0 = math.Min(x0, p[0])
			y0 = math.Min(y0, p[1])
			x1 = math.Max(x1, p[0])
			y1 = math.Max(y1, p[1])
		}
	}
	return x0, y0, x1 - x0, y1 - y0
}

// clipBBox clips an axis-aligned box to [0,iw]x[0,ih]. changed reports
// whether any coordinate moved. A box that clips to w or h <= 0 is
// returned as-is with changed=true; the caller drops it (RuleDropped).
func clipBBox(x, y, w, h float64, iw, ih int) (nx, ny, nw, nh float64, changed bool) {
	nx = clampF(x, 0, float64(iw))
	ny = clampF(y, 0, float64(ih))
	x1 := clampF(x+w, 0, float64(iw))
	y1 := clampF(y+h, 0, float64(ih))
	nw, nh = x1-nx, y1-ny
	return nx, ny, nw, nh, nx != x || ny != y || nw != w || nh != h
}

// clipPoints clips every point into [0,iw]x[0,ih], returning a fresh slice
// and whether any coordinate moved.
func clipPoints(pts [][2]float64, iw, ih int) (out [][2]float64, changed bool) {
	out = make([][2]float64, len(pts))
	for i, p := range pts {
		nx := clampF(p[0], 0, float64(iw))
		ny := clampF(p[1], 0, float64(ih))
		out[i] = [2]float64{nx, ny}
		if nx != p[0] || ny != p[1] {
			changed = true
		}
	}
	return out, changed
}

// clampF clips v into [lo, hi].
func clampF(v, lo, hi float64) float64 {
	return math.Min(math.Max(v, lo), hi)
}

// convexHull returns the convex hull of pts (monotone chain, collinear
// points on edges removed), counter-clockwise, without repeating the first
// vertex at the end. Fewer than three distinct points come back unchanged.
//
// "Counter-clockwise" is ir's convention: increasing atan2 in image
// coordinates (y down), i.e. a positive shoelace sum, so the vertex order
// matches ir.orderQuad's.
func convexHull(pts [][2]float64) [][2]float64 {
	if len(pts) < 3 {
		return append([][2]float64(nil), pts...)
	}
	sorted := make([][2]float64, len(pts))
	copy(sorted, pts)
	slices.SortFunc(sorted, func(a, b [2]float64) int {
		if c := cmp.Compare(a[0], b[0]); c != 0 {
			return c
		}
		return cmp.Compare(a[1], b[1])
	})
	uniq := sorted[:1]
	for _, p := range sorted[1:] {
		if p != uniq[len(uniq)-1] {
			uniq = append(uniq, p)
		}
	}
	if len(uniq) < 3 {
		return append([][2]float64(nil), uniq...)
	}

	// Popping on cross <= 0 drops collinear points and yields the ring with a
	// positive shoelace sum under image coordinates.
	cross := func(o, a, b [2]float64) float64 {
		return (a[0]-o[0])*(b[1]-o[1]) - (a[1]-o[1])*(b[0]-o[0])
	}
	lower := make([][2]float64, 0, len(uniq))
	for _, p := range uniq {
		for len(lower) >= 2 && cross(lower[len(lower)-2], lower[len(lower)-1], p) <= 0 {
			lower = lower[:len(lower)-1]
		}
		lower = append(lower, p)
	}
	upper := make([][2]float64, 0, len(uniq))
	for i := len(uniq) - 1; i >= 0; i-- {
		p := uniq[i]
		for len(upper) >= 2 && cross(upper[len(upper)-2], upper[len(upper)-1], p) <= 0 {
			upper = upper[:len(upper)-1]
		}
		upper = append(upper, p)
	}
	hull := make([][2]float64, 0, len(lower)+len(upper)-2)
	hull = append(hull, lower[:len(lower)-1]...)
	hull = append(hull, upper[:len(upper)-1]...)
	return hull
}

// minAreaRect returns the minimum-area rotated rectangle covering pts
// (rotating calipers over the convex hull): centre, edge lengths, and the
// direction of the w edge in radians (image coordinates, y down), so that
// ir.quadFromParams(cx, cy, w, h, angle) reproduces the rectangle.
//
// The frame is canonicalised for determinism: w >= h (swapping rotates angle
// by 90°) and angle within (-pi/2, pi/2], so one rectangle has exactly one
// description. Degenerate input stays degenerate: one point gives w=h=0, a
// collinear set gives h=0 with w along the segment.
func minAreaRect(pts [][2]float64) (cx, cy, w, h, angle float64) {
	hull := convexHull(pts)
	switch len(hull) {
	case 0:
		return 0, 0, 0, 0, 0
	case 1:
		return hull[0][0], hull[0][1], 0, 0, 0
	case 2:
		dx, dy := hull[1][0]-hull[0][0], hull[1][1]-hull[0][1]
		return (hull[0][0] + hull[1][0]) / 2, (hull[0][1] + hull[1][1]) / 2,
			math.Hypot(dx, dy), 0, math.Atan2(dy, dx)
	}

	bestArea := math.Inf(1)
	var (
		bestU, bestV           [2]float64
		minU, maxU, minV, maxV float64
	)
	for i := range hull {
		a, b := hull[i], hull[(i+1)%len(hull)]
		dx, dy := b[0]-a[0], b[1]-a[1]
		l := math.Hypot(dx, dy)
		if l == 0 {
			continue
		}
		u := [2]float64{dx / l, dy / l}
		v := [2]float64{-u[1], u[0]}
		loU, hiU := math.Inf(1), math.Inf(-1)
		loV, hiV := math.Inf(1), math.Inf(-1)
		for _, p := range hull {
			pu := p[0]*u[0] + p[1]*u[1]
			pv := p[0]*v[0] + p[1]*v[1]
			loU, hiU = math.Min(loU, pu), math.Max(hiU, pu)
			loV, hiV = math.Min(loV, pv), math.Max(hiV, pv)
		}
		if area := (hiU - loU) * (hiV - loV); area < bestArea-1e-12 {
			bestArea = area
			bestU, bestV = u, v
			minU, maxU, minV, maxV = loU, hiU, loV, hiV
		}
	}
	if math.IsInf(bestArea, 1) {
		// Every hull edge had zero length: fall back to the first vertex.
		return hull[0][0], hull[0][1], 0, 0, 0
	}

	mu, mv := (minU+maxU)/2, (minV+maxV)/2
	cx = mu*bestU[0] + mv*bestV[0]
	cy = mu*bestU[1] + mv*bestV[1]
	w, h = maxU-minU, maxV-minV
	angle = math.Atan2(bestU[1], bestU[0])
	if w < h {
		w, h = h, w
		angle += math.Pi / 2
	}
	for angle <= -math.Pi/2 {
		angle += math.Pi
	}
	for angle > math.Pi/2 {
		angle -= math.Pi
	}
	return cx, cy, w, h, angle
}

// bridgeRings merges a multi-ring polygon into one self-intersecting ring
// for YOLO seg (Ultralytics merge_multi_segment style): rings sorted by
// absolute area descending, then each next ring connected to the running
// contour through the closest vertex pair as a zero-width bridge. A single
// ring is returned unchanged (same slice contents, fresh copy).
//
// The input is never modified; empty rings are ignored.
func bridgeRings(rings [][][2]float64) [][2]float64 {
	live := make([][][2]float64, 0, len(rings))
	for _, r := range rings {
		if len(r) > 0 {
			live = append(live, r)
		}
	}
	if len(live) == 0 {
		return nil
	}
	if len(live) == 1 {
		out := make([][2]float64, len(live[0]))
		copy(out, live[0])
		return out
	}

	ordered := make([][][2]float64, len(live))
	copy(ordered, live)
	slices.SortStableFunc(ordered, func(a, b [][2]float64) int {
		return cmp.Compare(ringAbsArea(b), ringAbsArea(a))
	})

	contour := make([][2]float64, len(ordered[0]))
	copy(contour, ordered[0])
	for _, ring := range ordered[1:] {
		ci, ri := 0, 0
		best := math.Inf(1)
		for i := range contour {
			for j := range ring {
				d := math.Hypot(contour[i][0]-ring[j][0], contour[i][1]-ring[j][1])
				if d < best {
					best, ci, ri = d, i, j
				}
			}
		}
		merged := make([][2]float64, 0, len(contour)+len(ring)+1)
		merged = append(merged, contour[:ci+1]...)
		for k := 0; k < len(ring); k++ {
			merged = append(merged, ring[(ri+k)%len(ring)])
		}
		merged = append(merged, ring[ri]) // close the detour back onto the bridge
		merged = append(merged, contour[ci+1:]...)
		contour = merged
	}
	return contour
}

// --- helpers shared with the format builders ---

// ringAbsArea returns the absolute shoelace area of a closed ring.
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

// quadFromParamsPts mirrors ir.quadFromParams: the rw x rh corners around the
// centre rotated by angle, so p0->p1 runs along angle.
func quadFromParamsPts(cx, cy, rw, rh, angle float64) [][2]float64 {
	cos, sin := math.Cos(angle), math.Sin(angle)
	local := [4][2]float64{
		{-rw / 2, -rh / 2},
		{rw / 2, -rh / 2},
		{rw / 2, rh / 2},
		{-rw / 2, rh / 2},
	}
	pts := make([][2]float64, 4)
	for i, p := range local {
		pts[i] = [2]float64{cx + p[0]*cos - p[1]*sin, cy + p[0]*sin + p[1]*cos}
	}
	return pts
}

// quadParamsOf mirrors ir.quadParams: centroid, opposite-edge averages, and
// the p0->p1 direction.
func quadParamsOf(pts [][2]float64) (cx, cy, w, h, angle float64) {
	if len(pts) != 4 {
		return 0, 0, 0, 0, 0
	}
	cx = (pts[0][0] + pts[1][0] + pts[2][0] + pts[3][0]) / 4
	cy = (pts[0][1] + pts[1][1] + pts[2][1] + pts[3][1]) / 4
	edge := func(i, j int) float64 {
		return math.Hypot(pts[j][0]-pts[i][0], pts[j][1]-pts[i][1])
	}
	w = (edge(0, 1) + edge(2, 3)) / 2
	h = (edge(1, 2) + edge(3, 0)) / 2
	angle = math.Atan2(pts[1][1]-pts[0][1], pts[1][0]-pts[0][0])
	return cx, cy, w, h, angle
}

// orderQuadPts mirrors ir.orderQuad's canonical vertex order: counter-
// clockwise around the centroid (increasing atan2 in image coordinates, so
// the ring runs p0->p1->p2->p3), starting at the leftmost vertex, ties broken
// by the topmost one. ir.orderQuad is unexported, hence the local copy.
func orderQuadPts(pts [][2]float64) [][2]float64 {
	out := make([][2]float64, len(pts))
	copy(out, pts)
	if len(out) == 0 {
		return out
	}
	var cx, cy float64
	for _, p := range out {
		cx += p[0]
		cy += p[1]
	}
	n := float64(len(out))
	cx, cy = cx/n, cy/n
	slices.SortStableFunc(out, func(a, b [2]float64) int {
		return cmp.Compare(math.Atan2(a[1]-cy, a[0]-cx), math.Atan2(b[1]-cy, b[0]-cx))
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

// quadOf returns the four vertices of a rotated_bbox: Points is authoritative,
// the derived cache is the fallback (ir.Normalize keeps them in sync). The
// returned slice may alias a.Points, so callers must copy before mutating.
func quadOf(a ir.Annotation) ([][2]float64, bool) {
	if len(a.Points) == 4 {
		return a.Points, true
	}
	if a.RW > 0 && a.RH > 0 {
		return quadFromParamsPts(a.CX, a.CY, a.RW, a.RH, a.Angle), true
	}
	return nil, false
}

// nonEmptyRings drops rings without vertices.
func nonEmptyRings(rings [][][2]float64) [][][2]float64 {
	out := make([][][2]float64, 0, len(rings))
	for _, r := range rings {
		if len(r) > 0 {
			out = append(out, r)
		}
	}
	return out
}

// planCategories copies the project categories with Index set to their
// position (the 0-based class id for YOLO) and returns the key->index lookup.
// Duplicate keys keep the first occurrence; a missing Name falls back to the
// key so every target format has a printable class name.
func planCategories(cats []ir.Category) ([]ir.Category, map[string]int) {
	out := make([]ir.Category, 0, len(cats))
	index := make(map[string]int, len(cats))
	for _, c := range cats {
		if _, dup := index[c.Key]; dup {
			continue
		}
		index[c.Key] = len(out)
		c.Index = len(out)
		if strings.TrimSpace(c.Name) == "" {
			if strings.TrimSpace(c.Key) != "" {
				c.Name = c.Key
			} else {
				c.Name = fmt.Sprintf("class_%d", len(out))
			}
		}
		out = append(out, c)
	}
	return out, index
}

// sortedImages returns a deterministic (File-sorted) copy of images. The
// annotation slices are shared, never mutated.
func sortedImages(images []ir.Image) []ir.Image {
	out := make([]ir.Image, len(images))
	copy(out, images)
	slices.SortStableFunc(out, func(a, b ir.Image) int {
		return strings.Compare(a.File, b.File)
	})
	return out
}

// splitOf resolves an image's split, defaulting to train.
func splitOf(splits map[string]string, file string) string {
	if splits[file] == "val" {
		return "val"
	}
	return "train"
}

// ruleMessage is the human description stored in a LossyStat sample.
func ruleMessage(rule string) string {
	switch rule {
	case RuleObbToDet:
		return "旋转框已转为其轴对齐外接框（丢方向）"
	case RuleSegToDet:
		return "多边形已转为其轴对齐外接框"
	case RuleObbToSeg:
		return "旋转框已转为四点闭合轮廓"
	case RuleDetToSeg:
		return "矩形已转为矩形四点"
	case RuleDetToObb:
		return "矩形已转为矩形四点（obb）"
	case RuleSegToObb:
		return "多边形已转为其最小外接旋转矩形（仅适合近似矩形的目标）"
	case RuleMultiRing:
		return "多环多边形已用细桥接合并为单个自交环"
	case RuleObbToBndbox:
		return "旋转框已降级为其轴对齐 bndbox（丢方向）"
	case RuleSegDropped:
		return "分割标注不随 VOC 导出（已跳过）"
	case RuleClipped:
		return "坐标越界，已裁剪到图像范围"
	}
	return rule
}
