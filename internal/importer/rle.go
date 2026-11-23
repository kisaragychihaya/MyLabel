package importer

import (
	"errors"
	"fmt"
)

// cocoRLEMask is a COCO run-length-encoded binary mask. Counts are
// alternating zero-run / one-run lengths starting with a zero run, in
// column-major order (pixel index = column*Height + row) — the layout
// pycocotools uses.
type cocoRLEMask struct {
	Counts []int64
	Height int
	Width  int

	// Inclusive bounding box of the set pixels, in image pixels.
	MinX, MinY, MaxX, MaxY int
	// HasPixels reports whether the mask sets at least one pixel.
	HasPixels bool
}

var (
	errRLETruncated = errors.New("rle: truncated compressed counts string")
	errRLEToolong   = errors.New("rle: compressed count value too long")
)

// decodeRLEString decodes the compressed COCO counts string written by
// pycocotools rleToString. Every character carries 6 bits offset by ascii '0'
// (48): 5 data bits plus continuation bit 0x20, chunks little-endian base 32.
// The terminating chunk of a value is sign extended from its bit 0x10, and
// every count from the 4th on stores the delta against the count two
// positions earlier (pycocotools "cnts[i] -= cnts[i-2]" compression).
func decodeRLEString(s string) ([]int64, error) {
	counts := make([]int64, 0, len(s))
	for p := 0; p < len(s); {
		var x int64
		var k uint
		for {
			if p >= len(s) {
				return nil, errRLETruncated
			}
			if k > 11 {
				return nil, errRLEToolong
			}
			c := s[p] - '0'
			if c > 0x3f {
				return nil, fmt.Errorf("rle: invalid compressed counts byte %q", s[p])
			}
			x |= int64(c&0x1f) << (5 * k)
			p++
			k++
			if c&0x20 == 0 {
				if c&0x10 != 0 {
					x |= -1 << (5 * k)
				}
				break
			}
		}
		if len(counts) >= 3 {
			x += counts[len(counts)-2]
		}
		counts = append(counts, x)
	}
	return counts, nil
}

// newCOCOMask validates COCO run lengths against a height x width mask and
// computes the bounding box of the set pixels. Runs that overrun the mask are
// clipped rather than rejected.
func newCOCOMask(counts []int64, height, width int) (*cocoRLEMask, error) {
	if height <= 0 || width <= 0 {
		return nil, fmt.Errorf("rle: invalid mask size %dx%d", height, width)
	}
	total := int64(height) * int64(width)
	if total <= 0 {
		return nil, fmt.Errorf("rle: mask size %dx%d overflows", height, width)
	}
	m := &cocoRLEMask{
		Counts: counts,
		Height: height,
		Width:  width,
		MinX:   width,
		MinY:   height,
		MaxX:   -1,
		MaxY:   -1,
	}
	var pos int64
	for i, n := range counts {
		if n < 0 {
			return nil, fmt.Errorf("rle: negative run length %d at index %d", n, i)
		}
		end := min(pos+n, total)
		if i%2 == 1 && pos < end {
			// One-run covering the linear range [pos, end).
			h := int64(height)
			startCol, startRow := pos/h, pos%h
			lastCol, lastRow := (end-1)/h, (end-1)%h
			minRow, maxRow := startRow, lastRow
			if lastCol > startCol {
				// Columns after the first one start at row 0, before the
				// last one they end at the final row.
				minRow, maxRow = 0, h-1
			}
			m.MinX = min(m.MinX, int(startCol))
			m.MaxX = max(m.MaxX, int(lastCol))
			m.MinY = min(m.MinY, int(minRow))
			m.MaxY = max(m.MaxY, int(maxRow))
		}
		pos = end
	}
	m.HasPixels = m.MaxX >= m.MinX && m.MaxY >= m.MinY
	return m, nil
}
