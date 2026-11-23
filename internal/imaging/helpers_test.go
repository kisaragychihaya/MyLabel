package imaging

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"testing"
)

// testOrientationBase builds a 6x4 image split into four quadrants with
// clearly distinguishable colours, the sample points used to tell them apart.
const (
	quadImageW = 6
	quadImageH = 4
)

type testQuad struct {
	name string // TL, TR, BL, BR: which quadrant of the *source* image
	x, y int
	c    color.RGBA
}

// testQuads sample the centre area of each source quadrant. Under any of the
// eight orientation transforms these points stay inside the matching
// destination quadrant, so they survive JPEG re-encoding with their colour.
var testQuads = []testQuad{
	{"TL", 1, 1, color.RGBA{220, 20, 20, 255}},  // red
	{"TR", 4, 1, color.RGBA{20, 200, 20, 255}},  // green
	{"BL", 1, 2, color.RGBA{20, 20, 220, 255}},  // blue
	{"BR", 4, 2, color.RGBA{220, 200, 20, 255}}, // yellow
}

// quadrantImage paints testQuad colours onto a w x h image whose quadrant
// boundaries sit at w/2 and h/2.
func quadrantImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			q := "TL"
			switch {
			case x >= w/2 && y >= h/2:
				q = "BR"
			case x >= w/2:
				q = "TR"
			case y >= h/2:
				q = "BL"
			}
			for _, tq := range testQuads {
				if tq.name == q {
					img.SetRGBA(x, y, tq.c)
				}
			}
		}
	}
	return img
}

// gradientImage returns a small image in which every pixel has a distinct
// colour, so orientation mistakes cannot hide behind symmetry.
func gradientImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(20 + x*15),
				G: uint8(15 + y*25),
				B: uint8(200 - x*5),
				A: 255,
			})
		}
	}
	return img
}

// quadPoint returns the sample point of destination quadrant name in a
// w x h image.
func quadPoint(name string, w, h int) (int, int) {
	switch name {
	case "TR":
		return w - 2, 1
	case "BL":
		return 1, h - 2
	case "BR":
		return w - 2, h - 2
	}
	return 1, 1
}

// appendU16 appends v in the given byte order.
func appendU16(b []byte, order binary.ByteOrder, v uint16) []byte {
	var buf [2]byte
	order.PutUint16(buf[:], v)
	return append(b, buf[:]...)
}

// appendU32 appends v in the given byte order.
func appendU32(b []byte, order binary.ByteOrder, v uint32) []byte {
	var buf [4]byte
	order.PutUint32(buf[:], v)
	return append(b, buf[:]...)
}

// app1Exif builds an APP1 segment whose payload is a minimal EXIF block with a
// single IFD0 Orientation entry (tag 0x0112, type SHORT).
func app1Exif(t *testing.T, orientation int, order binary.ByteOrder) []byte {
	t.Helper()

	tiff := make([]byte, 0, 26)
	if order == binary.LittleEndian {
		tiff = append(tiff, 'I', 'I')
	} else {
		tiff = append(tiff, 'M', 'M')
	}
	tiff = appendU16(tiff, order, 42)                  // TIFF magic
	tiff = appendU32(tiff, order, 8)                   // IFD0 offset
	tiff = appendU16(tiff, order, 1)                   // one entry
	tiff = appendU16(tiff, order, 0x0112)              // tag: Orientation
	tiff = appendU16(tiff, order, 3)                   // type: SHORT
	tiff = appendU32(tiff, order, 1)                   // count
	tiff = appendU16(tiff, order, uint16(orientation)) // value, inline
	tiff = append(tiff, 0, 0)                          // value field padding
	tiff = appendU32(tiff, order, 0)                   // no next IFD

	const exifHeader = "Exif\x00\x00"
	segLen := 2 + len(exifHeader) + len(tiff)
	seg := []byte{0xFF, 0xE1, byte(segLen >> 8), byte(segLen)}
	seg = append(seg, exifHeader...)
	return append(seg, tiff...)
}

// encodeJPEG encodes img at quality 95 and returns the bytes.
func encodeJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

// writeJPEG writes img as JPEG to path.
func writePlainJPEG(t *testing.T, path string, img image.Image) {
	t.Helper()
	if err := os.WriteFile(path, encodeJPEG(t, img), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// writeJPEGWithExif writes img as JPEG with an APP1 EXIF Orientation segment
// spliced in right after the SOI marker.
func writeJPEGWithExif(t *testing.T, path string, img image.Image, orientation int, order binary.ByteOrder) {
	t.Helper()
	raw := encodeJPEG(t, img)
	if len(raw) < 2 || raw[0] != 0xFF || raw[1] != 0xD8 {
		t.Fatalf("encoded JPEG does not start with SOI")
	}
	out := make([]byte, 0, len(raw)+32)
	out = append(out, raw[:2]...)
	out = append(out, app1Exif(t, orientation, order)...)
	out = append(out, raw[2:]...)
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// setExifOrientation splices an APP1 Orientation segment into an existing
// JPEG file. Re-normalized files carry no EXIF, so duplicates do not occur.
func setExifOrientation(t *testing.T, path string, orientation int) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(raw) < 2 || raw[0] != 0xFF || raw[1] != 0xD8 {
		t.Fatalf("%s is not a JPEG", path)
	}
	out := make([]byte, 0, len(raw)+32)
	out = append(out, raw[:2]...)
	out = append(out, app1Exif(t, orientation, binary.LittleEndian)...)
	out = append(out, raw[2:]...)
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// decodeJPEGFile decodes a JPEG file into an image.
func decodeJPEGFile(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	img, err := jpeg.Decode(f)
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return img
}

// rgba8 flattens a colour to 8-bit components, ignoring alpha (all test images
// are opaque).
func rgba8(c color.Color) (int, int, int) {
	r, g, b, _ := c.RGBA()
	return int(r >> 8), int(g >> 8), int(b >> 8)
}

// requireColorNear fails when got differs from want by more than tol on any
// channel.
func requireColorNear(t *testing.T, got, want color.Color, tol int, context string) {
	t.Helper()
	gr, gg, gb := rgba8(got)
	wr, wg, wb := rgba8(want)
	if abs(gr-wr) > tol || abs(gg-wg) > tol || abs(gb-wb) > tol {
		t.Fatalf("%s: got rgb(%d,%d,%d), want rgb(%d,%d,%d) ±%d", context, gr, gg, gb, wr, wg, wb, tol)
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
