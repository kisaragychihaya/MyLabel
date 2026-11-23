package imaging

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// dstQuadrant maps a source quadrant to the destination quadrant it must end
// up in, per the EXIF orientation definitions (orientation 1 = as stored).
var dstQuadrant = map[int]map[string]string{
	2: {"TL": "TR", "TR": "TL", "BL": "BR", "BR": "BL"}, // mirror horizontal
	3: {"TL": "BR", "TR": "BL", "BL": "TR", "BR": "TL"}, // rotate 180
	4: {"TL": "BL", "TR": "BR", "BL": "TL", "BR": "TR"}, // mirror vertical
	5: {"TL": "TL", "TR": "BL", "BL": "TR", "BR": "BR"}, // transpose
	6: {"TL": "TR", "TR": "BR", "BL": "TL", "BR": "BL"}, // rotate 90 CW
	7: {"TL": "BR", "TR": "TR", "BL": "BL", "BR": "TL"}, // transverse
	8: {"TL": "BL", "TR": "TL", "BL": "BR", "BR": "TR"}, // rotate 90 CCW
}

// orientedSize returns the expected width/height after baking orientation in.
func orientedSize(orientation, w, h int) (int, int) {
	if orientation >= 5 {
		return h, w
	}
	return w, h
}

// TestNormalizeOrientationDirections covers all eight EXIF orientations: each
// coloured quadrant of the 6x4 source must land in the quadrant the EXIF spec
// prescribes, the size must swap for 5..8, the EXIF must be gone afterwards and
// a second call must be a no-op.
func TestNormalizeOrientationDirections(t *testing.T) {
	for orientation := 2; orientation <= 8; orientation++ {
		t.Run(fmt.Sprintf("orientation_%d", orientation), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "src.jpg")
			writeJPEGWithExif(t, path, quadrantImage(quadImageW, quadImageH), orientation, binary.LittleEndian)

			// Reference colours: the pixels actually stored in the file, so the
			// comparison only has to absorb one JPEG re-encode.
			src := decodeJPEGFile(t, path)

			rotated, err := NormalizeOrientation(path)
			if err != nil {
				t.Fatalf("NormalizeOrientation: %v", err)
			}
			if !rotated {
				t.Fatalf("NormalizeOrientation = false, want true for orientation %d", orientation)
			}

			dst := decodeJPEGFile(t, path)
			wantW, wantH := orientedSize(orientation, quadImageW, quadImageH)
			if got := dst.Bounds().Dx(); got != wantW {
				t.Fatalf("width = %d, want %d", got, wantW)
			}
			if got := dst.Bounds().Dy(); got != wantH {
				t.Fatalf("height = %d, want %d", got, wantH)
			}

			for _, q := range testQuads {
				dx, dy := quadPoint(dstQuadrant[orientation][q.name], wantW, wantH)
				requireColorNear(t, dst.At(dx, dy), src.At(q.x, q.y), 30,
					fmt.Sprintf("orientation %d: source %s quadrant at dst (%d,%d)", orientation, q.name, dx, dy))
			}

			// The orientation is baked into the pixels: EXIF is gone for good.
			got, err := Orientation(path)
			if err != nil {
				t.Fatalf("Orientation after normalize: %v", err)
			}
			if got != 1 {
				t.Fatalf("Orientation after normalize = %d, want 1", got)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read normalized file: %v", err)
			}
			again, err := NormalizeOrientation(path)
			if err != nil {
				t.Fatalf("second NormalizeOrientation: %v", err)
			}
			if again {
				t.Fatalf("second NormalizeOrientation = true, want false")
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("re-read normalized file: %v", err)
			}
			if !bytes.Equal(before, after) {
				t.Fatalf("second NormalizeOrientation modified the file")
			}
			assertNoTempFiles(t, dir)
		})
	}
}

