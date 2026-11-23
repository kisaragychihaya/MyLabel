package ir

import "fmt"

// Validate checks img against p's category table and geometry rules: polygon
// outer rings need >= 3 points, rotated_bbox exactly 4 points, category keys
// must exist, coordinates must be finite. Out-of-range coordinates are NOT
// errors here (Normalize clips them with a warning).
//
// A rotated_bbox may carry only Points or only the derived cache (Normalize
// completes either from the other); only an annotation with neither is
// reported. The input is never modified.
func Validate(p *Project, img *Image) []Issue {
	if img == nil {
		return nil
	}

	var cats map[string]bool
	if p != nil {
		cats = make(map[string]bool, len(p.Categories))
		for _, c := range p.Categories {
			cats[c.Key] = true
		}
	}

	var issues []Issue
	for i := range img.Annotations {
		a := &img.Annotations[i]
		add := func(field, format string, args ...any) {
			issues = append(issues, Issue{
				File:    img.File,
				Index:   i,
				Field:   field,
				Message: fmt.Sprintf(format, args...),
			})
		}

		switch a.Kind {
		case KindBBox:
			if a.W == 0 {
				add("w", "bbox 宽度为 0")
			}
			if a.H == 0 {
				add("h", "bbox 高度为 0")
			}
			checkFinite(add, "x", a.X, a.Y, a.W, a.H)

		case KindPolygon:
			switch {
			case len(a.Rings) == 0:
				add("rings", "多边形缺少外环")
			case len(a.Rings[0]) < 3:
				add("rings", "多边形外环只有 %d 个点，至少需要 3 个", len(a.Rings[0]))
			}
			for _, ring := range a.Rings {
				for _, pt := range ring {
					checkFinite(add, "rings", pt[0], pt[1])
				}
			}

		case KindRotated:
			switch {
			case len(a.Points) == 0 && !hasQuadParams(a):
				add("points", "旋转框既没有 points 也没有 cx/cy/rw/rh/angle 参数")
			case len(a.Points) > 0 && len(a.Points) != 4:
				add("points", "旋转框需要 4 个点，实际 %d 个", len(a.Points))
			}
			for _, pt := range a.Points {
				checkFinite(add, "points", pt[0], pt[1])
			}
			checkFinite(add, "cx", a.CX, a.CY, a.RW, a.RH, a.Angle)
		}

		if cats != nil && !cats[a.Category] {
			add("category", "类别 %q 不在项目类别表中", a.Category)
		}
	}
	return issues
}

// checkFinite reports a single issue for field when any of vals is NaN/Inf.
func checkFinite(add func(field, format string, args ...any), field string, vals ...float64) {
	for _, v := range vals {
		if !finite(v) {
			add(field, "%s 坐标不是有限数值（NaN/Inf）", field)
			return
		}
	}
}
