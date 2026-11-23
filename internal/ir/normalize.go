package ir

import (
	"cmp"
	"fmt"
	"slices"
)

// Normalize canonicalizes img in place and returns warnings for every
// destructive adjustment:
//   - rotated_bbox: vertices reordered CCW around the centroid starting at
//     the leftmost (ties: topmost) point; CX/CY/RW/RH/Angle recomputed from
//     Points (opposite-edge average; Angle = direction of p0->p1); when
//     Points is missing but the derived cache is present, Points is
//     reconstructed from it
//   - polygon: rings sorted by |area| descending (shoelace), largest first
//   - all coordinates clipped to [0,Width]x[0,Height] (warning per clipped
//     annotation)
//   - bbox W/H < 0 flipped positive (warning); a zero extent cannot be
//     repaired and is reported by Validate instead
//
// Normalize is idempotent. Clipping happens before the derived rotated_bbox
// cache is recomputed, so the cache always matches the stored vertices.
func Normalize(p *Project, img *Image) (warnings []Issue) {
	if img == nil {
		return nil
	}
	// A missing size means there is nothing to clip against.
	clip := img.Width > 0 && img.Height > 0

	for i := range img.Annotations {
		a := &img.Annotations[i]
		warn := func(field, format string, args ...any) {
			warnings = append(warnings, Issue{
				File:    img.File,
				Index:   i,
				Field:   field,
				Message: fmt.Sprintf(format, args...),
			})
		}

		switch a.Kind {
		case KindRotated:
			normalizeQuad(a, i, img.Width, img.Height, clip, warn)
		case KindPolygon:
			normalizeRings(a, i, img.Width, img.Height, clip, warn)
		case KindBBox:
			normalizeBBox(a, i, img.Width, img.Height, clip, warn)
		}
	}
	return warnings
}

// normalizeQuad brings a rotated_bbox into canonical form: vertices from
// Points (authoritative) or reconstructed from the derived cache, clipped,
// canonically ordered, then the cache recomputed from the vertices.
func normalizeQuad(a *Annotation, idx, width, height int, clip bool, warn warnFunc) {
	var pts [][2]float64
	switch {
	case len(a.Points) > 0:
		pts = make([][2]float64, len(a.Points))
		copy(pts, a.Points)
	case hasQuadParams(a):
		pts = quadFromParams(a.CX, a.CY, a.RW, a.RH, a.Angle)
	default:
		return // nothing to work from; Validate reports it
	}
	if len(pts) != 4 {
		return // not a quad; leave it to Validate
	}

	if clip && clipPoints(pts, width, height) {
		warn("points", "第 %d 条标注的旋转框顶点越界，已裁剪", idx+1)
	}
	pts = orderQuad(pts)
	a.Points = pts
	a.CX, a.CY, a.RW, a.RH, a.Angle = quadParams(pts)
}

// normalizeRings clips a polygon's rings, then sorts them by |area| descending.
// Clipping comes first so the ordering is computed on the final geometry and a
// second pass cannot reorder anything.
func normalizeRings(a *Annotation, idx, width, height int, clip bool, warn warnFunc) {
	if clip {
		clipped := false
		for _, ring := range a.Rings {
			if clipPoints(ring, width, height) {
				clipped = true
			}
		}
		if clipped {
			warn("rings", "第 %d 条标注的多边形顶点越界，已裁剪", idx+1)
		}
	}
	if len(a.Rings) > 1 {
		slices.SortStableFunc(a.Rings, func(x, y [][2]float64) int {
			return cmp.Compare(ringAbsArea(y), ringAbsArea(x))
		})
	}
}

// normalizeBBox flips negative extents positive, then clips the box; W/H are
// rewritten so the clipped corner pair stays the corner pair of the box.
func normalizeBBox(a *Annotation, idx, width, height int, clip bool, warn warnFunc) {
	if a.W < 0 {
		a.X += a.W
		a.W = -a.W
		warn("w", "第 %d 条标注的宽度为负，已翻正", idx+1)
	}
	if a.H < 0 {
		a.Y += a.H
		a.H = -a.H
		warn("h", "第 %d 条标注的高度为负，已翻正", idx+1)
	}
	if !clip {
		return
	}

	x0, y0 := a.X, a.Y
	x1, y1 := a.X+a.W, a.Y+a.H
	nx0, cx0 := clampCoord(x0, width)
	ny0, cy0 := clampCoord(y0, height)
	nx1, cx1 := clampCoord(x1, width)
	ny1, cy1 := clampCoord(y1, height)
	if !cx0 && !cy0 && !cx1 && !cy1 {
		return
	}
	a.X, a.Y, a.W, a.H = nx0, ny0, nx1-nx0, ny1-ny0
	warn("x", "第 %d 条标注的 bbox 越界，已裁剪", idx+1)
}

// warnFunc records one normalization warning for the annotation at the index
// the caller passes in; the message carries the 1-based ordinal.
type warnFunc func(field, format string, args ...any)
