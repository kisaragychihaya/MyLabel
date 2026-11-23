package imaging

import (
	"fmt"
	"image"
	_ "image/gif" // decodeConfigSize: format registration
	_ "image/png" // decodeConfigSize: format registration
	"os"
	"path/filepath"
	"strings"

	_ "golang.org/x/image/bmp"  // decodeConfigSize: format registration
	_ "golang.org/x/image/webp" // decodeConfigSize: format registration
)

// imageExts are the extensions Scan accepts, lowercase. image/jpeg is
// registered by imaging.go, which imports it for decoding.
var imageExts = map[string]bool{
	".jpg":  true,
	".jpeg": true,
	".png":  true,
	".webp": true,
	".bmp":  true,
	".gif":  true,
}

// isImagePath reports whether path has an extension Scan treats as an image.
func isImagePath(path string) bool {
	return imageExts[strings.ToLower(filepath.Ext(path))]
}

// isJPEGPath reports whether path should be treated as a JPEG, by extension
// only (case-insensitive): a mislabelled file is caught when decoding.
func isJPEGPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		return true
	}
	return false
}

// decodeConfigSize reads only the image header and returns its dimensions.
func decodeConfigSize(path string) (w, h int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return 0, 0, fmt.Errorf("%s: 无效尺寸 %dx%d", filepath.Base(path), cfg.Width, cfg.Height)
	}
	return cfg.Width, cfg.Height, nil
}
