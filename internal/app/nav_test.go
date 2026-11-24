package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestGoImageClampsAtEnds: §1 navigation moves selImg within the list and
// reports at the edges instead of wrapping.
func TestGoImageClampsAtEnds(t *testing.T) {
	a := storeApp(t, "a.png", "b.png", "c.png")
	if a.selImg != 0 {
		t.Fatalf("selImg = %d after openDir, want 0", a.selImg)
	}

	if !a.goImage(1) || a.selImg != 1 {
		t.Fatalf("goImage(+1) = %v, selImg = %d; want true, 1", a.selImg == 1, a.selImg)
	}
	if !a.goImage(-1) || a.selImg != 0 {
		t.Fatalf("goImage(-1) back: selImg = %d, want 0", a.selImg)
	}
	if a.goImage(-1) {
		t.Error("goImage(-1) at the first image should refuse")
	}
	if a.lastStatus != "已经是第一张" {
		t.Errorf("lastStatus = %q, want 已经是第一张", a.lastStatus)
	}

	a.selImg = len(a.images) - 1
	if a.goImage(1) {
		t.Error("goImage(+1) at the last image should refuse")
	}
	if a.lastStatus != "已经是最后一张" {
		t.Errorf("lastStatus = %q, want 已经是最后一张", a.lastStatus)
	}
	if a.selImg != len(a.images)-1 {
		t.Errorf("selImg = %d after refused navigation, want %d", a.selImg, len(a.images)-1)
	}
}

// TestGoImageWithoutImages: an empty workspace has nowhere to go.
func TestGoImageWithoutImages(t *testing.T) {
	a := New()
	t.Cleanup(a.Close)
	if a.goImage(1) || a.goImage(-1) {
		t.Error("goImage without images should refuse")
	}
}

// TestGoImageBlockedDuringDrag: a gesture holds annotation snapshots by
// index; switching images under it is refused, as with Delete.
func TestGoImageBlockedDuringDrag(t *testing.T) {
	a := storeApp(t, "a.png", "b.png")
	a.mode = ModeMove
	if a.goImage(1) {
		t.Error("goImage during a drag should refuse")
	}
	if a.selImg != 0 {
		t.Errorf("selImg = %d after refused navigation, want 0", a.selImg)
	}
}

// TestNavKeysSwitchImages: A/D without modifiers move one image back/forward;
// with Ctrl held they keep their other meanings (Ctrl+A select all, Ctrl+D
// status toggle in View) and never navigate.
func TestNavKeysSwitchImages(t *testing.T) {
	a := storeApp(t, "a.png", "b.png")

	if !keyWith(a, ui.KeyD, 0) || a.selImg != 1 {
		t.Fatalf("D: handled = %v, selImg = %d; want true, 1", a.selImg == 1, a.selImg)
	}
	if !keyWith(a, ui.KeyA, 0) || a.selImg != 0 {
		t.Fatalf("A: handled = %v, selImg = %d; want true, 0", a.selImg == 0, a.selImg)
	}
	if keyWith(a, ui.KeyD, ui.Ctrl) {
		t.Error("Ctrl+D must not reach image navigation")
	}
	if a.selImg != 0 {
		t.Errorf("selImg = %d after Ctrl+D, want 0 (unchanged)", a.selImg)
	}
	if keyWith(a, ui.KeyA, ui.Shift) {
		t.Error("Shift+A must not navigate")
	}
}

// TestFitWindowKey: F re-arms the next-frame fit for a loaded image, and
// refuses when there is none.
func TestFitWindowKey(t *testing.T) {
	a := storeApp(t, "a.png")
	if !a.hasImage() {
		t.Fatal("storeApp should have the first image loaded")
	}
	a.fitOnLoad = false
	if !keyWith(a, ui.KeyF, 0) {
		t.Fatal("F should be handled for a loaded image")
	}
	if !a.fitOnLoad {
		t.Error("F should arm fitOnLoad for the next Draw")
	}

	empty := New()
	t.Cleanup(empty.Close)
	if empty.fitWindow() {
		t.Error("fitWindow without an image should refuse")
	}
}
