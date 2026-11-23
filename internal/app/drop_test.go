package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestDropFolderOpensIt drops a folder on the window: it becomes the current
// folder with its first image loaded.
func TestDropFolderOpensIt(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "b.png"), 40, 30)
	writePNG(t, filepath.Join(dir, "a.png"), 20, 10)

	a := New()
	t.Cleanup(a.Close) // releases mylabel.db in the temporary folder
	tt := ui.NewTester(a.View, 1280, 800)
	if !tt.DropFiles(640, 400, dir) {
		t.Fatal("drop not accepted by the window")
	}
	if a.dir != dir {
		t.Fatalf("dir = %q after dropping a folder, want %q", a.dir, dir)
	}
	if len(a.images) != 2 || a.images[0].Name != "a.png" {
		t.Fatalf("images = %+v after dropping a folder", a.images)
	}
	if a.loaded != 0 || a.bmp == nil {
		t.Errorf("loaded = %d, bmp = %v after dropping a folder", a.loaded, a.bmp != nil)
	}
}

// TestDropImageSelectsIt drops one image file: its containing folder opens
// and the dropped image is the selected one.
func TestDropImageSelectsIt(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "a.png"), 20, 10)
	writePNG(t, filepath.Join(dir, "b.png"), 40, 30)
	dropped := filepath.Join(dir, "b.png")

	a := New()
	t.Cleanup(a.Close) // releases mylabel.db in the temporary folder
	tt := ui.NewTester(a.View, 1280, 800)
	if !tt.DropFiles(640, 400, dropped) {
		t.Fatal("drop not accepted by the window")
	}
	if a.dir != dir {
		t.Fatalf("dir = %q after dropping an image, want %q", a.dir, dir)
	}
	if a.loaded != 1 || a.images[a.loaded].Name != "b.png" {
		t.Errorf("loaded = %d (%v), want 1 (b.png)", a.loaded, a.images[a.loaded].Name)
	}
	if a.bmp == nil {
		t.Error("no main image loaded after dropping an image")
	}
}

// TestDropNonImageRejected drops a non-image file: nothing opens.
func TestDropNonImageRejected(t *testing.T) {
	dir := t.TempDir()
	txt := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(txt, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := New()
	t.Cleanup(a.Close) // releases mylabel.db in the temporary folder
	tt := ui.NewTester(a.View, 1280, 800)
	tt.DropFiles(640, 400, txt)
	if a.dir != "" || len(a.images) != 0 {
		t.Errorf("dropping a .txt opened something: dir=%q images=%d", a.dir, len(a.images))
	}
}

// TestOpenFocusesCanvas: opening a folder moves keyboard focus to the canvas,
// so the category keys work without clicking the canvas first.
func TestOpenFocusesCanvas(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "a.png"), 20, 10)

	a := New()
	t.Cleanup(a.Close) // releases mylabel.db in the temporary folder
	tt := ui.NewTester(a.View, 1280, 800)
	a.openDir(dir)
	// A new folder starts with no categories, and the digit keys pick among
	// them: inject the fixture list this test's key press assumes.
	a.categories = defaultCategoryList()
	a.curCat = 0
	tt.Frame()

	tt.Key(0, ui.Key3)
	if a.curCat != 2 {
		t.Errorf("curCat = %d after pressing 3 with no click, want 2: the canvas did not have focus", a.curCat)
	}
}
