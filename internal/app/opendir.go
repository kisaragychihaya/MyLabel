package app

import (
	"os"
	"path/filepath"
	"strings"
)

// yoloImagesDir and yoloLabelsDir are the two sibling directories of an
// Ultralytics dataset root (coco8/images/train, coco8/labels/train.txt, ...).
const (
	yoloImagesDir = "images"
	yoloLabelsDir = "labels"
)

// resolveOpenDir widens an Ultralytics dataset subfolder to the dataset root:
// X/images/train and X/images both open as X, so train and val images end up in
// one workspace and the importer can match every split against its labels. All
// names are compared case-insensitively. Anything else (and any path that is
// not an existing directory) is returned as given; the widening is never
// applied twice, so X/images/train stops at X.
func resolveOpenDir(dir string) string {
	clean := filepath.Clean(dir)
	if st, err := os.Stat(clean); err != nil || !st.IsDir() {
		return dir
	}

	if strings.EqualFold(filepath.Base(clean), yoloImagesDir) {
		// X/images → X, provided X holds the labels directory.
		if parent := filepath.Dir(clean); hasChildDir(parent, yoloLabelsDir) {
			return parent
		}
	}
	if strings.EqualFold(filepath.Base(filepath.Dir(clean)), yoloImagesDir) {
		// X/images/train → X, provided X holds the labels directory.
		if grand := filepath.Dir(filepath.Dir(clean)); hasChildDir(grand, yoloLabelsDir) {
			return grand
		}
	}
	return dir
}

// hasChildDir reports whether parent has a subdirectory whose name equals name,
// ignoring case. The literal spelling is stat'ed first, which is the only probe
// needed for a normally named dataset; the directory listing is the fallback
// that makes LABELS/Labels work too. The contents of that subdirectory are
// never read.
func hasChildDir(parent, name string) bool {
	literal := filepath.Join(parent, name)
	if st, err := os.Stat(literal); err == nil && st.IsDir() {
		return true
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !strings.EqualFold(e.Name(), name) {
			continue
		}
		if st, err := os.Stat(filepath.Join(parent, e.Name())); err == nil && st.IsDir() {
			return true
		}
	}
	return false
}
