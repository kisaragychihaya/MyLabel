package imaging

import (
	"encoding/binary"
	"image"
	"image/gif"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/image/bmp"
)

// writePNG writes a w x h PNG to path.
func writePNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		t.Fatalf("encode png %s: %v", path, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close %s: %v", path, err)
	}
}

// writeGIF writes a w x h GIF to path.
func writeGIF(t *testing.T, path string, img image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	if err := gif.Encode(f, img, nil); err != nil {
		_ = f.Close()
		t.Fatalf("encode gif %s: %v", path, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close %s: %v", path, err)
	}
}

// writeBMP writes a w x h BMP to path.
func writeBMP(t *testing.T, path string, img image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	if err := bmp.Encode(f, img); err != nil {
		_ = f.Close()
		t.Fatalf("encode bmp %s: %v", path, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close %s: %v", path, err)
	}
}

// TestScanRecursive walks a nested tree and checks discovery, ordering, slash
// separators, sizes, hashes, the Rotated flag and that broken files are
// skipped instead of aborting the scan.
func TestScanRecursive(t *testing.T) {
	root := t.TempDir()
	mustMkdirAll(t, filepath.Join(root, "sub", "deeper"))
	mustMkdirAll(t, filepath.Join(root, "empty"))

	// Root level: a plain JPEG (8x6) and a PNG (5x3), plus non-images.
	writePlainJPEG(t, filepath.Join(root, "a.jpg"), gradientImage(8, 6))
	writePNG(t, filepath.Join(root, "b.PNG"), gradientImage(5, 3))
	writeFile(t, filepath.Join(root, "notes.txt"), "not an image")
	writeFile(t, filepath.Join(root, "broken.jpg"), "garbage bytes, not a jpeg")
	writeFile(t, filepath.Join(root, "empty.png"), "")

	// Subdirectory: an EXIF orientation 6 JPEG that must be rotated in place.
	exifPath := filepath.Join(root, "sub", "c.jpeg")
	writeJPEGWithExif(t, exifPath, quadrantImage(quadImageW, quadImageH), 6, binary.LittleEndian)
	// Deeper: a GIF and a BMP, to prove recursion and decoder registration.
	writeGIF(t, filepath.Join(root, "sub", "deeper", "d.gif"), gradientImage(4, 4))
	writeBMP(t, filepath.Join(root, "sub", "deeper", "e.bmp"), gradientImage(3, 2))

	got, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	wantRels := []string{"a.jpg", "b.PNG", "sub/c.jpeg", "sub/deeper/d.gif", "sub/deeper/e.bmp"}
	if len(got) != len(wantRels) {
		t.Fatalf("scan found %d images (%v), want %d", len(got), relPaths(got), len(wantRels))
	}
	for i, want := range wantRels {
		if got[i].RelPath != want {
			t.Fatalf("result[%d].RelPath = %q, want %q (sorted ascending)", i, got[i].RelPath, want)
		}
	}

	byRel := make(map[string]ScannedImage, len(got))
	for _, img := range got {
		byRel[img.RelPath] = img
		if strings.Contains(img.RelPath, `\`) {
			t.Fatalf("RelPath %q uses backslashes, want slashes", img.RelPath)
		}
		if img.Name != filepath.Base(img.RelPath) {
			t.Fatalf("%q: Name = %q, want base name", img.RelPath, img.Name)
		}
		if !filepath.IsAbs(img.AbsPath) {
			t.Fatalf("%q: AbsPath %q is not absolute", img.RelPath, img.AbsPath)
		}
		if wantAbs := filepath.Join(root, filepath.FromSlash(img.RelPath)); img.AbsPath != wantAbs {
			t.Fatalf("%q: AbsPath = %q, want %q", img.RelPath, img.AbsPath, wantAbs)
		}
		sum, err := HashFile(img.AbsPath)
		if err != nil {
			t.Fatalf("%q: HashFile: %v", img.RelPath, err)
		}
		if img.SHA256 != sum {
			t.Fatalf("%q: SHA256 = %q, want post-normalization hash %q", img.RelPath, img.SHA256, sum)
		}
		if len(img.SHA256) != 64 {
			t.Fatalf("%q: SHA256 %q is not 64 hex chars", img.RelPath, img.SHA256)
		}
	}

	checks := []struct {
		rel           string
		width, height int
		rotated       bool
	}{
		{"a.jpg", 8, 6, false},
		{"b.PNG", 5, 3, false},
		{"sub/c.jpeg", 4, 6, true}, // 6x4 rotated 90 CW: width/height swap
		{"sub/deeper/d.gif", 4, 4, false},
		{"sub/deeper/e.bmp", 3, 2, false},
	}
	for _, c := range checks {
		img, ok := byRel[c.rel]
		if !ok {
			t.Fatalf("%q missing from scan result", c.rel)
		}
		if img.Width != c.width || img.Height != c.height {
			t.Fatalf("%q: size = %dx%d, want %dx%d", c.rel, img.Width, img.Height, c.width, c.height)
		}
		if img.Rotated != c.rotated {
			t.Fatalf("%q: Rotated = %v, want %v", c.rel, img.Rotated, c.rotated)
		}
	}

	// The EXIF of the rotated file is gone, and a rescan reports Rotated=false.
	if o, err := Orientation(exifPath); err != nil || o != 1 {
		t.Fatalf("rotated file Orientation = (%d, %v), want (1, nil)", o, err)
	}
	again, err := Scan(root)
	if err != nil {
		t.Fatalf("second Scan: %v", err)
	}
	for _, img := range again {
		if img.Rotated {
			t.Fatalf("second Scan reports Rotated for %q", img.RelPath)
		}
		if img.RelPath == "sub/c.jpeg" && (img.Width != 4 || img.Height != 6) {
			t.Fatalf("second Scan size = %dx%d, want 4x6", img.Width, img.Height)
		}
	}
}

// TestScanRootErrors covers the only failure mode Scan reports.
func TestScanRootErrors(t *testing.T) {
	dir := t.TempDir()

	if _, err := Scan(filepath.Join(dir, "does-not-exist")); err == nil {
		t.Fatalf("Scan of a missing root: err = nil, want error")
	}

	file := filepath.Join(dir, "file.jpg")
	writePlainJPEG(t, file, gradientImage(4, 4))
	if _, err := Scan(file); err == nil {
		t.Fatalf("Scan of a regular file: err = nil, want error")
	}
}

// TestScanEmptyRoot documents that an empty tree yields an empty result.
func TestScanEmptyRoot(t *testing.T) {
	root := t.TempDir()
	got, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Scan found %d images in an empty tree: %v", len(got), relPaths(got))
	}
}

// TestHashFile checks sha256 hex against well-known vectors.
func TestHashFile(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"empty", "", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		{"abc", "abc", "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
		{"hello", "hello", "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"},
		{"greeting", "The quick brown fox jumps over the lazy dog", "d7a8fbb307d7809469ca9abcb0082e4f8d5651e46d3cdb762d02d0bf37c9e592"},
	}
	dir := t.TempDir()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".bin")
			writeFile(t, path, tc.content)
			got, err := HashFile(path)
			if err != nil {
				t.Fatalf("HashFile: %v", err)
			}
			if got != tc.want {
				t.Fatalf("HashFile = %q, want %q", got, tc.want)
			}
		})
	}

	if _, err := HashFile(filepath.Join(dir, "missing.bin")); err == nil {
		t.Fatalf("HashFile of a missing file: err = nil, want error")
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func relPaths(imgs []ScannedImage) []string {
	out := make([]string, len(imgs))
	for i, img := range imgs {
		out[i] = img.RelPath
	}
	return out
}
