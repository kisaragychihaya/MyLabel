// Package imaging implements the image import pipeline: recursive scan,
// JPEG EXIF orientation normalization (physically re-encoding the file),
// dimension probing and content hashing. No third-party dependencies.
// See docs/phase1-design.md §4 for the binding contract.
package imaging

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/jpeg"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	// tmpSuffix names the scratch file written next to the target while
	// re-encoding; it never shares an image extension, so scans ignore it.
	tmpSuffix = ".tmp-mylabel"
	// jpegQuality is the quality used when re-encoding a normalized JPEG.
	jpegQuality = 95
	// maxExifHeader bounds how much of a file is read for EXIF parsing.
	// EXIF always sits in the first segments, so 64KB is plenty.
	maxExifHeader = 64 << 10
)

// ScannedImage describes one image file discovered by Scan.
type ScannedImage struct {
	RelPath string // relative to the scan root, slash-separated
	Name    string // base file name
	AbsPath string // absolute path
	Width   int    // pixel size after EXIF normalization
	Height  int
	SHA256  string // hex content hash after EXIF normalization
	Rotated bool   // Scan physically re-encoded this file to fix orientation
}

// Scan recursively walks root for .jpg/.jpeg/.png/.webp/.bmp/.gif files
// (case-insensitive) and returns them ordered by RelPath.
//
// Per file: JPEGs go through NormalizeOrientation first, then dimensions
// and the sha256 are read. Files that fail any step are skipped silently;
// only an unreadable root produces an error.
func Scan(root string) ([]ScannedImage, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	// Fail early and explicitly on an unreadable root: WalkDir would otherwise
	// surface the same problem only through the callback.
	if _, err := os.ReadDir(absRoot); err != nil {
		return nil, err
	}

	out := make([]ScannedImage, 0, 64)
	err = filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// A subtree that cannot be read is not fatal; the root is.
			if path == absRoot {
				return err
			}
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() || !isImagePath(path) {
			return nil
		}
		img, ok := probeImage(absRoot, path)
		if !ok {
			return nil
		}
		out = append(out, img)
		return nil
	})
	if err != nil {
		return nil, err
	}

	slices.SortFunc(out, func(a, b ScannedImage) int {
		return strings.Compare(a.RelPath, b.RelPath)
	})
	return out, nil
}

// probeImage runs the per-file pipeline (orientation, size, hash) and reports
// whether the file made it through all three steps.
func probeImage(root, path string) (ScannedImage, bool) {
	rotated, err := NormalizeOrientation(path)
	if err != nil {
		return ScannedImage{}, false
	}
	width, height, err := decodeConfigSize(path)
	if err != nil {
		return ScannedImage{}, false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return ScannedImage{}, false
	}
	sum, err := HashFile(path)
	if err != nil {
		return ScannedImage{}, false
	}
	return ScannedImage{
		RelPath: filepath.ToSlash(rel),
		Name:    filepath.Base(path),
		AbsPath: path,
		Width:   width,
		Height:  height,
		SHA256:  sum,
		Rotated: rotated,
	}, true
}

// NormalizeOrientation reads the JPEG EXIF Orientation (tag 0x0112):
//   - 1, missing or invalid: returns (false, nil), file untouched;
//   - 2..8: decodes, flips/rotates the pixels accordingly, re-encodes as
//     JPEG quality 95 and atomically replaces the file (tmp + rename).
//     The re-encoded file carries no EXIF, so the orientation is baked in.
//
// Non-JPEG files return (false, nil) immediately.
func NormalizeOrientation(path string) (rotated bool, err error) {
	if !isJPEGPath(path) {
		return false, nil
	}
	orientation, err := Orientation(path)
	if err != nil {
		return false, err
	}
	if orientation < 2 || orientation > 8 {
		return false, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	src, decodeErr := jpeg.Decode(f)
	closeErr := f.Close()
	if decodeErr != nil {
		return false, decodeErr
	}
	if closeErr != nil {
		return false, closeErr
	}

	tmp := path + tmpSuffix
	if err := writeJPEG(tmp, applyOrientation(src, orientation)); err != nil {
		_ = os.Remove(tmp)
		return false, err
	}

	// Windows cannot rename over an existing file, so the target goes first;
	// same trade-off as internal/app/persist.go (docs/phase1-design.md §4.2).
	if err := os.Remove(path); err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	return true, nil
}

// writeJPEG encodes img as quality-95 JPEG into path.
func writeJPEG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// Orientation returns the EXIF Orientation of a JPEG file. Missing,
// invalid, or non-JPEG input yields (1, nil).
func Orientation(path string) (int, error) {
	if !isJPEGPath(path) {
		return 1, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return 1, err
	}
	defer f.Close()

	buf := make([]byte, maxExifHeader)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return 1, err
	}
	return exifOrientation(buf[:n]), nil
}

// HashFile returns the lowercase hex sha256 of the file content.
func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