// TestNormalizeOrientationBigEndianExif checks the MM (big-endian) TIFF header
// path used by most cameras.
func TestNormalizeOrientationBigEndianExif(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "be.jpg")
	writeJPEGWithExif(t, path, quadrantImage(quadImageW, quadImageH), 6, binary.BigEndian)

	if got, err := Orientation(path); err != nil || got != 6 {
		t.Fatalf("Orientation = (%d, %v), want (6, nil)", got, err)
	}
	src := decodeJPEGFile(t, path)
	rotated, err := NormalizeOrientation(path)
	if err != nil {
		t.Fatalf("NormalizeOrientation: %v", err)
	}
	if !rotated {
		t.Fatalf("NormalizeOrientation = false, want true")
	}
	dst := decodeJPEGFile(t, path)
	if w, h := dst.Bounds().Dx(), dst.Bounds().Dy(); w != 4 || h != 6 {
		t.Fatalf("size = %dx%d, want 4x6", w, h)
	}
	for _, q := range testQuads {
		dx, dy := quadPoint(dstQuadrant[6][q.name], 4, 6)
		requireColorNear(t, dst.At(dx, dy), src.At(q.x, q.y), 30,
			fmt.Sprintf("big-endian orientation 6: source %s quadrant", q.name))
	}
}

// TestNormalizeOrientationRoundTrip pins the geometry semantically: applying
// the same symmetry twice, or complementary quarter turns in sequence, must
// restore the original pixels. That holds only for a correct 8-direction map.
func TestNormalizeOrientationRoundTrip(t *testing.T) {
	const w, h = 12, 8
	cases := []struct {
		name         string
		orientations []int
	}{
		{"mirror_horizontal_twice", []int{2, 2}},
		{"rotate_180_twice", []int{3, 3}},
		{"mirror_vertical_twice", []int{4, 4}},
		{"transpose_twice", []int{5, 5}},
		{"transverse_twice", []int{7, 7}},
		{"rotate_cw_then_ccw", []int{6, 8}},
		{"rotate_ccw_then_cw", []int{8, 6}},
		{"rotate_cw_four_times", []int{6, 6, 6, 6}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "rt.jpg")
			original := gradientImage(w, h)
			writePlainJPEG(t, path, original)
			base := decodeJPEGFile(t, path)

			for i, orientation := range tc.orientations {
				setExifOrientation(t, path, orientation)
				rotated, err := NormalizeOrientation(path)
				if err != nil {
					t.Fatalf("step %d (orientation %d): %v", i, orientation, err)
				}
				if !rotated {
					t.Fatalf("step %d (orientation %d): rotated = false, want true", i, orientation)
				}
			}

			got := decodeJPEGFile(t, path)
			if got.Bounds().Dx() != w || got.Bounds().Dy() != h {
				t.Fatalf("size = %dx%d, want %dx%d", got.Bounds().Dx(), got.Bounds().Dy(), w, h)
			}
			for y := 0; y < h; y++ {
				for x := 0; x < w; x++ {
					requireColorNear(t, got.At(x, y), base.At(x, y), 20,
						fmt.Sprintf("pixel (%d,%d) after %v", x, y, tc.orientations))
				}
			}
			assertNoTempFiles(t, dir)
		})
	}
}

// TestNormalizeOrientationNoOp covers every input that must be left untouched.
func TestNormalizeOrientationNoOp(t *testing.T) {
	pngImg := image.NewRGBA(image.Rect(0, 0, 5, 3))
	for i := 0; i < 5*3; i++ {
		pngImg.Pix[i*4] = uint8(i * 7)
		pngImg.Pix[i*4+1] = uint8(i * 3)
		pngImg.Pix[i*4+2] = uint8(i * 5)
		pngImg.Pix[i*4+3] = 0xFF
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, pngImg); err != nil {
		t.Fatalf("encode png: %v", err)
	}

	cases := []struct {
		name string
		file string
		// write fills the file; orientation is spliced in when > 0
		write func(t *testing.T, path string)
	}{
		{
			name: "jpeg_without_exif",
			file: "plain.jpg",
			write: func(t *testing.T, path string) {
				writePlainJPEG(t, path, gradientImage(8, 6))
			},
		},
		{
			name: "jpeg_orientation_1",
			file: "one.JPG", // uppercase extension must still be treated as JPEG
			write: func(t *testing.T, path string) {
				writeJPEGWithExif(t, path, gradientImage(8, 6), 1, binary.LittleEndian)
			},
		},
		{
			name: "jpeg_orientation_invalid_0",
			file: "zero.jpeg",
			write: func(t *testing.T, path string) {
				writeJPEGWithExif(t, path, gradientImage(8, 6), 0, binary.LittleEndian)
			},
		},
		{
			name: "jpeg_orientation_invalid_9",
			file: "nine.jpg",
			write: func(t *testing.T, path string) {
				writeJPEGWithExif(t, path, gradientImage(8, 6), 9, binary.LittleEndian)
			},
		},
		{
			name: "png",
			file: "img.PNG",
			write: func(t *testing.T, path string) {
				if err := os.WriteFile(path, pngBuf.Bytes(), 0o644); err != nil {
					t.Fatalf("write png: %v", err)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, tc.file)
			tc.write(t, path)

			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read before: %v", err)
			}
			rotated, err := NormalizeOrientation(path)
			if err != nil {
				t.Fatalf("NormalizeOrientation: %v", err)
			}
			if rotated {
				t.Fatalf("NormalizeOrientation = true, want false")
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read after: %v", err)
			}
			if !bytes.Equal(before, after) {
				t.Fatalf("file bytes changed")
			}
			if got, err := Orientation(path); err != nil || got != 1 {
				t.Fatalf("Orientation = (%d, %v), want (1, nil)", got, err)
			}
			assertNoTempFiles(t, dir)
		})
	}
}

