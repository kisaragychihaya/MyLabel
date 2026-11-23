package app

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
)

// handleDroppedFiles opens whatever the user dropped on the window this
// frame: a folder opens as the current folder; image files open their
// containing folder with the first dropped image selected. Reading
// DroppedFiles also makes the element accept dragged files, so it must be
// called every frame.
func (a *App) handleDroppedFiles(root ui.Element) {
	paths := root.DroppedFiles()
	if len(paths) == 0 {
		return
	}

	// A dropped folder wins over any files dropped with it.
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			a.openDir(p)
			return
		}
	}

	var images []string
	for _, p := range paths {
		if isImagePath(p) {
			images = append(images, p)
		}
	}
	if len(images) == 0 {
		a.lastStatus = "拖入的内容不是图片或文件夹"
		return
	}

	a.openDir(filepath.Dir(images[0]))

	// Select the first dropped image in the freshly opened folder.
	want := filepath.Clean(images[0])
	for i := range a.images {
		if filepath.Clean(a.images[i].Path) == want {
			a.selImg = i
			a.loadImage(i)
			a.loaded = i
			a.clearSelection()
			a.grid.ScrollIntoView(i)
			return
		}
	}
}

// isImagePath reports whether path looks like an image file: the extensions
// internal/imaging.Scan accepts, case-insensitively. It only tells dropped
// files from dropped folders; the scan decides afterwards what is decodable.
func isImagePath(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".jpg", ".jpeg", ".png", ".webp", ".bmp", ".gif":
		return true
	}
	return false
}
