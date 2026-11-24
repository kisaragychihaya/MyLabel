package exporter

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"slices"
)

// VOC segmentation masks (docs/phase5-design.md §6). Pixel values are palette
// indices: 0 is background, 1..254 label the categories / object ids, and 255 is
// the PASCAL border white, which is reserved but never written.
const (
	vocMaskPaletteSize = 256
	// vocMaskMaxClass is the largest usable class value: a 0-based category
	// index i is written as i+1, so index 253 is the last one that fits.
	vocMaskMaxClass = 254
	// vocMaskMaxObjects caps the per-image seg object ids 1..255.
	vocMaskMaxObjects = 255
	vocMaskBackground = 0
	vocMaskBorder     = 255
)

// vocLabelColors is the official PASCAL VOC label colormap (the 20 colours after
// the black background), indexed here from 0 = palette entry 1.
var vocLabelColors = [20][3]uint8{
	{128, 0, 0}, {0, 128, 0}, {128, 128, 0}, {0, 0, 128}, {128, 0, 128},
	{0, 128, 128}, {128, 128, 128}, {64, 0, 0}, {192, 0, 0}, {64, 128, 0},
	{192, 128, 0}, {64, 0, 128}, {192, 0, 128}, {64, 128, 128}, {192, 128, 128},
	{0, 64, 0}, {128, 64, 0}, {0, 192, 0}, {128, 192, 0}, {0, 64, 128},
}

// vocBorderColor is the PASCAL border white written at palette entry 255.
var vocBorderColor = [3]uint8{224, 224, 192}

// newVOCPalette builds the 256-entry VOC palette: 0 opaque black, 1..254
// cycling the official label colours (PoC: beyond the 20 official labels colours
// repeat), 255 the border white.
func newVOCPalette() color.Palette {
	p := make(color.Palette, vocMaskPaletteSize)
	p[vocMaskBackground] = color.RGBA{A: 0xff}
	for i := 1; i < vocMaskBorder; i++ {
		c := vocLabelColors[(i-1)%len(vocLabelColors)]
		p[i] = color.RGBA{R: c[0], G: c[1], B: c[2], A: 0xff}
	}
	p[vocMaskBorder] = color.RGBA{
		R: vocBorderColor[0], G: vocBorderColor[1], B: vocBorderColor[2], A: 0xff,
	}
	return p
}

// newVOCMask allocates an empty palette mask for an w x h image.
func newVOCMask(w, h int) *image.Paletted {
	return image.NewPaletted(image.Rect(0, 0, w, h), newVOCPalette())
}

// vocClassValue maps a 0-based category index to its mask pixel value; 0 is
// reserved for the background.
func vocClassValue(classIdx int) int { return classIdx + 1 }

// rasterizeRing fills ring into m with the palette index v using the scanline
// even-odd rule, sampling each pixel centre (x+0.5, y+0.5). For a bridged
// multi-ring polygon that means inner rings punch holes while disjoint parts
// stay separate. Painting is clipped to the mask bounds; clipped reports whether
// any vertex fell outside them. painted is false when no pixel was touched — the
// caller drops such a seg.
//
// m is never resized and the ring is never modified.
func rasterizeRing(m *image.Paletted, ring [][2]float64, v uint8) (painted, clipped bool) {
	if len(ring) < 3 || m == nil {
		return false, false
	}
	bounds := m.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 0 || h <= 0 {
		return false, false
	}
	fw, fh := float64(w), float64(h)
	for _, p := range ring {
		if p[0] < 0 || p[0] > fw || p[1] < 0 || p[1] > fh {
			clipped = true
			break
		}
	}

	crossings := make([]float64, 0, len(ring))
	for y := 0; y < h; y++ {
		cy := float64(y) + 0.5
		crossings = crossings[:0]
		for i, p := range ring {
			q := ring[(i+1)%len(ring)]
			if (p[1] > cy) == (q[1] > cy) {
				continue
			}
			t := (cy - p[1]) / (q[1] - p[1])
			crossings = append(crossings, p[0]+t*(q[0]-p[0]))
		}
		if len(crossings) < 2 {
			continue
		}
		slices.Sort(crossings)
		for i := 0; i+1 < len(crossings); i += 2 {
			// Pixel centres strictly inside the span (x0, x1].
			startX := int(math.Ceil(crossings[i] - 0.5))
			endX := int(math.Ceil(crossings[i+1]-0.5)) - 1
			if startX < 0 {
				startX = 0
			}
			if endX > w-1 {
				endX = w - 1
			}
			for x := startX; x <= endX; x++ {
				m.SetColorIndex(x, y, v)
				painted = true
			}
		}
	}
	return painted, clipped
}

// encodeVOCMask PNG-encodes a palette mask (colour type 3, 8 bits per index).
func encodeVOCMask(m *image.Paletted) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, m); err != nil {
		return nil, fmt.Errorf("exporter: 编码掩码 PNG 失败: %w", err)
	}
	return buf.Bytes(), nil
}