// TestOrientation covers the reader independently of the normalizer.
func TestOrientation(t *testing.T) {
	dir := t.TempDir()

	plain := filepath.Join(dir, "plain.jpg")
	writePlainJPEG(t, plain, gradientImage(8, 6))

	withExif := filepath.Join(dir, "exif.jpg")
	writeJPEGWithExif(t, withExif, gradientImage(8, 6), 3, binary.LittleEndian)

	pngPath := filepath.Join(dir, "img.png")
	var buf bytes.Buffer
	if err := png.Encode(&buf, gradientImage(4, 4)); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	if err := os.WriteFile(pngPath, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write png: %v", err)
	}

	garbage := filepath.Join(dir, "garbage.jpg")
	if err := os.WriteFile(garbage, []byte("this is not a jpeg at all"), 0o644); err != nil {
		t.Fatalf("write garbage: %v", err)
	}

	cases := []struct {
		name    string
		path    string
		want    int
		wantErr bool
	}{
		{"jpeg_without_exif", plain, 1, false},
		{"jpeg_with_exif", withExif, 3, false},
		{"png_by_extension", pngPath, 1, false},
		{"missing_png", filepath.Join(dir, "nope.png"), 1, false},
		{"missing_jpeg", filepath.Join(dir, "nope.jpg"), 1, true},
		{"garbage_jpeg", garbage, 1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Orientation(tc.path)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Orientation(%q) err = nil, want error", tc.path)
				}
				return
			}
			if err != nil {
				t.Fatalf("Orientation(%q): %v", tc.path, err)
			}
			if got != tc.want {
				t.Fatalf("Orientation(%q) = %d, want %d", tc.path, got, tc.want)
			}
		})
	}
}

