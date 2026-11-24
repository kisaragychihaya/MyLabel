package app

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"mylabel/internal/store"
)

// carryFixture is the source image's annotation set: one rectangle and one
// polygon with attributes, so the deep-copy rules of §2 are exercised.
func carryFixture() []Ann {
	return []Ann{
		{ID: "src_rect", Cat: 1, Kind: KindRect, CX: 50, CY: 40, W: 30, H: 20},
		{ID: "src_poly", Cat: 2, Kind: KindPoly,
			Points:     []Point{{X: 1, Y: 2}, {X: 30, Y: 2}, {X: 15, Y: 28}},
			Attributes: map[string]string{"难度": "高"}},
	}
}

// TestCarryPrevImageAppendsAndSelects: Ctrl+B copies the previous image's
// annotations onto the loaded one, keeps their relative order, deep-copies
// vertices and attributes, and selects the batch it added.
func TestCarryPrevImageAppendsAndSelects(t *testing.T) {
	a := storeApp(t, "a.png", "b.png")
	seedAnns(t, a, 0, carryFixture())
	a.selImg, a.loaded = 1, 1 // looking at b.png

	if !a.carryPrevImage() {
		t.Fatal("carryPrevImage refused a valid carry")
	}
	img := &a.images[1]
	if len(img.Anns) != 2 {
		t.Fatalf("carried %d annotations, want 2", len(img.Anns))
	}
	if img.Anns[0].ID != "src_rect" || img.Anns[1].ID != "src_poly" {
		t.Errorf("carried order = %q, %q; want src_rect, src_poly", img.Anns[0].ID, img.Anns[1].ID)
	}
	if img.Anns[0].Cat != 1 || img.Anns[1].Cat != 2 {
		t.Errorf("carried categories = %d, %d; want 1, 2", img.Anns[0].Cat, img.Anns[1].Cat)
	}

	// Deep copies: mutating the source must not reach the carried batch.
	a.images[0].Anns[1].Points[0].X = 999
	a.images[0].Anns[1].Attributes["难度"] = "低"
	if img.Anns[1].Points[0].X != 1 {
		t.Errorf("carried polygon aliases the source's vertices: %+v", img.Anns[1].Points[0])
	}
	if img.Anns[1].Attributes["难度"] != "高" {
		t.Errorf("carried attributes alias the source's map: %v", img.Anns[1].Attributes)
	}

	// The batch is selected, primary on its last member.
	if a.selectionCount() != 2 || !a.isSelected(0) || !a.isSelected(1) || a.selPrimary != 1 {
		t.Errorf("selection = %v primary %d, want {0,1} primary 1", a.selSet, a.selPrimary)
	}

	// The carry is an edit: dirty, and the fresh image is labeling now.
	if !a.dirty {
		t.Error("carry should mark the document dirty")
	}
	if got := img.Status; got != store.StatusLabeling {
		t.Errorf("status = %q after carry, want labeling", got)
	}
}

// TestCarryPrevImageReissuesIDs: a carried ID that the target already has is
// replaced, so IDs stay unique within the image.
func TestCarryPrevImageReissuesIDs(t *testing.T) {
	a := storeApp(t, "a.png", "b.png")
	seedAnns(t, a, 0, carryFixture())
	a.selImg, a.loaded = 1, 1
	a.images[1].Anns = []Ann{{ID: "src_rect", Cat: 0, Kind: KindRect, CX: 5, CY: 5, W: 4, H: 4}}

	if !a.carryPrevImage() {
		t.Fatal("carryPrevImage refused")
	}
	img := &a.images[1]
	if len(img.Anns) != 3 {
		t.Fatalf("annotations = %d, want 3", len(img.Anns))
	}
	seen := map[string]bool{}
	for _, an := range img.Anns {
		if seen[an.ID] {
			t.Fatalf("duplicate ID after carry: %q", an.ID)
		}
		seen[an.ID] = true
	}
	if img.Anns[1].ID == "src_rect" {
		t.Error("colliding carried ID was not reissued")
	}
}

// TestCarryPrevImageUndoRedo: the whole batch is one undo step.
func TestCarryPrevImageUndoRedo(t *testing.T) {
	a := storeApp(t, "a.png", "b.png")
	seedAnns(t, a, 0, carryFixture())
	a.selImg, a.loaded = 1, 1

	stackBefore := len(a.undoStack)
	if !a.carryPrevImage() {
		t.Fatal("carryPrevImage refused")
	}
	if len(a.undoStack) != stackBefore+1 {
		t.Fatalf("undo stack grew by %d, want 1", len(a.undoStack)-stackBefore)
	}

	a.undo()
	if got := len(a.images[1].Anns); got != 0 {
		t.Errorf("undo left %d annotations, want 0", got)
	}
	a.redo()
	if got := len(a.images[1].Anns); got != 2 {
		t.Errorf("redo restored %d annotations, want 2", got)
	}
}

// TestCarryPrevImageRefusals: no previous image, an empty previous image, and
// a drag in progress all refuse without touching the undo stack.
func TestCarryPrevImageRefusals(t *testing.T) {
	a := storeApp(t, "a.png", "b.png")

	if a.carryPrevImage() {
		t.Error("carry on the first image should refuse")
	}
	if a.lastStatus != "没有上一张图片" {
		t.Errorf("lastStatus = %q, want 没有上一张图片", a.lastStatus)
	}

	a.selImg, a.loaded = 1, 1 // b.png; a.png has no annotations
	if a.carryPrevImage() {
		t.Error("carry from an empty previous image should refuse")
	}
	if a.lastStatus != "上一张没有标注可沿用" {
		t.Errorf("lastStatus = %q, want 上一张没有标注可沿用", a.lastStatus)
	}
	if len(a.undoStack) != 0 {
		t.Errorf("refused carries pushed %d undo commands", len(a.undoStack))
	}

	seedAnns(t, a, 0, carryFixture())
	stackBefore := len(a.undoStack)
	a.mode = ModeMove
	if a.carryPrevImage() {
		t.Error("carry during a drag should refuse")
	}
	a.mode = ModeIdle
	if len(a.undoStack) != stackBefore {
		t.Error("a refused carry must not push an undo command")
	}
}

// TestCarryKeyBinding: Ctrl+B drives the carry; plain B does nothing.
func TestCarryKeyBinding(t *testing.T) {
	a := storeApp(t, "a.png", "b.png")
	seedAnns(t, a, 0, carryFixture())
	a.selImg, a.loaded = 1, 1

	if keyWith(a, ui.KeyB, 0) {
		t.Error("plain B must not carry")
	}
	if !keyWith(a, ui.KeyB, ui.Ctrl) {
		t.Fatal("Ctrl+B should carry")
	}
	if got := len(a.images[1].Anns); got != 2 {
		t.Errorf("Ctrl+B carried %d annotations, want 2", got)
	}
}
