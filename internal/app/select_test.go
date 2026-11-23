package app

import (
	"image/color"
	"math"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

// Tests for the multi-selection chain: Ctrl+click toggling, marquee selection,
// Ctrl+A, batch delete / re-categorize / group move as one undo step each, the
// sole-selection rule for handles and vertex edits, and the selection reset on
// image switch. docs/phase2-design.md §5, §8 item 3.

// rectAnn is an axis-aligned w×h rectangle centered at (cx, cy).
func rectAnn(id string, cat int, cx, cy, w, h float64) Ann {
	return Ann{ID: id, Cat: cat, Kind: KindRect, CX: cx, CY: cy, W: w, H: h}
}

// wantSelection checks the selected indices, ascending, and the primary index.
func wantSelection(t *testing.T, a *App, want ...int) {
	t.Helper()
	got := a.selectedIndices()
	if len(got) != len(want) {
		t.Fatalf("selection = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("selection = %v, want %v", got, want)
		}
	}
	if len(want) == 0 {
		if a.selPrimary != -1 {
			t.Fatalf("selPrimary = %d with an empty selection, want -1", a.selPrimary)
		}
		return
	}
	if !a.isSelected(a.selPrimary) {
		t.Fatalf("selPrimary = %d is not selected (%v)", a.selPrimary, got)
	}
}

// clickWith clicks image point p once, with mods (polyDown carries the
// modifiers the Tester cannot set).
func clickWith(a *App, p Point, mods ui.Modifiers) {
	a.handleCanvasInput(ui.Element{}, polyDown(a, p, 1, mods))
	a.handleCanvasInput(ui.Element{}, polyUp(a, p))
}

// dragWith presses at from, moves through the midpoint to to and releases
// there, carrying mods on every event.
func dragWith(a *App, from, to Point, mods ui.Modifiers) {
	a.handleCanvasInput(ui.Element{}, polyDown(a, from, 1, mods))
	a.handleCanvasInput(ui.Element{}, polyMove(a, Point{X: (from.X + to.X) / 2, Y: (from.Y + to.Y) / 2}, mods))
	a.handleCanvasInput(ui.Element{}, polyMove(a, to, mods))
	a.handleCanvasInput(ui.Element{}, polyUp(a, to))
}

// keyWith presses a key with modifiers held.
func keyWith(a *App, key ui.Key, mods ui.Modifiers) bool {
	return a.handleCanvasInput(ui.Element{}, ui.InputEvent{Kind: ui.InputKeyDown, Key: key, Mods: mods})
}

// threeRects returns an App whose image holds three separated 100×100 boxes at
// x = 100, 300, 500 (y = 100), plus a fourth far away at (500, 400).
func threeRects(t *testing.T) *App {
	t.Helper()
	a := newTestApp()
	a.images[0].Anns = []Ann{
		rectAnn("a", 0, 100, 100, 100, 100),
		rectAnn("b", 1, 300, 100, 100, 100),
		rectAnn("c", 2, 500, 100, 100, 100),
		rectAnn("d", 3, 500, 400, 100, 100),
	}
	return a
}

// --- Ctrl+click ------------------------------------------------------------

func TestCtrlClickTogglesSelection(t *testing.T) {
	a := threeRects(t)

	// A plain click selects one annotation and nobody else.
	clickWith(a, Point{100, 100}, 0)
	wantSelection(t, a, 0)
	if a.soleSelection() != 0 {
		t.Fatalf("soleSelection = %d, want 0", a.soleSelection())
	}

	// Ctrl+click adds, and the new one becomes primary.
	clickWith(a, Point{300, 100}, ui.Ctrl)
	wantSelection(t, a, 0, 1)
	if a.selPrimary != 1 {
		t.Fatalf("selPrimary = %d after Ctrl+click, want 1", a.selPrimary)
	}

	clickWith(a, Point{500, 100}, ui.Ctrl)
	wantSelection(t, a, 0, 1, 2)

	// Ctrl+click on the primary removes it; the primary hands over to the
	// lowest remaining index.
	clickWith(a, Point{500, 100}, ui.Ctrl)
	wantSelection(t, a, 0, 1)
	if a.selPrimary != 0 {
		t.Fatalf("selPrimary = %d after untoggling the primary, want 0", a.selPrimary)
	}

	clickWith(a, Point{100, 100}, ui.Ctrl)
	wantSelection(t, a, 1)
	if a.selPrimary != 1 {
		t.Fatalf("selPrimary = %d, want 1", a.selPrimary)
	}

	clickWith(a, Point{300, 100}, ui.Ctrl)
	wantSelection(t, a)

	// Toggling never drags: nothing moved and nothing was pushed on the stack.
	if !sameAnn(anns(a)[0], rectAnn("a", 0, 100, 100, 100, 100)) {
		t.Errorf("Ctrl+click moved annotation a: %+v", anns(a)[0])
	}
	if a.mode != ModeIdle {
		t.Errorf("mode = %v after Ctrl+click, want idle", a.mode)
	}
	if len(a.undoStack) != 0 {
		t.Errorf("undo stack = %d after Ctrl+clicks, want 0", len(a.undoStack))
	}
}

// TestClickEmptyCanvasClearsSelection checks the plain-click-on-empty rule.
func TestClickEmptyCanvasClearsSelection(t *testing.T) {
	a := threeRects(t)
	a.selectAll()
	wantSelection(t, a, 0, 1, 2, 3)

	clickWith(a, Point{750, 560}, 0)
	wantSelection(t, a)
	if n := len(anns(a)); n != 4 {
		t.Fatalf("annotations = %d after a click on empty canvas, want 4", n)
	}
}

// TestEscapeCancelsDragThenClearsSelection checks the order §5 asks for: the
// gesture in progress is cancelled first, the selection only after that.
func TestEscapeCancelsDragThenClearsSelection(t *testing.T) {
	a := threeRects(t)
	a.selectAll()
	before := cloneAnn(anns(a)[0])

	// A group drag in progress: Escape reverts it and keeps the selection.
	a.handleCanvasInput(ui.Element{}, polyDown(a, Point{100, 100}, 1, 0))
	a.handleCanvasInput(ui.Element{}, polyMove(a, Point{140, 130}, 0))
	a.handleCanvasInput(ui.Element{}, polyKey(ui.KeyEscape))
	if !sameAnn(anns(a)[0], before) {
		t.Fatalf("after Escape = %+v, want the pre-drag %+v", anns(a)[0], before)
	}
	if a.selectionCount() != 4 {
		t.Fatalf("selection = %d after cancelling a drag, want 4 (Escape cleared it too early)", a.selectionCount())
	}

	// Nothing in progress any more: Escape clears the selection.
	a.handleCanvasInput(ui.Element{}, polyKey(ui.KeyEscape))
	wantSelection(t, a)
}

// --- marquee ---------------------------------------------------------------

func TestMarqueeSelectsIntersectingAnns(t *testing.T) {
	a := threeRects(t)
	a.selectOnly(3) // a marquee replaces the selection, it does not add to it

	// Around the first box only.
	dragWith(a, Point{40, 40}, Point{160, 160}, ui.Shift)
	wantSelection(t, a, 0)

	// Touching two boxes partially selects both: the test is an intersection,
	// not containment. The drag starts above the boxes, on empty canvas.
	dragWith(a, Point{120, 30}, Point{280, 140}, ui.Shift)
	wantSelection(t, a, 0, 1)

	// A marquee over bare canvas clears the selection and creates nothing.
	dragWith(a, Point{600, 500}, Point{700, 550}, ui.Shift)
	wantSelection(t, a)
	if n := len(anns(a)); n != 4 {
		t.Fatalf("annotations = %d after a marquee, want 4 (no creation)", n)
	}
	if a.mode != ModeIdle {
		t.Errorf("mode = %v after the marquee release, want idle", a.mode)
	}
	if len(a.undoStack) != 0 {
		t.Errorf("undo stack = %d after marquees, want 0", len(a.undoStack))
	}
}

// TestMarqueeUsesPolygonBoundingBox checks §5's rule that a polygon is
// selected by its vertices' bounding box, not by its filled body.
func TestMarqueeUsesPolygonBoundingBox(t *testing.T) {
	a := newTestApp()
	diamond := newPolyAnn("p", 0, []Point{{200, 100}, {300, 200}, {200, 300}, {100, 200}})
	a.images[0].Anns = []Ann{diamond}

	// The premise: the bounding box's top-left corner is outside the diamond.
	if insideAnn(&diamond, Point{120, 120}) {
		t.Fatal("test premise broken: (120,120) is inside the diamond")
	}

	dragWith(a, Point{100, 100}, Point{140, 140}, ui.Shift)
	wantSelection(t, a, 0)
}

// TestMarqueeRubberBandIsDrawn checks that the marquee paints inside the single
// canvas Draw callback (§5): the accent box must be visible while dragging.
func TestMarqueeRubberBandIsDrawn(t *testing.T) {
	a, tt := tester(t)
	a.bmp = markerImage()
	a.zoom, a.panX, a.panY = 1, 0, 0
	a.images[0].Anns = []Ann{rectAnn("a", 0, 400, 400, 100, 100)}
	cv := canvasBounds(t, tt)
	at := func(p Point) color.RGBA {
		x, y := screenOf(cv, a, p)
		return tt.Image().RGBAAt(int(math.Round(float64(x))), int(math.Round(float64(y))))
	}

	// Press and drag, without releasing: the marquee is on screen now.
	a.handleCanvasInput(ui.Element{}, polyDown(a, Point{200, 200}, 1, ui.Shift))
	a.handleCanvasInput(ui.Element{}, polyMove(a, Point{320, 320}, ui.Shift))
	tt.Frame()

	edge := at(Point{260, 200}) // on the marquee's top edge
	if blackish(edge) || whiteish(edge) {
		t.Errorf("marquee edge = %v, want the accent color over the black image", edge)
	}
	// Its interior is a translucent wash over the black image: lighter than the
	// untouched image beside it, darker than the edge.
	inner := at(Point{260, 260})
	if inner == at(Point{600, 500}) {
		t.Errorf("marquee interior = %v, want a translucent accent fill", inner)
	}

	a.handleCanvasInput(ui.Element{}, polyUp(a, Point{320, 320}))
}

// --- Ctrl+A ----------------------------------------------------------------

func TestCtrlASelectsEveryAnnotation(t *testing.T) {
	a := newTestApp()
	a.images[0].Anns = []Ann{
		rectAnn("a", 0, 100, 100, 100, 100),
		squarePolyAnn("p", 1),
		rectAnn("c", 2, 500, 400, 100, 100),
	}
	if !keyWith(a, ui.KeyA, ui.Ctrl) {
		t.Fatal("Ctrl+A not handled")
	}
	wantSelection(t, a, 0, 1, 2)
	if a.soleSelection() != -1 {
		t.Fatalf("soleSelection = %d with three selected, want -1", a.soleSelection())
	}
	if a.handlesAnn() != nil {
		t.Fatal("handles offered for a three-annotation selection")
	}

	// Ctrl+A on an empty image selects nothing rather than failing.
	a.images[0].Anns = nil
	if !keyWith(a, ui.KeyA, ui.Ctrl) {
		t.Fatal("Ctrl+A not handled on an empty image")
	}
	wantSelection(t, a)
}

// --- batch delete ----------------------------------------------------------

func TestBatchDeleteIsOneUndoStep(t *testing.T) {
	a := threeRects(t)
	before := make([]Ann, 4)
	for i := range before {
		before[i] = cloneAnn(anns(a)[i])
	}

	a.selectOnly(0)
	a.selectToggle(2)
	a.selectToggle(3)
	wantSelection(t, a, 0, 2, 3)

	a.handleCanvasInput(ui.Element{}, polyKey(ui.KeyDelete))
	if n := len(anns(a)); n != 1 {
		t.Fatalf("annotations = %d after Delete, want 1", n)
	}
	if !sameAnn(anns(a)[0], before[1]) {
		t.Fatalf("survivor = %+v, want %+v", anns(a)[0], before[1])
	}
	if len(a.undoStack) != 1 {
		t.Fatalf("undo stack = %d after a three-annotation delete, want 1 (one batch command)", len(a.undoStack))
	}
	wantSelection(t, a)

	// One undo restores the whole set, in order.
	a.undo()
	if n := len(anns(a)); n != 4 {
		t.Fatalf("annotations = %d after undo, want 4", n)
	}
	for i := range before {
		if !sameAnn(anns(a)[i], before[i]) {
			t.Fatalf("undo restored %d = %+v, want %+v", i, anns(a)[i], before[i])
		}
	}

	// ... and one redo removes it again.
	a.redo()
	if n := len(anns(a)); n != 1 {
		t.Fatalf("annotations = %d after redo, want 1", n)
	}
}

// TestDeleteSingleAnnIsStillOneCommand keeps the single-selection path on
// cmdDelete, whose name and behaviour the earlier tests rely on.
func TestDeleteSingleAnnIsStillOneCommand(t *testing.T) {
	a := threeRects(t)
	a.selectOnly(1)
	a.handleCanvasInput(ui.Element{}, polyKey(ui.KeyDelete))
	if n := len(anns(a)); n != 3 {
		t.Fatalf("annotations = %d after Delete, want 3", n)
	}
	if len(a.undoStack) != 1 || a.undoStack[0].Name != "delete" {
		t.Fatalf("undo stack = %v, want one %q command", a.undoStack, "delete")
	}
	a.undo()
	if n := len(anns(a)); n != 4 {
		t.Fatalf("annotations = %d after undo, want 4", n)
	}
}

// TestDeleteDuringGroupDragIsIgnored checks the guard that keeps a drag's
// index-based snapshots valid: deleting (or re-categorizing) while the mouse
// is still down would make them name other annotations.
func TestDeleteDuringGroupDragIsIgnored(t *testing.T) {
	a := threeRects(t)
	a.selectOnly(0)
	a.selectToggle(1)

	a.handleCanvasInput(ui.Element{}, polyDown(a, Point{100, 100}, 1, 0))
	a.handleCanvasInput(ui.Element{}, polyMove(a, Point{120, 110}, 0))
	if a.handleCanvasInput(ui.Element{}, polyKey(ui.KeyDelete)) {
		t.Error("Delete was handled during a drag")
	}
	if n := len(anns(a)); n != 4 {
		t.Fatalf("annotations = %d after Delete during a drag, want 4", n)
	}
	if a.handleCanvasInput(ui.Element{}, polyKey(ui.Key3)) {
		t.Error("a category key was handled during a drag")
	}
	if anns(a)[0].Cat != 0 {
		t.Fatalf("category = %d after a digit during a drag, want 0", anns(a)[0].Cat)
	}

	a.handleCanvasInput(ui.Element{}, polyUp(a, Point{120, 110}))
	if n := len(anns(a)); n != 4 {
		t.Fatalf("annotations = %d after the drag, want 4", n)
	}
	if len(a.undoStack) != 1 {
		t.Fatalf("undo stack = %d, want 1 (the group move)", len(a.undoStack))
	}
}

// --- batch category --------------------------------------------------------

func TestBatchCategoryIsOneUndoStep(t *testing.T) {
	a := threeRects(t)
	a.selectOnly(0)
	a.selectToggle(2)
	wantSelection(t, a, 0, 2)

	a.handleCanvasInput(ui.Element{}, polyKey(ui.Key3)) // category index 2
	if anns(a)[0].Cat != 2 || anns(a)[2].Cat != 2 {
		t.Fatalf("categories = %d,%d, want 2,2", anns(a)[0].Cat, anns(a)[2].Cat)
	}
	if anns(a)[1].Cat != 1 || anns(a)[3].Cat != 3 {
		t.Fatalf("unselected categories changed: %d,%d", anns(a)[1].Cat, anns(a)[3].Cat)
	}
	if len(a.undoStack) != 1 {
		t.Fatalf("undo stack = %d for a two-annotation recategorization, want 1", len(a.undoStack))
	}

	a.undo()
	if anns(a)[0].Cat != 0 || anns(a)[2].Cat != 2 {
		t.Fatalf("after undo categories = %d,%d, want 0,2", anns(a)[0].Cat, anns(a)[2].Cat)
	}

	// Nothing selected: the digit picks the category for the next annotation.
	a.clearSelection()
	a.handleCanvasInput(ui.Element{}, polyKey(ui.Key5))
	if a.curCat != 4 {
		t.Fatalf("curCat = %d after a digit with no selection, want 4", a.curCat)
	}
}

// --- group move ------------------------------------------------------------

func TestGroupMoveDragsWholeSelection(t *testing.T) {
	a := newTestApp()
	a.images[0].Anns = []Ann{
		rectAnn("r", 0, 100, 100, 100, 100), // 50..150
		squarePolyAnn("p", 1),               // 100..300 × 100..250
	}
	a.selectOnly(0)
	a.selectToggle(1)
	wantSelection(t, a, 0, 1)
	beforeRect := cloneAnn(anns(a)[0])
	beforePoly := cloneAnn(anns(a)[1])

	// Drag from inside the rectangle by (+40, +30): the selected polygon moves
	// with it (§5).
	dragWith(a, Point{70, 70}, Point{110, 100}, 0)

	if !nearly(anns(a)[0].CX, beforeRect.CX+40, 1e-6) || !nearly(anns(a)[0].CY, beforeRect.CY+30, 1e-6) {
		t.Fatalf("rectangle center = (%v,%v), want (%v,%v)",
			anns(a)[0].CX, anns(a)[0].CY, beforeRect.CX+40, beforeRect.CY+30)
	}
	wantPts := translatePolygon(beforePoly.Points, Point{X: 40, Y: 30})
	polyWantPointsNear(t, "moved polygon", anns(a)[1], wantPts, 1e-6)
	wantSelection(t, a, 0, 1)

	if len(a.undoStack) != 1 {
		t.Fatalf("undo stack = %d after a group move, want 1", len(a.undoStack))
	}
	a.undo()
	if !sameAnn(anns(a)[0], beforeRect) || !sameAnn(anns(a)[1], beforePoly) {
		t.Fatalf("after undo = %+v / %+v, want %+v / %+v",
			anns(a)[0], anns(a)[1], beforeRect, beforePoly)
	}
}

// TestGroupMoveDragsOnlyWhenAlreadySelected checks that clicking an unselected
// annotation replaces the selection instead of moving the old one with it.
func TestGroupMoveDragsOnlyWhenAlreadySelected(t *testing.T) {
	a := threeRects(t)
	a.selectOnly(0)
	beforeB := cloneAnn(anns(a)[1])

	dragWith(a, Point{300, 100}, Point{340, 130}, 0)
	wantSelection(t, a, 1)
	if !sameAnn(anns(a)[0], rectAnn("a", 0, 100, 100, 100, 100)) {
		t.Errorf("the previously selected annotation moved too: %+v", anns(a)[0])
	}
	if !nearly(anns(a)[1].CX, beforeB.CX+40, 1e-6) {
		t.Errorf("dragged annotation center x = %v, want %v", anns(a)[1].CX, beforeB.CX+40)
	}
}

// --- sole-selection rule ---------------------------------------------------

// TestMultiSelectionKeepsVertexEditingSingle checks §5's rule that vertex
// editing is a sole-selection gesture: with two annotations selected the same
// Ctrl+click toggles instead of deleting a vertex, and with one selected it
// deletes it again.
func TestMultiSelectionKeepsVertexEditingSingle(t *testing.T) {
	a := newTestApp()
	a.images[0].Anns = []Ann{
		squarePolyAnn("p", 0),               // 100..300 × 100..250
		rectAnn("r", 1, 600, 400, 100, 100), // far away
	}
	a.selectOnly(0)
	a.selectToggle(1)
	wantSelection(t, a, 0, 1)

	clickWith(a, Point{100, 100}, ui.Ctrl)
	if n := len(anns(a)[0].Points); n != 4 {
		t.Fatalf("vertices = %d after Ctrl+click with a multi-selection, want 4", n)
	}
	wantSelection(t, a, 1) // the click toggled the polygon out of the selection

	// Sole selection: the same click deletes the vertex, undoably.
	a.selectOnly(0)
	clickWith(a, Point{100, 100}, ui.Ctrl)
	if n := len(anns(a)[0].Points); n != 3 {
		t.Fatalf("vertices = %d after Ctrl+click on a sole selection, want 3", n)
	}
	if len(a.undoStack) != 1 {
		t.Fatalf("undo stack = %d, want 1", len(a.undoStack))
	}
	a.undo()
	if n := len(anns(a)[0].Points); n != 4 {
		t.Fatalf("vertices = %d after undo, want 4", n)
	}
}

// TestSoleSelectionDragsOneVertex checks that a vertex drag still moves exactly
// one vertex when the polygon is the sole selection.
func TestSoleSelectionDragsOneVertex(t *testing.T) {
	a := newTestApp()
	a.images[0].Anns = []Ann{squarePolyAnn("p", 0)}
	a.selectOnly(0)
	before := cloneAnn(anns(a)[0])

	dragWith(a, Point{100, 100}, Point{80, 70}, 0)
	want := cloneAnn(before)
	want.Points[0] = Point{80, 70}
	syncPolyBounds(&want)
	polyWantPointsNear(t, "after the vertex drag", anns(a)[0], want.Points, 1e-6)
	if len(a.undoStack) != 1 {
		t.Fatalf("undo stack = %d, want 1", len(a.undoStack))
	}
	a.undo()
	if !sameAnn(anns(a)[0], before) {
		t.Fatalf("after undo = %+v, want %+v", anns(a)[0], before)
	}
}

// TestMultiSelectionDrawsNoHandles checks the pixel side of the sole-selection
// rule: the corner handle of a multi-selected annotation is not painted, and
// neither is its fill (§5).
func TestMultiSelectionDrawsNoHandles(t *testing.T) {
	a, tt := tester(t)
	a.bmp = markerImage()
	a.zoom, a.panX, a.panY = 1, 0, 0
	a.images[0].Anns = []Ann{
		rectAnn("a", 0, 400, 400, 100, 100), // corners at 350 and 450
		rectAnn("b", 1, 600, 400, 100, 100),
	}
	cv := canvasBounds(t, tt)
	at := func(p Point) (uint8, uint8, uint8) {
		x, y := screenOf(cv, a, p)
		c := tt.Image().RGBAAt(int(math.Round(float64(x))), int(math.Round(float64(y))))
		return c.R, c.G, c.B
	}

	a.selectOnly(0)
	tt.Frame()
	if r, g, b := at(Point{350, 350}); !(r > 200 && g > 200 && b > 200) {
		t.Fatalf("sole selection: corner pixel = (%d,%d,%d), want a white handle", r, g, b)
	}
	if a.handlesAnn() == nil {
		t.Fatal("sole selection offers no handles")
	}

	a.selectToggle(1)
	tt.Frame()
	if r, g, b := at(Point{350, 350}); r > 200 && g > 200 && b > 200 {
		t.Errorf("multi-selection: corner pixel = (%d,%d,%d), want no handle there", r, g, b)
	}
	if a.handlesAnn() != nil {
		t.Fatal("multi-selection still offers handles")
	}
	// The selection is still visible: the outline is the category color.
	if r, g, b := at(Point{400, 350}); r <= g || r <= b {
		t.Errorf("multi-selected outline = (%d,%d,%d), want the category color", r, g, b)
	}
}

// --- state resets and deep copies ------------------------------------------

// TestImageSwitchClearsSelection checks that indices never carry over to
// another image's annotation list (§5).
func TestImageSwitchClearsSelection(t *testing.T) {
	dir := t.TempDir()
	p1 := filepath.Join(dir, "a.png")
	p2 := filepath.Join(dir, "b.png")
	writePNG(t, p1, 800, 600)
	writePNG(t, p2, 800, 600)

	a, tt := tester(t)
	a.images = []ImageInfo{
		{Path: p1, RelPath: "a.png", Name: "a.png", Width: 800, Height: 600},
		{Path: p2, RelPath: "b.png", Name: "b.png", Width: 800, Height: 600},
	}
	a.images[0].Anns = []Ann{rectAnn("a", 0, 200, 200, 100, 100)}
	a.grid.Key = func(i int) any { return a.images[i].Path }
	a.grid.Selected = &a.selImg
	a.loaded = 0
	a.selectOnly(0)
	tt.Frame()
	wantSelection(t, a, 0)

	a.selImg = 1
	tt.Frame()
	if a.loaded != 1 {
		t.Fatalf("loaded = %d after switching the thumbnail strip, want 1", a.loaded)
	}
	wantSelection(t, a)
}

// TestBatchCommandsDeepCopy checks the phase-2 deep-copy discipline for the two
// new batch commands (docs/phase2-design.md §2.3, §5).
func TestBatchCommandsDeepCopy(t *testing.T) {
	a := undoTestApp(t, 1)
	a.images[0].Anns = []Ann{squarePolyAnn("p1", 0), squarePolyAnn("p2", 1)}
	orig := []Ann{cloneAnn(a.images[0].Anns[0]), cloneAnn(a.images[0].Anns[1])}

	del := cmdBatchDelete(a, 0, []int{0, 1})
	del.Do()
	if n := len(a.images[0].Anns); n != 0 {
		t.Fatalf("batch delete left %d annotations", n)
	}
	del.Undo()
	if n := len(a.images[0].Anns); n != 2 {
		t.Fatalf("batch undo restored %d annotations, want 2", n)
	}
	for i := range orig {
		if !sameAnn(a.images[0].Anns[i], orig[i]) {
			t.Fatalf("restored %d = %+v, want %+v", i, a.images[0].Anns[i], orig[i])
		}
	}

	// An in-place edit of a restored annotation must not reach the command's
	// snapshot, and the next redo/undo pair must restore the original.
	a.images[0].Anns[0].Points[0] = Point{-1, -1}
	if orig[0].Points[0] == (Point{-1, -1}) {
		t.Fatal("the batch snapshot was mutated through the restored annotation")
	}
	del.Do()
	del.Undo()
	if !sameAnn(a.images[0].Anns[0], orig[0]) {
		t.Fatalf("after redo/undo = %+v, want %+v", a.images[0].Anns[0], orig[0])
	}

	// The batch set command copies on write as well.
	before := cloneAnn(a.images[0].Anns[0])
	after := cloneAnn(before)
	after.Cat = 4
	set := cmdBatchSet(a, 0, []annEdit{{index: 0, before: before, after: after}}, "test")
	set.Do()
	a.images[0].Anns[0].Cat = 7
	if after.Cat != 4 {
		t.Fatalf("the batch set's after snapshot followed the live edit: cat = %d", after.Cat)
	}
	set.Undo()
	if !sameAnn(a.images[0].Anns[0], before) {
		t.Fatalf("batch set undo = %+v, want %+v", a.images[0].Anns[0], before)
	}
}