// TestExifOrientationRobustness feeds the parser structurally broken input: it
// must always answer with a usable orientation instead of panicking.
func TestExifOrientationRobustness(t *testing.T) {
	valid := encodeJPEG(t, gradientImage(8, 6))
	spliced := append([]byte{}, valid[:2]...)
	spliced = append(spliced, app1Exif(t, 6, binary.LittleEndian)...)
	spliced = append(spliced, valid[2:]...)

	cases := []struct {
		name string
		data []byte
		want int
	}{
		{"empty", nil, 1},
		{"not_jpeg", []byte("GIF89a"), 1},
		{"soi_only", []byte{0xFF, 0xD8}, 1},
		{"soi_truncated_segment", []byte{0xFF, 0xD8, 0xFF, 0xE1, 0x00}, 1},
		{"segment_length_beyond_eof", []byte{0xFF, 0xD8, 0xFF, 0xE1, 0xFF, 0xFF, 'E'}, 1},
		{"exif_header_cut_mid_tiff", spliced[:20], 1}, {"valid_spliced", spliced, 6},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := exifOrientation(tc.data); got != tc.want {
				t.Fatalf("exifOrientation = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestTiffOrientationEndianness pins the TIFF walker on both byte orders and
// rejects values outside 1..8.
func TestTiffOrientationEndianness(t *testing.T) {
	orders := []struct {
		name  string
		order binary.ByteOrder
	}{
		{"little_endian", binary.LittleEndian},
		{"big_endian", binary.BigEndian},
	}
	for _, tc := range orders {
		for orientation := 1; orientation <= 8; orientation++ {
			t.Run(fmt.Sprintf("%s_%d", tc.name, orientation), func(t *testing.T) {
				seg := app1Exif(t, orientation, tc.order)
				payload := seg[4:] // drop marker + length
				if got := exifOrientation(append([]byte{0xFF, 0xD8}, seg...)); got != orientation {
					t.Fatalf("exifOrientation = %d, want %d", got, orientation)
				}
				if got := tiffOrientation(payload[len("Exif\x00\x00"):]); got != orientation {
					t.Fatalf("tiffOrientation = %d, want %d", got, orientation)
				}
			})
		}
	}

	// Out-of-range values degrade to 1 (unrotated) rather than erroring.
	for _, bad := range []int{0, 9, 42} {
		seg := app1Exif(t, bad, binary.LittleEndian)
		if got := exifOrientation(append([]byte{0xFF, 0xD8}, seg...)); got != 1 {
			t.Fatalf("orientation %d: exifOrientation = %d, want 1", bad, got)
		}
	}

	// An APP1 that is not EXIF (e.g. XMP) must be skipped, not parsed as TIFF.
	xmp := []byte{0xFF, 0xE1, 0x00, 0x20}
	xmp = append(xmp, "http://ns.adobe.com/xap/1.0/\x00"...)
	xmp = append(xmp, make([]byte, 0x20-2-len("http://ns.adobe.com/xap/1.0/\x00"))...)
	withBoth := append([]byte{0xFF, 0xD8}, xmp...)
	withBoth = append(withBoth, app1Exif(t, 8, binary.BigEndian)...)
	if got := exifOrientation(withBoth); got != 8 {
		t.Fatalf("exifOrientation with leading XMP APP1 = %d, want 8", got)
	}
}

// TestApplyOrientationNonZeroBounds pins the pixel map on an image whose
// bounds do not start at (0,0), as drawn from a SubImage.
func TestApplyOrientationNonZeroBounds(t *testing.T) {
	full := gradientImage(8, 6)
	sub := full.SubImage(image.Rect(2, 1, 8, 6)).(*image.RGBA)
	b := sub.Bounds()
	sw, sh := b.Dx(), b.Dy()

	cases := []struct {
		orientation   int
		width, height int
		source        func(x, y int) (int, int)
	}{
		{2, sw, sh, func(x, y int) (int, int) { return b.Min.X + sw - 1 - x, b.Min.Y + y }},
		{3, sw, sh, func(x, y int) (int, int) { return b.Min.X + sw - 1 - x, b.Min.Y + sh - 1 - y }},
		{5, sh, sw, func(x, y int) (int, int) { return b.Min.X + y, b.Min.Y + x }},
		{6, sh, sw, func(x, y int) (int, int) { return b.Min.X + y, b.Min.Y + sh - 1 - x }},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("orientation_%d", tc.orientation), func(t *testing.T) {
			got := applyOrientation(sub, tc.orientation)
			if got.Bounds().Dx() != tc.width || got.Bounds().Dy() != tc.height {
				t.Fatalf("size = %dx%d, want %dx%d", got.Bounds().Dx(), got.Bounds().Dy(), tc.width, tc.height)
			}
			for y := 0; y < tc.height; y++ {
				for x := 0; x < tc.width; x++ {
					sx, sy := tc.source(x, y)
					gr, gg, gb, _ := got.At(x, y).RGBA()
					wr, wg, wb, _ := sub.At(sx, sy).RGBA()
					if gr != wr || gg != wg || gb != wb {
						t.Fatalf("pixel (%d,%d) = (%d,%d,%d), want (%d,%d,%d) from source (%d,%d)",
							x, y, gr>>8, gg>>8, gb>>8, wr>>8, wg>>8, wb>>8, sx, sy)
					}
				}
			}
		})
	}
}

// assertNoTempFiles fails when the re-encoder left scratch files behind.
func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), tmpSuffix) {
			t.Fatalf("leftover scratch file %q", e.Name())
		}
	}
}
