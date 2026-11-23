package ir

import "reflect"

// deepCopyImage clones everything Normalize/Validate could touch, so a test
// can compare before/after.
func deepCopyImage(img Image) Image {
	out := img
	out.Annotations = make([]Annotation, len(img.Annotations))
	for i, a := range img.Annotations {
		c := a
		if a.Points != nil {
			c.Points = make([][2]float64, len(a.Points))
			copy(c.Points, a.Points)
		}
		if a.Rings != nil {
			c.Rings = make([][][2]float64, len(a.Rings))
			for j, r := range a.Rings {
				c.Rings[j] = make([][2]float64, len(r))
				copy(c.Rings[j], r)
			}
		}
		if a.Attributes != nil {
			c.Attributes = make(map[string]string, len(a.Attributes))
			for k, v := range a.Attributes {
				c.Attributes[k] = v
			}
		}
		out.Annotations[i] = c
	}
	return out
}

// imagesEqual compares two images structurally.
func imagesEqual(a, b Image) bool {
	return reflect.DeepEqual(a, b)
}
